//! End-to-end tests of the `tokendance` binary against an isolated HOME and data directory.
#![cfg(unix)]

use std::fs;
use std::path::PathBuf;
use std::process::{Command, Output, Stdio};
use std::time::{Duration, Instant, SystemTime, UNIX_EPOCH};

use serde_json::Value;

struct Env {
    dir: tempfile::TempDir,
}

impl Env {
    fn new() -> Self {
        let dir = tempfile::tempdir().unwrap();
        fs::create_dir_all(dir.path().join("home")).unwrap();
        Self { dir }
    }

    fn home(&self) -> PathBuf {
        self.dir.path().join("home")
    }

    fn data(&self) -> PathBuf {
        self.dir.path().join("data")
    }

    fn command(&self, args: &[&str]) -> Command {
        let mut cmd = Command::new(env!("CARGO_BIN_EXE_tokendance"));
        cmd.env_clear()
            .env("PATH", std::env::var_os("PATH").unwrap_or_default())
            .env("HOME", self.home())
            .env("USERPROFILE", self.home())
            .env("XDG_CONFIG_HOME", self.dir.path().join("xdg-config"))
            .env("XDG_DATA_HOME", self.dir.path().join("xdg-data"))
            .arg("--data-dir")
            .arg(self.data())
            .arg("--json")
            .args(args);
        cmd
    }

    fn run(&self, args: &[&str]) -> (i32, Value) {
        let out = self.command(args).output().unwrap();
        (out.status.code().unwrap_or(-1), stdout_json(&out))
    }

    fn init(&self) {
        let (code, json) = self.run(&["init", "--credential-backend", "private-file"]);
        assert_eq!(code, 0, "{json}");
    }

    /// A Codex session with one request dated now (first collection only admits today's events).
    fn write_codex_session(&self) {
        let dir = self.home().join(".codex/sessions/2026/01/01");
        fs::create_dir_all(&dir).unwrap();
        let now = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .unwrap()
            .as_millis() as u64;
        let header =
            serde_json::json!({"type":"session_meta","timestamp":now,"payload":{"id":"s-1"}});
        let usage = serde_json::json!({
            "type":"event_msg","timestamp":now,
            "payload":{"type":"token_count","info":{
                "last_token_usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15}
            }}
        });
        fs::write(
            dir.join("rollout-s-1.jsonl"),
            format!("{header}\n{usage}\n"),
        )
        .unwrap();
    }

    fn events(&self) -> Option<i64> {
        let path = self.data().join("tokendance-events.sqlite3");
        if !path.exists() {
            return None;
        }
        let conn =
            rusqlite::Connection::open_with_flags(path, rusqlite::OpenFlags::SQLITE_OPEN_READ_ONLY)
                .ok()?;
        conn.query_row(
            "SELECT count(*) FROM events WHERE delete_at IS NULL",
            [],
            |r| r.get(0),
        )
        .ok()
    }
}

fn stdout_json(out: &Output) -> Value {
    serde_json::from_slice(&out.stdout).unwrap_or_else(|e| {
        panic!(
            "stdout is not JSON ({e}): {}\nstderr: {}",
            String::from_utf8_lossy(&out.stdout),
            String::from_utf8_lossy(&out.stderr)
        )
    })
}

fn error_code(json: &Value) -> &str {
    json["error"]["code"].as_str().unwrap_or("")
}

#[test]
fn init_creates_private_state_and_is_repeatable() {
    use std::os::unix::fs::PermissionsExt;
    let env = Env::new();
    let (code, json) = env.run(&["init", "--credential-backend", "private-file"]);
    assert_eq!(code, 0, "{json}");
    assert_eq!(json["schema_version"], 1);
    assert_eq!(json["ok"], true);
    assert_eq!(json["data"]["credential_backend"], "private-file");
    let key = env.data().join("identity.key");
    assert_eq!(
        fs::metadata(&key).unwrap().permissions().mode() & 0o777,
        0o600
    );
    assert_eq!(
        fs::metadata(env.data()).unwrap().permissions().mode() & 0o077,
        0
    );
    let first = fs::read(&key).unwrap();
    let id = fs::read_to_string(env.data().join("installation-id")).unwrap();

    // Running init again keeps the same identity.
    let (code, _) = env.run(&["init"]);
    assert_eq!(code, 0);
    assert_eq!(fs::read(&key).unwrap(), first);
    assert_eq!(
        fs::read_to_string(env.data().join("installation-id")).unwrap(),
        id
    );
    // Credentials never reach the config file or the output.
    let config = fs::read_to_string(env.data().join("config.toml")).unwrap();
    assert!(
        config.contains("private-file") && !config.contains("key"),
        "{config}"
    );
}

#[cfg(target_os = "linux")]
#[test]
fn init_on_linux_requires_an_explicit_credential_backend() {
    let env = Env::new();
    let (code, json) = env.run(&["init"]);
    assert_eq!(code, 2, "{json}");
    assert_eq!(error_code(&json), "INVALID_INPUT");
    assert!(json["error"]["hint"]
        .as_str()
        .unwrap()
        .contains("private-file"));
    assert!(!env.data().join("identity.key").exists());
}

#[test]
fn init_rejects_unknown_backend_and_source() {
    let env = Env::new();
    assert_eq!(env.run(&["init", "--credential-backend", "mock"]).0, 2);
    assert_eq!(
        env.run(&[
            "init",
            "--credential-backend",
            "private-file",
            "--sources",
            "nope"
        ])
        .0,
        2
    );
    assert!(!env.data().join("identity.key").exists());
}

#[test]
fn sources_enable_disable_and_set_path() {
    let env = Env::new();
    env.init();
    let (code, json) = env.run(&["sources", "disable", "codex"]);
    assert_eq!(code, 0, "{json}");
    assert_eq!(json["data"]["source"]["enabled"], false);
    let config = fs::read_to_string(env.data().join("config.toml")).unwrap();
    assert!(
        config.contains("[sources.codex]") && config.contains("enabled = false"),
        "{config}"
    );
    assert_eq!(
        env.run(&["sources", "enable", "codex"]).1["data"]["source"]["enabled"],
        true
    );

    let custom = env.dir.path().join("custom-codex");
    fs::create_dir_all(&custom).unwrap();
    let (code, json) = env.run(&["sources", "set-path", "codex", custom.to_str().unwrap()]);
    assert_eq!(code, 0, "{json}");
    assert_eq!(json["data"]["source"]["path_origin"], "configured");

    let (code, json) = env.run(&["sources", "set-path", "codex", "/definitely/not/here"]);
    assert_eq!((code, error_code(&json)), (2, "INVALID_INPUT"));
    let (code, _) = env.run(&["sources", "enable", "nope"]);
    assert_eq!(code, 2);
    // The rejected path was not saved.
    assert!(!fs::read_to_string(env.data().join("config.toml"))
        .unwrap()
        .contains("not/here"));
}

#[test]
fn collect_once_stores_events_and_is_idempotent() {
    let env = Env::new();
    env.init();
    env.write_codex_session();
    let (code, json) = env.run(&["collect", "--once"]);
    assert_eq!(code, 0, "{json}");
    assert_eq!(json["data"]["complete"], true);
    assert_eq!(json["data"]["failures"], 0);
    assert!(
        json["data"]["database"]["events"].as_i64().unwrap() >= 1,
        "{json}"
    );
    assert_eq!(
        json["data"]["database"]["metric_tasks_pending"], 0,
        "{json}"
    );
    let events = env.events().unwrap();

    // Re-reading the same source must not double count.
    let (code, json) = env.run(&["collect", "--once"]);
    assert_eq!(code, 0, "{json}");
    assert_eq!(env.events().unwrap(), events);

    let (code, json) = env.run(&["doctor"]);
    assert_eq!(code, 0, "{json}");
    assert_eq!(json["data"]["healthy"], true);
}

#[test]
fn disabled_source_is_not_collected() {
    let env = Env::new();
    env.init();
    env.write_codex_session();
    assert_eq!(env.run(&["sources", "disable", "codex"]).0, 0);
    let (code, json) = env.run(&["collect", "--once"]);
    assert_eq!(code, 0, "{json}");
    assert_eq!(env.events().unwrap_or(0), 0);
}

#[test]
fn collect_before_init_reports_missing_credentials() {
    let env = Env::new();
    let (code, json) = env.run(&["collect", "--once"]);
    assert_eq!(
        (code, error_code(&json)),
        (3, "CREDENTIALS_UNAVAILABLE"),
        "{json}"
    );
}

#[test]
fn write_commands_yield_to_a_running_collector_but_doctor_still_works() {
    let env = Env::new();
    env.init();
    let paths = collector_service::AppPaths::for_root(env.data());
    let _held = collector_service::InstanceLock::acquire(&paths).unwrap();
    for args in [
        &["collect", "--once"][..],
        &["sources", "disable", "codex"],
        &["init"],
    ] {
        let (code, json) = env.run(args);
        assert_eq!(
            (code, error_code(&json)),
            (5, "INSTANCE_IN_USE"),
            "{args:?}: {json}"
        );
    }
    let (code, json) = env.run(&["doctor"]);
    assert_eq!(code, 0, "{json}");
    let checks = json["data"]["checks"].as_array().unwrap();
    let process = checks
        .iter()
        .find(|c| c["id"] == "collector_process")
        .unwrap();
    assert!(
        process["detail"].as_str().unwrap().starts_with("running"),
        "{process}"
    );
}

#[test]
fn doctor_flags_a_key_file_that_others_can_read() {
    use std::os::unix::fs::PermissionsExt;
    let env = Env::new();
    env.init();
    let key = env.data().join("identity.key");
    fs::set_permissions(&key, fs::Permissions::from_mode(0o644)).unwrap();
    let (code, json) = env.run(&["doctor"]);
    assert_eq!(
        (code, error_code(&json)),
        (3, "CREDENTIALS_UNAVAILABLE"),
        "{json}"
    );
    assert_eq!(json["data"]["healthy"], false);
    let text = json.to_string();
    assert!(
        !text.contains(&format!("{:?}", fs::read(&key).unwrap())),
        "key bytes leaked"
    );
    // collect refuses too rather than using a key that others could read.
    assert_eq!(env.run(&["collect", "--once"]).0, 3);
}

#[test]
fn doctor_on_an_empty_environment_is_read_only() {
    let env = Env::new();
    let (code, json) = env.run(&["doctor"]);
    assert_eq!(code, 0, "{json}");
    assert!(
        !env.data().exists(),
        "doctor must not create the data directory"
    );
}

#[test]
fn newer_database_schema_is_never_written() {
    let env = Env::new();
    env.init();
    env.write_codex_session();
    assert_eq!(env.run(&["collect", "--once"]).0, 0);
    let db = env.data().join("tokendance-events.sqlite3");
    rusqlite::Connection::open(&db)
        .unwrap()
        .execute("UPDATE schema_meta SET schema_version=99 WHERE id=1", [])
        .unwrap();
    let before = fs::read(&db).unwrap();
    let (code, json) = env.run(&["collect", "--once"]);
    assert_eq!((code, error_code(&json)), (6, "INCOMPATIBLE"), "{json}");
    assert_eq!(env.run(&["doctor"]).0, 6);
    assert_eq!(fs::read(&db).unwrap(), before, "database bytes changed");
}

#[test]
fn argument_errors_exit_2_and_are_json_when_asked() {
    let env = Env::new();
    let (code, json) = env.run(&["collect"]);
    assert_eq!((code, error_code(&json)), (2, "INVALID_INPUT"), "{json}");
    assert_eq!(env.run(&["collect", "--once", "--timeout", "0s"]).0, 2);
    let plain = Command::new(env!("CARGO_BIN_EXE_tokendance"))
        .arg("collect")
        .output()
        .unwrap();
    assert_eq!(plain.status.code(), Some(2));
    assert!(plain.stdout.is_empty(), "plain-mode errors go to stderr");
}

#[test]
fn invalid_config_is_rejected_with_exit_2() {
    let env = Env::new();
    env.init();
    fs::write(
        env.data().join("config.toml"),
        "credential_token = \"abc\"\n",
    )
    .unwrap();
    let (code, json) = env.run(&["collect", "--once"]);
    assert_eq!((code, error_code(&json)), (2, "INVALID_INPUT"), "{json}");
}

fn wait_for(what: &str, mut condition: impl FnMut() -> bool) {
    let deadline = Instant::now() + Duration::from_secs(60);
    while Instant::now() < deadline {
        if condition() {
            return;
        }
        std::thread::sleep(Duration::from_millis(200));
    }
    panic!("timed out waiting for {what}");
}

fn signal(child: &std::process::Child, sig: libc::c_int) {
    // SAFETY: plain kill(2) on a child we own.
    assert_eq!(unsafe { libc::kill(child.id() as libc::pid_t, sig) }, 0);
}

fn spawn_run(env: &Env) -> std::process::Child {
    env.command(&["run", "--quiet"])
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .unwrap()
}

#[test]
fn run_collects_and_stops_cleanly_on_sigterm() {
    let env = Env::new();
    env.init();
    env.write_codex_session();
    let child = spawn_run(&env);
    wait_for("the first events", || env.events().unwrap_or(0) >= 1);
    // A second collector cannot start while this one holds the lock.
    assert_eq!(env.run(&["collect", "--once"]).0, 5);
    signal(&child, libc::SIGTERM);
    let out = child.wait_with_output().unwrap();
    assert_eq!(
        out.status.code(),
        Some(0),
        "{}",
        String::from_utf8_lossy(&out.stderr)
    );
    let json = stdout_json(&out);
    assert_eq!(json["data"]["clean_shutdown"], true, "{json}");
    assert_eq!(json["data"]["stopped_by"], "sigterm");
    // The lock is free again.
    assert_eq!(env.run(&["collect", "--once"]).0, 0);
}

#[test]
fn run_exits_130_on_ctrl_c() {
    let env = Env::new();
    env.init();
    env.write_codex_session();
    let child = spawn_run(&env);
    wait_for("the first events", || env.events().unwrap_or(0) >= 1);
    signal(&child, libc::SIGINT);
    let out = child.wait_with_output().unwrap();
    assert_eq!(out.status.code(), Some(130));
    let json = stdout_json(&out);
    assert_eq!(error_code(&json), "INTERRUPTED");
    assert_eq!(json["data"]["clean_shutdown"], true);
}
