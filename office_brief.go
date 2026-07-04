package main

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// office_brief.go — Wave 8's daily-value floor: the Morning Brief, Portfolio
// Health, and the approval round-trip loop.
//
// Everything here is COMPOSITION over existing snapshots — no new ambient
// worker, no new datastore. The Brief reads pending approvals, recent finished
// artifacts, board deltas, unread channel activity, and the quarantine tray
// (Wave 7) straight off memory + the notification store; Portfolio Health is
// pure aggregation over venturePackagePayloads. The round-trip loop rides the
// Wave-3 push channel (proposal events) + targeted notifications so a
// non-admin's external-write request never vanishes into the admin's queue.

const (
	// briefStaleDays is the freshness floor: a package untouched this long
	// surfaces first in Portfolio Health with a nudge line (design §9,
	// domain §6.2). Ten days matches the design's "surfaced first" threshold.
	briefStaleDays = 10
	// briefCompletedLimit / briefBoardDeltaLimit / briefUnreadLimit cap each
	// Brief section so the card stays skimmable (the four-line discipline).
	briefCompletedLimit  = 8
	briefBoardDeltaLimit = 6
	briefUnreadLimit     = 6
)

// artifactAwaitingApproval reports whether an artifact is parked at its
// external-write ship gate waiting on the admin. Mirrors the guard in
// approveCodexArtifactExternalWrite so the Brief and the gate agree on state.
func artifactAwaitingApproval(metadata map[string]string) bool {
	return strings.TrimSpace(metadata["reviewGate"]) == "approval_required" ||
		strings.TrimSpace(metadata["threadStatus"]) == codexJobStatusApprovalRequired
}

// approvalRequesterEmail resolves who asked for the work behind an artifact,
// normalized to an account email. requestedBy/createdBy may carry either an
// email or a display name (the goal engine stamps requestedBy=createdBy at
// launch); a name resolves through the roster, an unknown value yields "".
func approvalRequesterEmail(metadata map[string]string) string {
	raw := firstNonEmptyString(
		strings.TrimSpace(metadata["requestedByEmail"]),
		strings.TrimSpace(metadata["requestedBy"]),
		strings.TrimSpace(metadata["createdBy"]),
	)
	if raw == "" {
		return ""
	}
	if strings.Contains(raw, "@") {
		return normalizeAccountEmail(raw)
	}
	return participantEmail(raw)
}

// approvalAdminDisplayName is the "queued for AJ's sign-off" name — the admin's
// first name, falling back to a stable label if the roster is unavailable.
func approvalAdminDisplayName() string {
	name := strings.TrimSpace(participantNameForEmail(artifactLibraryAdminEmail))
	if name == "" {
		return "the admin"
	}
	if first := strings.Fields(name); len(first) > 0 {
		return first[0]
	}
	return name
}

// pendingApprovalEntries gathers artifacts parked at the external-write gate.
// The admin sees every waiting item with act=true (one-tap approve/reject);
// a non-admin sees only the items they requested, as read-only waiting cards
// ("queued for AJ's sign-off") — their request is visible and owned, never
// swallowed into someone else's queue.
func (app *kanbanBoardApp) pendingApprovalEntries(viewerEmail string, isAdmin bool) []map[string]any {
	entries := []map[string]any{}
	if app == nil || app.memory == nil {
		return entries
	}
	viewerEmail = normalizeAccountEmail(viewerEmail)
	adminName := approvalAdminDisplayName()
	artifacts := app.osArtifactsSnapshot(0)
	// newest-first: the freshest request sits at the top of the queue.
	for i := len(artifacts) - 1; i >= 0; i-- {
		artifact := artifacts[i]
		if !artifactAwaitingApproval(artifact.Metadata) {
			continue
		}
		requesterEmail := approvalRequesterEmail(artifact.Metadata)
		mine := requesterEmail != "" && requesterEmail == viewerEmail
		if !isAdmin && !mine {
			continue
		}
		requesterName := firstNonEmptyString(
			participantNameForEmail(requesterEmail),
			strings.TrimSpace(artifact.Metadata["requestedBy"]),
			strings.TrimSpace(artifact.Metadata["createdBy"]),
			"someone",
		)
		entry := map[string]any{
			"id":            artifact.ID,
			"title":         firstNonEmptyString(strings.TrimSpace(artifact.Metadata["title"]), assistantToolLabel(artifact.Kind)+" artifact"),
			"mode":          firstNonEmptyString(strings.TrimSpace(artifact.Metadata["mode"]), artifact.Kind),
			"requestedBy":   requesterName,
			"requestedMine": mine,
			"origin":        firstNonEmptyString(strings.TrimSpace(artifact.Metadata["originKind"]), "artifacts"),
			"createdAt":     artifact.CreatedAt.UTC().Format(time.RFC3339Nano),
			// state drives the requester's waiting card copy; the admin card
			// shows the same "waiting" with act=true.
			"state":     "waiting",
			"waitingOn": adminName,
			"canAct":    isAdmin,
		}
		if pkg := strings.TrimSpace(artifact.Metadata["packageId"]); pkg != "" {
			entry["packageId"] = pkg
		}
		entries = append(entries, entry)
	}
	return entries
}

// recentCompletedArtifacts is the "what finished overnight" strip: terminal
// artifacts (completed/published/verified goals + drafts), newest first,
// excluding anything still parked at a gate. Titles only.
func (app *kanbanBoardApp) recentCompletedArtifacts(limit int) []map[string]any {
	items := []map[string]any{}
	if app == nil || app.memory == nil {
		return items
	}
	artifacts := app.osArtifactsSnapshot(0)
	for i := len(artifacts) - 1; i >= 0; i-- {
		artifact := artifacts[i]
		if artifactAwaitingApproval(artifact.Metadata) {
			continue
		}
		if !osArtifactIsTerminal(artifact.Metadata) {
			continue
		}
		item := map[string]any{
			"id":    artifact.ID,
			"title": firstNonEmptyString(strings.TrimSpace(artifact.Metadata["title"]), assistantToolLabel(artifact.Kind)+" artifact"),
			"mode":  firstNonEmptyString(strings.TrimSpace(artifact.Metadata["mode"]), artifact.Kind),
			"at":    firstNonEmptyString(strings.TrimSpace(artifact.Metadata["updatedAt"]), artifact.CreatedAt.UTC().Format(time.RFC3339Nano)),
		}
		if pkg := strings.TrimSpace(artifact.Metadata["packageId"]); pkg != "" {
			item["packageId"] = pkg
		}
		if published := artifactIsPublished(artifact); published {
			item["published"] = true
		}
		items = append(items, item)
		if limit > 0 && len(items) >= limit {
			break
		}
	}
	return items
}

// recentBoardDeltas summarizes what moved on the board: the recent board-update
// digests (the board worker's "what changed" summaries) newest first, plus the
// count of Scout-drafted cards still awaiting a human accept/dismiss.
func (app *kanbanBoardApp) recentBoardDeltas(limit int) map[string]any {
	deltas := []map[string]any{}
	draftCount := 0
	if app == nil || app.memory == nil {
		return map[string]any{"items": deltas, "draftCount": draftCount}
	}
	updates := app.memory.entriesOfKind(meetingMemoryKindBoardUpdate, limit)
	for i := len(updates) - 1; i >= 0; i-- {
		entry := updates[i]
		summary := compactAssistantLine(entry.Text)
		if summary == "" {
			continue
		}
		deltas = append(deltas, map[string]any{
			"id":      entry.ID,
			"summary": trimForStorage(summary, 160),
			"at":      entry.CreatedAt.UTC().Format(time.RFC3339Nano),
		})
	}
	for _, card := range app.snapshotState().Cards {
		if card.Draft {
			draftCount++
		}
	}
	return map[string]any{"items": deltas, "draftCount": draftCount}
}

// unreadChannelActivity is the viewer's unread chat/channel notifications —
// "#dealflow has four unread" in the quiet-Tuesday journey. Targeted per viewer.
func (app *kanbanBoardApp) unreadChannelActivity(viewerEmail string, limit int) map[string]any {
	items := []map[string]any{}
	if app == nil {
		return map[string]any{"count": 0, "items": items}
	}
	for _, record := range app.notificationsForUserFiltered(normalizeAccountEmail(viewerEmail), 40, true) {
		if asString(record["kind"]) != notificationKindChat {
			continue
		}
		item := map[string]any{
			"id":   asString(record["id"]),
			"text": asString(record["text"]),
			"at":   asString(record["createdAt"]),
		}
		if threadID := asString(record["threadId"]); threadID != "" {
			item["threadId"] = threadID
		}
		if tool := asString(record["tool"]); tool != "" {
			item["tool"] = tool
		}
		items = append(items, item)
		if limit > 0 && len(items) >= limit {
			break
		}
	}
	return map[string]any{"count": len(items), "items": items}
}

// morningBriefPayload composes the whole Brief for one viewer. Admin-ness gates
// the approval affordances and the quarantine tray's delete button.
func (app *kanbanBoardApp) morningBriefPayload(viewer *userAccount) map[string]any {
	isAdmin := isArtifactApprovalAdmin(viewer)
	viewerEmail := ""
	greeting := "there"
	if viewer != nil {
		viewerEmail = normalizeAccountEmail(viewer.Email)
		if name := strings.Fields(strings.TrimSpace(viewer.Name)); len(name) > 0 {
			greeting = name[0]
		}
	}
	approvals := app.pendingApprovalEntries(viewerEmail, isAdmin)
	completed := app.recentCompletedArtifacts(briefCompletedLimit)
	board := app.recentBoardDeltas(briefBoardDeltaLimit)
	unread := app.unreadChannelActivity(viewerEmail, briefUnreadLimit)
	quarantine := app.quarantineListPayloads()

	return map[string]any{
		"greeting": greeting,
		"isAdmin":  isAdmin,
		"approvals": map[string]any{
			"items": approvals,
			"count": len(approvals),
		},
		"completed": map[string]any{
			"items": completed,
			"count": len(completed),
		},
		"board":          board,
		"unreadChannels": unread,
		"quarantine": map[string]any{
			"entries":   quarantine,
			"count":     len(quarantine),
			"canDelete": isAdmin,
		},
	}
}

// --- Portfolio Health -------------------------------------------------------

// latestGrillReadiness pulls the most recent grill artifact's stamped readiness
// score and trend delta off a package (newest attached artifact wins).
func (app *kanbanBoardApp) latestGrillReadiness(record venturePackageRecord) (score string, delta string) {
	for i := len(record.ArtifactIDs) - 1; i >= 0; i-- {
		artifact, ok := app.osArtifactByID(record.ArtifactIDs[i])
		if !ok {
			continue
		}
		if strings.ToLower(strings.TrimSpace(artifact.Metadata["mode"])) != "grill" {
			continue
		}
		if s := strings.TrimSpace(artifact.Metadata["readinessScore"]); s != "" {
			return s, strings.TrimSpace(artifact.Metadata["readinessDelta"])
		}
		if match := packageGrillScoreRE.FindStringSubmatch(artifact.Text); len(match) > 1 {
			return match[1], ""
		}
	}
	return "", ""
}

// packageFreshnessDays is whole days since a package last moved; -1 when the
// timestamp is missing or unparseable (rendered as "unknown" client-side).
func packageFreshnessDays(updatedAt string, now time.Time) int {
	parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(updatedAt))
	if err != nil {
		parsed, err = time.Parse(time.RFC3339, strings.TrimSpace(updatedAt))
		if err != nil {
			return -1
		}
	}
	days := int(now.UTC().Sub(parsed.UTC()).Hours() / 24)
	if days < 0 {
		return 0
	}
	return days
}

// portfolioHealthPayload is the whole book on one screen: every package's
// stage, readiness dial + trend, freshness, and open gaps, stale-first with a
// nudge line. Pure aggregation over venturePackagePayloads.
func (app *kanbanBoardApp) portfolioHealthPayload(now time.Time) []map[string]any {
	packages := []map[string]any{}
	if app == nil || app.memory == nil {
		return packages
	}
	for _, record := range app.venturePackagesSnapshot() {
		payload := app.packagePayload(record)
		score, delta := app.latestGrillReadiness(record)
		freshness := packageFreshnessDays(record.UpdatedAt, now)
		stale := freshness >= briefStaleDays
		gaps := []string{}
		if stats, ok := payload["stats"].(map[string]any); ok {
			if raw, ok := stats["gaps"].([]string); ok {
				gaps = raw
			}
		}
		nudge := portfolioNudgeLine(record.Name, freshness, stale, gaps)
		packages = append(packages, map[string]any{
			"id":             record.ID,
			"name":           record.Name,
			"stage":          record.Stage,
			"stageIndex":     packageStageIndex(record.Stage),
			"stages":         packageStages,
			"readinessScore": score,
			"readinessDelta": delta,
			"freshnessDays":  freshness,
			"stale":          stale,
			"gaps":           gaps,
			"updatedAt":      record.UpdatedAt,
			"channelId":      record.ChannelID,
			"nudge":          nudge,
		})
	}
	// Stale-first, then oldest-moved-first within each band, then by name so the
	// order is stable across refreshes.
	sort.SliceStable(packages, func(i, j int) bool {
		si, _ := packages[i]["stale"].(bool)
		sj, _ := packages[j]["stale"].(bool)
		if si != sj {
			return si
		}
		fi, _ := packages[i]["freshnessDays"].(int)
		fj, _ := packages[j]["freshnessDays"].(int)
		if fi != fj {
			return fi > fj
		}
		return asString(packages[i]["name"]) < asString(packages[j]["name"])
	})
	return packages
}

// portfolioNudgeLine is the one-line "why this needs attention" surfaced on a
// stale or gap-bearing package; "" when the package is healthy.
func portfolioNudgeLine(name string, freshnessDays int, stale bool, gaps []string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "This package"
	}
	if stale && len(gaps) > 0 {
		return name + " hasn't moved in " + strconv.Itoa(freshnessDays) + " days — " + strings.Join(gaps, ", ") + " still open."
	}
	if stale {
		return name + " hasn't moved in " + strconv.Itoa(freshnessDays) + " days."
	}
	if len(gaps) > 0 {
		return name + " still has " + strings.Join(gaps, ", ") + " open."
	}
	return ""
}

// portfolioHealthSpoken renders the portfolio_health voice tool's spoken-ready
// summary: leads with the stale nudges, then a one-line-per-package readout.
func (app *kanbanBoardApp) portfolioHealthSpoken(now time.Time) string {
	packages := app.portfolioHealthPayload(now)
	if len(packages) == 0 {
		return "There are no venture packages in the book yet."
	}
	lines := []string{}
	lines = append(lines, "You have "+strconv.Itoa(len(packages))+" packages in the book.")
	for _, pkg := range packages {
		name := asString(pkg["name"])
		stage := asString(pkg["stage"])
		line := name + " is at " + firstNonEmptyString(stage, "an early stage")
		if score := asString(pkg["readinessScore"]); score != "" {
			line += ", readiness " + score
			if delta := asString(pkg["readinessDelta"]); delta != "" {
				line += " (" + delta + ")"
			}
		}
		if days, ok := pkg["freshnessDays"].(int); ok && days >= 0 {
			if days == 0 {
				line += ", moved today"
			} else if days == 1 {
				line += ", last moved yesterday"
			} else {
				line += ", last moved " + strconv.Itoa(days) + " days ago"
			}
		}
		if nudge := asString(pkg["nudge"]); nudge != "" {
			line += ". " + nudge
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, " ")
}

// portfolioHealthTool is the private-voice portfolio_health dispatch — "Scout,
// how's the portfolio?". Returns a spoken-ready summary plus the structured
// payload; no-ops gracefully (never errors) so keyless voice stays usable.
func (app *kanbanBoardApp) portfolioHealthTool() (map[string]any, bool, error) {
	now := time.Now()
	return map[string]any{
		"ok":        true,
		"spoken":    app.portfolioHealthSpoken(now),
		"portfolio": app.portfolioHealthPayload(now),
	}, false, nil
}

// --- approval round-trip loop -----------------------------------------------

// recordApprovalOutcome closes the loop after the admin acts on an external-
// write gate: it fans a proposal event on the push channel (so the requester's
// origin surface flips its waiting card) and notifies the requester directly
// with the outcome — approved, or rejected with the admin's one-line reason.
// The requester "owns the outcome": their request never vanishes into the
// admin's queue. Called from artifactRunnerActionHandler after a successful
// approve/reject; a missing/self requester is a silent no-op.
func (app *kanbanBoardApp) recordApprovalOutcome(artifact meetingMemoryEntry, action string, reason string, approverName string) {
	if app == nil || strings.TrimSpace(artifact.ID) == "" {
		return
	}
	title := firstNonEmptyString(strings.TrimSpace(artifact.Metadata["title"]), assistantToolLabel(artifact.Kind)+" artifact")
	origin := firstNonEmptyString(strings.TrimSpace(artifact.Metadata["originKind"]), "artifacts")
	approved := strings.EqualFold(strings.TrimSpace(action), "approve")

	// Push-channel proposal event: title-only, so the requester's surface and
	// the bell learn the transition and re-fetch the card by ref.
	broadcastOSEvent(osEvent{
		Kind:          osEventProposal,
		Ref:           artifact.ID,
		Title:         title,
		OriginSurface: origin,
		Actor:         canonicalRoomActorName(approverName),
	})

	requesterEmail := approvalRequesterEmail(artifact.Metadata)
	if requesterEmail == "" || requesterEmail == normalizeAccountEmail(participantEmail(approverName)) {
		// No resolvable requester, or the admin approved their own request —
		// the push event already covered the surface refresh.
		return
	}

	var text string
	if approved {
		text = "Approved · sent: " + title
	} else {
		reason = strings.TrimSpace(reason)
		if reason == "" {
			reason = "no reason given"
		}
		text = "Rejected: " + title + " — " + reason
	}
	if _, err := app.createNotification(requesterEmail, notificationKindAgent, text, origin, artifact.ID, "", false); err != nil {
		log.Errorf("Failed to notify %s of approval outcome for %s: %v", requesterEmail, artifact.ID, err)
	}
}

// approvalGateNotified dedups the admin's gate-entry notification so an
// artifact that re-writes while parked at the gate (bookkeeping updates) pings
// the admin exactly once per time it enters the gate. Cleared when the artifact
// leaves approval_required so a re-triggered gate notifies again.
var (
	approvalGateNotifiedMu sync.Mutex
	approvalGateNotified   = map[string]bool{}
)

// maybeNotifyApprovalGate pings the admin the moment an artifact parks at the
// external-write gate — the admin's half of the round-trip ("AJ gets an
// approval notification with one-tap approve/reject from the bell", design §9).
// Targeted to the admin only, so no non-admin bell ever shows the affordance;
// the tool="approval" + artifactId markers let the bell render the one-tap
// buttons and deep-link to the Brief. Rides the emitOSArtifactEvent transition
// seam, so it sees every path (goal engine + codex fold) with no new hook.
func (app *kanbanBoardApp) maybeNotifyApprovalGate(entry meetingMemoryEntry) {
	if app == nil || entry.Kind != meetingMemoryKindOSArtifact {
		return
	}
	id := strings.TrimSpace(entry.ID)
	if id == "" {
		return
	}
	waiting := artifactAwaitingApproval(entry.Metadata)

	approvalGateNotifiedMu.Lock()
	already := approvalGateNotified[id]
	if !waiting {
		// left the gate (approved/rejected/verified): reset so a future
		// re-trigger notifies again, and bound the map.
		delete(approvalGateNotified, id)
		approvalGateNotifiedMu.Unlock()
		return
	}
	if already {
		approvalGateNotifiedMu.Unlock()
		return
	}
	approvalGateNotified[id] = true
	if len(approvalGateNotified) > 2048 {
		approvalGateNotified = map[string]bool{id: true}
	}
	approvalGateNotifiedMu.Unlock()

	title := firstNonEmptyString(strings.TrimSpace(entry.Metadata["title"]), assistantToolLabel(entry.Kind)+" artifact")
	text := "Approval needed: " + title
	if requesterEmail := approvalRequesterEmail(entry.Metadata); requesterEmail != "" {
		if name := strings.TrimSpace(participantNameForEmail(requesterEmail)); name != "" {
			text += " — from " + name
		}
	}
	if _, err := app.createNotification(artifactLibraryAdminEmail, notificationKindTask, text, "approval", id, "", false); err != nil {
		log.Errorf("Failed to notify admin of approval gate for %s: %v", id, err)
	}
}

// --- HTTP handlers ----------------------------------------------------------

func assistantBriefHandler(w http.ResponseWriter, r *http.Request) {
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
		writeAuthError(w, http.StatusServiceUnavailable, "the brief is unavailable")
		return
	}
	writeAuthJSON(w, http.StatusOK, map[string]any{
		"ok":    true,
		"brief": kanbanApp.morningBriefPayload(user),
	})
}

func assistantPortfolioHandler(w http.ResponseWriter, r *http.Request) {
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
		writeAuthError(w, http.StatusServiceUnavailable, "portfolio health is unavailable")
		return
	}
	writeAuthJSON(w, http.StatusOK, map[string]any{
		"ok":        true,
		"portfolio": kanbanApp.portfolioHealthPayload(time.Now()),
	})
}
