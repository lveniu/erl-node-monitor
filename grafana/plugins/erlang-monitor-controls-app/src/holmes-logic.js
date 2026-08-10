export const investigationStates = {
  created: '未开始',
  running: '模型思考',
  awaiting_approval: '等待审批',
  completed: '已完成',
  failed: '失败',
  cancelled: '已取消',
};

export const eventStates = {
  investigation_started: '连接中',
  assistant_message: '模型思考',
  tool_started: '工具执行',
  tool_finished: '工具完成',
  approval_required: '等待审批',
  compaction_started: '整理会话',
  compaction_completed: '模型思考',
  investigation_completed: '已完成',
  investigation_failed: '失败',
  investigation_cancelled: '已取消',
};

export function statusLabel(session, latestEventType = '') {
  const terminalEvents = new Set(['investigation_completed', 'investigation_failed', 'investigation_cancelled']);
  const terminalStatuses = new Set(['completed', 'failed', 'cancelled']);
  // A replayed terminal SSE event can arrive before an older session GET.
  // Keep the badge consistent with the authoritative persisted status.
  if (terminalEvents.has(latestEventType) && session?.status && !terminalStatuses.has(session.status)) {
    return investigationStates[session.status] || '未开始';
  }
  return eventStates[latestEventType] || investigationStates[session?.status] || '未开始';
}

export function publicModels(payload) {
  return Array.isArray(payload?.models)
    ? payload.models.filter((model) => model?.available && /^[A-Za-z0-9_-]+$/.test(model.alias || ''))
    : [];
}

export function startSerialPoll(poll, delayMs, scheduler = globalThis) {
  let cancelled = false;
  let timer = null;

  const schedule = () => {
    timer = scheduler.setTimeout(async () => {
      try {
        await poll();
      } finally {
        if (!cancelled) schedule();
      }
    }, delayMs);
  };

  schedule();
  return () => {
    cancelled = true;
    if (timer !== null) scheduler.clearTimeout(timer);
  };
}

export function tryAcquireRequestLock(lock) {
  if (!lock || lock.current) return false;
  lock.current = true;
  return true;
}

export function releaseRequestLock(lock) {
  if (lock) lock.current = false;
}

export function investigationRequestError(error, fallback) {
  const apiError = error?.data?.error;
  if (apiError?.code === 'SESSION_BUSY' || apiError?.message === 'session already has a running request') {
    return '上一条追问正在处理中，请等待完成';
  }
  return apiError?.message || fallback;
}

export function createReloadScheduler(reload, delayMs, scheduler = globalThis) {
  let timer = null;
  return {
    queue() {
      if (timer !== null) return;
      timer = scheduler.setTimeout(() => {
        timer = null;
        reload();
      }, delayMs);
    },
    cancel() {
      if (timer !== null) scheduler.clearTimeout(timer);
      timer = null;
    },
  };
}

function markdownCells(line) {
  return line.trim().replace(/^\|/, '').replace(/\|$/, '').split('|').map((cell) => cell.trim());
}

function markdownTableSeparator(line) {
  const cells = markdownCells(line);
  return cells.length > 0 && cells.every((cell) => /^:?-{3,}:?$/.test(cell));
}

export function markdownBlocks(value, maxLength = 65536) {
  const lines = safeMarkdownText(value, maxLength).split(/\r?\n/);
  const blocks = [];
  let index = 0;
  while (index < lines.length) {
    const line = lines[index];
    if (!line.trim()) {
      index += 1;
      continue;
    }
    const fence = /^```([A-Za-z0-9_+-]{0,32})\s*$/.exec(line.trim());
    if (fence) {
      const content = [];
      index += 1;
      while (index < lines.length && !/^```\s*$/.test(lines[index].trim())) {
        content.push(lines[index]);
        index += 1;
      }
      if (index < lines.length) index += 1;
      blocks.push({ type: 'code', language: fence[1], text: content.join('\n') });
      continue;
    }
    const heading = /^(#{1,6})\s+(.+)$/.exec(line.trim());
    if (heading) {
      blocks.push({ type: 'heading', level: heading[1].length, text: heading[2] });
      index += 1;
      continue;
    }
    if (line.includes('|') && index + 1 < lines.length && markdownTableSeparator(lines[index + 1])) {
      const headers = markdownCells(line);
      const rows = [];
      index += 2;
      while (index < lines.length && lines[index].includes('|') && lines[index].trim()) {
        rows.push(markdownCells(lines[index]));
        index += 1;
      }
      blocks.push({ type: 'table', headers, rows });
      continue;
    }
    const list = /^\s*(?:([-*])|(\d+)\.)\s+(.+)$/.exec(line);
    if (list) {
      const ordered = Boolean(list[2]);
      const items = [];
      while (index < lines.length) {
        const item = /^\s*(?:([-*])|(\d+)\.)\s+(.+)$/.exec(lines[index]);
        if (!item || Boolean(item[2]) !== ordered) break;
        items.push(item[3]);
        index += 1;
      }
      blocks.push({ type: 'list', ordered, items });
      continue;
    }
    const paragraph = [line.trim()];
    index += 1;
    while (index < lines.length && lines[index].trim()
      && !/^(#{1,6})\s+/.test(lines[index].trim())
      && !/^```/.test(lines[index].trim())
      && !/^\s*(?:[-*]|\d+\.)\s+/.test(lines[index])) {
      if (lines[index].includes('|') && index + 1 < lines.length && markdownTableSeparator(lines[index + 1])) break;
      paragraph.push(lines[index].trim());
      index += 1;
    }
    blocks.push({ type: 'paragraph', text: paragraph.join(' ') });
  }
  return blocks;
}

export function holmesProxyRequestURL(base, path, parameters = {}) {
  const query = new URLSearchParams(parameters);
  query.set('_path', path);
  return `${base}?${query.toString()}`;
}

export function safeMarkdownText(value, maxLength = 65536) {
  const text = String(value ?? '')
    .replace(/<[^>]*>/g, '')
    .replace(/!\[[^\]]*\]\([^)]*\)/g, '[外链图片已隐藏]')
    .replace(/\[([^\]]+)\]\((?:javascript|data|vbscript):[^)]*\)/gi, '$1')
    .replace(/[\u0000-\u0008\u000b\u000c\u000e-\u001f]/g, '');
  return text.length > maxLength ? `${text.slice(0, maxLength)}…` : text;
}

export function resolveTime(value, now = Date.now()) {
  const raw = String(value || '');
  if (raw === 'now' || raw === '') {
    return new Date(now).toISOString();
  }
  const relative = /^now-(\d+)([mhd])$/.exec(raw);
  if (relative) {
    const units = { m: 60_000, h: 3_600_000, d: 86_400_000 };
    return new Date(now - Number(relative[1]) * units[relative[2]]).toISOString();
  }
  const parsed = Date.parse(raw);
  return Number.isFinite(parsed) ? new Date(parsed).toISOString() : new Date(now).toISOString();
}

export function boundedRange(fromValue, toValue, now = Date.now()) {
  const to = Date.parse(resolveTime(toValue, now));
  const requestedFrom = Date.parse(resolveTime(fromValue || 'now-1h', now));
  const from = Math.max(requestedFrom, to - 24 * 60 * 60 * 1000);
  return { from: new Date(from).toISOString(), to: new Date(to).toISOString() };
}

export function toolSummary(event) {
  const data = event?.data || {};
  return {
    id: data.tool_call_id || data.id || `event-${event?.id || 0}`,
    name: data.name || data.tool_name || 'unknown_tool',
    status: data.result?.status || (event?.type === 'tool_started' ? 'running' : 'finished'),
    startedAt: data.started_at || event?.at || '',
    durationMs: Number.isFinite(Number(data.duration_ms)) ? Number(data.duration_ms) : null,
    detail: safeMarkdownText(JSON.stringify(data.result || data, null, 2), 8192),
  };
}

export function activeAlertContext(samples, node = '') {
  const alerts = Array.isArray(samples) ? samples.filter((sample) => sample?.metric && typeof sample.metric === 'object') : [];
  alerts.sort((left, right) => JSON.stringify(left.metric).localeCompare(JSON.stringify(right.metric)));
  const selected = alerts.find((sample) => node && sample.metric.node === node) || alerts[0];
  if (!selected) return { labels: {}, fingerprint: '' };
  const labels = Object.fromEntries(Object.entries(selected.metric)
    .filter(([key, value]) => key && typeof value === 'string')
    .slice(0, 40));
  return {
    labels,
    fingerprint: String(labels.alert_fingerprint || labels.fingerprint || '').slice(0, 256),
  };
}

export function approvalPayload(callID, approved, requestID) {
  if (!callID || !requestID) {
    throw new Error('tool call and request IDs are required');
  }
  return { request_id: requestID, tool_call_id: callID, approved: Boolean(approved) };
}

export function prometheusSamples(payload) {
  const results = payload?.data?.result;
  if (!Array.isArray(results)) return [];
  return results.map((result) => ({
    metric: result?.metric || {},
    value: Number(result?.value?.[1]),
    sampledAt: Number(result?.value?.[0]) * 1000,
  })).filter((sample) => Number.isFinite(sample.value) && Number.isFinite(sample.sampledAt));
}

export function formatBytes(value) {
  const bytes = Number(value);
  if (!Number.isFinite(bytes)) return '无数据';
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB'];
  let scaled = bytes;
  let index = 0;
  while (Math.abs(scaled) >= 1024 && index < units.length - 1) {
    scaled /= 1024;
    index += 1;
  }
  return `${scaled.toFixed(index > 1 ? 2 : 1)} ${units[index]}`;
}
