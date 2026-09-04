package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStudioWorkUsageExactIdentityAndUnknownCost(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	entries := []llmUsageEntry{
		{TS: now, Provider: "openai", Model: "priced", GoalID: "root", ThreadID: "run", InputTokens: 20, CachedInputTokens: 5, OutputTokens: 10, EstCostUSD: .02},
		{TS: now, Provider: "anthropic", Model: "unpriced", ThreadID: "child", CacheCreationTokens: 7, OutputTokens: 3, PriceMissing: true, EstCostUSD: 999},
		{TS: now, Provider: "openai", Model: "estimated", GoalID: "root", Estimated: true},
		{TS: now, Provider: "hidden", Model: "private", GoalID: "root", ThreadID: "private-child", InputTokens: 9000},
		{TS: now, Provider: "hidden", Model: "wrong-goal", GoalID: "other", ThreadID: "run", InputTokens: 9000},
		{TS: now, Provider: "hidden", Model: "conversation", ThreadID: "source-chat", InputTokens: 9000},
		{TS: now, Provider: "hidden", Model: "untagged", InputTokens: 9000},
		{TS: now.Add(time.Hour), Provider: "hidden", Model: "future", GoalID: "root", InputTokens: 9000},
	}
	var body strings.Builder
	for _, entry := range entries {
		raw, _ := json.Marshal(entry)
		body.Write(raw)
		body.WriteByte('\n')
	}
	path := filepath.Join(dir, "usage-2026-09-04.jsonl")
	if err := os.WriteFile(path, []byte(body.String()), 0600); err != nil {
		t.Fatal(err)
	}
	view := readStudioWorkUsage(context.Background(), dir, now, map[string]bool{"root": true}, map[string]bool{"run": true, "child": true}, studioWorkUsageMaxBytes)
	if view.Coverage != "partial" || view.Calls != 3 || view.InputTokens != 20 || view.CachedInputTokens != 5 || view.CacheCreationTokens != 7 || view.OutputTokens != 13 || view.EstimatedCostUSD != .02 || view.UnpricedCalls != 2 || view.EstimatedUsageCalls != 1 || len(view.Models) != 3 {
		t.Fatalf("incorrect attributed usage: %+v", view)
	}
	if view.ScanLimited || view.ReadErrors {
		t.Fatalf("unexpected scan state: %+v", view)
	}
	limited := readStudioWorkUsage(context.Background(), dir, now, map[string]bool{"root": true}, nil, 10)
	if !limited.ScanLimited || limited.Coverage != "unavailable" || limited.Calls != 0 {
		t.Fatalf("bounded scan claimed data: %+v", limited)
	}
	missing := readStudioWorkUsage(context.Background(), t.TempDir(), now, nil, nil, studioWorkUsageMaxBytes)
	if missing.Coverage != "unavailable" || missing.Calls != 0 {
		t.Fatalf("missing ledger claimed free work: %+v", missing)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if view := readStudioWorkUsage(cancelled, dir, now, nil, nil, studioWorkUsageMaxBytes); !view.ScanLimited {
		t.Fatal("cancelled scan did not stop")
	}
}

func TestStudioWorkUsageDetailOnlyAndViewerFence(t *testing.T) {
	setupAuthTestEnv(t)
	setUsageLedgerDirForTest(t)
	priorApp, priorAuthorizer := kanbanApp, artifactObjectAuthorizer
	kanbanApp, artifactObjectAuthorizer = newIsolatedKanbanBoardApp(t), LegacyCompatibleObjectAuthorizer{}
	t.Cleanup(func() { kanbanApp, artifactObjectAuthorizer = priorApp, priorAuthorizer })
	owner := "aj@shareability.com"
	thread, err := kanbanApp.createScoutChatThread(owner, "AJ", "Private work", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatal(err)
	}
	root := seedStudioProjectRoot(t, kanbanApp, thread, documentReportProcessID, "Attributable work", goalStateBlocked)
	var children []goalSubtask
	for _, childOwner := range []string{owner, "tim@shareability.com"} {
		child, _, err := kanbanApp.createOSArtifactWithMetadata("research", "Child output", "body", "Scout", map[string]string{
			"source": "scout_thread", "type": artifactTypeMarkdown, "goalDeliverable": "true", "goalParentId": root.ID,
			"threadId": "child-" + childOwner, "visibility": "private", "ownerEmail": childOwner, "requestedBy": childOwner,
		})
		if err != nil {
			t.Fatal(err)
		}
		children = append(children, goalSubtask{ID: child.ID, ArtifactID: child.ID})
		recordLLMUsage(llmUsageEntry{Provider: "anthropic", Model: "child-model", GoalID: root.ID, ThreadID: "child-" + childOwner, InputTokens: 25, PriceMissing: true})
	}
	root = updateStudioProjectPlan(t, kanbanApp, root, func(plan *goalPlan) { plan.Subtasks = children })
	recordLLMUsage(llmUsageEntry{Provider: "openai", Model: "gpt-5.5", GoalID: root.ID, ThreadID: root.Metadata["threadId"], InputTokens: 100})
	recordLLMUsage(llmUsageEntry{Provider: "private-other", Model: "secret", GoalID: "someone-else", InputTokens: 100000})
	cookies := loginAs(t, owner, "B0NFIRE!")
	list := artifactAuthorizationRequest(t, http.MethodGet, "/api/studio-projects/v1", "", cookies, studioProjectsHandler)
	if list.Code != 200 || strings.Contains(list.Body.String(), `"usage"`) {
		t.Fatalf("list must not scan or expose usage: %d %s", list.Code, list.Body.String())
	}
	detail := artifactAuthorizationRequest(t, http.MethodGet, "/api/studio-projects/v1?id="+root.ID, "", cookies, studioProjectsHandler)
	var payload struct {
		Project studioProjectView `json:"project"`
	}
	if err := json.Unmarshal(detail.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if detail.Code != 200 || payload.Project.Usage == nil || payload.Project.Usage.Calls != 2 || payload.Project.Usage.InputTokens != 125 || strings.Contains(detail.Body.String(), "private-other") {
		t.Fatalf("detail: %d %s", detail.Code, detail.Body.String())
	}
	other := loginAs(t, "tim@shareability.com", "B0NFIRE!")
	denied := artifactAuthorizationRequest(t, http.MethodGet, "/api/studio-projects/v1?id="+root.ID, "", other, studioProjectsHandler)
	if denied.Code != 404 || strings.Contains(denied.Body.String(), `"usage"`) {
		t.Fatalf("private work usage exposed: %d %s", denied.Code, denied.Body.String())
	}
}

func TestWorkUsageContextReachesOpenAIWireLedger(t *testing.T) {
	dir := openAIResponsesLedgerDir(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		if _, ok := payload["goal_id"]; ok {
			t.Error("server attribution leaked onto provider wire")
		}
		if _, ok := payload["thread_id"]; ok {
			t.Error("server attribution leaked onto provider wire")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"model":"gpt-5.5","output":[{"type":"message","content":[{"type":"output_text","text":"done"}]}],"usage":{"input_tokens":10,"output_tokens":5}}`))
	}))
	defer server.Close()
	routeOpenAIResponsesToTestServer(t, server.URL)
	thread := scoutAgentThread{ID: "server-child", Artifact: meetingMemoryEntry{ID: "child-artifact", Metadata: map[string]string{"goalParentId": "server-root"}}}
	ctx, cancel := agentThreadRequestContext(context.Background(), thread)
	defer cancel()
	if _, err := createOpenAITextResponseHTTP(ctx, "test-key", openAITextRequest{Model: "gpt-5.5", Seat: seatDeliverable, Input: "draft"}); err != nil {
		t.Fatal(err)
	}
	rows := readLedgerLines(t, filepath.Join(dir, "usage-2026-07-11.jsonl"))
	if len(rows) != 1 || rows[0]["thread_id"] != "server-child" || rows[0]["goal_id"] != "server-root" {
		t.Fatalf("missing server identity: %+v", rows)
	}
}
