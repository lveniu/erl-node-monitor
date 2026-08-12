import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';

const plugin = JSON.parse(readFileSync(new URL('../plugin.json', import.meta.url), 'utf8'));
const moduleSource = readFileSync(new URL('../src/module.js', import.meta.url), 'utf8');
const homeSource = readFileSync(new URL('../src/home-page.js', import.meta.url), 'utf8');

test('plugin exposes monitoring, operations, and code analysis pages', () => {
  assert.deepEqual(plugin.includes.map(({ path }) => path).sort(), [
    '/a/erlang-monitor-controls-app/code-analysis',
    '/a/erlang-monitor-controls-app/ops-agent',
    '/a/erlang-monitor-controls-app/overview',
  ]);
  assert.deepEqual([...new Set(plugin.routes.map(({ path }) => path))].sort(), [
    'code-mcp',
    'code-mcp-health',
    'collect',
    'config-status',
    'ops-agent',
    'ops-agent-admin',
    'ops-agent-health',
    'status',
  ]);
  assert.doesNotMatch(moduleSource, /WorkbenchPage/);
});

test('plugin root uses a context-free application home', () => {
  assert.match(moduleSource, /import \{ HomePage \} from '\.\/home-page\.js'/);
  assert.match(moduleSource, /return h\(HomePage\);/);
  assert.doesNotMatch(moduleSource, /Unknown and legacy routes fall back to the read-only overview/);
  assert.match(homeSource, /\/a\/erlang-monitor-controls-app\/code-analysis/);
  assert.match(homeSource, /\/a\/erlang-monitor-controls-app\/ops-agent/);
  assert.match(homeSource, /从具体服务器 Dashboard 进入/);
});
