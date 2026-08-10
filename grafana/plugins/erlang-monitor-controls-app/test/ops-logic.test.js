import test from 'node:test';
import assert from 'node:assert/strict';

import { preferredServer, serverOptions, skillSummaries, withTaskID } from '../src/ops-logic.js';

test('normalizes server options and selects only an exact requested target', () => {
  const servers = serverOptions({ servers: [
    { server_id: 'qt01-internal-debug', display_name: '192.168.100.23' },
    { server_id: '', display_name: 'invalid' },
  ] });
  assert.deepEqual(servers, [{ id: 'qt01-internal-debug', name: '192.168.100.23' }]);
  assert.deepEqual(preferredServer(servers, '192.168.100.23'), servers[0]);
  assert.deepEqual(preferredServer(servers, 'qt01-internal-debug'), servers[0]);
  assert.equal(preferredServer(servers, '101.34.55.142'), null);
  assert.equal(preferredServer(servers, ''), null);
});

test('keeps only skills with a name and description', () => {
  assert.deepEqual(skillSummaries({ skills: [
    { name: ' internal-disk-space-recovery ', description: ' 固定路径磁盘恢复 ' },
    { name: 'missing-description' },
  ] }), [{ name: 'internal-disk-space-recovery', description: '固定路径磁盘恢复' }]);
});

test('persists and removes the current task ID without losing dashboard context', () => {
  const source = 'https://monitor.example/a/ops-agent?server=192.168.100.23&from=now-6h';
  const persisted = new URL(withTaskID(source, 'abc123'));
  assert.equal(persisted.searchParams.get('task_id'), 'abc123');
  assert.equal(persisted.searchParams.get('server'), '192.168.100.23');
  assert.equal(persisted.searchParams.get('from'), 'now-6h');

  const cleared = new URL(withTaskID(persisted.toString(), ''));
  assert.equal(cleared.searchParams.has('task_id'), false);
  assert.equal(cleared.searchParams.get('server'), '192.168.100.23');
});
