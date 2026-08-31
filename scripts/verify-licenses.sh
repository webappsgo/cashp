#!/usr/bin/env bash
# shellcheck shell=bash
# - - - - - - - - - - - - - - - - - - - - - - - - -
##@Version           :  202608260118-git
# @@Author           :  Jason Hempstead
# @@Contact          :  git-admin@casjaysdev.pro
# @@License          :  WTFPL
# @@ReadME           :  verify-licenses.sh --help
# @@Copyright        :  Copyright: (c) 2026 Jason Hempstead, Casjays Developments
# @@Created          :  Wednesday, August 26, 2026 01:18 EDT
# @@File             :  verify-licenses.sh
# @@Description      :  Reject copyleft dependencies and regenerate the third party license report
# @@Changelog        :  Initial release
# @@TODO             :  None
# @@Other            :  go-licenses ships inside casjaysdev/go:latest and is never installed inline
# @@Resource         :  AI.md PART 2 "License Verification Script"
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
if [[ -z "${PROJECT_NAME}" ]]; then
  PROJECT_NAME="$(basename -- "${PROJECT_DIR}")"
fi
# - - - - - - - - - - - - - - - - - - - - - - - - -
# Caller-settable overrides
CASHP_GO_IMAGE="${CASHP_GO_IMAGE:-casjaysdev/go:latest}"
CASHP_LICENSE_CSV="${CASHP_LICENSE_CSV:-licenses.csv}"
CASHP_LICENSE_DIR="${CASHP_LICENSE_DIR:-third_party_licenses}"
CASHP_LICENSE_DENY="${CASHP_LICENSE_DENY:-GPL|AGPL|LGPL}"
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
CHECK_ONLY="false"
TEMP_DIR=""
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
  printf '  Fails when any dependency carries a copyleft license, then regenerates\n'
  printf '  %s and %s/. Re-executes itself inside\n' "${CASHP_LICENSE_CSV}" "${CASHP_LICENSE_DIR}"
  printf '  %s when go-licenses is not on PATH.\n' "${CASHP_GO_IMAGE}"
  __help_section "Options"
  __help_line "--help" "Show this help message and exit"
  __help_line "--version" "Show version and exit"
  __help_line "--check" "Only scan; do not write the report files"
  __help_line "--color auto" "Control color output (auto|yes|no)"
  __help_section "Environment"
  __help_line "CASHP_GO_IMAGE" "Toolchain image (default: casjaysdev/go:latest)"
  __help_line "CASHP_LICENSE_CSV" "CSV report path (default: licenses.csv)"
  __help_line "CASHP_LICENSE_DIR" "Directory for saved licenses (default: third_party_licenses)"
  __help_line "CASHP_LICENSE_DENY" "Denied license regex (default: GPL|AGPL|LGPL)"
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
__rand_suffix() {
  tr -dc 'a-z0-9' </dev/urandom | head -c8
}
# - - - - - - - - - - - - - - - - - - - - - - - - -
# Without go-licenses on PATH the only correct move is to rerun inside the toolchain image
__reexec_in_docker() {
  local _args=()
  if ! __cmd_exists docker; then
    printf 'ERROR: go-licenses not found and docker is unavailable\n' >&2
    printf 'Run this script inside %s\n' "${CASHP_GO_IMAGE}" >&2
    exit 69
  fi
  printf 'go-licenses not on PATH - re-running inside %s...\n' "${CASHP_GO_IMAGE}"
  mkdir -p "${GO_CACHE}" "${GO_BUILD}"
  _args=(
    docker run --rm
    --name "${PROJECT_NAME}-licenses-$(__rand_suffix)"
    -v "${PROJECT_DIR}:/app"
    -v "${GO_CACHE}:/usr/local/share/go/pkg/mod"
    -v "${GO_BUILD}:/usr/local/share/go/cache"
    -w /app
    -e CGO_ENABLED=0
    -e GOFLAGS=-buildvcs=false
    -e "CASHP_LICENSE_CSV=${CASHP_LICENSE_CSV}"
    -e "CASHP_LICENSE_DIR=${CASHP_LICENSE_DIR}"
    -e "CASHP_LICENSE_DENY=${CASHP_LICENSE_DENY}"
    "${CASHP_GO_IMAGE}"
    bash "/app/scripts/${APPNAME}"
  )
  if [[ "${CHECK_ONLY}" == "true" ]]; then
    _args+=(--check)
  fi
  exec "${_args[@]}"
}
# - - - - - - - - - - - - - - - - - - - - - - - - -
__main() {
  local _csv=""
  if ! __cmd_exists go-licenses; then
    __reexec_in_docker
  fi
  TEMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/cashp-licenses-XXXXXX")"
  _csv="${TEMP_DIR}/licenses.csv"
  printf '%sScanning dependency licenses...%s\n' "${C_BOLD}" "${C_RESET}"
  if ! go-licenses csv ./... >"${_csv}"; then
    printf '%sERROR: go-licenses could not enumerate dependencies%s\n' "${C_RED}" "${C_RESET}" >&2
    exit 70
  fi
  if grep -iE -- "${CASHP_LICENSE_DENY}" "${_csv}"; then
    printf '%sERROR: copyleft license detected%s\n' "${C_RED}" "${C_RESET}" >&2
    printf 'Remove the dependency or find an alternative.\n' >&2
    exit 1
  fi
  printf '%sAll licenses are compatible%s\n' "${C_GREEN}" "${C_RESET}"
  if [[ "${CHECK_ONLY}" == "true" ]]; then
    return 0
  fi
  printf 'Generating the license report...\n'
  install -m 0644 -- "${_csv}" "${PROJECT_DIR}/${CASHP_LICENSE_CSV}"
  rm -rf "${PROJECT_DIR:?}/${CASHP_LICENSE_DIR}"
  go-licenses save ./... --save_path="${PROJECT_DIR}/${CASHP_LICENSE_DIR}"
  printf '%sLicense report saved to %s and %s/%s\n' "${C_GREEN}" "${CASHP_LICENSE_CSV}" "${CASHP_LICENSE_DIR}" "${C_RESET}"
  printf '\nNext steps:\n'
  printf '  1. Review %s\n' "${CASHP_LICENSE_CSV}"
  printf '  2. Update LICENSE.md with any new dependencies\n'
  printf '  3. Commit the changes\n'
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
        check)
          CHECK_ONLY="true"
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
