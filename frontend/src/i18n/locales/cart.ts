// Namespace: cart. Translated by the cart feature(s) — keys must exist in all
// three languages (parity is checked by tools/check-i18n.mjs).
import type { LocaleBundle } from '../core';

export const cart: LocaleBundle = {
  en: {
    title: 'Purchase cart',
    confirmedBanner:
      'Purchase confirmed — the cart was cleared. The offer report is kept below under “Past searches”.',
    hints: {
      addBefore: 'Add products from the ',
      productsLink: 'Products',
      addMiddle:
        ' overview, then search current offers for them in your local markets with the AI connector from ',
      settingsLink: 'Settings',
      addAfter: '.',
    },
    tabs: {
      aria: 'Purchase cart views',
      cart: 'Cart',
      searches: 'Searches',
    },
    cart: {
      cardTitle: 'Cart',
      empty: 'The cart is empty — add products from the products overview.',
    },
    qty: {
      decreaseAria: 'Decrease quantity of {{name}}',
      aria: 'Quantity of {{name}}',
      increaseAria: 'Increase quantity of {{name}}',
    },
    lastPrice: 'last: {{price}}',
    remove: {
      title: 'Remove from cart',
      aria: 'Remove {{name}} from the cart',
    },
    add: {
      aria: 'Add a product to the cart',
      placeholder: 'Add a product by name…',
    },
    search: {
      offers: 'Search offers',
      clearAll: 'Clear all',
    },
    options: {
      storeScope: 'Store scope',
      anyMarket: 'Any local market — whatever appears available',
      pinnedStores: 'Only these stores (missing products are reported per store)',
      loadingStores: 'Loading your stores…',
      noStores:
        'No stores recorded yet — add one on a bill, or leave the scope open.',
      loose:
        'Include similar names and varieties (loose) — e.g. avocado also matches Hass, XL, ready-to-eat; each offer names what the market sells. Off = exact name only (strict).',
      start: 'Start search',
    },
    searching: {
      title: 'Searching offers',
      timedOut:
        'Still searching — grounded web searches can take a while. You can leave this page; the result appears here (and under “Past searches”) once it finishes.',
      spinner:
        'Asking the AI connector for current offers in your local markets…',
    },
    failed: {
      title: 'Offer search failed',
      default: 'The offer search failed.',
      hintBefore:
        'The configured AI connector could not search the web. Pick a model or provider with web search in ',
      settingsLink: 'Settings',
      hintAfter:
        ' (Gemini, Anthropic, or an OpenAI search model), then try again.',
      retry: 'Try again',
    },
    result: {
      title: 'Offers found',
      confirm: 'Confirm purchase',
    },
    history: {
      title: 'Past searches',
      empty: 'No offer searches yet.',
      products_one: '{{count}} product',
      products_other: '{{count}} products',
      pinned: '{{count}} pinned',
      loose: 'loose',
    },
    statuses: {
      searching: 'searching',
      done: 'done',
      failed: 'failed',
      cancelled: 'cancelled',
    },
    view: {
      searchedOn:
        'Offers searched on {{date}} — green marks the best price, red the worst (per product and currency).',
      noData: 'No offer data came back for the cart.',
      noOffers: 'no offers',
      nothingFound: 'Nothing was found for this product.',
      market: 'Market',
      brand: 'Brand',
      price: 'Price',
      offerQuestion: 'Offer?',
      availabilityNotAvailable: 'not available',
      availabilityNotPublished: 'not published',
      offerTag: 'offer',
      notFound: 'not found',
      bestPrice: 'from {{price}}',
    },
    actions: {
      cancel: 'Cancel',
      backToCart: 'Back to cart',
      discard: 'Discard',
    },
    errors: {
      notFoundOrExpired: 'offer search not found or expired',
      load: 'Could not load the offer search.',
      start: 'Could not start the offer search.',
      retry: 'Retry failed.',
      discard: 'Could not discard the search.',
      searchFailed: 'The offer search failed.',
    },
  },
  de: {
    title: 'Einkaufswagen',
    confirmedBanner:
      'Kauf bestätigt — der Warenkorb wurde geleert. Der Angebotsbericht bleibt unten unter „Frühere Suchläufe“ erhalten.',
    hints: {
      addBefore: 'Füge Produkte aus der ',
      productsLink: 'Produkte',
      addMiddle:
        '-Übersicht hinzu und suche dann aktuelle Angebote dafür in deinen lokalen Märkten — mit dem KI-Konnektor aus den ',
      settingsLink: 'Einstellungen',
      addAfter: '.',
    },
    tabs: {
      aria: 'Ansichten des Einkaufswagens',
      cart: 'Warenkorb',
      searches: 'Suchläufe',
    },
    cart: {
      cardTitle: 'Warenkorb',
      empty: 'Der Warenkorb ist leer — füge Produkte aus der Produktübersicht hinzu.',
    },
    qty: {
      decreaseAria: 'Menge von {{name}} verringern',
      aria: 'Menge von {{name}}',
      increaseAria: 'Menge von {{name}} erhöhen',
    },
    lastPrice: 'zuletzt: {{price}}',
    remove: {
      title: 'Aus dem Warenkorb entfernen',
      aria: '{{name}} aus dem Warenkorb entfernen',
    },
    add: {
      aria: 'Produkt in den Warenkorb legen',
      placeholder: 'Produkt nach Namen hinzufügen…',
    },
    search: {
      offers: 'Angebote suchen',
      clearAll: 'Alles leeren',
    },
    options: {
      storeScope: 'Marktauswahl',
      anyMarket: 'Jeder lokale Markt — was auch immer verfügbar ist',
      pinnedStores:
        'Nur diese Märkte (fehlende Produkte werden pro Markt gemeldet)',
      loadingStores: 'Deine Märkte werden geladen…',
      noStores:
        'Noch keine Märkte erfasst — leg einen über einen Beleg an oder lass die Auswahl offen.',
      loose:
        'Ähnliche Namen und Sorten einbeziehen (lose) — z. B. passt zu Avocado auch Hass, XL, essfertig; jedes Angebot nennt, was der Markt tatsächlich verkauft. Aus = nur der exakte Name (strikt).',
      start: 'Suche starten',
    },
    searching: {
      title: 'Angebote werden gesucht',
      timedOut:
        'Suche läuft noch — KI-gestützte Websuchen können eine Weile dauern. Du kannst die Seite verlassen; das Ergebnis erscheint hier (und unter „Frühere Suchläufe“), sobald es fertig ist.',
      spinner:
        'Der KI-Konnektor sucht aktuelle Angebote in deinen lokalen Märkten…',
    },
    failed: {
      title: 'Angebotssuche fehlgeschlagen',
      default: 'Die Angebotssuche ist fehlgeschlagen.',
      hintBefore:
        'Der konfigurierte KI-Konnektor konnte das Web nicht durchsuchen. Wähle in den ',
      settingsLink: 'Einstellungen',
      hintAfter:
        ' ein Modell oder einen Anbieter mit Websuche (Gemini, Anthropic oder ein OpenAI-Suchmodell) und versuche es dann erneut.',
      retry: 'Erneut versuchen',
    },
    result: {
      title: 'Angebote gefunden',
      confirm: 'Kauf bestätigen',
    },
    history: {
      title: 'Frühere Suchläufe',
      empty: 'Noch keine Angebotssuchen.',
      products_one: '{{count}} Produkt',
      products_other: '{{count}} Produkte',
      pinned: '{{count}} angeheftet',
      loose: 'lose',
    },
    statuses: {
      searching: 'läuft',
      done: 'fertig',
      failed: 'fehlgeschlagen',
      cancelled: 'abgebrochen',
    },
    view: {
      searchedOn:
        'Angebote gesucht am {{date}} — grün markiert den besten Preis, rot den schlechtesten (pro Produkt und Währung).',
      noData: 'Für den Warenkorb sind keine Angebote zurückgekommen.',
      noOffers: 'keine Angebote',
      nothingFound: 'Für dieses Produkt wurde nichts gefunden.',
      market: 'Markt',
      brand: 'Marke',
      price: 'Preis',
      offerQuestion: 'Angebot?',
      availabilityNotAvailable: 'nicht verfügbar',
      availabilityNotPublished: 'nicht gelistet',
      offerTag: 'Angebot',
      notFound: 'nicht gefunden',
      bestPrice: 'ab {{price}}',
    },
    actions: {
      cancel: 'Abbrechen',
      backToCart: 'Zurück zum Warenkorb',
      discard: 'Verwerfen',
    },
    errors: {
      notFoundOrExpired: 'Angebotssuche nicht gefunden oder abgelaufen',
      load: 'Die Angebotssuche konnte nicht geladen werden.',
      start: 'Die Angebotssuche konnte nicht gestartet werden.',
      retry: 'Wiederholung fehlgeschlagen.',
      discard: 'Die Suche konnte nicht verworfen werden.',
      searchFailed: 'Die Angebotssuche ist fehlgeschlagen.',
    },
  },
  es: {
    title: 'Carrito de compra',
    confirmedBanner:
      'Compra confirmada — el carrito se ha vaciado. El informe de ofertas se conserva abajo en «Búsquedas anteriores».',
    hints: {
      addBefore: 'Añade productos desde la vista de ',
      productsLink: 'Productos',
      addMiddle:
        ' y busca ofertas actuales para ellos en tus mercados locales con el conector de IA de ',
      settingsLink: 'Ajustes',
      addAfter: '.',
    },
    tabs: {
      aria: 'Vistas del carrito de compra',
      cart: 'Carrito',
      searches: 'Búsquedas',
    },
    cart: {
      cardTitle: 'Carrito',
      empty: 'El carrito está vacío: añade productos desde la vista de productos.',
    },
    qty: {
      decreaseAria: 'Disminuir la cantidad de {{name}}',
      aria: 'Cantidad de {{name}}',
      increaseAria: 'Aumentar la cantidad de {{name}}',
    },
    lastPrice: 'último: {{price}}',
    remove: {
      title: 'Quitar del carrito',
      aria: 'Quitar {{name}} del carrito',
    },
    add: {
      aria: 'Añadir un producto al carrito',
      placeholder: 'Añadir un producto por nombre…',
    },
    search: {
      offers: 'Buscar ofertas',
      clearAll: 'Vaciar todo',
    },
    options: {
      storeScope: 'Alcance de tiendas',
      anyMarket: 'Cualquier tienda local — lo que aparezca disponible',
      pinnedStores:
        'Solo estas tiendas (los productos faltantes se informan por tienda)',
      loadingStores: 'Cargando tus tiendas…',
      noStores:
        'Aún no hay tiendas registradas: añade una con un ticket o deja el alcance abierto.',
      loose:
        'Incluir nombres y variedades similares (flexible) — p. ej., para aguacate también coinciden Hass, XL, listo para consumir; cada oferta indica qué vende la tienda. Desactivado = solo el nombre exacto (estricto).',
      start: 'Iniciar búsqueda',
    },
    searching: {
      title: 'Buscando ofertas',
      timedOut:
        'Sigue buscando — las búsquedas web asistidas por IA pueden tardar un rato. Puedes dejar esta página; el resultado aparece aquí (y en «Búsquedas anteriores») cuando termine.',
      spinner:
        'El conector de IA busca ofertas actuales en tus mercados locales…',
    },
    failed: {
      title: 'La búsqueda de ofertas falló',
      default: 'La búsqueda de ofertas falló.',
      hintBefore:
        'El conector de IA configurado no pudo buscar en la web. Elige en ',
      settingsLink: 'Ajustes',
      hintAfter:
        ' un modelo o proveedor con búsqueda web (Gemini, Anthropic o un modelo de búsqueda de OpenAI) y vuelve a intentarlo.',
      retry: 'Reintentar',
    },
    result: {
      title: 'Ofertas encontradas',
      confirm: 'Confirmar compra',
    },
    history: {
      title: 'Búsquedas anteriores',
      empty: 'Aún no hay búsquedas de ofertas.',
      products_one: '{{count}} producto',
      products_other: '{{count}} productos',
      pinned: '{{count}} fijadas',
      loose: 'flexible',
    },
    statuses: {
      searching: 'en curso',
      done: 'completada',
      failed: 'fallida',
      cancelled: 'cancelada',
    },
    view: {
      searchedOn:
        'Ofertas buscadas el {{date}} — en verde el mejor precio, en rojo el peor (por producto y moneda).',
      noData: 'No llegó ningún dato de ofertas para el carrito.',
      noOffers: 'sin ofertas',
      nothingFound: 'No se encontró nada para este producto.',
      market: 'Tienda',
      brand: 'Marca',
      price: 'Precio',
      offerQuestion: '¿Oferta?',
      availabilityNotAvailable: 'no disponible',
      availabilityNotPublished: 'no publicado',
      offerTag: 'oferta',
      notFound: 'no encontrado',
      bestPrice: 'desde {{price}}',
    },
    actions: {
      cancel: 'Cancelar',
      backToCart: 'Volver al carrito',
      discard: 'Descartar',
    },
    errors: {
      notFoundOrExpired: 'búsqueda de ofertas no encontrada o caducada',
      load: 'No se pudo cargar la búsqueda de ofertas.',
      start: 'No se pudo iniciar la búsqueda de ofertas.',
      retry: 'El reintento falló.',
      discard: 'No se pudo descartar la búsqueda.',
      searchFailed: 'La búsqueda de ofertas falló.',
    },
  },
};