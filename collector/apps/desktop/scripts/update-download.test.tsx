import { afterEach, expect, it, vi } from 'vitest';
import { openUpdateDownloads } from '../src/update-state';

const mock = vi.hoisted(() => ({ invoke: vi.fn(), native: true }));
vi.mock('@tauri-apps/api/core', () => ({ invoke: mock.invoke }));
vi.mock('../src/tauri-bridge', () => ({ isTauriEnvironment: () => mock.native }));
afterEach(() => { vi.restoreAllMocks(); vi.clearAllMocks(); mock.native = true; });

it('opens the official release list through the native browser command', async () => {
  await openUpdateDownloads();
  expect(mock.invoke).toHaveBeenCalledExactlyOnceWith('open_website', {
    url: 'https://github.com/DarrenHoo-10/token_dance/releases',
  });
});

it('uses an isolated browser tab in the preview without calling the native installer', async () => {
  mock.native = false;
  const open = vi.spyOn(window, 'open').mockReturnValue(null);
  await openUpdateDownloads();
  expect(open).toHaveBeenCalledExactlyOnceWith(
    'https://github.com/DarrenHoo-10/token_dance/releases', '_blank', 'noopener,noreferrer',
  );
  expect(mock.invoke).not.toHaveBeenCalled();
});
