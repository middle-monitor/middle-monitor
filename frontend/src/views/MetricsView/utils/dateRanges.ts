import type { ServiceResult } from '../../../api';

export const getDateRangeStart = (range: string): Date => {
  const now = new Date();
  switch (range) {
    case '1h':
      return new Date(now.getTime() - 60 * 60 * 1000);
    case '6h':
      return new Date(now.getTime() - 6 * 60 * 60 * 1000);
    case '24h':
      return new Date(now.getTime() - 24 * 60 * 60 * 1000);
    case '7d':
      return new Date(now.getTime() - 7 * 24 * 60 * 60 * 1000);
    case '30d':
      return new Date(now.getTime() - 30 * 24 * 60 * 60 * 1000);
    default:
      return new Date(now.getTime() - 60 * 60 * 1000); // Default to 1h
  }
};

export const filterResultsByDateRange = (
  results: ServiceResult[],
  dateRange: string
): ServiceResult[] => {
  if (!dateRange || dateRange === 'all') {
    return results;
  }

  const startDate = getDateRangeStart(dateRange);
  const now = new Date();

  const startTime = startDate.getTime();
  const nowTime = now.getTime();
  const res = results.filter((result) => {
    // RFC 3339 timestamps carry their zone, so getTime() is already absolute.
    const resultTime = new Date(result.timestamp).getTime();
    return resultTime >= startTime && resultTime <= nowTime;
  });

  const allResultsSorted = [...res].sort((a, b) => {
    const dateA = new Date(a.timestamp).getTime();
    const dateB = new Date(b.timestamp).getTime();
    return dateA - dateB;
  });

  return allResultsSorted;
};
