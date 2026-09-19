import { describe, expect, it } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { HomeLeaderboard } from '@/components/analytics/HomeLeaderboard';
import { CommunityShareBoard } from '@/components/analytics/CommunityShareBoard';
import { calendarPeriodChange } from '@/components/analytics/calendarPeriodChange';
import { LocaleProvider } from '@/context/LocaleContext';
import { usageColor } from '@/utils/usageColors';

describe('Approved homepage interactions', () => {
  it('expands six entries, searches loaded names and handles, and keeps the full-board route', () => {
    const entries = Array.from({length:8}, (_,i) => ({rankNo:i+1,handle:`builder_${i}`,displayName:`创作者 ${i}`,avatarUrl:null,metricValue:'100'}));
    render(<LocaleProvider><MemoryRouter><HomeLeaderboard entries={entries} window="7d" /></MemoryRouter></LocaleProvider>);
    expect(screen.getAllByRole('row')).toHaveLength(7);
    fireEvent.click(screen.getByRole('button',{name:'展开当前榜单'}));
    expect(screen.getAllByRole('row')).toHaveLength(9);
    fireEvent.change(screen.getByRole('textbox'),{target:{value:'BUILDER_7'}});
    expect(screen.getAllByRole('row')).toHaveLength(2);
    expect(screen.getByRole('link',{name:/创作者 7/})).toHaveAttribute('href','/u/builder_7');
    fireEvent.change(screen.getByRole('textbox'),{target:{value:'absent'}});
    expect(screen.getByText('当前榜单没有匹配的开发者')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button',{name:'清除搜索'}));
    fireEvent.click(screen.getByRole('button',{name:'收起榜单'}));
    expect(screen.getAllByRole('row')).toHaveLength(7);
    expect(screen.getByRole('link',{name:'查看全部开发者'})).toHaveAttribute('href','/leaderboard/list?window=7d');
  });
  it('compares complete calendar windows and refuses missing history or a zero baseline', () => {
    const days=Array.from({length:14},(_,i)=>({date:`2026-09-${String(i+1).padStart(2,'0')}`,level:1,tokenTotal:i<7?'100':'125'}));
    expect(calendarPeriodChange([...days].reverse(),7)).toBe(25);
    expect(calendarPeriodChange(days.slice(1),7)).toBeNull();
    expect(calendarPeriodChange(days.map((day,i)=>i===7?{...day,date:'2026-08-01'}:day),7)).toBeNull();
    expect(calendarPeriodChange(days.map((day,i)=>i<7?{...day,tokenTotal:'0'}:day),7)).toBeNull();
    expect(calendarPeriodChange(days.map((day,i)=>i>=7?{...day,tokenTotal:'0'}:day),7)).toBe(-100);
  });
  it('renders community share boards and keeps the empty caption', () => {
    const { rerender } = render(
      <LocaleProvider>
        <MemoryRouter>
          <CommunityShareBoard
            title="社区模型排行榜"
            helpTo="/docs/sources"
            helpLabel="模型用量说明"
            empty="暂无社区模型用量数据。"
            caption="社区近 7 天 Token 占比 · 按模型"
            items={[{ id: 'gpt-5', label: 'gpt-5', sharePct: 41 }]}
          />
        </MemoryRouter>
      </LocaleProvider>,
    );
    expect(screen.getByRole('heading', { name: '社区模型排行榜' })).toBeInTheDocument();
    expect(screen.getByText('gpt-5')).toBeInTheDocument();
    expect(screen.getByText('41%')).toBeInTheDocument();
    expect((document.querySelector('.tool-track i') as HTMLElement).style.backgroundColor).toBe('rgb(139, 92, 246)');
    expect(usageColor('gpt-5')).toBe('#8B5CF6');
    rerender(
      <LocaleProvider>
        <MemoryRouter>
          <CommunityShareBoard
            title="社区 Skill 排行榜"
            helpTo="/docs/sources"
            helpLabel="Skill 用量说明"
            empty="暂无社区 Skill 用量数据。"
            caption="社区近 7 天 调用占比 · 按 Skill"
            items={[]}
          />
        </MemoryRouter>
      </LocaleProvider>,
    );
    expect(screen.getByRole('heading', { name: '社区 Skill 排行榜' })).toBeInTheDocument();
    expect(screen.getByText('暂无社区 Skill 用量数据。')).toBeInTheDocument();
  });
});
