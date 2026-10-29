import type { ButtonHTMLAttributes, ReactNode } from 'react';

/**
 * Small inline SVG icon set — hand-rolled 24-basis stroke paths (Feather-like
 * geometry), rendered at 16px so rows stay tight. Icons carry no meaning on
 * their own: every IconButton also sets title + aria-label, so these remain
 * decorative (aria-hidden) and the string is the source of truth.
 */
function Svg({ children }: { children: ReactNode }) {
  return (
    <svg
      width="16"
      height="16"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.8"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      focusable="false"
    >
      {children}
    </svg>
  );
}

export function IconEdit() {
  return (
    <Svg>
      <path d="M17 3a2.828 2.828 0 1 1 4 4L7.5 20.5 2 22l1.5-5.5L17 3z" />
    </Svg>
  );
}

/** Details: circle with an "i" — opens the read-everything modal for a row. */
export function IconInfo() {
  return (
    <Svg>
      <circle cx="12" cy="12" r="9.5" />
      <path d="M12 11v5" />
      <path d="M12 8v.5" />
    </Svg>
  );
}

/** Help: circle with a "?" — the field-hint reveal trigger (hover/tap). */
export function IconHelp() {
  return (
    <Svg>
      <circle cx="12" cy="12" r="9.5" />
      <path d="M9.4 9.3a2.6 2.6 0 1 1 3.6 2.4c-.7.3-1 .8-1 1.6" />
      <path d="M12 16.7v.4" />
    </Svg>
  );
}

export function IconTrash() {
  return (
    <Svg>
      <path d="M3 6h18" />
      <path d="M8 6V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2" />
      <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6" />
      <path d="M10 11v6" />
      <path d="M14 11v6" />
    </Svg>
  );
}

export function IconArchive() {
  return (
    <Svg>
      <path d="M21 8v13H3V8" />
      <path d="M1 3h22v5H1z" />
      <path d="M10 12h4" />
    </Svg>
  );
}

/** Fund a bag: money into the reserve (arrow down into a tray). */
export function IconFund() {
  return (
    <Svg>
      <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4" />
      <path d="M7 10l5 5 5-5" />
      <path d="M12 15V3" />
    </Svg>
  );
}

/** Withdraw from a bag: release back out (arrow up out of a tray). */
export function IconWithdraw() {
  return (
    <Svg>
      <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4" />
      <path d="M17 8l-5-5-5 5" />
      <path d="M12 3v12" />
    </Svg>
  );
}

/** Move money between bags (rebalance with a preselected source). */
export function IconMoveBetween() {
  return (
    <Svg>
      <path d="M16 3h5v5" />
      <path d="M4 20L21 3" />
      <path d="M21 16v5h-5" />
      <path d="M15 15l6 6" />
      <path d="M4 4l5 5" />
    </Svg>
  );
}

/** Adjust — an explicit ledger correction (three sliders). */
export function IconSliders() {
  return (
    <Svg>
      <path d="M4 21v-7" />
      <path d="M4 10V3" />
      <path d="M12 21v-9" />
      <path d="M12 8V3" />
      <path d="M20 21v-5" />
      <path d="M20 12V3" />
      <path d="M1 14h6" />
      <path d="M9 8h6" />
      <path d="M17 16h6" />
    </Svg>
  );
}

/** Ledger history (a lined list). */
export function IconLedger() {
  return (
    <Svg>
      <path d="M8 6h13" />
      <path d="M8 12h13" />
      <path d="M8 18h13" />
      <path d="M3 6h.01" />
      <path d="M3 12h.01" />
      <path d="M3 18h.01" />
    </Svg>
  );
}

/** Close: stop accepting spend / mark finished. */
export function IconCloseCircle() {
  return (
    <Svg>
      <circle cx="12" cy="12" r="10" />
      <path d="M8 12h8" />
    </Svg>
  );
}

/** Reopen / redo. */
export function IconReopen() {
  return (
    <Svg>
      <path d="M1 4v6h6" />
      <path d="M3.51 15a9 9 0 1 0 2.13-9.36L1 10" />
    </Svg>
  );
}

/** Mark as done / finished. */
export function IconCheck() {
  return (
    <Svg>
      <path d="M20 6L9 17l-5-5" />
    </Svg>
  );
}

/** Month navigation — previous. */
export function IconChevronLeft() {
  return (
    <Svg>
      <path d="M15 18l-6-6 6-6" />
    </Svg>
  );
}

/** Month navigation — next. */
export function IconChevronRight() {
  return (
    <Svg>
      <path d="M9 18l6-6-6-6" />
    </Svg>
  );
}

/**
 * Icon-only action button: the `label` is both the accessible name
 * (aria-label) and the visible tooltip (data-tooltip, styled by
 * .icon-btn::after — not the browser's delayed native title, which reads as
 * missing). The icon itself is decorative; the label is the source of truth.
 * Danger variant tints the hover.
 */
interface IconButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  icon: ReactNode;
  label: string;
  variant?: 'default' | 'danger';
}

export function IconButton({
  icon,
  label,
  variant = 'default',
  className = '',
  ...rest
}: IconButtonProps) {
  return (
    <button
      type="button"
      className={`icon-btn${variant === 'danger' ? ' icon-btn-danger' : ''} ${className}`}
      data-tooltip={label}
      aria-label={label}
      {...rest}
    >
      {icon}
    </button>
  );
}