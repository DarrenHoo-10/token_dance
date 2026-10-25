import { describe, expect, it } from 'vitest';
import { render } from '@testing-library/react';
import { LocaleProvider } from '@/context/LocaleContext';
import { ModelBreakdown } from '@/components/analytics/ModelBreakdown';
import type { BreakdownItem } from '@/types/api';

const items: BreakdownItem[] = [
  { key: 'claude-opus-5-5', label: 'claude-opus-5-5', tokenTotal: '604418602', percentage: 61.9 },
  { key: 'GLM-5.3-Flash', label: 'GLM-5.3-Flash', tokenTotal: '98033761', percentage: 10.0 },
  { key: 'glm-5.3-flash', label: 'glm-5.3-flash', tokenTotal: '81131313', percentage: 8.3 },
  { key: 'glm-5.3-flash', label: 'glm-5.3-flash', tokenTotal: '0', percentage: 0 },
];

function rows(container: HTMLElement) {
  return [...container.querySelectorAll('.model-share-list li')];
}

describe('ModelBreakdown model variant merge', () => {
  it('merges case variants of one model into a single row', () => {
    const { container } = render(<LocaleProvider><ModelBreakdown items={items} /></LocaleProvider>);
    expect(rows(container).map((li) => li.querySelector('span')?.textContent)).toEqual(['claude-opus-5-5', 'GLM-5.3-Flash']);
    const glm = rows(container)[1];
    expect(glm?.querySelector('b')?.textContent).toBe('18.3%');
    expect(glm?.querySelector('em')?.textContent).toContain('179');
  });

  it('highlights the merged row for any of its raw spellings', () => {
    const { container } = render(<LocaleProvider><ModelBreakdown items={items} selectedModel="glm-5.3-flash" /></LocaleProvider>);
    const [first, glm] = rows(container);
    expect(glm?.className).toContain('selected');
    expect(first?.className).not.toContain('selected');
  });

  it('counts merged models instead of raw rows', () => {
    const { container } = render(<LocaleProvider><ModelBreakdown items={items} /></LocaleProvider>);
    expect(container.querySelector('.model-breakdown-head small')?.textContent).toContain('2');
  });
});

describe('ModelBreakdown provider path folding', () => {
  const deepseekItems: BreakdownItem[] = [
    { key: 'claude-opus-5-5', label: 'claude-opus-5-5', tokenTotal: '9330000', percentage: 79.8 },
    { key: 'accounts/fireworks/models/deepseek-v4p1-flash', label: 'accounts/fireworks/models/deepseek-v4p1-flash', tokenTotal: '1470000', percentage: 12.6 },
    { key: 'deepseek-v4.1-flash', label: 'deepseek-v4.1-flash', tokenTotal: '900000', percentage: 7.6 },
  ];

  it('folds provider-scoped paths into the base model name', () => {
    const { container } = render(<LocaleProvider><ModelBreakdown items={deepseekItems} /></LocaleProvider>);
    expect(rows(container).map((li) => li.querySelector('span')?.textContent)).toEqual(['claude-opus-5-5', 'deepseek-v4.1-flash']);
    const deepseek = rows(container)[1];
    expect(deepseek?.querySelector('b')?.textContent).toBe('20.2%');
    expect(deepseek?.querySelector('em')?.textContent).toContain('2.37M');
  });

  it('renames a solo provider path to its folded base model', () => {
    const solo: BreakdownItem[] = [
      { key: 'accounts/fireworks/models/deepseek-v4p1-flash', label: 'accounts/fireworks/models/deepseek-v4p1-flash', tokenTotal: '1470000', percentage: 100 },
    ];
    const { container } = render(<LocaleProvider><ModelBreakdown items={solo} /></LocaleProvider>);
    expect(rows(container).map((li) => li.querySelector('span')?.textContent)).toEqual(['deepseek-v4.1-flash']);
  });

  it('highlights the folded row for the raw provider-path spelling', () => {
    const { container } = render(<LocaleProvider><ModelBreakdown items={deepseekItems} selectedModel="accounts/fireworks/models/deepseek-v4p1-flash" /></LocaleProvider>);
    const [first, deepseek] = rows(container);
    expect(deepseek?.className).toContain('selected');
    expect(first?.className).not.toContain('selected');
  });
});
