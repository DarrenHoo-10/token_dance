# tokendance CLI

Command-line collector that shares its kernel (`collector-service`) with the desktop app.
Design: [docs/process/cli-tool/README.md](../../../docs/process/cli-tool/README.md).

| Command | Behaviour |
| --- | --- |
| `tokendance init [--credential-backend os\|private-file] [--sources a,b]` | Creates the private data directory, the identity secret and `config.toml`. Linux must choose `private-file` explicitly. |
| `tokendance sources enable\|disable <name>` / `set-path <name> <path>` | Edits `config.toml` (path is checked for readability). Applies the next time `collect` / `run` starts. |
| `tokendance collect --once [--timeout 5m]` | Discovers sources, reads until nothing is due, applies statistics, exits. Never logs in or uploads. |
| `tokendance run [--quiet]` | Foreground collector (4 acquisition workers, a metrics consumer, a 1 s maintenance tick). SIGTERM exits 0, Ctrl+C exits 130, both after a bounded graceful stop. |
| `tokendance doctor` | Read-only diagnostics; never creates, migrates or locks for writing. |

Global flags: `--json` (one object on stdout, `schema_version` 1; errors are `{"ok":false,"error":{"code","exit_code","message","hint"}}`), `--no-input`, `--data-dir` (`TOKENDANCE_DATA_DIR`), `--config` (`TOKENDANCE_CONFIG`), `TOKENDANCE_CREDENTIAL_BACKEND`.

Exit codes: `0` done, `1` internal, `2` invalid arguments/config, `3` credentials unavailable, `4` incomplete (e.g. timeout), `5` another collector holds the lock, `6` incompatible schema/config, `130` interrupted.

Safety rules implemented here: every writing command takes the same instance lock as the desktop app; the identity secret is never replaced when data already exists; a database written by a newer schema is never opened for writing; the CLI does not schedule a historical rebuild (`reconstruction::ensure`); `TOKENDANCE_ALLOW_MOCK_KEYSTORE` is not consulted.

Not yet in this crate: `login`/`logout`/`sync`, Linux system keyring, `service install`, the local `dashboard`, and the XDG state/cache/runtime split for Linux paths (it currently reuses the collector's existing data directory).
