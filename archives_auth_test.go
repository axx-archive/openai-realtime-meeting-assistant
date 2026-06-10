package main

import (
	"net/http"
	"net/http/httptest"
	"os"
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
		{"/archives/meeting-test.json?key=" + archiveAccessToken("meeting-test"), http.StatusOK},
		// the room password must NOT open archives: accepting it made
		// /archives/ an unauthenticated password-guessing oracle.
		{"/archives/meeting-test.json?key=test-secret", http.StatusUnauthorized},
		{"/archives/other.json?key=" + archiveAccessToken("meeting-test"), http.StatusUnauthorized},
		{"/archives/?key=test-secret", http.StatusUnauthorized},
		{"/archives/", http.StatusUnauthorized},
	} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, testCase.url, nil)
		meetingArchiveHandler(recorder, request)
		if recorder.Code != testCase.status {
			t.Fatalf("GET %s status=%d, want %d", testCase.url, recorder.Code, testCase.status)
		}
	}

	keyedURL := meetingArchiveDownloadURLWithKey("meeting-test")
	if want := "/archives/meeting-test.json?key=" + archiveAccessToken("meeting-test"); keyedURL != want {
		t.Fatalf("keyed download url=%q, want %q", keyedURL, want)
	}
	if strings.Contains(keyedURL, "test-secret") {
		t.Fatalf("keyed download url=%q must not embed the room password", keyedURL)
	}
}

func TestArchiveTokenKeyedOnServerSecretNotPassword(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))
	t.Setenv("MEETING_ROOM_PASSWORD", "first-password")

	token := archiveAccessToken("meeting-test")
	secretPath := filepath.Join(filepath.Dir(meetingMemoryPath()), "archive-secret")
	info, err := os.Stat(secretPath)
	if err != nil {
		t.Fatalf("archive secret not persisted: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("archive secret mode=%v, want 0600", perm)
	}

	// rotating the room password must not change issued tokens: the token is
	// keyed on the server secret, not the credential it protects.
	t.Setenv("MEETING_ROOM_PASSWORD", "second-password")
	if got := archiveAccessToken("meeting-test"); got != token {
		t.Fatalf("token changed with the room password: %q vs %q", got, token)
	}
	if !validArchiveKey("meeting-test", token) {
		t.Fatal("issued token should stay valid")
	}
	if validArchiveKey("meeting-test", "first-password") || validArchiveKey("meeting-test", "second-password") {
		t.Fatal("room password must never grant archive access")
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
		got := entry.Metadata["downloadUrl"]
		if want := "/archives/meeting-test.json?key=" + archiveAccessToken("meeting-test"); got != want {
			t.Fatalf("client downloadUrl=%q, want %q", got, want)
		}
		if strings.Contains(got, "test-secret") {
			t.Fatalf("client downloadUrl=%q must not embed the room password", got)
		}
	}
	if !found {
		t.Fatal("archive entry missing from client memory snapshot")
	}
}
