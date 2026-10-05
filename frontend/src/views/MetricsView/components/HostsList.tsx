import { useState } from 'react';
import {
  HiLocationMarker,
  HiShieldExclamation,
  HiCheckCircle,
  HiXCircle,
  HiQuestionMarkCircle,
  HiPencil,
  HiTrash,
  HiPlus,
  HiInbox,
} from 'react-icons/hi';
import { useTranslation } from 'react-i18next';
import { ConfirmDialog } from '../../../components/ConfirmDialog';

import type { HoveredResult } from '../types';
import { processServiceData } from '../utils/serviceData';
import type {
  Host,
  ServiceWithResults,
  ServiceResult,
  Service,
  AgentMetricPoint,
} from '../../../api';

import { AgentServiceChart } from './AgentServiceChart';
import { CertificateServiceDisplay } from './CertificateServiceDisplay';
import { LatencyChart } from './LatencyChart';
import { ServiceCard } from './ServiceCard';

interface HostsListProps {
  hosts: Host[];
  editingHost: Host | null;
  showAddHost: boolean;
  hostServices: Record<number, ServiceWithResults[]>;
  serviceResults: Record<number, ServiceResult[]>;
  loadingHostServices: Record<number, boolean>;
  selectedHost: number | null;
  getServiceDateRange: (serviceId: number) => string;
  setSelectedHost: (hostId: number | null) => void;
  fetchHostServices: (host: Host) => Promise<void>;
  openModal: (service: Service | Partial<Service>, hosts: Host[]) => void;
  onEditHost: (host: Host) => void;
  deleteService: (serviceId: number) => Promise<any>;
  deleteHost: (hostId: number) => Promise<any>;
  onRefresh: () => void;
  openServiceResultsModal: (service: Service) => Promise<void>;
  setHoveredResult: (result: HoveredResult | null) => void;
}

export function HostsList({
  hosts,
  editingHost,
  showAddHost,
  hostServices,
  serviceResults,
  loadingHostServices,
  selectedHost,
  getServiceDateRange,
  setSelectedHost,
  fetchHostServices,
  openModal,
  onEditHost,
  deleteService,
  deleteHost,
  onRefresh,
  openServiceResultsModal,
  setHoveredResult,
}: HostsListProps): JSX.Element | null {
  const { t } = useTranslation();
  const [pendingDeleteHost, setPendingDeleteHost] = useState<{ id: number; name: string } | null>(null);

  if (hosts.length === 0 || editingHost || showAddHost) {
    return null;
  }

  const operationalCount = hosts.filter((h) => h.status === 'success').length;
  const failingCount = hosts.filter((h) => h.status === 'failure').length;
  const warningCount = hosts.filter((h) => h.status === 'warning').length;
  const unknownCount = hosts.filter((h) => h.status === 'unknown' || !h.status).length;

  const getStatusColor = (status: string | undefined) => {
    switch (status) {
      case 'failure':
        return {
          bg: 'var(--status-error-bg)',
          border: 'var(--status-error-border)',
          bar: 'var(--status-error)',
        };
      case 'success':
        return {
          bg: 'var(--status-success-bg)',
          border: 'var(--status-success-border)',
          bar: 'var(--status-success)',
        };
      case 'warning':
        return {
          bg: 'var(--status-warning-bg)',
          border: 'var(--status-warning-border)',
          bar: 'var(--status-warning)',
        };
      default:
        return {
          bg: 'var(--bg-secondary)',
          border: 'var(--border-primary)',
          bar: 'var(--text-tertiary)',
        };
    }
  };

  return (
    <>
      <div style={{ marginTop: '1rem' }}>
        <div
          style={{
            display: 'flex',
            justifyContent: 'space-between',
            alignItems: 'center',
            marginBottom: '1rem',
            padding: '0 0.25rem',
          }}>
          <h3
            style={{
              color: 'var(--text-primary)',
              fontSize: '0.875rem',
              fontWeight: 600,
              margin: 0,
            }}>
            {t('metrics_view.hosts_title', { count: hosts.length })}
          </h3>
          <div style={{ fontSize: '0.75rem', color: 'var(--text-tertiary)' }}>
            {t('metrics_view.status_operational', { count: operationalCount })} •{' '}
            {t('metrics_view.status_failing', { count: failingCount })} •{' '}
            {t('metrics_view.status_warning', { count: warningCount })} •{' '}
            {t('metrics_view.status_unknown', { count: unknownCount })}
          </div>
        </div>

        <div style={{ display: 'flex', flexDirection: 'column', gap: '0.75rem' }}>
          {hosts.map((host) => {
            const statusColors = getStatusColor(host.status);

            return (
              <div
                key={host.id}
                className='card'
                style={{
                  position: 'relative',
                  overflow: 'hidden',
                  borderColor: statusColors.border,
                }}>
                {/* Status indicator bar */}
                <div
                  style={{
                    position: 'absolute',
                    top: 0,
                    left: 0,
                    right: 0,
                    height: '3px',
                    background: `linear-gradient(90deg, ${statusColors.bar} 0%, ${statusColors.bar}50 100%)`,
                  }}
                />

                <div
                  style={{
                    display: 'flex',
                    justifyContent: 'space-between',
                    alignItems: 'flex-start',
                    paddingTop: '0.5rem',
                  }}>
                  <div style={{ flex: 1 }}>
                    {/* Host Header */}
                    <div
                      style={{
                        display: 'flex',
                        alignItems: 'center',
                        gap: '0.75rem',
                        marginBottom: '1rem',
                        flexWrap: 'wrap',
                      }}>
                      <h4
                        style={{
                          color: 'var(--text-primary)',
                          fontSize: '1rem',
                          fontWeight: 600,
                          margin: 0,
                          display: 'flex',
                          alignItems: 'center',
                          gap: '0.5rem',
                        }}>
                        <HiLocationMarker
                          style={{ fontSize: '1.125rem', color: 'var(--brand-primary)' }}
                        />
                        <span>{host.display_name || host.name}</span>
                      </h4>
                      {host.status && (
                        <span
                          className={`status-badge ${
                            host.status === 'failure'
                              ? 'status-down'
                              : host.status === 'success'
                              ? 'status-healthy'
                              : 'status-degraded'
                          }`}>
                          {host.status === 'failure' ? (
                            <>
                              <HiXCircle />
                              {t('metrics_view.status.failure')}
                            </>
                          ) : host.status === 'success' ? (
                            <>
                              <HiCheckCircle />
                              {t('metrics_view.status.operational')}
                            </>
                          ) : host.status === 'warning' ? (
                            <>
                              <HiShieldExclamation />
                              {t('metrics_view.status.warning')}
                            </>
                          ) : (
                            <>
                              <HiQuestionMarkCircle />
                              {t('metrics_view.status.unknown')}
                            </>
                          )}
                        </span>
                      )}
                    </div>

                    {/* Host Details */}
                    <div
                      style={{
                        display: 'grid',
                        gridTemplateColumns: 'repeat(auto-fit, minmax(140px, 1fr))',
                        gap: '0.75rem',
                        padding: '0.75rem',
                        background: 'var(--bg-secondary)',
                        borderRadius: '6px',
                        marginBottom: '0.75rem',
                      }}>
                      <div>
                        <div
                          style={{
                            fontSize: '0.6875rem',
                            color: 'var(--text-tertiary)',
                            textTransform: 'uppercase',
                            letterSpacing: '0.05em',
                            marginBottom: '0.25rem',
                          }}>
                          {t('metrics_view.labels.host')}
                        </div>
                        <div
                          style={{
                            fontSize: '0.8125rem',
                            color: 'var(--text-primary)',
                            fontFamily: 'monospace',
                            fontWeight: 500,
                          }}>
                          {host.host}
                        </div>
                      </div>
                      <div>
                        <div
                          style={{
                            fontSize: '0.6875rem',
                            color: 'var(--text-tertiary)',
                            textTransform: 'uppercase',
                            letterSpacing: '0.05em',
                            marginBottom: '0.25rem',
                          }}>
                          {t('metrics_view.labels.service')}
                        </div>
                        <div
                          style={{
                            fontSize: '0.8125rem',
                            color: 'var(--text-primary)',
                            fontWeight: 500,
                          }}>
                          {host.service}
                        </div>
                      </div>
                      {hostServices[host.id] && hostServices[host.id].length > 0 && (
                        <div>
                          <div
                            style={{
                              fontSize: '0.6875rem',
                              color: 'var(--text-tertiary)',
                              textTransform: 'uppercase',
                              letterSpacing: '0.05em',
                              marginBottom: '0.25rem',
                            }}>
                            {t('metrics_view.labels.services')}
                          </div>
                          <div
                            style={{
                              fontSize: '0.8125rem',
                              color: 'var(--brand-primary)',
                              fontWeight: 600,
                            }}>
                            {t('metrics_view.service_count', {
                              count: hostServices[host.id]?.length || 0,
                            })}
                          </div>
                        </div>
                      )}
                    </div>
                  </div>

                  {/* Action Buttons */}
                  <div style={{ display: 'flex', gap: '0.5rem' }}>
                    <button
                      onClick={async () => {
                        const newSelectedHost =
                          host.id === selectedHost ? null : host.id;
                        setSelectedHost(newSelectedHost);
                        if (newSelectedHost !== null) {
                          await fetchHostServices(host);
                        }
                      }}
                      className='btn btn-secondary'
                      style={{
                        background:
                          selectedHost === host.id
                            ? 'var(--brand-primary-light)'
                            : undefined,
                        borderColor:
                          selectedHost === host.id
                            ? 'var(--brand-primary-border)'
                            : undefined,
                        color:
                          selectedHost === host.id
                            ? 'var(--brand-primary)'
                            : undefined,
                      }}>
                      {selectedHost === host.id ? (
                        <HiXCircle style={{ fontSize: '0.875rem' }} />
                      ) : (
                        <HiPlus style={{ fontSize: '0.875rem' }} />
                      )}
                      <span>{t('metrics_view.checks')}</span>
                    </button>
                    <button className='btn btn-ghost' onClick={() => onEditHost(host)}>
                      <HiPencil style={{ fontSize: '0.875rem' }} />
                    </button>
                    <button
                      onClick={() => setPendingDeleteHost({ id: host.id, name: host.display_name || host.name })}
                      className='btn btn-danger'>
                      <HiTrash style={{ fontSize: '0.875rem' }} />
                    </button>
                  </div>
                </div>

                {/* Expanded Services Section */}
                {selectedHost === host.id && (
                  <div
                    style={{
                      marginTop: '1rem',
                      paddingTop: '1rem',
                      borderTop: '1px solid var(--border-primary)',
                    }}>
                    <div
                      style={{
                        display: 'flex',
                        justifyContent: 'space-between',
                        alignItems: 'center',
                        marginBottom: '1rem',
                      }}>
                      <h4
                        style={{
                          color: 'var(--text-primary)',
                          fontSize: '0.8125rem',
                          fontWeight: 600,
                          margin: 0,
                          textTransform: 'uppercase',
                          letterSpacing: '0.05em',
                        }}>
                        {t('metrics_view.labels.services')}
                      </h4>
                      <button
                        onClick={() => {
                          openModal({ host_id: host.id } as Partial<Service>, hosts);
                        }}
                        className='btn btn-primary'>
                        <HiPlus style={{ fontSize: '0.875rem' }} />
                        <span>{t('metrics_view.add_service')}</span>
                      </button>
                    </div>

                    {loadingHostServices[host.id] ? (
                      <div className='loading'>
                        <div className='loading-spinner' />
                        {t('metrics_view.loading_services')}
                      </div>
                    ) : hostServices[host.id] && hostServices[host.id].length > 0 ? (
                      <div
                        style={{
                          display: 'flex',
                          flexDirection: 'column',
                          gap: '0.75rem',
                        }}>
                        {hostServices[host.id].map((service) => {
                          const results = serviceResults[service.id] || [];
                          const serviceDateRange = getServiceDateRange(service.id);
                          const processedData = processServiceData(
                            service,
                            results,
                            serviceDateRange
                          );

                          return (
                            <ServiceCard
                              key={service.id}
                              service={service}
                              results={processedData.results}
                              filteredResults={processedData.filteredResults}
                              recentResults={processedData.recentResults}
                              resultsWithLatency={processedData.resultsWithLatency}
                              successCount={processedData.successCount}
                              warningCount={processedData.warningCount}
                              failureCount={processedData.failureCount}
                              hasAnyResults={processedData.hasAnyResults}
                              hasResultsInRange={processedData.hasResultsInRange}
                              status={processedData.status}
                              serviceDateRange={serviceDateRange}
                              hosts={hosts}
                              onEdit={openModal}
                              onDelete={deleteService}
                              onRefresh={onRefresh}
                              onOpenResultsModal={openServiceResultsModal}
                              onSetHoveredResult={setHoveredResult}
                              isHostService={true}
                              renderChart={({
                                resultsWithLatency,
                                service,
                                onSetHoveredResult,
                              }) => {
                                const serviceType = service.type.toLowerCase();

                                if (['http', 'sql', 'snmp'].includes(serviceType)) {
                                  return (
                                    <LatencyChart
                                      resultsWithLatency={resultsWithLatency}
                                      service={service}
                                      onSetHoveredResult={onSetHoveredResult}
                                    />
                                  );
                                }

                                if (serviceType === 'certificate') {
                                  const allSorted = [...processedData.results].sort(
                                    (a, b) => new Date(b.timestamp).getTime() - new Date(a.timestamp).getTime()
                                  );
                                  return (
                                    <CertificateServiceDisplay
                                      latestResult={allSorted[0] ?? null}
                                    />
                                  );
                                }

                                if (serviceType.startsWith('agent_')) {
                                  const expectedMetricType = serviceType.replace(
                                    'agent_',
                                    ''
                                  );

                                  const metrics: AgentMetricPoint[] =
                                    processedData.results
                                      .filter(
                                        (r) =>
                                          r.metric_type === expectedMetricType &&
                                          r.metric_value !== undefined
                                      )
                                      .map((r) => {
                                        let metadata:
                                          | AgentMetricPoint['metadata']
                                          | undefined;
                                        if (r.metadata) {
                                          try {
                                            metadata = JSON.parse(r.metadata);
                                          } catch (e) {
                                            // Ignore parse errors
                                          }
                                        }
                                        return {
                                          value: r.metric_value!,
                                          metadata,
                                          timestamp: r.timestamp,
                                        };
                                      })
                                      .sort(
                                        (a, b) =>
                                          new Date(a.timestamp).getTime() -
                                          new Date(b.timestamp).getTime()
                                      );
                                  return (
                                    <AgentServiceChart
                                      service={service}
                                      metrics={metrics}
                                    />
                                  );
                                }

                                return null;
                              }}
                            />
                          );
                        })}
                      </div>
                    ) : (
                      <div className='empty-state'>
                        <HiInbox className='empty-state-icon' />
                        <div className='empty-state-title'>{t('metrics_view.empty.no_services')}</div>
                        <div className='empty-state-description'>
                          {t('metrics_view.empty.create_first')}
                        </div>
                      </div>
                    )}
                  </div>
                )}
              </div>
            );
          })}
        </div>
      </div>
      {pendingDeleteHost && (
        <ConfirmDialog
          title={t('common.delete')}
          message={t('metrics_view.delete_host_confirm', { name: pendingDeleteHost.name })}
          onConfirm={async () => {
            try {
              await deleteHost(pendingDeleteHost.id);
              onRefresh();
            } catch (err: any) {
              if (err.response?.status === 409) {
                alert(t('host_detail.delete_blocked_services'));
              } else {
                alert(t('metrics_view.delete_error'));
              }
            } finally {
              setPendingDeleteHost(null);
            }
          }}
          onCancel={() => setPendingDeleteHost(null)}
        />
      )}
    </>
  );
}
