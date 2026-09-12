import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { LocaleProvider } from '@/context/LocaleContext';
import { LocaleSwitcher } from '@/components/common/LocaleSwitcher';
import { DownloadPage } from '@/pages/resources/DownloadPage';
import { DocsPage } from '@/pages/resources/DocsPage';
import { selectWindowsRelease, releasesApi, validAssetUrl } from '@/pages/resources/windowsRelease';
import { macosReleasesApi, selectMacRelease } from '@/pages/resources/macosRelease';

import contract from '../../../schemas/fixtures/desktop-release-manifest.json';
const downloadBase = 'https://downloads.example.com';
const makeRelease = (tag: string, overrides: Record<string, unknown> = {}) => ({
  version: tag, platform: 'windows-x64', prerelease: true, publishedAt: '2026-09-07T00:00:00Z', notes: 'Release notes',
  exe: { url: `${downloadBase}/${tag}/TokenDance.exe`, size: 20000000, sha256: 'a'.repeat(64) }, ...overrides,
});
const manifest = (releases: unknown[]) => ({ schemaVersion: 1, releases });
const respond = (payload: unknown, ok = true) => Promise.resolve(new Response(JSON.stringify(payload), { status: ok ? 200 : 503 }));
const makeMacRelease = (tag: string, architecture = 'arm64', overrides: Record<string, unknown> = {}) => ({
  version: tag, platform: `macos-${architecture}`, publishedAt: '2026-09-12T00:00:00Z',
  minimumSystemVersion: '13.0', notarized: true, notes: 'Mac release', prerelease: false,
  dmg: { url: `${downloadBase}/${tag}/TokenDance-${architecture}.dmg`, size: 90_000_000, sha256: 'b'.repeat(64) },
  ...overrides,
});

describe('macOS DMG releases', () => {
  it('clearly labels unnotarized downloads and explains the first-open step', async () => {
    vi.stubGlobal('fetch', vi.fn(url => respond(url === macosReleasesApi ? manifest([makeMacRelease('0.3.0','arm64',{notarized:false})]) : manifest([]))));
    page();
    expect(await screen.findByText('未公证版本 · 免费分发')).toBeInTheDocument();
    expect(screen.getByText(/首次打开若被 macOS 阻止/)).toBeInTheDocument();
    expect(screen.queryByText('已通过 Apple 公证')).not.toBeInTheDocument();
  });
  it('selects newest versions independently for Apple Silicon and Intel', () => {
    const payload = manifest([makeMacRelease('0.1.9'), makeMacRelease('0.1.10'), makeMacRelease('0.2.0', 'x64'), makeRelease('9.0.0')]);
    expect(selectMacRelease(payload, 'arm64')?.version).toBe('0.1.10');
    expect(selectMacRelease(payload, 'x64')?.version).toBe('0.2.0');
    expect(selectMacRelease(manifest([]), 'x64')).toBeNull();
  });
  it('rejects broken newest packages instead of falling back', () => {
    for (const broken of [
      { dmg: null }, { exe: {} }, { minimumSystemVersion: 'latest' }, { publishedAt: '2026-02-30T00:00:00Z' },
      { dmg: { ...makeMacRelease('0.2.0').dmg, url: 'http://downloads.example.com/a.dmg' } },
      { dmg: { ...makeMacRelease('0.2.0').dmg, url: 'https://downloads.example.com/a.zip' } },
      { dmg: { ...makeMacRelease('0.2.0').dmg, sha256: '' } },
      { dmg: { ...makeMacRelease('0.2.0').dmg, size: 513 * 1024 * 1024 } },
    ]) expect(() => selectMacRelease(manifest([makeMacRelease('0.1.0'), makeMacRelease('0.2.0','arm64',broken)]), 'arm64')).toThrow();
    expect(() => selectMacRelease(manifest([makeMacRelease('1.0.0'), makeMacRelease('1.0.0')]), 'arm64')).toThrow();
  });
  it('shows real DMG links, architecture requirements and independent versions', async () => {
    vi.stubGlobal('fetch', vi.fn((url) => respond(url === macosReleasesApi
      ? manifest([makeMacRelease('0.3.0'), makeMacRelease('0.2.0','x64')]) : manifest([makeRelease('0.1.0')]))));
    page();
    expect(await screen.findByRole('link', {name:'下载 Apple Silicon DMG'})).toHaveAttribute('href', `${downloadBase}/0.3.0/TokenDance-arm64.dmg`);
    expect(screen.getByRole('link', {name:'下载 Intel DMG'})).toHaveAttribute('href', `${downloadBase}/0.2.0/TokenDance-x64.dmg`);
    expect(screen.getByText(/v0.3.0 · macOS 13.0/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', {name:'EN'}));
    expect(screen.getByRole('link', {name:'Download Apple Silicon DMG'})).toBeInTheDocument();
  });
  it('does not invent Mac download links before a package is published', async () => {
    vi.stubGlobal('fetch', vi.fn(url => url === macosReleasesApi ? Promise.resolve(new Response('', {status:404})) : respond(manifest([makeRelease('0.1.0')]))));
    page();
    expect(await screen.findAllByText('该架构暂未发布安装包。')).toHaveLength(2);
    expect(screen.queryByRole('link', {name:'下载 Apple Silicon DMG'})).not.toBeInTheDocument();
    expect(screen.getAllByRole('link', {name:'下载 Windows 版'})).toHaveLength(2);
  });
  it('one invalid Mac architecture does not hide the valid one', async () => {
    vi.stubGlobal('fetch', vi.fn(url => respond(url === macosReleasesApi
      ? manifest([makeMacRelease('0.3.0'), makeMacRelease('0.3.0','x64',{dmg:null})]) : manifest([]))));
    page();
    expect(await screen.findByRole('link', {name:'下载 Apple Silicon DMG'})).toBeInTheDocument();
    expect(screen.queryByRole('link', {name:'下载 Intel DMG'})).not.toBeInTheDocument();
    expect(screen.getByRole('button', {name:'重新获取 Mac 版本'})).toBeInTheDocument();
  });
  it('documents DMG installation and manual updates on Mac', () => {
    page('/docs/install#macos');
    expect(screen.getByRole('heading', {name:'安装 macOS 版'})).toBeInTheDocument();
    expect(screen.getByText(/将 TokenDance 拖入 Applications/)).toBeInTheDocument();
  });
});

beforeEach(() => {
  localStorage.clear();
  vi.spyOn(window, 'scrollTo').mockImplementation(() => {});
  Element.prototype.scrollIntoView = vi.fn();
});
afterEach(() => { vi.useRealTimers(); vi.restoreAllMocks(); vi.unstubAllGlobals(); localStorage.clear(); });

describe('Windows release manifest selection', () => {
  it('selects the greatest numeric Windows version independently of list order', () => {
    const result = selectWindowsRelease(manifest([makeRelease('0.1.9'), makeRelease('0.1.10'), makeRelease('9.0.0', { platform: 'macos-arm64' })]));
    expect(result?.version).toBe('0.1.10');
    expect(result?.prerelease).toBe(true);
    expect(result?.exeUrl).toBe(`${downloadBase}/0.1.10/TokenDance.exe`);
  });
  it('shares its contract fixture with the native updater', () => {
    expect(selectWindowsRelease(contract)).toMatchObject({ version: '0.2.0', bytes: 2, sha256: contract.releases[0].exe.sha256 });
  });
  it('rejects malformed versions, schemas and duplicates', () => {
    for (const v of ['v1.0.0', '01.0.0', '1.0.0-beta.1', '1.0.0+test', '18446744073709551616.0.0']) {
      expect(() => selectWindowsRelease(manifest([makeRelease(v)]))).toThrow();
    }
    expect(() => selectWindowsRelease({ schemaVersion: 2, releases: [] })).toThrow();
    expect(() => selectWindowsRelease(manifest([makeRelease('1.0.0'), makeRelease('1.0.0')]))).toThrow();
  });
  it('does not fall back to an old version when the newest package is incomplete', () => {
    expect(() => selectWindowsRelease(manifest([makeRelease('0.1.1'), makeRelease('0.1.2', { exe: null })]))).toThrow();
  });
  it('rejects insecure URLs, missing hashes, oversized packages and invalid dates', () => {
    for (const url of ['http://cdn.example.com/a.exe', 'file:///tmp/a.exe', 'https://u:p@cdn.example.com/a.exe', 'https://127.0.0.1/a.exe', 'https://[::1]/a.exe', 'https://localhost/a.exe', 'https://host.local/a.exe', 'https://cdn.example.com/a.exe?token=secret', 'https://cdn.example.com:8443/a.exe']) expect(validAssetUrl(url)).toBe(false);
    const r = makeRelease('0.1.2');
    expect(() => selectWindowsRelease(manifest([{ ...r, exe: { ...r.exe, sha256: null } }]))).toThrow();
    expect(() => selectWindowsRelease(manifest([{ ...r, exe: { ...r.exe, size: 151 * 1024 * 1024 } }]))).toThrow();
    expect(() => selectWindowsRelease(manifest([{ ...r, publishedAt: 'invalid' }]))).toThrow();
    expect(() => selectWindowsRelease(manifest([{ ...r, publishedAt: '2026-02-30T00:00:00Z' }]))).toThrow();
    expect(selectWindowsRelease(manifest([]))).toBeNull();
  });
});

function page(path = '/download', basename?: string) {
  return render(<LocaleProvider><MemoryRouter basename={basename} initialEntries={[path]} future={{ v7_startTransition: true, v7_relativeSplatPath: true }}><LocaleSwitcher /><Routes><Route path="/download" element={<DownloadPage />} /><Route path="/docs/:slug" element={<DocsPage />} /></Routes></MemoryRouter></LocaleProvider>);
}

describe('Downloads and docs', () => {
  it('loads latest metadata and keeps executable links and version aligned without signing in', async () => {
    const fetchMock = vi.fn(() => respond(manifest([makeRelease('0.1.9'), makeRelease('0.1.10')])));
    vi.stubGlobal('fetch', fetchMock);
    page();
    const links = await screen.findAllByRole('link', { name: '下载 Windows 版' });
    expect(links).toHaveLength(2);
    for (const link of links) expect(link).toHaveAttribute('href', `${downloadBase}/0.1.10/TokenDance.exe`);
    expect(screen.getByText(/v0.1.10 · 2026-09-07/)).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'macOS' })).toBeInTheDocument();
    expect(fetchMock).toHaveBeenCalledWith(releasesApi, expect.objectContaining({ credentials: 'omit' }));
    fireEvent.click(screen.getByRole('button', { name: 'EN' }));
    expect(screen.getAllByRole('link', { name: 'Download for Windows' })).toHaveLength(2);
    expect(screen.getByText(/One desktop app connects multiple AI tools/)).toBeInTheDocument();
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });
  it('shows retry on a failed manifest request, then recovers without linking to GitHub', async () => {
    let windowsAttempts = 0;
    const fetchMock = vi.fn(url => url === macosReleasesApi ? respond(manifest([]))
      : ++windowsAttempts === 1 ? respond({}, false) : respond(manifest([makeRelease('0.2.0')])));
    vi.stubGlobal('fetch', fetchMock);
    page();
    expect(await screen.findByRole('alert')).toHaveTextContent('暂时无法获取最新版本');
    expect(screen.queryByRole('link', { name: '下载 Windows 版' })).not.toBeInTheDocument();
    expect(screen.queryByRole('link', { name: /GitHub/ })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '重新获取' }));
    await waitFor(() => expect(screen.getAllByRole('link', { name: '下载 Windows 版' })[0]).toHaveAttribute('href', `${downloadBase}/0.2.0/TokenDance.exe`));
  });
  it('renders a distinct empty state', async () => {
    vi.stubGlobal('fetch', vi.fn(() => respond(manifest([]))));
    page();
    expect(await screen.findByText('暂未发布 Windows 客户端。')).toBeInTheDocument();
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });
  it('rejects oversized metadata and times out stalled requests', async () => {
    vi.stubGlobal('fetch', vi.fn(() => respond({ padding: 'x'.repeat(2 * 1024 * 1024) })));
    const mounted = page();
    expect(await screen.findByRole('alert')).toBeInTheDocument();
    mounted.unmount();
    vi.useFakeTimers();
    vi.stubGlobal('fetch', vi.fn((_url, options) => new Promise((_resolve, reject) => {
      options.signal.addEventListener('abort', () => reject(new Error('aborted')));
    })));
    page();
    await act(async () => { await vi.advanceTimersByTimeAsync(10_000); });
    expect(screen.getByRole('alert')).toBeInTheDocument();
  });
  it('keeps docs and download navigation under the deployment subpath and supports language switching', async () => {
    page('/token-dance/docs/quickstart', '/token-dance');
    expect(screen.getByRole('link', { name: '前往客户端下载' })).toHaveAttribute('href', '/token-dance/download');
    const menu = screen.getByRole('navigation', { name: '文档导航' });
    fireEvent.click(within(menu).getByRole('link', { name: '数据与隐私' }));
    expect(screen.getByRole('heading', { name: '数据与隐私' })).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'EN' }));
    expect(screen.getByRole('heading', { name: 'Data & privacy' })).toBeInTheDocument();
    expect(screen.getByText(/remain on the leaderboard/)).toBeInTheDocument();
  });
  it('opens an FAQ from a table-of-contents link and treats unknown docs as 404', async () => {
    const result = page('/docs/faq');
    fireEvent.click(within(screen.getByRole('navigation', { name: '本页目录' })).getByRole('link', { name: '支持哪些操作系统？' }));
    await waitFor(() => expect(document.querySelector('details#platform')).toHaveAttribute('open'));
    result.unmount();
    page('/docs/missing');
    expect(screen.getByText('404')).toBeInTheDocument();
  });
});
