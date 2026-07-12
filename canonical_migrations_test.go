package main

import (
	"crypto/sha256"
	"strings"
	"testing"
)

func TestLoadCanonicalMigrations(t *testing.T) {
	migrations, err := loadCanonicalMigrations()
	if err != nil {
		t.Fatalf("load migrations: %v", err)
	}
	if len(migrations) != 3 {
		t.Fatalf("migration count = %d, want 3", len(migrations))
	}
	migration := migrations[0]
	if migration.Version != 1 || migration.Name != "0001_canonical.sql" {
		t.Fatalf("unexpected migration identity: %#v", migration)
	}
	if migration.SHA256 != sha256.Sum256([]byte(migration.SQL)) {
		t.Fatal("migration checksum does not match embedded SQL")
	}
	if migrations[1].Version != 2 || migrations[1].Name != "0002_approval_repository.sql" || migrations[1].SHA256 != sha256.Sum256([]byte(migrations[1].SQL)) {
		t.Fatalf("unexpected approval repository migration: %+v", migrations[1])
	}
	if migrations[2].Version != 3 || migrations[2].Name != "0003_purge_ledger.sql" || migrations[2].SHA256 != sha256.Sum256([]byte(migrations[2].SQL)) || !strings.Contains(migrations[2].SQL, "CREATE TABLE purge_ledger") {
		t.Fatalf("unexpected purge ledger migration: %+v", migrations[2])
	}
	for _, marker := range []string{
		"CREATE TABLE canonical_events",
		"CREATE TABLE object_grants",
		"CREATE TABLE approvals",
		"CREATE TABLE consent_records",
		"CREATE TABLE jobs",
		"CREATE TABLE migration_epochs",
		"CREATE TABLE legacy_object_versions",
	} {
		if !strings.Contains(migration.SQL, marker) {
			t.Fatalf("migration missing %q", marker)
		}
	}
}
