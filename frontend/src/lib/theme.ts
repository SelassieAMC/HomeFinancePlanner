// Theme choice persisted per browser (hfp.theme): 'system' follows the OS
// preference and stays live via the matchMedia listener in useTheme; the
// resolved light/dark value is applied as `data-theme` on <html> so every
// token set in global.css flips at once. index.html runs the same resolve
// inline before the bundle loads, so the first paint is never the wrong
// theme (no FOUC) — these functions only keep it in sync afterwards.
export type ThemeChoice = 'system' | 'light' | 'dark';
export type ResolvedTheme = 'light' | 'dark';

export const THEME_STORAGE_KEY = 'hfp.theme';

const media = typeof window !== 'undefined' && 'matchMedia' in window
  ? window.matchMedia('(prefers-color-scheme: dark)')
  : null;

export function readStoredChoice(): ThemeChoice {
  if (typeof window === 'undefined') return 'system';
  const raw = window.localStorage.getItem(THEME_STORAGE_KEY);
  return raw === 'light' || raw === 'dark' || raw === 'system' ? raw : 'system';
}

export function resolve(choice: ThemeChoice): ResolvedTheme {
  if (choice !== 'system') return choice;
  return media?.matches ? 'dark' : 'light';
}

export function apply(choice: ThemeChoice): void {
  const resolved = resolve(choice);
  document.documentElement.dataset.theme = resolved;
  document.documentElement.style.colorScheme = resolved;
  // The browser paints the OS chrome / scrollbars from this.
  const meta = document.querySelector<HTMLMetaElement>('meta[name="theme-color"]');
  if (meta) meta.content = resolved === 'dark' ? '#12161d' : '#f5f6f8';
}

export function applyStored(): void {
  apply(readStoredChoice());
}

export function persist(choice: ThemeChoice): void {
  if (choice === 'system') window.localStorage.removeItem(THEME_STORAGE_KEY);
  else window.localStorage.setItem(THEME_STORAGE_KEY, choice);
  apply(choice);
}