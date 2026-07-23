package main

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

type ConsentLane string

const (
	// Audio transport is the live room path to other participants. It is not
	// capture: an unavailable/declined consent store must not disable joining,
	// listening, chat, camera, or direct WebRTC audio transport.
	ConsentLaneAudioTransport ConsentLane = "audio_transport"
	ConsentLaneAudioCapture   ConsentLane = "audio_capture"
	ConsentLaneTranscription  ConsentLane = "transcription"
	ConsentLaneModelAnalysis  ConsentLane = "model_analysis"
	ConsentLaneOrgMemory      ConsentLane = "org_memory"
)

var (
	ErrConsentAuthorityUnavailable = errors.New("consent authority unavailable")
	ErrConsentAdmissionInvalid     = errors.New("invalid consent admission binding")
	ErrConsentUnauthenticated      = errors.New("consent principal is unauthenticated")
	ErrConsentFenceStale           = errors.New("consent fence is stale")
)

// ConsentAdmissionBinding is created from server-owned admission state. The
// HTTP surface never accepts any of these identity/scope fields from JSON.
// AnchorID proves the principal was durably admitted before consent is used.
type ConsentAdmissionBinding struct {
	TenantID              string
	PrincipalKind         ACLPrincipalKind
	PrincipalID           string
	RoomID                string
	SittingID             string
	AnchorID              string
	GuestPolicyListenOnly bool
}

const consentContributorBindingsMetadataKey = "consentContributorBindings"

// consentContributorBinding is the durable, server-authored provenance for
// every microphone principal whose samples contributed to one mixed segment.
// It intentionally carries the admission anchor: downstream recall must bind
// the metadata back to the canonical admission record before consulting
// consent, rather than treating a principal id copied from JSONL as authority.
type consentContributorBinding struct {
	TenantID              string           `json:"tenantId"`
	PrincipalKind         ACLPrincipalKind `json:"principalKind"`
	PrincipalID           string           `json:"principalId"`
	RoomID                string           `json:"roomId"`
	SittingID             string           `json:"sittingId"`
	AnchorID              string           `json:"anchorId"`
	GuestPolicyListenOnly bool             `json:"guestPolicyListenOnly,omitempty"`
}

func durableConsentContributorBindings(fences []ConsentFence) []ConsentAdmissionBinding {
	byKey := make(map[string]ConsentAdmissionBinding, len(fences))
	for _, fence := range fences {
		if fence.binding.Validate() != nil {
			continue
		}
		byKey[consentBindingKey(fence.binding)] = fence.binding
	}
	keys := make([]string, 0, len(byKey))
	for key := range byKey {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	bindings := make([]ConsentAdmissionBinding, 0, len(keys))
	for _, key := range keys {
		bindings = append(bindings, byKey[key])
	}
	return bindings
}

func encodeConsentContributorBindings(bindings []ConsentAdmissionBinding) (string, error) {
	byKey := make(map[string]ConsentAdmissionBinding, len(bindings))
	for _, binding := range bindings {
		if err := binding.Validate(); err != nil {
			return "", err
		}
		byKey[consentBindingKey(binding)] = binding
	}
	if len(byKey) == 0 {
		return "", ErrConsentAdmissionInvalid
	}
	keys := make([]string, 0, len(byKey))
	for key := range byKey {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	records := make([]consentContributorBinding, 0, len(keys))
	for _, key := range keys {
		binding := byKey[key]
		records = append(records, consentContributorBinding{
			TenantID: binding.TenantID, PrincipalKind: binding.PrincipalKind, PrincipalID: binding.PrincipalID,
			RoomID: binding.RoomID, SittingID: binding.SittingID, AnchorID: binding.AnchorID,
			GuestPolicyListenOnly: binding.GuestPolicyListenOnly,
		})
	}
	raw, err := json.Marshal(records)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func decodeConsentContributorBindings(raw string) ([]ConsentAdmissionBinding, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var records []consentContributorBinding
	if err := json.Unmarshal([]byte(raw), &records); err != nil || len(records) == 0 {
		return nil, ErrConsentAdmissionInvalid
	}
	bindings := make([]ConsentAdmissionBinding, 0, len(records))
	seen := make(map[string]struct{}, len(records))
	for _, record := range records {
		binding := ConsentAdmissionBinding{
			TenantID: record.TenantID, PrincipalKind: record.PrincipalKind, PrincipalID: record.PrincipalID,
			RoomID: record.RoomID, SittingID: record.SittingID, AnchorID: record.AnchorID,
			GuestPolicyListenOnly: record.GuestPolicyListenOnly,
		}
		if err := binding.Validate(); err != nil {
			return nil, err
		}
		key := consentBindingKey(binding)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		bindings = append(bindings, binding)
	}
	sort.Slice(bindings, func(i, j int) bool { return consentBindingKey(bindings[i]) < consentBindingKey(bindings[j]) })
	return bindings, nil
}

func (binding ConsentAdmissionBinding) Validate() error {
	if strings.TrimSpace(binding.TenantID) == "" ||
		(binding.PrincipalKind != ACLPrincipalUser && binding.PrincipalKind != ACLPrincipalGuest) ||
		strings.TrimSpace(binding.PrincipalID) == "" || strings.TrimSpace(binding.RoomID) == "" ||
		strings.TrimSpace(binding.SittingID) == "" || strings.TrimSpace(binding.AnchorID) == "" {
		return ErrConsentAdmissionInvalid
	}
	if binding.PrincipalKind == ACLPrincipalGuest && !isHexDigest(binding.PrincipalID) {
		return ErrConsentAdmissionInvalid
	}
	return nil
}

type ConsentLaneDecision struct {
	Lane          ConsentLane
	Allowed       bool
	MissingScopes []ConsentScope
	RecordIDs     map[ConsentScope]string
	Dispositions  map[ConsentScope]ConsentDisposition
	Fence         ConsentFence
}

// ConsentFence is intentionally body-free. A downstream capture, STT,
// model, or org-memory seam must ValidateFence immediately before committing
// work; a withdrawal, later denial, store outage, or different sitting makes
// the old fence unusable.
type ConsentFence struct {
	binding      ConsentAdmissionBinding
	lane         ConsentLane
	policy       string
	generation   uint64
	recordDigest string
	issuedAt     time.Time
}

// consentRoomDecisionFence binds a room-owned side effect to the complete
// consent-decision generation observed before the provider tool result is
// accepted. It is intentionally broader than one participant: mixed room
// audio may contain several principals, so any consent decision in the room
// invalidates queued room-tool work and forces a fresh provider turn.
type consentRoomDecisionFence struct {
	authority  *ConsentLaneAuthority
	tenantID   string
	roomID     string
	sittingID  string
	policy     string
	generation uint64
}

type ConsentLaneAuthority struct {
	Store         ConsentStore
	PolicyVersion string
	Now           func() time.Time
	CaptureCutoff func() (uint64, error)
	OnWithdrawal  func(ConsentWithdrawalNotice)

	mu                sync.RWMutex
	generations       map[string]uint64
	roomGenerations   map[string]uint64
	bindingMu         sync.Mutex
	bindingLocks      map[string]*sync.Mutex
	roomDecisionMu    sync.Mutex
	roomDecisionLocks map[string]*sync.Mutex
}

const consentIngressRefreshInterval = 250 * time.Millisecond
const consentAuthorityLockTimeout = 5 * time.Second

// AuthorizeIngress resolves the whole dependency stack in one store read and
// derives lane-specific fences from the exact record subsets. This avoids
// issuing three overlapping durable queries for every refresh tick.
func (authority *ConsentLaneAuthority) AuthorizeIngress(ctx context.Context, binding ConsentAdmissionBinding) (map[ConsentLane]ConsentLaneDecision, error) {
	if err := binding.Validate(); err != nil {
		return nil, err
	}
	if err := authority.Health(ctx); err != nil {
		return nil, err
	}
	decision, err := authority.Store.Effective(ctx, ConsentQuery{
		TenantID: binding.TenantID, PrincipalKind: binding.PrincipalKind, PrincipalID: binding.PrincipalID,
		RoomID: binding.RoomID, SittingID: binding.SittingID, PolicyVersion: authority.PolicyVersion,
		Scopes: []ConsentScope{ConsentOrgMemory},
	})
	if err != nil {
		return nil, fmt.Errorf("%w: effective ingress decision: %v", ErrConsentAuthorityUnavailable, err)
	}
	generation := authority.generation(binding)
	result := make(map[ConsentLane]ConsentLaneDecision, 3)
	for _, lane := range []ConsentLane{ConsentLaneAudioCapture, ConsentLaneTranscription, ConsentLaneModelAnalysis} {
		required, _ := consentLaneScopes(lane)
		required, _ = normalizeConsentQuery(ConsentQuery{
			TenantID: binding.TenantID, PrincipalKind: binding.PrincipalKind, PrincipalID: binding.PrincipalID,
			RoomID: binding.RoomID, SittingID: binding.SittingID, PolicyVersion: authority.PolicyVersion, Scopes: required,
		})
		laneDecision := ConsentLaneDecision{
			Lane: lane, Allowed: true, RecordIDs: make(map[ConsentScope]string),
			Dispositions: make(map[ConsentScope]ConsentDisposition),
		}
		for _, scope := range required {
			disposition, ok := decision.Dispositions[scope]
			if id := decision.RecordIDs[scope]; id != "" {
				laneDecision.RecordIDs[scope] = id
			}
			if ok {
				laneDecision.Dispositions[scope] = disposition
			}
			if !ok || disposition != ConsentGranted {
				laneDecision.Allowed = false
				laneDecision.MissingScopes = append(laneDecision.MissingScopes, scope)
			}
		}
		laneDecision.Fence = ConsentFence{
			binding: binding, lane: lane, policy: authority.PolicyVersion, generation: generation,
			recordDigest: consentRecordSetDigest(laneDecision.RecordIDs), issuedAt: time.Now().UTC(),
		}
		result[lane] = laneDecision
	}
	return result, nil
}

type consentAudioIngressGate struct {
	authority *ConsentLaneAuthority
	app       *kanbanBoardApp
	principal CanonicalPrincipalRef
	roomID    string
	stop      chan struct{}
	done      chan struct{}
	closeOnce sync.Once
	mu        sync.RWMutex
	fences    map[ConsentLane]ConsentFence
}

func newConsentAudioIngressGate(app *kanbanBoardApp, principal CanonicalPrincipalRef, roomID string) *consentAudioIngressGate {
	gate := &consentAudioIngressGate{
		authority: currentConsentLaneAuthority(), app: app, principal: principal,
		roomID: normalizeRoomID(roomID), stop: make(chan struct{}), done: make(chan struct{}),
	}
	go gate.run()
	return gate
}

func (gate *consentAudioIngressGate) run() {
	defer close(gate.done)
	ticker := time.NewTicker(consentIngressRefreshInterval)
	defer ticker.Stop()
	for {
		gate.refresh()
		select {
		case <-gate.stop:
			return
		case <-ticker.C:
		}
	}
}

func (gate *consentAudioIngressGate) refresh() {
	fences := map[ConsentLane]ConsentFence{}
	if gate.app != nil && gate.authority != nil {
		ctx, cancel := context.WithTimeout(context.Background(), consentAuthorityLockTimeout)
		defer cancel()
		if meeting, ok := gate.app.meetings.activeRecord(gate.roomID); ok {
			if binding, err := gate.app.consentBindingForPrincipal(ctx, gate.principal, gate.roomID, meeting.ID); err == nil {
				if decisions, err := gate.authority.AuthorizeIngress(ctx, binding); err == nil {
					for lane, decision := range decisions {
						if decision.Allowed {
							fences[lane] = decision.Fence
						}
					}
				}
			}
		}
	}
	gate.mu.Lock()
	gate.fences = fences
	gate.mu.Unlock()
}

func (gate *consentAudioIngressGate) admittedFences() (map[ConsentLane]ConsentFence, bool) {
	if gate == nil || gate.authority == nil {
		return nil, false
	}
	gate.mu.RLock()
	current := make(map[ConsentLane]ConsentFence, len(gate.fences))
	for lane, fence := range gate.fences {
		current[lane] = fence
	}
	gate.mu.RUnlock()
	capture, ok := current[ConsentLaneAudioCapture]
	if !ok || gate.authority.ValidateIngressFence(capture) != nil {
		return nil, false
	}
	for lane, fence := range current {
		if gate.authority.ValidateIngressFence(fence) != nil {
			delete(current, lane)
		}
	}
	return current, true
}

// ValidateIngressFence adds a short durable-authority lease to the local
// generation check. If PostgreSQL becomes unreachable, issuedAt cannot renew
// and capture/model ingress fails closed without blocking direct RTP.
func (authority *ConsentLaneAuthority) ValidateIngressFence(fence ConsentFence) error {
	if err := authority.ValidateFenceLocal(fence); err != nil {
		return err
	}
	if fence.issuedAt.IsZero() || time.Since(fence.issuedAt) > 2*consentIngressRefreshInterval {
		return ErrConsentFenceStale
	}
	return nil
}

func (gate *consentAudioIngressGate) close() {
	if gate == nil {
		return
	}
	gate.closeOnce.Do(func() { close(gate.stop); <-gate.done })
}

// ConsentWithdrawalNotice is emitted only after the immutable withdrawal is
// durable. Room/media owners use it to cancel in-flight uncommitted segments
// and dependent queued work; the new generation has already made every prior
// fence fail before the callback runs.
type ConsentWithdrawalNotice struct {
	Binding                     ConsentAdmissionBinding
	Scope                       ConsentScope
	RecordID                    string
	LastAcceptedCaptureSequence uint64
}

func NewConsentLaneAuthority(store ConsentStore, policyVersion string) *ConsentLaneAuthority {
	return &ConsentLaneAuthority{
		Store: store, PolicyVersion: strings.TrimSpace(policyVersion), Now: time.Now,
		generations: make(map[string]uint64), roomGenerations: make(map[string]uint64),
		bindingLocks: make(map[string]*sync.Mutex), roomDecisionLocks: make(map[string]*sync.Mutex),
	}
}

func (authority *ConsentLaneAuthority) Health(ctx context.Context) error {
	if authority == nil || authority.Store == nil || strings.TrimSpace(authority.PolicyVersion) == "" {
		return ErrConsentAuthorityUnavailable
	}
	if store, ok := authority.Store.(*PostgresConsentStore); ok {
		if store == nil || store.canonical == nil {
			return ErrConsentAuthorityUnavailable
		}
		if err := store.canonical.Health(ctx); err != nil {
			return fmt.Errorf("%w: durable store health", ErrConsentAuthorityUnavailable)
		}
	}
	return nil
}

func (authority *ConsentLaneAuthority) Authorize(ctx context.Context, binding ConsentAdmissionBinding, lane ConsentLane) (ConsentLaneDecision, error) {
	if err := binding.Validate(); err != nil {
		return ConsentLaneDecision{Lane: lane}, err
	}
	if lane == ConsentLaneAudioTransport {
		return ConsentLaneDecision{Lane: lane, Allowed: true, RecordIDs: map[ConsentScope]string{}}, nil
	}
	scopes, ok := consentLaneScopes(lane)
	if !ok {
		return ConsentLaneDecision{Lane: lane}, ErrConsentInvalid
	}
	if err := authority.Health(ctx); err != nil {
		return ConsentLaneDecision{Lane: lane}, err
	}
	decision, err := authority.Store.Effective(ctx, ConsentQuery{
		TenantID: binding.TenantID, PrincipalKind: binding.PrincipalKind, PrincipalID: binding.PrincipalID,
		RoomID: binding.RoomID, SittingID: binding.SittingID, PolicyVersion: authority.PolicyVersion,
		Scopes: scopes,
	})
	if err != nil {
		return ConsentLaneDecision{Lane: lane}, fmt.Errorf("%w: effective decision: %v", ErrConsentAuthorityUnavailable, err)
	}
	generation := authority.generation(binding)
	fence := ConsentFence{
		binding: binding, lane: lane, policy: authority.PolicyVersion, generation: generation,
		recordDigest: consentRecordSetDigest(decision.RecordIDs), issuedAt: time.Now().UTC(),
	}
	return ConsentLaneDecision{
		Lane: lane, Allowed: decision.Allowed,
		MissingScopes: append([]ConsentScope(nil), decision.MissingScopes...),
		RecordIDs:     cloneConsentRecordIDs(decision.RecordIDs),
		Dispositions:  cloneConsentDispositions(decision.Dispositions), Fence: fence,
	}, nil
}

// ValidateFenceLocal is the hot-path half of a consent check. The fence was
// minted only from durable effective consent; this check makes a locally
// persisted denial or withdrawal invalidate queued audio immediately without
// a database round trip. Provider and durable-commit seams still use
// ValidateFence, which re-resolves PostgreSQL and detects remote changes or an
// authority outage.
func (authority *ConsentLaneAuthority) ValidateFenceLocal(fence ConsentFence) error {
	if authority == nil || strings.TrimSpace(fence.policy) == "" || fence.policy != strings.TrimSpace(authority.PolicyVersion) {
		return ErrConsentFenceStale
	}
	if fence.binding.Validate() != nil || fence.generation != authority.generation(fence.binding) {
		return ErrConsentFenceStale
	}
	return nil
}

// ValidateFence re-resolves durable effective consent. It is the runtime seam
// for org-memory and every earlier lane; callers never stamp or trust a
// `consented=true` metadata field.
func (authority *ConsentLaneAuthority) ValidateFence(ctx context.Context, fence ConsentFence) error {
	if err := authority.ValidateFenceLocal(fence); err != nil {
		return err
	}
	current, err := authority.Authorize(ctx, fence.binding, fence.lane)
	if err != nil {
		return err
	}
	if !current.Allowed || current.Fence.generation != fence.generation || current.Fence.recordDigest != fence.recordDigest {
		return ErrConsentFenceStale
	}
	return nil
}

// CommitWithFence linearizes a consent-sensitive local commit against choices
// accepted by this authority. A withdrawal cannot land between the final
// durable re-check and the JSONL append in the live process.
func (authority *ConsentLaneAuthority) CommitWithFence(ctx context.Context, fence ConsentFence, commit func() error) error {
	return authority.CommitWithFences(ctx, []ConsentFence{fence}, commit)
}

// CommitWithFences is the mixed-source form of CommitWithFence. It acquires
// each contributor binding in a stable canonical order, then re-resolves every
// lane while all locks remain held. That ordering prevents cross-segment
// deadlocks and makes the append one atomic authority boundary for the whole
// mixed segment: one stale contributor rejects the commit.
func (authority *ConsentLaneAuthority) CommitWithFences(ctx context.Context, fences []ConsentFence, commit func() error) error {
	if commit == nil {
		return ErrConsentInvalid
	}
	if authority == nil || len(fences) == 0 {
		return ErrConsentAdmissionInvalid
	}
	type bindingLockRef struct {
		key     string
		binding ConsentAdmissionBinding
		lock    *sync.Mutex
	}
	bindingsByKey := make(map[string]ConsentAdmissionBinding, len(fences))
	for _, fence := range fences {
		if fence.binding.Validate() != nil {
			return ErrConsentAdmissionInvalid
		}
		bindingsByKey[consentBindingKey(fence.binding)] = fence.binding
	}
	bindings := make([]bindingLockRef, 0, len(bindingsByKey))
	for key, binding := range bindingsByKey {
		bindings = append(bindings, bindingLockRef{key: key, binding: binding, lock: authority.bindingLock(binding)})
	}
	sort.Slice(bindings, func(i, j int) bool { return bindings[i].key < bindings[j].key })
	for _, binding := range bindings {
		binding.lock.Lock()
	}
	defer func() {
		for index := len(bindings) - 1; index >= 0; index-- {
			bindings[index].lock.Unlock()
		}
	}()

	orderedBindings := make([]ConsentAdmissionBinding, 0, len(bindings))
	for _, binding := range bindings {
		orderedBindings = append(orderedBindings, binding.binding)
	}
	release, err := authority.acquireDistributedBindingLocks(ctx, orderedBindings)
	if err != nil {
		return err
	}
	defer release()

	sortedFences := append([]ConsentFence(nil), fences...)
	sort.Slice(sortedFences, func(i, j int) bool {
		left, right := consentBindingKey(sortedFences[i].binding), consentBindingKey(sortedFences[j].binding)
		if left != right {
			return left < right
		}
		return sortedFences[i].lane < sortedFences[j].lane
	})
	for _, fence := range sortedFences {
		if err := authority.ValidateFence(ctx, fence); err != nil {
			return err
		}
	}
	return commit()
}

// RecordDecision persists exactly one explicit lane choice. Identity, room,
// sitting, policy, evidence, time, and withdrawal cutoff are all server-owned;
// only the scope and the human's grant/deny/withdraw choice cross the API.
func (authority *ConsentLaneAuthority) RecordDecision(ctx context.Context, binding ConsentAdmissionBinding, scope ConsentScope, disposition ConsentDisposition) (ConsentRecord, error) {
	if err := binding.Validate(); err != nil {
		return ConsentRecord{}, err
	}
	if !validConsentScope(scope) || !validConsentDisposition(disposition) {
		return ConsentRecord{}, ErrConsentInvalid
	}
	if err := authority.Health(ctx); err != nil {
		return ConsentRecord{}, err
	}
	roomLock := authority.roomDecisionLock(binding.TenantID, binding.RoomID, binding.SittingID)
	roomLock.Lock()
	record, err := authority.recordDecisionUnderRoomLock(ctx, binding, scope, disposition)
	roomLock.Unlock()
	if err != nil {
		return ConsentRecord{}, err
	}
	// Fanout and its cancellation wait run only after every authority lock is
	// released. A stale tool waiting on the room lock can now observe the bumped
	// generation, abort, and drain instead of deadlocking withdrawal.
	if disposition == ConsentWithdrawn && authority.OnWithdrawal != nil {
		authority.OnWithdrawal(ConsentWithdrawalNotice{
			Binding: binding, Scope: scope, RecordID: record.ID,
			LastAcceptedCaptureSequence: *record.LastAcceptedCaptureSequence,
		})
	}
	return cloneConsentRecord(record), nil
}

func (authority *ConsentLaneAuthority) recordDecisionUnderRoomLock(ctx context.Context, binding ConsentAdmissionBinding, scope ConsentScope, disposition ConsentDisposition) (ConsentRecord, error) {
	lock := authority.bindingLock(binding)
	lock.Lock()
	defer lock.Unlock()
	release, err := authority.acquireDistributedBindingLock(ctx, binding)
	if err != nil {
		return ConsentRecord{}, err
	}
	defer release()
	now := time.Now
	if authority.Now != nil {
		now = authority.Now
	}
	record := ConsentRecord{
		ID: NewConsentRecordID(), TenantID: binding.TenantID,
		PrincipalKind: binding.PrincipalKind, PrincipalID: binding.PrincipalID,
		RoomID: binding.RoomID, SittingID: binding.SittingID,
		PolicyVersion: authority.PolicyVersion, Scopes: []ConsentScope{scope},
		Disposition: disposition, EvidenceKind: "server_authenticated_choice",
		EvidenceRef: "consent-choice-" + uuid.New().String(), RecordedAt: now().UTC().Truncate(time.Microsecond),
	}
	if disposition == ConsentWithdrawn {
		if authority.CaptureCutoff == nil {
			return ConsentRecord{}, fmt.Errorf("%w: withdrawal capture cutoff unavailable", ErrConsentAuthorityUnavailable)
		}
		cutoff, err := authority.CaptureCutoff()
		if err != nil {
			return ConsentRecord{}, fmt.Errorf("%w: withdrawal capture cutoff", ErrConsentAuthorityUnavailable)
		}
		record.LastAcceptedCaptureSequence = &cutoff
	}
	if _, err := authority.Store.Append(ctx, record); err != nil {
		return ConsentRecord{}, fmt.Errorf("%w: persist decision: %v", ErrConsentAuthorityUnavailable, err)
	}
	authority.bumpGeneration(binding)
	authority.bumpRoomGeneration(binding.TenantID, binding.RoomID, binding.SittingID)
	return record, nil
}

func consentRoomDecisionKey(tenantID, roomID, sittingID string) string {
	return strings.Join([]string{strings.TrimSpace(tenantID), normalizeRoomID(roomID), strings.TrimSpace(sittingID)}, "\x00")
}

func (authority *ConsentLaneAuthority) roomDecisionLock(tenantID, roomID, sittingID string) *sync.Mutex {
	key := consentRoomDecisionKey(tenantID, roomID, sittingID)
	authority.roomDecisionMu.Lock()
	defer authority.roomDecisionMu.Unlock()
	if authority.roomDecisionLocks == nil {
		authority.roomDecisionLocks = make(map[string]*sync.Mutex)
	}
	lock := authority.roomDecisionLocks[key]
	if lock == nil {
		lock = &sync.Mutex{}
		authority.roomDecisionLocks[key] = lock
	}
	return lock
}

func (authority *ConsentLaneAuthority) roomDecisionFence(tenantID, roomID, sittingID string) consentRoomDecisionFence {
	if authority == nil {
		return consentRoomDecisionFence{}
	}
	key := consentRoomDecisionKey(tenantID, roomID, sittingID)
	lock := authority.roomDecisionLock(tenantID, roomID, sittingID)
	lock.Lock()
	authority.mu.RLock()
	generation := authority.roomGenerations[key]
	authority.mu.RUnlock()
	lock.Unlock()
	return consentRoomDecisionFence{
		authority: authority, tenantID: strings.TrimSpace(tenantID), roomID: normalizeRoomID(roomID),
		sittingID: strings.TrimSpace(sittingID), policy: roomDecisionPolicyVersion(authority), generation: generation,
	}
}

func roomDecisionPolicyVersion(authority *ConsentLaneAuthority) string {
	if authority == nil {
		return ""
	}
	if policy := strings.TrimSpace(authority.PolicyVersion); policy != "" {
		return policy
	}
	// Room-owned tool mutation fencing is still required when durable capture
	// consent is not configured. This local generation contract authorizes no
	// capture lane; it only keeps withdrawal/epoch linearization operative.
	return "bonfire-room-decision-fence-v1"
}

// CommitWithRoomDecisionFence linearizes a room-owned side effect against
// every consent choice accepted in that room. The mutation runs while holding
// the same room lock RecordDecision takes before its durable append.
func (authority *ConsentLaneAuthority) CommitWithRoomDecisionFence(ctx context.Context, fence consentRoomDecisionFence, commit func() error) error {
	if authority == nil || commit == nil || fence.authority != authority || strings.TrimSpace(fence.policy) == "" ||
		fence.policy != roomDecisionPolicyVersion(authority) || strings.TrimSpace(fence.tenantID) == "" ||
		strings.TrimSpace(fence.roomID) == "" || strings.TrimSpace(fence.sittingID) == "" {
		return ErrConsentFenceStale
	}
	lock := authority.roomDecisionLock(fence.tenantID, fence.roomID, fence.sittingID)
	lock.Lock()
	defer lock.Unlock()
	if ctx != nil && ctx.Err() != nil {
		return ErrConsentFenceStale
	}
	key := consentRoomDecisionKey(fence.tenantID, fence.roomID, fence.sittingID)
	authority.mu.RLock()
	current := authority.roomGenerations[key]
	authority.mu.RUnlock()
	if current != fence.generation {
		return ErrConsentFenceStale
	}
	return commit()
}

func (authority *ConsentLaneAuthority) bindingLock(binding ConsentAdmissionBinding) *sync.Mutex {
	key := consentBindingKey(binding)
	authority.bindingMu.Lock()
	defer authority.bindingMu.Unlock()
	if authority.bindingLocks == nil {
		authority.bindingLocks = make(map[string]*sync.Mutex)
	}
	lock := authority.bindingLocks[key]
	if lock == nil {
		lock = &sync.Mutex{}
		authority.bindingLocks[key] = lock
	}
	return lock
}

func (authority *ConsentLaneAuthority) acquireDistributedBindingLock(ctx context.Context, binding ConsentAdmissionBinding) (func(), error) {
	store, ok := authority.Store.(*PostgresConsentStore)
	if !ok {
		return func() {}, nil
	}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, consentAuthorityLockTimeout)
		defer cancel()
	}
	return store.acquireBindingLock(ctx, consentBindingAdvisoryKey(binding, authority.PolicyVersion))
}

func (authority *ConsentLaneAuthority) acquireDistributedBindingLocks(ctx context.Context, bindings []ConsentAdmissionBinding) (func(), error) {
	store, ok := authority.Store.(*PostgresConsentStore)
	if !ok {
		return func() {}, nil
	}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, consentAuthorityLockTimeout)
		defer cancel()
	}
	keys := make([]int64, 0, len(bindings))
	for _, binding := range bindings {
		keys = append(keys, consentBindingAdvisoryKey(binding, authority.PolicyVersion))
	}
	return store.acquireBindingLocks(ctx, keys)
}

func consentBindingAdvisoryKey(binding ConsentAdmissionBinding, policy string) int64 {
	digest := sha256.Sum256([]byte("bonfire-consent\x00" + consentBindingKey(binding) + "\x00" + strings.TrimSpace(policy)))
	return int64(binary.BigEndian.Uint64(digest[:8]))
}

func consentLaneScopes(lane ConsentLane) ([]ConsentScope, bool) {
	switch lane {
	case ConsentLaneAudioCapture:
		return []ConsentScope{ConsentAudioCapture}, true
	case ConsentLaneTranscription:
		return []ConsentScope{ConsentTranscription}, true
	case ConsentLaneModelAnalysis:
		return []ConsentScope{ConsentModelAnalysis}, true
	case ConsentLaneOrgMemory:
		return []ConsentScope{ConsentOrgMemory}, true
	default:
		return nil, false
	}
}

func (authority *ConsentLaneAuthority) generation(binding ConsentAdmissionBinding) uint64 {
	authority.mu.RLock()
	defer authority.mu.RUnlock()
	return authority.generations[consentBindingKey(binding)]
}

func (authority *ConsentLaneAuthority) bumpGeneration(binding ConsentAdmissionBinding) {
	authority.mu.Lock()
	defer authority.mu.Unlock()
	authority.generations[consentBindingKey(binding)]++
}

func (authority *ConsentLaneAuthority) bumpRoomGeneration(tenantID, roomID, sittingID string) {
	authority.mu.Lock()
	defer authority.mu.Unlock()
	if authority.roomGenerations == nil {
		authority.roomGenerations = make(map[string]uint64)
	}
	authority.roomGenerations[consentRoomDecisionKey(tenantID, roomID, sittingID)]++
}

func consentBindingKey(binding ConsentAdmissionBinding) string {
	return strings.Join([]string{binding.TenantID, string(binding.PrincipalKind), binding.PrincipalID, binding.RoomID, binding.SittingID, binding.AnchorID}, "\x00")
}

func cloneConsentRecordIDs(source map[ConsentScope]string) map[ConsentScope]string {
	result := make(map[ConsentScope]string, len(source))
	for scope, id := range source {
		result[scope] = id
	}
	return result
}

func cloneConsentDispositions(source map[ConsentScope]ConsentDisposition) map[ConsentScope]ConsentDisposition {
	result := make(map[ConsentScope]ConsentDisposition, len(source))
	for scope, disposition := range source {
		result[scope] = disposition
	}
	return result
}

func consentRecordSetDigest(records map[ConsentScope]string) string {
	keys := make([]string, 0, len(records))
	for scope := range records {
		keys = append(keys, string(scope))
	}
	sort.Strings(keys)
	hash := sha256.New()
	for _, rawScope := range keys {
		hash.Write([]byte(rawScope))
		hash.Write([]byte{0})
		hash.Write([]byte(records[ConsentScope(rawScope)]))
		hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func consentAuditRecordIDs(records map[ConsentScope]string) string {
	ids := make([]string, 0, len(records))
	for _, id := range records {
		if id = strings.TrimSpace(id); id != "" {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return strings.Join(ids, ",")
}

func consentAuditRecordIDSet(records map[string]struct{}) string {
	ids := make([]string, 0, len(records))
	for id := range records {
		if id = strings.TrimSpace(id); id != "" {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return strings.Join(ids, ",")
}

var consentAuthorityRuntime struct {
	sync.Mutex
	override  *ConsentLaneAuthority
	authority *ConsentLaneAuthority
	canonical *PostgresCanonicalStore
	policy    string
}

func consentPolicyVersionFromEnvironment() string {
	return strings.TrimSpace(os.Getenv("BONFIRE_CONSENT_POLICY_VERSION"))
}

func currentConsentLaneAuthority() *ConsentLaneAuthority {
	consentAuthorityRuntime.Lock()
	defer consentAuthorityRuntime.Unlock()
	if consentAuthorityRuntime.override != nil {
		return consentAuthorityRuntime.override
	}
	runtime := currentCanonicalRuntime()
	var canonical *PostgresCanonicalStore
	if runtime != nil {
		canonical = runtime.postgres
	}
	policy := consentPolicyVersionFromEnvironment()
	if consentAuthorityRuntime.authority == nil || consentAuthorityRuntime.canonical != canonical || consentAuthorityRuntime.policy != policy {
		var store ConsentStore
		if canonical != nil {
			store = NewPostgresConsentStore(canonical)
		}
		consentAuthorityRuntime.authority = NewConsentLaneAuthority(store, policy)
		consentAuthorityRuntime.authority.CaptureCutoff = currentConsentCaptureCutoff
		consentAuthorityRuntime.authority.OnWithdrawal = handleConsentWithdrawal
		consentAuthorityRuntime.canonical = canonical
		consentAuthorityRuntime.policy = policy
	}
	return consentAuthorityRuntime.authority
}

func currentConsentCaptureCutoff() (uint64, error) {
	app := kanbanApp
	if app == nil || app.memory == nil {
		return 0, ErrConsentAuthorityUnavailable
	}
	app.memory.mu.Lock()
	defer app.memory.mu.Unlock()
	return currentDurableCaptureSequence(app.memory.path, maxPersistedCaptureSequence(app.memory.entries))
}

var consentWithdrawalRuntime = struct {
	sync.RWMutex
	nextID    uint64
	listeners map[uint64]func(ConsentWithdrawalNotice)
}{listeners: make(map[uint64]func(ConsentWithdrawalNotice))}

// handleConsentWithdrawal fans a durable withdrawal out to active provider
// lanes. Registration is runtime-only: the immutable PostgreSQL record and
// generation bump remain the authority, while listeners only accelerate
// cancellation of already queued, uncommitted work.
func handleConsentWithdrawal(notice ConsentWithdrawalNotice) {
	cancelActiveConsentProviderWork(notice)

	consentWithdrawalRuntime.RLock()
	listeners := make([]func(ConsentWithdrawalNotice), 0, len(consentWithdrawalRuntime.listeners))
	for _, listener := range consentWithdrawalRuntime.listeners {
		listeners = append(listeners, listener)
	}
	consentWithdrawalRuntime.RUnlock()
	for _, listener := range listeners {
		listener(notice)
	}
}

// cancelActiveConsentProviderWork runs before local listener fanout so a busy
// mixer or transcription queue can never delay clearing provider-owned audio,
// responses, or room tool contexts. Durable generation fencing has already
// stopped new ingress when this callback runs.
func cancelActiveConsentProviderWork(notice ConsentWithdrawalNotice) {
	if notice.Scope != ConsentAudioCapture && notice.Scope != ConsentTranscription && notice.Scope != ConsentModelAnalysis {
		return
	}
	app := kanbanApp
	if app == nil {
		return
	}
	roomID := normalizeRoomID(notice.Binding.RoomID)
	if roomID == officeRoomID {
		if meeting, ok := app.meetings.activeRecord(roomID); !ok || meeting.ID != notice.Binding.SittingID {
			return
		}
		app.cancelOfficeScoutWorkForSitting(notice.Binding.SittingID)
		clearErr := app.SendEvent(map[string]any{"type": "input_audio_buffer.clear"})
		cancelErr := app.SendEvent(map[string]any{"type": "response.cancel"})
		if clearErr != nil || cancelErr != nil {
			log.Warnf("Consent withdrawal could not clear office Realtime state; restarting provider: clear=%v cancel=%v", clearErr, cancelErr)
			go app.restartRealtimePeer("consent withdrawal provider clear failed")
		}
		return
	}
	app.mu.Lock()
	bundle := app.roomLiveLocked(roomID).realtime
	app.mu.Unlock()
	if bundle != nil && bundle.scope.SittingID == notice.Binding.SittingID {
		_ = bundle.cancelBufferedAudio()
	}
}

func subscribeConsentWithdrawals(listener func(ConsentWithdrawalNotice)) func() {
	if listener == nil {
		return func() {}
	}
	consentWithdrawalRuntime.Lock()
	consentWithdrawalRuntime.nextID++
	id := consentWithdrawalRuntime.nextID
	consentWithdrawalRuntime.listeners[id] = listener
	consentWithdrawalRuntime.Unlock()
	return func() {
		consentWithdrawalRuntime.Lock()
		delete(consentWithdrawalRuntime.listeners, id)
		consentWithdrawalRuntime.Unlock()
	}
}
