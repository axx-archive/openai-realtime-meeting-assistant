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
	"strconv"
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
		// Signal capture (signals.go): binding an artifact into a package binder
		// is a positive vote on that output. Log-and-continue inside.
		app.recordSignalEvent(updatedBy, signalEventArtifactAttached, signalValencePositive, refID, persisted.ID, nil)
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

/* ---------- Interlock compiler (Wave 4 item 19) ---------- */

// package_assembly's deterministic PRE-pass (packaging OS §3 tail + §4): before
// the binder model spends a single token, the engine scans everything attached
// to the package pairwise for the contradictions the binder exists to catch —
// values carrying the SAME label that disagree (the "$2M ask here, $3M ask
// there" class), plus the explicit interlocks[] rules the Wave 3 scaffold
// stores on artifacts (artifactInterlocks, memory_query.go). Findings feed the
// deliverable prompt as a MUST-RESOLVE list, land on the binder artifact as
// interlockFindings JSON, and are re-checked mechanically at review time
// through the law-sweep seam (packageBinderLawSweep) — zero model cost end to
// end, the toolLawSweep precedent.

// packageInterlockFindingsMetadataKey holds the pre-pass findings on the
// binder artifact (a JSON array; "[]" records a clean scan, absent means the
// pre-pass never ran and the sweep enforces nothing).
const packageInterlockFindingsMetadataKey = "interlockFindings"

const (
	interlockKindValueCollision = "value_collision"
	interlockKindRule           = "interlock_rule"

	interlockSeverityMustResolve = "must_resolve"
	// interlockSeverityKill marks the vision-deck/rigor-companion class (§4's
	// no-contradiction rule): a MUST-RESOLVE finding between a deck-type and a
	// memo/rigor-type artifact is kill-condition-grade — the binder must
	// reconcile it, never ship it disclosed-open.
	interlockSeverityKill = "kill"
)

// packageInterlockFinding is one contradiction the pre-pass found. IDs are
// deterministic (IL-1, IL-2, … in scan order) because the law sweep greps the
// produced binder for them.
type packageInterlockFinding struct {
	ID          string   `json:"id"`
	Kind        string   `json:"kind"`
	Severity    string   `json:"severity"`
	Label       string   `json:"label"`
	Detail      string   `json:"detail"`
	ArtifactIDs []string `json:"artifactIds"`
}

// The three value shapes the scan understands. Conservative on purpose: bare
// integers ("12 episodes") are NOT values — only money, percentages, and dated
// quarters/months carry enough intent to compare across artifacts.
var (
	interlockMoneyRE   = regexp.MustCompile(`(?i)\$\s?\d[\d,]*(?:\.\d+)?(?:\s?(?:[kmb]n?\b|thousand|million|billion))?`)
	interlockPercentRE = regexp.MustCompile(`\b\d+(?:\.\d+)?\s?%`)
	interlockDateRE    = regexp.MustCompile(`(?i)\b(?:q[1-4]\s+20\d{2}|(?:jan(?:uary)?|feb(?:ruary)?|mar(?:ch)?|apr(?:il)?|may|jun(?:e)?|jul(?:y)?|aug(?:ust)?|sep(?:t|tember)?|oct(?:ober)?|nov(?:ember)?|dec(?:ember)?)\.?\s+(?:\d{1,2},?\s+)?20\d{2})\b`)
)

var interlockValueREs = []*regexp.Regexp{interlockMoneyRE, interlockPercentRE, interlockDateRE}

// interlockLabelSegmentRE splits the text before a value at clause boundaries
// (including colons, so "Slide 9: Series A ask: $2M" labels as "series a ask")
// so a label never leaks in from the previous clause.
var interlockLabelSegmentRE = regexp.MustCompile(`[.;!?•|:]|—|–`)

var interlockLabelWordRE = regexp.MustCompile(`[A-Za-z][A-Za-z0-9'\-]*`)

// Connector words trimmed off a label's tail ("the Series A ask is $2M" →
// "series a ask") and articles off its head. A label keeps >= 2 words after
// trimming — one-word labels ("revenue") collide constantly across honest
// artifacts, and the scan flags only exact-label collisions it can defend.
var interlockLabelTrailingStops = map[string]bool{
	"is": true, "was": true, "are": true, "be": true, "of": true, "at": true,
	"to": true, "for": true, "in": true, "on": true, "by": true, "the": true,
	"a": true, "an": true, "about": true, "around": true, "roughly": true,
	"approximately": true, "currently": true, "now": true, "says": true,
	"stays": true, "remains": true, "targets": true, "near": true,
	"under": true, "over": true,
}

var interlockLabelLeadingStops = map[string]bool{
	"the": true, "a": true, "an": true, "our": true, "their": true, "its": true,
	"this": true, "that": true, "and": true, "with": true, "per": true,
	"while": true, "but": true, "so": true, "as": true,
}

const (
	interlockLabelMaxWords = 6
	interlockLabelMinWords = 2
)

// interlockLabelBefore derives the exact label a value is attached to: the
// last few words of the same clause, connector-trimmed and lowercased. ""
// means no defensible label — the value is skipped (a missed check is cheap; a
// false contradiction in front of the binder model is not).
func interlockLabelBefore(prefix string) string {
	prefix = strings.TrimRight(prefix, " \t")
	prefix = strings.TrimSuffix(prefix, ":")
	segments := interlockLabelSegmentRE.Split(prefix, -1)
	words := interlockLabelWordRE.FindAllString(strings.ToLower(segments[len(segments)-1]), -1)
	if len(words) > interlockLabelMaxWords {
		words = words[len(words)-interlockLabelMaxWords:]
	}
	for len(words) > 0 && interlockLabelTrailingStops[words[len(words)-1]] {
		words = words[:len(words)-1]
	}
	for len(words) > 0 && interlockLabelLeadingStops[words[0]] {
		words = words[1:]
	}
	if len(words) < interlockLabelMinWords {
		return ""
	}
	return strings.Join(words, " ")
}

// interlockMoneySuffixes in check order — longest first so "bn" wins over "b".
var interlockMoneySuffixes = []struct {
	suffix string
	factor float64
}{
	{"thousand", 1e3}, {"million", 1e6}, {"billion", 1e9},
	{"bn", 1e9}, {"k", 1e3}, {"m", 1e6}, {"b", 1e9},
}

// canonicalInterlockValue normalizes a matched value so "$2M", "$2 million",
// and "$2,000,000" compare equal — the scan flags disagreements, never
// formatting differences.
func canonicalInterlockValue(raw string) string {
	lower := strings.ToLower(strings.TrimSpace(raw))
	if strings.HasPrefix(lower, "$") {
		rest := strings.TrimSpace(strings.TrimPrefix(lower, "$"))
		factor := 1.0
		for _, candidate := range interlockMoneySuffixes {
			if strings.HasSuffix(rest, candidate.suffix) {
				factor = candidate.factor
				rest = strings.TrimSpace(strings.TrimSuffix(rest, candidate.suffix))
				break
			}
		}
		rest = strings.ReplaceAll(rest, ",", "")
		if value, err := strconv.ParseFloat(rest, 64); err == nil {
			return "$" + strconv.FormatFloat(value*factor, 'f', -1, 64)
		}
		return "$" + rest
	}
	if strings.HasSuffix(lower, "%") {
		rest := strings.TrimSpace(strings.TrimSuffix(lower, "%"))
		if value, err := strconv.ParseFloat(rest, 64); err == nil {
			return strconv.FormatFloat(value, 'f', -1, 64) + "%"
		}
		return rest + "%"
	}
	// Dates: lowercase, punctuation dropped, month words truncated to their
	// 3-letter stem so "January 2027" and "Jan. 2027" compare equal.
	fields := strings.Fields(strings.NewReplacer(",", " ", ".", " ").Replace(lower))
	for index, field := range fields {
		if len(field) > 3 && field[0] >= 'a' && field[0] <= 'z' {
			fields[index] = field[:3]
		}
	}
	return strings.Join(fields, " ")
}

// interlockCanonType buckets a canonical value so the scan never compares a
// percentage to a date under a shared label — incomparable types are not a
// contradiction.
func interlockCanonType(canon string) string {
	switch {
	case strings.HasPrefix(canon, "$"):
		return "money"
	case strings.HasSuffix(canon, "%"):
		return "percent"
	default:
		return "date"
	}
}

// interlockValueClaim is one (label, value) assertion extracted from one
// artifact body.
type interlockValueClaim struct {
	label string
	canon string
	raw   string
}

// extractInterlockClaims pulls every labeled value from an artifact body, line
// by line (a label never crosses a line break).
func extractInterlockClaims(body string) []interlockValueClaim {
	claims := []interlockValueClaim{}
	for _, line := range strings.Split(body, "\n") {
		for _, re := range interlockValueREs {
			for _, loc := range re.FindAllStringIndex(line, -1) {
				label := interlockLabelBefore(line[:loc[0]])
				if label == "" {
					continue
				}
				raw := strings.TrimSpace(line[loc[0]:loc[1]])
				claims = append(claims, interlockValueClaim{label: label, canon: canonicalInterlockValue(raw), raw: raw})
			}
		}
	}
	return claims
}

// artifactInterlockClass buckets an attached artifact for the vision-deck /
// rigor-companion rule (§4): "deck" is the narrative the room sees, "rigor" is
// the diligence document standing behind it. Everything else is "".
func artifactInterlockClass(entry meetingMemoryEntry) string {
	switch strings.ToLower(strings.TrimSpace(entry.Metadata["artifactContract"])) {
	case "deck_outline_v1":
		return "deck"
	case "research_brief_v2", "economics_scan_v1", "rights_map_v1", "update_memo_v1":
		return "rigor"
	}
	if artifactType(entry) == artifactTypeHTMLDeck {
		return "deck"
	}
	return ""
}

// packageInterlockFindings runs the deterministic pairwise consistency scan
// across everything attached to a package: exact-label value collisions
// between DIFFERENT artifacts, plus the explicit interlocks[] rules stored on
// attached artifacts whose counterpart is also attached. ok=false only when
// the package does not resolve; a clean scan returns an empty (non-nil) slice.
func (app *kanbanBoardApp) packageInterlockFindings(packageID string) ([]packageInterlockFinding, bool) {
	if app == nil || app.memory == nil {
		return nil, false
	}
	record, ok := app.venturePackageByID(packageID)
	if !ok {
		return nil, false
	}
	type attachedArtifact struct {
		entry meetingMemoryEntry
		title string
		class string
	}
	attached := []attachedArtifact{}
	attachedIndex := map[string]int{}
	for _, artifactID := range record.ArtifactIDs {
		entry, found := app.osArtifactByID(artifactID)
		if !found {
			continue
		}
		attachedIndex[entry.ID] = len(attached)
		attached = append(attached, attachedArtifact{
			entry: entry,
			title: firstNonEmptyString(entry.Metadata["title"], entry.Metadata["threadQuery"], "untitled artifact"),
			class: artifactInterlockClass(entry),
		})
	}

	findings := []packageInterlockFinding{}

	// (1) Exact-label collisions. Per artifact, (label, value) pairs are
	// deduplicated so a value repeated inside one document never inflates the
	// scan; a label then flags only when two DIFFERENT artifacts assert
	// different canonical values of the same value type — the honest,
	// conservative read of "same label, numbers disagree".
	type valueSighting struct {
		artifactIndex int
		raw           string
	}
	byLabel := map[string]map[string][]valueSighting{}
	for index, artifact := range attached {
		seen := map[string]bool{}
		for _, claim := range extractInterlockClaims(artifact.entry.Text) {
			key := claim.label + "\x00" + claim.canon
			if seen[key] {
				continue
			}
			seen[key] = true
			if byLabel[claim.label] == nil {
				byLabel[claim.label] = map[string][]valueSighting{}
			}
			byLabel[claim.label][claim.canon] = append(byLabel[claim.label][claim.canon], valueSighting{artifactIndex: index, raw: claim.raw})
		}
	}
	labels := make([]string, 0, len(byLabel))
	for label := range byLabel {
		labels = append(labels, label)
	}
	sort.Strings(labels)
	for _, label := range labels {
		values := byLabel[label]
		canons := make([]string, 0, len(values))
		for canon := range values {
			canons = append(canons, canon)
		}
		sort.Strings(canons)
		involved := map[int]bool{}
		kill := false
		for i := 0; i < len(canons); i++ {
			for j := i + 1; j < len(canons); j++ {
				if interlockCanonType(canons[i]) != interlockCanonType(canons[j]) {
					continue
				}
				for _, left := range values[canons[i]] {
					for _, right := range values[canons[j]] {
						if left.artifactIndex == right.artifactIndex {
							continue
						}
						involved[left.artifactIndex] = true
						involved[right.artifactIndex] = true
						if classes := map[string]bool{
							attached[left.artifactIndex].class:  true,
							attached[right.artifactIndex].class: true,
						}; classes["deck"] && classes["rigor"] {
							kill = true
						}
					}
				}
			}
		}
		if len(involved) == 0 {
			continue
		}
		parts := []string{}
		artifactIDs := []string{}
		seenArtifacts := map[int]bool{}
		for _, canon := range canons {
			names := []string{}
			for _, sighting := range values[canon] {
				if !involved[sighting.artifactIndex] {
					continue
				}
				names = append(names, attached[sighting.artifactIndex].title+" says "+sighting.raw)
				if !seenArtifacts[sighting.artifactIndex] {
					seenArtifacts[sighting.artifactIndex] = true
					artifactIDs = append(artifactIDs, attached[sighting.artifactIndex].entry.ID)
				}
			}
			if len(names) > 0 {
				parts = append(parts, strings.Join(names, "; "))
			}
		}
		sort.Strings(artifactIDs)
		severity := interlockSeverityMustResolve
		if kill {
			severity = interlockSeverityKill
		}
		findings = append(findings, packageInterlockFinding{
			Kind:        interlockKindValueCollision,
			Severity:    severity,
			Label:       label,
			Detail:      fmt.Sprintf("%q disagrees across the package: %s.", label, strings.Join(parts, " vs ")),
			ArtifactIDs: artifactIDs,
		})
	}

	// (2) Explicit interlock rules (the Wave 3 scaffold). A rule counts only
	// when both sides are attached to THIS package; reciprocal stamps of the
	// same rule collapse to one finding.
	seenRules := map[string]bool{}
	for _, artifact := range attached {
		for _, rule := range artifactInterlocks(artifact.entry) {
			counterpartIndex, attachedToo := attachedIndex[rule.WithArtifactID]
			if !attachedToo || rule.WithArtifactID == artifact.entry.ID {
				continue
			}
			pair := []string{artifact.entry.ID, rule.WithArtifactID}
			sort.Strings(pair)
			key := strings.Join(pair, "\x00") + "\x00" + strings.ToLower(rule.Rule)
			if seenRules[key] {
				continue
			}
			seenRules[key] = true
			severity := interlockSeverityMustResolve
			if classes := map[string]bool{
				artifact.class:                   true,
				attached[counterpartIndex].class: true,
			}; classes["deck"] && classes["rigor"] {
				severity = interlockSeverityKill
			}
			findings = append(findings, packageInterlockFinding{
				Kind:        interlockKindRule,
				Severity:    severity,
				Label:       rule.Rule,
				Detail:      fmt.Sprintf("interlock rule between %s and %s: %s. The binder must verify the rule holds across both artifacts.", artifact.title, attached[counterpartIndex].title, rule.Rule),
				ArtifactIDs: pair,
			})
		}
	}

	for index := range findings {
		findings[index].ID = fmt.Sprintf("IL-%d", index+1)
	}
	return findings, true
}

// renderInterlockMustResolve turns the pre-pass findings into the MUST-RESOLVE
// block appended to the binder's deliverable prompt. The IL-<n> status-line
// format is load-bearing: packageBinderInterlockSweep greps the produced body
// for it, and the format deliberately carries no em dash (the binder is
// client-facing copy under the em-dash law).
func renderInterlockMustResolve(packageName string, findings []packageInterlockFinding) string {
	var builder strings.Builder
	builder.WriteString("## INTERLOCK PRE-PASS (deterministic scan, zero model cost) — MUST-RESOLVE\n")
	if len(findings) == 0 {
		builder.WriteString("The engine's pairwise consistency scan across everything attached to " + packageName +
			" came back clean: no exact-label value collisions, no pending interlock rules. Say so in the Interlocks section (what was checked, nothing to resolve or disclose).")
		return builder.String()
	}
	builder.WriteString("The engine scanned every artifact attached to " + packageName + " pairwise before you started. Every finding below MUST appear in your Interlocks section as exactly one status line:\n")
	builder.WriteString("  \"IL-<n> RESOLVED: <how you reconciled the sources>\"  or\n")
	builder.WriteString("  \"IL-<n> DISCLOSED: <why it ships open>\"\n")
	builder.WriteString("A [KILL] finding is kill-condition-grade (the vision deck contradicts its rigor companion): it MUST be RESOLVED by reconciling the sources, never disclosed — the deterministic law sweep rejects the binder otherwise.\n")
	for _, finding := range findings {
		tag := "MUST-RESOLVE"
		if finding.Severity == interlockSeverityKill {
			tag = "KILL"
		}
		fmt.Fprintf(&builder, "- %s [%s] %s\n", finding.ID, tag, finding.Detail)
	}
	return strings.TrimRight(builder.String(), "\n")
}

// packageInterlockPrePass is the compiler's write half, run when the
// package_assembly deliverable prompt is assembled (toolPromptForThread):
// compute the findings, stamp them as interlockFindings JSON on the binder
// artifact (and on its goal parent — the binder of record the Deal Room
// reads), and return the MUST-RESOLVE prompt block. ok=false when the package
// does not resolve — the binder then degrades to the pre-compiler contract.
func (app *kanbanBoardApp) packageInterlockPrePass(packageID string, binderArtifactID string) (string, bool) {
	findings, ok := app.packageInterlockFindings(packageID)
	if !ok {
		return "", false
	}
	record, _ := app.venturePackageByID(packageID)
	if encoded, err := json.Marshal(findings); err == nil {
		stampIDs := []string{strings.TrimSpace(binderArtifactID)}
		if binder, found := app.osArtifactByID(binderArtifactID); found {
			if parentID := strings.TrimSpace(binder.Metadata["goalParentId"]); parentID != "" {
				stampIDs = append(stampIDs, parentID)
			}
		}
		for _, stampID := range stampIDs {
			if stampID == "" {
				continue
			}
			// Metadata-only stamp (the setOSArtifactInterlocks seam): bookkeeping,
			// never a version mint. Log-and-continue — a failed stamp must not
			// block the binder run.
			if _, _, stampErr := app.memory.updateOSArtifactMetadata(stampID, map[string]string{packageInterlockFindingsMetadataKey: string(encoded)}); stampErr != nil {
				log.Errorf("Failed to stamp interlockFindings on artifact %s: %v", stampID, stampErr)
			}
		}
	}
	return renderInterlockMustResolve(record.Name, findings), true
}

// decodePackageInterlockFindings reads the stamped JSON back; malformed or
// absent metadata reads as no findings (the artifactInterlocks discipline).
func decodePackageInterlockFindings(raw string) []packageInterlockFinding {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var decoded []packageInterlockFinding
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		return nil
	}
	return decoded
}

func isInterlockIDChar(c byte) bool {
	return c == '-' || (c >= '0' && c <= '9') || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
}

// interlockStatusLine finds the first body line carrying the finding id as a
// whole token (so IL-1 never matches inside IL-10). "" means the finding is
// missing from the binder.
func interlockStatusLine(body string, findingID string) string {
	for _, line := range strings.Split(body, "\n") {
		from := 0
		for {
			at := strings.Index(line[from:], findingID)
			if at < 0 {
				break
			}
			at += from
			end := at + len(findingID)
			startsClean := at == 0 || !isInterlockIDChar(line[at-1])
			endsClean := end >= len(line) || !isInterlockIDChar(line[end])
			if startsClean && endsClean {
				return line
			}
			from = end
		}
	}
	return ""
}

// packageBinderInterlockSweep is the deterministic enforcement half of the
// compiler: every stamped finding must appear in the produced binder with an
// explicit RESOLVED/DISCLOSED status line, and a kill-grade finding (vision
// deck vs rigor companion) must be RESOLVED. Reasons open with
// toolLawSweepPrefix so the engine stamps the verdict as mechanical and
// short-circuits to revise (goal_engine.go's law-sweep seam) — which is
// exactly how the existing gate machinery enforces the kill flag.
func packageBinderInterlockSweep(findings []packageInterlockFinding, body string) (string, bool) {
	for _, finding := range findings {
		line := interlockStatusLine(body, finding.ID)
		if line == "" {
			return fmt.Sprintf("%s (package_binder_v1): interlock finding %s (%s) is missing from the Interlocks section. Add \"%s RESOLVED: <how>\" or \"%s DISCLOSED: <why it ships open>\".",
				toolLawSweepPrefix, finding.ID, finding.Label, finding.ID, finding.ID), true
		}
		upper := strings.ToUpper(line)
		resolved := strings.Contains(upper, "RESOLVED") && !strings.Contains(upper, "UNRESOLVED")
		disclosed := strings.Contains(upper, "DISCLOSED")
		if !resolved && !disclosed {
			return fmt.Sprintf("%s (package_binder_v1): interlock finding %s carries no explicit status. Mark it \"%s RESOLVED: <how>\" or \"%s DISCLOSED: <why it ships open>\".",
				toolLawSweepPrefix, finding.ID, finding.ID, finding.ID), true
		}
		if finding.Severity == interlockSeverityKill && !resolved {
			return fmt.Sprintf("%s (package_binder_v1): interlock finding %s is kill-condition-grade: the vision deck and its rigor companion contradict each other (%s). A kill-grade contradiction never ships disclosed-open; reconcile the source artifacts and mark it \"%s RESOLVED: <how>\".",
				toolLawSweepPrefix, finding.ID, finding.Label, finding.ID), true
		}
	}
	return "", false
}

// packageBinderLawSweep resolves a produced binder body back to its stamped
// pre-pass findings (exact-text match over artifacts carrying the stamp — the
// engine's law sweep hands us the body alone) and runs the interlock sweep.
// No stamp anywhere → no enforcement: binders that predate the compiler, and
// packages that never ran the pre-pass, degrade gracefully.
func (app *kanbanBoardApp) packageBinderLawSweep(body string) (string, bool) {
	if app == nil || app.memory == nil || strings.TrimSpace(body) == "" {
		return "", false
	}
	for _, entry := range app.memory.entriesOfKind(meetingMemoryKindOSArtifact, 0) {
		raw := strings.TrimSpace(entry.Metadata[packageInterlockFindingsMetadataKey])
		if raw == "" || entry.Text != body {
			continue
		}
		return packageBinderInterlockSweep(decodePackageInterlockFindings(raw), body)
	}
	return "", false
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
