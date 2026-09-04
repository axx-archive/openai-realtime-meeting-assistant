package business

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

var errWorkerCrash = errors.New("injected worker process boundary")

// This ledger stands in for an independent provider's operation lookup. New
// worker/adapter instances share it, just as a provider survives our restart.
// It does not call any provider or claim provider interoperability.
type workerTestLedger struct {
	mu                                   sync.Mutex
	outcomes                             map[string]WorkerObservation
	executes, accepts, reconciles, plans int
	definitiveAbsent                     bool
}
type workerTestAdapter struct {
	ledger       *workerTestLedger
	planHook     func(WorkerPlanInput)
	executeHook  func(context.Context, WorkerInvocation) error
	afterAccept  func(context.Context, WorkerInvocation) error
	recoveryHook func(WorkerRecovery)
	unknownCost  bool
	badDigest    bool
}

func (a *workerTestAdapter) ID() string { return "test-private-document-adapter" }
func (a *workerTestAdapter) Plan(ctx context.Context, in WorkerPlanInput) (WorkerPlan, error) {
	a.ledger.mu.Lock()
	a.ledger.plans++
	a.ledger.mu.Unlock()
	if a.planHook != nil {
		a.planHook(in)
	}
	request := []byte(in.Work.Objective)
	op := Operation{in.OperationID, contentDigest(string(request)), a.ID(), "test-route-1", "test-price-1", 40}
	if a.badDigest {
		op.RequestDigest = "changed"
	}
	return WorkerPlan{op, request}, ctx.Err()
}
func (a *workerTestAdapter) Execute(ctx context.Context, in WorkerInvocation) (WorkerObservation, error) {
	a.ledger.mu.Lock()
	a.ledger.executes++
	a.ledger.mu.Unlock()
	if a.executeHook != nil {
		if e := a.executeHook(ctx, in); e != nil {
			return WorkerObservation{}, e
		}
	}
	if e := in.CheckAuthority(ctx); e != nil {
		return WorkerObservation{}, e
	}
	cost := int64(17)
	o := WorkerObservation{true, "succeeded", "A useful private document.", CostEvidence{&cost, "test-cost:" + in.Operation.ID}, "test-outcome:" + in.Operation.ID}
	if a.unknownCost {
		o.Cost.ActualMicros = nil
	}
	a.ledger.mu.Lock()
	a.ledger.accepts++
	a.ledger.outcomes[in.Operation.ID] = o
	a.ledger.mu.Unlock()
	if a.afterAccept != nil {
		if e := a.afterAccept(ctx, in); e != nil {
			return WorkerObservation{}, e
		}
	}
	return o, nil
}
func (a *workerTestAdapter) Reconcile(ctx context.Context, in WorkerRecovery) (WorkerObservation, error) {
	if a.recoveryHook != nil {
		a.recoveryHook(in)
	}
	a.ledger.mu.Lock()
	defer a.ledger.mu.Unlock()
	a.ledger.reconciles++
	if o, ok := a.ledger.outcomes[in.Operation.ID]; ok {
		return o, ctx.Err()
	}
	if a.ledger.definitiveAbsent {
		zero := int64(0)
		return WorkerObservation{Resolved: true, Outcome: "not_accepted", Cost: CostEvidence{&zero, "authoritative-test-no-charge:" + in.Operation.ID}, OutcomeEvidenceRef: "authoritative-test-nonacceptance:" + in.Operation.ID}, nil
	}
	return WorkerObservation{Resolved: false}, ctx.Err()
}

func TestPostgresWorkerAdapterScopeSurvivesRestart(t *testing.T) {
	s, admin, pool := testStore(t)
	ctx := context.Background()
	a := newWorkerTestAdapter()
	expected := map[string]Scope{}
	seen := map[string]int{}
	check := func(stage string, scope Scope, workID string) {
		t.Helper()
		if scope != expected[workID] {
			t.Fatalf("%s: incorrect adapter scope %+v", stage, scope)
		}
		seen[stage]++
	}
	a.planHook = func(in WorkerPlanInput) { check("plan", in.Scope, in.Work.ID) }
	a.executeHook = func(_ context.Context, in WorkerInvocation) error { check("execute", in.Scope, in.Work.ID); return nil }
	a.afterAccept = func(context.Context, WorkerInvocation) error { return errWorkerCrash }
	a.recoveryHook = func(in WorkerRecovery) { check("reconcile", in.Scope, in.Work.ID) }
	for i := 0; i < 2; i++ {
		f := makeFixture(t, s)
		work := admit(t, s, f)
		expected[work.ID] = f.scope
		_, err := driver(t, s, a).Step(ctx, f.scope, work.ID)
		mustErr(t, err, errWorkerCrash)
		expireWorkAttempt(t, s, admin, f, work)
		out, err := driver(t, reopenWorkerStore(t, pool), a).Step(ctx, f.scope, work.ID)
		if err != nil || out.State != "completed" {
			t.Fatal(out.State, err)
		}
	}
	for _, stage := range []string{"plan", "execute", "reconcile"} {
		if seen[stage] != 2 {
			t.Fatal(stage, seen[stage])
		}
	}
}
func newWorkerTestAdapter() *workerTestAdapter {
	return &workerTestAdapter{ledger: &workerTestLedger{outcomes: map[string]WorkerObservation{}}}
}
func driver(t *testing.T, s WorkerStore, a WorkerAdapter) *Worker {
	t.Helper()
	w, e := NewWorker(s, a, WorkerConfig{"test-worker", 60, 5 * time.Second})
	if e != nil {
		t.Fatal(e)
	}
	return w
}
func reopenWorkerStore(t *testing.T, runtime *pgxpool.Pool) *Store {
	t.Helper()
	p, e := pgxpool.NewWithConfig(context.Background(), runtime.Config().Copy())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(p.Close)
	s, e := New(context.Background(), p)
	if e != nil {
		t.Fatal(e)
	}
	return s
}

type workerCrashStore struct {
	WorkerStore
	beforePrepare, afterPrepare, afterComplete bool
}

func (s workerCrashStore) PrepareOperation(ctx context.Context, scope Scope, in PrepareOperationArgs) (Attempt, error) {
	if s.beforePrepare {
		return Attempt{}, errWorkerCrash
	}
	a, e := s.WorkerStore.PrepareOperation(ctx, scope, in)
	if e == nil && s.afterPrepare {
		return Attempt{}, errWorkerCrash
	}
	return a, e
}
func (s workerCrashStore) CompleteAttempt(ctx context.Context, scope Scope, in CompleteAttemptArgs) (AttemptCompletion, error) {
	o, e := s.WorkerStore.CompleteAttempt(ctx, scope, in)
	if e == nil && s.afterComplete {
		return AttemptCompletion{}, errWorkerCrash
	}
	return o, e
}
func expireWorkAttempt(t *testing.T, s *Store, admin *pgxpool.Pool, f fixture, w Work) {
	t.Helper()
	as, e := s.ListAttempts(context.Background(), f.scope, w.ID)
	if e != nil || len(as) == 0 {
		t.Fatalf("missing attempt %+v %v", as, e)
	}
	expire(t, admin, f.scope.OrganizationID, as[len(as)-1].ID)
}

type workerDeadlineStore struct{ WorkerStore }

func (s workerDeadlineStore) RenewAttempt(ctx context.Context, scope Scope, l AttemptLease, seconds int) (Attempt, error) {
	if _, ok := ctx.Deadline(); !ok {
		return Attempt{}, errors.New("adapter escaped worker deadline")
	}
	return s.WorkerStore.RenewAttempt(ctx, scope, l, seconds)
}

func TestWorkerRequiresExplicitAdapter(t *testing.T) {
	_, e := NewWorker(&Store{}, nil, WorkerConfig{"worker", 60, time.Second})
	mustErr(t, e, ErrWorkerAdapterUnavailable)
}
func TestPostgresBoundedWorker(t *testing.T) {
	s, admin, runtime := testStore(t)
	ctx := context.Background()
	t.Run("one_step_actual_private_result_and_settlement", func(t *testing.T) {
		f := makeFixture(t, s)
		w := admit(t, s, f)
		a := newWorkerTestAdapter()
		out, e := driver(t, s, a).Step(ctx, f.scope, w.ID)
		if e != nil || out.State != "completed" || out.Result == nil || !out.Result.Eligible {
			t.Fatalf("outcome %+v %v", out, e)
		}
		if a.ledger.accepts != 1 || a.ledger.reconciles != 0 {
			t.Fatal("unexpected adapter effects")
		}
		b, e := s.GetBudget(ctx, f.scope, w.BusinessID)
		if e != nil || b.SettledMicros != 17 || b.ReservedMicros != 0 {
			t.Fatalf("settlement %+v %v", b, e)
		}
		again, e := driver(t, reopenWorkerStore(t, runtime), a).Step(ctx, f.scope, w.ID)
		if e != nil || again.State != "terminal" || again.Result.ID != out.Result.ID || a.ledger.accepts != 1 {
			t.Fatalf("terminal replay %+v %v", again, e)
		}
	})
	t.Run("crash_before_marker_reclaims_without_new_root", func(t *testing.T) {
		f := makeFixture(t, s)
		w := admit(t, s, f)
		a := newWorkerTestAdapter()
		_, e := driver(t, workerCrashStore{WorkerStore: s, beforePrepare: true}, a).Step(ctx, f.scope, w.ID)
		mustErr(t, e, errWorkerCrash)
		as, e := s.ListAttempts(ctx, f.scope, w.ID)
		if e != nil || len(as) != 1 || as[0].Operation != nil {
			t.Fatalf("marker unexpectedly written %+v %v", as, e)
		}
		first := as[0]
		expireWorkAttempt(t, s, admin, f, w)
		out, e := driver(t, reopenWorkerStore(t, runtime), a).Step(ctx, f.scope, w.ID)
		if e != nil || out.Attempt.ID != first.ID || out.Attempt.Generation <= first.Generation || out.Work.ID != w.ID || a.ledger.accepts != 1 {
			t.Fatalf("reclaim %+v %v", out, e)
		}
	})
	t.Run("crash_after_marker_never_blindly_executes", func(t *testing.T) {
		f := makeFixture(t, s)
		w := admit(t, s, f)
		a := newWorkerTestAdapter()
		_, e := driver(t, workerCrashStore{WorkerStore: s, afterPrepare: true}, a).Step(ctx, f.scope, w.ID)
		mustErr(t, e, errWorkerCrash)
		if a.ledger.executes != 0 {
			t.Fatal("called adapter after ambiguous commit")
		}
		expireWorkAttempt(t, s, admin, f, w)
		out, e := driver(t, reopenWorkerStore(t, runtime), a).Step(ctx, f.scope, w.ID)
		if e != nil || out.State != "reconciliation_required" || a.ledger.executes != 0 || a.ledger.reconciles != 1 {
			t.Fatalf("blind retry %+v %v", out, e)
		}
		b, e := s.GetBudget(ctx, f.scope, w.BusinessID)
		if e != nil || b.ReservedMicros != 60 || b.SettledMicros != 0 {
			t.Fatalf("unknown exposure lost %+v %v", b, e)
		}
		a.ledger.definitiveAbsent = true
		expireWorkAttempt(t, s, admin, f, w)
		out, e = driver(t, s, a).Step(ctx, f.scope, w.ID)
		if e != nil || out.State != "retry_ready" || a.ledger.executes != 0 {
			t.Fatalf("nonacceptance auto-retried %+v %v", out, e)
		}
		out, e = driver(t, s, a).Step(ctx, f.scope, w.ID)
		if e != nil || out.State != "completed" || out.Attempt.Ordinal != 2 || a.ledger.accepts != 1 {
			t.Fatalf("next bounded attempt %+v %v", out, e)
		}
	})
	t.Run("crash_after_acceptance_recovers_provider_receipt", func(t *testing.T) {
		f := makeFixture(t, s)
		w := admit(t, s, f)
		a := newWorkerTestAdapter()
		a.afterAccept = func(context.Context, WorkerInvocation) error { return errWorkerCrash }
		_, e := driver(t, s, a).Step(ctx, f.scope, w.ID)
		mustErr(t, e, errWorkerCrash)
		expireWorkAttempt(t, s, admin, f, w)
		freshAdapter := &workerTestAdapter{ledger: a.ledger}
		out, e := driver(t, reopenWorkerStore(t, runtime), freshAdapter).Step(ctx, f.scope, w.ID)
		if e != nil || out.State != "completed" || a.ledger.executes != 1 || a.ledger.reconciles != 1 || out.Attempt.OutcomeEvidenceRef == "" {
			t.Fatalf("acceptance recovery %+v %v", out, e)
		}
	})
	t.Run("lost_terminal_ack_does_not_recharge", func(t *testing.T) {
		f := makeFixture(t, s)
		w := admit(t, s, f)
		a := newWorkerTestAdapter()
		_, e := driver(t, workerCrashStore{WorkerStore: s, afterComplete: true}, a).Step(ctx, f.scope, w.ID)
		mustErr(t, e, errWorkerCrash)
		out, e := driver(t, reopenWorkerStore(t, runtime), a).Step(ctx, f.scope, w.ID)
		if e != nil || out.State != "terminal" || out.Result == nil || a.ledger.executes != 1 || a.ledger.reconciles != 0 {
			t.Fatalf("ack recovery %+v %v", out, e)
		}
		b, e := s.GetBudget(ctx, f.scope, w.BusinessID)
		if e != nil || b.SettledMicros != 17 {
			t.Fatalf("duplicate charge %+v %v", b, e)
		}
	})
	t.Run("cancelled_context_cannot_start", func(t *testing.T) {
		f := makeFixture(t, s)
		w := admit(t, s, f)
		a := newWorkerTestAdapter()
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		_, e := driver(t, s, a).Step(cancelled, f.scope, w.ID)
		mustErr(t, e, context.Canceled)
		if a.ledger.plans != 0 || a.ledger.executes != 0 {
			t.Fatal("cancelled call executed")
		}
	})
	t.Run("revocation_before_adapter_effect_and_private_reconciliation", func(t *testing.T) {
		f := makeFixture(t, s)
		w := admit(t, s, f)
		a := newWorkerTestAdapter()
		a.executeHook = func(context.Context, WorkerInvocation) error {
			_, e := s.RevokeMandate(ctx, f.scope, RevokeMandateArgs{uuid.NewString(), f.mandate.ID, 1})
			return e
		}
		_, e := driver(t, s, a).Step(ctx, f.scope, w.ID)
		mustErr(t, e, ErrLease)
		if a.ledger.accepts != 0 {
			t.Fatal("revoked effect accepted")
		}
		b, e := s.GetBudget(ctx, f.scope, w.BusinessID)
		if e != nil || b.ReservedMicros != 60 {
			t.Fatalf("possible issuance falsely released %+v %v", b, e)
		}
		a.ledger.definitiveAbsent = true
		out, e := driver(t, s, a).Step(ctx, f.scope, w.ID)
		if e != nil || out.Work.Status != "cancelled" || a.ledger.accepts != 0 {
			t.Fatalf("cancel recovery %+v %v", out, e)
		}
	})
	t.Run("bounded_timeout_after_acceptance_keeps_liability", func(t *testing.T) {
		f := makeFixture(t, s)
		w := admit(t, s, f)
		a := newWorkerTestAdapter()
		a.afterAccept = func(c context.Context, _ WorkerInvocation) error { <-c.Done(); return c.Err() }
		bounded, e := NewWorker(s, a, WorkerConfig{"bounded-worker", 5, 50 * time.Millisecond})
		if e != nil {
			t.Fatal(e)
		}
		out, e := bounded.Step(ctx, f.scope, w.ID)
		mustErr(t, e, context.DeadlineExceeded)
		if out.State != "reconciliation_required" {
			t.Fatalf("timeout lost uncertainty %+v", out)
		}
		expireWorkAttempt(t, s, admin, f, w)
		fresh := &workerTestAdapter{ledger: a.ledger}
		out, e = driver(t, s, fresh).Step(ctx, f.scope, w.ID)
		if e != nil || out.State != "completed" || a.ledger.accepts != 1 {
			t.Fatalf("timeout recovery %+v %v", out, e)
		}
	})
	t.Run("stale_generation_cannot_terminalize", func(t *testing.T) {
		f := makeFixture(t, s)
		w := admit(t, s, f)
		a := newWorkerTestAdapter()
		a.afterAccept = func(_ context.Context, in WorkerInvocation) error {
			expire(t, admin, f.scope.OrganizationID, in.Attempt.ID)
			_, e := s.ClaimAttempt(ctx, f.scope, ClaimAttemptArgs{w.ID, "competing-worker", uuid.NewString(), 60})
			return e
		}
		_, e := driver(t, s, a).Step(ctx, f.scope, w.ID)
		mustErr(t, e, ErrLease)
		current, e := s.GetWork(ctx, f.scope, w.ID)
		if e != nil || current.ResultID != "" {
			t.Fatalf("stale result committed %+v %v", current, e)
		}
		expireWorkAttempt(t, s, admin, f, w)
		fresh := &workerTestAdapter{ledger: a.ledger}
		out, e := driver(t, s, fresh).Step(ctx, f.scope, w.ID)
		if e != nil || out.State != "completed" || a.ledger.accepts != 1 {
			t.Fatalf("fence recovery %+v %v", out, e)
		}
	})
	t.Run("unknown_cost_then_reconcile_same_result", func(t *testing.T) {
		f := makeFixture(t, s)
		w := admit(t, s, f)
		a := newWorkerTestAdapter()
		a.unknownCost = true
		out, e := driver(t, s, a).Step(ctx, f.scope, w.ID)
		if e != nil || out.State != "reconciliation_required" || out.Result == nil {
			t.Fatalf("unknown cost %+v %v", out, e)
		}
		rid := out.Result.ID
		cost := int64(19)
		a.ledger.mu.Lock()
		ob := a.ledger.outcomes[out.Attempt.Operation.ID]
		ob.Cost.ActualMicros = &cost
		a.ledger.outcomes[out.Attempt.Operation.ID] = ob
		a.ledger.mu.Unlock()
		expireWorkAttempt(t, s, admin, f, w)
		out, e = driver(t, reopenWorkerStore(t, runtime), &workerTestAdapter{ledger: a.ledger}).Step(ctx, f.scope, w.ID)
		if e != nil || out.State != "completed" || out.Result.ID != rid || a.ledger.executes != 1 {
			t.Fatalf("unknown settlement %+v %v", out, e)
		}
		b, e := s.GetBudget(ctx, f.scope, w.BusinessID)
		if e != nil || b.SettledMicros != 19 || b.ReservedMicros != 0 {
			t.Fatalf("settled budget %+v %v", b, e)
		}
	})
	t.Run("overlapping_same_named_worker_cannot_execute_twice", func(t *testing.T) {
		f := makeFixture(t, s)
		w := admit(t, s, f)
		a := newWorkerTestAdapter()
		entered, release := make(chan struct{}), make(chan struct{})
		a.executeHook = func(context.Context, WorkerInvocation) error { close(entered); <-release; return nil }
		first := driver(t, s, a)
		done := make(chan error, 1)
		go func() { _, e := first.Step(ctx, f.scope, w.ID); done <- e }()
		<-entered
		out, e := driver(t, s, a).Step(ctx, f.scope, w.ID)
		mustErr(t, e, ErrConcurrency)
		if out.State != "busy" {
			t.Fatalf("overlap %+v", out)
		}
		close(release)
		if e = <-done; e != nil {
			t.Fatal(e)
		}
		if a.ledger.accepts != 1 {
			t.Fatal("duplicate execution")
		}
	})
	t.Run("adapter_cannot_remove_authority_check_deadline", func(t *testing.T) {
		f := makeFixture(t, s)
		w := admit(t, s, f)
		a := newWorkerTestAdapter()
		a.executeHook = func(_ context.Context, in WorkerInvocation) error { return in.CheckAuthority(context.Background()) }
		out, e := driver(t, workerDeadlineStore{s}, a).Step(ctx, f.scope, w.ID)
		if e != nil || out.State != "completed" {
			t.Fatalf("authority deadline %+v %v", out, e)
		}
	})
	t.Run("invalid_plan_and_changed_authority_do_not_issue", func(t *testing.T) {
		for _, changeAuthority := range []bool{false, true} {
			f := makeFixture(t, s)
			w := admit(t, s, f)
			a := newWorkerTestAdapter()
			a.badDigest = !changeAuthority
			if changeAuthority {
				a.planHook = func(WorkerPlanInput) {
					if _, e := s.RevokeMandate(ctx, f.scope, RevokeMandateArgs{uuid.NewString(), f.mandate.ID, 1}); e != nil {
						t.Fatal(e)
					}
				}
			}
			_, e := driver(t, s, a).Step(ctx, f.scope, w.ID)
			if e == nil || a.ledger.executes != 0 {
				t.Fatalf("unsafe plan issued %v", e)
			}
		}
	})
}
