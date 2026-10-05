import { useState, useEffect, useMemo, useRef } from 'react';
import { useNavigate } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import type { TFunction } from 'i18next';
import {
  HiOutlineShieldCheck,
  HiOutlineInbox,
  HiOutlineBellAlert,
  HiOutlinePlus,
  HiOutlineExclamationTriangle,
  HiOutlineCheckCircle,
  HiOutlineClock,
  HiOutlineXMark,
  HiOutlineEye,
  HiOutlineMagnifyingGlass,
  HiOutlineFunnel,
} from 'react-icons/hi2';
import { keepPreviousData, useQuery } from '@tanstack/react-query';
import { useOrgApi, useOrgQueryScope } from '../hooks/useOrgApi';
import { useUrlState, pageFromUrl, pageToUrl } from '../hooks/useUrlState';
import { useQueryRefresh } from '../hooks/useQueryRefresh';
import { RefreshControl } from '../components/RefreshControl';
import { useAuth } from '../contexts/AuthContext';
import { useOrgPath } from '../hooks/useOrgPath';
import type { Incident, IncidentStats } from '../api';
import { Pagination } from '../components/Pagination';
import './IncidentsView.css';

const SEVERITIES = ['critical', 'warning', 'info'] as const;
const STATUSES = ['open', 'acknowledged', 'resolved'] as const;

// Module scope: a fresh object every render would re-run every memo that reads it.
const EMPTY_STATS: IncidentStats = { total: 0, open: 0, acknowledged: 0, resolved: 0, critical: 0, services: [] };

// Maps what the user can type → canonical incident field
const FILTER_ALIASES: Record<string, keyof typeof FIELD_VALUES_FALLBACK> = {
  host: 'service',
  service: 'service',
  svc: 'service',
  severity: 'severity',
  sev: 'severity',
  status: 'status',
  st: 'status',
};

const FILTER_KEYS = ['service:', 'severity:', 'status:', 'host:'];

const FIELD_VALUES_FALLBACK = {
  service: [] as string[],
  severity: [...SEVERITIES],
  status: [...STATUSES],
};

interface ParsedQuery {
  filters: Record<string, string>;
  text: string;
}

function parseSearch(raw: string): ParsedQuery {
  const filters: Record<string, string> = {};
  const remaining: string[] = [];
  for (const token of raw.trim().split(/\s+/)) {
    const m = token.match(/^([a-z]+):(.+)$/i);
    if (m) {
      const field = FILTER_ALIASES[m[1].toLowerCase()];
      if (field) { filters[field] = m[2].toLowerCase(); continue; }
    }
    if (token) remaining.push(token);
  }
  return { filters, text: remaining.join(' ') };
}

function timeAgo(dateStr: string, t: TFunction): string {
  const diff = Date.now() - new Date(dateStr).getTime();
  const m = Math.floor(diff / 60000);
  if (m < 1) return t('incidents.time.just_now');
  if (m < 60) return t('incidents.time.minutes_ago', { m });
  const h = Math.floor(m / 60);
  if (h < 24) return t('incidents.time.hours_ago', { h });
  return t('incidents.time.days_ago', { d: Math.floor(h / 24) });
}

// ─── Smart search autocomplete ────────────────────────────────────────────────

function buildSuggestions(
  input: string,
  fieldValues: Record<string, string[]>,
): string[] {
  const tokens = input.split(/\s+/);
  const last = tokens[tokens.length - 1] ?? '';

  // Still typing the key (no colon yet) → suggest matching keys
  if (!last.includes(':')) {
    return FILTER_KEYS.filter(k => k.startsWith(last.toLowerCase()) && k !== last.toLowerCase());
  }

  // Has colon → suggest values
  const colonIdx = last.indexOf(':');
  const typedKey = last.slice(0, colonIdx).toLowerCase();
  const typedVal = last.slice(colonIdx + 1).toLowerCase();
  const field = FILTER_ALIASES[typedKey];
  if (!field) return [];

  const values = fieldValues[field] ?? FIELD_VALUES_FALLBACK[field] ?? [];
  return values
    .filter(v => v.toLowerCase().startsWith(typedVal) && v.toLowerCase() !== typedVal)
    .slice(0, 8);
}

function applySuggestion(current: string, suggestion: string): string {
  const tokens = current.split(/\s+/);
  const last = tokens[tokens.length - 1] ?? '';

  // Completing a key prefix
  if (!last.includes(':')) {
    tokens[tokens.length - 1] = suggestion;
    return tokens.join(' ');
  }

  // Completing a value
  const colonIdx = last.indexOf(':');
  const prefix = last.slice(0, colonIdx + 1);
  tokens[tokens.length - 1] = prefix + suggestion;
  return tokens.join(' ') + ' ';
}

// ─── Component ────────────────────────────────────────────────────────────────

function IncidentsView() {
  const { t } = useTranslation();
  const orgApi = useOrgApi();
  const { canWrite } = useAuth();
  const navigate = useNavigate();
  const { orgPath } = useOrgPath();

  const scope = useOrgQueryScope();

  const [urlState, setUrlState] = useUrlState({ status: '', severity: '', q: '', page: '1' });
  const page = pageFromUrl(urlState.page);
  const setPage = (next: number) => setUrlState({ page: pageToUrl(next) });
  // A filter change invalidates the page number: page 4 of "open" has nothing
  // to do with page 4 of the unfiltered list.
  const setFilter = (next: { status?: string; severity?: string; q?: string }) => setUrlState({ ...next, page: '1' });
  const statusFilter = urlState.status;
  const severityFilter = urlState.severity;
  const search = urlState.q;
  // The search box keeps the page until the debounce fires, see below.
  const setSearch = (value: string) => setUrlState({ q: value });
  const pageSize = 50;
  const [showForm, setShowForm] = useState(false);
  const [formData, setFormData] = useState({ title: '', description: '', severity: 'warning', service: '' });

  const [updatingId, setUpdatingId] = useState<number | null>(null);
  const [resolvingId, setResolvingId] = useState<number | null>(null);
  const [resolutionNote, setResolutionNote] = useState('');

  // Autocomplete state
  const [suggestions, setSuggestions] = useState<string[]>([]);
  const [suggestionIndex, setSuggestionIndex] = useState(-1);
  const inputRef = useRef<HTMLInputElement>(null);
  const dropdownRef = useRef<HTMLDivElement>(null);

  // Debounced so typing in the search box does not fire a request per keystroke.
  // The page reset rides the same timer: dropping it on the keystroke would
  // refetch the previous search at page 0 first. Equal values mean a page
  // restored from a link, which must survive.
  const [debouncedSearch, setDebouncedSearch] = useState(search);
  useEffect(() => {
    if (search === debouncedSearch) return;
    const handle = setTimeout(() => { setDebouncedSearch(search); setPage(0); }, 250);
    return () => clearTimeout(handle);
  }, [search, debouncedSearch]); // eslint-disable-line react-hooks/exhaustive-deps -- setPage is rebuilt on every render

  const parsed = useMemo(() => parseSearch(debouncedSearch), [debouncedSearch]);

  // Effective filters, sent to the backend. Inline "field:value" tokens in the
  // search box override the chip toggles, matching the previous client behaviour.
  const queryParams = useMemo(() => ({
    status: parsed.filters['status'] || statusFilter,
    severity: parsed.filters['severity'] || severityFilter,
    service: parsed.filters['service'] || '',
    search: parsed.text,
  }), [statusFilter, severityFilter, parsed]);

  // An open form or a pending resolution is a detail row: refreshing under one
  // is what pausing is for.
  const { refetchInterval, buildControl } = useQueryRefresh(
    'incidents',
    0,
    showForm || resolvingId !== null,
  );

  // Best-effort: the counters are a secondary panel, and their failure must not
  // report the whole view as stale nor stop its cadence.
  const statsQuery = useQuery({
    queryKey: [scope, 'incidents', 'stats'],
    queryFn: async () => (await orgApi.incidents.stats()).data ?? EMPTY_STATS,
    refetchInterval,
  });

  const incidentsQueryKey = [
    scope, 'incidents', 'list',
    queryParams.status, queryParams.severity, queryParams.service, queryParams.search,
    page,
  ];
  const incidentsQuery = useQuery({
    queryKey: incidentsQueryKey,
    queryFn: async () => {
      const res = await orgApi.incidents.list({
        status: queryParams.status || undefined,
        severity: queryParams.severity || undefined,
        service: queryParams.service || undefined,
        search: queryParams.search || undefined,
        limit: pageSize,
        offset: page * pageSize,
      });
      return {
        incidents: (res.data as Incident[]) || [],
        total: Number(res.headers['x-total-count']) || 0,
      };
    },
    // The current page stays on screen while the next one loads.
    placeholderData: keepPreviousData,
    refetchInterval,
  });

  const stats = statsQuery.data ?? EMPTY_STATS;
  const incidents = incidentsQuery.data?.incidents ?? [];
  const total = incidentsQuery.data?.total ?? 0;

  const autoRefresh = buildControl({
    query: incidentsQuery,
    queryKey: incidentsQueryKey,
    prefix: [scope, 'incidents'],
  });
  const { refresh } = autoRefresh;

  // Distinct services (global) for the search autocomplete.
  const fieldValues = useMemo<Record<string, string[]>>(() => ({
    service: stats.services,
    severity: [...SEVERITIES],
    status: [...STATUSES],
  }), [stats.services]);

  // Update suggestions on search change
  useEffect(() => {
    if (!search.trim()) { setSuggestions([]); return; }
    const s = buildSuggestions(search, fieldValues);
    setSuggestions(s);
    setSuggestionIndex(-1);
  }, [search, fieldValues]);

  // Close dropdown on outside click
  useEffect(() => {
    const handler = (e: MouseEvent) => {
      if (!dropdownRef.current?.contains(e.target as Node) &&
          !inputRef.current?.contains(e.target as Node)) {
        setSuggestions([]);
      }
    };
    document.addEventListener('mousedown', handler);
    return () => document.removeEventListener('mousedown', handler);
  }, []);

  const handleSearchKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (!suggestions.length) return;
    if (e.key === 'ArrowDown') {
      e.preventDefault();
      setSuggestionIndex(i => Math.min(i + 1, suggestions.length - 1));
    } else if (e.key === 'ArrowUp') {
      e.preventDefault();
      setSuggestionIndex(i => Math.max(i - 1, -1));
    } else if (e.key === 'Tab' || e.key === 'Enter') {
      if (suggestionIndex >= 0) {
        e.preventDefault();
        setSearch(applySuggestion(search, suggestions[suggestionIndex]));
        setSuggestions([]);
      }
    } else if (e.key === 'Escape') {
      setSuggestions([]);
    }
  };

  const pickSuggestion = (s: string) => {
    setSearch(applySuggestion(search, s));
    setSuggestions([]);
    inputRef.current?.focus();
  };

  const handleCreate = async (e: React.FormEvent) => {
    e.preventDefault();
    try {
      await orgApi.incidents.create(formData);
      setShowForm(false);
      setFormData({ title: '', description: '', severity: 'warning', service: '' });
      refresh();
    } catch (err) {
      console.error('Failed to create incident:', err);
    }
  };

  const handleStatusChange = async (id: number, newStatus: string, note?: string) => {
    setUpdatingId(id);
    try {
      await orgApi.incidents.updateStatus(id, newStatus, note);
      refresh();
    } catch (err) {
      console.error('Failed to update incident:', err);
    } finally {
      setUpdatingId(null);
    }
  };

  const openResolveModal = (id: number) => {
    setResolutionNote('');
    setResolvingId(id);
  };

  const confirmResolve = async (e: React.FormEvent) => {
    e.preventDefault();
    if (resolvingId === null) return;
    const id = resolvingId;
    setResolvingId(null);
    await handleStatusChange(id, 'resolved', resolutionNote);
  };

  const toggleStatus = (s: string) => setFilter({ status: statusFilter === s ? '' : s });
  const toggleSeverity = (s: string) => setFilter({ severity: severityFilter === s ? '' : s });
  const hasFilters = statusFilter || severityFilter || search;

  return (
    <div className="incidents-view">
      <div className="incidents-header">
        <div>
          <h1><HiOutlineShieldCheck /> {t('incidents.title')}</h1>
          <p className="subtitle">{t('incidents.subtitle')}</p>
        </div>
        <div style={{ display: 'flex', alignItems: 'center', gap: '0.75rem' }}>
          <RefreshControl control={autoRefresh} />
          {canWrite && (
            <button className="btn-primary" onClick={() => setShowForm(true)}>
              <HiOutlinePlus /> {t('incidents.declare')}
            </button>
          )}
        </div>
      </div>

      {/* Stats */}
      <div className="incidents-stats">
        <button className={`incident-stat-card ${!statusFilter ? 'active' : ''}`} onClick={() => setFilter({ status: '' })}>
          <div className="incident-stat-icon neutral"><HiOutlineInbox /></div>
          <div className="incident-stat-value">{stats.total}</div>
          <div className="incident-stat-label">{t('incidents.stats.total')}</div>
        </button>
        <button className={`incident-stat-card open ${statusFilter === 'open' ? 'active' : ''}`} onClick={() => toggleStatus('open')}>
          <div className="incident-stat-icon red"><HiOutlineBellAlert /></div>
          <div className="incident-stat-value">{stats.open}</div>
          <div className="incident-stat-label">{t('incidents.stats.open')}</div>
        </button>
        <button className={`incident-stat-card ack ${statusFilter === 'acknowledged' ? 'active' : ''}`} onClick={() => toggleStatus('acknowledged')}>
          <div className="incident-stat-icon amber"><HiOutlineEye /></div>
          <div className="incident-stat-value">{stats.acknowledged}</div>
          <div className="incident-stat-label">{t('incidents.stats.acknowledged')}</div>
        </button>
        <button className={`incident-stat-card resolved ${statusFilter === 'resolved' ? 'active' : ''}`} onClick={() => toggleStatus('resolved')}>
          <div className="incident-stat-icon green"><HiOutlineCheckCircle /></div>
          <div className="incident-stat-value">{stats.resolved}</div>
          <div className="incident-stat-label">{t('incidents.stats.resolved')}</div>
        </button>
        {stats.critical > 0 && (
          <button className={`incident-stat-card critical-stat ${severityFilter === 'critical' ? 'active' : ''}`} onClick={() => toggleSeverity('critical')}>
            <div className="incident-stat-icon red"><HiOutlineExclamationTriangle /></div>
            <div className="incident-stat-value">{stats.critical}</div>
            <div className="incident-stat-label">{t('incidents.stats.critical')}</div>
          </button>
        )}
      </div>

      {/* Toolbar */}
      <div className="incidents-toolbar">
        {/* Smart search with autocomplete */}
        <div className="incidents-search-wrap">
          <HiOutlineMagnifyingGlass className="incidents-search-icon" />
          <input
            ref={inputRef}
            className="incidents-search"
            type="text"
            placeholder={t('incidents.search_placeholder')}
            value={search}
            onChange={e => setSearch(e.target.value)}
            onKeyDown={handleSearchKeyDown}
            autoComplete="off"
            spellCheck={false}
          />
          {search && (
            <button className="incidents-search-clear" onClick={() => { setSearch(''); setSuggestions([]); }}>
              <HiOutlineXMark />
            </button>
          )}

          {/* Autocomplete dropdown */}
          {suggestions.length > 0 && (
            <div className="incidents-autocomplete" ref={dropdownRef}>
              {suggestions.map((s, idx) => {
                const isKey = s.endsWith(':');
                return (
                  <button
                    key={s}
                    className={`autocomplete-item ${idx === suggestionIndex ? 'active' : ''} ${isKey ? 'is-key' : 'is-val'}`}
                    onMouseDown={e => { e.preventDefault(); pickSuggestion(s); }}
                    onMouseEnter={() => setSuggestionIndex(idx)}
                  >
                    {isKey ? (
                      <><span className="autocomplete-key">{s}</span><span className="autocomplete-hint">{t('incidents.filter_hint')}</span></>
                    ) : (
                      <span className="autocomplete-val">{s}</span>
                    )}
                  </button>
                );
              })}
            </div>
          )}
        </div>

        {/* Status filter chips */}
        <div className="incidents-filter-group">
          <HiOutlineFunnel className="incidents-filter-icon" />
          {STATUSES.map(s => (
            <button
              key={s}
              className={`incidents-filter-chip status-chip-${s} ${statusFilter === s ? 'active' : ''}`}
              onClick={() => toggleStatus(s)}
            >
              {t(`incidents.status.${s}`)}
            </button>
          ))}
        </div>

        {/* Severity filter chips */}
        <div className="incidents-filter-group">
          {SEVERITIES.map(s => (
            <button
              key={s}
              className={`incidents-filter-chip severity-chip-${s} ${severityFilter === s ? 'active' : ''}`}
              onClick={() => toggleSeverity(s)}
            >
              {s}
            </button>
          ))}
        </div>

        {hasFilters && (
          <button className="incidents-clear-all" onClick={() => { setFilter({ status: '', severity: '', q: '' }); }}>
            <HiOutlineXMark /> {t('incidents.clear_filters')}
          </button>
        )}
      </div>

      {hasFilters && !incidentsQuery.isPending && (
        <div className="incidents-result-count">
          {t('incidents.result_count', { count: total, total: stats.total })}
        </div>
      )}

      {/* Create form modal */}
      {showForm && (
        <div className="alert-modal-overlay" onClick={() => setShowForm(false)}>
          <div className="alert-modal" onClick={e => e.stopPropagation()}>
            <div className="alert-modal-header">
              <h3>{t('incidents.declare')}</h3>
              <button className="btn-close" onClick={() => setShowForm(false)}><HiOutlineXMark /></button>
            </div>
            <form onSubmit={handleCreate} className="alert-form">
              <div className="form-row">
                <label>{t('incidents.form.title')} *</label>
                <input type="text" required value={formData.title} onChange={e => setFormData({ ...formData, title: e.target.value })} placeholder={t('incidents.form.title_placeholder')} />
              </div>
              <div className="form-row">
                <label>{t('incidents.form.description')}</label>
                <textarea value={formData.description} onChange={e => setFormData({ ...formData, description: e.target.value })} placeholder={t('incidents.form.description_placeholder')} />
              </div>
              <div className="form-row-group">
                <div className="form-row">
                  <label>{t('incidents.form.severity')}</label>
                  <select value={formData.severity} onChange={e => setFormData({ ...formData, severity: e.target.value })}>
                    <option value="critical">{t('incidents.severity.critical')}</option>
                    <option value="warning">{t('incidents.severity.warning')}</option>
                    <option value="info">{t('incidents.severity.info')}</option>
                  </select>
                </div>
                <div className="form-row">
                  <label>{t('incidents.form.service')}</label>
                  <input type="text" value={formData.service} onChange={e => setFormData({ ...formData, service: e.target.value })} placeholder={t('incidents.form.service_placeholder')} />
                </div>
              </div>
              <div className="form-actions">
                <button type="button" className="btn-secondary" onClick={() => setShowForm(false)}>{t('common.cancel')}</button>
                <button type="submit" className="btn-primary">{t('incidents.form.submit')}</button>
              </div>
            </form>
          </div>
        </div>
      )}

      {/* Resolve modal (optional post-mortem note) */}
      {resolvingId !== null && (
        <div className="alert-modal-overlay" onClick={() => setResolvingId(null)}>
          <div className="alert-modal" onClick={e => e.stopPropagation()}>
            <div className="alert-modal-header">
              <h3>{t('incidents.resolve_modal.title')}</h3>
              <button className="btn-close" onClick={() => setResolvingId(null)}><HiOutlineXMark /></button>
            </div>
            <form onSubmit={confirmResolve} className="alert-form">
              <div className="form-row">
                <label>{t('incidents.resolve_modal.note_label')}</label>
                <textarea
                  value={resolutionNote}
                  onChange={e => setResolutionNote(e.target.value)}
                  placeholder={t('incidents.resolve_modal.note_placeholder')}
                  rows={4}
                  autoFocus
                />
              </div>
              <div className="form-actions">
                <button type="button" className="btn-secondary" onClick={() => setResolvingId(null)}>{t('common.cancel')}</button>
                <button type="submit" className="btn-primary">
                  <HiOutlineCheckCircle /> {t('incidents.resolve_modal.confirm')}
                </button>
              </div>
            </form>
          </div>
        </div>
      )}

      {/* List */}
      <div className="incidents-list">
        {incidentsQuery.isPending ? (
          <div className="incidents-loading"><HiOutlineClock /> {t('incidents.loading')}</div>
        ) : incidents.length === 0 ? (
          <div className="empty-state">
            <HiOutlineShieldCheck className="empty-state-icon" />
            <div className="empty-state-title">{hasFilters ? t('incidents.filtered_empty_title') : t('incidents.empty_title')}</div>
            <div className="empty-state-description">{hasFilters ? t('incidents.filtered_empty_desc') : t('incidents.empty_desc')}</div>
          </div>
        ) : (
          incidents.map(incident => {
            // Deep-link to the impacted entity: host-scoped rules win, else the
            // monitored service, else app incidents land on the errors view
            // filtered on the service name.
            const target = incident.host_id
              ? { path: `/hosts/${incident.host_id}`, label: t('incidents.open_host') }
              : incident.service_id
                ? { path: `/services/${incident.service_id}`, label: t('incidents.open_service') }
                : incident.service
                  ? { path: `/errors?service=${encodeURIComponent(incident.service)}&relative=24h`, label: t('incidents.open_errors') }
                  : null;
            return (
            <div
              key={incident.id}
              className={`incident-card severity-${incident.severity}${target ? ' clickable' : ''}`}
              onClick={target ? () => navigate(orgPath(target.path)) : undefined}
              title={target ? target.label : undefined}
            >
              <div className="incident-card-header">
                <div className="incident-status-wrapper">
                  <span className={`incident-status-badge status-${incident.status}`}>
                    {incident.status === 'open' && <HiOutlineBellAlert />}
                    {incident.status === 'acknowledged' && <HiOutlineEye />}
                    {incident.status === 'resolved' && <HiOutlineCheckCircle />}
                    {t(`incidents.status.${incident.status}`)}
                  </span>
                  <span className={`incident-severity-badge severity-${incident.severity}`}>
                    {incident.severity}
                  </span>
                </div>
                <div className="incident-time" title={new Date(incident.started_at).toLocaleString()}>
                  {timeAgo(incident.started_at, t)}
                </div>
              </div>

              <h3 className="incident-title">{incident.title}</h3>
              {incident.description && <p className="incident-description">{incident.description}</p>}

              <div className="incident-meta">
                {incident.service && (
                  <button className="incident-tag clickable" onClick={e => { e.stopPropagation(); setSearch(`service:${incident.service}`); }}>
                    {incident.service}
                  </button>
                )}
                {incident.acknowledged_at && (
                  <span className="incident-meta-info">{t('incidents.acknowledged_ago', { time: timeAgo(incident.acknowledged_at, t) })}</span>
                )}
                {incident.resolved_at && (
                  <span className="incident-resolved">
                    {t('incidents.resolved_at', { date: new Date(incident.resolved_at).toLocaleString() })}
                  </span>
                )}
              </div>

              {incident.status === 'resolved' && incident.resolution_note && (
                <p className="incident-description" style={{ fontStyle: 'italic' }}>
                  <strong>{t('incidents.resolution_note_label')}</strong> {incident.resolution_note}
                </p>
              )}

              {canWrite && (
              <div className="incident-actions" onClick={e => e.stopPropagation()}>
                {incident.status === 'open' && (
                  <>
                    <button className="btn-sm ack" disabled={updatingId === incident.id} onClick={() => handleStatusChange(incident.id, 'acknowledged')}>
                      <HiOutlineEye /> {updatingId === incident.id ? '…' : t('incidents.actions.acknowledge')}
                    </button>
                    <button className="btn-sm resolve" disabled={updatingId === incident.id} onClick={() => openResolveModal(incident.id)}>
                      <HiOutlineCheckCircle /> {updatingId === incident.id ? '…' : t('incidents.actions.resolve')}
                    </button>
                  </>
                )}
                {incident.status === 'acknowledged' && (
                  <button className="btn-sm resolve" disabled={updatingId === incident.id} onClick={() => openResolveModal(incident.id)}>
                    <HiOutlineCheckCircle /> {updatingId === incident.id ? '…' : t('incidents.actions.resolve')}
                  </button>
                )}
                {incident.status === 'resolved' && (
                  <button className="btn-sm reopen" disabled={updatingId === incident.id} onClick={() => handleStatusChange(incident.id, 'open')}>
                    <HiOutlineExclamationTriangle /> {updatingId === incident.id ? '…' : t('incidents.actions.reopen')}
                  </button>
                )}
              </div>
              )}
            </div>
            );
          })
        )}
      </div>

      <Pagination page={page} pageSize={pageSize} total={total} onPageChange={setPage} />
    </div>
  );
}

export default IncidentsView;
