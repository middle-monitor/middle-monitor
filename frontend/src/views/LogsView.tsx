import { Fragment, useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  HiOutlineDocumentText,
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
import type { LogEntry } from '../api';
import { LogSearchInput } from '../components/LogSearchInput';
import { useDateRange } from '../contexts/DateRangeContext';
import { useDateRangeBounds, useDateRangeKey } from '../hooks/useDateRangeBounds';
import { DateRangePicker } from '../components/DateRangePicker';
import './LogsView.css';

const SEVERITIES = ['', 'DEBUG', 'INFO', 'WARN', 'ERROR', 'FATAL'];

function LogsView() {
  const { t } = useTranslation();
  const orgApi = useOrgApi();
  const scope = useOrgQueryScope();
  const { dateRange, setDateRange, formatDateRangeLabel } = useDateRange();
  const getDateBounds = useDateRangeBounds();
  const dateRangeKey = useDateRangeKey();
  const [urlState, setUrlState] = useUrlState({ q: '', severity: '', page: '1' });
  const query = urlState.q;
  const severity = urlState.severity;
  const page = pageFromUrl(urlState.page);
  const setPage = (next: number) => setUrlState({ page: pageToUrl(next) });
  // A submitted search or a severity change invalidates the page number.
  const runSearch = (next: string) => setUrlState({ q: next, page: '1' });
  // The box is seeded from the URL so a refresh or a shared link shows the
  // search that produced the list.
  const [searchInput, setSearchInput] = useState(query);
  const [calendarOpen, setCalendarOpen] = useState(false);
  const pageSize = 50;
  const [expandedLog, setExpandedLog] = useState<string | null>(null);

  // The expanded log is a detail row: refreshing under it is what pausing is for.
  const { refetchInterval, buildControl } = useQueryRefresh('logs', 0, expandedLog !== null);

  const logsQueryKey = [scope, 'logs', 'search', query, severity, dateRangeKey, page];
  const logsQuery = useQuery({
    queryKey: logsQueryKey,
    queryFn: async () => {
      const { startIso: start, endIso: end } = getDateBounds();
      const res = await orgApi.logs.search({
        q: query || undefined,
        severity: severity || undefined,
        start,
        end,
        from: page * pageSize,
        size: pageSize,
      });
      return { hits: (res.data.hits as LogEntry[]) || [], total: res.data.total || 0 };
    },
    // The current page stays on screen while the next one loads.
    placeholderData: keepPreviousData,
    refetchInterval,
  });

  const logs = logsQuery.data?.hits ?? [];
  const total = logsQuery.data?.total ?? 0;

  const autoRefresh = buildControl({
    query: logsQuery,
    queryKey: logsQueryKey,
    prefix: [scope, 'logs'],
  });

  const handleSearch = (e: React.FormEvent) => {
    e.preventDefault();
    runSearch(searchInput);
  };

  const getSeverityClass = (sev: string) => {
    const s = sev?.toUpperCase();
    if (s === 'ERROR' || s === 'FATAL') return 'severity-error';
    if (s === 'WARN' || s === 'WARNING') return 'severity-warn';
    if (s === 'INFO') return 'severity-info';
    return 'severity-debug';
  };

  const totalPages = Math.ceil(total / pageSize);

  return (
    <div className="logs-view">
      <header className="view-header">
        <div>
          <h1><HiOutlineDocumentText /> {t('logs_view.title')}</h1>
          <p className="subtitle">{t('logs_view.subtitle')}</p>
        </div>
        <RefreshControl control={autoRefresh} />
      </header>

      {/* Search & Filters */}
      <div className="logs-filters">
        <form onSubmit={handleSearch} className="logs-search-form">
          <HiOutlineMagnifyingGlass />
          <LogSearchInput
            value={searchInput}
            onChange={setSearchInput}
            onSubmit={() => runSearch(searchInput)}
            onFieldValues={async (field, prefix) => {
              const start = dateRange.start.toISOString();
              const end = dateRange.end.toISOString();
              const res = await orgApi.logs.fieldValues({ field, prefix, start, end, size: 20 });
              return res.data.values || [];
            }}
            placeholder={t('logs_view.search_placeholder')}
          />
          <button type="submit" className="btn-search">{t('logs_view.search_button')}</button>
        </form>
        <div className="logs-filter-controls">
          <select value={severity} onChange={e => setUrlState({ severity: e.target.value, page: '1' })}>
            <option value="">{t('logs_view.all_severities')}</option>
            {SEVERITIES.filter(Boolean).map(s => <option key={s} value={s}>{s}</option>)}
          </select>
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

      {/* Results count */}
      <div className="logs-results-info">
        <span>{t('logs_view.results_count', { count: total })}</span>
      </div>

      {/* Logs table */}
      <div className="logs-table-card">
        {logsQuery.isPending ? (
          <div className="logs-loading">{t('logs_view.loading')}</div>
        ) : logs.length === 0 ? (
          <div className="empty-state">
            <HiOutlineDocumentText className="empty-state-icon" />
            <div className="empty-state-title">{t('logs_view.empty_title')}</div>
            <div className="empty-state-description">{t('logs_view.empty_desc')}</div>
          </div>
        ) : (
          <table className="logs-table">
            <thead>
              <tr>
                <th style={{ width: '160px' }}>{t('logs_view.table.timestamp')}</th>
                <th style={{ width: '80px' }}>{t('logs_view.table.severity')}</th>
                <th style={{ width: '120px' }}>{t('logs_view.table.service')}</th>
                <th>{t('logs_view.table.message')}</th>
              </tr>
            </thead>
            <tbody>
              {logs.map((log) => (
                <Fragment key={log._id}>
                  <tr className={`log-row ${getSeverityClass(log.severity_text || log.severity)}`} onClick={() => setExpandedLog(expandedLog === log._id ? null : log._id)}>
                    <td className="log-timestamp">{new Date(log['@timestamp']).toLocaleString()}</td>
                    <td><span className={`log-severity ${getSeverityClass(log.severity_text || log.severity)}`}>{log.severity_text || log.severity || '—'}</span></td>
                    <td className="log-service">{log.service_name || '—'}</td>
                    <td className="log-body">{log.body || '—'}</td>
                  </tr>
                  {expandedLog === log._id && (
                    <tr className="log-detail-row">
                      <td colSpan={4}>
                        <div className="log-detail">
                          {log.hostname && <div><strong>Hostname:</strong> {log.hostname}</div>}
                          {log.trace_id && <div><strong>Trace ID:</strong> <code>{log.trace_id}</code></div>}
                          {log.span_id && <div><strong>Span ID:</strong> <code>{log.span_id}</code></div>}
                          {log.attributes && Object.keys(log.attributes).length > 0 && (
                            <div><strong>Attributes:</strong> <pre>{JSON.stringify(log.attributes, null, 2)}</pre></div>
                          )}
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

      {/* Pagination */}
      {totalPages > 1 && (
        <div className="logs-pagination">
          <button disabled={page === 0} onClick={() => setPage(page - 1)}><HiOutlineChevronLeft /> {t('logs_view.pagination.previous')}</button>
          <span>{t('logs_view.pagination.of', { page: page + 1, total: totalPages })}</span>
          <button disabled={page >= totalPages - 1} onClick={() => setPage(page + 1)}>{t('logs_view.pagination.next')} <HiOutlineChevronRight /></button>
        </div>
      )}
    </div>
  );
}

export default LogsView;
