import type { ServiceResult, ServiceWithResults } from '../api';

export type ServiceUiStatus = 'healthy' | 'failing' | 'warning' | 'unknown';

// Agent metrics whose value is a utilization percentage, which is also what
// their thresholds apply to. agent_network is excluded on purpose: its
// metric_value is throughput (MB/s) while its thresholds target ping latency.
export const AGENT_PERCENT_TYPES = ['agent_cpu', 'agent_ram', 'agent_disk'];

// The value a service's thresholds apply to: utilization % for cpu/ram/disk,
// ping latency (ms) for network, check latency (ms) otherwise. Network keeps its
// latency in metadata; a failed ping has no measurable latency.
export function getResultValue(serviceType: string, result: ServiceResult): number | null {
  if (serviceType === 'agent_network') {
    if (!result.metadata) return null;
    try {
      const meta = JSON.parse(result.metadata);
      if (meta.network_ping_success === 0) return null;
      return typeof meta.network_ping_latency_ms === 'number' ? meta.network_ping_latency_ms : null;
    } catch {
      return null;
    }
  }
  if (AGENT_PERCENT_TYPES.includes(serviceType)) return result.metric_value ?? null;
  return result.latency ?? null;
}

// Active checks trust their latest result: the worker already retried it
// max_attempts times before recording it, so a single blip never gets stored.
// Agent metrics are pushed, not checked, so nothing retries them: they only
// breach once their last max_attempts samples ALL breach.
// Results are re-sorted newest-first so callers can pass them in any order.
// cpu/ram/disk compare metric_value here so threshold edits apply retroactively;
// network trusts the stored status, since the backend is the one that compares
// its ping latency to the thresholds.
export function getServiceStatus(service: ServiceWithResults): ServiceUiStatus {
  const results = service.results || [];
  if (results.length === 0) return 'unknown';

  const sorted = [...results].sort(
    (a, b) => new Date(b.timestamp).getTime() - new Date(a.timestamp).getTime()
  );
  const latest = sorted[0];

  if (!service.type.startsWith('agent_')) {
    if (latest.status === 'failure') return 'failing';
    if (latest.status === 'warning') return 'warning';
    if (latest.status === 'success') return 'healthy';
    return getResultValue(service.type, latest) != null ? 'healthy' : 'unknown';
  }

  const maxAttempts = service.max_attempts || 3;
  const recent = sorted.slice(0, maxAttempts);
  const fullWindow = recent.length === maxAttempts;

  if (!AGENT_PERCENT_TYPES.includes(service.type)) {
    if (fullWindow && recent.every((r) => r.status === 'failure')) return 'failing';
    if (fullWindow && recent.every((r) => r.status === 'warning')) return 'warning';
  } else {
    if (
      service.critical_threshold != null &&
      fullWindow &&
      recent.every((r) => r.metric_value != null && r.metric_value > service.critical_threshold!)
    )
      return 'failing';
    if (
      service.warning_threshold != null &&
      fullWindow &&
      recent.every((r) => r.metric_value != null && r.metric_value > service.warning_threshold!)
    )
      return 'warning';
  }

  if (latest.status === 'success') return 'healthy';
  return getResultValue(service.type, latest) != null ? 'healthy' : 'unknown';
}
