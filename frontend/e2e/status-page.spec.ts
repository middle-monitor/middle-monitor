import { test, expect } from '@playwright/test';

// The page renders times in the viewer's timezone, so a fixture in UTC would
// assert differently on a Paris laptop and on a UTC CI container. Pin it.
test.use({ timezoneId: 'UTC' });

// /status reads GET /api/v1/status, and CI runs the dev server with no backend
// behind it. Both paths are stubbed here: a known payload to check the page
// renders what the API says, and a failing call to check the page still says
// something — a status page that goes blank when the backend is down fails at
// the one moment it exists for.

// 30 consecutive days ending 2026-08-04, the way the API returns them.
const day = (i: number) =>
  new Date(Date.UTC(2026, 7, 4) - (29 - i) * 86400000).toISOString().slice(0, 10);

const PAYLOAD = {
  status: 'degraded',
  window_days: 30,
  updated_at: '2026-08-04T09:00:00Z',
  components: [
    {
      name: 'API',
      status: 'operational',
      uptime: 99.95,
      days: Array.from({ length: 30 }, (_, i) => ({
        date: day(i),
        status: i === 29 ? 'degraded' : 'operational',
        ...(i === 29 ? { downtime_minutes: 22 } : {}),
      })),
    },
    {
      name: 'Ingestion pipeline',
      status: 'degraded',
      uptime: 98.2,
      days: Array.from({ length: 30 }, (_, i) => ({
        date: day(i),
        status: 'operational',
      })),
    },
  ],
  incidents: [
    {
      component: 'Ingestion pipeline',
      kind: 'degraded',
      severity: 'warning',
      started_at: '2026-08-04T08:10:00Z',
    },
    // Same component, same day, three separate outages: the page must roll them
    // into one line instead of repeating "Outage on API" three times.
    {
      component: 'API',
      kind: 'unreachable',
      severity: 'critical',
      started_at: '2026-08-01T17:22:00Z',
      resolved_at: '2026-08-01T18:45:00Z',
    },
    {
      component: 'API',
      kind: 'unreachable',
      severity: 'critical',
      started_at: '2026-08-01T12:00:00Z',
      resolved_at: '2026-08-01T12:05:00Z',
    },
    {
      component: 'API',
      kind: 'unreachable',
      severity: 'critical',
      started_at: '2026-08-01T09:00:00Z',
      resolved_at: '2026-08-01T09:02:00Z',
    },
  ],
  maintenance: [
    {
      name: 'Postgres 16 upgrade',
      starts_at: '2026-08-10T22:00:00Z',
      ends_at: '2026-08-11T02:00:00Z',
    },
  ],
};

test('the status page renders the components, history and incidents the API returns', async ({
  page,
}) => {
  await page.route('**/api/v1/status', (route) =>
    route.fulfill({ json: PAYLOAD })
  );
  await page.goto('/status');

  // The banner reflects the overall status, not the first component's.
  await expect(page.locator('.status-banner h1')).toHaveText(/degraded/i);

  await expect(page.locator('.status-component')).toHaveCount(2);
  // The percentage sits between the two ends of the window it covers, so it can
  // never be mistaken for today's figure.
  await expect(page.locator('.status-component').first().locator('footer')).toContainText(
    '99.95% uptime over 30 days'
  );
  // One bar per day of the window the API reported, not a hardcoded 90.
  await expect(page.locator('.status-component').first().locator('.status-bar')).toHaveCount(30);

  await expect(page.getByText('Postgres 16 upgrade')).toBeVisible();

  // Hovering a bar answers what happened that day and for how long.
  await page.locator('.status-component').first().locator('.status-bar').last().hover();
  const tooltip = page.locator('.status-tooltip');
  await expect(tooltip).toContainText('August 4, 2026');
  await expect(tooltip).toContainText('22 min');
  // An amber bar with downtime is a partial outage, stated as the outage and its
  // duration — never as "was down", which reads worse than what happened.
  await expect(tooltip).toContainText('Partial outage');
  await expect(tooltip).not.toContainText('was down');
  // A clean day says so plainly instead of claiming 0 min of downtime.
  await page.locator('.status-component').last().locator('.status-bar').last().hover();
  await expect(page.locator('.status-tooltip')).toContainText('No errors recorded this day');
  await expect(page.locator('.status-tooltip')).not.toContainText('min');

  // Grouped by day, newest first — and the three API outages of August 1 collapse
  // into a single line carrying the count and the span.
  await expect(page.locator('.status-incident-day')).toHaveCount(2);
  await expect(page.locator('.status-incident-day li')).toHaveCount(2);
  await expect(page.locator('.status-incident-day').last().locator('li')).toContainText(
    '3 outages, from 09:00 AM to 06:45 PM'
  );
  // The page words the failure itself; the stored title names an internal check
  // ("Check Failure: self-api") and must never reach a visitor.
  await expect(page.locator('.status-incident-day li').first()).toContainText(
    'Degraded performance on Ingestion pipeline'
  );
  // Incidents name what happened and where; that it is still going is carried by
  // the time line, not by an alarming verb.
  await expect(page.locator('.status-main')).not.toContainText('was unavailable');
  await expect(page.locator('.status-main')).not.toContainText('is not answering');
  await expect(page.locator('.status-main')).not.toContainText('self-');
});

test('the status page still reports something when the API is unreachable', async ({
  page,
}) => {
  await page.route('**/api/v1/status', (route) => route.abort());
  await page.goto('/status');

  await expect(page.locator('.status-banner')).toBeVisible();
  await expect(page.locator('.status-banner h1')).toHaveText(/unavailable/i);
});

test('the status page is reachable from the landing footer', async ({ page }) => {
  await page.goto('/');
  await page.getByRole('link', { name: 'Status Page', exact: true }).click();
  await expect(page).toHaveURL(/\/status$/);
});
