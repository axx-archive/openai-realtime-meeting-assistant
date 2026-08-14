package main

// meeting_records.go projects the existing durable meeting directory,
// principal-filtered transcript sources, and current cumulative digest into a
// closed permanent-record contract. It is deliberately read-only: the meeting
// record never becomes a second summary/source-of-truth and never calls a
// model. If an analysis anchor no longer resolves through the requester's
// current recall authority, its prose is withheld and reported only as an
// unavailable-source count.

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	meetingRecordContractVersion = "meeting-record-v1"
	meetingRecordIndexLimit      = 60
	meetingRecordTranscriptLimit = 100
	meetingRecordTranscriptMax   = 250
)

const meetingRecordDigestSourceRevisionsMetadataKey = "meetingRecordSourceRevisions"

type meetingRecordSourceRef struct {
	SegmentID       string `json:"segmentId"`
	Revision        string `json:"revision"`
	Speaker         string `json:"speaker,omitempty"`
	At              string `json:"at"`
	CorrectionState string `json:"correctionState"`
}

type meetingRecordClaim struct {
	Kind         string                   `json:"kind"`
	Text         string                   `json:"text"`
	Owner        string                   `json:"owner,omitempty"`
	OwnerState   string                   `json:"ownerState,omitempty"`
	DueState     string                   `json:"dueState,omitempty"`
	WorkState    string                   `json:"workState,omitempty"`
	ProjectState string                   `json:"projectState,omitempty"`
	Work         []meetingRecordReference `json:"work,omitempty"`
	Projects     []meetingRecordReference `json:"projects,omitempty"`
	Status       string                   `json:"status"`
	Sources      []meetingRecordSourceRef `json:"sources"`
	Importance   int                      `json:"importance,omitempty"`
}

type meetingRecordTranscriptSegment struct {
	ID              string `json:"id"`
	Revision        string `json:"revision"`
	Speaker         string `json:"speaker,omitempty"`
	At              string `json:"at"`
	Text            string `json:"text"`
	Source          string `json:"source"`
	CaptureSequence uint64 `json:"captureSequence,omitempty"`
	CorrectionState string `json:"correctionState"`
}

type meetingRecordTranscriptPage struct {
	Segments   []meetingRecordTranscriptSegment `json:"segments"`
	NextCursor string                           `json:"nextCursor,omitempty"`
	HasMore    bool                             `json:"hasMore"`
	Query      string                           `json:"query,omitempty"`
}

type meetingRecordCoverage struct {
	State             string   `json:"state"`
	TranscriptCount   int      `json:"transcriptCount"`
	TranscriptThrough string   `json:"transcriptThrough,omitempty"`
	AnalysisThrough   string   `json:"analysisThrough,omitempty"`
	UnavailableClaims int      `json:"unavailableClaims"`
	Gaps              []string `json:"gaps"`
	ListenOnly        bool     `json:"listenOnly"`
}

type meetingRecordReference struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Kind     string `json:"kind"`
	OpenKind string `json:"openKind,omitempty"`
	OpenID   string `json:"openId,omitempty"`
}

type meetingRecordIndexItem struct {
	Contract        string   `json:"contract"`
	ID              string   `json:"id"`
	RoomID          string   `json:"roomId"`
	Title           string   `json:"title"`
	OutcomePreview  string   `json:"outcomePreview"`
	RecordRevision  string   `json:"recordRevision"`
	StartedAt       string   `json:"startedAt"`
	EndedAt         string   `json:"endedAt,omitempty"`
	Active          bool     `json:"active"`
	DurationSeconds int64    `json:"durationSeconds"`
	Participants    []string `json:"participants"`
	CoverageState   string   `json:"coverageState"`
	DecisionCount   int      `json:"decisionCount"`
	CommitmentCount int      `json:"commitmentCount"`
	UnresolvedCount int      `json:"unresolvedCount"`
	TranscriptCount int      `json:"transcriptCount"`
}

type meetingRecordDetail struct {
	meetingRecordIndexItem
	ExecutiveRecap []meetingRecordClaim        `json:"executiveRecap"`
	NeedsToKnow    []meetingRecordClaim        `json:"needsToKnow"`
	Decisions      []meetingRecordClaim        `json:"decisions"`
	Commitments    []meetingRecordClaim        `json:"commitments"`
	Blockers       []meetingRecordClaim        `json:"blockers"`
	People         []string                    `json:"people"`
	Work           []meetingRecordReference    `json:"work"`
	Projects       []meetingRecordReference    `json:"projects"`
	Artifacts      []meetingRecordReference    `json:"artifacts"`
	Coverage       meetingRecordCoverage       `json:"coverage"`
	Transcript     meetingRecordTranscriptPage `json:"transcript"`
}

type meetingRecordProjection struct {
	record                meetingRecord
	index                 meetingRecordIndexItem
	digest                meetingMemoryEntry
	payload               meetingDigestPayload
	hasDigest             bool
	digestSourceRevisions map[string]string
	segments              []meetingRecordTranscriptSegment
	segmentByID           map[string]meetingRecordTranscriptSegment
	unavailable           int
	legacyDetail          *meetingMemoryDetail
}

type meetingRecordReferenceProjection struct {
	Work      []meetingRecordReference
	Projects  []meetingRecordReference
	Artifacts []meetingRecordReference
	Claims    map[string]meetingRecordClaimReferences
}

type meetingRecordClaimReferences struct {
	Work     []meetingRecordReference
	Projects []meetingRecordReference
}

func meetingRecordTranscriptRevision(entry meetingMemoryEntry) string {
	bodyDigest := strings.TrimSpace(entry.BodyDigest)
	if bodyDigest == "" {
		bodyDigest = sha256Hex([]byte(entry.Text))
	}
	return temporalDigest(strings.Join([]string{
		meetingRecordContractVersion,
		entry.ID,
		entry.CreatedAt.UTC().Format(time.RFC3339Nano),
		bodyDigest,
		strings.TrimSpace(entry.Metadata["speaker"]),
		strings.TrimSpace(entry.Metadata["captureSequence"]),
		strings.TrimSpace(entry.Metadata["mediaGeneration"]),
		strings.TrimSpace(entry.Metadata["source"]),
		meetingRecordCorrectionState(entry),
		strings.TrimSpace(entry.Metadata["supersedesId"]),
	}, "\x00"))
}

func meetingRecordCorrectionState(entry meetingMemoryEntry) string {
	state := strings.ToLower(strings.TrimSpace(entry.Metadata["correctionState"]))
	switch state {
	case "corrected":
		return "corrected"
	case "withdrawn", "deleted", "superseded":
		return state
	default:
		return "current"
	}
}

func meetingRecordTranscriptText(entry meetingMemoryEntry) string {
	text := strings.TrimSpace(entry.Text)
	speaker := strings.TrimSpace(entry.Metadata["speaker"])
	if speaker != "" {
		prefix := speaker + ":"
		if strings.HasPrefix(strings.ToLower(text), strings.ToLower(prefix)) {
			text = strings.TrimSpace(text[len(prefix):])
		}
	}
	return text
}

func meetingRecordSegments(entries []meetingMemoryEntry, meetingID string) []meetingRecordTranscriptSegment {
	superseded := map[string]struct{}{}
	for _, entry := range entries {
		if entry.Kind == meetingMemoryKindTranscript && strings.TrimSpace(entry.Metadata["meetingId"]) == meetingID {
			if prior := strings.TrimSpace(entry.Metadata["supersedesId"]); prior != "" {
				superseded[prior] = struct{}{}
			}
		}
	}
	segments := make([]meetingRecordTranscriptSegment, 0)
	for _, entry := range entries {
		if entry.Kind != meetingMemoryKindTranscript || strings.TrimSpace(entry.Metadata["meetingId"]) != meetingID || memoryEntryHiddenFromRecall(entry) {
			continue
		}
		state := meetingRecordCorrectionState(entry)
		if _, replaced := superseded[entry.ID]; replaced || state == "withdrawn" || state == "deleted" || state == "superseded" {
			continue
		}
		sequence, _ := entryCaptureSequence(entry)
		source := strings.TrimSpace(entry.Metadata["source"])
		if source == "" {
			source = "transcript"
		}
		segments = append(segments, meetingRecordTranscriptSegment{
			ID: entry.ID, Revision: meetingRecordTranscriptRevision(entry), Speaker: strings.TrimSpace(entry.Metadata["speaker"]),
			At: entry.CreatedAt.UTC().Format(time.RFC3339Nano), Text: meetingRecordTranscriptText(entry), Source: source,
			CaptureSequence: sequence, CorrectionState: state,
		})
	}
	sort.SliceStable(segments, func(left, right int) bool {
		if segments[left].CaptureSequence > 0 && segments[right].CaptureSequence > 0 && segments[left].CaptureSequence != segments[right].CaptureSequence {
			return segments[left].CaptureSequence < segments[right].CaptureSequence
		}
		if segments[left].At != segments[right].At {
			return segments[left].At < segments[right].At
		}
		return segments[left].ID < segments[right].ID
	})
	return segments
}

func meetingRecordDigestSourceRevisionMetadata(payload meetingDigestPayload, segments []meetingRecordTranscriptSegment) string {
	byID := make(map[string]string, len(segments))
	for _, segment := range segments {
		byID[segment.ID] = segment.Revision
	}
	anchors := []string{}
	for _, topic := range payload.Topics {
		anchors = append(anchors, topic.Anchor)
	}
	for _, decision := range payload.Decisions {
		anchors = append(anchors, decision.Anchor)
	}
	for _, action := range payload.ActionItems {
		anchors = append(anchors, action.Anchor)
	}
	for _, question := range payload.OpenQuestions {
		anchors = append(anchors, question.Anchor)
	}
	bound := map[string]string{}
	for _, anchor := range anchors {
		anchor = strings.TrimSpace(anchor)
		if revision := byID[anchor]; anchor != "" && revision != "" {
			bound[anchor] = revision
		}
	}
	encoded, _ := json.Marshal(bound)
	return string(encoded)
}

func parseMeetingRecordDigestSourceRevisions(entry meetingMemoryEntry) map[string]string {
	revisions := map[string]string{}
	if json.Unmarshal([]byte(strings.TrimSpace(entry.Metadata[meetingRecordDigestSourceRevisionsMetadataKey])), &revisions) != nil {
		return map[string]string{}
	}
	return revisions
}

func (projection *meetingRecordProjection) groundedSource(anchor string) ([]meetingRecordSourceRef, bool) {
	segments := projection.segmentByID
	segment, ok := segments[strings.TrimSpace(anchor)]
	if !ok || projection.digestSourceRevisions[segment.ID] != segment.Revision {
		return []meetingRecordSourceRef{}, false
	}
	return []meetingRecordSourceRef{{
		SegmentID: segment.ID, Revision: segment.Revision, Speaker: segment.Speaker, At: segment.At, CorrectionState: segment.CorrectionState,
	}}, true
}

func meetingRecordStatus(value string) string {
	if value = strings.TrimSpace(value); value != "" {
		return value
	}
	return "unresolved"
}

func (projection *meetingRecordProjection) topicClaim(topic meetingDigestTopic) (meetingRecordClaim, bool) {
	sources, ok := projection.groundedSource(topic.Anchor)
	if !ok || strings.TrimSpace(topic.T) == "" {
		return meetingRecordClaim{}, false
	}
	return meetingRecordClaim{Kind: "topic", Text: strings.TrimSpace(topic.T), Status: "current", Sources: sources, Importance: topic.Importance}, true
}

func (projection *meetingRecordProjection) decisionClaim(decision meetingDigestDecision) (meetingRecordClaim, bool) {
	sources, ok := projection.groundedSource(decision.Anchor)
	if !ok || strings.TrimSpace(decision.D) == "" {
		return meetingRecordClaim{}, false
	}
	owner := strings.TrimSpace(decision.By)
	ownerState := "resolved"
	if owner == "" {
		ownerState = "unresolved"
	}
	return meetingRecordClaim{Kind: "decision", Text: strings.TrimSpace(decision.D), Owner: owner, OwnerState: ownerState,
		Status: meetingRecordStatus(decision.Status), Sources: sources, Importance: decision.Importance}, true
}

func (projection *meetingRecordProjection) actionClaim(action meetingDigestAction) (meetingRecordClaim, bool) {
	sources, ok := projection.groundedSource(action.Anchor)
	if !ok || strings.TrimSpace(action.A) == "" {
		return meetingRecordClaim{}, false
	}
	owner := strings.TrimSpace(action.Owner)
	ownerState := "resolved"
	if owner == "" {
		ownerState = "unresolved"
	}
	// The current digest schema does not carry a due date. Do not infer one.
	return meetingRecordClaim{Kind: "commitment", Text: strings.TrimSpace(action.A), Owner: owner, OwnerState: ownerState,
		DueState: "unresolved", WorkState: "unresolved", ProjectState: "unresolved",
		Status: meetingRecordStatus(action.Status), Sources: sources, Importance: action.Importance}, true
}

func (projection *meetingRecordProjection) questionClaim(question meetingDigestQuestion) (meetingRecordClaim, bool) {
	sources, ok := projection.groundedSource(question.Anchor)
	if !ok || strings.TrimSpace(question.Q) == "" {
		return meetingRecordClaim{}, false
	}
	return meetingRecordClaim{Kind: "unresolved_question", Text: strings.TrimSpace(question.Q), Status: "unresolved", Sources: sources, Importance: question.Importance}, true
}

func meetingRecordDuration(record meetingRecord, now time.Time) int64 {
	started, err := time.Parse(time.RFC3339Nano, record.StartedAt)
	if err != nil {
		return 0
	}
	end := now
	if strings.TrimSpace(record.EndedAt) != "" {
		if parsed, parseErr := time.Parse(time.RFC3339Nano, record.EndedAt); parseErr == nil {
			end = parsed
		}
	}
	if elapsed := end.Sub(started); elapsed > 0 {
		return int64(elapsed / time.Second)
	}
	return 0
}

func meetingRecordHonestTitle(record meetingRecord, payload meetingDigestPayload, hasGroundedDigest bool) string {
	if hasGroundedDigest {
		if title := strings.TrimSpace(payload.Title); title != "" {
			return title
		}
	}
	// The meeting directory is durable identity, not viewer authority. A custom
	// directory title can contain participant or topic information that is not
	// present in the viewer's current source projection, so only a grounded
	// digest may supply a semantic title.
	if started, err := time.Parse(time.RFC3339Nano, record.StartedAt); err == nil {
		return "Meeting · " + started.Local().Format("Jan 2, 2006 at 3:04 PM")
	}
	return "Meeting"
}

func meetingRecordCoverageForProjection(projection *meetingRecordProjection) meetingRecordCoverage {
	coverage := meetingRecordCoverage{State: coverageLabelUnknown, TranscriptCount: len(projection.segments), Gaps: []string{}, ListenOnly: projection.record.ListenOnly, UnavailableClaims: projection.unavailable}
	if len(projection.segments) > 0 {
		coverage.TranscriptThrough = projection.segments[len(projection.segments)-1].At
	}
	if projection.hasDigest {
		coverage.State = firstNonEmptyString(strings.TrimSpace(projection.digest.Metadata[digestCoverageMetadataKey]), coverageLabelUnknown)
		coverage.AnalysisThrough = strings.TrimSpace(projection.digest.Metadata[digestSpanEndMetadataKey])
	} else if len(projection.segments) == 0 {
		coverage.State = "no_transcript"
	} else {
		coverage.State = "catching_up"
	}
	switch coverage.State {
	case coverageLabelPartialLateStart:
		coverage.Gaps = append(coverage.Gaps, "The opening may be missing from the authorized transcript.")
	case coverageLabelPartialGaps:
		coverage.Gaps = append(coverage.Gaps, "One or more long transcript intervals may be missing.")
	case coverageLabelPartialSynthesis:
		coverage.Gaps = append(coverage.Gaps, "The transcript was captured, but analysis did not cover every interval.")
	case coverageLabelUnknown:
		coverage.Gaps = append(coverage.Gaps, "Source coverage could not be verified.")
	case "no_transcript":
		coverage.Gaps = append(coverage.Gaps, "No authorized transcript is available.")
	case "catching_up":
		coverage.Gaps = append(coverage.Gaps, "The transcript is available, but analysis is still catching up.")
	}
	if projection.record.ListenOnly {
		coverage.Gaps = append(coverage.Gaps, "This was a listen-only sitting; conversation may predate capture.")
	}
	if projection.unavailable > 0 {
		coverage.Gaps = append(coverage.Gaps, fmt.Sprintf("%d analyzed item(s) were withheld because their current source could not be resolved.", projection.unavailable))
	}
	return coverage
}

func meetingRecordRevision(record meetingRecord, digest meetingMemoryEntry, segments []meetingRecordTranscriptSegment) string {
	// Directory title and participant names are identity metadata, not an
	// independent disclosure grant. Keep them out of the viewer-visible
	// revision digest so an unauthorized directory-only change cannot become a
	// hash oracle; authorized transcript speakers and analysis are already bound
	// below through exact segment/digest revisions.
	parts := []string{meetingRecordContractVersion, record.ID, meetingRoomID(record), record.StartedAt, record.EndedAt, strconv.FormatBool(record.ListenOnly)}
	if digest.ID != "" {
		digestBody := strings.TrimSpace(digest.BodyDigest)
		if digestBody == "" {
			digestBody = sha256Hex([]byte(digest.Text))
		}
		parts = append(parts, digest.ID, digestBody, digest.CreatedAt.UTC().Format(time.RFC3339Nano))
	}
	for _, segment := range segments {
		parts = append(parts, segment.ID, segment.Revision)
	}
	return temporalDigest(strings.Join(parts, "\x00"))
}

func newMeetingRecordProjection(record meetingRecord, entries []meetingMemoryEntry, legacyDetail *meetingMemoryDetail, now time.Time) *meetingRecordProjection {
	projection := &meetingRecordProjection{record: record, segments: meetingRecordSegments(entries, record.ID), segmentByID: map[string]meetingRecordTranscriptSegment{}, legacyDetail: legacyDetail}
	for _, segment := range projection.segments {
		projection.segmentByID[segment.ID] = segment
	}
	for _, entry := range entries {
		if entry.Kind != meetingMemoryKindMeetingDigest || strings.TrimSpace(entry.Metadata["meetingId"]) != record.ID || !digestEntryCurrent(entry) || memoryEntryHiddenFromRecall(entry) {
			continue
		}
		payload, ok := parseMeetingDigest(entry.Text)
		if !ok {
			continue
		}
		if !projection.hasDigest || projection.digest.CreatedAt.Before(entry.CreatedAt) {
			projection.digest, projection.payload, projection.hasDigest = entry, payload, true
		}
	}
	if projection.hasDigest {
		projection.digestSourceRevisions = parseMeetingRecordDigestSourceRevisions(projection.digest)
	}
	groundedDigest := false
	if projection.hasDigest {
		anchors := make([]string, 0, len(projection.payload.Topics)+len(projection.payload.Decisions)+len(projection.payload.ActionItems)+len(projection.payload.OpenQuestions))
		for _, topic := range projection.payload.Topics {
			anchors = append(anchors, topic.Anchor)
		}
		for _, decision := range projection.payload.Decisions {
			anchors = append(anchors, decision.Anchor)
		}
		for _, action := range projection.payload.ActionItems {
			anchors = append(anchors, action.Anchor)
		}
		for _, question := range projection.payload.OpenQuestions {
			anchors = append(anchors, question.Anchor)
		}
		for _, anchor := range anchors {
			if _, ok := projection.groundedSource(anchor); ok {
				groundedDigest = true
			} else {
				projection.unavailable++
			}
		}
	}

	// Participant names are derived only from transcript sources the principal
	// may currently read. Directory participants and ungrounded digest attendees
	// are identity metadata, not an independent disclosure grant.
	participants := make([]string, 0)
	for _, segment := range projection.segments {
		if speaker := strings.TrimSpace(segment.Speaker); speaker != "" {
			participants, _ = unionMeetingParticipants(participants, []string{speaker})
		}
	}
	decisions, commitments, unresolved := 0, 0, 0
	if projection.hasDigest {
		for _, decision := range projection.payload.Decisions {
			if _, ok := projection.groundedSource(decision.Anchor); ok && strings.TrimSpace(decision.D) != "" {
				decisions++
			}
		}
		for _, action := range projection.payload.ActionItems {
			if _, ok := projection.groundedSource(action.Anchor); ok && strings.TrimSpace(action.A) != "" {
				commitments++
			}
		}
		for _, question := range projection.payload.OpenQuestions {
			if _, ok := projection.groundedSource(question.Anchor); ok && strings.TrimSpace(question.Q) != "" {
				unresolved++
			}
		}
	}
	coverage := meetingRecordCoverageForProjection(projection)
	outcome := "No grounded outcomes yet."
	if projection.hasDigest {
		for _, decision := range projection.payload.Decisions {
			if _, ok := projection.groundedSource(decision.Anchor); ok && strings.TrimSpace(decision.D) != "" {
				outcome = strings.TrimSpace(decision.D)
				break
			}
		}
		if outcome == "No grounded outcomes yet." {
			for _, action := range projection.payload.ActionItems {
				if _, ok := projection.groundedSource(action.Anchor); ok && strings.TrimSpace(action.A) != "" {
					outcome = strings.TrimSpace(action.A)
					break
				}
			}
		}
		if outcome == "No grounded outcomes yet." {
			for _, topic := range projection.payload.Topics {
				if _, ok := projection.groundedSource(topic.Anchor); ok && strings.TrimSpace(topic.T) != "" {
					outcome = strings.TrimSpace(topic.T)
					break
				}
			}
		}
	} else if len(projection.segments) > 0 {
		outcome = "Transcript captured · analysis is catching up."
	}
	projection.index = meetingRecordIndexItem{
		Contract: meetingRecordContractVersion, ID: record.ID, RoomID: meetingRoomID(record), Title: meetingRecordHonestTitle(record, projection.payload, groundedDigest),
		OutcomePreview: trimForStorage(outcome, meetingDetailSummaryLimit), RecordRevision: meetingRecordRevision(record, projection.digest, projection.segments),
		StartedAt: record.StartedAt, EndedAt: record.EndedAt, Active: record.EndedAt == "", DurationSeconds: meetingRecordDuration(record, now),
		Participants: participants, CoverageState: coverage.State, DecisionCount: decisions, CommitmentCount: commitments,
		UnresolvedCount: unresolved, TranscriptCount: len(projection.segments),
	}
	return projection
}

func meetingRecordClaimImportanceSort(claims []meetingRecordClaim) {
	sort.SliceStable(claims, func(left, right int) bool { return claims[left].Importance > claims[right].Importance })
}

func (projection *meetingRecordProjection) detail(references meetingRecordReferenceProjection, cursor, query, segmentID string, limit int) meetingRecordDetail {
	executive := []meetingRecordClaim{}
	needs := []meetingRecordClaim{}
	decisions := []meetingRecordClaim{}
	commitments := []meetingRecordClaim{}
	blockers := []meetingRecordClaim{}
	if projection.hasDigest {
		for _, topic := range projection.payload.Topics {
			if claim, ok := projection.topicClaim(topic); ok {
				executive = append(executive, claim)
			}
		}
		for _, decision := range projection.payload.Decisions {
			if claim, ok := projection.decisionClaim(decision); ok {
				decisions = append(decisions, claim)
				needs = append(needs, claim)
			}
		}
		for _, action := range projection.payload.ActionItems {
			if claim, ok := projection.actionClaim(action); ok {
				if linked, exists := references.Claims[strings.TrimSpace(action.Anchor)]; exists {
					claim.Work = append([]meetingRecordReference(nil), linked.Work...)
					claim.Projects = append([]meetingRecordReference(nil), linked.Projects...)
					if len(claim.Work) > 0 {
						claim.WorkState = "resolved"
					}
					if len(claim.Projects) > 0 {
						claim.ProjectState = "resolved"
					}
				}
				commitments = append(commitments, claim)
				needs = append(needs, claim)
				if strings.EqualFold(claim.Status, "blocked") {
					blockers = append(blockers, claim)
				}
			}
		}
		for _, question := range projection.payload.OpenQuestions {
			if claim, ok := projection.questionClaim(question); ok {
				blockers = append(blockers, claim)
				needs = append(needs, claim)
			}
		}
	}
	meetingRecordClaimImportanceSort(executive)
	meetingRecordClaimImportanceSort(needs)
	if len(executive) > 3 {
		executive = executive[:3]
	}
	if len(needs) > 6 {
		needs = needs[:6]
	}
	coverage := meetingRecordCoverageForProjection(projection)
	projection.index.CoverageState = coverage.State

	return meetingRecordDetail{
		meetingRecordIndexItem: projection.index,
		ExecutiveRecap:         executive, NeedsToKnow: needs, Decisions: decisions, Commitments: commitments, Blockers: blockers,
		People: append([]string(nil), projection.index.Participants...), Work: references.Work,
		Projects: references.Projects, Artifacts: references.Artifacts,
		Coverage: coverage, Transcript: meetingRecordTranscriptSlice(projection.segments, cursor, query, segmentID, limit),
	}
}

func appendMeetingRecordReference(values []meetingRecordReference, value meetingRecordReference) []meetingRecordReference {
	if strings.TrimSpace(value.ID) == "" || strings.TrimSpace(value.Title) == "" {
		return values
	}
	for _, existing := range values {
		if existing.ID == value.ID && existing.Kind == value.Kind {
			return values
		}
	}
	return append(values, value)
}

// meetingRecordReferencesForViewer resolves durable historical meeting-to-card
// links through current viewer-authorized successor truth. A retired Board card
// is never itself presented as a current Work destination: it yields a Work
// reference only when an exact authorized successor artifact exists, and that
// reference opens the artifact. Projects are shown only when the same successor
// carries an exact durable origin link. Tag/title guesses remain unavailable.
func (app *kanbanBoardApp) meetingRecordReferencesForViewer(ctx context.Context, user *userAccount, detail *meetingMemoryDetail) meetingRecordReferenceProjection {
	references := meetingRecordReferenceProjection{Work: []meetingRecordReference{}, Projects: []meetingRecordReference{}, Artifacts: []meetingRecordReference{}, Claims: map[string]meetingRecordClaimReferences{}}
	if app == nil || user == nil || detail == nil || len(detail.CardIDs) == 0 {
		return references
	}
	cards := map[string]kanbanCard{}
	for _, card := range app.snapshotState().Cards {
		cards[card.ID] = card
	}
	rows := map[string]boardCardViewerProjection{}
	for _, row := range app.boardProjectionForViewer(ctx, user).Cards {
		rows[row.CardID] = row
	}
	for _, cardID := range detail.CardIDs {
		card, exists := cards[cardID]
		if !exists || strings.TrimSpace(card.Title) == "" {
			continue
		}
		row, projected := rows[card.ID]
		if !projected {
			continue
		}
		if row.ArtifactID != "" {
			references.Work = appendMeetingRecordReference(references.Work, meetingRecordReference{
				ID: card.ID, Title: card.Title, Kind: "work", OpenKind: "artifact", OpenID: row.ArtifactID,
			})
		}
		if row.ProjectResolution == "linked" && row.ProjectID != "needs-project" {
			references.Projects = appendMeetingRecordReference(references.Projects, meetingRecordReference{ID: row.ProjectID, Title: row.ProjectTitle, Kind: "project", OpenKind: "project", OpenID: row.ProjectID})
		}
		if row.ArtifactID != "" {
			references.Artifacts = appendMeetingRecordReference(references.Artifacts, meetingRecordReference{ID: row.ArtifactID, Title: card.Title, Kind: "artifact", OpenKind: "artifact", OpenID: row.ArtifactID})
		}
	}
	for segmentID, cardIDs := range detail.ClaimCardIDs {
		claim := meetingRecordClaimReferences{Work: []meetingRecordReference{}, Projects: []meetingRecordReference{}}
		for _, cardID := range cardIDs {
			card, exists := cards[cardID]
			row, projected := rows[cardID]
			if !exists || !projected || strings.TrimSpace(card.Title) == "" {
				continue
			}
			if row.ArtifactID != "" {
				claim.Work = appendMeetingRecordReference(claim.Work, meetingRecordReference{
					ID: card.ID, Title: card.Title, Kind: "work", OpenKind: "artifact", OpenID: row.ArtifactID,
				})
			}
			if row.ProjectResolution == "linked" && row.ProjectID != "needs-project" {
				claim.Projects = appendMeetingRecordReference(claim.Projects, meetingRecordReference{ID: row.ProjectID, Title: row.ProjectTitle, Kind: "project", OpenKind: "project", OpenID: row.ProjectID})
			}
		}
		if len(claim.Work) > 0 || len(claim.Projects) > 0 {
			references.Claims[strings.TrimSpace(segmentID)] = claim
		}
	}
	return references
}

func meetingRecordTranscriptSlice(all []meetingRecordTranscriptSegment, cursor, query, segmentID string, limit int) meetingRecordTranscriptPage {
	if limit < 1 {
		limit = meetingRecordTranscriptLimit
	}
	if limit > meetingRecordTranscriptMax {
		limit = meetingRecordTranscriptMax
	}
	query = strings.TrimSpace(query)
	filtered := all
	if query != "" {
		needle := strings.ToLower(query)
		filtered = make([]meetingRecordTranscriptSegment, 0)
		for _, segment := range all {
			if strings.Contains(strings.ToLower(segment.Text), needle) || strings.Contains(strings.ToLower(segment.Speaker), needle) {
				filtered = append(filtered, segment)
			}
		}
	}
	start := 0
	if cursor = strings.TrimSpace(cursor); cursor != "" {
		for index, segment := range filtered {
			if segment.ID == cursor {
				start = index + 1
				break
			}
		}
	} else if query == "" && strings.TrimSpace(segmentID) != "" {
		for index, segment := range filtered {
			if segment.ID == strings.TrimSpace(segmentID) {
				start = index
				break
			}
		}
	}
	end := start + limit
	if end > len(filtered) {
		end = len(filtered)
	}
	segments := append([]meetingRecordTranscriptSegment(nil), filtered[start:end]...)
	page := meetingRecordTranscriptPage{Segments: segments, HasMore: end < len(filtered), Query: query}
	if page.HasMore && len(segments) > 0 {
		page.NextCursor = segments[len(segments)-1].ID
	}
	return page
}

func (app *kanbanBoardApp) meetingRecordProjectionsForPrincipal(ctx context.Context, principal RecallPrincipal, limit int, exactMeetingID string, includeDetailBodies bool) ([]*meetingRecordProjection, *meetingMemoryStore) {
	projections, store, _, _ := app.meetingRecordPageProjectionsForPrincipal(ctx, principal, limit, exactMeetingID, "", includeDetailBodies)
	return projections, store
}

func (app *kanbanBoardApp) meetingRecordPageProjectionsForPrincipal(ctx context.Context, principal RecallPrincipal, limit int, exactMeetingID, directoryCursor string, includeDetailBodies bool) ([]*meetingRecordProjection, *meetingMemoryStore, string, bool) {
	if app == nil || app.meetings == nil {
		return nil, &meetingMemoryStore{}, "", false
	}
	if limit < 1 {
		limit = meetingRecordIndexLimit
	}
	if limit > 100 {
		limit = 100
	}
	// The durable meeting directory supplies one bounded candidate page before
	// memory is touched. The metadata pass then follows only those IDs through
	// the process-local entry index, so unrelated chat/artifact history cannot
	// increase index-request work or writer-lock time.
	exactMeetingID = strings.TrimSpace(exactMeetingID)
	records := []meetingRecord{}
	directoryNextCursor := ""
	directoryHasMore := false
	if exactMeetingID != "" {
		if record, found := app.meetings.recordByID(exactMeetingID); found {
			records = append(records, record)
		}
	} else {
		records, directoryNextCursor, directoryHasMore = app.meetings.recentPage(meetingDirectoryScanLimit, directoryCursor)
	}
	candidates := make(map[string]struct{}, len(records))
	for _, record := range records {
		candidates[record.ID] = struct{}{}
	}
	metadataStore := app.meetingRecordStoreForPrincipal(ctx, principal, candidates, nil)
	entries := metadataStore.snapshot(0)
	wanted := map[string]struct{}{}
	for _, entry := range entries {
		if meetingID := strings.TrimSpace(entry.Metadata["meetingId"]); meetingID != "" {
			// A derived digest never grants its own historical visibility. At
			// least one current authorized source must remain.
			if !isMeetingDigestKind(entry.Kind) && !isUIStateMemoryKind(entry.Kind) {
				wanted[meetingID] = struct{}{}
			}
		}
	}
	selected := map[string]struct{}{}
	selectedRecords := make([]meetingRecord, 0, limit)
	for _, record := range records {
		if _, visible := wanted[record.ID]; !visible {
			continue
		}
		selected[record.ID] = struct{}{}
		selectedRecords = append(selectedRecords, record)
		if len(selectedRecords) == limit {
			break
		}
	}
	includeBody := func(kind string) bool { return includeDetailBodies || isMeetingDigestKind(kind) }
	scoped := app.meetingRecordStoreForPrincipal(ctx, principal, selected, includeBody)
	entries = scoped.snapshot(0)
	entriesByMeeting := map[string][]meetingMemoryEntry{}
	currentSources := map[string]struct{}{}
	for _, entry := range entries {
		meetingID := strings.TrimSpace(entry.Metadata["meetingId"])
		if meetingID == "" {
			continue
		}
		entriesByMeeting[meetingID] = append(entriesByMeeting[meetingID], entry)
		if !isMeetingDigestKind(entry.Kind) && !isUIStateMemoryKind(entry.Kind) {
			currentSources[meetingID] = struct{}{}
		}
	}
	legacy := map[string]*meetingMemoryDetail{}
	if includeDetailBodies {
		legacy = meetingMemoryDetailsFromStore(scoped, selected)
	}
	now := time.Now().UTC()
	projections := make([]*meetingRecordProjection, 0, limit)
	for _, record := range selectedRecords {
		// Re-check the bounded second read. A source revoked between metadata
		// selection and body hydration can never leave a directory-only row.
		if _, visible := currentSources[record.ID]; !visible {
			continue
		}
		projections = append(projections, newMeetingRecordProjection(record, entriesByMeeting[record.ID], legacy[record.ID], now))
		if len(projections) == limit {
			break
		}
	}
	if exactMeetingID != "" || len(records) == 0 {
		return projections, scoped, "", false
	}
	if len(selectedRecords) == limit {
		lastSelectedID := selectedRecords[len(selectedRecords)-1].ID
		return projections, scoped, meetingDirectoryCursorForID(lastSelectedID), directoryHasMore || lastSelectedID != records[len(records)-1].ID
	}
	return projections, scoped, directoryNextCursor, directoryHasMore
}

func parseMeetingRecordTranscriptLimit(raw string) int {
	limit := meetingRecordTranscriptLimit
	if parsed, err := strconv.Atoi(strings.TrimSpace(raw)); err == nil && parsed > 0 {
		limit = parsed
	}
	if limit > meetingRecordTranscriptMax {
		limit = meetingRecordTranscriptMax
	}
	return limit
}
