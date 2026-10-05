import { createContext, useContext } from 'react';

import i18n from '../i18n';

export interface DateRange {
  start: Date;
  end: Date;
  relative?: string;
}

export function formatDateRangeLabel(range: DateRange, options?: { withTime?: boolean }): string {
  const locale = i18n.language;
  const opts: Intl.DateTimeFormatOptions = options?.withTime
    ? { day: '2-digit', month: 'short', hour: '2-digit', minute: '2-digit' }
    : { day: '2-digit', month: 'short', year: 'numeric', hour: '2-digit', minute: '2-digit' }; // Always show time for clarity if relative

  // If not explicitly asked, let's at least show time for relative short ranges
  const timeOpts = { hour: '2-digit', minute: '2-digit' } as Intl.DateTimeFormatOptions;

  const s = range.start.toLocaleString(locale, options?.withTime ? opts : (range.relative && ['15m','1h','3h','24h'].includes(range.relative) ? { ...opts, ...timeOpts } : opts));
  const e = range.end.toLocaleString(locale, options?.withTime ? opts : (range.relative && ['15m','1h','3h','24h'].includes(range.relative) ? { ...opts, ...timeOpts } : opts));
  const absoluteLabel = s === e ? s : `${s} — ${e}`;

  if (range.relative) {
    switch (range.relative) {
      case '15m':
        return i18n.t('date_range.relative.15m', {
          start: range.start.toLocaleTimeString(locale, timeOpts),
          end: range.end.toLocaleTimeString(locale, timeOpts),
        });
      case '1h':
        return i18n.t('date_range.relative.1h', {
          start: range.start.toLocaleTimeString(locale, timeOpts),
          end: range.end.toLocaleTimeString(locale, timeOpts),
        });
      case '3h':
        return i18n.t('date_range.relative.3h', {
          start: range.start.toLocaleTimeString(locale, timeOpts),
          end: range.end.toLocaleTimeString(locale, timeOpts),
        });
      case '24h':
        return i18n.t('date_range.relative.24h', { start: s, end: e });
      case '7d':
        return i18n.t('date_range.relative.7d', { start: s, end: e });
      case '30d':
        return i18n.t('date_range.relative.30d', { start: s, end: e });
    }
  }

  return absoluteLabel;
}

export interface DateRangeContextType {
  dateRange: DateRange;
  setDateRange: (range: DateRange) => void;
  formatDateRangeLabel: (range: DateRange, options?: { withTime?: boolean }) => string;
}

// The context object lives in this component-free module on purpose: editing the
// DateRangeProvider (a separate file) no longer re-runs createContext, so its
// identity stays stable across Fast Refresh and consumers can't desync into a
// spurious "must be used within a DateRangeProvider" throw.
export const DateRangeContext = createContext<DateRangeContextType | undefined>(undefined);

export function useDateRange(): DateRangeContextType {
  const ctx = useContext(DateRangeContext);
  if (ctx === undefined) {
    throw new Error('useDateRange must be used within a DateRangeProvider');
  }
  return ctx;
}
