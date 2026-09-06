import { useEffect, useRef } from 'react';

const REFRESH_MS = 30_000;

export function useVisibleRefresh(onRefresh: () => void) {
  const onRefreshRef = useRef(onRefresh);
  onRefreshRef.current = onRefresh;

  useEffect(() => {
    const refreshIfVisible = () => {
      if (document.visibilityState !== 'hidden') onRefreshRef.current();
    };
    const id = window.setInterval(refreshIfVisible, REFRESH_MS);
    document.addEventListener('visibilitychange', refreshIfVisible);
    return () => {
      window.clearInterval(id);
      document.removeEventListener('visibilitychange', refreshIfVisible);
    };
  }, []);
}
