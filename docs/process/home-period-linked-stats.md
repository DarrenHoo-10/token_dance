# Homepage period-linked statistics · 2026-09-19

The homepage now opens on the rolling 7-day window. Its period tabs drive both the leaderboard and the hero community metrics: tokens, active developers, generated code lines, AI interactions, estimated cost, deltas and harness shares.

The public community endpoint accepts `window=today|7d|30d|all`. It reads the existing precomputed daily community rows and window scores. Today compares with yesterday; 7-day and 30-day totals compare with the preceding equal-length period; all-time omits a comparison. Active developers use users with positive tokens in the selected precomputed window. Multiple currencies remain separate and are never FX-merged.

The client clears stale hero values while a new period loads and ignores late responses from the previous selection. Period-specific headings and comparison captions make the active scope visible. The release branch retains its bounded public cache with a separate community cache key per period.

Validation on the implementation branch: all 183 web tests in 29 files, TypeScript, the frontend production build, and all Go tests passed. Browser verification covered default 7-day selection, a switch to today, matching hero values, request parameters, existing interactions, 320-1536 px overflow and runtime errors.
