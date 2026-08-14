package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func boardCardRequest(t *testing.T, method string, path string, body string, cookies []*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	assistantBoardCardsHandler(recorder, req)
	return recorder
}

func TestAssistantBoardCardsMutationsAreRetiredWithoutDeletingHistory(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })
	cookies := loginAs(t, "tim@shareability.com", "B0NFIRE!")

	unauthorized := boardCardRequest(t, http.MethodPost, "/assistant/board/cards", `{"title":"Nope"}`, nil)
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("signed-out create status=%d, want 401", unauthorized.Code)
	}
	before := kanbanApp.snapshotState()
	requests := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodPost, "/assistant/board/cards", `{"title":"Must not exist"}`},
		{http.MethodPut, "/assistant/board/cards/legacy-card", `{"title":"Must not change"}`},
		{http.MethodDelete, "/assistant/board/cards/legacy-card", ""},
		{http.MethodPost, "/assistant/board/cards/undo", ""},
	}
	for _, request := range requests {
		response := boardCardRequest(t, request.method, request.path, request.body, cookies)
		if response.Code != http.StatusGone || !strings.Contains(response.Body.String(), "Board is retired") {
			t.Fatalf("%s %s status=%d body=%s", request.method, request.path, response.Code, response.Body.String())
		}
	}
	after := kanbanApp.snapshotState()
	if boardSnapshotDigest(before) != boardSnapshotDigest(after) {
		t.Fatalf("retired Board mutation changed history: before=%+v after=%+v", before, after)
	}
}

func boardSnapshotDigest(value any) string {
	raw, _ := canonicalJSON(value)
	return temporalDigest(string(raw))
}
