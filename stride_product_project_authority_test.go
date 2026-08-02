package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type strideProjectAuthorityFixture struct {
	app        *kanbanBoardApp
	runtime    *STRIDERuntime
	config     STRIDERuntimeConfig
	user       *userAccount
	source     scoutChatThreadRecord
	suggestion STRIDEProductWorkRecord
}

func newSTRIDEProjectAuthorityFixture(t *testing.T) strideProjectAuthorityFixture {
	t.Helper()
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
	previous := kanbanApp
	kanbanApp = app
	t.Cleanup(func() {
		kanbanApp = previous
		_ = app.Close()
	})

	source, _, err := app.ensureScoutChatThread(
		"stride_project_authority_source",
		"tim@shareability.com",
		"Tim",
		"Member Project",
		scoutChatVisibilityPublic,
		[]string{"caitlyn@shareability.com"},
	)
	if err != nil {
		t.Fatal(err)
	}
	user := accountStore().findUser("tim@shareability.com")
	if user == nil {
		t.Fatal("seed user missing")
	}
	response, err := app.appendScoutChatThreadMessage(context.Background(), user, source.ID, "@scout create an Insights & Opportunities report for this project", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	suggestion, ok := response["suggestion"].(STRIDEProductWorkRecord)
	if !ok {
		t.Fatalf("suggestion=%#v", response["suggestion"])
	}
	return strideProjectAuthorityFixture{app: app, runtime: runtime, config: config, user: user, source: source, suggestion: suggestion}
}

func TestSTRIDEProjectSuggestionReviewerStaysInsideSourceAudience(t *testing.T) {
	fixture := newSTRIDEProjectAuthorityFixture(t)
	tim := strideRuntimePrincipalForEmail("tim@shareability.com")
	caitlyn := strideRuntimePrincipalForEmail("caitlyn@shareability.com")
	aj := strideRuntimePrincipalForEmail(artifactLibraryAdminEmail)
	if len(fixture.suggestion.RecipientIDs) != 2 || !strideWorkContainsString(fixture.suggestion.RecipientIDs, tim) ||
		!strideWorkContainsString(fixture.suggestion.RecipientIDs, caitlyn) || strideWorkContainsString(fixture.suggestion.RecipientIDs, aj) {
		t.Fatalf("member project recipients escaped source audience: %v", fixture.suggestion.RecipientIDs)
	}
	if err := fixture.runtime.WithProductContext(canonicalTenantID(), STRIDEProductScopeWork, func(ctx STRIDEProductContext) error {
		if _, _, _, err := ctx.reauthorizeWorkForRead(aj, fixture.suggestion.ID, ctx.Receipt.IssuedAt); !errors.Is(err, ErrSTRIDEProductDenied) {
			t.Fatalf("outsider read error=%v, want denied", err)
		}
		record, found := ctx.Product.workRecord(fixture.suggestion.ID)
		if !found || record.SourceInvalidated {
			t.Fatalf("outsider invalidated member work: %+v", record)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestSTRIDEProjectProposalRolesFenceMereRecipientMutationAndApproval(t *testing.T) {
	fixture := newSTRIDEProjectAuthorityFixture(t)
	owner := strideRuntimePrincipalForEmail("tim@shareability.com")
	reviewer := strideRuntimePrincipalForEmail("caitlyn@shareability.com")
	mereRecipient := strideRuntimePrincipalForEmail("aj@shareability.com")
	if fixture.suggestion.OwnerID != owner || fixture.suggestion.ReviewerID != reviewer || fixture.suggestion.ApprovalPolicy.Revision != 1 ||
		!sameStringSet(fixture.suggestion.ApprovalPolicy.EligiblePrincipals, []string{owner, reviewer}) {
		t.Fatalf("proposal role authority=%+v", fixture.suggestion)
	}
	if err := fixture.runtime.WithProductContext(canonicalTenantID(), STRIDEProductScopeWork, func(ctx STRIDEProductContext) error {
		ctx.Product.mu.Lock()
		record := ctx.Product.work[fixture.suggestion.ID]
		record.RecipientIDs = uniqueSortedStrings(append(record.RecipientIDs, mereRecipient))
		ctx.Product.work[record.ID] = record
		ctx.Product.mu.Unlock()
		if _, err := ctx.Product.reviseWork(record.ID, record.Revision, mereRecipient, func(value *STRIDEProductWorkRecord) error { value.Title = "unauthorized"; return nil }, ctx.Receipt.IssuedAt); !errors.Is(err, ErrSTRIDEProductConflict) {
			t.Fatalf("mere recipient mutation error=%v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	user := accountStore().findUser("aj@shareability.com")
	req := httptest.NewRequest(http.MethodPost, "/destination", strings.NewReader(`{"Revision":1,"Mode":"new","Title":"Unauthorized"}`))
	recorder := httptest.NewRecorder()
	strideProductSetDestination(recorder, req, user, fixture.runtime, fixture.suggestion.ID, mereRecipient)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("mere recipient destination status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestSTRIDEProductApprovalResumesExactlyOnceAcrossDurableCrashBoundaries(t *testing.T) {
	for _, boundary := range []string{"approval_consumed", "stable_run_claimed", "lifecycle_checkpoint_saved", "artifact_saved", "run_completed", "outcome_saved", "product_completion_saved"} {
		t.Run(boundary, func(t *testing.T) {
			fixture := newSTRIDEProjectAuthorityFixture(t)
			principal := strideRuntimePrincipalForEmail(fixture.user.Email)
			destinationReq := httptest.NewRequest(http.MethodPost, "/destination", strings.NewReader(`{"Revision":1,"Mode":"new","Title":"Crash Safe"}`))
			destinationRecorder := httptest.NewRecorder()
			strideProductSetDestination(destinationRecorder, destinationReq, fixture.user, fixture.runtime, fixture.suggestion.ID, principal)
			if destinationRecorder.Code != http.StatusOK {
				t.Fatalf("destination status=%d body=%s", destinationRecorder.Code, destinationRecorder.Body.String())
			}

			injected := errors.New("injected durable restart")
			fired := false
			t.Cleanup(func() { strideProductLifecycleCheckpointHook = nil })
			strideProductLifecycleCheckpointHook = func(name string) error {
				if !fired && name == boundary {
					fired = true
					return injected
				}
				return nil
			}
			err := fixture.runtime.WithProductContext(canonicalTenantID(), STRIDEProductScopeWork, func(ctx STRIDEProductContext) error {
				_, runErr := ctx.approveAndRunWork(principal, fixture.suggestion.ID, 2, ctx.Receipt.IssuedAt)
				return runErr
			})
			strideProductLifecycleCheckpointHook = nil
			if !fired || !errors.Is(err, injected) {
				t.Fatalf("boundary %q fired=%t err=%v", boundary, fired, err)
			}

			fixture.config.BootstrapEmpty = false
			restarted, err := NewSTRIDERuntime(fixture.config)
			if err != nil {
				t.Fatal(err)
			}
			fixture.app.strideRuntime = restarted
			if err = restarted.WithProductContext(canonicalTenantID(), STRIDEProductScopeWork, func(ctx STRIDEProductContext) error {
				completed, runErr := ctx.approveAndRunWork(principal, fixture.suggestion.ID, 2, ctx.Receipt.IssuedAt)
				if runErr != nil {
					return runErr
				}
				if completed.Status != "completed" {
					t.Fatalf("completed=%+v", completed)
				}
				ctx.WorkStore.mu.Lock()
				defer ctx.WorkStore.mu.Unlock()
				if len(ctx.WorkStore.Runs) != 1 || len(ctx.WorkStore.Artifacts) != 1 || len(ctx.WorkStore.Outcomes) != 1 {
					t.Fatalf("duplicate durable effects runs=%d artifacts=%d outcomes=%d", len(ctx.WorkStore.Runs), len(ctx.WorkStore.Artifacts), len(ctx.WorkStore.Outcomes))
				}
				for _, run := range ctx.WorkStore.Runs {
					if len(run.Checkpoints) != 1 {
						t.Fatalf("checkpoints=%d", len(run.Checkpoints))
					}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSTRIDENewProjectDestinationIsDeterministicMemberScopedAcrossRestartReplay(t *testing.T) {
	fixture := newSTRIDEProjectAuthorityFixture(t)
	principal := strideRuntimePrincipalForEmail(fixture.user.Email)
	body := `{"Revision":1,"Mode":"new","Title":"Bound Project"}`
	invoke := func(runtime *STRIDERuntime) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/destination", strings.NewReader(body))
		recorder := httptest.NewRecorder()
		strideProductSetDestination(recorder, req, fixture.user, runtime, fixture.suggestion.ID, principal)
		return recorder
	}
	first := invoke(fixture.runtime)
	if first.Code != http.StatusOK {
		t.Fatalf("first destination status=%d body=%s", first.Code, first.Body.String())
	}
	var firstBody struct {
		Suggestion STRIDEProductWorkRecord `json:"suggestion"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &firstBody); err != nil {
		t.Fatal(err)
	}
	destinationID := firstBody.Suggestion.DestinationThreadID
	thread, _, err := fixture.app.scoutChatThreadByID(fixture.user.Email, destinationID)
	if err != nil {
		t.Fatal(err)
	}
	if got := scoutChatThreadMemberEmails(thread); len(got) != 2 || got[0] != "caitlyn@shareability.com" || got[1] != "tim@shareability.com" {
		t.Fatalf("destination members=%v", got)
	}
	if _, _, err := fixture.app.scoutChatThreadByID("aj@shareability.com", destinationID); err == nil {
		t.Fatal("nonrecipient can read new project destination")
	}

	if err := fixture.runtime.Close(); err != nil {
		t.Fatal(err)
	}
	fixture.config.BootstrapEmpty = false
	restarted, err := NewSTRIDERuntime(fixture.config)
	if err != nil {
		t.Fatal(err)
	}
	fixture.runtime = restarted
	fixture.app.strideRuntime = restarted
	replay := invoke(restarted)
	if replay.Code != http.StatusOK || !strings.Contains(replay.Body.String(), `"replayed":true`) {
		t.Fatalf("restart replay status=%d body=%s", replay.Code, replay.Body.String())
	}
	count := 0
	for _, entry := range fixture.app.memory.snapshot(0) {
		if entry.Kind == meetingMemoryKindScoutChat && entry.ID == destinationID {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("deterministic destination records=%d, want one", count)
	}
	approve := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/approve", strings.NewReader(`{"Revision":2}`))
		recorder := httptest.NewRecorder()
		strideProductApprove(recorder, req, fixture.user, restarted, fixture.suggestion.ID, principal)
		return recorder
	}
	approved := approve()
	if approved.Code != http.StatusOK {
		t.Fatalf("approve status=%d body=%s", approved.Code, approved.Body.String())
	}
	replayedApproval := approve()
	if replayedApproval.Code != http.StatusOK {
		t.Fatalf("approve replay status=%d body=%s", replayedApproval.Code, replayedApproval.Body.String())
	}
	thread, _, err = fixture.app.scoutChatThreadByID(fixture.user.Email, destinationID)
	if err != nil {
		t.Fatal(err)
	}
	completionCount := 0
	for _, message := range thread.Messages {
		if strings.HasPrefix(message.ID, "stride-work-completion-") {
			completionCount++
		}
	}
	if completionCount != 1 {
		t.Fatalf("completion messages=%d, want one", completionCount)
	}
}

func TestSTRIDEProductCompletionProjectionDoesNotReenterRuntimeLock(t *testing.T) {
	fixture := newSTRIDEProjectAuthorityFixture(t)
	principal := strideRuntimePrincipalForEmail(fixture.user.Email)
	destinationRequest := httptest.NewRequest(http.MethodPost, "/destination", strings.NewReader(`{"Revision":1,"Mode":"new","Title":"Projection Safe"}`))
	destinationRecorder := httptest.NewRecorder()
	strideProductSetDestination(destinationRecorder, destinationRequest, fixture.user, fixture.runtime, fixture.suggestion.ID, principal)
	if destinationRecorder.Code != http.StatusOK {
		t.Fatalf("destination status=%d body=%s", destinationRecorder.Code, destinationRecorder.Body.String())
	}

	approve := func() *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/approve", strings.NewReader(`{"Revision":2}`))
		recorder := httptest.NewRecorder()
		strideProductApprove(recorder, request, fixture.user, fixture.runtime, fixture.suggestion.ID, principal)
		return recorder
	}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- approve() }()
	var approved *httptest.ResponseRecorder
	select {
	case approved = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("completion projection re-entered STRIDERuntime.mu")
	}
	if approved.Code != http.StatusOK {
		t.Fatalf("approve status=%d body=%s", approved.Code, approved.Body.String())
	}
	var payload struct {
		Suggestion STRIDEProductWorkRecord `json:"suggestion"`
	}
	if err := json.Unmarshal(approved.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if !payload.Suggestion.CompletionPosted || payload.Suggestion.RunID == "" || payload.Suggestion.DestinationThreadID == "" {
		t.Fatalf("completion was not atomically recorded: %+v", payload.Suggestion)
	}
	messageID := "stride-work-completion-" + temporalDigest(payload.Suggestion.ID + "\x00" + payload.Suggestion.RunID)[:20]
	thread, _, err := fixture.app.scoutChatThreadByID(fixture.user.Email, payload.Suggestion.DestinationThreadID)
	if err != nil {
		t.Fatal(err)
	}
	messageCount := 0
	for _, message := range thread.Messages {
		if message.ID == messageID {
			messageCount++
		}
	}
	projectedCount := 0
	if err := fixture.runtime.WithTenantDomains(canonicalTenantID(), func(domains STRIDERuntimeDomains) error {
		snapshot, snapshotErr := domains.ConversationLedger.Snapshot()
		if snapshotErr != nil {
			return snapshotErr
		}
		for _, event := range snapshot.Events {
			if event.Append.Event.SourceID == messageID && event.Append.Event.ThreadID == thread.ID {
				projectedCount++
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if messageCount != 1 || projectedCount != 1 {
		t.Fatalf("completion exact-once message=%d projection=%d", messageCount, projectedCount)
	}
	if replay := approve(); replay.Code != http.StatusOK {
		t.Fatalf("approval replay status=%d body=%s", replay.Code, replay.Body.String())
	}
	thread, _, err = fixture.app.scoutChatThreadByID(fixture.user.Email, payload.Suggestion.DestinationThreadID)
	if err != nil {
		t.Fatal(err)
	}
	messageCount = 0
	for _, message := range thread.Messages {
		if message.ID == messageID {
			messageCount++
		}
	}
	if messageCount != 1 {
		t.Fatalf("approval replay duplicated completion messages=%d", messageCount)
	}
}

func TestSTRIDEProjectDestinationAuthorityBindsTitleMembershipAndArchive(t *testing.T) {
	setupAuthTestEnv(t)
	base := scoutChatThreadRecord{ID: "stride_project_bound", Title: "Bound", OwnerEmail: "tim@shareability.com", Visibility: scoutChatVisibilityPublic, MemberEmails: []string{"caitlyn@shareability.com", "tim@shareability.com"}}
	audience, version, err := strideProductProjectDestinationAuthority(base)
	if err != nil || audience.Visibility != "project" {
		t.Fatalf("base authority=%+v version=%d err=%v", audience, version, err)
	}
	renamed := base
	renamed.Title = "Renamed"
	_, renamedVersion, err := strideProductProjectDestinationAuthority(renamed)
	if err != nil || renamedVersion == version {
		t.Fatalf("rename did not change bound revision: before=%d after=%d err=%v", version, renamedVersion, err)
	}
	changedMembers := base
	changedMembers.MemberEmails = append(changedMembers.MemberEmails, "aj@shareability.com")
	_, memberVersion, err := strideProductProjectDestinationAuthority(changedMembers)
	if err != nil || memberVersion == version {
		t.Fatalf("membership did not change bound revision: before=%d after=%d err=%v", version, memberVersion, err)
	}
	archived := base
	archived.ArchivedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if _, _, err := strideProductProjectDestinationAuthority(archived); !errors.Is(err, ErrSTRIDEProductDenied) {
		t.Fatalf("archived authority error=%v, want denied", err)
	}
}

func TestSTRIDEMarketplaceHireReplayCreatesOneDeterministicDirectThread(t *testing.T) {
	setupAuthTestEnv(t)
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
	previous := kanbanApp
	kanbanApp = app
	t.Cleanup(func() {
		kanbanApp = previous
		_ = app.Close()
	})
	mux := http.NewServeMux()
	registerSTRIDERuntimeRoutes(mux)
	cookies := loginAs(t, artifactLibraryAdminEmail, defaultMeetingRoomPassword)
	post := func(path string, body string) *httptest.ResponseRecorder {
		var reader *strings.Reader
		if body != "" {
			reader = strings.NewReader(body)
		} else {
			reader = strings.NewReader("")
		}
		req := httptest.NewRequest(http.MethodPost, "https://bonfire.test"+path, reader)
		req.Header.Set("Origin", "https://bonfire.test")
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		} else {
			req.Body = http.NoBody
			req.ContentLength = 0
		}
		for _, cookie := range cookies {
			req.AddCookie(cookie)
		}
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, req)
		return recorder
	}
	trial := post(strideRuntimeAPIBase+"marketplace/mary-marketing/trial", "")
	if trial.Code != http.StatusOK {
		t.Fatalf("trial status=%d body=%s", trial.Code, trial.Body.String())
	}
	var trialBody struct {
		Seat STRIDEProductTeamAgent `json:"seat"`
	}
	if err := json.Unmarshal(trial.Body.Bytes(), &trialBody); err != nil {
		t.Fatal(err)
	}
	hireBody := `{"revision":` + fmt.Sprint(trialBody.Seat.Revision) + `}`
	first := post(strideRuntimeAPIBase+"marketplace/mary-marketing/hire", hireBody)
	if first.Code != http.StatusOK {
		t.Fatalf("hire status=%d body=%s", first.Code, first.Body.String())
	}
	var firstBody struct {
		Seat STRIDEProductTeamAgent `json:"seat"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &firstBody); err != nil {
		t.Fatal(err)
	}
	wantID := "stride_agent_direct_" + temporalDigest(firstBody.Seat.ID + "\x00" + normalizeAccountEmail(artifactLibraryAdminEmail))[:20]
	if firstBody.Seat.DirectThreadID != wantID {
		t.Fatalf("direct thread=%q, want deterministic %q", firstBody.Seat.DirectThreadID, wantID)
	}
	replay := post(strideRuntimeAPIBase+"marketplace/mary-marketing/hire", hireBody)
	if replay.Code != http.StatusOK || !strings.Contains(replay.Body.String(), `"replayed":true`) {
		t.Fatalf("hire replay status=%d body=%s", replay.Code, replay.Body.String())
	}
	count := 0
	for _, entry := range app.memory.snapshot(0) {
		if entry.Kind == meetingMemoryKindScoutChat && entry.ID == wantID {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("direct thread records=%d, want one", count)
	}
}

func TestSTRIDEApprovalRejectsDestinationArchiveRenameAndMembershipDrift(t *testing.T) {
	for _, mutation := range []string{"archive", "rename", "membership"} {
		t.Run(mutation, func(t *testing.T) {
			fixture := newSTRIDEProjectAuthorityFixture(t)
			principal := strideRuntimePrincipalForEmail(fixture.user.Email)
			destination, _, err := fixture.app.ensureScoutChatThread(
				"stride_project_destination_"+mutation,
				fixture.user.Email,
				fixture.user.Name,
				"Destination",
				scoutChatVisibilityPublic,
				[]string{"caitlyn@shareability.com"},
			)
			if err != nil {
				t.Fatal(err)
			}
			audience, aclVersion, err := strideProductProjectDestinationAuthority(destination)
			if err != nil {
				t.Fatal(err)
			}
			var selected STRIDEProductWorkRecord
			if err := fixture.runtime.WithProductContext(canonicalTenantID(), STRIDEProductScopeWork, func(ctx STRIDEProductContext) error {
				var reviseErr error
				selected, reviseErr = ctx.Product.reviseWork(fixture.suggestion.ID, fixture.suggestion.Revision, principal, func(record *STRIDEProductWorkRecord) error {
					record.DestinationMode = "existing"
					record.DestinationThreadID = destination.ID
					record.DestinationTitle = destination.Title
					copyAudience := cloneAudience(audience)
					record.DestinationAudience = &copyAudience
					record.DestinationACLVersion = aclVersion
					record.Lifecycle = append(record.Lifecycle, "destination_explicitly_selected")
					return nil
				}, ctx.Receipt.IssuedAt)
				return reviseErr
			}); err != nil {
				t.Fatal(err)
			}
			if err := fixture.runtime.Save(); err != nil {
				t.Fatal(err)
			}
			switch mutation {
			case "archive":
				if _, err := fixture.app.setScoutChatThreadArchived(fixture.user.Email, destination.ID, true); err != nil {
					t.Fatal(err)
				}
			case "rename":
				if _, err := fixture.app.renameScoutChatThread(fixture.user.Email, destination.ID, "Renamed Destination"); err != nil {
					t.Fatal(err)
				}
			case "membership":
				lock := fixture.app.scoutChatThreadLock(destination.ID)
				lock.Lock()
				current, _, readErr := fixture.app.scoutChatThreadByID(fixture.user.Email, destination.ID)
				if readErr == nil {
					current.MemberEmails = append(current.MemberEmails, "aj@shareability.com")
					readErr = fixture.app.saveScoutChatThread(current)
				}
				lock.Unlock()
				if readErr != nil {
					t.Fatal(readErr)
				}
			}
			req := httptest.NewRequest(http.MethodPost, "/approve", strings.NewReader(`{"Revision":2}`))
			recorder := httptest.NewRecorder()
			strideProductApprove(recorder, req, fixture.user, fixture.runtime, selected.ID, principal)
			if recorder.Code != http.StatusForbidden {
				t.Fatalf("approval status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			if err := fixture.runtime.WithProductContext(canonicalTenantID(), STRIDEProductScopeWork, func(ctx STRIDEProductContext) error {
				ctx.WorkStore.mu.Lock()
				defer ctx.WorkStore.mu.Unlock()
				if len(ctx.WorkStore.Runs) != 0 || len(ctx.WorkStore.Intents) != 0 {
					t.Fatalf("destination drift created work: intents=%d runs=%d", len(ctx.WorkStore.Intents), len(ctx.WorkStore.Runs))
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSTRIDEApprovalWaitsForSourceProjectionAndRejectsEditedSource(t *testing.T) {
	fixture := newSTRIDEProjectAuthorityFixture(t)
	principal := strideRuntimePrincipalForEmail(fixture.user.Email)
	destination, _, err := fixture.app.ensureScoutChatThread(
		"stride_project_authority_destination",
		fixture.user.Email,
		fixture.user.Name,
		"Destination",
		scoutChatVisibilityPublic,
		[]string{"caitlyn@shareability.com"},
	)
	if err != nil {
		t.Fatal(err)
	}
	audience, aclVersion, err := strideProductProjectDestinationAuthority(destination)
	if err != nil {
		t.Fatal(err)
	}
	var selected STRIDEProductWorkRecord
	if err := fixture.runtime.WithProductContext(canonicalTenantID(), STRIDEProductScopeWork, func(ctx STRIDEProductContext) error {
		var reviseErr error
		selected, reviseErr = ctx.Product.reviseWork(fixture.suggestion.ID, fixture.suggestion.Revision, principal, func(record *STRIDEProductWorkRecord) error {
			record.DestinationMode = "existing"
			record.DestinationThreadID = destination.ID
			record.DestinationTitle = destination.Title
			copyAudience := cloneAudience(audience)
			record.DestinationAudience = &copyAudience
			record.DestinationACLVersion = aclVersion
			record.Lifecycle = append(record.Lifecycle, "destination_explicitly_selected")
			return nil
		}, ctx.Receipt.IssuedAt)
		return reviseErr
	}); err != nil {
		t.Fatal(err)
	}
	if err := fixture.runtime.Save(); err != nil {
		t.Fatal(err)
	}

	sourceLock := fixture.app.scoutChatThreadLock(fixture.source.ID)
	sourceLock.Lock()
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		req := httptest.NewRequest(http.MethodPost, "/approve", strings.NewReader(`{"Revision":2}`))
		recorder := httptest.NewRecorder()
		strideProductApprove(recorder, req, fixture.user, fixture.runtime, selected.ID, principal)
		done <- recorder
	}()
	select {
	case recorder := <-done:
		sourceLock.Unlock()
		t.Fatalf("approval crossed source barrier early: status=%d body=%s", recorder.Code, recorder.Body.String())
	case <-time.After(100 * time.Millisecond):
	}
	source, _, err := fixture.app.scoutChatThreadByID(fixture.user.Email, fixture.source.ID)
	if err != nil {
		sourceLock.Unlock()
		t.Fatal(err)
	}
	index := scoutChatMessageIndex(source, fixture.suggestion.SourceMessageID)
	if index < 0 {
		sourceLock.Unlock()
		t.Fatal("source message missing")
	}
	edited := source.Messages[index]
	edited.Text = "Request withdrawn before approval."
	edited.EditedAt = time.Now().UTC().Add(time.Second).Format(time.RFC3339Nano)
	source.Messages[index] = edited
	if err := fixture.app.saveScoutChatThread(source); err != nil {
		sourceLock.Unlock()
		t.Fatal(err)
	}
	if _, err := fixture.app.projectSTRIDETeamChatMessage(source, edited, "edit", fixture.user.Email); err != nil {
		sourceLock.Unlock()
		t.Fatal(err)
	}
	sourceLock.Unlock()
	select {
	case recorder := <-done:
		if recorder.Code != http.StatusGone {
			t.Fatalf("approval status=%d body=%s", recorder.Code, recorder.Body.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("approval remained blocked after source projection")
	}
	if err := fixture.runtime.WithProductContext(canonicalTenantID(), STRIDEProductScopeWork, func(ctx STRIDEProductContext) error {
		ctx.WorkStore.mu.Lock()
		defer ctx.WorkStore.mu.Unlock()
		if len(ctx.WorkStore.Runs) != 0 || len(ctx.WorkStore.Intents) != 0 {
			t.Fatalf("stale source created work: intents=%d runs=%d", len(ctx.WorkStore.Intents), len(ctx.WorkStore.Runs))
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
