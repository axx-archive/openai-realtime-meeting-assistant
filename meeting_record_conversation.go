package main

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

const meetingRecordConversationBindingVersion = "meeting-record-conversation-v1"
const meetingRecordContextRefVersion = "meeting"

type scoutChatMeetingRecordBinding struct {
	Version        string `json:"version"`
	MeetingID      string `json:"meetingId"`
	RecordRevision string `json:"recordRevision"`
	OwnerEmail     string `json:"ownerEmail"`
	BoundAt        string `json:"boundAt"`
}

type meetingRecordConversationContext struct {
	binding  scoutChatMeetingRecordBinding
	record   *meetingRecordProjection
	segments []meetingRecordTranscriptSegment
}

func meetingRecordContextRef(meetingID, recordRevision string) string {
	meetingID, recordRevision = strings.TrimSpace(meetingID), strings.TrimSpace(recordRevision)
	if meetingID == "" || recordRevision == "" || strings.Contains(meetingID, "|") || strings.Contains(recordRevision, "|") {
		return ""
	}
	return strings.Join([]string{meetingRecordContextRefVersion, meetingID, recordRevision}, "|")
}

func (context *meetingRecordConversationContext) contextRef() string {
	if context == nil {
		return ""
	}
	return meetingRecordContextRef(context.binding.MeetingID, context.binding.RecordRevision)
}

func meetingRecordConversationThreadID(ownerEmail, meetingID, recordRevision string) string {
	digest := sha256Hex([]byte(strings.Join([]string{
		meetingRecordConversationBindingVersion,
		normalizeAccountEmail(ownerEmail),
		strings.TrimSpace(meetingID),
		strings.TrimSpace(recordRevision),
	}, "\x00")))
	return "scout-meeting-" + digest[:24]
}

func validMeetingRecordConversationBinding(thread scoutChatThreadRecord, ownerEmail, meetingID, recordRevision string) bool {
	binding := thread.MeetingRecord
	return binding != nil && binding.Version == meetingRecordConversationBindingVersion &&
		normalizeAccountEmail(binding.OwnerEmail) == normalizeAccountEmail(ownerEmail) &&
		binding.MeetingID == strings.TrimSpace(meetingID) && binding.RecordRevision == strings.TrimSpace(recordRevision) &&
		normalizeAccountEmail(thread.OwnerEmail) == normalizeAccountEmail(ownerEmail) &&
		scoutChatThreadVisibility(thread) == scoutChatVisibilityPrivate && thread.ArchivedAt == "" && !thread.Table && thread.Intake == "" &&
		strings.TrimSpace(thread.AgentID) == ""
}

func (app *kanbanBoardApp) meetingRecordProjectionForPrincipal(ctx context.Context, principal RecallPrincipal, meetingID string) (*meetingRecordProjection, bool) {
	meetingID = strings.TrimSpace(meetingID)
	if meetingID == "" {
		return nil, false
	}
	projections, _ := app.meetingRecordProjectionsForPrincipal(ctx, principal, 1, meetingID, true)
	for _, projection := range projections {
		if projection != nil && projection.index.ID == meetingID {
			return projection, true
		}
	}
	return nil, false
}

func (app *kanbanBoardApp) ensureMeetingRecordConversation(user *userAccount, projection *meetingRecordProjection) (scoutChatThreadRecord, bool, error) {
	if app == nil || app.memory == nil || user == nil || projection == nil {
		return scoutChatThreadRecord{}, false, fmt.Errorf("Meeting Record conversation is unavailable")
	}
	ownerEmail := normalizeAccountEmail(user.Email)
	meetingID := strings.TrimSpace(projection.index.ID)
	revision := strings.TrimSpace(projection.index.RecordRevision)
	if ownerEmail == "" || meetingID == "" || revision == "" {
		return scoutChatThreadRecord{}, false, fmt.Errorf("Meeting Record conversation is unavailable")
	}
	threadID := meetingRecordConversationThreadID(ownerEmail, meetingID, revision)
	lock := app.scoutChatThreadLock(threadID)
	lock.Lock()
	defer lock.Unlock()
	if entry, found := app.memory.entryByKindAndID(meetingMemoryKindScoutChat, threadID); found {
		existing, ok := decodeScoutChatThreadEntry(entry)
		if !ok || !validMeetingRecordConversationBinding(existing, ownerEmail, meetingID, revision) {
			return scoutChatThreadRecord{}, false, fmt.Errorf("Meeting Record conversation identity does not match")
		}
		return existing, false, nil
	}
	now := time.Now().UTC()
	thread := scoutChatThreadRecord{
		ID: threadID, Title: "Meeting · " + strings.TrimSpace(projection.index.Title), Preview: "Ask Scout about this meeting",
		OwnerEmail: ownerEmail, CreatedBy: scoutChatAuthorName(user), Visibility: scoutChatVisibilityPrivate,
		CreatedAt: now.Format(time.RFC3339Nano), UpdatedAt: now.Format(time.RFC3339Nano),
		MeetingRecord: &scoutChatMeetingRecordBinding{
			Version: meetingRecordConversationBindingVersion, MeetingID: meetingID, RecordRevision: revision,
			OwnerEmail: ownerEmail, BoundAt: now.Format(time.RFC3339Nano),
		},
	}
	encoded, err := encodeScoutChatThread(thread)
	if err == nil {
		_, _, err = app.memory.appendScoutChatThread(thread.ID, encoded, scoutChatThreadMetadata(thread))
	}
	if err != nil {
		return scoutChatThreadRecord{}, false, err
	}
	return thread, true, nil
}

func (app *kanbanBoardApp) currentMeetingRecordConversationContext(ctx context.Context, user *userAccount, thread scoutChatThreadRecord) (*meetingRecordConversationContext, error) {
	if user == nil || thread.MeetingRecord == nil || !validMeetingRecordConversationBinding(thread, user.Email, thread.MeetingRecord.MeetingID, thread.MeetingRecord.RecordRevision) {
		return nil, fmt.Errorf("Meeting Record conversation binding is unavailable")
	}
	projection, ok := app.meetingRecordProjectionForPrincipal(ctx, recallPrincipalForUser(user), thread.MeetingRecord.MeetingID)
	if !ok || projection.index.RecordRevision != thread.MeetingRecord.RecordRevision || len(projection.segments) == 0 {
		return nil, fmt.Errorf("Meeting Record revision is unavailable")
	}
	binding := *thread.MeetingRecord
	return &meetingRecordConversationContext{binding: binding, record: projection, segments: append([]meetingRecordTranscriptSegment(nil), projection.segments...)}, nil
}

func (app *kanbanBoardApp) meetingRecordConversationBindingCurrent(viewerEmail string, thread scoutChatThreadRecord) bool {
	if thread.MeetingRecord == nil {
		return true
	}
	user := accountStore().findUser(normalizeAccountEmail(viewerEmail))
	if user == nil {
		return false
	}
	_, err := app.currentMeetingRecordConversationContext(context.Background(), user, thread)
	return err == nil
}

func meetingRecordConversationQueryTerms(query string) map[string]struct{} {
	terms := map[string]struct{}{}
	for _, term := range normalizeForGrounding(query) {
		if len(term) < 3 {
			continue
		}
		if _, stop := sourceStopwords[term]; !stop {
			terms[term] = struct{}{}
		}
	}
	return terms
}

func selectMeetingRecordConversationSegments(segments []meetingRecordTranscriptSegment, query string, limit int) []meetingRecordTranscriptSegment {
	if limit < 1 {
		limit = 24
	}
	type scored struct {
		segment meetingRecordTranscriptSegment
		score   int
		index   int
	}
	terms := meetingRecordConversationQueryTerms(query)
	rows := make([]scored, 0, len(segments))
	for index, segment := range segments {
		score := 0
		for _, word := range normalizeForGrounding(segment.Speaker + " " + segment.Text) {
			if _, matched := terms[word]; matched {
				score++
			}
		}
		rows = append(rows, scored{segment: segment, score: score, index: index})
	}
	sort.SliceStable(rows, func(left, right int) bool {
		if rows[left].score != rows[right].score {
			return rows[left].score > rows[right].score
		}
		return rows[left].index > rows[right].index
	})
	if len(rows) > limit {
		rows = rows[:limit]
	}
	sort.SliceStable(rows, func(left, right int) bool { return rows[left].index < rows[right].index })
	selected := make([]meetingRecordTranscriptSegment, 0, len(rows))
	for _, row := range rows {
		selected = append(selected, row.segment)
	}
	return selected
}

func (context *meetingRecordConversationContext) modelQuery(question string) string {
	if context == nil || context.record == nil {
		return strings.TrimSpace(question)
	}
	segments := selectMeetingRecordConversationSegments(context.segments, question, 24)
	var source strings.Builder
	source.WriteString("Exact Meeting Record transcript context. Use only these transcript segments; do not use Board, Files, general memory, or other conversations. ")
	source.WriteString("Begin supported answers with 'Transcript:' and cite every factual statement with [segment:SEGMENT_ID]. ")
	source.WriteString("If the authorized intervals do not answer the question, say unavailable. Do not create work, publish, or widen the audience.\n")
	fmt.Fprintf(&source, "meeting_id=%s\nrecord_revision=%s\ncoverage=%s\n", context.binding.MeetingID, context.binding.RecordRevision, context.record.index.CoverageState)
	for _, gap := range meetingRecordCoverageForProjection(context.record).Gaps {
		fmt.Fprintf(&source, "coverage_gap=%s\n", gap)
	}
	source.WriteString("TRANSCRIPT TRUTH\n")
	for _, segment := range segments {
		text := trimForStorage(segment.Text, 1600)
		fmt.Fprintf(&source, "[segment:%s] %s · %s: %s\n", segment.ID, segment.At, firstNonEmptyString(segment.Speaker, "Unknown speaker"), text)
	}
	source.WriteString("END TRANSCRIPT\nQuestion: ")
	source.WriteString(strings.TrimSpace(question))
	return source.String()
}

var meetingRecordSegmentCitationPattern = regexp.MustCompile(`(?i)\[segment:([^\]\s]{1,200})\]`)

func (context *meetingRecordConversationContext) groundAnswer(answer string, limit int) []answerSource {
	if context == nil || limit == 0 {
		return []answerSource{}
	}
	byID := make(map[string]meetingRecordTranscriptSegment, len(context.segments))
	for _, segment := range context.segments {
		byID[segment.ID] = segment
	}
	sources := make([]answerSource, 0, limit)
	seen := map[string]struct{}{}
	appendSegment := func(segment meetingRecordTranscriptSegment, quote string) {
		if len(sources) >= limit {
			return
		}
		if _, duplicate := seen[segment.ID]; duplicate {
			return
		}
		seen[segment.ID] = struct{}{}
		sources = append(sources, answerSource{Kind: "meeting_transcript", MeetingID: context.binding.MeetingID,
			SegmentID: segment.ID, Revision: segment.Revision, At: segment.At, Author: segment.Speaker, Quote: quote})
	}
	for _, match := range meetingRecordSegmentCitationPattern.FindAllStringSubmatch(answer, -1) {
		if segment, ok := byID[match[1]]; ok {
			appendSegment(segment, trimForStorage(segment.Text, 180))
		}
	}
	if len(sources) > 0 {
		return sources
	}
	answerWords := normalizeForGrounding(answer)
	for _, segment := range context.segments {
		run, content := longestSharedRun(answerWords, normalizeForGrounding(segment.Text))
		if content >= 3 {
			appendSegment(segment, strings.Join(run, " "))
		}
	}
	return sources
}
