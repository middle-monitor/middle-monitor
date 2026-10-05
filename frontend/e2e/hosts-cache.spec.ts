import { test, expect, type Page } from '@playwright/test';

import { enterDemo } from './demo';

// Hosts is the pilot view for the shared query cache: it reads through orgApi,
// so demo mode exercises the exact same code path as a signed-in org.
function sidebarLink(page: Page, path: string) {
  return page.locator(`a.sidebar-item[href="/organizations/demo${path}"]`);
}

// Records whether a full-view loading screen is ever inserted from now on.
// Polling for it would miss a spinner that only lives for a frame.
async function watchForLoadingScreen(page: Page) {
  await page.evaluate(() => {
    (window as unknown as { __sawLoading: boolean }).__sawLoading = false;
    new MutationObserver(() => {
      if (document.querySelector('.loading')) {
        (window as unknown as { __sawLoading: boolean }).__sawLoading = true;
      }
    }).observe(document.body, { childList: true, subtree: true });
  });
}

function sawLoadingScreen(page: Page) {
  return page.evaluate(() => (window as unknown as { __sawLoading: boolean }).__sawLoading);
}

test('coming back to the hosts view paints the cached list, with no loading screen', async ({ page }) => {
  // One auto-refresh cycle (15s) plus the app boot does not fit the default timeout.
  test.setTimeout(60_000);

  await enterDemo(page);
  await sidebarLink(page, '/hosts').click();
  const rows = page.locator('.hosts-table tbody tr');
  await expect(rows.first()).toBeVisible();
  const rowsOnFirstVisit = await rows.count();

  // Client-side navigation only: page.goto would reload the app and wipe the
  // in-memory cache, which is the very thing under test.
  await sidebarLink(page, '/timeline').click();
  await expect(page).toHaveURL(/\/timeline$/);

  await watchForLoadingScreen(page);
  await sidebarLink(page, '/hosts').click();
  await expect(rows).toHaveCount(rowsOnFirstVisit);
  expect(await sawLoadingScreen(page)).toBe(false);

  // The view then revalidates on its own cadence; the list must stay on screen
  // while it does, not be replaced by a spinner.
  await page.waitForTimeout(16_000);
  await expect(rows.first()).toBeVisible();
  expect(await sawLoadingScreen(page)).toBe(false);
});
