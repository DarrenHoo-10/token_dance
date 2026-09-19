import React from 'react';
import { ChevronDown, Globe2 } from 'lucide-react';
import { useLocale } from '@/context/LocaleContext';

export const LocaleSwitcher: React.FC<{ compact?: boolean }> = ({ compact = false }) => {
  const { locale, setLocale, t } = useLocale();

  if (compact) return <label className="sky-language-select"><Globe2 size={18} /><select aria-label={t('common.languageSelector')} value={locale} onChange={event => setLocale(event.target.value as 'zh-CN' | 'en-US')}><option value="zh-CN">中文</option><option value="en-US">English</option></select><ChevronDown size={12} /></label>;

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
