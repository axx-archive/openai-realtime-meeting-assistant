package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func temporalTestHeader(kind STRIDEContractType, id string, revision int64, at time.Time) STRIDEContractHeader {
	return STRIDEContractHeader{TenantID: "tenant-1", ID: id, Revision: revision, SchemaVersion: STRIDEContractSchemaVersion,
		ContractType: kind, ContentDigest: temporalDigest(id + "-body-" + string(rune(revision))), CreatedAt: at.UTC()}
}

func temporalTestRef(kind STRIDEContractType, id string) STRIDEReference {
	return referenceFromHeader(temporalTestHeader(kind, id, 1, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)))
}

func temporalTestTranscript(segmentID, revisionID, text, status, supersedes string, revision int64, sequence uint64, sourceStart, sourceEnd, capturedAt time.Time, audience, topics []string) TemporalMeetingEvent {
	conversationHeader := temporalTestHeader(STRIDEContractConversationEvent, "conversation-"+segmentID, 1, capturedAt)
	conversation := ConversationEvent{
		Header: conversationHeader, SourceType: "room", SourceID: "source-" + segmentID, RoomID: "room-1", SittingID: "sitting-1",
		AuthorPrincipal: "speaker-1", AuthorName: "Speaker One", OccurredAt: sourceStart.UTC(), IngestedAt: capturedAt.UTC(), EventType: "transcript_turn",
		ContentRevision: 1, ContentDigest: temporalDigest(text), Audience: STRIDEAudience{Visibility: "meeting", Principals: audience}, ACLVersion: 1,
		RetentionPolicy: "meeting-default", PurgeGeneration: 0, Provenance: "provider",
	}
	segmentHeader := temporalTestHeader(STRIDEContractTranscriptSegment, segmentID, 1, capturedAt)
	segment := TranscriptSegment{
		Header: segmentHeader, ConversationRef: referenceFromHeader(conversationHeader), RoomID: "room-1", SittingID: "sitting-1", MediaGeneration: 1,
		CaptureSequence: sequence, SourceStart: sourceStart.UTC(), SourceEnd: sourceEnd.UTC(), Status: "authoritative_final", Speaker: "speaker-1",
		Attribution: "known", ConsentScopes: []string{"meeting_intelligence"}, ModelDigest: temporalDigest("model"), ConfigDigest: temporalDigest("config"),
		ContextDigest: temporalDigest("context"), CreatedAt: capturedAt.UTC(),
	}
	revisionHeader := temporalTestHeader(STRIDEContractTranscriptRevision, revisionID, revision, capturedAt)
	revisionContract := TranscriptRevision{Header: revisionHeader, SegmentID: segmentID, Revision: revision, TextDigest: temporalDigest(text), Status: status,
		SupersedesID: supersedes, Evidence: []STRIDEReference{referenceFromHeader(segmentHeader)}}
	return TemporalMeetingEvent{Sequence: sequence, Kind: TemporalMeetingEventTranscript, Transcript: &TemporalTranscriptRevisionEvent{
		Conversation: conversation, Segment: segment, Revision: revisionContract, Text: text, TopicIDs: topics,
	}}
}

func temporalTestAnalysis(id, kind, statement string, sequence, sourceHighWater uint64, source TemporalMeetingEvent, windowStart, windowEnd, freshThrough time.Time, audience, topics []string) TemporalMeetingEvent {
	projection := AnalysisProjection{
		Header: temporalTestHeader(STRIDEContractAnalysisProjection, id, 1, freshThrough), Kind: kind,
		SourceRefs: []STRIDEReference{referenceFromHeader(source.Transcript.Revision.Header)}, WindowStart: windowStart.UTC(), WindowEnd: windowEnd.UTC(),
		ThroughSegmentID: source.Transcript.Segment.Header.ID, SourceHighWater: sourceHighWater, ProjectionHighWater: sequence,
		ModelDigest: temporalDigest("model"), PromptDigest: temporalDigest("prompt"), EvidenceDigest: temporalDigest("evidence"), Confidence: .9,
		Audience: STRIDEAudience{Visibility: "meeting", Principals: audience}, FreshThrough: freshThrough.UTC(),
	}
	return TemporalMeetingEvent{Sequence: sequence, Kind: TemporalMeetingEventAnalysis, Analysis: &TemporalAnalysisEvent{Projection: projection, Statement: statement, TopicIDs: topics}}
}

func temporalTestBrain(t *testing.T, start, end time.Time) *TemporalMeetingBrain {
	t.Helper()
	brain, err := NewTemporalMeetingBrain(TemporalMeetingBrainConfig{TenantID: "tenant-1", RoomID: "room-1", SittingID: "sitting-1", SittingStart: start.UTC(), SittingEnd: end.UTC()})
	if err != nil {
		t.Fatal(err)
	}
	return brain
}

func TestTemporalMeetingBrainReducerUsesSourceTimeAndPopulatesTypedState(t *testing.T) {
	start := time.Date(2026, 7, 30, 17, 0, 0, 0, time.UTC)
	brain := temporalTestBrain(t, start, start.Add(time.Hour))
	lateSource := temporalTestTranscript("segment-late", "revision-late", "later source turn", "authoritative_final", "", 1, 1,
		start.Add(20*time.Minute), start.Add(21*time.Minute), start.Add(2*time.Minute), []string{"alice"}, []string{"pricing"})
	earlySource := temporalTestTranscript("segment-early", "revision-early", "earlier source turn", "authoritative_final", "", 1, 2,
		start.Add(5*time.Minute), start.Add(6*time.Minute), start.Add(40*time.Minute), []string{"alice"}, []string{"pricing"})
	for _, event := range []TemporalMeetingEvent{lateSource, earlySource} {
		if err := brain.Apply(event); err != nil {
			t.Fatal(err)
		}
	}
	kinds := []string{"decision", "commitment", "blocker", "storyline", "alignment", "position", "open_question", "entity", "artifact", "work_intent_candidate"}
	for index, kind := range kinds {
		event := temporalTestAnalysis("fact-"+kind, kind, "statement "+kind, uint64(index+3), 2, earlySource,
			start.Add(5*time.Minute), start.Add(6*time.Minute), start.Add(time.Hour), []string{"alice"}, []string{"pricing"})
		if err := brain.Apply(event); err != nil {
			t.Fatalf("apply %s: %v", kind, err)
		}
	}
	state := brain.CurrentState()
	counts := []int{len(state.Decisions), len(state.Commitments), len(state.Blockers), len(state.Storylines), len(state.Alignment), len(state.Positions), len(state.Questions), len(state.Entities), len(state.Artifacts), len(state.WorkCandidates)}
	if !reflect.DeepEqual(counts, []int{1, 1, 1, 1, 1, 1, 1, 1, 1, 1}) {
		t.Fatalf("typed state counts = %v", counts)
	}
	if got := brain.sortedSources(); got[0].SegmentID != "segment-early" || got[1].SegmentID != "segment-late" {
		t.Fatalf("source-time ordering ignored: %+v", got)
	}
	intervals, err := brain.ResolveQuery(TemporalMeetingQuery{Kind: TemporalQueryLastFiveMinutes, AsOf: start.Add(21 * time.Minute), Timezone: "UTC", RequestedAt: start.Add(21 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if !intervals[0].StartUTC.Equal(start.Add(16*time.Minute)) || !intervals[0].EndUTC.Equal(start.Add(21*time.Minute)) {
		t.Fatalf("last-five interval = %+v", intervals[0])
	}
	intervals, err = brain.ResolveQuery(TemporalMeetingQuery{Kind: TemporalQueryLastThirtyMinutes, AsOf: start.Add(21 * time.Minute), Timezone: "UTC", RequestedAt: start.Add(21 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if !intervals[0].StartUTC.Equal(start) || !intervals[0].EndUTC.Equal(start.Add(21*time.Minute)) {
		t.Fatalf("last-thirty clipping = %+v", intervals[0])
	}
	topic, err := brain.ResolveQuery(TemporalMeetingQuery{Kind: TemporalQueryTopic, TopicID: "pricing", Timezone: "UTC", RequestedAt: start.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if len(topic) != 2 || !topic[0].StartUTC.Equal(start.Add(5*time.Minute)) || !topic[1].StartUTC.Equal(start.Add(20*time.Minute)) {
		t.Fatalf("topic intervals = %+v", topic)
	}
}

func TestTemporalExplicitClockHasExactDSTAndCalendarSemantics(t *testing.T) {
	brain := temporalTestBrain(t, time.Date(2026, 11, 1, 7, 0, 0, 0, time.UTC), time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC))
	base := TemporalMeetingQuery{Kind: TemporalQueryExplicitClock, Timezone: "America/Los_Angeles", StartLocal: "2026-11-01T01:15", EndLocal: "2026-11-01T01:45", RequestedAt: time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)}
	if _, err := brain.ResolveQuery(base); !errors.Is(err, ErrTemporalClockAmbiguous) {
		t.Fatalf("ambiguous clock error = %v", err)
	}
	base.StartFold, base.EndFold = 1, 2
	intervals, err := brain.ResolveQuery(base)
	if err != nil {
		t.Fatal(err)
	}
	if got := intervals[0].EndUTC.Sub(intervals[0].StartUTC); got != 90*time.Minute {
		t.Fatalf("fold-spanning duration = %v", got)
	}
	gap := base
	gap.StartLocal, gap.EndLocal = "2026-03-08T02:15", "2026-03-08T03:15"
	gap.StartFold, gap.EndFold = 0, 0
	if _, err := brain.ResolveQuery(gap); !errors.Is(err, ErrTemporalClockNonexistent) {
		t.Fatalf("spring gap error = %v", err)
	}
	cross := base
	cross.StartLocal, cross.EndLocal = "2026-11-01T23:55", "2026-11-02T00:05"
	cross.StartFold, cross.EndFold = 0, 0
	intervals, err = brain.ResolveQuery(cross)
	if err != nil {
		t.Fatal(err)
	}
	if intervals[0].EndUTC.Sub(intervals[0].StartUTC) != 10*time.Minute {
		t.Fatalf("calendar boundary interval = %+v", intervals[0])
	}
}

func TestTemporalAnswerFiltersACLAndConsentBeforeBodiesAndFallsBackWhenAnalysisStale(t *testing.T) {
	start := time.Date(2026, 7, 30, 18, 0, 0, 0, time.UTC)
	brain := temporalTestBrain(t, start, start.Add(time.Hour))
	alice := temporalTestTranscript("segment-alice", "revision-alice", "alice only", "authoritative_final", "", 1, 1, start, start.Add(time.Minute), start.Add(2*time.Minute), []string{"alice"}, nil)
	bob := temporalTestTranscript("segment-bob", "revision-bob", "bob only", "authoritative_final", "", 1, 2, start.Add(time.Minute), start.Add(2*time.Minute), start.Add(3*time.Minute), []string{"bob"}, nil)
	for _, event := range []TemporalMeetingEvent{alice, bob} {
		if err := brain.Apply(event); err != nil {
			t.Fatal(err)
		}
	}
	if err := brain.Apply(temporalTestAnalysis("decision-alice", "decision", "Alice decided", 3, 2, alice, start, start.Add(time.Minute), start.Add(time.Hour), []string{"alice"}, nil)); err != nil {
		t.Fatal(err)
	}
	interval, _ := NewBoundedTemporalQuery(TemporalExplicitRange, start, start.Add(10*time.Minute), "UTC", "room-1", "sitting-1", "acl test")
	principal := ACLPrincipal{TenantID: "tenant-1", ID: "alice", Kind: ACLPrincipalUser}
	answer, err := brain.Answer(principal, []string{"meeting_intelligence"}, []TemporalQuery{interval}, start.Add(20*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(answer.Sources) != 1 || answer.Sources[0].Text != "alice only" || strings.Contains(answer.EvidenceDigest, "bob only") || len(answer.Facts) != 1 || !answer.AnalysisFresh {
		t.Fatalf("alice answer leaked or lost data: %+v", answer)
	}
	bobAnswer, err := brain.Answer(ACLPrincipal{TenantID: "tenant-1", ID: "bob", Kind: ACLPrincipalUser}, []string{"meeting_intelligence"}, []TemporalQuery{interval}, start.Add(20*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(bobAnswer.Sources) != 1 || bobAnswer.Sources[0].Text != "bob only" || bobAnswer.TranscriptHighWater == answer.TranscriptHighWater || len(bobAnswer.Facts) != 0 {
		t.Fatalf("principal-differential answer = %+v", bobAnswer)
	}
	withoutConsent, err := brain.Answer(principal, nil, []TemporalQuery{interval}, start.Add(20*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(withoutConsent.Sources) != 0 || withoutConsent.TranscriptHighWater != 0 {
		t.Fatalf("consent denial leaked inventory: %+v", withoutConsent)
	}
	newAlice := temporalTestTranscript("segment-alice-2", "revision-alice-2", "new turn", "authoritative_final", "", 1, 4, start.Add(3*time.Minute), start.Add(4*time.Minute), start.Add(4*time.Minute), []string{"alice"}, nil)
	if err := brain.Apply(newAlice); err != nil {
		t.Fatal(err)
	}
	stale, err := brain.Answer(principal, []string{"meeting_intelligence"}, []TemporalQuery{interval}, start.Add(20*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if stale.AnalysisFresh || stale.Mode != "transcript_first" || len(stale.Facts) != 0 || !temporalTestContains(stale.Coverage.Gaps, "analysis_stale") {
		t.Fatalf("stale fallback = %+v", stale)
	}
}

func TestTemporalLateJoinUsesHalfOpenSourceTimeCutoffAndSettlement(t *testing.T) {
	start := time.Date(2026, 7, 30, 19, 0, 0, 0, time.UTC)
	brain := temporalTestBrain(t, start, start.Add(time.Hour))
	watermark := start.Add(10*time.Minute - time.Second)
	anchor := normalizeAdmissionAnchor(AdmissionAnchor{TenantID: "tenant-1", RoomID: "room-1", SittingID: "sitting-1", Principal: CanonicalPrincipalRef{Kind: "user", ID: "alice"},
		AdmittedAt: start.Add(10 * time.Minute), CaptureSequenceCutoff: 2, CaptureWatermark: watermark})
	anchor.AnchorID = deterministicAdmissionAnchorID(anchor)
	events := []TemporalMeetingEvent{
		temporalTestTranscript("segment-before", "revision-before", "arrived late", "authoritative_final", "", 1, 1, start.Add(8*time.Minute), start.Add(9*time.Minute), start.Add(11*time.Minute), []string{"alice"}, nil),
		temporalTestTranscript("segment-cutoff", "revision-cutoff", "wrong generation", "authoritative_final", "", 1, 3, start.Add(9*time.Minute), start.Add(9*time.Minute+30*time.Second), start.Add(9*time.Minute+40*time.Second), []string{"alice"}, nil),
		temporalTestTranscript("segment-boundary", "revision-boundary", "at admission", "authoritative_final", "", 1, 4, start.Add(10*time.Minute), start.Add(11*time.Minute), start.Add(11*time.Minute), []string{"alice"}, nil),
	}
	for _, event := range events {
		if err := brain.Apply(event); err != nil {
			t.Fatal(err)
		}
	}
	intervals, err := brain.ResolveQuery(TemporalMeetingQuery{Kind: TemporalQueryLateJoin, Timezone: "UTC", Anchor: anchor, SettleDelay: 2 * time.Minute, RequestedAt: start.Add(10 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	before, err := brain.ResolveQuery(TemporalMeetingQuery{Kind: TemporalQueryBeforeAdmission, Timezone: "UTC", Anchor: anchor, SettleDelay: 2 * time.Minute, RequestedAt: start.Add(10 * time.Minute)})
	if err != nil || len(before) != 1 || !intervals[0].StartUTC.Equal(before[0].StartUTC) || !intervals[0].EndUTC.Equal(before[0].EndUTC) ||
		intervals[0].CaptureSequenceCutoff != before[0].CaptureSequenceCutoff || !intervals[0].CaptureWatermark.Equal(before[0].CaptureWatermark) || !intervals[0].SettleUntil.Equal(before[0].SettleUntil) {
		t.Fatalf("before-admission/late-join semantic drift: %+v %+v %v", intervals, before, err)
	}
	principal := ACLPrincipal{TenantID: "tenant-1", ID: "alice", Kind: ACLPrincipalUser}
	answer, err := brain.Answer(principal, []string{"meeting_intelligence"}, intervals, start.Add(11*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(answer.Sources) != 1 || answer.Sources[0].SegmentID != "segment-before" || !answer.Sources[0].LateArrival || answer.Coverage.Settled {
		t.Fatalf("unsettled late join = %+v", answer)
	}
	settled, err := brain.Answer(principal, []string{"meeting_intelligence"}, intervals, start.Add(12*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if !settled.Coverage.Settled || len(settled.Sources) != 1 {
		t.Fatalf("settled late join = %+v", settled)
	}
}

func TestTemporalCorrectionRetractionAndPurgeInvalidateDerivedState(t *testing.T) {
	start := time.Date(2026, 7, 30, 20, 0, 0, 0, time.UTC)
	brain := temporalTestBrain(t, start, start.Add(time.Hour))
	original := temporalTestTranscript("segment-1", "revision-1", "secret old text", "authoritative_final", "", 1, 1, start, start.Add(time.Minute), start.Add(time.Minute), []string{"alice"}, nil)
	if err := brain.Apply(original); err != nil {
		t.Fatal(err)
	}
	if err := brain.Apply(temporalTestAnalysis("decision-old", "decision", "old decision", 2, 1, original, start, start.Add(time.Minute), start.Add(time.Hour), []string{"alice"}, nil)); err != nil {
		t.Fatal(err)
	}
	corrected := temporalTestTranscript("segment-1", "revision-2", "corrected text", "corrected", "revision-1", 2, 3, start, start.Add(time.Minute), start.Add(2*time.Minute), []string{"alice"}, nil)
	if err := brain.Apply(corrected); err != nil {
		t.Fatal(err)
	}
	if len(brain.CurrentState().Decisions) != 0 || brain.sources["segment-1"].Text != "corrected text" {
		t.Fatalf("correction did not invalidate: %+v", brain.CurrentState())
	}
	if err := brain.Apply(temporalTestAnalysis("decision-new", "decision", "new decision", 4, 3, corrected, start, start.Add(time.Minute), start.Add(time.Hour), []string{"alice"}, nil)); err != nil {
		t.Fatal(err)
	}
	if err := brain.Apply(TemporalMeetingEvent{Sequence: 5, Kind: TemporalMeetingEventPurge, Purge: &TemporalPurgeEvent{TenantID: "tenant-1", SegmentID: "segment-1", RevisionID: "revision-2", PurgeGeneration: 1}}); err != nil {
		t.Fatal(err)
	}
	if len(brain.sources) != 0 || len(brain.CurrentState().Decisions) != 0 {
		t.Fatalf("purge did not invalidate state")
	}
	snapshot, err := brain.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(snapshot, []byte("secret old text")) || bytes.Contains(snapshot, []byte("corrected text")) || bytes.Contains(snapshot, []byte("new decision")) {
		t.Fatalf("purged bodies survived compact snapshot: %s", snapshot)
	}

	retractionBrain := temporalTestBrain(t, start, start.Add(time.Hour))
	if err := retractionBrain.Apply(original); err != nil {
		t.Fatal(err)
	}
	retracted := temporalTestTranscript("segment-1", "revision-retracted", "removed", "retracted", "revision-1", 2, 2, start, start.Add(time.Minute), start.Add(2*time.Minute), []string{"alice"}, nil)
	if err := retractionBrain.Apply(retracted); err != nil {
		t.Fatal(err)
	}
	if len(retractionBrain.sources) != 0 {
		t.Fatal("retracted revision remained current")
	}
	supersessionBrain := temporalTestBrain(t, start, start.Add(time.Hour))
	if err := supersessionBrain.Apply(original); err != nil {
		t.Fatal(err)
	}
	if err := supersessionBrain.Apply(temporalTestAnalysis("decision-superseded", "decision", "obsolete", 2, 1, original, start, start.Add(time.Minute), start.Add(time.Hour), []string{"alice"}, nil)); err != nil {
		t.Fatal(err)
	}
	superseded := temporalTestTranscript("segment-1", "revision-superseded", "obsolete", "superseded", "revision-1", 2, 3, start, start.Add(time.Minute), start.Add(2*time.Minute), []string{"alice"}, nil)
	if err := supersessionBrain.Apply(superseded); err != nil {
		t.Fatal(err)
	}
	if len(supersessionBrain.sources) != 0 || len(supersessionBrain.CurrentState().Decisions) != 0 {
		t.Fatal("superseded revision or derivative remained current")
	}
}

func TestTemporalSnapshotRestoreAndCanonicalRebuildAreIdentical(t *testing.T) {
	start := time.Date(2026, 7, 30, 21, 0, 0, 0, time.UTC)
	config := TemporalMeetingBrainConfig{TenantID: "tenant-1", RoomID: "room-1", SittingID: "sitting-1", SittingStart: start, SittingEnd: start.Add(time.Hour)}
	source := temporalTestTranscript("segment-1", "revision-1", "durable turn", "authoritative_final", "", 1, 1, start, start.Add(time.Minute), start.Add(2*time.Minute), []string{"alice"}, []string{"launch"})
	analysis := temporalTestAnalysis("commitment-1", "commitment", "Ship Friday", 2, 1, source, start, start.Add(time.Minute), start.Add(time.Hour), []string{"alice"}, []string{"launch"})
	brain, err := RebuildTemporalMeetingBrain(config, []TemporalMeetingEvent{analysis, source})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := brain.AuthenticatedSnapshot(strideSnapshotAuthorityForTest(), 7)
	if err != nil {
		t.Fatal(err)
	}
	policy := STRIDESnapshotRestorePolicy{Authority: strideSnapshotAuthorityForTest(), MinimumGeneration: 7}
	restored, err := RestoreTemporalMeetingBrain(snapshot, policy)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := brain.StateDigest()
	got, _ := restored.StateDigest()
	if want != got || !reflect.DeepEqual(brain.CurrentState(), restored.CurrentState()) {
		t.Fatalf("restart identity mismatch: %s != %s", want, got)
	}
	tampered := bytes.Replace(snapshot, []byte("durable turn"), []byte("altered turn"), 1)
	if _, err := RestoreTemporalMeetingBrain(tampered, policy); !errors.Is(err, ErrTemporalBrainSnapshot) {
		t.Fatalf("tampered snapshot error = %v", err)
	}
	if _, err := RebuildTemporalMeetingBrain(config, []TemporalMeetingEvent{source, source}); !errors.Is(err, ErrTemporalBrainSequence) {
		t.Fatalf("duplicate sequence error = %v", err)
	}
}

func TestTemporalRestoreRejectsPurgeResurrectionMissingEvidenceAndRollback(t *testing.T) {
	start := time.Date(2026, 7, 30, 21, 30, 0, 0, time.UTC)
	brain := temporalTestBrain(t, start, start.Add(time.Hour))
	transcript := temporalTestTranscript("segment-1", "revision-1", "erase me", "authoritative_final", "", 1, 1, start, start.Add(time.Minute), start.Add(time.Minute), []string{"alice"}, nil)
	analysis := temporalTestAnalysis("decision-1", "decision", "private decision", 2, 1, transcript, start, start.Add(time.Minute), start.Add(time.Hour), []string{"alice"}, nil)
	if err := brain.Apply(transcript); err != nil {
		t.Fatal(err)
	}
	sourceBeforePurge := brain.sources["segment-1"]
	if err := brain.Apply(analysis); err != nil {
		t.Fatal(err)
	}
	factBeforePurge := brain.facts["decision-1"]
	if err := brain.Apply(TemporalMeetingEvent{Sequence: 3, Kind: TemporalMeetingEventPurge, Purge: &TemporalPurgeEvent{TenantID: "tenant-1", SegmentID: "segment-1", RevisionID: "revision-1", PurgeGeneration: 1}}); err != nil {
		t.Fatal(err)
	}
	raw, err := brain.AuthenticatedSnapshot(strideSnapshotAuthorityForTest(), 10)
	if err != nil {
		t.Fatal(err)
	}
	policy := STRIDESnapshotRestorePolicy{Authority: strideSnapshotAuthorityForTest(), MinimumGeneration: 10}
	var snapshot temporalMeetingBrainSnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		t.Fatal(err)
	}

	// An attacker can recompute the public digest, but cannot authenticate the
	// resurrected source or derivative under the configured key.
	snapshot.Sources = append(snapshot.Sources, sourceBeforePurge)
	snapshot.Facts = append(snapshot.Facts, factBeforePurge)
	snapshot.StateDigest, err = snapshot.canonicalStateDigest()
	if err != nil {
		t.Fatal(err)
	}
	recomputedRaw, _ := canonicalJSON(snapshot)
	if _, err := RestoreTemporalMeetingBrain(recomputedRaw, policy); !errors.Is(err, ErrTemporalBrainSnapshot) {
		t.Fatalf("recomputed purge resurrection error=%v", err)
	}

	// Canonical restore validation independently rejects a purged revision even
	// if a trusted snapshot producer accidentally seals the inconsistent state.
	snapshot.Signature, err = strideSnapshotMAC(strideSnapshotAuthorityForTest(), "temporal_meeting_brain", snapshot.SnapshotGeneration, snapshot.StateDigest)
	if err != nil {
		t.Fatal(err)
	}
	resignedRaw, _ := canonicalJSON(snapshot)
	if _, err := RestoreTemporalMeetingBrain(resignedRaw, policy); !errors.Is(err, ErrTemporalBrainSnapshot) {
		t.Fatalf("signed purge resurrection error=%v", err)
	}

	if _, err := RestoreTemporalMeetingBrain(raw, STRIDESnapshotRestorePolicy{Authority: strideSnapshotAuthorityForTest(), MinimumGeneration: 11}); !errors.Is(err, ErrTemporalBrainSnapshot) {
		t.Fatalf("rollback generation error=%v", err)
	}

	evidenceBrain := temporalTestBrain(t, start, start.Add(time.Hour))
	if err := evidenceBrain.Apply(transcript); err != nil {
		t.Fatal(err)
	}
	if err := evidenceBrain.Apply(analysis); err != nil {
		t.Fatal(err)
	}
	evidenceRaw, err := evidenceBrain.AuthenticatedSnapshot(strideSnapshotAuthorityForTest(), 20)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(evidenceRaw, &snapshot); err != nil {
		t.Fatal(err)
	}
	snapshot.Facts[0].Evidence = []STRIDEReference{temporalTestRef(STRIDEContractTranscriptRevision, "revision-missing")}
	snapshot.StateDigest, _ = snapshot.canonicalStateDigest()
	snapshot.Signature, _ = strideSnapshotMAC(strideSnapshotAuthorityForTest(), "temporal_meeting_brain", snapshot.SnapshotGeneration, snapshot.StateDigest)
	missingEvidenceRaw, _ := canonicalJSON(snapshot)
	if _, err := RestoreTemporalMeetingBrain(missingEvidenceRaw, STRIDESnapshotRestorePolicy{Authority: strideSnapshotAuthorityForTest(), MinimumGeneration: 20}); !errors.Is(err, ErrTemporalBrainSnapshot) {
		t.Fatalf("fact without current evidence error=%v", err)
	}
}

func TestSpecialistContextContainsOnlyExactAuthorizedBodyFreeReferencesAndBudgets(t *testing.T) {
	start := time.Date(2026, 7, 30, 22, 0, 0, 0, time.UTC)
	interval, _ := NewBoundedTemporalQuery(TemporalExplicitRange, start, start.Add(5*time.Minute), "UTC", "room-1", "sitting-1", "approved")
	intervalDigest, _ := temporalIntervalDigest([]TemporalQuery{interval})
	transcriptRef := temporalTestRef(STRIDEContractTranscriptRevision, "revision-1")
	answer := TemporalMeetingAnswer{Mode: "analysis_with_transcript_evidence", TranscriptHighWater: 7, AnalysisHighWater: 7, AnalysisFresh: true,
		Sources:  []TemporalAuthorizedTranscript{{Evidence: transcriptRef, SegmentID: "segment-1", SourceStart: start, SourceEnd: start.Add(time.Minute), CoveredStart: start, CoveredEnd: start.Add(time.Minute), Text: "must not enter envelope"}},
		Coverage: TemporalAnswerCoverage{Intervals: []TemporalQuery{interval}, AuthorizedSources: 1, Settled: true}, EvidenceDigest: temporalDigest("answer")}
	audienceAlice := STRIDEAudience{Visibility: "private", Principals: []string{"alice"}}
	audienceBob := STRIDEAudience{Visibility: "private", Principals: []string{"bob"}}
	contextRef := func(kind STRIDEContractType, id string, audience STRIDEAudience, highWater uint64) TemporalContextReference {
		return TemporalContextReference{Reference: temporalTestRef(kind, id), Audience: audience, ConsentScopes: []string{"meeting_intelligence"}, WindowStart: start,
			WindowEnd: start.Add(time.Minute), HighWater: highWater, FreshThrough: start.Add(5 * time.Minute)}
	}
	eligibilityRef := temporalTestRef(STRIDEContractAgentAssignment, "meeting-eligibility-1")
	request := MeetingSpecialistContextRequest{
		Header: temporalTestHeader(STRIDEContractMeetingSpecialistContext, "specialist-context-1", 1, start.Add(6*time.Minute)),
		Invitation: MeetingAgentInvitation{
			Header: temporalTestHeader(STRIDEContractMeetingAgentInvitation, "invitation-1", 1, start), RoomID: "room-1", SittingID: "sitting-1",
			SpecialistProfile: temporalTestRef(STRIDEContractAgentCoreProfile, "specialist-profile"), Capability: temporalTestRef(STRIDEContractAgentCapabilityManifest, "specialist-capability"), Eligibility: &eligibilityRef,
			Requester: "alice", EligibleConfirmer: "alice", PurposeDigest: temporalDigest("purpose"), ContextClasses: []string{"transcript", "analysis", "brain", "work"},
			SourceIntervalDigest: intervalDigest, Audience: audienceAlice, ConsentPolicyRevision: temporalTestRef(STRIDEContractKnowledgeAssertion, "consent-policy"),
			ExpectedTimeSeconds: 30, ExpectedCostCents: 0, ExpiresAt: start.Add(time.Hour), Decision: "approved", DecisionPrincipal: "alice", DecisionAt: func() *time.Time { value := start; return &value }(),
			IdempotencyKeyDigest: temporalDigest("invitation-key"),
		}, AgentProfile: temporalTestRef(STRIDEContractAgentCoreProfile, "profile-1"),
		RuntimeRevision: temporalTestRef(STRIDEContractAgentCapabilityManifest, "runtime-1"), ModelRevision: temporalTestRef(STRIDEContractAgentCapabilityManifest, "model-1"),
		Principal: ACLPrincipal{TenantID: "tenant-1", ID: "alice", Kind: ACLPrincipalUser}, ConsentScopes: []string{"meeting_intelligence"},
		ApprovedIntervals: []TemporalQuery{interval}, ApprovedIntervalDigest: intervalDigest, Answer: answer,
		Analysis:        []TemporalContextReference{contextRef(STRIDEContractAnalysisProjection, "analysis-good", audienceAlice, 7), contextRef(STRIDEContractAnalysisProjection, "analysis-stale", audienceAlice, 6)},
		Brain:           []TemporalContextReference{contextRef(STRIDEContractKnowledgeAssertion, "brain-good", audienceAlice, 3), contextRef(STRIDEContractKnowledgeAssertion, "brain-denied", audienceBob, 999)},
		Work:            []TemporalContextReference{contextRef(STRIDEContractWorkIntent, "work-good", audienceAlice, 1)},
		RetentionDigest: temporalDigest("retention"), ToolIDs: []string{"read-only"}, ResponseContract: "briefing", FloorPolicy: "wait_turn",
		TimeBudgetSeconds: 30, TurnBudget: 1, AudioBudgetSeconds: 15, TokenBudget: 0, CostBudgetCents: 0, MaxAnalysisRefs: 2, MaxBrainRefs: 2, MaxWorkRefs: 1,
	}
	assembled, err := AssembleMeetingSpecialistContext(request)
	if err != nil {
		t.Fatal(err)
	}
	if assembled.Envelope.Validate() != nil || len(assembled.Envelope.TranscriptRefs) != 1 || len(assembled.Envelope.AnalysisRefs) != 1 || len(assembled.Envelope.BrainRefs) != 1 || len(assembled.Envelope.WorkRefs) != 1 {
		t.Fatalf("assembled envelope = %+v", assembled)
	}
	withoutEligibility := request
	withoutEligibility.Invitation.Eligibility = nil
	if _, err := AssembleMeetingSpecialistContext(withoutEligibility); !errors.Is(err, ErrTemporalBrainInvalid) {
		t.Fatalf("eligibility-free specialist context error=%v", err)
	}
	if assembled.Envelope.BrainHighWater != 3 || assembled.Envelope.TokenBudget != 0 || !temporalTestContains(assembled.Gaps, "authorized_context_filtered_or_stale") {
		t.Fatalf("freshness/budget/gaps = %+v", assembled)
	}
	raw, _ := json.Marshal(assembled)
	if bytes.Contains(raw, []byte("must not enter envelope")) || bytes.Contains(raw, []byte("brain-denied")) || bytes.Contains(raw, []byte("999")) {
		t.Fatalf("context leaked body or denied metadata: %s", raw)
	}
	request.ApprovedIntervalDigest = temporalDigest("wrong")
	if _, err := AssembleMeetingSpecialistContext(request); !errors.Is(err, ErrTemporalBrainInvalid) {
		t.Fatalf("interval digest mismatch = %v", err)
	}
}

func temporalTestContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
