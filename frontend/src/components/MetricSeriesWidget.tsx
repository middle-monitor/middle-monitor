import { useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  CartesianGrid,
  Line,
  LineChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts';
import { useOrgApi } from '../hooks/useOrgApi';
import { useTheme } from '../contexts/ThemeContext';
import type { DashboardSeriesQuery, MetricSeries } from '../api';
import { MAX_SERIES, seriesColors } from './metricSeriesColors';
import { readyQueries, seriesDisplayName, toExpressionQueries } from './seriesExpression';
import './MetricSeriesWidget.css';

interface MetricSeriesWidgetProps {
  query: DashboardSeriesQuery;
  start: string;
  end: string;
}

/** A dashboard widget drawing one custom metric query. It fetches its own data:
 *  each widget carries a different metric, so they cannot share one request. */
export function MetricSeriesWidget({ query, start, end }: MetricSeriesWidgetProps) {
  const { t } = useTranslation();
  const orgApi = useOrgApi();
  const { isDark } = useTheme();

  const [series, setSeries] = useState<MetricSeries[]>([]);
  const [loading, setLoading] = useState(true);
  const [failed, setFailed] = useState(false);

  const queries = useMemo(() => readyQueries(toExpressionQueries(query)), [query]);
  const queriesKey = JSON.stringify(queries);
  const expression = query.expression ?? '';

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setFailed(false);
    orgApi.metricSeries
      .expression({ queries, expression: expression || undefined }, { start, end })
      .then((res) => {
        if (cancelled) return;
        setSeries(res.data.series ?? []);
      })
      .catch(() => {
        if (cancelled) return;
        setSeries([]);
        setFailed(true);
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
    // queriesKey stands in for the query array: a new array identity each render
    // would re-fetch forever.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [orgApi, queriesKey, expression, start, end]);

  const shown = series.slice(0, MAX_SERIES);
  const colors = seriesColors(isDark);

  const chartData = useMemo(() => {
    const rows = new Map<string, Record<string, number | string | null>>();
    shown.forEach((s, index) => {
      for (const point of s.points) {
        const row = rows.get(point.timestamp) ?? { timestamp: point.timestamp };
        row[`s${index}`] = point.value;
        rows.set(point.timestamp, row);
      }
    });
    return [...rows.values()].sort((a, b) =>
      String(a.timestamp) < String(b.timestamp) ? -1 : 1,
    );
  }, [shown]);

  if (loading) {
    return <div className="metric-series-widget-message">{t('common.loading')}</div>;
  }
  if (failed) {
    return (
      <div className="metric-series-widget-message">
        {t('metrics_explorer.custom.query_failed')}
      </div>
    );
  }
  if (!chartData.length) {
    return <div className="metric-series-widget-message">{t('dashboards.widget.no_data')}</div>;
  }

  const axisColor = isDark ? '#9CA3AF' : '#6B7280';
  const gridColor = isDark ? 'rgba(75, 85, 99, 0.3)' : '#E5E7EB';

  return (
    <div className="metric-series-widget">
      <ResponsiveContainer width="100%" height="100%">
        <LineChart data={chartData} margin={{ top: 4, right: 8, bottom: 0, left: 0 }}>
          <CartesianGrid stroke={gridColor} strokeDasharray="4" vertical={false} />
          <XAxis
            dataKey="timestamp"
            tick={{ fill: axisColor, fontSize: 10 }}
            tickFormatter={(value: string) => new Date(value).toLocaleTimeString()}
            minTickGap={48}
            stroke={gridColor}
          />
          <YAxis tick={{ fill: axisColor, fontSize: 10 }} stroke={gridColor} width={40} />
          <Tooltip
            contentStyle={{
              background: 'var(--surface-primary)',
              border: '1px solid var(--border-primary)',
              borderRadius: 6,
              color: 'var(--text-primary)',
              fontSize: 12,
            }}
            labelFormatter={(value: string) => new Date(value).toLocaleString()}
          />
          {shown.map((s, index) => (
            <Line
              key={index}
              type="monotone"
              dataKey={`s${index}`}
              name={seriesDisplayName(s, queries.length > 1 && !expression)}
              stroke={colors[index]}
              strokeWidth={2}
              dot={false}
              connectNulls={false}
            />
          ))}
        </LineChart>
      </ResponsiveContainer>

      {/* Identity never rests on colour alone, so the legend ships even here. */}
      {shown.length > 1 && (
        <ul className="metric-series-widget-legend">
          {shown.map((s, index) => (
            <li key={index}>
              <span
                className="metric-series-widget-swatch"
                style={{ background: colors[index] }}
                aria-hidden="true"
              />
              {seriesDisplayName(s, queries.length > 1 && !expression)}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

export default MetricSeriesWidget;
