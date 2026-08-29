package main

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// replaySTRIDETeamChatProjection is the startup adapter from the real durable
// public-channel source into the shadow conversation ledger. It never reads a
// private Scout thread and never makes a feature visible. Replays are
// idempotent, and one authenticated runtime snapshot follows the full pass.
func (app *kanbanBoardApp) replaySTRIDETeamChatProjection() {
	if app == nil || app.strideRuntime == nil || app.memory == nil || app.strideRuntime.Health().State != STRIDERuntimeStandby {
		return
	}
	entries := app.memory.snapshot(0)
	changed := false
	for _, entry := range entries {
		thread, ok := decodeScoutChatThreadEntry(entry)
		if !ok || scoutChatThreadVisibility(thread) != scoutChatVisibilityPublic {
			continue
		}
		for _, message := range thread.Messages {
			projected, err := app.projectSTRIDETeamChatMessage(thread, message, "message", "")
			if err != nil {
				log.Errorf("STRIDE team-chat startup projection unavailable: %v", err)
				return
			}
			changed = changed || projected
		}
	}
	if changed {
		if err := app.strideRuntime.Save(); err != nil {
			log.Errorf("STRIDE team-chat startup snapshot unavailable: %v", err)
		}
	}
}

func (app *kanbanBoardApp) observeSTRIDETeamChatMessage(thread scoutChatThreadRecord, message scoutChatMessageRecord, eventType, actorEmail string) {
	if app == nil {
		return
	}
	// SourceEpisode publication is post-commit and covers both public channels
	// and owner-only Scout conversations. It stays independent of the optional
	// legacy STRIDE shadow runtime below.
	if err := app.publishCommittedConversationSourceEpisode(thread, message, eventType); err != nil && !errors.Is(err, ErrSourceEpisodeUnavailable) {
		log.Errorf("SourceEpisode conversation publication unavailable: %v", err)
	}
	if scoutChatThreadVisibility(thread) != scoutChatVisibilityPublic {
		return
	}
	// Continuity is the fail-closed conversational projection, not a child of
	// the default-off STRIDE shadow runtime. Keep it current even while that
	// runtime is degraded or unavailable so edits/deletes cannot leave a stale
	// checkpoint eligible for the next model prompt.
	if _, _, continuityErr := app.rebuildConversationContinuity(thread, eventType); continuityErr != nil {
		log.Errorf("ConversationContinuity rebuild unavailable: %v", continuityErr)
	}
	if app.strideRuntime == nil || app.strideRuntime.Health().State != STRIDERuntimeStandby {
		return
	}
	changed, err := app.projectSTRIDETeamChatMessage(thread, message, eventType, actorEmail)
	if err != nil {
		log.Errorf("STRIDE team-chat projection unavailable: %v", err)
		return
	}
	if changed {
		if err := app.strideRuntime.Save(); err != nil {
			log.Errorf("STRIDE team-chat snapshot unavailable: %v", err)
		}
	}
	// Reconcile only after the conversation ledger has accepted the edit/delete.
	// Before this edge the superseded source still projects as live, so the
	// collaboration store truthfully has nothing to retract yet.
	if eventType == "edit" || eventType == "delete" {
		app.reconcileSTRIDERelationshipSourceMutation(actorEmail)
	}
	// Proactive attention is event-driven. The durable conversation projection
	// is the admission edge; the worker re-reads the current public source and
	// reauthorizes it before any classifier call or visible action.
	if eventType == "message" || eventType == "edit" {
		app.nudgeScoutProactiveAttention(thread, message, "")
	}
}

func (app *kanbanBoardApp) projectSTRIDETeamChatMessage(thread scoutChatThreadRecord, message scoutChatMessageRecord, eventType, actorEmail string) (bool, error) {
	return app.projectSTRIDETeamChatMessageWithStructuredRefs(thread, message, eventType, actorEmail, nil)
}

func (app *kanbanBoardApp) projectSTRIDETeamChatMessageWithStructuredRefs(thread scoutChatThreadRecord, message scoutChatMessageRecord, eventType, actorEmail string, extraStructuredRefs []STRIDEReference) (bool, error) {
	if app == nil || app.strideRuntime == nil || scoutChatThreadVisibility(thread) != scoutChatVisibilityPublic ||
		!strideIdentifier(thread.ID) || !strideIdentifier(message.ID) || !oneOf(eventType, "message", "edit", "delete", "reaction") {
		return false, ErrSTRIDEConversationInvalid
	}
	changed := false
	err := app.strideRuntime.WithTenantDomains(canonicalTenantID(), func(domains STRIDERuntimeDomains) error {
		snapshot, err := domains.ConversationLedger.Snapshot()
		if err != nil {
			return err
		}
		var latest *STRIDEConversationEventRecord
		for index := range snapshot.Events {
			record := snapshot.Events[index]
			event := record.Append.Event
			if event.Header.TenantID == canonicalTenantID() && event.SourceType == "channel_message" && event.SourceID == message.ID && event.ThreadID == thread.ID &&
				(latest == nil || latest.Sequence < record.Sequence) {
				copyRecord := record
				latest = &copyRecord
			}
		}
		if eventType != "message" && latest == nil {
			return ErrSTRIDEConversationUnknown
		}
		contentDigest, err := strideChatMessageContentDigest(eventType == "delete", message)
		if err != nil {
			return err
		}
		attachmentRefs, linkRefs, structuredRefs, err := strideChatMessageRichRefs(message)
		if err != nil {
			return err
		}
		for _, ref := range extraStructuredRefs {
			if ref.Validate() != nil {
				return ErrSTRIDEConversationInvalid
			}
			structuredRefs = append(structuredRefs, ref)
		}
		structuredRefs = SortedSTRIDEReferences(structuredRefs)
		reactionActors := strideChatReactionActors(message)
		if eventType == "reaction" && reactionActors == nil {
			// An explicit empty set means the mutation cleared the final
			// reaction. nil is retained only for pre-field snapshot replay.
			reactionActors = []string{}
		}
		if latest != nil && latest.Append.Event.ContentDigest == contentDigest && eventType != "delete" &&
			sameSTRIDEReferenceSlice(latest.Append.Event.AttachmentRefs, attachmentRefs) && sameSTRIDEReferenceSlice(latest.Append.Event.LinkRefs, linkRefs) &&
			sameSTRIDEReferenceSlice(latest.Append.Event.StructuredRefs, structuredRefs) && sameSTRIDEIDSet(latest.Append.Event.ReactionActors, reactionActors) {
			return nil
		}
		revision := int64(1)
		if latest != nil {
			revision = latest.Append.Event.ContentRevision + 1
		}
		occurredAt := time.Now().UTC()
		if eventType != "reaction" {
			occurredAt, err = parseSTRIDEChatTime(firstNonEmptyString(message.EditedAt, message.CreatedAt))
			if err != nil {
				return err
			}
		}
		authorEmail := firstNonEmptyString(actorEmail, message.AuthorEmail, message.PostedOnBehalfOf)
		authorPrincipal := strideRuntimePrincipalForEmail(authorEmail)
		if authorPrincipal == "" {
			authorPrincipal = "service:scout"
		}
		authorName := strings.TrimSpace(message.AuthorName)
		if eventType == "reaction" && actorEmail != "" {
			authorName = participantNameForEmail(actorEmail)
		}
		if authorName == "" {
			authorName = scoutParticipantName
		}
		audience, aclVersion, authorityErr := strideRuntimeChatAudienceAuthority(thread)
		if authorityErr != nil {
			return ErrSTRIDEConversationDenied
		}
		identityDigest := sha256Hex([]byte(fmt.Sprintf("%s\x00%s\x00%s\x00%d\x00%s", canonicalTenantID(), thread.ID, message.ID, revision, contentDigest)))
		conversationEvent := ConversationEvent{
			Header:     STRIDEContractHeader{TenantID: canonicalTenantID(), ID: "chat_event_" + identityDigest[:24], Revision: revision, SchemaVersion: STRIDEContractSchemaVersion, ContractType: STRIDEContractConversationEvent, ContentDigest: contentDigest, CreatedAt: occurredAt},
			SourceType: "channel_message", SourceID: message.ID, ThreadID: thread.ID, AuthorPrincipal: authorPrincipal, AuthorName: authorName,
			OccurredAt: occurredAt, IngestedAt: time.Now().UTC(), EventType: eventType, ContentRevision: revision, ContentDigest: contentDigest,
			Audience: audience, ACLVersion: aclVersion, RetentionPolicy: "company_default", PurgeGeneration: 0,
			StructuredRefs: structuredRefs, AttachmentRefs: attachmentRefs, LinkRefs: linkRefs, ReactionActors: reactionActors,
			BodyRef: "chat_body_" + identityDigest[:24], Provenance: strideRuntimeChatProvenance(message),
		}
		if message.PostedOnBehalfOf != "" {
			conversationEvent.OnBehalfOf = strideRuntimePrincipalForEmail(message.PostedOnBehalfOf)
		}
		if latest != nil && oneOf(eventType, "edit", "delete") {
			conversationEvent.SupersedesEventID = latest.Append.Event.Header.ID
		}
		if message.ReplyTo != nil {
			for index := len(snapshot.Events) - 1; index >= 0; index-- {
				candidate := snapshot.Events[index].Append.Event
				if candidate.Header.TenantID == canonicalTenantID() && candidate.SourceID == message.ReplyTo.MessageID && candidate.ThreadID == thread.ID {
					conversationEvent.ReplyToEventID = candidate.Header.ID
					break
				}
			}
		}
		result, err := domains.ConversationLedger.Append(STRIDEConversationAppend{Event: conversationEvent, IdempotencyKey: "chat_" + identityDigest[:24]})
		if err != nil {
			return err
		}
		changed = !result.Existing
		if changed {
			recipients, _, recipientErr := strideProductConversationRecipients(conversationEvent, conversationEvent.AuthorPrincipal)
			if recipientErr != nil && message.Role == "user" && isSTRIDEInsightsOutcomeRequest(message.Text) {
				return recipientErr
			}
			destination := app.strideProductRecommendDestination(
				thread.ID,
				message.Text,
				conversationEvent.Audience,
				recipients,
				firstNonEmptyString(message.AuthorEmail, actorEmail, message.PostedOnBehalfOf),
				conversationEvent.IngestedAt,
			)
			if _, _, err := domains.Product.suggestFromConversationWithDestination(conversationEvent, thread, message, destination); err != nil {
				return err
			}
		}
		return nil
	})
	return changed, err
}

func (app *kanbanBoardApp) latestSTRIDETeamChatEvent(threadID string, messageID string) (ConversationEvent, bool, error) {
	if app != nil && app.strideRuntime.legacyTeamChatProjectionProvablyAbsent() {
		// A constructor-verified disabled runtime with no snapshot or generation
		// files has never admitted a canonical chat event. The durable, body-free
		// chat moderation receipt is therefore the complete audit record.
		return ConversationEvent{}, false, nil
	}
	if app == nil || app.strideRuntime == nil {
		return ConversationEvent{}, false, ErrSTRIDERuntimeUnavailable
	}
	if app.strideRuntime.Health().State != STRIDERuntimeStandby {
		if latest, found, authenticated := app.strideRuntime.authenticatedLegacyTeamChatEvent(threadID, messageID); authenticated {
			return latest, found, nil
		}
		return ConversationEvent{}, false, ErrSTRIDERuntimeUnavailable
	}
	var latest ConversationEvent
	found := false
	err := app.strideRuntime.WithTenantDomains(canonicalTenantID(), func(domains STRIDERuntimeDomains) error {
		snapshot, snapshotErr := domains.ConversationLedger.Snapshot()
		if snapshotErr != nil {
			return snapshotErr
		}
		var sequence uint64
		for _, record := range snapshot.Events {
			event := record.Append.Event
			if event.Header.TenantID == canonicalTenantID() && event.SourceType == "channel_message" && event.ThreadID == threadID && event.SourceID == messageID && (!found || record.Sequence > sequence) {
				latest, sequence, found = event, record.Sequence, true
			}
		}
		return nil
	})
	return latest, found, err
}

func scoutChatModerationReference(receipt scoutChatModerationReceipt) (STRIDEReference, error) {
	digest, err := STRIDEContractDigest(struct {
		OperationID         string                          `json:"operationId"`
		ThreadID            string                          `json:"threadId"`
		MessageID           string                          `json:"messageId"`
		ActorEmail          string                          `json:"actorEmail"`
		ReasonDigest        string                          `json:"reasonDigest"`
		TargetContentDigest string                          `json:"targetContentDigest"`
		TargetEventID       string                          `json:"targetEventId,omitempty"`
		TargetEventRevision int64                           `json:"targetEventRevision,omitempty"`
		TargetWork          *scoutChatWorkModerationBinding `json:"targetWork,omitempty"`
		ReplacementWork     *scoutChatWorkModerationBinding `json:"replacementWork,omitempty"`
	}{receipt.OperationID, receipt.ThreadID, receipt.MessageID, receipt.ActorEmail, receipt.ReasonDigest, receipt.TargetContentDigest, receipt.TargetEventID, receipt.TargetEventRevision, receipt.TargetWork, receipt.ReplacementWork})
	if err != nil {
		return STRIDEReference{}, err
	}
	ref := STRIDEReference{ContractType: STRIDEContractRichMessagePart, ID: "chat_moderation_" + digest[:24], Revision: 1, Digest: digest}
	if ref.Validate() != nil {
		return STRIDEReference{}, ErrSTRIDEConversationInvalid
	}
	return ref, nil
}

func strideConversationEventHasReference(event ConversationEvent, wanted STRIDEReference) bool {
	wantedKey := strideConversationReferenceKey(wanted)
	for _, ref := range event.StructuredRefs {
		if strideConversationReferenceKey(ref) == wantedKey {
			return true
		}
	}
	return false
}

// retractSTRIDETeamChatModeration durably closes one moderation outbox item.
// A retry after the ledger append but before the snapshot/receipt update sees
// the exact moderation reference and only re-saves; it never creates another
// delete revision. If the source was provably never projected (or was already
// retracted), persisting that current runtime snapshot is sufficient proof of
// canonical absence.
func (app *kanbanBoardApp) retractSTRIDETeamChatModeration(thread scoutChatThreadRecord, receipt scoutChatModerationReceipt) error {
	ref, err := scoutChatModerationReference(receipt)
	if err != nil {
		return err
	}
	if app != nil && app.strideRuntime.legacyTeamChatProjectionProvablyAbsent() {
		// No projection exists to retract when the legacy runtime is explicitly
		// disabled. The exact local moderation receipt remains durable in the
		// chat record and is sufficient proof of the projection-only removal.
		return nil
	}
	latest, found, err := app.latestSTRIDETeamChatEvent(thread.ID, receipt.MessageID)
	if err != nil {
		return err
	}
	standby := app.strideRuntime != nil && app.strideRuntime.Health().State == STRIDERuntimeStandby
	if !found || latest.EventType == "delete" && strideConversationEventHasReference(latest, ref) {
		if !standby {
			return nil
		}
		return app.strideRuntime.Save()
	}
	if !standby {
		return ErrSTRIDERuntimeUnavailable
	}
	if receipt.TargetEventID != "" && (latest.Header.ID != receipt.TargetEventID || latest.ContentRevision != receipt.TargetEventRevision) {
		return ErrSTRIDEConversationConflict
	}
	if _, err := app.projectSTRIDETeamChatMessageWithStructuredRefs(thread, receipt.Target, "delete", receipt.ActorEmail, []STRIDEReference{ref}); err != nil {
		return err
	}
	return app.strideRuntime.Save()
}

func parseSTRIDEChatTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(value))
	if err != nil || parsed.IsZero() {
		return time.Time{}, ErrSTRIDEConversationInvalid
	}
	return parsed.UTC(), nil
}

func strideRuntimeChatProvenance(message scoutChatMessageRecord) string {
	if strings.TrimSpace(message.Via) != "" || strings.TrimSpace(message.PostedOnBehalfOf) != "" {
		return "tool"
	}
	if message.Role == "user" {
		return "client"
	}
	return "server"
}

func strideRuntimePrincipalForEmail(email string) string {
	email = normalizeAccountEmail(email)
	if email == "" {
		return ""
	}
	return "user:" + sha256Hex([]byte(email))[:24]
}

func strideRuntimeOrganizationAudience() STRIDEAudience {
	principals := []string{}
	for _, value := range runtimeMemberPrincipals() {
		email := strings.TrimPrefix(strings.TrimSpace(value), "user:")
		if principal := strideRuntimePrincipalForEmail(email); principal != "" {
			principals = append(principals, principal)
		}
	}
	principals = sortedUniqueSTRIDEIDs(principals)
	return STRIDEAudience{Visibility: "channel", Principals: principals}
}

func strideRuntimeChatAudience(thread scoutChatThreadRecord) STRIDEAudience {
	if scoutChatThreadIsOrganizationPublic(thread) {
		return strideRuntimeOrganizationAudience()
	}
	principals := make([]string, 0, len(thread.MemberEmails))
	for _, email := range scoutChatThreadMemberEmails(thread) {
		if principal := strideRuntimePrincipalForEmail(email); principal != "" {
			principals = append(principals, principal)
		}
	}
	return STRIDEAudience{Visibility: "project", Principals: sortedUniqueSTRIDEIDs(principals)}
}

func strideRuntimeChatAudienceAuthority(thread scoutChatThreadRecord) (STRIDEAudience, int64, error) {
	if scoutChatThreadVisibility(thread) != scoutChatVisibilityPublic || thread.ArchivedAt != "" || !strideIdentifier(thread.ID) {
		return STRIDEAudience{}, 0, ErrSTRIDEConversationDenied
	}
	audience := strideRuntimeChatAudience(thread)
	if audience.Validate() != nil {
		return STRIDEAudience{}, 0, ErrSTRIDEConversationDenied
	}
	if scoutChatThreadIsOrganizationPublic(thread) {
		return audience, 1, nil
	}
	digest := temporalDigest("stride-chat-audience/v1\x00" + thread.ID + "\x00" + workDigest(audience))
	// Thirteen hex digits stay inside RFC 8785's interoperable integer range.
	version, err := strconv.ParseInt(digest[:13], 16, 64)
	if err != nil {
		return STRIDEAudience{}, 0, ErrSTRIDEConversationInvalid
	}
	return audience, version + 1, nil
}

func strideRuntimeChatAuthority(thread scoutChatThreadRecord) (STRIDEAudience, int64, error) {
	audience, _, err := strideRuntimeChatAudienceAuthority(thread)
	if err != nil {
		return STRIDEAudience{}, 0, err
	}
	digest := temporalDigest(strings.Join([]string{
		"stride-chat-authority/v1",
		thread.ID,
		strings.TrimSpace(thread.Title),
		fmt.Sprint(thread.Table),
		strings.TrimSpace(thread.ArchivedAt),
		workDigest(audience),
	}, "\x00"))
	// Thirteen hex digits stay inside RFC 8785's interoperable integer range.
	version, err := strconv.ParseInt(digest[:13], 16, 64)
	if err != nil {
		return STRIDEAudience{}, 0, ErrSTRIDEConversationInvalid
	}
	return audience, version + 1, nil
}

// ApplyTemporalEvidence is the sole reducer entry for an already-authorized,
// typed temporal event. Provider and legacy transcript code cannot mutate a
// brain directly or choose another tenant.
func (runtime *STRIDERuntime) ApplyTemporalEvidence(tenantID string, config TemporalMeetingBrainConfig, event TemporalMeetingEvent) error {
	return runtime.WithTemporalMeetingBrain(tenantID, config, func(brain *TemporalMeetingBrain) error { return brain.Apply(event) })
}

// projectSTRIDEAuthoritativeTranscript is the production adapter from the
// canonical ACL/purge/consent reader already used by exact catch-up. It skips
// rather than guesses when canonical identity, purge generation, time bounds,
// media generation, or an authorized audience is unavailable.
func (app *kanbanBoardApp) projectSTRIDEAuthoritativeTranscript(ctx context.Context, meeting meetingRecord, entry meetingMemoryEntry) error {
	if app == nil || app.strideRuntime == nil || app.strideRuntime.Health().State != STRIDERuntimeStandby {
		return nil
	}
	resolver, ok := app.catchUpRecapResolver.(*productionCatchUpResolver)
	if !ok || resolver.Sources == nil || entry.Kind != meetingMemoryKindTranscript {
		return ErrBrainRetrievalUnavailable
	}
	start, end, _ := brainMemoryEntryTimes(entry)
	if start.IsZero() || end.IsZero() || !start.Before(end) {
		return ErrTemporalBrainInvalid
	}
	query, err := NewBoundedTemporalQuery(TemporalExplicitRange, start, end, "UTC", normalizeRoomID(entry.Metadata["roomId"]), strings.TrimSpace(entry.Metadata["meetingId"]), "stride_runtime_projection")
	if err != nil {
		return err
	}
	// This entry was just durably appended by the caller. Re-resolve that exact
	// current row and its canonical identity once; the general inventory API is
	// intentionally lifetime-wide and previously reparsed the full memory JSONL
	// once per principal for every live transcript.
	committed, found, committedErr := resolver.Sources.authoritativeRecentMemoryEntry(entry.ID)
	if committedErr != nil || !found || committed.Kind != meetingMemoryKindTranscript || memoryEntryHiddenFromRecall(committed) || digestBrainString(committed.Text) != digestBrainString(entry.Text) {
		return ErrBrainRetrievalRetry
	}
	purgeGeneration, err := resolver.Sources.Purge.CurrentPurgeGeneration(ctx, canonicalTenantID())
	if err != nil || purgeGeneration < 0 {
		return ErrBrainRetrievalUnavailable
	}
	object, err := resolver.Sources.Objects.CurrentBrainObject(ctx, canonicalTenantID(), "memory", committed.ID)
	if err != nil {
		return ErrBrainRetrievalUnavailable
	}
	if err := resolver.Sources.Consent.VerifyBrainSourceConsent(ctx, committed); err != nil {
		if errors.Is(err, ErrBrainSourceConsentAbsent) {
			return ErrTemporalContextUnauthorized
		}
		return ErrBrainRetrievalUnavailable
	}
	digest := digestBrainString(committed.Text)
	roomID := normalizeRoomID(committed.Metadata["roomId"])
	sittingID := strings.TrimSpace(committed.Metadata["meetingId"])
	occurredStart, occurredEnd, capturedAt := brainMemoryEntryTimes(committed)
	if object.Deleted || object.Ref.TenantID != canonicalTenantID() || object.Ref.Type != "memory" || object.Ref.ID != committed.ID ||
		object.Ref.ACLVersion < 1 || object.CurrentContentRevision < 1 || object.CurrentContentDigest != digest ||
		roomID != query.RoomID || sittingID != query.SittingID || !occurredStart.Before(query.EndUTC) || !query.StartUTC.Before(occurredEnd) {
		return ErrBrainRetrievalRetry
	}
	sequence, _ := entryCaptureSequence(committed)
	source := BrainSourceMetadata{
		Evidence: BrainEvidenceRef{
			TenantID: canonicalTenantID(), SourceFamily: "memory", ObjectID: committed.ID,
			ContentRevision: object.CurrentContentRevision, ACLVersion: object.Ref.ACLVersion, ContentDigest: digest,
			RoomID: roomID, SittingID: sittingID, OccurredStart: occurredStart, OccurredEnd: occurredEnd,
			PurgeGeneration: purgeGeneration, Trust: BrainEvidenceTrusted,
		},
		CaptureSequence: sequence, CapturedAt: capturedAt,
		Segments: []BrainSourceSegmentMetadata{{OccurredStart: occurredStart, OccurredEnd: occurredEnd, ByteStart: 0, ByteEnd: len(committed.Text)}},
	}
	principals := []string{}
	for _, member := range runtimeMemberPrincipals() {
		email := strings.TrimPrefix(strings.TrimSpace(member), "user:")
		principal := ACLPrincipal{TenantID: canonicalTenantID(), ID: email, Kind: ACLPrincipalUser, TeamIDs: []string{"organization"}, RoomID: query.RoomID, SittingID: query.SittingID}
		metadataDecision := resolver.Sources.Kernel.Authorize(ctx, principal, ACLReadMetadata, object.Ref, ACLRevisionRef{})
		contentDecision := resolver.Sources.Kernel.Authorize(ctx, principal, ACLReadContent, object.Ref, ACLRevisionRef{
			ContentRevision: object.CurrentContentRevision, ContentDigest: object.CurrentContentDigest,
		})
		if metadataDecision.DenialCode == ACLDenialUnavailable || contentDecision.DenialCode == ACLDenialUnavailable {
			return ErrBrainRetrievalUnavailable
		}
		if metadataDecision.Allowed && contentDecision.Allowed {
			principals = append(principals, strideRuntimePrincipalForEmail(email))
		}
	}
	principals = sortedUniqueSTRIDEIDs(principals)
	if len(principals) == 0 {
		return ErrTemporalContextUnauthorized
	}
	// Revalidate the exact current row, consent, canonical revision and purge
	// generation immediately before projection. This preserves the old
	// fail-closed read boundary without reopening 74 MiB of unrelated history.
	current, currentFound, currentErr := resolver.Sources.authoritativeRecentMemoryEntry(committed.ID)
	currentObject, objectErr := resolver.Sources.Objects.CurrentBrainObject(ctx, canonicalTenantID(), "memory", committed.ID)
	currentPurge, purgeErr := resolver.Sources.Purge.CurrentPurgeGeneration(ctx, canonicalTenantID())
	consentErr := resolver.Sources.Consent.VerifyBrainSourceConsent(ctx, current)
	if currentErr != nil || !currentFound || objectErr != nil || purgeErr != nil || consentErr != nil || currentPurge != source.Evidence.PurgeGeneration ||
		currentObject.Deleted || currentObject.Ref.ACLVersion != source.Evidence.ACLVersion ||
		currentObject.CurrentContentRevision != source.Evidence.ContentRevision || currentObject.CurrentContentDigest != source.Evidence.ContentDigest ||
		digestBrainString(current.Text) != source.Evidence.ContentDigest {
		return ErrBrainRetrievalRetry
	}
	reauthorizedPrincipals := make([]string, 0, len(principals))
	publicationPrincipals := make([]ACLPrincipal, 0, len(principals))
	for _, member := range runtimeMemberPrincipals() {
		email := strings.TrimPrefix(strings.TrimSpace(member), "user:")
		stridePrincipal := strideRuntimePrincipalForEmail(email)
		if !containsSTRIDEPrincipal(principals, stridePrincipal) {
			continue
		}
		principal := ACLPrincipal{TenantID: canonicalTenantID(), ID: email, Kind: ACLPrincipalUser, TeamIDs: []string{"organization"}, RoomID: query.RoomID, SittingID: query.SittingID}
		metadataDecision := resolver.Sources.Kernel.Authorize(ctx, principal, ACLReadMetadata, currentObject.Ref, ACLRevisionRef{})
		contentDecision := resolver.Sources.Kernel.Authorize(ctx, principal, ACLReadContent, currentObject.Ref, ACLRevisionRef{
			ContentRevision: currentObject.CurrentContentRevision, ContentDigest: currentObject.CurrentContentDigest,
		})
		if metadataDecision.DenialCode == ACLDenialUnavailable || contentDecision.DenialCode == ACLDenialUnavailable {
			return ErrBrainRetrievalRetry
		}
		if metadataDecision.Allowed && contentDecision.Allowed && metadataDecision.ACLVersion == source.Evidence.ACLVersion && contentDecision.ACLVersion == source.Evidence.ACLVersion {
			reauthorizedPrincipals = append(reauthorizedPrincipals, stridePrincipal)
			publicationPrincipals = append(publicationPrincipals, principal)
		}
	}
	principals = sortedUniqueSTRIDEIDs(reauthorizedPrincipals)
	if len(principals) == 0 {
		return ErrTemporalContextUnauthorized
	}
	readBody := current.Text
	mediaGeneration, err := strconv.ParseUint(strings.TrimSpace(entry.Metadata["mediaGeneration"]), 10, 64)
	if err != nil || mediaGeneration == 0 || source.CaptureSequence == 0 {
		return ErrTemporalBrainInvalid
	}
	sittingStart, err := time.Parse(time.RFC3339Nano, meeting.StartedAt)
	if err != nil {
		return ErrTemporalBrainInvalid
	}
	config := TemporalMeetingBrainConfig{TenantID: canonicalTenantID(), RoomID: source.Evidence.RoomID, SittingID: source.Evidence.SittingID, SittingStart: sittingStart.UTC()}
	if meeting.EndedAt != "" {
		ended, parseErr := time.Parse(time.RFC3339Nano, meeting.EndedAt)
		if parseErr != nil {
			return ErrTemporalBrainInvalid
		}
		config.SittingEnd = ended.UTC()
	}
	idDigest := sha256Hex([]byte(entry.ID))
	segmentID := "segment_" + idDigest[:24]
	conversationID := "transcript_event_" + idDigest[:24]
	revisionID := "transcript_revision_" + idDigest[:24]
	createdAt := entry.CreatedAt.UTC()
	audience := STRIDEAudience{Visibility: "meeting", Principals: principals}
	conversation := ConversationEvent{
		Header:     STRIDEContractHeader{TenantID: canonicalTenantID(), ID: conversationID, Revision: source.Evidence.ContentRevision, SchemaVersion: STRIDEContractSchemaVersion, ContractType: STRIDEContractConversationEvent, ContentDigest: source.Evidence.ContentDigest, CreatedAt: createdAt},
		SourceType: "meeting_transcript", SourceID: entry.ID, RoomID: source.Evidence.RoomID, SittingID: source.Evidence.SittingID,
		AuthorPrincipal: "speaker:" + sha256Hex([]byte(entry.Metadata["speaker"]))[:24], AuthorName: firstNonEmptyString(entry.Metadata["speaker"], "Unknown speaker"),
		OccurredAt: source.Evidence.OccurredStart, IngestedAt: createdAt, EventType: "transcript_turn", ContentRevision: source.Evidence.ContentRevision,
		ContentDigest: source.Evidence.ContentDigest, Audience: audience, ACLVersion: source.Evidence.ACLVersion, RetentionPolicy: "company_default",
		PurgeGeneration: source.Evidence.PurgeGeneration, BodyRef: "memory_" + idDigest[:24], Provenance: "provider",
	}
	conversationRef := referenceFromHeader(conversation.Header)
	segment := TranscriptSegment{
		Header:          STRIDEContractHeader{TenantID: canonicalTenantID(), ID: segmentID, Revision: source.Evidence.ContentRevision, SchemaVersion: STRIDEContractSchemaVersion, ContractType: STRIDEContractTranscriptSegment, ContentDigest: source.Evidence.ContentDigest, CreatedAt: createdAt},
		ConversationRef: conversationRef, RoomID: source.Evidence.RoomID, SittingID: source.Evidence.SittingID, MediaGeneration: mediaGeneration,
		CaptureSequence: source.CaptureSequence, SourceStart: source.Evidence.OccurredStart, SourceEnd: source.Evidence.OccurredEnd,
		ProviderItemID: sha256Hex([]byte(firstNonEmptyString(entry.Metadata["itemId"], entry.ID))), Status: "authoritative_final",
		Speaker: "speaker:" + sha256Hex([]byte(entry.Metadata["speaker"]))[:24], Attribution: "server_attribution", ConsentScopes: []string{"org_memory", "transcription"},
		ModelDigest: sha256Hex([]byte(entry.Metadata["model"])), ConfigDigest: sha256Hex([]byte(entry.Metadata["consentPolicyVersion"])),
		ContextDigest: sha256Hex([]byte(source.Evidence.RoomID + "\x00" + source.Evidence.SittingID)), CreatedAt: createdAt,
	}
	segmentRef := referenceFromHeader(segment.Header)
	revision := TranscriptRevision{
		Header:    STRIDEContractHeader{TenantID: canonicalTenantID(), ID: revisionID, Revision: source.Evidence.ContentRevision, SchemaVersion: STRIDEContractSchemaVersion, ContractType: STRIDEContractTranscriptRevision, ContentDigest: source.Evidence.ContentDigest, CreatedAt: createdAt},
		SegmentID: segmentID, Revision: source.Evidence.ContentRevision, TextDigest: source.Evidence.ContentDigest, Status: "authoritative_final", Evidence: []STRIDEReference{conversationRef, segmentRef},
	}
	event := TemporalMeetingEvent{Sequence: source.CaptureSequence, Kind: TemporalMeetingEventTranscript, Transcript: &TemporalTranscriptRevisionEvent{Conversation: conversation, Segment: segment, Revision: revision, Text: readBody}}
	evidenceID, err := brainRetrievalEvidenceID(source.Evidence)
	if err != nil {
		return err
	}
	publicationSource := RetrievalSnapshotSource{EvidenceID: evidenceID, Evidence: source.Evidence}
	return resolver.CommitSTRIDETranscriptProjection(ctx, current, publicationSource, publicationPrincipals, func(commitAuthority func() error) error {
		return app.strideRuntime.ApplyLiveTemporalEvidence(canonicalTenantID(), config, event, commitAuthority)
	})
}

// AdmitSuggestedWorkCandidate is the product seam for a future authorized
// evidence adapter. Today it proves the real orchestrator remains fenced: a
// caller cannot turn a detected chat/model suggestion into durable work merely
// by reaching this boundary.
func (runtime *STRIDERuntime) AdmitSuggestedWorkCandidate(ctx context.Context, tenantID string, candidate STRIDEWorkIntentCandidate) (STRIDEAdmittedWorkIntent, error) {
	var admitted STRIDEAdmittedWorkIntent
	err := runtime.WithTenantDomains(tenantID, func(domains STRIDERuntimeDomains) error {
		var err error
		admitted, err = domains.WorkOrchestrator.AdmitIntent(ctx, candidate)
		return err
	})
	return admitted, err
}
