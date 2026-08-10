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
import { OverviewPage } from './overview-page.js';
import { OpsPage } from './ops-page.js';

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

export const plugin = new AppPlugin().setRootPage(RootPage);
