import { test, expect, type Page } from '@playwright/test';

import { enterDemo } from './demo';

// Story #5: a view that is fetching must not take its list away. The shared net
// (refresh-state.spec.ts) replays a refresh cycle on the ten views of the
// story; this file covers the two things that net cannot reach — the pager and
// the content area during a page change, and a row whose key used to move under
// the reader on every new occurrence.
//
// Demo mode is the only backend CI has (frontend-only, fixed data, see
// src/demo/demoApi.ts).

// Holds the demo response back so the loading state is observable at all
// (src/demo/demoApi.ts): without it a page change resolves within the tick.
const REFRESH_LATENCY_MS = 1500;

// A short viewport, so the content area collapsing is a visible change.
test.use({ viewport: { width: 1000, height: 520 } });

test.describe.configure({ timeout: 60_000 });

async function slowDownRequests(page: Page) {
  await page.evaluate((ms) => sessionStorage.setItem('mm.demo.latency', String(ms)), REFRESH_LATENCY_MS);
}

// Whichever pager the view renders: the shared component, or a view-local one.
const PAGER = '.pagination, [class$="-pagination"]';

// What the reader has in front of them, read in one go so a page change cannot
// land halfway through it. The height is the span the rows occupy rather than
// the height of the document: these views carry secondary panels of their own,
// and this test is about the list.
function readList([rowsSelector, pagerSelector]: [string, string]) {
  const rowEls = document.querySelectorAll(rowsSelector);
  const pager = document.querySelector(pagerSelector);
  // Going back is the way out of a slow page 2, so that is the button that has
  // to stay usable; the forward one is rightly disabled on the last page.
  const back = pager?.querySelector('button:first-of-type') as HTMLButtonElement | null;
  // The sign that a request is out: the refresh button spins (RefreshControl).
  const spinner = document.querySelector('.refresh-control-spinning');
  const list = rowEls[0]?.parentElement ?? null;
  return {
    rows: rowEls.length,
    height: rowEls.length
      ? Math.round(
          rowEls[rowEls.length - 1].getBoundingClientRect().bottom -
            rowEls[0].getBoundingClientRect().top,
        )
      : 0,
    pagerVisible: !!pager && (pager as HTMLElement).offsetParent !== null,
    backEnabled: !!back && !back.disabled,
    spinning: !!spinner,
    spinnerOutsideList: !!spinner && !!list && !list.contains(spinner),
  };
}

interface PagedView {
  name: string;
  /** Path under /organizations/demo. */
  path: string;
  /** Rows of the list the reader is looking at. */
  rows: string;
  /** Demo seams the view needs, set before the page scripts run. */
  demoKeys?: Record<string, string>;
}

// The four views the story names for the page change. Incidents holds seven
// real rows against a page of fifty, so its demo list is padded to have a
// second page at all.
const PAGED_VIEWS: PagedView[] = [
  { name: 'Incidents', path: 'alerts/incidents', rows: '.incidents-list .incident-card', demoKeys: { 'mm.demo.incidents-padding': '60' } },
  { name: 'Profiling', path: 'profiling', rows: '.profiling-table tbody tr' },
  { name: 'Logs', path: 'logs', rows: '.logs-table tbody tr.log-row' },
  { name: 'Traces', path: 'traces', rows: '.traces-table tbody tr' },
];

for (const view of PAGED_VIEWS) {
  test(`${view.name} keeps the rows and the pager while the next page loads`, async ({ page }) => {
    if (view.demoKeys) {
      await page.addInitScript((keys: Record<string, string>) => {
        for (const [key, value] of Object.entries(keys)) sessionStorage.setItem(key, value);
      }, view.demoKeys);
    }
    await enterDemo(page, view.path);

    const rows = page.locator(view.rows);
    await expect(rows.first()).toBeVisible();

    const before = await page.evaluate(readList, [view.rows, PAGER] as [string, string]);

    await slowDownRequests(page);

    // The manual refresh button reports the view's own request: disabled while
    // one is in flight (RefreshControl).
    const fetching = page.locator('.refresh-control-button');
    await page.locator(PAGER).first().locator('button').last().click();
    await expect(fetching).toBeDisabled();

    // Read while the request is out and the next page has not landed.
    const during = await page.evaluate(readList, [view.rows, PAGER] as [string, string]);
    // And it still was when that snapshot was taken, so it is not the state
    // after the page landed.
    await expect(fetching).toBeDisabled();

    // The rows of the page being left are still on screen, and the area they
    // sit in has not collapsed under them.
    expect(during.rows).toBe(before.rows);
    expect(during.height).toBeGreaterThanOrEqual(before.height);
    // The pager is the way out of a slow page: it must stay there and stay
    // usable rather than be unmounted for the duration.
    expect(during.pagerVisible).toBe(true);
    expect(during.backEnabled).toBe(true);
    // The reader is still told a request is out — beside the list, not in its
    // place.
    expect(during.spinning).toBe(true);
    expect(during.spinnerOutsideList).toBe(true);

    await expect(page).toHaveURL(/[?&]page=2/);
    await expect(fetching).toBeEnabled();
    await expect(rows.first()).toBeVisible();
  });
}

// One group per service in the sandbox, and the drill-down is where the group
// rows live.
const ERRORS_PATH = 'errors?service=payment-service';
const ERROR_ROW = '.card div[role="button"]';

test('an open error detail survives a new occurrence', async ({ page }) => {
  await enterDemo(page, ERRORS_PATH);

  const row = page.locator(ERROR_ROW).first();
  await expect(row).toBeVisible();

  // Hold on to the row node itself. The demo derives last_seen and sample_id
  // from Date.now() (errorLastSeen / errorSampleId, src/demo/demoApi.ts), as the
  // backend does for the group's latest occurrence, so every refresh carries a
  // newer one — which is what a key built on either turns into a remount,
  // throwing away the node and everything attached to it.
  await page.evaluate((selector) => {
    (window as unknown as { __row: Element | null }).__row = document.querySelector(selector);
  }, ERROR_ROW);

  await row.click();
  const detail = page.locator('.modal-overlay');
  await expect(detail).toBeVisible();

  // Read further down the detail, which is the position a refresh must not
  // take back. The panel scrolls in its own box.
  const panel = detail.locator('> .card');
  await panel.evaluate((el) => el.scrollTo(0, el.scrollHeight));
  const scrollTop = await panel.evaluate((el) => Math.round(el.scrollTop));
  expect(scrollTop).toBeGreaterThan(0);

  // dispatchEvent, not click: the overlay is over the button, and a real click
  // would scroll it into view — the position is what this test is about.
  const status = page.getByTestId('refresh-status').first();
  const previous = await status.getAttribute('data-last-success');
  await page.locator('.refresh-control-button').dispatchEvent('click');
  await expect
    .poll(() => status.getAttribute('data-last-success'), { timeout: 20_000 })
    .not.toBe(previous);

  await expect(detail).toBeVisible();
  expect(await panel.evaluate((el) => Math.round(el.scrollTop))).toBe(scrollTop);
  // The row was updated, not rebuilt: same node as before the refresh.
  const sameNode = await page.evaluate(
    (selector) =>
      (window as unknown as { __row: Element | null }).__row === document.querySelector(selector),
    ERROR_ROW,
  );
  expect(sameNode).toBe(true);
});
