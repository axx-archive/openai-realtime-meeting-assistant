package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"
)

// The native runtime is deliberately limited to already-durable, non-live
// sources. It retains no bodies and never scans history during publication.
func initializeSourceEpisodeNativeRuntime(app *kanbanBoardApp) (*SourceEpisodeRuntimeRegistry, error) {
	if app == nil || app.sourceEpisodes == nil || app.sourceEpisodesErr != nil {
		return nil, ErrSourceEpisodeUnavailable
	}
	registry := NewSourceEpisodeRuntimeRegistry()
	conversation := &conversationSourceEpisodeProvider{app: app}
	drive := &driveSourceEpisodeProvider{app: app}
	realtime := &realtimeVoiceSourceEpisodeProvider{app: app}
	work := &workArtifactSourceEpisodeProvider{app: app}
	for _, registration := range []struct {
		family    string
		authority SourceEpisodeBrainAuthority
		body      SourceEpisodeNativeBodyReader
	}{
		{SourceEpisodeFamilyConversationEvent, conversation, conversation},
		{SourceEpisodeFamilyDriveFileRevision, drive, drive},
		{SourceEpisodeFamilyRealtimeVoiceSession, realtime, realtime},
		{SourceEpisodeFamilyWorkArtifactRevision, work, work},
	} {
		if err := registry.RegisterAuthority(registration.family, registration.authority); err != nil {
			return nil, err
		}
		if err := registry.RegisterBodyReader(registration.family, registration.body); err != nil {
			return nil, err
		}
	}
	app.memory.workArtifactMutationHook = app.handleDurableWorkArtifactCommit
	app.reconcileNativeSourceEpisodesAfterRestart()
	return registry, nil
}

func (app *kanbanBoardApp) handleDurableWorkArtifactCommit(entry meetingMemoryEntry) {
	if err := app.publishCommittedWorkArtifactSourceEpisode(entry); err != nil && !errors.Is(err, ErrSourceEpisodeUnavailable) {
		log.Errorf("SourceEpisode Work artifact commit publication unavailable: %v", err)
	}
}

// reconcileNativeSourceEpisodesAfterRestart derives missing shadow writes from
// durable native state once at boot. It never runs on an ordinary commit and
// therefore cannot turn a live media path into a lifetime scan.
func (app *kanbanBoardApp) reconcileNativeSourceEpisodesAfterRestart() {
	if app == nil || app.memory == nil || app.sourceEpisodes == nil {
		return
	}
	for _, entry := range app.memory.entriesOfKind(meetingMemoryKindOSArtifact, 0) {
		if err := app.publishCommittedWorkArtifactSourceEpisode(entry); err != nil && !errors.Is(err, ErrSourceEpisodeConflict) {
			log.Errorf("SourceEpisode Work artifact restart reconciliation unavailable: %v", err)
		}
	}
	for _, entry := range app.memory.entriesOfKind(meetingMemoryKindScoutChat, 0) {
		thread, ok := decodeScoutChatThreadEntry(entry)
		if !ok || thread.ArchivedAt != "" || thread.VoiceSession == nil || thread.VoiceSession.Lease == nil || thread.VoiceSession.Lease.State != "stopped" {
			continue
		}
		if err := app.publishClosedRealtimeVoiceSourceEpisode(thread); err != nil && !errors.Is(err, ErrSourceEpisodeConflict) && !errors.Is(err, ErrSourceEpisodeBodyMissing) {
			log.Errorf("SourceEpisode Realtime voice restart reconciliation unavailable: %v", err)
		}
	}
}

func (app *kanbanBoardApp) publishCommittedConversationSourceEpisode(thread scoutChatThreadRecord, message scoutChatMessageRecord, eventType string) error {
	if app == nil || app.sourceEpisodes == nil || app.sourceEpisodesErr != nil || !oneOf(eventType, "message", "edit", "delete", "reaction") {
		return ErrSourceEpisodeUnavailable
	}
	episodeID := conversationSourceEpisodeID(thread.ID, message.ID)
	boundaryAt := sourceEpisodeBoundaryTime(thread.UpdatedAt, time.Now().UTC())
	if eventType == "delete" {
		return app.tombstoneNativeSourceEpisode(threadTenantID(thread), episodeID, SourceEpisodeTombstoneRetraction, boundaryAt)
	}
	if thread.ArchivedAt != "" {
		return app.tombstoneNativeSourceEpisode(threadTenantID(thread), episodeID, SourceEpisodeTombstoneACL, boundaryAt)
	}
	body := message.Text
	if strings.TrimSpace(body) == "" {
		return app.tombstoneNativeSourceEpisode(threadTenantID(thread), episodeID, SourceEpisodeTombstoneCorrection, boundaryAt)
	}
	authority, scope, kind, err := conversationSourceEpisodeAuthority(thread, boundaryAt)
	if err != nil {
		return err
	}
	if err := app.bindCurrentSourceEpisodePurge(threadTenantID(thread), &authority); err != nil {
		return err
	}
	start := sourceEpisodeBoundaryTime(message.CreatedAt, boundaryAt.Add(-time.Nanosecond))
	end := sourceEpisodeBoundaryTime(firstNonEmptyString(message.EditedAt, message.CreatedAt), start).Add(time.Nanosecond)
	if !end.After(start) {
		end = start.Add(time.Nanosecond)
	}
	if boundaryAt.Before(end) {
		boundaryAt = end
		authority.ObservedAt = boundaryAt
	}
	objectID, err := conversationSourceEpisodeObjectID(thread.ID, message.ID)
	if err != nil {
		return err
	}
	return app.publishNativeSourceEpisode(nativeSourceEpisodePublication{
		TenantID: threadTenantID(thread), EpisodeID: episodeID, Kind: kind, ObjectID: objectID, Body: body,
		Scope: scope, Authority: authority, OccurredStart: start, OccurredEnd: end,
		BoundaryType: SourceEpisodeBoundaryConversationCommit, BoundaryAt: boundaryAt,
	})
}

// reconcileConversationSourceEpisodeAuthority runs only on the exceptional
// archive/restore authority transition. Ordinary commits remain O(delta).
func (app *kanbanBoardApp) reconcileConversationSourceEpisodeAuthority(thread scoutChatThreadRecord) {
	if app == nil || app.sourceEpisodes == nil || app.sourceEpisodesErr != nil {
		return
	}
	if thread.VoiceSession != nil && thread.VoiceSession.Lease != nil && thread.VoiceSession.Lease.State == "stopped" {
		voiceID := realtimeVoiceSourceEpisodeID(thread.ID, thread.VoiceSession.SessionDigest, thread.VoiceSession.Lease.Generation)
		if thread.ArchivedAt != "" {
			if err := app.tombstoneNativeSourceEpisode(threadTenantID(thread), voiceID, SourceEpisodeTombstoneACL, sourceEpisodeBoundaryTime(thread.UpdatedAt, time.Now().UTC())); err != nil && !errors.Is(err, ErrSourceEpisodeConflict) {
				log.Errorf("SourceEpisode Realtime voice authority reconciliation unavailable: %v", err)
			}
		} else if err := app.publishClosedRealtimeVoiceSourceEpisode(thread); err != nil && !errors.Is(err, ErrSourceEpisodeConflict) {
			log.Errorf("SourceEpisode Realtime voice authority reconciliation unavailable: %v", err)
		}
	}
	for _, message := range thread.Messages {
		if strings.TrimSpace(message.Text) == "" {
			continue
		}
		if thread.ArchivedAt != "" {
			if err := app.tombstoneNativeSourceEpisode(threadTenantID(thread), conversationSourceEpisodeID(thread.ID, message.ID), SourceEpisodeTombstoneACL, sourceEpisodeBoundaryTime(thread.UpdatedAt, time.Now().UTC())); err != nil && !errors.Is(err, ErrSourceEpisodeConflict) {
				log.Errorf("SourceEpisode conversation authority reconciliation unavailable: %v", err)
			}
			continue
		}
		if err := app.publishCommittedConversationSourceEpisode(thread, message, "message"); err != nil && !errors.Is(err, ErrSourceEpisodeConflict) {
			log.Errorf("SourceEpisode conversation authority reconciliation unavailable: %v", err)
		}
	}
	// Drive copies retain their source conversation authority. This bounded
	// scan runs only on an explicit archive/restore transition, never on an
	// ordinary message commit.
	if thread.ArchivedAt != "" {
		cursor := ""
		for {
			page, err := app.sourceEpisodes.ListSourceEpisodes(context.Background(), threadTenantID(thread), cursor)
			if err != nil {
				log.Errorf("SourceEpisode Drive authority reconciliation unavailable: %v", err)
				break
			}
			for _, episode := range page.Episodes {
				if episode.Kind == SourceEpisodeDriveFileRevision && episode.Scope.ConversationID == thread.ID {
					_ = app.tombstoneNativeSourceEpisode(episode.Header.TenantID, episode.Header.ID, SourceEpisodeTombstoneACL, sourceEpisodeBoundaryTime(thread.UpdatedAt, time.Now().UTC()))
				}
			}
			if page.NextCursor == "" {
				break
			}
			cursor = page.NextCursor
		}
		return
	}
	if app.memory != nil {
		for _, entry := range app.memory.entriesOfKind(meetingMemoryKindFile, 0) {
			if strings.TrimSpace(entry.Metadata["sourceThreadId"]) != thread.ID {
				continue
			}
			if err := app.publishDriveFileSourceEpisode(entry); err != nil && !errors.Is(err, ErrSourceEpisodeConflict) {
				log.Errorf("SourceEpisode Drive restore reconciliation unavailable: %v", err)
			}
		}
	}
}

func (app *kanbanBoardApp) publishDriveFileSourceEpisode(entry meetingMemoryEntry) error {
	if app == nil || app.sourceEpisodes == nil || app.sourceEpisodesErr != nil || entry.Kind != meetingMemoryKindFile || strings.TrimSpace(entry.Text) == "" {
		return ErrSourceEpisodeUnavailable
	}
	tenantID := driveSourceEpisodeTenant(entry)
	boundaryAt := time.Now().UTC()
	start := entry.CreatedAt.UTC()
	if start.IsZero() {
		start = boundaryAt.Add(-time.Nanosecond)
	}
	end := start.Add(time.Nanosecond)
	if boundaryAt.Before(end) {
		boundaryAt = end
	}
	authority, scope, err := app.driveSourceEpisodeAuthority(entry, boundaryAt)
	if err != nil {
		return err
	}
	return app.publishNativeSourceEpisode(nativeSourceEpisodePublication{
		TenantID: tenantID, EpisodeID: driveSourceEpisodeID(entry.ID), Kind: SourceEpisodeDriveFileRevision,
		ObjectID: entry.ID, Body: entry.Text,
		Scope: scope, Authority: authority,
		OccurredStart: start, OccurredEnd: end, BoundaryType: SourceEpisodeBoundaryDriveCommit, BoundaryAt: boundaryAt,
	})
}

func (app *kanbanBoardApp) tombstoneDriveFileSourceEpisode(entry meetingMemoryEntry, occurredAt time.Time) error {
	if entry.Kind != meetingMemoryKindFile {
		return nil
	}
	return app.tombstoneNativeSourceEpisode(driveSourceEpisodeTenant(entry), driveSourceEpisodeID(entry.ID), SourceEpisodeTombstoneRetraction, occurredAt.UTC())
}

type nativeSourceEpisodePublication struct {
	TenantID, EpisodeID, ObjectID, Body string
	Kind                                SourceEpisodeKind
	Scope                               SourceEpisodeScope
	Authority                           SourceEpisodeAuthoritySnapshot
	OccurredStart, OccurredEnd          time.Time
	BoundaryType                        string
	BoundaryAt                          time.Time
	ContentRevision                     int64
}

func (app *kanbanBoardApp) publishNativeSourceEpisode(publication nativeSourceEpisodePublication) error {
	ledger := app.sourceEpisodes
	latest, found, err := ledger.LatestSourceEpisode(context.Background(), publication.TenantID, publication.EpisodeID)
	if err != nil {
		return err
	}
	active, activeFound, err := ledger.CurrentSourceEpisode(context.Background(), publication.TenantID, publication.EpisodeID)
	if err != nil {
		return err
	}
	bodyRef := SourceEpisodeRevisionRef{
		SourceFamily: sourceEpisodeFamilyForKind(publication.Kind), ObjectID: publication.ObjectID,
		ContentRevision: 1, ContentDigest: digestBrainString(publication.Body), SizeBytes: int64(len(publication.Body)),
	}
	if publication.ContentRevision > 0 {
		bodyRef.ContentRevision = publication.ContentRevision
	} else if found {
		bodyRef.ContentRevision = latest.Source.ContentRevision + 1
	}
	if activeFound && active.Source.ContentDigest == bodyRef.ContentDigest && active.Source.SizeBytes == bodyRef.SizeBytes &&
		reflect.DeepEqual(active.Scope, publication.Scope) && sourceEpisodeAuthorityEquivalent(active.Authority, publication.Authority) {
		return nil
	}
	if activeFound {
		if err := app.tombstoneNativeSourceEpisode(publication.TenantID, publication.EpisodeID, SourceEpisodeTombstoneCorrection, publication.BoundaryAt); err != nil {
			return err
		}
	}
	revision := int64(1)
	var supersedes *STRIDEReference
	if found {
		revision = latest.Header.Revision + 1
		prior := referenceFromHeader(latest.Header)
		supersedes = &prior
	}
	receiptDigest, err := STRIDEContractDigest(struct {
		TenantID, EpisodeID, BoundaryType string
		Source                            SourceEpisodeRevisionRef
		BoundaryAt                        time.Time
	}{publication.TenantID, publication.EpisodeID, publication.BoundaryType, bodyRef, publication.BoundaryAt})
	if err != nil {
		return err
	}
	header := STRIDEContractHeader{
		TenantID: publication.TenantID, ID: publication.EpisodeID, Revision: revision,
		SchemaVersion: STRIDEContractSchemaVersion, ContractType: STRIDEContractSourceEpisode, CreatedAt: publication.BoundaryAt,
	}
	phase := SourceEpisodePhasePostCommit
	if publication.Kind == SourceEpisodeRealtimeVoiceSession {
		phase = SourceEpisodePhasePostClose
	}
	input := SourceEpisodeAdapterInput{
		Header: header, Source: bodyRef, RetrievalBody: bodyRef, Scope: publication.Scope, Authority: publication.Authority,
		OccurredStart: publication.OccurredStart, OccurredEnd: publication.OccurredEnd,
		PhaseProof: SourceEpisodePhaseProof{Phase: phase, BoundaryType: publication.BoundaryType, BoundaryAt: publication.BoundaryAt, ReceiptDigest: receiptDigest},
		Supersedes: supersedes,
	}
	var episode SourceEpisode
	switch publication.Kind {
	case SourceEpisodePublicChannelSegment:
		episode, err = AdaptPublicChannelSourceEpisode(input)
	case SourceEpisodePrivateConversationSegment:
		episode, err = AdaptPrivateConversationSourceEpisode(input)
	case SourceEpisodeDriveFileRevision:
		episode, err = AdaptDriveFileSourceEpisode(input)
	case SourceEpisodeRealtimeVoiceSession:
		episode, err = AdaptRealtimeVoiceSourceEpisode(input)
	case SourceEpisodeWorkArtifactRevision:
		episode, err = AdaptWorkArtifactSourceEpisode(input)
	default:
		return ErrSourceEpisodeInvalid
	}
	if err != nil {
		return err
	}
	_, err = ledger.DualWriteSourceEpisode(context.Background(), episode, supersedes)
	return err
}

func (app *kanbanBoardApp) tombstoneNativeSourceEpisode(tenantID, episodeID, cause string, occurredAt time.Time) error {
	if app == nil || app.sourceEpisodes == nil {
		return ErrSourceEpisodeUnavailable
	}
	current, found, err := app.sourceEpisodes.CurrentSourceEpisode(context.Background(), tenantID, episodeID)
	if err != nil || !found {
		return err
	}
	ref := referenceFromHeader(current.Header)
	tombstone := SourceEpisodeTombstone{
		TenantID: tenantID, Episode: ref, Cause: cause, PurgeGeneration: current.Authority.PurgeGeneration,
		ReasonDigest: sha256Hex([]byte(strings.Join([]string{tenantID, episodeID, cause, ref.Digest}, "\x00"))), OccurredAt: occurredAt.UTC(),
	}
	tombstone.IdempotencyKeyDigest = SourceEpisodeTombstoneIdempotencyKey(tenantID, ref, cause, tombstone.PurgeGeneration)
	_, err = app.sourceEpisodes.TombstoneSourceEpisode(context.Background(), tombstone)
	return err
}

func (app *kanbanBoardApp) bindCurrentSourceEpisodePurge(tenantID string, authority *SourceEpisodeAuthoritySnapshot) error {
	if app == nil || app.sourceEpisodes == nil || authority == nil {
		return ErrSourceEpisodeUnavailable
	}
	generation, err := app.sourceEpisodes.CurrentPurgeGeneration(context.Background(), tenantID)
	if err != nil {
		return err
	}
	authority.PurgeGeneration = generation
	return nil
}

func sourceEpisodeFamilyForKind(kind SourceEpisodeKind) string {
	switch kind {
	case SourceEpisodePublicChannelSegment, SourceEpisodePrivateConversationSegment:
		return SourceEpisodeFamilyConversationEvent
	case SourceEpisodeDriveFileRevision:
		return SourceEpisodeFamilyDriveFileRevision
	case SourceEpisodeRealtimeVoiceSession:
		return SourceEpisodeFamilyRealtimeVoiceSession
	case SourceEpisodeWorkArtifactRevision:
		return SourceEpisodeFamilyWorkArtifactRevision
	default:
		return ""
	}
}

func conversationSourceEpisodeAuthority(thread scoutChatThreadRecord, observedAt time.Time) (SourceEpisodeAuthoritySnapshot, SourceEpisodeScope, SourceEpisodeKind, error) {
	tenantID := threadTenantID(thread)
	owner := strideRuntimePrincipalForEmail(thread.OwnerEmail)
	if owner == "" || thread.ArchivedAt != "" {
		return SourceEpisodeAuthoritySnapshot{}, SourceEpisodeScope{}, "", ErrSourceEpisodeAuthorityDenied
	}
	kind := SourceEpisodePrivateConversationSegment
	audience := STRIDEAudience{Visibility: "private", Principals: []string{owner}}
	scope := SourceEpisodeScope{CompanyID: tenantID, ConversationID: thread.ID, PersonIDs: []string{owner}, MemoryScope: SourceEpisodeMemoryConversation}
	retention := "private_default"
	if scoutChatThreadVisibility(thread) == scoutChatVisibilityPublic {
		kind = SourceEpisodePublicChannelSegment
		retention = "company_default"
		members := scoutChatThreadMemberEmails(thread)
		if len(members) == 0 {
			audience = STRIDEAudience{Visibility: "organization", Principals: []string{"organization"}}
			scope = SourceEpisodeScope{CompanyID: tenantID, ConversationID: thread.ID, MemoryScope: SourceEpisodeMemoryCompany}
		} else {
			principals := make([]string, 0, len(members))
			for _, member := range members {
				principals = append(principals, strideRuntimePrincipalForEmail(member))
			}
			audience = STRIDEAudience{Visibility: "channel", Principals: sortedUniqueSTRIDEIDs(principals)}
			scope = SourceEpisodeScope{CompanyID: tenantID, ConversationID: thread.ID, MemoryScope: SourceEpisodeMemoryConversation}
		}
	}
	aclDigest, err := STRIDEContractDigest(struct {
		TenantID, ThreadID, Visibility, ArchivedAt string
		Audience                                   STRIDEAudience
	}{tenantID, thread.ID, scoutChatThreadVisibility(thread), thread.ArchivedAt, audience})
	if err != nil {
		return SourceEpisodeAuthoritySnapshot{}, SourceEpisodeScope{}, "", err
	}
	consentDigest := sha256Hex([]byte("source-episode-conversation-consent/v1\x00" + string(kind)))
	return SourceEpisodeAuthoritySnapshot{
		Audience: audience, ACLRevision: sourceEpisodeRevisionFromDigest(aclDigest), ACLDigest: aclDigest,
		ConsentRevision: 1, ConsentDigest: consentDigest, PurgeGeneration: 0, RetentionPolicy: retention, ObservedAt: observedAt.UTC(),
	}, scope, kind, nil
}

func (app *kanbanBoardApp) driveSourceEpisodeAuthority(entry meetingMemoryEntry, observedAt time.Time) (SourceEpisodeAuthoritySnapshot, SourceEpisodeScope, error) {
	tenantID := driveSourceEpisodeTenant(entry)
	audience := STRIDEAudience{Visibility: "organization", Principals: []string{"organization"}}
	scope := SourceEpisodeScope{CompanyID: tenantID, MemoryScope: SourceEpisodeMemoryCompany}
	retention := "company_default"
	sourceAuthorityDigest := ""
	if threadID := strings.TrimSpace(entry.Metadata["sourceThreadId"]); threadID != "" {
		if app == nil || app.memory == nil {
			return SourceEpisodeAuthoritySnapshot{}, SourceEpisodeScope{}, ErrSourceEpisodeAuthorityDenied
		}
		threadEntry, found := app.memory.entryByKindAndID(meetingMemoryKindScoutChat, threadID)
		if !found {
			return SourceEpisodeAuthoritySnapshot{}, SourceEpisodeScope{}, ErrSourceEpisodeAuthorityDenied
		}
		thread, decoded := decodeScoutChatThreadEntry(threadEntry)
		if !decoded || threadTenantID(thread) != tenantID {
			return SourceEpisodeAuthoritySnapshot{}, SourceEpisodeScope{}, ErrSourceEpisodeAuthorityDenied
		}
		sourceAuthority, sourceScope, _, err := conversationSourceEpisodeAuthority(thread, observedAt)
		if err != nil {
			return SourceEpisodeAuthoritySnapshot{}, SourceEpisodeScope{}, err
		}
		audience, scope, retention = sourceAuthority.Audience, sourceScope, sourceAuthority.RetentionPolicy
		sourceAuthorityDigest = sourceAuthority.ACLDigest
	}
	aclDigest, err := STRIDEContractDigest(struct {
		TenantID, FileID, SourceThreadID, SourceAuthorityDigest string
		Audience                                                STRIDEAudience
	}{tenantID, entry.ID, strings.TrimSpace(entry.Metadata["sourceThreadId"]), sourceAuthorityDigest, audience})
	if err != nil {
		return SourceEpisodeAuthoritySnapshot{}, SourceEpisodeScope{}, err
	}
	authority := SourceEpisodeAuthoritySnapshot{
		Audience:    audience,
		ACLRevision: sourceEpisodeRevisionFromDigest(aclDigest), ACLDigest: aclDigest,
		ConsentRevision: 1, ConsentDigest: sha256Hex([]byte("source-episode-drive-consent/v1")),
		PurgeGeneration: 0, RetentionPolicy: retention, ObservedAt: observedAt.UTC(),
	}
	if err := app.bindCurrentSourceEpisodePurge(tenantID, &authority); err != nil {
		return SourceEpisodeAuthoritySnapshot{}, SourceEpisodeScope{}, err
	}
	return authority, scope, nil
}

func sourceEpisodeAuthorityEquivalent(left, right SourceEpisodeAuthoritySnapshot) bool {
	left.ObservedAt, right.ObservedAt = time.Time{}, time.Time{}
	return reflect.DeepEqual(left, right)
}

func sourceEpisodeRevisionFromDigest(digest string) int64 {
	value, err := strconv.ParseInt(digest[:13], 16, 64)
	if err != nil {
		return 1
	}
	return value + 1
}

func sourceEpisodeBoundaryTime(raw string, fallback time.Time) time.Time {
	parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(raw))
	if err != nil || parsed.IsZero() {
		return fallback.UTC()
	}
	return parsed.UTC()
}

func threadTenantID(thread scoutChatThreadRecord) string {
	if tenantID := strings.TrimSpace(scoutChatThreadMetadata(thread)["tenantId"]); strideIdentifier(tenantID) {
		return tenantID
	}
	return canonicalTenantID()
}

func driveSourceEpisodeTenant(entry meetingMemoryEntry) string {
	if tenantID := strings.TrimSpace(entry.Metadata["tenantId"]); strideIdentifier(tenantID) {
		return tenantID
	}
	return canonicalTenantID()
}

func conversationSourceEpisodeID(threadID, messageID string) string {
	return "source_episode_conversation_" + sha256Hex([]byte(threadID + "\x00" + messageID))[:24]
}

func driveSourceEpisodeID(fileID string) string {
	return "source_episode_drive_" + sha256Hex([]byte(fileID))[:24]
}

func conversationSourceEpisodeObjectID(threadID, messageID string) (string, error) {
	encoded := "conversation." + base64.RawURLEncoding.EncodeToString([]byte(threadID)) + "." + base64.RawURLEncoding.EncodeToString([]byte(messageID))
	if !strideIdentifier(encoded) {
		return "", ErrSourceEpisodeInvalid
	}
	return encoded, nil
}

func parseConversationSourceEpisodeObjectID(value string) (string, string, error) {
	parts := strings.Split(value, ".")
	if len(parts) != 3 || parts[0] != "conversation" {
		return "", "", ErrSourceEpisodeInvalid
	}
	threadID, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", "", ErrSourceEpisodeInvalid
	}
	messageID, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(threadID) == 0 || len(messageID) == 0 {
		return "", "", ErrSourceEpisodeInvalid
	}
	return string(threadID), string(messageID), nil
}

type conversationSourceEpisodeProvider struct{ app *kanbanBoardApp }

func (provider *conversationSourceEpisodeProvider) AuthorizeSourceEpisodeMetadata(ctx context.Context, principal ACLPrincipal, episode SourceEpisode) (bool, error) {
	allowed := false
	err := provider.WithCurrentSourceEpisodeAuthority(ctx, episode, func() error {
		allowed = sourceEpisodeAudienceAllowsPrincipal(episode.Authority.Audience, principal)
		return nil
	})
	if err != nil {
		return false, err
	}
	if !allowed {
		return false, ErrSourceEpisodeAuthorityDenied
	}
	return true, nil
}

func (provider *conversationSourceEpisodeProvider) WithCurrentSourceEpisodeAuthority(_ context.Context, episode SourceEpisode, use func() error) error {
	if provider == nil || provider.app == nil || use == nil || episode.Source.SourceFamily != SourceEpisodeFamilyConversationEvent {
		return ErrSourceEpisodeAuthorityStale
	}
	threadID, messageID, err := parseConversationSourceEpisodeObjectID(episode.Source.ObjectID)
	if err != nil {
		return ErrSourceEpisodeAuthorityStale
	}
	lock := provider.app.scoutChatThreadLock(threadID)
	lock.Lock()
	defer lock.Unlock()
	thread, message, found := provider.current(threadID, messageID)
	if !found || thread.ArchivedAt != "" || digestBrainString(message.Text) != episode.Source.ContentDigest || int64(len(message.Text)) != episode.Source.SizeBytes {
		return ErrSourceEpisodeAuthorityStale
	}
	authority, scope, kind, err := conversationSourceEpisodeAuthority(thread, episode.Authority.ObservedAt)
	if err == nil {
		err = provider.app.bindCurrentSourceEpisodePurge(threadTenantID(thread), &authority)
	}
	if err != nil || kind != episode.Kind || !reflect.DeepEqual(scope, episode.Scope) || !sourceEpisodeAuthorityEquivalent(authority, episode.Authority) {
		return ErrSourceEpisodeAuthorityStale
	}
	current, active, err := provider.app.sourceEpisodes.CurrentSourceEpisode(context.Background(), episode.Header.TenantID, episode.Header.ID)
	if err != nil || !active || referenceFromHeader(current.Header) != referenceFromHeader(episode.Header) {
		return ErrSourceEpisodeAuthorityStale
	}
	return use()
}

func (provider *conversationSourceEpisodeProvider) ReadExactSourceEpisodeBody(_ context.Context, ref SourceEpisodeRevisionRef) (SourceEpisodeNativeBody, error) {
	if provider == nil || provider.app == nil || ref.SourceFamily != SourceEpisodeFamilyConversationEvent {
		return SourceEpisodeNativeBody{}, ErrSourceEpisodeBodyMissing
	}
	threadID, messageID, err := parseConversationSourceEpisodeObjectID(ref.ObjectID)
	if err != nil {
		return SourceEpisodeNativeBody{}, ErrSourceEpisodeBodyMissing
	}
	_, message, found := provider.current(threadID, messageID)
	if !found || digestBrainString(message.Text) != ref.ContentDigest || int64(len(message.Text)) != ref.SizeBytes {
		return SourceEpisodeNativeBody{}, ErrSourceEpisodeBodyMissing
	}
	return SourceEpisodeNativeBody{Revision: ref, Body: message.Text}, nil
}

func (provider *conversationSourceEpisodeProvider) current(threadID, messageID string) (scoutChatThreadRecord, scoutChatMessageRecord, bool) {
	if provider.app.memory == nil {
		return scoutChatThreadRecord{}, scoutChatMessageRecord{}, false
	}
	entry, found := provider.app.memory.entryByKindAndID(meetingMemoryKindScoutChat, threadID)
	if !found {
		return scoutChatThreadRecord{}, scoutChatMessageRecord{}, false
	}
	thread, ok := decodeScoutChatThreadEntry(entry)
	if !ok {
		return scoutChatThreadRecord{}, scoutChatMessageRecord{}, false
	}
	index := scoutChatMessageIndex(thread, messageID)
	if index < 0 {
		return scoutChatThreadRecord{}, scoutChatMessageRecord{}, false
	}
	return thread, thread.Messages[index], true
}

type driveSourceEpisodeProvider struct{ app *kanbanBoardApp }

func (provider *driveSourceEpisodeProvider) AuthorizeSourceEpisodeMetadata(ctx context.Context, principal ACLPrincipal, episode SourceEpisode) (bool, error) {
	allowed := false
	err := provider.WithCurrentSourceEpisodeAuthority(ctx, episode, func() error {
		allowed = sourceEpisodeAudienceAllowsPrincipal(episode.Authority.Audience, principal)
		return nil
	})
	if err != nil {
		return false, err
	}
	if !allowed {
		return false, ErrSourceEpisodeAuthorityDenied
	}
	return true, nil
}

func (provider *driveSourceEpisodeProvider) WithCurrentSourceEpisodeAuthority(_ context.Context, episode SourceEpisode, use func() error) error {
	if provider == nil || provider.app == nil || use == nil || episode.Source.SourceFamily != SourceEpisodeFamilyDriveFileRevision {
		return ErrSourceEpisodeAuthorityStale
	}
	lock := provider.app.scoutChatThreadLock("source-episode-drive-" + episode.Source.ObjectID)
	lock.Lock()
	defer lock.Unlock()
	entry, found := provider.app.memory.entryByKindAndID(meetingMemoryKindFile, episode.Source.ObjectID)
	if !found || driveSourceEpisodeTenant(entry) != episode.Header.TenantID || digestBrainString(entry.Text) != episode.Source.ContentDigest || int64(len(entry.Text)) != episode.Source.SizeBytes {
		return ErrSourceEpisodeAuthorityStale
	}
	if sourceThreadID := strings.TrimSpace(entry.Metadata["sourceThreadId"]); sourceThreadID != "" {
		sourceLock := provider.app.scoutChatThreadLock(sourceThreadID)
		sourceLock.Lock()
		defer sourceLock.Unlock()
	}
	authority, scope, err := provider.app.driveSourceEpisodeAuthority(entry, episode.Authority.ObservedAt)
	if err != nil || !reflect.DeepEqual(scope, episode.Scope) || !sourceEpisodeAuthorityEquivalent(authority, episode.Authority) {
		return ErrSourceEpisodeAuthorityStale
	}
	current, active, err := provider.app.sourceEpisodes.CurrentSourceEpisode(context.Background(), episode.Header.TenantID, episode.Header.ID)
	if err != nil || !active || referenceFromHeader(current.Header) != referenceFromHeader(episode.Header) {
		return ErrSourceEpisodeAuthorityStale
	}
	return use()
}

func (provider *driveSourceEpisodeProvider) ReadExactSourceEpisodeBody(_ context.Context, ref SourceEpisodeRevisionRef) (SourceEpisodeNativeBody, error) {
	if provider == nil || provider.app == nil || ref.SourceFamily != SourceEpisodeFamilyDriveFileRevision {
		return SourceEpisodeNativeBody{}, ErrSourceEpisodeBodyMissing
	}
	entry, found := provider.app.memory.entryByKindAndID(meetingMemoryKindFile, ref.ObjectID)
	if !found || digestBrainString(entry.Text) != ref.ContentDigest || int64(len(entry.Text)) != ref.SizeBytes {
		return SourceEpisodeNativeBody{}, ErrSourceEpisodeBodyMissing
	}
	return SourceEpisodeNativeBody{Revision: ref, Body: entry.Text}, nil
}

// publishClosedRealtimeVoiceSourceEpisode is called only after the stopped
// lease and its exact transcript turns are durable in the private thread. It
// is intentionally absent from offer, RTP, renewal, and live turn paths.
func (app *kanbanBoardApp) publishClosedRealtimeVoiceSourceEpisode(thread scoutChatThreadRecord) error {
	if app == nil || app.sourceEpisodes == nil || app.sourceEpisodesErr != nil || thread.VoiceSession == nil || thread.VoiceSession.Lease == nil {
		return ErrSourceEpisodeUnavailable
	}
	lease := thread.VoiceSession.Lease
	if lease.State != "stopped" || lease.Generation < 1 || strings.TrimSpace(lease.TerminalAt) == "" {
		return ErrSourceEpisodePhase
	}
	body, occurredStart, occurredEnd, err := realtimeVoiceSourceEpisodeBody(thread)
	if err != nil {
		return err
	}
	authority, scope, err := app.realtimeVoiceSourceEpisodeAuthority(thread, occurredEnd)
	if err != nil {
		return err
	}
	objectID, err := realtimeVoiceSourceEpisodeObjectID(thread.ID, thread.VoiceSession.SessionDigest, lease.Generation)
	if err != nil {
		return err
	}
	return app.publishNativeSourceEpisode(nativeSourceEpisodePublication{
		TenantID: threadTenantID(thread), EpisodeID: realtimeVoiceSourceEpisodeID(thread.ID, thread.VoiceSession.SessionDigest, lease.Generation),
		Kind: SourceEpisodeRealtimeVoiceSession, ObjectID: objectID, Body: body, Scope: scope, Authority: authority,
		OccurredStart: occurredStart, OccurredEnd: occurredEnd, BoundaryType: SourceEpisodeBoundaryRealtimeVoiceClose,
		BoundaryAt: occurredEnd, ContentRevision: int64(lease.Generation),
	})
}

func realtimeVoiceSourceEpisodeBody(thread scoutChatThreadRecord) (string, time.Time, time.Time, error) {
	if thread.VoiceSession == nil || thread.VoiceSession.Lease == nil {
		return "", time.Time{}, time.Time{}, ErrSourceEpisodePhase
	}
	lease := thread.VoiceSession.Lease
	start := sourceEpisodeBoundaryTime(firstNonEmptyString(lease.AcquiredAt, thread.VoiceSession.BoundAt), time.Time{})
	end := sourceEpisodeBoundaryTime(lease.TerminalAt, time.Time{})
	if start.IsZero() || end.IsZero() || !start.Before(end) {
		return "", time.Time{}, time.Time{}, ErrSourceEpisodePhase
	}
	var lines []string
	for _, message := range thread.Messages {
		at := sourceEpisodeBoundaryTime(message.CreatedAt, time.Time{})
		body := strings.TrimSpace(message.Text)
		if at.IsZero() || at.Before(start) || at.After(end) || body == "" {
			continue
		}
		role := firstNonEmptyString(strings.TrimSpace(message.Role), "participant")
		lines = append(lines, at.Format(time.RFC3339Nano)+"\t"+message.ID+"\t"+role+"\t"+body)
	}
	if len(lines) == 0 {
		return "", time.Time{}, time.Time{}, ErrSourceEpisodeBodyMissing
	}
	return strings.Join(lines, "\n"), start, end, nil
}

func (app *kanbanBoardApp) realtimeVoiceSourceEpisodeAuthority(thread scoutChatThreadRecord, observedAt time.Time) (SourceEpisodeAuthoritySnapshot, SourceEpisodeScope, error) {
	base, scope, kind, err := conversationSourceEpisodeAuthority(thread, observedAt)
	if err != nil || kind != SourceEpisodePrivateConversationSegment || thread.VoiceSession == nil || thread.VoiceSession.Lease == nil || thread.VoiceSession.Lease.State != "stopped" {
		return SourceEpisodeAuthoritySnapshot{}, SourceEpisodeScope{}, ErrSourceEpisodeAuthorityDenied
	}
	lease := thread.VoiceSession.Lease
	aclDigest, err := STRIDEContractDigest(struct {
		ConversationACL, SessionDigest, TerminalAt string
		Generation, TransportRevision              int
	}{base.ACLDigest, thread.VoiceSession.SessionDigest, lease.TerminalAt, lease.Generation, lease.TransportRevision})
	if err != nil {
		return SourceEpisodeAuthoritySnapshot{}, SourceEpisodeScope{}, err
	}
	base.ACLDigest = aclDigest
	base.ACLRevision = sourceEpisodeRevisionFromDigest(aclDigest)
	base.ConsentDigest = sha256Hex([]byte("source-episode-private-realtime-consent/v1\x00" + thread.VoiceSession.SessionDigest))
	base.RetentionPolicy = "private_voice_default"
	base.ObservedAt = observedAt.UTC()
	if err := app.bindCurrentSourceEpisodePurge(threadTenantID(thread), &base); err != nil {
		return SourceEpisodeAuthoritySnapshot{}, SourceEpisodeScope{}, err
	}
	return base, scope, nil
}

func realtimeVoiceSourceEpisodeID(threadID, sessionDigest string, generation int) string {
	return "source_episode_realtime_" + sha256Hex([]byte(threadID + "\x00" + sessionDigest + "\x00" + strconv.Itoa(generation)))[:24]
}

func realtimeVoiceSourceEpisodeObjectID(threadID, sessionDigest string, generation int) (string, error) {
	encoded := "realtime." + base64.RawURLEncoding.EncodeToString([]byte(threadID)) + "." + sessionDigest + "." + strconv.Itoa(generation)
	if !strideIdentifier(encoded) || !isHexDigest(sessionDigest) || generation < 1 {
		return "", ErrSourceEpisodeInvalid
	}
	return encoded, nil
}

func parseRealtimeVoiceSourceEpisodeObjectID(value string) (string, string, int, error) {
	parts := strings.Split(value, ".")
	if len(parts) != 4 || parts[0] != "realtime" || !isHexDigest(parts[2]) {
		return "", "", 0, ErrSourceEpisodeInvalid
	}
	threadID, err := base64.RawURLEncoding.DecodeString(parts[1])
	generation, generationErr := strconv.Atoi(parts[3])
	if err != nil || generationErr != nil || len(threadID) == 0 || generation < 1 {
		return "", "", 0, ErrSourceEpisodeInvalid
	}
	return string(threadID), parts[2], generation, nil
}

type realtimeVoiceSourceEpisodeProvider struct{ app *kanbanBoardApp }

func (provider *realtimeVoiceSourceEpisodeProvider) AuthorizeSourceEpisodeMetadata(ctx context.Context, principal ACLPrincipal, episode SourceEpisode) (bool, error) {
	allowed := false
	err := provider.WithCurrentSourceEpisodeAuthority(ctx, episode, func() error {
		allowed = sourceEpisodeAudienceAllowsPrincipal(episode.Authority.Audience, principal)
		return nil
	})
	if err != nil {
		return false, err
	}
	if !allowed {
		return false, ErrSourceEpisodeAuthorityDenied
	}
	return true, nil
}

func (provider *realtimeVoiceSourceEpisodeProvider) WithCurrentSourceEpisodeAuthority(_ context.Context, episode SourceEpisode, use func() error) error {
	if provider == nil || provider.app == nil || use == nil || episode.Source.SourceFamily != SourceEpisodeFamilyRealtimeVoiceSession {
		return ErrSourceEpisodeAuthorityStale
	}
	threadID, sessionDigest, generation, err := parseRealtimeVoiceSourceEpisodeObjectID(episode.Source.ObjectID)
	if err != nil {
		return ErrSourceEpisodeAuthorityStale
	}
	lock := provider.app.scoutChatThreadLock(threadID)
	lock.Lock()
	defer lock.Unlock()
	thread, found := provider.current(threadID)
	if !found || thread.ArchivedAt != "" || thread.VoiceSession == nil || thread.VoiceSession.Lease == nil ||
		thread.VoiceSession.SessionDigest != sessionDigest || thread.VoiceSession.Lease.Generation != generation || thread.VoiceSession.Lease.State != "stopped" {
		return ErrSourceEpisodeAuthorityStale
	}
	body, _, end, err := realtimeVoiceSourceEpisodeBody(thread)
	if err != nil || digestBrainString(body) != episode.Source.ContentDigest || int64(len(body)) != episode.Source.SizeBytes {
		return ErrSourceEpisodeAuthorityStale
	}
	authority, scope, err := provider.app.realtimeVoiceSourceEpisodeAuthority(thread, end)
	if err != nil || !reflect.DeepEqual(scope, episode.Scope) || !sourceEpisodeAuthorityEquivalent(authority, episode.Authority) {
		return ErrSourceEpisodeAuthorityStale
	}
	current, active, err := provider.app.sourceEpisodes.CurrentSourceEpisode(context.Background(), episode.Header.TenantID, episode.Header.ID)
	if err != nil || !active || referenceFromHeader(current.Header) != referenceFromHeader(episode.Header) {
		return ErrSourceEpisodeAuthorityStale
	}
	return use()
}

func (provider *realtimeVoiceSourceEpisodeProvider) ReadExactSourceEpisodeBody(_ context.Context, ref SourceEpisodeRevisionRef) (SourceEpisodeNativeBody, error) {
	if provider == nil || provider.app == nil || ref.SourceFamily != SourceEpisodeFamilyRealtimeVoiceSession {
		return SourceEpisodeNativeBody{}, ErrSourceEpisodeBodyMissing
	}
	threadID, sessionDigest, generation, err := parseRealtimeVoiceSourceEpisodeObjectID(ref.ObjectID)
	thread, found := provider.current(threadID)
	if err != nil || !found || thread.VoiceSession == nil || thread.VoiceSession.Lease == nil || thread.VoiceSession.SessionDigest != sessionDigest ||
		thread.VoiceSession.Lease.Generation != generation || int64(generation) != ref.ContentRevision {
		return SourceEpisodeNativeBody{}, ErrSourceEpisodeBodyMissing
	}
	body, _, _, err := realtimeVoiceSourceEpisodeBody(thread)
	if err != nil || digestBrainString(body) != ref.ContentDigest || int64(len(body)) != ref.SizeBytes {
		return SourceEpisodeNativeBody{}, ErrSourceEpisodeBodyMissing
	}
	return SourceEpisodeNativeBody{Revision: ref, Body: body}, nil
}

func (provider *realtimeVoiceSourceEpisodeProvider) current(threadID string) (scoutChatThreadRecord, bool) {
	if provider == nil || provider.app == nil || provider.app.memory == nil {
		return scoutChatThreadRecord{}, false
	}
	entry, found := provider.app.memory.entryByKindAndID(meetingMemoryKindScoutChat, threadID)
	if !found {
		return scoutChatThreadRecord{}, false
	}
	thread, ok := decodeScoutChatThreadEntry(entry)
	return thread, ok
}

func (app *kanbanBoardApp) publishCommittedWorkArtifactSourceEpisode(entry meetingMemoryEntry) error {
	if app == nil || app.sourceEpisodes == nil || app.sourceEpisodesErr != nil || entry.Kind != meetingMemoryKindOSArtifact || strings.TrimSpace(entry.Text) == "" {
		return ErrSourceEpisodeUnavailable
	}
	boundaryAt := time.Now().UTC()
	start := entry.CreatedAt.UTC()
	if start.IsZero() || !start.Before(boundaryAt) {
		start = boundaryAt.Add(-time.Nanosecond)
	}
	authority, scope, err := app.workArtifactSourceEpisodeAuthority(entry, boundaryAt)
	if err != nil {
		return err
	}
	return app.publishNativeSourceEpisode(nativeSourceEpisodePublication{
		TenantID: workArtifactSourceEpisodeTenant(entry), EpisodeID: workArtifactSourceEpisodeID(entry.ID), Kind: SourceEpisodeWorkArtifactRevision,
		ObjectID: entry.ID, Body: entry.Text, Scope: scope, Authority: authority, OccurredStart: start, OccurredEnd: boundaryAt,
		BoundaryType: SourceEpisodeBoundaryWorkCommit, BoundaryAt: boundaryAt, ContentRevision: int64(artifactVersion(entry)),
	})
}

func (app *kanbanBoardApp) tombstoneWorkArtifactSourceEpisode(entry meetingMemoryEntry, occurredAt time.Time) error {
	if entry.Kind != meetingMemoryKindOSArtifact {
		return nil
	}
	return app.tombstoneNativeSourceEpisode(workArtifactSourceEpisodeTenant(entry), workArtifactSourceEpisodeID(entry.ID), SourceEpisodeTombstoneRetraction, occurredAt.UTC())
}

func (app *kanbanBoardApp) workArtifactSourceEpisodeAuthority(entry meetingMemoryEntry, observedAt time.Time) (SourceEpisodeAuthoritySnapshot, SourceEpisodeScope, error) {
	if app == nil || entry.Kind != meetingMemoryKindOSArtifact {
		return SourceEpisodeAuthoritySnapshot{}, SourceEpisodeScope{}, ErrSourceEpisodeAuthorityDenied
	}
	header := app.resolveArtifactHeaderOwner(artifactAuthorizationHeaderFromEntry(entry))
	tenantID := firstNonEmptyString(strings.TrimSpace(header.TenantID), canonicalTenantID())
	projectID := firstNonEmptyString(strings.TrimSpace(entry.Metadata["projectId"]), strings.TrimSpace(entry.Metadata["goalParentId"]), strings.TrimSpace(entry.Metadata["goalId"]), strings.TrimSpace(entry.Metadata["processId"]))
	if !strideIdentifier(projectID) {
		projectID = "work_artifact_project_" + sha256Hex([]byte(entry.ID))[:24]
	}
	audience := STRIDEAudience{Visibility: "organization", Principals: []string{"organization"}}
	scope := SourceEpisodeScope{CompanyID: tenantID, ProjectIDs: []string{projectID}, MemoryScope: SourceEpisodeMemoryProject}
	retention := "company_default"
	if threadID := strings.TrimPrefix(strings.TrimSpace(header.OriginSurface), "chat:"); threadID != "" && threadID != strings.TrimSpace(header.OriginSurface) {
		threadEntry, found := app.memory.entryByKindAndID(meetingMemoryKindScoutChat, threadID)
		thread, decoded := decodeScoutChatThreadEntry(threadEntry)
		if !found || !decoded || threadTenantID(thread) != tenantID {
			return SourceEpisodeAuthoritySnapshot{}, SourceEpisodeScope{}, ErrSourceEpisodeAuthorityDenied
		}
		conversationAuthority, conversationScope, _, conversationErr := conversationSourceEpisodeAuthority(thread, observedAt)
		if conversationErr != nil {
			return SourceEpisodeAuthoritySnapshot{}, SourceEpisodeScope{}, conversationErr
		}
		audience, retention = conversationAuthority.Audience, conversationAuthority.RetentionPolicy
		scope.ConversationID = conversationScope.ConversationID
		scope.PersonIDs = append([]string(nil), conversationScope.PersonIDs...)
		scope.MemoryScope = conversationScope.MemoryScope
	} else if legacyArtifactHeaderIsPrivate(header) {
		owner := strideRuntimePrincipalForEmail(header.OwnerEmail)
		if owner == "" {
			return SourceEpisodeAuthoritySnapshot{}, SourceEpisodeScope{}, ErrSourceEpisodeAuthorityDenied
		}
		audience = STRIDEAudience{Visibility: "private", Principals: []string{owner}}
		scope.MemoryScope = SourceEpisodeMemoryPerson
		scope.PersonIDs = []string{owner}
		retention = "private_default"
	}
	aclDigest, err := STRIDEContractDigest(struct {
		TenantID, ObjectID, Visibility, OwnerEmail, OriginSurface string
		ACLVersion, ContentRevision                               int64
		Audience                                                  STRIDEAudience
	}{tenantID, header.ObjectID, header.Visibility, header.OwnerEmail, header.OriginSurface, header.ACLVersion, header.ContentRevision, audience})
	if err != nil {
		return SourceEpisodeAuthoritySnapshot{}, SourceEpisodeScope{}, err
	}
	authority := SourceEpisodeAuthoritySnapshot{
		Audience: audience, ACLRevision: sourceEpisodeRevisionFromDigest(aclDigest), ACLDigest: aclDigest,
		ConsentRevision: 1, ConsentDigest: sha256Hex([]byte("source-episode-work-artifact-consent/v1")), PurgeGeneration: 0,
		RetentionPolicy: retention, ObservedAt: observedAt.UTC(),
	}
	if err := app.bindCurrentSourceEpisodePurge(tenantID, &authority); err != nil {
		return SourceEpisodeAuthoritySnapshot{}, SourceEpisodeScope{}, err
	}
	return authority, scope, nil
}

func workArtifactSourceEpisodeTenant(entry meetingMemoryEntry) string {
	if tenantID := strings.TrimSpace(entry.Metadata["tenantId"]); strideIdentifier(tenantID) {
		return tenantID
	}
	return canonicalTenantID()
}

func workArtifactSourceEpisodeID(artifactID string) string {
	return "source_episode_work_" + sha256Hex([]byte(artifactID))[:24]
}

type workArtifactSourceEpisodeProvider struct{ app *kanbanBoardApp }

func (provider *workArtifactSourceEpisodeProvider) AuthorizeSourceEpisodeMetadata(ctx context.Context, principal ACLPrincipal, episode SourceEpisode) (bool, error) {
	allowed := false
	err := provider.WithCurrentSourceEpisodeAuthority(ctx, episode, func() error {
		allowed = sourceEpisodeAudienceAllowsPrincipal(episode.Authority.Audience, principal)
		return nil
	})
	if err != nil {
		return false, err
	}
	if !allowed {
		return false, ErrSourceEpisodeAuthorityDenied
	}
	return true, nil
}

func (provider *workArtifactSourceEpisodeProvider) WithCurrentSourceEpisodeAuthority(_ context.Context, episode SourceEpisode, use func() error) error {
	if provider == nil || provider.app == nil || use == nil || episode.Source.SourceFamily != SourceEpisodeFamilyWorkArtifactRevision {
		return ErrSourceEpisodeAuthorityStale
	}
	lock := provider.app.scoutChatThreadLock("source-episode-work-" + episode.Source.ObjectID)
	lock.Lock()
	defer lock.Unlock()
	entry, found := provider.app.memory.entryByKindAndID(meetingMemoryKindOSArtifact, episode.Source.ObjectID)
	if !found || workArtifactSourceEpisodeTenant(entry) != episode.Header.TenantID || int64(artifactVersion(entry)) != episode.Source.ContentRevision ||
		digestBrainString(entry.Text) != episode.Source.ContentDigest || int64(len(entry.Text)) != episode.Source.SizeBytes {
		return ErrSourceEpisodeAuthorityStale
	}
	authority, scope, err := provider.app.workArtifactSourceEpisodeAuthority(entry, episode.Authority.ObservedAt)
	if err != nil || !reflect.DeepEqual(scope, episode.Scope) || !sourceEpisodeAuthorityEquivalent(authority, episode.Authority) {
		return ErrSourceEpisodeAuthorityStale
	}
	current, active, err := provider.app.sourceEpisodes.CurrentSourceEpisode(context.Background(), episode.Header.TenantID, episode.Header.ID)
	if err != nil || !active || referenceFromHeader(current.Header) != referenceFromHeader(episode.Header) {
		return ErrSourceEpisodeAuthorityStale
	}
	return use()
}

func (provider *workArtifactSourceEpisodeProvider) ReadExactSourceEpisodeBody(_ context.Context, ref SourceEpisodeRevisionRef) (SourceEpisodeNativeBody, error) {
	if provider == nil || provider.app == nil || ref.SourceFamily != SourceEpisodeFamilyWorkArtifactRevision {
		return SourceEpisodeNativeBody{}, ErrSourceEpisodeBodyMissing
	}
	entry, found := provider.app.memory.entryByKindAndID(meetingMemoryKindOSArtifact, ref.ObjectID)
	if !found || int64(artifactVersion(entry)) != ref.ContentRevision || digestBrainString(entry.Text) != ref.ContentDigest || int64(len(entry.Text)) != ref.SizeBytes {
		return SourceEpisodeNativeBody{}, ErrSourceEpisodeBodyMissing
	}
	return SourceEpisodeNativeBody{Revision: ref, Body: entry.Text}, nil
}

func sourceEpisodeAudienceAllowsPrincipal(audience STRIDEAudience, principal ACLPrincipal) bool {
	if principal.Kind != ACLPrincipalUser && principal.Kind != ACLPrincipalService {
		return false
	}
	if audience.Visibility == "organization" {
		for _, teamID := range principal.TeamIDs {
			if teamID == "organization" {
				return true
			}
		}
		return false
	}
	wanted := strings.TrimSpace(principal.ID)
	if strings.Contains(wanted, "@") {
		wanted = strideRuntimePrincipalForEmail(wanted)
	}
	for _, candidate := range audience.Principals {
		if candidate == wanted {
			return true
		}
	}
	return false
}

func (publication nativeSourceEpisodePublication) String() string {
	return fmt.Sprintf("%s/%s", publication.TenantID, publication.EpisodeID)
}
