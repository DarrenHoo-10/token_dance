import { useEffect, useMemo, useState } from 'react';
import { publicHomeDay } from '@/utils/publicHomeCache';

// Render the persisted snapshot synchronously, then refresh without clearing it.
export function usePublicHomeResource<T>(
  key: string,
  read: (key: string) => T | null,
  write: (key: string, value: T) => void,
  load: () => Promise<T>,
  refreshTick: number,
) {
  const day = publicHomeDay();
  const scope = `${day}:${key}`;
  const cached = useMemo(() => read(key), [key, day, read]);
  const [state, setState] = useState({ scope, data: cached, failed: false });

  useEffect(() => {
    let active = true;
    setState(previous => previous.scope === scope ? previous : { scope, data: cached, failed: false });
    void load().then(data => {
      if (!active || day !== publicHomeDay()) return;
      write(key, data);
      setState({ scope, data, failed: false });
    }, () => {
      if (!active || day !== publicHomeDay()) return;
      setState(previous => ({ scope, data: previous.scope === scope ? previous.data : cached, failed: true }));
    });
    return () => { active = false; };
  }, [scope, key, day, cached, load, write, refreshTick]);

  // A changed tab/day must never flash the preceding period's values.
  return state.scope === scope ? state : { scope, data: cached, failed: false };
}
