// Web Storage can throw (Safari private mode, blocked site data, sandboxed frames).
// Every access goes through here so a blocked store degrades to in-memory values
// for the current page instead of crashing the app.

export interface SafeStorage {
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
  removeItem(key: string): void;
  keys(): string[];
}

function createSafeStorage(pick: () => Storage): SafeStorage {
  const memory = new Map<string, string>();

  return {
    getItem(key) {
      try {
        const value = pick().getItem(key);
        if (value !== null) return value;
      } catch {
        // fall through to the in-memory copy
      }
      return memory.get(key) ?? null;
    },
    setItem(key, value) {
      try {
        pick().setItem(key, value);
        memory.delete(key);
      } catch {
        memory.set(key, value);
      }
    },
    removeItem(key) {
      memory.delete(key);
      try {
        pick().removeItem(key);
      } catch {
        // nothing stored
      }
    },
    keys() {
      const found = new Set(memory.keys());
      try {
        const storage = pick();
        for (let i = 0; i < storage.length; i += 1) {
          const key = storage.key(i);
          if (key) found.add(key);
        }
      } catch {
        // only in-memory keys are available
      }
      return [...found];
    },
  };
}

export const safeLocalStorage = createSafeStorage(() => window.localStorage);
export const safeSessionStorage = createSafeStorage(() => window.sessionStorage);
