import { describe, expect, it } from 'vitest';
import type { TFunction } from 'i18next';

import type { MetricSeries } from '../api';
import {
  expressionOrphaned,
  readyQueries,
  seriesDisplayName,
  seriesErrorMessage,
  toExpressionQueries,
} from './seriesExpression';

// Dashboards saved before expressions existed must keep drawing the same chart.
describe('toExpressionQueries', () => {
  it('reads a legacy single-query widget as $A, keeping its query', () => {
    const queries = toExpressionQueries({
      metric: 'http_requests_total',
      filters: [{ key: 'route', value: '/cart' }],
      group_by: 'route',
      aggregation: 'p95',
    });
    expect(queries).toEqual([
      {
        ref: 'A',
        metric: 'http_requests_total',
        filters: [{ key: 'route', value: '/cart' }],
        group_by: 'route',
        aggregation: 'p95',
      },
    ]);
  });

  // The legacy widget form defaulted the aggregation; the backend rejects an empty one.
  it('defaults a legacy widget without aggregation to avg', () => {
    expect(toExpressionQueries({ metric: 'm' })[0].aggregation).toBe('avg');
  });

  it('prefers the saved queries over the legacy fields', () => {
    const queries = [
      { ref: 'A', metric: 'errors', aggregation: 'rate' as const },
      { ref: 'B', metric: 'requests', aggregation: 'rate' as const },
    ];
    expect(toExpressionQueries({ metric: 'errors', aggregation: 'avg', queries })).toBe(queries);
  });
});

// A half-built query in the editor must not reach the backend: an empty metric
// is a 400, and an empty filter would match no series at all.
describe('readyQueries', () => {
  it('drops queries without a metric and half-filled filters', () => {
    const ready = readyQueries([
      {
        ref: 'A',
        metric: 'm',
        aggregation: 'avg',
        group_by: '',
        filters: [
          { key: 'route', value: '/a' },
          { key: 'route', value: '' },
          { key: '', value: '' },
        ],
      },
      { ref: 'B', metric: '', aggregation: 'avg' },
    ]);
    expect(ready).toEqual([
      { ref: 'A', metric: 'm', aggregation: 'avg', group_by: undefined, filters: [{ key: 'route', value: '/a' }] },
    ]);
  });
});

// A scope change can empty a query the expression names; sending it anyway is a
// 400 unknown ref, so the expression goes with the metric.
describe('expressionOrphaned', () => {
  const queries = [
    { ref: 'A', metric: 'errors', aggregation: 'avg' as const },
    { ref: 'B', metric: '', aggregation: 'avg' as const },
  ];

  it('reports an expression naming a query left without a metric', () => {
    expect(expressionOrphaned(queries, '$A / $B')).toBe(true);
  });

  it('leaves an expression that only names queries still holding a metric', () => {
    expect(expressionOrphaned(queries, '$A * 100')).toBe(false);
    expect(expressionOrphaned(queries, '')).toBe(false);
  });
});

describe('seriesDisplayName', () => {
  const series: MetricSeries = { metric_name: 'm', labels: { route: '/a' }, points: [], query: 'B' };

  // Two raw queries on one chart can carry identical labels; the ref is what tells them apart.
  it('prefixes the query ref when several raw queries share a chart', () => {
    expect(seriesDisplayName(series, true)).toBe('$B route=/a');
  });

  it('shows labels alone otherwise, or the metric name when there are none', () => {
    expect(seriesDisplayName(series, false)).toBe('route=/a');
    expect(seriesDisplayName({ metric_name: '$A / $B', labels: {}, points: [] }, true)).toBe('$A / $B');
  });
});

// A typo in the expression must say what is wrong; a server failure must not
// leak its status text as if it were the user's mistake.
describe('seriesErrorMessage', () => {
  const t = ((key: string, opts?: { reason?: string }) =>
    opts?.reason ? `${key}: ${opts.reason}` : key) as unknown as TFunction;

  it('names the reason of a 400', () => {
    const err = { response: { status: 400, data: { error: 'invalid expression' } } };
    expect(seriesErrorMessage(err, t)).toBe('metrics_explorer.custom.invalid_query: invalid expression');
  });

  it('falls back to the generic failure otherwise', () => {
    expect(seriesErrorMessage({ response: { status: 500, data: { error: 'Internal Server Error' } } }, t)).toBe(
      'metrics_explorer.custom.query_failed',
    );
    expect(seriesErrorMessage(new Error('Network Error'), t)).toBe('metrics_explorer.custom.query_failed');
  });
});
