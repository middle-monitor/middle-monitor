import {
  AreaChart,
  Area,
  Line,
  XAxis,
  YAxis,
  CartesianGrid,
  Tooltip,
  ResponsiveContainer,
} from 'recharts';
import { HiExclamationTriangle } from 'react-icons/hi2';
import { useTranslation } from 'react-i18next';
import type { Service, AgentMetricPoint } from '../../../api';
import { useTheme } from '../../../contexts/ThemeContext';

interface AgentServiceChartProps {
  service: Service;
  metrics: AgentMetricPoint[];
}

export function AgentServiceChart({
  service,
  metrics,
}: AgentServiceChartProps) {
  const { t, i18n } = useTranslation();
  const locale = i18n.language;
  const { isDark } = useTheme();

  // Theme-aware colors for Recharts (SVG doesn't support CSS variables directly)
  const chartColors = {
    axis: isDark ? '#9CA3AF' : '#6B7280',
    grid: isDark ? 'rgba(75, 85, 99, 0.3)' : '#E5E7EB',
    text: isDark ? '#9CA3AF' : '#4B5563',
  };

  if (metrics.length === 0) {
    return (
      <div
        style={{
          background: 'var(--bg-secondary)',
          borderRadius: '8px',
          padding: '1rem',
          border: '1px solid var(--border-primary)',
          textAlign: 'center',
          color: 'var(--text-tertiary)',
          fontSize: '0.875rem',
        }}>
        {t('metrics_view.agent_chart.waiting_data')}
      </div>
    );
  }

  const metricType = service.type.replace('agent_', '').toUpperCase();
  const isRAM = service.type === 'agent_ram';
  const isCPU = service.type === 'agent_cpu';
  const isDisk = service.type === 'agent_disk';
  const isNetwork = service.type === 'agent_network';

  const latest = metrics.length > 0 ? metrics[metrics.length - 1] : null;
  const currentValue = latest?.value ?? null;

  let diskTotalGB = null;
  let diskFreeGB = null;
  if (isDisk) {
    for (const m of metrics) {
      if (m.metadata) {
        const diskTotal = m.metadata.disk_total_gb;
        if (diskTotal !== undefined && diskTotal !== null) {
          const numValue =
            typeof diskTotal === 'string' ? parseFloat(diskTotal) : diskTotal;
          if (!isNaN(numValue) && numValue > 0) {
            diskTotalGB = numValue;
          }
        }
        const diskFree = m.metadata.disk_free_gb;
        if (diskFree !== undefined && diskFree !== null) {
          const numValue =
            typeof diskFree === 'string' ? parseFloat(diskFree) : diskFree;
          if (!isNaN(numValue) && numValue >= 0) {
            diskFreeGB = numValue;
          }
        }
        if (diskTotalGB && diskFreeGB) {
          break;
        }
      }
    }
    if (!diskTotalGB && latest?.metadata?.disk_total_gb) {
      const diskTotal = latest.metadata.disk_total_gb;
      const numValue =
        typeof diskTotal === 'string' ? parseFloat(diskTotal) : diskTotal;
      if (!isNaN(numValue) && numValue > 0) {
        diskTotalGB = numValue;
      }
    }
    if (!diskFreeGB && latest?.metadata?.disk_free_gb) {
      const diskFree = latest.metadata.disk_free_gb;
      const numValue =
        typeof diskFree === 'string' ? parseFloat(diskFree) : diskFree;
      if (!isNaN(numValue) && numValue >= 0) {
        diskFreeGB = numValue;
      }
    }
  }

  const ramTotalGB =
    metrics.find((m) => m.metadata?.ram_total_gb)?.metadata?.ram_total_gb ||
    latest?.metadata?.ram_total_gb;

  let finalRamTotalGB = ramTotalGB;
  if (!finalRamTotalGB && isRAM && metrics.length > 0) {
    for (const m of metrics) {
      if (m.metadata?.ram_total_gb) {
        finalRamTotalGB = m.metadata.ram_total_gb;
        break;
      }
    }
  }

  // Network doesn't have a threshold warning (it's a speed metric, not a percentage)
  const threshold = isNetwork ? null : 90;
  const isWarning = !isNetwork && currentValue !== null && currentValue > threshold!;
  const color = isWarning
    ? '#EF4444'
    : currentValue !== null && currentValue > 70
    ? '#FF9800'
    : '#3B82F6';

  const formatTime = (timestamp: string) => {
    const date = new Date(timestamp);
    const now = new Date();
    const isSameDay = date.toDateString() === now.toDateString();
    if (isSameDay) {
      return date.toLocaleTimeString(locale, {
        hour: '2-digit',
        minute: '2-digit',
      });
    } else {
      return date.toLocaleString(locale, {
        month: '2-digit',
        day: '2-digit',
        hour: '2-digit',
        minute: '2-digit',
      });
    }
  };

  const last10Metrics = metrics.slice(-10);
  const chartData = last10Metrics.map((m) => ({
    time: formatTime(m.timestamp),
    value: Number(m.value.toFixed(2)),
    timestamp: m.timestamp,
    usedPercent: Number(m.value.toFixed(2)),
    metadata: m.metadata,
    diskTotalGB: diskTotalGB,
  }));

  // For network, use adaptive domain based on data, otherwise 0-100 for percentages
  let yAxisDomain: [number, number];
  let yAxisLabel: string;
  
  if (isNetwork) {
    // For network, find max value and add 20% padding
    const maxValue = Math.max(...metrics.map(m => m.value || 0));
    const maxDomain = maxValue > 0 ? maxValue * 1.2 : 10; // At least 10 MB/s domain
    yAxisDomain = [0, Math.max(maxDomain, 10)];
    yAxisLabel = 'MB/s';
  } else {
    yAxisDomain = [0, 100];
    yAxisLabel = '%';
  }

  const renderCustomTooltip = ({ active, payload, label }: any) => {
    if (active && payload && payload.length) {
      const data = payload[0].payload;
      const currentValue = data.usedPercent ?? data.value;

      return (
        <div
          style={{
            background: 'var(--surface-primary)',
            border: '1px solid var(--brand-primary-border)',
            borderRadius: '8px',
            padding: '12px 16px',
            boxShadow: 'var(--shadow-lg)',
          }}>
          <div
            style={{
              color: 'var(--brand-primary)',
              fontSize: '0.8125rem',
              fontWeight: 600,
              marginBottom: '12px',
            }}>
            {label}
          </div>
          <div
            style={{
              display: 'flex',
              flexDirection: 'column',
              gap: '8px',
            }}>
            {isRAM && finalRamTotalGB && (
              <>
                <div
                  style={{
                    fontSize: '0.75rem',
                    fontWeight: 600,
                    color: 'var(--text-tertiary)',
                    textTransform: 'uppercase',
                    letterSpacing: '0.05em',
                    marginTop: '4px',
                    marginBottom: '4px',
                  }}>
                  {t('metrics_view.agent_chart.ram')}
                </div>
                <div
                  style={{
                    display: 'flex',
                    justifyContent: 'space-between',
                    alignItems: 'center',
                    gap: '16px',
                  }}>
                  <span
                    style={{
                      color: 'var(--text-tertiary)',
                      fontSize: '0.8125rem',
                    }}>
                    {t('metrics_view.agent_chart.used')}
                  </span>
                  <span
                    style={{
                      color: 'var(--text-primary)',
                      fontSize: '0.875rem',
                      fontWeight: 600,
                      fontFamily: 'monospace',
                    }}>
                    {currentValue.toFixed(2)}% (
                    {((currentValue * finalRamTotalGB) / 100).toFixed(2)} GB)
                  </span>
                </div>
                <div
                  style={{
                    display: 'flex',
                    justifyContent: 'space-between',
                    alignItems: 'center',
                    gap: '16px',
                  }}>
                  <span
                    style={{
                      color: 'var(--text-tertiary)',
                      fontSize: '0.8125rem',
                    }}>
                    {t('metrics_view.agent_chart.available')}
                  </span>
                  <span
                    style={{
                      color: 'var(--text-primary)',
                      fontSize: '0.875rem',
                      fontWeight: 600,
                      fontFamily: 'monospace',
                    }}>
                    {(100 - currentValue).toFixed(2)}% (
                    {(((100 - currentValue) * finalRamTotalGB) / 100).toFixed(
                      2
                    )}{' '}
                    GB)
                  </span>
                </div>
                <div
                  style={{
                    display: 'flex',
                    justifyContent: 'space-between',
                    alignItems: 'center',
                    gap: '16px',
                    marginBottom: '8px',
                  }}>
                  <span
                    style={{
                      color: 'var(--text-tertiary)',
                      fontSize: '0.8125rem',
                    }}>
                    {t('metrics_view.agent_chart.total')}
                  </span>
                  <span
                    style={{
                      color: 'var(--brand-primary)',
                      fontSize: '0.875rem',
                      fontWeight: 600,
                      fontFamily: 'monospace',
                    }}>
                    {finalRamTotalGB.toFixed(2)} GB
                  </span>
                </div>
              </>
            )}

            {isCPU && (
              <>
                <div
                  style={{
                    fontSize: '0.75rem',
                    fontWeight: 600,
                    color: 'var(--text-tertiary)',
                    textTransform: 'uppercase',
                    letterSpacing: '0.05em',
                    marginTop: '4px',
                    marginBottom: '4px',
                  }}>
                  {t('metrics_view.agent_chart.cpu')}
                </div>
                {(() => {
                  const load1min = data.metadata?.load_1min;
                  const load5min = data.metadata?.load_5min;
                  const load15min = data.metadata?.load_15min;
                  const latestMetric =
                    metrics.length > 0 ? metrics[metrics.length - 1] : null;
                  const finalLoad1min =
                    load1min ?? latestMetric?.metadata?.load_1min;
                  const finalLoad5min =
                    load5min ?? latestMetric?.metadata?.load_5min;
                  const finalLoad15min =
                    load15min ?? latestMetric?.metadata?.load_15min;

                  if (
                    finalLoad1min !== undefined &&
                    finalLoad5min !== undefined &&
                    finalLoad15min !== undefined
                  ) {
                    return (
                      <div
                        style={{
                          display: 'flex',
                          flexDirection: 'column',
                          gap: '6px',
                        }}>
                        <div
                          style={{
                            display: 'flex',
                            justifyContent: 'space-between',
                            alignItems: 'center',
                            gap: '16px',
                          }}>
                          <span
                            style={{
                              color: 'var(--text-tertiary)',
                              fontSize: '0.8125rem',
                            }}>
                            {t('metrics_view.agent_chart.load_average')}
                          </span>
                          <span
                            style={{
                              color: 'var(--text-primary)',
                              fontSize: '0.875rem',
                              fontWeight: 600,
                              fontFamily: 'monospace',
                            }}>
                            {finalLoad1min.toFixed(2)},{' '}
                            {finalLoad5min.toFixed(2)},{' '}
                            {finalLoad15min.toFixed(2)}
                          </span>
                        </div>
                        <div
                          style={{
                            fontSize: '0.6875rem',
                            color: 'var(--text-tertiary)',
                            textAlign: 'right',
                            fontStyle: 'italic',
                          }}>
                          {t('metrics_view.agent_chart.load_periods')}
                        </div>
                      </div>
                    );
                  }

                  return (
                    <div
                      style={{
                        display: 'flex',
                        justifyContent: 'space-between',
                        alignItems: 'center',
                        gap: '16px',
                      }}>
                      <span
                        style={{
                          color: 'var(--text-tertiary)',
                          fontSize: '0.8125rem',
                        }}>
                        {t('metrics_view.agent_chart.usage')}
                      </span>
                      <span
                        style={{
                          color: 'var(--text-primary)',
                          fontSize: '0.875rem',
                          fontWeight: 600,
                          fontFamily: 'monospace',
                        }}>
                        {currentValue.toFixed(2)}%
                      </span>
                    </div>
                  );
                })()}
              </>
            )}

            {isDisk &&
              (() => {
                const diskTotalFromData = data.metadata?.disk_total_gb;
                const finalDiskTotalGB =
                  diskTotalFromData ?? data.diskTotalGB ?? diskTotalGB;

                if (!finalDiskTotalGB) {
                  return (
                    <>
                      <div
                        style={{
                          fontSize: '0.75rem',
                          fontWeight: 600,
                          color: 'var(--text-tertiary)',
                          textTransform: 'uppercase',
                          letterSpacing: '0.05em',
                          marginTop: '4px',
                          marginBottom: '4px',
                          paddingTop: '8px',
                          borderTop: '1px solid var(--border-primary)',
                        }}>
                        {t('metrics_view.agent_chart.disk')}
                      </div>
                      <div
                        style={{
                          display: 'flex',
                          justifyContent: 'space-between',
                          alignItems: 'center',
                          gap: '16px',
                        }}>
                        <span
                          style={{
                            color: 'var(--text-tertiary)',
                            fontSize: '0.8125rem',
                          }}>
                          {t('metrics_view.agent_chart.usage')}
                        </span>
                        <span
                          style={{
                            color: 'var(--text-primary)',
                            fontSize: '0.875rem',
                            fontWeight: 600,
                            fontFamily: 'monospace',
                          }}>
                          {currentValue.toFixed(2)}%
                        </span>
                      </div>
                      <div
                        style={{
                          fontSize: '0.75rem',
                          color: 'var(--text-tertiary)',
                          fontStyle: 'italic',
                          marginTop: '4px',
                        }}>
                        {t('metrics_view.agent_chart.total_size_unavailable')}
                      </div>
                    </>
                  );
                }

                // Use disk_free_gb from metadata if available (more accurate), otherwise calculate
                const diskFreeFromData = data.metadata?.disk_free_gb;
                const finalDiskFreeGB = diskFreeFromData ?? diskFreeGB;
                
                let usedGB: number;
                let freeGB: number;
                
                if (finalDiskFreeGB !== null && finalDiskFreeGB !== undefined && finalDiskTotalGB) {
                  // Use direct free space value (matches macOS Settings)
                  freeGB = typeof finalDiskFreeGB === 'string' ? parseFloat(finalDiskFreeGB) : finalDiskFreeGB;
                  usedGB = finalDiskTotalGB - freeGB;
                } else {
                  // Fallback: calculate from percentage
                  usedGB = (currentValue * finalDiskTotalGB) / 100;
                  freeGB = ((100 - currentValue) * finalDiskTotalGB) / 100;
                }

                return (
                  <>
                    <div
                      style={{
                        fontSize: '0.75rem',
                        fontWeight: 600,
                        color: 'var(--text-tertiary)',
                        textTransform: 'uppercase',
                        letterSpacing: '0.05em',
                        marginTop: '4px',
                        marginBottom: '4px',
                        paddingTop: '8px',
                        borderTop: '1px solid var(--border-primary)',
                      }}>
                      DISK
                    </div>
                    <div
                      style={{
                        display: 'flex',
                        justifyContent: 'space-between',
                        alignItems: 'center',
                        gap: '16px',
                      }}>
                      <span
                        style={{
                          color: 'var(--text-tertiary)',
                          fontSize: '0.8125rem',
                        }}>
                        {t('metrics_view.agent_chart.used_space')}
                      </span>
                      <span
                        style={{
                          color: 'var(--text-primary)',
                          fontSize: '0.875rem',
                          fontWeight: 600,
                          fontFamily: 'monospace',
                        }}>
                        {usedGB.toFixed(2)} GB ({currentValue.toFixed(2)}%)
                      </span>
                    </div>
                    <div
                      style={{
                        display: 'flex',
                        justifyContent: 'space-between',
                        alignItems: 'center',
                        gap: '16px',
                      }}>
                      <span
                        style={{
                          color: 'var(--text-tertiary)',
                          fontSize: '0.8125rem',
                        }}>
                        {t('metrics_view.agent_chart.free_space')}
                      </span>
                      <span
                        style={{
                          color: 'var(--text-primary)',
                          fontSize: '0.875rem',
                          fontWeight: 600,
                          fontFamily: 'monospace',
                        }}>
                        {freeGB.toFixed(2)} GB (
                        {(100 - currentValue).toFixed(2)}%)
                      </span>
                    </div>
                    <div
                      style={{
                        display: 'flex',
                        justifyContent: 'space-between',
                        alignItems: 'center',
                        gap: '16px',
                        marginTop: '4px',
                        paddingTop: '8px',
                        borderTop: '1px solid var(--border-primary)',
                      }}>
                      <span
                        style={{
                          color: 'var(--text-tertiary)',
                          fontSize: '0.8125rem',
                        }}>
                        {t('metrics_view.agent_chart.total')}
                      </span>
                      <span
                        style={{
                          color: 'var(--brand-primary)',
                          fontSize: '0.875rem',
                          fontWeight: 600,
                          fontFamily: 'monospace',
                        }}>
                        {finalDiskTotalGB.toFixed(2)} GB
                      </span>
                    </div>
                  </>
                );
              })()}

            {isNetwork && (
              <>
                <div
                  style={{
                    fontSize: '0.75rem',
                    fontWeight: 600,
                    color: 'var(--text-tertiary)',
                    textTransform: 'uppercase',
                    letterSpacing: '0.05em',
                    marginTop: '4px',
                    marginBottom: '4px',
                    paddingTop: '8px',
                    borderTop: '1px solid var(--border-primary)',
                  }}>
                  {t('metrics_view.agent_chart.network')}
                </div>
                {(() => {
                  // Support both new and old naming
                  const speedIn = data.metadata?.network_speed_in_mb_per_s ?? data.metadata?.network_speed_in_mbps ?? latest?.metadata?.network_speed_in_mb_per_s ?? latest?.metadata?.network_speed_in_mbps ?? 0;
                  const speedOut = data.metadata?.network_speed_out_mb_per_s ?? data.metadata?.network_speed_out_mbps ?? latest?.metadata?.network_speed_out_mb_per_s ?? latest?.metadata?.network_speed_out_mbps ?? 0;
                  const bytesInTotal = data.metadata?.network_bytes_in_total ?? latest?.metadata?.network_bytes_in_total;
                  const bytesOutTotal = data.metadata?.network_bytes_out_total ?? latest?.metadata?.network_bytes_out_total;

                  return (
                    <>
                      <div
                        style={{
                          display: 'flex',
                          justifyContent: 'space-between',
                          alignItems: 'center',
                          gap: '16px',
                        }}>
                        <span
                          style={{
                            color: 'var(--text-tertiary)',
                            fontSize: '0.8125rem',
                          }}>
                          {t('metrics_view.agent_chart.speed_in')}
                        </span>
                        <span
                          style={{
                            color: 'var(--brand-primary)',
                            fontSize: '0.875rem',
                            fontWeight: 600,
                            fontFamily: 'monospace',
                          }}>
                          {speedIn.toFixed(2)} MB/s
                        </span>
                      </div>
                      <div
                        style={{
                          display: 'flex',
                          justifyContent: 'space-between',
                          alignItems: 'center',
                          gap: '16px',
                        }}>
                        <span
                          style={{
                            color: 'var(--text-tertiary)',
                            fontSize: '0.8125rem',
                          }}>
                          {t('metrics_view.agent_chart.speed_out')}
                        </span>
                        <span
                          style={{
                            color: '#F59E0B',
                            fontSize: '0.875rem',
                            fontWeight: 600,
                            fontFamily: 'monospace',
                          }}>
                          {speedOut.toFixed(2)} MB/s
                        </span>
                      </div>
                      <div
                        style={{
                          display: 'flex',
                          justifyContent: 'space-between',
                          alignItems: 'center',
                          gap: '16px',
                        }}>
                        <span
                          style={{
                            color: 'var(--text-tertiary)',
                            fontSize: '0.8125rem',
                          }}>
                          {t('metrics_view.agent_chart.speed_total')}
                        </span>
                        <span
                          style={{
                            color: 'var(--text-primary)',
                            fontSize: '0.875rem',
                            fontWeight: 600,
                            fontFamily: 'monospace',
                          }}>
                          {currentValue.toFixed(2)} MB/s
                        </span>
                      </div>
                      {bytesInTotal !== undefined && bytesOutTotal !== undefined && (
                        <>
                          <div
                            style={{
                              marginTop: '8px',
                              paddingTop: '8px',
                              borderTop: '1px solid var(--border-primary)',
                            }}>
                            <div
                              style={{
                                fontSize: '0.6875rem',
                                color: 'var(--text-tertiary)',
                                marginBottom: '4px',
                                fontStyle: 'italic',
                              }}>
                              {t('metrics_view.agent_chart.totals_since_boot')}
                            </div>
                            <div
                              style={{
                                display: 'flex',
                                justifyContent: 'space-between',
                                alignItems: 'center',
                                gap: '16px',
                                marginTop: '4px',
                              }}>
                              <span
                                style={{
                                  color: 'var(--text-tertiary)',
                                  fontSize: '0.8125rem',
                                }}>
                                {t('metrics_view.agent_chart.received')}
                              </span>
                              <span
                                style={{
                                  color: 'var(--brand-primary)',
                                  fontSize: '0.875rem',
                                  fontWeight: 600,
                                  fontFamily: 'monospace',
                                }}>
                                {(bytesInTotal / (1024 * 1024 * 1024)).toFixed(2)} GB
                              </span>
                            </div>
                            <div
                              style={{
                                display: 'flex',
                                justifyContent: 'space-between',
                                alignItems: 'center',
                                gap: '16px',
                                marginTop: '4px',
                              }}>
                              <span
                                style={{
                                  color: 'var(--text-tertiary)',
                                  fontSize: '0.8125rem',
                                }}>
                                {t('metrics_view.agent_chart.sent')}
                              </span>
                              <span
                                style={{
                                  color: '#F59E0B',
                                  fontSize: '0.875rem',
                                  fontWeight: 600,
                                  fontFamily: 'monospace',
                                }}>
                                {(bytesOutTotal / (1024 * 1024 * 1024)).toFixed(2)} GB
                              </span>
                            </div>
                          </div>
                        </>
                      )}
                    </>
                  );
                })()}
              </>
            )}
          </div>
        </div>
      );
    }
    return null;
  };

  return (
    <div
      style={{
        minHeight: '300px',
        background: 'var(--bg-secondary)',
        borderRadius: '8px',
        padding: '1rem',
        border: '1px solid var(--border-primary)',
      }}>
      <div
        style={{
          display: 'flex',
          justifyContent: 'space-between',
          alignItems: 'center',
          marginBottom: '1rem',
          flexWrap: 'wrap',
          gap: '0.75rem',
        }}>
        <div
          style={{
            display: 'flex',
            alignItems: 'center',
            gap: '0.75rem',
            flexWrap: 'wrap',
          }}>
          <h4
            style={{
              fontSize: '0.75rem',
              fontWeight: 600,
              color: 'var(--text-tertiary)',
              textTransform: 'uppercase',
              letterSpacing: '0.05em',
              margin: 0,
            }}>
            {metricType} {isNetwork ? '(MB/s)' : '(%)'}
          </h4>
        </div>
        {currentValue !== null && (
          <div
            style={{
              display: 'flex',
              alignItems: 'center',
              gap: '0.5rem',
              padding: '0.375rem 0.75rem',
              background: isWarning
                ? 'var(--status-error-bg)'
                : currentValue > 70
                ? 'var(--status-warning-bg)'
                : 'var(--status-success-bg)',
              border: `1px solid ${isWarning ? 'var(--status-error-border)' : currentValue > 70 ? 'var(--status-warning-border)' : 'var(--status-success-border)'}`,
              borderRadius: '6px',
            }}>
            <span
              style={{
                fontSize: '1.125rem',
                fontWeight: 700,
                color: color,
                fontFamily: 'monospace',
              }}>
              {isNetwork 
                ? `${currentValue.toFixed(2)} MB/s`
                : `${currentValue.toFixed(1)}%`}
            </span>
            {isWarning && (
              <HiExclamationTriangle
                style={{
                  fontSize: '0.875rem',
                  color: color,
                }}
              />
            )}
          </div>
        )}
      </div>
      {chartData.length > 0 ? (
        <div style={{ position: 'relative' }}>
          <div
            style={{
              fontSize: '0.6875rem',
              color: 'var(--text-tertiary)',
              marginBottom: '0.5rem',
              textAlign: 'right',
              fontFamily: 'monospace',
            }}>
            {t('metrics_view.agent_chart.history_points', { count: chartData.length })}
          </div>
          <div
            style={{
              width: '100%',
              height: '280px',
              background: 'var(--bg-tertiary)',
              borderRadius: '8px',
              border: '1px solid var(--border-primary)',
              padding: '8px',
            }}>
            <ResponsiveContainer width='100%' height='100%'>
              <AreaChart
                data={chartData}
                margin={{ top: 10, right: 15, left: 5, bottom: 10 }}>
                <defs>
                  <linearGradient
                    id={`gradient-${service.id}`}
                    x1='0'
                    y1='0'
                    x2='0'
                    y2='1'>
                    <stop offset='0%' stopColor={color} stopOpacity={0.3} />
                    <stop offset='100%' stopColor={color} stopOpacity={0.05} />
                  </linearGradient>
                </defs>
                <CartesianGrid
                  strokeDasharray='3 3'
                  stroke={chartColors.grid}
                  vertical={false}
                  strokeWidth={1}
                />
                <XAxis
                  dataKey='time'
                  stroke={chartColors.axis}
                  fontSize={11}
                  tick={{ fill: chartColors.text, fontWeight: 500 }}
                  axisLine={{
                    stroke: chartColors.grid,
                    strokeWidth: 1,
                  }}
                  tickLine={false}
                  interval={
                    chartData.length > 20 ? Math.floor(chartData.length / 8) : 0
                  }
                  angle={chartData.length > 10 ? -45 : 0}
                  textAnchor={chartData.length > 10 ? 'end' : 'middle'}
                  height={chartData.length > 10 ? 60 : 30}
                />
                <YAxis
                  domain={yAxisDomain}
                  stroke={chartColors.axis}
                  fontSize={11}
                  tick={{ fill: chartColors.text, fontWeight: 500 }}
                  axisLine={{
                    stroke: chartColors.grid,
                    strokeWidth: 1,
                  }}
                  tickLine={false}
                  label={{
                    value: yAxisLabel,
                    angle: -90,
                    position: 'insideLeft',
                    fill: chartColors.text,
                    fontSize: 10,
                    fontWeight: 600,
                  }}
                />
                <Tooltip
                  content={renderCustomTooltip}
                  cursor={{
                    stroke: color,
                    strokeWidth: 2,
                    strokeDasharray: '5 5',
                    strokeOpacity: 0.6,
                  }}
                />
                <Area
                  type='monotone'
                  dataKey='value'
                  stroke={color}
                  strokeWidth={2.5}
                  fill={`url(#gradient-${service.id})`}
                  dot={false}
                  activeDot={{
                    r: 5,
                    fill: color,
                    stroke: 'var(--bg-primary)',
                    strokeWidth: 2,
                  }}
                  name={metricType}
                  animationDuration={300}
                />
                {!isNetwork && threshold !== null && (
                  <Line
                    type='monotone'
                    dataKey={() => threshold}
                    stroke='#EF4444'
                    strokeWidth={1.5}
                    strokeDasharray='6 4'
                    dot={false}
                    name={t('metrics_view.agent_chart.threshold')}
                    legendType='none'
                    strokeOpacity={0.7}
                  />
                )}
              </AreaChart>
            </ResponsiveContainer>
          </div>
        </div>
      ) : (
        <div
          style={{
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            height: '280px',
            color: 'var(--text-tertiary)',
            fontSize: '0.875rem',
          }}>
          {metrics.length === 0
            ? t('metrics_view.agent_chart.waiting_data')
            : t('metrics_view.agent_chart.invalid_format', { count: metrics.length })}
        </div>
      )}
    </div>
  );
}
