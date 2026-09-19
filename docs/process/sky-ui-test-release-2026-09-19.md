# Sky UI test release · 2026-09-19

- URL: https://nexorai.com.cn/token-dance-test/teams
- Build branch: `release` (clean worktree)
- Build commit: `f45252c1fee2448efed6c6bb9d0ca0135e114147`
- Built at: `2026-09-19 07:53:57 UTC`
- Activated at: `2026-09-19 07:55:05 UTC`
- Package SHA-256: `4741242ceac9140f47cf921663ad6f3c00f49e85544275dd132d5318f345bea4`
- Source implementation commits: `4086195` and `34db2a9` on `codex/sky-ui-implementation`.

The release integrates the approved application UI while retaining the test branch's public-home cache, avatar handling and backend behavior. The main-based implementation continues to use its existing authenticated leaderboard API and avatar persistence helper. No production or desktop release was performed.

## Verification

TypeScript, 186 tests across 30 test files, the base-path frontend build and all four Linux amd64 service binaries passed. A browser checked the compiled release at `/token-dance-test/` with synthetic responses: all five pages, 320–1536 px, calendar navigation, mobile navigation, date range, invitation dialog, sharing versioning/dependent flags, unsaved profile preservation and English settings. No page overflow or runtime errors were found.

The deployment tool verified the package checksum, checked the previous release to prevent a concurrent overwrite, confirmed the `tokendance_dev` target, backed up the test database, checked migrations/schema, switched the test service, and passed readiness. The server retains the rollback target and backup record. Nginx syntax validation passed with pre-existing duplicate-host warnings.

Live post-deployment checks passed: HTTPS readiness; anonymous team API returns 401; sky assets load; real test-account login; ten personal metrics; team analytics, members and sharing settings; deep-link refresh; mobile overflow and browser runtime checks. Live team data was not edited during verification. Credentials, session responses and private screenshots remain outside tracked files.

No pull request or remote push was made as part of this local test deployment; no GitHub CI result is claimed. Source review notes: [implementation review](sky-ui-implementation.md).
