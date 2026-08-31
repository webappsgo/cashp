package support

// Bot pattern data. This file is STATIC DATA compiled into the binary.
// It is generated at BUILD TIME by scanning the cashp source tree (src/errors
// code constants, IDEA.md feature surface, validation messages) and is never
// regenerated, fetched, or inferred at runtime. There is no AI or ML anywhere
// in the production path: the bot performs regular-expression matching only.
// No HTTP route, API endpoint, template, or SQL query exposes this data.

// Universal bot categories. These are the ten categories every deployment
// carries regardless of which cashp features an operator enabled.
const (
	BotCategoryAuth         = "auth"
	BotCategoryPerformance  = "performance"
	BotCategoryAccess       = "access"
	BotCategoryErrorMessage = "error_message"
	BotCategoryBilling      = "billing"
	BotCategoryData         = "data"
	BotCategoryInstall      = "install"
	BotCategoryBug          = "bug"
	BotCategoryHowTo        = "howto"
	BotCategoryAccount      = "account"
)

// Project-specific bot categories derived from the cashp feature surface
// documented in IDEA.md "Product scope & non-goals".
const (
	BotCategoryHosting   = "hosting"
	BotCategoryPaaS      = "paas"
	BotCategoryContainer = "container"
	BotCategoryVM        = "vm"
	BotCategoryMail      = "mail"
	BotCategoryDNS       = "dns"
	BotCategoryDatabase  = "database"
	BotCategoryBackup    = "backup"
	BotCategoryCluster   = "cluster"
	BotCategoryQuota     = "quota"
)

// botPatterns is the compiled-in pattern table. Order is fixed so matching is
// reproducible: the engine sorts candidates by score and then by ID, and a tie
// at the top score is treated as ambiguous rather than being broken arbitrarily.
var botPatterns = []BotPattern{
	{
		ID:       "auth.login-failed",
		Category: BotCategoryAuth,
		Expressions: []string{
			`(?i)\b(can'?t|cannot|unable to)\s+(log ?in|sign ?in)\b`,
			`(?i)\blogin\s+(fail(ed|s|ure)|not working|doesn'?t work)\b`,
			`(?i)\bauthentication\s+failed\b`,
			`(?i)\bUNAUTHORIZED\b`,
			`(?i)\binvalid\s+(password|credentials|username)\b`,
		},
		Response: "Sign-in problems are almost always one of three things:\n" +
			"1. The username is correct but the password is not — use the password reset link on the sign-in page.\n" +
			"2. Your session cookie is stale — sign out fully, clear cookies for this panel's domain, then try again.\n" +
			"3. The account is temporarily locked after repeated failures — locks expire on their own; wait and retry.\n" +
			"Usernames are case-sensitive and are never reused after deletion, so an old username will not sign in.",
		SuggestedCategory: BotCategoryAuth,
		SuggestedPriority: PriorityNormal,
	},
	{
		ID:       "auth.account-locked",
		Category: BotCategoryAuth,
		Expressions: []string{
			`(?i)\bACCOUNT_LOCKED\b`,
			`(?i)\baccount\s+(is\s+)?(locked|suspended|disabled)\b`,
			`(?i)\blocked\s+(me\s+)?out\b`,
		},
		Response: "An account lock is an automatic protection after repeated failed sign-in attempts. " +
			"It clears itself once the lockout window passes — no action is needed. " +
			"If you still cannot sign in after the window, the account may have been disabled by your account administrator, " +
			"who can re-enable it from the panel's user list.",
		SuggestedCategory: BotCategoryAuth,
		SuggestedPriority: PriorityHigh,
	},
	{
		ID:       "auth.two-factor",
		Category: BotCategoryAuth,
		Expressions: []string{
			`(?i)\b2FA_(REQUIRED|INVALID)\b`,
			`(?i)\b(2fa|two[- ]factor|totp|authenticator)\b`,
			`(?i)\b(recovery|backup)\s+codes?\b`,
		},
		Response: "Two-factor codes are time-based, so the clock on the device generating them must be accurate — " +
			"enable automatic time synchronisation on that device and try again. " +
			"If the device is lost, use one of the recovery codes issued when two-factor was enabled; each code works once. " +
			"Nobody, including a global administrator, can read or disable another account's two-factor secret, " +
			"so a lost device with no recovery codes requires the account to be recreated by your account administrator.",
		SuggestedCategory: BotCategoryAuth,
		SuggestedPriority: PriorityHigh,
	},
	{
		ID:       "auth.session-expired",
		Category: BotCategoryAuth,
		Expressions: []string{
			`(?i)\bTOKEN_(EXPIRED|INVALID|REVOKED)\b`,
			`(?i)\bsession\s+(expired|timed?\s*out|invalid)\b`,
			`(?i)\b(keeps?|kept)\s+logging\s+me\s+out\b`,
		},
		Response: "Sessions expire after a period of inactivity, and every session is invalidated when you change your password " +
			"or rotate an API token. Signing in again issues a fresh session. " +
			"If you are being signed out within minutes, a reverse proxy in front of the panel is most likely stripping or " +
			"rewriting the session cookie — confirm the proxy forwards cookies and the original Host header unchanged.",
		SuggestedCategory: BotCategoryAuth,
		SuggestedPriority: PriorityNormal,
	},
	{
		ID:       "performance.slow",
		Category: BotCategoryPerformance,
		Expressions: []string{
			`(?i)\b(very\s+)?slow(ly)?\b`,
			`(?i)\b(loading|spinning)\s+forever\b`,
			`(?i)\btakes?\s+(too\s+long|ages|forever)\b`,
			`(?i)\b(not\s+responding|unresponsive|frozen|hangs?)\b`,
			`(?i)\bTIMEOUT\b`,
		},
		Response: "Slowness in the panel is usually resource pressure on the node rather than the panel itself. " +
			"Open the monitoring view and check CPU, memory, and disk I/O for the node hosting the affected resource. " +
			"A node at or near its memory limit will make every request slow, not just one page. " +
			"On low-power hardware, background work such as malware scanning or a running backup will noticeably slow " +
			"interactive requests until it finishes.",
		SuggestedCategory: BotCategoryPerformance,
		SuggestedPriority: PriorityNormal,
	},
	{
		ID:       "access.forbidden",
		Category: BotCategoryAccess,
		Expressions: []string{
			`(?i)\bFORBIDDEN\b`,
			`(?i)\b403\b`,
			`(?i)\b(access|permission)\s+denied\b`,
			`(?i)\bnot\s+authori[sz]ed\b`,
			`(?i)\b(can'?t|cannot)\s+access\b`,
		},
		Response: "A permission error means your role does not grant that action, and roles are enforced on the server for " +
			"every request — the panel never hides a control you are actually allowed to use.\n" +
			"An end user can only use the specific services their account administrator granted them.\n" +
			"An account administrator manages only their own account's resources, never another account's.\n" +
			"Ask your account administrator to grant the specific resource, rather than a broader role.",
		SuggestedCategory: BotCategoryAccess,
		SuggestedPriority: PriorityNormal,
	},
	{
		ID:       "error.generic-code",
		Category: BotCategoryErrorMessage,
		Expressions: []string{
			`(?i)\bSERVER_ERROR\b`,
			`(?i)\bBAD_REQUEST\b`,
			`(?i)\b(exception|stack ?trace|panic)\b`,
			`\bERR_[A-Z0-9_]{2,}\b`,
		},
		Response: "Every failed request in cashp is answered with a stable machine-readable error code plus a request " +
			"identifier. Both appear in the error box in the panel. " +
			"Copy the exact code and the request identifier into your ticket — with them, the matching server-side log line " +
			"can be found immediately, which is far faster than reproducing the problem.",
		SuggestedCategory: BotCategoryErrorMessage,
		SuggestedPriority: PriorityNormal,
	},
	{
		ID:       "error.validation",
		Category: BotCategoryErrorMessage,
		Expressions: []string{
			`(?i)\bVALIDATION_FAILED\b`,
			`(?i)\bis\s+required\b`,
			`(?i)\binvalid\s+(value|format|input|field)\b`,
			`(?i)\bmust\s+be\s+(a|an|at least|between)\b`,
		},
		Response: "A validation error names the exact field that was rejected and why, directly beneath that field. " +
			"cashp validates on the server, so a value the browser accepted can still be rejected. " +
			"The most common causes are a domain name with a trailing dot or an underscore, a port already claimed by " +
			"another service on the node, and a value longer than the field's maximum length.",
		SuggestedCategory: BotCategoryErrorMessage,
		SuggestedPriority: PriorityLow,
	},
	{
		ID:       "error.conflict",
		Category: BotCategoryErrorMessage,
		Expressions: []string{
			`(?i)\bCONFLICT\b`,
			`(?i)\balready\s+(exists|taken|in use|registered)\b`,
			`(?i)\bduplicate\b`,
		},
		Response: "A conflict means the name you chose is already claimed. " +
			"Usernames are unique across the whole cluster and are permanently tombstoned when deleted, so a name that " +
			"once belonged to a removed account never becomes available again. " +
			"Domains, database names, and mailbox addresses are likewise unique — pick a different name, " +
			"or remove the existing resource first if it is yours.",
		SuggestedCategory: BotCategoryErrorMessage,
		SuggestedPriority: PriorityNormal,
	},
	{
		ID:       "error.rate-limited",
		Category: BotCategoryErrorMessage,
		Expressions: []string{
			`(?i)\bRATE_LIMITED\b`,
			`(?i)\b429\b`,
			`(?i)\btoo\s+many\s+(requests|attempts|tries)\b`,
			`(?i)\brate\s*limit(ed|ing)?\b`,
		},
		Response: "Rate limits protect every tenant on a shared node and apply identically to all accounts on all plans. " +
			"The response carries the number of seconds to wait; waiting that long and retrying always succeeds. " +
			"If an automated integration is hitting the limit, spread its requests out or batch them — " +
			"the limit is per account, not per API token, so adding tokens will not raise it.",
		SuggestedCategory: BotCategoryErrorMessage,
		SuggestedPriority: PriorityNormal,
	},
	{
		ID:       "error.maintenance",
		Category: BotCategoryErrorMessage,
		Expressions: []string{
			`(?i)\b(MAINTENANCE|UNAVAILABLE)\b`,
			`(?i)\b(503|502|504)\b`,
			`(?i)\bmaintenance\s+mode\b`,
		},
		Response: "The panel returns a maintenance response while an operator-initiated upgrade or restore is running. " +
			"Hosted sites, apps, containers, and mail keep serving during panel maintenance — only the control surface " +
			"is paused. The status page shows when the window is expected to end.",
		SuggestedCategory: BotCategoryErrorMessage,
		SuggestedPriority: PriorityHigh,
	},
	{
		ID:       "billing.payment-failed",
		Category: BotCategoryBilling,
		Expressions: []string{
			`(?i)\bpayment\s+(fail(ed|ure)|declin(e|ed)|error)\b`,
			`(?i)\bcard\s+(declin(e|ed)|expired|rejected)\b`,
			`(?i)\b(billing|invoice|charge[ds]?|refund)\b`,
			`(?i)\b(subscription|plan)\s+(problem|issue|question)\b`,
		},
		Response: "A declined payment is reported by the payment provider, not by cashp — cashp never stores card details. " +
			"Check that the card has not expired, that the billing address matches the card exactly, and that your bank " +
			"has not blocked the charge. " +
			"Service is not suspended for a single failed charge: the billing event is queued and retried, and an invoice " +
			"is issued for every billing event so you always have a record.",
		SuggestedCategory: BotCategoryBilling,
		SuggestedPriority: PriorityHigh,
	},
	{
		ID:       "quota.exceeded",
		Category: BotCategoryQuota,
		Expressions: []string{
			`(?i)\bQUOTA_EXCEEDED\b`,
			`(?i)\bPAYLOAD_TOO_LARGE\b`,
			`(?i)\b(quota|limit)\s+(exceeded|reached|hit|full)\b`,
			`(?i)\b(out of|no more)\s+(space|storage|disk)\b`,
			`(?i)\bdisk\s+full\b`,
		},
		Response: "Quotas cap how much of a resource an account may use; they never remove a feature. " +
			"Every plan runs exactly the same code with different ceilings, so upgrading raises the ceiling rather than " +
			"unlocking anything new. " +
			"The account overview lists current usage against each limit — storage, bandwidth, and the counts of sites, " +
			"apps, containers, and virtual machines. Freeing space or removing an unused resource clears the error immediately.",
		SuggestedCategory: BotCategoryQuota,
		SuggestedPriority: PriorityHigh,
	},
	{
		ID:       "data.missing",
		Category: BotCategoryData,
		Expressions: []string{
			`(?i)\bNOT_FOUND\b`,
			`(?i)\b404\b`,
			`(?i)\b(data|files?|records?)\s+(missing|gone|disappeared)\b`,
			`(?i)\bnot\s+(syncing|saving|showing|appearing)\b`,
			`(?i)\blost\s+(my|all|the)\b`,
		},
		Response: "Data that vanished from the panel is usually still on disk. " +
			"Check the backup browser first: cashp keeps recent daily, several weekly, and a few monthly restore points, " +
			"and any of them can be restored without a full system restore. " +
			"If the resource is missing from lists but its files remain, it was most likely removed from the panel's " +
			"inventory rather than deleted — a restore from the most recent backup brings it back.",
		SuggestedCategory: BotCategoryData,
		SuggestedPriority: PriorityHigh,
	},
	{
		ID:       "install.getting-started",
		Category: BotCategoryInstall,
		Expressions: []string{
			`(?i)\bhow\s+(do i|to)\s+install\b`,
			`(?i)\b(getting|get)\s+started\b`,
			`(?i)\b(first[- ]time|new user|initial)\s+setup\b`,
			`(?i)\bsetup\s+token\b`,
		},
		Response: "cashp is a single binary and works with no configuration on first run. " +
			"On first start it prints a one-time setup token to the console; whoever redeems that token creates the primary " +
			"global administrator. There is no \"first account becomes admin\" behaviour, and self-service registration " +
			"never grants administrative rights. " +
			"If the token scrolled past, it is also written to the server log for that first start.",
		SuggestedCategory: BotCategoryInstall,
		SuggestedPriority: PriorityNormal,
	},
	{
		ID:       "install.dependency",
		Category: BotCategoryInstall,
		Expressions: []string{
			`(?i)\b(missing|unmet|broken)\s+dependenc(y|ies)\b`,
			`(?i)\b(package|repo(sitory)?)\s+(not found|unavailable|fail(ed|s))\b`,
			`(?i)\b(apt|dnf|yum|apk|pacman)\b.*\b(error|fail)`,
			`(?i)\bgpg\b.*\b(key|signature)\b`,
		},
		Response: "cashp writes third-party repository definitions itself and pins each signing key to a fingerprint " +
			"compiled into the binary; it never runs a vendor install script and never disables signature checking. " +
			"So a repository failure means the repository host was unreachable or its key did not match the pinned " +
			"fingerprint. Only the feature that needed that repository fails — the rest of the install continues. " +
			"Retry once the network is reachable; if the key mismatch persists, report it, because that is worth investigating.",
		SuggestedCategory: BotCategoryInstall,
		SuggestedPriority: PriorityHigh,
	},
	{
		ID:       "hosting.ssl",
		Category: BotCategoryHosting,
		Expressions: []string{
			`(?i)\b(ssl|tls|https|certificate|cert)\b.*\b(fail|error|expired|invalid|not working|renew)`,
			`(?i)\b(lets ?encrypt|let'?s encrypt|acme)\b`,
			`(?i)\bNET::ERR_CERT`,
		},
		Response: "Certificate issuance is domain-validated, so the domain must already resolve to this server and port 80 " +
			"must be reachable from the internet before a certificate can be issued. " +
			"A failed issuance never takes a site offline: the site keeps serving on its existing certificate, or falls back " +
			"to plain HTTP with a warning, and issuance is retried automatically on schedule. " +
			"The usual causes are DNS still pointing elsewhere, a firewall blocking port 80, or a CAA record on the domain " +
			"that forbids the issuing authority.",
		SuggestedCategory: BotCategoryHosting,
		SuggestedPriority: PriorityHigh,
	},
	{
		ID:       "hosting.php-version",
		Category: BotCategoryHosting,
		Expressions: []string{
			`(?i)\bphp[- ]?(fpm|version|\d+\.\d+)\b`,
			`(?i)\bphp\b.*\b(not (available|listed|installed)|missing|wrong version)`,
		},
		Response: "Multiple PHP versions run side by side and are chosen per site, not per server. " +
			"Which versions are offered depends on the host operating system: Alpine cannot provide PHP 5.6 or any 7.x " +
			"release at all, and Arch offers only its single current PHP package. " +
			"On Debian, Ubuntu, RHEL-family, and Fedora hosts the full 5.6 to 8.4 range is available once the pinned " +
			"third-party PHP repository has been added. The site's PHP selector lists exactly what this host can actually run.",
		SuggestedCategory: BotCategoryHosting,
		SuggestedPriority: PriorityNormal,
	},
	{
		ID:       "hosting.ftp",
		Category: BotCategoryHosting,
		Expressions: []string{
			`(?i)\b(ftp|sftp|scp)\b.*\b(fail|error|refus|denied|can'?t connect|timeout)`,
			`(?i)\b(can'?t|cannot|unable to)\s+(upload|connect).*\b(ftp|sftp)\b`,
		},
		Response: "File transfer accounts are scoped to a single site's document root and cannot traverse above it. " +
			"A refused connection is normally the firewall or the client using plain FTP where only SFTP is enabled. " +
			"A connection that succeeds but shows an empty directory means the account is pointed at a different site's root " +
			"than you expected — check which site the transfer account belongs to in the site's file access settings.",
		SuggestedCategory: BotCategoryHosting,
		SuggestedPriority: PriorityNormal,
	},
	{
		ID:       "paas.deploy",
		Category: BotCategoryPaaS,
		Expressions: []string{
			`(?i)\b(deploy(ment)?|build)\b.*\b(fail(ed|s|ure)|error|broke)`,
			`(?i)\b(git\s+push|blue[- ]green|rollback)\b`,
			`(?i)\b(buildpack|runtime)\s+(not )?detect`,
		},
		Response: "Deployments are blue-green: the new release is built and started before traffic moves, so a failed build " +
			"never takes the running release down, and rolling back restores the previous release immediately. " +
			"Runtime detection reads the standard project layout for each supported language — a Node.js project needs its " +
			"package.json at the repository root, a Python project its requirements file or pyproject file, a Go project its " +
			"go.mod, and so on. A project whose manifest sits in a subdirectory is not detected; set the build directory " +
			"explicitly in the app's settings.",
		SuggestedCategory: BotCategoryPaaS,
		SuggestedPriority: PriorityHigh,
	},
	{
		ID:       "container.runtime",
		Category: BotCategoryContainer,
		Expressions: []string{
			`(?i)\b(docker|podman|incus|container)\b.*\b(fail|error|won'?t start|crash|exit(ed)?)`,
			`(?i)\b(image\s+pull|registry)\b.*\b(fail|error|denied|unauthorized)`,
			`(?i)\bOCI\b`,
		},
		Response: "Docker, Incus, and Podman all run side by side on the same node, so a workload failing under one backend " +
			"is not a reason to switch — check the container's own log output first, which the panel streams live.\n" +
			"A pull failure from a private registry is nearly always missing or expired registry credentials on that " +
			"workload's settings.\n" +
			"A container that starts then exits immediately has usually failed inside its own entrypoint; the exit code and " +
			"the last lines of its log identify the cause. cashp does not inspect or vet image contents.",
		SuggestedCategory: BotCategoryContainer,
		SuggestedPriority: PriorityHigh,
	},
	{
		ID:       "container.network",
		Category: BotCategoryContainer,
		Expressions: []string{
			`(?i)\b(container|workload)s?\b.*\b(can'?t|cannot)\s+(reach|see|talk to|connect)`,
			`(?i)\b(port\s+(mapping|forward)|expose[ds]?\s+port)\b`,
		},
		Response: "Every account's containers run on their own isolated network, which is what prevents one tenant reaching " +
			"another's workloads — so two containers can only reach each other if they belong to the same account and the " +
			"same network. " +
			"To reach a container from outside, publish the port through the workload's port settings rather than binding it " +
			"directly on the host; a directly bound port bypasses the isolation and is refused.",
		SuggestedCategory: BotCategoryContainer,
		SuggestedPriority: PriorityNormal,
	},
	{
		ID:       "vm.virtualization",
		Category: BotCategoryVM,
		Expressions: []string{
			`(?i)\b(vm|virtual machine|kvm|qemu|libvirt|hypervisor)\b`,
			`(?i)\b(vnc|spice)\b.*\b(console|connect|black|blank)`,
			`(?i)\b(passthrough|gpu|pci|usb)\b.*\b(vm|guest)`,
		},
		Response: "Virtual machine features are enabled only when the host actually supports hardware virtualisation; " +
			"on a host without it, the whole feature is switched off rather than offered and failing.\n" +
			"A blank console is normally a guest that has not finished booting, or a guest whose display is on a different " +
			"graphics device than the one the console is attached to.\n" +
			"Device passthrough requires the host to expose the device in its own isolation group; the panel lists exactly " +
			"which devices on this host can be passed through, and devices absent from that list cannot be.",
		SuggestedCategory: BotCategoryVM,
		SuggestedPriority: PriorityHigh,
	},
	{
		ID:       "mail.delivery",
		Category: BotCategoryMail,
		Expressions: []string{
			`(?i)\b(e?mail|mailbox|smtp|imap|pop3)\b.*\b(not (send|sending|receiv|arriv|work)|fail|bounce|reject)`,
			`(?i)\b(spam|junk)\s+folder\b`,
			`(?i)\b(spf|dkim|dmarc)\b`,
		},
		Response: "Outbound mail that is accepted here but never arrives is almost always a receiving-side reputation check. " +
			"Confirm all three DNS records for the sending domain: an SPF record authorising this server, a DKIM record " +
			"matching the signing key cashp generated for the domain, and a DMARC record. The mail domain view shows each " +
			"record's current published value against the expected one.\n" +
			"Inbound mail that never arrives is usually held by anti-spam or anti-virus scanning, which is always on and " +
			"cannot be disabled for one account; the mailbox's quarantine view shows what was held and why.",
		SuggestedCategory: BotCategoryMail,
		SuggestedPriority: PriorityHigh,
	},
	{
		ID:       "dns.zone",
		Category: BotCategoryDNS,
		Expressions: []string{
			`(?i)\b(dns|nameserver|name server|dnssec|zone)\b`,
			`(?i)\b(a|aaaa|cname|mx|txt|ns)\s+record\b`,
			`(?i)\b(domain|subdomain)\b.*\b(not resolv|won'?t resolv|propagat)`,
		},
		Response: "A record change is published immediately but resolvers elsewhere keep the old answer until its time-to-live " +
			"expires, so lowering the record's TTL before a planned change is what makes it take effect quickly.\n" +
			"If a domain does not resolve at all, check that the domain's registrar still points its nameservers at this " +
			"server — cashp manages DNS records but is not a registrar and never registers or transfers domains.\n" +
			"When DNSSEC is enabled, the signing key's delegation record must also be published at the registrar, " +
			"or validating resolvers will refuse the zone outright.",
		SuggestedCategory: BotCategoryDNS,
		SuggestedPriority: PriorityHigh,
	},
	{
		ID:       "database.connection",
		Category: BotCategoryDatabase,
		Expressions: []string{
			`(?i)\b(database|db|postgres(ql)?|mariadb|mysql|mongo(db)?|valkey|redis)\b.*\b(connect|fail|error|refus|down|slow)`,
			`(?i)\b(replication|failover)\b`,
			`(?i)\bconnection\s+(refused|reset|pool)\b`,
		},
		Response: "Database instances run as containers that cashp manages, so every node in a cluster runs the identical " +
			"version — the engine is not something to install or upgrade yourself.\n" +
			"A refused connection from an application is nearly always the host or port: connect using the internal hostname " +
			"the database's detail page shows, not localhost, because the engine runs in its own container.\n" +
			"Credentials are stored encrypted and are shown in that same detail page; if they were rotated, the application's " +
			"configuration needs the new value.",
		SuggestedCategory: BotCategoryDatabase,
		SuggestedPriority: PriorityHigh,
	},
	{
		ID:       "backup.restore",
		Category: BotCategoryBackup,
		Expressions: []string{
			`(?i)\b(backup|restore|snapshot|retention)\b`,
			`(?i)\b(recover|roll ?back)\b.*\b(data|site|database|server)`,
		},
		Response: "Backups are deduplicated and compressed, so the stored size is a fraction of the primary data, and " +
			"retention keeps recent dailies, several weeklies, and a few monthlies rather than every snapshot forever.\n" +
			"A restore only proceeds after its checksum, manifest, decryption test, and version compatibility all verify — " +
			"a restore that refuses to start is reporting which of those checks failed, and forcing past it is not offered.\n" +
			"Restores can be per-service or a full bare-metal restore, and the archive format is open, so a backup can be " +
			"read without cashp.",
		SuggestedCategory: BotCategoryBackup,
		SuggestedPriority: PriorityUrgent,
	},
	{
		ID:       "cluster.node",
		Category: BotCategoryCluster,
		Expressions: []string{
			`(?i)\b(cluster|node|failover|floating ip|high availability|ha)\b`,
			`(?i)\bnode\s+(offline|down|unreachable|left|not joining)\b`,
		},
		Response: "A node that shows as offline has stopped sending heartbeats; its workloads fail over automatically and the " +
			"floating address moves to a remaining node. " +
			"The usual causes are the node's clock drifting far enough to break authentication between nodes, or the cluster " +
			"port being blocked between them. " +
			"On low-power hardware, failover is slower rather than unavailable — that is expected and not a fault.",
		SuggestedCategory: BotCategoryCluster,
		SuggestedPriority: PriorityUrgent,
	},
	{
		ID:       "bug.report",
		Category: BotCategoryBug,
		Expressions: []string{
			`(?i)\b(bug|broken|defect)\b`,
			`(?i)\b(doesn'?t|does not|isn'?t|is not)\s+work(ing)?\b`,
			`(?i)\bnot\s+working\s+(properly|correctly|right)\b`,
			`(?i)\bunexpected(ly)?\b`,
		},
		Response: "To have a bug fixed rather than triaged, a ticket needs three things: the exact steps that reproduce it, " +
			"what you expected instead, and the error code plus request identifier from the error box if one appeared. " +
			"Say whether it happens every time or only sometimes, and on which resource — a fault that affects one site but " +
			"not another usually points at that site's own configuration rather than the panel.",
		SuggestedCategory: BotCategoryBug,
		SuggestedPriority: PriorityNormal,
	},
	{
		ID:       "howto.find",
		Category: BotCategoryHowTo,
		Expressions: []string{
			`(?i)\bhow\s+(do|does|can)\s+i\b`,
			`(?i)\bhow\s+to\b`,
			`(?i)\bwhere\s+(is|do i find|can i find)\b`,
			`(?i)\b(can'?t|cannot)\s+find\b`,
		},
		Response: "The knowledge base is searchable from the help page and covers every panel feature. " +
			"The panel groups everything by the resource it belongs to rather than by feature, so a setting for one site " +
			"lives on that site's own page, not in a global settings screen. " +
			"Anything the panel can do is also reachable through the API, and the API documentation lists every action with " +
			"its exact request shape.",
		SuggestedCategory: BotCategoryHowTo,
		SuggestedPriority: PriorityLow,
	},
	{
		ID:       "account.manage",
		Category: BotCategoryAccount,
		Expressions: []string{
			`(?i)\b(delete|close|cancel)\s+(my\s+)?account\b`,
			`(?i)\bchange\s+(my\s+)?(email|e-mail|password|username)\b`,
			`(?i)\bupdate\s+(my\s+)?profile\b`,
		},
		Response: "Your email address, password, and two-factor settings are all changed from your own profile page; " +
			"changing the password signs out every other session.\n" +
			"A username cannot be changed and is never released after deletion — it is permanently tombstoned so that no " +
			"later account can inherit the old identity or its mail and DNS references.\n" +
			"Closing an account removes its resources permanently, so export anything you want to keep first; the export is " +
			"a plain archive with no lock-in.",
		SuggestedCategory: BotCategoryAccount,
		SuggestedPriority: PriorityNormal,
	},
}

// botUrgencyExpressions raise a pre-filled ticket's suggested priority when the
// user's own words signal urgency. Detection is literal keyword matching.
var botUrgencyExpressions = []string{
	`(?i)\b(urgent|asap|emergency|critical)\b`,
	`(?i)\b(production|prod)\s+(is\s+)?(down|outage|broken)\b`,
	`(?i)\b(everything|all sites?|whole (site|server|cluster))\s+(is\s+)?down\b`,
	`(?i)\bdata\s+loss\b`,
}

// BotPatternCount reports how many patterns are compiled into this build. It
// exposes a count only — never the patterns themselves — so that operators can
// confirm the table loaded without any route ever revealing its contents.
func BotPatternCount() int {
	return len(botPatterns)
}
