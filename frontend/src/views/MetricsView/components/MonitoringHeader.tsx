import { HiPlus, HiMinus } from 'react-icons/hi';
import { HiSignal } from 'react-icons/hi2';
import { useTranslation } from 'react-i18next';

interface MonitoringHeaderProps {
  showAddHost: boolean;
  onToggleAddHost: () => void;
}

export function MonitoringHeader({
  showAddHost,
  onToggleAddHost,
}: MonitoringHeaderProps) {
  const { t } = useTranslation();
  return (
    <div className='card'>
      <div
        style={{
          display: 'flex',
          justifyContent: 'space-between',
          alignItems: 'center',
        }}>
        <div>
          <h2 className='card-title' style={{ marginBottom: '0.5rem' }}>
            <HiSignal
              className='card-title-icon'
              style={{ color: 'var(--brand-primary)' }}
            />
            <span>{t('metrics_view.monitoring_header.title')}</span>
          </h2>
          <p
            style={{
              fontSize: '0.8125rem',
              color: 'var(--text-secondary)',
              margin: 0,
            }}>
            {t('metrics_view.monitoring_header.subtitle')}
          </p>
        </div>
        <button
          onClick={onToggleAddHost}
          className='btn btn-primary'
          style={{
            whiteSpace: 'nowrap',
          }}>
          {showAddHost ? (
            <HiMinus style={{ fontSize: '0.875rem' }} />
          ) : (
            <HiPlus style={{ fontSize: '0.875rem' }} />
          )}
          <span>{t('hosts.new_host')}</span>
        </button>
      </div>
    </div>
  );
}
