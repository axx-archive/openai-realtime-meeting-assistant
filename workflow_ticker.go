package main

// Workflow ticker (card 067, with the merged 068 routing rule) — a ~5-minute
// server-side status re-scan that launches ONLY human-approved, non-disruptive
// agent work. It is deliberately NOT an ambientAgentConfig: it makes no model
// call and appends no artifact, it re-scans codex proposals and, for the narrow
// set that already carry a recorded human approval, drives the SAME confirm
// bookkeeping the HTTP confirm uses (launchApprovedProposal). Two eligible
// shapes:
//
//   A. a proposal a human already CONFIRMED whose launch never stamped a
//      threadId — the crash/failure gap between resolveCodexProposal's
//      persist-before-launch write and its thread-id stamp. Relaunched under
//      the original confirmer, once, past a short grace so a confirm still
//      in-flight is never double-launched.
//   B. an auto_run-lane proposal carrying 069's standing approval
//      (laneApprovedBy). Inert until 069 writes that metadata: a proposal never
//      runs without a recorded human approval.
//
// Cost is structurally bounded: the ticker only ever calls
// launchAgentThreadWithOrigin (one thread run), never the /goal engine or
// packaging_studio, and it launches at most BONFIRE_WORKFLOW_TICKER_MAX_PER_PASS
// proposals per tick. It holds app.proposalMu for the whole pass — the same
// mutex resolveCodexProposal holds for its entire confirm — so a ticker pass and
// an HTTP confirm can never double-launch the same proposal.

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	workflowTickerAgentName         = "workflow ticker"
	defaultWorkflowTickerInterval   = 5 * time.Minute
	defaultWorkflowTickerMaxPerPass = 2
	// workflowTickerConfirmGrace is how long a confirmed-but-unstamped proposal
	// must sit before the ticker relaunches it, so a confirm whose launch/stamp
	// is still in flight (status persisted, thread not yet stamped) is never
	// double-launched. Belt-and-suspenders on top of the shared proposalMu.
	workflowTickerConfirmGrace = 2 * time.Minute
)

// workflowTickerInterval parses BONFIRE_WORKFLOW_TICKER_INTERVAL exactly like
// ambientAgentConfig.interval(): a bare duration, or "0"/"off"/"false"/
// "disabled" to turn the ticker off; anything unparseable falls back to 5m.
func workflowTickerInterval() time.Duration {
	raw := strings.TrimSpace(os.Getenv("BONFIRE_WORKFLOW_TICKER_INTERVAL"))
	if raw == "" {
		return defaultWorkflowTickerInterval
	}
	switch strings.ToLower(raw) {
	case "0", "off", "false", "disabled":
		return 0
	}
	interval, err := time.ParseDuration(raw)
	if err != nil || interval < time.Second {
		return defaultWorkflowTickerInterval
	}
	return interval
}

func workflowTickerMaxPerPass() int {
	return positiveIntEnv("BONFIRE_WORKFLOW_TICKER_MAX_PER_PASS", defaultWorkflowTickerMaxPerPass)
}

func workflowTickerEnabled() bool {
	return workflowTickerInterval() > 0 && !boolEnv("BONFIRE_WORKFLOW_TICKER_DISABLED")
}

var (
	workflowTickerStatMu    sync.Mutex
	workflowTickerLastPass  time.Time
	workflowTickerLastCount int
)

func recordWorkflowTickerPass(now time.Time, launched int) {
	workflowTickerStatMu.Lock()
	workflowTickerLastPass = now
	workflowTickerLastCount = launched
	workflowTickerStatMu.Unlock()
}

// readinessWorkflowTickerSnapshot exposes the ticker's config + last-pass state
// on the readiness agents map, parity with readinessCodexRunnerSnapshot.
func readinessWorkflowTickerSnapshot() map[string]any {
	interval := workflowTickerInterval()
	snapshot := map[string]any{
		"enabled":    workflowTickerEnabled(),
		"interval":   interval.String(),
		"maxPerPass": workflowTickerMaxPerPass(),
	}
	workflowTickerStatMu.Lock()
	lastPass := workflowTickerLastPass
	lastCount := workflowTickerLastCount
	workflowTickerStatMu.Unlock()
	if !lastPass.IsZero() {
		snapshot["lastPassAt"] = lastPass.UTC().Format(time.RFC3339Nano)
		snapshot["lastLaunchCount"] = lastCount
	}
	return snapshot
}

// startWorkflowTicker registers the ticker's cancel/done channels through the
// same app.agentCancels/app.agentDones maps the ambient agents use (so a
// re-JoinConferenceRoom restarts it cleanly) and runs its loop. Disabled forms
// short-circuit before any goroutine starts.
func (app *kanbanBoardApp) startWorkflowTicker() {
	if app == nil || app.memory == nil || boolEnv("BONFIRE_WORKFLOW_TICKER_DISABLED") {
		return
	}
	interval := workflowTickerInterval()
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
	oldCancel := app.agentCancels[workflowTickerAgentName]
	oldDone := app.agentDones[workflowTickerAgentName]
	app.agentCancels[workflowTickerAgentName] = cancel
	app.agentDones[workflowTickerAgentName] = done
	app.mu.Unlock()

	if oldCancel != nil {
		close(oldCancel)
		if oldDone != nil {
			<-oldDone
		}
	}

	go app.runWorkflowTickerLoop(interval, cancel, done)
}

func (app *kanbanBoardApp) runWorkflowTickerLoop(interval time.Duration, cancel <-chan struct{}, done chan<- struct{}) {
	defer close(done)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			app.runWorkflowTickerOnce(time.Now())
		case <-cancel:
			return
		}
	}
}

// runWorkflowTickerOnce scans codex proposals under proposalMu and launches at
// most workflowTickerMaxPerPass eligible, human-approved, non-disruptive
// proposals. Returns the count launched (readiness snapshot + tests). Holding
// proposalMu for the whole pass means a confirm's status persist is visible
// before the ticker reads it, so the two paths can never double-launch.
func (app *kanbanBoardApp) runWorkflowTickerOnce(now time.Time) int {
	if app == nil || app.memory == nil {
		return 0
	}

	app.proposalMu.Lock()
	defer app.proposalMu.Unlock()

	maxPerPass := workflowTickerMaxPerPass()
	launched := 0
	for _, entry := range app.memory.entriesOfKind(meetingMemoryKindCodexProposal, 100) {
		if launched >= maxPerPass {
			break
		}
		actorName, actorEmail, origin, ok := app.workflowTickerEligible(entry, now)
		if !ok {
			continue
		}
		if _, err := app.launchApprovedProposal(entry, actorName, actorEmail, origin); err != nil {
			log.Errorf("Workflow ticker failed to launch approved proposal %s: %v", entry.ID, err)
			continue
		}
		launched++
	}
	recordWorkflowTickerPass(now, launched)
	return launched
}

// workflowTickerEligible decides whether a proposal is a human-approved,
// non-disruptive launch the ticker may run, and returns the launch attribution
// (actorName/actorEmail) plus the 068-routed origin when it is. It never
// launches: a grill-mode proposal (grill stays manual), an unknown/empty mode,
// or an external-write-phrase query (which parks at the codex approval gate
// anyway — this is defense in depth).
func (app *kanbanBoardApp) workflowTickerEligible(entry meetingMemoryEntry, now time.Time) (string, string, map[string]string, bool) {
	mode := normalizeAgentThreadMode(entry.Metadata["mode"])
	switch mode {
	case "research", "design", "artifacts", "workflow":
	default:
		return "", "", nil, false
	}
	if codexJobAuthorityForThread(scoutAgentThread{Mode: mode, Query: entry.Metadata["query"]}) == codexJobAuthorityExternalWrite {
		return "", "", nil, false
	}

	actorName := ""
	actorEmail := ""
	switch {
	case entry.Metadata["status"] == codexProposalStatusConfirmed && strings.TrimSpace(entry.Metadata["threadId"]) == "":
		// Case A: a confirmed launch that never stamped a threadId. Skip until
		// the confirm grace elapses so an in-flight confirm is never re-launched;
		// a missing/unparseable resolvedAt is treated as not-yet-eligible (safe).
		resolvedAt, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(entry.Metadata["resolvedAt"]))
		if err != nil || now.Sub(resolvedAt) < workflowTickerConfirmGrace {
			return "", "", nil, false
		}
		// The empty threadId is only a MISSED LAUNCH (the genuine crash gap) when
		// launchApprovedProposal never reached its confirm-signal write. If a
		// proposal_confirmed signal already exists for this proposal, the launch
		// DID run and only the follow-up threadId stamp write failed non-fatally
		// — relaunching would double the agent thread and re-settle the
		// notification, so treat the empty threadId as lost linkage, not a gap.
		if app.proposalConfirmSignalRecorded(entry.ID) {
			return "", "", nil, false
		}
		actorName = firstNonEmptyString(strings.TrimSpace(entry.Metadata["confirmedBy"]), workflowTickerAgentName)
		actorEmail = strings.TrimSpace(entry.Metadata["confirmedByEmail"])
	case entry.Metadata["status"] == codexProposalStatusProposed && proposalLane(entry) == codexProposalLaneAutoRun:
		// Case B: 069's auto_run lane WITH a recorded standing approval. Empty
		// laneApprovedBy means no human ever approved it — never launch.
		approvedBy := strings.TrimSpace(entry.Metadata["laneApprovedBy"])
		if approvedBy == "" {
			return "", "", nil, false
		}
		actorName = workflowTickerAgentName + " · standing approval: " + approvedBy
		if strings.Contains(approvedBy, "@") {
			actorEmail = normalizeAccountEmail(approvedBy)
		}
	default:
		return "", "", nil, false
	}

	origin := app.routeProposalOrigin(entry, actorName, actorEmail)
	return actorName, actorEmail, origin, true
}

// proposalConfirmSignalRecorded reports whether launchApprovedProposal has
// already run a launch for this proposal, detected by the proposal_confirmed
// signal it records right after a successful launchAgentThreadWithOrigin. This
// is the discriminator between the two shapes of a confirmed-but-unstamped
// proposal: the genuine Case-A crash gap (status persisted as confirmed, then a
// crash BEFORE the launch ever ran, so no confirm signal exists) versus a
// non-fatal threadId-stamp failure (the launch DID run and recorded its confirm
// signal, but the follow-up stamp write failed, leaving threadId empty). Only
// the former is a missed launch the ticker may recover; the latter must never
// double-launch. Scanned only inside the already-narrow Case-A branch, so the
// signal walk stays rare.
func (app *kanbanBoardApp) proposalConfirmSignalRecorded(proposalID string) bool {
	proposalID = strings.TrimSpace(proposalID)
	if proposalID == "" || app == nil || app.memory == nil {
		return false
	}
	for _, entry := range app.memory.entriesOfKind(meetingMemoryKindSignal, 0) {
		record, ok := decodeSignalEntry(entry)
		if !ok || record.Event != signalEventProposalConfirmed {
			continue
		}
		if strings.TrimSpace(record.Payload["proposalId"]) == proposalID {
			return true
		}
	}
	return false
}

// routeProposalOrigin implements the merged 068 delivery rule for a ticker
// launch. Precedence: (a) the originating public channel the proposal captured
// (originThreadId), else (b) the best-match public channel by token-set Jaccard
// over title+query, else (c) #general created under the approver, else (d)
// creator-notification-only when no owner email can mint #general — never an
// owner-less channel. The routeNote disclosure rides the origin map so the
// launch card (and deliverArtifactToOrigin's completion card) can show WHY the
// work landed where it did.
func (app *kanbanBoardApp) routeProposalOrigin(entry meetingMemoryEntry, actorName string, actorEmail string) map[string]string {
	title := strings.TrimSpace(entry.Metadata["title"])
	query := strings.TrimSpace(entry.Metadata["query"])

	// (a) originating thread: a captured, still-public, unarchived channel wins.
	if channel, ok := app.channelForOriginThread(entry.Metadata["originThreadId"]); ok {
		return channelDeliveryOrigin(channel, "")
	}

	// (b) best-match public channel (threshold + decisive margin, or fall through).
	if channel, note, ok := app.bestMatchPublicChannel(title, query); ok {
		return channelDeliveryOrigin(channel, note)
	}

	// (c) #general under a resolvable owner.
	if ownerEmail := normalizeAccountEmail(actorEmail); ownerEmail != "" {
		if channel, err := app.resolveOrCreatePublicChannel(ownerEmail, actorName, "general"); err == nil {
			return channelDeliveryOrigin(channel, "routed to #general — no originating thread")
		}
	}

	// (d) no resolvable owner → creator-notification-only (never mint an
	// owner-less channel). Some relaunches deliver via the bell alone.
	log.Errorf("Workflow ticker: no routable channel for proposal %s; delivering via notification only", entry.ID)
	return map[string]string{"originKind": agentThreadOriginTool}
}

func channelDeliveryOrigin(channel scoutChatThreadRecord, routeNote string) map[string]string {
	origin := map[string]string{
		"originKind": agentThreadOriginChannel,
		"originId":   channel.ID,
	}
	if strings.TrimSpace(routeNote) != "" {
		origin["routeNote"] = routeNote
	}
	return origin
}

// channelForOriginThread resolves a channel id to a still-public, unarchived
// channel — the same guard deliverArtifactToOrigin enforces before writing to a
// channel. A private/archived/unknown id resolves to no channel.
func (app *kanbanBoardApp) channelForOriginThread(channelID string) (scoutChatThreadRecord, bool) {
	channelID = strings.TrimSpace(channelID)
	if channelID == "" || app == nil || app.memory == nil {
		return scoutChatThreadRecord{}, false
	}
	entry, ok := app.memory.entryByKindAndID(meetingMemoryKindScoutChat, channelID)
	if !ok {
		return scoutChatThreadRecord{}, false
	}
	thread, ok := decodeScoutChatThreadEntry(entry)
	if !ok || thread.ArchivedAt != "" || scoutChatThreadVisibility(thread) != scoutChatVisibilityPublic {
		return scoutChatThreadRecord{}, false
	}
	return thread, true
}

// bestMatchPublicChannel finds the public channel whose title best matches the
// proposal's title+query, reusing the board-card matcher's Jaccard threshold and
// decisive-margin discipline (linkage.go) so a weak or ambiguous best match
// falls through to #general instead of posting a completion card in an
// unrelated channel. Returns the channel, a routeNote disclosure, and whether a
// confident match was found.
func (app *kanbanBoardApp) bestMatchPublicChannel(title string, query string) (scoutChatThreadRecord, string, bool) {
	if app == nil || app.memory == nil {
		return scoutChatThreadRecord{}, "", false
	}
	tokens := linkageMatchTokens(strings.TrimSpace(title + " " + query))
	if len(tokens) == 0 {
		return scoutChatThreadRecord{}, "", false
	}

	best := scoutChatThreadRecord{}
	bestScore := 0.0
	secondScore := 0.0
	for _, entry := range app.memory.snapshot(0) {
		channel, ok := decodeScoutChatThreadEntry(entry)
		if !ok || channel.ArchivedAt != "" || scoutChatThreadVisibility(channel) != scoutChatVisibilityPublic {
			continue
		}
		if strings.TrimSpace(channel.Title) == "" {
			continue
		}
		score := tokenSetJaccard(tokens, linkageMatchTokens(channel.Title))
		if score < linkageFuzzyMatchThreshold {
			continue
		}
		if score > bestScore {
			secondScore = bestScore
			bestScore = score
			best = channel
		} else if score > secondScore {
			secondScore = score
		}
	}
	if bestScore < linkageFuzzyMatchThreshold {
		return scoutChatThreadRecord{}, "", false
	}
	if secondScore > 0 && bestScore-secondScore < linkageAmbiguityMargin {
		return scoutChatThreadRecord{}, "", false
	}
	return best, "best match: #" + best.Title, true
}

// postChannelLaunchCard posts a "running" thread card into a channel at
// ticker-launch time, carrying the launched agent thread's id. The terminal
// seam (updateScoutChatThreadRefs) flips this same ref to complete, and
// deliverArtifactToOrigin's scoutChatThreadHasAgentRef check then suppresses a
// duplicate completion card. Best-effort: a failure only loses the running card
// (the completion still delivers). The routeNote rides in the card text so the
// channel discloses WHY the work landed there.
func (app *kanbanBoardApp) postChannelLaunchCard(channelID string, thread scoutAgentThread, routeNote string) {
	channel, ok := app.channelForOriginThread(channelID)
	if !ok {
		return
	}
	title := firstNonEmptyString(strings.TrimSpace(thread.Artifact.Metadata["title"]), thread.Query)
	text := fmt.Sprintf("launching %s — %s", assistantToolLabel(thread.Mode), title)
	if note := strings.TrimSpace(routeNote); note != "" {
		text += " · " + note
	}
	message := scoutChatMessageRecord{
		ID:        fmt.Sprintf("scout-chat-message-%d", time.Now().UTC().UnixNano()),
		Kind:      "thread",
		Role:      "scout",
		Text:      text,
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Thread: &scoutChatThreadRef{
			ID:         thread.ID,
			Mode:       thread.Mode,
			Query:      firstNonEmptyString(thread.Query, thread.Artifact.Metadata["threadQuery"]),
			Status:     "running",
			ArtifactID: thread.Artifact.ID,
		},
	}
	if _, err := app.commitScoutChatThreadMessages(channel.OwnerEmail, channel.ID, message); err != nil {
		log.Errorf("Workflow ticker failed to post launch card to channel %s: %v", channel.ID, err)
	}
}
