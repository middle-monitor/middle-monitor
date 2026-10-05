import { defineConfig, devices } from '@playwright/test';

// Dev server, not the production build: these are UI smoke tests (navigation,
// demo data rendering, theme/language toggles), not a check on the build
// pipeline — that is already covered by the build CI job.
const PORT = 4173;

export default defineConfig({
  testDir: './e2e',
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  // A single dev server backs every worker; more than a couple of Chromium
  // instances hitting its first cold transform at once on the CI runner's
  // constrained CPU produced timeouts that a local, less contended run never showed.
  workers: process.env.CI ? 2 : undefined,
  // inp-reporter prints the refresh-state net's per-view result and its INP
  // percentile straight into the job output, report or no report.
  reporter: process.env.CI
    ? [['list'], ['./e2e/inp-reporter.ts'], ['html', { open: 'never' }]]
    : [['list'], ['./e2e/inp-reporter.ts']],
  use: {
    baseURL: `http://localhost:${PORT}`,
    trace: 'retain-on-failure',
  },
  webServer: {
    command: `npx vite --port ${PORT} --strictPort`,
    url: `http://localhost:${PORT}`,
    reuseExistingServer: !process.env.CI,
    timeout: 30_000,
  },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
});
