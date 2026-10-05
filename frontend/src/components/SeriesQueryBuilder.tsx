import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { HiOutlinePlus, HiOutlineXMark, HiOutlineCalculator } from 'react-icons/hi2';
import { useOrgApi } from '../hooks/useOrgApi';
import type { ExpressionAggregation, SeriesExpressionQuery } from '../api';
import { emptyQuery } from './seriesExpression';
import './CustomMetricsExplorer.css';

const EXPRESSION_AGGREGATIONS: ExpressionAggregation[] = [
  'avg',
  'min',
  'max',
  'sum',
  'count',
  'rate',
  'p50',
  'p75',
  'p90',
  'p95',
  'p99',
];

// The backend accepts up to five queries, addressed $A to $E.
const MAX_QUERIES = 5;

export type ScopeParams = { host_id?: number; host_group_id?: number };

function nextRef(queries: SeriesExpressionQuery[]): string {
  const used = new Set(queries.map((q) => q.ref));
  return 'ABCDE'.split('').find((ref) => !used.has(ref)) ?? '';
}

interface SeriesQueryBuilderProps {
  queries: SeriesExpressionQuery[];
  expression: string;
  onQueriesChange: (queries: SeriesExpressionQuery[]) => void;
  onExpressionChange: (expression: string) => void;
  names: string[];
  range: { start: string; end: string };
  scopeParams?: ScopeParams;
}

export function SeriesQueryBuilder({
  queries,
  expression,
  onQueriesChange,
  onExpressionChange,
  names,
  range,
  scopeParams,
}: SeriesQueryBuilderProps) {
  const { t } = useTranslation();
  // Applied on Enter or blur: a query per keystroke would chart half-typed math.
  const [draft, setDraft] = useState(expression);
  useEffect(() => setDraft(expression), [expression]);

  const update = (index: number, next: SeriesExpressionQuery) =>
    onQueriesChange(queries.map((q, i) => (i === index ? next : q)));

  const remove = (index: number) => {
    const removed = queries[index].ref;
    onQueriesChange(queries.filter((_, i) => i !== index));
    if (expression.includes(`$${removed}`)) onExpressionChange('');
  };

  return (
    <div className="series-query-builder">
      {queries.map((query, index) => (
        <QueryEditor
          key={query.ref}
          query={query}
          names={names}
          range={range}
          scopeParams={scopeParams}
          onChange={(next) => update(index, next)}
          onRemove={queries.length > 1 ? () => remove(index) : undefined}
        />
      ))}

      <div className="series-query-builder-footer">
        <button
          type="button"
          className="custom-metrics-add-filter"
          onClick={() => onQueriesChange([...queries, emptyQuery(nextRef(queries))])}
          disabled={queries.length >= MAX_QUERIES}
        >
          <HiOutlinePlus /> {t('metrics_explorer.custom.add_query')}
        </button>

        <label className="series-expression">
          <HiOutlineCalculator aria-hidden="true" />
          <input
            type="text"
            value={draft}
            maxLength={256}
            placeholder={t('metrics_explorer.custom.expression_placeholder')}
            aria-label={t('metrics_explorer.custom.expression')}
            onChange={(e) => setDraft(e.target.value)}
            onBlur={() => draft !== expression && onExpressionChange(draft.trim())}
            onKeyDown={(e) => {
              if (e.key === 'Enter') onExpressionChange(draft.trim());
            }}
          />
        </label>
      </div>
      <p className="series-expression-hint">{t('metrics_explorer.custom.expression_hint')}</p>
    </div>
  );
}

function QueryEditor({
  query,
  names,
  range,
  scopeParams,
  onChange,
  onRemove,
}: {
  query: SeriesExpressionQuery;
  names: string[];
  range: { start: string; end: string };
  scopeParams?: ScopeParams;
  onChange: (next: SeriesExpressionQuery) => void;
  onRemove?: () => void;
}) {
  const { t } = useTranslation();
  const orgApi = useOrgApi();
  const [labelKeys, setLabelKeys] = useState<string[]>([]);
  const filters = query.filters ?? [];
  const scopeKey = JSON.stringify(scopeParams ?? {});

  useEffect(() => {
    if (!query.metric) {
      setLabelKeys([]);
      return;
    }
    let cancelled = false;
    orgApi.metricSeries
      .labelKeys({ metric: query.metric, ...range, ...scopeParams })
      .then((res) => {
        if (!cancelled) setLabelKeys(res.data.keys ?? []);
      })
      .catch(() => {
        if (!cancelled) setLabelKeys([]);
      });
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [orgApi, query.metric, range, scopeKey]);

  return (
    <div className="series-query">
      <div className="custom-metrics-controls">
        <span className="series-query-ref">${query.ref}</span>

        <label>
          <span>{t('metrics_explorer.custom.metric')}</span>
          <select
            value={query.metric}
            // Labels differ between metrics, so the label-driven controls reset.
            onChange={(e) => onChange({ ...query, metric: e.target.value, filters: [], group_by: undefined })}
          >
            <option value="">{t('metrics_explorer.custom.no_metric')}</option>
            {names.map((name) => (
              <option key={name} value={name}>
                {name}
              </option>
            ))}
          </select>
        </label>

        <label>
          <span>{t('metrics_explorer.custom.aggregation')}</span>
          <select
            value={query.aggregation}
            onChange={(e) => onChange({ ...query, aggregation: e.target.value as ExpressionAggregation })}
          >
            {EXPRESSION_AGGREGATIONS.map((a) => (
              <option key={a} value={a}>
                {a === 'rate' ? t('metrics_explorer.custom.rate') : a}
              </option>
            ))}
          </select>
        </label>

        <label>
          <span>{t('metrics_explorer.custom.group_by')}</span>
          <select
            value={query.group_by ?? ''}
            onChange={(e) => onChange({ ...query, group_by: e.target.value || undefined })}
            disabled={!query.metric}
          >
            <option value="">{t('metrics_explorer.custom.no_group_by')}</option>
            {labelKeys.map((key) => (
              <option key={key} value={key}>
                {key}
              </option>
            ))}
          </select>
        </label>

        {onRemove && (
          <button
            type="button"
            className="series-query-remove"
            onClick={onRemove}
            aria-label={t('metrics_explorer.custom.remove_query')}
          >
            <HiOutlineXMark />
          </button>
        )}
      </div>

      <div className="custom-metrics-filters">
        {filters.map((filter, index) => (
          <LabelFilterRow
            key={index}
            filter={filter}
            labelKeys={labelKeys}
            metric={query.metric}
            range={range}
            onChange={(next) =>
              onChange({ ...query, filters: filters.map((f, i) => (i === index ? next : f)) })
            }
            onRemove={() => onChange({ ...query, filters: filters.filter((_, i) => i !== index) })}
          />
        ))}
        <button
          type="button"
          className="custom-metrics-add-filter"
          onClick={() => onChange({ ...query, filters: [...filters, { key: '', value: '' }] })}
          disabled={!query.metric || !labelKeys.length}
        >
          <HiOutlinePlus /> {t('metrics_explorer.custom.add_filter')}
        </button>
      </div>
    </div>
  );
}

function LabelFilterRow({
  filter,
  labelKeys,
  metric,
  range,
  onChange,
  onRemove,
}: {
  filter: { key: string; value: string };
  labelKeys: string[];
  metric: string;
  range: { start: string; end: string };
  onChange: (next: { key: string; value: string }) => void;
  onRemove: () => void;
}) {
  const { t } = useTranslation();
  const orgApi = useOrgApi();
  const [values, setValues] = useState<string[]>([]);

  useEffect(() => {
    if (!filter.key || !metric) {
      setValues([]);
      return;
    }
    let cancelled = false;
    orgApi.metricSeries
      .labelValues({ key: filter.key, metric, ...range, size: 200 })
      .then((res) => {
        if (!cancelled) setValues(res.data.values ?? []);
      })
      .catch(() => {
        if (!cancelled) setValues([]);
      });
    return () => {
      cancelled = true;
    };
  }, [orgApi, filter.key, metric, range]);

  return (
    <div className="custom-metrics-filter">
      <select
        value={filter.key}
        onChange={(e) => onChange({ key: e.target.value, value: '' })}
        aria-label={t('metrics_explorer.custom.filter_key')}
      >
        <option value="">{t('metrics_explorer.custom.filter_key')}</option>
        {labelKeys.map((key) => (
          <option key={key} value={key}>
            {key}
          </option>
        ))}
      </select>
      <select
        value={filter.value}
        onChange={(e) => onChange({ ...filter, value: e.target.value })}
        disabled={!filter.key}
        aria-label={t('metrics_explorer.custom.filter_value')}
      >
        <option value="">{t('metrics_explorer.custom.filter_value')}</option>
        {values.map((value) => (
          <option key={value} value={value}>
            {value}
          </option>
        ))}
      </select>
      <button
        type="button"
        onClick={onRemove}
        aria-label={t('metrics_explorer.custom.remove_filter')}
      >
        <HiOutlineXMark />
      </button>
    </div>
  );
}
