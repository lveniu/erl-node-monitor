#!/usr/bin/env bash
set -euo pipefail

PROJECT_ROOT=/data/node_monitor
RUNTIME_ROOT=/opt/erlang-monitor
PASSWORD_FILE=${PROJECT_ROOT}/secrets/grafana_admin_password
OPS_AGENT_TOOL_TOKEN_FILE=${PROJECT_ROOT}/secrets/ops_agent_tool_api_token

if [[ ! -s "${PASSWORD_FILE}" ]]; then
  echo "Grafana admin password file is missing or empty: ${PASSWORD_FILE}" >&2
  exit 1
fi

grafana_admin_password=$(tr -d '\r\n' <"${PASSWORD_FILE}")
if [[ -z ${grafana_admin_password} ]]; then
  echo "Grafana admin password file contains no usable value" >&2
  exit 1
fi
export GF_SECURITY_ADMIN_PASSWORD="${grafana_admin_password}"
unset grafana_admin_password

if [[ -s "${OPS_AGENT_TOOL_TOKEN_FILE}" ]]; then
  OPS_AGENT_TOOL_API_TOKEN=$(tr -d '\r\n' <"${OPS_AGENT_TOOL_TOKEN_FILE}")
  if [[ -z ${OPS_AGENT_TOOL_API_TOKEN} ]]; then
    echo "Ops Agent Tool Token file contains no usable value" >&2
    exit 1
  fi
  export OPS_AGENT_TOOL_API_TOKEN
fi

exec "${RUNTIME_ROOT}/grafana/bin/grafana" server \
  --homepath="${RUNTIME_ROOT}/grafana" \
  --config="${PROJECT_ROOT}/grafana/grafana.local.ini"
