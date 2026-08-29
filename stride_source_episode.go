package main

// SourceEpisode is the canonical, body-free dual-write envelope for durable
// company context. Source bodies stay in their native ACL-governed stores; the
// envelope records only an exact immutable revision and the authority/scope
// snapshot required to rebuild derived brain projections.

import (
	"context"
	"errors"
	"sort"
	"time"
)

type SourceEpisodeKind string

const (
	SourceEpisodeMeetingAnalysis            SourceEpisodeKind = "meeting_analysis"
	SourceEpisodePublicChannelSegment       SourceEpisodeKind = "public_channel_segment"
	SourceEpisodePrivateConversationSegment SourceEpisodeKind = "private_conversation_segment"
	SourceEpisodeRealtimeVoiceSession       SourceEpisodeKind = "realtime_voice_session"
	SourceEpisodeDriveFileRevision          SourceEpisodeKind = "drive_file_revision"
	SourceEpisodeWorkArtifactRevision       SourceEpisodeKind = "work_artifact_revision"

	SourceEpisodePhasePostClose  = "post_close"
	SourceEpisodePhasePostCommit = "post_commit"

	SourceEpisodeMemoryConversation = "conversation"
	SourceEpisodeMemoryPerson       = "person"
	SourceEpisodeMemoryProject      = "project"
	SourceEpisodeMemoryCompany      = "company"

	SourceEpisodeFamilyMeetingAnalysis      = "meeting_source_episode"
	SourceEpisodeFamilyMeetingAnalysisBody  = "meeting_analysis"
	SourceEpisodeFamilyConversationEvent    = "conversation_event"
	SourceEpisodeFamilyRealtimeVoiceSession = "realtime_voice_session"
	SourceEpisodeFamilyDriveFileRevision    = "file_revision"
	SourceEpisodeFamilyWorkArtifactRevision = "work_artifact_revision"

	SourceEpisodeBoundaryMeetingClose       = "meeting_close"
	SourceEpisodeBoundaryRealtimeVoiceClose = "realtime_voice_close"
	SourceEpisodeBoundaryConversationCommit = "conversation_commit"
	SourceEpisodeBoundaryDriveCommit        = "drive_revision_commit"
	SourceEpisodeBoundaryWorkCommit         = "work_artifact_commit"
)

var (
	ErrSourceEpisodeInvalid     = errors.New("source episode is invalid")
	ErrSourceEpisodePrivacy     = errors.New("source episode would widen private source scope")
	ErrSourceEpisodePhase       = errors.New("source episode is not admitted from a post-close or post-commit phase")
	ErrSourceEpisodeConflict    = errors.New("source episode dual-write conflicts with existing idempotency or lineage")
	ErrSourceEpisodeUnavailable = errors.New("source episode dual-write is unavailable")
)

// SourceEpisodeRevisionRef is deliberately generic because Drive and work
// artifact revisions do not share a STRIDE contract type with conversations.
// It still has the same exact identity tuple used by canonical object storage.
type SourceEpisodeRevisionRef struct {
	SourceFamily    string `json:"sourceFamily"`
	ObjectID        string `json:"objectId"`
	ContentRevision int64  `json:"contentRevision"`
	ContentDigest   string `json:"contentDigest"`
	SizeBytes       int64  `json:"sizeBytes"`
}

func (ref SourceEpisodeRevisionRef) Validate() error {
	if !strideIdentifier(ref.SourceFamily) || !strideIdentifier(ref.ObjectID) || ref.ContentRevision < 1 || !isHexDigest(ref.ContentDigest) || ref.SizeBytes < 1 {
		return ErrSourceEpisodeInvalid
	}
	return nil
}

// SourceEpisodeScope keeps association separate from disclosure authority.
// CompanyID is tenancy, while project/conversation/person IDs are stable
// retrieval joins. None of these fields by itself grants access.
type SourceEpisodeScope struct {
	CompanyID      string   `json:"companyId"`
	ProjectIDs     []string `json:"projectIds,omitempty"`
	ConversationID string   `json:"conversationId,omitempty"`
	PersonIDs      []string `json:"personIds,omitempty"`
	RoomID         string   `json:"roomId,omitempty"`
	SittingID      string   `json:"sittingId,omitempty"`
	MemoryScope    string   `json:"memoryScope"`
}

func (scope SourceEpisodeScope) Validate() error {
	if !strideIdentifier(scope.CompanyID) || !oneOf(scope.MemoryScope, SourceEpisodeMemoryConversation, SourceEpisodeMemoryPerson, SourceEpisodeMemoryProject, SourceEpisodeMemoryCompany) ||
		!validOptionalSourceEpisodeIDs(scope.ProjectIDs) || !validOptionalSTRIDEID(scope.ConversationID) || !validOptionalSourceEpisodeIDs(scope.PersonIDs) ||
		!validOptionalSTRIDEID(scope.RoomID) || !validOptionalSTRIDEID(scope.SittingID) || (scope.RoomID == "") != (scope.SittingID == "") {
		return ErrSourceEpisodeInvalid
	}
	if scope.MemoryScope == SourceEpisodeMemoryConversation && scope.ConversationID == "" || scope.MemoryScope == SourceEpisodeMemoryPerson && len(scope.PersonIDs) == 0 ||
		scope.MemoryScope == SourceEpisodeMemoryProject && len(scope.ProjectIDs) == 0 {
		return ErrSourceEpisodeInvalid
	}
	return nil
}

// SourceEpisodeAuthoritySnapshot binds every source kind to the same minimum
// revocation surface. ConsentRevision may describe processing/disclosure policy
// for non-audio sources; its digest remains body-free.
type SourceEpisodeAuthoritySnapshot struct {
	Audience        STRIDEAudience `json:"audience"`
	ACLRevision     int64          `json:"aclRevision"`
	ACLDigest       string         `json:"aclDigest"`
	ConsentRevision int64          `json:"consentRevision"`
	ConsentDigest   string         `json:"consentDigest"`
	PurgeGeneration int64          `json:"purgeGeneration"`
	RetentionPolicy string         `json:"retentionPolicy"`
	ObservedAt      time.Time      `json:"observedAt"`
}

func (snapshot SourceEpisodeAuthoritySnapshot) Validate() error {
	if snapshot.Audience.Validate() != nil || snapshot.ACLRevision < 1 || !isHexDigest(snapshot.ACLDigest) || snapshot.ConsentRevision < 1 ||
		!isHexDigest(snapshot.ConsentDigest) || snapshot.PurgeGeneration < 0 || !strideIdentifier(snapshot.RetentionPolicy) ||
		snapshot.ObservedAt.IsZero() || snapshot.ObservedAt.Location() != time.UTC {
		return ErrSourceEpisodeInvalid
	}
	return nil
}

// SourceEpisodePhaseProof prevents this adapter boundary from representing an
// in-flight RTP/capture write. Meeting and voice sources require a durable
// close receipt; all other sources require their native commit receipt.
type SourceEpisodePhaseProof struct {
	Phase         string    `json:"phase"`
	BoundaryType  string    `json:"boundaryType"`
	BoundaryAt    time.Time `json:"boundaryAt"`
	ReceiptDigest string    `json:"receiptDigest"`
}

func (proof SourceEpisodePhaseProof) Validate() error {
	if !oneOf(proof.Phase, SourceEpisodePhasePostClose, SourceEpisodePhasePostCommit) || !strideIdentifier(proof.BoundaryType) || proof.BoundaryAt.IsZero() ||
		proof.BoundaryAt.Location() != time.UTC || !isHexDigest(proof.ReceiptDigest) {
		return ErrSourceEpisodePhase
	}
	return nil
}

type SourceEpisode struct {
	Header                     STRIDEContractHeader           `json:"header"`
	Kind                       SourceEpisodeKind              `json:"kind"`
	Source                     SourceEpisodeRevisionRef       `json:"source"`
	RetrievalBody              SourceEpisodeRevisionRef       `json:"retrievalBody"`
	Scope                      SourceEpisodeScope             `json:"scope"`
	Authority                  SourceEpisodeAuthoritySnapshot `json:"authority"`
	OccurredStart              time.Time                      `json:"occurredStart"`
	OccurredEnd                time.Time                      `json:"occurredEnd"`
	PhaseProof                 SourceEpisodePhaseProof        `json:"phaseProof"`
	RawMeetingTranscriptAccess string                         `json:"rawMeetingTranscriptAccess,omitempty"`
	IdempotencyKeyDigest       string                         `json:"idempotencyKeyDigest"`
	Supersedes                 *STRIDEReference               `json:"supersedes,omitempty"`
}

func (episode SourceEpisode) Validate() error {
	if episode.Header.Validate(STRIDEContractSourceEpisode) != nil || !validSourceEpisodeKind(episode.Kind) || episode.Source.Validate() != nil || episode.RetrievalBody.Validate() != nil ||
		!validSourceEpisodeNativeFamily(episode.Kind, episode.Source.SourceFamily) || episode.Scope.Validate() != nil || episode.Scope.CompanyID != episode.Header.TenantID ||
		episode.Authority.Validate() != nil || episode.OccurredStart.IsZero() || episode.OccurredEnd.IsZero() || !episode.OccurredStart.Before(episode.OccurredEnd) ||
		episode.OccurredStart.Location() != time.UTC || episode.OccurredEnd.Location() != time.UTC || episode.PhaseProof.Validate() != nil ||
		episode.PhaseProof.BoundaryAt.Before(episode.OccurredEnd) || episode.Authority.ObservedAt.Before(episode.PhaseProof.BoundaryAt) ||
		episode.Header.CreatedAt.Before(episode.Authority.ObservedAt) || !isHexDigest(episode.IdempotencyKeyDigest) {
		return ErrSourceEpisodeInvalid
	}
	if episode.IdempotencyKeyDigest != SourceEpisodeIdempotencyKey(episode.Header.TenantID, episode.Kind, episode.Source) {
		return ErrSourceEpisodeInvalid
	}
	if err := validateSourceEpisodeKindContract(episode); err != nil {
		return err
	}
	if (episode.Header.Revision > 1) != (episode.Supersedes != nil) || episode.Supersedes != nil &&
		(episode.Supersedes.Validate() != nil || episode.Supersedes.ContractType != STRIDEContractSourceEpisode ||
			episode.Supersedes.ID != episode.Header.ID || episode.Supersedes.Revision != episode.Header.Revision-1) {
		return ErrSourceEpisodeInvalid
	}
	digest, err := episode.ContentDigest()
	if err != nil || digest != episode.Header.ContentDigest {
		return ErrSourceEpisodeInvalid
	}
	return nil
}

func validateSourceEpisodeKindContract(episode SourceEpisode) error {
	switch episode.Kind {
	case SourceEpisodeMeetingAnalysis:
		if episode.PhaseProof.Phase != SourceEpisodePhasePostClose || episode.Scope.ConversationID == "" ||
			episode.Scope.RoomID == "" || episode.PhaseProof.BoundaryType != SourceEpisodeBoundaryMeetingClose ||
			episode.RetrievalBody.SourceFamily != SourceEpisodeFamilyMeetingAnalysisBody || episode.RawMeetingTranscriptAccess != MeetingSourceEpisodeRawExactSegments {
			return ErrSourceEpisodePhase
		}
	case SourceEpisodeRealtimeVoiceSession:
		if episode.PhaseProof.Phase != SourceEpisodePhasePostClose || episode.Scope.ConversationID == "" || len(episode.Scope.PersonIDs) == 0 ||
			episode.PhaseProof.BoundaryType != SourceEpisodeBoundaryRealtimeVoiceClose || episode.RawMeetingTranscriptAccess != "" {
			return ErrSourceEpisodePhase
		}
		if err := validatePrivateSourceEpisode(episode); err != nil {
			return err
		}
	case SourceEpisodePublicChannelSegment:
		if episode.PhaseProof.Phase != SourceEpisodePhasePostCommit || episode.PhaseProof.BoundaryType != SourceEpisodeBoundaryConversationCommit {
			return ErrSourceEpisodePhase
		}
		if episode.Scope.ConversationID == "" || !oneOf(episode.Authority.Audience.Visibility, "channel", "organization") || episode.RawMeetingTranscriptAccess != "" {
			return ErrSourceEpisodeInvalid
		}
	case SourceEpisodePrivateConversationSegment:
		if episode.PhaseProof.Phase != SourceEpisodePhasePostCommit || episode.Scope.ConversationID == "" || len(episode.Scope.PersonIDs) == 0 ||
			episode.PhaseProof.BoundaryType != SourceEpisodeBoundaryConversationCommit || episode.RawMeetingTranscriptAccess != "" {
			return ErrSourceEpisodePhase
		}
		if err := validatePrivateSourceEpisode(episode); err != nil {
			return err
		}
	case SourceEpisodeDriveFileRevision:
		if episode.PhaseProof.Phase != SourceEpisodePhasePostCommit || episode.PhaseProof.BoundaryType != SourceEpisodeBoundaryDriveCommit || episode.RawMeetingTranscriptAccess != "" {
			return ErrSourceEpisodePhase
		}
	case SourceEpisodeWorkArtifactRevision:
		if episode.PhaseProof.Phase != SourceEpisodePhasePostCommit || episode.PhaseProof.BoundaryType != SourceEpisodeBoundaryWorkCommit || len(episode.Scope.ProjectIDs) == 0 || episode.RawMeetingTranscriptAccess != "" {
			return ErrSourceEpisodePhase
		}
	default:
		return ErrSourceEpisodeInvalid
	}
	if episode.Kind != SourceEpisodeMeetingAnalysis && episode.RetrievalBody != episode.Source {
		return ErrSourceEpisodeInvalid
	}
	return nil
}

func validatePrivateSourceEpisode(episode SourceEpisode) error {
	if episode.Authority.Audience.Visibility != "private" ||
		episode.Scope.MemoryScope == SourceEpisodeMemoryCompany || episode.Scope.MemoryScope == SourceEpisodeMemoryProject {
		return ErrSourceEpisodePrivacy
	}
	allowed := make(map[string]bool, len(episode.Authority.Audience.Principals))
	for _, principal := range episode.Authority.Audience.Principals {
		allowed[principal] = true
	}
	for _, person := range episode.Scope.PersonIDs {
		if !allowed[person] {
			return ErrSourceEpisodePrivacy
		}
	}
	return nil
}

func validSourceEpisodeKind(kind SourceEpisodeKind) bool {
	switch kind {
	case SourceEpisodeMeetingAnalysis, SourceEpisodePublicChannelSegment, SourceEpisodePrivateConversationSegment,
		SourceEpisodeRealtimeVoiceSession, SourceEpisodeDriveFileRevision, SourceEpisodeWorkArtifactRevision:
		return true
	default:
		return false
	}
}

func validSourceEpisodeNativeFamily(kind SourceEpisodeKind, family string) bool {
	switch kind {
	case SourceEpisodeMeetingAnalysis:
		return family == SourceEpisodeFamilyMeetingAnalysis
	case SourceEpisodePublicChannelSegment, SourceEpisodePrivateConversationSegment:
		return family == SourceEpisodeFamilyConversationEvent
	case SourceEpisodeRealtimeVoiceSession:
		return family == SourceEpisodeFamilyRealtimeVoiceSession
	case SourceEpisodeDriveFileRevision:
		return family == SourceEpisodeFamilyDriveFileRevision
	case SourceEpisodeWorkArtifactRevision:
		return family == SourceEpisodeFamilyWorkArtifactRevision
	default:
		return false
	}
}

func validOptionalSourceEpisodeIDs(values []string) bool {
	if len(values) == 0 {
		return true
	}
	if !uniqueSTRIDEIDs(values) {
		return false
	}
	return sort.StringsAreSorted(values)
}

func SourceEpisodeIdempotencyKey(tenantID string, kind SourceEpisodeKind, source SourceEpisodeRevisionRef) string {
	digest, _ := STRIDEContractDigest(struct {
		TenantID string
		Kind     SourceEpisodeKind
		Source   SourceEpisodeRevisionRef
	}{TenantID: tenantID, Kind: kind, Source: source})
	return digest
}

func (episode SourceEpisode) ContentDigest() (string, error) {
	header := episode.Header
	header.ContentDigest = ""
	return STRIDEContractDigest(struct {
		Header  STRIDEContractHeader
		Episode sourceEpisodeDigestBody
	}{Header: header, Episode: episode.digestBody()})
}

type sourceEpisodeDigestBody struct {
	Kind                       SourceEpisodeKind
	Source                     SourceEpisodeRevisionRef
	RetrievalBody              SourceEpisodeRevisionRef
	Scope                      SourceEpisodeScope
	Authority                  SourceEpisodeAuthoritySnapshot
	OccurredStart              time.Time
	OccurredEnd                time.Time
	PhaseProof                 SourceEpisodePhaseProof
	RawMeetingTranscriptAccess string
	IdempotencyKeyDigest       string
	Supersedes                 *STRIDEReference
}

func (episode SourceEpisode) digestBody() sourceEpisodeDigestBody {
	return sourceEpisodeDigestBody{
		Kind: episode.Kind, Source: episode.Source, RetrievalBody: episode.RetrievalBody, Scope: episode.Scope, Authority: episode.Authority,
		OccurredStart: episode.OccurredStart, OccurredEnd: episode.OccurredEnd, PhaseProof: episode.PhaseProof,
		RawMeetingTranscriptAccess: episode.RawMeetingTranscriptAccess, IdempotencyKeyDigest: episode.IdempotencyKeyDigest, Supersedes: episode.Supersedes,
	}
}

type SourceEpisodeAdapterInput struct {
	Header        STRIDEContractHeader
	Source        SourceEpisodeRevisionRef
	RetrievalBody SourceEpisodeRevisionRef
	Scope         SourceEpisodeScope
	Authority     SourceEpisodeAuthoritySnapshot
	OccurredStart time.Time
	OccurredEnd   time.Time
	PhaseProof    SourceEpisodePhaseProof
	Supersedes    *STRIDEReference
}

func AdaptMeetingAnalysisSourceEpisode(input SourceEpisodeAdapterInput) (SourceEpisode, error) {
	return adaptSourceEpisode(input, SourceEpisodeMeetingAnalysis, MeetingSourceEpisodeRawExactSegments)
}

func AdaptPublicChannelSourceEpisode(input SourceEpisodeAdapterInput) (SourceEpisode, error) {
	return adaptSourceEpisode(input, SourceEpisodePublicChannelSegment, "")
}

func AdaptPrivateConversationSourceEpisode(input SourceEpisodeAdapterInput) (SourceEpisode, error) {
	return adaptSourceEpisode(input, SourceEpisodePrivateConversationSegment, "")
}

func AdaptRealtimeVoiceSourceEpisode(input SourceEpisodeAdapterInput) (SourceEpisode, error) {
	return adaptSourceEpisode(input, SourceEpisodeRealtimeVoiceSession, "")
}

func AdaptDriveFileSourceEpisode(input SourceEpisodeAdapterInput) (SourceEpisode, error) {
	return adaptSourceEpisode(input, SourceEpisodeDriveFileRevision, "")
}

func AdaptWorkArtifactSourceEpisode(input SourceEpisodeAdapterInput) (SourceEpisode, error) {
	return adaptSourceEpisode(input, SourceEpisodeWorkArtifactRevision, "")
}

func adaptSourceEpisode(input SourceEpisodeAdapterInput, kind SourceEpisodeKind, rawMeetingAccess string) (SourceEpisode, error) {
	header := input.Header
	header.ContractType = STRIDEContractSourceEpisode
	header.ContentDigest = ""
	episode := SourceEpisode{
		Header: header, Kind: kind, Source: input.Source, RetrievalBody: input.RetrievalBody, Scope: input.Scope, Authority: input.Authority,
		OccurredStart: input.OccurredStart, OccurredEnd: input.OccurredEnd, PhaseProof: input.PhaseProof,
		RawMeetingTranscriptAccess: rawMeetingAccess, Supersedes: input.Supersedes,
	}
	episode.IdempotencyKeyDigest = SourceEpisodeIdempotencyKey(header.TenantID, kind, episode.Source)
	digest, err := episode.ContentDigest()
	if err != nil {
		return SourceEpisode{}, err
	}
	episode.Header.ContentDigest = digest
	if err := episode.Validate(); err != nil {
		return SourceEpisode{}, err
	}
	return episode, nil
}

type SourceEpisodeDualWriteResult struct {
	Reference STRIDEReference `json:"reference"`
	Replayed  bool            `json:"replayed"`
}

type SourceEpisodeAuthorityExpectation struct {
	Kind          SourceEpisodeKind
	Source        SourceEpisodeRevisionRef
	RetrievalBody SourceEpisodeRevisionRef
	Scope         SourceEpisodeScope
	Authority     SourceEpisodeAuthoritySnapshot
	OccurredEnd   time.Time
	PhaseProof    SourceEpisodePhaseProof
}

// SourceEpisodeCurrentAuthority rechecks the native source revision, grants,
// consent, purge generation, retention policy, and close/commit receipt while
// the canonical dual-write callback runs.
type SourceEpisodeCurrentAuthority interface {
	WithCurrentSourceEpisodeAuthority(context.Context, SourceEpisodeAuthorityExpectation, func() error) error
}

// SourceEpisodeDualWriter is the persistence seam for the transition period:
// implementations atomically append the envelope to the canonical ledger and
// mark the native source revision as dual-written under IdempotencyKeyDigest.
// expectedCurrent is nil for revision one and the exact superseded envelope
// for later revisions.
type SourceEpisodeDualWriter interface {
	DualWriteSourceEpisode(context.Context, SourceEpisode, *STRIDEReference) (SourceEpisodeDualWriteResult, error)
}

func DualWriteCanonicalSourceEpisode(ctx context.Context, authority SourceEpisodeCurrentAuthority, writer SourceEpisodeDualWriter, episode SourceEpisode) (SourceEpisodeDualWriteResult, error) {
	if authority == nil || writer == nil {
		return SourceEpisodeDualWriteResult{}, ErrSourceEpisodeUnavailable
	}
	if err := episode.Validate(); err != nil {
		return SourceEpisodeDualWriteResult{}, err
	}
	expectation := SourceEpisodeAuthorityExpectation{
		Kind: episode.Kind, Source: episode.Source, RetrievalBody: episode.RetrievalBody, Scope: episode.Scope, Authority: episode.Authority,
		OccurredEnd: episode.OccurredEnd, PhaseProof: episode.PhaseProof,
	}
	var result SourceEpisodeDualWriteResult
	err := authority.WithCurrentSourceEpisodeAuthority(ctx, expectation, func() error {
		var writeErr error
		result, writeErr = writer.DualWriteSourceEpisode(ctx, episode, episode.Supersedes)
		return writeErr
	})
	if err != nil {
		return SourceEpisodeDualWriteResult{}, err
	}
	want := referenceFromHeader(episode.Header)
	if result.Reference != want {
		return SourceEpisodeDualWriteResult{}, ErrSourceEpisodeConflict
	}
	return result, nil
}

// SourceEpisodeReplayDecision is a pure guard that durable adapters can use
// after looking up an idempotency key. Same key+content is replay; any other
// reuse is a conflict.
func SourceEpisodeReplayDecision(existing, candidate SourceEpisode) (bool, error) {
	if existing.Validate() != nil || candidate.Validate() != nil || existing.IdempotencyKeyDigest != candidate.IdempotencyKeyDigest {
		return false, ErrSourceEpisodeConflict
	}
	if existing.Header.ContentDigest != candidate.Header.ContentDigest || existing.Header.ID != candidate.Header.ID || existing.Header.Revision != candidate.Header.Revision {
		return false, ErrSourceEpisodeConflict
	}
	return true, nil
}
