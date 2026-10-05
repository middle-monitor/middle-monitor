import { describe, expect, it } from 'vitest';

import type { ServiceResult, ServiceWithResults } from '../api';
import { getResultValue, getServiceStatus } from './serviceStatus';

// Samples are given newest-first for readability; the helper spaces them a
// minute apart so getServiceStatus has real timestamps to sort on.
function results(...samples: Partial<ServiceResult>[]): ServiceResult[] {
  const base = Date.parse('2026-01-01T12:00:00Z');
  return samples.map((s, i) => ({
    id: i,
    service_id: 1,
    status: 'success',
    timestamp: new Date(base - i * 60_000).toISOString(),
    ...s,
  }));
}

function service(over: Partial<ServiceWithResults>): ServiceWithResults {
  return {
    id: 1,
    organization_id: 1,
    name: 'check',
    type: 'http',
    host: 'example.com',
    service: 'app',
    service_interval: 60,
    max_attempts: 3,
    created_at: '2026-01-01T00:00:00Z',
    results: [],
    ...over,
  } as ServiceWithResults;
}

describe('getServiceStatus', () => {
  it('reports unknown when a check has never reported', () => {
    expect(getServiceStatus(service({ results: [] }))).toBe('unknown');
  });

  // An active check is already retried max_attempts times by the worker before
  // a result is stored, so a single stored failure is a real failure. Applying
  // the agent sample window here too would delay every outage by two cycles.
  it('trusts the latest stored result of an active check', () => {
    expect(getServiceStatus(service({ type: 'http', results: results({ status: 'failure' }) }))).toBe('failing');
    expect(getServiceStatus(service({ type: 'http', results: results({ status: 'warning' }) }))).toBe('warning');
    expect(getServiceStatus(service({ type: 'http', results: results({ status: 'success' }) }))).toBe('healthy');
  });

  // Callers hand results over in whatever order the API returned them, so the
  // newest has to be found rather than assumed to be first.
  it('finds the newest result whatever order it is given in', () => {
    const oldestFirst = [...results({ status: 'failure' }, { status: 'success' })].reverse();
    expect(getServiceStatus(service({ type: 'http', results: oldestFirst }))).toBe('failing');
  });

  describe('agent metrics', () => {
    // Nothing retries a pushed metric, so the window is what stops one spike
    // from paging. This is the frontend half of the rule the evaluator applies.
    it('needs every sample in the window over the threshold to fail', () => {
      const breaching = service({
        type: 'agent_cpu',
        max_attempts: 3,
        critical_threshold: 90,
        results: results({ metric_value: 95 }, { metric_value: 96 }, { metric_value: 97 }),
      });
      expect(getServiceStatus(breaching)).toBe('failing');

      const oneSpike = service({
        type: 'agent_cpu',
        max_attempts: 3,
        critical_threshold: 90,
        results: results({ metric_value: 95 }, { metric_value: 10 }, { metric_value: 10 }),
      });
      expect(getServiceStatus(oneSpike)).toBe('healthy');
    });

    // A partial window is not evidence yet: the check has not been running long
    // enough to tell a spike from a trend.
    it('does not fail on a window that is not full yet', () => {
      const partial = service({
        type: 'agent_cpu',
        max_attempts: 3,
        critical_threshold: 90,
        results: results({ metric_value: 95 }, { metric_value: 96 }),
      });
      expect(getServiceStatus(partial)).toBe('healthy');
    });

    // Comparing the stored values rather than the stored status is what makes a
    // threshold edit apply to history instead of only to future samples.
    it('re-applies an edited threshold to samples already stored', () => {
      const lowered = service({
        type: 'agent_cpu',
        max_attempts: 3,
        warning_threshold: 40,
        results: results({ metric_value: 50 }, { metric_value: 55 }, { metric_value: 60 }),
      });
      expect(getServiceStatus(lowered)).toBe('warning');
    });

    // Critical outranks warning when both are crossed, so a saturated host is
    // never reported as merely warning.
    it('prefers critical over warning', () => {
      const both = service({
        type: 'agent_cpu',
        max_attempts: 3,
        warning_threshold: 40,
        critical_threshold: 80,
        results: results({ metric_value: 95 }, { metric_value: 96 }, { metric_value: 97 }),
      });
      expect(getServiceStatus(both)).toBe('failing');
    });

    // agent_network keeps its thresholds on ping latency, which the backend
    // already compared; its metric_value is throughput and must not be measured
    // against them here.
    it('trusts the stored status for network checks instead of its metric_value', () => {
      const network = service({
        type: 'agent_network',
        max_attempts: 3,
        critical_threshold: 100,
        results: results(
          { status: 'failure', metric_value: 1200 },
          { status: 'failure', metric_value: 1200 },
          { status: 'failure', metric_value: 1200 }
        ),
      });
      expect(getServiceStatus(network)).toBe('failing');

      // Throughput far over the latency threshold on its own proves nothing.
      const busyButHealthy = service({
        type: 'agent_network',
        max_attempts: 3,
        critical_threshold: 100,
        results: results({ status: 'success', metric_value: 1200 }),
      });
      expect(getServiceStatus(busyButHealthy)).toBe('healthy');
    });
  });
});

describe('getResultValue', () => {
  // cpu/ram/disk thresholds are percentages, and metric_value is that
  // percentage.
  it('reads metric_value for utilisation metrics', () => {
    const result = results({ metric_value: 42 })[0];
    expect(getResultValue('agent_cpu', result)).toBe(42);
    expect(getResultValue('agent_ram', result)).toBe(42);
    expect(getResultValue('agent_disk', result)).toBe(42);
  });

  it('reads latency for everything else', () => {
    const result = results({ latency: 120, metric_value: 42 })[0];
    expect(getResultValue('http', result)).toBe(120);
  });

  // Network thresholds target ping latency, which lives in metadata; reading
  // metric_value here would compare throughput against a millisecond threshold.
  it('reads the ping latency out of network metadata, not metric_value', () => {
    const result = results({
      metric_value: 950,
      metadata: JSON.stringify({ network_ping_latency_ms: 12, network_ping_success: 1 }),
    })[0];
    expect(getResultValue('agent_network', result)).toBe(12);
  });

  // A ping that did not come back has no latency to compare, so the caller must
  // get null rather than a zero that reads as a perfect result.
  it('returns null when the ping failed', () => {
    const result = results({
      metadata: JSON.stringify({ network_ping_latency_ms: 0, network_ping_success: 0 }),
    })[0];
    expect(getResultValue('agent_network', result)).toBeNull();
  });

  // Metadata is a stored string; a truncated or malformed one must not throw
  // and take the whole services list down with it.
  it('survives missing or malformed metadata', () => {
    expect(getResultValue('agent_network', results({})[0])).toBeNull();
    expect(getResultValue('agent_network', results({ metadata: '{not json' })[0])).toBeNull();
    expect(getResultValue('agent_network', results({ metadata: '{}' })[0])).toBeNull();
  });
});
