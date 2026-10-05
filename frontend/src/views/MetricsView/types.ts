import type { ServiceResult } from '../../api';

export interface HoveredResult extends ServiceResult {
  serviceId: number;
  serviceName: string;
  maxAttempts: number;
}

export interface ChartDataPoint {
  time: string;
  cpu: number;
  ram: number;
  latency: number | null;
}

export interface ServiceDateRange {
  start: string;
  end: string;
}

