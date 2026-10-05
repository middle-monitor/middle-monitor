import { useTranslation } from 'react-i18next';
import { HiOutlineArrowPath, HiOutlineExclamationTriangle, HiOutlinePause } from 'react-icons/hi2';
import { REFRESH_INTERVALS, type AutoRefreshControl } from '../hooks/useQueryRefresh';
import './RefreshControl.css';

interface RefreshControlProps {
  control: AutoRefreshControl;
}

/**
 * Manual refresh button plus cadence selector, shared by every view that
 * refreshes on its own. Also reports when the last refresh failed, so data
 * kept on screen is never presented as fresh.
 */
export function RefreshControl({ control }: RefreshControlProps) {
  const { t, i18n } = useTranslation();
  const { interval, setInterval, refresh, refreshing, paused, autoStopped, lastSuccessAt, lastFailed } =
    control;

  const intervalLabel = (ms: number): string => {
    if (ms === 0) return t('common.auto_refresh.off');
    if (ms < 60000) return t('common.auto_refresh.seconds', { value: ms / 1000 });
    return t('common.auto_refresh.minutes', { value: ms / 60000 });
  };

  const time = lastSuccessAt
    ? new Date(lastSuccessAt).toLocaleTimeString(i18n.language)
    : null;

  let status = time
    ? t('common.auto_refresh.updated_at', { time })
    : t('common.auto_refresh.never');
  if (lastFailed) {
    status = time
      ? t('common.auto_refresh.failed_at', { time })
      : t('common.auto_refresh.failed');
  }

  return (
    <div className="refresh-control" data-testid="refresh-control">
      <button
        type="button"
        className="refresh-control-button"
        onClick={refresh}
        disabled={refreshing}
        title={t('common.auto_refresh.refresh_now')}
        aria-label={t('common.auto_refresh.refresh_now')}
      >
        <HiOutlineArrowPath className={refreshing ? 'refresh-control-spinning' : undefined} />
      </button>
      <select
        className="select refresh-control-select"
        value={interval}
        onChange={e => setInterval(Number(e.target.value))}
        aria-label={t('common.auto_refresh.label')}
      >
        {REFRESH_INTERVALS.map(ms => (
          <option key={ms} value={ms}>
            {intervalLabel(ms)}
          </option>
        ))}
      </select>
      <span
        className={`refresh-control-status${lastFailed ? ' refresh-control-status-failed' : ''}`}
        data-testid="refresh-status"
        data-last-success={lastSuccessAt ?? ''}
        data-auto-stopped={autoStopped}
      >
        {paused && interval > 0 && (
          <>
            <HiOutlinePause /> {t('common.auto_refresh.paused')} &middot;{' '}
          </>
        )}
        {lastFailed && <HiOutlineExclamationTriangle />} {status}
      </span>
    </div>
  );
}
