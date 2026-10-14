import type { ReactNode } from 'react';
import { Card, Spinner, ErrorMessage, EmptyState } from '../../../components/ui';

interface ChartCardProps {
  title: string;
  subtitle?: string;
  /** The data window the chart actually covers ("Sep 8 – 14", "Apr – Sep
   *  2026"), shown next to the title. Charts that ignore the dashboard's
   *  period picker (trend windows) say so by stating their own window. */
  window?: string;
  loading: boolean;
  error: Error | null;
  /** Rendered when loading finished, nothing failed, and `empty` holds. */
  emptyMessage: string;
  empty: boolean;
  children: ReactNode;
  className?: string;
}

/** Card chrome shared by all analytics charts: the standard loading/error/
 * empty trio plus an optional explanatory subtitle. */
export function ChartCard({
  title,
  subtitle,
  window,
  loading,
  error,
  emptyMessage,
  empty,
  children,
  className = 'chart-card',
}: ChartCardProps) {
  return (
    <Card title={title} className={className}>
      {window && <p className="chart-window">{window}</p>}
      {subtitle && <p className="chart-subtitle">{subtitle}</p>}
      {loading ? (
        <Spinner />
      ) : error ? (
        <ErrorMessage message={error.message} />
      ) : empty ? (
        <EmptyState message={emptyMessage} />
      ) : (
        children
      )}
    </Card>
  );
}