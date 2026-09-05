import type { AgentConfig } from "./tauri-bridge.ts";

// Calendar dates in the device's local timezone, including today; avoid UTC/DST shifts.
export function lastSevenDays(now = new Date()): string[] {
  return Array.from({ length: 7 }, (_, index) => {
    const day = new Date(now.getFullYear(), now.getMonth(), now.getDate() - 6 + index, 12);
    return `${day.getFullYear()}-${String(day.getMonth() + 1).padStart(2, "0")}-${String(day.getDate()).padStart(2, "0")}`;
  });
}

export function weeklyUsage(agents: AgentConfig[], now = new Date()) {
  const dates = lastSevenDays(now);
  const totals = new Map<string, number | null>();
  const included: number[][] = [];
  for (const agent of agents) {
    const days = dates.map(date => agent.dailyUsage?.find(day => day.date === date)?.tokens);
    if (agent.accuracy === "unknown" || days.some(value => value == null || !Number.isFinite(value) || value < 0)) {
      totals.set(agent.id, null);
    } else {
      const values = days as number[];
      totals.set(agent.id, values.reduce((sum, value) => sum + value, 0));
      included.push(values);
    }
  }
  const points = dates.map((date, index) => ({ date, tokens: included.length ? included.reduce((sum, days) => sum + days[index], 0) : null }));
  return { dates, totals, points, complete: included.length === agents.length, total: included.length ? points.reduce((sum, point) => sum + (point.tokens ?? 0), 0) : null };
}
