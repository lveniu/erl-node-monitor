#!/usr/bin/env bash
set -euo pipefail

PROJECT_ROOT=/data/node_monitor
key=$(tr -d '\r\n' <"${PROJECT_ROOT}/secrets/holmes_api_key")
if [[ -z "${key}" ]]; then
  echo "Holmes API key is empty" >&2
  exit 1
fi

curl --fail --silent --show-error --max-time 5 http://127.0.0.1:20905/healthz
printf '\n'
curl --fail --silent --show-error --max-time 5 http://127.0.0.1:20905/readyz
printf '\n'
curl --fail --silent --show-error --max-time 5 \
  -H "X-API-Key: ${key}" \
  http://127.0.0.1:20905/api/model
printf '\n'
curl --fail --silent --show-error --max-time 5 http://127.0.0.1:20904/healthz
printf '\n'

echo "Native Holmes and Gateway health checks passed."
