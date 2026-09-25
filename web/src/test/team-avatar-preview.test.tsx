import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { LocaleProvider } from '@/context/LocaleContext';
import { TeamSettingsPage } from '@/pages/teams/TeamSettingsPage';
import { persistTeamAvatar } from '@/pages/teams/TeamShared';
import { teamsApi } from '@/api/teams';
import { sampleScope } from './teams-test-helpers';

const applyScope = vi.fn();
const showToast = vi.fn();
const scope = sampleScope({
  team: { ...sampleScope().team, avatarUrl: '/api/v1/teams/tem_0123456789abcdefghijklmnop/avatar/content' },
  permissions: { ...sampleScope().permissions, transferOwnership: false, dissolve: false },
});

vi.mock('@/context/TeamContext', () => ({ useTeam: () => ({ scope, applyScope, clearScope: vi.fn() }) }));
vi.mock('@/context/NotificationContext', () => ({ useNotification: () => ({ showToast }) }));
vi.mock('@/pages/teams/TeamShared', () => ({ persistTeamAvatar: vi.fn(), teamErrorMessage: () => 'Upload failed' }));
vi.mock('@/pages/teams/TeamSharingCard', () => ({ TeamSharingCard: () => null }));

function showSettings() {
  return render(<LocaleProvider><MemoryRouter><TeamSettingsPage /></MemoryRouter></LocaleProvider>);
}

describe('Team avatar preview', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    Object.defineProperty(URL, 'createObjectURL', { configurable: true, value: vi.fn(() => 'blob:team-preview') });
    Object.defineProperty(URL, 'revokeObjectURL', { configurable: true, value: vi.fn() });
    vi.spyOn(teamsApi, 'getMembers').mockResolvedValue({ members: [], nextCursor: null });
  });
  afterEach(() => vi.restoreAllMocks());

  it('shows the selected image while upload is pending and restores the saved image on failure', async () => {
    let rejectUpload!: (error: Error) => void;
    vi.mocked(persistTeamAvatar).mockReturnValue(new Promise((_, reject) => { rejectUpload = reject; }));
    const { container } = showSettings();
    const originalSrc = container.querySelector<HTMLImageElement>('.tw-avatar-upload img')?.src;
    const file = new File(['image'], 'team.png', { type: 'image/png' });

    fireEvent.change(screen.getByLabelText('更换头像'), { target: { files: [file] } });
    expect(container.querySelector<HTMLImageElement>('.tw-avatar-upload img')).toHaveAttribute('src', 'blob:team-preview');
    expect(persistTeamAvatar).toHaveBeenCalledWith(scope, file);

    await act(async () => rejectUpload(new Error('upload failed')));
    await waitFor(() => expect(container.querySelector<HTMLImageElement>('.tw-avatar-upload img')?.src).toBe(originalSrc));
    expect(URL.revokeObjectURL).toHaveBeenCalledWith('blob:team-preview');
    expect(applyScope).not.toHaveBeenCalled();
  });
});
