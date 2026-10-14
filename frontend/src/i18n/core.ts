// i18n core: the closed locale set every namespace bundle must ship, and the
// helpers that map i18next's language back onto Intl tags for dates/months.
// Keep in sync with backend/internal/domain.ValidLocales (en, de, es).

export const DEFAULT_LOCALE = 'en' as const;

export const SUPPORTED_LOCALES = ['en', 'de', 'es'] as const;

export type LocaleCode = (typeof SUPPORTED_LOCALES)[number];

export const LOCALE_LABELS: Record<LocaleCode, string> = {
  en: 'English',
  de: 'Deutsch',
  es: 'Español',
};

export function isSupportedLocale(locale: string): locale is LocaleCode {
  return (SUPPORTED_LOCALES as readonly string[]).includes(locale);
}

/** A per-namespace translation bundle: one nested object per language. Keys
 *  must match across all three (a build-time script checks parity). */
export interface LocaleBundle {
  en: Record<string, unknown>;
  de: Record<string, unknown>;
  es: Record<string, unknown>;
}

/** BCP-47 tag for Intl calls (months, weekdays, date ranges) of the active
 *  i18next language. Falls back to the default tag for anything unknown. */
export function intlLocale(language: string): string {
  switch (language) {
    case 'de':
      return 'de-DE';
    case 'es':
      return 'es-ES';
    default:
      return 'en-US';
  }
}

/** Pre-login default: the browser language when it is one we ship, else 'en'.
 *  After login the user's stored locale (users.locale) always wins. */
export function detectPreLoginLocale(): LocaleCode {
  for (const candidate of navigator.languages ?? []) {
    const base = candidate.split('-')[0]?.toLowerCase();
    if (isSupportedLocale(base ?? '')) return base as LocaleCode;
  }
  return DEFAULT_LOCALE;
}