# Security

Keep passwords, tokens, private keys, personal exports, production logs, database
dumps, and one-off server diagnostics outside this repository. Runtime
credentials belong in a secret manager or a restricted file/environment on the
machine that needs them. Examples must use fictitious identities and placeholders.

## Before committing

Install [Gitleaks 8.30.1](https://github.com/gitleaks/gitleaks/releases/tag/v8.30.1),
then enable this repository's local hook:

```sh
git config core.hooksPath .githooks
gitleaks git . --pre-commit --staged --redact=100 --ignore-gitleaks-allow
```

The hook scans staged changes. CI runs once on push and scans only commits in
the `before..after` range, including intermediate commits whose changes were
subsequently deleted. It does not rescan all history or run a duplicate PR scan.
New feature branches scan from their merge base with the default branch; branch
creation at the default branch's tip and manual runs scan only the tip commit.
Deleted branches are skipped. Missing commit objects fail the check rather than
silently skipping commits. Fetching full history still allows these ranges to be
resolved, but does not make the scan a full-history scan.
GitHub secret scanning and push
protection supplement these checks; ordinary passwords also require the custom
rules in `.gitleaks.toml`. Run `python tools/security/test_secret_scan.py` when
changing them. `GITLEAKS_BIN` can point to the installed executable.

`.gitleaksignore` contains only reviewed, exact historical finding fingerprints.
Do not exclude entire test directories or automatically accept a scanner report
as a new baseline. Review additions to the scanner configuration and baseline.
`.gitignore` does not protect files that are already tracked or force-added.

## If a credential is exposed

1. Revoke or rotate it immediately and update dependent protected configuration.
   Verify service health and reject the old credential. Revoke affected login
   sessions when an account password is exposed.
2. Remove the sensitive content from every affected branch and tag. Review
   releases, artifacts, logs, and pull requests separately. A deletion commit
   alone does not remove Git history.
3. Coordinate a fresh clone after a history rewrite. Do not merge old history
   back into the cleaned repository. Export and scan any uncommitted changes
   before applying them to a fresh clone.
4. Request GitHub Support removal of cached views and affected pull-request refs.
   Forks and existing clones cannot be recalled; rotation remains essential.

Please use GitHub's private vulnerability reporting when available. Do not post
credentials, account details, or production logs in public issues or pull requests.
