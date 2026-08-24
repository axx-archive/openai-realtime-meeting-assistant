package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func waitForScoutOpeningReplyState(t *testing.T, app *kanbanBoardApp, threadID string, state string) scoutChatThreadRecord {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		thread, ok := app.scoutOpeningThreadByID(threadID)
		if ok && thread.OpeningOperation != nil {
			index := scoutChatMessageIndex(thread, thread.OpeningOperation.ReplyMessageID)
			if index >= 0 && thread.Messages[index].Reply != nil && thread.Messages[index].Reply.State == state {
				return thread
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("thread %s did not reach reply state %s", threadID, state)
	return scoutChatThreadRecord{}
}

func TestEnsureScoutHomeOpeningIsAtomicAndIdempotent(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { _ = app.Close() })
	user := accountStore().findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("seed user missing")
	}

	thread, created, err := app.ensureScoutHomeOpening(user, "opening-key-1", "Help me think through pricing")
	if err != nil || !created {
		t.Fatalf("ensure opening created=%v err=%v", created, err)
	}
	if scoutChatThreadVisibility(thread) != scoutChatVisibilityPrivate || len(thread.Messages) != 2 || thread.OpeningOperation == nil {
		t.Fatalf("atomic thread=%+v", thread)
	}
	if thread.Messages[0].Role != "user" || thread.Messages[0].Text != "Help me think through pricing" {
		t.Fatalf("opening message=%+v", thread.Messages[0])
	}
	if thread.Messages[1].Reply == nil || thread.Messages[1].Reply.State != scoutReplyStateQueued || thread.Messages[1].Reply.InReplyTo != thread.Messages[0].ID {
		t.Fatalf("reply placeholder=%+v", thread.Messages[1])
	}
	if thread.Title != "Help me think through pricing" {
		t.Fatalf("title=%q", thread.Title)
	}

	replayed, created, err := app.ensureScoutHomeOpening(user, "opening-key-1", "Help me think through pricing")
	if err != nil || created || replayed.ID != thread.ID || len(replayed.Messages) != 2 {
		t.Fatalf("replay created=%v err=%v thread=%+v", created, err, replayed)
	}
	if _, _, err := app.ensureScoutHomeOpening(user, "opening-key-1", "Different text"); !errors.Is(err, errScoutOpeningConflict) {
		t.Fatalf("conflicting replay err=%v", err)
	}

	projected := app.projectScoutChatThreadForViewer(user.Email, thread)
	if projected.OpeningOperation != nil {
		t.Fatal("opening operation leaked through viewer projection")
	}
}

func TestPendingScoutHomeProjectRetryRequiresExactServerJournal(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { _ = app.Close() })
	user := accountStore().findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("seed user missing")
	}
	const key = "project-response-loss-key"
	const text = "Build the launch plan"
	const encodedToken = "server-signed-project-token"
	thread, _, err := app.ensureScoutHomeOpening(user, key, text)
	if err != nil || thread.OpeningOperation == nil {
		t.Fatalf("opening=%+v err=%v", thread, err)
	}
	tokenDigest := homeProjectTokenDigest(encodedToken)
	thread.OpeningOperation.BodyDigest = scoutOpeningDigest(text, tokenDigest)
	thread.ProjectLinkOperations = []scoutChatProjectLinkOperation{{
		OperationID: thread.OpeningOperation.OperationID, TokenDigest: tokenDigest,
		MessageID: thread.OpeningOperation.UserMessageID, State: "pending", ProjectKind: "project",
		ProjectTitle: "Launch Plan", Basis: "selected",
	}}
	userIndex := scoutChatMessageIndex(thread, thread.OpeningOperation.UserMessageID)
	replyIndex := scoutChatMessageIndex(thread, thread.OpeningOperation.ReplyMessageID)
	thread.Messages[userIndex].Project = &scoutChatProjectContext{Status: "pending", Title: "Launch Plan", Basis: "selected"}
	thread.Messages[replyIndex].Reply.State = scoutReplyStateProjectPending
	if err := app.saveScoutChatThread(thread); err != nil {
		t.Fatal(err)
	}
	if !app.acceptedScoutHomeProjectRetry(user, key, text, encodedToken) {
		t.Fatal("exact pending response-loss retry was not recognized")
	}
	thread.ProjectLinkOperations[0].State = "confirmed"
	if err := app.saveScoutChatThread(thread); err != nil {
		t.Fatal(err)
	}
	if !app.acceptedScoutHomeProjectRetry(user, key, text, encodedToken) {
		t.Fatal("exact completed response-loss retry was not recognized")
	}
	thread.ProjectLinkOperations[0].State = "failed_terminal"
	if err := app.saveScoutChatThread(thread); err != nil {
		t.Fatal(err)
	}
	if !app.acceptedScoutHomeProjectRetry(user, key, text, encodedToken) {
		t.Fatal("exact terminal response-loss retry was not recognized")
	}
	for _, changed := range []struct{ key, text, token string }{
		{"other-key", text, encodedToken}, {key, "Changed body", encodedToken}, {key, text, "other-token"},
	} {
		if app.acceptedScoutHomeProjectRetry(user, changed.key, changed.text, changed.token) {
			t.Fatalf("changed pending retry was accepted: %+v", changed)
		}
	}
	projected := app.projectScoutChatThreadForViewer(user.Email, thread)
	if len(projected.ProjectLinkOperations) != 0 || projected.Messages[userIndex].Project == nil || projected.Messages[userIndex].Project.Title != "Launch Plan" {
		t.Fatalf("viewer projection leaked journal or lost safe Project display: %+v", projected)
	}
}

func TestScoutHomeProjectNewSendIsRetiredButCompletedHTTPRetrySurvivesExpiryAndRestart(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv(homeProjectContextModeEnv, "enabled")
	ctx, store, _ := migratedPostgresCanonicalStore(t)
	seedProjectPostgresAuthority(t, ctx, store)

	const email = "aj@shareability.com"
	user := accountStore().findUser(email)
	if user == nil {
		t.Fatal("seed user missing")
	}
	snapshot := projectChatSnapshotFixture(t)
	snapshot.Person.AccountSubjectDigest = sha256Hex([]byte(email))
	sessions := userSessionStore()
	sessionToken, err := sessions.createMemberSession(email, snapshot.Person.Header.ID, snapshot.Organization.Header.ID, snapshot.Membership.Header.ID, snapshot.Membership.Header.Revision, snapshot.ActiveSession.SessionRevision, func(string, string, string, int64) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	snapshot.SessionHash = hashResetToken(sessionToken)
	snapshot.ActiveSession.SessionSubjectDigest = snapshot.SessionHash
	if _, err := store.pool.Exec(ctx, `DELETE FROM stride_active_organization_sessions WHERE session_subject_digest=decode($1,'hex')`, strings.Repeat("2", 64)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_active_organization_sessions(session_subject_digest,person_id,organization_id,membership_id,membership_revision,session_revision,authority_generation,status,bound_at,expires_at,updated_at) VALUES(decode($1,'hex'),$2,$3,$4,$5,$6,$6,'active',clock_timestamp()-interval '5 minutes',clock_timestamp()+interval '1 hour',clock_timestamp())`, snapshot.SessionHash, snapshot.Person.Header.ID, snapshot.Organization.Header.ID, snapshot.Membership.Header.ID, snapshot.Membership.Header.Revision, snapshot.ActiveSession.SessionRevision); err != nil {
		t.Fatal(err)
	}

	organizations := NewOrganizationAuthorityService()
	organizations.persons[snapshot.Person.Header.ID] = snapshot.Person
	organizations.accountPersons[snapshot.Person.AccountSubjectDigest] = snapshot.Person.Header.ID
	organizations.organizations[snapshot.Organization.Header.ID] = snapshot.Organization
	organizations.memberships[snapshot.Membership.Header.ID] = snapshot.Membership
	organizations.sessions[snapshot.SessionHash] = snapshot.ActiveSession
	resolver := &strideE10MainTenantAuthorityResolver{sessions: sessions, organizations: organizations, now: time.Now}
	restoreConverter := InstallStrideE10TenantRuntimeConverter(&StrideE10TenantConverter{resolver: resolver})
	defer restoreConverter()

	priorCanonical := currentCanonicalRuntime()
	setCanonicalRuntime(&CanonicalRuntime{mode: CanonicalModeOff, postgres: store})
	defer setCanonicalRuntime(priorCanonical)
	priorLive := strideE10LiveProductRuntime
	live := NewStrideE10ProductLiveRuntime(time.Now)
	live.setFeatureForTest(STRIDEFeatureOrganizationAuthorityRead, true)
	live.setFeatureForTest(STRIDEFeatureOrganizationAuthorityWrite, true)
	strideE10LiveProductRuntime = live
	defer func() { strideE10LiveProductRuntime = priorLive }()

	key := StrideE10TenantAuthorityEnvelopeKey{ID: "home_project_http_retry", Version: 1, Secret: []byte(strings.Repeat("k", 32))}
	restoreEnvelope := InstallStrideE10TenantAuthorityEnvelopeRuntime(&strideE10TenantEnvelopeTestKeyring{current: key, keys: map[string]StrideE10TenantAuthorityEnvelopeKey{key.ID: key}})
	defer restoreEnvelope()
	app := newIsolatedKanbanBoardApp(t)
	text := "Build the durable launch plan"
	now := time.Now().UTC()
	projectToken := homeProjectContextToken{
		Version: homeProjectContextVersion, Kind: "create", TextDigest: sha256Hex([]byte(text)), Destination: homeProjectDestination{Route: "new-private"},
		PersonID: snapshot.Person.Header.ID, OrganizationID: snapshot.Organization.Header.ID, MembershipID: snapshot.Membership.Header.ID,
		MembershipRevision: snapshot.Membership.Header.Revision, SessionSubjectDigest: snapshot.SessionHash, SessionRevision: snapshot.ActiveSession.SessionRevision,
		AuthorityGeneration: snapshot.Generation, ProjectTitle: "Launch Plan", Basis: "selected", ClassifierRevision: "project_linker_v1", Confidence: 1,
		IssuedAt: now, ExpiresAt: now.Add(3 * time.Second), KeyID: key.ID, KeyVersion: key.Version,
	}
	rawToken, _ := json.Marshal(projectToken)
	encodedToken := base64.RawURLEncoding.EncodeToString(rawToken) + "." + base64.RawURLEncoding.EncodeToString(homeProjectTokenMAC(key, rawToken))
	body, _ := json.Marshal(map[string]any{"openingMessage": map[string]any{"text": text, "projectContextToken": encodedToken}})
	request := func() *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/assistant/chat-threads", strings.NewReader(string(body)))
		r.Header.Set("Authorization", "Bearer "+sessionToken)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", "project-http-retry")
		return r
	}
	if err := withCurrentHomeProjectAuthority(request(), func(current StrideE10TenantAuthoritySnapshot) error {
		if current.SessionHash != snapshot.SessionHash || current.Person.Header.ID != snapshot.Person.Header.ID || current.Organization.Header.ID != snapshot.Organization.Header.ID || current.Membership.Header.ID != snapshot.Membership.Header.ID || current.Generation != snapshot.Generation {
			return fmt.Errorf("resolved authority mismatch: %+v", current)
		}
		_, resolveErr := resolveHomeProjectToken(context.Background(), encodedToken, text, homeProjectDestination{Route: "new-private"}, current)
		return resolveErr
	}); err != nil {
		t.Fatalf("preflight exact authority/token: %v", err)
	}

	first := httptest.NewRecorder()
	handleScoutHomeOpening(first, request(), app, user, "", "", "", scoutHomeOpeningMessage{Text: text, ProjectContextToken: encodedToken})
	if first.Code != http.StatusConflict || !strings.Contains(first.Body.String(), errManualProjectAttachmentRetired.Error()) {
		t.Fatalf("new manual Project Send status=%d body=%s", first.Code, first.Body.String())
	}
	if _, found := app.scoutOpeningThreadByID("scout-home-" + scoutOpeningDigest(normalizeAccountEmail(user.Email), "project-http-retry")[:24]); found {
		t.Fatal("retired manual Project Send created a thread")
	}
	seeded, created, err := app.ensureScoutHomeOpeningWithProject(context.Background(), user, "project-http-retry", text, encodedToken, projectToken)
	if err != nil || !created || seeded.OpeningOperation == nil {
		t.Fatalf("seed historical accepted operation created=%v thread=%+v err=%v", created, seeded, err)
	}
	time.Sleep(time.Until(projectToken.ExpiresAt) + 50*time.Millisecond)
	if err := app.Close(); err != nil {
		t.Fatal(err)
	}
	restarted := newKanbanBoardApp()
	defer restarted.Close()
	second := httptest.NewRecorder()
	handleScoutHomeOpening(second, request(), restarted, user, "", "", "", scoutHomeOpeningMessage{Text: text, ProjectContextToken: encodedToken})
	if second.Code != http.StatusOK {
		t.Fatalf("expired completed retry status=%d body=%s", second.Code, second.Body.String())
	}
	var response struct {
		Replayed bool                  `json:"replayed"`
		Thread   scoutChatThreadRecord `json:"thread"`
	}
	if err := json.Unmarshal(second.Body.Bytes(), &response); err != nil || !response.Replayed || len(response.Thread.Messages) != 2 {
		t.Fatalf("retry response=%+v err=%v body=%s", response, err, second.Body.String())
	}
	message := response.Thread.Messages[scoutChatMessageIndex(response.Thread, response.Thread.ID+"-user")]
	if message.Project == nil || message.Project.Status != "confirmed" || message.Project.Title != "Launch Plan" {
		t.Fatalf("completed Project projection=%+v", message.Project)
	}
	var receipts int
	if err := store.pool.QueryRow(ctx, `SELECT count(*) FROM stride_project_chat_send_receipts WHERE organization_id=$1 AND thread_id=$2`, snapshot.Organization.Header.ID, response.Thread.ID).Scan(&receipts); err != nil || receipts != 1 {
		t.Fatalf("durable Send receipts=%d err=%v", receipts, err)
	}
	// The successful replay queues the ordinary Scout answer. Join that worker
	// before deliberately corrupting the whole-thread journal below: production
	// writers serialize through the thread lock, while a raw test save racing a
	// legitimate reply-lifecycle save can otherwise have its synthetic
	// corruption overwritten and turn this into an order-dependent false pass.
	restarted.stopScoutOpeningReplyWorkers()
	confirmed, ok := restarted.scoutOpeningThreadByID(response.Thread.ID)
	if !ok || confirmed.OpeningOperation == nil {
		t.Fatal("confirmed internal thread missing")
	}
	originalJSON, _ := encodeScoutChatThread(confirmed)
	assertConflict := func(label string, mutate func(*scoutChatThreadRecord)) {
		t.Helper()
		candidate := confirmed
		candidate.Messages = append([]scoutChatMessageRecord(nil), confirmed.Messages...)
		candidate.ProjectLinkOperations = append([]scoutChatProjectLinkOperation(nil), confirmed.ProjectLinkOperations...)
		mutate(&candidate)
		if err := restarted.saveScoutChatThread(candidate); err != nil {
			t.Fatal(err)
		}
		recorder := httptest.NewRecorder()
		handleScoutHomeOpening(recorder, request(), restarted, user, "", "", "", scoutHomeOpeningMessage{Text: text, ProjectContextToken: encodedToken})
		if recorder.Code != http.StatusConflict {
			t.Fatalf("%s canonical disagreement status=%d body=%s", label, recorder.Code, recorder.Body.String())
		}
		restored, ok := decodeScoutChatThreadEntry(meetingMemoryEntry{Kind: meetingMemoryKindScoutChat, Text: originalJSON})
		if !ok {
			t.Fatal("could not restore confirmed fixture")
		}
		if err := restarted.saveScoutChatThread(restored); err != nil {
			t.Fatal(err)
		}
	}
	assertConflict("journal", func(candidate *scoutChatThreadRecord) {
		candidate.ProjectLinkOperations[0].ProjectID = "project_corrupt"
	})
	assertConflict("message projection", func(candidate *scoutChatThreadRecord) {
		index := scoutChatMessageIndex(*candidate, candidate.OpeningOperation.UserMessageID)
		project := *candidate.Messages[index].Project
		project.ProjectID = "project_corrupt"
		candidate.Messages[index].Project = &project
	})
}

func TestScoutHomeProjectAuthorityFailureTerminalizesBeforeProviderClaim(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	defer app.Close()
	user := accountStore().findUser("aj@shareability.com")
	thread, _, err := app.ensureScoutHomeOpening(user, "project-terminal-key", "Build the plan")
	if err != nil {
		t.Fatal(err)
	}
	thread.ProjectLinkOperations = []scoutChatProjectLinkOperation{{
		OperationID: thread.OpeningOperation.OperationID, MessageID: thread.OpeningOperation.UserMessageID,
		TokenDigest: strings.Repeat("a", 64), State: "pending", ProjectKind: "project", ProjectTitle: "Launch Plan",
	}}
	userIndex := scoutChatMessageIndex(thread, thread.OpeningOperation.UserMessageID)
	replyIndex := scoutChatMessageIndex(thread, thread.OpeningOperation.ReplyMessageID)
	thread.Messages[userIndex].Project = &scoutChatProjectContext{Status: "pending", Title: "Launch Plan"}
	thread.Messages[replyIndex].Reply.State = scoutReplyStateProjectPending
	if err := app.saveScoutChatThread(thread); err != nil {
		t.Fatal(err)
	}
	failed, err := app.failScoutHomeProjectLink(user, thread, errHomeProjectStale)
	if err != nil {
		t.Fatal(err)
	}
	if scoutHomeProjectOperationState(failed, failed.OpeningOperation.OperationID) != "failed_terminal" ||
		failed.Messages[userIndex].Project == nil || failed.Messages[userIndex].Project.Status != "unavailable" ||
		failed.Messages[replyIndex].Reply.State != scoutReplyStateCanceled {
		t.Fatalf("terminal Project state=%+v", failed)
	}
	if _, _, _, claimed := app.claimScoutOpeningReply(failed.ID); claimed {
		t.Fatal("provider claimed a Project-gated reply after terminal authority failure")
	}
}

func TestEnsureScoutHomeOpeningConcurrentDuplicatesAndOwnerIsolation(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { _ = app.Close() })
	owner := accountStore().findUser("aj@shareability.com")
	other := accountStore().findUser("tim@shareability.com")
	if owner == nil || other == nil {
		t.Fatal("seed users missing")
	}

	const workers = 24
	var wg sync.WaitGroup
	results := make(chan scoutChatThreadRecord, workers)
	created := make(chan bool, workers)
	errs := make(chan error, workers)
	for index := 0; index < workers; index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			thread, wasCreated, err := app.ensureScoutHomeOpening(owner, "concurrent-key", "One durable turn")
			results <- thread
			created <- wasCreated
			errs <- err
		}()
	}
	wg.Wait()
	close(results)
	close(created)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent ensure: %v", err)
		}
	}
	createdCount := 0
	for value := range created {
		if value {
			createdCount++
		}
	}
	if createdCount != 1 {
		t.Fatalf("created count=%d, want exactly one", createdCount)
	}
	threadID := ""
	for thread := range results {
		if threadID == "" {
			threadID = thread.ID
		}
		if thread.ID != threadID || len(thread.Messages) != 2 {
			t.Fatalf("duplicate result=%+v", thread)
		}
	}
	otherThread, wasCreated, err := app.ensureScoutHomeOpening(other, "concurrent-key", "One durable turn")
	if err != nil || !wasCreated || otherThread.ID == threadID {
		t.Fatalf("owner-isolated thread=%+v created=%v err=%v", otherThread, wasCreated, err)
	}
	if _, _, err := app.scoutChatThreadByID(other.Email, threadID); err == nil {
		t.Fatal("another owner read a private atomic Scout thread")
	}
}

func TestScoutHomeOpeningHandlerReturnsBeforeProviderAndCompletesPlaceholder(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "openai-test-key"
	kanbanApp = app
	t.Cleanup(func() {
		_ = app.Close()
		kanbanApp = previousApp
	})
	t.Setenv("OPENAI_API_KEY", "openai-test-key")

	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	swapOpenAITextResponder(t, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		once.Do(func() { close(started) })
		<-release
		if request.Workflow == "scout_route" {
			return openAIScoutRouteJSON(t, openAIScoutRouterOutput{Route: "inline"}), nil
		}
		if request.Workflow == "scout_chat" {
			return "Pricing changed because the pilot scope expanded.", nil
		}
		return "", errors.New("unexpected workflow")
	})

	cookies := loginAs(t, "aj@shareability.com", "B0NFIRE!")
	request := httptest.NewRequest(http.MethodPost, "/assistant/chat-threads", strings.NewReader(`{"openingMessage":{"text":"Why did pricing change?"}}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "home-send-1")
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	assistantChatThreadsHandler(recorder, request)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Thread scoutChatThreadRecord `json:"thread"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.Thread.ID == "" || len(payload.Thread.Messages) != 2 || payload.Thread.Messages[1].Reply == nil {
		t.Fatalf("accepted payload=%+v", payload.Thread)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("reply worker did not start")
	}
	select {
	case <-time.After(50 * time.Millisecond):
		// The HTTP response already returned while the provider remains blocked.
	case <-release:
		t.Fatal("provider release channel closed unexpectedly")
	}
	close(release)

	completed := waitForScoutOpeningReplyState(t, app, payload.Thread.ID, scoutReplyStateCompleted)
	if len(completed.Messages) != 2 || completed.Messages[1].ID != payload.Thread.Messages[1].ID || !strings.Contains(completed.Messages[1].Text, "pilot scope") {
		t.Fatalf("completed thread=%+v", completed)
	}
}

func TestScoutHomeOpeningDirectAuthoredRequestsStartOnceWithoutProposal(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "openai-test-key"
	kanbanApp = app
	t.Cleanup(func() {
		_ = app.Close()
		kanbanApp = previousApp
	})
	previousStarter := startGoalThreadAsync
	startGoalThreadAsync = func(_ *kanbanBoardApp, _ string) {}
	t.Cleanup(func() { startGoalThreadAsync = previousStarter })
	swapOpenAITextResponder(t, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		t.Fatalf("direct authored opening reached provider workflow %q", request.Workflow)
		return "", nil
	})
	user := accountStore().findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("seed user missing")
	}

	for _, scenario := range []struct {
		name, key, prompt, processID, kind string
	}{
		{name: "presentation", key: "home-direct-presentation", prompt: "Create a private editable three-slide presentation for the release review.", processID: packagingStudioProcessID, kind: studioProjectKindPresentation},
		{name: "research document", key: "home-direct-document", prompt: "Create one private editable Markdown document with the release findings.", processID: documentReportProcessID, kind: studioProjectKindDocument},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			thread, created, err := app.ensureScoutHomeOpening(user, scenario.key, scenario.prompt)
			if err != nil || !created {
				t.Fatalf("ensure opening created=%v err=%v", created, err)
			}
			claimed, userMessage, leaseID, ok := app.claimScoutOpeningReply(thread.ID)
			if !ok || leaseID == "" {
				t.Fatal("opening reply was not claimed")
			}
			resolved, err := app.resolveScoutOpeningReply(context.Background(), user, claimed, userMessage)
			if err != nil {
				t.Fatal(err)
			}
			if resolved.Proposal != nil || resolved.Thread == nil || resolved.IntentOutcome != string(conversationIntentStartPrivateWork) || resolved.Thread.ProcessID != scenario.processID {
				t.Fatalf("resolved=%+v, want direct %s work card", resolved, scenario.processID)
			}
			app.finishScoutOpeningReply(thread.ID, leaseID, resolved, nil)

			completed, ok := app.scoutOpeningThreadByID(thread.ID)
			if !ok || len(completed.Messages) != 2 || completed.OpeningOperation == nil {
				t.Fatalf("completed=%+v", completed)
			}
			replyIndex := scoutChatMessageIndex(completed, completed.OpeningOperation.ReplyMessageID)
			if replyIndex < 0 {
				t.Fatal("opening reply disappeared")
			}
			reply := completed.Messages[replyIndex]
			if reply.Reply == nil || reply.Reply.State != scoutReplyStateCompleted || reply.Thread == nil || reply.Thread.ID != resolved.Thread.ID {
				t.Fatalf("completed reply=%+v", reply)
			}
			projected := app.projectScoutChatThreadForViewer(user.Email, completed)
			projectedReply := projected.Messages[replyIndex]
			if projectedReply.StudioProject == nil || projectedReply.StudioProject.ID != resolved.Thread.ArtifactID ||
				projectedReply.StudioProject.Kind != scenario.kind || projectedReply.StudioProject.Href != "/work?project="+resolved.Thread.ArtifactID {
				t.Fatalf("projected reply=%+v", projectedReply)
			}
			root, found := app.osArtifactByID(resolved.Thread.ArtifactID)
			rootKind, plan, canonical, recognized := studioProjectClassification(root)
			if !found || !canonical || !recognized || rootKind != scenario.kind || plan.ProcessID != scenario.processID {
				t.Fatalf("root found=%v kind=%q canonical=%v recognized=%v plan=%+v", found, rootKind, canonical, recognized, plan)
			}
		})
	}
}

func TestScoutHomeOpeningDirectWorkReclaimsLeaseWithoutDuplicateRoot(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "openai-test-key"
	kanbanApp = app
	t.Cleanup(func() {
		_ = app.Close()
		kanbanApp = previousApp
	})
	previousStarter := startGoalThreadAsync
	startGoalThreadAsync = func(_ *kanbanBoardApp, _ string) {}
	t.Cleanup(func() { startGoalThreadAsync = previousStarter })
	swapOpenAITextResponder(t, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		t.Fatalf("direct authored replay reached provider workflow %q", request.Workflow)
		return "", nil
	})
	user := accountStore().findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("seed user missing")
	}
	thread, _, err := app.ensureScoutHomeOpening(user, "home-direct-reclaim", "Create a private editable three-slide presentation for restart QA.")
	if err != nil {
		t.Fatal(err)
	}
	claimed, userMessage, firstLease, ok := app.claimScoutOpeningReply(thread.ID)
	if !ok {
		t.Fatal("first opening reply was not claimed")
	}
	first, err := app.resolveScoutOpeningReply(context.Background(), user, claimed, userMessage)
	if err != nil || first.Thread == nil {
		t.Fatalf("first resolution=%+v err=%v", first, err)
	}
	app.requeueExpiredScoutOpeningReply(thread.ID, firstLease)
	claimed, userMessage, secondLease, ok := app.claimScoutOpeningReply(thread.ID)
	if !ok || secondLease == firstLease {
		t.Fatal("reclaimed opening reply did not mint a new lease")
	}
	second, err := app.resolveScoutOpeningReply(context.Background(), user, claimed, userMessage)
	if err != nil || second.Thread == nil || second.Thread.ID != first.Thread.ID || second.Thread.ArtifactID != first.Thread.ArtifactID {
		firstID, firstArtifactID, secondID, secondArtifactID := "", "", "", ""
		if first.Thread != nil {
			firstID, firstArtifactID = first.Thread.ID, first.Thread.ArtifactID
		}
		if second.Thread != nil {
			secondID, secondArtifactID = second.Thread.ID, second.Thread.ArtifactID
		}
		t.Fatalf("second id=%q artifact=%q first id=%q artifact=%q err=%v", secondID, secondArtifactID, firstID, firstArtifactID, err)
	}
	app.finishScoutOpeningReply(thread.ID, secondLease, second, nil)

	rootCount := 0
	for _, entry := range app.memory.snapshot(0) {
		if _, _, canonical := studioProjectCandidate(entry); canonical && entry.Metadata["operationId"] == thread.OpeningOperation.OperationID &&
			entry.Metadata["operationBodyDigest"] == thread.OpeningOperation.BodyDigest && entry.Metadata["originId"] == thread.ID {
			rootCount++
		}
	}
	completed, ok := app.scoutOpeningThreadByID(thread.ID)
	if !ok || len(completed.Messages) != 2 || rootCount != 1 || completed.Messages[1].Thread == nil || completed.Messages[1].Thread.ID != first.Thread.ID || completed.Messages[1].Reply == nil || completed.Messages[1].Reply.State != scoutReplyStateCompleted {
		t.Fatalf("completed=%+v rootCount=%d", completed, rootCount)
	}
}

func TestScoutHomeOpeningUsesAuthorizedMeetingBriefingBeforeGenericProvider(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	user := accountStore().findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("test user missing")
	}
	now := time.Now().In(meetingTimeLocation())
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	spanStart := now.Add(-time.Hour)
	if spanStart.Before(dayStart) {
		spanStart = dayStart
	}
	digest := `{"meetingId":"home-opening-meeting","title":"Launch review","day":"` + now.Format("2006-01-02") + `",` +
		`"decisions":[{"d":"Keep the Friday launch with AJ as rollback owner","status":"decided","importance":5}]}`
	upsertBriefingTestDigest(t, app, "home-opening-meeting", digest, now.Format("2006-01-02"), spanStart.UTC().Format(time.RFC3339), now.UTC().Format(time.RFC3339))

	providerCalls := 0
	swapOpenAITextResponder(t, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		providerCalls++
		t.Fatalf("Home meeting briefing reached generic provider workflow %q", request.Workflow)
		return "", nil
	})
	thread, _, err := app.ensureScoutHomeOpening(user, "home-meeting-briefing-key", "Catch me up on today's meetings")
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := app.resolveScoutOpeningReply(context.Background(), user, thread, thread.Messages[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(resolved.Text, "Keep the Friday launch") || resolved.CausedByMessageID != thread.Messages[0].ID || providerCalls != 0 {
		t.Fatalf("resolved=%+v providerCalls=%d", resolved, providerCalls)
	}
}

func TestScoutHomeOpeningTransportAcceptsMaxEscapedText(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	app := newIsolatedKanbanBoardApp(t)
	kanbanApp = app
	t.Cleanup(func() {
		_ = app.Close()
		kanbanApp = previousApp
	})
	text := strings.Repeat("\x01", scoutHomeOpeningMaxRunes)
	body, err := json.Marshal(map[string]any{"openingMessage": map[string]any{"text": text}})
	if err != nil {
		t.Fatal(err)
	}
	if len(body) <= 16<<10 {
		t.Fatalf("fixture body=%d, want proof above the old transport cap", len(body))
	}
	cookies := loginAs(t, "aj@shareability.com", "B0NFIRE!")
	request := httptest.NewRequest(http.MethodPost, "/assistant/chat-threads", strings.NewReader(string(body)))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "escaped-max-opening")
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	assistantChatThreadsHandler(recorder, request)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestScoutHomeOpeningFailureIsSafeAndRetryDoesNotDuplicateUserMessage(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "openai-test-key"
	t.Cleanup(func() { _ = app.Close() })
	t.Setenv("OPENAI_API_KEY", "openai-test-key")
	user := accountStore().findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("seed user missing")
	}

	var mu sync.Mutex
	failAnswer := true
	swapOpenAITextResponder(t, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		if request.Workflow == "scout_route" {
			return openAIScoutRouteJSON(t, openAIScoutRouterOutput{Route: "inline"}), nil
		}
		mu.Lock()
		fail := failAnswer
		mu.Unlock()
		if fail {
			return "", &openAIProviderFailure{err: errors.New("temporary network outage")}
		}
		return "Recovered answer.", nil
	})

	thread, _, err := app.ensureScoutHomeOpening(user, "retry-key", "Can you answer this?")
	if err != nil {
		t.Fatalf("ensure opening: %v", err)
	}
	app.queueScoutOpeningReply(thread.ID)
	failed := waitForScoutOpeningReplyState(t, app, thread.ID, scoutReplyStateFailed)
	if len(failed.Messages) != 2 || failed.Messages[1].Text != scoutReplySafeFailureText || !failed.Messages[1].Reply.Retryable {
		t.Fatalf("failed thread=%+v", failed)
	}

	mu.Lock()
	failAnswer = false
	mu.Unlock()
	if _, _, err := app.retryScoutOpeningReply(user.Email, thread.ID, failed.Messages[1].ID); err != nil {
		t.Fatalf("retry reply: %v", err)
	}
	completed := waitForScoutOpeningReplyState(t, app, thread.ID, scoutReplyStateCompleted)
	if len(completed.Messages) != 2 || completed.Messages[0].ID != thread.Messages[0].ID || completed.Messages[1].ID != thread.Messages[1].ID || completed.Messages[1].Text != "Recovered answer." {
		t.Fatalf("retry duplicated or replaced identities: %+v", completed)
	}
}

func TestScoutHomeOpeningQueuedReplyRecoversAfterRestart(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	user := accountStore().findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("seed user missing")
	}
	thread, _, err := app.ensureScoutHomeOpening(user, "restart-key", "Recover this queued answer")
	if err != nil {
		t.Fatalf("ensure opening: %v", err)
	}
	if err := app.Close(); err != nil {
		t.Fatalf("close first app: %v", err)
	}

	restarted := newKanbanBoardApp()
	restarted.apiKey = "openai-test-key"
	t.Cleanup(func() { _ = restarted.Close() })
	t.Setenv("OPENAI_API_KEY", "openai-test-key")
	swapOpenAITextResponder(t, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		if request.Workflow == "scout_route" {
			return openAIScoutRouteJSON(t, openAIScoutRouterOutput{Route: "inline"}), nil
		}
		return "Recovered after restart.", nil
	})
	restarted.startScoutOpeningReplyWorkers()
	completed := waitForScoutOpeningReplyState(t, restarted, thread.ID, scoutReplyStateCompleted)
	if len(completed.Messages) != 2 || completed.Messages[1].Text != "Recovered after restart." {
		t.Fatalf("recovered thread=%+v", completed)
	}
}

func TestScoutHomeOpeningDirectLaunchPreservesRouterProvenance(t *testing.T) {
	setupAuthTestEnv(t)
	resetCapabilityRuntimeForTest(t)
	dir := ledgerTestDir(t)
	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "openai-test-key"
	t.Cleanup(func() { _ = app.Close() })
	previousStarter := startGoalThreadAsync
	startGoalThreadAsync = func(_ *kanbanBoardApp, _ string) {}
	t.Cleanup(func() { startGoalThreadAsync = previousStarter })
	user := accountStore().findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("seed user missing")
	}

	thread, _, err := app.ensureScoutHomeOpening(user, "provenance-key", "package this end to end")
	if err != nil {
		t.Fatalf("ensure opening: %v", err)
	}
	app.queueScoutOpeningReply(thread.ID)
	completed := waitForScoutOpeningReplyState(t, app, thread.ID, scoutReplyStateCompleted)
	if completed.Messages[1].Proposal != nil || completed.Messages[1].Thread == nil || completed.Messages[1].IntentOutcome != string(conversationIntentStartPrivateWork) {
		t.Fatalf("completed reply=%+v, want direct work card", completed.Messages[1])
	}

	events := readRouterLedgerEvents(t, dir)
	minted := filterLedgerEvents(events, telemetryTypeProposal, proposalEventMinted)
	if len(minted) != 0 {
		t.Fatalf("minted events=%d, direct launch must not mint a proposal", len(minted))
	}
	directOutcomes := 0
	for _, event := range filterLedgerEvents(events, telemetryTypeEval, evalKindRouterOutcome) {
		fields := ledgerEventFields(event)
		if fields["verdict"] == string(conversationIntentStartPrivateWork) && fields["source"] == proposalSourceDeterministicGuard {
			directOutcomes++
		}
	}
	if directOutcomes != 1 {
		t.Fatalf("direct outcomes=%d, want deterministic start_private_work provenance", directOutcomes)
	}
	if state := capabilityState(capabilityTypedScoutRouter); !state.LastSuccess.IsZero() {
		t.Fatalf("deterministic guard manufactured provider success: %+v", state)
	}
}

func TestScoutHomeOpeningMutationsFenceQueuedAndRunningReplies(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { _ = app.Close() })
	user := accountStore().findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("seed user missing")
	}

	t.Run("edit cancels running and discards late completion", func(t *testing.T) {
		thread, _, err := app.ensureScoutHomeOpening(user, "mutation-edit", "Original opening")
		if err != nil {
			t.Fatalf("ensure opening: %v", err)
		}
		_, _, leaseID, claimed := app.claimScoutOpeningReply(thread.ID)
		if !claimed {
			t.Fatal("claim running reply")
		}
		changed := "Edited opening"
		edited, _, err := app.editScoutChatThreadMessage(context.Background(), user, thread.ID, thread.OpeningOperation.UserMessageID, &changed, nil)
		if err != nil {
			t.Fatalf("edit opening: %v", err)
		}
		replyIndex := scoutChatMessageIndex(edited, thread.OpeningOperation.ReplyMessageID)
		if replyIndex < 0 || edited.Messages[replyIndex].Reply == nil || edited.Messages[replyIndex].Reply.State != scoutReplyStateCanceled {
			t.Fatalf("edited thread=%+v, want canceled reply", edited)
		}
		app.finishScoutOpeningReply(thread.ID, leaseID, scoutChatMessageRecord{Kind: "message", Role: "scout", Text: "late answer"}, nil)
		current, _ := app.scoutOpeningThreadByID(thread.ID)
		if current.Messages[replyIndex].Reply.State != scoutReplyStateCanceled || strings.Contains(current.Messages[replyIndex].Text, "late answer") {
			t.Fatalf("late completion escaped CAS: %+v", current.Messages[replyIndex])
		}
	})

	t.Run("delete removes opening and placeholder", func(t *testing.T) {
		thread, _, err := app.ensureScoutHomeOpening(user, "mutation-delete", "Delete this opening")
		if err != nil {
			t.Fatalf("ensure opening: %v", err)
		}
		deleted, err := app.deleteScoutChatThreadMessage(user.Email, thread.ID, thread.OpeningOperation.UserMessageID)
		if err != nil {
			t.Fatalf("delete opening: %v", err)
		}
		if deleted.OpeningOperation != nil || len(deleted.Messages) != 0 {
			t.Fatalf("deleted thread=%+v, want opening pair removed", deleted)
		}
	})

	t.Run("archive cancels queued reply", func(t *testing.T) {
		thread, _, err := app.ensureScoutHomeOpening(user, "mutation-archive", "Archive this opening")
		if err != nil {
			t.Fatalf("ensure opening: %v", err)
		}
		archived, err := app.setScoutChatThreadArchived(user.Email, thread.ID, true)
		if err != nil {
			t.Fatalf("archive opening: %v", err)
		}
		replyIndex := scoutChatMessageIndex(archived, thread.OpeningOperation.ReplyMessageID)
		if replyIndex < 0 || archived.Messages[replyIndex].Reply == nil || archived.Messages[replyIndex].Reply.State != scoutReplyStateCanceled {
			t.Fatalf("archived thread=%+v, want canceled reply", archived)
		}
	})
}

func TestRecoveredScoutOpeningReplyCannotConsumeOrClobberLaterTurns(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "openai-test-key"
	t.Cleanup(func() { _ = app.Close() })
	user := accountStore().findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("seed user missing")
	}
	thread, _, err := app.ensureScoutHomeOpening(user, "causal-cutoff", "Answer the opening question")
	if err != nil {
		t.Fatalf("ensure opening: %v", err)
	}

	lock := app.scoutChatThreadLock(thread.ID)
	lock.Lock()
	current, ok := app.scoutOpeningThreadByID(thread.ID)
	if !ok {
		lock.Unlock()
		t.Fatal("opening thread missing")
	}
	now := time.Now().UTC()
	laterUser := scoutChatMessageRecord{ID: "later-user", Kind: "message", Role: "user", Text: "LATER CAUSAL CONTAMINANT", CreatedAt: now.Format(time.RFC3339Nano), AuthorEmail: user.Email}
	laterReply := scoutChatMessageRecord{ID: "later-scout", Kind: "message", Role: "scout", Text: "Newest preview must survive", CreatedAt: now.Add(time.Millisecond).Format(time.RFC3339Nano)}
	current.Messages = append(current.Messages, laterUser, laterReply)
	updateScoutChatThreadSummary(&current, laterUser, laterReply)
	if err := app.saveScoutChatThread(current); err != nil {
		lock.Unlock()
		t.Fatalf("save later turn: %v", err)
	}
	previewBefore, updatedBefore := current.Preview, current.UpdatedAt
	lock.Unlock()

	swapOpenAITextResponder(t, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		if strings.Contains(fmt.Sprint(request.Input), "LATER CAUSAL CONTAMINANT") {
			t.Fatalf("opening provider input consumed a later turn: %v", request.Input)
		}
		switch request.Workflow {
		case "scout_route":
			return openAIScoutRouteJSON(t, openAIScoutRouterOutput{Route: "inline"}), nil
		case "scout_chat":
			return "Opening answer arrived late.", nil
		default:
			return "", fmt.Errorf("unexpected workflow %q", request.Workflow)
		}
	})
	claimedThread, openingMessage, leaseID, claimed := app.claimScoutOpeningReply(thread.ID)
	if !claimed {
		t.Fatal("claim delayed opening reply")
	}
	resolved, err := app.resolveScoutOpeningReply(context.Background(), user, claimedThread, openingMessage)
	if err != nil {
		t.Fatalf("resolve delayed opening: %v", err)
	}
	app.finishScoutOpeningReply(thread.ID, leaseID, resolved, nil)

	finished, _ := app.scoutOpeningThreadByID(thread.ID)
	if finished.Preview != previewBefore || finished.UpdatedAt != updatedBefore {
		t.Fatalf("late opening clobbered newer summary: preview=%q/%q updated=%q/%q", finished.Preview, previewBefore, finished.UpdatedAt, updatedBefore)
	}
	if finished.Messages[len(finished.Messages)-1].Text != laterReply.Text {
		t.Fatalf("newest message moved or changed: %+v", finished.Messages)
	}
}
