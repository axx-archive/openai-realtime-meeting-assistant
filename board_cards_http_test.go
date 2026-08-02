package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
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
	records, err := boardLifecycleCommittedRecords(filepath.Join(filepath.Dir(meetingMemoryPath()), "deleted-objects.jsonl"))
	if err != nil || len(records) != 1 ||
		records[0].Phase != canonicalLifecyclePhaseCommitted ||
		records[0].OperationID == "" ||
		records[0].Family != "board_card" || records[0].ObjectID != cardID ||
		records[0].Reason != "board_card_deleted" || !isHexDigest(records[0].StateDigest) {
		t.Fatalf("delete lifecycle journal=%+v err=%v", records, err)
	}

	restored := boardCardRequest(t, http.MethodPost, "/assistant/board/cards/undo", "", cookies)
	if restored.Code != http.StatusOK {
		t.Fatalf("undo status=%d body=%s cards=%+v", restored.Code, restored.Body.String(), kanbanApp.snapshotState().Cards)
	}
	foundRestored := false
	firstRestoredAt := ""
	for _, card := range kanbanApp.snapshotState().Cards {
		if card.ID == cardID {
			foundRestored = true
			if strings.TrimSpace(card.RestoredAt) == "" {
				t.Fatal("restored card is missing its canonical lifecycle generation")
			}
			firstRestoredAt = card.RestoredAt
		}
	}
	if !foundRestored {
		t.Fatalf("restored card missing: %+v", kanbanApp.snapshotState().Cards)
	}

	revised := boardCardRequest(t, http.MethodPut, "/assistant/board/cards/"+cardID, `{"title":"Native card restored and revised","status":"In progress","owner":"Tim","notes":"Second lifecycle","tags":["mobile"]}`, cookies)
	if revised.Code != http.StatusOK {
		t.Fatalf("post-undo update status=%d body=%s", revised.Code, revised.Body.String())
	}
	deletedAgain := boardCardRequest(t, http.MethodDelete, "/assistant/board/cards/"+cardID, "", cookies)
	if deletedAgain.Code != http.StatusOK {
		t.Fatalf("second delete status=%d body=%s", deletedAgain.Code, deletedAgain.Body.String())
	}
	records, err = boardLifecycleCommittedRecords(filepath.Join(filepath.Dir(meetingMemoryPath()), "deleted-objects.jsonl"))
	if err != nil || len(records) != 2 ||
		records[0].Phase != canonicalLifecyclePhaseCommitted ||
		records[1].Phase != canonicalLifecyclePhaseCommitted ||
		records[0].StateDigest == records[1].StateDigest ||
		!records[1].At.After(records[0].At) {
		t.Fatalf("second delete lifecycle generations=%+v err=%v", records, err)
	}
	restoredAgain := boardCardRequest(t, http.MethodPost, "/assistant/board/cards/undo", "", cookies)
	if restoredAgain.Code != http.StatusOK {
		t.Fatalf("second undo status=%d body=%s", restoredAgain.Code, restoredAgain.Body.String())
	}
	foundRestoredAgain := false
	for _, card := range kanbanApp.snapshotState().Cards {
		if card.ID == cardID {
			foundRestoredAgain = true
			if card.RestoredAt == firstRestoredAt {
				t.Fatalf("second undo reused lifecycle generation %q", card.RestoredAt)
			}
		}
	}
	if !foundRestoredAgain {
		t.Fatal("second undo did not restore the same card identity")
	}
}
