import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { keepPreviousData, useQuery } from '@tanstack/react-query';
import {
  HiOutlineArrowDownTray,
  HiOutlineCalendarDays,
  HiOutlineChartBar,
  HiOutlineEye,
  HiOutlineMagnifyingGlass,
  HiOutlineXMark,
} from 'react-icons/hi2';
import { useOrgApi, useOrgQueryScope } from '../hooks/useOrgApi';
import { useUrlState, pageFromUrl, pageToUrl } from '../hooks/useUrlState';
import { useQueryRefresh } from '../hooks/useQueryRefresh';
import { RefreshControl } from '../components/RefreshControl';
import type { ProfileCapture, FlameNode, ProfileSeriesPoint } from '../api';
import { FlameGraphViewer } from '../components/FlameGraphViewer';
import { Pagination } from '../components/Pagination';
import { useDateRange } from '../contexts/DateRangeContext';
import { useDateRangeBounds, useDateRangeKey } from '../hooks/useDateRangeBounds';
import { DateRangePicker } from '../components/DateRangePicker';
import { PlanGate } from '../components/PlanGate';
import './ProfilingView.css';

function formatDate(createdAt: string): string {
  const d = new Date(createdAt);
  return d.toLocaleString(undefined, { dateStyle: 'short', timeStyle: 'short' });
}

function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

function ProfilingView() {
  const { t } = useTranslation();
  const orgApi = useOrgApi();
  const scope = useOrgQueryScope();
  const [downloadingId, setDownloadingId] = useState<number | null>(null);
  const [flameProfile, setFlameProfile] = useState<ProfileCapture | null>(null);
  const [flameData, setFlameData] = useState<FlameNode | null>(null);
  const [flameLoading, setFlameLoading] = useState(false);
  const [flameError, setFlameError] = useState<string | null>(null);
  const [filters] = useState<{ service?: string; profile_type?: string }>({});
  const [urlState, setUrlState] = useUrlState({ service: '', page: '1' });
  const page = pageFromUrl(urlState.page);
  const setPage = (next: number) => setUrlState({ page: pageToUrl(next) });
  const seriesService = urlState.service;
  const pageSize = 50;
  // RAM over time chart
  const { dateRange: seriesDateRange, setDateRange: setSeriesDateRange, formatDateRangeLabel } = useDateRange();
  const getDateBounds = useDateRangeBounds();
  const dateRangeKey = useDateRangeKey();
  const [seriesCalendarOpen, setSeriesCalendarOpen] = useState(false);

  // An open flame graph is a detail row: refreshing under it is what pausing
  // is for.
  const { refetchInterval, buildControl } = useQueryRefresh('profiling', 0, flameProfile !== null);

  const profilesQueryKey = [scope, 'profiles', 'list', filters.service ?? '', filters.profile_type ?? '', page];
  const profilesQuery = useQuery({
    queryKey: profilesQueryKey,
    queryFn: async () => {
      const res = await orgApi.profiles.list({ ...filters, limit: pageSize, offset: page * pageSize });
      return {
        profiles: (res.data as ProfileCapture[]) ?? [],
        total: Number(res.headers['x-total-count']) || 0,
      };
    },
    // The current page stays on screen while the next one loads.
    placeholderData: keepPreviousData,
    refetchInterval,
  });

  const profiles = profilesQuery.data?.profiles ?? [];
  const total = profilesQuery.data?.total ?? 0;

  // Best-effort: the RAM chart is a secondary panel, and its failure must not
  // report the whole view as stale nor stop its cadence.
  const seriesQuery = useQuery({
    queryKey: [scope, 'profiles', 'series', seriesService, dateRangeKey],
    queryFn: async () => {
      const { startIso: from, endIso: to } = getDateBounds();
      const res = await orgApi.profiles.getSeries({
        service: seriesService || undefined,
        from,
        to,
        limit: 500,
      });
      return (res.data?.points as ProfileSeriesPoint[]) ?? [];
    },
    placeholderData: keepPreviousData,
    refetchInterval,
  });

  const seriesPoints = seriesQuery.data ?? [];
  // isFetching, not isPending: with keepPreviousData the latter is true on the
  // first load only, and changing service or date window would update the chart
  // with no sign that anything was loading.
  const seriesLoading = seriesQuery.isFetching;

  const autoRefresh = buildControl({
    query: profilesQuery,
    queryKey: profilesQueryKey,
    prefix: [scope, 'profiles'],
  });

  const uniqueServices = [...new Set(profiles.map(p => p.service))].sort();
  const ramChartData = seriesPoints.map(p => ({ ts: p.timestamp, value: p.memory_mb }));
  const avgRAM = ramChartData.length > 0 ? ramChartData.reduce((s, d) => s + d.value, 0) / ramChartData.length : 0;

  const handleViewFlame = async (p: ProfileCapture) => {
    setFlameProfile(p);
    setFlameData(null);
    setFlameError(null);
    setFlameLoading(true);
    try {
      const res = await orgApi.profiles.getFlamegraph(p.id);
      setFlameData(res.data ?? null);
    } catch (err) {
      setFlameError(err instanceof Error ? err.message : t('profiling.errors.load_flame_graph'));
      setFlameData(null);
    } finally {
      setFlameLoading(false);
    }
  };

  const handleDownload = async (p: ProfileCapture) => {
    if (downloadingId !== null) return;
    setDownloadingId(p.id);
    try {
      const res = await orgApi.profiles.download(p.id);
      const blob = res.data as unknown as Blob;
      const url = URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = url;
      a.download = `profile_${p.profile_type}_${p.id}.pprof`;
      document.body.appendChild(a);
      a.click();
      document.body.removeChild(a);
      URL.revokeObjectURL(url);
    } catch (err) {
      console.error('Download failed:', err);
    } finally {
      setDownloadingId(null);
    }
  };

  return (
    <PlanGate feature="Profiling">
    <div className="profiling-view">
      <div className="profiling-header">
        <div>
          <h1><HiOutlineChartBar /> {t('profiling.title')}</h1>
          <p className="subtitle">{t('profiling.subtitle')}</p>
        </div>
        <RefreshControl control={autoRefresh} />
      </div>

      {/* RAM over time (Grafana-like) */}
      <div className="profiling-ram-section">
        <h2 className="profiling-ram-title">{t('profiling.ram_section.title')}</h2>
        <div className="profiling-ram-filters">
          <span className="filter-icon"><HiOutlineMagnifyingGlass /></span>
          <select value={seriesService} onChange={e => setUrlState({ service: e.target.value })}>
            <option value="">{t('profiling.ram_section.all_services')}</option>
            {uniqueServices.map(s => <option key={s} value={s}>{s}</option>)}
          </select>
          <button
            type="button"
            className="date-range-trigger"
            onClick={() => setSeriesCalendarOpen(true)}
          >
            <HiOutlineCalendarDays />
            {formatDateRangeLabel(seriesDateRange, { withTime: true })}
          </button>
        </div>
        <DateRangePicker
          isOpen={seriesCalendarOpen}
          value={seriesDateRange}
          onChange={setSeriesDateRange}
          onClose={() => setSeriesCalendarOpen(false)}
          showTime
        />
        <div className="profiling-ram-chart-card">
          {seriesLoading ? (
            <div className="profiling-ram-loading">{t('common.loading')}</div>
          ) : ramChartData.length === 0 ? (
            <div className="profiling-ram-empty">{t('profiling.ram_section.no_data')}</div>
          ) : (
            <>
              <div className="profiling-ram-summary">{t('profiling.ram_section.average')} <strong>{avgRAM.toFixed(1)} MB</strong> — {t('profiling.ram_section.points', { count: ramChartData.length })}</div>
              <ProfilingRAMChart data={ramChartData} />
            </>
          )}
        </div>
      </div>

      {profilesQuery.isPending ? (
        <div className="profiling-loading">
          <div className="loading-spinner" />
          <span>{t('profiling.loading_profiles')}</span>
        </div>
      ) : profiles.length === 0 ? (
        <div className="empty-state">
          <HiOutlineChartBar className="empty-state-icon" />
          <div className="empty-state-title">{t('profiling.empty.title')}</div>
          <div className="empty-state-description">{t('profiling.empty.desc')}</div>
        </div>
      ) : (
        <div className="profiling-table-wrap">
          <table className="profiling-table">
            <thead>
              <tr>
                <th>{t('profiling.table.service')}</th>
                <th>{t('profiling.table.type')}</th>
                <th>{t('profiling.table.duration')}</th>
                <th>{t('profiling.table.size')}</th>
                <th>{t('profiling.table.captured')}</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              {profiles.map((p) => (
                <tr key={p.id}>
                  <td className="profiling-service">{p.service}</td>
                  <td><span className="profiling-type-badge">{p.profile_type}</span></td>
                  <td>{p.duration_seconds != null ? `${p.duration_seconds}s` : '—'}</td>
                  <td>{formatSize(p.size_bytes)}</td>
                  <td className="profiling-date">{formatDate(p.created_at)}</td>
                  <td>
                    <button
                      type="button"
                      className="btn-view"
                      onClick={() => handleViewFlame(p)}
                      title={t('profiling.actions.view_flame_graph')}
                    >
                      <HiOutlineEye /> {t('profiling.actions.view')}
                    </button>
                    <button
                      type="button"
                      className="btn-download"
                      onClick={() => handleDownload(p)}
                      disabled={downloadingId !== null}
                      title={t('profiling.actions.download_pprof')}
                    >
                      {downloadingId === p.id ? <span className="spinner-small" /> : <HiOutlineArrowDownTray />}
                      {t('profiling.actions.download')}
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      <Pagination page={page} pageSize={pageSize} total={total} onPageChange={setPage} />

      {/* Flame graph modal */}
      {flameProfile != null && (
        <div className="profiling-modal-overlay" onClick={() => setFlameProfile(null)}>
          <div className="profiling-modal" onClick={(e) => e.stopPropagation()}>
            <div className="profiling-modal-header">
              <h2>{t('profiling.flame_modal.title', { service: flameProfile.service, type: flameProfile.profile_type })}</h2>
              <button type="button" className="btn-close-modal" onClick={() => setFlameProfile(null)}>
                <HiOutlineXMark />
              </button>
            </div>
            <div className="profiling-modal-body">
              {flameLoading && (
                <div className="profiling-flame-loading">
                  <div className="loading-spinner" />
                  <span>{t('profiling.flame_modal.loading')}</span>
                </div>
              )}
              {flameError && (
                <div className="profiling-flame-error">{flameError}</div>
              )}
              {!flameLoading && !flameError && flameData && (
                <FlameGraphViewer
                  data={flameData}
                  width={Math.min(1200, typeof window !== 'undefined' ? window.innerWidth - 80 : 1000)}
                />
              )}
            </div>
          </div>
        </div>
      )}
    </div>
    </PlanGate>
  );
}

// Line chart for RAM time series (same style as MetricsExplorerView)
function ProfilingRAMChart({ data }: { data: { ts: string; value: number }[] }) {
  const width = 900;
  const height = 220;
  const padding = { top: 16, right: 24, bottom: 24, left: 52 };
  const values = data.map(d => d.value);
  const dataMin = Math.min(...values);
  const dataMax = Math.max(...values);
  const range = dataMax - dataMin || 1;
  const minVal = Math.max(0, dataMin - range * 0.1);
  const maxVal = dataMax + range * 0.1;
  const xScale = (i: number) => padding.left + (i / (data.length - 1 || 1)) * (width - padding.left - padding.right);
  const yScale = (v: number) => padding.top + (1 - (v - minVal) / (maxVal - minVal)) * (height - padding.top - padding.bottom);
  const points = data.map((d, i) => `${xScale(i)},${yScale(d.value)}`).join(' ');
  const areaPoints = `${xScale(0)},${height - padding.bottom} ${points} ${xScale(data.length - 1)},${height - padding.bottom}`;
  const yTicks = Array.from({ length: 5 }, (_, i) => minVal + (i / 4) * (maxVal - minVal));
  const color = '#8b5cf6';
  return (
    <svg viewBox={`0 0 ${width} ${height}`} className="profiling-ram-svg">
      <defs>
        <linearGradient id="profiling-ram-grad" x1="0" y1="0" x2="0" y2="1">
          <stop offset="0%" stopColor={color} stopOpacity="0.3" />
          <stop offset="100%" stopColor={color} stopOpacity="0.05" />
        </linearGradient>
      </defs>
      {yTicks.map((tick, i) => (
        <g key={i}>
          <line x1={padding.left} y1={yScale(tick)} x2={width - padding.right} y2={yScale(tick)} stroke="var(--border-primary)" strokeDasharray="4" />
          <text x={padding.left - 6} y={yScale(tick) + 4} textAnchor="end" fill="var(--text-tertiary)" fontSize="11">{tick.toFixed(0)} MB</text>
        </g>
      ))}
      <polygon points={areaPoints} fill="url(#profiling-ram-grad)" />
      <polyline points={points} fill="none" stroke={color} strokeWidth="2" strokeLinejoin="round" />
      {data.map((d, i) => (
        <circle key={i} cx={xScale(i)} cy={yScale(d.value)} r={data.length > 150 ? 1.5 : 3} fill={color} opacity={0.8} />
      ))}
    </svg>
  );
}

export default ProfilingView;
