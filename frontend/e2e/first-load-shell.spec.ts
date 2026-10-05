import { test, expect, type Page } from '@playwright/test';

import { enterDemo as openDemo } from './demo';

// Story #6: on a slow first load the user must still be able to read where they
// are and start typing a filter. Each view therefore keeps its title, filters,
// search and pager mounted, and shows a skeleton only where the results go.
//
// Demo mode answers within the tick, so a first load would never be observable:
// mm.demo.latency holds every demo response back by that many milliseconds
// (see src/demo/demoApi.ts), which is the frontend-only stand-in for a slow
// network. HealthView and MetricsView are in the story's ten but have no route
// in App.tsx, so they cannot be driven from here.

// Held long enough that a cold Vite transform plus the navigation cannot eat
// the window the first assertions read: below that the data is already there
// and the skeleton assertions fail for the wrong reason.
const LATENCY_MS = 8000;

// The load these tests drive is deliberate, so it counts against the test
// budget: the default 30 s leaves too little once the dev server is cold.
test.describe.configure({ timeout: 60_000 });

// The init script is installed before the helper navigates, so the seams are
// already set when the demo API answers its first read.
async function enterDemo(page: Page, instantReads = '') {
  await page.addInitScript(([ms, instant]) => {
    sessionStorage.setItem('mm.demo.latency', ms);
    if (instant) sessionStorage.setItem('mm.demo.instant-reads', instant);
  }, [String(LATENCY_MS), instantReads]);
  await openDemo(page);
}

interface ViewCase {
  name: string;
  path: string;
  /** Tells the user which view they are on, before any data arrives. */
  header: string;
  /**
   * Filter, search or period control that must be usable during the load.
   * Omitted where the view has none: overview is read-only, and the only
   * control of host groups is its create button, which a read-only session
   * (demo mode) never renders.
   */
  control?: string;
}

const VIEWS: ViewCase[] = [
  { name: 'overview', path: '', header: '.page-title' },
  { name: 'hosts', path: 'hosts', header: '.page-title', control: '.hosts-search-input' },
  { name: 'host groups', path: 'hosts/groups', header: '.page-title' },
  { name: 'host detail', path: 'hosts/1', header: '.host-detail-back', control: '.services-search-input' },
  { name: 'services', path: 'services', header: '.page-title', control: '.services-search-input' },
  { name: 'service detail', path: 'services/5', header: '.service-detail-back', control: '.service-detail-date-trigger' },
  { name: 'timeline', path: 'timeline', header: '.page-title', control: '.date-range-trigger' },
  { name: 'errors', path: 'errors', header: '.page-title', control: '.date-range-trigger' },
];

for (const view of VIEWS) {
  test(`${view.name} keeps its shell while its first load runs`, async ({ page }) => {
    await enterDemo(page);
    await page.goto(`/organizations/demo/${view.path}`);

    // The result area is the last skeleton on the page: a detail view puts a
    // smaller one where its own header will be.
    const skeleton = page.locator('.skeleton').last();
    await expect(skeleton).toBeVisible();

    // The shell is what the user reads and acts on while the data is missing.
    await expect(page.locator(view.header).first()).toBeVisible();
    if (view.control) {
      await expect(page.locator(view.control).first()).toBeVisible();
    }

    // A spinner covering the content area is the defect being fixed.
    await expect(page.locator('.loading')).toHaveCount(0);

    // The skeleton stands in for the list, so what sits below it does not climb
    // to the top of the page and drop back down when the rows arrive.
    const box = await skeleton.boundingBox();
    expect(box?.height ?? 0).toBeGreaterThan(150);

    await expect(page.locator('.skeleton')).toHaveCount(0, { timeout: 15_000 });
    await expect(page.locator(view.header).first()).toBeVisible();
  });
}

// Typing before the data lands is the point of keeping the search mounted: a
// remount would drop both the text and the caret.
for (const view of [
  { name: 'hosts', path: 'hosts', search: '.hosts-search-input' },
  { name: 'services', path: 'services', search: '.services-search-input' },
  { name: 'host detail', path: 'hosts/1', search: '.services-search-input' },
]) {
  test(`${view.name} keeps text typed into its search during the first load`, async ({ page }) => {
    await enterDemo(page);
    await page.goto(`/organizations/demo/${view.path}`);

    // Typed while the results are still a skeleton, not after they land.
    await expect(page.locator('.skeleton').last()).toBeVisible();
    const search = page.locator(view.search);
    await expect(search).toBeVisible();
    await search.click();
    await search.pressSequentially('web');

    await expect(page.locator('.skeleton')).toHaveCount(0, { timeout: 15_000 });
    await expect(search).toHaveValue('web');
    await expect(search).toBeFocused();
  });
}

// A supervision product must not read "0 failing" while it has no data yet: the
// figure is false and reassuring for as long as the load runs, and the spinner
// used to hide it.
for (const view of [
  { name: 'hosts', path: 'hosts', value: '.hosts-stat-value' },
  { name: 'services', path: 'services', value: '.services-stat-value' },
]) {
  test(`${view.name} shows no count while its first load runs`, async ({ page }) => {
    await enterDemo(page);
    await page.goto(`/organizations/demo/${view.path}`);

    await expect(page.locator('.skeleton').last()).toBeVisible();
    await expect(page.locator('.page-subtitle')).not.toContainText(/\d/);
    const values = page.locator(view.value);
    expect(await values.count()).toBeGreaterThan(0);
    for (const text of await values.allTextContents()) {
      expect(text.trim()).toBe('—');
    }

    await expect(page.locator('.skeleton')).toHaveCount(0, { timeout: 15_000 });
    expect((await values.allTextContents()).join('')).toMatch(/\d/);
  });
}

// The stat cards and the list are two requests, so they have two first loads.
// Gating the cards on the list is wrong in both directions: the dash outlives
// counts that have arrived, and a list that answers first shows "0 failing"
// while the stats are still in flight. Here the stats answer within the tick
// and the list stays held back.
test('hosts stat cards wait on their own request, not on the list', async ({ page }) => {
  await enterDemo(page, 'hosts.stats');
  await page.goto('/organizations/demo/hosts');

  // The list has not landed, so the result area is still a skeleton.
  await expect(page.locator('.skeleton').last()).toBeVisible();

  // The counts have, and a dash here would hide data the user could read.
  await expect(page.locator('.page-subtitle')).toContainText(/\d/);
  expect((await page.locator('.hosts-stat-value').allTextContents()).join('')).toMatch(/\d/);

  // The table title counts rows, so it is the one that follows the list.
  await expect(page.locator('.card-title').first()).toContainText('—');
});
