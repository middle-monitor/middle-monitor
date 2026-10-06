import { useState, useEffect, useMemo, type ReactNode } from 'react';
import { useNavigate, useSearchParams } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import type { TFunction } from 'i18next';
import { HiExclamationCircle, HiPlus, HiInbox, HiX, HiLink, HiTrash, HiPencil, HiClipboardCopy, HiArrowLeft, HiLocationMarker } from 'react-icons/hi';
import { HiOutlineCalendarDays } from 'react-icons/hi2';
import {
  type ErrorStats,
  type ApplicationError,
  type ErrorGroup,
  type TimeSeriesPoint,
  type Service,
  type CorrelationResult,
  type ApplicationLink,
  type LinkSuggestion,
  type LinkSuggestionsEmptyReason,
  type Host,
  type HostGroup,
} from '../api';
import { useOrgApi, useOrgQueryScope } from '../hooks/useOrgApi';
import { useUrlState, pageFromUrl, pageToUrl } from '../hooks/useUrlState';
import { useQueryRefresh } from '../hooks/useQueryRefresh';
import { RefreshControl } from '../components/RefreshControl';
import { rangeForPreset } from '../utils/dateRangePresets';
import { useOrgPath } from '../hooks/useOrgPath';
import { ConfirmDialog } from '../components/ConfirmDialog';
import { useDateRange } from '../contexts/DateRangeContext';
import { useDateRangeBounds, useDateRangeKey } from '../hooks/useDateRangeBounds';
import { DateRangePicker } from '../components/DateRangePicker';
import { AddErrorServiceForm } from './ErrorsView/components/AddErrorServiceForm';
import { AddCorrelationModal } from './ErrorsView/components/AddCorrelationModal';
import { EditErrorServiceModal } from './ErrorsView/components/EditErrorServiceModal';
import { parsePanicExplanation } from '../utils/panicExplanation';
import { parseExternalApiCause } from '../utils/externalApiCause';
import { ExplainPanel } from '../components/ExplainPanel';
import { Pagination } from '../components/Pagination';
import { Skeleton } from '../components/Skeleton';

// A fresh [] on every render gives each dependent useMemo a new identity and
// makes it recompute every time. One frozen empty list keeps that identity stable.
const EMPTY_LIST: never[] = [];

function getTimeAgo(date: Date, locale: string): string {
  const now = new Date();
  const diffInSeconds = Math.floor((now.getTime() - date.getTime()) / 1000);

  if (diffInSeconds < 60) {
    return `${diffInSeconds}s`;
  }
  const diffInMinutes = Math.floor(diffInSeconds / 60);
  if (diffInMinutes < 60) {
    return `${diffInMinutes}min`;
  }
  const diffInHours = Math.floor(diffInMinutes / 60);
  if (diffInHours < 24) {
    return `${diffInHours}h`;
  }
  const diffInDays = Math.floor(diffInHours / 24);
  if (diffInDays < 7) {
    return `${diffInDays}d`;
  }
  return date.toLocaleDateString(locale);
}

/** Small sparkline for error group timeseries */
function Sparkline({ timeseries, width = 80, height = 24 }: { timeseries: TimeSeriesPoint[]; width?: number; height?: number }) {
  if (!timeseries?.length) return null;
  const max = Math.max(1, ...timeseries.map((p) => p.count));
  const padding = { top: 2, right: 2, bottom: 2, left: 2 };
  const innerW = width - padding.left - padding.right;
  const innerH = height - padding.top - padding.bottom;
  const points = timeseries
    .map((p, i) => {
      const x = padding.left + (i / (timeseries.length - 1 || 1)) * innerW;
      const y = padding.top + innerH - (p.count / max) * innerH;
      return `${x},${y}`;
    })
    .join(' ');
  return (
    <svg width={width} height={height} style={{ display: 'block', overflow: 'visible' }} aria-hidden>
      <polyline
        fill="none"
        stroke="var(--status-error)"
        strokeWidth="1.5"
        points={points}
        vectorEffect="non-scaling-stroke"
      />
    </svg>
  );
}

/** Compact "3min" / "45s" delta between two ISO timestamps (for antecedence hints). */
function formatDelta(fromIso: string, toIso: string): string {
  const seconds = Math.max(0, Math.floor((new Date(toIso).getTime() - new Date(fromIso).getTime()) / 1000));
  if (seconds < 60) return `${seconds}s`;
  return `${Math.floor(seconds / 60)}min`;
}

/** Label for a correlation link's target type (host / host group / service / app). */
function targetTypeLabel(type: string, t: TFunction): string {
  switch (type) {
    case 'host':
      return t('errors_view.target_type_host');
    case 'host_group':
      return t('errors_view.target_type_host_group');
    case 'app':
      return t('errors_view.target_type_app');
    default:
      return t('errors_view.target_type_service');
  }
}

/** Short badge label for the inferred runtime of a parsed error. */
function runtimeLabel(runtime: string): string {
  switch (runtime) {
    case 'go':
      return 'GO';
    case 'browser':
      return 'UI';
    case 'node':
      return 'Node.js';
    default:
      return runtime.charAt(0).toUpperCase() + runtime.slice(1);
  }
}

/** One consistent card for the "Context / Correlation" section of the error modal. */
function ContextCard({
  icon,
  title,
  badge,
  tone = 'default',
  children,
}: {
  icon: ReactNode;
  title: ReactNode;
  badge?: ReactNode;
  tone?: 'default' | 'warning';
  children: ReactNode;
}) {
  const warning = tone === 'warning';
  return (
    <div
      style={{
        padding: '0.75rem',
        background: warning ? 'var(--status-warning-bg, rgba(245, 158, 11, 0.1))' : 'var(--bg-secondary)',
        borderRadius: '6px',
        border: `1px solid ${warning ? 'var(--status-warning, #eab308)' : 'var(--border-primary)'}`,
        fontSize: '0.8125rem',
      }}>
      <div style={{ fontWeight: 600, display: 'flex', alignItems: 'center', gap: '0.5rem' }}>
        {icon}
        <span>{title}</span>
        {badge}
      </div>
      {children}
    </div>
  );
}

function ErrorsView() {
  const { t, i18n } = useTranslation();
  const orgApi = useOrgApi();
  const scope = useOrgQueryScope();
  const navigate = useNavigate();
  const { orgPath } = useOrgPath();
  const { dateRange, setDateRange, formatDateRangeLabel } = useDateRange();
  const getDateBounds = useDateRangeBounds();
  const dateRangeKey = useDateRangeKey();
  const [searchParams, setSearchParams] = useSearchParams();
  // Grouped-errors pagination, only used when drilled into a single service.
  const [urlState, setUrlState] = useUrlState({ page: '1' });
  const page = pageFromUrl(urlState.page);
  const setPage = (next: number) => setUrlState({ page: pageToUrl(next) });
  const pageSize = 50;
  const [calendarOpen, setCalendarOpen] = useState(false);
  const [showAddService, setShowAddService] = useState(false);
  const [selectedService, setSelectedService] = useState<string | null>(null);
  const [selectedErrorName, setSelectedErrorName] = useState<string | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<Service | null>(null);
  const [editTarget, setEditTarget] = useState<Service | null>(null);
  const [selectedError, setSelectedError] = useState<ApplicationError | null>(null);
  const [correlationData, setCorrelationData] = useState<CorrelationResult | null>(null);
  const [loadingCorrelation, setLoadingCorrelation] = useState(false);
  const [showAddCorrelation, setShowAddCorrelation] = useState(false);
  const [suggestions, setSuggestions] = useState<LinkSuggestion[] | null>(null);
  const [suggestionsEmptyReason, setSuggestionsEmptyReason] = useState<LinkSuggestionsEmptyReason | ''>('');
  const [loadingSuggestions, setLoadingSuggestions] = useState(false);
  const [linkActionError, setLinkActionError] = useState<string | null>(null);
  const [copiedTokenId, setCopiedTokenId] = useState<number | null>(null);

  const copyServiceToken = async (serviceId: number, token: string | undefined) => {
    if (!token) return;
    try {
      await navigator.clipboard.writeText(token);
      setCopiedTokenId(serviceId);
      setTimeout(() => setCopiedTokenId(null), 2000);
    } catch {
      // fallback for older browsers
      alert(t('errors_view.token_fallback_alert', { token }));
    }
  };

  const selectError = (err: ApplicationError | null) => {
    setSelectedError(err);
    const newParams = new URLSearchParams(searchParams);
    if (err) {
      newParams.set('errorId', String(err.id));
    } else {
      newParams.delete('errorId');
    }
    // Opening the panel pushes a history entry so back closes it without
    // leaving the view; closing replaces, or back would reopen it.
    setSearchParams(newParams, { replace: !err, preventScrollReset: true });
  };

  // An open error panel is a detail row: refreshing under it is what pausing
  // is for.
  const { refetchInterval, buildControl } = useQueryRefresh('errors', 10000, selectedError !== null);

  const errorsQueryKey = [scope, 'errors', 'groups', selectedService ?? '', dateRangeKey, page];
  const errorsQuery = useQuery({
    queryKey: errorsQueryKey,
    queryFn: async () => {
      const { startIso: start, endIso: end } = getDateBounds();
      const [statsResponse, groupsResponse, servicesResponse] = await Promise.all([
        orgApi.dashboard.getErrorStats(),
        orgApi.errors.list({
          grouped: true,
          service: selectedService || undefined,
          start,
          end,
          // Paginate only when drilled into a service; the overview needs every
          // group to compute its per-service facets.
          ...(selectedService ? { limit: pageSize, offset: page * pageSize } : {}),
        }),
        orgApi.errors.getServices(),
      ]);
      return {
        stats: statsResponse.data as ErrorStats,
        errorGroups: (groupsResponse.data || []) as ErrorGroup[],
        total: Number(groupsResponse.headers['x-total-count']) || 0,
        errorServices: (servicesResponse.data as Service[]) || [],
      };
    },
    // Another page keeps the current groups on screen while it loads. Another
    // drill-down does not: they would show under the new service's title.
    placeholderData: (previous, previousQuery) =>
      previousQuery?.queryKey[3] === (selectedService ?? '') ? previous : undefined,
    refetchInterval,
  });

  // Correlation reads: nothing in the overview shows them, only an open error
  // row and the drilled-down app's links card do. Kept out of the list query so
  // the rows never wait for them, and off the refresh cadence so the page does
  // not repay them every ten seconds.
  const correlationQuery = useQuery({
    queryKey: [scope, 'errors', 'correlation-context'],
    queryFn: async () => {
      const [linksResponse, hostsResponse, servicesListResponse, hostGroupsResponse] = await Promise.all([
        orgApi.links.list().catch(() => ({ data: [] })),
        orgApi.hosts.list().catch(() => ({ data: [] })),
        orgApi.services.list().catch(() => ({ data: [] })),
        orgApi.hostGroups.list().catch(() => ({ data: [] })),
      ]);
      return {
        links: (linksResponse.data as ApplicationLink[]) || [],
        hosts: (hostsResponse.data as Host[]) || [],
        servicesList: (servicesListResponse.data as Service[]) || [],
        hostGroups: (hostGroupsResponse.data as HostGroup[]) || [],
      };
    },
    enabled: selectedError !== null || selectedService !== null,
  });

  const stats = errorsQuery.data?.stats ?? null;
  const errorGroups = errorsQuery.data?.errorGroups ?? EMPTY_LIST;
  const total = errorsQuery.data?.total ?? 0;
  const errorServices = errorsQuery.data?.errorServices ?? EMPTY_LIST;
  const links = correlationQuery.data?.links ?? [];
  const hosts = correlationQuery.data?.hosts ?? [];
  const servicesList = correlationQuery.data?.servicesList ?? [];
  const hostGroups = correlationQuery.data?.hostGroups ?? [];
  // Links arrive after the row opens: until they do, "no link" is unknown, not
  // false.
  const correlationLoaded = correlationQuery.data !== undefined;

  const autoRefresh = buildControl({
    query: errorsQuery,
    queryKey: errorsQueryKey,
    prefix: [scope, 'errors'],
  });
  const { refresh } = autoRefresh;

  // Flat list of samples for URL sync and compatibility
  const errors = useMemo(() => errorGroups.map((g) => g.sample), [errorGroups]);

  // Sync selectedError from URL (errorId param) when groups are loaded
  useEffect(() => {
    const errorId = searchParams.get('errorId');
    if (!errorId || errors.length === 0) return;
    const id = parseInt(errorId, 10);
    if (isNaN(id)) return;
    const err = errors.find((e) => e.id === id);
    if (err && (!selectedError || selectedError.id !== id)) {
      setSelectedError(err);
    }
  }, [searchParams, errors]); // eslint-disable-line react-hooks/exhaustive-deps -- selectedError intentionally excluded

  // Sync service filter and optional relative time range from URL
  useEffect(() => {
    const serviceParam = searchParams.get('service');
    setSelectedService(serviceParam || null);
    setSelectedErrorName(searchParams.get('error_name') || null);

    const relativeParam = searchParams.get('relative');
    if (relativeParam) {
      const fresh = rangeForPreset(relativeParam);
      if (fresh) {
        setDateRange({ ...fresh, relative: relativeParam });
      }
      const newParams = new URLSearchParams(searchParams);
      newParams.delete('relative');
      setSearchParams(newParams, { replace: true });
    }
  }, [searchParams]); // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    if (!selectedError) {
      setCorrelationData(null);
      return;
    }
    let cancelled = false;
    setLoadingCorrelation(true);
    orgApi.errors
      .getCorrelation(selectedError.id, '5m')
      .then((res) => {
        if (!cancelled) setCorrelationData(res.data);
      })
      .catch(() => {
        if (!cancelled) setCorrelationData(null);
      })
      .finally(() => {
        if (!cancelled) setLoadingCorrelation(false);
      });
    return () => {
      cancelled = true;
    };
    // Keyed on the id alone: the selectedError object is rebuilt on every list
    // refresh, and refetching the correlation on each of those is the cost this
    // view was already trimmed to avoid.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [selectedError?.id]);

  const handleDeleteLink = async (id: number) => {
    try {
      await orgApi.links.delete(id);
      refresh();
    } catch (err) {
      console.error(err);
    }
  };

  const handleDeleteService = async () => {
    if (!deleteTarget) return;
    try {
      await orgApi.services.delete(deleteTarget.id);
      if (selectedService === deleteTarget.name) {
        handleFilterChange('all');
      }
      refresh();
    } catch (err) {
      alert(t('metrics_view.delete_error'));
      console.error(err);
    } finally {
      setDeleteTarget(null);
    }
  };

  // Apps shown in the services datatable: every app registered to report
  // errors, plus every app that has ever errored (stats.by_service is an
  // all-time count, unlike errorGroups which is scoped to the selected date
  // range) — membership must not depend on the date range, or an app quietly
  // disappears from the table (and becomes impossible to open/correlate) the
  // moment its last error falls outside the current window.
  const errorApps = useMemo(() => {
    const seen = new Set<string>();
    const out: string[] = [];
    for (const svc of errorServices) {
      // svc.name is the app tag reported on errors (see the row-click filter
      // above); svc.service is the underlying host's own service field and
      // is unrelated to the app identity.
      if (svc.name && !seen.has(svc.name)) {
        seen.add(svc.name);
        out.push(svc.name);
      }
    }
    for (const app of Object.keys(stats?.by_service || {})) {
      if (!seen.has(app)) {
        seen.add(app);
        out.push(app);
      }
    }
    return out;
  }, [errorServices, stats]);

  // Per-app aggregates for the datatable: event count and most recent
  // occurrence in the currently selected date range.
  const errorCountByApp = useMemo(() => {
    const counts: Record<string, number> = {};
    for (const g of errorGroups) {
      counts[g.sample.service] = (counts[g.sample.service] ?? 0) + g.event_count;
    }
    return counts;
  }, [errorGroups]);

  const lastSeenByApp = useMemo(() => {
    const lastSeen: Record<string, string> = {};
    for (const g of errorGroups) {
      const service = g.sample.service;
      if (!lastSeen[service] || g.last_seen > lastSeen[service]) {
        lastSeen[service] = g.last_seen;
      }
    }
    return lastSeen;
  }, [errorGroups]);

  // Auto-load trace-based suggestions for the selected app instead of
  // requiring a separate picker + "view suggestions" click.
  useEffect(() => {
    setLinkActionError(null);
    if (!selectedService) {
      setSuggestions(null);
      setSuggestionsEmptyReason('');
      return;
    }
    let cancelled = false;
    setLoadingSuggestions(true);
    orgApi.links
      .getSuggestions(selectedService)
      .then((res) => {
        if (cancelled) return;
        setSuggestions(res.data.suggestions || []);
        setSuggestionsEmptyReason(res.data.empty_reason || '');
      })
      .catch(() => {
        if (cancelled) return;
        setSuggestions([]);
        setSuggestionsEmptyReason('');
      })
      .finally(() => {
        if (!cancelled) setLoadingSuggestions(false);
      });
    return () => {
      cancelled = true;
    };
  }, [selectedService, orgApi]);

  const acceptSuggestion = async (suggestion: LinkSuggestion) => {
    if (!selectedService) return;
    try {
      await orgApi.links.create({
        app_service_name: selectedService,
        target_type: suggestion.target_type,
        target_id: suggestion.target_id,
      });
      setSuggestions((prev) => prev?.filter((s) => !(s.target_type === suggestion.target_type && s.target_id === suggestion.target_id)) ?? null);
      setLinkActionError(null);
      refresh();
    } catch (err: any) {
      setLinkActionError(err.response?.data?.error || t('errors_view.link_action_error'));
      console.error(err);
    }
  };

  const ignoreSuggestion = (suggestion: LinkSuggestion) => {
    setSuggestions((prev) => prev?.filter((s) => !(s.target_type === suggestion.target_type && s.target_id === suggestion.target_id)) ?? null);
  };

  // Detail-mode error groups. The server already filters by service, but the
  // fetch is async: right after a row click, errorGroups still holds the
  // org-wide list from list mode — filtering client-side too avoids flashing
  // every app's errors for a beat until the scoped response lands.
  const detailGroups = useMemo(() => {
    let groups = errorGroups;
    if (selectedService) groups = groups.filter((g) => g.sample.service === selectedService);
    if (selectedErrorName) groups = groups.filter((g) => g.sample.name === selectedErrorName);
    return groups;
  }, [errorGroups, selectedService, selectedErrorName]);

  const handleFilterChange = (filterId: string) => {
    const newParams = new URLSearchParams(searchParams);
    if (filterId === 'all') {
      newParams.delete('service');
    } else if (filterId.startsWith('service:')) {
      newParams.set('service', filterId.replace('service:', ''));
    }
    // A drill-down invalidates the page number of the previous list.
    newParams.delete('page');
    setSearchParams(newParams, { preventScrollReset: true });
  };

  // Overview counts are 24h-bounded, so the drill-down forces the same range
  // or the listed groups would not match the clicked count.
  const filterByErrorName = (name: string) => {
    const newParams = new URLSearchParams(searchParams);
    newParams.set('error_name', name);
    newParams.set('relative', '24h');
    newParams.delete('page');
    setSearchParams(newParams, { preventScrollReset: true });
  };

  const clearErrorNameFilter = () => {
    const newParams = new URLSearchParams(searchParams);
    newParams.delete('error_name');
    newParams.delete('page');
    setSearchParams(newParams, { preventScrollReset: true });
  };

  // Only the results area waits on the first load: a background refresh, and a
  // refresh that fails while data is already on screen, keep the list visible.
  if (errorsQuery.isLoadingError) {
    return <div className='error-message'>{t('errors_view.load_error')}</div>;
  }

  return (
    <div>
      {/* Page Header */}
      <div className='page-header'>
        {selectedService ? (
          <div
            style={{
              display: 'flex',
              flex: 1,
              alignItems: 'center',
              justifyContent: 'space-between',
              gap: '1rem',
              flexWrap: 'wrap',
            }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: '1rem' }}>
              <button
                onClick={() => handleFilterChange('all')}
                className='btn btn-secondary btn-icon'
                title={t('errors_view.back_to_services')}
                style={{ padding: '0.5rem', borderRadius: '8px' }}>
                <HiArrowLeft size={20} />
              </button>
              <div>
                <h1 className='page-title'>{t('errors_view.title_for_service', { service: selectedService })}</h1>
                <p className='page-subtitle'>
                  {t('errors_view.subtitle_for_service', { service: selectedService })}
                </p>
              </div>
            </div>
            <div style={{ display: 'flex', alignItems: 'center', gap: '0.75rem' }}>
              <RefreshControl control={autoRefresh} />
              <button
                className='date-range-trigger'
                onClick={() => setCalendarOpen(true)}
                style={{ padding: '0.5rem 0.75rem', fontSize: '0.8125rem' }}>
                <HiOutlineCalendarDays />
                {formatDateRangeLabel(dateRange, { withTime: true })}
              </button>
              <button
                className='btn btn-primary'
                onClick={() => setShowAddCorrelation(true)}>
                <HiLink />
                <span>{t('errors_view.add_correlation')}</span>
              </button>
            </div>
          </div>
        ) : selectedErrorName ? (
          <div
            style={{
              display: 'flex',
              flex: 1,
              alignItems: 'center',
              justifyContent: 'space-between',
              gap: '1rem',
              flexWrap: 'wrap',
            }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: '1rem' }}>
              <button
                onClick={clearErrorNameFilter}
                className='btn btn-secondary btn-icon'
                title={t('errors_view.back_to_services')}
                style={{ padding: '0.5rem', borderRadius: '8px' }}>
                <HiArrowLeft size={20} />
              </button>
              <div>
                <h1 className='page-title'>{t('errors_view.title_for_type', { name: selectedErrorName })}</h1>
                <p className='page-subtitle'>
                  {t('errors_view.subtitle_for_type', { name: selectedErrorName })}
                </p>
              </div>
            </div>
            <div style={{ display: 'flex', alignItems: 'center', gap: '0.75rem' }}>
              <RefreshControl control={autoRefresh} />
              <button
                className='date-range-trigger'
                onClick={() => setCalendarOpen(true)}
                style={{ padding: '0.5rem 0.75rem', fontSize: '0.8125rem' }}>
                <HiOutlineCalendarDays />
                {formatDateRangeLabel(dateRange, { withTime: true })}
              </button>
            </div>
          </div>
        ) : (
          <div
            style={{
              display: 'flex',
              flex: 1,
              justifyContent: 'space-between',
              alignItems: 'flex-start',
              flexWrap: 'wrap',
              gap: '1rem',
            }}>
            <div>
              <h1 className='page-title'>{t('errors_view.title')}</h1>
              <p className='page-subtitle'>
                {t('errors_view.subtitle')}
              </p>
            </div>
            <div style={{ display: 'flex', alignItems: 'center', gap: '0.75rem' }}>
              <RefreshControl control={autoRefresh} />
              <button
                className='date-range-trigger'
                onClick={() => setCalendarOpen(true)}
                style={{ padding: '0.5rem 0.75rem', fontSize: '0.8125rem' }}>
                <HiOutlineCalendarDays />
                {formatDateRangeLabel(dateRange, { withTime: true })}
              </button>
              <button
                onClick={() => setShowAddService(!showAddService)}
                className='btn btn-primary'>
                <HiPlus />
                <span>{showAddService ? t('common.cancel') : t('errors_view.add_service')}</span>
              </button>
            </div>
          </div>
        )}
      </div>

      <DateRangePicker
        isOpen={calendarOpen}
        value={dateRange}
        onChange={(range) => { setDateRange(range); setPage(0); }}
        onClose={() => setCalendarOpen(false)}
        showTime
      />

      {/* Add Service Form */}
      {showAddService && (
        <AddErrorServiceForm
          onSuccess={() => {
            setShowAddService(false);
            refresh();
          }}
          onCancel={() => setShowAddService(false)}
        />
      )}

      {/* Stats Overview */}
      {!selectedService && !selectedErrorName && errorsQuery.isPending && (
        <div className='card'>
          <Skeleton rows={2} silent />
        </div>
      )}
      {!selectedService && !selectedErrorName && stats && (
        <div className='card'>
          <h2 className='card-title'>
            <HiExclamationCircle
              className='card-title-icon'
              style={{ color: 'var(--status-error)' }}
            />
            <span>{t('errors_view.overview_24h')}</span>
          </h2>

          <div className='stat-grid'>
            <div className='stat-card'>
              <div className='stat-label'>{t('services.stats.total')}</div>
              <div className='stat-value error'>{stats.total}</div>
              <div className='stat-label' style={{ marginTop: '1rem' }}>
                {t('errors_view.stats.by_application')}
              </div>
              <BreakdownList
                counts={stats.by_service}
                title={t('errors_view.open_application')}
                onPick={(name) => handleFilterChange(`service:${name}`)}
              />
            </div>

            <div className='stat-card'>
              <div className='stat-label'>{t('errors_view.stats.by_error_type')}</div>
              <BreakdownList
                counts={stats.by_error}
                title={t('errors_view.filter_by_type')}
                onPick={filterByErrorName}
              />
            </div>
          </div>
        </div>
      )}

      {/* Services datatable — list mode only, mirrors ServicesView/HostsView */}
      {!selectedService && !selectedErrorName && (
        <div className='card'>
          <h2 className='card-title'>
            <span>{t('errors_view.apps_table_title')}</span>
          </h2>
          {errorsQuery.isPending ? (
            <Skeleton rows={6} />
          ) : errorApps.length === 0 ? (
            <div className='empty-state'>
              <HiInbox className='empty-state-icon' />
              <div className='empty-state-title'>{t('errors_view.empty_title')}</div>
              <div className='empty-state-description'>{t('errors_view.empty_recent')}</div>
            </div>
          ) : (
            <div style={{ overflowX: 'auto' }}>
              <table className='table'>
                <thead>
                  <tr>
                    <th>{t('errors_view.table.status')}</th>
                    <th>{t('services.table.name')}</th>
                    <th>{t('errors_view.table.registered')}</th>
                    <th>{t('errors_view.table.errors_in_range')}</th>
                    <th>{t('errors_view.table.last_seen')}</th>
                    <th>{t('common.actions')}</th>
                  </tr>
                </thead>
                <tbody>
                  {errorApps.map((app) => {
                    const count = errorCountByApp[app] ?? 0;
                    const lastSeen = lastSeenByApp[app];
                    const registered = errorServices.find((s) => s.name === app);
                    return (
                      <tr
                        key={app}
                        style={{ cursor: 'pointer' }}
                        onClick={() => handleFilterChange(`service:${app}`)}
                        title={t('errors_view.view_service_errors')}>
                        <td>
                          <span
                            style={{
                              display: 'inline-block',
                              width: 8,
                              height: 8,
                              borderRadius: '50%',
                              background: count > 0 ? 'var(--status-error)' : 'var(--status-success)',
                            }}
                            title={count > 0 ? t('errors_view.status_erroring') : t('errors_view.status_quiet')}
                          />
                        </td>
                        <td style={{ fontWeight: 500, color: 'var(--brand-primary)' }}>{app}</td>
                        <td>
                          {registered ? (
                            <span className='status-badge status-info'>
                              {registered.type.replace('error_service_', '').toUpperCase()}
                            </span>
                          ) : (
                            <span style={{ color: 'var(--text-tertiary)' }}>—</span>
                          )}
                        </td>
                        <td>
                          {count > 0 ? (
                            <span
                              style={{
                                color: 'var(--status-error)',
                                fontWeight: 600,
                                background: 'var(--status-error-bg)',
                                padding: '0.125rem 0.5rem',
                                borderRadius: '4px',
                                fontSize: '0.75rem',
                              }}>
                              {count}
                            </span>
                          ) : (
                            <span style={{ color: 'var(--text-tertiary)' }}>0</span>
                          )}
                        </td>
                        <td style={{ fontSize: '0.75rem', color: 'var(--text-tertiary)', fontFamily: 'monospace' }}>
                          {lastSeen ? getTimeAgo(new Date(lastSeen), i18n.language) : '—'}
                        </td>
                        <td onClick={(e) => e.stopPropagation()} style={{ whiteSpace: 'nowrap' }}>
                          {registered ? (
                            <>
                              <button
                                type='button'
                                onClick={() => setEditTarget(registered)}
                                style={{ background: 'none', border: 'none', cursor: 'pointer', padding: '0.25rem', color: 'var(--text-secondary)' }}
                                aria-label={t('errors_view.edit_service_title')}
                                title={t('errors_view.edit_service_title')}>
                                <HiPencil size={18} />
                              </button>
                              <button
                                type='button'
                                onClick={() => setDeleteTarget(registered)}
                                style={{ background: 'none', border: 'none', cursor: 'pointer', padding: '0.25rem', color: 'var(--status-error)' }}
                                aria-label={t('common.delete')}
                                title={t('common.delete')}>
                                <HiTrash size={18} />
                              </button>
                            </>
                          ) : (
                            <span style={{ color: 'var(--text-tertiary)' }}>—</span>
                          )}
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
          )}
        </div>
      )}

      {/* Errors List — service drill-down or error-type drill-down */}
      {(selectedService || selectedErrorName) && (
      <div className='card'>
        <div
          style={{
            display: 'flex',
            justifyContent: 'space-between',
            alignItems: 'center',
            marginBottom: '1rem',
          }}>
          <h2 className='card-title' style={{ marginBottom: 0 }}>
            <span>{t('errors_view.grouped_errors')}</span>
          </h2>
        </div>

        {errorsQuery.isPending ? (
          <Skeleton rows={6} />
        ) : detailGroups.length === 0 ? (
          <div className='empty-state'>
            <HiInbox className='empty-state-icon' />
            <div className='empty-state-title'>{t('errors_view.empty_title')}</div>
            <div className='empty-state-description'>
              {selectedService
                ? t('errors_view.empty_for_service', { service: selectedService })
                : t('errors_view.empty_for_type', { name: selectedErrorName })}
            </div>
          </div>
        ) : (
          <div style={{ display: 'flex', flexDirection: 'column', gap: '0.5rem' }}>
            {detailGroups.map((group) => {
              const err = group.sample;
              const fileName = err.file?.split('/').pop() || err.file || '—';
              const timeAgo = getTimeAgo(new Date(group.last_seen), i18n.language);

              return (
                <div
                  // The identity the backend groups on. sample_id is the id of
                  // the group's latest occurrence, so it moves like last_seen,
                  // and a changing key remounts the row instead of updating it.
                  key={`${err.name}|${err.message}|${err.file ?? ''}`}
                  role="button"
                  tabIndex={0}
                  onClick={() => selectError(err)}
                  onKeyDown={(e) => e.key === 'Enter' && selectError(err)}
                  style={{
                    display: 'flex',
                    flexWrap: 'wrap',
                    alignItems: 'center',
                    gap: '0.75rem 1rem',
                    border: '1px solid var(--border-primary)',
                    borderRadius: '8px',
                    padding: '0.75rem 1rem',
                    background: selectedError?.id === err.id ? 'var(--bg-hover)' : 'var(--bg-secondary)',
                    transition: 'all 0.15s ease',
                    cursor: 'pointer',
                  }}
                  onMouseEnter={(e) => {
                    if (selectedError?.id !== err.id) e.currentTarget.style.background = 'var(--bg-hover)';
                    e.currentTarget.style.borderColor = 'var(--status-error-border)';
                  }}
                  onMouseLeave={(e) => {
                    if (selectedError?.id !== err.id) e.currentTarget.style.background = 'var(--bg-secondary)';
                    e.currentTarget.style.borderColor = 'var(--border-primary)';
                  }}>
                  <div style={{ minWidth: 0, flex: '1 1 200px' }}>
                    <div style={{ fontWeight: 600, fontSize: '0.875rem', color: 'var(--status-error)', marginBottom: '0.25rem' }}>
                      {err.name}
                    </div>
                    <div style={{ fontSize: '0.8125rem', color: 'var(--text-primary)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                      {err.message}
                    </div>
                    <div style={{ fontSize: '0.75rem', color: 'var(--text-tertiary)', marginTop: '0.25rem' }} title={err.file}>
                      {fileName}{err.line ? `:${err.line}` : ''} · {err.service} · {t('common.ago', { time: timeAgo })}
                    </div>
                  </div>
                  <div style={{ fontSize: '0.875rem', fontWeight: 600, color: 'var(--status-error)', whiteSpace: 'nowrap' }}>
                    {t('errors_view.event_count', { count: group.event_count })}
                  </div>
                  <div style={{ width: 80, height: 24, flexShrink: 0 }}>
                    <Sparkline timeseries={group.timeseries} width={80} height={24} />
                  </div>
                </div>
              );
            })}
          </div>
        )}
        {/* Server-side pagination only exists for the service drill-down; the
            type drill-down filters the full list client-side. */}
        {selectedService && (
          <Pagination page={page} pageSize={pageSize} total={total} onPageChange={setPage} />
        )}
      </div>
      )}

      {/* Error detail modal with correlation */}
      {selectedError && (
        <div
          className="modal-overlay"
          onClick={() => selectError(null)}
          style={{
            position: 'fixed',
            inset: 0,
            background: 'rgba(0,0,0,0.5)',
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            zIndex: 1000,
          }}>
          <div
            className="card"
            onClick={(e) => e.stopPropagation()}
            style={{
              maxWidth: '600px',
              width: '90%',
              maxHeight: '90vh',
              overflow: 'auto',
            }}>
            <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', marginBottom: '1rem' }}>
              <h2 className="card-title" style={{ marginBottom: 0 }}>
                {t('errors_view.detail_title')}
              </h2>
              <button
                type="button"
                onClick={() => selectError(null)}
                style={{ background: 'none', border: 'none', cursor: 'pointer', padding: '0.25rem' }}
                aria-label={t('common.close')}>
                <HiX size={24} />
              </button>
            </div>
            <div style={{ marginBottom: '1rem' }}>
              <div style={{ fontWeight: 600, marginBottom: '0.5rem' }}>{selectedError.name}</div>
              <div style={{ fontSize: '0.875rem', color: 'var(--text-secondary)', marginBottom: '0.5rem' }}>
                {selectedError.message}
              </div>
              <div style={{ fontSize: '0.75rem', color: 'var(--text-tertiary)' }}>
                {selectedError.service} · {new Date(selectedError.timestamp).toLocaleString(i18n.language)}
              </div>
              {(selectedError.file || (selectedError.line != null && selectedError.line > 0)) && (
                <div style={{ fontSize: '0.75rem', color: 'var(--text-tertiary)', marginTop: '0.375rem' }} title={selectedError.file || undefined}>
                  {t('errors_view.file_label')} {selectedError.file ? selectedError.file.split('/').pop() || selectedError.file : '—'}
                  {selectedError.line != null && selectedError.line > 0 ? ` · ${t('errors_view.line_label', { line: selectedError.line })}` : ''}
                </div>
              )}
              {selectedError.trace_id && (
                <button
                  type="button"
                  className="btn btn-secondary"
                  style={{ marginTop: '0.625rem', fontSize: '0.75rem', padding: '0.25rem 0.5rem', display: 'inline-flex', alignItems: 'center', gap: '0.25rem' }}
                  onClick={() => navigate(orgPath(`/traces?q=${selectedError.trace_id}`))}>
                  <HiLink />
                  {t('errors_view.view_trace')}
                </button>
              )}
            </div>

            <ExplainPanel
              key={`explain-err-${selectedError.id}`}
              fetchExplanationSSE={(force) => orgApi.errors.explainSSE(selectedError.id, force)}
            />

            <h3 style={{ fontSize: '0.875rem', marginBottom: '0.75rem', display: 'flex', alignItems: 'center', gap: '0.5rem' }}>
              <HiLink />
              {t('errors_view.correlation_title')}
              {!loadingCorrelation && typeof correlationData?.confidence === 'number' && correlationData.confidence > 0 && (
                <span className="status-badge status-info" style={{ fontSize: '0.7rem' }}>
                  {t('errors_view.confidence_label', { pct: Math.round(correlationData.confidence * 100) })}
                </span>
              )}
            </h3>
            {(() => {
              const panic = parsePanicExplanation(selectedError.name, selectedError.message, selectedError.file);
              const externalCause = parseExternalApiCause(selectedError.message);
              const hasCorrelation = !!correlationData?.has_correlation;
              const hasLocation = selectedError.file || (selectedError.line != null && selectedError.line > 0);
              const fileLabel = selectedError.file ? selectedError.file.split('/').pop() || selectedError.file : '—';

              return (
                <div style={{ display: 'flex', flexDirection: 'column', gap: '0.5rem' }}>
                  {panic && (
                    <ContextCard
                      icon={<HiExclamationCircle size={16} style={{ flexShrink: 0 }} />}
                      title={panic.label}
                      badge={
                        panic.runtime && panic.runtime !== 'unknown' ? (
                          <span className="status-badge status-info" style={{ fontSize: '0.7rem' }}>
                            {runtimeLabel(panic.runtime)}
                          </span>
                        ) : undefined
                      }>
                      <p style={{ margin: '0.375rem 0 0', color: 'var(--text-secondary)' }}>{panic.cause}</p>
                      {hasLocation && (
                        <p style={{ margin: '0.375rem 0 0', fontSize: '0.75rem', color: 'var(--text-tertiary)', fontFamily: 'monospace', display: 'flex', alignItems: 'center', gap: '0.25rem' }} title={selectedError.file || undefined}>
                          <HiLocationMarker style={{ flexShrink: 0 }} />
                          <span>
                            {fileLabel}
                            {selectedError.line != null && selectedError.line > 0 ? `:${selectedError.line}` : ''}
                          </span>
                        </p>
                      )}
                      {panic.suggestion && (
                        <p style={{ margin: '0.5rem 0 0', fontSize: '0.75rem', color: 'var(--text-tertiary)' }}>→ {panic.suggestion}</p>
                      )}
                    </ContextCard>
                  )}

                  {externalCause && (
                    <ContextCard tone="warning" icon={<HiLink />} title={t('errors_view.external_api_cause')}>
                      <p style={{ margin: '0.375rem 0 0', color: 'var(--text-secondary)' }}>{externalCause.summary}</p>
                      <p style={{ margin: '0.375rem 0 0', fontSize: '0.75rem', fontFamily: 'monospace', color: 'var(--text-tertiary)' }} title={externalCause.target}>
                        {externalCause.target} — {externalCause.reason}
                      </p>
                    </ContextCard>
                  )}

                  {!loadingCorrelation && correlationData?.recurrence &&
                    (correlationData.recurrence.is_new || correlationData.recurrence.is_recurrent || correlationData.recurrence.count_last_hour > 1) && (
                    <ContextCard
                      tone={correlationData.recurrence.is_new ? 'warning' : 'default'}
                      icon={<HiExclamationCircle size={16} style={{ flexShrink: 0 }} />}
                      title={
                        correlationData.recurrence.is_new
                          ? t('errors_view.recurrence_new_title')
                          : correlationData.recurrence.is_recurrent
                          ? t('errors_view.recurrence_chronic_title')
                          : t('errors_view.recurrence_repeat_title')
                      }
                      badge={
                        correlationData.recurrence.is_new ? (
                          <span className="status-badge status-error" style={{ fontSize: '0.7rem' }}>{t('errors_view.recurrence_new_badge')}</span>
                        ) : correlationData.recurrence.is_recurrent ? (
                          <span className="status-badge status-info" style={{ fontSize: '0.7rem' }}>{t('errors_view.recurrence_chronic_badge')}</span>
                        ) : undefined
                      }>
                      <p style={{ margin: '0.375rem 0 0', color: 'var(--text-secondary)' }}>
                        {t('errors_view.recurrence_counts', {
                          hour: correlationData.recurrence.count_last_hour,
                          day: correlationData.recurrence.count_24h,
                          week: correlationData.recurrence.count_7d,
                        })}
                      </p>
                    </ContextCard>
                  )}

                  {loadingCorrelation ? (
                    <div style={{ padding: '0.5rem 0', color: 'var(--text-tertiary)', fontSize: '0.8125rem' }}>{t('common.loading')}</div>
                  ) : hasCorrelation ? (
                    <>
                      {correlationData!.summary && (
                        <p style={{ fontSize: '0.8125rem', color: 'var(--text-secondary)', margin: 0 }}>{correlationData!.summary}</p>
                      )}
                      {correlationData!.semantic_matches.map((match, i) => (
                        <ContextCard key={`sem-${i}`} icon={<HiExclamationCircle size={16} />} title={t('errors_view.text_clue')}>
                          <p style={{ margin: '0.375rem 0 0', color: 'var(--text-secondary)' }}>{match}</p>
                        </ContextCard>
                      ))}
                      {correlationData!.infra.map((infra, i) => (
                        <ContextCard
                          key={`infra-${i}`}
                          icon={<HiExclamationCircle size={16} color="var(--status-error)" />}
                          title={`${infra.metric_name} (${correlationData!.host_name || t('errors_view.host_fallback')})`}>
                          <p style={{ margin: '0.375rem 0 0', color: 'var(--text-secondary)' }}>{infra.description}</p>
                        </ContextCard>
                      ))}
                      {correlationData!.services.map((svc, i) => (
                        <ContextCard
                          key={`svc-${i}`}
                          icon={<HiLink size={16} />}
                          title={t('errors_view.group_neighbour', { name: svc.service_name })}
                          badge={<span className="status-badge status-error" style={{ fontSize: '0.7rem' }}>{t('errors_view.failure_badge')}</span>}>
                          <p style={{ margin: '0.375rem 0 0', color: 'var(--text-secondary)' }}>{svc.description}</p>
                          {svc.preceded_incident && svc.occurred_at && (
                            <p style={{ margin: '0.375rem 0 0', fontSize: '0.75rem', color: 'var(--text-tertiary)' }}>
                              {t('errors_view.occurred_before', { time: formatDelta(svc.occurred_at, selectedError.timestamp) })}
                            </p>
                          )}
                        </ContextCard>
                      ))}
                      {correlationData!.apps.map((app, i) => (
                        <ContextCard
                          key={`app-${i}`}
                          icon={<HiExclamationCircle size={16} color="var(--status-error)" />}
                          title={app.service}
                          badge={
                            <>
                              {app.relation === 'dependency' ? (
                                <span className="status-badge status-warning" style={{ fontSize: '0.7rem' }}>{t('errors_view.relation_dependency')}</span>
                              ) : app.relation === 'dependent' ? (
                                <span className="status-badge status-info" style={{ fontSize: '0.7rem' }}>{t('errors_view.relation_dependent')}</span>
                              ) : (
                                <span className="status-badge status-info" style={{ fontSize: '0.7rem' }}>{t('errors_view.relation_host_group')}</span>
                              )}
                              <span className="status-badge status-error" style={{ fontSize: '0.7rem' }}>{t('errors_view.coapp_badge', { count: app.count })}</span>
                            </>
                          }>
                          <p style={{ margin: '0.375rem 0 0', color: 'var(--text-secondary)' }}>{t('errors_view.coapp_desc', { name: app.error_name })}</p>
                          {app.preceded_incident && app.occurred_at && (
                            <p style={{ margin: '0.375rem 0 0', fontSize: '0.75rem', color: 'var(--text-tertiary)' }}>
                              {t('errors_view.occurred_before', { time: formatDelta(app.occurred_at, selectedError.timestamp) })}
                            </p>
                          )}
                        </ContextCard>
                      ))}
                    </>
                  ) : !panic && !externalCause ? (
                    // Only surface the empty message when there is genuinely no other
                    // context above it — otherwise it just contradicts the cards shown.
                    <div style={{ padding: '0.5rem 0', fontSize: '0.8125rem', color: 'var(--text-tertiary)' }}>
                      {t('errors_view.no_correlation')}
                      {selectedService && correlationLoaded && !links.some((l) => l.app_service_name === selectedService) && (
                        <div style={{ marginTop: '0.75rem' }}>
                          <p style={{ margin: '0 0 0.5rem' }}>{t('errors_view.no_links_hint')}</p>
                          <button
                            type="button"
                            className="btn btn-primary"
                            style={{ fontSize: '0.8125rem', padding: '0.375rem 0.75rem' }}
                            onClick={() => {
                              selectError(null);
                              setShowAddCorrelation(true);
                            }}>
                            <HiLink />
                            <span>{t('errors_view.add_correlation')}</span>
                          </button>
                        </div>
                      )}
                    </div>
                  ) : null}
                </div>
              );
            })()}
          </div>
        </div>
      )}

      {/* Registered-service info — detail mode only, shown if this app has an
          Error Service registration (SDK type, host, token). */}
      {selectedService && (() => {
        const svc = errorServices.find((s) => s.name === selectedService);
        if (!svc) return null;
        return (
          <div className="card" style={{ marginTop: '1.5rem', display: 'flex', alignItems: 'center', gap: '1rem', flexWrap: 'wrap' }}>
            <span className="status-badge status-info">{svc.type.replace('error_service_', '').toUpperCase()}</span>
            <span style={{ fontSize: '0.8125rem', color: 'var(--text-secondary)', fontFamily: 'monospace' }}>{svc.host}</span>
            {svc.token && (
              <button
                type="button"
                onClick={() => copyServiceToken(svc.id, svc.token)}
                className="btn btn-secondary"
                style={{ fontSize: '0.75rem', padding: '0.25rem 0.5rem', display: 'inline-flex', alignItems: 'center', gap: '0.25rem' }}
                title={t('errors_view.copy_token_title')}>
                <HiClipboardCopy />
                {copiedTokenId === svc.id ? t('common.copied') : t('errors_view.copy_token')}
              </button>
            )}
            <button
              type="button"
              onClick={() => setEditTarget(svc)}
              className="btn btn-secondary"
              style={{ fontSize: '0.75rem', padding: '0.25rem 0.5rem', display: 'inline-flex', alignItems: 'center', gap: '0.25rem' }}
              title={t('errors_view.edit_service_title')}>
              <HiPencil />
              {t('common.edit')}
            </button>
            <button
              type="button"
              onClick={() => setDeleteTarget(svc)}
              className="btn btn-danger"
              style={{ fontSize: '0.75rem', padding: '0.25rem 0.5rem', display: 'inline-flex', alignItems: 'center', gap: '0.25rem' }}
              title={t('common.delete')}>
              <HiTrash />
              {t('common.delete')}
            </button>
          </div>
        );
      })()}

      {/* Correlation card — detail mode only, scoped to this one app. Adding a
          link happens via the "Add correlation" button in the header (modal);
          this card only shows suggestions and the app's existing links. */}
      {selectedService && (
      <div className="card" style={{ marginTop: '1.5rem' }}>
        <h2 className="card-title">
          <HiLink className="card-title-icon" />
          {t('errors_view.correlation_links_title')}
        </h2>
        <p style={{ fontSize: '0.875rem', color: 'var(--text-secondary)', marginBottom: '1rem' }}>
          {t('errors_view.links_intro')}
        </p>

        {/* Suggestions inferred from traces — loaded automatically for this app */}
        <div style={{ marginBottom: '1.25rem' }}>
          <div style={{ fontSize: '0.8125rem', fontWeight: 600, marginBottom: '0.5rem' }}>{t('errors_view.suggestions_title')}</div>
          {loadingSuggestions ? (
            <div style={{ fontSize: '0.8125rem', color: 'var(--text-tertiary)' }}>{t('common.loading')}</div>
          ) : suggestions && suggestions.length > 0 ? (
            <div style={{ display: 'flex', flexDirection: 'column', gap: '0.5rem' }}>
              {suggestions.map((s) => (
                <div
                  key={`${s.target_type}-${s.target_id}`}
                  style={{
                    display: 'flex',
                    alignItems: 'center',
                    justifyContent: 'space-between',
                    padding: '0.5rem 0.75rem',
                    background: 'var(--bg-secondary)',
                    borderRadius: '6px',
                    fontSize: '0.875rem',
                  }}>
                  <span>
                    <strong>{s.target_name}</strong>
                    <span className="status-badge status-info" style={{ marginLeft: '0.5rem' }}>{targetTypeLabel(s.target_type, t)}</span>
                    <span style={{ marginLeft: '0.5rem', color: 'var(--text-tertiary)', fontSize: '0.75rem' }}>{s.reason}</span>
                  </span>
                  <div style={{ display: 'flex', gap: '0.5rem' }}>
                    <button type="button" className="btn btn-primary" style={{ padding: '0.25rem 0.5rem', fontSize: '0.8125rem' }} onClick={() => acceptSuggestion(s)}>
                      {t('errors_view.accept')}
                    </button>
                    <button type="button" className="btn btn-secondary" style={{ padding: '0.25rem 0.5rem', fontSize: '0.8125rem' }} onClick={() => ignoreSuggestion(s)}>
                      {t('errors_view.ignore')}
                    </button>
                  </div>
                </div>
              ))}
            </div>
          ) : (
            <div style={{ fontSize: '0.8125rem', color: 'var(--text-tertiary)' }}>
              {suggestionsEmptyReason === 'tracing_not_configured'
                ? t('errors_view.suggestions_empty_not_configured')
                : suggestionsEmptyReason === 'no_trace_data'
                ? t('errors_view.suggestions_empty_no_data')
                : t('errors_view.suggestions_empty_no_matches')}
            </div>
          )}
          {linkActionError && (
            <div style={{ marginTop: '0.5rem', fontSize: '0.8125rem', color: 'var(--status-error)' }}>{linkActionError}</div>
          )}
        </div>

        {/* Configured links for this app only */}
        {(() => {
          if (!correlationLoaded) {
            return <div style={{ fontSize: '0.8125rem', color: 'var(--text-tertiary)' }}>{t('common.loading')}</div>;
          }
          const appLinks = links.filter((l) => l.app_service_name === selectedService);
          return appLinks.length === 0 ? (
            <div className="empty-state" style={{ minHeight: '120px' }}>
              <div className="empty-state-title">{t('errors_view.no_links_title')}</div>
              <div className="empty-state-description">{t('errors_view.no_links_desc')}</div>
            </div>
          ) : (
            <div style={{ overflowX: 'auto' }}>
              <table className="table">
                <thead>
                  <tr>
                    <th>{t('services.table.type')}</th>
                    <th>{t('errors_view.table_links.target')}</th>
                    <th></th>
                  </tr>
                </thead>
                <tbody>
                  {appLinks.map((link) => (
                    <tr key={link.id}>
                      <td>{targetTypeLabel(link.target_type, t)}</td>
                      <td>{link.target_name || `#${link.target_id}`}</td>
                      <td>
                        <button
                          type="button"
                          onClick={() => handleDeleteLink(link.id)}
                          style={{ background: 'none', border: 'none', cursor: 'pointer', padding: '0.25rem', color: 'var(--status-error)' }}
                          aria-label={t('common.delete')}>
                          <HiTrash size={18} />
                        </button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          );
        })()}
      </div>
      )}

      {showAddCorrelation && selectedService && (
        <AddCorrelationModal
          appName={selectedService}
          hosts={hosts}
          servicesList={servicesList}
          hostGroups={hostGroups}
          appNames={errorApps}
          onSuccess={() => {
            setShowAddCorrelation(false);
            refresh();
          }}
          onCancel={() => setShowAddCorrelation(false)}
        />
      )}

      {editTarget && (
        <EditErrorServiceModal
          service={editTarget}
          onSuccess={(newName) => {
            // Renaming while drilled into that app: follow the new name.
            if (selectedService === editTarget.name && newName !== editTarget.name) {
              const newParams = new URLSearchParams(searchParams);
              newParams.set('service', newName);
              setSearchParams(newParams, { replace: true });
            }
            setEditTarget(null);
            refresh();
          }}
          onCancel={() => setEditTarget(null)}
        />
      )}

      {deleteTarget && (
        <ConfirmDialog
          title={t('common.delete')}
          message={t('metrics_view.delete_service_confirm', { name: deleteTarget.name })}
          onConfirm={handleDeleteService}
          onCancel={() => setDeleteTarget(null)}
        />
      )}

    </div>
  );
}

export default ErrorsView;

interface BreakdownListProps {
  counts: Record<string, number> | undefined;
  title: string;
  onPick: (name: string) => void;
}

// The five largest entries of a count map, each one a drill-down.
function BreakdownList({ counts, title, onPick }: BreakdownListProps) {
  const top = Object.entries(counts || {})
    .sort((a, b) => b[1] - a[1])
    .slice(0, 5);
  return (
    <div style={{ marginTop: '0.75rem' }}>
      {top.map(([name, count]) => (
        <div
          key={name}
          role='button'
          tabIndex={0}
          onClick={() => onPick(name)}
          onKeyDown={(e) => e.key === 'Enter' && onPick(name)}
          title={title}
          style={{
            display: 'flex',
            justifyContent: 'space-between',
            alignItems: 'center',
            fontSize: '0.8125rem',
            padding: '0.375rem 0.375rem',
            borderBottom: '1px solid var(--border-primary)',
            borderRadius: '4px',
            cursor: 'pointer',
          }}
          onMouseEnter={(e) => {
            e.currentTarget.style.background = 'var(--bg-hover)';
          }}
          onMouseLeave={(e) => {
            e.currentTarget.style.background = 'transparent';
          }}>
          <span
            style={{
              color: 'var(--text-secondary)',
              overflow: 'hidden',
              textOverflow: 'ellipsis',
              whiteSpace: 'nowrap',
              marginRight: '0.5rem',
              flex: 1,
            }}>
            {name}
          </span>
          <span
            style={{
              color: 'var(--status-error)',
              fontWeight: 600,
              background: 'var(--status-error-bg)',
              padding: '0.125rem 0.5rem',
              borderRadius: '4px',
              fontSize: '0.75rem',
              flexShrink: 0,
            }}>
            {count}
          </span>
        </div>
      ))}
    </div>
  );
}
