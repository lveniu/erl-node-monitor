System.register(['@grafana/data', '@grafana/runtime', 'react'], (function (exports) {
  'use strict';
  var AppPlugin, getBackendSrv, locationService, config, React;
  return {
    setters: [function (module) {
      AppPlugin = module.AppPlugin;
    }, function (module) {
      getBackendSrv = module.getBackendSrv;
      locationService = module.locationService;
      config = module.config;
    }, function (module) {
      React = module.default;
    }],
    execute: (function () {

      const refreshCooldownMs = 10_000;
      const homePresentationCSS = '[data-erlang-monitor-home-hidden="true"]{display:none!important}'
        + '[data-erlang-monitor-home-layout="true"]{height:auto!important;width:auto!important}'
        + '[data-erlang-monitor-home-dashboard="true"]{position:relative!important;left:auto!important;top:auto!important;transform:none!important;width:auto!important}'
        + '[data-erlang-monitor-home-dashboard="true"] section{width:100%!important}';
      const prometheusSnapshotRetryDelaysMs = [500, 1_500, 3_000];

      function dashboardUID(pathname) {
        const match = /^\/d\/([^/]+)/.exec(pathname || '');
        return match ? decodeURIComponent(match[1]) : '';
      }

      function startupDashboardUID(search) {
        const uid = new URLSearchParams(search || '').get('erlang-monitor-dashboard') || '';
        return /^[A-Za-z0-9_-]+$/.test(uid) ? uid : '';
      }

      function isAnonymousGrafanaStatus(status) {
        return Number(status) === 401;
      }

      function isAnonymousGrafanaUser(user) {
        return user?.isSignedIn === false;
      }

      function prometheusSnapshotRetryDelayMs(status, attempt) {
        const code = Number(status);
        const transient = !Number.isFinite(code) || code === 0 || code === 401 || code === 408 || code === 429 || code >= 500;
        return transient ? prometheusSnapshotRetryDelaysMs[attempt] ?? null : null;
      }

      function shouldHideGuestNavigation(href) {
        try {
          const target = new URL(String(href || ''), 'http://grafana.local');
          return target.pathname !== '/dashboards' || target.search !== '';
        } catch {
          return true;
        }
      }

      function shouldHideUnusedNavigation(href) {
        try {
          const pathname = new URL(String(href || ''), 'http://grafana.local').pathname;
          return pathname === '/drilldown' || pathname.startsWith('/drilldown/');
        } catch {
          return false;
        }
      }

      function syncBooleanAttribute(element, name, active) {
        if (!element || !name) {
          return false;
        }
        const value = element.getAttribute(name);
        if (active) {
          if (value === 'true') {
            return false;
          }
          element.setAttribute(name, 'true');
          return true;
        }
        if (value == null) {
          return false;
        }
        element.removeAttribute(name);
        return true;
      }

      function homeGridPresentationTargets(dashboardGridItem, boundary = null) {
        const hiddenGridItems = [];
        const gridLayout = dashboardGridItem?.parentElement;
        if (gridLayout?.matches?.('.react-grid-layout')) {
          for (const item of gridLayout.children || []) {
            if (item !== dashboardGridItem && item.matches?.('.react-grid-item')) {
              hiddenGridItems.push(item);
            }
          }
        }

        const homeLayouts = [];
        for (let parent = dashboardGridItem?.parentElement; parent && parent !== boundary; parent = parent.parentElement) {
          if (parent.matches?.('.react-grid-layout')) {
            homeLayouts.push(parent);
          }
        }
        return { hiddenGridItems, homeLayouts };
      }

      function isOverviewDashboardUID(uid) {
        return /^erlang-(?:monitor|qt05|qt07)-[A-Za-z0-9_-]+$/.test(String(uid || ''));
      }

      function shouldPollControlState(pathname) {
        return isOverviewDashboardUID(dashboardUID(pathname));
      }

      function overviewEntryURL(pathname, search) {
        const source = new URLSearchParams(search || '');
        const dashboardUIDValue = dashboardUID(pathname);
        const uid = isOverviewDashboardUID(dashboardUIDValue) ? dashboardUIDValue : startupDashboardUID(search);
        if (!uid) {
          return '';
        }
        const target = new URLSearchParams({ dashboard_uid: uid });
        for (const key of ['orgId', 'from', 'to', 'timezone']) {
          const value = source.get(key);
          if (value) {
            target.set(key, value);
          }
        }
        target.set('kiosk', '');
        return `/a/erlang-monitor-controls-app/overview?${target.toString()}`;
      }

      function dashboardServer(payload) {
        const variables = payload?.dashboard?.templating?.list;
        if (!Array.isArray(variables)) {
          return '';
        }
        const variable = variables.find((item) => item?.name === 'server');
        const value = variable?.current?.value;
        if (Array.isArray(value)) {
          return value.length === 1 ? String(value[0]).trim() : '';
        }
        return value == null ? '' : String(value).trim();
      }

      function cooldownStorageKey(server) {
        return `erlang-monitor-refresh-cooldown:${server}`;
      }

      function cooldownDeadline(storage, server) {
        if (!storage || !server) {
          return 0;
        }
        try {
          const value = Number(storage.getItem(cooldownStorageKey(server)));
          return Number.isFinite(value) && value > 0 ? value : 0;
        } catch {
          return 0;
        }
      }

      function beginCooldown(storage, server, now = Date.now()) {
        const deadline = now + refreshCooldownMs;
        try {
          storage?.setItem(cooldownStorageKey(server), String(deadline));
        } catch {
          // Session storage can be unavailable in hardened browsers. The caller
          // still applies the in-memory deadline.
        }
        return deadline;
      }

      function cooldownRemaining(storage, server, now = Date.now(), memoryDeadline = 0) {
        return Math.max(0, Math.max(cooldownDeadline(storage, server), memoryDeadline) - now);
      }

      function serverLastAttemptMs(payload, serverID) {
        const value = payload?.servers?.[serverID]?.last_attempt;
        const timestamp = typeof value === 'string' ? Date.parse(value) : Number.NaN;
        return Number.isFinite(timestamp) ? timestamp : 0;
      }

      function latestPrometheusSampleMs(payload) {
        const results = payload?.data?.result;
        if (!Array.isArray(results)) {
          return 0;
        }
        return results.reduce((latest, result) => {
          const timestamp = Number(result?.value?.[1]) * 1000;
          return Number.isFinite(timestamp) ? Math.max(latest, timestamp) : latest;
        }, 0);
      }

      function prometheusLabelValue(value) {
        return String(value ?? '')
          .replaceAll('\\', '\\\\')
          .replaceAll('"', '\\"')
          .replaceAll('\n', '\\n');
      }

      function isDynamicAutoRefresh(search) {
        return new URLSearchParams(search || '').get('refresh') === 'auto';
      }

      function gibibytes(value) {
        const bytes = Number(value);
        return Number.isFinite(bytes) ? bytes / (1024 ** 3) : null;
      }

      function fixed(value, decimals = 2) {
      	if (value == null || value === '') return '无数据';
        const number = Number(value);
        return Number.isFinite(number) ? number.toFixed(decimals) : '无数据';
      }

      function cpuCapacityPercent(logicalCPUs) {
        const cores = Number(logicalCPUs);
        return Number.isFinite(cores) && cores > 0 ? cores * 100 : null;
      }

      function isMNodeInfrastructureNode(nodeName) {
      	const shortName = String(nodeName || '').split('@')[0];
      	return /(?:^|_)c\d+(?:_|$)/i.test(shortName);
      }

      function mergeNodeSamples(upSamples, registeredSamples, onlineSamples, processSamples, residentMemorySamples, cpuRatioSamples, mnodeAvailableSamples = [], mnodeConnectionSamples = []) {
        const rows = new Map();
        const merge = (samples, key) => {
          for (const sample of Array.isArray(samples) ? samples : []) {
            const node = String(sample?.metric?.node || '');
            if (!node) continue;
      	  const row = rows.get(node) || { node, up: null, registered: null, online: null, processes: null, residentMemoryBytes: null, cpuRatio: null, mnodeAvailable: null, connections: [] };
            row[key] = sample.value;
            rows.set(node, row);
          }
        };
        merge(upSamples, 'up');
        merge(registeredSamples, 'registered');
        merge(onlineSamples, 'online');
        merge(processSamples, 'processes');
      	merge(residentMemorySamples, 'residentMemoryBytes');
      	merge(cpuRatioSamples, 'cpuRatio');
      	merge(mnodeAvailableSamples, 'mnodeAvailable');
      	for (const sample of Array.isArray(mnodeConnectionSamples) ? mnodeConnectionSamples : []) {
      		const sourceNode = String(sample?.metric?.node || '');
      		const type = String(sample?.metric?.connection_type || '');
      		if (!sourceNode || !['central', 'region'].includes(type)) continue;
      		const row = rows.get(sourceNode) || { node: sourceNode, up: null, registered: null, online: null, processes: null, residentMemoryBytes: null, cpuRatio: null, mnodeAvailable: null, connections: [] };
      		const state = Number(sample.value);
      		row.connections.push({
      			nodeID: String(sample?.metric?.node_id || ''),
      			node: String(sample?.metric?.connection_node || ''),
      			type,
      			state: Number.isFinite(state) ? state : null,
      			usable: state === 2,
      		});
      		rows.set(sourceNode, row);
      	}
      	for (const row of rows.values()) {
      		row.connections.sort((left, right) => (left.type === right.type ? left.nodeID.localeCompare(right.nodeID) : left.type === 'central' ? -1 : 1));
      	}
        return [...rows.values()].sort((left, right) => left.node.localeCompare(right.node));
      }

      function alertFingerprint(labels) {
        return [labels.alertname, labels.name, labels.node, labels.pid].filter(Boolean).join('|').slice(0, 512);
      }

      function activeAlertsFromRules(payload, serverName) {
        const groups = Array.isArray(payload?.data?.groups) ? payload.data.groups : [];
        const alerts = [];
        for (const group of groups) {
          for (const rule of Array.isArray(group?.rules) ? group.rules : []) {
            for (const alert of Array.isArray(rule?.alerts) ? rule.alerts : []) {
              const labels = { ...(rule?.labels || {}), ...(alert?.labels || {}) };
              if (serverName && labels.name !== serverName) continue;
              if (!['firing', 'pending'].includes(String(alert?.state || rule?.state || ''))) continue;
              alerts.push({
                fingerprint: String(alert?.fingerprint || alertFingerprint(labels)),
                state: String(alert?.state || rule?.state || ''),
                labels,
                annotations: { ...(rule?.annotations || {}), ...(alert?.annotations || {}) },
                activeAt: String(alert?.activeAt || ''),
                value: Number(alert?.value),
              });
            }
          }
        }
        return alerts.sort((left, right) => {
          const severity = { critical: 0, warning: 1 };
          const severityOrder = (severity[left.labels.severity] ?? 2) - (severity[right.labels.severity] ?? 2);
          return severityOrder || left.activeAt.localeCompare(right.activeAt) || left.fingerprint.localeCompare(right.fingerprint);
        });
      }

      function alertLabelText(labels) {
        const preferred = ['severity', 'server', 'name', 'node', 'pid', 'registered_name', 'initial_call', 'current_function'];
        const keys = [...preferred.filter((key) => labels?.[key] != null), ...Object.keys(labels || {}).filter((key) => !preferred.includes(key)).sort()];
        return keys.slice(0, 40).map((key) => `${key}=${String(labels[key])}`).join('，');
      }

      function safeMarkdownText(value, maxLength = 65536) {
        const text = String(value ?? '')
          .replace(/<[^>]*>/g, '')
          .replace(/!\[[^\]]*\]\([^)]*\)/g, '[外链图片已隐藏]')
          .replace(/\[([^\]]+)\]\((?:javascript|data|vbscript):[^)]*\)/gi, '$1')
          .replace(/[\u0000-\u0008\u000b\u000c\u000e-\u001f]/g, '');
        return text.length > maxLength ? `${text.slice(0, maxLength)}…` : text;
      }

      function prometheusSamples(payload) {
        const results = payload?.data?.result;
        if (!Array.isArray(results)) return [];
        return results.map((result) => ({
          metric: result?.metric || {},
          value: Number(result?.value?.[1]),
          sampledAt: Number(result?.value?.[0]) * 1000,
        })).filter((sample) => Number.isFinite(sample.value) && Number.isFinite(sample.sampledAt));
      }

      const h$4 = React.createElement;
      const prometheusProxyURL$1 = '/api/datasources/proxy/uid/prometheus/api/v1';
      const collectProxyURL$1 = '/api/plugin-proxy/erlang-monitor-controls-app/collect';
      const statusProxyURL$1 = '/api/plugin-proxy/erlang-monitor-controls-app/status';
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
        return getBackendSrv().get(`${prometheusProxyURL$1}/query?query=${encodeURIComponent(expression)}`);
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
        return h$4('article', { className: 'eo-gauge' },
          h$4('div', { className: `eo-ring ${colorClass}`, style: { '--value': bounded }, role: 'progressbar', 'aria-label': `${label} ${fixed(value)}，上限 ${fixed(ariaMax, 0)}`, 'aria-valuemin': 0, 'aria-valuemax': ariaMax || 100, 'aria-valuenow': Number(ariaValue) || 0 },
            h$4('div', { className: 'eo-ring-value' }, `${fixed(value)}%`, h$4('small', null, label))),
          h$4('div', null, h$4('div', { className: 'eo-gauge-title' }, label), h$4('div', { className: 'eo-gauge-main' }, detail), h$4('div', { className: 'eo-gauge-sub' }, subdetail)));
      }

      function NodeConnections({ sourceNode, available, connections = [] }) {
        if (isMNodeInfrastructureNode(sourceNode)) return h$4('span', { className: 'eo-connection-empty', title: '中央/赛区等非游戏节点不展示连接关系' }, '—');
        if (Number(available) !== 1) return h$4('span', { className: 'eo-connection-empty' }, '待采集');
        if (!connections.length) return h$4('span', { className: 'eo-connection-empty' }, '未连接');
        return h$4('div', { className: 'eo-connection-list' }, connections.map((connection) => {
          const kind = connection.type === 'central' ? 'C8' : 'C9';
          const shortName = String(connection.node || '').split('@')[0];
          return h$4('span', {
            className: `eo-connection ${connection.type === 'region' ? 'region' : ''} ${connection.usable ? '' : 'unusable'}`.trim(),
            key: `${connection.type}:${connection.nodeID}:${connection.node}`,
            title: `${connection.node || '未知节点'} · state=${connection.state ?? '未知'}`,
          }, h$4('b', null, kind), h$4('span', null, `${connection.nodeID}${shortName ? ` · ${shortName}` : ''}`));
        }));
      }

      function NodeTable({ nodes }) {
        const [expanded, setExpanded] = React.useState(false);
        const canExpand = nodes.length > 6;
        const visible = expanded ? nodes : nodes.slice(0, 6);
        const hiddenOnline = nodes.slice(6).filter((node) => node.up === 1).length;
        return h$4('section', { className: 'eo-panel' },
          h$4('div', { className: 'eo-head' }, h$4('span', null, 'Erlang 节点状态'), h$4('span', { className: 'eo-ok' }, `${nodes.filter((node) => node.up === 1).length} / ${nodes.length} 在线`)),
          h$4('div', { className: 'eo-table-wrap' },
            h$4('table', { className: 'eo-table' },
              h$4('thead', null, h$4('tr', null,
                h$4('th', { className: 'eo-node' }, '节点'), h$4('th', { className: 'eo-status' }, '状态'),
                h$4('th', { className: 'eo-process' }, '进程总数'), h$4('th', { className: 'eo-resource', title: 'BEAM进程常驻内存（RSS）' }, '内存（G）'),
                h$4('th', { className: 'eo-resource', title: 'BEAM进程CPU，100%约等于一个逻辑核' }, 'CPU比例'), h$4('th', { className: 'eo-connections' }, '节点连接'),
                h$4('th', { className: 'eo-pending' }, '注册人数'), h$4('th', { className: 'eo-pending' }, '在线人数'))),
              h$4('tbody', null,
                visible.map((node) => h$4('tr', { key: node.node },
                  h$4('td', { className: 'eo-node' }, node.node),
                  h$4('td', { className: 'eo-status' }, h$4('span', { className: `eo-node-status ${node.up === 1 ? 'up' : 'down'}` }, node.up === 1 ? '在线' : '离线')),
                  h$4('td', { className: 'eo-process' }, formatNumber(node.processes)),
                  h$4('td', { className: 'eo-resource' }, formatResidentMemory(node.residentMemoryBytes)),
                  h$4('td', { className: 'eo-resource' }, formatCPURatio(node.cpuRatio)),
                  h$4('td', { className: 'eo-connections' }, h$4(NodeConnections, { sourceNode: node.node, available: node.mnodeAvailable, connections: node.connections })),
                  h$4('td', { className: 'eo-pending' }, formatNumber(node.registered)),
                  h$4('td', { className: 'eo-pending' }, formatNumber(node.online)))),
                canExpand ? h$4('tr', { className: 'eo-more' }, h$4('td', { colSpan: 8 }, h$4('button', {
                  className: 'eo-more-button', type: 'button', 'aria-expanded': expanded, onClick: () => setExpanded((value) => !value),
                }, expanded ? `收起节点列表 · 共 ${nodes.length} 个节点` : `其余 ${nodes.length - 6} 个节点：${hiddenOnline} 在线`, h$4('span', { className: 'eo-more-arrow', 'aria-hidden': true }, expanded ? '▲' : '▼')))) : null))));
      }

      function AlertList({ alerts, dashboardUID, server }) {
        const source = new URLSearchParams(window.location.search);
        const opsAgentBase = new URLSearchParams({ dashboard_uid: dashboardUID, server, from: source.get('from') || 'now-6h', to: source.get('to') || 'now' });
        return h$4('section', { className: 'eo-panel' },
          h$4('div', { className: 'eo-head' }, h$4('span', null, '异常详情'), h$4('span', { className: alerts.length ? 'eo-warn' : 'eo-ok' }, alerts.length ? `${alerts.length} 条告警` : '当前无异常')),
          alerts.length ? alerts.map((alert, index) => {
            const node = alert.labels.node || '';
            const params = new URLSearchParams(opsAgentBase);
            if (node) params.set('node', node);
            return h$4('details', { className: 'eo-alert', key: alert.fingerprint, open: index === 0 },
              h$4('summary', { className: 'eo-alert-summary' },
                h$4('span', { className: 'eo-alert-icon' }, '!'),
                h$4('span', { className: 'eo-alert-title' }, h$4('strong', null, alert.annotations.summary || alert.labels.alertname || '监控告警'), h$4('small', null, [node, alert.labels.registered_name].filter(Boolean).join(' · ') || alert.labels.name)),
                h$4('span', { className: `eo-severity ${alert.labels.severity || ''}` }, alert.labels.severity || alert.state),
                h$4('span', { className: 'eo-alert-time' }, formatDate(alert.activeAt)), h$4('span', null, '⌄')),
              h$4('div', { className: 'eo-alert-body' },
                h$4('div', { className: 'eo-alert-line' }, h$4('div', { className: 'eo-alert-label' }, '当前值'), h$4('div', { className: 'eo-alert-value eo-current' }, alert.annotations.value || fixed(alert.value))),
                h$4('div', { className: 'eo-alert-line' }, h$4('div', { className: 'eo-alert-label' }, '触发条件'), h$4('div', { className: 'eo-alert-value' }, alert.annotations.condition || '由 Prometheus 告警规则触发')),
                h$4('div', { className: 'eo-alert-line' }, h$4('div', { className: 'eo-alert-label' }, '影响'), h$4('div', { className: 'eo-alert-value' }, alert.annotations.impact || '请结合节点状态判断影响范围')),
                h$4('div', { className: 'eo-alert-line' }, h$4('div', { className: 'eo-alert-label' }, '建议处理'), h$4('div', { className: 'eo-alert-value' }, alert.annotations.action || '进入运维 Agent 分析或按运维流程处理')),
                h$4('div', { className: 'eo-alert-line' }, h$4('div', { className: 'eo-alert-label' }, '标签'), h$4('div', { className: 'eo-alert-value' }, alertLabelText(alert.labels))),
                h$4('div', { className: 'eo-alert-line' }, h$4('div', { className: 'eo-alert-label' }, '辅助分析'), h$4('a', { className: 'eo-agent', href: `/a/erlang-monitor-controls-app/ops-agent?${params.toString()}` }, '打开运维 Agent'))));
          }) : h$4('div', { className: 'eo-empty' }, '当前没有 firing 或 pending 告警'));
      }

      function OverviewPage() {
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
                getBackendSrv().get(`${prometheusProxyURL$1}/rules?type=alert`),
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
            const before = await getBackendSrv().get(statusProxyURL$1).catch(() => null);
            const response = await getBackendSrv().post(collectProxyURL$1, { server: context.server });
            const serverID = String(response?.server || '');
            const baseline = serverLastAttemptMs(before, serverID) || requestedAt;
            const status = await waitFor(() => getBackendSrv().get(statusProxyURL$1), (value) => serverLastAttemptMs(value, serverID) > baseline, 120000);
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

        return h$4('main', { className: 'erlang-overview-page' },
          h$4('style', null, overviewCSS),
          h$4('div', { className: 'eo-toolbar' },
            h$4('div', { className: 'eo-title' }, h$4('h1', null, context.title || 'Erlang 运行总览'), h$4('small', null, context.server || '正在解析服务器')),
            h$4('div', { className: 'eo-controls' },
              dashboards.length ? h$4('select', { className: 'eo-control eo-select', value: context.uid, 'aria-label': '目录-页面', onChange: (event) => locationService.push(`/a/erlang-monitor-controls-app/overview?dashboard_uid=${encodeURIComponent(event.target.value)}&kiosk`) }, dashboards.map((item) => h$4('option', { key: item.uid, value: item.uid }, item.title))) : null,
              h$4('button', { className: 'eo-control', type: 'button', disabled: busy || !context.server, onClick: collectNow }, busy ? '正在采集…' : '刷新采集'),
              h$4('span', { className: 'eo-control' }, '自动 30m'))),
          error ? h$4('div', { className: 'eo-error', role: 'alert' }, safeMarkdownText(error, 1000)) : null,
          h$4('section', { className: 'eo-health' },
            h$4('div', { className: 'eo-health-main' }, h$4('span', { className: `eo-health-icon ${alertCount ? 'warn' : 'ok'}` }, alertCount ? '!' : '✓'), h$4('div', null, h$4('strong', null, alertCount ? `需要关注 · ${alertCount} 条告警` : '运行正常'), h$4('small', null, `${snapshot.nodes.filter((node) => node.up === 1).length}/${snapshot.nodes.length} 节点在线`))),
            h$4('div', { className: 'eo-time' }, '最近采集 ', h$4('strong', null, snapshot.lastSuccess ? new Date(snapshot.lastSuccess * 1000).toLocaleString('zh-CN', { hour12: false }) : '无数据'))),
          h$4('section', { className: 'eo-panel' }, h$4('div', { className: 'eo-head' }, h$4('span', null, '资源水位'), h$4('span', { className: 'eo-ok' }, '主机指标')),
            h$4('div', { className: 'eo-gauges' },
              h$4(MetricRing, { label: 'CPU', value: cpuCurrent, detail: `上限 ${fixed(cpuCapacity, 0)}%`, subdetail: `${fixed(snapshot.cpuLogical, 0)} 逻辑核`, ariaMax: cpuCapacity, ariaValue: cpuCurrent, fill: cpuFill }),
              h$4(MetricRing, { label: '内存', value: memoryPercent, detail: `${fixed(usedG)} / ${fixed(totalG)} G`, subdetail: `可用 ${fixed(availableG)} G`, colorClass: 'memory' }),
              h$4(MetricRing, { label: '硬盘', value: diskPercent, detail: `${fixed(diskUsedG)} / ${fixed(diskTotalG)} G`, subdetail: `可用 ${fixed(diskAvailableG)} G`, colorClass: 'disk' }))),
          h$4(NodeTable, { key: context.server, nodes: snapshot.nodes }),
          h$4(AlertList, { alerts: snapshot.alerts, dashboardUID: context.uid, server: context.server }));
      }

      function serverOptions(payload) {
        const items = Array.isArray(payload?.servers) ? payload.servers : [];
        return items
          .filter((item) => typeof item?.server_id === 'string' && item.server_id.trim() && typeof item?.display_name === 'string' && item.display_name.trim())
          .map((item) => ({ id: item.server_id.trim(), name: item.display_name.trim() }));
      }

      function preferredServer(servers, requested) {
        const value = typeof requested === 'string' ? requested.trim() : '';
        if (!value) return null;
        return servers.find((server) => server.id === value || server.name === value) || null;
      }

      function skillSummaries(payload) {
        const items = Array.isArray(payload?.skills) ? payload.skills : [];
        return items
          .filter((item) => typeof item?.name === 'string' && item.name.trim() && typeof item?.description === 'string' && item.description.trim())
          .map((item) => ({ name: item.name.trim(), description: item.description.trim() }));
      }

      function withTaskID(url, taskID) {
        const next = new URL(url);
        const value = typeof taskID === 'string' ? taskID.trim() : '';
        if (value) next.searchParams.set('task_id', value);
        else next.searchParams.delete('task_id');
        return next.toString();
      }

      const h$3 = React.createElement;
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

      const css$2 = `
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

      function OpsPage() {
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
        const approval = pending ? h$3('div', { className: 'ops-approval' },
          h$3('strong', null, '需要 Admin 批准 Shell'),
          h$3('div', { style: { color: '#f4cf95', fontSize: '12px', marginTop: '6px' } }, pending.target, ' · ', pending.reason),
          h$3('code', null, pending.command),
          h$3('div', { className: 'ops-actions' },
            h$3('button', { className: 'ops-button', disabled: busy, onClick: () => decide(true) }, '批准执行'),
            h$3('button', { className: 'ops-button danger', disabled: busy, onClick: () => decide(false) }, '拒绝'))) : null;
        const timeline = task?.events?.length ? h$3('div', { className: 'ops-timeline' }, task.events.map((event) =>
          h$3('article', { className: 'ops-event', key: event.id },
            h$3('div', { className: 'ops-event-head' }, h$3('span', null, event.type), h$3('time', null, new Date(event.at).toLocaleTimeString('zh-CN', { hour12: false }))),
            h$3('pre', null, safeMarkdownText(eventText(event), 16000))))) : h$3('div', { className: 'ops-empty' }, '任务启动后，这里会显示 Skill、Shell 和验证步骤。');
        return h$3('main', { className: 'ops-page' }, h$3('style', null, css$2),
          h$3('header', { className: 'ops-head' },
            h$3('div', null, h$3('div', { className: 'ops-kicker' }, 'ERLANG / OPERATIONS AGENT'), h$3('h1', { className: 'ops-title' }, '一次任务，完成分析与处理'), h$3('div', { className: 'ops-sub' }, '模型只负责判断和提出动作；服务器边界、Skill、Shell 审批和结果验证由平台控制。')),
            h$3('div', { className: 'ops-chip' }, task ? statusText(task.status) : '待命')),
          h$3('div', { className: 'ops-grid' },
            h$3('section', { className: 'ops-panel' },
              h$3('h2', null, '目标上下文'),
              h$3('div', { className: 'ops-meta' },
                h$3('div', null,
                  h$3('label', { className: 'ops-field-label', htmlFor: 'ops-server-select' }, '内网节点（单选）'),
                  h$3('select', { id: 'ops-server-select', className: 'ops-select', value: server.id, disabled: Boolean(task) || catalogLoading, onChange: selectServer },
                    h$3('option', { value: '' }, catalogLoading ? '正在读取节点清单…' : '请选择一个内网节点'),
                    servers.map((item) => h$3('option', { key: item.id, value: item.id }, `${item.name} · ${item.id}`)))),
                h$3('div', null, h$3('span', null, 'Stable ID'), h$3('strong', null, server.id || '—')),
                h$3('div', null, h$3('span', null, 'Node'), h$3('strong', null, node || '未固定')),
                h$3('div', null, h$3('span', null, 'Time window'), h$3('strong', null, `${query.get('from') || 'now-1h'} → ${query.get('to') || 'now'}`))),
              h$3('div', { className: 'ops-notice', style: { marginTop: '16px' } }, '只允许在已选 192.168.100.* 内网节点上按已加载 Skill 的职责执行。纯 ls / grep / ps / cd / head / tail / df / find 只读组合自动执行；其他允许的 Shell 逐条等待 Grafana Admin 审批。Agent 不保存长期记忆。')),
            h$3('div', { className: 'ops-stack' },
              h$3('section', { className: 'ops-panel' },
                h$3('h2', null, '执行轨迹'), timeline,
                task?.final_answer ? h$3('div', { style: { marginTop: '18px' } }, h$3('h2', null, '最终结果'), h$3('div', { className: 'ops-answer' }, safeMarkdownText(task.final_answer, 32000))) : null),
              h$3('section', { className: 'ops-panel' },
                h$3('h2', null, '任务输入'),
                h$3('textarea', { className: 'ops-textarea', value: question, disabled: Boolean(task), onChange: (event) => setQuestion(event.target.value), maxLength: 8000 }),
                h$3('div', { className: 'ops-actions' },
                  !task ? h$3('button', { className: 'ops-button', disabled: busy || !server.id, onClick: start }, busy ? '正在启动…' : '开始运维任务') : null,
                  task && ['completed', 'failed'].includes(task.status) ? h$3('button', { className: 'ops-button secondary', onClick: () => { persistTaskID(''); setTask(null); setError(''); } }, '新建任务') : null),
                error ? h$3('div', { className: 'ops-error', style: { marginTop: '12px' } }, safeMarkdownText(error, 1000)) : null,
                approval)),
            h$3('aside', { className: 'ops-panel ops-skills-panel' },
              h$3('h2', null, '可用 Skill'),
              h$3('div', { className: 'ops-skill-count' }, catalogLoading ? '正在读取…' : `${skills.length} 个专项流程`),
              skills.length ? h$3('div', { className: 'ops-skill-list' }, skills.map((skill) =>
                h$3('article', { className: 'ops-skill', key: skill.name },
                  h$3('code', null, skill.name),
                  h$3('p', null, safeMarkdownText(skill.description, 1000))))) : h$3('div', { className: 'ops-empty' }, catalogLoading ? '正在加载 Skill 清单。' : '当前没有可用 Skill。'))));
      }

      const requiredProjectHeaders = ['project_id', '项目', 'repo_path', 'agent', 'skill'];

      function inlineTokens(value) {
        const source = String(value ?? '');
        const tokens = [];
        const pattern = /(`[^`\n]+`|\*\*[^*\n]+\*\*|__[^_\n]+__|\*[^*\n]+\*|_[^_\n]+_|\[[^\]\n]+\]\([^)\s]+\))/g;
        let cursor = 0;
        for (const match of source.matchAll(pattern)) {
          if (match.index > cursor) tokens.push({ type: 'text', text: source.slice(cursor, match.index) });
          const raw = match[0];
          if (raw.startsWith('`')) tokens.push({ type: 'code', text: raw.slice(1, -1) });
          else if (raw.startsWith('**') || raw.startsWith('__')) tokens.push({ type: 'strong', text: raw.slice(2, -2) });
          else if (raw.startsWith('*') || raw.startsWith('_')) tokens.push({ type: 'em', text: raw.slice(1, -1) });
          else {
            const link = raw.match(/^\[([^\]]+)\]\(([^)]+)\)$/);
            const url = link?.[2] || '';
            if (/^https?:\/\//i.test(url)) tokens.push({ type: 'link', text: link[1], url });
            else tokens.push({ type: 'text', text: link?.[1] || raw });
          }
          cursor = match.index + raw.length;
        }
        if (cursor < source.length) tokens.push({ type: 'text', text: source.slice(cursor) });
        return tokens;
      }

      function markdownCells(line) {
        return line.trim().replace(/^\|/, '').replace(/\|$/, '').split(/(?<!\\)\|/).map((cell) => cell.trim().replace(/\\\|/g, '|'));
      }

      function isTableDivider(line) {
        const cells = markdownCells(line);
        return cells.length > 0 && cells.every((cell) => /^:?-{3,}:?$/.test(cell));
      }

      function hasMarkdownDocumentStructure(value) {
        const lines = String(value ?? '').split('\n');
        const signals = new Set();
        for (let index = 0; index < lines.length; index += 1) {
          const line = lines[index];
          if (/^\s{0,3}#{1,6}\s+\S/.test(line)) signals.add('heading');
          if (/^\s{0,3}(?:[-+*]|\d+[.)])\s+\S/.test(line)) signals.add('list');
          if (/^\s{0,3}>\s?\S/.test(line)) signals.add('quote');
          if (/(?:\*\*|__)[^\n]+(?:\*\*|__)/.test(line)) signals.add('strong');
          if (line.includes('|') && index + 1 < lines.length && isTableDivider(lines[index + 1])) signals.add('table');
        }
        return signals.has('heading') && signals.size >= 2;
      }

      function unwrapMarkdownDocumentFence(value) {
        const source = String(value ?? '').replace(/\r\n?/g, '\n').trim();
        const lines = source.split('\n');
        if (lines.length < 3) return source;
        const opening = lines[0].match(/^```\s*([\w.+-]*)\s*$/);
        if (!opening || !/^```\s*$/.test(lines[lines.length - 1])) return source;
        const language = opening[1].toLowerCase();
        if (!['', 'markdown', 'md', 'code'].includes(language)) return source;
        if (lines.slice(1, -1).some((line) => /^```\s*[\w.+-]*\s*$/.test(line))) return source;
        const body = lines.slice(1, -1).join('\n').trim();
        if (!['markdown', 'md'].includes(language) && !hasMarkdownDocumentStructure(body)) return source;
        return body;
      }

      function markdownForClipboard(value, maxLength = 65536) {
        return unwrapMarkdownDocumentFence(safeMarkdownText(value, maxLength)).trim();
      }

      function parseSafeMarkdown(value, maxLength = 65536) {
        const text = unwrapMarkdownDocumentFence(safeMarkdownText(value, maxLength));
        const lines = text.split('\n');
        const blocks = [];
        let index = 0;
        while (index < lines.length) {
          const line = lines[index];
          if (!line.trim()) { index += 1; continue; }

          const fence = line.match(/^\s*```\s*([\w.+-]*)\s*$/);
          if (fence) {
            const code = [];
            index += 1;
            while (index < lines.length && !/^\s*```\s*$/.test(lines[index])) { code.push(lines[index]); index += 1; }
            if (index < lines.length) index += 1;
            blocks.push({ type: 'codeBlock', language: fence[1].slice(0, 24), text: code.join('\n') });
            continue;
          }

          const heading = line.match(/^\s{0,3}(#{1,6})\s+(.+?)\s*#*\s*$/);
          if (heading) {
            blocks.push({ type: 'heading', level: heading[1].length, children: inlineTokens(heading[2]) });
            index += 1;
            continue;
          }

          if (/^\s{0,3}(?:-{3,}|\*{3,}|_{3,})\s*$/.test(line)) {
            blocks.push({ type: 'rule' }); index += 1; continue;
          }

          if (line.includes('|') && index + 1 < lines.length && isTableDivider(lines[index + 1])) {
            const header = markdownCells(line).map(inlineTokens);
            const rows = [];
            index += 2;
            while (index < lines.length && lines[index].includes('|') && lines[index].trim()) {
              rows.push(markdownCells(lines[index]).map(inlineTokens)); index += 1;
            }
            blocks.push({ type: 'table', header, rows });
            continue;
          }

          const listMatch = line.match(/^\s{0,3}([-+*]|\d+[.)])\s+(.+)$/);
          if (listMatch) {
            const ordered = /^\d/.test(listMatch[1]);
            const items = [];
            while (index < lines.length) {
              const item = lines[index].match(/^\s{0,3}([-+*]|\d+[.)])\s+(.+)$/);
              if (!item || /^\d/.test(item[1]) !== ordered) break;
              items.push(inlineTokens(item[2])); index += 1;
            }
            blocks.push({ type: 'list', ordered, items });
            continue;
          }

          if (/^\s{0,3}>/.test(line)) {
            const quote = [];
            while (index < lines.length && /^\s{0,3}>/.test(lines[index])) {
              quote.push(lines[index].replace(/^\s{0,3}>\s?/, '')); index += 1;
            }
            blocks.push({ type: 'quote', children: inlineTokens(quote.join('\n')) });
            continue;
          }

          const paragraph = [line.trim()];
          index += 1;
          while (index < lines.length && lines[index].trim()
            && !/^\s*```/.test(lines[index])
            && !/^\s{0,3}(?:#{1,6})\s+/.test(lines[index])
            && !/^\s{0,3}(?:[-+*]|\d+[.)])\s+/.test(lines[index])
            && !/^\s{0,3}>/.test(lines[index])
            && !(lines[index].includes('|') && index + 1 < lines.length && isTableDivider(lines[index + 1]))) {
            paragraph.push(lines[index].trim()); index += 1;
          }
          blocks.push({ type: 'paragraph', children: inlineTokens(paragraph.join('\n')) });
        }
        return blocks;
      }

      function mcpInitializeRequest(id) {
        return {
          jsonrpc: '2.0', id, method: 'initialize',
          params: { protocolVersion: '2025-06-18', capabilities: {}, clientInfo: { name: 'grafana-code-analysis', version: '1.0.0' } },
        };
      }

      function mcpInitializedNotification() {
        return { jsonrpc: '2.0', method: 'notifications/initialized' };
      }

      function mcpToolsListRequest(id) {
        return { jsonrpc: '2.0', id, method: 'tools/list', params: {} };
      }

      function mcpToolRequest(id, name, args = {}) {
        return { jsonrpc: '2.0', id, method: 'tools/call', params: { name, arguments: args } };
      }

      function parseMCPEnvelope(payload, expectedID) {
        const source = String(payload ?? '').trim();
        if (!source) throw new Error('代码 MCP 返回了空响应');
        const candidates = [];
        if (source.startsWith('{') || source.startsWith('[')) candidates.push(source);
        else for (const line of source.split(/\r?\n/)) if (line.startsWith('data:')) candidates.push(line.slice(5).trim());
        for (const candidate of candidates) {
          let decoded;
          try { decoded = JSON.parse(candidate); } catch { continue; }
          const messages = Array.isArray(decoded) ? decoded : [decoded];
          const matched = messages.find((message) => String(message?.id) === String(expectedID));
          if (matched) return matched;
        }
        throw new Error('代码 MCP 响应中没有匹配的 JSON-RPC 结果');
      }

      function toolText(envelope, toolName) {
        if (envelope?.error) throw new Error(`${toolName} 调用失败：${envelope.error.message || 'JSON-RPC 调用失败'}`);
        const result = envelope?.result;
        const text = Array.isArray(result?.content)
          ? result.content.filter((item) => item?.type === 'text').map((item) => String(item.text || '')).join('\n')
          : '';
        if (result?.isError === true) {
          const code = text.match(/(?:错误码|error code)\s*[：:]\s*([A-Z_]+)/i)?.[1] || 'TOOL_ERROR';
          throw new Error(`${toolName} 调用失败 [${code}]：${text || '未返回错误详情'}`);
        }
        if (!text.trim()) throw new Error(`${toolName} 没有返回文本结果`);
        return text;
      }

      function requireMCPTools(envelope, requiredNames) {
        if (envelope?.error) throw new Error(`tools/list 调用失败：${envelope.error.message || 'JSON-RPC 调用失败'}`);
        const names = Array.isArray(envelope?.result?.tools) ? envelope.result.tools.map((tool) => tool?.name).filter(Boolean) : [];
        const missing = requiredNames.filter((name) => !names.includes(name));
        if (missing.length) throw new Error(`代码 MCP 缺少必要工具：${missing.join(', ')}`);
        return names;
      }

      function appendTrace(current, trace, maxItems = 40) {
        const items = Array.isArray(current) ? current : [];
        if (['protocol', 'catalog'].includes(trace?.type) && items.some((item) => item?.type === trace.type && item?.detail === trace.detail)) return items;
        const previous = items[items.length - 1];
        if (previous?.type === trace?.type && previous?.detail === trace?.detail) return items;
        return [...items, trace].slice(-maxItems);
      }

      function formatDuration(milliseconds) {
        const totalSeconds = Math.max(0, Math.floor(Number(milliseconds) / 1000) || 0);
        const hours = Math.floor(totalSeconds / 3600);
        const minutes = Math.floor((totalSeconds % 3600) / 60);
        const seconds = totalSeconds % 60;
        if (hours) return `${hours}:${String(minutes).padStart(2, '0')}:${String(seconds).padStart(2, '0')}`;
        return `${String(minutes).padStart(2, '0')}:${String(seconds).padStart(2, '0')}`;
      }

      function parseProjects(markdown) {
        const projects = [];
        const lines = String(markdown ?? '').split(/\r?\n/);
        let columns = null;
        for (let index = 0; index < lines.length; index += 1) {
          const line = lines[index];
          if (!line.trim().startsWith('|')) { columns = null; continue; }
          const cells = markdownCells(line);
          if (index + 1 < lines.length && isTableDivider(lines[index + 1])) {
            const normalized = cells.map((cell) => cell.trim().toLowerCase());
            const candidate = Object.fromEntries(requiredProjectHeaders.map((header) => [header, normalized.indexOf(header)]));
            if (requiredProjectHeaders.every((header) => candidate[header] >= 0)) {
              columns = {
                id: candidate.project_id,
                name: candidate['项目'],
                repoPath: candidate.repo_path,
                agent: candidate.agent,
                skill: candidate.skill,
                code: normalized.indexOf('code'),
                svnUpdate: normalized.indexOf('分析前svn更新'),
              };
            } else {
              columns = null;
            }
            index += 1;
            continue;
          }
          if (!columns || isTableDivider(line)) continue;
          const id = cells[columns.id];
          const name = cells[columns.name];
          const repoPath = cells[columns.repoPath];
          const agent = cells[columns.agent];
          const skill = cells[columns.skill];
          if (!id || !name || !/^(?:[A-Za-z]:[\\/]|\/)/.test(repoPath)) continue;
          const normalizedRepoPath = repoPath.replace(/\\/g, '/').replace(/\/+$/, '');
          const project = { id, name, repoPath: normalizedRepoPath, branch: normalizedRepoPath.split('/').pop() || '', agent, skill };
          if (columns.code >= 0 && cells[columns.code]) project.code = cells[columns.code];
          if (columns.svnUpdate >= 0 && cells[columns.svnUpdate]) project.svnUpdate = cells[columns.svnUpdate];
          projects.push(project);
        }
        return projects;
      }

      function inspectionList(value) {
        return String(value || '').split(/[、,，]/).map((item) => item.trim()).filter(Boolean);
      }

      function namedInspectionTool(value) {
        const text = String(value || '').trim();
        const match = text.match(/^([^（(]+)[（(]([^）)]+)[）)]$/);
        return match ? { status: match[1].trim(), name: match[2].trim() } : { status: text, name: '' };
      }

      function parseRepositoryInspection(markdown) {
        const fields = {};
        const extra = [];
        for (const sourceLine of safeMarkdownText(markdown, 12000).split(/\r?\n/)) {
          const line = sourceLine.trim().replace(/^#+\s*/, '').replace(/^[-*]\s+/, '');
          if (!line) continue;
          const match = line.match(/^([^：:]+)[：:]\s*(.*)$/);
          if (!match) { extra.push(line); continue; }
          const key = match[1].replace(/[`*_]/g, '').trim().toLowerCase();
          const value = match[2].replace(/^`|`$/g, '').trim();
          fields[key] = value;
        }
        const agent = namedInspectionTool(fields.agent);
        const skill = namedInspectionTool(fields.skill);
        return {
          projectName: fields['项目检查'] || '',
          projectID: fields.projectid || '',
          projectCode: fields.projectcode || '',
          access: fields['目录访问'] || '',
          module: fields['module.md'] || '',
          agent,
          skill,
          svnUpdate: fields['分析前svn更新'] || '',
          conflictPolicy: fields['本地冲突策略'] || '',
          allowedDirectories: inspectionList(fields['允许目录']),
          allowedFiles: inspectionList(fields['允许文件']),
          excludedDirectories: inspectionList(fields['过滤目录']),
          timeout: fields['分析超时'] || '',
          concurrency: fields['项目并发上限'] || '',
          extra,
        };
      }

      function clipped(value, maxLength) {
        const text = String(value ?? '').trim();
        return text.length <= maxLength ? text : `${text.slice(0, maxLength)}\n[前文已截断]`;
      }

      function buildConversationQuestion(turns, currentQuestion, maxLength = 16000) {
        const current = String(currentQuestion ?? '').trim();
        if (!current) return '';
        const history = Array.isArray(turns)
          ? turns.filter((turn) => ['user', 'assistant'].includes(turn?.role) && String(turn?.content || '').trim()).slice(-6)
          : [];
        if (!history.length) return current.slice(0, maxLength);
        const prefix = '这是同一代码分析会话中的追问。前文仅用于理解指代，所有结论仍须重新依据当前源码核对；不要把前文回答当作已验证事实。\n\n前文：\n';
        const suffix = `\n\n当前追问：\n${current}`;
        const budget = Math.max(0, maxLength - prefix.length - suffix.length);
        const selected = [];
        let used = 0;
        for (let index = history.length - 1; index >= 0; index -= 1) {
          const turn = history[index];
          const label = turn.role === 'user' ? '用户' : '代码分析';
          const remaining = budget - used - label.length - 4;
          if (remaining <= 0) break;
          const content = clipped(turn.content, Math.min(remaining, turn.role === 'assistant' ? 6000 : 3000));
          selected.unshift(`${label}：${content}`);
          used += content.length + label.length + 3;
        }
        const historyText = selected.join('\n\n');
        const boundedHistory = historyText.length > budget ? historyText.slice(-budget) : historyText;
        return `${prefix}${boundedHistory}${suffix}`;
      }

      function restoreConversation(value) {
        if (!value || typeof value !== 'object') return null;
        const turns = Array.isArray(value.turns)
          ? value.turns.filter((turn) => ['user', 'assistant'].includes(turn?.role) && typeof turn.content === 'string').slice(-20)
          : [];
        const traces = Array.isArray(value.traces)
          ? value.traces.filter((trace) => typeof trace?.type === 'string' && typeof trace?.at === 'string').slice(-40).reduce((items, trace) => appendTrace(items, trace), [])
          : [];
        if (typeof value.id !== 'string' || typeof value.repoPath !== 'string') return null;
        return { id: value.id, repoPath: value.repoPath, projectName: String(value.projectName || ''), turns, traces };
      }

      function reconcileConversationProject(conversation, projects) {
        const items = Array.isArray(projects) ? projects : [];
        const selected = items.find((project) => project?.repoPath === conversation?.repoPath);
        if (selected) return { repoPath: selected.repoPath, reset: false };
        return { repoPath: items[0]?.repoPath || '', reset: Boolean(conversation?.turns?.length) };
      }

      const h$2 = React.createElement;
      const codeProxyURL = '/api/plugin-proxy/erlang-monitor-controls-app/code-mcp';
      const codeHealthURL = '/api/plugin-proxy/erlang-monitor-controls-app/code-mcp-health';
      const storageKey = 'erlang-monitor-code-chat-v1';

      const css$1 = `
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
          if (token.type === 'code') nodes.push(h$2('code', { key }, token.text));
          else if (token.type === 'strong') nodes.push(h$2('strong', { key }, token.text));
          else if (token.type === 'em') nodes.push(h$2('em', { key }, token.text));
          else if (token.type === 'link') nodes.push(h$2('a', { key, href: token.url, target: '_blank', rel: 'noopener noreferrer' }, token.text));
          else {
            const lines = token.text.split('\n');
            lines.forEach((line, lineIndex) => {
              if (lineIndex) nodes.push(h$2('br', { key: `${key}-br-${lineIndex}` }));
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
          if (block.type === 'heading') return h$2(`h${block.level}`, { key }, inlineNodes(block.children, key));
          if (block.type === 'paragraph') return h$2('p', { key }, inlineNodes(block.children, key));
          if (block.type === 'quote') return h$2('blockquote', { key }, inlineNodes(block.children, key));
          if (block.type === 'rule') return h$2('hr', { key });
          if (block.type === 'list') {
            return h$2(block.ordered ? 'ol' : 'ul', { key }, block.items.map((item, itemIndex) => h$2('li', { key: `${key}-${itemIndex}` }, inlineNodes(item, `${key}-${itemIndex}`))));
          }
          if (block.type === 'table') {
            return h$2('div', { className: 'code-table-wrap', key }, h$2('table', null,
              h$2('thead', null, h$2('tr', null, block.header.map((cell, cellIndex) => h$2('th', { key: `${key}-h-${cellIndex}` }, inlineNodes(cell, `${key}-h-${cellIndex}`))))),
              h$2('tbody', null, block.rows.map((row, rowIndex) => h$2('tr', { key: `${key}-r-${rowIndex}` }, row.map((cell, cellIndex) => h$2('td', { key: `${key}-r-${rowIndex}-${cellIndex}` }, inlineNodes(cell, `${key}-r-${rowIndex}-${cellIndex}`))))))));
          }
          if (block.type === 'codeBlock') {
            return h$2('div', { className: 'code-codeblock', key },
              h$2('div', { className: 'code-code-head' }, h$2('span', null, block.language || 'code'), h$2('span', null, '只读代码片段')),
              h$2('pre', null, h$2('code', null, block.text)));
          }
          return null;
        });
        return h$2(React.Fragment, null,
          copyable ? h$2('div', { style: { display: 'flex', justifyContent: 'flex-end', marginBottom: '8px' } },
            h$2('button', { type: 'button', className: 'code-copy-button', onClick: copyMarkdown, disabled: copyState === 'copied' }, copyState === 'copied' ? '已复制 Markdown' : (copyState === 'failed' ? '复制失败' : '复制 Markdown'))) : null,
          h$2('div', { className: 'code-markdown' }, content));
      }

      function checkTone(value) {
        return /正常|可用|启用/.test(value) ? 'ok' : (/缺失|不可用|失败/.test(value) ? 'warn' : '');
      }

      function RepositoryInspection({ value }) {
        const report = React.useMemo(() => parseRepositoryInspection(value), [value]);
        const checkItem = (label, content) => content ? h$2('div', { className: `code-check-item ${checkTone(content)}` }, h$2('span', null, label), h$2('strong', null, content)) : null;
        const boundaryGroup = (label, items) => items.length ? h$2('div', { className: 'code-boundary-group' }, h$2('b', null, label), h$2('div', { className: 'code-boundary-tags' }, items.map((item) => h$2('code', { className: 'code-boundary-tag', key: item }, item)))) : null;
        return h$2('div', { className: 'code-inspection-summary' },
          report.projectName ? h$2('div', { className: 'code-inspection-title' }, report.projectName) : null,
          h$2('div', { className: 'code-inspection-id' },
            report.projectID ? h$2('code', null, `project_id · ${report.projectID}`) : null,
            report.projectCode ? h$2('code', null, `code · ${report.projectCode}`) : null),
          h$2('div', { className: 'code-inspection-grid' },
            checkItem('目录访问', report.access),
            checkItem('MODULE.md', report.module),
            checkItem('Agent', [report.agent.status, report.agent.name].filter(Boolean).join(' · ')),
            checkItem('SKILL', [report.skill.status, report.skill.name].filter(Boolean).join(' · ')),
            checkItem('分析前 SVN 更新', report.svnUpdate)),
          report.conflictPolicy ? h$2('div', { className: 'code-conflict' }, h$2('strong', null, '本地冲突策略：'), report.conflictPolicy) : null,
          h$2('details', { className: 'code-advanced' }, h$2('summary', null, '高级边界与运行限制'),
            boundaryGroup('允许目录', report.allowedDirectories),
            boundaryGroup('允许文件', report.allowedFiles),
            boundaryGroup('过滤目录', report.excludedDirectories),
            h$2('div', { className: 'code-limit-row' },
              report.timeout ? h$2('span', null, `分析超时：${report.timeout}`) : null,
              report.concurrency ? h$2('span', null, `项目并发上限：${report.concurrency}`) : null),
            report.extra.length ? h$2('div', { className: 'code-boundary-group' }, h$2('b', null, '其他说明'), h$2(MarkdownContent, { value: report.extra.join('\n'), maxLength: 2000 })) : null));
      }

      const promptExamples = [
        '定位这个模块的入口、主要调用链和关键分支',
        '解释上一轮提到的字段类型和数据来源',
        '检查这个错误码的触发条件与返回路径',
      ];

      function CodePage() {
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

        const turnList = turns.length ? h$2('div', { className: 'code-turns', ref: turnsRef, 'aria-live': 'polite' }, turns.map((turn, index) =>
          h$2('article', { className: `code-turn ${turn.role}`, key: `${turn.at}-${index}` },
            h$2('div', { className: 'code-turn-head' },
              h$2('span', { className: 'code-turn-role' }, h$2('span', { className: 'code-avatar' }, turn.role === 'user' ? '你' : 'AI'), turn.role === 'user' ? '你的问题' : '代码解析报告'),
              h$2('time', null, new Date(turn.at).toLocaleTimeString('zh-CN', { hour12: false }))),
            h$2(MarkdownContent, { value: turn.content, maxLength: 64000, copyable: turn.role === 'assistant' })))) : h$2('div', { className: 'code-turns', ref: turnsRef },
              h$2('div', { className: 'code-welcome' },
                h$2('div', { className: 'code-welcome-mark', 'aria-hidden': 'true' }, '</>'),
                h$2('h3', null, '从一个具体代码问题开始'),
                h$2('p', null, '选择注册项目后，系统会先检查仓库边界，再调用只读 MCP 分析源码。回答支持 Markdown 排版，并在每轮追问时重新核对证据。'),
                h$2('div', { className: 'code-steps' },
                  h$2('div', { className: 'code-step' }, h$2('b', null, '01 · 选择'), h$2('span', null, '锁定已注册代码项目')),
                  h$2('div', { className: 'code-step' }, h$2('b', null, '02 · 分析'), h$2('span', null, '检查 Agent 与 SKILL')),
                  h$2('div', { className: 'code-step' }, h$2('b', null, '03 · 追问'), h$2('span', null, '携带限长上下文继续核对'))),
                h$2('div', { className: 'code-examples' }, promptExamples.map((example) => h$2('button', { className: 'code-example', type: 'button', key: example, disabled: busy || !repoPath, onClick: () => setQuestion(example) }, example)))));

        return h$2('main', { className: 'code-page' }, h$2('style', null, css$1),
          h$2('header', { className: 'code-head' },
            h$2('div', { className: 'code-brand' }, h$2('div', { className: 'code-kicker' }, 'CODE ANALYSIS / MCP'), h$2('h1', { className: 'code-title' }, '代码解析对话'), h$2('div', { className: 'code-sub' }, '通过 Grafana 服务端代理连接只读 Code Analysis MCP。每轮使用项目 Agent 与 SKILL 核对源码；多轮上下文只用于理解指代，不替代本轮证据。')),
            h$2('div', { className: 'code-head-side' },
              h$2('div', { className: 'code-health' }, h$2('span', { className: 'code-health-dot', 'aria-hidden': 'true' }), health),
              h$2('div', { className: 'code-capabilities' }, h$2('span', { className: 'code-chip' }, '只读分析'), h$2('span', { className: 'code-chip' }, '多轮对话'), h$2('span', { className: 'code-chip' }, 'Markdown')))),
          h$2('div', { className: 'code-grid' },
            h$2('section', { className: 'code-panel code-boundary' },
              h$2('div', { className: 'code-panel-head' }, h$2('h2', null, '注册代码项目'), h$2('span', { className: 'code-panel-label' }, projectLocked ? '已锁定' : `${projects.length} 个项目`)),
              h$2('div', { className: 'code-field' }, h$2('label', { htmlFor: 'code-project' }, '当前 Code MCP 下的项目'),
                h$2('select', { id: 'code-project', className: 'code-select', value: repoPath, disabled: busy || projectLocked, onChange: (event) => { setRepoPath(event.target.value); setInspection(''); setInspectionReady(false); } },
                  projects.length ? projects.map((project) => h$2('option', { key: project.id, value: project.repoPath }, project.name)) : h$2('option', { value: '' }, '正在读取项目清单…'))),
              selected ? h$2('div', { className: 'code-project-card' },
                h$2('div', { className: 'code-project-name' }, selected.name),
                h$2('div', { className: 'code-project-branch' }, selected.branch),
                h$2('div', { className: 'code-project-meta' },
                  h$2('div', null, h$2('span', null, 'project_id'), h$2('strong', { title: selected.id }, selected.id)),
                  selected.code ? h$2('div', null, h$2('span', null, 'code'), h$2('strong', { title: selected.code }, selected.code)) : null,
                  h$2('div', null, h$2('span', null, 'Agent'), h$2('strong', { title: selected.agent }, selected.agent)),
                  h$2('div', null, h$2('span', null, 'SKILL'), h$2('strong', { title: selected.skill }, selected.skill)),
                  selected.svnUpdate ? h$2('div', null, h$2('span', null, '分析前 SVN 更新'), h$2('strong', null, selected.svnUpdate)) : null,
                  h$2('div', null, h$2('span', null, 'Mode'), h$2('strong', null, 'read-only')))) : null,
              h$2('div', { className: 'code-note', style: { marginTop: '14px' } }, projectLocked ? '本会话已有消息，项目已锁定。新建会话后可切换项目。' : '这里列出当前 Code MCP 注册的代码项目，不是 MCP 服务列表；只能选择服务端返回的精确项目。'),
              inspection ? h$2('details', { className: 'code-inspection' }, h$2('summary', null, '查看仓库检查报告'), h$2('div', { className: 'code-inspection-body' }, h$2(RepositoryInspection, { value: inspection }))) : null),
            h$2('section', { className: 'code-panel code-chat' },
              h$2('div', { className: 'code-panel-head' }, h$2('h2', null, '分析会话'), h$2('span', { className: 'code-session' }, conversationID.slice(0, 8))), turnList,
              h$2('div', { className: 'code-composer' }, h$2('div', { className: 'code-field' }, h$2('label', { htmlFor: 'code-question' }, turns.length ? '继续追问' : '分析问题'),
                h$2('textarea', { id: 'code-question', className: 'code-textarea', value: question, disabled: busy || !repoPath, maxLength: 4000, placeholder: '例如：先确认 m27_h 是否存在，再分析指定 GM 命令的参数类型、调用链与风险。', onChange: (event) => setQuestion(event.target.value), onKeyDown: (event) => { if (event.ctrlKey && event.key === 'Enter') void submit(); } })),
              h$2('div', { className: 'code-actions' },
                h$2('button', { className: 'code-button', disabled: busy || !repoPath || !question.trim(), onClick: submit }, busy ? (activity ? `${traceLabel(activity.type)} ${formatDuration(clock - activity.startedAt)}` : '正在准备…') : (turns.length ? '发送追问' : '开始分析')),
                busy && abortRef.current ? h$2('button', { className: 'code-button danger', onClick: () => abortRef.current?.abort() }, '取消本轮') : null,
                h$2('button', { className: 'code-button secondary', disabled: busy, onClick: newConversation }, '新建会话'),
                h$2('span', { className: 'code-shortcut' }, 'Ctrl + Enter 发送')),
              activity ? h$2('div', { className: 'code-timing', role: 'status', 'aria-live': 'polite', style: { marginTop: '10px' } },
                h$2('span', { className: 'code-timing-dot', 'aria-hidden': true }),
                h$2('div', { className: 'code-timing-copy' }, h$2('b', null, `当前：${traceLabel(activity.type)} ${formatDuration(clock - activity.startedAt)}`), h$2('span', null, `本轮总计 ${formatDuration(clock - activity.totalStartedAt)}`))) : null,
              error ? h$2('div', { className: 'code-error', role: 'alert', style: { marginTop: '12px' } }, safeMarkdownText(error, 2000)) : null)),
            h$2('aside', { className: 'code-panel code-trace-panel' },
              h$2('div', { className: 'code-panel-head' }, h$2('h2', null, 'MCP 调用轨迹'), h$2('span', { className: 'code-panel-label' }, `${traces.length} 条`)),
              traces.length ? h$2('div', { className: 'code-traces' }, traces.map((trace) => h$2('div', { className: 'code-trace', key: trace.id }, h$2('div', { className: 'code-trace-head' }, h$2('strong', null, traceLabel(trace.type)), Number.isFinite(trace.durationMs) ? h$2('span', { className: 'code-trace-time' }, formatDuration(trace.durationMs)) : null), h$2('span', null, safeMarkdownText(trace.detail, 600))))) : h$2('div', { className: 'code-empty' }, '调用后显示项目清单、仓库检查和分析阶段。'),
              h$2('div', { className: 'code-note', style: { marginTop: '14px' } }, '当前能力只读：不修改代码、不构建、不部署，也不直接执行玩家 GM。需要操作时，应回到受审批保护的运维流程。'))));
      }

      const h$1 = React.createElement;

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

      function HomePage() {
        return h$1('main', { className: 'em-home' },
          h$1('style', null, css),
          h$1('div', { className: 'em-home-shell' },
            h$1('header', { className: 'em-home-head' },
              h$1('div', null,
                h$1('div', { className: 'em-home-kicker' }, 'ERLANG MONITOR · CONTROL CENTER'),
                h$1('h1', { className: 'em-home-title' }, '监控、运维与代码分析，一个入口。'),
                h$1('p', { className: 'em-home-lead' }, '这是 Erlang Monitor Controls 的应用入口。按任务选择工作台；服务器运行总览仍从具体 Dashboard 上下文进入。')),
              h$1('div', { className: 'em-home-status' }, '应用入口已就绪')),
            h$1('section', { className: 'em-home-grid', 'aria-label': '应用功能入口' },
              entries.map((entry) => h$1('a', { className: `em-home-card ${entry.className}`, href: entry.href, key: entry.href },
                h$1('div', { className: 'em-home-card-body' },
                  h$1('span', { className: 'em-home-index' }, entry.index),
                  h$1('h2', null, entry.title),
                  h$1('p', null, entry.description),
                  h$1('div', { className: 'em-home-tags' }, entry.tags.map((tag) => h$1('span', { className: 'em-home-tag', key: tag }, tag))),
                  h$1('div', { className: 'em-home-enter' }, entry.action, h$1('span', { 'aria-hidden': true }, '→')))))),
            h$1('footer', { className: 'em-home-foot' },
              h$1('span', null, h$1('strong', null, '运行总览：'), '请从具体服务器 Dashboard 进入，以自动携带 dashboard_uid。'),
              h$1('span', null, '代码解析与运维 Agent 需要 Editor 或更高权限。'))));
      }

      const collectProxyURL = '/api/plugin-proxy/erlang-monitor-controls-app/collect';
      const statusProxyURL = '/api/plugin-proxy/erlang-monitor-controls-app/status';
      const prometheusProxyURL = '/api/datasources/proxy/uid/prometheus/api/v1/query';
      const runButtonSelector = '[data-testid$="RefreshPicker run button"]';
      const intervalButtonSelector = '[data-testid$="RefreshPicker interval button"]';
      const collectionPollIntervalMs = 1_000;
      const controlPollIntervalMs = 1_000;
      const collectionWaitTimeoutMs = 120_000;
      const prometheusWaitTimeoutMs = 75_000;
      const contexts = new Map();
      const contextRequests = new Map();
      const memoryDeadlines = new Map();
      const collectionGenerations = new Map();
      let refreshMenuVisibleUntil = 0;
      let normalizedAutoURL = '';
      let overviewRedirectTarget = '';
      let queryRefreshOnly = false;
      let anonymousNavigation = null;

      const h = React.createElement;

      function opsAgentURL(uid, server) {
        const source = new URLSearchParams(window.location.search);
        const target = new URLSearchParams({
          dashboard_uid: uid,
          server,
          from: source.get('from') || 'now-1h',
          to: source.get('to') || 'now',
        });
        const node = source.get('var-node');
        if (node && node !== 'All' && node !== '$__all') {
          target.set('node', node);
        }
        return `/a/erlang-monitor-controls-app/ops-agent?${target.toString()}`;
      }

      async function ensureOpsAgentLauncher() {
        const uid = currentUID();
        let launcher = document.getElementById('erlang-ops-agent-launcher');
        if (!uid) {
          launcher?.remove();
          return;
        }
        const context = await loadContext(uid);
        if (!context.server || uid !== currentUID()) {
          return;
        }
        if (!launcher) {
          launcher = document.createElement('a');
          launcher.id = 'erlang-ops-agent-launcher';
          launcher.textContent = '运维 Agent';
          Object.assign(launcher.style, {
            position: 'fixed', right: '24px', bottom: '22px', zIndex: '1000', padding: '10px 16px',
            borderRadius: '8px', background: '#1f60c4', color: 'white', textDecoration: 'none',
            boxShadow: '0 5px 18px rgba(0,0,0,.32)', fontWeight: '600',
          });
          launcher.title = '登录后使用 AI 辅助根因分析';
          document.body.appendChild(launcher);
        }
        launcher.href = opsAgentURL(uid, context.server);
      }

      function redirectOverviewEntry() {
        const target = overviewEntryURL(window.location.pathname, window.location.search);
        if (!target) {
          overviewRedirectTarget = '';
          return false;
        }
        if (overviewRedirectTarget === target) {
          return true;
        }
        overviewRedirectTarget = target;
        locationService.push(target);
        return true;
      }

      function applyHomePresentation() {
        const tracked = [...document.querySelectorAll('[data-erlang-monitor-home-hidden],[data-erlang-monitor-home-dashboard],[data-erlang-monitor-home-layout]')];
        if (window.location.pathname !== '/') {
          for (const item of tracked) {
            syncBooleanAttribute(item, 'data-erlang-monitor-home-hidden', false);
            syncBooleanAttribute(item, 'data-erlang-monitor-home-dashboard', false);
            syncBooleanAttribute(item, 'data-erlang-monitor-home-layout', false);
          }
          return;
        }
        const main = document.querySelector('main');
        const sections = main ? [...main.querySelectorAll('section')].filter((section) => section.querySelector('h1,h2')) : [];
        const dashboardSection = sections.find((section) =>
          [...section.querySelectorAll('h2')].some((heading) => heading.textContent?.trim() === 'Dashboards')
        );
        if (!dashboardSection) {
          return;
        }
        const dashboardGridItem = dashboardSection.closest('.react-grid-item') || dashboardSection;
        const presentationTargets = homeGridPresentationTargets(dashboardGridItem, main);
        const hiddenGridItems = new Set(presentationTargets.hiddenGridItems);
        const homeLayouts = new Set(presentationTargets.homeLayouts);
        for (const section of sections) {
          const gridItem = section.closest('.react-grid-item') || section;
          if (section !== dashboardSection) {
            hiddenGridItems.add(gridItem);
          }
        }
        const candidates = new Set([...tracked, dashboardGridItem, ...hiddenGridItems, ...homeLayouts]);
        for (const item of candidates) {
          syncBooleanAttribute(item, 'data-erlang-monitor-home-hidden', hiddenGridItems.has(item));
          syncBooleanAttribute(item, 'data-erlang-monitor-home-dashboard', item === dashboardGridItem);
          syncBooleanAttribute(item, 'data-erlang-monitor-home-layout', homeLayouts.has(item));
        }
      }

      function installMonitorNavigation() {
        if (window.__erlangMonitorNavigationInstalled) {
          return;
        }
        window.__erlangMonitorNavigationInstalled = true;

        const style = document.createElement('style');
        style.id = 'erlang-monitor-navigation-style';
        style.textContent = `/* erlang-monitor-controls 1.3.5 */
    [data-erlang-monitor-nav-hidden="true"]{display:none!important}
    ${homePresentationCSS}`;
        document.head.appendChild(style);

        const apply = () => {
          applyHomePresentation();
          if (anonymousNavigation === null) {
            return;
          }
          const dashboardLink = [...document.querySelectorAll('nav a[href="/dashboards"]')].find((link) => {
            const navigation = link.closest('nav');
            return navigation?.querySelector('a[href="/alerting"],a[href="/bookmarks"],a[href^="/dashboards?starred"]');
          });
          const dashboardItem = dashboardLink?.closest('li');
          const menu = dashboardItem?.parentElement;
          if (!dashboardItem || !menu) {
            return;
          }
          for (const item of menu.children) {
            const link = item.querySelector('a[href]');
            const href = link?.getAttribute('href');
            const hidden = anonymousNavigation
              ? item !== dashboardItem && shouldHideGuestNavigation(href)
              : shouldHideUnusedNavigation(href);
            if (hidden) {
              item.setAttribute('data-erlang-monitor-nav-hidden', 'true');
            } else {
              item.removeAttribute('data-erlang-monitor-nav-hidden');
            }
          }
        };

        const bootUser = config?.bootData?.user;
        if (typeof bootUser?.isSignedIn === 'boolean') {
          anonymousNavigation = isAnonymousGrafanaUser(bootUser);
        }
        apply();
        new MutationObserver(apply).observe(document.body, { childList: true, subtree: true });
        void getBackendSrv().get('/api/user').then(() => {
          anonymousNavigation = false;
          apply();
        }).catch((error) => {
          // A 401 is Grafana's normal anonymous-viewer response. Other failures
          // preserve the full navigation so operator controls are never hidden.
          anonymousNavigation = isAnonymousGrafanaStatus(error?.status);
          apply();
        });
      }

      function currentUID() {
        return dashboardUID(window.location.pathname);
      }

      async function loadContext(uid) {
        if (!uid) {
          return { uid: '', server: '' };
        }
        if (contexts.has(uid)) {
          return contexts.get(uid);
        }
        if (!contextRequests.has(uid)) {
          const request = getBackendSrv()
            .get(`/api/dashboards/uid/${encodeURIComponent(uid)}`)
            .then((payload) => {
              const context = { uid, server: dashboardServer(payload) };
              contexts.set(uid, context);
              return context;
            })
            .catch((error) => {
              console.error('Erlang monitor controls could not resolve the dashboard server', error);
              return { uid, server: '' };
            })
            .finally(() => contextRequests.delete(uid));
          contextRequests.set(uid, request);
        }
        return contextRequests.get(uid);
      }

      function sessionStorageOrNull() {
        try {
          return window.sessionStorage;
        } catch {
          return null;
        }
      }

      function remainingFor(server) {
        return cooldownRemaining(sessionStorageOrNull(), server, Date.now(), memoryDeadlines.get(server) || 0);
      }

      function delay(milliseconds) {
        return new Promise((resolve) => window.setTimeout(resolve, milliseconds));
      }

      async function waitForValue(load, accept, timeoutMs, isCurrent) {
        const deadline = Date.now() + timeoutMs;
        while (isCurrent() && Date.now() < deadline) {
          try {
            const value = await load();
            if (accept(value)) {
              return value;
            }
          } catch {
            // Status and Prometheus can be briefly unavailable during a local
            // service reload. Retry until the bounded deadline.
          }
          await delay(collectionPollIntervalMs);
        }
        return null;
      }

      function refreshGrafanaQueries(uid) {
        if (uid !== currentUID()) {
          return;
        }
        const button = document.querySelector(runButtonSelector);
        if (!button || button.disabled) {
          return;
        }
        queryRefreshOnly = true;
        try {
          button.click();
        } finally {
          queryRefreshOnly = false;
        }
      }

      function setButtonCooldown(button, remaining) {
        if (remaining > 0) {
          button.disabled = true;
          button.setAttribute('aria-disabled', 'true');
          button.dataset.erlangMonitorCooldown = 'true';
          button.title = `${Math.ceil(remaining / 1000)} 秒后可再次采集`;
          return;
        }
        if (button.dataset.erlangMonitorCooldown === 'true') {
          button.disabled = false;
          button.removeAttribute('aria-disabled');
          button.removeAttribute('data-erlang-monitor-cooldown');
          button.removeAttribute('title');
        }
      }

      function hideDynamicAutoOption() {
        if (Date.now() > refreshMenuVisibleUntil) {
          return;
        }
        for (const item of document.querySelectorAll('[role="menuitemradio"]')) {
          if (item.textContent?.trim() === 'Auto') {
            item.hidden = true;
            item.setAttribute('aria-hidden', 'true');
          }
        }
      }

      async function refreshControlState() {
        if (redirectOverviewEntry()) {
          return;
        }
        const uid = currentUID();
        if (!uid) {
          return;
        }
        const context = await loadContext(uid);
        if (!context.server || uid !== currentUID()) {
          return;
        }

        const button = document.querySelector(runButtonSelector);
        if (button) {
          setButtonCooldown(button, remainingFor(context.server));
        }
        hideDynamicAutoOption();
        void ensureOpsAgentLauncher();

        if (isDynamicAutoRefresh(window.location.search) && normalizedAutoURL !== window.location.href) {
          normalizedAutoURL = window.location.href;
          locationService.partial({ refresh: '30m' }, true);
        }
      }

      async function triggerCollection(uid) {
        const context = await loadContext(uid);
        if (!context.server || uid !== currentUID()) {
          return;
        }
        if (remainingFor(context.server) > 0) {
          return;
        }

        const deadline = beginCooldown(sessionStorageOrNull(), context.server);
        memoryDeadlines.set(context.server, deadline);
        const generation = (collectionGenerations.get(context.server) || 0) + 1;
        collectionGenerations.set(context.server, generation);
        const isCurrent = () => collectionGenerations.get(context.server) === generation && uid === currentUID();
        await refreshControlState();

        try {
          const requestedAt = Date.now();
          const statusBefore = await getBackendSrv().get(statusProxyURL).catch(() => null);
          const response = await getBackendSrv().post(collectProxyURL, { server: context.server });
          const serverID = String(response?.server || '');
          if (!serverID || !isCurrent()) {
            return;
          }

          const baselineAttempt = serverLastAttemptMs(statusBefore, serverID) || requestedAt;
          const completedStatus = await waitForValue(
            () => getBackendSrv().get(statusProxyURL),
            (status) => serverLastAttemptMs(status, serverID) > baselineAttempt,
            collectionWaitTimeoutMs,
            isCurrent
          );
          if (!completedStatus || !isCurrent()) {
            console.error(`Timed out waiting for Erlang collection to finish for ${context.server}`);
            return;
          }

          const completedAt = serverLastAttemptMs(completedStatus, serverID);
          const query = `timestamp(erlang_exporter_server_up{name="${prometheusLabelValue(context.server)}"})`;
          const queryURL = `${prometheusProxyURL}?query=${encodeURIComponent(query)}`;
          const scraped = await waitForValue(
            () => getBackendSrv().get(queryURL),
            (payload) => latestPrometheusSampleMs(payload) >= completedAt,
            prometheusWaitTimeoutMs,
            isCurrent
          );
          if (!scraped || !isCurrent()) {
            console.error(`Timed out waiting for Prometheus to scrape ${context.server}`);
            return;
          }

          const remaining = remainingFor(context.server);
          if (remaining > 0) {
            await delay(remaining + 50);
          }
          if (isCurrent()) {
            refreshGrafanaQueries(uid);
          }
        } catch (error) {
          console.error(`Erlang collection/refresh flow failed for ${context.server}`, error);
        }
      }

      function handleDocumentClick(event) {
        const target = event.target instanceof Element ? event.target : null;
        if (!target) {
          return;
        }

        const intervalButton = target.closest(intervalButtonSelector);
        if (intervalButton) {
          refreshMenuVisibleUntil = Date.now() + 3_000;
          window.setTimeout(hideDynamicAutoOption, 0);
          return;
        }

        const runButton = target.closest(runButtonSelector);
        if (!runButton) {
          return;
        }
        if (queryRefreshOnly) {
          return;
        }
        const uid = currentUID();
        const context = contexts.get(uid);
        if (context?.server && remainingFor(context.server) > 0) {
          event.preventDefault();
          event.stopImmediatePropagation();
          setButtonCooldown(runButton, remainingFor(context.server));
          return;
        }

        // Let Grafana perform its normal Prometheus refresh. Collection is queued in
        // parallel and the ten-second UI cooldown is scoped to this dashboard IP.
        void triggerCollection(uid);
      }

      function installControls() {
        if (window.__erlangMonitorControlsInstalled) {
          return;
        }
        window.__erlangMonitorControlsInstalled = true;
        document.addEventListener('click', handleDocumentClick, true);
        window.setInterval(() => {
          if (shouldPollControlState(window.location.pathname)) {
            void refreshControlState();
          }
        }, controlPollIntervalMs);
        installMonitorNavigation();
        if (redirectOverviewEntry()) {
          return;
        }
        void refreshControlState();
      }

      if (typeof window !== 'undefined' && typeof document !== 'undefined') {
        installControls();
      }

      function RootPage() {
        if (window.location.pathname.endsWith('/overview')) {
          return h(OverviewPage);
        }
        if (window.location.pathname.endsWith('/ops-agent')) {
          return h(OpsPage);
        }
        if (window.location.pathname.endsWith('/code-analysis')) {
          return h(CodePage);
        }
        // The plugin root and unknown legacy routes land on a context-free app home.
        return h(HomePage);
      }

      const plugin = exports("plugin", new AppPlugin().setRootPage(RootPage));

    })
  };
}));
