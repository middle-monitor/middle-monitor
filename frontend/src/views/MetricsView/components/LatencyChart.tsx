import {
  BarChart,
  Bar,
  XAxis,
  YAxis,
  CartesianGrid,
  Tooltip,
  ResponsiveContainer,
} from 'recharts';
import { useTranslation } from 'react-i18next';
import type { ServiceResult, Service } from '../../../api';
import type { HoveredResult } from '../types';
import { useTheme } from '../../../contexts/ThemeContext';

interface LatencyChartProps {
  resultsWithLatency: ServiceResult[];
  service: Service;
  onSetHoveredResult: (result: HoveredResult | null) => void;
}

export function LatencyChart({
  resultsWithLatency,
  service,
  onSetHoveredResult,
}: LatencyChartProps) {
  const { t, i18n } = useTranslation();
  const { isDark } = useTheme();
  const locale = i18n.language;

  const chartColors = {
    axis: isDark ? '#9CA3AF' : '#6B7280',
    grid: isDark ? 'rgba(75, 85, 99, 0.3)' : '#E5E7EB',
    text: isDark ? '#9CA3AF' : '#4B5563',
    tooltipBg: isDark ? '#1F2937' : '#FFFFFF',
    tooltipBorder: isDark ? 'rgba(59, 130, 246, 0.3)' : '#E5E7EB',
    tooltipText: isDark ? '#F9FAFB' : '#111827',
    cursorFill: isDark ? 'rgba(59, 130, 246, 0.15)' : 'rgba(59, 130, 246, 0.1)',
  };

  if (resultsWithLatency.length === 0) {
    return (
      <div
        style={{
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          height: '150px',
          color: 'var(--text-tertiary)',
          fontSize: '0.875rem',
          textAlign: 'center',
        }}>
        {t('metrics_view.latency_chart.no_data')}
      </div>
    );
  }

  const chartData = resultsWithLatency
    .map((r) => {
      const latencyValue =
        r.latency !== null && r.latency !== undefined ? Number(r.latency) : 0;
      const resultDate = new Date(r.timestamp);
      return {
        timestamp: resultDate.toLocaleTimeString(locale, {
          hour: '2-digit',
          minute: '2-digit',
        }),
        fullTimestamp: resultDate,
        latency: latencyValue > 0 ? latencyValue : 0.1,
        result: r,
      };
    })
    .reverse();

  const latencyValues = chartData.map((d) => d.latency);
  const maxLatency = Math.max(...latencyValues, 0);
  const yAxisDomain = [0, Math.max(maxLatency * 1.1, 100)];

  return (
    <div style={{ height: '150px', width: '100%' }}>
      <ResponsiveContainer width='100%' height='100%' minHeight={150}>
        <BarChart
          data={chartData}
          margin={{ top: 10, right: 15, left: 5, bottom: 10 }}>
          <defs>
            <linearGradient
              id={`barGradient-${service.id}`}
              x1='0'
              y1='0'
              x2='0'
              y2='1'>
              <stop offset='0%' stopColor='#3B82F6' stopOpacity={1} />
              <stop offset='100%' stopColor='#2563EB' stopOpacity={0.8} />
            </linearGradient>
          </defs>
          <CartesianGrid
            strokeDasharray='3 3'
            stroke={chartColors.grid}
            vertical={false}
            strokeWidth={1}
          />
          <XAxis
            dataKey='timestamp'
            stroke={chartColors.axis}
            fontSize={11}
            tick={{ fill: chartColors.text, fontWeight: 500 }}
            axisLine={{ stroke: chartColors.grid, strokeWidth: 1 }}
            tickLine={false}
          />
          <YAxis
            domain={yAxisDomain}
            stroke={chartColors.axis}
            fontSize={11}
            tick={{ fill: chartColors.text, fontWeight: 500 }}
            axisLine={{ stroke: chartColors.grid, strokeWidth: 1 }}
            tickLine={false}
            label={{
              value: 'ms',
              angle: -90,
              position: 'insideLeft',
              fill: chartColors.text,
              fontSize: 10,
              fontWeight: 600,
            }}
          />
          <Tooltip
            contentStyle={{
              background: chartColors.tooltipBg,
              border: `1px solid ${chartColors.tooltipBorder}`,
              borderRadius: '8px',
              color: chartColors.tooltipText,
              padding: '12px 16px',
              boxShadow: '0 10px 25px -5px rgba(0, 0, 0, 0.1), 0 8px 10px -6px rgba(0, 0, 0, 0.1)',
            }}
            labelStyle={{
              color: '#3B82F6',
              fontSize: '0.8125rem',
              fontWeight: 600,
              marginBottom: '8px',
            }}
            itemStyle={{
              color: chartColors.tooltipText,
              fontSize: '0.875rem',
              fontWeight: 500,
              padding: '2px 0',
            }}
            cursor={{
              fill: chartColors.cursorFill,
            }}
            labelFormatter={(label: string, payload: any[]) => {
              if (
                payload &&
                payload.length > 0 &&
                payload[0].payload?.fullTimestamp
              ) {
                const date = payload[0].payload.fullTimestamp as Date;
                return date.toLocaleString(locale, {
                  day: '2-digit',
                  month: '2-digit',
                  year: 'numeric',
                  hour: '2-digit',
                  minute: '2-digit',
                });
              }
              return label;
            }}
            formatter={(value: any) => [
              `${Number(value).toFixed(2)} ms`,
              t('metrics_view.latency_chart.latency'),
            ]}
            separator=': '
          />
          <Bar
            dataKey='latency'
            fill={`url(#barGradient-${service.id})`}
            name={t('metrics_view.latency_chart.latency_ms')}
            radius={[6, 6, 0, 0]}
            onClick={(data: any) => {
              if (data && data.result) {
                const clickedResult = data.result;
                onSetHoveredResult({
                  ...clickedResult,
                  serviceId: service.id,
                  serviceName: service.name,
                  maxAttempts: service.max_attempts || 3,
                } as HoveredResult);
              }
            }}
            animationDuration={300}
          />
        </BarChart>
      </ResponsiveContainer>
    </div>
  );
}
