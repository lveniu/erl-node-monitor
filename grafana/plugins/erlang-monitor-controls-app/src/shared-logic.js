export function safeMarkdownText(value, maxLength = 65536) {
  const text = String(value ?? '')
    .replace(/<[^>]*>/g, '')
    .replace(/!\[[^\]]*\]\([^)]*\)/g, '[外链图片已隐藏]')
    .replace(/\[([^\]]+)\]\((?:javascript|data|vbscript):[^)]*\)/gi, '$1')
    .replace(/[\u0000-\u0008\u000b\u000c\u000e-\u001f]/g, '');
  return text.length > maxLength ? `${text.slice(0, maxLength)}…` : text;
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
