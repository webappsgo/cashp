package support

import (
	"context"
	"sort"
	"strings"
	"unicode"

	"github.com/webappsgo/cashp/src/errors"
)

// articleTransitions is the knowledge base lifecycle. It is a closed table for
// the same reason the ticket machine is: an article can only ever be in one of
// four states and can only move along an edge listed here.
var articleTransitions = map[string][]string{
	ArticleDraft:     {ArticleReview},
	ArticleReview:    {ArticleDraft, ArticlePublished},
	ArticlePublished: {ArticleArchived},
	ArticleArchived:  {ArticleDraft},
}

// CanPublishArticle reports whether an article may move between two states.
func CanPublishArticle(from, to string) bool {
	for _, allowed := range articleTransitions[from] {
		if allowed == to {
			return true
		}
	}
	return false
}

// ArticleInput is the editable part of an article.
type ArticleInput struct {
	Title      string
	Body       string
	Slug       string
	CategoryID string
	Tags       string
}

// normalize cleans and bounds the submitted fields.
func (in ArticleInput) normalize() ArticleInput {
	in.Title = truncate(clean(in.Title), 200)
	in.Body = truncate(cleanMultiline(in.Body), 200000)
	in.CategoryID = truncate(clean(in.CategoryID), 64)
	in.Tags = truncate(strings.ToLower(clean(in.Tags)), 500)
	in.Slug = Slugify(firstNonEmpty(in.Slug, in.Title))
	return in
}

// Slugify reduces a title to a URL-safe slug. Only unreserved characters
// survive, so a slug can never introduce a path segment or a query string.
func Slugify(text string) string {
	var b strings.Builder
	lastDash := true
	for _, r := range strings.ToLower(text) {
		switch {
		case unicode.IsLetter(r) && r < unicode.MaxASCII, unicode.IsDigit(r):
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	return truncate(strings.Trim(b.String(), "-"), 120)
}

// CreateArticle starts a new article in DRAFT. Drafts belong to the author's
// organization and are never visible to customers.
func (s *Service) CreateArticle(ctx context.Context, id Identity, in ArticleInput) (Article, error) {
	agent, err := s.requireAgent(ctx, id)
	if err != nil {
		return Article{}, err
	}
	in = in.normalize()
	if in.Title == "" || in.Body == "" {
		return Article{}, errors.New(errors.CodeValidation, 400, "A title and body are required").
			WithDetails(map[string]any{"field": "title"})
	}
	if in.Slug == "" {
		return Article{}, errors.New(errors.CodeValidation, 400, "That title cannot be turned into a link").
			WithDetails(map[string]any{"field": "slug"})
	}
	if _, err := s.store.ArticleBySlug(ctx, in.Slug); err == nil {
		return Article{}, errors.New(errors.CodeConflict, 409, "An article already uses that link")
	}

	at := s.nowUnix()
	a := Article{
		ID:         newID("kba"),
		OrgID:      id.OrgID,
		Slug:       in.Slug,
		Title:      in.Title,
		Body:       in.Body,
		CategoryID: in.CategoryID,
		Tags:       in.Tags,
		Status:     ArticleDraft,
		AuthorID:   agent.UserID,
		CreatedAt:  at,
		UpdatedAt:  at,
		Version:    1,
	}
	if err := s.store.InsertArticle(ctx, a); err != nil {
		return Article{}, err
	}
	if err := s.audit(ctx, id, AuditEntry{
		Action:     "kb.create",
		EntityType: "article",
		EntityID:   a.ID,
		ToState:    ArticleDraft,
	}); err != nil {
		return Article{}, err
	}
	return a, nil
}

// EditArticle rewrites an article's content without changing its state.
func (s *Service) EditArticle(ctx context.Context, id Identity, articleID string, in ArticleInput) (Article, error) {
	if _, err := s.requireAgent(ctx, id); err != nil {
		return Article{}, err
	}
	a, err := s.store.Article(ctx, articleID)
	if err != nil {
		return Article{}, err
	}
	in = in.normalize()
	if in.Title == "" || in.Body == "" {
		return Article{}, errors.New(errors.CodeValidation, 400, "A title and body are required").
			WithDetails(map[string]any{"field": "title"})
	}
	if in.Slug != "" && in.Slug != a.Slug {
		if _, err := s.store.ArticleBySlug(ctx, in.Slug); err == nil {
			return Article{}, errors.New(errors.CodeConflict, 409, "An article already uses that link")
		}
		a.Slug = in.Slug
	}

	a.Title = in.Title
	a.Body = in.Body
	a.CategoryID = in.CategoryID
	a.Tags = in.Tags
	a.UpdatedAt = s.nowUnix()
	if err := s.store.UpdateArticle(ctx, a); err != nil {
		return Article{}, err
	}
	a.Version++
	if err := s.audit(ctx, id, AuditEntry{
		Action:     "kb.edit",
		EntityType: "article",
		EntityID:   a.ID,
	}); err != nil {
		return Article{}, err
	}
	if a.Status == ArticlePublished {
		if err := s.RebuildKBIndex(ctx); err != nil {
			return Article{}, err
		}
	}
	return a, nil
}

// TransitionArticle moves an article along its lifecycle. Publishing is an
// administrator's decision: an agent may write and submit for review, but only
// an administrator turns customer-visible content on.
func (s *Service) TransitionArticle(ctx context.Context, id Identity, articleID, to string) (Article, error) {
	if _, err := s.requireAgent(ctx, id); err != nil {
		return Article{}, err
	}
	to = strings.ToUpper(clean(to))
	a, err := s.store.Article(ctx, articleID)
	if err != nil {
		return Article{}, err
	}
	if !CanPublishArticle(a.Status, to) {
		return Article{}, errors.New(errors.CodeConflict, 409, "An article cannot move from "+a.Status+" to "+to)
	}
	if to == ArticlePublished && s.RoleOf(ctx, id) != RoleAdmin {
		return Article{}, errors.New(errors.CodeForbidden, 403, "Publishing an article requires an administrator")
	}

	from := a.Status
	a.Status = to
	a.UpdatedAt = s.nowUnix()
	if to == ArticlePublished {
		a.PublishedAt = a.UpdatedAt
	}
	if err := s.store.UpdateArticle(ctx, a); err != nil {
		return Article{}, err
	}
	a.Version++
	if err := s.audit(ctx, id, AuditEntry{
		Action:     "kb.transition",
		EntityType: "article",
		EntityID:   a.ID,
		FromState:  from,
		ToState:    to,
	}); err != nil {
		return Article{}, err
	}
	// The bot's reading list is refreshed when publication changes, never when
	// a question is asked. Nothing is fetched at request time.
	if err := s.RebuildKBIndex(ctx); err != nil {
		return Article{}, err
	}
	return a, nil
}

// PublicArticle loads one article for a reader. Only published articles are
// reachable by search; an archived article stays reachable by its direct link
// so old references keep working, and drafts are not reachable at all.
func (s *Service) PublicArticle(ctx context.Context, id Identity, slug string) (Article, error) {
	a, err := s.store.ArticleBySlug(ctx, slug)
	if err != nil {
		return Article{}, err
	}
	switch a.Status {
	case ArticlePublished, ArticleArchived:
	default:
		if !s.IsStaff(ctx, id) {
			return Article{}, notFound("Article")
		}
	}
	if err := s.store.BumpArticleCounter(ctx, a.ID, "view"); err != nil {
		return Article{}, err
	}
	a.ViewCount++
	return a, nil
}

// SearchArticles searches the published knowledge base. Guests may read it when
// the installation allows a public knowledge base.
func (s *Service) SearchArticles(ctx context.Context, id Identity, query string, page, limit int) ([]Article, Page, error) {
	if !id.Authenticated && !s.settingBool(ctx, SettingKBPublicEnabled) {
		return nil, Page{}, errors.New(errors.CodeUnauthorized, 401, "Sign in to read the knowledge base")
	}
	return s.store.ListArticles(ctx, ArticleFilter{
		Statuses: []string{ArticlePublished},
		Search:   truncate(clean(query), 120),
		Page:     page,
		Limit:    limit,
	})
}

// StaffArticles lists articles at any stage for support staff.
func (s *Service) StaffArticles(ctx context.Context, id Identity, statuses []string, query string, page, limit int) ([]Article, Page, error) {
	if _, err := s.requireAgent(ctx, id); err != nil {
		return nil, Page{}, err
	}
	for _, st := range statuses {
		switch st {
		case ArticleDraft, ArticleReview, ArticlePublished, ArticleArchived:
		default:
			return nil, Page{}, errors.New(errors.CodeValidation, 400, "Unknown article state").
				WithDetails(map[string]any{"field": "status"})
		}
	}
	return s.store.ListArticles(ctx, ArticleFilter{
		Statuses: statuses,
		Search:   truncate(clean(query), 120),
		Page:     page,
		Limit:    limit,
	})
}

// ArticleFeedback records whether an article helped.
func (s *Service) ArticleFeedback(ctx context.Context, id Identity, slug string, helpful bool) error {
	a, err := s.store.ArticleBySlug(ctx, slug)
	if err != nil {
		return err
	}
	if a.Status != ArticlePublished {
		return notFound("Article")
	}
	counter := "not_helpful"
	if helpful {
		counter = "helpful"
	}
	return s.store.BumpArticleCounter(ctx, a.ID, counter)
}

// kbEntry is one published article reduced to the keywords the bot matches on.
type kbEntry struct {
	slug  string
	title string
	terms map[string]bool
}

// RebuildKBIndex refreshes the bot's reading list from the published articles.
// It runs when publication state changes, never while answering a question.
func (s *Service) RebuildKBIndex(ctx context.Context) error {
	articles, _, err := s.store.ListArticles(ctx, ArticleFilter{
		Statuses: []string{ArticlePublished},
		Limit:    MaxPageLimit,
	})
	if err != nil {
		return err
	}
	index := make([]kbEntry, 0, len(articles))
	for _, a := range articles {
		terms := map[string]bool{}
		for _, term := range kbTerms(a.Title + " " + a.Tags) {
			terms[term] = true
		}
		if len(terms) == 0 {
			continue
		}
		index = append(index, kbEntry{slug: a.Slug, title: a.Title, terms: terms})
	}
	sort.Slice(index, func(i, j int) bool { return index[i].slug < index[j].slug })

	s.kbMu.Lock()
	s.kbIndex = index
	s.kbMu.Unlock()
	return nil
}

// kbStopWords are words too common to identify an article.
var kbStopWords = map[string]bool{
	"the": true, "and": true, "for": true, "with": true, "from": true,
	"how": true, "why": true, "you": true, "your": true, "can": true,
	"not": true, "does": true, "did": true, "was": true, "are": true,
	"this": true, "that": true, "when": true, "what": true, "have": true,
}

// kbTerms splits text into the lowercase words worth indexing.
func kbTerms(text string) []string {
	fields := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	var out []string
	seen := map[string]bool{}
	for _, f := range fields {
		if len(f) < 3 || kbStopWords[f] || seen[f] {
			continue
		}
		seen[f] = true
		out = append(out, f)
	}
	return out
}

// ArticleSuggestion is one piece of suggested reading.
type ArticleSuggestion struct {
	Slug  string
	Title string
	Score int
}

// SuggestArticles returns published articles whose indexed keywords appear in
// the user's own words. The match is a plain set intersection over a prebuilt
// index: the same question always returns the same list, in the same order.
func (s *Service) SuggestArticles(text string, max int) []ArticleSuggestion {
	terms := kbTerms(text)
	if len(terms) == 0 || max <= 0 {
		return nil
	}
	asked := make(map[string]bool, len(terms))
	for _, t := range terms {
		asked[t] = true
	}

	s.kbMu.RLock()
	index := s.kbIndex
	s.kbMu.RUnlock()

	var out []ArticleSuggestion
	for _, entry := range index {
		score := 0
		for term := range entry.terms {
			if asked[term] {
				score++
			}
		}
		if score < 2 {
			continue
		}
		out = append(out, ArticleSuggestion{Slug: entry.slug, Title: entry.title, Score: score})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].Slug < out[j].Slug
	})
	if len(out) > max {
		out = out[:max]
	}
	return out
}
