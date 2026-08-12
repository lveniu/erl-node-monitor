import { getBackendSrv } from '@grafana/runtime';
import React from 'react';

import { appendTrace, buildConversationQuestion, formatDuration, markdownForClipboard, mcpInitializedNotification, mcpInitializeRequest, mcpToolRequest, mcpToolsListRequest, parseMCPEnvelope, parseProjects, parseRepositoryInspection, parseSafeMarkdown, reconcileConversationProject, requireMCPTools, restoreConversation, toolText } from './code-logic.js';
import { safeMarkdownText } from './shared-logic.js';

const h = React.createElement;
const codeProxyURL = '/api/plugin-proxy/erlang-monitor-controls-app/code-mcp';
const codeHealthURL = '/api/plugin-proxy/erlang-monitor-controls-app/code-mcp-health';
const storageKey = 'erlang-monitor-code-chat-v1';

const css = `
.code-page{--cc-ink:#eef4ff;--cc-muted:#96a8c0;--cc-line:#273b58;--cc-panel:#101c2e;--cc-panel2:#0c1728;--cc-blue:#82bdff;--cc-blue2:#4b92df;--cc-green:#68d7b7;--cc-red:#ff8d96;max-width:1600px;margin:0 auto;padding:20px 24px 28px;color:var(--cc-ink);font-family:Inter,system-ui,sans-serif;background:radial-gradient(circle at 12% 0,rgba(70,126,205,.2),transparent 31%),radial-gradient(circle at 88% 18%,rgba(58,173,151,.08),transparent 27%),linear-gradient(145deg,#08111e,#0c1727 56%,#08101b);min-height:calc(100vh - 40px)}
.code-head{display:flex;justify-content:space-between;gap:24px;align-items:flex-start;padding:7px 2px 20px}.code-brand{max-width:850px}.code-kicker{color:var(--cc-blue);font:700 10px ui-monospace,monospace;letter-spacing:.2em}.code-title{font-family:Georgia,"Noto Serif SC",serif;font-size:36px;letter-spacing:-.04em;margin:5px 0 7px}.code-sub{color:var(--cc-muted);font-size:13px;line-height:1.7}.code-head-side{display:grid;justify-items:end;gap:10px}.code-health{display:flex;align-items:center;gap:8px;border:1px solid var(--cc-line);border-radius:999px;padding:7px 11px;font:11px ui-monospace,monospace;color:var(--cc-green);white-space:nowrap;background:rgba(9,20,34,.72)}.code-health-dot{width:7px;height:7px;border-radius:50%;background:currentColor;box-shadow:0 0 12px currentColor}.code-capabilities{display:flex;gap:6px;flex-wrap:wrap;justify-content:flex-end}.code-chip{border:1px solid rgba(130,189,255,.25);border-radius:999px;padding:4px 8px;color:#b8cce6;font:10px ui-monospace,monospace;background:rgba(130,189,255,.05)}
.code-grid{display:grid;grid-template-columns:minmax(238px,.7fr) minmax(560px,1.85fr) minmax(244px,.72fr);gap:14px;align-items:start}.code-panel{background:linear-gradient(180deg,rgba(17,30,49,.96),rgba(12,23,40,.96));border:1px solid var(--cc-line);border-radius:10px;padding:16px;min-width:0;box-shadow:0 20px 48px rgba(0,0,0,.18)}.code-panel-head{display:flex;justify-content:space-between;align-items:center;gap:10px;margin-bottom:13px}.code-panel h2{font:500 18px Georgia,"Noto Serif SC",serif;margin:0}.code-panel-label{color:var(--cc-muted);font:10px ui-monospace,monospace}.code-field{display:grid;gap:7px;margin-bottom:12px}.code-field label{color:var(--cc-muted);font:700 10px ui-monospace,monospace;letter-spacing:.12em;text-transform:uppercase}.code-select,.code-textarea{width:100%;box-sizing:border-box;background:#081422;color:var(--cc-ink);border:1px solid #304563;border-radius:6px;padding:10px 11px;font:13px ui-monospace,Consolas,monospace;line-height:1.55;transition:border-color .16s,box-shadow .16s}.code-select:hover,.code-textarea:hover{border-color:#416184}.code-textarea{min-height:104px;resize:vertical}.code-button{border:1px solid var(--cc-blue);border-radius:6px;background:linear-gradient(180deg,#91c7ff,#73b4f8);color:#061321;padding:9px 14px;font-weight:750;cursor:pointer;transition:transform .14s,filter .14s}.code-button:hover:not(:disabled){filter:brightness(1.08);transform:translateY(-1px)}.code-button.secondary{background:transparent;color:var(--cc-blue)}.code-button.danger{background:transparent;border-color:var(--cc-red);color:var(--cc-red)}.code-button:disabled{opacity:.42;cursor:not-allowed}.code-actions{display:flex;gap:8px;flex-wrap:wrap;align-items:center}.code-shortcut{margin-left:auto;color:#71849e;font:10px ui-monospace,monospace}.code-note,.code-error{border:1px solid rgba(130,189,255,.2);border-left:3px solid var(--cc-blue);border-radius:5px;padding:10px;background:rgba(130,189,255,.06);color:#bed5f3;font-size:11px;line-height:1.6}.code-error{border-color:rgba(255,141,150,.32);border-left-color:var(--cc-red);background:rgba(255,141,150,.07);color:#ffc5c9}.code-project-card{padding:11px;border:1px solid rgba(130,189,255,.17);border-radius:7px;background:rgba(6,17,30,.45)}.code-project-name{font-size:14px;font-weight:700;color:#dcecff}.code-project-path{color:#8ea5c2;font:10px/1.55 ui-monospace,monospace;overflow-wrap:anywhere;margin-top:4px}.code-project-meta{display:grid;grid-template-columns:1fr 1fr;gap:9px;margin-top:11px}.code-project-meta div{border-top:1px solid rgba(42,60,88,.75);padding-top:8px;min-width:0}.code-project-meta span{display:block;color:var(--cc-muted);font:9px ui-monospace,monospace;text-transform:uppercase;letter-spacing:.1em;margin-bottom:4px}.code-project-meta strong{display:block;font:10px ui-monospace,monospace;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.code-boundary{position:sticky;top:10px}.code-inspection{margin-top:12px;border-top:1px solid var(--cc-line);padding-top:10px}.code-inspection summary{cursor:pointer;color:var(--cc-blue);font:11px ui-monospace,monospace}.code-inspection-body{max-height:260px;overflow:auto;margin-top:9px}
.code-chat{padding:0;overflow:hidden}.code-chat>.code-panel-head{padding:15px 17px 12px;margin:0;border-bottom:1px solid var(--cc-line)}.code-session{border:1px solid rgba(104,215,183,.22);border-radius:999px;padding:4px 8px;color:var(--cc-green);font:10px ui-monospace,monospace}.code-turns{display:grid;gap:14px;min-height:360px;max-height:calc(100vh - 360px);overflow:auto;padding:18px 17px;scrollbar-color:#334a67 transparent}.code-turn{border:1px solid var(--cc-line);border-radius:9px;padding:13px 14px;background:rgba(18,34,56,.72)}.code-turn.user{margin-left:12%;border-color:#375e8c;background:linear-gradient(135deg,rgba(35,69,108,.62),rgba(23,44,71,.75))}.code-turn.assistant{margin-right:2%;border-color:#2d6258;background:linear-gradient(135deg,rgba(18,46,48,.52),rgba(16,30,48,.78))}.code-turn-head{display:flex;justify-content:space-between;color:var(--cc-muted);font:10px ui-monospace,monospace;letter-spacing:.06em;margin-bottom:11px}.code-turn-role{display:flex;align-items:center;gap:7px}.code-avatar{display:grid;place-items:center;width:18px;height:18px;border-radius:5px;background:rgba(130,189,255,.15);color:var(--cc-blue);font-weight:800}.code-turn.assistant .code-avatar{background:rgba(104,215,183,.13);color:var(--cc-green)}.code-composer{padding:14px 17px 16px;border-top:1px solid var(--cc-line);background:rgba(7,16,29,.58)}.code-composer .code-field{margin-bottom:9px}.code-empty{color:var(--cc-muted);font-size:13px;line-height:1.65}.code-welcome{text-align:center;padding:24px 20px 18px}.code-welcome-mark{display:grid;place-items:center;width:48px;height:48px;margin:0 auto 13px;border:1px solid rgba(130,189,255,.35);border-radius:13px;background:linear-gradient(145deg,rgba(130,189,255,.17),rgba(104,215,183,.08));color:var(--cc-blue);font:700 19px ui-monospace,monospace}.code-welcome h3{margin:0 0 7px;color:var(--cc-ink);font:500 21px Georgia,"Noto Serif SC",serif}.code-welcome>p{max-width:580px;margin:0 auto;color:var(--cc-muted);font-size:12px;line-height:1.7}.code-steps{display:grid;grid-template-columns:repeat(3,1fr);gap:8px;margin:18px 0 14px;text-align:left}.code-step{border:1px solid rgba(130,189,255,.15);border-radius:7px;padding:10px;background:rgba(6,17,30,.4)}.code-step b{display:block;color:var(--cc-blue);font:10px ui-monospace,monospace;margin-bottom:5px}.code-step span{color:#b4c5da;font-size:11px;line-height:1.5}.code-examples{display:flex;gap:7px;justify-content:center;flex-wrap:wrap}.code-example{border:1px solid #304968;border-radius:999px;background:transparent;color:#bcd1e8;padding:6px 10px;font-size:11px;cursor:pointer}.code-example:hover{border-color:var(--cc-blue);color:var(--cc-blue)}
.code-markdown{font-size:13px;line-height:1.72;color:#dce7f5;overflow-wrap:anywhere}.code-markdown>:first-child{margin-top:0}.code-markdown>:last-child{margin-bottom:0}.code-markdown h1,.code-markdown h2,.code-markdown h3,.code-markdown h4,.code-markdown h5,.code-markdown h6{font-family:Inter,system-ui,sans-serif;color:#f4f8ff;line-height:1.35;margin:1.1em 0 .5em}.code-markdown h1{font-size:20px;border-bottom:1px solid var(--cc-line);padding-bottom:6px}.code-markdown h2{font-size:17px}.code-markdown h3{font-size:15px;color:#bcd8f7}.code-markdown h4,.code-markdown h5,.code-markdown h6{font-size:13px}.code-markdown p{margin:.65em 0;white-space:pre-wrap}.code-markdown ul,.code-markdown ol{margin:.65em 0;padding-left:22px}.code-markdown li{margin:.3em 0}.code-markdown strong{color:#fff}.code-markdown em{color:#b9cae0}.code-markdown code{font:12px/1.5 ui-monospace,Consolas,monospace;color:#a8d2ff;background:#071321;border:1px solid #263d59;border-radius:4px;padding:1px 5px}.code-codeblock{margin:.8em 0;border:1px solid #2a405d;border-radius:7px;overflow:hidden;background:#06111e}.code-code-head{display:flex;justify-content:space-between;padding:6px 10px;border-bottom:1px solid #243a55;color:#7890ad;font:9px ui-monospace,monospace;text-transform:uppercase}.code-codeblock pre{margin:0;padding:12px;overflow:auto}.code-codeblock pre code{display:block;border:0;background:transparent;padding:0;color:#d9e8f7;white-space:pre;font:12px/1.65 ui-monospace,Consolas,monospace}.code-markdown blockquote{margin:.8em 0;padding:7px 12px;border-left:3px solid var(--cc-blue2);background:rgba(75,146,223,.08);color:#b8cbe2}.code-markdown hr{border:0;border-top:1px solid var(--cc-line);margin:1.1em 0}.code-table-wrap{overflow:auto;margin:.8em 0;border:1px solid #293e59;border-radius:6px}.code-markdown table{width:100%;border-collapse:collapse;font-size:11px}.code-markdown th,.code-markdown td{padding:8px 10px;border-bottom:1px solid #263a54;border-right:1px solid #263a54;text-align:left;vertical-align:top}.code-markdown th{color:#b9d8f7;background:#0a1929;font-weight:700}.code-markdown tr:last-child td{border-bottom:0}.code-markdown th:last-child,.code-markdown td:last-child{border-right:0}.code-markdown a{color:#88c2ff;text-decoration:none;border-bottom:1px dotted #5889bb}.code-markdown a:hover{color:#b5d9ff}.code-traces{display:grid;gap:10px}.code-trace{position:relative;border-left:2px solid #304968;padding:2px 0 3px 12px}.code-trace:before{content:"";position:absolute;left:-5px;top:3px;width:8px;height:8px;border-radius:50%;background:#142842;border:1px solid var(--cc-blue)}.code-trace-head{display:flex;justify-content:space-between;gap:8px;align-items:center}.code-trace strong{display:block;color:var(--cc-blue);font:11px ui-monospace,monospace}.code-trace span{color:var(--cc-muted);font-size:10px;line-height:1.5}.code-trace-time{flex:none;border:1px solid rgba(130,189,255,.22);border-radius:999px;padding:2px 6px;color:#a9cff8!important;font:9px ui-monospace,monospace!important;background:rgba(130,189,255,.05)}.code-trace-panel{position:sticky;top:10px}.code-timing{display:flex;align-items:center;gap:10px;min-width:210px;padding:8px 10px;border:1px solid rgba(104,215,183,.28);border-radius:6px;background:rgba(104,215,183,.06);color:#bde8da;font:10px ui-monospace,Consolas,monospace}.code-timing-dot{width:7px;height:7px;border-radius:50%;background:var(--cc-green);box-shadow:0 0 11px var(--cc-green);animation:code-pulse 1.25s ease-in-out infinite}.code-timing-copy{display:grid;gap:2px}.code-timing-copy b{color:#e3f8f1;font-size:10px}.code-timing-copy span{color:#8fbbaa;font-size:9px}.code-page button:focus,.code-page textarea:focus,.code-page select:focus,.code-page summary:focus,.code-page a:focus{outline:2px solid var(--cc-blue);outline-offset:2px}@keyframes code-pulse{50%{opacity:.35;transform:scale(.72)}}
.code-copy-button{border:1px solid rgba(130,189,255,.3);border-radius:5px;background:rgba(130,189,255,.06);color:#bcd7f4;padding:5px 9px;font-size:10px;cursor:pointer}.code-copy-button:hover{border-color:var(--cc-blue);color:var(--cc-blue)}.code-copy-button:disabled{opacity:.45;cursor:wait}
.code-project-branch{display:inline-flex;margin-top:7px;border:1px solid rgba(130,189,255,.22);border-radius:999px;padding:3px 8px;color:#a9ccef;background:rgba(130,189,255,.06);font:10px/1.4 ui-monospace,monospace}
.code-inspection-summary{display:grid;gap:10px;margin-top:10px}.code-inspection-title{font-size:12px;font-weight:750;color:#e6f1ff}.code-inspection-id{display:flex;gap:6px;flex-wrap:wrap}.code-inspection-id code,.code-boundary-tag{border:1px solid rgba(130,189,255,.18);border-radius:4px;background:#081422;color:#b9d9fa;padding:3px 6px;font:9px/1.4 ui-monospace,monospace}.code-inspection-grid{display:grid;grid-template-columns:1fr 1fr;gap:7px}.code-check-item{border:1px solid rgba(130,189,255,.13);border-radius:6px;padding:8px;background:rgba(6,17,30,.36);min-width:0}.code-check-item span{display:block;color:var(--cc-muted);font:9px ui-monospace,monospace;text-transform:uppercase;letter-spacing:.08em;margin-bottom:4px}.code-check-item strong{display:block;color:#dcecff;font-size:10px;line-height:1.45;overflow-wrap:anywhere}.code-check-item.ok strong{color:var(--cc-green)}.code-check-item.warn strong{color:#ffd27e}.code-conflict{border-left:2px solid var(--cc-blue2);padding:7px 9px;background:rgba(75,146,223,.06);color:#b9cde5;font-size:10px;line-height:1.5}.code-advanced{border-top:1px solid var(--cc-line);padding-top:8px}.code-advanced summary{color:#91bde9;font:10px ui-monospace,monospace}.code-boundary-group{margin-top:9px}.code-boundary-group b{display:block;color:var(--cc-muted);font:9px ui-monospace,monospace;margin-bottom:5px}.code-boundary-tags{display:flex;gap:5px;flex-wrap:wrap}.code-limit-row{display:flex;gap:12px;margin-top:9px;color:#aebfd4;font-size:10px}
@media(max-width:1480px){.code-grid{grid-template-columns:minmax(225px,.67fr) minmax(500px,1.6fr)}.code-trace-panel{grid-column:1/-1;position:static}.code-traces{grid-template-columns:repeat(3,1fr)}}@media(max-width:800px){.code-page{padding:14px}.code-grid{grid-template-columns:1fr}.code-boundary,.code-trace-panel{position:static;grid-column:auto}.code-head{display:block}.code-head-side{justify-items:start;margin-top:13px}.code-capabilities{justify-content:flex-start}.code-title{font-size:29px}.code-turns{max-height:none}.code-turn.user,.code-turn.assistant{margin-left:0;margin-right:0}.code-steps,.code-traces{grid-template-columns:1fr}.code-shortcut{display:none}}
`;

function randomID(prefix) {
  return typeof crypto?.randomUUID === 'function' ? crypto.randomUUID() : `${prefix}-${Date.now()}-${Math.random().toString(16).slice(2)}`;
}

async function postMCP(body, signal) {
  const response = await fetch(codeProxyURL, {
    method: 'POST', credentials: 'same-origin', signal,
    headers: { 'Content-Type': 'application/json', Accept: 'application/json, text/event-stream' },
    body: JSON.stringify(body),
  });
  const payload = await response.text();
  if (!response.ok) throw new Error(`代码 MCP HTTP ${response.status}：${payload.slice(0, 300) || response.statusText}`);
  return payload;
}

async function initializeMCP(signal, runStage, totalStartedAt) {
  const initializeID = randomID('initialize');
  const initializePayload = await runStage('initialize', 'MCP 会话已建立', () => postMCP(mcpInitializeRequest(initializeID), signal), totalStartedAt);
  const initialized = parseMCPEnvelope(initializePayload, initializeID);
  if (initialized?.error || !initialized?.result?.serverInfo?.name) throw new Error('代码 MCP 初始化失败');
  await runStage('initialized', 'MCP 会话确认完成', () => postMCP(mcpInitializedNotification(), signal), totalStartedAt);
  const listID = randomID('tools');
  const listPayload = await runStage('tools', 'MCP 工具清单已返回', () => postMCP(mcpToolsListRequest(listID), signal), totalStartedAt);
  return requireMCPTools(parseMCPEnvelope(listPayload, listID), ['list_projects', 'inspect_repository', 'analyze_codebase']);
}

async function callTool(name, args, signal) {
  const id = randomID('mcp');
  const payload = await postMCP(mcpToolRequest(id, name, args), signal);
  return toolText(parseMCPEnvelope(payload, id), name);
}

function readStoredConversation() {
  try { return restoreConversation(JSON.parse(window.sessionStorage.getItem(storageKey) || 'null')); } catch { return null; }
}

function saveConversation(value) {
  try { window.sessionStorage.setItem(storageKey, JSON.stringify(value)); } catch { /* optional */ }
}

function traceLabel(type) {
  return { health: '健康检查', initialize: 'MCP 初始化', initialized: '会话确认', tools: '工具发现', protocol: 'initialize + tools/list', catalog: '项目清单', inspect: '仓库检查', analyze: '源码分析', error: '调用失败' }[type] || type;
}

function inlineNodes(tokens, prefix) {
  const nodes = [];
  for (const [index, token] of tokens.entries()) {
    const key = `${prefix}-${index}`;
    if (token.type === 'code') nodes.push(h('code', { key }, token.text));
    else if (token.type === 'strong') nodes.push(h('strong', { key }, token.text));
    else if (token.type === 'em') nodes.push(h('em', { key }, token.text));
    else if (token.type === 'link') nodes.push(h('a', { key, href: token.url, target: '_blank', rel: 'noopener noreferrer' }, token.text));
    else {
      const lines = token.text.split('\n');
      lines.forEach((line, lineIndex) => {
        if (lineIndex) nodes.push(h('br', { key: `${key}-br-${lineIndex}` }));
        if (line) nodes.push(line);
      });
    }
  }
  return nodes;
}

function MarkdownContent({ value, maxLength = 65536, copyable = false }) {
  const blocks = React.useMemo(() => parseSafeMarkdown(value, maxLength), [maxLength, value]);
  const [copyState, setCopyState] = React.useState('idle');
  async function copyMarkdown() {
    try {
      if (!navigator.clipboard?.writeText) throw new Error('clipboard unavailable');
      await navigator.clipboard.writeText(markdownForClipboard(value, maxLength));
      setCopyState('copied');
    } catch {
      setCopyState('failed');
    }
    window.setTimeout(() => setCopyState('idle'), 1800);
  }
  const content = blocks.map((block, index) => {
    const key = `md-${index}`;
    if (block.type === 'heading') return h(`h${block.level}`, { key }, inlineNodes(block.children, key));
    if (block.type === 'paragraph') return h('p', { key }, inlineNodes(block.children, key));
    if (block.type === 'quote') return h('blockquote', { key }, inlineNodes(block.children, key));
    if (block.type === 'rule') return h('hr', { key });
    if (block.type === 'list') {
      return h(block.ordered ? 'ol' : 'ul', { key }, block.items.map((item, itemIndex) => h('li', { key: `${key}-${itemIndex}` }, inlineNodes(item, `${key}-${itemIndex}`))));
    }
    if (block.type === 'table') {
      return h('div', { className: 'code-table-wrap', key }, h('table', null,
        h('thead', null, h('tr', null, block.header.map((cell, cellIndex) => h('th', { key: `${key}-h-${cellIndex}` }, inlineNodes(cell, `${key}-h-${cellIndex}`))))),
        h('tbody', null, block.rows.map((row, rowIndex) => h('tr', { key: `${key}-r-${rowIndex}` }, row.map((cell, cellIndex) => h('td', { key: `${key}-r-${rowIndex}-${cellIndex}` }, inlineNodes(cell, `${key}-r-${rowIndex}-${cellIndex}`))))))));
    }
    if (block.type === 'codeBlock') {
      return h('div', { className: 'code-codeblock', key },
        h('div', { className: 'code-code-head' }, h('span', null, block.language || 'code'), h('span', null, '只读代码片段')),
        h('pre', null, h('code', null, block.text)));
    }
    return null;
  });
  return h(React.Fragment, null,
    copyable ? h('div', { style: { display: 'flex', justifyContent: 'flex-end', marginBottom: '8px' } },
      h('button', { type: 'button', className: 'code-copy-button', onClick: copyMarkdown, disabled: copyState === 'copied' }, copyState === 'copied' ? '已复制 Markdown' : (copyState === 'failed' ? '复制失败' : '复制 Markdown'))) : null,
    h('div', { className: 'code-markdown' }, content));
}

function checkTone(value) {
  return /正常|可用|启用/.test(value) ? 'ok' : (/缺失|不可用|失败/.test(value) ? 'warn' : '');
}

function RepositoryInspection({ value }) {
  const report = React.useMemo(() => parseRepositoryInspection(value), [value]);
  const checkItem = (label, content) => content ? h('div', { className: `code-check-item ${checkTone(content)}` }, h('span', null, label), h('strong', null, content)) : null;
  const boundaryGroup = (label, items) => items.length ? h('div', { className: 'code-boundary-group' }, h('b', null, label), h('div', { className: 'code-boundary-tags' }, items.map((item) => h('code', { className: 'code-boundary-tag', key: item }, item)))) : null;
  return h('div', { className: 'code-inspection-summary' },
    report.projectName ? h('div', { className: 'code-inspection-title' }, report.projectName) : null,
    h('div', { className: 'code-inspection-id' },
      report.projectID ? h('code', null, `project_id · ${report.projectID}`) : null,
      report.projectCode ? h('code', null, `code · ${report.projectCode}`) : null),
    h('div', { className: 'code-inspection-grid' },
      checkItem('目录访问', report.access),
      checkItem('MODULE.md', report.module),
      checkItem('Agent', [report.agent.status, report.agent.name].filter(Boolean).join(' · ')),
      checkItem('SKILL', [report.skill.status, report.skill.name].filter(Boolean).join(' · ')),
      checkItem('分析前 SVN 更新', report.svnUpdate)),
    report.conflictPolicy ? h('div', { className: 'code-conflict' }, h('strong', null, '本地冲突策略：'), report.conflictPolicy) : null,
    h('details', { className: 'code-advanced' }, h('summary', null, '高级边界与运行限制'),
      boundaryGroup('允许目录', report.allowedDirectories),
      boundaryGroup('允许文件', report.allowedFiles),
      boundaryGroup('过滤目录', report.excludedDirectories),
      h('div', { className: 'code-limit-row' },
        report.timeout ? h('span', null, `分析超时：${report.timeout}`) : null,
        report.concurrency ? h('span', null, `项目并发上限：${report.concurrency}`) : null),
      report.extra.length ? h('div', { className: 'code-boundary-group' }, h('b', null, '其他说明'), h(MarkdownContent, { value: report.extra.join('\n'), maxLength: 2000 })) : null));
}

const promptExamples = [
  '定位这个模块的入口、主要调用链和关键分支',
  '解释上一轮提到的字段类型和数据来源',
  '检查这个错误码的触发条件与返回路径',
];

export function CodePage() {
  const restored = React.useMemo(readStoredConversation, []);
  const [projects, setProjects] = React.useState([]);
  const [repoPath, setRepoPath] = React.useState(restored?.repoPath || '');
  const [turns, setTurns] = React.useState(restored?.turns || []);
  const [traces, setTraces] = React.useState(restored?.traces || []);
  const [conversationID, setConversationID] = React.useState(restored?.id || randomID('conversation'));
  const [inspection, setInspection] = React.useState('');
  const [inspectionReady, setInspectionReady] = React.useState(false);
  const [question, setQuestion] = React.useState('');
  const [health, setHealth] = React.useState('检查中');
  const [busy, setBusy] = React.useState(true);
  const [error, setError] = React.useState('');
  const [activity, setActivity] = React.useState(null);
  const [clock, setClock] = React.useState(Date.now());
  const abortRef = React.useRef(null);
  const turnsRef = React.useRef(null);
  const selected = projects.find((project) => project.repoPath === repoPath);
  const projectLocked = turns.length > 0;

  const addTrace = React.useCallback((type, detail, timing = {}) => {
    setTraces((current) => appendTrace(current, { id: randomID('trace'), type, detail, at: new Date().toISOString(), ...timing }));
  }, []);

  const runStage = React.useCallback(async (type, detail, operation, totalStartedAt = Date.now()) => {
    const id = randomID('stage');
    const startedAt = Date.now();
    setActivity({ id, type, startedAt, totalStartedAt });
    try {
      const result = await operation();
      addTrace(type, detail, { durationMs: Date.now() - startedAt });
      return result;
    } finally {
      setActivity((current) => current?.id === id ? null : current);
    }
  }, [addTrace]);

  React.useEffect(() => {
    if (!activity) return undefined;
    setClock(Date.now());
    const timer = window.setInterval(() => setClock(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, [activity?.id]);

  React.useEffect(() => {
    let active = true;
    const controller = new AbortController();
    const totalStartedAt = Date.now();
    const healthStartedAt = Date.now();
    Promise.all([
      getBackendSrv().get(codeHealthURL).then(() => { if (active) { setHealth('MCP 可达'); addTrace('health', 'Code MCP HTTP 可达', { durationMs: Date.now() - healthStartedAt }); } }),
      initializeMCP(controller.signal, runStage, totalStartedAt).then((names) => runStage('catalog', `${names.length} 个工具，注册项目清单已返回`, () => callTool('list_projects', {}, controller.signal), totalStartedAt)),
    ]).then(([, markdown]) => {
      if (!active) return;
      const items = parseProjects(markdown);
      if (!items.length) throw new Error('代码 MCP 没有返回可选择的注册项目');
      const reconciled = reconcileConversationProject(restored, items);
      setProjects(items);
      setRepoPath(reconciled.repoPath);
      if (reconciled.reset) {
        setConversationID(randomID('conversation'));
        setTurns([]);
        setTraces([]);
        setInspection('');
        setInspectionReady(false);
      }
      setError('');
    }).catch((requestError) => {
      if (!active) return;
      setHealth('MCP 不可用');
      setError(requestError?.data?.error?.message || requestError.message || '无法连接代码 MCP');
    }).finally(() => { if (active) setBusy(false); });
    return () => { active = false; controller.abort(); };
  }, [addTrace, runStage]);

  React.useEffect(() => {
    const project = projects.find((item) => item.repoPath === repoPath);
    saveConversation({ id: conversationID, repoPath, projectName: project?.name || restored?.projectName || '', turns, traces });
  }, [conversationID, repoPath, projects, restored?.projectName, traces, turns]);

  React.useEffect(() => {
    const container = turnsRef.current;
    if (container) container.scrollTop = container.scrollHeight;
  }, [busy, turns]);

  async function inspectProject(signal) {
    if (!repoPath) throw new Error('请先选择代码项目');
    setInspectionReady(false);
    const report = await callTool('inspect_repository', { repo_path: repoPath }, signal);
    setInspection(report); setInspectionReady(true);
  }

  async function submit() {
    const current = question.trim();
    if (!repoPath || !current || busy) return;
    const controller = new AbortController();
    abortRef.current = controller;
    setBusy(true); setError('');
    const totalStartedAt = Date.now();
    try {
      if (!inspectionReady) await runStage('inspect', '项目目录、Agent 与 SKILL 检查完成', () => inspectProject(controller.signal), totalStartedAt);
      const requestQuestion = buildConversationQuestion(turns, current);
      const nextTurns = [...turns, { role: 'user', content: current, at: new Date().toISOString() }];
      setTurns(nextTurns); setQuestion('');
      const report = await runStage('analyze', turns.length ? '携带限长前文重新分析源码，完整 Markdown 报告已返回' : '首轮只读源码分析完成，完整 Markdown 报告已返回', () => callTool('analyze_codebase', { repo_path: repoPath, question: requestQuestion, mode: 'analyze' }, controller.signal), totalStartedAt);
      setTurns([...nextTurns, { role: 'assistant', content: report, at: new Date().toISOString() }].slice(-20));
    } catch (requestError) {
      const message = requestError?.name === 'AbortError' ? '本轮代码分析已取消' : (requestError.message || '代码分析失败');
      setError(message); addTrace('error', message);
    } finally {
      if (abortRef.current === controller) abortRef.current = null;
      setBusy(false);
    }
  }

  function newConversation() {
    abortRef.current?.abort();
    setConversationID(randomID('conversation'));
    setTurns([]); setTraces([]); setInspection(''); setInspectionReady(false); setQuestion(''); setError(''); setActivity(null);
    try { window.sessionStorage.removeItem(storageKey); } catch { /* optional */ }
  }

  const turnList = turns.length ? h('div', { className: 'code-turns', ref: turnsRef, 'aria-live': 'polite' }, turns.map((turn, index) =>
    h('article', { className: `code-turn ${turn.role}`, key: `${turn.at}-${index}` },
      h('div', { className: 'code-turn-head' },
        h('span', { className: 'code-turn-role' }, h('span', { className: 'code-avatar' }, turn.role === 'user' ? '你' : 'AI'), turn.role === 'user' ? '你的问题' : '代码解析报告'),
        h('time', null, new Date(turn.at).toLocaleTimeString('zh-CN', { hour12: false }))),
      h(MarkdownContent, { value: turn.content, maxLength: 64000, copyable: turn.role === 'assistant' })))) : h('div', { className: 'code-turns', ref: turnsRef },
        h('div', { className: 'code-welcome' },
          h('div', { className: 'code-welcome-mark', 'aria-hidden': 'true' }, '</>'),
          h('h3', null, '从一个具体代码问题开始'),
          h('p', null, '选择注册项目后，系统会先检查仓库边界，再调用只读 MCP 分析源码。回答支持 Markdown 排版，并在每轮追问时重新核对证据。'),
          h('div', { className: 'code-steps' },
            h('div', { className: 'code-step' }, h('b', null, '01 · 选择'), h('span', null, '锁定已注册代码项目')),
            h('div', { className: 'code-step' }, h('b', null, '02 · 分析'), h('span', null, '检查 Agent 与 SKILL')),
            h('div', { className: 'code-step' }, h('b', null, '03 · 追问'), h('span', null, '携带限长上下文继续核对'))),
          h('div', { className: 'code-examples' }, promptExamples.map((example) => h('button', { className: 'code-example', type: 'button', key: example, disabled: busy || !repoPath, onClick: () => setQuestion(example) }, example)))));

  return h('main', { className: 'code-page' }, h('style', null, css),
    h('header', { className: 'code-head' },
      h('div', { className: 'code-brand' }, h('div', { className: 'code-kicker' }, 'CODE ANALYSIS / MCP'), h('h1', { className: 'code-title' }, '代码解析对话'), h('div', { className: 'code-sub' }, '通过 Grafana 服务端代理连接只读 Code Analysis MCP。每轮使用项目 Agent 与 SKILL 核对源码；多轮上下文只用于理解指代，不替代本轮证据。')),
      h('div', { className: 'code-head-side' },
        h('div', { className: 'code-health' }, h('span', { className: 'code-health-dot', 'aria-hidden': 'true' }), health),
        h('div', { className: 'code-capabilities' }, h('span', { className: 'code-chip' }, '只读分析'), h('span', { className: 'code-chip' }, '多轮对话'), h('span', { className: 'code-chip' }, 'Markdown')))),
    h('div', { className: 'code-grid' },
      h('section', { className: 'code-panel code-boundary' },
        h('div', { className: 'code-panel-head' }, h('h2', null, '注册代码项目'), h('span', { className: 'code-panel-label' }, projectLocked ? '已锁定' : `${projects.length} 个项目`)),
        h('div', { className: 'code-field' }, h('label', { htmlFor: 'code-project' }, '当前 Code MCP 下的项目'),
          h('select', { id: 'code-project', className: 'code-select', value: repoPath, disabled: busy || projectLocked, onChange: (event) => { setRepoPath(event.target.value); setInspection(''); setInspectionReady(false); } },
            projects.length ? projects.map((project) => h('option', { key: project.id, value: project.repoPath }, project.name)) : h('option', { value: '' }, '正在读取项目清单…'))),
        selected ? h('div', { className: 'code-project-card' },
          h('div', { className: 'code-project-name' }, selected.name),
          h('div', { className: 'code-project-branch' }, selected.branch),
          h('div', { className: 'code-project-meta' },
            h('div', null, h('span', null, 'project_id'), h('strong', { title: selected.id }, selected.id)),
            selected.code ? h('div', null, h('span', null, 'code'), h('strong', { title: selected.code }, selected.code)) : null,
            h('div', null, h('span', null, 'Agent'), h('strong', { title: selected.agent }, selected.agent)),
            h('div', null, h('span', null, 'SKILL'), h('strong', { title: selected.skill }, selected.skill)),
            selected.svnUpdate ? h('div', null, h('span', null, '分析前 SVN 更新'), h('strong', null, selected.svnUpdate)) : null,
            h('div', null, h('span', null, 'Mode'), h('strong', null, 'read-only')))) : null,
        h('div', { className: 'code-note', style: { marginTop: '14px' } }, projectLocked ? '本会话已有消息，项目已锁定。新建会话后可切换项目。' : '这里列出当前 Code MCP 注册的代码项目，不是 MCP 服务列表；只能选择服务端返回的精确项目。'),
        inspection ? h('details', { className: 'code-inspection' }, h('summary', null, '查看仓库检查报告'), h('div', { className: 'code-inspection-body' }, h(RepositoryInspection, { value: inspection }))) : null),
      h('section', { className: 'code-panel code-chat' },
        h('div', { className: 'code-panel-head' }, h('h2', null, '分析会话'), h('span', { className: 'code-session' }, conversationID.slice(0, 8))), turnList,
        h('div', { className: 'code-composer' }, h('div', { className: 'code-field' }, h('label', { htmlFor: 'code-question' }, turns.length ? '继续追问' : '分析问题'),
          h('textarea', { id: 'code-question', className: 'code-textarea', value: question, disabled: busy || !repoPath, maxLength: 4000, placeholder: '例如：先确认 m27_h 是否存在，再分析指定 GM 命令的参数类型、调用链与风险。', onChange: (event) => setQuestion(event.target.value), onKeyDown: (event) => { if (event.ctrlKey && event.key === 'Enter') void submit(); } })),
        h('div', { className: 'code-actions' },
          h('button', { className: 'code-button', disabled: busy || !repoPath || !question.trim(), onClick: submit }, busy ? (activity ? `${traceLabel(activity.type)} ${formatDuration(clock - activity.startedAt)}` : '正在准备…') : (turns.length ? '发送追问' : '开始分析')),
          busy && abortRef.current ? h('button', { className: 'code-button danger', onClick: () => abortRef.current?.abort() }, '取消本轮') : null,
          h('button', { className: 'code-button secondary', disabled: busy, onClick: newConversation }, '新建会话'),
          h('span', { className: 'code-shortcut' }, 'Ctrl + Enter 发送')),
        activity ? h('div', { className: 'code-timing', role: 'status', 'aria-live': 'polite', style: { marginTop: '10px' } },
          h('span', { className: 'code-timing-dot', 'aria-hidden': true }),
          h('div', { className: 'code-timing-copy' }, h('b', null, `当前：${traceLabel(activity.type)} ${formatDuration(clock - activity.startedAt)}`), h('span', null, `本轮总计 ${formatDuration(clock - activity.totalStartedAt)}`))) : null,
        error ? h('div', { className: 'code-error', role: 'alert', style: { marginTop: '12px' } }, safeMarkdownText(error, 2000)) : null)),
      h('aside', { className: 'code-panel code-trace-panel' },
        h('div', { className: 'code-panel-head' }, h('h2', null, 'MCP 调用轨迹'), h('span', { className: 'code-panel-label' }, `${traces.length} 条`)),
        traces.length ? h('div', { className: 'code-traces' }, traces.map((trace) => h('div', { className: 'code-trace', key: trace.id }, h('div', { className: 'code-trace-head' }, h('strong', null, traceLabel(trace.type)), Number.isFinite(trace.durationMs) ? h('span', { className: 'code-trace-time' }, formatDuration(trace.durationMs)) : null), h('span', null, safeMarkdownText(trace.detail, 600))))) : h('div', { className: 'code-empty' }, '调用后显示项目清单、仓库检查和分析阶段。'),
        h('div', { className: 'code-note', style: { marginTop: '14px' } }, '当前能力只读：不修改代码、不构建、不部署，也不直接执行玩家 GM。需要操作时，应回到受审批保护的运维流程。'))));
}
