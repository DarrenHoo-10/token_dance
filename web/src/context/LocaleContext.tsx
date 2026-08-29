import React, { createContext, useContext, useState, useEffect, useCallback } from 'react';
import type { Locale } from '@/types/api';
import { getTranslation, defaultLocale } from '@/i18n';

interface LocaleContextType {
  locale: Locale;
  setLocale: (locale: Locale) => void;
  t: (key: string, params?: Record<string, string | number>) => string;
}

const LocaleContext = createContext<LocaleContextType | undefined>(undefined);

export const LocaleProvider: React.FC<{ children: React.ReactNode }> = ({ children }) => {
  const [locale, setLocaleState] = useState<Locale>(() => {
    const saved = localStorage.getItem('tokendance_locale');
    if (saved === 'zh-CN' || saved === 'en-US') {
      return saved;
    }
    return defaultLocale;
  });

  const setLocale = useCallback((newLocale: Locale) => {
    setLocaleState(newLocale);
    localStorage.setItem('tokendance_locale', newLocale);
    document.documentElement.lang = newLocale === 'en-US' ? 'en' : 'zh-CN';
  }, []);

  useEffect(() => {
    document.documentElement.lang = locale === 'en-US' ? 'en' : 'zh-CN';
  }, [locale]);

  const t = useCallback(
    (key: string, params?: Record<string, string | number>) => {
      return getTranslation(locale, key, params);
    },
    [locale]
  );

  return (
    <LocaleContext.Provider value={{ locale, setLocale, t }}>
      {children}
    </LocaleContext.Provider>
  );
};

export function useLocale(): LocaleContextType {
  const context = useContext(LocaleContext);
  if (!context) {
    throw new Error('useLocale must be used within a LocaleProvider');
  }
  return context;
}
