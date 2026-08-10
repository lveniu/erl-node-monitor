#!/usr/bin/env bash
set -euo pipefail

PROJECT_ROOT=/data/node_monitor
RUNTIME_ROOT=/opt/erlang-monitor/holmesgpt
SECRET_ROOT=${PROJECT_ROOT}/secrets
MODEL_LIST=${PROJECT_ROOT}/holmes/model_list.local.yaml

read_secret() {
  local path=$1
  if [[ ! -s "${path}" ]]; then
    echo "Required Holmes secret is missing or empty: ${path}" >&2
    exit 1
  fi
  tr -d '\r\n' <"${path}"
}

if [[ ! -x "${RUNTIME_ROOT}/.venv/bin/python" || ! -f "${RUNTIME_ROOT}/server.py" ]]; then
  echo "Native Holmes virtual environment is not installed" >&2
  exit 1
fi
if [[ ! -s "${MODEL_LIST}" ]]; then
  echo "Holmes model list is missing: ${MODEL_LIST}" >&2
  exit 1
fi

export HOLMES_API_KEY
export GLM_API_KEY
HOLMES_API_KEY=$(read_secret "${SECRET_ROOT}/holmes_api_key")
GLM_API_KEY=$(read_secret "${SECRET_ROOT}/glm_api_key")
if [[ -s "${SECRET_ROOT}/kimi_api_key" ]]; then
  export KIMI_API_KEY
  KIMI_API_KEY=$(read_secret "${SECRET_ROOT}/kimi_api_key")
fi

export HOLMES_CONFIGPATH_DIR=${PROJECT_ROOT}/holmes/native
export MODEL_LIST_FILE_LOCATION=${MODEL_LIST}
export HOLMES_HOST=127.0.0.1
export HOLMES_PORT=20905
export HOLMES_TOOL_RESULT_STORAGE_ENABLED=false
export HOLMES_DISABLE_MCP_OAUTH=true
export TOOLSET_STATUS_REFRESH_INTERVAL_SECONDS=0
export ENABLE_JSON_LOGS_FORMAT=true
export LITELLM_LOCAL_MODEL_COST_MAP=true
export PYTHONUTF8=1
export PYTHONIOENCODING=utf-8

cd "${RUNTIME_ROOT}"
if [[ ${1:-} == "--check" ]]; then
  exec "${RUNTIME_ROOT}/.venv/bin/python" -c \
    'import server; executor=server.config.create_tool_executor(dal=server.dal,reuse_executor=True); names=sorted(t.name for t in executor.toolsets); assert names == ["core_investigation", "prometheus/metrics", "skills"], names; assert server.config.get_models_list(); print("Native Holmes configuration valid:", names)'
fi

exec "${RUNTIME_ROOT}/.venv/bin/python" -u "${RUNTIME_ROOT}/server.py"
