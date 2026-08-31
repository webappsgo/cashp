// Package support implements cashp's customer support subsystem: tenant-scoped
// ticketing with a nine-state lifecycle, a deterministic help bot, a knowledge
// base, live chat, canned responses, SLA tracking, and the agent workspace.
//
// Two rules govern the whole package. First, there is no AI or ML anywhere in
// the production path: the bot matches compiled-in regular expressions and
// nothing else. Second, every read and every write is scoped to the caller's
// organization, so no query in this package may run without an org filter.
package support

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
	"time"

	"github.com/webappsgo/cashp/src/security"
)

// Support roles derived from the identity the host application supplies. cashp
// has three account roles (global admin, account admin, end user); the support
// role is derived from those plus the support_agents table rather than being a
// second, competing hierarchy stored on the user record.
const (
	// RoleGuest is an unauthenticated visitor. Guests may read published
	// knowledge base articles and nothing else.
	RoleGuest = "GUEST"
	// RoleUser is any authenticated account holder.
	RoleUser = "REGISTERED_USER"
	// RoleAgent is an account with an enabled support_agents row.
	RoleAgent = "SUPPORT_AGENT"
	// RoleAdmin is a global administrator of the installation.
	RoleAdmin = "SYSTEM_ADMINISTRATOR"
)

// Agent availability states. Availability is derived by the service from
// activity and chat load; it is never set directly by a form field.
const (
	AvailabilityAvailable = "AVAILABLE"
	AvailabilityBusy      = "BUSY"
	AvailabilityAway      = "AWAY"
	AvailabilityOffline   = "OFFLINE"
)

// Knowledge base article lifecycle states.
const (
	ArticleDraft     = "DRAFT"
	ArticleReview    = "REVIEW"
	ArticlePublished = "PUBLISHED"
	ArticleArchived  = "ARCHIVED"
)

// Live chat session states.
const (
	ChatQueued    = "QUEUED"
	ChatActive    = "ACTIVE"
	ChatClosed    = "CLOSED"
	ChatEscalated = "ESCALATED"
	ChatAbandoned = "ABANDONED"
)

// Canned response scopes, most general first. An agent sees SYSTEM responses,
// the DEPARTMENT responses of their own department, and their own PERSONAL
// responses — never another agent's personal responses.
const (
	CannedSystem     = "SYSTEM"
	CannedDepartment = "DEPARTMENT"
	CannedPersonal   = "PERSONAL"
)

// AwayAfter is how long an agent may be idle before availability falls back to
// AWAY. Any agent action resets the timer.
const AwayAfter = 15 * time.Minute

// AutoCloseAfter is how long a RESOLVED ticket stands before the scheduler
// closes it on the user's behalf.
const AutoCloseAfter = 72 * time.Hour

// DraftAutosaveSeconds is the interval the ticket form autosaves at. It is
// surfaced to the template so the markup and the server agree on one number.
const DraftAutosaveSeconds = 30

// BotMaxAttempts is how many times the bot may fail to understand a user
// before it stops trying and offers a pre-filled ticket instead.
const BotMaxAttempts = 3

// Identity is the caller as the host application sees them. The support
// package never reads session cookies or tokens itself: src/server resolves
// the caller and hands the result to Service.Identity.
type Identity struct {
	// Authenticated is false for guests.
	Authenticated bool
	// UserID is the account id; zero for guests.
	UserID int64
	// OrgID is the organization the request is scoped to; zero for guests.
	OrgID int64
	// DisplayName is the account's own display name. It is used for the
	// ticket author, never for an agent byline.
	DisplayName string
	// GlobalAdmin marks an installation-wide administrator.
	GlobalAdmin bool
	// OrgAdmin marks an administrator of OrgID.
	OrgAdmin bool
	// SessionID identifies the browser session and keys CSRF tokens and the
	// in-memory support-mode state.
	SessionID string
	// RemoteKey identifies the caller for rate limiting; normally the client
	// IP as the server resolved it behind any trusted proxy.
	RemoteKey string
}

// BotPattern is one compiled-in rule of the deterministic bot. The struct is
// populated only by the generated table in bot_patterns.go; nothing reads a
// pattern from the database, a file, or the network.
type BotPattern struct {
	// ID uniquely names the rule and orders ties deterministically.
	ID string
	// Category is the bot category the rule belongs to.
	Category string
	// Expressions are Go regular expressions; a rule matches when at least
	// one of them matches the user's text.
	Expressions []string
	// Response is the fixed answer text. It is plain text and is escaped on
	// output like any other untrusted string.
	Response string
	// SuggestedCategory pre-fills the ticket form when the rule did not
	// resolve the user's problem.
	SuggestedCategory string
	// SuggestedPriority pre-fills the ticket priority.
	SuggestedPriority string
}

// Agent is a support agent profile. The presence of an enabled row here is
// what makes an account a support agent.
type Agent struct {
	ID                 string
	UserID             int64
	DisplayName        string
	DepartmentID       string
	MaxConcurrentChats int
	Enabled            bool
	LastActivityAt     int64
	CreatedAt          int64
	UpdatedAt          int64
}

// Department groups agents for routing and for department-scoped canned
// responses.
type Department struct {
	ID          string
	Name        string
	Description string
	Enabled     bool
	CreatedAt   int64
	UpdatedAt   int64
}

// Category is a node of the ticket category tree.
type Category struct {
	ID       string
	ParentID string
	Name     string
	Slug     string
	Position int
	Enabled  bool
}

// Ticket is one support request. OrgID scopes it: a ticket is only ever
// readable or writable by members of the organization that raised it, plus
// support staff acting in support mode.
type Ticket struct {
	ID              string
	OrgID           int64
	Number          string
	Title           string
	Description     string
	CategoryID      string
	Priority        string
	Status          string
	UserID          int64
	AssignedTo      int64
	BotContext      string
	SLAPolicyID     string
	FirstResponseAt int64
	ResolvedAt      int64
	ClosedAt        int64
	CreatedAt       int64
	UpdatedAt       int64
	Version         int64
}

// TicketMessage is a reply on a ticket. Internal notes share the table and are
// filtered out of every user-facing query by the Internal flag.
type TicketMessage struct {
	ID         string
	TicketID   string
	OrgID      int64
	AuthorID   int64
	AuthorRole string
	AuthorName string
	Body       string
	Internal   bool
	CreatedAt  int64
}

// Attachment is a file attached to a ticket. StoredName is a generated name
// under the configured attachment directory; the user's original filename is
// kept for display only and never used to build a path.
type Attachment struct {
	ID           string
	TicketID     string
	OrgID        int64
	MessageID    string
	OriginalName string
	StoredName   string
	ContentType  string
	SizeBytes    int64
	UploadedBy   int64
	CreatedAt    int64
}

// Assignment records one ticket-to-agent assignment change.
type Assignment struct {
	ID          string
	TicketID    string
	OrgID       int64
	FromAgentID int64
	ToAgentID   int64
	ActorID     int64
	Reason      string
	CreatedAt   int64
}

// AuditEntry is one append-only support audit record. Rows are inserted and
// never updated or deleted.
type AuditEntry struct {
	ID          string
	OrgID       int64
	ActorID     int64
	OnBehalfOf  int64
	Action      string
	EntityType  string
	EntityID    string
	FromState   string
	ToState     string
	Detail      string
	SupportMode bool
	CreatedAt   int64
}

// SLAPolicy is the response and resolution allowance for one priority.
type SLAPolicy struct {
	ID                string
	Priority          string
	FirstResponseMins int
	ResolutionMins    int
	EscalatePercent   int
	Enabled           bool
	UpdatedAt         int64
}

// Article is a knowledge base article.
type Article struct {
	ID            string
	OrgID         int64
	Slug          string
	Title         string
	Body          string
	CategoryID    string
	Tags          string
	Status        string
	AuthorID      int64
	HelpfulCount  int64
	NotHelpfulCnt int64
	ViewCount     int64
	PublishedAt   int64
	CreatedAt     int64
	UpdatedAt     int64
	Version       int64
}

// ChatSession is one live chat conversation.
type ChatSession struct {
	ID          string
	OrgID       int64
	UserID      int64
	AgentID     int64
	Status      string
	Subject     string
	TicketID    string
	QueuedAt    int64
	StartedAt   int64
	EndedAt     int64
	Rating      int
	LastEventAt int64
}

// ChatMessage is one message inside a chat session.
type ChatMessage struct {
	ID         string
	SessionID  string
	OrgID      int64
	AuthorID   int64
	AuthorRole string
	AuthorName string
	Body       string
	CreatedAt  int64
}

// CannedResponse is a reusable agent reply in one of the three scopes.
type CannedResponse struct {
	ID           string
	Scope        string
	DepartmentID string
	AgentUserID  int64
	Title        string
	Body         string
	Tags         string
	UsageCount   int64
	CreatedAt    int64
	UpdatedAt    int64
}

// Setting is one row of the support configuration store. Support settings live
// here and are edited through the admin UI; the package never reads an
// environment variable for configuration.
type Setting struct {
	Key       string
	Value     string
	UpdatedAt int64
}

// Page describes one page of a listed collection.
type Page struct {
	Page  int
	Limit int
	Total int
	Pages int
}

// DefaultPageLimit is the project-wide default page size.
const DefaultPageLimit = 250

// MaxPageLimit caps a caller-supplied page size so a single request cannot ask
// for an unbounded result set.
const MaxPageLimit = 1000

// newPage normalizes a requested page and limit and computes the page count.
func newPage(page, limit, total int) Page {
	if page < 1 {
		page = 1
	}
	if limit < 1 {
		limit = DefaultPageLimit
	}
	if limit > MaxPageLimit {
		limit = MaxPageLimit
	}
	pages := 0
	if total > 0 {
		pages = (total + limit - 1) / limit
	}
	return Page{Page: page, Limit: limit, Total: total, Pages: pages}
}

// offsetFor converts a page and limit into a SQL offset.
func offsetFor(p Page) int {
	return (p.Page - 1) * p.Limit
}

// newID returns a random, unguessable identifier with a short type prefix.
// Identifiers are opaque: nothing derives meaning from their contents.
func newID(prefix string) string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		// A failing system CSPRNG is not recoverable here, and a predictable
		// identifier would be worse than a duplicate, so fall back to a
		// time-derived value that the unique index still guards.
		return prefix + "_" + hex.EncodeToString([]byte(time.Now().UTC().Format(time.RFC3339Nano)))
	}
	return prefix + "_" + hex.EncodeToString(buf)
}

// clean normalizes an untrusted single-line string: control characters are
// stripped and surrounding whitespace is removed. It is applied on the way in;
// escaping still happens on the way out.
func clean(s string) string {
	return strings.TrimSpace(security.StripControlChars(s))
}

// cleanMultiline normalizes an untrusted multi-line string, preserving line
// breaks while removing every other control character.
func cleanMultiline(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(security.StripControlChars(line), " \t")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// truncate limits a string to n runes so that an oversized field cannot bloat a
// row or a rendered page.
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// now returns the current wall clock in Unix seconds.
func now() int64 {
	return time.Now().UTC().Unix()
}
