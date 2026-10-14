// Namespace: auth. Translated by the auth feature(s) — keys must exist in all
// three languages (parity is checked by tools/check-i18n.mjs).
import type { LocaleBundle } from '../core';

export const auth: LocaleBundle = {
  en: {
    title: 'Home Finance Planner',
    subtitle: 'Who is planning?',
    user_label: 'User',
    password_label: 'Password',
    keep_logged_in: 'Keep me logged in',
    sign_in: 'Sign in',
    signing_in: 'Signing in…',
    login_failed: 'Login failed.',
    load_users_error: 'Could not load users: {{message}}',
  },
  de: {
    title: 'Home Finance Planner',
    subtitle: 'Wer plant?',
    user_label: 'Benutzer',
    password_label: 'Passwort',
    keep_logged_in: 'Angemeldet bleiben',
    sign_in: 'Anmelden',
    signing_in: 'Anmeldung läuft…',
    login_failed: 'Anmeldung fehlgeschlagen.',
    load_users_error: 'Benutzer konnten nicht geladen werden: {{message}}',
  },
  es: {
    title: 'Home Finance Planner',
    subtitle: '¿Quién planifica?',
    user_label: 'Usuario',
    password_label: 'Contraseña',
    keep_logged_in: 'Mantener la sesión iniciada',
    sign_in: 'Iniciar sesión',
    signing_in: 'Iniciando sesión…',
    login_failed: 'Error al iniciar sesión.',
    load_users_error: 'No se pudieron cargar los usuarios: {{message}}',
  },
};