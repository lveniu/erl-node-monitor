import { getBackendSrv } from '@grafana/runtime';
import React from 'react';

import { preferredServer, serverOptions, skillSummaries, withTaskID } from './ops-logic.js';
import { safeMarkdownText } from './shared-logic.js';

const h = React.createElement;
const opsProxyURL = '/api/plugin-proxy/erlang-monitor-controls-app/ops-agent';
const opsAdminProxyURL = '/api/plugin-proxy/erlang-monitor-controls-app/ops-agent-admin';

function proxyURL(base, path, params = {}) {
  const query = new URLSearchParams(params);
  query.set('_path', path);
  return `${base}?${query.toString()}`;
}

function persistTaskID(id) {
  window.history.replaceState({}, '', withTaskID(window.location.href, id));
}

function requestID() {
  return typeof crypto?.randomUUID === 'function' ? crypto.randomUUID() : `ops-${Date.now()}-${Math.random().toString(16).slice(2)}`;
}

const css = `
.ops-page{--oa-ink:#dce7e5;--oa-muted:#8fa4a2;--oa-line:#254241;--oa-panel:#0d1c1c;--oa-panel2:#102625;--oa-cyan:#65e0ce;--oa-amber:#f0b35b;--oa-red:#f07b73;max-width:1500px;margin:0 auto;padding:22px;color:var(--oa-ink);font-family:ui-monospace,SFMono-Regular,Consolas,monospace;background:radial-gradient(circle at 85% 4%,rgba(45,121,111,.22),transparent 34%),linear-gradient(135deg,#091313,#0b1918 55%,#07100f);min-height:calc(100vh - 40px)}
.ops-head{display:flex;justify-content:space-between;gap:20px;align-items:flex-start;border-bottom:1px solid var(--oa-line);padding-bottom:18px;margin-bottom:18px}.ops-kicker{color:var(--oa-cyan);font-size:11px;letter-spacing:.18em;text-transform:uppercase}.ops-title{font-family:Georgia,serif;font-size:34px;line-height:1.05;margin:5px 0 8px;letter-spacing:-.04em}.ops-sub{color:var(--oa-muted);font-size:13px;max-width:680px;line-height:1.6}.ops-chip{border:1px solid var(--oa-line);color:var(--oa-cyan);padding:7px 10px;font-size:11px;white-space:nowrap}.ops-grid{display:grid;grid-template-columns:minmax(250px,.82fr) minmax(340px,1.35fr) minmax(250px,.9fr);gap:14px;align-items:start}.ops-stack{display:grid;gap:14px;min-width:0}.ops-panel{border:1px solid var(--oa-line);background:rgba(13,28,28,.9);box-shadow:0 18px 45px rgba(0,0,0,.18);padding:16px}.ops-panel h2{font-family:Georgia,serif;font-size:20px;font-weight:400;margin:0 0 12px}.ops-meta{display:grid;gap:10px}.ops-meta>div{border-top:1px solid rgba(37,66,65,.72);padding-top:8px}.ops-meta span,.ops-field-label{display:block;color:var(--oa-muted);font-size:10px;text-transform:uppercase;letter-spacing:.13em;margin-bottom:5px}.ops-meta strong{font-family:ui-monospace,monospace;font-weight:500;overflow-wrap:anywhere}.ops-select,.ops-input,.ops-textarea{width:100%;box-sizing:border-box;background:#081313;border:1px solid var(--oa-line);color:var(--oa-ink);padding:10px;font:inherit;line-height:1.5}.ops-select{cursor:pointer}.ops-select:disabled{cursor:not-allowed;opacity:.65}.ops-textarea{min-height:120px;resize:vertical}.ops-button{border:1px solid var(--oa-cyan);background:var(--oa-cyan);color:#08211e;padding:9px 13px;font:inherit;font-weight:700;cursor:pointer}.ops-button:disabled{opacity:.45;cursor:not-allowed}.ops-button.secondary{background:transparent;color:var(--oa-cyan)}.ops-button.danger{border-color:var(--oa-red);background:transparent;color:var(--oa-red)}.ops-actions{display:flex;gap:8px;flex-wrap:wrap;margin-top:12px}.ops-notice{border-left:3px solid var(--oa-amber);background:rgba(240,179,91,.08);padding:10px;color:#f4cf95;font-size:12px;line-height:1.5}.ops-error{border-left:3px solid var(--oa-red);background:rgba(240,123,115,.08);padding:10px;color:#ffc1bc;font-size:12px;line-height:1.5}.ops-skill-count{color:var(--oa-muted);font-size:11px;margin:-5px 0 11px}.ops-skill-list{display:grid;gap:9px}.ops-skill{position:relative;border:1px solid rgba(37,66,65,.85);background:linear-gradient(135deg,rgba(16,38,37,.9),rgba(8,19,19,.94));padding:11px 11px 11px 14px;overflow:hidden}.ops-skill:before{content:'';position:absolute;inset:0 auto 0 0;width:2px;background:var(--oa-cyan)}.ops-skill code{color:var(--oa-cyan);font-size:12px;overflow-wrap:anywhere}.ops-skill p{color:#adc1be;font-size:12px;line-height:1.55;margin:7px 0 0}.ops-timeline{display:grid;gap:8px;max-height:620px;overflow:auto}.ops-event{border:1px solid rgba(37,66,65,.8);background:rgba(16,38,37,.72);padding:10px}.ops-event-head{display:flex;justify-content:space-between;gap:10px;color:var(--oa-cyan);font-size:11px}.ops-event pre{white-space:pre-wrap;overflow-wrap:anywhere;color:#bad0cc;font-size:12px;line-height:1.5;margin:8px 0 0}.ops-approval{border:1px solid var(--oa-amber);background:rgba(240,179,91,.1);padding:14px;margin-top:12px}.ops-approval code{display:block;background:#07100f;color:#f7d49b;padding:10px;white-space:pre-wrap;overflow-wrap:anywhere;margin:8px 0;font-size:12px}.ops-answer{white-space:pre-wrap;line-height:1.65;color:#d7e7e3}.ops-empty{color:var(--oa-muted);font-size:13px}.ops-page button:focus,.ops-page textarea:focus,.ops-page select:focus{outline:2px solid var(--oa-cyan);outline-offset:2px}@media(max-width:1120px){.ops-grid{grid-template-columns:minmax(240px,.8fr) minmax(340px,1.2fr)}.ops-skills-panel{grid-column:1/-1}}@media(max-width:720px){.ops-page{padding:14px}.ops-grid{grid-template-columns:1fr}.ops-skills-panel{grid-column:auto}.ops-title{font-size:28px}.ops-head{display:block}.ops-chip{display:inline-block;margin-top:12px}}
`;

function statusText(status) {
  return { running: '分析中', awaiting_approval: '等待审批', completed: '已完成', failed: '失败' }[status] || '未开始';
}

function eventText(event) {
  const data = event?.data || {};
  if (event.type === 'model_started') return `第 ${data.step || '?'} 步：正在请求 ${data.model || '模型'}，等待响应…`;
  if (event.type === 'model_finished') return `第 ${data.step || '?'} 步：模型调用${data.status === 'success' ? '完成' : '失败'}${Number.isFinite(data.duration_ms) ? `，耗时 ${data.duration_ms} ms` : ''}`;
  if (event.type === 'assistant_message') return data.content || '';
  if (event.type === 'approval_required') return `目标：${data.target}\n原因：${data.reason}\n命令：${data.command}`;
  if (event.type === 'tool_finished') return JSON.stringify(data.result || data, null, 2);
  if (event.type === 'task_failed') return data.error || 'Agent 失败';
  if (event.type === 'task_completed') return '任务完成';
  return JSON.stringify(data, null, 2);
}

export function OpsPage() {
  const query = new URLSearchParams(window.location.search);
  const initialTaskID = query.get('task_id') || '';
  const serverName = query.get('server') || '';
  const [node, setNode] = React.useState(query.get('node') || '');
  const [server, setServer] = React.useState({ id: '', name: '' });
  const [servers, setServers] = React.useState([]);
  const [skills, setSkills] = React.useState([]);
  const [catalogLoading, setCatalogLoading] = React.useState(true);
  const [question, setQuestion] = React.useState('分析当前服务器问题，基于已有的 SKILL 推荐解决方案，必要时按对应 SKILL 执行受控命令并验证结果。');
  const [task, setTask] = React.useState(null);
  const [error, setError] = React.useState('');
  const [busy, setBusy] = React.useState(false);

  const loadTask = React.useCallback(async (id) => {
    const value = await getBackendSrv().get(proxyURL(opsProxyURL, `/tasks/${encodeURIComponent(id)}`));
    setTask(value);
    return value;
  }, []);

  React.useEffect(() => {
    if (!initialTaskID) return undefined;
    let active = true;
    void loadTask(initialTaskID).catch((requestError) => {
      if (!active) return;
      persistTaskID('');
      setError(requestError?.data?.error?.message || '无法恢复上次运维任务');
    });
    return () => { active = false; };
  }, [initialTaskID, loadTask]);

  React.useEffect(() => {
    let active = true;
    Promise.all([
      getBackendSrv().get(proxyURL(opsProxyURL, '/servers')),
      getBackendSrv().get(proxyURL(opsProxyURL, '/skills')),
    ]).then(([serverPayload, skillPayload]) => {
      if (!active) return;
      const availableServers = serverOptions(serverPayload);
      const selected = preferredServer(availableServers, serverName);
      setServers(availableServers);
      setSkills(skillSummaries(skillPayload));
      setServer(selected || { id: '', name: '' });
      if (!selected) setNode('');
      setError('');
    }).catch((requestError) => {
      if (active) setError(requestError?.data?.error?.message || '无法读取内网节点和 Skill 清单');
    }).finally(() => { if (active) setCatalogLoading(false); });
    return () => { active = false; };
  }, [serverName]);

  function selectServer(event) {
    const selected = servers.find((item) => item.id === event.target.value);
    setServer(selected || { id: '', name: '' });
    setNode('');
  }

  React.useEffect(() => {
    if (!task?.id || ['completed', 'failed'].includes(task.status)) return undefined;
    const timer = window.setInterval(() => { void loadTask(task.id).catch(() => {}); }, 1000);
    const source = new EventSource(proxyURL(opsProxyURL, `/tasks/${encodeURIComponent(task.id)}/events`));
    const refresh = () => { void loadTask(task.id).catch(() => {}); };
    ['task_started', 'model_started', 'model_finished', 'assistant_message', 'approval_required', 'approval_decided', 'tool_started', 'tool_finished', 'task_completed', 'task_failed'].forEach((name) => source.addEventListener(name, refresh));
    return () => { window.clearInterval(timer); source.close(); };
  }, [task?.id, task?.status, loadTask]);

  async function start() {
    if (!server.id || !question.trim()) return;
    setBusy(true); setError(''); setTask(null);
    try {
      const value = await getBackendSrv().post(proxyURL(opsProxyURL, '/tasks'), { request_id: requestID(), question: question.trim(), context: { server_id: server.id, server_name: server.name, node, dashboard_uid: query.get('dashboard_uid') || '', from: query.get('from') || 'now-1h', to: query.get('to') || 'now', alert_labels: {} } });
      persistTaskID(value.id);
      setTask(value);
    } catch (requestError) { setError(requestError?.data?.error?.message || '无法创建运维任务'); }
    finally { setBusy(false); }
  }

  async function decide(approved) {
    if (!task?.pending_command) return;
    setBusy(true); setError('');
    try { const value = await getBackendSrv().post(proxyURL(opsAdminProxyURL, `/tasks/${encodeURIComponent(task.id)}/decision`), { request_id: requestID(), call_id: task.pending_command.call_id, approved }); setTask(value); }
    catch (requestError) { setError(requestError?.data?.error?.message || '审批失败'); }
    finally { setBusy(false); }
  }

  const pending = task?.pending_command;
  const approval = pending ? h('div', { className: 'ops-approval' },
    h('strong', null, '需要 Admin 批准 Shell'),
    h('div', { style: { color: '#f4cf95', fontSize: '12px', marginTop: '6px' } }, pending.target, ' · ', pending.reason),
    h('code', null, pending.command),
    h('div', { className: 'ops-actions' },
      h('button', { className: 'ops-button', disabled: busy, onClick: () => decide(true) }, '批准执行'),
      h('button', { className: 'ops-button danger', disabled: busy, onClick: () => decide(false) }, '拒绝'))) : null;
  const timeline = task?.events?.length ? h('div', { className: 'ops-timeline' }, task.events.map((event) =>
    h('article', { className: 'ops-event', key: event.id },
      h('div', { className: 'ops-event-head' }, h('span', null, event.type), h('time', null, new Date(event.at).toLocaleTimeString('zh-CN', { hour12: false }))),
      h('pre', null, safeMarkdownText(eventText(event), 16000))))) : h('div', { className: 'ops-empty' }, '任务启动后，这里会显示 Skill、Shell 和验证步骤。');
  return h('main', { className: 'ops-page' }, h('style', null, css),
    h('header', { className: 'ops-head' },
      h('div', null, h('div', { className: 'ops-kicker' }, 'ERLANG / OPERATIONS AGENT'), h('h1', { className: 'ops-title' }, '一次任务，完成分析与处理'), h('div', { className: 'ops-sub' }, '模型只负责判断和提出动作；服务器边界、Skill、Shell 审批和结果验证由平台控制。')),
      h('div', { className: 'ops-chip' }, task ? statusText(task.status) : '待命')),
    h('div', { className: 'ops-grid' },
      h('section', { className: 'ops-panel' },
        h('h2', null, '目标上下文'),
        h('div', { className: 'ops-meta' },
          h('div', null,
            h('label', { className: 'ops-field-label', htmlFor: 'ops-server-select' }, '内网节点（单选）'),
            h('select', { id: 'ops-server-select', className: 'ops-select', value: server.id, disabled: Boolean(task) || catalogLoading, onChange: selectServer },
              h('option', { value: '' }, catalogLoading ? '正在读取节点清单…' : '请选择一个内网节点'),
              servers.map((item) => h('option', { key: item.id, value: item.id }, `${item.name} · ${item.id}`)))),
          h('div', null, h('span', null, 'Stable ID'), h('strong', null, server.id || '—')),
          h('div', null, h('span', null, 'Node'), h('strong', null, node || '未固定')),
          h('div', null, h('span', null, 'Time window'), h('strong', null, `${query.get('from') || 'now-1h'} → ${query.get('to') || 'now'}`))),
        h('div', { className: 'ops-notice', style: { marginTop: '16px' } }, '只允许在已选 192.168.100.* 内网节点上按已加载 Skill 的职责执行。纯 ls / grep / ps / cd / head / tail / df / find 只读组合自动执行；其他允许的 Shell 逐条等待 Grafana Admin 审批。Agent 不保存长期记忆。')),
      h('div', { className: 'ops-stack' },
        h('section', { className: 'ops-panel' },
          h('h2', null, '执行轨迹'), timeline,
          task?.final_answer ? h('div', { style: { marginTop: '18px' } }, h('h2', null, '最终结果'), h('div', { className: 'ops-answer' }, safeMarkdownText(task.final_answer, 32000))) : null),
        h('section', { className: 'ops-panel' },
          h('h2', null, '任务输入'),
          h('textarea', { className: 'ops-textarea', value: question, disabled: Boolean(task), onChange: (event) => setQuestion(event.target.value), maxLength: 8000 }),
          h('div', { className: 'ops-actions' },
            !task ? h('button', { className: 'ops-button', disabled: busy || !server.id, onClick: start }, busy ? '正在启动…' : '开始运维任务') : null,
            task && ['completed', 'failed'].includes(task.status) ? h('button', { className: 'ops-button secondary', onClick: () => { persistTaskID(''); setTask(null); setError(''); } }, '新建任务') : null),
          error ? h('div', { className: 'ops-error', style: { marginTop: '12px' } }, safeMarkdownText(error, 1000)) : null,
          approval)),
      h('aside', { className: 'ops-panel ops-skills-panel' },
        h('h2', null, '可用 Skill'),
        h('div', { className: 'ops-skill-count' }, catalogLoading ? '正在读取…' : `${skills.length} 个专项流程`),
        skills.length ? h('div', { className: 'ops-skill-list' }, skills.map((skill) =>
          h('article', { className: 'ops-skill', key: skill.name },
            h('code', null, skill.name),
            h('p', null, safeMarkdownText(skill.description, 1000))))) : h('div', { className: 'ops-empty' }, catalogLoading ? '正在加载 Skill 清单。' : '当前没有可用 Skill。'))));
}
