package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	scoutChatThreadRequestLimit = 768 << 10
	scoutChatMaxFilesPerMessage = 6
	scoutChatMaxFileTextBytes   = 64 << 10
)

type scoutChatFileAttachment struct {
	Name string `json:"name"`
	Kind string `json:"kind,omitempty"`
	Size int64  `json:"size,omitempty"`
	Text string `json:"text,omitempty"`
}

type scoutChatThreadRef struct {
	ID         string `json:"id"`
	Mode       string `json:"mode"`
	Query      string `json:"query"`
	Status     string `json:"status"`
	ArtifactID string `json:"artifactId,omitempty"`
}

type scoutChatMessageRecord struct {
	ID        string                    `json:"id"`
	Kind      string                    `json:"kind"`
	Role      string                    `json:"role"`
	Text      string                    `json:"text,omitempty"`
	CreatedAt string                    `json:"createdAt"`
	Files     []scoutChatFileAttachment `json:"files,omitempty"`
	Thread    *scoutChatThreadRef       `json:"thread,omitempty"`
}

type scoutChatThreadRecord struct {
	ID         string                   `json:"id"`
	Title      string                   `json:"title"`
	Preview    string                   `json:"preview"`
	OwnerEmail string                   `json:"ownerEmail"`
	CreatedBy  string                   `json:"createdBy,omitempty"`
	CreatedAt  string                   `json:"createdAt"`
	UpdatedAt  string                   `json:"updatedAt"`
	ArchivedAt string                   `json:"archivedAt,omitempty"`
	Messages   []scoutChatMessageRecord `json:"messages,omitempty"`
}

func assistantChatThreadsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
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

	switch r.Method {
	case http.MethodGet:
		includeArchived := strings.EqualFold(r.URL.Query().Get("archived"), "true") || strings.EqualFold(r.URL.Query().Get("includeArchived"), "true")
		writeAuthJSON(w, http.StatusOK, map[string]any{
			"ok":      true,
			"threads": kanbanApp.scoutChatThreadsSnapshot(user.Email, includeArchived, 100),
		})
	case http.MethodPost:
		payload := struct {
			Title string `json:"title"`
		}{}
		if r.Body != nil {
			_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&payload)
		}
		thread, err := kanbanApp.createScoutChatThread(user.Email, user.Name, payload.Title)
		if err != nil {
			writeAuthError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeAuthJSON(w, http.StatusCreated, map[string]any{"ok": true, "thread": thread})
	}
}

func assistantChatThreadHandler(w http.ResponseWriter, r *http.Request) {
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

	suffix := strings.Trim(strings.TrimPrefix(r.URL.Path, "/assistant/chat-threads/"), "/")
	parts := strings.Split(suffix, "/")
	if suffix == "" || len(parts) > 2 {
		http.NotFound(w, r)
		return
	}
	threadID := parts[0]
	if threadID == "" {
		http.NotFound(w, r)
		return
	}

	if len(parts) == 1 && r.Method == http.MethodPatch {
		payload := struct {
			Archived bool `json:"archived"`
		}{Archived: true}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&payload); err != nil {
			writeAuthError(w, http.StatusBadRequest, "could not read thread update")
			return
		}
		thread, err := kanbanApp.setScoutChatThreadArchived(user.Email, threadID, payload.Archived)
		if err != nil {
			writeScoutChatThreadError(w, err)
			return
		}
		writeAuthJSON(w, http.StatusOK, map[string]any{"ok": true, "thread": thread})
		return
	}

	if len(parts) == 2 && parts[1] == "messages" && r.Method == http.MethodPost {
		payload := struct {
			Text  string                    `json:"text"`
			Files []scoutChatFileAttachment `json:"files"`
		}{}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, scoutChatThreadRequestLimit)).Decode(&payload); err != nil {
			writeAuthError(w, http.StatusBadRequest, "could not read chat message")
			return
		}
		response, err := kanbanApp.appendScoutChatThreadMessage(r.Context(), user, threadID, payload.Text, payload.Files)
		if err != nil {
			writeScoutChatThreadError(w, err)
			return
		}
		writeAuthJSON(w, http.StatusOK, response)
		return
	}

	http.NotFound(w, r)
}

func writeScoutChatThreadError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	if strings.Contains(err.Error(), "not found") {
		status = http.StatusNotFound
	}
	if strings.Contains(err.Error(), "archived") {
		status = http.StatusConflict
	}
	writeAuthError(w, status, err.Error())
}

func (app *kanbanBoardApp) createScoutChatThread(ownerEmail string, createdBy string, title string) (scoutChatThreadRecord, error) {
	if app == nil || app.memory == nil {
		return scoutChatThreadRecord{}, fmt.Errorf("chat thread memory is unavailable")
	}
	now := time.Now().UTC()
	ownerEmail = normalizeAccountEmail(ownerEmail)
	if ownerEmail == "" {
		return scoutChatThreadRecord{}, fmt.Errorf("thread owner is required")
	}
	createdBy = canonicalRoomActorName(createdBy)
	thread := scoutChatThreadRecord{
		ID:         fmt.Sprintf("scout-chat-%d", now.UnixNano()),
		Title:      firstNonEmptyString(strings.TrimSpace(title), "Scout"),
		Preview:    "new chat thread",
		OwnerEmail: ownerEmail,
		CreatedBy:  createdBy,
		CreatedAt:  now.Format(time.RFC3339Nano),
		UpdatedAt:  now.Format(time.RFC3339Nano),
	}
	entryText, err := encodeScoutChatThread(thread)
	if err != nil {
		return scoutChatThreadRecord{}, err
	}
	_, _, err = app.memory.appendScoutChatThread(thread.ID, entryText, scoutChatThreadMetadata(thread))
	if err != nil {
		return scoutChatThreadRecord{}, err
	}
	return thread, nil
}

func (app *kanbanBoardApp) appendScoutChatThreadMessage(ctx context.Context, user *userAccount, threadID string, text string, files []scoutChatFileAttachment) (map[string]any, error) {
	thread, _, err := app.scoutChatThreadByID(user.Email, threadID)
	if err != nil {
		return nil, err
	}
	if thread.ArchivedAt != "" {
		return nil, fmt.Errorf("chat thread is archived")
	}

	files = sanitizeScoutChatFiles(files)
	text = strings.TrimSpace(text)
	if text == "" && len(files) == 0 {
		return nil, fmt.Errorf("message text or attachment is required")
	}

	now := time.Now().UTC()
	userMessage := scoutChatMessageRecord{
		ID:        fmt.Sprintf("scout-chat-message-%d", now.UnixNano()),
		Kind:      "message",
		Role:      "user",
		Text:      text,
		CreatedAt: now.Format(time.RFC3339Nano),
		Files:     files,
	}
	history := scoutChatHistoryFromThread(thread)
	thread.Messages = append(thread.Messages, userMessage)

	modelQuery := scoutChatMessageModelText(userMessage)
	mode := scoutChatThreadModeForText(text)
	response := map[string]any{
		"ok":      true,
		"message": userMessage,
	}
	if mode != "" {
		agentThread, err := app.launchAgentThread(mode, text, user.Name)
		if err != nil {
			return nil, err
		}
		viewerThread := agentThreadForViewer(agentThread, canAccessArtifactLibrary(user))
		replyText := assistantToolLabel(mode) + " thread launched"
		assistantMessage := scoutChatMessageRecord{
			ID:        fmt.Sprintf("scout-chat-message-%d", time.Now().UTC().UnixNano()),
			Kind:      "thread",
			Role:      "scout",
			Text:      replyText,
			CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
			Thread: &scoutChatThreadRef{
				ID:         agentThread.ID,
				Mode:       agentThread.Mode,
				Query:      agentThread.Query,
				Status:     agentThread.Status,
				ArtifactID: agentThread.Artifact.ID,
			},
		}
		thread.Messages = append(thread.Messages, assistantMessage)
		updateScoutChatThreadSummary(&thread, userMessage, assistantMessage)
		if err := app.saveScoutChatThread(thread); err != nil {
			return nil, err
		}
		response["answer"] = assistantMessage
		response["thread"] = thread
		response["agentThread"] = viewerThread
		response["artifact"] = viewerThread.Artifact
		response["actions"] = viewerThread.Actions
		return response, nil
	}

	result, err := app.resolveAssistantQueryContext(ctx, modelQuery, history)
	if err != nil {
		errorMessage := scoutChatMessageRecord{
			ID:        fmt.Sprintf("scout-chat-message-%d", time.Now().UTC().UnixNano()),
			Kind:      "message",
			Role:      "error",
			Text:      err.Error(),
			CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		}
		thread.Messages = append(thread.Messages, errorMessage)
		updateScoutChatThreadSummary(&thread, userMessage, errorMessage)
		_ = app.saveScoutChatThread(thread)
		return nil, err
	}
	answer := strings.TrimSpace(result.answer)
	if answer == "" {
		answer = "no answer yet"
	}
	assistantMessage := scoutChatMessageRecord{
		ID:        fmt.Sprintf("scout-chat-message-%d", time.Now().UTC().UnixNano()),
		Kind:      "message",
		Role:      "scout",
		Text:      answer,
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	thread.Messages = append(thread.Messages, assistantMessage)
	updateScoutChatThreadSummary(&thread, userMessage, assistantMessage)
	if err := app.saveScoutChatThread(thread); err != nil {
		return nil, err
	}
	response["answer"] = assistantMessage
	response["thread"] = thread
	return response, nil
}

func (app *kanbanBoardApp) setScoutChatThreadArchived(ownerEmail string, threadID string, archived bool) (scoutChatThreadRecord, error) {
	thread, _, err := app.scoutChatThreadByID(ownerEmail, threadID)
	if err != nil {
		return scoutChatThreadRecord{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if archived {
		thread.ArchivedAt = now
	} else {
		thread.ArchivedAt = ""
	}
	thread.UpdatedAt = now
	if archived {
		thread.Preview = "archived"
	} else if thread.Preview == "" || thread.Preview == "archived" {
		thread.Preview = scoutChatThreadPreview(thread)
	}
	if err := app.saveScoutChatThread(thread); err != nil {
		return scoutChatThreadRecord{}, err
	}
	return thread, nil
}

func (app *kanbanBoardApp) saveScoutChatThread(thread scoutChatThreadRecord) error {
	entryText, err := encodeScoutChatThread(thread)
	if err != nil {
		return err
	}
	_, _, err = app.memory.updateScoutChatThread(thread.ID, entryText, scoutChatThreadMetadata(thread))
	return err
}

func (app *kanbanBoardApp) scoutChatThreadsSnapshot(ownerEmail string, includeArchived bool, limit int) []scoutChatThreadRecord {
	if app == nil || app.memory == nil {
		return nil
	}
	ownerEmail = normalizeAccountEmail(ownerEmail)
	if ownerEmail == "" {
		return nil
	}

	entries := app.memory.snapshot(0)
	threads := make([]scoutChatThreadRecord, 0, len(entries))
	for _, entry := range entries {
		thread, ok := decodeScoutChatThreadEntry(entry)
		if !ok || normalizeAccountEmail(thread.OwnerEmail) != ownerEmail {
			continue
		}
		if !includeArchived && thread.ArchivedAt != "" {
			continue
		}
		threads = append(threads, thread)
	}
	sort.SliceStable(threads, func(i, j int) bool {
		return scoutChatThreadTime(threads[i]).After(scoutChatThreadTime(threads[j]))
	})
	if limit > 0 && len(threads) > limit {
		threads = threads[:limit]
	}
	return threads
}

func (app *kanbanBoardApp) scoutChatThreadByID(ownerEmail string, threadID string) (scoutChatThreadRecord, meetingMemoryEntry, error) {
	ownerEmail = normalizeAccountEmail(ownerEmail)
	threadID = strings.TrimSpace(threadID)
	if ownerEmail == "" || threadID == "" {
		return scoutChatThreadRecord{}, meetingMemoryEntry{}, fmt.Errorf("chat thread not found")
	}
	for _, entry := range app.memory.snapshot(0) {
		if entry.Kind != meetingMemoryKindScoutChat || entry.ID != threadID {
			continue
		}
		thread, ok := decodeScoutChatThreadEntry(entry)
		if !ok || normalizeAccountEmail(thread.OwnerEmail) != ownerEmail {
			break
		}
		return thread, entry, nil
	}
	return scoutChatThreadRecord{}, meetingMemoryEntry{}, fmt.Errorf("chat thread not found")
}

func encodeScoutChatThread(thread scoutChatThreadRecord) (string, error) {
	raw, err := json.Marshal(thread)
	if err != nil {
		return "", fmt.Errorf("encode chat thread: %w", err)
	}
	return string(raw), nil
}

func decodeScoutChatThreadEntry(entry meetingMemoryEntry) (scoutChatThreadRecord, bool) {
	if entry.Kind != meetingMemoryKindScoutChat {
		return scoutChatThreadRecord{}, false
	}
	var thread scoutChatThreadRecord
	if err := json.Unmarshal([]byte(entry.Text), &thread); err != nil {
		return scoutChatThreadRecord{}, false
	}
	if strings.TrimSpace(thread.ID) == "" {
		thread.ID = entry.ID
	}
	if strings.TrimSpace(thread.OwnerEmail) == "" {
		thread.OwnerEmail = entry.Metadata["ownerEmail"]
	}
	if strings.TrimSpace(thread.Title) == "" {
		thread.Title = firstNonEmptyString(entry.Metadata["title"], "Scout")
	}
	if strings.TrimSpace(thread.CreatedAt) == "" && !entry.CreatedAt.IsZero() {
		thread.CreatedAt = entry.CreatedAt.Format(time.RFC3339Nano)
	}
	if strings.TrimSpace(thread.UpdatedAt) == "" {
		thread.UpdatedAt = firstNonEmptyString(entry.Metadata["updatedAt"], thread.CreatedAt)
	}
	return thread, true
}

func scoutChatThreadMetadata(thread scoutChatThreadRecord) map[string]string {
	metadata := map[string]string{
		"ownerEmail": normalizeAccountEmail(thread.OwnerEmail),
		"title":      strings.TrimSpace(thread.Title),
		"preview":    strings.TrimSpace(thread.Preview),
		"createdAt":  strings.TrimSpace(thread.CreatedAt),
		"updatedAt":  strings.TrimSpace(thread.UpdatedAt),
		"source":     "scout_chat",
		"status":     "active",
	}
	if strings.TrimSpace(thread.CreatedBy) != "" {
		metadata["createdBy"] = strings.TrimSpace(thread.CreatedBy)
	}
	if strings.TrimSpace(thread.ArchivedAt) != "" {
		metadata["archivedAt"] = strings.TrimSpace(thread.ArchivedAt)
		metadata["status"] = "archived"
	}
	return metadata
}

func sanitizeScoutChatFiles(files []scoutChatFileAttachment) []scoutChatFileAttachment {
	if len(files) > scoutChatMaxFilesPerMessage {
		files = files[:scoutChatMaxFilesPerMessage]
	}
	cleaned := make([]scoutChatFileAttachment, 0, len(files))
	for _, file := range files {
		name := trimForStorage(file.Name, 180)
		if name == "" {
			name = "file"
		}
		kind := trimForStorage(file.Kind, 120)
		size := file.Size
		if size < 0 {
			size = 0
		}
		text := strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(file.Text, "\r\n", "\n"), "\r", "\n"))
		if len(text) > scoutChatMaxFileTextBytes {
			text = text[:scoutChatMaxFileTextBytes]
			for !utf8.ValidString(text) && len(text) > 0 {
				text = text[:len(text)-1]
			}
			text = strings.TrimSpace(text) + "\n[truncated]"
		}
		cleaned = append(cleaned, scoutChatFileAttachment{
			Name: name,
			Kind: kind,
			Size: size,
			Text: text,
		})
	}
	return cleaned
}

func scoutChatHistoryFromThread(thread scoutChatThreadRecord) []scoutChatTurn {
	if len(thread.Messages) == 0 {
		return nil
	}
	start := 0
	if len(thread.Messages) > scoutChatMaxHistoryTurns {
		start = len(thread.Messages) - scoutChatMaxHistoryTurns
	}
	history := make([]scoutChatTurn, 0, len(thread.Messages)-start)
	for _, message := range thread.Messages[start:] {
		role := strings.TrimSpace(message.Role)
		switch role {
		case "assistant", "scout":
			role = "scout"
		case "user":
			role = "user"
		default:
			continue
		}
		text := scoutChatMessageModelText(message)
		if strings.TrimSpace(text) == "" {
			continue
		}
		history = append(history, scoutChatTurn{role: role, text: text})
	}
	return history
}

func scoutChatMessageModelText(message scoutChatMessageRecord) string {
	text := strings.TrimSpace(message.Text)
	parts := make([]string, 0, len(message.Files)+1)
	if text != "" {
		parts = append(parts, text)
	}
	for _, file := range message.Files {
		label := strings.TrimSpace(file.Name)
		if label == "" {
			label = "file"
		}
		metaParts := []string{}
		if strings.TrimSpace(file.Kind) != "" {
			metaParts = append(metaParts, strings.TrimSpace(file.Kind))
		}
		if file.Size > 0 {
			metaParts = append(metaParts, fmt.Sprintf("%d bytes", file.Size))
		}
		meta := strings.Join(metaParts, ", ")
		metaSuffix := ""
		if meta != "" {
			metaSuffix = " (" + meta + ")"
		}
		if strings.TrimSpace(file.Text) == "" {
			parts = append(parts, fmt.Sprintf("Attached file: %s%s.", label, metaSuffix))
			continue
		}
		parts = append(parts, fmt.Sprintf("Attached file: %s%s:\n%s", label, metaSuffix, strings.TrimSpace(file.Text)))
	}
	if len(parts) == 0 && len(message.Files) > 0 {
		return "Use the attached files as context."
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}

func updateScoutChatThreadSummary(thread *scoutChatThreadRecord, userMessage scoutChatMessageRecord, assistantMessage scoutChatMessageRecord) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	thread.UpdatedAt = now
	if strings.TrimSpace(thread.Title) == "" || thread.Title == "Scout" || thread.Title == "New Scout thread" {
		thread.Title = scoutChatThreadTitle(userMessage)
	}
	thread.Preview = firstNonEmptyString(strings.TrimSpace(assistantMessage.Text), scoutChatThreadPreview(*thread))
}

func scoutChatThreadTitle(message scoutChatMessageRecord) string {
	text := strings.TrimSpace(message.Text)
	if text == "" && len(message.Files) > 0 {
		text = "Files: " + message.Files[0].Name
	}
	if text == "" {
		return "Scout"
	}
	return trimForStorage(text, 72)
}

func scoutChatThreadPreview(thread scoutChatThreadRecord) string {
	for index := len(thread.Messages) - 1; index >= 0; index-- {
		if text := strings.TrimSpace(thread.Messages[index].Text); text != "" {
			return trimForStorage(text, 140)
		}
	}
	return "new chat thread"
}

func scoutChatThreadTime(thread scoutChatThreadRecord) time.Time {
	for _, value := range []string{thread.UpdatedAt, thread.CreatedAt} {
		if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
			return parsed
		}
	}
	return time.Time{}
}

func trimForStorage(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 || len([]rune(value)) <= limit {
		return value
	}
	runes := []rune(value)
	return strings.TrimSpace(string(runes[:limit-1])) + "..."
}
