import { getBackendSrv, locationService } from '@grafana/runtime';
import React from 'react';

import {
  dashboardServer,
  latestPrometheusSampleMs,
  prometheusLabelValue,
  prometheusSnapshotRetryDelayMs,
  serverLastAttemptMs,
} from './control-logic.js';
import {
  activeAlertsFromRules,
  alertLabelText,
  cpuCapacityPercent,
  fixed,
  gibibytes,
  isMNodeInfrastructureNode,
  mergeNodeSamples,
} from './overview-logic.js';
import { prometheusSamples, safeMarkdownText } from './shared-logic.js';

const h = React.createElement;
const prometheusProxyURL = '/api/datasources/proxy/uid/prometheus/api/v1';
const collectProxyURL = '/api/plugin-proxy/erlang-monitor-controls-app/collect';
const statusProxyURL = '/api/plugin-proxy/erlang-monitor-controls-app/status';
const refreshIntervalMs = 30 * 60 * 1000;

const overviewCSS = `
.erlang-overview-page{--eo-surface:var(--background-secondary,#181b1f);--eo-soft:var(--background-canvas,#111217);--eo-border:var(--border-weak,#34373d);--eo-text:var(--text-primary,#d9d9d9);--eo-muted:var(--text-secondary,#9da2a8);--eo-green:#56a64b;--eo-blue:#5794f2;--eo-cyan:#45b8cc;--eo-orange:#f2a93b;--eo-red:#e24d42;max-width:1600px;margin:0 auto;padding:12px;color:var(--eo-text)}
.eo-toolbar,.eo-health,.eo-head,.eo-net,.eo-alert-summary{display:flex;align-items:center}.eo-toolbar{justify-content:space-between;gap:12px;flex-wrap:wrap;margin-bottom:8px}.eo-title{min-width:0}.eo-title h1{margin:0;font-size:20px;font-weight:600;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}.eo-title small{color:var(--eo-muted)}.eo-controls{display:flex;gap:6px;align-items:center;flex-wrap:wrap}.eo-control{min-height:32px;border:1px solid var(--eo-border);border-radius:4px;background:var(--eo-surface);color:var(--eo-text);padding:6px 9px;line-height:18px}.eo-control:disabled{opacity:.55}.eo-select{width:min(280px,55vw);height:32px;max-width:280px;color:var(--eo-text);background-color:var(--eo-surface)}.eo-select option{color:var(--eo-text);background:var(--eo-surface)}
.eo-health{justify-content:space-between;gap:12px;border:1px solid var(--eo-border);border-radius:6px;background:var(--eo-surface);padding:9px 11px;margin-bottom:8px}.eo-health-main{display:flex;align-items:center;gap:8px}.eo-health-icon{display:grid;place-items:center;width:28px;height:28px;border-radius:50%;font-weight:700}.eo-health-icon.ok{color:var(--eo-green);background:rgba(86,166,75,.14)}.eo-health-icon.warn{color:var(--eo-orange);background:rgba(242,169,59,.14)}.eo-health strong{display:block;font-weight:600}.eo-health small,.eo-time{color:var(--eo-muted)}.eo-error{border:1px solid rgba(226,77,66,.6);background:rgba(226,77,66,.12);color:#ff8b85;border-radius:6px;padding:9px 11px;margin-bottom:8px}
.eo-panel{min-width:0;border:1px solid var(--eo-border);border-radius:6px;background:var(--eo-surface);overflow:hidden;margin-bottom:8px}.eo-head{justify-content:space-between;gap:8px;min-height:35px;padding:7px 10px;border-bottom:1px solid var(--eo-border);font-weight:600}.eo-ok{color:var(--eo-green);white-space:nowrap}.eo-warn{color:var(--eo-orange);white-space:nowrap}
.eo-gauges{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:1px;background:var(--eo-border)}.eo-gauge{min-width:0;display:grid;grid-template-columns:auto minmax(0,1fr);align-items:center;gap:12px;padding:11px;background:var(--eo-surface)}.eo-ring{--value:0;--color:var(--eo-blue);width:88px;height:88px;display:grid;place-items:center;border-radius:50%;position:relative;background:conic-gradient(var(--color) calc(var(--value)*1%),var(--eo-border) 0)}.eo-ring:before{content:"";position:absolute;inset:8px;border-radius:50%;background:var(--eo-surface)}.eo-ring.memory{--color:var(--eo-cyan)}.eo-ring.disk{--color:var(--eo-green)}.eo-ring-value{position:relative;text-align:center;font-size:17px;font-weight:600;font-variant-numeric:tabular-nums}.eo-ring-value small{display:block;color:var(--eo-muted);font-size:11px;font-weight:400}.eo-gauge-title{font-weight:600;margin-bottom:6px}.eo-gauge-main,.eo-gauge-sub{font-variant-numeric:tabular-nums;white-space:nowrap}.eo-gauge-sub{margin-top:4px;color:var(--eo-muted)}
.eo-table-wrap{overflow-x:auto}.eo-table{width:100%;min-width:1000px;border-collapse:collapse;table-layout:fixed}.eo-table th,.eo-table td{padding:7px 8px;border-bottom:1px solid var(--eo-border);white-space:nowrap;overflow:hidden;text-overflow:ellipsis;text-align:left}.eo-table th{color:var(--eo-muted);font-weight:400}.eo-node{width:25%}.eo-status{width:8%}.eo-pending{width:8%;text-align:center!important;color:var(--eo-muted)}.eo-process{width:8%;text-align:right!important;font-variant-numeric:tabular-nums}.eo-resource{width:9%;text-align:right!important;font-variant-numeric:tabular-nums}.eo-connections{width:24%;white-space:normal!important}.eo-connection-list{display:grid;gap:3px}.eo-connection{min-width:0;display:grid;grid-template-columns:28px minmax(0,1fr);align-items:center;gap:6px;font:11px Bahnschrift,"Microsoft YaHei UI",sans-serif}.eo-connection b{padding:1px 3px;border-radius:2px;color:var(--eo-green);background:rgba(86,166,75,.14);text-align:center;font-size:9px}.eo-connection.region b{color:var(--eo-orange);background:rgba(242,169,59,.14)}.eo-connection.unusable,.eo-connection.unusable b{color:var(--eo-red)}.eo-connection.unusable b{background:rgba(226,77,66,.14)}.eo-connection span{overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.eo-connection-empty{color:var(--eo-muted);font-size:12px}.eo-node-status.up{color:var(--eo-green)}.eo-node-status.down{color:var(--eo-red)}.eo-node-status:before{content:"●";margin-right:5px}.eo-more td{padding:0;text-align:center;background:var(--eo-soft);color:var(--eo-muted);border-bottom:0}.eo-more-button{width:100%;min-height:36px;border:0;background:transparent;color:inherit;cursor:pointer;font:inherit;transition:background-color .15s ease,color .15s ease}.eo-more-button:hover{background:rgba(87,148,242,.08);color:var(--eo-text)}.eo-more-button:focus-visible{outline:2px solid var(--eo-blue);outline-offset:-2px}.eo-more-arrow{display:inline-block;margin-left:7px;color:var(--eo-blue);font-size:11px}
.eo-alert{border-top:1px solid var(--eo-border)}.eo-alert-summary{list-style:none;display:grid;grid-template-columns:auto minmax(0,1fr) auto auto auto;gap:9px;min-height:46px;padding:8px 10px;cursor:pointer}.eo-alert-summary::-webkit-details-marker{display:none}.eo-alert-icon{display:grid;place-items:center;width:28px;height:28px;border-radius:50%;color:var(--eo-orange);background:rgba(242,169,59,.14)}.eo-alert-title{min-width:0}.eo-alert-title strong,.eo-alert-title small{display:block;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.eo-alert-title strong{font-weight:600}.eo-alert-title small{color:var(--eo-muted)}.eo-severity{border-radius:99px;padding:3px 7px;color:var(--eo-orange);background:rgba(242,169,59,.14)}.eo-severity.critical{color:#ff8b85;background:rgba(226,77,66,.14)}.eo-alert-time{color:var(--eo-muted);white-space:nowrap}.eo-alert-body{border-top:1px solid var(--eo-border);background:var(--eo-soft)}.eo-alert-line{display:grid;grid-template-columns:96px minmax(0,1fr);gap:8px;padding:8px 12px;border-bottom:1px solid var(--eo-border)}.eo-alert-line:last-child{border-bottom:0}.eo-alert-label{color:var(--eo-muted);font-weight:600;white-space:nowrap}.eo-alert-label:before{content:"●";font-size:9px;color:var(--eo-orange);margin-right:7px}.eo-alert-value{overflow-wrap:anywhere;word-break:break-word}.eo-current{color:var(--eo-orange);font-weight:600}.eo-empty{padding:16px;text-align:center;color:var(--eo-muted)}.eo-agent{color:var(--eo-blue);text-decoration:none}
@media(max-width:760px){.eo-gauge{grid-template-columns:1fr;justify-items:center;text-align:center;padding:9px 4px}.eo-ring{width:76px;height:76px}.eo-alert-summary{grid-template-columns:auto minmax(0,1fr) auto}.eo-alert-time{grid-column:2/-1}}@media(max-width:480px){.erlang-overview-page{padding:8px}.eo-health{align-items:flex-start;flex-direction:column}.eo-ring{width:64px;height:64px}.eo-ring:before{inset:6px}.eo-ring-value{font-size:14px}.eo-gauge-main,.eo-gauge-sub{font-size:11px;white-space:normal}.eo-alert-line{grid-template-columns:1fr;gap:4px}}
`;

function scalar(samples) {
  return Array.isArray(samples) && samples.length ? samples[0].value : null;
}

function sampleTime(groups) {
  const values = groups.flat().map((sample) => sample.sampledAt).filter(Number.isFinite);
  return values.length ? Math.max(...values) : 0;
}

function formatDate(value) {
  const parsed = Date.parse(value);
  return Number.isFinite(parsed) ? new Date(parsed).toLocaleString('zh-CN', { hour12: false }) : '未知';
}

function formatNumber(value) {
  return Number.isFinite(value) ? Math.round(value).toLocaleString('zh-CN') : '待接口';
}

function formatResidentMemory(value) {
  const amount = gibibytes(value);
  return amount == null ? '无数据' : `${fixed(amount)} G`;
}

function formatCPURatio(value) {
  return Number.isFinite(value) ? `${fixed(value * 100)}%` : '无数据';
}

function metricQuery(expression) {
  return getBackendSrv().get(`${prometheusProxyURL}/query?query=${encodeURIComponent(expression)}`);
}

function wait(milliseconds) {
  return new Promise((resolve) => window.setTimeout(resolve, milliseconds));
}

async function waitFor(load, accept, timeoutMs) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    const value = await load().catch(() => null);
    if (value && accept(value)) return value;
    await wait(1000);
  }
  return null;
}

function MetricRing({ label, value, detail, subdetail, colorClass = '', ariaMax = 100, ariaValue = value, fill = value }) {
  const bounded = Math.max(0, Math.min(100, Number(fill) || 0));
  return h('article', { className: 'eo-gauge' },
    h('div', { className: `eo-ring ${colorClass}`, style: { '--value': bounded }, role: 'progressbar', 'aria-label': `${label} ${fixed(value)}，上限 ${fixed(ariaMax, 0)}`, 'aria-valuemin': 0, 'aria-valuemax': ariaMax || 100, 'aria-valuenow': Number(ariaValue) || 0 },
      h('div', { className: 'eo-ring-value' }, `${fixed(value)}%`, h('small', null, label))),
    h('div', null, h('div', { className: 'eo-gauge-title' }, label), h('div', { className: 'eo-gauge-main' }, detail), h('div', { className: 'eo-gauge-sub' }, subdetail)));
}

function NodeConnections({ sourceNode, available, connections = [] }) {
  if (isMNodeInfrastructureNode(sourceNode)) return h('span', { className: 'eo-connection-empty', title: '中央/赛区等非游戏节点不展示连接关系' }, '—');
  if (Number(available) !== 1) return h('span', { className: 'eo-connection-empty' }, '待采集');
  if (!connections.length) return h('span', { className: 'eo-connection-empty' }, '未连接');
  return h('div', { className: 'eo-connection-list' }, connections.map((connection) => {
    const kind = connection.type === 'central' ? 'C8' : 'C9';
    const shortName = String(connection.node || '').split('@')[0];
    return h('span', {
      className: `eo-connection ${connection.type === 'region' ? 'region' : ''} ${connection.usable ? '' : 'unusable'}`.trim(),
      key: `${connection.type}:${connection.nodeID}:${connection.node}`,
      title: `${connection.node || '未知节点'} · state=${connection.state ?? '未知'}`,
    }, h('b', null, kind), h('span', null, `${connection.nodeID}${shortName ? ` · ${shortName}` : ''}`));
  }));
}

function NodeTable({ nodes }) {
  const [expanded, setExpanded] = React.useState(false);
  const canExpand = nodes.length > 6;
  const visible = expanded ? nodes : nodes.slice(0, 6);
  const hiddenOnline = nodes.slice(6).filter((node) => node.up === 1).length;
  return h('section', { className: 'eo-panel' },
    h('div', { className: 'eo-head' }, h('span', null, 'Erlang 节点状态'), h('span', { className: 'eo-ok' }, `${nodes.filter((node) => node.up === 1).length} / ${nodes.length} 在线`)),
    h('div', { className: 'eo-table-wrap' },
      h('table', { className: 'eo-table' },
        h('thead', null, h('tr', null,
          h('th', { className: 'eo-node' }, '节点'), h('th', { className: 'eo-status' }, '状态'),
          h('th', { className: 'eo-process' }, '进程总数'), h('th', { className: 'eo-resource', title: 'BEAM进程常驻内存（RSS）' }, '内存（G）'),
          h('th', { className: 'eo-resource', title: 'BEAM进程CPU，100%约等于一个逻辑核' }, 'CPU比例'), h('th', { className: 'eo-connections' }, '节点连接'),
          h('th', { className: 'eo-pending' }, '注册人数'), h('th', { className: 'eo-pending' }, '在线人数'))),
        h('tbody', null,
          visible.map((node) => h('tr', { key: node.node },
            h('td', { className: 'eo-node' }, node.node),
            h('td', { className: 'eo-status' }, h('span', { className: `eo-node-status ${node.up === 1 ? 'up' : 'down'}` }, node.up === 1 ? '在线' : '离线')),
            h('td', { className: 'eo-process' }, formatNumber(node.processes)),
            h('td', { className: 'eo-resource' }, formatResidentMemory(node.residentMemoryBytes)),
            h('td', { className: 'eo-resource' }, formatCPURatio(node.cpuRatio)),
            h('td', { className: 'eo-connections' }, h(NodeConnections, { sourceNode: node.node, available: node.mnodeAvailable, connections: node.connections })),
            h('td', { className: 'eo-pending' }, formatNumber(node.registered)),
            h('td', { className: 'eo-pending' }, formatNumber(node.online)))),
          canExpand ? h('tr', { className: 'eo-more' }, h('td', { colSpan: 8 }, h('button', {
            className: 'eo-more-button', type: 'button', 'aria-expanded': expanded, onClick: () => setExpanded((value) => !value),
          }, expanded ? `收起节点列表 · 共 ${nodes.length} 个节点` : `其余 ${nodes.length - 6} 个节点：${hiddenOnline} 在线`, h('span', { className: 'eo-more-arrow', 'aria-hidden': true }, expanded ? '▲' : '▼')))) : null))));
}

function AlertList({ alerts, dashboardUID, server }) {
  const source = new URLSearchParams(window.location.search);
  const opsAgentBase = new URLSearchParams({ dashboard_uid: dashboardUID, server, from: source.get('from') || 'now-6h', to: source.get('to') || 'now' });
  return h('section', { className: 'eo-panel' },
    h('div', { className: 'eo-head' }, h('span', null, '异常详情'), h('span', { className: alerts.length ? 'eo-warn' : 'eo-ok' }, alerts.length ? `${alerts.length} 条告警` : '当前无异常')),
    alerts.length ? alerts.map((alert, index) => {
      const node = alert.labels.node || '';
      const params = new URLSearchParams(opsAgentBase);
      if (node) params.set('node', node);
      return h('details', { className: 'eo-alert', key: alert.fingerprint, open: index === 0 },
        h('summary', { className: 'eo-alert-summary' },
          h('span', { className: 'eo-alert-icon' }, '!'),
          h('span', { className: 'eo-alert-title' }, h('strong', null, alert.annotations.summary || alert.labels.alertname || '监控告警'), h('small', null, [node, alert.labels.registered_name].filter(Boolean).join(' · ') || alert.labels.name)),
          h('span', { className: `eo-severity ${alert.labels.severity || ''}` }, alert.labels.severity || alert.state),
          h('span', { className: 'eo-alert-time' }, formatDate(alert.activeAt)), h('span', null, '⌄')),
        h('div', { className: 'eo-alert-body' },
          h('div', { className: 'eo-alert-line' }, h('div', { className: 'eo-alert-label' }, '当前值'), h('div', { className: 'eo-alert-value eo-current' }, alert.annotations.value || fixed(alert.value))),
          h('div', { className: 'eo-alert-line' }, h('div', { className: 'eo-alert-label' }, '触发条件'), h('div', { className: 'eo-alert-value' }, alert.annotations.condition || '由 Prometheus 告警规则触发')),
          h('div', { className: 'eo-alert-line' }, h('div', { className: 'eo-alert-label' }, '影响'), h('div', { className: 'eo-alert-value' }, alert.annotations.impact || '请结合节点状态判断影响范围')),
          h('div', { className: 'eo-alert-line' }, h('div', { className: 'eo-alert-label' }, '建议处理'), h('div', { className: 'eo-alert-value' }, alert.annotations.action || '进入运维 Agent 分析或按运维流程处理')),
          h('div', { className: 'eo-alert-line' }, h('div', { className: 'eo-alert-label' }, '标签'), h('div', { className: 'eo-alert-value' }, alertLabelText(alert.labels))),
          h('div', { className: 'eo-alert-line' }, h('div', { className: 'eo-alert-label' }, '辅助分析'), h('a', { className: 'eo-agent', href: `/a/erlang-monitor-controls-app/ops-agent?${params.toString()}` }, '打开运维 Agent'))));
    }) : h('div', { className: 'eo-empty' }, '当前没有 firing 或 pending 告警'));
}

export function OverviewPage() {
  const initial = new URLSearchParams(window.location.search);
  const initialUID = initial.get('dashboard_uid') || '';
  const [context, setContext] = React.useState({ uid: initialUID, title: '', server: initial.get('server') || '', tag: '' });
  const [dashboards, setDashboards] = React.useState([]);
  const [snapshot, setSnapshot] = React.useState({ nodes: [], alerts: [] });
  const [error, setError] = React.useState('');
  const [busy, setBusy] = React.useState(false);

  React.useEffect(() => {
    if (!initialUID) {
      setError('缺少 dashboard_uid，无法确定服务器页面');
      return undefined;
    }
    let active = true;
    getBackendSrv().get(`/api/dashboards/uid/${encodeURIComponent(initialUID)}`).then((payload) => {
      if (!active) return;
      const tags = Array.isArray(payload?.dashboard?.tags) ? payload.dashboard.tags : [];
      setContext({ uid: initialUID, title: String(payload?.dashboard?.title || initialUID), server: dashboardServer(payload), tag: tags.find((tag) => /^qt-/i.test(tag)) || '' });
    }).catch(() => { if (active) setError('无法读取服务器仪表板上下文'); });
    return () => { active = false; };
  }, [initialUID]);

  const loadSnapshot = React.useCallback(async () => {
    if (!context.server) return;
    const label = prometheusLabelValue(context.server);
    const expressions = [
      `erlang_exporter_node_up{name="${label}"}`,
      `erlang_game_registered_users{name="${label}"}`,
      `erlang_game_online_users{name="${label}"}`,
      `erlang_vm_process_count{name="${label}"}`,
      `erlang_host_cpu_usage_ratio{name="${label}"}`,
      `erlang_host_cpu_logical_cores{name="${label}"}`,
      `erlang_host_cpu_usage_cores_percent{name="${label}"}`,
      `erlang_host_memory_total_bytes{name="${label}"}`,
      `erlang_host_memory_available_bytes{name="${label}"}`,
      `erlang_host_filesystem_size_bytes{name="${label}"}`,
      `erlang_host_filesystem_available_bytes{name="${label}"}`,
      `erlang_host_network_receive_bytes_per_second{name="${label}"}`,
      `erlang_host_network_transmit_bytes_per_second{name="${label}"}`,
      `erlang_exporter_last_success_timestamp_seconds{name="${label}"}`,
	  `erlang_beam_resident_memory_bytes{name="${label}"}`,
	  `erlang_vm_cpu_usage_ratio{name="${label}"}`,
      `erlang_mnode_connections_available{name="${label}"}`,
      `erlang_mnode_connection_state{name="${label}"}`,
    ];
    for (let attempt = 0; ; attempt += 1) {
      try {
        const [payloads, rules] = await Promise.all([
          Promise.all(expressions.map(metricQuery)),
          getBackendSrv().get(`${prometheusProxyURL}/rules?type=alert`),
        ]);
        const samples = payloads.map(prometheusSamples);
        setSnapshot({
		  nodes: mergeNodeSamples(samples[0], samples[1], samples[2], samples[3], samples[14], samples[15], samples[16], samples[17]),
          cpuRatio: scalar(samples[4]), cpuLogical: scalar(samples[5]), cpuCorePercent: scalar(samples[6]),
          memoryTotal: scalar(samples[7]), memoryAvailable: scalar(samples[8]),
          filesystemSize: scalar(samples[9]), filesystemAvailable: scalar(samples[10]),
          networkReceive: scalar(samples[11]), networkTransmit: scalar(samples[12]), lastSuccess: scalar(samples[13]),
          alerts: activeAlertsFromRules(rules, context.server), sampledAt: sampleTime(samples), expressions,
        });
        setError('');
        return;
      } catch (requestError) {
        const retryDelay = prometheusSnapshotRetryDelayMs(requestError?.status, attempt);
        if (retryDelay == null) {
          setError(requestError?.data?.error || '无法读取 Prometheus 监控快照');
          return;
        }
        await wait(retryDelay);
      }
    }
  }, [context.server]);

  React.useEffect(() => {
    if (!context.server) return undefined;
    void loadSnapshot();
    const interval = window.setInterval(() => void loadSnapshot(), refreshIntervalMs);
    return () => window.clearInterval(interval);
  }, [context.server, loadSnapshot]);

  React.useEffect(() => {
    if (!context.tag) return undefined;
    let active = true;
    getBackendSrv().get(`/api/search?type=dash-db&tag=${encodeURIComponent(context.tag)}&limit=100`).then((items) => {
      if (active) setDashboards((Array.isArray(items) ? items : []).filter((item) => item.uid).sort((a, b) => String(a.title).localeCompare(String(b.title))));
    }).catch(() => {});
    return () => { active = false; };
  }, [context.tag]);

  async function collectNow() {
    if (!context.server || busy) return;
    setBusy(true);
    setError('');
    try {
      const requestedAt = Date.now();
      const before = await getBackendSrv().get(statusProxyURL).catch(() => null);
      const response = await getBackendSrv().post(collectProxyURL, { server: context.server });
      const serverID = String(response?.server || '');
      const baseline = serverLastAttemptMs(before, serverID) || requestedAt;
      const status = await waitFor(() => getBackendSrv().get(statusProxyURL), (value) => serverLastAttemptMs(value, serverID) > baseline, 120000);
      if (!status) throw new Error('等待服务器采集完成超时');
      const completedAt = serverLastAttemptMs(status, serverID);
      const label = prometheusLabelValue(context.server);
      const scraped = await waitFor(() => metricQuery(`timestamp(erlang_exporter_server_up{name="${label}"})`), (value) => latestPrometheusSampleMs(value) >= completedAt, 75000);
      if (!scraped) throw new Error('等待 Prometheus 抓取最新数据超时');
      await loadSnapshot();
    } catch (requestError) {
      setError(requestError?.message || requestError?.data?.error || '手动采集失败');
    } finally {
      setBusy(false);
    }
  }

  const totalG = gibibytes(snapshot.memoryTotal);
  const availableG = gibibytes(snapshot.memoryAvailable);
  const usedG = totalG != null && availableG != null ? totalG - availableG : null;
  const memoryPercent = totalG > 0 ? usedG / totalG * 100 : null;
  const diskTotalG = gibibytes(snapshot.filesystemSize);
  const diskAvailableG = gibibytes(snapshot.filesystemAvailable);
  const diskUsedG = diskTotalG != null && diskAvailableG != null ? diskTotalG - diskAvailableG : null;
  const diskPercent = diskTotalG > 0 ? diskUsedG / diskTotalG * 100 : null;
  const cpuCapacity = cpuCapacityPercent(snapshot.cpuLogical);
  const cpuCurrent = snapshot.cpuCorePercent ?? (snapshot.cpuRatio != null && snapshot.cpuLogical != null ? snapshot.cpuRatio * snapshot.cpuLogical * 100 : null);
  const cpuFill = cpuCapacity > 0 && cpuCurrent != null ? cpuCurrent / cpuCapacity * 100 : null;
  const alertCount = snapshot.alerts.length;

  return h('main', { className: 'erlang-overview-page' },
    h('style', null, overviewCSS),
    h('div', { className: 'eo-toolbar' },
      h('div', { className: 'eo-title' }, h('h1', null, context.title || 'Erlang 运行总览'), h('small', null, context.server || '正在解析服务器')),
      h('div', { className: 'eo-controls' },
        dashboards.length ? h('select', { className: 'eo-control eo-select', value: context.uid, 'aria-label': '目录-页面', onChange: (event) => locationService.push(`/a/erlang-monitor-controls-app/overview?dashboard_uid=${encodeURIComponent(event.target.value)}&kiosk`) }, dashboards.map((item) => h('option', { key: item.uid, value: item.uid }, item.title))) : null,
        h('button', { className: 'eo-control', type: 'button', disabled: busy || !context.server, onClick: collectNow }, busy ? '正在采集…' : '刷新采集'),
        h('span', { className: 'eo-control' }, '自动 30m'))),
    error ? h('div', { className: 'eo-error', role: 'alert' }, safeMarkdownText(error, 1000)) : null,
    h('section', { className: 'eo-health' },
      h('div', { className: 'eo-health-main' }, h('span', { className: `eo-health-icon ${alertCount ? 'warn' : 'ok'}` }, alertCount ? '!' : '✓'), h('div', null, h('strong', null, alertCount ? `需要关注 · ${alertCount} 条告警` : '运行正常'), h('small', null, `${snapshot.nodes.filter((node) => node.up === 1).length}/${snapshot.nodes.length} 节点在线`))),
      h('div', { className: 'eo-time' }, '最近采集 ', h('strong', null, snapshot.lastSuccess ? new Date(snapshot.lastSuccess * 1000).toLocaleString('zh-CN', { hour12: false }) : '无数据'))),
    h('section', { className: 'eo-panel' }, h('div', { className: 'eo-head' }, h('span', null, '资源水位'), h('span', { className: 'eo-ok' }, '主机指标')),
      h('div', { className: 'eo-gauges' },
        h(MetricRing, { label: 'CPU', value: cpuCurrent, detail: `上限 ${fixed(cpuCapacity, 0)}%`, subdetail: `${fixed(snapshot.cpuLogical, 0)} 逻辑核`, ariaMax: cpuCapacity, ariaValue: cpuCurrent, fill: cpuFill }),
        h(MetricRing, { label: '内存', value: memoryPercent, detail: `${fixed(usedG)} / ${fixed(totalG)} G`, subdetail: `可用 ${fixed(availableG)} G`, colorClass: 'memory' }),
        h(MetricRing, { label: '硬盘', value: diskPercent, detail: `${fixed(diskUsedG)} / ${fixed(diskTotalG)} G`, subdetail: `可用 ${fixed(diskAvailableG)} G`, colorClass: 'disk' }))),
    h(NodeTable, { key: context.server, nodes: snapshot.nodes }),
    h(AlertList, { alerts: snapshot.alerts, dashboardUID: context.uid, server: context.server }));
}
