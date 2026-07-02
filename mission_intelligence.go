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

	if previous := app.memory.entriesOfKind(meetingMemoryKindMissionInsight, 1); len(previous) > 0 {
		builder.WriteString("\n\n# Previous mission insight\n")
		builder.WriteString(previous[0].Text)
	}

	if decisions := extractDecisionItems(app.memory.snapshot(0), 10); len(decisions) > 0 {
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
	model := meetingBrainModel()
	text, err := responder(ctx, apiKey, openAITextRequest{
		Model:           model,
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
		log.Errorf("%s returned non-JSON output; skipping this pass", missionIntelAgentName)
		return meetingMemoryEntry{}, nil
	}

	firstBrain := inputs[0]
	lastBrain := inputs[len(inputs)-1]
	metadata := map[string]string{
		"source":         "openai_responses",
		"model":          model,
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

	broadcastKanbanEvent("mission_insight", missionInsightEventPayload(entry, insight))

	return entry, nil
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
			"pulse":           map[string]any{"last24h": missionPulseWindow(nil, now), "last7d": missionPulseWindow(nil, now), "totalEntries": 0, "lastIngestAt": "", "currentMeetingId": "", "liveParticipants": 0},
			"contributions":   map[string]any{"people": []missionContributionRow{}, "unattributed": 0, "fuelMax": 0},
			"themes":          nil,
			"themesAvailable": false,
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
		"currentMeetingId": app.memory.currentMeetingID(),
		"liveParticipants": app.activeParticipantCount(),
	}

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
		"degraded":        degraded,
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
