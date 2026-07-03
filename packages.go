package main

// Venture packages — the per-IP mission binders. A package binds the
// artifacts, board cards, decisions, and (optionally) one channel that move
// one piece of IP through the pipeline stages. STORAGE: memory kind "package"
// in data/meeting-memory.jsonl (the scout_chat_thread precedent — entry.Text
// is the full record JSON), NOT a packages.json sidecar: the JSONL store
// already provides durable append + in-place rewrite, id dedupe, boot
// loading, and automatic meetingId provenance. Trust boundary = the
// signed-in team: anyone signed in can create/edit; payloads carry artifact
// TITLES only, never artifact text.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"
)

// packageStages is the fixed pipeline; advance without an explicit stage
// steps forward one (no-op at "assembled").
var packageStages = []string{"thesis", "research", "design", "pitch", "grill", "assembled"}

const (
	packageRefTypeArtifact = "artifact"
	packageRefTypeCard     = "card"
	packageRefTypeChannel  = "channel"
	packageRefTypeDecision = "decision"
	// packageFuzzyNameThreshold is the token-overlap floor for resolving a
	// spoken package name ("advance Nimbus") to a stored package.
	packageFuzzyNameThreshold = 0.7
)

// packageGrillScoreRE matches the first "N/10" score line in a grill-mode
// artifact body (buildGrillModeAnswer emits a Score line; codex grill reports
// typically do too).
var packageGrillScoreRE = regexp.MustCompile(`(?i)\b(\d+(?:\.\d+)?)\s*/\s*10\b`)

type venturePackageRecord struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Thesis       string   `json:"thesis"`
	Stage        string   `json:"stage"`
	ArtifactIDs  []string `json:"artifactIds"`
	BoardCardIDs []string `json:"boardCardIds"`
	DecisionIDs  []string `json:"decisionIds"`
	ChannelID    string   `json:"channelId"`
	CreatedBy    string   `json:"createdBy"`
	CreatedAt    string   `json:"createdAt"`
	UpdatedBy    string   `json:"updatedBy"`
	UpdatedAt    string   `json:"updatedAt"`
}

func normalizePackageStage(stage string) string {
	stage = strings.ToLower(strings.TrimSpace(stage))
	for _, candidate := range packageStages {
		if candidate == stage {
			return candidate
		}
	}
	return ""
}

func packageStageIndex(stage string) int {
	for index, candidate := range packageStages {
		if candidate == stage {
			return index
		}
	}
	return 0
}

func normalizePackageRefType(refType string) string {
	switch strings.ToLower(strings.TrimSpace(refType)) {
	case packageRefTypeArtifact, packageRefTypeCard, packageRefTypeChannel, packageRefTypeDecision:
		return strings.ToLower(strings.TrimSpace(refType))
	default:
		return ""
	}
}

func encodeVenturePackage(record venturePackageRecord) (string, error) {
	raw, err := json.Marshal(record)
	if err != nil {
		return "", fmt.Errorf("encode venture package: %w", err)
	}
	return string(raw), nil
}

// decodeVenturePackageEntry mirrors decodeScoutChatThreadEntry: the record is
// the entry text, with entry id/metadata backfilling older rows.
func decodeVenturePackageEntry(entry meetingMemoryEntry) (venturePackageRecord, bool) {
	if entry.Kind != meetingMemoryKindPackage {
		return venturePackageRecord{}, false
	}
	var record venturePackageRecord
	if err := json.Unmarshal([]byte(entry.Text), &record); err != nil {
		return venturePackageRecord{}, false
	}
	if strings.TrimSpace(record.ID) == "" {
		record.ID = entry.ID
	}
	if strings.TrimSpace(record.Name) == "" {
		record.Name = entry.Metadata["name"]
	}
	if normalizePackageStage(record.Stage) == "" {
		record.Stage = firstNonEmptyString(normalizePackageStage(entry.Metadata["stage"]), packageStages[0])
	}
	if strings.TrimSpace(record.CreatedAt) == "" && !entry.CreatedAt.IsZero() {
		record.CreatedAt = entry.CreatedAt.UTC().Format(time.RFC3339Nano)
	}
	if strings.TrimSpace(record.UpdatedAt) == "" {
		record.UpdatedAt = firstNonEmptyString(entry.Metadata["updatedAt"], record.CreatedAt)
	}
	return record, true
}

// venturePackagesSnapshot returns every decodable package, newest-updated
// first.
func (app *kanbanBoardApp) venturePackagesSnapshot() []venturePackageRecord {
	if app == nil || app.memory == nil {
		return nil
	}
	records := []venturePackageRecord{}
	for _, entry := range app.memory.entriesOfKind(meetingMemoryKindPackage, 0) {
		if record, ok := decodeVenturePackageEntry(entry); ok {
			records = append(records, record)
		}
	}
	sort.SliceStable(records, func(left, right int) bool {
		return records[left].UpdatedAt > records[right].UpdatedAt
	})
	return records
}

func (app *kanbanBoardApp) venturePackageByID(id string) (venturePackageRecord, bool) {
	if app == nil || app.memory == nil {
		return venturePackageRecord{}, false
	}
	entry, ok := app.memory.entryByKindAndID(meetingMemoryKindPackage, strings.TrimSpace(id))
	if !ok {
		return venturePackageRecord{}, false
	}
	return decodeVenturePackageEntry(entry)
}

// venturePackageByExactName resolves a package by case-insensitive exact name
// only — the contract the decision ledger's optional "package" output field
// uses (no fuzzy guessing from a model-written name).
func (app *kanbanBoardApp) venturePackageByExactName(name string) (venturePackageRecord, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return venturePackageRecord{}, false
	}
	for _, record := range app.venturePackagesSnapshot() {
		if strings.EqualFold(strings.TrimSpace(record.Name), name) {
			return record, true
		}
	}
	return venturePackageRecord{}, false
}

// findPackageByNameOrID resolves a package reference: exact id first, then a
// case-insensitive name match, then a token-overlap fuzzy match with a single
// clear winner (for voice: "advance Nimbus").
func (app *kanbanBoardApp) findPackageByNameOrID(ref string) (venturePackageRecord, bool) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return venturePackageRecord{}, false
	}
	if record, ok := app.venturePackageByID(ref); ok {
		return record, true
	}
	records := app.venturePackagesSnapshot()
	for _, record := range records {
		if strings.EqualFold(strings.TrimSpace(record.Name), ref) {
			return record, true
		}
	}
	refTokens := linkageMatchTokens(ref)
	best := venturePackageRecord{}
	bestScore := 0.0
	winners := 0
	for _, record := range records {
		score := tokenSetJaccard(refTokens, linkageMatchTokens(record.Name))
		if score < packageFuzzyNameThreshold {
			continue
		}
		if score > bestScore {
			best = record
			bestScore = score
			winners = 1
		} else if score == bestScore {
			winners++
		}
	}
	if bestScore < packageFuzzyNameThreshold || winners != 1 {
		return venturePackageRecord{}, false
	}
	return best, true
}

// persistVenturePackage writes the record (create or whole-record update),
// keeping the cheap {"name","stage"} metadata mirror in sync. Callers hold
// packageMu — which is exactly why it must NOT broadcast: websocket writes
// under packageMu would serialize every package mutation behind slow office
// sockets (the meetings.go lock rule: mutate under the store lock, broadcast
// only after every lock is released). Mutators call broadcastVenturePackage
// after packageMu is unlocked.
func (app *kanbanBoardApp) persistVenturePackage(record venturePackageRecord, create bool) (venturePackageRecord, error) {
	encoded, err := encodeVenturePackage(record)
	if err != nil {
		return venturePackageRecord{}, err
	}
	metadata := map[string]string{
		"name":      record.Name,
		"stage":     record.Stage,
		"updatedAt": record.UpdatedAt,
	}
	if create {
		_, appended, appendErr := app.memory.appendVenturePackage(record.ID, encoded, metadata)
		if appendErr != nil {
			return venturePackageRecord{}, appendErr
		}
		if !appended {
			return venturePackageRecord{}, fmt.Errorf("package was not saved")
		}
	} else {
		if _, _, updateErr := app.memory.updateEntryWithMetadata(meetingMemoryKindPackage, record.ID, encoded, metadata); updateErr != nil {
			return venturePackageRecord{}, updateErr
		}
	}
	return record, nil
}

// broadcastVenturePackage fans the package payload out on the office channel.
// Only ever called AFTER packageMu is released.
func (app *kanbanBoardApp) broadcastVenturePackage(record venturePackageRecord) {
	broadcastOfficeKanbanEvent("package", app.packagePayload(record))
}

// createVenturePackage creates a package at stage "thesis". Names are
// required and unique case-insensitively.
func (app *kanbanBoardApp) createVenturePackage(name string, thesis string, createdBy string) (venturePackageRecord, error) {
	if app == nil || app.memory == nil {
		return venturePackageRecord{}, fmt.Errorf("meeting memory is unavailable")
	}
	name = canonicalizeBoardText(name)
	if name == "" {
		return venturePackageRecord{}, fmt.Errorf("package name is required")
	}

	record, err := func() (venturePackageRecord, error) {
		app.packageMu.Lock()
		defer app.packageMu.Unlock()

		for _, existing := range app.venturePackagesSnapshot() {
			if strings.EqualFold(strings.TrimSpace(existing.Name), name) {
				return venturePackageRecord{}, fmt.Errorf("a package named %q already exists", existing.Name)
			}
		}

		now := time.Now().UTC().Format(time.RFC3339Nano)
		record := venturePackageRecord{
			ID:           durableTimestampID("package", time.Now()),
			Name:         name,
			Thesis:       strings.TrimSpace(thesis),
			Stage:        packageStages[0],
			ArtifactIDs:  []string{},
			BoardCardIDs: []string{},
			DecisionIDs:  []string{},
			CreatedBy:    strings.TrimSpace(createdBy),
			CreatedAt:    now,
			UpdatedBy:    strings.TrimSpace(createdBy),
			UpdatedAt:    now,
		}
		return app.persistVenturePackage(record, true)
	}()
	if err != nil {
		return venturePackageRecord{}, err
	}
	app.broadcastVenturePackage(record)
	return record, nil
}

// updateVenturePackage applies name/thesis edits.
func (app *kanbanBoardApp) updateVenturePackage(id string, updates map[string]string, updatedBy string) (venturePackageRecord, error) {
	if app == nil || app.memory == nil {
		return venturePackageRecord{}, fmt.Errorf("meeting memory is unavailable")
	}

	record, persisted, err := func() (venturePackageRecord, bool, error) {
		app.packageMu.Lock()
		defer app.packageMu.Unlock()

		record, ok := app.venturePackageByID(id)
		if !ok {
			return venturePackageRecord{}, false, fmt.Errorf("package not found")
		}
		changed := false
		if rawName, present := updates["name"]; present {
			name := canonicalizeBoardText(rawName)
			if name == "" {
				return venturePackageRecord{}, false, fmt.Errorf("package name is required")
			}
			for _, existing := range app.venturePackagesSnapshot() {
				if existing.ID != record.ID && strings.EqualFold(strings.TrimSpace(existing.Name), name) {
					return venturePackageRecord{}, false, fmt.Errorf("a package named %q already exists", existing.Name)
				}
			}
			if record.Name != name {
				record.Name = name
				changed = true
			}
		}
		if rawThesis, present := updates["thesis"]; present {
			thesis := strings.TrimSpace(rawThesis)
			if record.Thesis != thesis {
				record.Thesis = thesis
				changed = true
			}
		}
		if !changed {
			return record, false, nil
		}
		record.UpdatedBy = strings.TrimSpace(updatedBy)
		record.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
		persisted, err := app.persistVenturePackage(record, false)
		return persisted, err == nil, err
	}()
	if err != nil {
		return venturePackageRecord{}, err
	}
	if persisted {
		app.broadcastVenturePackage(record)
	}
	return record, nil
}

// advancePackageStage moves a package through the pipeline. An empty stage
// steps to the next stage (no-op at "assembled"); an explicit stage may be
// any valid value, forward or back — 6-person trust.
func (app *kanbanBoardApp) advancePackageStage(id string, stage string, updatedBy string) (venturePackageRecord, error) {
	if app == nil || app.memory == nil {
		return venturePackageRecord{}, fmt.Errorf("meeting memory is unavailable")
	}

	record, persisted, err := func() (venturePackageRecord, bool, error) {
		app.packageMu.Lock()
		defer app.packageMu.Unlock()

		record, ok := app.venturePackageByID(id)
		if !ok {
			return venturePackageRecord{}, false, fmt.Errorf("package not found")
		}
		next := ""
		if strings.TrimSpace(stage) == "" {
			index := packageStageIndex(record.Stage)
			if index >= len(packageStages)-1 {
				// terminal: assembled stays assembled
				return record, false, nil
			}
			next = packageStages[index+1]
		} else {
			next = normalizePackageStage(stage)
			if next == "" {
				return venturePackageRecord{}, false, fmt.Errorf("stage must be one of %s", strings.Join(packageStages, ", "))
			}
		}
		if record.Stage == next {
			return record, false, nil
		}
		record.Stage = next
		record.UpdatedBy = strings.TrimSpace(updatedBy)
		record.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
		persisted, err := app.persistVenturePackage(record, false)
		return persisted, err == nil, err
	}()
	if err != nil {
		return venturePackageRecord{}, err
	}
	if persisted {
		app.broadcastVenturePackage(record)
		// Unified push channel: the stage advance as a title-only signal. The
		// package rail is a rich consumer — it re-reads packagePayload by ref
		// on receipt (the full 'package' event above self-heals it too).
		broadcastOSEvent(osEvent{
			Kind:          osEventPackageAdvanced,
			Ref:           record.ID,
			Title:         strings.TrimSpace(record.Name) + " → " + record.Stage,
			OriginSurface: "packages",
			Actor:         firstNonEmptyString(strings.TrimSpace(updatedBy), scoutParticipantName),
		})
	}
	return record, nil
}

func appendUniqueString(values []string, value string) ([]string, bool) {
	for _, existing := range values {
		if existing == value {
			return values, false
		}
	}
	return append(values, value), true
}

func removeString(values []string, value string) ([]string, bool) {
	next := make([]string, 0, len(values))
	removed := false
	for _, existing := range values {
		if existing == value {
			removed = true
			continue
		}
		next = append(next, existing)
	}
	return next, removed
}

// validatePackageRef confirms the referenced object exists (and, for
// channels, is public).
func (app *kanbanBoardApp) validatePackageRef(refType string, refID string) error {
	switch refType {
	case packageRefTypeArtifact:
		if _, ok := app.osArtifactByID(refID); !ok {
			return fmt.Errorf("artifact not found")
		}
	case packageRefTypeCard:
		if _, ok := app.matchBoardCard("", refID); !ok {
			return fmt.Errorf("board card not found")
		}
	case packageRefTypeChannel:
		entry, ok := app.memory.entryByKindAndID(meetingMemoryKindScoutChat, refID)
		if !ok {
			return fmt.Errorf("channel not found")
		}
		thread, decoded := decodeScoutChatThreadEntry(entry)
		if !decoded || scoutChatThreadVisibility(thread) != scoutChatVisibilityPublic {
			return fmt.Errorf("channel not found")
		}
	case packageRefTypeDecision:
		if _, ok := app.memory.entryByKindAndID(meetingMemoryKindDecision, refID); !ok {
			return fmt.Errorf("decision not found")
		}
	default:
		return fmt.Errorf("ref_type must be artifact, card, channel, or decision")
	}
	return nil
}

// attachToPackage binds an existing artifact/card/channel/decision to the
// package. Idempotent (re-attaching is a no-op); channel is single-valued
// (replace). Attaching an artifact or decision also stamps packageId onto
// that entry — the bidirectional link the completion hooks and Scout context
// rely on.
func (app *kanbanBoardApp) attachToPackage(id string, refType string, refID string, updatedBy string) (venturePackageRecord, error) {
	if app == nil || app.memory == nil {
		return venturePackageRecord{}, fmt.Errorf("meeting memory is unavailable")
	}
	refType = normalizePackageRefType(refType)
	refID = strings.TrimSpace(refID)
	if refType == "" {
		return venturePackageRecord{}, fmt.Errorf("ref_type must be artifact, card, channel, or decision")
	}
	if refID == "" {
		return venturePackageRecord{}, fmt.Errorf("ref_id is required")
	}
	if err := app.validatePackageRef(refType, refID); err != nil {
		return venturePackageRecord{}, err
	}

	persisted, changed, err := func() (venturePackageRecord, bool, error) {
		app.packageMu.Lock()
		defer app.packageMu.Unlock()

		record, ok := app.venturePackageByID(id)
		if !ok {
			return venturePackageRecord{}, false, fmt.Errorf("package not found")
		}
		changed := false
		switch refType {
		case packageRefTypeArtifact:
			record.ArtifactIDs, changed = appendUniqueString(record.ArtifactIDs, refID)
		case packageRefTypeCard:
			record.BoardCardIDs, changed = appendUniqueString(record.BoardCardIDs, refID)
		case packageRefTypeChannel:
			changed = record.ChannelID != refID
			record.ChannelID = refID
		case packageRefTypeDecision:
			record.DecisionIDs, changed = appendUniqueString(record.DecisionIDs, refID)
		}
		if !changed {
			return record, false, nil
		}
		record.UpdatedBy = strings.TrimSpace(updatedBy)
		record.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
		persisted, err := app.persistVenturePackage(record, false)
		return persisted, err == nil, err
	}()
	if err != nil {
		return venturePackageRecord{}, err
	}
	if !changed {
		return persisted, nil
	}

	// Bidirectional stamp: failures only lose the reverse link (the package
	// side is already saved), so they are logged, never fatal.
	switch refType {
	case packageRefTypeArtifact:
		if artifact, found := app.osArtifactByID(refID); found {
			if _, _, stampErr := app.updateOSArtifactWithMetadata(refID, "", artifact.Text, "", map[string]string{"packageId": persisted.ID}); stampErr != nil {
				log.Errorf("Failed to stamp packageId on artifact %s: %v", refID, stampErr)
			}
		}
	case packageRefTypeDecision:
		if entry, found := app.memory.entryByKindAndID(meetingMemoryKindDecision, refID); found {
			if _, _, stampErr := app.memory.updateEntryWithMetadata(meetingMemoryKindDecision, refID, entry.Text, map[string]string{"packageId": persisted.ID}); stampErr != nil {
				log.Errorf("Failed to stamp packageId on decision %s: %v", refID, stampErr)
			}
		}
	}
	app.broadcastVenturePackage(persisted)
	return persisted, nil
}

// detachFromPackage removes a reference (and clears the reverse packageId
// stamp where one exists).
func (app *kanbanBoardApp) detachFromPackage(id string, refType string, refID string, updatedBy string) (venturePackageRecord, error) {
	if app == nil || app.memory == nil {
		return venturePackageRecord{}, fmt.Errorf("meeting memory is unavailable")
	}
	refType = normalizePackageRefType(refType)
	refID = strings.TrimSpace(refID)
	if refType == "" {
		return venturePackageRecord{}, fmt.Errorf("ref_type must be artifact, card, channel, or decision")
	}
	if refID == "" {
		return venturePackageRecord{}, fmt.Errorf("ref_id is required")
	}

	persisted, changed, err := func() (venturePackageRecord, bool, error) {
		app.packageMu.Lock()
		defer app.packageMu.Unlock()

		record, ok := app.venturePackageByID(id)
		if !ok {
			return venturePackageRecord{}, false, fmt.Errorf("package not found")
		}
		changed := false
		switch refType {
		case packageRefTypeArtifact:
			record.ArtifactIDs, changed = removeString(record.ArtifactIDs, refID)
		case packageRefTypeCard:
			record.BoardCardIDs, changed = removeString(record.BoardCardIDs, refID)
		case packageRefTypeChannel:
			changed = record.ChannelID == refID && refID != ""
			if changed {
				record.ChannelID = ""
			}
		case packageRefTypeDecision:
			record.DecisionIDs, changed = removeString(record.DecisionIDs, refID)
		}
		if !changed {
			return record, false, nil
		}
		record.UpdatedBy = strings.TrimSpace(updatedBy)
		record.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
		persisted, err := app.persistVenturePackage(record, false)
		return persisted, err == nil, err
	}()
	if err != nil {
		return venturePackageRecord{}, err
	}
	if !changed {
		return persisted, nil
	}
	switch refType {
	case packageRefTypeArtifact:
		if artifact, found := app.osArtifactByID(refID); found && artifact.Metadata["packageId"] == persisted.ID {
			if _, _, stampErr := app.updateOSArtifactWithMetadata(refID, "", artifact.Text, "", map[string]string{"packageId": ""}); stampErr != nil {
				log.Errorf("Failed to clear packageId on artifact %s: %v", refID, stampErr)
			}
		}
	case packageRefTypeDecision:
		if entry, found := app.memory.entryByKindAndID(meetingMemoryKindDecision, refID); found && entry.Metadata["packageId"] == persisted.ID {
			if _, _, stampErr := app.memory.updateEntryWithMetadata(meetingMemoryKindDecision, refID, entry.Text, map[string]string{"packageId": ""}); stampErr != nil {
				log.Errorf("Failed to clear packageId on decision %s: %v", refID, stampErr)
			}
		}
	}
	app.broadcastVenturePackage(persisted)
	return persisted, nil
}

// packageArtifactTuple is the SAFE artifact projection for the all-users
// payload: titles and status metadata only, never artifact text (same rule
// as buildMissionIntelInput). Grill readiness metadata rides along when a
// re-grill pass has stored it; the client omits the field otherwise.
func packageArtifactTuple(artifact meetingMemoryEntry) map[string]any {
	tuple := map[string]any{
		"id":     artifact.ID,
		"title":  firstNonEmptyString(artifact.Metadata["title"], artifact.Metadata["threadQuery"], "untitled artifact"),
		"mode":   artifact.Metadata["mode"],
		"status": firstNonEmptyString(artifact.Metadata["threadStatus"], artifact.Metadata["status"]),
	}
	if readiness := readinessTupleValue(artifact.Metadata); readiness != "" {
		tuple["readiness"] = readiness
	}
	if readinessDelta := strings.TrimSpace(artifact.Metadata["readinessDelta"]); readinessDelta != "" {
		tuple["readinessDelta"] = readinessDelta
	}
	return tuple
}

// readinessTupleValue prefers the machine-parsed READINESS contract score
// (metadata["readinessScore"], stamped by the grill terminal seams in
// agent_thread_followup.go) formatted for display; artifacts carrying only a
// free-form "readiness" stamp pass through unchanged.
func readinessTupleValue(metadata map[string]string) string {
	if score := strings.TrimSpace(metadata["readinessScore"]); score != "" {
		return score + "/10"
	}
	return strings.TrimSpace(metadata["readiness"])
}

// packagePayload shapes the wire/GET form of a package: the record plus
// derived stats (counts, pipeline gaps, latest grill score) and safe attached
// tuples (artifact titles, decision statements).
func (app *kanbanBoardApp) packagePayload(record venturePackageRecord) map[string]any {
	artifacts := []map[string]any{}
	modesSeen := map[string]bool{}
	grillScore := ""
	// newest-first so the first grill score found is the latest attached
	for index := len(record.ArtifactIDs) - 1; index >= 0; index-- {
		artifact, ok := app.osArtifactByID(record.ArtifactIDs[index])
		if !ok {
			continue
		}
		mode := strings.ToLower(strings.TrimSpace(artifact.Metadata["mode"]))
		modesSeen[mode] = true
		if grillScore == "" && mode == "grill" {
			// The stamped READINESS score wins; the body regex remains the
			// fallback for grill artifacts that predate the contract.
			if score := strings.TrimSpace(artifact.Metadata["readinessScore"]); score != "" {
				grillScore = score
			} else if match := packageGrillScoreRE.FindStringSubmatch(artifact.Text); len(match) > 1 {
				grillScore = match[1]
			}
		}
		artifacts = append(artifacts, packageArtifactTuple(artifact))
	}

	decisions := []map[string]any{}
	for _, decisionID := range record.DecisionIDs {
		entry, ok := app.memory.entryByKindAndID(meetingMemoryKindDecision, decisionID)
		if !ok {
			continue
		}
		decisions = append(decisions, map[string]any{
			"id":        entry.ID,
			"statement": entry.Text,
			"madeBy":    entry.Metadata["madeBy"],
		})
	}

	// gaps: deliverable stages at-or-before the current stage with no
	// attached artifact of that mode
	gaps := []string{}
	stageIndex := packageStageIndex(record.Stage)
	for _, stage := range []string{"research", "design", "grill"} {
		if packageStageIndex(stage) <= stageIndex && !modesSeen[stage] {
			gaps = append(gaps, stage)
		}
	}

	stats := map[string]any{
		"artifactCount": len(artifacts),
		"decisionCount": len(decisions),
		"cardCount":     len(record.BoardCardIDs),
		"gaps":          gaps,
	}
	if grillScore != "" {
		stats["grillScore"] = grillScore
	}

	payload := map[string]any{
		"id":           record.ID,
		"name":         record.Name,
		"thesis":       record.Thesis,
		"stage":        record.Stage,
		"stageIndex":   stageIndex,
		"stages":       packageStages,
		"artifactIds":  record.ArtifactIDs,
		"boardCardIds": record.BoardCardIDs,
		"decisionIds":  record.DecisionIDs,
		"channelId":    record.ChannelID,
		"createdBy":    record.CreatedBy,
		"createdAt":    record.CreatedAt,
		"updatedAt":    record.UpdatedAt,
		"artifacts":    artifacts,
		"decisions":    decisions,
		"stats":        stats,
	}
	return payload
}

func (app *kanbanBoardApp) venturePackagePayloads() []map[string]any {
	payloads := []map[string]any{}
	for _, record := range app.venturePackagesSnapshot() {
		payloads = append(payloads, app.packagePayload(record))
	}
	return payloads
}

/* ---------- Scout tools ---------- */

// packageToolActor resolves who a package mutation is attributed to: the
// private dashboard voice passes the signed-in user's email; the shared room
// voice and workers fall back to Scout.
func packageToolActor(requesterEmail string) string {
	return firstNonEmptyString(participantNameForEmail(requesterEmail), normalizeAccountEmail(requesterEmail), scoutParticipantName)
}

func (app *kanbanBoardApp) createPackageTool(args map[string]any, actor string) (map[string]any, bool, error) {
	record, err := app.createVenturePackage(asString(args["name"]), asString(args["thesis"]), firstNonEmptyString(strings.TrimSpace(actor), scoutParticipantName))
	if err != nil {
		return nil, false, err
	}
	return map[string]any{
		"ok":      true,
		"package": app.packagePayload(record),
	}, true, nil
}

func (app *kanbanBoardApp) attachToPackageTool(args map[string]any, actor string) (map[string]any, bool, error) {
	record, ok := app.findPackageByNameOrID(asString(args["package"]))
	if !ok {
		return nil, false, fmt.Errorf("package not found: %s", asString(args["package"]))
	}
	refType := normalizePackageRefType(asString(args["ref_type"]))
	if refType == "" {
		return nil, false, fmt.Errorf("ref_type must be artifact, card, channel, or decision")
	}
	refID := strings.TrimSpace(asString(args["ref_id"]))
	if refID == "" {
		refID = app.resolvePackageRefTitle(refType, asString(args["ref_title"]))
	}
	if refID == "" {
		return nil, false, fmt.Errorf("could not resolve a %s to attach; pass ref_id or a closer ref_title", refType)
	}
	updated, err := app.attachToPackage(record.ID, refType, refID, firstNonEmptyString(strings.TrimSpace(actor), scoutParticipantName))
	if err != nil {
		return nil, false, err
	}
	return map[string]any{
		"ok":      true,
		"package": app.packagePayload(updated),
	}, true, nil
}

func (app *kanbanBoardApp) advancePackageStageTool(args map[string]any, actor string) (map[string]any, bool, error) {
	record, ok := app.findPackageByNameOrID(asString(args["package"]))
	if !ok {
		return nil, false, fmt.Errorf("package not found: %s", asString(args["package"]))
	}
	updated, err := app.advancePackageStage(record.ID, asString(args["stage"]), firstNonEmptyString(strings.TrimSpace(actor), scoutParticipantName))
	if err != nil {
		return nil, false, err
	}
	return map[string]any{
		"ok":      true,
		"package": app.packagePayload(updated),
	}, updated.Stage != record.Stage || strings.TrimSpace(asString(args["stage"])) == "", nil
}

// resolvePackageRefTitle fuzzy-resolves a spoken/typed title to a concrete
// ref id within one ref type: artifacts by metadata title, cards through the
// linkage matcher, channels by public thread title, decisions by statement.
// Conservative: ambiguity resolves to "" (a missed attach is cheap).
func (app *kanbanBoardApp) resolvePackageRefTitle(refType string, title string) string {
	title = strings.TrimSpace(title)
	if title == "" {
		return ""
	}
	switch refType {
	case packageRefTypeCard:
		if card, ok := app.matchBoardCard(title, ""); ok {
			return card.ID
		}
		return ""
	case packageRefTypeArtifact:
		titleTokens := linkageMatchTokens(title)
		bestID := ""
		bestScore := 0.0
		winners := 0
		for _, artifact := range app.osArtifactsSnapshot(0) {
			candidate := firstNonEmptyString(artifact.Metadata["title"], artifact.Metadata["threadQuery"])
			score := tokenSetJaccard(titleTokens, linkageMatchTokens(candidate))
			if score < packageFuzzyNameThreshold {
				continue
			}
			if score > bestScore {
				bestID = artifact.ID
				bestScore = score
				winners = 1
			} else if score == bestScore {
				winners++
			}
		}
		if winners != 1 {
			return ""
		}
		return bestID
	case packageRefTypeChannel:
		titleTokens := linkageMatchTokens(title)
		bestID := ""
		bestScore := 0.0
		winners := 0
		for _, entry := range app.memory.entriesOfKind(meetingMemoryKindScoutChat, 0) {
			thread, ok := decodeScoutChatThreadEntry(entry)
			if !ok || scoutChatThreadVisibility(thread) != scoutChatVisibilityPublic {
				continue
			}
			score := tokenSetJaccard(titleTokens, linkageMatchTokens(thread.Title))
			if score < packageFuzzyNameThreshold {
				continue
			}
			if score > bestScore {
				bestID = thread.ID
				bestScore = score
				winners = 1
			} else if score == bestScore {
				winners++
			}
		}
		if winners != 1 {
			return ""
		}
		return bestID
	case packageRefTypeDecision:
		titleTokens := linkageMatchTokens(title)
		bestID := ""
		bestScore := 0.0
		winners := 0
		for _, entry := range app.memory.entriesOfKind(meetingMemoryKindDecision, 0) {
			score := tokenSetJaccard(titleTokens, linkageMatchTokens(entry.Text))
			if score < packageFuzzyNameThreshold {
				continue
			}
			if score > bestScore {
				bestID = entry.ID
				bestScore = score
				winners = 1
			} else if score == bestScore {
				winners++
			}
		}
		if winners != 1 {
			return ""
		}
		return bestID
	default:
		return ""
	}
}

/* ---------- HTTP ---------- */

// assistantPackagesHandler serves GET (list) and POST (create) on
// /assistant/packages with the same origin + session guards as
// assistantMissionHandler: any signed-in user — payloads carry titles,
// statements, and counts, never artifact text.
func assistantPackagesHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
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
		writeAuthError(w, http.StatusServiceUnavailable, "packages are unavailable")
		return
	}

	if r.Method == http.MethodGet {
		writeAuthJSON(w, http.StatusOK, map[string]any{
			"ok":       true,
			"packages": kanbanApp.venturePackagePayloads(),
		})
		return
	}

	payload := struct {
		Name   string `json:"name"`
		Thesis string `json:"thesis"`
	}{}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10)).Decode(&payload); err != nil {
		writeAuthError(w, http.StatusBadRequest, "could not read package")
		return
	}
	record, err := kanbanApp.createVenturePackage(payload.Name, payload.Thesis, user.Name)
	if err != nil {
		writeAuthError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeAuthJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"package": kanbanApp.packagePayload(record),
	})
}

// assistantPackageActionHandler serves POST /assistant/packages/{id}/action
// (path parsing mirrors assistantProposalActionHandler). Actions:
// advance_stage, set_stage, attach, detach, update.
func assistantPackageActionHandler(w http.ResponseWriter, r *http.Request) {
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
		writeAuthError(w, http.StatusServiceUnavailable, "packages are unavailable")
		return
	}

	suffix := strings.Trim(strings.TrimPrefix(r.URL.Path, "/assistant/packages/"), "/")
	parts := strings.Split(suffix, "/")
	if suffix == "" || len(parts) != 2 || parts[0] == "" || parts[1] != "action" {
		http.NotFound(w, r)
		return
	}

	payload := struct {
		Action  string  `json:"action"`
		Stage   string  `json:"stage"`
		RefType string  `json:"refType"`
		RefID   string  `json:"refId"`
		Name    *string `json:"name"`
		Thesis  *string `json:"thesis"`
	}{}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10)).Decode(&payload); err != nil {
		writeAuthError(w, http.StatusBadRequest, "could not read package action")
		return
	}

	id := parts[0]
	var record venturePackageRecord
	var err error
	switch strings.ToLower(strings.TrimSpace(payload.Action)) {
	case "advance_stage":
		record, err = kanbanApp.advancePackageStage(id, "", user.Name)
	case "set_stage":
		record, err = kanbanApp.advancePackageStage(id, payload.Stage, user.Name)
	case "attach":
		record, err = kanbanApp.attachToPackage(id, payload.RefType, payload.RefID, user.Name)
	case "detach":
		record, err = kanbanApp.detachFromPackage(id, payload.RefType, payload.RefID, user.Name)
	case "update":
		updates := map[string]string{}
		if payload.Name != nil {
			updates["name"] = *payload.Name
		}
		if payload.Thesis != nil {
			updates["thesis"] = *payload.Thesis
		}
		record, err = kanbanApp.updateVenturePackage(id, updates, user.Name)
	default:
		writeAuthError(w, http.StatusBadRequest, "action must be advance_stage, set_stage, attach, detach, or update")
		return
	}
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "not found") {
			status = http.StatusNotFound
		}
		writeAuthError(w, status, err.Error())
		return
	}
	writeAuthJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"package": kanbanApp.packagePayload(record),
	})
}
