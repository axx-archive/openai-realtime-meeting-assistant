package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// An explicit BONFIRE_BUILD_VERSION is the deploy-time source of truth and must
// win over the derived hash.
func TestComputeBuildVersionEnvOverride(t *testing.T) {
	t.Setenv("BONFIRE_BUILD_VERSION", "deploy-42")
	if got := computeBuildVersion([]byte("<html>")); got != "deploy-42" {
		t.Fatalf("computeBuildVersion = %q, want deploy-42", got)
	}
}

// Without an override the version is derived from (binary identity + frontend
// bytes): stable for identical input, different when the frontend changes. This
// is what makes a plain restart quiet but a real deploy detectable.
func TestComputeBuildVersionTracksFrontend(t *testing.T) {
	t.Setenv("BONFIRE_BUILD_VERSION", "")

	a := computeBuildVersion([]byte("<html>a</html>"))
	again := computeBuildVersion([]byte("<html>a</html>"))
	b := computeBuildVersion([]byte("<html>b</html>"))

	if a == "" {
		t.Fatalf("derived build version is empty")
	}
	if a != again {
		t.Fatalf("derived build version unstable for identical input: %q vs %q", a, again)
	}
	if a == b {
		t.Fatalf("derived build version did not change with frontend bytes: %q", a)
	}
}

// The served HTML must carry the resolved build id, never the raw placeholder,
// so a booted tab knows exactly which frontend it is running.
func TestBuildVersionPlaceholderSubstitution(t *testing.T) {
	source := []byte(`<meta name="bonfire-build" content="` + buildVersionPlaceholder + `">`)
	version := "stamp-xyz"
	stamped := bytes.ReplaceAll(source, []byte(buildVersionPlaceholder), []byte(version))

	if bytes.Contains(stamped, []byte(buildVersionPlaceholder)) {
		t.Fatalf("placeholder survived substitution: %s", stamped)
	}
	if !bytes.Contains(stamped, []byte(version)) {
		t.Fatalf("stamped HTML missing version %q: %s", version, stamped)
	}
}

func TestHealthzReportsBuildVersion(t *testing.T) {
	prev := serverBuildVersion
	serverBuildVersion = "hz-build-1"
	t.Cleanup(func() { serverBuildVersion = prev })

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	recorder := httptest.NewRecorder()
	healthHandler(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("health status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode health payload: %v", err)
	}
	if payload["version"] != "hz-build-1" {
		t.Fatalf("health version=%v, want hz-build-1", payload["version"])
	}
}

// A room joiner (participant hello) must receive the running build id so a tab
// that reconnected across a deploy can detect the skew.
func TestWebsocketParticipantReceivesServerVersion(t *testing.T) {
	prev := serverBuildVersion
	serverBuildVersion = "participant-build"
	t.Cleanup(func() { serverBuildVersion = prev })

	conn := newIsolatedNativeWebsocket(t, "tom@shareability.com")
	writeNativeWebsocketEvent(t, conn, "participant", map[string]any{
		"client": map[string]string{"platform": "web"},
	})

	data := waitForKanbanEvent(t, conn, "server_version", 5*time.Second)
	var got string
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("decode server_version: %v", err)
	}
	if got != "participant-build" {
		t.Fatalf("server_version=%q, want participant-build", got)
	}
}

// The always-on office socket (signed in, out of room) is the one that catches
// a deploy while the tab sits idle in the lobby, so it must carry the id too.
func TestWebsocketOfficeReceivesServerVersion(t *testing.T) {
	prev := serverBuildVersion
	serverBuildVersion = "office-build"
	t.Cleanup(func() { serverBuildVersion = prev })

	conn := newIsolatedNativeWebsocket(t, "tom@shareability.com")
	writeNativeWebsocketEvent(t, conn, "office", map[string]any{})

	data := waitForKanbanEvent(t, conn, "server_version", 5*time.Second)
	var got string
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("decode server_version: %v", err)
	}
	if got != "office-build" {
		t.Fatalf("server_version=%q, want office-build", got)
	}
}
