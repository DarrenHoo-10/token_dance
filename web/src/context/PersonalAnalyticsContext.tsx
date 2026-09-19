import React, { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState } from 'react';
import { X } from 'lucide-react';
import { useAuth } from '@/context/AuthContext';
import { useLocale } from '@/context/LocaleContext';
import { PersonalAnalytics } from '@/pages/me/PersonalAnalytics';
import '@/personal-analytics.css';

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
  const { authenticated } = useAuth();
  const [open, setOpen] = useState(false);
  const show = useCallback(() => setOpen(true), []);
  const hide = useCallback(() => setOpen(false), []);
  const value = useMemo(() => ({ open, show, hide }), [open, show, hide]);

  useEffect(() => {
    if (!authenticated) setOpen(false);
  }, [authenticated]);

  return (
    <PersonalAnalyticsContext.Provider value={value}>
      {children}
      <PersonalAnalyticsDialog open={open} onClose={hide} ready={authenticated} />
    </PersonalAnalyticsContext.Provider>
  );
};

function replayDialogAnimation(node: HTMLDialogElement) {
  try {
    node.style.animation = 'none';
    void node.offsetWidth;
    node.style.animation = '';
  } catch {
    /* jsdom layout is optional */
  }
}

export function PersonalAnalyticsDialog({
  open,
  onClose,
  ready = open,
}: {
  open: boolean;
  onClose: () => void;
  ready?: boolean;
}) {
  const { locale } = useLocale();
  const dialogRef = useRef<HTMLDialogElement>(null);
  const returnFocus = useRef<HTMLElement | null>(null);
  const zh = locale === 'zh-CN';

  useEffect(() => {
    const node = dialogRef.current;
    if (!node) return;
    if (open) {
      if (!node.open) returnFocus.current = document.activeElement as HTMLElement;
      if (typeof node.showModal === 'function' && !node.open) {
        try { node.showModal(); } catch { /* jsdom has no modal dialog */ }
      }
      if (!node.open) node.setAttribute('open', '');
      node.removeAttribute('aria-hidden');
      replayDialogAnimation(node);
      document.body.style.overflow = 'hidden';
    } else {
      if (node.open) {
        try { node.close(); } catch { node.removeAttribute('open'); }
      } else {
        node.removeAttribute('open');
      }
      node.setAttribute('aria-hidden', 'true');
      document.body.style.overflow = '';
      returnFocus.current?.focus();
    }
    return () => { document.body.style.overflow = ''; };
  }, [open]);

  if (!ready && !open) return null;

  return (
    <dialog
      ref={dialogRef}
      className="dialog wide-dialog analytics-dialog"
      onCancel={(event) => { event.preventDefault(); onClose(); }}
      onClick={(event) => { if (event.target === event.currentTarget) onClose(); }}
      aria-labelledby="personal-analytics-heading"
      aria-hidden={open ? undefined : 'true'}
    >
      <div className="dialog-inner">
        <button type="button" className="icon-button close-dialog" onClick={onClose} aria-label={zh ? '关闭' : 'Close'}>
          <X size={21} />
        </button>
        <PersonalAnalytics onLeave={onClose} active={ready || open} />
      </div>
    </dialog>
  );
}
