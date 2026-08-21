package main

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// Readiness runs every 30 seconds in production and shares the process with
// live RTP. Large decks must not turn it into a whole-store cloning job.
func TestReadinessCapabilitySnapshotDoesNotCloneLargeArtifactCorpus(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	app := newIsolatedKanbanBoardApp(t)
	kanbanApp = app
	t.Cleanup(func() { kanbanApp = previousApp })

	largeSharedBody := strings.Repeat("deck", 256*1024)
	app.memory.mu.Lock()
	for index := 0; index < 1500; index++ {
		app.memory.entries = append(app.memory.entries, meetingMemoryEntry{
			ID:        fmt.Sprintf("large-deck-%04d", index),
			Kind:      meetingMemoryKindOSArtifact,
			Text:      largeSharedBody,
			CreatedAt: time.Unix(int64(index+1), 0).UTC(),
			Metadata: map[string]string{
				"type":         "html_deck",
				"title":        fmt.Sprintf("Deck %d", index),
				"threadStatus": "complete",
			},
		})
	}
	app.memory.mu.Unlock()

	allocations := testing.AllocsPerRun(2, func() {
		capabilitySnapshot(time.Now().UTC())
	})
	if allocations > 15000 {
		t.Fatalf("capability snapshot allocated %.0f objects over a large artifact corpus; readiness is cloning/scanning product bodies", allocations)
	}
}
