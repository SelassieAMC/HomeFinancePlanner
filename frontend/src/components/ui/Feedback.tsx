import type { ReactNode } from 'react';
import { useTranslation } from 'react-i18next';

// The default loading label resolves at render time so the spinner text
// follows the active language; callers may still override `label`.
export function Spinner({ label }: { label?: string }) {
  const { t } = useTranslation('common');
  return (
    <div className="spinner-wrap" role="status" aria-live="polite">
      <span className="spinner" aria-hidden="true" />
      <span>{label ?? t('loading')}</span>
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
 * OK/confirm button the caller puts in the content. `wide` widens the box for
 * forms that need more room than a confirmation.
 */
export function Dialog({
  title,
  children,
  wide = false,
}: {
  title: string;
  children: ReactNode;
  wide?: boolean;
}) {
  return (
    <div className="dialog-backdrop">
      <div
        className={wide ? 'dialog dialog-wide' : 'dialog'}
        role="dialog"
        aria-modal="true"
        aria-labelledby="dialog-title"
      >
        <h3 className="dialog-title" id="dialog-title">
          {title}
        </h3>
        {children}
      </div>
    </div>
  );
}