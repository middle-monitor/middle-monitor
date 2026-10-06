// Every preset of the date range picker, computed from the current time. A
// stored or selected preset keeps only its id, and is recomputed from this
// table on every tick, so "today" still means today after midnight.

const MINUTE = 60 * 1000;
const HOUR = 60 * MINUTE;
const DAY = 24 * HOUR;

// Trailing windows slide continuously with the clock.
const SLIDING_MS: Record<string, number> = {
  '15m': 15 * MINUTE,
  '1h': HOUR,
  '3h': 3 * HOUR,
  '6h': 6 * HOUR,
  '24h': DAY,
  '7d': 7 * DAY,
  '30d': 30 * DAY,
};

function startOfDay(d: Date): Date {
  const r = new Date(d);
  r.setHours(0, 0, 0, 0);
  return r;
}

function endOfDay(d: Date): Date {
  const r = new Date(d);
  r.setHours(23, 59, 59, 999);
  return r;
}

function startOfWeek(d: Date): Date {
  const r = startOfDay(d);
  const day = r.getDay();
  r.setDate(r.getDate() + (day === 0 ? -6 : 1 - day)); // Monday
  return r;
}

// Calendar periods move by whole days: their bounds only change at midnight.
const CALENDAR: Record<string, (now: Date) => { start: Date; end: Date }> = {
  today: (now) => ({ start: startOfDay(now), end: endOfDay(now) }),
  yesterday: (now) => {
    const d = new Date(now);
    d.setDate(d.getDate() - 1);
    return { start: startOfDay(d), end: endOfDay(d) };
  },
  this_week: (now) => ({ start: startOfWeek(now), end: endOfDay(now) }),
  this_month: (now) => ({ start: new Date(now.getFullYear(), now.getMonth(), 1), end: endOfDay(now) }),
  this_quarter: (now) => ({
    start: new Date(now.getFullYear(), Math.floor(now.getMonth() / 3) * 3, 1),
    end: endOfDay(now),
  }),
  this_year: (now) => ({ start: new Date(now.getFullYear(), 0, 1), end: endOfDay(now) }),
  '12_months': (now) => {
    const start = startOfDay(now);
    start.setFullYear(start.getFullYear() - 1);
    return { start, end: endOfDay(now) };
  },
};

/** The bounds a preset covers at `now`, or null for an unknown id. */
export function rangeForPreset(id: string, now: Date = new Date()): { start: Date; end: Date } | null {
  const ms = SLIDING_MS[id];
  if (ms != null) return { start: new Date(now.getTime() - ms), end: now };
  const calendar = CALENDAR[id];
  return calendar ? calendar(now) : null;
}

/** True for a trailing window, whose bounds change on every tick. */
export function isSlidingPreset(id: string): boolean {
  return id in SLIDING_MS;
}
