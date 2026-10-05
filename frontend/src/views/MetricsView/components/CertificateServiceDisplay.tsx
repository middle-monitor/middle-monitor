import { HiCheckCircle, HiXCircle } from 'react-icons/hi';
import { useTranslation } from 'react-i18next';
import type { ServiceResult } from '../../../api';

interface CertificateServiceDisplayProps {
  latestResult: ServiceResult | null;
}

export function CertificateServiceDisplay({
  latestResult,
}: CertificateServiceDisplayProps) {
  const { t, i18n } = useTranslation();

  if (!latestResult) {
    return (
      <div
        style={{
          background: 'var(--bg-secondary)',
          borderRadius: '8px',
          padding: '1rem',
          border: '1px solid var(--border-primary)',
          textAlign: 'center',
          color: 'var(--text-tertiary)',
          fontSize: '0.875rem',
        }}>
        {t('metrics_view.certificate_display.waiting')}
      </div>
    );
  }

  // Parse metadata to get expiration date
  let expirationDate: string | null = null;
  if (latestResult.metadata) {
    try {
      const metadata = JSON.parse(latestResult.metadata);
      if (metadata.expires_at) {
        const expiresAt = new Date(metadata.expires_at);
        expirationDate = expiresAt.toLocaleDateString(i18n.language, {
          day: 'numeric',
          month: 'long',
          year: 'numeric',
          hour: '2-digit',
          minute: '2-digit',
        });
      }
    } catch {
      // Invalid JSON, ignore
    }
  }

  const isSuccess = latestResult.status === 'success';
  const borderColor = isSuccess
    ? 'var(--status-success-border)'
    : 'var(--status-error-border)';
  const statusColor = isSuccess ? 'var(--status-success)' : 'var(--status-error)';
  const statusLabel = isSuccess
    ? t('metrics_view.certificate_display.valid')
    : t('metrics_view.certificate_display.invalid');

  return (
    <div
      style={{
        background: 'var(--bg-secondary)',
        borderRadius: '8px',
        padding: '1rem',
        border: `1px solid ${borderColor}`,
      }}>
      <h4
        style={{
          marginBottom: '1rem',
          fontSize: '0.75rem',
          fontWeight: 600,
          color: 'var(--text-tertiary)',
          textTransform: 'uppercase',
          letterSpacing: '0.05em',
        }}>
        {t('metrics_view.certificate_display.last_check')}
      </h4>
      <div
        style={{
          display: 'flex',
          alignItems: 'center',
          gap: '0.875rem',
          padding: '0.75rem',
          background: isSuccess ? 'var(--status-success-bg)' : 'var(--status-error-bg)',
          border: `1px solid ${borderColor}`,
          borderRadius: '6px',
        }}>
        <div
          style={{
            width: '12px',
            height: '12px',
            borderRadius: '50%',
            background: statusColor,
            flexShrink: 0,
          }}
        />
        <div style={{ flex: 1, minWidth: 0 }}>
          <div
            style={{
              display: 'flex',
              justifyContent: 'space-between',
              alignItems: 'center',
              marginBottom: '0.25rem',
              flexWrap: 'wrap',
              gap: '0.5rem',
            }}>
            <span
              style={{
                fontWeight: 600,
                color: statusColor,
                fontSize: '0.875rem',
                display: 'flex',
                alignItems: 'center',
                gap: '0.25rem',
              }}>
              {isSuccess ? (
                <>
                  <HiCheckCircle style={{ fontSize: '1rem' }} />
                  {statusLabel}
                </>
              ) : (
                <>
                  <HiXCircle style={{ fontSize: '1rem' }} />
                  {statusLabel}
                </>
              )}
            </span>
            <span
              style={{
                fontSize: '0.75rem',
                color: 'var(--text-tertiary)',
                fontFamily: 'monospace',
              }}>
              {new Date(latestResult.timestamp).toLocaleString(i18n.language)}
            </span>
          </div>
          {expirationDate ? (
            <div
              style={{
                fontSize: '0.8125rem',
                color: 'var(--text-secondary)',
                wordBreak: 'break-word',
                marginTop: '0.5rem',
              }}>
              {t('metrics_view.certificate_display.expires_on', { date: expirationDate })}
            </div>
          ) : latestResult.message ? (
            <div
              style={{
                fontSize: '0.8125rem',
                color: 'var(--text-secondary)',
                wordBreak: 'break-word',
                marginTop: '0.5rem',
              }}>
              {latestResult.message}
            </div>
          ) : null}
        </div>
      </div>
    </div>
  );
}
