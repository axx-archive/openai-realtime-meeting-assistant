package main

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
)

var errPriorWorkFeedbackChanged = errors.New("prior work feedback changed; retry with current evidence")

func (store *meetingMemoryStore) updateOSArtifactWithMetadataIfHeaderMetadataAndFeedbackMatch(expected ArtifactAuthorizationHeader, expectedMetadata map[string]string, id, title, text, updatedBy string, metadata map[string]string, fence *workFeedbackTerminalFence) (meetingMemoryEntry, bool, error) {
	return store.updateOSArtifactWithMetadataExpected(&expected, expectedMetadata, "", "", "", id, title, text, updatedBy, metadata, fence)
}

// A transient compare-and-swap input, never a new permission or a persisted
// source body. Capture before authorization and compare under the result write
// lock so a correction cannot race between the final check and publication.
type workFeedbackTerminalFence struct {
	IDs    []string
	Digest string
}

func workFeedbackUsedDigests(entries []meetingMemoryEntry) string {
	digests := map[string]string{}
	for _, entry := range entries {
		if entry.Kind == workFeedbackEvidenceKind {
			digests[entry.ID] = entry.BodyDigest
		}
	}
	raw, _ := json.Marshal(digests)
	return string(raw)
}

func (app *kanbanBoardApp) prepareWorkFeedbackTerminalFence(ctx context.Context, thread scoutAgentThread, metadata map[string]string) (*workFeedbackTerminalFence, error) {
	raw := strings.TrimSpace(metadata["workFeedbackEvidence"])
	if raw == "" {
		return nil, nil
	}
	var citations []workFeedbackEvidenceCitation
	var used map[string]string
	if json.Unmarshal([]byte(raw), &citations) != nil || len(citations) == 0 || len(citations) > 3 ||
		json.Unmarshal([]byte(metadata["workFeedbackEvidenceDigests"]), &used) != nil || len(used) != len(citations) {
		return nil, errPriorWorkFeedbackChanged
	}
	ids := map[string]bool{}
	for _, citation := range citations {
		for _, id := range []string{citation.RootID, citation.Result.ArtifactID, citation.AcceptanceID, citation.OutcomeID} {
			if id == "" {
				return nil, errPriorWorkFeedbackChanged
			}
			ids[id] = true
		}
	}
	if id := strings.TrimSpace(thread.Artifact.Metadata["originId"]); id != "" {
		ids[id] = true
	}
	fence := &workFeedbackTerminalFence{}
	for id := range ids {
		fence.IDs = append(fence.IDs, id)
	}
	sort.Strings(fence.IDs)
	app.memory.mu.RLock()
	fence.Digest = app.memory.workFeedbackTerminalFingerprintLocked(fence.IDs)
	app.memory.mu.RUnlock()
	if fence.Digest == "" {
		return nil, errPriorWorkFeedbackChanged
	}
	current := map[string]meetingMemoryEntry{}
	for _, entry := range app.priorWorkFeedbackContext(ctx, thread) {
		current[entry.ID] = entry
	}
	for _, citation := range citations {
		entry, found := current[citation.ID]
		if !found || used[citation.ID] == "" || entry.BodyDigest != used[citation.ID] {
			return nil, errPriorWorkFeedbackChanged
		}
		var exact workFeedbackEvidenceCitation
		if json.Unmarshal([]byte(entry.Metadata["workFeedbackCitation"]), &exact) != nil || exact != citation {
			return nil, errPriorWorkFeedbackChanged
		}
	}
	return fence, nil
}

// Match the bounded review window used at context admission. Besides pinning
// exact source entries, this detects a newly appended review/outcome that
// supersedes a used event. No full-store scan or body copy is required.
func (store *meetingMemoryStore) workFeedbackTerminalFingerprintLocked(ids []string) string {
	parts := make([]any, 0, len(ids)+256)
	for _, id := range ids {
		index, exists := store.entryIndexByID[id]
		if !exists || index < 0 || index >= len(store.entries) || store.entries[index].ID != id {
			return ""
		}
		entry := store.entries[index]
		parts = append(parts, struct {
			ID, Kind, Body string
			Metadata       map[string]string
		}{entry.ID, entry.Kind, sha256Hex([]byte(entry.Text)), entry.Metadata})
	}
	visited, reviews := 0, 0
	for index := len(store.entries) - 1; index >= 0 && visited < 10000 && reviews < 256; index-- {
		visited++
		entry := store.entries[index]
		if entry.Kind != meetingMemoryKindWorkReview {
			continue
		}
		reviews++
		parts = append(parts, struct {
			ID, Body string
			Metadata map[string]string
		}{entry.ID, sha256Hex([]byte(entry.Text)), entry.Metadata})
	}
	raw, err := json.Marshal(parts)
	if err != nil {
		return ""
	}
	return sha256Hex(raw)
}

func (store *meetingMemoryStore) workFeedbackTerminalFenceCurrentLocked(fence *workFeedbackTerminalFence) bool {
	return fence != nil && fence.Digest != "" && store.workFeedbackTerminalFingerprintLocked(fence.IDs) == fence.Digest
}

func clearWorkFeedbackResultMetadata(metadata map[string]string) {
	metadata["workFeedbackEvidence"] = ""
	metadata["workFeedbackEvidenceDigests"] = ""
	metadata["workFeedbackEvidenceSourceVersion"] = ""
}
