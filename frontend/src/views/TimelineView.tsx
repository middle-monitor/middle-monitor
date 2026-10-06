import { useState, useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import { keepPreviousData, useQuery } from '@tanstack/react-query';
import {
  HiRocketLaunch,
  HiArrowPath,
  HiBolt,
  HiArrowTrendingUp,
  HiMapPin,
  HiInbox,
  HiClock,
  HiOutlineCalendarDays,
} from 'react-icons/hi2';
import { Link } from 'react-router-dom';
import { type Event, type Host } from '../api';
import { useOrgApi, useOrgQueryScope } from '../hooks/useOrgApi';
import { useUrlState } from '../hooks/useUrlState';
import { useQueryRefresh } from '../hooks/useQueryRefresh';
import { allHostsKey } from '../queryClient';
import { RefreshControl } from '../components/RefreshControl';
import { useOrgPath } from '../hooks/useOrgPath';
import { useDateRange } from '../contexts/DateRangeContext';
import { useDateRangeBounds, useDateRangeKey } from '../hooks/useDateRangeBounds';
import { DateRangePicker } from '../components/DateRangePicker';
import { FilterTabs } from '../components/Tabs';
import { Skeleton, firstLoadCount } from '../components/Skeleton';

// A fresh [] on every render gives each dependent useMemo a new identity and
// makes it recompute every time. One frozen empty list keeps that identity stable.
const EMPTY_LIST: never[] = [];

function TimelineView() {
  const { t, i18n } = useTranslation();
  const orgApi = useOrgApi();
  const scope = useOrgQueryScope();
  const { dateRange, setDateRange, formatDateRangeLabel } = useDateRange();
  const getDateBounds = useDateRangeBounds();
  const dateRangeKey = useDateRangeKey();
  const [urlState, setFilter] = useUrlState({ type: 'all', service: '' });
  const activeFilter = urlState.type;
  const serviceFilter = urlState.service;
  const [calendarOpen, setCalendarOpen] = useState(false);
  const { orgPath } = useOrgPath();

  const { refetchInterval, buildControl } = useQueryRefresh('timeline', 30000);

  // An event's `service` is a bare name covering two kinds of entity: a host
  // (agent restarts, TLS renewals) or an APM application (deploys, error
  // spikes). Resolving it against the hosts list is what tells them apart.
  const hostsQuery = useQuery({
    queryKey: allHostsKey(scope),
    queryFn: async () => ((await orgApi.hosts.list()).data as Host[]) || [],
  });

  // No hosts loaded: every badge falls back to the errors view.
  const hostIds = useMemo(() => {
    const map: Record<string, number> = {};
    for (const h of hostsQuery.data ?? []) map[h.name] = h.id;
    return map;
  }, [hostsQuery.data]);

  const targetFor = (service: string): string =>
    hostIds[service] !== undefined
      ? orgPath(`/hosts/${hostIds[service]}`)
      : orgPath(`/errors?service=${encodeURIComponent(service)}`);

  const timelineQueryKey = [scope, 'timeline', dateRangeKey];
  const timelineQuery = useQuery({
    queryKey: timelineQueryKey,
    queryFn: async () => {
      const { startIso, endIso } = getDateBounds();
      const response = await orgApi.dashboard.getTimeline(undefined, startIso, endIso);
      return (response.data as Event[]) || [];
    },
    // Another window keeps the current events on screen while it loads.
    placeholderData: keepPreviousData,
    refetchInterval,
  });

  const events = timelineQuery.data ?? EMPTY_LIST;

  const autoRefresh = buildControl({
    query: timelineQuery,
    queryKey: timelineQueryKey,
    prefix: [scope, 'timeline'],
  });

  // Filter tabs based on event types
  const filterTabs = useMemo(() => {
    const typeCounts = events.reduce((acc, event) => {
      acc[event.type] = (acc[event.type] || 0) + 1;
      return acc;
    }, {} as Record<string, number>);

    const tabs: Array<{
      id: string;
      label: string;
      count: number;
      color: 'default' | 'success' | 'warning' | 'error';
    }> = [{ id: 'all', label: t('timeline_view.filters.all'), count: events.length, color: 'default' }];

    if (typeCounts['deploy']) {
      tabs.push({ id: 'deploy', label: t('timeline_view.filters.deploy'), count: typeCounts['deploy'], color: 'success' });
    }
    if (typeCounts['crash']) {
      tabs.push({ id: 'crash', label: t('timeline_view.filters.crash'), count: typeCounts['crash'], color: 'error' });
    }
    if (typeCounts['error_spike']) {
      tabs.push({ id: 'error_spike', label: t('timeline_view.filters.error_spike'), count: typeCounts['error_spike'], color: 'warning' });
    }
    if (typeCounts['restart']) {
      tabs.push({ id: 'restart', label: t('timeline_view.filters.restart'), count: typeCounts['restart'], color: 'default' });
    }

    return tabs;
  }, [events, t]);

  // Service multi-filter options
  const serviceOptions = useMemo(() => Array.from(new Set(events.map(e => e.service))).sort(), [events]);

  const filteredEvents = useMemo(() => {
    return events.filter((event) => {
      if (activeFilter !== 'all' && event.type !== activeFilter) return false;
      if (serviceFilter && event.service !== serviceFilter) return false;
      return true;
    });
  }, [events, activeFilter, serviceFilter]);

  const eventValue = firstLoadCount(timelineQuery.isPending);

  // Only the results area waits on the first load: a failed refresh keeps the
  // last known events on screen.
  if (timelineQuery.isLoadingError) {
    return <div className='error-message'>{t('timeline_view.load_error')}</div>;
  }

  const getEventIcon = (type: string) => {
    const iconStyle = { fontSize: '1.125rem' };
    switch (type) {
      case 'deploy':   return <HiRocketLaunch style={{ ...iconStyle, color: 'var(--status-success)' }} />;
      case 'restart':  return <HiArrowPath style={{ ...iconStyle, color: 'var(--brand-primary)' }} />;
      case 'crash':    return <HiBolt style={{ ...iconStyle, color: 'var(--status-error)' }} />;
      case 'error_spike': return <HiArrowTrendingUp style={{ ...iconStyle, color: 'var(--status-warning)' }} />;
      default:         return <HiMapPin style={{ ...iconStyle, color: 'var(--text-tertiary)' }} />;
    }
  };

  const getEventStyles = (type: string) => {
    switch (type) {
      case 'deploy':    return { bg: 'var(--status-success-bg)', border: 'var(--status-success-border)', text: 'var(--status-success)', dot: 'var(--status-success)' };
      case 'restart':   return { bg: 'var(--status-info-bg)', border: 'var(--status-info-border)', text: 'var(--status-info)', dot: 'var(--brand-primary)' };
      case 'crash':     return { bg: 'var(--status-error-bg)', border: 'var(--status-error-border)', text: 'var(--status-error)', dot: 'var(--status-error)' };
      case 'error_spike': return { bg: 'var(--status-warning-bg)', border: 'var(--status-warning-border)', text: 'var(--status-warning)', dot: 'var(--status-warning)' };
      default:          return { bg: 'var(--bg-secondary)', border: 'var(--border-primary)', text: 'var(--text-secondary)', dot: 'var(--text-tertiary)' };
    }
  };

  return (
    <div>
      {/* Page Header */}
      <div className='page-header'>
        <div>
          <h1 className='page-title'>{t('timeline_view.title')}</h1>
          <p className='page-subtitle'>{t('timeline_view.subtitle')}</p>
        </div>

        {/* Date range picker */}
        <div style={{ display: 'flex', alignItems: 'center', gap: '0.5rem' }}>
          <RefreshControl control={autoRefresh} />
          <button className='date-range-trigger' onClick={() => setCalendarOpen(true)}>
            <HiOutlineCalendarDays />
            {formatDateRangeLabel(dateRange, { withTime: false })}
          </button>
        </div>
      </div>

      <DateRangePicker
        isOpen={calendarOpen}
        value={dateRange}
        onChange={(r) => { setDateRange(r); setCalendarOpen(false); }}
        onClose={() => setCalendarOpen(false)}
      />

      <div className='card'>
        <h2 className='card-title'>
          <HiClock className='card-title-icon' style={{ color: 'var(--brand-primary)' }} />
          <span>{t('timeline_view.recent_events')}</span>
          <span style={{ marginLeft: 'auto', fontSize: '0.8rem', fontWeight: 400, color: 'var(--text-secondary)' }}>
            {t('timeline_view.event_count', {
              count: filteredEvents.length,
              value: eventValue(filteredEvents.length),
            })}
          </span>
        </h2>

        {/* Filters bar */}
        <div style={{ display: 'flex', flexWrap: 'wrap', gap: '0.75rem', marginBottom: '1rem', alignItems: 'center' }}>
          {events.length > 0 && (
            <FilterTabs filters={filterTabs} activeFilter={activeFilter} onChange={(type) => setFilter({ type })} />
          )}
          {serviceOptions.length > 1 && (
            <select
              className='form-control'
              value={serviceFilter}
              onChange={(e) => setFilter({ service: e.target.value })}
              style={{ maxWidth: 180, height: 36, fontSize: '0.8125rem' }}
            >
              <option value=''>{t('timeline_view.all_services')}</option>
              {serviceOptions.map((s) => <option key={s} value={s}>{s}</option>)}
            </select>
          )}
        </div>

        <div style={{ position: 'relative', paddingLeft: '2rem' }}>
          {timelineQuery.isPending && <Skeleton rows={6} />}
          {filteredEvents.map((event, index) => {
            const styles = getEventStyles(event.type);
            const isLast = index === filteredEvents.length - 1;

            return (
              <div
                key={event.id}
                style={{
                  position: 'relative',
                  paddingBottom: isLast ? 0 : '1.5rem',
                  borderLeft: isLast ? 'none' : '2px solid var(--border-primary)',
                  marginLeft: '-1rem',
                  paddingLeft: '1.5rem',
                }}>
                <div
                  style={{
                    position: 'absolute',
                    left: '-1.5rem',
                    top: '0.25rem',
                    width: '12px',
                    height: '12px',
                    borderRadius: '50%',
                    background: styles.dot,
                    border: '2px solid var(--surface-primary)',
                    boxShadow: 'var(--shadow-sm)',
                  }}
                />

                <div
                  style={{
                    background: styles.bg,
                    border: `1px solid ${styles.border}`,
                    borderRadius: '8px',
                    padding: '1rem',
                    transition: 'all 0.15s ease',
                  }}>
                  <div style={{ display: 'flex', alignItems: 'center', gap: '0.5rem', marginBottom: '0.5rem' }}>
                    {getEventIcon(event.type)}
                    <span style={{ fontWeight: 600, fontSize: '0.6875rem', textTransform: 'uppercase', letterSpacing: '0.05em', color: styles.text }}>
                      {t(`timeline_view.event_type.${event.type}`, { defaultValue: event.type.replace('_', ' ') })}
                    </span>
                    <span style={{ fontSize: '0.75rem', color: 'var(--text-tertiary)', marginLeft: 'auto', fontFamily: 'monospace' }}>
                      {new Date(event.timestamp).toLocaleString(i18n.language)}
                    </span>
                  </div>

                  <div style={{ color: 'var(--text-primary)', fontSize: '0.875rem', marginBottom: '0.75rem', lineHeight: 1.5 }}>
                    {event.message}
                  </div>

                  <div style={{ display: 'flex', gap: '0.5rem', flexWrap: 'wrap' }}>
                    <Link
                      to={targetFor(event.service)}
                      className='status-badge status-info'>
                      {event.service}
                    </Link>
                  </div>
                </div>
              </div>
            );
          })}

          {!timelineQuery.isPending && filteredEvents.length === 0 && (
            <div className='empty-state'>
              <HiInbox className='empty-state-icon' />
              <div className='empty-state-title'>{t('timeline_view.empty.title')}</div>
              <div className='empty-state-description'>
                {activeFilter !== 'all'
                  ? t('timeline_view.empty.filtered', { type: t(`timeline_view.event_type.${activeFilter}`, { defaultValue: activeFilter.replace('_', ' ') }) })
                  : t('timeline_view.empty.none')}
              </div>
            </div>
          )}
        </div>
      </div>
    </div>
  );
}

export default TimelineView;
