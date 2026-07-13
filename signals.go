package main

// Signal capture (packaging OS §5 "Capture", Wave 1 item 3) — the compounding
// brain's missing input. Every seam where a human reacts to OS output (edits
// agent copy, publishes, attaches to a package, asks for a re-run, approves or
// rejects a proposal, opens a deliverable) appends one memory kind="signal"
// entry. Capture is append-only and FREE: no model calls, ever — tokens are
// spent only later, at distillation (the Taste Analyst / House-Style Distiller
// read the signal window; raw signals themselves never ground an answer).
//
// STORAGE: kind "signal" rides the JSONL store like every other kind
// (entry.Text = compact signalRecord JSON, the deal_room/package precedent).
// Signals are UI/workspace state, never knowledge: registered in
// isUIStateMemoryKind (memory.go) so they never enter Scout search results or
// model context.
//
// FAILURE CONTRACT: a signal write must never fail its parent operation. Seams
// call (app *kanbanBoardApp).recordSignalEvent, which logs and continues.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"
)

const (
	// meetingMemoryKindSignal is one captured human-feedback event — entry.Text
	// is the compact signalRecord JSON. UI state: raw signals never pollute
	// recall (isUIStateMemoryKind), only distillation reads them.
	meetingMemoryKindSignal = "signal"

	signalValencePositive = "positive"
	signalValenceNegative = "negative"
	signalValenceNeutral  = "neutral"

	// signal event names, one per capture seam.
	signalEventArtifactEdited    = "artifact_edited"
	signalEventArtifactPublished = "artifact_published"
	signalEventArtifactAttached  = "artifact_attached"
	signalEventArtifactRerun     = "artifact_rerun"
	signalEventArtifactOpened    = "artifact_opened"
	signalEventProposalApproved  = "proposal_approved"
	signalEventProposalRejected  = "proposal_rejected"
	// artifact_salvaged (§5 item 2): a goal terminated needs_attention but its
	// best draft was saved — an agent failure worth studying, with the missed
	// gate bar as payload.
	signalEventArtifactSalvaged = "artifact_salvaged"
	// quarantine_restored (§5 item 6): a human overruled the slop classifier —
	// a precision datum on the classifier and a vote for the entry.
	signalEventQuarantineRestored = "quarantine_restored"
	// proposal_confirmed/_dismissed (§5 item 6): the codex proposal card's
	// confirm/dismiss — a distinct seam from the approval gate's
	// proposal_approved/_rejected above.
	signalEventProposalConfirmed = "proposal_confirmed"
	signalEventProposalDismissed = "proposal_dismissed"
	// survey_landed/_off (§5 "Surveys: garnish, not a surface", Wave 2 item
	// 11): the one-tap chips on existing completion cards — the ONLY explicit
	// ask in the whole capture system, so the taste rules below keep it rare.
	signalEventSurveyLanded = "survey_landed"
	signalEventSurveyOff    = "survey_off"

	// signalPayloadValueLimit caps each payload value (bytes, rune-boundary
	// safe) so a pasted novel can never bloat the highest-volume kind in the
	// store.
	signalPayloadValueLimit = 500

	// survey verdicts: the two chips. "off" carries the optional free-text
	// line; "landed" needs no elaboration.
	surveyVerdictLanded = "landed"
	surveyVerdictOff    = "off"

	// signalSurveyNoteLimit caps the "off" chip's free-text line (bytes,
	// rune-boundary safe) — one line of taste, not an essay.
	signalSurveyNoteLimit = 300
	// signalSurveyImplicitVolumeThreshold is spec rule zero made concrete:
	// once this many implicit signals already reference an artifact, a survey
	// answer is redundant — store it flagged suppressed=true so the analyst
	// can calibrate the chips, but never let it count as fresh taste.
	signalSurveyImplicitVolumeThreshold = 3
)

// signalRecord is the wire/storage shape of one signal — compact JSON in
// entry.Text (the dealRoomRecord precedent).
type signalRecord struct {
	Actor      string            `json:"actor,omitempty"`
	Event      string            `json:"event"`
	Valence    string            `json:"valence,omitempty"`
	ArtifactID string            `json:"artifactId,omitempty"`
	PackageID  string            `json:"packageId,omitempty"`
	Payload    map[string]string `json:"payload,omitempty"`
}

// recordSignal appends one kind=signal entry. Zero model calls; the payload
// values are truncated so capture stays cheap in bytes too. Metadata mirrors
// {event, valence, actor, artifactId, packageId} so future distillers can
// filter without parsing JSON.
func recordSignal(store *meetingMemoryStore, actor string, event string, valence string, artifactID string, packageID string, payload map[string]string) (meetingMemoryEntry, error) {
	if store == nil {
		return meetingMemoryEntry{}, fmt.Errorf("memory store is unavailable")
	}
	event = strings.TrimSpace(event)
	if event == "" {
		return meetingMemoryEntry{}, fmt.Errorf("signal event is required")
	}
	valence = strings.TrimSpace(strings.ToLower(valence))
	switch valence {
	case signalValencePositive, signalValenceNegative, signalValenceNeutral, "":
	default:
		valence = signalValenceNeutral
	}

	record := signalRecord{
		Actor:      strings.TrimSpace(actor),
		Event:      event,
		Valence:    valence,
		ArtifactID: strings.TrimSpace(artifactID),
		PackageID:  strings.TrimSpace(packageID),
	}
	if len(payload) > 0 {
		record.Payload = make(map[string]string, len(payload))
		for key, value := range payload {
			key = strings.TrimSpace(key)
			value = truncateAgentThreadText(value, signalPayloadValueLimit)
			if key == "" || value == "" {
				continue
			}
			record.Payload[key] = value
		}
		if len(record.Payload) == 0 {
			record.Payload = nil
		}
	}

	encoded, err := json.Marshal(record)
	if err != nil {
		return meetingMemoryEntry{}, fmt.Errorf("encode signal: %w", err)
	}

	metadata := map[string]string{"event": event}
	if record.Valence != "" {
		metadata["valence"] = record.Valence
	}
	if record.Actor != "" {
		metadata["actor"] = record.Actor
	}
	if record.ArtifactID != "" {
		metadata["artifactId"] = record.ArtifactID
	}
	if record.PackageID != "" {
		metadata["packageId"] = record.PackageID
	}

	id := fmt.Sprintf("signal-%s-%d", event, time.Now().UnixNano())
	entry, appended, err := store.appendEntry(meetingMemoryKindSignal, id, string(encoded), metadata)
	if err != nil {
		return meetingMemoryEntry{}, err
	}
	if !appended {
		return meetingMemoryEntry{}, fmt.Errorf("signal was not saved")
	}
	return entry, nil
}

// recordSignalEvent is the seam-facing wrapper: a signal write NEVER fails the
// parent operation — errors are logged and swallowed.
func (app *kanbanBoardApp) recordSignalEvent(actor string, event string, valence string, artifactID string, packageID string, payload map[string]string) {
	if app == nil || app.memory == nil {
		return
	}
	if _, err := recordSignal(app.memory, actor, event, valence, artifactID, packageID, payload); err != nil {
		log.Errorf("Failed to record %s signal for %q: %v", event, artifactID, err)
	}
}

// decodeSignalEntry mirrors decodeDealRoomEntry: the record is the entry text.
func decodeSignalEntry(entry meetingMemoryEntry) (signalRecord, bool) {
	if entry.Kind != meetingMemoryKindSignal {
		return signalRecord{}, false
	}
	var record signalRecord
	if err := json.Unmarshal([]byte(entry.Text), &record); err != nil {
		return signalRecord{}, false
	}
	if strings.TrimSpace(record.Event) == "" {
		return signalRecord{}, false
	}
	return record, true
}

// --- section-level diff helper (the artifact_edited payload) ----------------

// signalHeadingPattern matches a markdown heading line; the capture is the
// heading text without the hashes.
var signalHeadingPattern = regexp.MustCompile(`^#{1,6}\s+(.+)$`)

// signalDiffIntroHeading names the pseudo-section before the first heading.
const signalDiffIntroHeading = "(intro)"

// summarizeArtifactDiff builds the compact artifact_edited payload from the
// prior and replacement bodies: changed/added/removed section headings plus a
// rough character delta. Deliberately NEVER both full bodies — a deleted
// section heading and a shrinking delta ARE the taste data; the store must not
// double as a diff backup.
func summarizeArtifactDiff(prior string, next string) map[string]string {
	priorOrder, priorSections := artifactDiffSections(prior)
	nextOrder, nextSections := artifactDiffSections(next)

	added := make([]string, 0, len(nextOrder))
	changed := make([]string, 0, len(nextOrder))
	for _, heading := range nextOrder {
		priorBody, existed := priorSections[heading]
		if !existed {
			added = append(added, heading)
			continue
		}
		if priorBody != nextSections[heading] {
			changed = append(changed, heading)
		}
	}
	removed := make([]string, 0, len(priorOrder))
	for _, heading := range priorOrder {
		if _, exists := nextSections[heading]; !exists {
			removed = append(removed, heading)
		}
	}

	summary := map[string]string{
		"charsDelta": fmt.Sprintf("%+d", len(next)-len(prior)),
	}
	if len(added) > 0 {
		summary["addedSections"] = strings.Join(added, "; ")
	}
	if len(removed) > 0 {
		summary["removedSections"] = strings.Join(removed, "; ")
	}
	if len(changed) > 0 {
		summary["changedSections"] = strings.Join(changed, "; ")
	}
	return summary
}

// artifactDiffSections splits a markdown body into heading -> body, first
// occurrence winning on duplicate headings; text before the first heading
// files under "(intro)". Order preserves the document's heading order.
func artifactDiffSections(text string) ([]string, map[string]string) {
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	order := []string{}
	sections := map[string]string{}
	current := signalDiffIntroHeading
	bodies := map[string][]string{}
	for _, line := range strings.Split(text, "\n") {
		if match := signalHeadingPattern.FindStringSubmatch(strings.TrimSpace(line)); match != nil {
			heading := strings.TrimSpace(match[1])
			if _, seen := sections[heading]; !seen {
				order = append(order, heading)
				sections[heading] = ""
			}
			current = heading
			continue
		}
		bodies[current] = append(bodies[current], line)
	}
	for heading := range sections {
		sections[heading] = strings.TrimSpace(strings.Join(bodies[heading], "\n"))
	}
	if intro := strings.TrimSpace(strings.Join(bodies[signalDiffIntroHeading], "\n")); intro != "" {
		order = append([]string{signalDiffIntroHeading}, order...)
		sections[signalDiffIntroHeading] = intro
	}
	return order, sections
}

// --- POST /artifacts/open ----------------------------------------------------

// artifactOpenHandler records the open/ignore signal (spec §5: "a never-opened
// deliverable is a negative signal on that tool for that user") and stamps
// metadata openedAt — both on the FIRST open only. The datum is open vs
// never-opened; click counts are zero-information volume in the store's
// highest-volume kind. Session-gated exactly like artifactsHandler; registered
// beside it in main.go.
func artifactOpenHandler(w http.ResponseWriter, r *http.Request) {
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
		writeAuthError(w, http.StatusServiceUnavailable, "artifacts are unavailable")
		return
	}

	payload := struct {
		ID string `json:"id"`
	}{}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&payload); err != nil {
		writeAuthError(w, http.StatusBadRequest, "could not read artifact open request")
		return
	}
	artifact, found := authorizedArtifactByID(r.Context(), user, ACLReadContent, strings.TrimSpace(payload.ID))
	if !found {
		writeAuthError(w, http.StatusNotFound, "artifact not found")
		return
	}

	openedAt := strings.TrimSpace(artifact.Metadata["openedAt"])
	if openedAt == "" {
		openedAt = time.Now().UTC().Format(time.RFC3339Nano)
		// First open only. The §5 item 5 datum is open vs never-opened, not a
		// click count — signals are the highest-volume kind in a JSONL store
		// held in RAM and rewritten whole, and compaction doesn't ship until
		// Wave 3, so routine library browsing must not flood it. Recorded
		// before the stamp so a failed stamp never loses the signal.
		kanbanApp.recordSignalEvent(user.Name, signalEventArtifactOpened, signalValenceNeutral, artifact.ID, artifact.Metadata["packageId"], nil)
		// Bookkeeping stamp only, via the metadata-only path: passing the
		// artifact.Text snapshot read above into updateOSArtifactWithMetadata
		// would revert a body written concurrently (opens happen exactly while
		// a thread runner finishes the artifact). A failure loses first-open
		// latency data, never the open signal above — log and continue.
		if _, _, err := kanbanApp.memory.updateOSArtifactMetadata(artifact.ID, map[string]string{"openedAt": openedAt}); err != nil {
			log.Errorf("Failed to stamp openedAt on artifact %s: %v", artifact.ID, err)
		}
	}

	writeAuthJSON(w, http.StatusOK, map[string]any{
		"ok":       true,
		"openedAt": openedAt,
	})
}

// --- POST /signals/survey ------------------------------------------------------

// surveyTasteOutcome is evaluateSurveyTasteRules' verdict on whether one more
// explicit ask is worth its interruption cost.
type surveyTasteOutcome int

const (
	// surveyOutcomeStore: fresh taste — record it.
	surveyOutcomeStore surveyTasteOutcome = iota
	// surveyOutcomeDuplicate: this package+stage combination was already
	// surveyed — idempotent 200, nothing stored (re-taps must not double-vote).
	surveyOutcomeDuplicate
	// surveyOutcomeRateLimited: the user already stored a survey today — 429.
	surveyOutcomeRateLimited
	// surveyOutcomeSuppressed: implicit volume on the artifact is already high
	// — store flagged suppressed=true (rule zero: never ask what implicit
	// signals already answered; the flag lets the analyst calibrate the chips).
	surveyOutcomeSuppressed
)

// isSurveySignalEvent separates the explicit chips from the implicit seams.
func isSurveySignalEvent(event string) bool {
	return event == signalEventSurveyLanded || event == signalEventSurveyOff
}

// sameUTCDay is the survey rate limit's calendar: signal CreatedAt stamps are
// UTC (memory.go appendEntry), so the daily budget resets at UTC midnight.
func sameUTCDay(a time.Time, b time.Time) bool {
	aYear, aMonth, aDay := a.UTC().Date()
	bYear, bMonth, bDay := b.UTC().Date()
	return aYear == bYear && aMonth == bMonth && aDay == bDay
}

// evaluateSurveyTasteRules enforces the spec's server-side taste rules (§5
// "Surveys: garnish, not a surface"): max 1 stored survey per user per UTC
// day; never two surveys for the same package+stage combination; suppressed
// when implicit signal volume already answers the question. Pure store reads,
// zero model calls — surveys stay fully keyless like every other signal.
// Check order matters: dedupe wins over the rate limit so a re-tap of an
// already-answered chip stays an idempotent 200 instead of a scary 429.
func evaluateSurveyTasteRules(store *meetingMemoryStore, actor string, artifactID string, packageID string, stage string, now time.Time) surveyTasteOutcome {
	actor = strings.TrimSpace(actor)
	artifactID = strings.TrimSpace(artifactID)
	packageID = strings.TrimSpace(packageID)
	stage = strings.TrimSpace(stage)

	duplicate := false
	storedToday := false
	implicitVolume := 0
	for _, entry := range store.entriesOfKind(meetingMemoryKindSignal, 0) {
		record, ok := decodeSignalEntry(entry)
		if !ok {
			continue
		}
		if !isSurveySignalEvent(record.Event) {
			// Implicit seam referencing this artifact — the volume that makes
			// an explicit ask redundant.
			if artifactID != "" && record.ArtifactID == artifactID {
				implicitVolume++
			}
			continue
		}
		// Stored surveys (suppressed ones included: the chip WAS answered)
		// feed both dedupe and the daily budget.
		if packageID != "" && record.PackageID == packageID && record.Payload["stage"] == stage {
			duplicate = true
		}
		if actor != "" && record.Actor == actor && sameUTCDay(entry.CreatedAt, now) {
			storedToday = true
		}
	}

	switch {
	case duplicate:
		return surveyOutcomeDuplicate
	case storedToday:
		return surveyOutcomeRateLimited
	case implicitVolume >= signalSurveyImplicitVolumeThreshold:
		return surveyOutcomeSuppressed
	default:
		return surveyOutcomeStore
	}
}

// signalSurveyHandler is POST /signals/survey — the two-chip micro-survey
// (§5, Wave 2 item 11). Session-gated exactly like its /artifacts neighbors;
// registered beside them in main.go. Records survey_landed (positive) or
// survey_off (negative) via recordSignal with the note truncated to one line
// and the artifact's toolTemplate/packageId/stage pulled in for free context.
// Zero model calls: surveys work KEYLESS, always.
func signalSurveyHandler(w http.ResponseWriter, r *http.Request) {
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
	if kanbanApp == nil || kanbanApp.memory == nil {
		writeAuthError(w, http.StatusServiceUnavailable, "surveys are unavailable")
		return
	}

	payload := struct {
		ArtifactID string `json:"artifactId"`
		Verdict    string `json:"verdict"`
		Note       string `json:"note"`
	}{}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&payload); err != nil {
		writeAuthError(w, http.StatusBadRequest, "could not read survey request")
		return
	}

	event := ""
	valence := ""
	switch strings.ToLower(strings.TrimSpace(payload.Verdict)) {
	case surveyVerdictLanded:
		event = signalEventSurveyLanded
		valence = signalValencePositive
	case surveyVerdictOff:
		event = signalEventSurveyOff
		valence = signalValenceNegative
	default:
		writeAuthError(w, http.StatusBadRequest, "verdict must be landed or off")
		return
	}

	artifact, found := authorizedArtifactForActions(r.Context(), user, strings.TrimSpace(payload.ArtifactID), ACLReadContent, ACLWrite)
	if !found {
		writeAuthError(w, http.StatusNotFound, "artifact not found")
		return
	}

	// The dedupe key is the package's CURRENT stage: a package that advanced
	// since its last survey has genuinely new work to react to.
	packageID := strings.TrimSpace(artifact.Metadata["packageId"])
	stage := ""
	if packageID != "" {
		if record, ok := kanbanApp.venturePackageByID(packageID); ok {
			stage = strings.TrimSpace(record.Stage)
		}
	}

	outcome := evaluateSurveyTasteRules(kanbanApp.memory, user.Name, artifact.ID, packageID, stage, time.Now().UTC())
	switch outcome {
	case surveyOutcomeDuplicate:
		writeAuthJSON(w, http.StatusOK, map[string]any{
			"ok":     true,
			"stored": false,
			"reason": "package stage already surveyed",
		})
		return
	case surveyOutcomeRateLimited:
		writeAuthError(w, http.StatusTooManyRequests, "one survey per day — the implicit signals carry the rest")
		return
	}

	surveyPayload := map[string]string{}
	// Truncated here to the one-line budget (recordSignal's 500-byte cap is
	// the store-wide backstop, not the survey contract).
	if note := truncateAgentThreadText(payload.Note, signalSurveyNoteLimit); note != "" {
		surveyPayload["note"] = note
	}
	if toolTemplate := strings.TrimSpace(artifact.Metadata["toolTemplate"]); toolTemplate != "" {
		surveyPayload["toolTemplate"] = toolTemplate
	}
	if stage != "" {
		surveyPayload["stage"] = stage
	}
	suppressed := outcome == surveyOutcomeSuppressed
	if suppressed {
		surveyPayload["suppressed"] = "true"
	}

	if _, err := recordSignal(kanbanApp.memory, user.Name, event, valence, artifact.ID, packageID, surveyPayload); err != nil {
		log.Errorf("Failed to record %s survey for %q: %v", event, artifact.ID, err)
		writeAuthError(w, http.StatusInternalServerError, "could not save the survey")
		return
	}

	writeAuthJSON(w, http.StatusOK, map[string]any{
		"ok":         true,
		"stored":     true,
		"suppressed": suppressed,
	})
}
