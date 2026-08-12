import assert from 'node:assert/strict';
import test from 'node:test';

import { appendTrace, buildConversationQuestion, formatDuration, markdownForClipboard, mcpInitializedNotification, mcpInitializeRequest, mcpToolRequest, mcpToolsListRequest, parseMCPEnvelope, parseProjects, parseRepositoryInspection, parseSafeMarkdown, reconcileConversationProject, requireMCPTools, restoreConversation, toolText, unwrapMarkdownDocumentFence } from '../src/code-logic.js';

test('builds the MCP initialization, notification, and discovery messages', () => {
  assert.equal(mcpInitializeRequest('i1').method, 'initialize');
  assert.equal(mcpInitializeRequest('i1').params.protocolVersion, '2025-06-18');
  assert.deepEqual(mcpInitializedNotification(), { jsonrpc: '2.0', method: 'notifications/initialized' });
  assert.deepEqual(mcpToolsListRequest('t1'), { jsonrpc: '2.0', id: 't1', method: 'tools/list', params: {} });
  assert.deepEqual(requireMCPTools({ result: { tools: [{ name: 'list_projects' }, { name: 'inspect_repository' }] } }, ['list_projects']), ['list_projects', 'inspect_repository']);
  assert.throws(() => requireMCPTools({ result: { tools: [] } }, ['analyze_codebase']), /缺少必要工具/);
});

test('builds a standard MCP tools/call request', () => {
  assert.deepEqual(mcpToolRequest('r1', 'inspect_repository', { repo_path: 'D:/server05/trunk2' }), {
    jsonrpc: '2.0', id: 'r1', method: 'tools/call', params: { name: 'inspect_repository', arguments: { repo_path: 'D:/server05/trunk2' } },
  });
});

test('deduplicates adjacent MCP traces from repeated development effects', () => {
  const first = { id: '1', type: 'catalog', detail: '1 个注册项目', at: '2026-08-10T00:00:00Z' };
  assert.equal(appendTrace([first], { ...first, id: '2' }).length, 1);
  assert.equal(appendTrace([first], { ...first, id: '2', detail: '2 个注册项目' }).length, 2);
  const protocol = { id: 'p1', type: 'protocol', detail: '必要工具已发现', at: '2026-08-10T00:00:00Z' };
  assert.equal(appendTrace([protocol, first], { ...protocol, id: 'p2' }).length, 2);
});

test('formats live and completed stage durations consistently', () => {
  assert.equal(formatDuration(0), '00:00');
  assert.equal(formatDuration(65_999), '01:05');
  assert.equal(formatDuration(3_661_000), '1:01:01');
});

test('parses streamable HTTP SSE and extracts text tool results', () => {
  const payload = 'event: message\ndata: {"result":{"content":[{"type":"text","text":"# ok"}]},"jsonrpc":"2.0","id":"r1"}\n\n';
  assert.equal(toolText(parseMCPEnvelope(payload, 'r1'), 'inspect_repository'), '# ok');
});

test('parses exact registered projects from the MCP markdown table', () => {
  const markdown = '# 可用代码项目\n\n| project_id | 项目 | repo_path | Agent | SKILL |\n| --- | --- | --- | --- | --- |\n| server05 | Server 05 | D:/server05/trunk2 | code-agent | code-skill |';
  assert.deepEqual(parseProjects(markdown), [{ id: 'server05', name: 'Server 05', repoPath: 'D:/server05/trunk2', branch: 'trunk2', agent: 'code-agent', skill: 'code-skill' }]);
});

test('parses the current MCP project table with code and SVN update columns', () => {
  const markdown = '# 可用代码项目\n\n| project_id | code | 项目 | repo_path | Agent | SKILL | 分析前SVN更新 |\n| --- | --- | --- | --- | --- | --- | --- |\n| qt_01_trunk | qt-01 | qt-01 Erlang game server | D:/code-mcp/code_src/qt-01-trunk | code-error-logic-agent | code-error-logic-consultant | 是 |\n| qt_05_trunk | qt-05 | qt-05 Erlang game server | D:/code-mcp/code_src/qt-05-trunk | code-error-logic-agent | code-error-logic-consultant | 是 |';
  assert.deepEqual(parseProjects(markdown), [
    { id: 'qt_01_trunk', code: 'qt-01', name: 'qt-01 Erlang game server', repoPath: 'D:/code-mcp/code_src/qt-01-trunk', branch: 'qt-01-trunk', agent: 'code-error-logic-agent', skill: 'code-error-logic-consultant', svnUpdate: '是' },
    { id: 'qt_05_trunk', code: 'qt-05', name: 'qt-05 Erlang game server', repoPath: 'D:/code-mcp/code_src/qt-05-trunk', branch: 'qt-05-trunk', agent: 'code-error-logic-agent', skill: 'code-error-logic-consultant', svnUpdate: '是' },
  ]);
});

test('locates required project fields by header name instead of column position', () => {
  const markdown = '| 项目 | SKILL | repo_path | project_id | Agent |\n| --- | --- | --- | --- | --- |\n| Reordered | skill-a | /srv/repo | repo-a | agent-a |';
  assert.deepEqual(parseProjects(markdown), [{ id: 'repo-a', name: 'Reordered', repoPath: '/srv/repo', branch: 'repo', agent: 'agent-a', skill: 'skill-a' }]);
});

test('uses the normalized repository leaf as the visible branch name', () => {
  const markdown = '| project_id | 项目 | repo_path | Agent | SKILL |\n| --- | --- | --- | --- | --- |\n| qt_01_trunk | qt-01 | D:\\code-mcp\\code_src\\qt-01-trunk\\ | agent | skill |';
  assert.equal(parseProjects(markdown)[0].branch, 'qt-01-trunk');
});

test('parses repository inspection into a structured summary and advanced boundaries', () => {
  const report = '项目检查：qt-05 Erlang game server\nproject_id：`qt_05_trunk`\nproject_code：qt-05\n\n目录访问：正常\nMODULE.md：缺失\nAgent：可用（code-error-logic-agent）\nSKILL：可用（code-error-logic-consultant）\n\n分析前SVN更新：启用\n\n本地冲突策略：服务器版本优先（更新前清除本地变更）\n允许目录：.claude/agents、src、config/app\n允许文件：AGENTS.md、CLAUDE.md\n过滤目录：.svn、.git、_build\n分析超时：600 秒\n项目并发上限：2';
  assert.deepEqual(parseRepositoryInspection(report), {
    projectName: 'qt-05 Erlang game server', projectID: 'qt_05_trunk', projectCode: 'qt-05', access: '正常', module: '缺失',
    agent: { status: '可用', name: 'code-error-logic-agent' }, skill: { status: '可用', name: 'code-error-logic-consultant' },
    svnUpdate: '启用', conflictPolicy: '服务器版本优先（更新前清除本地变更）',
    allowedDirectories: ['.claude/agents', 'src', 'config/app'], allowedFiles: ['AGENTS.md', 'CLAUDE.md'], excludedDirectories: ['.svn', '.git', '_build'],
    timeout: '600 秒', concurrency: '2', extra: [],
  });
});

test('normalizes markdown punctuation in repository inspection keys', () => {
  const report = '## 项目检查：qt-05 Erlang game server\nproject*id：qt*05_trunk\nAgent: 不可用\n未识别说明';
  const parsed = parseRepositoryInspection(report);
  assert.equal(parsed.projectName, 'qt-05 Erlang game server');
  assert.equal(parsed.projectID, 'qt*05_trunk');
  assert.deepEqual(parsed.agent, { status: '不可用', name: '' });
  assert.deepEqual(parsed.extra, ['未识别说明']);
});

test('builds a bounded follow-up that treats earlier answers as unverified context', () => {
  const question = buildConversationQuestion([
    { role: 'user', content: 'm27_h 在哪里？' },
    { role: 'assistant', content: '在 mod_role_gm.erl。' },
  ], '参数类型是什么？', 500);
  assert.match(question, /前文仅用于理解指代/);
  assert.match(question, /代码分析：在 mod_role_gm\.erl/);
  assert.match(question, /当前追问：\n参数类型是什么/);
  assert.ok(question.length <= 500);
});

test('restores only valid bounded conversation data', () => {
  const value = restoreConversation({ id: 'c1', repoPath: 'D:/repo', projectName: 'Repo', turns: [{ role: 'user', content: 'q' }, { role: 'system', content: 'ignore' }], traces: [] });
  assert.deepEqual(value.turns, [{ role: 'user', content: 'q' }]);
  assert.equal(restoreConversation({ id: 1, repoPath: 'D:/repo' }), null);
  const protocol = { id: 'p1', type: 'protocol', detail: 'ready', at: '2026-08-10T00:00:00Z' };
  assert.equal(restoreConversation({ id: 'c2', repoPath: 'D:/repo', turns: [], traces: [protocol, { ...protocol, id: 'p2' }] }).traces.length, 1);
});

test('resets stale conversation context when its registered project disappeared', () => {
  const projects = [{ repoPath: 'D:/qt-01' }, { repoPath: 'D:/qt-05' }];
  assert.deepEqual(reconcileConversationProject({ repoPath: 'D:/qt-05', turns: [{ role: 'user', content: 'q' }] }, projects), { repoPath: 'D:/qt-05', reset: false });
  assert.deepEqual(reconcileConversationProject({ repoPath: 'D:/server05', turns: [{ role: 'user', content: 'old' }] }, projects), { repoPath: 'D:/qt-01', reset: true });
});

test('parses safe markdown blocks without retaining raw HTML or external images', () => {
  const blocks = parseSafeMarkdown('# 结论\n\n- **存在** `m32_h`\n- [源码](https://example.test/source)\n\n| 字段 | 类型 |\n| --- | --- |\n| RoleID | integer |\n\n```erlang\nm32_h().\n```\n<script>alert(1)</script>\n![secret](https://example.test/a.png)');
  assert.deepEqual(blocks.map(({ type }) => type), ['heading', 'list', 'table', 'codeBlock', 'paragraph']);
  assert.equal(blocks[1].items[0][0].type, 'strong');
  assert.equal(blocks[1].items[0][1].type, 'text');
  assert.equal(blocks[1].items[0][2].type, 'code');
  assert.equal(blocks[1].items[1][0].type, 'link');
  assert.equal(blocks[2].rows[0][0][0].text, 'RoleID');
  assert.equal(blocks[3].language, 'erlang');
  assert.doesNotMatch(JSON.stringify(blocks), /script|secret/);
  assert.match(JSON.stringify(blocks), /alert\(1\)/);
  assert.match(JSON.stringify(blocks), /外链图片已隐藏/);
});

test('unwraps a whole markdown document fence before parsing', () => {
  const blocks = parseSafeMarkdown('```markdown\n## 结论\n\n- **存在问题**\n- 建议复核\n```');
  assert.deepEqual(blocks.map(({ type }) => type), ['heading', 'list']);
  assert.equal(blocks[0].children[0].text, '结论');
});

test('unwraps a whole code fence only when its body is clearly markdown', () => {
  const blocks = parseSafeMarkdown('```code\n## 功能文件定位\n\n- `role_misc:call_role_map/2` 返回异常\n- **调用链**需要复核\n```');
  assert.deepEqual(blocks.map(({ type }) => type), ['heading', 'list']);
});

test('keeps a whole erlang fence and non-markdown code fence as code', () => {
  const erlang = parseSafeMarkdown('```erlang\nm32_h().\n```');
  const generic = parseSafeMarkdown('```code\nrole_misc:call_role_map(RoleID, Msg).\n```');
  assert.equal(erlang[0].type, 'codeBlock');
  assert.equal(erlang[0].language, 'erlang');
  assert.equal(generic[0].type, 'codeBlock');
  assert.equal(generic[0].language, 'code');
});

test('does not unwrap responses containing multiple fenced code blocks', () => {
  const source = '```markdown\n## 示例\n```\n\n```erlang\nm32_h().\n```';
  assert.equal(unwrapMarkdownDocumentFence(source), source);
  assert.deepEqual(parseSafeMarkdown(source).map(({ type }) => type), ['codeBlock', 'codeBlock']);
});

test('copies the sanitized markdown document instead of rendered plain text', () => {
  const source = '```markdown\n## 结论\n\n- **保留格式** `m32_h`\n<script>alert(1)</script>\n```';
  assert.equal(markdownForClipboard(source), '## 结论\n\n- **保留格式** `m32_h`\nalert(1)');
});

test('renders dangerous and unsupported markdown links as plain text', () => {
  const [block] = parseSafeMarkdown('[危险](javascript:alert(1)) [本地](file:///tmp/a) [安全](https://example.test)');
  assert.equal(block.type, 'paragraph');
  assert.equal(block.children.some(({ type, text }) => type === 'link' && text === '安全'), true);
  assert.equal(block.children.some(({ type }) => type === 'link'), true);
  assert.doesNotMatch(JSON.stringify(block.children), /javascript|file:\/\//);
});
