export type CostAmountLike = {
  amount?: string | number | null;
  currency?: string | null;
  supported?: boolean;
};

function parseAmount(amount: string | number | null | undefined): number | null {
  if (amount === null || amount === undefined || amount === '') return null;
  const num = typeof amount === 'number' ? amount : parseFloat(amount);
  if (!Number.isFinite(num)) return null;
  return num;
}

function formatMajor(num: number): string {
  return num.toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 });
}

/** Single-currency card: USD keeps `$`, other codes stay explicit. */
export function formatCost(
  amount: string | number | null | undefined,
  currency: string | null | undefined = 'USD',
): string | null {
  const num = parseAmount(amount);
  if (num === null) return null;
  const formatted = formatMajor(num);
  const curr = (currency || 'USD').trim() || 'USD';
  return curr === 'USD' ? `$${formatted}` : `${formatted} ${curr}`;
}

/** Multi-currency list, e.g. `USD 1.00 · CNY 7.00`. Never FX-sums. */
export function formatCostList(costs: CostAmountLike[] | null | undefined): string | null {
  const items = (costs ?? []).filter((c) => c.supported !== false && parseAmount(c.amount) !== null);
  if (items.length === 0) return null;
  if (items.length === 1) return formatCost(items[0].amount, items[0].currency);
  return items
    .map((c) => {
      const num = parseAmount(c.amount)!;
      const curr = (c.currency || '').trim() || 'UNK';
      return `${curr} ${formatMajor(num)}`;
    })
    .join(' · ');
}

export function formatPersonalCost(
  scalar: CostAmountLike | null | undefined,
  costs?: CostAmountLike[] | null,
): { value: string | null; supported: boolean } {
  const withAmount = (costs ?? []).filter(
    (c) => c.supported !== false && parseAmount(c.amount) !== null,
  );
  if (withAmount.length > 1) {
    return { value: formatCostList(withAmount), supported: true };
  }
  if (withAmount.length === 1) {
    return { value: formatCost(withAmount[0].amount, withAmount[0].currency), supported: true };
  }
  return {
    value: formatCost(scalar?.amount, scalar?.currency),
    supported: scalar?.supported !== false,
  };
}

export function formatCommunityCost(stats: {
  costAmount?: number | null;
  costs?: Array<{ amount: number; currency: string }> | null;
}): string {
  const list = stats.costs ?? [];
  if (list.length > 1) {
    return formatCostList(list.map((c) => ({ amount: c.amount, currency: c.currency, supported: true }))) ?? '—';
  }
  if (list.length === 1) {
    return formatCost(list[0].amount, list[0].currency) ?? '—';
  }
  if (stats.costAmount != null) {
    return formatCost(stats.costAmount, 'USD') ?? '—';
  }
  return '—';
}
