package database

// PostgreSQL support for cluster deployments. The pgx v5 stdlib shim
// registers a database/sql driver under the name "pgx".
import _ "github.com/jackc/pgx/v5/stdlib"
