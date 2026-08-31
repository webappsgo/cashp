package dbservice

import (
	"io"
	"strconv"
	"strings"
	"time"
)

// valkeyAdapter manages Valkey instances through valkey-cli. Commands are
// piped on standard input rather than passed as arguments, and the
// administrative password is delivered through the client's own environment
// variables, so no credential ever reaches an argument list.
//
// Valkey has no named databases and no per-database privileges. Both are
// reported through the capability matrix and any attempt to use them returns
// the typed unsupported error rather than quietly doing nothing.
type valkeyAdapter struct{}

// valkeyVersions are the supported versions, oldest first.
var valkeyVersions = []string{"7.2", "8.0", "8.1"}

// valkeyConfigFile is the engine configuration written before first start.
// It lives on the data volume so CONFIG REWRITE can persist changes.
const valkeyConfigFile = "/data/valkey.conf"

// valkeyACLFile holds the account definitions. Valkey only persists accounts
// created with ACL SETUSER when an ACL file is configured, so every managed
// instance has one from the first boot.
const valkeyACLFile = "/data/users.acl"

// valkeySnapshotFile is the RDB snapshot the engine loads at startup and the
// file an offline restore replaces.
const valkeySnapshotFile = "/data/dump.rdb"

// valkeyUID and valkeyGID are the account the engine image runs as.
const (
	valkeyUID = 999
	valkeyGID = 999
)

func (valkeyAdapter) engine() Engine { return EngineValkey }

func (valkeyAdapter) versions() []string { return valkeyVersions }

func (valkeyAdapter) image(version string) string {
	return "docker.io/valkey/valkey:" + version + "-alpine"
}

func (valkeyAdapter) defaultPort() int { return 6379 }

// scheme is the redis URI scheme, which every Valkey client understands.
func (valkeyAdapter) scheme() string { return "redis" }

func (valkeyAdapter) dataPath() string { return "/data" }

// adminUser is the built-in default account, the only one that exists before
// any ACL is defined.
func (valkeyAdapter) adminUser() string { return "default" }

func (valkeyAdapter) capabilities() Capabilities {
	return Capabilities{
		NamedDatabases:  false,
		Users:           true,
		Grants:          true,
		Replicas:        true,
		InPlaceUpgrade:  true,
		PerDatabaseDump: false,
		OnlineRestore:   false,
	}
}

// upgradeStrategy is in place: Valkey reads the snapshot written by an older
// version without conversion.
func (valkeyAdapter) upgradeStrategy() UpgradeStrategy { return StrategyInPlace }

func (a valkeyAdapter) containerSpec(p specParams) ContainerSpec {
	spec := baseContainerSpec(p, a, a.image(p.Instance.EngineVersion))
	spec.Cmd = []string{"valkey-server", valkeyConfigFile}
	return spec
}

// bootstrapFiles stages the engine configuration and the account file. Valkey
// has no environment variable for its password, so the credential is written
// to an owner-only file on the data volume before the engine first starts.
func (a valkeyAdapter) bootstrapFiles(p specParams) []fileDrop {
	config := strings.Join([]string{
		"bind * -::*",
		"protected-mode yes",
		"dir " + a.dataPath(),
		"aclfile " + valkeyACLFile,
		"appendonly no",
		"save 900 1",
		"save 300 10",
		"save 60 10000",
		"dbfilename " + strings.TrimPrefix(valkeySnapshotFile, a.dataPath()+"/"),
	}, "\n") + "\n"
	acl := "user " + p.AdminUser + " on >" + p.AdminPassword + " ~* &* +@all\n"
	return []fileDrop{
		{Path: valkeyConfigFile, Mode: 0o600, UID: valkeyUID, GID: valkeyGID, Content: []byte(config)},
		{Path: valkeyACLFile, Mode: 0o600, UID: valkeyUID, GID: valkeyGID, Content: []byte(acl)},
	}
}

// postStartCommands is empty: the configuration written before first start
// already leaves the instance in its final shape.
func (valkeyAdapter) postStartCommands(c engineCtx) ([]command, error) { return nil, nil }

// healthCommand asks the engine for its replication state, which proves the
// connection authenticated and the engine answered at protocol level.
func (a valkeyAdapter) healthCommand(c engineCtx) command {
	argv := append(a.cliArgv(c), "INFO", "replication")
	return command{Exec: execRequest(argv, a.env(c), nil, healthTimeout)}
}

func (valkeyAdapter) parseHealth(res ExecResult) (HealthState, string) {
	if res.ExitCode != 0 {
		return HealthUnhealthy, "The engine did not answer its health probe."
	}
	var role, link string
	for _, line := range trimLines(res.Stdout) {
		switch {
		case strings.HasPrefix(line, "role:"):
			role = strings.TrimPrefix(line, "role:")
		case strings.HasPrefix(line, "master_link_status:"):
			link = strings.TrimPrefix(line, "master_link_status:")
		}
	}
	switch role {
	case "master":
		return HealthHealthy, "Accepting reads and writes."
	case "slave":
		if link == "up" {
			return HealthHealthy, "Serving reads as a replica."
		}
		return HealthDegraded, "The replica is not currently connected to its primary."
	default:
		return HealthDegraded, "The engine answered but did not report a replication role."
	}
}

// createDatabase is unsupported: Valkey exposes a fixed set of numbered
// keyspaces rather than named databases a tenant can create.
func (valkeyAdapter) createDatabase(c engineCtx, name, owner string) ([]command, error) {
	return nil, ErrUnsupported(EngineValkey, "named databases")
}

// dropDatabase is unsupported for the same reason as createDatabase.
func (valkeyAdapter) dropDatabase(c engineCtx, name string) ([]command, error) {
	return nil, ErrUnsupported(EngineValkey, "named databases")
}

// listDatabases is unsupported for the same reason as createDatabase.
func (valkeyAdapter) listDatabases(c engineCtx) ([]command, error) {
	return nil, ErrUnsupported(EngineValkey, "named databases")
}

// parseDatabaseList is unsupported for the same reason as createDatabase.
func (valkeyAdapter) parseDatabaseList(res ExecResult) ([]string, error) {
	return nil, ErrUnsupported(EngineValkey, "named databases")
}

// createUser creates a disabled-in-all-but-login account: it may authenticate
// and can run no command and touch no key until grant widens it.
func (a valkeyAdapter) createUser(c engineCtx, username, database, password string) ([]command, error) {
	if err := a.checkNoDatabase(database); err != nil {
		return nil, err
	}
	user, err := a.account(username)
	if err != nil {
		return nil, err
	}
	if err := assertSafeSecret(password); err != nil {
		return nil, err
	}
	cmd, err := a.cli(c, []string{
		"ACL SETUSER " + user + " on >" + password + " resetkeys resetchannels nocommands",
		"ACL SAVE",
	}, adminTimeout)
	if err != nil {
		return nil, err
	}
	return []command{cmd}, nil
}

func (a valkeyAdapter) setPassword(c engineCtx, username, database, password string) ([]command, error) {
	if err := a.checkNoDatabase(database); err != nil {
		return nil, err
	}
	user, err := a.account(username)
	if err != nil {
		return nil, err
	}
	if err := assertSafeSecret(password); err != nil {
		return nil, err
	}
	cmd, err := a.cli(c, []string{
		"ACL SETUSER " + user + " resetpass >" + password,
		"ACL SAVE",
	}, adminTimeout)
	if err != nil {
		return nil, err
	}
	return []command{cmd}, nil
}

func (a valkeyAdapter) dropUser(c engineCtx, username, database string) ([]command, error) {
	if err := a.checkNoDatabase(database); err != nil {
		return nil, err
	}
	user, err := a.account(username)
	if err != nil {
		return nil, err
	}
	cmd, err := a.cli(c, []string{
		"ACL DELUSER " + user,
		"ACL SAVE",
	}, adminTimeout)
	if err != nil {
		return nil, err
	}
	return []command{cmd}, nil
}

// grant widens an account's ACL. A per-database grant is refused: Valkey
// privileges apply to key patterns across the whole instance, so pretending
// to scope one to a database would be a silent lie.
func (a valkeyAdapter) grant(c engineCtx, username, database string, level GrantLevel) ([]command, error) {
	if err := a.checkNoDatabase(database); err != nil {
		return nil, err
	}
	user, err := a.account(username)
	if err != nil {
		return nil, err
	}
	if !level.Valid() {
		return nil, ErrValidation("That privilege level is not one this server issues.")
	}
	var rules string
	switch level {
	case GrantReadOnly:
		rules = "resetkeys resetchannels nocommands ~* +@read +@connection"
	case GrantReadWrite:
		rules = "resetkeys resetchannels nocommands ~* &* +@read +@write +@keyspace +@string +@list +@set +@sortedset +@hash +@stream +@pubsub +@transaction +@connection"
	case GrantOwner:
		rules = "resetkeys resetchannels nocommands ~* &* +@all -@admin -@dangerous"
	}
	cmd, err := a.cli(c, []string{
		"ACL SETUSER " + user + " on " + rules,
		"ACL SAVE",
	}, adminTimeout)
	if err != nil {
		return nil, err
	}
	return []command{cmd}, nil
}

func (a valkeyAdapter) revoke(c engineCtx, username, database string) ([]command, error) {
	if err := a.checkNoDatabase(database); err != nil {
		return nil, err
	}
	user, err := a.account(username)
	if err != nil {
		return nil, err
	}
	cmd, err := a.cli(c, []string{
		"ACL SETUSER " + user + " off resetkeys resetchannels nocommands",
		"ACL SAVE",
	}, adminTimeout)
	if err != nil {
		return nil, err
	}
	return []command{cmd}, nil
}

// dump streams a point-in-time snapshot on standard output. A per-database
// dump is unsupported because a Valkey snapshot covers the whole instance.
func (a valkeyAdapter) dump(c engineCtx, database string, out io.Writer) ([]command, error) {
	if database != "" {
		return nil, ErrUnsupported(EngineValkey, "backing up a single database")
	}
	argv := append(a.cliArgv(c), "--rdb", "-")
	cmd, err := streamCommand(argv, a.env(c), out, transferTimeout)
	if err != nil {
		return nil, err
	}
	return []command{cmd}, nil
}

// restore is offline: the engine loads its snapshot only at startup, so the
// snapshot file is replaced while the instance is stopped and the engine is
// started again afterwards.
func (a valkeyAdapter) restore(c engineCtx, database string, in io.Reader) (restorePlan, error) {
	if database != "" {
		return restorePlan{}, ErrUnsupported(EngineValkey, "restoring a single database")
	}
	return restorePlan{Offline: &offlineRestore{
		Path: valkeySnapshotFile,
		Mode: 0o600,
		UID:  valkeyUID,
		GID:  valkeyGID,
	}}, nil
}

// replicaNeedsSeed is false: a Valkey replica performs a full synchronisation
// from its primary as soon as it is pointed at one.
func (valkeyAdapter) replicaNeedsSeed() bool { return false }

func (a valkeyAdapter) attachReplica(replica, primary engineCtx, databases []string) ([]replicaCommand, error) {
	if err := assertSafeSecret(primary.AdminPassword); err != nil {
		return nil, err
	}
	if !instanceNamePattern.MatchString(primary.Instance.ContainerName) {
		return nil, ErrInternal(nil, "That replica could not be attached.")
	}
	cmd, err := a.cli(replica, []string{
		"CONFIG SET masteruser " + primary.AdminUser,
		"CONFIG SET masterauth " + primary.AdminPassword,
		"REPLICAOF " + primary.Instance.ContainerName + " " + strconv.Itoa(primary.Port),
		"CONFIG SET replica-read-only yes",
		"CONFIG REWRITE",
	}, adminTimeout)
	if err != nil {
		return nil, err
	}
	return []replicaCommand{{Target: targetReplica, Cmd: cmd}}, nil
}

// detachReplica promotes the replica back to a standalone instance and clears
// the primary's credential from its configuration.
func (a valkeyAdapter) detachReplica(replica, primary engineCtx, databases []string) ([]replicaCommand, error) {
	cmd, err := a.cli(replica, []string{
		"REPLICAOF NO ONE",
		"CONFIG SET masterauth \"\"",
		"CONFIG SET masteruser \"\"",
		"CONFIG REWRITE",
	}, adminTimeout)
	if err != nil {
		return nil, err
	}
	return []replicaCommand{{Target: targetReplica, Cmd: cmd}}, nil
}

// checkNoDatabase refuses any account operation that names a database.
func (valkeyAdapter) checkNoDatabase(database string) error {
	if database != "" {
		return ErrUnsupported(EngineValkey, "per-database privileges")
	}
	return nil
}

// account validates a username and renders it for an ACL command. Valkey
// takes an account name as a bare word, and the allowlist already excludes
// every character that could split it into two arguments.
func (valkeyAdapter) account(username string) (string, error) {
	if err := ValidateIdentifier(EngineValkey, "username", username); err != nil {
		return "", err
	}
	return QuoteIdentifier(EngineValkey, username)
}

// cliArgv builds the client argument list without any credential.
func (valkeyAdapter) cliArgv(c engineCtx) []string {
	return []string{
		"valkey-cli",
		"-h", c.Host,
		"-p", strconv.Itoa(c.Port),
		"--user", c.AdminUser,
		"--no-auth-warning",
	}
}

// env carries the administrative password out of the argument list. Both
// variable names are set: Valkey reads its own and keeps the Redis one for
// compatibility, and which is honoured depends on the client build.
func (valkeyAdapter) env(c engineCtx) map[string]string {
	return map[string]string{
		"VALKEYCLI_AUTH": c.AdminPassword,
		"REDISCLI_AUTH":  c.AdminPassword,
	}
}

// cli builds a command that pipes one command per line into valkey-cli.
func (a valkeyAdapter) cli(c engineCtx, commands []string, timeout time.Duration) (command, error) {
	return statementCommand(a.cliArgv(c), a.env(c), commands, timeout)
}
