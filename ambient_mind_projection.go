package main

// E10-R2 establishes the canonical, body-free AmbientMind projection graph.
// It is deliberately shadow-only: no handler or worker reads this graph and
// the matching feature switch is permanently false in its introducing
// migration. The reducer is deterministic so PostgreSQL can rebuild the same
// current/superseded/retracted graph from immutable source events after a
// restart, correction, revocation, or purge.

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

var (
	ErrAmbientMindProjectionInvalid  = errors.New("invalid AmbientMind projection event")
	ErrAmbientMindProjectionConflict = errors.New("AmbientMind projection conflict")
	ErrAmbientMindProjectionGap      = errors.New("AmbientMind projection high-water gap")
	ErrAmbientMindProjectionDenied   = errors.New("AmbientMind projection access denied")
)

type AmbientMindProjectionKind string

const (
	AmbientMindDecision    AmbientMindProjectionKind = "decision"
	AmbientMindCommitment  AmbientMindProjectionKind = "commitment"
	AmbientMindBlocker     AmbientMindProjectionKind = "blocker"
	AmbientMindAlignment   AmbientMindProjectionKind = "alignment"
	AmbientMindStoryline   AmbientMindProjectionKind = "storyline"
	AmbientMindEntity      AmbientMindProjectionKind = "entity"
	AmbientMindArtifact    AmbientMindProjectionKind = "artifact"
	AmbientMindWorkReceipt AmbientMindProjectionKind = "work_receipt"
	AmbientMindKnownGap    AmbientMindProjectionKind = "known_gap"
)

type AmbientMindProjectionStatus string

const (
	AmbientMindProjectionCurrent    AmbientMindProjectionStatus = "current"
	AmbientMindProjectionSuperseded AmbientMindProjectionStatus = "superseded"
	AmbientMindProjectionRetracted  AmbientMindProjectionStatus = "retracted"
)

type AmbientMindProjectionOperation string

const (
	AmbientMindProjectionUpsert  AmbientMindProjectionOperation = "upsert"
	AmbientMindProjectionRevoke  AmbientMindProjectionOperation = "revoke"
	AmbientMindProjectionRetract AmbientMindProjectionOperation = "retract"
)

// AmbientMindSourceAuthority captures the exact source revision and its
// audience. It is not a grant: every read still intersects the caller with all
// live source authorities, and revoking this revision retracts its full
// downstream fan-out.
type AmbientMindSourceAuthority struct {
	Ref             STRIDEReference `json:"ref"`
	Audience        STRIDEAudience  `json:"audience"`
	ACLVersion      int64           `json:"aclVersion"`
	SourceHighWater uint64          `json:"sourceHighWater"`
	FreshThrough    time.Time       `json:"freshThrough"`
}

func (source AmbientMindSourceAuthority) Validate() error {
	if source.Ref.Validate() != nil || source.Audience.Validate() != nil || source.Audience.Visibility == "private" ||
		source.ACLVersion < 1 || source.SourceHighWater == 0 || source.FreshThrough.IsZero() {
		return ErrAmbientMindProjectionInvalid
	}
	return nil
}

// AmbientMindProjectionNode is one immutable typed graph revision. LogicalID
// selects the current revision; Ref remains the immutable revision identity.
// ParentRefs form projection-to-projection DAG edges, while SourceRefs form the
// evidence/revocation fan-out index.
type AmbientMindProjectionNode struct {
	Ref             STRIDEReference           `json:"ref"`
	LogicalID       string                    `json:"logicalId"`
	Kind            AmbientMindProjectionKind `json:"kind"`
	SourceRefs      []STRIDEReference         `json:"sourceRefs"`
	ParentRefs      []STRIDEReference         `json:"parentRefs,omitempty"`
	Audience        STRIDEAudience            `json:"audience"`
	ACLVersion      int64                     `json:"aclVersion"`
	SourceHighWater uint64                    `json:"sourceHighWater"`
	FreshThrough    time.Time                 `json:"freshThrough"`
	SupersedesRef   *STRIDEReference          `json:"supersedesRef,omitempty"`
}

func (node AmbientMindProjectionNode) Validate() error {
	if node.Ref.Validate() != nil || !oneOf(string(node.Ref.ContractType), string(STRIDEContractAnalysisProjection), string(STRIDEContractKnowledgeAssertion)) ||
		!strideIdentifier(node.LogicalID) || !oneOf(string(node.Kind), string(AmbientMindDecision), string(AmbientMindCommitment), string(AmbientMindBlocker), string(AmbientMindAlignment), string(AmbientMindStoryline), string(AmbientMindEntity), string(AmbientMindArtifact), string(AmbientMindWorkReceipt), string(AmbientMindKnownGap)) ||
		len(node.SourceRefs) == 0 || !validateSTRIDERefs(node.SourceRefs) || !validateOptionalSTRIDERefs(node.ParentRefs) || node.Audience.Validate() != nil || node.Audience.Visibility == "private" ||
		node.ACLVersion < 1 || node.SourceHighWater == 0 || node.FreshThrough.IsZero() {
		return ErrAmbientMindProjectionInvalid
	}
	for _, parent := range node.ParentRefs {
		if !oneOf(string(parent.ContractType), string(STRIDEContractAnalysisProjection), string(STRIDEContractKnowledgeAssertion)) || parent == node.Ref {
			return ErrAmbientMindProjectionInvalid
		}
	}
	if node.SupersedesRef != nil {
		if node.SupersedesRef.Validate() != nil || !oneOf(string(node.SupersedesRef.ContractType), string(STRIDEContractAnalysisProjection), string(STRIDEContractKnowledgeAssertion)) || *node.SupersedesRef == node.Ref {
			return ErrAmbientMindProjectionInvalid
		}
	}
	return nil
}

type AmbientMindProjectionEvent struct {
	TenantID        string                         `json:"tenantId"`
	EventID         string                         `json:"eventId"`
	IdempotencyKey  string                         `json:"idempotencyKey"`
	Sequence        uint64                         `json:"sequence"`
	SourceHighWater uint64                         `json:"sourceHighWater"`
	Operation       AmbientMindProjectionOperation `json:"operation"`
	Node            *AmbientMindProjectionNode     `json:"node,omitempty"`
	Sources         []AmbientMindSourceAuthority   `json:"sources,omitempty"`
	TargetRef       *STRIDEReference               `json:"targetRef,omitempty"`
	Reason          string                         `json:"reason,omitempty"`
	OccurredAt      time.Time                      `json:"occurredAt"`
}

func (event AmbientMindProjectionEvent) Validate() error {
	if !strideIdentifier(event.TenantID) || !strideIdentifier(event.EventID) || !strideIdentifier(event.IdempotencyKey) ||
		event.Sequence == 0 || event.SourceHighWater == 0 || event.OccurredAt.IsZero() ||
		!oneOf(string(event.Operation), string(AmbientMindProjectionUpsert), string(AmbientMindProjectionRevoke), string(AmbientMindProjectionRetract)) {
		return ErrAmbientMindProjectionInvalid
	}
	switch event.Operation {
	case AmbientMindProjectionUpsert:
		if event.Node == nil || event.Node.Validate() != nil || event.TargetRef != nil || event.Reason != "" || len(event.Sources) == 0 || event.Node.SourceHighWater != event.SourceHighWater {
			return ErrAmbientMindProjectionInvalid
		}
		seen := map[string]struct{}{}
		for _, source := range event.Sources {
			if source.Validate() != nil || source.SourceHighWater > event.SourceHighWater {
				return ErrAmbientMindProjectionInvalid
			}
			key := strideConversationReferenceKey(source.Ref)
			if _, duplicate := seen[key]; duplicate {
				return ErrAmbientMindProjectionInvalid
			}
			seen[key] = struct{}{}
			if !ambientMindAudienceSubset(event.Node.Audience, source.Audience) || event.Node.FreshThrough.After(source.FreshThrough) {
				return ErrAmbientMindProjectionDenied
			}
		}
		for _, ref := range event.Node.SourceRefs {
			if _, ok := seen[strideConversationReferenceKey(ref)]; !ok {
				return ErrAmbientMindProjectionInvalid
			}
		}
		if len(seen) != len(event.Node.SourceRefs) {
			return ErrAmbientMindProjectionInvalid
		}
	case AmbientMindProjectionRevoke, AmbientMindProjectionRetract:
		if event.Node != nil || len(event.Sources) != 0 || event.TargetRef == nil || event.TargetRef.Validate() != nil || !strideIdentifier(event.Reason) {
			return ErrAmbientMindProjectionInvalid
		}
	}
	return nil
}

type AmbientMindProjectionNodeState struct {
	TenantID string                      `json:"tenantId"`
	Node     AmbientMindProjectionNode   `json:"node"`
	Status   AmbientMindProjectionStatus `json:"status"`
	Reason   string                      `json:"reason,omitempty"`
}

type AmbientMindProjectionCheckpoint struct {
	Generation           uint64    `json:"generation"`
	ThroughSequence      uint64    `json:"throughSequence"`
	SourceHighWater      uint64    `json:"sourceHighWater"`
	ProjectionHighWater  uint64    `json:"projectionHighWater"`
	FreshThrough         time.Time `json:"freshThrough"`
	SourceManifestDigest string    `json:"sourceManifestDigest"`
	ProjectionDigest     string    `json:"projectionDigest"`
}

type AmbientMindProjectionSnapshot struct {
	Format     int                              `json:"format"`
	Events     []AmbientMindProjectionEvent     `json:"events"`
	Sources    []AmbientMindSourceAuthority     `json:"sources"`
	Nodes      []AmbientMindProjectionNodeState `json:"nodes"`
	Checkpoint AmbientMindProjectionCheckpoint  `json:"checkpoint"`
	Digest     string                           `json:"digest"`
}

type AmbientMindProjector struct {
	mu         sync.RWMutex
	tenantID   string
	events     []AmbientMindProjectionEvent
	idempotent map[string]string
	snapshot   AmbientMindProjectionSnapshot
}

func NewAmbientMindProjector() *AmbientMindProjector {
	return &AmbientMindProjector{idempotent: map[string]string{}, snapshot: AmbientMindProjectionSnapshot{Format: 1}}
}

func (projector *AmbientMindProjector) Apply(event AmbientMindProjectionEvent) (AmbientMindProjectionCheckpoint, error) {
	if projector == nil || event.Validate() != nil {
		return AmbientMindProjectionCheckpoint{}, ErrAmbientMindProjectionInvalid
	}
	fingerprint, err := STRIDEContractDigest(event)
	if err != nil {
		return AmbientMindProjectionCheckpoint{}, err
	}
	projector.mu.Lock()
	defer projector.mu.Unlock()
	if projector.tenantID != "" && projector.tenantID != event.TenantID {
		return AmbientMindProjectionCheckpoint{}, ErrAmbientMindProjectionDenied
	}
	idempotencyKey := event.TenantID + "\x00" + event.IdempotencyKey
	if prior, ok := projector.idempotent[idempotencyKey]; ok {
		if prior != fingerprint {
			return AmbientMindProjectionCheckpoint{}, ErrAmbientMindProjectionConflict
		}
		return projector.snapshot.Checkpoint, nil
	}
	wantSequence := projector.snapshot.Checkpoint.ThroughSequence + 1
	if event.Sequence != wantSequence || event.SourceHighWater < projector.snapshot.Checkpoint.SourceHighWater {
		return AmbientMindProjectionCheckpoint{}, ErrAmbientMindProjectionGap
	}
	events := append(cloneAmbientMindEvents(projector.events), cloneAmbientMindEvent(event))
	next, err := rebuildAmbientMindProjection(events, projector.snapshot.Checkpoint.Generation)
	if err != nil {
		return AmbientMindProjectionCheckpoint{}, err
	}
	projector.events = events
	projector.tenantID = event.TenantID
	projector.idempotent[idempotencyKey] = fingerprint
	projector.snapshot = next
	return next.Checkpoint, nil
}

func (projector *AmbientMindProjector) Rebuild() (AmbientMindProjectionCheckpoint, error) {
	if projector == nil {
		return AmbientMindProjectionCheckpoint{}, ErrAmbientMindProjectionInvalid
	}
	projector.mu.Lock()
	defer projector.mu.Unlock()
	next, err := rebuildAmbientMindProjection(projector.events, projector.snapshot.Checkpoint.Generation+1)
	if err != nil {
		return AmbientMindProjectionCheckpoint{}, err
	}
	projector.snapshot = next
	return next.Checkpoint, nil
}

func (projector *AmbientMindProjector) Snapshot() AmbientMindProjectionSnapshot {
	if projector == nil {
		return AmbientMindProjectionSnapshot{}
	}
	projector.mu.RLock()
	defer projector.mu.RUnlock()
	return cloneAmbientMindSnapshot(projector.snapshot)
}

func RestoreAmbientMindProjector(snapshot AmbientMindProjectionSnapshot) (*AmbientMindProjector, error) {
	if snapshot.Format != 1 || !isHexDigest(snapshot.Digest) {
		return nil, ErrAmbientMindProjectionInvalid
	}
	rebuilt, err := rebuildAmbientMindProjection(snapshot.Events, snapshot.Checkpoint.Generation)
	if err != nil || rebuilt.Digest != snapshot.Digest || rebuilt.Checkpoint != snapshot.Checkpoint {
		return nil, ErrAmbientMindProjectionConflict
	}
	projector := NewAmbientMindProjector()
	projector.events = cloneAmbientMindEvents(snapshot.Events)
	projector.snapshot = rebuilt
	for _, event := range projector.events {
		if projector.tenantID != "" && projector.tenantID != event.TenantID {
			return nil, ErrAmbientMindProjectionConflict
		}
		projector.tenantID = event.TenantID
		digest, _ := STRIDEContractDigest(event)
		projector.idempotent[event.TenantID+"\x00"+event.IdempotencyKey] = digest
	}
	return projector, nil
}

func (projector *AmbientMindProjector) QueryForPrincipal(tenantID, principal string) ([]AmbientMindProjectionNode, AmbientMindProjectionCheckpoint, error) {
	if projector == nil || !strideIdentifier(tenantID) || !strideIdentifier(principal) {
		return nil, AmbientMindProjectionCheckpoint{}, ErrAmbientMindProjectionDenied
	}
	projector.mu.RLock()
	defer projector.mu.RUnlock()
	sourceByRef := map[string]AmbientMindSourceAuthority{}
	for _, source := range projector.snapshot.Sources {
		sourceByRef[strideConversationReferenceKey(source.Ref)] = source
	}
	result := []AmbientMindProjectionNode{}
	for _, state := range projector.snapshot.Nodes {
		if state.TenantID != tenantID || state.Status != AmbientMindProjectionCurrent || !containsSTRIDEID(state.Node.Audience.Principals, principal) {
			continue
		}
		authorized := true
		for _, sourceRef := range state.Node.SourceRefs {
			source, ok := sourceByRef[strideConversationReferenceKey(sourceRef)]
			if !ok || !containsSTRIDEID(source.Audience.Principals, principal) {
				authorized = false
				break
			}
		}
		if authorized {
			result = append(result, cloneAmbientMindNode(state.Node))
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].LogicalID < result[j].LogicalID })
	return result, projector.snapshot.Checkpoint, nil
}

func rebuildAmbientMindProjection(events []AmbientMindProjectionEvent, generation uint64) (AmbientMindProjectionSnapshot, error) {
	snapshot := AmbientMindProjectionSnapshot{Format: 1, Events: cloneAmbientMindEvents(events)}
	sources := map[string]AmbientMindSourceAuthority{}
	nodes := map[string]AmbientMindProjectionNodeState{}
	current := map[string]string{}
	children := map[string][]string{}
	var sourceHighWater uint64
	seenEvents := map[string]string{}
	seenIdempotency := map[string]string{}
	for index, event := range events {
		if event.Validate() != nil || event.Sequence != uint64(index+1) || event.SourceHighWater < sourceHighWater {
			return AmbientMindProjectionSnapshot{}, ErrAmbientMindProjectionGap
		}
		digest, _ := STRIDEContractDigest(event)
		eventKey := event.TenantID + "\x00" + event.EventID
		idempotencyKey := event.TenantID + "\x00" + event.IdempotencyKey
		if prior := seenEvents[eventKey]; prior != "" && prior != digest {
			return AmbientMindProjectionSnapshot{}, ErrAmbientMindProjectionConflict
		}
		if prior := seenIdempotency[idempotencyKey]; prior != "" && prior != digest {
			return AmbientMindProjectionSnapshot{}, ErrAmbientMindProjectionConflict
		}
		seenEvents[eventKey], seenIdempotency[idempotencyKey] = digest, digest
		sourceHighWater = event.SourceHighWater
		switch event.Operation {
		case AmbientMindProjectionUpsert:
			node := cloneAmbientMindNode(*event.Node)
			for _, source := range event.Sources {
				key := event.TenantID + "\x00" + strideConversationReferenceKey(source.Ref)
				if prior, ok := sources[key]; ok && !ambientMindSourcesEqual(prior, source) {
					return AmbientMindProjectionSnapshot{}, ErrAmbientMindProjectionConflict
				}
				sources[key] = cloneAmbientMindSource(source)
			}
			nodeKey := event.TenantID + "\x00" + strideConversationReferenceKey(node.Ref)
			logicalKey := event.TenantID + "\x00" + node.LogicalID
			if _, exists := nodes[nodeKey]; exists {
				return AmbientMindProjectionSnapshot{}, ErrAmbientMindProjectionConflict
			}
			if priorKey := current[logicalKey]; priorKey != "" {
				prior := nodes[priorKey]
				if node.SupersedesRef == nil || strideConversationReferenceKey(*node.SupersedesRef) != strideConversationReferenceKey(prior.Node.Ref) {
					return AmbientMindProjectionSnapshot{}, ErrAmbientMindProjectionConflict
				}
				prior.Status, prior.Reason = AmbientMindProjectionSuperseded, "superseded"
				nodes[priorKey] = prior
				ambientMindRetractDescendants(priorKey, "superseded_source", nodes, children)
			} else if node.SupersedesRef != nil {
				return AmbientMindProjectionSnapshot{}, ErrAmbientMindProjectionConflict
			}
			for _, parent := range node.ParentRefs {
				parentKey := event.TenantID + "\x00" + strideConversationReferenceKey(parent)
				parentState, ok := nodes[parentKey]
				if !ok || parentState.Status != AmbientMindProjectionCurrent {
					return AmbientMindProjectionSnapshot{}, ErrAmbientMindProjectionConflict
				}
				children[parentKey] = append(children[parentKey], nodeKey)
			}
			nodes[nodeKey] = AmbientMindProjectionNodeState{TenantID: event.TenantID, Node: node, Status: AmbientMindProjectionCurrent}
			current[logicalKey] = nodeKey
		case AmbientMindProjectionRevoke, AmbientMindProjectionRetract:
			targetKey := event.TenantID + "\x00" + strideConversationReferenceKey(*event.TargetRef)
			if state, ok := nodes[targetKey]; ok {
				state.Status, state.Reason = AmbientMindProjectionRetracted, event.Reason
				nodes[targetKey] = state
				logicalKey := event.TenantID + "\x00" + state.Node.LogicalID
				if current[logicalKey] == targetKey {
					delete(current, logicalKey)
				}
				ambientMindRetractDescendants(targetKey, event.Reason, nodes, children)
				continue
			}
			found := false
			for nodeKey, state := range nodes {
				if state.TenantID != event.TenantID || state.Status == AmbientMindProjectionRetracted {
					continue
				}
				for _, sourceRef := range state.Node.SourceRefs {
					if strideConversationReferenceKey(sourceRef) == strideConversationReferenceKey(*event.TargetRef) {
						found = true
						state.Status, state.Reason = AmbientMindProjectionRetracted, event.Reason
						nodes[nodeKey] = state
						logicalKey := event.TenantID + "\x00" + state.Node.LogicalID
						if current[logicalKey] == nodeKey {
							delete(current, logicalKey)
						}
						ambientMindRetractDescendants(nodeKey, event.Reason, nodes, children)
						break
					}
				}
			}
			if !found {
				return AmbientMindProjectionSnapshot{}, ErrAmbientMindProjectionConflict
			}
		}
	}
	for _, source := range sources {
		snapshot.Sources = append(snapshot.Sources, cloneAmbientMindSource(source))
	}
	sort.Slice(snapshot.Sources, func(i, j int) bool {
		return strideConversationReferenceKey(snapshot.Sources[i].Ref) < strideConversationReferenceKey(snapshot.Sources[j].Ref)
	})
	for _, state := range nodes {
		snapshot.Nodes = append(snapshot.Nodes, cloneAmbientMindNodeState(state))
	}
	sort.Slice(snapshot.Nodes, func(i, j int) bool {
		return strideConversationReferenceKey(snapshot.Nodes[i].Node.Ref) < strideConversationReferenceKey(snapshot.Nodes[j].Node.Ref)
	})
	sourceDigest, _ := STRIDEContractDigest(snapshot.Sources)
	projectionDigest, _ := STRIDEContractDigest(snapshot.Nodes)
	var freshThrough time.Time
	for _, state := range snapshot.Nodes {
		if state.Status == AmbientMindProjectionCurrent && (freshThrough.IsZero() || state.Node.FreshThrough.Before(freshThrough)) {
			freshThrough = state.Node.FreshThrough
		}
	}
	snapshot.Checkpoint = AmbientMindProjectionCheckpoint{Generation: generation, ThroughSequence: uint64(len(events)), SourceHighWater: sourceHighWater,
		ProjectionHighWater: uint64(len(events)), FreshThrough: freshThrough, SourceManifestDigest: sourceDigest, ProjectionDigest: projectionDigest}
	digestInput := snapshot
	digestInput.Digest = ""
	digestInput.Checkpoint.Generation = 0
	snapshot.Digest, _ = STRIDEContractDigest(digestInput)
	return snapshot, nil
}

func ambientMindRetractDescendants(key, reason string, nodes map[string]AmbientMindProjectionNodeState, children map[string][]string) {
	for _, childKey := range children[key] {
		child, ok := nodes[childKey]
		if !ok || child.Status == AmbientMindProjectionRetracted {
			continue
		}
		child.Status, child.Reason = AmbientMindProjectionRetracted, reason
		nodes[childKey] = child
		ambientMindRetractDescendants(childKey, reason, nodes, children)
	}
}

func ambientMindAudienceSubset(candidate, source STRIDEAudience) bool {
	if candidate.Visibility == "organization" && source.Visibility != "organization" {
		return false
	}
	for _, principal := range candidate.Principals {
		if !containsSTRIDEID(source.Principals, principal) {
			return false
		}
	}
	return true
}

func ambientMindSourcesEqual(left, right AmbientMindSourceAuthority) bool {
	left.Audience.Principals = sortedUniqueSTRIDEIDs(left.Audience.Principals)
	right.Audience.Principals = sortedUniqueSTRIDEIDs(right.Audience.Principals)
	return fmt.Sprintf("%#v", left) == fmt.Sprintf("%#v", right)
}

func cloneAmbientMindSource(value AmbientMindSourceAuthority) AmbientMindSourceAuthority {
	value.Audience.Principals = append([]string(nil), value.Audience.Principals...)
	sort.Strings(value.Audience.Principals)
	return value
}

func cloneAmbientMindNode(value AmbientMindProjectionNode) AmbientMindProjectionNode {
	value.SourceRefs = SortedSTRIDEReferences(value.SourceRefs)
	value.ParentRefs = SortedSTRIDEReferences(value.ParentRefs)
	value.Audience.Principals = append([]string(nil), value.Audience.Principals...)
	sort.Strings(value.Audience.Principals)
	if value.SupersedesRef != nil {
		ref := *value.SupersedesRef
		value.SupersedesRef = &ref
	}
	return value
}

func cloneAmbientMindEvent(value AmbientMindProjectionEvent) AmbientMindProjectionEvent {
	if value.Node != nil {
		node := cloneAmbientMindNode(*value.Node)
		value.Node = &node
	}
	value.Sources = append([]AmbientMindSourceAuthority(nil), value.Sources...)
	for index := range value.Sources {
		value.Sources[index] = cloneAmbientMindSource(value.Sources[index])
	}
	if value.TargetRef != nil {
		ref := *value.TargetRef
		value.TargetRef = &ref
	}
	return value
}

func cloneAmbientMindEvents(values []AmbientMindProjectionEvent) []AmbientMindProjectionEvent {
	result := make([]AmbientMindProjectionEvent, len(values))
	for index, value := range values {
		result[index] = cloneAmbientMindEvent(value)
	}
	return result
}

func cloneAmbientMindNodeState(value AmbientMindProjectionNodeState) AmbientMindProjectionNodeState {
	value.Node = cloneAmbientMindNode(value.Node)
	return value
}

func cloneAmbientMindSnapshot(value AmbientMindProjectionSnapshot) AmbientMindProjectionSnapshot {
	value.Events = cloneAmbientMindEvents(value.Events)
	value.Sources = append([]AmbientMindSourceAuthority(nil), value.Sources...)
	for index := range value.Sources {
		value.Sources[index] = cloneAmbientMindSource(value.Sources[index])
	}
	value.Nodes = append([]AmbientMindProjectionNodeState(nil), value.Nodes...)
	for index := range value.Nodes {
		value.Nodes[index] = cloneAmbientMindNodeState(value.Nodes[index])
	}
	return value
}
