import { Fragment, useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  HiOutlineSignal,
  HiOutlineMagnifyingGlass,
  HiOutlineChevronLeft,
  HiOutlineChevronRight,
  HiOutlineCalendarDays,
} from 'react-icons/hi2';
import { keepPreviousData, useQuery } from '@tanstack/react-query';
import { useOrgApi, useOrgQueryScope } from '../hooks/useOrgApi';
import { useUrlState, pageFromUrl, pageToUrl } from '../hooks/useUrlState';
import { useQueryRefresh } from '../hooks/useQueryRefresh';
import { RefreshControl } from '../components/RefreshControl';
import type { TraceSpan } from '../api';
import { useDateRange } from '../contexts/DateRangeContext';
import { useDateRangeBounds, useDateRangeKey } from '../hooks/useDateRangeBounds';
import { DateRangePicker } from '../components/DateRangePicker';
import './TracesView.css';

function TracesView() {
  const { t } = useTranslation();
  const orgApi = useOrgApi();
  const { dateRange, setDateRange, formatDateRangeLabel } = useDateRange();
  const getDateBounds = useDateRangeBounds();
  const dateRangeKey = useDateRangeKey();
  const scope = useOrgQueryScope();
  // The search comes from the URL, so other views can deep-link (e.g. an
  // error's "View trace" button passes ?q=<trace_id>) and a search run here is
  // shareable in turn.
  const [urlState, setUrlState] = useUrlState({ q: '', page: '1' });
  const query = urlState.q;
  const page = pageFromUrl(urlState.page);
  const setPage = (next: number) => setUrlState({ page: pageToUrl(next) });
  // A submitted search invalidates the page number.
  const runSearch = (next: string) => setUrlState({ q: next, page: '1' });
  const [searchInput, setSearchInput] = useState(query);
  const [calendarOpen, setCalendarOpen] = useState(false);
  const pageSize = 50;
  const [expandedTrace, setExpandedTrace] = useState<string | null>(null);

  // The expanded span is a detail row: refreshing under it is what pausing is for.
  const { refetchInterval, buildControl } = useQueryRefresh('traces', 0, expandedTrace !== null);

  const tracesQueryKey = [scope, 'traces', 'search', query, dateRangeKey, page];
  const tracesQuery = useQuery({
    queryKey: tracesQueryKey,
    queryFn: async () => {
      const { startIso: start, endIso: end } = getDateBounds();
      const res = await orgApi.traces.search({
        q: query || undefined,
        start,
        end,
        from: page * pageSize,
        size: pageSize,
      });
      return { hits: (res.data.hits as TraceSpan[]) || [], total: res.data.total || 0 };
    },
    // The current page stays on screen while the next one loads.
    placeholderData: keepPreviousData,
    refetchInterval,
  });

  const traces = tracesQuery.data?.hits ?? [];
  const total = tracesQuery.data?.total ?? 0;

  const autoRefresh = buildControl({
    query: tracesQuery,
    queryKey: tracesQueryKey,
    prefix: [scope, 'traces'],
  });

  const handleSearch = (e: React.FormEvent) => {
    e.preventDefault();
    runSearch(searchInput);
  };

  const getStatusClass = (statusCode: string) => {
    if (statusCode === 'ERROR' || statusCode === '2') return 'trace-status-error';
    if (statusCode === 'OK' || statusCode === '1') return 'trace-status-ok';
    return 'trace-status-unset';
  };

  const getSpanKindLabel = (kind: string) => {
    const map: Record<string, string> = {
      'SPAN_KIND_SERVER': 'Server',
      'SPAN_KIND_CLIENT': 'Client',
      'SPAN_KIND_INTERNAL': 'Internal',
      'SPAN_KIND_PRODUCER': 'Producer',
      'SPAN_KIND_CONSUMER': 'Consumer',
    };
    return map[kind] || kind;
  };

  const totalPages = Math.ceil(total / pageSize);

  return (
    <div className="traces-view">
      <div className="traces-header">
        <div>
          <h1><HiOutlineSignal /> {t('traces_view.title')}</h1>
          <p className="subtitle">{t('traces_view.subtitle')}</p>
        </div>
        <RefreshControl control={autoRefresh} />
      </div>

      {/* Search & Filters */}
      <div className="traces-filters">
        <form onSubmit={handleSearch} className="traces-search-form">
          <HiOutlineMagnifyingGlass />
          <input
            type="text"
            placeholder={t('traces_view.search_placeholder')}
            value={searchInput}
            onChange={e => setSearchInput(e.target.value)}
          />
          <button type="submit" className="btn-search">{t('traces_view.search_button')}</button>
        </form>
        <div className="traces-filter-controls">
          <button
            className="date-range-trigger"
            onClick={() => setCalendarOpen(true)}
          >
            <HiOutlineCalendarDays />
            {formatDateRangeLabel(dateRange, { withTime: true })}
          </button>
        </div>
      </div>

      <DateRangePicker
        isOpen={calendarOpen}
        value={dateRange}
        onChange={(range) => { setDateRange(range); setPage(0); }}
        onClose={() => setCalendarOpen(false)}
        showTime
      />

      <div className="traces-results-info">
        <span>{t('traces_view.results_count', { count: total })}</span>
      </div>

      {/* Traces table */}
      <div className="traces-table-card">
        {tracesQuery.isPending ? (
          <div className="traces-loading">{t('traces_view.loading')}</div>
        ) : traces.length === 0 ? (
          <div className="empty-state">
            <HiOutlineSignal className="empty-state-icon" />
            <div className="empty-state-title">{t('traces_view.empty_title')}</div>
            <div className="empty-state-description">{t('traces_view.empty_desc')}</div>
          </div>
        ) : (
          <table className="traces-table">
            <thead>
              <tr>
                <th style={{ width: '150px' }}>{t('traces_view.table.timestamp')}</th>
                <th style={{ width: '120px' }}>{t('traces_view.table.service')}</th>
                <th>{t('traces_view.table.operation')}</th>
                <th style={{ width: '80px' }}>{t('traces_view.table.kind')}</th>
                <th style={{ width: '80px' }}>{t('traces_view.table.status')}</th>
                <th style={{ width: '90px' }}>{t('traces_view.table.duration')}</th>
              </tr>
            </thead>
            <tbody>
              {traces.map((span) => (
                <Fragment key={span._id}>
                  <tr className="trace-row" onClick={() => setExpandedTrace(expandedTrace === span._id ? null : span._id)}>
                    <td className="trace-timestamp">{new Date(span['@timestamp'] || span.start_time).toLocaleString()}</td>
                    <td className="trace-service">{span.service_name || '—'}</td>
                    <td className="trace-operation">{span.operation_name || '—'}</td>
                    <td><span className="trace-kind-badge">{getSpanKindLabel(span.span_kind)}</span></td>
                    <td><span className={`trace-status-badge ${getStatusClass(span.status_code)}`}>{span.status_code || 'UNSET'}</span></td>
                    <td className="trace-duration">{span.duration_ms != null ? `${span.duration_ms.toFixed(1)}ms` : '—'}</td>
                  </tr>
                  {expandedTrace === span._id && (
                    <tr className="trace-detail-row">
                      <td colSpan={6}>
                        <div className="trace-detail">
                          <div className="trace-detail-grid">
                            <div><strong>Trace ID</strong><code>{span.trace_id}</code></div>
                            <div><strong>Span ID</strong><code>{span.span_id}</code></div>
                            {span.parent_span_id && <div><strong>Parent Span ID</strong><code>{span.parent_span_id}</code></div>}
                            {span.hostname && <div><strong>Hostname</strong>{span.hostname}</div>}
                            {span.status_message && <div><strong>Status Message</strong>{span.status_message}</div>}
                          </div>
                          {span.attributes && Object.keys(span.attributes).length > 0 && (
                            <div className="trace-attributes">
                              <strong>Attributes</strong>
                              <pre>{JSON.stringify(span.attributes, null, 2)}</pre>
                            </div>
                          )}
                          {/* Duration bar */}
                          <div className="trace-duration-bar">
                            <div className="trace-duration-fill" style={{ width: `${Math.min((span.duration_ms || 0) / 10, 100)}%` }} />
                            <span>{span.duration_ms?.toFixed(2)}ms</span>
                          </div>
                        </div>
                      </td>
                    </tr>
                  )}
                </Fragment>
              ))}
            </tbody>
          </table>
        )}
      </div>

      {totalPages > 1 && (
        <div className="traces-pagination">
          <button disabled={page === 0} onClick={() => setPage(page - 1)}><HiOutlineChevronLeft /> {t('traces_view.pagination.previous')}</button>
          <span>{t('traces_view.pagination.of', { page: page + 1, total: totalPages })}</span>
          <button disabled={page >= totalPages - 1} onClick={() => setPage(page + 1)}>{t('traces_view.pagination.next')} <HiOutlineChevronRight /></button>
        </div>
      )}
    </div>
  );
}

export default TracesView;
