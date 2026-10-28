import { useEffect, useState } from 'react';
import { dollarsToCents } from '../../lib/money';

/**
 * Currency-aware money field: the user types freely (any decimal separator,
 * group separators — parseDecimalInput's rules), and on blur the value
 * COMMITS as cents and reformats to the field's currency decimals with
 * thousands grouping ("1234.5" → "1,234.50" — the app's fixed en-US money
 * rendering, like formatCents). The currency code rides as a suffix chip so
 * the field always states which currency an input price speaks. Invalid or
 * negative input reverts to the committed value instead of sticking.
 *
 * cents === null means "empty" — legitimate only on optional fields.
 */
export function MoneyInput({
  valueCents,
  currency,
  ariaLabel,
  placeholder,
  onCommit,
}: {
  valueCents: number | null;
  currency: string;
  ariaLabel: string;
  placeholder?: string;
  /** Commits the parsed cents, or null when the field was cleared. */
  onCommit: (cents: number | null) => void;
}) {
  const [text, setText] = useState(() => formatMoneyField(valueCents));
  const [focused, setFocused] = useState(false);

  // External commits (prefill, reset) reformat while the user is not typing.
  useEffect(() => {
    if (!focused) setText(formatMoneyField(valueCents));
  }, [valueCents, focused]);

  function handleBlur() {
    setFocused(false);
    const raw = text.trim();
    const cents = raw === '' ? null : dollarsToCents(raw);
    if (!Number.isFinite(cents) || (cents !== null && cents < 0)) {
      setText(formatMoneyField(valueCents));
      return;
    }
    if (cents !== valueCents) onCommit(cents);
    setText(formatMoneyField(cents));
  }

  return (
    <div className="money-input-row">
      <input
        inputMode="decimal"
        value={text}
        aria-label={ariaLabel}
        placeholder={placeholder}
        onFocus={() => setFocused(true)}
        onBlur={handleBlur}
        onChange={(e) => setText(e.target.value)}
      />
      <span className="money-currency">{currency}</span>
    </div>
  );
}

/** The field's money rendering: grouped, two decimals, no symbol — the
 *  currency chip carries which currency it is (matches formatCents's fixed
 *  en-US display convention). */
function formatMoneyField(cents: number | null): string {
  return cents === null
    ? ''
    : new Intl.NumberFormat('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 }).format(cents / 100);
}