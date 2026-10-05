import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useQuery } from '@tanstack/react-query';
import { HiOutlineServerStack } from 'react-icons/hi2';

import {
  type Service,
  type ServiceResult,
  type ServiceWithResults,
  type Host,
} from '../api';
import { useServiceModal } from '../contexts/ServiceModalContext';
import { useDateRange } from '../contexts/DateRangeContext';
import { useOrgApi, useOrgQueryScope } from '../hooks/useOrgApi';
import { useQueryRefresh } from '../hooks/useQueryRefresh';
import { allHostsKey } from '../queryClient';
import { RefreshControl } from '../components/RefreshControl';

import type { HoveredResult } from './MetricsView/types';
import { AddHostForm } from './MetricsView/components/AddHostForm';
import { ServiceDetailsSidebar } from './MetricsView/components/ServiceDetailsSidebar';
import { ServiceResultsModal } from './MetricsView/components/ServiceResultsModal';
import { MonitoringHeader } from './MetricsView/components/MonitoringHeader';
import { HostsList } from './MetricsView/components/HostsList';
import { Skeleton } from '../components/Skeleton';

function MetricsView() {
  const { t } = useTranslation();
  const orgApi = useOrgApi();
  const scope = useOrgQueryScope();
  const {
    openModal,
    isOpen: isModalOpen,
  } = useServiceModal();
  const [services] = useState<ServiceWithResults[]>([]);
  const [hostServices, setHostServices] = useState<
    Record<number, ServiceWithResults[]>
  >({});
  const [serviceResults, setServiceResults] = useState<
    Record<number, ServiceResult[]>
  >({});
  const [showAddHost, setShowAddHost] = useState(false);
  const [editingHost, setEditingHost] = useState<Host | null>(null);
  const [selectedHost, setSelectedHost] = useState<number | null>(null);
  const [loadingHostServices, setLoadingHostServices] = useState<
    Record<number, boolean>
  >({});
  const [hoveredResult, setHoveredResult] = useState<HoveredResult | null>(
    null
  );
  const [serviceDateRanges] = useState<
    Record<number, string>
  >({});

  const { dateRange, setDateRange } = useDateRange();
  const [selectedServiceForResults, setSelectedServiceForResults] =
    useState<Service | null>(null);
  const [serviceResultsModal, setServiceResultsModal] = useState<
    ServiceResult[]
  >([]);
  const [loadingServiceResults, setLoadingServiceResults] = useState(false);

  const resultsDateRange = {
    start: dateRange.start.toISOString().split('.')[0],
    end: dateRange.end.toISOString().split('.')[0],
  };

  const getServiceDateRange = (serviceId: number): string => {
    return serviceDateRanges[serviceId] || '1h';
  };

  // Unrouted: /metrics renders the explorer, which carries the 10s the
  // criterion names for Metrics, so this view is one of the eleven left Off.
  const { refetchInterval, buildControl } = useQueryRefresh(
    'metrics',
    0,
    isModalOpen || selectedServiceForResults !== null,
  );

  const hostsQueryKey = allHostsKey(scope);
  const hostsQuery = useQuery({
    queryKey: hostsQueryKey,
    queryFn: async () => ((await orgApi.hosts.list()).data as Host[]) || [],
    refetchInterval,
  });

  const hosts = hostsQuery.data ?? [];

  const autoRefresh = buildControl({
    query: hostsQuery,
    queryKey: hostsQueryKey,
    prefix: hostsQueryKey,
  });
  const { refresh } = autoRefresh;

  const fetchHostServices = async (host: Host) => {
    if (loadingHostServices[host.id]) {
      return;
    }

    setLoadingHostServices((prev) => ({ ...prev, [host.id]: true }));
    try {
      const response = await orgApi.hosts.getServicesByName(host.name);
      const servicesWithResults = response.data || [];

      setHostServices((prev) => ({
        ...prev,
        [host.id]: servicesWithResults,
      }));

      const resultsMap: Record<number, ServiceResult[]> = {};
      servicesWithResults.forEach((serviceWithResults) => {
        resultsMap[serviceWithResults.id] = serviceWithResults.results || [];
      });
      setServiceResults((prev) => ({ ...prev, ...resultsMap }));
    } catch (err) {
      console.error(`Error fetching services for host ${host.name}:`, err);
    } finally {
      setLoadingHostServices((prev) => ({ ...prev, [host.id]: false }));
    }
  };

  const fetchServiceResultsWithDateRange = async (
    service: Service,
    startDate: string,
    endDate: string
  ) => {
    setLoadingServiceResults(true);
    try {
      const response = await orgApi.services.getResults(service.id, startDate, endDate);
      setServiceResultsModal(response.data || []);
    } catch (err) {
      console.error(`Error fetching results for service ${service.id}:`, err);
      setServiceResultsModal([]);
    } finally {
      setLoadingServiceResults(false);
    }
  };

  const openServiceResultsModal = async (service: Service) => {
    setSelectedServiceForResults(service);
    await fetchServiceResultsWithDateRange(
      service,
      resultsDateRange.start,
      resultsDateRange.end
    );
  };

  const handleDateRangeChange = async (start: string, end: string) => {
    setDateRange({ start: new Date(start), end: new Date(end) });
    if (selectedServiceForResults) {
      await fetchServiceResultsWithDateRange(
        selectedServiceForResults,
        start,
        end
      );
    }
  };

  // Only the results area waits on the first load: a failed refresh keeps the
  // last known hosts on screen.
  if (hostsQuery.isLoadingError) {
    return <div className='error-message'>{t('monitoring.load_error')}</div>;
  }

  return (
    <div
      style={{
        position: 'relative',
        marginRight: hoveredResult ? '380px' : '0',
        transition: 'margin-right 0.3s ease',
      }}>
      {/* Page Header */}
      <div className='page-header'>
        <div>
          <h1 className='page-title'>{t('monitoring.title')}</h1>
          <p className='page-subtitle'>
            {t('monitoring.subtitle')}
          </p>
        </div>
        <RefreshControl control={autoRefresh} />
      </div>

      {/* Monitoring Header with Add Host Button */}
      <MonitoringHeader
        showAddHost={showAddHost}
        onToggleAddHost={() => setShowAddHost(!showAddHost)}
      />

      {/* Add/Edit Host Form */}
      {showAddHost && !editingHost && (
        <AddHostForm
          onSuccess={() => {
            setShowAddHost(false);
            refresh();
          }}
          onCancel={() => setShowAddHost(false)}
        />
      )}
      {editingHost && (
        <AddHostForm
          host={editingHost}
          onSuccess={() => {
            setEditingHost(null);
            refresh();
          }}
          onCancel={() => setEditingHost(null)}
        />
      )}

      {/* Hosts List */}
      {hostsQuery.isPending ? (
        <Skeleton rows={6} />
      ) : (
        <HostsList
          hosts={hosts}
          editingHost={editingHost}
          showAddHost={showAddHost}
          hostServices={hostServices}
          serviceResults={serviceResults}
          loadingHostServices={loadingHostServices}
          selectedHost={selectedHost}
          getServiceDateRange={getServiceDateRange}
          setSelectedHost={setSelectedHost}
          fetchHostServices={fetchHostServices}
          openModal={openModal}
          onEditHost={setEditingHost}
          deleteService={(serviceId: number) => orgApi.services.delete(serviceId)}
          deleteHost={(hostId: number) => orgApi.hosts.delete(hostId)}
          onRefresh={refresh}
          openServiceResultsModal={openServiceResultsModal}
          setHoveredResult={setHoveredResult}
        />
      )}

      {/* Empty State */}
      {!hostsQuery.isPending && hosts.length === 0 && !showAddHost && (
        <div className='empty-state'>
          <HiOutlineServerStack className='empty-state-icon' />
          <div className='empty-state-title'>{t('monitoring.empty.title')}</div>
          <div className='empty-state-description'>
            {t('monitoring.empty.desc')}
          </div>
        </div>
      )}

      {/* Service Details Sidebar */}
      {hoveredResult && (
        <ServiceDetailsSidebar
          hoveredResult={hoveredResult}
          service={
            services.find((s) => s.id === hoveredResult.serviceId) ||
            Object.values(hostServices)
              .flat()
              .find((s) => s.id === hoveredResult.serviceId)
          }
          onClose={() => setHoveredResult(null)}
        />
      )}

      {/* Service Results Modal */}
      {selectedServiceForResults && (
        <ServiceResultsModal
          service={selectedServiceForResults}
          results={serviceResultsModal}
          loading={loadingServiceResults}
          dateRange={resultsDateRange}
          onDateRangeChange={handleDateRangeChange}
          onClose={() => setSelectedServiceForResults(null)}
        />
      )}
    </div>
  );
}

export default MetricsView;
