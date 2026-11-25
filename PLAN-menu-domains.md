# Plan: Menu domains split

Status: ready to implement. Frontend-only — **no backend changes, no migration.**
Rationale and boundary decisions live in
`home-planner-context/Menu Domains & Permissions Design.md`. Permissions are
**out of scope** for this plan; the grouping only makes the menu ready for a
future per-domain gate.

## Goal

Group the navigation into three domains (Money, Catalog & Markets, System) plus
a self-service Profile, replacing today's flat list.

Target sidebar structure:

```
Home
Profile

MONEY
  Accounts
  Activity
  Planning
  Bills
  Scan receipt

CATALOG
  Products
  Stores
  Price search

SYSTEM
  Settings
  Job queue
```

Mobile bottom tabs (Home · Activity · [FAB] · Planning · More) stay; the **More
sheet** gets the same domain headers.

## Scope

- Frontend only. Every route and API endpoint already exists (`frontend/src/app/router.tsx`).
- No permission logic. Grouping is presentation only.
- No changes to: dashboard, transactions, planning, accounts, stores, products,
  cart, bills, scan pages. Only navigation + the Settings split.

## Steps

### 1. Split Profile out of Settings

`frontend/src/features/settings/SettingsPage.tsx`:

- Remove `'profile'` from `type SettingsTab` (line 89) and from the `tabs` array
  (line 273).
- Remove the `{tab === 'profile' && <ProfileTab key="profile" />}` render
  (line 285).
- Change the default tab `useState<SettingsTab>('profile')` (line 94) to
  `'general'`.
- The `ProfileTab` import can stay or move (see step 2).

New file `frontend/src/features/settings/ProfilePage.tsx` — a thin page wrapper
around the existing `ProfileTab` (reuse it, do not rewrite its logic):

```tsx
import { useTranslation } from 'react-i18next';
import { ProfileTab } from './ProfileTab';

export function ProfilePage() {
  const { t } = useTranslation('settings');
  return (
    <div className="page">
      <h2 className="page-title">{t('profile.pageTitle')}</h2>
      <ProfileTab />
    </div>
  );
}
```

Add the `profile.pageTitle` key to `frontend/src/i18n/locales/settings.ts`
(en/de/es), e.g. "Profile" / "Profil" / "Perfil". Run
`node tools/check-i18n.mjs` to confirm parity.

### 2. Add the `/profile` route

`frontend/src/app/router.tsx`:

- Import `ProfilePage`.
- Add `{ path: 'profile', element: <ProfilePage /> }` next to the settings
  routes.
- `/settings` keeps rendering `SettingsPage` (now without the profile tab);
  `/settings/prompts` unchanged.

### 3. Group the navigation

`frontend/src/components/layout/AppLayout.tsx` — replace the flat `NavItem[]`
lists with a grouped structure. Two surfaces to update:

**Desktop sidebar** (`ALL_ITEMS` → grouped). Suggested shape:

```ts
type NavItem = { to: string; label: string; end?: boolean; external?: true; href?: string };
type NavGroup = { titleKey?: string; items: NavItem[] };

const NAV_GROUPS: NavGroup[] = [
  { items: [
    { to: '/', label: 'nav.home', end: true },
    { to: '/profile', label: 'nav.profile' },
  ]},
  { titleKey: 'nav.section_money', items: [
    { to: '/accounts', label: 'nav.accounts' },
    { to: '/transactions', label: 'nav.activity' },
    { to: '/planning', label: 'nav.planning' },
    { to: '/bills', label: 'nav.bills' },
    { to: '/scan', label: 'nav.scan_bill' },
  ]},
  { titleKey: 'nav.section_catalog', items: [
    { to: '/products', label: 'nav.products' },
    { to: '/stores', label: 'nav.stores' },
    { to: '/cart', label: 'nav.price_search' },
  ]},
  { titleKey: 'nav.section_system', items: [
    { to: '/settings', label: 'nav.settings' },
    { to: '/riverui', label: 'nav.job_queue', external: true, href: '/riverui' },
  ]},
];
```

Render each group as its own `<ul className="sidebar-nav">` preceded by a
`<li className="sidebar-section-title">{t(group.titleKey)}</li>` (or a heading
element). Keep the existing `external` → plain `<a>` vs `NavLink` handling.

**Mobile "More" sheet** (`MORE_ITEMS`) — the same groups minus Home/Profile
(the top group is only for the desktop sidebar). Reuse `NAV_GROUPS` and skip the
first group, or define a parallel `MORE_GROUPS`. Keep the existing
`more-link` classes; render the same section titles.

**Keep unchanged:** `TAB_ITEMS` (Home/Activity/Planning bottom tabs),
`FAB_ITEMS`, the `moreActive`/`moreOpen` logic, and the session gate.

### 4. i18n keys

`frontend/src/i18n/locales/common.ts`, in the `nav` object of **en, de, es**:

- `profile` — "Profile" / "Profil" / "Perfil"
- `section_money` — "Money" / "Geld" / "Dinero"
- `section_catalog` — "Catalog" / "Katalog" / "Catálogo"
- `section_system` — "System" / "System" / "Sistema"
- `price_search` — "Price search" / "Preissuche" / "Búsqueda de precios"
  (cosmetic rename of the cart label; the route/page stay `/cart`. Deferrable —
  if skipped, keep `label: 'nav.purchase_cart'`.)

Run `node tools/check-i18n.mjs` after adding keys.

### 5. CSS for section titles

`frontend/src/styles/global.css`, next to `.sidebar-nav` (~line 220) and the
`.more-sheet` block (~line 2165):

```css
.sidebar-section-title,
.more-section-title {
  padding: 0.75rem 0.75rem 0.25rem;
  font-size: 0.7rem;
  font-weight: 600;
  letter-spacing: 0.08em;
  text-transform: uppercase;
  color: var(--text-muted);
}
```

(Use the existing `--text-muted` token; match dark mode via the existing
`data-theme` token system — no new colors.)

### 6. Point the avatar menu at the new page

`frontend/src/components/layout/UserAvatar.tsx` — the `user_menu.settings_profile`
("Settings & profile") action currently targets `/settings`. Point it at
`/profile`, or split it into two entries ("Profile" → `/profile`,
"Settings" → `/settings`). Minimal change: retarget to `/profile`.

## Verification

Run from the repo root:

```bash
make typecheck-frontend   # or: npx tsc --noEmit
make lint-frontend
node tools/check-i18n.mjs
```

Manual checks:

1. Log in — sidebar shows the four groups with headers, Home/Profile on top.
2. `/profile` renders name, language, avatar, password (no other tabs).
3. `/settings` opens on the General tab and no longer shows a Profile tab.
4. `/settings/prompts` still reachable (the "Manage prompts" link in the
   connectors tab points there).
5. Mobile: bottom tabs unchanged; "More" sheet shows the same Money/Catalog/
   System headers.
6. Language switch to de and es — section titles and new labels translate.
7. Dark mode — section titles remain legible.

## Out of scope (explicitly deferred)

- Permissions / roles / server-side authorization (future).
- Renaming routes or pages (`/cart`, `/settings` paths stay).
- Moving `ProfileTab`/`UsersTab` to a new folder (optional cleanup only).
- Any backend change.
