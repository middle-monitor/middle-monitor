import { readFileSync } from 'node:fs';
import { test, expect } from '@playwright/test';

import { createQueryClient, orgQueryScope, shouldRetry } from '../src/queryClient';

// The cache policy the whole dashboard inherits. It is decided once in
// src/queryClient.ts, so it is worth pinning once — a view that silently
// retried a 4xx, or one that never resumed refreshing after an outage, would be
// invisible in a UI test until an endpoint actually broke.

test('a 4xx is never retried, a 5xx and a network error are retried exactly once', () => {
  // A rejected filter or a missing resource answers the same way on the next
  // try: retrying only doubles the load and delays the error the user sees.
  expect(shouldRetry(0, { response: { status: 400 } })).toBe(false);
  expect(shouldRetry(0, { response: { status: 404 } })).toBe(false);

  // A 5xx or a dropped connection is worth one more attempt, and only one.
  expect(shouldRetry(0, { response: { status: 500 } })).toBe(true);
  expect(shouldRetry(1, { response: { status: 500 } })).toBe(false);
  expect(shouldRetry(0, new Error('Network Error'))).toBe(true);
  expect(shouldRetry(1, new Error('Network Error'))).toBe(false);
});

test('auto refresh gives up after two failed cycles and restarts on the first success', async ({ page }) => {
  // Driven from the page, through a real QueryObserver: what this pins is an
  // ordering, the interval being recomputed while query-core notifies its
  // observers. Calling the interval function by hand, after the cache callbacks
  // have run, would never see it.
  await page.goto('/');
  const counts = await page.evaluate(async () => {
    const probe = await import('/e2e/cadence-probe.ts');
    // Short enough to keep the test quick, long enough for a loaded CI runner
    // to tell one cycle from the next.
    return probe.runCadenceProbe(150);
  });

  // One bad cycle can be a blip, two mean the endpoint is down: the first good
  // fetch, two failed cycles, and nothing more.
  expect(counts.afterOutage).toBe(3);

  // Nobody has to reload the page: the first call that works restarts the
  // cadence on its own.
  expect(counts.afterRecovery).toBeGreaterThan(counts.afterOutage + 1);
});

test('polling stops with the tab instead of running in the background', () => {
  // A background tab that keeps polling burns quota for a screen nobody reads,
  // and freezes its retries until the tab comes back.
  const queries = createQueryClient().getDefaultOptions().queries;
  expect(queries?.refetchIntervalInBackground).toBe(false);
});

test('a query key is scoped so two orgs, and the demo sandbox, never share cached data', () => {
  expect(orgQueryScope('acme', false)).not.toBe(orgQueryScope('globex', false));
  // The demo org slug is a plain slug: a real org named "demo" must not read
  // the sandbox cache, nor the other way round.
  expect(orgQueryScope('demo', true)).not.toBe(orgQueryScope('demo', false));
});

test('the response cache is never persisted, so a browser reload refetches', () => {
  // Decision of the epic: the cache lives in memory only. Installing any
  // persistence plugin would serve stale data after F5, which is exactly the
  // "corrupted data" the cache was allowed on condition of avoiding.
  const pkg = JSON.parse(readFileSync(new URL('../package.json', import.meta.url), 'utf8'));
  const installed = Object.keys({ ...pkg.dependencies, ...pkg.devDependencies });

  expect(installed.filter((name) => name.includes('persist'))).toEqual([]);
});
