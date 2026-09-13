import { describe, it, expect } from 'vitest';
import { getTranslation, translations } from '@/i18n';

function flattenKeys(value: unknown, prefix = ''): string[] {
  if (!value || typeof value !== 'object') return [prefix];
  return Object.entries(value as Record<string, unknown>).flatMap(([key, child]) => {
    const next = prefix ? `${prefix}.${key}` : key;
    if (child && typeof child === 'object') return flattenKeys(child, next);
    return [next];
  });
}

describe('teams i18n keys', () => {
  const zhKeys = flattenKeys(translations['zh-CN'].teams);
  const enKeys = flattenKeys(translations['en-US'].teams);

  it('keeps matching teams.* keys in zh-CN and en-US', () => {
    expect(zhKeys).toEqual(enKeys);
  });

  it('covers create, invitation, join, analysis and error keys', () => {
    const required = [
      'teams.entryHeadline',
      'teams.create.alreadyTitle',
      'teams.create.viewMine',
      'teams.invite.acceptTitle',
      'teams.join.submit',
      'teams.join.reopenLink',
      'teams.analytics.updating',
      'teams.analytics.authChanged',
      'teams.errors.TEAM_MEMBERSHIP_EXISTS',
      'teams.errors.TEAM_INVITE_LINK_NOT_FOUND',
    ];
    for (const key of required) {
      expect(getTranslation('zh-CN', key)).not.toBe(key);
      expect(getTranslation('en-US', key)).not.toBe(key);
    }
  });

  it('does not use English action words on the Chinese create and invite surfaces', () => {
    expect(getTranslation('zh-CN', 'teams.create.action')).toBe('创建团队');
    expect(getTranslation('zh-CN', 'teams.invite.action')).toBe('邀请成员');
    expect(getTranslation('zh-CN', 'teams.join.submit')).toBe('加入团队');
    expect(getTranslation('zh-CN', 'teams.settings.saveSharing')).toBe('保存共享设置');
  });
});
