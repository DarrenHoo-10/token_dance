import { describe, it, expect, beforeEach, vi } from 'vitest';
import { screen } from '@testing-library/react';
import { api } from '@/api/client';
import { teamsApi } from '@/api/teams';
import { CreateTeamPage } from '@/pages/teams/CreateTeamPage';
import { renderTeams, sampleScope, signedInUser } from './teams-test-helpers';

describe('Create team gate', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    sessionStorage.clear();
    localStorage.clear();
  });

  it('blocks the create form when the account already has a team', async () => {
    vi.spyOn(api, 'getSession').mockResolvedValue(signedInUser);
    vi.spyOn(teamsApi, 'getMyTeam').mockResolvedValue(sampleScope());
    const createSpy = vi.spyOn(teamsApi, 'createTeam');

    renderTeams(<CreateTeamPage />, '/teams/new');

    expect(await screen.findByRole('heading', { name: '你已经有一个团队' })).toBeInTheDocument();
    expect(screen.getByText('星河开发组')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: '查看我的团队' })).toHaveAttribute(
      'href',
      '/teams/tem_0123456789abcdefghijklmnop'
    );
    expect(screen.queryByLabelText('团队名称')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '创建团队' })).not.toBeInTheDocument();
    expect(createSpy).not.toHaveBeenCalled();
  });

  it('opens the create form only after membership is confirmed empty', async () => {
    vi.spyOn(api, 'getSession').mockResolvedValue(signedInUser);
    vi.spyOn(teamsApi, 'getMyTeam').mockResolvedValue({ team: null });
    vi.spyOn(api, 'getProfile').mockResolvedValue({
      userId: 'usr_01',
      displayName: 'Test User',
      handle: 'testuser',
      avatarUrl: null,
      timezone: 'Asia/Shanghai',
      locale: 'zh-CN',
      profileVersion: 1,
    });

    renderTeams(<CreateTeamPage />, '/teams/new');

    expect(await screen.findByLabelText('团队名称')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '创建团队' })).toBeEnabled();
    expect(screen.getByLabelText('将基础用量计入团队')).not.toBeChecked();
    expect(screen.queryByLabelText('展示我的成员贡献')).not.toBeInTheDocument();
  });
});
