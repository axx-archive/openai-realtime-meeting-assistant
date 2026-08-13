package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestHomeProjectContextTokenBindsDraftDestinationAndAuthority(t *testing.T) {
	snapshot := projectChatSnapshotFixture(t)
	key := StrideE10TenantAuthorityEnvelopeKey{ID: "home_project_key", Version: 1, Secret: []byte(strings.Repeat("h", 32))}
	restore := InstallStrideE10TenantAuthorityEnvelopeRuntime(&strideE10TenantEnvelopeTestKeyring{current: key, keys: map[string]StrideE10TenantAuthorityEnvelopeKey{key.ID: key}})
	defer restore()
	destination := homeProjectDestination{Route: "new-private"}
	project := homeProjectRow{ID: "project_home", Revision: 2, Digest: strings.Repeat("a", 64), Title: "Launch Plan"}
	token, err := mintHomeProjectToken(context.Background(), snapshot, "Build the launch plan", destination, project, "project", "suggested", .9)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := resolveHomeProjectToken(context.Background(), token, "Build the launch plan", destination, snapshot)
	if err != nil || resolved.ProjectID != project.ID || resolved.ProjectTitle != project.Title || resolved.ExpiresAt.Before(time.Now().UTC()) {
		t.Fatalf("resolved=%+v err=%v", resolved, err)
	}
	if _, err := resolveHomeProjectToken(context.Background(), token, "Changed draft", destination, snapshot); err == nil {
		t.Fatal("token survived a draft edit")
	}
	if _, err := resolveHomeProjectToken(context.Background(), token, "Build the launch plan", homeProjectDestination{Route: "thread", ThreadID: "other_thread"}, snapshot); err == nil {
		t.Fatal("token survived a destination change")
	}
	tampered := "x" + token[1:]
	if _, err := resolveHomeProjectToken(context.Background(), tampered, "Build the launch plan", destination, snapshot); err == nil {
		t.Fatal("tampered token was accepted")
	}
	other := snapshot
	other.Generation++
	if _, err := resolveHomeProjectToken(context.Background(), token, "Build the launch plan", destination, other); err == nil {
		t.Fatal("token crossed an authority generation")
	}
}

func TestHomeProjectExpiredTokenOnlyResolvesForAcceptedPendingRetry(t *testing.T) {
	snapshot := projectChatSnapshotFixture(t)
	key := StrideE10TenantAuthorityEnvelopeKey{ID: "home_project_retry_key", Version: 1, Secret: []byte(strings.Repeat("r", 32))}
	restore := InstallStrideE10TenantAuthorityEnvelopeRuntime(&strideE10TenantEnvelopeTestKeyring{current: key, keys: map[string]StrideE10TenantAuthorityEnvelopeKey{key.ID: key}})
	defer restore()
	destination := homeProjectDestination{Route: "new-private"}
	token := homeProjectContextToken{
		Version: homeProjectContextVersion, Kind: "create", TextDigest: sha256Hex([]byte("Build the plan")), Destination: destination,
		PersonID: snapshot.Person.Header.ID, OrganizationID: snapshot.Organization.Header.ID, MembershipID: snapshot.Membership.Header.ID,
		MembershipRevision: snapshot.Membership.Header.Revision, SessionSubjectDigest: snapshot.SessionHash,
		SessionRevision: snapshot.ActiveSession.SessionRevision, AuthorityGeneration: snapshot.Generation,
		ProjectTitle: "Launch Plan", Basis: "selected", ClassifierRevision: "project_linker_v1", Confidence: 1,
		IssuedAt: time.Now().UTC().Add(-2 * time.Hour), ExpiresAt: time.Now().UTC().Add(-time.Hour), KeyID: key.ID, KeyVersion: key.Version,
	}
	raw, _ := json.Marshal(token)
	encoded := base64.RawURLEncoding.EncodeToString(raw) + "." + base64.RawURLEncoding.EncodeToString(homeProjectTokenMAC(key, raw))
	if _, err := resolveHomeProjectToken(context.Background(), encoded, "Build the plan", destination, snapshot); err == nil {
		t.Fatal("expired token started a new Send")
	}
	resolved, err := resolveHomeProjectTokenForRetry(context.Background(), encoded, "Build the plan", destination, snapshot, true)
	if err != nil || resolved.ProjectTitle != "Launch Plan" {
		t.Fatalf("accepted pending retry did not recover: resolved=%+v err=%v", resolved, err)
	}
	if _, err := resolveHomeProjectTokenForRetry(context.Background(), encoded, "Changed", destination, snapshot, true); err == nil {
		t.Fatal("pending retry exception survived a body change")
	}
}
