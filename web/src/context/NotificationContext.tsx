import React, { createContext, useContext, useState, useCallback, useEffect, useRef } from 'react';
import { useLocale } from './LocaleContext';

export type ToastType = 'success' | 'error' | 'info';

export interface ToastMessage {
  id: string;
  message: string;
  type: ToastType;
}

interface NotificationContextType {
  showToast: (message: string, type?: ToastType) => void;
}

const NotificationContext = createContext<NotificationContextType | undefined>(undefined);

// Errors stay longer: they are the ones people need time to read and act on.
export const TOAST_DURATION_MS: Record<ToastType, number> = { success: 4000, info: 4000, error: 8000 };

export const NotificationProvider: React.FC<{ children: React.ReactNode }> = ({ children }) => {
  const [toasts, setToasts] = useState<ToastMessage[]>([]);
  const timers = useRef(new Map<string, number>());
  const typesRef = useRef(new Map<string, ToastType>());
  const { locale } = useLocale();

  const removeToast = useCallback((id: string) => {
    window.clearTimeout(timers.current.get(id));
    timers.current.delete(id);
    typesRef.current.delete(id);
    setToasts((prev) => prev.filter((t) => t.id !== id));
  }, []);

  const startTimer = useCallback((id: string, type: ToastType) => {
    window.clearTimeout(timers.current.get(id));
    timers.current.set(id, window.setTimeout(() => removeToast(id), TOAST_DURATION_MS[type]));
  }, [removeToast]);

  const pauseTimer = useCallback((id: string) => {
    window.clearTimeout(timers.current.get(id));
    timers.current.delete(id);
  }, []);

  const showToast = useCallback((message: string, type: ToastType = 'info') => {
    const id = Math.random().toString(36).substring(2, 9);
    typesRef.current.set(id, type);
    setToasts((prev) => [...prev, { id, message, type }]);
    startTimer(id, type);
  }, [startTimer]);

  useEffect(() => {
    const active = timers.current;
    return () => { active.forEach((timer) => window.clearTimeout(timer)); active.clear(); };
  }, []);

  return (
    <NotificationContext.Provider value={{ showToast }}>
      {children}
      <div className="toast-container">
        {toasts.map((toast) => (
          <div
            key={toast.id}
            className={`toast-item toast-${toast.type}`}
            // Errors interrupt (alert); everything else is announced politely (status).
            role={toast.type === 'error' ? 'alert' : 'status'}
            onMouseEnter={() => pauseTimer(toast.id)}
            onMouseLeave={() => startTimer(toast.id, toast.type)}
            onFocus={() => pauseTimer(toast.id)}
            onBlur={() => startTimer(toast.id, toast.type)}
          >
            <span className="toast-icon" aria-hidden="true">
              {toast.type === 'success' && '✓'}
              {toast.type === 'error' && '✕'}
              {toast.type === 'info' && 'ℹ'}
            </span>
            <span className="toast-message">{toast.message}</span>
            <button type="button" className="toast-close" onClick={() => removeToast(toast.id)} aria-label={locale === 'en-US' ? 'Dismiss' : '关闭提示'}>×</button>
          </div>
        ))}
      </div>
    </NotificationContext.Provider>
  );
};

export function useNotification(): NotificationContextType {
  const context = useContext(NotificationContext);
  if (!context) {
    throw new Error('useNotification must be used within a NotificationProvider');
  }
  return context;
}
