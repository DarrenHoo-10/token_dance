# Team page parity test release · 2026-09-19

- URL: https://nexorai.com.cn/token-dance-test/teams?range=30d
- Build branch: `release` (clean worktree)
- Build commit: `3a29c60ec20a1efa763478b8c8e0eea8e9ae9de6`
- Built at: `2026-09-19 11:55:52 UTC`
- Activated at: `2026-09-19 11:56:20 UTC`
- Package SHA-256: `2d0f306aae2a4b1205eef13f7869e9221f21bc2cc719017caa262ef57ef9eee3`
- Release path: `/opt/token-dance-test/releases/20260919-115552-3a29c60e`
- Source implementation: `3a29c60` on `feat/team-page-parity` (from `origin/main` `abaf076`)
- Previous test release: `/opt/token-dance-test/releases/20260919-114125-3b1507d5`
- Database backup: `/var/backups/token-dance-test/20260919-115620`

This release isolates leftover product CSS so the sky team workspace matches the tokenboard-sky mock at 1920: tab padding and underline, switch radius, invite-dialog margins, and the hidden chart keyboard. Tokens trend uses the mock area/readout chart. Decorative banner art stays hidden. Join remains always-on sharing (three switch rows on+disabled). Real APIs and `tokendance_dev` are unchanged.

## Verification

Focused frontend tests (`teams-analysis`, `teams-i18n`, `teams-invitation`, `teams-join`, `teams-create`, `team-cost-format`) passed 40/40 on the implementation commit. The release worktree built the `/token-dance-test/` frontend and Linux amd64 API / Worker / migrate / checkproviders binaries from a clean tree.

Deployment verified the package checksum, confirmed `tokendance_dev`, backed up the test database, applied migrations, switched the test service, and passed HTTPS `/token-dance-test/readyz`. Published `build-info.json` matches branch `release` and the full SHA above. Production `/token-dance/readyz` stayed 200 and was not redeployed.

Live Playwright checks at 1920×1080 against 测试团队 (`?range=30d`): workspace 1440 / padding `18px 32px 22px`; banner 1376×197 / padding `32px 34px`; h1 `36px` / `750` / `-1.2px` / margin `8px 0 9px`; tabbar 53 / active tab padding `16px 3px 19px`; invite dialog 540×519; three sharing switches 37×21, padding `3px`, radius `20px`, lime `#a3d84e`, on+disabled; no banner art.

No pull request or remote push was made as part of this local test deployment; no GitHub CI result is claimed.
