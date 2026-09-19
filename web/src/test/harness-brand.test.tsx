import { render } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { HarnessMark } from '@/components/common/HarnessMark';
import { matchHarnessId, resolveHarnessBrand } from '@/components/common/harnessBrand';

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
});
