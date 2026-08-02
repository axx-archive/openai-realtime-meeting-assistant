package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func withSTRIDERuntimeHTTPTestApp(t *testing.T, runtime *STRIDERuntime) *http.ServeMux {
	t.Helper()
	previous := kanbanApp
	kanbanApp = &kanbanBoardApp{strideRuntime: runtime}
	t.Cleanup(func() {
		kanbanApp = previous
		if runtime != nil {
			_ = runtime.Close()
		}
	})
	mux := http.NewServeMux()
	registerSTRIDERuntimeRoutes(mux)
	return mux
}

func strideRuntimeHTTPGet(t *testing.T, mux *http.ServeMux, path string, cookies []*http.Cookie) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, req)
	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %s response (status=%d body=%q): %v", path, recorder.Code, recorder.Body.String(), err)
	}
	return recorder.Code, body
}

func TestSTRIDERuntimeHTTPRoutesRequireAuthentication(t *testing.T) {
	setupAuthTestEnv(t)
	runtime, err := NewSTRIDERuntime(STRIDERuntimeConfig{})
	if err != nil {
		t.Fatal(err)
	}
	mux := withSTRIDERuntimeHTTPTestApp(t, runtime)
	for _, path := range []string{
		"/api/stride/v1/status",
		"/api/stride/v1/marketplace",
		"/api/stride/v1/roster",
		"/api/stride/v1/work",
		"/api/stride/v1/temporal",
	} {
		status, _ := strideRuntimeHTTPGet(t, mux, path, nil)
		if status != http.StatusUnauthorized {
			t.Fatalf("%s status=%d, want %d", path, status, http.StatusUnauthorized)
		}
	}
}

func TestSTRIDERuntimeHTTPDisabledShapesAreStableAndEmpty(t *testing.T) {
	setupAuthTestEnv(t)
	cookies := loginAs(t, "aj@shareability.com", "B0NFIRE!")
	runtime, err := NewSTRIDERuntime(STRIDERuntimeConfig{})
	if err != nil {
		t.Fatal(err)
	}
	mux := withSTRIDERuntimeHTTPTestApp(t, runtime)

	status, body := strideRuntimeHTTPGet(t, mux, "/api/stride/v1/status?tenantId=other_tenant", cookies)
	if status != http.StatusForbidden || body["error"] != "tenant scope is server-derived" {
		t.Fatalf("tenant override status=%d body=%+v", status, body)
	}
	status, body = strideRuntimeHTTPGet(t, mux, "/api/stride/v1/status", cookies)
	if status != http.StatusOK || body["ok"] != true {
		t.Fatalf("status response status=%d body=%+v", status, body)
	}
	runtimeBody, ok := body["runtime"].(map[string]any)
	if !ok || runtimeBody["state"] != string(STRIDERuntimeDisabled) || runtimeBody["configured"] != false || runtimeBody["activationFenced"] != true {
		t.Fatalf("disabled runtime body=%+v", runtimeBody)
	}
	if _, exposed := runtimeBody["tenantId"]; exposed {
		t.Fatalf("status exposed tenant selection: %+v", runtimeBody)
	}

	tests := []struct {
		path   string
		arrays []string
		reason string
	}{
		{path: "/api/stride/v1/marketplace", arrays: []string{"listings"}, reason: "runtime_disabled"},
		{path: "/api/stride/v1/roster", arrays: []string{"seats", "recommendations"}, reason: "runtime_disabled"},
		{path: "/api/stride/v1/work", arrays: []string{"suggestions", "runs"}, reason: "runtime_disabled"},
		{path: "/api/stride/v1/temporal", arrays: []string{"meetings"}},
	}
	for _, test := range tests {
		status, body := strideRuntimeHTTPGet(t, mux, test.path+"?tenantId=other_tenant", cookies)
		if status != http.StatusForbidden || body["error"] != "tenant scope is server-derived" {
			t.Fatalf("%s tenant override status=%d body=%+v", test.path, status, body)
		}
		status, body = strideRuntimeHTTPGet(t, mux, test.path, cookies)
		if status != http.StatusOK || body["ok"] != true || body["available"] != false || test.reason != "" && body["reason"] != test.reason {
			t.Fatalf("%s status=%d body=%+v", test.path, status, body)
		}
		for _, key := range test.arrays {
			values, ok := body[key].([]any)
			if !ok || len(values) != 0 {
				t.Fatalf("%s %s=%#v, want empty array", test.path, key, body[key])
			}
		}
	}
}

func TestSTRIDERuntimeHTTPStandbyStillReportsFeaturesFenced(t *testing.T) {
	setupAuthTestEnv(t)
	cookies := loginAs(t, "aj@shareability.com", "B0NFIRE!")
	runtime, err := NewSTRIDERuntime(strideIntegratedRuntimeConfig(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	mux := withSTRIDERuntimeHTTPTestApp(t, runtime)
	for _, path := range []string{
		"/api/stride/v1/marketplace",
		"/api/stride/v1/roster",
		"/api/stride/v1/work",
	} {
		status, body := strideRuntimeHTTPGet(t, mux, path, cookies)
		if status != http.StatusOK || body["available"] != false || body["reason"] != "product_preview_disabled" {
			t.Fatalf("%s status=%d body=%+v", path, status, body)
		}
	}
}

func TestSTRIDERuntimePublicCapabilitySnapshotNeverLeaksFailureDetails(t *testing.T) {
	runtime := &STRIDERuntime{
		config:    STRIDERuntimeConfig{Enabled: true, TenantID: "bonfire"},
		state:     STRIDERuntimeUnavailable,
		healthErr: errors.New("persist /private/runtime.snapshot with secret key material"),
	}
	snapshot := strideRuntimeCapabilitySnapshot(&kanbanBoardApp{strideRuntime: runtime})
	if _, exposed := snapshot["lastError"]; exposed {
		t.Fatalf("public capability snapshot exposed runtime error details: %+v", snapshot)
	}
	if snapshot["status"] != "degraded" || snapshot["state"] != STRIDERuntimeUnavailable {
		t.Fatalf("public capability snapshot lost typed degraded state: %+v", snapshot)
	}
}
