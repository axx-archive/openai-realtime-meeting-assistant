package main

import (
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
)

// Thread catch-up and deposits — design §11 and §12 of
// docs/plans/the-table-design.md.
//
// These are the two answers to the everything-channel's defining failure: you
// scroll back and you fail. Catch-up compresses what you missed; deposits
// surface what the conversation produced, in the thread that produced it.
//
// Both are PURE functions over the thread's own messages. The room-scoped
// catch-up (catch_up_recap.go) goes through brain retrieval, ACL principals,
// and canonical publication because it summarizes meetings it does not hold.
// A chat thread holds its own messages, so none of that machinery is needed —
// and skipping it means no model call, no latency, and no hallucination
// surface at all.

// ── Deposits ────────────────────────────────────────────────────────────────

type threadDepositFile struct {
	Name      string `json:"name"`
	Mime      string `json:"mime,omitempty"`
	Ref       string `json:"ref,omitempty"`
	MessageID string `json:"messageId"`
	Author    string `json:"author,omitempty"`
}

type threadDepositLink struct {
	URL       string `json:"url"`
	Host      string `json:"host"`
	MessageID string `json:"messageId"`
	Author    string `json:"author,omitempty"`
}

type threadDepositsResult struct {
	Files []threadDepositFile `json:"files"`
	Links []threadDepositLink `json:"links"`
}

// Any reports whether there is anything to render. The rail is absent when
// empty — a strip that narrates its own emptiness is chrome, not information.
func (d threadDepositsResult) Any() bool {
	return len(d.Files) > 0 || len(d.Links) > 0
}

var linkPattern = regexp.MustCompile(`https?://[^\s<>"']+`)

// trimLinkTail strips sentence punctuation that a URL regex greedily captures.
// A chip that 404s because it swallowed the full stop is worse than no chip.
func trimLinkTail(raw string) string {
	for len(raw) > 0 {
		last := rune(raw[len(raw)-1])
		if strings.ContainsRune(".,;:!?", last) || last == ')' || last == ']' || last == '>' {
			raw = raw[:len(raw)-1]
			continue
		}
		break
	}
	return raw
}

// threadDeposits mines a thread for what it produced.
//
// Files come free — they are already on the message record and were simply
// never surfaced. Links are extracted from message text. Both anchor to the
// message that carried them, so a chip can scroll you to its origin.
func threadDeposits(messages []scoutChatMessageRecord) threadDepositsResult {
	result := threadDepositsResult{Files: []threadDepositFile{}, Links: []threadDepositLink{}}
	seenLink := map[string]struct{}{}
	seenFile := map[string]struct{}{}

	for _, message := range messages {
		author := strings.TrimSpace(message.AuthorName)

		for _, file := range message.Files {
			name := strings.TrimSpace(file.Name)
			if name == "" {
				continue
			}
			if _, dup := seenFile[name]; dup {
				continue
			}
			seenFile[name] = struct{}{}
			result.Files = append(result.Files, threadDepositFile{
				Name:      name,
				Mime:      file.Mime,
				Ref:       file.Ref,
				MessageID: message.ID,
				Author:    author,
			})
		}

		for _, match := range linkPattern.FindAllString(message.Text, -1) {
			trimmed := trimLinkTail(match)
			if trimmed == "" {
				continue
			}
			if _, dup := seenLink[trimmed]; dup {
				continue
			}
			seenLink[trimmed] = struct{}{}
			host := ""
			if parsed, err := url.Parse(trimmed); err == nil {
				host = strings.TrimPrefix(parsed.Host, "www.")
			}
			result.Links = append(result.Links, threadDepositLink{
				URL: trimmed,
				// The host is what a 90pt chip can actually show; a full URL
				// truncates to nothing useful.
				Host:      host,
				MessageID: message.ID,
				Author:    author,
			})
		}
	}
	return result
}

// ── Catch-up ────────────────────────────────────────────────────────────────

type threadCatchUpBullet struct {
	Text      string `json:"text"`
	Author    string `json:"author"`
	MessageID string `json:"messageId"`
	CreatedAt string `json:"createdAt"`
}

type threadCatchUpResult struct {
	Headline    string                `json:"headline"`
	Bullets     []threadCatchUpBullet `json:"bullets"`
	TotalUnread int                   `json:"totalUnread"`
}

// catchUpFillerFloor is the length below which a message carries no summary
// value. "ok", "thanks!", "sounds good" are the everything-channel's connective
// tissue, not its content.
const catchUpFillerFloor = 24

var catchUpFiller = map[string]struct{}{
	"ok": {}, "okay": {}, "k": {}, "yes": {}, "no": {}, "yep": {}, "nope": {},
	"thanks": {}, "thank you": {}, "ty": {}, "thx": {}, "cheers": {},
	"sounds good": {}, "sg": {}, "agreed": {}, "same": {}, "lol": {}, "haha": {},
	"nice": {}, "great": {}, "perfect": {}, "done": {}, "got it": {}, "sure": {},
	"+1": {}, "ditto": {}, "yup": {}, "morning": {}, "hi": {}, "hey": {},
}

// carriesSubstance decides whether a message earns a place in a recap.
//
// Deliberately conservative and deterministic: no model, no scoring, no
// ranking that could silently reorder someone's words. A message either looks
// like content or it does not, and the rule is legible enough that a reader
// can predict it.
func carriesSubstance(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return false
	}
	lowered := strings.ToLower(strings.TrimRight(trimmed, "!.?"))
	if _, filler := catchUpFiller[lowered]; filler {
		return false
	}

	// A message with no letters at all is an emoji or a reaction.
	letters := 0
	for _, r := range trimmed {
		if unicode.IsLetter(r) {
			letters++
		}
	}
	if letters == 0 {
		return false
	}
	return len([]rune(trimmed)) >= catchUpFillerFloor
}

// threadCatchUp builds an EXTRACTIVE recap of what the viewer missed.
//
// Every bullet is a verbatim slice of a real message carrying that message's
// id — the same discipline composeEvidenceLinkedCatchUp enforces for rooms,
// and for the same reason: a recap that paraphrases a colleague inaccurately
// is worse than no recap, because it gets quoted back at them.
//
// `limit` caps the bullets, and TotalUnread reports the true size so the client
// can say what was left out. Silently truncating would misrepresent the thread
// as smaller than it is.
func threadCatchUp(
	messages []scoutChatMessageRecord,
	readAt string,
	viewerEmail string,
	limit int,
) threadCatchUpResult {
	result := threadCatchUpResult{Bullets: []threadCatchUpBullet{}}
	viewer := normalizeAccountEmail(viewerEmail)

	var since time.Time
	if trimmed := strings.TrimSpace(readAt); trimmed != "" {
		if parsed, err := time.Parse(time.RFC3339, trimmed); err == nil {
			since = parsed
		}
	}

	speakers := []string{}
	seenSpeaker := map[string]struct{}{}

	unread := []scoutChatMessageRecord{}
	for _, message := range messages {
		if viewer != "" && normalizeAccountEmail(message.AuthorEmail) == viewer {
			continue
		}
		created, err := time.Parse(time.RFC3339, strings.TrimSpace(message.CreatedAt))
		if err != nil || !created.After(since) {
			continue
		}
		unread = append(unread, message)
	}
	result.TotalUnread = len(unread)
	if len(unread) == 0 {
		return result
	}

	for _, message := range unread {
		if !carriesSubstance(message.Text) {
			continue
		}
		author := strings.TrimSpace(message.AuthorName)
		if author != "" {
			if _, dup := seenSpeaker[author]; !dup {
				seenSpeaker[author] = struct{}{}
				speakers = append(speakers, author)
			}
		}
		result.Bullets = append(result.Bullets, threadCatchUpBullet{
			// Verbatim. Collapsing whitespace is the only transformation
			// allowed — anything more and it stops being a quote.
			Text:      strings.Join(strings.Fields(message.Text), " "),
			Author:    author,
			MessageID: message.ID,
			CreatedAt: message.CreatedAt,
		})
	}

	if limit > 0 && len(result.Bullets) > limit {
		result.Bullets = result.Bullets[:limit]
	}
	result.Headline = catchUpHeadline(speakers, result.TotalUnread)
	return result
}

func catchUpHeadline(speakers []string, total int) string {
	if total == 0 {
		return ""
	}
	sort.Strings(speakers)
	noun := "messages"
	if total == 1 {
		noun = "message"
	}
	switch len(speakers) {
	case 0:
		return "While you were away: " + itoa(total) + " " + noun + "."
	case 1:
		return itoa(total) + " " + noun + " from " + speakers[0] + "."
	case 2:
		return itoa(total) + " " + noun + " from " + speakers[0] + " and " + speakers[1] + "."
	default:
		return itoa(total) + " " + noun + " from " + strings.Join(speakers[:2], ", ") +
			" and " + itoa(len(speakers)-2) + " others."
	}
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	digits := []byte{}
	for value > 0 {
		digits = append([]byte{byte('0' + value%10)}, digits...)
		value /= 10
	}
	return string(digits)
}

// ── HTTP ────────────────────────────────────────────────────────────────────

// assistantThreadDigestHandler serves the catch-up and the deposit rail
// together: the thread screen needs both on open, and one round trip beats two.
func assistantThreadDigestHandler(w http.ResponseWriter, r *http.Request) {
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

	threadID := strings.TrimSpace(r.URL.Query().Get("threadId"))
	if threadID == "" {
		writeAuthError(w, http.StatusBadRequest, "threadId is required")
		return
	}
	thread, _, err := kanbanApp.scoutChatThreadByID(user.Email, threadID)
	if err != nil {
		writeAuthError(w, http.StatusNotFound, "chat thread not found")
		return
	}

	marker := lookupThreadReadMarker("", user.Email, threadID)
	writeAuthJSON(w, http.StatusOK, map[string]any{
		"ok":       true,
		"catchUp":  threadCatchUp(thread.Messages, marker.ReadAt, user.Email, 6),
		"deposits": threadDeposits(kanbanApp.projectScoutChatThreadForViewer(user.Email, thread).Messages),
	})
}
