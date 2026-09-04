package business

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

var ErrWorkerAdapterUnavailable = errors.New("business: execution adapter unavailable")

// WorkerStore is the existing persistent authority boundary. Implementations
// must preserve the Store's transaction, fencing and settlement semantics.
type WorkerStore interface {
	GetWork(context.Context, Scope, string) (Work, error)
	GetResult(context.Context, Scope, string) (Result, error)
	ClaimAttempt(context.Context, Scope, ClaimAttemptArgs) (Attempt, error)
	RenewAttempt(context.Context, Scope, AttemptLease, int) (Attempt, error)
	PrepareOperation(context.Context, Scope, PrepareOperationArgs) (Attempt, error)
	CompleteAttempt(context.Context, Scope, CompleteAttemptArgs) (AttemptCompletion, error)
	ReconcileAttempt(context.Context, Scope, ReconcileAttemptArgs) (AttemptCompletion, error)
}

// WorkerAdapter is installed by the server, never selected by a prompt or HTTP
// request. This package supplies no real provider implementation. A Dissent
// adapter could compile its route/price/request in Plan and record actual route
// evidence through the outcome reference; no qualification is implied here.
//
// Plan MUST be local and effect-free, including no paid model invocation. Execute
// MUST honor ctx and call CheckAuthority immediately before its one external
// operation. Reconcile MUST inspect the exact existing operation without issuing
// another generation request. If its API cannot establish an outcome, it returns
// Resolved:false; it must not turn a missing acknowledgement into nonacceptance.
// Real activation additionally requires an adapter-specific idempotency,
// cancellation, source/resource authority and reconciliation proof. Database
// fencing cannot retroactively prevent an external action already accepted.
type WorkerAdapter interface {
	ID() string
	Plan(context.Context, WorkerPlanInput) (WorkerPlan, error)
	Execute(context.Context, WorkerInvocation) (WorkerObservation, error)
	Reconcile(context.Context, WorkerRecovery) (WorkerObservation, error)
}

type WorkerPlanInput struct {
	Scope       Scope
	Work        Work
	AttemptID   string
	OperationID string
}
type WorkerPlan struct {
	Operation Operation
	Request   []byte
}
type WorkerInvocation struct {
	Scope     Scope
	Work      Work
	Attempt   Attempt
	Operation Operation
	Request   []byte
	// CheckAuthority renews the exact current lease, checking current private-work
	// authority in SQL. It is an action-time check, not a transferable capability.
	CheckAuthority func(context.Context) error
}
type WorkerRecovery struct {
	Scope          Scope
	Work           Work
	Attempt        Attempt
	Operation      Operation
	ExistingResult *Result
}
type WorkerObservation struct {
	Resolved           bool
	Outcome            string
	Content            string
	Cost               CostEvidence
	OutcomeEvidenceRef string
}
type WorkerConfig struct {
	WorkerID     string
	LeaseSeconds int
	StepTimeout  time.Duration
}
type WorkerStep struct {
	State   string   `json:"state"`
	Work    Work     `json:"work"`
	Attempt *Attempt `json:"attempt,omitempty"`
	Result  *Result  `json:"result,omitempty"`
}
type Worker struct {
	store   WorkerStore
	adapter WorkerAdapter
	config  WorkerConfig
}

func NewWorker(store WorkerStore, adapter WorkerAdapter, c WorkerConfig) (*Worker, error) {
	if store == nil {
		return nil, ErrInvalid
	}
	if adapter == nil {
		return nil, ErrWorkerAdapterUnavailable
	}
	if !validText(adapter.ID(), 200) || !validText(c.WorkerID, 200) || c.LeaseSeconds < 5 || c.LeaseSeconds > 300 || c.StepTimeout <= 0 || c.StepTimeout > time.Duration(c.LeaseSeconds-1)*time.Second {
		return nil, ErrInvalid
	}
	return &Worker{store, adapter, c}, nil
}

// Step performs at most one adapter Execute or Reconcile and has no polling,
// automatic retry, scheduling or daemon activation. Each invocation uses a new
// claim key; even another invocation with the same worker name must wait for an
// active lease rather than assuming it owns the in-flight call. A process that
// dies after Prepare leaves durable possible issuance for a later reconciler.
func (w *Worker) Step(parent context.Context, scope Scope, workID string) (WorkerStep, error) {
	out := WorkerStep{State: "blocked"}
	ctx, cancel := context.WithTimeout(parent, w.config.StepTimeout)
	defer cancel()
	if e := ctx.Err(); e != nil {
		return out, e
	}
	work, e := w.store.GetWork(ctx, scope, workID)
	if e != nil {
		return out, e
	}
	out.Work = work
	if work.Status == "completed" || work.Status == "cancelled" || work.Status == "failed" {
		out.State = "terminal"
		if work.ResultID != "" {
			r, e := w.store.GetResult(ctx, scope, work.ID)
			if e != nil {
				return out, e
			}
			out.Result = &r
		}
		return out, nil
	}
	attempt, e := w.store.ClaimAttempt(ctx, scope, ClaimAttemptArgs{work.ID, w.config.WorkerID, uuid.NewString(), w.config.LeaseSeconds})
	if e != nil {
		if errors.Is(e, ErrConcurrency) {
			out.State = "busy"
		}
		return out, e
	}
	out.Attempt = &attempt
	// Reload current Work after claiming: a previous attempt may have changed its
	// result/held balance between our initial read and this transaction.
	work, e = w.store.GetWork(ctx, scope, work.ID)
	if e != nil {
		return out, e
	}
	out.Work = work
	if attempt.Mode == "reconcile" {
		return w.recover(ctx, scope, out)
	}
	if attempt.Operation != nil {
		out.State = "reconciliation_required"
		return out, ErrReconciliation
	}
	if attempt.State != "claimed" {
		return out, ErrInactive
	}
	operationID := "operation_" + uuid.NewSHA1(uuid.NameSpaceOID, []byte(scope.OrganizationID+"\x00"+attempt.ID)).String()
	plan, e := w.adapter.Plan(ctx, WorkerPlanInput{Scope: scope, Work: work, AttemptID: attempt.ID, OperationID: operationID})
	if e != nil {
		return out, e
	}
	if e = ctx.Err(); e != nil {
		return out, e
	}
	if plan.Operation.ID != operationID || plan.Operation.AdapterID != w.adapter.ID() || len(plan.Request) == 0 || len(plan.Request) > 256000 || plan.Operation.RequestDigest != contentDigest(string(plan.Request)) {
		return out, fmt.Errorf("%w: adapter plan binding", ErrInvalid)
	}
	// Detach the bytes from any mutable planning buffer. The adapter receives only
	// this exact digest-bound request, not permission to reconstruct a new prompt.
	request := append([]byte(nil), plan.Request...)
	attempt, e = w.store.PrepareOperation(ctx, scope, PrepareOperationArgs{attemptLease(attempt), plan.Operation})
	if e != nil {
		return out, e
	}
	out.Attempt = &attempt
	out.State = "reconciliation_required"
	if e = ctx.Err(); e != nil {
		return out, e
	}
	gate := func(callCtx context.Context) error {
		if e := ctx.Err(); e != nil {
			return e
		}
		if callCtx == nil {
			return ErrInvalid
		}
		// An adapter cannot remove the Step deadline by supplying Background.
		gateCtx, stopGate := context.WithCancel(ctx)
		stopCaller := context.AfterFunc(callCtx, stopGate)
		defer stopCaller()
		defer stopGate()
		if e := callCtx.Err(); e != nil {
			return e
		}
		_, e := w.store.RenewAttempt(gateCtx, scope, attemptLease(attempt), w.config.LeaseSeconds)
		return e
	}
	if e = gate(ctx); e != nil {
		return out, e
	}
	observation, e := w.adapter.Execute(ctx, WorkerInvocation{Scope: scope, Work: work, Attempt: attempt, Operation: plan.Operation, Request: request, CheckAuthority: gate})
	if e != nil {
		return out, e
	}
	if e = ctx.Err(); e != nil {
		return out, e
	}
	if !observation.Resolved {
		return out, nil
	}
	completion, e := w.store.CompleteAttempt(ctx, scope, observationArgs(attempt, observation))
	if e != nil {
		return out, e
	}
	return workerCompleted(completion), nil
}
func (w *Worker) recover(ctx context.Context, scope Scope, out WorkerStep) (WorkerStep, error) {
	out.State = "reconciliation_required"
	a := *out.Attempt
	if a.Operation == nil {
		return out, ErrInvalid
	}
	if a.Operation.AdapterID != w.adapter.ID() {
		return out, ErrWorkerAdapterUnavailable
	}
	var result *Result
	if a.ResultID != "" {
		r, e := w.store.GetResult(ctx, scope, out.Work.ID)
		if e != nil {
			return out, e
		}
		result = &r
		out.Result = &r
	}
	observation, e := w.adapter.Reconcile(ctx, WorkerRecovery{Scope: scope, Work: out.Work, Attempt: a, Operation: *a.Operation, ExistingResult: result})
	if e != nil {
		return out, e
	}
	if e = ctx.Err(); e != nil {
		return out, e
	}
	if !observation.Resolved {
		return out, nil
	}
	completion, e := w.store.ReconcileAttempt(ctx, scope, ReconcileAttemptArgs(observationArgs(a, observation)))
	if e != nil {
		return out, e
	}
	return workerCompleted(completion), nil
}
func attemptLease(a Attempt) AttemptLease { return AttemptLease{a.ID, a.WorkerID, a.Generation} }
func observationArgs(a Attempt, o WorkerObservation) CompleteAttemptArgs {
	digest := ""
	if o.Outcome == "succeeded" {
		digest = contentDigest(o.Content)
	}
	return CompleteAttemptArgs{attemptLease(a), a.Operation.ID, o.Outcome, o.Content, digest, o.Cost, o.OutcomeEvidenceRef}
}
func workerCompleted(c AttemptCompletion) WorkerStep {
	state := "completed"
	if c.Work.Status == "reconciling" {
		state = "reconciliation_required"
	} else if c.Work.Status == "admitted" {
		state = "retry_ready"
	} else if c.Work.Status == "cancelled" || c.Work.Status == "failed" {
		state = "terminal"
	}
	return WorkerStep{state, c.Work, &c.Attempt, c.Result}
}
