package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
)

func TestResetRequestRefusesUntrustedHostForLinks(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("BONFIRE_PUBLIC_URL", "")
	sent := captureAccountEmails(t)

	// httptest requests carry Host "example.com" — an untrusted, non-loopback
	// host must never become a reset link.
	recorder := postAuthJSON(t, "/auth/reset/request", `{"email":"aj@shareability.com"}`, nil)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", recorder.Code)
	}
	if len(*sent) != 0 {
		t.Fatalf("expected no email when the link base cannot be trusted, got %d", len(*sent))
	}
}

func newAuthGet(t *testing.T, path string, cookies []*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	authHandler(recorder, req)
	return recorder
}

func captureAccountEmails(t *testing.T) *[]sentAccountEmail {
	t.Helper()
	var sent []sentAccountEmail
	previous := sendAccountEmail
	sendAccountEmail = func(to, subject, html string) error {
		sent = append(sent, sentAccountEmail{To: to, Subject: subject, HTML: html})
		return nil
	}
	t.Cleanup(func() { sendAccountEmail = previous })
	return &sent
}

func TestResetRequestSendsEmailWithToken(t *testing.T) {
	setupAuthTestEnv(t)
	sent := captureAccountEmails(t)

	recorder := postAuthJSON(t, "/auth/reset/request", `{"email":"joel@shareability.com"}`, nil)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body %s", recorder.Code, recorder.Body.String())
	}
	if len(*sent) != 1 {
		t.Fatalf("expected one reset email, got %d", len(*sent))
	}
	email := (*sent)[0]
	if email.To != "joel@shareability.com" {
		t.Errorf("unexpected recipient %q", email.To)
	}

	tokenPattern := regexp.MustCompile(`reset=([0-9a-f]{64})`)
	match := tokenPattern.FindStringSubmatch(email.HTML)
	if match == nil {
		t.Fatalf("expected reset link in email body, got: %s", email.HTML)
	}
	if resolved, ok := accountStore().consumePasswordResetToken(match[1]); !ok || resolved != "joel@shareability.com" {
		t.Fatalf("expected emailed token to resolve to joel, got %q ok=%v", resolved, ok)
	}
}

func TestResetRequestUnknownEmailSendsNothing(t *testing.T) {
	setupAuthTestEnv(t)
	sent := captureAccountEmails(t)

	recorder := postAuthJSON(t, "/auth/reset/request", `{"email":"stranger@evil.com"}`, nil)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("expected 202 (no account enumeration), got %d", recorder.Code)
	}
	if len(*sent) != 0 {
		t.Fatalf("expected no email for unknown account, got %d", len(*sent))
	}
}

func TestResetConfirmRotatesPasswordAndSessions(t *testing.T) {
	setupAuthTestEnv(t)
	sent := captureAccountEmails(t)

	cookies := loginAs(t, "tyler@shareability.com", "B0NFIRE!")

	recorder := postAuthJSON(t, "/auth/reset/request", `{"email":"tyler@shareability.com"}`, nil)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("reset request failed: %d", recorder.Code)
	}
	token := regexp.MustCompile(`reset=([0-9a-f]{64})`).FindStringSubmatch((*sent)[0].HTML)[1]

	recorder = postAuthJSON(t, "/auth/reset/confirm", fmt.Sprintf(`{"token":%q,"newPassword":"after-reset-1"}`, token), nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected confirm 200, got %d body %s", recorder.Code, recorder.Body.String())
	}

	if _, ok := accountStore().authenticate("tyler@shareability.com", "after-reset-1"); !ok {
		t.Error("expected new password to authenticate")
	}
	if _, ok := accountStore().authenticate("tyler@shareability.com", "B0NFIRE!"); ok {
		t.Error("expected old password to stop working")
	}

	// The pre-reset session must be revoked.
	req := newAuthGet(t, "/auth/me", cookies)
	if req.Code != http.StatusUnauthorized {
		t.Errorf("expected pre-reset session to be revoked, got %d", req.Code)
	}

	// Token is single-use.
	recorder = postAuthJSON(t, "/auth/reset/confirm", fmt.Sprintf(`{"token":%q,"newPassword":"another-pass-2"}`, token), nil)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected reused token to be rejected, got %d", recorder.Code)
	}
}
