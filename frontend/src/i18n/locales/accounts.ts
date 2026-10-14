// Namespace: accounts. Translated by the accounts feature(s) — keys must exist in all
// three languages (parity is checked by tools/check-i18n.mjs).
import type { LocaleBundle } from '../core';

export const accounts: LocaleBundle = {
  en: {
    title: 'Accounts',
    types: {
      checking: 'Checking',
      savings: 'Savings',
      credit: 'Credit',
      cash: 'Cash',
      other: 'Other',
    },
    add: {
      title: 'Add account',
      namePlaceholder: 'Name',
      currencyAria: 'Account currency',
      balancePlaceholder: 'Starting balance ({{currency}})',
      submit: 'Add',
    },
    labels: {
      type: 'Type',
      currency: 'Currency',
      balance: 'Balance',
      card: 'Card',
    },
    edit: {
      typeAria: 'Account type',
      balancePlaceholder: 'Balance ({{currency}})',
      cardDigitsPlaceholder: 'Card last digits (optional)',
    },
    actions: {
      add: 'Add',
      save: 'Save',
      cancel: 'Cancel',
      edit: 'Edit',
      delete: 'Delete account',
    },
    errors: {
      invalidBalance: 'Enter a valid non-negative balance.',
      invalidStartingBalance: 'Enter a valid non-negative starting balance.',
      create: 'Failed to create account.',
      update: 'Failed to update account.',
      delete: 'Failed to delete account.',
    },
    empty: 'No accounts yet — add your first one above.',
  },
  de: {
    title: 'Konten',
    types: {
      checking: 'Girokonto',
      savings: 'Sparkonto',
      credit: 'Kredit',
      cash: 'Bargeld',
      other: 'Sonstiges',
    },
    add: {
      title: 'Konto hinzufügen',
      namePlaceholder: 'Name',
      currencyAria: 'Kontowährung',
      balancePlaceholder: 'Startsaldo ({{currency}})',
      submit: 'Hinzufügen',
    },
    labels: {
      type: 'Typ',
      currency: 'Währung',
      balance: 'Saldo',
      card: 'Karte',
    },
    edit: {
      typeAria: 'Kontotyp',
      balancePlaceholder: 'Saldo ({{currency}})',
      cardDigitsPlaceholder: 'Letzte Kartenziffern (optional)',
    },
    actions: {
      add: 'Hinzufügen',
      save: 'Speichern',
      cancel: 'Abbrechen',
      edit: 'Bearbeiten',
      delete: 'Konto löschen',
    },
    errors: {
      invalidBalance: 'Gib einen gültigen, nicht negativen Saldo ein.',
      invalidStartingBalance: 'Gib einen gültigen, nicht negativen Startsaldo ein.',
      create: 'Konto konnte nicht angelegt werden.',
      update: 'Konto konnte nicht aktualisiert werden.',
      delete: 'Konto konnte nicht gelöscht werden.',
    },
    empty: 'Noch keine Konten — lege oben dein erstes Konto an.',
  },
  es: {
    title: 'Cuentas',
    types: {
      checking: 'Corriente',
      savings: 'Ahorro',
      credit: 'Crédito',
      cash: 'Efectivo',
      other: 'Otra',
    },
    add: {
      title: 'Añadir cuenta',
      namePlaceholder: 'Nombre',
      currencyAria: 'Moneda de la cuenta',
      balancePlaceholder: 'Saldo inicial ({{currency}})',
      submit: 'Añadir',
    },
    labels: {
      type: 'Tipo',
      currency: 'Moneda',
      balance: 'Saldo',
      card: 'Tarjeta',
    },
    edit: {
      typeAria: 'Tipo de cuenta',
      balancePlaceholder: 'Saldo ({{currency}})',
      cardDigitsPlaceholder: 'Últimos dígitos de la tarjeta (opcional)',
    },
    actions: {
      add: 'Añadir',
      save: 'Guardar',
      cancel: 'Cancelar',
      edit: 'Editar',
      delete: 'Eliminar cuenta',
    },
    errors: {
      invalidBalance: 'Introduce un saldo válido, no negativo.',
      invalidStartingBalance: 'Introduce un saldo inicial válido, no negativo.',
      create: 'No se pudo crear la cuenta.',
      update: 'No se pudo actualizar la cuenta.',
      delete: 'No se pudo eliminar la cuenta.',
    },
    empty: 'Aún no hay cuentas: añade la primera arriba.',
  },
};