import type { Service, ServiceResult, Host } from '../../../api';
import type { HoveredResult } from '../types';
import { ServiceInfo } from './ServiceInfo';
import { ServiceActions } from './ServiceActions';

interface ServiceCardProps {
  service: Service;
  results: ServiceResult[];
  filteredResults: ServiceResult[];
  recentResults: ServiceResult[];
  resultsWithLatency: ServiceResult[];
  successCount: number;
  warningCount: number;
  failureCount: number;
  hasAnyResults: boolean;
  hasResultsInRange: boolean;
  status: string;
  serviceDateRange: string;
  hosts: Host[];
  isHostService?: boolean;
  onEdit: (service: Service, hosts: Host[]) => void;
  onDelete: (serviceId: number) => Promise<void>;
  onRefresh: () => void;
  onOpenResultsModal: (service: Service) => void;
  onSetHoveredResult: (result: HoveredResult | null) => void;
  renderChart?: (props: {
    resultsWithLatency: ServiceResult[];
    service: Service;
    onSetHoveredResult: (result: HoveredResult | null) => void;
  }) => JSX.Element | null;
}

export function ServiceCard({
  service,
  results,
  filteredResults,
  resultsWithLatency,
  successCount,
  failureCount,
  warningCount,
  hasAnyResults,
  hasResultsInRange,
  status,
  serviceDateRange,
  hosts,
  isHostService: _isHostService = false,
  onEdit,
  onDelete,
  onRefresh,
  onOpenResultsModal,
  onSetHoveredResult,
  renderChart,
}: ServiceCardProps) {
  const getStatusStyles = () => {
    switch (status) {
      case 'healthy':
        return {
          borderColor: 'var(--status-success-border)',
          background: 'var(--surface-primary)',
        };
      case 'failing':
        return {
          borderColor: 'var(--status-error-border)',
          background: 'var(--surface-primary)',
        };
      case 'warning':
        return {
          borderColor: 'var(--status-warning-border)',
          background: 'var(--surface-primary)',
        };
      default:
        return {
          borderColor: 'var(--border-primary)',
          background: 'var(--surface-primary)',
        };
    }
  };

  const statusStyles = getStatusStyles();

  return (
    <div
      key={service.id}
      style={{
        padding: '1rem',
        background: statusStyles.background,
        border: `1px solid ${statusStyles.borderColor}`,
        borderRadius: '8px',
        cursor: 'pointer',
        transition: 'all 0.15s ease',
      }}
      onClick={() => onOpenResultsModal(service)}
      onMouseEnter={(e) => {
        e.currentTarget.style.borderColor = 'var(--brand-primary-border)';
        e.currentTarget.style.boxShadow = 'var(--shadow-md)';
      }}
      onMouseLeave={(e) => {
        e.currentTarget.style.borderColor = statusStyles.borderColor;
        e.currentTarget.style.boxShadow = 'none';
      }}>
      <div
        style={{
          display: 'flex',
          justifyContent: 'space-between',
          alignItems: 'flex-start',
          marginBottom: '0.75rem',
        }}>
        <div style={{ flex: 1 }}>
          <div
            style={{
              fontWeight: 600,
              color: 'var(--brand-primary)',
              marginBottom: '0.375rem',
              display: 'flex',
              alignItems: 'center',
              gap: '0.5rem',
              fontSize: '0.875rem',
            }}>
            <span>
              {service.name} ({service.type.toUpperCase()})
            </span>
          </div>
          {service.host && (
            <div
              style={{
                fontSize: '0.75rem',
                color: 'var(--text-secondary)',
                marginBottom: '0.375rem',
              }}>
              Host: {service.host} | Service: {service.service}
            </div>
          )}
          <div
            style={{
              fontSize: '0.75rem',
              color: 'var(--text-tertiary)',
              marginBottom: '0.5rem',
            }}>
            Intervalle: {service.service_interval || 60}s | Max tentatives:{' '}
            {service.max_attempts || 3}
          </div>
          <ServiceInfo
            successCount={successCount}
            failureCount={failureCount}
            warningCount={warningCount}
            hasAnyResults={hasAnyResults}
            hasResultsInRange={hasResultsInRange}
            serviceDateRange={serviceDateRange}
            results={results}
          />
        </div>
        <div
          style={{
            display: 'flex',
            flexDirection: 'column',
            gap: '0.375rem',
            alignItems: 'flex-end',
          }}
          onClick={(e) => e.stopPropagation()}>
          <ServiceActions
            service={service}
            hosts={hosts}
            onEdit={onEdit}
            onDelete={onDelete}
            onRefresh={onRefresh}
          />
        </div>
      </div>

      {filteredResults.length > 0 && renderChart && (
        <div style={{ marginTop: '0.75rem' }}>
          <div
            style={{
              fontSize: '0.6875rem',
              color: 'var(--text-tertiary)',
              marginBottom: '0.5rem',
              textTransform: 'uppercase',
              letterSpacing: '0.05em',
            }}>
            Historique de latence
          </div>
          <div onClick={(e) => e.stopPropagation()}>
            {renderChart({
              resultsWithLatency,
              service,
              onSetHoveredResult,
            })}
          </div>
        </div>
      )}
    </div>
  );
}
