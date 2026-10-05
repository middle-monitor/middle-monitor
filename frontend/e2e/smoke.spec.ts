import { test, expect } from '@playwright/test';

import { enterDemo } from './demo';

// The sidebar and its data, read from the demo sandbox: see ./demo.
async function goToDemo(page: import('@playwright/test').Page, path = '') {
  await enterDemo(page);
  if (path) await page.goto(`/organizations/demo/${path}`);
}

test('the marketing landing page loads', async ({ page }) => {
  await page.goto('/');
  await expect(page).toHaveTitle(/Middle Monitor/i);
});

test('demo mode lands on the org overview with the sidebar loaded', async ({ page }) => {
  await goToDemo(page);
  await expect(page.locator('.sidebar-action', { hasText: /English|Français/ })).toBeVisible();
});

test('the services list shows the known check states', async ({ page }) => {
  await goToDemo(page, 'services');
  // Fixed demo data: one down check, one degraded — see demoApi.ts DEMO_CHECKS.
  await expect(page.getByText('Back-office')).toBeVisible();
  await expect(page.getByText('Payment webhook')).toBeVisible();
});

test('a demo host detail page loads', async ({ page }) => {
  await goToDemo(page, 'hosts/1');
  await expect(page.locator('.host-detail-title')).toHaveText('web-01');
});

test('the docs page renders and its search finds a section', async ({ page }) => {
  await page.goto('/docs');
  // Not an exact match: each heading carries a "copy link" button whose
  // aria-label folds into the heading's accessible name.
  await expect(page.getByRole('heading', { name: /^Introduction/ })).toBeVisible();

  await page.getByText('Search docs...').first().click();
  await page.keyboard.type('agent');
  await expect(page.getByText(/install agent/i).first()).toBeVisible();
});

test('the sidebar language toggle switches the UI language', async ({ page }) => {
  await goToDemo(page);

  const langButton = page.locator('.sidebar-action', { hasText: /English|Français/ });
  await expect(langButton).toBeVisible();
  const initial = await langButton.innerText();

  await langButton.click();
  await expect(langButton).not.toHaveText(initial);
});

test('the sidebar theme toggle switches light and dark mode', async ({ page }) => {
  await goToDemo(page);

  const root = page.locator('html');
  const before = await root.getAttribute('class');

  await page.locator('.sidebar-action', { hasText: /Light Mode|Dark Mode/ }).click();
  await expect(root).not.toHaveClass(before ?? '');
});
