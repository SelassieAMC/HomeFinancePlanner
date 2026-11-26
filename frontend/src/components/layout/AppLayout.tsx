import { Fragment, useEffect, useState, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { Navigate, NavLink, Outlet, useLocation, useNavigate } from 'react-router-dom';
import {
  IconChevronDown,
  IconGear,
  IconHome,
  IconTag,
  IconWallet,
  Spinner,
  ThemeToggle,
} from '../ui';
import { useAuth } from '../../features/auth/AuthContext';
import { UserAvatar } from './UserAvatar';

// Mobile bottom tab bar: the three destinations that matter daily get tabs;
// the rest live behind "More". The center button opens a speed dial with the
// two quick-entry actions. The desktop sidebar shows Home on top, then three
// collapsible domain sections (Money, Catalog, System): a section row reads
// like a regular item (icon + label, no uppercase caption) and its pages
// indent under a vertical rail, so the hierarchy stands out. Open/closed
// state persists per browser; the avatar menu owns the self-service Profile
// (Settings lives here, in System). The mobile "More" sheet reuses the
// section pages as a plain list — no accordion there, the sheet is transient.
// External items are plain anchors outside the SPA router (the backend serves
// them directly — e.g. the River job-queue UI), never NavLink routes.
// The `label` field holds a `common` i18n key (nav.*) — translated at render
// time so the language switch applies without a reload of the module.
type NavItem = {
  to: string;
  label: string;
  end?: boolean;
  icon?: ReactNode;
  external?: true;
  href?: string;
};
type NavSection = { id: string; titleKey: string; icon: ReactNode; items: NavItem[] };

const TAB_ITEMS: NavItem[] = [
  { to: '/', label: 'nav.home', end: true },
  { to: '/transactions', label: 'nav.activity' },
  { to: '/planning', label: 'nav.planning' },
];

// The lone top entry of the desktop sidebar (no section header).
const TOP_ITEM: NavItem = { to: '/', label: 'nav.home', end: true, icon: <IconHome /> };

// Desktop sidebar sections; the mobile "More" sheet flattens the same items.
const NAV_SECTIONS: NavSection[] = [
  {
    id: 'money',
    titleKey: 'nav.section_money',
    icon: <IconWallet />,
    items: [
      { to: '/accounts', label: 'nav.accounts' },
      { to: '/transactions', label: 'nav.activity' },
      { to: '/planning', label: 'nav.planning' },
      { to: '/bills', label: 'nav.bills' },
      { to: '/scan', label: 'nav.scan_bill' },
    ],
  },
  {
    id: 'catalog',
    titleKey: 'nav.section_catalog',
    icon: <IconTag />,
    items: [
      { to: '/products', label: 'nav.products' },
      { to: '/stores', label: 'nav.stores' },
      { to: '/cart', label: 'nav.price_search' },
    ],
  },
  {
    id: 'system',
    titleKey: 'nav.section_system',
    icon: <IconGear />,
    items: [
      { to: '/settings', label: 'nav.settings' },
      { to: '/riverui', label: 'nav.job_queue', external: true, href: '/riverui' },
    ],
  },
];

const FAB_ITEMS = [
  { to: '/scan', label: 'nav.scan_bill' },
  { to: '/transactions', label: 'nav.add_transaction' },
];

// Which sections are folded, persisted per browser (default: all open).
const COLLAPSED_KEY = 'hfp.nav.collapsed.v1';

export function AppLayout() {
  const { user, loading } = useAuth();
  const { t } = useTranslation('common');
  const location = useLocation();
  const navigate = useNavigate();
  const [moreOpen, setMoreOpen] = useState(false);
  const [fabOpen, setFabOpen] = useState(false);

  // Collapsed sidebar sections (accordion): read once from storage, default
  // to "all open" and remember the user's folds across visits.
  const [collapsed, setCollapsed] = useState<string[]>(() => {
    try {
      const raw = window.localStorage.getItem(COLLAPSED_KEY);
      const parsed: unknown = raw ? JSON.parse(raw) : [];
      if (Array.isArray(parsed)) {
        const ids: string[] = [];
        for (const id of parsed) {
          if (typeof id === 'string' && NAV_SECTIONS.some((s) => s.id === id)) ids.push(id);
        }
        return ids;
      }
    } catch {
      // Corrupted storage: fall through to the default.
    }
    return [];
  });

  function toggleSection(id: string) {
    const next = collapsed.includes(id)
      ? collapsed.filter((g) => g !== id)
      : [...collapsed, id];
    setCollapsed(next);
    try {
      window.localStorage.setItem(COLLAPSED_KEY, JSON.stringify(next));
    } catch {
      // Private mode / quota: the fold stays in memory only.
    }
  }

  // Any navigation closes the overlays.
  useEffect(() => {
    setMoreOpen(false);
    setFabOpen(false);
  }, [location]);

  // Escape closes whichever overlay is open.
  useEffect(() => {
    if (!moreOpen && !fabOpen) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        setMoreOpen(false);
        setFabOpen(false);
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [moreOpen, fabOpen]);

  // A deep link into a folded section unfolds it, so the active page is never
  // hidden (bookmarks, the More sheet and the avatar menu can land anywhere).
  useEffect(() => {
    const active = NAV_SECTIONS.find((section) =>
      section.items.some((item) => item.to !== '/' && location.pathname.startsWith(item.to)),
    );
    if (active && collapsed.includes(active.id)) {
      const next = collapsed.filter((g) => g !== active.id);
      setCollapsed(next);
      try {
        window.localStorage.setItem(COLLAPSED_KEY, JSON.stringify(next));
      } catch {
        // Private mode / quota: ignore.
      }
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [location.pathname]);

  // "More" is active while any of its pages is on screen.
  const moreActive =
    !TAB_ITEMS.some((item) => item.to === location.pathname) &&
    NAV_SECTIONS.some((section) =>
      section.items.some((item) => location.pathname.startsWith(item.to)),
    );

  // The session check gates the shell: no user means the login page, and a
  // mid-session sign-out (any 401) lands here on the next render.
  if (loading) return <div className="app-layout"><Spinner /></div>;
  if (!user) return <Navigate to="/login" replace />;

  return (
    <div className="app-layout">
      {/* Top-right profile chip (fixed; visible on desktop and mobile). */}
      <UserAvatar />
      {/* Desktop navigation — hidden behind the tab bar on mobile. */}
      <aside className="sidebar">
        <div className="sidebar-brand">Home Finance Planner</div>
        <nav aria-label={t('nav.main_aria')}>
          <ul className="sidebar-nav">
            <li>
              <NavLink
                to={TOP_ITEM.to}
                end={TOP_ITEM.end}
                className={({ isActive }) => (isActive ? 'nav-link active' : 'nav-link')}
              >
                <span className="nav-link-icon">{TOP_ITEM.icon}</span>
                {t(TOP_ITEM.label)}
              </NavLink>
            </li>
          </ul>
          {NAV_SECTIONS.map((section) => {
            const open = !collapsed.includes(section.id);
            const hasActive = section.items.some((item) =>
              location.pathname.startsWith(item.to),
            );
            return (
              <div className="sidebar-section" key={section.id}>
                <button
                  type="button"
                  className={hasActive ? 'nav-section-row has-active' : 'nav-section-row'}
                  aria-expanded={open}
                  onClick={() => toggleSection(section.id)}
                >
                  <span className="nav-link-icon">{section.icon}</span>
                  <span className="nav-section-label">{t(section.titleKey)}</span>
                  <span
                    className={open ? 'nav-section-chevron' : 'nav-section-chevron closed'}
                    aria-hidden="true"
                  >
                    <IconChevronDown />
                  </span>
                </button>
                {open && (
                  <ul className="sidebar-nav sidebar-subnav">
                    {section.items.map((item) =>
                      item.external ? (
                        <li key={item.to}>
                          <a href={item.href} className="nav-link">
                            {t(item.label)}
                          </a>
                        </li>
                      ) : (
                        <li key={item.to}>
                          <NavLink
                            to={item.to}
                            end={item.end}
                            className={({ isActive }) =>
                              isActive ? 'nav-link active' : 'nav-link'
                            }
                          >
                            {t(item.label)}
                          </NavLink>
                        </li>
                      ),
                    )}
                  </ul>
                )}
              </div>
            );
          })}
        </nav>
        <div className="sidebar-footer">
          <ThemeToggle />
        </div>
      </aside>
      <main className="app-main">
        <Outlet />
      </main>

      {/* Mobile: bottom tab bar with a center speed-dial FAB. */}
      <nav className="tabbar" aria-label={t('nav.main_aria')}>
        <NavLink
          to="/"
          end
          className={({ isActive }) => (isActive ? 'tab-link active' : 'tab-link')}
        >
          {t('nav.home')}
        </NavLink>
        <NavLink
          to="/transactions"
          className={({ isActive }) => (isActive ? 'tab-link active' : 'tab-link')}
        >
          {t('nav.activity')}
        </NavLink>
        <span className="tab-fab-slot">
          {fabOpen && (
            <>
              {/* Transparent tap-catcher: tapping anywhere else closes the dial. */}
              <div
                className="fab-backdrop"
                aria-hidden="true"
                onClick={() => setFabOpen(false)}
              />
              <div className="fab-menu">
                {FAB_ITEMS.map((item) => (
                  <button
                    key={item.to}
                    type="button"
                    className="fab-item"
                    onClick={() => navigate(item.to)}
                  >
                    {t(item.label)}
                  </button>
                ))}
              </div>
            </>
          )}
          <button
            type="button"
            className={fabOpen ? 'tab-fab open' : 'tab-fab'}
            aria-label={t('nav.quick_actions_aria')}
            aria-expanded={fabOpen}
            onClick={() => setFabOpen((open) => !open)}
          >
            +
          </button>
        </span>
        <NavLink
          to="/planning"
          className={({ isActive }) => (isActive ? 'tab-link active' : 'tab-link')}
        >
          {t('nav.planning')}
        </NavLink>
        <button
          type="button"
          className={moreActive || moreOpen ? 'tab-link active' : 'tab-link'}
          aria-haspopup="dialog"
          aria-expanded={moreOpen}
          onClick={() => setMoreOpen(true)}
        >
          {t('nav.more')}
        </button>
      </nav>

      {/* Mobile: "More" bottom sheet with the secondary destinations. */}
      {moreOpen && (
        <>
          <div
            className="sheet-backdrop"
            aria-hidden="true"
            onClick={() => setMoreOpen(false)}
          />
          <div className="more-sheet" role="dialog" aria-label={t('nav.more_pages_aria')}>
            <nav>
              <ul onClick={() => setMoreOpen(false)}>
                {NAV_SECTIONS.map((section) => (
                  <Fragment key={section.id}>
                    <li className="more-section-title">{t(section.titleKey)}</li>
                    {section.items.map((item) => (
                      <li key={item.to}>
                        {item.external ? (
                          <a href={item.href} className="more-link">
                            {t(item.label)}
                          </a>
                        ) : (
                          <NavLink
                            to={item.to}
                            className={({ isActive }) =>
                              isActive ? 'more-link active' : 'more-link'
                            }
                          >
                            {t(item.label)}
                          </NavLink>
                        )}
                      </li>
                    ))}
                  </Fragment>
                ))}
              </ul>
            </nav>
          </div>
        </>
      )}
    </div>
  );
}