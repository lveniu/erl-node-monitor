import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';

const plugin = JSON.parse(readFileSync(new URL('../plugin.json', import.meta.url), 'utf8'));
const moduleSource = readFileSync(new URL('../src/module.js', import.meta.url), 'utf8');

test('plugin exposes only the overview and operations Agent pages', () => {
  assert.deepEqual(plugin.includes.map(({ path }) => path).sort(), [
    '/a/erlang-monitor-controls-app/ops-agent',
    '/a/erlang-monitor-controls-app/overview',
  ]);
  assert.deepEqual([...new Set(plugin.routes.map(({ path }) => path))].sort(), [
    'collect',
    'config-status',
    'ops-agent',
    'ops-agent-admin',
    'ops-agent-health',
    'status',
  ]);
  assert.doesNotMatch(moduleSource, /WorkbenchPage/);
});
