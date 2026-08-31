package main

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestSTRIDEScoutRichActionsRemainUnavailableUntilIndividuallyAdmitted(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	fixture := newSTRIDECoworkerTestFixture(t)

	gifBytes := []byte{71, 73, 70, 56, 57, 97, 1, 0, 1, 0, 128, 0, 0, 0, 0, 0, 255, 255, 255, 33, 249, 4, 1, 0, 0, 0, 0, 44, 0, 0, 0, 0, 1, 0, 1, 0, 0, 2, 2, 68, 1, 0, 59}
	ref, err := putBlob(gifBytes, "image/gif")
	if err != nil {
		t.Fatal(err)
	}
	meta, err := blobStatForRef(ref)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := fixture.app.memory.appendEntry(meetingMemoryKindFile, "drive_launch_brief", "body is not file authority", map[string]string{
		"name": "launch-brief.gif", "blobRef": ref, "mime": meta.Mime, "size": fmt.Sprint(meta.Size),
		"uploaderEmail": fixture.user.Email, "uploaderName": fixture.user.Name, "origin": "files", "brainStatus": fileBrainStatusStored,
	}); err != nil {
		t.Fatal(err)
	}

	providerCalls := 0
	originalTextResponse := createOpenAITextResponse
	createOpenAITextResponse = func(context.Context, string, openAITextRequest) (string, error) {
		providerCalls++
		return "", fmt.Errorf("provider must remain fenced")
	}
	t.Cleanup(func() { createOpenAITextResponse = originalTextResponse })
	fixture.app.mu.Lock()
	fixture.app.apiKey = "must-not-be-used"
	fixture.app.mu.Unlock()

	fileResponse, err := fixture.app.appendScoutChatThreadMessage(context.Background(), fixture.user, fixture.table.ID, "@scout share the launch-brief.gif file", nil, "")
	if err != nil {
		t.Fatalf("actual file chat turn: %v", err)
	}
	fileAnswer, ok := fileResponse["answer"].(scoutChatMessageRecord)
	if !ok || fileAnswer.IntentOutcome != string(conversationIntentUnavailable) || len(fileAnswer.Files) != 0 {
		t.Fatalf("file answer=%+v response=%+v", fileAnswer, fileResponse)
	}
	if fileResponse["intentOutcome"] != string(conversationIntentUnavailable) || fileResponse["file"] != nil || fileResponse["providerCalls"] != 0 || fileResponse["dependencyRequired"] != true {
		t.Fatalf("unadmitted file action did not fail closed: %+v", fileResponse)
	}
	if providerCalls != 0 {
		t.Fatalf("rich action called conversational provider %d times", providerCalls)
	}

	current, _, err := fixture.app.scoutChatThreadByID(fixture.user.Email, fixture.table.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(current.Messages) != 2 || current.Messages[1].ID != fileAnswer.ID {
		t.Fatalf("unavailable response was not durably posted exactly once: %+v", current.Messages)
	}

	ordinary := scoutChatMessageRecord{ID: "ordinary", Role: "user", Text: "@scout what did you think of that meeting?", AuthorEmail: fixture.user.Email, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	if _, explicit := fixture.app.planExplicitSTRIDEScoutChatRichAction(context.Background(), fixture.user, current, ordinary); explicit {
		t.Fatal("general social question autonomously selected a rich side effect")
	}
	directAmbiguous := scoutChatMessageRecord{ID: "direct-ambiguous", Role: "user", Text: "@scout send a GIF", AuthorEmail: fixture.user.Email, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	emptyContext := current
	emptyContext.Messages = nil
	plan, explicit := fixture.app.planExplicitSTRIDEScoutChatRichAction(context.Background(), fixture.user, emptyContext, directAmbiguous)
	if !explicit || plan.clarification == "" || plan.responseMode != STRIDEScoutResponseText {
		t.Fatalf("ambiguous direct GIF request did not abstain: explicit=%v plan=%+v", explicit, plan)
	}
	ambiguous := scoutChatMessageRecord{ID: "ambiguous", Role: "user", Text: "@scout what did you think of that?", AuthorEmail: fixture.user.Email, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	ambiguousContext := current
	ambiguousContext.Messages = []scoutChatMessageRecord{{ID: "ambiguous-prior", Kind: "message", Role: "user", Text: "Let’s revisit the agenda tomorrow.", AuthorEmail: fixture.user.Email}}
	plan, explicit = fixture.app.planExplicitSTRIDEScoutChatRichAction(context.Background(), fixture.user, ambiguousContext, ambiguous)
	if !explicit || plan.clarification == "" || plan.responseMode != STRIDEScoutResponseText {
		t.Fatalf("ambiguous contextual request did not abstain: explicit=%v plan=%+v", explicit, plan)
	}
	sensitiveContext := current
	sensitiveContext.Messages = []scoutChatMessageRecord{{ID: "sensitive-prior", Kind: "message", Role: "user", Text: "The layoffs announcement was unbelievable.", AuthorEmail: fixture.user.Email}}
	sensitive := scoutChatMessageRecord{ID: "sensitive", Role: "user", Text: "@scout what did you think of that?", AuthorEmail: fixture.user.Email, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	plan, explicit = fixture.app.planExplicitSTRIDEScoutChatRichAction(context.Background(), fixture.user, sensitiveContext, sensitive)
	if !explicit || plan.responseMode != STRIDEScoutResponseSafeRefusal || plan.clarification == "" {
		t.Fatalf("sensitive contextual request did not fail closed: explicit=%v plan=%+v", explicit, plan)
	}
	harassmentContext := current
	harassmentContext.Messages = []scoutChatMessageRecord{{ID: "harassment-prior", Kind: "message", Role: "user", Text: "Erick is ridiculous and incompetent.", AuthorEmail: fixture.user.Email}}
	plan, explicit = fixture.app.planExplicitSTRIDEScoutChatRichAction(context.Background(), fixture.user, harassmentContext, sensitive)
	if !explicit || plan.responseMode != STRIDEScoutResponseSafeRefusal || plan.clarification == "" {
		t.Fatalf("person-targeted reaction did not fail closed: explicit=%v plan=%+v", explicit, plan)
	}
	mixedContext := current
	mixedContext.Messages = []scoutChatMessageRecord{{ID: "mixed-prior", Kind: "message", Role: "user", Text: "That was ridiculous, but congratulations—we shipped.", AuthorEmail: fixture.user.Email}}
	plan, explicit = fixture.app.planExplicitSTRIDEScoutChatRichAction(context.Background(), fixture.user, mixedContext, sensitive)
	if !explicit || plan.responseMode != STRIDEScoutResponseText || plan.clarification == "" {
		t.Fatalf("mixed contextual semantics did not abstain: explicit=%v plan=%+v", explicit, plan)
	}
	projectChannel := current
	projectChannel.Table = false
	if _, explicit := fixture.app.planExplicitSTRIDEScoutChatRichAction(context.Background(), fixture.user, projectChannel, sensitive); explicit {
		t.Fatal("contextual GIF behavior escaped the durable #team channel")
	}
}

func TestSTRIDEScoutContextualGIFUsesExplicitReplyAndNeverSearchesPastScout(t *testing.T) {
	fixture := newSTRIDECoworkerTestFixture(t)
	target := scoutChatMessageRecord{ID: "reply-target", Kind: "message", Role: "user", Text: "That idea is ridiculous.", AuthorName: fixture.user.Name, AuthorEmail: fixture.user.Email}
	scoutIntervening := scoutChatMessageRecord{ID: "scout-intervening", Kind: "message", Role: "scout", Text: "Tell me more.", AuthorName: scoutParticipantName}
	thread := fixture.table
	thread.Messages = []scoutChatMessageRecord{target, scoutIntervening}
	source := scoutChatMessageRecord{
		ID: "reply-source", Kind: "message", Role: "user", Text: "@scout what did you think of that?", AuthorEmail: fixture.user.Email,
		ReplyTo: &scoutChatReplyRef{MessageID: target.ID, AuthorName: target.AuthorName, AuthorEmail: target.AuthorEmail, Text: target.Text},
	}
	plan, explicit := fixture.app.planExplicitSTRIDEScoutChatRichAction(context.Background(), fixture.user, thread, source)
	if !explicit || plan.responseMode != STRIDEScoutResponseGIFOnly || plan.reaction != "facepalm" || plan.tone != "dry" || plan.clarification != "" {
		t.Fatalf("explicit reply did not select its exact safe context: explicit=%v plan=%+v", explicit, plan)
	}

	source.ReplyTo.Text = "That was hilarious."
	plan, explicit = fixture.app.planExplicitSTRIDEScoutChatRichAction(context.Background(), fixture.user, thread, source)
	if !explicit || plan.responseMode != STRIDEScoutResponseText || plan.clarification == "" {
		t.Fatalf("tampered reply snapshot was accepted: explicit=%v plan=%+v", explicit, plan)
	}

	source.ReplyTo = nil
	plan, explicit = fixture.app.planExplicitSTRIDEScoutChatRichAction(context.Background(), fixture.user, thread, source)
	if !explicit || plan.responseMode != STRIDEScoutResponseText || plan.clarification == "" {
		t.Fatalf("planner searched past intervening Scout message: explicit=%v plan=%+v", explicit, plan)
	}
}
