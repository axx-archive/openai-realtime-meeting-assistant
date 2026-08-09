package main

import (
	"context"
	"crypto/hmac"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func strideE10W4ActivationTestFiles(t *testing.T) (strideE10W4ActivationPaths, *strideE10W4Keyring, []byte, []byte) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("BONFIRE_USERS_PATH", filepath.Join(dir, "users.json"))
	t.Setenv("BONFIRE_SESSIONS_PATH", filepath.Join(dir, "sessions.json"))
	t.Setenv("BONFIRE_RELEASE_COMMIT", strings.Repeat("a", 40))
	service, err := strideE10W4OrganizationFromMigration(strideE10W4TestManifest(), time.Unix(1_780_000_000, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	runtime := newStrideE10ProductLiveRuntimeWithStores(nil, newStrideE10MemoryPortableDeletionStore(), newStrideE10MemoryOperationStore())
	runtime.organization = service
	keys := strideE10W4TestKeyring()
	paths := strideE10W4ActivationPaths{Snapshot: filepath.Join(dir, "runtime.json"), Sessions: filepath.Join(dir, "sessions.json"), BackupDir: filepath.Join(dir, "activation-backup"), Receipt: filepath.Join(dir, "activation-receipt.json")}
	paths.Journal = filepath.Join(paths.BackupDir, "activation.journal.json")
	snapshot, err := captureStrideE10W4RuntimeSnapshot(runtime)
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(snapshot)
	v1 := strideE10W4SnapshotEnvelope{SchemaVersion: 1, Generation: 1, KeyID: keys.key.ID, KeyVersion: keys.key.Version, Payload: payload}
	v1.MAC = strideE10W4SnapshotMACV1(keys.key, v1.Generation, payload)
	v1Body, _ := json.MarshalIndent(v1, "", "  ")
	if err := writeFileAtomicallyDurable(paths.Snapshot, v1Body, 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_780_100_000, 0).UTC()
	sessions := map[string]sessionRecord{}
	for index, binding := range strideE10W4TestManifest().Bindings {
		hash := sha256Hex([]byte("activation-session-" + binding.PersonID))
		sessions[hash] = sessionRecord{Email: binding.NormalizedSubject, Expires: now.Add(24 * time.Hour), PersonID: binding.PersonID, AccountSubjectDigest: sha256Hex([]byte(binding.NormalizedSubject)), AuthorityGeneration: uint64(index + 1)}
	}
	sessions[sha256Hex([]byte("activation-guest"))] = sessionRecord{Kind: "guest", RoomID: "room-one", GuestName: "Guest", Expires: now.Add(time.Hour)}
	body, _ := json.MarshalIndent(sessions, "", "  ")
	if err := writeFileAtomicallyDurable(paths.Sessions, body, 0o600); err != nil {
		t.Fatal(err)
	}
	originalSnapshot, _ := os.ReadFile(paths.Snapshot)
	originalSessions, _ := os.ReadFile(paths.Sessions)
	return paths, keys, originalSnapshot, originalSessions
}

func TestStrideE10W4NetworkActivationBindsEverySoleMembershipAndKeepsProfilesPrivate(t *testing.T) {
	t.Setenv("BONFIRE_USERS_PATH", t.TempDir()+"/users.json")
	service, err := strideE10W4OrganizationFromMigration(strideE10W4TestManifest(), time.Unix(1_780_000_000, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	runtime := newStrideE10ProductLiveRuntimeWithStores(nil, newStrideE10MemoryPortableDeletionStore(), newStrideE10MemoryOperationStore())
	runtime.organization = service
	snapshot, err := captureStrideE10W4RuntimeSnapshot(runtime)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_780_100_000, 0).UTC()
	sessions := map[string]sessionRecord{}
	for index, binding := range strideE10W4TestManifest().Bindings {
		hash := sha256Hex([]byte("session-" + binding.PersonID))
		sessions[hash] = sessionRecord{Email: binding.NormalizedSubject, Expires: now.Add(24 * time.Hour), PersonID: binding.PersonID, AccountSubjectDigest: sha256Hex([]byte(binding.NormalizedSubject)), AuthorityGeneration: uint64(index + 1)}
	}
	guestHash := sha256Hex([]byte("guest"))
	sessions[guestHash] = sessionRecord{Kind: "guest", RoomID: "room-one", GuestName: "Guest", Expires: now.Add(time.Hour)}
	activated, updated, receipt, err := strideE10W4ActivateSnapshot(snapshot, sessions, now)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.BoundMemberSessions != 7 || receipt.PreservedGuestSessions != 1 || receipt.NetworkDraftCount != 7 || len(receipt.EnabledFeatures) != len(strideE10W4NetworkFeatures) {
		t.Fatalf("activation receipt=%+v", receipt)
	}
	if updated[guestHash] != sessions[guestHash] {
		t.Fatal("guest session changed")
	}
	for hash, record := range updated {
		if record.Kind != "" {
			continue
		}
		if record.ActiveOrganizationID == "" || record.OrganizationMembershipID == "" || record.OrganizationMembershipRev != 1 || record.ActiveOrganizationSessionRev != 1 {
			t.Fatalf("session %s not active: %+v", hash, record)
		}
		active := activated.Organization.Sessions[hash]
		if active.Validate() != nil || active.PersonID != record.PersonID || active.OrganizationID != record.ActiveOrganizationID || active.MembershipID != record.OrganizationMembershipID {
			t.Fatalf("active session mismatch: %+v", active)
		}
	}
	for _, profile := range activated.Network.Profiles {
		if profile.Validate() != nil || profile.State != "draft" || profile.Discoverability != "unlisted" || profile.Publication != (STRIDEReference{}) || len(profile.Fields) != 1 || profile.Fields[0].EvidenceLabel != "self_described" {
			t.Fatalf("private draft invalid: %+v", profile)
		}
	}
	if len(activated.Contribution.Grants) != 18 || len(activated.Network.MembershipAuthorities) != 7 || len(activated.Network.CapabilityAuthorities) != 1 || len(activated.Network.Grants) != 1 {
		t.Fatalf("authority inventory contribution=%d memberships=%d capability=%d grants=%d", len(activated.Contribution.Grants), len(activated.Network.MembershipAuthorities), len(activated.Network.CapabilityAuthorities), len(activated.Network.Grants))
	}
	firstDigest, err := STRIDEContractDigest(activated)
	if err != nil {
		t.Fatal(err)
	}
	firstSessionDigest, err := STRIDEContractDigest(updated)
	if err != nil {
		t.Fatal(err)
	}
	replayed, replayedSessions, replayReceipt, err := strideE10W4ActivateSnapshot(activated, updated, now.Add(10*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	replayedDigest, _ := STRIDEContractDigest(replayed)
	replayedSessionDigest, _ := STRIDEContractDigest(replayedSessions)
	if replayedDigest != firstDigest || replayedSessionDigest != firstSessionDigest || replayReceipt.BoundMemberSessions != 7 {
		t.Fatalf("activation replay changed authority state snapshot=%t sessions=%t receipt=%+v", replayedDigest == firstDigest, replayedSessionDigest == firstSessionDigest, replayReceipt)
	}
}

func TestNetworkAuthoritySelfDescribedProfileDoesNotRequireFakeEvidence(t *testing.T) {
	now := time.Unix(1_780_100_000, 0).UTC()
	authority := NewNetworkAuthority(func() time.Time { return now })
	actor := STRIDEControllerRevision{PrincipalID: "person-one", AuthorityID: "publisher-person-one", AuthorityRevision: 1, PolicyRevision: 1}
	visible := []byte(`"Person One"`)
	fields := []NetworkPublishedField{{FieldKey: "display_name", ValueDigest: sha256Hex(visible), VisibleValue: visible, EvidenceLabel: "self_described"}}
	digest, _ := STRIDEContractDigest(fields)
	profile := NetworkProfileProjection{Header: strideE10LiveHeader(STRIDEContractNetworkProfileProjection, STRIDEGlobalPersonTenant, "profile-self-described", 1, "self-described", now), SubjectPersonID: actor.PrincipalID, Fields: fields, FieldsDigest: digest, Discoverability: "unlisted", Controller: actor, State: "draft", StateChangedAt: now}
	created, _, _, err := authority.PutProfile(actor, profile, 0, sha256Hex([]byte("create")))
	if err != nil || created.Publication != (STRIDEReference{}) {
		t.Fatalf("create=%+v err=%v", created, err)
	}
	published := cloneNetworkProjection(created)
	published.Header = nextAuthorityHeader(created.Header, "publish", now.Add(time.Minute))
	published.State, published.Discoverability, published.StateChangedAt = "published", "signed_in_network", now.Add(time.Minute)
	if _, _, _, err := authority.PutProfile(actor, published, created.Header.Revision, sha256Hex([]byte("publish"))); err == nil {
		t.Fatal("publicationless self-described draft became discoverable")
	}
	forged := cloneNetworkProjection(created)
	forged.Header = nextAuthorityHeader(created.Header, "forged", now.Add(2*time.Minute))
	forged.Fields[0].EvidenceLabel = "organization_verified_redacted"
	forged.Fields[0].Claim = &STRIDEReference{ContractType: STRIDEContractPublishedContributionClaim, ID: "claim-forged", Revision: 1, Digest: sha256Hex([]byte("forged"))}
	forged.FieldsDigest, _ = STRIDEContractDigest(forged.Fields)
	if _, _, _, err := authority.PutProfile(actor, forged, created.Header.Revision, sha256Hex([]byte("forged"))); err == nil {
		t.Fatal("unbound verified evidence accepted")
	}
}

func TestStrideE10W4ActivationJournalRecoversEveryPhaseAndPreservesOriginalBackups(t *testing.T) {
	paths, keys, originalSnapshot, originalSessions := strideE10W4ActivationTestFiles(t)
	at := time.Unix(1_780_100_000, 0).UTC()
	for _, phase := range []strideE10W4ActivationPhase{strideE10W4ActivationPrepared, strideE10W4ActivationSessions, strideE10W4ActivationSnapshot, strideE10W4ActivationReceiptWritten, strideE10W4ActivationCommitted} {
		receipt, err := strideE10W4RunActivation(context.Background(), paths, keys, at, phase)
		if err != nil {
			t.Fatalf("phase %s: %v", phase, err)
		}
		journal, err := strideE10W4LoadJournal(paths.Journal, keys)
		if err != nil || journal.Phase != phase || receipt.ReleaseCommit != strings.Repeat("a", 40) || receipt.ActivationID != journal.ActivationID {
			t.Fatalf("phase=%s journal=%+v receipt=%+v err=%v", phase, journal, receipt, err)
		}
		if backup, _ := os.ReadFile(journal.SnapshotBackupPath); !hmac.Equal(backup, originalSnapshot) {
			t.Fatalf("phase %s changed original snapshot backup", phase)
		}
		if backup, _ := os.ReadFile(journal.SessionsBackupPath); !hmac.Equal(backup, originalSessions) {
			t.Fatalf("phase %s changed original sessions backup", phase)
		}
	}
	if _, err := strideE10W4RunActivation(context.Background(), paths, keys, at.Add(time.Hour), strideE10W4ActivationCommitted); err != nil {
		t.Fatalf("committed replay: %v", err)
	}
	if err := strideE10W4VerifyCommittedActivation(paths, keys, strings.Repeat("a", 40)); err != nil {
		t.Fatal(err)
	}
	before := make(map[string][]byte)
	for _, path := range []string{paths.Snapshot, paths.Sessions, paths.Journal, paths.Receipt} {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		before[path] = body
	}
	if err := strideE10W4VerifyCommittedActivation(paths, keys, strings.Repeat("a", 40)); err != nil {
		t.Fatalf("read-only verifier: %v", err)
	}
	for path, want := range before {
		got, err := os.ReadFile(path)
		if err != nil || !hmac.Equal(got, want) {
			t.Fatalf("read-only verifier changed %s: %v", path, err)
		}
	}
}

func TestStrideE10W4ActivationRejectsTamperStaleReleaseAndPreservesRollbackEvidence(t *testing.T) {
	paths, keys, originalSnapshot, originalSessions := strideE10W4ActivationTestFiles(t)
	if _, err := strideE10W4RunActivation(context.Background(), paths, keys, time.Unix(1_780_100_000, 0).UTC(), strideE10W4ActivationCommitted); err != nil {
		t.Fatal(err)
	}
	_, activatedEnvelope, err := loadStrideE10W4SnapshotEnvelope(paths.Snapshot, keys)
	if err != nil || activatedEnvelope.SchemaVersion != 2 || activatedEnvelope.ActivationID == "" || activatedEnvelope.ActivationReceiptDigest == "" {
		t.Fatalf("journaled activation did not perform the sole v1-to-v2 transition envelope=%+v err=%v", activatedEnvelope, err)
	}
	journal, _ := strideE10W4LoadJournal(paths.Journal, keys)
	backupSnapshot, _ := os.ReadFile(journal.SnapshotBackupPath)
	backupSessions, _ := os.ReadFile(journal.SessionsBackupPath)

	t.Run("stale release", func(t *testing.T) {
		if err := strideE10W4VerifyCommittedActivation(paths, keys, strings.Repeat("b", 40)); !errors.Is(err, ErrStrideE10Denied) {
			t.Fatalf("stale release err=%v", err)
		}
	})
	t.Run("coherently signed stale receipt", func(t *testing.T) {
		body, _ := os.ReadFile(paths.Receipt)
		var receipt strideE10W4ActivationReceipt
		if strideE10W4DecodeAuthenticatedArtifact(body, "stride.e10.w4.activation-receipt-envelope.v1", "receipt", keys, &receipt) != nil {
			t.Fatal("decode receipt")
		}
		receipt.SnapshotGeneration++
		stale, _ := strideE10W4EncodeAuthenticatedArtifact("stride.e10.w4.activation-receipt-envelope.v1", "receipt", receipt, keys)
		if err := writeFileAtomicallyDurable(paths.Receipt, stale, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := strideE10W4VerifyCommittedActivation(paths, keys, strings.Repeat("a", 40)); !errors.Is(err, ErrStrideE10Denied) {
			t.Fatalf("stale signed receipt err=%v", err)
		}
		_ = writeFileAtomicallyDurable(paths.Receipt, body, 0o600)
	})
	t.Run("journal tamper", func(t *testing.T) {
		body, _ := os.ReadFile(paths.Journal)
		tampered := append([]byte(nil), body...)
		tampered[len(tampered)/2] ^= 1
		_ = writeFileAtomicallyDurable(paths.Journal, tampered, 0o600)
		if _, err := strideE10W4LoadJournal(paths.Journal, keys); err == nil {
			t.Fatal("tampered journal loaded")
		}
		_ = writeFileAtomicallyDurable(paths.Journal, body, 0o600)
	})
	t.Run("current snapshot tamper", func(t *testing.T) {
		body, _ := os.ReadFile(paths.Snapshot)
		tampered := append([]byte(nil), body...)
		tampered[len(tampered)/2] ^= 1
		_ = writeFileAtomicallyDurable(paths.Snapshot, tampered, 0o600)
		if err := strideE10W4VerifyCommittedActivation(paths, keys, strings.Repeat("a", 40)); !errors.Is(err, ErrStrideE10Denied) {
			t.Fatalf("tampered snapshot err=%v", err)
		}
		_ = writeFileAtomicallyDurable(paths.Snapshot, body, 0o600)
	})
	if err := strideE10W4RollbackActivation(context.Background(), paths, keys); err != nil {
		t.Fatal(err)
	}
	if err := strideE10W4RollbackActivation(context.Background(), paths, keys); err != nil {
		t.Fatalf("rollback replay: %v", err)
	}
	currentSnapshot, _ := os.ReadFile(paths.Snapshot)
	currentSessions, _ := os.ReadFile(paths.Sessions)
	afterBackupSnapshot, _ := os.ReadFile(journal.SnapshotBackupPath)
	afterBackupSessions, _ := os.ReadFile(journal.SessionsBackupPath)
	if !hmac.Equal(currentSnapshot, originalSnapshot) || !hmac.Equal(currentSessions, originalSessions) || !hmac.Equal(afterBackupSnapshot, backupSnapshot) || !hmac.Equal(afterBackupSessions, backupSessions) {
		t.Fatal("rollback did not restore originals or changed immutable evidence")
	}
	_, restoredEnvelope, err := loadStrideE10W4SnapshotEnvelope(paths.Snapshot, keys)
	if err != nil || restoredEnvelope.SchemaVersion != 1 || restoredEnvelope.ActivationID != "" || restoredEnvelope.ActivationReceiptDigest != "" {
		t.Fatalf("failed-initial rollback did not restore retained-binary-compatible v1 envelope=%+v err=%v", restoredEnvelope, err)
	}
	if err := strideE10W4VerifyCommittedActivation(paths, keys, strings.Repeat("a", 40)); !errors.Is(err, ErrStrideE10Denied) {
		t.Fatalf("rolled-back state verified live: %v", err)
	}
	if err := strideE10W4VerifyRolledBackActivation(paths, keys, strings.Repeat("a", 40)); err != nil {
		t.Fatalf("rollback verification: %v", err)
	}
	oldJournal := journal
	if _, err := strideE10W4RunActivation(context.Background(), paths, keys, time.Unix(1_780_200_000, 0).UTC(), strideE10W4ActivationCommitted); err != nil {
		t.Fatalf("fresh activation after rollback: %v", err)
	}
	fresh, err := strideE10W4LoadJournal(paths.Journal, keys)
	if err != nil || fresh.ActivationID == oldJournal.ActivationID || fresh.Phase != strideE10W4ActivationCommitted {
		t.Fatalf("fresh journal=%+v err=%v", fresh, err)
	}
	archivedJournal := filepath.Join(filepath.Dir(oldJournal.SnapshotBackupPath), "activation.journal.rolled-back.json")
	if archived, err := os.ReadFile(archivedJournal); err != nil || len(archived) == 0 {
		t.Fatalf("old rollback journal was not preserved: %v", err)
	}
	if oldBackup, _ := os.ReadFile(oldJournal.SnapshotBackupPath); !hmac.Equal(oldBackup, backupSnapshot) {
		t.Fatal("fresh activation overwrote prior immutable backup")
	}
}

func TestStrideE10W4RollbackJournalRecoversEveryInterruption(t *testing.T) {
	paths, keys, _, _ := strideE10W4ActivationTestFiles(t)
	if _, err := strideE10W4RunActivation(context.Background(), paths, keys, time.Unix(1_780_100_000, 0).UTC(), strideE10W4ActivationCommitted); err != nil {
		t.Fatal(err)
	}
	for _, phase := range []strideE10W4ActivationPhase{strideE10W4RollbackStarted, strideE10W4RollbackSessions, strideE10W4RollbackSnapshot, strideE10W4RolledBack} {
		if err := strideE10W4RunRollback(context.Background(), paths, keys, phase); err != nil {
			t.Fatalf("rollback phase %s: %v", phase, err)
		}
		journal, err := strideE10W4LoadJournal(paths.Journal, keys)
		if err != nil || journal.Phase != phase {
			t.Fatalf("rollback phase=%s journal=%+v err=%v", phase, journal, err)
		}
		if phase != strideE10W4RolledBack {
			if _, err := strideE10W4RunActivation(context.Background(), paths, keys, time.Unix(1_780_200_000, 0).UTC(), strideE10W4ActivationCommitted); !errors.Is(err, ErrStrideE10Conflict) {
				t.Fatalf("activation escaped interrupted rollback phase=%s err=%v", phase, err)
			}
		}
	}
	if err := strideE10W4VerifyRolledBackActivation(paths, keys, strings.Repeat("a", 40)); err != nil {
		t.Fatal(err)
	}
}

func TestStrideE10W4ActivationRejectsTamperedOriginalBackup(t *testing.T) {
	paths, keys, _, _ := strideE10W4ActivationTestFiles(t)
	if _, err := strideE10W4RunActivation(context.Background(), paths, keys, time.Unix(1_780_100_000, 0).UTC(), strideE10W4ActivationPrepared); err != nil {
		t.Fatal(err)
	}
	journal, _ := strideE10W4LoadJournal(paths.Journal, keys)
	if err := os.WriteFile(journal.SnapshotBackupPath, []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := strideE10W4RunActivation(context.Background(), paths, keys, time.Unix(1_780_100_000, 0).UTC(), strideE10W4ActivationCommitted); !errors.Is(err, ErrStrideE10Denied) {
		t.Fatalf("tampered original backup err=%v", err)
	}
}
