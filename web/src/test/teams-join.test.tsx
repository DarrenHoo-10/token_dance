import { describe, it, expect, beforeEach, vi } from 'vitest';
import { screen } from '@testing-library/react';
import { api } from '@/api/client';
import { teamsApi } from '@/api/teams';
import {
  JOIN_TOKEN_TTL_MS,
  captureJoinToken,
  joinReturnTo,
  readJoinToken,
  redactInviteSecrets,
} from '@/pages/teams/teamUtils';
import { JoinTeamPage } from '@/pages/teams/JoinTeamPage';
import { renderTeams } from './teams-test-helpers';

describe('Join token handling', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    sessionStorage.clear();
    localStorage.clear();
  });

  it('reads the fragment token, clears it, and stores it with a 15 minute TTL', () => {
    const replaceSpy = vi.spyOn(window.history, 'replaceState');
    const token = captureJoinToken('tln_abc', '#key=secret-token-value');

    expect(token).toBe('secret-token-value');
    expect(readJoinToken('tln_abc')).toBe('secret-token-value');
    expect(replaceSpy).toHaveBeenCalled();
    const stored = JSON.parse(sessionStorage.getItem('td.team-join.tln_abc') || '{}');
    expect(stored.expiresAt - Date.now()).toBeLessThanOrEqual(JOIN_TOKEN_TTL_MS);
    expect(stored.expiresAt - Date.now()).toBeGreaterThan(JOIN_TOKEN_TTL_MS - 5_000);
    expect(localStorage.length).toBe(0);
    expect(joinReturnTo('tln_abc')).toBe('/teams/join/tln_abc');
    expect(redactInviteSecrets('https://app.example/teams/join/tln_abc#key=secret-token-value')).not.toContain('secret-token-value');
  });

  it('keeps login return_to on /teams/join/{linkId} and never puts the token in it', async () => {
    vi.spyOn(api, 'getSession').mockResolvedValue({ authenticated: false, user: null });
    const previewSpy = vi.spyOn(teamsApi, 'previewInviteLink');

    renderTeams(<JoinTeamPage />, '/teams/join/tln_abc#key=secret-token-value');

    const login = await screen.findByRole('link', { name: '前往登录' });
    expect(login).toHaveAttribute('href', '/login?return_to=%2Fteams%2Fjoin%2Ftln_abc');
    expect(login.getAttribute('href')).not.toContain('secret-token-value');
    expect(login.getAttribute('href')).not.toContain('key=');
    expect(previewSpy).not.toHaveBeenCalled();
    expect(readJoinToken('tln_abc')).toBe('secret-token-value');
  });

  it('drops expired session tokens and asks the user to reopen the original link', async () => {
    vi.spyOn(api, 'getSession').mockResolvedValue({ authenticated: false, user: null });
    sessionStorage.setItem(
      'td.team-join.tln_abc',
      JSON.stringify({ linkId: 'tln_abc', token: 'old-token', expiresAt: Date.now() - 1 })
    );

    renderTeams(<JoinTeamPage />, '/teams/join/tln_abc');

    expect(await screen.findByText('邀请凭证已失效或丢失，请从原始分享链接重新打开。')).toBeInTheDocument();
    expect(readJoinToken('tln_abc')).toBeNull();
  });
});
