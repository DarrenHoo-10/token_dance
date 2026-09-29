import React, { Suspense, createContext, lazy, useCallback, useContext, useEffect, useMemo, useRef, useState } from 'react';
import { X } from 'lucide-react';
import { useAuth } from '@/context/AuthContext';
import { useLocale } from '@/context/LocaleContext';
import { LoadingState } from '@/components/states/LoadingState';
import '@/personal-analytics.css';
import { BackdropDialog } from '@/components/common/BackdropDialog';

interface PersonalAnalyticsContextValue {
  open: boolean;
  show: () => void;
  showPublic: (handle: string) => void;
  hide: () => void;
}

// The dashboard is large; it is fetched the first time the dialog opens.
const PersonalAnalytics = lazy(() => import('@/pages/me/PersonalAnalytics').then((m) => ({ default: m.PersonalAnalytics })));

const PersonalAnalyticsContext = createContext<PersonalAnalyticsContextValue | null>(null);

export function usePersonalAnalytics(): PersonalAnalyticsContextValue {
  return useContext(PersonalAnalyticsContext) ?? { open: false, show() {}, showPublic() {}, hide() {} };
}

export const PersonalAnalyticsProvider: React.FC<{ children: React.ReactNode }> = ({ children }) => {
  const { authenticated, user } = useAuth();
  const [open, setOpen] = useState(false);
  const [publicHandle, setPublicHandle] = useState<string | null>(null);
  const show = useCallback(() => { setPublicHandle(null); setOpen(true); }, []);
  const showPublic = useCallback((handle: string) => {
    if (user?.handle?.toLowerCase() === handle.toLowerCase()) {
      show();
      return;
    }
    setPublicHandle(handle);
    setOpen(true);
  }, [show, user?.handle]);
  const hide = useCallback(() => { setOpen(false); setPublicHandle(null); }, []);
  const value = useMemo(() => ({ open, show, showPublic, hide }), [open, show, showPublic, hide]);

  useEffect(() => {
    if (!authenticated) setOpen(false);
  }, [authenticated]);

  return (
    <PersonalAnalyticsContext.Provider value={value}>
      {children}
      <PersonalAnalyticsDialog open={open} onClose={hide} ready={authenticated || open} publicHandle={publicHandle} />
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
  publicHandle = null,
}: {
  open: boolean;
  onClose: () => void;
  ready?: boolean;
  publicHandle?: string | null;
}) {
  const { locale } = useLocale();
  const dialogRef = useRef<HTMLDialogElement>(null);
  const returnFocus = useRef<HTMLElement | null>(null);
  const [everOpened, setEverOpened] = useState(open);
  const zh = locale === 'zh-CN';

  useEffect(() => {
    if (open) setEverOpened(true);
  }, [open]);

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
    <BackdropDialog
      ref={dialogRef}
      className="dialog wide-dialog analytics-dialog"
      onCancel={(event) => { event.preventDefault(); onClose(); }}
      onClose={onClose}
      aria-labelledby="personal-analytics-heading"
      aria-hidden={open ? undefined : 'true'}
    >
      <div className="analytics-dialog-toolbar">
        <span>{zh ? '个人数据' : 'Personal analytics'}</span>
        <button type="button" className="icon-button close-dialog" onClick={onClose} aria-label={zh ? '关闭' : 'Close'}>
          <X size={21} />
        </button>
      </div>
      <div className="dialog-inner analytics-dialog-scroll">
        {(open || everOpened) && (
          <Suspense fallback={<LoadingState />}>
            <PersonalAnalytics key={publicHandle || 'own'} publicHandle={publicHandle} onLeave={onClose} active={open} />
          </Suspense>
        )}
      </div>
    </BackdropDialog>
  );
}
