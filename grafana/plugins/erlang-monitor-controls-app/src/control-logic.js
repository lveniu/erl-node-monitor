export const refreshCooldownMs = 10_000;
export const homePresentationCSS = '[data-erlang-monitor-home-hidden="true"]{display:none!important}'
  + '[data-erlang-monitor-home-layout="true"]{height:auto!important;width:auto!important}'
  + '[data-erlang-monitor-home-dashboard="true"]{position:relative!important;left:auto!important;top:auto!important;transform:none!important;width:auto!important}'
  + '[data-erlang-monitor-home-dashboard="true"] section{width:100%!important}';
const prometheusSnapshotRetryDelaysMs = [500, 1_500, 3_000];

export function dashboardUID(pathname) {
  const match = /^\/d\/([^/]+)/.exec(pathname || '');
  return match ? decodeURIComponent(match[1]) : '';
}

export function startupDashboardUID(search) {
  const uid = new URLSearchParams(search || '').get('erlang-monitor-dashboard') || '';
  return /^[A-Za-z0-9_-]+$/.test(uid) ? uid : '';
}

export function isAnonymousGrafanaStatus(status) {
  return Number(status) === 401;
}

export function isAnonymousGrafanaUser(user) {
  return user?.isSignedIn === false;
}

export function prometheusSnapshotRetryDelayMs(status, attempt) {
  const code = Number(status);
  const transient = !Number.isFinite(code) || code === 0 || code === 401 || code === 408 || code === 429 || code >= 500;
  return transient ? prometheusSnapshotRetryDelaysMs[attempt] ?? null : null;
}

export function shouldHideGuestNavigation(href) {
  try {
    const target = new URL(String(href || ''), 'http://grafana.local');
    return target.pathname !== '/dashboards' || target.search !== '';
  } catch {
    return true;
  }
}

export function shouldHideUnusedNavigation(href) {
  try {
    const pathname = new URL(String(href || ''), 'http://grafana.local').pathname;
    return pathname === '/drilldown' || pathname.startsWith('/drilldown/');
  } catch {
    return false;
  }
}

export function syncBooleanAttribute(element, name, active) {
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

export function homeGridPresentationTargets(dashboardGridItem, boundary = null) {
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

export function isOverviewDashboardUID(uid) {
  return /^erlang-(?:monitor|qt05|qt07)-[A-Za-z0-9_-]+$/.test(String(uid || ''));
}

export function shouldPollControlState(pathname) {
  return isOverviewDashboardUID(dashboardUID(pathname));
}

export function overviewEntryURL(pathname, search) {
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

export function dashboardServer(payload) {
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

export function cooldownStorageKey(server) {
  return `erlang-monitor-refresh-cooldown:${server}`;
}

export function cooldownDeadline(storage, server) {
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

export function beginCooldown(storage, server, now = Date.now()) {
  const deadline = now + refreshCooldownMs;
  try {
    storage?.setItem(cooldownStorageKey(server), String(deadline));
  } catch {
    // Session storage can be unavailable in hardened browsers. The caller
    // still applies the in-memory deadline.
  }
  return deadline;
}

export function cooldownRemaining(storage, server, now = Date.now(), memoryDeadline = 0) {
  return Math.max(0, Math.max(cooldownDeadline(storage, server), memoryDeadline) - now);
}

export function serverLastAttemptMs(payload, serverID) {
  const value = payload?.servers?.[serverID]?.last_attempt;
  const timestamp = typeof value === 'string' ? Date.parse(value) : Number.NaN;
  return Number.isFinite(timestamp) ? timestamp : 0;
}

export function latestPrometheusSampleMs(payload) {
  const results = payload?.data?.result;
  if (!Array.isArray(results)) {
    return 0;
  }
  return results.reduce((latest, result) => {
    const timestamp = Number(result?.value?.[1]) * 1000;
    return Number.isFinite(timestamp) ? Math.max(latest, timestamp) : latest;
  }, 0);
}

export function prometheusLabelValue(value) {
  return String(value ?? '')
    .replaceAll('\\', '\\\\')
    .replaceAll('"', '\\"')
    .replaceAll('\n', '\\n');
}

export function isDynamicAutoRefresh(search) {
  return new URLSearchParams(search || '').get('refresh') === 'auto';
}
