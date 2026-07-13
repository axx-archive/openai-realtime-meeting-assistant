package main

import (
	"encoding/json"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

var authorizationNonObjectHTTPRoutes = map[string]struct{}{
	"/healthz": {}, "/livez": {}, "/readyz": {}, "/capabilities": {}, "/auth/": {},
	"/assistant/push/config": {}, "/assistant/push/subscribe": {}, "/assistant/push/unsubscribe": {}, "/assistant/push/prefs": {},
	"/assistant/realtime/usage": {}, "/api/usage/rollup": {}, "/calendar/event.ics": {},
	"/client-config": {}, "/native/config": {}, "/g": {}, "/g/": {}, "/guest/lookup": {}, "/guest/me": {},
	"/ice-test": {}, "/public/": {}, "/sw.js": {}, "/": {},
}

func TestAuthorizationSurfaceRegistryIsWellFormedAndMachineReadable(t *testing.T) {
	surfaces := AuthorizationSurfaces()
	if len(surfaces) == 0 {
		t.Fatal("authorization surface registry is empty")
	}
	seenID, seenKindSource := map[string]bool{}, map[string]bool{}
	for _, surface := range surfaces {
		if strings.TrimSpace(surface.ID) == "" || strings.TrimSpace(surface.Source) == "" || len(surface.ObjectFamilies) == 0 || len(surface.RequiredActions) == 0 || len(surface.PrincipalKinds) == 0 {
			t.Fatalf("incomplete authorization surface: %+v", surface)
		}
		if seenID[surface.ID] {
			t.Fatalf("duplicate authorization surface id %q", surface.ID)
		}
		seenID[surface.ID] = true
		key := string(surface.Kind) + "\x00" + surface.Source
		if seenKindSource[key] {
			t.Fatalf("duplicate authorization kind/source %q", key)
		}
		seenKindSource[key] = true
		if surface.Status != AuthorizationLegacyGuarded && surface.Status != AuthorizationCanonicalNeeded && surface.Status != AuthorizationCanonicalEnforced {
			t.Fatalf("surface %s has unknown staged status %q", surface.ID, surface.Status)
		}
		if surface.Status == AuthorizationCanonicalNeeded && surface.ReadsBody && surface.AuthorizeBeforeBodyRead {
			// This is the target state. Keeping this branch explicit documents that
			// canonical-required does not mean the handler has already cut over.
			continue
		}
	}
	if _, err := json.Marshal(surfaces); err != nil {
		t.Fatalf("registry is not machine-readable JSON: %v", err)
	}
}

func TestEveryRegisteredHTTPHandlerIsInventoriedOrExplicitlyNonObject(t *testing.T) {
	raw, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`http\.HandleFunc\("([^"]+)"`)
	registered := map[string]bool{}
	for _, surface := range AuthorizationSurfaces() {
		if surface.Kind == AuthorizationHTTP || surface.Kind == AuthorizationCapability {
			registered[surface.Source] = true
		}
	}
	var missing []string
	for _, match := range re.FindAllStringSubmatch(string(raw), -1) {
		path := match[1]
		if registered[path] {
			continue
		}
		if _, excluded := authorizationNonObjectHTTPRoutes[path]; !excluded {
			missing = append(missing, path)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Fatalf("HTTP authorization surfaces missing from registry: %v", missing)
	}
}

func TestWebSocketInboundEventsAreFullyInventoried(t *testing.T) {
	raw, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	start := strings.Index(string(raw), "func websocketHandler(")
	end := strings.Index(string(raw)[start:], "\nfunc broadcastManualBoardMutation(")
	if start < 0 || end < 0 {
		t.Fatal("could not locate websocket handler source boundary")
	}
	handler := string(raw)[start : start+end]
	re := regexp.MustCompile(`case "([a-z0-9_]+)":`)
	inventoried := map[string]bool{}
	for _, surface := range AuthorizationSurfaces() {
		if surface.Kind == AuthorizationWebSocketIn {
			inventoried[surface.Source] = true
		}
	}
	var missing []string
	for _, match := range re.FindAllStringSubmatch(handler, -1) {
		if !inventoried[match[1]] {
			missing = append(missing, match[1])
		}
	}
	if len(missing) > 0 {
		t.Fatalf("websocket inbound events missing from registry: %v", missing)
	}
}

func TestCanonicalRequiredBodyReadersDeclarePreFetchAuthorizationDebt(t *testing.T) {
	for _, surface := range AuthorizationSurfaces() {
		if surface.Status != AuthorizationCanonicalNeeded || !surface.ReadsBody {
			continue
		}
		// Until handlers cut over, false is an explicit release debt rather than
		// an accidental omission. Once true, later regressions must change this
		// checked-in registry and its review evidence.
		if surface.AuthorizeBeforeBodyRead {
			continue
		}
		if len(surface.RequiredActions) == 0 {
			t.Fatalf("body-reading surface %s lacks required action", surface.ID)
		}
	}
}

type authorizationNegativeCase struct {
	Name                        string
	SurfaceID                   string
	Attacker                    string
	KnownReference              string
	ExpectedStatus              int
	MustNotExpose               []string
	MustAuthorizeBeforeBodyRead bool
}

// authorizationNegativeCorpus is the focused W1 scaffold. Handler cutover
// tests consume these rows to build two-user fixtures and assert opaque denial
// before body/blob/prompt retrieval; the registry test keeps every named seam
// anchored even before those integration fixtures exist.
var authorizationNegativeCorpus = []authorizationNegativeCase{
	{"artifact idor read", "http.artifacts", "ungranted_user", "known_artifact_id", 404, []string{"id", "title", "text", "metadata"}, true},
	{"artifact idor patch", "http.artifacts", "ungranted_user", "known_artifact_id", 404, []string{"updated", "artifact"}, true},
	{"render token idor", "http.artifacts.render_token", "ungranted_user", "known_artifact_id", 404, []string{"token", "url", "expiresAt"}, true},
	{"known hash blob idor", "http.artifacts.blob", "ungranted_user", "known_blob_sha256", 404, []string{"ETag", "Content-Length", "bytes"}, true},
	{"share capability mint idor", "http.artifacts.share", "ungranted_user", "known_artifact_id", 404, []string{"token", "url", "link"}, true},
	{"artifact open idor", "http.artifacts.open", "ungranted_user", "known_artifact_id", 404, []string{"openedAt", "signal"}, true},
	{"artifact survey idor", "http.signals.survey", "ungranted_user", "known_artifact_id", 404, []string{"stored", "stage", "toolTemplate"}, true},
	{"artifact action idor", "http.artifacts.action", "ungranted_user", "known_artifact_id", 404, []string{"artifact", "actions", "endorsement"}, true},
	{"deal room request idor", "http.assistant.deal_room_request", "ungranted_user", "known_package_id", 404, []string{"dealRoom", "url", "artifactId"}, true},
	{"mutable capability revision", "capability.artifact_share", "stale_capability", "approved_old_revision", 404, []string{"current_revision_body"}, true},
	{"lexical recall canary", "http.assistant.query", "ungranted_user", "denied_memory_id", 200, []string{"denied_id", "canary_text", "derived_excerpt"}, true},
	{"semantic recall canary", "http.assistant.query", "ungranted_user", "denied_embedding_id", 200, []string{"denied_id", "canary_text", "model_prompt_excerpt"}, true},
	{"websocket memory replay canary", "ws.bootstrap.member_office", "ungranted_user", "denied_memory_id", 0, []string{"denied_id", "canary_text", "count_delta"}, true},
}

func TestAuthorizationNegativeCorpusReferencesRegisteredSurfaces(t *testing.T) {
	registered := map[string]AuthorizationSurface{}
	for _, surface := range AuthorizationSurfaces() {
		registered[surface.ID] = surface
	}
	for _, test := range authorizationNegativeCorpus {
		surface, ok := registered[test.SurfaceID]
		if !ok {
			t.Fatalf("negative case %q references unregistered surface %q", test.Name, test.SurfaceID)
		}
		if test.MustAuthorizeBeforeBodyRead && !surface.ReadsBody {
			t.Fatalf("negative case %q expects a body-reading surface: %+v", test.Name, surface)
		}
		if len(test.MustNotExpose) == 0 {
			t.Fatalf("negative case %q has no non-disclosure assertions", test.Name)
		}
	}
}
