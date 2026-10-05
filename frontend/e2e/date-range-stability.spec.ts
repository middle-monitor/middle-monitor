import { test, expect } from '@playwright/test';

import { enterDemo } from './demo';

// DateRangeProvider re-computes a relative window every 15s so the range keeps
// sliding. Views must not treat that tick as a reason to reload: the reader did
// not ask for anything. Logs is the visible witness — it swaps its list for a
// loading line on every fetch, so an unwanted reload cannot hide.

test('a sliding relative range does not reload the logs view on its own', async ({ page }) => {
  // Fake clock: the provider's tick fires on fast forward, so the window slides
  // half an hour without the test waiting for it.
  await page.clock.install();

  await enterDemo(page);
  await page.locator('a.sidebar-item[href="/organizations/demo/logs"]').click();
  const newest = page.locator('.log-timestamp').first();
  await expect(newest).toBeVisible();
  const newestBefore = await newest.textContent();
  const rangeLabel = page.locator('.date-range-trigger');
  const labelBefore = await rangeLabel.textContent();

  await page.evaluate(() => {
    (window as unknown as { __reloaded: boolean }).__reloaded = false;
    new MutationObserver(() => {
      if (document.querySelector('.logs-loading')) {
        (window as unknown as { __reloaded: boolean }).__reloaded = true;
      }
    }).observe(document.body, { childList: true, subtree: true });
  });

  await page.clock.fastForward('30:00');

  // The window did slide: the label moved with the clock. Without this, an
  // absence of reload would also be what a provider that stopped ticking, or a
  // getter that froze its bounds, looks like.
  await expect.poll(() => rangeLabel.textContent()).not.toBe(labelBefore);
  expect(await page.evaluate(() => (window as unknown as { __reloaded: boolean }).__reloaded)).toBe(false);

  // And the bounds still follow it: the next fetch the reader does ask for
  // comes back with logs from the window that slid, not from the frozen one.
  await page.getByTestId('refresh-control').getByRole('button').click();
  await expect.poll(() => newest.textContent()).not.toBe(newestBefore);
});
