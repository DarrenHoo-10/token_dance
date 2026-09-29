import React, { Suspense } from 'react';
import { BrowserRouter, Routes, Route, Navigate, useLocation } from 'react-router-dom';
import { LocaleProvider } from '@/context/LocaleContext';
import { NotificationProvider } from '@/context/NotificationContext';
import { AuthProvider } from '@/context/AuthContext';
import { AppLayout } from '@/components/layout/AppLayout';
import { useRouteTitle } from '@/hooks/useRouteTitle';

import { LeaderboardPage } from '@/pages/public/LeaderboardPage';
import { TeamProvider } from '@/context/TeamContext';
import { PersonalAnalyticsProvider } from '@/context/PersonalAnalyticsContext';
import { LoadingState } from '@/components/states/LoadingState';
import {
  LoginPage, DesktopLoginPage, RegisterPage, ForgotPasswordPage, ResetPasswordPage, OnboardingPage,
  PersonalDashboardPage, ActivityPage,
  SettingsLayout, ProfileSettingsPage, PrivacySettingsPage, DevicesSettingsPage, ExportsSettingsPage,
  PublicProfilePage, CommunityPage, LeaderboardListPage,
  TeamDashboardPage, CreateTeamPage, InvitationPage, JoinTeamPage, TeamLayout, TeamMembersPage, TeamAnalyticsPage, TeamSettingsPage,
  NotFoundPage, DownloadPage, DocsPage,
} from '@/routes';

// Standalone pages (outside AppLayout) load their chunk behind their own fallback.
const Standalone: React.FC<{ children: React.ReactNode }> = ({ children }) => (
  <Suspense fallback={<LoadingState />}>{children}</Suspense>
);

export const RouteTitle: React.FC = () => {
  useRouteTitle();
  return null;
};

const PreserveSearchRedirect: React.FC<{ to: string }> = ({ to }) => {
  const { search } = useLocation();
  return <Navigate to={{ pathname: to, search }} relative="path" replace />;
};

export const RootRedirect: React.FC = () => {
  return <Navigate to="/leaderboard" replace />;
};

export const App: React.FC = () => {
  return (
    <LocaleProvider>
      <NotificationProvider>
        <AuthProvider>
          <BrowserRouter basename={import.meta.env.BASE_URL} future={{ v7_startTransition: true, v7_relativeSplatPath: true }}>
            <TeamProvider>
            <PersonalAnalyticsProvider>
            <RouteTitle />
            <Routes>
              {/* Standalone Auth & Onboarding */}
              <Route path="/login" element={<Standalone><LoginPage /></Standalone>} />
              <Route path="/desktop-login" element={<Standalone><DesktopLoginPage /></Standalone>} />
              <Route path="/register" element={<Standalone><RegisterPage /></Standalone>} />
              <Route path="/forgot-password" element={<Standalone><ForgotPasswordPage /></Standalone>} />
              <Route path="/reset-password" element={<Standalone><ResetPasswordPage /></Standalone>} />
              <Route path="/onboarding" element={<Standalone><OnboardingPage /></Standalone>} />

              {/* Main Application Layout */}
              <Route element={<AppLayout />}>
                <Route path="/" element={<RootRedirect />} />
                <Route path="/dashboard" element={<PersonalDashboardPage />} />
                
                {/* /me personal dashboard & activity */}
                <Route path="/me" element={<PersonalDashboardPage />} />
                <Route path="/me/summary" element={<PersonalDashboardPage />} />
                <Route path="/me/activity" element={<ActivityPage />} />

                {/* Settings & Privacy & Devices & Exports */}
                <Route path="/settings" element={<SettingsLayout />}>
                  <Route index element={<Navigate to="/settings/profile" replace />} />
                  <Route path="profile" element={<ProfileSettingsPage />} />
                  <Route path="privacy" element={<PrivacySettingsPage />} />
                  <Route path="devices" element={<DevicesSettingsPage />} />
                  <Route path="exports" element={<ExportsSettingsPage />} />
                </Route>

                {/* Public community pages */}
                <Route path="/u/:handle" element={<PublicProfilePage />} />
                <Route path="/community" element={<CommunityPage />} />
                <Route path="/leaderboard" element={<LeaderboardPage />} />
                <Route path="/leaderboard/list" element={<LeaderboardListPage />} />
                <Route path="/download" element={<DownloadPage />} />
                <Route path="/docs" element={<Navigate to="/docs/quickstart" replace />} />
                <Route path="/docs/:slug" element={<DocsPage />} />
                <Route path="/teams" element={<TeamDashboardPage />} />
                <Route path="/teams/new" element={<CreateTeamPage />} />
                <Route path="/teams/invitations/:invitationId" element={<InvitationPage />} />
                <Route path="/teams/join/:linkId" element={<JoinTeamPage />} />
                <Route path="/teams/:teamId" element={<TeamLayout />}>
                  <Route index element={<TeamAnalyticsPage />} />
                  <Route path="analytics" element={<PreserveSearchRedirect to=".." />} />
                  <Route path="members" element={<TeamMembersPage />} />
                  <Route path="settings" element={<TeamSettingsPage />} />
                </Route>

                {/* 404 catch-all */}
                <Route path="*" element={<NotFoundPage />} />
              </Route>
            </Routes>
            </PersonalAnalyticsProvider>
            </TeamProvider>
          </BrowserRouter>
        </AuthProvider>
      </NotificationProvider>
    </LocaleProvider>
  );
};

export default App;
