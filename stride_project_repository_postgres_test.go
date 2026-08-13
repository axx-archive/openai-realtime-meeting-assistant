package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

type projectSQLStatement struct {
	query string
	args  []any
}

func execProjectSQLBatch(ctx context.Context, store *PostgresCanonicalStore, statements ...projectSQLStatement) error {
	batch := &pgx.Batch{}
	for _, statement := range statements {
		batch.Queue(statement.query, statement.args...)
	}
	results := store.pool.SendBatch(ctx, batch)
	for range statements {
		if _, err := results.Exec(); err != nil {
			_ = results.Close()
			return err
		}
	}
	return results.Close()
}

func seedProjectPostgresAuthority(t *testing.T, ctx context.Context, store *PostgresCanonicalStore) {
	t.Helper()
	err := execProjectSQLBatch(ctx, store,
		projectSQLStatement{`INSERT INTO stride_person_principals(person_id,revision,account_subject_digest,status,recovery_revision,custody_revision,created_at) VALUES('person_project_test',1,decode($1,'hex'),'active',1,1,clock_timestamp()-interval '1 hour')`, []any{strings.Repeat("1", 64)}},
		projectSQLStatement{`INSERT INTO stride_organizations(organization_id,revision,name,slug,status,discoverability,creator_person_id,created_at,updated_at) VALUES('organization_project_test',1,'Project Test','project-test','active','private','person_project_test',clock_timestamp()-interval '1 hour',clock_timestamp()-interval '1 hour')`, nil},
		projectSQLStatement{`INSERT INTO stride_organization_membership_revisions(membership_id,revision,organization_id,person_id,role,status,granted_at,created_at,created_by_person_id) VALUES('membership_project_test',1,'organization_project_test','person_project_test','owner','active',clock_timestamp()-interval '1 hour',clock_timestamp()-interval '1 hour','person_project_test')`, nil},
		projectSQLStatement{`INSERT INTO stride_organization_memberships_current(membership_id,revision,organization_id,person_id,role,status,updated_at) VALUES('membership_project_test',1,'organization_project_test','person_project_test','owner','active',clock_timestamp())`, nil},
		projectSQLStatement{`INSERT INTO stride_active_organization_sessions(session_subject_digest,person_id,organization_id,membership_id,membership_revision,session_revision,status,bound_at,expires_at,updated_at) VALUES(decode($1,'hex'),'person_project_test','organization_project_test','membership_project_test',1,1,'active',clock_timestamp()-interval '5 minutes',clock_timestamp()+interval '1 hour',clock_timestamp())`, []any{strings.Repeat("2", 64)}},
	)
	if err != nil {
		t.Fatalf("seed Project authority: %v", err)
	}
}

func TestPostgresProjectMigrationBackfillsExistingSessionGeneration(t *testing.T) {
	ctx, pool := startDisposableCanonicalPostgres(t)
	store := NewPostgresCanonicalStore(pool, testCanonicalRegistry(t))
	t.Setenv(canonicalMigrationMaxVersionEnv, "17")
	if err := store.ApplyMigrations(ctx); err != nil {
		t.Fatal(err)
	}
	seedProjectPostgresAuthority(t, ctx, store)
	if _, err := store.pool.Exec(ctx, `UPDATE stride_active_organization_sessions SET session_revision=4,updated_at=clock_timestamp() WHERE session_subject_digest=decode($1,'hex')`, strings.Repeat("2", 64)); err != nil {
		t.Fatal(err)
	}
	t.Setenv(canonicalMigrationMaxVersionEnv, "18")
	if err := store.ApplyMigrations(ctx); err != nil {
		t.Fatalf("upgrade to Project migration: %v", err)
	}
	var revision, generation int
	if err := store.pool.QueryRow(ctx, `SELECT session_revision,authority_generation FROM stride_active_organization_sessions WHERE session_subject_digest=decode($1,'hex')`, strings.Repeat("2", 64)).Scan(&revision, &generation); err != nil || revision != 4 || generation != 4 {
		t.Fatalf("session generation backfill = rev %d gen %d err %v", revision, generation, err)
	}
}

func TestPostgresProjectMigrationRequiresAuthorityReceiptsAndLegalTransitions(t *testing.T) {
	ctx, store, _ := migratedPostgresCanonicalStore(t)
	seedProjectPostgresAuthority(t, ctx, store)
	controllers := `[{"contractType":"organization_membership","id":"membership_project_test","revision":1,"digest":"` + strings.Repeat("3", 64) + `"}]`
	audience := `{"visibility":"project","principals":["person_project_test"]}`
	err := execProjectSQLBatch(ctx, store,
		projectSQLStatement{`INSERT INTO stride_project_revisions(project_id,revision,organization_id,title,aliases,lifecycle,retention_policy,controller_memberships,audience,acl_revision,creator_person_id,created_at,updated_at,content_digest) VALUES('project_test',1,'organization_project_test','Project Test','[]','active','organization_default',$1::jsonb,$2::jsonb,1,'person_project_test',clock_timestamp(),clock_timestamp(),decode($3,'hex'))`, []any{controllers, audience, strings.Repeat("4", 64)}},
		projectSQLStatement{`INSERT INTO stride_project_thread_binding_revisions(binding_id,revision,organization_id,project_id,project_revision,thread_id,kind,state,thread_audience_revision,thread_acl_digest,actor_person_id,actor_membership_id,actor_membership_revision,bound_at,content_digest) VALUES('binding_project_test',1,'organization_project_test','project_test',1,'thread_project_test','primary','active',1,decode($1,'hex'),'person_project_test','membership_project_test',1,clock_timestamp(),decode($2,'hex'))`, []any{strings.Repeat("5", 64), strings.Repeat("6", 64)}},
	)
	if err != nil {
		t.Fatalf("insert Project candidate revisions: %v", err)
	}
	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_projects_current(project_id,revision,organization_id,lifecycle,content_digest,updated_at)
VALUES('project_test',1,'organization_project_test','active',decode($1,'hex'),clock_timestamp())`, strings.Repeat("4", 64)); err == nil {
		t.Fatal("Project current pointer advanced without authority receipt")
	}
	if err := execProjectSQLBatch(ctx, store,
		projectSQLStatement{`INSERT INTO stride_project_operation_receipts(operation_id,organization_id,operation_kind,project_id,project_revision,binding_id,binding_revision,actor_person_id,actor_membership_id,actor_membership_revision,session_subject_digest,session_revision,authority_generation,idempotency_key_digest,request_fingerprint,recorded_at) VALUES('operation_project_create','organization_project_test','create_project','project_test',1,'binding_project_test',1,'person_project_test','membership_project_test',1,decode($1,'hex'),1,1,decode($2,'hex'),decode($3,'hex'),clock_timestamp())`, []any{strings.Repeat("2", 64), strings.Repeat("7", 64), strings.Repeat("8", 64)}},
		projectSQLStatement{`INSERT INTO stride_projects_current(project_id,revision,organization_id,lifecycle,content_digest,updated_at) VALUES('project_test',1,'organization_project_test','active',decode($1,'hex'),clock_timestamp())`, []any{strings.Repeat("4", 64)}},
		projectSQLStatement{`INSERT INTO stride_project_thread_bindings_current(binding_id,revision,organization_id,project_id,thread_id,kind,state,content_digest,updated_at) VALUES('binding_project_test',1,'organization_project_test','project_test','thread_project_test','primary','active',decode($1,'hex'),clock_timestamp())`, []any{strings.Repeat("6", 64)}},
	); err != nil {
		t.Fatalf("receipted Project create failed: %v", err)
	}

	sourceRefs := `[{"contractType":"conversation_event","id":"turn_project_test","revision":1,"digest":"` + strings.Repeat("9", 64) + `"}]`
	sourceAudience := `{"visibility":"private","principals":["person_project_test"]}`
	if _, err = store.pool.Exec(ctx, `INSERT INTO stride_conversation_events(tenant_id,event_id,event_revision,sequence,schema_version,idempotency_key,source_type,source_id,thread_id,author_principal,author_name,occurred_at,ingested_at,event_type,content_revision,content_digest,audience_digest,visibility,acl_version,retention_policy,purge_generation,provenance,structured_refs)
VALUES('organization_project_test','turn_project_test',1,1,1,'turn-project-test','chat','thread_project_test','thread_project_test','person_project_test','AJ',clock_timestamp(),clock_timestamp(),'message',1,decode($1,'hex'),sha256(convert_to($2::jsonb::text,'UTF8')),'private',1,'organization_default',1,'client','[]')`, strings.Repeat("9", 64), sourceAudience); err != nil {
		t.Fatalf("insert canonical conversation source: %v", err)
	}
	var sourceACLDigest string
	if err = store.pool.QueryRow(ctx, `SELECT encode(sha256(convert_to(concat_ws(E'\x1f',tenant_id,event_id,content_revision::text,encode(content_digest,'hex'),encode(audience_digest,'hex'),visibility,acl_version::text,purge_generation::text),'UTF8')),'hex') FROM stride_conversation_events WHERE tenant_id='organization_project_test' AND event_id='turn_project_test'`).Scan(&sourceACLDigest); err != nil {
		t.Fatal(err)
	}
	if _, err = store.pool.Exec(ctx, `INSERT INTO stride_project_source_authority_receipts(source_authority_receipt_id,organization_id,subject_contract_type,subject_id,subject_revision,subject_digest,source_refs,evidence_coverage_digest,source_audience,source_acl_revision,source_acl_digest,consent_revision,purge_generation,actor_person_id,actor_membership_id,actor_membership_revision,session_subject_digest,session_revision,authority_generation,idempotency_key_digest,request_fingerprint,recorded_at,expires_at)
VALUES('source_authority_project_test','organization_project_test','conversation_event','turn_project_test',1,decode($1,'hex'),$2::jsonb,decode($3,'hex'),$4::jsonb,1,decode($5,'hex'),1,1,'person_project_test','membership_project_test',1,decode($6,'hex'),1,1,decode($7,'hex'),decode($8,'hex'),clock_timestamp(),clock_timestamp()+interval '20 minutes')`, strings.Repeat("9", 64), sourceRefs, strings.Repeat("a", 64), sourceAudience, sourceACLDigest, strings.Repeat("2", 64), strings.Repeat("0", 64), strings.Repeat("8", 64)); err != nil {
		t.Fatalf("insert source authority receipt: %v", err)
	}
	if err := execProjectSQLBatch(ctx, store,
		projectSQLStatement{`INSERT INTO stride_project_association_revisions(association_id,revision,organization_id,project_id,project_revision,subject_contract_type,subject_id,subject_revision,subject_digest,source_refs,source_authority_receipt_id,evidence_coverage_digest,state,basis,classifier_revision,confidence,actor_person_id,actor_membership_id,actor_membership_revision,session_subject_digest,session_revision,authority_generation,source_audience,source_acl_revision,source_acl_digest,consent_revision,purge_generation,idempotency_key_digest,recorded_at,content_digest) VALUES('association_direct_confirm',1,'organization_project_test','project_test',1,'conversation_event','turn_project_test',1,decode($1,'hex'),$2::jsonb,'source_authority_project_test',decode($3,'hex'),'confirmed','selected','project_linker_v1',1,'person_project_test','membership_project_test',1,decode($4,'hex'),1,1,$5::jsonb,1,decode($6,'hex'),1,1,decode($7,'hex'),clock_timestamp(),decode($8,'hex'))`, []any{strings.Repeat("9", 64), sourceRefs, strings.Repeat("a", 64), strings.Repeat("2", 64), sourceAudience, sourceACLDigest, strings.Repeat("b", 64), strings.Repeat("c", 64)}},
		projectSQLStatement{`INSERT INTO stride_project_association_events(event_id,organization_id,association_id,association_revision,action,resulting_state,prior_revision,new_revision,actor_person_id,actor_membership_id,actor_membership_revision,session_subject_digest,session_revision,authority_generation,idempotency_key_digest,request_fingerprint,occurred_at) VALUES('event_direct_confirm','organization_project_test','association_direct_confirm',1,'confirm','confirmed',0,1,'person_project_test','membership_project_test',1,decode($1,'hex'),1,1,decode($2,'hex'),decode($3,'hex'),clock_timestamp())`, []any{strings.Repeat("2", 64), strings.Repeat("b", 64), strings.Repeat("c", 64)}},
	); err == nil {
		t.Fatal("revision-one confirmed ProjectAssociation bypassed atomic correction receipt")
	}
	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_project_association_revisions(association_id,revision,organization_id,project_id,project_revision,subject_contract_type,subject_id,subject_revision,subject_digest,source_refs,source_authority_receipt_id,evidence_coverage_digest,state,basis,classifier_revision,confidence,actor_person_id,actor_membership_id,actor_membership_revision,session_subject_digest,session_revision,authority_generation,source_audience,source_acl_revision,source_acl_digest,consent_revision,purge_generation,idempotency_key_digest,recorded_at,content_digest) VALUES('association_direct_confirm_alt',1,'organization_project_test','project_test',1,'conversation_event','turn_project_test',1,decode($1,'hex'),$2::jsonb,'source_authority_project_test',decode($3,'hex'),'confirmed','selected','project_linker_v1',1,'person_project_test','membership_project_test',1,decode($4,'hex'),1,1,$5::jsonb,1,decode($6,'hex'),1,1,decode($7,'hex'),clock_timestamp(),decode($8,'hex'))`, strings.Repeat("9", 64), sourceRefs, strings.Repeat("a", 64), strings.Repeat("2", 64), sourceAudience, sourceACLDigest, strings.Repeat("d", 64), strings.Repeat("e", 64)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_project_association_events(event_id,organization_id,association_id,association_revision,action,resulting_state,prior_revision,new_revision,actor_person_id,actor_membership_id,actor_membership_revision,session_subject_digest,session_revision,authority_generation,idempotency_key_digest,request_fingerprint,occurred_at) VALUES('event_direct_confirm_alt','organization_project_test','association_direct_confirm_alt',1,'confirm','confirmed',1,2,'person_project_test','membership_project_test',1,decode($1,'hex'),1,1,decode($2,'hex'),decode($3,'hex'),clock_timestamp())`, strings.Repeat("2", 64), strings.Repeat("d", 64), strings.Repeat("e", 64)); err == nil {
		t.Fatal("incoherent event numbering bypassed direct-confirm correction receipt")
	}
	_, err = store.pool.Exec(ctx, `INSERT INTO stride_project_association_revisions(
association_id,revision,organization_id,project_id,project_revision,subject_contract_type,subject_id,subject_revision,subject_digest,source_refs,source_authority_receipt_id,evidence_coverage_digest,state,basis,classifier_revision,confidence,actor_person_id,actor_membership_id,actor_membership_revision,session_subject_digest,session_revision,authority_generation,source_audience,source_acl_revision,source_acl_digest,consent_revision,purge_generation,idempotency_key_digest,expires_at,recorded_at,content_digest)
VALUES('association_project_test',1,'organization_project_test','project_test',1,'conversation_event','turn_project_test',1,decode($1,'hex'),$2::jsonb,'source_authority_project_test',decode($3,'hex'),'proposed','suggested','project_linker_v1',0.9,'person_project_test','membership_project_test',1,decode($4,'hex'),1,1,$5::jsonb,1,decode($6,'hex'),1,1,decode($7,'hex'),clock_timestamp()+interval '15 minutes',clock_timestamp(),decode($8,'hex'))`, strings.Repeat("9", 64), sourceRefs, strings.Repeat("a", 64), strings.Repeat("2", 64), sourceAudience, sourceACLDigest, strings.Repeat("c", 64), strings.Repeat("d", 64))
	if err != nil {
		t.Fatalf("insert proposed association revision: %v", err)
	}
	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_project_associations_current(association_id,revision,organization_id,project_id,state,content_digest,updated_at)
VALUES('association_project_test',1,'organization_project_test','project_test','proposed',decode($1,'hex'),clock_timestamp())`, strings.Repeat("d", 64)); err == nil {
		t.Fatal("ProjectAssociation current pointer advanced without authority event")
	}
	if err := execProjectSQLBatch(ctx, store,
		projectSQLStatement{`INSERT INTO stride_project_association_events(event_id,organization_id,association_id,association_revision,action,resulting_state,prior_revision,new_revision,actor_person_id,actor_membership_id,actor_membership_revision,session_subject_digest,session_revision,authority_generation,idempotency_key_digest,request_fingerprint,occurred_at) VALUES('event_project_propose','organization_project_test','association_project_test',1,'propose','proposed',0,1,'person_project_test','membership_project_test',1,decode($1,'hex'),1,1,decode($2,'hex'),decode($3,'hex'),clock_timestamp())`, []any{strings.Repeat("2", 64), strings.Repeat("c", 64), strings.Repeat("e", 64)}},
		projectSQLStatement{`INSERT INTO stride_project_associations_current(association_id,revision,organization_id,project_id,state,content_digest,updated_at) VALUES('association_project_test',1,'organization_project_test','project_test','proposed',decode($1,'hex'),clock_timestamp())`, []any{strings.Repeat("d", 64)}},
	); err != nil {
		t.Fatalf("receipted association proposal failed: %v", err)
	}

	// A terminal removed edge cannot be returned to confirmed even if a buggy
	// writer appends syntactically valid revisions and events.
	insertAssociationRevision := func(revision int, state, action, digest, key string) error {
		return execProjectSQLBatch(ctx, store,
			projectSQLStatement{`INSERT INTO stride_project_association_revisions(association_id,revision,organization_id,project_id,project_revision,subject_contract_type,subject_id,subject_revision,subject_digest,source_refs,source_authority_receipt_id,evidence_coverage_digest,state,basis,classifier_revision,confidence,actor_person_id,actor_membership_id,actor_membership_revision,session_subject_digest,session_revision,authority_generation,source_audience,source_acl_revision,source_acl_digest,consent_revision,purge_generation,idempotency_key_digest,supersedes_revision,supersedes_digest,recorded_at,content_digest) VALUES('association_project_test',$1::bigint,'organization_project_test','project_test',1,'conversation_event','turn_project_test',1,decode($2,'hex'),$3::jsonb,'source_authority_project_test',decode($4,'hex'),$5,'selected','project_linker_v1',1,'person_project_test','membership_project_test',1,decode($6,'hex'),1,1,$7::jsonb,1,decode($8,'hex'),1,1,decode($9,'hex'),$1::bigint-1,decode($10,'hex'),clock_timestamp(),decode($11,'hex'))`, []any{revision, strings.Repeat("9", 64), sourceRefs, strings.Repeat("a", 64), state, strings.Repeat("2", 64), sourceAudience, sourceACLDigest, key, map[int]string{2: strings.Repeat("d", 64), 3: strings.Repeat("1", 64), 4: strings.Repeat("3", 64)}[revision], digest}},
			projectSQLStatement{`INSERT INTO stride_project_association_events(event_id,organization_id,association_id,association_revision,action,resulting_state,prior_revision,new_revision,actor_person_id,actor_membership_id,actor_membership_revision,session_subject_digest,session_revision,authority_generation,idempotency_key_digest,request_fingerprint,occurred_at) VALUES('event_project_'||$1::bigint::text,'organization_project_test','association_project_test',$1::bigint,$2,$3,$1::bigint-1,$1::bigint,'person_project_test','membership_project_test',1,decode($4,'hex'),1,1,decode($5,'hex'),decode($6,'hex'),clock_timestamp())`, []any{revision, action, state, strings.Repeat("2", 64), key, strings.Repeat(digest[:1], 64)}},
		)
	}
	if err := insertAssociationRevision(2, "confirmed", "confirm", strings.Repeat("1", 64), strings.Repeat("2", 64)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `UPDATE stride_project_associations_current SET revision=2,state='confirmed',content_digest=decode($1,'hex'),updated_at=clock_timestamp() WHERE association_id='association_project_test'`, strings.Repeat("1", 64)); err != nil {
		t.Fatal(err)
	}
	// A correction is one deferred, atomic receipt: terminalize the old edge,
	// create its confirmed replacement, append both exact events, and advance
	// both current pointers in the same transaction.
	statements := []projectSQLStatement{
		{`INSERT INTO stride_project_association_revisions(association_id,revision,organization_id,project_id,project_revision,subject_contract_type,subject_id,subject_revision,subject_digest,source_refs,source_authority_receipt_id,evidence_coverage_digest,state,basis,classifier_revision,confidence,actor_person_id,actor_membership_id,actor_membership_revision,session_subject_digest,session_revision,authority_generation,source_audience,source_acl_revision,source_acl_digest,consent_revision,purge_generation,idempotency_key_digest,supersedes_revision,supersedes_digest,replacement_association_id,replacement_association_revision,replacement_association_digest,recorded_at,content_digest)
VALUES('association_project_test',3,'organization_project_test','project_test',1,'conversation_event','turn_project_test',1,decode($1,'hex'),$2::jsonb,'source_authority_project_test',decode($3,'hex'),'corrected','selected','project_linker_v1',1,'person_project_test','membership_project_test',1,decode($4,'hex'),1,1,$5::jsonb,1,decode($6,'hex'),1,1,decode($7,'hex'),2,decode($8,'hex'),'association_project_replacement',1,decode($9,'hex'),clock_timestamp(),decode($10,'hex'))`, []any{strings.Repeat("9", 64), sourceRefs, strings.Repeat("a", 64), strings.Repeat("2", 64), sourceAudience, sourceACLDigest, strings.Repeat("7", 64), strings.Repeat("1", 64), strings.Repeat("4", 64), strings.Repeat("3", 64)}},
		{`INSERT INTO stride_project_association_revisions(association_id,revision,organization_id,project_id,project_revision,subject_contract_type,subject_id,subject_revision,subject_digest,source_refs,source_authority_receipt_id,evidence_coverage_digest,state,basis,classifier_revision,confidence,actor_person_id,actor_membership_id,actor_membership_revision,session_subject_digest,session_revision,authority_generation,source_audience,source_acl_revision,source_acl_digest,consent_revision,purge_generation,idempotency_key_digest,recorded_at,content_digest)
VALUES('association_project_replacement',1,'organization_project_test','project_test',1,'conversation_event','turn_project_test',1,decode($1,'hex'),$2::jsonb,'source_authority_project_test',decode($3,'hex'),'confirmed','selected','project_linker_v1',1,'person_project_test','membership_project_test',1,decode($4,'hex'),1,1,$5::jsonb,1,decode($6,'hex'),1,1,decode($7,'hex'),clock_timestamp(),decode($8,'hex'))`, []any{strings.Repeat("9", 64), sourceRefs, strings.Repeat("a", 64), strings.Repeat("2", 64), sourceAudience, sourceACLDigest, strings.Repeat("7", 64), strings.Repeat("4", 64)}},
		{`INSERT INTO stride_project_association_events(event_id,organization_id,association_id,association_revision,action,resulting_state,prior_revision,new_revision,replacement_association_id,replacement_association_revision,replacement_association_digest,correction_id,actor_person_id,actor_membership_id,actor_membership_revision,session_subject_digest,session_revision,authority_generation,idempotency_key_digest,request_fingerprint,occurred_at) VALUES('event_project_correct','organization_project_test','association_project_test',3,'correct','corrected',2,3,'association_project_replacement',1,decode($1,'hex'),'correction_project_test','person_project_test','membership_project_test',1,decode($2,'hex'),1,1,decode($3,'hex'),decode($4,'hex'),clock_timestamp())`, []any{strings.Repeat("4", 64), strings.Repeat("2", 64), strings.Repeat("7", 64), strings.Repeat("8", 64)}},
		{`INSERT INTO stride_project_association_events(event_id,organization_id,association_id,association_revision,action,resulting_state,prior_revision,new_revision,correction_id,actor_person_id,actor_membership_id,actor_membership_revision,session_subject_digest,session_revision,authority_generation,idempotency_key_digest,request_fingerprint,occurred_at) VALUES('event_project_replacement_confirm','organization_project_test','association_project_replacement',1,'confirm','confirmed',0,1,'correction_project_test','person_project_test','membership_project_test',1,decode($1,'hex'),1,1,decode($2,'hex'),decode($3,'hex'),clock_timestamp())`, []any{strings.Repeat("2", 64), strings.Repeat("7", 64), strings.Repeat("8", 64)}},
		{`UPDATE stride_project_associations_current SET revision=3,state='corrected',content_digest=decode($1,'hex'),updated_at=clock_timestamp() WHERE association_id='association_project_test'`, []any{strings.Repeat("3", 64)}},
		{`INSERT INTO stride_project_associations_current(association_id,revision,organization_id,project_id,state,content_digest,updated_at) VALUES('association_project_replacement',1,'organization_project_test','project_test','confirmed',decode($1,'hex'),clock_timestamp())`, []any{strings.Repeat("4", 64)}},
		{`INSERT INTO stride_project_correction_receipts(correction_id,organization_id,old_association_id,old_association_revision,replacement_association_id,replacement_association_revision,old_event_id,replacement_event_id,actor_person_id,actor_membership_id,actor_membership_revision,session_subject_digest,session_revision,authority_generation,idempotency_key_digest,request_fingerprint,recorded_at) VALUES('correction_project_test','organization_project_test','association_project_test',3,'association_project_replacement',1,'event_project_correct','event_project_replacement_confirm','person_project_test','membership_project_test',1,decode($1,'hex'),1,1,decode($2,'hex'),decode($3,'hex'),clock_timestamp())`, []any{strings.Repeat("2", 64), strings.Repeat("7", 64), strings.Repeat("8", 64)}},
	}
	mismatched, err := store.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for index, statement := range statements {
		args := append([]any(nil), statement.args...)
		if index == 1 {
			args[6] = strings.Repeat("f", 64) // replacement revision key
		}
		if _, err = mismatched.Exec(ctx, statement.query, args...); err != nil {
			t.Fatal(err)
		}
	}
	if err = mismatched.Commit(ctx); err == nil {
		t.Fatal("Project correction committed with replacement revision authority/key mismatch")
	}
	failed, err := store.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for index, statement := range statements {
		if index == 5 { // exact replacement current pointer
			continue
		}
		if _, err = failed.Exec(ctx, statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	if err = failed.Commit(ctx); err == nil {
		t.Fatal("Project correction committed without replacement current pointer")
	}

	tx, err := store.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	for _, statement := range statements {
		if _, err = tx.Exec(ctx, statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatalf("atomic Project correction rejected: %v", err)
	}
	if err := insertAssociationRevision(4, "confirmed", "confirm", strings.Repeat("5", 64), strings.Repeat("6", 64)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `UPDATE stride_project_associations_current SET revision=4,state='confirmed',content_digest=decode($1,'hex'),updated_at=clock_timestamp() WHERE association_id='association_project_test'`, strings.Repeat("5", 64)); err == nil {
		t.Fatal("corrected ProjectAssociation was resurrected")
	}

	var enabled int
	if err := store.pool.QueryRow(ctx, `SELECT count(*) FROM stride_feature_switches WHERE enabled AND feature_key IN ('project_authority_read','project_authority_write','project_smart_link','project_record_projection')`).Scan(&enabled); err != nil || enabled != 0 {
		t.Fatalf("Project feature switches activated: count=%d err=%v", enabled, err)
	}
}

func TestPostgresProjectAuthorityRejectsStaleSessionReceipt(t *testing.T) {
	ctx, store, _ := migratedPostgresCanonicalStore(t)
	seedProjectPostgresAuthority(t, ctx, store)
	controllers := `[{"contractType":"organization_membership","id":"membership_project_test","revision":1,"digest":"` + strings.Repeat("3", 64) + `"}]`
	audience := `{"visibility":"project","principals":["person_project_test"]}`
	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_project_revisions(project_id,revision,organization_id,title,aliases,lifecycle,retention_policy,controller_memberships,audience,acl_revision,creator_person_id,created_at,updated_at,content_digest)
VALUES('project_stale',1,'organization_project_test','Stale','[]','active','organization_default',$1::jsonb,$2::jsonb,1,'person_project_test',clock_timestamp(),clock_timestamp(),decode($3,'hex'))`, controllers, audience, strings.Repeat("4", 64)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `UPDATE stride_active_organization_sessions SET session_revision=2,authority_generation=2,status='invalidated',invalidated_at=clock_timestamp(),updated_at=clock_timestamp() WHERE session_subject_digest=decode($1,'hex')`, strings.Repeat("2", 64)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_project_operation_receipts(operation_id,organization_id,operation_kind,project_id,project_revision,actor_person_id,actor_membership_id,actor_membership_revision,session_subject_digest,session_revision,authority_generation,idempotency_key_digest,request_fingerprint,recorded_at)
VALUES('operation_stale','organization_project_test','revise','project_stale',1,'person_project_test','membership_project_test',1,decode($1,'hex'),1,1,decode($2,'hex'),decode($3,'hex'),clock_timestamp())`, strings.Repeat("2", 64), strings.Repeat("5", 64), strings.Repeat("6", 64)); err == nil {
		t.Fatal("stale invalidated session authorized a Project operation receipt")
	}
}

func TestPostgresProjectAuthorityGenerationAdvancesMonotonically(t *testing.T) {
	ctx, store, _ := migratedPostgresCanonicalStore(t)
	seedProjectPostgresAuthority(t, ctx, store)
	controllers := `[{"contractType":"organization_membership","id":"membership_project_test","revision":1,"digest":"` + strings.Repeat("3", 64) + `"}]`
	audience := `{"visibility":"project","principals":["person_project_test"]}`
	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_project_revisions(project_id,revision,organization_id,title,aliases,lifecycle,retention_policy,controller_memberships,audience,acl_revision,creator_person_id,created_at,updated_at,content_digest) VALUES('project_generation',1,'organization_project_test','Generation','[]','active','organization_default',$1::jsonb,$2::jsonb,1,'person_project_test',clock_timestamp(),clock_timestamp(),decode($3,'hex'))`, controllers, audience, strings.Repeat("4", 64)); err != nil {
		t.Fatal(err)
	}
	insertReceipt := func(id string, sessionRevision, generation int, key string) error {
		_, err := store.pool.Exec(ctx, `INSERT INTO stride_project_operation_receipts(operation_id,organization_id,operation_kind,project_id,project_revision,actor_person_id,actor_membership_id,actor_membership_revision,session_subject_digest,session_revision,authority_generation,idempotency_key_digest,request_fingerprint,recorded_at) VALUES($1,'organization_project_test','revise_project','project_generation',1,'person_project_test','membership_project_test',1,decode($2,'hex'),$3,$4,decode($5,'hex'),decode($6,'hex'),clock_timestamp())`, id, strings.Repeat("2", 64), sessionRevision, generation, key, strings.Repeat("8", 64))
		return err
	}
	if err := insertReceipt("operation_generation_mismatch", 1, 2, strings.Repeat("5", 64)); err == nil {
		t.Fatal("generation not equal to canonical session revision authorized operation")
	}
	if _, err := store.pool.Exec(ctx, `UPDATE stride_active_organization_sessions SET session_revision=2,authority_generation=2,updated_at=clock_timestamp() WHERE session_subject_digest=decode($1,'hex')`, strings.Repeat("2", 64)); err != nil {
		t.Fatalf("canonical session/generation advance failed: %v", err)
	}
	if err := insertReceipt("operation_generation_current", 2, 2, strings.Repeat("6", 64)); err != nil {
		t.Fatalf("current authority generation rejected: %v", err)
	}
	if _, err := store.pool.Exec(ctx, `UPDATE stride_active_organization_sessions SET session_revision=1,authority_generation=1,updated_at=clock_timestamp() WHERE session_subject_digest=decode($1,'hex')`, strings.Repeat("2", 64)); err == nil {
		t.Fatal("authority generation decreased or was reused")
	}
	if _, err := store.pool.Exec(ctx, `UPDATE stride_active_organization_sessions SET session_revision=3,authority_generation=4,updated_at=clock_timestamp() WHERE session_subject_digest=decode($1,'hex')`, strings.Repeat("2", 64)); err == nil {
		t.Fatal("authority generation skipped or diverged from session revision")
	}
	if _, err := store.pool.Exec(ctx, `UPDATE stride_active_organization_sessions SET session_revision=3,authority_generation=3,updated_at=clock_timestamp() WHERE session_subject_digest=decode($1,'hex')`, strings.Repeat("2", 64)); err != nil {
		t.Fatalf("session rebind did not advance canonical generation: %v", err)
	}
	if err := insertReceipt("operation_generation_replay", 2, 2, strings.Repeat("7", 64)); err == nil {
		t.Fatal("pre-rebind generation replay remained authoritative")
	}
}

func TestPostgresProjectSourceReceiptRequiresCurrentAuthorizedCanonicalEvent(t *testing.T) {
	ctx, store, _ := migratedPostgresCanonicalStore(t)
	seedProjectPostgresAuthority(t, ctx, store)
	session := strings.Repeat("2", 64)
	digest := strings.Repeat("9", 64)
	coverage := strings.Repeat("a", 64)
	audience := `{"visibility":"private","principals":["person_project_test"]}`
	foreignAudience := `{"visibility":"private","principals":["person_other"]}`

	insertReceipt := func(id, eventID, sourceAudience, key string) error {
		refs := `[{"contractType":"conversation_event","id":"` + eventID + `","revision":1,"digest":"` + digest + `"}]`
		var aclDigest string
		if err := store.pool.QueryRow(ctx, `SELECT encode(sha256(convert_to(concat_ws(E'\x1f',tenant_id,event_id,content_revision::text,encode(content_digest,'hex'),encode(audience_digest,'hex'),visibility,acl_version::text,purge_generation::text),'UTF8')),'hex') FROM stride_conversation_events WHERE tenant_id='organization_project_test' AND event_id=$1`, eventID).Scan(&aclDigest); err != nil {
			aclDigest = strings.Repeat("b", 64)
		}
		_, err := store.pool.Exec(ctx, `INSERT INTO stride_project_source_authority_receipts(source_authority_receipt_id,organization_id,subject_contract_type,subject_id,subject_revision,subject_digest,source_refs,evidence_coverage_digest,source_audience,source_acl_revision,source_acl_digest,consent_revision,purge_generation,actor_person_id,actor_membership_id,actor_membership_revision,session_subject_digest,session_revision,authority_generation,idempotency_key_digest,request_fingerprint,recorded_at,expires_at)
VALUES($1,'organization_project_test','conversation_event',$2,1,decode($3,'hex'),$4::jsonb,decode($5,'hex'),$6::jsonb,1,decode($7,'hex'),1,1,'person_project_test','membership_project_test',1,decode($8,'hex'),1,1,decode($9,'hex'),decode($10,'hex'),clock_timestamp(),clock_timestamp()+interval '20 minutes')`, id, eventID, digest, refs, coverage, sourceAudience, aclDigest, session, key, strings.Repeat("8", 64))
		return err
	}

	if err := insertReceipt("source_missing", "turn_missing", audience, strings.Repeat("1", 64)); err == nil {
		t.Fatal("guessed nonexistent conversation event minted a Project source receipt")
	}
	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_conversation_events(tenant_id,event_id,event_revision,sequence,schema_version,idempotency_key,source_type,source_id,thread_id,author_principal,author_name,occurred_at,ingested_at,event_type,content_revision,content_digest,audience_digest,visibility,acl_version,retention_policy,purge_generation,provenance,structured_refs)
VALUES('organization_project_test','turn_foreign',1,1,1,'turn-foreign','chat','thread_foreign','thread_foreign','person_other','Other',clock_timestamp(),clock_timestamp(),'message',1,decode($1,'hex'),sha256(convert_to($2::jsonb::text,'UTF8')),'private',1,'organization_default',1,'client','[]')`, digest, foreignAudience); err != nil {
		t.Fatal(err)
	}
	if err := insertReceipt("source_foreign", "turn_foreign", foreignAudience, strings.Repeat("2", 64)); err == nil {
		t.Fatal("private conversation event outside the held person's audience minted a Project source receipt")
	}
	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_conversation_events(tenant_id,event_id,event_revision,sequence,schema_version,idempotency_key,source_type,source_id,thread_id,author_principal,author_name,occurred_at,ingested_at,event_type,content_revision,content_digest,audience_digest,visibility,acl_version,retention_policy,purge_generation,provenance,structured_refs,invalidated_at,invalidation_reason)
VALUES('organization_project_test','turn_revoked',1,2,1,'turn-revoked','chat','thread_revoked','thread_revoked','person_project_test','AJ',clock_timestamp(),clock_timestamp(),'message',1,decode($1,'hex'),sha256(convert_to($2::jsonb::text,'UTF8')),'private',1,'organization_default',1,'client','[]',clock_timestamp(),'acl_revoked')`, digest, audience); err != nil {
		t.Fatal(err)
	}
	if err := insertReceipt("source_revoked", "turn_revoked", audience, strings.Repeat("3", 64)); err == nil {
		t.Fatal("invalidated conversation event minted a Project source receipt")
	}

	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_conversation_events(tenant_id,event_id,event_revision,sequence,schema_version,idempotency_key,source_type,source_id,thread_id,author_principal,author_name,occurred_at,ingested_at,event_type,content_revision,content_digest,audience_digest,visibility,acl_version,retention_policy,purge_generation,provenance,structured_refs)
VALUES('organization_project_test','turn_current',1,3,1,'turn-current','chat','thread_current','thread_current','person_project_test','AJ',clock_timestamp(),clock_timestamp(),'message',1,decode($1,'hex'),sha256(convert_to($2::jsonb::text,'UTF8')),'private',1,'organization_default',1,'client','[]')`, digest, audience); err != nil {
		t.Fatal(err)
	}
	if err := insertReceipt("source_current", "turn_current", audience, strings.Repeat("4", 64)); err != nil {
		t.Fatalf("current authorized canonical event failed to mint receipt: %v", err)
	}
	controllers := `[{"contractType":"organization_membership","id":"membership_project_test","revision":1,"digest":"` + strings.Repeat("3", 64) + `"}]`
	projectAudience := `{"visibility":"project","principals":["person_project_test"]}`
	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_project_revisions(project_id,revision,organization_id,title,aliases,lifecycle,retention_policy,controller_memberships,audience,acl_revision,creator_person_id,created_at,updated_at,content_digest) VALUES('project_source_stale',1,'organization_project_test','Stale Source','[]','active','organization_default',$1::jsonb,$2::jsonb,1,'person_project_test',clock_timestamp(),clock_timestamp(),decode($3,'hex'))`, controllers, projectAudience, strings.Repeat("4", 64)); err != nil {
		t.Fatal(err)
	}
	if err := execProjectSQLBatch(ctx, store,
		projectSQLStatement{`INSERT INTO stride_project_thread_binding_revisions(binding_id,revision,organization_id,project_id,project_revision,thread_id,kind,state,thread_audience_revision,thread_acl_digest,actor_person_id,actor_membership_id,actor_membership_revision,bound_at,content_digest)
VALUES('binding_project_source_stale',1,'organization_project_test','project_source_stale',1,'thread_current','primary','active',1,decode($1,'hex'),'person_project_test','membership_project_test',1,clock_timestamp(),decode($2,'hex'))`, []any{strings.Repeat("b", 64), strings.Repeat("c", 64)}},
		projectSQLStatement{`INSERT INTO stride_project_operation_receipts(operation_id,organization_id,operation_kind,project_id,project_revision,binding_id,binding_revision,actor_person_id,actor_membership_id,actor_membership_revision,session_subject_digest,session_revision,authority_generation,idempotency_key_digest,request_fingerprint,recorded_at)
VALUES('operation_project_source_stale_create','organization_project_test','create_project','project_source_stale',1,'binding_project_source_stale',1,'person_project_test','membership_project_test',1,decode($1,'hex'),1,1,decode($2,'hex'),decode($3,'hex'),clock_timestamp())`, []any{session, strings.Repeat("d", 64), strings.Repeat("e", 64)}},
		projectSQLStatement{`INSERT INTO stride_projects_current(project_id,revision,organization_id,lifecycle,content_digest,updated_at)
VALUES('project_source_stale',1,'organization_project_test','active',decode($1,'hex'),clock_timestamp())`, []any{strings.Repeat("4", 64)}},
		projectSQLStatement{`INSERT INTO stride_project_thread_bindings_current(binding_id,revision,organization_id,project_id,thread_id,kind,state,content_digest,updated_at)
VALUES('binding_project_source_stale',1,'organization_project_test','project_source_stale','thread_current','primary','active',decode($1,'hex'),clock_timestamp())`, []any{strings.Repeat("c", 64)}},
	); err != nil {
		t.Fatal(err)
	}
	refs := `[{"contractType":"conversation_event","id":"turn_current","revision":1,"digest":"` + digest + `"}]`
	var aclDigest string
	if err := store.pool.QueryRow(ctx, `SELECT encode(sha256(convert_to(concat_ws(E'\x1f',tenant_id,event_id,content_revision::text,encode(content_digest,'hex'),encode(audience_digest,'hex'),visibility,acl_version::text,purge_generation::text),'UTF8')),'hex') FROM stride_conversation_events WHERE tenant_id='organization_project_test' AND event_id='turn_current'`).Scan(&aclDigest); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_project_association_revisions(association_id,revision,organization_id,project_id,project_revision,subject_contract_type,subject_id,subject_revision,subject_digest,source_refs,source_authority_receipt_id,evidence_coverage_digest,state,basis,classifier_revision,confidence,actor_person_id,actor_membership_id,actor_membership_revision,session_subject_digest,session_revision,authority_generation,source_audience,source_acl_revision,source_acl_digest,consent_revision,purge_generation,idempotency_key_digest,expires_at,recorded_at,content_digest)
	VALUES('association_stale_source',1,'organization_project_test','project_source_stale',1,'conversation_event','turn_current',1,decode($1,'hex'),$2::jsonb,'source_current',decode($3,'hex'),'proposed','suggested','project_linker_v1',0.9,'person_project_test','membership_project_test',1,decode($4,'hex'),1,1,$5::jsonb,1,decode($6,'hex'),1,1,decode($7,'hex'),clock_timestamp()+interval '10 minutes',clock_timestamp(),decode($8,'hex'))`, digest, refs, coverage, session, audience, aclDigest, strings.Repeat("5", 64), strings.Repeat("6", 64)); err != nil {
		t.Fatalf("current source association rejected: %v", err)
	}
	if err := execProjectSQLBatch(ctx, store,
		projectSQLStatement{`INSERT INTO stride_project_association_events(event_id,organization_id,association_id,association_revision,action,resulting_state,prior_revision,new_revision,actor_person_id,actor_membership_id,actor_membership_revision,session_subject_digest,session_revision,authority_generation,idempotency_key_digest,request_fingerprint,occurred_at) VALUES('event_stale_source_propose','organization_project_test','association_stale_source',1,'propose','proposed',0,1,'person_project_test','membership_project_test',1,decode($1,'hex'),1,1,decode($2,'hex'),decode($3,'hex'),clock_timestamp())`, []any{session, strings.Repeat("5", 64), strings.Repeat("7", 64)}},
		projectSQLStatement{`INSERT INTO stride_project_associations_current(association_id,revision,organization_id,project_id,state,content_digest,updated_at) VALUES('association_stale_source',1,'organization_project_test','project_source_stale','proposed',decode($1,'hex'),clock_timestamp())`, []any{strings.Repeat("6", 64)}},
	); err != nil {
		t.Fatal(err)
	}
	if err := execProjectSQLBatch(ctx, store,
		projectSQLStatement{`INSERT INTO stride_project_association_revisions(association_id,revision,organization_id,project_id,project_revision,subject_contract_type,subject_id,subject_revision,subject_digest,source_refs,source_authority_receipt_id,evidence_coverage_digest,state,basis,classifier_revision,confidence,actor_person_id,actor_membership_id,actor_membership_revision,session_subject_digest,session_revision,authority_generation,source_audience,source_acl_revision,source_acl_digest,consent_revision,purge_generation,idempotency_key_digest,expires_at,supersedes_revision,supersedes_digest,recorded_at,content_digest)
SELECT association_id,2,organization_id,project_id,project_revision,subject_contract_type,subject_id,subject_revision,subject_digest,source_refs,source_authority_receipt_id,evidence_coverage_digest,'confirmed',basis,classifier_revision,confidence,actor_person_id,actor_membership_id,actor_membership_revision,session_subject_digest,session_revision,authority_generation,source_audience,source_acl_revision,source_acl_digest,consent_revision,purge_generation,decode($1,'hex'),NULL,1,content_digest,clock_timestamp(),decode($2,'hex') FROM stride_project_association_revisions WHERE association_id='association_stale_source' AND revision=1`, []any{strings.Repeat("c", 64), strings.Repeat("d", 64)}},
		projectSQLStatement{`INSERT INTO stride_project_association_events(event_id,organization_id,association_id,association_revision,action,resulting_state,prior_revision,new_revision,actor_person_id,actor_membership_id,actor_membership_revision,session_subject_digest,session_revision,authority_generation,idempotency_key_digest,request_fingerprint,occurred_at) VALUES('event_stale_source_confirm','organization_project_test','association_stale_source',2,'confirm','confirmed',1,2,'person_project_test','membership_project_test',1,decode($1,'hex'),1,1,decode($2,'hex'),decode($3,'hex'),clock_timestamp())`, []any{session, strings.Repeat("c", 64), strings.Repeat("e", 64)}},
		projectSQLStatement{`UPDATE stride_project_associations_current SET revision=2,state='confirmed',content_digest=decode($1,'hex'),updated_at=clock_timestamp() WHERE association_id='association_stale_source'`, []any{strings.Repeat("d", 64)}},
	); err != nil {
		t.Fatal(err)
	}
	var visible int
	if err := store.pool.QueryRow(ctx, `SELECT count(*) FROM stride_project_associations_authorized_current WHERE association_id='association_stale_source'`).Scan(&visible); err != nil || visible != 1 {
		t.Fatalf("current authorized association missing before revoke: %d %v", visible, err)
	}
	if _, err := store.pool.Exec(ctx, `UPDATE stride_conversation_events SET invalidated_at=clock_timestamp(),invalidation_reason='acl_revoked' WHERE tenant_id='organization_project_test' AND event_id='turn_current'`); err != nil {
		t.Fatal(err)
	}
	if err := store.pool.QueryRow(ctx, `SELECT count(*) FROM stride_project_associations_authorized_current WHERE association_id='association_stale_source'`).Scan(&visible); err != nil || visible != 0 {
		t.Fatalf("revoked source remained in authorized current Project truth: %d %v", visible, err)
	}
	var purgeJobs int
	if err := store.pool.QueryRow(ctx, `SELECT count(*) FROM stride_project_projection_outbox WHERE association_id='association_stale_source' AND operation='purge'`).Scan(&purgeJobs); err != nil || purgeJobs != 4 {
		t.Fatalf("source revoke did not enqueue all Project projection purges: %d %v", purgeJobs, err)
	}
	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_project_association_revisions(association_id,revision,organization_id,project_id,project_revision,subject_contract_type,subject_id,subject_revision,subject_digest,source_refs,source_authority_receipt_id,evidence_coverage_digest,state,basis,classifier_revision,confidence,actor_person_id,actor_membership_id,actor_membership_revision,session_subject_digest,session_revision,authority_generation,source_audience,source_acl_revision,source_acl_digest,consent_revision,purge_generation,idempotency_key_digest,expires_at,recorded_at,content_digest)
VALUES('association_after_stale',1,'organization_project_test','project_source_stale',1,'conversation_event','turn_current',1,decode($1,'hex'),$2::jsonb,'source_current',decode($3,'hex'),'proposed','suggested','project_linker_v1',0.9,'person_project_test','membership_project_test',1,decode($4,'hex'),1,1,$5::jsonb,1,decode($6,'hex'),1,1,decode($7,'hex'),clock_timestamp()+interval '10 minutes',clock_timestamp(),decode($8,'hex'))`, digest, refs, coverage, session, audience, aclDigest, strings.Repeat("8", 64), strings.Repeat("9", 64)); err == nil {
		t.Fatal("stale source authority receipt survived canonical source invalidation")
	}
}

func TestPostgresProjectIdempotencyIsOperationScopedAndFingerprintBound(t *testing.T) {
	ctx, store, _ := migratedPostgresCanonicalStore(t)
	seedProjectPostgresAuthority(t, ctx, store)
	controllers := `[{"contractType":"organization_membership","id":"membership_project_test","revision":1,"digest":"` + strings.Repeat("3", 64) + `"}]`
	audience := `{"visibility":"project","principals":["person_project_test"]}`
	for _, projectID := range []string{"project_idempotency_a", "project_idempotency_b"} {
		if _, err := store.pool.Exec(ctx, `INSERT INTO stride_project_revisions(project_id,revision,organization_id,title,aliases,lifecycle,retention_policy,controller_memberships,audience,acl_revision,creator_person_id,created_at,updated_at,content_digest) VALUES($1,1,'organization_project_test',$1,'[]','active','organization_default',$2::jsonb,$3::jsonb,1,'person_project_test',clock_timestamp(),clock_timestamp(),decode($4,'hex'))`, projectID, controllers, audience, strings.Repeat("4", 64)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_project_thread_binding_revisions(binding_id,revision,organization_id,project_id,project_revision,thread_id,kind,state,thread_audience_revision,thread_acl_digest,actor_person_id,actor_membership_id,actor_membership_revision,bound_at,content_digest) VALUES('binding_idempotency_b',1,'organization_project_test','project_idempotency_b',1,'thread_idempotency_b','primary','active',1,decode($1,'hex'),'person_project_test','membership_project_test',1,clock_timestamp(),decode($2,'hex'))`, strings.Repeat("5", 64), strings.Repeat("6", 64)); err != nil {
		t.Fatal(err)
	}
	key := strings.Repeat("7", 64)
	fingerprint := strings.Repeat("8", 64)
	insertRevise := func(id, projectID, requestFingerprint string) error {
		_, err := store.pool.Exec(ctx, `INSERT INTO stride_project_operation_receipts(operation_id,organization_id,operation_kind,project_id,project_revision,actor_person_id,actor_membership_id,actor_membership_revision,session_subject_digest,session_revision,authority_generation,idempotency_key_digest,request_fingerprint,recorded_at) VALUES($1,'organization_project_test','revise_project',$2,1,'person_project_test','membership_project_test',1,decode($3,'hex'),1,1,decode($4,'hex'),decode($5,'hex'),clock_timestamp())`, id, projectID, strings.Repeat("2", 64), key, requestFingerprint)
		return err
	}
	if err := insertRevise("operation_idempotency_revise", "project_idempotency_a", fingerprint); err != nil {
		t.Fatal(err)
	}
	if replay, err := store.ResolveProjectOperationReplay(ctx, "organization_project_test", projectOperationReviseProject, key, fingerprint); err != nil || !replay {
		t.Fatalf("exact durable lost-response retry did not replay: replay=%v err=%v", replay, err)
	}
	if replay, err := store.ResolveProjectOperationReplay(ctx, "organization_project_test", projectOperationReviseProject, key, strings.Repeat("9", 64)); !errors.Is(err, ErrProjectAuthorityConflict) || replay {
		t.Fatalf("different fingerprint replay did not conflict: replay=%v err=%v", replay, err)
	}
	if replay, err := store.ResolveProjectOperationReplay(ctx, "organization_project_test", projectOperationCreateProject, key, fingerprint); err != nil || replay {
		t.Fatalf("different operation reused durable replay before its own write: replay=%v err=%v", replay, err)
	}
	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_project_operation_receipts(operation_id,organization_id,operation_kind,project_id,project_revision,binding_id,binding_revision,actor_person_id,actor_membership_id,actor_membership_revision,session_subject_digest,session_revision,authority_generation,idempotency_key_digest,request_fingerprint,recorded_at) VALUES('operation_idempotency_create','organization_project_test','create_project','project_idempotency_b',1,'binding_idempotency_b',1,'person_project_test','membership_project_test',1,decode($1,'hex'),1,1,decode($2,'hex'),decode($3,'hex'),clock_timestamp())`, strings.Repeat("2", 64), key, fingerprint); err != nil {
		t.Fatalf("same key collided across operation scopes: %v", err)
	}
	if replay, err := store.ResolveProjectOperationReplay(ctx, "organization_project_test", projectOperationCreateProject, key, fingerprint); err != nil || !replay {
		t.Fatalf("exact create replay failed after durable write: replay=%v err=%v", replay, err)
	}
	var stored string
	if err := store.pool.QueryRow(ctx, `SELECT encode(request_fingerprint,'hex') FROM stride_project_operation_receipts WHERE organization_id='organization_project_test' AND operation_kind='revise_project' AND idempotency_key_digest=decode($1,'hex')`, key).Scan(&stored); err != nil || stored != fingerprint {
		t.Fatalf("exact replay fingerprint unavailable: %q %v", stored, err)
	}
	if err := insertRevise("operation_idempotency_revise_conflict", "project_idempotency_b", strings.Repeat("9", 64)); err == nil {
		t.Fatal("same operation/key accepted a different request fingerprint")
	}
}
