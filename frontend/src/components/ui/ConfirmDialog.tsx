import type { ReactNode } from 'react';
import { Button } from './Button';
import { Dialog, ErrorMessage } from './Feedback';

/**
 * Standard destructive-action confirmation — one shape for every "are you
 * sure" in the app. The message names what a click destroys; the confirm
 * button is danger-filled by default (the caller can opt into a primary
 * confirm for decisions that are important but not destructive, e.g. applying
 * a scheduled movement). Cancel is always the ghost left-hand button, confirm
 * the right-hand one, so the pair reads identically across features.
 */
export function ConfirmDialog({
  title,
  message,
  confirmLabel,
  cancelLabel,
  busy = false,
  danger = true,
  error,
  onCancel,
  onConfirm,
}: {
  title: string;
  message: ReactNode;
  confirmLabel: string;
  cancelLabel: string;
  busy?: boolean;
  /** Confirm is destructive (red) unless the caller overrides. */
  danger?: boolean;
  /** Inline error keeps the dialog open and puts the message by the buttons. */
  error?: string | null;
  onCancel: () => void;
  onConfirm: () => void;
}) {
  return (
    <Dialog title={title} onClose={onCancel}>
      <p className="hint-text">{message}</p>
      {error && <ErrorMessage message={error} />}
      <div className="dialog-actions">
        <Button variant="ghost" onClick={onCancel} disabled={busy}>
          {cancelLabel}
        </Button>
        <Button variant={danger ? 'danger' : 'primary'} onClick={onConfirm} disabled={busy}>
          {confirmLabel}
        </Button>
      </div>
    </Dialog>
  );
}
