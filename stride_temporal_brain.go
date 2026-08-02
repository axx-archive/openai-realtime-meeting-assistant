package main

// STRIDE E3 temporal meeting intelligence is deliberately data-only. It does
// not register a worker, call a model, fetch a body, or grant authority. The
// caller supplies already-finalized transcript revisions and body-free
// analysis references; every read is filtered for one principal before any
// transcript text or derived reference is copied into the result.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

var (
	ErrTemporalBrainInvalid        = errors.New("temporal meeting brain input is invalid")
	ErrTemporalBrainSequence       = errors.New("temporal meeting brain sequence is not strictly increasing")
	ErrTemporalBrainSupersession   = errors.New("temporal meeting brain supersession does not name the current revision")
	ErrTemporalBrainStaleEvidence  = errors.New("temporal meeting analysis cites a non-current transcript revision")
	ErrTemporalBrainSnapshot       = errors.New("temporal meeting brain snapshot is invalid")
	ErrTemporalClockAmbiguous      = errors.New("local clock time is ambiguous; an explicit fold is required")
	ErrTemporalClockNonexistent    = errors.New("local clock time does not exist")
	ErrTemporalContextUnauthorized = errors.New("meeting specialist context is not authorized")
)

const temporalMeetingBrainSnapshotFormat = 1

type TemporalMeetingBrainConfig struct {
	TenantID     string    `json:"tenantId"`
	RoomID       string    `json:"roomId"`
	SittingID    string    `json:"sittingId"`
	SittingStart time.Time `json:"sittingStart"`
	SittingEnd   time.Time `json:"sittingEnd,omitempty"`
}

func (config TemporalMeetingBrainConfig) validate() error {
	if !strideIdentifier(config.TenantID) || !strideIdentifier(config.RoomID) || !strideIdentifier(config.SittingID) ||
		config.SittingStart.IsZero() || config.SittingStart.Location() != time.UTC ||
		(!config.SittingEnd.IsZero() && (config.SittingEnd.Location() != time.UTC || !config.SittingStart.Before(config.SittingEnd))) {
		return ErrTemporalBrainInvalid
	}
	return nil
}

type TemporalTranscriptRevisionEvent struct {
	Conversation ConversationEvent  `json:"conversation"`
	Segment      TranscriptSegment  `json:"segment"`
	Revision     TranscriptRevision `json:"revision"`
	Text         string             `json:"text,omitempty"`
	TopicIDs     []string           `json:"topicIds,omitempty"`
}

type TemporalAnalysisEvent struct {
	Projection AnalysisProjection `json:"projection"`
	Statement  string             `json:"statement"`
	TopicIDs   []string           `json:"topicIds,omitempty"`
}

type TemporalPurgeEvent struct {
	TenantID        string `json:"tenantId"`
	SegmentID       string `json:"segmentId"`
	RevisionID      string `json:"revisionId"`
	PurgeGeneration int64  `json:"purgeGeneration"`
}

type TemporalMeetingEventKind string

const (
	TemporalMeetingEventTranscript TemporalMeetingEventKind = "transcript_revision"
	TemporalMeetingEventAnalysis   TemporalMeetingEventKind = "analysis_projection"
	TemporalMeetingEventPurge      TemporalMeetingEventKind = "purge"
)

type TemporalMeetingEvent struct {
	Sequence   uint64                           `json:"sequence"`
	Kind       TemporalMeetingEventKind         `json:"kind"`
	Transcript *TemporalTranscriptRevisionEvent `json:"transcript,omitempty"`
	Analysis   *TemporalAnalysisEvent           `json:"analysis,omitempty"`
	Purge      *TemporalPurgeEvent              `json:"purge,omitempty"`
}

type TemporalTranscriptSource struct {
	SegmentID       string          `json:"segmentId"`
	Revision        STRIDEReference `json:"revision"`
	SegmentRef      STRIDEReference `json:"segmentRef"`
	SourceStart     time.Time       `json:"sourceStart"`
	SourceEnd       time.Time       `json:"sourceEnd"`
	CaptureSequence uint64          `json:"captureSequence"`
	HighWater       uint64          `json:"highWater"`
	CapturedAt      time.Time       `json:"capturedAt"`
	Speaker         string          `json:"speaker"`
	Text            string          `json:"text,omitempty"`
	Audience        STRIDEAudience  `json:"audience"`
	ConsentScopes   []string        `json:"consentScopes"`
	ACLVersion      int64           `json:"aclVersion"`
	PurgeGeneration int64           `json:"purgeGeneration"`
	TopicIDs        []string        `json:"topicIds,omitempty"`
}

type TemporalMeetingFact struct {
	ID              string            `json:"id"`
	Kind            string            `json:"kind"`
	Statement       string            `json:"statement"`
	Evidence        []STRIDEReference `json:"evidence"`
	WindowStart     time.Time         `json:"windowStart"`
	WindowEnd       time.Time         `json:"windowEnd"`
	SourceHighWater uint64            `json:"sourceHighWater"`
	FreshThrough    time.Time         `json:"freshThrough"`
	Confidence      float64           `json:"confidence"`
	Audience        STRIDEAudience    `json:"audience"`
	TopicIDs        []string          `json:"topicIds,omitempty"`
}

type TemporalCurrentMeetingState struct {
	Config              TemporalMeetingBrainConfig `json:"config"`
	TranscriptHighWater uint64                     `json:"transcriptHighWater"`
	AnalysisHighWater   uint64                     `json:"analysisHighWater"`
	PurgeGeneration     int64                      `json:"purgeGeneration"`
	Decisions           []TemporalMeetingFact      `json:"decisions"`
	Commitments         []TemporalMeetingFact      `json:"commitments"`
	Blockers            []TemporalMeetingFact      `json:"blockers"`
	Storylines          []TemporalMeetingFact      `json:"storylines"`
	Alignment           []TemporalMeetingFact      `json:"alignment"`
	Positions           []TemporalMeetingFact      `json:"positions"`
	Questions           []TemporalMeetingFact      `json:"questions"`
	Entities            []TemporalMeetingFact      `json:"entities"`
	Artifacts           []TemporalMeetingFact      `json:"artifacts"`
	WorkCandidates      []TemporalMeetingFact      `json:"workCandidates"`
}

type temporalMeetingBrainSnapshot struct {
	Format              int                        `json:"format"`
	SnapshotGeneration  uint64                     `json:"snapshotGeneration,omitempty"`
	KeyID               string                     `json:"keyId,omitempty"`
	Config              TemporalMeetingBrainConfig `json:"config"`
	LastSequence        uint64                     `json:"lastSequence"`
	TranscriptHighWater uint64                     `json:"transcriptHighWater"`
	AnalysisHighWater   uint64                     `json:"analysisHighWater"`
	PurgeGeneration     int64                      `json:"purgeGeneration"`
	Sources             []TemporalTranscriptSource `json:"sources"`
	Facts               []TemporalMeetingFact      `json:"facts"`
	PurgedRevisions     []string                   `json:"purgedRevisions"`
	StateDigest         string                     `json:"stateDigest"`
	Signature           string                     `json:"signature,omitempty"`
}

type TemporalMeetingBrain struct {
	config              TemporalMeetingBrainConfig
	lastSequence        uint64
	transcriptHighWater uint64
	analysisHighWater   uint64
	purgeGeneration     int64
	sources             map[string]TemporalTranscriptSource
	facts               map[string]TemporalMeetingFact
	purgedRevisions     map[string]bool
	snapshotGeneration  uint64
}

func NewTemporalMeetingBrain(config TemporalMeetingBrainConfig) (*TemporalMeetingBrain, error) {
	if err := config.validate(); err != nil {
		return nil, err
	}
	return &TemporalMeetingBrain{config: config, sources: map[string]TemporalTranscriptSource{}, facts: map[string]TemporalMeetingFact{}, purgedRevisions: map[string]bool{}}, nil
}

func RebuildTemporalMeetingBrain(config TemporalMeetingBrainConfig, events []TemporalMeetingEvent) (*TemporalMeetingBrain, error) {
	brain, err := NewTemporalMeetingBrain(config)
	if err != nil {
		return nil, err
	}
	ordered := append([]TemporalMeetingEvent(nil), events...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Sequence < ordered[j].Sequence })
	for _, event := range ordered {
		if err := brain.Apply(event); err != nil {
			return nil, err
		}
	}
	return brain, nil
}

func (brain *TemporalMeetingBrain) Apply(event TemporalMeetingEvent) error {
	if brain == nil || event.Sequence == 0 || event.Sequence <= brain.lastSequence {
		return ErrTemporalBrainSequence
	}
	var err error
	switch event.Kind {
	case TemporalMeetingEventTranscript:
		if event.Transcript == nil || event.Analysis != nil || event.Purge != nil {
			return ErrTemporalBrainInvalid
		}
		err = brain.applyTranscript(event.Sequence, *event.Transcript)
	case TemporalMeetingEventAnalysis:
		if event.Analysis == nil || event.Transcript != nil || event.Purge != nil {
			return ErrTemporalBrainInvalid
		}
		err = brain.applyAnalysis(*event.Analysis)
	case TemporalMeetingEventPurge:
		if event.Purge == nil || event.Transcript != nil || event.Analysis != nil {
			return ErrTemporalBrainInvalid
		}
		err = brain.applyPurge(event.Sequence, *event.Purge)
	default:
		err = ErrTemporalBrainInvalid
	}
	if err != nil {
		return err
	}
	brain.lastSequence = event.Sequence
	return nil
}

func (brain *TemporalMeetingBrain) applyTranscript(sequence uint64, event TemporalTranscriptRevisionEvent) error {
	if event.Conversation.Validate() != nil || event.Segment.Validate() != nil || event.Revision.Validate() != nil ||
		event.Conversation.Header.TenantID != brain.config.TenantID || event.Segment.Header.TenantID != brain.config.TenantID || event.Revision.Header.TenantID != brain.config.TenantID ||
		event.Conversation.RoomID != brain.config.RoomID || event.Conversation.SittingID != brain.config.SittingID || event.Segment.RoomID != brain.config.RoomID || event.Segment.SittingID != brain.config.SittingID ||
		event.Segment.ConversationRef.ID != event.Conversation.Header.ID || event.Segment.ConversationRef.Revision != event.Conversation.Header.Revision ||
		event.Revision.SegmentID != event.Segment.Header.ID || event.Revision.TextDigest != temporalDigest(event.Text) || !validOptionalTemporalIDs(event.TopicIDs) {
		return ErrTemporalBrainInvalid
	}
	status := event.Revision.Status
	if status == "degraded_final" {
		return ErrTemporalBrainInvalid
	}
	current, exists := brain.sources[event.Revision.SegmentID]
	if !exists && (status == "corrected" || status == "superseded" || status == "retracted") {
		return ErrTemporalBrainSupersession
	}
	if exists && event.Revision.Revision <= current.Revision.Revision {
		return ErrTemporalBrainSupersession
	}
	if exists && event.Revision.SupersedesID != current.Revision.ID {
		return ErrTemporalBrainSupersession
	}
	if !exists && event.Revision.Revision > 1 && event.Revision.SupersedesID == "" {
		return ErrTemporalBrainSupersession
	}
	if exists {
		delete(brain.sources, event.Revision.SegmentID)
		brain.invalidateFactsForReference(current.Revision)
	}
	if status == "superseded" || status == "retracted" {
		brain.transcriptHighWater = sequence
		return nil
	}
	if status != "authoritative_final" && status != "corrected" {
		return ErrTemporalBrainInvalid
	}
	ref := referenceFromHeader(event.Revision.Header)
	if brain.purgedRevisions[ref.ID] {
		return ErrTemporalBrainInvalid
	}
	brain.sources[event.Revision.SegmentID] = TemporalTranscriptSource{
		SegmentID: event.Revision.SegmentID, Revision: ref, SegmentRef: referenceFromHeader(event.Segment.Header),
		SourceStart: event.Segment.SourceStart.UTC(), SourceEnd: event.Segment.SourceEnd.UTC(), CaptureSequence: event.Segment.CaptureSequence,
		HighWater:  sequence,
		CapturedAt: event.Segment.CreatedAt.UTC(), Speaker: event.Segment.Speaker, Text: event.Text, Audience: cloneAudience(event.Conversation.Audience),
		ConsentScopes: sortedStrings(event.Segment.ConsentScopes), ACLVersion: event.Conversation.ACLVersion,
		PurgeGeneration: event.Conversation.PurgeGeneration, TopicIDs: sortedStrings(event.TopicIDs),
	}
	brain.transcriptHighWater = sequence
	return nil
}

func (brain *TemporalMeetingBrain) applyAnalysis(event TemporalAnalysisEvent) error {
	projection := event.Projection
	if projection.Validate() != nil || projection.Header.TenantID != brain.config.TenantID || strings.TrimSpace(event.Statement) == "" || !validOptionalTemporalIDs(event.TopicIDs) ||
		projection.SourceHighWater > brain.transcriptHighWater || projection.ProjectionHighWater == 0 {
		return ErrTemporalBrainInvalid
	}
	for _, ref := range projection.SourceRefs {
		if ref.ContractType != STRIDEContractTranscriptRevision || !brain.hasCurrentReference(ref) {
			return ErrTemporalBrainStaleEvidence
		}
	}
	if projection.SupersedesID != "" {
		if _, ok := brain.facts[projection.SupersedesID]; !ok {
			return ErrTemporalBrainSupersession
		}
		delete(brain.facts, projection.SupersedesID)
	}
	fact := TemporalMeetingFact{
		ID: projection.Header.ID, Kind: projection.Kind, Statement: strings.TrimSpace(event.Statement), Evidence: append([]STRIDEReference(nil), projection.SourceRefs...),
		WindowStart: projection.WindowStart.UTC(), WindowEnd: projection.WindowEnd.UTC(), SourceHighWater: projection.SourceHighWater,
		FreshThrough: projection.FreshThrough.UTC(), Confidence: projection.Confidence, Audience: cloneAudience(projection.Audience), TopicIDs: sortedStrings(event.TopicIDs),
	}
	brain.facts[fact.ID] = fact
	if projection.ProjectionHighWater > brain.analysisHighWater {
		brain.analysisHighWater = projection.ProjectionHighWater
	}
	return nil
}

func (brain *TemporalMeetingBrain) applyPurge(sequence uint64, event TemporalPurgeEvent) error {
	if event.TenantID != brain.config.TenantID || !strideIdentifier(event.SegmentID) || !strideIdentifier(event.RevisionID) || event.PurgeGeneration <= brain.purgeGeneration {
		return ErrTemporalBrainInvalid
	}
	brain.purgeGeneration = event.PurgeGeneration
	brain.purgedRevisions[event.RevisionID] = true
	if source, ok := brain.sources[event.SegmentID]; ok && source.Revision.ID == event.RevisionID {
		delete(brain.sources, event.SegmentID)
		brain.invalidateFactsForReference(source.Revision)
	}
	brain.transcriptHighWater = sequence
	return nil
}

func (brain *TemporalMeetingBrain) hasCurrentReference(ref STRIDEReference) bool {
	for _, source := range brain.sources {
		if sameSTRIDEReference(source.Revision, ref) {
			return true
		}
	}
	return false
}

func (brain *TemporalMeetingBrain) invalidateFactsForReference(ref STRIDEReference) {
	for id, fact := range brain.facts {
		for _, evidence := range fact.Evidence {
			if sameSTRIDEReference(evidence, ref) {
				delete(brain.facts, id)
				break
			}
		}
	}
}

func (brain *TemporalMeetingBrain) CurrentState() TemporalCurrentMeetingState {
	state := TemporalCurrentMeetingState{Config: brain.config, TranscriptHighWater: brain.transcriptHighWater, AnalysisHighWater: brain.analysisHighWater, PurgeGeneration: brain.purgeGeneration}
	for _, fact := range brain.sortedFacts() {
		switch fact.Kind {
		case "decision":
			state.Decisions = append(state.Decisions, fact)
		case "commitment":
			state.Commitments = append(state.Commitments, fact)
		case "blocker":
			state.Blockers = append(state.Blockers, fact)
		case "storyline":
			state.Storylines = append(state.Storylines, fact)
		case "alignment", "divergence":
			state.Alignment = append(state.Alignment, fact)
		case "position":
			state.Positions = append(state.Positions, fact)
		case "open_question":
			state.Questions = append(state.Questions, fact)
		case "entity", "project", "topic", "vocabulary", "alias":
			state.Entities = append(state.Entities, fact)
		case "link", "file", "artifact":
			state.Artifacts = append(state.Artifacts, fact)
		case "work_intent_candidate":
			state.WorkCandidates = append(state.WorkCandidates, fact)
		}
	}
	return state
}

type TemporalMeetingQueryKind string

const (
	TemporalQueryLastFiveMinutes   TemporalMeetingQueryKind = "last_5_minutes"
	TemporalQueryLastThirtyMinutes TemporalMeetingQueryKind = "last_30_minutes"
	TemporalQueryExplicitClock     TemporalMeetingQueryKind = "explicit_clock"
	TemporalQueryTopic             TemporalMeetingQueryKind = "topic"
	TemporalQueryBeforeAdmission   TemporalMeetingQueryKind = "before_admission"
	TemporalQueryLateJoin          TemporalMeetingQueryKind = "late_join"
)

type TemporalMeetingQuery struct {
	Kind        TemporalMeetingQueryKind `json:"kind"`
	AsOf        time.Time                `json:"asOf,omitempty"`
	Timezone    string                   `json:"timezone"`
	StartLocal  string                   `json:"startLocal,omitempty"`
	EndLocal    string                   `json:"endLocal,omitempty"`
	StartFold   int                      `json:"startFold,omitempty"`
	EndFold     int                      `json:"endFold,omitempty"`
	TopicID     string                   `json:"topicId,omitempty"`
	Anchor      AdmissionAnchor          `json:"anchor,omitempty"`
	SettleDelay time.Duration            `json:"settleDelay,omitempty"`
	RequestedAt time.Time                `json:"requestedAt"`
}

func (brain *TemporalMeetingBrain) ResolveQuery(query TemporalMeetingQuery) ([]TemporalQuery, error) {
	if brain == nil || strings.TrimSpace(query.Timezone) == "" || query.RequestedAt.IsZero() || query.RequestedAt.Location() != time.UTC {
		return nil, ErrTemporalBrainInvalid
	}
	switch query.Kind {
	case TemporalQueryLastFiveMinutes, TemporalQueryLastThirtyMinutes:
		if query.AsOf.IsZero() || query.AsOf.Location() != time.UTC {
			return nil, ErrTemporalBrainInvalid
		}
		minutes := 5
		if query.Kind == TemporalQueryLastThirtyMinutes {
			minutes = 30
		}
		end := query.AsOf
		if !brain.config.SittingEnd.IsZero() && end.After(brain.config.SittingEnd) {
			end = brain.config.SittingEnd
		}
		return oneTemporalQuery(NewLastMinutesTemporalQuery(brain.config.SittingStart, end, minutes, query.Timezone, brain.config.RoomID, brain.config.SittingID, string(query.Kind)))
	case TemporalQueryExplicitClock:
		start, err := resolveLocalClock(query.StartLocal, query.Timezone, query.StartFold)
		if err != nil {
			return nil, err
		}
		end, err := resolveLocalClock(query.EndLocal, query.Timezone, query.EndFold)
		if err != nil {
			return nil, err
		}
		start, end, err = brain.clipInterval(start, end)
		if err != nil {
			return nil, err
		}
		return oneTemporalQuery(NewBoundedTemporalQuery(TemporalExplicitRange, start, end, query.Timezone, brain.config.RoomID, brain.config.SittingID, "explicit local clock range"))
	case TemporalQueryTopic:
		if !strideIdentifier(query.TopicID) {
			return nil, ErrTemporalBrainInvalid
		}
		intervals := make([]TemporalQuery, 0)
		for _, source := range brain.sortedSources() {
			if !temporalContainsString(source.TopicIDs, query.TopicID) {
				continue
			}
			resolved, err := NewBoundedTemporalQuery(TemporalExplicitRange, source.SourceStart, source.SourceEnd, query.Timezone, brain.config.RoomID, brain.config.SittingID, "topic:"+query.TopicID)
			if err != nil {
				return nil, err
			}
			intervals = append(intervals, resolved)
		}
		return coalesceTemporalQueries(intervals), nil
	case TemporalQueryBeforeAdmission, TemporalQueryLateJoin:
		if query.SettleDelay < 0 || query.Anchor.TenantID != brain.config.TenantID || query.Anchor.RoomID != brain.config.RoomID || query.Anchor.SittingID != brain.config.SittingID {
			return nil, ErrTemporalBrainInvalid
		}
		note := string(query.Kind)
		return oneTemporalQuery(NewBeforeAdmissionTemporalQuery(query.Anchor, brain.config.SittingStart, query.Timezone, query.SettleDelay, note))
	default:
		return nil, ErrTemporalBrainInvalid
	}
}

type TemporalAuthorizedTranscript struct {
	Evidence     STRIDEReference `json:"evidence"`
	SegmentID    string          `json:"segmentId"`
	Speaker      string          `json:"speaker"`
	SourceStart  time.Time       `json:"sourceStart"`
	SourceEnd    time.Time       `json:"sourceEnd"`
	CoveredStart time.Time       `json:"coveredStart"`
	CoveredEnd   time.Time       `json:"coveredEnd"`
	Text         string          `json:"text,omitempty"`
	BodyOmitted  bool            `json:"bodyOmitted"`
	LateArrival  bool            `json:"lateArrival"`
}

type TemporalAnswerCoverage struct {
	Intervals         []TemporalQuery `json:"intervals"`
	AuthorizedSources int             `json:"authorizedSources"`
	LateArrivals      int             `json:"lateArrivals"`
	Settled           bool            `json:"settled"`
	Gaps              []string        `json:"gaps"`
}

type TemporalMeetingAnswer struct {
	Mode                string                         `json:"mode"`
	Sources             []TemporalAuthorizedTranscript `json:"sources"`
	Facts               []TemporalMeetingFact          `json:"facts"`
	TranscriptHighWater uint64                         `json:"transcriptHighWater"`
	AnalysisHighWater   uint64                         `json:"analysisHighWater"`
	AnalysisFresh       bool                           `json:"analysisFresh"`
	Coverage            TemporalAnswerCoverage         `json:"coverage"`
	EvidenceDigest      string                         `json:"evidenceDigest"`
}

func (brain *TemporalMeetingBrain) Answer(principal ACLPrincipal, consentScopes []string, intervals []TemporalQuery, requestedAt time.Time) (TemporalMeetingAnswer, error) {
	answer := TemporalMeetingAnswer{Mode: "transcript_first",
		Coverage: TemporalAnswerCoverage{Intervals: append([]TemporalQuery(nil), intervals...), Settled: true}}
	if brain == nil || principal.TenantID != brain.config.TenantID || strings.TrimSpace(principal.ID) == "" || len(intervals) == 0 || requestedAt.IsZero() {
		return answer, ErrTemporalBrainInvalid
	}
	for _, interval := range intervals {
		if interval.Validate() != nil || interval.RoomID != brain.config.RoomID || interval.SittingID != brain.config.SittingID {
			return answer, ErrTemporalBrainInvalid
		}
		if !interval.Settled(requestedAt) {
			answer.Coverage.Settled = false
		}
	}
	authorizedRefs := map[string]bool{}
	boundaryOmitted := false
	for _, source := range brain.sortedSources() {
		if !audienceAllows(source.Audience, principal) || !containsAll(consentScopes, source.ConsentScopes) {
			continue
		}
		for _, interval := range intervals {
			decision := interval.DecideSegment(CapturedTemporalSegment{OccurredStart: source.SourceStart, OccurredEnd: source.SourceEnd, CaptureSequence: source.CaptureSequence, CapturedAt: source.CapturedAt})
			if !decision.Include {
				continue
			}
			item := TemporalAuthorizedTranscript{Evidence: source.Revision, SegmentID: source.SegmentID, Speaker: source.Speaker, SourceStart: source.SourceStart,
				SourceEnd: source.SourceEnd, CoveredStart: decision.ClippedStart, CoveredEnd: decision.ClippedEnd, LateArrival: decision.LateArrival}
			if decision.Clipped {
				item.BodyOmitted = true
				boundaryOmitted = true
			} else {
				item.Text = source.Text
			}
			answer.Sources = append(answer.Sources, item)
			authorizedRefs[referenceKey(source.Revision)] = true
			if source.HighWater > answer.TranscriptHighWater {
				answer.TranscriptHighWater = source.HighWater
			}
			if decision.LateArrival {
				answer.Coverage.LateArrivals++
			}
			break
		}
	}
	answer.Coverage.AuthorizedSources = len(answer.Sources)
	latestEnd := intervals[0].EndUTC
	for _, interval := range intervals[1:] {
		if interval.EndUTC.After(latestEnd) {
			latestEnd = interval.EndUTC
		}
	}
	eligibleFacts := make([]TemporalMeetingFact, 0)
	for _, fact := range brain.sortedFacts() {
		if fact.FreshThrough.Before(latestEnd) || !audienceAllows(fact.Audience, principal) || !factCoveredByIntervals(fact, intervals) {
			continue
		}
		allowed := true
		for _, ref := range fact.Evidence {
			if !authorizedRefs[referenceKey(ref)] {
				allowed = false
				break
			}
		}
		if allowed {
			eligibleFacts = append(eligibleFacts, fact)
			candidate := fact.SourceHighWater
			if candidate > answer.TranscriptHighWater {
				candidate = answer.TranscriptHighWater
			}
			if candidate > answer.AnalysisHighWater {
				answer.AnalysisHighWater = candidate
			}
		}
	}
	answer.AnalysisFresh = answer.TranscriptHighWater > 0 && answer.AnalysisHighWater >= answer.TranscriptHighWater
	if answer.AnalysisFresh {
		for _, fact := range eligibleFacts {
			if fact.SourceHighWater < answer.TranscriptHighWater {
				continue
			}
			answer.Facts = append(answer.Facts, fact)
		}
		answer.Mode = "analysis_with_transcript_evidence"
	} else {
		answer.Coverage.Gaps = append(answer.Coverage.Gaps, "analysis_stale")
	}
	if len(answer.Sources) == 0 {
		answer.Coverage.Gaps = append(answer.Coverage.Gaps, "no_authorized_transcript")
	}
	if boundaryOmitted {
		answer.Coverage.Gaps = append(answer.Coverage.Gaps, "boundary_segment_body_omitted")
	}
	if !answer.Coverage.Settled {
		answer.Coverage.Gaps = append(answer.Coverage.Gaps, "before_admission_unsettled")
	}
	if answer.Coverage.LateArrivals > 0 {
		answer.Coverage.Gaps = append(answer.Coverage.Gaps, "late_arrivals_detected")
	}
	answer.Coverage.Gaps = sortedUniqueStrings(answer.Coverage.Gaps)
	digestMaterial := struct {
		Sources []TemporalAuthorizedTranscript `json:"sources"`
		Facts   []TemporalMeetingFact          `json:"facts"`
		Gaps    []string                       `json:"gaps"`
	}{answer.Sources, answer.Facts, answer.Coverage.Gaps}
	raw, err := canonicalJSON(digestMaterial)
	if err != nil {
		return answer, err
	}
	answer.EvidenceDigest = temporalDigestBytes(raw)
	return answer, nil
}

type TemporalContextReference struct {
	Reference     STRIDEReference `json:"reference"`
	Audience      STRIDEAudience  `json:"audience"`
	ConsentScopes []string        `json:"consentScopes"`
	WindowStart   time.Time       `json:"windowStart"`
	WindowEnd     time.Time       `json:"windowEnd"`
	HighWater     uint64          `json:"highWater"`
	FreshThrough  time.Time       `json:"freshThrough"`
}

type MeetingSpecialistContextRequest struct {
	Header                 STRIDEContractHeader       `json:"header"`
	Invitation             MeetingAgentInvitation     `json:"invitation"`
	AgentProfile           STRIDEReference            `json:"agentProfile"`
	RuntimeRevision        STRIDEReference            `json:"runtimeRevision"`
	ModelRevision          STRIDEReference            `json:"modelRevision"`
	Principal              ACLPrincipal               `json:"-"`
	ConsentScopes          []string                   `json:"consentScopes"`
	ApprovedIntervals      []TemporalQuery            `json:"approvedIntervals"`
	ApprovedIntervalDigest string                     `json:"approvedIntervalDigest"`
	Answer                 TemporalMeetingAnswer      `json:"answer"`
	Analysis               []TemporalContextReference `json:"analysis"`
	Brain                  []TemporalContextReference `json:"brain"`
	Work                   []TemporalContextReference `json:"work"`
	RetentionDigest        string                     `json:"retentionDigest"`
	PurgeGeneration        int64                      `json:"purgeGeneration"`
	ToolIDs                []string                   `json:"toolIds"`
	ResponseContract       string                     `json:"responseContract"`
	FloorPolicy            string                     `json:"floorPolicy"`
	TimeBudgetSeconds      int64                      `json:"timeBudgetSeconds"`
	TurnBudget             int                        `json:"turnBudget"`
	AudioBudgetSeconds     int64                      `json:"audioBudgetSeconds"`
	TokenBudget            int64                      `json:"tokenBudget"`
	CostBudgetCents        int64                      `json:"costBudgetCents"`
	MaxAnalysisRefs        int                        `json:"maxAnalysisRefs"`
	MaxBrainRefs           int                        `json:"maxBrainRefs"`
	MaxWorkRefs            int                        `json:"maxWorkRefs"`
}

type AssembledMeetingSpecialistContext struct {
	Envelope  MeetingSpecialistContextEnvelope `json:"envelope"`
	Intervals []TemporalQuery                  `json:"intervals"`
	Gaps      []string                         `json:"gaps"`
}

func AssembleMeetingSpecialistContext(request MeetingSpecialistContextRequest) (AssembledMeetingSpecialistContext, error) {
	var result AssembledMeetingSpecialistContext
	intervalDigest, err := temporalIntervalDigest(request.ApprovedIntervals)
	if err != nil || intervalDigest != request.ApprovedIntervalDigest || request.Invitation.SourceIntervalDigest != intervalDigest || request.Invitation.Decision != "approved" ||
		request.Invitation.DecisionPrincipal == "" || request.Invitation.DecisionAt == nil || request.Principal.TenantID != request.Header.TenantID || request.Invitation.Header.TenantID != request.Header.TenantID ||
		strings.TrimSpace(request.Principal.ID) == "" || !audienceAllows(request.Invitation.Audience, request.Principal) || request.Header.Validate(STRIDEContractMeetingSpecialistContext) != nil ||
		request.Invitation.Validate() != nil || request.Invitation.Eligibility == nil || request.Invitation.Eligibility.Validate() != nil || request.AgentProfile.Validate() != nil || request.RuntimeRevision.Validate() != nil || request.ModelRevision.Validate() != nil ||
		!isHexDigest(request.RetentionDigest) || request.PurgeGeneration < 0 || request.TimeBudgetSeconds < 0 || request.TurnBudget < 0 || request.AudioBudgetSeconds < 0 || request.TokenBudget < 0 || request.CostBudgetCents < 0 ||
		request.MaxAnalysisRefs < 0 || request.MaxBrainRefs < 0 || request.MaxWorkRefs < 0 || !uniqueSTRIDEIDs(request.ToolIDs) || !strideIdentifier(request.ResponseContract) || !strideIdentifier(request.FloorPolicy) {
		return result, ErrTemporalBrainInvalid
	}
	for _, interval := range request.ApprovedIntervals {
		if interval.RoomID != request.Invitation.RoomID || interval.SittingID != request.Invitation.SittingID {
			return result, ErrTemporalContextUnauthorized
		}
	}
	approvedTranscript := make([]STRIDEReference, 0, len(request.Answer.Sources))
	for _, source := range request.Answer.Sources {
		if !coveredByIntervals(source.CoveredStart, source.CoveredEnd, request.ApprovedIntervals) {
			return result, ErrTemporalContextUnauthorized
		}
		if source.BodyOmitted || !coveredByIntervals(source.SourceStart, source.SourceEnd, request.ApprovedIntervals) {
			continue
		}
		approvedTranscript = append(approvedTranscript, source.Evidence)
	}
	gaps := append([]string(nil), request.Answer.Coverage.Gaps...)
	analysis, analysisGap := filterContextReferences(request.Analysis, request.Principal, request.ConsentScopes, request.ApprovedIntervals, request.Answer.TranscriptHighWater, request.MaxAnalysisRefs, true)
	brain, brainGap := filterContextReferences(request.Brain, request.Principal, request.ConsentScopes, request.ApprovedIntervals, 0, request.MaxBrainRefs, false)
	work, workGap := filterContextReferences(request.Work, request.Principal, request.ConsentScopes, request.ApprovedIntervals, 0, request.MaxWorkRefs, false)
	if analysisGap != "" {
		gaps = append(gaps, analysisGap)
	}
	if brainGap != "" {
		gaps = append(gaps, brainGap)
	}
	if workGap != "" {
		gaps = append(gaps, workGap)
	}
	gaps = sortedUniqueStrings(gaps)
	coverageMaterial := struct {
		Intervals                         []TemporalQuery `json:"intervals"`
		Transcript, Analysis, Brain, Work []STRIDEReference
	}{request.ApprovedIntervals, approvedTranscript, analysis, brain, work}
	coverageRaw, _ := canonicalJSON(coverageMaterial)
	coverageDigest := temporalDigestBytes(coverageRaw)
	gapsRaw, _ := canonicalJSON(gaps)
	envelope := MeetingSpecialistContextEnvelope{
		Header: request.Header, Invitation: referenceFromHeader(request.Invitation.Header), AgentProfile: request.AgentProfile, RuntimeRevision: request.RuntimeRevision, ModelRevision: request.ModelRevision,
		TranscriptRefs: approvedTranscript, AnalysisRefs: analysis, BrainRefs: brain, WorkRefs: work,
		Audience: STRIDEAudience{Visibility: "private", Principals: []string{request.Principal.ID}}, RetentionDigest: request.RetentionDigest, PurgeGeneration: request.PurgeGeneration,
		TranscriptHighWater: request.Answer.TranscriptHighWater, AnalysisHighWater: request.Answer.AnalysisHighWater,
		GapsDigest: temporalDigestBytes(gapsRaw), CoverageDigest: coverageDigest, ToolIDs: sortedStrings(request.ToolIDs), ResponseContract: request.ResponseContract, FloorPolicy: request.FloorPolicy,
		TimeBudgetSeconds: request.TimeBudgetSeconds, TurnBudget: request.TurnBudget, AudioBudgetSeconds: request.AudioBudgetSeconds, TokenBudget: request.TokenBudget, CostBudgetCents: request.CostBudgetCents,
	}
	if len(brain) > 0 {
		allowed := map[string]bool{}
		for _, ref := range brain {
			allowed[referenceKey(ref)] = true
		}
		for _, item := range request.Brain {
			if allowed[referenceKey(item.Reference)] && item.HighWater > envelope.BrainHighWater {
				envelope.BrainHighWater = item.HighWater
			}
		}
	}
	contextMaterial := struct {
		IntervalDigest, CoverageDigest, GapsDigest string
		Envelope                                   MeetingSpecialistContextEnvelope
	}{intervalDigest, coverageDigest, envelope.GapsDigest, envelope}
	contextRaw, _ := canonicalJSON(contextMaterial)
	envelope.ContextDigest = temporalDigestBytes(contextRaw)
	envelope.Header.ContentDigest = envelope.ContextDigest
	if envelope.Validate() != nil {
		return result, ErrTemporalBrainInvalid
	}
	return AssembledMeetingSpecialistContext{Envelope: envelope, Intervals: append([]TemporalQuery(nil), request.ApprovedIntervals...), Gaps: gaps}, nil
}

func filterContextReferences(items []TemporalContextReference, principal ACLPrincipal, consent []string, intervals []TemporalQuery, sourceHighWater uint64, limit int, requireFresh bool) ([]STRIDEReference, string) {
	filtered := make([]STRIDEReference, 0, len(items))
	omitted := false
	for _, item := range items {
		if item.Reference.Validate() != nil || item.Audience.Validate() != nil || !audienceAllows(item.Audience, principal) || !containsAll(consent, item.ConsentScopes) ||
			item.WindowStart.IsZero() || item.WindowEnd.IsZero() || !item.WindowStart.Before(item.WindowEnd) || !coveredByIntervals(item.WindowStart, item.WindowEnd, intervals) {
			omitted = true
			continue
		}
		if requireFresh && (item.HighWater < sourceHighWater || item.FreshThrough.Before(maxIntervalEnd(intervals))) {
			omitted = true
			continue
		}
		filtered = append(filtered, item.Reference)
	}
	sort.Slice(filtered, func(i, j int) bool { return referenceKey(filtered[i]) < referenceKey(filtered[j]) })
	if limit < len(filtered) {
		filtered = filtered[:limit]
		omitted = true
	}
	if omitted {
		return filtered, "authorized_context_filtered_or_stale"
	}
	return filtered, ""
}

func (brain *TemporalMeetingBrain) Snapshot() ([]byte, error) {
	if brain == nil {
		return nil, ErrTemporalBrainSnapshot
	}
	snapshot, err := brain.snapshotValue()
	if err != nil {
		return nil, err
	}
	return canonicalJSON(snapshot)
}

func (brain *TemporalMeetingBrain) snapshotValue() (temporalMeetingBrainSnapshot, error) {
	snapshot := temporalMeetingBrainSnapshot{Format: temporalMeetingBrainSnapshotFormat, SnapshotGeneration: brain.snapshotGeneration, Config: brain.config, LastSequence: brain.lastSequence,
		TranscriptHighWater: brain.transcriptHighWater, AnalysisHighWater: brain.analysisHighWater, PurgeGeneration: brain.purgeGeneration,
		Sources: brain.sortedSources(), Facts: brain.sortedFacts()}
	for id := range brain.purgedRevisions {
		snapshot.PurgedRevisions = append(snapshot.PurgedRevisions, id)
	}
	sort.Strings(snapshot.PurgedRevisions)
	digest, err := snapshot.canonicalStateDigest()
	if err != nil {
		return temporalMeetingBrainSnapshot{}, err
	}
	snapshot.StateDigest = digest
	return snapshot, nil
}

// AuthenticatedSnapshot binds the meeting state and a monotonic generation to
// a configured MAC authority. The key and the last accepted generation must be
// held outside the snapshot (normally in KMS plus a durable restore ledger).
func (brain *TemporalMeetingBrain) AuthenticatedSnapshot(authority STRIDESnapshotMACAuthority, generation uint64) ([]byte, error) {
	if brain == nil || !authority.valid() || generation == 0 || generation <= brain.snapshotGeneration {
		return nil, ErrTemporalBrainSnapshot
	}
	snapshot, err := brain.snapshotValue()
	if err != nil {
		return nil, err
	}
	snapshot.SnapshotGeneration = generation
	snapshot.KeyID = authority.KeyID
	snapshot.Signature, err = strideSnapshotMAC(authority, "temporal_meeting_brain", generation, snapshot.StateDigest)
	if err != nil {
		return nil, ErrTemporalBrainSnapshot
	}
	brain.snapshotGeneration = generation
	return canonicalJSON(snapshot)
}

func RestoreTemporalMeetingBrain(raw []byte, policies ...STRIDESnapshotRestorePolicy) (*TemporalMeetingBrain, error) {
	var snapshot temporalMeetingBrainSnapshot
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&snapshot); err != nil {
		return nil, ErrTemporalBrainSnapshot
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, ErrTemporalBrainSnapshot
	}
	want, err := snapshot.canonicalStateDigest()
	if len(policies) != 1 || err != nil || snapshot.Format != temporalMeetingBrainSnapshotFormat || !isHexDigest(snapshot.StateDigest) || snapshot.StateDigest != want || snapshot.Config.validate() != nil || snapshot.PurgeGeneration < 0 || snapshot.TranscriptHighWater > snapshot.LastSequence || snapshot.AnalysisHighWater > snapshot.LastSequence || snapshot.SnapshotGeneration < uint64(snapshot.PurgeGeneration) || !verifySTRIDESnapshotMAC(policies[0], "temporal_meeting_brain", snapshot.KeyID, snapshot.SnapshotGeneration, snapshot.StateDigest, snapshot.Signature) {
		return nil, ErrTemporalBrainSnapshot
	}
	brain, _ := NewTemporalMeetingBrain(snapshot.Config)
	brain.lastSequence, brain.transcriptHighWater, brain.analysisHighWater, brain.purgeGeneration = snapshot.LastSequence, snapshot.TranscriptHighWater, snapshot.AnalysisHighWater, snapshot.PurgeGeneration
	brain.snapshotGeneration = snapshot.SnapshotGeneration
	for _, id := range snapshot.PurgedRevisions {
		if !strideIdentifier(id) || brain.purgedRevisions[id] {
			return nil, ErrTemporalBrainSnapshot
		}
		brain.purgedRevisions[id] = true
	}
	seenSources := map[string]bool{}
	for _, source := range snapshot.Sources {
		if !validTemporalSnapshotSource(source, snapshot, brain.purgedRevisions) {
			return nil, ErrTemporalBrainSnapshot
		}
		if seenSources[source.SegmentID] {
			return nil, ErrTemporalBrainSnapshot
		}
		seenSources[source.SegmentID] = true
		brain.sources[source.SegmentID] = source
	}
	seenFacts := map[string]bool{}
	for _, fact := range snapshot.Facts {
		if !validTemporalSnapshotFact(fact, snapshot, brain.sources, brain.purgedRevisions) {
			return nil, ErrTemporalBrainSnapshot
		}
		if seenFacts[fact.ID] {
			return nil, ErrTemporalBrainSnapshot
		}
		seenFacts[fact.ID] = true
		brain.facts[fact.ID] = fact
	}
	return brain, nil
}

func (brain *TemporalMeetingBrain) StateDigest() (string, error) {
	raw, err := brain.Snapshot()
	if err != nil {
		return "", err
	}
	var snapshot temporalMeetingBrainSnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return "", err
	}
	return snapshot.StateDigest, nil
}

func (snapshot temporalMeetingBrainSnapshot) canonicalStateDigest() (string, error) {
	snapshot.StateDigest = ""
	snapshot.SnapshotGeneration = 0
	snapshot.KeyID = ""
	snapshot.Signature = ""
	raw, err := canonicalJSON(snapshot)
	if err != nil {
		return "", err
	}
	return temporalDigestBytes(raw), nil
}

func validTemporalSnapshotSource(source TemporalTranscriptSource, snapshot temporalMeetingBrainSnapshot, purged map[string]bool) bool {
	return strideIdentifier(source.SegmentID) && source.Revision.Validate() == nil && source.Revision.ContractType == STRIDEContractTranscriptRevision &&
		source.SegmentRef.Validate() == nil && source.SegmentRef.ContractType == STRIDEContractTranscriptSegment && source.SegmentRef.ID == source.SegmentID &&
		!purged[source.Revision.ID] && !source.SourceStart.IsZero() && source.SourceStart.Location() == time.UTC && source.SourceStart.Before(source.SourceEnd) && source.SourceEnd.Location() == time.UTC &&
		source.CaptureSequence > 0 && source.HighWater > 0 && source.HighWater <= snapshot.TranscriptHighWater && source.HighWater <= snapshot.LastSequence &&
		!source.CapturedAt.IsZero() && source.CapturedAt.Location() == time.UTC && strideIdentifier(source.Speaker) && strings.TrimSpace(source.Text) != "" &&
		source.Audience.Validate() == nil && len(source.ConsentScopes) > 0 && uniqueSTRIDEIDs(source.ConsentScopes) && source.ACLVersion > 0 &&
		source.PurgeGeneration >= 0 && source.PurgeGeneration <= snapshot.PurgeGeneration && validOptionalTemporalIDs(source.TopicIDs)
}

func validTemporalSnapshotFact(fact TemporalMeetingFact, snapshot temporalMeetingBrainSnapshot, sources map[string]TemporalTranscriptSource, purged map[string]bool) bool {
	if !strideIdentifier(fact.ID) || !oneOf(fact.Kind, "decision", "commitment", "blocker", "storyline", "alignment", "divergence", "position", "open_question", "entity", "project", "topic", "link", "file", "artifact", "vocabulary", "alias", "work_intent_candidate") ||
		strings.TrimSpace(fact.Statement) == "" || len(fact.Evidence) == 0 || !validateSTRIDERefs(fact.Evidence) || fact.WindowStart.IsZero() || fact.WindowStart.Location() != time.UTC || !fact.WindowStart.Before(fact.WindowEnd) || fact.WindowEnd.Location() != time.UTC ||
		fact.SourceHighWater == 0 || fact.SourceHighWater > snapshot.TranscriptHighWater || fact.SourceHighWater > snapshot.LastSequence || fact.FreshThrough.IsZero() || fact.FreshThrough.Location() != time.UTC || fact.Confidence < 0 || fact.Confidence > 1 || fact.Audience.Validate() != nil || !validOptionalTemporalIDs(fact.TopicIDs) {
		return false
	}
	for _, evidence := range fact.Evidence {
		if evidence.ContractType != STRIDEContractTranscriptRevision || purged[evidence.ID] {
			return false
		}
		current := false
		for _, source := range sources {
			if sameSTRIDEReference(source.Revision, evidence) {
				current = true
				break
			}
		}
		if !current {
			return false
		}
	}
	return true
}

func (brain *TemporalMeetingBrain) sortedSources() []TemporalTranscriptSource {
	result := make([]TemporalTranscriptSource, 0, len(brain.sources))
	for _, source := range brain.sources {
		result = append(result, source)
	}
	sort.Slice(result, func(i, j int) bool {
		if !result[i].SourceStart.Equal(result[j].SourceStart) {
			return result[i].SourceStart.Before(result[j].SourceStart)
		}
		return result[i].Revision.ID < result[j].Revision.ID
	})
	return result
}

func (brain *TemporalMeetingBrain) sortedFacts() []TemporalMeetingFact {
	result := make([]TemporalMeetingFact, 0, len(brain.facts))
	for _, fact := range brain.facts {
		result = append(result, fact)
	}
	sort.Slice(result, func(i, j int) bool {
		if !result[i].WindowStart.Equal(result[j].WindowStart) {
			return result[i].WindowStart.Before(result[j].WindowStart)
		}
		return result[i].ID < result[j].ID
	})
	return result
}

func resolveLocalClock(value, timezone string, fold int) (time.Time, error) {
	location, err := time.LoadLocation(strings.TrimSpace(timezone))
	if err != nil {
		return time.Time{}, ErrTemporalBrainInvalid
	}
	layout := "2006-01-02T15:04"
	if len(value) == len("2006-01-02T15:04:05") {
		layout = "2006-01-02T15:04:05"
	}
	wall, err := time.Parse(layout, strings.TrimSpace(value))
	if err != nil {
		return time.Time{}, ErrTemporalBrainInvalid
	}
	want := wall.Format(layout)
	offsets := map[int]bool{}
	for step := -72; step <= 72; step++ {
		_, offset := wall.Add(time.Duration(step) * 30 * time.Minute).In(location).Zone()
		offsets[offset] = true
	}
	candidates := make([]time.Time, 0, 2)
	for offset := range offsets {
		candidate := wall.Add(-time.Duration(offset) * time.Second).UTC()
		if candidate.In(location).Format(layout) == want {
			candidates = append(candidates, candidate)
		}
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].Before(candidates[j]) })
	if len(candidates) == 0 {
		return time.Time{}, ErrTemporalClockNonexistent
	}
	if len(candidates) > 1 {
		if fold != 1 && fold != 2 {
			return time.Time{}, ErrTemporalClockAmbiguous
		}
		return candidates[fold-1], nil
	}
	if fold != 0 && fold != 1 {
		return time.Time{}, ErrTemporalBrainInvalid
	}
	return candidates[0], nil
}

func (brain *TemporalMeetingBrain) clipInterval(start, end time.Time) (time.Time, time.Time, error) {
	if !start.Before(end) {
		return time.Time{}, time.Time{}, ErrTemporalBrainInvalid
	}
	if start.Before(brain.config.SittingStart) {
		start = brain.config.SittingStart
	}
	if !brain.config.SittingEnd.IsZero() && end.After(brain.config.SittingEnd) {
		end = brain.config.SittingEnd
	}
	if !start.Before(end) {
		return time.Time{}, time.Time{}, ErrTemporalBrainInvalid
	}
	return start, end, nil
}

func oneTemporalQuery(query TemporalQuery, err error) ([]TemporalQuery, error) {
	if err != nil {
		return nil, err
	}
	return []TemporalQuery{query}, nil
}

func coalesceTemporalQueries(values []TemporalQuery) []TemporalQuery {
	if len(values) < 2 {
		return values
	}
	sort.Slice(values, func(i, j int) bool { return values[i].StartUTC.Before(values[j].StartUTC) })
	result := []TemporalQuery{values[0]}
	for _, value := range values[1:] {
		last := &result[len(result)-1]
		if !value.StartUTC.After(last.EndUTC) {
			if value.EndUTC.After(last.EndUTC) {
				last.EndUTC = value.EndUTC
			}
			continue
		}
		result = append(result, value)
	}
	return result
}

func temporalIntervalDigest(intervals []TemporalQuery) (string, error) {
	if len(intervals) == 0 {
		return "", ErrTemporalBrainInvalid
	}
	for _, interval := range intervals {
		if interval.Validate() != nil {
			return "", ErrTemporalBrainInvalid
		}
	}
	raw, err := canonicalJSON(intervals)
	if err != nil {
		return "", err
	}
	return temporalDigestBytes(raw), nil
}

// TemporalApprovedIntervalDigest binds an approved specialist invitation to
// the exact ordered set of half-open source intervals it may receive.
func TemporalApprovedIntervalDigest(intervals []TemporalQuery) (string, error) {
	return temporalIntervalDigest(intervals)
}

func referenceFromHeader(header STRIDEContractHeader) STRIDEReference {
	return STRIDEReference{ContractType: header.ContractType, ID: header.ID, Revision: header.Revision, Digest: header.ContentDigest}
}
func sameSTRIDEReference(a, b STRIDEReference) bool {
	return a.ContractType == b.ContractType && a.ID == b.ID && a.Revision == b.Revision && a.Digest == b.Digest
}
func referenceKey(ref STRIDEReference) string {
	return string(ref.ContractType) + "\x00" + ref.ID + "\x00" + fmt.Sprint(ref.Revision) + "\x00" + ref.Digest
}
func temporalDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
func temporalDigestBytes(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}
func cloneAudience(value STRIDEAudience) STRIDEAudience {
	return STRIDEAudience{Visibility: value.Visibility, Principals: append([]string(nil), value.Principals...)}
}
func sortedStrings(values []string) []string {
	result := append([]string(nil), values...)
	sort.Strings(result)
	return result
}
func sortedUniqueStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	result := sortedStrings(values)
	out := result[:0]
	for _, v := range result {
		if len(out) == 0 || out[len(out)-1] != v {
			out = append(out, v)
		}
	}
	return out
}
func temporalContainsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
func containsAll(have, required []string) bool {
	for _, item := range required {
		if !temporalContainsString(have, item) {
			return false
		}
	}
	return true
}
func validOptionalTemporalIDs(values []string) bool {
	return len(values) == 0 || uniqueSTRIDEIDs(values)
}

func audienceAllows(audience STRIDEAudience, principal ACLPrincipal) bool {
	if audience.Validate() != nil {
		return false
	}
	if temporalContainsString(audience.Principals, principal.ID) {
		return true
	}
	for _, team := range principal.TeamIDs {
		if temporalContainsString(audience.Principals, team) {
			return true
		}
	}
	return false
}

func factCoveredByIntervals(fact TemporalMeetingFact, intervals []TemporalQuery) bool {
	return coveredByIntervals(fact.WindowStart, fact.WindowEnd, intervals)
}
func overlapsIntervals(start, end time.Time, intervals []TemporalQuery) bool {
	for _, interval := range intervals {
		if start.Before(interval.EndUTC) && interval.StartUTC.Before(end) {
			return true
		}
	}
	return false
}
func coveredByIntervals(start, end time.Time, intervals []TemporalQuery) bool {
	for _, interval := range intervals {
		if !start.Before(interval.StartUTC) && !end.After(interval.EndUTC) {
			return true
		}
	}
	return false
}
func maxIntervalEnd(intervals []TemporalQuery) time.Time {
	var result time.Time
	for _, interval := range intervals {
		if interval.EndUTC.After(result) {
			result = interval.EndUTC
		}
	}
	return result
}
