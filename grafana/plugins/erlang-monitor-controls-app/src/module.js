import { AppPlugin } from '@grafana/data';
import { config, getBackendSrv, locationService } from '@grafana/runtime';
import React from 'react';

import {
  beginCooldown,
  cooldownRemaining,
  dashboardServer,
  dashboardUID,
  isAnonymousGrafanaStatus,
  isAnonymousGrafanaUser,
  isDynamicAutoRefresh,
  homeGridPresentationTargets,
  homePresentationCSS,
  latestPrometheusSampleMs,
  overviewEntryURL,
  prometheusLabelValue,
  serverLastAttemptMs,
  shouldHideGuestNavigation,
  shouldHideUnusedNavigation,
  shouldPollControlState,
  syncBooleanAttribute,
} from './control-logic.js';
import {
  activeAlertContext,
  approvalPayload,
  boundedRange,
  formatBytes,
  createReloadScheduler,
  prometheusSamples,
  publicModels,
  holmesProxyRequestURL,
  investigationRequestError,
  markdownBlocks,
  releaseRequestLock,
  safeMarkdownText,
  startSerialPoll,
  statusLabel,
  toolSummary,
  tryAcquireRequestLock,
} from './holmes-logic.js';
import { OverviewPage } from './overview-page.js';
import { OpsPage } from './ops-page.js';

const collectProxyURL = '/api/plugin-proxy/erlang-monitor-controls-app/collect';
const statusProxyURL = '/api/plugin-proxy/erlang-monitor-controls-app/status';
const prometheusProxyURL = '/api/datasources/proxy/uid/prometheus/api/v1/query';
const holmesProxyURL = '/api/plugin-proxy/erlang-monitor-controls-app/holmes';
const holmesAdminProxyURL = '/api/plugin-proxy/erlang-monitor-controls-app/holmes-admin';
const runButtonSelector = '[data-testid$="RefreshPicker run button"]';
const intervalButtonSelector = '[data-testid$="RefreshPicker interval button"]';
const collectionPollIntervalMs = 1_000;
const controlPollIntervalMs = 1_000;
const investigationPollIntervalMs = 2_000;
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

function requestID() {
  if (typeof crypto?.randomUUID === 'function') {
    return crypto.randomUUID();
  }
  return `request-${Date.now()}-${Math.random().toString(16).slice(2)}`;
}

function currentDashboardRange() {
  const query = new URLSearchParams(window.location.search);
  return boundedRange(query.get('from') || 'now-1h', query.get('to') || 'now');
}

function workbenchURL(uid, server) {
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

async function ensureHolmesLauncher() {
  const uid = currentUID();
  let launcher = document.getElementById('erlang-holmes-launcher');
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
    launcher.id = 'erlang-holmes-launcher';
    launcher.textContent = '运维 Agent';
    Object.assign(launcher.style, {
      position: 'fixed', right: '24px', bottom: '22px', zIndex: '1000', padding: '10px 16px',
      borderRadius: '8px', background: '#1f60c4', color: 'white', textDecoration: 'none',
      boxShadow: '0 5px 18px rgba(0,0,0,.32)', fontWeight: '600',
    });
    launcher.title = '登录后使用 AI 辅助根因分析';
    document.body.appendChild(launcher);
  }
  launcher.href = workbenchURL(uid, context.server);
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
  void ensureHolmesLauncher();

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

const styles = {
  page: { padding: '20px', maxWidth: '1600px', margin: '0 auto', color: 'var(--text-primary, #d9d9d9)' },
  header: { display: 'flex', justifyContent: 'space-between', alignItems: 'center', gap: '16px', marginBottom: '16px', flexWrap: 'wrap' },
  title: { margin: 0, fontSize: '24px', fontWeight: 650 },
  badge: { display: 'inline-flex', padding: '5px 10px', borderRadius: '999px', background: '#1f60c4', color: '#fff', fontSize: '12px' },
  grid: { display: 'grid', gridTemplateColumns: 'minmax(260px, 0.85fr) minmax(420px, 1.65fr)', gap: '16px' },
  card: { background: 'var(--background-secondary, #181b1f)', border: '1px solid var(--border-weak, #34373d)', borderRadius: '10px', padding: '16px', minWidth: 0 },
  section: { marginTop: 0, marginBottom: '12px', fontSize: '16px' },
  label: { display: 'block', fontSize: '12px', opacity: 0.72, marginBottom: '4px' },
  value: { overflowWrap: 'anywhere', marginBottom: '14px' },
  input: { width: '100%', boxSizing: 'border-box', borderRadius: '6px', border: '1px solid #58606e', background: '#101216', color: '#f4f5f6', padding: '9px 10px' },
  textarea: { width: '100%', minHeight: '96px', boxSizing: 'border-box', borderRadius: '6px', border: '1px solid #58606e', background: '#101216', color: '#f4f5f6', padding: '10px', resize: 'vertical' },
  button: { border: 0, borderRadius: '6px', padding: '9px 14px', background: '#1f60c4', color: '#fff', cursor: 'pointer', fontWeight: 600 },
  dangerButton: { border: '1px solid #d34a4a', borderRadius: '6px', padding: '8px 13px', background: 'transparent', color: '#ff7777', cursor: 'pointer' },
  mutedButton: { border: '1px solid #6a707c', borderRadius: '6px', padding: '8px 13px', background: 'transparent', color: 'inherit', cursor: 'pointer' },
  row: { display: 'flex', gap: '8px', alignItems: 'center', flexWrap: 'wrap' },
  event: { borderLeft: '3px solid #5794f2', padding: '8px 10px', marginBottom: '8px', background: 'rgba(87,148,242,.08)' },
  evidence: { whiteSpace: 'pre-wrap', overflowWrap: 'anywhere', maxHeight: '260px', overflow: 'auto', fontSize: '12px', background: '#0d0f12', padding: '10px', borderRadius: '6px' },
  answer: { whiteSpace: 'pre-wrap', overflowWrap: 'anywhere', lineHeight: 1.6, background: '#101317', borderRadius: '8px', padding: '14px' },
  error: { color: '#ff7b7b', background: 'rgba(200,40,40,.12)', border: '1px solid rgba(255,80,80,.35)', borderRadius: '6px', padding: '10px', marginBottom: '12px' },
  notice: { color: '#f2c96d', fontSize: '12px', marginTop: '16px' },
};

function SummaryField({ label, value }) {
  return h('div', null, h('span', { style: styles.label }, label), h('div', { style: styles.value }, value || '未指定'));
}

function markdownInline(text, keyPrefix) {
  const parts = [];
  const pattern = /(\*\*[^*]+\*\*|`[^`]+`)/g;
  let offset = 0;
  let match;
  while ((match = pattern.exec(String(text || ''))) !== null) {
    if (match.index > offset) parts.push(String(text).slice(offset, match.index));
    const token = match[0];
    if (token.startsWith('**')) {
      parts.push(h('strong', { key: `${keyPrefix}-${match.index}` }, token.slice(2, -2)));
    } else {
      parts.push(h('code', { key: `${keyPrefix}-${match.index}`, style: { background: '#252a31', borderRadius: '3px', padding: '1px 4px' } }, token.slice(1, -1)));
    }
    offset = pattern.lastIndex;
  }
  if (offset < String(text || '').length) parts.push(String(text).slice(offset));
  return parts;
}

function MarkdownAnswer({ text }) {
  const blocks = markdownBlocks(text);
  return h('div', { style: { ...styles.answer, whiteSpace: 'normal' } }, blocks.map((block, index) => {
    const key = `markdown-${index}`;
    if (block.type === 'heading') {
      const level = Math.min(6, Math.max(2, block.level + 1));
      return h(`h${level}`, { key, style: { margin: '16px 0 8px' } }, ...markdownInline(block.text, key));
    }
    if (block.type === 'code') {
      return h('pre', { key, style: styles.evidence }, h('code', null, block.text));
    }
    if (block.type === 'list') {
      const tag = block.ordered ? 'ol' : 'ul';
      return h(tag, { key, style: { margin: '8px 0', paddingLeft: '24px' } }, block.items.map((item, itemIndex) => h('li', { key: `${key}-${itemIndex}` }, ...markdownInline(item, `${key}-${itemIndex}`))));
    }
    if (block.type === 'table') {
      return h('div', { key, style: { overflowX: 'auto', margin: '10px 0' } }, h('table', { style: { width: '100%', borderCollapse: 'collapse' } },
        h('thead', null, h('tr', null, block.headers.map((cell, cellIndex) => h('th', { key: `${key}-h-${cellIndex}`, style: { textAlign: 'left', border: '1px solid #3f4650', padding: '6px 8px' } }, ...markdownInline(cell, `${key}-h-${cellIndex}`))))),
        h('tbody', null, block.rows.map((row, rowIndex) => h('tr', { key: `${key}-r-${rowIndex}` }, row.map((cell, cellIndex) => h('td', { key: `${key}-r-${rowIndex}-${cellIndex}`, style: { border: '1px solid #3f4650', padding: '6px 8px', verticalAlign: 'top' } }, ...markdownInline(cell, `${key}-r-${rowIndex}-${cellIndex}`))))))));
    }
    return h('p', { key, style: { margin: '8px 0' } }, ...markdownInline(block.text, key));
  }));
}

function WorkbenchPage() {
  const initial = new URLSearchParams(window.location.search);
  const dashboardUIDValue = initial.get('dashboard_uid') || '';
  const dashboardServerName = initial.get('server') || '';
  const initialNode = initial.get('node') || '';
  const initialSessionID = initial.get('session_id') || '';
  const range = boundedRange(initial.get('from') || 'now-1h', initial.get('to') || 'now');
  const [server, setServer] = React.useState({ id: '', name: dashboardServerName });
  const [models, setModels] = React.useState([]);
  const [model, setModel] = React.useState('');
  const [ask, setAsk] = React.useState('分析当前服务器告警的可能根因');
  const [sessionID, setSessionID] = React.useState(initialSessionID);
  const [session, setSession] = React.useState(null);
  // Reloads can overlap: an older GET must not overwrite a newer terminal
  // snapshot received after the investigation_completed SSE event.
  const sessionReloadSequence = React.useRef(0);
  const followUpRequestInFlight = React.useRef(false);
  const [latestEvent, setLatestEvent] = React.useState('');
  const [error, setError] = React.useState('');
  const [busy, setBusy] = React.useState(false);
  const [overview, setOverview] = React.useState({ nodes: [], cpu: null, memoryTotal: null, memoryAvailable: null, alerts: 0, alertContext: { labels: {}, fingerprint: '' }, queries: [], sampledAt: 0 });

  const reloadSession = React.useCallback(async (id = sessionID) => {
    if (!id) return;
    const sequence = ++sessionReloadSequence.current;
    try {
      const value = await getBackendSrv().get(holmesProxyRequestURL(holmesProxyURL, `/investigations/${encodeURIComponent(id)}`));
      if (sequence !== sessionReloadSequence.current) return;
      setSession(value);
      setError(value?.error?.message || '');
    } catch (requestError) {
      if (sequence !== sessionReloadSequence.current) return;
      setError(requestError?.data?.error?.message || '无法恢复调查会话');
    }
  }, [sessionID]);

  React.useEffect(() => {
    let active = true;
    Promise.all([
      getBackendSrv().get(holmesProxyRequestURL(holmesProxyURL, '/models')),
      dashboardServerName
        ? getBackendSrv().get(holmesProxyRequestURL(holmesProxyURL, '/servers/resolve', { name: dashboardServerName }))
        : Promise.resolve(null),
    ]).then(([modelPayload, serverPayload]) => {
      if (!active) return;
      const allowed = publicModels(modelPayload);
      setModels(allowed);
      setModel((current) => current || allowed[0]?.alias || '');
      if (serverPayload) setServer({ id: serverPayload.server_id, name: serverPayload.display_name });
    }).catch((requestError) => {
      if (active) setError(requestError?.status === 403 ? '登录并取得 Editor 权限后才能使用 Holmes 分析' : '无法读取 Holmes 模型或服务器上下文');
    });
    return () => { active = false; };
  }, [dashboardServerName]);

  React.useEffect(() => {
    if (!server.name) return undefined;
    let active = true;
    const label = prometheusLabelValue(server.name);
    const queries = [
      `erlang_exporter_node_up{name="${label}"}`,
      `erlang_host_cpu_usage_ratio{name="${label}"}`,
      `erlang_host_memory_total_bytes{name="${label}"}`,
      `erlang_host_memory_available_bytes{name="${label}"}`,
      `ALERTS{name="${label}",alertstate="firing"}`,
    ];
    Promise.all(queries.map((query) => getBackendSrv().get(`${prometheusProxyURL}?query=${encodeURIComponent(query)}`)))
      .then((payloads) => {
        if (!active) return;
        const samples = payloads.map(prometheusSamples);
        const timestamps = samples.flat().map((sample) => sample.sampledAt);
	        setOverview({
          nodes: samples[0].map((sample) => ({ name: sample.metric.node, up: sample.value === 1 })),
          cpu: samples[1][0]?.value ?? null,
          memoryTotal: samples[2][0]?.value ?? null,
          memoryAvailable: samples[3][0]?.value ?? null,
          alerts: samples[4].length,
          alertContext: activeAlertContext(samples[4], initialNode),
          queries,
          sampledAt: timestamps.length ? Math.max(...timestamps) : 0,
        });
      }).catch(() => { if (active) setOverview((current) => ({ ...current, unavailable: true })); });
    return () => { active = false; };
  }, [server.name]);

  React.useEffect(() => { if (sessionID) void reloadSession(sessionID); }, [sessionID, reloadSession]);

  React.useEffect(() => {
    if (!sessionID || ['completed', 'failed', 'cancelled'].includes(session?.status)) {
      return undefined;
    }
    // Grafana/Nginx may buffer or interrupt plugin-proxy SSE. Polling is a
    // bounded fallback so terminal state and the final answer still appear
    // without requiring a manual page refresh.
    return startSerialPoll(() => reloadSession(sessionID), investigationPollIntervalMs, window);
  }, [sessionID, session?.status, reloadSession]);

  React.useEffect(() => {
    if (!sessionID || !session || ['completed', 'failed', 'cancelled'].includes(session.status)) return undefined;
    const refresh = createReloadScheduler(() => void reloadSession(sessionID), 50, window);
    const source = new EventSource(holmesProxyRequestURL(holmesProxyURL, `/investigations/${encodeURIComponent(sessionID)}/events`));
    const types = ['investigation_started', 'assistant_message', 'tool_started', 'tool_finished', 'approval_required', 'approval_decided', 'usage_updated', 'compaction_started', 'compaction_completed', 'investigation_completed', 'investigation_failed', 'investigation_cancelled'];
    const onEvent = (event) => {
      setLatestEvent(event.type);
      refresh.queue();
      if (['investigation_completed', 'investigation_failed', 'investigation_cancelled'].includes(event.type)) source.close();
    };
    types.forEach((type) => source.addEventListener(type, onEvent));
    source.onerror = () => { /* EventSource reconnects and sends Last-Event-ID. */ };
    return () => {
      refresh.cancel();
      source.close();
    };
  }, [sessionID, session?.status, reloadSession]);

  async function createInvestigation() {
    if (!server.id || !model || !ask.trim()) {
      setError('服务器、可用模型和问题不能为空');
      return;
    }
    setBusy(true);
    setError('');
    try {
      const response = await getBackendSrv().post(holmesProxyRequestURL(holmesProxyURL, '/investigations'), {
        request_id: requestID(), model, ask: ask.trim(),
        context: {
          server_id: server.id, node: initialNode, dashboard_uid: dashboardUIDValue,
          from: range.from, to: range.to,
          alert_fingerprint: overview.alertContext?.fingerprint || undefined,
          alert_labels: overview.alertContext?.labels || {},
        },
      });
      setSessionID(response.session_id);
      const next = new URL(window.location.href);
      next.searchParams.set('session_id', response.session_id);
      window.history.replaceState({}, '', next);
    } catch (requestError) {
      setError(requestError?.data?.error?.message || '创建调查失败');
    } finally {
      setBusy(false);
    }
  }

  async function sendFollowUp() {
    if (!sessionID || !ask.trim() || !tryAcquireRequestLock(followUpRequestInFlight)) return;
    const followUpRequestID = requestID();
    setBusy(true);
    setError('');
    setLatestEvent('investigation_started');
    setSession((current) => current ? { ...current, status: 'created', running_request_id: followUpRequestID } : current);
    try {
      const response = await getBackendSrv().post(holmesProxyRequestURL(holmesProxyURL, `/investigations/${encodeURIComponent(sessionID)}/messages`), { request_id: followUpRequestID, ask: ask.trim() });
      setAsk('');
      setSession((current) => current ? { ...current, status: response?.status || 'created', running_request_id: followUpRequestID } : current);
      await reloadSession(sessionID);
    } catch (requestError) {
      await reloadSession(sessionID);
      setError(investigationRequestError(requestError, '追问失败'));
    } finally {
      releaseRequestLock(followUpRequestInFlight);
      setBusy(false);
    }
  }

  async function decide(callID, approved) {
    try {
      await getBackendSrv().post(holmesProxyRequestURL(holmesAdminProxyURL, `/investigations/${encodeURIComponent(sessionID)}/decisions`), approvalPayload(callID, approved, requestID()));
      await reloadSession(sessionID);
    } catch (requestError) {
      setError(requestError?.status === 403 ? '该操作需要 Grafana Admin 审批' : (requestError?.data?.error?.message || '审批失败'));
    }
  }

  async function cancel() {
    try {
      await getBackendSrv().post(holmesProxyRequestURL(holmesProxyURL, `/investigations/${encodeURIComponent(sessionID)}/cancel`), {});
      await reloadSession(sessionID);
    } catch (requestError) { setError(requestError?.data?.error?.message || '取消失败'); }
  }

  const events = Array.isArray(session?.events) ? session.events : [];
  const tools = events.filter((event) => event.type === 'tool_started' || event.type === 'tool_finished').map(toolSummary);
  const messages = events.filter((event) => event.type === 'assistant_message');
  const pending = Array.isArray(session?.pending_tools) ? session.pending_tools.filter((tool) => tool.requires_user && tool.approved == null) : [];
  const active = session?.status === 'running' || session?.status === 'awaiting_approval' || session?.status === 'created';

  const overviewPanel = h('section', { style: styles.card },
    h('h2', { style: styles.section }, '运行概览'),
    h(SummaryField, { label: '服务器', value: server.name }),
    h(SummaryField, { label: '稳定服务器 ID', value: server.id || '正在从服务端清单解析' }),
    h(SummaryField, { label: 'Erlang 节点', value: initialNode || '未固定，调查时由指标与节点清单确认' }),
    h(SummaryField, { label: '时间范围（UTC，最长 24h）', value: `${range.from} — ${range.to}` }),
    h(SummaryField, { label: '仪表板 UID', value: dashboardUIDValue }),
    h('h2', { style: styles.section }, 'Prometheus 当前快照'),
    h(SummaryField, { label: '节点列表', value: overview.nodes.length ? overview.nodes.map((node) => `${node.name} (${node.up ? 'UP' : 'DOWN'})`).join('、') : (overview.unavailable ? 'Prometheus 暂时不可用' : '暂无样本') }),
    h(SummaryField, { label: '主机 CPU', value: overview.cpu == null ? '无数据' : `${(overview.cpu * 100).toFixed(2)}%` }),
    h(SummaryField, { label: '主机内存（可用 / 总量）', value: `${formatBytes(overview.memoryAvailable)} / ${formatBytes(overview.memoryTotal)}` }),
    h(SummaryField, { label: '活动告警', value: String(overview.alerts) }),
    h(SummaryField, { label: '带入调查的告警标签', value: Object.keys(overview.alertContext?.labels || {}).length ? JSON.stringify(overview.alertContext.labels) : '当前没有活动告警标签' }),
    h(SummaryField, { label: '数据来源 / 采样时间', value: `Prometheus / ${overview.sampledAt ? new Date(overview.sampledAt).toISOString() : '无样本'}` }),
    h('details', null,
      h('summary', null, '实际执行的 PromQL 与时间范围'),
      h('pre', { style: styles.evidence }, safeMarkdownText(`${(overview.queries || []).join('\n')}\nUTC: ${range.from} — ${range.to}`, 8192))),
    h('h2', { style: styles.section }, '安全边界'),
    h('ul', null,
      h('li', null, '浏览器只访问 Grafana 同源后端代理'),
      h('li', null, 'Prometheus 只读；SSH/Erlang 仅结构化白名单工具'),
      h('li', null, '页面不保存或显示模型 Key、SSH 凭据和隐藏推理')));

  const controls = h('section', { style: styles.card },
    h('div', { style: styles.row },
      h('div', { style: { flex: '1 1 180px' } },
        h('label', { style: styles.label }, '模型（只影响新调查）'),
        h('select', { style: styles.input, value: model, disabled: active, onChange: (event) => setModel(event.target.value) },
          models.length
            ? models.map((item) => h('option', { key: item.alias, value: item.alias }, item.display_name || item.alias))
            : h('option', { value: '' }, '没有通过服务端过滤的可用模型'))),
      sessionID ? h('div', { style: { fontSize: '12px', opacity: 0.75 } }, `会话 ${sessionID}`) : null),
    h('label', { style: { ...styles.label, marginTop: '12px' } }, session?.status === 'completed' || session?.status === 'failed' ? '连续追问' : '调查问题'),
    h('textarea', { style: styles.textarea, value: ask, disabled: busy || active, onChange: (event) => setAsk(event.target.value), maxLength: 8000 }),
    h('div', { style: { ...styles.row, marginTop: '10px' } },
      !sessionID ? h('button', { style: styles.button, disabled: busy || !server.id || !model, onClick: createInvestigation }, busy ? '正在创建…' : '开始只读分析') : null,
      sessionID && !active ? h('button', { style: styles.button, disabled: busy, onClick: sendFollowUp }, busy ? '正在发送…' : '发送追问') : null,
      active ? h('button', { style: styles.dangerButton, onClick: cancel }, '取消调查') : null),
    active ? h('div', { role: 'status', 'aria-live': 'polite', style: { ...styles.notice, marginTop: '12px' } }, '调查正在进行，阶段说明和工具证据会持续更新；代理异常时每 2 秒自动刷新。') : null,
    error ? h('div', { style: { ...styles.error, marginTop: '10px', marginBottom: 0 }, role: 'alert', 'aria-live': 'polite' }, safeMarkdownText(error, 1000)) : null,
    messages.length ? h('div', { style: { marginTop: '18px' } },
      h('h2', { style: styles.section }, '阶段说明'),
      messages.map((event) => h('div', { key: event.id, style: styles.event }, safeMarkdownText(event.data?.content, 16000)))) : null,
    tools.length ? h('div', { style: { marginTop: '18px' } },
      h('h2', { style: styles.section }, '工具证据'),
      tools.map((tool) => h('details', { key: `${tool.id}-${tool.status}`, style: { marginBottom: '8px' } },
        h('summary', null, `${tool.name} · ${tool.status}${tool.durationMs == null ? '' : ` · ${tool.durationMs} ms`}`),
        h('pre', { style: styles.evidence }, tool.detail)))) : null,
    pending.map((tool) => h('div', { key: tool.call_id, style: { ...styles.card, borderColor: '#f2c96d', marginTop: '14px' } },
      h('h2', { style: styles.section }, '待 Admin 审批的只读诊断'),
      h(SummaryField, { label: '工具', value: tool.name }),
      h(SummaryField, { label: '服务器 / 节点', value: `${server.name} / ${initialNode || '未固定'}` }),
      h(SummaryField, { label: '超时 / 输出上限', value: `${tool.name.includes('hotspots') ? '45 秒 / 64 KiB' : '10 秒 / 32 KiB'}` }),
      h('pre', { style: styles.evidence }, safeMarkdownText(JSON.stringify(tool.arguments, null, 2), 8192)),
      h('div', { style: styles.row },
        h('button', { style: styles.button, onClick: () => decide(tool.call_id, true) }, '批准本次调用'),
        h('button', { style: styles.mutedButton, onClick: () => decide(tool.call_id, false) }, '拒绝')))),
    session?.final_answer ? h('div', { style: { marginTop: '18px' } },
      h('h2', { style: styles.section }, '最终根因分析'),
      h(MarkdownAnswer, { text: session.final_answer })) : null);

  return h('main', { className: 'holmes-page', style: styles.page },
    h('style', null, '@media(max-width:900px){.holmes-grid{grid-template-columns:1fr!important}.holmes-page{padding:12px!important}}'),
    h('div', { style: styles.header },
      h('div', null,
        h('h1', { style: styles.title }, 'Erlang 智能根因分析'),
        h('div', { style: styles.notice }, 'AI 分析仅供辅助，执行修复前需人工确认。')),
      h('span', { style: styles.badge }, statusLabel(session, latestEvent))),
    h('div', { className: 'holmes-grid', style: styles.grid }, overviewPanel, controls));
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
  // Holmes remains optional at the service layer, but its Grafana page is
  // intentionally not exposed by this plugin build. Unknown and legacy
  // routes fall back to the read-only overview instead of the workbench.
  return h(OverviewPage);
}

export const plugin = new AppPlugin().setRootPage(RootPage);
