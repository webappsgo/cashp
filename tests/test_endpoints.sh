#!/usr/bin/env bash
# shellcheck shell=bash
# - - - - - - - - - - - - - - - - - - - - - - - - -
##@Version           :  202608260118-git
# @@Author           :  Jason Hempstead
# @@Contact          :  git-admin@casjaysdev.pro
# @@License          :  WTFPL
# @@ReadME           :  test_endpoints.sh --help
# @@Copyright        :  Copyright: (c) 2026 Jason Hempstead, Casjays Developments
# @@Created          :  Wednesday, August 26, 2026 01:18 EDT
# @@File             :  test_endpoints.sh
# @@Description      :  Phase 2 endpoint coverage suite - hits a running server and asserts envelope, status, and content type
# @@Changelog        :  Initial release
# @@TODO             :  None
# @@Other            :  Requires a reachable server; never builds or starts one itself
# @@Resource         :  AI.md PART 13, PART 14, PART 29
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
# Caller-settable overrides - every value has a sane default and is never hardcoded inline
CASHP_BASE_URL="${CASHP_BASE_URL:-http://localhost:64580}"
CASHP_API_VERSION="${CASHP_API_VERSION:-v1}"
CASHP_ADMIN_PATH="${CASHP_ADMIN_PATH:-administration}"
CASHP_CURL_TIMEOUT="${CASHP_CURL_TIMEOUT:-15}"
# - - - - - - - - - - - - - - - - - - - - - - - - -
# Strip any trailing slash so route concatenation never produces a double slash
CASHP_BASE_URL="${CASHP_BASE_URL%/}"
API_PREFIX="/api/${CASHP_API_VERSION}"
# - - - - - - - - - - - - - - - - - - - - - - - - -
# Color output honors NO_COLOR (no-color.org) and --color no
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
# Routes that render HTML for browsers and plain text for CLI clients (PART 14 content negotiation)
FRONTEND_ROUTES=(
  "/"
  "/server/healthz"
  "/server/metrics"
)
# - - - - - - - - - - - - - - - - - - - - - - - - -
# Versioned API routes that MUST answer both application/json and text/plain
API_ROUTES=(
  "${API_PREFIX}/server/healthz"
  "${API_PREFIX}/server/metrics"
)
# - - - - - - - - - - - - - - - - - - - - - - - - -
# Unversioned machine-friendly aliases mounted on the same handler as their versioned route
API_ALIAS_ROUTES=(
  "/api/healthz"
  "/api/metrics"
)
# - - - - - - - - - - - - - - - - - - - - - - - - -
# Health routes return the BARE health object - never the ok/data envelope (PART 13)
HEALTH_JSON_ROUTES=(
  "${API_PREFIX}/server/healthz"
  "/api/healthz"
)
# - - - - - - - - - - - - - - - - - - - - - - - - -
# Every .txt endpoint the server serves; each must return 200 with a text content type
TXT_ENDPOINTS=(
  "/robots.txt"
  "/.well-known/security.txt"
  "${API_PREFIX}/server/healthz.txt"
)
# - - - - - - - - - - - - - - - - - - - - - - - - -
# Static generated documents that are neither HTML pages nor API resources
STATIC_ROUTES=(
  "/sitemap.xml"
  "/favicon.ico"
)
# - - - - - - - - - - - - - - - - - - - - - - - - -
# Admin routes that MUST reject unauthenticated callers (401/403/302 to login)
PROTECTED_ROUTES=(
  "/server/${CASHP_ADMIN_PATH}"
  "${API_PREFIX}/server/${CASHP_ADMIN_PATH}/config/settings"
)
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
__help_footer() {
  printf '\n'
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
  __help_line "CASHP_ADMIN_PATH" "Admin panel path segment (default: administration)"
  __help_line "CASHP_CURL_TIMEOUT" "Per-request timeout in seconds (default: 15)"
  __help_footer
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
# Perform one request; body lands in RESPONSE_BODY, status in HTTP_CODE, content type in HTTP_CTYPE
__request() {
  local _method="$1" _path="$2" _accept="$3" _meta=""
  HTTP_CODE=""
  HTTP_CTYPE=""
  _meta="$(curl -q -LSs --max-time "${CASHP_CURL_TIMEOUT}" -X "${_method}" -H "Accept: ${_accept}" -o "${RESPONSE_BODY}" -w '%{http_code} %{content_type}' "${CASHP_BASE_URL}${_path}" 2>/dev/null)" || return 1
  HTTP_CODE="${_meta%% *}"
  HTTP_CTYPE="${_meta#* }"
  return 0
}
# - - - - - - - - - - - - - - - - - - - - - - - - -
# A frontend route asked for HTML must answer 200 with a real HTML document
__check_frontend_html() {
  local _path="$1"
  if ! __request GET "${_path}" "text/html"; then
    __fail "GET ${_path} (text/html) - request failed"
    return 0
  fi
  if [[ "${HTTP_CODE}" != "200" ]]; then
    __fail "GET ${_path} (text/html) - expected 200, got ${HTTP_CODE}"
    return 0
  fi
  if [[ "${HTTP_CTYPE}" != text/html* ]]; then
    __fail "GET ${_path} (text/html) - expected text/html, got ${HTTP_CTYPE}"
    return 0
  fi
  if grep -qi -- "<!doctype html" "${RESPONSE_BODY}"; then
    __pass "GET ${_path} (text/html)"
  else
    __fail "GET ${_path} (text/html) - body is not an HTML document"
  fi
}
# - - - - - - - - - - - - - - - - - - - - - - - - -
# The same frontend route asked for plain text must answer text, never HTML markup
__check_frontend_text() {
  local _path="$1"
  if ! __request GET "${_path}" "text/plain"; then
    __fail "GET ${_path} (text/plain) - request failed"
    return 0
  fi
  if [[ "${HTTP_CODE}" != "200" ]]; then
    __fail "GET ${_path} (text/plain) - expected 200, got ${HTTP_CODE}"
    return 0
  fi
  if [[ "${HTTP_CTYPE}" != text/plain* ]]; then
    __fail "GET ${_path} (text/plain) - expected text/plain, got ${HTTP_CTYPE}"
    return 0
  fi
  if grep -qi -- "<!doctype html\|<html" "${RESPONSE_BODY}"; then
    __fail "GET ${_path} (text/plain) - body contains HTML markup"
  else
    __pass "GET ${_path} (text/plain)"
  fi
}
# - - - - - - - - - - - - - - - - - - - - - - - - -
# An API route asked for JSON must answer 200 with parseable JSON
__check_api_json() {
  local _path="$1"
  if ! __request GET "${_path}" "application/json"; then
    __fail "GET ${_path} (application/json) - request failed"
    return 0
  fi
  if [[ "${HTTP_CODE}" != "200" ]]; then
    __fail "GET ${_path} (application/json) - expected 200, got ${HTTP_CODE}"
    return 0
  fi
  if [[ "${HTTP_CTYPE}" != application/json* ]]; then
    __fail "GET ${_path} (application/json) - expected application/json, got ${HTTP_CTYPE}"
    return 0
  fi
  if jq -e . "${RESPONSE_BODY}" >/dev/null 2>&1; then
    __pass "GET ${_path} (application/json)"
  else
    __fail "GET ${_path} (application/json) - body is not valid JSON"
  fi
}
# - - - - - - - - - - - - - - - - - - - - - - - - -
# An API route asked for plain text must answer text, never JSON or HTML
__check_api_text() {
  local _path="$1"
  if ! __request GET "${_path}" "text/plain"; then
    __fail "GET ${_path} (text/plain) - request failed"
    return 0
  fi
  if [[ "${HTTP_CODE}" != "200" ]]; then
    __fail "GET ${_path} (text/plain) - expected 200, got ${HTTP_CODE}"
    return 0
  fi
  if [[ "${HTTP_CTYPE}" != text/plain* ]]; then
    __fail "GET ${_path} (text/plain) - expected text/plain, got ${HTTP_CTYPE}"
    return 0
  fi
  if grep -qi -- "<!doctype html\|<html" "${RESPONSE_BODY}"; then
    __fail "GET ${_path} (text/plain) - body contains HTML markup"
  else
    __pass "GET ${_path} (text/plain)"
  fi
}
# - - - - - - - - - - - - - - - - - - - - - - - - -
# Health payloads are the bare health object - an ok/data envelope here is a PART 13 violation
__check_health_bare_json() {
  local _path="$1"
  if ! __request GET "${_path}" "application/json"; then
    __fail "GET ${_path} (health bare JSON) - request failed"
    return 0
  fi
  if [[ "${HTTP_CODE}" != "200" ]]; then
    __fail "GET ${_path} (health bare JSON) - expected 200, got ${HTTP_CODE}"
    return 0
  fi
  if ! jq -e 'type == "object"' "${RESPONSE_BODY}" >/dev/null 2>&1; then
    __fail "GET ${_path} (health bare JSON) - body is not a JSON object"
    return 0
  fi
  if jq -e 'has("ok") or has("data")' "${RESPONSE_BODY}" >/dev/null 2>&1; then
    __fail "GET ${_path} (health bare JSON) - health response is enveloped, must be bare"
    return 0
  fi
  if jq -e 'has("status") and has("version")' "${RESPONSE_BODY}" >/dev/null 2>&1; then
    __pass "GET ${_path} (health bare JSON)"
  else
    __fail "GET ${_path} (health bare JSON) - missing required status/version fields"
  fi
}
# - - - - - - - - - - - - - - - - - - - - - - - - -
# Every .txt endpoint must return 200 and a text content type
__check_txt_endpoint() {
  local _path="$1"
  if ! __request GET "${_path}" "*/*"; then
    __fail "GET ${_path} (.txt) - request failed"
    return 0
  fi
  if [[ "${HTTP_CODE}" != "200" ]]; then
    __fail "GET ${_path} (.txt) - expected 200, got ${HTTP_CODE}"
    return 0
  fi
  if [[ "${HTTP_CTYPE}" == text/* ]]; then
    __pass "GET ${_path} (.txt)"
  else
    __fail "GET ${_path} (.txt) - expected a text content type, got ${HTTP_CTYPE}"
  fi
}
# - - - - - - - - - - - - - - - - - - - - - - - - -
# Generated static documents only need to exist and return 200
__check_static_route() {
  local _path="$1"
  if ! __request GET "${_path}" "*/*"; then
    __fail "GET ${_path} (static) - request failed"
    return 0
  fi
  if [[ "${HTTP_CODE}" == "200" ]]; then
    __pass "GET ${_path} (static)"
  else
    __fail "GET ${_path} (static) - expected 200, got ${HTTP_CODE}"
  fi
}
# - - - - - - - - - - - - - - - - - - - - - - - - -
# Admin surfaces must never answer 200 to an anonymous caller
__check_protected_route() {
  local _path="$1"
  if ! __request GET "${_path}" "application/json"; then
    __fail "GET ${_path} (unauthenticated) - request failed"
    return 0
  fi
  case "${HTTP_CODE}" in
    401 | 403 | 302 | 303)
      __pass "GET ${_path} (unauthenticated rejected with ${HTTP_CODE})"
      ;;
    200)
      __fail "GET ${_path} (unauthenticated) - admin route is NOT protected"
      ;;
    *)
      __fail "GET ${_path} (unauthenticated) - expected 401/403/302, got ${HTTP_CODE}"
      ;;
  esac
}
# - - - - - - - - - - - - - - - - - - - - - - - - -
# An unknown API path must answer 404 wrapped in the ok:false error envelope
__check_api_error_envelope() {
  local _path="${API_PREFIX}/this-route-does-not-exist"
  if ! __request GET "${_path}" "application/json"; then
    __fail "GET ${_path} (error envelope) - request failed"
    return 0
  fi
  if [[ "${HTTP_CODE}" != "404" ]]; then
    __fail "GET ${_path} (error envelope) - expected 404, got ${HTTP_CODE}"
    return 0
  fi
  if jq -e '.ok == false and (.error | type == "string") and (.message | type == "string")' "${RESPONSE_BODY}" >/dev/null 2>&1; then
    __pass "GET ${_path} (ok:false error envelope)"
  else
    __fail "GET ${_path} (error envelope) - expected {ok:false,error,message}"
  fi
}
# - - - - - - - - - - - - - - - - - - - - - - - - -
# An unknown frontend path must render the themed 404 page, not a bare body
__check_frontend_not_found() {
  local _path="/this-page-does-not-exist"
  if ! __request GET "${_path}" "text/html"; then
    __fail "GET ${_path} (404 page) - request failed"
    return 0
  fi
  if [[ "${HTTP_CODE}" != "404" ]]; then
    __fail "GET ${_path} (404 page) - expected 404, got ${HTTP_CODE}"
    return 0
  fi
  if grep -qi -- "<!doctype html" "${RESPONSE_BODY}"; then
    __pass "GET ${_path} (themed 404 page)"
  else
    __fail "GET ${_path} (404 page) - body is not an HTML document"
  fi
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
  TEMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/cashp-endpoints-XXXXXX")"
  RESPONSE_BODY="${TEMP_DIR}/response.out"
  printf '%sEndpoint suite against %s (api: %s, admin: %s)%s\n' "${C_BOLD}" "${CASHP_BASE_URL}" "${CASHP_API_VERSION}" "${CASHP_ADMIN_PATH}" "${C_RESET}"
  __section "Frontend routes - text/html and text/plain"
  for _route in "${FRONTEND_ROUTES[@]}"; do
    __check_frontend_html "${_route}"
    __check_frontend_text "${_route}"
  done
  __section "API routes - application/json and text/plain"
  for _route in "${API_ROUTES[@]}"; do
    __check_api_json "${_route}"
    __check_api_text "${_route}"
  done
  __section "Unversioned API aliases"
  for _route in "${API_ALIAS_ROUTES[@]}"; do
    __check_api_json "${_route}"
  done
  __section "Health endpoints - bare JSON object, never enveloped"
  for _route in "${HEALTH_JSON_ROUTES[@]}"; do
    __check_health_bare_json "${_route}"
  done
  __section "Plain text (.txt) endpoints"
  for _route in "${TXT_ENDPOINTS[@]}"; do
    __check_txt_endpoint "${_route}"
  done
  __section "Generated static documents"
  for _route in "${STATIC_ROUTES[@]}"; do
    __check_static_route "${_route}"
  done
  __section "Admin routes reject anonymous callers"
  for _route in "${PROTECTED_ROUTES[@]}"; do
    __check_protected_route "${_route}"
  done
  __section "Error paths"
  __check_api_error_envelope
  __check_frontend_not_found
  printf '\n%sTotal: %s  Failed: %s%s\n' "${C_BOLD}" "${TESTS_RUN}" "${TESTS_FAILED}" "${C_RESET}"
  if [[ "${TESTS_FAILED}" -ne 0 ]]; then
    printf '%sEndpoint suite FAILED%s\n' "${C_RED}" "${C_RESET}" >&2
    exit 1
  fi
  printf '%sEndpoint suite passed%s\n' "${C_GREEN}" "${C_RESET}"
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
