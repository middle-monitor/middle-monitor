import { useState, useMemo, useCallback } from 'react';
import { useTranslation } from 'react-i18next';
import { useParams, useNavigate, Link } from 'react-router-dom';
import { useQuery, useQueryClient } from '@tanstack/react-query';
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
  HiOutlineArrowLeft,
} from 'react-icons/hi2';

import { HiPlus } from 'react-icons/hi';

import { type Host, type ServiceWithResults, type MaintenanceWindow } from '../api';
import { useOrgApi, useOrgQueryScope } from '../hooks/useOrgApi';
import { useQueryRefresh } from '../hooks/useQueryRefresh';
import { invalidateEntityDelete } from '../queryClient';
import { RefreshControl } from '../components/RefreshControl';
import { HostIngestCost } from '../components/HostIngestCost';
import { useUrlState } from '../hooks/useUrlState';
import { useAuth } from '../contexts/AuthContext';
import { useOrgPath } from '../hooks/useOrgPath';
import { useServiceModal } from '../contexts/ServiceModalContext';
import { useDateRange } from '../contexts/DateRangeContext';
import { useDateRangeBounds, useDateRangeKey } from '../hooks/useDateRangeBounds';
import { DateRangePicker } from '../components/DateRangePicker';
import { HiOutlineCalendarDays } from 'react-icons/hi2';
import { AddHostForm } from './MetricsView/components/AddHostForm';
import { HiOutlinePencil } from 'react-icons/hi2';
import { MaintenanceBadge, findMaintenanceForTarget } from '../components/MaintenanceBadge';
import { ServiceSparkline } from '../components/ServiceSparkline';
import { ScheduleDowntimeModal } from '../components/ScheduleDowntimeModal';
import { ConfirmDialog } from '../components/ConfirmDialog';
import { Skeleton, firstLoadCount } from '../components/Skeleton';
import { HiOutlineWrenchScrewdriver, HiOutlineTrash } from 'react-icons/hi2';
import { getServiceStatus, getResultValue } from '../utils/serviceStatus';

import './HostDetailView.css';

// A fresh [] on every render gives each dependent useMemo a new identity and
// makes it recompute every time. One frozen empty list keeps that identity stable.
const EMPTY_LIST: never[] = [];

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

const METRIC_UNIT: Record<string, string> = {
  agent_cpu: '%',
  agent_ram: '%',
  agent_disk: '%',
};

// The headline value for a service: the latest metric value (%) for agent
// metrics, or the latest latency (ms) for checks and network (ping latency).
function getServiceValue(service: ServiceWithResults): { value: number | null; unit: string } {
  const r = service.results?.[0];
  if (!r) return { value: null, unit: '' };
  return { value: getResultValue(service.type, r), unit: METRIC_UNIT[service.type] ?? 'ms' };
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

function HostDetailView() {
  const { t, i18n } = useTranslation();
  const { id } = useParams<{ id: string }>();
  const orgApi = useOrgApi();
  const queryClient = useQueryClient();
  const scope = useOrgQueryScope();
  const { canWrite } = useAuth();
  const { orgPath } = useOrgPath();
  const navigate = useNavigate();
  const { openModal } = useServiceModal();
  const { dateRange, setDateRange, formatDateRangeLabel } = useDateRange();
  const getDateBounds = useDateRangeBounds();
  const dateRangeKey = useDateRangeKey();
  const [calendarOpen, setCalendarOpen] = useState(false);
  const [urlState, setFilter] = useUrlState({ status: 'all', type: 'all', q: '' });
  const statusFilter = urlState.status as 'all' | 'healthy' | 'failing' | 'warning' | 'unknown';
  const typeFilter = urlState.type;
  const search = urlState.q;
  const [showHostModal, setShowHostModal] = useState(false);
  const [showDowntimeModal, setShowDowntimeModal] = useState(false);
  const [showDeleteConfirm, setShowDeleteConfirm] = useState(false);

  // Delete this host, then go back to the host list. The backend refuses (409)
  // while services are still attached to the host.
  const handleDeleteHost = async () => {
    if (!host) return;
    try {
      await orgApi.hosts.delete(host.id);
      // Every date window of this host, not just the one on screen.
      invalidateEntityDelete(queryClient, scope, [scope, 'hosts', 'detail', id]);
      navigate(orgPath('/hosts'));
    } catch (err: any) {
      if (err.response?.status === 409) {
        alert(t('host_detail.delete_blocked_services'));
      } else {
        alert(t('metrics_view.delete_error'));
      }
    } finally {
      setShowDeleteConfirm(false);
    }
  };

  const getTimeAgo = useCallback((dateStr: string): string => {
    const now = new Date();
    const date = new Date(dateStr);
    const diff = Math.floor((now.getTime() - date.getTime()) / 1000);
    if (diff < 60) return t('host_detail.time.seconds', { count: diff });
    const diffMins = Math.floor(diff / 60);
    if (diff < 3600) return t('overview.time.minutes_ago', { count: diffMins });
    const diffHours = Math.floor(diff / 3600);
    if (diff < 86400) return t('overview.time.hours_ago', { count: diffHours });
    return t('overview.time.days_ago', { count: Math.floor(diff / 86400) });
  }, [t]);

  // An open modal or a pending confirmation is a detail row: refreshing under
  // one is what pausing is for.
  const { refetchInterval, buildControl } = useQueryRefresh(
    'host-detail',
    15000,
    showHostModal || showDowntimeModal || showDeleteConfirm,
  );

  const hostQueryKey = [scope, 'hosts', 'detail', id, dateRangeKey];
  const hostQuery = useQuery({
    queryKey: hostQueryKey,
    queryFn: async () => {
      const { startIso, endIso } = getDateBounds();
      const [hostRes, servicesRes, maintRes] = await Promise.all([
        orgApi.hosts.getById(parseInt(id!, 10)),
        orgApi.hosts.getServices(parseInt(id!, 10), {
          start_date: startIso,
          end_date: endIso,
        }),
        orgApi.maintenance.list().catch(() => ({ data: [] })),
      ]);
      return {
        host: hostRes.data as Host,
        // Error-tracking services are managed in the Errors view, not listed per host.
        services: ((servicesRes.data as ServiceWithResults[]) || []).filter((s) => !s.type.startsWith('error_service_')),
        maintenanceWindows: (maintRes.data as MaintenanceWindow[]) || [],
      };
    },
    enabled: !!id,
    // Another date window keeps the current services on screen while it loads.
    // Another host does not: its name and services would show under the new URL.
    placeholderData: (previous, previousQuery) =>
      previousQuery?.queryKey[3] === id ? previous : undefined,
    refetchInterval,
  });

  const host = hostQuery.data?.host ?? null;
  const services = hostQuery.data?.services ?? EMPTY_LIST;
  const maintenanceWindows = hostQuery.data?.maintenanceWindows ?? [];

  const autoRefresh = buildControl({
    query: hostQuery,
    queryKey: hostQueryKey,
    prefix: [scope, 'hosts'],
  });
  const { refresh } = autoRefresh;

  // Compute unique service types for filter
  const serviceTypes = useMemo(() => {
    const types = new Set(services.map((s) => s.type));
    return Array.from(types).sort();
  }, [services]);

  // Filtered services
  const filteredServices = useMemo(() => {
    return services.filter((service) => {
      if (statusFilter !== 'all') {
        const status = getServiceStatus(service);
        if (status !== statusFilter) return false;
      }
      if (typeFilter !== 'all' && service.type !== typeFilter) return false;
      if (search) {
        const q = search.toLowerCase();
        const matchesName = service.name.toLowerCase().includes(q);
        const matchesHost = service.host?.toLowerCase().includes(q);
        const matchesService = service.service?.toLowerCase().includes(q);
        if (!matchesName && !matchesHost && !matchesService) return false;
      }
      return true;
    });
  }, [services, statusFilter, typeFilter, search]);

  // Stats
  const stats = useMemo(() => {
    const s = { total: services.length, healthy: 0, failing: 0, warning: 0, unknown: 0 };
    services.forEach((svc) => {
      const st = getServiceStatus(svc);
      s[st]++;
    });
    return s;
  }, [services]);

  // The stats are counted from the services hostQuery returns, so they wait on it.
  const statValue = firstLoadCount(hostQuery.isPending);

  // Only the results area waits on the first load: a failed refresh keeps the
  // last known host on screen.
  if (hostQuery.isLoadingError) {
    return <div className="error-message">{t('host_detail.error_load')}</div>;
  }

  if (!host && !hostQuery.isPending) {
    return <div className="error-message">{t('host_detail.not_found')}</div>;
  }

  // The host's status is the worst of its services (threshold-aware), not the
  // raw backend status — so a host with any failing/warning service reflects it.
  const hostStatus = stats.failing > 0
    ? 'failing'
    : stats.warning > 0
      ? 'warning'
      : stats.healthy > 0
        ? 'healthy'
        : 'unknown';

  return (
    <div className="host-detail-view">
      {/* Breadcrumb / Back */}
      <div className="host-detail-breadcrumb">
        <Link to={orgPath('/hosts')} className="host-detail-back">
          <HiOutlineArrowLeft />
          {t('host_detail.back_to_hosts')}
        </Link>
      </div>

      {/* Host Header */}
      {host ? (
        <div className="host-detail-header">
          <div className="host-detail-header-left">
            <span className={`hosts-status-dot hosts-status-dot-${hostStatus}`} />
            <div>
              <div style={{ display: 'flex', alignItems: 'center', gap: '0.6rem', flexWrap: 'wrap' }}>
                <h1 className="host-detail-title" style={{ margin: 0 }}>{host.display_name || host.name}</h1>
                {(() => { const m = findMaintenanceForTarget(maintenanceWindows, 'host', host.id); return m ? <MaintenanceBadge window={m} /> : null; })()}
              </div>
              <div className="host-detail-meta">
                <span className="host-detail-meta-item">
                  <HiOutlineServer />
                  {host.host}
                </span>
                <span className="host-detail-meta-sep">&middot;</span>
                <span className="host-detail-meta-item">{host.service}</span>
                <span className="host-detail-meta-sep">&middot;</span>
                <span className="host-detail-meta-item">
                  <HiOutlineClock />
                  {t('host_detail.created_ago', { time: getTimeAgo(host.created_at) })}
                </span>
              </div>
            </div>
          </div>
          <div style={{ display: 'flex', gap: '0.5rem', alignItems: 'center' }}>
            <RefreshControl control={autoRefresh} />
            {canWrite && (
            <button
              className="btn btn-secondary"
              style={{ whiteSpace: 'nowrap', flexShrink: 0, display: 'flex', alignItems: 'center', gap: '0.5rem' }}
              onClick={() => setShowHostModal(true)}
            >
              <HiOutlinePencil />
              <span>{t('common.edit')}</span>
            </button>
            )}
            {canWrite && (
            <button
              className="btn btn-secondary"
              style={{ whiteSpace: 'nowrap', flexShrink: 0, display: 'flex', alignItems: 'center', gap: '0.5rem' }}
              onClick={() => setShowDowntimeModal(true)}
            >
              <HiOutlineWrenchScrewdriver style={{ fontSize: '0.875rem' }} />
              <span>{t('maintenance.schedule')}</span>
            </button>
            )}
            {canWrite && (
            <button
              className="btn btn-primary"
              style={{ whiteSpace: 'nowrap', flexShrink: 0, display: 'flex', alignItems: 'center', gap: '0.5rem' }}
              onClick={() => openModal({ host_id: host.id, service: host.service }, [host])}
            >
              <HiPlus style={{ fontSize: '0.875rem' }} />
              <span>{t('services.new_service')}</span>
            </button>
            )}
            {canWrite && (
            <button
              className="btn btn-danger"
              style={{ whiteSpace: 'nowrap', flexShrink: 0, display: 'flex', alignItems: 'center', gap: '0.5rem' }}
              onClick={() => setShowDeleteConfirm(true)}
            >
              <HiOutlineTrash style={{ fontSize: '0.875rem' }} />
              <span>{t('common.delete')}</span>
            </button>
            )}
          </div>
        </div>
      ) : (
        <Skeleton rows={2} silent />
      )}

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
          className="host-detail-date-trigger"
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
            onChange={(e) => setFilter({ q: e.target.value })}
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
          {host
            ? t('host_detail.services_title', {
                host: host.display_name || host.name,
                count: filteredServices.length,
              })
            : t('services.title')}
        </div>

        {hostQuery.isPending ? (
          <Skeleton rows={6} />
        ) : filteredServices.length === 0 ? (
          <div className="empty-state">
            <HiOutlineCube className="empty-state-icon" />
            <div className="empty-state-title">{t('services.empty.title')}</div>
            <div className="empty-state-description">
              {statusFilter !== 'all' || search || typeFilter !== 'all'
                ? t('services.empty.desc_filtered')
                : t('host_detail.empty.desc_host')}
            </div>
          </div>
        ) : (
          <div className="services-table-wrapper">
            <table className="table services-table">
              <thead>
                <tr>
                  <th>{t('services.table.status')}</th>
                  <th>{t('host_detail.table.service')}</th>
                  <th>{t('services.table.type')}</th>
                  <th>{t('host_detail.table.value')}</th>
                  <th>{t('services.table.history')}</th>
                </tr>
              </thead>
              <tbody>
                {filteredServices.map((service) => {
                  const status = getServiceStatus(service);
                  const { value, unit } = getServiceValue(service);
                  const lastResult =
                    service.results && service.results.length > 0
                      ? service.results[0]
                      : null;
                  const svcMaint = findMaintenanceForTarget(maintenanceWindows, 'service', service.id);

                  return (
                    <tr
                      key={service.id}
                      className={`services-row services-row-${status} services-row-clickable`}
                      onClick={() => {
                        if (service.type.startsWith('error_service_')) {
                          navigate(orgPath(`/errors?service=${service.name}`));
                        } else {
                          navigate(orgPath(`/services/${service.id}`), { state: { fromHostId: host?.id, fromHostName: host?.name } });
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
                            {svcMaint && <MaintenanceBadge window={svcMaint} />}
                          </div>
                          <span className="services-host-url">
                            {service.host}
                            {service.path ? service.path : ''}
                          </span>
                        </div>
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
                        {value !== null ? (
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
                            ? `${t('services.table.last_checked')}: ${new Date(lastResult.timestamp).toLocaleString(i18n.language)}`
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

      {host && <HostIngestCost hostId={host.id} />}

      {showDowntimeModal && host && (
        <ScheduleDowntimeModal
          targetType="host"
          targetId={host.id}
          targetName={host.display_name || host.name}
          onClose={() => setShowDowntimeModal(false)}
          onCreated={refresh}
        />
      )}

      {showDeleteConfirm && host && (
        <ConfirmDialog
          title={t('common.delete')}
          message={t('metrics_view.delete_host_confirm', { name: host.display_name || host.name })}
          onConfirm={handleDeleteHost}
          onCancel={() => setShowDeleteConfirm(false)}
        />
      )}

      {showHostModal && host && (
        <div className="modal-overlay" onClick={(e) => { if (e.target === e.currentTarget) setShowHostModal(false); }}>
          <div className="modal-content" style={{ maxWidth: '600px' }} onClick={(e) => e.stopPropagation()}>
            <div className="modal-header">
              <h3 className="modal-title" style={{ display: 'flex', alignItems: 'center', gap: '0.5rem' }}>
                <HiOutlinePencil style={{ color: 'var(--brand-primary)' }} />
                {t('host_detail.edit_host')}
              </h3>
              <button className="modal-close" onClick={() => setShowHostModal(false)}>
                &times;
              </button>
            </div>
            <div className="modal-body">
              <AddHostForm
                onSuccess={() => {
                  setShowHostModal(false);
                  refresh();
                }}
                onCancel={() => setShowHostModal(false)}
                host={host}
              />
            </div>
          </div>
        </div>
      )}
    </div>
  );
}

export default HostDetailView;
