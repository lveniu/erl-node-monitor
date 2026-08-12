import { safeMarkdownText } from './shared-logic.js';

const requiredProjectHeaders = ['project_id', '项目', 'repo_path', 'agent', 'skill'];

function inlineTokens(value) {
  const source = String(value ?? '');
  const tokens = [];
  const pattern = /(`[^`\n]+`|\*\*[^*\n]+\*\*|__[^_\n]+__|\*[^*\n]+\*|_[^_\n]+_|\[[^\]\n]+\]\([^)\s]+\))/g;
  let cursor = 0;
  for (const match of source.matchAll(pattern)) {
    if (match.index > cursor) tokens.push({ type: 'text', text: source.slice(cursor, match.index) });
    const raw = match[0];
    if (raw.startsWith('`')) tokens.push({ type: 'code', text: raw.slice(1, -1) });
    else if (raw.startsWith('**') || raw.startsWith('__')) tokens.push({ type: 'strong', text: raw.slice(2, -2) });
    else if (raw.startsWith('*') || raw.startsWith('_')) tokens.push({ type: 'em', text: raw.slice(1, -1) });
    else {
      const link = raw.match(/^\[([^\]]+)\]\(([^)]+)\)$/);
      const url = link?.[2] || '';
      if (/^https?:\/\//i.test(url)) tokens.push({ type: 'link', text: link[1], url });
      else tokens.push({ type: 'text', text: link?.[1] || raw });
    }
    cursor = match.index + raw.length;
  }
  if (cursor < source.length) tokens.push({ type: 'text', text: source.slice(cursor) });
  return tokens;
}

function markdownCells(line) {
  return line.trim().replace(/^\|/, '').replace(/\|$/, '').split(/(?<!\\)\|/).map((cell) => cell.trim().replace(/\\\|/g, '|'));
}

function isTableDivider(line) {
  const cells = markdownCells(line);
  return cells.length > 0 && cells.every((cell) => /^:?-{3,}:?$/.test(cell));
}

function hasMarkdownDocumentStructure(value) {
  const lines = String(value ?? '').split('\n');
  const signals = new Set();
  for (let index = 0; index < lines.length; index += 1) {
    const line = lines[index];
    if (/^\s{0,3}#{1,6}\s+\S/.test(line)) signals.add('heading');
    if (/^\s{0,3}(?:[-+*]|\d+[.)])\s+\S/.test(line)) signals.add('list');
    if (/^\s{0,3}>\s?\S/.test(line)) signals.add('quote');
    if (/(?:\*\*|__)[^\n]+(?:\*\*|__)/.test(line)) signals.add('strong');
    if (line.includes('|') && index + 1 < lines.length && isTableDivider(lines[index + 1])) signals.add('table');
  }
  return signals.has('heading') && signals.size >= 2;
}

export function unwrapMarkdownDocumentFence(value) {
  const source = String(value ?? '').replace(/\r\n?/g, '\n').trim();
  const lines = source.split('\n');
  if (lines.length < 3) return source;
  const opening = lines[0].match(/^```\s*([\w.+-]*)\s*$/);
  if (!opening || !/^```\s*$/.test(lines[lines.length - 1])) return source;
  const language = opening[1].toLowerCase();
  if (!['', 'markdown', 'md', 'code'].includes(language)) return source;
  if (lines.slice(1, -1).some((line) => /^```\s*[\w.+-]*\s*$/.test(line))) return source;
  const body = lines.slice(1, -1).join('\n').trim();
  if (!['markdown', 'md'].includes(language) && !hasMarkdownDocumentStructure(body)) return source;
  return body;
}

export function markdownForClipboard(value, maxLength = 65536) {
  return unwrapMarkdownDocumentFence(safeMarkdownText(value, maxLength)).trim();
}

export function parseSafeMarkdown(value, maxLength = 65536) {
  const text = unwrapMarkdownDocumentFence(safeMarkdownText(value, maxLength));
  const lines = text.split('\n');
  const blocks = [];
  let index = 0;
  while (index < lines.length) {
    const line = lines[index];
    if (!line.trim()) { index += 1; continue; }

    const fence = line.match(/^\s*```\s*([\w.+-]*)\s*$/);
    if (fence) {
      const code = [];
      index += 1;
      while (index < lines.length && !/^\s*```\s*$/.test(lines[index])) { code.push(lines[index]); index += 1; }
      if (index < lines.length) index += 1;
      blocks.push({ type: 'codeBlock', language: fence[1].slice(0, 24), text: code.join('\n') });
      continue;
    }

    const heading = line.match(/^\s{0,3}(#{1,6})\s+(.+?)\s*#*\s*$/);
    if (heading) {
      blocks.push({ type: 'heading', level: heading[1].length, children: inlineTokens(heading[2]) });
      index += 1;
      continue;
    }

    if (/^\s{0,3}(?:-{3,}|\*{3,}|_{3,})\s*$/.test(line)) {
      blocks.push({ type: 'rule' }); index += 1; continue;
    }

    if (line.includes('|') && index + 1 < lines.length && isTableDivider(lines[index + 1])) {
      const header = markdownCells(line).map(inlineTokens);
      const rows = [];
      index += 2;
      while (index < lines.length && lines[index].includes('|') && lines[index].trim()) {
        rows.push(markdownCells(lines[index]).map(inlineTokens)); index += 1;
      }
      blocks.push({ type: 'table', header, rows });
      continue;
    }

    const listMatch = line.match(/^\s{0,3}([-+*]|\d+[.)])\s+(.+)$/);
    if (listMatch) {
      const ordered = /^\d/.test(listMatch[1]);
      const items = [];
      while (index < lines.length) {
        const item = lines[index].match(/^\s{0,3}([-+*]|\d+[.)])\s+(.+)$/);
        if (!item || /^\d/.test(item[1]) !== ordered) break;
        items.push(inlineTokens(item[2])); index += 1;
      }
      blocks.push({ type: 'list', ordered, items });
      continue;
    }

    if (/^\s{0,3}>/.test(line)) {
      const quote = [];
      while (index < lines.length && /^\s{0,3}>/.test(lines[index])) {
        quote.push(lines[index].replace(/^\s{0,3}>\s?/, '')); index += 1;
      }
      blocks.push({ type: 'quote', children: inlineTokens(quote.join('\n')) });
      continue;
    }

    const paragraph = [line.trim()];
    index += 1;
    while (index < lines.length && lines[index].trim()
      && !/^\s*```/.test(lines[index])
      && !/^\s{0,3}(?:#{1,6})\s+/.test(lines[index])
      && !/^\s{0,3}(?:[-+*]|\d+[.)])\s+/.test(lines[index])
      && !/^\s{0,3}>/.test(lines[index])
      && !(lines[index].includes('|') && index + 1 < lines.length && isTableDivider(lines[index + 1]))) {
      paragraph.push(lines[index].trim()); index += 1;
    }
    blocks.push({ type: 'paragraph', children: inlineTokens(paragraph.join('\n')) });
  }
  return blocks;
}

export function mcpInitializeRequest(id) {
  return {
    jsonrpc: '2.0', id, method: 'initialize',
    params: { protocolVersion: '2025-06-18', capabilities: {}, clientInfo: { name: 'grafana-code-analysis', version: '1.0.0' } },
  };
}

export function mcpInitializedNotification() {
  return { jsonrpc: '2.0', method: 'notifications/initialized' };
}

export function mcpToolsListRequest(id) {
  return { jsonrpc: '2.0', id, method: 'tools/list', params: {} };
}

export function mcpToolRequest(id, name, args = {}) {
  return { jsonrpc: '2.0', id, method: 'tools/call', params: { name, arguments: args } };
}

export function parseMCPEnvelope(payload, expectedID) {
  const source = String(payload ?? '').trim();
  if (!source) throw new Error('代码 MCP 返回了空响应');
  const candidates = [];
  if (source.startsWith('{') || source.startsWith('[')) candidates.push(source);
  else for (const line of source.split(/\r?\n/)) if (line.startsWith('data:')) candidates.push(line.slice(5).trim());
  for (const candidate of candidates) {
    let decoded;
    try { decoded = JSON.parse(candidate); } catch { continue; }
    const messages = Array.isArray(decoded) ? decoded : [decoded];
    const matched = messages.find((message) => String(message?.id) === String(expectedID));
    if (matched) return matched;
  }
  throw new Error('代码 MCP 响应中没有匹配的 JSON-RPC 结果');
}

export function toolText(envelope, toolName) {
  if (envelope?.error) throw new Error(`${toolName} 调用失败：${envelope.error.message || 'JSON-RPC 调用失败'}`);
  const result = envelope?.result;
  const text = Array.isArray(result?.content)
    ? result.content.filter((item) => item?.type === 'text').map((item) => String(item.text || '')).join('\n')
    : '';
  if (result?.isError === true) {
    const code = text.match(/(?:错误码|error code)\s*[：:]\s*([A-Z_]+)/i)?.[1] || 'TOOL_ERROR';
    throw new Error(`${toolName} 调用失败 [${code}]：${text || '未返回错误详情'}`);
  }
  if (!text.trim()) throw new Error(`${toolName} 没有返回文本结果`);
  return text;
}

export function requireMCPTools(envelope, requiredNames) {
  if (envelope?.error) throw new Error(`tools/list 调用失败：${envelope.error.message || 'JSON-RPC 调用失败'}`);
  const names = Array.isArray(envelope?.result?.tools) ? envelope.result.tools.map((tool) => tool?.name).filter(Boolean) : [];
  const missing = requiredNames.filter((name) => !names.includes(name));
  if (missing.length) throw new Error(`代码 MCP 缺少必要工具：${missing.join(', ')}`);
  return names;
}

export function appendTrace(current, trace, maxItems = 40) {
  const items = Array.isArray(current) ? current : [];
  if (['protocol', 'catalog'].includes(trace?.type) && items.some((item) => item?.type === trace.type && item?.detail === trace.detail)) return items;
  const previous = items[items.length - 1];
  if (previous?.type === trace?.type && previous?.detail === trace?.detail) return items;
  return [...items, trace].slice(-maxItems);
}

export function formatDuration(milliseconds) {
  const totalSeconds = Math.max(0, Math.floor(Number(milliseconds) / 1000) || 0);
  const hours = Math.floor(totalSeconds / 3600);
  const minutes = Math.floor((totalSeconds % 3600) / 60);
  const seconds = totalSeconds % 60;
  if (hours) return `${hours}:${String(minutes).padStart(2, '0')}:${String(seconds).padStart(2, '0')}`;
  return `${String(minutes).padStart(2, '0')}:${String(seconds).padStart(2, '0')}`;
}

export function parseProjects(markdown) {
  const projects = [];
  const lines = String(markdown ?? '').split(/\r?\n/);
  let columns = null;
  for (let index = 0; index < lines.length; index += 1) {
    const line = lines[index];
    if (!line.trim().startsWith('|')) { columns = null; continue; }
    const cells = markdownCells(line);
    if (index + 1 < lines.length && isTableDivider(lines[index + 1])) {
      const normalized = cells.map((cell) => cell.trim().toLowerCase());
      const candidate = Object.fromEntries(requiredProjectHeaders.map((header) => [header, normalized.indexOf(header)]));
      if (requiredProjectHeaders.every((header) => candidate[header] >= 0)) {
        columns = {
          id: candidate.project_id,
          name: candidate['项目'],
          repoPath: candidate.repo_path,
          agent: candidate.agent,
          skill: candidate.skill,
          code: normalized.indexOf('code'),
          svnUpdate: normalized.indexOf('分析前svn更新'),
        };
      } else {
        columns = null;
      }
      index += 1;
      continue;
    }
    if (!columns || isTableDivider(line)) continue;
    const id = cells[columns.id];
    const name = cells[columns.name];
    const repoPath = cells[columns.repoPath];
    const agent = cells[columns.agent];
    const skill = cells[columns.skill];
    if (!id || !name || !/^(?:[A-Za-z]:[\\/]|\/)/.test(repoPath)) continue;
    const normalizedRepoPath = repoPath.replace(/\\/g, '/').replace(/\/+$/, '');
    const project = { id, name, repoPath: normalizedRepoPath, branch: normalizedRepoPath.split('/').pop() || '', agent, skill };
    if (columns.code >= 0 && cells[columns.code]) project.code = cells[columns.code];
    if (columns.svnUpdate >= 0 && cells[columns.svnUpdate]) project.svnUpdate = cells[columns.svnUpdate];
    projects.push(project);
  }
  return projects;
}

function inspectionList(value) {
  return String(value || '').split(/[、,，]/).map((item) => item.trim()).filter(Boolean);
}

function namedInspectionTool(value) {
  const text = String(value || '').trim();
  const match = text.match(/^([^（(]+)[（(]([^）)]+)[）)]$/);
  return match ? { status: match[1].trim(), name: match[2].trim() } : { status: text, name: '' };
}

export function parseRepositoryInspection(markdown) {
  const fields = {};
  const extra = [];
  for (const sourceLine of safeMarkdownText(markdown, 12000).split(/\r?\n/)) {
    const line = sourceLine.trim().replace(/^#+\s*/, '').replace(/^[-*]\s+/, '');
    if (!line) continue;
    const match = line.match(/^([^：:]+)[：:]\s*(.*)$/);
    if (!match) { extra.push(line); continue; }
    const key = match[1].replace(/[`*_]/g, '').trim().toLowerCase();
    const value = match[2].replace(/^`|`$/g, '').trim();
    fields[key] = value;
  }
  const agent = namedInspectionTool(fields.agent);
  const skill = namedInspectionTool(fields.skill);
  return {
    projectName: fields['项目检查'] || '',
    projectID: fields.projectid || '',
    projectCode: fields.projectcode || '',
    access: fields['目录访问'] || '',
    module: fields['module.md'] || '',
    agent,
    skill,
    svnUpdate: fields['分析前svn更新'] || '',
    conflictPolicy: fields['本地冲突策略'] || '',
    allowedDirectories: inspectionList(fields['允许目录']),
    allowedFiles: inspectionList(fields['允许文件']),
    excludedDirectories: inspectionList(fields['过滤目录']),
    timeout: fields['分析超时'] || '',
    concurrency: fields['项目并发上限'] || '',
    extra,
  };
}

function clipped(value, maxLength) {
  const text = String(value ?? '').trim();
  return text.length <= maxLength ? text : `${text.slice(0, maxLength)}\n[前文已截断]`;
}

export function buildConversationQuestion(turns, currentQuestion, maxLength = 16000) {
  const current = String(currentQuestion ?? '').trim();
  if (!current) return '';
  const history = Array.isArray(turns)
    ? turns.filter((turn) => ['user', 'assistant'].includes(turn?.role) && String(turn?.content || '').trim()).slice(-6)
    : [];
  if (!history.length) return current.slice(0, maxLength);
  const prefix = '这是同一代码分析会话中的追问。前文仅用于理解指代，所有结论仍须重新依据当前源码核对；不要把前文回答当作已验证事实。\n\n前文：\n';
  const suffix = `\n\n当前追问：\n${current}`;
  const budget = Math.max(0, maxLength - prefix.length - suffix.length);
  const selected = [];
  let used = 0;
  for (let index = history.length - 1; index >= 0; index -= 1) {
    const turn = history[index];
    const label = turn.role === 'user' ? '用户' : '代码分析';
    const remaining = budget - used - label.length - 4;
    if (remaining <= 0) break;
    const content = clipped(turn.content, Math.min(remaining, turn.role === 'assistant' ? 6000 : 3000));
    selected.unshift(`${label}：${content}`);
    used += content.length + label.length + 3;
  }
  const historyText = selected.join('\n\n');
  const boundedHistory = historyText.length > budget ? historyText.slice(-budget) : historyText;
  return `${prefix}${boundedHistory}${suffix}`;
}

export function restoreConversation(value) {
  if (!value || typeof value !== 'object') return null;
  const turns = Array.isArray(value.turns)
    ? value.turns.filter((turn) => ['user', 'assistant'].includes(turn?.role) && typeof turn.content === 'string').slice(-20)
    : [];
  const traces = Array.isArray(value.traces)
    ? value.traces.filter((trace) => typeof trace?.type === 'string' && typeof trace?.at === 'string').slice(-40).reduce((items, trace) => appendTrace(items, trace), [])
    : [];
  if (typeof value.id !== 'string' || typeof value.repoPath !== 'string') return null;
  return { id: value.id, repoPath: value.repoPath, projectName: String(value.projectName || ''), turns, traces };
}

export function reconcileConversationProject(conversation, projects) {
  const items = Array.isArray(projects) ? projects : [];
  const selected = items.find((project) => project?.repoPath === conversation?.repoPath);
  if (selected) return { repoPath: selected.repoPath, reset: false };
  return { repoPath: items[0]?.repoPath || '', reset: Boolean(conversation?.turns?.length) };
}
