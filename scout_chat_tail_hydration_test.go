package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestScoutChatTailHydrationBoundsPayloadAndWalksStableCursor(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	thread, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "Dense private history", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatalf("create thread: %v", err)
	}
	base := time.Date(2026, time.August, 22, 12, 0, 0, 0, time.UTC)
	largeText := strings.Repeat("source-grounded western culture proof ", 52)
	thread.Messages = make([]scoutChatMessageRecord, 0, 1000)
	for index := 0; index < 1000; index++ {
		thread.Messages = append(thread.Messages, scoutChatMessageRecord{
			ID:          fmt.Sprintf("message-%04d", index),
			Kind:        "message",
			Role:        "scout",
			AuthorName:  "Scout",
			AuthorEmail: "scout@thebonfire.xyz",
			Text:        fmt.Sprintf("%04d %s", index, largeText),
			CreatedAt:   base.Add(time.Duration(index) * time.Second).Format(time.RFC3339Nano),
		})
	}
	thread.Preview = "message 0999"
	thread.UpdatedAt = base.Add(999 * time.Second).Format(time.RFC3339Nano)
	if err := kanbanApp.saveScoutChatThread(thread); err != nil {
		t.Fatalf("save dense thread: %v", err)
	}

	cookies := loginAs(t, "aj@shareability.com", "B0NFIRE!")
	get := func(ctx context.Context, path string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request = request.WithContext(ctx)
		for _, cookie := range cookies {
			request.AddCookie(cookie)
		}
		response := httptest.NewRecorder()
		assistantChatThreadHandler(response, request)
		return response
	}
	type hydrationPayload struct {
		Thread  scoutChatThreadRecord `json:"thread"`
		History scoutChatHistoryPage  `json:"history"`
	}

	projectedMessageIDs := []string{}
	probeContext := withScoutChatMessageProjectionProbe(context.Background(), func(messageID string) {
		projectedMessageIDs = append(projectedMessageIDs, messageID)
	})
	firstResponse := get(probeContext, "/assistant/chat-threads/"+thread.ID+"?view=tail&limit=64&since="+base.Add(990*time.Second).Format(time.RFC3339Nano))
	if firstResponse.Code != http.StatusOK {
		t.Fatalf("tail status=%d body=%s", firstResponse.Code, firstResponse.Body.String())
	}
	if size := firstResponse.Body.Len(); size >= scoutChatHydrationResponseMax {
		t.Fatalf("tail response=%d bytes, want <%d", size, scoutChatHydrationResponseMax)
	}
	var first hydrationPayload
	if err := json.Unmarshal(firstResponse.Body.Bytes(), &first); err != nil {
		t.Fatalf("decode tail: %v", err)
	}
	if got := len(first.Thread.Messages); got != 64 {
		t.Fatalf("tail messages=%d, want 64", got)
	}
	if len(projectedMessageIDs) != 64 || projectedMessageIDs[0] != "message-0936" {
		t.Fatalf("per-message projection touched %d messages starting at %q, want only bounded tail", len(projectedMessageIDs), firstNonEmptyString(projectedMessageIDs...))
	}
	if first.Thread.Messages[0].ID != "message-0936" || first.Thread.Messages[63].ID != "message-0999" {
		t.Fatalf("tail ids=%s..%s, want message-0936..message-0999", first.Thread.Messages[0].ID, first.Thread.Messages[63].ID)
	}
	if !first.History.HasEarlier || first.History.NextBeforeMessageID != "message-0936" || first.History.MessageCount != 64 {
		t.Fatalf("tail history=%+v", first.History)
	}
	if first.History.UnreadCount != 9 || first.History.UnreadRootCount != 9 {
		t.Fatalf("unread counts=%d/%d, want 9/9", first.History.UnreadCount, first.History.UnreadRootCount)
	}
	fullBody, err := json.Marshal(map[string]any{"ok": true, "thread": thread})
	if err != nil {
		t.Fatalf("marshal full comparison payload: %v", err)
	}
	t.Logf("dense cold hydration: full=%d bytes bounded=%d bytes messages=%d projected=%d", len(fullBody), firstResponse.Body.Len(), len(first.Thread.Messages), len(projectedMessageIDs))

	// New live traffic landing after page one must not move its exclusive-before
	// seam. Walking backward still returns the exact preceding records with no
	// overlap, gap, or dependence on the thread's new total length.
	for index := 1000; index < 1005; index++ {
		thread.Messages = append(thread.Messages, scoutChatMessageRecord{
			ID: fmt.Sprintf("message-%04d", index), Kind: "message", Role: "scout", AuthorName: "Scout",
			Text: fmt.Sprintf("%04d concurrent append", index), CreatedAt: base.Add(time.Duration(index) * time.Second).Format(time.RFC3339Nano),
		})
	}
	thread.UpdatedAt = base.Add(1004 * time.Second).Format(time.RFC3339Nano)
	if err := kanbanApp.saveScoutChatThread(thread); err != nil {
		t.Fatalf("save concurrent appends: %v", err)
	}
	secondResponse := get(context.Background(), "/assistant/chat-threads/"+thread.ID+"?view=tail&limit=64&before="+first.History.NextBeforeMessageID)
	if secondResponse.Code != http.StatusOK {
		t.Fatalf("earlier status=%d body=%s", secondResponse.Code, secondResponse.Body.String())
	}
	var second hydrationPayload
	if err := json.Unmarshal(secondResponse.Body.Bytes(), &second); err != nil {
		t.Fatalf("decode earlier page: %v", err)
	}
	if got := len(second.Thread.Messages); got != 64 {
		t.Fatalf("earlier messages=%d, want 64", got)
	}
	if second.Thread.Messages[0].ID != "message-0872" || second.Thread.Messages[63].ID != "message-0935" {
		t.Fatalf("earlier ids=%s..%s, want message-0872..message-0935", second.Thread.Messages[0].ID, second.Thread.Messages[63].ID)
	}
	seen := map[string]bool{}
	for _, message := range first.Thread.Messages {
		seen[message.ID] = true
	}
	for _, message := range second.Thread.Messages {
		if seen[message.ID] {
			t.Fatalf("cursor page overlapped first page at %s", message.ID)
		}
	}

	// Paging never weakens the thread ACL, and a made-up seam fails closed
	// instead of silently restarting at the latest messages.
	unauthorized := httptest.NewRequest(http.MethodGet, "/assistant/chat-threads/"+thread.ID+"?view=tail&limit=64", nil)
	for _, cookie := range loginAs(t, "tim@shareability.com", "B0NFIRE!") {
		unauthorized.AddCookie(cookie)
	}
	unauthorizedResponse := httptest.NewRecorder()
	assistantChatThreadHandler(unauthorizedResponse, unauthorized)
	if unauthorizedResponse.Code != http.StatusNotFound {
		t.Fatalf("private tail ACL status=%d body=%s", unauthorizedResponse.Code, unauthorizedResponse.Body.String())
	}
	stale := get(context.Background(), "/assistant/chat-threads/"+thread.ID+"?view=tail&before=not-a-server-cursor")
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale cursor status=%d body=%s, want 409", stale.Code, stale.Body.String())
	}

	// A reaction against an older, currently visible history record returns the
	// exact changed message plus a bounded live-edge repair tail. It must not
	// project or serialize all 1,005 records just because the durable mutation
	// rewrote the canonical thread.
	projectedMessageIDs = nil
	reactionRequest := httptest.NewRequest(http.MethodPut, "/assistant/chat-threads/"+thread.ID+"/messages/message-0200/reaction", strings.NewReader(`{"emoji":"❤️"}`))
	reactionRequest.Header.Set("Content-Type", "application/json")
	reactionRequest = reactionRequest.WithContext(probeContext)
	for _, cookie := range cookies {
		reactionRequest.AddCookie(cookie)
	}
	reactionResponse := httptest.NewRecorder()
	assistantChatThreadHandler(reactionResponse, reactionRequest)
	if reactionResponse.Code != http.StatusOK {
		t.Fatalf("reaction status=%d body=%s", reactionResponse.Code, reactionResponse.Body.String())
	}
	if size := reactionResponse.Body.Len(); size >= scoutChatHydrationResponseMax {
		t.Fatalf("reaction response=%d bytes, want <%d", size, scoutChatHydrationResponseMax)
	}
	var reaction struct {
		Thread  scoutChatThreadRecord  `json:"thread"`
		History scoutChatHistoryPage   `json:"history"`
		Message scoutChatMessageRecord `json:"message"`
	}
	if err := json.Unmarshal(reactionResponse.Body.Bytes(), &reaction); err != nil {
		t.Fatalf("decode reaction response: %v", err)
	}
	if len(reaction.Thread.Messages) != scoutChatHydrationDefaultLimit {
		t.Fatalf("reaction repair tail=%d messages, want %d", len(reaction.Thread.Messages), scoutChatHydrationDefaultLimit)
	}
	if reaction.Message.ID != "message-0200" || len(reaction.Message.Reactions) != 1 || reaction.Message.Reactions[0].Emoji != "❤️" {
		t.Fatalf("reaction exact message=%+v", reaction.Message)
	}
	if reaction.History.NextBeforeMessageID != "message-0925" {
		t.Fatalf("reaction cursor=%q, want message-0925", reaction.History.NextBeforeMessageID)
	}
	if len(projectedMessageIDs) != scoutChatHydrationDefaultLimit+1 {
		t.Fatalf("reaction projected %d messages, want exact target + %d-record repair tail", len(projectedMessageIDs), scoutChatHydrationDefaultLimit)
	}
	outsideTail := []string{}
	for _, messageID := range projectedMessageIDs {
		if messageID < "message-0925" {
			outsideTail = append(outsideTail, messageID)
		}
	}
	if len(outsideTail) != 1 || outsideTail[0] != "message-0200" {
		t.Fatalf("reaction projected old history %v, want only exact mutation target", outsideTail)
	}
	t.Logf("dense reaction acknowledgement: bytes=%d repair_messages=%d projected=%d", reactionResponse.Body.Len(), len(reaction.Thread.Messages), len(projectedMessageIDs))

	// The normal append handler uses the same response projector. Pin its
	// acknowledgement contract directly without invoking an external Scout
	// provider: exact committed message plus an 80-record repair tail, never the
	// thousand-record source thread.
	appended := scoutChatMessageRecord{
		ID: "message-1005", Kind: "message", Role: "user", AuthorName: "AJ", AuthorEmail: "aj@shareability.com",
		Text: "new bounded append", CreatedAt: base.Add(1005 * time.Second).Format(time.RFC3339Nano),
	}
	appendThread := thread
	appendThread.Messages = append(append([]scoutChatMessageRecord(nil), thread.Messages...), appended)
	appendProjections := []string{}
	appendContext := withScoutChatMessageProjectionProbe(context.Background(), func(messageID string) {
		appendProjections = append(appendProjections, messageID)
	})
	appendPayload := kanbanApp.projectScoutChatMutationResponseForViewer("aj@shareability.com", thread.ID, map[string]any{
		"ok": true, "thread": appendThread, "message": appended,
	}, appendContext)
	appendBody, err := json.Marshal(appendPayload)
	if err != nil {
		t.Fatalf("marshal append acknowledgement: %v", err)
	}
	if len(appendBody) >= scoutChatHydrationResponseMax {
		t.Fatalf("append acknowledgement=%d bytes, want <%d", len(appendBody), scoutChatHydrationResponseMax)
	}
	appendTail, ok := appendPayload["thread"].(scoutChatThreadRecord)
	if !ok || len(appendTail.Messages) != scoutChatHydrationDefaultLimit || appendTail.Messages[len(appendTail.Messages)-1].ID != appended.ID {
		t.Fatalf("append repair tail=%+v", appendPayload["thread"])
	}
	if len(appendProjections) != scoutChatHydrationDefaultLimit+1 {
		t.Fatalf("append projected %d messages, want exact + bounded tail", len(appendProjections))
	}
	t.Logf("dense append acknowledgement: bytes=%d repair_messages=%d projected=%d", len(appendBody), len(appendTail.Messages), len(appendProjections))
}

func TestScoutChatTailHydrationClosesReplyHeavyPageOverRoots(t *testing.T) {
	thread := scoutChatThreadRecord{ID: "reply-heavy", Visibility: scoutChatVisibilityPublic}
	base := time.Date(2026, time.August, 22, 16, 0, 0, 0, time.UTC)
	rootOld := scoutChatMessageRecord{ID: "root-old", Kind: "message", Role: "user", Text: "Older root", CreatedAt: base.Format(time.RFC3339Nano)}
	thread.Messages = append(thread.Messages, rootOld)
	for index := 0; index < 120; index++ {
		thread.Messages = append(thread.Messages, scoutChatMessageRecord{ID: fmt.Sprintf("filler-%03d", index), Kind: "message", Role: "user", Text: "root", CreatedAt: base.Add(time.Duration(index+1) * time.Second).Format(time.RFC3339Nano)})
	}
	rootCurrent := scoutChatMessageRecord{ID: "root-current", Kind: "message", Role: "user", Text: "Current root", CreatedAt: base.Add(121 * time.Second).Format(time.RFC3339Nano)}
	thread.Messages = append(thread.Messages, rootCurrent)
	for index := 0; index < 90; index++ {
		root := rootOld
		if index%2 == 1 {
			root = rootCurrent
		}
		thread.Messages = append(thread.Messages, scoutChatMessageRecord{
			ID: fmt.Sprintf("reply-%03d", index), Kind: "message", Role: "user", Text: "reply",
			CreatedAt: base.Add(time.Duration(122+index) * time.Second).Format(time.RFC3339Nano),
			ReplyTo:   &scoutChatReplyRef{MessageID: root.ID, AuthorName: "AJ", Text: root.Text},
		})
	}

	window, page, err := scoutChatTailSourceWindow(thread, "", 80, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(window.Messages) > scoutChatHydrationMaxLimit {
		t.Fatalf("reply-closed page=%d messages, want <=%d", len(window.Messages), scoutChatHydrationMaxLimit)
	}
	ids := map[string]bool{}
	for _, message := range window.Messages {
		ids[message.ID] = true
	}
	if !ids[rootOld.ID] || !ids[rootCurrent.ID] {
		t.Fatalf("reply-heavy tail omitted roots: old=%v current=%v", ids[rootOld.ID], ids[rootCurrent.ID])
	}
	if page.NextBeforeMessageID != "reply-010" {
		t.Fatalf("reply cursor=%q, want contiguous suffix boundary reply-010", page.NextBeforeMessageID)
	}
	if page.OldestMessageID != rootOld.ID {
		t.Fatalf("oldest projected id=%q, want injected root %q", page.OldestMessageID, rootOld.ID)
	}
	if page.ReplyCounts[rootOld.ID] != 45 || page.ReplyCounts[rootCurrent.ID] != 45 {
		t.Fatalf("reply totals=%v, want exact 45/45", page.ReplyCounts)
	}
	if !page.HasEarlier {
		t.Fatal("reply-heavy page lost earlier-history cursor")
	}
}

func TestScoutChatTailHydrationDeepReplyChainIsHardBoundedAndCursorStable(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	thread, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "Deep reply chain", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatalf("create thread: %v", err)
	}
	base := time.Date(2026, time.August, 22, 18, 0, 0, 0, time.UTC)
	largeText := strings.Repeat("rights-safe evidence ", 420)
	root := scoutChatMessageRecord{ID: "deep-root", Kind: "message", Role: "user", AuthorName: "AJ", AuthorEmail: "aj@shareability.com", Text: largeText, CreatedAt: base.Format(time.RFC3339Nano)}
	thread.Messages = []scoutChatMessageRecord{root}
	parent := root
	for index := 0; index < 6000; index++ {
		text := "nested reply"
		if index >= 5880 {
			text = largeText
		}
		message := scoutChatMessageRecord{
			ID: fmt.Sprintf("deep-reply-%04d", index), Kind: "message", Role: "user", AuthorName: "AJ", AuthorEmail: "aj@shareability.com",
			Text: text, CreatedAt: base.Add(time.Duration(index+1) * time.Second).Format(time.RFC3339Nano),
			ReplyTo: &scoutChatReplyRef{MessageID: parent.ID, AuthorName: parent.AuthorName, AuthorEmail: parent.AuthorEmail, Text: parent.Text},
		}
		thread.Messages = append(thread.Messages, message)
		parent = message
	}
	thread.UpdatedAt = parent.CreatedAt

	started := time.Now()
	window, page, err := scoutChatTailSourceWindow(thread, "", 100, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := len(window.Messages); got > scoutChatHydrationMaxLimit {
		t.Fatalf("deep-chain page=%d messages, want <=%d", got, scoutChatHydrationMaxLimit)
	}
	firstID := ""
	if len(window.Messages) > 0 {
		firstID = window.Messages[0].ID
	}
	if len(window.Messages) != scoutChatHydrationMaxLimit || firstID != root.ID {
		t.Fatalf("deep-chain window=%d first=%q, want hard-capped page with canonical root", len(window.Messages), firstID)
	}
	for _, message := range window.Messages[1:] {
		rootHint := ""
		if message.ReplyTo != nil {
			rootHint = message.ReplyTo.RootMessageID
		}
		if rootHint != root.ID {
			t.Fatalf("reply %q canonical root hint=%q, want %q", message.ID, rootHint, root.ID)
		}
		if message.ReplyTo.MessageID == root.ID && message.ID != "deep-reply-0000" {
			t.Fatalf("reply %q direct parent was rewritten instead of preserved", message.ID)
		}
	}
	if page.ReplyCounts[root.ID] != 6000 || !page.HasEarlier || page.NextBeforeMessageID == "" {
		t.Fatalf("deep-chain history=%+v", page)
	}
	firstIDs := map[string]bool{}
	for _, message := range window.Messages {
		firstIDs[message.ID] = true
	}
	earlier, earlierPage, err := scoutChatTailSourceWindow(thread, page.NextBeforeMessageID, 100, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(earlier.Messages) > scoutChatHydrationMaxLimit || earlierPage.NextBeforeMessageID == page.NextBeforeMessageID {
		t.Fatalf("earlier deep-chain page=%d history=%+v", len(earlier.Messages), earlierPage)
	}
	for _, message := range earlier.Messages {
		if message.ID != root.ID && firstIDs[message.ID] {
			t.Fatalf("exclusive-before deep-chain cursor overlapped at %q", message.ID)
		}
	}

	if err := kanbanApp.saveScoutChatThread(thread); err != nil {
		t.Fatalf("save deep thread: %v", err)
	}
	request := httptest.NewRequest(http.MethodGet, "/assistant/chat-threads/"+thread.ID+"?view=tail&limit=100", nil)
	for _, cookie := range loginAs(t, "aj@shareability.com", "B0NFIRE!") {
		request.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	assistantChatThreadHandler(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("deep-chain tail status=%d body=%s", response.Code, response.Body.String())
	}
	if response.Body.Len() > scoutChatHydrationResponseMax {
		t.Fatalf("deep-chain response=%d bytes, want <=%d", response.Body.Len(), scoutChatHydrationResponseMax)
	}
	var payload struct {
		Thread  scoutChatThreadRecord `json:"thread"`
		History scoutChatHistoryPage  `json:"history"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode deep-chain response: %v", err)
	}
	wireFirstID := ""
	if len(payload.Thread.Messages) > 0 {
		wireFirstID = payload.Thread.Messages[0].ID
	}
	if len(payload.Thread.Messages) > scoutChatHydrationMaxLimit || wireFirstID != root.ID {
		t.Fatalf("deep-chain wire page=%d first=%q", len(payload.Thread.Messages), wireFirstID)
	}
	t.Logf("deep reply tail: source=%d wire_messages=%d wire_bytes=%d root_resolution=%s", len(thread.Messages), len(payload.Thread.Messages), response.Body.Len(), time.Since(started))
}
