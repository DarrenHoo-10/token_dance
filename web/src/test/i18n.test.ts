import { describe, it, expect } from 'vitest';
import { getApiErrorMessage, getTranslation, translations } from '@/i18n';

describe('I18n Translation Tests', () => {
  it('resolves nested keys for zh-CN and en-US', () => {
    const zhTitle = getTranslation('zh-CN', 'auth.titleLogin');
    const enTitle = getTranslation('en-US', 'auth.titleLogin');

    expect(zhTitle).toBe('使用邮箱登录');
    expect(enTitle).toBe('Sign in with email');
  });

  it('interpolates parameters correctly', () => {
    const zhBanner = getTranslation('zh-CN', 'settings.deletionPendingBanner', { date: '2026-09-06' });
    const enBanner = getTranslation('en-US', 'settings.deletionPendingBanner', { date: '2026-09-06' });

    expect(zhBanner).toContain('2026-09-06');
    expect(enBanner).toContain('2026-09-06');
  });

  it('maps raw API message keys to localized error copy', () => {
    const error = { status: 400, code: 'API_INVALID_ARGUMENT', messageKey: 'api.invalidBody' };

    expect(getApiErrorMessage((key) => getTranslation('zh-CN', key), error)).toBe('请求内容无效，请检查后重试');
    expect(getApiErrorMessage((key) => getTranslation('en-US', key), error)).toBe('The request is invalid. Please review it and try again.');
  });

  it('keeps registration-code errors distinct from login credential errors', () => {
    const error = { status: 400, code: 'AUTH_INVALID_CREDENTIALS', messageKey: 'auth.invalidCode' };

    expect(getApiErrorMessage((key) => getTranslation('zh-CN', key), error)).toBe('验证码不正确，请检查后重试');
    expect(getApiErrorMessage((key) => getTranslation('en-US', key), error)).toBe('The verification code is incorrect. Check it and try again.');
  });

  it('contains consistent root sections in both locales', () => {
    const zhSections = Object.keys(translations['zh-CN']);
    const enSections = Object.keys(translations['en-US']);

    expect(zhSections).toEqual(enSections);
  });

  it('localizes the public ranking and authentication hero labels in Chinese', () => {
    expect(getTranslation('zh-CN', 'publicProfile.globalPosition')).toBe('全球排名');
    expect(getTranslation('zh-CN', 'publicProfile.tokenLeaderboard')).toBe('Token 排行榜');
    expect(getTranslation('zh-CN', 'auth.loginHeroLine1')).toBe('让 Token');
    expect(getTranslation('zh-CN', 'auth.registerHeroLine2')).toBe('账户。');
  });
});
