// i18n entry point: folds every namespace bundle into i18next resources and
// initializes the instance. Initialized at import time (main.tsx imports this
// before rendering); AuthProvider switches the active language to the logged
// in user's stored locale.
import i18next, { type Resource } from 'i18next';
import { initReactI18next } from 'react-i18next';

import { common } from './locales/common';
import { auth } from './locales/auth';
import { settings } from './locales/settings';
import { dashboard } from './locales/dashboard';
import { accounts } from './locales/accounts';
import { transactions } from './locales/transactions';
import { bills } from './locales/bills';
import { budgets } from './locales/budgets';
import { goals } from './locales/goals';
import { bags } from './locales/bags';
import { planning } from './locales/planning';
import { schedules } from './locales/schedules';
import { products } from './locales/products';
import { stores } from './locales/stores';
import { cart } from './locales/cart';
import { prompts } from './locales/prompts';
import { DEFAULT_LOCALE, type LocaleBundle } from './core';

// One entry per namespace file — a namespace is translated as a unit by one
// feature folder's components.
const NAMESPACES: Record<string, LocaleBundle> = {
  common,
  auth,
  settings,
  dashboard,
  accounts,
  transactions,
  bills,
  budgets,
  goals,
  bags,
  planning,
  schedules,
  products,
  stores,
  cart,
  prompts,
};

const resources: Resource = {};
for (const [ns, bundle] of Object.entries(NAMESPACES)) {
  for (const lng of ['en', 'de', 'es'] as const) {
    (resources[lng] ??= {})[ns] = bundle[lng];
  }
}

i18next.use(initReactI18next).init({
  resources,
  // The default is english; the logged-in user's stored locale (users.locale)
  // takes over in AuthContext. No browser sniffing: surprising language flips
  // on the login screen were worse than a plain, predictable default.
  lng: DEFAULT_LOCALE,
  fallbackLng: 'en',
  interpolation: { escapeValue: false }, // React escapes on its own
  returnEmptyString: false,
  // All resources are bundled inline, so nothing ever needs suspending.
  // The default `useSuspense: true` suspends every useTranslation consumer on
  // a language change and, with no <Suspense> boundary in the app tree, that
  // intermittently wedged the UI (frozen renders, stray wrong-language
  // screens) until a manual reload — hence explicitly off.
  react: { useSuspense: false },
});

export default i18next;