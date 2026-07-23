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
	if len(migrations) != 7 {
		t.Fatalf("migration count = %d, want 7", len(migrations))
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
	if migrations[3].Version != 4 || migrations[3].Name != "0004_brain_projection_checkpoints.sql" || migrations[3].SHA256 != sha256.Sum256([]byte(migrations[3].SQL)) || !strings.Contains(migrations[3].SQL, "CREATE TABLE brain_projection_checkpoints") {
		t.Fatalf("unexpected brain projection checkpoint migration: %+v", migrations[3])
	}
	if len(migrations) < 5 || migrations[4].Version != 5 || migrations[4].Name != "0005_purge_ledger_object_type.sql" ||
		migrations[4].SHA256 != sha256.Sum256([]byte(migrations[4].SQL)) || !strings.Contains(migrations[4].SQL, "ADD COLUMN object_type") {
		t.Fatalf("unexpected purge family migration: %+v", migrations)
	}
	if len(migrations) < 6 || migrations[5].Version != 6 || migrations[5].Name != "0006_brain_projection_work.sql" ||
		migrations[5].SHA256 != sha256.Sum256([]byte(migrations[5].SQL)) || !strings.Contains(migrations[5].SQL, "CREATE TABLE brain_projection_work") {
		t.Fatalf("unexpected projection work migration: %+v", migrations)
	}
	if migrations[6].Version != 7 || migrations[6].Name != "0007_catch_up_publications.sql" ||
		migrations[6].SHA256 != sha256.Sum256([]byte(migrations[6].SQL)) || !strings.Contains(migrations[6].SQL, "CREATE TABLE catch_up_publications") ||
		!strings.Contains(migrations[6].SQL, "authority_sha256") || !strings.Contains(migrations[6].SQL, "retain_until") ||
		!strings.Contains(migrations[6].SQL, "push_dispatched_at") || !strings.Contains(migrations[6].SQL, "redacted_at") {
		t.Fatalf("unexpected catch-up publication migration: %+v", migrations[6])
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
