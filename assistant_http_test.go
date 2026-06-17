package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAssistantQueryRequiresAuth(t *testing.T) {
	setupAuthTestEnv(t)

	req := httptest.NewRequest(http.MethodPost, "/assistant/query", strings.NewReader(`{"query":"what is blocked?"}`))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	assistantQueryHandler(recorder, req)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d, want %d", recorder.Code, http.StatusUnauthorized)
	}
}

func TestAssistantQueryAnswersFromBoardWithoutRoom(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	req := httptest.NewRequest(http.MethodPost, "/assistant/query", strings.NewReader(`{"query":"what is the status of Finish RTP HEVC Packetizer?","mode":"chat"}`))
	req.Header.Set("Content-Type", "application/json")
	for _, cookie := range loginAs(t, "aj@shareability.com", "B0NFIRE!") {
		req.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()

	assistantQueryHandler(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s, want %d", recorder.Code, recorder.Body.String(), http.StatusOK)
	}
	var payload struct {
		Answer string `json:"answer"`
		Source string `json:"source"`
		Mode   string `json:"mode"`
		User   string `json:"user"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.Source != "board" {
		t.Fatalf("source=%q, want board", payload.Source)
	}
	if payload.Mode != "chat" || payload.User != "AJ" {
		t.Fatalf("payload mode/user=%q/%q, want chat/AJ", payload.Mode, payload.User)
	}
	if !strings.Contains(payload.Answer, "Finish RTP HEVC Packetizer") {
		t.Fatalf("answer=%q, want board card answer", payload.Answer)
	}
}

func TestAssistantQueryShapesOSModesWithoutModelKey(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	req := httptest.NewRequest(http.MethodPost, "/assistant/query", strings.NewReader(`{
		"query":"Our BI OS helps operators spot revenue risk because it connects meetings, artifacts, and board work to decisions. We need a pilot with three customers and a clear weekly scorecard.",
		"mode":"grill"
	}`))
	req.Header.Set("Content-Type", "application/json")
	for _, cookie := range loginAs(t, "aj@shareability.com", "B0NFIRE!") {
		req.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()

	assistantQueryHandler(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s, want %d", recorder.Code, recorder.Body.String(), http.StatusOK)
	}
	var payload struct {
		Answer   string              `json:"answer"`
		Source   string              `json:"source"`
		Mode     string              `json:"mode"`
		Actions  []osAssistantAction `json:"actions"`
		Artifact meetingMemoryEntry  `json:"artifact"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.Mode != "grill" || payload.Source != "grill" {
		t.Fatalf("payload mode/source=%q/%q, want grill/grill", payload.Mode, payload.Source)
	}
	for _, want := range []string{"Grill mode scorecard", "Final score:", "Tough questions"} {
		if !strings.Contains(payload.Answer, want) {
			t.Fatalf("grill answer missing %q: %s", want, payload.Answer)
		}
	}

	artifacts := kanbanApp.osArtifactsSnapshot(10)
	if len(artifacts) != 1 {
		t.Fatalf("artifacts=%d, want 1 saved grill artifact", len(artifacts))
	}
	if artifacts[0].Kind != meetingMemoryKindOSArtifact || artifacts[0].Metadata["mode"] != "grill" {
		t.Fatalf("artifact kind/mode=%q/%q, want os_artifact/grill", artifacts[0].Kind, artifacts[0].Metadata["mode"])
	}
	if !strings.Contains(artifacts[0].Text, "Grill mode scorecard") {
		t.Fatalf("artifact text=%q, want scorecard", artifacts[0].Text)
	}
	if payload.Artifact.ID == "" {
		t.Fatalf("response missing saved artifact: %#v", payload)
	}
	if !hasAssistantAction(payload.Actions, "open_tool", "grill", payload.Artifact.ID) {
		t.Fatalf("actions=%#v, want grill open_tool for artifact %q", payload.Actions, payload.Artifact.ID)
	}
}

func TestAssistantQueryInfersOSNavigationActions(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	artifact, _, err := kanbanApp.createOSArtifact("research", "latest customer brief", "Research brief\n\nCustomer evidence.", "AJ")
	if err != nil {
		t.Fatalf("createOSArtifact: %v", err)
	}

	artifactPayload := postAssistantQueryForTest(t, `{"query":"open the latest artifact and show the artifacts app","mode":"chat"}`)
	if !hasAssistantAction(artifactPayload.Actions, "open_tool", "artifacts", artifact.ID) {
		t.Fatalf("actions=%#v, want artifacts open_tool for %q", artifactPayload.Actions, artifact.ID)
	}
	if !hasAssistantAction(artifactPayload.Actions, "select_artifact", "artifacts", artifact.ID) {
		t.Fatalf("actions=%#v, want select_artifact for %q", artifactPayload.Actions, artifact.ID)
	}

	designPayload := postAssistantQueryForTest(t, `{"query":"kick off design work for the investor workflow","mode":"chat"}`)
	if !hasAssistantAction(designPayload.Actions, "open_tool", "design", "") {
		t.Fatalf("actions=%#v, want design open_tool", designPayload.Actions)
	}
}

func TestArtifactsHandlerListsSavedArtifactsForSignedInUser(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	if _, _, err := kanbanApp.createOSArtifact("research", "how should we validate demand?", "Research brief\n\n1. Interview operators.", "AJ"); err != nil {
		t.Fatalf("createOSArtifact: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/artifacts", nil)
	for _, cookie := range loginAs(t, "aj@shareability.com", "B0NFIRE!") {
		req.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()

	artifactsHandler(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s, want %d", recorder.Code, recorder.Body.String(), http.StatusOK)
	}
	var payload struct {
		Artifacts []meetingMemoryEntry `json:"artifacts"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(payload.Artifacts) != 1 {
		t.Fatalf("artifacts=%d, want 1", len(payload.Artifacts))
	}
	if payload.Artifacts[0].Metadata["title"] == "" {
		t.Fatalf("artifact missing title metadata: %#v", payload.Artifacts[0])
	}
}

func TestArtifactsHandlerUpdatesSavedArtifactForSignedInUser(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	artifact, _, err := kanbanApp.createOSArtifact("design", "draft a workspace", "Design kickoff\n\nOriginal body.", "AJ")
	if err != nil {
		t.Fatalf("createOSArtifact: %v", err)
	}

	body := fmt.Sprintf(`{"id":%q,"title":"Edited artifact","text":"Edited body\n\nWith details."}`, artifact.ID)
	req := httptest.NewRequest(http.MethodPatch, "/artifacts", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for _, cookie := range loginAs(t, "aj@shareability.com", "B0NFIRE!") {
		req.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()

	artifactsHandler(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s, want %d", recorder.Code, recorder.Body.String(), http.StatusOK)
	}
	var payload struct {
		Updated  bool               `json:"updated"`
		Artifact meetingMemoryEntry `json:"artifact"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !payload.Updated {
		t.Fatal("updated=false, want true")
	}
	if payload.Artifact.Text != "Edited body\n\nWith details." {
		t.Fatalf("artifact text=%q, want edited body", payload.Artifact.Text)
	}
	if payload.Artifact.Metadata["title"] != "Edited artifact" || payload.Artifact.Metadata["updatedBy"] != "AJ" {
		t.Fatalf("artifact metadata=%v, want title and updater", payload.Artifact.Metadata)
	}
}

func postAssistantQueryForTest(t *testing.T, body string) struct {
	Actions []osAssistantAction `json:"actions"`
} {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, "/assistant/query", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for _, cookie := range loginAs(t, "aj@shareability.com", "B0NFIRE!") {
		req.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()

	assistantQueryHandler(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s, want %d", recorder.Code, recorder.Body.String(), http.StatusOK)
	}
	var payload struct {
		Actions []osAssistantAction `json:"actions"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return payload
}

func hasAssistantAction(actions []osAssistantAction, actionType string, tool string, artifactID string) bool {
	for _, action := range actions {
		if action.Type != actionType || action.Tool != tool {
			continue
		}
		if artifactID != "" && action.ArtifactID != artifactID {
			continue
		}
		return true
	}
	return false
}
