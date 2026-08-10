import test from 'node:test';
import assert from 'node:assert/strict';

import {
  activeAlertsFromRules,
  alertLabelText,
  cpuCapacityPercent,
  displayAlertValue,
  fixed,
  gibibytes,
  isMNodeInfrastructureNode,
  mergeNodeSamples,
} from '../src/overview-logic.js';

test('formats binary capacity as two-decimal G display values', () => {
  assert.equal(fixed(gibibytes(64 * 1024 ** 3)), '64.00');
  assert.equal(fixed(gibibytes(Number.NaN)), '无数据');
});

test('derives single-core percent capacity from logical CPUs', () => {
  assert.equal(cpuCapacityPercent(1), 100);
  assert.equal(cpuCapacityPercent(16), 1600);
  assert.equal(cpuCapacityPercent(0), null);
});

test('classifies c-number Erlang nodes as non-game mnode infrastructure', () => {
  assert.equal(isMNodeInfrastructureNode('wl_c801_1@192.168.100.23'), true);
  assert.equal(isMNodeInfrastructureNode('mhd_c901_2@192.168.100.47'), true);
  assert.equal(isMNodeInfrastructureNode('c901@127.0.0.1'), true);
  assert.equal(isMNodeInfrastructureNode('wl_debug_1@127.0.0.1'), false);
  assert.equal(isMNodeInfrastructureNode('wl_ssjj_1814@127.0.0.1'), false);
});

test('merges node metrics into stable rows', () => {
  const rows = mergeNodeSamples(
    [{ metric: { node: 'b' }, value: 1 }, { metric: { node: 'a' }, value: 0 }],
    [{ metric: { node: 'a' }, value: Number.NaN }],
    [],
    [{ metric: { node: 'b' }, value: 6204 }],
	[{ metric: { node: 'b' }, value: 3221225472 }],
	[{ metric: { node: 'b' }, value: 1.25 }],
	[{ metric: { node: 'b' }, value: 1 }],
	[
	  { metric: { node: 'b', node_id: '901100005', connection_node: 'wl_ssjj_100005@172.19.33.104', connection_type: 'region' }, value: 1 },
	  { metric: { node: 'b', node_id: '801000001', connection_node: 'wl_ssjj_1@172.19.33.98', connection_type: 'central' }, value: 2 },
	],
  );
  assert.deepEqual(rows, [
	{ node: 'a', up: 0, registered: Number.NaN, online: null, processes: null, residentMemoryBytes: null, cpuRatio: null, mnodeAvailable: null, connections: [] },
	{ node: 'b', up: 1, registered: null, online: null, processes: 6204, residentMemoryBytes: 3221225472, cpuRatio: 1.25, mnodeAvailable: 1, connections: [
	  { nodeID: '801000001', node: 'wl_ssjj_1@172.19.33.98', type: 'central', state: 2, usable: true },
	  { nodeID: '901100005', node: 'wl_ssjj_100005@172.19.33.104', type: 'region', state: 1, usable: false },
	] },
  ]);
});

test('extracts active server alerts with rule annotations', () => {
  const payload = { data: { groups: [{ rules: [{
    labels: { severity: 'warning' },
    annotations: { condition: '超过阈值' },
    alerts: [
      { state: 'firing', labels: { alertname: 'ErlangProcessMemoryHigh', name: 'server-a', node: 'game@127.0.0.1', pid: '<0.1.0>' }, annotations: { value: '最大单进程内存=300MiB' }, activeAt: '2026-08-04T08:07:05Z', value: '314572800' },
      { state: 'firing', labels: { alertname: 'Other', name: 'server-b' } },
    ],
  }] }] } };
  const alerts = activeAlertsFromRules(payload, 'server-a');
  assert.equal(alerts.length, 1);
  assert.equal(alerts[0].annotations.condition, '超过阈值');
  assert.equal(displayAlertValue(alerts[0]), '最大单进程内存=300MiB');
  assert.match(alertLabelText(alerts[0].labels), /^severity=warning，name=server-a，node=game@127\.0\.0\.1，pid=<0\.1\.0>/);
});
