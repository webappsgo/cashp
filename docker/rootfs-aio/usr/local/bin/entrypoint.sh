#!/bin/bash
set -eo pipefail

# =============================================================================
# All-in-One Container Entrypoint Script
# Prepares the EXTERNAL services (PostgreSQL, Valkey) and hands off to supervisor.
# The cashp binary still owns its own directories, permissions, users and Tor.
# =============================================================================

# Set timezone
if [ -n "$TZ" ]; then
    ln -snf "/usr/share/zoneinfo/$TZ" /etc/localtime
    echo "$TZ" > /etc/timezone
fi

# Setup directories for EXTERNAL services only (PostgreSQL, Valkey)
# NOTE: App directories (config, data, sqlite, logs) are created by the server binary
# External services need special ownership that binary can't set
mkdir -p /data/db/postgres /data/db/valkey /data/log/postgres /run/postgresql /run/valkey
chown -R postgres:postgres /data/db/postgres /data/log/postgres /run/postgresql
chmod 700 /data/db/postgres
chmod 755 /run/valkey

# Initialize PostgreSQL if not already done
if [ ! -f /data/db/postgres/PG_VERSION ]; then
    echo "Initializing PostgreSQL database..."
    su - postgres -c "initdb -D /data/db/postgres"

    # Copy optimized config baked into the image (/config is shadowed by the runtime volume mount)
    cp /usr/share/cashp/postgres/postgresql.conf /data/db/postgres/postgresql.conf
    chown postgres:postgres /data/db/postgres/postgresql.conf

    # Start PostgreSQL temporarily to create database and user
    su - postgres -c "pg_ctl -D /data/db/postgres -l /data/log/postgres/init.log start"
    sleep 3

    # Create application database and user
    su - postgres -c "psql -c \"CREATE USER ${DB_USER:-cashp} WITH PASSWORD '${DB_PASSWORD:-cashp}';\""
    su - postgres -c "psql -c \"CREATE DATABASE ${DB_NAME:-cashp} OWNER ${DB_USER:-cashp};\""
    su - postgres -c "psql -c \"GRANT ALL PRIVILEGES ON DATABASE ${DB_NAME:-cashp} TO ${DB_USER:-cashp};\""

    # Stop PostgreSQL (supervisor will start it)
    su - postgres -c "pg_ctl -D /data/db/postgres stop"
fi

# Set Tor enabled flag for supervisor
# Tor is auto-enabled: the binary is installed in the AIO image; set TOR_ENABLED=false to opt out
export TOR_ENABLED="${TOR_ENABLED:-true}"

# Set I2P enabled flag (OPT-IN: disabled by default, unlike Tor). Set I2P_ENABLED=true
# to enable the eepsite; the app then prefers the i2pd binary and falls back to SAM.
export I2P_ENABLED="${I2P_ENABLED:-false}"

# Start supervisor (manages postgresql + valkey + tor + app)
exec /usr/bin/supervisord -c /etc/supervisor/supervisord.conf
