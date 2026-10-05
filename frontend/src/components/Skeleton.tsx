import { useTranslation } from 'react-i18next';
import './Skeleton.css';

interface SkeletonProps {
  /** Placeholder rows; pick what the loaded area usually shows, so it does not shrink when data lands. */
  rows?: number;
  /** Set on the secondary skeleton of a view, so the load is announced once. */
  silent?: boolean;
}

/**
 * Placeholder for the result area of a view during its first load, so the
 * title, filters, search and pager around it stay mounted.
 */
export function Skeleton({ rows = 5, silent = false }: SkeletonProps) {
  const { t } = useTranslation();

  const lines = Array.from({ length: rows }, (_, i) => (
    <div key={i} className="skeleton-row" aria-hidden="true" />
  ));

  if (silent) {
    return <div className="skeleton" aria-hidden="true">{lines}</div>;
  }

  return (
    // The rows say nothing to a screen reader; the loading state has to, since
    // the skeleton replaces the node that carried common.loading.
    <div className="skeleton" role="status" aria-busy="true">
      <span className="skeleton-label">{t('common.loading')}</span>
      {lines}
    </div>
  );
}

/**
 * Reads a count that may not have loaded yet: 0 and "not loaded" look the same
 * on screen, so the first load shows a dash rather than a figure it corrects a
 * moment later. Takes the pending state of the query the count comes from, not
 * the one the skeleton waits on.
 */
export function firstLoadCount(pending: boolean) {
  return (n: number) => (pending ? '—' : n);
}
