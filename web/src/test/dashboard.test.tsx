import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { LocaleProvider } from '@/context/LocaleContext';
import { MetricGrid } from '@/components/analytics/MetricGrid';
import { AgentBreakdown } from '@/components/analytics/AgentBreakdown';
import { SkillRanking } from '@/components/analytics/SkillRanking';
import { SyncStatusCard } from '@/components/analytics/SyncStatusCard';
import type { PersonalSummaryMetrics } from '@/types/api';

describe('Dashboard Components Tests', () => {
  const mockMetrics: PersonalSummaryMetrics = {
    estimatedCost: { amount: '1428.60000000', currency: 'USD', supported: true },
    totalTokens: { value: '325700000', supported: true },
    generatedCodeLines: { value: '864200', supported: true },
    tokensPerCodeLine: { value: '376.88', supported: true },
    inputContextTokens: { value: '184600000', supported: true },
    outputTokens: { value: '78300000', supported: true },
    cacheHitRate: { value: '0.386', supported: true },
    activeDurationMs: { value: '1737360000', supported: true },
    messageCount: { value: '42800', supported: true },
    userMessageCount: { value: '18400', supported: true },
  };

  it('renders all 10 core metrics in MetricGrid', () => {
    render(
      <LocaleProvider>
        <MetricGrid metrics={mockMetrics} />
      </LocaleProvider>
    );

    expect(screen.getByText('预估费用')).toBeInTheDocument();
    expect(screen.getByText('$1,428.60')).toBeInTheDocument();
    expect(screen.getByText('总 Token')).toBeInTheDocument();
    expect(screen.getByText('325.7M')).toBeInTheDocument();
    expect(screen.getByText('生成代码行')).toBeInTheDocument();
    expect(screen.getByText('864.2K')).toBeInTheDocument();
    expect(screen.getByText('单行 Token')).toBeInTheDocument();
    expect(screen.getByText('376.9')).toBeInTheDocument();
    expect(screen.getByText('输入上下文')).toBeInTheDocument();
    expect(screen.getByText('输出 Token')).toBeInTheDocument();
    expect(screen.getByText('缓存命中率')).toBeInTheDocument();
    expect(screen.getByText('38.6%')).toBeInTheDocument();
    expect(screen.getByText('总时长')).toBeInTheDocument();
    expect(screen.getByText('482.6h')).toBeInTheDocument();
    expect(screen.getByText('总消息数')).toBeInTheDocument();
    expect(screen.getByText('用户消息数')).toBeInTheDocument();
  });

  it('renders AgentBreakdown bars accurately', () => {
    const items = [
      { agentId: 'claude-code', displayName: 'Claude Code', tokenTotal: '136800000', percentage: 42 },
      { agentId: 'codex', displayName: 'Codex', tokenTotal: '101000000', percentage: 31 },
    ];

    render(
      <LocaleProvider>
        <AgentBreakdown items={items} />
      </LocaleProvider>
    );

    expect(screen.getByText('Claude Code')).toBeInTheDocument();
    expect(screen.getByText('42%')).toBeInTheDocument();
    expect(screen.getByText('136.8M')).toBeInTheDocument();
  });

  it('renders SkillRanking list with rank badges', () => {
    const skills = [
      { rankNo: 1, skillPublicName: 'codex-review', useCount: 1284, activeDays: 18 },
      { rankNo: 2, skillPublicName: 'commit-context', useCount: 936, activeDays: 14 },
    ];

    render(
      <LocaleProvider>
        <SkillRanking skills={skills} />
      </LocaleProvider>
    );

    expect(screen.getByText('codex-review')).toBeInTheDocument();
    expect(screen.getByText('1,284')).toBeInTheDocument();
    expect(screen.getByText('commit-context')).toBeInTheDocument();
  });

  it('renders SyncStatusCard with health badge', () => {
    render(
      <LocaleProvider>
        <SyncStatusCard
          lastCommittedAt={new Date().toISOString()}
          pendingLocalCount={0}
          status="healthy"
        />
      </LocaleProvider>
    );

    expect(screen.getByText('正常')).toBeInTheDocument();
    expect(screen.getByText('Just now')).toBeInTheDocument();
  });
});
