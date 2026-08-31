package database

// SQLite is the default driver and the only one used by a single-instance
// deployment. modernc.org/sqlite is the pure-Go implementation; the cgo
// mattn/go-sqlite3 driver is banned because every build is CGO_ENABLED=0.
// It registers itself under the name "sqlite".
import _ "modernc.org/sqlite"
