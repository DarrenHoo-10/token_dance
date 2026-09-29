import { useEffect } from 'react';
import { useLocation } from 'react-router-dom';
import { useLocale } from '@/context/LocaleContext';

export const DEFAULT_TITLE = 'TokenDance - Let Token Dance';

// Longest prefix wins. /download and /docs set their own titles (useResourceNavigation).
const TITLES: Array<[string, string]> = [
  ['/leaderboard/list', 'pageTitle.leaderboard'],
  ['/login', 'pageTitle.login'],
  ['/desktop-login', 'pageTitle.login'],
  ['/register', 'pageTitle.register'],
  ['/forgot-password', 'pageTitle.forgot'],
  ['/reset-password', 'pageTitle.reset'],
  ['/onboarding', 'pageTitle.onboarding'],
  ['/me/activity', 'pageTitle.activity'],
  ['/me', 'pageTitle.me'],
  ['/dashboard', 'pageTitle.me'],
  ['/settings', 'pageTitle.settings'],
  ['/teams', 'pageTitle.teams'],
  ['/community', 'pageTitle.community'],
];

export function titleKeyFor(pathname: string): string | null {
  if (pathname === '/' || pathname === '/leaderboard' || pathname.startsWith('/download') || pathname.startsWith('/docs')) return null;
  if (pathname.startsWith('/u/')) return 'profile';
  const match = TITLES.filter(([prefix]) => pathname === prefix || pathname.startsWith(`${prefix}/`)).sort((a, b) => b[0].length - a[0].length)[0];
  return match ? match[1] : 'pageTitle.notFound';
}

/** Keeps document.title in step with the current route. */
export function useRouteTitle() {
  const { pathname } = useLocation();
  const { t } = useLocale();
  useEffect(() => {
    if (pathname.startsWith('/download') || pathname.startsWith('/docs')) return;
    const key = titleKeyFor(pathname);
    if (key === 'profile') {
      const handle = decodeURIComponent(pathname.split('/')[2] || '');
      document.title = handle ? `@${handle} · TokenDance` : DEFAULT_TITLE;
    } else {
      document.title = key ? `${t(key)} · TokenDance` : DEFAULT_TITLE;
    }
  }, [pathname, t]);
}
