import { useTranslation } from 'react-i18next';

interface PaginationProps {
  total: number;
  limit: number;
  offset: number;
  /** Receives the offset of the page to show. */
  onPage: (offset: number) => void;
}

/** Prev/Next pager for server-side paginated lists. Renders "X–Y of Z" and
 *  disables buttons at the edges — simple on purpose; no page-number list. */
export function Pagination({ total, limit, offset, onPage }: PaginationProps) {
  const { t } = useTranslation('common');
  if (total <= 0) return null;
  const first = offset + 1;
  const last = Math.min(offset + limit, total);
  return (
    <div className="pagination">
      <button
        className="btn btn-secondary"
        disabled={offset <= 0}
        onClick={() => onPage(Math.max(0, offset - limit))}
      >
        {t('pagination.prev')}
      </button>
      <span className="pagination-status">
        {t('pagination.status', { first, last, total })}
      </span>
      <button
        className="btn btn-secondary"
        disabled={offset + limit >= total}
        onClick={() => onPage(offset + limit)}
      >
        {t('pagination.next')}
      </button>
    </div>
  );
}