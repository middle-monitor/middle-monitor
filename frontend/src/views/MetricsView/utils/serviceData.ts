import type { Service, ServiceResult } from '../../../api';

import { filterResultsByDateRange } from './dateRanges';
import { getServiceStatus } from '../../../utils/serviceStatus';

export interface ProcessedServiceData {
  results: ServiceResult[];
  filteredResults: ServiceResult[];
  recentResults: ServiceResult[];
  resultsWithLatency: ServiceResult[];
  successCount: number;
  warningCount: number;
  failureCount: number;
  status: string;
  hasAnyResults: boolean;
  hasResultsInRange: boolean;
}

export const processServiceData = (
  service: Service,
  results: ServiceResult[],
  serviceDateRange: string
): ProcessedServiceData => {
  const filteredResults = filterResultsByDateRange(results, serviceDateRange);

  const recentResults = filteredResults.slice(0, 500).reverse();

  const resultsWithLatency =
    service.type === 'sql'
      ? recentResults.filter(
          (r) => r.latency !== null && r.latency !== undefined
        )
      : recentResults.filter(
          (r) =>
            r.latency !== null &&
            r.latency !== undefined &&
            Number(r.latency) > 0
        );

  const successCount = filteredResults.filter(
    (r) => r.status === 'success'
  ).length;
  const failureCount = filteredResults.filter(
    (r) => r.status === 'failure'
  ).length;
  const warningCount = filteredResults.filter(
    (r) => r.status === 'warning'
  ).length;
  // Smoothed status shared with the services/host pages: failing/warning only
  // after max_attempts consecutive breaches.
  const status = getServiceStatus({ ...service, results });

  const hasAnyResults = results.length > 0;
  const hasResultsInRange = filteredResults.length > 0;

  return {
    results,
    filteredResults,
    recentResults,
    resultsWithLatency,
    successCount,
    failureCount,
    warningCount,
    status,
    hasAnyResults,
    hasResultsInRange,
  };
};
