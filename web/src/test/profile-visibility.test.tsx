import { afterEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { ProfileVisibilitySwitch } from '@/components/analytics/ProfileVisibilitySwitch';
import { LocaleProvider } from '@/context/LocaleContext';
import { NotificationProvider } from '@/context/NotificationContext';
import { api, ApiError } from '@/api/client';
import type { PrivacySettings } from '@/types/api';

const privacy: PrivacySettings = {
  publicProfileEnabled: false, leaderboardVisibility: 'private', privacyVersion: 3,
  showBio: false, showTokenTotal: true, showTrends: false, showActivityCalendar: true,
  showAgentBreakdown: false, showSkillRanking: false, showAchievements: true,
};
function renderSwitch() {
  return render(<LocaleProvider><NotificationProvider><MemoryRouter future={{ v7_startTransition: true, v7_relativeSplatPath: true }}><ProfileVisibilitySwitch /></MemoryRouter></NotificationProvider></LocaleProvider>);
}
afterEach(() => { vi.restoreAllMocks(); localStorage.clear(); });

describe('Personal data visibility', () => {
  it('only changes public visibility, preserves individual sharing preferences, and can turn off again', async () => {
    vi.spyOn(api, 'getPrivacy').mockResolvedValue(privacy);
    const update = vi.spyOn(api, 'updatePrivacy')
      .mockResolvedValueOnce({ ...privacy, publicProfileEnabled: true, leaderboardVisibility: 'public', privacyVersion: 4 })
      .mockResolvedValueOnce({ ...privacy, privacyVersion: 5 });
    renderSwitch();
    const toggle = screen.getByRole('checkbox', { name: '公开我的数据' });
    await waitFor(() => expect(toggle).toBeEnabled());
    expect(toggle).not.toBeChecked();
    expect(update).not.toHaveBeenCalled();
    fireEvent.click(toggle);
    const { privacyVersion, ...preferences } = privacy;
    await waitFor(() => expect(toggle).toBeChecked());
    expect(update).toHaveBeenNthCalledWith(1, { ...preferences, publicProfileEnabled: true, leaderboardVisibility: 'public' }, privacyVersion);
    fireEvent.click(toggle);
    await waitFor(() => expect(toggle).not.toBeChecked());
    expect(update).toHaveBeenNthCalledWith(2, preferences, 4);
  });

  it('does not claim success after a version conflict and reloads preferences before retrying', async () => {
    const refreshed = { ...privacy, showBio: true, privacyVersion: 7 };
    vi.spyOn(api, 'getPrivacy').mockResolvedValueOnce(privacy).mockResolvedValue(refreshed);
    const update = vi.spyOn(api, 'updatePrivacy')
      .mockRejectedValueOnce(new ApiError(412, { code: 'PRIVACY_VERSION_CONFLICT', messageKey: 'errors.unknown' }))
      .mockResolvedValueOnce({ ...refreshed, publicProfileEnabled: true, leaderboardVisibility: 'public', privacyVersion: 8 });
    renderSwitch();
    const toggle = screen.getByRole('checkbox', { name: '公开我的数据' });
    await waitFor(() => expect(toggle).toBeEnabled());
    fireEvent.click(toggle);
    await waitFor(() => expect(api.getPrivacy).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(toggle).toBeEnabled());
    expect(toggle).not.toBeChecked();
    fireEvent.click(toggle);
    await waitFor(() => expect(toggle).toBeChecked());
    expect(update).toHaveBeenLastCalledWith(expect.objectContaining({ showBio: true }), 7);
  });

  it('keeps the dashboard accessible when privacy cannot be loaded', async () => {
    vi.spyOn(api, 'getPrivacy').mockRejectedValue(new Error('offline'));
    renderSwitch();
    expect(await screen.findByRole('status')).toHaveTextContent('不影响查看自己的数据');
    expect(screen.queryByRole('checkbox')).not.toBeInTheDocument();
  });
});
