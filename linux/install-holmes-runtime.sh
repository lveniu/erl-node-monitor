#!/usr/bin/env bash
set -euo pipefail

if [[ ${EUID} -ne 0 ]]; then
  echo "install-holmes-runtime.sh must run as root" >&2
  exit 1
fi

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
PROJECT_ROOT=${ERLANG_MONITOR_PROJECT_ROOT:-/data/node_monitor}
INSTALL_ROOT=${ERLANG_MONITOR_INSTALL_ROOT:-/opt/erlang-monitor}
SERVICE_USER=${ERLANG_MONITOR_SERVICE_USER:-erlang-monitor}
PYTHON_VERSION=3.11.15
HOLMES_VERSION=0.38.1
HOLMES_NATIVE_BUILD=2
PYTHON_ARCHIVE=cpython-3.11.15+20260804-x86_64-unknown-linux-gnu-install_only_stripped.tgz
HOLMES_ARCHIVE=holmesgpt-0.38.1-native-centos7.tgz
WHEELS_ARCHIVE=holmes-wheels-0.38.1-centos7-x86_64.tgz
PYTHON_TARGET=${INSTALL_ROOT}/python-${PYTHON_VERSION}
HOLMES_TARGET=${INSTALL_ROOT}/holmesgpt-${HOLMES_VERSION}-native-${HOLMES_NATIVE_BUILD}
REQUIREMENTS=${SCRIPT_DIR}/holmes-native-requirements.txt

for relative in \
  "packages/${PYTHON_ARCHIVE}" \
  "packages/${HOLMES_ARCHIVE}" \
  "packages/${WHEELS_ARCHIVE}" \
  bin/holmes-gateway; do
  if [[ ! -f "${SCRIPT_DIR}/${relative}" ]]; then
    echo "Required native Holmes artifact is missing: linux/${relative}" >&2
    exit 1
  fi
  grep "  ${relative}$" "${SCRIPT_DIR}/checksums.sha256" | \
    (cd "${SCRIPT_DIR}" && sha256sum --check -)
done

if [[ ! -s "${REQUIREMENTS}" ]]; then
  echo "Native Holmes requirements are missing: ${REQUIREMENTS}" >&2
  exit 1
fi

if ! getent passwd "${SERVICE_USER}" >/dev/null; then
  useradd --system --home-dir /var/lib/erlang-monitor --shell /sbin/nologin --no-create-home "${SERVICE_USER}"
fi

install -d -o root -g root -m 0755 "${INSTALL_ROOT}" "${INSTALL_ROOT}/bin"
install -d -o root -g "${SERVICE_USER}" -m 0750 \
  "${PROJECT_ROOT}/secrets" \
  "${PROJECT_ROOT}/holmes"
install -d -o "${SERVICE_USER}" -g "${SERVICE_USER}" -m 0750 \
  "${PROJECT_ROOT}/data/holmes" \
  "${PROJECT_ROOT}/data/holmes/sessions" \
  "${PROJECT_ROOT}/data/holmes/audit"

if [[ ! -x "${PYTHON_TARGET}/bin/python3.11" ]]; then
  python_stage=$(mktemp -d "${INSTALL_ROOT}/.python-${PYTHON_VERSION}.XXXXXX")
  tar -xzf "${SCRIPT_DIR}/packages/${PYTHON_ARCHIVE}" -C "${python_stage}"
  if [[ ! -x "${python_stage}/python/bin/python3.11" ]]; then
    echo "Python archive does not contain python/bin/python3.11" >&2
    exit 1
  fi
  if [[ -e "${PYTHON_TARGET}" ]]; then
    echo "Refusing to replace incomplete Python runtime: ${PYTHON_TARGET}" >&2
    exit 1
  fi
  mv "${python_stage}/python" "${PYTHON_TARGET}"
  rmdir "${python_stage}"
fi

python_version=$("${PYTHON_TARGET}/bin/python3.11" -c 'import platform; print(platform.python_version())')
if [[ "${python_version}" != "${PYTHON_VERSION}" ]]; then
  echo "Unexpected native Python version: ${python_version}" >&2
  exit 1
fi

install -o root -g root -m 0755 "${SCRIPT_DIR}/bin/holmes-gateway" "${INSTALL_ROOT}/bin/holmes-gateway"

if [[ ! -s "${HOLMES_TARGET}/.native-runtime-manifest" ]]; then
  if [[ -e "${HOLMES_TARGET}" ]]; then
    echo "Refusing to replace incomplete Holmes runtime: ${HOLMES_TARGET}" >&2
    exit 1
  fi

  source_stage=$(mktemp -d "${INSTALL_ROOT}/.holmes-${HOLMES_VERSION}.XXXXXX")
  wheel_stage=$(mktemp -d "${INSTALL_ROOT}/.holmes-wheels-${HOLMES_VERSION}.XXXXXX")
  cleanup_staging() {
    rm -rf -- "${source_stage}" "${wheel_stage}"
  }
  trap cleanup_staging EXIT

  tar -xzf "${SCRIPT_DIR}/packages/${HOLMES_ARCHIVE}" -C "${source_stage}"
  tar -xzf "${SCRIPT_DIR}/packages/${WHEELS_ARCHIVE}" -C "${wheel_stage}"
  source_root=${source_stage}/holmesgpt-${HOLMES_VERSION}
  if [[ ! -f "${source_root}/server.py" || ! -f "${source_root}/holmes/__init__.py" ]]; then
    echo "Holmes source archive has an unexpected layout" >&2
    exit 1
  fi

  "${PYTHON_TARGET}/bin/python3.11" -m venv "${source_root}/.venv"
  "${source_root}/.venv/bin/python" -m pip install \
    --disable-pip-version-check \
    --no-index \
    --no-deps \
    --find-links "${wheel_stage}" \
    --requirement "${REQUIREMENTS}"

  (
    cd "${source_root}"
    HOLMES_DISABLE_MCP_OAUTH=true \
      "${source_root}/.venv/bin/python" -c \
      'import fastapi, jq, litellm, tiktoken, uvicorn; from holmes.core.tools_utils.oauth_tool_connector import OAuthToolConnector; from holmes.plugins.toolsets.prometheus.prometheus import PrometheusToolset; assert OAuthToolConnector().apply_user_tools([], None, {}) == []; print("Holmes native imports OK")'
  )

  {
    echo "holmes_version=${HOLMES_VERSION}"
    echo "native_build=${HOLMES_NATIVE_BUILD}"
    echo "upstream_commit=7af34f5e716e28adcbcbd584cd4708434929f183"
    echo "python_version=${PYTHON_VERSION}"
    grep "  packages/${HOLMES_ARCHIVE}$" "${SCRIPT_DIR}/checksums.sha256"
    grep "  packages/${WHEELS_ARCHIVE}$" "${SCRIPT_DIR}/checksums.sha256"
  } >"${source_root}/.native-runtime-manifest"

  chown -R root:root "${source_root}"
  mv "${source_root}" "${HOLMES_TARGET}"
  rm -rf -- "${source_stage}" "${wheel_stage}"
  trap - EXIT
fi

ln -sfnT "python-${PYTHON_VERSION}" "${INSTALL_ROOT}/python"
ln -sfnT "holmesgpt-${HOLMES_VERSION}-native-${HOLMES_NATIVE_BUILD}" "${INSTALL_ROOT}/holmesgpt"

"${INSTALL_ROOT}/python/bin/python3.11" --version
"${INSTALL_ROOT}/holmesgpt/.venv/bin/python" -c 'import jq, litellm, tiktoken; print("Holmes virtual environment ready")'
file "${INSTALL_ROOT}/bin/holmes-gateway"

echo "Native Holmes runtime is installed. No service was started."
