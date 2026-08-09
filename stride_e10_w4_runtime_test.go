package main

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func strideE10W4TestKeyring() *strideE10W4Keyring {
	return &strideE10W4Keyring{key: StrideE10MigrationMACKey{ID: "w4-test-key", Version: 1, Secret: []byte(strings.Repeat("k", 32))}}
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
	prior := strideE10LiveProductRuntime
	strideE10W4RuntimeState.Lock()
	strideE10W4RuntimeState.ready = false
	strideE10W4RuntimeState.Unlock()
	t.Cleanup(func() {
		strideE10LiveProductRuntime = prior
		strideE10W4RuntimeState.Lock()
		strideE10W4RuntimeState.ready = false
		strideE10W4RuntimeState.Unlock()
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
	if !ok || record.PersonID == "" || record.ActiveOrganizationID != "" || record.AuthorityGeneration != 1 {
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
