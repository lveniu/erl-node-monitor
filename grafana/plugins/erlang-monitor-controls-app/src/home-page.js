import React from 'react';

const h = React.createElement;

const css = `
.em-home{--home-ink:#eef5ff;--home-muted:#92a6bf;--home-line:#28415f;--home-blue:#8bc6ff;--home-green:#65d6b7;min-height:calc(100vh - 40px);box-sizing:border-box;padding:clamp(24px,4vw,64px);color:var(--home-ink);font-family:"Segoe UI","Noto Sans SC",sans-serif;background:radial-gradient(circle at 12% 8%,rgba(61,125,207,.22),transparent 29%),radial-gradient(circle at 88% 20%,rgba(50,171,142,.12),transparent 25%),linear-gradient(145deg,#07111e,#0d1a2c 54%,#08121f)}
.em-home-shell{max-width:1280px;margin:0 auto}.em-home-head{display:grid;grid-template-columns:minmax(0,1fr) auto;gap:30px;align-items:end;padding-bottom:28px;border-bottom:1px solid var(--home-line)}
.em-home-kicker{color:var(--home-blue);font:700 11px/1.4 ui-monospace,Consolas,monospace;letter-spacing:.18em;text-transform:uppercase}.em-home-title{max-width:780px;margin:8px 0 12px;font-family:Georgia,"Noto Serif SC",serif;font-size:clamp(36px,5vw,64px);font-weight:500;line-height:1.02;letter-spacing:-.045em}.em-home-lead{max-width:760px;margin:0;color:var(--home-muted);font-size:14px;line-height:1.8}
.em-home-status{display:flex;align-items:center;gap:9px;padding:9px 13px;border:1px solid var(--home-line);border-radius:999px;background:rgba(8,20,35,.7);color:var(--home-green);font:11px ui-monospace,Consolas,monospace;white-space:nowrap}.em-home-status:before{content:"";width:7px;height:7px;border-radius:50%;background:currentColor;box-shadow:0 0 14px currentColor}
.em-home-grid{display:grid;grid-template-columns:1.08fr .92fr;gap:16px;margin-top:24px}.em-home-card{position:relative;display:flex;min-height:250px;box-sizing:border-box;overflow:hidden;border:1px solid var(--home-line);border-radius:12px;padding:28px;color:inherit;text-decoration:none;background:linear-gradient(160deg,rgba(20,38,62,.96),rgba(10,23,40,.96));box-shadow:0 24px 58px rgba(0,0,0,.2);transition:transform .18s ease,border-color .18s ease,box-shadow .18s ease}.em-home-card:after{content:"";position:absolute;right:-48px;bottom:-70px;width:180px;height:180px;border:1px solid rgba(139,198,255,.22);border-radius:50%;box-shadow:0 0 0 28px rgba(139,198,255,.035),0 0 0 58px rgba(139,198,255,.02)}.em-home-card:hover{transform:translateY(-3px);border-color:#4a78a6;box-shadow:0 30px 70px rgba(0,0,0,.28)}.em-home-card:focus{outline:2px solid var(--home-blue);outline-offset:3px}.em-home-card.ops{background:linear-gradient(150deg,rgba(17,47,49,.96),rgba(9,26,30,.97));border-color:#285654}.em-home-card.ops:after{border-color:rgba(101,214,183,.22);box-shadow:0 0 0 28px rgba(101,214,183,.035),0 0 0 58px rgba(101,214,183,.02)}
.em-home-card-body{position:relative;z-index:1;display:flex;flex-direction:column;align-items:flex-start;max-width:560px}.em-home-index{color:var(--home-blue);font:700 10px ui-monospace,Consolas,monospace;letter-spacing:.16em}.em-home-card.ops .em-home-index{color:var(--home-green)}.em-home-card h2{margin:18px 0 10px;font-family:Georgia,"Noto Serif SC",serif;font-size:30px;font-weight:500}.em-home-card p{margin:0 0 24px;color:#a9bbd1;font-size:13px;line-height:1.75}.em-home-tags{display:flex;gap:7px;flex-wrap:wrap;margin-bottom:25px}.em-home-tag{border:1px solid rgba(139,198,255,.22);border-radius:999px;padding:5px 9px;color:#bcd8f4;background:rgba(139,198,255,.05);font:10px ui-monospace,Consolas,monospace}.em-home-card.ops .em-home-tag{border-color:rgba(101,214,183,.22);color:#b7ddcf;background:rgba(101,214,183,.05)}.em-home-enter{display:inline-flex;align-items:center;gap:10px;margin-top:auto;color:var(--home-blue);font-size:13px;font-weight:700}.em-home-card.ops .em-home-enter{color:var(--home-green)}.em-home-enter span{font-size:18px;transition:transform .18s ease}.em-home-card:hover .em-home-enter span{transform:translateX(4px)}
.em-home-foot{display:flex;justify-content:space-between;gap:20px;margin-top:18px;padding:15px 2px;color:#7489a3;font-size:11px;line-height:1.6}.em-home-foot strong{color:#a8bdd4;font-weight:600}
@media(max-width:820px){.em-home{padding:22px 16px}.em-home-head{grid-template-columns:1fr;align-items:start}.em-home-status{justify-self:start}.em-home-grid{grid-template-columns:1fr}.em-home-card{min-height:230px;padding:23px}.em-home-foot{display:block}.em-home-foot span{display:block;margin-bottom:7px}}
`;

const entries = [
  {
    className: 'code', index: '01 / CODE MCP', title: '代码解析',
    description: '选择已注册的代码项目，通过 MCP 进行只读仓库检查、调用链分析和多轮追问。',
    tags: ['多轮对话', 'Markdown', '只读分析'],
    href: '/a/erlang-monitor-controls-app/code-analysis', action: '进入代码解析',
  },
  {
    className: 'ops', index: '02 / OPS AGENT', title: '运维 Agent',
    description: '结合监控上下文和项目 Skill 定位服务器问题；受控命令仍遵循权限和审批边界。',
    tags: ['监控上下文', '任务轨迹', '审批边界'],
    href: '/a/erlang-monitor-controls-app/ops-agent', action: '进入运维 Agent',
  },
];

export function HomePage() {
  return h('main', { className: 'em-home' },
    h('style', null, css),
    h('div', { className: 'em-home-shell' },
      h('header', { className: 'em-home-head' },
        h('div', null,
          h('div', { className: 'em-home-kicker' }, 'ERLANG MONITOR · CONTROL CENTER'),
          h('h1', { className: 'em-home-title' }, '监控、运维与代码分析，一个入口。'),
          h('p', { className: 'em-home-lead' }, '这是 Erlang Monitor Controls 的应用入口。按任务选择工作台；服务器运行总览仍从具体 Dashboard 上下文进入。')),
        h('div', { className: 'em-home-status' }, '应用入口已就绪')),
      h('section', { className: 'em-home-grid', 'aria-label': '应用功能入口' },
        entries.map((entry) => h('a', { className: `em-home-card ${entry.className}`, href: entry.href, key: entry.href },
          h('div', { className: 'em-home-card-body' },
            h('span', { className: 'em-home-index' }, entry.index),
            h('h2', null, entry.title),
            h('p', null, entry.description),
            h('div', { className: 'em-home-tags' }, entry.tags.map((tag) => h('span', { className: 'em-home-tag', key: tag }, tag))),
            h('div', { className: 'em-home-enter' }, entry.action, h('span', { 'aria-hidden': true }, '→')))))),
      h('footer', { className: 'em-home-foot' },
        h('span', null, h('strong', null, '运行总览：'), '请从具体服务器 Dashboard 进入，以自动携带 dashboard_uid。'),
        h('span', null, '代码解析与运维 Agent 需要 Editor 或更高权限。'))));
}
