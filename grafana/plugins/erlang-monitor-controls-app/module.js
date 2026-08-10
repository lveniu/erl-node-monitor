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

      const h$2 = React.createElement;
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
        return h$2('article', { className: 'eo-gauge' },
          h$2('div', { className: `eo-ring ${colorClass}`, style: { '--value': bounded }, role: 'progressbar', 'aria-label': `${label} ${fixed(value)}，上限 ${fixed(ariaMax, 0)}`, 'aria-valuemin': 0, 'aria-valuemax': ariaMax || 100, 'aria-valuenow': Number(ariaValue) || 0 },
            h$2('div', { className: 'eo-ring-value' }, `${fixed(value)}%`, h$2('small', null, label))),
          h$2('div', null, h$2('div', { className: 'eo-gauge-title' }, label), h$2('div', { className: 'eo-gauge-main' }, detail), h$2('div', { className: 'eo-gauge-sub' }, subdetail)));
      }

      function NodeConnections({ sourceNode, available, connections = [] }) {
        if (isMNodeInfrastructureNode(sourceNode)) return h$2('span', { className: 'eo-connection-empty', title: '中央/赛区等非游戏节点不展示连接关系' }, '—');
        if (Number(available) !== 1) return h$2('span', { className: 'eo-connection-empty' }, '待采集');
        if (!connections.length) return h$2('span', { className: 'eo-connection-empty' }, '未连接');
        return h$2('div', { className: 'eo-connection-list' }, connections.map((connection) => {
          const kind = connection.type === 'central' ? 'C8' : 'C9';
          const shortName = String(connection.node || '').split('@')[0];
          return h$2('span', {
            className: `eo-connection ${connection.type === 'region' ? 'region' : ''} ${connection.usable ? '' : 'unusable'}`.trim(),
            key: `${connection.type}:${connection.nodeID}:${connection.node}`,
            title: `${connection.node || '未知节点'} · state=${connection.state ?? '未知'}`,
          }, h$2('b', null, kind), h$2('span', null, `${connection.nodeID}${shortName ? ` · ${shortName}` : ''}`));
        }));
      }

      function NodeTable({ nodes }) {
        const [expanded, setExpanded] = React.useState(false);
        const canExpand = nodes.length > 6;
        const visible = expanded ? nodes : nodes.slice(0, 6);
        const hiddenOnline = nodes.slice(6).filter((node) => node.up === 1).length;
        return h$2('section', { className: 'eo-panel' },
          h$2('div', { className: 'eo-head' }, h$2('span', null, 'Erlang 节点状态'), h$2('span', { className: 'eo-ok' }, `${nodes.filter((node) => node.up === 1).length} / ${nodes.length} 在线`)),
          h$2('div', { className: 'eo-table-wrap' },
            h$2('table', { className: 'eo-table' },
              h$2('thead', null, h$2('tr', null,
                h$2('th', { className: 'eo-node' }, '节点'), h$2('th', { className: 'eo-status' }, '状态'),
                h$2('th', { className: 'eo-process' }, '进程总数'), h$2('th', { className: 'eo-resource', title: 'BEAM进程常驻内存（RSS）' }, '内存（G）'),
                h$2('th', { className: 'eo-resource', title: 'BEAM进程CPU，100%约等于一个逻辑核' }, 'CPU比例'), h$2('th', { className: 'eo-connections' }, '节点连接'),
                h$2('th', { className: 'eo-pending' }, '注册人数'), h$2('th', { className: 'eo-pending' }, '在线人数'))),
              h$2('tbody', null,
                visible.map((node) => h$2('tr', { key: node.node },
                  h$2('td', { className: 'eo-node' }, node.node),
                  h$2('td', { className: 'eo-status' }, h$2('span', { className: `eo-node-status ${node.up === 1 ? 'up' : 'down'}` }, node.up === 1 ? '在线' : '离线')),
                  h$2('td', { className: 'eo-process' }, formatNumber(node.processes)),
                  h$2('td', { className: 'eo-resource' }, formatResidentMemory(node.residentMemoryBytes)),
                  h$2('td', { className: 'eo-resource' }, formatCPURatio(node.cpuRatio)),
                  h$2('td', { className: 'eo-connections' }, h$2(NodeConnections, { sourceNode: node.node, available: node.mnodeAvailable, connections: node.connections })),
                  h$2('td', { className: 'eo-pending' }, formatNumber(node.registered)),
                  h$2('td', { className: 'eo-pending' }, formatNumber(node.online)))),
                canExpand ? h$2('tr', { className: 'eo-more' }, h$2('td', { colSpan: 8 }, h$2('button', {
                  className: 'eo-more-button', type: 'button', 'aria-expanded': expanded, onClick: () => setExpanded((value) => !value),
                }, expanded ? `收起节点列表 · 共 ${nodes.length} 个节点` : `其余 ${nodes.length - 6} 个节点：${hiddenOnline} 在线`, h$2('span', { className: 'eo-more-arrow', 'aria-hidden': true }, expanded ? '▲' : '▼')))) : null))));
      }

      function AlertList({ alerts, dashboardUID, server }) {
        const source = new URLSearchParams(window.location.search);
        const opsAgentBase = new URLSearchParams({ dashboard_uid: dashboardUID, server, from: source.get('from') || 'now-6h', to: source.get('to') || 'now' });
        return h$2('section', { className: 'eo-panel' },
          h$2('div', { className: 'eo-head' }, h$2('span', null, '异常详情'), h$2('span', { className: alerts.length ? 'eo-warn' : 'eo-ok' }, alerts.length ? `${alerts.length} 条告警` : '当前无异常')),
          alerts.length ? alerts.map((alert, index) => {
            const node = alert.labels.node || '';
            const params = new URLSearchParams(opsAgentBase);
            if (node) params.set('node', node);
            return h$2('details', { className: 'eo-alert', key: alert.fingerprint, open: index === 0 },
              h$2('summary', { className: 'eo-alert-summary' },
                h$2('span', { className: 'eo-alert-icon' }, '!'),
                h$2('span', { className: 'eo-alert-title' }, h$2('strong', null, alert.annotations.summary || alert.labels.alertname || '监控告警'), h$2('small', null, [node, alert.labels.registered_name].filter(Boolean).join(' · ') || alert.labels.name)),
                h$2('span', { className: `eo-severity ${alert.labels.severity || ''}` }, alert.labels.severity || alert.state),
                h$2('span', { className: 'eo-alert-time' }, formatDate(alert.activeAt)), h$2('span', null, '⌄')),
              h$2('div', { className: 'eo-alert-body' },
                h$2('div', { className: 'eo-alert-line' }, h$2('div', { className: 'eo-alert-label' }, '当前值'), h$2('div', { className: 'eo-alert-value eo-current' }, alert.annotations.value || fixed(alert.value))),
                h$2('div', { className: 'eo-alert-line' }, h$2('div', { className: 'eo-alert-label' }, '触发条件'), h$2('div', { className: 'eo-alert-value' }, alert.annotations.condition || '由 Prometheus 告警规则触发')),
                h$2('div', { className: 'eo-alert-line' }, h$2('div', { className: 'eo-alert-label' }, '影响'), h$2('div', { className: 'eo-alert-value' }, alert.annotations.impact || '请结合节点状态判断影响范围')),
                h$2('div', { className: 'eo-alert-line' }, h$2('div', { className: 'eo-alert-label' }, '建议处理'), h$2('div', { className: 'eo-alert-value' }, alert.annotations.action || '进入运维 Agent 分析或按运维流程处理')),
                h$2('div', { className: 'eo-alert-line' }, h$2('div', { className: 'eo-alert-label' }, '标签'), h$2('div', { className: 'eo-alert-value' }, alertLabelText(alert.labels))),
                h$2('div', { className: 'eo-alert-line' }, h$2('div', { className: 'eo-alert-label' }, '辅助分析'), h$2('a', { className: 'eo-agent', href: `/a/erlang-monitor-controls-app/ops-agent?${params.toString()}` }, '打开运维 Agent'))));
          }) : h$2('div', { className: 'eo-empty' }, '当前没有 firing 或 pending 告警'));
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

        return h$2('main', { className: 'erlang-overview-page' },
          h$2('style', null, overviewCSS),
          h$2('div', { className: 'eo-toolbar' },
            h$2('div', { className: 'eo-title' }, h$2('h1', null, context.title || 'Erlang 运行总览'), h$2('small', null, context.server || '正在解析服务器')),
            h$2('div', { className: 'eo-controls' },
              dashboards.length ? h$2('select', { className: 'eo-control eo-select', value: context.uid, 'aria-label': '目录-页面', onChange: (event) => locationService.push(`/a/erlang-monitor-controls-app/overview?dashboard_uid=${encodeURIComponent(event.target.value)}&kiosk`) }, dashboards.map((item) => h$2('option', { key: item.uid, value: item.uid }, item.title))) : null,
              h$2('button', { className: 'eo-control', type: 'button', disabled: busy || !context.server, onClick: collectNow }, busy ? '正在采集…' : '刷新采集'),
              h$2('span', { className: 'eo-control' }, '自动 30m'))),
          error ? h$2('div', { className: 'eo-error', role: 'alert' }, safeMarkdownText(error, 1000)) : null,
          h$2('section', { className: 'eo-health' },
            h$2('div', { className: 'eo-health-main' }, h$2('span', { className: `eo-health-icon ${alertCount ? 'warn' : 'ok'}` }, alertCount ? '!' : '✓'), h$2('div', null, h$2('strong', null, alertCount ? `需要关注 · ${alertCount} 条告警` : '运行正常'), h$2('small', null, `${snapshot.nodes.filter((node) => node.up === 1).length}/${snapshot.nodes.length} 节点在线`))),
            h$2('div', { className: 'eo-time' }, '最近采集 ', h$2('strong', null, snapshot.lastSuccess ? new Date(snapshot.lastSuccess * 1000).toLocaleString('zh-CN', { hour12: false }) : '无数据'))),
          h$2('section', { className: 'eo-panel' }, h$2('div', { className: 'eo-head' }, h$2('span', null, '资源水位'), h$2('span', { className: 'eo-ok' }, '主机指标')),
            h$2('div', { className: 'eo-gauges' },
              h$2(MetricRing, { label: 'CPU', value: cpuCurrent, detail: `上限 ${fixed(cpuCapacity, 0)}%`, subdetail: `${fixed(snapshot.cpuLogical, 0)} 逻辑核`, ariaMax: cpuCapacity, ariaValue: cpuCurrent, fill: cpuFill }),
              h$2(MetricRing, { label: '内存', value: memoryPercent, detail: `${fixed(usedG)} / ${fixed(totalG)} G`, subdetail: `可用 ${fixed(availableG)} G`, colorClass: 'memory' }),
              h$2(MetricRing, { label: '硬盘', value: diskPercent, detail: `${fixed(diskUsedG)} / ${fixed(diskTotalG)} G`, subdetail: `可用 ${fixed(diskAvailableG)} G`, colorClass: 'disk' }))),
          h$2(NodeTable, { key: context.server, nodes: snapshot.nodes }),
          h$2(AlertList, { alerts: snapshot.alerts, dashboardUID: context.uid, server: context.server }));
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

      const h$1 = React.createElement;
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
        const approval = pending ? h$1('div', { className: 'ops-approval' },
          h$1('strong', null, '需要 Admin 批准 Shell'),
          h$1('div', { style: { color: '#f4cf95', fontSize: '12px', marginTop: '6px' } }, pending.target, ' · ', pending.reason),
          h$1('code', null, pending.command),
          h$1('div', { className: 'ops-actions' },
            h$1('button', { className: 'ops-button', disabled: busy, onClick: () => decide(true) }, '批准执行'),
            h$1('button', { className: 'ops-button danger', disabled: busy, onClick: () => decide(false) }, '拒绝'))) : null;
        const timeline = task?.events?.length ? h$1('div', { className: 'ops-timeline' }, task.events.map((event) =>
          h$1('article', { className: 'ops-event', key: event.id },
            h$1('div', { className: 'ops-event-head' }, h$1('span', null, event.type), h$1('time', null, new Date(event.at).toLocaleTimeString('zh-CN', { hour12: false }))),
            h$1('pre', null, safeMarkdownText(eventText(event), 16000))))) : h$1('div', { className: 'ops-empty' }, '任务启动后，这里会显示 Skill、Shell 和验证步骤。');
        return h$1('main', { className: 'ops-page' }, h$1('style', null, css),
          h$1('header', { className: 'ops-head' },
            h$1('div', null, h$1('div', { className: 'ops-kicker' }, 'ERLANG / OPERATIONS AGENT'), h$1('h1', { className: 'ops-title' }, '一次任务，完成分析与处理'), h$1('div', { className: 'ops-sub' }, '模型只负责判断和提出动作；服务器边界、Skill、Shell 审批和结果验证由平台控制。')),
            h$1('div', { className: 'ops-chip' }, task ? statusText(task.status) : '待命')),
          h$1('div', { className: 'ops-grid' },
            h$1('section', { className: 'ops-panel' },
              h$1('h2', null, '目标上下文'),
              h$1('div', { className: 'ops-meta' },
                h$1('div', null,
                  h$1('label', { className: 'ops-field-label', htmlFor: 'ops-server-select' }, '内网节点（单选）'),
                  h$1('select', { id: 'ops-server-select', className: 'ops-select', value: server.id, disabled: Boolean(task) || catalogLoading, onChange: selectServer },
                    h$1('option', { value: '' }, catalogLoading ? '正在读取节点清单…' : '请选择一个内网节点'),
                    servers.map((item) => h$1('option', { key: item.id, value: item.id }, `${item.name} · ${item.id}`)))),
                h$1('div', null, h$1('span', null, 'Stable ID'), h$1('strong', null, server.id || '—')),
                h$1('div', null, h$1('span', null, 'Node'), h$1('strong', null, node || '未固定')),
                h$1('div', null, h$1('span', null, 'Time window'), h$1('strong', null, `${query.get('from') || 'now-1h'} → ${query.get('to') || 'now'}`))),
              h$1('div', { className: 'ops-notice', style: { marginTop: '16px' } }, '只允许在已选 192.168.100.* 内网节点上按已加载 Skill 的职责执行。纯 ls / grep / ps / cd / head / tail / df / find 只读组合自动执行；其他允许的 Shell 逐条等待 Grafana Admin 审批。Agent 不保存长期记忆。')),
            h$1('div', { className: 'ops-stack' },
              h$1('section', { className: 'ops-panel' },
                h$1('h2', null, '执行轨迹'), timeline,
                task?.final_answer ? h$1('div', { style: { marginTop: '18px' } }, h$1('h2', null, '最终结果'), h$1('div', { className: 'ops-answer' }, safeMarkdownText(task.final_answer, 32000))) : null),
              h$1('section', { className: 'ops-panel' },
                h$1('h2', null, '任务输入'),
                h$1('textarea', { className: 'ops-textarea', value: question, disabled: Boolean(task), onChange: (event) => setQuestion(event.target.value), maxLength: 8000 }),
                h$1('div', { className: 'ops-actions' },
                  !task ? h$1('button', { className: 'ops-button', disabled: busy || !server.id, onClick: start }, busy ? '正在启动…' : '开始运维任务') : null,
                  task && ['completed', 'failed'].includes(task.status) ? h$1('button', { className: 'ops-button secondary', onClick: () => { persistTaskID(''); setTask(null); setError(''); } }, '新建任务') : null),
                error ? h$1('div', { className: 'ops-error', style: { marginTop: '12px' } }, safeMarkdownText(error, 1000)) : null,
                approval)),
            h$1('aside', { className: 'ops-panel ops-skills-panel' },
              h$1('h2', null, '可用 Skill'),
              h$1('div', { className: 'ops-skill-count' }, catalogLoading ? '正在读取…' : `${skills.length} 个专项流程`),
              skills.length ? h$1('div', { className: 'ops-skill-list' }, skills.map((skill) =>
                h$1('article', { className: 'ops-skill', key: skill.name },
                  h$1('code', null, skill.name),
                  h$1('p', null, safeMarkdownText(skill.description, 1000))))) : h$1('div', { className: 'ops-empty' }, catalogLoading ? '正在加载 Skill 清单。' : '当前没有可用 Skill。'))));
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
        // Unknown and legacy routes fall back to the read-only overview.
        return h(OverviewPage);
      }

      const plugin = exports("plugin", new AppPlugin().setRootPage(RootPage));

    })
  };
}));
