import test from 'node:test';
import assert from 'node:assert/strict';

import {
  activeAlertContext,
  approvalPayload,
  boundedRange,
  createReloadScheduler,
  formatBytes,
  prometheusSamples,
  publicModels,
  safeMarkdownText,
  startSerialPoll,
  holmesProxyRequestURL,
  investigationRequestError,
  markdownBlocks,
  releaseRequestLock,
  statusLabel,
  toolSummary,
  tryAcquireRequestLock,
} from '../src/holmes-logic.js';

test('builds exact Grafana proxy URLs with a bounded gateway path', () => {
  assert.equal(
    holmesProxyRequestURL('/api/plugin-proxy/app/holmes', '/servers/resolve', { name: 'qt 05' }),
    '/api/plugin-proxy/app/holmes?name=qt+05&_path=%2Fservers%2Fresolve',
  );
});

test('filters disabled and unsafe model aliases', () => {
  assert.deepEqual(publicModels({ models: [
    { alias: 'glm', available: true },
    { alias: 'kimi', available: false },
    { alias: 'bad/model', available: true },
  ] }), [{ alias: 'glm', available: true }]);
});

test('waits for each session reload before scheduling the next poll', async () => {
  const callbacks = [];
  const cleared = [];
  const scheduler = {
    setTimeout(callback, delay) {
      callbacks.push({ callback, delay });
      return callbacks.length;
    },
    clearTimeout(timer) {
      cleared.push(timer);
    },
  };
  let finishReload;
  let reloads = 0;
  const stop = startSerialPoll(() => {
    reloads += 1;
    return new Promise((resolve) => { finishReload = resolve; });
  }, 2000, scheduler);

  assert.equal(callbacks.length, 1);
  assert.equal(callbacks[0].delay, 2000);
  const runningPoll = callbacks[0].callback();
  assert.equal(reloads, 1);
  assert.equal(callbacks.length, 1);
  finishReload();
  await runningPoll;
  assert.equal(callbacks.length, 2);
  assert.equal(callbacks[1].delay, 2000);
  stop();
  assert.deepEqual(cleared, [2]);
});

test('prevents duplicate follow-up submission before React rerenders', () => {
  const lock = { current: false };
  assert.equal(tryAcquireRequestLock(lock), true);
  assert.equal(tryAcquireRequestLock(lock), false);
  releaseRequestLock(lock);
  assert.equal(tryAcquireRequestLock(lock), true);
});

test('localizes old and new busy-session errors', () => {
  assert.equal(investigationRequestError({ data: { error: { code: 'SESSION_BUSY' } } }, 'fallback'), '上一条追问正在处理中，请等待完成');
  assert.equal(investigationRequestError({ data: { error: { message: 'session already has a running request' } } }, 'fallback'), '上一条追问正在处理中，请等待完成');
});

test('coalesces replayed SSE events into one session reload', () => {
  const callbacks = [];
  const cleared = [];
  let reloads = 0;
  const scheduler = {
    setTimeout(callback, delay) {
      callbacks.push({ callback, delay });
      return callbacks.length;
    },
    clearTimeout(timer) {
      cleared.push(timer);
    },
  };
  const refresh = createReloadScheduler(() => { reloads += 1; }, 50, scheduler);
  for (let index = 0; index < 82; index += 1) refresh.queue();
  assert.equal(callbacks.length, 1);
  assert.equal(callbacks[0].delay, 50);
  callbacks[0].callback();
  assert.equal(reloads, 1);
  refresh.queue();
  assert.equal(callbacks.length, 2);
  refresh.cancel();
  assert.deepEqual(cleared, [2]);
});

test('parses safe Markdown headings, tables, lists, and code blocks', () => {
  assert.deepEqual(markdownBlocks('## 结论\n\n| 项目 | 值 |\n|---|---|\n| 状态 | **正常** |\n\n- 证据一\n- 证据二\n\n```text\nraw <script>alert(1)</script>\n```'), [
    { type: 'heading', level: 2, text: '结论' },
    { type: 'table', headers: ['项目', '值'], rows: [['状态', '**正常**']] },
    { type: 'list', ordered: false, items: ['证据一', '证据二'] },
    { type: 'code', language: 'text', text: 'raw alert(1)' },
  ]);
});

test('bounds dashboard time range to 24 hours', () => {
  const now = Date.parse('2026-08-04T06:00:00Z');
  assert.deepEqual(boundedRange('now-7d', 'now', now), {
    from: '2026-08-03T06:00:00.000Z',
    to: '2026-08-04T06:00:00.000Z',
  });
});

test('sanitizes HTML, scripts, dangerous links, and external images', () => {
  const result = safeMarkdownText('<script>alert(1)</script> [x](javascript:alert(1)) ![track](https://x/p.png)');
  assert.equal(result.includes('<script>'), false);
  assert.equal(result.toLowerCase().includes('javascript:'), false);
  assert.equal(result.includes('https://x'), false);
  assert.match(result, /外链图片已隐藏/);
});

test('maps investigation and tool states for the workbench', () => {
  assert.equal(statusLabel({ status: 'awaiting_approval' }), '等待审批');
  assert.equal(statusLabel({ status: 'running' }, 'compaction_started'), '整理会话');
  assert.equal(statusLabel({ status: 'running' }, 'investigation_completed'), '模型思考');
  assert.deepEqual(toolSummary({ id: 2, type: 'tool_started', data: { id: 'call-1', tool_name: 'get_host_snapshot' } }), {
    id: 'call-1', name: 'get_host_snapshot', status: 'running', startedAt: '', durationMs: null, detail: '{\n  "id": "call-1",\n  "tool_name": "get_host_snapshot"\n}',
  });
});

test('selects a node-matching active alert for investigation context', () => {
  const samples = [
    { metric: { alertname: 'HostCPUHigh', node: 'other@127.0.0.1', severity: 'warning' } },
    { metric: { alertname: 'RunQueueHigh', node: 'game@127.0.0.1', severity: 'critical', fingerprint: 'fp-1' } },
  ];
  assert.deepEqual(activeAlertContext(samples, 'game@127.0.0.1'), {
    labels: { alertname: 'RunQueueHigh', node: 'game@127.0.0.1', severity: 'critical', fingerprint: 'fp-1' },
    fingerprint: 'fp-1',
  });
});

test('creates one-call approval decisions', () => {
  assert.deepEqual(approvalPayload('call-1', false, 'request-1'), {
    request_id: 'request-1', tool_call_id: 'call-1', approved: false,
  });
});

test('parses bounded Prometheus samples for the overview', () => {
  assert.deepEqual(prometheusSamples({ data: { result: [{ metric: { node: 'game@127.0.0.1' }, value: [1785823200, '1'] }] } }), [{
    metric: { node: 'game@127.0.0.1' }, value: 1, sampledAt: 1785823200000,
  }]);
  assert.equal(formatBytes(1073741824), '1.00 GiB');
});
