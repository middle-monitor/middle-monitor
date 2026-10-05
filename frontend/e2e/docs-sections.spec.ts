import { test, expect } from '@playwright/test';

test('the Prometheus scraping section renders with its Nomad and expose subsections', async ({
  page,
}) => {
  await page.goto('/docs#scraping');
  await expect(page.locator('#scraping')).toBeVisible();

  await expect(
    page.getByRole('heading', { name: 'Service discovery: Nomad and DNS SRV' }),
  ).toBeVisible();
  await expect(
    page.getByRole('heading', { name: 'Keeping your Prometheus during the migration' }),
  ).toBeVisible();

  // The yaml snippets are the operational payoff of this section; if they
  // silently went missing the page would still "render".
  await expect(page.locator('#scraping').getByText('discovery_interval').first()).toBeVisible();
  await expect(page.locator('#scraping').getByText('expose:').first()).toBeVisible();
});

test('the custom metrics section documents scope, aggregation and the alert rule API', async ({
  page,
}) => {
  await page.goto('/docs#custom-metrics');
  await expect(page.locator('#custom-metrics')).toBeVisible();
  await expect(page.locator('#custom-metrics').getByText('custom_metric').first()).toBeVisible();
  await expect(page.locator('#custom-metrics').getByText('8 series')).toBeVisible();
});

test('the sidebar links to both new sections and the docs search finds them', async ({ page }) => {
  await page.goto('/docs');
  // exact + case-sensitive: the sidebar title "Custom Metrics" would otherwise
  // also match the lowercase inline link "custom metrics" in the prose.
  await expect(page.getByRole('link', { name: 'Prometheus Scraping', exact: true })).toBeVisible();
  await expect(page.getByRole('link', { name: 'Custom Metrics', exact: true })).toBeVisible();

  await page.getByText('Search docs...').first().click();
  await page.keyboard.type('nomad');
  await expect(page.getByText('Prometheus Scraping').first()).toBeVisible();
});
