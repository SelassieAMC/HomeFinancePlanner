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
        ← Prev
      </button>
      <span className="pagination-status">
        {first}–{last} of {total}
      </span>
      <button
        className="btn btn-secondary"
        disabled={offset + limit >= total}
        onClick={() => onPage(offset + limit)}
      >
        Next →
      </button>
    </div>
  );
}