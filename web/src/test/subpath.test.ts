import { afterEach, expect, it, vi } from 'vitest';

afterEach(() => {
  vi.unstubAllEnvs();
  vi.unstubAllGlobals();
  vi.resetModules();
});

it('keeps session and collector requests under the deployed application path', async () => {
  vi.stubEnv('BASE_URL', '/token-dance/');
  vi.resetModules();
  const fetchMock = vi.fn().mockResolvedValue({
    ok: true, status: 200, headers: new Headers({ 'Content-Type': 'application/json' }), json: async () => ({}),
  });
  vi.stubGlobal('fetch', fetchMock);
  const { api } = await import('@/api/client');
  await api.getSession();
  expect(fetchMock.mock.calls[0][0]).toBe('/token-dance/api/v1/auth/session');
  await api.claimInstallation({ code: 'code', publicKey: 'key', deviceName: 'Windows', osType: 'windows', osVersion: '11', architecture: 'amd64', collectorVersion: '1.2.0' });
  expect(fetchMock.mock.calls[1][0]).toBe('/token-dance/v1/installations/claim');
});
