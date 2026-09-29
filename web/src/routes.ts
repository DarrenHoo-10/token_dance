import { lazy, type ComponentType } from 'react';

// Route components are loaded on first visit so the home page ships only what it needs.
function lazyNamed<M extends Record<string, unknown>, K extends keyof M>(loader: () => Promise<M>, name: K) {
  const Component = lazy(async () => ({ default: (await loader())[name] as ComponentType }));
  return Object.assign(Component, { preload: () => { void loader(); } });
}

export const LoginPage = lazyNamed(() => import('@/pages/auth/LoginPage'), 'LoginPage');
export const DesktopLoginPage = lazyNamed(() => import('@/pages/auth/DesktopLoginPage'), 'DesktopLoginPage');
export const RegisterPage = lazyNamed(() => import('@/pages/auth/RegisterPage'), 'RegisterPage');
export const ForgotPasswordPage = lazyNamed(() => import('@/pages/auth/ForgotPasswordPage'), 'ForgotPasswordPage');
export const ResetPasswordPage = lazyNamed(() => import('@/pages/auth/ResetPasswordPage'), 'ResetPasswordPage');
export const OnboardingPage = lazyNamed(() => import('@/pages/onboarding/OnboardingPage'), 'OnboardingPage');

export const PersonalDashboardPage = lazyNamed(() => import('@/pages/me/PersonalDashboardPage'), 'PersonalDashboardPage');
export const ActivityPage = lazyNamed(() => import('@/pages/me/ActivityPage'), 'ActivityPage');

export const SettingsLayout = lazyNamed(() => import('@/components/layout/SettingsLayout'), 'SettingsLayout');
export const ProfileSettingsPage = lazyNamed(() => import('@/pages/settings/ProfileSettingsPage'), 'ProfileSettingsPage');
export const PrivacySettingsPage = lazyNamed(() => import('@/pages/settings/PrivacySettingsPage'), 'PrivacySettingsPage');
export const DevicesSettingsPage = lazyNamed(() => import('@/pages/settings/DevicesSettingsPage'), 'DevicesSettingsPage');
export const ExportsSettingsPage = lazyNamed(() => import('@/pages/settings/ExportsSettingsPage'), 'ExportsSettingsPage');

export const PublicProfilePage = lazyNamed(() => import('@/pages/public/PublicProfilePage'), 'PublicProfilePage');
export const CommunityPage = lazyNamed(() => import('@/pages/public/CommunityPage'), 'CommunityPage');
export const LeaderboardListPage = lazyNamed(() => import('@/pages/public/LeaderboardListPage'), 'LeaderboardListPage');

export const TeamDashboardPage = lazyNamed(() => import('@/pages/teams/TeamDashboardPage'), 'TeamDashboardPage');
export const CreateTeamPage = lazyNamed(() => import('@/pages/teams/CreateTeamPage'), 'CreateTeamPage');
export const InvitationPage = lazyNamed(() => import('@/pages/teams/InvitationPage'), 'InvitationPage');
export const JoinTeamPage = lazyNamed(() => import('@/pages/teams/JoinTeamPage'), 'JoinTeamPage');
export const TeamLayout = lazyNamed(() => import('@/pages/teams/TeamLayout'), 'TeamLayout');
export const TeamMembersPage = lazyNamed(() => import('@/pages/teams/TeamMembersPage'), 'TeamMembersPage');
export const TeamAnalyticsPage = lazyNamed(() => import('@/pages/teams/TeamAnalyticsPage'), 'TeamAnalyticsPage');
export const TeamSettingsPage = lazyNamed(() => import('@/pages/teams/TeamSettingsPage'), 'TeamSettingsPage');

export const NotFoundPage = lazyNamed(() => import('@/pages/system/NotFoundPage'), 'NotFoundPage');
export const DownloadPage = lazyNamed(() => import('@/pages/resources/DownloadPage'), 'DownloadPage');
export const DocsPage = lazyNamed(() => import('@/pages/resources/DocsPage'), 'DocsPage');

/** Warms the chunk for a link the pointer is about to click. */
export function preloadRoute(pathname: string) {
  if (pathname.startsWith('/teams')) TeamDashboardPage.preload();
  else if (pathname.startsWith('/download')) DownloadPage.preload();
  else if (pathname.startsWith('/docs')) DocsPage.preload();
  else if (pathname === '/leaderboard/list') LeaderboardListPage.preload();
  else if (pathname === '/login') LoginPage.preload();
  else if (pathname === '/register') RegisterPage.preload();
}
