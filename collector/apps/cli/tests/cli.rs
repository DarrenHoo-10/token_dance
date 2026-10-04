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
        let mut cmd = self.plain(args);
        cmd.arg("--json");
        cmd
    }

    /// Same isolation, without `--json` (the dashboard's web mode is the non-JSON mode).
    fn plain(&self, args: &[&str]) -> Command {
        let mut cmd = Command::new(env!("CARGO_BIN_EXE_tokendance"));
        cmd.env_clear()
            .env("PATH", std::env::var_os("PATH").unwrap_or_default())
            .env("HOME", self.home())
            .env("USERPROFILE", self.home())
            .env("XDG_CONFIG_HOME", self.dir.path().join("xdg-config"))
            .env("XDG_DATA_HOME", self.dir.path().join("xdg-data"))
            .arg("--data-dir")
            .arg(self.data())
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

// ---------------------------------------------------------------- dashboard --json

fn collected_env() -> Env {
    let env = Env::new();
    env.init();
    env.write_codex_session();
    assert_eq!(env.run(&["collect", "--once"]).0, 0);
    env
}

#[test]
fn dashboard_json_before_any_data_reports_no_data_without_touching_disk() {
    let env = Env::new();
    let (code, json) = env.run(&["dashboard"]);
    assert_eq!(code, 0, "{json}");
    assert_eq!(json["command"], "dashboard");
    assert_eq!(json["data"]["state"], "no_data");
    assert_eq!(json["data"]["scope"], "local");
    assert_eq!(json["data"]["business_timezone"], "Asia/Shanghai");
    assert!(json["data"]["summary"].is_null());
    assert!(json["data"]["hint"]
        .as_str()
        .unwrap()
        .contains("tokendance init"));
    assert!(
        !env.data().exists(),
        "a query must not create the data directory"
    );
}

#[test]
fn dashboard_json_reports_collected_usage_with_decimal_strings() {
    let env = collected_env();
    let db = env.data().join("tokendance-events.sqlite3");
    let before = fs::read(&db).unwrap();
    for range in ["24h", "day", "7d", "30d", "all"] {
        let (code, json) = env.run(&["dashboard", "--range", range]);
        assert_eq!(code, 0, "{range}: {json}");
        let data = &json["data"];
        assert_eq!(data["state"], "ok");
        assert_eq!(data["range"], range);
        assert_eq!(data["partial"], false);
        assert_eq!(data["summary"]["tokens"]["value"], "15", "{range}: {data}");
        assert_eq!(data["summary"]["input_context_tokens"]["value"], "10");
        // The fixture reports no messages or code lines: unknown must not be shown as zero.
        assert!(
            data["summary"]["messages"].is_null()
                && data["summary"]["code_generated_lines"].is_null(),
            "{data}"
        );
        assert_eq!(data["summary"]["output_tokens"]["value"], "5");
        assert_eq!(data["agents"][0]["agent"], "codex");
        assert_eq!(data["agents"][0]["tokens"]["value"], "15");
        let points = data["trend"]["points"].as_array().unwrap();
        let total: i64 = points
            .iter()
            .map(|p| p["tokens"].as_str().unwrap().parse::<i64>().unwrap())
            .sum();
        assert_eq!(total, 15, "{range}: trend must add up to the summary");
        assert!(
            data["window"]["start_ms"].as_i64().unwrap()
                < data["window"]["end_ms"].as_i64().unwrap()
        );
        assert_eq!(data["collection"]["process"]["state"], "stopped");
        assert!(
            data["collection"]["database"]["events"]
                .as_str()
                .unwrap()
                .parse::<i64>()
                .unwrap()
                >= 1,
            "{data}"
        );
        assert_eq!(data["calendar"]["days"].as_array().unwrap().len(), 1);
    }
    let (_, json) = env.run(&["dashboard", "--range", "24h"]);
    assert_eq!(json["data"]["trend"]["grain"], "hour");
    assert_eq!(
        json["data"]["trend"]["points"].as_array().unwrap().len(),
        24
    );
    assert_eq!(
        env.run(&["dashboard", "--range", "day"]).1["data"]["window"]["summary_grain"],
        "day"
    );
    assert_eq!(
        fs::read(&db).unwrap(),
        before,
        "querying must not change the database"
    );
}

#[test]
fn dashboard_json_rejects_bad_scope_and_range() {
    let env = collected_env();
    let (code, json) = env.run(&["dashboard", "--range", "today"]);
    assert_eq!((code, error_code(&json)), (2, "INVALID_INPUT"));
    let (code, json) = env.run(&["dashboard", "--scope", "account"]);
    assert_eq!((code, error_code(&json)), (3, "CREDENTIALS_UNAVAILABLE"));
    assert_eq!(env.run(&["dashboard", "--scope", "nope"]).0, 2);
}

#[test]
fn dashboard_json_partial_failure_is_exit_4_with_block_errors() {
    let env = collected_env();
    rusqlite::Connection::open(env.data().join("tokendance-events.sqlite3"))
        .unwrap()
        .execute(
            "ALTER TABLE skill_metrics RENAME TO skill_metrics_moved",
            [],
        )
        .unwrap();
    let (code, json) = env.run(&["dashboard"]);
    assert_eq!((code, error_code(&json)), (4, "INCOMPLETE"), "{json}");
    let data = &json["data"];
    assert_eq!(data["partial"], true);
    assert!(data["errors"]["skills"]["message"].is_string());
    assert!(data["skills"].is_null());
    // The other blocks are still delivered.
    assert_eq!(data["summary"]["tokens"]["value"], "15");
}

#[test]
fn dashboard_json_refuses_a_newer_schema() {
    let env = collected_env();
    rusqlite::Connection::open(env.data().join("tokendance-events.sqlite3"))
        .unwrap()
        .execute("UPDATE schema_meta SET schema_version=99 WHERE id=1", [])
        .unwrap();
    let (code, json) = env.run(&["dashboard"]);
    assert_eq!((code, error_code(&json)), (6, "INCOMPATIBLE"), "{json}");
}

#[test]
fn dashboard_works_while_a_collector_holds_the_lock() {
    let env = collected_env();
    let paths = collector_service::AppPaths::for_root(env.data());
    let _held = collector_service::InstanceLock::acquire(&paths).unwrap();
    let (code, json) = env.run(&["dashboard"]);
    assert_eq!(code, 0, "{json}");
    assert_eq!(json["data"]["collection"]["process"]["state"], "running");
}

// ---------------------------------------------------------------- dashboard web server

struct Web {
    child: std::process::Child,
    port: u16,
    token: String,
}

struct Reply {
    status: u16,
    headers: String,
    body: String,
}

impl Reply {
    fn header(&self, name: &str) -> Option<&str> {
        self.headers.lines().find_map(|l| {
            let (k, v) = l.split_once(':')?;
            k.eq_ignore_ascii_case(name).then(|| v.trim())
        })
    }
}

impl Web {
    fn start(env: &Env) -> Self {
        use std::io::{BufRead, BufReader};
        let mut child = env
            .plain(&["dashboard", "--no-open"])
            .stdout(Stdio::piped())
            .stderr(Stdio::piped())
            .spawn()
            .unwrap();
        let mut lines = BufReader::new(child.stdout.take().unwrap());
        let mut first = String::new();
        lines.read_line(&mut first).unwrap();
        let url = first.split_whitespace().last().unwrap().to_string();
        let rest = url.strip_prefix("http://127.0.0.1:").expect("loopback URL");
        let (port, token) = rest.split_once("/#launch=").expect("launch fragment");
        // Keep draining stdout so the child never blocks on a full pipe.
        std::thread::spawn(move || {
            let mut sink = String::new();
            while lines.read_line(&mut sink).unwrap_or(0) > 0 {
                sink.clear();
            }
        });
        Self {
            child,
            port: port.parse().unwrap(),
            token: token.to_string(),
        }
    }

    fn request(&self, method: &str, path: &str, headers: &[(&str, &str)], body: &str) -> Reply {
        use std::io::{Read, Write};
        let mut stream = std::net::TcpStream::connect(("127.0.0.1", self.port)).unwrap();
        stream
            .set_read_timeout(Some(Duration::from_secs(10)))
            .unwrap();
        let mut text = format!("{method} {path} HTTP/1.1\r\nConnection: close\r\n");
        if !headers.iter().any(|(k, _)| k.eq_ignore_ascii_case("host")) {
            text.push_str(&format!("Host: 127.0.0.1:{}\r\n", self.port));
        }
        for (k, v) in headers {
            text.push_str(&format!("{k}: {v}\r\n"));
        }
        text.push_str(&format!("Content-Length: {}\r\n\r\n{body}", body.len()));
        stream.write_all(text.as_bytes()).unwrap();
        let mut raw = Vec::new();
        let _ = stream.read_to_end(&mut raw);
        let raw = String::from_utf8_lossy(&raw).to_string();
        let (head, body) = raw.split_once("\r\n\r\n").unwrap_or((raw.as_str(), ""));
        let status = head
            .split_whitespace()
            .nth(1)
            .and_then(|s| s.parse().ok())
            .unwrap_or(0);
        Reply {
            status,
            headers: head.to_string(),
            body: body.to_string(),
        }
    }

    fn redeem(&self, token: &str) -> Reply {
        self.request(
            "POST",
            "/api/v1/session",
            &[("Content-Type", "application/json")],
            &format!("{{\"token\":\"{token}\"}}"),
        )
    }

    fn cookie(&self, reply: &Reply) -> String {
        let set = reply.header("Set-Cookie").expect("session cookie");
        set.split(';').next().unwrap().to_string()
    }
}

impl Drop for Web {
    fn drop(&mut self) {
        let _ = self.child.kill();
        let _ = self.child.wait();
    }
}

#[test]
fn web_serves_static_assets_with_hardening_headers_and_no_data() {
    let env = collected_env();
    let web = Web::start(&env);
    for path in ["/", "/app.js", "/styles.css", "/logo.png"] {
        let r = web.request("GET", path, &[], "");
        assert_eq!(r.status, 200, "{path}");
        assert_eq!(r.header("Cache-Control"), Some("no-store"));
        assert_eq!(r.header("X-Content-Type-Options"), Some("nosniff"));
        assert_eq!(r.header("Referrer-Policy"), Some("no-referrer"));
        let csp = r.header("Content-Security-Policy").unwrap();
        assert!(
            csp.contains("default-src 'none'") && csp.contains("frame-ancestors 'none'"),
            "{csp}"
        );
        assert!(
            !csp.contains("unsafe-inline") && !csp.contains("unsafe-eval"),
            "{csp}"
        );
    }
    let page = web.request("GET", "/", &[], "");
    assert!(
        !page.body.contains("<script>") && !page.body.contains(" style="),
        "the CSP forbids inline code"
    );
    assert!(!page.body.contains(&web.token) && !page.headers.contains(&web.token));
}

#[test]
fn web_requires_the_launch_token_exactly_once() {
    let env = collected_env();
    let web = Web::start(&env);
    assert_eq!(
        web.request("GET", "/api/v1/snapshot", &[], "").status,
        401,
        "no cookie"
    );
    let bad = web.redeem("00");
    assert_eq!(
        (bad.status, bad.body.contains("BAD_TOKEN")),
        (401, true),
        "{}",
        bad.body
    );
    let wrong = web.redeem(&"a".repeat(web.token.len()));
    assert_eq!(wrong.status, 401);

    let ok = web.redeem(&web.token);
    assert_eq!(ok.status, 200, "{}", ok.body);
    let set = ok.header("Set-Cookie").unwrap();
    assert!(
        set.contains("HttpOnly") && set.contains("SameSite=Strict") && set.contains("Path=/"),
        "{set}"
    );
    let cookie = web.cookie(&ok);
    assert!(
        !ok.body.contains(&cookie[cookie.find('=').unwrap() + 1..]),
        "session secret must not be in the body"
    );

    // The link cannot be replayed, not even by someone who knows it.
    let replay = web.redeem(&web.token);
    assert_eq!(replay.status, 401);
    assert!(
        replay.body.contains("TOKEN_ALREADY_USED"),
        "{}",
        replay.body
    );

    let data = web.request(
        "GET",
        "/api/v1/snapshot?range=7d",
        &[("Cookie", &cookie)],
        "",
    );
    assert_eq!(data.status, 200, "{}", data.body);
    let json: Value = serde_json::from_str(&data.body).unwrap();
    assert_eq!(json["data"]["summary"]["tokens"]["value"], "15");
    assert_eq!(data.header("Cache-Control"), Some("no-store"));
    assert_eq!(
        web.request(
            "GET",
            "/api/v1/snapshot",
            &[("Cookie", "td_session=nope")],
            ""
        )
        .status,
        401
    );
    let bad_range = web.request(
        "GET",
        "/api/v1/snapshot?range=zzz",
        &[("Cookie", &cookie)],
        "",
    );
    assert_eq!(bad_range.status, 400);
}

#[test]
fn web_rejects_foreign_hosts_origins_and_write_methods() {
    let env = collected_env();
    let web = Web::start(&env);
    let cookie = web.cookie(&web.redeem(&web.token));
    // DNS rebinding: the browser sends the attacker's name in Host.
    for host in [
        "evil.example",
        &format!("evil.example:{}", web.port),
        "127.0.0.1",
        "127.0.0.1:1",
    ] {
        let r = web.request(
            "GET",
            "/api/v1/snapshot",
            &[("Host", host), ("Cookie", &cookie)],
            "",
        );
        assert_eq!(r.status, 421, "Host {host}");
    }
    assert_eq!(
        web.request("GET", "/", &[("Host", "evil.example")], "")
            .status,
        421,
        "even static pages"
    );
    // The literal name `localhost` is a loopback name and is allowed.
    let local = format!("localhost:{}", web.port);
    assert_eq!(web.request("GET", "/", &[("Host", &local)], "").status, 200);
    // Cross-origin pages cannot read data even with the cookie.
    for origin in [
        "http://evil.example",
        "https://127.0.0.1",
        "null",
        "http://127.0.0.1:1",
    ] {
        let r = web.request(
            "GET",
            "/api/v1/snapshot",
            &[("Origin", origin), ("Cookie", &cookie)],
            "",
        );
        assert_eq!(r.status, 403, "Origin {origin}");
    }
    let r = web.request(
        "GET",
        "/api/v1/snapshot",
        &[("Sec-Fetch-Site", "cross-site"), ("Cookie", &cookie)],
        "",
    );
    assert_eq!(r.status, 403);
    let same = format!("http://127.0.0.1:{}", web.port);
    let r = web.request(
        "GET",
        "/api/v1/snapshot",
        &[
            ("Origin", &same),
            ("Sec-Fetch-Site", "same-origin"),
            ("Cookie", &cookie),
        ],
        "",
    );
    assert_eq!(r.status, 200);
    // Read-only: nothing but GET/HEAD/the session exchange exists.
    for method in ["PUT", "DELETE", "PATCH"] {
        assert_eq!(
            web.request(method, "/api/v1/snapshot", &[("Cookie", &cookie)], "")
                .status,
            405,
            "{method}"
        );
    }
    assert_eq!(
        web.request("POST", "/api/v1/snapshot", &[("Cookie", &cookie)], "{}")
            .status,
        404
    );
    assert_eq!(
        web.request(
            "GET",
            "/api/v1/../../etc/passwd",
            &[("Cookie", &cookie)],
            ""
        )
        .status,
        404
    );
}

#[test]
fn web_leaves_the_database_untouched_and_stops_on_ctrl_c() {
    let env = collected_env();
    let db = env.data().join("tokendance-events.sqlite3");
    let before = fs::read(&db).unwrap();
    let mut web = Web::start(&env);
    let cookie = web.cookie(&web.redeem(&web.token));
    for range in ["24h", "day", "7d", "30d", "all"] {
        assert_eq!(
            web.request(
                "GET",
                &format!("/api/v1/snapshot?range={range}"),
                &[("Cookie", &cookie)],
                ""
            )
            .status,
            200
        );
    }
    assert_eq!(fs::read(&db).unwrap(), before);
    // The lock is not taken: a collector can still start next to the dashboard.
    assert_eq!(env.run(&["collect", "--once"]).0, 0);
    signal(&web.child, libc::SIGINT);
    let status = web.child.wait().unwrap();
    assert_eq!(status.code(), Some(130));
}

#[test]
fn web_refuses_to_start_on_a_newer_database_and_on_a_busy_port() {
    let env = collected_env();
    let web = Web::start(&env);
    let out = env
        .plain(&["dashboard", "--no-open", "--port", &web.port.to_string()])
        .output()
        .unwrap();
    assert_eq!(
        out.status.code(),
        Some(4),
        "{}",
        String::from_utf8_lossy(&out.stderr)
    );
    drop(web);
    rusqlite::Connection::open(env.data().join("tokendance-events.sqlite3"))
        .unwrap()
        .execute("UPDATE schema_meta SET schema_version=99 WHERE id=1", [])
        .unwrap();
    let out = env.plain(&["dashboard", "--no-open"]).output().unwrap();
    assert_eq!(out.status.code(), Some(6));
}
