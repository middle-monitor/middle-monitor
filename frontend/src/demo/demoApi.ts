import type { AxiosResponse, InternalAxiosRequestConfig } from 'axios';
import i18n from '../i18n';
import {
  createOrgApi,
  type AgentMetricPoint,
  type ApplicationError,
  type ApplicationLink,
  type CorrelationResult,
  type CustomDashboard,
  type ErrorGroup,
  type ErrorStats,
  type Event,
  type ExplanationResponse,
  type FlameNode,
  type Host,
  type HostGroup,
  type Incident,
  type LogEntry,
  type MaintenanceWindow,
  type MetricSeries,
  type NotificationChannel,
  type OrganizationStats,
  type ProfileCapture,
  type Service,
  type ServiceResult,
  type ServiceWithResults,
  type SystemMetric,
  type TimeSeriesPoint,
  type TraceSpan,
  type User,
} from '../api';
import { demoOrganization, demoUser, demoWritesEnabled } from './demoMode';
import { compileLogQuery, compileTraceQuery } from './demoQuery';
import { AGENT_PERCENT_TYPES, getServiceStatus } from '../utils/serviceStatus';

/**
 * In-memory replacement for createOrgApi, used behind /demo. Every read returns
 * synthetic telemetry generated relative to Date.now(), so the demo always shows
 * fresh data; values are seeded per minute bucket, so polling views see the data
 * evolve while pagination within a call stays consistent. Writes are rejected
 * with a localized "read-only demo" error.
 *
 * TypeScript pins this to the real client's shape (ReturnType<typeof createOrgApi>),
 * so any API change that would break the demo breaks the build instead.
 */
type OrgApi = ReturnType<typeof createOrgApi>;

// ---------------------------------------------------------------------------
// Plumbing
// ---------------------------------------------------------------------------

// Demo reads have no network behind them, so nothing can be intercepted to
// make one fail. This flag is the only lever e2e has to check that a view
// survives a failed revalidation; no UI sets it. '1' fails every read, a
// comma-separated list fails only the reads it names. Read in dev only, so the
// seam cannot be reached from the public demo.
const FAIL_READS_KEY = 'mm.demo.fail-reads';

function failingReads(): string {
  if (!import.meta.env.DEV) return '';
  try {
    return sessionStorage.getItem(FAIL_READS_KEY) ?? '';
  } catch {
    return '';
  }
}

function readsFail(): boolean {
  return failingReads() === '1';
}

// Demo responses resolve within the tick, so the first load of a view is never
// observable. This key holds them back by that many milliseconds, which is how
// the e2e suite drives a slow first load without a backend. Read in dev only,
// so the public demo cannot be slowed down from the outside.
const LATENCY_KEY = 'mm.demo.latency';

function demoLatency(): number {
  if (!import.meta.env.DEV) return 0;
  try {
    return Number(sessionStorage.getItem(LATENCY_KEY)) || 0;
  } catch {
    return 0;
  }
}

// A read named here answers within the tick while the rest of the view is
// still held back, which is how e2e drives the two queries of a view apart:
// the real backend serves a list and its facets from separate endpoints, so
// either can answer first.
const INSTANT_READS_KEY = 'mm.demo.instant-reads';

function isInstantRead(name?: string): boolean {
  if (!name || !import.meta.env.DEV) return false;
  try {
    return (sessionStorage.getItem(INSTANT_READS_KEY) ?? '').split(',').includes(name);
  } catch {
    return false;
  }
}

// The sandbox holds fewer services than the 50 a page the list asks for, so
// page 2 is out of reach — and a write that sent the reader back to page 1
// would go unseen. This key appends that many filler http checks, which is how
// the e2e net saves the service modal from a paginated list. Read in dev only,
// so the public demo keeps its own set.
const SERVICES_PADDING_KEY = 'mm.demo.services-padding';

function servicesPadding(): number {
  if (!import.meta.env.DEV) return 0;
  try {
    return Number(sessionStorage.getItem(SERVICES_PADDING_KEY)) || 0;
  } catch {
    return 0;
  }
}

// Same seam for the incident list, which holds seven rows against a page of
// fifty: without filler the pager has a single page, so neither a page change
// nor the pager staying usable through one can be observed. Dev only too.
const INCIDENTS_PADDING_KEY = 'mm.demo.incidents-padding';

function incidentsPadding(): number {
  if (!import.meta.env.DEV) return 0;
  try {
    return Number(sessionStorage.getItem(INCIDENTS_PADDING_KEY)) || 0;
  } catch {
    return 0;
  }
}

function respond<T>(
  data: T,
  headers: Record<string, string>,
  name?: string,
): Promise<AxiosResponse<T>> {
  if (readsFail()) return Promise.reject(new Error('demo read failed'));
  const response = {
    data,
    status: 200,
    statusText: 'OK',
    headers,
    config: {} as InternalAxiosRequestConfig,
  };
  const delay = isInstantRead(name) ? 0 : demoLatency();
  if (delay <= 0) return Promise.resolve(response);
  return new Promise((resolve) => setTimeout(() => resolve(response), delay));
}

function ok<T>(data: T, name?: string): Promise<AxiosResponse<T>> {
  return respond(data, {}, name);
}

// okPaged mirrors a server-paginated list response: it exposes the total item
// count through the X-Total-Count header, same as the real backend, so views
// can read res.headers['x-total-count'] uniformly in demo mode.
function okPaged<T>(data: T, total: number, name?: string): Promise<AxiosResponse<T>> {
  return respond(data, { 'x-total-count': String(total) }, name);
}

function readOnly(): Promise<never> {
  return Promise.reject(new Error(i18n.t('demo.read_only')));
}

// A named read answers like a definitive 4xx instead of returning data, so one
// query of a view can be failed on its own (see e2e/secondary-query-failure.spec.ts).
function injectedFailure(name: string): Promise<never> | null {
  if (!failingReads().split(',').includes(name)) return null;

  const error = new Error(`demo failure: ${name}`) as Error & { response: { status: number } };
  error.response = { status: 400 };
  return Promise.reject(error);
}

function locale(): 'en' | 'fr' {
  return i18n.language === 'fr' ? 'fr' : 'en';
}

function iso(ms: number): string {
  return new Date(ms).toISOString();
}

const MINUTE = 60_000;
const HOUR = 3_600_000;

// FNV-1a, then mulberry32: cheap deterministic rng from string parts.
function hashStr(s: string): number {
  let h = 2166136261;
  for (let i = 0; i < s.length; i++) {
    h ^= s.charCodeAt(i);
    h = Math.imul(h, 16777619);
  }
  return h >>> 0;
}

function mulberry32(a: number): () => number {
  return function () {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

function seeded(...parts: Array<string | number>): () => number {
  return mulberry32(hashStr(parts.join('|')));
}

function hexId(rand: () => number, len: number): string {
  let out = '';
  for (let i = 0; i < len; i++) out += Math.floor(rand() * 16).toString(16);
  return out;
}

function clamp(v: number, min: number, max: number): number {
  return Math.min(max, Math.max(min, v));
}

// Smooth pseudo-metric: slow sine drift + seeded per-bucket jitter.
function wave(seedKey: string, bucket: number, base: number, amp: number): number {
  const jitter = seeded(seedKey, bucket)() - 0.5;
  const phase = hashStr(seedKey) % 7;
  return base + amp * Math.sin(bucket / 9 + phase) + amp * 0.8 * jitter;
}

// Iterate time buckets over [startMs, endMs], capped so wide ranges stay cheap.
function eachBucket(
  startMs: number,
  endMs: number,
  maxBuckets: number,
  fn: (bucketStart: number, stepMs: number, minuteIndex: number) => void,
): void {
  const span = Math.max(endMs - startMs, MINUTE);
  const step = Math.max(MINUTE, Math.ceil(span / maxBuckets / MINUTE) * MINUTE);
  for (let t = Math.floor(startMs / step) * step; t < endMs; t += step) {
    fn(Math.max(t, startMs), step, Math.floor(t / MINUTE));
  }
}

// The end is clamped to now: presets like "today" or "this month" end at
// midnight, and nothing may fabricate telemetry for hours that have not happened.
function parseRange(start?: string, end?: string): { startMs: number; endMs: number } {
  const now = Date.now();
  const endMs = Math.min(end ? new Date(end).getTime() : now, now);
  const startMs = start ? new Date(start).getTime() : endMs - HOUR;
  if (!Number.isFinite(startMs) || !Number.isFinite(endMs) || startMs >= endMs) {
    return { startMs: now - HOUR, endMs: now };
  }
  return { startMs, endMs };
}

// ---------------------------------------------------------------------------
// Fictional estate: NovaShop, an e-commerce platform
// ---------------------------------------------------------------------------

const CREATED_AT = '2026-01-05T09:00:00Z';

// The single incident timeline, in "ms ago". Check results, error volume,
// incidents, events, correlation and explanations all derive from it, so no two
// views can tell a different story. Each incident lags the signal that caused
// it by the rule window / max_attempts the product actually waits for.
const CPU_SATURATION_AGO = 55 * MINUTE;
const CPU_INCIDENT_AGO = 50 * MINUTE; // rule window: 300s
const BACKOFFICE_DOWN_AGO = 55 * MINUTE;
const BACKOFFICE_INCIDENT_AGO = 52 * MINUTE; // 3 attempts x 60s
const ERROR_SPIKE_AGO = 45 * MINUTE;
const ERROR_INCIDENT_AGO = 35 * MINUTE; // rule window: 600s
const WEBHOOK_DEGRADED_AGO = 40 * MINUTE;
const WEBHOOK_INCIDENT_AGO = 34 * MINUTE; // 3 attempts x 120s
const NETWORK_DEGRADED_AGO = 50 * MINUTE;
const NETWORK_INCIDENT_AGO = 47 * MINUTE; // 3 attempts x 60s
// Already closed, kept as history.
const GATEWAY_DEGRADED_FROM_AGO = 26 * HOUR;
const GATEWAY_DEGRADED_TO_AGO = 25 * HOUR;
const BACKOFFICE_PAST_AGO = 3 * 24 * HOUR;

// Renewed 6h ago (see the TLS event), 90-day certificate.
const CERT_EXPIRES_AGO = -(90 * 24 * HOUR - 6 * HOUR);

const DEMO_HOSTS = [
  { id: 1, name: 'web-01', apps: ['api-gateway', 'checkout-service'] },
  { id: 2, name: 'web-02', apps: ['auth-service', 'catalog-service'] },
  { id: 3, name: 'worker-01', apps: ['payment-service'] },
  { id: 4, name: 'db-01', apps: [] },
];

const APP_SERVICES = ['api-gateway', 'checkout-service', 'auth-service', 'payment-service', 'catalog-service'];

function hostOf(app: string): string {
  return DEMO_HOSTS.find((h) => h.apps.includes(app))?.name ?? 'web-01';
}

// Monitored checks (uptime/agent side of the product). latencyMs is the nominal
// value the check reports when healthy; a degradation window overrides it, which
// is what turns the check warning/failing — no check is permanently breaching.
interface Degradation {
  sinceAgo: number;
  untilAgo?: number; // omitted: still ongoing
  latencyMs?: number; // elevated value while degraded
  down?: boolean; // hard failure: nothing measurable
  message?: string;
}

interface CheckDef {
  id: number;
  name: string;
  type: string;
  host: string;
  hostId?: number;
  latencyMs: number;
  interval: number;
  degraded?: Degradation;
}

const CHECKS: CheckDef[] = [
  { id: 1, name: 'web-01 cpu', type: 'agent_cpu', host: 'web-01', hostId: 1, latencyMs: 0, interval: 60 },
  { id: 2, name: 'web-02 cpu', type: 'agent_cpu', host: 'web-02', hostId: 2, latencyMs: 0, interval: 60 },
  { id: 3, name: 'worker-01 cpu', type: 'agent_cpu', host: 'worker-01', hostId: 3, latencyMs: 0, interval: 60 },
  { id: 4, name: 'db-01 cpu', type: 'agent_cpu', host: 'db-01', hostId: 4, latencyMs: 0, interval: 60 },

  { id: 13, name: 'web-01 ram', type: 'agent_ram', host: 'web-01', hostId: 1, latencyMs: 0, interval: 60 },
  { id: 14, name: 'web-02 ram', type: 'agent_ram', host: 'web-02', hostId: 2, latencyMs: 0, interval: 60 },
  { id: 15, name: 'worker-01 ram', type: 'agent_ram', host: 'worker-01', hostId: 3, latencyMs: 0, interval: 60 },
  { id: 16, name: 'db-01 ram', type: 'agent_ram', host: 'db-01', hostId: 4, latencyMs: 0, interval: 60 },

  { id: 17, name: 'web-01 disk', type: 'agent_disk', host: 'web-01', hostId: 1, latencyMs: 0, interval: 60 },
  { id: 18, name: 'web-02 disk', type: 'agent_disk', host: 'web-02', hostId: 2, latencyMs: 0, interval: 60 },
  { id: 19, name: 'worker-01 disk', type: 'agent_disk', host: 'worker-01', hostId: 3, latencyMs: 0, interval: 60 },
  { id: 20, name: 'db-01 disk', type: 'agent_disk', host: 'db-01', hostId: 4, latencyMs: 0, interval: 60 },

  // latencyMs is the ping latency the network thresholds apply to (metric_value is throughput).
  { id: 21, name: 'web-01 network', type: 'agent_network', host: 'web-01', hostId: 1, latencyMs: 14, interval: 60 },
  {
    id: 22, name: 'db-01 network', type: 'agent_network', host: 'db-01', hostId: 4, latencyMs: 22, interval: 60,
    degraded: { sinceAgo: NETWORK_DEGRADED_AGO, latencyMs: 181 },
  },

  {
    id: 5, name: 'API health', type: 'http', host: 'https://api.novashop.example/health', hostId: 1, latencyMs: 84, interval: 60,
    degraded: { sinceAgo: GATEWAY_DEGRADED_FROM_AGO, untilAgo: GATEWAY_DEGRADED_TO_AGO, latencyMs: 800 },
  },
  { id: 6, name: 'Storefront', type: 'http', host: 'https://www.novashop.example', hostId: 2, latencyMs: 132, interval: 60 },
  {
    id: 7, name: 'Payment webhook', type: 'http', host: 'https://hooks.novashop.example/stripe', hostId: 3, latencyMs: 180, interval: 120,
    degraded: { sinceAgo: WEBHOOK_DEGRADED_AGO, latencyMs: 462 },
  },
  { id: 8, name: 'Orders database', type: 'sql', host: 'postgres://db-01:5432/orders', hostId: 4, latencyMs: 12, interval: 120 },
  { id: 9, name: 'Storefront TLS', type: 'certificate', host: 'www.novashop.example:443', latencyMs: 0, interval: 3600 },
  {
    // Same window as the db-01 network check: one degraded link, both probes
    // see it. Stays under the 100ms threshold, so it alerts only once.
    id: 10, name: 'db-01 ping', type: 'ping', host: 'db-01', hostId: 4, latencyMs: 2, interval: 60,
    degraded: { sinceAgo: NETWORK_DEGRADED_AGO, latencyMs: 61 },
  },
  {
    id: 11, name: 'Back-office', type: 'http', host: 'https://admin.novashop.example', hostId: 2, latencyMs: 115, interval: 60,
    degraded: {
      sinceAgo: BACKOFFICE_DOWN_AGO,
      down: true,
      message: 'Get "https://admin.novashop.example": dial tcp 10.0.2.21:443: connect: connection refused',
    },
  },
  { id: 12, name: 'Status page', type: 'http', host: 'https://status.novashop.example', latencyMs: 95, interval: 300 },
];

// Hourly, and attached to no host: filler must not weigh on the generated
// results or move what the hosts views count.
for (let i = 0; i < servicesPadding(); i += 1) {
  CHECKS.push({
    id: 1000 + i,
    name: `Storefront region ${i + 1}`,
    type: 'http',
    host: `https://region-${i + 1}.novashop.example`,
    latencyMs: 90,
    interval: 3600,
  });
}

// Same defaults the backend applies when a check is created
// (defaultThresholdsForType), except http which this estate tuned tighter.
// Certificates are in days, agent cpu/ram/disk in %, the rest in ms.
function thresholdsFor(type: string): { warning: number | null; critical: number | null } {
  switch (type) {
    case 'http':
      return { warning: 300, critical: 1000 };
    case 'ping':
    case 'agent_network':
      return { warning: 100, critical: 300 };
    case 'agent_cpu':
    case 'agent_ram':
    case 'agent_disk':
      return { warning: 80, critical: 95 };
    case 'certificate':
      return { warning: 30, critical: 7 };
    case 'sql':
      return { warning: null, critical: 1000 };
    default:
      return { warning: null, critical: null };
  }
}

function checkToService(c: CheckDef): Service {
  const { warning, critical } = thresholdsFor(c.type);
  return {
    id: c.id,
    host_id: c.hostId,
    name: c.name,
    display_name: null,
    type: c.type,
    host: c.host,
    service: hostNameForCheck(c),
    service_interval: c.interval,
    max_attempts: 3,
    warning_threshold: warning,
    critical_threshold: critical,
    created_at: CREATED_AT,
  };
}

function hostNameForCheck(c: CheckDef): string {
  return DEMO_HOSTS.find((h) => h.id === c.hostId)?.name ?? '';
}

// SDK error groups. Rates are per hour; payment-service spikes since
// ERROR_SPIKE_AGO to back the open critical incident. firstSeenDays is how far
// back the fingerprint exists, which is what makes an error new vs chronic.
interface ErrorDef {
  id: number;
  service: string;
  name: string;
  message: string;
  file: string;
  line: number;
  ratePerHour: number;
  firstSeenDays: number;
  http_method?: string;
  http_url?: string;
}

const ERROR_DEFS: ErrorDef[] = [
  {
    id: 1,
    service: 'payment-service',
    name: 'ConnectionTimeout',
    message: 'connection to stripe-gateway timed out after 5000ms',
    file: 'internal/payment/gateway.go',
    line: 142,
    ratePerHour: 80,
    firstSeenDays: 4,
    http_method: 'POST',
    http_url: '/api/v1/payments/charge',
  },
  {
    id: 2,
    service: 'checkout-service',
    name: 'TypeError',
    message: "Cannot read properties of undefined (reading 'id')",
    file: 'src/cart/session.ts',
    line: 87,
    ratePerHour: 26,
    firstSeenDays: 5,
    http_method: 'POST',
    http_url: '/api/v1/checkout',
  },
  {
    id: 3,
    service: 'auth-service',
    name: 'TokenExpiredError',
    message: 'jwt expired',
    file: 'src/middleware/verify.ts',
    line: 41,
    ratePerHour: 12,
    firstSeenDays: 6,
    http_method: 'POST',
    http_url: '/api/v1/auth/refresh',
  },
  {
    id: 4,
    service: 'api-gateway',
    name: 'UpstreamTimeout',
    message: 'upstream request to catalog-service exceeded the 2s budget',
    file: 'gateway/proxy.go',
    line: 77,
    ratePerHour: 8,
    firstSeenDays: 7,
    http_method: 'GET',
    http_url: '/api/v1/products',
  },
  {
    id: 5,
    service: 'catalog-service',
    name: 'DBError',
    message: 'pq: deadlock detected',
    file: 'internal/catalog/repo.go',
    line: 203,
    ratePerHour: 4,
    firstSeenDays: 8,
  },
];

// Floor plus a seeded fraction instead of rounding: a rate below one event per
// bucket still lands its events, so a low-volume error reports the same rate
// whether the window is an hour or a week.
function errorCountAt(def: ErrorDef, bucketStart: number, stepMs: number): number {
  // Nothing before the fingerprint first appeared, so charts and "first seen" agree.
  if (bucketStart < errorFirstSeen(def)) return 0;
  const rand = seeded('err', def.id, Math.floor(bucketStart / MINUTE));
  const spike = def.id === 1 && Date.now() - bucketStart < ERROR_SPIKE_AGO ? 4 : 1;
  const expected = ((def.ratePerHour * stepMs) / HOUR) * spike;
  // Wide buckets average their noise out, so the same window reports the same
  // volume whether the view asked for 24 points over an hour or over a month.
  const spread = Math.min(0.6, 1.2 / Math.sqrt(Math.max(expected, 0.25)));
  return Math.floor(expected * (1 + spread * (rand() - 0.5) * 2) + rand());
}

function errorTimeseries(def: ErrorDef, startMs: number, endMs: number): TimeSeriesPoint[] {
  const points: TimeSeriesPoint[] = [];
  eachBucket(startMs, endMs, 24, (bucketStart, stepMs) => {
    points.push({ date: iso(bucketStart), count: errorCountAt(def, bucketStart, stepMs) });
  });
  return points;
}

// Occurrences over a window ending now, from the same generator the groups and
// the sparklines read, so a count never contradicts a chart.
function errorCountOver(def: ErrorDef, spanMs: number): number {
  const now = Date.now();
  return errorTimeseries(def, now - spanMs, now).reduce((sum, p) => sum + p.count, 0);
}

function errorFirstSeen(def: ErrorDef): number {
  return Date.now() - def.firstSeenDays * 24 * HOUR;
}

function errorLastSeen(def: ErrorDef): number {
  return Date.now() - def.id * 2 * MINUTE;
}

// The backend's sample is the group's latest occurrence, so its id moves every
// time a newer one lands (GetErrorsGroupedForOrg). The demo has no occurrence
// rows, so that occurrence's clock stands in for its id.
function errorSampleId(def: ErrorDef): number {
  return errorLastSeen(def);
}

function errorSample(def: ErrorDef): ApplicationError {
  return {
    id: def.id,
    name: def.name,
    message: def.message,
    file: def.file,
    line: def.line,
    // The sample IS the latest occurrence of the group, so both agree.
    timestamp: iso(errorLastSeen(def)),
    service: def.service,
    http_method: def.http_method,
    http_url: def.http_url,
  };
}

function errorGroups(startMs: number, endMs: number, service?: string): ErrorGroup[] {
  return ERROR_DEFS.filter((d) => !service || d.service === service)
    .map((def) => {
      const timeseries = errorTimeseries(def, startMs, endMs);
      const event_count = timeseries.reduce((sum, p) => sum + p.count, 0);
      return {
        event_count,
        first_seen: iso(errorFirstSeen(def)),
        last_seen: iso(errorLastSeen(def)),
        sample_id: errorSampleId(def),
        sample: errorSample(def),
        timeseries,
      };
    })
    .filter((g) => g.event_count > 0)
    .sort((a, b) => b.event_count - a.event_count);
}

// ---------------------------------------------------------------------------
// Logs & traces
// ---------------------------------------------------------------------------

const LOG_TEMPLATES: Record<string, Array<{ sev: string; body: string }>> = {
  'api-gateway': [
    { sev: 'INFO', body: 'Incoming request GET /api/v1/products completed in 42ms' },
    { sev: 'INFO', body: 'Incoming request POST /api/v1/checkout completed in 187ms' },
    { sev: 'DEBUG', body: 'Route matched: /api/v1/cart -> checkout-service' },
    { sev: 'WARN', body: 'Rate limit at 80% for client 10.0.4.18' },
    { sev: 'ERROR', body: 'upstream request to catalog-service exceeded the 2s budget' },
  ],
  'checkout-service': [
    { sev: 'INFO', body: 'Cart validated for session, 3 items, total 89.90 EUR' },
    { sev: 'INFO', body: 'Order draft created, forwarding to payment-service' },
    { sev: 'DEBUG', body: 'Session cache hit for cart lookup' },
    { sev: 'WARN', body: 'Inventory low for sku NS-4482 (2 left)' },
    { sev: 'ERROR', body: "TypeError: Cannot read properties of undefined (reading 'id')" },
  ],
  'auth-service': [
    { sev: 'INFO', body: 'JWT issued for user, scope: customer' },
    { sev: 'DEBUG', body: 'Refresh token rotated' },
    { sev: 'INFO', body: 'Login succeeded from 92.184.0.0/16' },
    { sev: 'WARN', body: 'Repeated login failures for account, backing off 30s' },
    { sev: 'ERROR', body: 'TokenExpiredError: jwt expired' },
  ],
  'payment-service': [
    { sev: 'INFO', body: 'Charge captured: 89.90 EUR via stripe-gateway' },
    { sev: 'DEBUG', body: 'Idempotency key accepted, no prior attempt' },
    { sev: 'WARN', body: 'stripe-gateway latency high (2400ms)' },
    { sev: 'ERROR', body: 'connection to stripe-gateway timed out after 5000ms' },
    { sev: 'ERROR', body: 'charge aborted, order rolled back' },
  ],
  'catalog-service': [
    { sev: 'INFO', body: 'Product list served: 24 items, category=sneakers' },
    { sev: 'DEBUG', body: 'Cache warm-up finished, 1832 products indexed' },
    { sev: 'INFO', body: 'Search query "running shoes" returned 61 hits in 38ms' },
    { sev: 'WARN', body: 'Slow query: SELECT on products took 890ms' },
    { sev: 'ERROR', body: 'pq: deadlock detected' },
  ],
};

// payment-service leans on its ERROR templates while the incident is hot.
function pickTemplate(service: string, rand: () => number, bucketStart: number): { sev: string; body: string } {
  const pool = LOG_TEMPLATES[service];
  const hot = service === 'payment-service' && Date.now() - bucketStart < ERROR_SPIKE_AGO;
  const r = rand();
  if (hot && r < 0.4) return pool[3 + Math.floor(rand() * 2)];
  if (r < 0.55) return pool[0];
  if (r < 0.75) return pool[Math.floor(rand() * 2) + 1];
  if (r < 0.92) return pool[3];
  return pool[4];
}

// Logs and traces have to share ids: reading a span id off a log detail and
// searching it in the trace view is the correlation the product is sold on, and
// two independent generators made it impossible.
function spanIdsFor(service: string, minuteIndex: number, index: number): { trace_id: string; span_id: string } {
  const rand = seeded('span', service, minuteIndex, index);
  return { trace_id: hexId(rand, 32), span_id: hexId(rand, 16) };
}

// How many spans genTraces emits for a bucket, so a log can point at one.
function traceCountFor(service: string, minuteIndex: number): number {
  return Math.floor(seeded('trace', service, minuteIndex)() * 3);
}

function genLogs(startMs: number, endMs: number): LogEntry[] {
  const entries: LogEntry[] = [];
  eachBucket(startMs, endMs, 160, (bucketStart, stepMs, minuteIndex) => {
    for (const service of APP_SERVICES) {
      const rand = seeded('log', service, minuteIndex);
      const count = Math.floor(rand() * 3);
      for (let i = 0; i < count; i++) {
        const ts = bucketStart + Math.floor(rand() * stepMs);
        if (ts > endMs) continue;
        const tpl = pickTemplate(service, rand, bucketStart);
        const traceCount = traceCountFor(service, minuteIndex);
        const withTrace = traceCount > 0 && rand() < 0.6;
        const link = withTrace
          ? spanIdsFor(service, minuteIndex, Math.floor(rand() * traceCount))
          : undefined;
        entries.push({
          _id: hexId(rand, 20),
          body: tpl.body,
          severity_text: tpl.sev,
          severity: tpl.sev,
          service_name: service,
          hostname: hostOf(service),
          trace_id: link?.trace_id,
          span_id: link?.span_id,
          '@timestamp': iso(ts),
        });
      }
    }
  });
  return entries.sort((a, b) => (a['@timestamp'] < b['@timestamp'] ? 1 : -1));
}

const TRACE_OPS: Record<string, Array<{ op: string; kind: string; baseMs: number }>> = {
  'api-gateway': [
    { op: 'GET /api/v1/products', kind: 'SPAN_KIND_SERVER', baseMs: 45 },
    { op: 'POST /api/v1/checkout', kind: 'SPAN_KIND_SERVER', baseMs: 190 },
    { op: 'GET /api/v1/cart', kind: 'SPAN_KIND_SERVER', baseMs: 30 },
  ],
  'checkout-service': [
    { op: 'POST /internal/checkout', kind: 'SPAN_KIND_SERVER', baseMs: 120 },
    { op: 'payment-service.Charge', kind: 'SPAN_KIND_CLIENT', baseMs: 340 },
  ],
  'auth-service': [
    { op: 'POST /internal/token/verify', kind: 'SPAN_KIND_SERVER', baseMs: 8 },
  ],
  'payment-service': [
    { op: 'POST /internal/charge', kind: 'SPAN_KIND_SERVER', baseMs: 380 },
    { op: 'stripe-gateway.CreateCharge', kind: 'SPAN_KIND_CLIENT', baseMs: 320 },
  ],
  'catalog-service': [
    { op: 'SELECT products', kind: 'SPAN_KIND_CLIENT', baseMs: 18 },
    { op: 'GET /internal/products', kind: 'SPAN_KIND_SERVER', baseMs: 35 },
  ],
};

function genTraces(startMs: number, endMs: number, service?: string): TraceSpan[] {
  const spans: TraceSpan[] = [];
  const services = service ? APP_SERVICES.filter((s) => s === service) : APP_SERVICES;
  eachBucket(startMs, endMs, 160, (bucketStart, stepMs, minuteIndex) => {
    for (const svc of services) {
      const rand = seeded('trace', svc, minuteIndex);
      const count = Math.floor(rand() * 3);
      for (let i = 0; i < count; i++) {
        const ts = bucketStart + Math.floor(rand() * stepMs);
        if (ts > endMs) continue;
        const ops = TRACE_OPS[svc];
        const def = ops[Math.floor(rand() * ops.length)];
        const hot = svc === 'payment-service' && Date.now() - bucketStart < ERROR_SPIKE_AGO;
        const failed = rand() < (hot ? 0.3 : 0.04);
        // A failed charge sits on the SDK's 5s client timeout, the same number
        // the ConnectionTimeout message reports.
        const duration =
          failed && svc === 'payment-service'
            ? 5000 * (0.98 + rand() * 0.04)
            : def.baseMs * (0.5 + rand() * 1.6) * (failed ? 6 : 1);
        const ids = spanIdsFor(svc, minuteIndex, i);
        spans.push({
          _id: hexId(rand, 20),
          trace_id: ids.trace_id,
          span_id: ids.span_id,
          parent_span_id: def.kind === 'SPAN_KIND_SERVER' ? undefined : hexId(rand, 16),
          service_name: svc,
          operation_name: def.op,
          span_kind: def.kind,
          status_code: failed ? 'ERROR' : 'OK',
          status_message: failed ? 'deadline exceeded' : undefined,
          start_time: iso(ts),
          end_time: iso(ts + duration),
          duration_ms: Math.round(duration * 10) / 10,
          hostname: hostOf(svc),
          '@timestamp': iso(ts),
          attributes: { 'http.status_code': failed ? 504 : 200, 'peer.hostname': hostOf(svc) },
        });
      }
    }
  });
  return spans.sort((a, b) => (a['@timestamp'] < b['@timestamp'] ? 1 : -1));
}

// ---------------------------------------------------------------------------
// Metrics, checks, hosts
// ---------------------------------------------------------------------------

// worker-01 saturates at CPU_SATURATION_AGO and stays there: the CPU incident,
// the error spike and the webhook slowdown all hang off it. The saturated band
// stays inside (90, 95) so the cpu check reads warning — never critical — and
// the error correlation (which needs >90) always has its signal.
function cpuValue(host: string, minuteIndex: number): number {
  if (host === 'worker-01' && Date.now() - minuteIndex * MINUTE < CPU_SATURATION_AGO) {
    return clamp(wave(`cpu-hot|${host}`, minuteIndex, 92.5, 1.5), 2, 99);
  }
  return clamp(wave(`cpu|${host}`, minuteIndex, host === 'db-01' ? 35 : 45, 12), 2, 99);
}

function ramValue(host: string, minuteIndex: number): number {
  return clamp(wave(`ram|${host}`, minuteIndex, 62, 8), 10, 95);
}

function diskValue(host: string, minuteIndex: number): number {
  return clamp(wave(`disk|${host}`, minuteIndex, 57, 2), 10, 95);
}

// payment-service latency follows the same slowdown as the Payment webhook check.
function appLatency(service: string, minuteIndex: number): number {
  const hot = service === 'payment-service' && Date.now() - minuteIndex * MINUTE < WEBHOOK_DEGRADED_AGO;
  return clamp(wave(`lat|${service}`, minuteIndex, hot ? 520 : 140, 60), 5, 4000);
}

// Custom metrics NovaShop's applications export over OTLP, with the labels a
// Prometheus-style exporter would carry.
// host is the demo host each metric is reported from, so the scope selector has
// something real to narrow.
const DEMO_METRICS: Record<
  string,
  { labels: Record<string, string[]>; base: number; amp: number; unit: string; host: string }
> = {
  http_requests_total: {
    host: 'web-01',
    labels: {
      route: ['/checkout', '/cart', '/api/v1/products', '/login', '/search'],
      method: ['GET', 'POST'],
      status: ['200', '404', '500'],
    },
    base: 420,
    amp: 90,
    unit: '1',
  },
  http_server_duration_ms: {
    host: 'web-02',
    labels: {
      route: ['/checkout', '/cart', '/api/v1/products', '/login', '/search'],
      method: ['GET', 'POST'],
    },
    base: 145,
    amp: 55,
    unit: 'ms',
  },
  checkout_queue_depth: {
    host: 'worker-01',
    // 'refunds' is a real label value (the queue exists) that has carried no
    // traffic yet — the one reachable way to demonstrate the empty-query
    // state in the demo, matching what min_doc_count buckets look like when
    // a metric is scoped to a slice with no data: buckets exist, values don't.
    labels: { queue: ['orders', 'payments', 'invoices', 'refunds'] },
    base: 12,
    amp: 8,
    unit: '1',
  },
  db_connections_active: {
    host: 'db-01',
    labels: { pool: ['primary', 'replica'] },
    base: 34,
    amp: 11,
    unit: '1',
  },
};

// Percentiles read higher than the mean, count is a different order of
// magnitude: a chart must not show the same curve whatever the statistic.
const AGGREGATION_SCALE: Record<string, number> = {
  min: 0.6,
  avg: 1,
  max: 1.6,
  sum: 12,
  count: 8,
  p50: 0.95,
  p75: 1.15,
  p90: 1.3,
  p95: 1.45,
  p99: 1.8,
};

// Label values that exist in the schema but carry no points — the one
// reachable way, in demo mode, to show the "no data for this query" state.
const QUIET_LABEL_VALUES = new Set(['refunds']);

// Demo hosts are 1..4 in the order below; a group holds the two web hosts.
const DEMO_HOST_NAMES = ['web-01', 'web-02', 'worker-01', 'db-01'];
const DEMO_GROUP_HOSTS: Record<number, string[]> = { 1: ['web-01', 'web-02'] };

function demoScopedMetrics(params?: { host_id?: number; host_group_id?: number }): string[] {
  const names = Object.keys(DEMO_METRICS);
  if (!params) return names;
  if (params.host_id) {
    const host = DEMO_HOST_NAMES[params.host_id - 1];
    return names.filter((name) => DEMO_METRICS[name].host === host);
  }
  if (params.host_group_id) {
    const hosts = DEMO_GROUP_HOSTS[params.host_group_id] ?? [];
    return names.filter((name) => hosts.includes(DEMO_METRICS[name].host));
  }
  return names;
}

function demoLabelKeys(metric?: string): string[] {
  const definitions = metric && DEMO_METRICS[metric] ? [DEMO_METRICS[metric]] : Object.values(DEMO_METRICS);
  const keys = new Set<string>();
  for (const definition of definitions) {
    for (const key of Object.keys(definition.labels)) keys.add(key);
  }
  return [...keys].sort();
}

function demoLabelValues(key: string, metric?: string): string[] {
  const definitions = metric && DEMO_METRICS[metric] ? [DEMO_METRICS[metric]] : Object.values(DEMO_METRICS);
  const values = new Set<string>();
  for (const definition of definitions) {
    for (const value of definition.labels[key] ?? []) values.add(value);
  }
  return [...values].sort();
}

// sum() collapses the series dimension, which a pointwise pass can only do on the
// end result: nested in a wider expression it is refused, not approximated.
function sumSpansWholeExpression(tokens: string[]): boolean {
  if (tokens.filter((t) => t === 'sum').length !== 1) return false;
  if (tokens[0] !== 'sum' || tokens[1] !== '(') return false;
  let depth = 0;
  for (let i = 1; i < tokens.length; i++) {
    if (tokens[i] === '(') depth++;
    else if (tokens[i] === ')' && --depth === 0) return i === tokens.length - 1;
  }
  return false;
}

// Pointwise stand-in for the backend evaluator: pairs series by labels, broadcasts
// a lone one, and collapses everything when the expression is a lone sum().
function demoEvaluateExpression(expression: string, env: Record<string, MetricSeries[]>): MetricSeries[] | null {
  const tokens: string[] = expression.match(/\$[A-Z]|\d*\.?\d+|[a-z]+|[-+*/()]/g) ?? [];
  if (tokens.join('') !== expression.replace(/\s+/g, '')) return null;
  const collapses = sumSpansWholeExpression(tokens);
  if (tokens.includes('sum') && !collapses) return null;
  let pos = 0;
  type Eval = (lookup: (ref: string) => number | null) => number | null;
  const binary = (l: Eval, op: string, r: Eval): Eval => (lookup) => {
    const a = l(lookup);
    const b = r(lookup);
    if (a == null || b == null || (op === '/' && b === 0)) return null;
    return op === '+' ? a + b : op === '-' ? a - b : op === '*' ? a * b : a / b;
  };
  const primary = (): Eval | null => {
    const tok = tokens[pos++];
    if (!tok) return null;
    if (tok.startsWith('$')) return env[tok.slice(1)] ? (lookup) => lookup(tok.slice(1)) : null;
    if (tok === '-') {
      const inner = primary();
      return inner && ((lookup) => { const v = inner(lookup); return v == null ? null : -v; });
    }
    if (tok === '(' || tok === 'abs' || tok === 'sum') {
      if (tok !== '(' && tokens[pos++] !== '(') return null;
      const inner = sum();
      if (!inner || tokens[pos++] !== ')') return null;
      return tok === 'abs' ? (lookup) => { const v = inner(lookup); return v == null ? null : Math.abs(v); } : inner;
    }
    const n = Number(tok);
    return Number.isNaN(n) ? null : () => n;
  };
  const chain = (next: () => Eval | null, ops: string): Eval | null => {
    let left = next();
    while (left && ops.includes(tokens[pos] ?? '_')) {
      const op = tokens[pos++];
      const right = next();
      if (!right) return null;
      left = binary(left, op, right);
    }
    return left;
  };
  const product = () => chain(primary, '*/');
  const sum = (): Eval | null => chain(product, '+-');
  const root = sum();
  if (!root || pos !== tokens.length) return null;

  const key = (labels: Record<string, string>) => JSON.stringify(Object.entries(labels).sort());
  // Only the queries the expression names shape the result.
  const used = Object.keys(env).filter((ref) => expression.includes(`$${ref}`)).map((ref) => env[ref]);
  const shape = used.reduce<MetricSeries[]>((a, b) => (b.length > a.length ? b : a), []);
  // As on the backend: a lone series stands for all only when its labels hold on
  // the row; otherwise it pairs by labels, and a row with no partner is dropped.
  const partner = (series: MetricSeries[], row: MetricSeries) =>
    series.find((s) => key(s.labels) === key(row.labels)) ??
    (series.length === 1 && Object.entries(series[0].labels).every(([k, v]) => row.labels[k] === v)
      ? series[0]
      : undefined);
  const rows = shape
    .filter((row) => used.every((series) => partner(series, row)))
    .map((row) => ({
      ...row,
      query: undefined,
      metric_name: expression,
      points: row.points.map((point, i) => {
        const value = root((ref) => partner(env[ref], row)?.points[i]?.value ?? null);
        return { ...point, value: value != null && Number.isFinite(value) ? value : null };
      }),
    }));
  if (!collapses || rows.length < 2) return rows;
  return [{
    ...rows[0],
    labels: {},
    points: rows[0].points.map((point, i) => {
      const values = rows.map((r) => r.points[i].value).filter((v): v is number => v != null);
      return { ...point, value: values.length ? values.reduce((a, b) => a + b, 0) : null };
    }),
  }];
}

function genMetricSeries(
  startMs: number,
  endMs: number,
  metric: string,
  filters: string[],
  groupBy: string | undefined,
  aggregation: string,
): MetricSeries[] {
  const definition = DEMO_METRICS[metric];
  if (!definition) return [];

  const constraints = new Map<string, string>();
  for (const raw of filters) {
    const [key, ...rest] = raw.split(':');
    if (key && rest.length) constraints.set(key, rest.join(':'));
  }
  // A filter on a value the metric never carries yields nothing, like the real query.
  for (const [key, value] of constraints) {
    if (!(definition.labels[key] ?? []).includes(value)) return [];
  }

  let groups: Array<Record<string, string>> = [{}];
  if (groupBy) {
    const values = constraints.has(groupBy)
      ? [constraints.get(groupBy) as string]
      : (definition.labels[groupBy] ?? []);
    if (!values.length) return [];
    groups = values.map((value) => ({ [groupBy]: value }));
  }

  const scale = AGGREGATION_SCALE[aggregation] ?? 1;
  return groups.map((labels) => {
    // A label value can exist without ever having carried a point — real
    // OpenSearch buckets look the same: present, timestamped, valued null.
    // Checked on both the group-by value and a plain filter constraint: a
    // constraint alone (no group by) never lands in `labels`.
    const isQuiet =
      Object.values(labels).some((v) => QUIET_LABEL_VALUES.has(v)) ||
      [...constraints.values()].some((v) => QUIET_LABEL_VALUES.has(v));
    const seedKey = [metric, ...Object.entries(labels).flat(), ...constraints.keys()].join('|');
    const points: MetricSeries['points'] = [];
    eachBucket(startMs, endMs, 150, (bucketStart, _stepMs, minuteIndex) => {
      if (isQuiet) {
        points.push({ timestamp: iso(bucketStart), value: null });
        return;
      }
      const value = wave(seedKey, minuteIndex, definition.base, definition.amp) * scale;
      points.push({ timestamp: iso(bucketStart), value: Math.max(0, Math.round(value * 100) / 100) });
    });
    return { metric_name: metric, labels, points };
  });
}

function genSystemMetrics(startMs: number, endMs: number, service?: string, host?: string): SystemMetric[] {
  const metrics: SystemMetric[] = [];
  let id = 1;
  const pairs = APP_SERVICES.map((s) => ({ service: s, host: hostOf(s) })).filter(
    (p) => (!service || p.service === service) && (!host || p.host === host),
  );
  eachBucket(startMs, endMs, 150, (bucketStart, _stepMs, minuteIndex) => {
    for (const pair of pairs) {
      const hasLatency = pair.service !== 'catalog-service' && pair.service !== 'auth-service';
      metrics.push({
        id: id++,
        service: pair.service,
        host: pair.host,
        cpu_perc: cpuValue(pair.host, minuteIndex),
        ram_perc: ramValue(pair.host, minuteIndex),
        http_latency: hasLatency ? appLatency(pair.service, minuteIndex) : undefined,
        endpoint: hasLatency ? TRACE_OPS[pair.service][0].op : undefined,
        timestamp: iso(bucketStart),
      });
    }
  });
  return metrics;
}

// The degradation covering a timestamp, if any.
function degradationAt(check: CheckDef, t: number): Degradation | undefined {
  const d = check.degraded;
  if (!d) return undefined;
  const ago = Date.now() - t;
  return ago <= d.sinceAgo && ago >= (d.untilAgo ?? 0) ? d : undefined;
}

function round1(v: number): number {
  return Math.round(v * 10) / 10;
}

// Metadata the agent actually ships per metric type (see AgentService.StoreMetrics),
// which is what the service detail page reads for its load / total / free tiles.
function agentMetadata(metric: string, host: string, minuteIndex: number, value: number): string {
  if (metric === 'cpu') {
    // 4 cores: load tracks utilization instead of drifting on its own.
    const load = (value / 100) * 4;
    return JSON.stringify({
      load_1min: round2(load * (0.9 + 0.2 * seeded('load', host, minuteIndex)())),
      load_5min: round2(load * 0.95),
      load_15min: round2(load * 0.9),
    });
  }
  if (metric === 'ram') return JSON.stringify({ ram_total_gb: 16 });
  return JSON.stringify({ disk_total_gb: 200 });
}

function round2(v: number): number {
  return Math.round(v * 100) / 100;
}

// Results are produced at the check's own interval and capped like the backend
// (ORDER BY timestamp DESC LIMIT 500), so the number of checks a view reports
// matches the interval it advertises. Newest first, same as the API.
const MAX_CHECK_RESULTS = 500;

function genCheckResults(check: CheckDef, startMs: number, endMs: number): ServiceResult[] {
  const step = check.interval * 1000;
  const oldest = Math.max(startMs, endMs - (MAX_CHECK_RESULTS - 1) * step);
  const { warning, critical } = thresholdsFor(check.type);
  const host = hostNameForCheck(check);
  const results: ServiceResult[] = [];

  for (let t = Math.floor(endMs / step) * step; t >= oldest; t -= step) {
    const minuteIndex = Math.floor(t / MINUTE);
    const rand = seeded('check', check.id, minuteIndex);
    // Stable per timestamp, and the check id stays recoverable from it, so RCA
    // and explain deep links keep pointing at the same result.
    const id = check.id * 100_000 + (Math.floor(t / step) % 100_000);

    if (AGENT_PERCENT_TYPES.includes(check.type)) {
      const metric = check.type.slice('agent_'.length);
      const value =
        metric === 'cpu' ? cpuValue(host, minuteIndex)
          : metric === 'ram' ? ramValue(host, minuteIndex)
            : diskValue(host, minuteIndex);
      const label = { cpu: 'CPU', ram: 'RAM', disk: 'Disk' }[metric];
      const breached = critical != null && value > critical ? critical : warning != null && value > warning ? warning : null;
      results.push({
        id,
        service_id: check.id,
        status: critical != null && value > critical ? 'failure' : breached != null ? 'warning' : 'success',
        message: breached != null
          ? `${label} usage is ${value.toFixed(2)}% (${breached === critical ? 'critical' : 'warning'} threshold ${breached}%)`
          : undefined,
        timestamp: iso(t),
        metric_type: metric,
        metric_value: round1(value),
        metadata: agentMetadata(metric, host, minuteIndex, value),
      });
      continue;
    }

    const deg = degradationAt(check, t);

    if (check.type === 'agent_network') {
      // Ping latency drives the status; metric_value stays throughput (MB/s).
      const latency = (deg?.latencyMs ?? check.latencyMs) * (0.85 + rand() * 0.3);
      const uptimeSec = (t - Date.parse(CREATED_AT)) / 1000;
      results.push({
        id,
        service_id: check.id,
        status: critical != null && latency > critical ? 'failure' : warning != null && latency > warning ? 'warning' : 'success',
        message: warning != null && latency > warning
          ? `Network ping latency is ${round1(latency)}ms (${critical != null && latency > critical ? 'critical' : 'warning'} threshold ${critical != null && latency > critical ? critical : warning}ms)`
          : undefined,
        latency: Math.round(latency),
        timestamp: iso(t),
        metric_type: 'network',
        metric_value: round2(wave(`net|${check.id}`, minuteIndex, 0.6, 0.4)),
        metadata: JSON.stringify({
          network_ping_latency_ms: round1(latency),
          network_ping_success: 1,
          network_speed_in_mb_per_s: round2(wave(`netin|${check.id}`, minuteIndex, 0.4, 0.2)),
          network_speed_out_mb_per_s: round2(wave(`netout|${check.id}`, minuteIndex, 0.2, 0.1)),
          network_bytes_in_total: Math.round(uptimeSec * 400_000),
          network_bytes_out_total: Math.round(uptimeSec * 200_000),
        }),
      });
      continue;
    }

    if (check.type === 'certificate') {
      // The worker stores the expiry date only: no latency, nothing to plot.
      results.push({
        id,
        service_id: check.id,
        status: 'success',
        timestamp: iso(t),
        metadata: JSON.stringify({ expires_at: iso(Date.now() - CERT_EXPIRES_AGO) }),
      });
      continue;
    }

    if (deg?.down) {
      results.push({
        id,
        service_id: check.id,
        status: 'failure',
        message: deg.message,
        timestamp: iso(t),
      });
      continue;
    }

    // Same status/message the worker derives from the check thresholds.
    const latency = Math.round((deg?.latencyMs ?? check.latencyMs) * (0.85 + rand() * 0.3));
    const overCritical = critical != null && latency > critical;
    const overWarning = warning != null && latency > warning;
    const label = check.type === 'ping' ? 'Ping' : check.type === 'sql' ? 'SQL' : 'HTTP';
    results.push({
      id,
      service_id: check.id,
      status: overCritical ? 'failure' : overWarning ? 'warning' : 'success',
      latency,
      message: overCritical
        ? `${label} latency (${latency}ms) exceeded critical threshold (${critical}ms)`
        : overWarning
          ? `${label} latency (${latency}ms) exceeded warning threshold (${warning}ms)`
          : undefined,
      timestamp: iso(t),
      metadata: check.type === 'http' ? JSON.stringify(httpPhases(check, latency, minuteIndex)) : undefined,
    });
  }
  return results;
}

// Connection phases as the worker reports them: pooled most of the time, cold
// again once the keep-alive lapses. A redirect lands on another host, so that
// check keeps paying for a fresh handshake on every run.
function httpPhases(check: CheckDef, latency: number, minuteIndex: number) {
  const redirects = check.host === 'https://www.novashop.example' ? 1 : 0;
  const cold = redirects > 0 || minuteIndex % 10 === 0;
  const dns = cold ? Math.round(latency * 0.14 * 10) / 10 : 0;
  const connect = cold ? Math.round(latency * 0.18 * 10) / 10 : 0;
  const tls = cold ? Math.round(latency * 0.3 * 10) / 10 : 0;
  return {
    http_dns_ms: dns,
    http_connect_ms: connect,
    http_tls_ms: tls,
    http_server_ms: Math.round((latency - dns - connect - tls) * 10) / 10,
    http_redirects: redirects,
    ...(redirects > 0 ? { http_final_url: check.host + '/home' } : {}),
  };
}

function checkWithResults(check: CheckDef, startMs: number, endMs: number): ServiceWithResults {
  return {
    ...checkToService(check),
    results: genCheckResults(check, startMs, endMs),
    host_name: hostNameForCheck(check) || undefined,
  };
}

// Current status of a check, from its latest result — the worker already
// retried it max_attempts times before recording it.
function checkStatusNow(check: CheckDef): ReturnType<typeof getServiceStatus> {
  const now = Date.now();
  return demoServiceStatus(checkWithResults(check, now - 3 * check.interval * 1000, now));
}

function demoHosts(): Host[] {
  return DEMO_HOSTS.map((h) => {
    const checks = CHECKS.filter((c) => c.hostId === h.id);
    const statuses = checks.map(checkStatusNow);
    const failing = statuses.filter((s) => s === 'failing').length;
    const warning = statuses.filter((s) => s === 'warning').length;
    return {
      id: h.id,
      name: h.name,
      display_name: null,
      host: h.name,
      service: h.name,
      host_group_id: 1,
      created_at: CREATED_AT,
      status: failing > 0 ? 'failing' : warning > 0 ? 'warning' : 'healthy',
      service_count: checks.length,
      healthy_count: checks.length - failing - warning,
      failing_count: failing,
      warning_count: warning,
      critical_count: 0,
    };
  });
}

// Mirrors the frontend getHostStatus so demo filtering/facets match the UI.
function demoHostStatus(h: Host): 'healthy' | 'failing' | 'warning' | 'unknown' {
  const fail = (h.failing_count ?? 0) + (h.critical_count ?? 0);
  const warn = h.warning_count ?? 0;
  const healthy = h.healthy_count ?? 0;
  const total = h.service_count ?? 0;
  if (total > 0) {
    if (fail > 0) return 'failing';
    if (warn > 0) return 'warning';
    if (healthy > 0) return 'healthy';
    return 'unknown';
  }
  if (!h.status) return 'unknown';
  if (h.status === 'success') return 'healthy';
  if (h.status === 'failure' || h.status === 'critical') return 'failing';
  if (h.status === 'warning') return 'warning';
  return 'unknown';
}

// Same smoothed getServiceStatus as the UI so demo filtering/facets match it.
function demoServiceStatus(s: ServiceWithResults): 'healthy' | 'failing' | 'warning' | 'unknown' {
  return getServiceStatus(s);
}

// Agent metric series for one agent check: the value is the metric the check
// tracks, not always CPU, and the metadata matches what that metric ships.
function genAgentMetrics(check: CheckDef, startMs: number, endMs: number): AgentMetricPoint[] {
  const host = hostNameForCheck(check);
  const metric = check.type.slice('agent_'.length);
  const points: AgentMetricPoint[] = [];
  eachBucket(startMs, endMs, 60, (bucketStart, _stepMs, minuteIndex) => {
    const value =
      metric === 'cpu' ? cpuValue(host, minuteIndex)
        : metric === 'ram' ? ramValue(host, minuteIndex)
          : metric === 'disk' ? diskValue(host, minuteIndex)
            : round2(wave(`net|${check.id}`, minuteIndex, 0.6, 0.4));
    points.push({
      value,
      timestamp: iso(bucketStart),
      metadata: JSON.parse(
        metric === 'network'
          ? JSON.stringify({
            network_speed_in_mb_per_s: round2(wave(`netin|${check.id}`, minuteIndex, 0.4, 0.2)),
            network_speed_out_mb_per_s: round2(wave(`netout|${check.id}`, minuteIndex, 0.2, 0.1)),
          })
          : agentMetadata(metric, host, minuteIndex, value),
      ),
    });
  });
  return points;
}

// ---------------------------------------------------------------------------
// Incidents, events, correlation, explanations
// ---------------------------------------------------------------------------

// One incident per red thing on screen, titled and described the way the
// product writes them: alert rules use the rule name, check thresholds use
// "<metric> threshold on <check>", a down check uses "Check Failure: <check>".
function demoIncidents(): Incident[] {
  const now = Date.now();
  const incidents: Incident[] = [
    {
      // The check's own threshold owns this one; the CPU alert rule only routes
      // criticals, so a single incident covers the saturation.
      id: 1,
      organization_id: 0,
      service_id: 3,
      title: 'CPU threshold on worker-01 cpu',
      description: 'CPU reached 92.40% (warning threshold: 80.00%)',
      severity: 'warning',
      status: 'open',
      service: 'worker-01',
      started_at: iso(now - CPU_INCIDENT_AGO),
    },
    {
      id: 2,
      organization_id: 0,
      service_id: 11,
      title: 'Check Failure: Back-office',
      description: "Service 'Back-office' has failed 3 consecutive times (max: 3)",
      severity: 'critical',
      status: 'open',
      service: 'web-02',
      started_at: iso(now - BACKOFFICE_INCIDENT_AGO),
    },
    {
      id: 3,
      organization_id: 0,
      alert_rule_id: 2,
      title: 'Application error spike',
      description: "Alert rule 'Application error spike': sum(error_count) = 63.00 critical threshold (window: 600s)",
      severity: 'critical',
      status: 'open',
      started_at: iso(now - ERROR_INCIDENT_AGO),
    },
    {
      id: 4,
      organization_id: 0,
      service_id: 7,
      title: 'Latency threshold on Payment webhook',
      description: 'Latency reached 462.00ms (warning threshold: 300.00ms)',
      severity: 'warning',
      status: 'open',
      service: 'worker-01',
      started_at: iso(now - WEBHOOK_INCIDENT_AGO),
    },
    {
      id: 5,
      organization_id: 0,
      service_id: 22,
      title: 'Latency threshold on db-01 network',
      description: 'Latency reached 181.00ms (warning threshold: 100.00ms)',
      severity: 'warning',
      status: 'open',
      service: 'db-01',
      started_at: iso(now - NETWORK_INCIDENT_AGO),
    },
    {
      id: 6,
      organization_id: 0,
      service_id: 5,
      title: 'Latency threshold on API health',
      description: 'Latency reached 913.00ms (warning threshold: 300.00ms)',
      severity: 'warning',
      service: 'web-01',
      status: 'resolved',
      started_at: iso(now - GATEWAY_DEGRADED_FROM_AGO + 10 * MINUTE),
      resolved_at: iso(now - GATEWAY_DEGRADED_TO_AGO),
      resolution_note: 'Catalog cache warm-up finished, latency back to baseline.',
    },
    {
      id: 7,
      organization_id: 0,
      service_id: 11,
      title: 'Check Failure: Back-office',
      description: "Service 'Back-office' has failed 3 consecutive times (max: 3)",
      severity: 'critical',
      status: 'resolved',
      service: 'web-02',
      started_at: iso(now - BACKOFFICE_PAST_AGO),
      resolved_at: iso(now - BACKOFFICE_PAST_AGO + 40 * MINUTE),
      resolution_note: 'nginx reloaded after certificate rotation.',
    },
  ];

  // Older than every real one, so the filler lands behind them and the first
  // page keeps the estate's own incidents.
  for (let i = 0; i < incidentsPadding(); i += 1) {
    incidents.push({
      id: 1000 + i,
      organization_id: 0,
      title: `Check Failure: Storefront region ${i + 1}`,
      description: "Service 'Storefront' has failed 3 consecutive times (max: 3)",
      severity: 'warning',
      status: 'resolved',
      service: `region-${i + 1}`,
      started_at: iso(now - BACKOFFICE_PAST_AGO - (1 + i) * HOUR),
      resolved_at: iso(now - BACKOFFICE_PAST_AGO - (1 + i) * HOUR + 20 * MINUTE),
    });
  }

  return incidents.sort((a, b) => (a.started_at < b.started_at ? 1 : -1));
}

// Event types the product actually writes: the worker emits error_spike, the
// rest come from the SDKs / events API (deploy, restart, crash).
function demoEvents(): Event[] {
  const now = Date.now();
  const events: Event[] = [
    { id: 1, type: 'error_spike', service: 'web-02', message: "Service 'Back-office' has failed 3 consecutive times (max: 3)", timestamp: iso(now - BACKOFFICE_INCIDENT_AGO) },
    { id: 2, type: 'error_spike', service: 'payment-service', message: 'Error spike: ConnectionTimeout on payment-service', timestamp: iso(now - ERROR_INCIDENT_AGO) },
    // The deploy that precedes the whole incident: correlation ranks it first.
    { id: 3, type: 'deploy', service: 'payment-service', message: 'Deployment v2.14.3 rolled out', timestamp: iso(now - CPU_SATURATION_AGO - 4 * MINUTE) },
    { id: 4, type: 'restart', service: 'web-02', message: 'Agent reconnected after restart', timestamp: iso(now - 4 * HOUR) },
    { id: 5, type: 'deploy', service: 'web-02', message: 'TLS certificate renewed for www.novashop.example', timestamp: iso(now - 6 * HOUR) },
    { id: 6, type: 'deploy', service: 'api-gateway', message: 'Deployment v2.14.2 rolled out', timestamp: iso(now - 28 * HOUR) },
  ];
  return events.sort((a, b) => new Date(b.timestamp).getTime() - new Date(a.timestamp).getTime());
}

function demoMaintenance(): MaintenanceWindow[] {
  const now = Date.now();
  return [
    {
      id: 1,
      organization_id: 0,
      name: 'PostgreSQL 16 upgrade',
      target_type: 'host',
      target_id: 4,
      target_name: 'db-01',
      starts_at: iso(now + 6 * HOUR),
      ends_at: iso(now + 8 * HOUR),
      created_at: iso(now - HOUR),
    },
  ];
}

// Same classification the backend applies: new when the fingerprint first
// appeared within the hour, chronic when it spans more than a day with volume.
function errorRecurrence(def: ErrorDef) {
  const countLastHour = errorCountOver(def, HOUR);
  const count24h = errorCountOver(def, 24 * HOUR);
  const count7d = errorCountOver(def, 7 * 24 * HOUR);
  const firstSeen = errorFirstSeen(def);
  const isNew = firstSeen > Date.now() - HOUR;
  const isRecurrent = !isNew && count7d >= 20 && firstSeen < Date.now() - 24 * HOUR;
  return {
    fingerprint: `demo-fp-${def.name.toLowerCase()}`,
    count_last_hour: countLastHour,
    count_24h: count24h,
    count_7d: count7d,
    first_seen: iso(firstSeen),
    last_seen: iso(errorLastSeen(def)),
    is_new: isNew,
    is_recurrent: isRecurrent,
    description: isNew
      ? `New error (never seen before this hour): ${countLastHour} occurrence(s) in the last hour — likely a recent regression.`
      : isRecurrent
        ? `Recurring error: ${count7d} occurrences over 7 days (${count24h} in 24h). Known chronic issue.`
        : `This error repeated ${countLastHour} times in the last hour.`,
  };
}

// The strongest signal, nudged up when several independent ones agree
// (overallConfidence in backend/services/correlation.go).
function overallConfidence(best: number, signals: number): number {
  if (signals === 0) return 0;
  let conf = best;
  if (signals >= 2) conf += 0.05;
  if (signals >= 3) conf += 0.05;
  return Math.min(round2(conf), 0.98);
}

// Above the threshold, the further the more confident (saturationConfidence).
function saturationConfidence(value: number, threshold: number): number {
  if (value <= threshold) return 0.6;
  return round2(Math.min(0.6 + 0.35 * ((value - threshold) / (100 - threshold)), 0.95));
}

// Infra correlation only fires above 90% CPU on the error path, and the value
// is read live from the same wave the CPU check and the charts plot.
// Raw value: each caller formats it the way the product does (%.1f in a
// correlation description, %.2f in a check message).
function workerCpuNow(): number {
  return cpuValue('worker-01', Math.floor(Date.now() / MINUTE));
}

// The timestamp a result id was generated for (genCheckResults encodes the
// bucket in the id), so an explanation quotes the very value that result shows.
function resultTimeAt(check: CheckDef, resultId: number): number {
  const step = check.interval * 1000;
  const index = resultId % 100_000;
  const current = Math.floor(Date.now() / step);
  const age = (((current - index) % 100_000) + 100_000) % 100_000;
  return (current - age) * step;
}

function demoCorrelation(errorId: number): CorrelationResult {
  const def = ERROR_DEFS.find((d) => d.id === errorId);
  if (!def) {
    return { has_correlation: false, infra: [], services: [], apps: [], semantic_matches: [], summary: '' };
  }
  const recurrence = errorRecurrence(def);
  if (errorId !== 1) {
    return {
      has_correlation: false,
      infra: [],
      services: [],
      apps: [],
      semantic_matches: [],
      recurrence,
      confidence: overallConfidence(0.65, recurrence.is_new || recurrence.is_recurrent ? 1 : 0),
      summary: recurrence.description,
    };
  }
  const cpu = workerCpuNow();
  const infra = [
    {
      metric_name: 'CPU',
      value: cpu,
      threshold: 90,
      description: `Host CPU reached ${cpu.toFixed(1)}% just before the error`,
      confidence: saturationConfidence(cpu, 90),
    },
  ];
  const apps = [
    {
      service: 'checkout-service',
      error_name: 'TypeError',
      count: Math.round((ERROR_DEFS[1].ratePerHour * 20) / 60),
      occurred_at: iso(Date.now() - 20 * MINUTE),
      preceded_incident: true,
      relation: 'dependent',
      confidence: 0.4,
    },
  ];
  const semantic_matches = ['Request timed out, possibly due to overload or a network stall'];
  return {
    has_correlation: true,
    host_id: 3,
    host_name: 'worker-01',
    infra,
    services: [],
    apps,
    semantic_matches,
    recurrence,
    // infra + app + semantic + recurrence = 4 agreeing signals.
    confidence: overallConfidence(Math.max(infra[0].confidence, 0.65), 4),
    summary: [
      recurrence.description,
      `The error message points to a problem of type: ${semantic_matches[0]}.`,
      'Infrastructure shows signs of saturation (CPU).',
    ].join(' '),
  };
}

// RCA for a check result: the backend correlates on the failing check's host,
// so only the checks on the saturated host carry a signal.
function demoServiceResultCorrelation(resultId: number): CorrelationResult {
  const checkId = Math.floor(resultId / 100_000);
  const empty: CorrelationResult = {
    has_correlation: false,
    infra: [],
    services: [],
    apps: [],
    semantic_matches: [],
    summary: 'No infrastructure or service correlation was found for this failure.',
  };
  const check = CHECKS.find((c) => c.id === checkId);
  if (!check || (checkId !== 7 && checkId !== 3)) return empty;
  // Same instant as the analysed result, so the RCA and the explanation agree.
  const cpu = cpuValue('worker-01', Math.floor(resultTimeAt(check, resultId) / MINUTE));
  const infra = [
    {
      metric_name: 'CPU',
      value: cpu,
      threshold: 85,
      description: `Host CPU reached ${cpu.toFixed(1)}% just before the failure`,
      confidence: saturationConfidence(cpu, 85),
    },
  ];
  const semantic_matches = checkId === 7 ? ['Request timed out: check network or CPU load'] : [];
  // Latency neighbours on the same host, excluding the subject: only the CPU
  // check has one (the webhook), and it slowed down instead of failing.
  const webhook = CHECKS.find((c) => c.id === 7)!;
  const degraded = checkId === 3
    ? [{
      service_id: webhook.id,
      service_name: webhook.name,
      host_name: 'worker-01',
      check_type: webhook.type,
      latency_ms: webhook.degraded!.latencyMs!,
      baseline_ms: webhook.latencyMs,
      multiplier: round2(webhook.degraded!.latencyMs! / webhook.latencyMs),
    }]
    : [];
  const degradedSummary = degraded.length
    ? `Latency degraded on ${degraded.map((d) => `${d.service_name} on ${d.host_name} (${Math.round(d.latency_ms)}ms vs ${Math.round(d.baseline_ms)}ms baseline)`).join(', ')} without failing.`
    : '';
  return {
    has_correlation: true,
    host_id: 3,
    host_name: 'worker-01',
    infra,
    services: [],
    degraded,
    apps: [],
    semantic_matches,
    confidence: overallConfidence(
      Math.max(infra[0].confidence, semantic_matches.length ? 0.6 : 0),
      1 + semantic_matches.length,
    ),
    summary: [
      ...semantic_matches.map((m) => `${m}.`),
      degradedSummary,
      'Infrastructure shows signs of saturation (CPU).',
    ].filter(Boolean).join(' '),
  };
}

// Same shape the rules engine renders (renderRulesReport): one bold cause
// sentence with its scope, then at most one correlation line.
function explainReport(cause: string, scope: string, correlation?: string): string {
  const head = `**${cause}** — ${scope}.`;
  return correlation ? `${head}\n\n${correlation}` : head;
}

const ERROR_CAUSES: Record<number, Record<'en' | 'fr', string>> = {
  1: {
    en: 'The call to stripe-gateway times out after 5000ms: the target does not respond in time.',
    fr: "L'appel à stripe-gateway expire après 5000 ms : la cible ne répond pas à temps.",
  },
  2: {
    en: 'Frontend code reads a property on an undefined value.',
    fr: 'Le frontend lit une propriété sur une valeur undefined.',
  },
  3: {
    en: 'The authentication token is expired: the client must refresh it or re-authenticate.',
    fr: "Le token d'authentification est expiré : le client doit le rafraîchir ou se ré-authentifier.",
  },
  4: {
    en: 'The call to catalog-service exceeds the 2s budget: the upstream does not respond in time.',
    fr: "L'appel à catalog-service dépasse le budget de 2 s : l'amont ne répond pas à temps.",
  },
  5: {
    en: 'Two transactions deadlocked on the products table and Postgres aborted one of them.',
    fr: 'Deux transactions se sont interbloquées sur la table products et Postgres en a annulé une.',
  },
};

function explainError(subjectId: number): string {
  const def = ERROR_DEFS.find((d) => d.id === subjectId) ?? ERROR_DEFS[0];
  const lang = locale();
  const cause = ERROR_CAUSES[def.id][lang];
  const scope = `${def.service} / ${def.file}:${def.line}`;
  if (def.id !== 1) return explainReport(cause, scope);
  const cpu = workerCpuNow();
  return explainReport(
    cause,
    scope,
    lang === 'en'
      ? `Possible correlation: Host CPU reached ${cpu.toFixed(1)}% just before the error.`
      : `Corrélation possible : le CPU du host a atteint ${cpu.toFixed(1)} % juste avant l'erreur.`,
  );
}

function explainServiceResult(subjectId: number): string {
  const lang = locale();
  const check = CHECKS.find((c) => c.id === Math.floor(subjectId / 100_000));
  const scope = check ? hostNameForCheck(check) || check.name : '';
  // Read the CPU at the analysed result's own timestamp, not now.
  const cpu = check
    ? cpuValue('worker-01', Math.floor(resultTimeAt(check, subjectId) / MINUTE))
    : workerCpuNow();
  const cpuLine = lang === 'en'
    ? `Possible correlation: Host CPU reached ${cpu.toFixed(1)}% just before the failure.`
    : `Corrélation possible : le CPU du host a atteint ${cpu.toFixed(1)} % juste avant l'échec.`;

  switch (check?.id) {
    case 11:
      return explainReport(
        lang === 'en'
          ? `Connection refused by ${check.host}`
          : `Connexion refusée par ${check.host}`,
        scope,
      );
    case 7:
      return explainReport(
        lang === 'en'
          ? `${check.host} answers above the 300ms warning threshold`
          : `${check.host} répond au-dessus du seuil d'alerte de 300 ms`,
        scope,
        cpuLine,
      );
    case 22:
      return explainReport(
        lang === 'en'
          ? 'Ping latency to db-01 is above the 100ms warning threshold'
          : "La latence de ping vers db-01 dépasse le seuil d'alerte de 100 ms",
        scope,
      );
    case 3:
      return explainReport(
        lang === 'en'
          ? `CPU usage is ${cpu.toFixed(2)}% on worker-01 (warning threshold 80%)`
          : `Le CPU est à ${cpu.toFixed(2)} % sur worker-01 (seuil d'alerte 80 %)`,
        scope,
      );
    default:
      return explainReport(
        lang === 'en'
          ? `${check?.type ?? 'check'} check breached its threshold on ${check?.host ?? ''}`
          : `Le check ${check?.type ?? ''} a dépassé son seuil sur ${check?.host ?? ''}`,
        scope,
      );
  }
}

function explanation(subjectType: 'error' | 'service_result', subjectId: number): ExplanationResponse {
  return {
    subject_type: subjectType,
    subject_id: subjectId,
    content: subjectType === 'error' ? explainError(subjectId) : explainServiceResult(subjectId),
    model: 'rules',
    duration_ms: 38,
    cached: false,
    created_at: iso(Date.now()),
  };
}

async function* explainStream(
  subjectType: 'error' | 'service_result',
  subjectId: number,
): AsyncGenerator<{ type: 'metadata' | 'chunk' | 'error'; data: unknown }> {
  const full = explanation(subjectType, subjectId);
  yield { type: 'metadata', data: { model: full.model, cached: false, created_at: full.created_at } };
  const words = full.content.split(/(?<=\s)/);
  const chunkSize = 4;
  for (let i = 0; i < words.length; i += chunkSize) {
    await new Promise((resolve) => setTimeout(resolve, 25));
    yield { type: 'chunk', data: words.slice(i, i + chunkSize).join('') };
  }
}

// ---------------------------------------------------------------------------
// Static org fixtures
// ---------------------------------------------------------------------------

const DEMO_USERS: User[] = [
  { id: 1, organization_id: 0, email: 'alice@novashop.example', name: 'Alice Martin', role: 'admin', email_verified: true, created_at: CREATED_AT, updated_at: CREATED_AT },
  { id: 2, organization_id: 0, email: 'bruno@novashop.example', name: 'Bruno Keller', role: 'read_write', email_verified: true, created_at: CREATED_AT, updated_at: CREATED_AT },
  { ...demoUser, id: 3 },
];

const DEMO_CHANNELS: NotificationChannel[] = [
  { id: 1, organization_id: 0, name: 'On-call email', type: 'email', config: { to: 'oncall@novashop.example' }, enabled: true, created_at: CREATED_AT, updated_at: CREATED_AT },
  { id: 2, organization_id: 0, name: '#alerts', type: 'slack', config: { webhook_url: 'https://hooks.slack.example/T000/B000' }, enabled: true, created_at: CREATED_AT, updated_at: CREATED_AT },
];

const DEMO_ALERT_RULES = [
  {
    id: 1,
    organization_id: 0,
    name: 'Host CPU saturation',
    description: 'Any host above 95% CPU for 5 minutes.',
    type: 'threshold',
    target_type: 'any',
    metric: 'cpu',
    operator: 'gt',
    threshold: 95,
    duration: 300,
    severity: 'critical',
    aggregation: 'avg',
    critical_threshold: 95,
    recovery_threshold: 90,
    notify_warning: false,
    notify_critical: true,
    enabled: true,
    channels: [1, 2],
    created_at: CREATED_AT,
    updated_at: CREATED_AT,
  },
  {
    // error_count is evaluated organization-wide, so this rule carries no target.
    id: 2,
    organization_id: 0,
    name: 'Application error spike',
    description: 'More than 50 application errors in a 10-minute window.',
    type: 'threshold',
    target_type: 'any',
    metric: 'error_count',
    operator: 'gt',
    threshold: 50,
    duration: 600,
    severity: 'critical',
    aggregation: 'sum',
    critical_threshold: 50,
    notify_warning: false,
    notify_critical: true,
    enabled: true,
    channels: [1, 2],
    created_at: CREATED_AT,
    updated_at: CREATED_AT,
  },
  {
    id: 3,
    organization_id: 0,
    name: 'Gateway latency p95',
    description: 'API health p95 latency above 1500ms.',
    type: 'threshold',
    target_type: 'service',
    target_id: 5,
    metric: 'latency',
    operator: 'gt',
    threshold: 1500,
    duration: 300,
    severity: 'critical',
    aggregation: 'p95',
    critical_threshold: 1500,
    notify_warning: false,
    notify_critical: true,
    enabled: true,
    channels: [2],
    created_at: CREATED_AT,
    updated_at: CREATED_AT,
  },
];

const DEMO_LINKS: ApplicationLink[] = [
  { id: 1, organization_id: 0, app_service_name: 'payment-service', target_type: 'host', target_id: 3, target_name: 'worker-01' },
  { id: 2, organization_id: 0, app_service_name: 'api-gateway', target_type: 'host_group', target_id: 1, target_name: 'production' },
];

const DEMO_HOST_GROUPS: HostGroup[] = [
  { id: 1, organization_id: 0, name: 'production', is_default: true, created_at: CREATED_AT, host_count: DEMO_HOSTS.length },
];

// Only the widget metrics the dashboard builder can actually render: stats read
// the org counters, charts read cpu/ram/latency. Each chart is scoped to one
// service or host, otherwise every series is drawn as a single zigzag line.
// Layouts are saved, like any dashboard a user has arranged once.
const DEMO_DASHBOARDS: CustomDashboard[] = [
  {
    id: 1,
    organization_id: 0,
    name: 'Production overview',
    widgets: [
      { id: 'w1', type: 'stat', metric: 'status', title: 'Status', size: 'small', layout: { i: 'w1', x: 0, y: 0, w: 3, h: 2 } },
      { id: 'w2', type: 'stat', metric: 'services_failing', title: 'Failing services', size: 'small', layout: { i: 'w2', x: 3, y: 0, w: 3, h: 2 } },
      { id: 'w3', type: 'stat', metric: 'errors_24h', title: 'Errors (24h)', size: 'small', layout: { i: 'w3', x: 6, y: 0, w: 3, h: 2 } },
      { id: 'w4', type: 'chart', metric: 'latency', title: 'payment-service latency', size: 'medium', filterService: 'payment-service', layout: { i: 'w4', x: 0, y: 2, w: 6, h: 4 } },
      { id: 'w5', type: 'chart', metric: 'cpu', title: 'worker-01 CPU', size: 'medium', filterHost: 'worker-01', layout: { i: 'w5', x: 6, y: 2, w: 6, h: 4 } },
    ],
    created_at: CREATED_AT,
    updated_at: CREATED_AT,
  },
];

// SDK services carry the platform in their type (error_service_<platform>),
// which is what the Errors view shows as the language badge.
const ERROR_SERVICE_PLATFORMS: Record<string, string> = {
  'api-gateway': 'go',
  'checkout-service': 'typescript',
  'auth-service': 'typescript',
  'payment-service': 'go',
  'catalog-service': 'go',
};

function errorServices(): Service[] {
  return APP_SERVICES.map((name, i) => ({
    id: 100 + i,
    host_id: DEMO_HOSTS.find((h) => h.apps.includes(name))?.id,
    name,
    display_name: null,
    type: `error_service_${ERROR_SERVICE_PLATFORMS[name]}`,
    host: hostOf(name),
    service: hostOf(name),
    service_interval: 0,
    max_attempts: 0,
    created_at: CREATED_AT,
    token: hexId(seeded('token', name), 64),
  }));
}

function demoStats(): OrganizationStats {
  const now = Date.now();
  const groups = errorGroups(now - 24 * HOUR, now);
  const by_service: Record<string, number> = {};
  for (const g of groups) {
    by_service[g.sample.service] = (by_service[g.sample.service] ?? 0) + g.event_count;
  }
  // Service and host counts derive from the same status logic as the Services
  // and Hosts pages, so the overview and their facets can never disagree.
  // Warning hosts count as neither healthy nor failing, mirroring the backend.
  const hosts = demoHosts();
  const hostStatuses = hosts.map(demoHostStatus);
  const checkStatuses = CHECKS.map(checkStatusNow);
  const count = (s: string) => checkStatuses.filter((v) => v === s).length;
  const services = {
    total: CHECKS.length,
    healthy: count('healthy'),
    warning: count('warning'),
    failing: count('failing'),
    unknown: count('unknown'),
  };
  const hostsFailing = hostStatuses.filter((s) => s === 'failing').length;
  return {
    services,
    hosts: {
      total: hosts.length,
      healthy: hostStatuses.filter((s) => s === 'healthy').length,
      failing: hostsFailing,
    },
    errors: {
      total_24h: groups.reduce((sum, g) => sum + g.event_count, 0),
      by_service,
      impacted_services: Object.keys(by_service).length,
    },
    status:
      services.failing > 0 || hostsFailing > 0
        ? services.failing > services.total / 2 || hostsFailing > hosts.length / 2
          ? 'critical'
          : 'degraded'
        : services.warning > 0
          ? 'degraded'
          : 'healthy',
    plan_usage: {
      hosts_used: hosts.length,
      hosts_limit: 10,
      // Agent metric checks are bound to hosts and don't count toward the quota.
      services_used: CHECKS.filter((c) => !c.type.startsWith('agent_')).length,
      services_limit: 50,
      error_services_used: APP_SERVICES.length,
      error_services_limit: 10,
      points_per_minute_limit: 2500,
      points_per_minute_peak: 1800,
      points_over_limit_24h: 0,
      points_limit_enforced: false,
    },
  };
}

function demoErrorStats(): ErrorStats {
  const stats = demoStats();
  const by_error: Record<string, number> = {};
  const now = Date.now();
  for (const def of ERROR_DEFS) {
    by_error[def.name] = errorTimeseries(def, now - 24 * HOUR, now).reduce((sum, p) => sum + p.count, 0);
  }
  return {
    total: stats.errors.total_24h,
    by_service: stats.errors.by_service,
    by_error,
    recent: ERROR_DEFS.map(errorSample).sort((a, b) => (a.timestamp < b.timestamp ? 1 : -1)),
  };
}

// Profile captures the SDKs upload on a schedule. The RAM chart reads the
// memory of these very captures (like the backend, which selects them from
// profile_captures), so the chart and the table can never disagree.
interface ProfileKind {
  service: string;
  type: 'cpu' | 'heap';
  everyMs: number;
  sizeBytes: number;
  code: number;
}

const PROFILE_KINDS: ProfileKind[] = [
  { service: 'payment-service', type: 'heap', everyMs: 5 * MINUTE, sizeBytes: 91824, code: 0 },
  { service: 'payment-service', type: 'cpu', everyMs: 30 * MINUTE, sizeBytes: 48213, code: 1 },
  { service: 'api-gateway', type: 'heap', everyMs: 15 * MINUTE, sizeBytes: 26410, code: 2 },
  { service: 'api-gateway', type: 'cpu', everyMs: 60 * MINUTE, sizeBytes: 3712, code: 3 },
];

function profileMemory(service: string, t: number): number {
  const base = service === 'payment-service' ? 240 : 120;
  return Math.round(clamp(wave(`mem|${service}`, Math.floor(t / MINUTE), base, 25), 60, 400));
}

// The capture kind is recoverable from the id, so a flame graph deep link keeps
// resolving to the same service and profile type.
function genProfiles(
  startMs: number,
  endMs: number,
  filter?: { service?: string; profile_type?: string },
  max = 200,
): ProfileCapture[] {
  const out: ProfileCapture[] = [];
  for (const kind of PROFILE_KINDS) {
    if (filter?.service && filter.service !== kind.service) continue;
    if (filter?.profile_type && filter.profile_type !== kind.type) continue;
    let n = 0;
    for (let t = Math.floor(endMs / kind.everyMs) * kind.everyMs; t >= startMs && n < max; t -= kind.everyMs, n++) {
      out.push({
        id: Math.floor(t / MINUTE) * 10 + kind.code,
        organization_id: 0,
        service: kind.service,
        profile_type: kind.type,
        duration_seconds: kind.type === 'cpu' ? 30 : undefined,
        size_bytes: Math.round(kind.sizeBytes * (0.9 + 0.2 * seeded('profile', kind.code, t)())),
        memory_mb: kind.type === 'heap' ? profileMemory(kind.service, t) : undefined,
        created_at: iso(t),
      });
    }
  }
  return out.sort((a, b) => (a.created_at < b.created_at ? 1 : -1)).slice(0, max);
}

function demoFlamegraph(profileId: number): FlameNode {
  const profile = PROFILE_KINDS.find((k) => k.code === profileId % 10);
  if (profile?.service === 'api-gateway') {
    return {
      name: 'api-gateway',
      value: 1800,
      unit: 'ms',
      valueType: 'cpu',
      children: [
        {
          name: 'net/http.(*ServeMux).ServeHTTP',
          value: 1600,
          children: [
            {
              name: 'gateway.(*Proxy).Forward',
              value: 1350,
              children: [
                { name: 'net/http.(*Transport).RoundTrip', value: 1100 },
                { name: 'encoding/json.Marshal', value: 250 },
              ],
            },
          ],
        },
        { name: 'runtime.gcBgMarkWorker', value: 200 },
      ],
    };
  }
  if (profile?.type === 'heap') {
    return {
      name: 'payment-service',
      value: 242,
      unit: 'MB',
      valueType: 'heap',
      children: [
        {
          name: 'internal/payment.(*Gateway).CreateCharge',
          value: 148,
          children: [{ name: 'net/http.(*Transport).roundTrip', value: 96 }],
        },
        { name: 'database/sql.(*DB).queryDC', value: 62 },
        { name: 'runtime.allocm', value: 32 },
      ],
    };
  }
  // Wall time of the 30s CPU profile is dominated by the stalled outbound dial,
  // which is the same 5s timeout the ConnectionTimeout error reports.
  return {
    name: 'payment-service',
    value: 5000,
    unit: 'ms',
    valueType: 'cpu',
    children: [
      {
        name: 'net/http.(*ServeMux).ServeHTTP',
        value: 4600,
        children: [
          {
            name: 'internal/payment.(*Handler).Charge',
            value: 4200,
            children: [
              { name: 'internal/payment.(*Gateway).CreateCharge', value: 3400, children: [{ name: 'net.(*Dialer).DialContext', value: 2900 }] },
              { name: 'database/sql.(*DB).ExecContext', value: 600 },
            ],
          },
        ],
      },
      { name: 'runtime.gcBgMarkWorker', value: 400 },
    ],
  };
}

// ---------------------------------------------------------------------------
// The demo client
// ---------------------------------------------------------------------------

export function createDemoApi(): OrgApi {
  return {
    get: () => ok(demoOrganization),
    update: () => readOnly(),
    delete: () => readOnly(),
    getUsers: () => ok(DEMO_USERS),
    inviteUser: () => readOnly(),
    updateUserRole: () => readOnly(),
    deleteUser: () => readOnly(),
    getStats: () => ok(demoStats()),

    dashboard: {
      getHealth: () => {
        const s = demoStats();
        return ok({
          status: s.status,
          services: s.services.total,
          services_failing: s.services.failing,
          errors_24h: s.errors.total_24h,
          last_update: iso(Date.now()),
        });
      },
      getErrorStats: () => ok(demoErrorStats(), 'errors.stats'),
      // Averages across every app service, like the backend's AVG over
      // system_metrics — not one host's instant value.
      getMetricStats: () => {
        const minuteIndex = Math.floor(Date.now() / MINUTE);
        const avg = (values: number[]) => values.reduce((a, b) => a + b, 0) / values.length;
        const latencies = APP_SERVICES.filter((s) => s !== 'catalog-service' && s !== 'auth-service').map((s) =>
          appLatency(s, minuteIndex),
        );
        return ok({
          cpu_avg: Math.round(avg(APP_SERVICES.map((s) => cpuValue(hostOf(s), minuteIndex)))),
          ram_avg: Math.round(avg(APP_SERVICES.map((s) => ramValue(hostOf(s), minuteIndex)))),
          http_latency: Math.round(avg(latencies)),
          last_update: iso(Date.now()),
        });
      },
      getTimeline: (service, startDate, endDate) => {
        let events = demoEvents();
        if (service) events = events.filter((e) => e.service === service);
        if (startDate || endDate) {
          const { startMs, endMs } = parseRange(startDate, endDate);
          events = events.filter((e) => {
            const t = new Date(e.timestamp).getTime();
            return t >= startMs && t <= endMs;
          });
        }
        return ok(events);
      },
    },

    customDashboards: {
      list: () => ok(DEMO_DASHBOARDS),
      create: () => readOnly(),
      update: () => readOnly(),
      delete: () => readOnly(),
    },

    errors: {
      list: (opts) => {
        const { startMs, endMs } = parseRange(opts?.start, opts?.end);
        let groups = errorGroups(startMs, endMs, opts?.service);
        const total = groups.length;
        // Mirror the backend: only slice when pagination is explicitly requested
        // (service drill-down), so the overview keeps every group for its facets.
        if (opts?.limit != null || opts?.offset != null) {
          const offset = opts?.offset ?? 0;
          const limit = opts?.limit ?? 50;
          groups = groups.slice(offset, offset + limit);
        }
        return okPaged(opts?.grouped ? groups : groups.map((g) => g.sample), total, 'errors.groups');
      },
      getServices: () => ok(errorServices(), 'errors.services'),
      getCorrelation: (errorId) => ok(demoCorrelation(errorId)),
      explain: (errorId) => ok(explanation('error', errorId)),
      explainSSE: (errorId) => explainStream('error', errorId),
    },

    payments: {
      checkout: () => readOnly(),
      checkoutCustom: () => readOnly(),
      getSubscription: () => ok({ hosts: 10, services: 50, sdk_services: 10, retention_days: 30 }),
      updateSubscription: () => readOnly(),
      billingPortal: () => readOnly(),
    },

    testSmtp: () => readOnly(),

    links: {
      list: () => ok(DEMO_LINKS),
      // Suggestions come from trace peers and never repeat an existing link.
      getSuggestions: (service) => {
        const host = DEMO_HOSTS.find((h) => h.apps.includes(service));
        if (!host || DEMO_LINKS.some((l) => l.app_service_name === service)) {
          return ok({ suggestions: [], empty_reason: 'no_trace_data' as const });
        }
        return ok({
          suggestions: [
            { target_type: 'host' as const, target_id: host.id, target_name: host.name, reason: 'Appears in traces as peer/destination' },
          ],
        });
      },
      create: () => readOnly(),
      delete: () => readOnly(),
    },

    hostGroups: {
      list: () => ok(DEMO_HOST_GROUPS),
      create: () => readOnly(),
      update: () => readOnly(),
      delete: () => readOnly(),
    },

    metrics: {
      list: (service) => {
        const now = Date.now();
        return ok(genSystemMetrics(now - HOUR, now, service));
      },
    },

    hosts: {
      list: (opts) => {
        let all = demoHosts();
        if (opts?.search) {
          const q = opts.search.toLowerCase();
          all = all.filter((h) =>
            h.name.toLowerCase().includes(q) ||
            (h.display_name?.toLowerCase().includes(q) ?? false) ||
            h.host?.toLowerCase().includes(q) ||
            h.service?.toLowerCase().includes(q));
        }
        if (opts?.status && opts.status !== 'all') {
          all = all.filter((h) => demoHostStatus(h) === opts.status);
        }
        if (opts?.limit != null || opts?.offset != null) {
          const offset = opts?.offset ?? 0;
          const limit = opts?.limit ?? 50;
          return okPaged(all.slice(offset, offset + limit), all.length);
        }
        return ok(all);
      },
      stats: () => {
        const all = demoHosts();
        const by = { healthy: 0, failing: 0, warning: 0, unknown: 0, total_services: 0 };
        for (const h of all) {
          by[demoHostStatus(h)] += 1;
          by.total_services += h.service_count ?? 0;
        }
        return ok({ total: all.length, ...by }, 'hosts.stats');
      },
      getById: (hostId) => {
        const host = demoHosts().find((h) => h.id === hostId);
        return host ? ok(host) : Promise.reject(new Error('Not found'));
      },
      // A node_exporter heavy on histogram buckets, scraped every 15s, so the
      // page has a real suggestion to show.
      ingestCost: () =>
        ok({
          window_minutes: 10,
          points_per_minute: 1420,
          org_points_per_minute_limit: 2500,
          enforced: false,
          targets: [
            {
              name: 'node_exporter',
              points_per_minute: 960,
              series: 240,
              scrape_interval_seconds: 15,
              bucket_points_per_minute: 640,
              top_metrics: [
                { name: 'node_disk_io_time_seconds_bucket', points_per_minute: 400 },
                { name: 'node_cpu_seconds_total', points_per_minute: 160 },
              ],
            },
            {
              name: 'app',
              points_per_minute: 400,
              series: 400,
              scrape_interval_seconds: 60,
              bucket_points_per_minute: 0,
              top_metrics: [{ name: 'http_requests_total', points_per_minute: 120 }],
            },
            { name: '', points_per_minute: 60, series: 60, scrape_interval_seconds: 60, bucket_points_per_minute: 0, top_metrics: [] },
          ],
        }),
      create: () => readOnly(),
      update: () => readOnly(),
      assignGroup: () => readOnly(),
      delete: () => readOnly(),
      getServices: (hostId, opts) => {
        const { startMs, endMs } = parseRange(opts?.start_date, opts?.end_date);
        return ok(CHECKS.filter((c) => c.hostId === hostId).map((c) => checkWithResults(c, startMs, endMs)));
      },
      getServicesByName: (hostName) => {
        const host = DEMO_HOSTS.find((h) => h.name === hostName);
        const { startMs, endMs } = parseRange();
        return ok(host ? CHECKS.filter((c) => c.hostId === host.id).map((c) => checkWithResults(c, startMs, endMs)) : []);
      },
    },

    services: {
      getById: (serviceId) => {
        const check = CHECKS.find((c) => c.id === serviceId);
        if (!check) return Promise.reject(new Error('Not found'));
        const { startMs, endMs } = parseRange();
        return ok(checkWithResults(check, startMs, endMs));
      },
      list: (opts) => {
        const { startMs, endMs } = parseRange(opts?.start_date, opts?.end_date);
        let all = CHECKS.map((c) => checkWithResults(c, startMs, endMs));
        if (opts?.search) {
          const q = opts.search.toLowerCase();
          all = all.filter((s) =>
            s.name.toLowerCase().includes(q) ||
            s.host?.toLowerCase().includes(q) ||
            s.host_name?.toLowerCase().includes(q) ||
            s.service?.toLowerCase().includes(q));
        }
        if (opts?.status && opts.status !== 'all') {
          all = all.filter((s) => demoServiceStatus(s) === opts.status);
        }
        if (opts?.type && opts.type !== 'all') {
          all = all.filter((s) => s.type === opts.type);
        }
        if (opts?.limit != null || opts?.offset != null) {
          const offset = opts?.offset ?? 0;
          const limit = opts?.limit ?? 50;
          return okPaged(all.slice(offset, offset + limit), all.length);
        }
        return ok(all);
      },
      stats: () => {
        // Current status, like the backend's facet query: independent of the
        // date range the list is showing.
        const by = { healthy: 0, failing: 0, warning: 0, unknown: 0 };
        const types = new Set<string>();
        for (const c of CHECKS) {
          by[checkStatusNow(c)] += 1;
          types.add(c.type);
        }
        return ok({ total: CHECKS.length, ...by, types: [...types].sort() });
      },
      // The one write the sandbox can serve, and only behind the dev-only seam:
      // the e2e net has to save the service modal for real to check that the
      // list refreshes under the reader instead of starting over.
      // A CheckDef holds less than the form sends: display_name, max_attempts
      // and the thresholds are dropped here and rebuilt by checkToService from
      // the type. The row that comes back is not what was typed.
      create: (serviceData) => {
        if (!demoWritesEnabled()) return readOnly();
        const check: CheckDef = {
          id: Math.max(...CHECKS.map((c) => c.id)) + 1,
          name: serviceData.name,
          type: serviceData.type,
          host: serviceData.host,
          hostId: serviceData.host_id,
          latencyMs: 120,
          interval: serviceData.service_interval,
        };
        CHECKS.push(check);
        return ok(checkToService(check));
      },
      update: () => readOnly(),
      delete: () => readOnly(),
      getResults: (serviceId, startDate, endDate) => {
        const check = CHECKS.find((c) => c.id === serviceId);
        if (!check) return ok([]);
        const { startMs, endMs } = parseRange(startDate, endDate);
        return ok(genCheckResults(check, startMs, endMs));
      },
      getCorrelation: (resultId) => ok(demoServiceResultCorrelation(resultId)),
      explainResult: (resultId) => ok(explanation('service_result', resultId)),
      explainResultSSE: (resultId) => explainStream('service_result', resultId),
      getAgentMetrics: (serviceId) => {
        const check = CHECKS.find((c) => c.id === serviceId && c.type.startsWith('agent_'));
        if (!check) return ok([]);
        const { startMs, endMs } = parseRange();
        return ok(genAgentMetrics(check, startMs, endMs));
      },
    },

    events: {
      list: () => ok(demoEvents()),
      create: () => readOnly(),
    },

    notificationChannels: {
      list: () => ok(DEMO_CHANNELS),
      create: () => readOnly(),
      update: () => readOnly(),
      delete: () => readOnly(),
      test: () => readOnly(),
    },

    alertRules: {
      list: () => ok(DEMO_ALERT_RULES),
      create: () => readOnly(),
      update: () => readOnly(),
      delete: () => readOnly(),
      toggle: () => readOnly(),
    },

    incidents: {
      list: (opts) => {
        const all = demoIncidents().filter((i) => {
          if (opts?.status && i.status !== opts.status) return false;
          if (opts?.severity && i.severity !== opts.severity) return false;
          if (opts?.service && !i.service?.toLowerCase().includes(opts.service.toLowerCase())) return false;
          if (opts?.search) {
            const q = opts.search.toLowerCase();
            const hit = i.title.toLowerCase().includes(q) || i.service?.toLowerCase().includes(q) || i.description?.toLowerCase().includes(q);
            if (!hit) return false;
          }
          return true;
        });
        const offset = opts?.offset ?? 0;
        const limit = opts?.limit ?? 50;
        return okPaged(all.slice(offset, offset + limit), all.length);
      },
      stats: () => {
        const failure = injectedFailure('incidents.stats');
        if (failure) return failure;

        const all = demoIncidents();
        return ok({
          total: all.length,
          open: all.filter((i) => i.status === 'open').length,
          acknowledged: all.filter((i) => i.status === 'acknowledged').length,
          resolved: all.filter((i) => i.status === 'resolved').length,
          critical: all.filter((i) => i.severity === 'critical').length,
          services: [...new Set(all.map((i) => i.service).filter(Boolean) as string[])].sort(),
        });
      },
      create: () => readOnly(),
      updateStatus: () => readOnly(),
    },

    maintenance: {
      list: () => ok(demoMaintenance()),
      create: () => readOnly(),
      delete: () => readOnly(),
    },

    apiKeys: {
      list: () =>
        ok([
          { id: 1, organization_id: 0, name: 'CI pipeline', key_prefix: 'mm_demo1', scopes: 'read', created_at: CREATED_AT },
        ]),
      create: () => readOnly(),
      delete: () => readOnly(),
    },

    installTokens: {
      list: () => ok([{ id: 1, organization_id: 0, token_prefix: 'mmdemo01', name: 'production fleet', created_at: CREATED_AT }]),
      create: () => readOnly(),
      delete: () => readOnly(),
    },

    logs: {
      search: (params) => {
        const { startMs, endMs } = parseRange(params.start, params.end);
        let entries = genLogs(startMs, endMs);
        if (params.service) entries = entries.filter((e) => e.service_name === params.service);
        if (params.severity) {
          const severity = params.severity.toUpperCase();
          entries = entries.filter((e) => e.severity_text === severity);
        }
        if (params.q) {
          entries = entries.filter(compileLogQuery(params.q));
        }
        const from = params.from ?? 0;
        const size = params.size ?? 50;
        return ok({ hits: entries.slice(from, from + size), total: entries.length, from, size });
      },
      fieldValues: (params) => {
        const severities = ['DEBUG', 'INFO', 'WARN', 'ERROR'];
        const pools: Record<string, string[]> = {
          service: APP_SERVICES,
          service_name: APP_SERVICES,
          status: severities,
          severity_text: severities,
          severity: severities,
          host: DEMO_HOSTS.map((h) => h.name),
          hostname: DEMO_HOSTS.map((h) => h.name),
        };
        const values = (pools[params.field] ?? []).filter(
          (v) => !params.prefix || v.toLowerCase().startsWith(params.prefix.toLowerCase()),
        );
        return ok({ values });
      },
    },

    traces: {
      search: (params) => {
        const { startMs, endMs } = parseRange(params.start, params.end);
        let spans = genTraces(startMs, endMs, params.service);
        if (params.q) {
          spans = spans.filter(compileTraceQuery(params.q));
        }
        const from = params.from ?? 0;
        const size = params.size ?? 50;
        return ok({ hits: spans.slice(from, from + size), total: spans.length, from, size });
      },
    },

    network: {
      // Same rows the agent_network checks store, flattened like the API does.
      getMetrics: (start, end) => {
        const { startMs, endMs } = parseRange(start, end);
        const metrics = CHECKS.filter((c) => c.type === 'agent_network').flatMap((c) =>
          genCheckResults(c, startMs, endMs).map((r) => ({
            id: r.id,
            service_id: c.id,
            service_name: c.name,
            host: hostNameForCheck(c),
            status: r.status,
            value: r.metric_value,
            metadata: r.metadata,
            timestamp: r.timestamp,
          })),
        );
        return ok(metrics);
      },
    },

    metricsExplorer: {
      get: (params) => {
        const { startMs, endMs } = parseRange(params.start, params.end);
        return ok({
          metrics: genSystemMetrics(startMs, endMs, params.service, params.host),
          filters: APP_SERVICES.map((s) => ({ service: s, host: hostOf(s) })),
        });
      },
    },

    metricSeries: {
      names: (params) => {
        const prefix = params?.prefix ?? '';
        return ok({ names: demoScopedMetrics(params).filter((n) => n.startsWith(prefix)).sort() });
      },
      labelKeys: (params) => ok({ keys: demoLabelKeys(params.metric) }),
      labelValues: (params) => {
        const prefix = params.prefix ?? '';
        return ok({
          values: demoLabelValues(params.key, params.metric).filter((v) => v.startsWith(prefix)),
        });
      },
      query: (params) => {
        const { startMs, endMs } = parseRange(params.start, params.end);
        // A metric outside the requested scope has no series, like the real query.
        if (!demoScopedMetrics(params).includes(params.metric)) {
          return ok({ series: [] });
        }
        return ok({
          series: genMetricSeries(
            startMs,
            endMs,
            params.metric,
            params.filter ?? [],
            params.group_by,
            params.aggregation ?? 'avg',
          ),
        });
      },
      expression: (body, params) => {
        const { startMs, endMs } = parseRange(params.start, params.end);
        const scoped = demoScopedMetrics(params);
        const env: Record<string, MetricSeries[]> = {};
        for (const q of body.queries) {
          env[q.ref] = scoped.includes(q.metric)
            ? genMetricSeries(
                startMs,
                endMs,
                q.metric,
                (q.filters ?? []).map((f) => `${f.key}:${f.value}`),
                q.group_by,
                q.aggregation,
              ).map((s) => ({ ...s, query: q.ref }))
            : [];
        }
        if (!body.expression) return ok({ series: Object.values(env).flat() });
        const series = demoEvaluateExpression(body.expression, env);
        if (!series) return Promise.reject({ response: { status: 400, data: { error: 'invalid expression' } } });
        return ok({ series });
      },
    },

    profiles: {
      // No date range on this endpoint: the last day of captures, newest first.
      list: (params) => {
        const now = Date.now();
        const all = genProfiles(now - 24 * HOUR, now, params);
        const offset = params?.offset ?? 0;
        const limit = params?.limit ?? 50;
        return okPaged(all.slice(offset, offset + limit), all.length);
      },
      getSeries: (params) => {
        const { startMs, endMs } = parseRange(params?.from, params?.to);
        // Exactly the heap captures of the window: one point per capture, and
        // a single service unless the caller asked for a specific one, since
        // the chart draws one line.
        const service = params?.service || 'payment-service';
        const points = genProfiles(startMs, endMs, { service, profile_type: 'heap' }, params?.limit ?? 500)
          .map((p) => ({ timestamp: p.created_at, memory_mb: p.memory_mb!, service: p.service }))
          .reverse();
        return ok({ points });
      },
      download: () => ok(new Blob(['demo profile'])),
      getFlamegraph: (profileId) => ok(demoFlamegraph(profileId)),
    },
  };
}
