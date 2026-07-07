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
	"regexp"
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
		produce:           (*kanbanBoardApp).produceNarrativeUpdates,
	}
}

func (app *kanbanBoardApp) startNarrativeMaintainerWorker(apiKey string) {
	app.startAmbientAgent(narrativeMaintainerAgent(), apiKey)
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
			Effort:       "low",
			MaxTokens:    4000,
		})
	} else {
		text, err = responder(ctx, apiKey, openAITextRequest{
			Model:           model,
			Instructions:    narrativeMaintainerInstructions(),
			Input:           input,
			ReasoningEffort: "low",
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
			"fromBrainId":      firstBrain.ID,
			narrativeCursorKey: lastBrain.ID,
			"brainCount":       strconv.Itoa(len(inputs)),
			"generatedAt":      now.Format(time.RFC3339),
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

	if strings.TrimSpace(latest.ID) == "" {
		// A legitimate "nothing storyline-worthy" pass appends no artifact, so
		// the chassis cursor would stall and re-feed the same brains every
		// tick. Advance it by stamping the consumed-through id onto the newest
		// existing narrative entry; a cold-start no-op (no narrative yet) just
		// retries the cheap window next pass.
		app.stampNarrativeCursor(lastBrain.ID)
		return meetingMemoryEntry{}, nil
	}

	return latest, nil
}

// stampNarrativeCursor advances the maintainer's consumed-through cursor on
// the newest narrative entry after a pass that appended nothing.
func (app *kanbanBoardApp) stampNarrativeCursor(throughBrainID string) {
	if app == nil || app.memory == nil || strings.TrimSpace(throughBrainID) == "" {
		return
	}
	latestID := app.memory.latestEntryIDOfKind(meetingMemoryKindNarrative)
	if latestID == "" {
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
