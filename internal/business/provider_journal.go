package business

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ProviderAdmin is deliberately separate from Store and tenant HTTP. Each grant
// is an independent allocation of host credit, not a shared provider balance.
// Only the separate migration administrator can construct this issuer.
type ProviderAdmin struct{ pool *pgxpool.Pool }
type ProviderGrantArgs struct {
	OrganizationID, ID, BusinessID                                    string
	AccountID, CredentialRef, AdapterID, RouteRevision, PriceRevision string
	Retention                                                         string
	AllowanceMicros, MaxOperationMicros                               int64
	MaxOperations                                                     int
	ExpiresAt                                                         time.Time
	// Snapshots are retained provenance, not executable authority or secrets.
	RouteSnapshot, PriceSnapshot json.RawMessage
}
type ProviderGrant struct {
	ProviderGrantArgs
	Status    string
	CreatedAt time.Time
}
type ProviderGrantBalance struct {
	Grant                                ProviderGrant
	HeldMicros, SettledMicros            int64
	IssuedOperations, ReservedOperations int
}
type ProviderWorkArgs struct {
	Work    WorkArgs
	GrantID string
	Request []byte
	// Immutable exact source identities/versions. Revalidation remains the host
	// adapter's responsibility; this package does not read legacy source stores.
	SourceBindings json.RawMessage
}
type ProviderRequest struct {
	OrganizationID, WorkID, GrantID, RequestDigest string
	Request                                        []byte
	SourceBindings                                 json.RawMessage
}
type ProviderOperation struct {
	OrganizationID, WorkID, AttemptID, GrantID, AccountID string
	Operation                                             Operation
	Generation                                            int64
	CreatedAt                                             time.Time
}

// A receipt capability can append bounded facts only. It cannot claim a lease,
// execute, publish a result, settle money, or read private request bytes.
type ProviderReceiptCapability struct {
	OrganizationID, OperationID string
	Token                       string `json:"-"`
}
type PrepareProviderOperationArgs struct {
	Lease        AttemptLease
	Operation    Operation
	ReceiptToken string `json:"-"`
}
type PreparedProviderOperation struct {
	Attempt    Attempt
	Journal    ProviderOperation
	Capability ProviderReceiptCapability `json:"-"`
}
type ProviderFactArgs struct {
	IdempotencyKey, Kind, ResponseID, ProviderStatus string
	// Terminal facts may advance unknown cost to known, but cannot replace an
	// outcome, content, response identity, or previously known cost.
	Outcome, Content, ContentDigest string
	ActualMicros                    *int64
	Evidence                        json.RawMessage
}
type ProviderFact struct {
	Sequence                        int
	ID, OrganizationID, OperationID string
	ProviderFactArgs
	RecordedAt time.Time
}

func (f ProviderFact) EvidenceRef() string { return "provider-fact:" + f.ID }

type ProviderJournalView struct {
	Journal    ProviderOperation
	ResponseID string
	Facts      []ProviderFact
}

func NewProviderAdmin(ctx context.Context, admin *pgxpool.Pool) (*ProviderAdmin, error) {
	if admin == nil {
		return nil, ErrInvalid
	}
	var permitted bool
	// Require the actual schema owner as well as bypass privilege. A runtime role
	// granted an unrelated administrative bit is not an intended grant issuer.
	e := admin.QueryRow(ctx, `SELECT (r.rolsuper OR r.rolbypassrls) AND n.nspowner=r.oid FROM pg_roles r JOIN pg_namespace n ON n.nspname='business' WHERE r.rolname=current_user`).Scan(&permitted)
	if e != nil {
		return nil, e
	}
	if !permitted {
		return nil, ErrDenied
	}
	return &ProviderAdmin{admin}, nil
}
func validSnapshot(raw json.RawMessage, max int) bool {
	return len(raw) > 0 && len(raw) <= max && json.Valid(raw)
}
func (a *ProviderAdmin) IssueGrant(ctx context.Context, in ProviderGrantArgs) (ProviderGrant, error) {
	var g ProviderGrant
	for _, v := range []string{in.OrganizationID, in.ID, in.BusinessID, in.AccountID, in.CredentialRef, in.AdapterID, in.RouteRevision, in.PriceRevision} {
		if !validText(v, 200) {
			return g, ErrInvalid
		}
	}
	if !money(in.AllowanceMicros) || !money(in.MaxOperationMicros) || in.MaxOperationMicros > in.AllowanceMicros || in.MaxOperations < 1 || in.MaxOperations > 1000 || (in.Retention != "store_false" && in.Retention != "store_true") || !validSnapshot(in.RouteSnapshot, 16000) || !validSnapshot(in.PriceSnapshot, 16000) {
		return g, ErrInvalid
	}
	scope := Scope{in.OrganizationID, Actor{"person", "host-provider-admin"}}
	tx, e := scopeTx(ctx, a.pool, scope)
	if e != nil {
		return g, e
	}
	defer tx.Rollback(ctx)
	if e = lockOrg(ctx, tx, scope); e != nil {
		return g, e
	}
	var prior ProviderGrant
	e = body(ctx, tx, "provider_grants", scope.OrganizationID, in.ID, &prior)
	if e == nil {
		if providerPolicyDigest(prior.ProviderGrantArgs) != providerPolicyDigest(in) {
			return g, ErrConflict
		}
		return prior, tx.Commit(ctx)
	}
	if !errors.Is(e, ErrNotFound) {
		return g, e
	}
	var b Business
	if e = body(ctx, tx, "businesses", scope.OrganizationID, in.BusinessID, &b); e != nil {
		return g, e
	}
	now, e := databaseNow(ctx, tx)
	if e != nil {
		return g, e
	}
	if !in.ExpiresAt.After(now) || in.ExpiresAt.After(now.Add(366*24*time.Hour)) {
		return g, ErrInvalid
	}
	g = ProviderGrant{in, "active", now}
	_, e = tx.Exec(ctx, `INSERT INTO business.provider_grants VALUES($1,$2,$3,'active',$4)`, in.OrganizationID, in.ID, in.BusinessID, jsonBytes(g))
	if e != nil {
		return g, e
	}
	if e = event(ctx, tx, in.OrganizationID, "host_provider_grant_issued", in.ID, g); e != nil {
		return g, e
	}
	return g, tx.Commit(ctx)
}
func (a *ProviderAdmin) RevokeGrant(ctx context.Context, org, gid string) (ProviderGrant, error) {
	var g ProviderGrant
	scope := Scope{org, Actor{"person", "host-provider-admin"}}
	tx, e := scopeTx(ctx, a.pool, scope)
	if e != nil {
		return g, e
	}
	defer tx.Rollback(ctx)
	if e = lockOrg(ctx, tx, scope); e != nil {
		return g, e
	}
	if e = body(ctx, tx, "provider_grants", org, gid, &g); e != nil {
		return g, e
	}
	if g.Status == "revoked" {
		return g, tx.Commit(ctx)
	}
	g.Status = "revoked"
	if _, e = tx.Exec(ctx, `UPDATE business.provider_grants SET status='revoked',body=$3 WHERE organization_id=$1 AND id=$2`, org, gid, jsonBytes(g)); e != nil {
		return g, e
	}
	rows, e := tx.Query(ctx, `SELECT work_id FROM business.provider_reservations WHERE organization_id=$1 AND grant_id=$2`, org, gid)
	if e != nil {
		return g, e
	}
	var ids []string
	for rows.Next() {
		var v string
		if e = rows.Scan(&v); e != nil {
			rows.Close()
			return g, e
		}
		ids = append(ids, v)
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		return g, e
	}
	for _, wid := range ids {
		var w Work
		if e = body(ctx, tx, "work_intents", org, wid, &w); e != nil {
			return g, e
		}
		if w.Status == "admitted" || w.Status == "reconciling" {
			if e = cancelAttemptWork(ctx, tx, org, &w, "host_provider_grant_revoked"); e != nil {
				return g, e
			}
		}
	}
	if e = event(ctx, tx, org, "host_provider_grant_revoked", gid, g); e != nil {
		return g, e
	}
	return g, tx.Commit(ctx)
}
func providerGrantBalance(ctx context.Context, tx pgx.Tx, org, gid string) (ProviderGrantBalance, error) {
	var v ProviderGrantBalance
	if e := body(ctx, tx, "provider_grants", org, gid, &v.Grant); e != nil {
		return v, e
	}
	e := tx.QueryRow(ctx, `SELECT coalesce(sum(held_micros),0)::bigint,coalesce(sum(settled_micros),0)::bigint,count(*) FILTER(WHERE slot_reserved)::integer FROM business.provider_reservations WHERE organization_id=$1 AND grant_id=$2`, org, gid).Scan(&v.HeldMicros, &v.SettledMicros, &v.ReservedOperations)
	if e != nil {
		return v, e
	}
	e = tx.QueryRow(ctx, `SELECT count(*) FROM business.provider_journal WHERE organization_id=$1 AND grant_id=$2`, org, gid).Scan(&v.IssuedOperations)
	return v, e
}
func providerGrantCurrent(ctx context.Context, tx pgx.Tx, org, gid, bid string) (ProviderGrantBalance, error) {
	g, e := providerGrantBalance(ctx, tx, org, gid)
	if e != nil {
		return g, e
	}
	if g.Grant.BusinessID != bid {
		return g, ErrDenied
	}
	now, e := databaseNow(ctx, tx)
	if e != nil {
		return g, e
	}
	if g.Grant.Status != "active" || !g.Grant.ExpiresAt.After(now) {
		return g, ErrInactive
	}
	if g.HeldMicros+g.SettledMicros > g.Grant.AllowanceMicros {
		return g, ErrBudget
	}
	var unknown bool
	e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM business.provider_journal j JOIN business.attempts a ON a.organization_id=j.organization_id AND a.id=j.attempt_id WHERE j.organization_id=$1 AND j.grant_id=$2 AND (a.body->>'state' IN ('reconciling','completed_unsettled') OR (a.body->>'state'='prepared' AND (a.body->>'leaseExpiresAt')::timestamptz<=clock_timestamp())))`, org, gid).Scan(&unknown)
	if e != nil {
		return g, e
	}
	if unknown {
		return g, ErrReconciliation
	}
	return g, nil
}
func (s *Store) GetProviderGrantBalance(ctx context.Context, scope Scope, gid string) (ProviderGrantBalance, error) {
	var v ProviderGrantBalance
	e := s.read(ctx, scope, func(tx pgx.Tx) error {
		var e error
		v, e = providerGrantBalance(ctx, tx, scope.OrganizationID, gid)
		if e != nil {
			return e
		}
		return businessAccess(ctx, tx, scope, v.Grant.BusinessID)
	})
	return v, e
}

// AdmitProviderWork is server-only admission for a frozen provider request. No
// HTTP request may supply this structure without an authoritative host adapter.
func (s *Store) AdmitProviderWork(ctx context.Context, scope Scope, in ProviderWorkArgs) (Work, error) {
	if len(in.Request) == 0 || len(in.Request) > 256000 || !validSnapshot(in.SourceBindings, 16000) {
		return Work{}, ErrInvalid
	}
	return command(ctx, s, scope, in.Work.IdempotencyKey, "admit_provider_work", in, false, func(tx pgx.Tx) (Work, error) {
		g, e := providerGrantCurrent(ctx, tx, scope.OrganizationID, in.GrantID, in.Work.BusinessID)
		if e != nil {
			return Work{}, e
		}
		if !money(in.Work.ReservationMicros) || in.Work.ReservationMicros > g.Grant.MaxOperationMicros || in.Work.ReservationMicros > g.Grant.AllowanceMicros-g.HeldMicros-g.SettledMicros || g.IssuedOperations+g.ReservedOperations >= g.Grant.MaxOperations {
			return Work{}, ErrBudget
		}
		w, e := admitWorkTx(ctx, tx, scope, in.Work)
		if e != nil {
			return w, e
		}
		r := ProviderRequest{scope.OrganizationID, w.ID, in.GrantID, contentDigest(string(in.Request)), in.Request, in.SourceBindings}
		if _, e = tx.Exec(ctx, `INSERT INTO business.provider_requests VALUES($1,$2,$3,$4)`, scope.OrganizationID, w.ID, in.GrantID, jsonBytes(r)); e != nil {
			return w, e
		}
		_, e = tx.Exec(ctx, `INSERT INTO business.provider_reservations(organization_id,work_id,grant_id,held_micros) VALUES($1,$2,$3,$4)`, scope.OrganizationID, w.ID, in.GrantID, w.HeldMicros)
		return w, e
	})
}
func providerRequest(ctx context.Context, tx pgx.Tx, org, wid string) (ProviderRequest, error) {
	var r ProviderRequest
	var raw []byte
	e := tx.QueryRow(ctx, `SELECT body FROM business.provider_requests WHERE organization_id=$1 AND work_id=$2`, org, wid).Scan(&raw)
	if errors.Is(e, pgx.ErrNoRows) {
		return r, ErrNotFound
	}
	if e != nil {
		return r, e
	}
	e = json.Unmarshal(raw, &r)
	return r, e
}
func (s *Store) GetProviderRequest(ctx context.Context, scope Scope, wid string) (ProviderRequest, error) {
	var r ProviderRequest
	e := s.read(ctx, scope, func(tx pgx.Tx) error {
		var w Work
		if e := body(ctx, tx, "work_intents", scope.OrganizationID, wid, &w); e != nil {
			return e
		}
		if e := executor(ctx, tx, scope, w); e != nil {
			return e
		}
		var e error
		r, e = providerRequest(ctx, tx, scope.OrganizationID, wid)
		return e
	})
	return r, e
}
func NewProviderReceiptToken() (string, error) {
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		return "", e
	}
	return hex.EncodeToString(b), nil
}
func validReceiptToken(v string) bool { b, e := hex.DecodeString(v); return e == nil && len(b) == 32 }
func putProviderCapability(ctx context.Context, tx pgx.Tx, org, op, token string, generation int64) error {
	_, e := tx.Exec(ctx, `INSERT INTO business.provider_receipt_capabilities VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, org, op, contentDigest(token), generation)
	return e
}
func (s *Store) PrepareProviderOperation(ctx context.Context, scope Scope, in PrepareProviderOperationArgs) (PreparedProviderOperation, error) {
	var out PreparedProviderOperation
	if !validReceiptToken(in.ReceiptToken) {
		return out, ErrInvalid
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
	r, e := providerRequest(ctx, tx, scope.OrganizationID, w.ID)
	if e != nil {
		return out, e
	}
	g, e := providerGrantCurrent(ctx, tx, scope.OrganizationID, r.GrantID, w.BusinessID)
	if e != nil {
		return out, e
	}
	op := in.Operation
	if op.AdapterID != g.Grant.AdapterID || op.RouteRevision != g.Grant.RouteRevision || op.PriceRevision != g.Grant.PriceRevision || op.RequestDigest != r.RequestDigest {
		return out, ErrConflict
	}
	if op.MaximumCostMicros > g.Grant.MaxOperationMicros {
		return out, ErrBudget
	}
	var j ProviderOperation
	e = body(ctx, tx, "provider_journal", scope.OrganizationID, op.ID, &j)
	exists := e == nil
	if e != nil && !errors.Is(e, ErrNotFound) {
		return out, e
	}
	if exists {
		if j.AttemptID != a.ID || j.WorkID != w.ID || j.Operation != op {
			return out, ErrConflict
		}
	} else {
		var slot bool
		if e = tx.QueryRow(ctx, `SELECT slot_reserved FROM business.provider_reservations WHERE organization_id=$1 AND work_id=$2`, scope.OrganizationID, w.ID).Scan(&slot); e != nil {
			return out, e
		}
		if !slot && g.IssuedOperations+g.ReservedOperations >= g.Grant.MaxOperations {
			return out, ErrBudget
		}
	}
	a, e = prepareOperationTx(ctx, tx, scope, PrepareOperationArgs{in.Lease, op}, true)
	if e != nil {
		return out, e
	}
	if !exists {
		now, e := databaseNow(ctx, tx)
		if e != nil {
			return out, e
		}
		j = ProviderOperation{scope.OrganizationID, w.ID, a.ID, r.GrantID, g.Grant.AccountID, op, a.Generation, now}
		if _, e = tx.Exec(ctx, `INSERT INTO business.provider_journal VALUES($1,$2,$3,$4,$5,$6,$7)`, scope.OrganizationID, op.ID, w.ID, a.ID, r.GrantID, g.Grant.AccountID, jsonBytes(j)); e != nil {
			return out, providerConflict(e)
		}
		if _, e = tx.Exec(ctx, `UPDATE business.provider_reservations SET slot_reserved=false WHERE organization_id=$1 AND work_id=$2`, scope.OrganizationID, w.ID); e != nil {
			return out, e
		}
	}
	if e = putProviderCapability(ctx, tx, scope.OrganizationID, op.ID, in.ReceiptToken, a.Generation); e != nil {
		return out, e
	}
	out = PreparedProviderOperation{a, j, ProviderReceiptCapability{scope.OrganizationID, op.ID, in.ReceiptToken}}
	return out, tx.Commit(ctx)
}

// CheckProviderAuthority is an additional action-time resource check. The host
// must separately revalidate selected source versions/permissions before egress.
func (s *Store) CheckProviderAuthority(ctx context.Context, scope Scope, lease AttemptLease, opid string) error {
	tx, e := scopeTx(ctx, s.pool, scope)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if e = lockOrg(ctx, tx, scope); e != nil {
		return e
	}
	a, w, e := leaseCurrent(ctx, tx, scope, lease)
	if e != nil {
		return e
	}
	if a.Mode != "execute" || a.State != "prepared" || a.Operation == nil || a.Operation.ID != opid {
		return ErrReconciliation
	}
	if e = currentWorkAuthority(ctx, tx, scope.OrganizationID, w); e != nil {
		return e
	}
	var j ProviderOperation
	if e = body(ctx, tx, "provider_journal", scope.OrganizationID, opid, &j); e != nil {
		return e
	}
	if j.AttemptID != a.ID || j.WorkID != w.ID || j.Operation != *a.Operation {
		return ErrConflict
	}
	if _, e = providerGrantCurrent(ctx, tx, scope.OrganizationID, j.GrantID, w.BusinessID); e != nil {
		return e
	}
	return tx.Commit(ctx)
}

// Reconciliation can obtain a new append-only capability under a current lease.
// Old capabilities remain evidence-only so an in-flight late ACK is not lost.
func (s *Store) AcquireProviderReceiptCapability(ctx context.Context, scope Scope, lease AttemptLease, token string) (ProviderReceiptCapability, error) {
	var c ProviderReceiptCapability
	if !validReceiptToken(token) {
		return c, ErrInvalid
	}
	tx, e := scopeTx(ctx, s.pool, scope)
	if e != nil {
		return c, e
	}
	defer tx.Rollback(ctx)
	if e = lockOrg(ctx, tx, scope); e != nil {
		return c, e
	}
	a, w, e := leaseCurrent(ctx, tx, scope, lease)
	if e != nil {
		return c, e
	}
	if a.Operation == nil {
		return c, ErrNotFound
	}
	var j ProviderOperation
	if e = body(ctx, tx, "provider_journal", scope.OrganizationID, a.Operation.ID, &j); e != nil {
		return c, e
	}
	if j.AttemptID != a.ID || j.WorkID != w.ID {
		return c, ErrConflict
	}
	if e = putProviderCapability(ctx, tx, scope.OrganizationID, j.Operation.ID, token, a.Generation); e != nil {
		return c, e
	}
	c = ProviderReceiptCapability{scope.OrganizationID, j.Operation.ID, token}
	return c, tx.Commit(ctx)
}
func providerConflict(e error) error {
	var p *pgconn.PgError
	if errors.As(e, &p) && p.Code == "23505" {
		return ErrConflict
	}
	return e
}

func providerFacts(ctx context.Context, tx pgx.Tx, org, op string) ([]ProviderFact, error) {
	out := []ProviderFact{}
	rows, e := tx.Query(ctx, `SELECT body FROM business.provider_facts WHERE organization_id=$1 AND operation_id=$2 ORDER BY sequence LIMIT 64`, org, op)
	if e != nil {
		return out, e
	}
	defer rows.Close()
	for rows.Next() {
		var b []byte
		var f ProviderFact
		if e = rows.Scan(&b); e != nil {
			return out, e
		}
		if e = json.Unmarshal(b, &f); e != nil {
			return out, e
		}
		out = append(out, f)
	}
	return out, rows.Err()
}
func validProviderFact(in ProviderFactArgs) bool {
	if !validText(in.IdempotencyKey, 200) || !validText(in.ProviderStatus, 100) || !validSnapshot(in.Evidence, 256000) || len(in.ResponseID) > 200 {
		return false
	}
	if in.Kind == "accepted" {
		return validText(in.ResponseID, 200) && in.Outcome == "" && in.Content == "" && in.ContentDigest == "" && in.ActualMicros == nil
	}
	if in.Kind != "terminal" || (in.Outcome != "succeeded" && in.Outcome != "failed" && in.Outcome != "not_accepted") {
		return false
	}
	if in.ActualMicros != nil && !money(*in.ActualMicros) {
		return false
	}
	if in.Outcome == "not_accepted" {
		return in.ResponseID == "" && in.ActualMicros != nil && in.Content == "" && in.ContentDigest == ""
	}
	if !validText(in.ResponseID, 200) {
		return false
	}
	if in.Outcome == "succeeded" {
		return validText(in.Content, 256000) && in.ContentDigest == contentDigest(in.Content)
	}
	return in.Content == "" && in.ContentDigest == ""
}
func (s *Store) AppendProviderFact(ctx context.Context, c ProviderReceiptCapability, in ProviderFactArgs) (ProviderFact, error) {
	var f ProviderFact
	if !validReceiptToken(c.Token) || !validProviderFact(in) {
		return f, ErrInvalid
	}
	// This synthetic value establishes RLS scope only. Authorization is the exact
	// operation-bound capability below, not a synthetic organization membership.
	scope := Scope{c.OrganizationID, Actor{"agent", "provider-receipt"}}
	tx, e := scopeTx(ctx, s.pool, scope)
	if e != nil {
		return f, e
	}
	defer tx.Rollback(ctx)
	if e = lockOrg(ctx, tx, scope); e != nil {
		return f, e
	}
	var valid bool
	if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM business.provider_receipt_capabilities WHERE organization_id=$1 AND operation_id=$2 AND token_digest=$3)`, c.OrganizationID, c.OperationID, contentDigest(c.Token)).Scan(&valid); e != nil {
		return f, e
	}
	if !valid {
		return f, ErrDenied
	}
	var j ProviderOperation
	if e = body(ctx, tx, "provider_journal", c.OrganizationID, c.OperationID, &j); e != nil {
		return f, e
	}
	digest := contentDigest(string(jsonBytes(in)))
	var priorDigest string
	var raw []byte
	e = tx.QueryRow(ctx, `SELECT digest,body FROM business.provider_facts WHERE organization_id=$1 AND operation_id=$2 AND idempotency_key=$3`, c.OrganizationID, c.OperationID, in.IdempotencyKey).Scan(&priorDigest, &raw)
	if e == nil {
		if priorDigest != digest {
			return f, ErrConflict
		}
		if e = json.Unmarshal(raw, &f); e != nil {
			return f, e
		}
		return f, tx.Commit(ctx)
	}
	if !errors.Is(e, pgx.ErrNoRows) {
		return f, e
	}
	facts, e := providerFacts(ctx, tx, c.OrganizationID, c.OperationID)
	if e != nil {
		return f, e
	}

	for _, prior := range facts {
		if prior.ResponseID != "" && prior.ResponseID != in.ResponseID {
			return f, ErrConflict
		}
		if prior.Kind == "terminal" {
			// A late acceptance for this exact response is still a useful fact,
			// even when a faster reconciler already recorded its terminal state.
			if in.Kind == "accepted" && prior.ResponseID != "" {
				continue
			}
			if in.Kind != "terminal" || prior.Outcome != in.Outcome || prior.ContentDigest != in.ContentDigest || prior.Content != in.Content || prior.ProviderStatus != in.ProviderStatus {
				return f, ErrConflict
			}
			if prior.ActualMicros != nil && (in.ActualMicros == nil || *prior.ActualMicros != *in.ActualMicros) {
				return f, ErrConflict
			}
		}
	}
	// Reserve terminal capacity independently of polling noise: one unknown and
	// one known terminal fact always have room. Same-key exact replay above is
	// unaffected. An adapter records acceptance once, not on every GET poll.
	accepted, unknown, known := 0, 0, 0
	for _, prior := range facts {
		if prior.Kind == "accepted" {
			accepted++
		} else if prior.ActualMicros == nil {
			unknown++
		} else {
			known++
		}
	}
	if (in.Kind == "accepted" && accepted >= 8) || (in.Kind == "terminal" && ((in.ActualMicros == nil && unknown >= 1) || (in.ActualMicros != nil && known >= 1))) {
		return f, fmt.Errorf("%w: provider fact class limit; reuse original receipt key", ErrInvalid)
	}
	if in.ResponseID != "" {
		// The global account/response uniqueness constraint also catches other
		// tenants, while the error exposes no identity or content from that tenant.
		var response string
		e = tx.QueryRow(ctx, `SELECT response_id FROM business.provider_response_bindings WHERE organization_id=$1 AND operation_id=$2`, c.OrganizationID, c.OperationID).Scan(&response)
		if e == nil {
			if response != in.ResponseID {
				return f, ErrConflict
			}
		} else if errors.Is(e, pgx.ErrNoRows) {
			if _, e = tx.Exec(ctx, `INSERT INTO business.provider_response_bindings VALUES($1,$2,$3,$4)`, c.OrganizationID, c.OperationID, j.AccountID, in.ResponseID); e != nil {
				return f, providerConflict(e)
			}
		} else {
			return f, e
		}
	}
	now, e := databaseNow(ctx, tx)
	if e != nil {
		return f, e
	}
	f = ProviderFact{len(facts) + 1, id("provider_fact"), c.OrganizationID, c.OperationID, in, now}
	if _, e = tx.Exec(ctx, `INSERT INTO business.provider_facts VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, c.OrganizationID, f.ID, c.OperationID, in.IdempotencyKey, digest, in.Kind, jsonBytes(f), f.Sequence); e != nil {
		return f, providerConflict(e)
	}
	return f, tx.Commit(ctx)
}
func (s *Store) GetProviderJournal(ctx context.Context, scope Scope, opid string) (ProviderJournalView, error) {
	out := ProviderJournalView{Facts: []ProviderFact{}}
	e := s.read(ctx, scope, func(tx pgx.Tx) error {
		if e := body(ctx, tx, "provider_journal", scope.OrganizationID, opid, &out.Journal); e != nil {
			return e
		}
		var w Work
		if e := body(ctx, tx, "work_intents", scope.OrganizationID, out.Journal.WorkID, &w); e != nil {
			return e
		}
		if e := executor(ctx, tx, scope, w); e != nil {
			return e
		}
		e := tx.QueryRow(ctx, `SELECT response_id FROM business.provider_response_bindings WHERE organization_id=$1 AND operation_id=$2`, scope.OrganizationID, opid).Scan(&out.ResponseID)
		if e != nil && !errors.Is(e, pgx.ErrNoRows) {
			return e
		}
		out.Facts, e = providerFacts(ctx, tx, scope.OrganizationID, opid)
		return e
	})
	return out, e
}
func validateProviderCompletion(ctx context.Context, tx pgx.Tx, org string, a Attempt, in CompleteAttemptArgs) error {
	var j ProviderOperation
	e := body(ctx, tx, "provider_journal", org, a.Operation.ID, &j)
	if errors.Is(e, ErrNotFound) {
		return nil
	}
	if e != nil {
		return e
	}
	if j.AttemptID != a.ID || j.Operation != *a.Operation {
		return ErrConflict
	}
	facts, e := providerFacts(ctx, tx, org, a.Operation.ID)
	if e != nil {
		return e
	}
	for _, f := range facts {
		if f.EvidenceRef() == in.OutcomeEvidenceRef && f.Kind == "terminal" {
			if f.Outcome != in.Outcome || f.Content != in.Content || f.ContentDigest != in.ContentDigest {
				return ErrConflict
			}
			if in.Cost.ActualMicros == nil {
				if f.ActualMicros != nil {
					return ErrConflict
				}
				return nil
			}
			if f.ActualMicros == nil || *f.ActualMicros != *in.Cost.ActualMicros || in.Cost.EvidenceRef != f.EvidenceRef() {
				return ErrConflict
			}
			return nil
		}
	}
	return ErrReconciliation
}

// Called only while holding the organization lock, in the SAME transaction as
// the canonical Work release/settlement. Unknown cost never invokes this hook.
func settleProviderReservation(ctx context.Context, tx pgx.Tx, org, wid string, release, actual int64, terminal bool) error {
	var held, settled int64
	e := tx.QueryRow(ctx, `SELECT held_micros,settled_micros FROM business.provider_reservations WHERE organization_id=$1 AND work_id=$2`, org, wid).Scan(&held, &settled)
	if errors.Is(e, pgx.ErrNoRows) {
		return nil
	}
	if e != nil {
		return e
	}
	if release < 0 || release > held || actual < 0 || actual > MaxMoneyMicros-settled {
		return ErrInvalid
	}
	_, e = tx.Exec(ctx, `UPDATE business.provider_reservations SET held_micros=held_micros-$3,settled_micros=settled_micros+$4,slot_reserved=CASE WHEN $5 THEN false ELSE slot_reserved END WHERE organization_id=$1 AND work_id=$2`, org, wid, release, actual, terminal)
	return e
}

// Resource revocation/expiry is part of current result authority as well as the
// pre-egress check. It never prevents reconciliation of already incurred cost.
func providerWorkResourceCurrent(ctx context.Context, tx pgx.Tx, org, wid string) error {
	var active bool
	e := tx.QueryRow(ctx, `SELECT g.status='active' AND (g.body->>'ExpiresAt')::timestamptz>clock_timestamp() FROM business.provider_requests r JOIN business.provider_grants g ON g.organization_id=r.organization_id AND g.id=r.grant_id WHERE r.organization_id=$1 AND r.work_id=$2`, org, wid).Scan(&active)
	if errors.Is(e, pgx.ErrNoRows) {
		return nil
	}
	if e != nil {
		return e
	}
	if !active {
		return ErrInactive
	}
	return nil
}

// JSONB normalizes nested snapshot key order and whitespace. Compare policy
// semantics for admin acknowledgement replay, retaining numeric precision.
// Provider request wire digests deliberately do NOT use this normalization.
func providerPolicyDigest(v any) string {
	d := json.NewDecoder(bytes.NewReader(jsonBytes(v)))
	d.UseNumber()
	var decoded any
	if e := d.Decode(&decoded); e != nil {
		return "invalid"
	}
	return contentDigest(string(jsonBytes(decoded)))
}
