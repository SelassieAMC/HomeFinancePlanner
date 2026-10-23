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
      openingBalance: 'Opening balance',
      card: 'Card',
    },
    edit: {
      typeAria: 'Account type',
      balancePlaceholder: 'Opening balance ({{currency}})',
      cardDigitsPlaceholder: 'Card last digits (optional)',
    },
    actions: {
      add: 'Add',
      save: 'Save',
      cancel: 'Cancel',
      edit: 'Edit',
      delete: 'Delete account',
      reconcile: 'Reconcile',
    },
    dialogs: {
      reconcile: {
        title: 'Reconcile balance',
        hint: 'The app computed a balance of {{balance}} from your transactions. If the bank says something different, type its number — the opening balance is adjusted and the transactions stay untouched.',
        statedLabel: 'What the bank says ({{currency}})',
      },
    },
    errors: {
      invalidBalance: 'Enter a valid number.',
      invalidStartingBalance: 'Enter a valid starting balance.',
      reconcile: 'Reconciliation failed.',
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
      openingBalance: 'Anfangssaldo',
      card: 'Karte',
    },
    edit: {
      typeAria: 'Kontotyp',
      balancePlaceholder: 'Anfangssaldo ({{currency}})',
      cardDigitsPlaceholder: 'Letzte Kartenziffern (optional)',
    },
    actions: {
      add: 'Hinzufügen',
      save: 'Speichern',
      cancel: 'Abbrechen',
      edit: 'Bearbeiten',
      delete: 'Konto löschen',
      reconcile: 'Abgleichen',
    },
    dialogs: {
      reconcile: {
        title: 'Saldo abgleichen',
        hint: 'Die App hat aus deinen Buchungen einen Saldo von {{balance}} berechnet. Sagt die Bank etwas anderes, gib ihre Zahl ein — der Anfangssaldo wird angepasst, die Buchungen bleiben unberührt.',
        statedLabel: 'Was die Bank meldet ({{currency}})',
      },
    },
    errors: {
      invalidBalance: 'Gib eine gültige Zahl ein.',
      invalidStartingBalance: 'Gib einen gültigen Startsaldo ein.',
      reconcile: 'Abgleich fehlgeschlagen.',
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
      openingBalance: 'Saldo inicial',
      card: 'Tarjeta',
    },
    edit: {
      typeAria: 'Tipo de cuenta',
      balancePlaceholder: 'Saldo inicial ({{currency}})',
      cardDigitsPlaceholder: 'Últimos dígitos de la tarjeta (opcional)',
    },
    actions: {
      add: 'Añadir',
      save: 'Guardar',
      cancel: 'Cancelar',
      edit: 'Editar',
      delete: 'Eliminar cuenta',
      reconcile: 'Conciliar',
    },
    dialogs: {
      reconcile: {
        title: 'Conciliar saldo',
        hint: 'La app calculó un saldo de {{balance}} a partir de tus transacciones. Si el banco dice otra cosa, introduce su cifra: se ajusta el saldo inicial y las transacciones quedan intactas.',
        statedLabel: 'Lo que dice el banco ({{currency}})',
      },
    },
    errors: {
      invalidBalance: 'Introduce un número válido.',
      invalidStartingBalance: 'Introduce un saldo inicial válido.',
      reconcile: 'La conciliación falló.',
      create: 'No se pudo crear la cuenta.',
      update: 'No se pudo actualizar la cuenta.',
      delete: 'No se pudo eliminar la cuenta.',
    },
    empty: 'Aún no hay cuentas: añade la primera arriba.',
  },
};