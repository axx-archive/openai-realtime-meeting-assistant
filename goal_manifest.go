package main

// goal_manifest.go — the package manifest card (redesign sheet s05 + §2c
// states): the moment the product hands over the goods. When a
// packaging_studio ship_approval resolves, the run posts ONE Kind:"manifest"
// message into its origin thread — the shipped card (or the held variant) with
// every deliverable, its actions, the run's provenance line, and every
// disclosed skip. The manifest is DATA persisted on the message (the
// proposal/choices law): the client renders it, reloads render the same card,
// and no action here ever launches anything.

import (
	"fmt"
	"strings"
	"time"
)

// scoutChatMessageKindManifest marks a persisted package manifest among the
// thread messages (the Proposal/Choices pattern in scout_chat_threads.go).
const scoutChatMessageKindManifest = "manifest"

// The manifest's status vocabulary (sheet §2c): shipped is the green success
// voice; held is the muted card — actions quieted, nothing leaves the office.
const (
	manifestStatusShipped = "shipped"
	manifestStatusHeld    = "held"
)

// scoutChatManifestDeliverable is one row of the manifest: the mono type
// badge, the title + format facts, and which actions the row carries. Open is
// always available (every row names its artifact); present is deck-only; the
// pdf download exists only when a rendered pdf asset landed on the artifact.
type scoutChatManifestDeliverable struct {
	ArtifactID string `json:"artifactId"`
	Title      string `json:"title"`
	Badge      string `json:"badge"`           // deck | paper | doc
	Facts      string `json:"facts,omitempty"` // "html · 8 pages · presenter mode"
	Present    bool   `json:"present,omitempty"`
	PdfRef     string `json:"pdfRef,omitempty"` // blob ref of the newest pdf asset
	PdfName    string `json:"pdfName,omitempty"`
}

// scoutChatManifestProvenance is the right-aligned mono provenance line:
// "gate 9.5 · 3 assumed · 4 decisions · 2h 25m".
type scoutChatManifestProvenance struct {
	GateScore   float64 `json:"gateScore,omitempty"`
	GateOutcome string  `json:"gateOutcome,omitempty"`
	Assumed     int     `json:"assumed"`
	Decisions   int     `json:"decisions"`
	WallClock   string  `json:"wallClock,omitempty"`
}

// scoutChatManifest is the persisted card. ShareArtifactID names the
// deliverable the footer's "share link" mints for — present ONLY when that
// artifact is share-eligible (a held package's share links stay dark, and the
// mint endpoint stays the enforcement point either way).
type scoutChatManifest struct {
	GoalID             string                         `json:"goalId"`
	Status             string                         `json:"status"` // shipped | held
	Title              string                         `json:"title"`
	Subline            string                         `json:"subline,omitempty"`
	HeldBy             string                         `json:"heldBy,omitempty"`
	Provenance         scoutChatManifestProvenance    `json:"provenance"`
	Deliverables       []scoutChatManifestDeliverable `json:"deliverables"`
	Skips              []string                       `json:"skips,omitempty"`
	PackageID          string                         `json:"packageId,omitempty"`
	AttachedTo         string                         `json:"attachedTo,omitempty"` // the package's display name
	FindingsArtifactID string                         `json:"findingsArtifactId,omitempty"`
	ShareArtifactID    string                         `json:"shareArtifactId,omitempty"`
}

// recordStudioShipResolution is the engine's seam (proceed + hold paths of a
// packaging_studio ship_approval): a real proceed approves the filed
// deliverables for sharing — the ship approval IS the human sign-off on
// exactly these artifacts leaving the building — then either resolution posts
// the manifest card into the origin thread. approved=false (a hold, or the
// disclosed budget-spent send-back fallback where the founder did NOT approve)
// ships the card with its share affordance dark. Silent no-op for any other
// process, stage, or a run whose compile record is missing.
func (app *kanbanBoardApp) recordStudioShipResolution(plan *goalPlan, parentID string, stageID string, status string, actor string, approved bool) {
	if app == nil || plan == nil || plan.ProcessID != packagingStudioProcessID || stageID != "ship_approval" {
		return
	}
	if status == manifestStatusShipped && approved {
		app.approveStudioShipDeliverables(studioShipArtifactIDs(app, plan), actor)
	}
	manifest, ok := app.composeStudioShipManifest(plan, parentID, status, actor)
	if !ok {
		return
	}
	line := "manifest filed — " + manifestCountLine(len(manifest.Deliverables))
	if manifest.AttachedTo != "" {
		line += " attached to " + manifest.AttachedTo
	}
	if status == manifestStatusHeld {
		line = "package held — release requires " + firstNonEmptyString(strings.TrimSpace(actor), "admin")
	}
	app.postGoalOriginMessage(parentID, scoutChatMessageRecord{
		Kind:     scoutChatMessageKindManifest,
		Role:     "scout",
		Text:     line,
		Manifest: &manifest,
	})
}

// composeStudioShipManifest assembles the persisted card from the run's OWN
// records: the ship_compile stage's filed deliverables (the shipArtifactIds
// stamp, the same seam the slide jury reads), the compile record's disclosed
// render skips, the slide jury's disclosure, the plan's gate/report telemetry,
// and the venture package binder.
func (app *kanbanBoardApp) composeStudioShipManifest(plan *goalPlan, parentID string, status string, actor string) (scoutChatManifest, bool) {
	compileSt := plan.subtaskByID("ship_compile")
	if compileSt == nil {
		return scoutChatManifest{}, false
	}
	record, ok := app.osArtifactByID(compileSt.ArtifactID)
	if !ok {
		return scoutChatManifest{}, false
	}

	manifest := scoutChatManifest{
		GoalID:    parentID,
		Status:    status,
		PackageID: plan.PackageID,
	}
	if status == manifestStatusHeld {
		manifest.HeldBy = firstNonEmptyString(strings.TrimSpace(actor), "admin")
	}
	if parent, found := app.osArtifactByID(parentID); found {
		manifest.Title = firstNonEmptyString(
			strings.TrimSpace(parent.Metadata["title"]),
			compactAssistantLine(plan.Objective),
		)
		manifest.Provenance.WallClock = manifestWallClock(time.Since(parent.CreatedAt))
	}
	if plan.PackageID != "" {
		if pkg, found := app.venturePackageByID(plan.PackageID); found {
			manifest.AttachedTo = pkg.Name
		}
	}

	for _, id := range studioShipArtifactIDs(app, plan) {
		artifact, found := app.osArtifactByID(id)
		if !found {
			continue
		}
		deliverable := scoutChatManifestDeliverable{
			ArtifactID: artifact.ID,
			Title:      firstNonEmptyString(strings.TrimSpace(artifact.Metadata["title"]), artifact.ID),
			Badge:      manifestDeliverableBadge(artifact),
			Facts:      manifestDeliverableFacts(artifact),
			Present:    artifactType(artifact) == artifactTypeHTMLDeck,
		}
		if asset, hasPdf := firstArtifactAssetOfKind(artifact, "pdf"); hasPdf {
			deliverable.PdfRef = asset.Ref
			deliverable.PdfName = firstNonEmptyString(asset.Name, deliverable.Title+".pdf")
		}
		if artifact.Metadata["artifactContract"] == packagingStudioFindingsContract {
			manifest.FindingsArtifactID = artifact.ID
		}
		manifest.Deliverables = append(manifest.Deliverables, deliverable)
	}
	if len(manifest.Deliverables) == 0 {
		return scoutChatManifest{}, false
	}

	manifest.Skips = studioShipManifestSkips(app, plan, record)
	manifest.Provenance.Assumed = plan.Report.AssumedClaimCount
	manifest.Provenance.GateOutcome = plan.Report.GateOutcome
	for index := range plan.Subtasks {
		st := &plan.Subtasks[index]
		if st.Role == processRoleGate && st.Review != nil && st.Review.Score > 0 {
			manifest.Provenance.GateScore = st.Review.Score
		}
		if st.Role == processRoleHumanCheckpoint && st.Status == subtaskComplete {
			manifest.Provenance.Decisions++
		}
	}

	// The footer's share affordance: only a share-ELIGIBLE deliverable earns
	// it (the shipped path just approved them; a held run's links stay dark).
	// The first deliverable is the deck — the artifact a share link hands out.
	if status == manifestStatusShipped {
		if first, found := app.osArtifactByID(manifest.Deliverables[0].ArtifactID); found && artifactShareEligible(first) {
			manifest.ShareArtifactID = first.ID
		}
	}

	manifest.Subline = manifestSubline(status, len(manifest.Deliverables), len(manifest.Skips), manifest.AttachedTo != "")
	return manifest, true
}

// approveStudioShipDeliverables records the ship approval ON the deliverables:
// status=approved (the vocabulary's explicit human-approved-for-external
// value) plus the durable human-approval stamp share_links.go keys on. This is
// what makes the manifest's share link mintable — and what a held package
// never gets.
func (app *kanbanBoardApp) approveStudioShipDeliverables(ids []string, approver string) {
	if app == nil || app.memory == nil {
		return
	}
	stamp := map[string]string{
		"status":                   artifactStatusApproved,
		artifactHumanApprovedAtKey: time.Now().UTC().Format(time.RFC3339Nano),
		artifactHumanApprovedByKey: canonicalRoomActorName(approver),
	}
	for _, id := range ids {
		if _, _, err := app.memory.updateOSArtifactMetadata(id, stamp); err != nil {
			log.Errorf("ship approval stamp on deliverable %s failed: %v", id, err)
		}
	}
}

// studioShipArtifactIDs reads the compile stage's shipArtifactIds stamp — the
// filed deliverables in send order (the studioShipArtifactsForJury seam).
func studioShipArtifactIDs(app *kanbanBoardApp, plan *goalPlan) []string {
	st := plan.subtaskByID("ship_compile")
	if st == nil {
		return nil
	}
	record, ok := app.osArtifactByID(st.ArtifactID)
	if !ok {
		return nil
	}
	var ids []string
	for _, id := range strings.Split(record.Metadata["shipArtifactIds"], ",") {
		if trimmed := strings.TrimSpace(id); trimmed != "" {
			ids = append(ids, trimmed)
		}
	}
	return ids
}

// manifestDeliverableBadge maps a deliverable onto the sheet's mono type
// badges: the html_deck is "deck", a paper-kit document is "paper" (prints
// text-native), everything else is "doc".
func manifestDeliverableBadge(artifact meetingMemoryEntry) string {
	if artifactType(artifact) == artifactTypeHTMLDeck {
		return "deck"
	}
	if artifact.Metadata["paperKit"] == "true" {
		return "paper"
	}
	return "doc"
}

// manifestDeliverableFacts builds the quiet mono format line under a title —
// "html · 8 pages · presenter mode" style, from what is actually on file.
func manifestDeliverableFacts(artifact meetingMemoryEntry) string {
	switch artifact.Metadata["artifactContract"] {
	case packagingStudioDeckContract:
		parts := []string{"html"}
		if pages := len(artifactPageImageAssets(artifact)); pages > 0 {
			parts = append(parts, fmt.Sprintf("%d pages", pages))
		}
		return strings.Join(append(parts, "presenter mode"), " · ")
	case packagingStudioTalkContract:
		if _, hasPdf := firstArtifactAssetOfKind(artifact, "pdf"); hasPdf {
			return "pdf · text-native"
		}
		return "doc · text-native"
	case packagingStudioWallContract:
		return "doc"
	case packagingStudioRigorContract:
		return "doc · diligence"
	case packagingStudioFindingsContract:
		return "doc · every verdict on the record"
	}
	if artifactType(artifact) == artifactTypeHTMLDeck {
		return "html"
	}
	return "doc"
}

// studioShipManifestSkips collects the amber disclosure bullets: every render
// skip the compile record disclosed (parsed from its own lines — the record is
// the ship's ground truth), the slide jury's outcome, and the missing-package
// disclosure. Honesty as a design element — a skip is a bullet, never a
// silence.
func studioShipManifestSkips(app *kanbanBoardApp, plan *goalPlan, record meetingMemoryEntry) []string {
	var skips []string
	const marker = "render skipped (disclosed): "
	titleByContract := map[string]string{}
	for _, id := range studioShipArtifactIDs(app, plan) {
		if artifact, ok := app.osArtifactByID(id); ok {
			titleByContract[artifact.Metadata["artifactContract"]] = firstNonEmptyString(strings.TrimSpace(artifact.Metadata["title"]), id)
		}
	}
	for _, line := range strings.Split(record.Text, "\n") {
		cut := strings.Index(line, marker)
		if cut < 0 {
			continue
		}
		note := strings.TrimSpace(line[cut+len(marker):])
		title := ""
		if fields := strings.Fields(strings.TrimPrefix(strings.TrimSpace(line), "- ")); len(fields) > 0 {
			title = titleByContract[fields[0]]
		}
		if title != "" {
			skips = append(skips, "pdf export skipped ("+title+") — "+note)
		} else {
			skips = append(skips, "pdf export skipped — "+note)
		}
	}
	if strings.Contains(record.Text, "filed unattached (disclosed)") {
		skips = append(skips, "no venture package on this goal — the deliverables are filed unattached")
	}
	if jurySt := plan.subtaskByID("slide_jury"); jurySt != nil {
		if juryRecord, ok := app.osArtifactByID(jurySt.ArtifactID); ok {
			if strings.Contains(juryRecord.Text, "skipped (disclosed)") {
				reason := ""
				for _, line := range strings.Split(juryRecord.Text, "\n") {
					if trimmed := strings.TrimPrefix(line, "The vision jury did not run: "); trimmed != line {
						reason = strings.TrimSpace(trimmed)
						break
					}
				}
				skips = append(skips, "slide jury skipped — "+firstNonEmptyString(reason, "the jury did not run"))
			} else if strings.Contains(juryRecord.Text, "the critics saw the rendered pages") {
				skips = append(skips, "slide jury saw the rendered pages — advisory notes are on the findings record")
			}
		}
	}
	return skips
}

// manifestSubline is the quiet caption under the headline. The five-artifact
// full ship keeps the sheet's copy verbatim; a single deliverable carries no
// subline (the card scales down, same anatomy).
func manifestSubline(status string, deliverables int, skips int, attached bool) string {
	if status == manifestStatusHeld {
		return "Held before ship — artifacts stay filed, share links stay dark."
	}
	if deliverables <= 1 {
		return ""
	}
	if skips > 0 {
		return manifestCountLine(deliverables) + " filed — degraded paths disclosed below."
	}
	if deliverables == 5 && attached {
		return "Five interlocking deliverables, filed to the venture package."
	}
	if attached {
		return manifestCountLine(deliverables) + ", filed to the venture package."
	}
	return manifestCountLine(deliverables) + " filed."
}

// manifestCountLine spells the deliverable count in the card's voice.
func manifestCountLine(count int) string {
	words := map[int]string{1: "one deliverable", 2: "two deliverables", 3: "three deliverables", 4: "four deliverables", 5: "five deliverables"}
	if word, ok := words[count]; ok {
		return word
	}
	return fmt.Sprintf("%d deliverables", count)
}

// manifestWallClock formats the run's wall clock the way the provenance line
// reads it: "2h 25m", "11m", "45s".
func manifestWallClock(elapsed time.Duration) string {
	if elapsed <= 0 {
		return ""
	}
	if elapsed < time.Minute {
		return fmt.Sprintf("%ds", int(elapsed.Seconds()))
	}
	if elapsed < time.Hour {
		return fmt.Sprintf("%dm", int(elapsed.Minutes()))
	}
	return fmt.Sprintf("%dh %dm", int(elapsed.Hours()), int(elapsed.Minutes())%60)
}
