import { useEffect, useId, useRef, useState } from 'react';
import { categoriesBySection } from '../../lib/categories';
import type { Category, CategoryKind } from '../../types/domain';

interface CategorySelectProps {
  categories: Category[];
  /** Which category family to offer (product/expense). */
  kind: CategoryKind;
  value: number | null;
  onChange: (categoryId: number | null) => void;
  ariaLabel: string;
  /** Label of the "no category" choice (e.g. "All categories" for filters). */
  emptyLabel?: string;
}

/** One pickable row; a null id is the "no category" choice. */
interface CategoryRow {
  id: number | null;
  icon?: string | undefined;
  name: string;
  description?: string | undefined;
  /** Section shown as muted context (the old optgroup's information). */
  section?: string | undefined;
}

/**
 * Shared category dropdown with a search box: the options (grouped by
 * section, each with its emoji icon) are filterable by typing, so bills,
 * products and filters pick from the same list even as the taxonomy grows.
 * The "no category" choice is always the first row.
 */
export function CategorySelect({
  categories,
  kind,
  value,
  onChange,
  ariaLabel,
  emptyLabel = 'No category',
}: CategorySelectProps) {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState('');
  const [active, setActive] = useState(0);
  const rootRef = useRef<HTMLDivElement>(null);
  const listId = useId();

  const current = categories.find((c) => c.id === value) ?? null;

  // Close on click outside, like the product autocomplete.
  useEffect(() => {
    if (!open) return;
    const onPointerDown = (e: MouseEvent) => {
      if (rootRef.current && !rootRef.current.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener('mousedown', onPointerDown);
    return () => document.removeEventListener('mousedown', onPointerDown);
  }, [open]);

  function close() {
    setOpen(false);
    setQuery('');
    setActive(0);
  }

  function choose(row: CategoryRow) {
    onChange(row.id);
    close();
  }

  // Searching matches name, description and section, and flattens the
  // sections into one list (each row carries its section as context).
  const q = query.trim().toLowerCase();
  const rows: CategoryRow[] = [];
  if (!q || emptyLabel.toLowerCase().includes(q)) rows.push({ id: null, icon: '', name: emptyLabel });
  for (const [section, cats] of categoriesBySection(categories, kind)) {
    for (const c of cats) {
      if (q && !`${c.name} ${c.description ?? ''} ${section}`.toLowerCase().includes(q)) continue;
      rows.push({ id: c.id, icon: c.icon, name: c.name, description: c.description, section });
    }
  }
  const activeIndex = Math.min(active, Math.max(rows.length - 1, 0));

  function onSearchKeyDown(e: React.KeyboardEvent<HTMLInputElement>) {
    if (rows.length === 0) return;
    switch (e.key) {
      case 'ArrowDown':
        setActive((activeIndex + 1) % rows.length);
        e.preventDefault();
        break;
      case 'ArrowUp':
        setActive((activeIndex - 1 + rows.length) % rows.length);
        e.preventDefault();
        break;
      case 'Enter': {
        const row = rows[activeIndex];
        if (row) choose(row);
        e.preventDefault();
        break;
      }
      case 'Escape':
        close();
        e.preventDefault();
        break;
    }
  }

  return (
    <div className="category-select" ref={rootRef}>
      <button
        type="button"
        className="category-select-trigger"
        aria-haspopup="listbox"
        aria-expanded={open}
        aria-label={ariaLabel}
        onClick={() => (open ? close() : setOpen(true))}
        onKeyDown={(e) => {
          if (!open && (e.key === 'ArrowDown' || e.key === 'ArrowUp')) {
            setOpen(true);
            e.preventDefault();
          }
        }}
      >
        <span className="category-select-value">
          {current ? `${current.icon} ${current.name}`.trim() : emptyLabel}
        </span>
        <span className="category-select-caret" aria-hidden="true">
          ▾
        </span>
      </button>
      {open && (
        <div className="category-select-panel">
          <input
            className="category-select-search"
            autoFocus
            value={query}
            placeholder="Search categories…"
            aria-label={`Search, ${ariaLabel}`}
            role="combobox"
            aria-expanded
            aria-controls={listId}
            aria-activedescendant={rows.length > 0 ? `${listId}-${activeIndex}` : undefined}
            onChange={(e) => {
              setQuery(e.target.value);
              setActive(0);
            }}
            onKeyDown={onSearchKeyDown}
          />
          <ul id={listId} className="category-select-list" role="listbox">
            {rows.length === 0 ? (
              <li className="category-select-empty">No category matches “{query.trim()}”</li>
            ) : (
              rows.map((row, i) => (
                <li
                  key={row.id ?? 'none'}
                  id={`${listId}-${i}`}
                  role="option"
                  aria-selected={row.id === value}
                  className={`category-select-option${i === activeIndex ? ' is-active' : ''}${
                    row.id === value ? ' is-selected' : ''
                  }`}
                  // mousedown, not click: pick before the panel loses focus.
                  onMouseDown={(e) => {
                    e.preventDefault();
                    choose(row);
                  }}
                  onMouseEnter={() => setActive(i)}
                  title={row.description}
                >
                  <span className="category-select-name">
                    {row.icon ? `${row.icon} ${row.name}` : row.name}
                  </span>
                  {row.id !== null && row.section && (
                    <span className="category-select-meta">{row.section}</span>
                  )}
                </li>
              ))
            )}
          </ul>
        </div>
      )}
    </div>
  );
}