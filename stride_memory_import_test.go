package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

type strideMemoryImportTestResponse struct {
	Revision            int64                                  `json:"revision"`
	Consent             STRIDECollaborationMemoryConsent       `json:"consent"`
	Preferences         []STRIDECollaborationContextPreference `json:"preferences"`
	ImportedCount       int                                    `json:"importedCount"`
	AlreadyPresentCount int                                    `json:"alreadyPresentCount"`
}

func TestSTRIDEMemoryImportIsAtomicIdempotentAndSharedAcrossPrivateCoworkers(t *testing.T) {
	fixture := newSTRIDECoworkerTestFixture(t)
	fixture.runtime.mu.Lock()
	fixture.runtime.config.RelationshipMemoryEnabled = true
	fixture.runtime.mu.Unlock()
	mux := http.NewServeMux()
	registerSTRIDERuntimeRoutes(mux)
	request := func(expectedRevision int64, entries []strideRelationshipImportEntry) *httptest.ResponseRecorder {
		t.Helper()
		body, err := json.Marshal(map[string]any{"action": "import", "expectedRevision": expectedRevision, "entries": entries})
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, strideRuntimeAPIBase+"coworker/relationships/import", strings.NewReader(string(body)))
		req.Header.Set("Content-Type", "application/json")
		for _, cookie := range fixture.cookies {
			req.AddCookie(cookie)
		}
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, req)
		return recorder
	}

	longInstruction := "Lead with the outcome, then preserve this provider paragraph: " + strings.Repeat("context ", 4_000)
	entries := []strideRelationshipImportEntry{
		{Category: "instructions", Date: "unknown", Value: longInstruction},
		{Category: "projects", Date: "2026-08-05", Value: "Orchid launch research and partner evidence"},
	}
	first := request(0, entries)
	if first.Code != http.StatusOK {
		t.Fatalf("first import status=%d body=%s", first.Code, first.Body.String())
	}
	var imported strideMemoryImportTestResponse
	if err := json.Unmarshal(first.Body.Bytes(), &imported); err != nil {
		t.Fatal(err)
	}
	if imported.Revision != 1 || imported.ImportedCount != 2 || imported.AlreadyPresentCount != 0 || !imported.Consent.Enabled || imported.Consent.AllowInferred || imported.Consent.AllowShared || len(imported.Preferences) != 2 {
		t.Fatalf("atomic import=%+v body=%s", imported, first.Body.String())
	}
	for _, preference := range imported.Preferences {
		if preference.Relationship.AgentID != "stride_coworker_foundation" || !strings.Contains(preference.PreferenceType, "_import_") || preference.Scope != stridePreferencePrivate || !preference.ExpiresAt.Equal(strideMemoryImportDurableExpiresAt) {
			t.Fatalf("import was agent-scoped or non-private: %+v", preference)
		}
	}
	if !strings.Contains(first.Body.String(), strings.Repeat("context ", 70)) {
		t.Fatal("provider paragraph longer than 500 characters was not preserved")
	}

	repeat := request(imported.Revision, entries)
	if repeat.Code != http.StatusOK {
		t.Fatalf("repeat import status=%d body=%s", repeat.Code, repeat.Body.String())
	}
	var repeated strideMemoryImportTestResponse
	if err := json.Unmarshal(repeat.Body.Bytes(), &repeated); err != nil {
		t.Fatal(err)
	}
	if repeated.Revision != imported.Revision || repeated.ImportedCount != 0 || repeated.AlreadyPresentCount != 2 || len(repeated.Preferences) != 2 {
		t.Fatalf("repeat import was not idempotent: %+v", repeated)
	}

	mergedEntries := append(append([]strideRelationshipImportEntry(nil), entries...), strideRelationshipImportEntry{Category: "career", Date: "2026-08-05", Value: "New provider context merges later"})
	merged := request(repeated.Revision, mergedEntries)
	if merged.Code != http.StatusOK {
		t.Fatalf("merge import status=%d body=%s", merged.Code, merged.Body.String())
	}
	var mergeResponse strideMemoryImportTestResponse
	if err := json.Unmarshal(merged.Body.Bytes(), &mergeResponse); err != nil {
		t.Fatal(err)
	}
	if mergeResponse.Revision != 2 || mergeResponse.ImportedCount != 1 || mergeResponse.AlreadyPresentCount != 2 || len(mergeResponse.Preferences) != 3 {
		t.Fatalf("repeatable merge=%+v", mergeResponse)
	}

	privateAgentInstructions := fixture.app.agentThreadInstructionsForThread(scoutAgentThread{Mode: "research", Artifact: meetingMemoryEntry{Metadata: map[string]string{
		"originKind": agentThreadOriginPrivateThread, "requestedBy": fixture.user.Email, "agentName": "Colton",
	}}})
	if !strings.Contains(privateAgentInstructions, "Lead with the outcome") || !strings.Contains(privateAgentInstructions, "STRIDE private relationship context") {
		t.Fatalf("Colton did not receive the same authorized user foundation: %q", privateAgentInstructions)
	}
	for _, origin := range []string{agentThreadOriginChannel, agentThreadOriginRoom} {
		instructions := fixture.app.agentThreadInstructionsForThread(scoutAgentThread{Mode: "research", Artifact: meetingMemoryEntry{Metadata: map[string]string{
			"originKind": origin, "originId": fixture.table.ID, "originMeetingId": "meeting_test", "requestedBy": fixture.user.Email, "agentName": "Colton",
		}}})
		if strings.Contains(instructions, "Lead with the outcome") || strings.Contains(instructions, "STRIDE private relationship context") {
			t.Fatalf("private import escaped into %s instructions: %q", origin, instructions)
		}
	}
	product, err := fixture.app.strideCoworkerProduct()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(product.collaborationRepo.path)
	if err != nil || !strings.Contains(string(raw), "New provider context merges later") {
		t.Fatalf("merged import was not durable: err=%v raw=%s", err, raw)
	}
}

func TestSTRIDEMemoryRuntimeProjectionIsQueryRelevantAndBounded(t *testing.T) {
	fixture := newSTRIDECoworkerTestFixture(t)
	fixture.runtime.mu.Lock()
	fixture.runtime.config.RelationshipMemoryEnabled = true
	fixture.runtime.mu.Unlock()
	mux := http.NewServeMux()
	registerSTRIDERuntimeRoutes(mux)
	entries := make([]strideRelationshipImportEntry, 0, 14)
	for index := 0; index < 12; index++ {
		entries = append(entries, strideRelationshipImportEntry{Category: "instructions", Date: "unknown", Value: fmt.Sprintf("Durable instruction %02d %s", index, strings.Repeat("detail ", 80))})
	}
	entries = append(entries,
		strideRelationshipImportEntry{Category: "projects", Date: "2026-08-05", Value: "Orchid launch research evidence"},
		strideRelationshipImportEntry{Category: "projects", Date: "2026-08-05", Value: "Saturn pricing analysis"},
	)
	body, _ := json.Marshal(map[string]any{"action": "import", "expectedRevision": 0, "entries": entries})
	req := httptest.NewRequest(http.MethodPost, strideRuntimeAPIBase+"coworker/relationships/import", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	for _, cookie := range fixture.cookies {
		req.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("import status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var imported strideMemoryImportTestResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &imported); err != nil {
		t.Fatal(err)
	}
	orchid := strideCoworkerPreferenceModelDataForQuery(imported.Preferences, "What is the latest Orchid launch evidence?")
	if !strings.Contains(orchid, "Orchid launch") || strings.Contains(orchid, "Saturn pricing") || len(orchid) > strideCoworkerModelPreferenceMaxBytes {
		t.Fatalf("Orchid projection was not relevant/bounded (%d bytes): %s", len(orchid), orchid)
	}
	var selected []map[string]any
	if err := json.Unmarshal([]byte(orchid), &selected); err != nil || len(selected) > strideCoworkerModelPreferenceMaxEntries {
		t.Fatalf("bounded projection decode=%v count=%d", err, len(selected))
	}
	instructions := 0
	for _, item := range selected {
		if strings.HasPrefix(fmt.Sprint(item["type"]), "user_instruction_") {
			instructions++
		}
	}
	if instructions > strideCoworkerModelInstructionMaxEntries {
		t.Fatalf("instruction projection count=%d", instructions)
	}
	saturn := strideCoworkerPreferenceModelDataForQuery(imported.Preferences, "Review Saturn pricing")
	if !strings.Contains(saturn, "Saturn pricing") || strings.Contains(saturn, "Orchid launch") {
		t.Fatalf("later turn did not retrieve a different durable slice: %s", saturn)
	}
}
