package business

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func testStore(t *testing.T) (*Store, *pgxpool.Pool, *pgxpool.Pool) {
	t.Helper()
	url := os.Getenv("BUSINESS_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set BUSINESS_TEST_DATABASE_URL to an isolated disposable PostgreSQL database")
	}
	ctx := context.Background()
	admin, e := pgxpool.New(ctx, url)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(admin.Close)
	if e = Migrate(ctx, admin); e != nil {
		t.Fatal(e)
	}
	if e = Migrate(ctx, admin); e != nil {
		t.Fatal("migration replay", e)
	}
	role := "business_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, e = admin.Exec(ctx, `CREATE ROLE `+role+` LOGIN NOSUPERUSER NOBYPASSRLS INHERIT; GRANT business_runtime TO `+role); e != nil {
		t.Fatal(e)
	}
	config, e := pgxpool.ParseConfig(url)
	if e != nil {
		t.Fatal(e)
	}
	config.ConnConfig.User = role
	config.MaxConns = 16
	runtime, e := pgxpool.NewWithConfig(ctx, config)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(runtime.Close)
	store, e := New(ctx, runtime)
	if e != nil {
		t.Fatal(e)
	}
	var current string
	if e = runtime.QueryRow(ctx, `SELECT current_user`).Scan(&current); e != nil || current != role {
		t.Fatalf("not actual restricted login: %s %v", current, e)
	}
	return store, admin, runtime
}
func setupArgs() SetupBusinessArgs {
	return SetupBusinessArgs{IdempotencyKey: uuid.NewString(), OrganizationName: "Test organization", Name: "Research company", Mission: "Produce useful private research", Customer: "Owner", FirstOutcome: "A private sourced brief", Leadership: "agent_ceo", AuthorityPreset: "full_autonomy", ModelAllowanceMicros: 100}
}

type fixture struct {
	scope      Scope
	result     SetupBusinessResult
	employment Employment
	mandate    Mandate
}

func makeFixture(t *testing.T, s *Store) fixture {
	t.Helper()
	ctx := context.Background()
	actor := Actor{"person", uuid.NewString()}
	r, e := s.SetupBusiness(ctx, actor, setupArgs())
	if e != nil {
		t.Fatal(e)
	}
	scope := Scope{r.Organization.ID, actor}
	b, e := s.UpdateBusiness(ctx, scope, UpdateBusinessArgs{uuid.NewString(), r.Business.ID, 1, "active", "agent_ceo", "full_autonomy"})
	if e != nil {
		t.Fatal(e)
	}
	r.Business = b
	emp, e := s.CreateEmployment(ctx, scope, EmploymentArgs{uuid.NewString(), b.ID, "Researcher", "local-researcher", "1", "sha256:reviewed-offering"})
	if e != nil {
		t.Fatal(e)
	}
	m, e := s.GrantMandate(ctx, scope, MandateArgs{uuid.NewString(), b.ID, emp.ID, time.Now().Add(time.Hour), 100, 10, 2})
	if e != nil {
		t.Fatal(e)
	}
	return fixture{scope, r, emp, m}
}
func (f fixture) work(cost int64) WorkArgs {
	return WorkArgs{uuid.NewString(), f.result.Business.ID, f.employment.ID, f.mandate.ID, f.mandate.Revision, "Write the first private brief", "private_document_v1", cost}
}
func mustErr(t *testing.T, e, want error) {
	t.Helper()
	if !errors.Is(e, want) {
		t.Fatalf("got %v want %v", e, want)
	}
}

func TestPostgresBusinessKernel(t *testing.T) {
	s, admin, runtime := testStore(t)
	ctx := context.Background()
	t.Run("runtime_cannot_own_or_bypass", func(t *testing.T) {
		if _, e := New(ctx, admin); e == nil {
			t.Fatal("accepted admin runtime")
		}
		if _, e := runtime.Exec(ctx, `UPDATE business.operations SET digest='changed'`); e == nil {
			t.Fatal("receipt mutation permitted")
		}
		if _, e := runtime.Exec(ctx, `DELETE FROM business.events`); e == nil {
			t.Fatal("event deletion permitted")
		}
	})
	t.Run("atomic_setup_and_lost_ack_replay", func(t *testing.T) {
		a := Actor{"person", uuid.NewString()}
		in := setupArgs()
		one, e := s.SetupBusiness(ctx, a, in)
		if e != nil {
			t.Fatal(e)
		}
		two, e := s.SetupBusiness(ctx, a, in)
		if e != nil || one != two {
			t.Fatalf("replay differs: %v %v", two, e)
		}
		if one.Business.Status != "draft" {
			t.Fatal("new business claims execution")
		}
		orgs, e := s.ListOrganizations(ctx, a)
		if e != nil || len(orgs) != 1 {
			t.Fatalf("directory %+v %v", orgs, e)
		}
		in.Mission = "Changed request"
		_, e = s.SetupBusiness(ctx, a, in)
		mustErr(t, e, ErrConflict)
		all, e := s.ListBusinesses(ctx, Scope{one.Organization.ID, a})
		if e != nil || len(all) != 1 {
			t.Fatalf("duplicate setup %+v %v", all, e)
		}
		other := Actor{"person", uuid.NewString()}
		in = setupArgs()
		in.OrganizationID = one.Organization.ID
		_, e = s.SetupBusiness(ctx, other, in)
		mustErr(t, e, ErrDenied)
	})
	t.Run("two_tenant_rls_and_authority", func(t *testing.T) {
		a, b := makeFixture(t, s), makeFixture(t, s)
		_, e := s.GetBusiness(ctx, Scope{a.scope.OrganizationID, b.scope.Actor}, a.result.Business.ID)
		mustErr(t, e, ErrDenied)
		_, e = s.GetBusiness(ctx, a.scope, b.result.Business.ID)
		mustErr(t, e, ErrNotFound)
		tx, e := scopeTx(ctx, runtime, a.scope)
		if e != nil {
			t.Fatal(e)
		}
		defer tx.Rollback(ctx)
		var n int
		if e = tx.QueryRow(ctx, `SELECT count(*) FROM business.businesses WHERE organization_id=$1`, b.scope.OrganizationID).Scan(&n); e != nil || n != 0 {
			t.Fatalf("cross read %d %v", n, e)
		}
		tag, e := tx.Exec(ctx, `UPDATE business.businesses SET body='{}' WHERE organization_id=$1`, b.scope.OrganizationID)
		if e != nil || tag.RowsAffected() != 0 {
			t.Fatalf("cross update %v %v", tag, e)
		}
		_, e = tx.Exec(ctx, `INSERT INTO business.events(organization_id,kind,entity_id,body) VALUES($1,'intrusion','x','{}')`, b.scope.OrganizationID)
		if e == nil {
			t.Fatal("cross insert accepted")
		}
		var all int
		if e = runtime.QueryRow(ctx, `SELECT count(*) FROM business.businesses`).Scan(&all); e != nil || all != 0 {
			t.Fatalf("unscoped rows %d %v", all, e)
		}
	})
	t.Run("funding_requires_owner_full_autonomy_is_not_bypass", func(t *testing.T) {
		f := makeFixture(t, s)
		m, e := s.AddMember(ctx, f.scope, MemberArgs{uuid.NewString(), uuid.NewString(), "member", 0})
		if e != nil {
			t.Fatal(e)
		}
		for _, actor := range []Actor{{"person", m.PersonID}, {"agent", f.employment.ID}} {
			sc := Scope{f.scope.OrganizationID, actor}
			_, e = s.FundBudget(ctx, sc, BudgetArgs{uuid.NewString(), f.result.Business.ID, 1, 100})
			mustErr(t, e, ErrDenied)
			_, e = s.SetBudgetCap(ctx, sc, BudgetArgs{uuid.NewString(), f.result.Business.ID, 1, 200})
			mustErr(t, e, ErrDenied)
			_, e = s.GrantMandate(ctx, sc, MandateArgs{uuid.NewString(), f.result.Business.ID, f.employment.ID, time.Now().Add(time.Hour), 100, 1, 1})
			mustErr(t, e, ErrDenied)
		}
		b, e := s.FundBudget(ctx, f.scope, BudgetArgs{uuid.NewString(), f.result.Business.ID, 1, 100})
		if e != nil || b.FundedMicros != 200 || b.CapMicros != 100 {
			t.Fatalf("funding silently raised cap %+v %v", b, e)
		}
	})
	t.Run("work_intent_reservation_and_replay", func(t *testing.T) {
		f := makeFixture(t, s)
		in := f.work(60)
		sc := Scope{f.scope.OrganizationID, Actor{"agent", f.employment.ID}}
		w, e := s.AdmitWork(ctx, sc, in)
		if e != nil {
			t.Fatal(e)
		}
		again, e := s.AdmitWork(ctx, sc, in)
		if e != nil || w.ID != again.ID {
			t.Fatalf("duplicate %v %v", again, e)
		}
		in.Objective = "Changed"
		_, e = s.AdmitWork(ctx, sc, in)
		mustErr(t, e, ErrConflict)
		b, e := s.GetBudget(ctx, f.scope, f.result.Business.ID)
		if e != nil || b.ReservedMicros != 60 {
			t.Fatalf("reserved %+v %v", b, e)
		}
		_, e = s.AdmitWork(ctx, sc, f.work(50))
		mustErr(t, e, ErrBudget)
		works, e := s.ListWork(ctx, f.scope, f.result.Business.ID)
		if e != nil || len(works) != 1 || works[0].Status != "admitted" {
			t.Fatalf("work %+v %v", works, e)
		}
	})
	t.Run("concurrent_budget_and_idempotency", func(t *testing.T) {
		f := makeFixture(t, s)
		var wg sync.WaitGroup
		var mu sync.Mutex
		ok := 0
		var unexpected []error
		for i := 0; i < 12; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, e := s.AdmitWork(ctx, f.scope, f.work(60))
				mu.Lock()
				defer mu.Unlock()
				if e == nil {
					ok++
				} else if !errors.Is(e, ErrBudget) {
					unexpected = append(unexpected, e)
				}
			}()
		}
		wg.Wait()
		if ok != 1 || len(unexpected) != 0 {
			t.Fatalf("admissions %d errors %v", ok, unexpected)
		}
		b, e := s.GetBudget(ctx, f.scope, f.result.Business.ID)
		if e != nil || b.ReservedMicros != 60 {
			t.Fatalf("overspend %+v %v", b, e)
		}
		g := makeFixture(t, s)
		in := g.work(20)
		ids := map[string]bool{}
		unexpected = nil
		for i := 0; i < 12; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				w, e := s.AdmitWork(ctx, g.scope, in)
				mu.Lock()
				defer mu.Unlock()
				if e != nil {
					unexpected = append(unexpected, e)
				} else {
					ids[w.ID] = true
				}
			}()
		}
		wg.Wait()
		if len(ids) != 1 || len(unexpected) != 0 {
			t.Fatalf("duplicate race %v %v", ids, unexpected)
		}
	})
	t.Run("concurrency_limit_across_mandates", func(t *testing.T) {
		f := makeFixture(t, s)
		m, e := s.GrantMandate(ctx, f.scope, MandateArgs{uuid.NewString(), f.result.Business.ID, f.employment.ID, time.Now().Add(time.Hour), 100, 1, 1})
		if e != nil {
			t.Fatal(e)
		}
		in := f.work(1)
		in.MandateID = m.ID
		in.MandateRevision = m.Revision
		if _, e = s.AdmitWork(ctx, f.scope, in); e != nil {
			t.Fatal(e)
		}
		in.IdempotencyKey = uuid.NewString()
		_, e = s.AdmitWork(ctx, f.scope, in)
		mustErr(t, e, ErrConcurrency)
	})
	t.Run("revocation_serializes_against_admission_and_releases", func(t *testing.T) {
		for i := 0; i < 5; i++ {
			f := makeFixture(t, s)
			var wg sync.WaitGroup
			var admitErr, revokeErr error
			wg.Add(2)
			go func() { defer wg.Done(); _, admitErr = s.AdmitWork(ctx, f.scope, f.work(40)) }()
			go func() {
				defer wg.Done()
				_, revokeErr = s.RevokeMandate(ctx, f.scope, RevokeMandateArgs{uuid.NewString(), f.mandate.ID, 1})
			}()
			wg.Wait()
			if revokeErr != nil {
				t.Fatal(revokeErr)
			}
			if admitErr != nil && !errors.Is(admitErr, ErrConflict) && !errors.Is(admitErr, ErrInactive) {
				t.Fatal(admitErr)
			}
			b, e := s.GetBudget(ctx, f.scope, f.result.Business.ID)
			if e != nil || b.ReservedMicros != 0 {
				t.Fatalf("revoked reservation %+v %v", b, e)
			}
			ws, e := s.ListWork(ctx, f.scope, f.result.Business.ID)
			if e != nil {
				t.Fatal(e)
			}
			for _, w := range ws {
				if w.Status != "cancelled" {
					t.Fatal("revoked work still admitted")
				}
			}
		}
	})
	t.Run("issuer_membership_revocation_and_last_owner", func(t *testing.T) {
		f := makeFixture(t, s)
		_, e := s.RevokeMembership(ctx, f.scope, MemberArgs{uuid.NewString(), f.scope.Actor.ID, "", 1})
		mustErr(t, e, ErrConflict)
		newOwner, e := s.AddMember(ctx, f.scope, MemberArgs{uuid.NewString(), uuid.NewString(), "owner", 0})
		if e != nil {
			t.Fatal(e)
		}
		if _, e = s.AdmitWork(ctx, f.scope, f.work(30)); e != nil {
			t.Fatal(e)
		}
		sc := Scope{f.scope.OrganizationID, Actor{"person", newOwner.PersonID}}
		_, e = s.RevokeMembership(ctx, sc, MemberArgs{uuid.NewString(), f.scope.Actor.ID, "", 1})
		if e != nil {
			t.Fatal(e)
		}
		_, e = s.GetBusiness(ctx, f.scope, f.result.Business.ID)
		mustErr(t, e, ErrDenied)
		b, e := s.GetBudget(ctx, sc, f.result.Business.ID)
		if e != nil || b.ReservedMicros != 0 {
			t.Fatalf("issuer did not fence %+v %v", b, e)
		}
		_, e = s.AdmitWork(ctx, Scope{f.scope.OrganizationID, Actor{"agent", f.employment.ID}}, f.work(1))
		mustErr(t, e, ErrInactive)
	})
	t.Run("policy_revision_fences_and_expiry", func(t *testing.T) {
		f := makeFixture(t, s)
		if _, e := s.AdmitWork(ctx, f.scope, f.work(30)); e != nil {
			t.Fatal(e)
		}
		_, e := s.UpdateBusiness(ctx, f.scope, UpdateBusinessArgs{uuid.NewString(), f.result.Business.ID, 2, "active", "human_ceo", "execute_assigned"})
		if e != nil {
			t.Fatal(e)
		}
		_, e = s.AdmitWork(ctx, Scope{f.scope.OrganizationID, Actor{"agent", f.employment.ID}}, f.work(1))
		mustErr(t, e, ErrDenied)
		b, e := s.GetBudget(ctx, f.scope, f.result.Business.ID)
		if e != nil || b.ReservedMicros != 0 {
			t.Fatalf("policy did not fence %+v %v", b, e)
		}
		_, e = s.GrantMandate(ctx, f.scope, MandateArgs{uuid.NewString(), f.result.Business.ID, f.employment.ID, time.Now().Add(-time.Second), 1, 1, 1})
		mustErr(t, e, ErrInvalid)
		_, e = admin.Exec(ctx, `UPDATE business.mandates SET body=jsonb_set(body,'{expiresAt}',to_jsonb('2000-01-01T00:00:00Z'::text)) WHERE organization_id=$1 AND id=$2`, f.scope.OrganizationID, f.mandate.ID)
		if e != nil {
			t.Fatal(e)
		}
		_, e = s.AdmitWork(ctx, f.scope, f.work(1))
		mustErr(t, e, ErrInactive)
	})
	t.Run("restart_and_transaction_rollback", func(t *testing.T) {
		f := makeFixture(t, s)
		in := f.work(20)
		w, e := s.AdmitWork(ctx, f.scope, in)
		if e != nil {
			t.Fatal(e)
		}
		config := runtime.Config().Copy()
		p, e := pgxpool.NewWithConfig(ctx, config)
		if e != nil {
			t.Fatal(e)
		}
		defer p.Close()
		fresh, e := New(ctx, p)
		if e != nil {
			t.Fatal(e)
		}
		replay, e := fresh.AdmitWork(ctx, f.scope, in)
		if e != nil || replay.ID != w.ID {
			t.Fatalf("restart %+v %v", replay, e)
		}
		before, e := s.GetBudget(ctx, f.scope, f.result.Business.ID)
		if e != nil {
			t.Fatal(e)
		}
		_, e = command(ctx, s, f.scope, uuid.NewString(), "test_rollback", nil, true, func(tx pgx.Tx) (Budget, error) {
			_, e := tx.Exec(ctx, `UPDATE business.budgets SET funded_micros=funded_micros+1 WHERE organization_id=$1 AND business_id=$2`, f.scope.OrganizationID, f.result.Business.ID)
			if e != nil {
				return Budget{}, e
			}
			return Budget{}, fmt.Errorf("injected failure before receipt")
		})
		if e == nil {
			t.Fatal("injected failure lost")
		}
		after, e := s.GetBudget(ctx, f.scope, f.result.Business.ID)
		if e != nil || before != after {
			t.Fatalf("partial transaction %+v %+v %v", before, after, e)
		}
	})
}
