import { avatarUrl } from '@/utils/avatar';
import React, { useEffect, useRef, useState } from 'react';
import { NavLink, useNavigate, useLocation } from 'react-router-dom';
import { ChevronDown, Database, FileText, LogOut, Settings, UserRound } from 'lucide-react';
import { useAuth } from '@/context/AuthContext';
import { useLocale } from '@/context/LocaleContext';
import { LocaleSwitcher } from '@/components/common/LocaleSwitcher';
import { Button } from '@/components/common/Button';

export const Navbar: React.FC = () => {
  const { user, authenticated, logout } = useAuth();
  const { t } = useLocale();
  const navigate = useNavigate();
  const location = useLocation();
  const [dropdownOpen, setDropdownOpen] = useState(false);
  const userMenuRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!dropdownOpen) return;

    const closeOnOutsideClick = (event: PointerEvent) => {
      if (!userMenuRef.current?.contains(event.target as Node)) {
        setDropdownOpen(false);
      }
    };

    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        setDropdownOpen(false);
      }
    };

    document.addEventListener('pointerdown', closeOnOutsideClick);
    document.addEventListener('keydown', closeOnEscape);

    return () => {
      document.removeEventListener('pointerdown', closeOnOutsideClick);
      document.removeEventListener('keydown', closeOnEscape);
    };
  }, [dropdownOpen]);

  useEffect(() => {
    setDropdownOpen(false);
  }, [location.pathname]);

  const handleLogout = async () => {
    await logout();
    navigate('/login');
  };

  const initials = user?.displayName
    ? user.displayName
        .split(' ')
        .map((n) => n[0])
        .join('')
        .substring(0, 2)
        .toUpperCase()
    : 'TD';

  return (
    <header className="navbar floating-nav">
      <NavLink to="/" className="nav-brand">
        <img src={`${import.meta.env.BASE_URL}logo-tokendance-v2.png`} alt="TokenDance" />
        <span>TokenDance</span>
      </NavLink>

      <nav className="nav-links" aria-label={t('common.mainNavigation')}>
        <NavLink
          to="/leaderboard"
          className={({ isActive }) =>
            `nav-link ${isActive || location.pathname.startsWith('/leaderboard') ? 'active' : ''}`
          }
        >
          TokenBoard
        </NavLink>
      </nav>

      <div className="nav-actions">
        <button
          type="button"
          className="nav-document"
          onClick={() => navigate(authenticated ? '/me' : '/login?return_to=%2Fme')}
          aria-label={t('nav.tokenBoard')}
        >
          <FileText size={22} aria-hidden="true" />
        </button>

        <LocaleSwitcher />

        {authenticated && user ? (
          <div className="user-menu" ref={userMenuRef}>
            <button
              type="button"
              className={`user-menu-trigger ${dropdownOpen ? 'open' : ''}`}
              onClick={() => setDropdownOpen((prev) => !prev)}
              aria-label={t('common.userMenu')}
              aria-haspopup="menu"
              aria-expanded={dropdownOpen}
              aria-controls="primary-user-menu"
            >
              <span className="avatar">
                {user.avatarUrl ? (
                  <img src={avatarUrl(user.avatarUrl)} alt="" />
                ) : (
                  <span>{initials}</span>
                )}
              </span>
              <ChevronDown size={16} aria-hidden="true" />
            </button>

            {dropdownOpen && (
              <div
                id="primary-user-menu"
                className="user-menu-popover"
                role="menu"
                aria-label={t('common.userMenu')}
              >
                <div className="user-menu-header">
                  <div className="user-menu-name">{user.displayName}</div>
                  <div className="user-menu-handle">
                    {user.handle ? `@${user.handle}` : t('common.private')}
                  </div>
                </div>

                <NavLink
                  to="/me"
                  className="user-menu-item"
                  role="menuitem"
                >
                  <UserRound size={16} aria-hidden="true" />
                  {t('publicProfile.headline')}
                </NavLink>

                <NavLink
                  to="/settings/privacy"
                  className="user-menu-item"
                  role="menuitem"
                >
                  <Settings size={16} aria-hidden="true" />
                  {t('nav.settings')}
                </NavLink>

                <NavLink
                  to="/settings/devices"
                  className="user-menu-item"
                  role="menuitem"
                >
                  <Database size={16} aria-hidden="true" />
                  {t('nav.devices')}
                </NavLink>

                <div className="user-menu-footer">
                  <button
                    type="button"
                    onClick={handleLogout}
                    className="user-menu-item danger"
                    role="menuitem"
                  >
                    <LogOut size={16} aria-hidden="true" />
                    {t('nav.logout')}
                  </button>
                </div>
              </div>
            )}
          </div>
        ) : (
          <div className="nav-auth-actions">
            <Button
              variant="outline"
              size="sm"
              onClick={() => navigate('/login')}
            >
              {t('nav.login')}
            </Button>
            <Button
              variant="primary"
              size="sm"
              onClick={() => navigate('/register')}
            >
              {t('nav.register')}
            </Button>
          </div>
        )}
      </div>
    </header>
  );
};
