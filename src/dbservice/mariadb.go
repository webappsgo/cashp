package dbservice

import (
	"hash/fnv"
	"io"
	"strconv"
	"strings"
	"time"
)

// mariadbAdapter manages MariaDB instances through the client binaries in the
// engine image: mariadb for statements and mariadb-dump for backups.
// Statement text is delivered on standard input and the password through
// MYSQL_PWD, so neither reaches an argument list.
type mariadbAdapter struct{}

// mariadbVersions are the supported versions, oldest first. Only long-term
// support releases are offered.
var mariadbVersions = []string{"10.6", "10.11", "11.4"}

// mariadbHost is the host part of every account this package creates. Managed
// instances are reachable only on their own tenant network, so a wildcard
// host does not widen exposure and lets an application container connect from
// whatever address the orchestrator gives it.
const mariadbHost = "%"

// mariadbHealthToken is the literal a health probe selects.
const mariadbHealthToken = "cashp_ok"

func (mariadbAdapter) engine() Engine { return EngineMariaDB }

func (mariadbAdapter) versions() []string { return mariadbVersions }

func (mariadbAdapter) image(version string) string {
	return "docker.io/library/mariadb:" + version
}

func (mariadbAdapter) defaultPort() int { return 3306 }

func (mariadbAdapter) scheme() string { return "mysql" }

func (mariadbAdapter) dataPath() string { return "/var/lib/mysql" }

// adminUser is root because MariaDB's replication and privilege commands are
// only available to it, and the image bootstraps exactly this one account.
func (mariadbAdapter) adminUser() string { return "root" }

func (mariadbAdapter) capabilities() Capabilities {
	return Capabilities{
		NamedDatabases:  true,
		Users:           true,
		Grants:          true,
		Replicas:        true,
		InPlaceUpgrade:  true,
		PerDatabaseDump: true,
		OnlineRestore:   true,
	}
}

// upgradeStrategy is in place: MariaDB reads an older data directory and
// migrates its own system tables on first start.
func (mariadbAdapter) upgradeStrategy() UpgradeStrategy { return StrategyInPlace }

func (a mariadbAdapter) containerSpec(p specParams) ContainerSpec {
	spec := baseContainerSpec(p, a, a.image(p.Instance.EngineVersion))
	spec.Env = map[string]string{
		"MARIADB_ROOT_PASSWORD": p.AdminPassword,
		"MARIADB_ROOT_HOST":     mariadbHost,
	}
	// The binary log and a stable unique server id are configured from the
	// first boot so a replica can be attached later without restarting the
	// primary.
	spec.Cmd = []string{
		"--server-id=" + strconv.FormatUint(uint64(mariadbServerID(p.Instance.ID)), 10),
		"--log-bin=cashp-bin",
		"--binlog-format=ROW",
		"--log-slave-updates=ON",
		"--skip-name-resolve",
		"--character-set-server=utf8mb4",
		"--collation-server=utf8mb4_unicode_ci",
	}
	if p.Instance.Role == RoleReplica {
		// A replica refuses writes from anyone but the replication thread, so
		// an application pointed at it cannot silently diverge from the
		// primary.
		spec.Cmd = append(spec.Cmd, "--read-only=ON")
	}
	return spec
}

// bootstrapFiles is empty: MariaDB takes its bootstrap password from the
// container environment.
func (mariadbAdapter) bootstrapFiles(p specParams) []fileDrop { return nil }

// postStartCommands removes the anonymous and test artefacts some builds still
// create, so a fresh instance grants nothing to an unauthenticated client.
func (a mariadbAdapter) postStartCommands(c engineCtx) ([]command, error) {
	cmd, err := a.client(c, "", []string{
		"DELETE FROM mysql.global_priv WHERE User = '';",
		"DROP DATABASE IF EXISTS test;",
		"FLUSH PRIVILEGES;",
	}, adminTimeout)
	if err != nil {
		return nil, err
	}
	return []command{cmd}, nil
}

func (a mariadbAdapter) healthCommand(c engineCtx) command {
	argv := append(a.clientArgv(c, ""), "--skip-column-names", "--batch")
	body := "SELECT '" + mariadbHealthToken + "', @@global.read_only;\n"
	return command{Exec: execRequest(argv, a.env(c), strings.NewReader(body), healthTimeout)}
}

func (mariadbAdapter) parseHealth(res ExecResult) (HealthState, string) {
	if res.ExitCode != 0 {
		return HealthUnhealthy, "The engine did not answer its health probe."
	}
	for _, line := range trimLines(res.Stdout) {
		if !strings.HasPrefix(line, mariadbHealthToken) {
			continue
		}
		if strings.HasSuffix(line, "1") {
			return HealthHealthy, "Serving reads only."
		}
		return HealthHealthy, "Accepting reads and writes."
	}
	return HealthDegraded, "The engine answered but did not complete the health statement."
}

func (a mariadbAdapter) createDatabase(c engineCtx, name, owner string) ([]command, error) {
	if err := ValidateIdentifier(EngineMariaDB, "database name", name); err != nil {
		return nil, err
	}
	db, err := QuoteIdentifier(EngineMariaDB, name)
	if err != nil {
		return nil, err
	}
	statements := []string{
		"CREATE DATABASE IF NOT EXISTS " + db + " CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;",
	}
	if owner != "" {
		if err := ValidateIdentifier(EngineMariaDB, "username", owner); err != nil {
			return nil, err
		}
		statements = append(statements,
			"GRANT ALL PRIVILEGES ON "+db+".* TO "+mariadbAccount(owner)+";",
			"FLUSH PRIVILEGES;",
		)
	}
	cmd, err := a.client(c, "", statements, adminTimeout)
	if err != nil {
		return nil, err
	}
	return []command{cmd}, nil
}

func (a mariadbAdapter) dropDatabase(c engineCtx, name string) ([]command, error) {
	if err := ValidateIdentifier(EngineMariaDB, "database name", name); err != nil {
		return nil, err
	}
	db, err := QuoteIdentifier(EngineMariaDB, name)
	if err != nil {
		return nil, err
	}
	cmd, err := a.client(c, "", []string{"DROP DATABASE IF EXISTS " + db + ";"}, adminTimeout)
	if err != nil {
		return nil, err
	}
	return []command{cmd}, nil
}

func (a mariadbAdapter) listDatabases(c engineCtx) ([]command, error) {
	argv := append(a.clientArgv(c, ""), "--skip-column-names", "--batch")
	body := "SELECT SCHEMA_NAME FROM information_schema.SCHEMATA ORDER BY SCHEMA_NAME;\n"
	return []command{{Exec: execRequest(argv, a.env(c), strings.NewReader(body), adminTimeout)}}, nil
}

func (mariadbAdapter) parseDatabaseList(res ExecResult) ([]string, error) {
	if res.ExitCode != 0 {
		return nil, ErrUnavailable("The instance did not answer a database listing.")
	}
	out := make([]string, 0)
	for _, line := range trimLines(res.Stdout) {
		if reservedIdentifiers[EngineMariaDB][strings.ToLower(line)] {
			continue
		}
		out = append(out, line)
	}
	return out, nil
}

// createUser creates an account with no privilege on any database. Access is
// added only by grant, so the account starts useless.
func (a mariadbAdapter) createUser(c engineCtx, username, database, password string) ([]command, error) {
	if err := ValidateIdentifier(EngineMariaDB, "username", username); err != nil {
		return nil, err
	}
	if err := assertSafeSecret(password); err != nil {
		return nil, err
	}
	cmd, err := a.client(c, "", []string{
		"CREATE USER IF NOT EXISTS " + mariadbAccount(username) + " IDENTIFIED BY " + sqlLiteral(password) + ";",
		"FLUSH PRIVILEGES;",
	}, adminTimeout)
	if err != nil {
		return nil, err
	}
	return []command{cmd}, nil
}

func (a mariadbAdapter) setPassword(c engineCtx, username, database, password string) ([]command, error) {
	if err := ValidateIdentifier(EngineMariaDB, "username", username); err != nil {
		return nil, err
	}
	if err := assertSafeSecret(password); err != nil {
		return nil, err
	}
	cmd, err := a.client(c, "", []string{
		"ALTER USER " + mariadbAccount(username) + " IDENTIFIED BY " + sqlLiteral(password) + ";",
		"FLUSH PRIVILEGES;",
	}, adminTimeout)
	if err != nil {
		return nil, err
	}
	return []command{cmd}, nil
}

func (a mariadbAdapter) dropUser(c engineCtx, username, database string) ([]command, error) {
	if err := ValidateIdentifier(EngineMariaDB, "username", username); err != nil {
		return nil, err
	}
	cmd, err := a.client(c, "", []string{
		"DROP USER IF EXISTS " + mariadbAccount(username) + ";",
		"FLUSH PRIVILEGES;",
	}, adminTimeout)
	if err != nil {
		return nil, err
	}
	return []command{cmd}, nil
}

func (a mariadbAdapter) grant(c engineCtx, username, database string, level GrantLevel) ([]command, error) {
	if err := ValidateIdentifier(EngineMariaDB, "username", username); err != nil {
		return nil, err
	}
	if err := ValidateIdentifier(EngineMariaDB, "database name", database); err != nil {
		return nil, err
	}
	if !level.Valid() {
		return nil, ErrValidation("That privilege level is not one this server issues.")
	}
	db, err := QuoteIdentifier(EngineMariaDB, database)
	if err != nil {
		return nil, err
	}
	account := mariadbAccount(username)
	var privileges string
	switch level {
	case GrantReadOnly:
		privileges = "SELECT, SHOW VIEW"
	case GrantReadWrite:
		privileges = "SELECT, INSERT, UPDATE, DELETE, SHOW VIEW, EXECUTE"
	case GrantOwner:
		privileges = "ALL PRIVILEGES"
	}
	cmd, err := a.client(c, "", []string{
		"GRANT " + privileges + " ON " + db + ".* TO " + account + ";",
		"FLUSH PRIVILEGES;",
	}, adminTimeout)
	if err != nil {
		return nil, err
	}
	return []command{cmd}, nil
}

func (a mariadbAdapter) revoke(c engineCtx, username, database string) ([]command, error) {
	if err := ValidateIdentifier(EngineMariaDB, "username", username); err != nil {
		return nil, err
	}
	if err := ValidateIdentifier(EngineMariaDB, "database name", database); err != nil {
		return nil, err
	}
	db, err := QuoteIdentifier(EngineMariaDB, database)
	if err != nil {
		return nil, err
	}
	cmd, err := a.client(c, "", []string{
		"REVOKE ALL PRIVILEGES, GRANT OPTION ON " + db + ".* FROM " + mariadbAccount(username) + ";",
		"FLUSH PRIVILEGES;",
	}, adminTimeout)
	if err != nil {
		return nil, err
	}
	return []command{cmd}, nil
}

// dump produces a SQL script. A whole-instance dump records the binary log
// position and GTID state as a comment, which is what lets a freshly seeded
// replica start following the primary from the right point.
func (a mariadbAdapter) dump(c engineCtx, database string, out io.Writer) ([]command, error) {
	argv := []string{
		"mariadb-dump",
		"--host=" + c.Host,
		"--port=" + strconv.Itoa(c.Port),
		"--user=" + c.AdminUser,
		"--protocol=TCP",
		"--single-transaction",
		"--routines",
		"--triggers",
		"--events",
		"--default-character-set=utf8mb4",
	}
	if database == "" {
		argv = append(argv, "--all-databases", "--master-data=2", "--gtid")
	} else {
		if err := ValidateIdentifier(EngineMariaDB, "database name", database); err != nil {
			return nil, err
		}
		argv = append(argv, "--databases", database)
	}
	cmd, err := streamCommand(argv, a.env(c), out, transferTimeout)
	if err != nil {
		return nil, err
	}
	return []command{cmd}, nil
}

func (a mariadbAdapter) restore(c engineCtx, database string, in io.Reader) (restorePlan, error) {
	if database != "" {
		if err := ValidateIdentifier(EngineMariaDB, "database name", database); err != nil {
			return restorePlan{}, err
		}
	}
	cmd, err := inputCommand(a.clientArgv(c, database), a.env(c), in, transferTimeout)
	if err != nil {
		return restorePlan{}, err
	}
	return restorePlan{Online: &cmd}, nil
}

// replicaNeedsSeed is true because MariaDB replication starts from a
// consistent snapshot: the replica is loaded from a whole-instance dump of
// the primary before the replication threads are started.
func (mariadbAdapter) replicaNeedsSeed() bool { return true }

// attachReplica points the replica at its primary using GTID positions
// recorded by the seed dump. MariaDB replicates the whole server, so the
// database list is not part of the plan.
func (a mariadbAdapter) attachReplica(replica, primary engineCtx, databases []string) ([]replicaCommand, error) {
	if err := assertSafeSecret(primary.AdminPassword); err != nil {
		return nil, err
	}
	if !instanceNamePattern.MatchString(primary.Instance.ContainerName) {
		return nil, ErrInternal(nil, "That replica could not be attached.")
	}
	cmd, err := a.client(replica, "", []string{
		"STOP SLAVE;",
		"CHANGE MASTER TO MASTER_HOST=" + sqlLiteral(primary.Instance.ContainerName) +
			", MASTER_PORT=" + strconv.Itoa(primary.Port) +
			", MASTER_USER=" + sqlLiteral(primary.AdminUser) +
			", MASTER_PASSWORD=" + sqlLiteral(primary.AdminPassword) +
			", MASTER_USE_GTID=slave_pos;",
		"START SLAVE;",
	}, adminTimeout)
	if err != nil {
		return nil, err
	}
	return []replicaCommand{{Target: targetReplica, Cmd: cmd}}, nil
}

// detachReplica stops replication and clears the stored connection so the
// primary's credentials do not linger in the replica's system tables.
func (a mariadbAdapter) detachReplica(replica, primary engineCtx, databases []string) ([]replicaCommand, error) {
	cmd, err := a.client(replica, "", []string{
		"STOP SLAVE;",
		"RESET SLAVE ALL;",
		"SET GLOBAL read_only = OFF;",
	}, adminTimeout)
	if err != nil {
		return nil, err
	}
	return []replicaCommand{{Target: targetReplica, Cmd: cmd}}, nil
}

// clientArgv builds the client argument list. An empty database connects
// without selecting one, which is what administrative statements need.
func (mariadbAdapter) clientArgv(c engineCtx, database string) []string {
	argv := []string{
		"mariadb",
		"--host=" + c.Host,
		"--port=" + strconv.Itoa(c.Port),
		"--user=" + c.AdminUser,
		"--protocol=TCP",
		"--default-character-set=utf8mb4",
	}
	if database != "" {
		argv = append(argv, "--database="+database)
	}
	return argv
}

// env carries the administrative password out of the argument list. MYSQL_PWD
// is read by every client binary in the MariaDB image.
func (mariadbAdapter) env(c engineCtx) map[string]string {
	return map[string]string{"MYSQL_PWD": c.AdminPassword}
}

// client builds a statement command.
func (a mariadbAdapter) client(c engineCtx, database string, statements []string, timeout time.Duration) (command, error) {
	if database != "" {
		if err := ValidateIdentifier(EngineMariaDB, "database name", database); err != nil {
			return command{}, err
		}
	}
	return statementCommand(a.clientArgv(c, database), a.env(c), statements, timeout)
}

// mariadbAccount renders a validated username as a MariaDB account
// specification. MariaDB takes the user and host parts as string literals
// rather than identifiers, so both are quoted as literals.
func mariadbAccount(username string) string {
	return sqlLiteral(username) + "@" + sqlLiteral(mariadbHost)
}

// mariadbServerID derives a stable, non-zero server id from an instance
// identifier. Replication requires every member of a topology to carry a
// distinct id, and deriving it from the identifier keeps it stable across
// restarts and upgrades without storing another column.
func mariadbServerID(instanceID string) uint32 {
	h := fnv.New32a()
	// Write never fails for a hash, so the error return is not actionable.
	_, _ = h.Write([]byte(instanceID))
	id := h.Sum32() & 0x7fffffff
	if id == 0 {
		id = 1
	}
	return id
}
