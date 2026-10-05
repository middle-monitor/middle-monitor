import { useCallback, useMemo, useRef } from 'react';
import { useSearchParams } from 'react-router-dom';

type UrlStateOptions = { push?: boolean };

// Filters, search and pagination live in the URL so a refresh, a second tab or a
// link sent to a colleague show the same list at the same page. Writes replace
// the history entry: ten keystrokes in a search box must stay one back-button
// step away from the previous view. Opening a detail passes { push: true } so
// back closes the panel instead of leaving the view. Scroll is never reset.
export function useUrlState<T extends Record<string, string>>(defaults: T) {
  const [searchParams, setSearchParams] = useSearchParams();
  // Defaults are literals rebuilt on every render; freeze the first one.
  const defaultsRef = useRef(defaults);

  const state = useMemo(() => {
    const next = { ...defaultsRef.current };
    for (const key of Object.keys(next)) {
      const value = searchParams.get(key);
      if (value !== null) next[key as keyof T] = value as T[keyof T];
    }
    return next;
  }, [searchParams]);

  const setState = useCallback((changes: Partial<T>, options?: UrlStateOptions) => {
    setSearchParams((prev) => {
      const next = new URLSearchParams(prev);
      for (const [key, value] of Object.entries(changes)) {
        if (value === undefined) continue;
        // Default values stay out of the URL, so a shared link only carries
        // what the user actually changed.
        if (value === defaultsRef.current[key]) next.delete(key);
        else next.set(key, value);
      }
      return next;
    }, { replace: !options?.push, preventScrollReset: true });
  }, [setSearchParams]);

  return [state, setState] as const;
}

// URL pages are 1-based because that is what the reader of a shared link sees;
// the views index from 0.
export function pageFromUrl(value: string): number {
  const n = Number(value);
  return Number.isFinite(n) && n > 1 ? Math.floor(n) - 1 : 0;
}

export function pageToUrl(page: number): string {
  return String(page + 1);
}
