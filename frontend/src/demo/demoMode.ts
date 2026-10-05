import type { Organization, User, UserOrganization } from '../api';

/**
 * Demo mode: a frontend-only sandbox behind /demo. When active, AuthContext
 * serves a fake read-only session and useOrgApi swaps the real HTTP client for
 * an in-memory one (see demoApi.ts). No request ever reaches the backend.
 *
 * The flag lives in sessionStorage so it is scoped to the tab and vanishes
 * when the tab closes. Entering/exiting triggers a full page reload so every
 * provider re-initializes from a clean state.
 */

export const DEMO_ORG_SLUG = 'demo';

const DEMO_FLAG_KEY = 'mm_demo_mode';

export function isDemoMode(): boolean {
  try {
    return sessionStorage.getItem(DEMO_FLAG_KEY) === '1';
  } catch {
    return false;
  }
}

export function enterDemoMode(): void {
  sessionStorage.setItem(DEMO_FLAG_KEY, '1');
}

export function exitDemoMode(): void {
  sessionStorage.removeItem(DEMO_FLAG_KEY);
}

// The sandbox rejects every write, so nothing in it can exercise what saving a
// form does to the list behind it. This key turns service creation into an
// in-memory append (see demoApi.ts), which is how the e2e net checks that a
// write keeps the reader's filter, page and scroll. Read in dev only, so the
// public demo stays read-only.
// AuthContext reads this once, when the module loads: set the key before the
// page script runs (addInitScript), never after navigating, or canWrite stays
// false with nothing to show for it.
const WRITES_KEY = 'mm.demo.writes';

export function demoWritesEnabled(): boolean {
  if (!import.meta.env.DEV) return false;
  try {
    return sessionStorage.getItem(WRITES_KEY) === '1';
  } catch {
    return false;
  }
}

// The API keys list lives in a settings page that redirects anyone but an
// admin away, and the demo session is not one. This key lifts that for the
// e2e net, the same way and under the same rules as WRITES_KEY: dev only, set
// before the page script runs.
const ADMIN_KEY = 'mm.demo.admin';

export function demoAdminEnabled(): boolean {
  if (!import.meta.env.DEV) return false;
  try {
    return sessionStorage.getItem(ADMIN_KEY) === '1';
  } catch {
    return false;
  }
}

const DEMO_EPOCH = '2026-01-05T09:00:00Z';

export const demoOrganization: Organization = {
  id: 0,
  name: 'NovaShop',
  slug: DEMO_ORG_SLUG,
  plan: 'pro',
  created_at: DEMO_EPOCH,
  updated_at: DEMO_EPOCH,
  alert_warning_enabled: true,
  alert_critical_enabled: true,
  mfa_required: false,
  smtp_configured: false,
};

export const demoUser: User = {
  id: 0,
  organization_id: 0,
  email: 'demo@middle-monitor.local',
  name: 'Demo Viewer',
  role: 'read_only',
  email_verified: true,
  totp_enabled: false,
  created_at: DEMO_EPOCH,
  updated_at: DEMO_EPOCH,
};

export const demoUserOrganizations: UserOrganization[] = [
  { ...demoOrganization, role: 'read_only' },
];
