package main

import (
	"context"
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
	publicationPrincipal := RecallPrincipal{
		ServiceID: "private-riff-publication", TenantID: canonicalArtifactTenantID(), Audience: "shared_channel", ThreadID: fixture.table.ID,
	}
	publicationEntries := fixture.app.authorizedSTRIDEConversationEntries(publicationPrincipal)
	if len(publicationEntries) != 1 || !strings.Contains(publicationEntries[0].Text, exactURL) || strings.Contains(publicationEntries[0].Text, privateCanary) {
		t.Fatalf("destination-bound publication recall=%+v", publicationEntries)
	}
	publicationSources, publicationManifest := privateRiffMemorySources(publicationEntries)
	if publicationManifest == "" || !fixture.app.privateRiffContextSourcesPublishable(context.Background(), fixture.table.ID, publicationSources) {
		t.Fatalf("authorized destination context could not be published: sources=%+v manifest=%q", publicationSources, publicationManifest)
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

func TestAgentThreadResearchContextEndsAtExactRequestMessage(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	fixture := newSTRIDECoworkerTestFixture(t)
	fixture.app.mu.Lock()
	fixture.app.apiKey = ""
	fixture.app.mu.Unlock()

	messages := []scoutChatMessageRecord{
		{ID: "bonfire-company-context", Kind: "message", Role: "user", Text: "Bonfire is the country culture experience network, built around original IP, community, and natural brand participation.", CreatedAt: time.Now().Add(-3 * time.Minute).UTC().Format(time.RFC3339Nano), AuthorName: "AJ", AuthorEmail: fixture.user.Email},
		{ID: "bonfire-research-request", Kind: "message", Role: "user", Text: "That's interesting. Scout, research comparable companies and write the elevator pitch.", CreatedAt: time.Now().Add(-2 * time.Minute).UTC().Format(time.RFC3339Nano), AuthorName: "AJ", AuthorEmail: fixture.user.Email},
		{ID: "later-unapproved-turn", Kind: "message", Role: "user", Text: "PRIVATE-LATER-DIRECTION-MUST-NOT-ENTER-APPROVED-RUN", CreatedAt: time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano), AuthorName: "AJ", AuthorEmail: fixture.user.Email},
	}
	if _, err := fixture.app.commitScoutChatThreadMessages(fixture.user.Email, fixture.table.ID, messages...); err != nil {
		t.Fatal(err)
	}
	principal := recallPrincipalForUser(fixture.user)
	principal.Audience = "shared_channel"
	principal.ThreadID = fixture.table.ID
	currentThread, _, err := fixture.app.scoutChatThreadByID(fixture.user.Email, fixture.table.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, sourceBinding, err := scoutChatSourceWindow(currentThread, "bonfire-research-request")
	if err != nil {
		t.Fatal(err)
	}
	metadata := map[string]string{
		"originKind":          agentThreadOriginChannel,
		"originId":            fixture.table.ID,
		"sourceMessageId":     sourceBinding.MessageID,
		"sourceMessageDigest": sourceBinding.MessageDigest,
		"sourceWindowDigest":  sourceBinding.WindowDigest,
	}
	entries, err := fixture.app.agentThreadSourceConversationEntries(principal, metadata)
	if err != nil {
		t.Fatal(err)
	}
	joined := ""
	for _, entry := range entries {
		joined += "\n" + entry.Text
	}
	if !strings.Contains(joined, "country culture experience network") || !strings.Contains(joined, "research comparable companies") {
		t.Fatalf("source-bound context omitted company/request: %s", joined)
	}
	if strings.Contains(joined, "PRIVATE-LATER-DIRECTION") {
		t.Fatalf("post-approval turn entered source-bound context: %s", joined)
	}
	currentThread.Title = "renamed after approval"
	if err := fixture.app.saveScoutChatThread(currentThread); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.app.agentThreadSourceConversationEntries(principal, metadata); err == nil {
		t.Fatal("channel-title change altered the provider projection without invalidating approval")
	}
	metadata["sourceWindowDigest"] = strings.Repeat("0", 64)
	if _, err := fixture.app.agentThreadSourceConversationEntries(principal, metadata); err == nil {
		t.Fatal("changed approved conversation window did not fail closed")
	}
	metadata["sourceWindowDigest"] = sourceBinding.WindowDigest
	metadata["sourceMessageId"] = "missing-message"
	if _, err := fixture.app.agentThreadSourceConversationEntries(principal, metadata); err == nil {
		t.Fatal("missing exact request message did not fail closed")
	}
}

func TestAgentThreadTerminalSourceAuthorityIsHeldThroughFinalEffect(t *testing.T) {
	fixture := newSTRIDECoworkerTestFixture(t)
	message := scoutChatMessageRecord{ID: "held-source", Kind: "message", Role: "user", Text: "Research the exact approved company context.", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), AuthorName: "AJ", AuthorEmail: fixture.user.Email}
	if _, err := fixture.app.commitScoutChatThreadMessages(fixture.user.Email, fixture.table.ID, message); err != nil {
		t.Fatal(err)
	}
	current, _, err := fixture.app.scoutChatThreadByID(fixture.user.Email, fixture.table.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, binding, err := scoutChatSourceWindow(current, message.ID)
	if err != nil {
		t.Fatal(err)
	}
	thread := scoutAgentThread{Artifact: meetingMemoryEntry{Metadata: map[string]string{
		"originKind": agentThreadOriginChannel, "originId": fixture.table.ID, "requestedBy": fixture.user.Email,
		"sourceMessageId": binding.MessageID, "sourceMessageDigest": binding.MessageDigest, "sourceWindowDigest": binding.WindowDigest,
	}}}
	entered, release, effectDone := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		effectDone <- fixture.app.withCurrentAgentThreadSource(thread, func() error {
			close(entered)
			<-release
			return nil
		})
	}()
	<-entered
	mutationDone := make(chan error, 1)
	go func() {
		_, mutationErr := fixture.app.commitScoutChatThreadMessages(fixture.user.Email, fixture.table.ID, scoutChatMessageRecord{ID: "post-approval-mutation", Kind: "message", Role: "user", Text: "must wait for terminal write", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), AuthorName: "AJ", AuthorEmail: fixture.user.Email})
		mutationDone <- mutationErr
	}()
	select {
	case mutationErr := <-mutationDone:
		t.Fatalf("source mutation escaped terminal authority lock: %v", mutationErr)
	case <-time.After(75 * time.Millisecond):
	}
	close(release)
	if err := <-effectDone; err != nil {
		t.Fatal(err)
	}
	if err := <-mutationDone; err != nil {
		t.Fatal(err)
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
