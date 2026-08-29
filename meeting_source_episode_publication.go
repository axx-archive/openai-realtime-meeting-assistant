package main

// Post-close publication is deliberately downstream of durable meeting
// finalization. It reads only the indexed entries for one sitting, freezes the
// exact analysis/source authority, and dual-writes a body-free SourceEpisode.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

var (
	ErrMeetingSourceEpisodePreempted = errors.New("meeting source episode publication yielded to an active meeting")
	ErrMeetingSourceEpisodeAdapter   = errors.New("meeting source episode requires a transactional canonical authority adapter")
)

type postCloseMeetingSourceMaterial struct {
	Record        meetingRecord
	Source        meetingFinalizationSourceHighWater
	ReceiptDigest string
	Segments      []MeetingSourceEpisodeSegment
	Transcripts   []meetingMemoryEntry
	Audience      STRIDEAudience
	ACLRevision   int64
	ACLDigest     string
	Consent       []ConsentFence
	ConsentRev    int64
	ConsentDigest string
	Purge         int64
	Brain         meetingMemoryEntry
	Digest        meetingMemoryEntry
	StartedAt     time.Time
	ClosedAt      time.Time
	PublishedAt   time.Time
}

type postCloseMeetingSourceEpisodeAuthority struct {
	app      *kanbanBoardApp
	expected postCloseMeetingSourceMaterial
	episode  MeetingSourceEpisode
}

type postCloseMeetingSourceEpisodeRepository struct {
	writer  SourceEpisodeDualWriter
	episode SourceEpisode
}

func postCloseMeetingSourceEpisodeLedgerPath() string {
	if path := strings.TrimSpace(os.Getenv("SOURCE_EPISODE_LEDGER_PATH")); path != "" {
		return path
	}
	return filepath.Join(filepath.Dir(meetingMemoryPath()), "source-episodes.jsonl")
}

func meetingSourceEpisodeID(meetingID string) string {
	return "meeting_source_episode_" + sha256Hex([]byte(strings.TrimSpace(meetingID)))[:24]
}

func (app *kanbanBoardApp) publishFinalizedMeetingSourceEpisode(ctx context.Context, record meetingRecord) (STRIDEReference, error) {
	if app == nil || app.memory == nil || app.meetings == nil || record.Finalization == nil || !meetingFinalizationReceiptReady(record.Finalization) ||
		!app.meetingFinalizationOutputsReady(record) || record.Finalization.Source.TranscriptCount == 0 {
		return STRIDEReference{}, ErrMeetingSourceEpisodeUnavailable
	}
	if len(app.meetings.openRoomIDs()) > 0 {
		return STRIDEReference{}, ErrMeetingSourceEpisodePreempted
	}
	if active, found := app.meetings.activeRecord(meetingRoomID(record)); found && active.ID != record.ID {
		return STRIDEReference{}, ErrMeetingSourceEpisodePreempted
	}
	ledger := app.sourceEpisodes
	postgres := app.postgresMeetingSourceEpisodes
	if app.sourceEpisodesErr != nil || ledger == nil || currentCanonicalRuntime() != nil && currentCanonicalRuntime().postgres != nil && postgres == nil {
		return STRIDEReference{}, ErrMeetingSourceEpisodeUnavailable
	}
	material, err := resolvePostCloseMeetingSourceMaterial(ctx, app, record)
	if err != nil {
		return STRIDEReference{}, err
	}
	if postgres != nil {
		material, err = postgres.ResolveAuthority(ctx, material)
		if err != nil {
			return STRIDEReference{}, err
		}
	}
	id := meetingSourceEpisodeID(record.ID)
	current, found, err := app.latestMeetingSourceEpisode(ctx, id)
	if err != nil {
		return STRIDEReference{}, err
	}
	activeCurrent, activeFound, activeErr := app.currentMeetingSourceEpisode(ctx, id)
	if activeErr != nil {
		return STRIDEReference{}, activeErr
	}
	if found && activeFound && activeCurrent.Header.ContentDigest == current.Header.ContentDigest && current.Kind == SourceEpisodeMeetingAnalysis && current.PhaseProof.ReceiptDigest == material.ReceiptDigest &&
		current.RetrievalBody.ContentDigest == material.Brain.BodyDigest && current.Authority.ACLDigest == material.ACLDigest &&
		current.Authority.ConsentDigest == material.ConsentDigest && current.Authority.PurgeGeneration == material.Purge {
		return referenceFromHeader(current.Header), nil
	}
	native, generic, err := buildPostCloseMeetingSourceEpisodes(material, current, found)
	if err != nil {
		return STRIDEReference{}, err
	}
	authority := &postCloseMeetingSourceEpisodeAuthority{app: app, expected: material, episode: native}
	if postgres != nil {
		result, err := authority.publishPostgres(ctx, postgres, generic, generic.Supersedes)
		if err != nil {
			return STRIDEReference{}, err
		}
		return result.Reference, nil
	}
	repository := postCloseMeetingSourceEpisodeRepository{writer: ledger, episode: generic}
	if err := (MeetingSourceEpisodePublisher{Authority: authority, Repository: repository}).Publish(ctx, native); err != nil {
		return STRIDEReference{}, err
	}
	return referenceFromHeader(generic.Header), nil
}

func (app *kanbanBoardApp) latestMeetingSourceEpisode(ctx context.Context, episodeID string) (SourceEpisode, bool, error) {
	if app.postgresMeetingSourceEpisodes != nil {
		return app.postgresMeetingSourceEpisodes.LatestSourceEpisode(ctx, canonicalTenantID(), episodeID)
	}
	return app.sourceEpisodes.LatestSourceEpisode(ctx, canonicalTenantID(), episodeID)
}

func (app *kanbanBoardApp) currentMeetingSourceEpisode(ctx context.Context, episodeID string) (SourceEpisode, bool, error) {
	if app.postgresMeetingSourceEpisodes != nil {
		return app.postgresMeetingSourceEpisodes.CurrentSourceEpisode(ctx, canonicalTenantID(), episodeID)
	}
	return app.sourceEpisodes.CurrentSourceEpisode(ctx, canonicalTenantID(), episodeID)
}

func (app *kanbanBoardApp) activeMeetingSourceEpisodeForMutation(meetingID string) (SourceEpisode, bool) {
	if app == nil || app.sourceEpisodes == nil || strings.TrimSpace(meetingID) == "" {
		return SourceEpisode{}, false
	}
	current, found, err := app.currentMeetingSourceEpisode(context.Background(), meetingSourceEpisodeID(meetingID))
	if err != nil || !found {
		return SourceEpisode{}, false
	}
	return current, true
}

func (app *kanbanBoardApp) retractMeetingSourceEpisodeForMutation(meetingID, cause string, current SourceEpisode) {
	if app == nil || app.sourceEpisodes == nil || current.Validate() != nil || current.Header.ID != meetingSourceEpisodeID(meetingID) {
		return
	}
	ref := referenceFromHeader(current.Header)
	reason := sha256Hex([]byte(strings.TrimSpace(cause) + "\x00" + meetingID + "\x00" + current.Header.ContentDigest))
	tombstone := SourceEpisodeTombstone{
		TenantID: canonicalTenantID(), Episode: ref, Cause: SourceEpisodeTombstoneRetraction,
		PurgeGeneration: current.Authority.PurgeGeneration, ReasonDigest: reason, OccurredAt: time.Now().UTC(),
	}
	tombstone.IdempotencyKeyDigest = SourceEpisodeTombstoneIdempotencyKey(tombstone.TenantID, tombstone.Episode, tombstone.Cause, tombstone.PurgeGeneration)
	var err error
	if app.postgresMeetingSourceEpisodes != nil {
		_, err = app.postgresMeetingSourceEpisodes.TombstoneSourceEpisode(context.Background(), tombstone)
	} else {
		_, err = app.sourceEpisodes.TombstoneSourceEpisode(context.Background(), tombstone)
	}
	if err != nil && !errors.Is(err, ErrSourceEpisodeConflict) {
		log.Errorf("Meeting %s SourceEpisode retraction failed: %v", meetingID, err)
	}
}

// handleMeetingSourceEpisodeOutputAppend invalidates only the derived episode.
// Core finalization owns the append itself; scheduling another finalization
// from its normal first output would create a feedback loop.
func (app *kanbanBoardApp) handleMeetingSourceEpisodeOutputAppend(entry meetingMemoryEntry) {
	if app == nil || !meetingFinalizationOutputEntry(entry) {
		return
	}
	meetingID := strings.TrimSpace(entry.Metadata["meetingId"])
	if episode, active := app.activeMeetingSourceEpisodeForMutation(meetingID); active {
		go app.retractMeetingSourceEpisodeForMutation(meetingID, "analysis_output_appended", episode)
	}
}

func (app *kanbanBoardApp) publishFinalizedMeetingSourceEpisodeFailSoft(ctx context.Context, record meetingRecord) {
	if record.Finalization == nil || record.Finalization.Source.TranscriptCount == 0 {
		return
	}
	if _, err := app.publishFinalizedMeetingSourceEpisode(ctx, record); err != nil {
		if errors.Is(err, ErrMeetingSourceEpisodePreempted) || errors.Is(err, ErrMeetingSourceEpisodeAdapter) ||
			errors.Is(err, ErrMeetingSourceEpisodeUnavailable) || errors.Is(err, ErrMeetingSourceEpisodeStale) {
			log.Infof("Meeting %s SourceEpisode publication deferred: %v", record.ID, err)
			return
		}
		log.Errorf("Meeting %s SourceEpisode publication failed: %v", record.ID, err)
	}
}

// scheduleMeetingSourceEpisodeRetrySweep coalesces retry signals at stable
// close boundaries. The durable intent is the finalized meeting record plus
// the absence of its active episode in the append-only ledger; no timer or
// process-local queue is required for restart recovery.
func (app *kanbanBoardApp) scheduleMeetingSourceEpisodeRetrySweep() {
	// Stable close is also the only retry signal for optional shadow cognition.
	// Its own idle admission remains fail-closed and media-first.
	app.scheduleSTRIDELeadShadowRetrySweep()
	if app == nil || app.meetings == nil || app.sourceEpisodes == nil {
		return
	}
	app.meetingFinalizationRunMu.Lock()
	if app.sourceEpisodeRetrySweep {
		app.sourceEpisodeRetryAgain = true
		app.meetingFinalizationRunMu.Unlock()
		return
	}
	app.sourceEpisodeRetrySweep = true
	app.meetingFinalizationRunMu.Unlock()
	go app.runMeetingSourceEpisodeRetrySweep()
}

func (app *kanbanBoardApp) runMeetingSourceEpisodeRetrySweep() {
	for {
		// Live media wins. A later stable close boundary signals this coalescer
		// again; restart also derives the same missing work from durable state.
		if len(app.meetings.openRoomIDs()) == 0 {
			for _, record := range app.meetings.recordsWithFinalization() {
				if len(app.meetings.openRoomIDs()) > 0 {
					break
				}
				if record.Finalization == nil || record.Finalization.State != meetingFinalizationFinalized ||
					record.Finalization.Source.TranscriptCount == 0 || !app.meetingFinalizationOutputsReady(record) {
					continue
				}
				if _, err := app.publishFinalizedMeetingSourceEpisode(context.Background(), record); err != nil &&
					!errors.Is(err, ErrMeetingSourceEpisodePreempted) && !errors.Is(err, ErrMeetingSourceEpisodeStale) &&
					!errors.Is(err, ErrMeetingSourceEpisodeAdapter) && !errors.Is(err, ErrMeetingSourceEpisodeUnavailable) {
					log.Errorf("Meeting %s SourceEpisode retry failed: %v", record.ID, err)
				}
			}
		}
		app.meetingFinalizationRunMu.Lock()
		if app.sourceEpisodeRetryAgain {
			app.sourceEpisodeRetryAgain = false
			app.meetingFinalizationRunMu.Unlock()
			continue
		}
		app.sourceEpisodeRetrySweep = false
		app.meetingFinalizationRunMu.Unlock()
		return
	}
}

func buildPostCloseMeetingSourceEpisodes(material postCloseMeetingSourceMaterial, current SourceEpisode, found bool) (MeetingSourceEpisode, SourceEpisode, error) {
	revision := int64(1)
	var nativeSupersedes, genericSupersedes *STRIDEReference
	if found {
		if current.Validate() != nil || current.Kind != SourceEpisodeMeetingAnalysis || current.Header.ID != meetingSourceEpisodeID(material.Record.ID) ||
			current.Source.SourceFamily != SourceEpisodeFamilyMeetingAnalysis || current.Source.ObjectID != current.Header.ID {
			return MeetingSourceEpisode{}, SourceEpisode{}, ErrMeetingSourceEpisodeConflict
		}
		revision = current.Header.Revision + 1
		nativeRef := STRIDEReference{ContractType: STRIDEContractMeetingSourceEpisode, ID: current.Source.ObjectID, Revision: current.Source.ContentRevision, Digest: current.Source.ContentDigest}
		genericRef := referenceFromHeader(current.Header)
		nativeSupersedes, genericSupersedes = &nativeRef, &genericRef
	}
	manifestDigest, err := STRIDEContractDigest(material.Segments)
	if err != nil {
		return MeetingSourceEpisode{}, SourceEpisode{}, err
	}
	highWater := material.Source.CaptureSequence
	analysisHighWater := material.Record.Finalization.SourceObservedRevision
	if analysisHighWater == 0 {
		analysisHighWater = 1
	}
	eventHighWater := highWater
	if analysisHighWater > eventHighWater {
		eventHighWater = analysisHighWater
	}
	conversationID := "meeting_conversation_" + sha256Hex([]byte(material.Record.ID))[:24]
	conversation := STRIDEReference{ContractType: STRIDEContractConversationEvent, ID: conversationID, Revision: revision, Digest: material.Source.ManifestDigest}
	brainRef := STRIDEReference{ContractType: STRIDEContractAnalysisProjection, ID: "meeting_analysis_" + sha256Hex([]byte(material.Brain.ID))[:24], Revision: int64(material.Record.Finalization.Brain.OutputRevision), Digest: material.Brain.BodyDigest}
	digestRef := STRIDEReference{ContractType: STRIDEContractKnowledgeAssertion, ID: "meeting_digest_" + sha256Hex([]byte(material.Digest.ID))[:24], Revision: int64(material.Record.Finalization.Digest.OutputRevision), Digest: material.Digest.BodyDigest}
	native := MeetingSourceEpisode{
		Header: STRIDEContractHeader{TenantID: canonicalTenantID(), ID: meetingSourceEpisodeID(material.Record.ID), Revision: revision, SchemaVersion: STRIDEContractSchemaVersion,
			ContractType: STRIDEContractMeetingSourceEpisode, CreatedAt: material.PublishedAt},
		ConversationRef: conversation, RoomID: meetingRoomID(material.Record), SittingID: material.Record.ID,
		MeetingStartedAt: material.StartedAt, MeetingClosedAt: material.ClosedAt, MeetingEventHighWater: eventHighWater,
		TranscriptHighWater: highWater, AnalysisHighWater: analysisHighWater, CaptureHighWater: highWater,
		Segments: material.Segments, SourceManifestDigest: manifestDigest, AnalysisRefs: []STRIDEReference{brainRef, digestRef},
		AnalysisBodyRef: material.Brain.ID, AnalysisBodyDigest: material.Brain.BodyDigest, Audience: material.Audience,
		ACLRevision: material.ACLRevision, ACLDigest: material.ACLDigest, ConsentRevision: material.ConsentRev, ConsentDigest: material.ConsentDigest,
		PurgeGeneration: material.Purge, RawTranscriptAccess: MeetingSourceEpisodeRawExactSegments, Status: "published",
		Supersedes: nativeSupersedes, PublishedAt: material.PublishedAt,
	}
	native.Header.ContentDigest, err = native.ContentDigest()
	if err != nil {
		return MeetingSourceEpisode{}, SourceEpisode{}, fmt.Errorf("digest meeting source episode: %w", err)
	}
	if err := native.Validate(); err != nil {
		return MeetingSourceEpisode{}, SourceEpisode{}, fmt.Errorf("validate meeting source episode: %w", err)
	}
	nativeRaw, err := json.Marshal(native)
	if err != nil {
		return MeetingSourceEpisode{}, SourceEpisode{}, fmt.Errorf("adapt canonical source episode: %w", err)
	}
	genericHeader := native.Header
	genericHeader.ContractType = STRIDEContractSourceEpisode
	genericHeader.ContentDigest = ""
	generic, err := AdaptMeetingAnalysisSourceEpisode(SourceEpisodeAdapterInput{
		Header: genericHeader,
		Source: SourceEpisodeRevisionRef{SourceFamily: SourceEpisodeFamilyMeetingAnalysis, ObjectID: native.Header.ID, ContentRevision: native.Header.Revision,
			ContentDigest: native.Header.ContentDigest, SizeBytes: int64(len(nativeRaw))},
		RetrievalBody: SourceEpisodeRevisionRef{SourceFamily: SourceEpisodeFamilyMeetingAnalysisBody, ObjectID: material.Brain.ID,
			ContentRevision: int64(material.Record.Finalization.Brain.OutputRevision), ContentDigest: material.Brain.BodyDigest, SizeBytes: int64(len([]byte(material.Brain.Text)))},
		Scope: SourceEpisodeScope{CompanyID: canonicalTenantID(), ConversationID: conversation.ID, RoomID: meetingRoomID(material.Record), SittingID: material.Record.ID,
			MemoryScope: SourceEpisodeMemoryConversation},
		Authority: SourceEpisodeAuthoritySnapshot{Audience: material.Audience, ACLRevision: material.ACLRevision, ACLDigest: material.ACLDigest,
			ConsentRevision: material.ConsentRev, ConsentDigest: material.ConsentDigest, PurgeGeneration: material.Purge,
			RetentionPolicy: "company_default", ObservedAt: material.PublishedAt},
		OccurredStart: material.StartedAt, OccurredEnd: material.ClosedAt,
		PhaseProof: SourceEpisodePhaseProof{Phase: SourceEpisodePhasePostClose, BoundaryType: SourceEpisodeBoundaryMeetingClose,
			BoundaryAt: material.ClosedAt, ReceiptDigest: material.ReceiptDigest},
		Supersedes: genericSupersedes,
	})
	if err != nil {
		return MeetingSourceEpisode{}, SourceEpisode{}, err
	}
	return native, generic, nil
}

func (repository postCloseMeetingSourceEpisodeRepository) CommitMeetingSourceEpisode(ctx context.Context, native MeetingSourceEpisode, expected *STRIDEReference) error {
	if repository.writer == nil || native.Validate() != nil || repository.episode.Validate() != nil || repository.episode.Source.ObjectID != native.Header.ID ||
		repository.episode.Source.ContentRevision != native.Header.Revision || repository.episode.Source.ContentDigest != native.Header.ContentDigest ||
		!sameOptionalSTRIDEReference(expected, native.Supersedes) {
		return ErrMeetingSourceEpisodeInvalid
	}
	result, err := repository.writer.DualWriteSourceEpisode(ctx, repository.episode, repository.episode.Supersedes)
	if err != nil {
		return err
	}
	if result.Reference != referenceFromHeader(repository.episode.Header) {
		return ErrSourceEpisodeConflict
	}
	return nil
}

func (authority *postCloseMeetingSourceEpisodeAuthority) WithCurrentMeetingSourceEpisode(ctx context.Context, preconditions MeetingSourceEpisodePublicationPreconditions, commit func() error) error {
	if authority == nil || authority.app == nil || commit == nil || preconditions.Validate() != nil ||
		!sameMeetingSourceEpisodePreconditions(authority.episode.PublicationPreconditions(), preconditions) {
		return ErrMeetingSourceEpisodeInvalid
	}
	app := authority.app
	if active, found := app.meetings.activeRecord(meetingRoomID(authority.expected.Record)); found && active.ID != authority.expected.Record.ID {
		return ErrMeetingSourceEpisodePreempted
	}
	// Only the closed sitting is fenced across the durable append. A successor
	// sitting has a different lease and its O(delta) capture never waits here.
	lease := app.memory.sourceEpisodeLease(authority.expected.Record.ID)
	lease.Lock()
	defer lease.Unlock()
	if active, found := app.meetings.activeRecord(meetingRoomID(authority.expected.Record)); found && active.ID != authority.expected.Record.ID {
		return ErrMeetingSourceEpisodePreempted
	}
	currentRecord, found := app.meetings.recordByID(authority.expected.Record.ID)
	if !found || currentRecord.EndedAt == "" || !sameMeetingFinalizationVersion(currentRecord.Finalization, authority.expected.Record.Finalization) {
		return ErrMeetingSourceEpisodeStale
	}
	consentAuthority := currentConsentLaneAuthority()
	if consentAuthority == nil || len(authority.expected.Consent) == 0 {
		return ErrMeetingSourceEpisodeStale
	}
	return consentAuthority.CommitWithFences(ctx, authority.expected.Consent, func() error {
		entries := meetingSourceEpisodeEntries(app.memory, currentRecord.ID)
		current, err := derivePostCloseMeetingSourceMaterial(currentRecord, entries, authority.expected.Consent, authority.expected.ConsentRev, authority.expected.ConsentDigest, authority.expected.Audience)
		if err != nil || !samePostCloseMeetingSourceMaterial(current, authority.expected) {
			return ErrMeetingSourceEpisodeStale
		}
		return commit()
	})
}

func (authority *postCloseMeetingSourceEpisodeAuthority) publishPostgres(ctx context.Context, store *PostgresMeetingSourceEpisodeStore, episode SourceEpisode, expected *STRIDEReference) (SourceEpisodeDualWriteResult, error) {
	if authority == nil || authority.app == nil || store == nil || episode.Validate() != nil {
		return SourceEpisodeDualWriteResult{}, ErrMeetingSourceEpisodeInvalid
	}
	app := authority.app
	if len(app.meetings.openRoomIDs()) > 0 {
		return SourceEpisodeDualWriteResult{}, ErrMeetingSourceEpisodePreempted
	}
	lease := app.memory.sourceEpisodeLease(authority.expected.Record.ID)
	lease.Lock()
	defer lease.Unlock()
	if len(app.meetings.openRoomIDs()) > 0 {
		return SourceEpisodeDualWriteResult{}, ErrMeetingSourceEpisodePreempted
	}
	currentRecord, found := app.meetings.recordByID(authority.expected.Record.ID)
	if !found || currentRecord.EndedAt == "" || !sameMeetingFinalizationVersion(currentRecord.Finalization, authority.expected.Record.Finalization) {
		return SourceEpisodeDualWriteResult{}, ErrMeetingSourceEpisodeStale
	}
	entries := meetingSourceEpisodeEntries(app.memory, currentRecord.ID)
	current, err := derivePostCloseMeetingSourceMaterial(currentRecord, entries, authority.expected.Consent, authority.expected.ConsentRev, authority.expected.ConsentDigest, authority.expected.Audience)
	if err != nil {
		return SourceEpisodeDualWriteResult{}, ErrMeetingSourceEpisodeStale
	}
	// PostgreSQL owns these three authority values. The serializable commit
	// below re-resolves them while holding canonical source, consent and purge
	// fences; this assignment only lets the local source snapshot comparison
	// ignore its deliberately weaker development-only ACL derivation.
	current.ACLRevision, current.ACLDigest, current.Purge = authority.expected.ACLRevision, authority.expected.ACLDigest, authority.expected.Purge
	if !samePostCloseMeetingSourceMaterial(current, authority.expected) {
		return SourceEpisodeDualWriteResult{}, ErrMeetingSourceEpisodeStale
	}
	return store.CommitMeetingSourceEpisode(ctx, episode, expected, authority.expected)
}

func resolvePostCloseMeetingSourceMaterial(ctx context.Context, app *kanbanBoardApp, record meetingRecord) (postCloseMeetingSourceMaterial, error) {
	if app == nil || app.memory == nil || app.meetings == nil || record.Finalization == nil || !meetingFinalizationReceiptReady(record.Finalization) {
		return postCloseMeetingSourceMaterial{}, ErrMeetingSourceEpisodeUnavailable
	}
	entries := meetingSourceEpisodeEntries(app.memory, record.ID)
	consent, revision, digest, audience, err := resolveMeetingSourceEpisodeConsent(ctx, entries)
	if err != nil {
		return postCloseMeetingSourceMaterial{}, err
	}
	return derivePostCloseMeetingSourceMaterial(record, entries, consent, revision, digest, audience)
}

func derivePostCloseMeetingSourceMaterial(record meetingRecord, entries []meetingMemoryEntry, consent []ConsentFence, consentRevision int64, consentDigest string, audience STRIDEAudience) (postCloseMeetingSourceMaterial, error) {
	if record.Finalization == nil || !meetingFinalizationReceiptReady(record.Finalization) || len(entries) == 0 || audience.Validate() != nil ||
		len(consent) == 0 || consentRevision < 1 || !isHexDigest(consentDigest) {
		return postCloseMeetingSourceMaterial{}, ErrMeetingSourceEpisodeStale
	}
	startedAt, startErr := time.Parse(time.RFC3339Nano, record.StartedAt)
	closedAt, closeErr := time.Parse(time.RFC3339Nano, record.EndedAt)
	publishedAt, publishErr := time.Parse(time.RFC3339Nano, record.Finalization.FinalizedAt)
	if startErr != nil || closeErr != nil || publishErr != nil || !startedAt.Before(closedAt) || publishedAt.Before(closedAt) {
		return postCloseMeetingSourceMaterial{}, ErrMeetingSourceEpisodeStale
	}
	transcripts := meetingFinalizationOrderedTranscripts(entries)
	segments := make([]MeetingSourceEpisodeSegment, 0, len(transcripts))
	var brain, digest meetingMemoryEntry
	for _, entry := range entries {
		switch {
		case entry.Kind == meetingMemoryKindBrain && entry.ID == record.Finalization.Brain.OutputID:
			brain = entry
		case entry.Kind == meetingMemoryKindMeetingDigest && entry.ID == record.Finalization.Digest.OutputID:
			digest = entry
		}
	}
	if len(transcripts) != record.Finalization.Source.TranscriptCount || brain.ID == "" || digest.ID == "" ||
		!meetingFinalizationStageOutputMatches(record.Finalization.Brain, brain) || !meetingFinalizationStageOutputMatches(record.Finalization.Digest, digest) {
		return postCloseMeetingSourceMaterial{}, ErrMeetingSourceEpisodeStale
	}
	brain.BodyDigest = sha256Hex([]byte(brain.Text))
	digest.BodyDigest = sha256Hex([]byte(digest.Text))
	refRevision := int64(record.Finalization.SourceObservedRevision)
	if refRevision < 1 {
		refRevision = 1
	}
	for _, entry := range transcripts {
		sequence, ok := entryCaptureSequence(entry)
		start, end, _ := brainMemoryEntryTimes(entry)
		if !ok || !start.Before(end) || entry.BodyDigest == "" {
			return postCloseMeetingSourceMaterial{}, ErrMeetingSourceEpisodeStale
		}
		idDigest := sha256Hex([]byte(entry.ID))
		segments = append(segments, MeetingSourceEpisodeSegment{
			SegmentRef:      STRIDEReference{ContractType: STRIDEContractTranscriptSegment, ID: "segment_" + idDigest[:24], Revision: refRevision, Digest: entry.BodyDigest},
			TranscriptRef:   STRIDEReference{ContractType: STRIDEContractTranscriptRevision, ID: "transcript_revision_" + idDigest[:24], Revision: refRevision, Digest: entry.BodyDigest},
			CaptureSequence: sequence, SourceStart: start, SourceEnd: end, ACLRevision: refRevision,
			ConsentFenceDigest: meetingSourceEpisodeEntryConsentDigest(entry, consent), PurgeGeneration: 0,
		})
	}
	aclDigest, err := STRIDEContractDigest(struct {
		MeetingID string
		Audience  STRIDEAudience
		Revision  int64
	}{record.ID, audience, refRevision})
	if err != nil {
		return postCloseMeetingSourceMaterial{}, err
	}
	receiptDigest, err := STRIDEContractDigest(*record.Finalization)
	if err != nil {
		return postCloseMeetingSourceMaterial{}, err
	}
	return postCloseMeetingSourceMaterial{
		Record: cloneMeetingRecord(record), Source: record.Finalization.Source, ReceiptDigest: receiptDigest, Segments: segments, Transcripts: cloneMemoryEntries(transcripts), Audience: cloneAudience(audience),
		ACLRevision: refRevision, ACLDigest: aclDigest, Consent: append([]ConsentFence(nil), consent...), ConsentRev: consentRevision,
		ConsentDigest: consentDigest, Purge: 0, Brain: cloneMemoryEntry(brain), Digest: cloneMemoryEntry(digest),
		StartedAt: startedAt.UTC(), ClosedAt: closedAt.UTC(), PublishedAt: publishedAt.UTC(),
	}, nil
}

func resolveMeetingSourceEpisodeConsent(ctx context.Context, entries []meetingMemoryEntry) ([]ConsentFence, int64, string, STRIDEAudience, error) {
	authority := currentConsentLaneAuthority()
	if authority == nil {
		return nil, 0, "", STRIDEAudience{}, ErrMeetingSourceEpisodeStale
	}
	bindings := map[string]ConsentAdmissionBinding{}
	for _, entry := range entries {
		if entry.Kind != meetingMemoryKindTranscript {
			continue
		}
		decoded, err := decodeConsentContributorBindings(entry.Metadata[consentContributorBindingsMetadataKey])
		if err != nil || len(decoded) == 0 {
			return nil, 0, "", STRIDEAudience{}, ErrMeetingSourceEpisodeStale
		}
		for _, binding := range decoded {
			bindings[consentBindingKey(binding)] = binding
		}
	}
	keys := make([]string, 0, len(bindings))
	for key := range bindings {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	fences := make([]ConsentFence, 0, len(keys)*2)
	principals := make([]string, 0, len(keys))
	for _, key := range keys {
		binding := bindings[key]
		for _, lane := range []ConsentLane{ConsentLaneModelAnalysis, ConsentLaneOrgMemory} {
			decision, err := authority.Authorize(ctx, binding, lane)
			if err != nil || !decision.Allowed {
				return nil, 0, "", STRIDEAudience{}, ErrMeetingSourceEpisodeStale
			}
			fences = append(fences, decision.Fence)
		}
		if binding.PrincipalKind == ACLPrincipalUser {
			principals = append(principals, strideRuntimePrincipalForEmail(binding.PrincipalID))
		} else {
			principals = append(principals, "guest:"+sha256Hex([]byte(binding.PrincipalID))[:24])
		}
	}
	principals = sortedUniqueSTRIDEIDs(principals)
	audience := STRIDEAudience{Visibility: "meeting", Principals: principals}
	if audience.Validate() != nil || len(fences) == 0 {
		return nil, 0, "", STRIDEAudience{}, ErrMeetingSourceEpisodeStale
	}
	var generation uint64
	type consentMaterial struct {
		Binding      string
		Lane         ConsentLane
		Policy       string
		Generation   uint64
		RecordDigest string
	}
	materials := make([]consentMaterial, 0, len(fences))
	for _, fence := range fences {
		if fence.generation > generation {
			generation = fence.generation
		}
		materials = append(materials, consentMaterial{consentBindingKey(fence.binding), fence.lane, fence.policy, fence.generation, fence.recordDigest})
	}
	digest, err := STRIDEContractDigest(materials)
	if err != nil {
		return nil, 0, "", STRIDEAudience{}, err
	}
	return fences, int64(generation) + 1, digest, audience, nil
}

func meetingSourceEpisodeEntryConsentDigest(entry meetingMemoryEntry, fences []ConsentFence) string {
	bindings, _ := decodeConsentContributorBindings(entry.Metadata[consentContributorBindingsMetadataKey])
	allowed := map[string]bool{}
	for _, binding := range bindings {
		allowed[consentBindingKey(binding)] = true
	}
	type fenceDigest struct {
		Binding    string
		Lane       ConsentLane
		Policy     string
		Generation uint64
		Digest     string
	}
	parts := make([]fenceDigest, 0)
	for _, fence := range fences {
		key := consentBindingKey(fence.binding)
		if allowed[key] {
			parts = append(parts, fenceDigest{key, fence.lane, fence.policy, fence.generation, fence.recordDigest})
		}
	}
	digest, _ := STRIDEContractDigest(parts)
	return digest
}

func meetingSourceEpisodeEntries(store *meetingMemoryStore, meetingID string) []meetingMemoryEntry {
	if store == nil {
		return nil
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	return meetingSourceEpisodeEntriesLocked(store, meetingID)
}

func meetingSourceEpisodeEntriesLocked(store *meetingMemoryStore, meetingID string) []meetingMemoryEntry {
	if store.meetingEntryIndexes == nil {
		store.rebuildMeetingEntryIndexesLocked()
	}
	indexes := store.meetingEntryIndexes[strings.TrimSpace(meetingID)]
	entries := make([]meetingMemoryEntry, 0, len(indexes))
	for _, index := range indexes {
		if index >= 0 && index < len(store.entries) && strings.TrimSpace(store.entries[index].Metadata["meetingId"]) == strings.TrimSpace(meetingID) {
			entries = append(entries, cloneMemoryEntry(store.entries[index]))
		}
	}
	return entries
}

func samePostCloseMeetingSourceMaterial(left, right postCloseMeetingSourceMaterial) bool {
	left.Consent, right.Consent = nil, nil
	leftRaw, leftErr := canonicalJSON(left)
	rightRaw, rightErr := canonicalJSON(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftRaw, rightRaw)
}

func sameMeetingSourceEpisodePreconditions(left, right MeetingSourceEpisodePublicationPreconditions) bool {
	leftRaw, leftErr := canonicalJSON(left)
	rightRaw, rightErr := canonicalJSON(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftRaw, rightRaw)
}

func sameOptionalSTRIDEReference(left, right *STRIDEReference) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}
