import { useEffect, useRef } from 'react';
import { invoke } from '@tauri-apps/api/core';
import { isTauriEnvironment } from './tauri-bridge';

// A hidden WebView may throttle animation frames. The bounded fallback keeps
// first launch from waiting forever while still allowing layout/image decoding.
export function useWindowReady(ready: boolean) {
  const sent = useRef(false);
  useEffect(() => {
    if (!ready || sent.current || !isTauriEnvironment()) return;
    let cancelled = false;
    let firstFrame = 0;
    let secondFrame = 0;
    let paintTimer = 0;
    let assetTimer = 0;
    const finish = async () => {
      const assets = [document.fonts.ready, ...Array.from(document.images).map(image => image.decode().catch(() => {}))];
      await Promise.race([Promise.all(assets), new Promise(resolve => { assetTimer = window.setTimeout(resolve, 500); })]);
      if (cancelled) return;
      await new Promise<void>(resolve => {
        paintTimer = window.setTimeout(resolve, 120);
        firstFrame = requestAnimationFrame(() => { secondFrame = requestAnimationFrame(() => resolve()); });
      });
      if (!cancelled) {
        try { await invoke('window_ready'); sent.current = true; }
        catch { /* A closing/replaced WebView should not be reopened. */ }
      }
    };
    void finish();
    return () => {
      cancelled = true;
      cancelAnimationFrame(firstFrame); cancelAnimationFrame(secondFrame);
      clearTimeout(paintTimer); clearTimeout(assetTimer);
    };
  }, [ready]);
}
