// Namespace: stores. Translated by the stores feature(s) — keys must exist in all
// three languages (parity is checked by tools/check-i18n.mjs).
import type { LocaleBundle } from '../core';

export const stores: LocaleBundle = {
  en: {
    title: 'Stores',
    add: {
      title: 'Add store',
      namePlaceholder: 'Name',
      descriptionPlaceholder: 'Description (optional)',
      locationPlaceholder: 'Location (optional)',
      submit: 'Add',
    },
    list: {
      empty: 'No stores yet — add your first one above, or scan a bill with a new market name.',
      bills_one: '{{count}} bill',
      bills_other: '{{count}} bills',
      logoAlt: '{{name}} logo',
    },
    labels: {
      name: 'Name',
      description: 'Description',
      location: 'Location',
      bills: 'Bills',
    },
    actions: {
      edit: 'Edit',
      save: 'Save',
      cancel: 'Cancel',
      replaceLogo: 'Replace logo',
      uploadLogo: 'Upload logo',
      removeLogo: 'Remove logo',
      delete: 'Delete store',
    },
    errors: {
      nameRequired: 'Store name is required.',
      create: 'Failed to create store.',
      save: 'Failed to save store.',
      uploadLogo: 'Failed to upload logo.',
      removeLogo: 'Failed to remove logo.',
      delete: 'Failed to delete store.',
    },
  },
  de: {
    title: 'Märkte',
    add: {
      title: 'Markt hinzufügen',
      namePlaceholder: 'Name',
      descriptionPlaceholder: 'Beschreibung (optional)',
      locationPlaceholder: 'Ort (optional)',
      submit: 'Hinzufügen',
    },
    list: {
      empty: 'Noch keine Märkte — lege oben deinen ersten an oder scanne einen Beleg mit neuem Marktnamen.',
      bills_one: '{{count}} Beleg',
      bills_other: '{{count}} Belege',
      logoAlt: 'Logo von {{name}}',
    },
    labels: {
      name: 'Name',
      description: 'Beschreibung',
      location: 'Ort',
      bills: 'Belege',
    },
    actions: {
      edit: 'Bearbeiten',
      save: 'Speichern',
      cancel: 'Abbrechen',
      replaceLogo: 'Logo ersetzen',
      uploadLogo: 'Logo hochladen',
      removeLogo: 'Logo entfernen',
      delete: 'Markt löschen',
    },
    errors: {
      nameRequired: 'Der Marktname ist erforderlich.',
      create: 'Markt konnte nicht angelegt werden.',
      save: 'Markt konnte nicht gespeichert werden.',
      uploadLogo: 'Logo konnte nicht hochgeladen werden.',
      removeLogo: 'Logo konnte nicht entfernt werden.',
      delete: 'Markt konnte nicht gelöscht werden.',
    },
  },
  es: {
    title: 'Tiendas',
    add: {
      title: 'Añadir tienda',
      namePlaceholder: 'Nombre',
      descriptionPlaceholder: 'Descripción (opcional)',
      locationPlaceholder: 'Ubicación (opcional)',
      submit: 'Añadir',
    },
    list: {
      empty: 'Aún no hay tiendas: añade la primera arriba o escanea un ticket con un nombre de tienda nuevo.',
      bills_one: '{{count}} ticket',
      bills_other: '{{count}} tickets',
      logoAlt: 'Logo de {{name}}',
    },
    labels: {
      name: 'Nombre',
      description: 'Descripción',
      location: 'Ubicación',
      bills: 'Tickets',
    },
    actions: {
      edit: 'Editar',
      save: 'Guardar',
      cancel: 'Cancelar',
      replaceLogo: 'Reemplazar logo',
      uploadLogo: 'Subir logo',
      removeLogo: 'Quitar logo',
      delete: 'Eliminar tienda',
    },
    errors: {
      nameRequired: 'El nombre de la tienda es obligatorio.',
      create: 'No se pudo crear la tienda.',
      save: 'No se pudo guardar la tienda.',
      uploadLogo: 'No se pudo subir el logo.',
      removeLogo: 'No se pudo quitar el logo.',
      delete: 'No se pudo eliminar la tienda.',
    },
  },
};