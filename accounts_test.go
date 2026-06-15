package main

import (
	"path/filepath"
	"testing"
	"time"
)

func newTestUserStore(t *testing.T) *userAccountStore {
	t.Helper()
	store, err := newUserAccountStore(filepath.Join(t.TempDir(), "users.json"))
	if err != nil {
		t.Fatalf("newUserAccountStore: %v", err)
	}
	return store
}

func TestUserStoreSeedsAllAccounts(t *testing.T) {
	store := newTestUserStore(t)

	emails := store.accountEmails()
	if len(emails) != 7 {
		t.Fatalf("expected 7 seeded accounts, got %d: %v", len(emails), emails)
	}

	expected := map[string]string{
		"aj@shareability.com":      "AJ",
		"tim@shareability.com":     "Tim",
		"e@shareability.com":       "Erick",
		"joel@shareability.com":    "Joel",
		"tyler@shareability.com":   "Tyler",
		"caitlyn@shareability.com": "Caitlyn",
		"tom@shareability.com":     "Tom",
	}
	for email, name := range expected {
		user := store.findUser(email)
		if user == nil {
			t.Fatalf("expected seeded account for %s", email)
		}
		if user.Name != name {
			t.Errorf("expected %s to map to name %q, got %q", email, name, user.Name)
		}
	}
}

func TestAuthenticateUser(t *testing.T) {
	store := newTestUserStore(t)

	user, ok := store.authenticate("aj@shareability.com", "B0NFIRE!")
	if !ok || user == nil {
		t.Fatal("expected seeded password to authenticate")
	}
	if user.Name != "AJ" {
		t.Errorf("expected name AJ, got %q", user.Name)
	}

	if _, ok := store.authenticate("AJ@Shareability.com ", "B0NFIRE!"); !ok {
		t.Error("expected email match to be case-insensitive and trimmed")
	}
	if _, ok := store.authenticate("aj@shareability.com", "wrong"); ok {
		t.Error("expected wrong password to fail")
	}
	if _, ok := store.authenticate("nobody@shareability.com", "B0NFIRE!"); ok {
		t.Error("expected unknown email to fail")
	}
}

func TestChangeUserPassword(t *testing.T) {
	store := newTestUserStore(t)

	if err := store.changePassword("tim@shareability.com", "wrong", "newpassword1"); err == nil {
		t.Fatal("expected wrong current password to be rejected")
	}
	if err := store.changePassword("tim@shareability.com", "B0NFIRE!", "short"); err == nil {
		t.Fatal("expected too-short new password to be rejected")
	}
	if err := store.changePassword("tim@shareability.com", "B0NFIRE!", "newpassword1"); err != nil {
		t.Fatalf("expected password change to succeed: %v", err)
	}
	if _, ok := store.authenticate("tim@shareability.com", "B0NFIRE!"); ok {
		t.Error("expected old password to stop working")
	}
	if _, ok := store.authenticate("tim@shareability.com", "newpassword1"); !ok {
		t.Error("expected new password to authenticate")
	}
}

func TestUserStorePersistsAcrossReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "users.json")
	store, err := newUserAccountStore(path)
	if err != nil {
		t.Fatalf("newUserAccountStore: %v", err)
	}
	if err := store.changePassword("joel@shareability.com", "B0NFIRE!", "rotated-pass-9"); err != nil {
		t.Fatalf("changePassword: %v", err)
	}

	reloaded, err := newUserAccountStore(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if _, ok := reloaded.authenticate("joel@shareability.com", "rotated-pass-9"); !ok {
		t.Error("expected changed password to survive reload (seeding must be idempotent)")
	}
	if emails := reloaded.accountEmails(); len(emails) != len(seededAccounts) {
		t.Errorf("expected reload to keep %d accounts, got %d", len(seededAccounts), len(emails))
	}
}

func TestPasswordResetTokenFlow(t *testing.T) {
	store := newTestUserStore(t)

	if _, err := store.createPasswordResetToken("nobody@shareability.com"); err == nil {
		t.Fatal("expected reset token for unknown account to fail")
	}

	token, err := store.createPasswordResetToken("caitlyn@shareability.com")
	if err != nil {
		t.Fatalf("createPasswordResetToken: %v", err)
	}
	if token == "" {
		t.Fatal("expected non-empty token")
	}

	email, ok := store.consumePasswordResetToken(token)
	if !ok || email != "caitlyn@shareability.com" {
		t.Fatalf("expected token to consume for caitlyn, got %q ok=%v", email, ok)
	}
	if _, ok := store.consumePasswordResetToken(token); ok {
		t.Fatal("expected token to be single-use")
	}

	expired, err := store.createPasswordResetToken("caitlyn@shareability.com")
	if err != nil {
		t.Fatalf("createPasswordResetToken: %v", err)
	}
	store.expireResetTokenForTest(expired, time.Now().Add(-time.Minute))
	if _, ok := store.consumePasswordResetToken(expired); ok {
		t.Fatal("expected expired token to be rejected")
	}
}
