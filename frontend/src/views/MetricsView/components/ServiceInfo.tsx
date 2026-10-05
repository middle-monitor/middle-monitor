import { useTranslation } from 'react-i18next';
import type { ServiceResult } from '../../../api';

interface ServiceInfoProps {
  successCount: number;
  failureCount: number;
  warningCount: number;
  hasAnyResults: boolean;
  hasResultsInRange: boolean;
  serviceDateRange: string;
  results: ServiceResult[];
}

export function ServiceInfo({
  successCount,
  failureCount,
  warningCount,
  hasAnyResults,
  hasResultsInRange,
  serviceDateRange,
  results,
}: ServiceInfoProps) {
  const { t } = useTranslation();

  if (!hasAnyResults) {
    return (
      <div
        style={{
          marginTop: '0.75rem',
          padding: '0.75rem',
          background: 'var(--status-info-bg)',
          border: '1px solid var(--status-info-border)',
          borderRadius: '6px',
          fontSize: '0.875rem',
          color: 'var(--status-info)',
          display: 'flex',
          alignItems: 'center',
          gap: '0.5rem',
        }}>
        <span>{t('metrics_view.service_info.waiting_first_run')}</span>
      </div>
    );
  }

  if (!hasResultsInRange) {
    const successTotal = results.filter((r) => r.status === 'success').length;
    const failureTotal = results.filter((r) => r.status === 'failure').length;

    return (
      <div
        style={{
          marginTop: '0.75rem',
          padding: '0.75rem',
          background: 'var(--bg-secondary)',
          border: '1px solid var(--border-primary)',
          borderRadius: '6px',
          fontSize: '0.875rem',
          color: 'var(--text-tertiary)',
        }}>
        <div
          style={{
            display: 'flex',
            alignItems: 'center',
            gap: '0.5rem',
            marginBottom: '0.5rem',
          }}>
          <span>
            {t('metrics_view.service_info.no_results_in_range', { range: serviceDateRange })}
          </span>
        </div>
        {results.length > 0 && (
          <div
            style={{
              marginTop: '0.5rem',
              padding: '0.5rem',
              background: 'var(--bg-tertiary)',
              borderRadius: '4px',
              fontSize: '0.8125rem',
            }}>
            <div
              style={{
                color: 'var(--text-tertiary)',
                marginBottom: '0.25rem',
              }}>
              {t('metrics_view.service_info.results_outside_range')}
            </div>
            <div
              style={{
                display: 'flex',
                gap: '1rem',
                color: 'var(--text-secondary)',
              }}>
              <span>
                {successTotal === 1
                  ? t('metrics_view.service_info.success_count', { count: successTotal })
                  : t('metrics_view.service_info.success_count_plural', { count: successTotal })}
              </span>
              <span>
                {failureTotal === 1
                  ? t('metrics_view.service_info.failure_count', { count: failureTotal })
                  : t('metrics_view.service_info.failure_count_plural', { count: failureTotal })}
              </span>
              <span style={{ color: 'var(--text-tertiary)' }}>
                {t('metrics_view.service_info.total', { count: results.length })}
              </span>
            </div>
            <div
              style={{
                marginTop: '0.5rem',
                fontSize: '0.75rem',
                color: 'var(--text-tertiary)',
              }}>
              {t('metrics_view.service_info.change_date_range')}
            </div>
          </div>
        )}
      </div>
    );
  }

  return (
    <div
      style={{
        display: 'flex',
        gap: '1rem',
        fontSize: '0.875rem',
      }}>
      <span
        style={{
          color: 'var(--status-success)',
          display: 'flex',
          alignItems: 'center',
          gap: '0.25rem',
        }}>
        {successCount === 1
          ? t('metrics_view.service_info.success_count', { count: successCount })
          : t('metrics_view.service_info.success_count_plural', { count: successCount })}
      </span>
      <span
        style={{
          color: 'var(--status-warning)',
          display: 'flex',
          alignItems: 'center',
          gap: '0.25rem',
        }}>
        {warningCount === 1
          ? t('metrics_view.service_info.warning_count', { count: warningCount })
          : t('metrics_view.service_info.warning_count_plural', { count: warningCount })}
      </span>
      <span
        style={{
          color: 'var(--status-error)',
          display: 'flex',
          alignItems: 'center',
          gap: '0.25rem',
        }}>
        {failureCount === 1
          ? t('metrics_view.service_info.failure_count', { count: failureCount })
          : t('metrics_view.service_info.failure_count_plural', { count: failureCount })}
      </span>
    </div>
  );
}
