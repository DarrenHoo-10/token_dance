export type CostAmountLike = {
  amount?: string | number | null;
  currency?: string | null;
  supported?: boolean;
};

/** Non-ISO aliases → ISO 4217 before lookup. */
const CURRENCY_ALIASES: Record<string, string> = {
  RMB: 'CNY',
};

/**
 * Narrow symbols for common currencies.
 * Matches desktop UsagePanel (`currencyDisplay: 'narrowSymbol'`) for USD/CNY/EUR/GBP.
 */
const CURRENCY_SYMBOLS: Record<string, string> = {
  USD: '$',
  CNY: '¥',
  EUR: '€',
  GBP: '£',
  JPY: '¥',
  KRW: '₩',
  INR: '₹',
};

function parseAmount(amount: string | number | null | undefined): number | null {
  if (amount === null || amount === undefined || amount === '') return null;
  const num = typeof amount === 'number' ? amount : parseFloat(amount);
  if (!Number.isFinite(num)) return null;
  return num;
}

function normalizeCurrency(currency: string | null | undefined, fallback = 'USD'): string {
  const raw = (currency || fallback).trim().toUpperCase() || fallback;
  return CURRENCY_ALIASES[raw] ?? raw;
}

function formatMajor(num: number): string {
  return num.toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 });
}

/** Prefer symbols ($ / ¥ / €); unknown codes stay as `CODE 1.00`. */
function formatWithSymbol(num: number, currency: string): string {
  const formatted = formatMajor(num);
  const symbol = CURRENCY_SYMBOLS[currency];
  if (symbol) return `${symbol}${formatted}`;
  try {
    const intl = new Intl.NumberFormat('en-US', {
      style: 'currency',
      currency,
      currencyDisplay: 'narrowSymbol',
      minimumFractionDigits: 2,
      maximumFractionDigits: 2,
    }).format(num);
    // Reject Intl when it still emits the ISO code (e.g. UNK 3.00) or the generic ¤.
    if (intl.includes(currency) || intl.includes('¤')) {
      return `${currency} ${formatted}`;
    }
    return intl;
  } catch {
    return `${currency} ${formatted}`;
  }
}

/** Single-currency card: use symbols ($ / ¥ / €), not ISO codes. */
export function formatCost(
  amount: string | number | null | undefined,
  currency: string | null | undefined = 'USD',
): string | null {
  const num = parseAmount(amount);
  if (num === null) return null;
  return formatWithSymbol(num, normalizeCurrency(currency));
}

/** Multi-currency list, e.g. `$1.00 · ¥7.00`. Never FX-sums. */
export function formatCostList(costs: CostAmountLike[] | null | undefined): string | null {
  const items = (costs ?? []).filter((c) => c.supported !== false && parseAmount(c.amount) !== null);
  if (items.length === 0) return null;
  if (items.length === 1) return formatCost(items[0].amount, items[0].currency);
  return items
    .map((c) => {
      const num = parseAmount(c.amount)!;
      const curr = normalizeCurrency(c.currency, 'UNK');
      return formatWithSymbol(num, curr);
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
