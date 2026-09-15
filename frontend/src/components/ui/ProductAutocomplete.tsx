import { useEffect, useRef, useState } from 'react';
import { productsApi } from '../../api/products';
import { formatCents } from '../../lib/money';
import type { Product } from '../../types/domain';

// ProductAutocomplete is a name input with server-side matching suggestions:
// typing queries the product catalogue (debounced, in-flight requests
// cancelled) and the user picks an existing product or keeps the typed name —
// unknown names are find-or-created server-side on save, like the bill flow.
interface Props {
  /** The typed name (controlled). */
  value: string;
  onValueChange: (name: string) => void;
  /** Called on Enter/click of a suggestion. Picking an existing product
   *  canonicalizes casing; `null` keeps the typed name (auto-created). */
  onPick: (product: Product | null) => void;
  /** Currency the suggestions' latest prices are formatted in. */
  currency?: string;
  placeholder?: string;
  ariaLabel: string;
}

const DEBOUNCE_MS = 300;
const SUGGEST_LIMIT = 8;

export function ProductAutocomplete({
  value,
  onValueChange,
  onPick,
  currency = '',
  placeholder,
  ariaLabel,
}: Props) {
  const [matches, setMatches] = useState<Product[]>([]);
  const [open, setOpen] = useState(false);
  const [active, setActive] = useState(0);
  const abortRef = useRef<AbortController | null>(null);
  const rootRef = useRef<HTMLDivElement>(null);

  // Debounced catalogue search, cancelling the previous request each time.
  useEffect(() => {
    const query = value.trim();
    if (query === '') {
      setMatches([]);
      setOpen(false);
      return;
    }
    const timer = setTimeout(() => {
      abortRef.current?.abort();
      const controller = new AbortController();
      abortRef.current = controller;
      productsApi
        .list({ name: query, limit: SUGGEST_LIMIT }, { signal: controller.signal })
        .then((page) => {
          if (controller.signal.aborted) return;
          setMatches(page.items);
          setOpen(true);
          setActive(0);
        })
        .catch((err: unknown) => {
          // Aborted keystrokes are expected; surface nothing.
          if (err instanceof DOMException && err.name === 'AbortError') return;
          setMatches([]);
          setOpen(false);
        });
    }, DEBOUNCE_MS);
    return () => clearTimeout(timer);
  }, [value]);

  // Cancel the in-flight request and close on unmount / click outside.
  useEffect(() => {
    const onClick = (e: MouseEvent) => {
      if (rootRef.current && !rootRef.current.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener('mousedown', onClick);
    return () => {
      document.removeEventListener('mousedown', onClick);
      abortRef.current?.abort();
    };
  }, []);

  const pick = (p: Product | null) => {
    setOpen(false);
    if (p) onValueChange(p.name);
    onPick(p);
  };

  const onKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (!open || matches.length === 0) {
      if (e.key === 'Enter') {
        // No suggestions to pick — keep the typed name (server find-or-creates).
        if (value.trim() !== '') {
          pick(null);
          e.preventDefault();
        }
        return;
      }
      return;
    }
    switch (e.key) {
      case 'ArrowDown':
        setActive((a) => (a + 1) % matches.length);
        e.preventDefault();
        break;
      case 'ArrowUp':
        setActive((a) => (a - 1 + matches.length) % matches.length);
        e.preventDefault();
        break;
      case 'Enter':
        pick(matches[active] ?? null);
        e.preventDefault();
        break;
      case 'Escape':
        // Close and keep the typed text — the server find-or-creates it.
        setOpen(false);
        e.preventDefault();
        break;
    }
  };

  const showList = open && value.trim() !== '';

  return (
    <div className="product-autocomplete" ref={rootRef}>
      <input
        className="product-autocomplete-input"
        type="text"
        value={value}
        placeholder={placeholder}
        aria-label={ariaLabel}
        role="combobox"
        aria-expanded={showList}
        aria-controls="product-autocomplete-list"
        aria-autocomplete="list"
        aria-activedescendant={showList && matches.length > 0 ? `pac-${active}` : undefined}
        autoComplete="off"
        onChange={(e) => {
          onValueChange(e.target.value);
          setOpen(true);
        }}
        onFocus={() => value.trim() !== '' && setOpen(true)}
        onKeyDown={onKeyDown}
      />
      {showList && (
        <ul
          id="product-autocomplete-list"
          className="product-autocomplete-list"
          role="listbox"
        >
          {matches.length === 0 ? (
            <li className="product-autocomplete-empty">
              No match — “{value.trim()}” is created on save
            </li>
          ) : (
            matches.map((p, i) => (
              <li
                key={p.id}
                id={`pac-${i}`}
                role="option"
                aria-selected={i === active}
                className={`product-autocomplete-option${i === active ? ' is-active' : ''}`}
                onMouseDown={(e) => {
                  // mousedown, not click: commit before the input's blur.
                  e.preventDefault();
                  pick(p);
                }}
                onMouseEnter={() => setActive(i)}
              >
                <span className="product-autocomplete-name">{p.name}</span>
                {p.brand && <span className="product-autocomplete-brand">{p.brand}</span>}
                <span className="product-autocomplete-meta">
                  {p.times_bought > 0 && currency
                    ? `${p.times_bought}× · ${formatCents(p.latest_price_cents ?? 0, p.price_currency || currency)}`
                    : p.times_bought > 0
                      ? `${p.times_bought}× bought`
                      : 'new'}
                </span>
              </li>
            ))
          )}
        </ul>
      )}
    </div>
  );
}