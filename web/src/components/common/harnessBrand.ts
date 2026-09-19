export interface HarnessBrand {
  id: string;
  known: boolean;
  color: string;
  markBg: string;
  markFg: string;
  border?: string;
  letter: string;
}

interface KnownHarness {
  color: string;
  markBg: string;
  markFg: string;
  border?: string;
}

const KNOWN: Record<string, KnownHarness> = {
  cursor: { color: '#F54E00', markBg: '#F54E00', markFg: '#fff' },
  zcode: { color: '#171717', markBg: '#171717', markFg: '#fff' },
  'claude-code': { color: '#D97757', markBg: '#FAF9F5', markFg: '#D97757', border: '#E8E0D6' },
  codex: { color: '#3941FF', markBg: '#3941FF', markFg: '#fff' },
  'grok-build': { color: '#111111', markBg: '#111111', markFg: '#fff' },
  'deepseek-harness': { color: '#4D6BFE', markBg: '#4D6BFE', markFg: '#fff' },
  opencode: { color: '#211E1E', markBg: '#211E1E', markFg: '#F4F2F1' },
  pi: { color: '#4D9ABF', markBg: '#FFF8F1', markFg: '#111', border: '#E8E0D6' },
  workbuddy: { color: '#01C886', markBg: '#01C886', markFg: '#fff' },
  'doubao-work': { color: '#1E37FC', markBg: '#F4F6FF', markFg: '#1E37FC', border: '#D6DCF8' },
};

const ALIASES: Record<string, string> = {
  cursor: 'cursor',
  zcode: 'zcode',
  'z-code': 'zcode',
  'claude-code': 'claude-code',
  claudecode: 'claude-code',
  codex: 'codex',
  'codex-cli': 'codex',
  openai: 'codex',
  'grok-build': 'grok-build',
  grok: 'grok-build',
  xai: 'grok-build',
  'deepseek-harness': 'deepseek-harness',
  deepseek: 'deepseek-harness',
  opencode: 'opencode',
  'open-code': 'opencode',
  pi: 'pi',
  workbuddy: 'workbuddy',
  'work-buddy': 'workbuddy',
  'doubao-work': 'doubao-work',
  doubao: 'doubao-work',
};

const FALLBACK_COLORS = ['#3D7A2A', '#1B6B8A', '#6B4EA0', '#B86A2A', '#A8446A', '#2A6B6B'];

export function normalizeHarnessKey(value?: string | null): string {
  return (value ?? '')
    .trim()
    .toLowerCase()
    .replace(/[._]+/g, '-')
    .replace(/\s+/g, '-')
    .replace(/-+/g, '-');
}

function firstLetter(value?: string | null): string {
  const ch = Array.from((value ?? '').trim())[0];
  return ch ? ch.toUpperCase() : '?';
}

function hashColor(key: string): string {
  let hash = 0;
  for (const ch of key) hash = (hash * 31 + ch.charCodeAt(0)) >>> 0;
  return FALLBACK_COLORS[hash % FALLBACK_COLORS.length];
}

export function matchHarnessId(agentId?: string | null, label?: string | null): string | null {
  for (const value of [agentId, label]) {
    const key = normalizeHarnessKey(value);
    if (!key) continue;
    if (ALIASES[key]) return ALIASES[key];
  }
  return null;
}

export function resolveHarnessBrand(agentId?: string | null, label?: string | null): HarnessBrand {
  const knownId = matchHarnessId(agentId, label);
  const letter = firstLetter(label || agentId);
  if (knownId) {
    const known = KNOWN[knownId];
    return { id: knownId, known: true, letter, ...known };
  }
  const key = normalizeHarnessKey(agentId || label) || 'unknown';
  const color = hashColor(key);
  return { id: key, known: false, color, markBg: color, markFg: '#fff', letter };
}
