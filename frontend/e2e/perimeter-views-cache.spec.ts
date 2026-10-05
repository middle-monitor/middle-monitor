import { test, expect, type Page } from '@playwright/test';

import { enterDemo } from './demo';
import { LOADING_SELECTORS } from './loading-selectors';

// The shared net for the migration: every perimeter view reached again must
// paint from cache, with no loading screen in between. Before the migration
// each view refetched from scratch on mount and blanked its content area, so
// simply walking away and coming back cost a full loading screen.
// Demo mode goes through orgApi like a signed-in org, so this is also the
// per-view demo check.

const DEMO_BASE = '/organizations/demo';

// Perimeter views reachable from the sidebar in demo mode. Metrics, Health and
// API Keys are the three that are not: the first two have no route, and org
// settings redirect the demo user before API Keys can render.
const PERIMETER = [
  { name: 'Overview', path: '' },
  { name: 'Services', path: '/services' },
  { name: 'Hosts', path: '/hosts' },
  { name: 'Host Groups', path: '/hosts/groups' },
  { name: 'Network', path: '/network' },
  { name: 'Metrics Explorer', path: '/metrics' },
  { name: 'Traces', path: '/traces' },
  { name: 'Profiling', path: '/profiling' },
  { name: 'Errors', path: '/errors' },
  { name: 'Logs', path: '/logs' },
  { name: 'Timeline', path: '/timeline' },
  { name: 'Incidents', path: '/alerts/incidents' },
  { name: 'Alert Rules', path: '/alerts/rules' },
  { name: 'Notification Channels', path: '/alerts/channels' },
];

// A marker each detour only paints once its own data is in: the detour's own
// loading indicator is one of LOADING_SELECTORS, so arming the observer while
// the detour still loads would flag a mutation the tested view never made.
const DETOUR_LOADED: Record<string, string> = {
  '/logs': '.logs-table, .logs-table-card .empty-state',
  '/timeline': '.card .card-title',
};

// The two perimeter views with no sidebar entry: they are reached by clicking a
// row, and left again with the back button. The PO asked for them in the shared
// net, since demo mode reaches both.
const DETAIL_VIEWS = [
  {
    name: 'Host Detail',
    list: '/hosts',
    open: (page: Page) => page.locator('tr.hosts-row-clickable').first().click(),
    heading: '.host-detail-title',
  },
  {
    // By name: the first services row may be an error service, which drills
    // down into the Errors view instead.
    name: 'Service Detail',
    list: '/services',
    open: (page: Page) => page.locator('tr.services-row-clickable', { hasText: 'Back-office' }).first().click(),
    heading: '.service-detail-title',
  },
];

function sidebarLink(page: Page, path: string) {
  return page.locator(`a.sidebar-item[href="${DEMO_BASE}${path || '/'}"], a.sidebar-item[href="${DEMO_BASE}${path}"]`).first();
}

// Records whether a loading indicator is ever inserted from now on. Polling for
// it would miss one that only lives for a frame, and reading the inserted nodes
// rather than the document catches one that is removed again in the same batch.
async function watchForLoading(page: Page, selectors: string) {
  await page.evaluate((sel) => {
    (window as unknown as { __sawLoading: boolean }).__sawLoading = false;
    new MutationObserver((records) => {
      for (const record of records) {
        for (const node of Array.from(record.addedNodes)) {
          if (!(node instanceof Element)) continue;
          if (node.matches(sel) || node.querySelector(sel)) {
            (window as unknown as { __sawLoading: boolean }).__sawLoading = true;
          }
        }
      }
    }).observe(document.body, { childList: true, subtree: true });
  }, selectors);
}

function sawLoading(page: Page) {
  return page.evaluate(() => (window as unknown as { __sawLoading: boolean }).__sawLoading);
}

for (const view of PERIMETER) {
  test(`${view.name} paints from cache when the reader comes back`, async ({ page }) => {
    await enterDemo(page);

    await sidebarLink(page, view.path).click();
    const heading = page.locator('h1').first();
    await expect(heading).toBeVisible();
    const title = await heading.textContent();

    // Client-side navigation only: page.goto would reload the app and wipe the
    // in-memory cache, which is the very thing under test.
    const detour = view.path === '/logs' ? '/timeline' : '/logs';
    await sidebarLink(page, detour).click();
    await expect(page).toHaveURL(new RegExp(`${detour}$`));
    await expect(page.locator(DETOUR_LOADED[detour]).first()).toBeVisible();

    await watchForLoading(page, LOADING_SELECTORS);
    await sidebarLink(page, view.path).click();

    // Immediately: the cached data is already there, the view then revalidates
    // behind it.
    await expect(heading).toHaveText(title ?? '', { timeout: 2_000 });
    expect(await sawLoading(page)).toBe(false);
  });
}

for (const view of DETAIL_VIEWS) {
  test(`${view.name} paints from cache when the reader comes back`, async ({ page }) => {
    await enterDemo(page);

    await sidebarLink(page, view.list).click();
    await view.open(page);
    const heading = page.locator(view.heading);
    await expect(heading).toBeVisible();
    const title = await heading.textContent();

    await sidebarLink(page, '/logs').click();
    await expect(page).toHaveURL(/\/logs$/);
    await expect(page.locator(DETOUR_LOADED['/logs']).first()).toBeVisible();

    await watchForLoading(page, LOADING_SELECTORS);
    // No sidebar entry to click: the reader comes back with the back button,
    // which React Router handles without reloading the app.
    await page.goBack();

    await expect(heading).toHaveText(title ?? '', { timeout: 2_000 });
    expect(await sawLoading(page)).toBe(false);
  });
}
