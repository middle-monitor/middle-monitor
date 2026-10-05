import { test, expect } from '@playwright/test';

import { enterDemo } from './demo';

// A view reads several queries; the ones that only feed a filter or a stat bar
// have no error branch of their own, so their data stays undefined when they
// fail for good. Anything derived from that fallback must keep a stable
// identity across renders, or the memo that reads it re-runs, the effect that
// depends on the memo sets state, and the view loops until the ErrorBoundary
// catches it. Regression cover for Incidents, where EMPTY_STATS was rebuilt on
// every render.

test('Incidents stays usable when its stats query fails for good', async ({ page }) => {
  const renderErrors: string[] = [];
  page.on('console', (msg) => {
    if (msg.type() === 'error' && msg.text().includes('Maximum update depth exceeded')) {
      renderErrors.push(msg.text());
    }
  });

  await page.addInitScript(() => {
    sessionStorage.setItem('mm.demo.fail-reads', 'incidents.stats');
  });
  await enterDemo(page);

  await page.goto('/organizations/demo/alerts/incidents');
  await expect(page.locator('h1').first()).toBeVisible();

  // The list query is untouched, so the incidents themselves still render.
  await expect(page.locator('.incident-card').first()).toBeVisible();

  // Typing drives the autocomplete, which is what reads the failed stats.
  await page.locator('input.incidents-search').fill('service:');
  await expect(page.locator('h1').first()).toBeVisible();

  expect(renderErrors).toEqual([]);
});
