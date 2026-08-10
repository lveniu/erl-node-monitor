export function serverOptions(payload) {
  const items = Array.isArray(payload?.servers) ? payload.servers : [];
  return items
    .filter((item) => typeof item?.server_id === 'string' && item.server_id.trim() && typeof item?.display_name === 'string' && item.display_name.trim())
    .map((item) => ({ id: item.server_id.trim(), name: item.display_name.trim() }));
}

export function preferredServer(servers, requested) {
  const value = typeof requested === 'string' ? requested.trim() : '';
  if (!value) return null;
  return servers.find((server) => server.id === value || server.name === value) || null;
}

export function skillSummaries(payload) {
  const items = Array.isArray(payload?.skills) ? payload.skills : [];
  return items
    .filter((item) => typeof item?.name === 'string' && item.name.trim() && typeof item?.description === 'string' && item.description.trim())
    .map((item) => ({ name: item.name.trim(), description: item.description.trim() }));
}

export function withTaskID(url, taskID) {
  const next = new URL(url);
  const value = typeof taskID === 'string' ? taskID.trim() : '';
  if (value) next.searchParams.set('task_id', value);
  else next.searchParams.delete('task_id');
  return next.toString();
}
