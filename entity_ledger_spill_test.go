package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// Wave 8 D6: what the overflow caps evict is reported by the merge and
// persisted as ledger_event metadata spilled[] instead of being dropped.
func TestLedgerAnchorSpillPersistsOnEvent(t *testing.T) {
	record := ledgerRecord{ID: "ldg-topic-1", Entity: ledgerEntityTopic, Title: "Packaging pilot", Status: ledgerStatusActive}
	for index := 0; index < ledgerAnchorCap; index++ {
		record.Anchors = append(record.Anchors, fmt.Sprintf("tx-primary-%02d", index))
		record.MeetingIDs = append(record.MeetingIDs, fmt.Sprintf("meeting-primary-%02d", index))
	}
	for index := 0; index < ledgerProvenanceOverflowCap; index++ {
		record.AnchorsOverflow = append(record.AnchorsOverflow, fmt.Sprintf("tx-overflow-%02d", index))
		record.MeetingIDsOverflow = append(record.MeetingIDsOverflow, fmt.Sprintf("meeting-overflow-%02d", index))
	}
	fact := ledgerFact{Entity: ledgerEntityTopic, Title: "Packaging pilot", Status: "active", Anchor: "tx-new", MeetingID: "meeting-new"}

	merged, changed, spilled := mergeLedgerFactSpill(record, fact, "2026-09-01T10:00:00Z")
	if !changed {
		t.Fatal("a new anchor must change the record")
	}
	if len(merged.Anchors) != ledgerAnchorCap || merged.Anchors[len(merged.Anchors)-1] != "tx-new" {
		t.Fatalf("primary anchors=%v", merged.Anchors)
	}
	if len(merged.AnchorsOverflow) != ledgerProvenanceOverflowCap || merged.AnchorsOverflow[len(merged.AnchorsOverflow)-1] != "tx-primary-00" {
		t.Fatalf("overflow anchors=%v, want the primary eviction appended at cap", merged.AnchorsOverflow)
	}
	if len(spilled) != 2 || spilled[0] != "anchor:tx-overflow-00" || spilled[1] != "meeting:meeting-overflow-00" {
		t.Fatalf("spilled=%v, want the two overflow evictions", spilled)
	}
	// the two-value wrapper stays byte-compatible for existing callers.
	if wrapped, wrappedChanged := mergeLedgerFact(record, fact, "2026-09-01T10:00:00Z"); !wrappedChanged || len(wrapped.Anchors) != len(merged.Anchors) {
		t.Fatal("mergeLedgerFact wrapper diverged from mergeLedgerFactSpill")
	}
	// under cap: nothing spills.
	if _, _, none := mergeLedgerFactSpill(ledgerRecord{ID: "r", Entity: ledgerEntityTopic, Title: "x"}, fact, "2026-09-01T10:00:00Z"); len(none) != 0 {
		t.Fatalf("under-cap merge spilled %v", none)
	}

	now := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	entries, err := ledgerEventEntries([]ledgerEventPayload{{Op: ledgerOpUpdate, Record: merged, Reason: "test", At: "2026-09-01T10:00:00Z", Spilled: spilled}}, nil, now, nil)
	if err != nil || len(entries) != 1 {
		t.Fatalf("ledgerEventEntries: entries=%d err=%v", len(entries), err)
	}
	if entries[0].Metadata["spilled"] != strings.Join(spilled, ",") || entries[0].Metadata["visibility"] != "organization" {
		t.Fatalf("event metadata=%v", entries[0].Metadata)
	}
	var payload ledgerEventPayload
	if err := json.Unmarshal([]byte(entries[0].Text), &payload); err != nil || len(payload.Spilled) != 2 {
		t.Fatalf("event payload must carry spilled[]: %+v err=%v", payload, err)
	}
	// the spill survives a store round-trip and never mutates the fold's record identity.
	app := newIsolatedKanbanBoardApp(t)
	if _, err := app.memory.appendLedgerEvents(entries); err != nil {
		t.Fatalf("append: %v", err)
	}
	stored := app.memory.entriesOfKind(meetingMemoryKindLedgerEvent, 0)
	if len(stored) != 1 || !strings.Contains(stored[0].Text, "tx-overflow-00") {
		t.Fatalf("stored event lost the spill: %+v", stored)
	}
	if folded := app.memory.ledgerState()["ldg-topic-1"]; folded.Title != "Packaging pilot" || len(folded.AnchorsOverflow) != ledgerProvenanceOverflowCap {
		t.Fatalf("fold changed by the spill: %+v", folded)
	}
}
