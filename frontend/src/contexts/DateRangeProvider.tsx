import { useState, useEffect, useCallback, type ReactNode } from 'react';

import {
  DateRangeContext,
  formatDateRangeLabel,
  type DateRange,
  type DateRangeContextType,
} from './DateRangeContext';
import { rangeForPreset } from '../utils/dateRangePresets';

const STORAGE_KEY = 'mm_date_range';

function getDefaultRange(): DateRange {
  const now = new Date();
  const start = new Date(now.getTime() - 60 * 60 * 1000); // 1 hour by default
  return { start, end: now, relative: '1h' };
}

function loadStoredRange(): DateRange {
  if (typeof window === 'undefined') return getDefaultRange();
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (!raw) return getDefaultRange();
    const parsed = JSON.parse(raw) as { start: string; end: string; relative?: string };
    if (!parsed.start || !parsed.end) return getDefaultRange();

    if (parsed.relative) {
      // A preset is stored by id and recomputed for the current time.
      const fresh = rangeForPreset(parsed.relative);
      if (fresh) return { ...fresh, relative: parsed.relative };
    }

    const start = new Date(parsed.start);
    const end = new Date(parsed.end);
    if (Number.isNaN(start.getTime()) || Number.isNaN(end.getTime())) return getDefaultRange();
    if (start >= end) return getDefaultRange();
    return { start, end };
  } catch {
    return getDefaultRange();
  }
}

function saveRange(range: DateRange): void {
  if (typeof window === 'undefined') return;
  try {
    localStorage.setItem(
      STORAGE_KEY,
      JSON.stringify({
        start: range.start.toISOString(),
        end: range.end.toISOString(),
        relative: range.relative,
      })
    );
  } catch {
    // ignore
  }
}

export function DateRangeProvider({ children }: { children: ReactNode }) {
  const [dateRange, setDateRangeState] = useState<DateRange>(loadStoredRange);

  const setDateRange = useCallback((range: DateRange) => {
    setDateRangeState(range);
    saveRange(range);
  }, []);

  // Auto-refresh relative dates
  useEffect(() => {
    if (!dateRange.relative) return;

    const intervalId = setInterval(() => {
      const fresh = rangeForPreset(dateRange.relative!);
      if (!fresh) return; // Unknown relative id, nothing to refresh

      // Update state without saving to localStorage repeatedly
      setDateRangeState({ ...fresh, relative: dateRange.relative });
    }, 15000); // Check every 15 seconds

    return () => clearInterval(intervalId);
  }, [dateRange.relative]);

  // Sync from localStorage when storage event (e.g. another tab) updates
  useEffect(() => {
    const handleStorage = (e: StorageEvent) => {
      if (e.key === STORAGE_KEY && e.newValue) {
        try {
          const parsed = JSON.parse(e.newValue) as { start: string; end: string; relative?: string };
          const fresh = parsed.relative ? rangeForPreset(parsed.relative) : null;
          setDateRangeState(
            fresh
              ? { ...fresh, relative: parsed.relative }
              : { start: new Date(parsed.start), end: new Date(parsed.end), relative: parsed.relative },
          );
        } catch {
          // ignore
        }
      }
    };
    window.addEventListener('storage', handleStorage);
    return () => window.removeEventListener('storage', handleStorage);
  }, []);

  const value: DateRangeContextType = {
    dateRange,
    setDateRange,
    formatDateRangeLabel,
  };

  return (
    <DateRangeContext.Provider value={value}>
      {children}
    </DateRangeContext.Provider>
  );
}
