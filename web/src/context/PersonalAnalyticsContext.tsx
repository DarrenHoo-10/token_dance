import React, { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState } from 'react';
import { X } from 'lucide-react';
import { useLocale } from '@/context/LocaleContext';
import { PersonalAnalytics } from '@/pages/me/PersonalAnalytics';

interface PersonalAnalyticsContextValue {
  open: boolean;
  show: () => void;
  hide: () => void;
}

const PersonalAnalyticsContext = createContext<PersonalAnalyticsContextValue | null>(null);

export function usePersonalAnalytics(): PersonalAnalyticsContextValue {
  return useContext(PersonalAnalyticsContext) ?? { open: false, show() {}, hide() {} };
}

export const PersonalAnalyticsProvider: React.FC<{ children: React.ReactNode }> = ({ children }) => {
  const [open, setOpen] = useState(false);
  const show = useCallback(() => setOpen(true), []);
  const hide = useCallback(() => setOpen(false), []);
  const value = useMemo(() => ({ open, show, hide }), [open, show, hide]);

  return (
    <PersonalAnalyticsContext.Provider value={value}>
      {children}
      <PersonalAnalyticsDialog open={open} onClose={hide} />
    </PersonalAnalyticsContext.Provider>
  );
};

export function PersonalAnalyticsDialog({ open, onClose }: { open: boolean; onClose: () => void }) {
  const { locale } = useLocale();
  const dialogRef = useRef<HTMLDialogElement>(null);
  const zh = locale === 'zh-CN';

  useEffect(() => {
    const node = dialogRef.current;
    if (!node) return;
    if (open) {
      if (typeof node.showModal === 'function' && !node.open) {
        try { node.showModal(); } catch { /* jsdom has no modal dialog */ }
      }
      if (!node.open) node.setAttribute('open', '');
      document.body.style.overflow = 'hidden';
    } else if (node.open) {
      node.close();
      document.body.style.overflow = '';
    }
    return () => { document.body.style.overflow = ''; };
  }, [open]);

  if (!open) return null;

  return (
    <dialog
      ref={dialogRef}
      className="analytics-dialog"
      role="dialog"
      aria-modal="true"
      onCancel={(event) => { event.preventDefault(); onClose(); }}
      onClick={(event) => { if (event.target === event.currentTarget) onClose(); }}
      aria-labelledby="personal-analytics-heading"
    >
      <div className="personal-analytics-shell dialog-inner">
        <button type="button" className="icon-button close-dialog" onClick={onClose} aria-label={zh ? '关闭' : 'Close'}>
          <X size={21} />
        </button>
        <PersonalAnalytics onLeave={onClose} />
      </div>
    </dialog>
  );
}
