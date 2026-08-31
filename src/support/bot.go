package support

import (
	"regexp"
	"sort"
	"strings"
	"sync"
)

// The deterministic help bot.
//
// The bot is a regular-expression matcher over the table compiled into
// bot_patterns.go. It performs no inference, consults no model, makes no
// network call, and reads nothing from the database except the user's own
// conversation. It answers only when exactly one rule wins outright, and it
// never writes a ticket: the most it does is hand the ticket form a set of
// suggested values the user is free to overwrite before submitting.

// compiledPattern is a pattern with its expressions compiled once at startup.
type compiledPattern struct {
	pattern BotPattern
	res     []*regexp.Regexp
}

var (
	botOnce      sync.Once
	botCompiled  []compiledPattern
	botUrgencyRE []*regexp.Regexp
)

// compileBot compiles the pattern table exactly once. An expression that fails
// to compile is skipped rather than panicking, so one bad generated rule cannot
// take the panel down; the remaining rules keep working.
func compileBot() {
	botOnce.Do(func() {
		botCompiled = make([]compiledPattern, 0, len(botPatterns))
		for _, p := range botPatterns {
			cp := compiledPattern{pattern: p}
			for _, expr := range p.Expressions {
				re, err := regexp.Compile(expr)
				if err != nil {
					continue
				}
				cp.res = append(cp.res, re)
			}
			if len(cp.res) == 0 {
				continue
			}
			botCompiled = append(botCompiled, cp)
		}
		sort.Slice(botCompiled, func(i, j int) bool {
			return botCompiled[i].pattern.ID < botCompiled[j].pattern.ID
		})
		for _, expr := range botUrgencyExpressions {
			re, err := regexp.Compile(expr)
			if err != nil {
				continue
			}
			botUrgencyRE = append(botUrgencyRE, re)
		}
	})
}

// BotReply is the bot's answer to one user message.
type BotReply struct {
	// Matched is true when exactly one rule won and Answer is that rule's text.
	Matched bool
	// Answer is the rule's fixed response, or the clarification prompt.
	Answer string
	// PatternID names the rule that answered, for the audit trail.
	PatternID string
	// Attempts is how many unmatched messages the session has now seen.
	Attempts int
	// Exhausted is true once the bot has failed BotMaxAttempts times and is
	// handing the conversation to a human.
	Exhausted bool
	// Prefill carries the suggested ticket values. It is a suggestion only:
	// the user may change every field, and nothing is stored until they submit.
	Prefill TicketPrefill
}

// TicketPrefill is the set of values the bot proposes for the ticket form.
type TicketPrefill struct {
	Title       string
	Description string
	Category    string
	Priority    string
}

// clarifyMessage is what the bot says when it has no unambiguous answer.
const clarifyMessage = "I'm not sure I understand. Could you describe the issue differently — " +
	"including any exact error text you saw?"

// escalateMessage is what the bot says once it gives up.
const escalateMessage = "Let me connect you with a human agent. I've started a ticket with what " +
	"you told me — change anything that isn't right, then submit it."

// MatchBot scores the compiled table against one user message and returns the
// reply. Scoring is fully deterministic: a rule's score is the number of its
// expressions that match, ties at the top score are treated as ambiguous, and
// the table is walked in a fixed order so the same input always produces the
// same output.
func MatchBot(text string) (BotPattern, bool) {
	compileBot()

	probe := strings.TrimSpace(text)
	if probe == "" {
		return BotPattern{}, false
	}

	best := -1
	bestScore := 0
	tied := false
	for i, cp := range botCompiled {
		score := 0
		for _, re := range cp.res {
			if re.MatchString(probe) {
				score++
			}
		}
		if score == 0 {
			continue
		}
		switch {
		case score > bestScore:
			bestScore, best, tied = score, i, false
		case score == bestScore:
			tied = true
		}
	}
	if best < 0 || tied {
		return BotPattern{}, false
	}
	return botCompiled[best].pattern, true
}

// botUrgent reports whether the user's own words signal urgency.
func botUrgent(text string) bool {
	compileBot()
	for _, re := range botUrgencyRE {
		if re.MatchString(text) {
			return true
		}
	}
	return false
}

// prefillFrom builds the ticket suggestion from the user's message and the
// last rule that came close, raising the priority when the wording is urgent.
func prefillFrom(text, category, priority string) TicketPrefill {
	body := cleanMultiline(text)
	title := body
	if idx := strings.IndexByte(title, '\n'); idx >= 0 {
		title = title[:idx]
	}
	title = truncate(strings.TrimSpace(title), 120)
	if title == "" {
		title = "Support request"
	}
	if !IsPriority(priority) {
		priority = PriorityNormal
	}
	if botUrgent(text) && PriorityRank(priority) < PriorityRank(PriorityHigh) {
		priority = PriorityHigh
	}
	return TicketPrefill{
		Title:       title,
		Description: truncate(body, 8000),
		Category:    category,
		Priority:    priority,
	}
}

// AskBot advances a bot conversation by one user message and returns the reply
// together with the updated session. The session is the caller's to persist;
// this function performs no I/O of any kind.
func AskBot(session BotSession, text string) (BotSession, BotReply) {
	message := cleanMultiline(text)
	session.Transcript = appendTranscript(session.Transcript, "user", message)

	pattern, ok := MatchBot(message)
	if ok {
		session.LastCategory = pattern.SuggestedCategory
		session.LastPriority = pattern.SuggestedPriority
		session.Transcript = appendTranscript(session.Transcript, "bot", pattern.Response)
		return session, BotReply{
			Matched:   true,
			Answer:    pattern.Response,
			PatternID: pattern.ID,
			Attempts:  session.Attempts,
			Prefill:   prefillFrom(message, pattern.SuggestedCategory, pattern.SuggestedPriority),
		}
	}

	session.Attempts++
	exhausted := session.Attempts >= BotMaxAttempts
	answer := clarifyMessage
	if exhausted {
		answer = escalateMessage
	}
	session.Transcript = appendTranscript(session.Transcript, "bot", answer)

	return session, BotReply{
		Matched:   false,
		Answer:    answer,
		Attempts:  session.Attempts,
		Exhausted: exhausted,
		Prefill:   prefillFrom(message, session.LastCategory, session.LastPriority),
	}
}

// appendTranscript appends one turn to a conversation transcript. The
// transcript is plain text with one turn per line and is escaped on output like
// any other untrusted content.
func appendTranscript(transcript, speaker, text string) string {
	line := speaker + ": " + strings.ReplaceAll(cleanMultiline(text), "\n", " ")
	if transcript == "" {
		return truncate(line, 16000)
	}
	return truncate(transcript+"\n"+line, 16000)
}

// TranscriptLines splits a stored transcript into speaker and text pairs for
// rendering.
func TranscriptLines(transcript string) []BotTurn {
	if transcript == "" {
		return nil
	}
	var out []BotTurn
	for _, line := range strings.Split(transcript, "\n") {
		speaker, text, found := strings.Cut(line, ": ")
		if !found {
			out = append(out, BotTurn{Speaker: "bot", Text: line})
			continue
		}
		out = append(out, BotTurn{Speaker: speaker, Text: text})
	}
	return out
}

// BotTurn is one rendered line of a bot transcript.
type BotTurn struct {
	Speaker string
	Text    string
}
