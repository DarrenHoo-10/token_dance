import { useEffect } from 'react';
import { useLocation } from 'react-router-dom';

export function useResourceNavigation(title: string) {
  const { pathname, hash } = useLocation();
  useEffect(() => {
    const previous = document.title;
    document.title = `${title} · TokenDance`;
    return () => { document.title = previous; };
  }, [title]);
  useEffect(() => {
    const frame = requestAnimationFrame(() => {
      const target = hash ? document.getElementById(hash.slice(1)) : null;
      if (target) target.scrollIntoView();
      else window.scrollTo(0, 0);
    });
    return () => cancelAnimationFrame(frame);
  }, [pathname, hash]);
}
