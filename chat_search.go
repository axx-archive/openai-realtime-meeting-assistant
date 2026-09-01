package main

// GET /assistant/chat-search (STRIDE v2.0 Wave 1, D8): case-insensitive
// substring search over the message text of every unarchived, non-Riff thread
// the viewer can already read. Scope is exactly the sidebar scope —
// scoutChatThreadsSnapshot applies scoutChatThreadAllowsViewer, so a private
// thread owned by someone else, or a human group the viewer is not in, is
// never scanned. Candidate threads are found on the raw record, but final
// hits come from the viewer projection so pending deletes, Riff redactions,
// and Meeting Record revision fences apply exactly as they do to the open
// conversation.

import (
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	chatSearchMinQueryRunes  = 2
	chatSearchMaxQueryRunes  = 200
	chatSearchDefaultLimit   = 20
	chatSearchMaxLimit       = 50
	chatSearchSnippetContext = 60
)

type chatSearchResult struct {
	ThreadID         string `json:"threadId"`
	ThreadTitle      string `json:"threadTitle"`
	ConversationKind string `json:"conversationKind"`
	Visibility       string `json:"visibility"`
	MessageID        string `json:"messageId"`
	AuthorName       string `json:"authorName"`
	AuthorEmail      string `json:"authorEmail"`
	CreatedAt        string `json:"createdAt"`
	Snippet          string `json:"snippet"`
}

// clampQueryLimit parses a positive integer query parameter, falling back to
// def when absent or invalid and never exceeding max.
func clampQueryLimit(raw string, def, max int) int {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value <= 0 {
		return def
	}
	if value > max {
		return max
	}
	return value
}

func chatSearchIsASCII(value string) bool {
	for index := 0; index < len(value); index++ {
		if value[index] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

// chatSearchFold lower-cases rune by rune. unicode.ToLower is a 1:1 rune
// mapping, so a byte index into the folded string converts to a rune index
// that is valid in the original text — strings.ToLower is not (it can change
// rune counts), which is why it is only used on the ASCII fast path, where
// the two agree and bytes are runes.
func chatSearchFold(value string) string {
	if chatSearchIsASCII(value) {
		return strings.ToLower(value)
	}
	runes := []rune(value)
	for index, r := range runes {
		runes[index] = unicode.ToLower(r)
	}
	return string(runes)
}

// chatSearchMatch returns the rune index of the first case-insensitive match
// of foldedNeedle (already passed through chatSearchFold) inside text. On the
// common ASCII path no rune slice is allocated and the byte index is the rune
// index.
func chatSearchMatch(text string, foldedNeedle string) (int, bool) {
	if chatSearchIsASCII(text) {
		if !chatSearchIsASCII(foldedNeedle) {
			return 0, false
		}
		index := strings.Index(strings.ToLower(text), foldedNeedle)
		if index < 0 {
			return 0, false
		}
		return index, true
	}
	folded := chatSearchFold(text)
	index := strings.Index(folded, foldedNeedle)
	if index < 0 {
		return 0, false
	}
	return utf8.RuneCountInString(folded[:index]), true
}

// chatSearchSnippet keeps up to chatSearchSnippetContext runes either side of
// the match, collapses whitespace to one line, and ellipsizes trimmed ends.
// Plain text only — the client highlights the query itself.
func chatSearchSnippet(text string, start int, needleRunes int) string {
	runes := []rune(text)
	from := start - chatSearchSnippetContext
	if from < 0 {
		from = 0
	}
	to := start + needleRunes + chatSearchSnippetContext
	if to > len(runes) {
		to = len(runes)
	}
	snippet := strings.Join(strings.Fields(string(runes[from:to])), " ")
	if from > 0 {
		snippet = "…" + snippet
	}
	if to < len(runes) {
		snippet += "…"
	}
	return snippet
}

// chatSearchMessageSearchable keeps human and Scout conversation turns only.
// Card kinds (proposal, choices, manifest, image, thread refs) are projections
// of work state, not things a person said, and are skipped.
func chatSearchMessageSearchable(message scoutChatMessageRecord) bool {
	if strings.TrimSpace(message.Text) == "" {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(message.Kind)) {
	case "", "message":
	default:
		return false
	}
	switch strings.ToLower(strings.TrimSpace(message.Role)) {
	case "system", "tool":
		return false
	}
	return true
}

func chatSearchAuthorName(message scoutChatMessageRecord) string {
	if name := strings.TrimSpace(message.AuthorName); name != "" {
		return name
	}
	switch strings.ToLower(strings.TrimSpace(message.Role)) {
	case "scout", "assistant":
		return scoutParticipantName
	}
	if email := normalizeAccountEmail(message.AuthorEmail); email != "" {
		if user := accountStore().findUser(email); user != nil {
			return accountDisplayName(user)
		}
	}
	return ""
}

func chatSearchTime(value string) time.Time {
	parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(value))
	if err != nil {
		return time.Time{}
	}
	return parsed
}

// chatSearchHits is a bounded newest-first set: at most limit entries, each
// with its CreatedAt parsed once at insertion.
type chatSearchHits struct {
	limit   int
	results []chatSearchResult
	times   []time.Time
}

func (hits *chatSearchHits) add(result chatSearchResult, at time.Time) {
	if hits.limit <= 0 {
		return
	}
	if len(hits.results) == hits.limit && !at.After(hits.times[len(hits.times)-1]) {
		return
	}
	position := len(hits.results)
	for position > 0 && at.After(hits.times[position-1]) {
		position--
	}
	hits.results = append(hits.results, chatSearchResult{})
	hits.times = append(hits.times, time.Time{})
	copy(hits.results[position+1:], hits.results[position:])
	copy(hits.times[position+1:], hits.times[position:])
	hits.results[position] = result
	hits.times[position] = at
	if len(hits.results) > hits.limit {
		hits.results = hits.results[:hits.limit]
		hits.times = hits.times[:hits.limit]
	}
}

// chatSearchThreadHasCandidate is the raw-record prefilter: only threads with
// at least one matching searchable message pay for the viewer projection.
func chatSearchThreadHasCandidate(thread scoutChatThreadRecord, foldedNeedle string) bool {
	for _, message := range thread.Messages {
		if !chatSearchMessageSearchable(message) {
			continue
		}
		if _, ok := chatSearchMatch(message.Text, foldedNeedle); ok {
			return true
		}
	}
	return false
}

// searchScoutChatMessages ranks matches newest first and truncates to limit.
func (app *kanbanBoardApp) searchScoutChatMessages(viewerEmail string, query string, limit int) []chatSearchResult {
	if app == nil || app.memory == nil {
		return []chatSearchResult{}
	}
	viewerEmail = normalizeAccountEmail(viewerEmail)
	query = strings.TrimSpace(query)
	if viewerEmail == "" || query == "" || limit <= 0 {
		return []chatSearchResult{}
	}
	foldedNeedle := chatSearchFold(query)
	needleRunes := utf8.RuneCountInString(query)
	hits := chatSearchHits{limit: limit, results: make([]chatSearchResult, 0, limit), times: make([]time.Time, 0, limit)}
	// includeArchived=false: the sidebar index hides archived threads, so a hit
	// inside one would be a dead click.
	for _, thread := range app.scoutChatThreadsSnapshot(viewerEmail, false, 0) {
		// The snapshot already filters by scoutChatThreadAllowsViewer; the
		// re-check is a belt-and-braces guard so a future snapshot change can
		// never widen search past the viewer's own thread list.
		if !scoutChatThreadAllowsViewer(thread, viewerEmail) {
			continue
		}
		// Riffs are not sidebar rows (thread_read_markers.go skips them too).
		if thread.Riff != nil || scoutChatThreadConversationKind(thread) == "channel_riff" {
			continue
		}
		if !chatSearchThreadHasCandidate(thread, foldedNeedle) {
			continue
		}
		projected := app.projectScoutChatThreadForViewerEpisodeWithResults(viewerEmail, thread, "", false)
		conversationKind := scoutChatThreadConversationKind(projected)
		visibility := scoutChatThreadVisibility(projected)
		for _, message := range projected.Messages {
			if !chatSearchMessageSearchable(message) {
				continue
			}
			start, ok := chatSearchMatch(message.Text, foldedNeedle)
			if !ok {
				continue
			}
			hits.add(chatSearchResult{
				ThreadID:         projected.ID,
				ThreadTitle:      projected.Title,
				ConversationKind: conversationKind,
				Visibility:       visibility,
				MessageID:        message.ID,
				AuthorName:       chatSearchAuthorName(message),
				AuthorEmail:      normalizeAccountEmail(message.AuthorEmail),
				CreatedAt:        message.CreatedAt,
				Snippet:          chatSearchSnippet(message.Text, start, needleRunes),
			}, chatSearchTime(message.CreatedAt))
		}
	}
	return hits.results
}

func assistantChatSearchHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !websocketOriginAllowed(r) {
		writeAuthError(w, http.StatusForbidden, "cross-origin request rejected")
		return
	}
	user := userFromRequest(r)
	if user == nil {
		writeAuthError(w, http.StatusUnauthorized, "not signed in")
		return
	}
	if kanbanApp == nil {
		writeAuthError(w, http.StatusServiceUnavailable, "chat threads are unavailable")
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	runes := utf8.RuneCountInString(query)
	if runes < chatSearchMinQueryRunes {
		writeAuthError(w, http.StatusBadRequest, "query too short")
		return
	}
	if runes > chatSearchMaxQueryRunes {
		writeAuthError(w, http.StatusBadRequest, "query too long")
		return
	}
	limit := clampQueryLimit(r.URL.Query().Get("limit"), chatSearchDefaultLimit, chatSearchMaxLimit)
	writeAuthJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"query":   query,
		"results": kanbanApp.searchScoutChatMessages(user.Email, query, limit),
	})
}
