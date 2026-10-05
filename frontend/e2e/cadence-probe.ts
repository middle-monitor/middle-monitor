import { QueryObserver } from '@tanstack/react-query';

import { autoRefreshInterval, createQueryClient } from '../src/queryClient';

// Loaded inside the page by query-cache-defaults.spec.ts: query-core schedules
// no refetch timer when there is no window, so the auto-refresh cadence can
// only be observed from a browser.

const wait = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));

async function until(predicate: () => boolean): Promise<void> {
  while (!predicate()) await wait(5);
}

/**
 * Polls a query that breaks, then comes back, and reports how many fetches the
 * observer actually emitted: once during the outage, once after the recovery.
 */
export async function runCadenceProbe(pollMs: number): Promise<{ afterOutage: number; afterRecovery: number }> {
  const client = createQueryClient();
  let outage = false;
  let fetches = 0;

  const observer = new QueryObserver(client, {
    queryKey: ['live:acme', 'hosts', 'stats'],
    queryFn: async () => {
      fetches += 1;
      if (outage) throw { response: { status: 500 } };
      // Counters, identical to the last good response: the cadence has to
      // restart on the response itself, not on a re-render it may never get.
      return { total: 3, failing: 0 };
    },
    // One cycle, one fetch here; retries inside a cycle are pinned by the spec.
    retry: false,
    staleTime: 0,
    refetchInterval: autoRefreshInterval(pollMs),
  });
  const unsubscribe = observer.subscribe(() => {});

  try {
    await until(() => fetches === 1);

    outage = true;
    await wait(pollMs * 6);
    const afterOutage = fetches;

    outage = false;
    await observer.refetch();
    await wait(pollMs * 3);

    return { afterOutage, afterRecovery: fetches };
  } finally {
    unsubscribe();
    client.clear();
  }
}
