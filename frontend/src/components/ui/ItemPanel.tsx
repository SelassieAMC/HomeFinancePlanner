import type { ReactNode } from 'react';

// ItemPanel is the collapsible record pattern used across views: the summary
// shows an icon, the record name, an optional secondary line and a highlighted
// value; expanding reveals the details/actions (children).
export function ItemPanel({
  icon,
  title,
  subtitle,
  value,
  valueClass,
  children,
}: {
  icon?: ReactNode;
  title: ReactNode;
  subtitle?: ReactNode;
  value?: ReactNode;
  valueClass?: string;
  children: ReactNode;
}) {
  return (
    <details className="item-panel">
      <summary>
        {icon && (
          <span className="item-icon" title={typeof icon === 'string' ? icon : undefined}>
            {icon}
          </span>
        )}
        <span className="item-title">
          <span className="item-name">{title}</span>
          {subtitle && <span className="item-brand">{subtitle}</span>}
        </span>
        {value !== undefined && (
          <span className={`item-price ${valueClass ?? ''}`}>{value}</span>
        )}
      </summary>
      <div className="item-detail">{children}</div>
    </details>
  );
}

/** Wraps a list of ItemPanels with the shared vertical rhythm. */
export function ItemPanels({ children }: { children: ReactNode }) {
  return <div className="item-panels">{children}</div>;
}