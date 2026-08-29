import { describe, it, expect, beforeEach, vi } from 'vitest';
import { api, ApiError } from '@/api/client';
import type {
  PersonalSummary,
  SkillsResponse,
  BreakdownResponse,
  TokenTrendsResponse,
  ExportListResponse,
  DeviceListResponse,
  CompareResponse,
  LeaderboardResponse,
  PublicUserProfile,
  ActivityResponse,
} from '@/types/api';

describe('ApiHttpClient Contract Tests against OpenAPI & Backend Fixtures', () => {
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

  it('targets /v1 prefix for claimInstallation with publicKey naming and /api/v1 for other endpoints', async () => {
    const urls: string[] = [];
    let capturedBody: string | undefined;

    global.fetch = vi.fn().mockImplementation((url, init) => {
      urls.push(url as string);
      capturedBody = init?.body as string;
      return Promise.resolve({
        ok: true,
        status: 200,
        headers: new Headers({ 'Content-Type': 'application/json' }),
        json: () =>
          Promise.resolve({
            installationId: 'ins_123',
            status: 'active',
            uploadPolicy: { maxBatchEvents: 1000, minIntervalSec: 10 },
          }),
      });
    });

    const res = await api.claimInstallation({
      code: '7K9M2P4X',
      publicKey: '0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef',
      deviceName: 'MacBook Pro',
      osType: 'macos',
      osVersion: '14.5',
      architecture: 'arm64',
      collectorVersion: '1.2.0',
    });

    expect(urls[0]).toContain('/v1/installations/claim');
    expect(res.installationId).toBe('ins_123');
    expect(res.status).toBe('active');
    expect(res.uploadPolicy?.maxBatchEvents).toBe(1000);

    const parsedBody = JSON.parse(capturedBody || '{}');
    expect(parsedBody.publicKey).toBe('0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef');
    expect(parsedBody.code).toBe('7K9M2P4X');
    expect(parsedBody.osType).toBe('macos');

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

  it('validates PersonalSummary backend JSON contract', async () => {
    const fixture: PersonalSummary = {
      range: {
        key: '30d',
        from: '2026-07-31T00:00:00Z',
        to: '2026-08-30T00:00:00Z',
        timezone: 'Asia/Shanghai',
      },
      metrics: {
        estimatedCost: { amount: '128.50', currency: 'USD', supported: true },
        totalTokens: { value: '45000000', supported: true },
        generatedCodeLines: { value: '15000', supported: true },
        tokensPerCodeLine: { value: '300.0', supported: true },
        inputContextTokens: { value: '30000000', supported: true },
        outputTokens: { value: '15000000', supported: true },
        cacheHitRate: { value: '0.45', supported: true },
        activeDurationMs: { value: '72000000', supported: true },
        messageCount: { value: '3200', supported: true },
        userMessageCount: { value: '1500', supported: true },
      },
      ranking: {
        visibility: 'public',
        rank: 42,
        delta: 3,
        percentile: 98.5,
      },
      sync: {
        lastCommittedAt: '2026-08-30T10:00:00Z',
        pendingLocalCount: 0,
      },
      dataWatermarkAt: '2026-08-30T10:00:00Z',
      aggregationVersion: 1,
    };

    global.fetch = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      headers: new Headers({ 'Content-Type': 'application/json' }),
      json: () => Promise.resolve(fixture),
    });

    const res = await api.getPersonalSummary('30d');
    expect(res.range.key).toBe('30d');
    expect(res.metrics.totalTokens.value).toBe('45000000');
    expect(res.metrics.estimatedCost.amount).toBe('128.50');
    expect(res.ranking.rank).toBe(42);
    expect(res.sync.lastCommittedAt).toBe('2026-08-30T10:00:00Z');
  });

  it('validates SkillsResponse backend JSON contract (skills array)', async () => {
    const fixture: SkillsResponse = {
      range: {
        key: '30d',
        from: '2026-07-31T00:00:00Z',
        to: '2026-08-30T00:00:00Z',
        timezone: 'UTC',
      },
      skills: [
        {
          skillId: 'sk_git_commit',
          skillPublicName: 'git-commit',
          useCount: '156',
          activeDays: 14,
          successRate: 0.98,
          previousDeltaPct: 15.2,
        },
      ],
      dataWatermarkAt: '2026-08-30T10:00:00Z',
      aggregationVersion: 1,
    };

    global.fetch = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      headers: new Headers({ 'Content-Type': 'application/json' }),
      json: () => Promise.resolve(fixture),
    });

    const res = await api.getPersonalSkills('30d');
    expect(Array.isArray(res.skills)).toBe(true);
    expect(res.skills[0].skillId).toBe('sk_git_commit');
    expect(res.skills[0].useCount).toBe('156');
    expect(res.skills[0].activeDays).toBe(14);
  });

  it('validates TokenTrends and Breakdown backend JSON contracts', async () => {
    const trendsFixture: TokenTrendsResponse = {
      range: { key: '30d', from: '2026-07-31', to: '2026-08-30', timezone: 'UTC' },
      mode: 'total',
      granularity: 'day',
      points: [
        {
          date: '2026-08-30',
          tokenTotal: '1500000',
          inputTokens: '1000000',
          outputTokens: '500000',
          cacheReadTokens: '200000',
          cacheWriteTokens: '50000',
          reasoningTokens: '10000',
        },
      ],
      dataWatermarkAt: '2026-08-30T10:00:00Z',
      aggregationVersion: 1,
    };

    const breakdownFixture: BreakdownResponse = {
      range: { key: '30d', from: '2026-07-31', to: '2026-08-30', timezone: 'UTC' },
      items: [
        { key: 'claude-code', label: 'Claude Code', tokenTotal: '25000000', percentage: 65.5 },
        { key: 'codex', label: 'Codex', tokenTotal: '15000000', percentage: 34.5 },
      ],
      dataWatermarkAt: '2026-08-30T10:00:00Z',
      aggregationVersion: 1,
    };

    global.fetch = vi.fn()
      .mockResolvedValueOnce({
        ok: true,
        status: 200,
        headers: new Headers({ 'Content-Type': 'application/json' }),
        json: () => Promise.resolve(trendsFixture),
      })
      .mockResolvedValueOnce({
        ok: true,
        status: 200,
        headers: new Headers({ 'Content-Type': 'application/json' }),
        json: () => Promise.resolve(breakdownFixture),
      });

    const trends = await api.getTokenTrends({ range: '30d' });
    expect(trends.points?.[0].tokenTotal).toBe('1500000');
    expect(trends.points?.[0].cacheReadTokens).toBe('200000');

    const breakdowns = await api.getAgentBreakdowns('30d');
    expect(breakdowns.items[0].key).toBe('claude-code');
    expect(breakdowns.items[0].percentage).toBe(65.5);
  });

  it('validates ExportListResponse backend JSON contract (exports array and field names)', async () => {
    const fixture: ExportListResponse = {
      exports: [
        {
          exportId: 'exp_998',
          userId: 'usr_001',
          idempotencyKey: 'idemp_key_1',
          exportScope: 'all_aggregates',
          exportFormat: 'csv',
          filter: {},
          jobStatus: 'completed',
          attemptCount: 1,
          fileSize: 4096,
          downloadUrl: 'https://download.tokendance.com/exp_998.csv',
          expiresAt: '2026-09-06T10:00:00Z',
          completedAt: '2026-08-30T10:05:00Z',
          createdAt: '2026-08-30T10:00:00Z',
          updatedAt: '2026-08-30T10:05:00Z',
        },
      ],
    };

    global.fetch = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      headers: new Headers({ 'Content-Type': 'application/json' }),
      json: () => Promise.resolve(fixture),
    });

    const res = await api.getExports();
    expect(Array.isArray(res.exports)).toBe(true);
    expect(res.exports[0].exportId).toBe('exp_998');
    expect(res.exports[0].exportScope).toBe('all_aggregates');
    expect(res.exports[0].exportFormat).toBe('csv');
    expect(res.exports[0].jobStatus).toBe('completed');
    expect(res.exports[0].fileSize).toBe(4096);
  });

  it('validates Activity backend JSON contract (items array)', async () => {
    const fixture: ActivityResponse = {
      items: [
        {
          occurredAt: '2026-08-30T09:30:00Z',
          agentId: 'claude-code',
          modelId: 'claude-3-7-sonnet',
          tokenTotal: '250000',
          inputTokens: '180000',
          outputTokens: '70000',
          sessionCount: 1,
          turnCount: 5,
          deviceName: 'Studio Mac',
          syncStatus: 'normal',
        },
      ],
    };

    global.fetch = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      headers: new Headers({ 'Content-Type': 'application/json' }),
      json: () => Promise.resolve(fixture),
    });

    const res = await api.getActivityRows({ range: '30d' });
    expect(Array.isArray(res.items)).toBe(true);
    expect(res.items[0].agentId).toBe('claude-code');
    expect(res.items[0].tokenTotal).toBe('250000');
  });

  it('validates LeaderboardResponse backend JSON contract (metricValue)', async () => {
    const fixture: LeaderboardResponse = {
      snapshotId: 'snap_global_30d_tokens',
      boardKey: 'global',
      window: '30d',
      metric: 'tokens',
      entries: [
        {
          rankNo: 1,
          handle: 'maxbauer',
          displayName: 'Max Bauer',
          avatarUrl: 'https://avatars.tokendance.com/max.png',
          metricValue: '325700000',
          rankDelta: 0,
        },
      ],
      nextCursor: 'cur_next_page',
      dataWatermarkAt: '2026-08-30T10:00:00Z',
    };

    global.fetch = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      headers: new Headers({ 'Content-Type': 'application/json' }),
      json: () => Promise.resolve(fixture),
    });

    const res = await api.getLeaderboard({ window: '30d', metric: 'tokens' });
    expect(res.entries[0].rankNo).toBe(1);
    expect(res.entries[0].handle).toBe('maxbauer');
    expect(res.entries[0].metricValue).toBe('325700000');
  });

  it('validates CompareResponse backend JSON contract for visible and invisible users', async () => {
    const fixture: CompareResponse = {
      users: [
        {
          handle: 'maxbauer',
          displayName: 'Max Bauer',
          avatarUrl: null,
          visible: true,
          tokenTotal: '325700000',
          rank: 1,
          percentile: 99.0,
          dataWatermarkAt: '2026-08-30T10:00:00Z',
        },
        {
          handle: 'hidden_coder',
          visible: false,
        },
      ],
      generatedAt: '2026-08-30T10:00:00Z',
    };

    global.fetch = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      headers: new Headers({ 'Content-Type': 'application/json' }),
      json: () => Promise.resolve(fixture),
    });

    const res = await api.compareUsers(['maxbauer', 'hidden_coder']);
    expect(res.users[0].visible).toBe(true);
    expect(res.users[0].tokenTotal).toBe('325700000');
    expect(res.users[1].visible).toBe(false);
    expect(res.users[1].displayName).toBeUndefined();
    expect(res.users[1].tokenTotal).toBeUndefined();
  });

  it('validates PublicProfileDTO backend JSON contract (without nested trends/skills)', async () => {
    const fixture: PublicUserProfile = {
      handle: 'sophiadev',
      displayName: 'Sophia Dev',
      avatarUrl: null,
      bio: 'Fullstack AI builder',
      rank: 2,
      rankDelta: 1,
      percentile: 98.5,
      tokenTotal: '210000000',
      activeDays: 25,
      currentStreak: 12,
      dataWatermarkAt: '2026-08-30T10:00:00Z',
      generatedAt: '2026-08-30T10:00:00Z',
      projectionVersion: 4,
      showBio: true,
      showTokenTotal: true,
      showTrends: true,
      showActivityCalendar: true,
      showAgentBreakdown: true,
      showSkillRanking: true,
      showAchievements: false,
    };

    global.fetch = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      headers: new Headers({ 'Content-Type': 'application/json' }),
      json: () => Promise.resolve(fixture),
    });

    const res = await api.getPublicProfile('sophiadev');
    expect(res.handle).toBe('sophiadev');
    expect(res.showTrends).toBe(true);
    expect(res.projectionVersion).toBe(4);
    expect(res.generatedAt).toBe('2026-08-30T10:00:00Z');
  });

  it('validates DeviceListResponse backend JSON contract', async () => {
    const fixture: DeviceListResponse = {
      devices: [
        {
          installationId: 'ins_dev_01',
          deviceName: 'Ubuntu Workstation',
          osType: 'linux',
          osVersion: '24.04',
          architecture: 'x86_64',
          collectorVersion: '1.2.0',
          installationStatus: 'active',
          registeredAt: '2026-08-01T12:00:00Z',
          lastSeenAt: '2026-08-30T10:00:00Z',
          disabledAt: null,
          disabledReason: null,
        },
      ],
    };

    global.fetch = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      headers: new Headers({ 'Content-Type': 'application/json' }),
      json: () => Promise.resolve(fixture),
    });

    const res = await api.getDevices();
    expect(res.devices[0].installationId).toBe('ins_dev_01');
    expect(res.devices[0].osType).toBe('linux');
    expect(res.devices[0].installationStatus).toBe('active');
  });
});
