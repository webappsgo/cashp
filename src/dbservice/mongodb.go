package dbservice

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"strconv"
	"strings"
	"time"
)

// mongodbAdapter manages MongoDB instances through the tools shipped in the
// engine image: mongosh for administration and mongodump/mongorestore for
// backups. mongosh is started with --nodb and receives its connection string
// from the exec environment, and the dump tools read their password from a
// short-lived owner-only config file, so a credential never appears in an
// argument list.
type mongodbAdapter struct{}

// mongodbVersions are the supported versions, oldest first.
var mongodbVersions = []string{"6.0", "7.0", "8.0"}

// mongodbReplicaSet is the replica set name every managed instance starts
// with. Running as a single-member set from the first boot means a replica
// can be added later without restarting the primary.
const mongodbReplicaSet = "rs0"

// mongodbKeyFile is where the shared replica set authentication key is
// staged inside the container.
const mongodbKeyFile = "/etc/cashp/mongo.key"

// mongodbUID and mongodbGID are the account the engine image runs as. The
// key file is refused by mongod unless it is owned by that account and
// readable by nobody else.
const (
	mongodbUID = 999
	mongodbGID = 999
)

// mongodbURIEnv is the environment variable the administration script reads
// its connection string from.
const mongodbURIEnv = "CASHP_MONGO_URI"

// mongodbHealthToken is the literal a health probe prints.
const mongodbHealthToken = "cashp_ok"

// mongodbDumpConfig and mongodbRestoreConfig are the owner-only files the
// dump tools read their password from. They are removed as soon as the
// command that needs them has finished.
const (
	mongodbDumpConfig    = "/tmp/cashp-mongodump.conf"
	mongodbRestoreConfig = "/tmp/cashp-mongorestore.conf"
)

func (mongodbAdapter) engine() Engine { return EngineMongoDB }

func (mongodbAdapter) versions() []string { return mongodbVersions }

func (mongodbAdapter) image(version string) string {
	return "docker.io/library/mongo:" + version
}

func (mongodbAdapter) defaultPort() int { return 27017 }

func (mongodbAdapter) scheme() string { return "mongodb" }

func (mongodbAdapter) dataPath() string { return "/data/db" }

// adminUser is the root account the image bootstraps from the environment.
func (mongodbAdapter) adminUser() string { return "cashp_admin" }

func (mongodbAdapter) capabilities() Capabilities {
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

// upgradeStrategy is in place: MongoDB migrates its own data files when it
// starts on a newer version of an adjacent release.
func (mongodbAdapter) upgradeStrategy() UpgradeStrategy { return StrategyInPlace }

func (a mongodbAdapter) containerSpec(p specParams) ContainerSpec {
	spec := baseContainerSpec(p, a, a.image(p.Instance.EngineVersion))
	spec.Env = map[string]string{
		"MONGO_INITDB_ROOT_USERNAME": p.AdminUser,
		"MONGO_INITDB_ROOT_PASSWORD": p.AdminPassword,
		"MONGO_INITDB_DATABASE":      "admin",
	}
	spec.Cmd = []string{
		"mongod",
		"--bind_ip_all",
		"--replSet", mongodbReplicaSet,
		"--keyFile", mongodbKeyFile,
	}
	return spec
}

// bootstrapFiles stages the replica set key before the engine's first start.
// The key is derived from the primary's administrative password and instance
// identifier, so a replica computes the same value without the key ever being
// stored anywhere or travelling between nodes.
func (a mongodbAdapter) bootstrapFiles(p specParams) []fileDrop {
	password, instanceID := p.AdminPassword, p.Instance.ID
	if p.ReplicaOf != nil {
		password, instanceID = p.ReplicaOf.AdminPassword, p.ReplicaOf.Instance.ID
	}
	return []fileDrop{{
		Path:    mongodbKeyFile,
		Mode:    0o400,
		UID:     mongodbUID,
		GID:     mongodbGID,
		Content: []byte(mongodbKeyMaterial(password, instanceID)),
	}}
}

// postStartCommands initiates the single-member replica set on a primary. A
// replica is added to its primary's set by attachReplica instead, so it needs
// nothing here.
func (a mongodbAdapter) postStartCommands(c engineCtx) ([]command, error) {
	if c.Instance.Role != RolePrimary {
		return nil, nil
	}
	host, err := mongodbMember(c)
	if err != nil {
		return nil, err
	}
	// An already-initiated set reports AlreadyInitialized; treating that as
	// success keeps the whole start path idempotent.
	cmd, err := a.script(c, []string{
		"try {",
		"  rs.status();",
		"} catch (e) {",
		"  rs.initiate({_id: " + mongodbSetName() + ", members: [{_id: 0, host: " + host + "}]});",
		"}",
	}, adminTimeout)
	if err != nil {
		return nil, err
	}
	return []command{cmd}, nil
}

func (a mongodbAdapter) healthCommand(c engineCtx) command {
	cmd, err := a.script(c, []string{
		"const reply = db.getSiblingDB('admin').runCommand({hello: 1});",
		"print('" + mongodbHealthToken + " ' + (reply.isWritablePrimary ? 'primary' : (reply.secondary ? 'secondary' : 'unelected')));",
	}, healthTimeout)
	if err != nil {
		// script only fails on an unsafe credential, which the caller reports
		// through the probe result rather than a panic.
		return command{Exec: ExecRequest{Argv: []string{"false"}, Timeout: healthTimeout}}
	}
	return cmd
}

func (mongodbAdapter) parseHealth(res ExecResult) (HealthState, string) {
	if res.ExitCode != 0 {
		return HealthUnhealthy, "The engine did not answer its health probe."
	}
	for _, line := range trimLines(res.Stdout) {
		if !strings.HasPrefix(line, mongodbHealthToken) {
			continue
		}
		switch {
		case strings.HasSuffix(line, "primary"):
			return HealthHealthy, "Accepting reads and writes."
		case strings.HasSuffix(line, "secondary"):
			return HealthHealthy, "Serving reads as a replica."
		default:
			return HealthDegraded, "The engine is running but has no elected primary."
		}
	}
	return HealthDegraded, "The engine answered but did not complete the health statement."
}

// createDatabase materialises a database. MongoDB creates a database lazily on
// first write, so a marker collection is created to make the database real and
// therefore visible, backupable and grantable straight away.
func (a mongodbAdapter) createDatabase(c engineCtx, name, owner string) ([]command, error) {
	if err := ValidateIdentifier(EngineMongoDB, "database name", name); err != nil {
		return nil, err
	}
	db, err := QuoteIdentifier(EngineMongoDB, name)
	if err != nil {
		return nil, err
	}
	lines := []string{
		"const target = db.getSiblingDB(" + db + ");",
		"if (!target.getCollectionNames().includes('cashp_init')) { target.createCollection('cashp_init'); }",
	}
	if owner != "" {
		if err := ValidateIdentifier(EngineMongoDB, "username", owner); err != nil {
			return nil, err
		}
		user, err := QuoteIdentifier(EngineMongoDB, owner)
		if err != nil {
			return nil, err
		}
		lines = append(lines,
			"target.updateUser("+user+", {roles: [{role: 'dbOwner', db: "+db+"}]});",
		)
	}
	cmd, err := a.script(c, lines, adminTimeout)
	if err != nil {
		return nil, err
	}
	return []command{cmd}, nil
}

func (a mongodbAdapter) dropDatabase(c engineCtx, name string) ([]command, error) {
	if err := ValidateIdentifier(EngineMongoDB, "database name", name); err != nil {
		return nil, err
	}
	db, err := QuoteIdentifier(EngineMongoDB, name)
	if err != nil {
		return nil, err
	}
	cmd, err := a.script(c, []string{
		"db.getSiblingDB(" + db + ").dropDatabase();",
	}, adminTimeout)
	if err != nil {
		return nil, err
	}
	return []command{cmd}, nil
}

func (a mongodbAdapter) listDatabases(c engineCtx) ([]command, error) {
	cmd, err := a.script(c, []string{
		"const reply = db.getSiblingDB('admin').adminCommand({listDatabases: 1, nameOnly: true});",
		"reply.databases.forEach(function (entry) { print(entry.name); });",
	}, adminTimeout)
	if err != nil {
		return nil, err
	}
	return []command{cmd}, nil
}

func (mongodbAdapter) parseDatabaseList(res ExecResult) ([]string, error) {
	if res.ExitCode != 0 {
		return nil, ErrUnavailable("The instance did not answer a database listing.")
	}
	out := make([]string, 0)
	for _, line := range trimLines(res.Stdout) {
		if reservedIdentifiers[EngineMongoDB][strings.ToLower(line)] {
			continue
		}
		out = append(out, line)
	}
	return out, nil
}

// createUser creates the account inside the database it is scoped to, with an
// empty role list. The account can authenticate and do nothing until grant
// gives it a role.
func (a mongodbAdapter) createUser(c engineCtx, username, database, password string) ([]command, error) {
	db, user, err := a.userTarget(username, database)
	if err != nil {
		return nil, err
	}
	if err := assertSafeSecret(password); err != nil {
		return nil, err
	}
	secret, err := jsonString(password)
	if err != nil {
		return nil, err
	}
	cmd, err := a.script(c, []string{
		"db.getSiblingDB(" + db + ").createUser({user: " + user + ", pwd: " + secret + ", roles: []});",
	}, adminTimeout)
	if err != nil {
		return nil, err
	}
	return []command{cmd}, nil
}

func (a mongodbAdapter) setPassword(c engineCtx, username, database, password string) ([]command, error) {
	db, user, err := a.userTarget(username, database)
	if err != nil {
		return nil, err
	}
	if err := assertSafeSecret(password); err != nil {
		return nil, err
	}
	secret, err := jsonString(password)
	if err != nil {
		return nil, err
	}
	cmd, err := a.script(c, []string{
		"db.getSiblingDB(" + db + ").updateUser(" + user + ", {pwd: " + secret + "});",
	}, adminTimeout)
	if err != nil {
		return nil, err
	}
	return []command{cmd}, nil
}

func (a mongodbAdapter) dropUser(c engineCtx, username, database string) ([]command, error) {
	db, user, err := a.userTarget(username, database)
	if err != nil {
		return nil, err
	}
	cmd, err := a.script(c, []string{
		"db.getSiblingDB(" + db + ").dropUser(" + user + ");",
	}, adminTimeout)
	if err != nil {
		return nil, err
	}
	return []command{cmd}, nil
}

func (a mongodbAdapter) grant(c engineCtx, username, database string, level GrantLevel) ([]command, error) {
	db, user, err := a.userTarget(username, database)
	if err != nil {
		return nil, err
	}
	if !level.Valid() {
		return nil, ErrValidation("That privilege level is not one this server issues.")
	}
	var role string
	switch level {
	case GrantReadOnly:
		role = "read"
	case GrantReadWrite:
		role = "readWrite"
	case GrantOwner:
		role = "dbOwner"
	}
	cmd, err := a.script(c, []string{
		"db.getSiblingDB(" + db + ").updateUser(" + user + ", {roles: [{role: '" + role + "', db: " + db + "}]});",
	}, adminTimeout)
	if err != nil {
		return nil, err
	}
	return []command{cmd}, nil
}

func (a mongodbAdapter) revoke(c engineCtx, username, database string) ([]command, error) {
	db, user, err := a.userTarget(username, database)
	if err != nil {
		return nil, err
	}
	cmd, err := a.script(c, []string{
		"db.getSiblingDB(" + db + ").updateUser(" + user + ", {roles: []});",
	}, adminTimeout)
	if err != nil {
		return nil, err
	}
	return []command{cmd}, nil
}

// dump streams a compressed archive on standard output. The password is
// staged in an owner-only config file for the duration of the command rather
// than passed as an argument.
func (a mongodbAdapter) dump(c engineCtx, database string, out io.Writer) ([]command, error) {
	argv := []string{
		"mongodump",
		"--host=" + c.Host,
		"--port=" + strconv.Itoa(c.Port),
		"--username=" + c.AdminUser,
		"--authenticationDatabase=admin",
		"--config=" + mongodbDumpConfig,
		"--archive",
		"--gzip",
	}
	if database != "" {
		if err := ValidateIdentifier(EngineMongoDB, "database name", database); err != nil {
			return nil, err
		}
		argv = append(argv, "--db="+database)
	}
	files, err := a.authConfig(c, mongodbDumpConfig)
	if err != nil {
		return nil, err
	}
	cmd, err := streamCommand(argv, nil, out, transferTimeout)
	if err != nil {
		return nil, err
	}
	cmd.Files = files
	cmd.Cleanup = []string{mongodbDumpConfig}
	return []command{cmd}, nil
}

func (a mongodbAdapter) restore(c engineCtx, database string, in io.Reader) (restorePlan, error) {
	argv := []string{
		"mongorestore",
		"--host=" + c.Host,
		"--port=" + strconv.Itoa(c.Port),
		"--username=" + c.AdminUser,
		"--authenticationDatabase=admin",
		"--config=" + mongodbRestoreConfig,
		"--archive",
		"--gzip",
		"--drop",
	}
	if database != "" {
		if err := ValidateIdentifier(EngineMongoDB, "database name", database); err != nil {
			return restorePlan{}, err
		}
		argv = append(argv, "--nsInclude="+database+".*")
	}
	files, err := a.authConfig(c, mongodbRestoreConfig)
	if err != nil {
		return restorePlan{}, err
	}
	cmd, err := inputCommand(argv, nil, in, transferTimeout)
	if err != nil {
		return restorePlan{}, err
	}
	cmd.Files = files
	cmd.Cleanup = []string{mongodbRestoreConfig}
	return restorePlan{Online: &cmd}, nil
}

// replicaNeedsSeed is false: a member added to a replica set performs its own
// initial sync from the primary.
func (mongodbAdapter) replicaNeedsSeed() bool { return false }

// attachReplica adds the replica to its primary's set as a non-voting,
// zero-priority member, so it serves reads but can never be elected primary
// behind the tenant's back.
func (a mongodbAdapter) attachReplica(replica, primary engineCtx, databases []string) ([]replicaCommand, error) {
	host, err := mongodbMember(replica)
	if err != nil {
		return nil, err
	}
	cmd, err := a.script(primary, []string{
		"rs.add({host: " + host + ", priority: 0, votes: 0});",
	}, adminTimeout)
	if err != nil {
		return nil, err
	}
	return []replicaCommand{{Target: targetPrimary, Cmd: cmd}}, nil
}

func (a mongodbAdapter) detachReplica(replica, primary engineCtx, databases []string) ([]replicaCommand, error) {
	host, err := mongodbMember(replica)
	if err != nil {
		return nil, err
	}
	cmd, err := a.script(primary, []string{
		"rs.remove(" + host + ");",
	}, adminTimeout)
	if err != nil {
		return nil, err
	}
	return []replicaCommand{{Target: targetPrimary, Cmd: cmd}}, nil
}

// userTarget validates and quotes the database and username every account
// operation needs. MongoDB has no server-wide account in this design: every
// tenant account belongs to exactly one database.
func (mongodbAdapter) userTarget(username, database string) (string, string, error) {
	if database == "" {
		return "", "", ErrValidation("A MongoDB account must be scoped to a database.")
	}
	if err := ValidateIdentifier(EngineMongoDB, "database name", database); err != nil {
		return "", "", err
	}
	if err := ValidateIdentifier(EngineMongoDB, "username", username); err != nil {
		return "", "", err
	}
	db, err := QuoteIdentifier(EngineMongoDB, database)
	if err != nil {
		return "", "", err
	}
	user, err := QuoteIdentifier(EngineMongoDB, username)
	if err != nil {
		return "", "", err
	}
	return db, user, nil
}

// script builds a mongosh command. mongosh is started with --nodb and the
// script opens its own connection from the environment, which is what keeps
// the credential out of the argument list.
func (a mongodbAdapter) script(c engineCtx, body []string, timeout time.Duration) (command, error) {
	if err := assertSafeSecret(c.AdminPassword); err != nil {
		return command{}, err
	}
	argv := []string{"mongosh", "--nodb", "--quiet"}
	if err := checkArgv(argv); err != nil {
		return command{}, err
	}
	lines := append([]string{
		"const conn = Mongo(process.env." + mongodbURIEnv + ");",
		"const db = conn.getDB('admin');",
	}, body...)
	env := map[string]string{mongodbURIEnv: mongodbURI(c)}
	return command{Exec: execRequest(argv, env, strings.NewReader(strings.Join(lines, "\n")+"\n"), timeout)}, nil
}

// authConfig stages the owner-only YAML file the dump tools read the
// administrative password from.
func (mongodbAdapter) authConfig(c engineCtx, path string) ([]fileDrop, error) {
	if err := assertSafeSecret(c.AdminPassword); err != nil {
		return nil, err
	}
	return []fileDrop{{
		Path:    path,
		Mode:    0o400,
		UID:     mongodbUID,
		GID:     mongodbGID,
		Content: []byte("password: " + c.AdminPassword + "\n"),
	}}, nil
}

// mongodbURI builds the administrative connection string. The password is
// alphanumeric by construction, so it needs no percent encoding.
func mongodbURI(c engineCtx) string {
	return "mongodb://" + c.AdminUser + ":" + c.AdminPassword +
		"@" + c.Host + ":" + strconv.Itoa(c.Port) +
		"/?authSource=admin&directConnection=true"
}

// mongodbMember renders an instance's replica set member address as a JSON
// string literal.
func mongodbMember(c engineCtx) (string, error) {
	if !instanceNamePattern.MatchString(c.Instance.ContainerName) {
		return "", ErrInternal(nil, "That instance could not be addressed.")
	}
	return jsonString(c.Instance.ContainerName + ":" + strconv.Itoa(c.Port))
}

// mongodbSetName renders the replica set name as a JSON string literal.
func mongodbSetName() string {
	encoded, err := jsonString(mongodbReplicaSet)
	if err != nil {
		// The name is a compile-time constant, so encoding it cannot fail;
		// the quoted constant is returned so the caller stays total.
		return `"` + mongodbReplicaSet + `"`
	}
	return encoded
}

// mongodbKeyMaterial derives the shared replica set key. A replica computes
// the same value from its primary's password and identifier, so the key never
// has to be stored or transported.
func mongodbKeyMaterial(adminPassword, instanceID string) string {
	sum := sha256.Sum256([]byte("cashp-mongo-keyfile:" + adminPassword + ":" + instanceID))
	return hex.EncodeToString(sum[:])
}
