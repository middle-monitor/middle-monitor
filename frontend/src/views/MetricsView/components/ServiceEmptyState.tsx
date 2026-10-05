import { HiInbox } from 'react-icons/hi';
import { useTranslation } from 'react-i18next';

export function ServiceEmptyState() {
  const { t } = useTranslation();

  return (
    <div
      style={{
        textAlign: 'center',
        padding: '2rem',
        color: '#6B7280',
        fontSize: '0.875rem',
        background: 'rgba(31, 41, 55, 0.3)',
        borderRadius: '8px',
        border: '1px dashed rgba(55, 65, 81, 0.5)',
      }}>
      <HiInbox
        style={{
          fontSize: '2rem',
          marginBottom: '0.5rem',
          opacity: 0.5,
          color: '#6B7280',
        }}
      />
      <div
        style={{
          fontWeight: 500,
          color: '#9CA3AF',
          marginBottom: '0.25rem',
        }}>
        {t('metrics_view.empty.no_services')}
      </div>
      <div style={{ fontSize: '0.8125rem' }}>
        {t('metrics_view.empty.create_first')}
      </div>
    </div>
  );
}
