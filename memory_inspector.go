package main

// Memory inspector API (Wave 8 D2) — "What Scout knows", ACL-scoped to the
// viewer. GET lists ledger records, narratives, decisions, notes, work results,
// and (only for the viewer themself) their user_profile; POST applies one of
// close | correct | forget:
//
//   - close / correct write a kind=ledger_event (the entity-ledger's own
//     append-only log): a ledger record gets a real close/update op that the
//     fold honors; a decision/narrative/note gets a closed audit record of
//     entity "correction" plus inspectorStatus/correction metadata on the
//     entry itself. Nothing is ever hard-deleted by these two.
//   - forget is allowed ONLY on a note the caller remembered/filed: it leaves
//     a tombstone (text replaced, relevance=expired, forgottenBy/At) so the
//     content leaves recall while the FACT of the note survives. Every other
//     target answers 403.
//
// Authorization mirrors assistantQuarantineHandler: same-origin, signed-in
// session, then the viewer's RecallPrincipal scopes every read through
// recallStoreForPrincipal — a private note or a project-channel-scoped record
// is invisible to anyone the recall ACL would deny.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

const (
	memoryInspectKindLedger     = "ledger"
	memoryInspectKindNarrative  = "narrative"
	memoryInspectKindDecision   = "decision"
	memoryInspectKindNote       = "note"
	memoryInspectKindProfile    = "user_profile"
	memoryInspectKindWorkResult = "work_result"
	memoryInspectMaxItems       = 200

	inspectorActionClose   = "close"
	inspectorActionCorrect = "correct"
	inspectorActionForget  = "forget"

	inspectorStatusMetadataKey     = "inspectorStatus"
	inspectorByMetadataKey         = "inspectorBy"
	inspectorAtMetadataKey         = "inspectorAt"
	inspectorCorrectionMetadataKey = "correction"
	inspectorCorrectedFromKey      = "correctedFrom"
	noteForgottenMetadataKey       = "forgotten"
	noteForgottenByMetadataKey     = "forgottenBy"
	noteForgottenAtMetadataKey     = "forgottenAt"
	noteForgottenText              = "[forgotten]"

	// ledgerEntityCorrection is the audit entity for inspector actions on
	// non-ledger targets. It is never grouped into the current-state view
	// (ledgerCurrentStateView only knows the four fact classes + position), so
	// a correction record can surface in a status lookup as history without
	// ever impersonating a live decision/action item.
	ledgerEntityCorrection = "correction"
)

type memoryInspectProvenance struct {
	Type  string `json:"type"`
	ID    string `json:"id"`
	Label string `json:"label,omitempty"`
}

type memoryInspectItem struct {
	ID         string                    `json:"id"`
	Kind       string                    `json:"kind"`
	Entity     string                    `json:"entity,omitempty"`
	Title      string                    `json:"title"`
	Summary    string                    `json:"summary"`
	At         string                    `json:"at"`
	Provenance []memoryInspectProvenance `json:"provenance"`
	Status     string                    `json:"status"`
	Person     string                    `json:"person,omitempty"`
	Subject    string                    `json:"subject,omitempty"`
	Own        bool                      `json:"own,omitempty"`
	at         time.Time
}

type memoryInspectFilter struct {
	Subject string
	Person  string
	Since   time.Time
	Kinds   map[string]bool
}

type memoryInspectError struct {
	status  int
	message string
}

func (e *memoryInspectError) Error() string { return e.message }

func inspectErrorf(status int, format string, args ...any) error {
	return &memoryInspectError{status: status, message: fmt.Sprintf(format, args...)}
}

// memoryInspectItems assembles the viewer-scoped inventory, newest first.
func (app *kanbanBoardApp) memoryInspectItems(ctx context.Context, user *userAccount, filter memoryInspectFilter) []memoryInspectItem {
	if app == nil || app.memory == nil || user == nil {
		return nil
	}
	viewer := normalizeAccountEmail(user.Email)
	scoped := app.scopedRecallApp(ctx, recallPrincipalForUser(user))
	if scoped == nil || scoped.memory == nil {
		return nil
	}
	items := make([]memoryInspectItem, 0, 64)

	for _, record := range scoped.memory.ledgerState() {
		items = append(items, memoryInspectItemFromLedgerRecord(record))
	}
	for _, entry := range scoped.allActiveNarrativeEntries() {
		items = append(items, memoryInspectItemFromNarrative(entry))
	}
	for _, entry := range scoped.memory.entriesOfKind(meetingMemoryKindDecision, 0) {
		items = append(items, memoryInspectItemFromDecision(entry))
	}
	for _, entry := range scoped.memory.entriesOfKind(meetingMemoryKindNote, 0) {
		if memoryEntryHiddenFromRecall(entry) {
			continue
		}
		items = append(items, memoryInspectItemFromNote(entry, viewer))
	}
	// The living profile is the viewer's own and only theirs: read from the
	// unscoped app (profiles are workflow artifacts) but never for anyone else.
	if profile, ok := app.tasteProfileForRequester(user.Email); ok {
		items = append(items, memoryInspectItemFromProfile(profile))
	}

	filtered := items[:0]
	for _, item := range items {
		if memoryInspectItemMatches(item, filter) {
			filtered = append(filtered, item)
		}
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		if !filtered[i].at.Equal(filtered[j].at) {
			return filtered[i].at.After(filtered[j].at)
		}
		return filtered[i].ID < filtered[j].ID
	})
	if len(filtered) > memoryInspectMaxItems {
		filtered = filtered[:memoryInspectMaxItems]
	}
	return filtered
}

func memoryInspectItemFromLedgerRecord(record ledgerRecord) memoryInspectItem {
	kind := memoryInspectKindLedger
	if record.Entity == ledgerEntityWorkResult {
		kind = memoryInspectKindWorkResult
	}
	status := strings.TrimSpace(record.Status)
	if !record.current() && !isTerminalLedgerStatus(status) {
		status = ledgerStatusClosed
	}
	provenance := make([]memoryInspectProvenance, 0, len(record.Anchors)+len(record.MeetingIDs)+len(record.AnchorsOverflow)+len(record.MeetingIDsOverflow))
	for _, anchor := range record.Anchors {
		provenance = append(provenance, memoryInspectProvenance{Type: "anchor", ID: anchor})
	}
	for _, anchor := range record.AnchorsOverflow {
		provenance = append(provenance, memoryInspectProvenance{Type: "anchor", ID: anchor, Label: "spilled"})
	}
	for _, meetingID := range record.MeetingIDs {
		provenance = append(provenance, memoryInspectProvenance{Type: "meeting", ID: meetingID})
	}
	for _, meetingID := range record.MeetingIDsOverflow {
		provenance = append(provenance, memoryInspectProvenance{Type: "meeting", ID: meetingID, Label: "spilled"})
	}
	summary := record.Entity + " · " + status
	if owner := strings.TrimSpace(record.Owner); owner != "" {
		summary += " · " + owner
	}
	if len(record.PastOwners) > 0 {
		summary += " (previously " + strings.Join(record.PastOwners, ", ") + ")"
	}
	at := parseInspectStamp(firstNonEmptyString(record.UpdatedAt, record.ValidFrom))
	return memoryInspectItem{
		ID: record.ID, Kind: kind, Entity: record.Entity, Title: record.Title, Summary: summary,
		At: at.Format(time.RFC3339Nano), Provenance: provenance, Status: status, Person: record.Owner,
		Subject: strings.Join(record.Aliases, ", "), at: at,
	}
}

func memoryInspectItemFromNarrative(entry meetingMemoryEntry) memoryInspectItem {
	provenance := []memoryInspectProvenance{{Type: "storyline", ID: strings.TrimSpace(entry.Metadata["slug"])}}
	for _, meetingID := range splitNarrativeMeetingIDs(entry.Metadata["meetingIds"]) {
		provenance = append(provenance, memoryInspectProvenance{Type: "meeting", ID: meetingID})
	}
	at := parseInspectStampOr(entry.Metadata["generatedAt"], entry.CreatedAt)
	status := firstNonEmptyString(strings.TrimSpace(entry.Metadata[inspectorStatusMetadataKey]), relevanceActive)
	return memoryInspectItem{
		ID: entry.ID, Kind: memoryInspectKindNarrative,
		Title:   firstNonEmptyString(strings.TrimSpace(entry.Metadata["title"]), strings.TrimSpace(entry.Metadata["slug"])),
		Summary: narrativeStatusLine(entry), At: at.Format(time.RFC3339Nano), Provenance: provenance, Status: status,
		Subject: strings.Join(splitNarrativeAliases(entry.Metadata["aliases"]), ", "), at: at,
	}
}

func memoryInspectItemFromDecision(entry meetingMemoryEntry) memoryInspectItem {
	provenance := make([]memoryInspectProvenance, 0, 4)
	if meetingID := strings.TrimSpace(entry.Metadata["meetingId"]); meetingID != "" && meetingID != "none" {
		provenance = append(provenance, memoryInspectProvenance{Type: "meeting", ID: meetingID})
	}
	if brainID := strings.TrimSpace(entry.Metadata["sourceBrainId"]); brainID != "" {
		provenance = append(provenance, memoryInspectProvenance{Type: "brain", ID: brainID})
	}
	if filedBy := strings.TrimSpace(entry.Metadata["filedBy"]); filedBy != "" {
		provenance = append(provenance, memoryInspectProvenance{Type: "person", ID: filedBy, Label: "filed by"})
	}
	if ratifiedBy := strings.TrimSpace(entry.Metadata["ratifiedBy"]); ratifiedBy != "" {
		provenance = append(provenance, memoryInspectProvenance{Type: "person", ID: ratifiedBy, Label: "ratified by"})
	}
	if supersededBy := strings.TrimSpace(entry.Metadata["supersededBy"]); supersededBy != "" {
		provenance = append(provenance, memoryInspectProvenance{Type: "decision", ID: supersededBy, Label: "superseded by"})
	}
	status := firstNonEmptyString(strings.TrimSpace(entry.Metadata[inspectorStatusMetadataKey]), strings.TrimSpace(entry.Metadata["status"]), decisionStatusActive)
	return memoryInspectItem{
		ID: entry.ID, Kind: memoryInspectKindDecision, Title: trimForStorage(compactAssistantLine(entry.Text), 240),
		Summary: strings.TrimSpace(entry.Metadata["context"]), At: entry.CreatedAt.UTC().Format(time.RFC3339Nano),
		Provenance: provenance, Status: status, Person: strings.TrimSpace(entry.Metadata["madeBy"]), at: entry.CreatedAt.UTC(),
	}
}

func memoryInspectItemFromNote(entry meetingMemoryEntry, viewer string) memoryInspectItem {
	provenance := make([]memoryInspectProvenance, 0, 3)
	if threadID := strings.TrimSpace(entry.Metadata[noteSourceThreadMetadataKey]); threadID != "" {
		provenance = append(provenance, memoryInspectProvenance{Type: "thread", ID: threadID})
	}
	if messageID := strings.TrimSpace(entry.Metadata[noteSourceMessageMetadataKey]); messageID != "" {
		provenance = append(provenance, memoryInspectProvenance{Type: "message", ID: messageID})
	}
	if meetingID := strings.TrimSpace(entry.Metadata["meetingId"]); meetingID != "" && meetingID != "none" {
		provenance = append(provenance, memoryInspectProvenance{Type: "meeting", ID: meetingID})
	}
	person := firstNonEmptyString(strings.TrimSpace(entry.Metadata[noteRememberedByMetadataKey]), strings.TrimSpace(entry.Metadata["author"]))
	if person != "" {
		provenance = append(provenance, memoryInspectProvenance{Type: "person", ID: person, Label: "remembered by"})
	}
	at := parseInspectStampOr(entry.Metadata[noteAtMetadataKey], entry.CreatedAt)
	subject := strings.TrimSpace(entry.Metadata[noteSubjectMetadataKey])
	if subject == "" {
		subject = strings.TrimSpace(entry.Metadata["topic"])
	}
	return memoryInspectItem{
		ID: entry.ID, Kind: memoryInspectKindNote, Title: firstNonEmptyString(subject, trimForStorage(compactAssistantLine(entry.Text), 120)),
		Summary: trimForStorage(entry.Text, 600), At: at.Format(time.RFC3339Nano), Provenance: provenance,
		Status: firstNonEmptyString(strings.TrimSpace(entry.Metadata[inspectorStatusMetadataKey]), relevanceActive),
		Person: person, Subject: subject, Own: noteAuthoredBy(entry, viewer), at: at,
	}
}

func memoryInspectItemFromProfile(entry meetingMemoryEntry) memoryInspectItem {
	provenance := make([]memoryInspectProvenance, 0, 2)
	if count := strings.TrimSpace(entry.Metadata["signalCount"]); count != "" {
		provenance = append(provenance, memoryInspectProvenance{Type: "signals", ID: count, Label: "distilled signals"})
	}
	if count := strings.TrimSpace(entry.Metadata[tasteProfileDecisionCountKey]); count != "" {
		provenance = append(provenance, memoryInspectProvenance{Type: "decisions", ID: count, Label: "decisions considered"})
	}
	if cursor := strings.TrimSpace(entry.Metadata[tasteAnalystCursorKey]); cursor != "" {
		provenance = append(provenance, memoryInspectProvenance{Type: "signal", ID: cursor, Label: "consumed through"})
	}
	at := parseInspectStampOr(entry.Metadata[tasteProfileDistilledAtKey], entry.CreatedAt)
	return memoryInspectItem{
		ID: entry.ID, Kind: memoryInspectKindProfile, Title: firstNonEmptyString(strings.TrimSpace(entry.Metadata["title"]), "Taste profile"),
		Summary: trimForStorage(entry.Text, 1200), At: at.Format(time.RFC3339Nano), Provenance: provenance, Status: "living",
		Person: strings.TrimSpace(entry.Metadata[tasteProfileUserKey]), Own: true, at: at,
	}
}

// noteAuthoredBy reports whether viewer remembered/filed the note: the
// rememberedBy email, or the author display name resolving to the viewer.
func noteAuthoredBy(entry meetingMemoryEntry, viewer string) bool {
	viewer = normalizeAccountEmail(viewer)
	if viewer == "" {
		return false
	}
	if normalizeAccountEmail(entry.Metadata[noteRememberedByMetadataKey]) == viewer {
		return true
	}
	for _, key := range []string{"author", "filedBy", "createdBy"} {
		value := strings.TrimSpace(entry.Metadata[key])
		if value == "" {
			continue
		}
		if normalizeAccountEmail(value) == viewer {
			return true
		}
		if name := participantNameForEmail(viewer); name != "" && strings.EqualFold(value, name) {
			return true
		}
	}
	return false
}

func parseInspectStamp(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	if parsed, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return parsed.UTC()
	}
	if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
		return parsed.UTC()
	}
	return time.Time{}
}

func parseInspectStampOr(raw string, fallback time.Time) time.Time {
	if parsed := parseInspectStamp(raw); !parsed.IsZero() {
		return parsed
	}
	return fallback.UTC()
}

// memoryInspectItemMatches applies the subject/person/since/kinds filter.
func memoryInspectItemMatches(item memoryInspectItem, filter memoryInspectFilter) bool {
	if len(filter.Kinds) > 0 && !filter.Kinds[item.Kind] {
		return false
	}
	if !filter.Since.IsZero() && item.at.Before(filter.Since) {
		return false
	}
	if person := strings.TrimSpace(filter.Person); person != "" && !inspectPersonMatches(item.Person, person) {
		return false
	}
	if subject := strings.TrimSpace(filter.Subject); subject != "" {
		wanted := stemmedMemoryTokenSet(subject)
		if len(wanted) > 0 {
			have := stemmedMemoryTokenSet(item.Title + " " + item.Summary + " " + item.Subject)
			matched := false
			for stem := range wanted {
				if _, ok := have[stem]; ok {
					matched = true
					break
				}
			}
			if !matched {
				return false
			}
		}
	}
	return true
}

// inspectPersonMatches resolves both sides through the roster so "AJ",
// "aj@shareability.com", and "attributed to AJ" all name the same person.
func inspectPersonMatches(have string, want string) bool {
	have = strings.TrimSpace(strings.TrimPrefix(strings.ToLower(strings.TrimSpace(have)), digestAttributionHedge))
	want = strings.TrimSpace(strings.ToLower(want))
	if have == "" || want == "" {
		return false
	}
	if have == want {
		return true
	}
	if haveName := participantNameForEmail(have); haveName != "" {
		have = strings.ToLower(haveName)
	}
	if wantName := participantNameForEmail(want); wantName != "" {
		want = strings.ToLower(wantName)
	}
	return have == want
}

// applyMemoryInspectAction performs close | correct | forget for one item.
func (app *kanbanBoardApp) applyMemoryInspectAction(ctx context.Context, user *userAccount, id string, action string, correction string) (map[string]any, error) {
	if app == nil || app.memory == nil {
		return nil, inspectErrorf(http.StatusServiceUnavailable, "memory is unavailable")
	}
	if user == nil {
		return nil, inspectErrorf(http.StatusUnauthorized, "not signed in")
	}
	viewer := normalizeAccountEmail(user.Email)
	actor := firstNonEmptyString(participantNameForEmail(viewer), viewer)
	id = strings.TrimSpace(id)
	action = strings.ToLower(strings.TrimSpace(action))
	correction = trimForStorage(normalizeMemoryText(correction), ledgerTitleLimit)
	if id == "" {
		return nil, inspectErrorf(http.StatusBadRequest, "id is required")
	}
	switch action {
	case inspectorActionClose, inspectorActionCorrect, inspectorActionForget:
	default:
		return nil, inspectErrorf(http.StatusBadRequest, "action must be close, correct, or forget")
	}
	if action == inspectorActionCorrect && correction == "" {
		return nil, inspectErrorf(http.StatusBadRequest, "correction is required")
	}
	now := time.Now().UTC()
	nowStamp := now.Format(time.RFC3339)
	scoped := app.scopedRecallApp(ctx, recallPrincipalForUser(user))

	// The viewer's OWN living profile: correctable (an overriding statement
	// appended to the body + a closed audit event), never closed or forgotten.
	// Anyone else's profile id is not an inspectable item for this viewer.
	if profile, ok := app.tasteProfileForRequester(user.Email); ok && profile.ID == id {
		if action != inspectorActionCorrect {
			return nil, inspectErrorf(http.StatusForbidden, "a living profile is corrected, never closed or forgotten")
		}
		body := appendProfileCorrection(profile.Text, correction, now)
		updates := map[string]string{
			inspectorStatusMetadataKey:     "corrected",
			inspectorByMetadataKey:         viewer,
			inspectorAtMetadataKey:         now.Format(time.RFC3339Nano),
			inspectorCorrectionMetadataKey: correction,
		}
		title := firstNonEmptyString(strings.TrimSpace(profile.Metadata["title"]), tasteProfileTitle(strings.TrimSpace(profile.Metadata[tasteProfileUserKey])))
		if _, _, err := app.updateOSArtifactWithMetadata(profile.ID, title, body, actor, updates); err != nil {
			return nil, inspectErrorf(http.StatusInternalServerError, "%v", err)
		}
		event := ledgerEventPayload{
			Op: ledgerOpUpdate,
			Record: ledgerRecord{
				ID: fmt.Sprintf("ldg-%s-%s-%d", ledgerEntityCorrection, profile.ID, now.UnixNano()), Entity: ledgerEntityCorrection,
				Title: correction, Status: "corrected", Owner: viewer,
				ValidFrom: profile.CreatedAt.UTC().Format(time.RFC3339), ValidTo: nowStamp, Anchors: []string{profile.ID}, UpdatedAt: nowStamp,
			},
			Reason: "profile corrected by " + actor,
			At:     nowStamp,
		}
		extra := map[string]string{
			"inspectorAction": action, "actor": viewer, "targetKind": memoryInspectKindProfile,
			inspectorCorrectionMetadataKey: correction,
			// The audit row is the viewer's own: private, like the profile card.
			"visibility": "private", "ownerEmail": viewer,
		}
		entries, err := ledgerEventEntries([]ledgerEventPayload{event}, nil, now, extra)
		if err != nil {
			return nil, inspectErrorf(http.StatusInternalServerError, "%v", err)
		}
		if _, err := app.memory.appendLedgerEvents(entries); err != nil {
			return nil, inspectErrorf(http.StatusInternalServerError, "%v", err)
		}
		broadcastSignedInKanbanEvent("memory", nil)
		return map[string]any{"ok": true, "id": id, "action": action, "kind": memoryInspectKindProfile, "eventId": entries[0].ID, "status": "corrected"}, nil
	}

	if record, ok := scoped.memory.ledgerState()[id]; ok {
		if action == inspectorActionForget {
			return nil, inspectErrorf(http.StatusForbidden, "ledger records are never deleted; close or correct instead")
		}
		event := ledgerEventPayload{Record: record, At: nowStamp}
		extra := map[string]string{"inspectorAction": action, "actor": viewer}
		// The audit event inherits the recall fence the record was filed under
		// (a private work_result stays private): ledgerState() is last-write-
		// wins per record id, so an organization-stamped close/correct would
		// republish the private title + owner org-wide.
		copyRecallFence(extra, ledgerRecordRecallFence(app.memory, id))
		switch action {
		case inspectorActionClose:
			if !record.current() {
				return nil, inspectErrorf(http.StatusConflict, "record is already closed")
			}
			event.Op = ledgerOpClose
			event.Record.Status = ledgerStatusClosed
			event.Record.ValidTo = nowStamp
			event.Reason = "closed by " + actor
		case inspectorActionCorrect:
			event.Op = ledgerOpUpdate
			event.Record.Title = correction
			event.Reason = "corrected by " + actor
			extra[inspectorCorrectionMetadataKey] = correction
		}
		event.Record.UpdatedAt = nowStamp
		entries, err := ledgerEventEntries([]ledgerEventPayload{event}, nil, now, extra)
		if err != nil {
			return nil, inspectErrorf(http.StatusInternalServerError, "%v", err)
		}
		if _, err := app.memory.appendLedgerEvents(entries); err != nil {
			return nil, inspectErrorf(http.StatusInternalServerError, "%v", err)
		}
		return map[string]any{"ok": true, "id": id, "action": action, "kind": memoryInspectKindLedger, "eventId": entries[0].ID, "status": event.Record.Status}, nil
	}

	entry, found := scoped.memory.entryByID(id)
	if !found {
		return nil, inspectErrorf(http.StatusNotFound, "memory item not found")
	}
	switch entry.Kind {
	case meetingMemoryKindNote, meetingMemoryKindDecision, meetingMemoryKindNarrative:
	default:
		return nil, inspectErrorf(http.StatusNotFound, "memory item not found")
	}

	if action == inspectorActionForget {
		if entry.Kind != meetingMemoryKindNote {
			return nil, inspectErrorf(http.StatusForbidden, "only a note can be forgotten; close or correct instead")
		}
		if !noteAuthoredBy(entry, viewer) {
			return nil, inspectErrorf(http.StatusForbidden, "only the person who remembered a note can forget it")
		}
		updates := map[string]string{
			relevanceMetadataKey:       relevanceExpired,
			noteForgottenMetadataKey:   "true",
			noteForgottenByMetadataKey: viewer,
			noteForgottenAtMetadataKey: now.Format(time.RFC3339Nano),
			digestAliasesMetadataKey:   "",
			noteAliasesMetadataKey:     "",
		}
		if _, _, err := app.memory.updateEntryWithMetadata(meetingMemoryKindNote, entry.ID, noteForgottenText, updates); err != nil {
			return nil, inspectErrorf(http.StatusInternalServerError, "%v", err)
		}
		broadcastSignedInKanbanEvent("memory", nil)
		return map[string]any{"ok": true, "id": id, "action": action, "kind": memoryInspectKindNote, "forgotten": true}, nil
	}

	title := trimForStorage(compactAssistantLine(entry.Text), ledgerTitleLimit)
	if entry.Kind == meetingMemoryKindNarrative {
		title = firstNonEmptyString(strings.TrimSpace(entry.Metadata["title"]), strings.TrimSpace(entry.Metadata["slug"]), title)
	}
	// The audit record gets its own id: the entry keeps its identity for later
	// actions (a correct followed by a close must not collide on the fold).
	event := ledgerEventPayload{
		Record: ledgerRecord{
			ID: fmt.Sprintf("ldg-%s-%s-%d", ledgerEntityCorrection, entry.ID, now.UnixNano()), Entity: ledgerEntityCorrection, Title: title, Owner: "",
			ValidFrom: entry.CreatedAt.UTC().Format(time.RFC3339), Anchors: []string{entry.ID}, UpdatedAt: nowStamp,
		},
		At: nowStamp,
	}
	extra := map[string]string{"inspectorAction": action, "actor": viewer, "targetKind": entry.Kind}
	// Same fence discipline as the ledger branch: a note/decision/narrative
	// filed private or member-restricted keeps its audit event inside that
	// fence instead of widening the corrected title to the organization.
	copyRecallFence(extra, entry.Metadata)
	updates := map[string]string{inspectorByMetadataKey: viewer, inspectorAtMetadataKey: now.Format(time.RFC3339Nano)}
	text := entry.Text
	switch action {
	case inspectorActionClose:
		event.Op = ledgerOpClose
		event.Record.Status = ledgerStatusClosed
		event.Record.ValidTo = nowStamp
		event.Reason = "closed by " + actor
		updates[inspectorStatusMetadataKey] = ledgerStatusClosed
	case inspectorActionCorrect:
		event.Op = ledgerOpUpdate
		event.Record.Status = "corrected"
		event.Record.Title = correction
		event.Record.ValidTo = nowStamp
		event.Reason = "corrected by " + actor
		extra[inspectorCorrectionMetadataKey] = correction
		updates[inspectorStatusMetadataKey] = "corrected"
		updates[inspectorCorrectionMetadataKey] = correction
		switch entry.Kind {
		case meetingMemoryKindNote, meetingMemoryKindDecision:
			// The corrected statement becomes what recall pins; the prior text
			// survives as metadata so the correction is auditable.
			updates[inspectorCorrectedFromKey] = trimForStorage(entry.Text, 500)
			text = correction
		case meetingMemoryKindNarrative:
			updates["status"] = correction
		}
	}
	entries, err := ledgerEventEntries([]ledgerEventPayload{event}, nil, now, extra)
	if err != nil {
		return nil, inspectErrorf(http.StatusInternalServerError, "%v", err)
	}
	if _, err := app.memory.appendLedgerEvents(entries); err != nil {
		return nil, inspectErrorf(http.StatusInternalServerError, "%v", err)
	}
	if _, _, err := app.memory.updateEntryWithMetadata(entry.Kind, entry.ID, text, updates); err != nil {
		return nil, inspectErrorf(http.StatusInternalServerError, "%v", err)
	}
	if entry.Kind == meetingMemoryKindDecision {
		if updated, ok := app.memory.entryByID(entry.ID); ok {
			broadcastOfficeKanbanEvent("decision", decisionPayload(updated))
		}
	}
	broadcastSignedInKanbanEvent("memory", nil)
	return map[string]any{"ok": true, "id": id, "action": action, "kind": entry.Kind, "eventId": entries[0].ID, "status": updates[inspectorStatusMetadataKey]}, nil
}

// recallFenceMetadataKeys are the stamps recallEntryScopeAllowed reads to fence
// an entry: who may recall it and in which tenant/room/sitting.
var recallFenceMetadataKeys = []string{"visibility", "ownerEmail", "memberEmails", "tenantId", "roomId", "sittingId"}

// copyRecallFence copies the recall fence stamps present on source onto dst
// without overriding a key dst already carries.
func copyRecallFence(dst map[string]string, source map[string]string) {
	if dst == nil || len(source) == 0 {
		return
	}
	for _, key := range recallFenceMetadataKeys {
		if strings.TrimSpace(dst[key]) != "" {
			continue
		}
		if value := strings.TrimSpace(source[key]); value != "" {
			dst[key] = value
		}
	}
}

// ledgerRecordRecallFence returns the recall fence stamps of the NEWEST
// ledger_event carrying this record id — the scope the record currently
// lives under in the fold — so a follow-up inspector event inherits it.
func ledgerRecordRecallFence(store *meetingMemoryStore, recordID string) map[string]string {
	recordID = strings.TrimSpace(recordID)
	if store == nil || recordID == "" {
		return nil
	}
	events := store.entriesOfKind(meetingMemoryKindLedgerEvent, 0)
	for index := len(events) - 1; index >= 0; index-- {
		if strings.TrimSpace(events[index].Metadata["recordId"]) != recordID {
			continue
		}
		fence := map[string]string{}
		copyRecallFence(fence, events[index].Metadata)
		return fence
	}
	return nil
}

// profileCorrectionsHeading is the section the viewer's own corrections live
// under on their profile body. The analyst's next pass reads the whole body as
// "the living document — update, never restart", so an overriding statement
// here outranks the distilled bullets above it.
const profileCorrectionsHeading = "## Corrections (from you — override the analyst above)"

// appendProfileCorrection appends one dated overriding statement to the
// profile body, creating the corrections section on first use.
func appendProfileCorrection(body string, correction string, at time.Time) string {
	body = strings.TrimRight(body, "\n")
	line := "- " + correction + " (corrected " + at.UTC().Format("2006-01-02") + ")"
	if strings.Contains(body, profileCorrectionsHeading) {
		return body + "\n" + line + "\n"
	}
	return body + "\n\n" + profileCorrectionsHeading + "\n" + line + "\n"
}

// parseMemoryInspectFilter reads the GET query string.
func parseMemoryInspectFilter(r *http.Request) memoryInspectFilter {
	query := r.URL.Query()
	filter := memoryInspectFilter{Subject: strings.TrimSpace(query.Get("subject")), Person: strings.TrimSpace(query.Get("person"))}
	if since := strings.TrimSpace(query.Get("since")); since != "" {
		if parsed := parseInspectStamp(since); !parsed.IsZero() {
			filter.Since = parsed
		} else if parsed, err := time.ParseInLocation("2006-01-02", since, meetingTimeLocation()); err == nil {
			filter.Since = parsed.UTC()
		}
	}
	if kinds := strings.TrimSpace(query.Get("kinds")); kinds != "" {
		filter.Kinds = map[string]bool{}
		for _, kind := range strings.Split(kinds, ",") {
			if kind = strings.ToLower(strings.TrimSpace(kind)); kind != "" {
				filter.Kinds[kind] = true
			}
		}
	}
	return filter
}

// assistantMemoryInspectHandler serves GET /assistant/memory/inspect.
func assistantMemoryInspectHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !websocketOriginAllowed(r) {
		writeAuthError(w, http.StatusForbidden, "cross-origin request rejected")
		return
	}
	user := userFromRequest(r)
	if user == nil {
		writeAuthError(w, http.StatusUnauthorized, "not signed in")
		return
	}
	if kanbanApp == nil {
		writeAuthError(w, http.StatusServiceUnavailable, "memory is unavailable")
		return
	}
	items := kanbanApp.memoryInspectItems(r.Context(), user, parseMemoryInspectFilter(r))
	if items == nil {
		items = []memoryInspectItem{}
	}
	writeAuthJSON(w, http.StatusOK, map[string]any{
		"ok":     true,
		"viewer": normalizeAccountEmail(user.Email),
		"count":  len(items),
		"items":  items,
	})
}

// assistantMemoryInspectActionHandler serves POST /assistant/memory/inspect/action.
func assistantMemoryInspectActionHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !websocketOriginAllowed(r) {
		writeAuthError(w, http.StatusForbidden, "cross-origin request rejected")
		return
	}
	user := userFromRequest(r)
	if user == nil {
		writeAuthError(w, http.StatusUnauthorized, "not signed in")
		return
	}
	if kanbanApp == nil {
		writeAuthError(w, http.StatusServiceUnavailable, "memory is unavailable")
		return
	}
	var payload struct {
		ID         string `json:"id"`
		Action     string `json:"action"`
		Correction string `json:"correction"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&payload); err != nil {
		writeAuthError(w, http.StatusBadRequest, "could not read inspect action")
		return
	}
	result, err := kanbanApp.applyMemoryInspectAction(r.Context(), user, payload.ID, payload.Action, payload.Correction)
	if err != nil {
		status := http.StatusBadRequest
		var typed *memoryInspectError
		if asInspectError(err, &typed) {
			status = typed.status
		}
		writeAuthError(w, status, err.Error())
		return
	}
	writeAuthJSON(w, http.StatusOK, result)
}

func asInspectError(err error, target **memoryInspectError) bool {
	typed, ok := err.(*memoryInspectError)
	if ok {
		*target = typed
	}
	return ok
}

// memoryInspectStatusCode is exported for tests that assert on the mapping.
func memoryInspectStatusCode(err error) int {
	var typed *memoryInspectError
	if asInspectError(err, &typed) {
		return typed.status
	}
	return http.StatusBadRequest
}
