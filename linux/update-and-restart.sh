#!/usr/bin/env bash
set -euo pipefail

PROJECT_ROOT=/home/qt/node_monitor
SVN_USERNAME=qt01_server_rebuild
SVN_BIN=/home/tools/subversion/bin/svn
SVN_PASSWORD_FILE=/home/save/${SVN_USERNAME}
SCRIPT_PATH=${PROJECT_ROOT}/linux/update-and-restart.sh
LOCK_FILE=/run/lock/node-monitor-update-and-restart.lock
SSH_AGENT_USER=erlang-monitor
SSH_AGENT_DIR=/run/erlang-monitor-ssh-agent
SSH_AUTH_SOCK_PATH=${SSH_AGENT_DIR}/agent.sock
SSH_AGENT_PID_FILE=${SSH_AGENT_DIR}/agent.pid
SSH_PRIVATE_KEY=${PROJECT_ROOT}/secrets/ssh/qthy@liujinxin
SSH_PUBLIC_KEY=${PROJECT_ROOT}/secrets/ssh/qthy@liujinxin.pub
SSH_AGENT_KEY_COPY=${SSH_AGENT_DIR}/qthy@liujinxin

SERVICES=(
  erlang-monitor-exporter.service
  erlang-monitor-alertmanager.service
  erlang-monitor-prometheus.service
  erlang-monitor-ops-agent.service
  erlang-monitor-grafana.service
)

usage() {
  cat <<'EOF'
Usage: sudo bash /home/qt/node_monitor/linux/update-and-restart.sh [--revision REVISION]

Updates the /home/qt/node_monitor SVN working copy, validates the native Linux
configuration, installs/enables the five monitoring units, restarts them in
dependency order, and checks their loopback health endpoints.

This script never commits to SVN and never restarts Nginx.
If the encrypted external-server key is not already loaded, it starts a
dedicated SSH Agent and interactively runs ssh-add. Run the script from a TTY.
EOF
}

revision_of() {
  "${SVN_BIN}" info "${PROJECT_ROOT}" | awk -F': ' '/^(Revision|版本):/ { print $2; exit }'
}

backup_local_config() {
  local revision=$1
  local timestamp backup_root relative
  timestamp=$(date +%Y%m%d-%H%M%S)
  backup_root=${PROJECT_ROOT}/data/deploy-backups/${timestamp}
  install -d -o root -g root -m 0700 "${backup_root}"

  cd "${PROJECT_ROOT}"
  for relative in \
    config/servers.native.yml \
    config/servers.local.yml \
    prometheus/prometheus.local.yml \
    prometheus/rules \
    alertmanager/alertmanager.local.yml \
    grafana/grafana.local.ini \
    grafana/provisioning-local; do
    if [[ -e "${relative}" ]]; then
      cp -a --parents "${relative}" "${backup_root}/"
    fi
  done
  printf '%s\n' "${revision}" >"${backup_root}/svn-revision-before-update"
  printf '%s\n' "${backup_root}"
}

wait_http() {
  local name=$1
  local url=$2
  local attempts=$3
  local attempt

  for ((attempt = 1; attempt <= attempts; attempt++)); do
    if curl --fail --silent --show-error --max-time 3 "${url}" >/dev/null 2>&1; then
      echo "${name} is ready: ${url}"
      return 0
    fi
    if ! systemctl is-active --quiet "${name}"; then
      systemctl --no-pager --full status "${name}" || true
      return 1
    fi
    sleep 1
  done

  echo "${name} did not become ready: ${url}" >&2
  systemctl --no-pager --full status "${name}" || true
  return 1
}

ssh_agent_command() {
  runuser -u "${SSH_AGENT_USER}" -- env \
    SSH_AUTH_SOCK="${SSH_AUTH_SOCK_PATH}" \
    "$@"
}

ssh_agent_is_reachable() {
  local result

  set +e
  ssh_agent_command ssh-add -l >/dev/null 2>&1
  result=$?
  set -e
  [[ ${result} -eq 0 || ${result} -eq 1 ]]
}

start_ssh_agent() {
  local output pid

  rm -f "${SSH_AUTH_SOCK_PATH}"
  output=$(runuser -u "${SSH_AGENT_USER}" -- \
    ssh-agent -a "${SSH_AUTH_SOCK_PATH}" -s 9>&-)
  pid=$(printf '%s\n' "${output}" | sed -n \
    's/^SSH_AGENT_PID=\([0-9][0-9]*\);.*$/\1/p')
  if [[ -z ${pid} ]]; then
    echo "Unable to determine the dedicated SSH Agent PID" >&2
    return 1
  fi
  if [[ -e /proc/${pid}/fd/9 ]]; then
    echo "Dedicated SSH Agent inherited the deployment lock descriptor" >&2
    kill "${pid}" 2>/dev/null || true
    return 1
  fi
  printf '%s\n' "${pid}" >"${SSH_AGENT_PID_FILE}"
  chown "${SSH_AGENT_USER}:${SSH_AGENT_USER}" "${SSH_AGENT_PID_FILE}"
  chmod 0600 "${SSH_AGENT_PID_FILE}"
}

prepare_ssh_agent() {
  local expected_fingerprint

  if [[ ! -s "${SSH_PRIVATE_KEY}" ]]; then
    echo "External-server SSH private key is missing: ${SSH_PRIVATE_KEY}" >&2
    return 1
  fi
  if [[ ! -s "${SSH_PUBLIC_KEY}" ]]; then
    echo "External-server SSH public key is missing: ${SSH_PUBLIC_KEY}" >&2
    return 1
  fi

  install -d -o "${SSH_AGENT_USER}" -g "${SSH_AGENT_USER}" -m 0700 \
    "${SSH_AGENT_DIR}"
  if ! ssh_agent_is_reachable; then
    echo "Starting dedicated SSH Agent at ${SSH_AUTH_SOCK_PATH}"
    start_ssh_agent
  fi

  tr -d '\r' <"${SSH_PRIVATE_KEY}" >"${SSH_AGENT_KEY_COPY}"
  chown "${SSH_AGENT_USER}:${SSH_AGENT_USER}" "${SSH_AGENT_KEY_COPY}"
  chmod 0600 "${SSH_AGENT_KEY_COPY}"
  expected_fingerprint=$(ssh-keygen -lf "${SSH_PUBLIC_KEY}" | \
    awk 'NR == 1 { print $2 }')
  if [[ -z ${expected_fingerprint} ]]; then
    echo "Unable to read the external-server SSH key fingerprint" >&2
    rm -f "${SSH_AGENT_KEY_COPY}"
    return 1
  fi

  if ssh_agent_command ssh-add -l 2>/dev/null | \
      awk '{ print $2 }' | grep -Fxq "${expected_fingerprint}"; then
    echo "External-server SSH key is already loaded: ${expected_fingerprint}"
    rm -f "${SSH_AGENT_KEY_COPY}"
    return 0
  fi

  if [[ ! -t 0 ]]; then
    echo "External-server SSH key must be unlocked interactively." >&2
    echo "Run this update script from a terminal with a TTY." >&2
    rm -f "${SSH_AGENT_KEY_COPY}"
    return 1
  fi

  echo "Loading external-server SSH key into the dedicated Agent"
  if ! ssh_agent_command ssh-add "${SSH_AGENT_KEY_COPY}"; then
    rm -f "${SSH_AGENT_KEY_COPY}"
    return 1
  fi
  rm -f "${SSH_AGENT_KEY_COPY}"

  if ! ssh_agent_command ssh-add -l 2>/dev/null | \
      awk '{ print $2 }' | grep -Fxq "${expected_fingerprint}"; then
    echo "ssh-add completed but the expected key is not loaded" >&2
    return 1
  fi
  echo "External-server SSH key loaded: ${expected_fingerprint}"
}

if [[ ${1:-} == "--help" || ${1:-} == "-h" ]]; then
  usage
  exit 0
fi

target_revision=
if [[ ${1:-} == "--revision" ]]; then
  if [[ ! ${2:-} =~ ^[0-9]+$ ]]; then
    usage >&2
    exit 2
  fi
  target_revision=$2
elif [[ -n ${1:-} && ${1:-} != "--after-update" ]]; then
  usage >&2
  exit 2
fi

if [[ ${EUID} -ne 0 ]]; then
  echo "update-and-restart.sh must run as root" >&2
  exit 1
fi

if [[ ! -x "${SVN_BIN}" ]]; then
  echo "Required SVN client is missing or not executable: ${SVN_BIN}" >&2
  exit 1
fi

for command_name in \
  systemctl systemd-analyze curl flock runuser \
  ssh-agent ssh-add ssh-keygen awk grep sed tr; do
  if ! command -v "${command_name}" >/dev/null 2>&1; then
    echo "Required command is missing: ${command_name}" >&2
    exit 1
  fi
done

exec 9>"${LOCK_FILE}"
if ! flock -n 9; then
  echo "Another node_monitor update or restart is already running" >&2
  exit 1
fi

if [[ ${1:-} != "--after-update" ]]; then
  if [[ ! -d "${PROJECT_ROOT}/.svn" ]]; then
    echo "SVN working copy is missing: ${PROJECT_ROOT}" >&2
    exit 1
  fi
  if [[ ! -s "${SVN_PASSWORD_FILE}" ]]; then
    echo "SVN password file is missing or empty: ${SVN_PASSWORD_FILE}" >&2
    exit 1
  fi

  revision_before=$(revision_of)
  backup_root=$(backup_local_config "${revision_before}")
  svn_password=$(tr -d '\r\n' <"${SVN_PASSWORD_FILE}")
  if [[ -z "${svn_password}" ]]; then
    echo "SVN password file contains no usable value" >&2
    exit 1
  fi

  echo "Updating ${PROJECT_ROOT} from SVN revision ${revision_before}"
  svn_update=("${SVN_BIN}" update "${PROJECT_ROOT}")
  if [[ -n ${target_revision} ]]; then
    svn_update+=(-r "${target_revision}")
  fi
  "${svn_update[@]}" \
      --username "${SVN_USERNAME}" \
      --password "${svn_password}" \
      --no-auth-cache \
      --non-interactive \
      --trust-server-cert
  unset svn_password

  if "${SVN_BIN}" status --xml "${PROJECT_ROOT}" | grep -q 'item="conflicted"'; then
    echo "SVN update left conflicts; monitoring services were not restarted" >&2
    echo "Pre-update configuration backup: ${backup_root}" >&2
    exit 1
  fi

  if [[ ! -f "${SCRIPT_PATH}" ]]; then
    echo "Updated restart script is missing: ${SCRIPT_PATH}" >&2
    exit 1
  fi

  exec bash "${SCRIPT_PATH}" --after-update "${revision_before}" "${backup_root}" "${target_revision}"
fi

revision_before=${2:-unknown}
backup_root=${3:-unknown}
target_revision=${4:-}
revision_after=$(revision_of)

if [[ -n ${target_revision} && ${revision_after} != "${target_revision}" ]]; then
  echo "Exact revision deployment failed: requested ${target_revision}, got ${revision_after}" >&2
  echo "Pre-update configuration backup: ${backup_root}" >&2
  exit 1
fi

echo "SVN update complete: ${revision_before} -> ${revision_after}"
echo "Validating project configuration before restart"
bash "${PROJECT_ROOT}/linux/validate-config.sh"

echo "Installing the validated monitoring runtime"
bash "${PROJECT_ROOT}/linux/install-runtime.sh"

echo "Preparing the external-server SSH Agent"
prepare_ssh_agent

echo "Installing and verifying systemd units"
bash "${PROJECT_ROOT}/linux/install-services.sh"
systemctl enable "${SERVICES[@]}"

if ! systemctl is-active --quiet chronyd; then
  echo "WARNING: chronyd is not active; enable time synchronization before production use" >&2
fi

echo "Stopping monitoring services"
systemctl stop erlang-monitor-grafana.service
systemctl stop erlang-monitor-ops-agent.service
systemctl stop erlang-monitor-prometheus.service
systemctl stop erlang-monitor-alertmanager.service
systemctl stop erlang-monitor-exporter.service

echo "Starting monitoring services"
systemctl start erlang-monitor-exporter.service
wait_http erlang-monitor-exporter.service http://127.0.0.1:20903/metrics 60

systemctl start erlang-monitor-alertmanager.service
wait_http erlang-monitor-alertmanager.service http://127.0.0.1:20902/-/ready 60

systemctl start erlang-monitor-prometheus.service
wait_http erlang-monitor-prometheus.service http://127.0.0.1:20901/-/ready 60

systemctl start erlang-monitor-ops-agent.service
wait_http erlang-monitor-ops-agent.service http://127.0.0.1:20906/healthz 60

systemctl start erlang-monitor-grafana.service
wait_http erlang-monitor-grafana.service http://127.0.0.1:20900/api/health 90

echo "Monitoring update and restart completed successfully."
echo "Configuration backup: ${backup_root}"
