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

func TestHomeProjectContextV2BindsExactSourceManifestAndChoice(t *testing.T) {
	snapshot := projectChatSnapshotFixture(t)
	key := StrideE10TenantAuthorityEnvelopeKey{ID: "home_project_manifest_key", Version: 1, Secret: []byte(strings.Repeat("m", 32))}
	restore := InstallStrideE10TenantAuthorityEnvelopeRuntime(&strideE10TenantEnvelopeTestKeyring{current: key, keys: map[string]StrideE10TenantAuthorityEnvelopeKey{key.ID: key}})
	defer restore()
	destination := homeProjectDestination{Route: "thread", ThreadID: "thread_manifest"}
	manifest := projectChatSourceManifest{
		Version: projectChatSourceManifestVersion, Destination: destination, TextDigest: sha256Hex([]byte("Ship it")),
		Attachments: []projectChatManifestAttachment{
			{Ordinal: 0, SourceID: "source_one", SourceRevision: "revision_one", BlobRef: "blob_one", BlobDigest: strings.Repeat("b", 64), Mime: "application/pdf", Size: 42, DestinationRevision: "destination_one"},
			{Ordinal: 1, SourceID: "source_two", SourceRevision: "revision_two", BlobRef: "blob_two", BlobDigest: strings.Repeat("f", 64), Mime: "image/png", Size: 84, DestinationRevision: "destination_one"},
		},
		Reply: &projectChatManifestReply{MessageID: "message_parent", EventID: "event_parent", SourceRevision: 2, SourceDigest: strings.Repeat("c", 64), LegacyDigest: strings.Repeat("d", 64), AuthorPersonID: "person_parent", AudienceDigest: strings.Repeat("e", 64), ACLRevision: 3, PurgeGeneration: 1},
	}
	manifest.Digest = projectChatManifestDigest(manifest)
	project := homeProjectRow{ID: "project_manifest", Revision: 4, Digest: strings.Repeat("a", 64), Title: "Launch Plan"}
	encoded, choiceKey, err := mintHomeProjectTokenV2(context.Background(), snapshot, "Ship it", destination, manifest, project, "project", "selected", 1)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := resolveHomeProjectTokenForRetryWithManifest(context.Background(), encoded, "Ship it", destination, manifest, snapshot, false)
	if err != nil || resolved.SourceManifestDigest != manifest.Digest || resolved.ChoiceKey != choiceKey || choiceKey == "" {
		t.Fatalf("resolved=%+v choice=%q err=%v", resolved, choiceKey, err)
	}
	changedOrder := manifest
	changedOrder.Attachments = append([]projectChatManifestAttachment(nil), manifest.Attachments...)
	changedOrder.Attachments[0].SourceRevision = "revision_two"
	changedOrder.Digest = projectChatManifestDigest(changedOrder)
	if _, err := resolveHomeProjectTokenForRetryWithManifest(context.Background(), encoded, "Ship it", destination, changedOrder, snapshot, false); err == nil {
		t.Fatal("v2 token survived an attachment revision change")
	}
	reordered := manifest
	reordered.Attachments = append([]projectChatManifestAttachment(nil), manifest.Attachments...)
	reordered.Attachments[0], reordered.Attachments[1] = reordered.Attachments[1], reordered.Attachments[0]
	reordered.Attachments[0].Ordinal, reordered.Attachments[1].Ordinal = 0, 1
	reordered.Digest = projectChatManifestDigest(reordered)
	if _, err := resolveHomeProjectTokenForRetryWithManifest(context.Background(), encoded, "Ship it", destination, reordered, snapshot, false); err == nil {
		t.Fatal("v2 token survived attachment reordering")
	}
	changedReply := manifest
	reply := *manifest.Reply
	reply.EventID = "event_other"
	changedReply.Reply = &reply
	changedReply.Digest = projectChatManifestDigest(changedReply)
	if _, err := resolveHomeProjectTokenForRetryWithManifest(context.Background(), encoded, "Ship it", destination, changedReply, snapshot, false); err == nil {
		t.Fatal("v2 token survived a reply change")
	}

	legacy, err := mintHomeProjectToken(context.Background(), snapshot, "Ship it", destination, project, "project", "selected", 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resolveHomeProjectTokenForRetryWithManifest(context.Background(), legacy, "Ship it", destination, manifest, snapshot, false); err == nil {
		t.Fatal("v1 token accepted attachments or a reply")
	}
}
