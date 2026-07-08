package main

// Digest producers (Track-2 Wave 2) — the two rollup workers above the brain
// tier, both on the generic ambient framework (agent_runner.go):
//
//   meetingDigestWorker  brain → meeting_digest. One cumulative anchored-JSON
//     digest per meetingId (T2 schema below), rebuilt with prior-digest
//     continuity (the mission-intel "previous insight" carry) and a per-fact
//     importance score 1-5 (amendment A4). Consumes ONLY kind=brain text, so
//     an os_artifact/base64 body is structurally unreachable by its prompt.
//     Meetings rebuilt per tick are capped (MEETING_DIGEST_MAX_MEETINGS_PER_TICK)
//     and the pass cursor only ever advances through brains whose meeting was
//     fully digested, so capped/failed meetings re-feed instead of dropping.
//
//   dayDigestWorker  meeting_digest → day_digest. A DETERMINISTIC Go fold —
//     no model call (amendments A2/A5 doctrine: load-bearing facts are records
//     you regroup, never re-summarize): facts are bucketed onto local calendar
//     days (dayBucket on each fact's own `at` stamp, so a marathon meeting
//     splits into per-day slices) and ranked importance-first. Its durable
//     cursor is a day_digest_pass artifact (the decision_pass pattern) so a
//     zero-fold pass still advances. Riding the same tick, amendment A3's
//     end-of-day REFLECTION spends the worker's one model call: a synthesis
//     pass over recent digests + decision-ledger deltas (recurring blockers,
//     consensus forming/diverging, decisions circled without closure,
//     ownership drift), written as a recall-eligible kind=reflection entry at
//     most once per local day.
//
// companyDigestWorker is deliberately NOT built here: amendment A2 makes T4 a
// ledger state view + thin narrative, which lands with the ledger (Wave 4).
// Backfill ships OFF by default (MEETING_DIGEST_BACKFILL / DAY_DIGEST_BACKFILL
// falsy → startAmbientAgent baselines at the newest pre-boot input), so a
// first deploy never token-spikes over weeks of stored brains.

import (
	"context"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	meetingDigestAgentName                 = "meeting digest"
	defaultMeetingDigestInterval           = 6 * time.Minute
	meetingDigestRequestTimeout            = 90 * time.Second
	defaultMeetingDigestMaxMeetingsPerTick = 3
	meetingDigestMaxOutputTokens           = 1500
	// meetingDigestCursorMetadataKey rides every upserted meeting_digest; the
	// runner reads it off the NEWEST digest to resume after the consumed
	// window (agent_runner.go unconsumedEntriesAfter).
	meetingDigestCursorMetadataKey = "throughBrainId"

	dayDigestAgentName         = "day digest"
	defaultDayDigestInterval   = 30 * time.Minute
	dayDigestRequestTimeout    = 120 * time.Second
	dayDigestCursorMetadataKey = "throughMeetingDigestId"
	// dayDigestFoldSource marks day digests as deterministic Go folds, never
	// model output — a debugging/audit stamp.
	dayDigestFoldSource = "digest_fold"

	// reflectionDisabledEnv turns off ONLY the A3 reflection pass while the
	// deterministic day folds keep running.
	reflectionDisabledEnv     = "DAY_REFLECTION_DISABLED"
	reflectionLookbackDays    = 7
	reflectionMaxDigests      = 10
	reflectionMaxDecisions    = 10
	reflectionMaxSupportIDs   = 12
	reflectionMaxOutputTokens = 700
)

// T2 section caps (meeting_digest); every string field is trimForStorage-bound
// so a whole digest stays ~4KB and rides recall context safely.
const (
	meetingDigestTopicCap    = 12
	meetingDigestDecisionCap = 12
	meetingDigestActionCap   = 16
	meetingDigestQuestionCap = 10
	meetingDigestThemeCap    = 8
	meetingDigestAttendeeCap = 12
	// meetingDigestDefaultImportance is stamped when the model omits a score
	// (0) — mid-scale, never accidentally top-ranked.
	meetingDigestDefaultImportance = 3
)

// T3 section caps (day_digest): a day folds several meetings, so the sections
// run wider than one meeting's.
const (
	dayDigestTopicCap    = 20
	dayDigestDecisionCap = 20
	dayDigestActionCap   = 24
	dayDigestQuestionCap = 15
	dayDigestThemeCap    = 10
)

func meetingDigestAgent() ambientAgentConfig {
	return ambientAgentConfig{
		name:              meetingDigestAgentName,
		defaultInterval:   defaultMeetingDigestInterval,
		intervalEnv:       "MEETING_DIGEST_INTERVAL",
		disabledEnv:       "MEETING_DIGEST_DISABLED",
		backfillEnv:       "MEETING_DIGEST_BACKFILL",
		minBatchEnv:       "MEETING_DIGEST_MIN_INPUTS",
		defaultMinBatch:   1,
		maxBatchEnv:       "MEETING_DIGEST_MAX_INPUTS",
		defaultMaxBatch:   24,
		inputKind:         meetingMemoryKindBrain,
		artifactKind:      meetingMemoryKindMeetingDigest,
		cursorMetadataKey: meetingDigestCursorMetadataKey,
		requestTimeout:    meetingDigestRequestTimeout,
		produce:           (*kanbanBoardApp).produceMeetingDigests,
	}
}

func dayDigestAgent() ambientAgentConfig {
	return ambientAgentConfig{
		name:              dayDigestAgentName,
		defaultInterval:   defaultDayDigestInterval,
		intervalEnv:       "DAY_DIGEST_INTERVAL",
		disabledEnv:       "DAY_DIGEST_DISABLED",
		backfillEnv:       "DAY_DIGEST_BACKFILL",
		minBatchEnv:       "DAY_DIGEST_MIN_INPUTS",
		defaultMinBatch:   1,
		maxBatchEnv:       "DAY_DIGEST_MAX_INPUTS",
		defaultMaxBatch:   16,
		inputKind:         meetingMemoryKindMeetingDigest,
		artifactKind:      meetingMemoryKindDayDigestPass,
		cursorMetadataKey: dayDigestCursorMetadataKey,
		requestTimeout:    dayDigestRequestTimeout,
		produce:           (*kanbanBoardApp).produceDayDigestPass,
	}
}

func (app *kanbanBoardApp) startMeetingDigestWorker(apiKey string) {
	app.startAmbientAgent(meetingDigestAgent(), apiKey)
}

func (app *kanbanBoardApp) startDayDigestWorker(apiKey string) {
	app.startAmbientAgent(dayDigestAgent(), apiKey)
}

func meetingDigestMaxMeetingsPerTick() int {
	return positiveIntEnv("MEETING_DIGEST_MAX_MEETINGS_PER_TICK", defaultMeetingDigestMaxMeetingsPerTick)
}

/* ---------- T2 anchored-JSON schema ---------- */

// The fact structs carry a MeetingID provenance field that stays EMPTY inside
// a meeting_digest (the digest itself is meeting-scoped) and is stamped by the
// day fold so a day_digest fact still points at its source meeting.

type meetingDigestTopic struct {
	T          string `json:"t"`
	Anchor     string `json:"anchor,omitempty"`
	At         string `json:"at,omitempty"`
	Importance int    `json:"importance,omitempty"`
	MeetingID  string `json:"meetingId,omitempty"`
}

type meetingDigestDecision struct {
	D          string `json:"d"`
	By         string `json:"by,omitempty"`
	Status     string `json:"status,omitempty"`
	Anchor     string `json:"anchor,omitempty"`
	At         string `json:"at,omitempty"`
	Importance int    `json:"importance,omitempty"`
	MeetingID  string `json:"meetingId,omitempty"`
}

type meetingDigestAction struct {
	A          string `json:"a"`
	Owner      string `json:"owner,omitempty"`
	Status     string `json:"status,omitempty"`
	Anchor     string `json:"anchor,omitempty"`
	At         string `json:"at,omitempty"`
	Importance int    `json:"importance,omitempty"`
	MeetingID  string `json:"meetingId,omitempty"`
}

type meetingDigestQuestion struct {
	Q          string `json:"q"`
	Anchor     string `json:"anchor,omitempty"`
	At         string `json:"at,omitempty"`
	Importance int    `json:"importance,omitempty"`
	MeetingID  string `json:"meetingId,omitempty"`
}

type meetingDigestPayload struct {
	MeetingID     string                  `json:"meetingId"`
	Title         string                  `json:"title,omitempty"`
	Day           string                  `json:"day"`
	Started       string                  `json:"started,omitempty"`
	Ended         string                  `json:"ended,omitempty"`
	Attendees     []string                `json:"attendees,omitempty"`
	Topics        []meetingDigestTopic    `json:"topics,omitempty"`
	Decisions     []meetingDigestDecision `json:"decisions,omitempty"`
	ActionItems   []meetingDigestAction   `json:"actionItems,omitempty"`
	OpenQuestions []meetingDigestQuestion `json:"openQuestions,omitempty"`
	Themes        []string                `json:"themes,omitempty"`
}

// parseMeetingDigest validates digest JSON with the same stray-markdown-fence
// tolerance as parseMissionInsight. Bad JSON → ok=false: the caller keeps the
// prior digest and leaves the cursor put so the window retries next pass.
func parseMeetingDigest(text string) (meetingDigestPayload, bool) {
	text = strings.TrimSpace(text)
	if fenced := strings.TrimPrefix(text, "```json"); fenced != text {
		text = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(fenced), "```"))
	} else if fenced := strings.TrimPrefix(text, "```"); fenced != text {
		text = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(fenced), "```"))
	}
	var payload meetingDigestPayload
	if text == "" || json.Unmarshal([]byte(text), &payload) != nil {
		return meetingDigestPayload{}, false
	}

	return payload, true
}

// clampImportance bounds a per-fact score to 1-5 (amendment A4); an absent or
// invalid score lands mid-scale so it is never accidentally top-ranked.
func clampImportance(value int) int {
	if value < 1 {
		return meetingDigestDefaultImportance
	}
	if value > 5 {
		return 5
	}

	return value
}

// clampMeetingDigestPayload bounds every model-written field before the
// canonical re-marshal is persisted: server-derived truth (meetingId, day,
// span fallbacks) overrides whatever the model claimed, sections are capped,
// strings trimForStorage-bound, importance clamped, and the day-fold-only
// MeetingID provenance fields cleared.
func clampMeetingDigestPayload(payload *meetingDigestPayload, meetingID string, day string, spanStart time.Time, spanEnd time.Time) {
	payload.MeetingID = meetingID
	payload.Day = day
	payload.Title = trimForStorage(payload.Title, 120)
	if strings.TrimSpace(payload.Started) == "" {
		payload.Started = spanStart.UTC().Format(time.RFC3339)
	}
	if strings.TrimSpace(payload.Ended) == "" {
		payload.Ended = spanEnd.UTC().Format(time.RFC3339)
	}
	if len(payload.Attendees) > meetingDigestAttendeeCap {
		payload.Attendees = payload.Attendees[:meetingDigestAttendeeCap]
	}
	for index := range payload.Attendees {
		payload.Attendees[index] = trimForStorage(payload.Attendees[index], 60)
	}
	if len(payload.Topics) > meetingDigestTopicCap {
		payload.Topics = payload.Topics[:meetingDigestTopicCap]
	}
	for index := range payload.Topics {
		topic := &payload.Topics[index]
		topic.T = trimForStorage(topic.T, 160)
		topic.Anchor = trimForStorage(topic.Anchor, 80)
		topic.At = trimForStorage(topic.At, 40)
		topic.Importance = clampImportance(topic.Importance)
		topic.MeetingID = ""
	}
	if len(payload.Decisions) > meetingDigestDecisionCap {
		payload.Decisions = payload.Decisions[:meetingDigestDecisionCap]
	}
	for index := range payload.Decisions {
		decision := &payload.Decisions[index]
		decision.D = trimForStorage(decision.D, 240)
		decision.By = trimForStorage(decision.By, 60)
		decision.Status = trimForStorage(decision.Status, 40)
		decision.Anchor = trimForStorage(decision.Anchor, 80)
		decision.At = trimForStorage(decision.At, 40)
		decision.Importance = clampImportance(decision.Importance)
		decision.MeetingID = ""
	}
	if len(payload.ActionItems) > meetingDigestActionCap {
		payload.ActionItems = payload.ActionItems[:meetingDigestActionCap]
	}
	for index := range payload.ActionItems {
		action := &payload.ActionItems[index]
		action.A = trimForStorage(action.A, 200)
		action.Owner = trimForStorage(action.Owner, 60)
		action.Status = trimForStorage(action.Status, 40)
		action.Anchor = trimForStorage(action.Anchor, 80)
		action.At = trimForStorage(action.At, 40)
		action.Importance = clampImportance(action.Importance)
		action.MeetingID = ""
	}
	if len(payload.OpenQuestions) > meetingDigestQuestionCap {
		payload.OpenQuestions = payload.OpenQuestions[:meetingDigestQuestionCap]
	}
	for index := range payload.OpenQuestions {
		question := &payload.OpenQuestions[index]
		question.Q = trimForStorage(question.Q, 200)
		question.Anchor = trimForStorage(question.Anchor, 80)
		question.At = trimForStorage(question.At, 40)
		question.Importance = clampImportance(question.Importance)
		question.MeetingID = ""
	}
	if len(payload.Themes) > meetingDigestThemeCap {
		payload.Themes = payload.Themes[:meetingDigestThemeCap]
	}
	for index := range payload.Themes {
		payload.Themes[index] = trimForStorage(payload.Themes[index], 80)
	}
}

/* ---------- meeting digest producer ---------- */

// digestKeyForBrain scopes a brain to its digest: the meetingId stamp, or —
// for the legacy null-meetingId entries — a stable synthetic per-local-day
// key, so pre-scoping history becomes a first-class recall object instead of
// staying invisible to meeting-scoped reads.
func digestKeyForBrain(entry meetingMemoryEntry) string {
	if meetingID := strings.TrimSpace(entry.Metadata["meetingId"]); meetingID != "" {
		return meetingID
	}

	return "meeting-legacy-" + dayBucket(entry.CreatedAt)
}

type brainDigestGroup struct {
	key    string
	brains []meetingMemoryEntry
}

// groupBrainsForDigest groups the unconsumed brain window by digest key,
// preserving first-appearance (store) order — the order the pass processes
// and the order the prefix cursor depends on.
func groupBrainsForDigest(inputs []meetingMemoryEntry) []brainDigestGroup {
	order := make([]string, 0, len(inputs))
	byKey := map[string][]meetingMemoryEntry{}
	for _, input := range inputs {
		key := digestKeyForBrain(input)
		if _, ok := byKey[key]; !ok {
			order = append(order, key)
		}
		byKey[key] = append(byKey[key], input)
	}
	groups := make([]brainDigestGroup, 0, len(order))
	for _, key := range order {
		groups = append(groups, brainDigestGroup{key: key, brains: byKey[key]})
	}

	return groups
}

// digestPassCursor returns the id of the last input in the longest PREFIX of
// the window whose every brain belongs to an already-digested meeting. The
// cursor therefore never advances past the first brain of a capped or failed
// meeting — those brains re-feed next tick instead of being dropped. Stamping
// an empty cursor would be interpreted as "consumed through my own position"
// (unconsumedEntriesAfter's position fallback), so the current group's own
// window is the defensive floor.
func digestPassCursor(inputs []meetingMemoryEntry, processed map[string]bool, group brainDigestGroup) string {
	cursor := ""
	for _, input := range inputs {
		if !processed[digestKeyForBrain(input)] {
			break
		}
		cursor = input.ID
	}
	if cursor == "" && len(group.brains) > 0 {
		cursor = group.brains[len(group.brains)-1].ID
	}

	return cursor
}

// brainWindowBounds resolves the transcript window a brain batch covers,
// preferring the brain worker's from/throughTranscriptCreatedAt stamps over
// the write-up's own CreatedAt.
func brainWindowBounds(brains []meetingMemoryEntry) (time.Time, time.Time) {
	var start, end time.Time
	for _, brain := range brains {
		from := brain.CreatedAt
		if parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(brain.Metadata["fromTranscriptCreatedAt"])); err == nil {
			from = parsed
		}
		through := brain.CreatedAt
		if parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(brain.Metadata["throughTranscriptCreatedAt"])); err == nil {
			through = parsed
		}
		if start.IsZero() || from.Before(start) {
			start = from
		}
		if end.IsZero() || through.After(end) {
			end = through
		}
	}

	return start, end
}

// meetingDigestSpan extends the prior digest's covered window with the new
// brain batch, so a cumulative digest's spanStart/spanEnd always bound
// EVERYTHING it has folded (digestsInRange overlap-matches on these).
func meetingDigestSpan(prior meetingMemoryEntry, hasPrior bool, brains []meetingMemoryEntry) (time.Time, time.Time) {
	start, end := brainWindowBounds(brains)
	if hasPrior {
		if priorStart, err := time.Parse(time.RFC3339, strings.TrimSpace(prior.Metadata[digestSpanStartMetadataKey])); err == nil && (start.IsZero() || priorStart.Before(start)) {
			start = priorStart
		}
		if priorEnd, err := time.Parse(time.RFC3339, strings.TrimSpace(prior.Metadata[digestSpanEndMetadataKey])); err == nil && priorEnd.After(end) {
			end = priorEnd
		}
	}

	return start, end
}

func meetingDigestInstructions() string {
	return strings.Join([]string{
		"You are Bonfire's meeting digest compiler.",
		"Fold the previous digest (when present) and the new brain write-ups into ONE cumulative digest of this meeting so far.",
		"Return STRICT JSON only, no markdown fence, matching:",
		`{"meetingId":string,"title":string(<=80 chars),"day":"YYYY-MM-DD","started":RFC3339,"ended":RFC3339,"attendees":[string](<=12),"topics":[{"t":string(<=160),"anchor":string,"at":RFC3339,"importance":int}],"decisions":[{"d":string(<=240),"by":string,"status":string,"anchor":string,"at":RFC3339,"importance":int}],"actionItems":[{"a":string(<=200),"owner":string,"status":string,"anchor":string,"at":RFC3339,"importance":int}],"openQuestions":[{"q":string(<=200),"anchor":string,"at":RFC3339,"importance":int}],"themes":[string]}.`,
		"importance scores each fact 1-5: 5 = blocking or company-critical, 4 = a real commitment or decision, 3 = notable, 2 = context, 1 = passing chatter.",
		"anchor = one transcript entry id copied VERBATIM from a Transcript reference in the write-ups; empty string when uncertain — never fabricate ids.",
		"at = the RFC3339 time within the covered window when the fact surfaced; empty string when unknown.",
		"Carry forward still-relevant facts from the previous digest and update their statuses; a decision stays until explicitly reversed; an action item marked done keeps its row with status done.",
		"Speaker attribution upstream is an energy heuristic and can be wrong: hedge who-said-what ('attributed to X'), never assert it as certain.",
		"Never invent facts, people, clients, dates, decisions, or action items.",
		"Caps: topics<=12, decisions<=12, actionItems<=16, openQuestions<=10, themes<=8. If the window is thin, return fewer items, never filler.",
	}, " ")
}

// buildMeetingDigestInput assembles one meeting's digest prompt: prior digest
// continuity plus the new brain write-ups. Brain/digest text ONLY — never
// os_artifact bodies — so the input is blob-free by construction.
func (app *kanbanBoardApp) buildMeetingDigestInput(meetingID string, prior meetingMemoryEntry, hasPrior bool, brains []meetingMemoryEntry, generatedAt time.Time) string {
	var builder strings.Builder
	builder.WriteString("# Generated at\n")
	builder.WriteString(generatedAt.Format(time.RFC3339))

	builder.WriteString("\n\n# Meeting\nid: ")
	builder.WriteString(meetingID)
	if title := app.meetingRecordTitle(meetingID); title != "" {
		builder.WriteString("\ntitle: ")
		builder.WriteString(title)
	}

	if hasPrior {
		builder.WriteString("\n\n# Previous digest for this meeting (continuity — carry forward, update statuses, never silently drop)\n")
		builder.WriteString(prior.Text)
	}

	builder.WriteString("\n\n# New brain write-ups (oldest first)\n")
	for _, brain := range brains {
		builder.WriteString("- id=")
		builder.WriteString(brain.ID)
		builder.WriteString(" time=")
		builder.WriteString(brain.CreatedAt.Format(time.RFC3339))
		builder.WriteByte('\n')
		for _, line := range strings.Split(brain.Text, "\n") {
			builder.WriteString("  ")
			builder.WriteString(strings.TrimSpace(line))
			builder.WriteByte('\n')
		}
	}

	return builder.String()
}

// meetingRecordTitle returns the meetings-directory title for a meeting id
// (the mission-intel auto-title), empty when the record is unknown/untitled.
func (app *kanbanBoardApp) meetingRecordTitle(meetingID string) string {
	if app == nil || app.meetings == nil || strings.TrimSpace(meetingID) == "" {
		return ""
	}
	for _, record := range app.meetings.recent(0) {
		if record.ID == meetingID {
			return strings.TrimSpace(record.Title)
		}
	}

	return ""
}

// produceMeetingDigests is the meeting-digest agent's pass body: group the
// unconsumed brain window by meeting, rebuild up to the per-tick cap of
// meetings (one model call each, prior digest carried for continuity), and
// upsert each digest with the prefix cursor. On a model error or non-JSON
// output the pass STOPS: the cursor never advances past the failed meeting's
// brains, so the window re-feeds and retries next tick while the prior digest
// stays current.
func (app *kanbanBoardApp) produceMeetingDigests(ctx context.Context, apiKey string, inputs []meetingMemoryEntry, responder openAITextResponder) (meetingMemoryEntry, error) {
	groups := groupBrainsForDigest(inputs)
	if maxMeetings := meetingDigestMaxMeetingsPerTick(); len(groups) > maxMeetings {
		// deferred, never dropped: the prefix cursor stops before the first
		// brain of every uncapped group, so they re-feed next tick.
		groups = groups[:maxMeetings]
	}

	model := meetingBrainModel()
	current := app.memory.latestDigestPerMeeting()
	processed := map[string]bool{}
	var newest meetingMemoryEntry
	for _, group := range groups {
		prior, hasPrior := current[group.key]
		text, err := responder(ctx, apiKey, openAITextRequest{
			Model:           model,
			Instructions:    meetingDigestInstructions(),
			Input:           app.buildMeetingDigestInput(group.key, prior, hasPrior, group.brains, time.Now().UTC()),
			ReasoningEffort: "low",
			Verbosity:       "low",
			MaxOutputTokens: meetingDigestMaxOutputTokens,
		})
		if err != nil {
			return newest, err
		}
		payload, ok := parseMeetingDigest(text)
		if !ok {
			// mission-intel precedent: never persist unparseable output — the
			// prior digest stays current, the cursor stays put, and the same
			// window (plus anything newer) retries next pass.
			log.Errorf("%s returned non-JSON output for %s; keeping the prior digest", meetingDigestAgentName, group.key)
			return newest, nil
		}

		processed[group.key] = true
		spanStart, spanEnd := meetingDigestSpan(prior, hasPrior, group.brains)
		day := dayBucket(spanEnd)
		clampMeetingDigestPayload(&payload, group.key, day, spanStart, spanEnd)
		canonical, err := json.Marshal(payload)
		if err != nil {
			return newest, err
		}
		metadata := map[string]string{
			"source": "openai_responses",
			"model":  model,
			// meetingId == digestKey on purpose: the digest belongs to ITS OWN
			// meeting's snapshotForMeeting/archive embed (upsertDigest never
			// auto-stamps the LIVE meeting id — Wave 1 contract).
			"meetingId":                    group.key,
			digestDayMetadataKey:           day,
			digestSpanStartMetadataKey:     spanStart.UTC().Format(time.RFC3339),
			digestSpanEndMetadataKey:       spanEnd.UTC().Format(time.RFC3339),
			"fromBrainId":                  group.brains[0].ID,
			meetingDigestCursorMetadataKey: digestPassCursor(inputs, processed, group),
			"brainCount":                   strconv.Itoa(len(group.brains)),
			"generatedAt":                  time.Now().UTC().Format(time.RFC3339),
		}
		entry, err := app.memory.upsertDigest(meetingMemoryKindMeetingDigest, group.key, string(canonical), metadata)
		if err != nil {
			return newest, err
		}
		newest = entry
	}

	return newest, nil
}

/* ---------- day digest fold (deterministic) ---------- */

// factDay buckets a fact onto its local calendar day via its own `at` stamp,
// falling back to the whole digest's day — the mechanism that splits a
// marathon meeting into per-day slices.
func factDay(at string, fallback string) string {
	if parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(at)); err == nil {
		return dayBucket(parsed)
	}

	return fallback
}

func meetingDigestFallbackDay(entry meetingMemoryEntry) string {
	if day := strings.TrimSpace(entry.Metadata[digestDayMetadataKey]); day != "" {
		return day
	}

	return dayBucket(entry.CreatedAt)
}

// meetingDigestAffectedDays lists every local calendar day a meeting_digest
// touches: each fact's own day plus the digest's fallback day. Used for day
// discovery only, so superseded inputs count too (their replacement covers
// the same days and the fold reads only current digests).
func meetingDigestAffectedDays(entry meetingMemoryEntry) []string {
	fallback := meetingDigestFallbackDay(entry)
	days := map[string]struct{}{fallback: {}}
	if payload, ok := parseMeetingDigest(entry.Text); ok {
		for _, topic := range payload.Topics {
			days[factDay(topic.At, fallback)] = struct{}{}
		}
		for _, decision := range payload.Decisions {
			days[factDay(decision.At, fallback)] = struct{}{}
		}
		for _, action := range payload.ActionItems {
			days[factDay(action.At, fallback)] = struct{}{}
		}
		for _, question := range payload.OpenQuestions {
			days[factDay(question.At, fallback)] = struct{}{}
		}
	}
	sorted := make([]string, 0, len(days))
	for day := range days {
		sorted = append(sorted, day)
	}
	sort.Strings(sorted)

	return sorted
}

type dayDigestMeetingRef struct {
	MeetingID string `json:"meetingId"`
	Title     string `json:"title,omitempty"`
}

type dayDigestPayload struct {
	Day           string                  `json:"day"`
	Meetings      []dayDigestMeetingRef   `json:"meetings"`
	Decisions     []meetingDigestDecision `json:"decisions,omitempty"`
	Topics        []meetingDigestTopic    `json:"topics,omitempty"`
	ActionItems   []meetingDigestAction   `json:"actionItems,omitempty"`
	OpenQuestions []meetingDigestQuestion `json:"openQuestions,omitempty"`
	Themes        []string                `json:"themes,omitempty"`
}

// foldDayDigest deterministically regroups the CURRENT meeting digests' facts
// onto one local calendar day — no model call, so nothing can be hallucinated
// or dropped: facts are selected by their own day bucket, stamped with source
// meeting provenance, and ranked importance-first (amendment A4). ok=false
// when no meeting contributed a fact for the day.
func foldDayDigest(day string, currentDigests map[string]meetingMemoryEntry) (dayDigestPayload, bool) {
	location := meetingTimeLocation()
	keys := make([]string, 0, len(currentDigests))
	for key := range currentDigests {
		keys = append(keys, key)
	}
	// deterministic meeting order: covered-window start, then key.
	sort.SliceStable(keys, func(i, j int) bool {
		startI, _ := digestSpan(currentDigests[keys[i]], location)
		startJ, _ := digestSpan(currentDigests[keys[j]], location)
		if !startI.Equal(startJ) {
			return startI.Before(startJ)
		}
		return keys[i] < keys[j]
	})

	payload := dayDigestPayload{Day: day, Meetings: []dayDigestMeetingRef{}}
	seenThemes := map[string]struct{}{}
	for _, key := range keys {
		entry := currentDigests[key]
		digest, ok := parseMeetingDigest(entry.Text)
		if !ok {
			continue
		}
		fallback := meetingDigestFallbackDay(entry)
		contributed := false
		for _, topic := range digest.Topics {
			if factDay(topic.At, fallback) != day {
				continue
			}
			topic.Importance = clampImportance(topic.Importance)
			topic.MeetingID = key
			payload.Topics = append(payload.Topics, topic)
			contributed = true
		}
		for _, decision := range digest.Decisions {
			if factDay(decision.At, fallback) != day {
				continue
			}
			decision.Importance = clampImportance(decision.Importance)
			decision.MeetingID = key
			payload.Decisions = append(payload.Decisions, decision)
			contributed = true
		}
		for _, action := range digest.ActionItems {
			if factDay(action.At, fallback) != day {
				continue
			}
			action.Importance = clampImportance(action.Importance)
			action.MeetingID = key
			payload.ActionItems = append(payload.ActionItems, action)
			contributed = true
		}
		for _, question := range digest.OpenQuestions {
			if factDay(question.At, fallback) != day {
				continue
			}
			question.Importance = clampImportance(question.Importance)
			question.MeetingID = key
			payload.OpenQuestions = append(payload.OpenQuestions, question)
			contributed = true
		}
		if !contributed {
			continue
		}
		payload.Meetings = append(payload.Meetings, dayDigestMeetingRef{MeetingID: key, Title: digest.Title})
		for _, theme := range digest.Themes {
			normalized := strings.ToLower(strings.TrimSpace(theme))
			if normalized == "" {
				continue
			}
			if _, ok := seenThemes[normalized]; ok {
				continue
			}
			seenThemes[normalized] = struct{}{}
			payload.Themes = append(payload.Themes, strings.TrimSpace(theme))
		}
	}
	if len(payload.Meetings) == 0 {
		return payload, false
	}
	rankDayDigestPayload(&payload)

	return payload, true
}

// rankDayDigestPayload orders each section importance-first (then by `at`,
// then text, for a stable deterministic fold) and applies the day caps —
// briefings lead with decisions and blockers, chatter falls off the end.
func rankDayDigestPayload(payload *dayDigestPayload) {
	sort.SliceStable(payload.Decisions, func(i, j int) bool {
		if payload.Decisions[i].Importance != payload.Decisions[j].Importance {
			return payload.Decisions[i].Importance > payload.Decisions[j].Importance
		}
		if payload.Decisions[i].At != payload.Decisions[j].At {
			return payload.Decisions[i].At < payload.Decisions[j].At
		}
		return payload.Decisions[i].D < payload.Decisions[j].D
	})
	sort.SliceStable(payload.Topics, func(i, j int) bool {
		if payload.Topics[i].Importance != payload.Topics[j].Importance {
			return payload.Topics[i].Importance > payload.Topics[j].Importance
		}
		if payload.Topics[i].At != payload.Topics[j].At {
			return payload.Topics[i].At < payload.Topics[j].At
		}
		return payload.Topics[i].T < payload.Topics[j].T
	})
	sort.SliceStable(payload.ActionItems, func(i, j int) bool {
		if payload.ActionItems[i].Importance != payload.ActionItems[j].Importance {
			return payload.ActionItems[i].Importance > payload.ActionItems[j].Importance
		}
		if payload.ActionItems[i].At != payload.ActionItems[j].At {
			return payload.ActionItems[i].At < payload.ActionItems[j].At
		}
		return payload.ActionItems[i].A < payload.ActionItems[j].A
	})
	sort.SliceStable(payload.OpenQuestions, func(i, j int) bool {
		if payload.OpenQuestions[i].Importance != payload.OpenQuestions[j].Importance {
			return payload.OpenQuestions[i].Importance > payload.OpenQuestions[j].Importance
		}
		if payload.OpenQuestions[i].At != payload.OpenQuestions[j].At {
			return payload.OpenQuestions[i].At < payload.OpenQuestions[j].At
		}
		return payload.OpenQuestions[i].Q < payload.OpenQuestions[j].Q
	})
	if len(payload.Decisions) > dayDigestDecisionCap {
		payload.Decisions = payload.Decisions[:dayDigestDecisionCap]
	}
	if len(payload.Topics) > dayDigestTopicCap {
		payload.Topics = payload.Topics[:dayDigestTopicCap]
	}
	if len(payload.ActionItems) > dayDigestActionCap {
		payload.ActionItems = payload.ActionItems[:dayDigestActionCap]
	}
	if len(payload.OpenQuestions) > dayDigestQuestionCap {
		payload.OpenQuestions = payload.OpenQuestions[:dayDigestQuestionCap]
	}
	if len(payload.Themes) > dayDigestThemeCap {
		payload.Themes = payload.Themes[:dayDigestThemeCap]
	}
}

// produceDayDigestPass is the day-digest agent's pass body; the wall clock is
// injected via runDayDigestPass so tests pin the day boundaries.
func (app *kanbanBoardApp) produceDayDigestPass(ctx context.Context, apiKey string, inputs []meetingMemoryEntry, responder openAITextResponder) (meetingMemoryEntry, error) {
	return app.runDayDigestPass(ctx, apiKey, inputs, responder, time.Now().UTC())
}

// runDayDigestPass: (1) rebuild the day_digest of EVERY local day the input
// meeting_digests touch — a deterministic fold over the CURRENT digests, so a
// superseded input self-heals and a marathon meeting lands one slice per day;
// (2) append the day_digest_pass cursor artifact so the window never re-feeds
// (the decision_pass pattern — required even when nothing folded); (3) ride
// the tick with amendment A3's end-of-day reflection, best-effort AFTER the
// cursor landed so a reflection failure can never re-feed the fold window.
func (app *kanbanBoardApp) runDayDigestPass(ctx context.Context, apiKey string, inputs []meetingMemoryEntry, responder openAITextResponder, now time.Time) (meetingMemoryEntry, error) {
	if app == nil || app.memory == nil || len(inputs) == 0 {
		// the runner's minBatch gate makes this unreachable on the ticker
		// path; direct callers (a future boundary flush) get a safe no-op.
		return meetingMemoryEntry{}, nil
	}
	daySet := map[string]struct{}{}
	for _, input := range inputs {
		for _, day := range meetingDigestAffectedDays(input) {
			daySet[day] = struct{}{}
		}
	}
	days := make([]string, 0, len(daySet))
	for day := range daySet {
		days = append(days, day)
	}
	sort.Strings(days)

	current := app.memory.latestDigestPerMeeting()
	rebuilt := make([]string, 0, len(days))
	for _, day := range days {
		payload, ok := foldDayDigest(day, current)
		if !ok {
			continue
		}
		canonical, err := json.Marshal(payload)
		if err != nil {
			return meetingMemoryEntry{}, err
		}
		meetingIDs := make([]string, 0, len(payload.Meetings))
		for _, meeting := range payload.Meetings {
			meetingIDs = append(meetingIDs, meeting.MeetingID)
		}
		metadata := map[string]string{
			"source":             dayDigestFoldSource,
			digestDayMetadataKey: day,
			"meetingIds":         strings.Join(meetingIDs, ","),
			"generatedAt":        now.UTC().Format(time.RFC3339),
		}
		if _, err := app.memory.upsertDigest(meetingMemoryKindDayDigest, day, string(canonical), metadata); err != nil {
			// the cursor artifact has not landed yet: the whole window
			// re-feeds next tick and the fold self-heals.
			return meetingMemoryEntry{}, err
		}
		rebuilt = append(rebuilt, day)
	}

	passText := "day digest pass: no day rebuilt"
	if len(rebuilt) > 0 {
		passText = "day digest pass: rebuilt " + strings.Join(rebuilt, ", ")
	}
	passEntry, _, err := app.memory.appendAmbientEntry(meetingMemoryKindDayDigestPass, durableTimestampID("day-digest-pass", now), passText, map[string]string{
		dayDigestCursorMetadataKey: inputs[len(inputs)-1].ID,
		"daysRebuilt":              strconv.Itoa(len(rebuilt)),
		"generatedAt":              now.UTC().Format(time.RFC3339),
	})
	if err != nil {
		return meetingMemoryEntry{}, err
	}

	if _, _, err := app.maybeEmitDailyReflection(ctx, apiKey, responder, now); err != nil {
		// best-effort: the folds and cursor already landed; the reflection
		// retries on the next tick that carries new material.
		log.Errorf("%s reflection failed: %v", dayDigestAgentName, err)
	}

	return passEntry, nil
}

/* ---------- end-of-day reflection (amendment A3) ---------- */

func reflectionInstructions() string {
	return strings.Join([]string{
		"You are Bonfire's end-of-day reflection.",
		"Synthesize ACROSS the supplied digests and decision-ledger deltas — do not re-summarize any single meeting; the digests already hold the facts.",
		"Answer the higher-order questions with concrete, named evidence:",
		"recurring blockers that keep resurfacing across meetings or days;",
		"where consensus is forming and where it is diverging;",
		"decisions being circled repeatedly WITHOUT closure;",
		"ownership drift — work whose owner keeps changing or was never clear.",
		"Write compact markdown with only the sections that have real signal, chosen from: '## Recurring blockers', '## Consensus forming', '## Consensus diverging', '## Circling without closure', '## Ownership drift'.",
		"Skip a section entirely when there is nothing real; never pad, never invent facts, people, or decisions.",
		"Speaker attribution upstream is heuristic: hedge ('attributed to X'), never assert.",
	}, " ")
}

func buildReflectionInput(day string, digests []meetingMemoryEntry, decisions []meetingMemoryEntry, prior meetingMemoryEntry, hasPrior bool, generatedAt time.Time) string {
	var builder strings.Builder
	builder.WriteString("# Generated at\n")
	builder.WriteString(generatedAt.Format(time.RFC3339))
	builder.WriteString("\n\n# Reflected day\n")
	builder.WriteString(day)

	if hasPrior {
		builder.WriteString("\n\n# Previous reflection (for continuity — is anything below STILL true, worse, or resolved?)\n")
		builder.WriteString(prior.Text)
	}

	if len(decisions) > 0 {
		builder.WriteString("\n\n# Recent decision-ledger deltas\n")
		for _, decision := range decisions {
			builder.WriteString("- ")
			builder.WriteString(decision.CreatedAt.Format(time.RFC3339))
			builder.WriteString(" | ")
			builder.WriteString(decision.Text)
			builder.WriteByte('\n')
		}
	}

	builder.WriteString("\n\n# Digest window (oldest first)\n")
	for _, digest := range digests {
		builder.WriteString("- kind=")
		builder.WriteString(digest.Kind)
		builder.WriteString(" key=")
		builder.WriteString(digestEntryKey(digest))
		builder.WriteString(" | ")
		builder.WriteString(digest.Text)
		builder.WriteByte('\n')
	}

	return builder.String()
}

// maybeEmitDailyReflection writes amendment A3's kind=reflection entry for the
// most recently COMPLETED local day: at most one per day, only when that day
// actually produced digest material, synthesized over the trailing
// reflectionLookbackDays of digests plus recent decision-ledger deltas.
// Because it rides the day-digest tick, a day with no follow-on digest
// activity is reflected on the first tick after new material lands — a
// documented lag, not a scheduler.
func (app *kanbanBoardApp) maybeEmitDailyReflection(ctx context.Context, apiKey string, responder openAITextResponder, now time.Time) (meetingMemoryEntry, bool, error) {
	if app == nil || app.memory == nil || boolEnv(reflectionDisabledEnv) {
		return meetingMemoryEntry{}, false, nil
	}
	if responder == nil {
		responder = createOpenAITextResponse
	}

	location := meetingTimeLocation()
	local := now.In(location)
	todayStart := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, location)
	dayStart := todayStart.AddDate(0, 0, -1)
	dayEnd := todayStart.Add(-time.Nanosecond)
	day := dayStart.Format(dayBucketLayout)

	if app.memory.hasReflectionForDay(day) {
		return meetingMemoryEntry{}, false, nil
	}
	// material gate: reflect only on a completed day that produced rollups.
	if len(app.memory.digestsInRange(dayStart, dayEnd)) == 0 {
		return meetingMemoryEntry{}, false, nil
	}

	windowStart := dayStart.AddDate(0, 0, -(reflectionLookbackDays - 1))
	digests := app.memory.digestsInRange(windowStart, dayEnd)
	if len(digests) > reflectionMaxDigests {
		// digestsInRange is oldest-first: keep the newest window.
		digests = digests[len(digests)-reflectionMaxDigests:]
	}
	decisions := make([]meetingMemoryEntry, 0, reflectionMaxDecisions)
	for _, decision := range app.activeDecisionEntries(reflectionMaxDecisions * 2) {
		if decision.CreatedAt.Before(windowStart) {
			continue
		}
		decisions = append(decisions, decision)
		if len(decisions) >= reflectionMaxDecisions {
			break
		}
	}
	var prior meetingMemoryEntry
	hasPrior := false
	if previous := app.memory.entriesOfKind(meetingMemoryKindReflection, 1); len(previous) > 0 {
		prior = previous[0]
		hasPrior = true
	}

	model := meetingBrainModel()
	text, err := responder(ctx, apiKey, openAITextRequest{
		Model:           model,
		Instructions:    reflectionInstructions(),
		Input:           buildReflectionInput(day, digests, decisions, prior, hasPrior, now.UTC()),
		ReasoningEffort: "low",
		Verbosity:       "low",
		MaxOutputTokens: reflectionMaxOutputTokens,
	})
	if err != nil {
		return meetingMemoryEntry{}, false, err
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return meetingMemoryEntry{}, false, nil
	}

	supporting := make([]string, 0, reflectionMaxSupportIDs)
	for index := len(digests) - 1; index >= 0 && len(supporting) < reflectionMaxSupportIDs; index-- {
		supporting = append(supporting, digests[index].ID)
	}
	entry, appended, err := app.memory.appendAmbientEntry(meetingMemoryKindReflection, durableTimestampID("reflection", now), text, map[string]string{
		digestDayMetadataKey:       day,
		"source":                   "openai_responses",
		"model":                    model,
		"supportingDigests":        strings.Join(supporting, ","),
		digestSpanStartMetadataKey: windowStart.UTC().Format(time.RFC3339),
		digestSpanEndMetadataKey:   dayEnd.UTC().Format(time.RFC3339),
		"generatedAt":              now.UTC().Format(time.RFC3339),
	})

	return entry, appended, err
}
