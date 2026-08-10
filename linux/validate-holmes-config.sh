#!/usr/bin/env bash
set -euo pipefail

PROJECT_ROOT=/data/node_monitor
RUNTIME_ROOT=/opt/erlang-monitor

for path in \
  "${PROJECT_ROOT}/secrets/holmes_api_key" \
  "${PROJECT_ROOT}/secrets/holmes_tool_api_token" \
  "${PROJECT_ROOT}/secrets/glm_api_key"; do
  if [[ ! -s "${path}" ]]; then
    echo "Required native Holmes secret is missing or empty: ${path}" >&2
    exit 1
  fi
  mode=$(stat -c '%a' "${path}")
  if (( 10#${mode: -1} != 0 )); then
    echo "Secret must not be accessible to other users: ${path} mode=${mode}" >&2
    exit 1
  fi
done

test -s "${PROJECT_ROOT}/holmes/model_list.local.yaml"
test -s "${PROJECT_ROOT}/holmes/native/config.yaml"
test -s "${PROJECT_ROOT}/holmes/gateway.native.yml"
test -x "${RUNTIME_ROOT}/holmesgpt/.venv/bin/python"
test -x "${RUNTIME_ROOT}/bin/holmes-gateway"

bash "${PROJECT_ROOT}/linux/run-holmes.sh" --check
bash "${PROJECT_ROOT}/linux/run-holmes-gateway.sh" --check

echo "Native Holmes and Gateway configuration is valid."
