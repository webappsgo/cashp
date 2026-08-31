package web

// This file defines the data shapes the shared UI components render. Feature
// packages build these values and hand them to the matching template instead of
// writing their own markup, so tables, meters, logs and empty states look and
// behave the same everywhere in the panel.

// Cell is one value in a resource row.
type Cell struct {
	// Value is the rendered text.
	Value string
	// Mono renders the value in the monospace, break-all style used for IP
	// addresses, hashes, tokens and onion addresses.
	Mono bool
}

// Action is a link or form button offered for a resource row.
type Action struct {
	Label string
	Href  string
	// Method is GET for a plain link, or POST for a form submit.
	Method string
	// Variant is a button modifier: primary, secondary or danger.
	Variant string
	// ConfirmID is the id of a confirm dialog that must be acknowledged before
	// the action runs. Empty means the action runs immediately.
	ConfirmID string
}

// ResourceRow is a single item in a resource list, such as a site, container,
// virtual machine, mailbox or DNS zone.
type ResourceRow struct {
	Name string
	// Href links to the resource detail page; empty renders plain text.
	Href string
	// Status feeds the status badge, for example running or stopped.
	Status string
	// Detail is the secondary line under the name.
	Detail  string
	Cells   []Cell
	Actions []Action
}

// EmptyState is what a list renders when it has nothing to show. Every empty
// state must explain what the list will contain and how to fill it.
type EmptyState struct {
	Icon        string
	Title       string
	Message     string
	ActionLabel string
	ActionHref  string
}

// ResourceList is the full payload for the resource table component.
type ResourceList struct {
	Caption string
	// Columns are the headers after the leading name/status columns.
	Columns []string
	Rows    []ResourceRow
	Empty   EmptyState
	// Loading renders the skeleton state instead of rows, for a page that is
	// rendered before a slow backend has answered.
	Loading bool
	// Error renders a failure state instead of rows.
	Error string
	// CSRFToken is required when any action uses the POST method.
	CSRFToken string
}

// UsageMeter shows consumption against a quota, such as disk, memory, bandwidth
// or the number of sites allowed by a plan.
type UsageMeter struct {
	Label string
	Used  float64
	Total float64
	// Unit is appended to the numbers, for example "GB" or "sites".
	Unit string
	// Note is optional supporting text under the bar.
	Note string
}

// UsedPercent returns the filled portion of the meter, clamped to 0-100.
func (u UsageMeter) UsedPercent() int {
	return percent(u.Used, u.Total)
}

// Level classifies the meter so the bar can be coloured and labelled.
func (u UsageMeter) Level() string {
	switch value := u.UsedPercent(); {
	case value >= 90:
		return "critical"
	case value >= 75:
		return "warning"
	default:
		return "normal"
	}
}

// LogOutput is a block of console or log lines, for example a deploy log, a
// container log tail or the output of a backup run.
type LogOutput struct {
	Title string
	// Source names where the lines came from, shown next to the title.
	Source string
	Lines  []string
	// Empty is the message shown when there is no output yet.
	Empty string
}

// FormStep is one step of a multi-step form such as the create-site or
// create-virtual-machine wizard.
type FormStep struct {
	Number int
	Title  string
	// Complete marks a step the user already finished.
	Complete bool
	// Current marks the step being filled in.
	Current bool
	Href    string
}

// ConfirmDialog is a destructive-action confirmation rendered as a native
// <dialog>. The form inside it posts to Action, so the confirmation still works
// when the dialog element is unsupported and the browser shows it inline.
type ConfirmDialog struct {
	ID      string
	Title   string
	Message string
	// ConfirmWord, when set, requires the user to type the resource name before
	// the confirm button submits, matching the delete flows for sites and
	// virtual machines.
	ConfirmWord  string
	ConfirmLabel string
	CancelLabel  string
	Action       string
	CSRFToken    string
}
