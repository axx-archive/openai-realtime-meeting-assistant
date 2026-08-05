package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type strideCoworkerTestFixture struct {
	app     *kanbanBoardApp
	user    *userAccount
	table   scoutChatThreadRecord
	cookies []*http.Cookie
	runtime *STRIDERuntime
	dir     string
	now     *time.Time
}

func newSTRIDECoworkerTestFixture(t *testing.T) strideCoworkerTestFixture {
	t.Helper()
	setupAuthTestEnv(t)
	cookies := loginAs(t, "aj@shareability.com", "B0NFIRE!")
	dir := t.TempDir()
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(dir, "memory.jsonl"))
	t.Setenv("KANBAN_BOARD_PATH", filepath.Join(dir, "board.json"))
	app := newKanbanBoardApp()
	user := accountStore().findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("seed user AJ missing")
	}
	table, err := app.ensureTable(user.Email)
	if err != nil {
		t.Fatal(err)
	}
	config := strideIntegratedRuntimeConfig(filepath.Join(dir, "runtime"))
	config.ProductPreviewEnabled = true
	config.RecallThreadIDs = []string{table.ID}
	runtimeNow := time.Date(2026, 7, 30, 23, 0, 0, 0, time.UTC)
	config.Now = func() time.Time { return runtimeNow }
	runtime, err := NewSTRIDERuntime(config)
	if err != nil {
		t.Fatal(err)
	}
	app.strideRuntime = runtime
	previous := kanbanApp
	kanbanApp = app
	t.Cleanup(func() {
		_ = runtime.Close()
		kanbanApp = previous
	})
	return strideCoworkerTestFixture{app: app, user: user, table: table, cookies: cookies, runtime: runtime, dir: dir, now: &runtimeNow}
}

func (fixture strideCoworkerTestFixture) commitUserMessage(t *testing.T, id, text string) scoutChatMessageRecord {
	t.Helper()
	message := scoutChatMessageRecord{ID: id, Kind: "message", Role: "user", Text: text, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), AuthorName: fixture.user.Name, AuthorEmail: fixture.user.Email}
	if _, err := fixture.app.commitScoutChatThreadMessages(fixture.user.Email, fixture.table.ID, message); err != nil {
		t.Fatal(err)
	}
	return message
}

func TestSTRIDECoworkerContextIsBodyFreeAndFeedsActualPublicScoutQuery(t *testing.T) {
	fixture := newSTRIDECoworkerTestFixture(t)
	fixture.commitUserMessage(t, "coworker-prior", "launch codename mercury is confidential body text")
	pending := scoutChatMessageRecord{ID: "coworker-current", Kind: "message", Role: "user", Text: "@scout what did I miss?", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), AuthorName: fixture.user.Name, AuthorEmail: fixture.user.Email}
	base := scoutChatContextTurnModelText(scoutChatContextTurnFromMessage(fixture.table, pending))
	prepared := fixture.app.prepareSTRIDECoworkerModelQuery(fixture.user, fixture.table, pending, base)
	if prepared == base || !strings.Contains(prepared, "[STRIDE authorized context:") || !strings.Contains(prepared, "brain_high_water=") {
		t.Fatalf("prepared query missing STRIDE lineage: %q", prepared)
	}
	if strings.Contains(prepared, "launch codename mercury") {
		t.Fatalf("body leaked into STRIDE suffix: %q", prepared)
	}
	if _, err := fixture.app.commitScoutChatThreadMessages(fixture.user.Email, fixture.table.ID, pending); err != nil {
		t.Fatal(err)
	}
	product, err := admittedSTRIDECoworkerProduct(fixture.runtime)
	if err != nil {
		t.Fatal(err)
	}
	thread, _, err := fixture.app.scoutChatThreadByID(fixture.user.Email, fixture.table.ID)
	if err != nil {
		t.Fatal(err)
	}
	assembled, err := fixture.app.assembleSTRIDECoworkerContext(product, fixture.user, thread, pending, true)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(assembled)
	if strings.Contains(string(raw), pending.Text) || strings.Contains(string(raw), "launch codename mercury") {
		t.Fatalf("assembled context contains a message body: %s", raw)
	}
	if assembled.Envelope.CurrentTurn.ID == "" || len(assembled.Envelope.RecentTurns) < 2 || assembled.BrainHighWater < 2 {
		t.Fatalf("assembled context lacks real projection lineage: %+v", assembled)
	}

	// Exercise the real public Scout model-query seam with a local responder;
	// no provider transport is called.
	fixture.app.mu.Lock()
	fixture.app.apiKey = "local-test-key"
	fixture.app.mu.Unlock()
	var captured string
	original := createOpenAITextResponse
	createOpenAITextResponse = func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		captured = request.Input
		return "Here’s the short version.", nil
	}
	t.Cleanup(func() { createOpenAITextResponse = original })
	if _, err := fixture.app.appendScoutChatThreadMessage(context.Background(), fixture.user, fixture.table.ID, "@scout give me the current thread summary", nil, ""); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(captured, "[STRIDE authorized context:") {
		t.Fatalf("actual Scout request omitted assembled STRIDE context: %q", captured)
	}
}

func TestSTRIDECoworkerDefaultOffAndInvocationBoundariesFailClosed(t *testing.T) {
	fixture := newSTRIDECoworkerTestFixture(t)
	message := fixture.commitUserMessage(t, "coworker-boundary", "ordinary team message")
	thread, _, _ := fixture.app.scoutChatThreadByID(fixture.user.Email, fixture.table.ID)
	if _, _, err := fixture.app.strideCoworkerPublicInvocation(fixture.user, thread.ID, message.ID, false); !errors.Is(err, ErrSTRIDECoworkerDenied) {
		t.Fatalf("non-@scout invocation error=%v", err)
	}
	private, err := fixture.app.createScoutChatThread(fixture.user.Email, fixture.user.Name, "private", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatal(err)
	}
	privateMessage := scoutChatMessageRecord{ID: "private-mention", Kind: "message", Role: "user", Text: "@scout secret", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), AuthorName: fixture.user.Name, AuthorEmail: fixture.user.Email}
	if _, err := fixture.app.commitScoutChatThreadMessages(fixture.user.Email, private.ID, privateMessage); err != nil {
		t.Fatal(err)
	}
	if _, _, err := fixture.app.strideCoworkerPublicInvocation(fixture.user, private.ID, privateMessage.ID, false); !errors.Is(err, ErrSTRIDECoworkerDenied) {
		t.Fatalf("private invocation error=%v", err)
	}
	first := fixture.commitUserMessage(t, "first-mention", "@scout first")
	fixture.commitUserMessage(t, "second-mention", "@scout second")
	if _, _, err := fixture.app.strideCoworkerPublicInvocation(fixture.user, fixture.table.ID, first.ID, false); !errors.Is(err, ErrSTRIDECoworkerDenied) {
		t.Fatalf("stale invocation error=%v", err)
	}

	disabled, err := NewSTRIDERuntime(STRIDERuntimeConfig{})
	if err != nil {
		t.Fatal(err)
	}
	old := fixture.app.strideRuntime
	fixture.app.strideRuntime = disabled
	pending := scoutChatMessageRecord{ID: "disabled-pending", Role: "user", Text: "@scout hi", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), AuthorName: fixture.user.Name, AuthorEmail: fixture.user.Email}
	if got := fixture.app.prepareSTRIDECoworkerModelQuery(fixture.user, fixture.table, pending, "unchanged"); got != "unchanged" {
		t.Fatalf("default-off query changed: %q", got)
	}
	disabledReq := httptest.NewRequest(http.MethodGet, strideRuntimeAPIBase+"coworker/context?threadId="+fixture.table.ID+"&messageId=second-mention", nil)
	for _, cookie := range fixture.cookies {
		disabledReq.AddCookie(cookie)
	}
	disabledRecorder := httptest.NewRecorder()
	strideCoworkerSubrouteHandler(disabledRecorder, disabledReq)
	if disabledRecorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("default-off route status=%d body=%s", disabledRecorder.Code, disabledRecorder.Body.String())
	}
	fixture.app.strideRuntime = old

	relationshipDisabledReq := httptest.NewRequest(http.MethodGet, strideRuntimeAPIBase+"coworker/relationships", nil)
	for _, cookie := range fixture.cookies {
		relationshipDisabledReq.AddCookie(cookie)
	}
	relationshipDisabledRecorder := httptest.NewRecorder()
	strideCoworkerSubrouteHandler(relationshipDisabledRecorder, relationshipDisabledReq)
	if relationshipDisabledRecorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("default-off relationship route status=%d body=%s", relationshipDisabledRecorder.Code, relationshipDisabledRecorder.Body.String())
	}

	req := httptest.NewRequest(http.MethodGet, strideRuntimeAPIBase+"coworker/context?tenantId=other&threadId="+fixture.table.ID+"&messageId=second-mention", nil)
	for _, cookie := range fixture.cookies {
		req.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	strideCoworkerSubrouteHandler(recorder, req)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("client tenant override status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	unauthenticated := httptest.NewRequest(http.MethodGet, strideRuntimeAPIBase+"coworker/context?threadId="+fixture.table.ID+"&messageId=second-mention", nil)
	recorder = httptest.NewRecorder()
	strideCoworkerSubrouteHandler(recorder, unauthenticated)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestSTRIDECoworkerContextHTTPReturnsAuthorizedProjectionWithoutBodies(t *testing.T) {
	fixture := newSTRIDECoworkerTestFixture(t)
	message := fixture.commitUserMessage(t, "context-http", "@scout use the secret falcon plan")
	req := httptest.NewRequest(http.MethodGet, strideRuntimeAPIBase+"coworker/context?threadId="+fixture.table.ID+"&messageId="+message.ID, nil)
	for _, cookie := range fixture.cookies {
		req.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	mux := http.NewServeMux()
	registerSTRIDERuntimeRoutes(mux)
	mux.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "secret falcon plan") || !strings.Contains(recorder.Body.String(), `"providerExecutionFenced":true`) {
		t.Fatalf("unsafe or incomplete context response: %s", recorder.Body.String())
	}
}

func TestSTRIDECoworkerRelationshipControlsPersistAndGateActualModelContext(t *testing.T) {
	fixture := newSTRIDECoworkerTestFixture(t)
	fixture.runtime.mu.Lock()
	fixture.runtime.config.RelationshipMemoryEnabled = true
	fixture.runtime.mu.Unlock()
	mux := http.NewServeMux()
	registerSTRIDERuntimeRoutes(mux)
	request := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		var reader *strings.Reader
		if body == "" {
			reader = strings.NewReader("")
		} else {
			reader = strings.NewReader(body)
		}
		req := httptest.NewRequest(method, path, reader)
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		for _, cookie := range fixture.cookies {
			req.AddCookie(cookie)
		}
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, req)
		return recorder
	}

	consent := request(http.MethodPost, strideRuntimeAPIBase+"coworker/relationships/consent", `{"action":"enable","expectedRevision":0,"allowInferred":false,"allowShared":true}`)
	if consent.Code != http.StatusOK || !strings.Contains(consent.Body.String(), `"revision":1`) {
		t.Fatalf("consent status=%d body=%s", consent.Code, consent.Body.String())
	}
	source := fixture.commitUserMessage(t, "relationship-remember-source", "@scout remember that I prefer one concise paragraph")
	remember := request(http.MethodPost, strideRuntimeAPIBase+"coworker/relationships/remember", fmt.Sprintf(`{"action":"remember","expectedRevision":1,"threadId":%q,"sourceMessageId":%q,"preferenceType":"response_length","value":"one concise paragraph","scope":"shared"}`, fixture.table.ID, source.ID))
	if remember.Code != http.StatusCreated {
		t.Fatalf("remember status=%d body=%s", remember.Code, remember.Body.String())
	}
	if !strings.Contains(remember.Body.String(), `"kind":"conversation"`) || !strings.Contains(remember.Body.String(), `"threadId":"`+fixture.table.ID+`"`) || !strings.Contains(remember.Body.String(), `"messageId":"`+source.ID+`"`) {
		t.Fatalf("remember response lacks exact inspectable source: %s", remember.Body.String())
	}
	var remembered struct {
		Revision    int64                                  `json:"revision"`
		Preferences []STRIDECollaborationContextPreference `json:"preferences"`
	}
	if err := json.Unmarshal(remember.Body.Bytes(), &remembered); err != nil || remembered.Revision != 2 || len(remembered.Preferences) != 1 {
		t.Fatalf("remember response=%+v err=%v body=%s", remembered, err, remember.Body.String())
	}
	relationshipID := remembered.Preferences[0].Reference.ID
	pending := scoutChatMessageRecord{ID: "relationship-context-pending", Kind: "message", Role: "user", Text: "@scout catch me up", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), AuthorName: fixture.user.Name, AuthorEmail: fixture.user.Email}
	query := fixture.app.prepareSTRIDECoworkerModelQuery(fixture.user, fixture.table, pending, "base query")
	if !strings.Contains(query, "approved_collaboration_preferences_data") || !strings.Contains(query, "one concise paragraph") || !strings.Contains(query, relationshipID) {
		t.Fatalf("approved preference missing from actual model query: %q", query)
	}
	otherThread, err := fixture.app.createScoutChatThread(fixture.user.Email, fixture.user.Name, "another public channel", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatal(err)
	}
	otherSource := scoutChatMessageRecord{ID: "relationship-wrong-thread-source", Kind: "message", Role: "user", Text: "@scout use this unrelated channel as evidence", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), AuthorName: fixture.user.Name, AuthorEmail: fixture.user.Email}
	if _, err := fixture.app.commitScoutChatThreadMessages(fixture.user.Email, otherThread.ID, otherSource); err != nil {
		t.Fatal(err)
	}
	crossThreadCorrection := request(http.MethodPost, strideRuntimeAPIBase+"coworker/relationships/correct", fmt.Sprintf(`{"action":"correct","expectedRevision":2,"relationshipId":%q,"value":"wrong channel value","threadId":%q,"sourceMessageId":%q}`, relationshipID, otherThread.ID, otherSource.ID))
	if crossThreadCorrection.Code != http.StatusForbidden {
		t.Fatalf("cross-thread correction status=%d body=%s", crossThreadCorrection.Code, crossThreadCorrection.Body.String())
	}
	correctionSource := fixture.commitUserMessage(t, "relationship-correct-source", "@scout correction: direct, then explain why")
	correct := request(http.MethodPost, strideRuntimeAPIBase+"coworker/relationships/correct", fmt.Sprintf(`{"action":"correct","expectedRevision":2,"relationshipId":%q,"value":"direct, then explain why","threadId":%q,"sourceMessageId":%q}`, relationshipID, fixture.table.ID, correctionSource.ID))
	if correct.Code != http.StatusOK || !strings.Contains(correct.Body.String(), "direct, then explain why") || strings.Contains(correct.Body.String(), "one concise paragraph") {
		t.Fatalf("correct status=%d body=%s", correct.Code, correct.Body.String())
	}
	pending.ID = "relationship-corrected-pending"
	query = fixture.app.prepareSTRIDECoworkerModelQuery(fixture.user, fixture.table, pending, "base query")
	if !strings.Contains(query, "direct, then explain why") || strings.Contains(query, "one concise paragraph") {
		t.Fatalf("correction not reflected in actual model query: %q", query)
	}

	forget := request(http.MethodPost, strideRuntimeAPIBase+"coworker/relationships/forget", fmt.Sprintf(`{"action":"forget","expectedRevision":3,"relationshipId":%q}`, relationshipID))
	if forget.Code != http.StatusOK || !strings.Contains(forget.Body.String(), `"preferences":[]`) {
		t.Fatalf("forget status=%d body=%s", forget.Code, forget.Body.String())
	}
	pending.ID = "relationship-forgotten-pending"
	query = fixture.app.prepareSTRIDECoworkerModelQuery(fixture.user, fixture.table, pending, "base query")
	if strings.Contains(query, "direct, then explain why") || strings.Contains(query, "approved_collaboration_preferences_data") {
		t.Fatalf("forgotten preference remained in actual model query: %q", query)
	}

	revoke := request(http.MethodPost, strideRuntimeAPIBase+"coworker/relationships/consent", `{"action":"disable","expectedRevision":4,"allowInferred":false,"allowShared":false}`)
	if revoke.Code != http.StatusOK || !strings.Contains(revoke.Body.String(), `"enabled":false`) {
		t.Fatalf("revoke status=%d body=%s", revoke.Code, revoke.Body.String())
	}
	staleCorrect := request(http.MethodPost, strideRuntimeAPIBase+"coworker/relationships/correct", fmt.Sprintf(`{"action":"correct","expectedRevision":4,"relationshipId":%q,"value":"revive stale memory","threadId":%q,"sourceMessageId":%q}`, relationshipID, fixture.table.ID, correctionSource.ID))
	if staleCorrect.Code != http.StatusForbidden && staleCorrect.Code != http.StatusConflict {
		t.Fatalf("stale/revoked correction status=%d body=%s", staleCorrect.Code, staleCorrect.Body.String())
	}

	product, err := fixture.app.strideCoworkerProduct()
	if err != nil {
		t.Fatal(err)
	}
	restarted, err := newDurableSTRIDECollaborationStore(product.collaborationRepo.path, true)
	if err != nil {
		t.Fatal(err)
	}
	principal := strideRuntimePrincipalForEmail(fixture.user.Email)
	restartedConsent, revision, err := restarted.Consent(principal)
	if err != nil || revision != 5 || restartedConsent.Enabled {
		t.Fatalf("restart consent=%+v revision=%d err=%v", restartedConsent, revision, err)
	}
}

func TestSTRIDECoworkerRelationshipRetractsWhenChatSourceChanges(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*testing.T, strideCoworkerTestFixture, scoutChatMessageRecord)
	}{
		{name: "edit", mutate: func(t *testing.T, fixture strideCoworkerTestFixture, source scoutChatMessageRecord) {
			text := "@scout this message no longer grants a preference"
			if _, _, err := fixture.app.editScoutChatThreadMessage(context.Background(), fixture.user, fixture.table.ID, source.ID, &text, nil); err != nil {
				t.Fatalf("edit source: %v", err)
			}
		}},
		{name: "delete", mutate: func(t *testing.T, fixture strideCoworkerTestFixture, source scoutChatMessageRecord) {
			if _, err := fixture.app.deleteScoutChatThreadMessage(fixture.user.Email, fixture.table.ID, source.ID); err != nil {
				t.Fatalf("delete source: %v", err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newSTRIDECoworkerTestFixture(t)
			fixture.runtime.mu.Lock()
			fixture.runtime.config.RelationshipMemoryEnabled = true
			fixture.runtime.mu.Unlock()
			mux := http.NewServeMux()
			registerSTRIDERuntimeRoutes(mux)
			request := func(method, path, body string) *httptest.ResponseRecorder {
				t.Helper()
				req := httptest.NewRequest(method, path, strings.NewReader(body))
				if body != "" {
					req.Header.Set("Content-Type", "application/json")
				}
				for _, cookie := range fixture.cookies {
					req.AddCookie(cookie)
				}
				recorder := httptest.NewRecorder()
				mux.ServeHTTP(recorder, req)
				return recorder
			}
			if response := request(http.MethodPost, strideRuntimeAPIBase+"coworker/relationships/consent", `{"action":"enable","expectedRevision":0,"allowInferred":false,"allowShared":true}`); response.Code != http.StatusOK {
				t.Fatalf("consent status=%d body=%s", response.Code, response.Body.String())
			}
			source := fixture.commitUserMessage(t, "relationship-retracted-source", "@scout remember that I prefer the source-bound value")
			remember := request(http.MethodPost, strideRuntimeAPIBase+"coworker/relationships/remember", fmt.Sprintf(`{"action":"remember","expectedRevision":1,"threadId":%q,"sourceMessageId":%q,"preferenceType":"response_length","value":"source-bound value","scope":"shared"}`, fixture.table.ID, source.ID))
			if remember.Code != http.StatusCreated {
				t.Fatalf("remember status=%d body=%s", remember.Code, remember.Body.String())
			}
			tc.mutate(t, fixture, source)
			product, err := fixture.app.strideCoworkerProduct()
			if err != nil {
				t.Fatal(err)
			}
			principal := strideRuntimePrincipalForEmail(fixture.user.Email)
			if _, revision, err := product.collaborationRepo.Consent(principal); err != nil || revision != 3 {
				t.Fatalf("source mutation did not reconcile relationship memory immediately: revision=%d err=%v", revision, err)
			}
			pending := scoutChatMessageRecord{ID: "relationship-retracted-pending-" + tc.name, Kind: "message", Role: "user", Text: "@scout answer", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), AuthorName: fixture.user.Name, AuthorEmail: fixture.user.Email}
			if query := fixture.app.prepareSTRIDECoworkerModelQuery(fixture.user, fixture.table, pending, "base query"); query != "base query" && strings.Contains(query, "source-bound value") {
				t.Fatalf("unauthorized source remained in model query: %q", query)
			}
			read := request(http.MethodGet, strideRuntimeAPIBase+"coworker/relationships", "")
			if read.Code != http.StatusOK || !strings.Contains(read.Body.String(), `"revision":3`) || !strings.Contains(read.Body.String(), `"preferences":[]`) {
				t.Fatalf("retracted relationship read status=%d body=%s", read.Code, read.Body.String())
			}
			raw, err := os.ReadFile(product.collaborationRepo.path)
			if err != nil || strings.Contains(string(raw), "source-bound value") {
				t.Fatalf("retracted relationship value remained durable: err=%v raw=%s", err, raw)
			}
		})
	}
}

func TestSTRIDECoworkerSettingsCanCreateCorrectAndForgetPrivateMemoryWithoutPublicChat(t *testing.T) {
	fixture := newSTRIDECoworkerTestFixture(t)
	fixture.runtime.mu.Lock()
	fixture.runtime.config.RelationshipMemoryEnabled = true
	fixture.runtime.mu.Unlock()
	mux := http.NewServeMux()
	registerSTRIDERuntimeRoutes(mux)
	request := func(path, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		for _, cookie := range fixture.cookies {
			req.AddCookie(cookie)
		}
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, req)
		return recorder
	}

	consent := request(strideRuntimeAPIBase+"coworker/relationships/consent", `{"action":"enable","expectedRevision":0,"allowInferred":false,"allowShared":false}`)
	if consent.Code != http.StatusOK {
		t.Fatalf("consent status=%d body=%s", consent.Code, consent.Body.String())
	}
	remember := request(strideRuntimeAPIBase+"coworker/relationships/remember", `{"action":"remember","expectedRevision":1,"preferenceType":"feedback_style","value":"Direct, kind, and specific.","scope":"private"}`)
	if remember.Code != http.StatusCreated {
		t.Fatalf("private Settings remember status=%d body=%s", remember.Code, remember.Body.String())
	}
	if !strings.Contains(remember.Body.String(), `"source":{"kind":"settings","label":"Added by you in Settings","available":true}`) {
		t.Fatalf("private Settings response lacks inspectable provenance: %s", remember.Body.String())
	}
	var remembered struct {
		Revision    int64                                  `json:"revision"`
		Preferences []STRIDECollaborationContextPreference `json:"preferences"`
	}
	if err := json.Unmarshal(remember.Body.Bytes(), &remembered); err != nil || remembered.Revision != 2 || len(remembered.Preferences) != 1 {
		t.Fatalf("remembered=%+v err=%v body=%s", remembered, err, remember.Body.String())
	}
	preference := remembered.Preferences[0]
	if preference.Scope != stridePreferencePrivate || preference.Relationship.Audience.Visibility != "private" || len(preference.Evidence) != 1 || !strings.HasPrefix(preference.Evidence[0].ID, "relationship_control_") {
		t.Fatalf("private Settings provenance=%+v", preference)
	}
	privateQuery := fixture.app.prepareSTRIDEPrivateRelationshipModelQuery(fixture.user.Email, "base private query")
	if !strings.Contains(privateQuery, "approved_collaboration_preferences_data") || !strings.Contains(privateQuery, "Direct, kind, and specific.") {
		t.Fatalf("private Settings preference missing from one-to-one Scout query: %q", privateQuery)
	}
	privateAgentInstructions := fixture.app.agentThreadInstructionsForThread(scoutAgentThread{
		Mode: "research",
		Artifact: meetingMemoryEntry{Metadata: map[string]string{
			"originKind":  agentThreadOriginPrivateThread,
			"requestedBy": fixture.user.Email,
			"agentName":   "Colton",
		}},
	})
	if !strings.Contains(privateAgentInstructions, "STRIDE private relationship context") || !strings.Contains(privateAgentInstructions, "Direct, kind, and specific.") {
		t.Fatalf("private Settings preference missing from direct teammate instructions: %q", privateAgentInstructions)
	}
	channelAgentInstructions := fixture.app.agentThreadInstructionsForThread(scoutAgentThread{
		Mode: "research",
		Artifact: meetingMemoryEntry{Metadata: map[string]string{
			"originKind":  agentThreadOriginChannel,
			"requestedBy": fixture.user.Email,
			"agentName":   "Colton",
		}},
	})
	if strings.Contains(channelAgentInstructions, "Direct, kind, and specific.") || strings.Contains(channelAgentInstructions, "STRIDE private relationship context") {
		t.Fatalf("private Settings preference escaped into shared teammate instructions: %q", channelAgentInstructions)
	}
	voiceConfig := fixture.app.privateRealtimeVoiceSessionConfigForUser("gpt-realtime-2", fixture.user.Email)
	voiceInstructions, _ := voiceConfig["instructions"].(string)
	if !strings.Contains(voiceInstructions, "Direct, kind, and specific.") || !strings.Contains(voiceInstructions, "STRIDE private relationship context") {
		t.Fatalf("private Settings preference missing from Realtime Scout instructions: %q", voiceInstructions)
	}
	thread, _, err := fixture.app.scoutChatThreadByID(fixture.user.Email, fixture.table.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range thread.Messages {
		if strings.Contains(message.Text, "Direct, kind, and specific") {
			t.Fatalf("Settings memory manufactured a public chat message: %+v", message)
		}
	}

	correct := request(strideRuntimeAPIBase+"coworker/relationships/correct", fmt.Sprintf(`{"action":"correct","expectedRevision":2,"relationshipId":%q,"value":"Direct and specific, with context when needed."}`, preference.Reference.ID))
	if correct.Code != http.StatusOK || !strings.Contains(correct.Body.String(), "Direct and specific, with context when needed.") || strings.Contains(correct.Body.String(), "Direct, kind, and specific.") {
		t.Fatalf("private Settings correct status=%d body=%s", correct.Code, correct.Body.String())
	}
	stale := request(strideRuntimeAPIBase+"coworker/relationships/correct", fmt.Sprintf(`{"action":"correct","expectedRevision":2,"relationshipId":%q,"value":"stale overwrite"}`, preference.Reference.ID))
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale Settings correct status=%d body=%s", stale.Code, stale.Body.String())
	}
	forget := request(strideRuntimeAPIBase+"coworker/relationships/forget", fmt.Sprintf(`{"action":"forget","expectedRevision":3,"relationshipId":%q}`, preference.Reference.ID))
	if forget.Code != http.StatusOK || !strings.Contains(forget.Body.String(), `"preferences":[]`) {
		t.Fatalf("private Settings forget status=%d body=%s", forget.Code, forget.Body.String())
	}
	product, err := fixture.app.strideCoworkerProduct()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(product.collaborationRepo.path)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"Direct, kind, and specific.", "Direct and specific, with context when needed.", "relationship_control_"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("forgotten Settings value/evidence %q remained on disk: %s", forbidden, raw)
		}
	}
	if query := fixture.app.prepareSTRIDEPrivateRelationshipModelQuery(fixture.user.Email, "base private query"); query != "base private query" {
		t.Fatalf("forgotten private preference remained in one-to-one Scout query: %q", query)
	}

	sharedConsent := request(strideRuntimeAPIBase+"coworker/relationships/consent", `{"action":"enable","expectedRevision":4,"allowInferred":false,"allowShared":true}`)
	if sharedConsent.Code != http.StatusOK {
		t.Fatalf("shared consent status=%d body=%s", sharedConsent.Code, sharedConsent.Body.String())
	}
	sharedSource := fixture.commitUserMessage(t, "settings-shared-source", "@scout remember that I want one paragraph in this channel")
	sharedRemember := request(strideRuntimeAPIBase+"coworker/relationships/remember", fmt.Sprintf(`{"action":"remember","expectedRevision":5,"threadId":%q,"sourceMessageId":%q,"preferenceType":"response_length","value":"one paragraph","scope":"shared"}`, fixture.table.ID, sharedSource.ID))
	if sharedRemember.Code != http.StatusCreated {
		t.Fatalf("shared remember status=%d body=%s", sharedRemember.Code, sharedRemember.Body.String())
	}
	var shared struct {
		Preferences []STRIDECollaborationContextPreference `json:"preferences"`
	}
	if err := json.Unmarshal(sharedRemember.Body.Bytes(), &shared); err != nil || len(shared.Preferences) != 1 || shared.Preferences[0].Scope != stridePreferenceShared {
		t.Fatalf("shared response=%+v err=%v body=%s", shared, err, sharedRemember.Body.String())
	}
	sharedCorrect := request(strideRuntimeAPIBase+"coworker/relationships/correct", fmt.Sprintf(`{"action":"correct","expectedRevision":6,"relationshipId":%q,"value":"one paragraph, then sources"}`, shared.Preferences[0].Reference.ID))
	if sharedCorrect.Code != http.StatusOK || !strings.Contains(sharedCorrect.Body.String(), "one paragraph, then sources") {
		t.Fatalf("shared Settings correction status=%d body=%s", sharedCorrect.Code, sharedCorrect.Body.String())
	}
	channelInstructions := fixture.app.agentThreadInstructionsForThread(scoutAgentThread{
		Mode: "research",
		Artifact: meetingMemoryEntry{Metadata: map[string]string{
			"originKind":  agentThreadOriginChannel,
			"originId":    fixture.table.ID,
			"requestedBy": fixture.user.Email,
			"agentName":   "Colton",
		}},
	})
	if !strings.Contains(channelInstructions, "Authenticated requester: AJ") || !strings.Contains(channelInstructions, "STRIDE shared coworker context") || !strings.Contains(channelInstructions, "one paragraph, then sources") {
		t.Fatalf("shared preference/requester missing from exact-channel coworker instructions: %q", channelInstructions)
	}
	if strings.Contains(channelInstructions, "STRIDE private relationship context") || strings.Contains(channelInstructions, "Direct, kind, and specific") {
		t.Fatalf("private profile escaped into exact-channel coworker instructions: %q", channelInstructions)
	}
	otherThread, err := fixture.app.createScoutChatThread(fixture.user.Email, fixture.user.Name, "shared-memory-other-channel", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatal(err)
	}
	otherInstructions := fixture.app.agentThreadInstructionsForThread(scoutAgentThread{
		Mode: "research",
		Artifact: meetingMemoryEntry{Metadata: map[string]string{
			"originKind":  agentThreadOriginChannel,
			"originId":    otherThread.ID,
			"requestedBy": fixture.user.Email,
			"agentName":   "Colton",
		}},
	})
	if strings.Contains(otherInstructions, "one paragraph, then sources") || strings.Contains(otherInstructions, "STRIDE shared coworker context") {
		t.Fatalf("channel-scoped preference escaped into another channel: %q", otherInstructions)
	}
}

func TestSTRIDECoworkerRelationshipUsesAdvancingServerClockForMutationAndExpiry(t *testing.T) {
	fixture := newSTRIDECoworkerTestFixture(t)
	fixture.runtime.mu.Lock()
	fixture.runtime.config.RelationshipMemoryEnabled = true
	fixture.runtime.mu.Unlock()
	mux := http.NewServeMux()
	registerSTRIDERuntimeRoutes(mux)
	request := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		for _, cookie := range fixture.cookies {
			req.AddCookie(cookie)
		}
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, req)
		return recorder
	}

	if response := request(http.MethodPost, strideRuntimeAPIBase+"coworker/relationships/consent", `{"action":"enable","expectedRevision":0,"allowInferred":false,"allowShared":false}`); response.Code != http.StatusOK {
		t.Fatalf("consent status=%d body=%s", response.Code, response.Body.String())
	}
	activationTime := *fixture.now
	mutationTime := activationTime.Add(30 * 24 * time.Hour)
	*fixture.now = mutationTime
	remember := request(http.MethodPost, strideRuntimeAPIBase+"coworker/relationships/remember", `{"action":"remember","expectedRevision":1,"preferenceType":"response_length","value":"lead with the answer","scope":"private"}`)
	if remember.Code != http.StatusCreated {
		t.Fatalf("remember status=%d body=%s", remember.Code, remember.Body.String())
	}
	var payload struct {
		Preferences []STRIDECollaborationContextPreference `json:"preferences"`
	}
	if err := json.Unmarshal(remember.Body.Bytes(), &payload); err != nil || len(payload.Preferences) != 1 {
		t.Fatalf("remember payload=%+v err=%v body=%s", payload, err, remember.Body.String())
	}
	wantExpiry := mutationTime.Add(180 * 24 * time.Hour)
	if !payload.Preferences[0].ExpiresAt.Equal(wantExpiry) {
		t.Fatalf("expiry=%s want advancing-clock expiry=%s activation=%s", payload.Preferences[0].ExpiresAt, wantExpiry, activationTime)
	}
	product, err := fixture.app.strideCoworkerProduct()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(product.collaborationRepo.path)
	if err != nil || !strings.Contains(string(raw), mutationTime.Format(time.RFC3339)) {
		t.Fatalf("durable mutation time did not use current clock: err=%v raw=%s", err, raw)
	}

	*fixture.now = wantExpiry.Add(time.Minute)
	read := request(http.MethodGet, strideRuntimeAPIBase+"coworker/relationships", "")
	if read.Code != http.StatusOK || !strings.Contains(read.Body.String(), `"preferences":[]`) {
		t.Fatalf("expired relationship remained visible status=%d body=%s", read.Code, read.Body.String())
	}
	raw, err = os.ReadFile(product.collaborationRepo.path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "lead with the answer") {
		t.Fatalf("expired relationship raw value remained on disk: %s", raw)
	}
	if query := fixture.app.prepareSTRIDEPrivateRelationshipModelQuery(fixture.user.Email, "base"); query != "base" {
		t.Fatalf("expired relationship remained in private model context: %q", query)
	}
}

func TestSTRIDECoworkerFileSelectionPostsExactlyOnceAndSurvivesRestart(t *testing.T) {
	fixture := newSTRIDECoworkerTestFixture(t)
	fixture.commitUserMessage(t, "file-invocation", "@scout share the launch gif")
	gif := []byte{71, 73, 70, 56, 57, 97, 1, 0, 1, 0, 128, 0, 0, 0, 0, 0, 255, 255, 255, 33, 249, 4, 1, 0, 0, 0, 0, 44, 0, 0, 0, 0, 1, 0, 1, 0, 0, 2, 2, 68, 1, 0, 59}
	ref, err := putBlob(gif, "image/gif")
	if err != nil {
		t.Fatal(err)
	}
	meta, err := blobStatForRef(ref)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = fixture.app.memory.appendEntry(meetingMemoryKindFile, "drive_launch_gif", "body must not grant authority", map[string]string{"name": "launch.gif", "blobRef": ref, "mime": meta.Mime, "size": fmt.Sprint(meta.Size), "uploaderEmail": fixture.user.Email, "uploaderName": fixture.user.Name, "origin": "files", "brainStatus": fileBrainStatusStored})
	if err != nil {
		t.Fatal(err)
	}
	productContext, err := admittedSTRIDECoworkerProduct(fixture.runtime)
	if err != nil {
		t.Fatal(err)
	}
	product, err := fixture.app.strideCoworkerProduct()
	if err != nil {
		t.Fatal(err)
	}
	source, err := fixture.app.resolveSTRIDECoworkerFileSource(context.Background(), fixture.user, "drive_launch_gif")
	if err != nil {
		t.Fatal(err)
	}
	destination, err := (strideCoworkerFileAuthority{app: fixture.app}).CurrentDestination(context.Background(), fixture.table.ID)
	if err != nil {
		t.Fatal(err)
	}
	service := product.fileService(productContext.Config.Now)
	token, err := service.Mint(context.Background(), STRIDEFileSelectionMintRequest{Requester: fixture.user.Email, Source: source.Object, SourceRevision: source.Revision, Destination: destination, Purpose: "share_existing_file", TTL: 5 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	const executionKey = "file-post-execution-0001"
	receipt, err := service.Post(context.Background(), token.ID, fixture.user.Email, executionKey)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := service.Post(context.Background(), token.ID, fixture.user.Email, executionKey)
	if err != nil || replay != receipt {
		t.Fatalf("same-key replay=%+v err=%v want=%+v", replay, err, receipt)
	}
	if _, err := service.Post(context.Background(), token.ID, fixture.user.Email, "different-execution-0002"); !errors.Is(err, ErrSTRIDEFileDispatchState) {
		t.Fatalf("different-key replay error=%v", err)
	}
	assertOneCoworkerMessage(t, fixture.app, fixture.user.Email, fixture.table.ID, receipt.MessageID)

	restartedRepo, err := newDurableSTRIDEFileSelectionRepository(product.fileRepo.path)
	if err != nil {
		t.Fatal(err)
	}
	restartedService := STRIDEFileSelectionService{Enabled: true, Repo: restartedRepo, Authority: strideCoworkerFileAuthority{app: fixture.app}, Poster: strideCoworkerFilePoster{app: fixture.app}, Now: productContext.Config.Now}
	restartedReceipt, err := restartedService.Post(context.Background(), token.ID, fixture.user.Email, executionKey)
	if err != nil || restartedReceipt != receipt {
		t.Fatalf("restart replay=%+v err=%v", restartedReceipt, err)
	}
	assertOneCoworkerMessage(t, fixture.app, fixture.user.Email, fixture.table.ID, receipt.MessageID)

	// A currently visible row whose exact blob revision changes after selection
	// cannot be posted from the stale handle.
	stale, err := service.Mint(context.Background(), STRIDEFileSelectionMintRequest{Requester: fixture.user.Email, Source: source.Object, SourceRevision: source.Revision, Destination: destination, Purpose: "share_existing_file", TTL: 5 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	otherRef, err := putBlob(append([]byte(nil), gif...), "image/gif")
	if err != nil {
		t.Fatal(err)
	}
	// Same bytes dedupe, so pin a distinct valid GIF payload.
	if otherRef == ref {
		other := append([]byte(nil), gif...)
		other[13] = 1
		otherRef, err = putBlob(other, "image/gif")
		if err != nil {
			t.Fatal(err)
		}
	}
	fixture.app.memory.mu.Lock()
	for index := range fixture.app.memory.entries {
		if fixture.app.memory.entries[index].ID == "drive_launch_gif" {
			fixture.app.memory.entries[index].Metadata["blobRef"] = otherRef
		}
	}
	fixture.app.memory.mu.Unlock()
	if _, err := service.Post(context.Background(), stale.ID, fixture.user.Email, "stale-source-exec-0003"); !errors.Is(err, ErrSTRIDEFileHandleDenied) {
		t.Fatalf("stale source post error=%v", err)
	}
}

func TestSTRIDECoworkerFileSelectionRaceCannotDuplicateMessage(t *testing.T) {
	fixture := newSTRIDECoworkerTestFixture(t)
	fixture.commitUserMessage(t, "file-race-invocation", "@scout share the race fixture")
	ref, err := putBlob([]byte{71, 73, 70, 56, 57, 97, 1, 0, 1, 0, 128, 0, 0, 0, 0, 0, 255, 255, 255, 33, 249, 4, 1, 0, 0, 0, 0, 44, 0, 0, 0, 0, 1, 0, 1, 0, 0, 2, 2, 68, 1, 0, 59}, "image/gif")
	if err != nil {
		t.Fatal(err)
	}
	meta, _ := blobStatForRef(ref)
	_, _, _ = fixture.app.memory.appendEntry(meetingMemoryKindFile, "drive_race_gif", "race fixture", map[string]string{"name": "race.gif", "blobRef": ref, "mime": meta.Mime, "size": fmt.Sprint(meta.Size), "uploaderEmail": fixture.user.Email, "uploaderName": fixture.user.Name, "origin": "files", "brainStatus": fileBrainStatusStored})
	productContext, _ := admittedSTRIDECoworkerProduct(fixture.runtime)
	product, _ := fixture.app.strideCoworkerProduct()
	source, err := fixture.app.resolveSTRIDECoworkerFileSource(context.Background(), fixture.user, "drive_race_gif")
	if err != nil {
		t.Fatal(err)
	}
	destination, _ := (strideCoworkerFileAuthority{app: fixture.app}).CurrentDestination(context.Background(), fixture.table.ID)
	service := product.fileService(productContext.Config.Now)
	token, err := service.Mint(context.Background(), STRIDEFileSelectionMintRequest{Requester: fixture.user.Email, Source: source.Object, SourceRevision: source.Revision, Destination: destination, Purpose: "share_existing_file", TTL: 5 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	var successes atomic.Int32
	var wg sync.WaitGroup
	for index := 0; index < 24; index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, postErr := service.Post(context.Background(), token.ID, fixture.user.Email, "race-file-execution-0004"); postErr == nil {
				successes.Add(1)
			} else if !errors.Is(postErr, ErrSTRIDEFileDispatchUnknown) {
				t.Errorf("race post error=%v", postErr)
			}
		}()
	}
	wg.Wait()
	if successes.Load() < 1 {
		t.Fatal("no race caller observed the confirmed post")
	}
	messageID := "scout-coworker-file-" + sha256Hex([]byte(token.ID))[:24]
	assertOneCoworkerMessage(t, fixture.app, fixture.user.Email, fixture.table.ID, messageID)
}

func TestSTRIDECoworkerGIFIsLocalGatedAndExactlyOnce(t *testing.T) {
	fixture := newSTRIDECoworkerTestFixture(t)
	source := fixture.commitUserMessage(t, "gif-invocation", "@scout what did you think of that joke?")
	thread, _, _ := fixture.app.scoutChatThreadByID(fixture.user.Email, fixture.table.ID)
	product, err := fixture.app.strideCoworkerProduct()
	if err != nil {
		t.Fatal(err)
	}
	message, replayed, action, err := product.postLocalGIF(context.Background(), fixture.user, thread, source, "laugh", "playful", "gif-execution-key-0001")
	if err != nil || replayed || action.Provider != "local_fixture" || action.Rating != "g" || !action.Immutable {
		t.Fatalf("initial GIF message=%+v replayed=%t action=%+v err=%v", message, replayed, action, err)
	}
	thread, _, _ = fixture.app.scoutChatThreadByID(fixture.user.Email, fixture.table.ID)
	replayedMessage, replayed, _, err := product.postLocalGIF(context.Background(), fixture.user, thread, source, "laugh", "playful", "gif-execution-key-0001")
	if err != nil || !replayed || replayedMessage.ID != message.ID {
		t.Fatalf("GIF replay message=%+v replayed=%t err=%v", replayedMessage, replayed, err)
	}
	assertOneCoworkerMessage(t, fixture.app, fixture.user.Email, fixture.table.ID, message.ID)
	if _, _, _, err := product.postLocalGIF(context.Background(), fixture.user, thread, source, "agree", "warm", "gif-execution-key-0001"); !errors.Is(err, ErrSTRIDECoworkerConflict) {
		t.Fatalf("same-key conflicting GIF error=%v", err)
	}
	sensitive := fixture.commitUserMessage(t, "gif-sensitive", "@scout make a joke about the layoffs")
	thread, _, _ = fixture.app.scoutChatThreadByID(fixture.user.Email, fixture.table.ID)
	if _, _, _, err := product.postLocalGIF(context.Background(), fixture.user, thread, sensitive, "laugh", "playful", "gif-execution-key-0002"); !errors.Is(err, ErrSTRIDEGIFDenied) {
		t.Fatalf("sensitive GIF error=%v", err)
	}

	raceSource := fixture.commitUserMessage(t, "gif-race", "@scout celebrate this milestone")
	thread, _, _ = fixture.app.scoutChatThreadByID(fixture.user.Email, fixture.table.ID)
	var wg sync.WaitGroup
	for index := 0; index < 16; index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, _, _, raceErr := product.postLocalGIF(context.Background(), fixture.user, thread, raceSource, "celebrate", "playful", "gif-race-execution-0003"); raceErr != nil {
				t.Errorf("GIF race error=%v", raceErr)
			}
		}()
	}
	wg.Wait()
	raceID := "scout-coworker-gif-" + sha256Hex([]byte("gif-race-execution-0003"))[:24]
	assertOneCoworkerMessage(t, fixture.app, fixture.user.Email, fixture.table.ID, raceID)
}

func TestSTRIDECoworkerDurableFileRepoFencesInterruptedRestartAndCorruption(t *testing.T) {
	fixture := newSTRIDECoworkerTestFixture(t)
	fixture.commitUserMessage(t, "repo-invocation", "@scout share the durable fixture")
	data := []byte{71, 73, 70, 56, 57, 97, 1, 0, 1, 0, 128, 0, 0, 0, 0, 0, 255, 255, 255, 33, 249, 4, 1, 0, 0, 0, 0, 44, 0, 0, 0, 0, 1, 0, 1, 0, 0, 2, 2, 68, 1, 0, 59}
	ref, err := putBlob(data, "image/gif")
	if err != nil {
		t.Fatal(err)
	}
	meta, _ := blobStatForRef(ref)
	_, _, err = fixture.app.memory.appendEntry(meetingMemoryKindFile, "durable_repo_file", "durable fixture", map[string]string{"name": "durable.gif", "blobRef": ref, "mime": meta.Mime, "size": fmt.Sprint(meta.Size), "uploaderEmail": fixture.user.Email, "uploaderName": fixture.user.Name, "origin": "files", "brainStatus": fileBrainStatusStored})
	if err != nil {
		t.Fatal(err)
	}
	productContext, _ := admittedSTRIDECoworkerProduct(fixture.runtime)
	product, _ := fixture.app.strideCoworkerProduct()
	source, err := fixture.app.resolveSTRIDECoworkerFileSource(context.Background(), fixture.user, "durable_repo_file")
	if err != nil {
		t.Fatal(err)
	}
	destination, _ := (strideCoworkerFileAuthority{app: fixture.app}).CurrentDestination(context.Background(), fixture.table.ID)
	service := product.fileService(productContext.Config.Now)
	token, err := service.Mint(context.Background(), STRIDEFileSelectionMintRequest{Requester: fixture.user.Email, Source: source.Object, SourceRevision: source.Revision, Destination: destination, Purpose: "share_existing_file", TTL: 5 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if err := product.fileRepo.Transact(context.Background(), token.ID, func(record *STRIDEFileSelectionRecord) error {
		record.Status = strideFileSelectionSending
		record.ExecutionKeyDigest = strideExecutionKeyDigest("interrupted-execution-0001")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	restarted, err := newDurableSTRIDEFileSelectionRepository(product.fileRepo.path)
	if err != nil {
		t.Fatal(err)
	}
	record, err := restarted.Read(context.Background(), token.ID)
	if err != nil || record.Status != strideFileSelectionAmbiguous || record.LastError != "dispatch interrupted by restart" {
		t.Fatalf("restart record=%+v err=%v", record, err)
	}
	restartedService := STRIDEFileSelectionService{Enabled: true, Repo: restarted, Authority: strideCoworkerFileAuthority{app: fixture.app}, Poster: strideCoworkerFilePoster{app: fixture.app}, Now: productContext.Config.Now}
	if _, err := restartedService.Post(context.Background(), token.ID, fixture.user.Email, "interrupted-execution-0001"); !errors.Is(err, ErrSTRIDEFileDispatchUnknown) {
		t.Fatalf("interrupted dispatch error=%v", err)
	}
	messageID := "scout-coworker-file-" + sha256Hex([]byte(token.ID))[:24]
	thread, _, _ := fixture.app.scoutChatThreadByID(fixture.user.Email, fixture.table.ID)
	if scoutChatMessageIndex(thread, messageID) >= 0 {
		t.Fatal("interrupted dispatch was retried after restart")
	}
	if err := writeFileAtomicallyDurable(product.fileRepo.path, []byte("{corrupt\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := newDurableSTRIDEFileSelectionRepository(product.fileRepo.path); !errors.Is(err, ErrSTRIDEFileHandleDenied) {
		t.Fatalf("corrupt repository error=%v", err)
	}
}

func TestSTRIDECoworkerHTTPFileAndGIFActionsAreExplicitAndProviderFenced(t *testing.T) {
	fixture := newSTRIDECoworkerTestFixture(t)
	fileInvocation := fixture.commitUserMessage(t, "http-file-invocation", "@scout share the drive fixture")
	gifData := []byte{71, 73, 70, 56, 57, 97, 1, 0, 1, 0, 128, 0, 0, 0, 0, 0, 255, 255, 255, 33, 249, 4, 1, 0, 0, 0, 0, 44, 0, 0, 0, 0, 1, 0, 1, 0, 0, 2, 2, 68, 1, 0, 59}
	ref, err := putBlob(gifData, "image/gif")
	if err != nil {
		t.Fatal(err)
	}
	meta, _ := blobStatForRef(ref)
	_, _, err = fixture.app.memory.appendEntry(meetingMemoryKindFile, "http_drive_fixture", "not an authority body", map[string]string{"name": "fixture.gif", "blobRef": ref, "mime": meta.Mime, "size": fmt.Sprint(meta.Size), "uploaderEmail": fixture.user.Email, "uploaderName": fixture.user.Name, "origin": "files", "brainStatus": fileBrainStatusStored})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	registerSTRIDERuntimeRoutes(mux)
	request := func(path, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		for _, cookie := range fixture.cookies {
			req.AddCookie(cookie)
		}
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, req)
		return recorder
	}
	selectRecorder := request(strideRuntimeAPIBase+"coworker/files/select", fmt.Sprintf(`{"action":"select","threadId":%q,"sourceMessageId":%q,"fileId":"http_drive_fixture"}`, fixture.table.ID, fileInvocation.ID))
	if selectRecorder.Code != http.StatusCreated || !strings.Contains(selectRecorder.Body.String(), `"providerExecutionFenced":true`) {
		t.Fatalf("select status=%d body=%s", selectRecorder.Code, selectRecorder.Body.String())
	}
	var selected struct {
		HandleID string `json:"handleId"`
	}
	if json.Unmarshal(selectRecorder.Body.Bytes(), &selected) != nil || selected.HandleID == "" {
		t.Fatalf("invalid select response: %s", selectRecorder.Body.String())
	}
	postBody := fmt.Sprintf(`{"action":"post","handleId":%q,"executionKey":"http-file-execution-0001"}`, selected.HandleID)
	postRecorder := request(strideRuntimeAPIBase+"coworker/files/post", postBody)
	if postRecorder.Code != http.StatusOK || !strings.Contains(postRecorder.Body.String(), `"providerExecutionFenced":true`) {
		t.Fatalf("post status=%d body=%s", postRecorder.Code, postRecorder.Body.String())
	}
	replayRecorder := request(strideRuntimeAPIBase+"coworker/files/post", postBody)
	if replayRecorder.Code != http.StatusOK || replayRecorder.Body.String() != postRecorder.Body.String() {
		t.Fatalf("file replay status/body=%d %s want=%s", replayRecorder.Code, replayRecorder.Body.String(), postRecorder.Body.String())
	}

	gifInvocation := fixture.commitUserMessage(t, "http-gif-invocation", "@scout react to the good news")
	gifRecorder := request(strideRuntimeAPIBase+"coworker/gifs/post", fmt.Sprintf(`{"action":"post","threadId":%q,"sourceMessageId":%q,"reaction":"celebrate","tone":"playful","executionKey":"http-gif-execution-0001"}`, fixture.table.ID, gifInvocation.ID))
	if gifRecorder.Code != http.StatusOK || !strings.Contains(gifRecorder.Body.String(), `"provider":"local_fixture"`) || !strings.Contains(gifRecorder.Body.String(), `"providerExecutionFenced":true`) {
		t.Fatalf("GIF status=%d body=%s", gifRecorder.Code, gifRecorder.Body.String())
	}
	implicit := request(strideRuntimeAPIBase+"coworker/gifs/post", fmt.Sprintf(`{"threadId":%q,"sourceMessageId":%q,"reaction":"celebrate","tone":"playful","executionKey":"http-gif-execution-0002"}`, fixture.table.ID, gifInvocation.ID))
	if implicit.Code != http.StatusBadRequest {
		t.Fatalf("implicit GIF status=%d body=%s", implicit.Code, implicit.Body.String())
	}
}

func assertOneCoworkerMessage(t *testing.T, app *kanbanBoardApp, viewer, threadID, messageID string) {
	t.Helper()
	thread, _, err := app.scoutChatThreadByID(viewer, threadID)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, message := range thread.Messages {
		if message.ID == messageID {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("message %s count=%d want=1", messageID, count)
	}
}
