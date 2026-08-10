#!/usr/bin/env bash
set -euo pipefail

PROJECT_ROOT=/data/node_monitor
RUNTIME_ROOT=/opt/erlang-monitor
EXPORTER=${RUNTIME_ROOT}/bin/erlang-exporter
OPS_AGENT=${RUNTIME_ROOT}/bin/ops-agent

if [[ -x "${PROJECT_ROOT}/linux/bin/erlang-exporter" ]]; then
  EXPORTER=${PROJECT_ROOT}/linux/bin/erlang-exporter
fi
if [[ -x "${PROJECT_ROOT}/linux/bin/ops-agent" ]]; then
  OPS_AGENT=${PROJECT_ROOT}/linux/bin/ops-agent
fi

"${EXPORTER}" \
  -config "${PROJECT_ROOT}/config/servers.native.yml" \
  -check-config
"${OPS_AGENT}" \
  -config "${PROJECT_ROOT}/ops-agent/config.native.yml" \
  -servers "${PROJECT_ROOT}/config/servers.native.yml" \
  -check-config
"${RUNTIME_ROOT}/prometheus/promtool" check config \
  "${PROJECT_ROOT}/prometheus/prometheus.local.yml"
"${RUNTIME_ROOT}/alertmanager/amtool" check-config \
  "${PROJECT_ROOT}/alertmanager/alertmanager.local.yml"

test -s "${PROJECT_ROOT}/secrets/grafana_admin_password"
test -s "${PROJECT_ROOT}/grafana/grafana.local.ini"
test -d "${PROJECT_ROOT}/grafana/provisioning-local"
test -d "${PROJECT_ROOT}/grafana/plugins/erlang-monitor-controls-app"
test -s "${PROJECT_ROOT}/secrets/glm_api_key"
test -s "${PROJECT_ROOT}/secrets/holmes_tool_api_token"

echo "Native Linux monitoring configuration is valid."
