import { useTranslation } from 'react-i18next';
import { useQuery } from '@tanstack/react-query';
import { HiHeart, HiCheckCircle, HiXCircle, HiServer } from 'react-icons/hi';
import { HiExclamationTriangle } from 'react-icons/hi2';
import {
  getSQLHealthCheck,
  type HealthStatus,
  type SQLHealthCheck,
} from '../api';
import { useOrgApi, useOrgQueryScope } from '../hooks/useOrgApi';
import { useQueryRefresh } from '../hooks/useQueryRefresh';
import { RefreshControl } from '../components/RefreshControl';
import { Skeleton } from '../components/Skeleton';

function HealthView() {
  const { t, i18n } = useTranslation();
  const orgApi = useOrgApi();
  const scope = useOrgQueryScope();

  const { refetchInterval, buildControl } = useQueryRefresh('health', 5000);

  const healthQueryKey = [scope, 'health', 'status'];
  const healthQuery = useQuery({
    queryKey: healthQueryKey,
    queryFn: async () => (await orgApi.dashboard.getHealth()).data as HealthStatus,
    refetchInterval,
  });

  // Secondary panel: a failing SQL probe hides its card, it does not take the
  // whole view down.
  const sqlQuery = useQuery({
    queryKey: [scope, 'health', 'sql'],
    queryFn: async () => (await getSQLHealthCheck()).data as SQLHealthCheck,
    refetchInterval,
  });

  const health = healthQuery.data ?? null;
  const sqlHealth = sqlQuery.data ?? null;

  const autoRefresh = buildControl({
    query: healthQuery,
    queryKey: healthQueryKey,
    prefix: [scope, 'health'],
  });

  // Only the results area waits on the first load: a refresh that fails keeps
  // the last known health on screen instead of replacing it with an error.
  if (healthQuery.isLoadingError) {
    return <div className='error-message'>{t('health.load_error')}</div>;
  }

  if (!health && !healthQuery.isPending) return null;

  const getStatusClass = (status: string) => {
    switch (status) {
      case 'healthy':
        return 'status-healthy';
      case 'degraded':
        return 'status-degraded';
      case 'critical':
        return 'status-down';
      default:
        return 'status-degraded';
    }
  };

  const getStatusLabel = (status: string) => {
    switch (status) {
      case 'healthy':
        return (
          <>
            <HiCheckCircle style={{ marginRight: '0.375rem' }} />
            {t('health.status.healthy')}
          </>
        );
      case 'degraded':
        return (
          <>
            <HiExclamationTriangle style={{ marginRight: '0.375rem' }} />
            {t('health.status.degraded')}
          </>
        );
      case 'critical':
        return (
          <>
            <HiXCircle style={{ marginRight: '0.375rem' }} />
            {t('health.status.critical')}
          </>
        );
      default:
        return status;
    }
  };

  return (
    <div>
      {/* Page Header */}
      <div className='page-header'>
        <div>
          <h1 className='page-title'>{t('health.title')}</h1>
          <p className='page-subtitle'>
            {t('health.subtitle')}
          </p>
        </div>
        <RefreshControl control={autoRefresh} />
      </div>

      {/* Main Health Card */}
      <div className='card'>
        <h2 className='card-title'>
          <HiHeart
            className='card-title-icon'
            style={{ color: 'var(--status-success)' }}
          />
          <span>{t('health.general_status')}</span>
        </h2>

        {health ? (
          <>
            <div className='stat-grid'>
              <div className='stat-card'>
                <div className='stat-label'>{t('health.global_status')}</div>
                <div style={{ marginTop: '0.5rem' }}>
                  <span className={`status-badge ${getStatusClass(health.status)}`}>
                    {getStatusLabel(health.status)}
                  </span>
                </div>
              </div>

              <div className='stat-card'>
                <div className='stat-label'>{t('health.active_services')}</div>
                <div className='stat-value'>{health.services}</div>
              </div>

              <div className='stat-card'>
                <div className='stat-label'>{t('health.errors_24h')}</div>
                <div
                  className={`stat-value ${
                    health.errors_24h && health.errors_24h > 0 ? 'error' : ''
                  }`}>
                  {health.errors_24h || 0}
                </div>
              </div>

              <div className='stat-card'>
                <div className='stat-label'>{t('health.failing_services')}</div>
                <div
                  className={`stat-value ${
                    health.services_failing && health.services_failing > 0
                      ? 'error'
                      : ''
                  }`}>
                  {health.services_failing || 0}
                </div>
              </div>
            </div>

            <div
              style={{
                marginTop: '1.5rem',
                paddingTop: '1rem',
                borderTop: '1px solid var(--border-primary)',
                fontSize: '0.75rem',
                color: 'var(--text-tertiary)',
              }}>
              {t('health.last_update')}{' '}
              {health.last_update
                ? new Date(health.last_update).toLocaleString(i18n.language)
                : t('common.not_available')}
            </div>
          </>
        ) : (
          <Skeleton rows={4} />
        )}
      </div>

      {/* SQL Health Card */}
      {sqlHealth && (
        <div className='card'>
          <h2 className='card-title'>
            <HiServer
              className='card-title-icon'
              style={{ color: 'var(--brand-primary)' }}
            />
            <span>{t('health.database')}</span>
          </h2>

          <div className='stat-grid'>
            <div className='stat-card'>
              <div className='stat-label'>{t('common.status')}</div>
              <div style={{ marginTop: '0.5rem' }}>
                <span
                  className={`status-badge ${
                    sqlHealth.status === 'ok' ? 'status-healthy' : 'status-down'
                  }`}>
                  {sqlHealth.status === 'ok' ? (
                    <>
                      <HiCheckCircle style={{ marginRight: '0.375rem' }} />
                      {t('health.connected')}
                    </>
                  ) : (
                    <>
                      <HiXCircle style={{ marginRight: '0.375rem' }} />
                      {t('common.error')}
                    </>
                  )}
                </span>
              </div>
            </div>

            <div className='stat-card'>
              <div className='stat-label'>{t('common.latency')}</div>
              <div className='stat-value'>{sqlHealth.latency_ms}ms</div>
            </div>

            <div className='stat-card' style={{ gridColumn: 'span 2' }}>
              <div className='stat-label'>{t('common.message')}</div>
              <div
                style={{
                  marginTop: '0.5rem',
                  fontSize: '0.875rem',
                  color: 'var(--text-secondary)',
                }}>
                {sqlHealth.message}
              </div>
            </div>
          </div>

          <div
            style={{
              marginTop: '1.5rem',
              paddingTop: '1rem',
              borderTop: '1px solid var(--border-primary)',
              fontSize: '0.75rem',
              color: 'var(--text-tertiary)',
            }}>
            {t('health.last_check')}{' '}
            {new Date(sqlHealth.timestamp).toLocaleString(i18n.language)}
          </div>
        </div>
      )}
    </div>
  );
}

export default HealthView;
