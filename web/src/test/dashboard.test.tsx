import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { LocaleProvider } from '@/context/LocaleContext';
import { MetricGrid } from '@/components/analytics/MetricGrid';
import { AgentBreakdown } from '@/components/analytics/AgentBreakdown';
import { SkillRanking } from '@/components/analytics/SkillRanking';
import { SyncStatusCard } from '@/components/analytics/SyncStatusCard';
import { TokenTrendChart } from '@/components/analytics/TokenTrendChart';
import { ActivityCalendar } from '@/components/analytics/ActivityCalendar';
import type { PersonalSummaryMetrics, AgentBreakdownItem, SkillItem } from '@/types/api';

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
    const items: AgentBreakdownItem[] = [
      { key: 'claude-code', label: 'Claude Code', agentId: 'claude-code', displayName: 'Claude Code', tokenTotal: '136800000', percentage: 42 },
      { key: 'codex', label: 'Codex', agentId: 'codex', displayName: 'Codex', tokenTotal: '101000000', percentage: 31 },
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
    const skills: SkillItem[] = [
      { skillId: 'sk_codex_review', skillPublicName: 'codex-review', useCount: '1284', activeDays: 18, rankNo: 1 },
      { skillId: 'sk_commit_context', skillPublicName: 'commit-context', useCount: '936', activeDays: 14, rankNo: 2 },
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
    expect(screen.getByText('刚刚')).toBeInTheDocument();
    expect(screen.getByText('0')).toBeInTheDocument();
  });

  it('ensures pendingLocalCount null stays unknown rather than 0', () => {
    render(
      <LocaleProvider>
        <SyncStatusCard
          lastCommittedAt={null}
          pendingLocalCount={null}
          status="healthy"
        />
      </LocaleProvider>
    );

    expect(screen.getByText('未知')).toBeInTheDocument();
    expect(screen.queryByText('0')).not.toBeInTheDocument();
  });

  it('localizes empty dashboard states and accessibility labels', () => {
    render(
      <LocaleProvider>
        <TokenTrendChart trends={[]} />
        <AgentBreakdown items={[]} />
        <SkillRanking skills={[]} />
        <ActivityCalendar days={[]} />
      </LocaleProvider>
    );

    expect(screen.getByText('当前筛选条件下暂无趋势数据')).toBeInTheDocument();
    expect(screen.getByText('尚未记录 Agent 数据')).toBeInTheDocument();
    expect(screen.getByText('尚未记录 Skill 使用数据')).toBeInTheDocument();
    expect(screen.getByText('较少')).toBeInTheDocument();
    expect(screen.getByText('较多')).toBeInTheDocument();
    expect(screen.getByRole('grid', { name: '活跃度热力图' })).toBeInTheDocument();
  });

  it('ensures supported=false differs from zero and renders N/A', () => {
    const unsupportedMetrics: PersonalSummaryMetrics = {
      estimatedCost: { amount: null, currency: 'USD', supported: false },
      totalTokens: { value: null, supported: false },
      generatedCodeLines: { value: '0', supported: true },
      tokensPerCodeLine: { value: null, supported: false },
      inputContextTokens: { value: null, supported: false },
      outputTokens: { value: null, supported: false },
      cacheHitRate: { value: null, supported: false },
      activeDurationMs: { value: null, supported: false },
      messageCount: { value: null, supported: false },
      userMessageCount: { value: null, supported: false },
    };

    render(
      <LocaleProvider>
        <MetricGrid metrics={unsupportedMetrics} />
      </LocaleProvider>
    );

    expect(screen.getAllByText('N/A').length).toBe(9);
  });
});
