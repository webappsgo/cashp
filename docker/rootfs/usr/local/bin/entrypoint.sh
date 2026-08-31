#!/usr/bin/env bash
set -eo pipefail

# =============================================================================
# Container Entrypoint Script - MINIMAL
# Only: set env, start services, start binary, handle signals
# Binary handles: directories, permissions, user/group, Tor, etc.
# =============================================================================

APP_NAME="cashp"
APP_BIN="/usr/local/bin/${APP_NAME}"

# Export environment defaults (binary reads these)
export TZ="${TZ:-America/New_York}"
export CONFIG_DIR="${CONFIG_DIR:-/config/${APP_NAME}}"
export DATA_DIR="${DATA_DIR:-/data/${APP_NAME}}"

# Track background PIDs for cleanup
declare -a PIDS=()

log() { echo "[entrypoint] $(date '+%Y-%m-%dT%H:%M:%S%z') $*"; }

# Signal handling for graceful shutdown
cleanup() {
    log "Shutdown signal received..."
    for ((i=${#PIDS[@]}-1; i>=0; i--)); do
        kill -TERM "${PIDS[i]}" 2>/dev/null || true
    done
    wait
    exit 0
}
trap cleanup SIGTERM SIGINT SIGQUIT

# =============================================================================
# Start main application
# =============================================================================
log "Starting ${APP_NAME}..."

# Build flags from environment
FLAGS="--address ${ADDRESS:-0.0.0.0} --port ${PORT:-80}"
# DEBUG accepts all truthy values: true/yes/1/on/enable (case-insensitive)
case "$(echo "${DEBUG:-false}" | tr '[:upper:]' '[:lower:]')" in
    true|yes|1|on|enable) FLAGS="$FLAGS --debug" ;;
esac

# Start binary (binary handles ALL setup: dirs, perms, user/group, Tor, etc.)
exec $APP_BIN $FLAGS "$@"
