import { useLocale } from '@/context/LocaleContext';

export function RankChange({ value }: { value?: number | null; isNew?: boolean }) {
  const { locale } = useLocale();
  const zh = locale === 'zh-CN';
  if (value == null || value === 0) return null;
  return <span className={`trend-badge ${value > 0 ? 'positive' : 'negative'}`} aria-label={zh ? `${value > 0 ? '上升' : '下降'} ${Math.abs(value)} 名` : `${value > 0 ? 'Up' : 'Down'} ${Math.abs(value)} places`}>{value > 0 ? '↑' : '↓'} {Math.abs(value)}</span>;
}
