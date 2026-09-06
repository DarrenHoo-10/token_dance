import { useLocale } from '@/context/LocaleContext';

export function RankChange({ value, isNew = false }: { value?: number | null; isNew?: boolean }) {
  const { locale } = useLocale();
  const zh = locale === 'zh-CN';
  if (isNew) return <span className="trend-neutral">{zh ? '新上榜' : 'New'}</span>;
  if (value == null) return <span className="trend-neutral">{zh ? '暂无对比' : 'No comparison'}</span>;
  if (value === 0) return <span className="trend-neutral">{zh ? '持平' : 'Unchanged'}</span>;
  return <span className={`trend-badge ${value > 0 ? 'positive' : 'negative'}`} aria-label={zh ? `${value > 0 ? '上升' : '下降'} ${Math.abs(value)} 名` : `${value > 0 ? 'Up' : 'Down'} ${Math.abs(value)} places`}>{value > 0 ? '↑' : '↓'} {Math.abs(value)}</span>;
}
