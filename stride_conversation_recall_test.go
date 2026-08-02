package main

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSTRIDECompanyConversationRecallUsesCurrentPublicSourceAndReactionState(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	fixture := newSTRIDECoworkerTestFixture(t)
	fixture.app.mu.Lock()
	fixture.app.apiKey = ""
	fixture.app.mu.Unlock()

	const (
		exactURL      = "https://example.com/launch?campaign=mercury#creative"
		privateCanary = "PRIVATE-CHANNEL-LINK-CANARY-8842"
	)
	query := "what was the link Erick shared in #team last week?"
	start, end, ok := relativeQueryTimeRange(query, time.Now())
	if !ok {
		t.Fatal("last-week query did not produce a deterministic range")
	}
	sharedAt := start.Add(end.Sub(start) / 2).UTC()
	publicMessage := scoutChatMessageRecord{
		ID: "team-erick-launch-link", Kind: "message", Role: "user",
		Text:      "Here is the launch reference " + exactURL,
		Files:     []scoutChatFileAttachment{{Name: "launch-brief.pdf", Kind: "file", Mime: "application/pdf"}},
		CreatedAt: sharedAt.Format(time.RFC3339Nano), AuthorName: "Erick", AuthorEmail: "e@shareability.com",
	}
	if _, err := fixture.app.commitScoutChatThreadMessages("e@shareability.com", fixture.table.ID, publicMessage); err != nil {
		t.Fatalf("commit public message: %v", err)
	}
	private, err := fixture.app.createScoutChatThread("e@shareability.com", "Erick", "Private link", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.app.commitScoutChatThreadMessages("e@shareability.com", private.ID, scoutChatMessageRecord{
		ID: "private-link", Kind: "message", Role: "user", Text: privateCanary + " https://private.example/secret",
		CreatedAt: sharedAt.Format(time.RFC3339Nano), AuthorName: "Erick", AuthorEmail: "e@shareability.com",
	}); err != nil {
		t.Fatal(err)
	}

	if _, _, err := fixture.app.updateScoutChatMessageReaction(fixture.user, fixture.table.ID, publicMessage.ID, "👍", true); err != nil {
		t.Fatalf("set production reaction: %v", err)
	}
	projection := strideConversationProjectionForTest(t, fixture.runtime, fixture.user.Email, publicMessage.ID)
	if len(projection.AttachmentRefs) != 1 || len(projection.LinkRefs) != 1 || len(projection.ReactionActors) != 1 {
		t.Fatalf("rich production projection incomplete: %+v", projection)
	}
	if _, _, err := fixture.app.updateScoutChatMessageReaction(fixture.user, fixture.table.ID, publicMessage.ID, "👍", false); err != nil {
		t.Fatalf("clear production reaction: %v", err)
	}
	projection = strideConversationProjectionForTest(t, fixture.runtime, fixture.user.Email, publicMessage.ID)
	if len(projection.ReactionActors) != 0 {
		t.Fatalf("cleared reaction remained projected before restart: %+v", projection.ReactionActors)
	}

	// Prove the explicit empty reaction set survives the signed runtime
	// snapshot/restart path instead of being reinterpreted as a legacy event.
	if err := fixture.runtime.Close(); err != nil {
		t.Fatal(err)
	}
	restartConfig := strideIntegratedRuntimeConfig(filepath.Join(fixture.dir, "runtime"))
	restartConfig.ProductPreviewEnabled = true
	restartConfig.RecallThreadIDs = []string{fixture.table.ID}
	restartConfig.BootstrapEmpty = false
	restarted, err := NewSTRIDERuntime(restartConfig)
	if err != nil {
		t.Fatalf("restart STRIDE runtime: %v", err)
	}
	t.Cleanup(func() { _ = restarted.Close() })
	fixture.app.strideRuntime = restarted
	projection = strideConversationProjectionForTest(t, restarted, fixture.user.Email, publicMessage.ID)
	if len(projection.ReactionActors) != 0 || len(projection.AttachmentRefs) != 1 || len(projection.LinkRefs) != 1 {
		t.Fatalf("restart projection lost current rich state: %+v", projection)
	}

	principal := fixture.app.recallPrincipalForMemberRoom(fixture.user.Email, "meeting-room")
	entries := fixture.app.authorizedSTRIDEConversationEntries(principal)
	if len(entries) != 1 || entries[0].Kind != memoryContextKindCompanyConversation || !strings.Contains(entries[0].Text, exactURL) || !strings.Contains(entries[0].Text, "launch-brief.pdf") {
		t.Fatalf("authorized company-conversation join=%+v", entries)
	}
	if strings.Contains(entries[0].Text, privateCanary) {
		t.Fatalf("private channel entered authorized join: %+v", entries[0])
	}
	if guest := fixture.app.authorizedSTRIDEConversationEntries(recallPrincipalForGuest("guest-1", "meeting-room", "sitting-1")); len(guest) != 0 {
		t.Fatalf("guest received company conversation: %+v", guest)
	}
	if matches := fixture.app.memory.search(privateCanary, 20); len(matches) != 0 {
		t.Fatalf("raw private Scout chat became searchable: %+v", matches)
	}
	for _, transcript := range fixture.app.memory.entriesOfKind(meetingMemoryKindTranscript, 0) {
		if strings.Contains(transcript.Text, exactURL) {
			t.Fatalf("channel link was injected into a meeting transcript: %+v", transcript)
		}
	}

	previousProbe := recallModelContextProbe
	var modelContext []meetingMemoryEntry
	recallModelContextProbe = func(entries []meetingMemoryEntry) { modelContext = append([]meetingMemoryEntry(nil), entries...) }
	t.Cleanup(func() { recallModelContextProbe = previousProbe })
	result, _, err := fixture.app.applyToolCallArgsForPrincipal("answer_memory_question", map[string]any{"query": query}, principal)
	if err != nil {
		t.Fatalf("production recall tool: %v", err)
	}
	answer, _ := result["answer"].(string)
	if !strings.Contains(answer, exactURL) || strings.Contains(answer, privateCanary) {
		t.Fatalf("production recall answer=%q", answer)
	}
	foundAuthorizedContext := false
	for _, entry := range modelContext {
		if entry.Kind == memoryContextKindCompanyConversation && strings.Contains(entry.Text, exactURL) {
			foundAuthorizedContext = true
		}
		if strings.Contains(entry.Text, privateCanary) {
			t.Fatalf("private channel entered model context: %+v", entry)
		}
	}
	if !foundAuthorizedContext {
		t.Fatalf("company conversation did not reach the production model-context seam: %+v", modelContext)
	}
}

func strideConversationProjectionForTest(t *testing.T, runtime *STRIDERuntime, email, sourceID string) STRIDEConversationMessageProjection {
	t.Helper()
	var found STRIDEConversationMessageProjection
	err := runtime.WithTenantDomains(canonicalTenantID(), func(domains STRIDERuntimeDomains) error {
		rows, err := domains.ConversationLedger.ProjectForTenantPrincipal(canonicalTenantID(), strideRuntimePrincipalForEmail(email))
		if err != nil {
			return err
		}
		for _, row := range rows {
			if row.SourceID == sourceID {
				found = row
				break
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if found.SourceID == "" {
		t.Fatalf("projection for %s not found", sourceID)
	}
	return found
}
