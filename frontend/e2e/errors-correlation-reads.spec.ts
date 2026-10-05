import { test, expect, type Page } from '@playwright/test';

import { enterDemo as openDemo } from './demo';

// Story #16: the Errors view used to await seven reads in one Promise.all
// before painting anything, and to replay all seven on every refresh cycle.
// Four of them (links, hosts, services, host-groups) only feed the correlation
// panel of an opened row, and two of those are the slowest calls of the screen.
// What is pinned here: the rows never wait for those four, and an overview
// nobody drills into never pays them at all, however long it stays open.

// The reads the list itself needs answer within the tick while every other demo
// read is held back by mm.demo.latency, which is how "still in flight" is told
// apart from "never went out" without a backend (see src/demo/demoApi.ts).
const LIST_READS = 'errors.stats,errors.groups,errors.services';

// Longer than any assertion below waits: a row that painted did not wait for a
// held-back read.
const LATENCY_MS = 8000;

const appRows = (page: Page) => page.locator('table.table tbody tr');
const errorRows = (page: Page) => page.locator('div[role="button"][tabindex="0"]');

// Fetch counts read from the cache the app itself uses: a demo read leaves no
// network trace, and what the criteria count is fetches, not renders.
function fetchCounts(page: Page): Promise<{ list: number; correlation: number }> {
  return page.evaluate(async () => {
    const { queryClient } = await import('/src/queryClient.ts');
    const counts = { list: 0, correlation: 0 };
    for (const query of queryClient.getQueryCache().findAll()) {
      const key = query.queryKey as unknown[];
      if (key[1] !== 'errors') continue;
      if (key[2] === 'correlation-context') counts.correlation += query.state.dataUpdateCount;
      if (key[2] === 'groups') counts.list += query.state.dataUpdateCount;
    }
    return counts;
  });
}

test('the error rows paint while the correlation reads are still held back', async ({ page }) => {
  // The deliberate 8s hold plus a cold dev server does not fit the default.
  test.setTimeout(60_000);

  await page.addInitScript(([ms, instant]) => {
    sessionStorage.setItem('mm.demo.latency', ms);
    sessionStorage.setItem('mm.demo.instant-reads', instant);
  }, [String(LATENCY_MS), LIST_READS]);
  await openDemo(page);
  await page.goto('/organizations/demo/errors');

  // The apps table is the first thing the overview owes the user, and it needs
  // none of the four.
  await expect(appRows(page).first()).toBeVisible({ timeout: LATENCY_MS - 3000 });

  // Drilling in is what makes an error row reachable; the rows must not wait
  // for the correlation reads the drill-down starts.
  await appRows(page).first().click();
  await expect(errorRows(page).first()).toBeVisible({ timeout: LATENCY_MS - 3000 });
});

test('the overview never fetches the correlation reads, cycle after cycle', async ({ page }) => {
  // Two refresh cycles at the default 10s cadence, which the story keeps.
  test.setTimeout(90_000);

  await openDemo(page);
  await page.goto('/organizations/demo/errors');
  await expect(appRows(page).first()).toBeVisible();

  const first = await fetchCounts(page);
  await page.waitForTimeout(22_000);
  const afterCycles = await fetchCounts(page);

  // The cadence still runs — otherwise the correlation count below would prove
  // nothing.
  expect(afterCycles.list).toBeGreaterThan(first.list);
  expect(afterCycles.correlation).toBe(0);

  // Opening an app pays for them once, and they stay off the cadence: a cycle
  // later the list has moved on and they have not.
  await appRows(page).first().click();
  await expect(errorRows(page).first()).toBeVisible();
  await expect.poll(async () => (await fetchCounts(page)).correlation).toBe(1);

  const beforeCycle = await fetchCounts(page);
  await page.waitForTimeout(12_000);
  const afterCycle = await fetchCounts(page);
  expect(afterCycle.list).toBeGreaterThan(beforeCycle.list);
  expect(afterCycle.correlation).toBe(1);
});

test('an opened row still has its hosts, services, links and host groups', async ({ page }) => {
  test.setTimeout(60_000);

  await openDemo(page);
  await page.goto('/organizations/demo/errors');
  await appRows(page).first().click();
  await expect(errorRows(page).first()).toBeVisible();

  // The links card of the drilled-down app: loaded, not stuck on its loading
  // line, and showing either its links or the empty state.
  const linksCard = page.locator('.card', { hasText: 'Correlation' }).last();
  await expect(linksCard.locator('table.table, .empty-state')).toHaveCount(1, { timeout: 10_000 });

  // Hosts, services and host groups reach the same place they did before: the
  // targets offered when adding a correlation.
  await page.locator('button', { hasText: 'Add correlation' }).first().click();
  const target = page.locator('.modal-content select.select').last();
  for (const type of ['host_group', 'host', 'service']) {
    await page.locator('.modal-content select.select').first().selectOption(type);
    await expect.poll(() => target.locator('option').count()).toBeGreaterThan(1);
  }
});
