package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLegacyMeetingSpecialistCustomerMutationsRetiredByDefault(t *testing.T) {
	t.Setenv(legacyMeetingSpecialistCustomerMutationsEnvironment, "")
	mux := http.NewServeMux()
	registerMeetingSpecialistProductRoutes(mux)

	for _, path := range []string{
		"/api/stride/v1/meeting-specialists/recommendations",
		"/api/stride/v1/meeting-specialists/invitations",
		"/api/stride/v1/meeting-specialists/invitations/legacy-invitation",
	} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
		mux.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusGone || !strings.Contains(recorder.Body.String(), "retired") {
			t.Fatalf("POST %s status=%d body=%s", path, recorder.Code, recorder.Body.String())
		}
	}
}

func TestLegacyMeetingSpecialistAdminCompatibilityGateIsExplicit(t *testing.T) {
	t.Setenv(legacyMeetingSpecialistCustomerMutationsEnvironment, "true")
	mux := http.NewServeMux()
	registerMeetingSpecialistProductRoutes(mux)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/stride/v1/meeting-specialists/invitations", strings.NewReader(`{}`))
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("explicit compatibility gate did not reach normal authorization: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
