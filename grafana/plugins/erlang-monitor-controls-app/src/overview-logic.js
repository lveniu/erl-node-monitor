export function gibibytes(value) {
  const bytes = Number(value);
  return Number.isFinite(bytes) ? bytes / (1024 ** 3) : null;
}

export function fixed(value, decimals = 2) {
	if (value == null || value === '') return '无数据';
  const number = Number(value);
  return Number.isFinite(number) ? number.toFixed(decimals) : '无数据';
}

export function cpuCapacityPercent(logicalCPUs) {
  const cores = Number(logicalCPUs);
  return Number.isFinite(cores) && cores > 0 ? cores * 100 : null;
}

export function isMNodeInfrastructureNode(nodeName) {
	const shortName = String(nodeName || '').split('@')[0];
	return /(?:^|_)c\d+(?:_|$)/i.test(shortName);
}

export function mergeNodeSamples(upSamples, registeredSamples, onlineSamples, processSamples, residentMemorySamples, cpuRatioSamples, mnodeAvailableSamples = [], mnodeConnectionSamples = []) {
  const rows = new Map();
  const merge = (samples, key) => {
    for (const sample of Array.isArray(samples) ? samples : []) {
      const node = String(sample?.metric?.node || '');
      if (!node) continue;
	  const row = rows.get(node) || { node, up: null, registered: null, online: null, processes: null, residentMemoryBytes: null, cpuRatio: null, mnodeAvailable: null, connections: [] };
      row[key] = sample.value;
      rows.set(node, row);
    }
  };
  merge(upSamples, 'up');
  merge(registeredSamples, 'registered');
  merge(onlineSamples, 'online');
  merge(processSamples, 'processes');
	merge(residentMemorySamples, 'residentMemoryBytes');
	merge(cpuRatioSamples, 'cpuRatio');
	merge(mnodeAvailableSamples, 'mnodeAvailable');
	for (const sample of Array.isArray(mnodeConnectionSamples) ? mnodeConnectionSamples : []) {
		const sourceNode = String(sample?.metric?.node || '');
		const type = String(sample?.metric?.connection_type || '');
		if (!sourceNode || !['central', 'region'].includes(type)) continue;
		const row = rows.get(sourceNode) || { node: sourceNode, up: null, registered: null, online: null, processes: null, residentMemoryBytes: null, cpuRatio: null, mnodeAvailable: null, connections: [] };
		const state = Number(sample.value);
		row.connections.push({
			nodeID: String(sample?.metric?.node_id || ''),
			node: String(sample?.metric?.connection_node || ''),
			type,
			state: Number.isFinite(state) ? state : null,
			usable: state === 2,
		});
		rows.set(sourceNode, row);
	}
	for (const row of rows.values()) {
		row.connections.sort((left, right) => (left.type === right.type ? left.nodeID.localeCompare(right.nodeID) : left.type === 'central' ? -1 : 1));
	}
  return [...rows.values()].sort((left, right) => left.node.localeCompare(right.node));
}

function alertFingerprint(labels) {
  return [labels.alertname, labels.name, labels.node, labels.pid].filter(Boolean).join('|').slice(0, 512);
}

export function activeAlertsFromRules(payload, serverName) {
  const groups = Array.isArray(payload?.data?.groups) ? payload.data.groups : [];
  const alerts = [];
  for (const group of groups) {
    for (const rule of Array.isArray(group?.rules) ? group.rules : []) {
      for (const alert of Array.isArray(rule?.alerts) ? rule.alerts : []) {
        const labels = { ...(rule?.labels || {}), ...(alert?.labels || {}) };
        if (serverName && labels.name !== serverName) continue;
        if (!['firing', 'pending'].includes(String(alert?.state || rule?.state || ''))) continue;
        alerts.push({
          fingerprint: String(alert?.fingerprint || alertFingerprint(labels)),
          state: String(alert?.state || rule?.state || ''),
          labels,
          annotations: { ...(rule?.annotations || {}), ...(alert?.annotations || {}) },
          activeAt: String(alert?.activeAt || ''),
          value: Number(alert?.value),
        });
      }
    }
  }
  return alerts.sort((left, right) => {
    const severity = { critical: 0, warning: 1 };
    const severityOrder = (severity[left.labels.severity] ?? 2) - (severity[right.labels.severity] ?? 2);
    return severityOrder || left.activeAt.localeCompare(right.activeAt) || left.fingerprint.localeCompare(right.fingerprint);
  });
}

export function alertLabelText(labels) {
  const preferred = ['severity', 'server', 'name', 'node', 'pid', 'registered_name', 'initial_call', 'current_function'];
  const keys = [...preferred.filter((key) => labels?.[key] != null), ...Object.keys(labels || {}).filter((key) => !preferred.includes(key)).sort()];
  return keys.slice(0, 40).map((key) => `${key}=${String(labels[key])}`).join('，');
}

export function displayAlertValue(alert) {
  const annotated = String(alert?.annotations?.value || '').trim();
  if (annotated) return annotated;
  return Number.isFinite(alert?.value) ? fixed(alert.value) : '无数据';
}
