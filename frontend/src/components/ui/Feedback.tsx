import type { ReactNode } from 'react';

export function Spinner({ label = 'Loading…' }: { label?: string }) {
  return (
    <div className="spinner-wrap" role="status" aria-live="polite">
      <span className="spinner" aria-hidden="true" />
      <span>{label}</span>
    </div>
  );
}

export function ErrorMessage({ message }: { message: string }) {
  return (
    <div className="error-message" role="alert">
      {message}
    </div>
  );
}

export function EmptyState({ message }: { message: string }) {
  return <div className="empty-state">{message}</div>;
}

/**
 * Modal dialog. The backdrop is visual only — dismissal is wired to whatever
 * OK/confirm button the caller puts in the content.
 */
export function Dialog({ title, children }: { title: string; children: ReactNode }) {
  return (
    <div className="dialog-backdrop">
      <div className="dialog" role="dialog" aria-modal="true" aria-labelledby="dialog-title">
        <h3 className="dialog-title" id="dialog-title">
          {title}
        </h3>
        {children}
      </div>
    </div>
  );
}