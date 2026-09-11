// Selectable currencies for accounts, bills and the base display currency.
// The set deliberately matches the Frankfurter/ECB reference table so
// anything the user can pick is convertible by the backend.

export interface CurrencyOption {
  code: string;
  label: string;
}

export const COMMON_CURRENCIES: CurrencyOption[] = [
  { code: 'EUR', label: 'Euro' },
  { code: 'USD', label: 'US Dollar' },
  { code: 'GBP', label: 'British Pound' },
  { code: 'CHF', label: 'Swiss Franc' },
  { code: 'JPY', label: 'Japanese Yen' },
  { code: 'SEK', label: 'Swedish Krona' },
  { code: 'NOK', label: 'Norwegian Krone' },
  { code: 'DKK', label: 'Danish Krone' },
  { code: 'PLN', label: 'Polish Złoty' },
  { code: 'CZK', label: 'Czech Koruna' },
  { code: 'HUF', label: 'Hungarian Forint' },
  { code: 'RON', label: 'Romanian Leu' },
  { code: 'BGN', label: 'Bulgarian Lev' },
  { code: 'ISK', label: 'Icelandic Króna' },
  { code: 'TRY', label: 'Turkish Lira' },
  { code: 'CAD', label: 'Canadian Dollar' },
  { code: 'AUD', label: 'Australian Dollar' },
  { code: 'NZD', label: 'New Zealand Dollar' },
  { code: 'CNY', label: 'Chinese Yuan' },
  { code: 'HKD', label: 'Hong Kong Dollar' },
  { code: 'SGD', label: 'Singapore Dollar' },
  { code: 'MXN', label: 'Mexican Peso' },
  { code: 'BRL', label: 'Brazilian Real' },
  { code: 'ZAR', label: 'South African Rand' },
  { code: 'INR', label: 'Indian Rupee' },
  { code: 'KRW', label: 'South Korean Won' },
  { code: 'IDR', label: 'Indonesian Rupiah' },
  { code: 'MYR', label: 'Malaysian Ringgit' },
  { code: 'PHP', label: 'Philippine Peso' },
  { code: 'THB', label: 'Thai Baht' },
  { code: 'ILS', label: 'Israeli Shekel' },
];

/** "EUR — Euro" for select options; falls back to the raw code. */
export function currencyLabel(code: string): string {
  const found = COMMON_CURRENCIES.find((c) => c.code === code);
  return found ? `${found.code} — ${found.label}` : code;
}