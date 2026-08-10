#!/usr/bin/env bash
set -euo pipefail

PROJECT_ROOT=/data/node_monitor
SECRET_ROOT=${PROJECT_ROOT}/secrets

export_if_present() {
  local name=$1
  local path=$2
  if [[ -s "${path}" ]]; then
    export "${name}=${path}"
  fi
}

export_if_present DINGTALK_WEBHOOK_URL_FILE "${SECRET_ROOT}/dingtalk_webhook_url"
export_if_present DINGTALK_SECRET_FILE "${SECRET_ROOT}/dingtalk_secret"
export_if_present DINGTALK_AT_MOBILES_FILE "${SECRET_ROOT}/dingtalk_at_mobiles"
export_if_present DINGTALK_AT_USER_IDS_FILE "${SECRET_ROOT}/dingtalk_at_user_ids"

exec /opt/erlang-monitor/bin/erlang-exporter "$@"
