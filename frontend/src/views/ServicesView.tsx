import { useState, useEffect } from 'react';
import { useNavigate } from 'react-router-dom';
import { keepPreviousData, useQuery } from '@tanstack/react-query';
import {
  HiOutlineCube,
  HiOutlineCheckCircle,
  HiOutlineXCircle,
  HiOutlineExclamationTriangle,
  HiOutlineClock,
  HiOutlineSignal,
  HiOutlineGlobeAlt,
  HiOutlineServerStack,
  HiOutlineCpuChip,
  HiOutlineDocumentText,
  HiOutlineShieldCheck,
  HiOutlineMagnifyingGlass,
  HiOutlineServer,
} from 'react-icons/hi2';

import { type ServiceWithResults, type MaintenanceWindow, type ServiceStats } from '../api';
import { HiPlus } from 'react-icons/hi';
import { Pagination } from '../components/Pagination';
import { Skeleton, firstLoadCount } from '../components/Skeleton';
import { useOrgApi, useOrgQueryScope } from '../hooks/useOrgApi';
import { useQueryRefresh } from '../hooks/useQueryRefresh';
import { RefreshControl } from '../components/RefreshControl';
import { useUrlState, pageFromUrl, pageToUrl } from '../hooks/useUrlState';
import { useAuth } from '../contexts/AuthContext';
import { useOrgPath } from '../hooks/useOrgPath';
import { useServiceModal } from '../contexts/ServiceModalContext';
import { useDateRange } from '../contexts/DateRangeContext';
import { useDateRangeBounds, useDateRangeKey } from '../hooks/useDateRangeBounds';
import { DateRangePicker } from '../components/DateRangePicker';
import { HiOutlineCalendarDays } from 'react-icons/hi2';
import { MaintenanceBadge, findMaintenanceForTarget } from '../components/MaintenanceBadge';
import { ServiceSparkline } from '../components/ServiceSparkline';
import { getServiceStatus, getResultValue } from '../utils/serviceStatus';

import './ServicesView.css';

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

const SERVICE_TYPE_LABELS: Record<string, string> = {
  http: 'HTTP',
  sql: 'SQL',
  snmp: 'SNMP',
  certificate: 'Certificate',
  agent_cpu: 'CPU',
  agent_ram: 'RAM',
  agent_disk: 'Disk',
  agent_network: 'Network',
};

const AGENT_METRIC_TYPES = ['agent_cpu', 'agent_ram', 'agent_disk', 'agent_network'];

const METRIC_UNIT: Record<string, string> = {
  agent_cpu: '%',
  agent_ram: '%',
  agent_disk: '%',
};

// Headline value: latest metric value (%) for agent metrics, latency (ms) for
// checks and network (ping latency from metadata).
function getServiceValue(service: ServiceWithResults): { value: number | null; unit: string } {
  const r = service.results?.[0];
  if (!r) return { value: null, unit: '' };
  return { value: getResultValue(service.type, r), unit: METRIC_UNIT[service.type] ?? 'ms' };
}

function getTimeAgo(dateStr: string): string {
  const now = new Date();
  const date = new Date(dateStr);
  const diff = Math.floor((now.getTime() - date.getTime()) / 1000);
  if (diff < 60) return `${diff}s`;
  if (diff < 3600) return `${Math.floor(diff / 60)}min`;
  if (diff < 86400) return `${Math.floor(diff / 3600)}h`;
  return `${Math.floor(diff / 86400)}d`; // 'd' applies internationally for simplicity or can be localized later.
}

function getTypeLabel(type: string): string {
  if (type.startsWith('error_service_')) {
    return `Error (${type.replace('error_service_', '')})`;
  }
  return SERVICE_TYPE_LABELS[type] || type;
}

function getTypeIcon(type: string): React.ReactNode {
  if (type.startsWith('error_service_')) return <HiOutlineExclamationTriangle />;
  return SERVICE_TYPE_ICONS[type] || <HiOutlineDocumentText />;
}

// Uptime percentage from the last results
const VALID_STATUSES = ['all', 'healthy', 'failing', 'warning', 'unknown'] as const;

const EMPTY_STATS: ServiceStats = { total: 0, healthy: 0, failing: 0, warning: 0, unknown: 0, types: [] };

import { useTranslation } from 'react-i18next';

function ServicesView() {
  const { t, i18n } = useTranslation();
  const orgApi = useOrgApi();
  const scope = useOrgQueryScope();
  const { canWrite } = useAuth();
  const { orgPath } = useOrgPath();
  const { openModal, isOpen: serviceModalOpen } = useServiceModal();
  const { dateRange, setDateRange, formatDateRangeLabel } = useDateRange();
  const getDateBounds = useDateRangeBounds();
  const dateRangeKey = useDateRangeKey();
  const navigate = useNavigate();
  const [urlState, setUrlState] = useUrlState({ status: 'all', type: 'all', q: '', page: '1' });
  const page = pageFromUrl(urlState.page);
  const setPage = (next: number) => setUrlState({ page: pageToUrl(next) });
  // A filter change invalidates the page number: page 4 of "failing" has
  // nothing to do with page 4 of "all".
  const setFilter = (next: { status?: string; type?: string; q?: string }) => setUrlState({ ...next, page: '1' });
  const statusFilter = VALID_STATUSES.includes(urlState.status as typeof VALID_STATUSES[number])
    ? (urlState.status as typeof VALID_STATUSES[number])
    : 'all';
  const typeFilter = urlState.type;
  const search = urlState.q;
  const pageSize = 50;
  const [calendarOpen, setCalendarOpen] = useState(false);
  const [openingServiceModal, setOpeningServiceModal] = useState(false);

  // Debounced so typing in the search box does not fire a request per keystroke.
  // The page reset rides the same timer: dropping it on the keystroke would
  // refetch the previous search at page 0 first. Equal values mean a page
  // restored from a link, which must survive.
  const [debouncedSearch, setDebouncedSearch] = useState(search);
  useEffect(() => {
    if (search === debouncedSearch) return;
    const handle = setTimeout(() => { setDebouncedSearch(search); setPage(0); }, 250);
    return () => clearTimeout(handle);
  }, [search, debouncedSearch]); // eslint-disable-line react-hooks/exhaustive-deps -- setPage is rebuilt on every render

  const handleAddService = async () => {
    setOpeningServiceModal(true);
    try {
      const response = await orgApi.hosts.list();
      const list = response.data || [];
      openModal(null, list);
    } catch (err) {
      console.error(err);
    } finally {
      setOpeningServiceModal(false);
    }
  };

  // The service modal is a detail row: refreshing under an open form is what
  // pausing is for.
  const { refetchInterval, buildControl } = useQueryRefresh('services', 15000, serviceModalOpen);

  const servicesQueryKey = [scope, 'services', 'list', debouncedSearch, statusFilter, typeFilter, page, dateRangeKey];
  const servicesQuery = useQuery({
    queryKey: servicesQueryKey,
    queryFn: async () => {
      const { startIso, endIso } = getDateBounds();
      const [svcRes, maintRes] = await Promise.all([
        orgApi.services.list({
          start_date: startIso,
          end_date: endIso,
          search: debouncedSearch || undefined,
          status: statusFilter,
          type: typeFilter,
          limit: pageSize,
          offset: page * pageSize,
        }),
        orgApi.maintenance.list().catch(() => ({ data: [] })),
      ]);
      return {
        services: (svcRes.data as ServiceWithResults[]) || [],
        total: Number(svcRes.headers['x-total-count']) || 0,
        maintenanceWindows: (maintRes.data as MaintenanceWindow[]) || [],
      };
    },
    // The current page stays on screen while the next one loads.
    placeholderData: keepPreviousData,
    refetchInterval,
  });

  // Facets on mount + periodic refresh alongside the service list. Best-effort:
  // the stat cards are a secondary panel, and their failure must not report the
  // whole view as stale nor stop its cadence.
  const statsQuery = useQuery({
    queryKey: [scope, 'services', 'stats'],
    queryFn: async () => (await orgApi.services.stats()).data ?? EMPTY_STATS,
    refetchInterval,
  });

  const services = servicesQuery.data?.services ?? [];
  const total = servicesQuery.data?.total ?? 0;
  const maintenanceWindows = servicesQuery.data?.maintenanceWindows ?? [];
  const stats = statsQuery.data ?? EMPTY_STATS;

  const autoRefresh = buildControl({
    query: servicesQuery,
    queryKey: servicesQueryKey,
    prefix: [scope, 'services'],
  });

  // Distinct service types (global) for the type filter dropdown.
  const serviceTypes = stats.types;

  // The stat cards and the subtitle read statsQuery, the table title reads the
  // list: two requests, two first loads, so each count waits on its own.
  const statValue = firstLoadCount(statsQuery.isPending);
  const listValue = firstLoadCount(servicesQuery.isPending);

  // Only the results area waits on the first load: a background refresh, and a
  // refresh that fails while data is already on screen, keep the list visible.
  if (servicesQuery.isLoadingError) {
    return <div className="error-message">{t('services.load_error')}</div>;
  }

  return (
    <div className="services-view">
      {/* Page Header */}
      <div
        className="page-header"
        style={{
          display: 'flex',
          justifyContent: 'space-between',
          alignItems: 'flex-start',
        }}
      >
        <div>
          <h1 className="page-title">{t('services.title')}</h1>
          <p className="page-subtitle">
            {t('services.subtitle', { count: stats.total, value: statValue(stats.total) })}
          </p>
        </div>
        <div style={{ display: 'flex', alignItems: 'center', gap: '0.75rem', flexShrink: 0 }}>
          <RefreshControl control={autoRefresh} />
          {canWrite && (
            <button
              type="button"
              onClick={handleAddService}
              className="btn btn-primary"
              disabled={openingServiceModal}
              style={{ whiteSpace: 'nowrap', flexShrink: 0 }}
            >
              <HiPlus style={{ fontSize: '0.875rem' }} />
              <span>{t('services.new_service')}</span>
            </button>
          )}
        </div>
      </div>

      {/* Stats Cards */}
      <div className="services-stats">
        <button
          className={`services-stat-card ${statusFilter === 'all' ? 'active' : ''}`}
          onClick={() => setFilter({ status: 'all' })}
        >
          <div className="services-stat-icon">
            <HiOutlineCube />
          </div>
          <div className="services-stat-content">
            <div className="services-stat-value">{statValue(stats.total)}</div>
            <div className="services-stat-label">{t('services.stats.total')}</div>
          </div>
        </button>

        <button
          className={`services-stat-card services-stat-healthy ${statusFilter === 'healthy' ? 'active' : ''}`}
          onClick={() => setFilter({ status: 'healthy' })}
        >
          <div className="services-stat-icon">
            <HiOutlineCheckCircle />
          </div>
          <div className="services-stat-content">
            <div className="services-stat-value">{statValue(stats.healthy)}</div>
            <div className="services-stat-label">{t('services.stats.healthy')}</div>
          </div>
        </button>

        <button
          className={`services-stat-card services-stat-failing ${statusFilter === 'failing' ? 'active' : ''}`}
          onClick={() => setFilter({ status: 'failing' })}
        >
          <div className="services-stat-icon">
            <HiOutlineXCircle />
          </div>
          <div className="services-stat-content">
            <div className="services-stat-value">{statValue(stats.failing)}</div>
            <div className="services-stat-label">{t('services.stats.failing')}</div>
          </div>
        </button>

        <button
          className={`services-stat-card services-stat-warning ${statusFilter === 'warning' ? 'active' : ''}`}
          onClick={() => setFilter({ status: 'warning' })}
        >
          <div className="services-stat-icon">
            <HiOutlineExclamationTriangle />
          </div>
          <div className="services-stat-content">
            <div className="services-stat-value">{statValue(stats.warning)}</div>
            <div className="services-stat-label">{t('services.stats.warning')}</div>
          </div>
        </button>

        {stats.unknown > 0 && (
          <button
            className={`services-stat-card services-stat-unknown ${statusFilter === 'unknown' ? 'active' : ''}`}
            onClick={() => setFilter({ status: 'unknown' })}
          >
            <div className="services-stat-icon">
              <HiOutlineClock />
            </div>
            <div className="services-stat-content">
              <div className="services-stat-value">{statValue(stats.unknown)}</div>
              <div className="services-stat-label">{t('services.stats.unknown')}</div>
            </div>
          </button>
        )}
      </div>

      {/* Filters bar */}
      <div className="services-filters">
        <button
          type="button"
          className="services-date-trigger"
          onClick={() => setCalendarOpen(true)}
          title={t('services.filters.compliance_period')}
        >
          <HiOutlineCalendarDays />
          {formatDateRangeLabel(dateRange, { withTime: true })}
        </button>
        <DateRangePicker
          isOpen={calendarOpen}
          value={dateRange}
          onChange={(range) => setDateRange(range)}
          onClose={() => setCalendarOpen(false)}
          showTime
        />
        <div className="services-search">
          <HiOutlineMagnifyingGlass className="services-search-icon" />
          <input
            type="text"
            placeholder={t('services.filters.search_placeholder')}
            value={search}
            onChange={(e) => setUrlState({ q: e.target.value })}
            className="services-search-input"
          />
        </div>
        <select
          className="services-type-filter"
          value={typeFilter}
          onChange={(e) => setFilter({ type: e.target.value })}
        >
          <option value="all">{t('services.filters.all_types')}</option>
          {serviceTypes.map((type) => (
            <option key={type} value={type}>
              {getTypeLabel(type)}
            </option>
          ))}
        </select>
      </div>

      {/* Services Table */}
      <div className="card">
        <div className="card-title">
          <HiOutlineCube className="card-title-icon" />
          Services ({listValue(total)})
        </div>

        {servicesQuery.isPending ? (
          <Skeleton rows={8} />
        ) : services.length === 0 ? (
          <div className="empty-state">
            <HiOutlineCube className="empty-state-icon" />
            <div className="empty-state-title">{t('services.empty.title')}</div>
            <div className="empty-state-description">
              {statusFilter !== 'all' || search || typeFilter !== 'all'
                ? t('services.empty.desc_filtered')
                : t('services.empty.desc_all')}
            </div>
          </div>
        ) : (
          <div className="services-table-wrapper">
            <table className="table services-table">
              <thead>
                <tr>
                  <th>{t('services.table.status')}</th>
                  <th>{t('services.table.name')}</th>
                  <th>{t('services.table.host')}</th>
                  <th>{t('services.table.type')}</th>
                  <th>{t('host_detail.table.value')}</th>
                  <th>{t('services.table.history')}</th>
                </tr>
              </thead>
              <tbody>
                {services.map((service) => {
                  const status = getServiceStatus(service);
                  const { value, unit } = getServiceValue(service);
                  const lastResult =
                    service.results && service.results.length > 0
                      ? service.results[0]
                      : null;
                  const maint = findMaintenanceForTarget(maintenanceWindows, 'service', service.id);

                  let certExpiresAt: Date | null = null;
                  if (service.type === 'certificate' && lastResult?.metadata) {
                    try {
                      const meta = JSON.parse(lastResult.metadata);
                      if (meta.expires_at) certExpiresAt = new Date(meta.expires_at);
                    } catch { /* invalid JSON */ }
                  }

                  return (
                    <tr
                      key={service.id}
                      className={`services-row services-row-${status} services-row-clickable`}
                      onClick={() => {
                        if (service.type.startsWith('error_service_')) {
                          navigate(orgPath(`/errors?service=${service.name}`));
                        } else {
                          navigate(orgPath(`/services/${service.id}`));
                        }
                      }}
                    >
                      {/* Status indicator */}
                      <td className="services-status-cell">
                        <span
                          className={`services-status-dot services-status-dot-${status}`}
                          title={
                            status === 'healthy'
                              ? t('services.stats.healthy')
                              : status === 'failing'
                                ? t('services.stats.failing')
                                : status === 'warning'
                                  ? t('services.stats.warning')
                                  : t('services.stats.unknown')
                          }
                        />
                      </td>

                      {/* Service name + host URL */}
                      <td>
                        <div className="services-name-cell">
                          <div style={{ display: 'flex', alignItems: 'center', gap: '0.4rem', flexWrap: 'wrap' }}>
                            <span className="services-name">{service.name}</span>
                            {maint && <MaintenanceBadge window={maint} />}
                          </div>
                          {!AGENT_METRIC_TYPES.includes(service.type) && (
                            <span className="services-host-url">
                              {service.host}
                              {service.path ? service.path : ''}
                            </span>
                          )}
                        </div>
                      </td>

                      {/* Host name */}
                      <td>
                        {service.host_name ? (
                          <span className="services-host-name">
                            <HiOutlineServer />
                            {service.host_name}
                          </span>
                        ) : (
                          <span className="services-host-name services-host-name-none">
                            —
                          </span>
                        )}
                      </td>

                      {/* Type */}
                      <td>
                        <span className="services-type-badge">
                          {getTypeIcon(service.type)}
                          {getTypeLabel(service.type)}
                        </span>
                      </td>

                      {/* Current value (CPU/RAM/Disk % or check latency), colored by status */}
                      <td>
                        {service.type === 'certificate' ? (
                          certExpiresAt ? (
                            <span style={{
                              fontSize: '0.8125rem',
                              color: status === 'failing' ? 'var(--status-error)' : 'var(--text-secondary)',
                              fontVariantNumeric: 'tabular-nums',
                            }}>
                              {t('metrics_view.certificate_display.expires_on', {
                                date: certExpiresAt.toLocaleDateString(i18n.language, {
                                  day: 'numeric', month: 'short', year: 'numeric',
                                }),
                              })}
                            </span>
                          ) : (
                            <span className="services-uptime services-uptime-na">—</span>
                          )
                        ) : value !== null ? (
                          <span
                            style={{
                              fontWeight: 700,
                              fontVariantNumeric: 'tabular-nums',
                              color:
                                status === 'failing'
                                  ? 'var(--status-error)'
                                  : status === 'warning'
                                    ? 'var(--status-warning)'
                                    : 'var(--status-success)',
                            }}
                          >
                            {unit === 'ms'
                              ? (value < 1000 ? `${Math.round(value)}ms` : `${(value / 1000).toFixed(1)}s`)
                              : `${value.toFixed(value < 10 ? 1 : 0)}${unit}`}
                          </span>
                        ) : (
                          <span className="services-uptime services-uptime-na">—</span>
                        )}
                      </td>

                      {/* Recent values sparkline; tooltip keeps the last check time */}
                      <td
                        title={
                          lastResult
                            ? `${t('services.table.last_checked')}: ${t('services.table_cells.ago', { time: getTimeAgo(lastResult.timestamp) })}`
                            : undefined
                        }
                      >
                        <ServiceSparkline
                          serviceType={service.type}
                          results={service.results || []}
                          status={status}
                        />
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}
      </div>

      <Pagination page={page} pageSize={pageSize} total={total} onPageChange={setPage} />
    </div>
  );
}

export default ServicesView;
