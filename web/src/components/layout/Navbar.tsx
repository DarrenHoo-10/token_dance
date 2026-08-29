import React, { useState } from 'react';
import { NavLink, useNavigate, useLocation } from 'react-router-dom';
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
    <header className="navbar">
      <NavLink to="/" className="nav-brand">
        <img src="/logo.png" alt="TokenDance" />
        <span>TokenDance</span>
      </NavLink>

      <nav className="nav-links" aria-label="Main Navigation">
        <NavLink
          to="/me"
          className={({ isActive }) =>
            `nav-link ${isActive || location.pathname.startsWith('/me') ? 'active' : ''}`
          }
        >
          {t('nav.tokenBoard')}
        </NavLink>
        <NavLink
          to="/explore"
          className={({ isActive }) =>
            `nav-link ${isActive || location.pathname.startsWith('/explore') ? 'active' : ''}`
          }
        >
          {t('nav.explore')}
        </NavLink>
        <NavLink
          to="/leaderboard"
          className={({ isActive }) =>
            `nav-link ${isActive || location.pathname.startsWith('/leaderboard') ? 'active' : ''}`
          }
        >
          {t('nav.leaderboard')}
        </NavLink>
        <NavLink
          to="/compare"
          className={({ isActive }) =>
            `nav-link ${isActive || location.pathname.startsWith('/compare') ? 'active' : ''}`
          }
        >
          {t('nav.compare')}
        </NavLink>
      </nav>

      <div className="nav-actions">
        <div
          onClick={() => navigate('/explore')}
          style={{
            display: 'flex',
            alignItems: 'center',
            height: 36,
            padding: '0 12px',
            border: '1px solid var(--border-light)',
            borderRadius: 'var(--radius-sm)',
            backgroundColor: 'var(--bg-subtle)',
            fontSize: 12,
            color: 'var(--text-subtle)',
            cursor: 'pointer',
            minWidth: 160,
          }}
        >
          <span>🔍 {t('common.searchPlaceholder')}</span>
        </div>

        <LocaleSwitcher />

        {authenticated && user ? (
          <div style={{ position: 'relative' }}>
            <button
              type="button"
              className="avatar"
              style={{
                width: 36,
                height: 36,
                cursor: 'pointer',
                border: '1px solid var(--border-light)',
              }}
              onClick={() => setDropdownOpen((prev) => !prev)}
              aria-label="User menu"
            >
              {user.avatarUrl ? (
                <img src={user.avatarUrl} alt={user.displayName} />
              ) : (
                <span>{initials}</span>
              )}
            </button>

            {dropdownOpen && (
              <div
                style={{
                  position: 'absolute',
                  right: 0,
                  top: '100%',
                  marginTop: 8,
                  width: 200,
                  backgroundColor: 'var(--bg-surface)',
                  border: '1px solid var(--border-light)',
                  borderRadius: 'var(--radius-md)',
                  boxShadow: 'var(--shadow-card)',
                  padding: '8px 0',
                  zIndex: 100,
                }}
                onClick={() => setDropdownOpen(false)}
              >
                <div style={{ padding: '8px 16px', borderBottom: '1px solid var(--border-light)' }}>
                  <div style={{ fontWeight: 700, fontSize: 13 }}>{user.displayName}</div>
                  <div style={{ fontSize: 11, color: 'var(--text-muted)' }}>
                    {user.handle ? `@${user.handle}` : t('common.private')}
                  </div>
                </div>

                {user.handle && (
                  <NavLink
                    to={`/u/${user.handle}`}
                    style={{
                      display: 'block',
                      padding: '8px 16px',
                      fontSize: 13,
                      color: 'var(--text-main)',
                    }}
                  >
                    {t('publicProfile.headline')}
                  </NavLink>
                )}

                <NavLink
                  to="/settings/privacy"
                  style={{
                    display: 'block',
                    padding: '8px 16px',
                    fontSize: 13,
                    color: 'var(--text-main)',
                  }}
                >
                  {t('nav.settings')}
                </NavLink>

                <NavLink
                  to="/settings/devices"
                  style={{
                    display: 'block',
                    padding: '8px 16px',
                    fontSize: 13,
                    color: 'var(--text-main)',
                  }}
                >
                  {t('nav.devices')}
                </NavLink>

                <div style={{ borderTop: '1px solid var(--border-light)', marginTop: 4, paddingTop: 4 }}>
                  <button
                    type="button"
                    onClick={handleLogout}
                    style={{
                      width: '100%',
                      textAlign: 'left',
                      padding: '8px 16px',
                      fontSize: 13,
                      color: 'var(--danger)',
                      background: 'none',
                      border: 'none',
                      cursor: 'pointer',
                      fontWeight: 600,
                    }}
                  >
                    {t('nav.logout')}
                  </button>
                </div>
              </div>
            )}
          </div>
        ) : (
          <div style={{ display: 'flex', gap: 8 }}>
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
