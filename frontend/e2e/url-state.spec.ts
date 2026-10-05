import { test, expect } from '@playwright/test';

import { enterDemo } from './demo';

// Filters, search and pagination must be readable from the URL: that is what
// makes a refresh, a second tab and a link sent to a colleague show the same
// list. Demo mode is the only backend CI has, and it filters and paginates for
// real (see src/demo/demoApi.ts), so it is what these tests drive.
test('a services filter and search reach the URL as they change', async ({ page }) => {
  await enterDemo(page, 'services');

  await page.locator('.services-stat-failing').click();
  await expect(page).toHaveURL(/[?&]status=failing/);

  await page.locator('.services-search-input').fill('webhook');
  await expect(page).toHaveURL(/[?&]q=webhook/);
});

test('a shared services URL opens on the same filtered list', async ({ page }) => {
  await enterDemo(page, 'services?q=webhook&type=http');

  await expect(page.locator('.services-search-input')).toHaveValue('webhook');
  await expect(page.locator('.services-type-filter')).toHaveValue('http');
  await expect(page.locator('.services-table tbody tr')).toHaveCount(1);
  await expect(page.getByText('Payment webhook')).toBeVisible();
});

test('a reload keeps the services filter and search', async ({ page }) => {
  await enterDemo(page, 'services');
  await page.locator('.services-stat-healthy').click();
  await page.locator('.services-search-input').fill('back');
  await expect(page).toHaveURL(/[?&]q=back/);

  await page.reload();

  await expect(page.locator('.services-search-input')).toHaveValue('back');
  await expect(page.locator('.services-stat-healthy')).toHaveClass(/active/);
});

test('ten keystrokes in a search box stay one back step from the previous view', async ({ page }) => {
  await enterDemo(page);
  await page.goto('/organizations/demo/services');
  await expect(page.getByText('Payment webhook')).toBeVisible();

  await page.locator('.services-search-input').pressSequentially('monitoring');
  await expect(page).toHaveURL(/[?&]q=monitoring/);

  // One back, not ten: filter writes replace the history entry.
  await page.goBack();
  await expect(page).not.toHaveURL(/\/services/);
});

test('opening an error detail adds a history entry that back closes', async ({ page }) => {
  await enterDemo(page, 'errors?service=api-gateway');

  const row = page.locator('.card div[role="button"][tabindex="0"]').first();
  await row.click();
  await expect(page).toHaveURL(/[?&]errorId=/);

  // Opening a detail pushes, unlike a filter: back closes the panel and leaves
  // the user on the same list.
  await page.goBack();
  await expect(page).not.toHaveURL(/errorId=/);
  await expect(page).toHaveURL(/\/errors/);
});

test('the traces search is read from the URL and written back to it', async ({ page }) => {
  await enterDemo(page, 'traces?q=checkout-service');
  const input = page.locator('.traces-search-form input');
  await expect(input).toHaveValue('checkout-service');

  await input.fill('auth-service');
  await page.locator('.traces-search-form .btn-search').click();
  await expect(page).toHaveURL(/[?&]q=auth-service/);
});

test('the logs page number survives a reload', async ({ page }) => {
  await enterDemo(page, 'logs');
  await expect(page.locator('.logs-table tbody tr').first()).toBeVisible();

  await page.locator('.logs-pagination button').last().click();
  await expect(page).toHaveURL(/[?&]page=2/);
  await expect(page.locator('.logs-pagination span')).toContainText('2');

  await page.reload();

  await expect(page).toHaveURL(/[?&]page=2/);
  await expect(page.locator('.logs-pagination span')).toContainText('2');
});

test('the date range stays out of the URL but survives a reload', async ({ page }) => {
  await enterDemo(page, 'logs');

  await page.locator('.date-range-trigger').click();
  await page.locator('.drp-preset-btn').first().click();
  const label = (await page.locator('.date-range-trigger').innerText()).split('(')[0];

  // Decision 5 of the epic design: the range lives in localStorage, shared
  // across tabs, and never in the link.
  expect(new URL(page.url()).search).not.toMatch(/start|end|relative/);

  await page.reload();
  await expect(page.locator('.date-range-trigger')).toContainText(label.trim());
});

test('a filter change does not scroll the list back to the top', async ({ page }) => {
  await page.setViewportSize({ width: 900, height: 400 });
  await enterDemo(page, 'services');
  await expect(page.getByText('Payment webhook')).toBeVisible();

  await page.evaluate(() => window.scrollTo(0, 250));
  await page.locator('.services-stat-healthy').click();
  await expect(page).toHaveURL(/[?&]status=healthy/);

  expect(await page.evaluate(() => window.scrollY)).toBeGreaterThan(0);
});
