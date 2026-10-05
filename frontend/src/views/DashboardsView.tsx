import { useState, useEffect, useCallback, useMemo, useRef } from 'react';
import {
  HiOutlineSquares2X2,
  HiOutlinePlus,
  HiOutlinePencil,
  HiOutlineTrash,
  HiOutlineCpuChip,
  HiOutlineCircleStack,
  HiOutlineClock,
  HiOutlineCheckCircle,
  HiOutlineXCircle,
  HiOutlineExclamationTriangle,
  HiOutlineXMark,
  HiOutlineArrowPath,
  HiOutlineArrowsPointingOut,
  HiOutlineMagnifyingGlassPlus,
  HiOutlineMagnifyingGlassMinus,
  HiOutlineArrowUturnLeft,
  HiOutlineExclamationCircle,
} from 'react-icons/hi2';
import { useOrgApi } from '../hooks/useOrgApi';
import { useDateRange } from '../contexts/DateRangeContext';
import type {
  SystemMetric,
  OrganizationStats,
  Host,
  DashboardWidget,
  CustomDashboard,
  SeriesExpressionQuery,
} from '../api';
import { DateRangePicker } from '../components/DateRangePicker';
import { MetricSeriesWidget } from '../components/MetricSeriesWidget';
import { SeriesQueryBuilder } from '../components/SeriesQueryBuilder';
import { emptyQuery, readyQueries } from '../components/seriesExpression';
import { ConfirmDialog } from '../components/ConfirmDialog';
import { apiErrorMessage } from '../utils/apiError';
import { HiOutlineCalendarDays } from 'react-icons/hi2';
import { useTranslation } from 'react-i18next';

import type { Layout } from 'react-grid-layout';

import { Responsive, WidthProvider } from 'react-grid-layout/legacy';
import 'react-grid-layout/css/styles.css';
import 'react-resizable/css/styles.css';
import './DashboardsView.css';

const ResponsiveReactGridLayout = WidthProvider(Responsive);

const WIDGET_TEMPLATES: DashboardWidget[] = [
  { id: '', type: 'stat', metric: 'services_total', title: 'Total Services', size: 'small' },
  { id: '', type: 'stat', metric: 'services_healthy', title: 'Services Healthy', size: 'small' },
  { id: '', type: 'stat', metric: 'services_failing', title: 'Services Failing', size: 'small' },
  { id: '', type: 'stat', metric: 'hosts_total', title: 'Total Hosts', size: 'small' },
  { id: '', type: 'stat', metric: 'errors_24h', title: 'Erreurs (24h)', size: 'small' },
  { id: '', type: 'chart', metric: 'cpu', title: 'CPU Usage', size: 'medium' },
  { id: '', type: 'chart', metric: 'ram', title: 'RAM Usage', size: 'medium' },
  { id: '', type: 'chart', metric: 'latency', title: 'HTTP Latency', size: 'medium' },
  { id: '', type: 'stat', metric: 'status', title: 'Status Global', size: 'small' },
  // Configured on the fly: its metric and labels come from what the org ingests.
  { id: '', type: 'chart', metric: 'custom_series', title: 'Custom metric', size: 'medium' },
];

const CUSTOM_SERIES_METRIC = 'custom_series';


function DashboardsView() {
  const { t } = useTranslation();
  const orgApi = useOrgApi();
  const { dateRange, setDateRange, formatDateRangeLabel } = useDateRange();
  const [dashboardCalendarOpen, setDashboardCalendarOpen] = useState(false);
  const [dashboards, setDashboards] = useState<CustomDashboard[]>([]);
  const [activeDashboard, setActiveDashboard] = useState<CustomDashboard | null>(null);
  const [showNewDashboard, setShowNewDashboard] = useState(false);
  // Creation happens inside the modal, so its failure has to be shown there: the
  // page behind the overlay is exactly where the user is not looking.
  const [createError, setCreateError] = useState('');
  const [showAddWidget, setShowAddWidget] = useState(false);
  const [newDashboardName, setNewDashboardName] = useState('');
  const [pendingDeleteDashboardId, setPendingDeleteDashboardId] = useState<number | null>(null);
  const [error, setError] = useState('');
  const [editingDashboardId, setEditingDashboardId] = useState<number | null>(null);
  const [editingName, setEditingName] = useState('');

  // Data sources
  const [stats, setStats] = useState<OrganizationStats | null>(null);
  const [metrics, setMetrics] = useState<SystemMetric[]>([]);
  const [explorerFilters, setExplorerFilters] = useState<{ service: string; host: string }[]>([]);
  const [hosts, setHosts] = useState<Host[]>([]);
  // When adding a chart widget: show config step to pick host/service/env
  const [widgetConfigTemplate, setWidgetConfigTemplate] = useState<DashboardWidget | null>(null);
  const [widgetConfigFilters, setWidgetConfigFilters] = useState<{ host: string; service: string }>({ host: '', service: '' });
  const [seriesConfigOpen, setSeriesConfigOpen] = useState(false);
  // null until the metric names arrive: an empty list would read as "no metrics".
  const [seriesNames, setSeriesNames] = useState<string[] | null>(null);
  const [seriesQueries, setSeriesQueries] = useState<SeriesExpressionQuery[]>([emptyQuery('A')]);
  const [seriesExpression, setSeriesExpression] = useState('');
  const [expandedChart, setExpandedChart] = useState<DashboardWidget | null>(null);
  const [chartZoom, setChartZoom] = useState<{ start: number; end: number }>({ start: 0, end: 1 });
  const [chartZoomHistory, setChartZoomHistory] = useState<{ start: number; end: number }[]>([]);
  const [chartSelection, setChartSelection] = useState<{ startX: number; endX: number } | null>(null);
  const chartContainerRef = useRef<HTMLDivElement>(null);
  // Persist layout changes (frequent during drag/resize) on a debounce so we
  // don't fire a PUT on every grid event. Set true once the user actually
  // interacts, so react-grid-layout's initial onLayoutChange never triggers a save.
  const persistTimer = useRef<number | null>(null);
  const layoutInteracted = useRef(false);

  const persistDashboard = useCallback((d: CustomDashboard) => {
    orgApi.customDashboards
      .update(d.id, { name: d.name, widgets: d.widgets })
      .catch(err => {
        console.error('Failed to save dashboard:', err);
        setError(apiErrorMessage(err, t('dashboards.error_save')));
      });
  }, [orgApi, t]);

  const persistDashboardDebounced = useCallback((d: CustomDashboard) => {
    if (persistTimer.current) window.clearTimeout(persistTimer.current);
    persistTimer.current = window.setTimeout(() => persistDashboard(d), 800);
  }, [persistDashboard]);

  // Apply a mutation to the active dashboard: update local state immediately
  // (source of truth for the UI) and persist to the backend.
  const applyDashboardUpdate = useCallback((updated: CustomDashboard, mode: 'now' | 'debounced') => {
    setDashboards(prev => prev.map(d => (d.id === updated.id ? updated : d)));
    setActiveDashboard(updated);
    if (mode === 'now') persistDashboard(updated);
    else persistDashboardDebounced(updated);
  }, [persistDashboard, persistDashboardDebounced]);

  // One-time import of pre-existing browser-local dashboards into the backend.
  // The old localStorage key was global (shared across orgs); we import it into
  // whichever org is opened first, then clear it so it never duplicates.
  const migrateLocalDashboards = useCallback(async (): Promise<CustomDashboard[]> => {
    if (localStorage.getItem('mm_dashboards_migrated')) return [];
    const raw = localStorage.getItem('mm_dashboards');
    if (!raw) {
      localStorage.setItem('mm_dashboards_migrated', '1');
      return [];
    }
    let legacy: { name: string; widgets: DashboardWidget[] }[] = [];
    try {
      legacy = JSON.parse(raw);
    } catch {
      legacy = [];
    }
    if (!Array.isArray(legacy) || legacy.length === 0) {
      localStorage.setItem('mm_dashboards_migrated', '1');
      localStorage.removeItem('mm_dashboards');
      return [];
    }
    try {
      const created = await Promise.all(
        legacy.map(d =>
          orgApi.customDashboards.create({ name: d.name, widgets: d.widgets || [] }).then(r => r.data)
        )
      );
      localStorage.setItem('mm_dashboards_migrated', '1');
      localStorage.removeItem('mm_dashboards');
      return created;
    } catch (err) {
      console.error('Failed to migrate local dashboards:', err);
      return [];
    }
  }, [orgApi]);

  const fetchData = useCallback(async () => {
    try {
      const [statsRes, metricsRes, hostsRes] = await Promise.all([
        orgApi.getStats(),
        orgApi.metricsExplorer.get({ start: dateRange.start.toISOString(), end: dateRange.end.toISOString() }),
        orgApi.hosts.list().catch(() => ({ data: [] })),
      ]);
      setStats(statsRes.data);
      setMetrics(metricsRes.data.metrics || []);
      setExplorerFilters(metricsRes.data.filters || []);
      setHosts(Array.isArray(hostsRes.data) ? hostsRes.data : []);
    } catch (err) {
      console.error('Failed to fetch dashboard data:', err);
    }
  }, [orgApi, dateRange]);

  useEffect(() => { fetchData(); }, [fetchData]);

  // Load dashboards for the current org. Re-runs when orgApi changes (org switch),
  // so dashboards are correctly scoped per organization.
  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const res = await orgApi.customDashboards.list();
        let list = res.data || [];
        if (list.length === 0) {
          list = await migrateLocalDashboards();
        }
        if (cancelled) return;
        setDashboards(list);
        setActiveDashboard(list.length > 0 ? list[0] : null);
      } catch (err) {
        if (!cancelled) console.error('Failed to load dashboards:', err);
      }
    })();
    return () => { cancelled = true; };
  }, [orgApi, migrateLocalDashboards]);

  useEffect(() => {
    if (!chartSelection) return;
    const onGlobalMouseUp = () => setChartSelection(null);
    window.addEventListener('mouseup', onGlobalMouseUp);
    return () => window.removeEventListener('mouseup', onGlobalMouseUp);
  }, [chartSelection]);

  const createDashboard = async () => {
    const name = newDashboardName.trim();
    if (!name) return;
    setCreateError('');
    try {
      const res = await orgApi.customDashboards.create({ name, widgets: [] });
      const created = res.data;
      setDashboards(prev => [...prev, created]);
      setActiveDashboard(created);
      setShowNewDashboard(false);
      setNewDashboardName('');
    } catch (err) {
      console.error('Failed to create dashboard:', err);
      setCreateError(apiErrorMessage(err, t('dashboards.error_create')));
    }
  };

  const startRename = (db: CustomDashboard) => {
    setEditingDashboardId(db.id);
    setEditingName(db.name);
  };

  const commitRename = () => {
    const id = editingDashboardId;
    setEditingDashboardId(null);
    if (id === null) return;
    const name = editingName.trim();
    const db = dashboards.find(d => d.id === id);
    if (!db || !name || name === db.name) return;
    const updated = { ...db, name };
    setDashboards(prev => prev.map(d => (d.id === updated.id ? updated : d)));
    if (activeDashboard?.id === updated.id) setActiveDashboard(updated);
    persistDashboard(updated);
  };

  const deleteDashboard = async (id: number) => {
    try {
      await orgApi.customDashboards.delete(id);
    } catch (err) {
      console.error('Failed to delete dashboard:', err);
      setError(apiErrorMessage(err, t('dashboards.error_delete')));
      setPendingDeleteDashboardId(null);
      return;
    }
    const updated = dashboards.filter(d => d.id !== id);
    setDashboards(updated);
    if (activeDashboard?.id === id) {
      setActiveDashboard(updated.length > 0 ? updated[0] : null);
    }
    setPendingDeleteDashboardId(null);
  };

  // Metric names and label keys for the custom-widget form, scoped to the
  // dashboard's own date range so the picker only offers what actually arrived.
  useEffect(() => {
    if (!seriesConfigOpen) return;
    let cancelled = false;
    orgApi.metricSeries
      .names({ start: dateRange.start.toISOString(), end: dateRange.end.toISOString(), size: 500 })
      .then(res => { if (!cancelled) setSeriesNames(res.data.names ?? []); })
      .catch(() => { if (!cancelled) setSeriesNames([]); });
    return () => { cancelled = true; };
  }, [orgApi, seriesConfigOpen, dateRange]);

  const seriesRange = useMemo(
    () => ({ start: dateRange.start.toISOString(), end: dateRange.end.toISOString() }),
    [dateRange],
  );
  const seriesReady = readyQueries(seriesQueries);

  const addWidget = (template: DashboardWidget, filters?: { host: string; service: string }) => {
    if (!activeDashboard) return;

    // Default dimensions based on size
    const w = template.size === 'small' ? 3 : (template.size === 'large' ? 12 : 6);
    const h = template.size === 'small' ? 2 : (template.size === 'large' ? 6 : 4);

    const widget: DashboardWidget = {
      ...template,
      id: Date.now().toString(),
      filterHost: filters?.host || undefined,
      filterService: filters?.service || undefined,
      layout: { i: Date.now().toString(), x: 0, y: Infinity, w, h }
    };
    applyDashboardUpdate({ ...activeDashboard, widgets: [...activeDashboard.widgets, widget] }, 'now');
    setShowAddWidget(false);
    setWidgetConfigTemplate(null);
    setWidgetConfigFilters({ host: '', service: '' });
  };

  const openAddWidgetConfig = (template: DashboardWidget) => {
    if (template.metric === CUSTOM_SERIES_METRIC) {
      setSeriesQueries([emptyQuery('A')]);
      setSeriesExpression('');
      setSeriesNames(null);
      setSeriesConfigOpen(true);
      return;
    }
    if (template.type === 'chart') {
      setWidgetConfigTemplate(template);
      setWidgetConfigFilters({ host: '', service: '' });
    } else {
      addWidget(template);
    }
  };

  const confirmAddWidgetWithFilters = () => {
    if (!widgetConfigTemplate) return;
    addWidget(widgetConfigTemplate, widgetConfigFilters);
  };

  const addSeriesWidget = () => {
    if (!activeDashboard || !seriesReady.length) return;
    const first = seriesReady[0];
    const id = Date.now().toString();
    const widget: DashboardWidget = {
      id,
      type: 'chart',
      metric: CUSTOM_SERIES_METRIC,
      title: seriesExpression || first.metric,
      size: 'medium',
      // metric/aggregation of $A stay set: they name the widget and keep older readers working.
      series: {
        metric: first.metric,
        aggregation: first.aggregation === 'rate' ? undefined : first.aggregation,
        queries: seriesReady,
        expression: seriesExpression || undefined,
      },
      layout: { i: id, x: 0, y: Infinity, w: 6, h: 4 },
    };
    applyDashboardUpdate({ ...activeDashboard, widgets: [...activeDashboard.widgets, widget] }, 'now');
    setSeriesConfigOpen(false);
    setShowAddWidget(false);
  };

  const removeWidget = (widgetId: string) => {
    if (!activeDashboard) return;
    applyDashboardUpdate(
      { ...activeDashboard, widgets: activeDashboard.widgets.filter(w => w.id !== widgetId) },
      'now',
    );
  };

  const getWidgetTitle = (metric: string) => t(`dashboards.widgets.${metric}`);

  // A custom widget is named by the metric it draws, not by a translation key.
  const getWidgetHeading = (widget: DashboardWidget) =>
    widget.series ? widget.series.expression || widget.series.metric : getWidgetTitle(widget.metric);

  const getWidgetValue = (metric: string) => {
    if (!stats) return '—';
    switch (metric) {
      case 'services_total': return stats.services.total;
      case 'services_healthy': return stats.services.healthy;
      case 'services_failing': return stats.services.failing;
      case 'hosts_total': return stats.hosts.total;
      case 'errors_24h': return stats.errors.total_24h;
      case 'status': return stats.status === 'healthy' ? t('dashboards.status.ok') : stats.status === 'degraded' ? t('dashboards.status.degraded') : t('dashboards.status.critical');
      default: return '—';
    }
  };

  const getWidgetIcon = (metric: string) => {
    switch (metric) {
      case 'services_total': return <HiOutlineSquares2X2 />;
      case 'services_healthy': return <HiOutlineCheckCircle />;
      case 'services_failing': return <HiOutlineXCircle />;
      case 'errors_24h': return <HiOutlineExclamationTriangle />;
      case 'cpu': return <HiOutlineCpuChip />;
      case 'ram': return <HiOutlineCircleStack />;
      case 'latency': return <HiOutlineClock />;
      default: return <HiOutlineSquares2X2 />;
    }
  };

  const getMetricColor = (metric: string) => {
    switch (metric) {
      case 'cpu': return '#0ea5e9'; // sky-500
      case 'ram': return '#8b5cf6'; // violet-500
      case 'latency': return '#f59e0b'; // amber-500
      default: return 'var(--brand-primary)';
    }
  };

  const filterMetricsByWidget = (widget: DashboardWidget): SystemMetric[] => {
    return metrics.filter(m => {
      if (widget.filterHost && m.host !== widget.filterHost) return false;
      if (widget.filterService && m.service !== widget.filterService) return false;
      return true;
    });
  };

  const getChartData = (widget: DashboardWidget) => {
    const filtered = filterMetricsByWidget(widget);
    switch (widget.metric) {
      case 'cpu': return filtered.map(m => m.cpu_perc);
      case 'ram': return filtered.map(m => m.ram_perc);
      case 'latency': return filtered.filter(m => m.http_latency).map(m => m.http_latency!);
      default: return [];
    }
  };

  const getWidgetFilterLabel = (widget: DashboardWidget): string => {
    const parts: string[] = [];
    if (widget.filterHost) parts.push(widget.filterHost);
    if (widget.filterService) parts.push(widget.filterService);
    return parts.length ? parts.join(' · ') : '';
  };

  /** Services that actually have data for this metric type (RAM → only "ram", CPU → only "cpu", etc.) */
  const getOptionsForMetricType = (metricType: 'cpu' | 'ram' | 'latency') => {
    const filtered =
      metricType === 'cpu'
        ? metrics.filter(m => m.cpu_perc != null && m.cpu_perc > 0)
        : metricType === 'ram'
          ? metrics.filter(m => m.ram_perc != null && m.ram_perc > 0)
          : metrics.filter(m => m.http_latency != null);
    const services = [...new Set(filtered.map(m => m.service).filter(Boolean))].sort();
    const hostNames = [...new Set(filtered.map(m => m.host).filter(Boolean))].sort();
    return { services, hostNames };
  };

  const renderWidget = (widget: DashboardWidget) => {
    if (widget.type === 'stat') {
      const val = getWidgetValue(widget.metric);
      return (
        <div className={`dashboard-widget widget-stat`} key={widget.id}>
          <div className="widget-header-controls">
            <button className="widget-remove" onClick={() => removeWidget(widget.id)} title={t('dashboards.widget.remove')}>
              <HiOutlineXMark />
            </button>
          </div>
          <div className="widget-icon">{getWidgetIcon(widget.metric)}</div>
          <div className="widget-value">{val}</div>
          <div className="widget-label">{getWidgetTitle(widget.metric)}</div>
        </div>
      );
    }

    // Custom metric widgets carry their own query and fetch their own data.
    if (widget.series) {
      return (
        <div className="dashboard-widget widget-chart" key={widget.id}>
          <div className="widget-header-controls">
            <button className="widget-remove" onClick={() => removeWidget(widget.id)} title={t('dashboards.widget.remove')}>
              <HiOutlineXMark />
            </button>
          </div>
          <div className="widget-chart-header">
            {getWidgetIcon(widget.metric)}
            <span>{getWidgetHeading(widget)}</span>
            <span className="widget-chart-filter">
              {widget.series.queries && widget.series.queries.length > 1
                ? t('dashboards.series_config.queries_badge', { count: widget.series.queries.length })
                : (widget.series.queries?.[0]?.aggregation ?? widget.series.aggregation ?? 'avg')}
            </span>
          </div>
          {/* Not .widget-sparkline: that container bottom-aligns its child and
              drop-shadows any svg inside it, which is wrong for a real chart. */}
          <div className="widget-series-body">
            <MetricSeriesWidget
              query={widget.series}
              start={dateRange.start.toISOString()}
              end={dateRange.end.toISOString()}
            />
          </div>
        </div>
      );
    }

    if (widget.type === 'chart') {
      const data = getChartData(widget);
      const maxVal = Math.max(...data, 1);
      const filterLabel = getWidgetFilterLabel(widget);
      return (
        <div className={`dashboard-widget widget-chart`} key={widget.id}>
          <div className="widget-header-controls">
            {data.length > 0 && (
              <button
                className="widget-expand"
                onClick={() => { setExpandedChart(widget); setChartZoom({ start: 0, end: 1 }); setChartZoomHistory([]); setChartSelection(null); }}
                title={t('dashboards.widget.expand')}
              >
                <HiOutlineArrowsPointingOut />
              </button>
            )}
            <button className="widget-remove" onClick={() => removeWidget(widget.id)} title={t('dashboards.widget.remove')}>
              <HiOutlineXMark />
            </button>
          </div>
          <div className="widget-chart-header">
            {getWidgetIcon(widget.metric)}
            <span>{getWidgetTitle(widget.metric)}</span>
            {filterLabel && <span className="widget-chart-filter">{filterLabel}</span>}
          </div>
          <div className="widget-sparkline">
            {data.length === 0 ? (
              <span className="no-data">{t('dashboards.widget.no_data')}</span>
            ) : (
              <svg viewBox={`0 0 ${Math.max(data.length - 1, 1)} 40`} preserveAspectRatio="none" width="100%" height="100%">
                <polyline
                  points={data.map((v, i) => `${i},${40 - (v / maxVal) * 38}`).join(' ')}
                  fill="none"
                  stroke={getMetricColor(widget.metric)}
                  strokeWidth="2"
                  vectorEffect="non-scaling-stroke"
                  strokeLinejoin="round"
                  strokeLinecap="round"
                />
                <polygon
                  points={`0,40 ${data.map((v, i) => `${i},${40 - (v / maxVal) * 38}`).join(' ')} ${Math.max(data.length - 1, 1)},40`}
                  fill={getMetricColor(widget.metric)}
                  opacity="0.15"
                />
              </svg>
            )}
          </div>
          {data.length > 0 && (
            <div className="widget-chart-footer">
              <span>{t('dashboards.widget.min', { value: Math.min(...data).toFixed(1) })}</span>
              <span>{t('dashboards.widget.avg', { value: (data.reduce((a, b) => a + b, 0) / data.length).toFixed(1) })}</span>
              <span>{t('dashboards.widget.max', { value: Math.max(...data).toFixed(1) })}</span>
            </div>
          )}
        </div>
      );
    }

    return null;
  };

  const onLayoutChange = (layout: Layout, _layouts: any) => {
    if (!activeDashboard) return;

    // Update the layouts in the current dashboard
    const updatedWidgets = activeDashboard.widgets.map(w => {
      const updatedLayout = layout.find((l: any) => l.i === w.id);
      if (updatedLayout) {
        return { ...w, layout: updatedLayout };
      }
      return w;
    });

    const updated = { ...activeDashboard, widgets: updatedWidgets };
    setDashboards(prev => prev.map(d => (d.id === updated.id ? updated : d)));
    setActiveDashboard(updated);
    // Only persist once the user has actually moved/resized something; ignore the
    // initial onLayoutChange react-grid-layout fires on mount.
    if (layoutInteracted.current) persistDashboardDebounced(updated);
  };

  // Build grid layout for ResponsiveReactGridLayout
  const generateLayout = () => {
    if (!activeDashboard) return [];
    
    return activeDashboard.widgets.map((w, index) => {
      if (w.layout) {
        // Enforce the id
        return { ...w.layout, i: w.id };
      }
      // Create defaults for widgets that didn't have layout saved yet
      const defaultW = w.size === 'small' ? 3 : (w.size === 'large' ? 12 : 6);
      const defaultH = w.size === 'small' ? 2 : (w.size === 'large' ? 6 : 4);
      return {
        i: w.id,
        x: (index * defaultW) % 12,
        y: Infinity, // puts it at the bottom
        w: defaultW,
        h: defaultH,
      };
    });
  };

  const gridLayout = generateLayout();

  return (
    <div className="dashboards-view">
      <div className="dashboards-header">
        <div>
          <h1><HiOutlineSquares2X2 /> {t('dashboards.title')}</h1>
          <p className="subtitle">{t('dashboards.subtitle')}</p>
        </div>
        <div className="dashboards-header-actions">
          <button
            type="button"
            className="dashboards-date-trigger"
            onClick={() => setDashboardCalendarOpen(true)}
          >
            <HiOutlineCalendarDays />
            {formatDateRangeLabel(dateRange, { withTime: true })}
          </button>
          <DateRangePicker
            isOpen={dashboardCalendarOpen}
            value={dateRange}
            onChange={(range) => setDateRange(range)}
            onClose={() => setDashboardCalendarOpen(false)}
            showTime
          />
          <button className="btn-icon" onClick={fetchData}><HiOutlineArrowPath /></button>
          <button className="btn-primary" onClick={() => { setCreateError(''); setShowNewDashboard(true); }}>
            <HiOutlinePlus /> {t('dashboards.new_dashboard')}
          </button>
        </div>
      </div>

      {error && (
        <div className="error-message" style={{ marginBottom: '1rem' }}>
          <HiOutlineExclamationCircle />
          <span>{error}</span>
          <button type="button" className="error-message-close" onClick={() => setError('')}>
            <HiOutlineXMark />
          </button>
        </div>
      )}

      {/* Dashboard tabs */}
      {dashboards.length > 0 && (
        <div className="dashboard-tabs">
          {dashboards.map(db => (
            <div key={db.id} className={`dashboard-tab ${activeDashboard?.id === db.id ? 'active' : ''}`}>
              {editingDashboardId === db.id ? (
                <input
                  className="tab-rename-input"
                  value={editingName}
                  autoFocus
                  onChange={e => setEditingName(e.target.value)}
                  onBlur={commitRename}
                  onKeyDown={e => {
                    if (e.key === 'Enter') commitRename();
                    if (e.key === 'Escape') setEditingDashboardId(null);
                  }}
                />
              ) : (
                <>
                  <button className="tab-name" onClick={() => setActiveDashboard(db)} onDoubleClick={() => startRename(db)}>{db.name}</button>
                  <button className="tab-edit" onClick={() => startRename(db)} title={t('dashboards.rename')}><HiOutlinePencil /></button>
                  <button className="tab-delete" onClick={() => setPendingDeleteDashboardId(db.id)}><HiOutlineTrash /></button>
                </>
              )}
            </div>
          ))}
        </div>
      )}

      {/* Active dashboard content */}
      {activeDashboard ? (
        <>
          <div className="dashboard-toolbar">
            <button className="btn-add-widget" onClick={() => setShowAddWidget(true)}>
              <HiOutlinePlus /> {t('dashboards.add_widget')}
            </button>
          </div>
          <div className="dashboard-grid-container">
            {activeDashboard.widgets.length === 0 ? (
              <div className="empty-state" style={{ width: '100%' }}>
                <div className="empty-state-title">{t('dashboards.empty.title')}</div>
                <div className="empty-state-description">{t('dashboards.empty.desc')}</div>
              </div>
            ) : (
              <ResponsiveReactGridLayout
                className="layout"
                layouts={{ lg: gridLayout }}
                breakpoints={{ lg: 1200, md: 996, sm: 768, xs: 480, xxs: 0 }}
                cols={{ lg: 12, md: 10, sm: 6, xs: 4, xxs: 2 }}
                rowHeight={60}
                onLayoutChange={onLayoutChange}
                onDragStart={() => { layoutInteracted.current = true; }}
                onResizeStart={() => { layoutInteracted.current = true; }}
                isResizable={true}
                margin={[16, 16]}
              >
                {activeDashboard.widgets.map((widget, index) => (
                  // Below lg only the lg layout exists, so a widget added after mount
                  // would get the 1x1 default without its own data-grid.
                  <div key={widget.id} data-grid={gridLayout[index]}>
                    {renderWidget(widget)}
                  </div>
                ))}
              </ResponsiveReactGridLayout>
            )}
          </div>
        </>
      ) : (
        <div className="empty-state">
          <HiOutlineSquares2X2 className="empty-state-icon" />
          <div className="empty-state-title">{t('dashboards.no_dashboard.title')}</div>
          <div className="empty-state-description">{t('dashboards.no_dashboard.desc')}</div>
          <button className="empty-state-action" onClick={() => { setCreateError(''); setShowNewDashboard(true); }}>
            <HiOutlinePlus /> {t('dashboards.no_dashboard.create')}
          </button>
        </div>
      )}

      {/* New dashboard modal */}
      {showNewDashboard && (
        <div className="alert-modal-overlay" onClick={() => setShowNewDashboard(false)}>
          <div className="alert-modal" onClick={e => e.stopPropagation()}>
            <div className="alert-modal-header">
              <h3>{t('dashboards.modal.new_title')}</h3>
              <button className="btn-close" onClick={() => setShowNewDashboard(false)}><HiOutlineXMark /></button>
            </div>
            <form onSubmit={e => { e.preventDefault(); createDashboard(); }} className="alert-form">
              {createError && (
                <div className="error-message" style={{ marginBottom: '1rem' }}>
                  <HiOutlineExclamationCircle />
                  <span>{createError}</span>
                </div>
              )}
              <div className="form-row">
                <label>{t('dashboards.modal.name_label')}</label>
                <input type="text" required value={newDashboardName} onChange={e => setNewDashboardName(e.target.value)} placeholder={t('dashboards.modal.name_placeholder')} />
              </div>
              <div className="form-actions">
                <button type="button" className="btn-secondary" onClick={() => setShowNewDashboard(false)}>{t('common.cancel')}</button>
                <button type="submit" className="btn-primary">{t('dashboards.modal.create')}</button>
              </div>
            </form>
          </div>
        </div>
      )}

      {/* Add widget modal */}
      {showAddWidget && (
        <div className="alert-modal-overlay" onClick={() => setShowAddWidget(false)}>
          <div className="alert-modal widget-picker" onClick={e => e.stopPropagation()}>
            <div className="alert-modal-header">
              <h3>{t('dashboards.add_widget_modal.title')}</h3>
              <button className="btn-close" onClick={() => setShowAddWidget(false)}><HiOutlineXMark /></button>
            </div>
            <div className="widget-templates">
              {WIDGET_TEMPLATES.map((template, i) => (
                <button key={i} className="widget-template" onClick={() => openAddWidgetConfig({ ...template, id: '' })}>
                  <div className="widget-template-icon">{getWidgetIcon(template.metric)}</div>
                  <div>
                    <div className="widget-template-title">{getWidgetTitle(template.metric)}</div>
                    <div className="widget-template-type">{template.type === 'stat' ? t('dashboards.add_widget_modal.type_stat') : t('dashboards.add_widget_modal.type_chart')}</div>
                  </div>
                </button>
              ))}
            </div>
          </div>
        </div>
      )}

      {/* Expanded chart modal with zoom and selection */}
      {expandedChart && (() => {
        const data = getChartData(expandedChart);
        const len = data.length;
        const startIdx = Math.floor(chartZoom.start * len);
        const endIdx = Math.min(Math.ceil(chartZoom.end * len), len);
        const slicedData = data.slice(startIdx, endIdx);
        const maxVal = Math.max(...slicedData, 1);
        const minVal = Math.min(...slicedData, 0);
        const valRange = maxVal - minVal || 1;
        const applyZoom = (next: { start: number; end: number }) => {
          setChartZoomHistory(h => [...h, chartZoom]);
          setChartZoom(next);
        };
        const zoomBack = () => {
          if (chartZoomHistory.length > 0) {
            const prev = chartZoomHistory[chartZoomHistory.length - 1];
            setChartZoomHistory(h => h.slice(0, -1));
            setChartZoom(prev);
          }
        };
        const zoomIn = () => {
          const span = chartZoom.end - chartZoom.start;
          const newSpan = Math.max(0.05, span * 0.6);
          const center = (chartZoom.start + chartZoom.end) / 2;
          applyZoom({ start: Math.max(0, center - newSpan / 2), end: Math.min(1, center + newSpan / 2) });
        };
        const zoomOut = () => {
          const span = chartZoom.end - chartZoom.start;
          const newSpan = Math.min(1, span * 1.4);
          const center = (chartZoom.start + chartZoom.end) / 2;
          applyZoom({ start: Math.max(0, center - newSpan / 2), end: Math.min(1, center + newSpan / 2) });
        };
        const pan = (delta: number) => {
          const span = chartZoom.end - chartZoom.start;
          setChartZoom({
            start: Math.max(0, Math.min(1 - span, chartZoom.start + delta)),
            end: Math.min(1, Math.max(span, chartZoom.end + delta)),
          });
        };
        const resetZoom = () => {
          if (chartZoom.start !== 0 || chartZoom.end !== 1) {
            setChartZoomHistory(h => [...h, chartZoom]);
            setChartZoom({ start: 0, end: 1 });
          }
        };
        const handleChartMouseDown = (e: React.MouseEvent<HTMLDivElement>) => {
          if (!chartContainerRef.current || len === 0) return;
          const rect = chartContainerRef.current.getBoundingClientRect();
          const x = (e.clientX - rect.left) / rect.width;
          setChartSelection({ startX: x, endX: x });
        };
        const handleChartMouseMove = (e: React.MouseEvent<HTMLDivElement>) => {
          if (!chartSelection) return;
          if (!chartContainerRef.current) return;
          const rect = chartContainerRef.current.getBoundingClientRect();
          const x = Math.max(0, Math.min(1, (e.clientX - rect.left) / rect.width));
          setChartSelection(s => s ? { ...s, endX: x } : null);
        };
        const handleChartMouseUp = () => {
          if (!chartSelection) return;
          const [a, b] = [chartSelection.startX, chartSelection.endX].sort((x, y) => x - y);
          const span = b - a;
          if (span >= 0.02) {
            applyZoom({ start: chartZoom.start + a * (chartZoom.end - chartZoom.start), end: chartZoom.start + b * (chartZoom.end - chartZoom.start) });
          }
          setChartSelection(null);
        };
        const handleChartMouseLeave = () => {
          if (chartSelection) setChartSelection(null);
        };
        const unit = expandedChart.metric === 'latency' ? ' ms' : ' %';
        const padding = { top: 24, right: 16, bottom: 32, left: 44 };
        const chartW = 800;
        const chartH = 200;
        const innerHeight = chartH - padding.top - padding.bottom;
        const bottomY = padding.top + innerHeight;
        const y = (v: number) => padding.top + ((maxVal - v) / (valRange || 1)) * innerHeight;
        const x = (i: number) => padding.left + (slicedData.length > 1 ? (i / (slicedData.length - 1)) * chartW : chartW / 2);
        return (
          <div className="alert-modal-overlay chart-expanded-overlay" onClick={() => setExpandedChart(null)}>
            <div className="chart-expanded-modal" style={{ width: '90%', maxWidth: '1000px', height: 'auto', minHeight: '400px', display: 'flex', flexDirection: 'column' }} onClick={e => e.stopPropagation()}>
              <div className="chart-expanded-header">
                <span className="chart-expanded-title" style={{ display: 'flex', alignItems: 'center', gap: '0.5rem' }}>{getWidgetIcon(expandedChart.metric)} {getWidgetTitle(expandedChart.metric)}</span>
                {getWidgetFilterLabel(expandedChart) && <span className="widget-chart-filter">{getWidgetFilterLabel(expandedChart)}</span>}
                <button className="btn-close" onClick={() => setExpandedChart(null)} aria-label={t('dashboards.chart_expanded.close')}><HiOutlineXMark /></button>
              </div>
              <div className="chart-expanded-toolbar" style={{ padding: '0.5rem 1rem', borderBottom: '1px solid var(--border-primary)', display: 'flex', gap: '0.5rem', alignItems: 'center' }}>
                <button type="button" className="btn-icon" onClick={zoomIn} title={t('dashboards.chart_expanded.zoom_in')}><HiOutlineMagnifyingGlassPlus /></button>
                <button type="button" className="btn-icon" onClick={zoomOut} title={t('dashboards.chart_expanded.zoom_out')}><HiOutlineMagnifyingGlassMinus /></button>
                <button type="button" className="btn-icon" onClick={zoomBack} title={t('dashboards.chart_expanded.zoom_back')} disabled={chartZoomHistory.length === 0}><HiOutlineArrowUturnLeft /></button>
                <button type="button" className="btn-icon" onClick={resetZoom} title={t('dashboards.chart_expanded.reset')}>⟲</button>
                <span className="chart-zoom-pan" style={{ display: 'flex', gap: '0.25rem', marginLeft: '0.5rem' }}>
                  <button type="button" className="btn-text" onClick={() => pan(-0.08)}>←</button>
                  <button type="button" className="btn-text" onClick={() => pan(0.08)}>→</button>
                </span>
                <span className="chart-zoom-hint" style={{ fontSize: '0.8rem', color: 'var(--text-tertiary)', marginLeft: '1rem' }}>{t('dashboards.chart_expanded.select_hint')}</span>
                <span className="chart-zoom-label" style={{ marginLeft: 'auto', fontSize: '0.85rem', color: 'var(--text-secondary)' }}>
                  {len > 0 ? t('dashboards.chart_expanded.range', { start: startIdx + 1, end: endIdx, total: len }) : ''}
                </span>
              </div>
              <div
                className={`chart-expanded-body ${slicedData.length > 0 ? 'chart-expanded-body-interactive' : ''}`}
                style={{ flex: 1, position: 'relative', minHeight: '300px', cursor: chartSelection ? 'crosshair' : 'default' }}
                ref={chartContainerRef}
                onMouseDown={slicedData.length > 0 ? handleChartMouseDown : undefined}
                onMouseMove={slicedData.length > 0 ? handleChartMouseMove : undefined}
                onMouseUp={slicedData.length > 0 ? handleChartMouseUp : undefined}
                onMouseLeave={slicedData.length > 0 ? handleChartMouseLeave : undefined}
              >
                {slicedData.length === 0 ? (
                  <div style={{ position: 'absolute', inset: 0, display: 'flex', alignItems: 'center', justifyContent: 'center', color: 'var(--text-tertiary)' }}>{t('dashboards.widget.no_data')}</div>
                ) : (
                  <div className="chart-expanded-inner" style={{ position: 'absolute', inset: '1rem' }}>
                    <svg viewBox={`0 0 ${chartW + padding.left + padding.right} ${chartH + padding.top + padding.bottom}`} preserveAspectRatio="none" className="chart-expanded-svg" style={{ width: '100%', height: '100%', overflow: 'visible' }}>
                      <defs>
                        <linearGradient id={`chart-fill-${expandedChart.id}`} x1="0" y1="0" x2="0" y2="1">
                          <stop offset="0%" stopColor={getMetricColor(expandedChart.metric)} stopOpacity="0.25" />
                          <stop offset="100%" stopColor={getMetricColor(expandedChart.metric)} stopOpacity="0.02" />
                        </linearGradient>
                      </defs>
                      {/* Grid */}
                      {[0, 0.25, 0.5, 0.75, 1].map((t, i) => {
                        const v = minVal + (1 - t) * valRange;
                        const yPos = padding.top + (t * (chartH - padding.top - padding.bottom));
                        return (
                          <g key={i}>
                            <line x1={padding.left} y1={yPos} x2={chartW + padding.left} y2={yPos} stroke="var(--border-primary)" strokeWidth="1" strokeDasharray="4 4" opacity="0.6" />
                            <text x={padding.left - 8} y={yPos + 4} textAnchor="end" fontSize="12" fill="var(--text-tertiary)">{v.toFixed(0)}{unit}</text>
                          </g>
                        );
                      })}
                      {/* Area + line */}
                      <polygon
                        points={`${padding.left},${bottomY} ${slicedData.map((v, i) => `${x(i)},${y(v)}`).join(' ')} ${padding.left + chartW},${bottomY}`}
                        fill={`url(#chart-fill-${expandedChart.id})`}
                      />
                      <polyline
                        points={slicedData.map((v, i) => `${x(i)},${y(v)}`).join(' ')}
                        fill="none"
                        stroke={getMetricColor(expandedChart.metric)}
                        strokeWidth="2.5"
                        strokeLinecap="round"
                        strokeLinejoin="round"
                      />
                    </svg>
                    {chartSelection && chartContainerRef.current && (
                      <div
                        className="chart-selection-overlay"
                        style={{
                          position: 'absolute',
                          top: 0,
                          bottom: 0,
                          backgroundColor: 'rgba(59, 130, 246, 0.1)',
                          borderLeft: '1px solid rgba(59, 130, 246, 0.5)',
                          borderRight: '1px solid rgba(59, 130, 246, 0.5)',
                          left: `${Math.min(chartSelection.startX, chartSelection.endX) * 100}%`,
                          width: `${Math.abs(chartSelection.endX - chartSelection.startX) * 100}%`,
                        }}
                      />
                    )}
                  </div>
                )}
              </div>
              {slicedData.length > 0 && (
                <div className="chart-expanded-footer" style={{ padding: '1rem', borderTop: '1px solid var(--border-primary)', display: 'flex', gap: '2rem', color: 'var(--text-secondary)', fontSize: '0.85rem', justifyContent: 'center' }}>
                  <span>{t('dashboards.widget.min', { value: `${Math.min(...slicedData).toFixed(2)}${unit}` })}</span>
                  <span>{t('dashboards.chart_expanded.avg', { value: (slicedData.reduce((a, b) => a + b, 0) / slicedData.length).toFixed(2), unit })}</span>
                  <span>{t('dashboards.widget.max', { value: `${Math.max(...slicedData).toFixed(2)}${unit}` })}</span>
                </div>
              )}
            </div>
          </div>
        );
      })()}

      {/* Configure chart widget: choose host / service */}
      {seriesConfigOpen && (
        <div className="alert-modal-overlay" onClick={() => { setSeriesConfigOpen(false); setShowAddWidget(false); }}>
          <div className="alert-modal widget-config" onClick={e => e.stopPropagation()}>
            <div className="alert-modal-header">
              <h3>{t('dashboards.series_config.title')}</h3>
              <button className="btn-close" onClick={() => { setSeriesConfigOpen(false); setShowAddWidget(false); }}><HiOutlineXMark /></button>
            </div>
            <div className="widget-config-filters">
              <SeriesQueryBuilder
                queries={seriesQueries}
                expression={seriesExpression}
                onQueriesChange={setSeriesQueries}
                onExpressionChange={setSeriesExpression}
                names={seriesNames ?? []}
                range={seriesRange}
              />
              {seriesNames?.length === 0 && (
                <p className="widget-config-hint">{t('dashboards.series_config.no_metrics')}</p>
              )}
            </div>
            <div className="alert-modal-actions">
              <button className="btn-secondary" onClick={() => { setSeriesConfigOpen(false); setShowAddWidget(false); }}>
                {t('common.cancel')}
              </button>
              <button className="btn-primary" onClick={addSeriesWidget} disabled={!seriesReady.length}>
                {t('dashboards.series_config.add')}
              </button>
            </div>
          </div>
        </div>
      )}

      {widgetConfigTemplate && (() => {
        const isPerHostMetric = widgetConfigTemplate.metric === 'cpu' || widgetConfigTemplate.metric === 'ram';
        const hostRequired = isPerHostMetric;
        const canSubmit = !hostRequired || !!widgetConfigFilters.host;
        const chartMetric = widgetConfigTemplate.metric === 'cpu' || widgetConfigTemplate.metric === 'ram' || widgetConfigTemplate.metric === 'latency'
          ? widgetConfigTemplate.metric
          : 'cpu';
        const { services: servicesForMetric, hostNames: hostNamesForMetric } = getOptionsForMetricType(chartMetric);
        const hostOptions = isPerHostMetric ? hostNamesForMetric : [...new Set(hosts.map(h => h.name).concat(explorerFilters.map(f => f.host).filter(Boolean)))];
        return (
          <div className="alert-modal-overlay" onClick={() => { setWidgetConfigTemplate(null); setShowAddWidget(false); }}>
            <div className="alert-modal widget-config" onClick={e => e.stopPropagation()}>
              <div className="alert-modal-header">
                <h3>{t('dashboards.widget_config.title', { title: getWidgetTitle(widgetConfigTemplate.metric) })}</h3>
                <button className="btn-close" onClick={() => { setWidgetConfigTemplate(null); setShowAddWidget(false); }}><HiOutlineXMark /></button>
              </div>
              <div className="widget-config-filters">
                {isPerHostMetric && (
                  <p className="widget-config-hint">{t('dashboards.widget_config.cpu_ram_hint')}</p>
                )}
                <div className="form-row">
                  <label>{t('dashboards.widget_config.host_label')} {hostRequired ? '*' : ''}</label>
                  <select
                    value={widgetConfigFilters.host}
                    onChange={e => setWidgetConfigFilters(f => ({ ...f, host: e.target.value }))}
                  >
                    <option value="">
                      {isPerHostMetric ? t('dashboards.widget_config.host_choose') : t('dashboards.widget_config.no_filter')}
                    </option>
                    {isPerHostMetric
                      ? hostNamesForMetric.map(host => (
                          <option key={host} value={host}>
                            {hosts.find(h => h.name === host)?.display_name || host}
                          </option>
                        ))
                      : [...new Set(hostOptions)].filter(Boolean).sort().map(host => (
                          <option key={host} value={host}>{hosts.find(h => h.name === host)?.display_name || host}</option>
                        ))}
                  </select>
                </div>
                <div className="form-row">
                  <label>{t('dashboards.widget_config.service_label')}</label>
                  <select
                    value={servicesForMetric.includes(widgetConfigFilters.service) ? widgetConfigFilters.service : ''}
                    onChange={e => setWidgetConfigFilters(f => ({ ...f, service: e.target.value }))}
                  >
                    <option value="">{t('dashboards.widget_config.no_filter')}</option>
                    {servicesForMetric.map(s => (
                      <option key={s} value={s}>{s}</option>
                    ))}
                  </select>
                </div>
              </div>
              <div className="form-actions" style={{ marginTop: '1rem' }}>
                <button type="button" className="btn-secondary" onClick={() => { setWidgetConfigTemplate(null); setShowAddWidget(false); }}>{t('common.cancel')}</button>
                <button type="button" className="btn-primary" onClick={confirmAddWidgetWithFilters} disabled={!canSubmit}>
                  {t('dashboards.widget_config.add_widget')}
                </button>
              </div>
            </div>
          </div>
        );
      })()}
      {pendingDeleteDashboardId !== null && (
        <ConfirmDialog
          title={t('common.delete')}
          message={t('dashboards.delete_confirm')}
          onConfirm={() => deleteDashboard(pendingDeleteDashboardId)}
          onCancel={() => setPendingDeleteDashboardId(null)}
        />
      )}
    </div>
  );
}

export default DashboardsView;
