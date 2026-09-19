import { expect, it } from 'vitest';
import { avatarUrl } from '@/utils/avatar';

it('uses compact versions of built-in avatars for existing profile URLs', () => {
  for (const name of ['bunny', 'cat', 'fox', 'panda']) {
    expect(avatarUrl(`/images/avatars/${name}.png`)).toBe(`/images/avatars/${name}-256.jpg`);
  }
  expect(avatarUrl('/api/v1/public/avatars/object')).toBe('/api/v1/public/avatars/object');
  expect(avatarUrl('https://example.test/fox.png')).toBe('https://example.test/fox.png');
});
