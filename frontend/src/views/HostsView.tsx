import { useState, useEffect, useRef } from 'react';
import { useNavigate } from 'react-router-dom';
import { keepPreviousData, useQuery } from '@tanstack/react-query';
import {
  HiOutlineServer,
  HiOutlineCheckCircle,
  HiOutlineXCircle,
  HiOutlineExclamationTriangle,
  HiOutlineCube,
  HiOutlineMagnifyingGlass,
  HiOutlineClock,
} from 'react-icons/hi2';
import { HiPlus, HiX } from 'react-icons/hi';

import { type Host, type MaintenanceWindow, type HostGroup, type HostStats } from '../api';
import { useOrgApi, useOrgQueryScope } from '../hooks/useOrgApi';
import { useUrlState, pageFromUrl, pageToUrl } from '../hooks/useUrlState';
import { useQueryRefresh } from '../hooks/useQueryRefresh';
import { RefreshControl } from '../components/RefreshControl';
import { useAuth } from '../contexts/AuthContext';
import { useOrgPath } from '../hooks/useOrgPath';
import { AddHostForm } from './MetricsView/components/AddHostForm';
import { MaintenanceBadge, findMaintenanceForTarget } from '../components/MaintenanceBadge';
import { Pagination } from '../components/Pagination';
import { Skeleton, firstLoadCount } from '../components/Skeleton';

import './HostsView.css';

function getHostStatus(host: Host): 'healthy' | 'failing' | 'warning' | 'unknown' {
  const failCount = (host.failing_count ?? 0) + (host.critical_count ?? 0);
  const warnCount = host.warning_count ?? 0;
  const healthyCount = host.healthy_count ?? 0;
  const totalServices = host.service_count ?? 0;

  if (totalServices > 0) {
    if (failCount > 0) return 'failing';
    if (warnCount > 0) return 'warning';
    if (healthyCount > 0) return 'healthy';
    return 'unknown';
  }

  if (!host.status) return 'unknown';
  if (host.status === 'success') return 'healthy';
  if (host.status === 'failure' || host.status === 'critical') return 'failing';
  if (host.status === 'warning') return 'warning';
  return 'unknown';
}

function getTimeAgo(dateStr: string): string {
  const now = new Date();
  const date = new Date(dateStr);
  const diff = Math.floor((now.getTime() - date.getTime()) / 1000);
  if (diff < 60) return `${diff}s`;
  if (diff < 3600) return `${Math.floor(diff / 60)}min`;
  if (diff < 86400) return `${Math.floor(diff / 3600)}h`;
  return `${Math.floor(diff / 86400)}d`;
}

import { useTranslation } from 'react-i18next';

// ... other code ...

const EMPTY_STATS: HostStats = { total: 0, healthy: 0, failing: 0, warning: 0, unknown: 0, total_services: 0 };

function HostsView() {
  const { t } = useTranslation();
  const orgApi = useOrgApi();
  const scope = useOrgQueryScope();
  const { canWrite } = useAuth();
  const { orgPath } = useOrgPath();
  const navigate = useNavigate();
  const [urlState, setUrlState] = useUrlState({ status: 'all', q: '', page: '1' });
  const page = pageFromUrl(urlState.page);
  const setPage = (next: number) => setUrlState({ page: pageToUrl(next) });
  // A filter change invalidates the page number: page 4 of "failing" has
  // nothing to do with page 4 of "all". The search box resets the page from its
  // debounce instead, so that the query key moves only once.
  const setFilter = (next: { status?: string }) => setUrlState({ ...next, page: '1' });
  const statusFilter = urlState.status as 'all' | 'healthy' | 'failing' | 'warning' | 'unknown';
  const search = urlState.q;
  const pageSize = 50;
  const [debouncedSearch, setDebouncedSearch] = useState(search);
  const [showHostModal, setShowHostModal] = useState(false);
  const [editingHost, setEditingHost] = useState<Host | null>(null);
  // The modal is a detail row: refreshing under an open form is what pausing
  // is for.
  const { refetchInterval, buildControl } = useQueryRefresh('hosts', 15000, showHostModal);

  // Debounced so typing in the search box does not fire a request per keystroke.
  // The page reset is committed in the same tick as the term: resetting it on
  // the keystroke would point the key at page 0 of the previous term, and
  // resetting it after the term would send the new term to the old offset.
  useEffect(() => {
    if (search === debouncedSearch) return;
    const handle = setTimeout(() => {
      setDebouncedSearch(search);
      setPage(0);
    }, 250);
    return () => clearTimeout(handle);
    // setPage writes the URL and is rebuilt on every render.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [search, debouncedSearch]);

  const hostsQueryKey = [scope, 'hosts', 'list', debouncedSearch, statusFilter, page];
  const hostsQuery = useQuery({
    queryKey: hostsQueryKey,
    queryFn: async () => {
      const [hostsRes, maintRes, groupsRes] = await Promise.all([
        orgApi.hosts.list({ search: debouncedSearch || undefined, status: statusFilter, limit: pageSize, offset: page * pageSize }),
        orgApi.maintenance.list().catch(() => ({ data: [] })),
        orgApi.hostGroups.list().catch(() => ({ data: [] })),
      ]);
      return {
        hosts: hostsRes.data || [],
        total: Number(hostsRes.headers['x-total-count']) || 0,
        maintenanceWindows: (maintRes.data as MaintenanceWindow[]) || [],
        hostGroups: (groupsRes.data as HostGroup[]) || [],
      };
    },
    // The current page stays on screen while the next one loads.
    placeholderData: keepPreviousData,
    refetchInterval,
  });

  // Facets on mount + periodic refresh alongside the host list.
  const statsQuery = useQuery({
    queryKey: [scope, 'hosts', 'stats'],
    queryFn: async () => (await orgApi.hosts.stats()).data ?? EMPTY_STATS,
    refetchInterval,
  });

  // keepPreviousData is dropped as soon as a fetch errors, so a page change that
  // fails would leave the view with no data at all; hold the last page that
  // rendered and show the failure next to it.
  const lastLoaded = useRef(hostsQuery.data);
  if (hostsQuery.data) lastLoaded.current = hostsQuery.data;
  const loaded = hostsQuery.data ?? lastLoaded.current;

  const hosts = loaded?.hosts ?? [];
  const total = loaded?.total ?? 0;
  const maintenanceWindows = loaded?.maintenanceWindows ?? [];
  const hostGroups = loaded?.hostGroups ?? [];
  const stats = statsQuery.data ?? EMPTY_STATS;

  // The stat cards are a secondary panel: their own failure must not report the
  // list as stale, nor stop its cadence.
  const control = buildControl({
    query: hostsQuery,
    queryKey: hostsQueryKey,
    prefix: [scope, 'hosts'],
  });
  const refreshHosts = control.refresh;

  const openAddHostModal = () => {
    setEditingHost(null);
    setShowHostModal(true);
  };

  const closeHostModal = () => {
    setShowHostModal(false);
    setEditingHost(null);
  };

  const handleAssignGroup = async (hostId: number, groupId: number) => {
    try {
      await orgApi.hosts.assignGroup(hostId, groupId);
      refreshHosts();
    } catch (err) {
      console.error(err);
    }
  };

  // The stat cards and the subtitle read statsQuery, the table title reads the
  // list: two requests, two first loads, so each count waits on its own.
  const statValue = firstLoadCount(statsQuery.isPending);
  const listValue = firstLoadCount(hostsQuery.isPending);

  // Only the results area waits on the first load: a background refresh, and a
  // refresh that fails while data is already on screen, keep the list visible.
  if (hostsQuery.isLoadingError && !loaded) {
    return <div className="error-message">{t('common.error')}</div>;
  }

  return (
    <div className="hosts-view">
      {/* Page Header */}
      <div className="page-header" style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start' }}>
        <div>
          <h1 className="page-title">{t('hosts.title')}</h1>
          <p className="page-subtitle">
            {t('hosts.subtitle', { total: statValue(stats.total), services: statValue(stats.total_services) })}
          </p>
        </div>
        <div style={{ display: 'flex', alignItems: 'center', gap: '0.75rem', flexShrink: 0 }}>
          <RefreshControl control={control} />
          {canWrite && (
            <button
              onClick={openAddHostModal}
              className="btn btn-primary"
              style={{ whiteSpace: 'nowrap', flexShrink: 0 }}
            >
              <HiPlus style={{ fontSize: '0.875rem' }} />
              <span>{t('hosts.new_host')}</span>
            </button>
          )}
        </div>
      </div>

      {/* Stats Cards */}
      <div className="hosts-stats">
        <button
          className={`hosts-stat-card ${statusFilter === 'all' ? 'active' : ''}`}
          onClick={() => setFilter({ status: 'all' })}
        >
          <div className="hosts-stat-icon">
            <HiOutlineServer />
          </div>
          <div className="hosts-stat-content">
            <div className="hosts-stat-value">{statValue(stats.total)}</div>
            <div className="hosts-stat-label">{t('hosts.stats.total')}</div>
          </div>
        </button>

        <button
          className={`hosts-stat-card hosts-stat-healthy ${statusFilter === 'healthy' ? 'active' : ''}`}
          onClick={() => setFilter({ status: 'healthy' })}
        >
          <div className="hosts-stat-icon">
            <HiOutlineCheckCircle />
          </div>
          <div className="hosts-stat-content">
            <div className="hosts-stat-value">{statValue(stats.healthy)}</div>
            <div className="hosts-stat-label">{t('hosts.stats.healthy')}</div>
          </div>
        </button>

        <button
          className={`hosts-stat-card hosts-stat-failing ${statusFilter === 'failing' ? 'active' : ''}`}
          onClick={() => setFilter({ status: 'failing' })}
        >
          <div className="hosts-stat-icon">
            <HiOutlineXCircle />
          </div>
          <div className="hosts-stat-content">
            <div className="hosts-stat-value">{statValue(stats.failing)}</div>
            <div className="hosts-stat-label">{t('hosts.stats.failing')}</div>
          </div>
        </button>

        {stats.warning > 0 && (
          <button
            className={`hosts-stat-card hosts-stat-warning ${statusFilter === 'warning' ? 'active' : ''}`}
            onClick={() => setFilter({ status: 'warning' })}
          >
            <div className="hosts-stat-icon">
              <HiOutlineExclamationTriangle />
            </div>
            <div className="hosts-stat-content">
              <div className="hosts-stat-value">{statValue(stats.warning)}</div>
              <div className="hosts-stat-label">{t('hosts.stats.warning')}</div>
            </div>
          </button>
        )}

        {stats.unknown > 0 && (
          <button
            className={`hosts-stat-card hosts-stat-unknown ${statusFilter === 'unknown' ? 'active' : ''}`}
            onClick={() => setFilter({ status: 'unknown' })}
          >
            <div className="hosts-stat-icon">
              <HiOutlineClock />
            </div>
            <div className="hosts-stat-content">
              <div className="hosts-stat-value">{statValue(stats.unknown)}</div>
              <div className="hosts-stat-label">{t('hosts.stats.unknown')}</div>
            </div>
          </button>
        )}
      </div>

      {/* Filters bar */}
      <div className="hosts-filters">
        <div className="hosts-search">
          <HiOutlineMagnifyingGlass className="hosts-search-icon" />
          <input
            type="text"
            placeholder={t('hosts.filters.search_placeholder')}
            value={search}
            onChange={(e) => setUrlState({ q: e.target.value })}
            className="hosts-search-input"
          />
        </div>
      </div>

      {/* Hosts Table */}
      <div className="card">
        <div className="card-title">
          <HiOutlineServer className="card-title-icon" />
          Hosts ({listValue(total)})
        </div>

        {hostsQuery.isPending ? (
          <Skeleton rows={8} />
        ) : hosts.length === 0 ? (
          <div className="empty-state">
            <HiOutlineServer className="empty-state-icon" />
            <div className="empty-state-title">{t('hosts.empty.title')}</div>
            <div className="empty-state-description">
              {statusFilter !== 'all' || search
                ? t('hosts.empty.desc_filtered')
                : t('hosts.empty.desc_all')}
            </div>
          </div>
        ) : (
          <div className="hosts-table-wrapper">
            <table className="table hosts-table">
              <thead>
                <tr>
                  <th>{t('hosts.table.status')}</th>
                  <th>{t('hosts.table.name')}</th>
                  <th>{t('hosts.table.address')}</th>
                  <th>{t('hosts.table.service')}</th>
                  <th>{t('hosts.table.group')}</th>
                  <th>{t('hosts.table.services')}</th>
                  <th>{t('hosts.table.health')}</th>
                  <th>{t('hosts.table.created')}</th>
                </tr>
              </thead>
              <tbody>
                {hosts.map((host) => {
                  const status = getHostStatus(host);
                  const serviceCount = host.service_count || 0;
                  const healthyCount = host.healthy_count || 0;
                  const failingCount = host.failing_count || 0;
                  const warningCount = host.warning_count || 0;
                  const criticalCount = host.critical_count || 0;
                  const maint = findMaintenanceForTarget(maintenanceWindows, 'host', host.id);

                  return (
                    <tr
                      key={host.id}
                      className={`hosts-row hosts-row-${status} hosts-row-clickable`}
                      onClick={() => navigate(orgPath(`/hosts/${host.id}`))}
                    >
                      <td className="hosts-status-cell">
                        <span
                          className={`hosts-status-dot hosts-status-dot-${status}`}
                          title={
                            status === 'healthy'
                              ? t('hosts.stats.healthy')
                              : status === 'failing'
                                ? t('hosts.stats.failing')
                                : status === 'warning'
                                  ? t('hosts.stats.warning')
                                  : t('hosts.stats.unknown')
                          }
                        />
                      </td>
                      <td>
                        <div className="hosts-name-cell" style={{ display: 'flex', alignItems: 'center', gap: '0.5rem', flexWrap: 'wrap' }}>
                          <span className="hosts-name">{host.display_name || host.name}</span>
                          {maint && <MaintenanceBadge window={maint} />}
                        </div>
                      </td>
                      <td>
                        <span className="hosts-address">{host.host}</span>
                      </td>
                      <td>
                        <span className="hosts-service-badge">{host.service}</span>
                      </td>
                      <td onClick={(e) => e.stopPropagation()}>
                        <select
                          className="input"
                          value={host.host_group_id ?? ''}
                          disabled={!canWrite}
                          onChange={(e) => handleAssignGroup(host.id, Number(e.target.value))}
                          style={{ width: 'auto', minWidth: '120px', fontSize: '0.8125rem', padding: '0.25rem 0.5rem' }}>
                          {host.host_group_id == null && <option value="">—</option>}
                          {hostGroups.map((g) => (
                            <option key={g.id} value={g.id}>
                              {g.name}
                            </option>
                          ))}
                        </select>
                      </td>
                      <td>
                        <span className="hosts-service-count">
                          <HiOutlineCube />
                          {serviceCount}
                        </span>
                      </td>
                      <td>
                        {serviceCount > 0 ? (
                          <div className="hosts-health-bar">
                            {healthyCount > 0 && (
                              <span className="hosts-health-segment hosts-health-segment-healthy" title={`${healthyCount} healthy`}>
                                {healthyCount}
                              </span>
                            )}
                            {warningCount > 0 && (
                              <span className="hosts-health-segment hosts-health-segment-warning" title={`${warningCount} warning(s)`}>
                                {warningCount}
                              </span>
                            )}
                            {criticalCount > 0 && (
                              <span className="hosts-health-segment hosts-health-segment-critical" title={`${criticalCount} critical`}>
                                {criticalCount}
                              </span>
                            )}
                            {failingCount > 0 && (
                              <span className="hosts-health-segment hosts-health-segment-failing" title={`${failingCount} failing`}>
                                {failingCount}
                              </span>
                            )}
                            {serviceCount - healthyCount - warningCount - criticalCount - failingCount > 0 && (
                              <span className="hosts-health-segment hosts-health-segment-unknown" title={`${serviceCount - healthyCount - warningCount - criticalCount - failingCount} unknown`}>
                                {serviceCount - healthyCount - warningCount - criticalCount - failingCount}
                              </span>
                            )}
                          </div>
                        ) : (
                          <span className="hosts-no-services">—</span>
                        )}
                      </td>
                      <td>
                        <span className="hosts-timestamp" title={new Date(host.created_at).toLocaleString()}>
                          {t('common.ago', { time: getTimeAgo(host.created_at) })}
                        </span>
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

      {/* Add/Edit Host Modal */}
      {showHostModal && (
        <div className="modal-overlay" onClick={(e) => { if (e.target === e.currentTarget) closeHostModal(); }}>
          <div className="modal-content" style={{ maxWidth: '600px' }} onClick={(e) => e.stopPropagation()}>
            <div className="modal-header">
              <h3 className="modal-title" style={{ display: 'flex', alignItems: 'center', gap: '0.5rem' }}>
                <HiPlus style={{ color: 'var(--brand-primary)' }} />
                <span>{editingHost ? t('host_detail.edit_host') : t('hosts.new_host')}</span>
              </h3>
              <button className="modal-close" onClick={closeHostModal}>
                <HiX style={{ fontSize: '1.25rem' }} />
              </button>
            </div>
            <div className="modal-body">
              <AddHostForm
                host={editingHost}
                onSuccess={() => {
                  closeHostModal();
                  refreshHosts();
                }}
                onCancel={closeHostModal}
              />
            </div>
          </div>
        </div>
      )}
    </div>
  );
}

export default HostsView;
