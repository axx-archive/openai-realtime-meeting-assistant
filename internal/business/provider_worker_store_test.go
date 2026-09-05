package business

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// This independent fake provider ledger survives replacement of the Worker,
// adapter, bridge and SQL pool. It deliberately supports GET only by accepted
// response ID; losing that ID cannot magically recover by Work or operation ID.
type providerBridgeLedger struct {
	mu            sync.Mutex
	creates, gets int
	responses     map[string]ProviderFactArgs
}
type providerBridgeAdapter struct {
	store                     *Store
	requests                  map[string]ProviderRequest
	ledger                    *providerBridgeLedger
	loseACK                   bool
	crashAfterTerminalReceipt bool
	beforePlanReturn          func()
}

func (a *providerBridgeAdapter) ID() string           { return "test-private-provider" }
func bridgeRequestKey(scope Scope, wid string) string { return scope.OrganizationID + "\x00" + wid }
func (a *providerBridgeAdapter) Plan(ctx context.Context, in WorkerPlanInput) (WorkerPlan, error) {
	r, ok := a.requests[bridgeRequestKey(in.Scope, in.Work.ID)]
	if !ok {
		// A deliberately permissive planner cannot bypass the bridge's required
		// frozen provider request and host grant. This fallback tests that seam.
		r.Request = []byte(in.Work.Objective)
		r.RequestDigest = contentDigest(string(r.Request))
	}
	if a.beforePlanReturn != nil {
		a.beforePlanReturn()
	}
	return WorkerPlan{Operation{in.OperationID, r.RequestDigest, a.ID(), "route-v1", "price-v1", in.Work.HeldMicros}, append([]byte(nil), r.Request...)}, ctx.Err()
}
func (a *providerBridgeAdapter) Execute(ctx context.Context, in WorkerInvocation) (WorkerObservation, error) {
	if e := in.CheckAuthority(ctx); e != nil {
		return WorkerObservation{}, e
	}
	if e := a.store.CheckProviderAuthority(ctx, in.Scope, lease(in.Attempt), in.Operation.ID); e != nil {
		return WorkerObservation{}, e
	}
	token, e := NewProviderReceiptToken()
	if e != nil {
		return WorkerObservation{}, e
	}
	cap, e := a.store.AcquireProviderReceiptCapability(ctx, in.Scope, lease(in.Attempt), token)
	if e != nil {
		return WorkerObservation{}, e
	}
	// Check immediately before the fake external action as well; acquiring a
	// receipt capability is not permission to issue after authority changes.
	if e := in.CheckAuthority(ctx); e != nil {
		return WorkerObservation{}, e
	}
	if e := a.store.CheckProviderAuthority(ctx, in.Scope, lease(in.Attempt), in.Operation.ID); e != nil {
		return WorkerObservation{}, e
	}
	response := "fake-response-" + uuid.NewString()
	cost := int64(17)
	a.ledger.mu.Lock()
	a.ledger.creates++
	a.ledger.responses[response] = terminalFact(response, &cost)
	a.ledger.mu.Unlock()
	if a.loseACK {
		return WorkerObservation{}, errWorkerCrash
	}
	ack := acceptedFact(response)
	ack.IdempotencyKey = "acceptance:" + response
	if _, e = a.store.AppendProviderFact(ctx, cap, ack); e != nil {
		return WorkerObservation{}, e
	}
	return WorkerObservation{Resolved: false}, nil
}
func bridgeObservation(f ProviderFact) WorkerObservation {
	return WorkerObservation{Resolved: true, Outcome: f.Outcome, Content: f.Content, Cost: CostEvidence{f.ActualMicros, f.EvidenceRef()}, OutcomeEvidenceRef: f.EvidenceRef()}
}
func (a *providerBridgeAdapter) Reconcile(ctx context.Context, in WorkerRecovery) (WorkerObservation, error) {
	view, e := a.store.GetProviderJournal(ctx, in.Scope, in.Operation.ID)
	if e != nil {
		return WorkerObservation{}, e
	}
	for _, f := range view.Facts {
		if f.Kind == "terminal" && f.ActualMicros != nil {
			return bridgeObservation(f), nil
		}
	}
	if view.ResponseID == "" {
		return WorkerObservation{Resolved: false}, nil
	}
	// This is the only fake-provider GET. No create is reachable here.
	a.ledger.mu.Lock()
	a.ledger.gets++
	terminal, found := a.ledger.responses[view.ResponseID]
	a.ledger.mu.Unlock()
	if !found {
		return WorkerObservation{Resolved: false}, nil
	}
	token, e := NewProviderReceiptToken()
	if e != nil {
		return WorkerObservation{}, e
	}
	cap, e := a.store.AcquireProviderReceiptCapability(ctx, in.Scope, lease(in.Attempt), token)
	if e != nil {
		return WorkerObservation{}, e
	}
	terminal.IdempotencyKey = "terminal-known:" + view.ResponseID
	fact, e := a.store.AppendProviderFact(ctx, cap, terminal)
	if e != nil {
		return WorkerObservation{}, e
	}
	if a.crashAfterTerminalReceipt {
		return WorkerObservation{}, errWorkerCrash
	}
	return bridgeObservation(fact), nil
}
func newProviderBridgeAdapter(t *testing.T, s *Store, ledger *providerBridgeLedger, scope Scope, works ...Work) *providerBridgeAdapter {
	t.Helper()
	a := &providerBridgeAdapter{store: s, requests: map[string]ProviderRequest{}, ledger: ledger}
	for _, w := range works {
		r, e := s.GetProviderRequest(context.Background(), scope, w.ID)
		if e != nil {
			t.Fatal(e)
		}
		a.requests[bridgeRequestKey(scope, w.ID)] = r
	}
	return a
}
func providerBridgeWorker(t *testing.T, s *Store, a WorkerAdapter) *Worker {
	t.Helper()
	bridge, e := NewProviderWorkerStore(s)
	if e != nil {
		t.Fatal(e)
	}
	w, e := NewWorker(bridge, a, WorkerConfig{WorkerID: "provider-worker-" + uuid.NewString(), LeaseSeconds: 60, StepTimeout: 5 * time.Second})
	if e != nil {
		t.Fatal(e)
	}
	return w
}
func TestProviderWorkerStoreRequiresStore(t *testing.T) {
	for _, s := range []*Store{nil, {}} {
		if _, e := NewProviderWorkerStore(s); !errors.Is(e, ErrInvalid) {
			t.Fatalf("invalid store %v", e)
		}
	}
	var bridge *ProviderWorkerStore
	if _, e := bridge.PrepareOperation(context.Background(), Scope{}, PrepareOperationArgs{}); !errors.Is(e, ErrInvalid) {
		t.Fatalf("nil receiver %v", e)
	}
}
func TestPostgresProviderWorkerBridge(t *testing.T) {
	s, admin, runtime := testStore(t)
	ctx := context.Background()
	issuer, e := NewProviderAdmin(ctx, admin)
	if e != nil {
		t.Fatal(e)
	}
	newLedger := func() *providerBridgeLedger { return &providerBridgeLedger{responses: map[string]ProviderFactArgs{}} }
	t.Run("queued_restart_get_and_terminal_receipt_survive_second_restart", func(t *testing.T) {
		f := makeFixture(t, s)
		g := hostGrant(t, issuer, f, 100, 2, "account-"+uuid.NewString())
		work := providerWork(t, s, f, g, 60)
		ledger := newLedger()
		original, e := s.GetProviderRequest(ctx, f.scope, work.ID)
		if e != nil {
			t.Fatal(e)
		}
		first, e := providerBridgeWorker(t, s, newProviderBridgeAdapter(t, s, ledger, f.scope, work)).Step(ctx, f.scope, work.ID)
		if e != nil || first.State != "reconciliation_required" || first.Attempt == nil {
			t.Fatalf("queued %+v %v", first, e)
		}
		if ledger.creates != 1 || ledger.gets != 0 {
			t.Fatal("initial provider actions", ledger)
		}
		view, e := s.GetProviderJournal(ctx, f.scope, first.Attempt.Operation.ID)
		if e != nil || view.ResponseID == "" || len(view.Facts) != 1 {
			t.Fatalf("durable ACK %+v %v", view, e)
		}
		if view.Journal.Operation.RequestDigest != original.RequestDigest {
			t.Fatal("operation changed preexisting request digest")
		}
		checkProviderBalance(t, s, f, g, 60, 0)
		expire(t, admin, f.scope.OrganizationID, first.Attempt.ID)
		reopened, e := New(ctx, runtime)
		if e != nil {
			t.Fatal(e)
		}
		adapter := newProviderBridgeAdapter(t, reopened, ledger, f.scope, work)
		adapter.crashAfterTerminalReceipt = true
		second, e := providerBridgeWorker(t, reopened, adapter).Step(ctx, f.scope, work.ID)
		if !errors.Is(e, errWorkerCrash) || second.Attempt == nil || second.Attempt.Mode != "reconcile" {
			t.Fatalf("terminal boundary %+v %v", second, e)
		}
		if ledger.creates != 1 || ledger.gets != 1 {
			t.Fatalf("restart created again %d/%d", ledger.creates, ledger.gets)
		}
		checkProviderBalance(t, s, f, g, 60, 0)
		expire(t, admin, f.scope.OrganizationID, second.Attempt.ID)
		reopenedAgain, e := New(ctx, runtime)
		if e != nil {
			t.Fatal(e)
		}
		worker := providerBridgeWorker(t, reopenedAgain, newProviderBridgeAdapter(t, reopenedAgain, ledger, f.scope, work))
		third, e := worker.Step(ctx, f.scope, work.ID)
		if e != nil || third.State != "completed" || third.Result == nil || !third.Result.Eligible {
			t.Fatalf("recovered result %+v %v", third, e)
		}
		if ledger.creates != 1 || ledger.gets != 1 {
			t.Fatal("retained terminal receipt did network work")
		}
		checkProviderBalance(t, s, f, g, 0, 17)
		view, e = reopenedAgain.GetProviderJournal(ctx, f.scope, third.Attempt.Operation.ID)
		if e != nil || len(view.Facts) != 2 {
			t.Fatalf("journal %+v %v", view, e)
		}
		if third.Attempt.OutcomeEvidenceRef != view.Facts[1].EvidenceRef() {
			t.Fatal("outcome not exact saved fact")
		}
		var settlementRef string
		if e = admin.QueryRow(ctx, `SELECT evidence_ref FROM business.settlements WHERE organization_id=$1 AND attempt_id=$2`, f.scope.OrganizationID, third.Attempt.ID).Scan(&settlementRef); e != nil || settlementRef != view.Facts[1].EvidenceRef() {
			t.Fatalf("settlement receipt %s %v", settlementRef, e)
		}
		fourth, e := worker.Step(ctx, f.scope, work.ID)
		if e != nil || fourth.Result.ID != third.Result.ID || ledger.creates != 1 || ledger.gets != 1 {
			t.Fatalf("terminal replay %+v %v", fourth, e)
		}
		checkProviderBalance(t, s, f, g, 0, 17)
		finalRequest, e := s.GetProviderRequest(ctx, f.scope, work.ID)
		if e != nil || string(finalRequest.Request) != string(original.Request) {
			t.Fatalf("frozen request changed %+v %v", finalRequest, e)
		}
	})
	t.Run("ordinary_unfunded_work_cannot_cross_provider_bridge", func(t *testing.T) {
		f := makeFixture(t, s)
		work := admit(t, s, f)
		ledger := newLedger()
		adapter := newProviderBridgeAdapter(t, s, ledger, f.scope)
		out, e := providerBridgeWorker(t, s, adapter).Step(ctx, f.scope, work.ID)
		if !errors.Is(e, ErrNotFound) || ledger.creates != 0 || out.Attempt == nil || out.Attempt.Operation != nil {
			t.Fatalf("unfunded execution %+v %v", out, e)
		}
		var count int
		if e = admin.QueryRow(ctx, `SELECT count(*) FROM business.provider_journal WHERE organization_id=$1`, f.scope.OrganizationID).Scan(&count); e != nil || count != 0 {
			t.Fatalf("unfunded journal %d %v", count, e)
		}
	})
	t.Run("grant_revoked_between_plan_and_prepare_never_executes", func(t *testing.T) {
		f := makeFixture(t, s)
		g := hostGrant(t, issuer, f, 100, 2, "account-"+uuid.NewString())
		work := providerWork(t, s, f, g, 60)
		ledger := newLedger()
		adapter := newProviderBridgeAdapter(t, s, ledger, f.scope, work)
		adapter.beforePlanReturn = func() {
			if _, e := issuer.RevokeGrant(ctx, f.scope.OrganizationID, g.ID); e != nil {
				t.Fatal(e)
			}
		}
		_, e := providerBridgeWorker(t, s, adapter).Step(ctx, f.scope, work.ID)
		if !errors.Is(e, ErrLease) || ledger.creates != 0 {
			t.Fatalf("revoked execution %d %v", ledger.creates, e)
		}
		checkProviderBalance(t, s, f, g, 0, 0)
	})
	t.Run("lost_create_ack_stays_unknown_without_second_create", func(t *testing.T) {
		f := makeFixture(t, s)
		g := hostGrant(t, issuer, f, 100, 2, "account-"+uuid.NewString())
		work := providerWork(t, s, f, g, 60)
		ledger := newLedger()
		adapter := newProviderBridgeAdapter(t, s, ledger, f.scope, work)
		adapter.loseACK = true
		first, e := providerBridgeWorker(t, s, adapter).Step(ctx, f.scope, work.ID)
		if !errors.Is(e, errWorkerCrash) || first.Attempt == nil || ledger.creates != 1 {
			t.Fatalf("lost ACK %+v %v", first, e)
		}
		for i := 0; i < 2; i++ {
			expire(t, admin, f.scope.OrganizationID, first.Attempt.ID)
			reopened, e := New(ctx, runtime)
			if e != nil {
				t.Fatal(e)
			}
			out, e := providerBridgeWorker(t, reopened, newProviderBridgeAdapter(t, reopened, ledger, f.scope, work)).Step(ctx, f.scope, work.ID)
			if e != nil || out.State != "reconciliation_required" || out.Result != nil {
				t.Fatalf("unknown recovery %+v %v", out, e)
			}
		}
		if ledger.creates != 1 || ledger.gets != 0 {
			t.Fatalf("lost ACK caused action %d/%d", ledger.creates, ledger.gets)
		}
		checkProviderBalance(t, s, f, g, 60, 0)
		_, e = s.AdmitProviderWork(ctx, f.scope, ProviderWorkArgs{f.work(0), g.ID, []byte(`{"input":"extra"}`), json.RawMessage(`[]`)})
		mustErr(t, e, ErrReconciliation)
	})
}
