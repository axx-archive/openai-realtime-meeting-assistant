package main

// Mission Intelligence — the all-users stats + insight surface behind the
// intel tool. Two pieces live here:
//
//  1. missionIntelligenceSnapshot: pure aggregation over the meeting-memory
//     store (ingestion pulse counts, per-person contribution "fuel", latest
//     synthesized themes). Served by GET /assistant/mission to ANY signed-in
//     user, so the payload only ever contains counts, participant names, and
//     model-generated theme/question/alignment strings — never artifact text,
//     never private-thread text, never private-thread message counts.
//  2. The "mission intelligence" ambient agent: an agent_runner.go worker
//     that consumes brain write-ups and appends kind mission_insight entries
//     (strict JSON: themes / openQuestions / alignments), plus the
//     rate-limited on-demand refresh behind POST /assistant/mission/refresh.

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	missionIntelAgentName       = "mission intelligence"
	defaultMissionIntelInterval = 15 * time.Minute
	missionIntelRequestTimeout  = 90 * time.Second
	missionIntelRefreshTimeout  = 60 * time.Second
	// missionIntelRefreshCooldown rate-limits the on-demand refresh endpoint
	// to one accepted attempt per five minutes across all users.
	missionIntelRefreshCooldown = 5 * time.Minute
	// dominantTitleDecayTau is the half-life-ish constant for the recency
	// weighting behind the dominant meeting title. A theme/segment that recurred
	// across passes keeps a high Σ e^(−Δt/τ) score; a single last-tick blip
	// decays away — so the title stops chasing whatever topic the most recent
	// pass happened to surface (the max-mentions/last-tick bug). τ ≈ 25m sits in
	// the founder's "a few minutes to half an hour" band.
	dominantTitleDecayTau = 25 * time.Minute
)

func missionIntelligenceAgent() ambientAgentConfig {
	return ambientAgentConfig{
		name:              missionIntelAgentName,
		defaultInterval:   defaultMissionIntelInterval,
		intervalEnv:       "MISSION_INTEL_INTERVAL",
		disabledEnv:       "MISSION_INTEL_DISABLED",
		backfillEnv:       "MISSION_INTEL_BACKFILL",
		minBatchEnv:       "MISSION_INTEL_MIN_INPUTS",
		defaultMinBatch:   1,
		maxBatchEnv:       "MISSION_INTEL_MAX_INPUTS",
		defaultMaxBatch:   12,
		inputKind:         meetingMemoryKindBrain,
		artifactKind:      meetingMemoryKindMissionInsight,
		roomScoped:        true, // W4 §7.4: titles THE ROOM's record off its own brains
		cursorMetadataKey: "throughBrainId",
		requestTimeout:    missionIntelRequestTimeout,
		produce:           (*kanbanBoardApp).produceMissionInsight,
	}
}

func (app *kanbanBoardApp) startMissionIntelligenceWorker(apiKey string) {
	app.startAmbientAgent(missionIntelligenceAgent(), apiKey)
}

type missionInsightTheme struct {
	Label    string   `json:"label"`
	Summary  string   `json:"summary"`
	Mentions int      `json:"mentions"`
	People   []string `json:"people"`
}

type missionInsightPayload struct {
	Themes        []missionInsightTheme `json:"themes"`
	OpenQuestions []string              `json:"openQuestions"`
	Alignments    []string              `json:"alignments"`
}

// parseMissionInsight validates agent output: strict JSON, with a tolerance
// for a stray markdown fence (the fence is stripped before persisting so the
// stored text is always plain JSON).
func parseMissionInsight(text string) (missionInsightPayload, string, bool) {
	text = strings.TrimSpace(text)
	if fenced := strings.TrimPrefix(text, "```json"); fenced != text {
		text = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(fenced), "```"))
	} else if fenced := strings.TrimPrefix(text, "```"); fenced != text {
		text = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(fenced), "```"))
	}
	var insight missionInsightPayload
	if text == "" || json.Unmarshal([]byte(text), &insight) != nil {
		return missionInsightPayload{}, "", false
	}

	return insight, text, true
}

func missionIntelInstructions() string {
	return strings.Join([]string{
		"You are Bonfire's mission intelligence.",
		"From the supplied meeting-brain write-ups, decisions, and workspace signals, return STRICT JSON only, no markdown fence, matching:",
		`{"themes":[{"label":string(<=6 words),"summary":string(<=200 chars),"mentions":int,"people":[string]}],"openQuestions":[string],"alignments":[string]}.`,
		"themes = recurring topics across the window (max 6, most recurrent first).",
		"Count mentions from the CURRENT window only; never inherit or add to counts from a previous insight.",
		"openQuestions = unresolved questions or disagreements the team should settle (max 5).",
		"alignments = points where the team is already in lockstep (max 5).",
		"Preserve real participant names; never invent people, clients, or decisions.",
		"If the window is thin, return fewer items, never filler.",
	}, " ")
}

// buildMissionIntelInput assembles the agent input: the brain write-up
// window plus continuity context. Artifact TITLES only — artifact text is
// admin-gated and must never reach this all-users pipeline.
func (app *kanbanBoardApp) buildMissionIntelInput(inputs []meetingMemoryEntry, generatedAt time.Time) string {
	var builder strings.Builder
	builder.WriteString("# Generated at\n")
	builder.WriteString(generatedAt.Format(time.RFC3339))

	// Continuity context — the previous insight's themes carried forward, but
	// WITHOUT the mentions ratchet: (i) the counts are stripped so the model
	// re-derives salience from THIS window instead of inheriting an inflated
	// running tally, and (ii) an insight synthesized before the live sitting
	// started belongs to the PRIOR sitting and is dropped entirely (the seam the
	// stale Samsung(15) title leaked through — one meeting id can span two
	// sittings, so we key the reset off record.StartedAt, never the id).
	roomID := ambientWindowRoomID(inputs)
	if previous := app.memory.entriesOfKind(meetingMemoryKindMissionInsight, 0); len(previous) > 0 {
		prev, hasPrev := newestEntryForRoom(previous, roomID)
		if hasPrev && app.previousInsightBelongsToSitting(prev, roomID) {
			if block := sanitizedPreviousInsight(prev.Text); block != "" {
				builder.WriteString("\n\n# Previous mission insight (themes carried forward — recount mentions from THIS window, do not inherit prior counts)\n")
				builder.WriteString(block)
			}
		}
	}

	// The decision ledger is the source of truth once it has entries; the
	// keyword scan survives only as the cold-start fallback.
	decisions := make([]string, 0, 10)
	for _, entry := range app.activeDecisionEntries(10) {
		decisions = append(decisions, entry.Text)
	}
	if len(decisions) == 0 {
		decisions = extractDecisionItems(app.memory.snapshot(0), 10)
	}
	if len(decisions) > 0 {
		builder.WriteString("\n\n# Recent decision signals\n")
		for _, decision := range decisions {
			builder.WriteString("- ")
			builder.WriteString(decision)
			builder.WriteByte('\n')
		}
	}

	if artifacts := app.memory.entriesOfKind(meetingMemoryKindOSArtifact, 15); len(artifacts) > 0 {
		builder.WriteString("\n\n# Recent artifact titles (titles only)\n")
		for _, artifact := range artifacts {
			// entriesOfKind is unfiltered by relevance: re-apply the recall guard
			// so a quarantined/expired draft's title never enters model context.
			if memoryEntryHiddenFromRecall(artifact) {
				continue
			}
			builder.WriteString("- ")
			builder.WriteString(firstNonEmptyString(strings.TrimSpace(artifact.Metadata["title"]), "untitled"))
			if mode := strings.TrimSpace(artifact.Metadata["mode"]); mode != "" {
				builder.WriteString(" (")
				builder.WriteString(mode)
				builder.WriteString(")")
			}
			builder.WriteByte('\n')
		}
	}

	builder.WriteString("\n\n# Board context\n")
	builder.WriteString(boardContextLine(app.snapshotState()))

	builder.WriteString("\n\n# Brain write-up window\n")
	for _, entry := range inputs {
		builder.WriteString("- ")
		builder.WriteString(entry.ID)
		builder.WriteString(" | ")
		builder.WriteString(entry.CreatedAt.Format(time.RFC3339))
		builder.WriteString(" | ")
		builder.WriteString(entry.Text)
		builder.WriteByte('\n')
	}

	return builder.String()
}

func (app *kanbanBoardApp) produceMissionInsight(ctx context.Context, apiKey string, inputs []meetingMemoryEntry, responder openAITextResponder) (meetingMemoryEntry, error) {
	roomID := ambientWindowRoomID(inputs)
	model := meetingBrainModel()
	text, err := responder(ctx, apiKey, openAITextRequest{
		Model:           model,
		Seat:            seatMissionIntel,
		Instructions:    missionIntelInstructions(),
		Input:           app.buildMissionIntelInput(inputs, time.Now().UTC()),
		ReasoningEffort: "low",
		Verbosity:       "low",
		MaxOutputTokens: 900,
	})
	if err != nil {
		return meetingMemoryEntry{}, err
	}
	insight, cleanText, ok := parseMissionInsight(text)
	if !ok {
		// Never persist unparseable output: the cursor stays put, so the
		// next pass (or an on-demand refresh) retries with more input.
		recordEvalEvent(seatMissionIntel, evalKindParseFailure, map[string]any{"seat": seatMissionIntel, "model": model})
		log.Errorf("%s returned non-JSON output; skipping this pass", missionIntelAgentName)
		return meetingMemoryEntry{}, nil
	}

	firstBrain := inputs[0]
	lastBrain := inputs[len(inputs)-1]
	metadata := map[string]string{
		"source":         "openai_responses",
		"model":          model,
		"roomId":         roomID,
		"fromBrainId":    firstBrain.ID,
		"throughBrainId": lastBrain.ID,
		"brainCount":     strconv.Itoa(len(inputs)),
		"generatedAt":    time.Now().UTC().Format(time.RFC3339),
	}
	id := durableTimestampID("mission-insight", time.Now())
	entry, appended, err := app.memory.appendMissionInsight(id, cleanText, metadata)
	if err != nil || !appended {
		return entry, err
	}

	broadcastOfficeKanbanEvent("mission_insight", missionInsightEventPayload(entry, insight))

	// server-side auto-title: recency-weighted salience over the CURRENT sitting
	// (narrative segments when the maintainer has produced them, decayed mission
	// themes otherwise) — NOT raw max-mentions, so a single last-tick blip can
	// never steal the title from a topic that owned the airtime.
	// refreshDominantTitle no-ops when no meeting is live. W4: it titles THE
	// WINDOW'S ROOM's active record, never the office's by default.
	app.refreshDominantTitle(roomID, time.Now().UTC())

	return entry, nil
}

// newestEntryForRoom picks the newest entry (entriesOfKind order is oldest
// first) whose roomId resolves to roomID — the W4 room dimension on the
// continuity carries. Absent roomId reads as office (§9).
func newestEntryForRoom(entries []meetingMemoryEntry, roomID string) (meetingMemoryEntry, bool) {
	roomID = normalizeRoomID(roomID)
	for index := len(entries) - 1; index >= 0; index-- {
		if normalizeRoomID(entries[index].Metadata["roomId"]) == roomID {
			return entries[index], true
		}
	}
	return meetingMemoryEntry{}, false
}

// refreshDominantTitle recomputes the active meeting's auto title from
// already-produced salience — no extra model call. Called from the mission
// tick (15m) AND every narrative-maintainer pass (10m), so the room title
// tracks the conversation on a ~10-minute cadence instead of only the final
// mission tick. Scope keys off record.StartedAt, never the meeting id (one id
// can span sittings — the load-bearing gotcha).
func (app *kanbanBoardApp) refreshDominantTitle(roomID string, now time.Time) {
	if app == nil || app.meetings == nil {
		return
	}
	record, ok := app.meetings.activeRecord(normalizeRoomID(roomID))
	if !ok {
		return
	}
	label := app.dominantMeetingTitle(record, now)
	if strings.TrimSpace(label) == "" {
		return
	}
	if updated, changed := app.meetings.setAutoTitle(record.ID, label); changed {
		app.broadcastMeetingRecord(updated)
	}
}

// dominantMeetingTitle is the single source of the room's dominant title: the
// most-salient narrative segment of the current sitting when segmentation
// exists (so the title is always a row of the topic timeline and the two can
// never contradict), falling back to decayed mission-theme salience on cold
// start before the first narrative pass of the sitting lands.
func (app *kanbanBoardApp) dominantMeetingTitle(record meetingRecord, now time.Time) string {
	segments := app.meetingSegments(record, now)
	if idx := dominantSegmentIndex(segments); idx >= 0 {
		if title := strings.TrimSpace(segments[idx].Title); title != "" {
			return title
		}
	}
	return app.decayedDominantTheme(record, now)
}

// decayedDominantTheme scores every mission-insight theme in the current
// sitting by decayed recurrence — Σ e^(−Δt/τ) over the passes that carried the
// label — and returns the argmax. Ties break toward the earliest-seen label
// (themes arrive most-recurrent-first). This replaces the old max-mentions
// reduce, which lagged (last-tick wins) and rode the model-invented, ratcheted
// mentions integer.
func (app *kanbanBoardApp) decayedDominantTheme(record meetingRecord, now time.Time) string {
	if app == nil || app.memory == nil {
		return ""
	}
	startedAt, ok := parseMeetingStartedAt(record)
	if !ok {
		return ""
	}
	weights := map[string]float64{}
	order := make([]string, 0, 8)
	recordRoom := meetingRoomID(record)
	for _, entry := range app.memory.entriesOfKind(meetingMemoryKindMissionInsight, 0) {
		if entry.CreatedAt.Before(startedAt) {
			continue
		}
		// W4: another room's insights never weigh this room's title.
		if normalizeRoomID(entry.Metadata["roomId"]) != recordRoom {
			continue
		}
		insight, _, parsed := parseMissionInsight(entry.Text)
		if !parsed {
			continue
		}
		decay := decayedWeight(now, entry.CreatedAt)
		for _, theme := range insight.Themes {
			label := strings.TrimSpace(theme.Label)
			if label == "" {
				continue
			}
			if _, seen := weights[label]; !seen {
				order = append(order, label)
			}
			weights[label] += decay
		}
	}
	best := ""
	bestWeight := 0.0
	for _, label := range order {
		if best == "" || weights[label] > bestWeight {
			best = label
			bestWeight = weights[label]
		}
	}
	return best
}

// decayedWeight is one recency-decayed vote: 1.0 at now, falling off with
// e^(−Δt/τ). Negative Δt (a clock skew where the entry looks newer than now)
// clamps to the full 1.0 vote rather than amplifying past it.
func decayedWeight(now time.Time, at time.Time) float64 {
	dt := now.Sub(at)
	if dt < 0 {
		dt = 0
	}
	return math.Exp(-dt.Minutes() / dominantTitleDecayTau.Minutes())
}

// parseMeetingStartedAt is the sitting-boundary anchor every title/segment
// scope filter keys off — record.StartedAt, never the meeting id's mint time.
func parseMeetingStartedAt(record meetingRecord) (time.Time, bool) {
	startedAt, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(record.StartedAt))
	if err != nil {
		return time.Time{}, false
	}
	return startedAt, true
}

// previousInsightBelongsToSitting reports whether a prior mission insight is
// safe to prime the next pass with: only when it was generated at/after the
// live sitting started. An insight older than record.StartedAt belongs to the
// previous sitting and would leak its themes forward (the priming ratchet).
// With no active record we cannot prove staleness, so we allow priming.
func (app *kanbanBoardApp) previousInsightBelongsToSitting(prev meetingMemoryEntry, roomID string) bool {
	if app == nil || app.meetings == nil {
		return true
	}
	record, ok := app.meetings.activeRecord(normalizeRoomID(roomID))
	if !ok {
		return true
	}
	startedAt, ok := parseMeetingStartedAt(record)
	if !ok {
		return true
	}
	generatedAt := prev.CreatedAt
	if parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(prev.Metadata["generatedAt"])); err == nil {
		generatedAt = parsed
	}
	return !generatedAt.Before(startedAt)
}

// sanitizedPreviousInsight renders the previous insight as continuity context
// with the mentions integers stripped — labels, summaries, open questions, and
// alignments only. Unparseable text primes nothing (returns "").
func sanitizedPreviousInsight(text string) string {
	insight, _, ok := parseMissionInsight(text)
	if !ok {
		return ""
	}
	var b strings.Builder
	if len(insight.Themes) > 0 {
		b.WriteString("Themes:\n")
		for _, theme := range insight.Themes {
			label := strings.TrimSpace(theme.Label)
			if label == "" {
				continue
			}
			b.WriteString("- ")
			b.WriteString(label)
			if summary := strings.TrimSpace(theme.Summary); summary != "" {
				b.WriteString(": ")
				b.WriteString(summary)
			}
			b.WriteByte('\n')
		}
	}
	if len(insight.OpenQuestions) > 0 {
		b.WriteString("Open questions:\n")
		for _, question := range insight.OpenQuestions {
			if trimmed := strings.TrimSpace(question); trimmed != "" {
				b.WriteString("- ")
				b.WriteString(trimmed)
				b.WriteByte('\n')
			}
		}
	}
	if len(insight.Alignments) > 0 {
		b.WriteString("Alignments:\n")
		for _, alignment := range insight.Alignments {
			if trimmed := strings.TrimSpace(alignment); trimmed != "" {
				b.WriteString("- ")
				b.WriteString(trimmed)
				b.WriteByte('\n')
			}
		}
	}
	return strings.TrimSpace(b.String())
}

func missionInsightEventPayload(entry meetingMemoryEntry, insight missionInsightPayload) map[string]any {
	brainCount, _ := strconv.Atoi(entry.Metadata["brainCount"])
	return map[string]any{
		"id":          entry.ID,
		"createdAt":   entry.CreatedAt.UTC().Format(time.RFC3339Nano),
		"generatedAt": firstNonEmptyString(entry.Metadata["generatedAt"], entry.CreatedAt.UTC().Format(time.RFC3339)),
		"brainCount":  brainCount,
		"insight":     insight,
	}
}

/* ---------- aggregation ---------- */

type missionContributionRow struct {
	Name               string `json:"name"`
	Spoken             int    `json:"spoken"`
	Chat               int    `json:"chat"`
	ChannelMessages    int    `json:"channelMessages"`
	ThreadsStarted     int    `json:"threadsStarted"`
	ProposalsConfirmed int    `json:"proposalsConfirmed"`
	ArtifactsCreated   int    `json:"artifactsCreated"`
	BoardCardsOwned    int    `json:"boardCardsOwned"`
	Fuel               int    `json:"fuel"`
}

func missionContributionFuel(row missionContributionRow) int {
	return row.Spoken + row.Chat + row.ChannelMessages + 3*row.ThreadsStarted + 3*row.ProposalsConfirmed + 5*row.ArtifactsCreated
}

// missionPulseHistogramBuckets × missionPulseHistogramBucket = the rolling
// window the ingestion pulse chart draws (36 five-minute bars, last 3h).
const (
	missionPulseHistogramBuckets = 36
	missionPulseHistogramBucket  = 5 * time.Minute
)

// missionPulseHistogram buckets memory ingestion into the pulse chart's 36
// five-minute bars ending at now. Scout chat threads are skipped: they
// rewrite in place, so CreatedAt is the thread's birth, not activity.
func missionPulseHistogram(entries []meetingMemoryEntry, now time.Time) []int {
	counts := make([]int, missionPulseHistogramBuckets)
	start := now.Add(-time.Duration(missionPulseHistogramBuckets) * missionPulseHistogramBucket)
	for _, entry := range entries {
		if entry.Kind == meetingMemoryKindScoutChat {
			continue
		}
		offset := entry.CreatedAt.Sub(start)
		if offset < 0 {
			continue
		}
		index := int(offset / missionPulseHistogramBucket)
		if index >= missionPulseHistogramBuckets {
			index = missionPulseHistogramBuckets - 1
		}
		counts[index]++
	}
	return counts
}

// missionTranscriptLineCount is the all-time transcript-line total the intel
// stat tiles show — real ingestion, never a synthetic ticker (D2).
func missionTranscriptLineCount(entries []meetingMemoryEntry) int {
	count := 0
	for _, entry := range entries {
		if entry.Kind == meetingMemoryKindTranscript {
			count++
		}
	}
	return count
}

// missionPulseWindow counts pipeline activity newer than since. Thread
// activity uses the thread's updatedAt (threads rewrite in place), everything
// else buckets on CreatedAt.
func missionPulseWindow(entries []meetingMemoryEntry, since time.Time) map[string]int {
	counts := map[string]int{
		"spokenTranscripts": 0,
		"roomChatMessages":  0,
		"brainWriteUps":     0,
		"boardUpdates":      0,
		"artifactsCreated":  0,
		"threadsActive":     0,
		"proposals":         0,
		"meetingsArchived":  0,
	}
	for _, entry := range entries {
		if entry.Kind == meetingMemoryKindScoutChat {
			updatedAt := entry.CreatedAt
			if parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(entry.Metadata["updatedAt"])); err == nil {
				updatedAt = parsed
			}
			if updatedAt.After(since) {
				counts["threadsActive"]++
			}
			continue
		}
		if !entry.CreatedAt.After(since) {
			continue
		}
		switch entry.Kind {
		case meetingMemoryKindTranscript:
			if entry.Metadata["source"] == transcriptSourceRoomChat {
				counts["roomChatMessages"]++
			} else {
				counts["spokenTranscripts"]++
			}
		case meetingMemoryKindBrain:
			counts["brainWriteUps"]++
		case meetingMemoryKindBoardUpdate:
			counts["boardUpdates"]++
		case meetingMemoryKindOSArtifact:
			counts["artifactsCreated"]++
		case meetingMemoryKindCodexProposal:
			counts["proposals"]++
		case meetingMemoryKindArchive:
			counts["meetingsArchived"]++
		}
	}

	return counts
}

// missionContributions attributes counts to canonical participants. Hard
// rule: private-thread message contents AND counts stay private — only
// public-channel messages are counted, and only as counts, never text.
func missionContributions(entries []meetingMemoryEntry, board kanbanBoardState) ([]missionContributionRow, int, int) {
	byName := make(map[string]*missionContributionRow, len(meetingParticipantNames))
	for _, name := range meetingParticipantNames {
		byName[name] = &missionContributionRow{Name: name}
	}
	credit := func(name string) *missionContributionRow {
		return byName[canonicalParticipantName(name)]
	}

	unattributed := 0
	for _, entry := range entries {
		switch entry.Kind {
		case meetingMemoryKindTranscript:
			speaker := strings.TrimSpace(entry.Metadata["speaker"])
			if entry.Metadata["source"] == transcriptSourceRoomChat {
				if row := credit(speaker); row != nil {
					row.Chat++
				}
				continue
			}
			if speaker == "" {
				unattributed++
				continue
			}
			// "A + B" composite attributions credit every named speaker.
			for _, part := range strings.Split(speaker, "+") {
				if row := credit(strings.TrimSpace(part)); row != nil {
					row.Spoken++
				} else {
					unattributed++
				}
			}
		case meetingMemoryKindScoutChat:
			thread, ok := decodeScoutChatThreadEntry(entry)
			if !ok {
				continue
			}
			starter := credit(thread.CreatedBy)
			if starter == nil {
				starter = credit(participantNameForEmail(thread.OwnerEmail))
			}
			if starter != nil {
				starter.ThreadsStarted++
			}
			if scoutChatThreadVisibility(thread) != scoutChatVisibilityPublic {
				continue
			}
			for _, message := range thread.Messages {
				if message.Role != "user" {
					continue
				}
				author := credit(message.AuthorName)
				if author == nil {
					author = credit(participantNameForEmail(message.AuthorEmail))
				}
				if author != nil {
					author.ChannelMessages++
				}
			}
		case meetingMemoryKindCodexProposal:
			if row := credit(entry.Metadata["confirmedBy"]); row != nil {
				row.ProposalsConfirmed++
			}
		case meetingMemoryKindOSArtifact:
			if row := credit(entry.Metadata["createdBy"]); row != nil {
				row.ArtifactsCreated++
			}
		}
	}
	for _, card := range board.Cards {
		if row := credit(card.Owner); row != nil {
			row.BoardCardsOwned++
		}
	}

	fuelMax := 0
	rows := make([]missionContributionRow, 0, len(byName))
	for _, name := range meetingParticipantNames {
		row := byName[name]
		row.Fuel = missionContributionFuel(*row)
		if row.Fuel > fuelMax {
			fuelMax = row.Fuel
		}
		rows = append(rows, *row)
	}
	// fuel desc, roster order as the stable tiebreak — counts, not rankings.
	for outer := 0; outer < len(rows); outer++ {
		for inner := outer + 1; inner < len(rows); inner++ {
			if rows[inner].Fuel > rows[outer].Fuel {
				rows[outer], rows[inner] = rows[inner], rows[outer]
			}
		}
	}

	return rows, unattributed, fuelMax
}

// latestMissionInsight returns the newest parseable mission_insight entry
// shaped for the GET payload, or nil when none exists.
func (app *kanbanBoardApp) latestMissionInsight() map[string]any {
	if app == nil || app.memory == nil {
		return nil
	}
	latest := app.memory.entriesOfKind(meetingMemoryKindMissionInsight, 1)
	if len(latest) == 0 {
		return nil
	}
	insight, _, ok := parseMissionInsight(latest[0].Text)
	if !ok {
		return nil
	}

	return missionInsightEventPayload(latest[0], insight)
}

// missionIntelligenceSnapshot builds the full GET /assistant/mission payload.
func (app *kanbanBoardApp) missionIntelligenceSnapshot(now time.Time) map[string]any {
	degraded := []string{}
	if app == nil || app.memory == nil {
		return map[string]any{
			"generatedAt":     now.UTC().Format(time.RFC3339Nano),
			"pulse":           map[string]any{"last24h": missionPulseWindow(nil, now), "last7d": missionPulseWindow(nil, now), "totalEntries": 0, "lastIngestAt": "", "currentMeetingId": "", "liveParticipants": 0, "histogram": missionPulseHistogram(nil, now), "transcriptLines": 0, "meetingsToday": 0, "meetingsThisWeek": 0},
			"contributions":   map[string]any{"people": []missionContributionRow{}, "unattributed": 0, "fuelMax": 0},
			"themes":          nil,
			"themesAvailable": false,
			"decisions":       []map[string]any{},
			"narratives":      []map[string]any{},
			"segments":        []map[string]any{},
			"degraded":        degraded,
		}
	}

	entries := app.memory.snapshot(0)
	board := app.snapshotState()

	lastIngestAt := ""
	newest := time.Time{}
	for _, entry := range entries {
		if entry.CreatedAt.After(newest) {
			newest = entry.CreatedAt
		}
	}
	if !newest.IsZero() {
		lastIngestAt = newest.UTC().Format(time.RFC3339)
	}
	pulse := map[string]any{
		"last24h":      missionPulseWindow(entries, now.Add(-24*time.Hour)),
		"last7d":       missionPulseWindow(entries, now.Add(-7*24*time.Hour)),
		"totalEntries": len(entries),
		"lastIngestAt": lastIngestAt,
		// currentMeetingId is the lazy ingest-session id — non-empty whenever
		// unarchived memory exists, so it must never drive "meeting live".
		// liveParticipants is real room occupancy (the same seat count the
		// /participants snapshot reports) and is what liveness keys on.
		"currentMeetingId": app.memory.currentMeetingID(officeRoomID),
		"liveParticipants": app.activeParticipantCount(officeRoomID),
		// The pulse chart + stat tiles (design §9.1): a real ingestion
		// histogram and real counters — never the prototype's fake ticker.
		"histogram":       missionPulseHistogram(entries, now),
		"transcriptLines": missionTranscriptLineCount(entries),
	}
	meetingsToday, meetingsThisWeek := app.meetings.countStartedSince(now)
	pulse["meetingsToday"] = meetingsToday
	pulse["meetingsThisWeek"] = meetingsThisWeek

	people, unattributed, fuelMax := missionContributions(entries, board)
	contributions := map[string]any{
		"people":       people,
		"unattributed": unattributed,
		"fuelMax":      fuelMax,
	}

	themes := app.latestMissionInsight()
	if strings.TrimSpace(app.currentAPIKey()) == "" {
		degraded = append(degraded, "openai_api_key_missing")
	}

	return map[string]any{
		"generatedAt":     now.UTC().Format(time.RFC3339Nano),
		"pulse":           pulse,
		"contributions":   contributions,
		"themes":          themes,
		"themesAvailable": themes != nil,
		"decisions":       app.decisionLedgerSnapshot(decisionSnapshotLimit),
		// Active storyline dossiers (narrative_maintainer.go), newest first —
		// identity + one-line summary only, the same no-artifact-text law as
		// the rest of this all-users payload. Additive: absent narratives read
		// back as an empty list.
		"narratives": app.narrativeSnapshotRows(narrativeStorylineContextLimit),
		// Per-sitting topic segments (narrative_maintainer.go), ordered by
		// firstSeenAt with [firstSeen,lastSeen] and a current/past status. The
		// dominant-title segment is marked "current" — the SAME segmentation the
		// server auto-title is drawn from, so timeline and title never diverge.
		"segments": app.meetingSegmentRows(now),
		"degraded": degraded,
	}
}

func (app *kanbanBoardApp) currentAPIKey() string {
	if app == nil {
		return ""
	}
	app.mu.Lock()
	defer app.mu.Unlock()

	return app.apiKey
}

// beginMissionIntelRefresh reserves the on-demand refresh slot: one accepted
// attempt per cooldown window, first caller wins. The previous stamp is
// returned so a failed run can roll its reservation back.
func (app *kanbanBoardApp) beginMissionIntelRefresh(now time.Time) (time.Time, bool) {
	app.mu.Lock()
	defer app.mu.Unlock()

	if !app.missionIntelRefreshAt.IsZero() && now.Sub(app.missionIntelRefreshAt) < missionIntelRefreshCooldown {
		return time.Time{}, false
	}
	previous := app.missionIntelRefreshAt
	app.missionIntelRefreshAt = now

	return previous, true
}

// missionIntelRefreshRetryAfterSeconds reports how long until the on-demand
// refresh slot frees up, for the 429 Retry-After header.
func (app *kanbanBoardApp) missionIntelRefreshRetryAfterSeconds(now time.Time) int {
	app.mu.Lock()
	defer app.mu.Unlock()

	remaining := missionIntelRefreshCooldown - now.Sub(app.missionIntelRefreshAt)
	if remaining < time.Second {
		return 1
	}
	return int(remaining.Round(time.Second) / time.Second)
}

// releaseMissionIntelRefresh rolls a reserved refresh slot back after a run
// that produced nothing, so a transient model error (or a canceled request)
// does not lock every user out of refresh for the full cooldown. Only the
// reservation that is still current is rolled back.
func (app *kanbanBoardApp) releaseMissionIntelRefresh(previous time.Time, reservedAt time.Time) {
	app.mu.Lock()
	defer app.mu.Unlock()

	if app.missionIntelRefreshAt.Equal(reservedAt) {
		app.missionIntelRefreshAt = previous
	}
}

/* ---------- HTTP ---------- */

// assistantMissionHandler serves GET /assistant/mission with the same origin
// + session guards as the notifications handler. Any signed-in user may read
// it — deliberately NO canAccessArtifactLibrary gate: the payload carries
// counts and generated themes only, never artifact text.
func assistantMissionHandler(w http.ResponseWriter, r *http.Request) {
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
		writeAuthError(w, http.StatusServiceUnavailable, "mission intelligence is unavailable")
		return
	}

	writeAuthJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"mission": kanbanApp.missionIntelligenceSnapshot(time.Now()),
	})
}

// assistantMissionRefreshHandler serves POST /assistant/mission/refresh: an
// on-demand themes pass for any signed-in user. Keyless deployments answer
// politely instead of erroring; the cooldown keeps refresh spam off the
// model, and the per-agent run lock serializes any concurrent passes.
func assistantMissionRefreshHandler(w http.ResponseWriter, r *http.Request) {
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
		writeAuthError(w, http.StatusServiceUnavailable, "mission intelligence is unavailable")
		return
	}

	apiKey := kanbanApp.currentAPIKey()
	if strings.TrimSpace(apiKey) == "" {
		writeAuthJSON(w, http.StatusOK, map[string]any{
			"ok":        true,
			"refreshed": false,
			"reason":    "openai_api_key_missing",
			"mission":   kanbanApp.missionIntelligenceSnapshot(time.Now()),
		})
		return
	}
	agent := missionIntelligenceAgent()
	if boolEnv(agent.disabledEnv) || agent.interval() <= 0 {
		writeAuthJSON(w, http.StatusOK, map[string]any{
			"ok":        true,
			"refreshed": false,
			"reason":    "mission_intel_disabled",
			"mission":   kanbanApp.missionIntelligenceSnapshot(time.Now()),
		})
		return
	}
	reservedAt := time.Now()
	previousRefreshAt, ok := kanbanApp.beginMissionIntelRefresh(reservedAt)
	if !ok {
		// Retry-After lets the client disable the refresh button with an
		// honest countdown instead of appearing dead for the cooldown.
		w.Header().Set("Retry-After", strconv.Itoa(kanbanApp.missionIntelRefreshRetryAfterSeconds(reservedAt)))
		writeAuthError(w, http.StatusTooManyRequests, "mission intelligence was refreshed recently")
		return
	}

	kanbanApp.ensureAmbientAgentBaseline(agent)
	ctx, cancel := context.WithTimeout(r.Context(), missionIntelRefreshTimeout)
	defer cancel()
	entry, err := kanbanApp.runAmbientAgentOnce(agent, ctx, apiKey, nil, 1)
	if err != nil {
		// nothing was produced — release the shared cooldown slot so a
		// transient failure doesn't lock everyone out for five minutes
		kanbanApp.releaseMissionIntelRefresh(previousRefreshAt, reservedAt)
		writeAuthError(w, http.StatusBadGateway, err.Error())
		return
	}

	writeAuthJSON(w, http.StatusOK, map[string]any{
		"ok":        true,
		"refreshed": entry.ID != "",
		"mission":   kanbanApp.missionIntelligenceSnapshot(time.Now()),
	})
}
