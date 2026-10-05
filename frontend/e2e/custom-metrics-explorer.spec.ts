import { test, expect } from '@playwright/test';

import { enterDemo } from './demo';

// Demo data is fixed in src/demo/demoApi.ts's DEMO_METRICS: four metrics, each
// pinned to one host (http_requests_total -> web-01, checkout_queue_depth ->
// worker-01, db_connections_active -> db-01), so scoping to a host is what
// narrows the metric list, not a live query result.

async function goToExplorer(page: import('@playwright/test').Page) {
  await enterDemo(page);
  await page.goto('/organizations/demo/metrics');
  await page.waitForSelector('.custom-metrics');
}

test('the explorer lists the demo metrics and offers p75', async ({ page }) => {
  await goToExplorer(page);

  const metricSelect = page.getByLabel('Metric');
  await expect(metricSelect.locator('option', { hasText: 'http_requests_total' })).toHaveCount(1);
  await expect(metricSelect.locator('option', { hasText: 'checkout_queue_depth' })).toHaveCount(1);

  const aggregationOptions = await page.getByLabel('Aggregation').locator('option').allInnerTexts();
  expect(aggregationOptions).toEqual(
    expect.arrayContaining(['avg', 'min', 'max', 'sum', 'count', 'p50', 'p75', 'p90', 'p95', 'p99']),
  );
});

test('picking a metric draws a chart', async ({ page }) => {
  await goToExplorer(page);

  await page.getByLabel('Metric').selectOption('http_requests_total');
  await expect(page.locator('.custom-metrics .recharts-line').first()).toBeVisible();
  await expect(page.getByText('No data for this query.')).not.toBeVisible();
});

// Scope is the feature the review specifically asked to correlate against a
// host or host group: narrowing it must actually narrow the metric list, not
// just filter the chart.
test('scoping to a host narrows the metric list', async ({ page }) => {
  await goToExplorer(page);

  const metricSelect = page.getByLabel('Metric');
  await expect(metricSelect.locator('option', { hasText: 'checkout_queue_depth' })).toHaveCount(1);

  // web-01 is host id 1 in the demo fixture; only http_requests_total is pinned there.
  await page.getByLabel('Scope').selectOption('host:1');
  await expect(metricSelect.locator('option', { hasText: 'http_requests_total' })).toHaveCount(1);
  await expect(metricSelect.locator('option', { hasText: 'checkout_queue_depth' })).toHaveCount(0);
});

test('a label filter narrows the series shown', async ({ page }) => {
  await goToExplorer(page);
  await page.getByLabel('Metric').selectOption('http_requests_total');
  await page.getByLabel('Group by').selectOption('route');

  const before = await page.locator('.custom-metrics .recharts-line').count();
  expect(before).toBeGreaterThan(1);

  await page.getByText('Add a filter').click();
  await page.getByLabel('Label').selectOption('route');
  await page.getByLabel('Value').selectOption('/checkout');

  await expect(page.locator('.custom-metrics .recharts-line')).toHaveCount(1);
});

// The prior version of this test only re-checked that a metric drops out of
// the dropdown when out of scope — already covered above, and it never
// actually asserted the empty state. 'refunds' is a real label value in the
// demo fixture whose points are all null, the same shape OpenSearch returns
// for a slice with buckets but no data: this is what exercises the hasValues
// fix (a flat zero line was drawn before it).
test('a query with only null-valued points shows the empty state, not a flat zero line', async ({
  page,
}) => {
  await goToExplorer(page);

  await page.getByLabel('Metric').selectOption('checkout_queue_depth');
  await expect(page.locator('.custom-metrics .recharts-line').first()).toBeVisible();

  await page.getByText('Add a filter').click();
  await page.getByLabel('Label').selectOption('queue');
  await page.getByLabel('Value').selectOption('refunds');

  await expect(page.getByText('No data for this query.')).toBeVisible();
  await expect(page.locator('.custom-metrics .recharts-line')).toHaveCount(0);
});
