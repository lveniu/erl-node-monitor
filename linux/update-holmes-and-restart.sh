#!/usr/bin/env bash
set -euo pipefail

PROJECT_ROOT=/data/node_monitor
SVN_USERNAME=qt01_server_rebuild
SVN_PASSWORD_FILE=/data/save/${SVN_USERNAME}
LOCK_FILE=/run/lock/node-monitor-holmes-update.lock
SERVICES=(
  erlang-monitor-exporter.service
  erlang-monitor-alertmanager.service
  erlang-monitor-prometheus.service
  erlang-monitor-grafana.service
)

usage() {
  echo "Usage: sudo bash /data/node_monitor/linux/update-holmes-and-restart.sh --revision REVISION" >&2
}

if [[ ${EUID} -ne 0 ]]; then
  echo "update-holmes-and-restart.sh must run as root" >&2
  exit 1
fi
if [[ ${1:-} != "--revision" || ! ${2:-} =~ ^[0-9]+$ ]]; then
  usage
  exit 2
fi
target_revision=$2

for command_name in svn systemctl systemd-analyze curl flock sha256sum; do
  if ! command -v "${command_name}" >/dev/null 2>&1; then
    echo "Required command is missing: ${command_name}" >&2
    exit 1
  fi
done
if [[ ! -d "${PROJECT_ROOT}/.svn" || ! -s "${SVN_PASSWORD_FILE}" ]]; then
  echo "SVN working copy or deployment password is unavailable" >&2
  exit 1
fi

exec 9>"${LOCK_FILE}"
if ! flock -n 9; then
  echo "Another Holmes update is already running" >&2
  exit 1
fi

for service in "${SERVICES[@]}"; do
  if ! systemctl is-active --quiet "${service}"; then
    echo "Existing monitoring service is not active before Holmes deployment: ${service}" >&2
    exit 1
  fi
done

revision_before=$(svn info "${PROJECT_ROOT}" | awk -F': ' '/^Revision:/ { print $2; exit }')
timestamp=$(date +%Y%m%d-%H%M%S)
backup_root=${PROJECT_ROOT}/data/deploy-backups/${timestamp}-holmes
install -d -o root -g root -m 0700 "${backup_root}"
for relative in holmes/model_list.local.yaml; do
  if [[ -e "${PROJECT_ROOT}/${relative}" ]]; then
    cp -a --parents "${PROJECT_ROOT}/${relative}" "${backup_root}/"
  fi
done
printf '%s\n' "${revision_before}" >"${backup_root}/svn-revision-before-update"

svn_password=$(tr -d '\r\n' <"${SVN_PASSWORD_FILE}")
svn update -r "${target_revision}" "${PROJECT_ROOT}" \
  --username "${SVN_USERNAME}" \
  --password "${svn_password}" \
  --no-auth-cache \
  --non-interactive \
  --trust-server-cert
unset svn_password

if svn status --xml "${PROJECT_ROOT}" | grep -q 'item="conflicted"'; then
  echo "SVN update left conflicts; no service was restarted" >&2
  exit 1
fi
revision_after=$(svn info "${PROJECT_ROOT}" | awk -F': ' '/^Revision:/ { print $2; exit }')
if [[ "${revision_after}" != "${target_revision}" ]]; then
  echo "Exact revision deployment failed: requested ${target_revision}, got ${revision_after}" >&2
  exit 1
fi

bash "${PROJECT_ROOT}/linux/install-holmes-runtime.sh"
bash "${PROJECT_ROOT}/linux/validate-holmes-config.sh"
bash "${PROJECT_ROOT}/linux/install-holmes-services.sh"
systemctl enable erlang-monitor-holmes.service erlang-monitor-holmes-gateway.service

systemctl restart erlang-monitor-holmes.service
for _ in $(seq 1 180); do
  if curl --fail --silent --max-time 3 http://127.0.0.1:20905/healthz >/dev/null; then
    break
  fi
  if ! systemctl is-active --quiet erlang-monitor-holmes.service; then
    systemctl --no-pager --full status erlang-monitor-holmes.service || true
    exit 1
  fi
  sleep 1
done
curl --fail --silent --show-error --max-time 5 http://127.0.0.1:20905/healthz >/dev/null

systemctl restart erlang-monitor-holmes-gateway.service
for _ in $(seq 1 60); do
  if curl --fail --silent --max-time 3 http://127.0.0.1:20904/healthz >/dev/null; then
    break
  fi
  if ! systemctl is-active --quiet erlang-monitor-holmes-gateway.service; then
    systemctl --no-pager --full status erlang-monitor-holmes-gateway.service || true
    exit 1
  fi
  sleep 1
done

bash "${PROJECT_ROOT}/linux/configure-holmes-grafana.sh" \
  --backup "${backup_root}/grafana-holmes-app-settings-before.json"
bash "${PROJECT_ROOT}/linux/holmes-health-check.sh"
for service in "${SERVICES[@]}"; do
  if ! systemctl is-active --quiet "${service}"; then
    echo "Existing monitoring service changed state during Holmes deployment: ${service}" >&2
    exit 1
  fi
done

echo "Native Holmes deployment completed: ${revision_before} -> ${revision_after}"
echo "Only Holmes and Holmes Gateway were restarted; Nginx and ports 20900-20903 were untouched."
echo "Configuration backup: ${backup_root}"
