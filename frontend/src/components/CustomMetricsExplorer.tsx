import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  CartesianGrid,
  Legend,
  Line,
  LineChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts';
import { useOrgApi } from '../hooks/useOrgApi';
import { useTheme } from '../contexts/ThemeContext';
import { useDateRange } from '../contexts/DateRangeContext';
import type { Host, HostGroup, MetricSeries, SeriesExpressionQuery } from '../api';
import { MAX_SERIES, seriesColors } from './metricSeriesColors';
import { SeriesQueryBuilder } from './SeriesQueryBuilder';
import {
  emptyQuery,
  expressionOrphaned,
  readyQueries,
  seriesDisplayName,
  seriesErrorMessage,
} from './seriesExpression';
import './CustomMetricsExplorer.css';

export function CustomMetricsExplorer() {
  const { t } = useTranslation();
  const orgApi = useOrgApi();
  const { isDark } = useTheme();
  const { dateRange } = useDateRange();

  const [names, setNames] = useState<string[]>([]);
  const [queries, setQueries] = useState<SeriesExpressionQuery[]>([emptyQuery('A')]);
  const [expression, setExpression] = useState('');
  const [hosts, setHosts] = useState<Host[]>([]);
  const [hostGroups, setHostGroups] = useState<HostGroup[]>([]);
  // "host:12" or "group:3"; empty means the whole organization.
  const [scope, setScope] = useState('');
  const [series, setSeries] = useState<MetricSeries[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');

  const range = useMemo(
    () => ({ start: dateRange.start.toISOString(), end: dateRange.end.toISOString() }),
    [dateRange],
  );

  // Correlation scope: metrics of one host, or of every host in a group.
  const scopeParams = useMemo(() => {
    const [kind, id] = scope.split(':');
    if (kind === 'host' && id) return { host_id: Number(id) };
    if (kind === 'group' && id) return { host_group_id: Number(id) };
    return {};
  }, [scope]);
  const scopeKey = scope;
  // Read by the names effect, which must see the queries without refiring on them.
  const queriesRef = useRef(queries);
  queriesRef.current = queries;

  useEffect(() => {
    let cancelled = false;
    Promise.all([orgApi.hosts.list(), orgApi.hostGroups.list()])
      .then(([hostsRes, groupsRes]) => {
        if (cancelled) return;
        setHosts(hostsRes.data ?? []);
        setHostGroups(groupsRes.data ?? []);
      })
      .catch(() => {
        if (cancelled) return;
        setHosts([]);
        setHostGroups([]);
      });
    return () => {
      cancelled = true;
    };
  }, [orgApi]);

  useEffect(() => {
    let cancelled = false;
    orgApi.metricSeries
      .names({ ...range, ...scopeParams, size: 500 })
      .then((res) => {
        if (cancelled) return;
        const found = res.data.names ?? [];
        setNames(found);
        const current = queriesRef.current;
        // Only a metric the scope took away orphans the expression; a query still
        // being filled in has none yet and keeps its place in the expression.
        const dropped = current.filter((q) => q.metric && !found.includes(q.metric));
        setExpression((e) => (dropped.some((q) => e.includes(`$${q.ref}`)) ? '' : e));
        setQueries(
          current.map((q, index) => {
            // A refresh must not move the user off the metric they are reading.
            if (q.metric && found.includes(q.metric)) return q;
            // But narrowing the scope can drop it: falling through to the first
            // available beats leaving a blank chart with no explanation. Filters
            // and grouping belong to the metric and go with it; the aggregation
            // is the user's own choice, as on the metric select.
            return {
              ...emptyQuery(q.ref, index === 0 ? (found[0] ?? '') : ''),
              aggregation: q.aggregation,
            };
          }),
        );
      })
      .catch(() => {
        if (!cancelled) setNames([]);
      });
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [orgApi, range, scopeKey]);

  // An expression naming a query without a metric waits for it: sending it now
  // would only come back as a 400 unknown ref.
  const orphaned = expressionOrphaned(queries, expression);

  const ready = useMemo(() => readyQueries(queries), [queries]);
  const readyKey = JSON.stringify(ready);

  const runQuery = useCallback(async () => {
    // Until the named query has a metric, querying would reject on an unknown ref,
    // and that error would outlive the request.
    if (orphaned) return;
    if (!ready.length) {
      setSeries([]);
      return;
    }
    setLoading(true);
    setError('');
    try {
      const res = await orgApi.metricSeries.expression(
        { queries: ready, expression: expression || undefined },
        { ...range, ...scopeParams },
      );
      setSeries(res.data.series ?? []);
    } catch (err) {
      setSeries([]);
      setError(seriesErrorMessage(err, t));
    } finally {
      setLoading(false);
    }
    // readyKey stands in for the query array, rebuilt on every keystroke elsewhere.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [orgApi, orphaned, readyKey, expression, range, scopeKey, t]);

  useEffect(() => {
    runQuery();
  }, [runQuery]);

  const labelFor = (s: MetricSeries) => seriesDisplayName(s, ready.length > 1 && !expression);

  const shown = series.slice(0, MAX_SERIES);
  const hidden = series.length - shown.length;
  const colors = seriesColors(isDark);

  // Recharts wants one row per timestamp with a column per series.
  const chartData = useMemo(() => {
    const rows = new Map<string, Record<string, number | string | null>>();
    shown.forEach((s, index) => {
      const key = `s${index}`;
      for (const point of s.points) {
        const row = rows.get(point.timestamp) ?? { timestamp: point.timestamp };
        row[key] = point.value;
        rows.set(point.timestamp, row);
      }
    });
    return [...rows.values()].sort((a, b) =>
      String(a.timestamp) < String(b.timestamp) ? -1 : 1,
    );
  }, [shown]);

  // The backend fills the whole range with buckets, empty ones included, so rows
  // are never absent — only their values are null. Testing the row count would
  // draw a flat line where there is no data at all.
  const hasValues = useMemo(
    () => chartData.some((row) => shown.some((_, index) => row[`s${index}`] != null)),
    [chartData, shown],
  );

  const axisColor = isDark ? '#9CA3AF' : '#6B7280';
  const gridColor = isDark ? 'rgba(75, 85, 99, 0.3)' : '#E5E7EB';

  return (
    <div className="custom-metrics">
      <div className="custom-metrics-header">
        <h3>{t('metrics_explorer.custom.title')}</h3>
        <p className="subtitle">{t('metrics_explorer.custom.subtitle')}</p>
      </div>

      <div className="custom-metrics-controls">
        <label>
          <span>{t('metrics_explorer.custom.scope')}</span>
          <select value={scope} onChange={(e) => setScope(e.target.value)}>
            <option value="">{t('metrics_explorer.custom.scope_all')}</option>
            {hostGroups.length > 0 && (
              <optgroup label={t('metrics_explorer.custom.scope_groups')}>
                {hostGroups.map((group) => (
                  <option key={`group-${group.id}`} value={`group:${group.id}`}>
                    {group.display_name || group.name}
                  </option>
                ))}
              </optgroup>
            )}
            {hosts.length > 0 && (
              <optgroup label={t('metrics_explorer.custom.scope_hosts')}>
                {hosts.map((host) => (
                  <option key={`host-${host.id}`} value={`host:${host.id}`}>
                    {host.display_name || host.name}
                  </option>
                ))}
              </optgroup>
            )}
          </select>
        </label>
      </div>

      <SeriesQueryBuilder
        queries={queries}
        expression={expression}
        onQueriesChange={setQueries}
        onExpressionChange={setExpression}
        names={names}
        range={range}
        scopeParams={scopeParams}
      />

      <div className="custom-metrics-chart">
        {loading ? (
          <div className="custom-metrics-message">{t('common.loading')}</div>
        ) : error ? (
          <div className="custom-metrics-message">{error}</div>
        ) : !hasValues ? (
          <div className="custom-metrics-message">{t('metrics_explorer.custom.no_data')}</div>
        ) : (
          <ResponsiveContainer width="100%" height={280}>
            <LineChart data={chartData} margin={{ top: 8, right: 16, bottom: 0, left: 0 }}>
              <CartesianGrid stroke={gridColor} strokeDasharray="4" vertical={false} />
              <XAxis
                dataKey="timestamp"
                tick={{ fill: axisColor, fontSize: 11 }}
                tickFormatter={(value: string) => new Date(value).toLocaleTimeString()}
                minTickGap={40}
                stroke={gridColor}
              />
              <YAxis tick={{ fill: axisColor, fontSize: 11 }} stroke={gridColor} width={56} />
              <Tooltip
                contentStyle={{
                  background: 'var(--surface-primary)',
                  border: '1px solid var(--border-primary)',
                  borderRadius: 6,
                  color: 'var(--text-primary)',
                }}
                labelFormatter={(value: string) => new Date(value).toLocaleString()}
              />
              {shown.length > 1 && (
                <Legend
                  wrapperStyle={{ fontSize: 12 }}
                  // Recharts tints the label with the series colour; identity is
                  // carried by the swatch, the text stays in the text token.
                  formatter={(value: string) => (
                    <span style={{ color: 'var(--text-secondary)' }}>{value}</span>
                  )}
                />
              )}
              {shown.map((s, index) => (
                <Line
                  key={index}
                  type="monotone"
                  dataKey={`s${index}`}
                  name={labelFor(s)}
                  stroke={colors[index]}
                  strokeWidth={2}
                  dot={false}
                  // Gaps are real: a bucket with no datapoint must not be bridged.
                  connectNulls={false}
                />
              ))}
            </LineChart>
          </ResponsiveContainer>
        )}
      </div>

      {hidden > 0 && (
        <p className="custom-metrics-truncated">
          {t('metrics_explorer.custom.truncated', { count: hidden })}
        </p>
      )}

      {shown.length > 0 && <SeriesTable series={shown} colors={colors} labelFor={labelFor} />}
    </div>
  );
}

// The light-mode palette dips below 3:1 on three slots, so the values are also
// readable as text and identity never rests on colour alone.
function SeriesTable({
  series,
  colors,
  labelFor,
}: {
  series: MetricSeries[];
  colors: string[];
  labelFor: (s: MetricSeries) => string;
}) {
  const { t } = useTranslation();

  const summarise = (s: MetricSeries) => {
    const values = s.points.map((p) => p.value).filter((v): v is number => v != null);
    if (!values.length) return { last: null, min: null, max: null };
    return {
      last: values[values.length - 1],
      min: Math.min(...values),
      max: Math.max(...values),
    };
  };

  const format = (value: number | null) =>
    value == null ? '—' : Math.abs(value) >= 100 ? value.toFixed(0) : value.toFixed(2);

  return (
    <table className="custom-metrics-table">
      <thead>
        <tr>
          <th>{t('metrics_explorer.custom.table.series')}</th>
          <th>{t('metrics_explorer.custom.table.last')}</th>
          <th>{t('metrics_explorer.custom.table.min')}</th>
          <th>{t('metrics_explorer.custom.table.max')}</th>
        </tr>
      </thead>
      <tbody>
        {series.map((s, index) => {
          const stats = summarise(s);
          return (
            <tr key={index}>
              <td>
                <span
                  className="custom-metrics-swatch"
                  style={{ background: colors[index] }}
                  aria-hidden="true"
                />
                {labelFor(s)}
              </td>
              <td>{format(stats.last)}</td>
              <td>{format(stats.min)}</td>
              <td>{format(stats.max)}</td>
            </tr>
          );
        })}
      </tbody>
    </table>
  );
}

export default CustomMetricsExplorer;
