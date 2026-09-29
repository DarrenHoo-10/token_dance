'use strict';
/* TokenDance local dashboard. Read-only; every value comes from /api/v1/snapshot.
   Text is always inserted with textContent, never parsed as HTML. */

const ICONS = {
  grid: '<rect x="3" y="3" width="7" height="7" rx="1.5"/><rect x="14" y="3" width="7" height="7" rx="1.5"/><rect x="3" y="14" width="7" height="7" rx="1.5"/><rect x="14" y="14" width="7" height="7" rx="1.5"/>',
  activity: '<path d="M3 12h4l3-8 4 16 3-8h4"/>',
  monitor: '<rect x="3" y="4" width="18" height="13" rx="2"/><path d="M8 21h8m-4-4v4"/>',
  terminal: '<rect x="3" y="4" width="18" height="16" rx="2"/><path d="m7 9 3 3-3 3m6 0h4"/>',
  shield: '<path d="M12 3 4 6v6c0 5 8 9 8 9s8-4 8-9V6l-8-3Z"/><path d="m8 12 3 3 5-6"/>',
  refresh: '<path d="M20 10a8 8 0 0 0-14-5L3 8m0-5v5h5M4 14a8 8 0 0 0 14 5l3-3m0 5v-5h-5"/>',
  cloud: '<path d="M6 18a5 5 0 1 1 1-10 6 6 0 0 1 11-1 5.5 5.5 0 0 1 0 11H6Z"/>',
  clock: '<circle cx="12" cy="12" r="9"/><path d="M12 7v5l3 2"/>',
  bolt: '<path d="m13 2-9 12h7l-1 8 10-12h-8l1-8Z"/>',
  coins: '<ellipse cx="12" cy="6" rx="8" ry="3"/><path d="M4 6v6c0 4 16 4 16 0V6M4 12v6c0 4 16 4 16 0v-6"/>',
  code: '<path d="m8 6-6 6 6 6m8-12 6 6-6 6m-3-14-2 16"/>',
  message: '<path d="M4 4h16v12H9l-5 4V4Z"/><path d="M8 8h8m-8 4h5"/>',
  alert: '<path d="m12 3 10 18H2L12 3Z"/><path d="M12 9v5m0 3h.01"/>',
  sparkle: '<path d="m12 3 3 6 6 3-6 3-3 6-3-6-6-3 6-3 3-6Z"/>',
  layers: '<path d="m12 3 10 5-10 5L2 8l10-5Z"/><path d="m3 12 9 5 9-5M3 16l9 5 9-5"/>',
  inbox: '<path d="M3 13h5l1 3h6l1-3h5"/><path d="M5 5h14l2 8v6H3v-6l2-8Z"/>',
};
const NAMES = { codex: 'Codex', 'claude-code': 'Claude Code', 'grok-build': 'Grok Build', cursor: 'Cursor', zcode: 'ZCode', pi: 'Pi', 'deepseek-harness': 'DeepSeek Harness', opencode: 'OpenCode', workbuddy: 'WorkBuddy', 'doubao-work': 'Doubao Work' };
const COLORS = ['#4c7a34', '#b0692a', '#3f7f8e', '#7a62a8', '#8a6d1f', '#a04a5d', '#476a8f', '#6b6f2a', '#7d5a45', '#3f6b5e'];
const RANGE_LABEL = { '24h': '过去 24 小时', day: '今日', '7d': '近 7 天', '30d': '近 30 天', all: '全部时间' };
const VIEWS = { overview: '用量总览', activity: '活动与技能', status: '采集状态' };
const REFRESH_MS = 30000;

const state = { view: 'overview', range: '24h', agent: 'all', data: null, seq: 0, failure: null, loading: false, fetchedAt: null };

/* ---------- DOM helpers ---------- */
function icon(name) {
  const span = document.createElement('span');
  span.innerHTML = `<svg class="icon" aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">${ICONS[name] || ICONS.grid}</svg>`;
  return span.firstChild;
}
function h(tag, props, ...children) {
  const el = document.createElement(tag);
  for (const [key, value] of Object.entries(props || {})) {
    if (value === undefined || value === null || value === false) continue;
    if (key === 'class') el.className = value;
    else if (key === 'text') el.textContent = value;
    else if (key === 'style') for (const [name, v] of Object.entries(value)) el.style.setProperty(name, v);
    else if (key === 'on') for (const [name, fn] of Object.entries(value)) el.addEventListener(name, fn);
    else el.setAttribute(key, value === true ? '' : value);
  }
  for (const child of children.flat()) {
    if (child === null || child === undefined || child === false) continue;
    el.append(child instanceof Node ? child : document.createTextNode(String(child)));
  }
  return el;
}

/* ---------- formatting ---------- */
const pad = (n) => String(n).padStart(2, '0');
const num = (v) => (v === null || v === undefined ? null : Number(v));
function compact(v) {
  const n = num(v);
  if (n === null || Number.isNaN(n)) return '—';
  if (n >= 1e9) return `${(n / 1e9).toFixed(2)}B`;
  if (n >= 1e6) return `${(n / 1e6).toFixed(2)}M`;
  if (n >= 1e3) return `${(n / 1e3).toFixed(1)}K`;
  return String(Math.round(n));
}
const count = (v) => (v === null || v === undefined ? '—' : Number(v).toLocaleString('en-US'));
const beijing = (ms) => new Date(ms + 8 * 3600e3);
const hhmm = (ms) => `${pad(beijing(ms).getUTCHours())}:${pad(beijing(ms).getUTCMinutes())}`;
const mmdd = (ms) => `${pad(beijing(ms).getUTCMonth() + 1)}.${pad(beijing(ms).getUTCDate())}`;
function duration(ms) {
  const n = num(ms);
  if (n === null) return '—';
  const minutes = n / 60000;
  return minutes >= 90 ? `${(minutes / 60).toFixed(1)} h` : `${Math.round(minutes)} min`;
}
function money(cost) {
  const value = Number(BigInt(cost.total)) / cost.unit_scale;
  const symbol = { USD: '$', CNY: '¥' }[cost.currency];
  const text = value.toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 });
  return symbol ? `${symbol}${text}` : `${text} ${cost.currency}`;
}
const agentName = (id) => NAMES[id] || id;
const agentColor = (id, ids) => COLORS[Math.max(0, ids.indexOf(id)) % COLORS.length];

/* ---------- data ---------- */
async function load() {
  const seq = ++state.seq;
  state.loading = true;
  document.getElementById('refresh').classList.add('spinning');
  try {
    const response = await fetch(`/api/v1/snapshot?range=${encodeURIComponent(state.range)}`, { credentials: 'same-origin', cache: 'no-store' });
    if (seq !== state.seq) return; // a newer request owns the page; never mix ranges
    if (response.status === 401) return disconnect('会话已失效或链接已使用过。请在终端重新运行：');
    const body = await response.json();
    if (!response.ok || !body.ok) throw new Error(body.error ? body.error.message : `HTTP ${response.status}`);
    state.data = body.data;
    state.failure = null;
    state.fetchedAt = Date.now();
  } catch (error) {
    if (seq !== state.seq) return;
    if (error instanceof TypeError) return disconnect('本地面板服务已停止，页面上的数据不再更新。请在终端重新运行：');
    state.failure = String(error.message || error);
  } finally {
    if (seq === state.seq) {
      state.loading = false;
      document.getElementById('refresh').classList.remove('spinning');
      render();
    }
  }
}
function disconnect(text) {
  state.seq++;
  document.getElementById('disconnect-text').textContent = text;
  document.getElementById('disconnect').hidden = false;
  document.querySelector('main').inert = true;
  document.querySelector('.sidebar').inert = true;
  const chip = document.getElementById('status-chip');
  chip.className = 'status-chip bad';
  chip.replaceChildren(h('span', { class: 'dot' }), '连接已断开');
}
async function redeemLaunchToken() {
  const match = /(?:^|[#&])launch=([0-9a-f]+)/.exec(location.hash);
  if (!match) return true;
  history.replaceState(null, '', location.pathname + location.search); // the token never stays in the address bar
  const response = await fetch('/api/v1/session', { method: 'POST', credentials: 'same-origin', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ token: match[1] }) });
  if (response.ok) return true;
  disconnect('这个启动链接无效或已被使用。请在终端重新运行：');
  return false;
}

/* ---------- panels ---------- */
const heading = (title, subtitle, right) => h('div', { class: 'panel-heading' }, h('div', null, h('h2', { text: title }), subtitle ? h('p', { text: subtitle }) : null), right);
const panelError = (title, retry = true) => h('div', { class: 'panel-error' }, icon('alert'), h('div', null, h('b', { text: title }), h('p', { text: '其余面板不受影响。' })), retry ? h('button', { class: 'button', text: '重试', on: { click: load } }) : null);
const blockFailed = (name) => state.data.errors && state.data.errors[name];

function metric(label, value, unit, note, symbol, primary) {
  return h('div', { class: `metric ${primary ? 'primary' : ''}` },
    h('div', { class: 'metric-label' }, label, icon(symbol)),
    h('div', { class: 'metric-number' }, value, unit ? h('small', { text: unit }) : null),
    h('div', { class: 'metric-change' }, h('span', { text: note })));
}
function splitCompact(v) {
  const text = compact(v);
  return /[BMK]$/.test(text) ? [text.slice(0, -1), text.slice(-1)] : [text, ''];
}
function metrics(s) {
  const [tv, tu] = splitCompact(s.tokens.value);
  const tokenNote = s.tokens.coverage === 'partial' ? `部分已知（${s.tokens.known_count}/${s.tokens.observed_count} 条）` : s.tokens.value === null ? '暂无可确认的 Token' : '本机已记录';
  const costs = s.costs;
  const costText = costs.length ? costs.map(money).join(' · ') : '—';
  const costNote = costs.length ? (costs.some((c) => c.unpriced_requests > 0) ? '部分请求尚无价格，未计入' : costs.some((c) => c.calculated_requests > 0) ? '含按价格表计算的金额' : '提供方报告的金额') : '暂无价格信息';
  const dur = s.active_duration_ms;
  const mini = [
    ['输入上下文', compact(s.input_context_tokens.value)],
    ['输出 Token', compact(s.output_tokens.value)],
    ['缓存命中率', s.cache_hit_rate.value === null ? '—' : `${(s.cache_hit_rate.value * 100).toFixed(1)}%`],
    ['单行 Token', s.tokens_per_code_line === null ? '—' : s.tokens_per_code_line],
    ['用户消息数', count(s.user_messages)],
  ];
  return [
    h('section', { class: 'metrics-grid', 'aria-label': '核心用量指标' },
      metric('总 Token', tv, tu, tokenNote, 'bolt', true),
      metric('预估费用', costText, '', costNote, 'coins'),
      metric('生成代码行', count(s.code_generated_lines), '', s.code_generated_lines === null ? '来源未提供，不等于 0' : '明确记录的行数', 'code'),
      metric('总消息数', count(s.messages), '', s.messages === null ? '来源未提供，不等于 0' : '轮次开始与完成合计', 'message'),
      metric('会话总时长', duration(dur.value), '', dur.coverage === 'partial' ? '仅含已知时长的记录' : '活动时长', 'clock')),
    h('div', { class: 'mini-metrics' }, mini.map(([label, value]) => h('div', { class: 'mini-metric' }, h('span', { text: label }), h('b', { text: value })))),
  ];
}

function liveStrip() {
  const c = state.data.collection, p = c.process, db = c.database;
  const running = p.state === 'running';
  const last = db && db.last_source_update_ms ? `上次采集 ${mmdd(db.last_source_update_ms)} ${hhmm(db.last_source_update_ms)}` : '尚无采集记录';
  return h('div', { class: `live-strip ${running ? '' : 'stopped'}` },
    h('div', { class: 'live-main' },
      h('span', { class: `dot ${running ? '' : 'amber'}` }),
      h('b', { text: running ? '本机正在采集' : '本机采集未运行' }),
      h('span', { class: 'live-separator' }),
      h('span', { class: 'live-queue', text: db ? `待上传 ${count(db.upload_tasks_pending)} 条（未登录，暂不同步）` : '' }),
      h('span', { class: 'live-extra', text: `· ${last}` }),
      running ? null : h('code', { class: 'inline-code', text: 'tokendance run' })),
    h('button', { class: 'text-button', text: '运行详情', on: { click: () => (location.hash = 'status') } }));
}

/* ---------- trend chart ---------- */
function niceScale(maxV) {
  const rough = maxV / 4, exp = Math.pow(10, Math.floor(Math.log10(rough))), f = rough / exp;
  const step = (f <= 1 ? 1 : f <= 2 ? 2 : f <= 2.5 ? 2.5 : f <= 5 ? 5 : 10) * exp;
  return { step, max: step * 4 };
}
function axisFormat(max) {
  if (max >= 1e9) return (v) => `${+(v / 1e9).toFixed(1)}B`;
  if (max >= 1e6) return (v) => `${+(v / 1e6).toFixed(1)}M`;
  return (v) => `${+(v / 1e3).toFixed(1)}K`;
}
function pointLabel(grain, ms) {
  if (grain === 'hour') return `${hhmm(ms)}`;
  if (grain === 'month') return `${beijing(ms).getUTCFullYear()}.${pad(beijing(ms).getUTCMonth() + 1)}`;
  return mmdd(ms);
}
function trendPanel() {
  const d = state.data;
  if (blockFailed('trend') || !d.trend) return h('section', { class: 'panel' }, heading('Token 用量趋势', RANGE_LABEL[state.range]), panelError('趋势数据加载失败'));
  const ids = (d.agents || []).map((a) => a.agent);
  const select = h('select', { id: 'agent-filter', 'aria-label': '趋势 Agent 筛选', on: { change: (e) => { state.agent = e.target.value; drawChart(); } } },
    h('option', { value: 'all', text: '全部 Agent' }),
    ids.map((id) => h('option', { value: id, text: agentName(id), selected: state.agent === id })));
  const grainText = { hour: '按小时', day: '按天', month: '按月' }[d.trend.grain];
  return h('section', { class: 'panel' },
    heading('Token 用量趋势', `${RANGE_LABEL[state.range]}的使用节奏`, h('span', { class: 'panel-subtag', text: '本机已记录用量' })),
    h('div', { class: 'filters' }, select, h('span', { class: 'filter-note', text: '仅影响趋势图' })),
    h('div', { class: 'chart', id: 'trend-chart' }),
    h('div', { class: 'chart-footer' }, h('span', null, '筛选内 Token ', h('b', { id: 'selection-total' })), h('div', { class: 'legend' }, h('span', null, h('i'), '用量'), h('span', { text: grainText }))),
    h('details', { class: 'data-table' }, h('summary', { text: '以表格查看数据' }), h('div', { class: 'table-scroll', id: 'trend-table' })));
}
function drawChart() {
  const el = document.getElementById('trend-chart');
  if (!el || !state.data || !state.data.trend) return;
  const { grain, points } = state.data.trend;
  const values = points.map((p) => (state.agent === 'all' ? Number(p.tokens) : Number((p.by_agent || {})[state.agent] || 0)));
  const sum = values.reduce((a, b) => a + b, 0);
  document.getElementById('selection-total').textContent = compact(sum);
  const table = document.getElementById('trend-table');
  if (points.length === 0 || sum === 0) {
    el.replaceChildren(h('div', { class: 'empty-state' }, icon('layers'), h('h3', { text: '这个周期内没有记录' }), h('p', { text: '没有记录不等于用量为零。' })));
    table.replaceChildren();
    return;
  }
  const rawMax = Math.max(...values), { step, max } = niceScale(rawMax * 1.05), fmt = axisFormat(max);
  const W = Math.max(260, Math.round(el.clientWidth)), H = el.clientHeight || 205, left = 44, right = 16, top = 12, bottom = 27, cw = W - left - right, ch = H - top - bottom, n = values.length;
  const x = (i) => (n === 1 ? left + cw / 2 : left + (i / (n - 1)) * cw);
  const pts = values.map((v, i) => [x(i), top + ch - (v / max) * ch]);
  const line = 'M' + pts.map(([px, py]) => `${px.toFixed(2)},${py.toFixed(2)}`).join(' L');
  const area = `${line} L${pts[n - 1][0]},${top + ch} L${pts[0][0]},${top + ch} Z`;
  const tickCount = W < 420 ? 3 : 6;
  const ticks = [...new Set(Array.from({ length: tickCount + 1 }, (_, k) => Math.round((k / tickCount) * (n - 1))))];
  const peak = values.indexOf(rawMax);
  const NS = 'http://www.w3.org/2000/svg';
  const svg = document.createElementNS(NS, 'svg');
  svg.setAttribute('viewBox', `0 0 ${W} ${H}`);
  svg.setAttribute('role', 'img');
  svg.setAttribute('aria-label', `${RANGE_LABEL[state.range]} Token 趋势，筛选内 ${compact(sum)}，峰值在 ${pointLabel(grain, points[peak].bucket_start_ms)}，可在图下方展开数据表`);
  const add = (tag, attrs, text) => { const node = document.createElementNS(NS, tag); for (const [k, v] of Object.entries(attrs)) node.setAttribute(k, v); if (text !== undefined) node.textContent = text; svg.append(node); return node; };
  const defs = add('defs', {}); const grad = document.createElementNS(NS, 'linearGradient'); grad.setAttribute('id', 'chart-fill'); grad.setAttribute('x1', '0'); grad.setAttribute('y1', '0'); grad.setAttribute('x2', '0'); grad.setAttribute('y2', '1');
  for (const [offset, color, opacity] of [['0', '#8fb85c', '.42'], ['1', '#dcecc1', '.07']]) { const stop = document.createElementNS(NS, 'stop'); stop.setAttribute('offset', offset); stop.setAttribute('stop-color', color); stop.setAttribute('stop-opacity', opacity); grad.append(stop); }
  defs.append(grad);
  for (let i = 0; i <= 4; i++) { const y = top + (i / 4) * ch; add('line', { class: 'gridline', x1: left, x2: W - right, y1: y, y2: y }); add('text', { x: 0, y: y + 4 }, fmt(step * (4 - i))); }
  add('path', { d: area, fill: 'url(#chart-fill)' });
  add('path', { d: line, stroke: '#5f8f3b', 'stroke-width': 2.2, fill: 'none', 'stroke-linejoin': 'round' });
  ticks.forEach((t, j) => add('text', { x: x(t), y: H - 5, 'text-anchor': j === 0 ? 'start' : j === ticks.length - 1 ? 'end' : 'middle' }, pointLabel(grain, points[t].bucket_start_ms)));
  add('circle', { class: 'chart-dot', cx: pts[peak][0], cy: pts[peak][1], r: 3.5 });
  el.replaceChildren(svg);
  table.replaceChildren(h('table', { class: 'source-table' }, h('thead', null, h('tr', null, h('th', { text: '时间' }), h('th', { text: 'Token' }))),
    h('tbody', null, points.map((p, i) => h('tr', null, h('td', { text: pointLabel(grain, p.bucket_start_ms) }), h('td', { text: compact(values[i]) }))))));
}

/* ---------- other panels ---------- */
function compositionPanel() {
  const d = state.data;
  if (blockFailed('agents') || !d.agents) return h('section', { class: 'panel' }, heading('Agent 构成', RANGE_LABEL[state.range]), panelError('Agent 数据加载失败'));
  const rows = d.agents.map((a) => ({ id: a.agent, tokens: Number(a.tokens.value || 0) }));
  const total = rows.reduce((s, r) => s + r.tokens, 0);
  const ids = rows.map((r) => r.id);
  return h('section', { class: 'panel' },
    heading('Agent 构成', `${RANGE_LABEL[state.range]} · 本机`),
    h('div', { class: 'composition-total' }, compact(total), h('small', { text: 'Token 总量' })),
    h('div', { class: 'stackbar', role: 'img', 'aria-label': '各工具用量占比' }, rows.map((r) => h('i', { style: { width: `${total ? (r.tokens / total) * 100 : 0}%`, background: agentColor(r.id, ids) } }))),
    rows.length ? rows.map((r) => h('div', { class: 'composition-row' }, h('i', { style: { background: agentColor(r.id, ids) } }), h('span', { text: agentName(r.id) }), h('strong', { text: compact(r.tokens) }), h('small', { text: total ? `${((r.tokens / total) * 100).toFixed(0)}%` : '—' })))
      : h('p', { class: 'filter-note', text: '这个周期内没有记录。' }));
}
function skillsPanel(limit) {
  const d = state.data;
  if (blockFailed('skills') || !d.skills) return h('section', { class: 'panel skills-panel' }, heading('常用 Skill', RANGE_LABEL[state.range]), panelError('Skill 数据加载失败'));
  const w = d.skills.window;
  const note = w.aligned_with_range ? RANGE_LABEL[state.range] : `${mmdd(w.start_ms)} — ${mmdd(w.end_ms - 1)}（按自然日汇总）`;
  const items = d.skills.items.slice(0, limit);
  return h('section', { class: 'panel skills-panel' }, heading('常用 Skill', `${note} · 按调用次数`),
    items.length ? h('div', { class: 'skill-list' }, items.map((s, i) => h('div', { class: 'skill-item' },
      h('span', { class: 'skill-rank', text: pad(i + 1) }), h('span', { class: 'skill-icon' }, icon('sparkle')),
      h('div', null, h('b', { text: s.name || '自定义 Skill' }), h('small', { text: s.success_rate.value === null ? `活跃 ${s.active_days} 天 · 成功率未知` : `活跃 ${s.active_days} 天 · 成功率 ${(s.success_rate.value * 100).toFixed(0)}%` })),
      h('strong', { text: count(s.uses) }))))
      : h('p', { class: 'filter-note', text: '这个窗口内没有 Skill 调用记录。' }));
}
function heatmapPanel() {
  const d = state.data;
  if (blockFailed('calendar') || !d.calendar) return h('section', { class: 'panel' }, heading('活跃日历', '过去 12 个月'), panelError('日历数据加载失败'));
  const byDate = new Map(d.calendar.days.map((x) => [x.date, Number(x.tokens)]));
  const max = Math.max(1, ...byDate.values());
  const end = new Date(`${d.calendar.end}T00:00:00Z`);
  const weeks = window.innerWidth <= 740 ? 26 : 52;
  const endDow = (end.getUTCDay() + 6) % 7;
  const start = new Date(end.getTime() - ((weeks - 1) * 7 + endDow) * 86400e3);
  const level = (v) => (v === undefined ? 0 : v <= 0 ? 0 : Math.max(1, Math.ceil((v / max) * 4)));
  const cells = [], first = d.calendar.start;
  for (let i = 0; i < weeks * 7; i++) {
    const t = new Date(start.getTime() + i * 86400e3), iso = t.toISOString().slice(0, 10);
    cells.push({ iso, future: t > end, unknown: iso < first, tokens: byDate.get(iso) });
  }
  const months = [];
  let lastMonth = -1, lastCol = -9;
  for (let c = 0; c < weeks; c++) { const m = new Date(`${cells[c * 7].iso}T00:00:00Z`).getUTCMonth(); if (m !== lastMonth && c - lastCol >= 3) { months.push(h('span', { style: { 'grid-column': `${c + 1} / span 3` }, text: `${m + 1} 月` })); lastCol = c; } lastMonth = m; }
  const active = cells.filter((c) => !c.future && (c.tokens || 0) > 0).length;
  const focus = [...cells].reverse().find((c) => !c.future);
  const summary = h('span', { id: 'heat-selection' }, `过去一年 `, h('b', { text: String(active) }), ' 个活跃日');
  const grid = h('div', { class: 'heatmap', role: 'grid', 'aria-label': '每日用量热力图，方向键移动，行为周一至周日', style: { '--w': String(weeks) } },
    cells.map((c) => c.future ? h('span', { class: 'future', 'aria-hidden': 'true' })
      : h('button', { role: 'gridcell', 'data-level': String(c.unknown ? 0 : level(c.tokens)), tabindex: c === focus ? '0' : '-1', title: `${c.iso} · ${c.unknown ? '尚未记录' : c.tokens === undefined ? '没有记录' : compact(c.tokens) + ' Token'}`, 'aria-label': `${c.iso}，${c.unknown ? '尚未记录' : c.tokens === undefined ? '没有记录' : compact(c.tokens) + ' Token'}`, on: { click: () => { summary.textContent = `${c.iso} · ${c.unknown ? '尚未记录' : c.tokens === undefined ? '没有记录' : compact(c.tokens) + ' Token'}`; } } })));
  grid.addEventListener('keydown', (e) => {
    const cur = e.target.closest('button'); if (!cur) return;
    const all = [...grid.children], i = all.indexOf(cur), step = { ArrowUp: -1, ArrowDown: 1, ArrowLeft: -7, ArrowRight: 7 }[e.key];
    if (!step) return; e.preventDefault();
    const next = all[i + step]; if (!next || next.classList.contains('future')) return;
    cur.tabIndex = -1; next.tabIndex = 0; next.focus();
  });
  const legend = h('div', { class: 'heat-legend' }, '少', ['#e8ede1', '#c9deaa', '#a2c672', '#6f9c40', '#4f7a2f'].map((c) => h('i', { style: { background: c } })), '多');
  return h('section', { class: 'panel' }, heading('活跃日历', '过去 12 个月 · 不受统计周期影响'),
    h('div', { class: 'heat-wrap' }, h('div', { class: 'months', style: { 'grid-template-columns': `repeat(${weeks},minmax(0,1fr))` } }, months), grid, h('div', { class: 'heat-footer' }, summary, legend)));
}

function emptyPanel() {
  return h('section', { class: 'panel state-panel' }, h('span', { class: 'state-icon' }, icon('inbox')), h('h2', { text: '还没有用量数据' }),
    h('p', { text: '这里显示的是“没有记录”，不是“用量为零”。按下面的步骤开始采集：' }),
    h('ol', { class: 'steps' },
      h('li', null, h('code', { text: 'tokendance init' }), h('span', { text: '选择要采集的来源' })),
      h('li', null, h('code', { text: 'tokendance run' }), h('span', { text: '前台持续采集，或先用 tokendance collect --once 采集一次' })),
      h('li', null, h('span', { text: '正常使用 Codex、Claude Code 等工具，几分钟后刷新本页' }))));
}

/* ---------- pages ---------- */
function overview() {
  const d = state.data;
  if (d.state === 'no_data') return [emptyPanel()];
  const summary = blockFailed('summary') || !d.summary ? h('section', { class: 'panel' }, heading('核心指标', RANGE_LABEL[state.range]), panelError('指标加载失败')) : metrics(d.summary);
  return [summary, liveStrip(), h('div', { class: 'main-grid' }, trendPanel()), h('div', { class: 'overview-bottom' }, compositionPanel(), skillsPanel(4))];
}
function activity() {
  const d = state.data;
  if (d.state === 'no_data') return [emptyPanel()];
  return [h('div', { class: 'info-strip' }, icon('activity'), '日历展示过去 12 个月，不受统计周期影响；Skill 排行跟随所选统计周期。'), heatmapPanel(), skillsPanel(20)];
}
function status() {
  const d = state.data, c = d.collection, p = c.process, db = c.database;
  const running = p.state === 'running';
  const label = { running: `运行中${p.pid ? `（pid ${p.pid}）` : ''}`, stopped: '未运行', never_started: '尚未运行过', unknown: '未知' }[p.state];
  const panel = h('section', { class: 'collector-panel' },
    h('div', { class: 'collector-header' }, h('div', { class: 'collector-title' }, h('span', { class: 'device-icon' }, icon('terminal')), h('div', null, h('h2', { text: '本机采集服务' }), h('small', { text: '与网页独立运行 · 不受上方周期影响' }))),
      h('span', { class: `status-pill ${running ? '' : 'warn'}` }, h('span', { class: `dot ${running ? '' : 'amber'}` }), label)),
    db ? h('div', { class: 'agent-totals' },
      h('div', null, h('span', { text: '已存事件' }), h('b', { text: count(db.events) })),
      h('div', null, h('span', { text: '统计任务待处理' }), h('b', { text: count(db.metric_tasks_pending) })),
      h('div', null, h('span', { text: '待上传（未登录）' }), h('b', { text: count(db.upload_tasks_pending) })),
      h('div', null, h('span', { text: '数据库 schema' }), h('b', { text: String(db.schema_version ?? '—') }))) : h('p', { class: 'filter-note', text: '本机还没有数据库。运行 tokendance init 与 tokendance collect --once 开始采集。' }),
    running ? null : h('p', null, '启动采集：', h('code', { class: 'inline-code', text: 'tokendance run' })));
  const rows = (db && db.sources) || [];
  const table = h('section', { class: 'panel table-wrap' }, heading('采集来源', '每个来源可含多个读取流；未知不按零计入'),
    rows.length ? h('table', { class: 'source-table' }, h('thead', null, h('tr', null, ['来源', '读取流', '已启用', '出错', '最近更新'].map((t) => h('th', { text: t })))),
      h('tbody', null, rows.map((r) => h('tr', null, h('td', null, h('b', { text: agentName(r.harness) })), h('td', { text: String(r.streams) }), h('td', { text: `${r.enabled_streams}/${r.streams}` }), h('td', { text: String(r.with_errors) }), h('td', { text: r.last_updated_ms ? `${mmdd(r.last_updated_ms)} ${hhmm(r.last_updated_ms)}` : '—' })))))
      : h('p', { class: 'filter-note', text: '还没有发现任何来源。' }));
  const note = h('div', { class: 'info-strip' }, icon('shield'), '首版面板只读：启停来源用 tokendance sources，采集用 tokendance run，诊断用 tokendance doctor。');
  return [panel, table, note];
}

/* ---------- shell ---------- */
function chipState() {
  const d = state.data;
  if (!d) return ['idle', '加载中'];
  if (d.state === 'no_data') return ['idle', '尚无数据'];
  if (d.partial) return ['warn', '部分数据失败'];
  const s = d.collection.process.state;
  if (s === 'running') return ['ok', '采集正常'];
  if (s === 'stopped' || s === 'never_started') return ['warn', '采集未运行'];
  return ['idle', '运行状态未知'];
}
function banner() {
  const d = state.data, box = [];
  if (state.failure) box.push(h('div', { class: 'banner amber', role: 'status' }, icon('alert'), h('div', null, h('b', { text: '数据获取失败' }), h('span', { text: state.failure })), h('button', { class: 'button', text: '重试', on: { click: load } })));
  else if (d && d.partial) box.push(h('div', { class: 'banner amber', role: 'status' }, icon('alert'), h('div', null, h('b', { text: '部分数据加载失败' }), h('span', { text: `${Object.keys(d.errors).join('、')} 暂时不可用，其余内容不受影响。` })), h('button', { class: 'button', text: '全部重试', on: { click: load } })));
  if (d && d.state !== 'no_data') box.push(h('div', { class: 'banner', role: 'status' }, icon('cloud'), h('div', null, h('b', { text: '正在查看本机数据' }), h('span', { text: '账号汇总与排名需要登录，登录功能将在后续版本提供；本机模式可一直使用。' }))));
  return box;
}
function render() {
  if (!state.data) return;
  const d = state.data;
  document.getElementById('page-title').textContent = VIEWS[state.view];
  document.getElementById('breadcrumb').textContent = VIEWS[state.view];
  document.querySelectorAll('[data-view]').forEach((el) => { el.classList.toggle('active', el.dataset.view === state.view); if (el.dataset.view === state.view) el.setAttribute('aria-current', 'page'); else el.removeAttribute('aria-current'); });
  document.querySelectorAll('[data-range]').forEach((el) => el.setAttribute('aria-pressed', String(el.dataset.range === state.range)));
  document.querySelector('.controls').hidden = d.state === 'no_data' || state.view === 'status';
  document.getElementById('date-range').textContent = d.range === 'all' ? '全部记录' : `${mmdd(d.window.start_ms)} ${hhmm(d.window.start_ms)} — ${mmdd(d.window.end_ms - 1)} ${hhmm(d.window.end_ms - 1)}`;
  const [cls, text] = chipState(), chip = document.getElementById('status-chip');
  chip.className = `status-chip ${cls}`; chip.replaceChildren(h('span', { class: 'dot' }), text);
  document.getElementById('fetched').textContent = state.fetchedAt ? `数据更新于 ${new Date(state.fetchedAt).toLocaleTimeString('zh-CN', { hour12: false })}` : '';
  document.getElementById('nav-dot').hidden = !(d.collection.process.state !== 'running' && d.state !== 'no_data');
  document.getElementById('banner').replaceChildren(...banner().flat());
  const focus = document.activeElement && document.activeElement.id;
  document.getElementById('view').replaceChildren(...[{ overview, activity, status }[state.view]()].flat(2));
  drawChart();
  if (focus) { const el = document.getElementById(focus); if (el) el.focus({ preventScroll: true }); }
}
function navigate(view) {
  state.view = VIEWS[view] ? view : 'overview';
  if (state.data) render();
  window.scrollTo({ top: 0, behavior: 'instant' });
}

document.querySelectorAll('[data-icon]').forEach((el) => { const holder = h('span', { class: el.className }); holder.append(icon(el.dataset.icon)); el.replaceWith(holder); });
document.querySelectorAll('[data-range]').forEach((el) => el.addEventListener('click', () => { state.range = el.dataset.range; document.querySelectorAll('[data-range]').forEach((b) => b.setAttribute('aria-pressed', String(b === el))); load(); }));
document.getElementById('status-chip').addEventListener('click', () => { location.hash = 'status'; });
document.getElementById('refresh').addEventListener('click', load);
window.addEventListener('hashchange', () => navigate(location.hash.slice(1)));
let resizeTimer;
window.addEventListener('resize', () => { clearTimeout(resizeTimer); resizeTimer = setTimeout(() => { if (state.data) render(); }, 120); });
setInterval(() => { if (!document.hidden && document.getElementById('disconnect').hidden) load(); }, REFRESH_MS);

(async () => {
  navigate(location.hash.startsWith('#launch=') ? 'overview' : location.hash.slice(1));
  if (await redeemLaunchToken()) load();
})();
