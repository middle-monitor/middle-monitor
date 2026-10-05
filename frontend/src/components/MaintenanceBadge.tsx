import { HiOutlineWrenchScrewdriver } from 'react-icons/hi2';
import { useTranslation } from 'react-i18next';
import type { MaintenanceWindow } from '../api';

export function getWindowState(w: MaintenanceWindow): 'active' | 'scheduled' | 'past' {
  const now = Date.now();
  const start = new Date(w.starts_at).getTime();
  const end = new Date(w.ends_at).getTime();
  if (now >= start && now <= end) return 'active';
  if (now < start) return 'scheduled';
  return 'past';
}

export function findMaintenanceForTarget(
  windows: MaintenanceWindow[],
  targetType: 'host' | 'service',
  targetId: number,
): MaintenanceWindow | null {
  const relevant = windows.filter(
    (w) => w.target_type === targetType && w.target_id === targetId,
  );
  const active = relevant.find((w) => getWindowState(w) === 'active');
  if (active) return active;
  return relevant.find((w) => getWindowState(w) === 'scheduled') ?? null;
}

const fmtDate = (iso: string) =>
  new Date(iso).toLocaleString(undefined, {
    day: '2-digit',
    month: 'short',
    hour: '2-digit',
    minute: '2-digit',
  });

interface Props {
  window: MaintenanceWindow;
}

export function MaintenanceBadge({ window: w }: Props) {
  const { t } = useTranslation();
  const state = getWindowState(w);
  const isActive = state === 'active';

  const label = isActive
    ? t('maintenance_badge.downtime_until', { date: fmtDate(w.ends_at) })
    : t('maintenance_badge.scheduled', { date: fmtDate(w.starts_at) });

  const title = isActive
    ? t('maintenance_badge.under_maintenance_until', { date: fmtDate(w.ends_at) })
    : t('maintenance_badge.scheduled_from_to', { start: fmtDate(w.starts_at), end: fmtDate(w.ends_at) });

  return (
    <span
      title={title}
      style={{
        display: 'inline-flex',
        alignItems: 'center',
        gap: '0.2rem',
        background: isActive ? 'rgba(251,191,36,0.12)' : 'rgba(139,92,246,0.12)',
        color: isActive ? '#d97706' : '#7c3aed',
        border: `1px solid ${isActive ? 'rgba(251,191,36,0.35)' : 'rgba(139,92,246,0.35)'}`,
        borderRadius: 999,
        padding: '0.1rem 0.55rem',
        fontSize: '0.68rem',
        fontWeight: 700,
        whiteSpace: 'nowrap',
        flexShrink: 0,
      }}
    >
      <HiOutlineWrenchScrewdriver style={{ width: '0.7rem', height: '0.7rem' }} />
      {label}
    </span>
  );
}
