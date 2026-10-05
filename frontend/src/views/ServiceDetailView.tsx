import { useState, useMemo } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useParams, Link, useLocation, useNavigate } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import type { TFunction } from 'i18next';
import {
  HiOutlineArrowLeft,
  HiOutlineExclamationTriangle,
  HiOutlineServer,
  HiOutlineGlobeAlt,
  HiOutlineServerStack,
  HiOutlineSignal,
  HiOutlineShieldCheck,
  HiOutlineCpuChip,
  HiOutlineDocumentText,
  HiOutlineCalendarDays,
  HiOutlineArrowRight,
  HiOutlineChartBar,
  HiOutlineChartBarSquare,
  HiOutlineXMark,
  HiPencil,
  HiOutlineTrash,
  HiOutlineWrenchScrewdriver,
} from 'react-icons/hi2';
import {
  type ServiceWithResults,
  type ServiceResult,
  type CorrelationResult,
  type Host,
  type MaintenanceWindow,
} from '../api';
import { useOrgApi, useOrgQueryScope } from '../hooks/useOrgApi';
import { useQueryRefresh } from '../hooks/useQueryRefresh';
import { allHostsKey, invalidateEntityDelete } from '../queryClient';
import { RefreshControl } from '../components/RefreshControl';
import { useAuth } from '../contexts/AuthContext';
import { useOrgPath } from '../hooks/useOrgPath';
import { useServiceModal } from '../contexts/ServiceModalContext';
import { useDateRange } from '../contexts/DateRangeContext';
import { useDateRangeBounds, useDateRangeKey } from '../hooks/useDateRangeBounds';
import { DateRangePicker } from '../components/DateRangePicker';
import { ExplainPanel } from '../components/ExplainPanel';
import { usePlan, ProUpgradeBanner } from '../components/PlanGate';
import { ScheduleDowntimeModal } from '../components/ScheduleDowntimeModal';
import { ConfirmDialog } from '../components/ConfirmDialog';
import { Skeleton } from '../components/Skeleton';
import {
  getServiceStatus,
  getResultValue,
  AGENT_PERCENT_TYPES,
} from '../utils/serviceStatus';
import {
  MaintenanceBadge,
  findMaintenanceForTarget,
} from '../components/MaintenanceBadge';
import './ServiceDetailView.css';

// ---- Helpers ----

const SERVICE_TYPE_ICONS: Record<string, React.ReactNode> = {
  http: <HiOutlineGlobeAlt />,
  sql: <HiOutlineServerStack />,
  snmp: <HiOutlineSignal />,
  certificate: <HiOutlineShieldCheck />,
  agent_cpu: <HiOutlineCpuChip />,
  agent_ram: <HiOutlineCpuChip />,
  agent_disk: <HiOutlineServerStack />,
  agent_network: <HiOutlineSignal />,
};

type DerivedStatus = 'success' | 'warning' | 'failure';

// Color class for a utilization stat, driven by the service warning/critical
// thresholds (the source of truth). Falls back to 80/90 only when no threshold
// is configured.
function utilizationColorClass(
  value: number | null | undefined,
  service: ServiceWithResults | null,
): string {
  if (value == null) return '';
  if (service?.critical_threshold != null && value > service.critical_threshold)
    return 'text-error';
  if (service?.warning_threshold != null && value > service.warning_threshold)
    return 'text-warning';
  if (
    service?.critical_threshold == null &&
    service?.warning_threshold == null
  ) {
    if (value >= 90) return 'text-error';
    if (value >= 80) return 'text-warning';
  }
  return 'text-success';
}

// Latency (ms) against the service thresholds. No fallback: without thresholds
// there is no meaningful "slow" for a latency, unlike a utilization percentage.
function latencyColorClass(
  value: number,
  service: ServiceWithResults | null,
): string {
  if (service?.critical_threshold != null && value > service.critical_threshold)
    return 'text-error';
  if (service?.warning_threshold != null && value > service.warning_threshold)
    return 'text-warning';
  return 'text-success';
}

function deriveStatus(
  r: ServiceResult,
  service: ServiceWithResults | null | undefined,
): DerivedStatus {
  const stored = (r.status as DerivedStatus) ?? 'success';
  if (!service) return stored;
  const isPercent = AGENT_PERCENT_TYPES.includes(service.type);
  const value = getResultValue(service.type, r);
  if (!isPercent && stored === 'failure') return 'failure';
  if (value != null) {
    if (
      service.critical_threshold != null &&
      value > service.critical_threshold
    )
      return 'failure';
    if (service.warning_threshold != null && value > service.warning_threshold)
      return 'warning';
  }
  return isPercent ? 'success' : stored;
}

function getStatusLabel(status: string, t: TFunction): string {
  switch (status) {
    case 'healthy':
      return t('services.stats.healthy');
    case 'failing':
      return t('services.stats.failing');
    case 'warning':
      return t('services.stats.warning');
    default:
      return t('services.stats.unknown');
  }
}

function getResultStatusLabel(status: string, t: TFunction): string {
  if (status === 'success') return t('overview.stat.ok');
  if (status === 'failure') return t('service_detail.status.failure');
  if (status === 'warning') return t('services.stats.warning');
  return status;
}

function getTypeLabel(type: string, t: TFunction): string {
  if (type.startsWith('error_service_')) {
    return t('service_detail.types.error_sdk', {
      platform: type.replace('error_service_', ''),
    });
  }
  const key = `service_detail.types.${type}`;
  return t(key, { defaultValue: type });
}

function getTypeIcon(type: string): React.ReactNode {
  if (type.startsWith('error_service_'))
    return <HiOutlineExclamationTriangle />;
  return SERVICE_TYPE_ICONS[type] || <HiOutlineDocumentText />;
}

function formatDuration(ms: number): string {
  if (ms < 1000) return `${Math.round(ms)}ms`;
  return `${(ms / 1000).toFixed(2)}s`;
}

function formatTimestamp(ts: string, locale: string): string {
  return new Date(ts).toLocaleString(locale, {
    day: '2-digit',
    month: '2-digit',
    year: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  });
}

function getTimeAgo(ts: string, t: TFunction): string {
  const diff = Math.floor((Date.now() - new Date(ts).getTime()) / 1000);
  if (diff < 60) return t('service_detail.time.seconds_ago', { count: diff });
  if (diff < 3600)
    return t('overview.time.minutes_ago', { count: Math.floor(diff / 60) });
  if (diff < 86400)
    return t('overview.time.hours_ago', { count: Math.floor(diff / 3600) });
  return t('overview.time.days_ago', { count: Math.floor(diff / 86400) });
}

function formatStorageGb(gb: number, t: TFunction): string {
  if (gb < 1024) return `${gb.toFixed(1)} ${t('service_detail.units.gb')}`;
  return `${(gb / 1024).toFixed(2)} ${t('service_detail.units.tb')}`;
}

function formatBytes(v: number, t: TFunction): string {
  if (v >= 1e9)
    return `${(v / 1e9).toFixed(2)} ${t('service_detail.units.gb')}`;
  if (v >= 1e6)
    return `${(v / 1e6).toFixed(1)} ${t('service_detail.units.mb')}`;
  return `${(v / 1e3).toFixed(1)} ${t('service_detail.units.kb')}`;
}

// ---- Timeline bucketing (aggregate when too many results) ----

const MAX_TIMELINE_BARS = 250;

interface TimelineBucket {
  /** Synthetic ServiceResult representing this bucket */
  result: ServiceResult;
  /** Number of original results in this bucket */
  count: number;
}

function bucketResults(
  results: ServiceResult[],
  t: TFunction,
): TimelineBucket[] {
  // Results come DESC from API, reverse to ASC
  const asc = results.slice().reverse();

  if (asc.length <= MAX_TIMELINE_BARS) {
    return asc.map((r) => ({ result: r, count: 1 }));
  }

  // Split into MAX_TIMELINE_BARS equal-time buckets
  const firstTs = new Date(asc[0].timestamp).getTime();
  const lastTs = new Date(asc[asc.length - 1].timestamp).getTime();
  const range = lastTs - firstTs || 1;
  const bucketSize = range / MAX_TIMELINE_BARS;

  const buckets: TimelineBucket[] = [];
  let bucketStart = firstTs;
  let bucketItems: ServiceResult[] = [];
  let idx = 0;

  for (let b = 0; b < MAX_TIMELINE_BARS; b++) {
    const bucketEnd = bucketStart + bucketSize;
    bucketItems = [];

    while (
      idx < asc.length &&
      new Date(asc[idx].timestamp).getTime() < bucketEnd
    ) {
      bucketItems.push(asc[idx]);
      idx++;
    }

    if (bucketItems.length === 0) {
      // Empty bucket — skip (will be filled by neighbor logic below or just omitted)
      bucketStart = bucketEnd;
      continue;
    }

    // Worst status: failure > warning > success
    const hasFailure = bucketItems.some((r) => r.status === 'failure');
    const hasWarning = bucketItems.some((r) => r.status === 'warning');
    const worstStatus = hasFailure
      ? 'failure'
      : hasWarning
        ? 'warning'
        : 'success';

    // Average latency
    const latencies = bucketItems
      .map((r) => r.latency)
      .filter((l): l is number => l != null && l > 0);
    const avgLat = latencies.length
      ? latencies.reduce((a, c) => a + c, 0) / latencies.length
      : undefined;

    // Representative item: for tooltip, show the point that caused failure/warning (so "89% OK" vs "91% Échec" is consistent)
    const midItem = bucketItems[Math.floor(bucketItems.length / 2)];
    let representative = midItem;
    if (bucketItems.length > 1 && (hasFailure || hasWarning)) {
      const worst = hasFailure
        ? bucketItems
            .filter((r) => r.status === 'failure')
            .sort((a, b) => (b.metric_value ?? 0) - (a.metric_value ?? 0))[0]
        : bucketItems
            .filter((r) => r.status === 'warning')
            .sort((a, b) => (b.metric_value ?? 0) - (a.metric_value ?? 0))[0];
      if (worst) representative = worst;
    }

    buckets.push({
      result: {
        ...representative,
        status: worstStatus,
        latency: avgLat,
        message:
          bucketItems.length > 1 && representative.message
            ? representative.message
            : bucketItems.length > 1
              ? t('service_detail.aggregated_checks', {
                  count: bucketItems.length,
                })
              : midItem.message,
      },
      count: bucketItems.length,
    });

    bucketStart = bucketEnd;
  }

  // Collect any remaining items
  if (idx < asc.length) {
    const remaining = asc.slice(idx);
    const hasFailure = remaining.some((r) => r.status === 'failure');
    const hasWarning = remaining.some((r) => r.status === 'warning');
    const worstStatus = hasFailure
      ? 'failure'
      : hasWarning
        ? 'warning'
        : 'success';
    const latencies = remaining
      .map((r) => r.latency)
      .filter((l): l is number => l != null && l > 0);
    const avgLat = latencies.length
      ? latencies.reduce((a, c) => a + c, 0) / latencies.length
      : undefined;
    const midItem = remaining[Math.floor(remaining.length / 2)];
    let representative = midItem;
    if (remaining.length > 1 && (hasFailure || hasWarning)) {
      const worst = hasFailure
        ? remaining
            .filter((r) => r.status === 'failure')
            .sort((a, b) => (b.metric_value ?? 0) - (a.metric_value ?? 0))[0]
        : remaining
            .filter((r) => r.status === 'warning')
            .sort((a, b) => (b.metric_value ?? 0) - (a.metric_value ?? 0))[0];
      if (worst) representative = worst;
    }
    buckets.push({
      result: {
        ...representative,
        status: worstStatus,
        latency: avgLat,
        message:
          remaining.length > 1 && representative.message
            ? representative.message
            : remaining.length > 1
              ? t('service_detail.aggregated_checks', {
                  count: remaining.length,
                })
              : midItem.message,
      },
      count: remaining.length,
    });
  }

  return buckets;
}

const FAILURE_SPARK_BINS = 24;

interface FailureGroup {
  /** Failure message used as the dedupe signature */
  message: string;
  /** Number of failures sharing this message in the window */
  count: number;
  /** Most recent occurrence — drives the timestamp and the "Analyze cause" RCA */
  latest: ServiceResult;
  firstSeen: string;
  lastSeen: string;
  /** Occurrence count per time bin across the window (for the sparkline) */
  spark: number[];
}

// aggregateFailures collapses failing results that share the same message into a
// single row (s-style): a recurring failure bubbles up by lastSeen and shows
// how many times it happened, instead of flooding the list with identical rows.
function aggregateFailures(results: ServiceResult[]): FailureGroup[] {
  const failures = results.filter((r) => r.status === 'failure');
  if (!failures.length) return [];

  const times = failures.map((r) => new Date(r.timestamp).getTime());
  const minTs = Math.min(...times);
  const maxTs = Math.max(...times);
  const range = maxTs - minTs || 1;

  const groups = new Map<string, FailureGroup>();
  for (const r of failures) {
    const key = (r.message ?? '').trim();
    let g = groups.get(key);
    if (!g) {
      g = {
        message: key,
        count: 0,
        latest: r,
        firstSeen: r.timestamp,
        lastSeen: r.timestamp,
        spark: new Array(FAILURE_SPARK_BINS).fill(0),
      };
      groups.set(key, g);
    }
    g.count++;
    const ts = new Date(r.timestamp).getTime();
    if (ts >= new Date(g.lastSeen).getTime()) {
      g.lastSeen = r.timestamp;
      g.latest = r;
    }
    if (ts < new Date(g.firstSeen).getTime()) g.firstSeen = r.timestamp;
    const bin = Math.min(
      FAILURE_SPARK_BINS - 1,
      Math.floor(((ts - minTs) / range) * FAILURE_SPARK_BINS),
    );
    g.spark[bin]++;
  }

  return [...groups.values()].sort(
    (a, b) => new Date(b.lastSeen).getTime() - new Date(a.lastSeen).getTime(),
  );
}

// FailureSparkline renders a tiny s-style bar chart of occurrences per time
// bin. Empty bins keep a faint baseline so the time axis reads continuously.
function FailureSparkline({
  data,
  width = 96,
  height = 28,
}: {
  data: number[];
  width?: number;
  height?: number;
}) {
  const max = Math.max(1, ...data);
  const n = data.length;
  const gap = 1;
  const barW = (width - gap * (n - 1)) / n;
  return (
    <svg width={width} height={height} style={{ display: 'block' }} aria-hidden>
      {data.map((c, i) => {
        const h = c > 0 ? Math.max(2, (c / max) * height) : 1;
        return (
          <rect
            key={i}
            x={i * (barW + gap)}
            y={height - h}
            width={barW}
            height={h}
            rx={0.5}
            fill='var(--status-error)'
            opacity={c > 0 ? 1 : 0.25}
          />
        );
      })}
    </svg>
  );
}

// ---- Latency / Utilization Graph (SVG) ----

interface LatencyGraphProps {
  results: ServiceResult[];
  /** When 'utilization', Y-axis shows metric_value (0-100%) for agent_disk */
  metricMode?: 'latency' | 'utilization';
  service?: ServiceWithResults | null;
  onPointClick?: (result: ServiceResult) => void;
}

import {
  AreaChart,
  Area,
  XAxis,
  YAxis,
  CartesianGrid,
  Tooltip,
  ResponsiveContainer,
  ReferenceArea,
  ReferenceLine,
} from 'recharts';

// A fresh [] on every render gives each dependent useMemo a new identity and
// makes it recompute every time. One frozen empty list keeps that identity stable.
const EMPTY_LIST: never[] = [];

function formatJsonKey(key: string): string {
  return key.replace(/_/g, ' ').replace(/\b\w/g, (c) => c.toUpperCase());
}

function formatJsonScalar(value: unknown): string {
  if (typeof value === 'number') {
    return Number.isInteger(value)
      ? String(value)
      : value.toFixed(3).replace(/\.?0+$/, '');
  }
  return String(value);
}

function CheckResultMessage({ message }: { message: string }) {
  const detailsIdx = message.indexOf(' Details: ');
  if (detailsIdx === -1) {
    return <div className='check-result-modal-message'>{message}</div>;
  }

  const prefix = message.slice(0, detailsIdx);
  const jsonStr = message.slice(detailsIdx + ' Details: '.length);
  let parsed: Record<string, unknown>;
  try {
    parsed = JSON.parse(jsonStr);
  } catch {
    return <div className='check-result-modal-message'>{message}</div>;
  }

  const entries = Object.entries(parsed);

  return (
    <div className='check-result-modal-message'>
      {prefix && (
        <div className='check-result-modal-message-prefix'>{prefix}</div>
      )}
      <div className='check-result-modal-details-table'>
        {entries.map(([key, value]) => {
          if (Array.isArray(value)) {
            if (value.length === 0) return null;
            const isObjArray =
              typeof value[0] === 'object' && value[0] !== null;
            return (
              <div key={key} className='check-result-modal-details-section'>
                <div className='check-result-modal-details-section-title'>
                  {formatJsonKey(key)}
                </div>
                {isObjArray ? (
                  (value as Record<string, unknown>[]).map((row, i) => (
                    <div key={i} className='check-result-modal-details-subrow'>
                      {Object.entries(row).map(([k, v]) => (
                        <span
                          key={k}
                          className='check-result-modal-details-chip'>
                          <span className='check-result-modal-details-chip-label'>
                            {formatJsonKey(k)}
                          </span>
                          <span className='check-result-modal-details-chip-value'>
                            {formatJsonScalar(v)}
                          </span>
                        </span>
                      ))}
                    </div>
                  ))
                ) : (
                  <div className='check-result-modal-details-value'>
                    {(value as unknown[]).map(formatJsonScalar).join(', ')}
                  </div>
                )}
              </div>
            );
          }
          if (typeof value === 'object' && value !== null) return null;
          const scalar = formatJsonScalar(value);
          const isLong = scalar.length > 55;
          return isLong ? (
            <div
              key={key}
              className='check-result-modal-details-row check-result-modal-details-row-block'>
              <span className='check-result-modal-details-label'>
                {formatJsonKey(key)}
              </span>
              <span className='check-result-modal-details-value-block'>
                {scalar}
              </span>
            </div>
          ) : (
            <div key={key} className='check-result-modal-details-row'>
              <span className='check-result-modal-details-label'>
                {formatJsonKey(key)}
              </span>
              <span className='check-result-modal-details-value'>{scalar}</span>
            </div>
          );
        })}
      </div>
    </div>
  );
}

const renderDot = (props: any) => {
  const { cx, cy, payload } = props;
  const s = payload.result.status;
  if (s === 'failure') {
    return (
      <circle
        key={`dot-${payload.index}`}
        cx={cx}
        cy={cy}
        r={4}
        fill='var(--status-error)'
        stroke='#fff'
        strokeWidth={1}
      />
    );
  }
  if (s === 'warning') {
    return (
      <circle
        key={`dot-${payload.index}`}
        cx={cx}
        cy={cy}
        r={3.5}
        fill='var(--status-warning)'
        stroke='#fff'
        strokeWidth={1}
      />
    );
  }
  return (
    <circle
      key={`dot-empty-${payload.index}`}
      cx={cx}
      cy={cy}
      r={0}
      opacity={0}
    />
  );
};

const renderActiveDot = (props: any) => {
  const { cx, cy, payload } = props;
  const s = payload.result.status;
  const fill =
    s === 'failure'
      ? 'var(--status-error)'
      : s === 'warning'
        ? 'var(--status-warning)'
        : 'var(--brand-primary)';
  return (
    <circle cx={cx} cy={cy} r={6} fill={fill} stroke='#fff' strokeWidth={2} />
  );
};

function LatencyGraph({
  results,
  metricMode = 'latency',
  service,
  onPointClick,
}: LatencyGraphProps) {
  const { t } = useTranslation();
  const reversed = useMemo(() => results.slice().reverse(), [results]);
  const isUtilization = metricMode === 'utilization';
  const isNetwork = service?.type === 'agent_network';
  const hasLatency =
    !isUtilization &&
    !isNetwork &&
    reversed.some((r) => r.latency != null && r.latency > 0);

  const [chartZoom, setChartZoom] = useState<{
    start: number | null;
    end: number | null;
  }>({ start: null, end: null });
  const [chartSelection, setChartSelection] = useState<{
    startX: number;
    endX: number;
  } | null>(null);

  const data = useMemo(() => {
    let sliced = reversed;
    if (chartZoom.start !== null && chartZoom.end !== null) {
      sliced = reversed.slice(chartZoom.start, chartZoom.end);
    }
    return sliced.map((r, i) => {
      const rawValue =
        isUtilization || isNetwork
          ? (r.metric_value ?? 0)
          : hasLatency
            ? (r.latency ?? 0)
            : 50;
      return {
        index: i,
        rawValue,
        result: r,
        isNetwork,
        isUtilization,
      };
    });
  }, [reversed, hasLatency, isUtilization, isNetwork, chartZoom]);

  // ping/certificate/sql failures carry no latency, so their point is drawn at 0
  // and disappears into the baseline. Mark the failing ranges on the X axis, which
  // stays visible whatever the Y scale is.
  const failureRanges = useMemo(() => {
    const ranges: { start: number; end: number }[] = [];
    let current: { start: number; end: number } | null = null;
    for (const d of data) {
      if (d.result.status === 'failure') {
        if (current) current.end = d.index;
        else current = { start: d.index, end: d.index };
      } else if (current) {
        ranges.push(current);
        current = null;
      }
    }
    if (current) ranges.push(current);
    return ranges;
  }, [data]);

  const handleChartMouseDown = (e: any) => {
    if (!e || e.activeTooltipIndex === undefined) return;
    setChartSelection({
      startX: e.activeTooltipIndex,
      endX: e.activeTooltipIndex,
    });
  };

  const handleChartMouseMove = (e: any) => {
    if (!chartSelection || !e || e.activeTooltipIndex === undefined) return;
    setChartSelection({ ...chartSelection, endX: e.activeTooltipIndex });
  };

  const handleChartMouseUp = () => {
    if (chartSelection) {
      const start = Math.min(chartSelection.startX, chartSelection.endX);
      const end = Math.max(chartSelection.startX, chartSelection.endX);
      if (end - start > 1) {
        // Minimum 2 points to zoom
        setChartZoom({
          start: chartZoom.start !== null ? chartZoom.start + start : start,
          end: chartZoom.start !== null ? chartZoom.start + end : end,
        });
      }
    }
    setChartSelection(null);
  };

  const resetZoom = () => {
    setChartZoom({ start: null, end: null });
  };

  if (reversed.length === 0)
    return (
      <div
        style={{
          padding: '2rem',
          textAlign: 'center',
          color: 'var(--text-tertiary)',
        }}>
        {t('service_detail.no_data')}
      </div>
    );

  return (
    <div className='service-detail-graph' style={{ position: 'relative' }}>
      {chartZoom.start !== null && (
        <button
          onClick={resetZoom}
          className='btn btn-secondary'
          style={{
            position: 'absolute',
            top: 0,
            right: 0,
            zIndex: 10,
            padding: '4px 8px',
            fontSize: '12px',
          }}>
          {t('service_detail.zoom_out')}
        </button>
      )}
      <ResponsiveContainer width='100%' height={220}>
        <AreaChart
          data={data}
          margin={{ top: 20, right: 20, bottom: 0, left: 0 }}
          onMouseDown={handleChartMouseDown}
          onMouseMove={handleChartMouseMove}
          onMouseUp={handleChartMouseUp}
          onMouseLeave={() => setChartSelection(null)}
          onClick={(e) => {
            if (e?.activePayload?.[0]?.payload?.result && onPointClick) {
              onPointClick(e.activePayload[0].payload.result);
            }
          }}
          style={{ cursor: 'pointer' }}>
          <defs>
            <linearGradient id='colorValue' x1='0' y1='0' x2='0' y2='1'>
              <stop
                offset='5%'
                stopColor='var(--brand-primary)'
                stopOpacity={0.3}
              />
              <stop
                offset='95%'
                stopColor='var(--brand-primary)'
                stopOpacity={0}
              />
            </linearGradient>
          </defs>
          <CartesianGrid
            strokeDasharray='3 3'
            stroke='var(--border-primary)'
            vertical={false}
          />
          {failureRanges.map((r) =>
            r.start === r.end ? (
              <ReferenceLine
                key={`failure-${r.start}`}
                x={r.start}
                stroke='var(--status-error)'
                strokeWidth={2}
                strokeOpacity={0.5}
              />
            ) : (
              <ReferenceArea
                key={`failure-${r.start}`}
                x1={r.start}
                x2={r.end}
                fill='var(--status-error)'
                fillOpacity={0.15}
                strokeOpacity={0}
              />
            ),
          )}
          <XAxis
            dataKey='index'
            tick={false}
            axisLine={false}
            tickLine={false}
          />
          <YAxis
            dataKey='rawValue'
            tick={{ fontSize: 11, fill: 'var(--text-tertiary)' }}
            axisLine={false}
            tickLine={false}
            tickFormatter={(tick) =>
              isUtilization
                ? `${tick}%`
                : isNetwork
                  ? `${tick.toFixed(2)} MB/s`
                  : tick < 1000
                    ? `${tick}ms`
                    : `${(tick / 1000).toFixed(1)}s`
            }
            width={60}
          />
          <Tooltip
            content={() => null}
            cursor={{ stroke: 'var(--border-primary)', strokeWidth: 1 }}
          />
          <Area
            type='monotone'
            dataKey='rawValue'
            stroke='var(--brand-primary)'
            strokeWidth={2}
            fillOpacity={1}
            fill='url(#colorValue)'
            isAnimationActive={false}
            dot={renderDot}
            activeDot={renderActiveDot}
          />
          {service?.warning_threshold != null && (
            <ReferenceLine
              y={service.warning_threshold}
              stroke='var(--status-warning)'
              strokeDasharray='4 4'
              strokeOpacity={0.7}
            />
          )}
          {service?.critical_threshold != null && (
            <ReferenceLine
              y={service.critical_threshold}
              stroke='var(--status-error)'
              strokeDasharray='4 4'
              strokeOpacity={0.8}
            />
          )}
          {chartSelection && (
            <ReferenceArea
              x1={chartSelection.startX}
              x2={chartSelection.endX}
              strokeOpacity={0.3}
              fill='var(--brand-primary)'
              fillOpacity={0.1}
            />
          )}
        </AreaChart>
      </ResponsiveContainer>
    </div>
  );
}

// ---- Certificate expiration card ----

interface CertificateExpirationCardProps {
  service: ServiceWithResults;
  results: ServiceResult[]; // newest first
}

// A certificate check has no meaningful time series (the worker only stores the
// expiry date, no latency), so instead of the generic latency graph we show the
// days remaining and where they sit relative to the warning/critical thresholds,
// which are defined in DAYS and fire when the remaining days drop BELOW them.
function CertificateExpirationCard({
  service,
  results,
}: CertificateExpirationCardProps) {
  const { t, i18n } = useTranslation();

  // Most recent result carrying an expiry date (a failed TLS dial stores none).
  const expiresAt = useMemo(() => {
    for (const r of results) {
      if (!r.metadata) continue;
      try {
        const meta = JSON.parse(r.metadata) as { expires_at?: string };
        if (meta.expires_at) return new Date(meta.expires_at);
      } catch {
        /* invalid JSON, skip */
      }
    }
    return null;
  }, [results]);

  const cardTitle = (
    <div className='card-title service-detail-cert-title'>
      <HiOutlineShieldCheck className='card-title-icon' />
      {t('service_detail.certificate.title')}
    </div>
  );

  if (!expiresAt) {
    return (
      <div className='card'>
        {cardTitle}
        <div className='service-detail-cert-empty'>
          {results[0]?.message || t('metrics_view.certificate_display.waiting')}
        </div>
      </div>
    );
  }

  const dayMs = 24 * 60 * 60 * 1000;
  const daysRemaining = Math.floor((expiresAt.getTime() - Date.now()) / dayMs);
  const expired = daysRemaining < 0;

  // Backend defaults when no threshold is set: warning 30 days, critical 7 days.
  const warning = service.warning_threshold ?? 30;
  const critical = service.critical_threshold ?? 7;

  const level =
    expired || daysRemaining <= critical
      ? 'critical'
      : daysRemaining <= warning
        ? 'warning'
        : 'ok';
  const colorClass =
    level === 'critical'
      ? 'text-error'
      : level === 'warning'
        ? 'text-warning'
        : 'text-success';

  // Threshold-anchored scale: keep enough headroom past the warning mark so the
  // cursor stays visible even when the certificate has plenty of life left.
  const maxScale = Math.max(warning * 2, daysRemaining, critical + 1);
  const pct = (v: number) => Math.max(0, Math.min(100, (v / maxScale) * 100));
  const criticalPct = pct(critical);
  const warningPct = pct(warning);
  const cursorPct = expired ? 0 : pct(daysRemaining);

  const expiryLabel = expiresAt.toLocaleDateString(i18n.language, {
    day: 'numeric',
    month: 'long',
    year: 'numeric',
  });

  return (
    <div className='card'>
      <div className='service-detail-cert-header'>
        {cardTitle}
        <span className='service-detail-threshold-hint'>
          {t('service_modal.labels.threshold_warning')} {'< '}
          {t('service_detail.certificate.mark_days', { count: warning })}
          {' · '}
          {t('service_modal.labels.threshold_critical')} {'< '}
          {t('service_detail.certificate.mark_days', { count: critical })}
        </span>
      </div>

      <div className='service-detail-cert-summary'>
        <span className={`service-detail-cert-count ${colorClass}`}>
          {expired
            ? t('service_detail.certificate.expired')
            : t('service_detail.certificate.days_remaining', {
                count: daysRemaining,
              })}
        </span>
        <span className='service-detail-cert-expiry'>
          {t('metrics_view.certificate_display.expires_on', {
            date: expiryLabel,
          })}
        </span>
      </div>

      <div className='service-detail-cert-bar'>
        <div
          className='service-detail-cert-bar-zone critical'
          style={{ left: 0, width: `${criticalPct}%` }}
        />
        <div
          className='service-detail-cert-bar-zone warning'
          style={{
            left: `${criticalPct}%`,
            width: `${warningPct - criticalPct}%`,
          }}
        />
        <div
          className='service-detail-cert-bar-zone ok'
          style={{ left: `${warningPct}%`, width: `${100 - warningPct}%` }}
        />
        <div
          className={`service-detail-cert-bar-cursor ${colorClass}`}
          style={{ left: `${cursorPct}%` }}
        />
      </div>
      <div className='service-detail-cert-bar-marks'>
        <span
          className='service-detail-cert-bar-mark'
          style={{ left: `${criticalPct}%` }}>
          {t('service_detail.certificate.mark_days', { count: critical })}
        </span>
        <span
          className='service-detail-cert-bar-mark'
          style={{ left: `${warningPct}%` }}>
          {t('service_detail.certificate.mark_days', { count: warning })}
        </span>
      </div>
    </div>
  );
}

// ---- Component ----

function ServiceDetailView() {
  const { t, i18n } = useTranslation();
  const { id } = useParams<{ id: string }>();
  const orgApi = useOrgApi();
  const queryClient = useQueryClient();
  const queryScope = useOrgQueryScope();
  const { isPro } = usePlan();
  const { canWrite } = useAuth();
  const { orgPath } = useOrgPath();
  const location = useLocation();
  const navigate = useNavigate();
  const fromHost = location.state as {
    fromHostId?: number;
    fromHostName?: string;
  } | null;
  const backPath = fromHost?.fromHostId
    ? orgPath(`/hosts/${fromHost.fromHostId}`)
    : orgPath('/services');
  const backLabel = fromHost?.fromHostName
    ? fromHost.fromHostName
    : t('services.title');
  const [showDeleteConfirm, setShowDeleteConfirm] = useState(false);

  const { openModal } = useServiceModal();
  const { dateRange, setDateRange, formatDateRangeLabel } = useDateRange();
  const getDateBounds = useDateRangeBounds();
  const dateRangeKey = useDateRangeKey();
  const [calendarOpen, setCalendarOpen] = useState(false);
  const [selectedResult, setSelectedResult] = useState<ServiceResult | null>(
    null,
  );
  const [chartMode, setChartMode] = useState<'timeline' | 'graph'>('graph');

  const [rcaResult, setRcaResult] = useState<CorrelationResult | null>(null);
  const [rcaLoading, setRcaLoading] = useState(false);
  const [selectedResultForRca, setSelectedResultForRca] =
    useState<ServiceResult | null>(null);
  const [showDowntimeModal, setShowDowntimeModal] = useState(false);

  const fetchRca = async (result: ServiceResult) => {
    setRcaLoading(true);
    setSelectedResultForRca(result);
    try {
      const res = await orgApi.services.getCorrelation(result.id);
      setRcaResult(res.data);
    } catch (err) {
      console.error('Failed to fetch RCA:', err);
    } finally {
      setRcaLoading(false);
    }
  };

  const serviceId = Number(id);

  const serviceQueryKey = [queryScope, 'services', 'detail', serviceId];

  // Delete this service, then go back where the user came from (host or list).
  const handleDelete = async () => {
    try {
      await orgApi.services.delete(serviceId);
      invalidateEntityDelete(queryClient, queryScope, serviceQueryKey);
      navigate(backPath);
    } catch {
      alert(t('metrics_view.delete_error'));
    } finally {
      setShowDeleteConfirm(false);
    }
  };

  // An open result, an open modal or a pending confirmation is a detail row:
  // refreshing under one is what pausing is for.
  const { refetchInterval, buildControl } = useQueryRefresh(
    'service-detail',
    0,
    selectedResult !== null || showDowntimeModal || showDeleteConfirm,
  );

  const serviceQuery = useQuery({
    queryKey: serviceQueryKey,
    queryFn: async () => {
      const [serviceRes, maintRes] = await Promise.all([
        orgApi.services.getById(serviceId),
        orgApi.maintenance
          .list()
          .catch(() => ({ data: [] as MaintenanceWindow[] })),
      ]);
      return {
        service: serviceRes.data as ServiceWithResults,
        maintenanceWindows: (maintRes.data as MaintenanceWindow[]) || [],
      };
    },
    enabled: !!serviceId,
    refetchInterval,
  });

  // Host list for the edit modal; it does not follow the refresh cadence.
  const hostsQuery = useQuery({
    queryKey: allHostsKey(queryScope),
    queryFn: async () => ((await orgApi.hosts.list()).data as Host[]) || [],
    enabled: !!serviceId,
  });

  const resultsQuery = useQuery({
    queryKey: [queryScope, 'services', 'results', serviceId, dateRangeKey],
    queryFn: async () => {
      const { startIso, endIso } = getDateBounds();
      const res = await orgApi.services.getResults(serviceId, startIso, endIso);
      return (res.data as ServiceResult[]) || [];
    },
    enabled: !!serviceId,
    // Another date window keeps the current chart on screen while it loads.
    // Another service does not: its results would show under the new URL.
    placeholderData: (previous, previousQuery) =>
      previousQuery?.queryKey[3] === serviceId ? previous : undefined,
    refetchInterval,
  });

  const service = serviceQuery.data?.service ?? null;
  const maintenanceWindows = serviceQuery.data?.maintenanceWindows ?? [];
  const hosts = hostsQuery.data ?? [];
  const results = resultsQuery.data ?? EMPTY_LIST;
  // A revalidation shows a discreet badge; it never empties the chart.
  const resultsLoading = resultsQuery.isFetching;

  const autoRefresh = buildControl({
    query: serviceQuery,
    queryKey: serviceQueryKey,
    prefix: [queryScope, 'services'],
  });
  const { refresh } = autoRefresh;

  // Results with status recomputed from the warning/critical thresholds (the
  // source of truth). Everything below — chart dots, timeline, counts, recent
  // failures — reads this so a breach shows consistently everywhere.
  const displayResults = useMemo(
    () => results.map((r) => ({ ...r, status: deriveStatus(r, service) })),
    [results, service],
  );

  // Failing results grouped by message: one row per distinct failure, sorted by
  // most recent so a recurring failure bubbles up with its occurrence count.
  const aggregatedFailures = useMemo(
    () => aggregateFailures(displayResults),
    [displayResults],
  );

  // Compute stats from results
  const resultStats = useMemo(() => {
    if (!displayResults.length) return null;
    const total = displayResults.length;
    const success = displayResults.filter((r) => r.status === 'success').length;
    const warning = displayResults.filter((r) => r.status === 'warning').length;
    const failure = displayResults.filter((r) => r.status === 'failure').length;
    const latencies = displayResults
      .map((r) => r.latency)
      .filter((l): l is number => l != null && l > 0);
    const avgLatency = latencies.length
      ? latencies.reduce((a, b) => a + b, 0) / latencies.length
      : null;
    const maxLatency = latencies.length ? Math.max(...latencies) : null;
    const minLatency = latencies.length ? Math.min(...latencies) : null;
    const p95Latency = latencies.length
      ? latencies.sort((a, b) => a - b)[Math.floor(latencies.length * 0.95)]
      : null;
    // Availability: a degraded check still answered, so only hard failures count
    // as downtime. Warnings get their own tile so the counts still add up.
    const uptime = total > 0 ? Math.round(((total - failure) / total) * 10000) / 100 : 0;

    // Disk (agent_disk): latest utilization and space from first (most recent) result
    const latest = displayResults[0];
    let diskUtilization: number | null = null;
    let diskFreeGb: number | null = null;
    let diskTotalGb: number | null = null;
    if (latest?.metric_value != null) diskUtilization = latest.metric_value;
    if (latest?.metadata) {
      try {
        const meta = JSON.parse(latest.metadata) as {
          disk_total_gb?: number;
          disk_free_gb?: number;
        };
        if (typeof meta.disk_total_gb === 'number')
          diskTotalGb = meta.disk_total_gb;
        if (typeof meta.disk_free_gb === 'number')
          diskFreeGb = meta.disk_free_gb;
      } catch {
        // ignore
      }
    }
    // If disk_free_gb is missing from metadata, compute it from the usage % and the total
    if (diskFreeGb == null && diskTotalGb != null && diskUtilization != null) {
      diskFreeGb = (diskTotalGb * (100 - diskUtilization)) / 100;
    }

    // CPU (agent_cpu): utilisation + load from metadata
    let cpuUtilization: number | null = null;
    let load1min: number | null = null;
    let load5min: number | null = null;
    let load15min: number | null = null;
    if (latest?.metric_value != null) cpuUtilization = latest.metric_value;
    if (latest?.metadata) {
      try {
        const meta = JSON.parse(latest.metadata) as {
          load_1min?: number;
          load_5min?: number;
          load_15min?: number;
        };
        if (typeof meta.load_1min === 'number') load1min = meta.load_1min;
        if (typeof meta.load_5min === 'number') load5min = meta.load_5min;
        if (typeof meta.load_15min === 'number') load15min = meta.load_15min;
      } catch {
        // ignore
      }
    }

    // RAM (agent_ram): utilisation + total from metadata
    let ramUtilization: number | null = null;
    let ramTotalGb: number | null = null;
    if (latest?.metric_value != null) ramUtilization = latest.metric_value;
    if (latest?.metadata) {
      try {
        const meta = JSON.parse(latest.metadata) as { ram_total_gb?: number };
        if (typeof meta.ram_total_gb === 'number')
          ramTotalGb = meta.ram_total_gb;
      } catch {
        // ignore
      }
    }

    // Network (agent_network): throughput and bytes from metadata
    let networkSpeedIn: number | null = null;
    let networkSpeedOut: number | null = null;
    let networkBytesIn: number | null = null;
    let networkBytesOut: number | null = null;
    let networkPingLatency: number | null = null;
    if (latest?.metadata) {
      try {
        const meta = JSON.parse(latest.metadata) as {
          network_speed_in_mb_per_s?: number;
          network_speed_out_mb_per_s?: number;
          network_bytes_in_total?: number;
          network_bytes_out_total?: number;
          network_ping_latency_ms?: number;
        };
        const si = meta.network_speed_in_mb_per_s;
        const so = meta.network_speed_out_mb_per_s;
        if (typeof si === 'number') networkSpeedIn = si;
        if (typeof so === 'number') networkSpeedOut = so;
        if (typeof meta.network_bytes_in_total === 'number')
          networkBytesIn = meta.network_bytes_in_total;
        if (typeof meta.network_bytes_out_total === 'number')
          networkBytesOut = meta.network_bytes_out_total;
        if (typeof meta.network_ping_latency_ms === 'number')
          networkPingLatency = meta.network_ping_latency_ms;
      } catch {
        // ignore
      }
    }

    // HTTP: connection phases of the latest check. All-zero phases mean the
    // connection came from the pool, which is the normal steady state.
    let httpPhases: {
      dns: number;
      connect: number;
      tls: number;
      server: number;
      redirects: number;
      finalUrl: string | null;
    } | null = null;
    if (latest?.metadata) {
      try {
        const meta = JSON.parse(latest.metadata) as {
          http_dns_ms?: number;
          http_connect_ms?: number;
          http_tls_ms?: number;
          http_server_ms?: number;
          http_redirects?: number;
          http_final_url?: string;
        };
        if (typeof meta.http_server_ms === 'number') {
          httpPhases = {
            dns: meta.http_dns_ms ?? 0,
            connect: meta.http_connect_ms ?? 0,
            tls: meta.http_tls_ms ?? 0,
            server: meta.http_server_ms,
            redirects: meta.http_redirects ?? 0,
            finalUrl: meta.http_final_url ?? null,
          };
        }
      } catch {
        // ignore
      }
    }

    return {
      total,
      success,
      warning,
      failure,
      avgLatency,
      maxLatency,
      minLatency,
      p95Latency,
      uptime,
      diskUtilization,
      diskFreeGb,
      diskTotalGb,
      cpuUtilization,
      load1min,
      load5min,
      load15min,
      ramUtilization,
      ramTotalGb,
      networkSpeedIn,
      networkSpeedOut,
      networkBytesIn,
      networkBytesOut,
      networkPingLatency,
      httpPhases,
    };
  }, [displayResults]);

  // Back link and period selector are shared with the loaded branch below, so
  // the user can read where they are and change the period before the data
  // arrives. Both branches start with a fragment: a bare div here would make
  // React drop and rebuild the whole subtree when the data lands.
  const breadcrumb = (
    <div className='service-detail-breadcrumb'>
      <Link to={backPath} className='service-detail-back'>
        <HiOutlineArrowLeft />
        {backLabel}
      </Link>
      {service && (
        <>
          <span className='service-detail-breadcrumb-sep'>/</span>
          <span className='service-detail-breadcrumb-current'>
            {service.name}
          </span>
        </>
      )}
    </div>
  );

  const periodBar = (
    <>
      <div className='service-detail-time-bar'>
        <div className='service-detail-time-label'>
          <HiOutlineCalendarDays />
          {t('service_detail.period')}
        </div>
        <button
          className='service-detail-date-trigger'
          onClick={() => setCalendarOpen(true)}>
          {formatDateRangeLabel(dateRange, { withTime: false })}
        </button>
        <RefreshControl control={autoRefresh} />
      </div>

      <DateRangePicker
        isOpen={calendarOpen}
        value={dateRange}
        onChange={(range) => setDateRange(range)}
        onClose={() => setCalendarOpen(false)}
        showTime
      />
    </>
  );

  // Only the results area waits on the first load: a failed refresh keeps the
  // last known service on screen.
  if (serviceQuery.isPending) {
    return (
      <>
        <div className='service-detail'>
          {breadcrumb}
          <Skeleton rows={2} silent />
          {periodBar}
          <Skeleton rows={6} />
        </div>
      </>
    );
  }

  if (serviceQuery.isLoadingError || !service) {
    return (
      <div className='service-detail-error'>
        <p>{t('service_detail.not_found')}</p>
        <Link to={backPath} className='btn-back'>
          <HiOutlineArrowLeft />{' '}
          {t('service_detail.back_to', { label: backLabel })}
        </Link>
      </div>
    );
  }

  const status = getServiceStatus(service);
  const chartModeLabel =
    service.type === 'agent_disk' ||
    service.type === 'agent_cpu' ||
    service.type === 'agent_ram'
      ? t('service_detail.chart.utilization')
      : service.type === 'agent_network'
        ? t('service_detail.chart.throughput')
        : t('service_detail.chart.latency');

  return (
    <>
      <div className='service-detail'>
        {/* Breadcrumb / Back */}
        {breadcrumb}

        {/* Header */}
        <div className='service-detail-header'>
          <div className='service-detail-header-left'>
            <div
              className={`service-detail-status-indicator service-detail-status-${status}`}>
              <span
                className={`service-detail-status-dot service-detail-status-dot-${status}`}
              />
              {getStatusLabel(status, t)}
            </div>
            <div
              style={{
                display: 'flex',
                alignItems: 'center',
                gap: '0.75rem',
                flexWrap: 'wrap',
              }}>
              <h1 className='service-detail-title'>{service.name}</h1>
              {(() => {
                const win = findMaintenanceForTarget(
                  maintenanceWindows,
                  'service',
                  service.id,
                );
                return win ? <MaintenanceBadge window={win} /> : null;
              })()}
              {canWrite && (
                <button
                  className='btn btn-secondary btn-icon'
                  onClick={() => openModal(service, hosts)}
                  title={t('service_detail.edit_service')}
                  style={{ padding: '0.375rem', borderRadius: '6px' }}>
                  <HiPencil size={16} />
                </button>
              )}
              {canWrite && (
                <button
                  className='btn btn-danger btn-icon'
                  onClick={() => setShowDeleteConfirm(true)}
                  title={t('common.delete')}
                  style={{ padding: '0.375rem', borderRadius: '6px' }}>
                  <HiOutlineTrash size={16} />
                </button>
              )}
              {canWrite && (
                <button
                  className='btn btn-secondary'
                  onClick={() => setShowDowntimeModal(true)}
                  style={{
                    padding: '0.375rem 0.75rem',
                    borderRadius: '6px',
                    fontSize: '0.8125rem',
                    display: 'inline-flex',
                    alignItems: 'center',
                    gap: '0.375rem',
                  }}>
                  <HiOutlineWrenchScrewdriver size={14} />
                  {t('maintenance.schedule')}
                </button>
              )}
              <Link
                to={orgPath(`/errors?service=${service.name}`)}
                className='btn btn-secondary'
                style={{
                  padding: '0.375rem 0.75rem',
                  borderRadius: '6px',
                  fontSize: '0.8125rem',
                  textDecoration: 'none',
                }}
                title={t('service_detail.view_errors_title')}>
                {t('service_detail.view_errors')}
              </Link>
            </div>
            <div className='service-detail-meta'>
              <span className='service-detail-type'>
                {getTypeIcon(service.type)}
                {getTypeLabel(service.type, t)}
              </span>
              {service.host_name && (
                <span className='service-detail-host'>
                  <HiOutlineServer />
                  {service.host_name}
                </span>
              )}
              <span className='service-detail-url'>
                {service.host}
                {service.path || ''}
              </span>
            </div>
          </div>
          <div className='service-detail-header-right'>
            <div className='service-detail-info-item'>
              <span className='service-detail-info-label'>
                {t('service_detail.interval')}
              </span>
              <span className='service-detail-info-value'>
                {service.service_interval}s
              </span>
            </div>
            <div className='service-detail-info-item'>
              <span className='service-detail-info-label'>
                {t('service_detail.max_attempts')}
              </span>
              <span className='service-detail-info-value'>
                {service.max_attempts}
              </span>
            </div>
            <div className='service-detail-info-item'>
              <span className='service-detail-info-label'>
                {t('service_detail.created_at')}
              </span>
              <span className='service-detail-info-value'>
                {new Date(service.created_at).toLocaleDateString(i18n.language)}
              </span>
            </div>
          </div>
        </div>

        {/* Time Range Selector */}
        {periodBar}

        {/* Stats Summary */}
        {resultStats && (
          <div className='service-detail-stats'>
            {service.type === 'agent_disk' ? (
              <>
                <div className='service-detail-stat'>
                  <div
                    className={`service-detail-stat-value ${utilizationColorClass(resultStats.diskUtilization, service)}`}>
                    {resultStats.diskUtilization != null
                      ? `${Math.round(resultStats.diskUtilization)}%`
                      : '—'}
                  </div>
                  <div className='service-detail-stat-label'>
                    {t('service_detail.stats.utilization')}
                  </div>
                </div>
                <div className='service-detail-stat'>
                  <div className='service-detail-stat-value'>
                    {resultStats.diskFreeGb != null
                      ? formatStorageGb(resultStats.diskFreeGb, t)
                      : '—'}
                  </div>
                  <div className='service-detail-stat-label'>
                    {t('service_detail.stats.free_space')}
                  </div>
                </div>
                <div className='service-detail-stat'>
                  <div className='service-detail-stat-value'>
                    {resultStats.diskTotalGb != null
                      ? formatStorageGb(resultStats.diskTotalGb, t)
                      : '—'}
                  </div>
                  <div className='service-detail-stat-label'>
                    {t('service_detail.stats.total')}
                  </div>
                </div>
              </>
            ) : service.type === 'agent_cpu' ? (
              <>
                <div className='service-detail-stat'>
                  <div
                    className={`service-detail-stat-value ${utilizationColorClass(resultStats.cpuUtilization, service)}`}>
                    {resultStats.cpuUtilization != null
                      ? `${Math.round(resultStats.cpuUtilization)}%`
                      : '—'}
                  </div>
                  <div className='service-detail-stat-label'>
                    {t('service_detail.stats.utilization')}
                  </div>
                </div>
                {resultStats.load1min != null && (
                  <div className='service-detail-stat'>
                    <div className='service-detail-stat-value'>
                      {resultStats.load1min.toFixed(2)}
                    </div>
                    <div className='service-detail-stat-label'>
                      {t('service_detail.stats.load_1min')}
                    </div>
                  </div>
                )}
                {resultStats.load5min != null && (
                  <div className='service-detail-stat'>
                    <div className='service-detail-stat-value'>
                      {resultStats.load5min.toFixed(2)}
                    </div>
                    <div className='service-detail-stat-label'>
                      {t('service_detail.stats.load_5min')}
                    </div>
                  </div>
                )}
                {resultStats.load15min != null && (
                  <div className='service-detail-stat'>
                    <div className='service-detail-stat-value'>
                      {resultStats.load15min.toFixed(2)}
                    </div>
                    <div className='service-detail-stat-label'>
                      {t('service_detail.stats.load_15min')}
                    </div>
                  </div>
                )}
              </>
            ) : service.type === 'agent_ram' ? (
              <>
                <div className='service-detail-stat'>
                  <div
                    className={`service-detail-stat-value ${utilizationColorClass(resultStats.ramUtilization, service)}`}>
                    {resultStats.ramUtilization != null
                      ? `${Math.round(resultStats.ramUtilization)}%`
                      : '—'}
                  </div>
                  <div className='service-detail-stat-label'>
                    {t('service_detail.stats.utilization')}
                  </div>
                </div>
                {resultStats.ramTotalGb != null && (
                  <div className='service-detail-stat'>
                    <div className='service-detail-stat-value'>
                      {formatStorageGb(resultStats.ramTotalGb, t)}
                    </div>
                    <div className='service-detail-stat-label'>
                      {t('service_detail.stats.total')}
                    </div>
                  </div>
                )}
                {resultStats.ramTotalGb != null &&
                  resultStats.ramUtilization != null && (
                    <div className='service-detail-stat'>
                      <div className='service-detail-stat-value'>
                        {formatStorageGb(
                          (resultStats.ramTotalGb *
                            (100 - resultStats.ramUtilization)) /
                            100,
                          t,
                        )}
                      </div>
                      <div className='service-detail-stat-label'>
                        {t('service_detail.stats.free_memory')}
                      </div>
                    </div>
                  )}
              </>
            ) : service.type === 'agent_network' ? (
              <>
                {resultStats.networkSpeedIn != null && (
                  <div className='service-detail-stat'>
                    <div className='service-detail-stat-value'>
                      {resultStats.networkSpeedIn.toFixed(2)} MB/s
                    </div>
                    <div className='service-detail-stat-label'>
                      {t('service_detail.stats.speed_in')}
                    </div>
                  </div>
                )}
                {resultStats.networkSpeedOut != null && (
                  <div className='service-detail-stat'>
                    <div className='service-detail-stat-value'>
                      {resultStats.networkSpeedOut.toFixed(2)} MB/s
                    </div>
                    <div className='service-detail-stat-label'>
                      {t('service_detail.stats.speed_out')}
                    </div>
                  </div>
                )}
                {resultStats.networkBytesIn != null && (
                  <div className='service-detail-stat'>
                    <div className='service-detail-stat-value'>
                      {formatBytes(resultStats.networkBytesIn, t)}
                    </div>
                    <div className='service-detail-stat-label'>
                      {t('service_detail.stats.received_total')}
                    </div>
                  </div>
                )}
                {resultStats.networkBytesOut != null && (
                  <div className='service-detail-stat'>
                    <div className='service-detail-stat-value'>
                      {formatBytes(resultStats.networkBytesOut, t)}
                    </div>
                    <div className='service-detail-stat-label'>
                      {t('service_detail.stats.sent_total')}
                    </div>
                  </div>
                )}
                {resultStats.networkPingLatency != null && (
                  <div className='service-detail-stat'>
                    <div
                      className={`service-detail-stat-value ${latencyColorClass(resultStats.networkPingLatency, service)}`}>
                      {resultStats.networkPingLatency.toFixed(0)} ms
                    </div>
                    <div className='service-detail-stat-label'>
                      {t('service_detail.stats.ping_latency')}
                    </div>
                  </div>
                )}
                {resultStats.networkSpeedIn == null &&
                  resultStats.networkSpeedOut == null &&
                  resultStats.networkBytesIn == null &&
                  resultStats.networkBytesOut == null &&
                  resultStats.networkPingLatency == null && (
                    <>
                      <div className='service-detail-stat'>
                        <div
                          className={`service-detail-stat-value ${resultStats.uptime >= 99 ? 'text-success' : resultStats.uptime >= 95 ? 'text-warning' : 'text-error'}`}>
                          {resultStats.uptime}%
                        </div>
                        <div className='service-detail-stat-label'>
                          {t('service_detail.stats.compliance')}
                        </div>
                      </div>
                      <div className='service-detail-stat'>
                        <div className='service-detail-stat-value'>
                          {resultStats.total}
                        </div>
                        <div className='service-detail-stat-label'>
                          {t('service_detail.stats.checks')}
                        </div>
                      </div>
                    </>
                  )}
              </>
            ) : (
              <>
                <div className='service-detail-stat'>
                  <div
                    className={`service-detail-stat-value ${resultStats.uptime >= 99 ? 'text-success' : resultStats.uptime >= 95 ? 'text-warning' : 'text-error'}`}>
                    {resultStats.uptime}%
                  </div>
                  <div className='service-detail-stat-label'>
                    {t('service_detail.stats.uptime')}
                  </div>
                </div>
                <div className='service-detail-stat'>
                  <div className='service-detail-stat-value'>
                    {resultStats.total}
                  </div>
                  <div className='service-detail-stat-label'>
                    {t('service_detail.stats.checks')}
                  </div>
                </div>
                <div className='service-detail-stat'>
                  <div className='service-detail-stat-value text-success'>
                    {resultStats.success}
                  </div>
                  <div className='service-detail-stat-label'>
                    {t('service_detail.stats.success')}
                  </div>
                </div>
                {resultStats.warning > 0 && (
                  <div className='service-detail-stat'>
                    <div className='service-detail-stat-value text-warning'>
                      {resultStats.warning}
                    </div>
                    <div className='service-detail-stat-label'>
                      {t('service_detail.stats.warnings')}
                    </div>
                  </div>
                )}
                <div className='service-detail-stat'>
                  <div
                    className={`service-detail-stat-value ${resultStats.failure > 0 ? 'text-error' : ''}`}>
                    {resultStats.failure}
                  </div>
                  <div className='service-detail-stat-label'>
                    {t('service_detail.stats.failures')}
                  </div>
                </div>
                {resultStats.avgLatency != null && (
                  <>
                    <div className='service-detail-stat'>
                      <div className='service-detail-stat-value'>
                        {formatDuration(resultStats.avgLatency)}
                      </div>
                      <div className='service-detail-stat-label'>
                        {t('service_detail.stats.avg_latency')}
                      </div>
                    </div>
                    <div className='service-detail-stat'>
                      <div className='service-detail-stat-value'>
                        {formatDuration(resultStats.p95Latency!)}
                      </div>
                      <div className='service-detail-stat-label'>
                        {t('service_detail.stats.p95')}
                      </div>
                    </div>
                    <div className='service-detail-stat'>
                      <div className='service-detail-stat-value'>
                        {formatDuration(resultStats.minLatency!)}
                      </div>
                      <div className='service-detail-stat-label'>
                        {t('service_detail.stats.min')}
                      </div>
                    </div>
                    <div className='service-detail-stat'>
                      <div className='service-detail-stat-value'>
                        {formatDuration(resultStats.maxLatency!)}
                      </div>
                      <div className='service-detail-stat-label'>
                        {t('service_detail.stats.max')}
                      </div>
                    </div>
                  </>
                )}
              </>
            )}
          </div>
        )}

        {/* HTTP: where the last check's latency went. A cold connection or a
            hidden redirect inflates the total, which otherwise reads as a slow server. */}
        {service.type === 'http' &&
          resultStats?.httpPhases &&
          (() => {
            const p = resultStats.httpPhases;
            const segments = [
              { key: 'dns', ms: p.dns, color: 'var(--http-phase-dns)' },
              { key: 'connect', ms: p.connect, color: 'var(--http-phase-connect)' },
              { key: 'tls', ms: p.tls, color: 'var(--http-phase-tls)' },
              { key: 'server', ms: p.server, color: 'var(--http-phase-server)' },
            ].filter((s) => s.ms > 0);
            const totalMs = segments.reduce((a, s) => a + s.ms, 0);
            if (totalMs <= 0) return null;
            const pooled = p.dns === 0 && p.connect === 0 && p.tls === 0;

            return (
              <div className='service-detail-phases'>
                <div className='service-detail-phases-head'>
                  <span className='service-detail-phases-title'>
                    {t('service_detail.http_phases.title')}
                  </span>
                  {p.redirects > 0 && (
                    <span className='service-detail-phases-redirect'>
                      <HiOutlineArrowRight size={14} />
                      {t('service_detail.http_phases.redirects', {
                        count: p.redirects,
                      })}
                      {p.finalUrl && (
                        <code title={p.finalUrl}>{p.finalUrl}</code>
                      )}
                    </span>
                  )}
                </div>
                <div className='service-detail-phases-bar'>
                  {segments.map((s) => (
                    <div
                      key={s.key}
                      className='service-detail-phases-seg'
                      style={{
                        flexGrow: s.ms,
                        background: s.color,
                      }}
                      title={`${t(`service_detail.http_phases.${s.key}`)} ${formatDuration(s.ms)}`}
                    />
                  ))}
                </div>
                {/* Values are labelled inline: identity never rests on color alone. */}
                <div className='service-detail-phases-legend'>
                  {segments.map((s) => (
                    <span key={s.key} className='service-detail-phases-item'>
                      <span
                        className='service-detail-phases-dot'
                        style={{ background: s.color }}
                      />
                      {t(`service_detail.http_phases.${s.key}`)}
                      <span className='service-detail-phases-value'>
                        {formatDuration(s.ms)}
                      </span>
                    </span>
                  ))}
                  {pooled && (
                    <span className='service-detail-phases-item'>
                      {t('service_detail.http_phases.reused')}
                    </span>
                  )}
                </div>
              </div>
            );
          })()}

        {/* Certificate: dedicated expiration block instead of the (meaningless) latency chart */}
        {results.length > 0 && service.type === 'certificate' && (
          <CertificateExpirationCard
            service={service}
            results={displayResults}
          />
        )}

        {/* Chart: Timeline or Graph */}
        {results.length > 0 && service.type !== 'certificate' && (
          <div className='card'>
            <div
              className='card-title'
              style={{
                display: 'flex',
                justifyContent: 'space-between',
                alignItems: 'center',
              }}>
              <span
                style={{
                  display: 'flex',
                  alignItems: 'center',
                  gap: '0.5rem',
                  flexWrap: 'wrap',
                }}>
                <HiOutlineChartBar className='card-title-icon' />
                {t('service_detail.chart.check_history_title', {
                  mode: chartModeLabel,
                  count: results.length,
                })}
                {(() => {
                  const unit = AGENT_PERCENT_TYPES.includes(service.type)
                    ? '%'
                    : 'ms';
                  const parts: string[] = [];
                  if (service.warning_threshold != null) {
                    parts.push(
                      `${t('service_modal.labels.threshold_warning')} > ${service.warning_threshold}${unit}`,
                    );
                  }
                  if (service.critical_threshold != null) {
                    parts.push(
                      `${t('service_modal.labels.threshold_critical')} > ${service.critical_threshold}${unit}`,
                    );
                  }
                  if (parts.length === 0) return null;
                  // The network chart plots throughput, so name the series the
                  // thresholds actually apply to instead of implying it is this one.
                  const scope =
                    service.type === 'agent_network'
                      ? `${t('service_detail.stats.ping_latency')} — `
                      : '';
                  return (
                    <span className='service-detail-threshold-hint'>
                      {scope}
                      {parts.join(' · ')}
                    </span>
                  );
                })()}
                {resultsLoading && (
                  <span className='service-detail-loading-badge'>
                    {t('common.loading')}
                  </span>
                )}
              </span>
              <div className='service-detail-chart-toggle'>
                <button
                  className={`service-detail-chart-toggle-btn ${chartMode === 'graph' ? 'active' : ''}`}
                  onClick={() => setChartMode('graph')}
                  title={t('service_detail.chart.graph')}>
                  <HiOutlineChartBarSquare />
                  {t('service_detail.chart.graph')}
                </button>
                <button
                  className={`service-detail-chart-toggle-btn ${chartMode === 'timeline' ? 'active' : ''}`}
                  onClick={() => setChartMode('timeline')}
                  title={t('service_detail.chart.timeline')}>
                  <HiOutlineChartBar />
                  {t('service_detail.chart.timeline')}
                </button>
              </div>
            </div>

            {chartMode === 'timeline' && (
              <>
                {results.length > MAX_TIMELINE_BARS && (
                  <div className='service-detail-aggregation-hint'>
                    {t('service_detail.chart.aggregation_hint', {
                      total: results.length,
                      bars: Math.min(MAX_TIMELINE_BARS, results.length),
                    })}
                  </div>
                )}
                <div className='service-detail-timeline'>
                  {(() => {
                    const buckets = bucketResults(displayResults, t);
                    const isAgentUtil =
                      service.type === 'agent_disk' ||
                      service.type === 'agent_cpu' ||
                      service.type === 'agent_ram';
                    const hasUtilization =
                      isAgentUtil &&
                      buckets.some((b) => b.result.metric_value != null);
                    const hasLatency =
                      !isAgentUtil &&
                      buckets.some(
                        (b) => b.result.latency != null && b.result.latency > 0,
                      );
                    const maxLat = resultStats?.maxLatency || 1;

                    return buckets.map((b, i) => {
                      const r = b.result;
                      let height: number;
                      if (hasUtilization && r.metric_value != null) {
                        height = Math.max(8, Math.min(100, r.metric_value));
                      } else if (hasLatency && r.latency) {
                        height = Math.max(8, (r.latency / maxLat) * 100);
                      } else {
                        height = hasLatency ? 8 : 100;
                      }

                      return (
                        <div
                          key={r.id || i}
                          className={`service-detail-timeline-bar service-detail-timeline-bar-${r.status === 'success' ? 'success' : r.status === 'warning' ? 'warning' : 'failure'}${selectedResult?.id === r.id ? ' service-detail-timeline-bar-active' : ''}`}
                          style={{ height: `${height}%`, cursor: 'pointer' }}
                          onClick={() => setSelectedResult(r)}
                        />
                      );
                    });
                  })()}
                </div>
              </>
            )}

            {chartMode === 'graph' && (
              <LatencyGraph
                results={displayResults}
                metricMode={
                  service.type === 'agent_disk' ||
                  service.type === 'agent_cpu' ||
                  service.type === 'agent_ram'
                    ? 'utilization'
                    : 'latency'
                }
                service={service}
                onPointClick={(r) => setSelectedResult(r)}
              />
            )}

            <div className='service-detail-timeline-labels'>
              {results.length > 0 && (
                <>
                  <span>
                    {getTimeAgo(results[results.length - 1].timestamp, t)}
                  </span>
                  <span>{t('overview.time.now')}</span>
                </>
              )}
            </div>
          </div>
        )}

        {/* Check result detail modal */}
        {selectedResult && (
          <div
            className='check-result-modal-backdrop'
            onClick={() => setSelectedResult(null)}>
            <div
              className='check-result-modal'
              onClick={(e) => e.stopPropagation()}>
              <div className='check-result-modal-header'>
                <div
                  className={`service-detail-tooltip-status service-detail-tooltip-status-${selectedResult.status}`}>
                  {getResultStatusLabel(selectedResult.status, t)}
                </div>
                <button
                  className='btn-icon'
                  onClick={() => setSelectedResult(null)}>
                  <HiOutlineXMark />
                </button>
              </div>
              <div
                className='service-detail-tooltip-time'
                style={{ marginBottom: '0.75rem' }}>
                {formatTimestamp(selectedResult.timestamp, i18n.language)}
              </div>

              {service.type === 'agent_disk' && (
                <>
                  {selectedResult.metric_value != null && (
                    <div className='service-detail-tooltip-row'>
                      <span className='service-detail-tooltip-label'>
                        {t('service_detail.stats.utilization')}
                      </span>
                      <span className='service-detail-tooltip-value'>
                        {Math.round(selectedResult.metric_value)}%
                      </span>
                    </div>
                  )}
                  {selectedResult.metadata &&
                    (() => {
                      try {
                        const meta = JSON.parse(selectedResult.metadata) as {
                          disk_total_gb?: number;
                          disk_free_gb?: number;
                        };
                        const total = meta.disk_total_gb;
                        const util = selectedResult.metric_value ?? 0;
                        const freeFromMeta = meta.disk_free_gb;
                        const free =
                          freeFromMeta != null
                            ? freeFromMeta
                            : total != null
                              ? (total * (100 - util)) / 100
                              : null;
                        return (
                          <>
                            {free != null && (
                              <div className='service-detail-tooltip-row'>
                                <span className='service-detail-tooltip-label'>
                                  {t('service_detail.stats.free_space')}
                                </span>
                                <span className='service-detail-tooltip-value'>
                                  {formatStorageGb(free, t)}
                                </span>
                              </div>
                            )}
                            {total != null && (
                              <div className='service-detail-tooltip-row'>
                                <span className='service-detail-tooltip-label'>
                                  {t('service_detail.stats.total')}
                                </span>
                                <span className='service-detail-tooltip-value'>
                                  {formatStorageGb(total, t)}
                                </span>
                              </div>
                            )}
                          </>
                        );
                      } catch {
                        return null;
                      }
                    })()}
                </>
              )}

              {service.type === 'agent_cpu' && (
                <>
                  {selectedResult.metric_value != null && (
                    <div className='service-detail-tooltip-row'>
                      <span className='service-detail-tooltip-label'>
                        {t('service_detail.stats.utilization')}
                      </span>
                      <span className='service-detail-tooltip-value'>
                        {Math.round(selectedResult.metric_value)}%
                      </span>
                    </div>
                  )}
                  {selectedResult.metadata &&
                    (() => {
                      try {
                        const meta = JSON.parse(selectedResult.metadata) as {
                          load_1min?: number;
                          load_5min?: number;
                          load_15min?: number;
                        };
                        const hasLoad =
                          meta.load_1min != null ||
                          meta.load_5min != null ||
                          meta.load_15min != null;
                        if (!hasLoad) return null;
                        return (
                          <>
                            {meta.load_1min != null && (
                              <div className='service-detail-tooltip-row'>
                                <span className='service-detail-tooltip-label'>
                                  {t('service_detail.stats.load_1min')}
                                </span>
                                <span className='service-detail-tooltip-value'>
                                  {meta.load_1min.toFixed(2)}
                                </span>
                              </div>
                            )}
                            {meta.load_5min != null && (
                              <div className='service-detail-tooltip-row'>
                                <span className='service-detail-tooltip-label'>
                                  {t('service_detail.stats.load_5min')}
                                </span>
                                <span className='service-detail-tooltip-value'>
                                  {meta.load_5min.toFixed(2)}
                                </span>
                              </div>
                            )}
                            {meta.load_15min != null && (
                              <div className='service-detail-tooltip-row'>
                                <span className='service-detail-tooltip-label'>
                                  {t('service_detail.stats.load_15min')}
                                </span>
                                <span className='service-detail-tooltip-value'>
                                  {meta.load_15min.toFixed(2)}
                                </span>
                              </div>
                            )}
                          </>
                        );
                      } catch {
                        return null;
                      }
                    })()}
                </>
              )}

              {service.type === 'agent_ram' && (
                <>
                  {selectedResult.metric_value != null && (
                    <div className='service-detail-tooltip-row'>
                      <span className='service-detail-tooltip-label'>
                        {t('service_detail.stats.utilization')}
                      </span>
                      <span className='service-detail-tooltip-value'>
                        {Math.round(selectedResult.metric_value)}%
                      </span>
                    </div>
                  )}
                  {selectedResult.metadata &&
                    (() => {
                      try {
                        const meta = JSON.parse(selectedResult.metadata) as {
                          ram_total_gb?: number;
                        };
                        const total = meta.ram_total_gb;
                        const util = selectedResult.metric_value ?? 0;
                        if (total == null) return null;
                        const freeGb = (total * (100 - util)) / 100;
                        return (
                          <>
                            <div className='service-detail-tooltip-row'>
                              <span className='service-detail-tooltip-label'>
                                {t('service_detail.stats.total')}
                              </span>
                              <span className='service-detail-tooltip-value'>
                                {formatStorageGb(total, t)}
                              </span>
                            </div>
                            <div className='service-detail-tooltip-row'>
                              <span className='service-detail-tooltip-label'>
                                {t('service_detail.stats.free_memory')}
                              </span>
                              <span className='service-detail-tooltip-value'>
                                {formatStorageGb(freeGb, t)}
                              </span>
                            </div>
                          </>
                        );
                      } catch {
                        return null;
                      }
                    })()}
                </>
              )}

              {service.type === 'agent_network' &&
                selectedResult.metadata &&
                (() => {
                  try {
                    const meta = JSON.parse(selectedResult.metadata) as {
                      network_speed_in_mb_per_s?: number;
                      network_speed_out_mb_per_s?: number;
                      network_bytes_in_total?: number;
                      network_bytes_out_total?: number;
                    };
                    const si = meta.network_speed_in_mb_per_s;
                    const so = meta.network_speed_out_mb_per_s;
                    const bi = meta.network_bytes_in_total;
                    const bo = meta.network_bytes_out_total;
                    const hasAny =
                      si != null || so != null || bi != null || bo != null;
                    if (!hasAny) return null;
                    const fmt = (v: number) => formatBytes(v, t);
                    return (
                      <>
                        {si != null && (
                          <div className='service-detail-tooltip-row'>
                            <span className='service-detail-tooltip-label'>
                              {t('service_detail.stats.speed_in')}
                            </span>
                            <span className='service-detail-tooltip-value'>
                              {si.toFixed(2)} MB/s
                            </span>
                          </div>
                        )}
                        {so != null && (
                          <div className='service-detail-tooltip-row'>
                            <span className='service-detail-tooltip-label'>
                              {t('service_detail.stats.speed_out')}
                            </span>
                            <span className='service-detail-tooltip-value'>
                              {so.toFixed(2)} MB/s
                            </span>
                          </div>
                        )}
                        {bi != null && (
                          <div className='service-detail-tooltip-row'>
                            <span className='service-detail-tooltip-label'>
                              {t('service_detail.stats.received_total')}
                            </span>
                            <span className='service-detail-tooltip-value'>
                              {fmt(bi)}
                            </span>
                          </div>
                        )}
                        {bo != null && (
                          <div className='service-detail-tooltip-row'>
                            <span className='service-detail-tooltip-label'>
                              {t('service_detail.stats.sent_total')}
                            </span>
                            <span className='service-detail-tooltip-value'>
                              {fmt(bo)}
                            </span>
                          </div>
                        )}
                      </>
                    );
                  } catch {
                    return null;
                  }
                })()}

              {service.type === 'http' && (
                <>
                  {selectedResult.latency != null && (
                    <div className='service-detail-tooltip-row'>
                      <span className='service-detail-tooltip-label'>
                        {t('service_detail.stats.response_time')}
                      </span>
                      <span className='service-detail-tooltip-value'>
                        {formatDuration(selectedResult.latency)}
                      </span>
                    </div>
                  )}
                </>
              )}

              {![
                'agent_disk',
                'agent_cpu',
                'agent_ram',
                'agent_network',
                'http',
              ].includes(service.type) && (
                <>
                  {selectedResult.latency != null && (
                    <div className='service-detail-tooltip-row'>
                      <span className='service-detail-tooltip-label'>
                        {t('service_detail.stats.latency')}
                      </span>
                      <span className='service-detail-tooltip-value'>
                        {formatDuration(selectedResult.latency)}
                      </span>
                    </div>
                  )}
                </>
              )}

              {service.type === 'certificate' &&
                selectedResult.metadata &&
                (() => {
                  try {
                    const meta = JSON.parse(selectedResult.metadata) as {
                      expires_at?: string;
                    };
                    if (!meta.expires_at) return null;
                    const expiresAt = new Date(meta.expires_at);
                    return (
                      <div className='service-detail-tooltip-row'>
                        <span className='service-detail-tooltip-label'>
                          {t('metrics_view.labels.expiration_date')}
                        </span>
                        <span className='service-detail-tooltip-value'>
                          {expiresAt.toLocaleDateString(i18n.language, {
                            day: 'numeric',
                            month: 'long',
                            year: 'numeric',
                          })}
                        </span>
                      </div>
                    );
                  } catch {
                    return null;
                  }
                })()}

              {selectedResult.message && (
                <CheckResultMessage message={selectedResult.message} />
              )}

              {(selectedResult.status === 'failure' ||
                selectedResult.status === 'warning') && (
                <div className='check-result-modal-actions'>
                  <button
                    className='btn btn-outline'
                    onClick={() => {
                      fetchRca(selectedResult);
                      setSelectedResult(null);
                    }}
                    disabled={rcaLoading}>
                    {t('service_detail.analyze_cause')}
                  </button>
                </div>
              )}
            </div>
          </div>
        )}

        {/* RCA Section */}
        {selectedResultForRca && (
          <div className='card service-rca-card'>
            <div className='card-header service-rca-header'>
              <h3 className='card-title service-rca-title'>
                <HiOutlineExclamationTriangle />
                {t('service_detail.rca.title', {
                  timestamp: formatTimestamp(
                    selectedResultForRca.timestamp,
                    i18n.language,
                  ),
                })}
              </h3>
              <button
                className='btn-icon'
                onClick={() => setSelectedResultForRca(null)}>
                <HiOutlineXMark />
              </button>
            </div>
            <div className='card-body'>
              <p className='service-rca-message'>
                <strong>{t('service_detail.rca.message')}</strong>{' '}
                {selectedResultForRca.message}
              </p>

              {isPro ? (
                <ExplainPanel
                  key={`explain-sr-${selectedResultForRca.id}`}
                  fetchExplanationSSE={(force) =>
                    orgApi.services.explainResultSSE(
                      selectedResultForRca.id,
                      force,
                    )
                  }
                  autoRun
                />
              ) : (
                <ProUpgradeBanner feature='Root Cause Analysis (RCA)' />
              )}

              {rcaLoading ? (
                <div className='service-rca-loading'>
                  {t('service_detail.rca.searching')}
                </div>
              ) : rcaResult && rcaResult.has_correlation ? (
                <div className='service-rca-correlation'>
                  <p className='service-rca-summary'>{rcaResult.summary}</p>

                  {rcaResult.infra && rcaResult.infra.length > 0 && (
                    <div className='service-rca-block'>
                      <strong>
                        {t('service_detail.rca.infrastructure', {
                          host:
                            rcaResult.host_name || t('services.stats.unknown'),
                        })}
                      </strong>
                      <ul>
                        {rcaResult.infra.map((inf, i) => (
                          <li key={i}>{inf.description}</li>
                        ))}
                      </ul>
                    </div>
                  )}

                  {rcaResult.services && rcaResult.services.length > 0 && (
                    <div className='service-rca-block'>
                      <strong>
                        {t('service_detail.rca.impacted_services')}
                      </strong>
                      <ul>
                        {rcaResult.services.map((svc, i) => (
                          <li key={i}>
                            <Link to={orgPath(`/services/${svc.service_id}`)}>
                              {svc.service_name}
                            </Link>{' '}
                            : {svc.description}
                          </li>
                        ))}
                      </ul>
                    </div>
                  )}

                  {/* Neighbours that only slowed down: a saturated host often
                      leaves latency as its single visible trace. */}
                  {rcaResult.degraded && rcaResult.degraded.length > 0 && (
                    <div className='service-rca-block'>
                      <strong>{t('service_detail.rca.degraded_services')}</strong>
                      <ul>
                        {rcaResult.degraded.map((d) => (
                          <li key={d.service_id}>
                            <Link to={orgPath(`/services/${d.service_id}`)}>
                              {t('service_detail.rca.degraded_item', {
                                name: d.host_name
                                  ? `${d.service_name} (${d.host_name})`
                                  : d.service_name,
                                latency: Math.round(d.latency_ms),
                                baseline: Math.round(d.baseline_ms),
                                multiplier: d.multiplier.toFixed(1),
                              })}
                            </Link>
                          </li>
                        ))}
                      </ul>
                    </div>
                  )}
                </div>
              ) : null}
            </div>
          </div>
        )}

        {/* Button to trigger RCA from tooltip can be done by capturing click on tooltip or just a list of recent errors */}
        {aggregatedFailures.length > 0 && (
          <div className='card' style={{ marginTop: '1.5rem' }}>
            <div className='card-header'>
              <h3 className='card-title'>
                {t('service_detail.recent_failures')}
              </h3>
            </div>
            <div className='card-body'>
              <div
                style={{
                  display: 'flex',
                  flexDirection: 'column',
                  gap: '0.5rem',
                }}>
                {aggregatedFailures.slice(0, 5).map((g) => (
                  <div
                    key={g.message}
                    style={{
                      display: 'flex',
                      flexWrap: 'wrap',
                      justifyContent: 'space-between',
                      alignItems: 'center',
                      gap: '0.75rem 1rem',
                      padding: '0.75rem',
                      background: 'var(--bg-secondary)',
                      borderRadius: '6px',
                    }}>
                    <div style={{ minWidth: 0, flex: '1 1 200px' }}>
                      <div
                        style={{
                          fontWeight: 500,
                          color: 'var(--status-error)',
                          overflow: 'hidden',
                          textOverflow: 'ellipsis',
                          whiteSpace: 'nowrap',
                        }}
                        title={g.message}>
                        {g.message || '—'}
                      </div>
                      <div
                        style={{
                          fontSize: '0.75rem',
                          color: 'var(--text-tertiary)',
                          marginTop: '0.25rem',
                        }}>
                        {t('service_detail.last_seen', {
                          time: formatTimestamp(g.lastSeen, i18n.language),
                        })}
                      </div>
                    </div>
                    <div style={{ width: 96, height: 28, flexShrink: 0 }}>
                      <FailureSparkline data={g.spark} />
                    </div>
                    <div
                      style={{
                        fontSize: '0.875rem',
                        fontWeight: 600,
                        color: 'var(--status-error)',
                        whiteSpace: 'nowrap',
                      }}>
                      {t('service_detail.occurrence_count', { count: g.count })}
                    </div>
                    <button
                      className='btn btn-outline'
                      onClick={() => fetchRca(g.latest)}
                      disabled={
                        rcaLoading && selectedResultForRca?.id === g.latest.id
                      }>
                      {t('service_detail.analyze_cause')}
                    </button>
                  </div>
                ))}
              </div>
            </div>
          </div>
        )}
      </div>
      {showDowntimeModal && service && (
        <ScheduleDowntimeModal
          targetType='service'
          targetId={service.id}
          targetName={service.name}
          onClose={() => setShowDowntimeModal(false)}
          onCreated={refresh}
        />
      )}
      {showDeleteConfirm && service && (
        <ConfirmDialog
          title={t('common.delete')}
          message={t('metrics_view.delete_service_confirm', {
            name: service.name,
          })}
          onConfirm={handleDelete}
          onCancel={() => setShowDeleteConfirm(false)}
        />
      )}
    </>
  );
}

export default ServiceDetailView;
