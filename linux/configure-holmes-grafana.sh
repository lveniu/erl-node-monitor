#!/usr/bin/env bash
set -euo pipefail

PROJECT_ROOT=/data/node_monitor
RUNTIME_ROOT=/opt/erlang-monitor
GRAFANA_URL=http://127.0.0.1:20900
PLUGIN_ID=erlang-monitor-controls-app
PASSWORD_FILE=${PROJECT_ROOT}/secrets/grafana_admin_password
TOOL_TOKEN_FILE=${PROJECT_ROOT}/secrets/holmes_tool_api_token
BACKUP_FILE=

usage() {
  echo "Usage: configure-holmes-grafana.sh [--backup FILE]" >&2
}

if [[ ${EUID} -ne 0 ]]; then
  echo "configure-holmes-grafana.sh must run as root" >&2
  exit 1
fi
if [[ $# -gt 0 ]]; then
  if [[ $# -ne 2 || $1 != "--backup" || -z $2 ]]; then
    usage
    exit 2
  fi
  BACKUP_FILE=$2
fi
for path in "${PASSWORD_FILE}" "${TOOL_TOKEN_FILE}"; do
  if [[ ! -s "${path}" ]]; then
    echo "Required Grafana/Holmes secret is missing or empty: ${path}" >&2
    exit 1
  fi
done
if [[ ! -x "${RUNTIME_ROOT}/python/bin/python3.11" ]]; then
  echo "Native Python runtime is unavailable" >&2
  exit 1
fi

temp_dir=$(mktemp -d /tmp/holmes-grafana-config.XXXXXX)
cleanup() {
  case "${temp_dir}" in
    /tmp/holmes-grafana-config.*) rm -rf -- "${temp_dir}" ;;
    *) echo "Refusing temporary cleanup outside the expected path: ${temp_dir}" >&2 ;;
  esac
}
trap cleanup EXIT
chmod 0700 "${temp_dir}"

"${RUNTIME_ROOT}/python/bin/python3.11" - \
  "${PASSWORD_FILE}" "${TOOL_TOKEN_FILE}" \
  "${temp_dir}/curl.conf" "${temp_dir}/settings.json" <<'PY'
import json
import pathlib
import sys

password = pathlib.Path(sys.argv[1]).read_text(encoding="utf-8").strip()
token = pathlib.Path(sys.argv[2]).read_text(encoding="utf-8").strip()
if not password or not token:
    raise SystemExit("Grafana/Holmes secret contains no usable value")
if any(character in password or character in token for character in "\r\n"):
    raise SystemExit("Grafana/Holmes secret must contain exactly one line")

escaped_password = password.replace("\\", "\\\\").replace('"', '\\"')
pathlib.Path(sys.argv[3]).write_text(
    f'user = "admin:{escaped_password}"\n', encoding="utf-8"
)
with pathlib.Path(sys.argv[4]).open("w", encoding="utf-8") as stream:
    json.dump(
        {
            "enabled": True,
            "jsonData": {
                "collectorUrl": "http://127.0.0.1:20903",
                "holmesGatewayUrl": "http://127.0.0.1:20904",
            },
            "secureJsonData": {"holmesToolToken": token},
        },
        stream,
        separators=(",", ":"),
    )
PY
chmod 0600 "${temp_dir}/curl.conf" "${temp_dir}/settings.json"

main_pid=$(systemctl show -p MainPID erlang-monitor-grafana.service | sed 's/^MainPID=//')
if [[ ! ${main_pid} =~ ^[1-9][0-9]*$ || ! -r /proc/${main_pid}/environ ]]; then
  echo "Grafana process environment is unavailable" >&2
  exit 1
fi
grafana_domain=$(tr '\0' '\n' <"/proc/${main_pid}/environ" | \
  sed -n 's/^GF_SERVER_DOMAIN=//p' | tail -n1)
curl_headers=()
if [[ -n "${grafana_domain}" ]]; then
  if [[ ! ${grafana_domain} =~ ^[A-Za-z0-9.-]+$ ]]; then
    echo "Grafana domain contains unsafe characters" >&2
    exit 1
  fi
  curl_headers+=(
    -H "Host: ${grafana_domain}"
    -H "X-Forwarded-Host: ${grafana_domain}"
    -H "X-Forwarded-Proto: https"
  )
fi

settings_url=${GRAFANA_URL}/api/plugins/${PLUGIN_ID}/settings
if [[ -n "${BACKUP_FILE}" ]]; then
  install -d -o root -g root -m 0700 "$(dirname "${BACKUP_FILE}")"
  curl --fail --silent --show-error --max-time 10 \
    --config "${temp_dir}/curl.conf" \
    "${curl_headers[@]}" \
    "${settings_url}" >"${BACKUP_FILE}"
  chmod 0600 "${BACKUP_FILE}"
fi

http_code=$(curl --silent --show-error --max-time 10 \
  --config "${temp_dir}/curl.conf" \
  "${curl_headers[@]}" \
  -H 'Content-Type: application/json' \
  --request POST \
  --data-binary @"${temp_dir}/settings.json" \
  --output "${temp_dir}/response.json" \
  --write-out '%{http_code}' \
  "${settings_url}")
if [[ "${http_code}" != 200 ]]; then
  echo "Grafana app settings update failed with HTTP ${http_code}" >&2
  exit 1
fi

curl --fail --silent --show-error --max-time 10 \
  --config "${temp_dir}/curl.conf" \
  "${curl_headers[@]}" \
  "${settings_url}" >"${temp_dir}/verified-settings.json"
"${RUNTIME_ROOT}/python/bin/python3.11" - "${temp_dir}/verified-settings.json" <<'PY'
import json
import pathlib
import sys

settings = json.loads(pathlib.Path(sys.argv[1]).read_text(encoding="utf-8"))
if not settings.get("enabled"):
    raise SystemExit("Grafana Holmes app is not enabled")
json_data = settings.get("jsonData") or {}
if json_data.get("holmesGatewayUrl") != "http://127.0.0.1:20904":
    raise SystemExit("Grafana Holmes Gateway URL is not configured")
if not (settings.get("secureJsonFields") or {}).get("holmesToolToken"):
    raise SystemExit("Grafana Holmes Tool Token was not stored securely")
PY

curl --fail --silent --show-error --max-time 10 \
  --config "${temp_dir}/curl.conf" \
  "${curl_headers[@]}" \
  "${GRAFANA_URL}/api/plugin-proxy/${PLUGIN_ID}/holmes-health" >/dev/null

curl --fail --silent --show-error --max-time 10 \
  --config "${temp_dir}/curl.conf" \
  "${curl_headers[@]}" \
  "${GRAFANA_URL}/api/plugin-proxy/${PLUGIN_ID}/holmes?_path=%2Fmodels" \
  >"${temp_dir}/proxy-models.json"
"${RUNTIME_ROOT}/python/bin/python3.11" - "${temp_dir}/proxy-models.json" <<'PY'
import json
import pathlib
import sys

response = json.loads(pathlib.Path(sys.argv[1]).read_text(encoding="utf-8"))
models = response.get("models") or []
if not models or not all(model.get("alias") for model in models):
    raise SystemExit("Grafana Holmes proxy returned no allowed models")
PY

echo "Grafana Holmes proxy settings are active; Grafana was not restarted."
