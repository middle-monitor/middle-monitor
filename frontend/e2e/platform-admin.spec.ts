import { test, expect, type Page } from '@playwright/test';

// /platform-admin is the only page outside the org shell: it lists every
// organization on the instance and is gated on the caller's email being in the
// backend's PLATFORM_ADMIN_EMAILS. Demo mode can't reach it (it is never a
// platform admin), so the session and both endpoints are stubbed here.
//
// What these tests protect: a non-owner must never see the org list, and the
// plan dropdown must send the plan the owner picked for the org they picked —
// sending it for the wrong id silently moves another customer's billing.

const ORGS = [
  {
    id: 1,
    name: 'Acme',
    slug: 'acme',
    plan: 'free',
    created_at: '2026-01-15T10:00:00Z',
  },
  {
    id: 2,
    name: 'Globex',
    slug: 'globex',
    plan: 'pro',
    trial_ends_at: '2026-12-01T10:00:00Z',
    created_at: '2026-02-20T10:00:00Z',
  },
];

// Stubs the session: a stored token is what makes ProtectedRoute render the
// page, and /auth/me carries the platform-admin flag the view gates on.
async function signIn(page: Page, isPlatformAdmin: boolean) {
  await page.addInitScript(() => localStorage.setItem('auth_token', 'e2e-token'));
  await page.route('**/api/v1/auth/me', (route) =>
    route.fulfill({
      json: {
        user: {
          id: 1,
          organization_id: 1,
          email: 'owner@example.com',
          name: 'Owner',
          role: 'admin',
          email_verified: true,
          created_at: '2026-01-01T10:00:00Z',
          updated_at: '2026-01-01T10:00:00Z',
        },
        organization: { id: 1, name: 'Acme', slug: 'acme', plan: 'free', created_at: '2026-01-15T10:00:00Z', updated_at: '2026-01-15T10:00:00Z' },
        organizations: [],
        is_platform_admin: isPlatformAdmin,
      },
    })
  );
}

test('the owner sees every organization with its plan', async ({ page }) => {
  await signIn(page, true);
  await page.route('**/api/v1/platform-admin/organizations', (route) =>
    route.fulfill({ json: ORGS })
  );

  await page.goto('/platform-admin');

  const rows = page.locator('table.table tbody tr');
  await expect(rows).toHaveCount(2);
  await expect(rows.first().locator('td').first()).toHaveText('Acme');
  await expect(rows.nth(1).locator('td').nth(1)).toHaveText('globex');
  // The stored plan drives the dropdown, so the owner reads the billing truth
  // rather than a default.
  await expect(page.locator('table.table tbody tr').nth(1).locator('select')).toHaveValue('pro');
});

test('changing a plan sends that plan for that organization', async ({ page }) => {
  await signIn(page, true);
  await page.route('**/api/v1/platform-admin/organizations', (route) =>
    route.fulfill({ json: ORGS })
  );

  const patched: { url: string; body: unknown }[] = [];
  await page.route('**/api/v1/platform-admin/organizations/*/plan', (route) => {
    patched.push({ url: route.request().url(), body: route.request().postDataJSON() });
    route.fulfill({ json: { plan: 'pro' } });
  });

  await page.goto('/platform-admin');
  await page.locator('table.table tbody tr').first().locator('select').selectOption('pro');

  await expect.poll(() => patched.length).toBe(1);
  // Acme is id 1: the row's own id, not the first id the page happened to load.
  expect(patched[0].url).toContain('/platform-admin/organizations/1/plan');
  expect(patched[0].body).toEqual({ plan: 'pro' });
  await expect(page.locator('.success-message')).toBeVisible();
});

// The backend clears trial_ends_at on every plan write. A row still showing a
// deadline after a successful change is the trial/subscription confusion this
// column exists to prevent, and it would survive until a manual reload.
test('a successful plan change drops the trial deadline with it', async ({ page }) => {
  await signIn(page, true);
  await page.route('**/api/v1/platform-admin/organizations', (route) =>
    route.fulfill({ json: ORGS })
  );
  await page.route('**/api/v1/platform-admin/organizations/*/plan', (route) =>
    route.fulfill({ json: { plan: 'free' } })
  );

  await page.goto('/platform-admin');
  // Globex is the row carrying a trial.
  const globex = page.locator('table.table tbody tr', { hasText: 'globex' });
  await expect(globex.locator('td').nth(3)).not.toHaveText('—');

  await globex.locator('select').selectOption('free');

  await expect(globex.locator('td').nth(3)).toHaveText('—');
});

// Each row rolls back on its own: restoring a whole-list snapshot would undo a
// change made to another row while this one was in flight.
test('a failed change on one row leaves another row alone', async ({ page }) => {
  await signIn(page, true);
  await page.route('**/api/v1/platform-admin/organizations', (route) =>
    route.fulfill({ json: ORGS })
  );
  // Acme (id 1) is refused, but only once the test says so: holding the route on
  // a promise rather than a delay makes the overlap exact instead of hoping a
  // timer outlasts a busy runner.
  let releaseAcme: () => void = () => {};
  const acmeHeld = new Promise<void>((resolve) => {
    releaseAcme = resolve;
  });
  await page.route('**/api/v1/platform-admin/organizations/1/plan', async (route) => {
    await acmeHeld;
    route.fulfill({ status: 500, json: { error: 'boom' } });
  });
  // Globex (id 2) succeeds.
  await page.route('**/api/v1/platform-admin/organizations/2/plan', (route) =>
    route.fulfill({ json: { plan: 'custom' } })
  );

  await page.goto('/platform-admin');
  const acme = page.locator('table.table tbody tr', { hasText: 'acme' });
  const globex = page.locator('table.table tbody tr', { hasText: 'globex' });

  await acme.locator('select').selectOption('pro');
  // Globex lands while Acme is still in flight, which is the case that matters.
  await globex.locator('select').selectOption('custom');
  await expect(globex.locator('select')).toHaveValue('custom');
  releaseAcme();

  // Acme reverts to its own previous plan, Globex keeps the change that worked.
  await expect(acme.locator('select')).toHaveValue('free');
  await expect(globex.locator('select')).toHaveValue('custom');
});

test('a failed plan change puts the previous plan back', async ({ page }) => {
  await signIn(page, true);
  await page.route('**/api/v1/platform-admin/organizations', (route) =>
    route.fulfill({ json: ORGS })
  );
  await page.route('**/api/v1/platform-admin/organizations/*/plan', (route) =>
    route.fulfill({ status: 403, json: { error: 'platform admin access required' } })
  );

  await page.goto('/platform-admin');
  const plan = page.locator('table.table tbody tr').first().locator('select');
  await plan.selectOption('pro');

  // The optimistic row must not keep showing a plan the backend refused.
  await expect(plan).toHaveValue('free');
  await expect(page.locator('.error-message')).toContainText('platform admin access required');
});

// The backend also refuses this route for an unverified address or a missing
// authenticator. "Loading failed" would send the owner hunting; the reason the
// API gave is the whole answer.
test('a refusal from the API is shown with its reason', async ({ page }) => {
  await signIn(page, true);
  await page.route('**/api/v1/platform-admin/organizations', (route) =>
    route.fulfill({ status: 403, json: { error: 'mfa enrollment required' } })
  );

  await page.goto('/platform-admin');

  await expect(page.locator('.error-message')).toContainText('mfa enrollment required');
});

test('a signed-in user who is not the owner never sees the organizations', async ({ page }) => {
  await signIn(page, false);

  let listed = false;
  await page.route('**/api/v1/platform-admin/organizations', (route) => {
    listed = true;
    route.fulfill({ json: ORGS });
  });

  await page.goto('/platform-admin');

  await expect(page.locator('.empty-state-title')).toBeVisible();
  await expect(page.locator('table.table')).toHaveCount(0);
  // Not just hidden: the page must not even ask for other tenants' data.
  expect(listed).toBe(false);
  // There is no sidebar on this route, so the refusal must offer a way out
  // rather than leave the browser's back button as the only exit.
  await page.locator('.empty-state-action').click();
  await expect(page).toHaveURL(/\/organizations\/acme$/);
});
