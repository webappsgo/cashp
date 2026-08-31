package dbservice

import (
	"io"
	"strconv"
	"strings"
	"time"
)

// postgresAdapter manages PostgreSQL instances. Everything it does goes
// through the official client binaries already present in the engine image:
// psql for statements, pg_dump and pg_dumpall for backups. Statement text is
// always delivered on standard input and the password always through the
// PGPASSWORD environment variable, so neither ever reaches an argument list.
type postgresAdapter struct{}

// postgresVersions are the supported major versions, oldest first.
var postgresVersions = []string{"13", "14", "15", "16", "17"}

// postgresPublication is the publication every replicated database on a
// primary uses. The name is a constant, never derived from tenant input.
const postgresPublication = "cashp_pub"

// postgresSubscriptionPrefix prefixes the per-database subscription name on a
// replica. The suffix is a validated database identifier.
const postgresSubscriptionPrefix = "cashp_sub_"

// postgresHealthToken is the literal a health probe selects. Finding it in the
// output proves the engine parsed and executed a statement, which a socket
// connectivity check alone would not.
const postgresHealthToken = "cashp_ok"

func (postgresAdapter) engine() Engine { return EnginePostgres }

func (postgresAdapter) versions() []string { return postgresVersions }

func (postgresAdapter) image(version string) string {
	return "docker.io/library/postgres:" + version + "-alpine"
}

func (postgresAdapter) defaultPort() int { return 5432 }

func (postgresAdapter) scheme() string { return "postgresql" }

func (postgresAdapter) dataPath() string { return "/var/lib/postgresql/data" }

// adminUser is the superuser the image bootstraps from the environment.
func (postgresAdapter) adminUser() string { return "cashp_admin" }

func (postgresAdapter) capabilities() Capabilities {
	return Capabilities{
		NamedDatabases:  true,
		Users:           true,
		Grants:          true,
		Replicas:        true,
		InPlaceUpgrade:  false,
		PerDatabaseDump: true,
		OnlineRestore:   true,
	}
}

// upgradeStrategy is dump and restore because the PostgreSQL on-disk format
// changes between major versions and a new major refuses to start on an older
// data directory.
func (postgresAdapter) upgradeStrategy() UpgradeStrategy { return StrategyDumpRestore }

func (a postgresAdapter) containerSpec(p specParams) ContainerSpec {
	spec := baseContainerSpec(p, a, a.image(p.Instance.EngineVersion))
	spec.Env = map[string]string{
		"POSTGRES_USER":     p.AdminUser,
		"POSTGRES_PASSWORD": p.AdminPassword,
		"POSTGRES_DB":       "postgres",
		// PGDATA is a subdirectory of the mount point so the volume root can
		// hold a lost+found entry without upsetting initdb.
		"PGDATA": a.dataPath() + "/pgdata",
	}
	// Logical replication is enabled from the first boot: turning it on later
	// requires a restart, and a tenant adding a replica should not have to
	// take an outage to do it.
	spec.Cmd = []string{
		"postgres",
		"-c", "wal_level=logical",
		"-c", "max_wal_senders=10",
		"-c", "max_replication_slots=10",
		"-c", "listen_addresses=*",
	}
	return spec
}

// bootstrapFiles is empty: PostgreSQL takes its bootstrap credentials from the
// container environment, so no secret needs to be staged on disk.
func (postgresAdapter) bootstrapFiles(p specParams) []fileDrop { return nil }

// postStartCommands revokes the public schema grant every fresh cluster ships
// with, so a new account has no implicit access anywhere.
func (a postgresAdapter) postStartCommands(c engineCtx) ([]command, error) {
	cmd, err := a.psql(c, "postgres", []string{
		"REVOKE ALL ON SCHEMA public FROM PUBLIC;",
		"REVOKE ALL ON DATABASE postgres FROM PUBLIC;",
	}, adminTimeout)
	if err != nil {
		return nil, err
	}
	return []command{cmd}, nil
}

func (a postgresAdapter) healthCommand(c engineCtx) command {
	argv := append(a.psqlArgv(c, "postgres"), "-t", "-A")
	return command{Exec: execRequest(argv, a.env(c),
		strings.NewReader("SELECT '"+postgresHealthToken+"', pg_is_in_recovery();\n"), healthTimeout)}
}

func (postgresAdapter) parseHealth(res ExecResult) (HealthState, string) {
	if res.ExitCode != 0 {
		return HealthUnhealthy, "The engine did not answer its health probe."
	}
	for _, line := range trimLines(res.Stdout) {
		if !strings.HasPrefix(line, postgresHealthToken) {
			continue
		}
		if strings.HasSuffix(line, "|t") {
			return HealthHealthy, "Serving reads in recovery mode."
		}
		return HealthHealthy, "Accepting reads and writes."
	}
	return HealthDegraded, "The engine answered but did not complete the health statement."
}

func (a postgresAdapter) createDatabase(c engineCtx, name, owner string) ([]command, error) {
	if err := ValidateIdentifier(EnginePostgres, "database name", name); err != nil {
		return nil, err
	}
	if owner == "" {
		owner = c.AdminUser
	}
	if err := ValidateIdentifier(EnginePostgres, "username", owner); err != nil {
		return nil, err
	}
	db, err := QuoteIdentifier(EnginePostgres, name)
	if err != nil {
		return nil, err
	}
	role, err := QuoteIdentifier(EnginePostgres, owner)
	if err != nil {
		return nil, err
	}
	// CREATE DATABASE cannot run inside a transaction block, so it is issued
	// on its own before the connection moves to the new database to lock down
	// its public schema.
	create, err := a.psql(c, "postgres", []string{
		"CREATE DATABASE " + db + " OWNER " + role + ";",
	}, adminTimeout)
	if err != nil {
		return nil, err
	}
	lock, err := a.psql(c, name, []string{
		"REVOKE ALL ON DATABASE " + db + " FROM PUBLIC;",
		"REVOKE ALL ON SCHEMA public FROM PUBLIC;",
		"ALTER SCHEMA public OWNER TO " + role + ";",
	}, adminTimeout)
	if err != nil {
		return nil, err
	}
	return []command{create, lock}, nil
}

func (a postgresAdapter) dropDatabase(c engineCtx, name string) ([]command, error) {
	if err := ValidateIdentifier(EnginePostgres, "database name", name); err != nil {
		return nil, err
	}
	db, err := QuoteIdentifier(EnginePostgres, name)
	if err != nil {
		return nil, err
	}
	// Sessions are terminated first: PostgreSQL refuses to drop a database
	// that still has a connected backend.
	cmd, err := a.psql(c, "postgres", []string{
		"SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = " + sqlLiteral(name) + ";",
		"DROP DATABASE IF EXISTS " + db + ";",
	}, adminTimeout)
	if err != nil {
		return nil, err
	}
	return []command{cmd}, nil
}

func (a postgresAdapter) listDatabases(c engineCtx) ([]command, error) {
	argv := append(a.psqlArgv(c, "postgres"), "-t", "-A")
	body := "SELECT datname FROM pg_database WHERE datistemplate = false ORDER BY datname;\n"
	return []command{{Exec: execRequest(argv, a.env(c), strings.NewReader(body), adminTimeout)}}, nil
}

func (postgresAdapter) parseDatabaseList(res ExecResult) ([]string, error) {
	if res.ExitCode != 0 {
		return nil, ErrUnavailable("The instance did not answer a database listing.")
	}
	out := make([]string, 0)
	for _, line := range trimLines(res.Stdout) {
		if reservedIdentifiers[EnginePostgres][strings.ToLower(line)] {
			continue
		}
		out = append(out, line)
	}
	return out, nil
}

// createUser creates a login role with no privilege anywhere. Access is only
// ever added afterwards by grant, so a fresh account is useless until it is
// deliberately scoped to one database.
func (a postgresAdapter) createUser(c engineCtx, username, database, password string) ([]command, error) {
	if err := ValidateIdentifier(EnginePostgres, "username", username); err != nil {
		return nil, err
	}
	if err := assertSafeSecret(password); err != nil {
		return nil, err
	}
	role, err := QuoteIdentifier(EnginePostgres, username)
	if err != nil {
		return nil, err
	}
	cmd, err := a.psql(c, "postgres", []string{
		"CREATE ROLE " + role + " LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS PASSWORD " + sqlLiteral(password) + ";",
	}, adminTimeout)
	if err != nil {
		return nil, err
	}
	return []command{cmd}, nil
}

func (a postgresAdapter) setPassword(c engineCtx, username, database, password string) ([]command, error) {
	if err := ValidateIdentifier(EnginePostgres, "username", username); err != nil {
		return nil, err
	}
	if err := assertSafeSecret(password); err != nil {
		return nil, err
	}
	role, err := QuoteIdentifier(EnginePostgres, username)
	if err != nil {
		return nil, err
	}
	cmd, err := a.psql(c, "postgres", []string{
		"ALTER ROLE " + role + " WITH PASSWORD " + sqlLiteral(password) + ";",
	}, adminTimeout)
	if err != nil {
		return nil, err
	}
	return []command{cmd}, nil
}

func (a postgresAdapter) dropUser(c engineCtx, username, database string) ([]command, error) {
	if err := ValidateIdentifier(EnginePostgres, "username", username); err != nil {
		return nil, err
	}
	role, err := QuoteIdentifier(EnginePostgres, username)
	if err != nil {
		return nil, err
	}
	cmds := make([]command, 0, 2)
	// Objects owned inside the account's database are dropped first: a role
	// with dependent objects cannot be removed.
	if database != "" {
		if err := ValidateIdentifier(EnginePostgres, "database name", database); err != nil {
			return nil, err
		}
		owned, err := a.psql(c, database, []string{
			"REASSIGN OWNED BY " + role + " TO CURRENT_USER;",
			"DROP OWNED BY " + role + ";",
		}, adminTimeout)
		if err != nil {
			return nil, err
		}
		cmds = append(cmds, owned)
	}
	drop, err := a.psql(c, "postgres", []string{
		"DROP ROLE IF EXISTS " + role + ";",
	}, adminTimeout)
	if err != nil {
		return nil, err
	}
	return append(cmds, drop), nil
}

func (a postgresAdapter) grant(c engineCtx, username, database string, level GrantLevel) ([]command, error) {
	if err := ValidateIdentifier(EnginePostgres, "username", username); err != nil {
		return nil, err
	}
	if err := ValidateIdentifier(EnginePostgres, "database name", database); err != nil {
		return nil, err
	}
	if !level.Valid() {
		return nil, ErrValidation("That privilege level is not one this server issues.")
	}
	role, err := QuoteIdentifier(EnginePostgres, username)
	if err != nil {
		return nil, err
	}
	db, err := QuoteIdentifier(EnginePostgres, database)
	if err != nil {
		return nil, err
	}
	statements := []string{
		"GRANT CONNECT ON DATABASE " + db + " TO " + role + ";",
		"GRANT USAGE ON SCHEMA public TO " + role + ";",
	}
	switch level {
	case GrantReadOnly:
		statements = append(statements,
			"GRANT SELECT ON ALL TABLES IN SCHEMA public TO "+role+";",
			"GRANT SELECT ON ALL SEQUENCES IN SCHEMA public TO "+role+";",
			"ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT ON TABLES TO "+role+";",
			"ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT ON SEQUENCES TO "+role+";",
		)
	case GrantReadWrite:
		statements = append(statements,
			"GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO "+role+";",
			"GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO "+role+";",
			"ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO "+role+";",
			"ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT USAGE, SELECT ON SEQUENCES TO "+role+";",
		)
	case GrantOwner:
		statements = append(statements,
			"ALTER DATABASE "+db+" OWNER TO "+role+";",
			"ALTER SCHEMA public OWNER TO "+role+";",
			"GRANT ALL PRIVILEGES ON DATABASE "+db+" TO "+role+";",
			"GRANT ALL PRIVILEGES ON ALL TABLES IN SCHEMA public TO "+role+";",
			"GRANT ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA public TO "+role+";",
		)
	}
	cmd, err := a.psql(c, database, statements, adminTimeout)
	if err != nil {
		return nil, err
	}
	return []command{cmd}, nil
}

func (a postgresAdapter) revoke(c engineCtx, username, database string) ([]command, error) {
	if err := ValidateIdentifier(EnginePostgres, "username", username); err != nil {
		return nil, err
	}
	if err := ValidateIdentifier(EnginePostgres, "database name", database); err != nil {
		return nil, err
	}
	role, err := QuoteIdentifier(EnginePostgres, username)
	if err != nil {
		return nil, err
	}
	db, err := QuoteIdentifier(EnginePostgres, database)
	if err != nil {
		return nil, err
	}
	cmd, err := a.psql(c, database, []string{
		"ALTER DEFAULT PRIVILEGES IN SCHEMA public REVOKE ALL ON TABLES FROM " + role + ";",
		"ALTER DEFAULT PRIVILEGES IN SCHEMA public REVOKE ALL ON SEQUENCES FROM " + role + ";",
		"REVOKE ALL PRIVILEGES ON ALL TABLES IN SCHEMA public FROM " + role + ";",
		"REVOKE ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA public FROM " + role + ";",
		"REVOKE ALL PRIVILEGES ON SCHEMA public FROM " + role + ";",
		"REVOKE ALL PRIVILEGES ON DATABASE " + db + " FROM " + role + ";",
	}, adminTimeout)
	if err != nil {
		return nil, err
	}
	return []command{cmd}, nil
}

// dump produces a plain SQL script. Ownership and privileges are excluded so
// the same artifact restores into a fresh instance on a different major
// version, which is what makes the dump-and-restore upgrade path work.
func (a postgresAdapter) dump(c engineCtx, database string, out io.Writer) ([]command, error) {
	argv := []string{
		"pg_dumpall",
		"--host=" + c.Host,
		"--port=" + strconv.Itoa(c.Port),
		"--username=" + c.AdminUser,
		"--no-role-passwords",
		"--clean",
		"--if-exists",
	}
	if database != "" {
		if err := ValidateIdentifier(EnginePostgres, "database name", database); err != nil {
			return nil, err
		}
		argv = []string{
			"pg_dump",
			"--host=" + c.Host,
			"--port=" + strconv.Itoa(c.Port),
			"--username=" + c.AdminUser,
			"--dbname=" + database,
			"--format=plain",
			"--clean",
			"--if-exists",
			"--no-owner",
			"--no-privileges",
		}
	}
	cmd, err := streamCommand(argv, a.env(c), out, transferTimeout)
	if err != nil {
		return nil, err
	}
	return []command{cmd}, nil
}

func (a postgresAdapter) restore(c engineCtx, database string, in io.Reader) (restorePlan, error) {
	target := "postgres"
	if database != "" {
		if err := ValidateIdentifier(EnginePostgres, "database name", database); err != nil {
			return restorePlan{}, err
		}
		target = database
	}
	argv := append(a.psqlArgv(c, target), "--quiet")
	cmd, err := inputCommand(argv, a.env(c), in, transferTimeout)
	if err != nil {
		return restorePlan{}, err
	}
	return restorePlan{Online: &cmd}, nil
}

// replicaNeedsSeed is true because logical replication copies rows but not
// schema: each replicated database is created from a schema-only dump of the
// primary before its subscription is created.
func (postgresAdapter) replicaNeedsSeed() bool { return true }

func (a postgresAdapter) attachReplica(replica, primary engineCtx, databases []string) ([]replicaCommand, error) {
	if len(databases) == 0 {
		return nil, ErrValidation("A PostgreSQL replica needs at least one database to replicate.")
	}
	if err := assertSafeSecret(primary.AdminPassword); err != nil {
		return nil, err
	}
	pub, err := QuoteIdentifier(EnginePostgres, postgresPublication)
	if err != nil {
		return nil, err
	}
	out := make([]replicaCommand, 0, len(databases)*2)
	for _, database := range databases {
		if err := ValidateIdentifier(EnginePostgres, "database name", database); err != nil {
			return nil, err
		}
		sub, err := QuoteIdentifier(EnginePostgres, postgresSubscriptionPrefix+database)
		if err != nil {
			return nil, err
		}
		publish, err := a.psql(primary, database, []string{
			"DROP PUBLICATION IF EXISTS " + pub + ";",
			"CREATE PUBLICATION " + pub + " FOR ALL TABLES;",
		}, adminTimeout)
		if err != nil {
			return nil, err
		}
		conn := "host=" + primary.Instance.ContainerName +
			" port=" + strconv.Itoa(primary.Port) +
			" user=" + primary.AdminUser +
			" password=" + primary.AdminPassword +
			" dbname=" + database
		subscribe, err := a.psql(replica, database, []string{
			"DROP SUBSCRIPTION IF EXISTS " + sub + ";",
			"CREATE SUBSCRIPTION " + sub + " CONNECTION " + sqlLiteral(conn) +
				" PUBLICATION " + pub + " WITH (copy_data = true);",
		}, adminTimeout)
		if err != nil {
			return nil, err
		}
		out = append(out,
			replicaCommand{Target: targetPrimary, Cmd: publish},
			replicaCommand{Target: targetReplica, Cmd: subscribe},
		)
	}
	return out, nil
}

func (a postgresAdapter) detachReplica(replica, primary engineCtx, databases []string) ([]replicaCommand, error) {
	pub, err := QuoteIdentifier(EnginePostgres, postgresPublication)
	if err != nil {
		return nil, err
	}
	out := make([]replicaCommand, 0, len(databases)*2)
	for _, database := range databases {
		if err := ValidateIdentifier(EnginePostgres, "database name", database); err != nil {
			return nil, err
		}
		sub, err := QuoteIdentifier(EnginePostgres, postgresSubscriptionPrefix+database)
		if err != nil {
			return nil, err
		}
		// The subscription is disabled and its slot detached before it is
		// dropped, so a primary that is already gone cannot wedge the drop.
		unsubscribe, err := a.psql(replica, database, []string{
			"ALTER SUBSCRIPTION " + sub + " DISABLE;",
			"ALTER SUBSCRIPTION " + sub + " SET (slot_name = NONE);",
			"DROP SUBSCRIPTION IF EXISTS " + sub + ";",
		}, adminTimeout)
		if err != nil {
			return nil, err
		}
		unpublish, err := a.psql(primary, database, []string{
			"DROP PUBLICATION IF EXISTS " + pub + ";",
		}, adminTimeout)
		if err != nil {
			return nil, err
		}
		out = append(out,
			replicaCommand{Target: targetReplica, Cmd: unsubscribe},
			replicaCommand{Target: targetPrimary, Cmd: unpublish},
		)
	}
	return out, nil
}

// psqlArgv builds the client argument list. The database name is a validated
// identifier and the password is never present: it travels in the
// environment instead.
func (postgresAdapter) psqlArgv(c engineCtx, database string) []string {
	return []string{
		"psql",
		"--host=" + c.Host,
		"--port=" + strconv.Itoa(c.Port),
		"--username=" + c.AdminUser,
		"--dbname=" + database,
		"--no-password",
		"--set=ON_ERROR_STOP=1",
	}
}

// env carries the administrative password out of the argument list.
func (postgresAdapter) env(c engineCtx) map[string]string {
	return map[string]string{"PGPASSWORD": c.AdminPassword}
}

// psql builds a statement command against one database. The maintenance
// database is the only reserved name accepted here, because administrative
// work such as CREATE DATABASE has to be issued from somewhere.
func (a postgresAdapter) psql(c engineCtx, database string, statements []string, timeout time.Duration) (command, error) {
	if database != "postgres" {
		if err := ValidateIdentifier(EnginePostgres, "database name", database); err != nil {
			return command{}, err
		}
	}
	return statementCommand(a.psqlArgv(c, database), a.env(c), statements, timeout)
}

// sqlLiteral renders a value as a single-quoted SQL string literal, doubling
// any embedded quote. Only generated secrets and validated identifiers ever
// reach it, so the doubling is a second layer rather than the only one.
func sqlLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}
