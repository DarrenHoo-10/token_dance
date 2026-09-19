# Homepage trend period test release · 2026-09-20

- URL: https://nexorai.com.cn/token-dance-test/leaderboard
- Source branch: `codex/home-trend-follows-ranking`
- Source commits: `85814649d45cd4f4da3ad8e16ae39a1e7e73c7b5`, `a1525cb`
- Build branch: `release` (clean worktree)
- Build commit: `9e0690100750832e96ad52a9609da991521f9ee5`
- Built at: `2026-09-19 16:44:20 UTC`
- Activated at: `2026-09-19 16:45:42 UTC`
- Package SHA-256: `27f85494991cb68ba6b017b13a9f7e6c03de7c0d3e4f5b3ca634ed6d9a1dbb3f`
- Release path: `/opt/token-dance-test/releases/20260919-164420-9e069010`
- Previous test release: `/opt/token-dance-test/releases/20260919-164036-859d2166`
- Database backup: `/var/backups/token-dance-test/20260919-164542`

The homepage now uses a rolling 24-hour first period instead of a midnight-reset calendar day. The leaderboard, hero totals, selected creator trajectory and personal summary share the same period. Trajectories use hourly points for 24 hours, daily points for 7 and 30 days, and monthly points for all time; the trajectory card no longer has a separate period selector. Active users without a legacy public-profile projection can now expose their safe public profile and trajectory, which restores Jiayu's historical chart.

## Verification

The implementation branch passed all Go tests, 210 frontend tests across 35 files, and the production frontend build. The release branch passed the same 210 frontend tests, its base-path build, all Go packages and the corrected 18-migration inventory check. Linux amd64 API, Worker, migrate and provider-check binaries were rebuilt from the clean release worktree.

Activation verified the archive checksum, confirmed `tokendance_dev`, backed up the test database, applied and checked migrations, switched the isolated test API and Worker, and passed readiness. Live checks confirmed build commit `9e0690100750832e96ad52a9609da991521f9ee5`, rolling statistics in `UTC+8`, and Jiayu trajectory granularities: hourly for the past 24 hours, daily for 7/30 days and monthly for all time. Jiayu currently has no event in the latest 24-hour window, while the deployed endpoint returns 5 daily points for 7 days, 28 for 30 days and 5 monthly points for all time.

The production readiness endpoint remained HTTP 200 and production was not redeployed. Nginx validation passed with the host's pre-existing duplicate-server-name warnings.
