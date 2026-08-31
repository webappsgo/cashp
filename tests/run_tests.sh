#!/usr/bin/env bash
# shellcheck shell=bash
# - - - - - - - - - - - - - - - - - - - - - - - - -
##@Version           :  202608260118-git
# @@Author           :  Jason Hempstead
# @@Contact          :  git-admin@casjaysdev.pro
# @@License          :  WTFPL
# @@ReadME           :  run_tests.sh --help
# @@Copyright        :  Copyright: (c) 2026 Jason Hempstead, Casjays Developments
# @@Created          :  Wednesday, August 26, 2026 01:18 EDT
# @@File             :  run_tests.sh
# @@Description      :  Phase 2 entry point - picks the best available container runtime and runs the binary validation suite
# @@Changelog        :  Initial release
# @@TODO             :  None
# @@Other            :  Incus is preferred because it gives real systemd; docker is the fallback
# @@Resource         :  AI.md PART 29
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
# - - - - - - - - - - - - - - - - - - - - - - - - -
# Force a runtime instead of autodetecting - accepted values: auto, incus, docker
CASHP_TEST_RUNTIME="${CASHP_TEST_RUNTIME:-auto}"
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
  printf '  Phase 2 binary validation. Builds in casjaysdev/go:latest, then exercises\n'
  printf '  the resulting binaries inside a throwaway container.\n'
  __help_section "Options"
  __help_line "--help" "Show this help message and exit"
  __help_line "--version" "Show version and exit"
  __help_line "--runtime auto" "Force a runtime (auto|incus|docker)"
  __help_section "Environment"
  __help_line "CASHP_TEST_RUNTIME" "Same as --runtime (default: auto)"
  __help_line "CASHP_TEST_PORT" "Host port the test server listens on (default: 64580)"
  __help_line "CASHP_API_VERSION" "API version prefix without slashes (default: v1)"
  __help_line "CASHP_ADMIN_PATH" "Admin panel path segment (default: administration)"
  __help_section "Notes"
  printf '  Browser end to end tests are separate and never run here - use tests/e2e.sh.\n\n'
}
# - - - - - - - - - - - - - - - - - - - - - - - - -
__version() {
  printf '%s %s\n' "${APPNAME}" "${VERSION}"
}
# - - - - - - - - - - - - - - - - - - - - - - - - -
__cmd_exists() {
  command -v "$1" &>/dev/null
}
# - - - - - - - - - - - - - - - - - - - - - - - - -
__no_runtime() {
  printf 'ERROR: neither incus nor docker was found\n' >&2
  printf 'Please install one of the following:\n' >&2
  printf '  - Incus (preferred): https://linuxcontainers.org/incus/\n' >&2
  printf '  - Docker (fallback): https://docker.com/\n' >&2
  exit 69
}
# - - - - - - - - - - - - - - - - - - - - - - - - -
__main() {
  case "${CASHP_TEST_RUNTIME}" in
    incus)
      if ! __cmd_exists incus; then
        printf 'ERROR: CASHP_TEST_RUNTIME=incus but incus is not installed\n' >&2
        exit 69
      fi
      printf 'Runtime forced to incus - running full systemd tests...\n'
      exec "${SCRIPT_DIR}/incus.sh" "$@"
      ;;
    docker)
      if ! __cmd_exists docker; then
        printf 'ERROR: CASHP_TEST_RUNTIME=docker but docker is not installed\n' >&2
        exit 69
      fi
      printf 'Runtime forced to docker - running container tests...\n'
      exec "${SCRIPT_DIR}/docker.sh" "$@"
      ;;
    auto)
      if __cmd_exists incus; then
        printf 'Incus detected - running full systemd tests...\n'
        exec "${SCRIPT_DIR}/incus.sh" "$@"
      elif __cmd_exists docker; then
        printf 'Docker detected - running container tests...\n'
        exec "${SCRIPT_DIR}/docker.sh" "$@"
      else
        __no_runtime
      fi
      ;;
    *)
      printf 'ERROR: unknown runtime "%s" - expected auto, incus or docker\n' "${CASHP_TEST_RUNTIME}" >&2
      exit 64
      ;;
  esac
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
        runtime)
          CASHP_TEST_RUNTIME="${val}"
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
if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
  __main "$@"
fi
# - - - - - - - - - - - - - - - - - - - - - - - - -
# ex: ts=2 sw=2 et filetype=sh
