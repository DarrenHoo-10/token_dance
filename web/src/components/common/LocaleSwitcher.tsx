import React from 'react';
import { useLocale } from '@/context/LocaleContext';

export const LocaleSwitcher: React.FC = () => {
  const { locale, setLocale, t } = useLocale();

  return (
    <div className="locale-switcher" aria-label={t('common.languageSelector')}>
      <button
        type="button"
        className={`locale-btn ${locale === 'zh-CN' ? 'active' : ''}`}
        onClick={() => setLocale('zh-CN')}
      >
        中文
      </button>
      <button
        type="button"
        className={`locale-btn ${locale === 'en-US' ? 'active' : ''}`}
        onClick={() => setLocale('en-US')}
      >
        EN
      </button>
    </div>
  );
};
