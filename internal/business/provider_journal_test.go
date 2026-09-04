package business

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func hostGrant(t *testing.T, issuer *ProviderAdmin, f fixture, cap int64, calls int, account string) ProviderGrant {
	t.Helper()
	g, e := issuer.IssueGrant(context.Background(), ProviderGrantArgs{
		OrganizationID: f.scope.OrganizationID, ID: id("grant"), BusinessID: f.result.Business.ID,
		AccountID: account, CredentialRef: "host-key-reference", AdapterID: "test-private-provider", RouteRevision: "route-v1", PriceRevision: "price-v1", Retention: "store_false",
		AllowanceMicros: cap, MaxOperationMicros: cap, MaxOperations: calls, ExpiresAt: time.Now().UTC().Add(time.Hour),
		RouteSnapshot: json.RawMessage(`{"model":"test-only"}`), PriceSnapshot: json.RawMessage(`{"currency":"USD","inputMicros":1}`),
	})
	if e != nil {
		t.Fatal(e)
	}
	return g
}
func providerWork(t *testing.T, s *Store, f fixture, g ProviderGrant, cost int64) Work {
	t.Helper()
	w, e := s.AdmitProviderWork(context.Background(), f.scope, ProviderWorkArgs{f.work(cost), g.ID, []byte(`{"input":"private frozen source","tools":[]}`), json.RawMessage(`[{"source":"mission","revision":2}]`)})
	if e != nil {
		t.Fatal(e)
	}
	return w
}
func providerPrepare(t *testing.T, s *Store, f fixture, w Work) PreparedProviderOperation {
	t.Helper()
	a := claim(t, s, f, w)
	r, e := s.GetProviderRequest(context.Background(), f.scope, w.ID)
	if e != nil {
		t.Fatal(e)
	}
	token, e := NewProviderReceiptToken()
	if e != nil {
		t.Fatal(e)
	}
	p, e := s.PrepareProviderOperation(context.Background(), f.scope, PrepareProviderOperationArgs{lease(a), Operation{id("operation"), r.RequestDigest, "test-private-provider", "route-v1", "price-v1", w.HeldMicros}, token})
	if e != nil {
		t.Fatal(e)
	}
	return p
}
func acceptedFact(response string) ProviderFactArgs {
	return ProviderFactArgs{IdempotencyKey: uuid.NewString(), Kind: "accepted", ResponseID: response, ProviderStatus: "queued", Evidence: json.RawMessage(`{"status":"queued"}`)}
}
func terminalFact(response string, cost *int64) ProviderFactArgs {
	c := "A useful private document."
	return ProviderFactArgs{IdempotencyKey: uuid.NewString(), Kind: "terminal", ResponseID: response, ProviderStatus: "completed", Outcome: "succeeded", Content: c, ContentDigest: contentDigest(c), ActualMicros: cost, Evidence: json.RawMessage(`{"usage":{"input_tokens":1,"output_tokens":1}}`)}
}
func finishProvider(a Attempt, f ProviderFact) CompleteAttemptArgs {
	return CompleteAttemptArgs{lease(a), a.Operation.ID, f.Outcome, f.Content, f.ContentDigest, CostEvidence{f.ActualMicros, f.EvidenceRef()}, f.EvidenceRef()}
}
func checkProviderBalance(t *testing.T, s *Store, f fixture, g ProviderGrant, held, settled int64) {
	t.Helper()
	v, e := s.GetProviderGrantBalance(context.Background(), f.scope, g.ID)
	if e != nil || v.HeldMicros != held || v.SettledMicros != settled {
		t.Fatalf("provider balance %+v %v want %d/%d", v, e, held, settled)
	}
	b, e := s.GetBudget(context.Background(), f.scope, f.result.Business.ID)
	if e != nil || b.ReservedMicros != held || b.SettledMicros != settled {
		t.Fatalf("business balance %+v %v want %d/%d", b, e, held, settled)
	}
}
func TestPostgresProviderJournal(t *testing.T) {
	s, admin, runtime := testStore(t)
	ctx := context.Background()
	issuer, e := NewProviderAdmin(ctx, admin)
	if e != nil {
		t.Fatal(e)
	}
	t.Run("runtime_cannot_issue_or_change_grants_or_immutable_evidence", func(t *testing.T) {
		_, e := NewProviderAdmin(ctx, runtime)
		mustErr(t, e, ErrDenied)
		f := makeFixture(t, s)
		g := hostGrant(t, issuer, f, 100, 2, "account-"+uuid.NewString())
		for _, q := range []string{`INSERT INTO business.provider_grants SELECT * FROM business.provider_grants WHERE false`, `UPDATE business.provider_grants SET status='active'`, `DELETE FROM business.provider_grants`, `UPDATE business.provider_requests SET body='{}'`, `UPDATE business.provider_journal SET body='{}'`, `DELETE FROM business.provider_facts`, `UPDATE business.provider_facts SET body='{}'`, `DELETE FROM business.provider_response_bindings`} {
			if _, e := runtime.Exec(ctx, q); e == nil {
				t.Fatal("runtime privilege", q)
			}
		}
		replay, e := issuer.IssueGrant(ctx, g.ProviderGrantArgs)
		if e != nil || replay.ID != g.ID {
			t.Fatalf("host replay %+v %v", replay, e)
		}
		reordered := g.ProviderGrantArgs
		reordered.PriceSnapshot = json.RawMessage(`{ "inputMicros": 1, "currency": "USD" }`)
		if _, e := issuer.IssueGrant(ctx, reordered); e != nil {
			t.Fatal("snapshot semantic replay", e)
		}
		changed := g.ProviderGrantArgs
		changed.AllowanceMicros++
		_, e = issuer.IssueGrant(ctx, changed)
		mustErr(t, e, ErrConflict)
		// Tenant budget is abundant; absent host allocation still denies provider work.
		_, e = s.AdmitProviderWork(ctx, f.scope, ProviderWorkArgs{f.work(10), "nonexistent-host-grant", []byte("private"), json.RawMessage(`[]`)})
		mustErr(t, e, ErrNotFound)
	})
	t.Run("atomic_admission_replay_and_failure_rollback", func(t *testing.T) {
		f := makeFixture(t, s)
		g := hostGrant(t, issuer, f, 50, 2, "account-"+uuid.NewString())
		in := ProviderWorkArgs{f.work(40), g.ID, []byte("exact private input"), json.RawMessage(`[]`)}
		one, e := s.AdmitProviderWork(ctx, f.scope, in)
		if e != nil {
			t.Fatal(e)
		}
		two, e := s.AdmitProviderWork(ctx, f.scope, in)
		if e != nil || one.ID != two.ID {
			t.Fatalf("admission replay %+v %v", two, e)
		}
		in.Request = []byte("changed private input")
		_, e = s.AdmitProviderWork(ctx, f.scope, in)
		mustErr(t, e, ErrConflict)
		_, e = s.AdmitProviderWork(ctx, f.scope, ProviderWorkArgs{f.work(20), g.ID, []byte("more"), json.RawMessage(`[]`)})
		mustErr(t, e, ErrBudget)
		checkProviderBalance(t, s, f, g, 40, 0)
		ws, e := s.ListWork(ctx, f.scope, f.result.Business.ID)
		if e != nil || len(ws) != 1 {
			t.Fatalf("orphan work %+v %v", ws, e)
		}
		r, e := s.GetProviderRequest(ctx, f.scope, one.ID)
		if e != nil || string(r.Request) != "exact private input" || r.RequestDigest != contentDigest(string(r.Request)) {
			t.Fatalf("request %+v %v", r, e)
		}
	})
	t.Run("competing_admissions_cannot_oversubscribe_host", func(t *testing.T) {
		f := makeFixture(t, s)
		g := hostGrant(t, issuer, f, 50, 1, "account-"+uuid.NewString())
		var wg sync.WaitGroup
		results := make(chan error, 8)
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, e := s.AdmitProviderWork(ctx, f.scope, ProviderWorkArgs{f.work(40), g.ID, []byte("same content"), json.RawMessage(`[]`)})
				results <- e
			}()
		}
		wg.Wait()
		close(results)
		wins := 0
		for e := range results {
			if e == nil {
				wins++
			} else if !errors.Is(e, ErrBudget) {
				t.Fatal(e)
			}
		}
		if wins != 1 {
			t.Fatalf("winners %d", wins)
		}
		checkProviderBalance(t, s, f, g, 40, 0)
	})
	t.Run("business_and_tenant_binding_actual_runtime_rls", func(t *testing.T) {
		a, b := makeFixture(t, s), makeFixture(t, s)
		g := hostGrant(t, issuer, a, 100, 2, "account-"+uuid.NewString())
		w := providerWork(t, s, a, g, 30)
		_, e := s.AdmitProviderWork(ctx, b.scope, ProviderWorkArgs{b.work(10), g.ID, []byte("intrusion"), json.RawMessage(`[]`)})
		mustErr(t, e, ErrNotFound)
		_, e = s.GetProviderRequest(ctx, b.scope, w.ID)
		mustErr(t, e, ErrNotFound)
		m, e := s.AddMember(ctx, a.scope, MemberArgs{uuid.NewString(), uuid.NewString(), "member", 0})
		if e != nil {
			t.Fatal(e)
		}
		_, e = s.GetProviderRequest(ctx, Scope{a.scope.OrganizationID, Actor{"person", m.PersonID}}, w.ID)
		mustErr(t, e, ErrDenied)
		tx, e := scopeTx(ctx, runtime, b.scope)
		if e != nil {
			t.Fatal(e)
		}
		defer tx.Rollback(ctx)
		for _, table := range []string{"provider_grants", "provider_requests", "provider_reservations"} {
			var n int
			if e = tx.QueryRow(ctx, `SELECT count(*) FROM business.`+table+` WHERE organization_id=$1`, a.scope.OrganizationID).Scan(&n); e != nil || n != 0 {
				t.Fatalf("RLS %s %d %v", table, n, e)
			}
		}
		// Even another Business in the SAME org cannot borrow this allocation.
		in := setupArgs()
		in.OrganizationID = a.scope.OrganizationID
		in.OrganizationName = ""
		other, e := s.SetupBusiness(ctx, a.scope.Actor, in)
		if e != nil {
			t.Fatal(e)
		}
		wrong := a.work(10)
		wrong.BusinessID = other.Business.ID
		_, e = s.AdmitProviderWork(ctx, a.scope, ProviderWorkArgs{wrong, g.ID, []byte("private"), json.RawMessage(`[]`)})
		mustErr(t, e, ErrDenied)
	})
	t.Run("prepare_exact_request_and_generic_path_denied", func(t *testing.T) {
		f := makeFixture(t, s)
		g := hostGrant(t, issuer, f, 100, 2, "account-"+uuid.NewString())
		w := providerWork(t, s, f, g, 60)
		a := claim(t, s, f, w)
		r, e := s.GetProviderRequest(ctx, f.scope, w.ID)
		if e != nil {
			t.Fatal(e)
		}
		token, _ := NewProviderReceiptToken()
		op := Operation{id("operation"), r.RequestDigest, g.AdapterID, g.RouteRevision, g.PriceRevision, 60}
		_, e = s.PrepareOperation(ctx, f.scope, PrepareOperationArgs{lease(a), op})
		mustErr(t, e, ErrDenied)
		bad := op
		bad.RequestDigest = "sha256:wrong"
		_, e = s.PrepareProviderOperation(ctx, f.scope, PrepareProviderOperationArgs{lease(a), bad, token})
		mustErr(t, e, ErrConflict)
		p, e := s.PrepareProviderOperation(ctx, f.scope, PrepareProviderOperationArgs{lease(a), op, token})
		if e != nil {
			t.Fatal(e)
		}
		again, e := s.PrepareProviderOperation(ctx, f.scope, PrepareProviderOperationArgs{lease(a), op, token})
		if e != nil || again.Journal.Operation != p.Journal.Operation {
			t.Fatalf("prepare replay %+v %v", again, e)
		}
		if e = s.CheckProviderAuthority(ctx, f.scope, lease(p.Attempt), op.ID); e != nil {
			t.Fatal(e)
		}
		encoded, _ := json.Marshal(p)
		if strings.Contains(string(encoded), token) {
			t.Fatal("capability serialized")
		}
		_, e = s.CompleteAttempt(ctx, f.scope, completeArgs(p.Attempt, nil))
		mustErr(t, e, ErrReconciliation)
	})
	t.Run("restart_queued_id_and_atomic_single_settlement", func(t *testing.T) {
		f := makeFixture(t, s)
		g := hostGrant(t, issuer, f, 100, 2, "account-"+uuid.NewString())
		w := providerWork(t, s, f, g, 60)
		p := providerPrepare(t, s, f, w)
		accepted := acceptedFact("response-" + uuid.NewString())
		one, e := s.AppendProviderFact(ctx, p.Capability, accepted)
		if e != nil {
			t.Fatal(e)
		}
		two, e := s.AppendProviderFact(ctx, p.Capability, accepted)
		if e != nil || one.ID != two.ID {
			t.Fatalf("ACK replay %+v %v", two, e)
		}
		reopened, e := New(ctx, runtime)
		if e != nil {
			t.Fatal(e)
		}
		view, e := reopened.GetProviderJournal(ctx, f.scope, p.Journal.Operation.ID)
		if e != nil || view.ResponseID != accepted.ResponseID || len(view.Facts) != 1 {
			t.Fatalf("durable ACK %+v %v", view, e)
		}
		cost := int64(23)
		terminal, e := s.AppendProviderFact(ctx, p.Capability, terminalFact(accepted.ResponseID, &cost))
		if e != nil {
			t.Fatal(e)
		}
		args := finishProvider(p.Attempt, terminal)
		out, e := s.CompleteAttempt(ctx, f.scope, args)
		if e != nil || out.Result == nil {
			t.Fatalf("completion %+v %v", out, e)
		}
		again, e := s.CompleteAttempt(ctx, f.scope, args)
		if e != nil || again.Result.ID != out.Result.ID {
			t.Fatalf("completion replay %+v %v", again, e)
		}
		checkProviderBalance(t, s, f, g, 0, 23)
		var count int
		if e = admin.QueryRow(ctx, `SELECT count(*) FROM business.results WHERE organization_id=$1 AND work_id=$2`, f.scope.OrganizationID, w.ID).Scan(&count); e != nil || count != 1 {
			t.Fatalf("result count %d %v", count, e)
		}
	})
	t.Run("late_receipt_after_fence_cannot_execute_or_settle", func(t *testing.T) {
		f := makeFixture(t, s)
		g := hostGrant(t, issuer, f, 100, 2, "account-"+uuid.NewString())
		w := providerWork(t, s, f, g, 60)
		p := providerPrepare(t, s, f, w)
		expire(t, admin, f.scope.OrganizationID, p.Attempt.ID)
		next := claim(t, s, f, w)
		if next.Mode != "reconcile" {
			t.Fatal(next)
		}
		ack, e := s.AppendProviderFact(ctx, p.Capability, acceptedFact("response-"+uuid.NewString()))
		if e != nil {
			t.Fatal(e)
		}
		if e = s.CheckProviderAuthority(ctx, f.scope, lease(p.Attempt), p.Journal.Operation.ID); !errors.Is(e, ErrLease) {
			t.Fatalf("stale authority %v", e)
		}
		cost := int64(22)
		fact, e := s.AppendProviderFact(ctx, p.Capability, terminalFact(ack.ResponseID, &cost))
		if e != nil {
			t.Fatal(e)
		}
		_, e = s.CompleteAttempt(ctx, f.scope, finishProvider(p.Attempt, fact))
		mustErr(t, e, ErrLease)
		checkProviderBalance(t, s, f, g, 60, 0)
		token, _ := NewProviderReceiptToken()
		cap, e := s.AcquireProviderReceiptCapability(ctx, f.scope, lease(next), token)
		if e != nil || cap.OperationID != p.Journal.Operation.ID {
			t.Fatalf("recovery capability %+v %v", cap, e)
		}
		args := finishProvider(next, fact)
		_, e = s.ReconcileAttempt(ctx, f.scope, ReconcileAttemptArgs(args))
		if e != nil {
			t.Fatal(e)
		}
		checkProviderBalance(t, s, f, g, 0, 22)
	})
	t.Run("unknown_cost_blocks_and_recovery_retains_output_and_evidence", func(t *testing.T) {
		f := makeFixture(t, s)
		g := hostGrant(t, issuer, f, 100, 4, "account-"+uuid.NewString())
		w := providerWork(t, s, f, g, 60)
		p := providerPrepare(t, s, f, w)
		response := "response-" + uuid.NewString()
		unknown, e := s.AppendProviderFact(ctx, p.Capability, terminalFact(response, nil))
		if e != nil {
			t.Fatal(e)
		}
		out, e := s.CompleteAttempt(ctx, f.scope, finishProvider(p.Attempt, unknown))
		if e != nil || out.Work.Status != "reconciling" {
			t.Fatalf("unknown %+v %v", out, e)
		}
		checkProviderBalance(t, s, f, g, 60, 0)
		_, e = s.AdmitProviderWork(ctx, f.scope, ProviderWorkArgs{f.work(0), g.ID, []byte("new"), json.RawMessage(`[]`)})
		mustErr(t, e, ErrReconciliation)
		expire(t, admin, f.scope.OrganizationID, p.Attempt.ID)
		a := claim(t, s, f, w)
		cost := int64(11)
		known, e := s.AppendProviderFact(ctx, p.Capability, terminalFact(response, &cost))
		if e != nil {
			t.Fatal(e)
		}
		reopened, e := New(ctx, runtime)
		if e != nil {
			t.Fatal(e)
		}
		view, e := reopened.GetProviderJournal(ctx, f.scope, p.Journal.Operation.ID)
		if e != nil || len(view.Facts) != 2 || view.Facts[0].ActualMicros != nil || view.Facts[1].ID != known.ID {
			t.Fatalf("history %+v %v", view, e)
		}
		result, e := reopened.ReconcileAttempt(ctx, f.scope, ReconcileAttemptArgs(finishProvider(a, known)))
		if e != nil || result.Result.ID != out.Result.ID {
			t.Fatalf("reconcile %+v %v", result, e)
		}
		checkProviderBalance(t, s, f, g, 0, 11)
		changed := terminalFact(response, &cost)
		changed.Content = "Replacement"
		changed.ContentDigest = contentDigest(changed.Content)
		_, e = s.AppendProviderFact(ctx, p.Capability, changed)
		mustErr(t, e, ErrConflict)
	})
	t.Run("host_revocation_preserves_possible_issuance_but_releases_unissued", func(t *testing.T) {
		f := makeFixture(t, s)
		g := hostGrant(t, issuer, f, 100, 3, "account-"+uuid.NewString())
		w := providerWork(t, s, f, g, 50)
		providerWork(t, s, f, g, 30)
		p := providerPrepare(t, s, f, w)
		if _, e = issuer.RevokeGrant(ctx, f.scope.OrganizationID, g.ID); e != nil {
			t.Fatal(e)
		}
		checkProviderBalance(t, s, f, g, 50, 0)
		if e = s.CheckProviderAuthority(ctx, f.scope, lease(p.Attempt), p.Journal.Operation.ID); !errors.Is(e, ErrLease) {
			t.Fatalf("revoked authority %v", e)
		}
		a := claim(t, s, f, w)
		cost := int64(17)
		fact, e := s.AppendProviderFact(ctx, p.Capability, terminalFact("response-"+uuid.NewString(), &cost))
		if e != nil {
			t.Fatal(e)
		}
		out, e := s.ReconcileAttempt(ctx, f.scope, ReconcileAttemptArgs(finishProvider(a, fact)))
		if e != nil || out.Result.Eligible || out.Work.Status != "cancelled" {
			t.Fatalf("revoked reconcile %+v %v", out, e)
		}
		checkProviderBalance(t, s, f, g, 0, 17)
	})
	t.Run("mandate_revocation_releases_both_budgets", func(t *testing.T) {
		f := makeFixture(t, s)
		g := hostGrant(t, issuer, f, 100, 2, "account-"+uuid.NewString())
		providerWork(t, s, f, g, 40)
		_, e := s.RevokeMandate(ctx, f.scope, RevokeMandateArgs{uuid.NewString(), f.mandate.ID, f.mandate.Revision})
		if e != nil {
			t.Fatal(e)
		}
		checkProviderBalance(t, s, f, g, 0, 0)
		v, e := s.GetProviderGrantBalance(ctx, f.scope, g.ID)
		if e != nil || v.ReservedOperations != 0 {
			t.Fatalf("slot not released %+v %v", v, e)
		}
	})
	t.Run("actual_overage_never_clamped_and_blocks_more_work", func(t *testing.T) {
		f := makeFixture(t, s)
		g := hostGrant(t, issuer, f, 50, 3, "account-"+uuid.NewString())
		w := providerWork(t, s, f, g, 40)
		p := providerPrepare(t, s, f, w)
		cost := int64(70)
		fact, e := s.AppendProviderFact(ctx, p.Capability, terminalFact("response-"+uuid.NewString(), &cost))
		if e != nil {
			t.Fatal(e)
		}
		if _, e = s.CompleteAttempt(ctx, f.scope, finishProvider(p.Attempt, fact)); e != nil {
			t.Fatal(e)
		}
		checkProviderBalance(t, s, f, g, 0, 70)
		_, e = s.AdmitProviderWork(ctx, f.scope, ProviderWorkArgs{f.work(0), g.ID, []byte("new"), json.RawMessage(`[]`)})
		mustErr(t, e, ErrBudget)
	})
	t.Run("response_id_unique_per_account_across_tenants_and_capabilities_bound", func(t *testing.T) {
		a, b := makeFixture(t, s), makeFixture(t, s)
		account := "account-" + uuid.NewString()
		ga, gb := hostGrant(t, issuer, a, 100, 3, account), hostGrant(t, issuer, b, 100, 3, account)
		pa := providerPrepare(t, s, a, providerWork(t, s, a, ga, 20))
		pb := providerPrepare(t, s, b, providerWork(t, s, b, gb, 20))
		in := acceptedFact("same-provider-response-" + uuid.NewString())
		if _, e = s.AppendProviderFact(ctx, pa.Capability, in); e != nil {
			t.Fatal(e)
		}
		_, e = s.AppendProviderFact(ctx, pb.Capability, in)
		mustErr(t, e, ErrConflict)
		changed := pa.Capability
		changed.OrganizationID = b.scope.OrganizationID
		changed.OperationID = pb.Journal.Operation.ID
		_, e = s.AppendProviderFact(ctx, changed, acceptedFact("other"))
		mustErr(t, e, ErrDenied)
		_, e = s.GetProviderJournal(ctx, b.scope, pa.Journal.Operation.ID)
		mustErr(t, e, ErrNotFound)
		// Identical opaque response IDs from DIFFERENT provider accounts are distinct.
		gc := hostGrant(t, issuer, b, 100, 3, "account-"+uuid.NewString())
		pc := providerPrepare(t, s, b, providerWork(t, s, b, gc, 20))
		if _, e = s.AppendProviderFact(ctx, pc.Capability, in); e != nil {
			t.Fatal(e)
		}
	})
	t.Run("forged_settlement_cannot_use_terminal_receipt", func(t *testing.T) {
		f := makeFixture(t, s)
		g := hostGrant(t, issuer, f, 100, 2, "account-"+uuid.NewString())
		p := providerPrepare(t, s, f, providerWork(t, s, f, g, 60))
		cost := int64(20)
		fact, e := s.AppendProviderFact(ctx, p.Capability, terminalFact("response-"+uuid.NewString(), &cost))
		if e != nil {
			t.Fatal(e)
		}
		args := finishProvider(p.Attempt, fact)
		zero := int64(0)
		args.Cost.ActualMicros = &zero
		_, e = s.CompleteAttempt(ctx, f.scope, args)
		mustErr(t, e, ErrConflict)
		checkProviderBalance(t, s, f, g, 60, 0)
		cost = 21
		_, e = s.AppendProviderFact(ctx, p.Capability, terminalFact(fact.ResponseID, &cost))
		mustErr(t, e, ErrConflict)
	})
	t.Run("expired_grant_cannot_issue_even_with_current_lease", func(t *testing.T) {
		f := makeFixture(t, s)
		g := hostGrant(t, issuer, f, 100, 2, "account-"+uuid.NewString())
		p := providerPrepare(t, s, f, providerWork(t, s, f, g, 60))
		if _, e = admin.Exec(ctx, `UPDATE business.provider_grants SET body=jsonb_set(body,'{ExpiresAt}',to_jsonb('2000-01-01T00:00:00Z'::text)) WHERE organization_id=$1 AND id=$2`, f.scope.OrganizationID, g.ID); e != nil {
			t.Fatal(e)
		}
		if e = s.CheckProviderAuthority(ctx, f.scope, lease(p.Attempt), p.Journal.Operation.ID); !errors.Is(e, ErrInactive) {
			t.Fatalf("expired resource %v", e)
		}
	})
	t.Run("late_ack_after_terminal_and_response_mismatch", func(t *testing.T) {
		f := makeFixture(t, s)
		g := hostGrant(t, issuer, f, 100, 2, "account-"+uuid.NewString())
		p := providerPrepare(t, s, f, providerWork(t, s, f, g, 60))
		response := "response-" + uuid.NewString()
		cost := int64(3)
		terminal, e := s.AppendProviderFact(ctx, p.Capability, terminalFact(response, &cost))
		if e != nil {
			t.Fatal(e)
		}
		if _, e = s.AppendProviderFact(ctx, p.Capability, acceptedFact(response)); e != nil {
			t.Fatal("late ACK lost", e)
		}
		_, e = s.AppendProviderFact(ctx, p.Capability, acceptedFact("different"))
		mustErr(t, e, ErrConflict)
		bad := terminalFact(response, &cost)
		bad.ProviderStatus = "failed"
		_, e = s.AppendProviderFact(ctx, p.Capability, bad)
		mustErr(t, e, ErrConflict)
		// Two successful terminalizers return the one durable result/settlement.
		var wg sync.WaitGroup
		results := make(chan AttemptCompletion, 2)
		errs := make(chan error, 2)
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				out, e := s.CompleteAttempt(ctx, f.scope, finishProvider(p.Attempt, terminal))
				results <- out
				errs <- e
			}()
		}
		wg.Wait()
		close(results)
		close(errs)
		for e := range errs {
			if e != nil {
				t.Fatal(e)
			}
		}
		rid := ""
		for out := range results {
			if out.Result == nil {
				t.Fatal(out)
			}
			if rid != "" && rid != out.Result.ID {
				t.Fatal("two results")
			}
			rid = out.Result.ID
		}
		checkProviderBalance(t, s, f, g, 0, 3)
	})
	t.Run("generation_cap_applies_to_proven_nonacceptance_retry", func(t *testing.T) {
		f := makeFixture(t, s)
		g := hostGrant(t, issuer, f, 100, 1, "account-"+uuid.NewString())
		w := providerWork(t, s, f, g, 60)
		p := providerPrepare(t, s, f, w)
		zero := int64(0)
		fact, e := s.AppendProviderFact(ctx, p.Capability, ProviderFactArgs{IdempotencyKey: uuid.NewString(), Kind: "terminal", ProviderStatus: "not_accepted", Outcome: "not_accepted", ActualMicros: &zero, Evidence: json.RawMessage(`{"transport":"proved_not_sent"}`)})
		if e != nil {
			t.Fatal(e)
		}
		if _, e = s.CompleteAttempt(ctx, f.scope, finishProvider(p.Attempt, fact)); e != nil {
			t.Fatal(e)
		}
		a := claim(t, s, f, w)
		token, _ := NewProviderReceiptToken()
		op := p.Journal.Operation
		op.ID = id("next-operation")
		_, e = s.PrepareProviderOperation(ctx, f.scope, PrepareProviderOperationArgs{lease(a), op, token})
		mustErr(t, e, ErrBudget)
		checkProviderBalance(t, s, f, g, 60, 0)
		if _, e = issuer.RevokeGrant(ctx, f.scope.OrganizationID, g.ID); e != nil {
			t.Fatal(e)
		}
		checkProviderBalance(t, s, f, g, 0, 0)
	})
	t.Run("zero_cost_unissued_slot_and_source_immutability", func(t *testing.T) {
		f := makeFixture(t, s)
		g := hostGrant(t, issuer, f, 100, 1, "account-"+uuid.NewString())
		w := providerWork(t, s, f, g, 0)
		if _, e = issuer.RevokeGrant(ctx, f.scope.OrganizationID, g.ID); e != nil {
			t.Fatal(e)
		}
		v, e := s.GetProviderGrantBalance(ctx, f.scope, g.ID)
		if e != nil || v.ReservedOperations != 0 {
			t.Fatalf("zero slot leaked %+v %v", v, e)
		}
		r, e := s.GetProviderRequest(ctx, f.scope, w.ID)
		if e != nil || providerPolicyDigest(r.SourceBindings) != providerPolicyDigest(json.RawMessage(`[{"source":"mission","revision":2}]`)) {
			t.Fatalf("source receipt %+v %v", r, e)
		}
	})
	t.Run("no_ack_stays_unknown_and_grant_expiry_blocks_actionable_result", func(t *testing.T) {
		f := makeFixture(t, s)
		g := hostGrant(t, issuer, f, 100, 3, "account-"+uuid.NewString())
		w := providerWork(t, s, f, g, 60)
		p := providerPrepare(t, s, f, w)
		expire(t, admin, f.scope.OrganizationID, p.Attempt.ID)
		a := claim(t, s, f, w)
		view, e := s.GetProviderJournal(ctx, f.scope, p.Journal.Operation.ID)
		if e != nil || view.ResponseID != "" || len(view.Facts) != 0 {
			t.Fatalf("invented acceptance %+v %v", view, e)
		}
		_, e = s.AdmitProviderWork(ctx, f.scope, ProviderWorkArgs{f.work(0), g.ID, []byte("new"), json.RawMessage(`[]`)})
		mustErr(t, e, ErrReconciliation)
		if _, e = admin.Exec(ctx, `UPDATE business.provider_grants SET body=jsonb_set(body,'{ExpiresAt}',to_jsonb('2000-01-01T00:00:00Z'::text)) WHERE organization_id=$1 AND id=$2`, f.scope.OrganizationID, g.ID); e != nil {
			t.Fatal(e)
		}
		cost := int64(5)
		fact, e := s.AppendProviderFact(ctx, p.Capability, terminalFact("response-"+uuid.NewString(), &cost))
		if e != nil {
			t.Fatal(e)
		}
		out, e := s.ReconcileAttempt(ctx, f.scope, ReconcileAttemptArgs(finishProvider(a, fact)))
		if e != nil || out.Result == nil || out.Result.Eligible || out.Work.Status != "cancelled" {
			t.Fatalf("expired-grant result %+v %v", out, e)
		}
		checkProviderBalance(t, s, f, g, 0, 5)
	})
	t.Run("polling_noise_cannot_starve_terminal_and_cost_refinement", func(t *testing.T) {
		f := makeFixture(t, s)
		g := hostGrant(t, issuer, f, 100, 2, "account-"+uuid.NewString())
		p := providerPrepare(t, s, f, providerWork(t, s, f, g, 60))
		response := "response-" + uuid.NewString()
		accepted := 0
		var original ProviderFactArgs
		for i := 0; i < 64; i++ {
			in := acceptedFact(response)
			if i == 0 {
				original = in
			}
			_, e := s.AppendProviderFact(ctx, p.Capability, in)
			if e == nil {
				accepted++
			} else if !errors.Is(e, ErrInvalid) {
				t.Fatal(e)
			}
		}
		if accepted != 8 {
			t.Fatalf("accepted noise %d", accepted)
		}
		if _, e = s.AppendProviderFact(ctx, p.Capability, original); e != nil {
			t.Fatal("exact ACK replay failed at bound", e)
		}
		unknownArgs := terminalFact(response, nil)
		unknown, e := s.AppendProviderFact(ctx, p.Capability, unknownArgs)
		if e != nil {
			t.Fatal("no unknown-terminal capacity", e)
		}
		if _, e = s.CompleteAttempt(ctx, f.scope, finishProvider(p.Attempt, unknown)); e != nil {
			t.Fatal(e)
		}
		for i := 0; i < 64; i++ {
			_, e := s.AppendProviderFact(ctx, p.Capability, terminalFact(response, nil))
			mustErr(t, e, ErrInvalid)
		}
		again, e := s.AppendProviderFact(ctx, p.Capability, unknownArgs)
		if e != nil || again.ID != unknown.ID {
			t.Fatalf("unknown exact replay %+v %v", again, e)
		}
		cost := int64(19)
		knownArgs := terminalFact(response, &cost)
		known, e := s.AppendProviderFact(ctx, p.Capability, knownArgs)
		if e != nil {
			t.Fatal("known-cost capacity starved", e)
		}
		again, e = s.AppendProviderFact(ctx, p.Capability, knownArgs)
		if e != nil || again.ID != known.ID {
			t.Fatalf("known replay %+v %v", again, e)
		}
		view, e := s.GetProviderJournal(ctx, f.scope, p.Journal.Operation.ID)
		if e != nil || len(view.Facts) != 10 {
			t.Fatalf("bounded history %+v %v", view, e)
		}
		for i, fact := range view.Facts {
			if fact.Sequence != i+1 {
				t.Fatal("nonsequential journal", view)
			}
		}
		expire(t, admin, f.scope.OrganizationID, p.Attempt.ID)
		a := claim(t, s, f, Work{ID: p.Attempt.WorkID})
		if _, e = s.ReconcileAttempt(ctx, f.scope, ReconcileAttemptArgs(finishProvider(a, known))); e != nil {
			t.Fatal(e)
		}
		checkProviderBalance(t, s, f, g, 0, 19)
	})
	t.Run("mismatching_admin_owner_rejected", func(t *testing.T) {
		// An elevated role that is not the migration/schema owner cannot become this
		// package's grant issuer merely because it has BYPASSRLS.
		role := "provider_admin_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
		if _, e = admin.Exec(ctx, `CREATE ROLE `+pgx.Identifier{role}.Sanitize()+` NOLOGIN BYPASSRLS`); e != nil {
			t.Fatal(e)
		}
		conn, e := admin.Acquire(ctx)
		if e != nil {
			t.Fatal(e)
		}
		defer conn.Release()
		if _, e = conn.Exec(ctx, `SET ROLE `+pgx.Identifier{role}.Sanitize()); e != nil {
			t.Fatal(e)
		}
		var allowed bool
		e = conn.QueryRow(ctx, `SELECT (r.rolsuper OR r.rolbypassrls) AND n.nspowner=r.oid FROM pg_roles r JOIN pg_namespace n ON n.nspname='business' WHERE r.rolname=current_user`).Scan(&allowed)
		_, reset := conn.Exec(ctx, `RESET ROLE`)
		if e != nil || reset != nil || allowed {
			t.Fatalf("issuer owner guard %v %v %v", allowed, e, reset)
		}
	})
}
