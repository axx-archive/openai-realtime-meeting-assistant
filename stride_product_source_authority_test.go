package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSTRIDEWorkApprovalRejectsEditedOrDeletedConversationSourceAcrossRestart(t *testing.T) {
	for _, eventType := range []string{"edit", "delete"} {
		t.Run(eventType, func(t *testing.T) {
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

			channel, err := app.createScoutChatThread("aj@shareability.com", "AJ", "team", scoutChatVisibilityPublic)
			if err != nil {
				t.Fatal(err)
			}
			project, err := app.createScoutChatThread("aj@shareability.com", "AJ", "Dog Perfect", scoutChatVisibilityPublic)
			if err != nil {
				t.Fatal(err)
			}
			user := accountStore().findUser("tim@shareability.com")
			if user == nil {
				t.Fatal("seed user missing")
			}

			providerCalls := 0
			previousOpenAI := createOpenAITextResponse
			createOpenAITextResponse = func(context.Context, string, openAITextRequest) (string, error) {
				providerCalls++
				return "", errors.New("provider must remain fenced")
			}
			t.Cleanup(func() { createOpenAITextResponse = previousOpenAI })

			response, err := app.appendScoutChatThreadMessage(context.Background(), user, channel.ID, "@scout create an Insights & Opportunities report for Dog Perfect", nil, "")
			if err != nil {
				t.Fatal(err)
			}
			suggestion := response["suggestion"].(STRIDEProductWorkRecord)
			principal := strideRuntimePrincipalForEmail(user.Email)
			var selected STRIDEProductWorkRecord
			if err := runtime.WithProductContext(canonicalTenantID(), STRIDEProductScopeWork, func(ctx STRIDEProductContext) error {
				selected, err = ctx.Product.reviseWork(suggestion.ID, suggestion.Revision, principal, func(record *STRIDEProductWorkRecord) error {
					record.DestinationMode = "existing"
					record.DestinationThreadID = project.ID
					record.DestinationTitle = project.Title
					audience := strideRuntimeOrganizationAudience()
					record.DestinationAudience = &audience
					record.DestinationACLVersion = 1
					record.Lifecycle = append(record.Lifecycle, "destination_explicitly_selected")
					return nil
				}, ctx.Receipt.IssuedAt)
				return err
			}); err != nil {
				t.Fatalf("select destination: %v", err)
			}
			if err := runtime.Save(); err != nil {
				t.Fatal(err)
			}

			saved, _, err := app.scoutChatThreadByID(user.Email, channel.ID)
			if err != nil {
				t.Fatal(err)
			}
			index := scoutChatMessageIndex(saved, suggestion.SourceMessageID)
			if index < 0 {
				t.Fatal("source message missing")
			}
			source := saved.Messages[index]
			source.EditedAt = time.Now().UTC().Add(time.Second).Format(time.RFC3339Nano)
			if eventType == "edit" {
				source.Text = "This request was materially changed and is no longer approved evidence."
			}
			if _, err := app.projectSTRIDETeamChatMessage(saved, source, eventType, user.Email); err != nil {
				t.Fatalf("project %s: %v", eventType, err)
			}
			if err := runtime.Save(); err != nil {
				t.Fatal(err)
			}

			assertDenied := func(candidate *STRIDERuntime) {
				t.Helper()
				if err := candidate.WithProductContext(canonicalTenantID(), STRIDEProductScopeWork, func(ctx STRIDEProductContext) error {
					_, approvalErr := ctx.approveAndRunWork(principal, selected.ID, selected.Revision, ctx.Receipt.IssuedAt)
					if !errors.Is(approvalErr, ErrSTRIDEWorkSourceChanged) {
						t.Fatalf("approval error=%v, want source changed", approvalErr)
					}
					ctx.WorkStore.mu.Lock()
					defer ctx.WorkStore.mu.Unlock()
					if len(ctx.WorkStore.Intents) != 0 || len(ctx.WorkStore.Runs) != 0 {
						t.Fatalf("stale source created durable work: intents=%d runs=%d", len(ctx.WorkStore.Intents), len(ctx.WorkStore.Runs))
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			}
			assertDenied(runtime)
			if err := runtime.Close(); err != nil {
				t.Fatal(err)
			}

			config.BootstrapEmpty = false
			restarted, err := NewSTRIDERuntime(config)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = restarted.Close() })
			assertDenied(restarted)
			if providerCalls != 0 {
				t.Fatalf("stale source reached provider %d times", providerCalls)
			}
		})
	}
}

func TestSTRIDEWorkApprovalReauthorizesMeetingTranscriptRevision(t *testing.T) {
	for _, purged := range []bool{false, true} {
		name := "current"
		if purged {
			name = "purged"
		}
		t.Run(name, func(t *testing.T) {
			t.Setenv("OPENAI_API_KEY", "")
			t.Setenv("ANTHROPIC_API_KEY", "")
			config := strideIntegratedRuntimeConfig(t.TempDir())
			config.ProductPreviewEnabled = true
			runtime, err := NewSTRIDERuntime(config)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = runtime.Close() })

			principal := strideRuntimePrincipalForEmail("aj@shareability.com")
			reviewer := strideRuntimePrincipalForEmail("tim@shareability.com")
			roomID, sittingID := "office", "sitting_source_authority"
			start := time.Date(2026, 7, 30, 19, 50, 0, 0, time.UTC)
			audience := STRIDEAudience{Visibility: "meeting", Principals: []string{principal, reviewer}}
			event := strideTemporalProductTranscriptEvent(config.TenantID, roomID, sittingID, 1, start, start.Add(time.Minute), audience, "We need an Insights & Opportunities report for Dog Perfect.")
			brainConfig := TemporalMeetingBrainConfig{TenantID: config.TenantID, RoomID: roomID, SittingID: sittingID, SittingStart: start.Add(-time.Minute)}
			if err := runtime.ApplyTemporalEvidence(config.TenantID, brainConfig, event); err != nil {
				t.Fatal(err)
			}
			ref := referenceFromHeader(event.Transcript.Revision.Header)
			result := STRIDETemporalRecallResult{
				RoomID: roomID, SittingID: sittingID, Text: "Create an Insights & Opportunities report for Dog Perfect.",
				Evidence: []STRIDEReference{ref}, EvidenceDigest: temporalDigest("meeting-source-authority"),
			}
			var selected STRIDEProductWorkRecord
			if err := runtime.WithProductContext(config.TenantID, STRIDEProductScopeWork, func(ctx STRIDEProductContext) error {
				record, createErr := ctx.Product.createMeetingSuggestion(result, principal, []string{principal, reviewer}, ctx.Receipt.IssuedAt)
				if createErr != nil {
					return createErr
				}
				selected, createErr = ctx.Product.reviseWork(record.ID, record.Revision, principal, func(work *STRIDEProductWorkRecord) error {
					work.DestinationMode = "existing"
					work.DestinationThreadID = "project_dog_perfect"
					work.DestinationTitle = "Dog Perfect"
					destinationAudience := STRIDEAudience{Visibility: "project", Principals: []string{principal, reviewer}}
					work.DestinationAudience = &destinationAudience
					work.DestinationACLVersion = 1
					work.Lifecycle = append(work.Lifecycle, "destination_explicitly_selected")
					return nil
				}, ctx.Receipt.IssuedAt)
				return createErr
			}); err != nil {
				t.Fatal(err)
			}

			if purged {
				purge := TemporalMeetingEvent{Sequence: 2, Kind: TemporalMeetingEventPurge, Purge: &TemporalPurgeEvent{
					TenantID: config.TenantID, SegmentID: event.Transcript.Revision.SegmentID, RevisionID: ref.ID, PurgeGeneration: 1,
				}}
				if err := runtime.ApplyTemporalEvidence(config.TenantID, brainConfig, purge); err != nil {
					t.Fatal(err)
				}
			}

			if err := runtime.WithProductContext(config.TenantID, STRIDEProductScopeWork, func(ctx STRIDEProductContext) error {
				completed, approvalErr := ctx.approveAndRunWork(principal, selected.ID, selected.Revision, ctx.Receipt.IssuedAt)
				if purged {
					if !errors.Is(approvalErr, ErrSTRIDEWorkSourceChanged) {
						t.Fatalf("purged approval error=%v, want source changed", approvalErr)
					}
					ctx.WorkStore.mu.Lock()
					defer ctx.WorkStore.mu.Unlock()
					if len(ctx.WorkStore.Intents) != 0 || len(ctx.WorkStore.Runs) != 0 {
						t.Fatalf("purged source created work: intents=%d runs=%d", len(ctx.WorkStore.Intents), len(ctx.WorkStore.Runs))
					}
					return nil
				}
				if approvalErr != nil || completed.Status != "completed" || completed.RunID == "" {
					t.Fatalf("current meeting source approval=%+v err=%v", completed, approvalErr)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSTRIDEMeetingWorkBindsFullEvidenceSetAndInvalidatesAnyRevisionAcrossRestart(t *testing.T) {
	for _, mutation := range []string{"current", "corrected", "purged"} {
		t.Run(mutation, func(t *testing.T) {
			for _, key := range []string{"OPENAI_API_KEY", "ANTHROPIC_API_KEY", "FISCAL_API_KEY", "FISCAL_AI_API_KEY", "OPENAI_REALTIME_API_KEY", "OPENAI_TRANSCRIPTION_API_KEY"} {
				t.Setenv(key, "")
			}
			providerCalls := 0
			previousOpenAI := createOpenAITextResponse
			previousAnthropic := createAnthropicTextResponse
			createOpenAITextResponse = func(context.Context, string, openAITextRequest) (string, error) {
				providerCalls++
				return "", errors.New("provider must remain fenced")
			}
			createAnthropicTextResponse = func(context.Context, string, anthropicTextRequest) (string, error) {
				providerCalls++
				return "", errors.New("provider must remain fenced")
			}
			t.Cleanup(func() {
				createOpenAITextResponse = previousOpenAI
				createAnthropicTextResponse = previousAnthropic
			})

			config := strideIntegratedRuntimeConfig(t.TempDir())
			config.ProductPreviewEnabled = true
			runtime, err := NewSTRIDERuntime(config)
			if err != nil {
				t.Fatal(err)
			}

			principal := strideRuntimePrincipalForEmail("aj@shareability.com")
			reviewer := strideRuntimePrincipalForEmail("tim@shareability.com")
			recipients := uniqueSortedStrings([]string{principal, reviewer})
			roomID, sittingID := "office", "sitting_full_evidence"
			start := time.Date(2026, 7, 31, 17, 0, 0, 0, time.UTC)
			audience := STRIDEAudience{Visibility: "meeting", Principals: recipients}
			first := strideSourceAuthorityTranscriptEvent(config.TenantID, roomID, sittingID, 9, 1, "a", start, start.Add(time.Minute), audience, "We need an Insights & Opportunities report for Dog Perfect.")
			second := strideSourceAuthorityTranscriptEvent(config.TenantID, roomID, sittingID, 9, 2, "z", start.Add(time.Minute), start.Add(2*time.Minute), audience, "The report should cover positioning, risks, and launch opportunities.")
			brainConfig := TemporalMeetingBrainConfig{TenantID: config.TenantID, RoomID: roomID, SittingID: sittingID, SittingStart: start.Add(-time.Minute)}
			if err := runtime.ApplyTemporalEvidence(config.TenantID, brainConfig, first); err != nil {
				t.Fatal(err)
			}
			if err := runtime.ApplyTemporalEvidence(config.TenantID, brainConfig, second); err != nil {
				t.Fatal(err)
			}
			firstRef := referenceFromHeader(first.Transcript.Revision.Header)
			secondRef := referenceFromHeader(second.Transcript.Revision.Header)
			canonical := uniqueSortedSTRIDEReferences([]STRIDEReference{secondRef, firstRef})
			result := STRIDETemporalRecallResult{
				RoomID: roomID, SittingID: sittingID,
				Text:     "Create an Insights & Opportunities report for Dog Perfect covering positioning, risks, and launch opportunities.",
				Evidence: []STRIDEReference{secondRef, firstRef}, EvidenceDigest: workDigest(canonical),
			}
			var selected STRIDEProductWorkRecord
			if err := runtime.WithProductContext(config.TenantID, STRIDEProductScopeWork, func(ctx STRIDEProductContext) error {
				record, createErr := ctx.Product.createMeetingSuggestion(result, principal, recipients, ctx.Receipt.IssuedAt)
				if createErr != nil {
					return createErr
				}
				if !sameOrderedSTRIDEReferences(record.SourceEvents, canonical) || record.SourceEvent != canonical[0] {
					t.Fatalf("meeting suggestion evidence=%+v primary=%+v want=%+v", record.SourceEvents, record.SourceEvent, canonical)
				}
				replayed, replayErr := ctx.Product.createMeetingSuggestion(result, principal, recipients, ctx.Receipt.IssuedAt)
				if replayErr != nil || replayed.ID != record.ID {
					t.Fatalf("full evidence replay=%+v err=%v", replayed, replayErr)
				}
				reducedResult := result
				reducedResult.Evidence = []STRIDEReference{firstRef}
				reducedResult.EvidenceDigest = workDigest(reducedResult.Evidence)
				reduced, reducedErr := ctx.Product.createMeetingSuggestion(reducedResult, principal, recipients, ctx.Receipt.IssuedAt)
				if reducedErr != nil || reduced.ID == record.ID {
					t.Fatalf("evidence-set identity did not change: full=%s reduced=%s err=%v", record.ID, reduced.ID, reducedErr)
				}
				widerAudience := strideRuntimeOrganizationAudience()
				wider, widerErr := ctx.Product.reviseWork(reduced.ID, reduced.Revision, principal, func(work *STRIDEProductWorkRecord) error {
					work.DestinationMode = "existing"
					work.DestinationThreadID = "org_public_project"
					work.DestinationTitle = "Organization-wide project"
					work.DestinationAudience = &widerAudience
					work.DestinationACLVersion = 1
					return nil
				}, ctx.Receipt.IssuedAt)
				if widerErr != nil {
					t.Fatalf("create wider destination fixture: %v", widerErr)
				}
				if _, widerErr = ctx.approveAndRunWork(principal, wider.ID, wider.Revision, ctx.Receipt.IssuedAt); !errors.Is(widerErr, ErrSTRIDEProductDenied) {
					t.Fatalf("meeting evidence widened into org project: err=%v", widerErr)
				}
				ctx.WorkStore.mu.Lock()
				if len(ctx.WorkStore.Intents) != 0 || len(ctx.WorkStore.Runs) != 0 {
					ctx.WorkStore.mu.Unlock()
					t.Fatalf("wider meeting destination created work")
				}
				ctx.WorkStore.mu.Unlock()
				selected, createErr = ctx.Product.reviseWork(record.ID, record.Revision, principal, func(work *STRIDEProductWorkRecord) error {
					work.DestinationMode = "existing"
					work.DestinationThreadID = "project_dog_perfect"
					work.DestinationTitle = "Dog Perfect"
					destinationAudience := STRIDEAudience{Visibility: "project", Principals: recipients}
					work.DestinationAudience = &destinationAudience
					work.DestinationACLVersion = 7
					work.Lifecycle = append(work.Lifecycle, "destination_explicitly_selected")
					return nil
				}, ctx.Receipt.IssuedAt)
				return createErr
			}); err != nil {
				t.Fatal(err)
			}
			if err := runtime.Save(); err != nil {
				t.Fatal(err)
			}
			if err := runtime.Close(); err != nil {
				t.Fatal(err)
			}

			config.BootstrapEmpty = false
			restarted, err := NewSTRIDERuntime(config)
			if err != nil {
				t.Fatal(err)
			}
			if mutation == "corrected" {
				corrected := strideSourceAuthorityCorrection(second, 3, "The second source was corrected and cannot authorize the old suggestion.")
				if err := restarted.ApplyTemporalEvidence(config.TenantID, brainConfig, corrected); err != nil {
					t.Fatal(err)
				}
			}
			if mutation == "purged" {
				purge := TemporalMeetingEvent{Sequence: 3, Kind: TemporalMeetingEventPurge, Purge: &TemporalPurgeEvent{
					TenantID: config.TenantID, SegmentID: second.Transcript.Revision.SegmentID, RevisionID: secondRef.ID, PurgeGeneration: 1,
				}}
				if err := restarted.ApplyTemporalEvidence(config.TenantID, brainConfig, purge); err != nil {
					t.Fatal(err)
				}
			}

			var completed STRIDEProductWorkRecord
			approvalErr := restarted.WithProductContext(config.TenantID, STRIDEProductScopeWork, func(ctx STRIDEProductContext) error {
				var runErr error
				completed, runErr = ctx.approveAndRunWork(principal, selected.ID, selected.Revision, ctx.Receipt.IssuedAt)
				if mutation != "current" {
					if !errors.Is(runErr, ErrSTRIDEWorkSourceChanged) {
						t.Fatalf("%s approval error=%v, want source changed", mutation, runErr)
					}
					stored, found := ctx.Product.workRecord(selected.ID)
					if !found || !stored.SourceInvalidated || stored.Status != "failed" || stored.FailureReason != "source_invalidated" || stored.SourceSnippet != "" || stored.CompletionSummary != "" || stored.Revision != selected.Revision+1 || !sameOrderedSTRIDEReferences(stored.SourceEvents, canonical) {
						t.Fatalf("invalidated record=%+v", stored)
					}
					ctx.WorkStore.mu.Lock()
					defer ctx.WorkStore.mu.Unlock()
					if len(ctx.WorkStore.Intents) != 0 || len(ctx.WorkStore.Runs) != 0 || len(ctx.WorkStore.Artifacts) != 0 || len(ctx.WorkStore.Outcomes) != 0 {
						t.Fatalf("%s stale evidence crossed work boundary: %+v", mutation, ctx.WorkStore)
					}
					return nil
				}
				if runErr != nil || completed.Status != "completed" || !sameOrderedSTRIDEReferences(completed.SourceEvents, canonical) {
					t.Fatalf("current full-evidence completion=%+v err=%v", completed, runErr)
				}
				ctx.WorkStore.mu.Lock()
				defer ctx.WorkStore.mu.Unlock()
				intent := ctx.WorkStore.Intents["intent_"+temporalDigest(selected.ID)[:20]]
				run := ctx.WorkStore.Runs[completed.RunID]
				artifact := ctx.WorkStore.Artifacts["artifact_"+completed.RunID]
				outcome := ctx.WorkStore.Outcomes["outcome_"+completed.RunID]
				if !sameOrderedSTRIDEReferences(intent.Evidence, canonical) || !sameOrderedSTRIDEReferences(run.Evidence, canonical) || !sameOrderedSTRIDEReferences(artifact.Evidence, canonical) || !sameOrderedSTRIDEReferences(outcome.Evidence, canonical) {
					t.Fatalf("full evidence did not propagate: intent=%+v run=%+v artifact=%+v outcome=%+v", intent.Evidence, run.Evidence, artifact.Evidence, outcome.Evidence)
				}
				if completed.DestinationAudience == nil || !sameAudience(run.Destination.Audience, *completed.DestinationAudience) || !sameAudience(artifact.Audience, *completed.DestinationAudience) || !sameAudience(outcome.Audience, *completed.DestinationAudience) || run.Destination.ACLVersion != completed.DestinationACLVersion {
					t.Fatalf("destination authority not bound: completed=%+v run=%+v artifact=%+v outcome=%+v", completed.DestinationAudience, run.Destination, artifact.Destination, outcome.Destination)
				}
				return nil
			})
			if approvalErr != nil {
				t.Fatal(approvalErr)
			}
			if err := restarted.Save(); err != nil {
				t.Fatal(err)
			}
			if err := restarted.Close(); err != nil {
				t.Fatal(err)
			}

			finalRuntime, err := NewSTRIDERuntime(config)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = finalRuntime.Close() })
			if err := finalRuntime.WithProductContext(config.TenantID, STRIDEProductScopeWork, func(ctx STRIDEProductContext) error {
				stored, found := ctx.Product.workRecord(selected.ID)
				if !found || !sameOrderedSTRIDEReferences(stored.SourceEvents, canonical) {
					t.Fatalf("restart lost evidence set: %+v", stored)
				}
				if mutation == "current" && stored.Status != "completed" {
					t.Fatalf("restart completion status=%s", stored.Status)
				}
				if mutation != "current" && (!stored.SourceInvalidated || stored.Status != "failed") {
					t.Fatalf("restart resurrected stale work: %+v", stored)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if providerCalls != 0 {
				t.Fatalf("provider calls=%d, want zero", providerCalls)
			}
		})
	}
}

func TestSTRIDEProductReadPathsReauthorizeAndRedactCompletedWork(t *testing.T) {
	setupAuthTestEnv(t)
	for _, key := range []string{"OPENAI_API_KEY", "ANTHROPIC_API_KEY", "FISCAL_API_KEY", "FISCAL_AI_API_KEY", "OPENAI_REALTIME_API_KEY", "OPENAI_TRANSCRIPTION_API_KEY"} {
		t.Setenv(key, "")
	}
	app := newIsolatedKanbanBoardApp(t)
	if app.strideRuntime != nil {
		_ = app.strideRuntime.Close()
	}
	config := strideIntegratedRuntimeConfig(t.TempDir())
	config.ProductPreviewEnabled = true
	runtime, err := NewSTRIDERuntime(config)
	if err != nil {
		t.Fatal(err)
	}
	app.strideRuntime = runtime
	previousApp := kanbanApp
	kanbanApp = app
	t.Cleanup(func() {
		_ = app.Close()
		kanbanApp = previousApp
	})

	channel, err := app.createScoutChatThread("aj@shareability.com", "AJ", "team", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatal(err)
	}
	project, err := app.createScoutChatThread("aj@shareability.com", "AJ", "Dog Perfect", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatal(err)
	}
	user := accountStore().findUser("tim@shareability.com")
	if user == nil {
		t.Fatal("seed user missing")
	}
	response, err := app.appendScoutChatThreadMessage(context.Background(), user, channel.ID, "@scout create an Insights & Opportunities report for Dog Perfect", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	suggestion := response["suggestion"].(STRIDEProductWorkRecord)
	principal := strideRuntimePrincipalForEmail(user.Email)
	destinationAudience, destinationACLVersion, err := strideProductProjectDestinationAuthority(project)
	if err != nil {
		t.Fatal(err)
	}
	var completed STRIDEProductWorkRecord
	if err := runtime.WithProductContext(config.TenantID, STRIDEProductScopeWork, func(ctx STRIDEProductContext) error {
		selected, selectErr := ctx.Product.reviseWork(suggestion.ID, suggestion.Revision, principal, func(record *STRIDEProductWorkRecord) error {
			record.DestinationMode = "existing"
			record.DestinationThreadID = project.ID
			record.DestinationTitle = project.Title
			audience := cloneAudience(destinationAudience)
			record.DestinationAudience = &audience
			record.DestinationACLVersion = destinationACLVersion
			record.Lifecycle = append(record.Lifecycle, "destination_explicitly_selected")
			return nil
		}, ctx.Receipt.IssuedAt)
		if selectErr != nil {
			return selectErr
		}
		completed, selectErr = ctx.approveAndRunWork(principal, selected.ID, selected.Revision, ctx.Receipt.IssuedAt)
		return selectErr
	}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Save(); err != nil {
		t.Fatal(err)
	}

	saved, _, err := app.scoutChatThreadByID(user.Email, channel.ID)
	if err != nil {
		t.Fatal(err)
	}
	index := scoutChatMessageIndex(saved, suggestion.SourceMessageID)
	if index < 0 {
		t.Fatal("source message missing")
	}
	edited := saved.Messages[index]
	edited.Text = "The request has been withdrawn."
	edited.EditedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := app.projectSTRIDETeamChatMessage(saved, edited, "edit", user.Email); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Save(); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	registerSTRIDERuntimeRoutes(mux)
	cookies := loginAs(t, user.Email, defaultMeetingRoomPassword)
	request := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		for _, cookie := range cookies {
			req.AddCookie(cookie)
		}
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, req)
		return recorder
	}
	evidence := request(completed.BrainHref)
	if evidence.Code != http.StatusGone {
		t.Fatalf("stale evidence status=%d body=%s", evidence.Code, evidence.Body.String())
	}
	artifact := request(completed.ArtifactHref)
	if artifact.Code != http.StatusGone {
		t.Fatalf("stale artifact status=%d body=%s", artifact.Code, artifact.Body.String())
	}
	individual := request(strideRuntimeAPIBase + "work/suggestions/" + completed.ID)
	if individual.Code != http.StatusOK {
		t.Fatalf("redacted suggestion status=%d body=%s", individual.Code, individual.Body.String())
	}
	var individualBody struct {
		SourceCurrent bool                    `json:"sourceCurrent"`
		Suggestion    STRIDEProductWorkRecord `json:"suggestion"`
	}
	if err := json.Unmarshal(individual.Body.Bytes(), &individualBody); err != nil {
		t.Fatal(err)
	}
	redacted := individualBody.Suggestion
	if individualBody.SourceCurrent || !redacted.SourceInvalidated || redacted.Status != "failed" || redacted.FailureReason != "source_invalidated" || redacted.SourceThreadID != "" || redacted.SourceMessageID != "" || redacted.SourceEvent != (STRIDEReference{}) || len(redacted.SourceEvents) != 0 || redacted.SourceSnippet != "" || redacted.ArtifactHref != "" || redacted.BrainHref != "" || redacted.CompletionSummary != "" {
		t.Fatalf("invalidated suggestion leaked source-derived data: %+v", redacted)
	}
	list := request(strideRuntimeAPIBase + "work")
	if list.Code != http.StatusOK {
		t.Fatalf("redacted work list status=%d body=%s", list.Code, list.Body.String())
	}
	var listBody struct {
		Suggestions []STRIDEProductWorkRecord `json:"suggestions"`
		Runs        []STRIDEDurableWorkRun    `json:"runs"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &listBody); err != nil {
		t.Fatal(err)
	}
	if len(listBody.Suggestions) != 1 || !listBody.Suggestions[0].SourceInvalidated || listBody.Suggestions[0].SourceThreadID != "" || len(listBody.Runs) != 0 {
		t.Fatalf("work list did not redact invalidated lineage: %+v", listBody)
	}

	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	config.BootstrapEmpty = false
	restarted, err := NewSTRIDERuntime(config)
	if err != nil {
		t.Fatal(err)
	}
	app.strideRuntime = restarted
	restartedRead := request(strideRuntimeAPIBase + "work/suggestions/" + completed.ID)
	if restartedRead.Code != http.StatusOK || !strings.Contains(restartedRead.Body.String(), `"sourceInvalidated":true`) || strings.Contains(restartedRead.Body.String(), suggestion.SourceMessageID) {
		t.Fatalf("restart did not preserve redaction status=%d body=%s", restartedRead.Code, restartedRead.Body.String())
	}
}

func strideSourceAuthorityTranscriptEvent(tenantID, roomID, sittingID string, generation, sequence uint64, suffix string, start, end time.Time, audience STRIDEAudience, text string) TemporalMeetingEvent {
	event := strideTemporalProductTranscriptEvent(tenantID, roomID, sittingID, generation, start, end, audience, text)
	event.Sequence = sequence
	event.Transcript.Conversation.Header.ID = "temporal_product_conversation_" + suffix
	event.Transcript.Conversation.SourceID = "temporal_product_source_" + suffix
	event.Transcript.Conversation.BodyRef = "temporal_product_body_" + suffix
	event.Transcript.Segment.Header.ID = "temporal_product_segment_" + suffix
	event.Transcript.Segment.ConversationRef = referenceFromHeader(event.Transcript.Conversation.Header)
	event.Transcript.Segment.CaptureSequence = sequence
	event.Transcript.Revision.Header.ID = "temporal_product_revision_" + suffix
	event.Transcript.Revision.SegmentID = event.Transcript.Segment.Header.ID
	event.Transcript.Revision.Evidence = []STRIDEReference{referenceFromHeader(event.Transcript.Segment.Header)}
	return event
}

func strideSourceAuthorityCorrection(prior TemporalMeetingEvent, sequence uint64, text string) TemporalMeetingEvent {
	corrected := prior
	corrected.Sequence = sequence
	digest := temporalDigest(text)
	oldRevision := prior.Transcript.Revision
	corrected.Transcript.Revision.Header.ID = oldRevision.Header.ID + "_corrected"
	corrected.Transcript.Revision.Header.Revision = oldRevision.Header.Revision + 1
	corrected.Transcript.Revision.Header.ContentDigest = digest
	corrected.Transcript.Revision.Revision = oldRevision.Revision + 1
	corrected.Transcript.Revision.TextDigest = digest
	corrected.Transcript.Revision.Status = "corrected"
	corrected.Transcript.Revision.SupersedesID = oldRevision.Header.ID
	corrected.Transcript.Text = text
	return corrected
}
