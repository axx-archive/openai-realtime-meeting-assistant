package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestMeetingArchiveHandlerRequiresRoomKey(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))
	t.Setenv("MEETING_ROOM_PASSWORD", "test-secret")

	archivePath, err := meetingArchivePath("meeting-test")
	if err != nil {
		t.Fatalf("meetingArchivePath: %v", err)
	}
	if err := writeMeetingArchive(archivePath, meetingArchive{ID: "meeting-test"}); err != nil {
		t.Fatalf("writeMeetingArchive: %v", err)
	}

	for _, testCase := range []struct {
		url    string
		status int
	}{
		{"/archives/meeting-test.json", http.StatusUnauthorized},
		{"/archives/meeting-test.json?key=wrong", http.StatusUnauthorized},
		{"/archives/meeting-test.json?key=test-secret", http.StatusOK},
		{"/archives/?key=test-secret", http.StatusNotFound},
		{"/archives/", http.StatusUnauthorized},
	} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, testCase.url, nil)
		meetingArchiveHandler(recorder, request)
		if recorder.Code != testCase.status {
			t.Fatalf("GET %s status=%d, want %d", testCase.url, recorder.Code, testCase.status)
		}
	}

	if got, want := meetingArchiveDownloadURLWithKey("meeting-test"), "/archives/meeting-test.json?key=test-secret"; got != want {
		t.Fatalf("keyed download url=%q, want %q", got, want)
	}
}

func TestMemorySnapshotForClientsAddsKeyedArchiveURL(t *testing.T) {
	t.Setenv("MEETING_ROOM_PASSWORD", "test-secret")
	app := newIsolatedKanbanBoardApp(t)

	if _, _, err := app.memory.appendArchive("meeting-test", "archived the meeting", map[string]string{
		"archiveId":   "meeting-test",
		"downloadUrl": meetingArchiveDownloadURL("meeting-test"),
	}); err != nil {
		t.Fatalf("appendArchive: %v", err)
	}

	for _, entry := range app.memorySnapshot(10) {
		if entry.Kind == meetingMemoryKindArchive && strings.Contains(entry.Metadata["downloadUrl"], "key=") {
			t.Fatalf("persisted downloadUrl=%q, must not embed the room key", entry.Metadata["downloadUrl"])
		}
	}

	found := false
	for _, entry := range app.memorySnapshotForClients(10) {
		if entry.Kind != meetingMemoryKindArchive {
			continue
		}
		found = true
		if got, want := entry.Metadata["downloadUrl"], "/archives/meeting-test.json?key=test-secret"; got != want {
			t.Fatalf("client downloadUrl=%q, want %q", got, want)
		}
	}
	if !found {
		t.Fatal("archive entry missing from client memory snapshot")
	}
}
