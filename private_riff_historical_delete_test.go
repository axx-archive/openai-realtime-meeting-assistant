package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestHistoricalPrivateRiffHTTPDeletePreservesClosedEpisodeAndTombstones(t *testing.T) {
	previous := kanbanApp
	app := newIsolatedKanbanBoardApp(t)
	kanbanApp = app
	t.Cleanup(func() { kanbanApp = previous })
	user := &userAccount{Email: "aj@shareability.com", Name: "AJ"}
	cookies := loginAs(t, user.Email, "B0NFIRE!")
	source := seedPrivateRiffChannel(t, app, user.Email, 3)
	first, _, err := app.createPrivateRiffWithEntryPoint(user, source.ID, "riff-source-02", "", "delete-old-first", privateRiffEntryPointMessage, "")
	if err != nil {
		t.Fatal(err)
	}
	first = commitRiffSpaceConversation(t, app, user, first, "HISTORICALDELETECANARY")
	oldEpisode := first.Riff.ActiveEpisodeID
	second, _, err := app.createPrivateRiffWithEntryPoint(user, source.ID, "riff-source-03", "", "delete-old-second", privateRiffEntryPointMessage, "")
	if err != nil {
		t.Fatal(err)
	}
	second = commitRiffSpaceConversation(t, app, user, second, "current-preserve")
	if second.Riff.ActiveEpisodeID == oldEpisode || second.Riff.EpisodeRecords[privateRiffEpisodeIndex(second.Riff, oldEpisode)].Status != privateRiffEpisodeClosed {
		t.Fatal("fixture must have closed earlier episode")
	}
	beforeBinding, _ := json.Marshal(second.Riff)
	messageID := "HISTORICALDELETECANARY-user"
	beforeRows := app.channelIngestionRows(second.ID, messageID)
	if len(beforeRows) != 1 {
		t.Fatalf("want exact ingested source, got %d", len(beforeRows))
	}
	call := func(id string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodDelete, "/assistant/chat-threads/"+second.ID+"/messages/"+id, nil)
		for _, c := range cookies {
			r.AddCookie(c)
		}
		w := httptest.NewRecorder()
		assistantChatThreadHandler(w, r)
		return w
	}
	if w := call("HISTORICALDELETECANARY-scout"); w.Code == http.StatusOK {
		t.Fatal("owner cannot delete another author through author-only endpoint")
	}
	if w := call(messageID); w.Code != http.StatusOK {
		t.Fatalf("delete HTTP %d: %s", w.Code, w.Body.String())
	}
	after, _, err := app.scoutChatThreadByID(user.Email, second.ID)
	if err != nil {
		t.Fatal(err)
	}
	afterBinding, _ := json.Marshal(after.Riff)
	if string(beforeBinding) != string(afterBinding) {
		t.Fatal("deletion changed episode lifecycle/checkpoints")
	}
	expected := make([]scoutChatMessageRecord, 0, len(second.Messages)-1)
	for _, m := range second.Messages {
		if m.ID != messageID {
			expected = append(expected, m)
		}
	}
	if !reflect.DeepEqual(after.Messages, expected) {
		t.Fatal("deletion changed unrelated messages")
	}
	row, ok := app.memory.entryByKindAndID(meetingMemoryKindTranscript, beforeRows[0].ID)
	if !ok || !memoryEntryHiddenFromRecall(row) || row.Metadata["tombstone"] != "true" || row.Metadata["correctionState"] != "deleted" || strings.Contains(row.Text, "HISTORICALDELETECANARY") {
		t.Fatal("historical message source not tombstoned")
	}
	if len(app.channelIngestionRows(second.ID, messageID)) != 0 {
		t.Fatal("deleted source remains recallable")
	}
	prior := app.projectScoutChatThreadForViewerEpisode(user.Email, after, oldEpisode)
	if prior.Riff.ViewedEpisodeID != oldEpisode || prior.Riff.ActiveEpisodeID != second.Riff.ActiveEpisodeID || len(prior.Messages) != 1 {
		t.Fatal("prior pass cannot be read without resuming after deletion")
	}
}
