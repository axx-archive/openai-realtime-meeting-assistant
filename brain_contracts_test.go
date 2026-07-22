package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

type fixedBrainPurgeGeneration int64

func (generation fixedBrainPurgeGeneration) CurrentPurgeGeneration(context.Context, string) (int64, error) {
	return int64(generation), nil
}

func w2TemporalQueryForTest() TemporalQuery {
	start := time.Date(2026, 7, 22, 10, 0, 0, 0, time.UTC)
	return TemporalQuery{
		StartUTC: start, EndUTC: start.Add(10 * time.Minute), Timezone: "America/Los_Angeles",
		RoomID: "room-a", SittingID: "sitting-a", AdmissionAnchorID: "anchor-a",
		CaptureSequenceCutoff: 100, CaptureWatermark: start.Add(10*time.Minute + time.Second),
		SettleUntil: start.Add(10*time.Minute + 3*time.Second), Interpretation: TemporalBeforeAdmission,
		InterpretationNote: "material before the member's first admission",
	}
}

func w2EvidenceForTest() BrainEvidenceRef {
	start := time.Date(2026, 7, 22, 10, 1, 0, 0, time.UTC)
	return BrainEvidenceRef{
		TenantID: "tenant-a", SourceFamily: "memory", ObjectID: "entry-a", ContentRevision: 2, ACLVersion: 3,
		ContentDigest: strings.Repeat("a", 64), RoomID: "room-a", SittingID: "sitting-a",
		OccurredStart: start, OccurredEnd: start.Add(time.Minute), PurgeGeneration: 4, Trust: BrainEvidenceTrusted,
	}
}

func TestBrainClaimRequiresResolvablePrimaryEvidenceWhenAsserted(t *testing.T) {
	claim := BrainClaim{
		ClaimID: "claim-a", ClaimType: "decision", AssertionDigest: strings.Repeat("b", 64), Status: BrainClaimAsserted, Confidence: .9,
		Generation: BrainGenerationProvenance{
			Provider: "anthropic", Model: "claude-opus-4-8", RouteSeat: "review", ReasoningEffort: "high",
			PromptVersion: "claims-v1", RetrievalSnapshotID: strings.Repeat("c", 64), GeneratedAt: time.Now().UTC(),
		},
	}
	if err := claim.Validate(); err == nil {
		t.Fatal("asserted claim without primary evidence passed validation")
	}
	claim.Evidence = []BrainEvidenceRef{w2EvidenceForTest()}
	if err := claim.Validate(); err != nil {
		t.Fatalf("valid asserted claim: %v", err)
	}
	claim.Evidence[0].ContentDigest = "not-a-digest"
	if err := claim.Validate(); err == nil {
		t.Fatal("claim with an invalid evidence revision passed validation")
	}
}

func TestBrainEvidenceRequiresExactLocatorAndTrust(t *testing.T) {
	ref := w2EvidenceForTest()
	if err := ref.Validate(); err != nil {
		t.Fatalf("valid evidence: %v", err)
	}
	ref.OccurredStart, ref.OccurredEnd = time.Time{}, time.Time{}
	if err := ref.Validate(); err == nil {
		t.Fatal("object-only evidence passed without a time interval or span")
	}
	ref.SpanStart, ref.SpanEnd, ref.Trust = 0, 25, BrainEvidenceUntrustedGuest
	ref.GuestOrigin = &BrainGuestOrigin{
		SessionKeyHash: strings.Repeat("f", 64), GuestLinkID: "guest-link-a", RoomID: ref.RoomID, SittingID: ref.SittingID,
		ConsentSnapshotDigest: strings.Repeat("1", 64),
	}
	if err := ref.Validate(); err != nil {
		t.Fatalf("valid untrusted guest span: %v", err)
	}
	ref.GuestOrigin = nil
	if err := ref.Validate(); err == nil {
		t.Fatal("untrusted guest evidence passed without structural guest provenance")
	}
}

func TestRetrievalSnapshotReauthorizesEverySourceRevision(t *testing.T) {
	evidence := w2EvidenceForTest()
	query := w2TemporalQueryForTest()
	snapshot := RetrievalSnapshot{
		TenantID: evidence.TenantID, PrincipalKind: ACLPrincipalUser, PrincipalID: "user-a", Query: "What was decided?",
		Temporal: query, SourceHighWater: 50, ProjectionHighWater: 50, PurgeGeneration: evidence.PurgeGeneration,
		Sources: []RetrievalSnapshotSource{{EvidenceID: "evidence-a", Evidence: evidence}}, CreatedAt: time.Date(2026, 7, 22, 10, 15, 0, 0, time.UTC),
	}
	snapshot.QueryDigest = digestBrainString(snapshot.Query)
	var err error
	snapshot.SnapshotID, err = snapshot.CanonicalID()
	if err != nil {
		t.Fatal(err)
	}
	if err := snapshot.Validate(); err != nil {
		t.Fatalf("valid snapshot: %v", err)
	}
	objectRef, revisionRef := evidence.ACLRefs()
	object := ACLObject{Ref: objectRef, CurrentContentRevision: revisionRef.ContentRevision, CurrentContentDigest: revisionRef.ContentDigest}
	grant := ACLGrant{
		ID: "grant-a", TenantID: evidence.TenantID, ObjectType: evidence.SourceFamily, ObjectID: evidence.ObjectID,
		ACLVersion: evidence.ACLVersion, SubjectKind: ACLSubjectPrincipal, SubjectID: "user-a", SubjectPrincipalKind: ACLPrincipalUser,
		Actions: []ACLAction{ACLReadContent},
	}
	store := &MemoryACLStore{Objects: map[string]ACLObject{aclObjectKey(objectRef): object}, Grants: map[string][]ACLGrant{aclObjectKey(objectRef): {grant}}}
	kernel := AuthorizationKernel{Store: store}
	principal := ACLPrincipal{TenantID: evidence.TenantID, Kind: ACLPrincipalUser, ID: "user-a"}
	purges := fixedBrainPurgeGeneration(evidence.PurgeGeneration)
	if err := ReauthorizeRetrievalSnapshot(context.Background(), kernel, purges, principal, snapshot); err != nil {
		t.Fatalf("reauthorize current snapshot: %v", err)
	}
	tampered := snapshot
	tampered.Query = "a different question"
	if err := tampered.Validate(); err == nil {
		t.Fatal("snapshot body tampering passed canonical digest validation")
	}
	if err := ReauthorizeRetrievalSnapshot(context.Background(), kernel, fixedBrainPurgeGeneration(evidence.PurgeGeneration+1), principal, snapshot); err != ErrRetrievalSnapshotStale {
		t.Fatalf("purge generation drift error=%v, want ErrRetrievalSnapshotStale", err)
	}

	// A same-object edit makes both the content revision and ACL-bound snapshot
	// stale; it cannot be used at critic/publication/read time.
	object.CurrentContentRevision++
	store.Objects[aclObjectKey(objectRef)] = object
	if err := ReauthorizeRetrievalSnapshot(context.Background(), kernel, purges, principal, snapshot); err != ErrRetrievalSnapshotStale {
		t.Fatalf("stale revision error=%v, want ErrRetrievalSnapshotStale", err)
	}
	object.CurrentContentRevision = revisionRef.ContentRevision
	store.Objects[aclObjectKey(objectRef)] = object
	revokedAt := time.Now().UTC()
	grant.RevokedAt = &revokedAt
	store.Grants[aclObjectKey(objectRef)] = []ACLGrant{grant}
	if err := ReauthorizeRetrievalSnapshot(context.Background(), kernel, purges, principal, snapshot); err != ErrRetrievalSnapshotStale {
		t.Fatalf("revoked source error=%v, want ErrRetrievalSnapshotStale", err)
	}
	if err := ReauthorizeRetrievalSnapshot(context.Background(), kernel, purges, ACLPrincipal{TenantID: evidence.TenantID, Kind: ACLPrincipalGuest, ID: "guest-a"}, snapshot); err != ErrRetrievalSnapshotStale {
		t.Fatalf("principal substitution error=%v, want ErrRetrievalSnapshotStale", err)
	}
}

func finalizedRecallCoverage(t *testing.T, coverage RecallCoverage) RecallCoverage {
	t.Helper()
	coverage.FreshSources, coverage.PartialSources, coverage.StaleSources = 0, 0, 0
	coverage.MissingSources, coverage.FailedSources, coverage.OmittedSources = 0, 0, 0
	coverage.AuthorizedSources = len(coverage.Sources)
	for _, source := range coverage.Sources {
		switch source.Status {
		case RecallSourceFresh:
			coverage.FreshSources++
		case RecallSourcePartial:
			coverage.PartialSources++
		case RecallSourceStale:
			coverage.StaleSources++
		case RecallSourceMissing:
			coverage.MissingSources++
		case RecallSourceFailed:
			coverage.FailedSources++
		case RecallSourceOmitted:
			coverage.OmittedSources++
		}
	}
	digest, err := coverage.CanonicalDigest()
	if err != nil {
		t.Fatal(err)
	}
	coverage.Digest = digest
	return coverage
}

func TestRecallCoverageDerivesCompletePartialAndUnavailableHonestly(t *testing.T) {
	start := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	base := RecallCoverage{
		SnapshotID: strings.Repeat("7", 64), Status: RecallCoverageComplete, RequestedStartUTC: start, RequestedEndUTC: start.Add(24 * time.Hour),
		ResolvedStartUTC: start, ResolvedEndUTC: start.Add(24 * time.Hour), Timezone: "UTC",
		SourceHighWater: 8, ProjectionHighWater: 8, AsOf: start.Add(25 * time.Hour),
		Sources: []RecallSourceCoverage{{SourceFamily: "meeting", ObjectID: "meeting-a", ContentDigest: strings.Repeat("e", 64), Status: RecallSourceFresh}},
		Lanes:   RecallLaneCoverage{Lexical: RecallLaneActive, Semantic: RecallLaneNotRequired, Digest: RecallLaneActive, Raw: RecallLaneActive},
	}
	base = finalizedRecallCoverage(t, base)
	if err := base.Validate(); err != nil {
		t.Fatalf("complete coverage: %v", err)
	}
	acceleratorOutage := base
	acceleratorOutage.ProjectionHighWater = 7
	acceleratorOutage.Lanes.Semantic, acceleratorOutage.Lanes.Digest = RecallLaneUnavailable, RecallLaneDegraded
	acceleratorOutage.Digest = ""
	acceleratorOutage = finalizedRecallCoverage(t, acceleratorOutage)
	if err := acceleratorOutage.Validate(); err != nil {
		t.Fatalf("complete raw fallback with degraded accelerators: %v", err)
	}

	partial := base
	partial.Status, partial.Reason = RecallCoveragePartial, "one authorized source failed"
	partial.Sources[0].Status = RecallSourceFailed
	partial.Digest = ""
	partial = finalizedRecallCoverage(t, partial)
	if err := partial.Validate(); err != nil {
		t.Fatalf("partial coverage: %v", err)
	}
	lying := partial
	lying.Status, lying.Reason = RecallCoverageComplete, ""
	lying.Digest = ""
	lying = finalizedRecallCoverage(t, lying)
	if err := lying.Validate(); err == nil {
		t.Fatal("complete label passed while an authorized source failed")
	}
	unavailable := base
	unavailable.Status, unavailable.Reason = RecallCoverageUnavailable, "no authorized retrieval lane is available"
	unavailable.Lanes = RecallLaneCoverage{Lexical: RecallLaneUnavailable, Semantic: RecallLaneUnavailable, Digest: RecallLaneUnavailable, Raw: RecallLaneUnavailable}
	unavailable.Digest = ""
	unavailable = finalizedRecallCoverage(t, unavailable)
	if err := unavailable.Validate(); err != nil {
		t.Fatalf("unavailable coverage: %v", err)
	}
	empty := base
	empty.Status, empty.Reason, empty.Sources = RecallCoveragePartial, "authorized range contains no resolvable primary evidence", nil
	empty.FreshSources, empty.Digest = 0, ""
	empty = finalizedRecallCoverage(t, empty)
	if err := empty.Validate(); err != nil {
		t.Fatalf("honestly partial empty coverage: %v", err)
	}
	empty.Status, empty.Reason, empty.Digest = RecallCoverageComplete, "", ""
	empty = finalizedRecallCoverage(t, empty)
	if err := empty.Validate(); err == nil {
		t.Fatal("zero-source coverage claimed complete")
	}
}

func TestTemporalQueryUsesHalfOpenWindowAndCaptureCutoff(t *testing.T) {
	query := w2TemporalQueryForTest()
	if err := query.Validate(); err != nil {
		t.Fatalf("valid admission query: %v", err)
	}
	segment := CapturedTemporalSegment{
		OccurredStart: query.StartUTC.Add(-time.Minute), OccurredEnd: query.StartUTC.Add(time.Minute),
		CaptureSequence: 99, CapturedAt: query.CaptureWatermark.Add(time.Second),
	}
	decision := query.DecideSegment(segment)
	if !decision.Include || !decision.Clipped || !decision.ClippedStart.Equal(query.StartUTC) || !decision.LateArrival {
		t.Fatalf("start-clipped decision=%+v", decision)
	}
	segment = CapturedTemporalSegment{
		OccurredStart: query.EndUTC.Add(-time.Minute), OccurredEnd: query.EndUTC.Add(time.Minute),
		CaptureSequence: 100, CapturedAt: query.CaptureWatermark,
	}
	decision = query.DecideSegment(segment)
	if !decision.Include || !decision.Clipped || !decision.ClippedEnd.Equal(query.EndUTC) {
		t.Fatalf("end-clipped decision=%+v", decision)
	}
	segment.CaptureSequence = 101
	if decision := query.DecideSegment(segment); decision.Include {
		t.Fatal("post-admission capture sequence entered pre-admission recap")
	}
	segment.CaptureSequence, segment.OccurredStart, segment.OccurredEnd = 100, query.EndUTC, query.EndUTC.Add(time.Minute)
	if decision := query.DecideSegment(segment); decision.Include {
		t.Fatal("segment at half-open end boundary entered recap")
	}
	if query.Settled(query.SettleUntil.Add(-time.Nanosecond)) || !query.Settled(query.SettleUntil) {
		t.Fatal("settle boundary is not half-open/deterministic")
	}
}

func TestTemporalBeforeAdmissionConstructorBindsPersistedAnchorIncludingEmptyCapture(t *testing.T) {
	admittedAt := time.Date(2026, 7, 22, 10, 10, 0, 0, time.UTC)
	anchor := AdmissionAnchor{TenantID: "tenant-a", RoomID: "room-a", SittingID: "sitting-a", Principal: memberAdmissionPrincipal("user@example.com"), AdmittedAt: admittedAt}
	anchor.AnchorID = deterministicAdmissionAnchorID(anchor)
	query, err := NewBeforeAdmissionTemporalQuery(anchor, admittedAt.Add(-10*time.Minute), "America/Los_Angeles", 3*time.Second, "before first admission")
	if err != nil {
		t.Fatalf("construct empty-capture admission query: %v", err)
	}
	if query.AdmissionAnchorID != anchor.AnchorID || query.CaptureSequenceCutoff != 0 || !query.CaptureWatermark.IsZero() || !query.EndUTC.Equal(admittedAt) {
		t.Fatalf("query did not bind exact anchor: %+v", query)
	}
}

func TestTemporalFirstAndLastMinuteConstructorsClampToSitting(t *testing.T) {
	start := time.Date(2026, 11, 1, 8, 55, 0, 0, time.UTC) // spans the US fall-back hour in Los Angeles
	end := start.Add(20 * time.Minute)
	first, err := NewFirstMinutesTemporalQuery(start, end, 5, "America/Los_Angeles", "room-a", "sitting-a", "first five minutes")
	if err != nil || !first.StartUTC.Equal(start) || !first.EndUTC.Equal(start.Add(5*time.Minute)) {
		t.Fatalf("first-minutes query=%+v err=%v", first, err)
	}
	last, err := NewLastMinutesTemporalQuery(start, end, 30, "America/Los_Angeles", "room-a", "sitting-a", "last thirty minutes clamped")
	if err != nil || !last.StartUTC.Equal(start) || !last.EndUTC.Equal(end) {
		t.Fatalf("last-minutes query=%+v err=%v", last, err)
	}
}

func TestTemporalExplicitRangeRejectsAdmissionState(t *testing.T) {
	query := w2TemporalQueryForTest()
	query.Interpretation = TemporalExplicitRange
	if err := query.Validate(); err == nil {
		t.Fatal("explicit range accepted hidden admission state")
	}
	query.AdmissionAnchorID, query.CaptureSequenceCutoff = "", 0
	query.CaptureWatermark, query.SettleUntil = time.Time{}, time.Time{}
	if err := query.Validate(); err != nil {
		t.Fatalf("valid explicit range: %v", err)
	}
}
