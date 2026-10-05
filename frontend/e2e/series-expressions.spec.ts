import { test, expect, type Page } from '@playwright/test';

import { enterDemo } from './demo';

// Demo mode evaluates expressions in src/demo/demoApi.ts. A query divided by
// itself is the one result the fixture guarantees whatever its random walk.

async function goToExplorer(page: Page) {
  await enterDemo(page, 'metrics');
  await page.waitForSelector('.series-query');
}

const seriesNames = (page: Page) =>
  page.locator('.custom-metrics-table tbody td:first-child').allInnerTexts();

async function addSecondQuery(page: Page, metric: string) {
  await page.getByRole('button', { name: 'Add a query' }).click();
  await page.locator('.series-query').nth(1).getByLabel('Metric').selectOption(metric);
}

async function applyExpression(page: Page, expression: string) {
  const input = page.getByLabel('Expression');
  await input.fill(expression);
  await input.press('Enter');
}

test('two raw queries on one chart are told apart by their ref', async ({ page }) => {
  await goToExplorer(page);
  await page.getByLabel('Metric').selectOption('http_requests_total');
  await addSecondQuery(page, 'http_requests_total');

  await expect.poll(() => seriesNames(page)).toEqual(['$A http_requests_total', '$B http_requests_total']);
});

test('an expression replaces the queries with its result', async ({ page }) => {
  await goToExplorer(page);
  await page.getByLabel('Metric').selectOption('http_requests_total');
  await addSecondQuery(page, 'http_requests_total');
  await applyExpression(page, '$A / $B * 100');

  await expect.poll(() => seriesNames(page)).toEqual(['$A / $B * 100']);
  // Last, min and max of a series divided by itself, as a percentage.
  const row = page.locator('.custom-metrics-table tbody tr').first();
  await expect(row.locator('td').nth(1)).toHaveText('100');
  await expect(row.locator('td').nth(2)).toHaveText('100');
  await expect(row.locator('td').nth(3)).toHaveText('100');
});

// A typo must say the query is wrong, not draw an empty chart that reads as "no data".
test('an invalid expression names the problem', async ({ page }) => {
  await goToExplorer(page);
  await applyExpression(page, '$A +');

  await expect(page.getByText('Invalid query: invalid expression')).toBeVisible();
  await expect(page.locator('.custom-metrics .recharts-line')).toHaveCount(0);
});

// Demo evaluates pointwise, so it cannot sum a sub-expression: it refuses rather
// than charting a division it summed afterwards.
test('a sum() nested in a wider expression is refused in demo', async ({ page }) => {
  await goToExplorer(page);
  await page.getByLabel('Metric').selectOption('http_requests_total');
  await addSecondQuery(page, 'http_server_duration_ms');

  await applyExpression(page, '$A / sum($B)');
  await expect(page.getByText('Invalid query: invalid expression')).toBeVisible();

  // The total on its own is computed, not refused.
  await applyExpression(page, 'sum($A)');
  await expect.poll(() => seriesNames(page)).toEqual(['sum($A)']);
});

// Scoping to web-01 drops $B's metric. Keeping the expression would send an
// unknown ref, which is what the PO hit: the chart came back as "invalid query".
test('narrowing the scope drops the expression that named the emptied query', async ({ page }) => {
  await goToExplorer(page);
  await page.getByLabel('Metric').selectOption('http_requests_total');
  await addSecondQuery(page, 'http_server_duration_ms');
  await applyExpression(page, '$A / $B');
  await expect.poll(() => seriesNames(page)).toEqual(['$A / $B']);

  // web-01 is host id 1 in the demo fixture; http_server_duration_ms is on web-02.
  await page.getByLabel('Scope').selectOption('host:1');

  await expect(page.getByLabel('Expression')).toHaveValue('');
  await expect(page.getByText('Invalid query:')).not.toBeVisible();
  await expect.poll(() => seriesNames(page)).toEqual(['http_requests_total']);
});

// The aggregation is the user's choice, not the metric's: a scope change that
// has to pick another metric must not silently put it back to avg.
test('a scope change that replaces the metric keeps the aggregation', async ({ page }) => {
  await goToExplorer(page);
  await page.getByLabel('Metric').selectOption('checkout_queue_depth');
  await page.getByLabel('Aggregation').selectOption('p95');

  // worker-01 holds checkout_queue_depth; scoping to web-01 leaves only its own metric.
  await page.getByLabel('Scope').selectOption('host:1');

  await expect(page.getByLabel('Metric')).toHaveValue('http_requests_total');
  await expect(page.getByLabel('Aggregation')).toHaveValue('p95');
});

test('rate is offered for counters', async ({ page }) => {
  await goToExplorer(page);
  await expect(page.getByLabel('Aggregation').locator('option', { hasText: 'rate (per second)' })).toHaveCount(1);
});

// Below the lg breakpoint the grid only knew the lg layout, so a widget added
// after mount fell back to 1x1 (97x60px) and that size was saved with it.
test('a custom widget added below lg gets its full size', async ({ page }) => {
  await page.setViewportSize({ width: 1100, height: 1000 });
  await enterDemo(page, 'dashboards');

  await page.getByRole('button', { name: 'Add a widget' }).first().click();
  await page.getByText('Custom metric').first().click();
  await page.locator('.series-query').first().getByLabel('Metric').selectOption('http_requests_total');
  await page.getByRole('button', { name: 'Add the widget' }).click();

  const widget = page.locator('.dashboard-widget').filter({ hasText: 'http_requests_total' });
  await expect(widget).toHaveCount(1);
  const box = await widget.boundingBox();
  expect(box?.width ?? 0).toBeGreaterThan(300);
  expect(box?.height ?? 0).toBeGreaterThan(200);
});

// Typing the expression before picking $B's metric is a normal order of work:
// the expression must wait for the metric, not be wiped.
test('an expression typed before its query has a metric is kept', async ({ page }) => {
  await goToExplorer(page);
  await page.getByLabel('Metric').selectOption('http_requests_total');
  await page.getByRole('button', { name: 'Add a query' }).click();
  await applyExpression(page, '$A / $B * 100');
  await expect(page.getByLabel('Expression')).toHaveValue('$A / $B * 100');
  await expect(page.getByText('Invalid query:')).not.toBeVisible();

  await page.locator('.series-query').nth(1).getByLabel('Metric').selectOption('http_requests_total');
  await expect.poll(() => seriesNames(page)).toEqual(['$A / $B * 100']);
});

// Demo mirrors the backend join: one labeled series left on a side is not a
// total, so it pairs with its own route only instead of dividing every route.
test('a lone labeled series pairs with its own route only', async ({ page }) => {
  await goToExplorer(page);
  await page.getByLabel('Metric').selectOption('http_requests_total');
  await page.getByLabel('Group by').selectOption('route');
  await page.getByText('Add a filter').click();
  await page.getByLabel('Label').selectOption('route');
  await page.getByLabel('Value').selectOption('/checkout');

  await addSecondQuery(page, 'http_requests_total');
  await page.locator('.series-query').nth(1).getByLabel('Group by').selectOption('route');
  await applyExpression(page, '$A / $B * 100');

  // Broadcasting by count drew one line per route of $B here.
  await expect.poll(() => seriesNames(page)).toEqual(['route=/checkout']);
});
