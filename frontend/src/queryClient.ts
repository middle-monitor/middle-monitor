import { QueryClient, type QueryKey } from '@tanstack/react-query';

/**
 * Application-wide data cache, sitting on top of orgApi (never axios directly,
 * so demo mode keeps working). In memory only: no persistence plugin is
 * installed, so a browser reload always goes back to the network.
 */

function httpStatus(error: unknown): number | undefined {
  return (error as { response?: { status?: number } } | null)?.response?.status;
}

// A 4xx won't fix itself on the next try; 5xx and network errors get one retry.
export function shouldRetry(failureCount: number, error: unknown): boolean {
  const status = httpStatus(error);
  if (status !== undefined && status >= 400 && status < 500) return false;
  return failureCount < 1;
}

// Number of failed cycles a view tolerates before its auto-refresh gives up.
export const MAX_FAILED_CYCLES = 2;

type PolledQuery = { state: { status: string; errorUpdateCount: number } };

// errorUpdateCount as it stood at each query's last success: what follows it is
// the run of failed cycles. Keyed by the query itself, so the counter lives and
// dies with the cache entry it describes, whichever client owns it.
const errorsAtLastSuccess = new WeakMap<PolledQuery, number>();

/**
 * refetchInterval for a view's own cadence, 0 meaning the user turned it off.
 * Returns false once the query has
 * failed MAX_FAILED_CYCLES times in a row, so a broken endpoint stops being
 * polled; the first successful call (manual refresh, window focus) resumes it.
 * Both decisions read the query state, which the reducer has already updated
 * when the observer asks for the interval — a QueryCache callback would answer
 * one dispatch too late, and the restart would then wait for a React render.
 */
export function autoRefreshInterval(ms: number) {
  if (ms <= 0) return false as const;
  return (query: PolledQuery): number | false => {
    if (query.state.status !== 'error') {
      errorsAtLastSuccess.set(query, query.state.errorUpdateCount);
      return ms;
    }
    const seen = errorsAtLastSuccess.get(query);
    // Never before the first success: the view is then showing its error
    // screen, which carries no control to restart from, so the timer is the
    // only thing that can bring it back when the endpoint recovers.
    if (seen === undefined) return ms;
    // Retries inside one cycle don't count: errorUpdateCount only moves when a
    // fetch has exhausted them.
    return query.state.errorUpdateCount - seen >= MAX_FAILED_CYCLES ? false : ms;
  };
}

/**
 * Whether a query's cadence has given up, for the control to report. Reads the
 * counter autoRefreshInterval decides on, so the two cannot drift.
 */
export function autoRefreshStopped(query: PolledQuery | undefined): boolean {
  if (!query || query.state.status !== 'error') return false;
  const seen = errorsAtLastSuccess.get(query);
  if (seen === undefined) return false;
  return query.state.errorUpdateCount - seen >= MAX_FAILED_CYCLES;
}

/**
 * Forgets the failed cycles of every query under a key prefix. Picking a
 * cadence re-arms a timer the failures had stopped: a user who asks for a rate
 * must get one back, not silence.
 */
export function rearmAutoRefresh(client: QueryClient, queryKey: QueryKey): void {
  for (const query of client.getQueryCache().findAll({ queryKey })) {
    errorsAtLastSuccess.set(query, query.state.errorUpdateCount);
  }
}

/**
 * First segment of every org-scoped query key. Two organizations, and the demo
 * sandbox, never share a cache entry: switching org must not flash the previous
 * org's data.
 */
export function orgQueryScope(slug: string, demo: boolean): string {
  return `${demo ? 'demo' : 'live'}:${slug}`;
}

/**
 * The host list Metrics Explorer, Timeline and Service Detail all read. Shared
 * on purpose: one entry, fetched once. A literal per view would drift into a
 * fourth entry on a single typo, and invalidateServiceWrite would miss it.
 */
export function allHostsKey(scope: string): QueryKey {
  return [scope, 'hosts', 'all'];
}

export function createQueryClient(): QueryClient {
  return new QueryClient({
    defaultOptions: {
      queries: {
        // Shorter than any view cadence: a revisited view paints from cache,
        // then revalidates in the background.
        staleTime: 5_000,
        retry: shouldRetry,
        // Default kept explicit: polling stops with the tab and resumes on focus.
        refetchIntervalInBackground: false,
      },
    },
  });
}

export const queryClient = createQueryClient();

/**
 * Cache entries a service write touches: the service lists and details, and the
 * host lists that count services. Invalidating them revalidates those views in
 * place — their query keys, which carry the filter and the page, are untouched,
 * so nothing resets. It replaces the former `serviceUpdated` window event,
 * which made four views reload everything they had.
 */
export function invalidateServiceWrite(client: QueryClient, scope: string): void {
  client.invalidateQueries({ queryKey: [scope, 'services'] });
  client.invalidateQueries({ queryKey: [scope, 'hosts'] });
}

/**
 * Same, for a delete: the entity's detail entry is dropped, not invalidated.
 * Invalidating leaves the data in place until the refetch answers, so coming
 * back to the URL repaints a dead entity — and the 404 never replaces it, since
 * a query that already holds data does not report a loading error.
 */
export function invalidateEntityDelete(
  client: QueryClient,
  scope: string,
  detailKey: QueryKey,
): void {
  client.removeQueries({ queryKey: detailKey });
  invalidateServiceWrite(client, scope);
}
