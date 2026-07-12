package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

type ApprovalStatus string

const (
	ApprovalPending  ApprovalStatus = "pending"
	ApprovalApproved ApprovalStatus = "approved"
	ApprovalRejected ApprovalStatus = "rejected"
	ApprovalRevoked  ApprovalStatus = "revoked"
	ApprovalConsumed ApprovalStatus = "consumed"
)

type ApprovalEndorserRole string

const (
	ApprovalRoleMember ApprovalEndorserRole = "member"
	ApprovalRoleOwner  ApprovalEndorserRole = "owner"
	ApprovalRoleAdmin  ApprovalEndorserRole = "admin"
)

// ApprovalActionPlan is deliberately closed and map-free so its JSON digest is
// stable. ParametersDigest binds any provider-specific normalized payload.
type ApprovalActionPlan struct {
	PolicyVersion    string `json:"policy_version"`
	Recipient        string `json:"recipient"`
	Destination      string `json:"destination"`
	Command          string `json:"command"`
	ParametersDigest string `json:"parameters_digest"`
	ACLVersion       int64  `json:"acl_version"`
}

type ApprovalBinding struct {
	Target   ACLObjectRef       `json:"target"`
	Revision ACLRevisionRef     `json:"revision"`
	Action   ACLAction          `json:"action"`
	Plan     ApprovalActionPlan `json:"plan"`
}

type ApprovalEndorsement struct {
	Principal ACLPrincipal
	Role      ApprovalEndorserRole
	At        time.Time
}

type ApprovalRequest struct {
	ID               string
	Binding          ApprovalBinding
	ActionPlanDigest string
	Status           ApprovalStatus
	RequestedBy      ACLPrincipal
	CreatedAt        time.Time
	ExpiresAt        time.Time
	Endorsements     map[string]ApprovalEndorsement
	RejectedBy       string
	RevokedBy        string
	Reason           string
	ConsumedAt       *time.Time
}

type DispatchStatus string

const (
	DispatchPrepared  DispatchStatus = "prepared"
	DispatchSending   DispatchStatus = "sending"
	DispatchConfirmed DispatchStatus = "confirmed"
	DispatchFailed    DispatchStatus = "failed"
	DispatchAmbiguous DispatchStatus = "ambiguous"
)

type ApprovalDispatch struct {
	ApprovalID       string
	ExecutionKey     string
	ActionPlanDigest string
	Status           DispatchStatus
	CreatedAt        time.Time
	UpdatedAt        time.Time
	ProviderReceipt  string
	Error            string
}

var (
	ErrApprovalNotFound       = errors.New("approval not found")
	ErrApprovalDenied         = errors.New("approval denied")
	ErrApprovalInvalid        = errors.New("approval binding is invalid")
	ErrApprovalState          = errors.New("approval status transition is not allowed")
	ErrApprovalBindingChanged = errors.New("approval binding changed")
	ErrDispatchAmbiguous      = errors.New("dispatch is ambiguous and cannot be resent")
)

// ApprovalRepository provides one serializable mutation boundary. A
// PostgreSQL implementation maps Transact to a row lock + database transaction.
type ApprovalRepository interface {
	CreateApproval(context.Context, ApprovalRequest) error
	ReadApproval(context.Context, string) (ApprovalRequest, *ApprovalDispatch, error)
	TransactApproval(context.Context, string, func(*ApprovalRequest, **ApprovalDispatch) error) error
}

type ApprovalAuthorizer interface {
	MayApprove(context.Context, ACLPrincipal, ApprovalBinding) (bool, error)
}

type ApprovalEligibility interface {
	EndorserRole(context.Context, ACLPrincipal) (ApprovalEndorserRole, bool, error)
}

type ApprovalTargetResolver interface {
	CurrentApprovalTarget(context.Context, ACLObjectRef) (ACLObject, error)
}

type ApprovalService struct {
	Repo        ApprovalRepository
	Authorizer  ApprovalAuthorizer
	Eligibility ApprovalEligibility
	Targets     ApprovalTargetResolver
	Now         func() time.Time
}

func (s ApprovalService) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s ApprovalService) Create(ctx context.Context, id string, binding ApprovalBinding, requestedBy ACLPrincipal, expiresAt time.Time) (ApprovalRequest, error) {
	if s.Repo == nil || s.Authorizer == nil || s.Eligibility == nil || s.Targets == nil {
		return ApprovalRequest{}, ErrApprovalDenied
	}
	digest, err := approvalBindingDigest(binding)
	if err != nil {
		return ApprovalRequest{}, err
	}
	allowed, err := s.Authorizer.MayApprove(ctx, requestedBy, binding)
	if err != nil || !allowed {
		return ApprovalRequest{}, ErrApprovalDenied
	}
	now := s.now()
	if expiresAt.IsZero() || !expiresAt.After(now) {
		return ApprovalRequest{}, fmt.Errorf("%w: expiry must be in the future", ErrApprovalInvalid)
	}
	if strings.TrimSpace(id) == "" {
		id, err = newApprovalID()
		if err != nil {
			return ApprovalRequest{}, err
		}
	} else {
		parsedID, parseErr := uuid.Parse(id)
		if parseErr != nil {
			return ApprovalRequest{}, fmt.Errorf("%w: approval id must be a UUID", ErrApprovalInvalid)
		}
		id = parsedID.String()
	}
	request := ApprovalRequest{
		ID: id, Binding: binding, ActionPlanDigest: digest, Status: ApprovalPending,
		RequestedBy: requestedBy, CreatedAt: now, ExpiresAt: expiresAt.UTC(),
		Endorsements: map[string]ApprovalEndorsement{},
	}
	if err := s.Repo.CreateApproval(ctx, request); err != nil {
		return ApprovalRequest{}, err
	}
	return cloneApprovalRequest(request), nil
}

func (s ApprovalService) Endorse(ctx context.Context, id string, principal ACLPrincipal) (ApprovalRequest, error) {
	if s.Repo == nil || s.Authorizer == nil || s.Eligibility == nil {
		return ApprovalRequest{}, ErrApprovalDenied
	}
	var result ApprovalRequest
	err := s.Repo.TransactApproval(ctx, id, func(request *ApprovalRequest, _ **ApprovalDispatch) error {
		if request.Status != ApprovalPending {
			return ErrApprovalState
		}
		if !request.ExpiresAt.After(s.now()) {
			request.Status = ApprovalRevoked
			request.Reason = "expired"
			return ErrApprovalState
		}
		allowed, err := s.Authorizer.MayApprove(ctx, principal, request.Binding)
		if err != nil || !allowed {
			return ErrApprovalDenied
		}
		role, active, err := s.Eligibility.EndorserRole(ctx, principal)
		if err != nil || !active || !validEndorser(principal, role) {
			return ErrApprovalDenied
		}
		key := approvalPrincipalKey(principal)
		if _, exists := request.Endorsements[key]; !exists {
			request.Endorsements[key] = ApprovalEndorsement{Principal: principal, Role: role, At: s.now()}
		}
		if approvalThresholdMet(ctx, s.Eligibility, request.Endorsements) {
			request.Status = ApprovalApproved
		}
		result = cloneApprovalRequest(*request)
		return nil
	})
	return result, err
}

func (s ApprovalService) Reject(ctx context.Context, id string, principal ACLPrincipal, reason string) error {
	return s.terminalDecision(ctx, id, principal, ApprovalRejected, reason)
}

func (s ApprovalService) Revoke(ctx context.Context, id string, principal ACLPrincipal, reason string) error {
	return s.terminalDecision(ctx, id, principal, ApprovalRevoked, reason)
}

func (s ApprovalService) terminalDecision(ctx context.Context, id string, principal ACLPrincipal, next ApprovalStatus, reason string) error {
	if s.Repo == nil || s.Authorizer == nil {
		return ErrApprovalDenied
	}
	return s.Repo.TransactApproval(ctx, id, func(request *ApprovalRequest, _ **ApprovalDispatch) error {
		allowed, err := s.Authorizer.MayApprove(ctx, principal, request.Binding)
		if err != nil || !allowed {
			return ErrApprovalDenied
		}
		if next == ApprovalRejected && request.Status != ApprovalPending {
			return ErrApprovalState
		}
		if next == ApprovalRevoked && request.Status != ApprovalPending && request.Status != ApprovalApproved {
			return ErrApprovalState
		}
		request.Status = next
		request.Reason = strings.TrimSpace(reason)
		if next == ApprovalRejected {
			request.RejectedBy = approvalPrincipalKey(principal)
		} else {
			request.RevokedBy = approvalPrincipalKey(principal)
		}
		return nil
	})
}

// Consume atomically consumes an exact approved binding and creates the one
// dispatch record. A retry returns that record; it never creates a second send.
func (s ApprovalService) Consume(ctx context.Context, id string, exact ApprovalBinding) (ApprovalDispatch, bool, error) {
	if s.Repo == nil || s.Eligibility == nil || s.Targets == nil {
		return ApprovalDispatch{}, false, ErrApprovalDenied
	}
	digest, err := approvalBindingDigest(exact)
	if err != nil {
		return ApprovalDispatch{}, false, err
	}
	var result ApprovalDispatch
	created := false
	err = s.Repo.TransactApproval(ctx, id, func(request *ApprovalRequest, dispatch **ApprovalDispatch) error {
		if request.ActionPlanDigest != digest {
			return ErrApprovalBindingChanged
		}
		if *dispatch != nil {
			result = **dispatch
			if result.Status == DispatchAmbiguous {
				return ErrDispatchAmbiguous
			}
			return nil
		}
		if request.Status != ApprovalApproved || !request.ExpiresAt.After(s.now()) || !approvalThresholdMet(ctx, s.Eligibility, request.Endorsements) {
			return ErrApprovalState
		}
		current, err := s.Targets.CurrentApprovalTarget(ctx, request.Binding.Target)
		if err != nil || current.Deleted || current.Ref != request.Binding.Target ||
			current.CurrentContentRevision != request.Binding.Revision.ContentRevision ||
			current.CurrentContentDigest != request.Binding.Revision.ContentDigest {
			return ErrApprovalBindingChanged
		}
		now := s.now()
		executionKey := stableExecutionKey(request.ID, request.ActionPlanDigest)
		fresh := &ApprovalDispatch{ApprovalID: request.ID, ExecutionKey: executionKey, ActionPlanDigest: digest, Status: DispatchPrepared, CreatedAt: now, UpdatedAt: now}
		*dispatch = fresh
		request.Status = ApprovalConsumed
		request.ConsumedAt = &now
		result = *fresh
		created = true
		return nil
	})
	return result, created, err
}

func (s ApprovalService) TransitionDispatch(ctx context.Context, id string, next DispatchStatus, receipt, failure string) (ApprovalDispatch, error) {
	var result ApprovalDispatch
	err := s.Repo.TransactApproval(ctx, id, func(_ *ApprovalRequest, dispatch **ApprovalDispatch) error {
		if *dispatch == nil || !dispatchTransitionAllowed((*dispatch).Status, next) {
			return ErrApprovalState
		}
		(*dispatch).Status = next
		(*dispatch).UpdatedAt = s.now()
		(*dispatch).ProviderReceipt = strings.TrimSpace(receipt)
		(*dispatch).Error = strings.TrimSpace(failure)
		result = **dispatch
		return nil
	})
	return result, err
}

func dispatchTransitionAllowed(from, to DispatchStatus) bool {
	if from == to {
		return true
	}
	switch from {
	case DispatchPrepared:
		return to == DispatchSending || to == DispatchConfirmed || to == DispatchFailed || to == DispatchAmbiguous
	case DispatchSending:
		return to == DispatchConfirmed || to == DispatchFailed || to == DispatchAmbiguous
	case DispatchAmbiguous:
		// Only receipt reconciliation may settle ambiguity. It can never return to
		// a sendable state.
		return to == DispatchConfirmed || to == DispatchFailed
	default:
		return false
	}
}

func approvalBindingDigest(binding ApprovalBinding) (string, error) {
	binding.Target.TenantID = strings.TrimSpace(binding.Target.TenantID)
	binding.Target.Type = strings.TrimSpace(binding.Target.Type)
	binding.Target.ID = strings.TrimSpace(binding.Target.ID)
	binding.Revision.ContentDigest = strings.TrimSpace(binding.Revision.ContentDigest)
	binding.Plan.PolicyVersion = strings.TrimSpace(binding.Plan.PolicyVersion)
	binding.Plan.Recipient = strings.TrimSpace(binding.Plan.Recipient)
	binding.Plan.Destination = strings.TrimSpace(binding.Plan.Destination)
	binding.Plan.Command = strings.TrimSpace(binding.Plan.Command)
	binding.Plan.ParametersDigest = strings.TrimSpace(binding.Plan.ParametersDigest)
	if binding.Target.TenantID == "" || binding.Target.Type == "" || binding.Target.ID == "" || binding.Target.ACLVersion < 1 ||
		binding.Revision.ContentRevision < 1 || !validLowerSHA256(binding.Revision.ContentDigest) || !validACLAction(binding.Action) ||
		binding.Plan.PolicyVersion == "" || binding.Plan.Recipient == "" || binding.Plan.Destination == "" || binding.Plan.Command == "" ||
		!validLowerSHA256(binding.Plan.ParametersDigest) || binding.Plan.ACLVersion != binding.Target.ACLVersion {
		return "", ErrApprovalInvalid
	}
	raw, err := json.Marshal(binding)
	if err != nil {
		return "", ErrApprovalInvalid
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func stableExecutionKey(approvalID, actionDigest string) string {
	// Length-prefix both fields so no pair of variable-length inputs can share
	// the same preimage by boundary ambiguity.
	preimage := fmt.Sprintf("%d:%s%d:%s", len(approvalID), approvalID, len(actionDigest), actionDigest)
	sum := sha256.Sum256([]byte(preimage))
	return hex.EncodeToString(sum[:])
}

func validLowerSHA256(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	for _, char := range value {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}

func newApprovalID() (string, error) {
	id, err := uuid.NewRandom()
	if err != nil {
		return "", err
	}
	return id.String(), nil
}

func approvalPrincipalKey(principal ACLPrincipal) string {
	return string(principal.Kind) + ":" + principal.TenantID + ":" + principal.ID
}

func validEndorser(principal ACLPrincipal, role ApprovalEndorserRole) bool {
	if principal.Kind != ACLPrincipalUser || strings.TrimSpace(principal.ID) == "" {
		return false
	}
	return role == ApprovalRoleMember || role == ApprovalRoleOwner || role == ApprovalRoleAdmin
}

func approvalThresholdMet(ctx context.Context, eligibility ApprovalEligibility, endorsements map[string]ApprovalEndorsement) bool {
	if eligibility == nil {
		return false
	}
	members := map[string]struct{}{}
	for _, endorsement := range endorsements {
		role, active, err := eligibility.EndorserRole(ctx, endorsement.Principal)
		if err != nil || !active || !validEndorser(endorsement.Principal, role) {
			continue
		}
		if role == ApprovalRoleOwner || role == ApprovalRoleAdmin {
			return true
		}
		members[approvalPrincipalKey(endorsement.Principal)] = struct{}{}
	}
	return len(members) >= 2
}

func cloneApprovalRequest(request ApprovalRequest) ApprovalRequest {
	copyRequest := request
	copyRequest.Endorsements = make(map[string]ApprovalEndorsement, len(request.Endorsements))
	for key, endorsement := range request.Endorsements {
		copyRequest.Endorsements[key] = endorsement
	}
	return copyRequest
}

// MemoryApprovalRepository is the reference transactional implementation for
// tests. The mutation callback runs under one mutex, matching a row-locked SQL
// transaction's exactly-once boundary.
type MemoryApprovalRepository struct {
	mu         sync.Mutex
	Approvals  map[string]ApprovalRequest
	Dispatches map[string]*ApprovalDispatch
}

func NewMemoryApprovalRepository() *MemoryApprovalRepository {
	return &MemoryApprovalRepository{Approvals: map[string]ApprovalRequest{}, Dispatches: map[string]*ApprovalDispatch{}}
}

func (r *MemoryApprovalRepository) CreateApproval(_ context.Context, request ApprovalRequest) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.Approvals[request.ID]; exists {
		return fmt.Errorf("approval already exists")
	}
	r.Approvals[request.ID] = cloneApprovalRequest(request)
	return nil
}

func (r *MemoryApprovalRepository) ReadApproval(_ context.Context, id string) (ApprovalRequest, *ApprovalDispatch, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	request, ok := r.Approvals[id]
	if !ok {
		return ApprovalRequest{}, nil, ErrApprovalNotFound
	}
	var dispatch *ApprovalDispatch
	if existing := r.Dispatches[id]; existing != nil {
		copyDispatch := *existing
		dispatch = &copyDispatch
	}
	return cloneApprovalRequest(request), dispatch, nil
}

func (r *MemoryApprovalRepository) TransactApproval(_ context.Context, id string, mutate func(*ApprovalRequest, **ApprovalDispatch) error) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	request, ok := r.Approvals[id]
	if !ok {
		return ErrApprovalNotFound
	}
	request = cloneApprovalRequest(request)
	var dispatch *ApprovalDispatch
	if existing := r.Dispatches[id]; existing != nil {
		copyDispatch := *existing
		dispatch = &copyDispatch
	}
	if err := mutate(&request, &dispatch); err != nil {
		// Persist state-only terminal changes such as expiry revocation.
		if request.Status != r.Approvals[id].Status && request.Status == ApprovalRevoked {
			r.Approvals[id] = request
		}
		return err
	}
	r.Approvals[id] = request
	if dispatch != nil {
		copyDispatch := *dispatch
		r.Dispatches[id] = &copyDispatch
	}
	return nil
}
