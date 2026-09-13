import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { fireEvent, screen } from '@testing-library/react';
import { webcrypto } from 'node:crypto';
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

  afterEach(() => {
    vi.unstubAllGlobals();
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
    expect(screen.getByRole('button', { name: '选择图片' })).toBeEnabled();
    expect(screen.queryByLabelText('将基础用量计入团队')).not.toBeInTheDocument();
    expect(screen.queryByLabelText('展示我的成员贡献')).not.toBeInTheDocument();
  });

  it('shows the success screen after create instead of the already-member gate', async () => {
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
    vi.spyOn(teamsApi, 'createTeam').mockResolvedValue(sampleScope({ team: { ...sampleScope().team, name: 'Nexorai' } }));

    renderTeams(<CreateTeamPage />, '/teams/new');
    fireEvent.change(await screen.findByLabelText('团队名称'), { target: { value: 'Nexorai' } });
    fireEvent.click(screen.getByRole('button', { name: '创建团队' }));

    expect(await screen.findByRole('heading', { name: '团队已创建' })).toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: '你已经有一个团队' })).not.toBeInTheDocument();
  });

  it('uploads a chosen avatar after the team is created', async () => {
    vi.stubGlobal('crypto', webcrypto);
    Object.defineProperty(URL, 'createObjectURL', { configurable: true, value: vi.fn(() => 'blob:team-avatar') });
    Object.defineProperty(URL, 'revokeObjectURL', { configurable: true, value: vi.fn() });
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
    const created = sampleScope({ team: { ...sampleScope().team, name: 'Nexorai' } });
    const withAvatar = sampleScope({
      team: { ...created.team, avatarUrl: '/api/v1/teams/tem_0123456789abcdefghijklmnop/avatar/content', profileVersion: '2' },
    });
    vi.spyOn(teamsApi, 'createTeam').mockResolvedValue(created);
    vi.spyOn(teamsApi, 'createAvatarUploadIntent').mockResolvedValue({ objectId: 'obj_avatar' });
    vi.spyOn(teamsApi, 'uploadAvatarContent').mockResolvedValue(undefined);
    vi.spyOn(teamsApi, 'completeAvatarUpload').mockResolvedValue(withAvatar);

    renderTeams(<CreateTeamPage />, '/teams/new');
    fireEvent.change(await screen.findByLabelText('团队名称'), { target: { value: 'Nexorai' } });
    const file = new File([new Uint8Array([1, 2, 3])], 'logo.png', { type: 'image/png' });
    Object.defineProperty(file, 'arrayBuffer', { value: async () => Uint8Array.from([1, 2, 3]).buffer });
    fireEvent.change(screen.getByLabelText('选择图片'), { target: { files: [file] } });
    expect(await screen.findByAltText('团队头像')).toHaveAttribute('src', 'blob:team-avatar');
    fireEvent.click(screen.getByRole('button', { name: '创建团队' }));

    expect(await screen.findByRole('heading', { name: '团队已创建' })).toBeInTheDocument();
    expect(teamsApi.createTeam).toHaveBeenCalled();
    expect(teamsApi.createAvatarUploadIntent).toHaveBeenCalledWith(
      created.team.id,
      expect.objectContaining({ contentType: 'image/png', byteSize: 3 })
    );
    expect(teamsApi.uploadAvatarContent).toHaveBeenCalledWith(created.team.id, 'obj_avatar', file);
    expect(teamsApi.completeAvatarUpload).toHaveBeenCalledWith(created.team.id, 'obj_avatar', { expectedProfileVersion: '1' });
  });

  it('still creates the team when avatar upload fails', async () => {
    vi.stubGlobal('crypto', webcrypto);
    Object.defineProperty(URL, 'createObjectURL', { configurable: true, value: vi.fn(() => 'blob:team-avatar') });
    Object.defineProperty(URL, 'revokeObjectURL', { configurable: true, value: vi.fn() });
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
    vi.spyOn(teamsApi, 'createTeam').mockResolvedValue(sampleScope({ team: { ...sampleScope().team, name: 'Nexorai' } }));
    vi.spyOn(teamsApi, 'createAvatarUploadIntent').mockRejectedValue(new Error('upload failed'));

    renderTeams(<CreateTeamPage />, '/teams/new');
    fireEvent.change(await screen.findByLabelText('团队名称'), { target: { value: 'Nexorai' } });
    const file = new File([new Uint8Array([1, 2, 3])], 'logo.png', { type: 'image/png' });
    Object.defineProperty(file, 'arrayBuffer', { value: async () => Uint8Array.from([1, 2, 3]).buffer });
    fireEvent.change(screen.getByLabelText('选择图片'), { target: { files: [file] } });
    fireEvent.click(screen.getByRole('button', { name: '创建团队' }));

    expect(await screen.findByRole('heading', { name: '团队已创建' })).toBeInTheDocument();
    expect(screen.getByRole('status')).toHaveTextContent('头像上传失败');
  });
});
