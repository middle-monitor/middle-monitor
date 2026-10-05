import {
  HiCheckCircle,
  HiXCircle,
  HiExclamationTriangle,
} from 'react-icons/hi2';

interface ServiceStatusBadgeProps {
  status: string;
  size?: 'sm' | 'md' | 'lg';
}

export function ServiceStatusBadge({
  status,
  size = 'md',
}: ServiceStatusBadgeProps) {
  const sizeStyles = {
    sm: { fontSize: '0.75rem', padding: '0.25rem 0.5rem' },
    md: { fontSize: '0.875rem', padding: '0.5rem 0.75rem' },
    lg: { fontSize: '1rem', padding: '0.75rem 1rem' },
  };

  const statusConfig = {
    success: {
      color: '#22c55e',
      bgColor: 'rgba(34, 197, 94, 0.1)',
      borderColor: 'rgba(34, 197, 94, 0.3)',
      icon: HiCheckCircle,
      label: 'Success',
    },
    warning: {
      color: '#F59E0B',
      bgColor: 'rgba(245, 158, 11, 0.1)',
      borderColor: 'rgba(245, 158, 11, 0.3)',
      icon: HiExclamationTriangle,
      label: 'Warning',
    },
    failure: {
      color: '#ef4444',
      bgColor: 'rgba(239, 68, 68, 0.1)',
      borderColor: 'rgba(239, 68, 68, 0.3)',
      icon: HiXCircle,
      label: 'Critical',
    },
    unknown: {
      color: '#9CA3AF',
      bgColor: 'rgba(156, 163, 175, 0.1)',
      borderColor: 'rgba(156, 163, 175, 0.3)',
      icon: null,
      label: 'Unknown',
    },
  };

  const config =
    statusConfig[status as keyof typeof statusConfig] || statusConfig.unknown;
  const Icon = config.icon;

  return (
    <span
      style={{
        display: 'inline-flex',
        alignItems: 'center',
        gap: '0.25rem',
        color: config.color,
        backgroundColor: config.bgColor,
        border: `1px solid ${config.borderColor}`,
        borderRadius: '4px',
        ...sizeStyles[size],
      }}>
      {Icon && <Icon style={{ fontSize: sizeStyles[size].fontSize }} />}
      <span>{config.label}</span>
    </span>
  );
}
