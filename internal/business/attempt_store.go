package business

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

func (s *Store) GetMembership(ctx context.Context, scope Scope) (Membership, error) {
	var m Membership
	e := s.read(ctx, scope, func(tx pgx.Tx) error { var e error; m, e = member(ctx, tx, scope, false); return e })
	return m, e
}
func (s *Store) ListEmployments(ctx context.Context, scope Scope, bid string) ([]Employment, error) {
	out := []Employment{}
	e := s.read(ctx, scope, func(tx pgx.Tx) error {
		if e := businessAccess(ctx, tx, scope, bid); e != nil {
			return e
		}
		rows, e := tx.Query(ctx, `SELECT body FROM business.employments WHERE organization_id=$1 AND business_id=$2 ORDER BY id`, scope.OrganizationID, bid)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var raw []byte
			var emp Employment
			if e = rows.Scan(&raw); e != nil {
				return e
			}
			if e = json.Unmarshal(raw, &emp); e != nil {
				return e
			}
			out = append(out, emp)
		}
		return rows.Err()
	})
	return out, e
}
func (s *Store) GetWork(ctx context.Context, scope Scope, wid string) (Work, error) {
	var w Work
	e := s.read(ctx, scope, func(tx pgx.Tx) error {
		if e := body(ctx, tx, "work_intents", scope.OrganizationID, wid, &w); e != nil {
			return e
		}
		return businessAccess(ctx, tx, scope, w.BusinessID)
	})
	return w, e
}
func (s *Store) ListAttempts(ctx context.Context, scope Scope, wid string) ([]Attempt, error) {
	out := []Attempt{}
	e := s.read(ctx, scope, func(tx pgx.Tx) error {
		var w Work
		if e := body(ctx, tx, "work_intents", scope.OrganizationID, wid, &w); e != nil {
			return e
		}
		if e := businessAccess(ctx, tx, scope, w.BusinessID); e != nil {
			return e
		}
		rows, e := tx.Query(ctx, `SELECT body FROM business.attempts WHERE organization_id=$1 AND work_id=$2 ORDER BY ordinal`, scope.OrganizationID, wid)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var raw []byte
			var a Attempt
			if e = rows.Scan(&raw); e != nil {
				return e
			}
			if e = json.Unmarshal(raw, &a); e != nil {
				return e
			}
			out = append(out, a)
		}
		return rows.Err()
	})
	return out, e
}
func (s *Store) GetResult(ctx context.Context, scope Scope, wid string) (Result, error) {
	var r Result
	e := s.read(ctx, scope, func(tx pgx.Tx) error {
		var w Work
		if e := body(ctx, tx, "work_intents", scope.OrganizationID, wid, &w); e != nil {
			return e
		}
		if e := businessAccess(ctx, tx, scope, w.BusinessID); e != nil {
			return e
		}
		if w.ResultID == "" {
			return ErrNotFound
		}
		if e := body(ctx, tx, "results", scope.OrganizationID, w.ResultID, &r); e != nil {
			return e
		}
		if r.Eligible {
			if e := currentWorkAuthority(ctx, tx, scope.OrganizationID, w); e != nil {
				r.Eligible = false
				r.IneligibleReason = "authority_changed"
			}
		}
		return nil
	})
	return r, e
}
func latestAttempt(ctx context.Context, tx pgx.Tx, org, wid string) (Attempt, error) {
	var a Attempt
	var raw []byte
	e := tx.QueryRow(ctx, `SELECT body FROM business.attempts WHERE organization_id=$1 AND work_id=$2 ORDER BY ordinal DESC LIMIT 1`, org, wid).Scan(&raw)
	if errors.Is(e, pgx.ErrNoRows) {
		return a, ErrNotFound
	}
	if e != nil {
		return a, e
	}
	e = json.Unmarshal(raw, &a)
	return a, e
}
func executor(ctx context.Context, tx pgx.Tx, scope Scope, w Work) error {
	if scope.Actor.Kind == "person" {
		_, e := member(ctx, tx, scope, true)
		return e
	}
	if scope.Actor.ID != w.EmploymentID {
		return ErrDenied
	}
	return actorCurrent(ctx, tx, scope, false)
}
func currentWorkAuthority(ctx context.Context, tx pgx.Tx, org string, w Work) error {
	if w.CancelRequested {
		return ErrInactive
	}
	var b Business
	if e := body(ctx, tx, "businesses", org, w.BusinessID, &b); e != nil {
		return e
	}
	if b.Status != "active" || b.Revision != w.BusinessRevision {
		return ErrInactive
	}
	var emp Employment
	if e := body(ctx, tx, "employments", org, w.EmploymentID, &emp); e != nil {
		return e
	}
	if emp.Status != "active" || emp.Revision != w.EmploymentRevision || emp.BusinessID != w.BusinessID {
		return ErrInactive
	}
	var m Mandate
	if e := body(ctx, tx, "mandates", org, w.MandateID, &m); e != nil {
		return e
	}
	now, e := databaseNow(ctx, tx)
	if e != nil {
		return e
	}
	if m.Revision != w.MandateRevision || m.Status != "active" || !m.ExpiresAt.After(now) || m.BusinessID != w.BusinessID || m.EmploymentID != w.EmploymentID {
		return ErrInactive
	}
	var issuer bool
	if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM business.memberships WHERE organization_id=$1 AND id=$2 AND revision=$3 AND status='active' AND role='owner')`, org, m.IssuerID, m.IssuerRevision).Scan(&issuer); e != nil {
		return e
	}
	if !issuer {
		return ErrInactive
	}
	if w.Actor.Kind == "person" {
		_, e = member(ctx, tx, Scope{org, w.Actor}, true)
		return e
	}
	if w.Actor.ID != w.EmploymentID || (b.AuthorityPreset != "take_initiative" && b.AuthorityPreset != "full_autonomy") {
		return ErrInactive
	}
	return nil
}
func businessLiability(ctx context.Context, tx pgx.Tx, org, bid string) error {
	var pending bool
	e := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM business.attempts a JOIN business.work_intents w ON w.organization_id=a.organization_id AND w.id=a.work_id WHERE a.organization_id=$1 AND w.business_id=$2 AND (a.body->>'state' IN ('reconciling','completed_unsettled') OR (a.body->>'state'='prepared' AND (a.body->>'leaseExpiresAt')::timestamptz<=clock_timestamp())))`, org, bid).Scan(&pending)
	if e != nil {
		return e
	}
	if pending {
		return ErrReconciliation
	}
	return nil
}
func (s *Store) ClaimAttempt(ctx context.Context, scope Scope, in ClaimAttemptArgs) (Attempt, error) {
	var a Attempt
	if !validText(in.WorkerID, 200) || !validText(in.IdempotencyKey, 200) || in.LeaseSeconds < 1 || in.LeaseSeconds > 300 {
		return a, ErrInvalid
	}
	tx, e := scopeTx(ctx, s.pool, scope)
	if e != nil {
		return a, e
	}
	defer tx.Rollback(ctx)
	if e = lockOrg(ctx, tx, scope); e != nil {
		return a, e
	}
	var w Work
	if e = body(ctx, tx, "work_intents", scope.OrganizationID, in.WorkID, &w); e != nil {
		return a, e
	}
	if e = executor(ctx, tx, scope, w); e != nil {
		return a, e
	}
	now, e := databaseNow(ctx, tx)
	if e != nil {
		return a, e
	}
	a, e = latestAttempt(ctx, tx, scope.OrganizationID, w.ID)
	exists := e == nil
	if e != nil && !errors.Is(e, ErrNotFound) {
		return a, e
	}
	if exists && a.LeaseExpiresAt.After(now) {
		if a.WorkerID == in.WorkerID && a.ClaimKey == in.IdempotencyKey {
			return a, tx.Commit(ctx)
		}
		return a, ErrConcurrency
	}
	reconcile := exists && (a.State == "prepared" || a.State == "reconciling" || a.State == "completed_unsettled")
	if !reconcile {
		if w.Status != "admitted" {
			return a, ErrInactive
		}
		if e = currentWorkAuthority(ctx, tx, scope.OrganizationID, w); e != nil {
			return a, e
		}
	}
	create := !exists || a.State == "not_accepted"
	if create {
		ordinal := 1
		if exists {
			ordinal = a.Ordinal + 1
		}
		if ordinal > w.MaxAttempts {
			return a, ErrInactive
		}
		a = Attempt{ID: id("attempt"), WorkID: w.ID, Ordinal: ordinal, Generation: 1, State: "claimed", Mode: "execute", CostState: "not_issued"}
	} else {
		if a.State != "claimed" && !reconcile {
			return a, ErrInactive
		}
		a.Generation++
		if reconcile {
			a.Mode = "reconcile"
			if a.State != "completed_unsettled" {
				a.State = "reconciling"
			}
		}
	}
	a.WorkerID = in.WorkerID
	a.ClaimKey = in.IdempotencyKey
	a.LeaseExpiresAt = now.Add(time.Duration(in.LeaseSeconds) * time.Second)
	if create {
		_, e = tx.Exec(ctx, `INSERT INTO business.attempts VALUES($1,$2,$3,$4,$5)`, scope.OrganizationID, a.ID, a.WorkID, a.Ordinal, jsonBytes(a))
	} else {
		e = saveBody(ctx, tx, "attempts", scope.OrganizationID, a.ID, a)
	}
	if e != nil {
		return a, e
	}
	if e = event(ctx, tx, scope.OrganizationID, "attempt_claimed", a.ID, a); e != nil {
		return a, e
	}
	return a, tx.Commit(ctx)
}
func leaseCurrent(ctx context.Context, tx pgx.Tx, scope Scope, l AttemptLease) (Attempt, Work, error) {
	var a Attempt
	var w Work
	if e := body(ctx, tx, "attempts", scope.OrganizationID, l.AttemptID, &a); e != nil {
		return a, w, e
	}
	if e := body(ctx, tx, "work_intents", scope.OrganizationID, a.WorkID, &w); e != nil {
		return a, w, e
	}
	if e := executor(ctx, tx, scope, w); e != nil {
		return a, w, e
	}
	now, e := databaseNow(ctx, tx)
	if e != nil {
		return a, w, e
	}
	if a.WorkerID != l.WorkerID || a.Generation != l.Generation || !a.LeaseExpiresAt.After(now) {
		return a, w, ErrLease
	}
	return a, w, nil
}
func (s *Store) RenewAttempt(ctx context.Context, scope Scope, l AttemptLease, seconds int) (Attempt, error) {
	var a Attempt
	if seconds < 1 || seconds > 300 {
		return a, ErrInvalid
	}
	tx, e := scopeTx(ctx, s.pool, scope)
	if e != nil {
		return a, e
	}
	defer tx.Rollback(ctx)
	if e = lockOrg(ctx, tx, scope); e != nil {
		return a, e
	}
	a, w, e := leaseCurrent(ctx, tx, scope, l)
	if e != nil {
		return a, e
	}
	if a.State != "claimed" && a.State != "prepared" && a.State != "reconciling" && a.State != "completed_unsettled" {
		return a, ErrInactive
	}
	if a.Mode != "reconcile" {
		if e = currentWorkAuthority(ctx, tx, scope.OrganizationID, w); e != nil {
			return a, e
		}
	}
	now, e := databaseNow(ctx, tx)
	if e != nil {
		return a, e
	}
	a.LeaseExpiresAt = now.Add(time.Duration(seconds) * time.Second)
	if e = saveBody(ctx, tx, "attempts", scope.OrganizationID, a.ID, a); e != nil {
		return a, e
	}
	return a, tx.Commit(ctx)
}
func (s *Store) PrepareOperation(ctx context.Context, scope Scope, in PrepareOperationArgs) (Attempt, error) {
	var a Attempt
	op := in.Operation
	if !validText(op.ID, 200) || !validText(op.RequestDigest, 200) || !validText(op.AdapterID, 200) || !validText(op.RouteRevision, 200) || !validText(op.PriceRevision, 200) || !money(op.MaximumCostMicros) {
		return a, ErrInvalid
	}
	tx, e := scopeTx(ctx, s.pool, scope)
	if e != nil {
		return a, e
	}
	defer tx.Rollback(ctx)
	if e = lockOrg(ctx, tx, scope); e != nil {
		return a, e
	}
	a, w, e := leaseCurrent(ctx, tx, scope, in.Lease)
	if e != nil {
		return a, e
	}
	if e = currentWorkAuthority(ctx, tx, scope.OrganizationID, w); e != nil {
		return a, e
	}
	if a.Operation != nil {
		if *a.Operation != op {
			return a, ErrConflict
		}
		if a.State != "prepared" || a.Mode != "execute" {
			return a, ErrReconciliation
		}
		return a, tx.Commit(ctx)
	}
	if a.State != "claimed" || a.Mode != "execute" || w.Status != "admitted" {
		return a, ErrInactive
	}
	if op.MaximumCostMicros > w.HeldMicros {
		return a, ErrBudget
	}
	if e = businessLiability(ctx, tx, scope.OrganizationID, w.BusinessID); e != nil {
		return a, e
	}
	fund, e := budget(ctx, tx, scope.OrganizationID, w.BusinessID)
	if e != nil {
		return a, e
	}
	if min(fund.FundedMicros, fund.CapMicros)-fund.ReservedMicros-fund.SettledMicros < 0 {
		return a, ErrBudget
	}
	a.Operation = &op
	a.State = "prepared"
	a.CostState = "unknown"
	if _, e = tx.Exec(ctx, `INSERT INTO business.settlements(organization_id,attempt_id,operation_id,actual_micros,evidence_ref) VALUES($1,$2,$3,NULL,'')`, scope.OrganizationID, a.ID, op.ID); e != nil {
		return a, e
	}
	if e = saveBody(ctx, tx, "attempts", scope.OrganizationID, a.ID, a); e != nil {
		return a, e
	}
	if e = event(ctx, tx, scope.OrganizationID, "operation_may_issue", a.ID, op); e != nil {
		return a, e
	}
	return a, tx.Commit(ctx)
}
func (s *Store) CompleteAttempt(ctx context.Context, scope Scope, in CompleteAttemptArgs) (AttemptCompletion, error) {
	return s.finishAttempt(ctx, scope, in, false)
}
func (s *Store) ReconcileAttempt(ctx context.Context, scope Scope, in ReconcileAttemptArgs) (AttemptCompletion, error) {
	return s.finishAttempt(ctx, scope, CompleteAttemptArgs(in), true)
}
func contentDigest(v string) string {
	h := sha256.Sum256([]byte(v))
	return "sha256:" + hex.EncodeToString(h[:])
}
func (s *Store) finishAttempt(ctx context.Context, scope Scope, in CompleteAttemptArgs, reconcile bool) (AttemptCompletion, error) {
	var out AttemptCompletion
	if in.Outcome != "succeeded" && in.Outcome != "failed" && in.Outcome != "not_accepted" {
		return out, ErrInvalid
	}
	if !validText(in.OutcomeEvidenceRef, 1000) {
		return out, ErrInvalid
	}
	if in.Cost.ActualMicros != nil && (!money(*in.Cost.ActualMicros) || !validText(in.Cost.EvidenceRef, 1000)) {
		return out, ErrInvalid
	}
	if in.Outcome == "succeeded" {
		if !validText(in.Content, 256000) || in.ContentDigest != contentDigest(in.Content) {
			return out, ErrInvalid
		}
	} else if in.Content != "" || in.ContentDigest != "" {
		return out, ErrInvalid
	}
	if in.Outcome == "not_accepted" && in.Cost.ActualMicros == nil {
		return out, ErrReconciliation
	}
	tx, e := scopeTx(ctx, s.pool, scope)
	if e != nil {
		return out, e
	}
	defer tx.Rollback(ctx)
	if e = lockOrg(ctx, tx, scope); e != nil {
		return out, e
	}
	a, w, e := leaseCurrent(ctx, tx, scope, in.Lease)
	if e != nil {
		return out, e
	}
	if a.Operation == nil || a.Operation.ID != in.OperationID {
		return out, ErrConflict
	}
	if reconcile {
		if a.Mode != "reconcile" {
			return out, ErrReconciliation
		}
	} else if a.Mode != "execute" {
		return out, ErrReconciliation
	}
	// A same-generation acknowledgement retry reads the original completion and
	// cannot create a second result or charge. Changed completion data conflicts.
	completionKey := "attempt_completion_" + a.ID
	if a.Outcome != "" && a.CostState == "known" {
		prior, found, e := replay[AttemptCompletion](ctx, tx, scope, completionKey, "attempt_completion", in)
		if e != nil {
			return out, e
		}
		if !found {
			return out, ErrConflict
		}
		return prior, tx.Commit(ctx)
	}
	if a.State != "prepared" && a.State != "reconciling" && a.State != "completed_unsettled" {
		return out, ErrInactive
	}
	if a.Outcome != "" && a.Outcome != in.Outcome {
		return out, ErrConflict
	}
	eligible := currentWorkAuthority(ctx, tx, scope.OrganizationID, w) == nil
	var result *Result
	if a.ResultID != "" {
		var r Result
		if e = body(ctx, tx, "results", scope.OrganizationID, a.ResultID, &r); e != nil {
			return out, e
		}
		if r.Digest != in.ContentDigest || r.Content != in.Content {
			return out, ErrConflict
		}
		if !eligible {
			r.Eligible = false
			r.IneligibleReason = "authority_changed"
		}
		result = &r
	} else if in.Outcome == "succeeded" {
		now, e := databaseNow(ctx, tx)
		if e != nil {
			return out, e
		}
		r := Result{ID: id("result"), WorkID: w.ID, AttemptID: a.ID, OperationID: in.OperationID, Generation: a.Generation, Content: in.Content, Digest: in.ContentDigest, ContentType: "text/markdown", Eligible: eligible, CreatedAt: now}
		if !eligible {
			r.IneligibleReason = "authority_changed"
		}
		if _, e = tx.Exec(ctx, `INSERT INTO business.results VALUES($1,$2,$3,$4,$5)`, scope.OrganizationID, r.ID, w.ID, a.ID, jsonBytes(r)); e != nil {
			return out, e
		}
		a.ResultID = r.ID
		w.ResultID = r.ID
		result = &r
	}
	a.Outcome = in.Outcome
	a.OutcomeEvidenceRef = in.OutcomeEvidenceRef
	retry := in.Outcome == "not_accepted" && eligible && a.Ordinal < w.MaxAttempts
	terminal := !retry
	if in.Cost.ActualMicros == nil {
		a.CostState = "unknown"
		a.State = "completed_unsettled"
		a.Mode = "reconcile"
		w.Status = "reconciling"
	} else {
		if e = settleAttempt(ctx, tx, scope.OrganizationID, &w, a, in.Cost, terminal); e != nil {
			return out, e
		}
		a.CostState = "known"
		a.State = in.Outcome
		if retry {
			a.LeaseExpiresAt = time.Time{}
		}
		if retry && w.HeldMicros > 0 {
			w.Status = "admitted"
		} else {
			if !terminal {
				if e = releaseWorkReservation(ctx, tx, scope.OrganizationID, &w); e != nil {
					return out, e
				}
			}
			if !eligible {
				w.Status = "cancelled"
			} else if in.Outcome == "succeeded" {
				w.Status = "completed"
			} else {
				w.Status = "failed"
			}
		}
	}
	if w.CancelRequested && w.Status != "reconciling" {
		w.Status = "cancelled"
	}
	if e = saveBody(ctx, tx, "attempts", scope.OrganizationID, a.ID, a); e != nil {
		return out, e
	}
	if e = saveBody(ctx, tx, "work_intents", scope.OrganizationID, w.ID, w); e != nil {
		return out, e
	}
	out = AttemptCompletion{a, w, result}
	if a.State != "completed_unsettled" {
		if e = receipt(ctx, tx, scope, completionKey, "attempt_completion", in, out); e != nil {
			return out, e
		}
	} else {
		if e = event(ctx, tx, scope.OrganizationID, "attempt_cost_unknown", a.ID, out); e != nil {
			return out, e
		}
	}
	return out, tx.Commit(ctx)
}
func settleAttempt(ctx context.Context, tx pgx.Tx, org string, w *Work, a Attempt, cost CostEvidence, terminal bool) error {
	var actual *int64
	var evidence string
	if e := tx.QueryRow(ctx, `SELECT actual_micros,evidence_ref FROM business.settlements WHERE organization_id=$1 AND attempt_id=$2`, org, a.ID).Scan(&actual, &evidence); e != nil {
		return e
	}
	if actual != nil {
		return ErrConflict
	}
	value := *cost.ActualMicros
	b, e := budget(ctx, tx, org, w.BusinessID)
	if e != nil {
		return e
	}
	if value > MaxMoneyMicros-b.SettledMicros {
		return ErrInvalid
	}
	release := min(w.HeldMicros, value)
	if terminal {
		release = w.HeldMicros
	}
	w.HeldMicros -= release
	w.SettledMicros += value
	if _, e = tx.Exec(ctx, `UPDATE business.budgets SET reserved_micros=reserved_micros-$3,settled_micros=settled_micros+$4,revision=revision+1 WHERE organization_id=$1 AND business_id=$2`, org, w.BusinessID, release, value); e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, `UPDATE business.settlements SET actual_micros=$3,evidence_ref=$4 WHERE organization_id=$1 AND attempt_id=$2`, org, a.ID, value, cost.EvidenceRef); e != nil {
		return e
	}
	return event(ctx, tx, org, "attempt_settled", a.ID, struct {
		ActualMicros   int64  `json:"actualMicros"`
		EvidenceRef    string `json:"evidenceRef"`
		ReleasedMicros int64  `json:"releasedMicros"`
	}{value, cost.EvidenceRef, release})
}
func releaseWorkReservation(ctx context.Context, tx pgx.Tx, org string, w *Work) error {
	if w.HeldMicros == 0 {
		return nil
	}
	_, e := tx.Exec(ctx, `UPDATE business.budgets SET reserved_micros=reserved_micros-$3,revision=revision+1 WHERE organization_id=$1 AND business_id=$2`, org, w.BusinessID, w.HeldMicros)
	if e == nil {
		w.HeldMicros = 0
	}
	return e
}
func cancelAttemptWork(ctx context.Context, tx pgx.Tx, org string, w *Work, reason string) error {
	a, e := latestAttempt(ctx, tx, org, w.ID)
	if e != nil && !errors.Is(e, ErrNotFound) {
		return e
	}
	exists := e == nil
	issued := exists && a.Operation != nil && a.CostState != "known"
	w.CancelRequested = true
	w.Status = "cancelled"
	released := w.HeldMicros
	if issued {
		w.Status = "reconciling"
		a.State = "reconciling"
		a.Mode = "reconcile"
		released = 0
	} else {
		if e = releaseWorkReservation(ctx, tx, org, w); e != nil {
			return e
		}
		if exists {
			a.State = "cancelled"
		}
	}
	if exists {
		a.Generation++
		a.WorkerID = ""
		a.ClaimKey = ""
		a.LeaseExpiresAt = time.Time{}
		if e = saveBody(ctx, tx, "attempts", org, a.ID, a); e != nil {
			return e
		}
	}
	if e = saveBody(ctx, tx, "work_intents", org, w.ID, w); e != nil {
		return e
	}
	return event(ctx, tx, org, "work_cancelled", w.ID, struct {
		Reason                 string `json:"reason"`
		ReleasedMicros         int64  `json:"releasedMicros"`
		ReconciliationRequired bool   `json:"reconciliationRequired"`
	}{reason, released, issued})
}
