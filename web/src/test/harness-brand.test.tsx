import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import type { TeamAnalysisReady } from '@/api/teams';
import { HarnessMark } from '@/components/common/HarnessMark';
import { matchHarnessId, resolveHarnessBrand } from '@/components/common/harnessBrand';
import { LocaleProvider } from '@/context/LocaleContext';
import { TeamUsageMix } from '@/pages/teams/TeamUsageMix';

const CLAUDE_MARK = 'm19.6 66.5';
const GENERIC_SPARKLE = 'M12 1.7';

describe('harness brand identity', () => {
  it('maps known harnesses to distinct brand colors', () => {
    const cursor = resolveHarnessBrand('cursor', 'Cursor');
    const zcode = resolveHarnessBrand('zcode', 'Zcode');
    const claude = resolveHarnessBrand('claude-code', 'Claude Code');
    expect(cursor.known).toBe(true);
    expect(zcode.known).toBe(true);
    expect(claude.known).toBe(true);
    expect(cursor.color).toBe('#F54E00');
    expect(zcode.color).toBe('#171717');
    expect(claude.color).toBe('#D97757');
    expect(new Set([cursor.color, zcode.color, claude.color]).size).toBe(3);
  });

  it('matches labels such as Codex CLI without treating model names as harnesses', () => {
    expect(matchHarnessId(undefined, 'Codex CLI')).toBe('codex');
    expect(matchHarnessId(undefined, 'Claude Code')).toBe('claude-code');
    expect(matchHarnessId('cursor-agent', 'Cursor')).toBe('cursor');
    expect(matchHarnessId(undefined, 'claude-sonnet-4')).toBeNull();
    expect(resolveHarnessBrand('gpt-4.1', 'gpt-4.1').known).toBe(false);
  });

  it('renders brand glyphs instead of shared initials', () => {
    const { container } = render(
      <>
        <HarnessMark agentId="cursor" label="Cursor" />
        <HarnessMark agentId="zcode" label="Zcode" />
        <HarnessMark agentId="claude-code" label="Claude Code" />
        <HarnessMark agentId="codex" label="Codex CLI" />
        <HarnessMark agentId="grok-build" label="Grok" />
        <HarnessMark agentId="opencode" label="OpenCode" />
        <HarnessMark agentId="pi" label="Pi" />
        <HarnessMark agentId="workbuddy" label="WorkBuddy" />
        <HarnessMark agentId="doubao-work" label="Doubao" />
      </>,
    );
    expect(container.querySelectorAll('svg')).toHaveLength(9);
    expect(container.querySelector('[data-harness="codex"] svg')).toBeTruthy();
    expect(container.querySelector('[data-harness="pi"] svg')).toBeTruthy();
    expect(container.textContent).not.toMatch(/π/);
    expect(container.textContent).not.toMatch(/C.*C/);
  });

  it('renders the Claude symbol instead of a generic sparkle', () => {
    const { container } = render(
      <>
        <HarnessMark agentId="claude-code" label="Claude" />
        <HarnessMark agentId="claude" label="Claude" />
        <HarnessMark agentId="anthropic" label="Anthropic" />
      </>,
    );
    const marks = container.querySelectorAll('[data-harness="claude-code"]');
    expect(marks).toHaveLength(3);
    for (const mark of marks) {
      const path = mark.querySelector('path')?.getAttribute('d') ?? '';
      expect(path.startsWith(CLAUDE_MARK)).toBe(true);
      expect(path).not.toContain(GENERIC_SPARKLE);
      expect(mark.querySelector('svg')?.getAttribute('viewBox')).toBe('0 0 100 100');
    }
  });

  it('uses the same Claude mark on the team harness mix', () => {
    render(
      <LocaleProvider>
        <TeamUsageMix
          analysis={{
            agents: {
              items: [
                { id: 'claude-code', label: 'Claude Code', tokens: { state: 'available', value: '1200' }, share: '60' },
                { id: 'codex', label: 'Codex CLI', tokens: { state: 'available', value: '800' }, share: '40' },
              ],
            },
            models: { items: [] },
            skills: { items: [] },
          } as unknown as TeamAnalysisReady}
        />
      </LocaleProvider>,
    );
    expect(screen.getAllByText('Claude Code').length).toBeGreaterThan(0);
    expect(document.body.textContent).not.toContain('✳');
    const claude = document.querySelector('[data-harness="claude-code"] path')?.getAttribute('d') ?? '';
    expect(claude.startsWith(CLAUDE_MARK)).toBe(true);
    expect(document.querySelector('[data-harness="codex"] svg')).toBeTruthy();
  });
});
