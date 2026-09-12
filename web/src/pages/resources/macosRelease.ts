import { useEffect, useState } from 'react';
import { compareVersions, readManifest, validAssetUrl, validDate, version } from './windowsRelease';

export const macosReleasesApi = `${import.meta.env.BASE_URL}releases/macos.json`;
export type MacArchitecture = 'arm64' | 'x64';
export interface MacRelease {
  notarized: boolean; version: string; publishedAt: string; prerelease: boolean; minimumSystemVersion: string;
  bytes: number; sha256: string; dmgUrl: string; notes: string;
}
type Status = 'loading' | 'ready' | 'empty' | 'error';
export type MacReleaseState = { status: Status; release: MacRelease | null };
type Json = Record<string, unknown>;
const object = (value: unknown): value is Json => value !== null && typeof value === 'object' && !Array.isArray(value);
const state = (status: Status): MacReleaseState => ({ status, release: null });

export function selectMacRelease(payload: unknown, architecture: MacArchitecture): MacRelease | null {
  if (!object(payload) || payload.schemaVersion !== 1 || !Array.isArray(payload.releases) || payload.releases.length > 100) throw new Error('Invalid release manifest');
  const seen = new Set<string>();
  let newest: { entry: Json; parts: bigint[] } | null = null;
  for (const entry of payload.releases) {
    if (!object(entry)) throw new Error('Invalid release');
    if (entry.platform !== `macos-${architecture}`) continue;
    const parts = version(entry.version);
    const key = String(entry.version);
    if (seen.has(key)) throw new Error('Duplicate release');
    seen.add(key);
    if (!newest || compareVersions(parts, newest.parts) > 0) newest = { entry, parts };
  }
  if (!newest) return null;
  const entry = newest.entry;
  const dmg = entry.dmg;
  if (!object(dmg) || !validAssetUrl(dmg.url) || !new URL(dmg.url).pathname.endsWith('.dmg')
    || typeof dmg.size !== 'number' || !Number.isSafeInteger(dmg.size) || dmg.size <= 0 || dmg.size > 512 * 1024 * 1024
    || typeof dmg.sha256 !== 'string' || !/^[a-f\d]{64}$/i.test(dmg.sha256)
    || typeof entry.minimumSystemVersion !== 'string' || !/^[1-9]\d*\.\d+(?:\.\d+)?$/.test(entry.minimumSystemVersion)
    || typeof entry.notarized !== 'boolean' || typeof entry.notes !== 'string' || !validDate(entry.publishedAt) || entry.exe != null || entry.zip != null
    || (entry.prerelease != null && typeof entry.prerelease !== 'boolean')) throw new Error('Unverified macOS package');
  return { notarized: entry.notarized, version: String(entry.version), publishedAt: entry.publishedAt.slice(0,10), prerelease: entry.prerelease === true,
    minimumSystemVersion: entry.minimumSystemVersion, bytes: dmg.size, sha256: dmg.sha256, dmgUrl: dmg.url, notes: entry.notes };
}

export function useMacReleases() {
  const [attempt, setAttempt] = useState(0);
  const [releases, setReleases] = useState<Record<MacArchitecture, MacReleaseState>>({ arm64: state('loading'), x64: state('loading') });
  useEffect(() => {
    let disposed = false;
    const controller = new AbortController();
    const timeout = window.setTimeout(() => controller.abort(), 10_000);
    setReleases({ arm64: state('loading'), x64: state('loading') });
    void (async () => {
      try {
        const response = await fetch(macosReleasesApi, { signal: controller.signal, credentials: 'omit', cache: 'no-cache', redirect: 'error', headers: { Accept: 'application/json' } });
        const payload = response.status === 404 && !response.redirected ? { schemaVersion: 1, releases: [] } : await readManifest(response);
        const read = (architecture: MacArchitecture): MacReleaseState => {
          try { const release = selectMacRelease(payload, architecture); return { status: release ? 'ready' : 'empty', release }; }
          catch { return state('error'); }
        };
        if (!disposed) setReleases({ arm64: read('arm64'), x64: read('x64') });
      } catch { if (!disposed) setReleases({ arm64: state('error'), x64: state('error') }); }
      finally { window.clearTimeout(timeout); }
    })();
    return () => { disposed = true; controller.abort(); window.clearTimeout(timeout); };
  }, [attempt]);
  return { ...releases, retry: () => setAttempt(value => value + 1) };
}
