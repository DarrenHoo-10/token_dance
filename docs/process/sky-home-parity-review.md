# Homepage reference parity review · 2026-09-19

The implemented homepage differed from the approved interactive reference: the hero-only background cropped the robot, the KPI container became a large white card, inherited range-tab dimensions increased panel height, and chart/calendar/leaderboard details lost reference interactions.

The update places the existing sky artwork behind the whole home shell, restores reference typography and panel proportions, and keeps deltas beside the KPI values. It adds a soft curved chart with pointer and keyboard selection, a searchable and expandable six-row home leaderboard, and calendar day detail plus recorded monthly totals. Existing public/authenticated API boundaries, privacy behavior, loading/error handling and real-data sources remain in use. Search only covers the loaded board; the full-board route remains available. Period changes require complete consecutive records and a nonzero baseline; missing history is not displayed as a zero change.

## Review and validation

- Reviewed homepage layout, shared chart/calendar consumers, search filtering, own-rank handling, untrusted names rendered through React, and the absence of fixture imports in production.
- TypeScript, 181 tests in 29 files, and the production frontend build passed. Existing large-chunk build warnings remain.
- At a 1536 px viewport, both pages start the first card row at y=649 and place the KPI band at y=524.2 with height 101.8; first-row height differs by about 2 px. Real values and avatars intentionally come from the API.
- Browser checks passed on home, personal, team overview, members and settings at widths 320, 390, 768, 1024 and 1536: no document overflow or runtime errors. Search-button focus, filtering, clearing, chart hover, calendar month navigation, mobile menu, team period/dialog and synthetic sharing-version behavior were checked. Unit coverage checks expand/collapse, handle search and incomplete comparison periods.
- Reference and verification screenshots remain local in ignored build output. No credentials, private screenshots, or session payloads are part of this change.

Release verification and the actual clean `release` build SHA are recorded separately after deployment.
