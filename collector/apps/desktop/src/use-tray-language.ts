import { useEffect } from 'react';
import { invoke } from '@tauri-apps/api/core';
import { isTauriEnvironment } from './tauri-bridge';

export function useTrayLanguage(language: 'zh' | 'en') {
  useEffect(() => {
    if (isTauriEnvironment()) {
      void invoke('set_tray_language', { language }).catch(error => console.warn('Tray language sync failed', error));
    }
  }, [language]);
}
