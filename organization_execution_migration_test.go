package main

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestOrganizationExecutionSignedMigrationMapsOpaqueOrganization(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	accounts, sessions := strideE10TestAuthorities(t, dir)
	keys := &strideE10W4Keyring{key: StrideE10MigrationMACKey{ID: "migration-execution-test", Version: 1, Secret: []byte(strings.Repeat("m", 32))}}
	config := strideE10TestConfig(dir, NewStrideE10LocalMigrationSource(accounts, sessions), &strideE10TestWriter{before: 100, after: 115}, keys)
	result, err := RunStrideE10MigrationRehearsal(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	runtime := NewStrideE10ProductLiveRuntime(time.Now)
	runtime.organization, err = strideE10W4OrganizationFromMigration(result.PrivateManifest, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(strideE10W4StatePathEnv, config.StatePath)
	t.Setenv("BONFIRE_CANONICAL_TENANT_ID", "bonfire")
	t.Setenv("BONFIRE_TENANT_ID", "")
	if organizationExecutionTenantMatches(runtime, result.Receipt.OrganizationID) {
		t.Fatal("opaque organization accepted without signed mapping")
	}
	if err = bindOrganizationExecutionMigration(ctx, runtime, keys); err != nil {
		t.Fatal(err)
	}
	if !organizationExecutionTenantMatches(runtime, result.Receipt.OrganizationID) {
		t.Fatal("valid signed migrated organization rejected")
	}
	for _, id := range []string{"bonfire", "organization_other", ""} {
		if organizationExecutionTenantMatches(runtime, id) {
			t.Fatal("unmapped organization accepted", id)
		}
	}
	t.Setenv("BONFIRE_TENANT_ID", "other")
	if organizationExecutionTenantMatches(runtime, result.Receipt.OrganizationID) {
		t.Fatal("retargeted data store accepted")
	}
	t.Setenv("BONFIRE_TENANT_ID", "")
	raw, err := os.ReadFile(config.StatePath)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(config.StatePath, append(raw, []byte("tampered")...), 0600); err != nil {
		t.Fatal(err)
	}
	fresh := NewStrideE10ProductLiveRuntime(time.Now)
	fresh.organization = runtime.organization
	if bindOrganizationExecutionMigration(ctx, fresh, keys) == nil {
		t.Fatal("tampered state accepted")
	}
	t.Setenv(strideE10W4StatePathEnv, config.PublicReceiptPath)
	if bindOrganizationExecutionMigration(ctx, fresh, keys) == nil {
		t.Fatal("public receipt substituted for private migration state")
	}
}

func TestOrganizationExecutionMappedAliasStillChecksCurrentMembership(t *testing.T) {
	f := newOrganizationExecutionFixture(t)
	t.Setenv("BONFIRE_CANONICAL_TENANT_ID", "bonfire")
	t.Setenv("BONFIRE_TENANT_ID", "")
	f.runtime.legacyExecutionBinding = &organizationExecutionLegacyBinding{OrganizationID: "organization_1", CanonicalTenantID: "bonfire", ArtifactTenantID: "bonfire", MigrationDigest: strings.Repeat("a", 64)}
	if _, err := organizationExecutionScopeForSession(context.Background(), f.hash); err != nil {
		t.Fatal("mapped authorized session denied", err)
	}
	f.bind(2, 2)
	if _, err := organizationExecutionScopeForSession(context.Background(), f.hash); err == nil {
		t.Fatal("foreign org accepted through mapping")
	}
	f.bind(1, 3)
	f.runtime.organization.mu.Lock()
	member := f.runtime.organization.memberships["membership_1_owner"]
	member.Status = "revoked"
	f.runtime.organization.memberships[member.Header.ID] = member
	f.runtime.organization.mu.Unlock()
	if _, err := organizationExecutionScopeForSession(context.Background(), f.hash); err == nil {
		t.Fatal("revoked membership accepted through mapping")
	}
}
