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
	if len(migrations) != 18 {
		t.Fatalf("migration count = %d, want 18", len(migrations))
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
	if migrations[13].Version != 14 || migrations[13].Name != "0014_stride_people_organizations.sql" ||
		migrations[13].SHA256 != sha256.Sum256([]byte(migrations[13].SQL)) ||
		!strings.Contains(migrations[13].SQL, "CREATE TABLE stride_account_person_mappings") ||
		!strings.Contains(migrations[13].SQL, "CREATE TABLE stride_account_person_mappings_current") ||
		!strings.Contains(migrations[13].SQL, "CREATE TABLE stride_person_profile_revisions") ||
		!strings.Contains(migrations[13].SQL, "CREATE TABLE stride_organizations") ||
		!strings.Contains(migrations[13].SQL, "CREATE TABLE stride_organization_membership_revisions") ||
		!strings.Contains(migrations[13].SQL, "CREATE TABLE stride_organization_memberships_current") ||
		!strings.Contains(migrations[13].SQL, "CREATE TABLE stride_organization_join_requests") ||
		!strings.Contains(migrations[13].SQL, "CREATE TABLE stride_active_organization_sessions") ||
		!strings.Contains(migrations[13].SQL, "CREATE TABLE stride_organization_audit_events") ||
		!strings.Contains(migrations[13].SQL, "CREATE TABLE stride_organization_member_profile_revisions") ||
		!strings.Contains(migrations[13].SQL, "CREATE FUNCTION stride_lock_and_assign_organization_slot") ||
		!strings.Contains(migrations[13].SQL, "CREATE FUNCTION stride_require_active_organization_owner") ||
		!strings.Contains(migrations[13].SQL, "CREATE UNIQUE INDEX stride_account_person_one_active_person") ||
		!strings.Contains(migrations[13].SQL, "VALUES\n    ('person_profile_authority', false, 1)") {
		t.Fatalf("unexpected STRIDE people/organizations migration: %+v", migrations[13])
	}
	if migrations[14].Version != 15 || migrations[14].Name != "0015_stride_contributions.sql" ||
		migrations[14].SHA256 != sha256.Sum256([]byte(migrations[14].SQL)) ||
		!strings.Contains(migrations[14].SQL, "CREATE TABLE stride_contribution_claim_revisions") ||
		!strings.Contains(migrations[14].SQL, "CREATE TABLE stride_field_release_approval_revisions") ||
		!strings.Contains(migrations[14].SQL, "CREATE TABLE stride_contribution_attestation_revisions") ||
		!strings.Contains(migrations[14].SQL, "CREATE TABLE stride_published_contribution_revisions") ||
		!strings.Contains(migrations[14].SQL, "CREATE TABLE stride_agent_influence_receipt_revisions") ||
		!strings.Contains(migrations[14].SQL, "CREATE FUNCTION stride_validate_contribution_claim_current") ||
		!strings.Contains(migrations[14].SQL, "('contribution_candidate_detection',false,1)") {
		t.Fatalf("unexpected STRIDE contributions migration: %+v", migrations[14])
	}
	if migrations[15].Version != 16 || migrations[15].Name != "0016_stride_network.sql" ||
		migrations[15].SHA256 != sha256.Sum256([]byte(migrations[15].SQL)) ||
		!strings.Contains(migrations[15].SQL, "CREATE TABLE stride_network_profile_revisions") ||
		!strings.Contains(migrations[15].SQL, "CREATE TABLE stride_talent_search_grant_revisions") ||
		!strings.Contains(migrations[15].SQL, "CREATE TABLE stride_network_search_receipts") ||
		!strings.Contains(migrations[15].SQL, "CREATE TABLE stride_contact_request_revisions") ||
		!strings.Contains(migrations[15].SQL, "CREATE TABLE stride_network_blocks_revisions") ||
		!strings.Contains(migrations[15].SQL, "CREATE TABLE stride_network_rate_breadth_accounting") ||
		!strings.Contains(migrations[15].SQL, "CREATE TABLE stride_derived_purge_receipts") ||
		!strings.Contains(migrations[15].SQL, "('network_profile_publication',false,1)") {
		t.Fatalf("unexpected STRIDE network migration: %+v", migrations[15])
	}
	if migrations[16].Version != 17 || migrations[16].Name != "0017_stride_mymind_private_custody.sql" ||
		migrations[16].SHA256 != sha256.Sum256([]byte(migrations[16].SQL)) ||
		!strings.Contains(migrations[16].SQL, "CREATE TABLE stride_mymind_private_custody_envelopes") ||
		!strings.Contains(migrations[16].SQL, "CREATE TABLE stride_mymind_private_operation_receipts") ||
		!strings.Contains(migrations[16].SQL, "CREATE TABLE stride_mymind_private_deletion_journals") ||
		!strings.Contains(migrations[16].SQL, "CREATE TABLE stride_mymind_private_key_destruction_receipts") ||
		!strings.Contains(migrations[16].SQL, "CREATE TABLE stride_mymind_private_source_tombstones") ||
		!strings.Contains(migrations[16].SQL, "CREATE TABLE stride_mymind_private_state_high_water") ||
		!strings.Contains(migrations[16].SQL, "stride_mymind_private_mutation_requires_current_authority") ||
		!strings.Contains(migrations[16].SQL, "UNIQUE (key_id, key_version, nonce)") ||
		!strings.Contains(migrations[16].SQL, "stride_mymind_private_operation_receipt_immutable") ||
		!strings.Contains(migrations[16].SQL, "stride_mymind_private_exact_destruction_receipt") ||
		!strings.Contains(migrations[16].SQL, "stride_mymind_private_destruction_receipt_verified") ||
		!strings.Contains(migrations[16].SQL, "source_envelope_digest") ||
		!strings.Contains(migrations[16].SQL, "verification_contract") ||
		!strings.Contains(migrations[16].SQL, "destruction_operation_id text NOT NULL UNIQUE") ||
		!strings.Contains(migrations[16].SQL, "receipt.destruction_operation_id <> NEW.destruction_operation_id") ||
		!strings.Contains(migrations[16].SQL, "stride_mymind_private_high_water_monotonic") ||
		!strings.Contains(migrations[16].SQL, "source_kind IN ('preference','reflection','correction')") ||
		!strings.Contains(migrations[16].SQL, "key_refs jsonb NOT NULL") ||
		!strings.Contains(migrations[16].SQL, "VALUES ('person_mymind_context', false, 1)") {
		t.Fatalf("unexpected private MyMind custody migration: %+v", migrations[16])
	}
	if migrations[17].Version != 18 || migrations[17].Name != "0018_stride_projects.sql" ||
		migrations[17].SHA256 != sha256.Sum256([]byte(migrations[17].SQL)) ||
		!strings.Contains(migrations[17].SQL, "CREATE TABLE stride_project_revisions") ||
		!strings.Contains(migrations[17].SQL, "ADD COLUMN authority_generation") ||
		!strings.Contains(migrations[17].SQL, "Project authority generation must advance exactly once") ||
		!strings.Contains(migrations[17].SQL, "CREATE TABLE stride_projects_current") ||
		!strings.Contains(migrations[17].SQL, "CREATE TABLE stride_project_thread_binding_revisions") ||
		!strings.Contains(migrations[17].SQL, "CREATE UNIQUE INDEX stride_project_thread_one_active_owner") ||
		!strings.Contains(migrations[17].SQL, "CREATE TABLE stride_project_association_revisions") ||
		!strings.Contains(migrations[17].SQL, "CREATE TABLE stride_project_association_events") ||
		!strings.Contains(migrations[17].SQL, "CREATE TABLE stride_project_correction_receipts") ||
		!strings.Contains(migrations[17].SQL, "CREATE TABLE stride_project_operation_receipts") ||
		!strings.Contains(migrations[17].SQL, "CREATE TABLE stride_project_source_authority_receipts") ||
		!strings.Contains(migrations[17].SQL, "CREATE TABLE stride_project_projection_outbox") ||
		!strings.Contains(migrations[17].SQL, "CREATE VIEW stride_project_associations_authorized_current") ||
		!strings.Contains(migrations[17].SQL, "CREATE FUNCTION stride_project_source_drift_unlist") ||
		!strings.Contains(migrations[17].SQL, "UNIQUE(organization_id,operation_kind,idempotency_key_digest)") ||
		!strings.Contains(migrations[17].SQL, "Project operation requires current organization session authority") ||
		!strings.Contains(migrations[17].SQL, "ProjectAssociation requires exact current source authority receipt") ||
		!strings.Contains(migrations[17].SQL, "Project source receipt requires exact authorized canonical conversation event") ||
		!strings.Contains(migrations[17].SQL, "FOREIGN KEY(organization_id,subject_id) REFERENCES stride_conversation_events") ||
		!strings.Contains(migrations[17].SQL, "source_event.invalidated_at IS NOT NULL") ||
		!strings.Contains(migrations[17].SQL, "ProjectAssociation current revision requires authority event") ||
		!strings.Contains(migrations[17].SQL, "Project correction receipt does not bind exact old and replacement current edges") ||
		!strings.Contains(migrations[17].SQL, "Project controller membership must belong to Project organization") ||
		!strings.Contains(migrations[17].SQL, "supersede exact current digest") ||
		!strings.Contains(migrations[17].SQL, "illegal ProjectAssociation transition or resurrection") ||
		!strings.Contains(migrations[17].SQL, "('project_authority_read',false,1)") ||
		!strings.Contains(migrations[17].SQL, "('project_record_projection',false,1)") {
		t.Fatalf("unexpected STRIDE Projects migration: %+v", migrations[17])
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

func TestPostgresStridePeopleOrganizationsMigrationEnforcesAuthoritySeams(t *testing.T) {
	ctx, store, _ := migratedPostgresCanonicalStore(t)

	for i, personID := range []string{"person-1", "person-2", "person-3", "person-4", "person-5", "person-6"} {
		hexDigit := string("abcdef"[i])
		if _, err := store.pool.Exec(ctx, `INSERT INTO stride_person_principals
			(person_id, revision, account_subject_digest, status, recovery_revision, custody_revision, created_at)
			VALUES ($1, 1, decode(repeat($2, 64), 'hex'), 'active', 1, 1, now())`, personID, hexDigit); err != nil {
			t.Fatalf("insert %s: %v", personID, err)
		}
	}

	createOrganization := func(organizationID, ownerPersonID string) {
		t.Helper()
		tx, err := store.pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		membershipID := "owner-" + organizationID
		if _, err = tx.Exec(ctx, `INSERT INTO stride_organizations
			(organization_id, revision, name, slug, status, creator_person_id, created_at, updated_at)
			VALUES ($1, 1, $1, $1, 'active', $2, now(), now())`, organizationID, ownerPersonID); err != nil {
			t.Fatalf("insert organization %s: %v", organizationID, err)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO stride_organization_membership_revisions
			(membership_id, revision, organization_id, person_id, role, status, granted_at, created_at, created_by_person_id)
			VALUES ($1, 1, $2, $3, 'owner', 'active', now(), now(), $3)`, membershipID, organizationID, ownerPersonID); err != nil {
			t.Fatalf("insert owner revision %s: %v", organizationID, err)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO stride_organization_memberships_current
			(membership_id, revision, organization_id, person_id, role, status, updated_at)
			VALUES ($1, 1, $2, $3, 'owner', 'active', now())`, membershipID, organizationID, ownerPersonID); err != nil {
			t.Fatalf("insert owner current %s: %v", organizationID, err)
		}
		if err = tx.Commit(ctx); err != nil {
			t.Fatalf("commit organization %s: %v", organizationID, err)
		}
	}
	for i, owner := range []string{"person-2", "person-3", "person-4", "person-5"} {
		createOrganization("org-"+string(rune('1'+i)), owner)
	}
	createOrganization("x", "person-6")
	createOrganization(strings.Repeat("a", 63), "person-6")

	tx, err := store.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO stride_organizations
		(organization_id, revision, name, slug, status, creator_person_id, created_at, updated_at)
		VALUES ('ownerless-org', 1, 'Ownerless', 'ownerless', 'active', 'person-6', now(), now())`); err != nil {
		t.Fatalf("insert deferred ownerless organization: %v", err)
	}
	if err = tx.Commit(ctx); err == nil || !strings.Contains(err.Error(), "requires at least one active owner") {
		t.Fatalf("ownerless organization commit error=%v, want atomic creator-owner rejection", err)
	}

	addMembership := func(membershipID, organizationID, personID, role string) error {
		tx, err := store.pool.Begin(ctx)
		if err != nil {
			return err
		}
		defer tx.Rollback(ctx)
		if _, err = tx.Exec(ctx, `INSERT INTO stride_organization_membership_revisions
			(membership_id, revision, organization_id, person_id, role, status, granted_at, created_at, created_by_person_id)
			VALUES ($1, 1, $2, $3, $4, 'active', now(), now(), $3)`, membershipID, organizationID, personID, role); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO stride_organization_memberships_current
			(membership_id, revision, organization_id, person_id, role, status, updated_at)
			VALUES ($1, 1, $2, $3, $4, 'active', now())`, membershipID, organizationID, personID, role); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	for _, organizationID := range []string{"org-1", "org-2", "org-3"} {
		if err := addMembership("member-"+organizationID, organizationID, "person-1", "member"); err != nil {
			t.Fatalf("add membership %s: %v", organizationID, err)
		}
	}
	if err := addMembership("member-org-4", "org-4", "person-1", "member"); err == nil || !strings.Contains(err.Error(), "three active organizations") {
		t.Fatalf("fourth active organization error=%v, want capacity rejection", err)
	}

	departMembership := func(membershipID, organizationID, personID, role string) error {
		tx, err := store.pool.Begin(ctx)
		if err != nil {
			return err
		}
		defer tx.Rollback(ctx)
		if _, err = tx.Exec(ctx, `INSERT INTO stride_organization_membership_revisions
			(membership_id, revision, organization_id, person_id, role, status, granted_at, ended_at, created_at, created_by_person_id, supersedes_revision)
			SELECT $1, 2, $2, $3, $4, 'departed', granted_at, now(), now(), $3, 1
			FROM stride_organization_membership_revisions WHERE membership_id=$1 AND revision=1`, membershipID, organizationID, personID, role); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE stride_organization_memberships_current
			SET revision=2, status='departed', active_slot=NULL, updated_at=now()
			WHERE membership_id=$1`, membershipID); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	if err := departMembership("member-org-1", "org-1", "person-1", "member"); err != nil {
		t.Fatalf("depart member: %v", err)
	}
	if err := addMembership("member-org-4", "org-4", "person-1", "member"); err != nil {
		t.Fatalf("freed slot was not reusable: %v", err)
	}

	if err := departMembership("owner-org-1", "org-1", "person-2", "owner"); err == nil || !strings.Contains(err.Error(), "requires at least one active owner") {
		t.Fatalf("final-owner departure error=%v, want owner rejection", err)
	}
	if err := addMembership("replacement-owner-org-1", "org-1", "person-6", "owner"); err != nil {
		t.Fatalf("add replacement owner: %v", err)
	}
	if err := departMembership("owner-org-1", "org-1", "person-2", "owner"); err != nil {
		t.Fatalf("owner transfer seam should permit former owner departure: %v", err)
	}

	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_active_organization_sessions
		(session_subject_digest, person_id, organization_id, membership_id, membership_revision, session_revision, status, bound_at, expires_at, updated_at)
		VALUES (decode(repeat('1',64),'hex'), 'person-1', 'org-1', 'member-org-1', 1, 1, 'active', now(), now() + interval '1 hour', now())`); err == nil || !strings.Contains(err.Error(), "exact current membership") {
		t.Fatalf("stale active-session binding error=%v, want exact-current rejection", err)
	}

	tx, err = store.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO stride_organization_join_requests
		(request_id, revision, organization_id, person_id, status, requested_at, decided_at, decided_by_membership_id, decided_by_membership_revision, updated_at)
		VALUES ('request-1', 1, 'org-4', 'person-6', 'denied', now(), now(), 'member-org-4', 1, now())`); err != nil {
		t.Fatalf("insert deferred join decision: %v", err)
	}
	if err = tx.Commit(ctx); err == nil || !strings.Contains(err.Error(), "owner/admin membership") {
		t.Fatalf("member join decision error=%v, want exact admin rejection", err)
	}

	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_organization_audit_events
		(event_id, organization_id, event_type, actor_person_id, new_revision, correlation_digest, idempotency_digest, audit, created_at)
		VALUES ('audit-1', 'org-4', 'switch', 'person-1', 1, decode(repeat('2',64),'hex'), decode(repeat('3',64),'hex'), '{"source":"session"}', now())`); err != nil {
		t.Fatalf("insert body-free audit: %v", err)
	}
	if _, err := store.pool.Exec(ctx, `UPDATE stride_organization_audit_events SET reason_code='rewrite' WHERE event_id='audit-1'`); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("audit update error=%v, want immutable rejection", err)
	}
	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_organization_audit_events
		(event_id, organization_id, event_type, actor_person_id, new_revision, correlation_digest, idempotency_digest, audit, created_at)
		VALUES ('audit-2', 'org-4', 'switch', 'person-1', 1, decode(repeat('4',64),'hex'), decode(repeat('5',64),'hex'), '{"nested":{"email":"raw@example.test"}}', now())`); err == nil {
		t.Fatal("audit accepted a nested raw email")
	}

	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_account_person_mappings
		(mapping_id, revision, account_subject_digest, person_id, status, created_at)
		VALUES ('mapping-1', 1, decode(repeat('7',64),'hex'), 'person-1', 'active', now())`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_account_person_mappings_current
		(mapping_id, revision, account_subject_digest, person_id, status, updated_at)
		VALUES ('mapping-1', 1, decode(repeat('7',64),'hex'), 'person-1', 'active', now())`); err != nil {
		t.Fatal(err)
	}
	tx, err = store.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO stride_account_person_mappings
		(mapping_id, revision, account_subject_digest, person_id, status, created_at)
		VALUES ('mapping-2', 1, decode(repeat('8',64),'hex'), 'person-1', 'active', now())`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO stride_account_person_mappings_current
		(mapping_id, revision, account_subject_digest, person_id, status, updated_at)
		VALUES ('mapping-2', 1, decode(repeat('8',64),'hex'), 'person-1', 'active', now())`); err == nil {
		t.Fatal("person accepted two active account mappings")
	}
	_ = tx.Rollback(ctx)

	var disabledSwitches int
	if err := store.pool.QueryRow(ctx, `SELECT count(*) FROM stride_feature_switches
		WHERE feature_key IN ('person_profile_authority','organization_authority_write','organization_authority_read','active_organization_session') AND enabled=false`).Scan(&disabledSwitches); err != nil || disabledSwitches != 4 {
		t.Fatalf("disabled organization switches=%d err=%v, want 4", disabledSwitches, err)
	}
	var rawEmailColumns int
	if err := store.pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns
		WHERE table_schema='public' AND
		(table_name LIKE 'stride_organization%' OR table_name LIKE 'stride_person_profile%' OR table_name LIKE 'stride_account_person%') AND
		lower(column_name) LIKE '%email%'`).Scan(&rawEmailColumns); err != nil || rawEmailColumns != 0 {
		t.Fatalf("raw-email columns=%d err=%v, want 0", rawEmailColumns, err)
	}
}

func TestPostgresStrideContributionNetworkMigrationsFenceEligibility(t *testing.T) {
	ctx, store, _ := migratedPostgresCanonicalStore(t)
	for personID, hexDigit := range map[string]string{"subject-1": "a", "reviewer-1": "b"} {
		if _, err := store.pool.Exec(ctx, `INSERT INTO stride_person_principals
			(person_id,revision,account_subject_digest,status,recovery_revision,custody_revision,created_at)
			VALUES($1,1,decode(repeat($2,64),'hex'),'active',1,1,now())`, personID, hexDigit); err != nil {
			t.Fatal(err)
		}
	}
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO stride_organizations
		(organization_id,revision,name,slug,status,creator_person_id,created_at,updated_at)
		VALUES('org-contrib',1,'Contributions','contributions','active','reviewer-1',now(),now())`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO stride_organization_membership_revisions
		(membership_id,revision,organization_id,person_id,role,status,granted_at,created_at,created_by_person_id)
		VALUES('owner-contrib',1,'org-contrib','reviewer-1','owner','active',now(),now(),'reviewer-1')`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO stride_organization_memberships_current
		(membership_id,revision,organization_id,person_id,role,status,updated_at)
		VALUES('owner-contrib',1,'org-contrib','reviewer-1','owner','active',now())`); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_contribution_controller_grants
		(grant_id,role,organization_id,person_id,authority_id,authority_revision,policy_revision,active) VALUES
		('grant-org','organization_reviewer','org-contrib','reviewer-1','org-review',1,1,true),
		('grant-subject','subject',NULL,'subject-1','subject-review',1,1,true),
		('grant-drift','drift_controller','org-contrib','reviewer-1','drift-review',1,1,true),
		('grant-publisher','person_publisher',NULL,'subject-1','self',1,1,true),
		('grant-outcome','outcome_reviewer','org-contrib','reviewer-1','outcome-review',1,1,true)`); err != nil {
		t.Fatal(err)
	}

	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_contribution_claim_revisions
		(claim_id,revision,organization_id,subject_person_id,contribution_kind,problem_class,outcome_class,source_refs,evidence_manifest_digest,attribution_method,acl_revision,consent_revision,purge_generation,policy_revision,mutation_person_id,mutation_authority_id,mutation_authority_revision,state,state_changed_at,created_at)
		VALUES('leaky',1,'org-contrib','subject-1','delivered','commerce','reliability','[{"body":"private"}]',decode(repeat('c',64),'hex'),'source_observed',1,1,0,1,'reviewer-1','org-review',1,'candidate',now(),now())`); err == nil {
		t.Fatal("contribution claim accepted a forbidden source body")
	}
	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_contribution_claim_revisions
		(claim_id,revision,organization_id,subject_person_id,contribution_kind,problem_class,outcome_class,source_refs,evidence_manifest_digest,attribution_method,acl_revision,consent_revision,purge_generation,policy_revision,mutation_person_id,mutation_authority_id,mutation_authority_revision,state,state_changed_at,created_at)
		VALUES('claim-forged',1,'org-contrib','subject-1','delivered','commerce','reliability','[{"contractType":"outcome","id":"outcome-1","revision":1}]',decode(repeat('c',64),'hex'),'source_observed',1,1,0,1,'reviewer-1','forged-authority',1,'candidate',now(),now())`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_contribution_claims_current
		(claim_id,revision,organization_id,subject_person_id,state,acl_revision,consent_revision,purge_generation,evidence_manifest_digest,updated_at)
		VALUES('claim-forged',1,'org-contrib','subject-1','candidate',1,1,0,decode(repeat('c',64),'hex'),now())`); err == nil || !strings.Contains(err.Error(), "exact active controller grant") {
		t.Fatalf("forged initial controller accepted: %v", err)
	}
	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_contribution_claim_revisions
		(claim_id,revision,organization_id,subject_person_id,contribution_kind,problem_class,outcome_class,source_refs,evidence_manifest_digest,attribution_method,acl_revision,consent_revision,purge_generation,policy_revision,mutation_person_id,mutation_authority_id,mutation_authority_revision,state,subject_review_person_id,subject_review_authority_id,subject_review_authority_revision,organization_review_person_id,organization_review_authority_id,organization_review_authority_revision,state_changed_at,created_at)
		VALUES('claim-1',1,'org-contrib','subject-1','delivered','commerce','reliability','[{"contractType":"outcome","id":"outcome-1","revision":1}]',decode(repeat('c',64),'hex'),'source_observed',1,1,0,1,'reviewer-1','org-review',1,'verified','subject-1','subject-review',1,'reviewer-1','org-review',1,now(),now())`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_contribution_claims_current
		(claim_id,revision,organization_id,subject_person_id,state,acl_revision,consent_revision,purge_generation,evidence_manifest_digest,updated_at)
		VALUES('claim-1',1,'org-contrib','subject-1','verified',1,1,0,decode(repeat('c',64),'hex'),now())`); err == nil || !strings.Contains(err.Error(), "initial contribution current") {
		t.Fatalf("unauthorized initial verified current accepted: %v", err)
	}
	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_contribution_claim_revisions
		(claim_id,revision,organization_id,subject_person_id,contribution_kind,problem_class,outcome_class,source_refs,evidence_manifest_digest,attribution_method,acl_revision,consent_revision,purge_generation,policy_revision,mutation_person_id,mutation_authority_id,mutation_authority_revision,state,state_changed_at,created_at)
		VALUES('claim-live',1,'org-contrib','subject-1','delivered','commerce','reliability','[{"contractType":"outcome","id":"outcome-1","revision":1}]',decode(repeat('c',64),'hex'),'source_observed',1,1,0,1,'reviewer-1','org-review',1,'candidate',now(),now())`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_contribution_claims_current
		(claim_id,revision,organization_id,subject_person_id,state,acl_revision,consent_revision,purge_generation,evidence_manifest_digest,updated_at)
		VALUES('claim-live',1,'org-contrib','subject-1','candidate',1,1,0,decode(repeat('c',64),'hex'),now())`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_contribution_claim_revisions
		(claim_id,revision,organization_id,subject_person_id,contribution_kind,problem_class,outcome_class,source_refs,evidence_manifest_digest,attribution_method,acl_revision,consent_revision,purge_generation,policy_revision,mutation_person_id,mutation_authority_id,mutation_authority_revision,state,subject_review_person_id,subject_review_authority_id,subject_review_authority_revision,supersedes_claim_id,supersedes_claim_revision,state_changed_at,created_at)
		VALUES('claim-live',2,'org-contrib','subject-1','delivered','commerce','reliability','[{"contractType":"outcome","id":"outcome-1","revision":1}]',decode(repeat('c',64),'hex'),'source_observed',1,1,0,1,'subject-1','subject-review',1,'subject_review','subject-1','subject-review',1,'claim-live',1,now(),now())`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `UPDATE stride_contribution_claims_current SET revision=2,state='subject_review',updated_at=now() WHERE claim_id='claim-live'`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_contribution_claim_revisions
		(claim_id,revision,organization_id,subject_person_id,contribution_kind,problem_class,outcome_class,source_refs,evidence_manifest_digest,attribution_method,acl_revision,consent_revision,purge_generation,policy_revision,mutation_person_id,mutation_authority_id,mutation_authority_revision,state,subject_review_person_id,subject_review_authority_id,subject_review_authority_revision,organization_review_person_id,organization_review_authority_id,organization_review_authority_revision,supersedes_claim_id,supersedes_claim_revision,state_changed_at,created_at)
		VALUES('claim-live',3,'org-contrib','subject-1','delivered','commerce','reliability','[{"contractType":"outcome","id":"outcome-1","revision":1}]',decode(repeat('c',64),'hex'),'source_observed',1,1,0,1,'reviewer-1','org-review',1,'verified','subject-1','subject-review',1,'reviewer-1','org-review',1,'claim-live',2,now(),now())`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `UPDATE stride_contribution_claims_current SET revision=3,state='verified',updated_at=now() WHERE claim_id='claim-live'`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `UPDATE stride_contribution_claim_revisions SET problem_class='rewrite' WHERE claim_id='claim-1' AND revision=1`); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("claim history update error=%v, want immutable rejection", err)
	}
	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_contribution_claim_revisions
		(claim_id,revision,organization_id,subject_person_id,contribution_kind,problem_class,outcome_class,source_refs,evidence_manifest_digest,attribution_method,acl_revision,consent_revision,purge_generation,policy_revision,mutation_person_id,mutation_authority_id,mutation_authority_revision,state,subject_review_person_id,subject_review_authority_id,subject_review_authority_revision,organization_review_person_id,organization_review_authority_id,organization_review_authority_revision,supersedes_claim_id,supersedes_claim_revision,state_changed_at,created_at)
		VALUES('claim-live',4,'org-contrib','subject-1','delivered','commerce','reliability','[{"contractType":"outcome","id":"outcome-1","revision":1}]',decode(repeat('c',64),'hex'),'source_observed',2,1,0,1,'reviewer-1','drift-review',1,'revalidation_required','subject-1','subject-review',1,'reviewer-1','org-review',1,'claim-live',3,now(),now())`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `UPDATE stride_contribution_claims_current SET
		revision=4,state='revalidation_required',acl_revision=2,eligibility_fence_generation=1,eligibility_fenced_at=now(),updated_at=now()
		WHERE claim_id='claim-live'`); err != nil {
		t.Fatalf("advance claim revalidation fence: %v", err)
	}
	var fenceReceipts int
	if err := store.pool.QueryRow(ctx, `SELECT count(*) FROM stride_contribution_fence_receipts WHERE claim_id='claim-live' AND fence_generation=1`).Scan(&fenceReceipts); err != nil || fenceReceipts != 1 {
		t.Fatalf("claim fence receipts=%d err=%v, want 1", fenceReceipts, err)
	}
	if _, err := store.pool.Exec(ctx, `DELETE FROM stride_contribution_claims_current WHERE claim_id='claim-live'`); err == nil || !strings.Contains(err.Error(), "current pointers cannot be deleted") {
		t.Fatalf("claim current delete bypass accepted: %v", err)
	}

	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_published_contribution_revisions
		(publication_id,revision,subject_person_id,narrative_digest,released_fields_digest,visibility,controller_person_id,controller_authority_id,controller_authority_revision,policy_revision,state,state_changed_at,created_at)
		VALUES('publication-1',1,'subject-1',decode(repeat('d',64),'hex'),decode(repeat('e',64),'hex'),'private','subject-1','self',1,1,'draft',now(),now())`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_published_contribution_revisions
		(publication_id,revision,subject_person_id,narrative_digest,released_fields_digest,visibility,controller_person_id,controller_authority_id,controller_authority_revision,policy_revision,state,state_changed_at,created_at)
		VALUES('publication-initial-bad',1,'subject-1',decode(repeat('d',64),'hex'),decode(repeat('e',64),'hex'),'private','subject-1','self',1,1,'approval_required',now(),now())`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_published_contributions_current
		(publication_id,revision,subject_person_id,state,visibility,updated_at)
		VALUES('publication-initial-bad',1,'subject-1','approval_required','private',now())`); err == nil || !strings.Contains(err.Error(), "invalid initial publication current") {
		t.Fatalf("invalid initial publication current accepted: %v", err)
	}
	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_published_contributions_current
		(publication_id,revision,subject_person_id,state,visibility,updated_at)
		VALUES('publication-1',1,'subject-1','draft','private',now())`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_network_profile_revisions
		(projection_id,revision,subject_person_id,publication_id,publication_revision,fields,fields_digest,discoverability,purge_generation,controller_person_id,controller_authority_id,controller_authority_revision,policy_revision,state,state_changed_at,created_at)
		VALUES('leaky-projection',1,'subject-1','publication-1',1,'[{"fieldKey":"mymind","valueDigest":"x"}]',decode(repeat('f',64),'hex'),'unlisted',0,'subject-1','self',1,1,'draft',now(),now())`); err == nil {
		t.Fatal("network projection accepted a private MyMind field")
	}
	tx, err = store.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO stride_network_profile_revisions
		(projection_id,revision,subject_person_id,publication_id,publication_revision,fields,fields_digest,discoverability,purge_generation,controller_person_id,controller_authority_id,controller_authority_revision,policy_revision,state,state_changed_at,created_at)
		VALUES('projection-1',1,'subject-1','publication-1',1,'[{"fieldKey":"contribution_role","valueDigest":"x"}]',decode(repeat('f',64),'hex'),'signed_in_network',0,'subject-1','self',1,1,'published',now(),now())`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO stride_network_profiles_current
		(projection_id,revision,subject_person_id,publication_id,publication_revision,state,discoverability,purge_generation,fields_digest,updated_at)
		VALUES('projection-1',1,'subject-1','publication-1',1,'published','signed_in_network',0,decode(repeat('f',64),'hex'),now())`); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err == nil || !strings.Contains(err.Error(), "eligible published contribution") {
		t.Fatalf("published projection over draft publication error=%v, want fail closed", err)
	}

	tx, err = store.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO stride_network_profile_revisions
		(projection_id,revision,subject_person_id,publication_id,publication_revision,fields,fields_digest,discoverability,purge_generation,controller_person_id,controller_authority_id,controller_authority_revision,policy_revision,state,state_changed_at,created_at)
		VALUES('projection-off',1,'subject-1','publication-1',1,'[{"fieldKey":"bio","valueDigest":"x"}]',decode(repeat('9',64),'hex'),'unlisted',0,'subject-1','self',1,1,'draft',now(),now())`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO stride_network_profiles_current
		(projection_id,revision,subject_person_id,publication_id,publication_revision,state,discoverability,purge_generation,fields_digest,updated_at)
		VALUES('projection-off',1,'subject-1','publication-1',1,'draft','unlisted',0,decode(repeat('9',64),'hex'),now())`); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatalf("initial private draft: %v", err)
	}
	tx, err = store.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO stride_network_profile_revisions
		(projection_id,revision,subject_person_id,publication_id,publication_revision,fields,fields_digest,discoverability,purge_generation,controller_person_id,controller_authority_id,controller_authority_revision,policy_revision,state,state_changed_at,supersedes_revision,created_at)
		VALUES('projection-off',2,'subject-1','publication-1',1,'[{"fieldKey":"bio","valueDigest":"x"}]',decode(repeat('9',64),'hex'),'unlisted',1,'subject-1','self',1,1,'off',now(),1,now())`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `UPDATE stride_network_profiles_current SET revision=2,state='off',discoverability='unlisted',purge_generation=1,updated_at=now() WHERE projection_id='projection-off'`); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatalf("private off transition: %v", err)
	}
	var offState string
	var eligible bool
	var exactStores int
	if err := store.pool.QueryRow(ctx, `SELECT p.state,e.eligible,(SELECT jsonb_array_length(stores) FROM stride_derived_purge_receipts WHERE trigger_id='projection-off' ORDER BY purge_generation DESC LIMIT 1) FROM stride_network_profiles_current p JOIN stride_network_projection_eligibility e USING(projection_id) WHERE p.projection_id='projection-off'`).Scan(&offState, &eligible, &exactStores); err != nil || offState != "off" || eligible || exactStores != 13 {
		t.Fatalf("off fence state=%q eligible=%t stores=%d err=%v", offState, eligible, exactStores, err)
	}
	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_derived_purge_receipts
		(purge_receipt_id,revision,subject_person_id,trigger_contract_type,trigger_id,trigger_revision,purge_generation,affected_fields_digest,stores,eligibility_fenced_at,recorded_at,state,created_at)
		VALUES('truncated-purge',1,'subject-1','network_profile_projection','projection-off',2,99,decode(repeat('9',64),'hex'),'[{"store":"projection","state":"queued","attemptCount":1}]',now(),now(),'queued',now())`); err == nil {
		t.Fatal("truncated network purge store set accepted")
	}

	evidenceRefs := `{"agentProfile":{},"runtimeRevision":{},"modelRevision":{},"agentRun":{},"agentOutput":{},"humanInteraction":{},"humanAdoption":{},"outcome":{}}`
	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_agent_influence_receipt_revisions
		(influence_id,revision,organization_id,subject_person_id,evidence_refs,reviewer_person_id,reviewer_authority_id,reviewer_authority_revision,policy_revision,state,created_at)
		VALUES('influence-1',1,'org-contrib','subject-1',$1::jsonb,'reviewer-1','outcome-review',1,1,'verified',now())`, evidenceRefs); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_agent_influence_receipts_current
		(influence_id,revision,organization_id,subject_person_id,state,updated_at) VALUES('influence-1',1,'org-contrib','subject-1','verified',now())`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_agent_influence_receipt_revisions
		(influence_id,revision,organization_id,subject_person_id,evidence_refs,reviewer_person_id,reviewer_authority_id,reviewer_authority_revision,policy_revision,state,supersedes_revision,created_at)
		VALUES('influence-1',2,'org-contrib','subject-1',$1::jsonb,'reviewer-1','forged-review',1,1,'revoked',1,now())`, evidenceRefs); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `UPDATE stride_agent_influence_receipts_current SET revision=2,state='revoked',updated_at=now() WHERE influence_id='influence-1'`); err == nil || !strings.Contains(err.Error(), "exact active reviewer authority") {
		t.Fatalf("forged terminal influence controller accepted: %v", err)
	}
	if _, err := store.pool.Exec(ctx, `DELETE FROM stride_agent_influence_receipts_current WHERE influence_id='influence-1'`); err == nil || !strings.Contains(err.Error(), "current pointers cannot be deleted") {
		t.Fatalf("influence current delete bypass accepted: %v", err)
	}

	var disabled int
	if err := store.pool.QueryRow(ctx, `SELECT count(*) FROM stride_feature_switches WHERE enabled=false AND feature_key IN
		('contribution_candidate_detection','contribution_review','work_record_private','network_profile_publication','network_projection_shadow','network_search','network_contact','network_query_parser_provider','network_semantic_reranker')`).Scan(&disabled); err != nil || disabled != 9 {
		t.Fatalf("disabled contribution/network switches=%d err=%v, want 9", disabled, err)
	}
}
