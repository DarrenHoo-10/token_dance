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
| `tokendance dashboard [--range 24h\|day\|7d\|30d\|all] [--no-open] [--port N]` | Serves the read-only local dashboard on `127.0.0.1` and prints a one-time launch link. Opens a browser only on an interactive desktop session (never over SSH, where it prints the `ssh -L` hint). |
| `tokendance dashboard --json [--range ...] [--scope local]` | Prints one snapshot and exits; no server, no browser. Partial failures exit 4 with `data.partial=true` and per-block `data.errors`. |

Global flags: `--json` (one object on stdout, `schema_version` 1; errors are `{"ok":false,"error":{"code","exit_code","message","hint"}}`), `--no-input`, `--data-dir` (`TOKENDANCE_DATA_DIR`), `--config` (`TOKENDANCE_CONFIG`), `TOKENDANCE_CREDENTIAL_BACKEND`.

Exit codes: `0` done, `1` internal, `2` invalid arguments/config, `3` credentials unavailable, `4` incomplete (e.g. timeout), `5` another collector holds the lock, `6` incompatible schema/config, `130` interrupted.

Safety rules implemented here: every writing command takes the same instance lock as the desktop app; the identity secret is never replaced when data already exists; a database written by a newer schema is never opened for writing; the CLI does not schedule a historical rebuild (`reconstruction::ensure`); `TOKENDANCE_ALLOW_MOCK_KEYSTORE` is not consulted.

Dashboard rules: it opens the database read-only (no lock, no migration, no writes), so it runs next to the desktop app or `run`. The launch token lives in the URL fragment (never sent to a server or logged), is redeemable once for an HttpOnly `SameSite=Strict` session cookie, and is compared in constant time. `Host` must be `127.0.0.1:<port>` or `localhost:<port>` (DNS rebinding), `Origin` and `Sec-Fetch-Site` must be same-origin, only `GET`/`HEAD` and the session exchange exist, and every response carries `Cache-Control: no-store` and a CSP without `unsafe-inline`. Tokens and money are decimal strings; a value nobody reported is `null`, never `0`. Windows follow the UTC+8 metric contract: `24h` uses hour buckets, `day` is the UTC+8 calendar day, `7d`/`30d` use day buckets, `all` uses month buckets. Skills exist at day grain only, so for `24h` they cover the last two calendar days and say so (`skills.window.aligned_with_range=false`). Sessions are not shown because they cannot be summed across hour buckets.

Not yet in this crate: `login`/`logout`/`sync`, the account scope and rankings, subscription quotas, previous-period deltas, Linux system keyring, `service install`, and the XDG state/cache/runtime split for Linux paths (it currently reuses the collector's existing data directory).
