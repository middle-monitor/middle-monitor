import type { ServiceResult } from '../api';
import { getResultValue, type ServiceUiStatus } from '../utils/serviceStatus';

const STATUS_COLORS: Record<ServiceUiStatus, string> = {
  healthy: 'var(--status-success)',
  warning: 'var(--status-warning)',
  failing: 'var(--status-error)',
  unknown: 'var(--text-secondary)',
};

const MAX_POINTS = 30;

/** Sparkline of the most recent result values, colored by the service UI status. */
export function ServiceSparkline({
  serviceType,
  results,
  status,
  width = 120,
  height = 28,
}: {
  serviceType: string;
  results: ServiceResult[];
  status: ServiceUiStatus;
  width?: number;
  height?: number;
}) {
  const values = [...results]
    .sort((a, b) => new Date(a.timestamp).getTime() - new Date(b.timestamp).getTime())
    .slice(-MAX_POINTS)
    .map((r) => getResultValue(serviceType, r))
    .filter((v): v is number => v != null);
  if (values.length < 2) {
    return <span className="services-latency services-latency-na">—</span>;
  }
  const min = Math.min(...values);
  const max = Math.max(...values);
  const padding = { top: 2, right: 2, bottom: 2, left: 2 };
  const innerW = width - padding.left - padding.right;
  const innerH = height - padding.top - padding.bottom;
  const points = values
    .map((v, i) => {
      const x = padding.left + (i / (values.length - 1)) * innerW;
      const y =
        max === min
          ? padding.top + innerH / 2
          : padding.top + innerH - ((v - min) / (max - min)) * innerH;
      return `${x},${y}`;
    })
    .join(' ');
  return (
    <svg width={width} height={height} style={{ display: 'block', overflow: 'visible' }} aria-hidden>
      <polyline
        fill="none"
        stroke={STATUS_COLORS[status]}
        strokeWidth="1.5"
        points={points}
        vectorEffect="non-scaling-stroke"
      />
    </svg>
  );
}
