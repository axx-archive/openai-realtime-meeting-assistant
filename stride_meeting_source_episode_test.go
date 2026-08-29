package main

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

var meetingSourceEpisodeTestTime = time.Date(2026, 8, 25, 14, 0, 0, 0, time.UTC)

func meetingSourceEpisodeTestRef(kind STRIDEContractType, id string, revision int64, digit string) STRIDEReference {
	return STRIDEReference{ContractType: kind, ID: id, Revision: revision, Digest: strings.Repeat(digit, 64)}
}

func meetingSourceEpisodeFixture(t *testing.T) MeetingSourceEpisode {
	t.Helper()
	segments := []MeetingSourceEpisodeSegment{
		{
			SegmentRef:      meetingSourceEpisodeTestRef(STRIDEContractTranscriptSegment, "segment_11", 1, "1"),
			TranscriptRef:   meetingSourceEpisodeTestRef(STRIDEContractTranscriptRevision, "transcript_11", 1, "2"),
			CaptureSequence: 11, SourceStart: meetingSourceEpisodeTestTime, SourceEnd: meetingSourceEpisodeTestTime.Add(time.Minute),
			ACLRevision: 4, ConsentFenceDigest: strings.Repeat("3", 64), PurgeGeneration: 7,
		},
		{
			SegmentRef:      meetingSourceEpisodeTestRef(STRIDEContractTranscriptSegment, "segment_12", 2, "4"),
			TranscriptRef:   meetingSourceEpisodeTestRef(STRIDEContractTranscriptRevision, "transcript_12", 2, "5"),
			CaptureSequence: 12, SourceStart: meetingSourceEpisodeTestTime.Add(time.Minute), SourceEnd: meetingSourceEpisodeTestTime.Add(2 * time.Minute),
			ACLRevision: 4, ConsentFenceDigest: strings.Repeat("6", 64), PurgeGeneration: 7,
		},
	}
	manifest, err := STRIDEContractDigest(segments)
	if err != nil {
		t.Fatal(err)
	}
	episode := MeetingSourceEpisode{
		Header: STRIDEContractHeader{
			TenantID: "org_1", ID: "meeting_episode_1", Revision: 1, SchemaVersion: STRIDEContractSchemaVersion,
			ContractType: STRIDEContractMeetingSourceEpisode, CreatedAt: meetingSourceEpisodeTestTime.Add(12 * time.Minute),
		},
		ConversationRef: meetingSourceEpisodeTestRef(STRIDEContractConversationEvent, "meeting_conversation_1", 8, "7"),
		RoomID:          "room_1", SittingID: "sitting_1", MeetingStartedAt: meetingSourceEpisodeTestTime,
		MeetingClosedAt: meetingSourceEpisodeTestTime.Add(10 * time.Minute), MeetingEventHighWater: 32,
		TranscriptHighWater: 30, AnalysisHighWater: 9, CaptureHighWater: 12, Segments: segments, SourceManifestDigest: manifest,
		AnalysisRefs: []STRIDEReference{
			meetingSourceEpisodeTestRef(STRIDEContractAnalysisProjection, "analysis_decisions_1", 1, "8"),
			meetingSourceEpisodeTestRef(STRIDEContractKnowledgeAssertion, "analysis_commitments_1", 1, "9"),
		},
		AnalysisBodyRef: "meeting_analysis_body_1", AnalysisBodyDigest: strings.Repeat("a", 64),
		Audience:    STRIDEAudience{Visibility: "meeting", Principals: []string{"person_1", "person_2"}},
		ACLRevision: 4, ACLDigest: strings.Repeat("b", 64), ConsentRevision: 6, ConsentDigest: strings.Repeat("c", 64), PurgeGeneration: 7,
		RawTranscriptAccess: MeetingSourceEpisodeRawExactSegments, Status: "published", PublishedAt: meetingSourceEpisodeTestTime.Add(12 * time.Minute),
	}
	episode.Header.ContentDigest, err = episode.ContentDigest()
	if err != nil {
		t.Fatal(err)
	}
	return episode
}

func TestMeetingSourceEpisodeFreezesBoundedPostMeetingAuthority(t *testing.T) {
	episode := meetingSourceEpisodeFixture(t)
	if err := episode.Validate(); err != nil {
		t.Fatalf("valid source episode rejected: %v", err)
	}
	preconditions := episode.PublicationPreconditions()
	if err := preconditions.Validate(); err != nil {
		t.Fatalf("publication preconditions rejected: %v", err)
	}
	if preconditions.TranscriptHighWater != 30 || preconditions.MeetingEventHighWater != 32 || preconditions.CaptureHighWater != 12 ||
		preconditions.ACLRevision != 4 || preconditions.ConsentRevision != 6 || preconditions.PurgeGeneration != 7 || len(preconditions.Segments) != 2 {
		t.Fatalf("publication preconditions lost exact authority: %+v", preconditions)
	}

	tests := []struct {
		name   string
		mutate func(*MeetingSourceEpisode)
	}{
		{"transcript beyond meeting high-water", func(v *MeetingSourceEpisode) { v.TranscriptHighWater = v.MeetingEventHighWater + 1 }},
		{"segment beyond capture high-water", func(v *MeetingSourceEpisode) { v.Segments[1].CaptureSequence = v.CaptureHighWater + 1 }},
		{"segment purge drift", func(v *MeetingSourceEpisode) { v.Segments[1].PurgeGeneration++ }},
		{"source manifest drift", func(v *MeetingSourceEpisode) { v.SourceManifestDigest = strings.Repeat("d", 64) }},
		{"acl unbound", func(v *MeetingSourceEpisode) { v.ACLRevision = 0 }},
		{"consent unbound", func(v *MeetingSourceEpisode) { v.ConsentDigest = "" }},
		{"lifetime raw query policy", func(v *MeetingSourceEpisode) { v.RawTranscriptAccess = "lifetime_corpus" }},
		{"body mutation", func(v *MeetingSourceEpisode) { v.AnalysisBodyDigest = strings.Repeat("e", 64) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			candidate := meetingSourceEpisodeFixture(t)
			tt.mutate(&candidate)
			if err := candidate.Validate(); !errors.Is(err, ErrMeetingSourceEpisodeInvalid) {
				t.Fatalf("mutation accepted: %v", err)
			}
		})
	}
}

func TestMeetingSourceEpisodeRevisionRequiresExactSupersession(t *testing.T) {
	first := meetingSourceEpisodeFixture(t)
	next := first
	next.Header.Revision = 2
	next.Header.CreatedAt = next.Header.CreatedAt.Add(time.Minute)
	next.PublishedAt = next.PublishedAt.Add(time.Minute)
	next.Supersedes = func() *STRIDEReference { ref := referenceFromHeader(first.Header); return &ref }()
	next.Header.ContentDigest, _ = next.ContentDigest()
	if err := next.Validate(); err != nil {
		t.Fatalf("valid superseding revision rejected: %v", err)
	}
	forged := next
	forged.Supersedes = func() *STRIDEReference {
		ref := referenceFromHeader(first.Header)
		ref.Digest = strings.Repeat("f", 64)
		return &ref
	}()
	forged.Header.ContentDigest, _ = forged.ContentDigest()
	if err := forged.Validate(); err != nil {
		t.Fatalf("body-bound lineage reference should be structurally valid before repository CAS: %v", err)
	}
	current := referenceFromHeader(first.Header)
	repository := &meetingSourceEpisodeTestRepository{current: &current, stored: map[int64]MeetingSourceEpisode{1: first}}
	authority := &meetingSourceEpisodeTestAuthority{want: forged.PublicationPreconditions()}
	if err := (MeetingSourceEpisodePublisher{Authority: authority, Repository: repository}).Publish(context.Background(), forged); !errors.Is(err, ErrMeetingSourceEpisodeConflict) {
		t.Fatalf("forged lineage bypassed exact current-pointer CAS: %v", err)
	}
}

func TestMeetingSourceEpisodeRawTranscriptReadsAreExactAndOptIn(t *testing.T) {
	episode := meetingSourceEpisodeFixture(t)
	plan, err := episode.PlanRawTranscriptRead(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.RawSegments) != 0 || plan.AnalysisBodyRef != episode.AnalysisBodyRef || !reflect.DeepEqual(plan.AnalysisRefs, episode.AnalysisRefs) {
		t.Fatalf("default retrieval did not remain analysis-only: %+v", plan)
	}
	requested := []STRIDEReference{episode.Segments[1].TranscriptRef, episode.Segments[0].TranscriptRef}
	plan, err = episode.PlanRawTranscriptRead(requested)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.RawSegments) != 2 || plan.RawSegments[0].CaptureSequence != 11 || plan.RawSegments[1].CaptureSequence != 12 {
		t.Fatalf("exact segments were not returned in capture order: %+v", plan.RawSegments)
	}
	foreign := meetingSourceEpisodeTestRef(STRIDEContractTranscriptRevision, "foreign_revision", 1, "f")
	if _, err := episode.PlanRawTranscriptRead([]STRIDEReference{foreign}); !errors.Is(err, ErrMeetingSourceEpisodeRawScope) {
		t.Fatalf("foreign raw revision accepted: %v", err)
	}
	if _, err := episode.PlanRawTranscriptRead([]STRIDEReference{requested[0], requested[0]}); !errors.Is(err, ErrMeetingSourceEpisodeRawScope) {
		t.Fatalf("duplicate raw revision accepted: %v", err)
	}
	retracted := episode
	retracted.Header.Revision = 2
	retracted.Status = "retracted"
	retracted.PublishedAt = retracted.PublishedAt.Add(time.Minute)
	retracted.Supersedes = func() *STRIDEReference { ref := referenceFromHeader(episode.Header); return &ref }()
	retracted.Header.ContentDigest, _ = retracted.ContentDigest()
	if _, err := retracted.PlanRawTranscriptRead(nil); !errors.Is(err, ErrMeetingSourceEpisodeRawScope) {
		t.Fatalf("retracted episode remained retrievable: %v", err)
	}
}

type meetingSourceEpisodeTestAuthority struct {
	want     MeetingSourceEpisodePublicationPreconditions
	drift    bool
	callback bool
}

func (authority *meetingSourceEpisodeTestAuthority) WithCurrentMeetingSourceEpisode(_ context.Context, got MeetingSourceEpisodePublicationPreconditions, use func() error) error {
	if authority.drift || !reflect.DeepEqual(authority.want, got) {
		return ErrMeetingSourceEpisodeStale
	}
	authority.callback = true
	return use()
}

type meetingSourceEpisodeTestRepository struct {
	current *STRIDEReference
	stored  map[int64]MeetingSourceEpisode
	fail    error
	commits int
}

func (repository *meetingSourceEpisodeTestRepository) CommitMeetingSourceEpisode(_ context.Context, episode MeetingSourceEpisode, expected *STRIDEReference) error {
	repository.commits++
	if repository.fail != nil {
		return repository.fail
	}
	if (expected == nil) != (repository.current == nil) || expected != nil && *expected != *repository.current {
		return ErrMeetingSourceEpisodeConflict
	}
	if repository.stored == nil {
		repository.stored = map[int64]MeetingSourceEpisode{}
	}
	repository.stored[episode.Header.Revision] = episode
	ref := referenceFromHeader(episode.Header)
	repository.current = &ref
	return nil
}

func TestMeetingSourceEpisodePublicationHoldsAuthorityThroughAtomicCommit(t *testing.T) {
	episode := meetingSourceEpisodeFixture(t)
	authority := &meetingSourceEpisodeTestAuthority{want: episode.PublicationPreconditions()}
	repository := &meetingSourceEpisodeTestRepository{}
	publisher := MeetingSourceEpisodePublisher{Authority: authority, Repository: repository}
	if err := publisher.Publish(context.Background(), episode); err != nil {
		t.Fatalf("publish failed: %v", err)
	}
	if !authority.callback || repository.commits != 1 || repository.current == nil || repository.current.Digest != episode.Header.ContentDigest {
		t.Fatalf("publication did not pass through authority-fenced commit: authority=%+v repository=%+v", authority, repository)
	}

	staleEpisode := meetingSourceEpisodeFixture(t)
	staleAuthority := &meetingSourceEpisodeTestAuthority{want: staleEpisode.PublicationPreconditions(), drift: true}
	staleRepository := &meetingSourceEpisodeTestRepository{}
	if err := (MeetingSourceEpisodePublisher{Authority: staleAuthority, Repository: staleRepository}).Publish(context.Background(), staleEpisode); !errors.Is(err, ErrMeetingSourceEpisodeStale) {
		t.Fatalf("authority drift did not fail closed: %v", err)
	}
	if staleAuthority.callback || staleRepository.commits != 0 || len(staleRepository.stored) != 0 {
		t.Fatalf("stale authority reached publication: authority=%+v repository=%+v", staleAuthority, staleRepository)
	}

	saveErr := errors.New("durable save failed")
	failingRepository := &meetingSourceEpisodeTestRepository{fail: saveErr}
	failingAuthority := &meetingSourceEpisodeTestAuthority{want: episode.PublicationPreconditions()}
	if err := (MeetingSourceEpisodePublisher{Authority: failingAuthority, Repository: failingRepository}).Publish(context.Background(), episode); !errors.Is(err, saveErr) {
		t.Fatalf("save failure was not returned: %v", err)
	}
	if failingRepository.current != nil || len(failingRepository.stored) != 0 {
		t.Fatalf("failed save left ghost publication: %+v", failingRepository)
	}
}
