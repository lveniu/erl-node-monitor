#!/usr/bin/env bash
set -euo pipefail

if [[ ${EUID} -ne 0 ]]; then
  echo "install-holmes-services.sh must run as root" >&2
  exit 1
fi

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
UNIT_SOURCE=${SCRIPT_DIR}/systemd
UNIT_TARGET=/etc/systemd/system
TIMESTAMP=$(date +%Y%m%d-%H%M%S)

chmod 0755 \
  "${SCRIPT_DIR}/install-holmes-runtime.sh" \
  "${SCRIPT_DIR}/install-holmes-services.sh" \
  "${SCRIPT_DIR}/run-holmes.sh" \
  "${SCRIPT_DIR}/run-holmes-gateway.sh" \
  "${SCRIPT_DIR}/configure-holmes-grafana.sh" \
  "${SCRIPT_DIR}/validate-holmes-config.sh" \
  "${SCRIPT_DIR}/holmes-health-check.sh" \
  "${SCRIPT_DIR}/update-holmes-and-restart.sh"

for name in \
  erlang-monitor-holmes.service \
  erlang-monitor-holmes-gateway.service; do
  source=${UNIT_SOURCE}/${name}
  target=${UNIT_TARGET}/${name}
  if [[ -f "${target}" ]]; then
    cp -a "${target}" "${target}.bak-${TIMESTAMP}"
  fi
  install -o root -g root -m 0644 "${source}" "${target}"
done

systemctl daemon-reload
systemd-analyze verify \
  "${UNIT_TARGET}/erlang-monitor-holmes.service" \
  "${UNIT_TARGET}/erlang-monitor-holmes-gateway.service"

echo "Native Holmes systemd units are installed and verified. No service was started."
