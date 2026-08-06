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
	if len(migrations) != 13 {
		t.Fatalf("migration count = %d, want 13", len(migrations))
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
	if migrations[7].Version != 8 || migrations[7].Name != "0008_stride_contracts.sql" ||
		migrations[7].SHA256 != sha256.Sum256([]byte(migrations[7].SQL)) ||
		!strings.Contains(migrations[7].SQL, "CREATE FUNCTION stride_jsonb_has_forbidden_key") ||
		strings.Count(migrations[7].SQL, "CHECK (NOT stride_jsonb_has_forbidden_key") != 2 ||
		!strings.Contains(migrations[7].SQL, "CREATE TABLE stride_contract_revisions") ||
		!strings.Contains(migrations[7].SQL, "CREATE TABLE stride_registry_revisions") ||
		!strings.Contains(migrations[7].SQL, "CREATE TABLE stride_feature_switches") ||
		!strings.Contains(migrations[7].SQL, "CREATE TABLE stride_source_derived_edges") {
		t.Fatalf("unexpected STRIDE contracts migration: %+v", migrations[7])
	}
	if migrations[8].Version != 9 || migrations[8].Name != "0009_stride_conversation_ledger.sql" ||
		migrations[8].SHA256 != sha256.Sum256([]byte(migrations[8].SQL)) ||
		!strings.Contains(migrations[8].SQL, "CREATE FUNCTION stride_structured_refs_are_valid") ||
		!strings.Contains(migrations[8].SQL, "CHECK (stride_structured_refs_are_valid(structured_refs))") ||
		!strings.Contains(migrations[8].SQL, "CREATE TABLE stride_conversation_events") ||
		!strings.Contains(migrations[8].SQL, "CREATE TABLE stride_conversation_projection_checkpoints") ||
		!strings.Contains(migrations[8].SQL, "CREATE TABLE stride_conversation_derived_edges") ||
		!strings.Contains(migrations[8].SQL, "recall_eligible boolean NOT NULL DEFAULT false CHECK (recall_eligible = false)") {
		t.Fatalf("unexpected STRIDE conversation ledger migration: %+v", migrations[8])
	}
	if migrations[9].Version != 10 || migrations[9].Name != "0010_stride_person_mymind.sql" ||
		migrations[9].SHA256 != sha256.Sum256([]byte(migrations[9].SQL)) ||
		!strings.Contains(migrations[9].SQL, "CREATE TABLE stride_person_principals") ||
		!strings.Contains(migrations[9].SQL, "CREATE TABLE stride_workspace_memberships") ||
		!strings.Contains(migrations[9].SQL, "CREATE TABLE stride_mymind_sources") ||
		!strings.Contains(migrations[9].SQL, "CREATE TABLE stride_mymind_disclosure_grants") ||
		!strings.Contains(migrations[9].SQL, "CREATE TABLE stride_mymind_authority_grants") ||
		!strings.Contains(migrations[9].SQL, "CREATE TABLE stride_mymind_custody_deletion_receipts") ||
		!strings.Contains(migrations[9].SQL, "CREATE TABLE stride_mymind_custody_deletion_items") ||
		!strings.Contains(migrations[9].SQL, "CREATE TABLE stride_mymind_export_receipts") ||
		!strings.Contains(migrations[9].SQL, "stride_person_delete_has_exact_custody_receipt") ||
		!strings.Contains(migrations[9].SQL, "destination_audience_id NOT IN ('', 'organization', 'workspace')") ||
		!strings.Contains(migrations[9].SQL, "VALUES ('person_mymind_context', false, 1)") {
		t.Fatalf("unexpected STRIDE person/MyMind migration: %+v", migrations[9])
	}
	if migrations[10].Version != 11 || migrations[10].Name != "0011_ambient_intelligence_replay.sql" ||
		migrations[10].SHA256 != sha256.Sum256([]byte(migrations[10].SQL)) ||
		!strings.Contains(migrations[10].SQL, "CREATE TABLE ambient_intelligence_replay_manifests") ||
		!strings.Contains(migrations[10].SQL, "CREATE TABLE ambient_intelligence_replay_sources") ||
		!strings.Contains(migrations[10].SQL, "CREATE TABLE ambient_intelligence_replay_stage_receipts") ||
		!strings.Contains(migrations[10].SQL, "idempotency_key bytea NOT NULL") ||
		!strings.Contains(migrations[10].SQL, "lease_expires_at timestamptz") ||
		!strings.Contains(migrations[10].SQL, "CHECK (stage <> 'board')") {
		t.Fatalf("unexpected ambient replay migration: %+v", migrations[10])
	}
	if migrations[11].Version != 12 || migrations[11].Name != "0012_artifact_dispositions.sql" ||
		migrations[11].SHA256 != sha256.Sum256([]byte(migrations[11].SQL)) ||
		!strings.Contains(migrations[11].SQL, "CREATE TABLE stride_artifact_disposition_states") ||
		!strings.Contains(migrations[11].SQL, "CREATE TABLE stride_artifact_discard_confirmations") ||
		!strings.Contains(migrations[11].SQL, "CREATE TABLE stride_artifact_disposition_receipts") {
		t.Fatalf("unexpected artifact disposition migration: %+v", migrations[11])
	}
	if migrations[12].Version != 13 || migrations[12].Name != "0013_ambient_mind_projections.sql" ||
		migrations[12].SHA256 != sha256.Sum256([]byte(migrations[12].SQL)) ||
		!strings.Contains(migrations[12].SQL, "CREATE TABLE stride_ambient_projection_events") ||
		!strings.Contains(migrations[12].SQL, "CREATE TABLE stride_ambient_projection_sources") ||
		!strings.Contains(migrations[12].SQL, "CREATE TABLE stride_ambient_projection_nodes") ||
		!strings.Contains(migrations[12].SQL, "CREATE TABLE stride_ambient_projection_source_edges") ||
		!strings.Contains(migrations[12].SQL, "CREATE TABLE stride_ambient_projection_node_edges") ||
		!strings.Contains(migrations[12].SQL, "CREATE TABLE stride_ambient_projection_node_states") ||
		!strings.Contains(migrations[12].SQL, "CREATE TABLE stride_ambient_projection_checkpoints") ||
		!strings.Contains(migrations[12].SQL, "VALUES ('ambient_mind_projection_shadow',false,1)") {
		t.Fatalf("unexpected AmbientMind projection migration: %+v", migrations[12])
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
