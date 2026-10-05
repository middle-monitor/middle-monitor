import { useState } from 'react';
import { Link } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import {
  HiOutlineGlobeAlt,
  HiOutlineServerStack,
  HiOutlineCalendarDays,
} from 'react-icons/hi2';
import { keepPreviousData, useQuery } from '@tanstack/react-query';
import { useOrgApi, useOrgQueryScope } from '../hooks/useOrgApi';
import { useUrlState } from '../hooks/useUrlState';
import { useQueryRefresh } from '../hooks/useQueryRefresh';
import { RefreshControl } from '../components/RefreshControl';
import { useOrgPath } from '../hooks/useOrgPath';
import { getServiceStatus, type ServiceUiStatus } from '../utils/serviceStatus';
import type { NetworkMetric, ServiceWithResults } from '../api';
import { useDateRange } from '../contexts/DateRangeContext';
import { useDateRangeBounds, useDateRangeKey } from '../hooks/useDateRangeBounds';
import { DateRangePicker } from '../components/DateRangePicker';
import './NetworkView.css';

interface ParsedNetworkMetric extends NetworkMetric {
  parsedMetadata?: {
    network_bytes_in_total?: number;
    network_bytes_out_total?: number;
    network_speed_in_mb_per_s?: number;
    network_speed_out_mb_per_s?: number;
    network_ping_latency_ms?: number;
    network_ping_success?: number;
  };
}

function NetworkView() {
  const { t, i18n } = useTranslation();
  const orgApi = useOrgApi();
  const scope = useOrgQueryScope();
  const { orgPath } = useOrgPath();
  const { dateRange, setDateRange, formatDateRangeLabel } = useDateRange();
  const getDateBounds = useDateRangeBounds();
  const dateRangeKey = useDateRangeKey();
  const [calendarOpen, setCalendarOpen] = useState(false);

  const { refetchInterval, buildControl } = useQueryRefresh('network', 0);

  const networkQueryKey = [scope, 'network', 'metrics', dateRangeKey];
  const networkQuery = useQuery({
    queryKey: networkQueryKey,
    queryFn: async () => {
      const { startIso: start, endIso: end } = getDateBounds();

      const [res, servicesRes] = await Promise.all([
        orgApi.network.getMetrics(start, end),
        orgApi.services.list({ start_date: start, end_date: end })
      ]);

      const parsed: ParsedNetworkMetric[] = (res.data || []).map(m => ({
        ...m,
        parsedMetadata: m.metadata ? JSON.parse(m.metadata) : undefined,
      }));

      return {
        metrics: parsed,
        pingServices: (servicesRes.data as ServiceWithResults[]).filter(s => s.type === 'ping'),
      };
    },
    // The current window stays on screen while another one loads.
    placeholderData: keepPreviousData,
    refetchInterval,
  });

  const metrics = networkQuery.data?.metrics ?? [];
  const pingServices = networkQuery.data?.pingServices ?? [];

  const autoRefresh = buildControl({
    query: networkQuery,
    queryKey: networkQueryKey,
    prefix: [scope, 'network'],
  });

  // Group by host+service
  const hostGroups = metrics.reduce<Record<string, ParsedNetworkMetric[]>>((acc, m) => {
    const key = `${m.host} (${m.service_name})`;
    if (!acc[key]) acc[key] = [];
    acc[key].push(m);
    return acc;
  }, {});

  const hostKeys = Object.keys(hostGroups).sort();
  const [urlState, setUrlState] = useUrlState({ host: '' });
  const selectedHost = urlState.host;
  const filteredHostGroups = selectedHost && hostGroups[selectedHost]
    ? { [selectedHost]: hostGroups[selectedHost] }
    : hostGroups;



  // Chart data for selected host (throughput over time - to detect outages/drops)
  const selectedMetrics = selectedHost && hostGroups[selectedHost] ? hostGroups[selectedHost] : [];
  const speedChartData = selectedMetrics
    .filter(m => m.parsedMetadata?.network_speed_in_mb_per_s != null || m.parsedMetadata?.network_speed_out_mb_per_s != null)
    .sort((a, b) => new Date(a.timestamp).getTime() - new Date(b.timestamp).getTime())
    .map(m => ({
      ts: m.timestamp,
      speedIn: m.parsedMetadata?.network_speed_in_mb_per_s ?? 0,
      speedOut: m.parsedMetadata?.network_speed_out_mb_per_s ?? 0,
    }));

  return (
    <div className="network-view">
      <div className="network-header">
        <div>
          <h1><HiOutlineGlobeAlt /> {t('network.title')}</h1>
          <p className="subtitle">{t('network.subtitle')}</p>
        </div>
        <div className="network-header-actions">
          <select
            className="network-host-select"
            value={selectedHost}
            onChange={e => setUrlState({ host: e.target.value })}
          >
            <option value="">{t('network.all_checks')}</option>
            {hostKeys.map(key => (
              <option key={key} value={key}>{key}</option>
            ))}
          </select>
          <button
            className="date-range-trigger"
            onClick={() => setCalendarOpen(true)}
          >
            <HiOutlineCalendarDays />
            {formatDateRangeLabel(dateRange, { withTime: true })}
          </button>
          <RefreshControl control={autoRefresh} />
        </div>
      </div>

      <DateRangePicker
        isOpen={calendarOpen}
        value={dateRange}
        onChange={setDateRange}
        onClose={() => setCalendarOpen(false)}
        showTime
      />

      {/* Throughput over time chart (when one host selected) - to detect outages/drops */}
      {selectedHost && speedChartData.length > 0 && (
        <div className="network-chart-card">
          <h3 className="network-chart-title">{t('network.speed_title', { host: selectedHost })}</h3>
          <p className="network-chart-subtitle">{t('network.speed_subtitle')}</p>
          <NetworkSpeedChart data={speedChartData} />
        </div>
      )}

      {/* Host breakdown */}
      <div className="network-hosts">
        {networkQuery.isPending ? (
          <div className="network-loading">{t('network.loading')}</div>
        ) : (
          <>
            {pingServices.length > 0 && (
              <div className="network-ping-section">
                <h3 className="network-chart-title" style={{ marginTop: '1rem', marginBottom: '1rem' }}>
                  {t('network.ping_section')}
                </h3>
                <div style={{ display: 'flex', flexDirection: 'column', gap: '0.5rem', marginBottom: '2rem' }}>
                  {pingServices.map(svc => {
                    const status = getServiceStatus(svc);
                    const latest = svc.results && svc.results.length > 0 ? svc.results[0] : null;
                    const successRate = svc.results && svc.results.length > 0
                      ? (svc.results.filter(r => r.status === 'success').length / svc.results.length) * 100
                      : null;
                    const badgeClass: Record<ServiceUiStatus, string> = {
                      healthy: 'status-healthy',
                      warning: 'status-degraded',
                      failing: 'status-down',
                      unknown: 'status-info',
                    };
                    const badgeLabel = status === 'failing'
                      ? t('network.offline')
                      : status === 'unknown'
                        ? t('services.stats.unknown')
                        : t('network.online');
                    return (
                    <Link key={`ping-${svc.id}`} to={orgPath(`/services/${svc.id}`)} style={{ textDecoration: 'none', color: 'inherit' }}>
                      <div
                        style={{
                          display: 'flex',
                          flexWrap: 'wrap',
                          alignItems: 'center',
                          gap: '0.75rem 1rem',
                          border: '1px solid var(--border-primary)',
                          borderRadius: '8px',
                          padding: '0.75rem 1rem',
                          background: 'var(--bg-secondary)',
                          transition: 'all 0.15s ease',
                        }}
                        onMouseEnter={(e) => {
                          e.currentTarget.style.background = 'var(--bg-hover)';
                        }}
                        onMouseLeave={(e) => {
                          e.currentTarget.style.background = 'var(--bg-secondary)';
                        }}
                      >
                        <div style={{ minWidth: 0, flex: '1 1 200px' }}>
                          <div style={{ display: 'flex', alignItems: 'center', gap: '0.5rem', marginBottom: '0.25rem' }}>
                            <span style={{ fontWeight: 600, color: 'var(--text-primary)' }}>{svc.name}</span>
                            <span className={`status-badge ${badgeClass[status]}`}>
                              {badgeLabel}
                            </span>
                          </div>
                          <div style={{ fontSize: '0.8125rem', color: 'var(--text-secondary)', fontFamily: 'monospace' }}>
                            {svc.host}
                          </div>
                        </div>

                        <div style={{ flex: '1 1 150px', fontSize: '0.8125rem' }}>
                          <div style={{ color: 'var(--text-tertiary)', marginBottom: '0.25rem' }}>{t('network.latency')}</div>
                          <div style={{ color: 'var(--text-primary)', fontWeight: 500 }}>
                            {latest?.latency != null ? `${latest.latency.toFixed(0)} ms` : '—'}
                          </div>
                        </div>

                        <div style={{ flex: '1 1 100px', fontSize: '0.8125rem' }}>
                          <div style={{ color: 'var(--text-tertiary)', marginBottom: '0.25rem' }}>{t('network.compliance')}</div>
                          <div style={{ fontWeight: 500, color: successRate != null && successRate >= 99 ? 'var(--status-success)' : 'var(--status-error)' }}>
                            {successRate != null ? `${successRate.toFixed(1)}%` : '—'}
                          </div>
                        </div>

                        <div style={{ flexShrink: 0, width: '100px', display: 'flex', justifyContent: 'flex-end' }}>
                          <MiniLatencyChart latencies={svc.results?.map(r => r.latency ?? 0).reverse() ?? []} color={status === 'failing' ? 'var(--status-error)' : 'var(--status-success)'} />
                        </div>
                      </div>
                    </Link>
                    );
                  })}
                </div>
              </div>
            )}

            <h3 className="network-chart-title" style={{ marginBottom: '1rem' }}>
              {t('network.bandwidth_section')}
            </h3>
            {Object.keys(hostGroups).length === 0 ? (
              <div className="empty-state" style={{ gridColumn: '1 / -1' }}>
                <HiOutlineGlobeAlt className="empty-state-icon" />
                <div className="empty-state-title">{t('network.empty_title')}</div>
                <div className="empty-state-description">{t('network.empty_desc')}</div>
              </div>
            ) : (
              <div style={{ display: 'flex', flexDirection: 'column', gap: '0.5rem' }}>
                {Object.entries(filteredHostGroups).map(([host, hostMetrics]) => {
                  const latest = hostMetrics[0];
                  return (
                    <Link key={host} to={orgPath(`/services/${latest.service_id}`)} style={{ textDecoration: 'none', color: 'inherit' }}>
                      <div
                        style={{
                          display: 'flex',
                          flexWrap: 'wrap',
                          alignItems: 'center',
                          gap: '0.75rem 1rem',
                          border: '1px solid var(--border-primary)',
                          borderRadius: '8px',
                          padding: '0.75rem 1rem',
                          background: 'var(--bg-secondary)',
                          transition: 'all 0.15s ease',
                        }}
                        onMouseEnter={(e) => {
                          e.currentTarget.style.background = 'var(--bg-hover)';
                        }}
                        onMouseLeave={(e) => {
                          e.currentTarget.style.background = 'var(--bg-secondary)';
                        }}
                      >
                        <div style={{ minWidth: 0, flex: '1 1 200px' }}>
                          <div style={{ display: 'flex', alignItems: 'center', gap: '0.5rem', marginBottom: '0.25rem' }}>
                            <HiOutlineServerStack />
                            <span style={{ fontWeight: 600, color: 'var(--text-primary)' }}>{host}</span>
                            <span className={`status-badge status-${latest.status === 'success' ? 'success' : latest.status === 'warning' ? 'warning' : 'error'}`}>
                              {latest.status === 'success'
                                ? t('network.connected')
                                : latest.status === 'warning'
                                  ? t('network.degraded')
                                  : t('network.error')}
                            </span>
                          </div>
                          <div style={{ fontSize: '0.8125rem', color: 'var(--text-tertiary)' }}>
                            {t('network.last_reading')} {new Date(latest.timestamp).toLocaleString(i18n.language)}
                          </div>
                        </div>

                        {latest.parsedMetadata && (
                          <>
                            <div style={{ flex: '1 1 120px', fontSize: '0.8125rem' }}>
                              <div style={{ color: 'var(--text-tertiary)', marginBottom: '0.25rem' }}>{t('network.speed_in_out')}</div>
                              <div style={{ color: 'var(--text-primary)', fontWeight: 500 }}>
                                {(latest.parsedMetadata.network_speed_in_mb_per_s || 0).toFixed(2)} MB/s<br/>
                                {(latest.parsedMetadata.network_speed_out_mb_per_s || 0).toFixed(2)} MB/s
                              </div>
                            </div>

                            
                            {latest.parsedMetadata.network_ping_success !== undefined && (
                              <div style={{ flex: '1 1 150px', fontSize: '0.8125rem' }}>
                                <div style={{ color: 'var(--text-tertiary)', marginBottom: '0.25rem' }}>{t('network.internet_latency')}</div>
                                <div style={{ color: latest.parsedMetadata.network_ping_success === 0 ? 'var(--status-error)' : 'var(--text-primary)', fontWeight: 500 }}>
                                  {latest.parsedMetadata.network_ping_success === 0 ? t('network.offline') : `${latest.parsedMetadata.network_ping_latency_ms?.toFixed(0) || 0} ms`}
                                </div>
                              </div>
                            )}
                            
                            <div style={{ flexShrink: 0, width: '100px', display: 'flex', justifyContent: 'flex-end' }}>
                              {latest.parsedMetadata.network_ping_success !== undefined && (
                                <MiniLatencyChart latencies={hostMetrics.slice().reverse().map(m => m.parsedMetadata?.network_ping_latency_ms ?? 0)} color={latest.parsedMetadata.network_ping_success === 1 ? 'var(--status-success)' : 'var(--status-error)'} />
                              )}
                            </div>
                          </>
                        )}
                      </div>
                    </Link>
                  );
                })}
              </div>
            )}
          </>
        )}
      </div>
    </div>
  );
}

function NetworkSpeedChart({ data }: { data: { ts: string; speedIn: number; speedOut: number }[] }) {
  const { t } = useTranslation();
  const width = 900;
  const height = 220;
  const padding = { top: 16, right: 24, bottom: 24, left: 55 };
  const values = data.flatMap(d => [d.speedIn, d.speedOut]).filter(v => v > 0);
  const dataMax = values.length > 0 ? Math.max(...values) : 1;
  const maxVal = dataMax * 1.1 || 1;
  const xScale = (i: number) => padding.left + (i / (data.length - 1 || 1)) * (width - padding.left - padding.right);
  const yScale = (v: number) => padding.top + (1 - v / maxVal) * (height - padding.top - padding.bottom);
  const pointsIn = data.map((d, i) => `${xScale(i)},${yScale(d.speedIn)}`).join(' ');
  const pointsOut = data.map((d, i) => `${xScale(i)},${yScale(d.speedOut)}`).join(' ');
  const yTicks = [0, maxVal * 0.25, maxVal * 0.5, maxVal * 0.75, maxVal];
  return (
    <svg viewBox={`0 0 ${width} ${height}`} className="network-svg-chart">
      {yTicks.map((tick, i) => (
        <g key={i}>
          <line x1={padding.left} y1={yScale(tick)} x2={width - padding.right} y2={yScale(tick)} stroke="var(--border-primary)" strokeDasharray="4" />
          <text x={padding.left - 6} y={yScale(tick) + 4} textAnchor="end" fill="var(--text-tertiary)" fontSize="11">{tick.toFixed(2)} {t('network.chart_unit')}</text>
        </g>
      ))}
      <polyline points={pointsIn} fill="none" stroke="#22c55e" strokeWidth="2" strokeLinejoin="round" />
      <polyline points={pointsOut} fill="none" stroke="#f59e0b" strokeWidth="2" strokeLinejoin="round" />
      <text x={width - padding.right - 70} y={padding.top + 12} fill="#22c55e" fontSize="11">{t('network.chart_in')}</text>
      <text x={width - padding.right - 70} y={padding.top + 26} fill="#f59e0b" fontSize="11">{t('network.chart_out')}</text>
    </svg>
  );
}

function MiniLatencyChart({ latencies, color = "#22c55e" }: { latencies: number[], color?: string }) {
  const validLatencies = latencies.filter(l => l != null && !isNaN(l));
  if (validLatencies.length === 0) return null;
  const width = 80;
  const height = 24;
  const max = Math.max(1, ...validLatencies);
  const padding = { top: 2, right: 2, bottom: 2, left: 2 };
  const innerW = width - padding.left - padding.right;
  const innerH = height - padding.top - padding.bottom;
  const points = validLatencies
    .map((val, i) => {
      const x = padding.left + (i / (validLatencies.length - 1 || 1)) * innerW;
      const y = padding.top + innerH - (val / max) * innerH;
      return `${x},${y}`;
    })
    .join(' ');

  return (
    <svg width={width} height={height} style={{ display: 'block', overflow: 'visible', marginTop: 'auto' }}>
      <polyline
        fill="none"
        stroke={color}
        strokeWidth="1.5"
        points={points}
        vectorEffect="non-scaling-stroke"
      />
    </svg>
  );
}

export default NetworkView;
