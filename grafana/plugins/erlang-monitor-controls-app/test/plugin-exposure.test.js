import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';

const plugin = JSON.parse(readFileSync(new URL('../plugin.json', import.meta.url), 'utf8'));
const moduleSource = readFileSync(new URL('../src/module.js', import.meta.url), 'utf8');

test('Holmes workbench is not exposed by the Grafana plugin', () => {
  assert.equal(
    plugin.includes.some(({ path }) => path === '/a/erlang-monitor-controls-app/holmes'),
    false,
  );
  assert.equal(
    plugin.routes.some(({ path }) => ['holmes-health', 'holmes', 'holmes-admin'].includes(path)),
    false,
  );
  assert.doesNotMatch(moduleSource, /return h\(WorkbenchPage\)/);
});

test('overview and operations Agent remain exposed', () => {
  assert.equal(
    plugin.includes.some(({ path }) => path === '/a/erlang-monitor-controls-app/overview'),
    true,
  );
  assert.equal(
    plugin.includes.some(({ path }) => path === '/a/erlang-monitor-controls-app/ops-agent'),
    true,
  );
  assert.equal(plugin.routes.some(({ path }) => path === 'collect'), true);
  assert.equal(plugin.routes.some(({ path }) => path === 'ops-agent'), true);
});
