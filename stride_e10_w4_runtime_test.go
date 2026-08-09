package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func strideE10W4SetRuntimeTestEnvironment(t *testing.T, paths strideE10W4ActivationPaths, keys *strideE10W4Keyring, operationPath string) {
	t.Helper()
	t.Setenv(strideE10W4SnapshotPathEnv, paths.Snapshot)
	t.Setenv(strideE10W4OperationPathEnv, operationPath)
	t.Setenv(strideE10W4ActivationBackupDirEnv, paths.BackupDir)
	t.Setenv(strideE10W4ActivationReceiptPathEnv, paths.Receipt)
	t.Setenv(strideE10W4KeyIDEnv, keys.key.ID)
	t.Setenv(strideE10W4KeyVersionEnv, "1")
	t.Setenv(strideE10W4KeySecretEnv, base64.StdEncoding.EncodeToString(keys.key.Secret))
}

func strideE10W4TestKeyring() *strideE10W4Keyring {
	return &strideE10W4Keyring{key: StrideE10MigrationMACKey{ID: "w4-test-key", Version: 1, Secret: []byte(strings.Repeat("k", 32))}}
}

func isolateStrideE10W4ReadinessForTest(t *testing.T) {
	t.Helper()
	prior := strideE10W4ReadinessSnapshot()
	t.Cleanup(func() {
		strideE10W4RuntimeState.Lock()
		strideE10W4RuntimeState.ready = prior["ready"].(bool)
		strideE10W4RuntimeState.mode = prior["mode"].(string)
		strideE10W4RuntimeState.generation = prior["generation"].(uint64)
		strideE10W4RuntimeState.schemaVersion = prior["schemaVersion"].(uint64)
		strideE10W4RuntimeState.activationID = prior["activationId"].(string)
		strideE10W4RuntimeState.activationReceiptDigest = prior["activationReceiptDigest"].(string)
		strideE10W4RuntimeState.enabledFeatures = append([]string(nil), prior["enabledFeatures"].([]string)...)
		strideE10W4RuntimeState.reason = prior["reason"].(string)
		strideE10W4RuntimeState.Unlock()
	})
	strideE10W4RuntimeState.Lock()
	strideE10W4RuntimeState.ready = false
	strideE10W4RuntimeState.mode = ""
	strideE10W4RuntimeState.generation = 0
	strideE10W4RuntimeState.schemaVersion = 0
	strideE10W4RuntimeState.activationID = ""
	strideE10W4RuntimeState.activationReceiptDigest = ""
	strideE10W4RuntimeState.enabledFeatures = nil
	strideE10W4RuntimeState.reason = ""
	strideE10W4RuntimeState.Unlock()
}

func TestStrideE10W4ReadinessTracksOnlyDurableGenerations(t *testing.T) {
	isolateStrideE10W4ReadinessForTest(t)
	features := []STRIDEFeature{STRIDEFeatureWorkRecordPrivate, STRIDEFeaturePersonProfileAuthority}
	updateStrideE10W4RuntimeReadiness(strideE10W4CanaryMode, 41, 2, "activation-one", "receipt-one", features)
	snapshot := strideE10W4ReadinessSnapshot()
	if snapshot["ready"] != true || snapshot["mode"] != strideE10W4CanaryMode || snapshot["generation"] != uint64(41) || snapshot["schemaVersion"] != uint64(2) || snapshot["activationId"] != "activation-one" || snapshot["activationReceiptDigest"] != "receipt-one" {
		t.Fatalf("readiness=%+v", snapshot)
	}
	gotFeatures := snapshot["enabledFeatures"].([]string)
	if len(gotFeatures) != 2 || gotFeatures[0] != string(STRIDEFeaturePersonProfileAuthority) || gotFeatures[1] != string(STRIDEFeatureWorkRecordPrivate) {
		t.Fatalf("features=%v", gotFeatures)
	}
	generation := uint64(41)
	published := uint64(0)
	if err := persistStrideE10W4RuntimeGeneration(&generation, func(uint64) error { return errors.New("disk unavailable") }, func(next uint64) { published = next }, markStrideE10W4RuntimePersistenceFailed); err == nil {
		t.Fatal("failed persistence advanced readiness")
	}
	failedSnapshot := strideE10W4ReadinessSnapshot()
	if generation != 41 || published != 0 || failedSnapshot["generation"] != uint64(41) || failedSnapshot["ready"] != false || failedSnapshot["reason"] != "persistence_failed" {
		t.Fatalf("failed persistence generation=%d published=%d readiness=%+v", generation, published, strideE10W4ReadinessSnapshot())
	}
	if err := persistStrideE10W4RuntimeGeneration(&generation, func(next uint64) error {
		if next != 42 {
			t.Fatalf("next=%d", next)
		}
		return nil
	}, func(next uint64) {
		published = next
		updateStrideE10W4RuntimeReadiness(strideE10W4CanaryMode, next, 2, "activation-one", "receipt-one", features)
	}, markStrideE10W4RuntimePersistenceFailed); err != nil {
		t.Fatal(err)
	}
	if generation != 42 || published != 42 || strideE10W4ReadinessSnapshot()["generation"] != uint64(42) {
		t.Fatalf("successful persistence generation=%d published=%d readiness=%+v", generation, published, strideE10W4ReadinessSnapshot())
	}
}

func strideE10W4TestManifest() StrideE10PrivateMigrationManifest {
	manifest := StrideE10PrivateMigrationManifest{Version: 1, OrganizationID: "organization_w4_bonfire", OrganizationName: "Bonfire", OrganizationSlug: "bonfire", TargetDigest: strings.Repeat("a", 64)}
	for index, account := range seededAccounts {
		role := "member"
		if normalizeAccountEmail(account.Email) == "aj@shareability.com" {
			role = "owner"
		}
		manifest.Bindings = append(manifest.Bindings, StrideE10PrivateMigrationBinding{NormalizedSubject: normalizeAccountEmail(account.Email), PersonID: "person_w4_" + string(rune('a'+index)), MembershipID: "membership_w4_" + string(rune('a'+index)), MembershipRevision: 1, Role: role, ProfileDigest: strings.Repeat(string(rune('a'+index)), 64)})
	}
	return manifest
}

func TestStrideE10W4SnapshotIsAuthenticatedAndRestartDurable(t *testing.T) {
	t.Setenv("BONFIRE_USERS_PATH", filepath.Join(t.TempDir(), "users.json"))
	service, err := strideE10W4OrganizationFromMigration(strideE10W4TestManifest(), time.Unix(1_780_000_000, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "runtime.json")
	keys := strideE10W4TestKeyring()
	runtime := newStrideE10ProductLiveRuntimeWithStores(nil, newStrideE10MemoryPortableDeletionStore(), newStrideE10MemoryOperationStore())
	runtime.organization = service
	runtime.portableStore.Save(StrideE10PortableDeletionRecord{PersonID: "person_w4_a", DeletedAt: time.Unix(1_780_000_100, 0).UTC()})
	runtime.joinCodes[sha256Hex([]byte("private-code"))] = "organization_w4_bonfire"
	if err := writeStrideE10W4RuntimeSnapshot(path, 1, keys, runtime); err != nil {
		t.Fatal(err)
	}
	reloaded, generation, err := loadStrideE10W4Snapshot(path, keys)
	if err != nil || generation != 1 || len(reloaded.Organization.Persons) != 7 || len(reloaded.Organization.Memberships) != 7 {
		t.Fatalf("reload generation=%d err=%v", generation, err)
	}
	if len(reloaded.Portable) != 1 || len(reloaded.JoinCodes) != 1 {
		t.Fatalf("durable runtime state portable=%d joinCodes=%d", len(reloaded.Portable), len(reloaded.JoinCodes))
	}
	body, _ := os.ReadFile(path)
	body[len(body)/2] ^= 1
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadStrideE10W4Snapshot(path, keys); err == nil {
		t.Fatal("tampered W4 snapshot loaded")
	}
}

func TestStrideE10W4PostgresAppliesExactSevenAndKeepsDatabaseSwitchesOff(t *testing.T) {
	t.Setenv("BONFIRE_USERS_PATH", filepath.Join(t.TempDir(), "users.json"))
	service, err := strideE10W4OrganizationFromMigration(strideE10W4TestManifest(), time.Unix(1_780_000_000, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	ctx, store, _ := migratedPostgresCanonicalStore(t)
	if err := strideE10W4ApplyPostgres(ctx, store, strideE10W4TestManifest(), service); err != nil {
		t.Fatal(err)
	}
	if err := strideE10W4ApplyPostgres(ctx, store, strideE10W4TestManifest(), service); err != nil {
		t.Fatalf("idempotent replay: %v", err)
	}
	var people, members, owners, enabled int
	if err := store.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM stride_person_principals WHERE person_id LIKE 'person_w4_%'),(SELECT count(*) FROM stride_organization_memberships_current WHERE organization_id='organization_w4_bonfire'),(SELECT count(*) FROM stride_organization_memberships_current WHERE organization_id='organization_w4_bonfire' AND role='owner'),(SELECT count(*) FROM stride_feature_switches WHERE feature_key IN ('person_profile_authority','organization_authority_read','organization_authority_write','active_organization_session','work_record_private') AND enabled)`).Scan(&people, &members, &owners, &enabled); err != nil {
		t.Fatal(err)
	}
	if people != 7 || members != 7 || owners != 1 || enabled != 0 {
		t.Fatalf("people=%d members=%d owners=%d enabled=%d", people, members, owners, enabled)
	}
}

func TestStrideE10W4ProductionInstallUsesOnlyClosedCanaryFeatures(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BONFIRE_USERS_PATH", filepath.Join(dir, "users.json"))
	t.Setenv("BONFIRE_SESSIONS_PATH", filepath.Join(dir, "sessions.json"))
	service, err := strideE10W4OrganizationFromMigration(strideE10W4TestManifest(), time.Unix(1_780_000_000, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	snapshot := filepath.Join(dir, "runtime.json")
	operations := filepath.Join(dir, "operations.json")
	keys := strideE10W4TestKeyring()
	runtime := newStrideE10ProductLiveRuntimeWithStores(nil, newStrideE10MemoryPortableDeletionStore(), newStrideE10MemoryOperationStore())
	runtime.organization = service
	if err := writeStrideE10W4RuntimeSnapshot(snapshot, 1, keys, runtime); err != nil {
		t.Fatal(err)
	}
	t.Setenv(strideE10W4ModeEnv, strideE10W4CanaryMode)
	t.Setenv(strideE10W4SnapshotPathEnv, snapshot)
	t.Setenv(strideE10W4OperationPathEnv, operations)
	t.Setenv(strideE10W4KeyIDEnv, keys.key.ID)
	t.Setenv(strideE10W4KeyVersionEnv, "1")
	t.Setenv(strideE10W4KeySecretEnv, base64.StdEncoding.EncodeToString(keys.key.Secret))
	isolateStrideE10W4ReadinessForTest(t)
	prior := strideE10LiveProductRuntime
	t.Cleanup(func() {
		strideE10LiveProductRuntime = prior
	})
	if err := installStrideE10W4ProductionRuntimeFromEnvironment(); err != nil {
		t.Fatal(err)
	}
	for _, feature := range allSTRIDEFeatures {
		want := containsSTRIDEString([]string{string(STRIDEFeaturePersonProfileAuthority), string(STRIDEFeatureOrganizationAuthorityRead), string(STRIDEFeatureOrganizationAuthorityWrite), string(STRIDEFeatureActiveOrganizationSession), string(STRIDEFeatureWorkRecordPrivate)}, string(feature))
		if strideE10LiveProductRuntime.Enabled(feature) != want {
			t.Fatalf("feature %s enabled=%t want=%t", feature, strideE10LiveProductRuntime.Enabled(feature), want)
		}
	}
	if !strideE10W4ProductionRuntimeReady() {
		t.Fatal("durable W4 runtime did not mark ready")
	}
	token, err := strideE10CreateAuthenticatedSession("aj@shareability.com")
	if err != nil {
		t.Fatal(err)
	}
	record, ok := userSessionStore().lookupRecord(token)
	if !ok || record.PersonID == "" || record.ActiveOrganizationID != "" || record.OrganizationMembershipID != "" || record.OrganizationMembershipRev != 0 || record.ActiveOrganizationSessionRev != 0 || record.AuthorityGeneration != 1 {
		t.Fatalf("W4 canary login record=%+v ok=%t", record, ok)
	}
	person := record.PersonID
	if _, _, err := strideE10LiveProductRuntime.Execute(context.Background(), StrideE10ProductPrincipal{PersonID: person}, StrideE10ProductCommand{Operation: "identity.self_profile", Method: "GET", Path: "/api/stride/v1/mobile/surfaces/profile", ResourceID: "profile"}); err != nil {
		t.Fatal(err)
	}
	persisted, _, err := loadStrideE10W4Snapshot(snapshot, keys)
	if err != nil || len(persisted.Actions) == 0 {
		t.Fatalf("server-minted actions not durable actions=%d err=%v", len(persisted.Actions), err)
	}
}

func TestStrideE10W4NetworkModeEnablesOnlyReviewedProductDependencies(t *testing.T) {
	features, err := strideE10W4FeaturesForMode(strideE10W4NetworkMode)
	if err != nil {
		t.Fatal(err)
	}
	want := map[STRIDEFeature]bool{}
	for _, feature := range strideE10W4NetworkFeatures {
		want[feature] = true
	}
	if len(features) != len(want) {
		t.Fatalf("network feature count=%d want=%d", len(features), len(want))
	}
	for _, feature := range allSTRIDEFeatures {
		if containsSTRIDEString([]string{string(STRIDEFeatureNetworkQueryParserProvider), string(STRIDEFeatureNetworkSemanticReranker), string(STRIDEFeaturePersonMyMindContext)}, string(feature)) && want[feature] {
			t.Fatalf("unqualified provider or MyMind dependency enabled: %s", feature)
		}
	}
	if _, err := strideE10W4FeaturesForMode("unknown"); err == nil {
		t.Fatal("unknown W4 mode accepted")
	}
}

func TestStrideE10W4SessionBindingPreservesTokensAndUsesZeroOrganization(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BONFIRE_USERS_PATH", filepath.Join(dir, "users.json"))
	t.Setenv("BONFIRE_SESSIONS_PATH", filepath.Join(dir, "sessions.json"))
	token, err := userSessionStore().create("aj@shareability.com")
	if err != nil {
		t.Fatal(err)
	}
	before := hashResetToken(token)
	if err := strideE10W4BindExistingSessions(strideE10W4TestManifest()); err != nil {
		t.Fatal(err)
	}
	record, ok := userSessionStore().lookupRecord(token)
	if !ok || record.PersonID == "" || !isHexDigest(record.AccountSubjectDigest) || record.AuthorityGeneration != 1 || record.ActiveOrganizationID != "" || record.OrganizationMembershipID != "" || record.OrganizationMembershipRev != 0 || record.ActiveOrganizationSessionRev != 0 {
		t.Fatalf("bound session=%+v ok=%t", record, ok)
	}
	if before != hashResetToken(token) {
		t.Fatal("session token identity changed")
	}
	reloaded := newSessionStore(filepath.Join(dir, "sessions.json"))
	if _, ok := reloaded.sessions[before]; !ok {
		t.Fatal("bound session did not persist under exact token hash")
	}
}

func TestStrideE10W4NetworkLiveStartupUsesLineageAndCurrentSessionSemantics(t *testing.T) {
	paths, keys, _, _ := strideE10W4ActivationTestFiles(t)
	if _, err := strideE10W4RunActivation(context.Background(), paths, keys, time.Unix(1_780_100_000, 0).UTC(), strideE10W4ActivationCommitted); err != nil {
		t.Fatal(err)
	}
	snapshot, envelope, err := loadStrideE10W4SnapshotEnvelope(paths.Snapshot, keys)
	if err != nil || envelope.ActivationID == "" || envelope.ActivationReceiptDigest == "" {
		t.Fatalf("activation lineage=%+v err=%v", envelope, err)
	}
	runtime := runtimeFromStrideE10W4Snapshot(snapshot, newStrideE10MemoryOperationStore())
	updatedProfileID := ""
	for id, current := range runtime.network.profiles {
		updated := cloneNetworkProjection(current)
		updated.Header = nextAuthorityHeader(current.Header, "private-edit", time.Unix(1_780_100_100, 0).UTC())
		updated.Fields[0].VisibleValue = json.RawMessage(`"Updated Person"`)
		updated.Fields[0].ValueDigest = sha256Hex(updated.Fields[0].VisibleValue)
		updated.FieldsDigest, _ = STRIDEContractDigest(updated.Fields)
		updated.StateChangedAt = time.Unix(1_780_100_100, 0).UTC()
		if _, _, _, err := runtime.network.PutProfile(updated.Controller, updated, current.Header.Revision, sha256Hex([]byte("w4-private-edit"))); err != nil {
			t.Fatal(err)
		}
		updatedProfileID = id
		break
	}
	for id := range runtime.network.profiles {
		if id != updatedProfileID {
			delete(runtime.network.profiles, id)
			break
		}
	}
	for id, grant := range runtime.network.grants {
		revokedAt := time.Unix(1_780_100_120, 0).UTC()
		grant.Header = nextAuthorityHeader(grant.Header, "revoke", revokedAt)
		grant.State, grant.RevokedAt = "revoked", &revokedAt
		runtime.network.grants[id] = grant
		runtime.network.grantVersions[networkVersionKey(id, grant.Header.Revision)] = grant
		break
	}
	var currentOwner OrganizationMembership
	for _, membership := range runtime.organization.memberships {
		if membership.Status == "active" && membership.Role == "owner" {
			currentOwner = membership
			break
		}
	}
	secondOrganization, secondOwner, _ := organizationTestCreate(currentOwner.PersonID, 1, time.Unix(1_780_100_130, 0).UTC())
	runtime.organization.organizations[secondOrganization.Header.ID] = secondOrganization
	runtime.organization.memberships[secondOwner.Header.ID] = secondOwner
	runtime.network.membershipAuthorities[secondOwner.Header.ID] = NetworkMembershipAuthority{MembershipID: secondOwner.Header.ID, OrganizationID: secondOwner.OrganizationID, PersonID: secondOwner.PersonID, Revision: secondOwner.Header.Revision, Active: true}
	var revokedMembership OrganizationMembership
	for id, membership := range runtime.organization.memberships {
		if membership.OrganizationID == currentOwner.OrganizationID && membership.Role != "owner" && membership.Status == "active" {
			endedAt := time.Unix(1_780_100_140, 0).UTC()
			membership.Header = nextAuthorityHeader(membership.Header, "revoke", endedAt)
			membership.Status, membership.EndedAt = "revoked", &endedAt
			runtime.organization.memberships[id] = membership
			runtime.network.membershipAuthorities[id] = NetworkMembershipAuthority{MembershipID: id, OrganizationID: membership.OrganizationID, PersonID: membership.PersonID, Revision: membership.Header.Revision, Active: false}
			revokedMembership = membership
			break
		}
	}
	sessionsBody, _ := os.ReadFile(paths.Sessions)
	var sessions map[string]sessionRecord
	_ = json.Unmarshal(sessionsBody, &sessions)
	for hash, record := range sessions {
		if record.PersonID == revokedMembership.PersonID {
			delete(sessions, hash)
			delete(runtime.organization.sessions, hash)
		}
	}
	var person PersonPrincipal
	for _, candidate := range snapshot.Organization.Persons {
		person = candidate
		break
	}
	newHash := sha256Hex([]byte("successor-zero-org-session"))
	sessions[newHash] = sessionRecord{Email: "successor@example.com", Expires: time.Now().UTC().Add(time.Hour), PersonID: person.Header.ID, AccountSubjectDigest: person.AccountSubjectDigest, AuthorityGeneration: 1}
	if err := writeStrideE10W4RuntimeSnapshotWithLineage(paths.Snapshot, envelope.Generation+1, keys, runtime, envelope.ActivationID, envelope.ActivationReceiptDigest); err != nil {
		t.Fatal(err)
	}
	updatedSessions, _ := json.MarshalIndent(sessions, "", "  ")
	if err := writeFileAtomicallyDurable(paths.Sessions, updatedSessions, 0o600); err != nil {
		t.Fatal(err)
	}
	// A later exact release inherits the signed activation lineage. Its own code
	// identity is enforced independently; it must not rewrite live user state.
	verifiedEnvelope, verifiedJournal, err := strideE10W4VerifySuccessorRuntime(paths, keys, strings.Repeat("b", 40))
	if err != nil || verifiedEnvelope.Generation != envelope.Generation+1 || verifiedJournal.ReleaseCommit != strings.Repeat("a", 40) {
		t.Fatalf("successor runtime verification envelope=%+v journal=%+v err=%v", verifiedEnvelope, verifiedJournal, err)
	}
	strideE10W4SetRuntimeTestEnvironment(t, paths, keys, filepath.Join(t.TempDir(), "operations.json"))
	isolateStrideE10W4ReadinessForTest(t)
	prior := strideE10LiveProductRuntime
	t.Cleanup(func() {
		strideE10LiveProductRuntime = prior
	})
	// An explicit live-to-canary downgrade authenticates the evolved lineage but
	// never runs rollback: member, profile, organization, and session evolution
	// remains byte-for-byte intact for the canary runtime.
	beforeDowngrade := map[string][]byte{}
	for _, path := range []string{paths.Snapshot, paths.Sessions, paths.Journal, paths.Receipt} {
		beforeDowngrade[path], _ = os.ReadFile(path)
	}
	if sha256Hex(beforeDowngrade[paths.Snapshot]) == verifiedJournal.TargetSnapshotDigest || sha256Hex(beforeDowngrade[paths.Sessions]) == verifiedJournal.TargetSessionsDigest {
		t.Fatal("evolved state unexpectedly equals the initial activation postimage")
	}
	if err := strideE10W4RollbackActivation(context.Background(), paths, keys); !errors.Is(err, ErrStrideE10Conflict) {
		t.Fatalf("evolved live state entered initial-failure rollback: %v", err)
	}
	t.Setenv("BONFIRE_RELEASE_COMMIT", strings.Repeat("b", 40))
	t.Setenv(strideE10W4ModeEnv, strideE10W4CanaryMode)
	if err := installStrideE10W4ProductionRuntimeFromEnvironment(); err != nil {
		t.Fatalf("evolved v2 canary install: %v", err)
	}
	for path, want := range beforeDowngrade {
		got, readErr := os.ReadFile(path)
		if readErr != nil || !bytes.Equal(got, want) {
			t.Fatalf("live-to-canary mutated %s: %v", path, readErr)
		}
	}
	afterDowngradeJournal, err := strideE10W4LoadJournal(paths.Journal, keys)
	if err != nil || afterDowngradeJournal.Phase != strideE10W4ActivationCommitted {
		t.Fatalf("live-to-canary invoked rollback journal=%+v err=%v", afterDowngradeJournal, err)
	}
	if strideE10LiveProductRuntime.Enabled(STRIDEFeatureContributionReview) || strideE10LiveProductRuntime.Enabled(STRIDEFeatureNetworkProfilePublication) || strideE10LiveProductRuntime.Enabled(STRIDEFeatureNetworkSearch) || strideE10LiveProductRuntime.Enabled(STRIDEFeatureNetworkContact) {
		t.Fatal("canary downgrade enabled live-only or W6 features")
	}
	t.Setenv(strideE10W4ModeEnv, strideE10W4NetworkMode)
	if err := installStrideE10W4ProductionRuntimeFromEnvironment(); err != nil {
		t.Fatal(err)
	}
	if strideE10LiveProductRuntime.Enabled(STRIDEFeatureNetworkProfilePublication) || strideE10LiveProductRuntime.Enabled(STRIDEFeatureNetworkSearch) || strideE10LiveProductRuntime.Enabled(STRIDEFeatureNetworkContact) {
		t.Fatal("W4 live enabled W6 publication/discovery/contact")
	}
	if err := strideE10LiveProductRuntime.persistRuntime(strideE10LiveProductRuntime); err != nil {
		t.Fatal(err)
	}
	_, persistedEnvelope, err := loadStrideE10W4SnapshotEnvelope(paths.Snapshot, keys)
	if err != nil || persistedEnvelope.Generation <= envelope.Generation || persistedEnvelope.ActivationID != envelope.ActivationID || persistedEnvelope.ActivationReceiptDigest != envelope.ActivationReceiptDigest {
		t.Fatalf("persisted lineage=%+v err=%v", persistedEnvelope, err)
	}
	correctSnapshot, _ := os.ReadFile(paths.Snapshot)
	wrongLineage := strings.Repeat("c", 64)
	currentSnapshot, _, _ := loadStrideE10W4SnapshotEnvelope(paths.Snapshot, keys)
	if err := writeStrideE10W4RuntimeSnapshotWithLineage(paths.Snapshot, persistedEnvelope.Generation+1, keys, runtimeFromStrideE10W4Snapshot(currentSnapshot, newStrideE10MemoryOperationStore()), envelope.ActivationID, wrongLineage); err != nil {
		t.Fatal(err)
	}
	if _, _, err := strideE10W4VerifySuccessorRuntime(paths, keys, strings.Repeat("b", 40)); !errors.Is(err, ErrStrideE10Denied) {
		t.Fatalf("tampered lineage successor verification err=%v", err)
	}
	if err := installStrideE10W4ProductionRuntimeFromEnvironment(); !errors.Is(err, ErrStrideE10Denied) {
		t.Fatalf("tampered lineage startup err=%v", err)
	}
	_ = writeFileAtomicallyDurable(paths.Snapshot, correctSnapshot, 0o600)
	sessions[newHash] = sessionRecord{Email: "successor@example.com", Expires: time.Now().UTC().Add(time.Hour), PersonID: person.Header.ID, AccountSubjectDigest: person.AccountSubjectDigest, AuthorityGeneration: 2, ActiveOrganizationID: "organization_wrong", OrganizationMembershipID: "membership_wrong", OrganizationMembershipRev: 1, ActiveOrganizationSessionRev: 1}
	unbound, _ := json.MarshalIndent(sessions, "", "  ")
	_ = writeFileAtomicallyDurable(paths.Sessions, unbound, 0o600)
	if _, _, err := strideE10W4VerifySuccessorRuntime(paths, keys, strings.Repeat("b", 40)); !errors.Is(err, ErrStrideE10Denied) {
		t.Fatalf("unbound session successor verification err=%v", err)
	}
	if err := installStrideE10W4ProductionRuntimeFromEnvironment(); !errors.Is(err, ErrStrideE10Denied) {
		t.Fatalf("unbound session startup err=%v", err)
	}
	_ = writeFileAtomicallyDurable(paths.Sessions, updatedSessions, 0o600)
	validSnapshot, validEnvelope, _ := loadStrideE10W4SnapshotEnvelope(paths.Snapshot, keys)
	for id, profile := range validSnapshot.Network.Profiles {
		profile.State, profile.Discoverability = "published", "signed_in_network"
		validSnapshot.Network.Profiles[id] = profile
		break
	}
	malformedRuntime := runtimeFromStrideE10W4Snapshot(validSnapshot, newStrideE10MemoryOperationStore())
	if err := writeStrideE10W4RuntimeSnapshotWithLineage(paths.Snapshot, validEnvelope.Generation+1, keys, malformedRuntime, validEnvelope.ActivationID, validEnvelope.ActivationReceiptDigest); err != nil {
		t.Fatal(err)
	}
	if err := installStrideE10W4ProductionRuntimeFromEnvironment(); !errors.Is(err, ErrStrideE10Denied) {
		t.Fatalf("published/private-canary state startup err=%v", err)
	}
}

func TestStrideE10W4CanaryLoadsV1SnapshotWithoutMutationAndRestarts(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BONFIRE_USERS_PATH", filepath.Join(dir, "users.json"))
	t.Setenv("BONFIRE_SESSIONS_PATH", filepath.Join(dir, "sessions.json"))
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
	keys := strideE10W4TestKeyring()
	payload, _ := json.Marshal(snapshot)
	v1 := strideE10W4SnapshotEnvelope{SchemaVersion: 1, Generation: 1, KeyID: keys.key.ID, KeyVersion: keys.key.Version, Payload: payload}
	v1.MAC = strideE10W4SnapshotMACV1(keys.key, v1.Generation, payload)
	body, _ := json.MarshalIndent(v1, "", "  ")
	path := filepath.Join(dir, "runtime-v1.json")
	if err := writeFileAtomicallyDurable(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	paths := strideE10W4ActivationPaths{Snapshot: path, Sessions: filepath.Join(dir, "sessions.json"), BackupDir: filepath.Join(dir, "backup"), Receipt: filepath.Join(dir, "receipt.json"), Journal: filepath.Join(dir, "backup", "activation.journal.json")}
	t.Setenv(strideE10W4ModeEnv, strideE10W4CanaryMode)
	strideE10W4SetRuntimeTestEnvironment(t, paths, keys, filepath.Join(dir, "operations.json"))
	isolateStrideE10W4ReadinessForTest(t)
	prior := strideE10LiveProductRuntime
	t.Cleanup(func() {
		strideE10LiveProductRuntime = prior
	})
	if err := installStrideE10W4ProductionRuntimeFromEnvironment(); err != nil {
		t.Fatal(err)
	}
	afterFirstStart, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(afterFirstStart, body) {
		t.Fatalf("first-hop canary rewrote retained-compatible v1 snapshot: %v", err)
	}
	_, loaded, err := loadStrideE10W4SnapshotEnvelope(path, keys)
	if err != nil || loaded.SchemaVersion != 1 || loaded.ActivationID != "" {
		t.Fatalf("loaded=%+v err=%v", loaded, err)
	}
	personID := ""
	for id := range snapshot.Organization.Persons {
		personID = id
		break
	}
	principal := StrideE10ProductPrincipal{PersonID: personID}
	serverMintedAction := func(surface, action string) (string, int64) {
		t.Helper()
		value, _, executeErr := strideE10LiveProductRuntime.Execute(context.Background(), principal, StrideE10ProductCommand{Operation: "identity.self_profile", Method: http.MethodGet, Path: "/api/stride/v1/mobile/surfaces/" + surface, ResourceID: surface})
		if executeErr != nil {
			t.Fatalf("GET %s: %v", surface, executeErr)
		}
		encoded, _ := json.Marshal(value)
		var projection struct {
			Items []struct {
				Actions []struct {
					ID               string `json:"id"`
					Type             string `json:"type"`
					ExpectedRevision int64  `json:"expectedRevision"`
				} `json:"actions"`
			} `json:"items"`
		}
		if json.Unmarshal(encoded, &projection) != nil {
			t.Fatalf("invalid %s projection", surface)
		}
		for _, item := range projection.Items {
			for _, candidate := range item.Actions {
				if candidate.Type == action {
					return candidate.ID, candidate.ExpectedRevision
				}
			}
		}
		t.Fatalf("%s did not mint %s", surface, action)
		return "", 0
	}
	executeAction := func(surface, action, id string, revision int64, values map[string]any) {
		t.Helper()
		actionBody, _ := json.Marshal(map[string]any{"action": action, "surface": surface, "expectedRevision": revision, "values": values})
		if _, _, executeErr := strideE10LiveProductRuntime.Execute(context.Background(), principal, StrideE10ProductCommand{Operation: "identity.self_profile", Method: http.MethodPost, Path: "/api/stride/v1/mobile/actions/" + id, ResourceID: id, ExpectedRevision: revision, IdempotencyKey: "v1-" + action, Body: actionBody}); executeErr != nil {
			t.Fatalf("POST %s: %v", action, executeErr)
		}
	}
	profileAction, profileRevision := serverMintedAction("profile", "profile-update")
	executeAction("profile", "profile-update", profileAction, profileRevision, map[string]any{"displayName": "Retained Compatible"})
	organizationAction, organizationRevision := serverMintedAction("organizations", "organization-create")
	executeAction("organizations", "organization-create", organizationAction, organizationRevision, map[string]any{"name": "Second Organization", "slug": "second-organization"})
	persisted, persistedEnvelope, err := loadStrideE10W4SnapshotEnvelope(path, keys)
	if err != nil || persistedEnvelope.SchemaVersion != 1 || persistedEnvelope.ActivationID != "" || persistedEnvelope.ActivationReceiptDigest != "" || persistedEnvelope.Generation <= loaded.Generation || len(persisted.Organization.Organizations) != 2 {
		t.Fatalf("v1 canary mutation persistence envelope=%+v organizations=%d err=%v", persistedEnvelope, len(persisted.Organization.Organizations), err)
	}
	beforeRestart, _ := os.ReadFile(path)
	if err := installStrideE10W4ProductionRuntimeFromEnvironment(); err != nil {
		t.Fatalf("v1 canary restart: %v", err)
	}
	afterRestart, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(afterRestart, beforeRestart) {
		t.Fatalf("v1 canary restart changed persisted retained-compatible snapshot: %v", err)
	}
	_, restartedEnvelope, err := loadStrideE10W4SnapshotEnvelope(path, keys)
	if err != nil || restartedEnvelope.SchemaVersion != 1 || restartedEnvelope.ActivationID != "" || restartedEnvelope.Generation != persistedEnvelope.Generation {
		t.Fatalf("v1 mutation restart envelope=%+v err=%v", restartedEnvelope, err)
	}
}
