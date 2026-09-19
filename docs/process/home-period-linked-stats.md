# Homepage period-linked statistics · 2026-09-20

The homepage opens on a rolling 24-hour window. Its period tabs drive the leaderboard, the selected creator's trajectory and the hero community metrics together: tokens, active developers, generated code lines, AI interactions, estimated cost, deltas and harness shares. The trajectory has no independent period control.

The public community endpoint accepts `window=today|7d|30d|all`. For compatibility the API key remains `today`, but it now means the current Beijing-time hour plus the preceding 23 hourly buckets, compared with the preceding 24 hours. The 7-day and 30-day windows use daily buckets and compare with the preceding equal-length period; all-time omits a comparison. Creator trajectories use hourly points for the rolling 24 hours, daily points for 7 and 30 days, and monthly points for all time. Active developers use users with positive tokens in the selected window. Multiple currencies remain separate and are never FX-merged.

The client clears stale hero values while a new period loads and ignores late responses from the previous selection. Period-specific headings and comparison captions make the active scope visible. The release branch retains its bounded public cache with a separate community cache key per period.

Validation details are recorded with the release that ships this change.
