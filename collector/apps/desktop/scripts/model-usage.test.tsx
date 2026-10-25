import { afterEach, expect, it } from 'vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { ModelUsageList, mergeModelVariants } from '../src/components/ModelUsageList';

afterEach(cleanup);
const models = [1, 4, 2, 3].map(n => ({
  modelKey: n, modelId: `model-${n}`, providerId: 'provider',
  todayTokens: n * 100, weekTokens: (5 - n) * 1000, totalTokens: n * 10000,
}));

it('shows the top three models, expands all, and collapses back to three', () => {
  const { container } = render(<ModelUsageList models={models} range="today" zh />);
  expect([...container.querySelectorAll('.usage-model-name')].map(el => el.textContent)).toEqual(['model-4', 'model-3', 'model-2']);
  expect(container.querySelector('details')?.open).toBe(false);
  (container.querySelector('details') as HTMLDetailsElement).open = true;
  fireEvent.click(screen.getByRole('button', { name: '展开全部 (4)' }));
  expect(screen.getByText('model-1')).toBeTruthy();
  fireEvent.click(screen.getByRole('button', { name: '收起' }));
  expect(screen.queryByText('model-1')).toBeNull();
});

it('ranks each selected period independently and shows no empty explanation', () => {
  const { container, rerender } = render(<ModelUsageList models={models} range="week" zh={false} />);
  expect([...container.querySelectorAll('.usage-model-name')].map(el => el.textContent)).toEqual(['model-1', 'model-2', 'model-3']);
  rerender(<ModelUsageList models={models} range="all" zh={false} />);
  expect(container.querySelector('.usage-model-name')?.textContent).toBe('model-4');
  rerender(<ModelUsageList models={[]} range="today" zh />);
  expect(container.innerHTML).toBe('');
});

it('omits zero usage and the expand button for three or fewer models', () => {
  render(<ModelUsageList models={[...models.slice(0, 2), { ...models[2], todayTokens: 0 }]} range="today" zh />);
  expect(screen.getAllByRole('listitem')).toHaveLength(2);
  expect(screen.queryByRole('button')).toBeNull();
});

it('merges case variants of one model into a single ranked row', () => {
  const glm = [
    { modelKey: 74, modelId: 'glm-5.3-flash', providerId: 'claude-code', todayTokens: 50, weekTokens: 700, totalTokens: 100 },
    { modelKey: 73, modelId: 'GLM-5.3-Flash', providerId: 'claude-code', todayTokens: 100, weekTokens: 0, totalTokens: 200 },
  ];
  const { container } = render(<ModelUsageList models={glm} range="today" zh />);
  expect([...container.querySelectorAll('.usage-model-name')].map(el => el.textContent)).toEqual(['GLM-5.3-Flash']);
  expect(container.querySelector('.usage-model-tokens')?.textContent).toContain('150');
  expect(container.querySelector('.usage-model-name')?.getAttribute('title')).toBe('claude-code / GLM-5.3-Flash');
  // Each period sums its own variants.
  const week = mergeModelVariants(glm);
  expect(week).toEqual([{ modelKey: 73, modelId: 'GLM-5.3-Flash', providerId: 'claude-code', todayTokens: 150, weekTokens: 700, totalTokens: 300 }]);
});

it('merges variants with surrounding whitespace and keeps distinct models separate', () => {
  const mixed = [
    ...models,
    { modelKey: 9, modelId: '  GLM-5.3-Flash ', providerId: 'claude-code', todayTokens: 1, weekTokens: 1, totalTokens: 1 },
    { modelKey: 8, modelId: 'glm-5.3-flash', providerId: 'claude-code', todayTokens: 2, weekTokens: 2, totalTokens: 2 },
  ];
  const merged = mergeModelVariants(mixed);
  // The higher-usage spelling carries the merged row.
  expect(merged.find(model => model.modelId === 'glm-5.3-flash')).toEqual({
    modelKey: 8, modelId: 'glm-5.3-flash', providerId: 'claude-code', todayTokens: 3, weekTokens: 3, totalTokens: 3,
  });
  expect(merged).toHaveLength(models.length + 1);
});
