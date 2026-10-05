import type { TFunction } from 'i18next';

import type { DashboardSeriesQuery, MetricSeries, SeriesExpressionQuery } from '../api';
import { apiErrorMessage } from '../utils/apiError';
import { seriesLabel } from './metricSeriesColors';

// Pure helpers of the series query builder, kept apart so they test without the API client.

export function emptyQuery(ref: string, metric = ''): SeriesExpressionQuery {
  return { ref, metric, filters: [], aggregation: 'avg' };
}

/** Widgets saved before expressions carry a single query; it becomes $A. */
export function toExpressionQueries(series: DashboardSeriesQuery): SeriesExpressionQuery[] {
  if (series.queries?.length) return series.queries;
  return [
    {
      ref: 'A',
      metric: series.metric,
      filters: series.filters ?? [],
      group_by: series.group_by,
      aggregation: series.aggregation ?? 'avg',
    },
  ];
}

/** Drops half-filled filters and queries without a metric before sending. */
export function readyQueries(queries: SeriesExpressionQuery[]): SeriesExpressionQuery[] {
  return queries
    .filter((q) => q.metric)
    .map((q) => ({
      ...q,
      filters: (q.filters ?? []).filter((f) => f.key && f.value),
      group_by: q.group_by || undefined,
    }));
}

/** True when the expression names a query that has no metric left to query. */
export function expressionOrphaned(
  queries: SeriesExpressionQuery[],
  expression: string,
): boolean {
  return queries.some((q) => !q.metric && expression.includes(`$${q.ref}`));
}

/** Prefixes the query ref when several raw queries share one chart. */
export function seriesDisplayName(series: MetricSeries, withRef: boolean): string {
  const label = seriesLabel(series.labels, series.metric_name);
  return withRef && series.query ? `$${series.query} ${label}` : label;
}

/** A 400 names the mistake in the expression; anything else is a plain failure. */
export function seriesErrorMessage(err: unknown, t: TFunction): string {
  const status = (err as { response?: { status?: number } }).response?.status;
  const reason = status === 400 ? apiErrorMessage(err, '') : '';
  return reason
    ? t('metrics_explorer.custom.invalid_query', { reason })
    : t('metrics_explorer.custom.query_failed');
}
