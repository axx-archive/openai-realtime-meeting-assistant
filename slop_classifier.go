package main

// Slop classifier — the fifth ambient worker (agent_runner.go recipe), the
// studio's knowledge steward. It keeps company memory DENSE by moving orphaned,
// duplicative, or superseded-and-never-sent material out of recall:
//
//   - transcript segments and unpublished/unattached os_artifacts are the ONLY
//     candidates (design §6, reconciled). A HARD deny-list — enforced in the
//     candidate builder, never trusted to the prompt — protects decisions,
//     archives, packages, every UI-state kind, published or package-attached
//     artifacts, human-pinned material, and anything younger than 7 days.
//   - a model pass returns keep|archive|quarantine per candidate; quarantine
//     requires confidence >= 0.85, archive >= 0.70, else keep. Bias to keep.
//   - quarantined entries leave recall for 30 VISIBLE days, then the expiry
//     sweep (same worker tick) hard-deletes them — the only hard delete in the
//     system — each leaving a slop_pass audit stub. Never a silent delete.
//   - every transition fans a quarantine_change OS event (Wave 3).
//
// Idempotence: transcript candidates advance the slop_pass cursor
// (slopConsumedThrough) exactly as decision_ledger advances its brain cursor;
// artifact candidates (which have no cursor) are stamped with classifierVerdict
// so a later pass skips them. The per-agent run-lock serializes whole passes.
//
// Keyless-safe: no OPENAI_API_KEY → the worker never starts, like every ambient
// agent.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	slopClassifierAgentName       = "slop_classifier"
	defaultSlopClassifierInterval = 6 * time.Hour
	slopClassifierRequestTimeout  = 2 * time.Minute
	slopClassifierCursorKey       = "slopConsumedThrough"
	defaultSlopClassifierMinBatch = 8
	defaultSlopClassifierMaxBatch = 40
	// slopEligibilityAge is the settled-entry gate: nothing younger is ever a
	// candidate (recent uncertainty may still become something).
	slopEligibilityAge = 7 * 24 * time.Hour
	// slopQuarantineExpiry is the visible reprieve before a quarantined entry is
	// hard-deleted by the expiry sweep.
	slopQuarantineExpiry = 30 * 24 * time.Hour
	// reconciled thresholds (design §6): stricter where deletion is downstream.
	slopQuarantineConfidence = 0.85
	slopArchiveConfidence    = 0.70
	// slopArtifactScanLimit bounds the per-pass unpublished-artifact scan.
	slopArtifactScanLimit = 200
	reviewedByClassifier  = "classifier"
)

func slopClassifierAgent() ambientAgentConfig {
	return ambientAgentConfig{
		name:              slopClassifierAgentName,
		defaultInterval:   defaultSlopClassifierInterval,
		intervalEnv:       "SLOP_CLASSIFIER_INTERVAL",
		disabledEnv:       "SLOP_CLASSIFIER_DISABLED",
		backfillEnv:       "SLOP_CLASSIFIER_BACKFILL",
		minBatchEnv:       "SLOP_CLASSIFIER_MIN_INPUTS",
		defaultMinBatch:   defaultSlopClassifierMinBatch,
		maxBatchEnv:       "SLOP_CLASSIFIER_MAX_INPUTS",
		defaultMaxBatch:   defaultSlopClassifierMaxBatch,
		inputKind:         meetingMemoryKindTranscript,
		artifactKind:      meetingMemoryKindSlopPass,
		cursorMetadataKey: slopClassifierCursorKey,
		requestTimeout:    slopClassifierRequestTimeout,
		// produce is unused: the classifier owns its loop (below) because the
		// expiry sweep must ride EVERY tick, not only minBatch-triggered passes.
	}
}

// startSlopClassifierWorker registers the classifier + expiry worker. Unlike the
// generic startAmbientAgent loop (which gates the whole tick on minBatch), this
// worker runs the expiry sweep every tick and only the classification pass is
// minBatch-gated — so a quarantined entry always expires on schedule even in a
// quiet week.
func (app *kanbanBoardApp) startSlopClassifierWorker(apiKey string) {
	agent := slopClassifierAgent()
	if app == nil || app.memory == nil || strings.TrimSpace(apiKey) == "" || boolEnv(agent.disabledEnv) {
		return
	}
	interval := agent.interval()
	if interval <= 0 {
		return
	}

	cancel := make(chan struct{})
	done := make(chan struct{})
	baselineID := ""
	if !boolEnv(agent.backfillEnv) {
		baselineID = app.memory.latestEntryIDOfKind(agent.inputKind)
	}

	app.mu.Lock()
	if app.agentCancels == nil {
		app.agentCancels = map[string]chan struct{}{}
		app.agentDones = map[string]chan struct{}{}
	}
	oldCancel := app.agentCancels[agent.name]
	oldDone := app.agentDones[agent.name]
	app.agentCancels[agent.name] = cancel
	app.agentDones[agent.name] = done
	app.setAmbientAgentBaselineIDLocked(agent.name, baselineID)
	app.mu.Unlock()

	if oldCancel != nil {
		close(oldCancel)
		if oldDone != nil {
			<-oldDone
		}
	}

	go app.runSlopClassifierLoop(agent, apiKey, interval, cancel, done)
}

func (app *kanbanBoardApp) runSlopClassifierLoop(agent ambientAgentConfig, apiKey string, interval time.Duration, cancel <-chan struct{}, done chan<- struct{}) {
	defer close(done)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			ctx, cancelRequest := context.WithTimeout(context.Background(), agent.requestTimeout)
			if err := app.runSlopClassifierOnce(agent, ctx, apiKey, nil, agent.minBatch()); err != nil {
				log.Errorf("%s worker failed: %v", agent.name, err)
			}
			cancelRequest()
		case <-cancel:
			return
		}
	}
}

// runSlopClassifierOnce is one whole tick: expiry sweep (always), then a
// minBatch-gated classification pass. Serialized by the per-agent run-lock so
// overlapping ticks never double-classify or double-delete.
func (app *kanbanBoardApp) runSlopClassifierOnce(agent ambientAgentConfig, ctx context.Context, apiKey string, responder openAITextResponder, minBatch int) error {
	if app == nil || app.memory == nil {
		return nil
	}
	if responder == nil {
		responder = createOpenAITextResponse
	}
	if minBatch < 1 {
		minBatch = 1
	}

	runLock := app.ambientAgentRunLock(agent.name)
	runLock.Lock()
	defer runLock.Unlock()

	// The forward cursor every slop_pass this tick must carry, so an expiry
	// audit stub (which consumes no transcript) never becomes the newest
	// slop_pass with an empty cursor and strands unclassified transcripts.
	priorCursor := app.newestSlopCursor()

	// (1) expiry sweep — always, regardless of minBatch.
	app.sweepExpiredQuarantine(priorCursor)

	// (1b) distilled-signal compaction — always, deterministic and token-free:
	// signals the Taste Analyst consumed into a living profile (stamped
	// distilledInto, taste_analyst.go) leave the store after their 30-day
	// reprieve. The profile carries the taste; the raw signal was only ever
	// distillation input, never recall material.
	app.sweepDistilledSignals(priorCursor)

	// (2) classification pass — minBatch-gated.
	candidates, transcriptCursor := app.buildSlopCandidates(agent, time.Now().UTC())
	if len(candidates) < minBatch {
		return nil
	}

	model := meetingBrainModel()
	text, err := responder(ctx, apiKey, openAITextRequest{
		Model:           model,
		Seat:            seatSlop,
		Instructions:    slopClassifierInstructions(),
		Input:           app.buildSlopClassifierInput(candidates, time.Now().UTC()),
		ReasoningEffort: "low",
		Verbosity:       "low",
		MaxOutputTokens: 1200,
	})
	if err != nil {
		return err
	}
	verdicts, ok := parseSlopClassifierOutput(text)
	if !ok {
		// Never advance the cursor on unparseable output: the next pass retries
		// the same window (decision-ledger precedent).
		recordEvalEvent(seatSlop, evalKindParseFailure, map[string]any{"seat": seatSlop, "model": model})
		log.Errorf("%s returned non-JSON output; skipping this pass", slopClassifierAgentName)
		return nil
	}

	byID := make(map[string]meetingMemoryEntry, len(candidates))
	for _, candidate := range candidates {
		byID[candidate.ID] = candidate
	}

	quarantined, archived := 0, 0
	for _, verdict := range verdicts {
		candidate, found := byID[strings.TrimSpace(verdict.EntryID)]
		if !found {
			continue // never act on an id the model invented outside the batch.
		}
		switch app.applySlopVerdict(candidate, verdict) {
		case relevanceQuarantined:
			quarantined++
		case relevanceArchived:
			archived++
		}
	}

	// Advance the transcript cursor (keep-transcripts are now "consumed") and
	// record the pass — always, so unconsumedEntriesAfter never re-feeds them.
	forwardCursor := firstNonEmptyString(transcriptCursor, priorCursor)
	passText := "Classified " + strconv.Itoa(len(candidates)) + " candidate(s): " +
		strconv.Itoa(quarantined) + " quarantined, " + strconv.Itoa(archived) + " archived"
	passMetadata := map[string]string{
		"source":                "openai_responses",
		"model":                 meetingBrainModel(),
		slopClassifierCursorKey: forwardCursor,
		"candidateCount":        strconv.Itoa(len(candidates)),
		"quarantinedCount":      strconv.Itoa(quarantined),
		"archivedCount":         strconv.Itoa(archived),
	}
	if _, _, err := app.memory.appendSlopPass(durableTimestampID("slop-pass", time.Now()), passText, passMetadata); err != nil {
		return err
	}
	if quarantined > 0 || archived > 0 {
		broadcastAssistantEvent("action", "Scout tidied memory: "+strconv.Itoa(quarantined)+" quarantined, "+strconv.Itoa(archived)+" archived.", map[string]any{"kind": "slop"})
	}

	return nil
}

// newestSlopCursor returns the slopConsumedThrough of the newest slop_pass
// entry (cursor pass or forward-carrying audit stub), or "" at boot.
func (app *kanbanBoardApp) newestSlopCursor() string {
	passes := app.memory.entriesOfKind(meetingMemoryKindSlopPass, 0)
	for index := len(passes) - 1; index >= 0; index-- {
		if cursor := strings.TrimSpace(passes[index].Metadata[slopClassifierCursorKey]); cursor != "" {
			return cursor
		}
	}
	return ""
}

// buildSlopCandidates assembles the eligible-candidate batch and the transcript
// cursor to advance to. Transcripts come from the unconsumed window (cursor +
// boot baseline bounded); unpublished/unattached artifacts from a bounded scan.
// EVERY candidate passes slopCandidateEligible — the hard deny-list.
func (app *kanbanBoardApp) buildSlopCandidates(agent ambientAgentConfig, now time.Time) ([]meetingMemoryEntry, string) {
	app.ensureAmbientAgentBaseline(agent)
	baselineID := app.ambientAgentBaselineID(agent.name)

	rawTranscripts := app.memory.unconsumedEntriesAfter(agent.inputKind, agent.artifactKind, agent.cursorMetadataKey, agent.maxBatch(), baselineID)
	candidates := make([]meetingMemoryEntry, 0, len(rawTranscripts))
	transcriptCursor := ""
	for _, entry := range rawTranscripts {
		if !slopCandidateEligible(entry, now) {
			continue
		}
		candidates = append(candidates, entry)
		// advance the cursor only over eligible (settled) transcripts, so a
		// still-too-young transcript is re-evaluated once it settles.
		transcriptCursor = entry.ID
	}

	for _, entry := range app.memory.entriesOfKind(meetingMemoryKindOSArtifact, slopArtifactScanLimit) {
		if !slopCandidateEligible(entry, now) {
			continue
		}
		// artifacts have no cursor: skip any already classified so a keep verdict
		// is not re-billed every pass.
		if strings.TrimSpace(entry.Metadata["classifierVerdict"]) != "" {
			continue
		}
		candidates = append(candidates, entry)
		if len(candidates) >= agent.maxBatch() {
			break
		}
	}

	return candidates, transcriptCursor
}

// slopCandidateEligible is the HARD deny-list, enforced in code (never the
// prompt). Only settled, active, transcript segments and unpublished/unattached
// os_artifacts that no human pinned are ever eligible.
func slopCandidateEligible(entry meetingMemoryEntry, now time.Time) bool {
	switch entry.Kind {
	case meetingMemoryKindTranscript:
		// eligible kind
	case meetingMemoryKindSignal:
		// distilled-and-aged signals are the compaction cohort (spec §5
		// flywheel): consumed into a living profile (taste_analyst.go stamped
		// distilledInto) and past the 30-day reprieve. They never join the
		// model classification batch — the deterministic sweep is their only
		// consumer — and an undistilled signal is never touched.
		return signalCompactionEligible(entry, now)
	case meetingMemoryKindOSArtifact:
		// published or package-attached artifacts are load-bearing.
		if strings.TrimSpace(entry.Metadata["published"]) == "true" {
			return false
		}
		if strings.TrimSpace(entry.Metadata["packageId"]) != "" {
			return false
		}
	default:
		// decision, archive, package, and every UI-state kind are never touched.
		return false
	}
	// anything a human pinned is exempt (the admin's scratch).
	if slopEntryHumanPinned(entry) {
		return false
	}
	// only settled entries: nothing younger than 7 days.
	if now.Sub(entry.CreatedAt) < slopEligibilityAge {
		return false
	}
	// only active material is a candidate: already archived/quarantined/expired
	// entries are settled business.
	if memoryEntryRelevance(entry) != relevanceActive {
		return false
	}
	return true
}

func slopEntryHumanPinned(entry meetingMemoryEntry) bool {
	return strings.TrimSpace(entry.Metadata["pinned"]) == "true" ||
		strings.TrimSpace(entry.Metadata["humanPinned"]) == "true"
}

// applySlopVerdict stamps the classifier's decision on one candidate and fans a
// quarantine_change event on a real transition. Returns the resulting relevance
// (relevanceActive for keep). Thresholds bias to keep.
func (app *kanbanBoardApp) applySlopVerdict(entry meetingMemoryEntry, verdict slopVerdict) string {
	now := time.Now().UTC()
	confidence := verdict.Confidence
	reason := trimForStorage(normalizeMemoryText(verdict.Reason), 200)
	evidence := trimForStorage(normalizeMemoryText(verdict.Evidence), 240)

	updates := map[string]string{
		"classifierVerdict": strings.ToLower(strings.TrimSpace(verdict.Verdict)),
		"classifierScore":   strconv.FormatFloat(confidence, 'f', 2, 64),
		"classifierReason":  reason,
	}
	if evidence != "" {
		updates["classifierEvidence"] = evidence
	}

	target := relevanceActive
	switch strings.ToLower(strings.TrimSpace(verdict.Verdict)) {
	case "quarantine", relevanceQuarantined:
		if confidence >= slopQuarantineConfidence {
			target = relevanceQuarantined
			updates[relevanceMetadataKey] = relevanceQuarantined
			updates["quarantinedAt"] = now.Format(time.RFC3339Nano)
			updates["expiresAt"] = now.Add(slopQuarantineExpiry).Format(time.RFC3339Nano)
			updates["reviewedBy"] = reviewedByClassifier
		} else {
			// below threshold → keep and re-evaluate a later pass.
			updates["classifierVerdict"] = "keep"
		}
	case relevanceArchived, "archive":
		if confidence >= slopArchiveConfidence {
			target = relevanceArchived
			updates[relevanceMetadataKey] = relevanceArchived
			updates["archivedAt"] = now.Format(time.RFC3339Nano)
			updates["reviewedBy"] = reviewedByClassifier
		} else {
			updates["classifierVerdict"] = "keep"
		}
	default:
		updates["classifierVerdict"] = "keep"
	}

	stamped, _, err := app.memory.updateEntryWithMetadata(entry.Kind, entry.ID, entry.Text, updates)
	if err != nil {
		log.Errorf("%s failed to stamp verdict on %s: %v", slopClassifierAgentName, entry.ID, err)
		return relevanceActive
	}
	if target == relevanceQuarantined || target == relevanceArchived {
		broadcastOSEvent(osEvent{
			Kind:          osEventQuarantineChange,
			Ref:           entry.ID,
			Title:         slopEntryEventTitle(stamped),
			OriginSurface: "quarantine",
			Actor:         reviewedByClassifier,
		})
	}
	return target
}

// sweepExpiredQuarantine hard-deletes quarantined entries past their expiry —
// the ONLY hard delete in the system — each leaving a slop_pass audit stub that
// records the deleted id + reason so the FACT of deletion survives. forwardCursor
// keeps the audit stub carrying the transcript cursor so it never strands the
// classification window.
func (app *kanbanBoardApp) sweepExpiredQuarantine(forwardCursor string) map[string][]scopedRoomDeliveryAcknowledgement {
	now := time.Now().UTC()
	artifactAcknowledgements := map[string][]scopedRoomDeliveryAcknowledgement{}
	for _, entry := range app.memory.entriesByRelevance(relevanceQuarantined) {
		expiresAt, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(entry.Metadata["expiresAt"]))
		if err != nil || !now.After(expiresAt) {
			continue
		}
		removed, acknowledgements, deleted, delErr := app.deleteEntryByIDAcknowledged(entry.ID)
		if delErr != nil {
			log.Errorf("%s expiry failed to delete %s: %v", slopClassifierAgentName, entry.ID, delErr)
			if !deleted {
				continue
			}
		}
		if !deleted {
			continue
		}
		if removed.Kind == meetingMemoryKindOSArtifact {
			artifactAcknowledgements[removed.ID] = acknowledgements
		}
		reason := firstNonEmptyString(strings.TrimSpace(removed.Metadata["classifierReason"]), "quarantine expired after 30 days")
		auditMetadata := map[string]string{
			"deletedId":             removed.ID,
			"deletedKind":           removed.Kind,
			"reason":                reason,
			"deletedAt":             now.Format(time.RFC3339Nano),
			slopClassifierCursorKey: forwardCursor,
		}
		if _, _, err := app.memory.appendSlopPass(durableTimestampID("slop-expiry", time.Now()), "Expired and deleted "+removed.ID, auditMetadata); err != nil {
			log.Errorf("%s expiry failed to write audit stub for %s: %v", slopClassifierAgentName, removed.ID, err)
		}
		broadcastOSEvent(osEvent{
			Kind:          osEventQuarantineChange,
			Ref:           removed.ID,
			Title:         "Expired from memory",
			OriginSurface: "quarantine",
			Actor:         reviewedByClassifier,
		})
	}
	return artifactAcknowledgements
}

// signalDistilledCompactionAge is the reprieve between a signal's distillation
// into a profile (taste_analyst.go stamps distilledInto/distilledAt) and its
// compaction out of the store.
const signalDistilledCompactionAge = 30 * 24 * time.Hour

// signalCompactionEligible reports whether one signal has been distilled into
// a profile AND is past the 30-day reprieve. A missing/garbled distilledAt
// falls back to the signal's own CreatedAt — never newer, so a stamped signal
// can never dodge compaction forever on a malformed timestamp.
//
// System/external signals — actor resolves to NO roster user (share_opened's
// actor=external, pdf_exported's actor=render_runner) — have no taste analyst
// to consume them, so distilledInto is structurally unreachable for them;
// they age out on the same 30-day reprieve from their own CreatedAt instead
// of accumulating forever in the RAM-held store.
func signalCompactionEligible(entry meetingMemoryEntry, now time.Time) bool {
	if entry.Kind != meetingMemoryKindSignal {
		return false
	}
	if strings.TrimSpace(entry.Metadata[signalDistilledIntoKey]) == "" {
		if signalActorOnRoster(entry) {
			return false
		}
		return now.Sub(entry.CreatedAt) >= signalDistilledCompactionAge
	}
	distilledAt, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(entry.Metadata[signalDistilledAtKey]))
	if err != nil {
		distilledAt = entry.CreatedAt
	}
	return now.Sub(distilledAt) >= signalDistilledCompactionAge
}

// signalActorOnRoster reports whether a signal's actor resolves to any roster
// user via the same matcher the taste analyst admits windows with — the exact
// set of signals that CAN eventually be distilled and must therefore wait for
// their distilledInto stamp.
func signalActorOnRoster(entry meetingMemoryEntry) bool {
	for _, userName := range meetingParticipantNames {
		if signalActorMatches(entry, userName) {
			return true
		}
	}
	return false
}

// sweepDistilledSignals hard-deletes every compaction-eligible signal in ONE
// batched rewrite (signals are the highest-volume kind in a JSONL store held
// in RAM and rewritten whole — never one rewrite per signal), leaving ONE
// slop_pass audit stub per sweep that records the count and the profiles the
// signals were distilled into, so the fact of compaction survives. No
// per-entry events: signals are invisible UI state, not tray material.
func (app *kanbanBoardApp) sweepDistilledSignals(forwardCursor string) {
	if app == nil || app.memory == nil {
		return
	}
	now := time.Now().UTC()
	ids := make([]string, 0)
	profiles := make([]string, 0)
	seenProfiles := map[string]struct{}{}
	for _, entry := range app.memory.entriesOfKind(meetingMemoryKindSignal, 0) {
		if !slopCandidateEligible(entry, now) {
			continue
		}
		ids = append(ids, entry.ID)
		profileID := strings.TrimSpace(entry.Metadata[signalDistilledIntoKey])
		if _, seen := seenProfiles[profileID]; !seen && profileID != "" {
			seenProfiles[profileID] = struct{}{}
			profiles = append(profiles, profileID)
		}
	}
	if len(ids) == 0 {
		return
	}

	deleted, err := app.memory.deleteEntriesByID(ids)
	if err != nil {
		log.Errorf("%s signal compaction failed: %v", slopClassifierAgentName, err)
		return
	}
	if deleted == 0 {
		return
	}
	auditMetadata := map[string]string{
		"compactedCount":        strconv.Itoa(deleted),
		"deletedKind":           meetingMemoryKindSignal,
		"distilledInto":         strings.Join(profiles, ","),
		"reason":                "distilled into taste profile(s) and past the 30-day reprieve",
		"deletedAt":             now.Format(time.RFC3339Nano),
		slopClassifierCursorKey: forwardCursor,
	}
	if _, _, err := app.memory.appendSlopPass(durableTimestampID("slop-compaction", time.Now()), "Compacted "+strconv.Itoa(deleted)+" distilled signal(s)", auditMetadata); err != nil {
		log.Errorf("%s signal compaction failed to write audit stub: %v", slopClassifierAgentName, err)
	}
}

// deleteEntriesByID hard-deletes a batch of entries in ONE rewrite — the
// compaction sweep's terminal step (deleteEntryByID would rewrite the whole
// file once per signal). Restores the in-memory slice on a failed rewrite.
func (store *meetingMemoryStore) deleteEntriesByID(ids []string) (int, error) {
	if store == nil {
		return 0, fmt.Errorf("memory store is unavailable")
	}
	want := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if id = strings.TrimSpace(id); id != "" {
			want[id] = struct{}{}
		}
	}
	if len(want) == 0 {
		return 0, nil
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	previous := store.entries
	next := make([]meetingMemoryEntry, 0, len(previous))
	removed := make([]string, 0, len(want))
	for _, entry := range previous {
		if _, matched := want[entry.ID]; matched {
			if entry.Kind == meetingMemoryKindOSArtifact {
				return 0, fmt.Errorf("OS artifacts require the projection-backed delete seam")
			}
			removed = append(removed, entry.ID)
			continue
		}
		next = append(next, entry)
	}
	if len(removed) == 0 {
		return 0, nil
	}
	store.entries = next
	if err := store.rewriteLocked(false); err != nil {
		store.entries = previous
		return 0, err
	}
	for _, id := range removed {
		delete(store.seen, id)
	}
	return len(removed), nil
}

// slopEntryEventTitle derives a body-free label for the push channel (titles
// only): an artifact carries a real title; a transcript is labeled by kind so no
// spoken content crosses the boundary. The tray fetches the detail by ref.
func slopEntryEventTitle(entry meetingMemoryEntry) string {
	if entry.Kind == meetingMemoryKindOSArtifact {
		if title := strings.TrimSpace(entry.Metadata["title"]); title != "" {
			return title
		}
		return "Draft artifact"
	}
	return "Transcript segment"
}

// --- prompt + I/O contract ---

type slopVerdict struct {
	EntryID    string  `json:"entry_id"`
	Verdict    string  `json:"verdict"`
	Confidence float64 `json:"confidence"`
	Reason     string  `json:"reason"`
	Evidence   string  `json:"evidence"`
}

// parseSlopClassifierOutput accepts a bare JSON array or an object wrapping one
// under "results"/"verdicts"/"candidates", with the stray-fence tolerance the
// other ambient parsers use.
func parseSlopClassifierOutput(text string) ([]slopVerdict, bool) {
	text = strings.TrimSpace(text)
	if fenced := strings.TrimPrefix(text, "```json"); fenced != text {
		text = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(fenced), "```"))
	} else if fenced := strings.TrimPrefix(text, "```"); fenced != text {
		text = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(fenced), "```"))
	}
	if text == "" {
		return nil, false
	}
	if strings.HasPrefix(text, "[") {
		var verdicts []slopVerdict
		if json.Unmarshal([]byte(text), &verdicts) == nil {
			return verdicts, true
		}
		return nil, false
	}
	var wrapper struct {
		Results    []slopVerdict `json:"results"`
		Verdicts   []slopVerdict `json:"verdicts"`
		Candidates []slopVerdict `json:"candidates"`
	}
	if json.Unmarshal([]byte(text), &wrapper) != nil {
		return nil, false
	}
	switch {
	case len(wrapper.Results) > 0:
		return wrapper.Results, true
	case len(wrapper.Verdicts) > 0:
		return wrapper.Verdicts, true
	case len(wrapper.Candidates) > 0:
		return wrapper.Candidates, true
	}
	// a valid object with an empty list is a legitimate empty pass.
	return []slopVerdict{}, true
}

// slopClassifierInstructions is the Domain Strategist's classifier prompt
// (domain §4.3) used verbatim as the system prompt, including the hard rules.
func slopClassifierInstructions() string {
	return strings.Join([]string{
		"## ROLE",
		"You are the studio's knowledge steward. Your job is to keep the company's",
		"memory DENSE: every entry should be a receipt for something the studio does.",
		"You are conservative — quarantine is reversible but you still err toward KEEP",
		"when unsure, because a wrongly-quarantined entry costs the studio a memory.",
		"",
		"## THE TEST",
		"For each candidate entry, decide: is this, or could this plausibly become, a",
		"receipt for a package, a decision, a deliverable, or a portfolio fact?",
		"  - YES, or attached/cited/acted-on ever → KEEP.",
		"  - Was attached to a package but that package/context is dead → ARCHIVE.",
		"  - Was published/sent to a human ever → KEEP or ARCHIVE, never quarantine.",
		"  - None of the above, orphaned, duplicative, or superseded-and-never-sent,",
		"    AND older than 7 days AND not human-pinned → QUARANTINE.",
		"",
		"## HARD RULES (never violate)",
		"- Never quarantine an entry younger than 7 days.",
		"- Never quarantine a transcript that produced any theme/decision/card — operate",
		"  at segment level; keep the substantive segments.",
		"- Never quarantine anything a human published, pinned, or attached.",
		"- Never quarantine anything ever attached to a package (archive instead).",
		"",
		"## OUTPUT (per candidate, machine-parseable):",
		"Return STRICT JSON only: a JSON array where each element is",
		`  {"entry_id": string, "verdict": "keep"|"archive"|"quarantine", "confidence": 0.0-1.0,`,
		`   "reason": <one line>, "evidence": <what it was/wasn't attached to>}.`,
		"Include one element per candidate id supplied. No prose outside the JSON.",
		"",
		"## CONFIDENCE THRESHOLDS",
		"- quarantine requires confidence >= 0.85. Below that → keep and re-evaluate",
		"  next pass. A borderline slop entry costs nothing to keep one more cycle; a",
		"  wrongly-discarded decision costs the studio.",
	}, "\n")
}

// buildSlopClassifierInput formats the candidate batch — id, kind, age, the
// linkage evidence the deny-list already computed, and a bounded excerpt — plus
// the package roster so "attached to a package" can be judged.
func (app *kanbanBoardApp) buildSlopClassifierInput(candidates []meetingMemoryEntry, now time.Time) string {
	location := meetingTimeLocation()
	var builder strings.Builder
	builder.WriteString("# Generated at\n")
	builder.WriteString(now.In(location).Format(time.RFC1123))

	if packages := app.venturePackagesSnapshot(); len(packages) > 0 {
		builder.WriteString("\n\n# Live venture packages (attachment context)\n")
		for _, record := range packages {
			builder.WriteString("- ")
			builder.WriteString(record.Name)
			builder.WriteByte('\n')
		}
	}

	builder.WriteString("\n# Candidate entries to classify\n")
	for _, entry := range candidates {
		ageDays := int(now.Sub(entry.CreatedAt).Hours() / 24)
		builder.WriteString("- entry_id=")
		builder.WriteString(entry.ID)
		builder.WriteString(" kind=")
		builder.WriteString(entry.Kind)
		builder.WriteString(" age_days=")
		builder.WriteString(strconv.Itoa(ageDays))
		if entry.Kind == meetingMemoryKindOSArtifact {
			builder.WriteString(" published=")
			builder.WriteString(firstNonEmptyString(strings.TrimSpace(entry.Metadata["published"]), "false"))
			builder.WriteString(" attached=")
			if strings.TrimSpace(entry.Metadata["packageId"]) != "" {
				builder.WriteString("true")
			} else {
				builder.WriteString("false")
			}
			if title := strings.TrimSpace(entry.Metadata["title"]); title != "" {
				builder.WriteString(" title=")
				builder.WriteString(strconv.Quote(title))
			}
		}
		builder.WriteByte('\n')
		builder.WriteString("  excerpt: ")
		builder.WriteString(trimForStorage(normalizeMemoryText(entry.Text), 280))
		builder.WriteByte('\n')
	}

	return builder.String()
}

// --- quarantine tray: list / restore / delete ---

// quarantineListPayloads shapes the quarantined entries for the Wave-8 tray,
// newest first, carrying the 10-second-decision kit (title, reason, linkage
// evidence, age, expiry). Behind the session auth guard, so excerpts are safe.
func (app *kanbanBoardApp) quarantineListPayloads() []map[string]any {
	if app == nil || app.memory == nil {
		return []map[string]any{}
	}
	entries := app.memory.entriesByRelevance(relevanceQuarantined)
	payloads := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		payloads = append(payloads, quarantineEntryPayload(entry))
	}
	return payloads
}

func (app *kanbanBoardApp) quarantineListPayloadsForPrincipal(principal RecallPrincipal) []map[string]any {
	if app == nil || app.memory == nil {
		return []map[string]any{}
	}
	type candidate struct {
		entry  meetingMemoryEntry
		header ArtifactAuthorizationHeader
	}
	var candidates []candidate
	app.memory.mu.Lock()
	for _, stored := range app.memory.entries {
		if memoryEntryRelevance(stored) != relevanceQuarantined || !recallEntryScopeAllowed(stored.Metadata, principal) {
			continue
		}
		item := candidate{}
		if stored.Kind == meetingMemoryKindOSArtifact {
			item.header = app.memory.resolveArtifactHeaderSecurityLocked(artifactAuthorizationHeaderFromEntry(meetingMemoryEntry{ID: stored.ID, Kind: stored.Kind, Metadata: stored.Metadata}))
			item.entry = meetingMemoryEntry{ID: stored.ID, Kind: stored.Kind}
		} else {
			item.entry = cloneMemoryEntry(stored)
		}
		candidates = append(candidates, item)
	}
	app.memory.mu.Unlock()
	payloads := make([]map[string]any, 0, len(candidates))
	for _, candidate := range candidates {
		entry := candidate.entry
		if entry.Kind == meetingMemoryKindOSArtifact {
			if !artifactHeaderAuthorized(context.Background(), principal.User, ACLReadContent, candidate.header) {
				continue
			}
			var found bool
			entry, found = app.memory.artifactSnapshotIfHeaderMatches(entry.ID, candidate.header)
			if !found {
				continue
			}
		}
		payloads = append(payloads, quarantineEntryPayload(entry))
	}
	return payloads
}

func quarantineEntryPayload(entry meetingMemoryEntry) map[string]any {
	title := slopEntryEventTitle(entry)
	if entry.Kind == meetingMemoryKindTranscript {
		// behind auth: a readable excerpt is the transcript's title line.
		title = trimForStorage(normalizeMemoryText(entry.Text), 90)
	}
	payload := map[string]any{
		"id":        entry.ID,
		"kind":      entry.Kind,
		"title":     title,
		"reason":    strings.TrimSpace(entry.Metadata["classifierReason"]),
		"evidence":  strings.TrimSpace(entry.Metadata["classifierEvidence"]),
		"score":     strings.TrimSpace(entry.Metadata["classifierScore"]),
		"relevance": relevanceQuarantined,
		"createdAt": entry.CreatedAt.UTC().Format(time.RFC3339Nano),
	}
	if quarantinedAt := strings.TrimSpace(entry.Metadata["quarantinedAt"]); quarantinedAt != "" {
		payload["quarantinedAt"] = quarantinedAt
	}
	if expiresAt := strings.TrimSpace(entry.Metadata["expiresAt"]); expiresAt != "" {
		payload["expiresAt"] = expiresAt
	}
	if published := strings.TrimSpace(entry.Metadata["published"]); published != "" {
		payload["published"] = published == "true"
	}
	payload["attached"] = strings.TrimSpace(entry.Metadata["packageId"]) != ""
	return payload
}

// restoreQuarantinedEntry flips a quarantined entry back to active (available
// to any authenticated user — undoing the classifier is always safe), stamps
// the human reviewer, and fans a quarantine_change event.
func (app *kanbanBoardApp) restoreQuarantinedEntry(id string, reviewerEmail string) (map[string]any, error) {
	if app == nil || app.memory == nil {
		return nil, errQuarantineUnavailable
	}
	entry, found := app.memory.entryByID(id)
	if !found {
		return nil, errQuarantineNotFound
	}
	if memoryEntryRelevance(entry) != relevanceQuarantined {
		return nil, errQuarantineNotQuarantined
	}
	updates := map[string]string{
		relevanceMetadataKey: relevanceActive,
		"reviewedBy":         strings.TrimSpace(reviewerEmail),
		"restoredAt":         time.Now().UTC().Format(time.RFC3339Nano),
		"quarantinedAt":      "",
		"expiresAt":          "",
	}
	restored, _, err := app.memory.updateEntryWithMetadata(entry.Kind, entry.ID, entry.Text, updates)
	if err != nil {
		return nil, err
	}
	// Signal capture (spec §5 item 6): a human overruling the slop classifier
	// is a precision datum on the classifier and a vote for the entry —
	// carrying the classifier's own reason so distillation can study where it
	// misfires. Log-and-continue inside; never fails the restore.
	app.recordSignalEvent(strings.TrimSpace(reviewerEmail), signalEventQuarantineRestored, signalValencePositive, restored.ID, restored.Metadata["packageId"], map[string]string{
		"entryKind":        restored.Kind,
		"classifierReason": strings.TrimSpace(restored.Metadata["classifierReason"]),
	})
	broadcastOSEvent(osEvent{
		Kind:          osEventQuarantineChange,
		Ref:           restored.ID,
		Title:         slopEntryEventTitle(restored),
		OriginSurface: "quarantine",
		Actor:         strings.TrimSpace(reviewerEmail),
	})
	return map[string]any{
		"id":        restored.ID,
		"kind":      restored.Kind,
		"relevance": relevanceActive,
	}, nil
}

// deleteQuarantinedEntry hard-deletes a quarantined entry now (admin only),
// leaving a slop_pass audit stub — the same terminal step the expiry sweep runs,
// on demand.
func (app *kanbanBoardApp) deleteQuarantinedEntry(id string, adminEmail string) error {
	_, err := app.deleteQuarantinedEntryAcknowledged(id, adminEmail)
	return err
}

func (app *kanbanBoardApp) deleteQuarantinedEntryAcknowledged(id string, adminEmail string) ([]scopedRoomDeliveryAcknowledgement, error) {
	if app == nil || app.memory == nil {
		return nil, errQuarantineUnavailable
	}
	entry, found := app.memory.entryByID(id)
	if !found {
		return nil, errQuarantineNotFound
	}
	if memoryEntryRelevance(entry) != relevanceQuarantined {
		return nil, errQuarantineNotQuarantined
	}
	removed, acknowledgements, deleted, err := app.deleteEntryByIDAcknowledged(entry.ID)
	if err != nil {
		if !deleted {
			return nil, err
		}
	}
	if !deleted {
		return nil, errQuarantineNotFound
	}
	now := time.Now().UTC()
	reason := firstNonEmptyString(strings.TrimSpace(removed.Metadata["classifierReason"]), "deleted by admin")
	auditMetadata := map[string]string{
		"deletedId":             removed.ID,
		"deletedKind":           removed.Kind,
		"reason":                reason,
		"deletedAt":             now.Format(time.RFC3339Nano),
		"deletedBy":             strings.TrimSpace(adminEmail),
		slopClassifierCursorKey: app.newestSlopCursor(),
	}
	if _, _, auditErr := app.memory.appendSlopPass(durableTimestampID("slop-delete", time.Now()), "Deleted "+removed.ID+" by admin", auditMetadata); auditErr != nil {
		log.Errorf("%s admin delete failed to write audit stub for %s: %v", slopClassifierAgentName, removed.ID, auditErr)
	}
	broadcastOSEvent(osEvent{
		Kind:          osEventQuarantineChange,
		Ref:           removed.ID,
		Title:         "Deleted from memory",
		OriginSurface: "quarantine",
		Actor:         strings.TrimSpace(adminEmail),
	})
	return acknowledgements, err
}

var (
	errQuarantineUnavailable    = &quarantineError{"quarantine is unavailable"}
	errQuarantineNotFound       = &quarantineError{"entry not found"}
	errQuarantineNotQuarantined = &quarantineError{"entry is not quarantined"}
)

type quarantineError struct{ message string }

func (e *quarantineError) Error() string { return e.message }

// assistantQuarantineHandler serves GET /assistant/quarantine — the quarantined
// list, newest first, for any authenticated user.
func assistantQuarantineHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !websocketOriginAllowed(r) {
		writeAuthError(w, http.StatusForbidden, "cross-origin request rejected")
		return
	}
	if userFromRequest(r) == nil {
		writeAuthError(w, http.StatusUnauthorized, "not signed in")
		return
	}
	if kanbanApp == nil {
		writeAuthError(w, http.StatusServiceUnavailable, "quarantine is unavailable")
		return
	}

	writeAuthJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"entries": kanbanApp.quarantineListPayloads(),
	})
}

// assistantQuarantineActionHandler serves POST /assistant/quarantine/{id}/restore
// (any authenticated user) and POST /assistant/quarantine/{id}/delete (admin
// only). Same origin + session guards as the proposal action handler.
func assistantQuarantineActionHandler(w http.ResponseWriter, r *http.Request) {
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
		writeAuthError(w, http.StatusServiceUnavailable, "quarantine is unavailable")
		return
	}

	suffix := strings.Trim(strings.TrimPrefix(r.URL.Path, "/assistant/quarantine/"), "/")
	parts := strings.Split(suffix, "/")
	if suffix == "" || len(parts) != 2 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	id, action := parts[0], parts[1]

	switch action {
	case "restore":
		payload, err := kanbanApp.restoreQuarantinedEntry(id, user.Email)
		if err != nil {
			writeQuarantineError(w, err)
			return
		}
		writeAuthJSON(w, http.StatusOK, map[string]any{"ok": true, "entry": payload})
	case "delete":
		if !isArtifactApprovalAdmin(user) {
			writeAuthError(w, http.StatusForbidden, "only an admin can delete memory now")
			return
		}
		if err := kanbanApp.deleteQuarantinedEntry(id, user.Email); err != nil {
			writeQuarantineError(w, err)
			return
		}
		writeAuthJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id, "deleted": true})
	default:
		http.NotFound(w, r)
	}
}

func writeQuarantineError(w http.ResponseWriter, err error) {
	switch err {
	case errQuarantineNotFound:
		writeAuthError(w, http.StatusNotFound, err.Error())
	case errQuarantineNotQuarantined:
		writeAuthError(w, http.StatusConflict, err.Error())
	case errQuarantineUnavailable:
		writeAuthError(w, http.StatusServiceUnavailable, err.Error())
	default:
		writeAuthError(w, http.StatusBadRequest, err.Error())
	}
}
