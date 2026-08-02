package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSTRIDETeamSuggestionProductionPathRecommendsNamedProjectWithoutStartingWork(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	app := newIsolatedKanbanBoardApp(t)
	if app.strideRuntime != nil {
		_ = app.strideRuntime.Close()
	}
	config := strideIntegratedRuntimeConfig(t.TempDir())
	config.ProductPreviewEnabled = true
	config.Now = func() time.Time { return time.Now().UTC() }
	runtime, err := NewSTRIDERuntime(config)
	if err != nil {
		t.Fatal(err)
	}
	app.strideRuntime = runtime
	t.Cleanup(func() { _ = app.Close() })

	team, err := app.createScoutChatThread("aj@shareability.com", "AJ", "team", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatal(err)
	}
	dog, err := app.createScoutChatThread("aj@shareability.com", "AJ", "Dog Perfect", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatal(err)
	}
	user := accountStore().findUser("tim@shareability.com")
	if user == nil {
		t.Fatal("seed user missing")
	}
	response, err := app.appendScoutChatThreadMessage(context.Background(), user, team.ID, "@scout create an Insights & Opportunities report for Dog Perfect", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	suggestion, ok := response["suggestion"].(STRIDEProductWorkRecord)
	if !ok || suggestion.DestinationThreadID != dog.ID || suggestion.DestinationRecommendation == nil || suggestion.DestinationRecommendation.Status != strideProductDestinationRecommended || suggestion.Revision != 1 || suggestion.Status != "suggested" || suggestion.RunID != "" {
		t.Fatalf("production-path recommendation=%+v response=%+v", suggestion, response)
	}
	answer, ok := response["answer"].(scoutChatMessageRecord)
	if !ok || !strings.Contains(answer.Text, dog.Title) || !strings.Contains(answer.Text, "nothing is running yet") {
		t.Fatalf("Scout did not explain the recommended, unrun destination: %+v", response["answer"])
	}
	if err := runtime.WithProductContext(canonicalTenantID(), STRIDEProductScopeWork, func(ctx STRIDEProductContext) error {
		ctx.WorkStore.mu.Lock()
		defer ctx.WorkStore.mu.Unlock()
		if len(ctx.WorkStore.Cards) != 0 || len(ctx.WorkStore.Runs) != 0 {
			t.Fatalf("recommendation launched before approval: cards=%d runs=%d", len(ctx.WorkStore.Cards), len(ctx.WorkStore.Runs))
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestSTRIDEProductDestinationRecommendationExactProjectMatch(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { _ = app.Close() })
	team, _, err := app.ensureScoutChatThread("team_exact_match", "aj@shareability.com", "AJ", "team", scoutChatVisibilityPublic, []string{"aj@shareability.com", "tim@shareability.com"})
	if err != nil {
		t.Fatal(err)
	}
	dog, _, err := app.ensureScoutChatThread("dog_perfect_exact_match", "aj@shareability.com", "AJ", "Dog Perfect", scoutChatVisibilityPublic, []string{"aj@shareability.com", "tim@shareability.com"})
	if err != nil {
		t.Fatal(err)
	}
	recipients := []string{strideRuntimePrincipalForEmail("aj@shareability.com"), strideRuntimePrincipalForEmail("tim@shareability.com")}
	proposal := app.strideProductRecommendDestination(team.ID, "Create the Dog Perfect launch report", strideRuntimeChatAudience(team), recipients, "aj@shareability.com", time.Date(2026, 8, 1, 18, 0, 0, 0, time.UTC))
	if proposal.Recommendation == nil || proposal.Recommendation.Status != strideProductDestinationRecommended || proposal.Recommendation.ThreadID != dog.ID || proposal.Recommendation.Title != dog.Title || proposal.Recommendation.Confidence != .96 || !strideWorkContainsString(proposal.Recommendation.EligibleThreadIDs, dog.ID) ||
		proposal.Recommendation.ParticipantEligibility != "eligible" || proposal.Recommendation.ACLVersion < 1 || proposal.Audience == nil || proposal.Recommendation.Audit.MatchBasis != "exact_project_title" || proposal.Recommendation.Audit.AuthorizedRelevantCandidates != 1 || !isHexDigest(proposal.Recommendation.Audit.Digest) {
		t.Fatalf("exact project recommendation=%+v audience=%+v", proposal.Recommendation, proposal.Audience)
	}
	event, message := strideDestinationTestConversation(team, "Create the Dog Perfect launch report", time.Date(2026, 8, 1, 18, 0, 0, 0, time.UTC))
	record, err := NewSTRIDEProductState().createSuggestionWithDestination(event, team, message, "Insights & Opportunities report", message.Text, event.AuthorPrincipal, event.IngestedAt, proposal)
	if err != nil || record.DestinationThreadID != dog.ID || record.DestinationRecommendation == nil || record.Revision != 1 || record.RunID != "" {
		t.Fatalf("persisted exact recommendation=%+v err=%v", record, err)
	}
}

func TestSTRIDEProductDestinationRecommendationAbstainsForAmbiguousUnauthorizedAndMissing(t *testing.T) {
	recipients := []string{strideRuntimePrincipalForEmail("aj@shareability.com"), strideRuntimePrincipalForEmail("tim@shareability.com")}
	now := time.Date(2026, 8, 1, 18, 10, 0, 0, time.UTC)
	for _, test := range []struct {
		name       string
		configure  func(*testing.T, *kanbanBoardApp)
		outcome    string
		matchBasis string
		relevant   int
		authorized int
	}{
		{
			name: "ambiguous", outcome: "Create the Dog Perfect report", matchBasis: "ambiguous", relevant: 2, authorized: 2,
			configure: func(t *testing.T, app *kanbanBoardApp) {
				if _, _, err := app.ensureScoutChatThread("dog_perfect_ambiguous_one", "aj@shareability.com", "AJ", "Dog Perfect", scoutChatVisibilityPublic, []string{"aj@shareability.com", "tim@shareability.com"}); err != nil {
					t.Fatal(err)
				}
				if _, _, err := app.ensureScoutChatThread("dog_perfect_ambiguous_two", "aj@shareability.com", "AJ", "Dog Perfect", scoutChatVisibilityPublic, []string{"aj@shareability.com", "tim@shareability.com"}); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "unauthorized", outcome: "Create the Dog Perfect report", matchBasis: "unauthorized", relevant: 1, authorized: 0,
			configure: func(t *testing.T, app *kanbanBoardApp) {
				if _, _, err := app.ensureScoutChatThread("dog_private_membership", "aj@shareability.com", "AJ", "Dog Perfect", scoutChatVisibilityPublic, []string{"aj@shareability.com"}); err != nil {
					t.Fatal(err)
				}
			},
		},
		{name: "missing", outcome: "Create the Northstar report", matchBasis: "no_match", relevant: 0, authorized: 0, configure: func(*testing.T, *kanbanBoardApp) {}},
	} {
		t.Run(test.name, func(t *testing.T) {
			setupAuthTestEnv(t)
			app := newIsolatedKanbanBoardApp(t)
			t.Cleanup(func() { _ = app.Close() })
			team, _, err := app.ensureScoutChatThread("team_"+test.name, "aj@shareability.com", "AJ", "team", scoutChatVisibilityPublic, []string{"aj@shareability.com", "tim@shareability.com"})
			if err != nil {
				t.Fatal(err)
			}
			test.configure(t, app)
			proposal := app.strideProductRecommendDestination(team.ID, test.outcome, strideRuntimeChatAudience(team), recipients, "aj@shareability.com", now)
			if proposal.Recommendation == nil || proposal.Recommendation.Status != strideProductDestinationManual || proposal.Recommendation.ThreadID != "" || proposal.Recommendation.Confidence != 0 || proposal.Recommendation.ParticipantEligibility != "unresolved" || proposal.Audience != nil ||
				proposal.Recommendation.Audit.MatchBasis != test.matchBasis || proposal.Recommendation.Audit.RelevantCandidates != test.relevant || proposal.Recommendation.Audit.AuthorizedRelevantCandidates != test.authorized || !isHexDigest(proposal.Recommendation.Audit.Digest) {
				t.Fatalf("abstention=%+v audience=%+v", proposal.Recommendation, proposal.Audience)
			}
			if test.name == "unauthorized" && strideWorkContainsString(proposal.Recommendation.EligibleThreadIDs, "dog_private_membership") {
				t.Fatalf("unauthorized thread leaked into eligible choices: %+v", proposal.Recommendation)
			}
			event, message := strideDestinationTestConversation(team, test.outcome, now)
			record, createErr := NewSTRIDEProductState().createSuggestionWithDestination(event, team, message, "Insights & Opportunities report", message.Text, event.AuthorPrincipal, event.IngestedAt, proposal)
			if createErr != nil || record.DestinationThreadID != "" || record.DestinationRecommendation == nil || record.DestinationRecommendation.Audit.MatchBasis != test.matchBasis || record.Revision != 1 || record.RunID != "" {
				t.Fatalf("persisted abstention=%+v err=%v", record, createErr)
			}
		})
	}
}

func strideDestinationTestConversation(thread scoutChatThreadRecord, text string, now time.Time) (ConversationEvent, scoutChatMessageRecord) {
	digest := temporalDigest(thread.ID + "\x00" + text)
	audience, aclVersion, _ := strideRuntimeChatAudienceAuthority(thread)
	message := scoutChatMessageRecord{ID: "message_" + digest[:20], Kind: "message", Role: "user", AuthorName: "AJ", AuthorEmail: "aj@shareability.com", Text: text, CreatedAt: now.UTC().Format(time.RFC3339Nano)}
	event := ConversationEvent{
		Header:     STRIDEContractHeader{TenantID: canonicalTenantID(), ID: "chat_event_" + digest[:20], Revision: 1, SchemaVersion: STRIDEContractSchemaVersion, ContractType: STRIDEContractConversationEvent, ContentDigest: digest, CreatedAt: now.UTC()},
		SourceType: "channel_message", SourceID: message.ID, ThreadID: thread.ID, AuthorPrincipal: strideRuntimePrincipalForEmail(message.AuthorEmail), AuthorName: message.AuthorName,
		OccurredAt: now.UTC(), IngestedAt: now.UTC(), EventType: "message", ContentRevision: 1, ContentDigest: digest, Audience: audience, ACLVersion: aclVersion,
		RetentionPolicy: "company_default", BodyRef: "chat_body_" + digest[:20], Provenance: "client",
	}
	return event, message
}

func TestSTRIDEProductRecommendedDestinationStillRequiresHumanApproval(t *testing.T) {
	fixture := newSTRIDEProjectAuthorityFixture(t)
	recommendation := fixture.suggestion.DestinationRecommendation
	if recommendation == nil || recommendation.Status != strideProductDestinationRecommended || recommendation.ThreadID != fixture.source.ID || fixture.suggestion.Revision != 1 || fixture.suggestion.Status != "suggested" || fixture.suggestion.RunID != "" {
		t.Fatalf("suggestion did not stop at a recommended, unrun destination: %+v", fixture.suggestion)
	}
	if err := fixture.runtime.WithProductContext(canonicalTenantID(), STRIDEProductScopeWork, func(ctx STRIDEProductContext) error {
		ctx.WorkStore.mu.Lock()
		defer ctx.WorkStore.mu.Unlock()
		if len(ctx.WorkStore.Runs) != 0 || len(ctx.WorkStore.Cards) != 0 {
			t.Fatalf("recommendation created orchestration state before approval: cards=%d runs=%d", len(ctx.WorkStore.Cards), len(ctx.WorkStore.Runs))
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	principal := strideRuntimePrincipalForEmail(fixture.user.Email)
	raw, err := json.Marshal(map[string]any{"Revision": fixture.suggestion.Revision})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/approve", bytes.NewReader(raw))
	recorder := httptest.NewRecorder()
	strideProductApprove(recorder, request, fixture.user, fixture.runtime, fixture.suggestion.ID, principal)
	if recorder.Code != http.StatusOK {
		t.Fatalf("approval status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Suggestion STRIDEProductWorkRecord `json:"suggestion"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Suggestion.Status != "completed" || response.Suggestion.RunID == "" || response.Suggestion.DestinationThreadID != fixture.source.ID || response.Suggestion.Revision != fixture.suggestion.Revision {
		t.Fatalf("approved recommendation lifecycle=%+v", response.Suggestion)
	}
	if err := fixture.runtime.WithProductContext(canonicalTenantID(), STRIDEProductScopeWork, func(ctx STRIDEProductContext) error {
		ctx.WorkStore.mu.Lock()
		defer ctx.WorkStore.mu.Unlock()
		if len(ctx.WorkStore.Runs) != 1 {
			t.Fatalf("approved recommendation runs=%d, want exactly one", len(ctx.WorkStore.Runs))
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
