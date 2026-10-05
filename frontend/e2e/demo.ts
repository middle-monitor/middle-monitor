import { expect, type Page } from '@playwright/test';

// Demo mode is a frontend-only sandbox with fixed data (see src/demo/demoApi.ts):
// no backend, no credentials, so it is what CI can actually drive. /demo sets a
// sessionStorage flag then hard-replaces the location into /organizations/demo.
// StrictMode runs that effect twice in dev, so the first replace is aborted by
// the second: wait for the URL the browser settles on rather than for one
// navigation, which is what made this flaky once a few workers shared one dev
// server.
// waitUntil 'commit' also carries date-range-stability.spec.ts, which installs a
// fake clock before calling this: under it the page load event never fires.
export async function enterDemo(page: Page, path = ''): Promise<void> {
  await page.goto('/demo', { waitUntil: 'commit' });
  // Same budget as the waitForURL this helper replaced: a cold dev server
  // shared by several workers takes well over the 5s expect.poll defaults to.
  await expect.poll(() => new URL(page.url()).pathname, { timeout: 30_000 }).toBe('/organizations/demo');
  if (path) await page.goto(`/organizations/demo/${path}`);
}
