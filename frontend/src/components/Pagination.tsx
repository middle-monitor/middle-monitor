import { HiOutlineChevronLeft, HiOutlineChevronRight } from 'react-icons/hi2';
import { useTranslation } from 'react-i18next';
import './Pagination.css';

interface PaginationProps {
  /** Zero-based current page index. */
  page: number;
  pageSize: number;
  /** Total number of items across all pages (from the X-Total-Count header). */
  total: number;
  onPageChange: (page: number) => void;
}

/**
 * Shared Previous/Next pager for server-paginated lists. Renders nothing when
 * there is a single page. Matches the inline pager previously used by the
 * Logs and Traces views.
 */
export function Pagination({ page, pageSize, total, onPageChange }: PaginationProps) {
  const { t } = useTranslation();
  const totalPages = Math.max(1, Math.ceil(total / pageSize));
  if (totalPages <= 1) return null;

  return (
    <div className="pagination">
      <button disabled={page === 0} onClick={() => onPageChange(page - 1)}>
        <HiOutlineChevronLeft /> {t('common.pagination.previous')}
      </button>
      <span>{t('common.pagination.of', { page: page + 1, total: totalPages })}</span>
      <button disabled={page >= totalPages - 1} onClick={() => onPageChange(page + 1)}>
        {t('common.pagination.next')} <HiOutlineChevronRight />
      </button>
    </div>
  );
}
