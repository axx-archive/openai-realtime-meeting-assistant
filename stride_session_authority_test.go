package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSessionStoreCanonicalMemberBindingAndRebind(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.json")
	store := newSessionStore(path)
	token, err := store.createMemberSession(
		"aj@shareability.com",
		"person_aj",
		"organization_bonfire",
		"membership_bonfire_aj",
		3,
		7,
		func(string, string, string, int64) error { return nil },
	)
	if err != nil {
		t.Fatalf("create canonical member session: %v", err)
	}
	record, ok := store.lookupRecord(token)
	if !ok || record.PersonID != "person_aj" || record.ActiveOrganizationID != "organization_bonfire" ||
		record.OrganizationMembershipID != "membership_bonfire_aj" || record.OrganizationMembershipRev != 3 || record.ActiveOrganizationSessionRev != 7 || record.AuthorityGeneration != 7 || !isHexDigest(record.AccountSubjectDigest) {
		t.Fatalf("unexpected canonical session: %#v ok=%v", record, ok)
	}

	updated, err := store.rebindActiveOrganization(
		token,
		"person_aj",
		"organization_second",
		"membership_second_aj",
		2,
		7,
		8,
		func(personID, organizationID, membershipID string, revision int64) error {
			if personID != "person_aj" || organizationID != "organization_second" || membershipID != "membership_second_aj" || revision != 2 {
				return ErrOrganizationAuthorityDenied
			}
			return nil
		},
	)
	if err != nil {
		t.Fatalf("rebind active organization: %v", err)
	}
	if updated.ActiveOrganizationID != "organization_second" || updated.OrganizationMembershipID != "membership_second_aj" ||
		updated.OrganizationMembershipRev != 2 || updated.ActiveOrganizationSessionRev != 8 || updated.AuthorityGeneration != 8 {
		t.Fatalf("unexpected rebound session: %#v", updated)
	}
	if _, err := store.rebindActiveOrganization(token, "person_aj", "organization_bonfire", "membership_bonfire_aj", 4, 7, 8, func(string, string, string, int64) error { return nil }); err == nil {
		t.Fatal("stale session revision unexpectedly rebound authority")
	}
	if _, err := store.rebindActiveOrganization(token, "person_other", "organization_second", "membership_second_aj", 2, 8, 9, func(string, string, string, int64) error {
		return ErrOrganizationAuthorityDenied
	}); err == nil {
		t.Fatal("unauthorized membership unexpectedly rebound authority")
	}

	restored := newSessionStore(path)
	restoredRecord, ok := restored.lookupRecord(token)
	if !ok || restoredRecord.ActiveOrganizationID != "organization_second" || restoredRecord.ActiveOrganizationSessionRev != 8 {
		t.Fatalf("canonical authority did not survive restart: %#v ok=%v", restoredRecord, ok)
	}
}

func TestSessionStoreCanonicalBindingRejectsPartialAndRevokesExactMembership(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.json")
	store := newSessionStore(path)
	if _, err := store.createMemberSession("aj@shareability.com", "person_aj", "", "", 0, 0, nil); err == nil {
		t.Fatal("partial canonical binding unexpectedly accepted")
	}
	zeroOrganization, err := store.createMemberSession("aj@shareability.com", "person_aj", "", "", 0, 0, func(personID, organizationID, membershipID string, revision int64) error {
		if personID != "person_aj" || organizationID != "" || membershipID != "" || revision != 0 {
			return ErrOrganizationAuthorityDenied
		}
		return nil
	})
	if err != nil {
		t.Fatalf("create explicit zero-organization session: %v", err)
	}
	if record, ok := store.lookupRecord(zeroOrganization); !ok || record.PersonID != "person_aj" || record.ActiveOrganizationID != "" || record.OrganizationMembershipID != "" || record.AuthorityGeneration != 1 {
		t.Fatalf("invalid explicit zero-organization session: %+v ok=%t", record, ok)
	}
	legacy, err := store.create("aj@shareability.com")
	if err != nil {
		t.Fatalf("create legacy session: %v", err)
	}
	canonical, err := store.createMemberSession("aj@shareability.com", "person_aj", "organization_bonfire", "membership_bonfire_aj", 4, 9, func(string, string, string, int64) error { return nil })
	if err != nil {
		t.Fatalf("create canonical session: %v", err)
	}
	newer, err := store.createMemberSession("aj@shareability.com", "person_aj", "organization_bonfire", "membership_bonfire_aj", 6, 10, func(string, string, string, int64) error { return nil })
	if err != nil {
		t.Fatalf("create newer canonical session: %v", err)
	}
	other, err := store.createMemberSession("aj@shareability.com", "person_aj", "organization_other", "membership_other_aj", 1, 1, func(string, string, string, int64) error { return nil })
	if err != nil {
		t.Fatalf("create other membership session: %v", err)
	}

	if removed := store.destroyAllForMembershipRevision("person_aj", "organization_bonfire", "membership_bonfire_aj", 4); removed != 1 {
		t.Fatalf("removed=%d want 1", removed)
	}
	if _, ok := store.lookupRecord(canonical); ok {
		t.Fatal("revoked canonical session remains live")
	}
	for label, token := range map[string]string{"legacy": legacy, "newer": newer, "other": other} {
		if _, ok := store.lookupRecord(token); !ok {
			t.Fatalf("%s session was over-revoked", label)
		}
	}
}

func TestSessionStoreRebindAndRevocationShareLinearizationFence(t *testing.T) {
	store := newSessionStore(filepath.Join(t.TempDir(), "sessions.json"))
	if _, err := store.createMemberSession("aj@shareability.com", "person_aj", "organization_bonfire", "membership_bonfire_aj", 1, 1, nil); err == nil {
		t.Fatal("canonical creation without authority resolver succeeded")
	}
	token, err := store.createMemberSession("aj@shareability.com", "person_aj", "organization_old", "membership_old", 1, 1, func(string, string, string, int64) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	authorized := make(chan struct{})
	releaseAuthorization := make(chan struct{})
	rebound := make(chan error, 1)
	go func() {
		_, err := store.rebindActiveOrganization(token, "person_aj", "organization_bonfire", "membership_bonfire_aj", 4, 1, 2, func(string, string, string, int64) error {
			close(authorized)
			<-releaseAuthorization
			return nil
		})
		rebound <- err
	}()
	<-authorized
	revoked := make(chan int, 1)
	go func() {
		revoked <- store.destroyAllForMembershipRevision("person_aj", "organization_bonfire", "membership_bonfire_aj", 4)
	}()
	select {
	case <-revoked:
		t.Fatal("revocation escaped the shared session-authority fence")
	case <-time.After(20 * time.Millisecond):
	}
	close(releaseAuthorization)
	if err := <-rebound; err != nil {
		t.Fatalf("rebind: %v", err)
	}
	if removed := <-revoked; removed != 1 {
		t.Fatalf("revocation removed=%d want 1", removed)
	}
	if _, ok := store.lookupRecord(token); ok {
		t.Fatal("revoked membership session survived interleaving")
	}
}

func TestSessionStoreLegacyRowsRemainBackwardCompatible(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.json")
	legacy := map[string]sessionRecord{
		hashResetToken("legacy-token"): {
			Email:   "tim@shareability.com",
			Expires: time.Now().Add(time.Hour),
		},
	}
	raw, err := json.Marshal(legacy)
	if err != nil {
		t.Fatalf("marshal legacy sessions: %v", err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("write legacy sessions: %v", err)
	}
	store := newSessionStore(path)
	record, ok := store.lookupRecord("legacy-token")
	if !ok || record.Email != "tim@shareability.com" || record.PersonID != "" || record.ActiveOrganizationSessionRev != 0 {
		t.Fatalf("legacy session did not remain compatible: %#v ok=%v", record, ok)
	}
}
