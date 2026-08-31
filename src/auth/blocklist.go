package auth

import "strings"

// UsernameBlocklist is the reserved-name list from AI.md PART 34 "Username Blocklist".
// Users and orgs share one namespace, so this list guards both usernames and org slugs.
// Server Admin accounts are exempt from this list, including the primary global admin.
var UsernameBlocklist = []string{
	"admin", "administrator", "root", "system", "sysadmin", "superuser",
	"master", "owner", "operator", "manager", "moderator", "mod",
	"staff", "support", "helpdesk", "help", "service", "daemon",

	"server", "host", "node", "cluster", "api", "www", "web", "mail",
	"email", "smtp", "ftp", "ssh", "dns", "proxy", "gateway", "router",
	"firewall", "localhost", "local", "internal", "external", "public",
	"private", "network", "database", "db", "cache", "redis", "mysql",
	"postgres", "mongodb", "elastic", "nginx", "apache", "docker",
	"healthz", "metrics", "swagger",

	"app", "application", "bot", "robot", "crawler", "spider", "scraper",
	"webhook", "callback", "cron", "scheduler", "worker", "queue", "job",
	"task", "process", "microservice", "lambda", "function",

	"auth", "authentication", "login", "logout", "signin", "signout",
	"signup", "register", "password", "passwd", "token", "oauth", "sso",
	"saml", "ldap", "kerberos", "security", "secure", "ssl", "tls",
	"certificate", "cert", "key", "secret", "credential", "session",

	"guest", "anonymous", "anon", "user", "users", "member", "members",
	"subscriber", "editor", "author", "contributor", "reviewer", "auditor",
	"analyst", "developer", "dev", "devops", "engineer", "architect",
	"designer", "tester", "qa", "billing", "finance", "legal", "hr",
	"sales", "marketing", "ceo", "cto", "cfo", "coo", "founder", "cofounder",

	"account", "accounts", "profile", "profiles", "settings", "config",
	"configuration", "dashboard", "panel", "console", "portal", "home",
	"index", "main", "default", "null", "nil", "undefined", "void",
	"true", "false", "test", "testing", "debug", "demo", "example",
	"sample", "temp", "temporary", "tmp", "backup", "archive", "log",
	"logs", "audit", "report", "reports", "analytics", "stats", "status",
	"about", "contact", "privacy", "terms", "docs",

	"rest", "graphql", "grpc", "websocket", "ws", "wss", "http",
	"https", "endpoint", "endpoints", "route", "routes", "path", "url",
	"uri", "hook", "hooks", "event", "events", "stream",
	"autodiscover",

	"blog", "news", "article", "articles", "post", "posts", "page", "pages",
	"feed", "rss", "atom", "sitemap", "robots", "favicon", "static",
	"assets", "images", "image", "img", "media", "upload", "uploads",
	"download", "downloads", "file", "files", "document", "documents",

	"message", "messages", "chat", "notification", "notifications",
	"alert", "alerts", "inbox", "outbox", "sent", "draft", "drafts",
	"spam", "abuse", "flag", "block", "mute", "ban",

	"shop", "store", "cart", "checkout", "order", "orders", "invoice",
	"invoices", "payment", "payments", "subscription", "subscriptions",
	"plan", "plans", "pricing", "refund", "coupon", "discount",

	"follow", "follower", "followers", "following", "friend", "friends",
	"like", "likes", "share", "shares", "comment", "comments", "reply",
	"mention", "mentions", "tag", "tags", "group", "groups", "team", "teams",
	"community", "communities", "forum", "forums", "channel", "channels",

	"official", "verified", "trusted", "partner", "affiliate", "sponsor",
	"brand", "trademark", "copyright", "policy", "policies",
	"tos", "eula", "gdpr", "dmca",

	"fuck", "shit", "ass", "bitch", "bastard", "damn", "cunt", "dick",
	"penis", "vagina", "sex", "porn", "xxx", "nude", "naked", "nsfw",
	"kill", "murder", "death", "die", "suicide", "hate", "nazi", "hitler",
	"racist", "racism", "terrorist", "terrorism", "isis", "alqaeda",

	"0", "1", "123", "1234", "12345", "000", "111", "666", "911", "420", "69",

	"info", "noreply", "no-reply", "donotreply", "mailer", "postmaster",
	"webmaster", "hostmaster", "junk", "trash",

	"cashp", "webappsgo",
}

// substringBlocked lists the critical terms that are also rejected when they appear
// anywhere inside a candidate name, not only as an exact match (AI.md PART 34 "Blocklist Notes").
var substringBlocked = []string{
	"admin", "root", "system", "mod", "official", "verified",
}

// blocklistSet is the exact-match index built once at package init.
var blocklistSet = func() map[string]struct{} {
	m := make(map[string]struct{}, len(UsernameBlocklist))
	for _, n := range UsernameBlocklist {
		m[strings.ToLower(n)] = struct{}{}
	}
	return m
}()

// extraBlocklist holds operator-supplied additions loaded from configuration.
var extraBlocklist = map[string]struct{}{}

// AddBlocklistEntries registers extra reserved names from config.
// Entries are matched case-insensitively as exact names.
func AddBlocklistEntries(names []string) {
	for _, n := range names {
		n = strings.ToLower(strings.TrimSpace(n))
		if n != "" {
			extraBlocklist[n] = struct{}{}
		}
	}
}

// IsBlockedName reports whether name is reserved and may never be registered.
// Matching is case-insensitive; the critical terms in substringBlocked also match as substrings.
func IsBlockedName(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return true
	}
	if _, ok := blocklistSet[name]; ok {
		return true
	}
	if _, ok := extraBlocklist[name]; ok {
		return true
	}
	for _, term := range substringBlocked {
		if strings.Contains(name, term) {
			return true
		}
	}
	return false
}

// BlockedTermIn returns the blocked term that caused name to be rejected, or "".
// Used only for the specific "reserved" validation message, which is safe to reveal.
func BlockedTermIn(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if _, ok := blocklistSet[name]; ok {
		return name
	}
	if _, ok := extraBlocklist[name]; ok {
		return name
	}
	for _, term := range substringBlocked {
		if strings.Contains(name, term) {
			return term
		}
	}
	return ""
}
