import type { ReactNode } from 'react';
import { Card, Spinner, ErrorMessage, EmptyState } from '../../../components/ui';

interface ChartCardProps {
  title: string;
  subtitle?: string;
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
  loading,
  error,
  emptyMessage,
  empty,
  children,
  className = 'chart-card',
}: ChartCardProps) {
  return (
    <Card title={title} className={className}>
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