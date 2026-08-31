#!/usr/bin/env bash
# shellcheck shell=bash
# - - - - - - - - - - - - - - - - - - - - - - - - -
##@Version           :  202608260118-git
# @@Author           :  Jason Hempstead
# @@Contact          :  git-admin@casjaysdev.pro
# @@License          :  WTFPL
# @@ReadME           :  test_content_negotiation.sh --help
# @@Copyright        :  Copyright: (c) 2026 Jason Hempstead, Casjays Developments
# @@Created          :  Wednesday, August 26, 2026 01:18 EDT
# @@File             :  test_content_negotiation.sh
# @@Description      :  Phase 2 content negotiation matrix - every route answered in html, plain and json
# @@Changelog        :  Initial release
# @@TODO             :  None
# @@Other            :  Requires a reachable server; never builds or starts one itself
# @@Resource         :  AI.md PART 14, PART 29
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
# - - - - - - - - - - - - - - - - - - - - - - - - -
# Caller-settable overrides - the API version is never hardcoded to v1
CASHP_BASE_URL="${CASHP_BASE_URL:-http://localhost:64580}"
CASHP_API_VERSION="${CASHP_API_VERSION:-v1}"
CASHP_CURL_TIMEOUT="${CASHP_CURL_TIMEOUT:-15}"
# - - - - - - - - - - - - - - - - - - - - - - - - -
CASHP_BASE_URL="${CASHP_BASE_URL%/}"
API_PREFIX="/api/${CASHP_API_VERSION}"
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
RESPONSE_BODY=""
HTTP_CODE=""
HTTP_CTYPE=""
# - - - - - - - - - - - - - - - - - - - - - - - - -
# Frontend routes: browsers get HTML, curl and friends get plain text
FRONTEND_ROUTES=(
  "/"
  "/server/healthz"
  "/server/metrics"
)
# - - - - - - - - - - - - - - - - - - - - - - - - -
# API routes: machines get JSON, terminals get plain text
API_ROUTES=(
  "${API_PREFIX}/server/healthz"
  "${API_PREFIX}/server/metrics"
  "/api/healthz"
  "/api/metrics"
)
# - - - - - - - - - - - - - - - - - - - - - - - - -
# User agent that must be served HTML even without an explicit Accept preference
BROWSER_UA="Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0 Safari/537.36"
# - - - - - - - - - - - - - - - - - - - - - - - - -
# User agent that must be served plain text even without an explicit Accept preference
CLI_UA="curl/8.9.1"
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
  __help_section "Options"
  __help_line "--help" "Show this help message and exit"
  __help_line "--version" "Show version and exit"
  __help_line "--color auto" "Control color output (auto|yes|no)"
  __help_section "Environment"
  __help_line "CASHP_BASE_URL" "Base URL of the running server (default: http://localhost:64580)"
  __help_line "CASHP_API_VERSION" "API version prefix without slashes (default: v1)"
  __help_line "CASHP_CURL_TIMEOUT" "Per-request timeout in seconds (default: 15)"
  printf '\n'
}
# - - - - - - - - - - - - - - - - - - - - - - - - -
__version() {
  printf '%s %s\n' "${APPNAME}" "${VERSION}"
}
# - - - - - - - - - - - - - - - - - - - - - - - - -
__cleanup() {
  if [[ -n "${TEMP_DIR}" ]] && [[ -d "${TEMP_DIR}" ]]; then
    rm -rf "${TEMP_DIR}"
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
# One request with an explicit Accept header and User-Agent
__request() {
  local _path="$1" _accept="$2" _agent="$3" _meta=""
  HTTP_CODE=""
  HTTP_CTYPE=""
  _meta="$(curl -q -LSs --max-time "${CASHP_CURL_TIMEOUT}" -H "Accept: ${_accept}" -A "${_agent}" -o "${RESPONSE_BODY}" -w '%{http_code} %{content_type}' "${CASHP_BASE_URL}${_path}" 2>/dev/null)" || return 1
  HTTP_CODE="${_meta%% *}"
  HTTP_CTYPE="${_meta#* }"
  return 0
}
# - - - - - - - - - - - - - - - - - - - - - - - - -
# Assert the negotiated content type and that the body actually matches that type
__expect_type() {
  local _path="$1" _accept="$2" _agent="$3" _want="$4" _label="$5"
  if ! __request "${_path}" "${_accept}" "${_agent}"; then
    __fail "${_label} ${_path} - request failed"
    return 0
  fi
  if [[ "${HTTP_CODE}" != "200" ]]; then
    __fail "${_label} ${_path} - expected 200, got ${HTTP_CODE}"
    return 0
  fi
  case "${HTTP_CTYPE}" in
    "${_want}"*) ;;
    *)
      __fail "${_label} ${_path} - expected ${_want}, got ${HTTP_CTYPE}"
      return 0
      ;;
  esac
  case "${_want}" in
    text/html)
      if ! grep -qi -- "<!doctype html" "${RESPONSE_BODY}"; then
        __fail "${_label} ${_path} - body is not an HTML document"
        return 0
      fi
      ;;
    text/plain)
      if grep -qi -- "<!doctype html\|<html" "${RESPONSE_BODY}"; then
        __fail "${_label} ${_path} - plain text body contains HTML markup"
        return 0
      fi
      ;;
    application/json)
      if ! jq -e . "${RESPONSE_BODY}" >/dev/null 2>&1; then
        __fail "${_label} ${_path} - body is not valid JSON"
        return 0
      fi
      ;;
  esac
  __pass "${_label} ${_path} -> ${_want}"
}
# - - - - - - - - - - - - - - - - - - - - - - - - -
# A ?format= query parameter must win over the Accept header
__check_format_override() {
  local _path="$1"
  if ! __request "${_path}?format=json" "text/html" "${BROWSER_UA}"; then
    __fail "format-override ${_path} - request failed"
    return 0
  fi
  if [[ "${HTTP_CODE}" != "200" ]]; then
    __fail "format-override ${_path} - expected 200, got ${HTTP_CODE}"
    return 0
  fi
  case "${HTTP_CTYPE}" in
    application/json*)
      __pass "format-override ${_path}?format=json beats Accept: text/html"
      ;;
    *)
      __fail "format-override ${_path} - expected application/json, got ${HTTP_CTYPE}"
      ;;
  esac
}
# - - - - - - - - - - - - - - - - - - - - - - - - -
__main() {
  local _route=""
  if ! __cmd_exists curl; then
    printf 'ERROR: curl is required but not installed\n' >&2
    exit 69
  fi
  if ! __cmd_exists jq; then
    printf 'ERROR: jq is required but not installed\n' >&2
    exit 69
  fi
  TEMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/cashp-negotiate-XXXXXX")"
  RESPONSE_BODY="${TEMP_DIR}/response.out"
  printf '%sContent negotiation matrix against %s (api: %s)%s\n' "${C_BOLD}" "${CASHP_BASE_URL}" "${CASHP_API_VERSION}" "${C_RESET}"
  __section "Frontend routes with an explicit Accept header"
  for _route in "${FRONTEND_ROUTES[@]}"; do
    __expect_type "${_route}" "text/html" "${BROWSER_UA}" "text/html" "accept-html"
    __expect_type "${_route}" "text/plain" "${CLI_UA}" "text/plain" "accept-text"
  done
  __section "API routes with an explicit Accept header"
  for _route in "${API_ROUTES[@]}"; do
    __expect_type "${_route}" "application/json" "${CLI_UA}" "application/json" "accept-json"
    __expect_type "${_route}" "text/plain" "${CLI_UA}" "text/plain" "accept-text"
  done
  __section "Smart detection from the User-Agent alone"
  for _route in "${FRONTEND_ROUTES[@]}"; do
    __expect_type "${_route}" "*/*" "${BROWSER_UA}" "text/html" "browser-ua"
    __expect_type "${_route}" "*/*" "${CLI_UA}" "text/plain" "cli-ua"
  done
  __section "Query parameter overrides the Accept header"
  for _route in "${FRONTEND_ROUTES[@]}"; do
    __check_format_override "${_route}"
  done
  printf '\n%sTotal: %s  Failed: %s%s\n' "${C_BOLD}" "${TESTS_RUN}" "${TESTS_FAILED}" "${C_RESET}"
  if [[ "${TESTS_FAILED}" -ne 0 ]]; then
    printf '%sContent negotiation suite FAILED%s\n' "${C_RED}" "${C_RESET}" >&2
    exit 1
  fi
  printf '%sContent negotiation suite passed%s\n' "${C_GREEN}" "${C_RESET}"
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
      esac
      if [[ "${OPTARG}" == *=* ]]; then
        val="${OPTARG#*=}"
      else
        val="${!OPTIND}"
        OPTIND=$((OPTIND + 1))
      fi
      case "${flag}" in
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
