import { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useNavigate } from 'react-router-dom';
import { authApi, avatarUrl } from '../../api/auth';
import { useAuth } from '../../features/auth/AuthContext';

// Top-right profile chip: the member's tiny avatar photo (or initials when
// none), opening a small menu with the settings entry and logout. Fixed
// positioning on both desktop and mobile — it always lives at the top-right
// corner above the page content.
export function UserAvatar() {
  const { user, setUser } = useAuth();
  const { t } = useTranslation('common');
  const navigate = useNavigate();
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    const onClick = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false);
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setOpen(false);
    };
    document.addEventListener('mousedown', onClick);
    window.addEventListener('keydown', onKey);
    return () => {
      document.removeEventListener('mousedown', onClick);
      window.removeEventListener('keydown', onKey);
    };
  }, [open]);

  if (!user) return null;

  const label = user.full_name || user.username;
  const initials = label
    .split(/\s+/)
    .slice(0, 2)
    .map((w) => w.charAt(0).toUpperCase())
    .join('');

  async function logout() {
    try {
      await authApi.logout();
    } finally {
      setUser(null);
      navigate('/login');
    }
  }

  return (
    <div className="user-avatar-slot" ref={ref}>
      <button
        type="button"
        className="user-avatar-btn"
        aria-haspopup="menu"
        aria-expanded={open}
        aria-label={t('user_menu.account_aria', { name: label })}
        onClick={() => setOpen((o) => !o)}
      >
        {user.has_photo ? (
          <img
            className="user-avatar-img"
            src={`${avatarUrl(user.id)}?v=${encodeURIComponent(user.updated_at)}`}
            alt=""
          />
        ) : (
          <span className="user-avatar-initials">{initials || '?'}</span>
        )}
      </button>

      {open && (
        <div className="user-menu" role="menu">
          <div className="user-menu-head">
            {user.has_photo && (
              <img
                className="user-menu-img"
                src={`${avatarUrl(user.id)}?v=${encodeURIComponent(user.updated_at)}`}
                alt=""
              />
            )}
            <div>
              <div className="user-menu-name">{label}</div>
              <div className="user-menu-role">{user.is_admin ? t('user_menu.administrator') : t('user_menu.family_member')}</div>
            </div>
          </div>
          <button type="button" role="menuitem" onClick={() => { setOpen(false); navigate('/settings'); }}>
            {t('user_menu.settings_profile')}
          </button>
          <button type="button" role="menuitem" onClick={() => { setOpen(false); void logout(); }}>
            {t('user_menu.log_out')}
          </button>
        </div>
      )}
    </div>
  );
}