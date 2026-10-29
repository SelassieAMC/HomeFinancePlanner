import type { ReactNode } from 'react';
import { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { IconHelp } from './IconButton';

/**
 * The help hint: a question-circle trigger whose explanation opens as a
 * floating tooltip — ONLY on click (2026-10-29 user decision: no hover
 * reveal, and the note must never push form content around). Closes on a
 * second click, an outside click, or Escape. Escape is swallowed here
 * (window capture listener) so an open hint inside a Dialog closes only the
 * hint, never the dialog beneath it.
 */
export function HelpHint({ note }: { note: ReactNode }) {
  const { t } = useTranslation('common');
  const [open, setOpen] = useState(false);
  const wrapRef = useRef<HTMLSpanElement>(null);

  useEffect(() => {
    if (!open) return;
    const onDown = (e: MouseEvent) => {
      if (wrapRef.current && !wrapRef.current.contains(e.target as Node)) setOpen(false);
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        setOpen(false);
        e.stopPropagation();
      }
    };
    document.addEventListener('mousedown', onDown);
    window.addEventListener('keydown', onKey, true);
    return () => {
      document.removeEventListener('mousedown', onDown);
      window.removeEventListener('keydown', onKey, true);
    };
  }, [open]);

  return (
    <span className="help-wrap" data-open={open ? '' : undefined} ref={wrapRef}>
      <button
        type="button"
        className="field-help"
        aria-label={t('help')}
        aria-expanded={open}
        onClick={() => setOpen((o) => !o)}
      >
        <IconHelp />
      </button>
      {open && (
        <span className="help-pop" role="status">
          {note}
        </span>
      )}
    </span>
  );
}

/** Block wrapper: existing head content + hint trigger (children may hold a
 *  control, e.g. the auto-apply checkbox). Without children it renders the
 *  bare trigger — for page/tab intros that have no heading of their own. */
export function HelpNote({ note, children }: { note: ReactNode; children?: ReactNode }) {
  if (!children) return <HelpHint note={note} />;
  return (
    <div className="help-note-head">
      {children}
      <HelpHint note={note} />
    </div>
  );
}

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
 * Modal dialog. `onClose` wires outside dismissal: clicking the backdrop (only
 * the backdrop — content clicks never leak through) and pressing Escape close
 * the dialog on the caller's behalf, exactly like the receipt zoom viewer.
 * Every dialog in the app is closable that way; without `onClose` the backdrop
 * is visual only. `wide` widens the box for forms that need more room.
 */
// Escape closes only the TOPMOST open dialog: nested dialogs (e.g. the merge
// dialog inside the product edit dialog) close inner-first. The stack holds
// mount-stable tokens; the effect runs once at mount so parent re-renders
// (new inline onClose identities) never reshuffle the z-order.
const escapeStack: object[] = [];

export function Dialog({
  title,
  help,
  children,
  wide = false,
  onClose,
}: {
  title: string;
  /** Optional explainer text behind a "?" beside the dialog title (the help
   *  reveal) — for how-it-works context that would clutter the decision
   *  form. Confirmation/decision text stays visible in the body. */
  help?: ReactNode;
  children: ReactNode;
  wide?: boolean;
  onClose?: () => void;
}) {
  // Stable per-mounted-dialog token + ref so effect deps don't churn with the
  // caller's inline arrow functions.
  const [token] = useState(() => ({}));
  const closeRef = useRef(onClose);
  closeRef.current = onClose;

  useEffect(() => {
    if (!onClose) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && escapeStack[escapeStack.length - 1] === token) {
        e.stopPropagation();
        closeRef.current?.();
      }
    };
    escapeStack.push(token);
    window.addEventListener('keydown', onKey, true);
    return () => {
      const i = escapeStack.indexOf(token);
      if (i >= 0) escapeStack.splice(i, 1);
      window.removeEventListener('keydown', onKey, true);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  return (
    <div
      className="dialog-backdrop"
      onClick={
        onClose &&
        ((e) => {
          if (e.target === e.currentTarget) onClose();
        })
      }
    >
      <div
        className={wide ? 'dialog dialog-wide' : 'dialog'}
        role="dialog"
        aria-modal="true"
        aria-labelledby="dialog-title"
      >
        <h3 className="dialog-title" id="dialog-title">
          {title}
          {help != null && <HelpHint note={help} />}
        </h3>
        {children}
      </div>
    </div>
  );
}