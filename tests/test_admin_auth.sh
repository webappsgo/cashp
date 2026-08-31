#!/usr/bin/env bash
# shellcheck shell=bash
# - - - - - - - - - - - - - - - - - - - - - - - - -
##@Version           :  202608260118-git
# @@Author           :  Jason Hempstead
# @@Contact          :  git-admin@casjaysdev.pro
# @@License          :  WTFPL
# @@ReadME           :  test_admin_auth.sh --help
# @@Copyright        :  Copyright: (c) 2026 Jason Hempstead, Casjays Developments
# @@Created          :  Wednesday, August 26, 2026 01:18 EDT
# @@File             :  test_admin_auth.sh
# @@Description      :  Phase 2 admin authentication flow - setup token, account creation, login, token issue, rejection
# @@Changelog        :  Initial release
# @@TODO             :  None
# @@Other            :  Never bypasses auth; debug mode must not weaken any assertion here
# @@Resource         :  AI.md PART 17, PART 29 "Testing Admin Routes"
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
# Caller-settable overrides - docker.sh and incus.sh export these before invoking this suite
CASHP_BASE_URL="${CASHP_BASE_URL:-http://localhost:64580}"
CASHP_API_VERSION="${CASHP_API_VERSION:-v1}"
CASHP_ADMIN_PATH="${CASHP_ADMIN_PATH:-administration}"
CASHP_SETUP_TOKEN="${CASHP_SETUP_TOKEN:-}"
CASHP_SERVER_LOG="${CASHP_SERVER_LOG:-}"
CASHP_ADMIN_USER="${CASHP_ADMIN_USER:-testadmin}"
CASHP_ADMIN_EMAIL="${CASHP_ADMIN_EMAIL:-testadmin@example.com}"
CASHP_ADMIN_PASS="${CASHP_ADMIN_PASS:-Test-Password-1234}"
CASHP_CURL_TIMEOUT="${CASHP_CURL_TIMEOUT:-15}"
# - - - - - - - - - - - - - - - - - - - - - - - - -
# Optional path the issued API token is written to for the CLI and agent suites
CASHP_TOKEN_OUT="${CASHP_TOKEN_OUT:-}"
# - - - - - - - - - - - - - - - - - - - - - - - - -
CASHP_BASE_URL="${CASHP_BASE_URL%/}"
API_PREFIX="/api/${CASHP_API_VERSION}"
ADMIN_API="${API_PREFIX}/server/${CASHP_ADMIN_PATH}"
ADMIN_UI="/server/${CASHP_ADMIN_PATH}"
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
SESSION_TOKEN=""
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
  __help_section "Options"
  __help_line "--help" "Show this help message and exit"
  __help_line "--version" "Show version and exit"
  __help_line "--color auto" "Control color output (auto|yes|no)"
  __help_section "Environment"
  __help_line "CASHP_BASE_URL" "Base URL of the running server (default: http://localhost:64580)"
  __help_line "CASHP_API_VERSION" "API version prefix without slashes (default: v1)"
  __help_line "CASHP_ADMIN_PATH" "Admin panel path segment (default: administration)"
  __help_line "CASHP_SETUP_TOKEN" "First-run setup token printed by the server"
  __help_line "CASHP_SERVER_LOG" "Log file to mine the setup token from when not given"
  __help_line "CASHP_ADMIN_USER" "Admin username to create (default: testadmin)"
  __help_line "CASHP_ADMIN_EMAIL" "Admin email to create (default: testadmin@example.com)"
  __help_line "CASHP_ADMIN_PASS" "Admin password to create (default: Test-Password-1234)"
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
# Redactions keep the key name and replace the value so tokens never reach the log
__redact() {
  local _value="$1"
  if [[ -z "${_value}" ]]; then
    printf 'unset'
  else
    printf '%s' "${_value:0:4}xxxxx"
  fi
}
# - - - - - - - - - - - - - - - - - - - - - - - - -
# Request without following redirects so a login redirect is observable as 302
__request() {
  local _method="$1" _path="$2" _body="$3"
  shift 3
  local _args=(-q -sS --max-time "${CASHP_CURL_TIMEOUT}" -X "${_method}" -H "Accept: application/json")
  if [[ -n "${_body}" ]]; then
    _args+=(-H "Content-Type: application/json" --data "${_body}")
  fi
  while [[ "$#" -gt 0 ]]; do
    _args+=(-H "$1")
    shift
  done
  HTTP_CODE=""
  HTTP_CODE="$(curl "${_args[@]}" -o "${RESPONSE_BODY}" -w '%{http_code}' "${CASHP_BASE_URL}${_path}" 2>/dev/null)" || return 1
  return 0
}
# - - - - - - - - - - - - - - - - - - - - - - - - -
# Mine the setup token out of a server log or journal capture
__extract_setup_token() {
  local _log="$1" _token=""
  if [[ ! -s "${_log}" ]]; then
    return 1
  fi
  _token="$(grep -i -- 'setup token' "${_log}" | grep -oE -- '[A-Za-z0-9_]{24,}' | tail -n1 || true)"
  if [[ -z "${_token}" ]]; then
    return 1
  fi
  printf '%s' "${_token}"
}
# - - - - - - - - - - - - - - - - - - - - - - - - -
# Step 1 - anonymous callers must never reach an admin surface
__check_anonymous_rejected() {
  local _path="$1"
  if ! __request GET "${_path}" ""; then
    __fail "anonymous GET ${_path} - request failed"
    return 0
  fi
  case "${HTTP_CODE}" in
    401 | 403 | 302 | 303)
      __pass "anonymous GET ${_path} rejected with ${HTTP_CODE}"
      ;;
    200)
      __fail "anonymous GET ${_path} returned 200 - admin surface is unprotected"
      ;;
    *)
      __fail "anonymous GET ${_path} - expected 401/403/302, got ${HTTP_CODE}"
      ;;
  esac
}
# - - - - - - - - - - - - - - - - - - - - - - - - -
# Step 2 - a wrong setup token must be rejected just like no token at all
__check_bad_setup_token_rejected() {
  if ! __request GET "${ADMIN_UI}/setup" "" "X-Setup-Token: not_a_valid_setup_token_value"; then
    __fail "bad setup token - request failed"
    return 0
  fi
  case "${HTTP_CODE}" in
    401 | 403 | 302 | 303)
      __pass "invalid X-Setup-Token rejected with ${HTTP_CODE}"
      ;;
    *)
      __fail "invalid X-Setup-Token - expected 401/403/302, got ${HTTP_CODE}"
      ;;
  esac
}
# - - - - - - - - - - - - - - - - - - - - - - - - -
# Step 3 - the real setup token unlocks the first-run setup surface
__check_setup_token_accepted() {
  if ! __request GET "${ADMIN_UI}/setup" "" "X-Setup-Token: ${CASHP_SETUP_TOKEN}"; then
    __fail "valid setup token - request failed"
    return 0
  fi
  if [[ "${HTTP_CODE}" == "200" ]]; then
    __pass "valid X-Setup-Token accepted (${HTTP_CODE})"
  else
    __fail "valid X-Setup-Token - expected 200, got ${HTTP_CODE}"
  fi
}
# - - - - - - - - - - - - - - - - - - - - - - - - -
# Step 4 - create the first administrator through the setup endpoint
__check_admin_created() {
  local _payload=""
  _payload="$(jq -n --arg u "${CASHP_ADMIN_USER}" --arg e "${CASHP_ADMIN_EMAIL}" --arg p "${CASHP_ADMIN_PASS}" '{username:$u,email:$e,password:$p}')"
  if ! __request POST "${ADMIN_API}/setup" "${_payload}" "X-Setup-Token: ${CASHP_SETUP_TOKEN}"; then
    __fail "admin creation - request failed"
    return 1
  fi
  case "${HTTP_CODE}" in
    200 | 201)
      if jq -e '.ok == true and has("data")' "${RESPONSE_BODY}" >/dev/null 2>&1; then
        __pass "admin account created (${HTTP_CODE}, ok:true envelope)"
        return 0
      fi
      __fail "admin creation - expected {ok:true,data} envelope"
      return 1
      ;;
    409)
      __pass "admin account already exists (409) - setup is not repeatable"
      return 0
      ;;
    *)
      __fail "admin creation - expected 200/201/409, got ${HTTP_CODE}"
      return 1
      ;;
  esac
}
# - - - - - - - - - - - - - - - - - - - - - - - - -
# Step 5 - a wrong password must never mint a session
__check_bad_login_rejected() {
  local _payload=""
  _payload="$(jq -n --arg u "${CASHP_ADMIN_USER}" '{username:$u,password:"definitely-the-wrong-password"}')"
  if ! __request POST "${ADMIN_API}/login" "${_payload}"; then
    __fail "invalid login - request failed"
    return 0
  fi
  case "${HTTP_CODE}" in
    400 | 401 | 403 | 422 | 429)
      if jq -e '.ok == false and has("error") and has("message")' "${RESPONSE_BODY}" >/dev/null 2>&1; then
        __pass "invalid credentials rejected with ${HTTP_CODE} and ok:false envelope"
      else
        __fail "invalid credentials rejected with ${HTTP_CODE} but envelope is malformed"
      fi
      ;;
    200)
      __fail "invalid credentials returned 200 - authentication is broken"
      ;;
    *)
      __fail "invalid login - expected 401/403, got ${HTTP_CODE}"
      ;;
  esac
}
# - - - - - - - - - - - - - - - - - - - - - - - - -
# Step 6 - the correct password mints a session token inside the success envelope
__check_login_succeeds() {
  local _payload=""
  _payload="$(jq -n --arg u "${CASHP_ADMIN_USER}" --arg p "${CASHP_ADMIN_PASS}" '{username:$u,password:$p}')"
  if ! __request POST "${ADMIN_API}/login" "${_payload}"; then
    __fail "login - request failed"
    return 1
  fi
  if [[ "${HTTP_CODE}" != "200" ]]; then
    __fail "login - expected 200, got ${HTTP_CODE}"
    return 1
  fi
  if ! jq -e '.ok == true and has("data")' "${RESPONSE_BODY}" >/dev/null 2>&1; then
    __fail "login - expected {ok:true,data} envelope"
    return 1
  fi
  SESSION_TOKEN="$(jq -r '.data.token // .data.session // empty' "${RESPONSE_BODY}")"
  if [[ -z "${SESSION_TOKEN}" ]]; then
    __fail "login - success envelope carried no session token"
    return 1
  fi
  __pass "login succeeded, session token issued ($(__redact "${SESSION_TOKEN}"))"
  return 0
}
# - - - - - - - - - - - - - - - - - - - - - - - - -
# Step 7 - the session token unlocks the protected admin API
__check_authenticated_access() {
  if ! __request GET "${ADMIN_API}/config/settings" "" "Authorization: Bearer ${SESSION_TOKEN}"; then
    __fail "authenticated admin GET - request failed"
    return 0
  fi
  if [[ "${HTTP_CODE}" != "200" ]]; then
    __fail "authenticated admin GET - expected 200, got ${HTTP_CODE}"
    return 0
  fi
  if jq -e '.ok == true and has("data")' "${RESPONSE_BODY}" >/dev/null 2>&1; then
    __pass "authenticated admin GET returned the ok:true envelope"
  else
    __fail "authenticated admin GET - expected {ok:true,data} envelope"
  fi
}
# - - - - - - - - - - - - - - - - - - - - - - - - -
# Step 8 - issue a long-lived API token for the CLI and agent suites
__check_api_token_issued() {
  local _payload=""
  _payload="$(jq -n '{name:"phase2-test-token"}')"
  if ! __request POST "${ADMIN_API}/tokens" "${_payload}" "Authorization: Bearer ${SESSION_TOKEN}"; then
    __fail "api token issue - request failed"
    return 0
  fi
  case "${HTTP_CODE}" in
    200 | 201) ;;
    *)
      __fail "api token issue - expected 200/201, got ${HTTP_CODE}"
      return 0
      ;;
  esac
  API_TOKEN="$(jq -r '.data.token // empty' "${RESPONSE_BODY}")"
  if [[ -z "${API_TOKEN}" ]]; then
    __fail "api token issue - success envelope carried no token"
    return 0
  fi
  if [[ "${API_TOKEN}" =~ ^adm_[A-Za-z0-9]{32}$ ]]; then
    __pass "api token issued in {prefix}_{32} format ($(__redact "${API_TOKEN}"))"
  else
    __fail "api token issued but does not match adm_ + 32 alphanumeric"
  fi
  if [[ -n "${CASHP_TOKEN_OUT}" ]]; then
    printf '%s\n' "${API_TOKEN}" >"${CASHP_TOKEN_OUT}"
  fi
}
# - - - - - - - - - - - - - - - - - - - - - - - - -
# Step 9 - a revoked or garbage bearer token must never be honored
__check_forged_token_rejected() {
  if ! __request GET "${ADMIN_API}/config/settings" "" "Authorization: Bearer adm_00000000000000000000000000000000"; then
    __fail "forged bearer token - request failed"
    return 0
  fi
  case "${HTTP_CODE}" in
    401 | 403)
      __pass "forged bearer token rejected with ${HTTP_CODE}"
      ;;
    200)
      __fail "forged bearer token returned 200 - token validation is broken"
      ;;
    *)
      __fail "forged bearer token - expected 401/403, got ${HTTP_CODE}"
      ;;
  esac
}
# - - - - - - - - - - - - - - - - - - - - - - - - -
__main() {
  if ! __cmd_exists curl; then
    printf 'ERROR: curl is required but not installed\n' >&2
    exit 69
  fi
  if ! __cmd_exists jq; then
    printf 'ERROR: jq is required but not installed\n' >&2
    exit 69
  fi
  if [[ -z "${CASHP_SETUP_TOKEN}" ]] && [[ -n "${CASHP_SERVER_LOG}" ]]; then
    CASHP_SETUP_TOKEN="$(__extract_setup_token "${CASHP_SERVER_LOG}" || true)"
  fi
  if [[ -z "${CASHP_SETUP_TOKEN}" ]]; then
    printf 'ERROR: no setup token available\n' >&2
    printf 'Set CASHP_SETUP_TOKEN, or point CASHP_SERVER_LOG at a log containing it\n' >&2
    exit 64
  fi
  TEMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/cashp-adminauth-XXXXXX")"
  RESPONSE_BODY="${TEMP_DIR}/response.out"
  printf '%sAdmin auth flow against %s (admin path: %s)%s\n' "${C_BOLD}" "${CASHP_BASE_URL}" "${CASHP_ADMIN_PATH}" "${C_RESET}"
  __section "Anonymous access is refused"
  __check_anonymous_rejected "${ADMIN_UI}"
  __check_anonymous_rejected "${ADMIN_API}/config/settings"
  __check_bad_setup_token_rejected
  __section "First run setup"
  __check_setup_token_accepted
  if ! __check_admin_created; then
    printf '\n%sAdmin auth suite FAILED - could not create the admin account%s\n' "${C_RED}" "${C_RESET}" >&2
    exit 1
  fi
  __section "Login"
  __check_bad_login_rejected
  if ! __check_login_succeeds; then
    printf '\n%sAdmin auth suite FAILED - could not log in%s\n' "${C_RED}" "${C_RESET}" >&2
    exit 1
  fi
  __section "Authenticated access and API tokens"
  __check_authenticated_access
  __check_api_token_issued
  __check_forged_token_rejected
  printf '\n%sTotal: %s  Failed: %s%s\n' "${C_BOLD}" "${TESTS_RUN}" "${TESTS_FAILED}" "${C_RESET}"
  if [[ "${TESTS_FAILED}" -ne 0 ]]; then
    printf '%sAdmin auth suite FAILED%s\n' "${C_RED}" "${C_RESET}" >&2
    exit 1
  fi
  printf '%sAdmin auth suite passed%s\n' "${C_GREEN}" "${C_RESET}"
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
