import { useEffect, useState } from 'react';

export const releasesApi = `${import.meta.env.BASE_URL}releases/stable.json`;
const MAX_DOWNLOAD = 150 * 1024 * 1024;
const MAX_MANIFEST = 2 * 1024 * 1024;
export interface WindowsRelease {
  version: string; publishedAt: string; prerelease: boolean; bytes: number;
  sha256: string; exeUrl: string; zipUrl?: string; notes: string;
}
type JsonObject = Record<string, unknown>;
const object = (value: unknown): value is JsonObject => value !== null && typeof value === 'object' && !Array.isArray(value);
function version(value: unknown): bigint[] {
  if (typeof value !== 'string' || !/^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/.test(value)) throw new Error('Invalid version');
  const parts = value.split('.').map(BigInt);
  if (parts.some(part => part > 18446744073709551615n)) throw new Error('Invalid version');
  return parts;
}
function compareVersions(left: bigint[], right: bigint[]): number {
  for (let i = 0; i < 3; i++) {
    if (left[i] !== right[i]) return left[i] > right[i] ? 1 : -1;
  }
  return 0;
}
function validDate(value: unknown): value is string {
  if (typeof value !== 'string') return false;
  const match = /^(\d{4})-(\d{2})-(\d{2})T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})$/.exec(value);
  if (!match || !Number.isFinite(Date.parse(value))) return false;
  const [, year, month, day] = match.map(Number);
  const leap = year % 4 === 0 && (year % 100 !== 0 || year % 400 === 0);
  const days = [31, leap ? 29 : 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31];
  return month >= 1 && month <= 12 && day >= 1 && day <= days[month - 1];
}
export function validAssetUrl(value: unknown): value is string {
  if (typeof value !== 'string' || /[\s\\?#]/.test(value)) return false;
  try {
    const url = new URL(value);
    return url.protocol === 'https:' && !url.username && !url.password && !url.search && !url.hash
      && (!url.port || url.port === '443') && url.hostname.includes('.') && !url.hostname.endsWith('.')
      && !/^[\d.]+$/.test(url.hostname) && !url.hostname.includes(':')
      && !/\.(localhost|local|internal)$/.test(url.hostname);
  } catch { return false; }
}
function asset(value: unknown): { url: string; size: number; sha256: string } {
  if (!object(value) || !validAssetUrl(value.url) || typeof value.size !== 'number'
    || !Number.isSafeInteger(value.size) || value.size <= 0 || value.size > MAX_DOWNLOAD
    || typeof value.sha256 !== 'string' || !/^[a-f\d]{64}$/i.test(value.sha256)) throw new Error('Unverified package');
  return { url: value.url, size: value.size, sha256: value.sha256 };
}
export function selectWindowsRelease(payload: unknown): WindowsRelease | null {
  if (!object(payload) || payload.schemaVersion !== 1 || !Array.isArray(payload.releases) || payload.releases.length > 100) throw new Error('Invalid release manifest');
  const seen = new Set<string>();
  let best: { entry: JsonObject; parts: bigint[] } | undefined;
  for (const entry of payload.releases) {
    if (!object(entry)) throw new Error('Invalid release');
    if (entry.platform !== 'windows-x64') continue;
    const parts = version(entry.version);
    const key = String(entry.version);
    if (seen.has(key)) throw new Error('Duplicate release');
    seen.add(key);
    if (!best || compareVersions(parts, best.parts) > 0) best = { entry, parts };
  }
  if (!best) return null;
  const release = best.entry;
  const exe = asset(release.exe);
  const zip = release.zip == null ? undefined : asset(release.zip);
  if (!validDate(release.publishedAt) || typeof release.notes !== 'string') throw new Error('Invalid release details');
  return { version: String(release.version), publishedAt: release.publishedAt.slice(0, 10),
    prerelease: release.prerelease === true, bytes: exe.size, sha256: exe.sha256,
    exeUrl: exe.url, zipUrl: zip?.url, notes: release.notes };
}
async function readManifest(response: Response): Promise<unknown> {
  if (!response.ok || response.redirected || !response.body) throw new Error('Release lookup failed');
  const reader = response.body.getReader();
  const chunks: Uint8Array[] = [];
  let size = 0;
  try {
    while (true) {
      const { done, value } = await reader.read();
      if (done) break;
      size += value.byteLength;
      if (size > MAX_MANIFEST) throw new Error('Release manifest too large');
      chunks.push(value);
    }
  } finally { await reader.cancel(); reader.releaseLock(); }
  const bytes = new Uint8Array(size);
  let offset = 0;
  for (const chunk of chunks) { bytes.set(chunk, offset); offset += chunk.byteLength; }
  return JSON.parse(new TextDecoder('utf-8', { fatal: true }).decode(bytes));
}
export function useWindowsRelease() {
  const [attempt, setAttempt] = useState(0);
  const [state, setState] = useState<{ status: 'loading' | 'ready' | 'empty' | 'error'; release: WindowsRelease | null }>({ status: 'loading', release: null });
  useEffect(() => {
    let disposed = false;
    const controller = new AbortController();
    const timeout = window.setTimeout(() => controller.abort(), 10_000);
    setState({ status: 'loading', release: null });
    void (async () => {
      try {
        const response = await fetch(releasesApi, { signal: controller.signal, credentials: 'omit', cache: 'no-cache', redirect: 'error', headers: { Accept: 'application/json' } });
        const release = selectWindowsRelease(await readManifest(response));
        if (!disposed) setState({ status: release ? 'ready' : 'empty', release });
      } catch {
        if (!disposed) setState({ status: 'error', release: null });
      } finally { window.clearTimeout(timeout); }
    })();
    return () => { disposed = true; controller.abort(); window.clearTimeout(timeout); };
  }, [attempt]);
  return { ...state, retry: () => setAttempt(value => value + 1) };
}
