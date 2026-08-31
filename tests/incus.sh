#!/usr/bin/env bash
# shellcheck shell=bash
# - - - - - - - - - - - - - - - - - - - - - - - - -
##@Version           :  202608260118-git
# @@Author           :  Jason Hempstead
# @@Contact          :  git-admin@casjaysdev.pro
# @@License          :  WTFPL
# @@ReadME           :  incus.sh --help
# @@Copyright        :  Copyright: (c) 2026 Jason Hempstead, Casjays Developments
# @@Created          :  Wednesday, August 26, 2026 01:18 EDT
# @@File             :  incus.sh
# @@Description      :  Phase 2 binary validation inside a throwaway Debian systemd container
# @@Changelog        :  Initial release
# @@TODO             :  None
# @@Other            :  Builds only in casjaysdev/go:latest - never compiles on the host
# @@Resource         :  AI.md PART 24, PART 29
# @@Terminal App     :  no
# @@sudo/root        :  no
# @@Template         :  shell/bash
# - - - - - - - - - - - - - - - - - - - - - - - - -
# shellcheck disable=SC1001,SC1003,SC2001,SC2003,SC2016,SC2031,SC2090,SC2115,SC2120,SC2155,SC2199,SC2229,SC2317,SC2329
# - - - - - - - - - - - - - - - - - - - - - - - - -
set -eo pipefail
# - - - - - - - - - - - - - - - - - - - - - - - - -
APPNAME="${0##*/}"
VERSION="YYYYMMDDHHMM-git"
SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" &>/dev/null && pwd -P)"
PROJECT_DIR="$(cd -- "${SCRIPT_DIR}/.." &>/dev/null && pwd -P)"
# - - - - - - - - - - - - - - - - - - - - - - - - -
# Project identity comes from the git remote and falls back to the directory layout
PROJECT_NAME="$(git -C "${PROJECT_DIR}" remote get-url origin 2>/dev/null | sed -E 's|.*/([^/]+)$|\1|; s|\.git$||' || true)"
PROJECT_ORG="$(git -C "${PROJECT_DIR}" remote get-url origin 2>/dev/null | sed -E 's|.*[:/]([^/]+)/[^/]+$|\1|; s|\.git$||' || true)"
if [[ -z "${PROJECT_NAME}" ]]; then
  PROJECT_NAME="$(basename -- "${PROJECT_DIR}")"
fi
if [[ -z "${PROJECT_ORG}" ]]; then
  PROJECT_ORG="$(basename -- "$(dirname -- "${PROJECT_DIR}")")"
fi
# - - - - - - - - - - - - - - - - - - - - - - - - -
# Caller-settable overrides - the port is the one the installed service listens on
CASHP_TEST_PORT="${CASHP_TEST_PORT:-80}"
CASHP_API_VERSION="${CASHP_API_VERSION:-v1}"
CASHP_ADMIN_PATH="${CASHP_ADMIN_PATH:-administration}"
CASHP_INCUS_IMAGE="${CASHP_INCUS_IMAGE:-images:debian/trixie}"
CASHP_GO_IMAGE="${CASHP_GO_IMAGE:-casjaysdev/go:latest}"
CASHP_STARTUP_TIMEOUT="${CASHP_STARTUP_TIMEOUT:-60}"
CASHP_KEEP_CONTAINER="${CASHP_KEEP_CONTAINER:-false}"
# - - - - - - - - - - - - - - - - - - - - - - - - -
# Go build cache directories bind-mounted into the toolchain container
GO_CACHE="${GO_CACHE:-${HOME}/go/pkg/mod}"
GO_BUILD="${GO_BUILD:-${HOME}/.cache/go-build/${PROJECT_NAME}}"
# - - - - - - - - - - - - - - - - - - - - - - - - -
COLOR_FLAG="${COLOR_FLAG:-auto}"
C_RESET=$'\e[0m'
C_RED=$'\e[31m'
C_GREEN=$'\e[32m'
C_BOLD=$'\e[1m'
# - - - - - - - - - - - - - - - - - - - - - - - - -
TESTS_RUN=0
TESTS_FAILED=0
TEMP_DIR=""
CONTAINER_NAME=""
BASE_URL="http://localhost:${CASHP_TEST_PORT}"
API_PREFIX="/api/${CASHP_API_VERSION}"
SETUP_TOKEN=""
API_TOKEN=""
# - - - - - - - - - - - - - - - - - - - - - - - - -
__help_header() {
  printf '\n\033[1;37mUsage:\033[0m %s\n' "$1"
}
# - - - - - - - - - - - - - - - - - - - - - - - - -
__help_section() {
  printf '\n\033[1;37m%s:\033[0m\n' "$1"
}
# - - - - - - - - - - - - - - - - - - - - - - - - -
__help_line() {
  printf '  %-38s- %s\n' "$1" "$2"
}
# - - - - - - - - - - - - - - - - - - - - - - - - -
__help() {
  __help_header "${APPNAME} [options]"
  __help_section "Description"
  printf '  Builds every binary in %s, then installs and exercises them as a\n' "${CASHP_GO_IMAGE}"
  printf '  real systemd service inside a throwaway %s container.\n' "${CASHP_INCUS_IMAGE}"
  __help_section "Options"
  __help_line "--help" "Show this help message and exit"
  __help_line "--version" "Show version and exit"
  __help_line "--port 80" "Port the installed service listens on"
  __help_line "--keep" "Leave the test container running for inspection"
  __help_line "--color auto" "Control color output (auto|yes|no)"
  __help_section "Environment"
  __help_line "CASHP_TEST_PORT" "Same as --port (default: 80)"
  __help_line "CASHP_API_VERSION" "API version prefix without slashes (default: v1)"
  __help_line "CASHP_ADMIN_PATH" "Admin panel path segment (default: administration)"
  __help_line "CASHP_INCUS_IMAGE" "Container image (default: images:debian/trixie)"
  __help_line "CASHP_GO_IMAGE" "Toolchain image used to build (default: casjaysdev/go:latest)"
  __help_line "CASHP_STARTUP_TIMEOUT" "Seconds to wait for the service to answer (default: 60)"
  printf '\n'
}
# - - - - - - - - - - - - - - - - - - - - - - - - -
__version() {
  printf '%s %s\n' "${APPNAME}" "${VERSION}"
}
# - - - - - - - - - - - - - - - - - - - - - - - - -
__cleanup() {
  if [[ -n "${CONTAINER_NAME}" ]]; then
    if [[ -n "${TEMP_DIR}" ]] && [[ "${TESTS_FAILED}" -ne 0 ]]; then
      incus exec "${CONTAINER_NAME}" -- bash -c "journalctl -u ${PROJECT_NAME} --no-pager" >"${TEMP_DIR}/journal.log" 2>/dev/null || true
    fi
    if [[ "${CASHP_KEEP_CONTAINER}" != "true" ]]; then
      incus delete "${CONTAINER_NAME}" --force &>/dev/null || true
    fi
  fi
  if [[ -n "${TEMP_DIR}" ]] && [[ -d "${TEMP_DIR}" ]]; then
    if [[ -s "${TEMP_DIR}/journal.log" ]]; then
      printf 'Service journal kept at %s/journal.log\n' "${TEMP_DIR}"
    else
      rm -rf "${TEMP_DIR}"
    fi
  fi
}
# - - - - - - - - - - - - - - - - - - - - - - - - -
__on_term() {
  printf 'Terminated.\n' >&2
  exit 143
}
# - - - - - - - - - - - - - - - - - - - - - - - - -
__on_int() {
  printf 'Interrupted.\n' >&2
  exit 130
}
# - - - - - - - - - - - - - - - - - - - - - - - - -
trap '__cleanup' EXIT
trap '__on_term' TERM
trap '__on_int' INT
# - - - - - - - - - - - - - - - - - - - - - - - - -
__disable_color() {
  C_RESET=""
  C_RED=""
  C_GREEN=""
  C_BOLD=""
}
# - - - - - - - - - - - - - - - - - - - - - - - - -
__cmd_exists() {
  command -v "$1" &>/dev/null
}
# - - - - - - - - - - - - - - - - - - - - - - - - -
__pass() {
  TESTS_RUN=$((TESTS_RUN + 1))
  printf '  %sPASS%s %s\n' "${C_GREEN}" "${C_RESET}" "$1"
}
# - - - - - - - - - - - - - - - - - - - - - - - - -
__fail() {
  TESTS_RUN=$((TESTS_RUN + 1))
  TESTS_FAILED=$((TESTS_FAILED + 1))
  printf '  %sFAIL%s %s\n' "${C_RED}" "${C_RESET}" "$1" >&2
}
# - - - - - - - - - - - - - - - - - - - - - - - - -
__section() {
  printf '\n%s=== %s ===%s\n' "${C_BOLD}" "$1" "${C_RESET}"
}
# - - - - - - - - - - - - - - - - - - - - - - - - -
__rand_suffix() {
  tr -dc 'a-z0-9' </dev/urandom | head -c8
}
# - - - - - - - - - - - - - - - - - - - - - - - - -
# Build every component using the Makefile when present, otherwise the toolchain image directly
__build_binaries() {
  local _go_docker=()
  __section "Build"
  if [[ -f "${PROJECT_DIR}/Makefile" ]]; then
    printf 'Building with make build...\n'
    make -C "${PROJECT_DIR}" build
    return 0
  fi
  if ! __cmd_exists docker; then
    printf 'ERROR: no Makefile and docker is unavailable - cannot build\n' >&2
    exit 69
  fi
  printf 'No Makefile found - building directly in %s...\n' "${CASHP_GO_IMAGE}"
  mkdir -p "${GO_CACHE}" "${GO_BUILD}" "${PROJECT_DIR}/binaries"
  _go_docker=(
    docker run --rm
    --name "${PROJECT_NAME}-build-$(__rand_suffix)"
    -v "${PROJECT_DIR}:/app"
    -v "${GO_CACHE}:/usr/local/share/go/pkg/mod"
    -v "${GO_BUILD}:/usr/local/share/go/cache"
    -w /app
    -e CGO_ENABLED=0
    -e GOFLAGS=-buildvcs=false
    "${CASHP_GO_IMAGE}"
  )
  printf 'Building server binary...\n'
  "${_go_docker[@]}" go build -buildvcs=false -trimpath -ldflags "-s -w" -o "/app/binaries/${PROJECT_NAME}" ./src
  if [[ -d "${PROJECT_DIR}/src/client" ]]; then
    printf 'Building client binary...\n'
    "${_go_docker[@]}" go build -buildvcs=false -trimpath -ldflags "-s -w" -o "/app/binaries/${PROJECT_NAME}-cli" ./src/client
  fi
  if [[ -d "${PROJECT_DIR}/src/agent" ]]; then
    printf 'Building agent binary...\n'
    "${_go_docker[@]}" go build -buildvcs=false -trimpath -ldflags "-s -w" -o "/app/binaries/${PROJECT_NAME}-agent" ./src/agent
  fi
}
# - - - - - - - - - - - - - - - - - - - - - - - - -
# Run a command inside the running test container
__in_container() {
  incus exec "${CONTAINER_NAME}" -- "$@"
}
# - - - - - - - - - - - - - - - - - - - - - - - - -
# Run a shell snippet inside the running test container
__sh_container() {
  incus exec "${CONTAINER_NAME}" -- bash -c "$1"
}
# - - - - - - - - - - - - - - - - - - - - - - - - -
# Launch the container, wait for systemd, push binaries and install the test tooling
__start_container() {
  local _waited=0 _binary=""
  __section "Test container"
  CONTAINER_NAME="test-${PROJECT_NAME}-$(__rand_suffix)"
  printf 'Launching %s as %s...\n' "${CASHP_INCUS_IMAGE}" "${CONTAINER_NAME}"
  incus launch "${CASHP_INCUS_IMAGE}" "${CONTAINER_NAME}" >/dev/null
  printf 'Waiting for systemd to finish booting...\n'
  while [[ "${_waited}" -lt "${CASHP_STARTUP_TIMEOUT}" ]]; do
    if __sh_container 'systemctl is-system-running --wait >/dev/null 2>&1 || systemctl is-system-running 2>/dev/null | grep -q -- "running\|degraded"'; then
      break
    fi
    sleep 1
    _waited=$((_waited + 1))
  done
  if [[ "${_waited}" -ge "${CASHP_STARTUP_TIMEOUT}" ]]; then
    printf 'ERROR: systemd never reached a running state inside %s\n' "${CONTAINER_NAME}" >&2
    exit 70
  fi
  printf 'Installing curl file jq...\n'
  __sh_container 'export DEBIAN_FRONTEND=noninteractive; apt-get update -qq && apt-get install -y -qq curl file jq' >/dev/null
  printf 'Pushing binaries...\n'
  for _binary in "${PROJECT_NAME}" "${PROJECT_NAME}-cli" "${PROJECT_NAME}-agent"; do
    if [[ -f "${PROJECT_DIR}/binaries/${_binary}" ]]; then
      incus file push "${PROJECT_DIR}/binaries/${_binary}" "${CONTAINER_NAME}/usr/local/bin/${_binary}" >/dev/null
      __in_container chmod 0755 "/usr/local/bin/${_binary}"
    fi
  done
  printf 'Pushing test suites...\n'
  __in_container mkdir -p /opt/tests
  incus file push "${SCRIPT_DIR}/test_endpoints.sh" "${CONTAINER_NAME}/opt/tests/test_endpoints.sh" >/dev/null
  incus file push "${SCRIPT_DIR}/test_content_negotiation.sh" "${CONTAINER_NAME}/opt/tests/test_content_negotiation.sh" >/dev/null
  incus file push "${SCRIPT_DIR}/test_admin_auth.sh" "${CONTAINER_NAME}/opt/tests/test_admin_auth.sh" >/dev/null
  __sh_container 'chmod 0755 /opt/tests/*.sh'
}
# - - - - - - - - - - - - - - - - - - - - - - - - -
# Every binary must answer --version and --help without a config file present
__check_binary_basics() {
  local _binary="$1" _label="$2"
  if __in_container "${_binary}" --version >/dev/null 2>&1; then
    __pass "${_label} --version"
  else
    __fail "${_label} --version"
  fi
  if __in_container "${_binary}" --help >/dev/null 2>&1; then
    __pass "${_label} --help"
  else
    __fail "${_label} --help"
  fi
  if __in_container file "/usr/local/bin/${_binary}" | grep -q -- 'ELF'; then
    __pass "${_label} is an ELF binary"
  else
    __fail "${_label} is not an ELF binary"
  fi
}
# - - - - - - - - - - - - - - - - - - - - - - - - -
# A renamed binary must report its new name - nothing may be hardcoded to the build name
__check_binary_rename() {
  local _binary="$1" _renamed="$2" _label="$3"
  __sh_container "cp /usr/local/bin/${_binary} /tmp/${_renamed} && chmod 0755 /tmp/${_renamed}"
  if __sh_container "/tmp/${_renamed} --help 2>&1" | grep -q -- "${_renamed}"; then
    __pass "${_label} rename to ${_renamed} reflected in --help"
  else
    __fail "${_label} rename to ${_renamed} NOT reflected in --help"
  fi
}
# - - - - - - - - - - - - - - - - - - - - - - - - -
# Install the unit, start it and wait until the service actually answers HTTP
__install_and_start_service() {
  local _waited=0
  __section "Service install and start"
  if __in_container "${PROJECT_NAME}" --service --install >/dev/null 2>&1; then
    __pass "${PROJECT_NAME} --service --install"
  else
    __fail "${PROJECT_NAME} --service --install"
    return 1
  fi
  if __in_container systemctl start "${PROJECT_NAME}" >/dev/null 2>&1; then
    __pass "systemctl start ${PROJECT_NAME}"
  else
    __fail "systemctl start ${PROJECT_NAME}"
    return 1
  fi
  while [[ "${_waited}" -lt "${CASHP_STARTUP_TIMEOUT}" ]]; do
    if __in_container curl -q -sf -o /dev/null "${BASE_URL}${API_PREFIX}/server/healthz"; then
      __pass "service answered ${API_PREFIX}/server/healthz after ${_waited}s"
      break
    fi
    sleep 1
    _waited=$((_waited + 1))
  done
  if [[ "${_waited}" -ge "${CASHP_STARTUP_TIMEOUT}" ]]; then
    __fail "service did not answer within ${CASHP_STARTUP_TIMEOUT}s"
    printf '%s--- journal ---%s\n' "${C_BOLD}" "${C_RESET}" >&2
    __sh_container "journalctl -u ${PROJECT_NAME} --no-pager -n 50" >&2 || true
    return 1
  fi
  if __in_container systemctl is-active --quiet "${PROJECT_NAME}"; then
    __pass "systemctl is-active ${PROJECT_NAME}"
  else
    __fail "systemctl is-active ${PROJECT_NAME}"
  fi
  return 0
}
# - - - - - - - - - - - - - - - - - - - - - - - - -
# Pull the first-run setup token out of the service journal
__read_setup_token() {
  SETUP_TOKEN="$(__sh_container "journalctl -u ${PROJECT_NAME} --no-pager | grep -i -- 'setup token' | grep -oE -- '[A-Za-z0-9_]{24,}' | tail -n1" 2>/dev/null || true)"
  if [[ -n "${SETUP_TOKEN}" ]]; then
    __pass "setup token captured from the journal (${SETUP_TOKEN:0:4}xxxxx)"
  else
    __fail "no setup token found in the journal"
  fi
}
# - - - - - - - - - - - - - - - - - - - - - - - - -
# Run one of the standalone suites inside the container against the live service
__run_suite() {
  local _suite="$1"
  shift
  __section "Suite: ${_suite}"
  if __in_container env "$@" "/opt/tests/${_suite}" --color no; then
    __pass "suite ${_suite}"
  else
    __fail "suite ${_suite}"
  fi
}
# - - - - - - - - - - - - - - - - - - - - - - - - -
# The client binary must work against the live service, not merely print help
__test_client() {
  __section "Client binary"
  if ! __in_container test -f "/usr/local/bin/${PROJECT_NAME}-cli"; then
    printf 'client not installed - skipping\n'
    return 0
  fi
  __check_binary_basics "${PROJECT_NAME}-cli" "client"
  __check_binary_rename "${PROJECT_NAME}-cli" "renamed-cli" "client"
  if [[ -n "${API_TOKEN}" ]]; then
    if __in_container "${PROJECT_NAME}-cli" --server "${BASE_URL}" --token "${API_TOKEN}" status >/dev/null 2>&1; then
      __pass "client status with an API token"
    else
      __fail "client status with an API token"
    fi
  else
    if __in_container "${PROJECT_NAME}-cli" --server "${BASE_URL}" status >/dev/null 2>&1; then
      __pass "client status without a token"
    else
      __fail "client status without a token"
    fi
  fi
}
# - - - - - - - - - - - - - - - - - - - - - - - - -
# The agent binary must register against the live service using the issued API token
__test_agent() {
  __section "Agent binary"
  if ! __in_container test -f "/usr/local/bin/${PROJECT_NAME}-agent"; then
    printf 'agent not installed - skipping\n'
    return 0
  fi
  __check_binary_basics "${PROJECT_NAME}-agent" "agent"
  __check_binary_rename "${PROJECT_NAME}-agent" "renamed-agent" "agent"
  if [[ -z "${API_TOKEN}" ]]; then
    __fail "agent functionality untested - no API token was issued"
    return 0
  fi
  if __in_container "${PROJECT_NAME}-agent" --server "${BASE_URL}" --token "${API_TOKEN}" status >/dev/null 2>&1; then
    __pass "agent status with an API token"
  else
    __fail "agent status with an API token"
  fi
}
# - - - - - - - - - - - - - - - - - - - - - - - - -
# The unit must stop cleanly and leave no listener behind
__stop_service() {
  __section "Service stop"
  if __in_container systemctl stop "${PROJECT_NAME}" >/dev/null 2>&1; then
    __pass "systemctl stop ${PROJECT_NAME}"
  else
    __fail "systemctl stop ${PROJECT_NAME}"
    return 0
  fi
  if __in_container systemctl is-active --quiet "${PROJECT_NAME}"; then
    __fail "${PROJECT_NAME} is still active after stop"
  else
    __pass "${PROJECT_NAME} is inactive after stop"
  fi
}
# - - - - - - - - - - - - - - - - - - - - - - - - -
__main() {
  if ! __cmd_exists incus; then
    printf 'ERROR: incus not found - install incus or use tests/docker.sh\n' >&2
    exit 69
  fi
  if [[ ! -d "${PROJECT_DIR}/src" ]]; then
    printf 'ERROR: %s/src does not exist - nothing to build\n' "${PROJECT_DIR}" >&2
    exit 66
  fi
  mkdir -p "${TMPDIR:-/tmp}/${PROJECT_ORG}"
  TEMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/${PROJECT_ORG}/${PROJECT_NAME}-XXXXXX")"
  printf '%s%s phase 2 incus validation (org: %s, port: %s)%s\n' "${C_BOLD}" "${PROJECT_NAME}" "${PROJECT_ORG}" "${CASHP_TEST_PORT}" "${C_RESET}"
  __build_binaries
  if [[ ! -f "${PROJECT_DIR}/binaries/${PROJECT_NAME}" ]]; then
    printf 'ERROR: expected binaries/%s after the build but it is missing\n' "${PROJECT_NAME}" >&2
    exit 70
  fi
  __start_container
  __section "Server binary"
  __check_binary_basics "${PROJECT_NAME}" "server"
  __check_binary_rename "${PROJECT_NAME}" "renamed-server" "server"
  if ! __install_and_start_service; then
    printf '\n%sIncus validation FAILED - the service never came up%s\n' "${C_RED}" "${C_RESET}" >&2
    exit 1
  fi
  __read_setup_token
  __run_suite test_endpoints.sh "CASHP_BASE_URL=${BASE_URL}" "CASHP_API_VERSION=${CASHP_API_VERSION}" "CASHP_ADMIN_PATH=${CASHP_ADMIN_PATH}"
  __run_suite test_content_negotiation.sh "CASHP_BASE_URL=${BASE_URL}" "CASHP_API_VERSION=${CASHP_API_VERSION}"
  if [[ -n "${SETUP_TOKEN}" ]]; then
    __run_suite test_admin_auth.sh "CASHP_BASE_URL=${BASE_URL}" "CASHP_API_VERSION=${CASHP_API_VERSION}" "CASHP_ADMIN_PATH=${CASHP_ADMIN_PATH}" "CASHP_SETUP_TOKEN=${SETUP_TOKEN}" "CASHP_TOKEN_OUT=/tmp/api_token"
    API_TOKEN="$(__sh_container 'cat /tmp/api_token 2>/dev/null' || true)"
  fi
  __test_client
  __test_agent
  __stop_service
  printf '\n%sTotal: %s  Failed: %s%s\n' "${C_BOLD}" "${TESTS_RUN}" "${TESTS_FAILED}" "${C_RESET}"
  if [[ "${TESTS_FAILED}" -ne 0 ]]; then
    printf '%sIncus validation FAILED%s\n' "${C_RED}" "${C_RESET}" >&2
    exit 1
  fi
  printf '%sIncus validation passed%s\n' "${C_GREEN}" "${C_RESET}"
}
# - - - - - - - - - - - - - - - - - - - - - - - - -
while getopts ":hv-:" opt; do
  case "${opt}" in
    h)
      __help
      exit 0
      ;;
    v)
      __version
      exit 0
      ;;
    -)
      flag="${OPTARG%%=*}"
      case "${flag}" in
        help)
          __help
          exit 0
          ;;
        version)
          __version
          exit 0
          ;;
        keep)
          CASHP_KEEP_CONTAINER="true"
          continue
          ;;
      esac
      if [[ "${OPTARG}" == *=* ]]; then
        val="${OPTARG#*=}"
      else
        val="${!OPTIND}"
        OPTIND=$((OPTIND + 1))
      fi
      case "${flag}" in
        port)
          CASHP_TEST_PORT="${val}"
          BASE_URL="http://localhost:${CASHP_TEST_PORT}"
          ;;
        color)
          COLOR_FLAG="${val}"
          ;;
        *)
          printf 'Unknown option: --%s\n' "${flag}" >&2
          exit 2
          ;;
      esac
      ;;
    *)
      printf 'Unknown option: -%s\n' "${OPTARG}" >&2
      exit 2
      ;;
  esac
done
shift $((OPTIND - 1))
# - - - - - - - - - - - - - - - - - - - - - - - - -
if [[ -n "${NO_COLOR}" ]] || [[ "${COLOR_FLAG}" == "no" ]]; then
  __disable_color
elif [[ "${COLOR_FLAG}" == "auto" ]] && [[ ! -t 1 ]]; then
  __disable_color
fi
# - - - - - - - - - - - - - - - - - - - - - - - - -
if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
  __main "$@"
fi
# - - - - - - - - - - - - - - - - - - - - - - - - -
# ex: ts=2 sw=2 et filetype=sh
