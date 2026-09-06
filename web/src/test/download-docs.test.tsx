import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { LocaleProvider } from '@/context/LocaleContext';
import { LocaleSwitcher } from '@/components/common/LocaleSwitcher';
import { DownloadPage } from '@/pages/resources/DownloadPage';
import { DocsPage } from '@/pages/resources/DocsPage';
import { selectWindowsRelease, releasesApi, releasesUrl } from '@/pages/resources/windowsRelease';

const makeRelease = (tag: string, overrides: Record<string, unknown> = {}) => ({
  tag_name: tag, draft: false, prerelease: true, published_at: '2026-09-06T10:00:00Z',
  assets: [{ name: 'TokenDance.exe', browser_download_url: `${releasesUrl}/download/${tag}/TokenDance.exe`, size: 20000000, digest: `sha256:${'a'.repeat(64)}` }],
  ...overrides,
});
const respond = (payload: unknown, ok = true) => Promise.resolve({ ok, json: async () => payload } as Response);

beforeEach(() => {
  localStorage.clear();
  vi.spyOn(window, 'scrollTo').mockImplementation(() => {});
  Element.prototype.scrollIntoView = vi.fn();
});
afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals(); localStorage.clear(); });

describe('Windows public release selection', () => {
  it('selects the greatest numeric version including GitHub prereleases, regardless of list order', () => {
    const result = selectWindowsRelease([makeRelease('v0.1.9'), makeRelease('v0.1.10'), makeRelease('v0.1.99', { draft: true }), makeRelease('v9.0.0-beta.1'), makeRelease('v0.1.8', { prerelease: false })]);
    expect(result?.version).toBe('0.1.10');
    expect(result?.prerelease).toBe(true);
    expect(result?.exeUrl).toBe(`${releasesUrl}/download/v0.1.10/TokenDance.exe`);
  });
  it('does not fall back to an old version while the newest release is missing its Windows package', () => {
    expect(() => selectWindowsRelease([makeRelease('v0.1.1'), makeRelease('v0.1.2', { assets: [] })])).toThrow();
  });
  it('rejects unexpected hosts, missing hashes, and incomplete responses', () => {
    const source = makeRelease('v0.1.2');
    expect(() => selectWindowsRelease([makeRelease('v0.1.2', { assets: [{ ...source.assets[0], browser_download_url: 'https://example.com/TokenDance.exe' }] })])).toThrow();
    expect(() => selectWindowsRelease([makeRelease('v0.1.2', { assets: [{ ...source.assets[0], digest: null }] })])).toThrow();
    expect(() => selectWindowsRelease({ message: 'rate limited' })).toThrow();
    expect(selectWindowsRelease([])).toBeNull();
  });
});

function page(path = '/download', basename?: string) {
  return render(<LocaleProvider><MemoryRouter basename={basename} initialEntries={[path]} future={{ v7_startTransition: true, v7_relativeSplatPath: true }}><LocaleSwitcher /><Routes><Route path="/download" element={<DownloadPage />} /><Route path="/docs/:slug" element={<DocsPage />} /></Routes></MemoryRouter></LocaleProvider>);
}

describe('Downloads and docs', () => {
  it('loads latest metadata and keeps executable links and version aligned without signing in', async () => {
    const fetchMock = vi.fn(() => respond([makeRelease('v0.1.9'), makeRelease('v0.1.10')]));
    vi.stubGlobal('fetch', fetchMock);
    page();
    const links = await screen.findAllByRole('link', { name: '下载 Windows 版' });
    expect(links).toHaveLength(2);
    for (const link of links) expect(link).toHaveAttribute('href', `${releasesUrl}/download/v0.1.10/TokenDance.exe`);
    expect(screen.getByText(/v0.1.10 · 2026-09-06/)).toBeInTheDocument();
    expect(screen.queryByText(/macOS/)).not.toBeInTheDocument();
    expect(fetchMock).toHaveBeenCalledWith(releasesApi, expect.objectContaining({ credentials: 'omit' }));
    fireEvent.click(screen.getByRole('button', { name: 'EN' }));
    expect(screen.getAllByRole('link', { name: 'Download for Windows' })).toHaveLength(2);
    expect(screen.getByText(/Currently available for Windows only/)).toBeInTheDocument();
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });
  it('shows retry and the public releases page on rate limiting, then recovers to the new version', async () => {
    const fetchMock = vi.fn().mockImplementationOnce(() => respond({}, false)).mockImplementationOnce(() => respond([makeRelease('v0.2.0')]));
    vi.stubGlobal('fetch', fetchMock);
    page();
    expect(await screen.findByRole('alert')).toHaveTextContent('暂时无法获取最新版本');
    expect(screen.queryByRole('link', { name: '下载 Windows 版' })).not.toBeInTheDocument();
    expect(screen.getByRole('link', { name: '前往 GitHub 发布页' })).toHaveAttribute('href', releasesUrl);
    fireEvent.click(screen.getByRole('button', { name: '重新获取' }));
    await waitFor(() => expect(screen.getAllByRole('link', { name: '下载 Windows 版' })[0]).toHaveAttribute('href', `${releasesUrl}/download/v0.2.0/TokenDance.exe`));
  });
  it('renders a distinct empty state', async () => {
    vi.stubGlobal('fetch', vi.fn(() => respond([])));
    page();
    expect(await screen.findByText('暂未发布 Windows 客户端。')).toBeInTheDocument();
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
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
