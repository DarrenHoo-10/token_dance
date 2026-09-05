import React from 'react';
import { BrowserRouter, Routes, Route, Navigate } from 'react-router-dom';
import { LocaleProvider } from '@/context/LocaleContext';
import { NotificationProvider } from '@/context/NotificationContext';
import { AuthProvider, useAuth } from '@/context/AuthContext';
import { AppLayout } from '@/components/layout/AppLayout';
import { SettingsLayout } from '@/components/layout/SettingsLayout';

// Auth Pages
import { LoginPage } from '@/pages/auth/LoginPage';
import { RegisterPage } from '@/pages/auth/RegisterPage';
import { ForgotPasswordPage } from '@/pages/auth/ForgotPasswordPage';
import { ResetPasswordPage } from '@/pages/auth/ResetPasswordPage';

// Onboarding
import { OnboardingPage } from '@/pages/onboarding/OnboardingPage';

// Personal Analytics & Dashboard
import { PersonalDashboardPage } from '@/pages/me/PersonalDashboardPage';
import { ActivityPage } from '@/pages/me/ActivityPage';

// Settings
import { ProfileSettingsPage } from '@/pages/settings/ProfileSettingsPage';
import { PrivacySettingsPage } from '@/pages/settings/PrivacySettingsPage';
import { DevicesSettingsPage } from '@/pages/settings/DevicesSettingsPage';
import { ExportsSettingsPage } from '@/pages/settings/ExportsSettingsPage';

// Public & Community
import { PublicProfilePage } from '@/pages/public/PublicProfilePage';
import { LeaderboardPage } from '@/pages/public/LeaderboardPage';
import { CommunityPage } from '@/pages/public/CommunityPage';
import { TeamDashboardPage } from '@/pages/teams/TeamDashboardPage';
import { NotFoundPage } from '@/pages/system/NotFoundPage';

export const RootRedirect: React.FC = () => {
  const { authenticated, loading } = useAuth();
  if (loading) return null;
  return authenticated ? <Navigate to="/leaderboard" replace /> : <Navigate to="/login?return_to=/" replace />;
};

export const App: React.FC = () => {
  return (
    <LocaleProvider>
      <NotificationProvider>
        <AuthProvider>
          <BrowserRouter future={{ v7_startTransition: true, v7_relativeSplatPath: true }}>
            <Routes>
              {/* Standalone Auth & Onboarding */}
              <Route path="/login" element={<LoginPage />} />
              <Route path="/register" element={<RegisterPage />} />
              <Route path="/forgot-password" element={<ForgotPasswordPage />} />
              <Route path="/reset-password" element={<ResetPasswordPage />} />
              <Route path="/onboarding" element={<OnboardingPage />} />

              {/* Main Application Layout */}
              <Route element={<AppLayout />}>
                <Route path="/" element={<RootRedirect />} />
                <Route path="/dashboard" element={<Navigate to="/me" replace />} />
                
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
                <Route path="/teams" element={<TeamDashboardPage />} />

                {/* 404 catch-all */}
                <Route path="*" element={<NotFoundPage />} />
              </Route>
            </Routes>
          </BrowserRouter>
        </AuthProvider>
      </NotificationProvider>
    </LocaleProvider>
  );
};

export default App;
