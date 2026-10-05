import { act, cleanup, render } from '@testing-library/react';
import { useState } from 'react';
import { afterEach, describe, expect, it } from 'vitest';

import { DateRangeContext, type DateRange, type DateRangeContextType } from '../contexts/DateRangeContext';
import { useDateRangeBounds, useDateRangeKey } from './useDateRangeBounds';

// vite.config.ts sets neither globals nor a setup file, so
// @testing-library/react never installs its own cleanup: without this, every
// render of the file stays mounted in document.body until the file ends.
afterEach(cleanup);

// A relative range like "last 15 minutes" slides every tick, so its start and
// end are different on every render. Keying a query on those bounds would
// reload every view on the clock, which is the reload nobody asked for. These
// two hooks are the contract that prevents it: the key follows the user's
// choice, and the getter hands out fresh bounds without being a dependency.

function provider(range: DateRange): DateRangeContextType {
  return {
    dateRange: range,
    setDateRange: () => {},
    formatDateRangeLabel: () => '',
  };
}

/** Renders both hooks under a range the test can swap, and records what they return. */
function harness(initial: DateRange) {
  const seen = { key: '', bounds: null as { startIso: string; endIso: string } | null };
  let getBounds: (() => { startIso: string; endIso: string }) | null = null;
  let swap: ((next: DateRange) => void) | null = null;
  const getterIdentities = new Set<unknown>();

  function Probe() {
    const [range, setRange] = useState(initial);
    swap = setRange;
    return (
      <DateRangeContext.Provider value={provider(range)}>
        <Inner />
      </DateRangeContext.Provider>
    );
  }

  function Inner() {
    const bounds = useDateRangeBounds();
    const key = useDateRangeKey();
    getBounds = bounds;
    getterIdentities.add(bounds);
    seen.key = key;
    seen.bounds = bounds();
    return null;
  }

  render(<Probe />);
  return {
    seen,
    getterIdentities,
    readBounds: () => getBounds!(),
    swap: (next: DateRange) => act(() => swap!(next)),
  };
}

// slideMs makes the slide explicit. Two calls to new Date() inside one test can
// land on the same millisecond, which would hide a getter that rebuilds itself
// on the bounds instead of on the user's choice.
const relative = (minutes: number, slideMs = 0): DateRange => {
  const now = Date.UTC(2026, 0, 1, 12, 0, 0) + slideMs;
  return {
    start: new Date(now - minutes * 60_000),
    end: new Date(now),
    relative: `${minutes}m`,
  };
};

const absolute = (from: string, to: string): DateRange => ({
  start: new Date(from),
  end: new Date(to),
});

describe('useDateRangeKey', () => {
  // The key is the user's choice, not the clock. Two renders of "last 15
  // minutes" a minute apart must produce the same key or every query keyed on
  // it refetches.
  it('is stable while a relative range slides', () => {
    const h = harness(relative(15));
    const first = h.seen.key;
    h.swap(relative(15, 30_000));
    expect(h.seen.key).toBe(first);
    expect(first).toBe('15m');
  });

  it('changes when the user picks another relative range', () => {
    const h = harness(relative(15));
    const first = h.seen.key;
    h.swap(relative(60));
    expect(h.seen.key).not.toBe(first);
  });

  // An absolute range has no name, so its bounds are the identity. Two
  // different windows must not collide on one cache entry.
  it('identifies an absolute range by its bounds', () => {
    const h = harness(absolute('2026-01-01T00:00:00Z', '2026-01-02T00:00:00Z'));
    const first = h.seen.key;
    expect(first).toContain('2026-01-01');

    h.swap(absolute('2026-02-01T00:00:00Z', '2026-02-02T00:00:00Z'));
    expect(h.seen.key).not.toBe(first);
  });
});

describe('useDateRangeBounds', () => {
  // The getter is passed into effects and callbacks. If its identity changed
  // with the sliding bounds, every one of those would re-run on the clock,
  // which is the reload this hook exists to avoid.
  it('keeps one identity while a relative range slides', () => {
    const h = harness(relative(15));
    h.swap(relative(15, 30_000));
    h.swap(relative(15, 60_000));
    expect(h.getterIdentities.size).toBe(1);
  });

  it('takes a new identity when the user picks another range', () => {
    const h = harness(relative(15));
    h.swap(relative(60));
    expect(h.getterIdentities.size).toBe(2);
  });

  // A stable identity is only useful if calling it still returns current
  // bounds; otherwise the view would keep querying the window it loaded with.
  // The slide must leave the key untouched: a range the user changed rebuilds
  // the getter anyway, so it would not tell a ref read apart from bounds
  // captured in the closure.
  it('still hands out the current bounds after the range slid', () => {
    const h = harness(relative(15));
    const before = h.readBounds();

    const slid = relative(15, 30_000);
    h.swap(slid);

    const after = h.readBounds();
    expect(after.endIso).toBe(slid.end.toISOString());
    expect(after.endIso).not.toBe(before.endIso);
  });

  it('returns ISO strings, which is what the API expects', () => {
    const h = harness(absolute('2026-01-01T10:00:00Z', '2026-01-01T11:00:00Z'));
    expect(h.seen.bounds).toEqual({
      startIso: '2026-01-01T10:00:00.000Z',
      endIso: '2026-01-01T11:00:00.000Z',
    });
  });
});
