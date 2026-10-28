import { useTranslation } from 'react-i18next';
import { useTheme } from '../../hooks/useTheme';

// Compact light/dark switch for the sidebar footer: cycles between the two
// resolved themes (a stored 'system' choice is kept until the user moves off
// it by clicking; the profile card offers the full three-way choice). Icon
// and tooltip always describe the theme a click SWITCHES TO, and reflect the
// resolved value so 'system' users see what they are getting.
export function ThemeToggle() {
  const { t } = useTranslation('common');
  const { resolved, setChoice } = useTheme();
  const next = resolved === 'dark' ? 'light' : 'dark';
  return (
    <button
      type="button"
      className="theme-toggle"
      aria-label={t(`theme.toggle_${next}`)}
      title={t(`theme.toggle_${next}`)}
      data-resolved={resolved}
      onClick={() => setChoice(next)}
    >
      {resolved === 'dark' ? (
        <svg viewBox="0 0 20 20" aria-hidden="true" focusable="false">
          <circle cx="10" cy="10" r="4.2" fill="currentColor" />
          <g stroke="currentColor" strokeWidth="1.6" strokeLinecap="round">
            <path d="M10 1.8v2" />
            <path d="M10 16.2v2" />
            <path d="M1.8 10h2" />
            <path d="M16.2 10h2" />
            <path d="M4.3 4.3l1.4 1.4" />
            <path d="M14.3 14.3l1.4 1.4" />
            <path d="M4.3 15.7l1.4-1.4" />
            <path d="M14.3 5.7l1.4-1.4" />
          </g>
        </svg>
      ) : (
        <svg viewBox="0 0 20 20" aria-hidden="true" focusable="false">
          <path
            d="M16.5 12.4A7.2 7.2 0 0 1 7.6 3.5a7.2 7.2 0 1 0 8.9 8.9Z"
            fill="currentColor"
          />
        </svg>
      )}
    </button>
  );
}