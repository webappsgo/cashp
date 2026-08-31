#!/usr/bin/env bash
# shellcheck shell=bash
# - - - - - - - - - - - - - - - - - - - - - - - - -
##@Version           :  202608260118-git
# @@Author           :  Jason Hempstead
# @@Contact          :  git-admin@casjaysdev.pro
# @@License          :  WTFPL
# @@ReadME           :  e2e.sh --help
# @@Copyright        :  Copyright: (c) 2026 Jason Hempstead, Casjays Developments
# @@Created          :  Wednesday, August 26, 2026 01:18 EDT
# @@File             :  e2e.sh
# @@Description      :  On demand browser end to end suite - runs the three chromedp tiers in Docker
# @@Changelog        :  Initial release
# @@TODO             :  None
# @@Other            :  Never part of make test - this is a manual, developer initiated suite
# @@Resource         :  AI.md PART 29 "Browser E2E Testing"
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
# Caller-settable overrides
CASHP_TEST_PORT="${CASHP_TEST_PORT:-64581}"
CASHP_API_VERSION="${CASHP_API_VERSION:-v1}"
CASHP_ADMIN_PATH="${CASHP_ADMIN_PATH:-administration}"
CASHP_GO_IMAGE="${CASHP_GO_IMAGE:-casjaysdev/go:latest}"
CASHP_BROWSER_IMAGE="${CASHP_BROWSER_IMAGE:-chromedp/headless-shell:latest}"
CASHP_E2E_TIMEOUT="${CASHP_E2E_TIMEOUT:-20m}"
CASHP_E2E_RUN="${CASHP_E2E_RUN:-}"
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
TEMP_DIR=""
KEEP_ARTIFACTS="false"
NETWORK_NAME=""
BROWSER_NAME=""
RUNNER_NAME=""
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
  printf '  Runs the three mandatory chromedp tiers (SSR, no JavaScript, full browser)\n'
  printf '  from tests/e2e/ against a %s sidecar. Manual only - make test\n' "${CASHP_BROWSER_IMAGE}"
  printf '  never runs this suite and there is no Makefile target for it.\n'
  __help_section "Options"
  __help_line "--help" "Show this help message and exit"
  __help_line "--version" "Show version and exit"
  __help_line "--run PATTERN" "Only run tests matching the go test -run pattern"
  __help_line "--keep-artifacts" "Keep screenshots and logs after a successful run"
  __help_line "--color auto" "Control color output (auto|yes|no)"
  __help_section "Environment"
  __help_line "CASHP_TEST_PORT" "Port the suite serves on (default: 64581)"
  __help_line "CASHP_API_VERSION" "API version prefix without slashes (default: v1)"
  __help_line "CASHP_ADMIN_PATH" "Admin panel path segment (default: administration)"
  __help_line "CASHP_BROWSER_IMAGE" "Headless Chromium image (default: chromedp/headless-shell:latest)"
  __help_line "CASHP_GO_IMAGE" "Toolchain image (default: casjaysdev/go:latest)"
  __help_line "CASHP_E2E_TIMEOUT" "go test timeout (default: 20m)"
  printf '\n'
}
# - - - - - - - - - - - - - - - - - - - - - - - - -
__version() {
  printf '%s %s\n' "${APPNAME}" "${VERSION}"
}
# - - - - - - - - - - - - - - - - - - - - - - - - -
__cleanup() {
  if [[ -n "${RUNNER_NAME}" ]]; then
    docker rm -f "${RUNNER_NAME}" &>/dev/null || true
  fi
  if [[ -n "${BROWSER_NAME}" ]]; then
    docker rm -f "${BROWSER_NAME}" &>/dev/null || true
  fi
  if [[ -n "${NETWORK_NAME}" ]]; then
    docker network rm "${NETWORK_NAME}" &>/dev/null || true
  fi
  if [[ -n "${TEMP_DIR}" ]] && [[ -d "${TEMP_DIR}" ]]; then
    if [[ "${KEEP_ARTIFACTS}" == "true" ]]; then
      printf 'Failure artifacts kept in %s\n' "${TEMP_DIR}"
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
__rand_suffix() {
  tr -dc 'a-z0-9' </dev/urandom | head -c8
}
# - - - - - - - - - - - - - - - - - - - - - - - - -
# The Go suite lives in tests/e2e behind the e2e build tag and is owned by the frontend work
__require_suite() {
  if [[ ! -d "${SCRIPT_DIR}/e2e" ]]; then
    printf 'ERROR: %s/e2e does not exist\n' "${SCRIPT_DIR}" >&2
    printf 'The browser suite is Go code behind the "e2e" build tag - see AI.md PART 29\n' >&2
    printf '"Browser E2E Testing". Create tests/e2e/*_test.go before running this script.\n' >&2
    exit 66
  fi
  if ! compgen -G "${SCRIPT_DIR}/e2e/*_test.go" >/dev/null; then
    printf 'ERROR: %s/e2e contains no *_test.go files\n' "${SCRIPT_DIR}" >&2
    printf 'All three tiers (SSR, no JavaScript, full browser) are required - see AI.md PART 29.\n' >&2
    exit 66
  fi
}
# - - - - - - - - - - - - - - - - - - - - - - - - -
# A private bridge lets the browser reach the server the Go suite starts
__start_browser() {
  local _waited=0
  NETWORK_NAME="${PROJECT_NAME}-e2e-$(__rand_suffix)"
  BROWSER_NAME="${PROJECT_NAME}-browser-$(__rand_suffix)"
  RUNNER_NAME="${PROJECT_NAME}-runner-$(__rand_suffix)"
  printf 'Creating network %s...\n' "${NETWORK_NAME}"
  docker network create "${NETWORK_NAME}" >/dev/null
  printf 'Starting %s as %s...\n' "${CASHP_BROWSER_IMAGE}" "${BROWSER_NAME}"
  docker run -d \
    --name "${BROWSER_NAME}" \
    --network "${NETWORK_NAME}" \
    --shm-size=2g \
    "${CASHP_BROWSER_IMAGE}" \
    --remote-debugging-address=0.0.0.0 \
    --remote-debugging-port=9222 \
    --disable-gpu \
    --no-sandbox >/dev/null
  printf 'Waiting for the CDP endpoint...\n'
  while [[ "${_waited}" -lt 60 ]]; do
    if docker logs "${BROWSER_NAME}" 2>&1 | grep -q -- 'DevTools listening on'; then
      printf 'CDP endpoint ready after %ss\n' "${_waited}"
      return 0
    fi
    sleep 1
    _waited=$((_waited + 1))
  done
  printf 'ERROR: the headless browser never exposed a CDP endpoint\n' >&2
  docker logs "${BROWSER_NAME}" >&2 || true
  exit 70
}
# - - - - - - - - - - - - - - - - - - - - - - - - -
# Run the tagged Go suite in the toolchain image, on the same network as the browser
__run_suite() {
  local _args=()
  _args=(
    docker run --rm
    --name "${RUNNER_NAME}"
    --network "${NETWORK_NAME}"
    -v "${PROJECT_DIR}:/app"
    -v "${GO_CACHE}:/usr/local/share/go/pkg/mod"
    -v "${GO_BUILD}:/usr/local/share/go/cache"
    -v "${TEMP_DIR}:/artifacts"
    -w /app
    -e CGO_ENABLED=0
    -e GOFLAGS=-buildvcs=false
    -e "CASHP_E2E_CDP_URL=http://${BROWSER_NAME}:9222"
    -e "CASHP_E2E_HOSTNAME=${RUNNER_NAME}"
    -e "CASHP_E2E_ARTIFACTS=/artifacts"
    -e "CASHP_TEST_PORT=${CASHP_TEST_PORT}"
    -e "CASHP_API_VERSION=${CASHP_API_VERSION}"
    -e "CASHP_ADMIN_PATH=${CASHP_ADMIN_PATH}"
    "${CASHP_GO_IMAGE}"
    go test -tags e2e -timeout "${CASHP_E2E_TIMEOUT}" -v
  )
  if [[ -n "${CASHP_E2E_RUN}" ]]; then
    _args+=(-run "${CASHP_E2E_RUN}")
  fi
  _args+=(./tests/e2e/...)
  printf '\n%sRunning the browser suite...%s\n' "${C_BOLD}" "${C_RESET}"
  "${_args[@]}"
}
# - - - - - - - - - - - - - - - - - - - - - - - - -
__main() {
  if ! __cmd_exists docker; then
    printf 'ERROR: docker is required but not installed\n' >&2
    exit 69
  fi
  __require_suite
  mkdir -p "${GO_CACHE}" "${GO_BUILD}" "${TMPDIR:-/tmp}/${PROJECT_ORG}"
  TEMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/${PROJECT_ORG}/${PROJECT_NAME}-XXXXXX")"
  printf '%s%s browser end to end suite (org: %s, port: %s)%s\n' "${C_BOLD}" "${PROJECT_NAME}" "${PROJECT_ORG}" "${CASHP_TEST_PORT}" "${C_RESET}"
  __start_browser
  if ! __run_suite; then
    KEEP_ARTIFACTS="true"
    printf '\n%sBrowser end to end suite FAILED%s\n' "${C_RED}" "${C_RESET}" >&2
    exit 1
  fi
  printf '\n%sBrowser end to end suite passed%s\n' "${C_GREEN}" "${C_RESET}"
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
        keep-artifacts)
          KEEP_ARTIFACTS="true"
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
        run)
          CASHP_E2E_RUN="${val}"
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
