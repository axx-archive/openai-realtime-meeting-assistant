package main

// meeting_agent_context.go binds a natural-language meeting range to one
// body-free, server-signed manifest of exact Meeting Record revisions. The
// manifest is small enough to ride an approved work card, while provider
// admission always re-resolves every record under the original requester's
// current authority. Workers receive compact grounded claims and only a
// bounded transcript fallback; they never receive an unbounded meeting log.

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const (
	meetingRangeContextRefPrefix      = "meeting-range"
	meetingRangeContextSchemaVersion  = 1
	meetingRangeContextRecordCap      = 12
	meetingRangeContextDirectoryPages = 4
	meetingRangeContextTextCap        = maxPromptBodyBytes
)

type meetingRangeContextRecord struct {
	MeetingID      string `json:"meetingId"`
	RecordRevision string `json:"recordRevision"`
}

type meetingRangeContextClaims struct {
	SchemaVersion int                         `json:"schemaVersion"`
	OwnerEmail    string                      `json:"ownerEmail"`
	RangeStart    string                      `json:"rangeStart"`
	RangeEnd      string                      `json:"rangeEnd"`
	Records       []meetingRangeContextRecord `json:"records"`
	Truncated     bool                        `json:"truncated,omitempty"`
}

func meetingRangeContextMAC(payload []byte) []byte {
	mac := hmac.New(sha256.New, archiveTokenSecret())
	_, _ = mac.Write([]byte("meeting-range-context/v1\x00"))
	_, _ = mac.Write(payload)
	return mac.Sum(nil)
}

func mintMeetingRangeContextRef(claims meetingRangeContextClaims) (string, error) {
	claims.SchemaVersion = meetingRangeContextSchemaVersion
	claims.OwnerEmail = normalizeAccountEmail(claims.OwnerEmail)
	if !validMeetingRangeContextClaims(claims) {
		return "", fmt.Errorf("meeting range context is invalid")
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(meetingRangeContextMAC(payload))
	return meetingRangeContextRefPrefix + "|" + token, nil
}

func resolveMeetingRangeContextRef(ref string) (meetingRangeContextClaims, bool) {
	prefix := meetingRangeContextRefPrefix + "|"
	if !strings.HasPrefix(strings.TrimSpace(ref), prefix) {
		return meetingRangeContextClaims{}, false
	}
	parts := strings.Split(strings.TrimPrefix(strings.TrimSpace(ref), prefix), ".")
	if len(parts) != 2 {
		return meetingRangeContextClaims{}, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || len(payload) == 0 || len(payload) > 8192 {
		return meetingRangeContextClaims{}, false
	}
	provided, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || !hmac.Equal(provided, meetingRangeContextMAC(payload)) {
		return meetingRangeContextClaims{}, false
	}
	var claims meetingRangeContextClaims
	if json.Unmarshal(payload, &claims) != nil || !validMeetingRangeContextClaims(claims) {
		return meetingRangeContextClaims{}, false
	}
	return claims, true
}

func validMeetingRangeContextClaims(claims meetingRangeContextClaims) bool {
	if claims.SchemaVersion != meetingRangeContextSchemaVersion || normalizeAccountEmail(claims.OwnerEmail) == "" ||
		len(claims.Records) == 0 || len(claims.Records) > meetingRangeContextRecordCap {
		return false
	}
	start, startErr := time.Parse(time.RFC3339Nano, strings.TrimSpace(claims.RangeStart))
	end, endErr := time.Parse(time.RFC3339Nano, strings.TrimSpace(claims.RangeEnd))
	if startErr != nil || endErr != nil || !end.After(start) {
		return false
	}
	seen := map[string]struct{}{}
	for _, record := range claims.Records {
		if strings.TrimSpace(record.MeetingID) == "" || strings.TrimSpace(record.RecordRevision) == "" ||
			strings.Contains(record.MeetingID, "|") || strings.Contains(record.RecordRevision, "|") {
			return false
		}
		if _, duplicate := seen[record.MeetingID]; duplicate {
			return false
		}
		seen[record.MeetingID] = struct{}{}
	}
	return true
}

func meetingRecordOverlapsRange(record meetingRecord, start, end, now time.Time) bool {
	started, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(record.StartedAt))
	if err != nil || !started.Before(end) {
		return false
	}
	ended := now
	if raw := strings.TrimSpace(record.EndedAt); raw != "" {
		parsed, parseErr := time.Parse(time.RFC3339Nano, raw)
		if parseErr != nil {
			return false
		}
		ended = parsed
	}
	return ended.After(start)
}

func (app *kanbanBoardApp) meetingRecordsOverlappingRange(start, end, now time.Time, cap int) ([]meetingRecord, bool) {
	if app == nil || app.meetings == nil || cap < 1 {
		return nil, false
	}
	recordsInRange := make([]meetingRecord, 0, cap)
	cursor := ""
	truncated := false
	for page := 0; page < meetingRangeContextDirectoryPages; page++ {
		records, next, hasMore := app.meetings.recentPage(meetingDirectoryScanLimit, cursor)
		if len(records) == 0 {
			break
		}
		for _, record := range records {
			if !meetingRecordOverlapsRange(record, start, end, now) {
				continue
			}
			if len(recordsInRange) >= cap {
				truncated = true
				continue
			}
			recordsInRange = append(recordsInRange, record)
		}
		if !hasMore || next == "" {
			break
		}
		cursor = next
		if page == meetingRangeContextDirectoryPages-1 {
			truncated = true
		}
	}
	return recordsInRange, truncated
}

// conversationMeetingSourceRange is broader than the direct briefing route:
// it recognizes meeting-backed questions and durable work while still
// requiring an explicit time range. Scheduling or generic future-meeting
// language never binds historical Meeting Records.
func conversationMeetingSourceRange(utterance string, now time.Time) (time.Time, time.Time, bool) {
	normalized := strings.ToLower(canonicalizeBoardText(utterance))
	if normalized == "" || (!strings.Contains(normalized, "meeting") && !strings.Contains(normalized, "call")) {
		return time.Time{}, time.Time{}, false
	}
	if !scoutTextHasWord(normalized, "analyze", "analyse", "review", "compare", "summarize", "summarise", "recap", "extract", "brief", "report", "research") &&
		!strings.Contains(normalized, "catch me up") && !strings.Contains(normalized, "what happened") &&
		!strings.Contains(normalized, "what did i miss") && !strings.Contains(normalized, "anything interesting") &&
		!strings.Contains(normalized, "action items") && !strings.Contains(normalized, "key callouts") {
		return time.Time{}, time.Time{}, false
	}
	start, end, ok := relativeQueryTimeRange(normalized, now)
	return start, end, ok
}

func conversationRequestsDurableMeetingWork(utterance string) bool {
	normalized := strings.ToLower(canonicalizeBoardText(utterance))
	for _, marker := range []string{
		"create a report", "write a report", "prepare a report", "make a report", "produce a report",
		"create a brief", "write a brief", "prepare a brief", "make a brief", "produce a brief",
		"create a deck", "make a deck", "prepare a deck", "build a deck", "research pass", "deep research",
		"turn this into", "turn them into", "work on",
	} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

// meetingRangeContextRefForPrincipal selects a bounded, chronological set of
// current Meeting Records. Directory work is capped independently from the
// returned record cap, so a corrupt or extremely dense history cannot turn a
// chat request into an unbounded scan.
func (app *kanbanBoardApp) meetingRangeContextRefForPrincipal(ctx context.Context, principal RecallPrincipal, query string, now time.Time) (string, bool, error) {
	if app == nil || app.meetings == nil || principal.User == nil {
		return "", false, nil
	}
	start, end, ok := conversationMeetingSourceRange(query, now)
	if !ok {
		return "", false, nil
	}
	claims := meetingRangeContextClaims{
		OwnerEmail: normalizeAccountEmail(principal.User.Email),
		RangeStart: start.UTC().Format(time.RFC3339Nano), RangeEnd: end.UTC().Format(time.RFC3339Nano),
		Records: []meetingRangeContextRecord{},
	}
	records, truncated := app.meetingRecordsOverlappingRange(start, end, now, meetingRangeContextRecordCap*4)
	claims.Truncated = truncated
	for _, record := range records {
		projection, readable := app.meetingRecordProjectionForPrincipal(ctx, principal, record.ID)
		if !readable || projection == nil || strings.TrimSpace(projection.index.RecordRevision) == "" {
			continue
		}
		if len(claims.Records) >= meetingRangeContextRecordCap {
			claims.Truncated = true
			continue
		}
		claims.Records = append(claims.Records, meetingRangeContextRecord{MeetingID: record.ID, RecordRevision: projection.index.RecordRevision})
	}
	if len(claims.Records) == 0 {
		return "", true, nil
	}
	// The directory is newest-first; worker context is chronological.
	for left, right := 0, len(claims.Records)-1; left < right; left, right = left+1, right-1 {
		claims.Records[left], claims.Records[right] = claims.Records[right], claims.Records[left]
	}
	ref, err := mintMeetingRangeContextRef(claims)
	return ref, true, err
}

func appendMeetingRangeContextLine(lines []string, line string) ([]string, bool) {
	line = strings.TrimSpace(line)
	if line == "" {
		return lines, true
	}
	candidate := strings.Join(append(lines, line), "\n")
	if len(candidate) > meetingRangeContextTextCap {
		return lines, false
	}
	return append(lines, line), true
}

func (app *kanbanBoardApp) meetingRangeContextEntry(ctx context.Context, principal RecallPrincipal, ref string) (meetingMemoryEntry, bool) {
	claims, ok := resolveMeetingRangeContextRef(ref)
	if !ok || principal.User == nil || normalizeAccountEmail(principal.User.Email) != normalizeAccountEmail(claims.OwnerEmail) {
		return meetingMemoryEntry{}, false
	}
	lines := []string{
		"Exact authorized Meeting Record range. Treat claims as derived analysis and source anchors as the governing evidence.",
		"range_start=" + claims.RangeStart,
		"range_end=" + claims.RangeEnd,
	}
	if claims.Truncated {
		lines = append(lines, "coverage_gap=more authorized meetings existed than this bounded worker manifest could include")
	}
	for _, bound := range claims.Records {
		projection, readable := app.meetingRecordProjectionForPrincipal(ctx, principal, bound.MeetingID)
		if !readable || projection == nil || projection.index.RecordRevision != bound.RecordRevision || len(projection.segments) == 0 {
			return meetingMemoryEntry{}, false
		}
		header := fmt.Sprintf("\nMEETING title=%q meeting_id=%s record_revision=%s started_at=%s coverage=%s", projection.index.Title, projection.index.ID, projection.index.RecordRevision, projection.index.StartedAt, projection.index.CoverageState)
		var fits bool
		if lines, fits = appendMeetingRangeContextLine(lines, header); !fits {
			lines = append(lines, "[remaining exact meetings omitted from this bounded worker context]")
			break
		}
		for _, gap := range meetingRecordCoverageForProjection(projection).Gaps {
			if lines, fits = appendMeetingRangeContextLine(lines, "coverage_gap="+gap); !fits {
				break
			}
		}
		claimsAdded := 0
		unavailableClaims := 0
		if projection.hasDigest {
			for _, decision := range projection.payload.Decisions {
				claim, grounded := projection.decisionClaim(decision)
				if !grounded {
					unavailableClaims++
					continue
				}
				line := "decision: " + claim.Text
				for _, source := range claim.Sources {
					line += fmt.Sprintf(" [segment:%s revision:%s]", source.SegmentID, source.Revision)
				}
				if lines, fits = appendMeetingRangeContextLine(lines, line); !fits {
					break
				}
				claimsAdded++
			}
			for _, action := range projection.payload.ActionItems {
				claim, grounded := projection.actionClaim(action)
				if !grounded {
					unavailableClaims++
					continue
				}
				line := "commitment: " + claim.Text
				if claim.Owner != "" {
					line += " owner=" + claim.Owner
				}
				for _, source := range claim.Sources {
					line += fmt.Sprintf(" [segment:%s revision:%s]", source.SegmentID, source.Revision)
				}
				if lines, fits = appendMeetingRangeContextLine(lines, line); !fits {
					break
				}
				claimsAdded++
			}
			for _, question := range projection.payload.OpenQuestions {
				claim, grounded := projection.questionClaim(question)
				if !grounded {
					unavailableClaims++
					continue
				}
				line := "open_question: " + claim.Text
				for _, source := range claim.Sources {
					line += fmt.Sprintf(" [segment:%s revision:%s]", source.SegmentID, source.Revision)
				}
				if lines, fits = appendMeetingRangeContextLine(lines, line); !fits {
					break
				}
				claimsAdded++
			}
		}
		if unavailableClaims > 0 {
			if lines, fits = appendMeetingRangeContextLine(lines, fmt.Sprintf("analysis_gap=%d derived claims are unavailable because their exact current source revisions no longer match", unavailableClaims)); !fits {
				break
			}
		}
		// When rolling analysis has not landed yet, preserve immediate recall with
		// a small exact transcript fallback instead of inventing a summary.
		if claimsAdded == 0 {
			for _, segment := range projection.segments {
				line := fmt.Sprintf("transcript [segment:%s revision:%s] %s · %s: %s", segment.ID, segment.Revision, segment.At, firstNonEmptyString(segment.Speaker, "Unknown speaker"), trimForStorage(segment.Text, 800))
				if lines, fits = appendMeetingRangeContextLine(lines, line); !fits {
					break
				}
			}
		}
	}
	return meetingMemoryEntry{
		ID: "meeting-range-context-" + temporalDigest(ref)[:20], Kind: meetingMemoryKindMeetingDigest,
		Text: strings.Join(lines, "\n"), CreatedAt: time.Now().UTC(),
		Metadata: map[string]string{
			"title": "Meeting intelligence range", "visibility": "private", "ownerEmail": normalizeAccountEmail(principal.User.Email),
			"meetingRangeContext": "true", "rangeStart": claims.RangeStart, "rangeEnd": claims.RangeEnd,
		},
	}, true
}
