#!/usr/bin/env bash
set -euo pipefail

if [[ ${EUID} -ne 0 ]]; then
  echo "install-runtime.sh must run as root" >&2
  exit 1
fi

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
PROJECT_ROOT=/home/qt/node_monitor
INSTALL_ROOT=${PROJECT_ROOT}/runtime
CONFIG_ROOT=${PROJECT_ROOT}/config
DATA_ROOT=${PROJECT_ROOT}/data
SERVICE_USER=erlang-monitor
SERVICE_HOME=${PROJECT_ROOT}/service-home
PROMETHEUS_VERSION=3.5.0
ALERTMANAGER_VERSION=0.28.1
GRAFANA_VERSION=12.1.0

set_selinux_fcontext() {
  local type=$1
  local pattern=$2

  if ! semanage fcontext -a -t "${type}" "${pattern}" 2>/dev/null; then
    semanage fcontext -m -t "${type}" "${pattern}"
  fi
}

configure_selinux_contexts() {
  if ! command -v selinuxenabled >/dev/null 2>&1 || ! selinuxenabled; then
    return
  fi
  for command_name in semanage restorecon; do
    if ! command -v "${command_name}" >/dev/null 2>&1; then
      echo "SELinux is enabled but required command is missing: ${command_name}" >&2
      exit 1
    fi
  done

  set_selinux_fcontext usr_t '/home/qt/node_monitor(/.*)?'
  set_selinux_fcontext bin_t '/home/qt/node_monitor/runtime(/.*)?'
  set_selinux_fcontext bin_t '/home/qt/node_monitor/linux/.*\.sh'
  set_selinux_fcontext var_lib_t '/home/qt/node_monitor/data(/.*)?'
  set_selinux_fcontext var_lib_t '/home/qt/node_monitor/service-home(/.*)?'
  set_selinux_fcontext etc_t '/home/qt/node_monitor/secrets(/.*)?'
  restorecon -RF "${PROJECT_ROOT}"
}

if [[ -f "${SCRIPT_DIR}/packages/prometheus-${PROMETHEUS_VERSION}.linux-amd64.tar.gz" ]]; then
  PACKAGE_ROOT=${SCRIPT_DIR}
elif [[ -f "${INSTALL_ROOT}/packages/prometheus-${PROMETHEUS_VERSION}.linux-amd64.tar.gz" ]]; then
  PACKAGE_ROOT=${INSTALL_ROOT}
else
  echo "Runtime packages are missing from ${SCRIPT_DIR}/packages and ${INSTALL_ROOT}/packages" >&2
  exit 1
fi

if [[ -f "${SCRIPT_DIR}/bin/erlang-exporter" ]]; then
  EXPORTER_ROOT=${SCRIPT_DIR}
elif [[ -f "${INSTALL_ROOT}/bin/erlang-exporter" ]]; then
  EXPORTER_ROOT=${INSTALL_ROOT}
else
  echo "Erlang Exporter is missing from ${SCRIPT_DIR}/bin and ${INSTALL_ROOT}/bin" >&2
  exit 1
fi

if [[ -f "${SCRIPT_DIR}/bin/ops-agent" ]]; then
  OPS_AGENT_ROOT=${SCRIPT_DIR}
elif [[ -f "${INSTALL_ROOT}/bin/ops-agent" ]]; then
  OPS_AGENT_ROOT=${INSTALL_ROOT}
else
  echo "Ops Agent is missing from ${SCRIPT_DIR}/bin and ${INSTALL_ROOT}/bin" >&2
  exit 1
fi

for relative in \
  "packages/prometheus-${PROMETHEUS_VERSION}.linux-amd64.tar.gz" \
  "packages/alertmanager-${ALERTMANAGER_VERSION}.linux-amd64.tar.gz" \
  "packages/grafana-${GRAFANA_VERSION}.linux-amd64.tar.gz"; do
  grep "  ${relative}$" "${SCRIPT_DIR}/checksums.sha256" | \
    (cd "${PACKAGE_ROOT}" && sha256sum --check -)
done
grep '  bin/erlang-exporter$' "${SCRIPT_DIR}/checksums.sha256" | \
  (cd "${EXPORTER_ROOT}" && sha256sum --check -)
grep '  bin/ops-agent$' "${SCRIPT_DIR}/checksums.sha256" | \
  (cd "${OPS_AGENT_ROOT}" && sha256sum --check -)

if ! getent passwd "${SERVICE_USER}" >/dev/null; then
  useradd --system --home-dir "${SERVICE_HOME}" --shell /sbin/nologin --no-create-home "${SERVICE_USER}"
elif [[ "$(getent passwd "${SERVICE_USER}" | cut -d: -f6)" != "${SERVICE_HOME}" ]]; then
  usermod --home "${SERVICE_HOME}" "${SERVICE_USER}"
fi

install -d -o root -g root -m 0755 \
  "${INSTALL_ROOT}" \
  "${INSTALL_ROOT}/bin" \
  "${PROJECT_ROOT}" \
  "${CONFIG_ROOT}" \
  "${PROJECT_ROOT}/prometheus" \
  "${PROJECT_ROOT}/alertmanager" \
  "${PROJECT_ROOT}/grafana"
install -d -o root -g "${SERVICE_USER}" -m 0750 \
  "${PROJECT_ROOT}/secrets" \
  "${PROJECT_ROOT}/secrets/ssh"
install -d -o "${SERVICE_USER}" -g "${SERVICE_USER}" -m 0750 \
  "${SERVICE_HOME}" \
  "${DATA_ROOT}" \
  "${DATA_ROOT}/prometheus" \
  "${DATA_ROOT}/alertmanager" \
  "${DATA_ROOT}/grafana" \
  "${DATA_ROOT}/logs"

configure_selinux_contexts

install_archive() {
  local archive=$1
  local source_name=$2
  local target_name=$3
  local staging
  local target="${INSTALL_ROOT}/${target_name}"

  if [[ -e "${target}" ]]; then
    echo "Keeping existing ${target}"
    return
  fi

  staging=$(mktemp -d "${INSTALL_ROOT}/.install-${target_name}.XXXXXX")
  tar -xzf "${PACKAGE_ROOT}/packages/${archive}" -C "${staging}"
  if [[ ! -d "${staging}/${source_name}" ]]; then
    echo "Archive ${archive} does not contain ${source_name}" >&2
    exit 1
  fi
  mv "${staging}/${source_name}" "${target}"
  rmdir "${staging}"
}

install_archive \
  "prometheus-${PROMETHEUS_VERSION}.linux-amd64.tar.gz" \
  "prometheus-${PROMETHEUS_VERSION}.linux-amd64" \
  "prometheus-${PROMETHEUS_VERSION}"
install_archive \
  "alertmanager-${ALERTMANAGER_VERSION}.linux-amd64.tar.gz" \
  "alertmanager-${ALERTMANAGER_VERSION}.linux-amd64" \
  "alertmanager-${ALERTMANAGER_VERSION}"
install_archive \
  "grafana-${GRAFANA_VERSION}.linux-amd64.tar.gz" \
  "grafana-v${GRAFANA_VERSION}" \
  "grafana-${GRAFANA_VERSION}"

if [[ "$(readlink -f "${EXPORTER_ROOT}/bin/erlang-exporter")" != "$(readlink -f "${INSTALL_ROOT}/bin/erlang-exporter" 2>/dev/null || true)" ]]; then
  install -o root -g root -m 0755 "${EXPORTER_ROOT}/bin/erlang-exporter" "${INSTALL_ROOT}/bin/erlang-exporter"
fi
if [[ "$(readlink -f "${OPS_AGENT_ROOT}/bin/ops-agent")" != "$(readlink -f "${INSTALL_ROOT}/bin/ops-agent" 2>/dev/null || true)" ]]; then
  install -o root -g root -m 0755 "${OPS_AGENT_ROOT}/bin/ops-agent" "${INSTALL_ROOT}/bin/ops-agent"
fi
ln -sfn "prometheus-${PROMETHEUS_VERSION}" "${INSTALL_ROOT}/prometheus"
ln -sfn "alertmanager-${ALERTMANAGER_VERSION}" "${INSTALL_ROOT}/alertmanager"
ln -sfn "grafana-${GRAFANA_VERSION}" "${INSTALL_ROOT}/grafana"

"${INSTALL_ROOT}/prometheus/prometheus" --version
"${INSTALL_ROOT}/alertmanager/alertmanager" --version
"${INSTALL_ROOT}/grafana/bin/grafana" server -v
file "${INSTALL_ROOT}/bin/erlang-exporter"
file "${INSTALL_ROOT}/bin/ops-agent"

echo "Linux monitoring runtime is installed. No service was started."
