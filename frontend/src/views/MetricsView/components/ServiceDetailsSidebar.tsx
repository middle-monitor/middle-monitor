import {
  HiCheckCircle,
  HiXCircle,
  HiClock,
  HiExclamationTriangle,
} from 'react-icons/hi2';
import { HiX } from 'react-icons/hi';
import { useTranslation } from 'react-i18next';
import type { Service, ServiceWithResults } from '../../../api';
import type { HoveredResult } from '../types';

interface ServiceDetailsSidebarProps {
  hoveredResult: HoveredResult | null;
  service: Service | ServiceWithResults | undefined;
  onClose: () => void;
}

export function ServiceDetailsSidebar({
  hoveredResult,
  service,
  onClose,
}: ServiceDetailsSidebarProps) {
  const { t, i18n } = useTranslation();
  const locale = i18n.language;

  if (!hoveredResult) return null;

  // Try to parse SQL check details from message
  let sqlDetails: any = null;
  const message = hoveredResult?.message || '';
  const detailsMatch = message.match(/Details: ({.*})$/);
  if (detailsMatch) {
    try {
      sqlDetails = JSON.parse(detailsMatch[1]);
    } catch (e) {
      // Not JSON, continue with regular message display
    }
  }

  // Parse certificate expiration date from metadata
  let certificateExpirationDate: string | null = null;
  if (service?.type === 'certificate' && hoveredResult?.metadata) {
    try {
      const metadata = JSON.parse(hoveredResult.metadata);
      if (metadata.expires_at) {
        const expiresAt = new Date(metadata.expires_at);
        certificateExpirationDate = expiresAt.toLocaleDateString(locale, {
          day: 'numeric',
          month: 'long',
          year: 'numeric',
          hour: '2-digit',
          minute: '2-digit',
        });
      }
    } catch (e) {
      // Invalid JSON, ignore
    }
  }

  return (
    <div
      style={{
        position: 'fixed',
        right: 0,
        top: 0,
        bottom: 0,
        width: '380px',
        background: 'var(--surface-primary)',
        borderLeft: '1px solid var(--border-primary)',
        boxShadow: 'var(--shadow-xl)',
        zIndex: 1000,
        overflowY: 'auto',
        padding: '1.5rem',
      }}>
      {/* Header */}
      <div
        style={{
          display: 'flex',
          justifyContent: 'space-between',
          alignItems: 'center',
          marginBottom: '2rem',
          paddingBottom: '1rem',
          borderBottom: '1px solid var(--border-primary)',
        }}>
        <h3
          style={{
            color: 'var(--text-primary)',
            fontSize: '1rem',
            fontWeight: 600,
            margin: 0,
          }}>
          {t('metrics_view.sidebar.title')}
        </h3>
        <button className='btn btn-ghost' onClick={onClose}>
          <HiX style={{ fontSize: '1.25rem' }} />
        </button>
      </div>

      {/* Service Name */}
      <div style={{ marginBottom: '1.75rem' }}>
        <div className='stat-label'>{t('metrics_view.labels.service')}</div>
        <div
          style={{
            fontSize: '1rem',
            color: 'var(--brand-primary)',
            fontWeight: 600,
          }}>
          {hoveredResult?.serviceName}
        </div>
        {service && (
          <div
            style={{
              marginTop: '1rem',
              padding: '0.75rem',
              background: 'var(--bg-secondary)',
              border: '1px solid var(--border-primary)',
              borderRadius: '6px',
              fontSize: '0.8125rem',
            }}>
            <div style={{ marginBottom: '0.375rem' }}>
              <span style={{ color: 'var(--text-tertiary)' }}>{t('metrics_view.labels.type')}</span>{' '}
              <span style={{ color: 'var(--text-primary)', fontWeight: 600 }}>
                {service.type.toUpperCase()}
              </span>
            </div>
            <div style={{ marginBottom: '0.375rem' }}>
              <span style={{ color: 'var(--text-tertiary)' }}>{t('metrics_view.labels.host')}:</span>{' '}
              <span
                style={{ color: 'var(--text-primary)', fontFamily: 'monospace' }}>
                {service.host}
              </span>
            </div>
            {service.service && (
              <div style={{ marginBottom: '0.375rem' }}>
                <span style={{ color: 'var(--text-tertiary)' }}>{t('metrics_view.labels.service_app')}</span>{' '}
                <span style={{ color: 'var(--text-primary)' }}>{service.service}</span>
              </div>
            )}
          </div>
        )}
      </div>

      {/* Timestamp */}
      <div style={{ marginBottom: '1.75rem' }}>
        <div className='stat-label'>{t('metrics_view.labels.timestamp')}</div>
        <div
          style={{
            fontSize: '0.8125rem',
            color: 'var(--text-primary)',
            fontFamily: 'monospace',
          }}>
          {hoveredResult &&
            new Date(hoveredResult.timestamp).toLocaleString(locale)}
        </div>
      </div>

      {/* Status */}
      <div style={{ marginBottom: '1.75rem' }}>
        <div className='stat-label'>{t('metrics_view.labels.status')}</div>
        <span
          className={`status-badge ${
            hoveredResult?.status === 'success'
              ? 'status-healthy'
              : hoveredResult?.status === 'warning'
              ? 'status-degraded'
              : hoveredResult?.status === 'failure'
              ? 'status-down'
              : 'status-degraded'
          }`}>
          {hoveredResult?.status === 'success' ? (
            <>
              <HiCheckCircle />
              {t('metrics_view.status.success')}
            </>
          ) : hoveredResult?.status === 'warning' ? (
            <>
              <HiExclamationTriangle />
              {t('metrics_view.status.warning')}
            </>
          ) : hoveredResult?.status === 'failure' ? (
            <>
              <HiXCircle />
              {t('metrics_view.status.failure')}
            </>
          ) : (
            <>
              <HiClock />
              {t('metrics_view.status.timeout')}
            </>
          )}
        </span>
      </div>

      {/* Latency */}
      {hoveredResult &&
        hoveredResult.latency !== null &&
        hoveredResult.latency !== undefined && (
          <div style={{ marginBottom: '1.75rem' }}>
            <div className='stat-label'>{t('metrics_view.labels.latency')}</div>
            <div className='stat-value' style={{ fontSize: '1.5rem' }}>
              {hoveredResult.latency}{' '}
              <span
                style={{
                  fontSize: '0.875rem',
                  fontWeight: 500,
                  color: 'var(--text-tertiary)',
                }}>
                ms
              </span>
            </div>
          </div>
        )}

      {/* Message Section */}
      {(() => {
        if (!message && !certificateExpirationDate) {
          return (
            <div style={{ marginBottom: '1.75rem' }}>
              <div className='stat-label'>{t('metrics_view.labels.message')}</div>
              <div
                style={{
                  fontSize: '0.8125rem',
                  color: 'var(--text-tertiary)',
                  padding: '0.875rem',
                  background: 'var(--bg-secondary)',
                  border: '1px solid var(--border-primary)',
                  borderRadius: '6px',
                  fontStyle: 'italic',
                }}>
                {t('metrics_view.sidebar.no_message')}
              </div>
            </div>
          );
        }

        // If SQL details found, display them in a structured way
        if (sqlDetails) {
          return (
            <div style={{ marginBottom: '1.75rem' }}>
              <div className='stat-label'>{t('metrics_view.sidebar.db_metrics')}</div>

              {/* Connection Info */}
              <div style={{ marginBottom: '1rem' }}>
                <div
                  style={{
                    fontSize: '0.8125rem',
                    fontWeight: 600,
                    color: 'var(--text-primary)',
                    marginBottom: '0.5rem',
                  }}>
                  {t('metrics_view.sidebar.connection')}
                </div>
                <div
                  style={{
                    display: 'grid',
                    gridTemplateColumns: '1fr 1fr',
                    gap: '0.5rem',
                    fontSize: '0.8125rem',
                  }}>
                  {sqlDetails.connection_latency_ms !== undefined && (
                    <div className='stat-card' style={{ padding: '0.5rem' }}>
                      <div className='stat-label' style={{ marginBottom: '0.25rem' }}>
                        {t('metrics_view.labels.latency')}
                      </div>
                      <div style={{ color: 'var(--brand-primary)', fontWeight: 600 }}>
                        {Number(sqlDetails.connection_latency_ms).toFixed(2)} ms
                      </div>
                    </div>
                  )}
                  {sqlDetails.active_connections !== undefined && (
                    <div className='stat-card' style={{ padding: '0.5rem' }}>
                      <div className='stat-label' style={{ marginBottom: '0.25rem' }}>
                        {t('metrics_view.sidebar.active_connections')}
                      </div>
                      <div
                        style={{
                          color: sqlDetails.connection_warning
                            ? 'var(--status-warning)'
                            : 'var(--status-success)',
                          fontWeight: 600,
                        }}>
                        {sqlDetails.active_connections}
                        {sqlDetails.connection_usage_percent !== undefined && (
                          <span
                            style={{
                              fontSize: '0.75rem',
                              color: 'var(--text-tertiary)',
                              marginLeft: '0.25rem',
                            }}>
                            ({Number(sqlDetails.connection_usage_percent).toFixed(1)}%)
                          </span>
                        )}
                      </div>
                    </div>
                  )}
                </div>
              </div>

              {/* Database Size */}
              {sqlDetails.database_size_gb !== undefined && (
                <div style={{ marginBottom: '1rem' }}>
                  <div
                    style={{
                      fontSize: '0.8125rem',
                      fontWeight: 600,
                      color: 'var(--text-primary)',
                      marginBottom: '0.5rem',
                    }}>
                    {t('metrics_view.sidebar.database_size')}
                  </div>
                  <div style={{ fontSize: '1.25rem', fontWeight: 600, color: '#8B5CF6' }}>
                    {Number(sqlDetails.database_size_gb).toFixed(2)} GB
                  </div>
                </div>
              )}

              {/* Slow Queries */}
              {sqlDetails.slow_queries_count !== undefined &&
                sqlDetails.slow_queries_count > 0 && (
                  <div style={{ marginBottom: '1rem' }}>
                    <div
                      style={{
                        fontSize: '0.8125rem',
                        fontWeight: 600,
                        color: 'var(--status-error)',
                        marginBottom: '0.5rem',
                        display: 'flex',
                        alignItems: 'center',
                        gap: '0.5rem',
                      }}>
                      <HiExclamationTriangle style={{ fontSize: '0.875rem' }} />
                      {t('metrics_view.sidebar.slow_queries', { count: sqlDetails.slow_queries_count })}
                    </div>
                    {Array.isArray(sqlDetails.slow_queries) &&
                      sqlDetails.slow_queries.length > 0 && (
                        <div
                          style={{
                            maxHeight: '200px',
                            overflowY: 'auto',
                            background: 'var(--bg-secondary)',
                            borderRadius: '6px',
                            padding: '0.5rem',
                          }}>
                          {sqlDetails.slow_queries.map((query: any, idx: number) => (
                            <div
                              key={idx}
                              style={{
                                marginBottom: '0.75rem',
                                paddingBottom: '0.75rem',
                                borderBottom:
                                  idx < sqlDetails.slow_queries.length - 1
                                    ? '1px solid var(--border-primary)'
                                    : 'none',
                              }}>
                              <div
                                style={{
                                  fontSize: '0.75rem',
                                  color: 'var(--text-tertiary)',
                                  marginBottom: '0.25rem',
                                }}>
                                {t('metrics_view.sidebar.slow_query_meta', {
                                  pid: query.pid,
                                  user: query.user,
                                  duration: query.duration,
                                })}
                              </div>
                              <div
                                style={{
                                  fontSize: '0.75rem',
                                  color: 'var(--text-primary)',
                                  fontFamily: 'monospace',
                                  background: 'var(--bg-tertiary)',
                                  padding: '0.5rem',
                                  borderRadius: '4px',
                                  wordBreak: 'break-word',
                                }}>
                                {query.query_preview}...
                              </div>
                            </div>
                          ))}
                        </div>
                      )}
                  </div>
                )}
            </div>
          );
        }

        // Regular message display
        const displayText = certificateExpirationDate
          ? t('metrics_view.sidebar.expires_at', { date: certificateExpirationDate })
          : message.split(' Details: ')[0];

        return (
          <div style={{ marginBottom: '1.75rem' }}>
            <div className='stat-label'>
              {certificateExpirationDate ? t('metrics_view.labels.expiration_date') : t('metrics_view.labels.message')}
            </div>
            <div
              style={{
                fontSize: '0.8125rem',
                color: 'var(--text-primary)',
                padding: '0.875rem',
                background: 'var(--bg-secondary)',
                border: '1px solid var(--border-primary)',
                borderRadius: '6px',
                wordBreak: 'break-word',
                lineHeight: '1.5',
              }}>
              {displayText}
            </div>
          </div>
        );
      })()}

      {/* Max attempts configured */}
      {hoveredResult && (
        <div style={{ marginBottom: '1.75rem' }}>
          <div className='stat-label'>{t('metrics_view.labels.max_attempts_configured')}</div>
          <div className='stat-value' style={{ fontSize: '1.5rem', color: '#8B5CF6' }}>
            {hoveredResult.maxAttempts || 3}
          </div>
          <div
            style={{
              fontSize: '0.75rem',
              color: 'var(--text-tertiary)',
              marginTop: '0.375rem',
              lineHeight: '1.4',
            }}>
            {t('metrics_view.sidebar.max_attempts_hint', {
              count: hoveredResult.maxAttempts || 3,
            })}
          </div>
        </div>
      )}

      {/* HTTP code */}
      {hoveredResult?.message && hoveredResult.message.includes('HTTP status:') && (
        <div style={{ marginBottom: '1.75rem' }}>
          <div className='stat-label'>{t('metrics_view.labels.http_code')}</div>
          <div
            className='stat-value'
            style={{ fontSize: '1.5rem', color: 'var(--status-warning)' }}>
            {hoveredResult.message.match(/HTTP status: (\d+)/)?.[1] || 'N/A'}
          </div>
        </div>
      )}
    </div>
  );
}
