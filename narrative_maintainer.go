package main

// Narrative threads — the brain's per-storyline dossiers. The narrative
// maintainer is an agent_runner.go ambient agent that consumes brain
// write-ups (the mission-intelligence input) and maintains ONE living
// kind=narrative entry per active storyline: an opportunity, client, or
// project (e.g. "samsung-tv-plus"). Each dossier carries the storyline's
// history — status, dated timeline, people, concerns, the deliverables and
// runs produced for it, and the feedback those deliverables earned — so
// "fill me in on the history of the Samsung opportunity" answers from ONE
// searchable body instead of a lexical scatter across transcripts.
//
// Lifecycle law: exactly one ACTIVE entry per slug. An update appends the new
// version and expires the predecessor via the relevance lifecycle
// (memory.go), so store.search and the mission snapshot only ever see the
// latest. Expired versions stay on disk — the dossier's own history is never
// deleted, just hidden from recall.
//
// Model seam: Sonnet fronts the maintainer whenever an Anthropic key is
// present (the answer engine's split in memory_query.go); keyless-Anthropic
// rides the chassis's OpenAI responder, and a fully keyless deploy simply
// never starts the agent — mission intelligence's degraded posture.

import (
	"context"
	"encoding/json"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	narrativeMaintainerAgentName       = "narrative maintainer"
	defaultNarrativeMaintainerInterval = 10 * time.Minute
	narrativeMaintainerRequestTimeout  = 90 * time.Second
	// narrativeBodyMaxChars caps one dossier body (runes): enough for the
	// eight sections without letting one storyline crowd recall context.
	narrativeBodyMaxChars = 6000
	// narrativeSlugMaxLen bounds a normalized storyline slug.
	narrativeSlugMaxLen = 60
	// narrativeStorylineContextLimit caps the "# Active storylines" section
	// pinned into every assistant query input (memory_query.go).
	narrativeStorylineContextLimit = 8
	// store-derived context bounds for one maintainer pass.
	narrativeContextArtifacts = 15
	narrativeContextRunLogs   = 12
	narrativeContextSignals   = 20
	narrativeContextDecisions = 10
	// narrativeCursorKey mirrors mission intelligence: both agents consume the
	// brain stream, each stamping the consumed-through brain id on its OWN
	// artifact kind, so the cursors never collide.
	narrativeCursorKey = "throughBrainId"
)

func narrativeMaintainerAgent() ambientAgentConfig {
	return ambientAgentConfig{
		name:              narrativeMaintainerAgentName,
		defaultInterval:   defaultNarrativeMaintainerInterval,
		intervalEnv:       "NARRATIVE_MAINTAINER_INTERVAL",
		disabledEnv:       "NARRATIVE_MAINTAINER_DISABLED",
		backfillEnv:       "NARRATIVE_MAINTAINER_BACKFILL",
		minBatchEnv:       "NARRATIVE_MAINTAINER_MIN_INPUTS",
		defaultMinBatch:   1,
		maxBatchEnv:       "NARRATIVE_MAINTAINER_MAX_INPUTS",
		defaultMaxBatch:   12,
		inputKind:         meetingMemoryKindBrain,
		artifactKind:      meetingMemoryKindNarrative,
		cursorMetadataKey: narrativeCursorKey,
		requestTimeout:    narrativeMaintainerRequestTimeout,
		roomScoped:        true, // W4 §7.4: segments THE ROOM's sitting off its own brains
		produce:           (*kanbanBoardApp).produceNarrativeUpdates,
	}
}

func (app *kanbanBoardApp) startNarrativeMaintainerWorker(apiKey string) {
	app.startAmbientAgent(narrativeMaintainerAgent(), apiKey)
}

// narrativeMaintainerEffort is the maintainer's thinking depth on both model
// paths (the Sonnet call and the keyless-OpenAI fallback). Default medium —
// the doctrine floor (agent_runner_anthropic.go): a summarization-maintenance
// seat needs no orchestrator-grade depth, but no surface ever runs below
// medium. A configured dial below the floor clamps UP with a logged warning,
// the orchestratorEffort/deliverableEffort idiom.
func narrativeMaintainerEffort() string {
	effort, clamped := flooredEffort(os.Getenv("NARRATIVE_MAINTAINER_EFFORT"), doctrineEffortFloor)
	if clamped {
		log.Warnf("NARRATIVE_MAINTAINER_EFFORT is below the doctrine floor (never below medium); clamping up to %s", doctrineEffortFloor)
	}
	return effort
}

// narrativeUpdatePayload is one storyline update in the maintainer's strict
// JSON output; body is the FULL replacement dossier for the slug.
type narrativeUpdatePayload struct {
	Slug   string `json:"slug"`
	Title  string `json:"title"`
	Status string `json:"status"`
	Body   string `json:"body"`
}

type narrativeMaintainerOutput struct {
	Narratives []narrativeUpdatePayload `json:"narratives"`
}

// parseNarrativeUpdates validates maintainer output: strict JSON with the
// parseMissionInsight tolerance for a stray markdown fence.
func parseNarrativeUpdates(text string) (narrativeMaintainerOutput, bool) {
	text = strings.TrimSpace(text)
	if fenced := strings.TrimPrefix(text, "```json"); fenced != text {
		text = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(fenced), "```"))
	} else if fenced := strings.TrimPrefix(text, "```"); fenced != text {
		text = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(fenced), "```"))
	}
	var output narrativeMaintainerOutput
	if text == "" || json.Unmarshal([]byte(text), &output) != nil {
		return narrativeMaintainerOutput{}, false
	}

	return output, true
}

var narrativeSlugPattern = regexp.MustCompile(`[^a-z0-9]+`)

// normalizeNarrativeSlug forces the model-supplied slug into a stable kebab
// form ("Samsung TV+!" → "samsung-tv"), so the same storyline can never fork
// on punctuation drift between passes.
func normalizeNarrativeSlug(raw string) string {
	slug := strings.Trim(narrativeSlugPattern.ReplaceAllString(strings.ToLower(strings.TrimSpace(raw)), "-"), "-")
	if len(slug) > narrativeSlugMaxLen {
		slug = strings.Trim(slug[:narrativeSlugMaxLen], "-")
	}
	return slug
}

// truncateNarrativeBody caps a dossier body at narrativeBodyMaxChars runes,
// announcing the cut the pinned-profile way.
func truncateNarrativeBody(body string) string {
	body = strings.TrimSpace(body)
	runes := []rune(body)
	if len(runes) <= narrativeBodyMaxChars {
		return body
	}
	return strings.TrimSpace(string(runes[:narrativeBodyMaxChars-1])) + "…"
}

func narrativeMaintainerInstructions() string {
	return strings.Join([]string{
		"You are Bonfire's narrative maintainer: you keep one living dossier per active storyline — an opportunity, client, or project the team is moving (e.g. the Samsung TV Plus opportunity).",
		"From the new brain write-ups plus the supplied workspace context, return STRICT JSON only, no markdown fence, matching:",
		`{"narratives":[{"slug":string(kebab-case, stable across passes),"title":string(<=8 words),"status":string(<=140 chars, the one-line current status),"body":string(markdown)}]}.`,
		"Return ONLY storylines the new window creates or materially changes; omit untouched storylines entirely — their existing dossiers stay live.",
		"Reuse the EXACT slug of an existing dossier when updating it; never mint a second slug for the same storyline.",
		"Each body is the FULL replacement dossier with exactly these markdown sections: ## Storyline, ## Current status, ## Timeline (dated bullets, oldest first), ## Key people, ## Concerns & counterpoints, ## Deliverables & runs (titles + artifact ids), ## Feedback so far, ## Open questions.",
		"Carry still-true facts forward from the previous dossier version; add new dated Timeline bullets from the window.",
		"Reference deliverables by title and artifact id only — never inline an artifact body.",
		"Preserve real participant names; never invent people, clients, decisions, runs, or feedback.",
		"Keep each body under 6000 characters.",
		`If nothing storyline-worthy changed, return {"narratives":[]}.`,
	}, " ")
}

// buildNarrativeMaintainerInput assembles one pass's input: the previous
// active dossiers, the store-derived context (artifact titles/ids, decisions,
// run-log lines, and the feedback signals keyed to those artifacts), and the
// new brain window. Artifact TITLES only — the mission-intelligence law: full
// bodies never ride an ambient pipeline.
func (app *kanbanBoardApp) buildNarrativeMaintainerInput(inputs []meetingMemoryEntry, generatedAt time.Time) string {
	var builder strings.Builder
	builder.WriteString("# Generated at\n")
	builder.WriteString(generatedAt.Format(time.RFC3339))

	if narratives := app.activeNarrativeEntries(narrativeStorylineContextLimit); len(narratives) > 0 {
		builder.WriteString("\n\n# Active storyline dossiers (previous versions)\n")
		for _, narrative := range narratives {
			builder.WriteString("## slug=")
			builder.WriteString(strings.TrimSpace(narrative.Metadata["slug"]))
			builder.WriteByte('\n')
			builder.WriteString(narrative.Text)
			builder.WriteString("\n\n")
		}
	}

	artifactIDs := map[string]struct{}{}
	if artifacts := app.memory.entriesOfKind(meetingMemoryKindOSArtifact, narrativeContextArtifacts); len(artifacts) > 0 {
		builder.WriteString("\n\n# Recent deliverables (titles + ids only)\n")
		for _, artifact := range artifacts {
			// entriesOfKind is unfiltered by relevance: re-apply the recall guard
			// so a quarantined/expired draft never enters model context.
			if memoryEntryHiddenFromRecall(artifact) {
				continue
			}
			artifactIDs[artifact.ID] = struct{}{}
			builder.WriteString("- ")
			builder.WriteString(artifact.ID)
			builder.WriteString(" | ")
			builder.WriteString(firstNonEmptyString(strings.TrimSpace(artifact.Metadata["title"]), "untitled"))
			if mode := strings.TrimSpace(artifact.Metadata["mode"]); mode != "" {
				builder.WriteString(" (")
				builder.WriteString(mode)
				builder.WriteString(")")
			}
			builder.WriteByte('\n')
		}
	}

	if runLogs := app.memory.entriesOfKind(meetingMemoryKindRunLog, narrativeContextRunLogs); len(runLogs) > 0 {
		builder.WriteString("\n\n# Recent agent runs\n")
		for _, runLog := range runLogs {
			if memoryEntryHiddenFromRecall(runLog) {
				continue
			}
			builder.WriteString("- ")
			builder.WriteString(runLog.Text)
			builder.WriteByte('\n')
		}
	}

	// Feedback signals keyed to the deliverables above — the ONLY place raw
	// signals reach a model outside the taste distillers: rendered as compact
	// event lines (event/valence/actor/artifact), never quoted payload prose.
	if signals := app.memory.entriesOfKind(meetingMemoryKindSignal, narrativeContextSignals); len(signals) > 0 {
		lines := make([]string, 0, len(signals))
		for _, entry := range signals {
			record, ok := decodeSignalEntry(entry)
			if !ok || record.ArtifactID == "" {
				continue
			}
			if _, known := artifactIDs[record.ArtifactID]; !known {
				continue
			}
			line := "- " + record.Event
			if record.Valence != "" {
				line += " (" + record.Valence + ")"
			}
			if record.Actor != "" {
				line += " by " + record.Actor
			}
			line += " on " + record.ArtifactID
			lines = append(lines, line)
		}
		if len(lines) > 0 {
			builder.WriteString("\n\n# Deliverable feedback events\n")
			builder.WriteString(strings.Join(lines, "\n"))
			builder.WriteByte('\n')
		}
	}

	if decisions := app.activeDecisionEntries(narrativeContextDecisions); len(decisions) > 0 {
		builder.WriteString("\n\n# Decisions on record\n")
		for _, decision := range decisions {
			builder.WriteString("- ")
			builder.WriteString(decision.Text)
			builder.WriteByte('\n')
		}
	}

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

func (app *kanbanBoardApp) produceNarrativeUpdates(ctx context.Context, apiKey string, inputs []meetingMemoryEntry, responder openAITextResponder) (meetingMemoryEntry, error) {
	input := app.buildNarrativeMaintainerInput(inputs, time.Now().UTC())
	model := meetingBrainModel()
	effort := narrativeMaintainerEffort()
	var text string
	var err error
	// Sonnet fronts the maintainer whenever an Anthropic key is present (the
	// memory_query.go split); keyless-Anthropic keeps the chassis's OpenAI
	// responder path so keyless deploys degrade exactly like mission intel.
	if anthropicKey := currentAnthropicAPIKey(); anthropicKey != "" {
		model = chatModel()
		text, err = createAnthropicTextResponse(ctx, anthropicKey, anthropicTextRequest{
			Model:        model,
			Instructions: narrativeMaintainerInstructions(),
			Input:        input,
			Effort:       effort,
			MaxTokens:    4000,
		})
	} else {
		text, err = responder(ctx, apiKey, openAITextRequest{
			Model:           model,
			Instructions:    narrativeMaintainerInstructions(),
			Input:           input,
			ReasoningEffort: effort,
			Verbosity:       "low",
			MaxOutputTokens: 4000,
		})
	}
	if err != nil {
		return meetingMemoryEntry{}, err
	}
	output, ok := parseNarrativeUpdates(text)
	if !ok {
		// Never persist unparseable output: the cursor stays put, so the next
		// pass retries with more input (the mission-intel contract).
		log.Errorf("%s returned non-JSON output; skipping this pass", narrativeMaintainerAgentName)
		return meetingMemoryEntry{}, nil
	}

	firstBrain := inputs[0]
	lastBrain := inputs[len(inputs)-1]
	now := time.Now().UTC()
	// Segment bookkeeping: stamp the sitting id plus the [firstSeen,lastSeen]
	// window of the brains that fed this pass, so the per-meeting topic timeline
	// (meetingSegments) has a real time range to draw without a second pass.
	// W4: the sitting is THE WINDOW'S ROOM's — never the office's by default.
	windowFirst, windowLast := brainWindowBounds(inputs)
	roomID := ambientWindowRoomID(inputs)
	meetingID := app.memory.currentMeetingID(roomID)
	var latest meetingMemoryEntry
	for _, update := range output.Narratives {
		slug := normalizeNarrativeSlug(update.Slug)
		body := truncateNarrativeBody(update.Body)
		if slug == "" || body == "" {
			continue
		}
		predecessor, hasPredecessor := app.activeNarrativeBySlug(slug)
		metadata := map[string]string{
			"slug":             slug,
			"title":            compactAssistantLine(firstNonEmptyString(strings.TrimSpace(update.Title), slug)),
			"status":           compactAssistantLine(update.Status),
			"source":           "narrative_maintainer",
			"model":            model,
			"roomId":           roomID,
			"fromBrainId":      firstBrain.ID,
			narrativeCursorKey: lastBrain.ID,
			"brainCount":       strconv.Itoa(len(inputs)),
			"generatedAt":      now.Format(time.RFC3339),
		}
		if strings.TrimSpace(meetingID) != "" {
			metadata["meetingId"] = meetingID
		}
		// Cross-call narrative arc (kanban-card-107): accumulate a capped,
		// de-duped union of every meeting this dossier has spanned (the
		// entity-ledger meetingIds precedent, cap ledgerMeetingIDCap) instead of
		// overwriting to the latest sitting — so a storyline discussed across
		// many meetings keeps its full provenance for later recall.
		var meetingIDs []string
		if hasPredecessor {
			meetingIDs = splitNarrativeMeetingIDs(predecessor.Metadata["meetingIds"])
		}
		if id := strings.TrimSpace(meetingID); id != "" {
			meetingIDs, _ = appendUniqueCapped(meetingIDs, id, ledgerMeetingIDCap)
		}
		if len(meetingIDs) > 0 {
			metadata["meetingIds"] = strings.Join(meetingIDs, ",")
		}
		if !windowFirst.IsZero() {
			metadata["firstSeenAt"] = windowFirst.Format(time.RFC3339Nano)
		}
		if !windowLast.IsZero() {
			metadata["lastSeenAt"] = windowLast.Format(time.RFC3339Nano)
		}
		if hasPredecessor {
			metadata["previousVersionId"] = predecessor.ID
		}
		entry, appended, appendErr := app.memory.appendNarrative(durableTimestampID("narrative-"+slug, time.Now()), body, metadata)
		if appendErr != nil {
			log.Errorf("%s failed to append %s dossier: %v", narrativeMaintainerAgentName, slug, appendErr)
			continue
		}
		if !appended {
			continue
		}
		latest = entry
		// ONE active entry per slug: the predecessor drops out of recall via
		// the relevance lifecycle — the ledger-supersede posture, never a
		// delete. Failure keeps two actives until the next pass retires it.
		if hasPredecessor {
			if _, _, expireErr := app.memory.updateEntryWithMetadata(meetingMemoryKindNarrative, predecessor.ID, predecessor.Text, map[string]string{
				relevanceMetadataKey: relevanceExpired,
				"expiredAt":          now.Format(time.RFC3339Nano),
				"supersededBy":       entry.ID,
			}); expireErr != nil {
				log.Errorf("%s failed to expire predecessor %s of %s: %v", narrativeMaintainerAgentName, predecessor.ID, slug, expireErr)
			}
		}
		broadcastOfficeKanbanEvent("narrative", narrativeSnapshotRow(entry))
	}

	// Recompute the dominant room title from the freshly-updated segment
	// salience — no extra model call. This is the ~10-minute re-derive trigger
	// that keeps the title off the 15-minute mission tick's lag.
	app.refreshDominantTitle(roomID, now)

	if strings.TrimSpace(latest.ID) == "" {
		// A legitimate "nothing storyline-worthy" pass appends no artifact, so
		// the chassis cursor would stall and re-feed the same brains every
		// tick. Advance it by stamping the consumed-through id onto the newest
		// existing narrative entry OF THIS ROOM — or, on cold start (no
		// narrative for the room yet), onto a hidden cursor-carrier entry, so
		// an all-empty workspace never re-reads the same brain window forever
		// and one room's stamp never corrupts another room's cursor.
		app.stampNarrativeCursor(roomID, lastBrain.ID)
		return meetingMemoryEntry{}, nil
	}

	return latest, nil
}

// stampNarrativeCursor advances the maintainer's consumed-through cursor
// after a pass that appended nothing. With an existing narrative entry the
// stamp lands on the newest one; on cold start (no narrative yet) it appends
// a hidden cursor-carrier entry instead — expired from birth and slugless, so
// recall, the mission snapshot, and the dossier context never see it. This is
// the chassis idiom of persisting the cursor independent of content
// production (mission intelligence appends its artifact even on a thin
// window); without it, a workspace whose every pass legitimately returns
// {"narratives":[]} would re-read the same brain window forever.
func (app *kanbanBoardApp) stampNarrativeCursor(roomID string, throughBrainID string) {
	if app == nil || app.memory == nil || strings.TrimSpace(throughBrainID) == "" {
		return
	}
	roomID = normalizeRoomID(roomID)
	latestID := app.memory.latestEntryIDOfKindForRoom(meetingMemoryKindNarrative, roomID)
	if latestID == "" {
		now := time.Now()
		if _, _, err := app.memory.appendNarrative(durableTimestampID("narrative-cursor", now), "Narrative maintainer cursor stamp — no storylines yet.", map[string]string{
			narrativeCursorKey:   throughBrainID,
			"source":             "narrative_maintainer",
			"roomId":             roomID,
			relevanceMetadataKey: relevanceExpired,
			"generatedAt":        now.UTC().Format(time.RFC3339),
		}); err != nil {
			log.Errorf("%s failed to persist cold-start cursor: %v", narrativeMaintainerAgentName, err)
		}
		return
	}
	entry, ok := app.memory.entryByKindAndID(meetingMemoryKindNarrative, latestID)
	if !ok {
		return
	}
	if _, _, err := app.memory.updateEntryWithMetadata(meetingMemoryKindNarrative, entry.ID, entry.Text, map[string]string{
		narrativeCursorKey: throughBrainID,
	}); err != nil {
		log.Errorf("%s failed to advance cursor on %s: %v", narrativeMaintainerAgentName, entry.ID, err)
	}
}

// activeNarrativeEntries returns the active storyline dossiers, newest first,
// one per slug (defensive dedupe — the lifecycle law already keeps one active
// entry per slug).
func (app *kanbanBoardApp) activeNarrativeEntries(limit int) []meetingMemoryEntry {
	if app == nil || app.memory == nil || limit <= 0 {
		return nil
	}
	entries := app.memory.entriesOfKind(meetingMemoryKindNarrative, 0)
	seen := map[string]struct{}{}
	newest := make([]meetingMemoryEntry, 0, limit)
	for index := len(entries) - 1; index >= 0 && len(newest) < limit; index-- {
		entry := entries[index]
		if memoryEntryRelevance(entry) != relevanceActive {
			continue
		}
		slug := strings.TrimSpace(entry.Metadata["slug"])
		if slug == "" {
			continue
		}
		if _, dup := seen[slug]; dup {
			continue
		}
		seen[slug] = struct{}{}
		newest = append(newest, entry)
	}

	return newest
}

// activeNarrativeBySlug finds the current active dossier for one storyline.
func (app *kanbanBoardApp) activeNarrativeBySlug(slug string) (meetingMemoryEntry, bool) {
	slug = strings.TrimSpace(slug)
	if app == nil || app.memory == nil || slug == "" {
		return meetingMemoryEntry{}, false
	}
	entries := app.memory.entriesOfKind(meetingMemoryKindNarrative, 0)
	for index := len(entries) - 1; index >= 0; index-- {
		entry := entries[index]
		if memoryEntryRelevance(entry) != relevanceActive {
			continue
		}
		if strings.TrimSpace(entry.Metadata["slug"]) == slug {
			return entry, true
		}
	}

	return meetingMemoryEntry{}, false
}

// splitNarrativeMeetingIDs parses a dossier's stored comma-joined meetingIds
// provenance list back into a slice, dropping blanks. Empty input yields nil.
func splitNarrativeMeetingIDs(joined string) []string {
	joined = strings.TrimSpace(joined)
	if joined == "" {
		return nil
	}
	parts := strings.Split(joined, ",")
	ids := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			ids = append(ids, trimmed)
		}
	}
	return ids
}

// narrativeStatusLine is the one-line status a storyline shows outside its
// body: the stamped status first, else the compact head of the dossier.
func narrativeStatusLine(entry meetingMemoryEntry) string {
	if status := strings.TrimSpace(entry.Metadata["status"]); status != "" {
		return compactAssistantLine(status)
	}
	return compactAssistantLine(entry.Text)
}

// narrativeSnapshotRow shapes one dossier for the mission snapshot payload
// and the office "narrative" event: identity + summary, never the body.
func narrativeSnapshotRow(entry meetingMemoryEntry) map[string]any {
	updatedAt := entry.CreatedAt.UTC().Format(time.RFC3339Nano)
	return map[string]any{
		"slug":      strings.TrimSpace(entry.Metadata["slug"]),
		"title":     firstNonEmptyString(strings.TrimSpace(entry.Metadata["title"]), strings.TrimSpace(entry.Metadata["slug"])),
		"updatedAt": firstNonEmptyString(strings.TrimSpace(entry.Metadata["generatedAt"]), updatedAt),
		"summary":   narrativeStatusLine(entry),
	}
}

// narrativeSnapshotRows is the "narratives" array on the mission snapshot:
// active dossiers, newest first.
func (app *kanbanBoardApp) narrativeSnapshotRows(limit int) []map[string]any {
	rows := make([]map[string]any, 0, limit)
	for _, entry := range app.activeNarrativeEntries(limit) {
		rows = append(rows, narrativeSnapshotRow(entry))
	}
	return rows
}

// meetingSegment is one storyline slug's presence in the CURRENT sitting: its
// title/status, the [firstSeenAt,lastSeenAt] window derived from the brains
// that fed it, and a decayed recurrence weight (Σ e^(−Δt/τ) over the versions
// that carried it). The dominant title is drawn from this same list, so the
// topbar title is always one of the timeline's rows.
type meetingSegment struct {
	Slug          string
	Title         string
	Status        string
	FirstSeenAt   time.Time
	LastSeenAt    time.Time
	DecayedWeight float64
}

// parseSegmentStamp reads a stamped RFC3339Nano segment time, falling back to
// the version's own creation time when the stamp is missing or malformed.
func parseSegmentStamp(raw string, fallback time.Time) time.Time {
	if parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(raw)); err == nil {
		return parsed
	}
	return fallback
}

// meetingSegments derives the per-sitting topic timeline from the narrative
// dossiers — one segment per slug touched during the sitting, ordered by
// firstSeenAt. It scans ALL narrative versions (active AND expired, so a
// storyline that recurred across passes accumulates weight) and scopes them by
// version CreatedAt >= record.StartedAt — NEVER by meeting id alone, since one
// id can span two sittings. Cursor-carrier and slugless entries are skipped.
func (app *kanbanBoardApp) meetingSegments(record meetingRecord, now time.Time) []meetingSegment {
	if app == nil || app.memory == nil {
		return nil
	}
	startedAt, ok := parseMeetingStartedAt(record)
	if !ok {
		return nil
	}

	type accum struct {
		seg           meetingSegment
		hasWindow     bool
		newestVersion time.Time
	}
	bySlug := map[string]*accum{}
	order := make([]string, 0, 8)
	recordRoom := meetingRoomID(record)
	for _, entry := range app.memory.entriesOfKind(meetingMemoryKindNarrative, 0) {
		slug := strings.TrimSpace(entry.Metadata["slug"])
		if slug == "" {
			continue
		}
		if entry.CreatedAt.Before(startedAt) {
			continue
		}
		// W4: versions written for another room's sitting never shape this
		// room's timeline (absent roomId == office, the legacy invariant).
		if normalizeRoomID(entry.Metadata["roomId"]) != recordRoom {
			continue
		}
		ac := bySlug[slug]
		if ac == nil {
			ac = &accum{seg: meetingSegment{Slug: slug}}
			bySlug[slug] = ac
			order = append(order, slug)
		}
		ac.seg.DecayedWeight += decayedWeight(now, entry.CreatedAt)
		first := parseSegmentStamp(entry.Metadata["firstSeenAt"], entry.CreatedAt)
		last := parseSegmentStamp(entry.Metadata["lastSeenAt"], entry.CreatedAt)
		if !ac.hasWindow {
			ac.seg.FirstSeenAt = first
			ac.seg.LastSeenAt = last
			ac.hasWindow = true
		} else {
			if first.Before(ac.seg.FirstSeenAt) {
				ac.seg.FirstSeenAt = first
			}
			if last.After(ac.seg.LastSeenAt) {
				ac.seg.LastSeenAt = last
			}
		}
		// title/status track the newest version of the slug in the sitting.
		if ac.newestVersion.IsZero() || entry.CreatedAt.After(ac.newestVersion) {
			ac.newestVersion = entry.CreatedAt
			ac.seg.Title = firstNonEmptyString(strings.TrimSpace(entry.Metadata["title"]), slug)
			ac.seg.Status = strings.TrimSpace(entry.Metadata["status"])
		}
	}

	segments := make([]meetingSegment, 0, len(order))
	for _, slug := range order {
		segments = append(segments, bySlug[slug].seg)
	}
	sort.SliceStable(segments, func(i, j int) bool {
		return segments[i].FirstSeenAt.Before(segments[j].FirstSeenAt)
	})
	return segments
}

// dominantSegmentIndex is the argmax decayed-weight segment — the "current"
// segment that names the room. Strict > keeps the first (earliest-firstSeen)
// on ties, so the marker is stable. Returns -1 for an empty list. Both the
// title derivation and the snapshot's current/past status use this ONE reduce,
// so they can never disagree about which segment is current.
func dominantSegmentIndex(segments []meetingSegment) int {
	best := -1
	for index := range segments {
		if best < 0 || segments[index].DecayedWeight > segments[best].DecayedWeight {
			best = index
		}
	}
	return best
}

// meetingSegmentRows shapes the current sitting's segments for the mission
// snapshot: identity + [firstSeen,lastSeen] + a current/past status. Empty
// (never nil) when no meeting is live or no storyline has been segmented yet.
func (app *kanbanBoardApp) meetingSegmentRows(now time.Time) []map[string]any {
	rows := []map[string]any{}
	if app == nil || app.meetings == nil {
		return rows
	}
	record, ok := app.meetings.activeRecord(officeRoomID)
	if !ok {
		return rows
	}
	segments := app.meetingSegments(record, now)
	dominant := dominantSegmentIndex(segments)
	for index, segment := range segments {
		status := "past"
		if index == dominant {
			status = "current"
		}
		rows = append(rows, map[string]any{
			"slug":        segment.Slug,
			"title":       segment.Title,
			"firstSeenAt": segment.FirstSeenAt.UTC().Format(time.RFC3339Nano),
			"lastSeenAt":  segment.LastSeenAt.UTC().Format(time.RFC3339Nano),
			"status":      status,
		})
	}
	return rows
}
