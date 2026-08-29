import { describe, it, expect, beforeEach, vi } from 'vitest';
import { api, ApiError } from '@/api/client';

describe('ApiHttpClient Contract Tests', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    api.setCsrfToken(null);
  });

  it('sets and includes X-CSRF-Token and X-Request-Id on mutating requests', async () => {
    api.setCsrfToken('csrf_test_token_123');

    let capturedHeaders: Headers | undefined;
    let capturedMethod = '';

    global.fetch = vi.fn().mockImplementation((_url, init) => {
      capturedMethod = init?.method;
      capturedHeaders = new Headers(init?.headers);
      return Promise.resolve({
        ok: true,
        status: 200,
        headers: new Headers({ 'Content-Type': 'application/json' }),
        json: () => Promise.resolve({ authenticated: true, user: null }),
      });
    });

    await api.login({ email: 'user@example.com', password: 'password123' });

    expect(capturedMethod).toBe('POST');
    expect(capturedHeaders).toBeDefined();
    expect(capturedHeaders!.get('X-CSRF-Token')).toBe('csrf_test_token_123');
    expect(capturedHeaders!.get('X-Request-Id')).toMatch(/^req_/);
    expect(capturedHeaders!.get('Content-Type')).toContain('application/json');
  });

  it('correctly parses ApiError on HTTP 401 / 400 JSON response', async () => {
    global.fetch = vi.fn().mockResolvedValue({
      ok: false,
      status: 401,
      headers: new Headers({ 'Content-Type': 'application/json' }),
      json: () =>
        Promise.resolve({
          error: {
            code: 'AUTH_INVALID_CREDENTIALS',
            messageKey: 'auth.invalidCredentials',
            requestId: 'req_01error',
          },
        }),
    });

    try {
      await api.login({ email: 'bad@example.com', password: 'wrong' });
      expect.fail('Should have thrown ApiError');
    } catch (err) {
      expect(err).toBeInstanceOf(ApiError);
      const apiErr = err as ApiError;
      expect(apiErr.status).toBe(401);
      expect(apiErr.code).toBe('AUTH_INVALID_CREDENTIALS');
      expect(apiErr.messageKey).toBe('auth.invalidCredentials');
      expect(apiErr.requestId).toBe('req_01error');
    }
  });

  it('targets /v1 prefix for claimInstallation and /api/v1 for other endpoints', async () => {
    const urls: string[] = [];

    global.fetch = vi.fn().mockImplementation((url) => {
      urls.push(url as string);
      return Promise.resolve({
        ok: true,
        status: 200,
        headers: new Headers({ 'Content-Type': 'application/json' }),
        json: () => Promise.resolve({ installationId: 'ins_123', uploadPolicy: { maxBatchBytes: 1024, ingestEndpoint: '/v1/ingest' } }),
      });
    });

    await api.claimInstallation({
      code: 'ABCDEFGH',
      devicePublicKey: 'pk_123',
      deviceName: 'MacBook',
      osType: 'darwin',
      osVersion: '14.0',
      architecture: 'arm64',
      collectorVersion: '1.2.0',
    });

    expect(urls[0]).toContain('/v1/installations/claim');

    await api.getPersonalSummary('30d');
    expect(urls[1]).toContain('/api/v1/me/summary?range=30d');
  });

  it('handles 204 No Content gracefully', async () => {
    global.fetch = vi.fn().mockResolvedValue({
      ok: true,
      status: 204,
      headers: new Headers(),
    });

    await expect(api.logout()).resolves.toBeUndefined();
  });
});
