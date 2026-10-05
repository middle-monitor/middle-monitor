import { HiClock } from 'react-icons/hi';
import { useTranslation } from 'react-i18next';

export function ServiceLoadingState() {
  const { t } = useTranslation();

  return (
    <div
      style={{
        padding: '2rem',
        textAlign: 'center',
        color: '#9CA3AF',
      }}>
      <HiClock
        style={{
          fontSize: '2rem',
          marginBottom: '1rem',
          animation: 'spin 1s linear infinite',
        }}
      />
      <div>{t('metrics_view.loading_services_state')}</div>
    </div>
  );
}
