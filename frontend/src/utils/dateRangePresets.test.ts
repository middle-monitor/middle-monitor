import { describe, expect, it } from 'vitest';

import { isSlidingPreset, rangeForPreset } from './dateRangePresets';

// A picked preset is kept as an id and recomputed as time passes. If it froze at
// the moment of the click, "this week" picked on Monday would stop at Monday
// 23:59 and hide everything from Tuesday on, even with auto refresh running.

describe('rangeForPreset', () => {
  it('moves "today" to the new day after midnight', () => {
    const lateMonday = new Date(2026, 9, 5, 23, 50);
    const earlyTuesday = new Date(2026, 9, 6, 0, 10);
    expect(rangeForPreset('today', lateMonday)!.start.getDate()).toBe(5);
    const tuesday = rangeForPreset('today', earlyTuesday)!;
    expect(tuesday.start.getDate()).toBe(6);
    expect(tuesday.end.getTime()).toBeGreaterThan(earlyTuesday.getTime());
  });

  it('keeps "this week" open up to the current day', () => {
    const thursday = new Date(2026, 9, 8, 14, 0);
    const week = rangeForPreset('this_week', thursday)!;
    expect(week.start.getDay()).toBe(1); // Monday
    expect(week.start.getDate()).toBe(5);
    expect(week.end.getDate()).toBe(8);
    expect(week.end.getTime()).toBeGreaterThan(thursday.getTime());
  });

  it('ends a trailing window at now', () => {
    const now = new Date(2026, 9, 6, 9, 30);
    const day = rangeForPreset('24h', now)!;
    expect(day.end).toEqual(now);
    expect(now.getTime() - day.start.getTime()).toBe(24 * 60 * 60 * 1000);
  });

  it('knows every preset the picker offers, and only those', () => {
    for (const id of ['15m', '1h', '6h', '24h', 'today', 'yesterday', 'this_week', 'this_month', 'this_quarter', 'this_year', '7d', '30d', '12_months']) {
      expect(rangeForPreset(id), id).not.toBeNull();
    }
    expect(rangeForPreset('fortnight')).toBeNull();
  });

  it('tells trailing windows from calendar periods', () => {
    expect(isSlidingPreset('24h')).toBe(true);
    expect(isSlidingPreset('today')).toBe(false);
  });
});
