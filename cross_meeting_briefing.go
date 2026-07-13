package main

// Cross-meeting briefing + anchor drill-down (Track-2 Wave 6).
//
// crossMeetingBriefingTool (tool cross_meeting_briefing) answers "what did I
// miss this week / since Monday" with a comprehensive, organized, day-by-day
// briefing spanning EVERY meeting in the range. Composition is DETERMINISTIC
// — the office_brief.go composition-over-snapshots doctrine per amendment A9:
// the current in-range meeting digests are regrouped per local calendar day
// through the SAME foldDayDigest the day-digest producer runs (amendment A6:
// one shared fold, with the producers' precomputed rollups acting as its
// cache), ranked importance-first (amendment A4), and merged with the ACTIVE
// decision ledger VERBATIM (decisions are records, never re-summarized). When
// the range has no digest coverage (pre-backfill, digests disabled, or a
// range older than the backfill window) the tool degrades to
// buildComprehensiveBriefing (recall_mapreduce.go) — an on-demand map-reduce
// over raw brains/transcripts — never to keyword scraps.
//
// composeCrossMeetingBriefing returns STRUCTURED sections (a callable Go
// function, not only a chat answer) so a later wave can slot the briefing
// into the Morning Brief surface; office_brief.go itself is deliberately
// untouched in this build (amendment A9).
//
// getMeetingDetail (tool get_meeting_detail) is the drill-down tier walk for
// one past meeting: its current digest, its newest brain write-ups, and — on
// explicit anchor request — the verbatim transcript exchange via
// transcriptWindowAround.
//
// The tools deliberately do NOT force a fresh digest pass (the meeting_recap
// 60s-brain-pass pattern): briefings read the rollup tiers, the primary
// recall lane already backfills the newest raw entries for recency, and the
// map-reduce fallback covers missing coverage — forcing the 8-pass ambient
// chain inline would trade minutes of latency for marginal freshness.
//
// PERSONALIZATION HOOK (noted, deliberately NOT built — amendments non-goal):
// the requester's identity is available at every call site (requesterEmail on
// the private voice path, requester on the chat path); a per-person briefing
// ("what did *I* commit to") would filter/re-rank these same structured
// sections by that identity. Follow-up, not this build.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const (
	// crossMeetingBriefingMaxDays bounds the day enumeration for pathological
	// ranges; every phrase relativeQueryTimeRange produces fits well inside.
	crossMeetingBriefingMaxDays = 31
	// briefingLedgerDecisionsPerDayCap bounds the verbatim decision-ledger
	// lines injected per day.
	briefingLedgerDecisionsPerDayCap = 12

	// Render caps (amendment A4: lead by importance, de-emphasize chatter).
	// The STRUCTURED payload keeps every folded fact; only the rendered text
	// trims each section, announcing the overflow.
	briefingRenderDecisionCap = 8
	briefingRenderActionCap   = 8
	briefingRenderTopicCap    = 6
	briefingRenderQuestionCap = 5
	briefingRenderThemeCap    = 6

	// getMeetingDetail bounds.
	meetingDetailBrainCap     = 4
	meetingDetailAnchorRadius = 3

	// briefing sources, stamped on the result for provenance.
	briefingSourceDigests   = "digests"
	briefingSourceMapReduce = "map_reduce"
)

// briefingDay is one local calendar day of a cross-meeting briefing: the
// deterministic day fold (meetings, decisions, topics, action items, open
// questions, themes — each fact carrying meeting provenance + importance)
// plus the day's active decision-ledger statements verbatim.
type briefingDay struct {
	Day             string
	Fold            dayDigestPayload
	HasFold         bool
	LedgerDecisions []string
}

// crossMeetingBriefingResult is the structured briefing (amendment A9: a
// callable Go function returning sections, so the Morning Brief surface can
// consume it later without re-parsing prose).
type crossMeetingBriefingResult struct {
	RangeStart time.Time
	RangeEnd   time.Time
	Days       []briefingDay
	// DigestedMeetings counts distinct meetings that contributed folded
	// facts; zero means the range has no digest coverage and the caller
	// should try the map-reduce fallback.
	DigestedMeetings int
	Source           string
	// OmittedMeetings counts in-range meetings the map-reduce cap skipped.
	OmittedMeetings int
	// MeetingCoverage maps a meeting id to the server-stamped coverage read of
	// its contributing digest (kanban-card-107) — how completely the captured
	// span covered the sitting. Rendering and the tool payload consult it so a
	// briefing never implies it saw a whole meeting it only partly captured.
	MeetingCoverage map[string]meetingCoverageSummary
}

func (briefing crossMeetingBriefingResult) empty() bool {
	for _, day := range briefing.Days {
		if day.HasFold || len(day.LedgerDecisions) > 0 {
			return false
		}
	}
	return true
}

// coverageSummaryLine is a one-line coverage roll-up across the meetings that
// contributed to the briefing, pinned onto the tool payload so a caller can
// state up front how complete the captured picture is. Degrades to an explicit
// "coverage unknown" when nothing carried a coverage stamp.
func (briefing crossMeetingBriefingResult) coverageSummaryLine() string {
	var full, partial, synthesis, unknown, listenOnly int
	for _, summary := range briefing.MeetingCoverage {
		switch summary.Label {
		case coverageLabelFull:
			full++
		case coverageLabelUnknown:
			unknown++
		case coverageLabelPartialSynthesis:
			// Captured-but-not-summarized is called out on its own line rather than
			// folded into "partial" (a capture caveat) — a synthesis dead-letter is
			// a distinct, quieter failure the reader should be able to see (F11).
			synthesis++
		default:
			partial++
		}
		if summary.ListenOnly {
			listenOnly++
		}
	}
	total := full + partial + synthesis + unknown
	if total == 0 {
		return "coverage unknown for this range"
	}
	parts := []string{fmt.Sprintf("%d of %d meetings fully captured", full, total)}
	if partial > 0 {
		parts = append(parts, fmt.Sprintf("%d partial", partial))
	}
	if synthesis > 0 {
		parts = append(parts, fmt.Sprintf("%d not fully synthesized", synthesis))
	}
	if unknown > 0 {
		parts = append(parts, fmt.Sprintf("%d unknown", unknown))
	}
	if listenOnly > 0 {
		parts = append(parts, fmt.Sprintf("%d listen-only", listenOnly))
	}
	return strings.Join(parts, ", ")
}

// localDaysInRange enumerates the local calendar days [start, end) touch,
// oldest first, capped. The end bound is exclusive (relativeQueryTimeRange's
// convention), so a range ending at local midnight excludes that day.
func localDaysInRange(start time.Time, end time.Time) []string {
	if !end.After(start) {
		return nil
	}
	location := meetingTimeLocation()
	last := end.In(location).Add(-time.Nanosecond)
	lastDay := time.Date(last.Year(), last.Month(), last.Day(), 0, 0, 0, 0, location)
	first := start.In(location)
	day := time.Date(first.Year(), first.Month(), first.Day(), 0, 0, 0, 0, location)

	days := make([]string, 0, 8)
	for !day.After(lastDay) && len(days) < crossMeetingBriefingMaxDays {
		days = append(days, day.Format(dayBucketLayout))
		day = day.AddDate(0, 0, 1)
	}
	return days
}

// activeDecisionTextsByDay buckets the ACTIVE decision-ledger statements onto
// their local recording day — the verbatim injection both briefing paths
// share (records are quoted, never re-summarized).
func (app *kanbanBoardApp) activeDecisionTextsByDay(start time.Time, end time.Time) map[string][]string {
	if app == nil || app.memory == nil {
		return nil
	}
	byDay := map[string][]string{}
	for _, entry := range app.memory.entriesOfKind(meetingMemoryKindDecision, 0) {
		if entry.Metadata["status"] != decisionStatusActive {
			continue
		}
		if entry.CreatedAt.Before(start) || !entry.CreatedAt.Before(end) {
			continue
		}
		day := dayBucket(entry.CreatedAt)
		if len(byDay[day]) >= briefingLedgerDecisionsPerDayCap {
			continue
		}
		if text := strings.TrimSpace(entry.Text); text != "" {
			byDay[day] = append(byDay[day], text)
		}
	}
	return byDay
}

// composeBriefingFromDigests is the ONE composition path both briefing modes
// share (amendment A6): the deterministic tool feeds it the stored current
// digests, the map-reduce fallback feeds it freshly mapped synthetic ones.
// Facts land on days via foldDayDigest — the producer's own fold — so the
// briefing and the day_digest tier can never drift apart. dayDigests (stored
// rollups) only backstop days where no in-range meeting digest contributes,
// e.g. a day whose meeting digest span drifted out of the queried window.
func (app *kanbanBoardApp) composeBriefingFromDigests(rangeStart time.Time, rangeEnd time.Time, meetingDigests map[string]meetingMemoryEntry, dayDigests map[string]meetingMemoryEntry, source string, omittedMeetings int) crossMeetingBriefingResult {
	result := crossMeetingBriefingResult{
		RangeStart:      rangeStart,
		RangeEnd:        rangeEnd,
		Source:          source,
		OmittedMeetings: omittedMeetings,
		MeetingCoverage: map[string]meetingCoverageSummary{},
	}
	// Coverage is read straight off each contributing digest's server stamps, so
	// the briefing shows the captured window and caveats partial/unknown/listen-
	// only meetings without recomputing anything.
	for key, digest := range meetingDigests {
		summary := meetingCoverageFromDigest(digest)
		// Coverage honesty (finding F11): the stamped label was written when the
		// digest was produced and cannot know about a synthesis dead-letter that
		// landed afterward, so a meeting whose transcript→brain→digest lane
		// abandoned a window would otherwise ride into the briefing as "full".
		// Consult the live tombstones here — the same override
		// meetingCoverageDetail applies for get_meeting_detail — so the day-header
		// caveat and the coverage roll-up both see it. Overrides even a "full"
		// stamp: partial_synthesis is strictly worse (captured but unsummarized).
		if app.memory.hasDeadLetterForMeeting(key, summary.SpanStart, summary.SpanEnd) {
			summary.Label = coverageLabelPartialSynthesis
		}
		result.MeetingCoverage[key] = summary
	}
	ledgerByDay := app.activeDecisionTextsByDay(rangeStart, rangeEnd)
	digestedMeetings := map[string]struct{}{}
	for _, day := range localDaysInRange(rangeStart, rangeEnd) {
		fold, ok := foldDayDigest(day, meetingDigests)
		if !ok {
			if stored, exists := dayDigests[day]; exists {
				if payload, parsed := parseDayDigestPayload(stored.Text); parsed && len(payload.Meetings) > 0 {
					fold, ok = payload, true
				}
			}
		}
		ledger := ledgerByDay[day]
		if !ok && len(ledger) == 0 {
			continue
		}
		if ok {
			for _, meeting := range fold.Meetings {
				digestedMeetings[meeting.MeetingID] = struct{}{}
			}
		}
		result.Days = append(result.Days, briefingDay{Day: day, Fold: fold, HasFold: ok, LedgerDecisions: ledger})
	}
	result.DigestedMeetings = len(digestedMeetings)
	return result
}

// parseDayDigestPayload decodes a stored day_digest body (the producers write
// strict JSON; tolerate stray fences the parseMeetingDigest way).
func parseDayDigestPayload(text string) (dayDigestPayload, bool) {
	text = strings.TrimSpace(text)
	if fenced := strings.TrimPrefix(text, "```json"); fenced != text {
		text = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(fenced), "```"))
	} else if fenced := strings.TrimPrefix(text, "```"); fenced != text {
		text = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(fenced), "```"))
	}
	var payload dayDigestPayload
	if text == "" || json.Unmarshal([]byte(text), &payload) != nil {
		return dayDigestPayload{}, false
	}
	return payload, true
}

// composeCrossMeetingBriefing is the deterministic (no model call, no raw
// scan) briefing composer: current in-range digests + the active decision
// ledger, day by day.
func (app *kanbanBoardApp) composeCrossMeetingBriefing(rangeStart time.Time, rangeEnd time.Time) crossMeetingBriefingResult {
	if app == nil || app.memory == nil {
		return crossMeetingBriefingResult{RangeStart: rangeStart, RangeEnd: rangeEnd, Source: briefingSourceDigests}
	}
	meetingDigests := map[string]meetingMemoryEntry{}
	dayDigests := map[string]meetingMemoryEntry{}
	for _, digest := range app.memory.digestsInRange(rangeStart, rangeEnd) {
		switch digest.Kind {
		case meetingMemoryKindMeetingDigest:
			if key := digestEntryKey(digest); key != "" {
				meetingDigests[key] = digest
			}
		case meetingMemoryKindDayDigest:
			day := strings.TrimSpace(digest.Metadata[digestDayMetadataKey])
			if day == "" {
				day = digestEntryKey(digest)
			}
			if day != "" {
				dayDigests[day] = digest
			}
		}
	}
	return app.composeBriefingFromDigests(rangeStart, rangeEnd, meetingDigests, dayDigests, briefingSourceDigests, 0)
}

/* ---------- rendering ---------- */

func briefingFactLine(text string, by string, status string, owner string, importance int, meetingID string) string {
	var builder strings.Builder
	builder.WriteString("- ")
	if importance >= 4 {
		builder.WriteString(fmt.Sprintf("[!%d] ", importance))
	}
	builder.WriteString(text)
	details := make([]string, 0, 4)
	if by = strings.TrimSpace(by); by != "" {
		details = append(details, by)
	}
	if owner = strings.TrimSpace(owner); owner != "" {
		details = append(details, "owner "+owner)
	}
	if status = strings.TrimSpace(status); status != "" {
		details = append(details, "status "+status)
	}
	if meetingID = strings.TrimSpace(meetingID); meetingID != "" {
		details = append(details, meetingID)
	}
	if len(details) > 0 {
		builder.WriteString(" (")
		builder.WriteString(strings.Join(details, "; "))
		builder.WriteString(")")
	}
	return builder.String()
}

// briefingCapturedSuffix renders the compact "· captured HH:MM–HH:MM (partial)"
// caption a briefing day-header appends after a meeting, sourced from the
// digest coverage stamps. Empty when no captured span is known (map-reduce or
// legacy digests), so those headers stay exactly as before. The captured window
// itself is always honest; only partial/listen-only add a caveat.
func briefingCapturedSuffix(summary meetingCoverageSummary) string {
	if summary.SpanStart.IsZero() || summary.SpanEnd.IsZero() {
		return ""
	}
	location := meetingTimeLocation()
	var builder strings.Builder
	fmt.Fprintf(&builder, " · captured %s–%s", summary.SpanStart.In(location).Format("15:04"), summary.SpanEnd.In(location).Format("15:04"))
	switch summary.Label {
	case coverageLabelPartialLateStart:
		builder.WriteString(" (partial — capture began late)")
	case coverageLabelPartialGaps:
		builder.WriteString(" (partial — a quiet or uncaptured stretch)")
	case coverageLabelPartialSynthesis:
		builder.WriteString(" (not fully synthesized)")
	}
	if summary.ListenOnly {
		builder.WriteString(" (listen-only)")
	}
	return builder.String()
}

// coverageDetailNote is the neutral, human-readable coverage caveat handed to
// get_meeting_detail callers. It never asserts a capture failure: a partial_gaps
// stretch is described as possibly-quiet-or-uncaptured, and full coverage returns
// no note at all (the empty string). listen-only is additive.
func coverageDetailNote(summary meetingCoverageSummary) string {
	var parts []string
	switch summary.Label {
	case coverageLabelPartialLateStart:
		parts = append(parts, "capture began after the meeting was already underway, so the opening is not captured")
	case coverageLabelPartialGaps:
		parts = append(parts, "there is a stretch with no captured transcript — a quiet spell or a capture gap")
	case coverageLabelPartialSynthesis:
		parts = append(parts, "the transcript was captured but an automated synthesis pass did not finish folding part of it in, so some of the detail may not be summarized yet")
	case coverageLabelUnknown:
		parts = append(parts, "how completely this captured the meeting is unknown")
	}
	if summary.ListenOnly {
		parts = append(parts, "this was a listen-only sitting that may have been underway before capture began")
	}
	if len(parts) == 0 {
		return ""
	}
	return "This reflects the captured portion only: " + strings.Join(parts, "; ") + "."
}

func writeBriefingSection(builder *strings.Builder, heading string, lines []string, limit int) {
	if len(lines) == 0 {
		return
	}
	builder.WriteString(heading)
	builder.WriteByte('\n')
	shown := lines
	if len(shown) > limit {
		shown = shown[:limit]
	}
	for _, line := range shown {
		builder.WriteString(line)
		builder.WriteByte('\n')
	}
	if hidden := len(lines) - len(shown); hidden > 0 {
		builder.WriteString(fmt.Sprintf("- …%d more\n", hidden))
	}
}

// renderCrossMeetingBriefing renders the structured briefing as compact
// markdown: day by day, decisions and blockers first (each section arrives
// importance-ranked from the fold), attribution hedged as the digests wrote
// it, every fact carrying its source meeting. Bounded by the fold caps plus
// the render caps, so a week never becomes a wall of text.
func renderCrossMeetingBriefing(briefing crossMeetingBriefingResult) string {
	var builder strings.Builder
	endDay := ""
	if days := localDaysInRange(briefing.RangeStart, briefing.RangeEnd); len(days) > 0 {
		endDay = days[len(days)-1]
	}
	builder.WriteString("# What you missed — " + dayBucket(briefing.RangeStart.In(meetingTimeLocation())))
	if endDay != "" && endDay != dayBucket(briefing.RangeStart.In(meetingTimeLocation())) {
		builder.WriteString(" to " + endDay)
	}
	builder.WriteByte('\n')

	for _, day := range briefing.Days {
		builder.WriteString("\n## " + day.Day)
		if day.HasFold && len(day.Fold.Meetings) > 0 {
			names := make([]string, 0, len(day.Fold.Meetings))
			for _, meeting := range day.Fold.Meetings {
				label := meeting.MeetingID
				if title := strings.TrimSpace(meeting.Title); title != "" {
					label = fmt.Sprintf("%s (%s)", title, meeting.MeetingID)
				}
				names = append(names, label+briefingCapturedSuffix(briefing.MeetingCoverage[meeting.MeetingID]))
			}
			builder.WriteString(" — " + strings.Join(names, ", "))
		}
		builder.WriteByte('\n')

		decisions := make([]string, 0, len(day.Fold.Decisions))
		for _, decision := range day.Fold.Decisions {
			decisions = append(decisions, briefingFactLine(decision.D, decision.By, decision.Status, "", decision.Importance, decision.MeetingID))
		}
		writeBriefingSection(&builder, "Decisions:", decisions, briefingRenderDecisionCap)

		ledger := make([]string, 0, len(day.LedgerDecisions))
		for _, text := range day.LedgerDecisions {
			ledger = append(ledger, "- "+text)
		}
		writeBriefingSection(&builder, "On the decision ledger (verbatim):", ledger, briefingLedgerDecisionsPerDayCap)

		actions := make([]string, 0, len(day.Fold.ActionItems))
		for _, action := range day.Fold.ActionItems {
			actions = append(actions, briefingFactLine(action.A, "", action.Status, action.Owner, action.Importance, action.MeetingID))
		}
		writeBriefingSection(&builder, "Action items:", actions, briefingRenderActionCap)

		topics := make([]string, 0, len(day.Fold.Topics))
		for _, topic := range day.Fold.Topics {
			topics = append(topics, briefingFactLine(topic.T, "", "", "", topic.Importance, topic.MeetingID))
		}
		writeBriefingSection(&builder, "Topics:", topics, briefingRenderTopicCap)

		questions := make([]string, 0, len(day.Fold.OpenQuestions))
		for _, question := range day.Fold.OpenQuestions {
			questions = append(questions, briefingFactLine(question.Q, "", "", "", question.Importance, question.MeetingID))
		}
		writeBriefingSection(&builder, "Open questions:", questions, briefingRenderQuestionCap)

		if len(day.Fold.Themes) > 0 {
			themes := day.Fold.Themes
			if len(themes) > briefingRenderThemeCap {
				themes = themes[:briefingRenderThemeCap]
			}
			builder.WriteString("Themes: " + strings.Join(themes, ", ") + "\n")
		}
	}

	if briefing.Source == briefingSourceMapReduce {
		builder.WriteString("\n(Composed on demand from raw meeting memory — stored digests did not cover this range.)\n")
	}
	if briefing.OmittedMeetings > 0 {
		builder.WriteString(fmt.Sprintf("\n(%d more meeting(s) in range were omitted by the briefing cap.)\n", briefing.OmittedMeetings))
	}
	return strings.TrimRight(builder.String(), "\n")
}

/* ---------- recall fallback (replaces the silent 8-hit collapse) ---------- */

// rangedBriefingAnswer is the Wave-6 fallback for a time-ranged recall query
// whose model answer came back empty (model outage, token 400, keyless):
// compose deterministically from the digest tiers + decision ledger; when the
// range has no digest coverage, map-reduce fresh from raw. Returns ok=false
// only when neither path has material — the caller's buildMemoryAnswer
// keyword scraps stay the true last resort instead of the first fallback.
func (app *kanbanBoardApp) rangedBriefingAnswer(query string) (string, bool) {
	if app == nil || app.memory == nil {
		return "", false
	}
	rangeStart, rangeEnd, hasTimeRange := relativeQueryTimeRange(query, time.Now())
	if !hasTimeRange {
		return "", false
	}

	briefing := app.composeCrossMeetingBriefing(rangeStart, rangeEnd)
	if briefing.DigestedMeetings == 0 {
		// digests missing/stale for the range (pre-backfill window, digests
		// disabled, or history older than the rollup tiers): compose fresh.
		app.mu.Lock()
		apiKey := app.apiKey
		app.mu.Unlock()
		if strings.TrimSpace(apiKey) != "" {
			ctx, cancel := context.WithTimeout(context.Background(), comprehensiveBriefingTimeout)
			defer cancel()
			mapped, err := app.buildComprehensiveBriefing(ctx, apiKey, rangeStart, rangeEnd, nil)
			if err != nil {
				log.Errorf("comprehensive briefing fallback failed: %v", err)
			} else if !mapped.empty() {
				briefing = mapped
			}
		}
	}
	if briefing.empty() {
		return "", false
	}
	return renderCrossMeetingBriefing(briefing), true
}

/* ---------- tools ---------- */

// briefingRangeFromArgs resolves the tool's range arguments in Go (amendment
// A5: date math never delegated to the model): explicit local start_day /
// end_day (inclusive) win; otherwise the natural range phrase rides the same
// relativeQueryTimeRange the recall lane uses; default = this week.
func briefingRangeFromArgs(args map[string]any, now time.Time) (time.Time, time.Time, string, error) {
	location := meetingTimeLocation()
	startDay := strings.TrimSpace(asString(args["start_day"]))
	endDay := strings.TrimSpace(asString(args["end_day"]))
	if startDay != "" {
		start, err := time.ParseInLocation(dayBucketLayout, startDay, location)
		if err != nil {
			return time.Time{}, time.Time{}, "", fmt.Errorf("start_day must be YYYY-MM-DD")
		}
		end := start.AddDate(0, 0, 1)
		label := startDay
		if endDay != "" {
			parsedEnd, err := time.ParseInLocation(dayBucketLayout, endDay, location)
			if err != nil {
				return time.Time{}, time.Time{}, "", fmt.Errorf("end_day must be YYYY-MM-DD")
			}
			if parsedEnd.Before(start) {
				return time.Time{}, time.Time{}, "", fmt.Errorf("end_day is before start_day")
			}
			end = parsedEnd.AddDate(0, 0, 1)
			label = startDay + " to " + endDay
		}
		return start.UTC(), end.UTC(), label, nil
	}

	phrase := strings.TrimSpace(asString(args["range"]))
	if phrase == "" {
		phrase = "this week"
	}
	// The natural-language range shares the recall lane's parser (item 1.2), so
	// this understands today/yesterday, this/last week, this/last month, "N
	// days/weeks/months ago", month names ("in June"), weekdays, and absolute
	// dates in addition to the explicit start_day/end_day pair above.
	start, end, ok := relativeQueryTimeRange(phrase, now)
	if !ok {
		return time.Time{}, time.Time{}, "", fmt.Errorf("could not understand range %q; try today, yesterday, this week, last month, \"in June\", \"3 weeks ago\", a weekday, YYYY-MM-DD, or start_day/end_day", phrase)
	}
	return start, end, phrase, nil
}

// crossMeetingBriefingTool is the cross_meeting_briefing dispatch body.
func (app *kanbanBoardApp) crossMeetingBriefingTool(args map[string]any) (map[string]any, bool, error) {
	if app == nil || app.memory == nil {
		return nil, false, fmt.Errorf("meeting memory is unavailable")
	}
	rangeStart, rangeEnd, label, err := briefingRangeFromArgs(args, time.Now())
	if err != nil {
		return nil, false, err
	}

	briefing := app.composeCrossMeetingBriefing(rangeStart, rangeEnd)
	if briefing.DigestedMeetings == 0 {
		app.mu.Lock()
		apiKey := app.apiKey
		app.mu.Unlock()
		if strings.TrimSpace(apiKey) != "" {
			ctx, cancel := context.WithTimeout(context.Background(), comprehensiveBriefingTimeout)
			defer cancel()
			if mapped, mapErr := app.buildComprehensiveBriefing(ctx, apiKey, rangeStart, rangeEnd, nil); mapErr != nil {
				log.Errorf("cross_meeting_briefing map-reduce fallback failed: %v", mapErr)
			} else if !mapped.empty() {
				briefing = mapped
			}
		}
	}

	if briefing.empty() {
		return map[string]any{
			"ok":       true,
			"range":    label,
			"briefing": "Nothing was captured in meeting memory for that range.",
			"days":     0,
			"source":   briefing.Source,
			"coverage": briefing.coverageSummaryLine(),
		}, false, nil
	}
	return map[string]any{
		"ok":       true,
		"range":    label,
		"briefing": renderCrossMeetingBriefing(briefing),
		"days":     len(briefing.Days),
		"meetings": briefing.DigestedMeetings,
		"source":   briefing.Source,
		"coverage": briefing.coverageSummaryLine(),
	}, false, nil
}

// crossMeetingBriefingToolForPrincipal filters every digest, ledger row, and
// raw map-reduce anchor before composition or model use. The returned payload
// is caller-owned; this helper never broadcasts.
func (app *kanbanBoardApp) crossMeetingBriefingToolForPrincipal(args map[string]any, principal RecallPrincipal) (map[string]any, bool, error) {
	if app == nil || app.memory == nil {
		return nil, false, fmt.Errorf("meeting memory is unavailable")
	}
	return app.scopedRecallApp(context.Background(), principal).crossMeetingBriefingTool(args)
}

// getMeetingDetail is the get_meeting_detail dispatch body: the drill-down
// walk digest → brains → verbatim transcript (anchors resolve through
// transcriptWindowAround, which excludes hidden lines and rides the prompt
// cap). meeting_id and/or anchor must be supplied.
func (app *kanbanBoardApp) getMeetingDetail(args map[string]any) (map[string]any, bool, error) {
	if app == nil || app.memory == nil {
		return nil, false, fmt.Errorf("meeting memory is unavailable")
	}
	meetingID := strings.TrimSpace(asString(args["meeting_id"]))
	anchor := strings.TrimSpace(asString(args["anchor"]))
	if meetingID == "" && anchor == "" {
		return nil, false, fmt.Errorf("meeting_id or anchor is required")
	}

	result := map[string]any{"ok": true}
	found := false

	if anchor != "" {
		window := app.memory.transcriptWindowAround(anchor, meetingDetailAnchorRadius)
		lines := make([]string, 0, len(window))
		for _, entry := range window {
			marker := ""
			if entry.ID == anchor {
				marker = " «anchor»"
			}
			lines = append(lines, fmt.Sprintf("[%s]%s %s", entry.CreatedAt.Format(time.RFC3339), marker, strings.TrimSpace(entry.Text)))
			if meetingID == "" {
				meetingID = strings.TrimSpace(entry.Metadata["meetingId"])
			}
		}
		if len(lines) > 0 {
			result["verbatim"] = strings.Join(lines, "\n")
			found = true
		}
	}

	if meetingID != "" {
		result["meetingId"] = meetingID
		if record, ok := app.meetingDirectoryRecord(meetingID); ok {
			if title := strings.TrimSpace(record.Title); title != "" {
				result["title"] = title
			}
			result["started"] = record.StartedAt
			if record.EndedAt != "" {
				result["ended"] = record.EndedAt
			}
		}
		// Coverage honesty (kanban-card-107): how completely the digest window
		// covered the sitting, preferring the durable server-authored stamp
		// (meetingCoverageDetail). The machine label rides as coverage=; a neutral
		// human note rides alongside so the model relays partial coverage without
		// asserting capture failed (a gap can be quiet time).
		coverage := app.meetingCoverageDetail(meetingID)
		result["coverage"] = coverage.Label
		if note := coverageDetailNote(coverage); note != "" {
			result["coverageNote"] = note
		}
		if coverage.ListenOnly {
			result[externalMayPredateCaptureMetadataKey] = true
		}
		if !coverage.SpanStart.IsZero() && !coverage.SpanEnd.IsZero() {
			location := meetingTimeLocation()
			result["captured"] = fmt.Sprintf("%s–%s", coverage.SpanStart.In(location).Format("15:04"), coverage.SpanEnd.In(location).Format("15:04"))
		}
		if digest, ok := app.memory.latestDigestPerMeeting()[meetingID]; ok {
			result["digest"] = digest.Text
			found = true
		}
		brains := make([]string, 0, meetingDetailBrainCap)
		meetingEntries := app.memory.snapshotForMeeting(meetingID, 0)
		for index := len(meetingEntries) - 1; index >= 0 && len(brains) < meetingDetailBrainCap; index-- {
			if meetingEntries[index].Kind != meetingMemoryKindBrain {
				continue
			}
			brains = append(brains, fmt.Sprintf("[%s]\n%s", meetingEntries[index].CreatedAt.Format(time.RFC3339), strings.TrimSpace(meetingEntries[index].Text)))
		}
		if len(brains) > 0 {
			// newest-first collected; present oldest-first for reading order.
			for i, j := 0, len(brains)-1; i < j; i, j = i+1, j-1 {
				brains[i], brains[j] = brains[j], brains[i]
			}
			result["brains"] = brains
			found = true
		}
	}

	if !found {
		return nil, false, fmt.Errorf("no meeting memory found for %s", firstNonEmptyString(meetingID, anchor))
	}
	return result, false, nil
}

// getMeetingDetailForPrincipal performs the scope reduction before resolving
// an anchor, digest, brain body, coverage, or meeting-directory metadata.
func (app *kanbanBoardApp) getMeetingDetailForPrincipal(args map[string]any, principal RecallPrincipal) (map[string]any, bool, error) {
	if app == nil || app.memory == nil {
		return nil, false, fmt.Errorf("meeting memory is unavailable")
	}
	scoped := app.scopedRecallApp(context.Background(), principal)
	meetingID := strings.TrimSpace(asString(args["meeting_id"]))
	if meetingID != "" && !scoped.meetingVisibleToRecallPrincipal(meetingID, principal) {
		return nil, false, fmt.Errorf("no meeting memory found for %s", meetingID)
	}
	return scoped.getMeetingDetail(args)
}

// meetingVisibleToRecallPrincipal is metadata-only. Named-room/sitting callers
// must match the durable meeting directory when present, and legacy records
// without a directory row are visible only when an already-filtered memory
// header proves the meeting belongs to the principal's scope.
func (app *kanbanBoardApp) meetingVisibleToRecallPrincipal(meetingID string, principal RecallPrincipal) bool {
	meetingID = strings.TrimSpace(meetingID)
	if app == nil || app.memory == nil || meetingID == "" {
		return false
	}
	// Directory rows are legacy organization-readable headers. Content access
	// is proved below by the already principal-filtered store; room membership
	// is not required for organization-granted historical recall.
	app.memory.mu.Lock()
	defer app.memory.mu.Unlock()
	for _, entry := range app.memory.entries {
		if strings.TrimSpace(entry.Metadata["meetingId"]) == meetingID && !memoryEntryHiddenFromRecall(entry) {
			return true
		}
	}
	return false
}

// meetingCoverageDetail resolves how completely the stored digest window covers
// a past meeting's room-occupancy sitting. It PREFERS the coverage label the
// per-meeting digest producer stamped (digestCoverageMetadataKey), because that
// stamp was computed while the raw transcripts were still intact — and raw
// transcript entries are deletable by design (slop-quarantine 30-day expiry,
// user chat deletes). Recomputing live from transcriptCoverageForMeeting would
// drift an aged meeting to partial_gaps/unknown as its lines age out, even
// though nothing about the meeting changed. The live recompute is therefore used
// ONLY as a legacy fallback when a digest carries no coverage stamp at all.
// Legacy synthetic keys, meetings with no directory record, and meetings with no
// captured span degrade to an explicit "unknown" — never a fabricated "full".
func (app *kanbanBoardApp) meetingCoverageDetail(meetingID string) meetingCoverageSummary {
	summary := meetingCoverageSummary{Label: coverageLabelUnknown}
	if app == nil || app.memory == nil {
		return summary
	}
	stamped := ""
	if digest, ok := app.memory.latestDigestPerMeeting()[meetingID]; ok {
		if start, end, spanOK := parseDigestSpanMetadata(digest); spanOK {
			summary.SpanStart, summary.SpanEnd = start, end
		}
		if strings.EqualFold(strings.TrimSpace(digest.Metadata[listenOnlyMetadataKey]), "true") {
			summary.ListenOnly = true
		}
		stamped = strings.TrimSpace(digest.Metadata[digestCoverageMetadataKey])
	}
	record, hasRecord := app.meetingDirectoryRecord(meetingID)
	if record.ListenOnly {
		summary.ListenOnly = true
	}
	// Prefer the durable server-authored stamp over a live recompute; otherwise
	// recompute from whatever transcript evidence survives (best effort).
	if stamped != "" {
		summary.Label = stamped
	} else {
		resolvable := hasRecord && !isLegacyMeetingKey(meetingID)
		var sittingStart time.Time
		if resolvable {
			if parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(record.StartedAt)); err == nil {
				sittingStart = parsed
			}
		}
		coverage := app.memory.transcriptCoverageForMeeting(meetingID)
		summary.Label = meetingCoverageLabel(resolvable, sittingStart, summary.SpanStart, coverage.MaxInternalGap)
	}
	// Dead-letter override (memory study 1.4, gap #9): the digest-synthesis lane
	// (transcript→brain→digest) abandoned a window of this meeting after repeated
	// failures — transcripts exist but were never folded into the digest. That is
	// orthogonal to (and strictly worse than) the capture labels above, so it
	// overrides even a "full" capture stamp: the honest read is captured-but-not-
	// fully-synthesized. hasDeadLetterForMeeting scopes this to the digest lane
	// (finding F13) so a board / mission / ledger dead-letter — which loses cards /
	// insights / records, not the digest — never flips coverage. Detected live
	// because a dead-letter can land long after the digest stamped its coverage.
	if app.memory.hasDeadLetterForMeeting(meetingID, summary.SpanStart, summary.SpanEnd) {
		summary.Label = coverageLabelPartialSynthesis
	}
	return summary
}

// meetingDirectoryRecord resolves one meetings-directory record by id.
func (app *kanbanBoardApp) meetingDirectoryRecord(meetingID string) (meetingRecord, bool) {
	if app == nil || app.meetings == nil || strings.TrimSpace(meetingID) == "" {
		return meetingRecord{}, false
	}
	for _, record := range app.meetings.recent(0) {
		if record.ID == meetingID {
			return record, true
		}
	}
	return meetingRecord{}, false
}
