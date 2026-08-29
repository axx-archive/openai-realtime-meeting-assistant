package main

// The meeting source episode is the first durable, post-close boundary between
// bounded meeting capture and the organizational brain. It is intentionally
// body-free: the analysis body remains behind an ACL-governed reference, while
// this contract freezes the exact transcript revisions and authority state
// from which that body was produced.

import (
	"context"
	"errors"
	"sort"
	"time"
)

const (
	MeetingSourceEpisodeRawExactSegments = "exact_segments_on_demand"
	meetingSourceEpisodeMaxRawSegments   = 64
)

var (
	ErrMeetingSourceEpisodeInvalid     = errors.New("meeting source episode is invalid")
	ErrMeetingSourceEpisodeStale       = errors.New("meeting source episode authority is stale")
	ErrMeetingSourceEpisodeConflict    = errors.New("meeting source episode revision conflicts with current publication")
	ErrMeetingSourceEpisodeUnavailable = errors.New("meeting source episode publication is unavailable")
	ErrMeetingSourceEpisodeRawScope    = errors.New("raw transcript request is not an exact source-episode segment set")
)

// MeetingSourceEpisodeSegment freezes one current transcript revision and the
// capture/authority values that admitted it into post-meeting analysis.
// ConsentFenceDigest contains only a digest of the current contributor fences;
// it never stores consent evidence bodies or microphone credentials.
type MeetingSourceEpisodeSegment struct {
	SegmentRef         STRIDEReference `json:"segmentRef"`
	TranscriptRef      STRIDEReference `json:"transcriptRef"`
	CaptureSequence    uint64          `json:"captureSequence"`
	SourceStart        time.Time       `json:"sourceStart"`
	SourceEnd          time.Time       `json:"sourceEnd"`
	ACLRevision        int64           `json:"aclRevision"`
	ConsentFenceDigest string          `json:"consentFenceDigest"`
	PurgeGeneration    int64           `json:"purgeGeneration"`
}

func (segment MeetingSourceEpisodeSegment) Validate() error {
	if segment.SegmentRef.Validate() != nil || segment.SegmentRef.ContractType != STRIDEContractTranscriptSegment ||
		segment.TranscriptRef.Validate() != nil || segment.TranscriptRef.ContractType != STRIDEContractTranscriptRevision ||
		segment.CaptureSequence == 0 || segment.SourceStart.IsZero() || segment.SourceEnd.IsZero() || !segment.SourceStart.Before(segment.SourceEnd) ||
		segment.SourceStart.Location() != time.UTC || segment.SourceEnd.Location() != time.UTC || segment.ACLRevision < 1 ||
		!isHexDigest(segment.ConsentFenceDigest) || segment.PurgeGeneration < 0 {
		return ErrMeetingSourceEpisodeInvalid
	}
	return nil
}

// MeetingSourceEpisode is a compact retrieval surface for one closed meeting.
// RawTranscriptAccess has exactly one legal value: ordinary retrieval uses the
// analysis body, while verification must name exact segment revisions through
// PlanRawTranscriptRead. A lifetime transcript corpus is not representable.
type MeetingSourceEpisode struct {
	Header                STRIDEContractHeader          `json:"header"`
	ConversationRef       STRIDEReference               `json:"conversationRef"`
	RoomID                string                        `json:"roomId"`
	SittingID             string                        `json:"sittingId"`
	MeetingStartedAt      time.Time                     `json:"meetingStartedAt"`
	MeetingClosedAt       time.Time                     `json:"meetingClosedAt"`
	MeetingEventHighWater uint64                        `json:"meetingEventHighWater"`
	TranscriptHighWater   uint64                        `json:"transcriptHighWater"`
	AnalysisHighWater     uint64                        `json:"analysisHighWater"`
	CaptureHighWater      uint64                        `json:"captureHighWater"`
	Segments              []MeetingSourceEpisodeSegment `json:"segments"`
	SourceManifestDigest  string                        `json:"sourceManifestDigest"`
	AnalysisRefs          []STRIDEReference             `json:"analysisRefs"`
	AnalysisBodyRef       string                        `json:"analysisBodyRef"`
	AnalysisBodyDigest    string                        `json:"analysisBodyDigest"`
	Audience              STRIDEAudience                `json:"audience"`
	ACLRevision           int64                         `json:"aclRevision"`
	ACLDigest             string                        `json:"aclDigest"`
	ConsentRevision       int64                         `json:"consentRevision"`
	ConsentDigest         string                        `json:"consentDigest"`
	PurgeGeneration       int64                         `json:"purgeGeneration"`
	RawTranscriptAccess   string                        `json:"rawTranscriptAccess"`
	Status                string                        `json:"status"`
	Supersedes            *STRIDEReference              `json:"supersedes,omitempty"`
	PublishedAt           time.Time                     `json:"publishedAt"`
}

func (episode MeetingSourceEpisode) Validate() error {
	if episode.Header.Validate(STRIDEContractMeetingSourceEpisode) != nil ||
		episode.ConversationRef.Validate() != nil || episode.ConversationRef.ContractType != STRIDEContractConversationEvent ||
		!strideIdentifier(episode.RoomID) || !strideIdentifier(episode.SittingID) || episode.MeetingStartedAt.IsZero() || episode.MeetingClosedAt.IsZero() ||
		episode.MeetingStartedAt.Location() != time.UTC || episode.MeetingClosedAt.Location() != time.UTC || !episode.MeetingStartedAt.Before(episode.MeetingClosedAt) ||
		episode.MeetingEventHighWater == 0 || episode.TranscriptHighWater == 0 || episode.TranscriptHighWater > episode.MeetingEventHighWater ||
		episode.AnalysisHighWater == 0 || episode.CaptureHighWater == 0 || !validMeetingSourceEpisodeSegments(episode.Segments, episode.CaptureHighWater, episode.PurgeGeneration) ||
		!isHexDigest(episode.SourceManifestDigest) || !validMeetingSourceEpisodeAnalysisRefs(episode.AnalysisRefs) || !strideIdentifier(episode.AnalysisBodyRef) ||
		!isHexDigest(episode.AnalysisBodyDigest) || episode.Audience.Validate() != nil || episode.ACLRevision < 1 || !isHexDigest(episode.ACLDigest) ||
		episode.ConsentRevision < 1 || !isHexDigest(episode.ConsentDigest) || episode.PurgeGeneration < 0 ||
		episode.RawTranscriptAccess != MeetingSourceEpisodeRawExactSegments || !oneOf(episode.Status, "published", "retracted") ||
		episode.PublishedAt.IsZero() || episode.PublishedAt.Location() != time.UTC || episode.PublishedAt.Before(episode.MeetingClosedAt) {
		return ErrMeetingSourceEpisodeInvalid
	}
	manifestDigest, err := STRIDEContractDigest(episode.Segments)
	if err != nil || manifestDigest != episode.SourceManifestDigest {
		return ErrMeetingSourceEpisodeInvalid
	}
	if (episode.Header.Revision > 1) != (episode.Supersedes != nil) || episode.Supersedes != nil &&
		(episode.Supersedes.Validate() != nil || episode.Supersedes.ContractType != STRIDEContractMeetingSourceEpisode ||
			episode.Supersedes.ID != episode.Header.ID || episode.Supersedes.Revision != episode.Header.Revision-1) {
		return ErrMeetingSourceEpisodeInvalid
	}
	contentDigest, err := episode.ContentDigest()
	if err != nil || contentDigest != episode.Header.ContentDigest {
		return ErrMeetingSourceEpisodeInvalid
	}
	return nil
}

func validMeetingSourceEpisodeSegments(segments []MeetingSourceEpisodeSegment, captureHighWater uint64, purgeGeneration int64) bool {
	if len(segments) == 0 {
		return false
	}
	seenSegments := make(map[string]bool, len(segments))
	seenRevisions := make(map[string]bool, len(segments))
	var priorCapture uint64
	for _, segment := range segments {
		if segment.Validate() != nil || segment.PurgeGeneration != purgeGeneration || segment.CaptureSequence <= priorCapture ||
			segment.CaptureSequence > captureHighWater || seenSegments[segment.SegmentRef.ID] || seenRevisions[segment.TranscriptRef.ID] {
			return false
		}
		seenSegments[segment.SegmentRef.ID] = true
		seenRevisions[segment.TranscriptRef.ID] = true
		priorCapture = segment.CaptureSequence
	}
	return true
}

func validMeetingSourceEpisodeAnalysisRefs(refs []STRIDEReference) bool {
	if !validateSTRIDERefs(refs) {
		return false
	}
	for _, ref := range refs {
		if ref.ContractType != STRIDEContractAnalysisProjection && ref.ContractType != STRIDEContractKnowledgeAssertion {
			return false
		}
	}
	return true
}

// ContentDigest covers every identity, high-water, source, authority, policy,
// and lineage field while excluding only the digest slot itself.
func (episode MeetingSourceEpisode) ContentDigest() (string, error) {
	header := episode.Header
	header.ContentDigest = ""
	material := struct {
		Header  STRIDEContractHeader
		Episode meetingSourceEpisodeDigestBody
	}{Header: header, Episode: episode.digestBody()}
	return STRIDEContractDigest(material)
}

type meetingSourceEpisodeDigestBody struct {
	ConversationRef       STRIDEReference
	RoomID                string
	SittingID             string
	MeetingStartedAt      time.Time
	MeetingClosedAt       time.Time
	MeetingEventHighWater uint64
	TranscriptHighWater   uint64
	AnalysisHighWater     uint64
	CaptureHighWater      uint64
	Segments              []MeetingSourceEpisodeSegment
	SourceManifestDigest  string
	AnalysisRefs          []STRIDEReference
	AnalysisBodyRef       string
	AnalysisBodyDigest    string
	Audience              STRIDEAudience
	ACLRevision           int64
	ACLDigest             string
	ConsentRevision       int64
	ConsentDigest         string
	PurgeGeneration       int64
	RawTranscriptAccess   string
	Status                string
	Supersedes            *STRIDEReference
	PublishedAt           time.Time
}

func (episode MeetingSourceEpisode) digestBody() meetingSourceEpisodeDigestBody {
	return meetingSourceEpisodeDigestBody{
		ConversationRef: episode.ConversationRef, RoomID: episode.RoomID, SittingID: episode.SittingID,
		MeetingStartedAt: episode.MeetingStartedAt, MeetingClosedAt: episode.MeetingClosedAt,
		MeetingEventHighWater: episode.MeetingEventHighWater, TranscriptHighWater: episode.TranscriptHighWater,
		AnalysisHighWater: episode.AnalysisHighWater, CaptureHighWater: episode.CaptureHighWater,
		Segments: episode.Segments, SourceManifestDigest: episode.SourceManifestDigest, AnalysisRefs: episode.AnalysisRefs,
		AnalysisBodyRef: episode.AnalysisBodyRef, AnalysisBodyDigest: episode.AnalysisBodyDigest, Audience: episode.Audience,
		ACLRevision: episode.ACLRevision, ACLDigest: episode.ACLDigest, ConsentRevision: episode.ConsentRevision,
		ConsentDigest: episode.ConsentDigest, PurgeGeneration: episode.PurgeGeneration,
		RawTranscriptAccess: episode.RawTranscriptAccess, Status: episode.Status, Supersedes: episode.Supersedes, PublishedAt: episode.PublishedAt,
	}
}

// MeetingSourceEpisodePublicationPreconditions are the exact values an
// adapter must hold current through the durable repository commit. A database
// adapter should lock the meeting, source revisions, ACL grants, consent rows,
// and purge generation before invoking the supplied commit callback.
type MeetingSourceEpisodePublicationPreconditions struct {
	TenantID              string
	RoomID                string
	SittingID             string
	ConversationRef       STRIDEReference
	MeetingClosedAt       time.Time
	MeetingEventHighWater uint64
	TranscriptHighWater   uint64
	AnalysisHighWater     uint64
	CaptureHighWater      uint64
	SourceManifestDigest  string
	Segments              []MeetingSourceEpisodeSegment
	Audience              STRIDEAudience
	ACLRevision           int64
	ACLDigest             string
	ConsentRevision       int64
	ConsentDigest         string
	PurgeGeneration       int64
}

func (episode MeetingSourceEpisode) PublicationPreconditions() MeetingSourceEpisodePublicationPreconditions {
	return MeetingSourceEpisodePublicationPreconditions{
		TenantID: episode.Header.TenantID, RoomID: episode.RoomID, SittingID: episode.SittingID, ConversationRef: episode.ConversationRef,
		MeetingClosedAt: episode.MeetingClosedAt, MeetingEventHighWater: episode.MeetingEventHighWater, TranscriptHighWater: episode.TranscriptHighWater,
		AnalysisHighWater: episode.AnalysisHighWater, CaptureHighWater: episode.CaptureHighWater, SourceManifestDigest: episode.SourceManifestDigest,
		Segments: append([]MeetingSourceEpisodeSegment(nil), episode.Segments...), Audience: cloneAudience(episode.Audience),
		ACLRevision: episode.ACLRevision, ACLDigest: episode.ACLDigest, ConsentRevision: episode.ConsentRevision,
		ConsentDigest: episode.ConsentDigest, PurgeGeneration: episode.PurgeGeneration,
	}
}

func (preconditions MeetingSourceEpisodePublicationPreconditions) Validate() error {
	if !strideIdentifier(preconditions.TenantID) || !strideIdentifier(preconditions.RoomID) || !strideIdentifier(preconditions.SittingID) ||
		preconditions.ConversationRef.Validate() != nil || preconditions.ConversationRef.ContractType != STRIDEContractConversationEvent ||
		preconditions.MeetingClosedAt.IsZero() || preconditions.MeetingClosedAt.Location() != time.UTC || preconditions.MeetingEventHighWater == 0 ||
		preconditions.TranscriptHighWater == 0 || preconditions.TranscriptHighWater > preconditions.MeetingEventHighWater || preconditions.AnalysisHighWater == 0 ||
		preconditions.CaptureHighWater == 0 || !validMeetingSourceEpisodeSegments(preconditions.Segments, preconditions.CaptureHighWater, preconditions.PurgeGeneration) ||
		!isHexDigest(preconditions.SourceManifestDigest) || preconditions.Audience.Validate() != nil || preconditions.ACLRevision < 1 ||
		!isHexDigest(preconditions.ACLDigest) || preconditions.ConsentRevision < 1 || !isHexDigest(preconditions.ConsentDigest) || preconditions.PurgeGeneration < 0 {
		return ErrMeetingSourceEpisodeInvalid
	}
	digest, err := STRIDEContractDigest(preconditions.Segments)
	if err != nil || digest != preconditions.SourceManifestDigest {
		return ErrMeetingSourceEpisodeInvalid
	}
	return nil
}

type MeetingSourceEpisodeAuthority interface {
	WithCurrentMeetingSourceEpisode(context.Context, MeetingSourceEpisodePublicationPreconditions, func() error) error
}

// MeetingSourceEpisodeRepository must commit one immutable revision and its
// current-pointer compare-and-swap atomically. An error must leave neither the
// revision nor its current pointer visible.
type MeetingSourceEpisodeRepository interface {
	CommitMeetingSourceEpisode(context.Context, MeetingSourceEpisode, *STRIDEReference) error
}

type MeetingSourceEpisodePublisher struct {
	Authority  MeetingSourceEpisodeAuthority
	Repository MeetingSourceEpisodeRepository
}

// Publish keeps source authority current until durable publication completes.
// It owns no in-memory cache, so a failed repository commit cannot create
// retrievable ghost evidence.
func (publisher MeetingSourceEpisodePublisher) Publish(ctx context.Context, episode MeetingSourceEpisode) error {
	if publisher.Authority == nil || publisher.Repository == nil {
		return ErrMeetingSourceEpisodeUnavailable
	}
	if err := episode.Validate(); err != nil {
		return err
	}
	preconditions := episode.PublicationPreconditions()
	if err := preconditions.Validate(); err != nil {
		return err
	}
	return publisher.Authority.WithCurrentMeetingSourceEpisode(ctx, preconditions, func() error {
		return publisher.Repository.CommitMeetingSourceEpisode(ctx, episode, episode.Supersedes)
	})
}

// MeetingSourceEpisodeRetrievalPlan defaults to the compact analysis artifact.
// RawSegments is empty unless a caller names exact transcript revisions from
// this episode; there is no method or selector for a lifetime transcript file.
type MeetingSourceEpisodeRetrievalPlan struct {
	EpisodeRef      STRIDEReference               `json:"episodeRef"`
	AnalysisBodyRef string                        `json:"analysisBodyRef"`
	AnalysisRefs    []STRIDEReference             `json:"analysisRefs"`
	RawSegments     []MeetingSourceEpisodeSegment `json:"rawSegments"`
}

func (episode MeetingSourceEpisode) PlanRawTranscriptRead(requested []STRIDEReference) (MeetingSourceEpisodeRetrievalPlan, error) {
	if err := episode.Validate(); err != nil {
		return MeetingSourceEpisodeRetrievalPlan{}, err
	}
	if episode.Status != "published" {
		return MeetingSourceEpisodeRetrievalPlan{}, ErrMeetingSourceEpisodeRawScope
	}
	plan := MeetingSourceEpisodeRetrievalPlan{
		EpisodeRef: referenceFromHeader(episode.Header), AnalysisBodyRef: episode.AnalysisBodyRef,
		AnalysisRefs: append([]STRIDEReference(nil), episode.AnalysisRefs...),
	}
	if len(requested) == 0 {
		return plan, nil
	}
	if len(requested) > meetingSourceEpisodeMaxRawSegments {
		return MeetingSourceEpisodeRetrievalPlan{}, ErrMeetingSourceEpisodeRawScope
	}
	byRevision := make(map[STRIDEReference]MeetingSourceEpisodeSegment, len(episode.Segments))
	for _, segment := range episode.Segments {
		byRevision[segment.TranscriptRef] = segment
	}
	seen := make(map[STRIDEReference]bool, len(requested))
	for _, ref := range requested {
		segment, found := byRevision[ref]
		if ref.Validate() != nil || ref.ContractType != STRIDEContractTranscriptRevision || !found || seen[ref] {
			return MeetingSourceEpisodeRetrievalPlan{}, ErrMeetingSourceEpisodeRawScope
		}
		seen[ref] = true
		plan.RawSegments = append(plan.RawSegments, segment)
	}
	sort.Slice(plan.RawSegments, func(i, j int) bool { return plan.RawSegments[i].CaptureSequence < plan.RawSegments[j].CaptureSequence })
	return plan, nil
}
