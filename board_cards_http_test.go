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

func TestAssistantBoardCardsCRUDAndUndoForSignedInMember(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })
	cookies := loginAs(t, "tim@shareability.com", "B0NFIRE!")

	unauthorized := boardCardRequest(t, http.MethodPost, "/assistant/board/cards", `{"title":"Nope"}`, nil)
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("signed-out create status=%d, want 401", unauthorized.Code)
	}

	created := boardCardRequest(t, http.MethodPost, "/assistant/board/cards", `{"title":"Native card","status":"Backlog","owner":"Tim","notes":"Created on iPhone"}`, cookies)
	if created.Code != http.StatusOK {
		t.Fatalf("create status=%d body=%s", created.Code, created.Body.String())
	}
	cards := kanbanApp.snapshotState().Cards
	var cardID string
	for _, card := range cards {
		if card.Title == "Native card" {
			cardID = card.ID
			if card.Draft {
				t.Fatal("manual native card must not be a draft")
			}
		}
	}
	if cardID == "" {
		t.Fatalf("create snapshot=%+v", cards)
	}

	updated := boardCardRequest(t, http.MethodPut, "/assistant/board/cards/"+cardID, `{"title":"Native card edited","status":"In progress","owner":"Tim","notes":"Edited on iPhone","tags":["mobile"]}`, cookies)
	if updated.Code != http.StatusOK {
		t.Fatalf("update status=%d body=%s", updated.Code, updated.Body.String())
	}
	cards = kanbanApp.snapshotState().Cards
	foundUpdated := false
	for _, card := range cards {
		if card.ID == cardID && card.Title == "Native card edited" && card.Status == kanbanStatusInProgress {
			foundUpdated = true
		}
	}
	if !foundUpdated {
		t.Fatalf("update snapshot=%+v", cards)
	}

	deleted := boardCardRequest(t, http.MethodDelete, "/assistant/board/cards/"+cardID, "", cookies)
	if deleted.Code != http.StatusOK {
		t.Fatalf("delete status=%d body=%s cards=%+v", deleted.Code, deleted.Body.String(), kanbanApp.snapshotState().Cards)
	}
	for _, card := range kanbanApp.snapshotState().Cards {
		if card.ID == cardID {
			t.Fatalf("deleted card still present: %+v", card)
		}
	}

	restored := boardCardRequest(t, http.MethodPost, "/assistant/board/cards/undo", "", cookies)
	if restored.Code != http.StatusOK {
		t.Fatalf("undo status=%d body=%s cards=%+v", restored.Code, restored.Body.String(), kanbanApp.snapshotState().Cards)
	}
	foundRestored := false
	for _, card := range kanbanApp.snapshotState().Cards {
		if card.ID == cardID {
			foundRestored = true
		}
	}
	if !foundRestored {
		t.Fatalf("restored card missing: %+v", kanbanApp.snapshotState().Cards)
	}
}
