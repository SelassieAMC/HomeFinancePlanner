// Namespace: goals. Translated by the goals feature(s) — keys must exist in all
// three languages (parity is checked by tools/check-i18n.mjs).
import type { LocaleBundle } from '../core';

export const goals: LocaleBundle = {
  en: {
    title: 'Goals',
    add: {
      title: 'Add goal',
      name_placeholder: 'Goal name',
      name_aria: 'Goal name',
      amount_placeholder: 'Target amount',
      account_aria: 'Tracked account',
      account_empty: 'Tracked account…',
      date_aria: 'Target date (optional)',
      submit: 'Add',
    },
    hints: {
      progress:
        "Progress is the tracked account's current balance — contribute with a transfer into it. The currency follows that account.",
      target_date:
        "The target date is a milestone you're aiming for (\"by when\") — purely informational: nothing is enforced or reminded by it.",
    },
    empty: 'No goals yet — add your first one above.',
    card: {
      reached: 'Goal reached',
      to_go: '{{amount}} to go',
      to_go_by: '{{amount}} to go · by {{date}}',
      edit: 'Edit',
      delete: 'Delete',
    },
    edit: {
      title: 'Edit goal',
      name: 'Name',
      target_amount: 'Target amount ({{currency}})',
      account: 'Tracked account',
      date: 'Target date (optional)',
      cancel: 'Cancel',
      save: 'Save changes',
      saving: 'Saving…',
    },
    delete: {
      title: 'Delete goal?',
      body: 'This permanently removes “{{name}}”. The tracked account and its balances are untouched. This cannot be undone.',
      cancel: 'Cancel',
      confirm: 'Delete',
      deleting: 'Deleting…',
    },
    validation: {
      positive_amount: 'Enter a positive target amount.',
      choose_account: 'Choose the account the goal tracks.',
    },
    errors: {
      create: 'Failed to create goal.',
      delete: 'Failed to delete goal.',
      save: 'Failed to save the goal.',
    },
  },
  de: {
    title: 'Ziele',
    add: {
      title: 'Ziel anlegen',
      name_placeholder: 'Zielname',
      name_aria: 'Zielname',
      amount_placeholder: 'Zielbetrag',
      account_aria: 'Beobachtetes Konto',
      account_empty: 'Beobachtetes Konto…',
      date_aria: 'Zieldatum (optional)',
      submit: 'Hinzufügen',
    },
    hints: {
      progress:
        'Der Fortschritt ist der aktuelle Kontostand des beobachteten Kontos – einzahlen mit einer Überweisung auf es. Die Währung folgt dem Konto.',
      target_date:
        'Das Zieldatum ist eine Markierung, auf die du hinarbeitest („bis wann“) – rein informativ: nichts wird dadurch erzwungen oder erinnert.',
    },
    empty: 'Noch keine Ziele – lege dein erstes oben an.',
    card: {
      reached: 'Ziel erreicht',
      to_go: '{{amount}} offen',
      to_go_by: '{{amount}} offen · bis {{date}}',
      edit: 'Bearbeiten',
      delete: 'Löschen',
    },
    edit: {
      title: 'Ziel bearbeiten',
      name: 'Name',
      target_amount: 'Zielbetrag ({{currency}})',
      account: 'Beobachtetes Konto',
      date: 'Zieldatum (optional)',
      cancel: 'Abbrechen',
      save: 'Änderungen speichern',
      saving: 'Wird gespeichert…',
    },
    delete: {
      title: 'Ziel löschen?',
      body: 'Das entfernt „{{name}}“ endgültig. Das beobachtete Konto und seine Kontostände bleiben unberührt. Dies kann nicht rückgängig gemacht werden.',
      cancel: 'Abbrechen',
      confirm: 'Löschen',
      deleting: 'Wird gelöscht…',
    },
    validation: {
      positive_amount: 'Gib einen positiven Zielbetrag ein.',
      choose_account: 'Wähle das Konto, das das Ziel beobachtet.',
    },
    errors: {
      create: 'Ziel konnte nicht erstellt werden.',
      delete: 'Ziel konnte nicht gelöscht werden.',
      save: 'Ziel konnte nicht gespeichert werden.',
    },
  },
  es: {
    title: 'Metas',
    add: {
      title: 'Añadir meta',
      name_placeholder: 'Nombre de la meta',
      name_aria: 'Nombre de la meta',
      amount_placeholder: 'Importe objetivo',
      account_aria: 'Cuenta seguida',
      account_empty: 'Cuenta seguida…',
      date_aria: 'Fecha objetivo (opcional)',
      submit: 'Añadir',
    },
    hints: {
      progress:
        'El progreso es el saldo actual de la cuenta seguida: aporta con una transferencia hacia ella. La moneda sigue a esa cuenta.',
      target_date:
        'La fecha objetivo es un hito al que apuntas («para cuándo»): puramente informativa, nada se impone ni se recuerda por ella.',
    },
    empty: 'Aún no hay metas: añade la primera arriba.',
    card: {
      reached: 'Meta alcanzada',
      to_go: 'Faltan {{amount}}',
      to_go_by: 'Faltan {{amount}} · para el {{date}}',
      edit: 'Editar',
      delete: 'Eliminar',
    },
    edit: {
      title: 'Editar meta',
      name: 'Nombre',
      target_amount: 'Importe objetivo ({{currency}})',
      account: 'Cuenta seguida',
      date: 'Fecha objetivo (opcional)',
      cancel: 'Cancelar',
      save: 'Guardar cambios',
      saving: 'Guardando…',
    },
    delete: {
      title: '¿Eliminar la meta?',
      body: 'Esto elimina definitivamente «{{name}}». La cuenta seguida y sus saldos quedan intactos. Esta acción no se puede deshacer.',
      cancel: 'Cancelar',
      confirm: 'Eliminar',
      deleting: 'Eliminando…',
    },
    validation: {
      positive_amount: 'Introduce un importe objetivo positivo.',
      choose_account: 'Elige la cuenta que sigue la meta.',
    },
    errors: {
      create: 'No se pudo crear la meta.',
      delete: 'No se pudo eliminar la meta.',
      save: 'No se pudo guardar la meta.',
    },
  },
};