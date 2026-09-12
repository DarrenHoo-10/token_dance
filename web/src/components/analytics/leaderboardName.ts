import type { LeaderboardEntry } from '@/types/api';

/** Visible public leaderboard name: nickname only; handle is a blank-name fallback. */
export function publicLeaderboardName(entry: Pick<LeaderboardEntry, 'displayName' | 'handle'>): string {
  return entry.displayName?.trim() || entry.handle;
}
