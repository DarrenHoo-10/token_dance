import React, { useEffect, useRef, useState } from 'react';
import { ChevronDown, Globe2 } from 'lucide-react';
import { useLocale } from '@/context/LocaleContext';

const OPTIONS: { value: 'zh-CN' | 'en-US'; label: string }[] = [
  { value: 'zh-CN', label: '中文' },
  { value: 'en-US', label: 'English' },
];

export const LocaleSwitcher: React.FC<{ compact?: boolean }> = ({ compact = false }) => {
  const { locale, setLocale, t } = useLocale();
  const [open, setOpen] = useState(false);
  const root = useRef<HTMLDivElement>(null);
  const current = OPTIONS.find((item) => item.value === locale) || OPTIONS[0];

  useEffect(() => {
    if (!open) return;
    const onPointer = (event: PointerEvent) => {
      if (!root.current?.contains(event.target as Node)) setOpen(false);
    };
    const onKey = (event: KeyboardEvent) => {
      if (event.key === 'Escape') setOpen(false);
    };
    document.addEventListener('pointerdown', onPointer);
    document.addEventListener('keydown', onKey);
    return () => {
      document.removeEventListener('pointerdown', onPointer);
      document.removeEventListener('keydown', onKey);
    };
  }, [open]);

  if (!compact) {
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
  }

  return (
    <div className="sky-language" ref={root}>
      <button
        type="button"
        className="sky-language-trigger"
        aria-label={t('common.languageSelector')}
        aria-haspopup="listbox"
        aria-expanded={open}
        onClick={() => setOpen((value) => !value)}
      >
        <Globe2 size={18} aria-hidden="true" />
        <span>{current.label}</span>
        <ChevronDown size={14} aria-hidden="true" />
      </button>
      {open && (
        <div className="sky-language-menu" role="listbox" aria-label={t('common.languageSelector')}>
          {OPTIONS.map((option) => (
            <button
              key={option.value}
              type="button"
              role="option"
              aria-selected={option.value === locale}
              onClick={() => {
                setLocale(option.value);
                setOpen(false);
              }}
            >
              {option.label}
            </button>
          ))}
        </div>
      )}
    </div>
  );
};
