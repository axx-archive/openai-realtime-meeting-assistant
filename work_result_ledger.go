package main

// Work results reach the ledger (Wave 8 D9). Every terminal agent run — the
// same funnel appendAgentRunLogEntry already owns for kind=run_log — also
// mints ONE kind=ledger_event carrying a work_result record: what was
// produced (artifactId + title), for whom (forEmail), and from which
// conversation (originThreadId). The entity ledger fold (ledgerState) keeps
// it as a record of entity "work_result", so recall can answer "what did our
// agents produce" from the deterministic fold instead of scraping run logs.
//
// The current-state view (ledgerCurrentStateView) groups only the four fact
// classes, so a work result never impersonates a decision or action item;
// the dedicated lane below (ledgerWorkResultLane / ledgerWorkResultAnswer)
// serves the "what did our agents produce" query shape.

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	ledgerEntityWorkResult = "work_result"
	ledgerOpWorkResult     = "work_result"
	// ledgerWorkResultMaxRecords bounds the lane / deterministic answer.
	ledgerWorkResultMaxRecords = 12
)

// workResultQueryMarkers route "what did our agents produce" questions to the
// work-result lane. Deliberately specific: ordinary status questions stay on
// the A5 current-state lane.
var workResultQueryMarkers = []string{
	"agents produce", "agent produce", "agents produced", "agent produced",
	"did our agents", "did the agents", "have our agents", "have the agents",
	"what did scout produce", "what has scout produced", "what did scout make", "what has scout made",
	"agent output", "agent outputs", "agent deliverable", "agent deliverables",
	"what got produced", "what was produced", "what has been produced",
	"work results", "work result", "recent deliverables", "latest deliverables",
	"what did the workers", "what have the workers",
}

func isWorkResultQuery(query string) bool {
	lower := strings.ToLower(strings.TrimSpace(query))
	if lower == "" {
		return false
	}
	for _, marker := range workResultQueryMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// appendWorkResultLedgerEvent mints the work_result ledger event for one
// terminal run. Idempotent per (artifact, status) via the deterministic id and
// the store seen-map, so a retried terminal twin never double-records.
func (app *kanbanBoardApp) appendWorkResultLedgerEvent(thread scoutAgentThread, artifact meetingMemoryEntry, status string) (meetingMemoryEntry, bool, error) {
	if app == nil || app.memory == nil {
		return meetingMemoryEntry{}, false, nil
	}
	artifactID := strings.TrimSpace(artifact.ID)
	if artifactID == "" {
		return meetingMemoryEntry{}, false, nil
	}
	status = strings.ToLower(strings.TrimSpace(status))
	if status == "" {
		status = "complete"
	}
	now := time.Now().UTC()
	nowStamp := now.Format(time.RFC3339)
	title := firstNonEmptyString(strings.TrimSpace(artifact.Metadata["title"]), strings.TrimSpace(artifact.Metadata["threadQuery"]), compactAssistantLine(thread.Query))
	title = trimForStorage(normalizeMemoryText(title), ledgerTitleLimit)
	if title == "" {
		title = "Untitled deliverable"
	}
	forEmail := normalizeAccountEmail(firstNonEmptyString(strings.TrimSpace(artifact.Metadata["requestedBy"]), strings.TrimSpace(artifact.Metadata["createdBy"])))
	originThreadID := strings.TrimSpace(firstNonEmptyString(thread.ID, artifact.Metadata["threadId"], artifact.Metadata["latestThreadRun"]))
	record := ledgerRecord{
		ID:        "ldg-work_result-" + artifactID,
		Entity:    ledgerEntityWorkResult,
		Title:     title,
		Status:    status,
		Owner:     forEmail,
		ValidFrom: nowStamp,
		Anchors:   []string{artifactID},
		UpdatedAt: nowStamp,
	}
	if originThreadID != "" {
		record.Aliases = []string{originThreadID}
	}
	event := ledgerEventPayload{
		Op:     ledgerOpWorkResult,
		Record: record,
		Reason: fmt.Sprintf("%s run %s", firstNonEmptyString(strings.TrimSpace(thread.Mode), "workflow"), status),
		At:     nowStamp,
	}
	extra := map[string]string{
		"kind":           ledgerEntityWorkResult,
		"artifactId":     artifactID,
		"title":          title,
		"forEmail":       forEmail,
		"originThreadId": originThreadID,
		"status":         status,
		"mode":           strings.TrimSpace(thread.Mode),
	}
	// Scope rides the artifact's own ACL stamps so a private deliverable's
	// existence never widens to the organization.
	for _, key := range []string{"visibility", "ownerEmail", "memberEmails", "tenantId", "roomId", "sittingId"} {
		if value := strings.TrimSpace(artifact.Metadata[key]); value != "" {
			extra[key] = value
		}
	}
	entries, err := ledgerEventEntries([]ledgerEventPayload{event}, nil, now, extra)
	if err != nil {
		return meetingMemoryEntry{}, false, err
	}
	entries[0].ID = "ledger-event-work-" + artifactID + "-" + status
	accepted, err := app.memory.appendLedgerEvents(entries)
	if err != nil {
		return meetingMemoryEntry{}, false, err
	}
	return entries[0], accepted > 0, nil
}

// workResultRecords returns the newest work_result records from the fold.
func (app *kanbanBoardApp) workResultRecords(limit int) []ledgerRecord {
	if app == nil || app.memory == nil || limit <= 0 {
		return nil
	}
	records := make([]ledgerRecord, 0, 8)
	for _, record := range app.memory.ledgerState() {
		if record.Entity == ledgerEntityWorkResult {
			records = append(records, record)
		}
	}
	sort.SliceStable(records, func(i, j int) bool {
		if records[i].UpdatedAt != records[j].UpdatedAt {
			return records[i].UpdatedAt > records[j].UpdatedAt
		}
		return records[i].ID < records[j].ID
	})
	if len(records) > limit {
		records = records[:limit]
	}
	return records
}

func writeWorkResultLines(builder *strings.Builder, records []ledgerRecord) {
	for _, record := range records {
		builder.WriteString("- ")
		builder.WriteString(record.Title)
		builder.WriteString(" | status=")
		builder.WriteString(firstNonEmptyString(record.Status, "complete"))
		if record.Owner != "" {
			builder.WriteString(" | for=")
			builder.WriteString(record.Owner)
		}
		if len(record.Anchors) > 0 {
			builder.WriteString(" | artifact=")
			builder.WriteString(record.Anchors[0])
		}
		if len(record.Aliases) > 0 {
			builder.WriteString(" | thread=")
			builder.WriteString(record.Aliases[0])
		}
		if record.UpdatedAt != "" {
			builder.WriteString(" | at=")
			builder.WriteString(record.UpdatedAt)
		}
		builder.WriteByte('\n')
	}
}

// ledgerWorkResultLane leads the model context for a "what did our agents
// produce" question with the folded work_result records. Empty otherwise.
func (app *kanbanBoardApp) ledgerWorkResultLane(query string, now time.Time) []meetingMemoryEntry {
	if !isWorkResultQuery(query) {
		return nil
	}
	records := app.workResultRecords(ledgerWorkResultMaxRecords)
	if len(records) == 0 {
		return nil
	}
	var builder strings.Builder
	builder.WriteString("Work results from the entity ledger (what the agents produced, for whom, from which conversation — folded in Go from terminal runs):\n")
	writeWorkResultLines(&builder, records)
	return []meetingMemoryEntry{{
		ID:        "ledger-work-results",
		Kind:      memoryContextKindLedgerState,
		Text:      strings.TrimRight(builder.String(), "\n"),
		CreatedAt: now,
		Metadata:  map[string]string{"source": "entity_ledger"},
	}}
}

// ledgerWorkResultAnswer is the deterministic model-outage fallback for the
// work-result query shape.
func (app *kanbanBoardApp) ledgerWorkResultAnswer(query string) (string, bool) {
	if app == nil || app.memory == nil || !isWorkResultQuery(query) {
		return "", false
	}
	records := app.workResultRecords(ledgerWorkResultMaxRecords)
	if len(records) == 0 {
		return "", false
	}
	var builder strings.Builder
	builder.WriteString("Recent work results:\n")
	writeWorkResultLines(&builder, records)
	builder.WriteString("(Composed from the entity ledger's work_result records; each artifact id opens the deliverable.)")
	return builder.String(), true
}
