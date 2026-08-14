package main

import (
	"errors"
	"sort"
	"strings"
)

var ErrBoardRetired = errors.New("the Kanban Board is retired; use conversation-backed Work")

const boardRetirementInventoryVersion = "board-retirement-inventory-v1"

type boardRetirementInventory struct {
	Version     string                          `json:"version"`
	BoardDigest string                          `json:"boardDigest"`
	CardCount   int                             `json:"cardCount"`
	Entries     []boardRetirementInventoryEntry `json:"entries"`
}

type boardRetirementInventoryEntry struct {
	CardID               string   `json:"cardId"`
	CardDigest           string   `json:"cardDigest"`
	Status               string   `json:"status"`
	Disposition          string   `json:"disposition"`
	SuccessorArtifactIDs []string `json:"successorArtifactIds,omitempty"`
}

// retiredBoardInventory is a body-free, deterministic migration ledger. It
// proves every archived card is accounted for without reopening Board writes
// or copying titles/notes into a new store. Exact historical artifact links are
// carried forward; everything else is honestly retained as legacy-only until a
// successor Work record exists.
func (app *kanbanBoardApp) retiredBoardInventory() boardRetirementInventory {
	inventory := boardRetirementInventory{Version: boardRetirementInventoryVersion}
	if app == nil {
		return inventory
	}

	successors := map[string][]string{}
	if app.memory != nil {
		for _, artifact := range app.memory.entriesOfKind(meetingMemoryKindOSArtifact, 0) {
			cardID := strings.TrimSpace(artifact.Metadata["boardCardId"])
			if cardID == "" || strings.TrimSpace(artifact.ID) == "" {
				continue
			}
			successors[cardID] = append(successors[cardID], artifact.ID)
		}
	}

	for _, card := range app.snapshotState().Cards {
		raw, _ := canonicalJSON(card)
		artifactIDs := append([]string(nil), successors[card.ID]...)
		sort.Strings(artifactIDs)
		artifactIDs = compactSortedStrings(artifactIDs)
		disposition := "legacy_only"
		if len(artifactIDs) > 0 {
			disposition = "projected_artifacts"
		}
		inventory.Entries = append(inventory.Entries, boardRetirementInventoryEntry{
			CardID: card.ID, CardDigest: temporalDigest(string(raw)), Status: string(card.Status),
			Disposition: disposition, SuccessorArtifactIDs: artifactIDs,
		})
	}
	sort.Slice(inventory.Entries, func(i, j int) bool { return inventory.Entries[i].CardID < inventory.Entries[j].CardID })
	inventory.CardCount = len(inventory.Entries)
	raw, _ := canonicalJSON(inventory.Entries)
	inventory.BoardDigest = temporalDigest(string(raw))
	return inventory
}

func compactSortedStrings(values []string) []string {
	if len(values) < 2 {
		return values
	}
	result := values[:0]
	for _, value := range values {
		if len(result) == 0 || result[len(result)-1] != value {
			result = append(result, value)
		}
	}
	return result
}

var retiredBoardMutationTools = map[string]bool{
	"create_ticket": true, "move_ticket": true, "add_tags": true, "add_key_date": true,
	"remove_key_dates": true, "update_ticket": true, "delete_ticket": true, "undo_delete_ticket": true,
}

func boardMutationToolRetired(name string) bool {
	return retiredBoardMutationTools[strings.TrimSpace(name)]
}

// withoutRetiredBoardTools removes the old filing-system verbs from every
// model surface. The returned non-Board tools retain their stable contracts;
// direct Board reads remain available only for historical migration.
func withoutRetiredBoardTools(tools []map[string]any) []map[string]any {
	result := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		name := asString(tool["name"])
		if boardMutationToolRetired(name) {
			continue
		}
		if name == "control_app" {
			if parameters, ok := tool["parameters"].(map[string]any); ok {
				if properties, ok := parameters["properties"].(map[string]any); ok {
					removeBoardEnum(properties["tool"])
					removeBoardArrayEnum(properties["also_open"])
				}
			}
		}
		if parameters, ok := tool["parameters"].(map[string]any); ok {
			if properties, ok := parameters["properties"].(map[string]any); ok {
				if name == "propose_codex_task" {
					delete(properties, "card_id")
				}
				if name == "attach_to_package" {
					removeEnumValue(properties["ref_type"], "card")
				}
			}
		}
		removeRetiredBoardEnumValues(tool)
		result = append(result, tool)
	}
	return result
}

func removeRetiredBoardEnumValues(value any) {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if key == "enum" {
				if values, ok := child.([]string); ok {
					typed[key] = stringsWithout(values, "board")
				}
				continue
			}
			removeRetiredBoardEnumValues(child)
		}
	case []any:
		for _, child := range typed {
			removeRetiredBoardEnumValues(child)
		}
	}
}

func removeBoardEnum(value any) {
	removeEnumValue(value, "board")
}

func removeEnumValue(value any, removed string) {
	property, ok := value.(map[string]any)
	if !ok {
		return
	}
	values, ok := property["enum"].([]string)
	if !ok {
		return
	}
	property["enum"] = stringsWithout(values, removed)
}

func removeBoardArrayEnum(value any) {
	property, ok := value.(map[string]any)
	if !ok {
		return
	}
	items, ok := property["items"].(map[string]any)
	if !ok {
		return
	}
	removeBoardEnum(items)
}

func stringsWithout(values []string, removed string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value != removed {
			result = append(result, value)
		}
	}
	return result
}
