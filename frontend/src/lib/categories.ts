import type { Category, CategoryKind } from '../types/domain';

// Categories of one family (product/expense), grouped by section for
// <select> optgroups. Categories without a section fall under 'Other'.
// The kind fallback mirrors the callers' defensive `?? kind` handling.
export function categoriesBySection(
  categories: Category[],
  kind: CategoryKind,
): [string, Category[]][] {
  const map = new Map<string, Category[]>();
  for (const c of categories) {
    if ((c.kind ?? kind) !== kind) continue;
    const key = c.section || 'Other';
    const list = map.get(key) ?? [];
    if (!map.has(key)) map.set(key, list);
    list.push(c);
  }
  return [...map.entries()];
}