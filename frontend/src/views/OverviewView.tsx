import { Link, useNavigate } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { useQuery } from '@tanstack/react-query';
import {
  HiOutlineCheckCircle,
  HiOutlineXCircle,
  HiOutlineExclamationTriangle,
  HiOutlineCube,
  HiOutlineServerStack,
  HiOutlineClock,
  HiOutlineArrowRight,
  HiOutlineExclamationCircle,
  HiOutlineShieldCheck,
  HiOutlineWrenchScrewdriver,
  HiOutlineRocketLaunch,
  HiOutlineGlobeAlt,
  HiOutlineCodeBracket,
} from 'react-icons/hi2';
import {
  type OrganizationStats,
  type Event,
  type Incident,
  type MaintenanceWindow,
} from '../api';
import { getWindowState } from '../components/MaintenanceBadge';
import { useOrgApi, useOrgQueryScope } from '../hooks/useOrgApi';
import { useQueryRefresh } from '../hooks/useQueryRefresh';
import { RefreshControl } from '../components/RefreshControl';
import { useOrgPath } from '../hooks/useOrgPath';
import { useServiceModal } from '../contexts/ServiceModalContext';
import { Skeleton } from '../components/Skeleton';
import './OverviewView.css';

function OverviewView() {
  const { t } = useTranslation();
  const orgApi = useOrgApi();
  const { orgPath } = useOrgPath();
  const navigate = useNavigate();
  const scope = useOrgQueryScope();
  const { openModal } = useServiceModal();

  const { refetchInterval, buildControl } = useQueryRefresh('overview', 30000);

  const overviewQueryKey = [scope, 'overview'];
  const overviewQuery = useQuery({
    queryKey: overviewQueryKey,
    queryFn: async () => {
      const [statsRes, timelineRes, incidentsRes, maintRes] = await Promise.all(
        [
          orgApi.getStats(),
          orgApi.dashboard.getTimeline(),
          orgApi.incidents.list({ status: 'open' }),
          orgApi.maintenance.list().catch(() => ({ data: [] })),
        ],
      );
      const now = Date.now();
      return {
        stats: statsRes.data as OrganizationStats,
        events: (timelineRes.data as Event[])?.slice(0, 8) || [],
        incidents: (incidentsRes.data as Incident[]) || [],
        maintenanceWindows: ((maintRes.data as MaintenanceWindow[]) || []).filter(
          (w) => new Date(w.ends_at).getTime() > now,
        ),
      };
    },
    refetchInterval,
  });

  const stats = overviewQuery.data?.stats ?? null;
  const events = overviewQuery.data?.events ?? [];
  const incidents = overviewQuery.data?.incidents ?? [];
  const maintenanceWindows = overviewQuery.data?.maintenanceWindows ?? [];

  const autoRefresh = buildControl({
    query: overviewQuery,
    queryKey: overviewQueryKey,
    prefix: [scope, 'overview'],
  });

  // Checked first: a failed first load leaves `stats` null too.
  if (overviewQuery.isLoadingError) {
    return <div className='error-message'>{t('overview.load_error')}</div>;
  }

  const header = (
    <div className='page-header'>
      <div>
        <h1 className='page-title'>{t('overview.title')}</h1>
        <p className='page-subtitle'>{t('overview.subtitle')}</p>
      </div>
      <RefreshControl control={autoRefresh} />
    </div>
  );

  // Same shell as the loaded view: the header keeps its place, only the cards
  // below it are still a placeholder.
  if (overviewQuery.isPending || !stats) {
    return (
      <div className='overview'>
        {header}
        <Skeleton rows={6} />
      </div>
    );
  }

  const serviceStats = stats.services;
  const hostStats = stats.hosts;
  const openIncidents = incidents.filter((i) => i.status === 'open');
  const criticalIncidents = openIncidents.filter(
    (i) => i.severity === 'critical',
  );

  const topErroringServices = stats.errors.by_service
    ? Object.entries(stats.errors.by_service)
        .sort(([, a], [, b]) => b - a)
        .slice(0, 5)
    : [];

  const formatRelativeTime = (timestamp: string) => {
    const now = new Date();
    const date = new Date(timestamp);
    const diffMs = now.getTime() - date.getTime();
    const diffMins = Math.floor(diffMs / 60000);
    const diffHours = Math.floor(diffMs / 3600000);
    const diffDays = Math.floor(diffMs / 86400000);
    if (diffMins < 1) return t('overview.time.now');
    if (diffMins < 60)
      return t('overview.time.minutes_ago', { count: diffMins });
    if (diffHours < 24)
      return t('overview.time.hours_ago', { count: diffHours });
    return t('overview.time.days_ago', { count: diffDays });
  };

  // Same event vocabulary as the timeline page: the worker writes error_spike,
  // deploy/restart/crash come from the events API.
  const getEventIcon = (type: string) => {
    switch (type) {
      case 'error':
      case 'crash':
        return <HiOutlineXCircle className='event-icon event-icon-error' />;
      case 'warning':
      case 'error_spike':
        return (
          <HiOutlineExclamationTriangle className='event-icon event-icon-warning' />
        );
      case 'success':
      case 'deploy':
        return (
          <HiOutlineCheckCircle className='event-icon event-icon-success' />
        );
      default:
        return <HiOutlineClock className='event-icon event-icon-info' />;
    }
  };

  return (
    <div className='overview'>
      {header}

      {/* ── Top stat cards ── */}
      <div className='overview-stats'>
        <Link
          to={orgPath('/services')}
          className='overview-stat-card overview-stat-link'>
          <div className='overview-stat-header'>
            <HiOutlineCube className='overview-stat-icon' />
            <span>{t('overview.stat.services')}</span>
          </div>
          <div className='overview-stat-value'>{serviceStats.total}</div>
          <div className='overview-stat-breakdown'>
            <span className='stat-healthy'>
              {serviceStats.healthy} {t('overview.stat.ok')}
            </span>
            <span className='stat-separator'>·</span>
            <span className='stat-warning'>
              {serviceStats.warning} {t('overview.stat.warn')}
            </span>
            <span className='stat-separator'>·</span>
            <span className='stat-failing'>
              {serviceStats.failing} {t('overview.stat.fail')}
            </span>
          </div>
        </Link>

        <Link
          to={orgPath('/hosts')}
          className='overview-stat-card overview-stat-link'>
          <div className='overview-stat-header'>
            <HiOutlineServerStack className='overview-stat-icon' />
            <span>{t('overview.stat.hosts')}</span>
          </div>
          <div className='overview-stat-value'>{hostStats.total}</div>
          <div className='overview-stat-breakdown'>
            <span className='stat-healthy'>
              {hostStats.healthy} {t('overview.stat.ok')}
            </span>
            <span className='stat-separator'>·</span>
            <span className='stat-failing'>
              {hostStats.failing} {t('overview.stat.fail')}
            </span>
          </div>
        </Link>

        <Link
          to={orgPath('/errors')}
          className='overview-stat-card overview-stat-errors overview-stat-link'>
          <div className='overview-stat-header'>
            <HiOutlineExclamationCircle className='overview-stat-icon' />
            <span>{t('overview.stat.errors_24h')}</span>
          </div>
          <div className='overview-stat-value'>{stats.errors.total_24h}</div>
          <div className='overview-stat-breakdown'>
            <span className='stat-muted'>
              {t('overview.stat.impacted_services', {
                count: stats.errors.impacted_services,
              })}
            </span>
          </div>
        </Link>

        <Link
          to={orgPath('/alerts/incidents')}
          className={`overview-stat-card overview-stat-status overview-stat-link ${
            criticalIncidents.length > 0
              ? 'status-fail'
              : openIncidents.length > 0
                ? 'status-warn'
                : 'status-ok'
          }`}>
          <div className='overview-stat-header'>
            <HiOutlineShieldCheck className='overview-stat-icon' />
            <span>{t('overview.incidents.open_label')}</span>
          </div>
          <div className='overview-stat-value'>{openIncidents.length}</div>
          <div className='overview-stat-breakdown'>
            {openIncidents.length === 0 ? (
              <span className='stat-healthy'>
                {t('overview.incidents.all_nominal')}
              </span>
            ) : (
              <>
                <span className='stat-failing'>
                  {t('overview.incidents.criticals', {
                    count: criticalIncidents.length,
                  })}
                </span>
                <span className='stat-separator'>·</span>
                <span className='stat-warning'>
                  {t('overview.incidents.warnings', {
                    count: openIncidents.length - criticalIncidents.length,
                  })}
                </span>
              </>
            )}
          </div>
        </Link>
      </div>

      {/* A new org has nothing to show yet: point at the three ways in. */}
      {serviceStats.total === 0 && hostStats.total === 0 && (
        <div className='card' style={{ marginBottom: '1.5rem' }}>
          <div className='empty-state'>
            <HiOutlineRocketLaunch className='empty-state-icon' />
            <div className='empty-state-title'>{t('overview.get_started.title')}</div>
            <div className='empty-state-description'>
              {t('overview.get_started.description')}
            </div>
            <div style={{ display: 'flex', gap: '0.5rem', flexWrap: 'wrap', justifyContent: 'center' }}>
              <button type='button' className='empty-state-action' onClick={() => openModal(null, [])}>
                <HiOutlineGlobeAlt /> {t('overview.get_started.check')}
              </button>
              <Link to={orgPath('/api-keys')} className='empty-state-action'>
                <HiOutlineServerStack /> {t('overview.get_started.agent')}
              </Link>
              <Link to={orgPath('/errors')} className='empty-state-action'>
                <HiOutlineCodeBracket /> {t('overview.get_started.sdk')}
              </Link>
            </div>
          </div>
        </div>
      )}

      {/* ── Main 2×2 grid ── */}
      <div className='overview-grid'>
        {/* [1] Open Incidents */}
        <div className='card overview-card'>
          <div className='card-header'>
            <h3 className='card-title'>
              <HiOutlineShieldCheck className='card-title-icon' />
              {t('overview.incidents.active_title')}
            </h3>
            <Link to={orgPath('/alerts/incidents')} className='card-link'>
              {t('overview.incidents.see_all')} <HiOutlineArrowRight />
            </Link>
          </div>
          {openIncidents.length === 0 ? (
            <div className='overview-empty'>
              <HiOutlineCheckCircle
                className='overview-empty-icon'
                style={{ color: 'var(--status-success)' }}
              />
              <span>{t('overview.incidents.none')}</span>
            </div>
          ) : (
            <div className='incidents-list'>
              {openIncidents.slice(0, 5).map((inc) => (
                <Link
                  key={inc.id}
                  to={orgPath('/alerts/incidents')}
                  className='incident-item'>
                  <span className={`inc-sev-dot sev-${inc.severity}`} />
                  <div className='incident-content'>
                    <div className='incident-title'>{inc.title}</div>
                    <div className='incident-meta'>
                      {inc.service && (
                        <span className='incident-service'>{inc.service}</span>
                      )}
                      {inc.service && <span className='incident-sep'>·</span>}
                      <span className='incident-time'>
                        {formatRelativeTime(inc.started_at)}
                      </span>
                    </div>
                  </div>
                  <span className={`inc-badge sev-${inc.severity}`}>
                    {inc.severity}
                  </span>
                </Link>
              ))}
              {openIncidents.length > 5 && (
                <div className='incidents-more'>
                  +{openIncidents.length - 5} autres incidents
                </div>
              )}
            </div>
          )}
        </div>

        {/* [2] Top Erroring Services */}
        <div className='card overview-card'>
          <div className='card-header'>
            <h3 className='card-title'>
              <HiOutlineExclamationTriangle className='card-title-icon' />
              {t('overview.top_errors.title')}
            </h3>
            <Link to={orgPath('/errors')} className='card-link'>
              {t('overview.top_errors.see_errors')} <HiOutlineArrowRight />
            </Link>
          </div>
          {topErroringServices.length === 0 ? (
            <div className='overview-empty'>
              <HiOutlineCheckCircle className='overview-empty-icon' />
              <span>{t('overview.top_errors.empty')}</span>
            </div>
          ) : (
            <div className='top-errors-list'>
              {topErroringServices.map(([serviceName, count]) => (
                <div
                  key={serviceName}
                  className='top-error-item top-error-item-clickable'
                  onClick={() =>
                    navigate(
                      orgPath(
                        `/errors?service=${encodeURIComponent(serviceName)}&relative=24h`,
                      ),
                    )
                  }
                  role='button'
                  tabIndex={0}
                  onKeyDown={(e) =>
                    e.key === 'Enter' &&
                    navigate(
                      orgPath(
                        `/errors?service=${encodeURIComponent(serviceName)}&relative=24h`,
                      ),
                    )
                  }>
                  <div className='top-error-service'>
                    <span className='top-error-name'>{serviceName}</span>
                  </div>
                  <div className='top-error-count'>
                    <span className='top-error-badge'>{count}</span>
                    <span className='top-error-label'>
                      {t('overview.top_errors.errors_label')}
                    </span>
                  </div>
                  <div className='top-error-bar'>
                    <div
                      className='top-error-bar-fill'
                      style={{
                        width: `${(count / (topErroringServices[0]?.[1] || 1)) * 100}%`,
                      }}
                    />
                  </div>
                </div>
              ))}
            </div>
          )}
        </div>

        {/* [3] Maintenance windows */}
        <Link
          to={orgPath('/alerts/maintenance')}
          className='card overview-card overview-card-link'>
          <div className='card-header'>
            <h3 className='card-title'>
              <HiOutlineWrenchScrewdriver className='card-title-icon' />
              {t('overview.maintenance.title')}
            </h3>
            <span className='card-link'>
              {t('overview.maintenance.see_all')} <HiOutlineArrowRight />
            </span>
          </div>
          {maintenanceWindows.length === 0 ? (
            <div className='overview-empty'>
              <HiOutlineCheckCircle
                className='overview-empty-icon'
                style={{ color: 'var(--status-success)' }}
              />
              <span>{t('overview.maintenance.none')}</span>
            </div>
          ) : (
            <div className='maintenance-list'>
              {maintenanceWindows.slice(0, 5).map((w) => {
                const state = getWindowState(w);
                const isActive = state === 'active';
                const fmtDate = (iso: string) =>
                  new Date(iso).toLocaleString(undefined, {
                    day: '2-digit',
                    month: 'short',
                    hour: '2-digit',
                    minute: '2-digit',
                  });
                return (
                  <div
                    key={w.id}
                    className={`maintenance-item ${isActive ? 'maint-active' : 'maint-scheduled'}`}>
                    <span
                      className={`maint-dot ${isActive ? 'dot-active' : 'dot-scheduled'}`}
                    />
                    <div className='maint-content'>
                      <div className='maint-name'>{w.name}</div>
                      <div className='maint-meta'>
                        <span className='maint-target'>
                          {w.target_name || `${w.target_type} #${w.target_id}`}
                        </span>
                        <span className='maint-sep'>·</span>
                        <span className='maint-date'>
                          {isActive
                            ? t('overview.maintenance.ends', {
                                date: fmtDate(w.ends_at),
                              })
                            : t('overview.maintenance.starts', {
                                date: fmtDate(w.starts_at),
                              })}
                        </span>
                      </div>
                    </div>
                    <span
                      className={`maint-badge ${isActive ? 'badge-active' : 'badge-scheduled'}`}>
                      {isActive
                        ? t('overview.maintenance.active')
                        : t('overview.maintenance.scheduled')}
                    </span>
                  </div>
                );
              })}
              {maintenanceWindows.length > 5 && (
                <div className='maintenance-more'>
                  +{maintenanceWindows.length - 5} autres
                </div>
              )}
            </div>
          )}
        </Link>

        {/* [4] Recent Activity */}
        <Link
          to={orgPath('/timeline')}
          className='card overview-card overview-card-link'>
          <div className='card-header'>
            <h3 className='card-title'>
              <HiOutlineClock className='card-title-icon' />
              {t('overview.activity.title')}
            </h3>
            <span className='card-link'>
              {t('overview.activity.see_all')} <HiOutlineArrowRight />
            </span>
          </div>
          {events.length === 0 ? (
            <div className='overview-empty'>
              <HiOutlineClock className='overview-empty-icon' />
              <span>{t('overview.activity.empty')}</span>
            </div>
          ) : (
            <div className='activity-list'>
              {events.map((event) => (
                <div key={event.id} className='activity-item'>
                  {getEventIcon(event.type)}
                  <div className='activity-content'>
                    <div className='activity-message'>{event.message}</div>
                    <div className='activity-meta'>
                      <span className='activity-service'>{event.service}</span>
                      <span className='activity-separator'>·</span>
                      <span className='activity-time'>
                        {formatRelativeTime(event.timestamp)}
                      </span>
                    </div>
                  </div>
                </div>
              ))}
            </div>
          )}
        </Link>
      </div>
    </div>
  );
}

export default OverviewView;
