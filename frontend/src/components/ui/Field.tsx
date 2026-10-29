import type { ReactNode } from 'react';
import { HelpHint } from './Feedback';

// Form-field wrapper: visible label + optional hint + optional error, built
// on the repo's wrapping-label pattern (label > control, the association CSS
// and screen readers trust everywhere else). Exists so a labeled field is the
// path of least resistance: raw <input>/<select> allowed forms to ship fields
// whose meaning lived only in placeholder or truncated dropdown text.
// - label is REQUIRED — a field without a discernible name is a bug, and the
//   component visually + structurally cannot be written without one.
// - hint renders as a floating tooltip behind the "?" trigger inside the
//   label (click only, never hover; the note never pushes content —
//   2026-10-28 the always-visible line made every create-form noisy,
//   2026-10-29 the inline expansion did too).
// - error renders below the control in the negative color and the wrapper
//   takes .field-error for styling hooks.
// - The control itself is passed as children; Field adds no input styling of
//   its own (input/select keep their existing rules).
// - 'row' layout keeps label and control on one line (filter rows, compact
//   toolbar fields); the default stacks them (forms and dialogs).
export function Field({
  label,
  hint,
  error,
  layout = 'stack',
  className,
  children,
}: {
  label: ReactNode;
  hint?: ReactNode;
  error?: ReactNode;
  layout?: 'stack' | 'row';
  className?: string;
  children: ReactNode;
}) {
  const cls = [
    'field',
    layout === 'row' ? 'field-row' : null,
    error ? 'field-error' : null,
    className,
  ]
    .filter(Boolean)
    .join(' ');
  return (
    <div className={cls}>
      <label>
        <span className="field-label">
          {label}
          {hint != null && <HelpHint note={hint} />}
        </span>
        {children}
      </label>
      {error != null && <p className="field-msg">{error}</p>}
    </div>
  );
}