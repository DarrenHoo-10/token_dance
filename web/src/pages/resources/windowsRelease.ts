import { useEffect, useState } from 'react';

export const releasesUrl = 'https://github.com/DarrenHoo-10/token_dance/releases';
export const releasesApi = 'https://api.github.com/repos/DarrenHoo-10/token_dance/releases?per_page=100';

export interface WindowsRelease {
  version: string;
  publishedAt: string;
  prerelease: boolean;
  bytes: number;
  sha256: string;
  exeUrl: string;
  zipUrl?: string;
  checksumsUrl?: string;
  releaseUrl: string;
}

type JsonObject = Record<string, unknown>;
const object = (value: unknown): value is JsonObject => value !== null && typeof value === 'object' && !Array.isArray(value);

// Match the desktop updater's public channel: numeric release tags, including
// GitHub prereleases. Never downgrade to an older tag when the newest is incomplete.
export function selectWindowsRelease(payload: unknown): WindowsRelease | null {
  if (!Array.isArray(payload)) throw new Error('Invalid release response');
  const candidates = payload.filter(object).filter(item => item.draft === false && typeof item.tag_name === 'string' && /^v?(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/.test(item.tag_name));
  candidates.sort((a, b) => {
    const left = String(a.tag_name).replace(/^v/, '').split('.').map(BigInt);
    const right = String(b.tag_name).replace(/^v/, '').split('.').map(BigInt);
    for (let i = 0; i < 3; i++) {
      if (left[i] !== right[i]) return left[i] > right[i] ? -1 : 1;
    }
    return 0;
  });
  const release = candidates[0];
  if (!release) return null;
  if (!Array.isArray(release.assets)) throw new Error('Missing release assets');
  const assets = release.assets.filter(object);
  const asset = assets.find(item => item.name === 'TokenDance.exe');
  const prefix = `${releasesUrl}/download/${release.tag_name}/`;
  if (!asset || asset.browser_download_url !== `${prefix}TokenDance.exe` || typeof asset.size !== 'number' || !Number.isSafeInteger(asset.size) || asset.size <= 0 || typeof asset.digest !== 'string' || !/^sha256:[a-f0-9]{64}$/i.test(asset.digest)) throw new Error('Windows release is not ready');
  if (typeof release.published_at !== 'string' || !Number.isFinite(Date.parse(release.published_at))) throw new Error('Missing release date');
  const optionalUrl = (name: string) => assets.find(item => item.name === name && item.browser_download_url === prefix + name && typeof item.size === 'number' && item.size > 0)?.browser_download_url as string | undefined;
  return {
    version: String(release.tag_name).replace(/^v/, ''),
    publishedAt: release.published_at.slice(0, 10),
    prerelease: release.prerelease === true,
    bytes: asset.size,
    sha256: asset.digest.slice(7),
    exeUrl: asset.browser_download_url,
    zipUrl: optionalUrl('TokenDance-windows-x64.zip'),
    checksumsUrl: optionalUrl('SHA256SUMS.txt'),
    releaseUrl: `${releasesUrl}/tag/${release.tag_name}`,
  };
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
        const response = await fetch(releasesApi, { signal: controller.signal, credentials: 'omit', cache: 'no-cache', headers: { Accept: 'application/vnd.github+json' } });
        if (!response.ok) throw new Error('Release lookup failed');
        const release = selectWindowsRelease(await response.json());
        if (!disposed) setState({ status: release ? 'ready' : 'empty', release });
      } catch {
        if (!disposed) setState({ status: 'error', release: null });
      } finally {
        window.clearTimeout(timeout);
      }
    })();
    return () => { disposed = true; controller.abort(); window.clearTimeout(timeout); };
  }, [attempt]);
  return { ...state, retry: () => setAttempt(value => value + 1) };
}
