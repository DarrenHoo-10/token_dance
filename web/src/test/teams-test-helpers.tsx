import React from 'react';
import { MemoryRouter, Navigate, Route, Routes, useLocation } from 'react-router-dom';
import { render } from '@testing-library/react';
import { LocaleProvider } from '@/context/LocaleContext';
import { NotificationProvider } from '@/context/NotificationContext';
import { AuthProvider } from '@/context/AuthContext';
import { TeamProvider } from '@/context/TeamContext';
import { EMPTY_SHARING, type TeamScope } from '@/api/teams';
import { TeamAnalyticsPage } from '@/pages/teams/TeamAnalyticsPage';
import { TeamLayout } from '@/pages/teams/TeamLayout';
import { TeamMembersPage } from '@/pages/teams/TeamMembersPage';

export const signedInUser = {
  authenticated: true,
  user: {
    userId: 'usr_01',
    displayName: 'Test User',
    handle: 'testuser',
    avatarUrl: null,
    locale: 'zh-CN' as const,
    onboardingRequired: false,
    productState: 'active_private' as const,
  },
};

export function sampleScope(overrides: Partial<TeamScope> = {}): TeamScope {
  return {
    team: {
      id: 'tem_0123456789abcdefghijklmnop',
      name: '星河开发组',
      description: '一起探索 AI 编程',
      timezone: 'Asia/Shanghai',
      visibility: 'private',
      status: 'active',
      profileVersion: '1',
      authRevision: '1',
    },
    membership: {
      id: 'tmb_0123456789abcdefghijklmnop',
      role: 'owner',
      sharingVersion: '1',
      joinedAt: '2026-09-06T08:00:00.000Z',
    },
    permissions: {
      inviteMembers: true,
      assignAdmins: true,
      editProfile: true,
      exportAnalytics: true,
      transferOwnership: true,
      dissolve: true,
      leave: false,
    },
    ...overrides,
  };
}

export function renderTeams(ui: React.ReactElement, route = '/teams') {
  return render(
    <LocaleProvider>
      <NotificationProvider>
        <AuthProvider>
          <MemoryRouter initialEntries={[route]} future={{ v7_startTransition: true, v7_relativeSplatPath: true }}>
            <TeamProvider>
              <Routes>
                <Route path="/teams/new" element={ui} />
                <Route path="/teams/invitations/:invitationId" element={ui} />
                <Route path="/teams/join/:linkId" element={ui} />
                <Route path="/teams/:teamId" element={ui} />
                <Route path="/teams/:teamId/analytics" element={ui} />
                <Route path="/teams/:teamId/members" element={ui} />
                <Route path="/teams/:teamId/settings" element={ui} />
                <Route path="/teams" element={ui} />
                <Route path="*" element={ui} />
              </Routes>
            </TeamProvider>
          </MemoryRouter>
        </AuthProvider>
      </NotificationProvider>
    </LocaleProvider>
  );
}

const AnalyticsRedirect: React.FC = () => {
  const { search } = useLocation();
  return <Navigate to={{ pathname: '..', search }} relative="path" replace />;
};

export function renderTeamWorkspace(route: string) {
  return render(
    <LocaleProvider>
      <NotificationProvider>
        <AuthProvider>
          <MemoryRouter initialEntries={[route]} future={{ v7_startTransition: true, v7_relativeSplatPath: true }}>
            <TeamProvider>
              <Routes>
                <Route path="/teams/:teamId" element={<TeamLayout />}>
                  <Route index element={<TeamAnalyticsPage />} />
                  <Route path="analytics" element={<AnalyticsRedirect />} />
                  <Route path="members" element={<TeamMembersPage />} />
                  <Route path="settings" element={<div>settings-tab</div>} />
                </Route>
              </Routes>
            </TeamProvider>
          </MemoryRouter>
        </AuthProvider>
      </NotificationProvider>
    </LocaleProvider>
  );
}

export { EMPTY_SHARING };
