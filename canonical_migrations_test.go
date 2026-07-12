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
	if len(migrations) != 1 {
		t.Fatalf("migration count = %d, want 1", len(migrations))
	}
	migration := migrations[0]
	if migration.Version != 1 || migration.Name != "0001_canonical.sql" {
		t.Fatalf("unexpected migration identity: %#v", migration)
	}
	if migration.SHA256 != sha256.Sum256([]byte(migration.SQL)) {
		t.Fatal("migration checksum does not match embedded SQL")
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
