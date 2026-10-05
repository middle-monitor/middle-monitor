import { useCallback, useRef } from 'react';

import { useDateRange, type DateRange } from '../contexts/DateRangeContext';

// One definition for both the getter's identity and the query key.
function rangeKeyOf(dateRange: DateRange): string {
  return dateRange.relative ?? `${dateRange.start.toISOString()}/${dateRange.end.toISOString()}`;
}

/**
 * Reads the global date range for data loading without tying a fetch to it.
 * A relative range slides every 15s, so depending on its bounds reloads views
 * nobody asked to reload; the returned getter keeps a stable identity until the
 * user picks another range, and still hands out the current bounds when called.
 */
export function useDateRangeBounds(): () => { startIso: string; endIso: string } {
  const { dateRange } = useDateRange();
  const startIso = dateRange.start.toISOString();
  const endIso = dateRange.end.toISOString();
  const rangeKey = rangeKeyOf(dateRange);

  // Written during render, not in an effect: the caller's own effect must not
  // have to run after this hook's for the getter to hand out fresh bounds.
  const bounds = useRef({ startIso, endIso });
  bounds.current = { startIso, endIso };

  // Keyed on the user's choice, not on the sliding bounds.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  return useCallback(() => bounds.current, [rangeKey]);
}

/**
 * Stable identity of the range the user picked, for a query key. A relative
 * range slides every 15s: keying on the choice rather than on the bounds keeps
 * that tick from invalidating a query nobody asked to reload.
 */
export function useDateRangeKey(): string {
  const { dateRange } = useDateRange();

  return rangeKeyOf(dateRange);
}
