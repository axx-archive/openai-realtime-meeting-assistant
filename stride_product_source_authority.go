package main

import (
	"context"
	"strings"
	"time"
)

// strideProductSourceAuthority re-checks the canonical evidence stores at the
// exact moment a Suggested Work card is approved. A work record is a durable
// proposal, not proof that its source is still current: chat edits/deletes,
// explicit invalidation, transcript correction, or purge must all revoke the
// right to start a run from that evidence.
type strideProductSourceAuthority struct {
	tenantID       string
	principal      string
	sourceThread   string
	conversation   *STRIDEConversationLedger
	temporalBrains map[string]*TemporalMeetingBrain
}

func (authority strideProductSourceAuthority) SourcesCurrent(_ context.Context, _ string, refs []STRIDEReference) error {
	if !strideIdentifier(authority.tenantID) || !strideIdentifier(authority.principal) || len(refs) == 0 {
		return ErrSTRIDEWorkSourceChanged
	}
	for _, ref := range refs {
		if ref.Validate() != nil {
			return ErrSTRIDEWorkSourceChanged
		}
		switch ref.ContractType {
		case STRIDEContractConversationEvent:
			if !authority.conversationReferenceCurrent(ref) {
				return ErrSTRIDEWorkSourceChanged
			}
		case STRIDEContractTranscriptRevision:
			if !authority.temporalReferenceCurrent(ref) {
				return ErrSTRIDEWorkSourceChanged
			}
		default:
			return ErrSTRIDEWorkSourceChanged
		}
	}
	return nil
}

// DestinationAudienceAllowed prevents a meeting- or channel-scoped source
// from being published into a wider project thread. The destination membership
// is a snapshot supplied by the app's canonical thread resolver; every member
// must have been authorized for every source revision in the evidence set.
func (authority strideProductSourceAuthority) DestinationAudienceAllowed(refs []STRIDEReference, destination STRIDEAudience) error {
	canonical, err := canonicalSTRIDEProductSourceEvents(refs)
	if err != nil || !sameOrderedSTRIDEReferences(canonical, refs) || destination.Validate() != nil {
		return ErrSTRIDEWorkSourceChanged
	}
	for _, ref := range canonical {
		var sourceAudience STRIDEAudience
		switch ref.ContractType {
		case STRIDEContractConversationEvent:
			sourceAudience, err = authority.conversationSourceAudience(ref)
		case STRIDEContractTranscriptRevision:
			sourceAudience, err = authority.temporalSourceAudience(ref)
		default:
			err = ErrSTRIDEWorkSourceChanged
		}
		if err != nil || !strideAudiencePrincipalsSubset(destination, sourceAudience) {
			return ErrSTRIDEWorkSourceChanged
		}
	}
	return nil
}

func strideAudiencePrincipalsSubset(candidate, limit STRIDEAudience) bool {
	if candidate.Validate() != nil || limit.Validate() != nil {
		return false
	}
	allowed := make(map[string]bool, len(limit.Principals))
	for _, principal := range limit.Principals {
		allowed[principal] = true
	}
	for _, principal := range candidate.Principals {
		if !allowed[principal] {
			return false
		}
	}
	return true
}

func (authority strideProductSourceAuthority) conversationSourceAudience(ref STRIDEReference) (STRIDEAudience, error) {
	ledger := authority.conversation
	if ledger == nil {
		return STRIDEAudience{}, ErrSTRIDEWorkSourceChanged
	}
	ledger.mu.RLock()
	defer ledger.mu.RUnlock()
	record, found := ledger.events[strideConversationEventKey(authority.tenantID, ref.ID)]
	if !found || record.Invalidated || strideConversationEventReference(record.Append.Event) != ref {
		return STRIDEAudience{}, ErrSTRIDEWorkSourceChanged
	}
	event := record.Append.Event
	if event.Header.TenantID != authority.tenantID || event.EventType == "delete" || event.Audience.Visibility == "private" {
		return STRIDEAudience{}, ErrSTRIDEWorkSourceChanged
	}
	return cloneAudience(event.Audience), nil
}

func (authority strideProductSourceAuthority) temporalSourceAudience(ref STRIDEReference) (STRIDEAudience, error) {
	var matched *STRIDEAudience
	for _, brain := range authority.temporalBrains {
		if brain == nil {
			continue
		}
		state := brain.CurrentState()
		if state.Config.TenantID != authority.tenantID || state.Config.RoomID != authority.sourceThread {
			continue
		}
		for _, source := range brain.sources {
			if !sameSTRIDEReference(source.Revision, ref) {
				continue
			}
			if matched != nil {
				return STRIDEAudience{}, ErrSTRIDEWorkSourceChanged
			}
			audience := cloneAudience(source.Audience)
			matched = &audience
		}
	}
	if matched == nil {
		return STRIDEAudience{}, ErrSTRIDEWorkSourceChanged
	}
	return *matched, nil
}

// RetrievalSnapshot resolves the already-authorized reference set back to the
// canonical source stores. It carries real source intervals, room/sitting,
// ACL, purge and high-water metadata into the deterministic workflow instead
// of manufacturing a one-second conversation window around proposal creation.
func (authority strideProductSourceAuthority) RetrievalSnapshot(query string, refs []STRIDEReference, now time.Time) (RetrievalSnapshot, error) {
	var snapshot RetrievalSnapshot
	canonical, err := canonicalSTRIDEProductSourceEvents(refs)
	if err != nil || !sameOrderedSTRIDEReferences(canonical, refs) || strings.TrimSpace(query) == "" || now.IsZero() || authority.SourcesCurrent(context.Background(), "retrieval_snapshot", canonical) != nil {
		return snapshot, ErrSTRIDEWorkSourceChanged
	}
	var start, end time.Time
	roomID, sittingID := "", ""
	purgeGeneration := int64(-1)
	for _, ref := range canonical {
		var source RetrievalSnapshotSource
		var sourceStart, sourceEnd time.Time
		var sourceRoom, sourceSitting string
		var sourceHighWater, projectionHighWater uint64
		var sourcePurge int64
		switch ref.ContractType {
		case STRIDEContractConversationEvent:
			source, sourceStart, sourceEnd, sourceRoom, sourceSitting, sourceHighWater, sourcePurge, err = authority.conversationSnapshotSource(ref)
		case STRIDEContractTranscriptRevision:
			source, sourceStart, sourceEnd, sourceRoom, sourceSitting, sourceHighWater, projectionHighWater, sourcePurge, err = authority.temporalSnapshotSource(ref)
		default:
			err = ErrSTRIDEWorkSourceChanged
		}
		if err != nil || source.Evidence.Validate() != nil {
			return RetrievalSnapshot{}, ErrSTRIDEWorkSourceChanged
		}
		if purgeGeneration < 0 {
			purgeGeneration = sourcePurge
		} else if purgeGeneration != sourcePurge {
			return RetrievalSnapshot{}, ErrSTRIDEWorkSourceChanged
		}
		if len(snapshot.Sources) == 0 {
			roomID, sittingID = sourceRoom, sourceSitting
		} else if roomID != sourceRoom || sittingID != sourceSitting {
			return RetrievalSnapshot{}, ErrSTRIDEWorkSourceChanged
		}
		if start.IsZero() || sourceStart.Before(start) {
			start = sourceStart
		}
		if end.IsZero() || sourceEnd.After(end) {
			end = sourceEnd
		}
		if sourceHighWater > snapshot.SourceHighWater {
			snapshot.SourceHighWater = sourceHighWater
		}
		if projectionHighWater > snapshot.ProjectionHighWater {
			snapshot.ProjectionHighWater = projectionHighWater
		}
		snapshot.Sources = append(snapshot.Sources, source)
	}
	temporal, err := NewBoundedTemporalQuery(TemporalExplicitRange, start, end, "UTC", roomID, sittingID, "approved current work evidence")
	if err != nil || purgeGeneration < 0 {
		return RetrievalSnapshot{}, ErrSTRIDEWorkSourceChanged
	}
	snapshot.TenantID = authority.tenantID
	snapshot.PrincipalKind = ACLPrincipalUser
	snapshot.PrincipalID = authority.principal
	snapshot.Query = query
	snapshot.QueryDigest = digestBrainString(query)
	snapshot.Temporal = temporal
	snapshot.PurgeGeneration = purgeGeneration
	snapshot.CreatedAt = now.UTC()
	snapshot.SnapshotID, err = snapshot.CanonicalID()
	if err != nil || snapshot.Validate() != nil {
		return RetrievalSnapshot{}, ErrSTRIDEWorkSourceChanged
	}
	return snapshot, nil
}

func (authority strideProductSourceAuthority) conversationSnapshotSource(ref STRIDEReference) (RetrievalSnapshotSource, time.Time, time.Time, string, string, uint64, int64, error) {
	ledger := authority.conversation
	if ledger == nil {
		return RetrievalSnapshotSource{}, time.Time{}, time.Time{}, "", "", 0, 0, ErrSTRIDEWorkSourceChanged
	}
	ledger.mu.RLock()
	defer ledger.mu.RUnlock()
	record, found := ledger.events[strideConversationEventKey(authority.tenantID, ref.ID)]
	if !found || record.Invalidated || strideConversationEventReference(record.Append.Event) != ref {
		return RetrievalSnapshotSource{}, time.Time{}, time.Time{}, "", "", 0, 0, ErrSTRIDEWorkSourceChanged
	}
	event := record.Append.Event
	principal := ACLPrincipal{TenantID: authority.tenantID, ID: authority.principal, Kind: ACLPrincipalUser, TeamIDs: []string{"organization"}}
	if event.Header.TenantID != authority.tenantID || event.EventType == "delete" || event.Audience.Visibility == "private" || !audienceAllows(event.Audience, principal) {
		return RetrievalSnapshotSource{}, time.Time{}, time.Time{}, "", "", 0, 0, ErrSTRIDEWorkSourceChanged
	}
	start, end := event.OccurredAt.UTC().Add(-time.Nanosecond), event.OccurredAt.UTC().Add(time.Nanosecond)
	evidence := BrainEvidenceRef{TenantID: authority.tenantID, SourceFamily: event.SourceType, ObjectID: ref.ID, ContentRevision: ref.Revision, ACLVersion: event.ACLVersion, ContentDigest: ref.Digest,
		RoomID: event.RoomID, SittingID: event.SittingID, OccurredStart: start, OccurredEnd: end, PurgeGeneration: event.PurgeGeneration, Trust: BrainEvidenceTrusted}
	return RetrievalSnapshotSource{EvidenceID: strideProductEvidenceID(ref), Evidence: evidence}, start, end, event.RoomID, event.SittingID, ledger.checkpoint.HighWater, event.PurgeGeneration, nil
}

func (authority strideProductSourceAuthority) temporalSnapshotSource(ref STRIDEReference) (RetrievalSnapshotSource, time.Time, time.Time, string, string, uint64, uint64, int64, error) {
	var matched *TemporalTranscriptSource
	var matchedState TemporalCurrentMeetingState
	for _, brain := range authority.temporalBrains {
		if brain == nil {
			continue
		}
		state := brain.CurrentState()
		if state.Config.TenantID != authority.tenantID || state.Config.RoomID != authority.sourceThread {
			continue
		}
		for _, candidate := range brain.sources {
			if !sameSTRIDEReference(candidate.Revision, ref) {
				continue
			}
			principal := ACLPrincipal{TenantID: authority.tenantID, ID: authority.principal, Kind: ACLPrincipalUser, TeamIDs: []string{"organization"}, RoomID: state.Config.RoomID, SittingID: state.Config.SittingID}
			if !audienceAllows(candidate.Audience, principal) || matched != nil {
				return RetrievalSnapshotSource{}, time.Time{}, time.Time{}, "", "", 0, 0, 0, ErrSTRIDEWorkSourceChanged
			}
			copySource := candidate
			matched, matchedState = &copySource, state
		}
	}
	if matched == nil || matched.SourceStart.IsZero() || matched.SourceEnd.IsZero() || !matched.SourceStart.Before(matched.SourceEnd) {
		return RetrievalSnapshotSource{}, time.Time{}, time.Time{}, "", "", 0, 0, 0, ErrSTRIDEWorkSourceChanged
	}
	evidence := BrainEvidenceRef{TenantID: authority.tenantID, SourceFamily: "meeting_transcript", ObjectID: ref.ID, ContentRevision: ref.Revision, ACLVersion: matched.ACLVersion, ContentDigest: ref.Digest,
		RoomID: matchedState.Config.RoomID, SittingID: matchedState.Config.SittingID, OccurredStart: matched.SourceStart.UTC(), OccurredEnd: matched.SourceEnd.UTC(), PurgeGeneration: matchedState.PurgeGeneration, Trust: BrainEvidenceTrusted}
	return RetrievalSnapshotSource{EvidenceID: strideProductEvidenceID(ref), Evidence: evidence}, matched.SourceStart.UTC(), matched.SourceEnd.UTC(), matchedState.Config.RoomID, matchedState.Config.SittingID,
		matchedState.TranscriptHighWater, matchedState.AnalysisHighWater, matchedState.PurgeGeneration, nil
}

func strideProductEvidenceID(ref STRIDEReference) string {
	return "source_" + temporalDigest(referenceKey(ref))[:24]
}

func (authority strideProductSourceAuthority) conversationReferenceCurrent(ref STRIDEReference) bool {
	ledger := authority.conversation
	if ledger == nil {
		return false
	}
	ledger.mu.RLock()
	defer ledger.mu.RUnlock()
	record, found := ledger.events[strideConversationEventKey(authority.tenantID, ref.ID)]
	if !found || record.Invalidated || strideConversationEventReference(record.Append.Event) != ref {
		return false
	}
	if _, invalidated := ledger.invalidated[strideConversationTenantReferenceKey(authority.tenantID, ref)]; invalidated {
		return false
	}
	event := record.Append.Event
	if event.Header.TenantID != authority.tenantID || event.EventType == "delete" || event.Audience.Visibility == "private" {
		return false
	}
	return audienceAllows(event.Audience, ACLPrincipal{TenantID: authority.tenantID, ID: authority.principal, Kind: ACLPrincipalUser, TeamIDs: []string{"organization"}})
}

func (authority strideProductSourceAuthority) temporalReferenceCurrent(ref STRIDEReference) bool {
	if !strideIdentifier(authority.sourceThread) {
		return false
	}
	for _, brain := range authority.temporalBrains {
		if brain == nil {
			continue
		}
		state := brain.CurrentState()
		if state.Config.TenantID != authority.tenantID || state.Config.RoomID != authority.sourceThread {
			continue
		}
		for _, source := range brain.sources {
			if !sameSTRIDEReference(source.Revision, ref) {
				continue
			}
			principal := ACLPrincipal{
				TenantID:  authority.tenantID,
				ID:        authority.principal,
				Kind:      ACLPrincipalUser,
				TeamIDs:   []string{"organization"},
				RoomID:    state.Config.RoomID,
				SittingID: state.Config.SittingID,
			}
			return audienceAllows(source.Audience, principal)
		}
	}
	return false
}
