import assert from 'node:assert/strict';
import test from 'node:test';

import {
  beginCooldown,
  cooldownRemaining,
  dashboardServer,
  dashboardUID,
  homePresentationCSS,
  homeGridPresentationTargets,
  isAnonymousGrafanaStatus,
  isAnonymousGrafanaUser,
  isDynamicAutoRefresh,
  isOverviewDashboardUID,
  latestPrometheusSampleMs,
  overviewEntryURL,
  prometheusSnapshotRetryDelayMs,
  prometheusLabelValue,
  refreshCooldownMs,
  serverLastAttemptMs,
  shouldPollControlState,
  shouldHideGuestNavigation,
  shouldHideUnusedNavigation,
  syncBooleanAttribute,
  startupDashboardUID,
} from '../src/control-logic.js';

class MemoryStorage {
  values = new Map();

  getItem(key) {
    return this.values.get(key) ?? null;
  }

  setItem(key, value) {
    this.values.set(key, value);
  }
}

test('extracts dashboard UID and fixed server value', () => {
  assert.equal(dashboardUID('/d/erlang-monitor-101-35-19-137/server?kiosk'), 'erlang-monitor-101-35-19-137');
  assert.equal(
    dashboardServer({ dashboard: { templating: { list: [{ name: 'server', current: { value: '101.35.19.137' } }] } } }),
    '101.35.19.137'
  );
});

test('cooldown is stored independently for each IP', () => {
  const storage = new MemoryStorage();
  const now = 1_000_000;
  beginCooldown(storage, '101.34.55.142', now);

  assert.equal(cooldownRemaining(storage, '101.34.55.142', now), refreshCooldownMs);
  assert.equal(cooldownRemaining(storage, '101.35.19.137', now), 0);
  assert.equal(cooldownRemaining(storage, '101.34.55.142', now + refreshCooldownMs), 0);
});

test('recognizes only Grafana dynamic auto refresh', () => {
  assert.equal(isDynamicAutoRefresh('?kiosk&refresh=auto'), true);
  assert.equal(isDynamicAutoRefresh('?kiosk&refresh=30m'), false);
});

test('accepts only a safe startup dashboard UID', () => {
  assert.equal(startupDashboardUID('?erlang-monitor-dashboard=erlang-monitor-overview&kiosk'), 'erlang-monitor-overview');
  assert.equal(startupDashboardUID('?erlang-monitor-dashboard=..%2Fapi'), '');
});

test('keeps only the dashboard entry for anonymous visitors', () => {
  assert.equal(isAnonymousGrafanaStatus(401), true);
  assert.equal(isAnonymousGrafanaStatus(200), false);
  assert.equal(isAnonymousGrafanaUser({ isSignedIn: false }), true);
  assert.equal(isAnonymousGrafanaUser({ isSignedIn: true }), false);
  assert.equal(isAnonymousGrafanaUser(undefined), false);
  assert.equal(shouldHideGuestNavigation('/dashboards'), false);
  assert.equal(shouldHideGuestNavigation('/'), true);
  assert.equal(shouldHideGuestNavigation('/bookmarks'), true);
  assert.equal(shouldHideGuestNavigation('/dashboards?starred'), true);
  assert.equal(shouldHideGuestNavigation('/alerting'), true);
  assert.equal(shouldHideGuestNavigation('/explore'), true);
  assert.equal(shouldHideGuestNavigation('/connections'), true);
  assert.equal(shouldHideGuestNavigation('/apps'), true);
  assert.equal(shouldHideGuestNavigation('/admin'), true);
});

test('retries transient Prometheus snapshot failures with bounded backoff', () => {
  assert.equal(prometheusSnapshotRetryDelayMs(401, 0), 500);
  assert.equal(prometheusSnapshotRetryDelayMs(502, 1), 1_500);
  assert.equal(prometheusSnapshotRetryDelayMs(undefined, 2), 3_000);
  assert.equal(prometheusSnapshotRetryDelayMs(503, 3), null);
  assert.equal(prometheusSnapshotRetryDelayMs(403, 0), null);
  assert.equal(prometheusSnapshotRetryDelayMs(422, 0), null);
});

test('hides the unused Drilldown entry for signed-in operators', () => {
  assert.equal(shouldHideUnusedNavigation('/drilldown'), true);
  assert.equal(shouldHideUnusedNavigation('/drilldown/metrics'), true);
  assert.equal(shouldHideUnusedNavigation('/dashboards'), false);
  assert.equal(shouldHideUnusedNavigation('/connections/datasources'), false);
  assert.equal(shouldHideUnusedNavigation('/admin'), false);
});

test('updates presentation markers only when their state changes', () => {
  const attributes = new Map();
  let mutations = 0;
  const element = {
    getAttribute: (name) => attributes.get(name) ?? null,
    setAttribute: (name, value) => { attributes.set(name, value); mutations += 1; },
    removeAttribute: (name) => { attributes.delete(name); mutations += 1; },
  };

  assert.equal(syncBooleanAttribute(element, 'data-home', true), true);
  assert.equal(syncBooleanAttribute(element, 'data-home', true), false);
  assert.equal(mutations, 1);
  assert.equal(syncBooleanAttribute(element, 'data-home', false), true);
  assert.equal(syncBooleanAttribute(element, 'data-home', false), false);
  assert.equal(mutations, 2);
});

test('keeps the Home dashboard card in stable document flow', () => {
  assert.match(
    homePresentationCSS,
    /\[data-erlang-monitor-home-layout="true"\]\{height:auto!important;width:auto!important\}/
  );
  assert.match(
    homePresentationCSS,
    /\[data-erlang-monitor-home-dashboard="true"\]\{position:relative!important;left:auto!important;top:auto!important;transform:none!important;width:auto!important\}/
  );
  assert.doesNotMatch(homePresentationCSS, /translate\(/);
});

test('hides every non-dashboard sibling including empty Home grid placeholders', () => {
  const node = (className, parentElement = null) => ({
    className,
    parentElement,
    children: [],
    matches: (selector) => className.split(' ').includes(selector.slice(1)),
  });
  const outerLayout = node('react-grid-layout');
  const innerLayout = node('react-grid-layout', outerLayout);
  const welcome = node('react-grid-item', innerLayout);
  const dashboard = node('react-grid-item', innerLayout);
  const news = node('react-grid-item', innerLayout);
  innerLayout.children = [welcome, dashboard, news];

  const targets = homeGridPresentationTargets(dashboard);

  assert.deepEqual(targets.hiddenGridItems, [welcome, news]);
  assert.deepEqual(targets.homeLayouts, [innerLayout, outerLayout]);
});

test('redirects legacy Erlang dashboard routes to the overview page', () => {
  const dashboardUIDs = [
    'erlang-monitor-overview',
    'erlang-monitor-101-35-19-137',
    'erlang-monitor-150-158-94-69',
    'erlang-monitor-162-14-141-52',
    'erlang-monitor-49-234-183-253',
    'erlang-monitor-internal-192-168-100-23',
    'erlang-monitor-internal-192-168-100-25',
    'erlang-qt05-192-168-100-33',
    'erlang-qt05-192-168-100-37',
    'erlang-qt07-192-168-100-47',
    'erlang-qt07-192-168-100-48',
  ];
  for (const uid of dashboardUIDs) {
    assert.equal(isOverviewDashboardUID(uid), true, uid);
  }
  assert.equal(isOverviewDashboardUID('other-dashboard'), false);
  assert.equal(
    overviewEntryURL(
      '/d/erlang-monitor-overview/101-34-55-142-game-gc',
      '?orgId=1&from=now-6h&to=now&timezone=browser&refresh=30m'
    ),
    '/a/erlang-monitor-controls-app/overview?dashboard_uid=erlang-monitor-overview&orgId=1&from=now-6h&to=now&timezone=browser&kiosk='
  );
  assert.equal(
    overviewEntryURL(
      '/d/erlang-monitor-internal-192-168-100-23/192-168-100-23-debug',
      '?orgId=1&from=now-6h&to=now&timezone=browser&refresh=30m'
    ),
    '/a/erlang-monitor-controls-app/overview?dashboard_uid=erlang-monitor-internal-192-168-100-23&orgId=1&from=now-6h&to=now&timezone=browser&kiosk='
  );
  assert.equal(overviewEntryURL('/d/other-dashboard/example', '?from=now-1h'), '');
});

test('polls controls only while a legacy Erlang dashboard route is active', () => {
  assert.equal(shouldPollControlState('/d/erlang-monitor-overview/example'), true);
  assert.equal(shouldPollControlState('/d/erlang-qt05-192-168-100-33/example'), true);
  assert.equal(shouldPollControlState('/'), false);
  assert.equal(shouldPollControlState('/dashboards'), false);
  assert.equal(shouldPollControlState('/a/erlang-monitor-controls-app/overview'), false);
  assert.equal(shouldPollControlState('/d/other-dashboard/example'), false);
});

test('reads the selected server collection attempt time', () => {
  const payload = { servers: { 'external-1': { last_attempt: '2026-08-04T05:23:25.445Z' } } };
  assert.equal(serverLastAttemptMs(payload, 'external-1'), Date.parse('2026-08-04T05:23:25.445Z'));
  assert.equal(serverLastAttemptMs(payload, 'external-2'), 0);
});

test('reads the newest Prometheus source sample timestamp', () => {
  const payload = { data: { result: [{ value: [1, '1785821967.553'] }, { value: [1, '1785821968.125'] }] } };
  assert.equal(latestPrometheusSampleMs(payload), 1_785_821_968_125);
  assert.equal(latestPrometheusSampleMs({ data: { result: [] } }), 0);
});

test('escapes a Prometheus string label value', () => {
  assert.equal(prometheusLabelValue('a\\b"c\nd'), 'a\\\\b\\"c\\nd');
});
