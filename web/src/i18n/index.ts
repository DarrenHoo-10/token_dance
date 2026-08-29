import { zhCN } from './locales/zh-CN';
import { enUS } from './locales/en-US';
import type { Locale } from '@/types/api';

export type TranslationDictionary = typeof zhCN;

export const translations: Record<Locale, TranslationDictionary> = {
  'zh-CN': zhCN,
  'en-US': enUS,
};

export const defaultLocale: Locale = 'zh-CN';

// Helper function to resolve nested keys like "auth.titleLogin"
export function getTranslation(locale: Locale, key: string, params?: Record<string, string | number>): string {
  const dict = translations[locale] || translations[defaultLocale];
  const keys = key.split('.');
  
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  let current: any = dict;
  for (const k of keys) {
    if (current && typeof current === 'object' && k in current) {
      current = current[k];
    } else {
      // Fallback to enUS or key itself
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      let fallback: any = translations['en-US'];
      for (const fk of keys) {
        if (fallback && typeof fallback === 'object' && fk in fallback) {
          fallback = fallback[fk];
        } else {
          fallback = key;
          break;
        }
      }
      current = fallback;
      break;
    }
  }

  if (typeof current !== 'string') {
    return key;
  }

  if (params) {
    let result = current;
    for (const [paramKey, paramValue] of Object.entries(params)) {
      result = result.replace(new RegExp(`\\{${paramKey}\\}`, 'g'), String(paramValue));
    }
    return result;
  }

  return current;
}
