# Homepage parity test release · 2026-09-19

- URL: https://nexorai.com.cn/token-dance-test/leaderboard
- Clean build branch: `release`
- Final build SHA: `8184030625727807225e8566a16950c3aeb0ad4d`
- Built: `2026-09-19 08:20:48 UTC`; activated: `2026-09-19 08:21:13 UTC`
- Package SHA-256: `1515a6b95fbbf3af62ef47f2c60d86ba73d221de8d2a29d0043661107649d583`
- Feature implementation: `d8cf21d` and `388d016` on `codex/sky-ui-implementation`.

See [reference comparison and review](sky-home-parity-review.md). The release adaptation preserves public-home caching, top-100 fetching and avatar fetch priority. It shows six rows initially, with search/expand operating on the cached board. Tests confirm the request stays public and capped at 100.

TypeScript and 188 tests across 31 files passed on the parity implementation. The final CSS-only follow-up fixes vendor-property ordering so optimization preserves the transparent navbar's blur reset. The final clean SHA was built for the frontend subpath and all four Linux amd64 binaries. Compiled browser checks passed at 320–1536 px across home, personal and team pages, including search/focus, chart hover and calendar/navigation interactions; computed navbar backdrop-filter is `none`. Existing large-chunk warnings remain.

Deployment verified checksums, guarded against concurrent test releases, backed up the development database, verified migrations and schema, and passed readiness. The final live browser checks passed using a test account: actual assets and navbar style, home search, personal metrics, team analytics/members/settings, deep-link refresh and mobile overflow with no runtime errors. No live team data was edited. Production was not deployed. Existing nginx duplicate-host warnings remain; syntax validation passed.

The first parity activation was `aaaa382a8717fb9027f5608d283e961adc86f316`; online inspection caught the optimized CSS issue and it was superseded by the final SHA above. No PR or remote push occurred; no GitHub CI result is claimed. Credentials, session payloads and private screenshots stay in ignored local output.
