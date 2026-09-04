package business

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresBusinessOverviewEvidenceAndIsolation(t *testing.T) {
	s, _, pool := testStore(t)
	ctx := context.Background()
	f := makeFixture(t, s)
	w := admit(t, s, f)
	a := prepare(t, s, f, claim(t, s, f, w))
	if _, err := s.CompleteAttempt(ctx, f.scope, completeArgs(a, nil)); err != nil {
		t.Fatal(err)
	}
	view, err := s.Overview(ctx, f.scope, f.result.Business.ID)
	if err != nil || view.UnknownCostOperations != 1 || view.Budget.ReservedMicros != 60 || view.Budget.SettledMicros != 0 || len(view.Team) != 1 || len(view.Work) != 1 {
		t.Fatalf("unknown liability: %+v %v", view, err)
	}
	reopened, err := New(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	detail, err := reopened.ReadWorkDetail(ctx, f.scope, f.result.Business.ID, w.ID)
	if err != nil || detail.Result == nil || detail.Result.Content != "A private result with evidence." || len(detail.Attempts) != 1 || detail.Attempts[0].OutcomeEvidenceRef != "test-result-receipt" {
		t.Fatalf("durable evidence: %+v %v", detail, err)
	}
	other := makeFixture(t, s)
	if _, err = reopened.ReadWorkDetail(ctx, other.scope, f.result.Business.ID, w.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign result", err)
	}
	args := setupArgs()
	args.OrganizationID = f.scope.OrganizationID
	sibling, err := s.SetupBusiness(ctx, f.scope.Actor, args)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ReadWorkDetail(ctx, f.scope, sibling.Business.ID, w.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("sibling business mismatch", err)
	}
	if _, err = s.RevokeMandate(ctx, f.scope, RevokeMandateArgs{uuid.NewString(), f.mandate.ID, 1}); err != nil {
		t.Fatal(err)
	}
	detail, err = s.ReadWorkDetail(ctx, f.scope, f.result.Business.ID, w.ID)
	if err != nil || detail.Result == nil || detail.Result.Eligible || detail.Result.IneligibleReason != "authority_changed" {
		t.Fatalf("revoked eligibility: %+v %v", detail, err)
	}
	// Revoking a reader removes their access, including after an earlier read.
	reader := Actor{"person", uuid.NewString()}
	m, err := s.AddMember(ctx, f.scope, MemberArgs{IdempotencyKey: uuid.NewString(), PersonID: reader.ID, Role: "member"})
	if err != nil {
		t.Fatal(err)
	}
	readerScope := Scope{f.scope.OrganizationID, reader}
	if _, err = s.ReadWorkDetail(ctx, readerScope, f.result.Business.ID, w.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.RevokeMembership(ctx, f.scope, MemberArgs{IdempotencyKey: uuid.NewString(), PersonID: reader.ID, ExpectedRevision: m.Revision}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ReadWorkDetail(ctx, readerScope, f.result.Business.ID, w.ID); !errors.Is(err, ErrDenied) {
		t.Fatal("revoked reader", err)
	}
}

func TestPostgresOverviewProjectionBackfillAndAtomicity(t *testing.T) {
	_, admin, runtime := testStore(t)
	ctx := context.Background()
	database := "overview_migration_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, e := admin.Exec(ctx, `CREATE DATABASE `+pgx.Identifier{database}.Sanitize()); e != nil {
		t.Fatal(e)
	}
	adminConfig := admin.Config().Copy()
	adminConfig.ConnConfig.Database = database
	oldAdmin, e := pgxpool.NewWithConfig(ctx, adminConfig)
	if e != nil {
		t.Fatal(e)
	}
	runtimeConfig := runtime.Config().Copy()
	runtimeConfig.ConnConfig.Database = database
	oldRuntime, e := pgxpool.NewWithConfig(ctx, runtimeConfig)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		oldRuntime.Close()
		oldAdmin.Close()
		_, e := admin.Exec(ctx, `DROP DATABASE `+pgx.Identifier{database}.Sanitize())
		if e != nil {
			t.Error(e)
		}
	})
	if _, e = oldAdmin.Exec(ctx, `CREATE SCHEMA business; CREATE TABLE business.schema_migrations(version text PRIMARY KEY,digest text NOT NULL)`); e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"001_business.sql", "002_attempts.sql", "004_provider_journal.sql"} {
		raw, e := migrationFiles.ReadFile("migrations/" + name)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = oldAdmin.Exec(ctx, string(raw)); e != nil {
			t.Fatal(e)
		}
		if _, e = oldAdmin.Exec(ctx, `INSERT INTO business.schema_migrations VALUES($1,$2)`, strings.SplitN(name, "_", 2)[0], strings.TrimPrefix(contentDigest(string(raw)), "sha256:")); e != nil {
			t.Fatal(e)
		}
	}
	// Exercise the current writer with only the overview migration omitted.
	// New must reject incomplete startup. This fixture is not an old binary
	// compatibility test; provider authority tables are required by current code.
	if _, e = New(ctx, oldRuntime); e == nil {
		t.Fatal("accepted missing overview migration")
	}
	oldStore := &Store{pool: oldRuntime}
	f := makeFixture(t, oldStore)
	w := admit(t, oldStore, f)
	a := prepare(t, oldStore, f, claim(t, oldStore, f, w))
	if e = Migrate(ctx, oldAdmin); e != nil {
		t.Fatal(e)
	}
	current, e := New(ctx, oldRuntime)
	if e != nil {
		t.Fatal(e)
	}
	view, e := current.Overview(ctx, f.scope, w.BusinessID)
	if e != nil || view.UnknownCostOperations != 1 || view.UnknownCostMore {
		t.Fatalf("backfill %+v %v", view, e)
	}
	tx, e := scopeTx(ctx, oldRuntime, f.scope)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = tx.Exec(ctx, `UPDATE business.attempts SET body=jsonb_set(body,'{costState}','"known"') WHERE organization_id=$1 AND id=$2`, f.scope.OrganizationID, a.ID); e != nil {
		t.Fatal(e)
	}
	var n int
	if e = tx.QueryRow(ctx, `SELECT count(*) FROM business.unknown_cost_operations WHERE organization_id=$1 AND business_id=$2`, f.scope.OrganizationID, w.BusinessID).Scan(&n); e != nil || n != 0 {
		t.Fatalf("transaction did not update projection %d %v", n, e)
	}
	if e = tx.Rollback(ctx); e != nil {
		t.Fatal(e)
	}
	view, e = current.Overview(ctx, f.scope, w.BusinessID)
	if e != nil || view.UnknownCostOperations != 1 {
		t.Fatalf("rollback lost projection %+v %v", view, e)
	}
	// Normal current-writer completion triggers projection removal and actual
	// settlement in the same commit.
	cost := int64(11)
	if _, e = oldStore.CompleteAttempt(ctx, f.scope, completeArgs(a, &cost)); e != nil {
		t.Fatal(e)
	}
	view, e = current.Overview(ctx, f.scope, w.BusinessID)
	if e != nil || view.UnknownCostOperations != 0 || view.Budget.SettledMicros != 11 {
		t.Fatalf("retained writer %+v %v", view, e)
	}
}

func TestPostgresOverviewIndexedHistoryAndBoundedCount(t *testing.T) {
	s, admin, runtime := testStore(t)
	ctx := context.Background()
	f := makeFixture(t, s)
	w := admit(t, s, f)
	args := setupArgs()
	args.OrganizationID = f.scope.OrganizationID
	sibling, e := s.SetupBusiness(ctx, f.scope.Actor, args)
	if e != nil {
		t.Fatal(e)
	}
	siblingEmp, e := s.CreateEmployment(ctx, f.scope, EmploymentArgs{IdempotencyKey: uuid.NewString(), BusinessID: sibling.Business.ID, Name: "Sibling", OfferingID: "fixture", OfferingVersion: "1", OfferingDigest: "test"})
	if e != nil {
		t.Fatal(e)
	}
	// Synthetic persisted history is a query-shape fixture, not provider execution
	// evidence. Both Businesses have large histories, with bounded unknown subsets.
	prefix := "history_" + uuid.NewString() + "_"
	for _, target := range []struct{ bid, emp, part string }{{w.BusinessID, f.employment.ID, "target_"}, {sibling.Business.ID, siblingEmp.ID, "sibling_"}} {
		_, e = admin.Exec(ctx, `INSERT INTO business.work_intents(organization_id,id,business_id,employment_id,mandate_id,body)
   SELECT $1,$2||i::text,$3,$4,$5,$6::jsonb||jsonb_build_object('id',$2||i::text,'businessId',$3::text,'employmentId',$4::text,'status','failed','heldMicros',0,'createdAt',to_char(timestamp '2026-01-01'+i*interval '1 second','YYYY-MM-DD"T"HH24:MI:SS.US"Z"')) FROM generate_series(1,12000) i`, f.scope.OrganizationID, prefix+target.part, target.bid, target.emp, f.mandate.ID, jsonBytes(w))
		if e != nil {
			t.Fatal(e)
		}
		unknownCount := 150
		if target.part == "sibling_" {
			unknownCount = 12000
		}
		_, e = admin.Exec(ctx, `INSERT INTO business.attempts(organization_id,id,work_id,ordinal,body)
   SELECT $1,$2||'attempt_'||i::text,$2||i::text,1,jsonb_build_object('id',$2||'attempt_'||i::text,'workId',$2||i::text,'ordinal',1,'costState','unknown','state','completed_unsettled') FROM generate_series(1,$3::integer) i`, f.scope.OrganizationID, prefix+target.part, unknownCount)
		if e != nil {
			t.Fatal(e)
		}
	}
	_, e = admin.Exec(ctx, `INSERT INTO business.employments(organization_id,id,business_id,body) SELECT $1,$2||i::text,$3,$4::jsonb||jsonb_build_object('id',$2||i::text) FROM generate_series(1,150) i`, f.scope.OrganizationID, "team_"+uuid.NewString()+"_", w.BusinessID, jsonBytes(f.employment))
	if e != nil {
		t.Fatal(e)
	}
	_, e = admin.Exec(ctx, `INSERT INTO business.employments(organization_id,id,business_id,body) SELECT $1,$2||i::text,$3,$4::jsonb||jsonb_build_object('id',$2||i::text,'businessId',$3::text) FROM generate_series(1,12000) i`, f.scope.OrganizationID, "sibling_team_"+uuid.NewString()+"_", sibling.Business.ID, jsonBytes(siblingEmp))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = admin.Exec(ctx, `ANALYZE business.work_intents; ANALYZE business.employments; ANALYZE business.unknown_cost_operations`); e != nil {
		t.Fatal(e)
	}
	view, e := s.Overview(ctx, f.scope, w.BusinessID)
	if e != nil || len(view.Work) != 100 || !view.WorkMore || len(view.Team) != 100 || !view.TeamMore || view.UnknownCostOperations != 100 || !view.UnknownCostMore {
		t.Fatalf("bounded overview %+v %v", view, e)
	}
	tx, e := scopeTx(ctx, runtime, f.scope)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(ctx)
	for _, test := range []struct{ name, query, index string }{{"work", overviewWorkQuery, "business_work_overview_recent"}, {"team", overviewTeamQuery, "business_employment_overview"}, {"unknown", overviewUnknownCostQuery, "unknown_cost_operations_pkey"}} {
		var raw []byte
		if e = tx.QueryRow(ctx, `EXPLAIN (ANALYZE,BUFFERS,FORMAT JSON) `+test.query, f.scope.OrganizationID, w.BusinessID).Scan(&raw); e != nil {
			t.Fatal(e)
		}
		var plans []struct {
			Plan map[string]any `json:"Plan"`
		}
		if e = json.Unmarshal(raw, &plans); e != nil {
			t.Fatal(e)
		}
		found := false
		var walk func(map[string]any)
		walk = func(node map[string]any) {
			kind, _ := node["Node Type"].(string)
			if kind == "Seq Scan" || kind == "Sort" {
				t.Fatalf("%s scanned/sorted history: %s", test.name, raw)
			}
			if node["Index Name"] == test.index {
				found = true
				if rows, _ := node["Actual Rows"].(float64); rows > 101 {
					t.Fatalf("%s read too many qualifying index rows: %s", test.name, raw)
				}
			}
			if children, ok := node["Plans"].([]any); ok {
				for _, child := range children {
					walk(child.(map[string]any))
				}
			}
		}
		walk(plans[0].Plan)
		if !found {
			t.Fatalf("%s did not use expected bounded index: %s", test.name, raw)
		}
		t.Logf("%s plan uses %s with at most101 qualifying rows", test.name, test.index)
	}
	// Same-organization Business spoofing is rejected, not merely cross-tenant RLS.
	if _, e = tx.Exec(ctx, `INSERT INTO business.unknown_cost_operations VALUES($1,$2,$3)`, f.scope.OrganizationID, sibling.Business.ID, prefix+"target_attempt_1"); e == nil {
		t.Fatal("wrong Business projection accepted")
	}
	tx.Rollback(ctx)
	other := makeFixture(t, s)
	otherTx, e := scopeTx(ctx, runtime, other.scope)
	if e != nil {
		t.Fatal(e)
	}
	defer otherTx.Rollback(ctx)
	var n int
	if e = otherTx.QueryRow(ctx, `SELECT count(*) FROM business.unknown_cost_operations WHERE organization_id=$1`, f.scope.OrganizationID).Scan(&n); e != nil || n != 0 {
		t.Fatalf("foreign projection leaked %d %v", n, e)
	}
	if _, e = otherTx.Exec(ctx, `INSERT INTO business.unknown_cost_operations VALUES($1,$2,$3)`, f.scope.OrganizationID, w.BusinessID, prefix+"target_attempt_1"); e == nil {
		t.Fatal("foreign projection insertion accepted")
	}
}
