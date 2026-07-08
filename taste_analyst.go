package main

// Taste Analyst — the sixth ambient worker (agent_runner.go recipe; the slop
// classifier precedent for owning its loop), the distillation half of the
// packaging OS §5 flywheel. Per participant (participants.go roster) it reads
// the user's unconsumed signal window — every kind=signal entry that user
// produced at the capture seams (edit diffs, publishes, survey verdicts,
// goal-lesson signals — all carry actor=user) — and maintains ONE living
// `user_profile` os_artifact per person: voice & style, recurring objections,
// comp taste, a do/don't list, every bullet evidence-cited to signal ids.
//
// Cadence (spec §5, Wave 3 item 15): a pass runs for a user when >= minBatch
// (default 15) unconsumed signals exist, OR weekly — the profile is older than
// 7 days (a profile-less user's clock anchors on their oldest waiting signal)
// and at least one signal is waiting. Zero signals is never a pass.
//
// Cursor: per-user, on the profile artifact itself (tasteConsumedThrough =
// last consumed signal id) — the generic newest-artifact cursor cannot work
// because the profile is UPDATED IN PLACE (never re-minted; the artifact-model
// versioning applies automatically on update). Consumed signals are also
// stamped distilledInto=<profileArtifactId> in ONE batched rewrite (the JSONL
// store is held in RAM and rewritten whole — never one rewrite per signal),
// which is both the compaction trigger (slop_classifier.go sweeps distilled
// signals after 30 days) and the belt to the cursor's suspenders: a compacted
// cursor target just means the distilledInto filter carries the window.
//
// Decision-ledger candidates and supersessions are PROPOSED, never written:
// they land as metadata on the profile artifact (ledgerProposals, status
// proposed) for a human to confirm — the codex_proposals/office_brief
// discipline. Direct ledger writes from an ambient worker are forbidden.
//
// Model: the Anthropic text helper (anthropic_text.go) at effort medium —
// taste distillation is a judgment surface, not a latency one. Keyless (no
// ANTHROPIC_API_KEY): the worker never starts, silently, like the goal engine.
//
// Guardrails (spec §5): bias to under-claim — a six-person office is thin
// data, so the prompt demands the ledger's explicit-only discipline and the
// pass is SKIPPED (cursor untouched, retried next tick) when the output is
// unparseable or cites no supplied signal id.

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	tasteAnalystAgentName       = "taste_analyst"
	defaultTasteAnalystInterval = time.Hour
	tasteAnalystRequestTimeout  = 2 * time.Minute
	defaultTasteAnalystMinBatch = 15
	defaultTasteAnalystMaxBatch = 60
	tasteAnalystEffort          = "medium"
	tasteAnalystMaxOutputTokens = 2500
	tasteAnalystMaxProposals    = 5
	tasteAnalystProposalTextCap = 300
	tasteAnalystDecisionContext = 20
	// tasteAnalystWeeklyStale is the weekly override: below minBatch, a pass
	// still runs once the profile (or, before one exists, the oldest waiting
	// signal) is at least this old.
	tasteAnalystWeeklyStale = 7 * 24 * time.Hour

	// tasteAnalystCursorKey is the per-user durable cursor, stamped on the
	// profile artifact: the last signal id consumed into it.
	tasteAnalystCursorKey = "tasteConsumedThrough"
	// tasteProfileArtifactType marks the living profile among os_artifacts
	// (metadata artifactType) so lookup never depends on title text.
	tasteProfileArtifactType    = "user_profile"
	tasteProfileArtifactTypeKey = "artifactType"
	tasteProfileUserKey         = "profileUser"
	tasteProfileDistilledAtKey  = "distilledAt"
	// tasteProfileProposalsKey holds the analyst's decision-ledger candidates
	// and supersessions as compact JSON on the profile artifact — recorded
	// proposals, never ledger writes; the confirm seam reads them from here.
	tasteProfileProposalsKey = "ledgerProposals"

	// signalDistilledIntoKey/-AtKey stamp a consumed signal with the profile
	// that absorbed it — the §5 compaction trigger (slop_classifier.go).
	signalDistilledIntoKey = "distilledInto"
	signalDistilledAtKey   = "distilledAt"

	tasteProposalStatusProposed = "proposed"
)

func tasteAnalystAgent() ambientAgentConfig {
	return ambientAgentConfig{
		name:              tasteAnalystAgentName,
		defaultInterval:   defaultTasteAnalystInterval,
		intervalEnv:       "TASTE_ANALYST_INTERVAL",
		disabledEnv:       "TASTE_ANALYST_DISABLED",
		minBatchEnv:       "TASTE_ANALYST_MIN_SIGNALS",
		defaultMinBatch:   defaultTasteAnalystMinBatch,
		maxBatchEnv:       "TASTE_ANALYST_MAX_SIGNALS",
		defaultMaxBatch:   defaultTasteAnalystMaxBatch,
		inputKind:         meetingMemoryKindSignal,
		artifactKind:      meetingMemoryKindOSArtifact,
		cursorMetadataKey: tasteAnalystCursorKey,
		requestTimeout:    tasteAnalystRequestTimeout,
		// produce is unused: cursors are per-user on each living profile (which
		// updates in place), so the newest-artifact cursor scan of the generic
		// loop cannot apply — the analyst owns its loop, the slop precedent.
		// backfillEnv is deliberately absent: signals exist ONLY to be
		// distilled, so the first pass always reads the whole waiting history.
	}
}

// ensureTasteAnalystStarted is the registration seam called from
// startAmbientAgent (agent_runner.go): when the brain worker (or any ambient
// agent) registers at room join, the analyst registers alongside on its own
// key. Idempotent via the agent bookkeeping map — a later JoinConferenceRoom
// still replaces cleanly through startTasteAnalystWorker's swap logic.
func (app *kanbanBoardApp) ensureTasteAnalystStarted() {
	if app == nil {
		return
	}
	app.mu.Lock()
	_, registered := app.agentCancels[tasteAnalystAgentName]
	app.mu.Unlock()
	if registered {
		return
	}
	app.startTasteAnalystWorker(currentAnthropicAPIKey())
}

// startTasteAnalystWorker registers the per-user analyst loop. Keyless (no
// ANTHROPIC_API_KEY) it silently never starts — the goal-engine posture; the
// rest of the OS is untouched.
func (app *kanbanBoardApp) startTasteAnalystWorker(apiKey string) {
	agent := tasteAnalystAgent()
	if app == nil || app.memory == nil || strings.TrimSpace(apiKey) == "" || boolEnv(agent.disabledEnv) {
		return
	}
	interval := agent.interval()
	if interval <= 0 {
		return
	}

	cancel := make(chan struct{})
	done := make(chan struct{})

	app.mu.Lock()
	if app.agentCancels == nil {
		app.agentCancels = map[string]chan struct{}{}
		app.agentDones = map[string]chan struct{}{}
	}
	oldCancel := app.agentCancels[agent.name]
	oldDone := app.agentDones[agent.name]
	app.agentCancels[agent.name] = cancel
	app.agentDones[agent.name] = done
	app.mu.Unlock()

	if oldCancel != nil {
		close(oldCancel)
		if oldDone != nil {
			<-oldDone
		}
	}

	go app.runTasteAnalystLoop(agent, apiKey, interval, cancel, done)
}

func (app *kanbanBoardApp) runTasteAnalystLoop(agent ambientAgentConfig, apiKey string, interval time.Duration, cancel <-chan struct{}, done chan<- struct{}) {
	defer close(done)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			// No tick-wide deadline here: runTasteAnalystOnce derives a FRESH
			// per-user timeout for each roster pass, so one slow distillation
			// can never exhaust a shared budget and starve the trailing users.
			if err := app.runTasteAnalystOnce(context.Background(), apiKey, nil); err != nil {
				log.Errorf("%s worker failed: %v", agent.name, err)
			}
		case <-cancel:
			return
		}
	}
}

// runTasteAnalystOnce is one whole tick: one gated pass per roster user,
// serialized by the per-agent run-lock so overlapping ticks never consume the
// same window twice. One user's failure never starves the rest of the roster.
func (app *kanbanBoardApp) runTasteAnalystOnce(ctx context.Context, apiKey string, responder anthropicTextResponder) error {
	if app == nil || app.memory == nil {
		return nil
	}
	if responder == nil {
		responder = createAnthropicTextResponse
	}
	agent := tasteAnalystAgent()

	runLock := app.ambientAgentRunLock(agent.name)
	runLock.Lock()
	defer runLock.Unlock()

	var firstErr error
	for _, userName := range meetingParticipantNames {
		// Each user gets their OWN request timeout derived from the caller's
		// context: a slow early call costs only that user's pass, never the
		// fixed-order roster tail (the "one user's failure never starves the
		// rest" contract, made structural).
		userCtx, cancelUser := context.WithTimeout(ctx, agent.requestTimeout)
		err := app.runTasteAnalystForUser(agent, userCtx, apiKey, userName, responder, time.Now().UTC())
		cancelUser()
		if err != nil {
			log.Errorf("%s pass for %s failed: %v", agent.name, userName, err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

// runTasteAnalystForUser runs one gated distillation pass for one user.
func (app *kanbanBoardApp) runTasteAnalystForUser(agent ambientAgentConfig, ctx context.Context, apiKey string, userName string, responder anthropicTextResponder, now time.Time) error {
	profile, hasProfile := app.tasteProfileForUser(userName)
	cursor := ""
	distilledAt := time.Time{}
	if hasProfile {
		cursor = strings.TrimSpace(profile.Metadata[tasteAnalystCursorKey])
		if parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(profile.Metadata[tasteProfileDistilledAtKey])); err == nil {
			distilledAt = parsed
		}
	}

	window := app.memory.unconsumedSignalsForActor(userName, cursor, agent.maxBatch())
	oldestSignalAt := time.Time{}
	if len(window) > 0 {
		oldestSignalAt = window[0].CreatedAt
	}
	if !tasteAnalystShouldRun(len(window), agent.minBatch(), distilledAt, oldestSignalAt, now) {
		return nil
	}

	priorBody := ""
	if hasProfile {
		priorBody = profile.Text
	}
	output, err := responder(ctx, apiKey, anthropicTextRequest{
		Model:        chatModel(),
		Instructions: tasteAnalystInstructions(),
		Input:        app.buildTasteAnalystInput(userName, priorBody, window, now),
		Effort:       tasteAnalystEffort,
		MaxTokens:    tasteAnalystMaxOutputTokens,
	})
	if err != nil {
		return err
	}

	parsed, ok := parseTasteAnalystOutput(output)
	if !ok || strings.TrimSpace(parsed.Profile) == "" {
		// Never advance the cursor on unparseable output: the next pass retries
		// the same window (the decision-ledger/slop precedent).
		log.Errorf("%s returned non-JSON output for %s; skipping this pass", tasteAnalystAgentName, userName)
		return nil
	}
	// Evidence discipline is structural, not just prompted: a profile that
	// cites none of the supplied signal ids has no receipts — skip the pass
	// (cursor untouched) rather than persist uncited claims.
	if !tasteProfileCitesWindow(parsed.Profile, window) {
		log.Errorf("%s profile for %s cited no supplied signal ids; skipping this pass", tasteAnalystAgentName, userName)
		return nil
	}

	proposals := filterTasteProposals(app, parsed.Proposals, window)
	lastSignal := window[len(window)-1]
	title := tasteProfileTitle(userName)
	metadataUpdates := map[string]string{
		tasteAnalystCursorKey:      lastSignal.ID,
		tasteProfileDistilledAtKey: now.Format(time.RFC3339Nano),
		"signalCount":              strconv.Itoa(len(window)),
		"source":                   agentThreadWorkerAnthropic,
		"model":                    chatModel(),
	}
	if encoded := encodeTasteProposals(proposals, now); encoded != "" {
		metadataUpdates[tasteProfileProposalsKey] = encoded
	}

	profileID := ""
	if hasProfile {
		// UPDATE the living profile in place — never mint a duplicate. The
		// artifact-model versioning rides updateOSArtifactWithMetadata for free.
		updated, _, err := app.updateOSArtifactWithMetadata(profile.ID, title, parsed.Profile, scoutParticipantName, metadataUpdates)
		if err != nil {
			return err
		}
		profileID = updated.ID
	} else {
		metadataUpdates["title"] = title
		metadataUpdates[tasteProfileArtifactTypeKey] = tasteProfileArtifactType
		metadataUpdates[tasteProfileUserKey] = userName
		created, appended, err := app.createOSArtifactWithMetadata("workflow", title, parsed.Profile, scoutParticipantName, metadataUpdates)
		if err != nil {
			return err
		}
		if !appended {
			return fmt.Errorf("taste profile for %s was not saved", userName)
		}
		profileID = created.ID
	}

	// Stamp every consumed signal distilledInto=<profile> in ONE rewrite —
	// the compaction trigger. A failed stamp is logged, never fatal: the
	// cursor already advanced, the signals just stay uncompactable.
	if err := app.memory.stampSignalsDistilled(memoryEntryIDs(window), profileID, now); err != nil {
		log.Errorf("%s failed to stamp distilledInto on %d signal(s) for %s: %v", tasteAnalystAgentName, len(window), userName, err)
	}
	return nil
}

// tasteAnalystShouldRun is the gate (spec: >= minBatch unconsumed signals OR
// weekly): zero signals never runs; a full batch always runs; below the batch
// the pass runs only when the last distillation — or, before a profile exists,
// the oldest waiting signal — is at least a week old.
func tasteAnalystShouldRun(unconsumed int, minBatch int, distilledAt time.Time, oldestSignalAt time.Time, now time.Time) bool {
	if unconsumed <= 0 {
		return false
	}
	if minBatch < 1 {
		minBatch = 1
	}
	if unconsumed >= minBatch {
		return true
	}
	anchor := distilledAt
	if anchor.IsZero() {
		anchor = oldestSignalAt
	}
	if anchor.IsZero() {
		return false
	}
	return now.Sub(anchor) >= tasteAnalystWeeklyStale
}

func tasteProfileTitle(userName string) string {
	return "Taste profile — " + strings.TrimSpace(userName)
}

// tasteProfileForUser finds the ONE living profile artifact for a user
// (newest wins if history ever holds duplicates).
func (app *kanbanBoardApp) tasteProfileForUser(userName string) (meetingMemoryEntry, bool) {
	if app == nil || app.memory == nil {
		return meetingMemoryEntry{}, false
	}
	entries := app.memory.entriesOfKind(meetingMemoryKindOSArtifact, 0)
	for index := len(entries) - 1; index >= 0; index-- {
		entry := entries[index]
		if entry.Metadata[tasteProfileArtifactTypeKey] != tasteProfileArtifactType {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(entry.Metadata[tasteProfileUserKey]), strings.TrimSpace(userName)) {
			return entry, true
		}
	}
	return meetingMemoryEntry{}, false
}

// unconsumedSignalsForActor returns up to limit kind=signal entries belonging
// to one actor, oldest first: after the cursor position when the cursor entry
// still exists, and always excluding already-distilled signals (the belt when
// compaction has deleted the cursor target). Per-user isolation lives here:
// the actor stamp is the ONLY admission — an actorless signal reaches nobody.
func (store *meetingMemoryStore) unconsumedSignalsForActor(actor string, cursorID string, limit int) []meetingMemoryEntry {
	if store == nil || limit <= 0 {
		return nil
	}

	store.mu.Lock()
	entries := cloneMemoryEntries(store.entries)
	store.mu.Unlock()

	startIndex := 0
	if cursorID = strings.TrimSpace(cursorID); cursorID != "" {
		for index := len(entries) - 1; index >= 0; index-- {
			if entries[index].ID == cursorID {
				startIndex = index + 1
				break
			}
		}
	}

	window := make([]meetingMemoryEntry, 0, limit)
	for _, entry := range entries[startIndex:] {
		if entry.Kind != meetingMemoryKindSignal {
			continue
		}
		if strings.TrimSpace(entry.Metadata[signalDistilledIntoKey]) != "" {
			continue
		}
		if !signalActorMatches(entry, actor) {
			continue
		}
		window = append(window, entry)
		if len(window) >= limit {
			break
		}
	}
	return window
}

// signalActorMatches admits a signal into one user's window. Seams stamp
// actor as the participant name, except quarantine restores which stamp the
// account email — resolve both, case-insensitively.
func signalActorMatches(entry meetingMemoryEntry, userName string) bool {
	actor := strings.TrimSpace(entry.Metadata["actor"])
	userName = strings.TrimSpace(userName)
	if actor == "" || userName == "" {
		return false
	}
	if strings.EqualFold(actor, userName) {
		return true
	}
	if name := participantNameForEmail(actor); name != "" && strings.EqualFold(name, userName) {
		return true
	}
	return false
}

func memoryEntryIDs(entries []meetingMemoryEntry) []string {
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		ids = append(ids, entry.ID)
	}
	return ids
}

// stampSignalsDistilled marks a consumed batch distilledInto=<profileID> in
// ONE rewrite — the store is held in RAM and rewritten whole, so a 60-signal
// pass must never cost 60 file rewrites. Restores the in-memory slice on a
// failed rewrite, the updateEntryWithMetadata contract.
func (store *meetingMemoryStore) stampSignalsDistilled(ids []string, profileID string, at time.Time) error {
	if store == nil {
		return fmt.Errorf("memory store is unavailable")
	}
	profileID = strings.TrimSpace(profileID)
	if profileID == "" || len(ids) == 0 {
		return nil
	}
	want := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if id = strings.TrimSpace(id); id != "" {
			want[id] = struct{}{}
		}
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	type priorState struct {
		index int
		entry meetingMemoryEntry
	}
	priors := make([]priorState, 0, len(want))
	stamp := at.UTC().Format(time.RFC3339Nano)
	for index, entry := range store.entries {
		if entry.Kind != meetingMemoryKindSignal {
			continue
		}
		if _, matched := want[entry.ID]; !matched {
			continue
		}
		if strings.TrimSpace(entry.Metadata[signalDistilledIntoKey]) == profileID {
			continue
		}
		next := cloneMemoryEntry(entry)
		if next.Metadata == nil {
			next.Metadata = map[string]string{}
		}
		next.Metadata[signalDistilledIntoKey] = profileID
		next.Metadata[signalDistilledAtKey] = stamp
		priors = append(priors, priorState{index: index, entry: store.entries[index]})
		store.entries[index] = next
	}
	if len(priors) == 0 {
		return nil
	}
	if err := store.rewriteLocked(false); err != nil {
		for _, prior := range priors {
			store.entries[prior.index] = prior.entry
		}
		return err
	}
	return nil
}

// --- model I/O contract -------------------------------------------------------

// tasteLedgerProposal is one PROPOSED decision-ledger candidate or
// supersession, recorded on the profile artifact for a human to confirm.
type tasteLedgerProposal struct {
	Kind       string   `json:"kind"` // "candidate" | "supersession"
	Text       string   `json:"text"`
	Supersedes string   `json:"supersedes,omitempty"` // existing decision entry id
	Evidence   []string `json:"evidence,omitempty"`   // signal ids from the window
	Status     string   `json:"status,omitempty"`
	ProposedAt string   `json:"proposedAt,omitempty"`
}

type tasteAnalystOutput struct {
	Profile   string                `json:"profile"`
	Proposals []tasteLedgerProposal `json:"ledgerProposals"`
	// Alt is the "proposals" spelling tolerance, folded in after decode.
	Alt []tasteLedgerProposal `json:"proposals"`
}

// parseTasteAnalystOutput decodes the strict-JSON contract with the
// stray-fence tolerance the other ambient parsers use.
func parseTasteAnalystOutput(text string) (tasteAnalystOutput, bool) {
	text = strings.TrimSpace(text)
	if fenced := strings.TrimPrefix(text, "```json"); fenced != text {
		text = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(fenced), "```"))
	} else if fenced := strings.TrimPrefix(text, "```"); fenced != text {
		text = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(fenced), "```"))
	}
	if text == "" || !strings.HasPrefix(text, "{") {
		return tasteAnalystOutput{}, false
	}
	var output tasteAnalystOutput
	if json.Unmarshal([]byte(text), &output) != nil {
		return tasteAnalystOutput{}, false
	}
	if len(output.Proposals) == 0 && len(output.Alt) > 0 {
		output.Proposals = output.Alt
	}
	output.Alt = nil
	return output, true
}

// tasteProfileCitesWindow requires at least one supplied signal id to appear
// verbatim in the profile body — the evidence-citation floor.
func tasteProfileCitesWindow(profile string, window []meetingMemoryEntry) bool {
	for _, entry := range window {
		if strings.Contains(profile, entry.ID) {
			return true
		}
	}
	return false
}

// filterTasteProposals keeps only proposals whose evidence ids all come from
// the supplied window (never act on an id the model invented — the slop rule)
// and whose supersession target is a real ledger decision. Capped and trimmed.
func filterTasteProposals(app *kanbanBoardApp, proposals []tasteLedgerProposal, window []meetingMemoryEntry) []tasteLedgerProposal {
	inWindow := make(map[string]struct{}, len(window))
	for _, entry := range window {
		inWindow[entry.ID] = struct{}{}
	}

	kept := make([]tasteLedgerProposal, 0, len(proposals))
	for _, proposal := range proposals {
		proposal.Text = trimForStorage(normalizeMemoryText(proposal.Text), tasteAnalystProposalTextCap)
		if proposal.Text == "" {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(proposal.Kind)) {
		case "candidate":
			proposal.Kind = "candidate"
			proposal.Supersedes = ""
		case "supersession":
			proposal.Kind = "supersession"
			proposal.Supersedes = strings.TrimSpace(proposal.Supersedes)
			if proposal.Supersedes == "" {
				continue
			}
			// a supersession of a decision that does not exist is invented.
			if app == nil || app.memory == nil {
				continue
			}
			if _, found := app.memory.entryByKindAndID(meetingMemoryKindDecision, proposal.Supersedes); !found {
				continue
			}
		default:
			continue
		}
		evidence := make([]string, 0, len(proposal.Evidence))
		for _, id := range proposal.Evidence {
			if _, ok := inWindow[strings.TrimSpace(id)]; ok {
				evidence = append(evidence, strings.TrimSpace(id))
			}
		}
		// explicit-only discipline: an uncited proposal is never recorded.
		if len(evidence) == 0 {
			continue
		}
		proposal.Evidence = evidence
		kept = append(kept, proposal)
		if len(kept) >= tasteAnalystMaxProposals {
			break
		}
	}
	return kept
}

// encodeTasteProposals renders the kept proposals as the compact JSON stored
// in the profile's ledgerProposals metadata, each stamped proposed.
func encodeTasteProposals(proposals []tasteLedgerProposal, now time.Time) string {
	if len(proposals) == 0 {
		return ""
	}
	stamp := now.UTC().Format(time.RFC3339Nano)
	for index := range proposals {
		proposals[index].Status = tasteProposalStatusProposed
		proposals[index].ProposedAt = stamp
	}
	encoded, err := json.Marshal(proposals)
	if err != nil {
		return ""
	}
	return string(encoded)
}

// --- prompt --------------------------------------------------------------------

func tasteAnalystInstructions() string {
	return strings.Join([]string{
		"## ROLE",
		"You are Bonfire's Taste Analyst. You maintain ONE living taste profile for",
		"one teammate, distilled from their captured feedback signals: section-level",
		"edit diffs on agent copy, publishes and attaches, re-run asks, proposal",
		"confirms/dismissals, survey verdicts (landed/off with notes), and goal",
		"lessons. The profile is injected into future agent prompts, so every line",
		"must be something an agent can OBEY.",
		"",
		"## THE DOCUMENT",
		"Update the supplied current profile — never restart from scratch; keep what",
		"still holds, revise what the new signals contradict. Write compact markdown",
		"with these sections: Voice & style, Recurring objections, Comp taste,",
		"Do, Don't.",
		"",
		"## EVIDENCE DISCIPLINE (hard rule)",
		"Every bullet MUST cite the signal id(s) it rests on, inline, e.g.",
		"\"Cuts intro throat-clearing (signal-artifact_edited-...)\". A claim with no",
		"signal id is forbidden. This is a six-person office: the data is THIN.",
		"Under-claim. Only patterns explicit in the signals — two or more consistent",
		"signals for a habit, one strong explicit signal (a survey note, a stated",
		"reason) for a preference. \"No clear pattern yet\" is a good section body.",
		"",
		"## LEDGER PROPOSALS",
		"When a rule is so consistent it should bind the whole office, propose it —",
		"do not decide it; a human confirms. Likewise propose a supersession when",
		"the signals contradict an existing ledger decision (cite its entry id).",
		"Most passes should propose NOTHING.",
		"",
		"## OUTPUT (machine-parseable)",
		"Return STRICT JSON only, no prose outside it:",
		`{"profile": "<the full updated markdown profile>",`,
		` "ledgerProposals": [{"kind": "candidate"|"supersession", "text": "<one-line rule>",`,
		`   "supersedes": "<decision entry id, supersession only>", "evidence": ["<signal id>", ...]}]}`,
		"ledgerProposals may be an empty array — that is the expected common case.",
	}, "\n")
}

// buildTasteAnalystInput assembles one user's distillation window: the living
// profile body, the active decision ledger (supersession targets), and the
// unconsumed signals — surveys and goal lessons ride the same actor-stamped
// window as every other seam.
func (app *kanbanBoardApp) buildTasteAnalystInput(userName string, priorProfile string, window []meetingMemoryEntry, now time.Time) string {
	var builder strings.Builder
	builder.WriteString("# Generated at\n")
	builder.WriteString(now.Format(time.RFC3339))
	builder.WriteString("\n\n# Teammate\n")
	builder.WriteString(userName)

	builder.WriteString("\n\n# Current taste profile (living document — update it, never restart)\n")
	if strings.TrimSpace(priorProfile) == "" {
		builder.WriteString("(none yet — this pass writes the first one)\n")
	} else {
		builder.WriteString(priorProfile)
		builder.WriteByte('\n')
	}

	builder.WriteString("\n# Active decision ledger (supersession targets — cite entry ids)\n")
	decisions := app.activeDecisionEntries(tasteAnalystDecisionContext)
	if len(decisions) == 0 {
		builder.WriteString("(no active decisions)\n")
	}
	for _, decision := range decisions {
		builder.WriteString("- ")
		builder.WriteString(decision.ID)
		builder.WriteString(" | ")
		builder.WriteString(trimForStorage(normalizeMemoryText(decision.Text), 200))
		builder.WriteByte('\n')
	}

	builder.WriteString("\n# Unconsumed signal window (cite these ids as evidence)\n")
	for _, entry := range window {
		builder.WriteString("- ")
		builder.WriteString(entry.ID)
		builder.WriteString(" | ")
		builder.WriteString(entry.CreatedAt.Format(time.RFC3339))
		record, ok := decodeSignalEntry(entry)
		if !ok {
			builder.WriteByte('\n')
			continue
		}
		builder.WriteString(" | event=")
		builder.WriteString(record.Event)
		if record.Valence != "" {
			builder.WriteString(" valence=")
			builder.WriteString(record.Valence)
		}
		if record.ArtifactID != "" {
			builder.WriteString(" artifact=")
			builder.WriteString(record.ArtifactID)
		}
		if record.PackageID != "" {
			builder.WriteString(" package=")
			builder.WriteString(record.PackageID)
		}
		if len(record.Payload) > 0 {
			if encoded, err := json.Marshal(record.Payload); err == nil {
				builder.WriteString(" payload=")
				builder.WriteString(trimForStorage(string(encoded), 400))
			}
		}
		builder.WriteByte('\n')
	}

	return builder.String()
}
