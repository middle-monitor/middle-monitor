import { test, expect, type Page } from '@playwright/test';

import { enterDemo as openDemo } from './demo';

// Demo mode is the only environment CI can drive (frontend-only, fixed data,
// see src/demo/demoApi.ts). It replaces orgApi in memory, so there is no HTTP
// request to observe: the control stamps the time of its last successful
// refresh, and that stamp is what both the user and these tests read.
async function enterDemo(page: Page, path = '') {
  await openDemo(page);
  if (path) await navigateInApp(page, `/organizations/demo/${path}`);
}

// The router listens to popstate, so this moves the app without reloading it.
async function navigateInApp(page: Page, path: string) {
  await page.evaluate((to) => {
    window.history.pushState({}, '', to);
    window.dispatchEvent(new PopStateEvent('popstate'));
  }, path);
}

// Demo reads resolve in memory, so there is no request to intercept: the demo
// API rejects every read while this flag is set (src/demo/demoApi.ts). It is
// the only way to fail a refresh without changing the entity being read.
async function failReads(page: Page, on: boolean) {
  await page.evaluate((flag) => {
    if (flag) sessionStorage.setItem('mm.demo.fail-reads', '1');
    else sessionStorage.removeItem('mm.demo.fail-reads');
  }, on);
}

const control = (page: Page) => page.getByTestId('refresh-control').first();
const intervalSelect = (page: Page) => control(page).locator('select');
const status = (page: Page) => page.getByTestId('refresh-status').first();

const lastSuccess = (page: Page) => status(page).getAttribute('data-last-success');

// A wall clock, in whatever format the active locale renders. The control must
// date the data it shows, so the reader can tell fresh from stale at a glance.
const CLOCK = /\d{1,2}:\d{2}:\d{2}/;

// Waits for one more successful refresh, so a test that then asserts "it
// stopped" starts from a known-running state.
async function waitForRefresh(page: Page, previous: string | null) {
  await expect.poll(() => lastSuccess(page), { timeout: 20_000 }).not.toBe(previous);
}

// Counts how many distinct successful refreshes the control stamps over a
// window, the stamp already on screen included.
async function countRefreshes(page: Page, windowMs: number): Promise<number> {
  const seen = new Set<string>();
  const deadline = Date.now() + windowMs;
  while (Date.now() < deadline) {
    const value = await lastSuccess(page);
    if (value) seen.add(value);
    await page.waitForTimeout(250);
  }
  return seen.size;
}

// The perimeter, as reachable in demo mode, with the cadence each view must
// show on a first visit. Three perimeter views carry a control but cannot be
// driven here: Health (5 s) and Metrics (Off) have no route at all, and API
// Keys lives inside org settings, which redirects anyone who is not an admin
// (SettingsView.tsx) — the demo user is not one.
const PERIMETER_DEFAULTS: Array<[route: string, interval: string]> = [
  ['', '30000'],
  ['errors', '10000'],
  ['services', '15000'],
  ['services/1', '0'],
  ['traces', '0'],
  ['hosts', '15000'],
  ['hosts/groups', '0'],
  ['hosts/1', '15000'],
  // /metrics renders the explorer, so this is the Metrics view the criterion
  // names: 10 s, not the 15 s the explorer polled at before the control existed.
  ['metrics', '10000'],
  ['profiling', '0'],
  ['network', '0'],
  ['timeline', '30000'],
  ['logs', '0'],
  ['alerts/incidents', '0'],
  ['alerts/rules', '0'],
  ['alerts/channels', '0'],
];

const INTERVAL_VALUES = ['0', '5000', '10000', '15000', '30000', '60000', '300000'];

// Sixteen views in a row, on a dev server the other worker is hitting too: a
// default 5 s assertion measures the runner more than it measures the view.
const SLOW_RUNNER = { timeout: 20_000 };

test('the chosen interval is remembered for that view alone', async ({ page }) => {
  // Per view, not global: a user who wants Logs fast must not turn every other
  // view fast at the same time, and the choice must survive leaving the page.
  await enterDemo(page, 'logs');
  await intervalSelect(page).selectOption('30000');

  await page.reload();
  await expect(intervalSelect(page)).toHaveValue('30000');

  await page.goto('/organizations/demo/traces');
  await expect(intervalSelect(page)).toHaveValue('0');
});

test('the view refreshes at the cadence picked, and Off stops it entirely', async ({ page }) => {
  test.setTimeout(90_000);
  await enterDemo(page, 'logs');

  // The selector is a cadence, not an on/off switch: 5 s must produce visibly
  // more refreshes over the same window than 30 s, or the value means nothing.
  await intervalSelect(page).selectOption('5000');
  expect(await countRefreshes(page, 12_000)).toBeGreaterThanOrEqual(3);

  await intervalSelect(page).selectOption('30000');
  expect(await countRefreshes(page, 12_000)).toBe(1);

  // Off must mean no refresh at all, not a slower one: it is the setting a user
  // picks to freeze what is on screen.
  await intervalSelect(page).selectOption('0');
  const frozen = await lastSuccess(page);
  await page.waitForTimeout(12_000);
  expect(await lastSuccess(page)).toBe(frozen);
});

test('every routed view of the perimeter carries the same control, on its own cadence', async ({ page }) => {
  test.setTimeout(120_000);
  // Decision 3: one control, one list of cadences, wherever the user is. A view
  // that kept its own affordance would defeat the point of unifying them.
  // The default is per view, not global: a view nobody watches live must not
  // poll, and the ones that are watched live keep the cadence the product
  // picked, without the user having to ask for it.
  await enterDemo(page);
  // Walked from inside the app, the way a user reaches these views: reloading
  // the whole bundle sixteen times measures the dev server, not the control.
  for (const [route, defaultInterval] of PERIMETER_DEFAULTS) {
    await navigateInApp(page, `/organizations/demo/${route}`);
    await expect(control(page), route).toBeVisible(SLOW_RUNNER);
    await expect(control(page).getByRole('button'), route).toBeVisible(SLOW_RUNNER);
    const values = await intervalSelect(page).locator('option').evaluateAll(
      options => options.map(o => (o as HTMLOptionElement).value),
    );
    expect(values, route).toEqual(INTERVAL_VALUES);
    await expect(intervalSelect(page), route).toHaveValue(defaultInterval, SLOW_RUNNER);
  }
});

test('a view backed by the query cache refreshes at the cadence picked too', async ({ page }) => {
  test.setTimeout(90_000);
  // Hosts reads through the shared query cache, so its cadence is a
  // refetchInterval rather than a timer the control owns. Both paths have to
  // answer the selector, or the value shown would only be a preference nothing
  // reads.
  await enterDemo(page, 'hosts');
  await intervalSelect(page).selectOption('5000');
  expect(await countRefreshes(page, 12_000)).toBeGreaterThanOrEqual(3);

  await intervalSelect(page).selectOption('0');
  const frozen = await lastSuccess(page);
  await page.waitForTimeout(12_000);
  expect(await lastSuccess(page)).toBe(frozen);
});

test('opening a detail row suspends the refresh and closing it resumes', async ({ page }) => {
  test.setTimeout(60_000);
  await enterDemo(page, 'logs');
  await intervalSelect(page).selectOption('5000');
  await waitForRefresh(page, await lastSuccess(page));

  // Reading an open row is exactly when a refresh underneath is unacceptable:
  // the row would be rewritten under the pointer.
  await page.locator('.log-row').first().click();
  await expect(page.locator('.log-detail-row')).toBeVisible();
  await expect(status(page)).toContainText(/Paused|Suspendu/);

  const whilePaused = await lastSuccess(page);
  await page.waitForTimeout(12_000);
  expect(await lastSuccess(page)).toBe(whilePaused);

  await page.locator('.log-row').first().click();
  await expect(page.locator('.log-detail-row')).toHaveCount(0);
  await expect(status(page)).not.toContainText(/Paused|Suspendu/);
  await waitForRefresh(page, whilePaused);
});

test('the manual refresh button works on a view set to Off', async ({ page }) => {
  // Off suspends the cadence, never the user's own refresh.
  await enterDemo(page, 'logs');
  await expect(intervalSelect(page)).toHaveValue('0');

  const before = await lastSuccess(page);
  await control(page).getByRole('button').click();
  await waitForRefresh(page, before);

  // The time of the last successful refresh, not a bare spinner: it is what
  // tells the reader whether what is on screen is a second or an hour old.
  await expect(status(page)).toContainText(CLOCK);
});

test('a failed refresh keeps the data on screen and says the data is stale', async ({ page }) => {
  test.setTimeout(60_000);
  // Same host, same URL, reads that start failing under the reader: that is
  // what "the data must survive a failed revalidation" is about.
  await enterDemo(page, 'hosts/1');
  await expect(page.locator('.host-detail-title')).toHaveText('web-01');
  await intervalSelect(page).selectOption('5000');
  await waitForRefresh(page, await lastSuccess(page));
  const lastGood = await lastSuccess(page);
  const lastGoodClock = (await status(page).textContent())?.match(CLOCK)?.[0];
  expect(lastGoodClock).toBeTruthy();

  await failReads(page, true);

  // Blanking the page on a failed revalidation would lose what the user was
  // reading; presenting it as fresh would be worse. Keep it, and date it.
  // The notice lands on the next tick, so allow more than the 5 s cycle above.
  await expect(status(page)).toContainText(/failed|échec/i, { timeout: 15_000 });
  await expect(page.locator('.host-detail-title')).toHaveText('web-01');
  expect(await lastSuccess(page)).toBe(lastGood);

  // The failure notice still carries a time, and it is the time of the data on
  // screen: "stale since" is actionable, "something went wrong" is not.
  await expect(status(page)).toContainText(lastGoodClock!);

  // Repeated failures stop the timer instead of hammering a broken endpoint,
  // but never disable the user's own refresh.
  await expect
    .poll(() => status(page).getAttribute('data-auto-stopped'), { timeout: 30_000 })
    .toBe('true');
  await expect(control(page).getByRole('button')).toBeEnabled();

  // And the stop is not sticky: the first success resumes the cadence.
  await failReads(page, false);
  await control(page).getByRole('button').click();
  await waitForRefresh(page, lastGood);
  await expect(status(page)).toHaveAttribute('data-auto-stopped', 'false');
});

test('a failed refresh keeps a list view populated too', async ({ page }) => {
  test.setTimeout(60_000);
  // Emptying the list in the catch was defensible while only a filter or a page
  // change could fetch. On the cadence it blanks the table while the control
  // says the data is merely stale, which is the one thing the criterion forbids.
  await enterDemo(page, 'profiling');
  const rows = page.locator('.profiling-table tbody tr');
  await expect(rows.first()).toBeVisible();
  const rowCount = await rows.count();
  await intervalSelect(page).selectOption('5000');
  await waitForRefresh(page, await lastSuccess(page));
  const lastGood = await lastSuccess(page);

  await failReads(page, true);
  await expect(status(page)).toContainText(/failed|échec/i, { timeout: 15_000 });
  await expect(rows).toHaveCount(rowCount);
  expect(await lastSuccess(page)).toBe(lastGood);
});

test('another entity under the same view is not covered by the previous one', async ({ page }) => {
  // Keeping data through a failed refresh holds for one entity. A route param
  // change is a different entity: the view stays mounted, so showing the
  // previous host would also point Delete and Edit at it.
  await enterDemo(page, 'hosts/1');
  await expect(page.locator('.host-detail-title')).toHaveText('web-01');
  await navigateInApp(page, '/organizations/demo/hosts/999999');
  await expect(page.locator('.error-message')).toBeVisible();
  await expect(page.locator('.host-detail-title')).toHaveCount(0);

  await page.goto('/organizations/demo/services/1');
  const serviceName = await page.locator('.service-detail-title').textContent();
  expect(serviceName).toBeTruthy();
  await navigateInApp(page, '/organizations/demo/services/999999');
  await expect(page.locator('.service-detail-error')).toBeVisible();
  await expect(page.locator('.service-detail-title')).toHaveCount(0);
});

test('a non-numeric entity id in the route does not take the view down', async ({ page }) => {
  // The reset that clears the previous entity compares the route param itself:
  // on Number(id) a non-numeric one gives NaN, which never equals itself, so
  // the view would reset on every render pass until React gives up on it.
  const crashes: string[] = [];
  page.on('pageerror', (err) => crashes.push(err.message));
  await enterDemo(page, 'services/abc');
  await expect(page.getByText('Something went wrong')).toHaveCount(0);
  expect(crashes).toEqual([]);
});

test('a view whose first load fails comes back on its own when reads recover', async ({ page }) => {
  test.setTimeout(120_000);
  // Auto-stop spares a broken endpoint, but the error screen carries no control
  // to restart from: stopping the timer before anything has ever loaded would
  // leave the view dead until the user reloads the page by hand. Before this
  // MR the plain setInterval kept running through the error, and the view
  // healed itself when the backend came back; that must still hold.
  await enterDemo(page);
  await failReads(page, true);
  await navigateInApp(page, '/organizations/demo/hosts');
  await expect(page.locator('.error-message')).toBeVisible();
  await expect(control(page)).toHaveCount(0);

  // Well past the two failed cycles that arm the auto-stop elsewhere (Hosts
  // ticks every 15 s by default): the timer must still be running.
  await page.waitForTimeout(40_000);
  await failReads(page, false);
  await expect(page.locator('.hosts-table')).toBeVisible({ timeout: 30_000 });
  await expect(control(page)).toBeVisible();
});

test('picking a cadence restarts a query-cache timer too, not just one attempt', async ({ page }) => {
  test.setTimeout(90_000);
  // Hosts polls through the query cache, where the cadence is a refetchInterval
  // read off the query state rather than a timer the control owns. The criterion
  // has to hold on that path as well, and the failure mode is different: the
  // revalidation fired on picking a cadence can fail in its turn, and the user
  // would then be left with one attempt and silence.
  await enterDemo(page, 'hosts');
  await intervalSelect(page).selectOption('5000');
  await waitForRefresh(page, await lastSuccess(page));

  await failReads(page, true);
  await expect
    .poll(() => status(page).getAttribute('data-auto-stopped'), { timeout: 30_000 })
    .toBe('true');

  // Reads are still failing here, on purpose: the control must go back to
  // running, keep trying at the cadence just picked, and give up again only
  // after another run of failed cycles. Staying stopped, or reporting itself as
  // running while never polling again, are both the same lie to the user.
  await intervalSelect(page).selectOption('10000');
  await expect(status(page)).toHaveAttribute('data-auto-stopped', 'false');
  await expect
    .poll(() => status(page).getAttribute('data-auto-stopped'), { timeout: 40_000 })
    .toBe('true');
});

test('picking a cadence restarts a timer that failures had stopped', async ({ page }) => {
  test.setTimeout(60_000);
  // Without this, the selector lies after an outage: the user asks for 5 s and
  // gets silence, with nothing on screen saying the timer is off for good.
  await enterDemo(page, 'hosts/1');
  await expect(page.locator('.host-detail-title')).toHaveText('web-01');
  await intervalSelect(page).selectOption('5000');
  await waitForRefresh(page, await lastSuccess(page));

  await failReads(page, true);
  await expect
    .poll(() => status(page).getAttribute('data-auto-stopped'), { timeout: 30_000 })
    .toBe('true');

  await intervalSelect(page).selectOption('10000');
  await expect(status(page)).toHaveAttribute('data-auto-stopped', 'false');
});
