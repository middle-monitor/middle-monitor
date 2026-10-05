import { useCallback, useState } from 'react';
import { useQueryClient, type QueryKey } from '@tanstack/react-query';

import { autoRefreshInterval, autoRefreshStopped, rearmAutoRefresh } from '../queryClient';

/** Selectable cadences, in milliseconds. 0 means Off. */
export const REFRESH_INTERVALS = [0, 5000, 10000, 15000, 30000, 60000, 300000] as const;

const STORAGE_PREFIX = 'mm.refresh.';

export interface AutoRefreshControl {
  interval: number;
  setInterval: (ms: number) => void;
  /** Rebuilt on every render, so not a stable dependency; never rejects. */
  refresh: () => void;
  refreshing: boolean;
  paused: boolean;
  autoStopped: boolean;
  lastSuccessAt: number | null;
  lastFailed: boolean;
}

function readStoredInterval(viewKey: string, defaultInterval: number): number {
  try {
    const raw = localStorage.getItem(STORAGE_PREFIX + viewKey);
    if (raw === null) return defaultInterval;
    const value = Number(raw);
    return REFRESH_INTERVALS.includes(value as (typeof REFRESH_INTERVALS)[number])
      ? value
      : defaultInterval;
  } catch {
    return defaultInterval;
  }
}

/** Cadence a view refreshes at, as the user picked it, kept per view. */
export function useRefreshInterval(
  viewKey: string,
  defaultInterval: number,
): [number, (ms: number) => void] {
  const [interval, setIntervalState] = useState(() => readStoredInterval(viewKey, defaultInterval));

  const select = useCallback(
    (ms: number) => {
      setIntervalState(ms);
      try {
        localStorage.setItem(STORAGE_PREFIX + viewKey, String(ms));
      } catch {
        // Private browsing: the choice just does not survive the session.
      }
    },
    [viewKey],
  );

  return [interval, select];
}

interface QueryRefreshOptions {
  /** The view's primary query, whose state the control reports. */
  query: { isFetching: boolean; isError: boolean; dataUpdatedAt: number };
  /** Exact key of that query, for the cadence the auto-stop applies to. */
  queryKey: QueryKey;
  /** What a manual refresh invalidates: every entry the view reads. */
  prefix: QueryKey;
}

/**
 * Same control, for a view whose data lives in the query cache: the cadence
 * feeds refetchInterval instead of a timer, and the outcome of the last attempt
 * is read from the query rather than kept in state. Split in two because
 * refetchInterval is needed before the query exists, and the control after.
 */
export function useQueryRefresh(viewKey: string, defaultInterval: number, paused = false) {
  const client = useQueryClient();
  const [interval, selectInterval] = useRefreshInterval(viewKey, defaultInterval);

  return {
    refetchInterval: paused ? false : autoRefreshInterval(interval),
    buildControl: ({ query, queryKey, prefix }: QueryRefreshOptions): AutoRefreshControl => ({
      interval,
      setInterval: (ms: number) => {
        selectInterval(ms);
        // Picking a cadence re-arms a timer the failures had stopped: a user who
        // asks for a rate must get one back, not silence. Only the timer
        // restarts; the data moves on the first tick.
        if (ms > 0) rearmAutoRefresh(client, prefix);
      },
      refresh: () => {
        client.invalidateQueries({ queryKey: prefix });
      },
      refreshing: query.isFetching,
      paused,
      // Secondary panels under the same prefix have their own entries: only the
      // primary query's cadence is reported as given up.
      autoStopped: autoRefreshStopped(client.getQueryCache().find({ queryKey, exact: true })),
      lastSuccessAt: query.dataUpdatedAt || null,
      lastFailed: query.isError,
    }),
  };
}
