package main

// Deliberate remember() seam (Wave 8 D1/D8) — the explicit write path into
// company memory. A signed-in user (or Scout acting for one through the
// remember_note tool) files ONE recall-eligible kind=note entry carrying who
// remembered it, when, a subject, searchable aliases, and the source pointer
// (threadId/messageId) it was lifted from.
//
// Privacy contract (execution-plan Critical Rule 7): a private you+Scout
// thread's text never enters memory implicitly. This seam is the one explicit
// door — copying a private-thread message into a note requires the caller to
// be that thread's viewer (scoutChatThreadByID enforces owner-only for private
// threads), and the note is stamped rememberedBy=<that viewer>. The implicit
// contract (private_chat_brain_contract_test.go) is untouched: nothing here
// runs without a deliberate request.
//
// ACL: notes are company memory (visibility=organization) unless private:true,
// which stamps visibility=private + ownerEmail so recallEntryScopeAllowed keeps
// the note owner-only on every recall lane.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	rememberNoteToolName         = "remember_note"
	rememberNoteSource           = "remember"
	noteRememberedByMetadataKey  = "rememberedBy"
	noteSubjectMetadataKey       = "subject"
	noteAliasesMetadataKey       = "aliases"
	noteAtMetadataKey            = "at"
	notePrivateMetadataKey       = "private"
	noteSourceThreadMetadataKey  = "threadId"
	noteSourceMessageMetadataKey = "messageId"
	rememberNoteTextLimit        = 4000
	rememberNoteSubjectLimit     = 120
)

// rememberNoteRequest is the POST /assistant/remember body and the
// remember_note tool argument shape. text may be empty when threadId+messageId
// point at a message whose text should be copied verbatim (D8).
type rememberNoteRequest struct {
	Text      string   `json:"text"`
	Subject   string   `json:"subject,omitempty"`
	Aliases   []string `json:"aliases,omitempty"`
	ThreadID  string   `json:"threadId,omitempty"`
	MessageID string   `json:"messageId,omitempty"`
	Private   bool     `json:"private,omitempty"`
}

// rememberNote files the note. idScope namespaces the deterministic id (the
// note_for_the_record F14 discipline): an accidental double-call in the same
// scope files once; a deliberate re-file in another scope is its own record.
func (app *kanbanBoardApp) rememberNote(user *userAccount, request rememberNoteRequest, idScope string) (meetingMemoryEntry, bool, error) {
	if app == nil || app.memory == nil {
		return meetingMemoryEntry{}, false, fmt.Errorf("meeting memory is unavailable")
	}
	if user == nil || normalizeAccountEmail(user.Email) == "" {
		return meetingMemoryEntry{}, false, fmt.Errorf("remember requires a signed-in user")
	}
	viewer := normalizeAccountEmail(user.Email)
	threadID := strings.TrimSpace(request.ThreadID)
	messageID := strings.TrimSpace(request.MessageID)
	text := normalizeMemoryText(request.Text)
	sourceVisibility := ""
	var sourceFence map[string]string
	if threadID != "" && messageID != "" {
		// D8: explicit copy of one message the caller can see. For a private
		// thread scoutChatThreadByID admits only the owner, so the note is
		// always the owner's own deliberate act.
		thread, _, err := app.scoutChatThreadByID(viewer, threadID)
		if err != nil {
			return meetingMemoryEntry{}, false, fmt.Errorf("chat thread not found")
		}
		found := false
		for _, message := range thread.Messages {
			if strings.TrimSpace(message.ID) != messageID {
				continue
			}
			found = true
			if text == "" {
				text = normalizeMemoryText(message.Text)
			}
			break
		}
		if !found {
			return meetingMemoryEntry{}, false, fmt.Errorf("chat message not found")
		}
		sourceVisibility = scoutChatThreadVisibility(thread)
		sourceFence = channelThreadRecallFence(thread)
	} else if threadID != "" || messageID != "" {
		return meetingMemoryEntry{}, false, fmt.Errorf("threadId and messageId are required together")
	}
	if text == "" {
		return meetingMemoryEntry{}, false, fmt.Errorf("text is required")
	}
	text = trimForStorage(text, rememberNoteTextLimit)
	subject := trimForStorage(normalizeMemoryText(request.Subject), rememberNoteSubjectLimit)
	aliases := clampDigestAliases(request.Aliases)
	author := firstNonEmptyString(participantNameForEmail(viewer), strings.TrimSpace(user.Name), viewer)
	now := time.Now().UTC()

	sum := sha256.Sum256([]byte(strings.Join([]string{idScope, viewer, subject, text, threadID, messageID}, "\x00")))
	id := "note-" + hex.EncodeToString(sum[:])[:16]

	// Pre-stamp the meetingId (live office sitting or "none") so a note filed
	// at idle never lazily opens a phantom sitting (the note_for_the_record
	// discipline).
	meetingID := firstNonEmptyString(app.memory.currentMeetingID(officeRoomID), "none")
	visibility := "organization"
	memberEmails := ""
	switch {
	case request.Private:
		visibility = "private"
	case sourceFence["visibility"] == "project":
		// A message lifted from a member-restricted channel (or a Riff) keeps
		// that channel's fence: the note is recallable by the members, never
		// by the whole organization. A private you+Scout thread is different —
		// the explicit copy IS the deliberate door into company memory (D8).
		visibility = "project"
		memberEmails = sourceFence["memberEmails"]
	}
	metadata := map[string]string{
		"author":                    author,
		"authorCertain":             "true",
		"source":                    rememberNoteSource,
		"roomId":                    officeRoomID,
		"meetingId":                 meetingID,
		"tenantId":                  canonicalArtifactTenantID(),
		"visibility":                visibility,
		"ownerEmail":                viewer,
		noteRememberedByMetadataKey: viewer,
		noteAtMetadataKey:           now.Format(time.RFC3339Nano),
		notePrivateMetadataKey:      strconv.FormatBool(request.Private),
	}
	if memberEmails != "" {
		metadata["memberEmails"] = memberEmails
	}
	if subject != "" {
		metadata[noteSubjectMetadataKey] = subject
		// topic keeps the note_for_the_record vocabulary readable by the same
		// consumers.
		metadata["topic"] = subject
	}
	if len(aliases) > 0 {
		metadata[noteAliasesMetadataKey] = strings.Join(aliases, ",")
		// Mirror the aliases into the same searchable metadata text the digest
		// producers use, so store.search's alias band matches a note by any of
		// its phrasings for free.
		metadata[digestAliasesMetadataKey] = digestAliasesMetadata(aliases)
	}
	if threadID != "" {
		metadata[noteSourceThreadMetadataKey] = threadID
		metadata[noteSourceMessageMetadataKey] = messageID
		metadata["sourceVisibility"] = sourceVisibility
	}
	entry, appended, err := app.memory.appendNote(id, text, metadata)
	if err != nil {
		return meetingMemoryEntry{}, false, err
	}
	if appended {
		broadcastSignedInKanbanEvent("memory", nil)
	}
	return entry, appended, nil
}

// rememberNotePayload is the wire shape shared by the HTTP seam and the tool.
func rememberNotePayload(entry meetingMemoryEntry, recorded bool) map[string]any {
	payload := map[string]any{
		"ok":           true,
		"id":           entry.ID,
		"kind":         meetingMemoryKindNote,
		"recorded":     recorded,
		"text":         entry.Text,
		"subject":      entry.Metadata[noteSubjectMetadataKey],
		"aliases":      splitNarrativeAliases(entry.Metadata[noteAliasesMetadataKey]),
		"rememberedBy": entry.Metadata[noteRememberedByMetadataKey],
		"at":           entry.Metadata[noteAtMetadataKey],
		"private":      strings.EqualFold(entry.Metadata[notePrivateMetadataKey], "true"),
		"visibility":   entry.Metadata["visibility"],
		"meetingId":    entry.Metadata["meetingId"],
	}
	if threadID := strings.TrimSpace(entry.Metadata[noteSourceThreadMetadataKey]); threadID != "" {
		payload["threadId"] = threadID
		payload["messageId"] = entry.Metadata[noteSourceMessageMetadataKey]
	}
	return payload
}

// assistantRememberHandler serves POST /assistant/remember. Same origin +
// session guards as the quarantine handlers; the signed-in user is the author.
func assistantRememberHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
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
		writeAuthError(w, http.StatusServiceUnavailable, "memory is unavailable")
		return
	}
	var request rememberNoteRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&request); err != nil {
		writeAuthError(w, http.StatusBadRequest, "could not read remember request")
		return
	}
	entry, recorded, err := kanbanApp.rememberNote(user, request, "remember-http:"+normalizeAccountEmail(user.Email))
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "not found") {
			status = http.StatusNotFound
		}
		writeAuthError(w, status, err.Error())
		return
	}
	writeAuthJSON(w, http.StatusOK, rememberNotePayload(entry, recorded))
}

// rememberNoteToolDefinition is the Scout tool registration (kanbanTools):
// Scout calls it when a user says "remember that…". Arguments mirror the HTTP
// body minus the source pointer (a tool call already carries its own thread).
func rememberNoteToolDefinition() map[string]any {
	return map[string]any{
		"type":        "function",
		"name":        rememberNoteToolName,
		"description": "Remember something for the signed-in user so it grounds future recall: use when they say \"remember that…\", \"don't forget…\", \"keep in mind…\", or \"note that…\" about a fact, preference, person, or plan. Writes ONE recall-eligible company note attributed to them (rememberedBy) with an optional subject and search aliases. Set private=true only when they explicitly want it kept to themselves; otherwise the note is company memory. For an explicit decision or stance use note_for_the_record with kind=decision instead.",
		"parameters": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"text":    map[string]any{"type": "string", "description": "What to remember, self-contained and in the user's own framing."},
				"subject": map[string]any{"type": "string", "description": "Optional short subject label (\"Samsung deal\", \"Tim's travel\")."},
				"aliases": map[string]any{
					"type":        "array",
					"description": "Optional alternate phrasings someone might search this by (synonyms, nicknames, acronyms); max 5, each <=60 chars.",
					"items":       map[string]any{"type": "string"},
				},
				"private": map[string]any{"type": "boolean", "description": "true keeps the note visible to the requester only; default false (company memory)."},
			},
			"required":             []string{"text"},
			"additionalProperties": false,
		},
	}
}

// rememberNoteTool executes remember_note for a signed-in requester. The room
// voice loop has no single actor, so a requester-less call is refused rather
// than filed author-uncertain: a note without rememberedBy is not a remember.
func (app *kanbanBoardApp) rememberNoteTool(args map[string]any, requesterEmail string, idScope string) (map[string]any, bool, error) {
	if app == nil || app.memory == nil {
		return nil, false, fmt.Errorf("meeting memory is unavailable")
	}
	user := accountStore().findUser(requesterEmail)
	if user == nil {
		return nil, false, fmt.Errorf("remember_note requires a signed-in requester")
	}
	request := rememberNoteRequest{
		Text:    asString(args["text"]),
		Subject: asString(args["subject"]),
		Aliases: rememberNoteAliasesArg(args["aliases"]),
	}
	switch value := args["private"].(type) {
	case bool:
		request.Private = value
	case string:
		request.Private = strings.EqualFold(strings.TrimSpace(value), "true")
	}
	entry, recorded, err := app.rememberNote(user, request, idScope)
	if err != nil {
		return nil, false, err
	}
	return rememberNotePayload(entry, recorded), false, nil
}

// rememberNoteAliasesArg accepts the tool's aliases as a JSON array or a
// comma-separated string (model output is not always well typed).
func rememberNoteAliasesArg(value any) []string {
	switch typed := value.(type) {
	case []string:
		return typed
	case []any:
		aliases := make([]string, 0, len(typed))
		for _, item := range typed {
			if alias := strings.TrimSpace(asString(item)); alias != "" {
				aliases = append(aliases, alias)
			}
		}
		return aliases
	case string:
		return splitNarrativeAliases(typed)
	}
	return nil
}
