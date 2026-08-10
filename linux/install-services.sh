#!/usr/bin/env bash
set -euo pipefail

if [[ ${EUID} -ne 0 ]]; then
  echo "install-services.sh must run as root" >&2
  exit 1
fi

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
UNIT_SOURCE=${SCRIPT_DIR}/systemd
UNIT_TARGET=/etc/systemd/system
TIMESTAMP=$(date +%Y%m%d-%H%M%S)

chmod 0755 \
  "${SCRIPT_DIR}/update-and-restart.sh" \
  "${SCRIPT_DIR}/run-exporter.sh" \
  "${SCRIPT_DIR}/run-grafana.sh" \
  "${SCRIPT_DIR}/validate-config.sh"

for name in \
  erlang-monitor-exporter.service \
  erlang-monitor-alertmanager.service \
  erlang-monitor-prometheus.service \
  erlang-monitor-grafana.service \
  erlang-monitor-ops-agent.service; do
  unit=${UNIT_SOURCE}/${name}
  target=${UNIT_TARGET}/${name}
  if [[ -f "${target}" ]]; then
    cp -a "${target}" "${target}.bak-${TIMESTAMP}"
  fi
  install -o root -g root -m 0644 "${unit}" "${target}"
done

systemctl daemon-reload
systemd-analyze verify \
  "${UNIT_TARGET}/erlang-monitor-exporter.service" \
  "${UNIT_TARGET}/erlang-monitor-alertmanager.service" \
  "${UNIT_TARGET}/erlang-monitor-prometheus.service" \
  "${UNIT_TARGET}/erlang-monitor-grafana.service" \
  "${UNIT_TARGET}/erlang-monitor-ops-agent.service"

echo "Systemd units are installed and verified. No service was enabled or started."
