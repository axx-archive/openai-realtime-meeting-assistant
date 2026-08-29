package main

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

var sourceEpisodeTestTime = time.Date(2026, 8, 25, 18, 0, 0, 0, time.UTC)

func sourceEpisodeAdapterFixture(kind SourceEpisodeKind) SourceEpisodeAdapterInput {
	input := SourceEpisodeAdapterInput{
		Header: STRIDEContractHeader{
			TenantID: "company_1", ID: "episode_" + string(kind), Revision: 1, SchemaVersion: STRIDEContractSchemaVersion,
			CreatedAt: sourceEpisodeTestTime.Add(5 * time.Minute),
		},
		Source: SourceEpisodeRevisionRef{ObjectID: "source_" + string(kind), ContentRevision: 3, ContentDigest: strings.Repeat("1", 64), SizeBytes: 128},
		Scope:  SourceEpisodeScope{CompanyID: "company_1", MemoryScope: SourceEpisodeMemoryCompany},
		Authority: SourceEpisodeAuthoritySnapshot{
			Audience:    STRIDEAudience{Visibility: "organization", Principals: []string{"company_1"}},
			ACLRevision: 4, ACLDigest: strings.Repeat("2", 64), ConsentRevision: 5, ConsentDigest: strings.Repeat("3", 64),
			PurgeGeneration: 6, RetentionPolicy: "company_default", ObservedAt: sourceEpisodeTestTime.Add(4 * time.Minute),
		},
		OccurredStart: sourceEpisodeTestTime, OccurredEnd: sourceEpisodeTestTime.Add(2 * time.Minute),
		PhaseProof: SourceEpisodePhaseProof{Phase: SourceEpisodePhasePostCommit, BoundaryAt: sourceEpisodeTestTime.Add(3 * time.Minute), ReceiptDigest: strings.Repeat("4", 64)},
	}
	switch kind {
	case SourceEpisodeMeetingAnalysis:
		input.Source.SourceFamily = SourceEpisodeFamilyMeetingAnalysis
		input.RetrievalBody = SourceEpisodeRevisionRef{SourceFamily: SourceEpisodeFamilyMeetingAnalysisBody, ObjectID: "analysis_body_1", ContentRevision: 1, ContentDigest: strings.Repeat("a", 64), SizeBytes: 256}
		input.Scope = SourceEpisodeScope{CompanyID: "company_1", ProjectIDs: []string{"project_1"}, ConversationID: "meeting_1", PersonIDs: []string{"person_1"}, RoomID: "room_1", SittingID: "sitting_1", MemoryScope: SourceEpisodeMemoryProject}
		input.Authority.Audience = STRIDEAudience{Visibility: "meeting", Principals: []string{"person_1"}}
		input.PhaseProof.Phase = SourceEpisodePhasePostClose
		input.PhaseProof.BoundaryType = SourceEpisodeBoundaryMeetingClose
	case SourceEpisodePublicChannelSegment:
		input.Source.SourceFamily = SourceEpisodeFamilyConversationEvent
		input.Scope.ConversationID = "channel_1"
		input.Authority.Audience = STRIDEAudience{Visibility: "channel", Principals: []string{"company_1"}}
		input.PhaseProof.BoundaryType = SourceEpisodeBoundaryConversationCommit
	case SourceEpisodePrivateConversationSegment:
		input.Source.SourceFamily = SourceEpisodeFamilyConversationEvent
		input.Scope = SourceEpisodeScope{CompanyID: "company_1", ConversationID: "private_1", PersonIDs: []string{"person_1", "person_2"}, MemoryScope: SourceEpisodeMemoryPerson}
		input.Authority.Audience = STRIDEAudience{Visibility: "private", Principals: []string{"person_1", "person_2"}}
		input.PhaseProof.BoundaryType = SourceEpisodeBoundaryConversationCommit
	case SourceEpisodeRealtimeVoiceSession:
		input.Source.SourceFamily = SourceEpisodeFamilyRealtimeVoiceSession
		input.Scope = SourceEpisodeScope{CompanyID: "company_1", ConversationID: "voice_1", PersonIDs: []string{"person_1"}, MemoryScope: SourceEpisodeMemoryConversation}
		input.Authority.Audience = STRIDEAudience{Visibility: "private", Principals: []string{"person_1"}}
		input.PhaseProof.Phase = SourceEpisodePhasePostClose
		input.PhaseProof.BoundaryType = SourceEpisodeBoundaryRealtimeVoiceClose
	case SourceEpisodeDriveFileRevision:
		input.Source.SourceFamily = SourceEpisodeFamilyDriveFileRevision
		input.Scope.ProjectIDs = []string{"project_1"}
		input.PhaseProof.BoundaryType = SourceEpisodeBoundaryDriveCommit
	case SourceEpisodeWorkArtifactRevision:
		input.Source.SourceFamily = SourceEpisodeFamilyWorkArtifactRevision
		input.Scope = SourceEpisodeScope{CompanyID: "company_1", ProjectIDs: []string{"project_1"}, ConversationID: "work_1", PersonIDs: []string{"person_1"}, MemoryScope: SourceEpisodeMemoryProject}
		input.Authority.Audience = STRIDEAudience{Visibility: "project", Principals: []string{"person_1"}}
		input.PhaseProof.BoundaryType = SourceEpisodeBoundaryWorkCommit
	}
	if kind != SourceEpisodeMeetingAnalysis {
		input.RetrievalBody = input.Source
	}
	return input
}

func sourceEpisodeAdapterForKind(kind SourceEpisodeKind, input SourceEpisodeAdapterInput) (SourceEpisode, error) {
	switch kind {
	case SourceEpisodeMeetingAnalysis:
		return AdaptMeetingAnalysisSourceEpisode(input)
	case SourceEpisodePublicChannelSegment:
		return AdaptPublicChannelSourceEpisode(input)
	case SourceEpisodePrivateConversationSegment:
		return AdaptPrivateConversationSourceEpisode(input)
	case SourceEpisodeRealtimeVoiceSession:
		return AdaptRealtimeVoiceSourceEpisode(input)
	case SourceEpisodeDriveFileRevision:
		return AdaptDriveFileSourceEpisode(input)
	case SourceEpisodeWorkArtifactRevision:
		return AdaptWorkArtifactSourceEpisode(input)
	default:
		return SourceEpisode{}, ErrSourceEpisodeInvalid
	}
}

func TestSourceEpisodeAdaptersCoverExactlySixBodyFreeSourceKinds(t *testing.T) {
	kinds := []SourceEpisodeKind{
		SourceEpisodeMeetingAnalysis, SourceEpisodePublicChannelSegment, SourceEpisodePrivateConversationSegment,
		SourceEpisodeRealtimeVoiceSession, SourceEpisodeDriveFileRevision, SourceEpisodeWorkArtifactRevision,
	}
	for _, kind := range kinds {
		t.Run(string(kind), func(t *testing.T) {
			input := sourceEpisodeAdapterFixture(kind)
			episode, err := sourceEpisodeAdapterForKind(kind, input)
			if err != nil {
				t.Fatalf("adapter rejected valid source: %v", err)
			}
			if err := episode.Validate(); err != nil {
				t.Fatalf("adapted episode invalid: %v", err)
			}
			if episode.Kind != kind || episode.Source.SourceFamily != input.Source.SourceFamily || episode.Source.ObjectID != input.Source.ObjectID ||
				episode.Source.ContentRevision != input.Source.ContentRevision || episode.Source.ContentDigest != input.Source.ContentDigest ||
				!reflect.DeepEqual(episode.Scope, input.Scope) || !reflect.DeepEqual(episode.Authority, input.Authority) {
				t.Fatalf("adapter lost exact source, scope, or authority: %+v", episode)
			}
			wantKey := SourceEpisodeIdempotencyKey(input.Header.TenantID, kind, episode.Source)
			if episode.IdempotencyKeyDigest != wantKey || !isHexDigest(wantKey) {
				t.Fatalf("unstable idempotency key: got=%s want=%s", episode.IdempotencyKeyDigest, wantKey)
			}
			if kind == SourceEpisodeMeetingAnalysis && episode.RawMeetingTranscriptAccess != MeetingSourceEpisodeRawExactSegments ||
				kind != SourceEpisodeMeetingAnalysis && episode.RawMeetingTranscriptAccess != "" {
				t.Fatalf("raw transcript policy leaked across kinds: %+v", episode)
			}
		})
	}
	if validSourceEpisodeKind("calendar_event") {
		t.Fatal("unapproved source kind became canonical")
	}
	drive := sourceEpisodeAdapterFixture(SourceEpisodeDriveFileRevision)
	drive.Source.SourceFamily = SourceEpisodeFamilyWorkArtifactRevision
	if _, err := AdaptDriveFileSourceEpisode(drive); !errors.Is(err, ErrSourceEpisodeInvalid) {
		t.Fatalf("Drive adapter accepted a non-Drive native locator: %v", err)
	}
	work := sourceEpisodeAdapterFixture(SourceEpisodeWorkArtifactRevision)
	work.Source.SourceFamily = SourceEpisodeFamilyDriveFileRevision
	if _, err := AdaptWorkArtifactSourceEpisode(work); !errors.Is(err, ErrSourceEpisodeInvalid) {
		t.Fatalf("work adapter accepted a non-work native locator: %v", err)
	}
}

func TestSourceEpisodeAdaptersRejectLiveAndPreCommitPhases(t *testing.T) {
	for _, kind := range []SourceEpisodeKind{SourceEpisodeMeetingAnalysis, SourceEpisodeRealtimeVoiceSession} {
		input := sourceEpisodeAdapterFixture(kind)
		input.PhaseProof.Phase = SourceEpisodePhasePostCommit
		if _, err := sourceEpisodeAdapterForKind(kind, input); !errors.Is(err, ErrSourceEpisodePhase) {
			t.Fatalf("%s accepted non-close phase: %v", kind, err)
		}
	}
	for _, kind := range []SourceEpisodeKind{SourceEpisodePublicChannelSegment, SourceEpisodePrivateConversationSegment, SourceEpisodeDriveFileRevision, SourceEpisodeWorkArtifactRevision} {
		input := sourceEpisodeAdapterFixture(kind)
		input.PhaseProof.Phase = SourceEpisodePhasePostClose
		if _, err := sourceEpisodeAdapterForKind(kind, input); !errors.Is(err, ErrSourceEpisodePhase) {
			t.Fatalf("%s accepted non-commit phase: %v", kind, err)
		}
	}
	input := sourceEpisodeAdapterFixture(SourceEpisodeMeetingAnalysis)
	input.PhaseProof.BoundaryAt = input.OccurredEnd.Add(-time.Nanosecond)
	if _, err := AdaptMeetingAnalysisSourceEpisode(input); !errors.Is(err, ErrSourceEpisodeInvalid) {
		t.Fatalf("meeting adapter accepted boundary before source end: %v", err)
	}
	input = sourceEpisodeAdapterFixture(SourceEpisodeMeetingAnalysis)
	input.Authority.ObservedAt = input.PhaseProof.BoundaryAt.Add(-time.Nanosecond)
	if _, err := AdaptMeetingAnalysisSourceEpisode(input); !errors.Is(err, ErrSourceEpisodeInvalid) {
		t.Fatalf("meeting adapter accepted authority observed before close: %v", err)
	}
	input = sourceEpisodeAdapterFixture(SourceEpisodeMeetingAnalysis)
	input.PhaseProof.BoundaryType = SourceEpisodeBoundaryConversationCommit
	if _, err := AdaptMeetingAnalysisSourceEpisode(input); !errors.Is(err, ErrSourceEpisodePhase) {
		t.Fatalf("meeting adapter accepted a conversation commit receipt: %v", err)
	}
}

func TestSourceEpisodePrivateSourcesCannotSelfPromoteOrWidenPeople(t *testing.T) {
	for _, kind := range []SourceEpisodeKind{SourceEpisodePrivateConversationSegment, SourceEpisodeRealtimeVoiceSession} {
		input := sourceEpisodeAdapterFixture(kind)
		input.Scope.MemoryScope = SourceEpisodeMemoryCompany
		if _, err := sourceEpisodeAdapterForKind(kind, input); !errors.Is(err, ErrSourceEpisodePrivacy) {
			t.Fatalf("%s self-promoted private content: %v", kind, err)
		}
		input = sourceEpisodeAdapterFixture(kind)
		input.Scope.PersonIDs = append(input.Scope.PersonIDs, "person_outsider")
		if _, err := sourceEpisodeAdapterForKind(kind, input); !errors.Is(err, ErrSourceEpisodePrivacy) {
			t.Fatalf("%s widened private people scope: %v", kind, err)
		}
	}
}

type sourceEpisodeTestAuthority struct {
	want     SourceEpisodeAuthorityExpectation
	drift    bool
	callback bool
}

func (authority *sourceEpisodeTestAuthority) WithCurrentSourceEpisodeAuthority(_ context.Context, got SourceEpisodeAuthorityExpectation, use func() error) error {
	if authority.drift || !reflect.DeepEqual(authority.want, got) {
		return ErrSourceEpisodeConflict
	}
	authority.callback = true
	return use()
}

type sourceEpisodeTestWriter struct {
	byKey   map[string]SourceEpisode
	current map[string]STRIDEReference
	writes  int
}

func (writer *sourceEpisodeTestWriter) DualWriteSourceEpisode(_ context.Context, episode SourceEpisode, expected *STRIDEReference) (SourceEpisodeDualWriteResult, error) {
	if writer.byKey == nil {
		writer.byKey = map[string]SourceEpisode{}
		writer.current = map[string]STRIDEReference{}
	}
	if existing, ok := writer.byKey[episode.IdempotencyKeyDigest]; ok {
		replayed, err := SourceEpisodeReplayDecision(existing, episode)
		if err != nil {
			return SourceEpisodeDualWriteResult{}, err
		}
		return SourceEpisodeDualWriteResult{Reference: referenceFromHeader(existing.Header), Replayed: replayed}, nil
	}
	current, hasCurrent := writer.current[episode.Header.ID]
	if (expected == nil) != !hasCurrent || expected != nil && *expected != current {
		return SourceEpisodeDualWriteResult{}, ErrSourceEpisodeConflict
	}
	writer.writes++
	writer.byKey[episode.IdempotencyKeyDigest] = episode
	ref := referenceFromHeader(episode.Header)
	writer.current[episode.Header.ID] = ref
	return SourceEpisodeDualWriteResult{Reference: ref}, nil
}

func sourceEpisodeExpectation(episode SourceEpisode) SourceEpisodeAuthorityExpectation {
	return SourceEpisodeAuthorityExpectation{
		Kind: episode.Kind, Source: episode.Source, RetrievalBody: episode.RetrievalBody, Scope: episode.Scope, Authority: episode.Authority,
		OccurredEnd: episode.OccurredEnd, PhaseProof: episode.PhaseProof,
	}
}

func TestSourceEpisodeDualWriteIsAuthorityFencedReplaySafeAndRevisionCASBound(t *testing.T) {
	first, err := AdaptPublicChannelSourceEpisode(sourceEpisodeAdapterFixture(SourceEpisodePublicChannelSegment))
	if err != nil {
		t.Fatal(err)
	}
	authority := &sourceEpisodeTestAuthority{want: sourceEpisodeExpectation(first)}
	writer := &sourceEpisodeTestWriter{}
	result, err := DualWriteCanonicalSourceEpisode(context.Background(), authority, writer, first)
	if err != nil || result.Replayed || !authority.callback || writer.writes != 1 {
		t.Fatalf("first dual-write failed: result=%+v err=%v authority=%+v writer=%+v", result, err, authority, writer)
	}
	authority.callback = false
	replay, err := DualWriteCanonicalSourceEpisode(context.Background(), authority, writer, first)
	if err != nil || !replay.Replayed || writer.writes != 1 {
		t.Fatalf("idempotent replay wrote twice: result=%+v err=%v writes=%d", replay, err, writer.writes)
	}

	drifted := first
	drifted.Authority.ACLRevision++
	drifted.Header.ContentDigest, _ = drifted.ContentDigest()
	driftAuthority := &sourceEpisodeTestAuthority{want: sourceEpisodeExpectation(drifted)}
	if _, err := DualWriteCanonicalSourceEpisode(context.Background(), driftAuthority, writer, drifted); !errors.Is(err, ErrSourceEpisodeConflict) {
		t.Fatalf("same idempotency key with authority drift replayed: %v", err)
	}
	if writer.writes != 1 {
		t.Fatalf("conflicting replay mutated canonical writer: %d", writer.writes)
	}

	input := sourceEpisodeAdapterFixture(SourceEpisodePublicChannelSegment)
	input.Header.Revision = 2
	input.Header.CreatedAt = input.Header.CreatedAt.Add(time.Minute)
	input.Source.ContentRevision++
	input.Source.ContentDigest = strings.Repeat("5", 64)
	input.RetrievalBody = input.Source
	prior := referenceFromHeader(first.Header)
	input.Supersedes = &prior
	second, err := AdaptPublicChannelSourceEpisode(input)
	if err != nil {
		t.Fatal(err)
	}
	secondAuthority := &sourceEpisodeTestAuthority{want: sourceEpisodeExpectation(second)}
	if _, err := DualWriteCanonicalSourceEpisode(context.Background(), secondAuthority, writer, second); err != nil {
		t.Fatalf("valid superseding source revision failed: %v", err)
	}
	if writer.writes != 2 || writer.current[first.Header.ID] != referenceFromHeader(second.Header) {
		t.Fatalf("revision CAS did not advance exactly once: %+v", writer)
	}

	blockedAuthority := &sourceEpisodeTestAuthority{want: sourceEpisodeExpectation(second), drift: true}
	beforeWrites := writer.writes
	if _, err := DualWriteCanonicalSourceEpisode(context.Background(), blockedAuthority, writer, second); !errors.Is(err, ErrSourceEpisodeConflict) {
		t.Fatalf("current authority drift did not fail closed: %v", err)
	}
	if blockedAuthority.callback || writer.writes != beforeWrites {
		t.Fatalf("authority drift reached dual writer: authority=%+v writes=%d", blockedAuthority, writer.writes)
	}
}
