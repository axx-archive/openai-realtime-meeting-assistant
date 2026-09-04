package business

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func lease(a Attempt) AttemptLease { return AttemptLease{a.ID, a.WorkerID, a.Generation} }
func claim(t *testing.T, s *Store, f fixture, w Work) Attempt {
	t.Helper()
	a, e := s.ClaimAttempt(context.Background(), f.scope, ClaimAttemptArgs{w.ID, uuid.NewString(), uuid.NewString(), 60})
	if e != nil {
		t.Fatal(e)
	}
	return a
}
func prepare(t *testing.T, s *Store, f fixture, a Attempt) Attempt {
	t.Helper()
	a, e := s.PrepareOperation(context.Background(), f.scope, PrepareOperationArgs{lease(a), Operation{uuid.NewString(), "sha256:request", "fake-test-adapter", "fixture-v1", "fixture-price-v1", 50}})
	if e != nil {
		t.Fatal(e)
	}
	return a
}
func completeArgs(a Attempt, cost *int64) CompleteAttemptArgs {
	content := "A private result with evidence."
	return CompleteAttemptArgs{lease(a), a.Operation.ID, "succeeded", content, contentDigest(content), CostEvidence{cost, "test-provider-receipt"}, "test-result-receipt"}
}
func admit(t *testing.T, s *Store, f fixture) Work {
	t.Helper()
	w, e := s.AdmitWork(context.Background(), f.scope, f.work(60))
	if e != nil {
		t.Fatal(e)
	}
	return w
}
func expire(t *testing.T, admin *pgxpool.Pool, org, aid string) {
	t.Helper()
	_, e := admin.Exec(context.Background(), `UPDATE business.attempts SET body=jsonb_set(body,'{leaseExpiresAt}',to_jsonb('2000-01-01T00:00:00Z'::text)) WHERE organization_id=$1 AND id=$2`, org, aid)
	if e != nil {
		t.Fatal(e)
	}
}
func TestPostgresAttemptPersistence(t *testing.T) {
	s, admin, runtime := testStore(t)
	ctx := context.Background()
	t.Run("schema_owner_and_migration_readiness", func(t *testing.T) {
		var role, owner string
		if e := runtime.QueryRow(ctx, `SELECT current_user`).Scan(&role); e != nil {
			t.Fatal(e)
		}
		if e := admin.QueryRow(ctx, `SELECT pg_get_userbyid(nspowner) FROM pg_namespace WHERE nspname='business'`).Scan(&owner); e != nil {
			t.Fatal(e)
		}
		if _, e := admin.Exec(ctx, `ALTER SCHEMA business OWNER TO `+pgx.Identifier{role}.Sanitize()); e != nil {
			t.Fatal(e)
		}
		_, newErr := New(ctx, runtime)
		if _, e := admin.Exec(ctx, `ALTER SCHEMA business OWNER TO `+pgx.Identifier{owner}.Sanitize()); e != nil {
			t.Fatal(e)
		}
		if newErr == nil {
			t.Fatal("schema owner accepted")
		}
		var digest string
		if e := admin.QueryRow(ctx, `DELETE FROM business.schema_migrations WHERE version='002' RETURNING digest`).Scan(&digest); e != nil {
			t.Fatal(e)
		}
		_, newErr = New(ctx, runtime)
		if _, e := admin.Exec(ctx, `INSERT INTO business.schema_migrations VALUES('002',$1)`, digest); e != nil {
			t.Fatal(e)
		}
		if newErr == nil {
			t.Fatal("unmigrated runtime accepted")
		}
	})
	t.Run("current_lists_and_other_employment_isolation", func(t *testing.T) {
		f := makeFixture(t, s)
		w := admit(t, s, f)
		es, e := s.ListEmployments(ctx, f.scope, f.result.Business.ID)
		if e != nil || len(es) != 1 || es[0].ID != f.employment.ID {
			t.Fatalf("employments %+v %v", es, e)
		}
		m, e := s.GetMembership(ctx, f.scope)
		if e != nil || m.Role != "owner" {
			t.Fatalf("membership %+v %v", m, e)
		}
		other := makeFixture(t, s)
		_, e = s.GetWork(ctx, other.scope, w.ID)
		mustErr(t, e, ErrNotFound)
		_, e = s.ClaimAttempt(ctx, Scope{f.scope.OrganizationID, Actor{"agent", other.employment.ID}}, ClaimAttemptArgs{w.ID, "worker", "key", 60})
		mustErr(t, e, ErrDenied)
	})
	t.Run("competing_claimers_and_database_expiry", func(t *testing.T) {
		f := makeFixture(t, s)
		w := admit(t, s, f)
		var wg sync.WaitGroup
		var mu sync.Mutex
		var winners []Attempt
		var failures []error
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				a, e := s.ClaimAttempt(ctx, f.scope, ClaimAttemptArgs{w.ID, uuid.NewString(), uuid.NewString(), 60})
				mu.Lock()
				defer mu.Unlock()
				if e == nil {
					winners = append(winners, a)
				} else if !errors.Is(e, ErrConcurrency) {
					failures = append(failures, e)
				}
			}()
		}
		wg.Wait()
		if len(winners) != 1 || len(failures) != 0 {
			t.Fatalf("claims %d %v", len(winners), failures)
		}
		a := winners[0]
		replayed, e := s.ClaimAttempt(ctx, f.scope, ClaimAttemptArgs{w.ID, a.WorkerID, a.ClaimKey, 60})
		if e != nil || !replayed.LeaseExpiresAt.Equal(a.LeaseExpiresAt) {
			t.Fatalf("duplicate extended lease %+v %v", replayed, e)
		}
		expire(t, admin, f.scope.OrganizationID, a.ID)
		_, e = s.RenewAttempt(ctx, f.scope, lease(a), 60)
		mustErr(t, e, ErrLease)
		next := claim(t, s, f, w)
		if next.ID != a.ID || next.Generation <= a.Generation || next.Mode != "execute" {
			t.Fatalf("unissued reclaim %+v", next)
		}
		_, e = s.PrepareOperation(ctx, f.scope, PrepareOperationArgs{lease(a), Operation{"old", "digest", "fake", "r1", "p1", 1}})
		mustErr(t, e, ErrLease)
	})
	t.Run("issuance_idempotency_restart_and_reconciliation", func(t *testing.T) {
		f := makeFixture(t, s)
		w := admit(t, s, f)
		a := prepare(t, s, f, claim(t, s, f, w))
		same, e := s.PrepareOperation(ctx, f.scope, PrepareOperationArgs{lease(a), *a.Operation})
		if e != nil || same.ID != a.ID {
			t.Fatalf("issuance replay %+v %v", same, e)
		}
		changed := *a.Operation
		changed.RequestDigest = "changed"
		_, e = s.PrepareOperation(ctx, f.scope, PrepareOperationArgs{lease(a), changed})
		mustErr(t, e, ErrConflict)
		expire(t, admin, f.scope.OrganizationID, a.ID)
		p, e := pgxpool.NewWithConfig(ctx, runtime.Config().Copy())
		if e != nil {
			t.Fatal(e)
		}
		defer p.Close()
		fresh, e := New(ctx, p)
		if e != nil {
			t.Fatal(e)
		}
		recovered := claim(t, fresh, f, w)
		if recovered.ID != a.ID || recovered.Mode != "reconcile" || recovered.Operation.ID != a.Operation.ID {
			t.Fatalf("lost issuance %+v", recovered)
		}
		_, e = fresh.PrepareOperation(ctx, f.scope, PrepareOperationArgs{lease(recovered), *recovered.Operation})
		mustErr(t, e, ErrReconciliation)
		cost := int64(25)
		_, e = fresh.CompleteAttempt(ctx, f.scope, completeArgs(a, &cost))
		mustErr(t, e, ErrLease)
		out, e := fresh.ReconcileAttempt(ctx, f.scope, ReconcileAttemptArgs(completeArgs(recovered, &cost)))
		if e != nil || out.Work.Status != "completed" {
			t.Fatalf("reconcile %+v %v", out, e)
		}
	})
	t.Run("exactly_one_result_and_one_charge", func(t *testing.T) {
		f := makeFixture(t, s)
		w := admit(t, s, f)
		a := prepare(t, s, f, claim(t, s, f, w))
		cost := int64(20)
		in := completeArgs(a, &cost)
		var wg sync.WaitGroup
		var mu sync.Mutex
		var outs []AttemptCompletion
		var errs []error
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				out, e := s.CompleteAttempt(ctx, f.scope, in)
				mu.Lock()
				defer mu.Unlock()
				if e != nil {
					errs = append(errs, e)
				} else {
					outs = append(outs, out)
				}
			}()
		}
		wg.Wait()
		if len(errs) > 0 || len(outs) != 8 {
			t.Fatalf("completion errors %v", errs)
		}
		for _, o := range outs {
			if o.Result == nil || o.Result.ID != outs[0].Result.ID {
				t.Fatal("multiple results")
			}
		}
		b, e := s.GetBudget(ctx, f.scope, w.BusinessID)
		if e != nil || b.SettledMicros != 20 || b.ReservedMicros != 0 {
			t.Fatalf("settlement %+v %v", b, e)
		}
		in.Content = "different"
		in.ContentDigest = contentDigest(in.Content)
		_, e = s.CompleteAttempt(ctx, f.scope, in)
		mustErr(t, e, ErrConflict)
		r, e := s.GetResult(ctx, f.scope, w.ID)
		if e != nil || r.Digest != outs[0].Result.Digest {
			t.Fatalf("result %+v %v", r, e)
		}
		if _, e = runtime.Exec(ctx, `UPDATE business.results SET body='{}'`); e == nil {
			t.Fatal("result mutation permitted")
		}
	})
	t.Run("unknown_cost_is_not_zero_and_blocks_new_work", func(t *testing.T) {
		f := makeFixture(t, s)
		w := admit(t, s, f)
		a := prepare(t, s, f, claim(t, s, f, w))
		out, e := s.CompleteAttempt(ctx, f.scope, completeArgs(a, nil))
		if e != nil || out.Work.Status != "reconciling" || out.Attempt.CostState != "unknown" {
			t.Fatalf("unknown %+v %v", out, e)
		}
		b, e := s.GetBudget(ctx, f.scope, w.BusinessID)
		if e != nil || b.SettledMicros != 0 || b.ReservedMicros != 60 {
			t.Fatalf("unknown released %+v %v", b, e)
		}
		var unknown bool
		if e = admin.QueryRow(ctx, `SELECT actual_micros IS NULL FROM business.settlements WHERE organization_id=$1 AND attempt_id=$2`, f.scope.OrganizationID, a.ID).Scan(&unknown); e != nil || !unknown {
			t.Fatalf("invented zero %v %v", unknown, e)
		}
		_, e = s.AdmitWork(ctx, f.scope, f.work(1))
		mustErr(t, e, ErrReconciliation)
		actual := int64(25)
		settled, e := s.ReconcileAttempt(ctx, f.scope, ReconcileAttemptArgs(completeArgs(out.Attempt, &actual)))
		if e != nil || settled.Result.ID != out.Result.ID || settled.Work.Status != "completed" {
			t.Fatalf("settled %+v %v", settled, e)
		}
		b, e = s.GetBudget(ctx, f.scope, w.BusinessID)
		if e != nil || b.SettledMicros != 25 || b.ReservedMicros != 0 {
			t.Fatalf("settled budget %+v %v", b, e)
		}
	})
	t.Run("actual_overage_preserved_and_blocks_even_zero_reservation", func(t *testing.T) {
		f := makeFixture(t, s)
		w := admit(t, s, f)
		a := prepare(t, s, f, claim(t, s, f, w))
		actual := int64(120)
		_, e := s.CompleteAttempt(ctx, f.scope, completeArgs(a, &actual))
		if e != nil {
			t.Fatal(e)
		}
		b, e := s.GetBudget(ctx, f.scope, w.BusinessID)
		if e != nil || b.SettledMicros != 120 || b.ReservedMicros != 0 {
			t.Fatalf("overage hidden %+v %v", b, e)
		}
		_, e = s.AdmitWork(ctx, f.scope, f.work(0))
		mustErr(t, e, ErrBudget)
	})
	t.Run("revocation_holds_issued_liability_and_withholds_success", func(t *testing.T) {
		f := makeFixture(t, s)
		w := admit(t, s, f)
		a := prepare(t, s, f, claim(t, s, f, w))
		_, e := s.RevokeMandate(ctx, f.scope, RevokeMandateArgs{uuid.NewString(), f.mandate.ID, 1})
		if e != nil {
			t.Fatal(e)
		}
		b, e := s.GetBudget(ctx, f.scope, w.BusinessID)
		if e != nil || b.ReservedMicros != 60 {
			t.Fatalf("revocation erased exposure %+v %v", b, e)
		}
		actual := int64(12)
		_, e = s.CompleteAttempt(ctx, f.scope, completeArgs(a, &actual))
		mustErr(t, e, ErrLease)
		recovered := claim(t, s, f, w)
		out, e := s.ReconcileAttempt(ctx, f.scope, ReconcileAttemptArgs(completeArgs(recovered, &actual)))
		if e != nil || out.Work.Status != "cancelled" || out.Result == nil || out.Result.Eligible {
			t.Fatalf("revoked actionable result %+v %v", out, e)
		}
		b, e = s.GetBudget(ctx, f.scope, w.BusinessID)
		if e != nil || b.ReservedMicros != 0 || b.SettledMicros != 12 {
			t.Fatalf("revoked settlement %+v %v", b, e)
		}
	})
	t.Run("unknown_result_then_takeover_reconciles_but_stays_ineligible", func(t *testing.T) {
		f := makeFixture(t, s)
		w := admit(t, s, f)
		a := prepare(t, s, f, claim(t, s, f, w))
		initial := completeArgs(a, nil)
		initial.OutcomeEvidenceRef = "provider-initial-outcome-ref"
		unknown, e := s.CompleteAttempt(ctx, f.scope, initial)
		if e != nil {
			t.Fatal(e)
		}
		reopenedPool, e := pgxpool.NewWithConfig(ctx, runtime.Config().Copy())
		if e != nil {
			t.Fatal(e)
		}
		defer reopenedPool.Close()
		reopened, e := New(ctx, reopenedPool)
		if e != nil {
			t.Fatal(e)
		}
		attempts, e := reopened.ListAttempts(ctx, f.scope, w.ID)
		if e != nil || len(attempts) != 1 || attempts[0].OutcomeEvidenceRef != initial.OutcomeEvidenceRef {
			t.Fatalf("initial outcome evidence missing after reopen %+v %v", attempts, e)
		}
		_, e = reopened.UpdateBusiness(ctx, f.scope, UpdateBusinessArgs{uuid.NewString(), w.BusinessID, 2, "paused", "human_ceo", "advise"})
		if e != nil {
			t.Fatal(e)
		}
		recovered := claim(t, reopened, f, w)
		actual := int64(7)
		final := completeArgs(recovered, &actual)
		final.OutcomeEvidenceRef = "provider-reconciled-outcome-ref"
		out, e := reopened.ReconcileAttempt(ctx, f.scope, ReconcileAttemptArgs(final))
		if e != nil || out.Result == nil || out.Result.ID != unknown.Result.ID || out.Result.Eligible || out.Work.Status != "cancelled" {
			t.Fatalf("takeover reconciliation %+v %v", out, e)
		}
		attempts, e = s.ListAttempts(ctx, f.scope, w.ID)
		if e != nil || len(attempts) != 1 || attempts[0].OutcomeEvidenceRef != final.OutcomeEvidenceRef {
			t.Fatalf("reconciled outcome evidence missing %+v %v", attempts, e)
		}
		var firstRef, lastRef string
		if e = admin.QueryRow(ctx, `SELECT body->'attempt'->>'outcomeEvidenceRef' FROM business.events WHERE organization_id=$1 AND entity_id=$2 AND kind='attempt_cost_unknown'`, f.scope.OrganizationID, a.ID).Scan(&firstRef); e != nil {
			t.Fatal(e)
		}
		if e = admin.QueryRow(ctx, `SELECT body->'result'->'attempt'->>'outcomeEvidenceRef' FROM business.events WHERE organization_id=$1 AND kind='attempt_completion'`, f.scope.OrganizationID).Scan(&lastRef); e != nil {
			t.Fatal(e)
		}
		if firstRef != initial.OutcomeEvidenceRef || lastRef != final.OutcomeEvidenceRef {
			t.Fatalf("immutable outcome trail lost %q %q", firstRef, lastRef)
		}
		r, e := s.GetResult(ctx, f.scope, w.ID)
		if e != nil || r.Eligible {
			t.Fatalf("current result eligibility %+v %v", r, e)
		}
	})

	t.Run("unissued_policy_change_fences_and_releases", func(t *testing.T) {
		f := makeFixture(t, s)
		w := admit(t, s, f)
		a := claim(t, s, f, w)
		_, e := s.UpdateBusiness(ctx, f.scope, UpdateBusinessArgs{uuid.NewString(), w.BusinessID, 2, "paused", "human_ceo", "advise"})
		if e != nil {
			t.Fatal(e)
		}
		_, e = s.RenewAttempt(ctx, f.scope, lease(a), 60)
		mustErr(t, e, ErrLease)
		b, e := s.GetBudget(ctx, f.scope, w.BusinessID)
		if e != nil || b.ReservedMicros != 0 {
			t.Fatalf("unissued held %+v %v", b, e)
		}
	})
	t.Run("not_accepted_retry_keeps_root_and_attempt_bound", func(t *testing.T) {
		f := makeFixture(t, s)
		w := admit(t, s, f)
		a := prepare(t, s, f, claim(t, s, f, w))
		zero := int64(0)
		in := CompleteAttemptArgs{lease(a), a.Operation.ID, "not_accepted", "", "", CostEvidence{&zero, "explicit-nonacceptance-cost"}, "provider-nonacceptance"}
		out, e := s.CompleteAttempt(ctx, f.scope, in)
		if e != nil || out.Work.Status != "admitted" || out.Work.HeldMicros != 60 {
			t.Fatalf("retry %+v %v", out, e)
		}
		next := claim(t, s, f, w)
		if next.ID == a.ID || next.Ordinal != 2 || next.WorkID != w.ID {
			t.Fatalf("new root or missing attempt %+v", next)
		}
		next = prepare(t, s, f, next)
		in.Lease = lease(next)
		in.OperationID = next.Operation.ID
		out, e = s.CompleteAttempt(ctx, f.scope, in)
		if e != nil || out.Work.Status != "failed" || out.Work.HeldMicros != 0 {
			t.Fatalf("unbounded retry %+v %v", out, e)
		}
		_, e = s.ClaimAttempt(ctx, f.scope, ClaimAttemptArgs{w.ID, "another", "another", 60})
		if e == nil {
			t.Fatal("exhausted work reclaimed")
		}
		as, e := s.ListAttempts(ctx, f.scope, w.ID)
		if e != nil || len(as) != 2 {
			t.Fatalf("attempts %+v %v", as, e)
		}
	})
	t.Run("same_offering_never_shares_attempt_or_result", func(t *testing.T) {
		a, b := makeFixture(t, s), makeFixture(t, s)
		wa := admit(t, s, a)
		attempt := prepare(t, s, a, claim(t, s, a, wa))
		actual := int64(10)
		out, e := s.CompleteAttempt(ctx, a.scope, completeArgs(attempt, &actual))
		if e != nil {
			t.Fatal(e)
		}
		_, e = s.GetResult(ctx, b.scope, wa.ID)
		mustErr(t, e, ErrNotFound)
		tx, e := scopeTx(ctx, runtime, b.scope)
		if e != nil {
			t.Fatal(e)
		}
		defer tx.Rollback(ctx)
		for _, table := range []string{"attempts", "results", "settlements"} {
			var n int
			e = tx.QueryRow(ctx, `SELECT count(*) FROM business.`+table+` WHERE organization_id=$1`, a.scope.OrganizationID).Scan(&n)
			if e != nil || n != 0 {
				t.Fatalf("%s leaked %d %v", table, n, e)
			}
		}
		if !strings.HasPrefix(out.Result.Digest, "sha256:") {
			t.Fatal("missing result digest")
		}
	})
}
