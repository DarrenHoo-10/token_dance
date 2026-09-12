import { describe, it, expect, beforeEach, vi } from 'vitest';
import { fireEvent, screen, waitFor } from '@testing-library/react';
import { api } from '@/api/client';
import { TEAM_JOIN_SHARING, teamsApi } from '@/api/teams';
import { InvitationPage } from '@/pages/teams/InvitationPage';
import { renderTeams, sampleScope, signedInUser } from './teams-test-helpers';

describe('Invitation accept', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    sessionStorage.clear();
  });

  it('shows login entry instead of a spinner when the visitor is signed out', async () => {
    vi.spyOn(api, 'getSession').mockResolvedValue({ authenticated: false, user: null });
    const previewSpy = vi.spyOn(teamsApi, 'getInvitation');

    renderTeams(<InvitationPage />, '/teams/invitations/tiv_01');

    expect(await screen.findByRole('heading', { name: '你收到一份团队邀请' })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: '前往登录' })).toHaveAttribute(
      'href',
      '/login?return_to=%2Fteams%2Finvitations%2Ftiv_01'
    );
    expect(screen.queryByText(/加载中/)).not.toBeInTheDocument();
    expect(previewSpy).not.toHaveBeenCalled();
  });

  it('accepts an email invitation with team sharing enabled automatically', async () => {
    vi.spyOn(api, 'getSession').mockResolvedValue(signedInUser);
    vi.spyOn(teamsApi, 'getMyTeam').mockResolvedValue({ team: null });
    vi.spyOn(teamsApi, 'getInvitation').mockResolvedValue({
      id: 'tiv_01',
      team: { id: 'tem_0123456789abcdefghijklmnop', name: '星河开发组' },
      inviterDisplayName: 'Ada',
      invitedRole: 'member',
      expiresAt: '2026-09-13T08:00:00.000Z',
      version: '1',
      status: 'pending',
    });
    const acceptSpy = vi.spyOn(teamsApi, 'acceptInvitation').mockResolvedValue({
      ...sampleScope(),
      alreadyMember: false,
    });

    renderTeams(<InvitationPage />, '/teams/invitations/tiv_01');

    expect(await screen.findByRole('heading', { name: '确认加入团队' })).toBeInTheDocument();
    expect(screen.getByText('星河开发组')).toBeInTheDocument();
    expect(screen.queryByLabelText('将基础用量计入团队')).not.toBeInTheDocument();
    expect(screen.queryByLabelText('展示我的成员贡献')).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: '加入团队' }));

    await waitFor(() => {
      expect(acceptSpy).toHaveBeenCalledWith(
        'tiv_01',
        { expectedInvitationVersion: '1', sharing: TEAM_JOIN_SHARING },
        expect.objectContaining({ idempotencyKey: expect.any(String) })
      );
    });
  });
});
