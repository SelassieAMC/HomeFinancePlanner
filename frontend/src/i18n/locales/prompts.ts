// Namespace: prompts. Translated by the prompts feature(s) — keys must exist in all
// three languages (parity is checked by tools/check-i18n.mjs).
import type { LocaleBundle } from '../core';

export const prompts: LocaleBundle = {
  en: {
    title: 'AI prompts',
    hints: {
      overviewBefore:
        'These instruction texts are sent to your AI connectors. Editing them changes how bills are read and how offers are searched — a broken prompt can break extraction, so keep the JSON schema intact. A prompt with empty content uses its built-in default; deleting one of the two seeded prompts is safe for the same reason. ',
      overviewAfter:
        ' is replaced at run time with the live product-category list from your categories.',
      categoriesNote:
        '{{token}} will be replaced at run time with the live product-category list (names with commas are quoted).',
    },
    empty: 'No prompts yet. The built-in defaults are in use.',
    usage: {
      billExtraction:
        'Used by: AI bill scanning — every receipt photo/PDF you scan (Bills → Scan) is read with this prompt.',
      offerSearch:
        'Used by: purchase-cart offer search — the current market prices of your cart products (Cart) are researched with this prompt.',
      unknown:
        'Not used by any process — kept as a template, or reserved for a future feature.',
    },
    panel: {
      usingDefault: ' using built-in default',
      defaultBadge: 'default',
    },
    fields: {
      name: 'Name',
      description: 'Description',
      descriptionPlaceholder: 'What this prompt is for',
      content: 'Prompt content',
      contentPlaceholder: 'Prompt text',
    },
    actions: {
      save: 'Save',
      saving: 'Saving…',
      reset: 'Reset to default',
      resetTitle: 'Restore the built-in default text',
      resetTitleNone: 'This prompt has no built-in default',
      delete: 'Delete',
      deleting: 'Deleting…',
      add: '+ Add prompt',
      cancel: 'Cancel',
    },
    add: {
      title: 'Add prompt',
      key: 'Key',
      keyHint:
        'lowercase letters, digits or underscores, starting with a letter; immutable after create',
      keyPlaceholder: 'my_custom_prompt',
      namePlaceholder: 'Display name',
      contentPlaceholder:
        'Prompt text (empty = the process will use nothing unless a default exists)',
      create: 'Create prompt',
      creating: 'Creating…',
    },
    deleteDialog: {
      title: 'Delete “{{name}}”?',
      used:
        'This prompt is used by a process. After deletion the process falls back to the built-in default prompt.',
      unused: 'This prompt is not used by any process.',
    },
    errors: {
      save: 'Failed to save prompt.',
      reset: 'Failed to reset prompt.',
      delete: 'Failed to delete prompt.',
      create: 'Failed to create prompt.',
    },
  },
  de: {
    title: 'KI-Prompts',
    hints: {
      overviewBefore:
        'Diese Anweisungstexte gehen an deine KI-Konnektoren. Bearbeitest du sie, ändert sich, wie Belege gelesen und wie Angebote gesucht werden — ein fehlerhafter Prompt kann die Extraktion lahmlegen, also lasse das JSON-Schema intakt. Ein Prompt mit leerem Inhalt nutzt seinen eingebauten Standard; einen der beiden mitgelieferten Prompts zu löschen ist aus demselben Grund unproblematisch. ',
      overviewAfter:
        ' wird zur Laufzeit durch die aktuelle Produktkategorienliste aus deinen Kategorien ersetzt.',
      categoriesNote:
        '{{token}} wird zur Laufzeit durch die aktuelle Produktkategorienliste ersetzt (Namen mit Kommas werden in Anführungszeichen gesetzt).',
    },
    empty: 'Noch keine Prompts. Die eingebauten Standards sind in Gebrauch.',
    usage: {
      billExtraction:
        'Verwendet von: KI-Belegscan — jedes Belegfoto/PDF, das du scannst (Belege → Scannen), wird mit diesem Prompt gelesen.',
      offerSearch:
        'Verwendet von: Angebotssuche im Einkaufswagen — die aktuellen Marktpreise deiner Warenkorbprodukte (Einkaufswagen) werden mit diesem Prompt recherchiert.',
      unknown:
        'Wird von keinem Prozess verwendet — als Vorlage aufbewahrt oder für ein künftiges Feature reserviert.',
    },
    panel: {
      usingDefault: ' nutzt den eingebauten Standard',
      defaultBadge: 'Standard',
    },
    fields: {
      name: 'Name',
      description: 'Beschreibung',
      descriptionPlaceholder: 'Wofür dieser Prompt gedacht ist',
      content: 'Prompt-Inhalt',
      contentPlaceholder: 'Prompt-Text',
    },
    actions: {
      save: 'Speichern',
      saving: 'Speichern…',
      reset: 'Auf Standard zurücksetzen',
      resetTitle: 'Den eingebauten Standardtext wiederherstellen',
      resetTitleNone: 'Dieser Prompt hat keinen eingebauten Standard',
      delete: 'Löschen',
      deleting: 'Wird gelöscht…',
      add: '+ Prompt hinzufügen',
      cancel: 'Abbrechen',
    },
    add: {
      title: 'Prompt hinzufügen',
      key: 'Schlüssel',
      keyHint:
        'Kleinbuchstaben, Ziffern oder Unterstriche, beginnend mit einem Buchstaben; nach dem Anlegen unveränderlich',
      keyPlaceholder: 'my_custom_prompt',
      namePlaceholder: 'Anzeigename',
      contentPlaceholder:
        'Prompt-Text (leer = der Prozess verwendet nichts, außer ein Standardtext existiert)',
      create: 'Prompt anlegen',
      creating: 'Wird angelegt…',
    },
    deleteDialog: {
      title: '„{{name}}“ löschen?',
      used:
        'Dieser Prompt wird von einem Prozess verwendet. Nach dem Löschen fällt der Prozess auf den eingebauten Standardprompt zurück.',
      unused: 'Dieser Prompt wird von keinem Prozess verwendet.',
    },
    errors: {
      save: 'Prompt konnte nicht gespeichert werden.',
      reset: 'Prompt konnte nicht zurückgesetzt werden.',
      delete: 'Prompt konnte nicht gelöscht werden.',
      create: 'Prompt konnte nicht angelegt werden.',
    },
  },
  es: {
    title: 'Prompts de IA',
    hints: {
      overviewBefore:
        'Estos textos de instrucciones se envían a tus conectores de IA. Editarlos cambia cómo se leen los tickets y cómo se buscan las ofertas — un prompt defectuoso puede romper la extracción, así que mantén intacto el esquema JSON. Un prompt con contenido vacío usa su valor por defecto incorporado; borrar uno de los dos prompts incluidos es seguro por la misma razón. ',
      overviewAfter:
        ' se sustituye en tiempo de ejecución por la lista actual de categorías de producto de tus categorías.',
      categoriesNote:
        '{{token}} se sustituye en tiempo de ejecución por la lista actual de categorías de producto (los nombres con comas se entrecomillan).',
    },
    empty: 'Aún no hay prompts. Se están usando los valores por defecto incorporados.',
    usage: {
      billExtraction:
        'Lo usa: el escaneo de tickets con IA — cada foto/PDF de ticket que escanees (Tickets → Escanear) se lee con este prompt.',
      offerSearch:
        'La usa: la búsqueda de ofertas del carrito — los precios actuales de mercado de los productos del carrito (Carrito) se investigan con este prompt.',
      unknown:
        'No lo usa ningún proceso — se conserva como plantilla o queda reservado para una función futura.',
    },
    panel: {
      usingDefault: ' usa el valor por defecto incorporado',
      defaultBadge: 'por defecto',
    },
    fields: {
      name: 'Nombre',
      description: 'Descripción',
      descriptionPlaceholder: 'Para qué sirve este prompt',
      content: 'Contenido del prompt',
      contentPlaceholder: 'Texto del prompt',
    },
    actions: {
      save: 'Guardar',
      saving: 'Guardando…',
      reset: 'Restablecer al valor por defecto',
      resetTitle: 'Restaurar el texto por defecto incorporado',
      resetTitleNone: 'Este prompt no tiene valor por defecto incorporado',
      delete: 'Eliminar',
      deleting: 'Eliminando…',
      add: '+ Añadir prompt',
      cancel: 'Cancelar',
    },
    add: {
      title: 'Añadir prompt',
      key: 'Clave',
      keyHint:
        'letras minúsculas, dígitos o guiones bajos, empezando por una letra; inmutable tras crearlo',
      keyPlaceholder: 'my_custom_prompt',
      namePlaceholder: 'Nombre para mostrar',
      contentPlaceholder:
        'Texto del prompt (vacío = el proceso no usará nada salvo que exista un valor por defecto)',
      create: 'Crear prompt',
      creating: 'Creando…',
    },
    deleteDialog: {
      title: '¿Eliminar «{{name}}»?',
      used:
        'Este prompt lo usa un proceso. Tras eliminarlo, el proceso recurre al prompt por defecto incorporado.',
      unused: 'Este prompt no lo usa ningún proceso.',
    },
    errors: {
      save: 'No se pudo guardar el prompt.',
      reset: 'No se pudo restablecer el prompt.',
      delete: 'No se pudo eliminar el prompt.',
      create: 'No se pudo crear el prompt.',
    },
  },
};