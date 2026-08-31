package support

// This file holds the page models. Every support page receives exactly one of
// these in View.Data, so a template never reaches into the service and a
// handler never hands a template a bare map whose shape nothing checks.

// botData drives the help page and the bot conversation.
type botData struct {
	Session   BotSession
	Turns     []BotTurn
	Reply     BotReply
	Articles  []ArticleSuggestion
	Greeting  string
	Answered  bool
	Escalated bool
	DraftID   string
}

// formData drives the ticket form. The form is always pre-filled from a draft
// the bot created; every field remains editable and nothing is submitted for
// the user.
type formData struct {
	Ticket     Ticket
	Categories []Category
	Priorities []string
	Autosave   int
}

// listData drives the user's own ticket list.
type listData struct {
	Tickets []Ticket
	Page    Page
	Now     int64
}

// ticketData drives one ticket as its owner sees it. Internal notes never
// reach this struct: they are filtered out in the service layer.
type ticketData struct {
	Ticket      Ticket
	Messages    []TicketMessage
	Attachments []Attachment
	Now         int64
	MaxKB       int
}

// chatData drives the customer side of live chat. The page works without
// JavaScript: sending a message posts a form and the server redirects back to
// the same page, which then shows the new message.
type chatData struct {
	Availability ChatAvailability
	Session      ChatSession
	Messages     []ChatMessage
	Position     int
	Active       bool
	Now          int64
}

// kbIndexData drives knowledge base search.
type kbIndexData struct {
	Query    string
	Articles []Article
	Page     Page
}

// kbArticleData drives one published article.
type kbArticleData struct {
	Article Article
}

// queueData drives the agent queue.
type queueData struct {
	Items   []QueueItem
	Page    Page
	Metrics AgentMetrics
	Roster  []AgentPresence
	Now     int64
}

// agentTicketData drives one ticket inside the agent workspace.
type agentTicketData struct {
	Ticket      Ticket
	Messages    []TicketMessage
	Attachments []Attachment
	Audit       []AuditEntry
	Risk        SLARisk
	Canned      []CannedResponse
	Suggested   []CannedSuggestion
	NextStates  []string
	Roster      []AgentPresence
	Now         int64
}

// agentChatsData drives the agent chat workspace, listing the waiting queue
// and, when one is open, the conversation itself.
type agentChatsData struct {
	Sessions []ChatSession
	Session  ChatSession
	Messages []ChatMessage
	Open     bool
	Now      int64
}

// agentKBData drives the knowledge base editor.
type agentKBData struct {
	Articles []Article
	Page     Page
	Article  Article
	Editing  bool
	CanPub   bool
	Statuses []string
	Canned   []CannedResponse
}

// adminData drives the support settings page.
type adminData struct {
	Settings    []Setting
	Policies    []SLAPolicy
	Departments []Department
	Categories  []Category
	Agents      []Agent
	Canned      []CannedResponse
	Report      Report
	ReportName  string
	ReportNames []string
}

// ticketPriorities is the fixed priority list offered by every form.
var ticketPriorities = []string{PriorityUrgent, PriorityHigh, PriorityNormal, PriorityLow}

// articleStatuses is the fixed article lifecycle list offered by the editor.
var articleStatuses = []string{ArticleDraft, ArticleReview, ArticlePublished, ArticleArchived}

// reportNames are the reports the admin page can render.
var reportNames = []string{"volume", "categories", "priorities", "sla", "satisfaction", "agents"}
