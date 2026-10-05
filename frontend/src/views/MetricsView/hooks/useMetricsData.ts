import { useState } from 'react';
import {
  type SystemMetric,
  type Host,
  type Service,
  type ServiceResult,
  type ServiceWithResults,
} from '../../../api';
import { useOrgApi } from '../../../hooks/useOrgApi';

export function useMetricsData() {
  const orgApi = useOrgApi();
  const [metrics, setMetrics] = useState<SystemMetric[]>([]);
  const [hosts, setHosts] = useState<Host[]>([]);
  const [services] = useState<ServiceWithResults[]>([]);
  const [hostServices, setHostServices] = useState<
    Record<number, ServiceWithResults[]>
  >({});
  const [serviceResults, setServiceResults] = useState<
    Record<number, ServiceResult[]>
  >({});
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [loadingHostServices, setLoadingHostServices] = useState<
    Record<number, boolean>
  >({});

  const fetchData = async () => {
    try {
      const [metricsResponse, hostsResponse] = await Promise.all([
        orgApi.metrics.list(),
        orgApi.hosts.list(),
      ]);
      setMetrics(metricsResponse.data || []);
      setHosts(hostsResponse.data || []);
      setError(null);
    } catch (err) {
      setError('Failed to load metrics');
      console.error(err);
    } finally {
      setLoading(false);
    }
  };

  const fetchHostServices = async (host: Host) => {
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
  ): Promise<ServiceResult[]> => {
    try {
      const response = await orgApi.services.getResults(service.id, startDate, endDate);
      return response.data || [];
    } catch (err) {
      console.error(`Error fetching results for service ${service.id}:`, err);
      return [];
    }
  };

  return {
    metrics,
    hosts,
    services,
    hostServices,
    serviceResults,
    loading,
    error,
    loadingHostServices,
    fetchData,
    fetchHostServices,
    fetchServiceResultsWithDateRange,
    setServiceResults,
  };
}
