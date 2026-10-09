import axios from 'axios';
import { captureError } from '@middle-monitor/web';

import i18n from './i18n';
import { isDemoMode } from './demo/demoMode';

/** App language for Explain/correlation API (follows the active i18n language). */
function explainLocale(): 'en' | 'fr' {
  return i18n.language === 'fr' ? 'fr' : 'en';
}

const api = axios.create({
  baseURL: (import.meta.env.VITE_API_URL ?? '') + '/api/v1',
  // A request that never settles would hold a view's auto-refresh for good:
  // the in-flight guard skips every tick and the manual button stays disabled.
  timeout: 30_000,
  headers: {
    'Content-Type': 'application/json',
  },
});

/**
 * Absolute API origin for commands the user copies out of the UI and runs
 * elsewhere (agent install, SDK snippets). Distinct from the client baseURL
 * above, which stays relative so the dashboard's own calls are same-origin.
 * Never window.location.origin: the dashboard is served from the apex, which
 * does not route the agent and OTLP endpoints.
 */
export const PUBLIC_API_URL =
  import.meta.env.VITE_API_URL || 'https://api.middlemonitor.io';

// Add auth token to requests
api.interceptors.request.use((config) => {
  const token = localStorage.getItem('auth_token');
  // Don't clobber an Authorization explicitly set by the caller.
  if (token && !config.headers.Authorization) {
    config.headers.Authorization = `Bearer ${token}`;
  }
  const lang = localStorage.getItem('appLang');
  if (lang === 'en' || lang === 'fr') {
    config.headers['Accept-Language'] = lang;
  }
  return config;
});

// Clears tokens and bounces to /login (unless already on a public auth page).
function forceLogout(error: unknown) {
  // The demo session is fake and has no refresh token, so any call that escapes
  // the in-memory client would answer 401 and bounce the visitor out of the
  // demo. Never log out of a sandbox.
  if (isDemoMode()) {
    return Promise.reject(error);
  }
  localStorage.removeItem('auth_token');
  localStorage.removeItem('refresh_token');
  if (
    window.location.pathname !== '/login' &&
    window.location.pathname !== '/get-started'
  ) {
    window.location.href = '/login';
  }
  return Promise.reject(error);
}

// Single-flight refresh: when an access token expires, the first 401 triggers one
// /auth/refresh call; concurrent 401s wait on the same refresh and then replay.
// Only if refresh fails do we log the user out — no more hard logout on expiry.
let isRefreshing = false;
let pendingQueue: Array<(token: string | null) => void> = [];

api.interceptors.response.use(
  (response) => response,
  async (error) => {
    const original = error.config;
    const status = error.response?.status;
    const url: string = original?.url || '';
    const isAuthCall =
      url.includes('/auth/login') ||
      url.includes('/auth/register') ||
      url.includes('/auth/refresh');

    // Self-monitoring: report unexpected dashboard API failures (network errors
    // and 5xx) through the web SDK. Skip expected 4xx (validation/auth).
    // The /unsub URL carries the recipient address: never report it.
    if ((!error.response || status >= 500) && !url.includes('/unsubscribe')) {
      captureError(
        new Error(
          `${(original?.method || 'get').toUpperCase()} ${url} -> ${status ?? 'network error'}: ${error.message}`
        ),
        { file: window.location.href }
      );
    }

    if (status !== 401 || isAuthCall || original?._retry) {
      return Promise.reject(error);
    }

    const refreshToken = localStorage.getItem('refresh_token');
    if (!refreshToken) {
      return forceLogout(error);
    }

    original._retry = true;

    if (isRefreshing) {
      // A refresh is already in-flight: queue this request and replay it after.
      return new Promise((resolve, reject) => {
        pendingQueue.push((newToken) => {
          if (!newToken) return reject(error);
          original.headers = original.headers || {};
          original.headers.Authorization = `Bearer ${newToken}`;
          resolve(api(original));
        });
      });
    }

    isRefreshing = true;
    try {
      // Bare axios (not `api`) so this call skips the interceptors entirely.
      const resp = await axios.post('/api/v1/auth/refresh', {
        refresh_token: refreshToken,
      });
      const tokens = resp.data?.tokens;
      if (!tokens?.access_token) {
        throw new Error('No access token in refresh response');
      }
      localStorage.setItem('auth_token', tokens.access_token);
      if (tokens.refresh_token) {
        localStorage.setItem('refresh_token', tokens.refresh_token);
      }
      pendingQueue.forEach((cb) => cb(tokens.access_token));
      pendingQueue = [];
      original.headers = original.headers || {};
      original.headers.Authorization = `Bearer ${tokens.access_token}`;
      return api(original);
    } catch {
      pendingQueue.forEach((cb) => cb(null));
      pendingQueue = [];
      return forceLogout(error);
    } finally {
      isRefreshing = false;
    }
  },
);

// Auth types
export interface User {
  id: number;
  organization_id: number;
  email: string;
  name: string;
  role: string;
  /** Self-serve accounts must confirm their email before the dashboard unlocks. */
  email_verified?: boolean;
  /** Whether the user has enrolled an authenticator app (TOTP). */
  totp_enabled?: boolean;
  /** True while an invited user has not yet accepted (set their password). */
  pending?: boolean;
  created_at: string;
  updated_at: string;
  last_login_at?: string;
}

export interface Organization {
  id: number;
  name: string;
  slug: string;
  plan?: string;
  /** End of the free Pro trial. Present next to plan 'pro' = the access is a trial, not a subscription. */
  trial_ends_at?: string;
  /** False on a self-hosted instance that sells no plans. */
  billing_enabled?: boolean;
  created_at: string;
  updated_at: string;
  /** Per-org opt-in: deliver warning-severity alerts. */
  alert_warning_enabled?: boolean;
  /** Per-org opt-in: deliver critical-severity alerts. */
  alert_critical_enabled?: boolean;
  /** When true, every member must enroll TOTP before accessing the dashboard. */
  mfa_required?: boolean;
  /** Per-org SMTP for email alert channels. */
  smtp_host?: string;
  smtp_port?: string;
  smtp_user?: string;
  smtp_from?: string;
  /** Read-only: whether an SMTP password is set (the password itself is never returned). */
  smtp_configured?: boolean;
  /** Write-only: send to set a new SMTP password (""=clear, omit=keep). */
  smtp_pass?: string;
}

// Maintenance / downtime window: suppresses alerts for a service or host.
export interface MaintenanceWindow {
  id: number;
  organization_id: number;
  name: string;
  target_type: 'service' | 'host';
  target_id: number;
  starts_at: string;
  ends_at: string;
  created_by?: number;
  created_at: string;
  target_name?: string;
}

export interface AuthTokens {
  access_token: string;
  refresh_token?: string;
  expires_in: number;
}

export interface LoginRequest {
  email: string;
  password: string;
}

export interface RegisterRequest {
  email: string;
  password: string;
  name: string;
  organization_name: string;
  organization_slug: string;
}

export interface InviteUserRequest {
  email: string;
  name: string;
  role: string;
}

export interface LoginResponse {
  /** Present only when the org enforces 2FA and the user is enrolled: the caller
   *  must complete the login via authApi.loginMfa with a code. */
  mfa_required?: boolean;
  mfa_token?: string;
  /** Present on a normal (single-factor) login. */
  user?: User;
  tokens?: AuthTokens;
}

export interface Setup2FAResponse {
  secret: string;
  otpauth_url: string;
  /** Base64-encoded PNG of the otpauth QR code (render as data URI). */
  qr_png: string;
}

export interface RegisterResponse {
  user: User;
  tokens: AuthTokens;
}

// An organization the user belongs to, with their per-org role. Drives the switcher.
export interface UserOrganization extends Organization {
  role: string;
}

export interface MeResponse {
  user: User;
  organization: Organization;
  /** Every org the user is a member of (for the org switcher). */
  organizations?: UserOrganization[];
  /** True for the emails listed in PLATFORM_ADMIN_EMAILS (the instance owner),
   *  unrelated to any org's own admin role. Gates the cross-org admin page. */
  is_platform_admin?: boolean;
}

// Auth API
export const authApi = {
  login: (data: LoginRequest) => api.post<LoginResponse>('/auth/login', data),
  // Second step of a 2FA login: exchange the challenge token + code for tokens.
  loginMfa: (mfaToken: string, code: string) =>
    api.post<{ user: User; tokens: AuthTokens }>('/auth/login/mfa', {
      mfa_token: mfaToken,
      code,
    }),
  register: (data: RegisterRequest) =>
    api.post<RegisterResponse>('/auth/register', data),
  getMe: () => api.get<MeResponse>('/auth/me'),
  deleteAccount: (password: string) => api.delete('/auth/me', { data: { password } }),
  refreshToken: (refreshToken: string) =>
    api.post<{ user: User; tokens: AuthTokens }>('/auth/refresh', {
      refresh_token: refreshToken,
    }),
  // Switch the active organization (must be one the user belongs to). Returns a
  // fresh session scoped to the target org.
  switchOrg: (organizationId: number) =>
    api.post<{ user: User; organization: Organization; tokens: AuthTokens }>(
      '/auth/switch-org',
      { organization_id: organizationId },
    ),
  verifyEmail: (token: string) =>
    api.post<{ message: string }>('/auth/verify-email', { token }),
  resendVerification: () =>
    api.post<{ message: string }>('/auth/resend-verification'),
  // An invited user sets their password to activate the account; returns a session.
  acceptInvite: (token: string, password: string) =>
    api.post<{ user: User; organization: Organization; tokens: AuthTokens }>(
      '/auth/accept-invite',
      { token, password },
    ),
  // Request a password-reset email. Always succeeds (generic message) so callers
  // can't tell whether the email is registered.
  forgotPassword: (email: string) =>
    api.post<{ message: string }>('/auth/forgot-password', { email }),
  // Set a new password from a reset-link token.
  resetPassword: (token: string, password: string) =>
    api.post<{ message: string }>('/auth/reset-password', { token, password }),
  // Begin authenticator enrollment: returns the secret + QR.
  setup2fa: () => api.post<Setup2FAResponse>('/auth/2fa/setup'),
  // Confirm enrollment with a code; returns one-time recovery codes.
  verify2fa: (code: string) =>
    api.post<{ recovery_codes: string[] }>('/auth/2fa/verify', { code }),
  // Turn off the current user's own 2FA (refused if the org enforces it).
  disable2fa: () => api.post<{ message: string }>('/auth/2fa/disable'),
  // Personal API tokens: authenticate AS the current user (carry their role).
  personalTokens: {
    list: () => api.get<APIKey[]>('/auth/api-tokens'),
    create: (data: { name: string; expires_at?: string }) =>
      api.post<APIKeyWithSecret>('/auth/api-tokens', data),
    delete: (id: number) => api.delete(`/auth/api-tokens/${id}`),
  },
};

// Cross-org platform admin (instance owner only, gated by PLATFORM_ADMIN_EMAILS).
export interface PlatformOrganization {
  id: number;
  name: string;
  slug: string;
  plan: string;
  trial_ends_at?: string;
  created_at: string;
}

export const platformAdminApi = {
  listOrganizations: () =>
    api.get<PlatformOrganization[]>('/platform-admin/organizations'),
  setPlan: (organizationId: number, plan: string) =>
    api.patch<{ plan: string }>(
      `/platform-admin/organizations/${organizationId}/plan`,
      { plan },
    ),
};

// Contact form (public endpoint, same-origin like every dashboard call)
export const contactApi = {
  send: (data: { name: string; email: string; message: string }) =>
    api.post<{ status: string }>('/contact', data),
};

// Opt-out from an outreach email (public endpoint, token-signed link)
export const unsubscribeApi = {
  confirm: (data: { email: string; token: string }) =>
    api.post<{ status: string }>('/unsubscribe', data),
};

// Public status page: Middle Monitor's own availability, from the checks it
// runs against itself. Unauthenticated — served even when the dashboard is down.
export type StatusLevel = 'operational' | 'degraded' | 'outage' | 'unknown';

export interface StatusDay {
  date: string;
  status: StatusLevel;
  /** Minutes the check spent failing that day; absent on a clean day. */
  downtime_minutes?: number;
}

export interface StatusComponent {
  name: string;
  status: StatusLevel;
  uptime: number;
  days: StatusDay[];
}

export interface StatusIncident {
  component: string;
  /** Classified failure type; the page renders its own localized wording. */
  kind: 'unreachable' | 'cert_expiring' | 'degraded' | 'disruption';
  severity: string;
  started_at: string;
  resolved_at?: string;
}

export interface StatusMaintenance {
  name: string;
  starts_at: string;
  ends_at: string;
}

export interface StatusPage {
  status: StatusLevel;
  window_days: number;
  updated_at: string;
  components: StatusComponent[];
  incidents: StatusIncident[];
  maintenance: StatusMaintenance[];
}

export const statusApi = {
  get: () => api.get<StatusPage>('/status'),
};

// Payments API
export interface CheckoutRequest {
  plan: string;
}

export interface CheckoutResponse {
  url: string;
}

// Organization Stats
export interface OrganizationStats {
  services: {
    total: number;
    healthy: number;
    warning: number;
    failing: number;
    unknown: number;
  };
  hosts: {
    total: number;
    healthy: number;
    failing: number;
  };
  errors: {
    total_24h: number;
    by_service: Record<string, number>;
    impacted_services: number;
  };
  status: 'healthy' | 'degraded' | 'critical';
  plan_usage: {
    hosts_used: number;
    hosts_limit: number; // -1 = unlimited
    services_used: number; // monitored check services
    services_limit: number; // -1 = unlimited
    error_services_used: number;
    error_services_limit: number; // -1 = unlimited
    // Metric ingestion budget: 250 points/min per plan host, pooled.
    points_per_minute_limit: number; // -1 = unlimited
    points_per_minute_peak: number; // busiest minute of the last hour
    points_over_limit_24h: number; // rejected only when enforced
    points_limit_enforced: boolean;
  };
}

// A host's metric points per minute, per scrape target, against the org budget.
export interface TargetIngestCost {
  name: string; // empty for the agent's own system metrics
  points_per_minute: number;
  series: number;
  scrape_interval_seconds: number; // 0 when unknown
  bucket_points_per_minute: number;
  top_metrics: { name: string; points_per_minute: number }[];
}

export interface HostIngestCost {
  window_minutes: number;
  points_per_minute: number;
  targets: TargetIngestCost[];
  org_points_per_minute_limit: number; // -1 = unlimited
  enforced: boolean;
}

// Organization-scoped API factory
// Usage: const orgApi = createOrgApi('my-org-slug');
//        orgApi.hosts.list()
export function createOrgApi(orgSlug: string) {
  const base = `/organizations/${orgSlug}`;

  return {
    // Organization management
    get: () => api.get<Organization>(base),
    update: (data: Partial<Organization>) => api.put<Organization>(base, data),
    // Admin only; confirm must repeat the org slug.
    delete: (confirm: string) => api.delete(base, { data: { confirm } }),
    getUsers: () => api.get<User[]>(`${base}/users`),
    inviteUser: (data: InviteUserRequest) =>
      api.post<{ user: User; message: string }>(`${base}/users`, data),
    updateUserRole: (userId: number, role: string) =>
      api.put<{ message: string; role: string }>(`${base}/users/${userId}`, {
        role,
      }),
    deleteUser: (userId: number) => api.delete(`${base}/users/${userId}`),
    getStats: () => api.get<OrganizationStats>(`${base}/stats`),

    // Dashboard
    dashboard: {
      getHealth: () => api.get<HealthStatus>(`${base}/dashboard/health`),
      getErrorStats: () => api.get<ErrorStats>(`${base}/dashboard/errors`),
      getMetricStats: (service?: string) => {
        const params: Record<string, string> = {};
        if (service) params.service = service;
        return api.get<MetricStats>(`${base}/dashboard/metrics`, { params });
      },
      getTimeline: (service?: string, startDate?: string, endDate?: string) => {
        const params: Record<string, string> = {};
        if (service) params.service = service;
        if (startDate) params.start_date = startDate;
        if (endDate) params.end_date = endDate;
        return api.get<Event[]>(`${base}/dashboard/timeline`, { params });
      },
    },

    // Custom Dashboards (user-built, persisted per organization)
    customDashboards: {
      list: () => api.get<CustomDashboard[]>(`${base}/custom-dashboards`),
      create: (data: { name: string; widgets: DashboardWidget[] }) =>
        api.post<CustomDashboard>(`${base}/custom-dashboards`, data),
      update: (
        id: number,
        data: { name: string; widgets: DashboardWidget[] },
      ) => api.put<CustomDashboard>(`${base}/custom-dashboards/${id}`, data),
      delete: (id: number) => api.delete(`${base}/custom-dashboards/${id}`),
    },

    // Errors
    errors: {
      list: (opts?: {
        service?: string;
        grouped?: boolean;
        window?: string;
        start?: string;
        end?: string;
        limit?: number;
        offset?: number;
      }) => {
        const params: Record<string, string> = {};
        if (opts?.service) params.service = opts.service;
        if (opts?.grouped) params.grouped = '1';
        if (opts?.window) params.window = opts.window;
        if (opts?.start) params.start = opts.start;
        if (opts?.end) params.end = opts.end;
        if (opts?.limit != null) params.limit = String(opts.limit);
        if (opts?.offset != null) params.offset = String(opts.offset);
        return api.get<ApplicationError[] | ErrorGroup[]>(`${base}/errors`, {
          params,
        });
      },
      getServices: () => api.get<Service[]>(`${base}/errors/services`),
      getCorrelation: (errorId: number, window?: string) => {
        const params: Record<string, string> = {};
        if (window) params.window = window;
        return api.get<CorrelationResult>(
          `${base}/errors/${errorId}/correlation`,
          { params },
        );
      },
      explain: (errorId: number, force = false) => {
        const params: Record<string, string> = { locale: explainLocale() };
        if (force) params.force = 'true';
        return api.post<ExplanationResponse>(
          `${base}/errors/${errorId}/explain`,
          null,
          { params },
        );
      },
      explainSSE: async function* (
        errorId: number,
        force = false,
      ): AsyncGenerator<{ type: 'metadata' | 'chunk' | 'error'; data: any }> {
        const token = localStorage.getItem('auth_token');
        const params = new URLSearchParams({ locale: explainLocale() });
        if (force) params.append('force', 'true');
        const url = `/api/v1${base}/errors/${errorId}/explain?${params.toString()}`;

        const response = await fetch(url, {
          method: 'POST',
          headers: {
            Accept: 'text/event-stream',
            'Accept-Language': explainLocale(),
            ...(token ? { Authorization: `Bearer ${token}` } : {}),
          },
        });

        if (!response.ok) {
          const errText = await response.text();
          throw new Error(errText || `HTTP error! status: ${response.status}`);
        }

        const reader = response.body?.getReader();
        const decoder = new TextDecoder();
        let buffer = '';

        if (!reader) return;

        while (true) {
          const { done, value } = await reader.read();
          if (done) break;

          buffer += decoder.decode(value, { stream: true });
          const lines = buffer.split('\n\n');
          buffer = lines.pop() || '';

          for (const block of lines) {
            const eventMatch = block.match(/^event: (.*)\n/);
            const dataMatch = block.match(/(?:^|\n)data: (.*)/);

            if (dataMatch) {
              const dataStr = dataMatch[1];
              if (dataStr === '[DONE]') return;
              const eventType = eventMatch ? eventMatch[1] : 'chunk';
              try {
                yield { type: eventType as any, data: JSON.parse(dataStr) };
              } catch (e) {
                yield { type: eventType as any, data: dataStr };
              }
            }
          }
        }
      },
    },

    // Payments
    payments: {
      checkout: (data: CheckoutRequest) =>
        api.post<CheckoutResponse>(`${base}/checkout`, data),
      checkoutCustom: (data: {
        hosts: number;
        services: number;
        sdk_services: number;
        retention_days: number;
      }) => api.post<CheckoutResponse>(`${base}/checkout/custom`, data),
      getSubscription: () =>
        api.get<{
          hosts: number;
          services: number;
          sdk_services: number;
          retention_days: number;
        }>(`${base}/subscription`),
      updateSubscription: (data: {
        hosts: number;
        services: number;
        sdk_services: number;
        retention_days: number;
      }) => api.put<{ status: string }>(`${base}/subscription`, data),
      billingPortal: () => api.post<CheckoutResponse>(`${base}/billing-portal`),
    },

    // Send a test email through the org's own SMTP config.
    testSmtp: (email?: string) =>
      api.post<{ status: string; to: string }>(`${base}/smtp/test`, { email }),

    // Application links (for alert correlation: link app service to hosts/services)
    links: {
      list: () => api.get<ApplicationLink[]>(`${base}/links`),
      getSuggestions: (service: string) =>
        api.get<LinkSuggestionsResponse>(`${base}/links/suggestions`, {
          params: { service },
        }),
      create: (
        data: Omit<ApplicationLink, 'id' | 'organization_id' | 'target_name'>,
      ) => api.post<ApplicationLink>(`${base}/links`, data),
      delete: (id: number) => api.delete(`${base}/links/${id}`),
    },

    // Host groups (scope correlation to a host + the hosts sharing its role)
    hostGroups: {
      list: () => api.get<HostGroup[]>(`${base}/host-groups`),
      create: (data: { name: string; display_name?: string }) =>
        api.post<HostGroup>(`${base}/host-groups`, data),
      update: (id: number, data: { name: string; display_name?: string }) =>
        api.put(`${base}/host-groups/${id}`, data),
      delete: (id: number) => api.delete(`${base}/host-groups/${id}`),
    },

    // Metrics
    metrics: {
      list: (service?: string) => {
        const params: Record<string, string> = {};
        if (service) params.service = service;
        return api.get<SystemMetric[]>(`${base}/metrics`, { params });
      },
    },

    // Hosts
    hosts: {
      // Pagination/filters are opt-in: callers that pass no opts get the full
      // list (host pickers, dashboards...). The hosts page passes limit/offset.
      list: (opts?: {
        search?: string;
        status?: string;
        limit?: number;
        offset?: number;
      }) => {
        const params: Record<string, string> = {};
        if (opts?.search) params.search = opts.search;
        if (opts?.status && opts.status !== 'all') params.status = opts.status;
        if (opts?.limit != null) params.limit = String(opts.limit);
        if (opts?.offset != null) params.offset = String(opts.offset);
        return api.get<Host[]>(`${base}/hosts`, { params });
      },
      stats: () => api.get<HostStats>(`${base}/hosts/stats`),
      getById: (hostId: number) => api.get<Host>(`${base}/hosts/${hostId}`),
      // Points per minute per scrape target over the last minutes.
      ingestCost: (hostId: number) => api.get<HostIngestCost>(`${base}/hosts/${hostId}/ingest-cost`),
      create: (
        hostData: Omit<Host, 'id' | 'created_at' | 'status' | 'services'>,
      ) => api.post<Host>(`${base}/hosts`, hostData),
      update: (hostId: number, hostData: Partial<Host>) =>
        api.put<Host>(`${base}/hosts/${hostId}`, hostData),
      assignGroup: (hostId: number, hostGroupId: number) =>
        api.put(`${base}/hosts/${hostId}/group`, {
          host_group_id: hostGroupId,
        }),
      delete: (hostId: number) => api.delete(`${base}/hosts/${hostId}`),
      getServices: (
        hostId: number,
        opts?: { start_date?: string; end_date?: string },
      ) => {
        const params: Record<string, string> = {};
        if (opts?.start_date) params.start_date = opts.start_date;
        if (opts?.end_date) params.end_date = opts.end_date;
        return api.get<ServiceWithResults[]>(
          `${base}/hosts/${hostId}/services`,
          { params },
        );
      },
      getServicesByName: (hostName: string) =>
        api.get<ServiceWithResults[]>(
          `${base}/hosts/by-name/${encodeURIComponent(hostName)}/services`,
        ),
    },

    // Services
    services: {
      getById: (serviceId: number) =>
        api.get<ServiceWithResults>(`${base}/services/${serviceId}`),
      // Pagination/filters are opt-in: callers passing no limit/offset get the
      // full list (service pickers, dashboards...); the services page paginates.
      list: (opts?: {
        service?: string;
        start_date?: string;
        end_date?: string;
        search?: string;
        status?: string;
        type?: string;
        limit?: number;
        offset?: number;
      }) => {
        const params: Record<string, string> = {};
        if (opts?.service) params.service = opts.service;
        if (opts?.start_date) params.start_date = opts.start_date;
        if (opts?.end_date) params.end_date = opts.end_date;
        if (opts?.search) params.search = opts.search;
        if (opts?.status && opts.status !== 'all') params.status = opts.status;
        if (opts?.type && opts.type !== 'all') params.type = opts.type;
        if (opts?.limit != null) params.limit = String(opts.limit);
        if (opts?.offset != null) params.offset = String(opts.offset);
        return api.get<ServiceWithResults[]>(`${base}/services`, { params });
      },
      stats: () => api.get<ServiceStats>(`${base}/services/stats`),
      create: (serviceData: Omit<Service, 'id' | 'created_at'>) =>
        api.post<Service>(`${base}/services`, serviceData),
      update: (serviceId: number, serviceData: Partial<Service>) =>
        api.put<Service>(`${base}/services/${serviceId}`, serviceData),
      delete: (serviceId: number) =>
        api.delete(`${base}/services/${serviceId}`),
      getResults: (serviceId: number, startDate?: string, endDate?: string) => {
        const params: Record<string, string> = {};
        if (startDate) params.start_date = startDate;
        if (endDate) params.end_date = endDate;
        return api.get<ServiceResult[]>(
          `${base}/services/${serviceId}/results`,
          { params },
        );
      },
      getCorrelation: (resultId: number) =>
        api.get<CorrelationResult>(
          `${base}/services/results/${resultId}/correlation`,
        ),
      explainResult: (resultId: number, force = false) => {
        const params: Record<string, string> = { locale: explainLocale() };
        if (force) params.force = 'true';
        return api.post<ExplanationResponse>(
          `${base}/services/results/${resultId}/explain`,
          null,
          { params },
        );
      },
      explainResultSSE: async function* (
        resultId: number,
        force = false,
      ): AsyncGenerator<{ type: 'metadata' | 'chunk' | 'error'; data: any }> {
        const token = localStorage.getItem('auth_token');
        const params = new URLSearchParams({ locale: explainLocale() });
        if (force) params.append('force', 'true');
        const url = `/api/v1${base}/services/results/${resultId}/explain?${params.toString()}`;

        const response = await fetch(url, {
          method: 'POST',
          headers: {
            Accept: 'text/event-stream',
            'Accept-Language': explainLocale(),
            ...(token ? { Authorization: `Bearer ${token}` } : {}),
          },
        });

        if (!response.ok) {
          const errText = await response.text();
          throw new Error(errText || `HTTP error! status: ${response.status}`);
        }

        const reader = response.body?.getReader();
        const decoder = new TextDecoder();
        let buffer = '';

        if (!reader) return;

        while (true) {
          const { done, value } = await reader.read();
          if (done) break;

          buffer += decoder.decode(value, { stream: true });
          const lines = buffer.split('\n\n');
          buffer = lines.pop() || '';

          for (const block of lines) {
            const eventMatch = block.match(/^event: (.*)\n/);
            const dataMatch = block.match(/(?:^|\n)data: (.*)/);

            if (dataMatch) {
              const dataStr = dataMatch[1];
              if (dataStr === '[DONE]') return;
              const eventType = eventMatch ? eventMatch[1] : 'chunk';
              try {
                yield { type: eventType as any, data: JSON.parse(dataStr) };
              } catch (e) {
                yield { type: eventType as any, data: dataStr };
              }
            }
          }
        }
      },
      getAgentMetrics: (serviceId: number) =>
        api.get<AgentMetricPoint[]>(
          `${base}/services/${serviceId}/agent-metrics`,
        ),
    },

    // Events
    events: {
      list: () => api.get<Event[]>(`${base}/events`),
      create: (eventData: Partial<Event>) =>
        api.post<Event>(`${base}/events`, eventData),
    },

    // Notification Channels
    notificationChannels: {
      list: () =>
        api.get<NotificationChannel[]>(`${base}/notification-channels`),
      create: (data: Partial<NotificationChannel>) =>
        api.post<NotificationChannel>(`${base}/notification-channels`, data),
      update: (id: number, data: Partial<NotificationChannel>) =>
        api.put<NotificationChannel>(
          `${base}/notification-channels/${id}`,
          data,
        ),
      delete: (id: number) => api.delete(`${base}/notification-channels/${id}`),
      test: (id: number) =>
        api.post<{ delivered: boolean }>(
          `${base}/notification-channels/${id}/test`,
        ),
    },

    // Alert Rules
    alertRules: {
      list: () => api.get<AlertRule[]>(`${base}/alert-rules`),
      create: (data: Partial<AlertRule>) =>
        api.post<AlertRule>(`${base}/alert-rules`, data),
      update: (id: number, data: Partial<AlertRule>) =>
        api.put<AlertRule>(`${base}/alert-rules/${id}`, data),
      delete: (id: number) => api.delete(`${base}/alert-rules/${id}`),
      toggle: (id: number, enabled: boolean) =>
        api.patch(`${base}/alert-rules/${id}/toggle`, { enabled }),
    },

    // Incidents
    incidents: {
      list: (opts?: {
        status?: string;
        severity?: string;
        service?: string;
        search?: string;
        limit?: number;
        offset?: number;
      }) => {
        const params: Record<string, string> = {};
        if (opts?.status) params.status = opts.status;
        if (opts?.severity) params.severity = opts.severity;
        if (opts?.service) params.service = opts.service;
        if (opts?.search) params.search = opts.search;
        if (opts?.limit != null) params.limit = String(opts.limit);
        if (opts?.offset != null) params.offset = String(opts.offset);
        return api.get<Incident[]>(`${base}/incidents`, { params });
      },
      stats: () => api.get<IncidentStats>(`${base}/incidents/stats`),
      create: (data: Partial<Incident>) =>
        api.post<Incident>(`${base}/incidents`, data),
      updateStatus: (id: number, status: string, resolutionNote?: string) =>
        api.put(`${base}/incidents/${id}/status`, {
          status,
          ...(resolutionNote?.trim()
            ? { resolution_note: resolutionNote.trim() }
            : {}),
        }),
    },

    // Maintenance / downtime windows
    maintenance: {
      list: () => api.get<MaintenanceWindow[]>(`${base}/maintenance-windows`),
      create: (data: {
        name: string;
        target_type: 'service' | 'host';
        target_id: number;
        starts_at: string;
        ends_at: string;
      }) => api.post<MaintenanceWindow>(`${base}/maintenance-windows`, data),
      delete: (id: number) => api.delete(`${base}/maintenance-windows/${id}`),
    },

    // API Keys
    apiKeys: {
      list: () => api.get<APIKey[]>(`${base}/api-keys`),
      create: (data: { name: string; expires_at?: string }) =>
        api.post<APIKeyWithSecret>(`${base}/api-keys`, data),
      delete: (id: number) => api.delete(`${base}/api-keys/${id}`),
    },

    // Install Tokens (agent install → org)
    installTokens: {
      list: () => api.get<InstallToken[]>(`${base}/install-tokens`),
      create: (data: { name?: string; expires_at?: string }) =>
        api.post<InstallTokenWithSecret>(`${base}/install-tokens`, data),
      delete: (id: number) => api.delete(`${base}/install-tokens/${id}`),
    },

    // Logs (OpenSearch)
    logs: {
      search: (params: {
        q?: string;
        service?: string;
        severity?: string;
        start?: string;
        end?: string;
        from?: number;
        size?: number;
      }) => api.get<LogSearchResult>(`${base}/logs`, { params }),
      fieldValues: (params: {
        field: string;
        prefix?: string;
        start?: string;
        end?: string;
        size?: number;
      }) =>
        api.get<{ values: string[] }>(`${base}/logs/field-values`, { params }),
    },

    // Traces (OpenSearch)
    traces: {
      search: (params: {
        q?: string;
        service?: string;
        start?: string;
        end?: string;
        from?: number;
        size?: number;
      }) => api.get<TraceSearchResult>(`${base}/traces`, { params }),
    },

    // Network metrics
    network: {
      getMetrics: (start?: string, end?: string) => {
        const params: Record<string, string> = {};
        if (start) params.start = start;
        if (end) params.end = end;
        return api.get<NetworkMetric[]>(`${base}/network`, { params });
      },
    },

    // Metrics explorer
    metricsExplorer: {
      get: (params: {
        service?: string;
        host?: string;
        start?: string;
        end?: string;
      }) =>
        api.get<MetricsExplorerResult>(`${base}/metrics/explorer`, { params }),
    },

    // Custom metric series: discovery then range query
    metricSeries: {
      names: (params?: {
        prefix?: string;
        start?: string;
        end?: string;
        size?: number;
        host_id?: number;
        host_group_id?: number;
      }) =>
        api.get<{ names: string[] }>(`${base}/metrics/series/names`, {
          params: params ?? {},
        }),
      labelKeys: (params: {
        metric?: string;
        start?: string;
        end?: string;
        size?: number;
        host_id?: number;
        host_group_id?: number;
      }) =>
        api.get<{ keys: string[] }>(`${base}/metrics/series/label-keys`, { params }),
      labelValues: (params: {
        key: string;
        metric?: string;
        prefix?: string;
        start?: string;
        end?: string;
        size?: number;
        host_id?: number;
        host_group_id?: number;
      }) => api.get<{ values: string[] }>(`${base}/metrics/series/label-values`, { params }),
      // filter is repeated: one "key:value" per label constraint.
      query: (params: {
        metric: string;
        filter?: string[];
        group_by?: string;
        aggregation?: SeriesAggregation;
        start?: string;
        end?: string;
        step?: number;
        host_id?: number;
        host_group_id?: number;
      }) => api.get<{ series: MetricSeries[] }>(`${base}/metrics/series/query`, { params }),
      // Several queries combined by math ("$A / $B * 100"); empty expression returns them all.
      expression: (
        body: { queries: SeriesExpressionQuery[]; expression?: string },
        params: { start?: string; end?: string; step?: number; host_id?: number; host_group_id?: number },
      ) =>
        api.post<{ series: MetricSeries[] }>(`${base}/metrics/series/expression`, body, { params }),
    },

    // Profiling (pprof captures)
    profiles: {
      list: (params?: {
        service?: string;
        profile_type?: string;
        limit?: number;
        offset?: number;
      }) =>
        api.get<ProfileCapture[]>(`${base}/profiles`, { params: params ?? {} }),
      getSeries: (params?: {
        service?: string;
        from?: string;
        to?: string;
        limit?: number;
      }) =>
        api.get<ProfileSeriesResponse>(`${base}/profiles/series`, {
          params: params ?? {},
        }),
      download: (id: number) =>
        api.get<Blob>(`${base}/profiles/${id}/download`, {
          responseType: 'blob',
        }),
      getFlamegraph: (id: number) =>
        api.get<FlameNode>(`${base}/profiles/${id}/flamegraph`),
    },
  };
}

// Types
export interface HealthStatus {
  /** healthy | degraded | critical, same escalation as OrganizationStats.status */
  status: string;
  services: number;
  services_failing: number;
  errors_24h?: number;
  last_update?: string;
}

export interface SQLHealthCheck {
  status: string;
  message: string;
  latency_ms: number;
  timestamp: string;
}

export interface ErrorStats {
  total: number;
  by_service: Record<string, number>;
  by_error: Record<string, number>;
  recent: ApplicationError[];
}

export interface MetricStats {
  cpu_avg: number;
  ram_avg: number;
  http_latency?: number;
  last_update: string;
}

export interface ApplicationError {
  id: number;
  name: string;
  message: string;
  file: string;
  line: number;
  timestamp: string;
  service: string;
  http_method?: string;
  http_url?: string;
  http_headers?: string;
  http_body?: string;
  // Links the error to a distributed trace (set by SDKs when tracing is active).
  trace_id?: string;
}

export interface InfraCorrelation {
  metric_name: string;
  value: number;
  threshold: number;
  description: string;
  confidence?: number;
}

export interface ServiceCorrelation {
  service_id: number;
  service_name: string;
  status: string;
  description: string;
  occurred_at?: string;
  preceded_incident?: boolean;
  confidence?: number;
}

// Another application that also errored in the window. relation qualifies the
// tie: "dependency" (declared app->app link, strongest when it errored first),
// "dependent" (downstream impact), "host_group" (co-occurrence context).
export interface AppCorrelation {
  service: string;
  error_name: string;
  count: number;
  occurred_at?: string;
  preceded_incident: boolean;
  relation?: 'dependency' | 'dependent' | 'host_group' | string;
  confidence?: number;
}

// How often the same error (by fingerprint) occurs: new regression vs chronic.
export interface ErrorRecurrence {
  fingerprint: string;
  count_last_hour: number;
  count_24h: number;
  count_7d: number;
  first_seen?: string;
  last_seen?: string;
  is_new: boolean;
  is_recurrent: boolean;
  description: string;
}

/** A check on the same host group that slowed down without ever failing. */
export interface DegradedNeighbour {
  service_id: number;
  service_name: string;
  host_name?: string;
  check_type?: string;
  latency_ms: number;
  baseline_ms: number;
  multiplier: number;
}

export interface CorrelationResult {
  has_correlation: boolean;
  host_id?: number;
  host_name?: string;
  infra: InfraCorrelation[];
  services: ServiceCorrelation[];
  degraded?: DegradedNeighbour[];
  apps: AppCorrelation[];
  semantic_matches: string[];
  recurrence?: ErrorRecurrence;
  confidence?: number;
  summary: string;
}

/** Grouped errors (s-style): same name+message+file with event count + sparkline */
export interface TimeSeriesPoint {
  date: string;
  count: number;
}

export interface ErrorGroup {
  event_count: number;
  first_seen: string;
  last_seen: string;
  sample_id: number;
  sample: ApplicationError;
  timeseries: TimeSeriesPoint[];
}

// LLM-generated root-cause explanation (returned by POST .../explain)
export interface ExplanationResponse {
  subject_type: 'error' | 'service_result';
  subject_id: number;
  content: string;
  model?: string;
  duration_ms?: number;
  cached: boolean;
  created_at: string;
}

// Application link: app (by service name) → host, service, host group, or
// another application (for correlation). App targets are name-based:
// target_app_name carries the other app's service tag and target_id stays 0,
// so the target app doesn't need to be registered.
export interface ApplicationLink {
  id: number;
  organization_id: number;
  app_service_name: string;
  target_type: 'host' | 'service' | 'host_group' | 'app';
  target_id: number;
  target_app_name?: string;
  target_name?: string;
}

export interface LinkSuggestion {
  target_type: 'host' | 'service';
  target_id: number;
  target_name: string;
  reason: string;
}

// "tracing_not_configured": no OTel/OpenSearch pipeline at all.
// "no_trace_data": tracing is set up but this app has no recent spans.
export type LinkSuggestionsEmptyReason =
  | 'tracing_not_configured'
  | 'no_trace_data'
  | '';

export interface LinkSuggestionsResponse {
  suggestions: LinkSuggestion[];
  empty_reason?: LinkSuggestionsEmptyReason;
}

// A host group: a set of hosts sharing the same logical role. Correlation is
// scoped to a host and its group. Each org has one default group.
export interface HostGroup {
  id: number;
  organization_id: number;
  name: string;
  display_name?: string | null;
  is_default: boolean;
  created_at: string;
  host_count?: number;
}

export interface SystemMetric {
  id: number;
  service: string;
  host?: string;
  cpu_perc: number;
  ram_perc: number;
  http_latency?: number;
  endpoint?: string;
  timestamp: string;
}

export interface Service {
  id: number;
  host_id?: number;
  name: string;
  display_name?: string | null;
  type: string;
  host: string;
  path?: string;
  credentials?: string;
  service: string;
  service_interval: number;
  max_attempts: number;
  failure_threshold?: number; // legacy single threshold (kept for back-compat)
  warning_threshold?: number | null; // raises a "warning" alert when crossed
  critical_threshold?: number | null; // raises a "critical" alert when crossed
  expected_status_code?: number | null; // HTTP: exact status code treated as success; null = any 2xx (default)
  expected_body_contains?: string | null; // HTTP: body assertion value — substring, or "path=value" in json_path mode; null/empty = not checked
  expected_body_mode?: string | null; // HTTP body assertion mode: "contains" (default) or "json_path"
  created_at: string;
  token?: string; // Token for error services
  /** API response: HTTP check has auth configured (secrets never returned) */
  http_auth_configured?: boolean;
  /** API response: none | bearer | basic */
  http_auth_mode?: string;
  /** API request (update): keep existing bearer/basic secrets when fields left blank */
  preserve_http_secrets?: boolean;
}

export interface ServiceResult {
  id: number;
  service_id: number;
  status: string;
  latency?: number;
  message?: string;
  timestamp: string;
  // Agent metrics fields (only present for agent services)
  metric_type?: string; // cpu, ram, disk
  metric_value?: number;
  metadata?: string; // JSON string
}

export interface AgentMetricPoint {
  value: number;
  metadata?: {
    ram_total_gb?: number;
    disk_total_gb?: number;
    disk_free_gb?: number;
    load_1min?: number;
    load_5min?: number;
    load_15min?: number;
    network_bytes_in_total?: number;
    network_bytes_out_total?: number;
    network_speed_in_mb_per_s?: number;
    network_speed_out_mb_per_s?: number;
    // Backward compatibility
    network_speed_in_mbps?: number;
    network_speed_out_mbps?: number;
  };
  timestamp: string;
}

export interface Host {
  id: number;
  name: string;
  display_name?: string | null;
  host: string;
  service: string;
  host_group_id?: number | null;
  created_at: string;
  status?: string;
  services?: Service[];
  service_count?: number;
  healthy_count?: number;
  failing_count?: number;
  warning_count?: number;
  critical_count?: number;
}

// Global host facet counts (independent of filters/page)
export interface HostStats {
  total: number;
  healthy: number;
  failing: number;
  warning: number;
  unknown: number;
  total_services: number;
}

export interface Event {
  id: number;
  type: string;
  service: string;
  message: string;
  metadata?: string;
  timestamp: string;
}

// Alert Rule
export interface AlertRule {
  id: number;
  organization_id: number;
  name: string;
  description?: string;
  type: string; // 'threshold'
  target_type: string; // 'service' | 'host' | 'any'
  target_id?: number;
  metric: string; // 'cpu' | 'ram' | 'disk' | 'latency' | 'error_count' | 'failure_rate'
  operator: string; // 'gt' | 'gte' | 'lt' | 'lte'
  threshold: number; // legacy single-level threshold
  duration: number; // seconds — evaluation window
  severity: string; // legacy single-level severity ('critical' | 'warning')
  // Policy fields (d/s-style)
  aggregation?: string; // 'avg'|'min'|'max'|'sum'|'p50'|'p75'|'p90'|'p95'|'p99'
  warning_threshold?: number | null;
  critical_threshold?: number | null;
  recovery_threshold?: number | null;
  // Routing fields: a rule routes a fired severity to its channels with tags.
  tags?: string; // comma-separated, forwarded to JSM
  notify_warning?: boolean;
  notify_critical?: boolean;
  enabled: boolean;
  channels: number[]; // notification channel IDs
  created_at: string;
  updated_at: string;
}

// Notification Channel
export interface NotificationChannel {
  id: number;
  organization_id: number;
  name: string;
  type: string; // 'email', 'slack', 'jsm', 'whatsapp'
  config: Record<string, any>;
  enabled: boolean;
  created_at: string;
  updated_at: string;
}

// Custom Dashboards: widgets (incl. their grid layout) are an opaque blob the
// backend stores verbatim; their shape is owned by the frontend.
export interface DashboardWidgetLayout {
  i: string;
  x: number;
  y: number;
  w: number;
  h: number;
}

// A custom metric widget carries its whole query. Kept as an optional field
// beside `metric` so dashboards saved before custom metrics existed still load.
export interface DashboardSeriesQuery {
  metric: string;
  filters?: { key: string; value: string }[];
  group_by?: string;
  aggregation?: SeriesAggregation;
  // When set, these replace the single query above, which only names the widget.
  queries?: SeriesExpressionQuery[];
  expression?: string;
}

export interface DashboardWidget {
  id: string;
  type: 'stat' | 'chart' | 'list';
  metric: string;
  title: string;
  size: 'small' | 'medium' | 'large';
  filterHost?: string;
  filterService?: string;
  series?: DashboardSeriesQuery;
  layout?: DashboardWidgetLayout;
}

export interface CustomDashboard {
  id: number;
  organization_id: number;
  name: string;
  widgets: DashboardWidget[];
  created_at: string;
  updated_at: string;
}

// Incident
export interface Incident {
  id: number;
  organization_id: number;
  alert_rule_id?: number;
  service_id?: number;
  host_id?: number;
  title: string;
  description?: string;
  severity: string;
  status: string;
  service?: string;
  started_at: string;
  resolved_at?: string;
  resolution_note?: string;
  acknowledged_at?: string;
  acknowledged_by?: number;
}

// Global incident facet counts + distinct services (independent of filters/page)
export interface IncidentStats {
  total: number;
  open: number;
  acknowledged: number;
  resolved: number;
  critical: number;
  services: string[];
}

// API Key
export interface APIKey {
  id: number;
  organization_id: number;
  name: string;
  key_prefix: string;
  scopes: string;
  last_used_at?: string;
  expires_at?: string;
  created_by?: number;
  created_at: string;
}

export interface APIKeyWithSecret extends APIKey {
  key: string;
}

export interface InstallToken {
  id: number;
  organization_id: number;
  token?: string; // Full token only on create
  token_prefix?: string; // First 8 chars for list display
  name?: string;
  created_by?: number;
  created_at: string;
  expires_at?: string;
}

export interface InstallTokenWithSecret extends InstallToken {
  token: string; // full token on create
}

// Log search
export interface LogSearchResult {
  hits: LogEntry[];
  total: number;
  from: number;
  size: number;
}

export interface LogEntry {
  _id: string;
  body: string;
  severity_text: string;
  severity: string;
  service_name: string;
  hostname: string;
  trace_id?: string;
  span_id?: string;
  '@timestamp': string;
  attributes?: Record<string, unknown>;
}

// Trace search
export interface TraceSearchResult {
  hits: TraceSpan[];
  total: number;
  from: number;
  size: number;
}

export interface TraceSpan {
  _id: string;
  trace_id: string;
  span_id: string;
  parent_span_id?: string;
  service_name: string;
  operation_name: string;
  span_kind: string;
  status_code: string;
  status_message?: string;
  start_time: string;
  end_time: string;
  duration_ms: number;
  hostname?: string;
  '@timestamp': string;
  attributes?: Record<string, unknown>;
}

// Network metric
export interface NetworkMetric {
  id: number;
  service_id: number;
  service_name: string;
  host: string;
  status: string;
  value?: number;
  metadata?: string;
  timestamp: string;
}

// Metrics explorer
export interface MetricsExplorerResult {
  metrics: SystemMetric[];
  filters: { service: string; host: string }[];
}

// Custom metric series (labelled metrics ingested over OTLP)
export type SeriesAggregation =
  | 'avg'
  | 'min'
  | 'max'
  | 'sum'
  | 'count'
  | 'p50'
  | 'p75'
  | 'p90'
  | 'p95'
  | 'p99';

// rate is per-second increase of a counter, served by the expression endpoint only.
export type ExpressionAggregation = SeriesAggregation | 'rate';

export interface SeriesExpressionQuery {
  ref: string;
  metric: string;
  filters?: { key: string; value: string }[];
  group_by?: string;
  aggregation: ExpressionAggregation;
}

export interface SeriesDataPoint {
  timestamp: string;
  value: number | null;
}

export interface MetricSeries {
  metric_name: string;
  labels: Record<string, string>;
  points: SeriesDataPoint[];
  // Ref of the query that produced the series, absent on an expression result.
  query?: string;
}

// Profile capture (pprof)
export interface ProfileCapture {
  id: number;
  organization_id: number;
  service: string;
  profile_type: string;
  duration_seconds?: number;
  size_bytes: number;
  memory_mb?: number;
  created_at: string;
}

// Profile memory time series (for RAM chart)
export interface ProfileSeriesPoint {
  timestamp: string;
  memory_mb: number;
  service: string;
}
export interface ProfileSeriesResponse {
  points: ProfileSeriesPoint[];
}

// Flame graph tree (for d3-flame-graph)
// unit/valueType describe what `value` measures and are only set on the root node.
export interface FlameNode {
  name: string;
  value: number;
  unit?: string;
  valueType?: string;
  children?: FlameNode[];
}

// Public API (no org scope)
export const getSQLHealthCheck = () => api.get<SQLHealthCheck>('/health/sql');

export interface ServiceWithResults extends Service {
  results: ServiceResult[];
  host_name?: string; // Name of the parent host, if any
}

// Global service facet counts + distinct types (independent of filters/page)
export interface ServiceStats {
  total: number;
  healthy: number;
  failing: number;
  warning: number;
  unknown: number;
  types: string[];
}
