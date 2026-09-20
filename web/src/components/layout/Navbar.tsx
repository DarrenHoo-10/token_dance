import { avatarUrl } from '@/utils/avatar';
import React, { useEffect, useRef, useState } from 'react';
import { NavLink, useNavigate, useLocation } from 'react-router-dom';
import { ChevronDown, Database, BarChart3, LogOut, Menu, Search, Settings, UserRound, X } from 'lucide-react';
import { useAuth } from '@/context/AuthContext';
import { useLocale } from '@/context/LocaleContext';
import { usePersonalAnalytics } from '@/context/PersonalAnalyticsContext';
import { LocaleSwitcher } from '@/components/common/LocaleSwitcher';
import { Button } from '@/components/common/Button';

export const Navbar: React.FC = () => {
  const { user, authenticated, logout } = useAuth();
  const { t, locale } = useLocale();
  const navigate = useNavigate();
  const location = useLocation();
  const home = location.pathname === '/leaderboard';
  const personalAnalytics = usePersonalAnalytics();
  const [dropdownOpen, setDropdownOpen] = useState(false);
  const [mobileOpen, setMobileOpen] = useState(false);
  const userMenuRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!dropdownOpen && !mobileOpen) return;

    const closeOnOutsideClick = (event: PointerEvent) => {
      if (!userMenuRef.current?.contains(event.target as Node)) {
        setDropdownOpen(false);
      }
      if (!(event.target as Element).closest('.navbar')) setMobileOpen(false);
    };

    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        setDropdownOpen(false);
        setMobileOpen(false);
      }
    };

    document.addEventListener('pointerdown', closeOnOutsideClick);
    document.addEventListener('keydown', closeOnEscape);

    return () => {
      document.removeEventListener('pointerdown', closeOnOutsideClick);
      document.removeEventListener('keydown', closeOnEscape);
    };
  }, [dropdownOpen, mobileOpen]);

  useEffect(() => {
    setDropdownOpen(false);
    setMobileOpen(false);
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

      <nav id="primary-navigation" className={`nav-links ${mobileOpen ? 'is-open' : ''}`} aria-label={t('common.mainNavigation')}>
        <NavLink
          to="/leaderboard"
          className={({ isActive }) =>
            `nav-link ${isActive || location.pathname.startsWith('/leaderboard') ? 'active' : ''}`
          }
        >
          TokenBoard
        </NavLink>
        <NavLink to="/teams" className={({ isActive }) => `nav-link ${isActive ? 'active' : ''}`}>
          {t('teams.label')}
        </NavLink>
        <NavLink to="/download" className={({ isActive }) => `nav-link ${isActive ? 'active' : ''}`}>
          {locale === 'zh-CN' ? '客户端下载' : 'Download'}
        </NavLink>
        <NavLink to="/docs" className={({ isActive }) => `nav-link ${isActive ? 'active' : ''}`}>
          {locale === 'zh-CN' ? '使用文档' : 'Docs'}
        </NavLink>
      </nav>

      <div className="nav-actions">
        <button
          type="button"
          className="nav-document"
          onClick={() => {
            if (home) {
              const input = document.querySelector<HTMLInputElement>('.sky-board-search input');
              input?.scrollIntoView({ block: 'center', behavior: 'smooth' });
              input?.focus({ preventScroll: true });
            } else {
              personalAnalytics.show();
            }
          }}
          aria-label={home ? (locale === 'zh-CN' ? '搜索开发者' : 'Find a developer') : t('publicProfile.headline')}
        >
          {home ? <Search size={21} aria-hidden="true" /> : <BarChart3 size={22} aria-hidden="true" />}
        </button>

        <LocaleSwitcher compact />

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
              <span className="sky-account-name">{user.displayName || user.handle}</span>
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

                <button
                  type="button"
                  className="user-menu-item"
                  role="menuitem"
                  onClick={() => { setDropdownOpen(false); personalAnalytics.show(); }}
                >
                  <UserRound size={16} aria-hidden="true" />
                  {locale === 'zh-CN' ? '我的数据' : 'My analytics'}
                </button>

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
        <button type="button" className="sky-mobile-toggle" aria-label={locale === 'zh-CN' ? '打开导航' : 'Open navigation'} aria-expanded={mobileOpen} aria-controls="primary-navigation" onClick={() => setMobileOpen(open => !open)}>{mobileOpen ? <X size={21} /> : <Menu size={21} />}</button>
      </div>
    </header>
  );
};
