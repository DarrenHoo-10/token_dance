'use strict';
const paths={
  grid:'<rect x="3" y="3" width="7" height="7" rx="1.5"/><rect x="14" y="3" width="7" height="7" rx="1.5"/><rect x="3" y="14" width="7" height="7" rx="1.5"/><rect x="14" y="14" width="7" height="7" rx="1.5"/>',
  layers:'<path d="m12 3 10 5-10 5L2 8l10-5Z"/><path d="m3 12 9 5 9-5M3 16l9 5 9-5"/>',
  activity:'<path d="M3 12h4l3-8 4 16 3-8h4"/>',
  monitor:'<rect x="3" y="4" width="18" height="13" rx="2"/><path d="M8 21h8m-4-4v4"/>',
  terminal:'<rect x="3" y="4" width="18" height="16" rx="2"/><path d="m7 9 3 3-3 3m6 0h4"/>',
  shield:'<path d="M12 3 4 6v6c0 5 8 9 8 9s8-4 8-9V6l-8-3Z"/><path d="m8 12 3 3 5-6"/>',
  help:'<circle cx="12" cy="12" r="9"/><path d="M9 9a3 3 0 0 1 6 0c0 2-3 2-3 5m0 3h.01"/>',
  'arrow-up-right':'<path d="M6 18 18 6M6 6h12v12"/>',
  'arrow-right':'<path d="M4 12h16m-6-6 6 6-6 6"/>',
  refresh:'<path d="M20 10a8 8 0 0 0-14-5L3 8m0-5v5h5M4 14a8 8 0 0 0 14 5l3-3m0 5v-5h-5"/>',
  cloud:'<path d="M6 18a5 5 0 1 1 1-10 6 6 0 0 1 11-1 5.5 5.5 0 0 1 0 11H6Z"/>',
  clock:'<circle cx="12" cy="12" r="9"/><path d="M12 7v5l3 2"/>',
  bolt:'<path d="m13 2-9 12h7l-1 8 10-12h-8l1-8Z"/>',
  coins:'<ellipse cx="12" cy="6" rx="8" ry="3"/><path d="M4 6v6c0 4 16 4 16 0V6M4 12v6c0 4 16 4 16 0v-6"/>',
  code:'<path d="m8 6-6 6 6 6m8-12 6 6-6 6m-3-14-2 16"/>',
  message:'<path d="M4 4h16v12H9l-5 4V4Z"/><path d="M8 8h8m-8 4h5"/>',
  alert:'<path d="m12 3 10 18H2L12 3Z"/><path d="M12 9v5m0 3h.01"/>',
  chevron:'<path d="m9 5 7 7-7 7"/>',
  sparkle:'<path d="m12 3 3 6 6 3-6 3-3 6-3-6-6-3 6-3 3-6Z"/>',
  check:'<path d="m5 12 4 4L19 6"/>',
  x:'<path d="m6 6 12 12M18 6 6 18"/>',
  server:'<rect x="3" y="3" width="18" height="7" rx="2"/><rect x="3" y="14" width="18" height="7" rx="2"/><path d="M7 6.5h.01M7 17.5h.01m4-11h6m-6 11h6"/>',
  copy:'<rect x="8" y="8" width="12" height="13" rx="2"/><path d="M16 8V3H3v13h5"/>',
  key:'<circle cx="8" cy="15" r="4"/><path d="m11 12 9-9m-3 3 3 3"/>',
  wifi:'<path d="M2 9a15 15 0 0 1 20 0M5 13a10 10 0 0 1 14 0M8.5 16.5a5 5 0 0 1 7 0M12 20h.01"/>',
  inbox:'<path d="M3 13h5l1 3h6l1-3h5"/><path d="M5 5h14l2 8v6H3v-6l2-8Z"/>'
};
const icon=name=>`<svg class="icon" aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">${paths[name]||paths.grid}</svg>`;
document.querySelectorAll('[data-icon]').forEach(el=>el.replaceWith(Object.assign(document.createElement('span'),{className:el.className,innerHTML:icon(el.dataset.icon)})));

/* ---------- 示例数据 ---------- */
const state={view:'overview',scope:'account',range:'24h',agent:'all',model:'all',scenario:'normal',failed:{trend:true,skills:true},heatSel:null};
const ranges={
  today:{label:'今日',factor:.72,date:'09.29 00:00 — 14:00',cache:'79.2%',line:'2.49K'},
  '24h':{label:'过去 24 小时',factor:1,date:'09.28 14:00 — 09.29 14:00',cache:'78.6%',line:'2.53K'},
  '7d':{label:'近 7 天',factor:4.87,date:'09.23 — 09.29',cache:'77.9%',line:'2.61K'},
  '30d':{label:'近 30 天',factor:17.66,date:'08.31 — 09.29',cache:'80.1%',line:'2.58K'},
  all:{label:'全部时间',factor:88.4,date:'2025.10 — 2026.09',cache:'81.4%',line:'2.72K'}
};
/* 颜色均与白底保持 3:1 以上，且明度、色相都有区分 */
const agents=[
  {id:'codex',name:'Codex',mark:'C',color:'#4c7a34',plan:'Pro',tokens:14.72,local:9.20,cost:13.12,windows:[['5 小时',32,'2 小时 18 分后重置'],['每周',56,'3 天后重置']],models:[['gpt-5',.76],['gpt-5-codex',.24]]},
  {id:'claude',name:'Claude Code',mark:'✳',color:'#b0692a',plan:'Max',tokens:6.86,local:5.18,cost:7.28,windows:[['5 小时',24,'3 小时 42 分后重置'],['每周',82,'1 天 6 小时后重置']],models:[['claude-sonnet-4',.62],['claude-opus-4',.38]]},
  {id:'cursor',name:'Cursor',mark:'↗',color:'#3f7f8e',plan:'Pro',tokens:2.04,local:1.20,cost:2.64,windows:[],models:[['claude-sonnet-4',.68],['gpt-5',.32]]},
  {id:'grok',name:'Grok Build',mark:'𝕏',color:'#7a62a8',plan:'SuperGrok',tokens:1.24,local:.60,cost:1.69,windows:[['每周',96,'4 天后重置']],models:[['grok-code-fast-1',1]]}
];
const skills=[['frontend-design',38,'界面设计'],['code-review',26,'代码审查'],['test-runner',19,'测试验证'],['git-essentials',12,'版本管理']];
const meta={overview:'用量总览',agents:'用量与额度',activity:'活动与技能',devices:'设备与同步'};
const PENDING=12;

/* ---------- 状态推导 ---------- */
const sc=()=>state.scenario;
const partialActive=()=>sc()==='partial'&&(state.failed.trend||state.failed.skills);
const accountBlocked=()=>state.scope==='account'&&(sc()==='expired'||sc()==='loggedout');
const stale=()=>sc()==='offline'&&state.scope==='account';
const total=()=>agents.reduce((s,a)=>s+(state.scope==='local'?a.local:a.tokens),0)*ranges[state.range].factor;
const amount=a=>(state.scope==='local'?a.local:a.tokens)*ranges[state.range].factor;
const scale=()=>ranges[state.range].factor*(state.scope==='local'?16.18/24.86:1);
const compact=(n,precision=2)=>n>=1000?`${(n/1000).toFixed(precision)}B`:`${n.toFixed(precision)}M`;
const count=n=>Math.round(n).toLocaleString('en-US');
const num=n=>n.toLocaleString('en-US',{minimumFractionDigits:2,maximumFractionDigits:2});
const pad=n=>String(n).padStart(2,'0');
const narrow=()=>window.innerWidth<=740;

/* ---------- 通用片段 ---------- */
const button=(text,attr='',symbol='arrow-right')=>`<button class="text-button" ${attr}>${text}${icon(symbol)}</button>`;
const mark=a=>`<span class="agent-mark ${a.id}">${a.mark}</span>`;
const heading=(title,subtitle='',right='')=>`<div class="panel-heading"><div><h2>${title}</h2>${subtitle?`<p>${subtitle}</p>`:''}</div>${right}</div>`;
const staleTag=()=>stale()?' · 快照 14:20':'';
function metric(label,value,unit,delta,symbol,primary=false){
  let change='<span>按已知用量与价格计算</span>';
  if(delta!==null&&state.range!=='all'){
    const cls=delta>0?'up':delta<0?'down':'flat',arrow=delta>0?'↗':delta<0?'↘':'→',text=delta===0?'持平':`${delta>0?'+':''}${delta.toFixed(1)}%`;
    change=`<strong class="${cls}">${arrow} ${text}</strong><span>${state.range==='24h'?'较前 24 小时':'较上一周期'}</span>`;
  }
  return `<div class="metric ${primary?'primary':''}"><div class="metric-label">${label}${icon(symbol)}</div><div class="metric-number">${value}${unit?`<small>${unit}</small>`:''}</div><div class="metric-change">${change}</div></div>`;
}
function metrics(){
  const s=scale(),n=total(),r=ranges[state.range];
  return `<section class="metrics-grid" aria-label="核心用量指标">${metric('总 Token',n>=1000?(n/1000).toFixed(2):n.toFixed(2),n>=1000?'B':'M',18.6,'bolt',true)}${metric('预估费用',`$${num(24.73*s)}`,'',-3.1,'coins')}${metric('生成代码行',count(9842*s),'',12.4,'code')}${metric('总消息数',count(428*s),'',0,'message')}${metric('会话总时长',(6.4*s).toFixed(1),'h',-6.8,'clock')}</section>
  <div class="mini-metrics">${[['输入上下文',compact(n*.75)],['输出 Token',compact(n*.25)],['缓存命中率',r.cache],['单行 Token',r.line],['用户消息数',count(126*s)]].map(([label,value])=>`<div class="mini-metric"><span>${label}</span><b>${value}</b></div>`).join('')}</div>`;
}
function strip(){
  const stopped=sc()==='stopped';
  return `<div class="live-strip ${stopped?'stopped':''}"><div class="live-main"><span class="dot ${stopped?'amber':''}"></span><b>${stopped?'本机采集未运行':'本机正在采集'}</b><span class="live-separator"></span><span class="live-queue">待同步 ${PENDING} 条</span><span class="live-extra">· ${stopped?'上次采集 12:04:31':'上次同步 14:32:08'}</span><span class="live-separator"></span>${stopped?`<code class="inline-code">tokendance run</code>`:`<button class="warning" data-action="quota-help">${icon('alert')}Cursor 额度待更新</button>`}</div>${button('运行详情','data-action="goto-devices"')}</div>`;
}
function windowUsed(used){return used>=95?'critical':used>=80?'caution':'';}
function quotaWindows(a){
  if(!a.windows.length)return `<div class="quota-missing"><span>本机额度授权已过期</span>${button('查看原因','data-action="quota-help"')}</div>`;
  return `<div class="quota-windows">${a.windows.map(([label,used,reset])=>{const lv=windowUsed(used);return `<div class="quota-window ${lv}"><div><span>${label}窗口</span><b>${used}<small>% 已用</small></b></div><div class="bar" style="--percent:${used}%"><i></i></div><small>${lv==='critical'?'即将用尽 · ':''}${reset}</small></div>`;}).join('')}</div>`;
}
function quotas(){
  return `<section class="panel quota-summary">${heading('订阅额度','本机来源 · 最新额度快照',`<span class="panel-subtag">不受统计周期影响</span>`)}${agents.slice(0,3).map(a=>`<div class="quota-row"><div class="quota-top">${mark(a)}<div class="agent-name"><b>${a.name}</b></div><span class="plan">${a.plan}</span></div>${quotaWindows(a)}</div>`).join('')}<div class="panel-more">${button('查看全部工具','data-action="goto-agents"')}</div></section>`;
}

/* ---------- 趋势图 ---------- */
const baseWeights={
  '24h':[.7,.4,.6,.3,.2,.28,.18,.6,1.2,1.6,1.1,2.2,1.5,1.8,1.3,2.8,4.6,3.4,2.3,2.8,3.7,2.7,4.7,3.8,3.2],
  today:[.3,.2,.15,.1,.1,.2,.6,1.2,1.8,2.4,2.9,3.4,3.9,4.1,3.7],
  '7d':[1.5,2.4,1.7,3.2,2.7,3.9,3.3],
  all:[.8,1.2,1.1,1.7,1.9,1.3,2.2,2.9,2.7,3.1,3.8,4.2]
};
const raw24=baseWeights['24h'];
baseWeights['30d']=Array.from({length:30},(_,i)=>raw24[(i*5+3)%24]);
const axes={
  '24h':{label:i=>`${pad((14+i)%24)}:00`,ticks:[0,4,8,12,16,20,24]},
  today:{label:i=>`${pad(i)}:00`,ticks:[0,2,4,6,8,10,12,14]},
  '7d':{label:i=>`09.${23+i}`,ticks:[0,1,2,3,4,5,6]},
  '30d':{label:i=>i===0?'08.31':`09.${pad(i)}`,ticks:[0,5,10,15,20,25,29]},
  all:{label:i=>`${[10,11,12,1,2,3,4,5,6,7,8,9][i]} 月`,ticks:[0,2,4,6,8,10,11]}
};
function niceScale(maxV){
  const rough=maxV/4,exp=Math.pow(10,Math.floor(Math.log10(rough))),f=rough/exp;
  const step=(f<=1?1:f<=2?2:f<=2.5?2.5:f<=5?5:10)*exp;
  return {step,max:step*4};
}
function axisFormat(max){
  if(max>=1000)return v=>`${+(v/1000).toFixed(1)}B`;
  if(max<1)return v=>`${Math.round(v*1000)}K`;
  return v=>`${+v.toFixed(1)}M`;
}
function trendValues(){
  const a=axes[state.range],n=baseWeights[state.range].length;
  const selected=agents.map((ag,k)=>({ag,k})).filter(({ag})=>state.agent==='all'||ag.id===state.agent);
  const values=Array(n).fill(0);let sum=0;
  for(const {ag,k} of selected){
    const share=state.model==='all'?1:ag.models.filter(([name])=>state.model==='gpt'?name.startsWith('gpt'):name.startsWith('claude')).reduce((s,m)=>s+m[1],0);
    const amt=amount(ag)*share;if(!amt)continue;
    const w=baseWeights[state.range].map((v,i)=>v*(1+.35*Math.sin((i+1)*(k+1)*.9)));
    const wt=w.reduce((x,y)=>x+y,0);
    w.forEach((v,i)=>{values[i]+=v/wt*amt;});sum+=amt;
  }
  return {values,sum,axis:a};
}
function trend(){
  if(sc()==='partial'&&state.failed.trend)return `<section class="panel">${heading('Token 用量趋势',`${ranges[state.range].label}的使用节奏`)}<div class="panel-error">${icon('alert')}<div><b>趋势数据加载失败</b><p>接口暂时不可用。其余面板不受影响。</p></div><button class="button" data-action="retry-trend">重试</button></div></section>`;
  return `<section class="panel">${heading('Token 用量趋势',`${ranges[state.range].label}的使用节奏`,`<span class="panel-subtag">${state.scope==='account'?'账号已同步用量':'本机已记录用量'}${staleTag()}</span>`)}<div class="filters"><select id="agent-filter" aria-label="趋势 Agent 筛选"><option value="all">全部 Agent</option>${agents.map(a=>`<option value="${a.id}" ${state.agent===a.id?'selected':''}>${a.name}</option>`).join('')}</select><select id="model-filter" aria-label="趋势模型筛选"><option value="all">全部模型</option><option value="gpt" ${state.model==='gpt'?'selected':''}>GPT 系列</option><option value="claude" ${state.model==='claude'?'selected':''}>Claude 系列</option></select><span class="filter-note">仅影响趋势图</span></div><div class="chart" id="trend-chart"></div><div class="chart-footer"><span>筛选内 Token <b id="selection-total"></b></span><div class="legend"><span><i></i>用量</span><span>${['24h','today'].includes(state.range)?'按小时':state.range==='all'?'按月':'按天'}</span></div></div><details class="data-table"><summary>以表格查看数据</summary><div class="table-scroll" id="trend-table"></div></details></section>`;
}
function renderChart(){
  const el=document.getElementById('trend-chart');if(!el)return;
  const {values,sum,axis}=trendValues();
  document.getElementById('selection-total').textContent=compact(sum);
  const table=document.getElementById('trend-table');
  if(sum===0){el.innerHTML=`<div class="empty-state">${icon('layers')}<h3>没有符合筛选的记录</h3><p>试试其他 Agent 或模型组合</p></div>`;table.innerHTML='';return;}
  const rawMax=Math.max(...values),{step,max}=niceScale(rawMax*1.05),fmt=axisFormat(max);
  const W=Math.max(260,Math.round(el.clientWidth)),H=el.clientHeight||205,left=40,right=16,top=12,bottom=27,cw=W-left-right,ch=H-top-bottom,n=values.length;
  const pts=values.map((v,i)=>[left+i/(n-1)*cw,top+ch-v/max*ch]);
  const d='M'+pts.map(([x,y])=>`${x.toFixed(2)},${y.toFixed(2)}`).join(' L');
  const area=d+` L${pts.at(-1)[0]},${top+ch} L${left},${top+ch} Z`;
  let ticks=axis.ticks;
  if(W<420)ticks=[...new Set([ticks[0],ticks[Math.floor(ticks.length/3)],ticks[Math.floor(2*ticks.length/3)],ticks.at(-1)])];
  const peak=values.indexOf(rawMax),hit=cw/(n-1);
  el.innerHTML=`<svg viewBox="0 0 ${W} ${H}" role="img" aria-label="${ranges[state.range].label} Token 趋势，筛选内 ${compact(sum)} Token，峰值在 ${axis.label(peak)}，可在图下方展开数据表"><defs><linearGradient id="chart-fill" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="#8fb85c" stop-opacity=".42"/><stop offset="1" stop-color="#dcecc1" stop-opacity=".07"/></linearGradient></defs>${[0,1,2,3,4].map(i=>{const y=top+i/4*ch;return `<line class="gridline" x1="${left}" x2="${W-right}" y1="${y}" y2="${y}"/><text x="0" y="${y+4}">${fmt(step*(4-i))}</text>`;}).join('')}<path d="${area}" fill="url(#chart-fill)"/><path d="${d}" stroke="#5f8f3b" stroke-width="2.2" fill="none" stroke-linejoin="round"/>${ticks.map((t,j)=>`<text x="${left+t/(n-1)*cw}" y="${H-5}" text-anchor="${j===0?'start':j===ticks.length-1?'end':'middle'}">${axis.label(t)}</text>`).join('')}<circle class="chart-dot" cx="${pts[peak][0]}" cy="${pts[peak][1]}" r="4"/>${pts.map(([x],i)=>`<rect class="chart-hit" x="${x-hit/2}" y="${top}" width="${hit}" height="${ch}" fill="transparent" data-point="${i}"/>`).join('')}</svg><div class="chart-tooltip"></div>`;
  const tip=el.querySelector('.chart-tooltip');
  el.querySelectorAll('[data-point]').forEach(h=>{
    h.addEventListener('mousemove',e=>{const i=Number(h.dataset.point);tip.textContent=`${axis.label(i)} · ${compact(values[i])} Token`;tip.style.display='block';const rect=el.getBoundingClientRect();tip.style.left=Math.min(Math.max(e.clientX-rect.left-60,0),rect.width-150)+'px';tip.style.top=Math.max(0,e.clientY-rect.top-45)+'px';});
    h.addEventListener('mouseleave',()=>tip.style.display='none');
  });
  table.innerHTML=`<table class="source-table"><thead><tr><th>时间</th><th>Token</th></tr></thead><tbody>${values.map((v,i)=>`<tr><td>${axis.label(i)}</td><td>${compact(v)}</td></tr>`).join('')}</tbody></table>`;
}

function composition(){
  const n=total();
  return `<section class="panel">${heading('Agent 构成',`${ranges[state.range].label} · ${state.scope==='account'?'全部设备':'本机'}${staleTag()}`,button('查看明细','data-action="goto-agents"'))}<div class="composition-total">${compact(n)}<small>Token 总量</small></div><div class="stackbar" role="img" aria-label="各工具用量占比">${agents.map(a=>`<i style="width:${amount(a)/n*100}%;background:${a.color}"></i>`).join('')}</div>${agents.map(a=>`<div class="composition-row"><i style="background:${a.color}"></i><span>${a.name}</span><strong>${compact(amount(a))}</strong><small>${(amount(a)/n*100).toFixed(0)}%</small></div>`).join('')}</section>`;
}

/* ---------- 活跃日历 ---------- */
const DAY=864e5,END=Date.UTC(2026,8,29),RECORD_START=Date.UTC(2025,9,9),WEEKDAYS=['周一','周二','周三','周四','周五','周六','周日'];
const iso=t=>new Date(t).toISOString().slice(0,10);
function heatCells(weeks){
  const endDow=(new Date(END).getUTCDay()+6)%7,start=END-((weeks-1)*7+endDow)*DAY,seed=state.scope==='local'?5:0,cells=[];
  for(let i=0;i<weeks*7;i++){
    const t=start+i*DAY;let level;
    if(t>END)level='future';
    else if(t<RECORD_START)level='unknown';
    else{const idx=Math.round((t-RECORD_START)/DAY),k=(idx*31+Math.floor(idx/7)*13+seed)%19;level=k<3?0:k<8?1:k<12?2:k<16?3:4;if(level===0&&t>END-14*DAY)level=1;}
    cells.push({t,level,date:iso(t),dow:i%7});
  }
  return cells;
}
function heatStats(){
  const cells=heatCells(52).filter(c=>c.level!=='future'&&c.level!=='unknown');
  let active=0,best=0,run=0,cur=0;const byDow=Array(7).fill(0),dowN=Array(7).fill(0);
  for(const c of cells){if(c.level>=1){active++;run++;best=Math.max(best,run);}else run=0;byDow[c.dow]+=c.level;dowN[c.dow]++;}
  for(let i=cells.length-1;i>=0&&cells[i].level>=1;i--)cur++;
  const avg=byDow.map((v,i)=>v/dowN[i]);
  return {active,best,cur,top:WEEKDAYS[avg.indexOf(Math.max(...avg))],days:cells.length};
}
const heatTokens=c=>c.level==='unknown'?null:Number(c.level)*186420;
function heatMonths(cells,weeks){
  const out=[];let last=-9,lastMonth=-1;
  for(let c=0;c<weeks;c++){const t=cells[c*7].t,m=new Date(t).getUTCMonth();if(m!==lastMonth&&c-last>=3){out.push(`<span style="grid-column:${c+1} / span 3">${m+1} 月</span>`);last=c;}lastMonth=m;}
  return out.join('');
}
function heatmap(mode='full'){
  const compactMode=mode==='compact',weeks=compactMode?12:(narrow()?26:52),cells=heatCells(weeks),stats=heatStats();
  const sel=state.heatSel&&cells.find(c=>c.date===state.heatSel),focusable=sel||cells.filter(c=>c.level!=='future').at(-1);
  const summary=sel?`${sel.date} · ${sel.level==='unknown'?'尚未记录':count(heatTokens(sel))+' Token'}`:`${compactMode?'近 12 周':'过去一年'} <b>${cells.filter(c=>c.level>=1&&c.level!=='future'&&c.level!=='unknown').length}</b> 个活跃日`;
  const facts=`<div class="activity-facts"><div><span>连续活跃</span><b>${stats.cur}<small>天</small></b></div><div><span>最长连续</span><b>${stats.best}<small>天</small></b></div><div><span>最活跃的日子</span><b>${stats.top}<small>平均用量最高</small></b></div></div>`;
  const grid=`<div class="heat-wrap"><div class="months" style="grid-template-columns:repeat(${weeks},minmax(0,1fr))">${heatMonths(cells,weeks)}</div><div class="heatmap ${compactMode?'compact':''}" role="grid" aria-label="每日用量热力图，方向键移动，行为周一至周日" style="--w:${weeks}">${cells.map(c=>c.level==='future'?'<span class="future" aria-hidden="true"></span>':`<button role="gridcell" data-level="${c.level}" data-date="${c.date}" tabindex="${c===focusable?0:-1}" class="${sel===c?'selected':''}" title="${c.date} · ${c.level==='unknown'?'未记录':count(heatTokens(c))+' Token'}" aria-label="${c.date}，${c.level==='unknown'?'未记录':count(heatTokens(c))+' Token，等级 '+c.level}"></button>`).join('')}</div><div class="heat-footer"><span id="heat-selection">${summary}</span><div class="heat-legend">少${['#e8ede1','#c9deaa','#a2c672','#6f9c40','#4f7a2f'].map(c=>`<i style="background:${c}"></i>`).join('')}多</div></div></div>`;
  const subtitle=compactMode?'近 12 周 · 不受统计周期影响':'过去 12 个月 · 不受统计周期影响';
  return `<section class="panel">${heading('活跃日历',subtitle,`<span class="panel-subtag">连续活跃 ${stats.cur} 天</span>`+(compactMode?button('全年视图','data-action="goto-activity"'):''))}${compactMode?`<div class="heat-compact">${grid}${facts}</div>`:`${grid}${facts}`}</section>`;
}
function rankCard(){
  let body;
  if(state.scope==='local'||sc()==='loggedout')body=`<div class="rank-empty">账号排名仅在“全部设备”视图中提供。${sc()==='loggedout'?'登录后可见。':''}</div>`;
  else if(sc()==='expired')body=`<div class="rank-empty">登录已过期，排名暂不可用。</div>`;
  else body=`<div class="rank-number">#58<small>${stale()?'快照 14:20':'个人排名'}</small></div><p>过去 24 小时 · 不随上方周期变化。未上榜时不显示名次。</p>`;
  return `<section class="panel rank-panel">${heading('排名','账号 · 独立窗口')}${body}</section>`;
}
function skillSummary(){
  if(sc()==='partial'&&state.failed.skills)return `<section class="panel skills-panel">${heading('常用 Skill',`${ranges[state.range].label}`)}<div class="panel-error">${icon('alert')}<div><b>Skill 数据加载失败</b><p>其余面板不受影响。</p></div><button class="button" data-action="retry-skills">重试</button></div></section>`;
  return `<section class="panel skills-panel">${heading('常用 Skill',`${ranges[state.range].label} · 调用记录（示例数据）`,button('全部技能','data-action="goto-activity"'))}<div class="skill-list">${skills.map(([name,value,desc],i)=>`<div class="skill-item"><span class="skill-rank">0${i+1}</span><span class="skill-icon">${icon('sparkle')}</span><div><b>${name}</b><small>${desc}</small></div><strong>${Math.round(value*scale())}</strong></div>`).join('')}</div></section>`;
}

/* ---------- 状态面板 ---------- */
function blockedPanel(){
  const lo=sc()==='loggedout';
  return `<section class="panel state-panel"><span class="state-icon">${icon('key')}</span><h2>${lo?'登录后查看全部设备':'登录已过期，账号数据暂不可用'}</h2><p>${lo?'未登录时可直接查看本机数据；登录后同步并汇总账号下所有设备。':'本机采集和本机数据不受影响。重新登录后账号统计会恢复。'}</p><code>tokendance login</code>${lo?'':'<div><button class="button" data-action="to-local">先看本机数据</button></div>'}</section>`;
}
function emptyPanel(){
  return `<section class="panel state-panel"><span class="state-icon">${icon('inbox')}</span><h2>还没有用量数据</h2><p>这里显示的是“没有记录”，不是“用量为零”。按下面的步骤开始采集：</p><ol class="steps"><li><code>tokendance init</code><span>选择要采集的来源</span></li><li><code>tokendance run</code><span>或 <code>tokendance service install</code> 让采集在后台运行</span></li><li><span>正常使用 Codex、Claude Code 等工具，几分钟后刷新本页</span></li></ol></section>`;
}
function bodyGuard(render){
  if(sc()==='nodata')return emptyPanel();
  if(accountBlocked())return blockedPanel();
  return render();
}

/* ---------- 各页面 ---------- */
function overview(){
  return bodyGuard(()=>`${metrics()}${strip()}<div class="main-grid">${trend()}${quotas()}</div><div class="overview-bottom">${composition()}${heatmap('compact')}</div><div class="bottom-row">${skillSummary()}${rankCard()}</div>`);
}
function agentCard(a){
  const v=amount(a),cursor=a.id==='cursor',stopped=sc()==='stopped';
  return `<article class="panel agent-card"><div class="agent-card-header">${mark(a)}<div><h2>${a.name}</h2><small>${a.plan} · 本机已连接</small></div><span class="status-pill ${cursor||stopped?'warn':''}"><span class="dot ${cursor||stopped?'amber':''}"></span>${cursor?'额度待授权':stopped?'采集未运行':'采集正常'}</span></div><div class="agent-totals"><div><span>${ranges[state.range].label}用量 · ${state.scope==='account'?'全部设备':'本机'}${staleTag()}</span><b>${compact(v)}<small>tokens</small></b></div><div><span>预估费用</span><b>$${num(a.cost*ranges[state.range].factor*(state.scope==='local'?a.local/a.tokens:1))}</b></div></div><div class="quota-label">本机订阅额度 · 不随统计周期变化</div>${quotaWindows(a)}${cursor?'<div class="empty-quota">用量采集正常，额度需要重新连接本机 Cursor 账号。<br>缺少额度信息不会显示为 0%。</div>':''}<p class="agent-observed">${cursor?'上次成功获取：12 分钟前 · 当前额度未知':'观测于 14:31:42 · 额度来自当前工具账号'}</p><details class="model-details" open><summary><span>模型用量</span><span class="model-count">${a.models.length} 个模型</span></summary>${a.models.map(([name,share])=>`<div class="model-row"><span>${name}</span><b>${compact(v*share)}</b><small>${(share*100).toFixed(0)}%</small></div>`).join('')}</details></article>`;
}
function agentsPage(){
  return bodyGuard(()=>`${strip()}<div class="info-strip">${icon('shield')}用量跟随页面上方的范围与周期；订阅额度始终来自这台设备上的工具账号。</div><div class="agent-cards">${agents.map(agentCard).join('')}</div><div class="subheading"><div><h2>其他来源</h2><p>来源能力不同，未知指标不按零计入。</p></div><span class="badge-muted">3 个可识别来源</span></div><section class="panel table-wrap"><table class="source-table"><thead><tr><th>来源</th><th>采集状态</th><th>用量</th><th>订阅额度</th><th></th></tr></thead><tbody><tr><td><b>Pi</b></td><td>已发现 · 暂无记录</td><td>—</td><td>来源不提供</td><td>${button('详情','data-source="pi"')}</td></tr><tr><td><b>OpenCode</b></td><td>未检测到本地数据</td><td>—</td><td>来源不提供</td><td>${button('详情','data-source="opencode"')}</td></tr><tr><td><b>DeepSeek Harness</b></td><td>尚未连接</td><td>—</td><td>未连接</td><td>${button('详情','data-source="deepseek"')}</td></tr></tbody></table></section>`);
}
function activityPage(){
  return bodyGuard(()=>{
    const bars=[43,74,61,89,56,97,78];
    return `<div class="info-strip">${icon('activity')}日历展示过去 12 个月，不受统计周期影响；Skill 排行跟随所选统计周期；活动时长固定为最近 7 个自然日。</div>${heatmap('full')}<div class="activity-layout">${sc()==='partial'&&state.failed.skills?`<section class="panel">${heading('Skill 使用排行',ranges[state.range].label)}<div class="panel-error">${icon('alert')}<div><b>Skill 数据加载失败</b><p>其余面板不受影响。</p></div><button class="button" data-action="retry-skills">重试</button></div></section>`:`<section class="panel">${heading('Skill 使用排行',`${ranges[state.range].label} · 按调用次数排序`,`<span class="panel-subtag">${Math.round(95*scale())} 次调用</span>`)}<div class="table-wrap"><table class="source-table skill-table"><thead><tr><th>Skill</th><th>调用</th><th>已知成功</th><th>来源</th></tr></thead><tbody>${skills.map(([name,n],i)=>`<tr><td><b>${name}</b></td><td>${Math.round(n*scale())}</td><td>${i===0?'—':i===1?'96%':'100%'}</td><td>${i%2?'Claude Code':'Codex'}</td></tr>`).join('')}</tbody></table></div><p class="footnote">— 表示没有可靠结果，不推算成功率。</p></section>`}<section class="panel">${heading('近期活动时长','最近 7 个自然日 · 会话累计时长')}<div class="activity-chart" role="img" aria-label="最近 7 天活动时长：${bars.map((h,i)=>`09.${23+i} ${(h/14).toFixed(1)} 小时`).join('，')}">${bars.map((h,i)=>`<div class="activity-bar" style="--h:${h}%" data-label="09.${23+i}" data-value="${(h/14).toFixed(1)}h" title="09.${23+i} · ${(h/14).toFixed(1)} 小时"></div>`).join('')}</div><p class="footnote bars-note">多会话时长可能重叠，不等同于人的工作时长。</p></section></div>`;
  });
}
function deviceCards(){
  if(accountBlocked())return blockedPanel();
  const values=[16.18,6.32,2.36],names=['MacBook Pro','Ubuntu Dev Server','Windows Workstation'],when=['刚刚','2 分钟前','8 分钟前'];
  const nodata=sc()==='nodata',stopped=sc()==='stopped',offline=sc()==='offline';
  return `<div class="device-grid">${names.filter((_,i)=>(state.scope==='account'&&sc()!=='nodata')||i===0).map((name,i)=>`<article class="panel device-card"><div class="device-head"><span class="device-icon">${icon(i===1?'server':'monitor')}</span><div><h3>${name}</h3><small>${['macOS · Apple Silicon','Ubuntu · x86_64','Windows · x86_64'][i]}</small></div>${i===0?'<span class="tag">本机</span>':''}</div><div class="device-token">${nodata?'—':compact(values[i]*ranges[state.range].factor)}<small>${nodata?'暂无记录':ranges[state.range].label}</small></div><div class="device-foot"><span><span class="dot ${(i===0&&(stopped||nodata))||(offline&&i>0)?'amber':''}"></span> ${nodata?'尚未开始采集':i===0&&stopped?'采集未运行':offline&&i>0?'快照 · 无法确认最新状态':'同步正常'}</span><span>${nodata?'':i===0&&stopped?'12:04 采集':offline&&i>0?'14:20':when[i]}</span></div></article>`).join('')}</div>`;
}
function devicesPage(){
  const stopped=sc()==='stopped'||sc()==='nodata',running=!stopped;
  const acct=state.scope==='account'&&sc()!=='nodata'&&!accountBlocked();
  return `<div class="subheading first"><div><h2>${state.scope==='account'?'账号设备':'本机设备'}</h2><p>${state.scope==='account'?'已同步到当前账号的设备用量':'本机运行信息，账号其他设备不计入当前范围'}</p></div><span class="badge-muted">${acct?'3 台已连接':'1 台设备'}</span></div>${deviceCards()}<section class="collector-panel"><div class="collector-header"><div class="collector-title"><span class="device-icon">${icon('terminal')}</span><div><h2>本机采集服务</h2><small>MacBook Pro · ${running?'后台运行中':'未运行'} · 与网页独立运行 · 不受上方周期影响</small></div></div><span class="status-pill ${running?'':'warn'}"><span class="dot ${running?'':'amber'}"></span>${running?'运行中':'未运行'}</span></div><div class="collector-stats"><div><span>运行时长</span><b>${running?'8h 24m':'—'}</b><small>${running?'今日 06:08 启动':'上次停止于 12:04'}</small></div><div><span>待同步记录</span><b>${sc()==='nodata'?'0':PENDING}</b><small>${sc()==='nodata'?'尚无记录':'本地已保存，等待确认'}</small></div><div><span>已确认记录</span><b>${sc()==='nodata'?'0':'8,436'}</b><small>${running?'本次运行以来':'上次运行期间'}</small></div><div><span>进程资源</span><b>${running?'42.6 <small>MB</small>':'—'}</b><small>${running?'CPU 0.8% · 示例观测值':'进程未运行'}</small></div></div><div class="readonly-note">${icon('shield')}<div><b>首版面板为只读</b><p>采集的启动与停止使用 <code>tokendance run</code> 或系统服务；立即同步使用 <code>tokendance sync</code>。面板不会成为第二个写入者。</p></div></div></section><section class="panel">${heading('同步动态','本机最近的处理记录',`<span class="panel-subtag">本机日志摘要</span>`)}<div class="timeline">${(sc()==='nodata'?[]:stopped?[['12:04:31','采集进程已退出','SIGTERM'],['12:04:30','已完成本地统计处理','无统计积压']]:[['14:32:08','已同步 128 条用量记录','服务端已确认'],['14:31:42','Codex 与 Claude Code 额度已更新','2 个来源'],['14:30:16','Cursor 额度需要重新授权','用量采集不受影响'],['14:29:55','已完成本地统计处理','无统计积压']]).map(([time,msg,label],i)=>`<div class="timeline-item"><time>${time}</time><span class="dot ${i===2&&running?'amber':''}"></span><p>${msg}</p><small>${label}</small></div>`).join('')||`<p class="footnote">暂无记录。</p>`}</div></section>`;
}

/* ---------- 顶栏、横幅与整体渲染 ---------- */
function chipState(){
  const s=sc();
  if(s==='disconnected')return ['bad','连接已断开'];
  if(s==='stopped')return ['warn','采集未运行'];
  if(s==='offline')return ['warn','网络离线'];
  if(s==='expired')return ['warn','登录已过期'];
  if(s==='loggedout')return ['idle','未登录 · 仅本机'];
  if(s==='nodata')return ['idle','尚无数据'];
  if(partialActive())return ['warn','部分数据失败'];
  return ['ok',`采集正常 · 待同步 ${PENDING}`];
}
function fetchedText(){
  const s=sc();
  if(s==='disconnected')return '最后更新 14:32:08';
  if(s==='offline')return '账号数据更新于 14:20:41 · 已过期 26 分钟';
  if(s==='expired')return '账号数据不可用 · 本机数据更新于 14:32:08';
  if(s==='nodata')return '尚未获取数据';
  return '数据更新于 14:32:08';
}
function banner(){
  const s=sc();
  if(s==='offline')return state.scope==='account'?`<div class="banner amber" role="status">${icon('wifi')}<div><b>网络离线，账号数据为快照</b><span>账号统计停留在 14:20:41（已过期 26 分钟）。不会自动换成本机数据。</span></div><button class="button" data-action="to-local">查看本机数据</button></div>`:`<div class="banner amber" role="status">${icon('wifi')}<div><b>网络离线</b><span>本机数据不受影响；上传暂停，恢复网络后自动继续。</span></div></div>`;
  if(s==='expired')return `<div class="banner amber" role="status">${icon('key')}<div><b>登录已过期</b><span>采集继续在本机运行。账号统计需要重新登录：</span></div><code class="inline-code">tokendance login</code></div>`;
  if(s==='loggedout')return `<div class="banner" role="status">${icon('cloud')}<div><b>未登录，正在查看本机数据</b><span>登录后可同步并查看全部设备与排名，也可以一直只用本机模式。</span></div><code class="inline-code">tokendance login</code></div>`;
  if(partialActive())return `<div class="banner amber" role="status">${icon('alert')}<div><b>部分数据加载失败</b><span>${[state.failed.trend&&'趋势',state.failed.skills&&'Skill'].filter(Boolean).join('、')}暂时不可用，其余内容不受影响。</span></div><button class="button" data-action="retry-all">全部重试</button></div>`;
  return '';
}
function userCard(){
  const s=sc();
  if(s==='loggedout')return `<span class="avatar idle">?</span><div><b>未登录</b><small>仅本机数据</small></div><span class="dot idle"></span>`;
  if(s==='expired')return `<span class="avatar">A</span><div><b>Alex</b><small>登录已过期</small></div><span class="dot amber"></span>`;
  return `<span class="avatar">A</span><div><b>Alex</b><small>个人账号 · 已登录</small></div><span class="dot"></span>`;
}
function scopeNote(){
  if(sc()==='loggedout')return '未登录 · 仅本机';
  if(state.scope==='account')return sc()==='nodata'?'尚无设备':'3 台设备';
  return 'MacBook Pro · 含尚未同步的数据';
}
function focusKey(){
  const a=document.activeElement;if(!a||!a.closest||!a.closest('#view'))return null;
  if(a.id)return '#'+a.id;
  for(const k of ['action','source','date'])if(a.dataset&&a.dataset[k])return `[data-${k}="${a.dataset[k]}"]`;
  return null;
}
function render(){
  const key=focusKey(),s=sc();
  document.getElementById('page-title').textContent=meta[state.view];
  document.getElementById('breadcrumb').textContent=meta[state.view];
  document.querySelectorAll('[data-view]').forEach(el=>{el.classList.toggle('active',el.dataset.view===state.view);if(el.dataset.view===state.view)el.setAttribute('aria-current','page');else el.removeAttribute('aria-current');});
  document.querySelectorAll('[data-scope]').forEach(el=>{el.setAttribute('aria-pressed',String(el.dataset.scope===state.scope));if(el.dataset.scope==='account'){const lo=s==='loggedout';el.disabled=lo;el.title=lo?'登录后可查看全部设备':'';}});
  document.querySelectorAll('[data-range]').forEach(el=>el.setAttribute('aria-pressed',String(el.dataset.range===state.range)));
  document.querySelector('.controls').hidden=s==='nodata';
  document.getElementById('scope-note').textContent=scopeNote();
  document.getElementById('date-range').textContent=ranges[state.range].date;
  const [cls,text]=chipState(),chip=document.getElementById('status-chip');
  chip.className=`status-chip ${cls}`;chip.innerHTML=`<span class="dot"></span>${text}`;
  document.getElementById('fetched').textContent=fetchedText();
  document.getElementById('fetched').className=`fetched ${stale()?'stale':''}`;
  document.getElementById('user-card').innerHTML=userCard();
  document.getElementById('nav-dot').hidden=!(s==='stopped'||s==='offline');
  document.getElementById('banner').innerHTML=banner();
  const disc=s==='disconnected';
  document.getElementById('disconnect').hidden=!disc;
  document.querySelector('main').inert=disc;document.querySelector('.sidebar').inert=disc;
  const view=document.getElementById('view');
  view.classList.toggle('is-stale',stale());
  view.innerHTML=({overview,agents:agentsPage,activity:activityPage,devices:devicesPage})[state.view]();
  renderChart();bindView();
  if(key){const el=document.querySelector('#view '+key)||document.getElementById('content');el.focus({preventScroll:true});}
}
function navigate(view){if(!meta[view])view='overview';state.view=view;render();window.scrollTo({top:0,behavior:'instant'});}
let toastTimer;
function toast(text){const el=document.getElementById('toast');el.textContent=text;el.classList.add('visible');clearTimeout(toastTimer);toastTimer=setTimeout(()=>el.classList.remove('visible'),3200);}
function dialog(title,html,eyebrow='DETAILS'){document.getElementById('dialog-title').textContent=title;document.getElementById('dialog-content').innerHTML=html;document.getElementById('dialog-eyebrow').textContent=eyebrow;document.getElementById('detail-dialog').showModal();}
function quotaHelp(){dialog('Cursor 额度待更新','<p>本机 Cursor 的额度授权已过期。Token 用量仍然正常采集，当前额度显示为未知。</p><div class="callout"><p>上次观测：12 分钟前<br>上次已用：64%（历史快照，非当前额度）<br>重置时间：待重新获取</p></div><p>正式版本会在这里提供来源连接指引。本原型仅展示状态，不访问或修改工具账号。</p>','SOURCE HEALTH');}
function heatKeys(e){
  const cur=e.target.closest('[data-date]');if(!cur)return;
  const grid=cur.closest('.heatmap'),all=[...grid.children],i=all.indexOf(cur);
  const step={ArrowUp:-1,ArrowDown:1,ArrowLeft:-7,ArrowRight:7}[e.key];
  if(!step)return;e.preventDefault();
  const j=i+step;
  if(!all[j]||all[j].classList.contains('future'))return;
  cur.tabIndex=-1;all[j].tabIndex=0;all[j].focus();
}
function bindView(){
  document.querySelectorAll('#view [data-action],#banner [data-action]').forEach(el=>el.addEventListener('click',()=>{
    const a=el.dataset.action;
    if(a.startsWith('goto-'))location.hash=a.slice(5);
    else if(a==='quota-help')quotaHelp();
    else if(a==='to-local'){state.scope='local';render();toast('已切换到本机数据');}
    else if(a==='retry-trend'){state.failed.trend=false;render();toast('趋势已重新加载（演示）');}
    else if(a==='retry-skills'){state.failed.skills=false;render();toast('Skill 已重新加载（演示）');}
    else if(a==='retry-all'){state.failed={trend:false,skills:false};render();toast('已重新加载（演示）');}
  }));
  document.querySelectorAll('[data-source]').forEach(el=>el.addEventListener('click',()=>dialog('来源详情',`<p>${{pi:'已识别 Pi 的本地目录，当前周期尚未观察到可信用量。',opencode:'尚未发现 OpenCode 的本地数据库，可在安装和使用后自动发现。',deepseek:'DeepSeek Harness 尚未连接，无法确认用量与额度。'}[el.dataset.source]}</p><div class="callout"><p>未记录与实际用量为零是不同状态。这里不会用 0 填补未知指标。</p></div>`,'SOURCE DETAILS')));
  document.querySelectorAll('[data-date]').forEach(el=>el.addEventListener('click',()=>{state.heatSel=el.dataset.date;render();}));
  const hm=document.querySelector('.heatmap');if(hm)hm.addEventListener('keydown',heatKeys);
  const af=document.getElementById('agent-filter'),mf=document.getElementById('model-filter');
  if(af)af.addEventListener('change',()=>{state.agent=af.value;renderChart();});
  if(mf)mf.addEventListener('change',()=>{state.model=mf.value;renderChart();});
}
document.querySelectorAll('[data-range]').forEach(el=>el.addEventListener('click',()=>{state.range=el.dataset.range;render();}));
document.querySelectorAll('[data-scope]').forEach(el=>el.addEventListener('click',()=>{state.scope=el.dataset.scope;render();}));
document.getElementById('status-chip').addEventListener('click',()=>{if(sc()!=='disconnected')location.hash='devices';});
document.getElementById('scenario').addEventListener('change',e=>{
  state.scenario=e.target.value;state.failed={trend:true,skills:true};
  if(state.scenario==='loggedout')state.scope='local';
  render();
});
document.getElementById('refresh').addEventListener('click',async e=>{const btn=e.currentTarget;btn.classList.add('spinning');btn.disabled=true;await new Promise(r=>setTimeout(r,550));render();btn.classList.remove('spinning');btn.disabled=false;toast(stale()?'仍然离线：账号数据保持 14:20 的快照':'示例数据已刷新 · 额度仍按其独立观测时间展示');});
document.getElementById('help-button').addEventListener('click',()=>dialog('一个面板，看清全部','<p>这是 TokenDance CLI 本地网页面板的交互原型。所有数据均为设计示例；右上角“状态演示”可切换离线、未登录、断开等状态。</p><div class="callout"><p><b>总览</b> · 个人指标、趋势与额度<br><b>用量与额度</b> · Agent、模型、额度窗口与重置时间<br><b>活动与技能</b> · 年度热力图与 Skill 调用<br><b>设备与同步</b> · 运行状态、同步积压与设备</p></div><p>正式版本使用以下命令打开：</p><code>tokendance login<br>tokendance dashboard</code><p>在没有桌面的机器上（SSH），先在远端运行 <code>tokendance dashboard --port 18765</code>，再在自己的电脑建立隧道：</p><code>ssh -N -L 127.0.0.1:18765:127.0.0.1:18765 user@host</code><p>关闭网页不影响后台采集。CLI 不提供悬浮球、托盘或桌面置顶窗口。</p>','LOCAL DASHBOARD'));
document.getElementById('close-dialog').addEventListener('click',()=>document.getElementById('detail-dialog').close());
document.getElementById('detail-dialog').addEventListener('click',e=>{if(e.target===e.currentTarget){const r=e.currentTarget.getBoundingClientRect();if(e.clientX<r.left||e.clientX>r.right||e.clientY<r.top||e.clientY>r.bottom)e.currentTarget.close();}});
let resizeTimer,wasNarrow=narrow();
window.addEventListener('resize',()=>{clearTimeout(resizeTimer);resizeTimer=setTimeout(()=>{if(wasNarrow!==narrow()){wasNarrow=narrow();render();}else renderChart();},100);});
window.addEventListener('hashchange',()=>navigate(location.hash.slice(1)));
navigate(location.hash.slice(1)||'overview');
