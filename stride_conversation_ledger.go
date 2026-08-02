package main

// The E1 conversation ledger is a deliberately local, default-off reducer.
// It does not replace chat persistence or expose a read path; it establishes
// deterministic event identity, projection, invalidation, and restore rules
// before any handler is allowed to make public conversation recallable.

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

var (
	ErrSTRIDEConversationInvalid  = errors.New("invalid STRIDE conversation event")
	ErrSTRIDEConversationConflict = errors.New("STRIDE conversation idempotency conflict")
	ErrSTRIDEConversationUnknown  = errors.New("unknown STRIDE conversation event")
	ErrSTRIDEConversationDenied   = errors.New("STRIDE conversation recall denied")
	ErrSTRIDEConversationSnapshot = errors.New("invalid STRIDE conversation snapshot")
)

type STRIDEConversationLedgerConfig struct {
	// RecallThreadIDs is the explicit projection allowlist. An empty allowlist
	// means every conversation remains non-recallable. In E1 callers should
	// supply only the permanent #team/project/meeting thread identities.
	RecallThreadIDs []string
}

type STRIDEConversationAppend struct {
	Event          ConversationEvent `json:"event"`
	IdempotencyKey string            `json:"idempotencyKey"`
	// PrivateShare fields reserve the future schema for an explicitly designed,
	// atomic share grant. The current E1-E9 ledger rejects every private_share
	// append: syntactically valid references are not authority, and no partial
	// callback protocol is allowed to create a revocation race.
	PrivateShareSource        *STRIDEReference `json:"privateShareSource,omitempty"`
	PrivateShareAuthorization *STRIDEReference `json:"privateShareAuthorization,omitempty"`
}

func (append STRIDEConversationAppend) Validate() error {
	if append.Event.Validate() != nil || !strideIdentifier(append.IdempotencyKey) {
		return ErrSTRIDEConversationInvalid
	}
	privateShare := append.PrivateShareSource != nil || append.PrivateShareAuthorization != nil
	if privateShare {
		if append.PrivateShareSource == nil || append.PrivateShareAuthorization == nil || append.PrivateShareSource.Validate() != nil || append.PrivateShareAuthorization.Validate() != nil ||
			append.Event.Audience.Visibility == "private" || append.Event.SourceType != "private_share" {
			return ErrSTRIDEConversationInvalid
		}
	} else if append.Event.SourceType == "private_share" {
		return ErrSTRIDEConversationInvalid
	}
	return nil
}

type STRIDEConversationEventRecord struct {
	Sequence           uint64                   `json:"sequence"`
	Append             STRIDEConversationAppend `json:"append"`
	RecallEligible     bool                     `json:"recallEligible"`
	Invalidated        bool                     `json:"invalidated"`
	InvalidationReason string                   `json:"invalidationReason,omitempty"`
}

type STRIDEConversationMessageProjection struct {
	TenantID        string            `json:"tenantId"`
	SourceType      string            `json:"sourceType"`
	SourceID        string            `json:"sourceId"`
	ThreadID        string            `json:"threadId"`
	AuthorPrincipal string            `json:"authorPrincipal"`
	AuthorName      string            `json:"authorName"`
	ReplyToEventID  string            `json:"replyToEventId,omitempty"`
	ReactionActors  []string          `json:"reactionActors,omitempty"`
	AttachmentRefs  []STRIDEReference `json:"attachmentRefs,omitempty"`
	LinkRefs        []STRIDEReference `json:"linkRefs,omitempty"`
	LatestEvent     STRIDEReference   `json:"latestEvent"`
	Audience        STRIDEAudience    `json:"audience"`
	ACLVersion      int64             `json:"aclVersion"`
	RetentionPolicy string            `json:"retentionPolicy"`
	PurgeGeneration int64             `json:"purgeGeneration"`
	Retracted       bool              `json:"retracted"`
	RecallEligible  bool              `json:"recallEligible"`
}

type STRIDEDerivedLane string

const (
	STRIDEDerivedKnowledge   STRIDEDerivedLane = "knowledge"
	STRIDEDerivedPreference  STRIDEDerivedLane = "preference"
	STRIDEDerivedLearning    STRIDEDerivedLane = "learning"
	STRIDEDerivedPerformance STRIDEDerivedLane = "performance"
	STRIDEDerivedAssignment  STRIDEDerivedLane = "assignment"
	STRIDEDerivedAnswer      STRIDEDerivedLane = "answer"
	STRIDEDerivedWork        STRIDEDerivedLane = "work"
)

type STRIDESourceDerivedEdge struct {
	TenantID string            `json:"tenantId"`
	Source   STRIDEReference   `json:"source"`
	Derived  STRIDEReference   `json:"derived"`
	Lane     STRIDEDerivedLane `json:"lane"`
}

func (edge STRIDESourceDerivedEdge) Validate() error {
	if !strideIdentifier(edge.TenantID) || edge.Source.Validate() != nil || edge.Derived.Validate() != nil || !oneOf(string(edge.Lane), string(STRIDEDerivedKnowledge), string(STRIDEDerivedPreference), string(STRIDEDerivedLearning), string(STRIDEDerivedPerformance), string(STRIDEDerivedAssignment), string(STRIDEDerivedAnswer), string(STRIDEDerivedWork)) {
		return ErrSTRIDEConversationInvalid
	}
	return nil
}

type STRIDEConversationInvalidation struct {
	TenantID  string          `json:"tenantId"`
	Reference STRIDEReference `json:"reference"`
	Reason    string          `json:"reason"`
	At        time.Time       `json:"at"`
}

type STRIDEConversationCheckpoint struct {
	Generation uint64    `json:"generation"`
	HighWater  uint64    `json:"highWater"`
	Checksum   string    `json:"checksum"`
	CreatedAt  time.Time `json:"createdAt"`
}

type STRIDEConversationSnapshot struct {
	Format        int                              `json:"format"`
	Events        []STRIDEConversationEventRecord  `json:"events"`
	Edges         []STRIDESourceDerivedEdge        `json:"edges"`
	Invalidations []STRIDEConversationInvalidation `json:"invalidations"`
	Checkpoint    STRIDEConversationCheckpoint     `json:"checkpoint"`
	Digest        string                           `json:"digest"`
}

type STRIDEConversationAppendResult struct {
	Record   STRIDEConversationEventRecord `json:"record"`
	Existing bool                          `json:"existing"`
}

type STRIDEConversationLedger struct {
	mu           sync.RWMutex
	config       STRIDEConversationLedgerConfig
	events       map[string]STRIDEConversationEventRecord
	idempotency  map[string]string
	sourceEvents map[string][]string
	edges        map[string]STRIDESourceDerivedEdge
	invalidated  map[string]STRIDEConversationInvalidation
	checkpoint   STRIDEConversationCheckpoint
}

func NewSTRIDEConversationLedger(config STRIDEConversationLedgerConfig) (*STRIDEConversationLedger, error) {
	if len(config.RecallThreadIDs) > 0 && !uniqueSTRIDEIDs(config.RecallThreadIDs) {
		return nil, ErrSTRIDEConversationInvalid
	}
	config.RecallThreadIDs = append([]string(nil), config.RecallThreadIDs...)
	sort.Strings(config.RecallThreadIDs)
	return &STRIDEConversationLedger{
		config: config, events: make(map[string]STRIDEConversationEventRecord), idempotency: make(map[string]string), sourceEvents: make(map[string][]string),
		edges: make(map[string]STRIDESourceDerivedEdge), invalidated: make(map[string]STRIDEConversationInvalidation),
	}, nil
}

func (ledger *STRIDEConversationLedger) Append(input STRIDEConversationAppend) (STRIDEConversationAppendResult, error) {
	if ledger == nil || input.Validate() != nil {
		return STRIDEConversationAppendResult{}, ErrSTRIDEConversationInvalid
	}
	if input.Event.SourceType == "private_share" {
		return STRIDEConversationAppendResult{}, ErrSTRIDEConversationDenied
	}
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	return ledger.appendLocked(input)
}

func (ledger *STRIDEConversationLedger) appendLocked(input STRIDEConversationAppend) (STRIDEConversationAppendResult, error) {
	idempotency := input.Event.Header.TenantID + "\x00" + input.IdempotencyKey
	fingerprint, err := STRIDEContractDigest(input)
	if err != nil {
		return STRIDEConversationAppendResult{}, err
	}
	if eventID, found := ledger.idempotency[idempotency]; found {
		existing := ledger.events[eventID]
		existingFingerprint, digestErr := STRIDEContractDigest(existing.Append)
		if digestErr != nil || existingFingerprint != fingerprint {
			return STRIDEConversationAppendResult{}, ErrSTRIDEConversationConflict
		}
		return STRIDEConversationAppendResult{Record: cloneSTRIDEConversationRecord(existing), Existing: true}, nil
	}
	eventID := input.Event.Header.ID
	eventKey := strideConversationEventKey(input.Event.Header.TenantID, eventID)
	if existing, found := ledger.events[eventKey]; found {
		existingFingerprint, digestErr := STRIDEContractDigest(existing.Append)
		if digestErr != nil || existingFingerprint != fingerprint {
			return STRIDEConversationAppendResult{}, ErrSTRIDEConversationConflict
		}
		return STRIDEConversationAppendResult{Record: cloneSTRIDEConversationRecord(existing), Existing: true}, nil
	}
	if input.Event.EventType != "message" && !ledger.hasEarlierSourceEventLocked(input.Event) {
		return STRIDEConversationAppendResult{}, ErrSTRIDEConversationInvalid
	}
	if input.Event.SupersedesEventID != "" {
		supersededKey := strideConversationEventKey(input.Event.Header.TenantID, input.Event.SupersedesEventID)
		superseded, found := ledger.events[supersededKey]
		if !found {
			return STRIDEConversationAppendResult{}, ErrSTRIDEConversationUnknown
		}
		if superseded.Append.Event.SourceType != input.Event.SourceType || superseded.Append.Event.SourceID != input.Event.SourceID {
			return STRIDEConversationAppendResult{}, ErrSTRIDEConversationInvalid
		}
		ledger.invalidateConversationEventKeyLocked(supersededKey, "superseded")
	}
	if input.Event.ReplyToEventID != "" {
		reply, found := ledger.events[strideConversationEventKey(input.Event.Header.TenantID, input.Event.ReplyToEventID)]
		if !found {
			return STRIDEConversationAppendResult{}, ErrSTRIDEConversationUnknown
		}
		if reply.Append.Event.ThreadID != input.Event.ThreadID {
			return STRIDEConversationAppendResult{}, ErrSTRIDEConversationInvalid
		}
	}
	if oneOf(input.Event.EventType, "edit", "delete") {
		ledger.invalidateSourceLocked(input.Event.Header.TenantID, input.Event.SourceType, input.Event.SourceID, input.Event.EventType)
	}
	sequence := ledger.checkpoint.HighWater + 1
	record := STRIDEConversationEventRecord{Sequence: sequence, Append: cloneSTRIDEConversationAppend(input), RecallEligible: ledger.recallEligibleLocked(input)}
	ledger.events[eventKey] = record
	ledger.idempotency[idempotency] = eventKey
	sourceKey := strideConversationSourceKey(input.Event.Header.TenantID, input.Event.SourceType, input.Event.SourceID)
	ledger.sourceEvents[sourceKey] = append(ledger.sourceEvents[sourceKey], eventKey)
	ledger.rebuildCheckpointLocked(ledger.checkpoint.Generation)
	return STRIDEConversationAppendResult{Record: cloneSTRIDEConversationRecord(record)}, nil
}

func (ledger *STRIDEConversationLedger) AddDerivedEdge(edge STRIDESourceDerivedEdge) error {
	if ledger == nil || edge.Validate() != nil {
		return ErrSTRIDEConversationInvalid
	}
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	if edge.Source.ContractType == STRIDEContractConversationEvent {
		if _, found := ledger.events[strideConversationEventKey(edge.TenantID, edge.Source.ID)]; !found {
			return ErrSTRIDEConversationUnknown
		}
	}
	key := strideConversationEdgeKey(edge)
	if existing, found := ledger.edges[key]; found {
		if existing != edge {
			return ErrSTRIDEConversationConflict
		}
		return nil
	}
	ledger.edges[key] = edge
	if invalidation, invalid := ledger.invalidated[strideConversationTenantReferenceKey(edge.TenantID, edge.Source)]; invalid {
		ledger.invalidateDerivedLocked(edge.TenantID, edge.Derived, invalidation.Reason)
	}
	ledger.rebuildCheckpointLocked(ledger.checkpoint.Generation)
	return nil
}

func (ledger *STRIDEConversationLedger) Invalidate(reference STRIDEReference, reason string) error {
	if ledger == nil || reference.Validate() != nil || !strideIdentifier(reason) {
		return ErrSTRIDEConversationInvalid
	}
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	tenantID, ok := ledger.soleTenantLocked()
	if !ok {
		return ErrSTRIDEConversationDenied
	}
	return ledger.invalidateForTenantLocked(tenantID, reference, reason)
}

func (ledger *STRIDEConversationLedger) InvalidateForTenant(tenantID string, reference STRIDEReference, reason string) error {
	if ledger == nil || !strideIdentifier(tenantID) || reference.Validate() != nil || !strideIdentifier(reason) {
		return ErrSTRIDEConversationInvalid
	}
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	return ledger.invalidateForTenantLocked(tenantID, reference, reason)
}

func (ledger *STRIDEConversationLedger) invalidateForTenantLocked(tenantID string, reference STRIDEReference, reason string) error {
	if reference.ContractType == STRIDEContractConversationEvent {
		if _, found := ledger.events[strideConversationEventKey(tenantID, reference.ID)]; !found {
			return ErrSTRIDEConversationUnknown
		}
	}
	ledger.invalidateDerivedLocked(tenantID, reference, reason)
	ledger.rebuildCheckpointLocked(ledger.checkpoint.Generation)
	return nil
}

func (ledger *STRIDEConversationLedger) Rebuild() (STRIDEConversationCheckpoint, error) {
	if ledger == nil {
		return STRIDEConversationCheckpoint{}, ErrSTRIDEConversationInvalid
	}
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	// The state is already append-only. Recomputing the checksum from a sorted
	// snapshot is the replay proof; no transient map/clock participates.
	ledger.rebuildCheckpointLocked(ledger.checkpoint.Generation + 1)
	return ledger.checkpoint, nil
}

func (ledger *STRIDEConversationLedger) Snapshot() (STRIDEConversationSnapshot, error) {
	if ledger == nil {
		return STRIDEConversationSnapshot{}, ErrSTRIDEConversationInvalid
	}
	ledger.mu.RLock()
	defer ledger.mu.RUnlock()
	snapshot := ledger.snapshotLocked()
	digest, err := strideConversationSnapshotDigest(snapshot)
	if err != nil {
		return STRIDEConversationSnapshot{}, err
	}
	snapshot.Digest = digest
	return snapshot, nil
}

func RestoreSTRIDEConversationLedger(config STRIDEConversationLedgerConfig, snapshot STRIDEConversationSnapshot) (*STRIDEConversationLedger, error) {
	if snapshot.Format != 1 || !isHexDigest(snapshot.Digest) {
		return nil, fmt.Errorf("%w: header", ErrSTRIDEConversationSnapshot)
	}
	want, err := strideConversationSnapshotDigest(snapshot)
	if err != nil || want != snapshot.Digest {
		return nil, fmt.Errorf("%w: digest", ErrSTRIDEConversationSnapshot)
	}
	ledger, err := NewSTRIDEConversationLedger(config)
	if err != nil {
		return nil, err
	}
	for _, record := range snapshot.Events {
		if record.Sequence == 0 || record.Append.Validate() != nil {
			return nil, fmt.Errorf("%w: event", ErrSTRIDEConversationSnapshot)
		}
		result, appendErr := ledger.Append(record.Append)
		if appendErr != nil || result.Existing || result.Record.Sequence != record.Sequence {
			return nil, fmt.Errorf("%w: append", ErrSTRIDEConversationSnapshot)
		}
	}
	for _, edge := range snapshot.Edges {
		if err := ledger.AddDerivedEdge(edge); err != nil {
			return nil, fmt.Errorf("%w: edge", ErrSTRIDEConversationSnapshot)
		}
	}
	for _, invalidation := range snapshot.Invalidations {
		if err := ledger.InvalidateForTenant(invalidation.TenantID, invalidation.Reference, invalidation.Reason); err != nil {
			return nil, fmt.Errorf("%w: invalidation", ErrSTRIDEConversationSnapshot)
		}
	}
	checkpoint, err := ledger.Rebuild()
	if err != nil || checkpoint.HighWater != snapshot.Checkpoint.HighWater || checkpoint.Checksum != snapshot.Checkpoint.Checksum {
		return nil, fmt.Errorf("%w: checkpoint got=%d/%s want=%d/%s", ErrSTRIDEConversationSnapshot, checkpoint.HighWater, checkpoint.Checksum, snapshot.Checkpoint.HighWater, snapshot.Checkpoint.Checksum)
	}
	return ledger, nil
}

func (ledger *STRIDEConversationLedger) ProjectForPrincipal(principal string) ([]STRIDEConversationMessageProjection, error) {
	if ledger == nil || !strideIdentifier(principal) {
		return nil, ErrSTRIDEConversationDenied
	}
	ledger.mu.RLock()
	defer ledger.mu.RUnlock()
	tenantID, ok := ledger.soleTenantLocked()
	if !ok {
		return nil, ErrSTRIDEConversationDenied
	}
	return ledger.projectForTenantPrincipalLocked(tenantID, principal), nil
}

func (ledger *STRIDEConversationLedger) ProjectForTenantPrincipal(tenantID, principal string) ([]STRIDEConversationMessageProjection, error) {
	if ledger == nil || !strideIdentifier(tenantID) || !strideIdentifier(principal) {
		return nil, ErrSTRIDEConversationDenied
	}
	ledger.mu.RLock()
	defer ledger.mu.RUnlock()
	return ledger.projectForTenantPrincipalLocked(tenantID, principal), nil
}

func (ledger *STRIDEConversationLedger) projectForTenantPrincipalLocked(tenantID, principal string) []STRIDEConversationMessageProjection {
	bySource := make(map[string]STRIDEConversationMessageProjection)
	for _, record := range ledger.sortedEventsLocked() {
		event := record.Append.Event
		if event.Header.TenantID != tenantID || !record.RecallEligible || record.Invalidated || event.Audience.Visibility == "private" || !containsSTRIDEID(event.Audience.Principals, principal) {
			continue
		}
		key := strideConversationSourceKey(event.Header.TenantID, event.SourceType, event.SourceID)
		projection := bySource[key]
		if projection.SourceID == "" {
			projection = STRIDEConversationMessageProjection{TenantID: event.Header.TenantID, SourceType: event.SourceType, SourceID: event.SourceID, ThreadID: event.ThreadID, AuthorPrincipal: event.AuthorPrincipal, AuthorName: event.AuthorName, Audience: event.Audience, ACLVersion: event.ACLVersion, RetentionPolicy: event.RetentionPolicy, PurgeGeneration: event.PurgeGeneration, RecallEligible: true}
		}
		projection.LatestEvent = strideConversationEventReference(event)
		projection.ACLVersion, projection.RetentionPolicy, projection.PurgeGeneration, projection.Audience = event.ACLVersion, event.RetentionPolicy, event.PurgeGeneration, event.Audience
		if event.ReplyToEventID != "" {
			projection.ReplyToEventID = event.ReplyToEventID
		}
		if len(event.AttachmentRefs) > 0 || oneOf(event.EventType, "message", "edit", "reaction") {
			projection.AttachmentRefs = append([]STRIDEReference(nil), event.AttachmentRefs...)
		}
		if len(event.LinkRefs) > 0 || oneOf(event.EventType, "message", "edit", "reaction") {
			projection.LinkRefs = append([]STRIDEReference(nil), event.LinkRefs...)
		}
		if event.EventType == "reaction" || len(event.ReactionActors) > 0 {
			projection.ReactionActors = append([]string(nil), event.ReactionActors...)
		}
		switch event.EventType {
		case "reaction":
			// New events carry the complete set above. Preserve compatibility
			// with pre-field snapshots whose reaction event recorded only the
			// actor that performed the mutation.
			if event.ReactionActors == nil {
				projection.ReactionActors = appendUniqueSTRIDEID(projection.ReactionActors, event.AuthorPrincipal)
			}
		case "file":
			projection.AttachmentRefs = append(projection.AttachmentRefs, event.StructuredRefs...)
		case "link":
			projection.LinkRefs = append(projection.LinkRefs, event.StructuredRefs...)
		case "delete":
			projection.Retracted = true
		}
		bySource[key] = projection
	}
	projections := make([]STRIDEConversationMessageProjection, 0, len(bySource))
	for _, projection := range bySource {
		if !projection.Retracted {
			projection.ReactionActors = sortedUniqueSTRIDEIDs(projection.ReactionActors)
			projection.AttachmentRefs = SortedSTRIDEReferences(projection.AttachmentRefs)
			projection.LinkRefs = SortedSTRIDEReferences(projection.LinkRefs)
			projections = append(projections, projection)
		}
	}
	sort.Slice(projections, func(i, j int) bool { return projections[i].LatestEvent.ID < projections[j].LatestEvent.ID })
	return projections
}

func (ledger *STRIDEConversationLedger) recallEligibleLocked(append STRIDEConversationAppend) bool {
	event := append.Event
	if event.Audience.Visibility == "private" || event.ThreadID == "" || !containsSTRIDEID(ledger.config.RecallThreadIDs, event.ThreadID) {
		return false
	}
	if event.SourceType == "private_thread" {
		return false
	}
	return event.SourceType != "private_share" || append.PrivateShareSource != nil
}
func (ledger *STRIDEConversationLedger) hasEarlierSourceEventLocked(event ConversationEvent) bool {
	return len(ledger.sourceEvents[strideConversationSourceKey(event.Header.TenantID, event.SourceType, event.SourceID)]) > 0
}
func (ledger *STRIDEConversationLedger) invalidateSourceLocked(tenantID, sourceType, sourceID, reason string) {
	for _, eventKey := range append([]string(nil), ledger.sourceEvents[strideConversationSourceKey(tenantID, sourceType, sourceID)]...) {
		ledger.invalidateConversationEventKeyLocked(eventKey, reason)
	}
}
func (ledger *STRIDEConversationLedger) invalidateConversationEventKeyLocked(eventKey, reason string) {
	record, ok := ledger.events[eventKey]
	if !ok {
		return
	}
	record.Invalidated = true
	record.InvalidationReason = reason
	ledger.events[eventKey] = record
	ledger.invalidateDerivedLocked(record.Append.Event.Header.TenantID, strideConversationEventReference(record.Append.Event), reason)
}
func (ledger *STRIDEConversationLedger) invalidateDerivedLocked(tenantID string, reference STRIDEReference, reason string) {
	key := strideConversationTenantReferenceKey(tenantID, reference)
	if _, exists := ledger.invalidated[key]; exists {
		return
	}
	ledger.invalidated[key] = STRIDEConversationInvalidation{TenantID: tenantID, Reference: reference, Reason: reason, At: time.Unix(0, 0).UTC()}
	for _, edge := range ledger.edges {
		if edge.TenantID == tenantID && edge.Source == reference {
			ledger.invalidateDerivedLocked(tenantID, edge.Derived, reason)
		}
	}
}
func (ledger *STRIDEConversationLedger) rebuildCheckpointLocked(generation uint64) {
	snapshot := ledger.snapshotLocked()
	snapshot.Checkpoint = STRIDEConversationCheckpoint{Generation: generation, HighWater: uint64(len(ledger.events))}
	digest, err := strideConversationSnapshotDigest(snapshot)
	if err != nil {
		panic(fmt.Sprintf("canonical STRIDE conversation snapshot: %v", err))
	}
	ledger.checkpoint = STRIDEConversationCheckpoint{Generation: generation, HighWater: uint64(len(ledger.events)), Checksum: digest, CreatedAt: time.Unix(0, 0).UTC()}
}
func (ledger *STRIDEConversationLedger) snapshotLocked() STRIDEConversationSnapshot {
	events := ledger.sortedEventsLocked()
	edges := make([]STRIDESourceDerivedEdge, 0, len(ledger.edges))
	for _, edge := range ledger.edges {
		edges = append(edges, edge)
	}
	sort.Slice(edges, func(i, j int) bool { return strideConversationEdgeKey(edges[i]) < strideConversationEdgeKey(edges[j]) })
	invalidations := make([]STRIDEConversationInvalidation, 0, len(ledger.invalidated))
	for _, invalidation := range ledger.invalidated {
		invalidations = append(invalidations, invalidation)
	}
	sort.Slice(invalidations, func(i, j int) bool {
		return strideConversationTenantReferenceKey(invalidations[i].TenantID, invalidations[i].Reference) < strideConversationTenantReferenceKey(invalidations[j].TenantID, invalidations[j].Reference)
	})
	return STRIDEConversationSnapshot{Format: 1, Events: events, Edges: edges, Invalidations: invalidations, Checkpoint: ledger.checkpoint}
}
func (ledger *STRIDEConversationLedger) sortedEventsLocked() []STRIDEConversationEventRecord {
	values := make([]STRIDEConversationEventRecord, 0, len(ledger.events))
	for _, record := range ledger.events {
		values = append(values, cloneSTRIDEConversationRecord(record))
	}
	sort.Slice(values, func(i, j int) bool { return values[i].Sequence < values[j].Sequence })
	return values
}
func strideConversationSnapshotDigest(snapshot STRIDEConversationSnapshot) (string, error) {
	snapshot.Digest = ""
	snapshot.Checkpoint.Checksum = ""
	snapshot.Checkpoint.CreatedAt = time.Time{}
	// A rebuild generation records operational lineage, not source state. It
	// must not change a deterministic replay checksum.
	snapshot.Checkpoint.Generation = 0
	return STRIDEContractDigest(snapshot)
}
func strideConversationSourceKey(tenant, sourceType, sourceID string) string {
	return tenant + "\x00" + sourceType + "\x00" + sourceID
}
func strideConversationEventKey(tenant, eventID string) string {
	return tenant + "\x00" + eventID
}
func strideConversationReferenceKey(reference STRIDEReference) string {
	return string(reference.ContractType) + "\x00" + reference.ID + "\x00" + fmt.Sprint(reference.Revision) + "\x00" + reference.Digest
}
func strideConversationTenantReferenceKey(tenantID string, reference STRIDEReference) string {
	return tenantID + "\x00" + strideConversationReferenceKey(reference)
}
func strideConversationEdgeKey(edge STRIDESourceDerivedEdge) string {
	return edge.TenantID + "\x00" + strideConversationReferenceKey(edge.Source) + "\x00" + strideConversationReferenceKey(edge.Derived) + "\x00" + string(edge.Lane)
}
func strideConversationEventReference(event ConversationEvent) STRIDEReference {
	return STRIDEReference{ContractType: STRIDEContractConversationEvent, ID: event.Header.ID, Revision: event.Header.Revision, Digest: event.Header.ContentDigest}
}
func cloneSTRIDEConversationAppend(value STRIDEConversationAppend) STRIDEConversationAppend {
	value.Event.StructuredRefs = append([]STRIDEReference(nil), value.Event.StructuredRefs...)
	value.Event.AttachmentRefs = append([]STRIDEReference(nil), value.Event.AttachmentRefs...)
	value.Event.LinkRefs = append([]STRIDEReference(nil), value.Event.LinkRefs...)
	if value.Event.ReactionActors != nil {
		value.Event.ReactionActors = append([]string{}, value.Event.ReactionActors...)
	}
	value.Event.Audience.Principals = append([]string(nil), value.Event.Audience.Principals...)
	if value.PrivateShareSource != nil {
		source := *value.PrivateShareSource
		value.PrivateShareSource = &source
	}
	if value.PrivateShareAuthorization != nil {
		authorization := *value.PrivateShareAuthorization
		value.PrivateShareAuthorization = &authorization
	}
	return value
}
func cloneSTRIDEConversationRecord(value STRIDEConversationEventRecord) STRIDEConversationEventRecord {
	value.Append = cloneSTRIDEConversationAppend(value.Append)
	return value
}
func (ledger *STRIDEConversationLedger) soleTenantLocked() (string, bool) {
	tenantID := ""
	for _, record := range ledger.events {
		candidate := record.Append.Event.Header.TenantID
		if tenantID == "" {
			tenantID = candidate
			continue
		}
		if tenantID != candidate {
			return "", false
		}
	}
	return tenantID, tenantID != ""
}
func containsSTRIDEID(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
func appendUniqueSTRIDEID(values []string, value string) []string {
	if containsSTRIDEID(values, value) {
		return values
	}
	return append(values, value)
}
func sortedUniqueSTRIDEIDs(values []string) []string {
	clone := append([]string(nil), values...)
	sort.Strings(clone)
	result := clone[:0]
	for _, value := range clone {
		if len(result) == 0 || result[len(result)-1] != value {
			result = append(result, value)
		}
	}
	return result
}
