#!/usr/bin/env bash
set -euo pipefail

PROJECT_ROOT=/data/node_monitor
RUNTIME_ROOT=/opt/erlang-monitor
SECRET_ROOT=${PROJECT_ROOT}/secrets
CONFIG=${PROJECT_ROOT}/holmes/gateway.native.yml
SERVERS=${PROJECT_ROOT}/config/servers.native.yml

if [[ ${1:-} == "--check" ]]; then
  exec "${RUNTIME_ROOT}/bin/holmes-gateway" \
    -config "${CONFIG}" \
    -servers "${SERVERS}" \
    -check-config
fi

read_secret() {
  local path=$1
  if [[ ! -s "${path}" ]]; then
    echo "Required Holmes Gateway secret is missing or empty: ${path}" >&2
    exit 1
  fi
  tr -d '\r\n' <"${path}"
}

export HOLMES_API_KEY
export HOLMES_TOOL_API_TOKEN
HOLMES_API_KEY=$(read_secret "${SECRET_ROOT}/holmes_api_key")
HOLMES_TOOL_API_TOKEN=$(read_secret "${SECRET_ROOT}/holmes_tool_api_token")

exec "${RUNTIME_ROOT}/bin/holmes-gateway" \
  -config "${CONFIG}" \
  -servers "${SERVERS}" \
  -listen 127.0.0.1:20904 \
  -data-dir "${PROJECT_ROOT}/data/holmes"
