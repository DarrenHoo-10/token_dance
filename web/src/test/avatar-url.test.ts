import { expect, it } from 'vitest';
import { avatarUrl, teamAvatarUrl } from '@/utils/avatar';

it('uses compact versions of built-in avatars for existing profile URLs', () => {
  for (const name of ['bunny', 'cat', 'fox', 'panda']) {
    expect(avatarUrl(`/images/avatars/${name}.png`)).toBe(`/images/avatars/${name}-256.jpg`);
  }
  expect(avatarUrl('/api/v1/public/avatars/object')).toBe('/api/v1/public/avatars/object');
  expect(avatarUrl('https://example.test/fox.png')).toBe('https://example.test/fox.png');
});

it('versions private team avatar URLs when the team profile changes', () => {
  const team = { id: 'team-1', avatarUrl: '/api/v1/teams/team-1/avatar/content', profileVersion: '2' };
  expect(teamAvatarUrl(team)).toBe(`${import.meta.env.BASE_URL.replace(/\/$/, '')}/api/v1/teams/team-1/avatar/content?v=2`);
  expect(teamAvatarUrl({ ...team, profileVersion: '3' })).not.toBe(teamAvatarUrl(team));
  expect(teamAvatarUrl({ ...team, avatarUrl: null })).toBe('');
});
