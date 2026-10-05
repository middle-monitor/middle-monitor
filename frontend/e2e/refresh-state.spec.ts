import { test, expect, type Page, type TestInfo } from '@playwright/test';

import { enterDemo } from './demo';
import { LOADING_SELECTORS } from './loading-selectors';

// Story #4: the net every view of the epic-3 migration is replayed against,
// before and after it moves. A refresh cycle, or a write, must not cost the
// reader their place: once the data is back, the filter they picked, the page
// they are on and where they had scrolled to are the ones they left.
//
// To run it on another view, add one entry to VIEWS: the path under the demo
// org and the selector of its list rows. Everything else is optional and
// describes what that view actually has — a filter, a second page, a demo seam
// it needs — because the ten views of story #5 do not all have all three. The
// scenario, the cadence and the INP measurement are shared. A filter must still
// leave more than one page of rows on a paginated view: the scenario reads
// page 2, and that page has to be long enough to scroll or there is no
// position to lose there.
//
// Demo mode is the only backend CI has (frontend-only, fixed data, see
// src/demo/demoApi.ts), and it filters and paginates for real.

interface Filter {
  /** A select to pick from, or a text box to type in and submit. */
  selector: string;
  value: string;
}

interface ViewUnderTest {
  name: string;
  /** Path under /organizations/demo. */
  path: string;
  /** Rows of the list the reader is scrolled into. */
  rows: string;
  /** Where the view offers one. */
  filter?: Filter;
  /** Set where the demo list runs past one page: the scenario then reads page 2. */
  paginated?: boolean;
  /** Set where the whole view fits on screen, so there is no scroll position to lose. */
  fitsOnScreen?: boolean;
  /** Demo seams the view needs, set before the page scripts run (src/demo/demoApi.ts). */
  demoKeys?: Record<string, string>;
}

// Filler incidents the demo list is padded with (src/demo/demoApi.ts): seven
// real ones against a page of fifty leave no second page to read.
const INCIDENTS_PADDING = 60;

const VIEWS: ViewUnderTest[] = [
  {
    name: 'Logs',
    path: 'logs',
    rows: '.logs-table tbody tr.log-row',
    filter: { selector: '.logs-filter-controls select', value: 'INFO' },
    paginated: true,
  },
  {
    name: 'Traces',
    path: 'traces',
    rows: '.traces-table tbody tr',
    // Not a service name: the demo holds barely more than one page of
    // api-gateway spans, which left page 2 too short to have a scroll position
    // to lose. An operation term keeps four pages, so page 2 is a full one.
    filter: { selector: '.traces-search-form input', value: 'POST' },
    paginated: true,
  },
  {
    name: 'Incidents',
    path: 'alerts/incidents',
    rows: '.incidents-list .incident-card',
    filter: { selector: '.incidents-search', value: 'Storefront' },
    paginated: true,
    demoKeys: { 'mm.demo.incidents-padding': String(INCIDENTS_PADDING) },
  },
  {
    name: 'Profiling',
    path: 'profiling',
    rows: '.profiling-table tbody tr',
    filter: { selector: '.profiling-ram-filters select', value: 'payment-service' },
    paginated: true,
  },
  {
    name: 'Alert Rules',
    path: 'alerts/rules',
    rows: '.alert-rules-list .alert-rule-card',
  },
  {
    name: 'Network',
    path: 'network',
    rows: '.network-hosts a',
    filter: { selector: '.network-host-select', value: 'web-01 (web-01 network)' },
  },
  {
    name: 'Notification Channels',
    path: 'alerts/channels',
    rows: '.notification-channels-view .card',
    fitsOnScreen: true,
  },
  {
    name: 'Metrics Explorer',
    path: 'metrics',
    // The chart is this view's list: it is what the loading state replaces.
    rows: '.metrics-chart-card svg',
    filter: { selector: '.metrics-explorer-filters select:first-of-type', value: 'web-01' },
  },
  {
    // The keys list is a section of the settings page, which turns anyone but
    // an admin away — hence the seam.
    name: 'API Keys',
    path: 'settings',
    rows: '.api-keys-table tbody tr',
    demoKeys: { 'mm.demo.admin': '1' },
  },
  {
    name: 'Errors',
    path: 'errors',
    rows: '.card table.table tbody tr',
  },
];

// The view the epic's write goes through. It is not in VIEWS: the scenario
// there is a refresh cycle, here it is a save. The demo org holds fewer
// services than one page, so the list is padded below to put the write on a
// paginated view — a save that drops the reader back to page 1 is exactly what
// this has to catch.
const WRITE_VIEW: ViewUnderTest & { filter: Filter } = {
  name: 'Services (write)',
  path: 'services',
  filter: { selector: '.services-type-filter', value: 'http' },
  rows: '.services-table tbody tr',
};

// Filler services the write view's list is padded with (src/demo/demoApi.ts).
// The view asks for 50 a page, so this leaves a second page well filled enough
// to be scrolled — and the created service lands on it, at the end of the list.
const WRITE_VIEW_PADDING = 60;

// Fastest cadence the refresh control offers, so a full cycle fits in a test.
const CADENCE_MS = 5000;

// Demo answers within the tick, so a view that dropped its list and put it back
// in the same commit would never be caught: nothing would ever be missing on
// screen. This holds the refresh back (src/demo/demoApi.ts), which is how a
// list taken away becomes observable — and how the scroll gets the chance to
// collapse with it.
const REFRESH_LATENCY_MS = 1500;

async function slowDownRefreshes(page: Page) {
  await page.evaluate((ms) => sessionStorage.setItem('mm.demo.latency', String(ms)), REFRESH_LATENCY_MS);
}

// A short viewport: the reader has to be scrolled down the list for losing that
// position to be observable at all.
test.use({ viewport: { width: 1000, height: 520 } });

// A refresh cycle plus a cold dev server does not fit the default timeout.
test.describe.configure({ timeout: 60_000 });

// Whichever pager the view renders: the shared component, or a view-local one.
const pager = (page: Page) => page.locator('.pagination, [class$="-pagination"]').first();

async function applyFilter(page: Page, { selector, value }: Filter) {
  const control = page.locator(selector);
  if (await control.evaluate((el) => el.tagName === 'SELECT')) {
    await control.selectOption(value);
  } else {
    await control.fill(value);
    await control.press('Enter');
  }
  // The epic put filters in the URL, so that is where the pick lands.
  await expect
    .poll(() => [...new URL(page.url()).searchParams.values()])
    .toContain(value);
  // Then wait for the link to stop moving: the incidents search box debounces,
  // and drops the page number when it fires. Reading page 2 before that would
  // race the reset. Two reads a debounce apart is settled.
  let previous = '';
  await expect
    .poll(
      () => {
        const current = page.url();
        const settled = current === previous;
        previous = current;
        return settled;
      },
      { intervals: [300, 300, 300, 300, 300] },
    )
    .toBe(true);
}

// Where the reader is. The filter and the page number are read from the link
// too: since the epic put them there, a lost filter is a changed URL.
interface Place {
  filter: string | null;
  url: string;
  scrollY: number;
}

async function place(page: Page, filterSelector?: string): Promise<Place> {
  return {
    filter: filterSelector ? await page.locator(filterSelector).inputValue() : null,
    url: new URL(page.url()).search,
    scrollY: await page.evaluate(() => Math.round(window.scrollY)),
  };
}

// Story #5: the first load is the one time a loading state may stand in for the
// list — there is nothing else to show. It only lives as long as the first
// response takes, so it is recorded from before the app runs rather than polled
// for, along with how many rows were up at that moment.
const FIRST_LOAD_SELECTORS = `${LOADING_SELECTORS}, .skeleton`;

async function watchFirstLoad(page: Page, rows: string) {
  await page.addInitScript(([loading, rowsSelector]) => {
    const w = window as unknown as { __firstLoad: { seen: boolean; rowsThen: number } };
    w.__firstLoad = { seen: false, rowsThen: -1 };
    new MutationObserver((records) => {
      if (w.__firstLoad.seen) return;
      for (const record of records) {
        for (const node of Array.from(record.addedNodes)) {
          if (!(node instanceof Element)) continue;
          if (!node.matches(loading) && !node.querySelector(loading)) continue;
          w.__firstLoad = { seen: true, rowsThen: document.querySelectorAll(rowsSelector).length };
          return;
        }
      }
      // document, not documentElement: this runs before the parser has created it.
    }).observe(document, { childList: true, subtree: true });
  }, [FIRST_LOAD_SELECTORS, rows]);
}

const firstLoad = (page: Page) =>
  page.evaluate(() => (window as unknown as { __firstLoad: { seen: boolean; rowsThen: number } }).__firstLoad);

// A view that goes back to swapping its list for a loading state empties it for
// a frame or two, which is too short to poll for but not to observe.
async function watchRows(page: Page, rows: string) {
  await page.evaluate((selector) => {
    const w = window as unknown as { __minRows: number };
    const count = () => document.querySelectorAll(selector).length;
    w.__minRows = count();
    new MutationObserver(() => {
      w.__minRows = Math.min(w.__minRows, count());
    }).observe(document.body, { childList: true, subtree: true });
  }, rows);
}

const rowsLowWaterMark = (page: Page) =>
  page.evaluate(() => (window as unknown as { __minRows: number }).__minRows);

// Reaching the control scrolls it back into view, so the cadence is picked
// while the reader is still at the top — before the scroll under test.
async function startRefreshing(page: Page) {
  await page.getByTestId('refresh-control').first().locator('select').selectOption(String(CADENCE_MS));
}

// The control stamps the time of the last successful refresh: a new stamp is a
// cycle that went all the way to the data.
async function waitForRefreshCycle(page: Page) {
  const status = page.getByTestId('refresh-status').first();
  const previous = await status.getAttribute('data-last-success');
  await expect
    .poll(() => status.getAttribute('data-last-success'), { timeout: 20_000 })
    .not.toBe(previous);
}

// INP, read from the browser rather than from a library: event timing entries
// are the same source web-vitals reads. The entry spec reports nothing under
// 16 ms, so a view that never crosses that floor scores 0.
async function measureInteractions(page: Page) {
  await page.addInitScript(() => {
    const w = window as unknown as { __interactions: Map<number, number> };
    w.__interactions = new Map();
    new PerformanceObserver((list) => {
      for (const entry of list.getEntries() as PerformanceEventTiming[]) {
        if (!entry.interactionId) continue;
        const worst = Math.max(w.__interactions.get(entry.interactionId) ?? 0, entry.duration);
        w.__interactions.set(entry.interactionId, worst);
      }
    }).observe({ type: 'event', buffered: true, durationThreshold: 16 });
  });
}

// INP over a visit this short is its slowest interaction: the percentile rule
// only starts discarding outliers past fifty of them. inp-reporter.ts turns the
// per-view values into the 75th percentile the CI job prints.
async function reportInp(page: Page, view: string, testInfo: TestInfo) {
  const inpMs = await page.evaluate(() =>
    Math.max(
      0,
      ...(window as unknown as { __interactions: Map<number, number> }).__interactions.values(),
    ),
  );
  await testInfo.attach('inp', {
    body: JSON.stringify({ view, inpMs }),
    contentType: 'application/json',
  });
}

for (const view of VIEWS) {
  test(`${view.name} keeps the filter, the page and the scroll through a refresh`, async ({
    page,
  }, testInfo) => {
    await measureInteractions(page);
    if (view.demoKeys) {
      await page.addInitScript((keys: Record<string, string>) => {
        for (const [key, value] of Object.entries(keys)) sessionStorage.setItem(key, value);
      }, view.demoKeys);
    }
    await watchFirstLoad(page, view.rows);
    await enterDemo(page, view.path);

    const rows = page.locator(view.rows);
    await expect(rows.first()).toBeVisible();

    // The first load had no data to keep, so it was right to stand a loading
    // state where the rows go — and there were none up behind it.
    expect(await firstLoad(page)).toEqual({ seen: true, rowsThen: 0 });

    if (view.filter) {
      await applyFilter(page, view.filter);
      await expect(rows.first()).toBeVisible();
    }

    if (view.paginated) {
      await pager(page).locator('button').last().click();
      await expect(page).toHaveURL(/[?&]page=2/);
      await expect(rows.first()).toBeVisible();
    }

    await slowDownRefreshes(page);
    await startRefreshing(page);
    // Picking a cadence fires a cycle straight away, and the panels a view
    // carries beside its list have their own loading heights: let it land, so
    // the position below is read from a settled page and not from one mid-cycle.
    await expect(page.locator('.refresh-control-button').first()).toBeEnabled();

    // Halfway down whatever the view has to scroll. Not to the very bottom: a
    // page pinned there follows its own height, and these views carry secondary
    // panels whose height moves on its own. 'instant' because the app scrolls
    // smoothly (index.css), and an animation still running would be read as a
    // position that moved by itself.
    await page.evaluate(() => {
      const max = document.documentElement.scrollHeight - window.innerHeight;
      window.scrollTo({ top: Math.round(max / 2), behavior: 'instant' });
    });
    const before = await place(page, view.filter?.selector);
    // Otherwise the scroll assertion below would hold on an unscrolled page.
    // A view that fits on screen has no position to lose: there, the rows and
    // the URL carry the scenario.
    if (!view.fitsOnScreen) expect(before.scrollY).toBeGreaterThan(0);

    await watchRows(page, view.rows);
    await waitForRefreshCycle(page);

    expect(await place(page, view.filter?.selector)).toEqual(before);
    // And the list was never taken off screen on the way there.
    expect(await rowsLowWaterMark(page)).toBeGreaterThan(0);

    await reportInp(page, view.name, testInfo);
  });
}

test(`${WRITE_VIEW.name} keeps the filter, the page and the scroll through a creation`, async ({
  page,
}, testInfo) => {
  await measureInteractions(page);
  // Both keys are read as the page's own scripts run, so they cannot be set
  // after navigating: writes once by AuthContext, the padding once by demoApi.
  await page.addInitScript((padding) => {
    sessionStorage.setItem('mm.demo.writes', '1');
    sessionStorage.setItem('mm.demo.services-padding', String(padding));
  }, WRITE_VIEW_PADDING);
  await enterDemo(page, WRITE_VIEW.path);

  const rows = page.locator(WRITE_VIEW.rows);
  await expect(rows.first()).toBeVisible();

  await applyFilter(page, WRITE_VIEW.filter);
  await expect(page).toHaveURL(/[?&]type=http/);

  await pager(page).locator('button').last().click();
  await expect(page).toHaveURL(/[?&]page=2/);
  await expect(rows.first()).toBeVisible();

  await page.evaluate(() => window.scrollTo(0, 200));

  // dispatchEvent, not click: clicking scrolls the button back into view, and
  // the scroll position is what this test is about.
  await page.locator('.services-view .page-header button.btn-primary').dispatchEvent('click');
  const form = page.locator('.modal-content');
  await expect(form).toBeVisible();
  await form.locator('input[name="name"]').fill('Checkout canary');
  await form.locator('input[name="host"]').fill('https://canary.novashop.example');
  await form.locator('#service-modal-host-id').selectOption({ index: 1 });

  await slowDownRefreshes(page);

  // Filling the form is setup; the save is what must leave this untouched.
  const before = await place(page, WRITE_VIEW.filter.selector);
  expect(before.scrollY).toBeGreaterThan(0);
  await watchRows(page, WRITE_VIEW.rows);

  // A real click, unlike the one that opened the modal: the modal scrolls in
  // its own box, so reaching its footer leaves the page where it is.
  await form.locator('button[type="submit"]').click();
  await expect(form).toBeHidden();
  // The list took the new service in place, without going away first. It is
  // the last row of the last page, so seeing it is also seeing page 2 held.
  await expect(page.getByText('Checkout canary')).toBeVisible();
  expect(await rowsLowWaterMark(page)).toBeGreaterThan(0);

  expect(await place(page, WRITE_VIEW.filter.selector)).toEqual(before);

  await reportInp(page, WRITE_VIEW.name, testInfo);
});
