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

/**
 * Shared category dropdown: options grouped by section, each showing the
 * category's emoji icon so bills and products pick from the same list.
 */
export function CategorySelect({
  categories,
  kind,
  value,
  onChange,
  ariaLabel,
  emptyLabel = 'No category',
}: CategorySelectProps) {
  return (
    <select
      value={value ?? ''}
      aria-label={ariaLabel}
      onChange={(e) => onChange(e.target.value ? Number(e.target.value) : null)}
    >
      <option value="">{emptyLabel}</option>
      {categoriesBySection(categories, kind).map(([section, cats]) => (
        <optgroup key={section} label={section}>
          {cats.map((c) => (
            <option key={c.id} value={c.id} title={c.description}>
              {c.icon} {c.name}
            </option>
          ))}
        </optgroup>
      ))}
    </select>
  );
}