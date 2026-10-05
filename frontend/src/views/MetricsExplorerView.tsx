import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { keepPreviousData, useQuery } from '@tanstack/react-query';
import {
  HiOutlineChartBar,
  HiOutlineCpuChip,
  HiOutlineCircleStack,
  HiOutlineClock,
  HiOutlineMagnifyingGlass,
  HiOutlineCalendarDays,
  HiOutlineServerStack,
} from 'react-icons/hi2';
import { useOrgApi, useOrgQueryScope } from '../hooks/useOrgApi';
import { useUrlState } from '../hooks/useUrlState';
import { useQueryRefresh } from '../hooks/useQueryRefresh';
import { RefreshControl } from '../components/RefreshControl';
import type { SystemMetric } from '../api';
import { useDateRange } from '../contexts/DateRangeContext';
import { useDateRangeBounds, useDateRangeKey } from '../hooks/useDateRangeBounds';
import { DateRangePicker } from '../components/DateRangePicker';
import { CustomMetricsExplorer } from '../components/CustomMetricsExplorer';
import './MetricsExplorerView.css';

function MetricsExplorerView() {
  const { t } = useTranslation();
  const orgApi = useOrgApi();
  const scope = useOrgQueryScope();
  const { dateRange, setDateRange, formatDateRangeLabel } = useDateRange();
  const getDateBounds = useDateRangeBounds();
  const dateRangeKey = useDateRangeKey();
  const [urlState, setFilter] = useUrlState({ service: '', host: '' });
  const selectedService = urlState.service;
  const selectedHost = urlState.host;
  const [calendarOpen, setCalendarOpen] = useState(false);
  const [activeMetric, setActiveMetric] = useState<'cpu' | 'ram' | 'latency'>('cpu');

  // The metrics view a user reaches at /metrics, so the 10s default the
  // criterion names for Metrics applies here.
  const { refetchInterval, buildControl } = useQueryRefresh('metrics-explorer', 10000);

  const metricsQueryKey = [scope, 'metrics-explorer', selectedService, selectedHost, dateRangeKey];
  const metricsQuery = useQuery({
    queryKey: metricsQueryKey,
    queryFn: async () => {
      const { startIso: start, endIso: end } = getDateBounds();
      const res = await orgApi.metricsExplorer.get({
        service: selectedService || undefined,
        host: selectedHost || undefined,
        start,
        end,
      });
      return {
        metrics: (res.data.metrics as SystemMetric[]) || [],
        filters: (res.data.filters as { service: string; host: string }[]) || [],
      };
    },
    // Changing a filter keeps the current chart on screen while the next loads.
    placeholderData: keepPreviousData,
    refetchInterval,
  });

  const metrics = metricsQuery.data?.metrics ?? [];
  const filters = metricsQuery.data?.filters ?? [];

  const autoRefresh = buildControl({
    query: metricsQuery,
    queryKey: metricsQueryKey,
    prefix: [scope, 'metrics-explorer'],
  });

  // Compute stats (only from points that have the metric)
  const cpuPoints = metrics.filter(m => m.cpu_perc != null && m.cpu_perc > 0);
  const ramPoints = metrics.filter(m => m.ram_perc != null && m.ram_perc > 0);
  const latencyPoints = metrics.filter(m => m.http_latency != null && m.http_latency > 0);
  const latestMetrics = metrics.slice(-1)[0];
  const avgCPU = cpuPoints.length > 0 ? cpuPoints.reduce((s, m) => s + m.cpu_perc, 0) / cpuPoints.length : 0;
  const avgRAM = ramPoints.length > 0 ? ramPoints.reduce((s, m) => s + m.ram_perc, 0) / ramPoints.length : 0;
  const avgLatency = latencyPoints.length > 0
    ? latencyPoints.reduce((s, m) => s + (m.http_latency ?? 0), 0) / latencyPoints.length
    : 0;

  // Unique services, envs, hosts from filters
  const uniqueServices = [...new Set(filters.map(f => f.service).filter(Boolean))].sort();
  const uniqueHosts = [...new Set(filters.map(f => f.host).filter(Boolean))].sort();

  // Context for display: HOST first (the key differentiator for cpu/ram from multiple machines)
  const metricsContext = selectedHost
    ? t('metrics_explorer.context.host_prefix', {
        details: `${selectedHost}${selectedService ? ` · ${selectedService}` : ''}`,
      })
    : selectedService
        ? selectedService
        : metrics.length > 0
            ? t('metrics_explorer.context.hosts_count', {
                count: uniqueHosts.length,
                hosts: `${uniqueHosts.slice(0, 3).join(', ')}${uniqueHosts.length > 3 ? '…' : ''}`,
              })
            : '';

  // Averages the series that share a timestamp: the chart draws a single line,
  // so several hosts/services would otherwise be stitched into one zigzag.
  const averagePerTimestamp = (
    points: SystemMetric[],
    value: (m: SystemMetric) => number,
  ): { ts: string; value: number }[] => {
    const buckets = new Map<string, { sum: number; n: number }>();
    for (const m of points) {
      const b = buckets.get(m.timestamp) ?? { sum: 0, n: 0 };
      b.sum += value(m);
      b.n += 1;
      buckets.set(m.timestamp, b);
    }
    return [...buckets.entries()]
      .sort(([a], [b]) => (a < b ? -1 : 1))
      .map(([ts, b]) => ({ ts, value: b.sum / b.n }));
  };

  // Get data series for chart (only points that have the selected metric)
  const getChartData = (): { ts: string; value: number }[] => {
    switch (activeMetric) {
      case 'cpu':
        return averagePerTimestamp(cpuPoints, m => m.cpu_perc);
      case 'ram':
        return averagePerTimestamp(ramPoints, m => m.ram_perc);
      case 'latency':
        return averagePerTimestamp(latencyPoints, m => m.http_latency!);
      default:
        return [];
    }
  };

  const chartData = getChartData();

  return (
    <div className="metrics-explorer-view">
      <div className="metrics-explorer-header">
        <div>
          <h1><HiOutlineChartBar /> {t('metrics_explorer.title')}</h1>
          <p className="subtitle">{t('metrics_explorer.subtitle')}</p>
        </div>
        <RefreshControl control={autoRefresh} />
      </div>

      {/* Filters */}
      <div className="metrics-explorer-filters">
        <div className="filter-group">
          <HiOutlineMagnifyingGlass />
          <select value={selectedHost} onChange={e => setFilter({ host: e.target.value })} title={t('metrics_explorer.filters.host_title')}>
            <option value="">{t('metrics_explorer.filters.all_hosts')}</option>
            {uniqueHosts.map(h => <option key={h} value={h}>{h}</option>)}
          </select>
          <select value={selectedService} onChange={e => setFilter({ service: e.target.value })}>
            <option value="">{t('metrics_explorer.filters.all_services')}</option>
            {uniqueServices.map(s => <option key={s} value={s}>{s}</option>)}
          </select>
        </div>
        <button
          className="date-range-trigger"
          onClick={() => setCalendarOpen(true)}
        >
          <HiOutlineCalendarDays />
          {formatDateRangeLabel(dateRange, { withTime: true })}
        </button>
      </div>

      <DateRangePicker
        isOpen={calendarOpen}
        value={dateRange}
        onChange={setDateRange}
        onClose={() => setCalendarOpen(false)}
        showTime
      />

      {/* Context banner */}
      {metricsContext && (
        <div className="metrics-context-banner">
          <span className="metrics-context-label">{t('metrics_explorer.context.data_from')}</span>
          <span className="metrics-context-value">{metricsContext}</span>
        </div>
      )}

      {/* Stats Cards */}
      <div className="metrics-stats-cards">
        <button className={`metrics-stat-card ${activeMetric === 'cpu' ? 'active' : ''}`} onClick={() => setActiveMetric('cpu')}>
          <div className="metrics-stat-icon cpu"><HiOutlineCpuChip /></div>
          <div>
            <div className="metrics-stat-value">{avgCPU.toFixed(1)}%</div>
            <div className="metrics-stat-label">{t('metrics_explorer.stats.avg_cpu')}</div>
          </div>
        </button>
        <button className={`metrics-stat-card ${activeMetric === 'ram' ? 'active' : ''}`} onClick={() => setActiveMetric('ram')}>
          <div className="metrics-stat-icon ram"><HiOutlineCircleStack /></div>
          <div>
            <div className="metrics-stat-value">{avgRAM.toFixed(1)}%</div>
            <div className="metrics-stat-label">{t('metrics_explorer.stats.avg_ram')}</div>
          </div>
        </button>
        <button className={`metrics-stat-card ${activeMetric === 'latency' ? 'active' : ''}`} onClick={() => setActiveMetric('latency')}>
          <div className="metrics-stat-icon latency"><HiOutlineClock /></div>
          <div>
            <div className="metrics-stat-value">{avgLatency.toFixed(0)}ms</div>
            <div className="metrics-stat-label">{t('metrics_explorer.stats.http_latency')}</div>
          </div>
        </button>
      </div>

      {/* Chart */}
      <div className="metrics-chart-card">
        <div className="metrics-chart-header">
          <h3>
            {activeMetric === 'cpu' ? t('metrics_explorer.chart.cpu_usage') : activeMetric === 'ram' ? t('metrics_explorer.chart.ram_usage') : t('metrics_explorer.chart.http_latency')}
            {metricsContext && <span className="metrics-chart-context"> — {metricsContext}</span>}
          </h3>
          <span className="metrics-chart-count">{t('metrics_explorer.chart.points', { count: chartData.length })}</span>
        </div>
        {metricsQuery.isPending ? (
          <div className="metrics-chart-loading">{t('common.loading')}</div>
        ) : chartData.length === 0 ? (
          <div className="metrics-chart-empty">{t('metrics_explorer.chart.no_data')}</div>
        ) : (
          <MetricsChart data={chartData} unit={activeMetric === 'latency' ? 'ms' : '%'} color={activeMetric === 'cpu' ? '#6366f1' : activeMetric === 'ram' ? '#8b5cf6' : '#f59e0b'} />
        )}
      </div>

      {/* Custom metric series (labelled metrics ingested over OTLP) */}
      <CustomMetricsExplorer />

      {/* Latest values table */}
      {latestMetrics && (
        <div className="metrics-latest-card">
          <h3>{t('metrics_explorer.latest.title')}</h3>
          <div className="metrics-latest-grid">
            {latestMetrics.host && <div><span className="label"><HiOutlineServerStack /> {t('metrics_explorer.latest.host')}</span><span className="value">{latestMetrics.host}</span></div>}
            <div><span className="label">{t('metrics_explorer.latest.service')}</span><span className="value">{latestMetrics.service}</span></div>
            <div><span className="label">{t('metrics_explorer.latest.cpu')}</span><span className="value">{latestMetrics.cpu_perc > 0 ? `${latestMetrics.cpu_perc.toFixed(1)}%` : '—'}</span></div>
            <div><span className="label">{t('metrics_explorer.latest.ram')}</span><span className="value">{latestMetrics.ram_perc > 0 ? `${latestMetrics.ram_perc.toFixed(1)}%` : '—'}</span></div>
            <div><span className="label">{t('metrics_explorer.latest.latency')}</span><span className="value">{latestMetrics.http_latency != null && latestMetrics.http_latency > 0 ? `${latestMetrics.http_latency.toFixed(0)}ms` : '—'}</span></div>
            <div><span className="label">{t('metrics_explorer.latest.timestamp')}</span><span className="value">{new Date(latestMetrics.timestamp).toLocaleString()}</span></div>
          </div>
        </div>
      )}
    </div>
  );
}

// SVG chart component
function MetricsChart({ data, unit, color }: { data: { ts: string; value: number }[]; unit: string; color: string }) {
  const width = 900;
  const height = 250;
  const padding = { top: 20, right: 40, bottom: 30, left: 60 };

  const values = data.map(d => d.value);
  const dataMin = Math.min(...values);
  const dataMax = Math.max(...values);
  const range = dataMax - dataMin || 1;
  const minVal = dataMin - range * 0.1;
  const maxVal = dataMax + range * 0.1;

  const xScale = (i: number) => padding.left + (i / (data.length - 1 || 1)) * (width - padding.left - padding.right);
  const yScale = (v: number) => padding.top + (1 - (v - minVal) / (maxVal - minVal)) * (height - padding.top - padding.bottom);

  const points = data.map((d, i) => `${xScale(i)},${yScale(d.value)}`).join(' ');
  const areaPoints = `${xScale(0)},${height - padding.bottom} ${points} ${xScale(data.length - 1)},${height - padding.bottom}`;

  // Y-axis ticks
  const yTicks = Array.from({ length: 5 }, (_, i) => minVal + (i / 4) * (maxVal - minVal));

  const [tooltip, setTooltip] = useState<{ x: number; y: number; value: number; ts: string } | null>(null);

  return (
    <svg viewBox={`0 0 ${width} ${height}`} className="metrics-svg-chart">
      <defs>
        <linearGradient id={`grad-${color}`} x1="0" y1="0" x2="0" y2="1">
          <stop offset="0%" stopColor={color} stopOpacity="0.3" />
          <stop offset="100%" stopColor={color} stopOpacity="0.02" />
        </linearGradient>
      </defs>
      {/* Grid */}
      {yTicks.map((tick, i) => (
        <g key={i}>
          <line x1={padding.left} y1={yScale(tick)} x2={width - padding.right} y2={yScale(tick)} stroke="var(--border-primary)" strokeDasharray="4" />
          <text x={padding.left - 8} y={yScale(tick) + 4} textAnchor="end" fill="var(--text-tertiary)" fontSize="11">{tick.toFixed(unit === 'ms' ? 0 : 1)}{unit}</text>
        </g>
      ))}
      {/* Area */}
      <polygon points={areaPoints} fill={`url(#grad-${color})`} />
      {/* Line */}
      <polyline points={points} fill="none" stroke={color} strokeWidth="2" strokeLinejoin="round" />
      {/* Data points (interactive) */}
      {data.map((d, i) => (
        <circle
          key={i}
          cx={xScale(i)}
          cy={yScale(d.value)}
          r={data.length > 200 ? 1 : 3}
          fill={color}
          opacity={0.8}
          onMouseEnter={() => setTooltip({ x: xScale(i), y: yScale(d.value), value: d.value, ts: d.ts })}
          onMouseLeave={() => setTooltip(null)}
          style={{ cursor: 'pointer' }}
        />
      ))}
      {/* Tooltip */}
      {tooltip && (
        <g>
          <rect x={tooltip.x - 60} y={tooltip.y - 40} width={120} height={30} rx={4} fill="var(--surface-primary)" stroke="var(--border-primary)" />
          <text x={tooltip.x} y={tooltip.y - 20} textAnchor="middle" fill="var(--text-primary)" fontSize="12" fontWeight="600">{tooltip.value.toFixed(unit === 'ms' ? 0 : 1)}{unit} — {new Date(tooltip.ts).toLocaleTimeString()}</text>
        </g>
      )}
    </svg>
  );
}

export default MetricsExplorerView;
