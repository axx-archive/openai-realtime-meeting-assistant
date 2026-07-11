package main

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestMain defaults USAGE_LEDGER_PATH to a per-run temp directory so tests
// that exercise recording seams WITHOUT explicit isolation (raw
// newKanbanBoardApp constructions canonicalizing local board text, direct
// meetingMemoryStore transcript appends, async embedding maintainers) never
// write to the repo's data/usage/. Tests that assert ledger contents keep
// their own per-test isolation via ledgerTestDir/t.Setenv, which overrides
// this default and restores it afterwards.
func TestMain(m *testing.M) {
	os.Exit(func() int {
		if strings.TrimSpace(os.Getenv("USAGE_LEDGER_PATH")) == "" {
			if dir, err := os.MkdirTemp("", "usage-ledger-tests-"); err == nil {
				os.Setenv("USAGE_LEDGER_PATH", dir)
				defer os.RemoveAll(dir)
			}
		}
		return m.Run()
	}())
}

// ledgerTestDir points the ledger at a temp dir and returns it. Tests always
// override USAGE_LEDGER_PATH so nothing touches the repo data dir.
func ledgerTestDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("USAGE_LEDGER_PATH", dir)
	t.Setenv("USAGE_LEDGER_DISABLED", "")
	return dir
}

// readLedgerLines decodes every JSONL line of the given ledger file into raw
// maps, failing the test on malformed lines.
func readLedgerLines(t *testing.T, path string) []map[string]any {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open ledger %s: %v", path, err)
	}
	defer file.Close()
	var lines []map[string]any
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 1<<20), 1<<20)
	for scanner.Scan() {
		var row map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
			t.Fatalf("malformed JSONL line %q: %v", scanner.Text(), err)
		}
		lines = append(lines, row)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan ledger: %v", err)
	}
	return lines
}

func TestRecordLLMUsageAppendsEntryWithComputedCost(t *testing.T) {
	dir := ledgerTestDir(t)
	ts := time.Date(2026, time.July, 11, 12, 0, 0, 0, time.UTC)
	recordLLMUsage(llmUsageEntry{
		TS:                ts,
		Provider:          providerAnthropic,
		Model:             "claude-sonnet-5",
		Seat:              seatRouter,
		RoomID:            "room-1",
		InputTokens:       1_000_000,
		CachedInputTokens: 500_000,
		OutputTokens:      100_000,
		DurationMS:        850,
	})

	rows := readLedgerLines(t, filepath.Join(dir, "usage-2026-07-11.jsonl"))
	if len(rows) != 1 {
		t.Fatalf("expected 1 ledger row, got %d", len(rows))
	}
	row := rows[0]
	if row["provider"] != "anthropic" || row["model"] != "claude-sonnet-5" || row["seat"] != "router" {
		t.Fatalf("wrong identity fields: %v", row)
	}
	if row["room_id"] != "room-1" {
		t.Fatalf("room_id not persisted: %v", row)
	}
	// Intro pricing (pre 2026-09-01): 1M in = $2, 0.5M cached = $0.10, 0.1M out = $1.
	wantCost := 2.0 + 0.10 + 1.0
	if got := row["est_cost_usd"].(float64); !floatClose(got, wantCost) {
		t.Fatalf("est_cost_usd = %v, want %v", got, wantCost)
	}
	if _, present := row["price_missing"]; present {
		t.Fatalf("price_missing should be omitted for a priced model: %v", row)
	}
}

func TestRecordLLMUsageDefaultsTimestampAndSeat(t *testing.T) {
	dir := ledgerTestDir(t)
	fixed := time.Date(2026, time.July, 12, 3, 4, 5, 0, time.UTC)
	prevNow := usageLedgerNow
	usageLedgerNow = func() time.Time { return fixed }
	defer func() { usageLedgerNow = prevNow }()

	recordLLMUsage(llmUsageEntry{Provider: providerOpenAI, Model: "gpt-5.5", OutputTokens: 10})

	rows := readLedgerLines(t, filepath.Join(dir, "usage-2026-07-12.jsonl"))
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0]["seat"] != seatUntagged {
		t.Fatalf("empty seat should record as %q, got %v", seatUntagged, rows[0]["seat"])
	}
	if !strings.HasPrefix(rows[0]["ts"].(string), "2026-07-12T03:04:05") {
		t.Fatalf("ts not defaulted to now: %v", rows[0]["ts"])
	}
}

func TestRecordLLMUsageRespectsCallerCostAndFlagsUnknownModels(t *testing.T) {
	dir := ledgerTestDir(t)
	ts := time.Date(2026, time.July, 11, 0, 0, 0, 0, time.UTC)

	// Caller-supplied cost is never recomputed.
	recordLLMUsage(llmUsageEntry{TS: ts, Provider: providerOpenAI, Model: "gpt-5.5", Seat: seatBrain, EstCostUSD: 1.23})
	// Unknown model: cost stays 0, price_missing stamps on.
	recordLLMUsage(llmUsageEntry{TS: ts, Provider: providerOpenAI, Model: "gpt-9.9-typo", Seat: seatBrain, InputTokens: 1000})

	rows := readLedgerLines(t, filepath.Join(dir, "usage-2026-07-11.jsonl"))
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	if got := rows[0]["est_cost_usd"].(float64); !floatClose(got, 1.23) {
		t.Fatalf("caller cost overwritten: %v", got)
	}
	if _, present := rows[0]["price_missing"]; present {
		t.Fatalf("priced call must not flag price_missing: %v", rows[0])
	}
	if rows[1]["price_missing"] != true {
		t.Fatalf("unknown model must flag price_missing: %v", rows[1])
	}
	if _, present := rows[1]["est_cost_usd"]; present {
		t.Fatalf("unknown model must bill 0 (omitted): %v", rows[1])
	}
}

func TestRecordLLMUsageDailyRotationByEntryTimestamp(t *testing.T) {
	dir := ledgerTestDir(t)
	day1 := time.Date(2026, time.July, 11, 23, 59, 0, 0, time.UTC)
	day2 := time.Date(2026, time.July, 12, 0, 1, 0, 0, time.UTC)
	recordLLMUsage(llmUsageEntry{TS: day1, Provider: providerOpenAI, Model: "gpt-5.5", Seat: seatBrain})
	recordLLMUsage(llmUsageEntry{TS: day2, Provider: providerOpenAI, Model: "gpt-5.5", Seat: seatBrain})

	if rows := readLedgerLines(t, filepath.Join(dir, "usage-2026-07-11.jsonl")); len(rows) != 1 {
		t.Fatalf("day-1 file: expected 1 row, got %d", len(rows))
	}
	if rows := readLedgerLines(t, filepath.Join(dir, "usage-2026-07-12.jsonl")); len(rows) != 1 {
		t.Fatalf("day-2 file: expected 1 row, got %d", len(rows))
	}
}

func TestUsageLedgerKillSwitchDisablesAllRecording(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("USAGE_LEDGER_PATH", dir)
	t.Setenv("USAGE_LEDGER_DISABLED", "1")
	before := usageLedgerDroppedWrites()

	recordLLMUsage(llmUsageEntry{Provider: providerOpenAI, Model: "gpt-5.5", Seat: seatBrain})
	recordEvalEvent(seatRouter, evalKindRouterOutcome, map[string]any{"verdict": "workflow"})
	recordProposalEvent(proposalEventMinted, "prop-1", nil)
	recordWorkflowRun(workflowRunEntry{WorkflowID: "followup_sweep", TriggerSurface: triggerSurfaceScheduler, Outcome: workflowOutcomeLaunched})

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("disabled ledger must write nothing, found %d files", len(entries))
	}
	if got := usageLedgerDroppedWrites(); got != before {
		t.Fatalf("disabled ledger must not count drops: %d -> %d", before, got)
	}
}

func TestRecordLLMUsageNeverFailsCallerOnWriteError(t *testing.T) {
	// Point the ledger DIR at an existing FILE so MkdirAll fails.
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatalf("write blocker: %v", err)
	}
	t.Setenv("USAGE_LEDGER_PATH", blocker)
	t.Setenv("USAGE_LEDGER_DISABLED", "")

	before := usageLedgerDroppedWrites()
	recordLLMUsage(llmUsageEntry{Provider: providerOpenAI, Model: "gpt-5.5", Seat: seatBrain}) // must not panic or error
	recordEvalEvent(seatBoard, evalKindParseFailure, nil)
	if got := usageLedgerDroppedWrites(); got != before+2 {
		t.Fatalf("dropped-writes counter = %d, want %d", got, before+2)
	}
}

func TestRecordLLMUsageConcurrentAppendsProduceIntactJSONL(t *testing.T) {
	dir := ledgerTestDir(t)
	ts := time.Date(2026, time.July, 11, 9, 0, 0, 0, time.UTC)
	const workers, perWorker = 16, 25

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				recordLLMUsage(llmUsageEntry{
					TS: ts, Provider: providerOpenAI, Model: "gpt-5.6-luna",
					Seat: seatBrain, InputTokens: 100, OutputTokens: 10,
				})
			}
		}()
	}
	wg.Wait()

	rows := readLedgerLines(t, filepath.Join(dir, "usage-2026-07-11.jsonl"))
	if len(rows) != workers*perWorker {
		t.Fatalf("expected %d intact rows, got %d", workers*perWorker, len(rows))
	}
	for i, row := range rows {
		if row["model"] != "gpt-5.6-luna" || row["seat"] != "brain" {
			t.Fatalf("row %d corrupted: %v", i, row)
		}
	}
}

func TestRecordEvalEventShape(t *testing.T) {
	dir := ledgerTestDir(t)
	fixed := time.Date(2026, time.July, 11, 10, 0, 0, 0, time.UTC)
	prevNow := usageLedgerNow
	usageLedgerNow = func() time.Time { return fixed }
	defer func() { usageLedgerNow = prevNow }()

	recordEvalEvent(seatBoard, evalKindBoardOpFidelity, map[string]any{"op_count": 4, "error_count": 1})
	recordEvalEvent(evalLaneTranscript, evalKindTranscriptSegment, map[string]any{"status": "failed"})

	rows := readLedgerLines(t, filepath.Join(dir, "eval-2026-07-11.jsonl"))
	if len(rows) != 2 {
		t.Fatalf("expected 2 eval rows, got %d", len(rows))
	}
	if rows[0]["type"] != telemetryTypeEval || rows[0]["lane"] != "board" || rows[0]["kind"] != "board_op_fidelity" {
		t.Fatalf("eval row 0 shape wrong: %v", rows[0])
	}
	fields := rows[0]["fields"].(map[string]any)
	if fields["op_count"].(float64) != 4 || fields["error_count"].(float64) != 1 {
		t.Fatalf("eval fields not persisted: %v", fields)
	}
	if rows[1]["lane"] != evalLaneTranscript {
		t.Fatalf("reserved transcript lane not persisted: %v", rows[1])
	}
}

func TestRecordProposalEventStampsProposalID(t *testing.T) {
	dir := ledgerTestDir(t)
	fixed := time.Date(2026, time.July, 11, 10, 0, 0, 0, time.UTC)
	prevNow := usageLedgerNow
	usageLedgerNow = func() time.Time { return fixed }
	defer func() { usageLedgerNow = prevNow }()

	recordProposalEvent(proposalEventMinted, "prop-42", map[string]any{
		"source":                proposalSourceBoardWorker,
		"from_brain_id":         "brain-7",
		"through_transcript_id": "tr-99",
	})
	recordProposalEvent(proposalEventResolved, "prop-42", map[string]any{"resolution": "confirmed"})

	rows := readLedgerLines(t, filepath.Join(dir, "eval-2026-07-11.jsonl"))
	if len(rows) != 2 {
		t.Fatalf("expected 2 proposal rows, got %d", len(rows))
	}
	for i, row := range rows {
		if row["type"] != telemetryTypeProposal {
			t.Fatalf("row %d type = %v", i, row["type"])
		}
		if row["fields"].(map[string]any)["proposal_id"] != "prop-42" {
			t.Fatalf("row %d missing proposal_id: %v", i, row)
		}
	}
	minted := rows[0]["fields"].(map[string]any)
	if minted["source"] != "board_worker" || minted["through_transcript_id"] != "tr-99" {
		t.Fatalf("lineage fields not persisted: %v", minted)
	}
	if rows[0]["kind"] != proposalEventMinted || rows[1]["kind"] != proposalEventResolved {
		t.Fatalf("kinds wrong: %v / %v", rows[0]["kind"], rows[1]["kind"])
	}
}

func TestRecordWorkflowRunProvenance(t *testing.T) {
	dir := ledgerTestDir(t)
	fixed := time.Date(2026, time.July, 11, 10, 0, 0, 0, time.UTC)
	prevNow := usageLedgerNow
	usageLedgerNow = func() time.Time { return fixed }
	defer func() { usageLedgerNow = prevNow }()

	recordWorkflowRun(workflowRunEntry{
		WorkflowID:     "meeting_prep",
		TriggerSurface: triggerSurfaceChatRouter,
		Proposer:       "scout",
		Approver:       "aj",
		Lane:           "standard",
		Seats:          []string{seatOrchestrator, seatReview},
		Outcome:        workflowOutcomeLaunched,
		ProposalID:     "prop-42",
		ThreadID:       "thread-1",
	})

	rows := readLedgerLines(t, filepath.Join(dir, "eval-2026-07-11.jsonl"))
	if len(rows) != 1 {
		t.Fatalf("expected 1 workflow row, got %d", len(rows))
	}
	row := rows[0]
	if row["type"] != telemetryTypeWorkflowRun || row["kind"] != workflowOutcomeLaunched {
		t.Fatalf("workflow row shape wrong: %v", row)
	}
	run := row["fields"].(map[string]any)["run"].(map[string]any)
	if run["workflow_id"] != "meeting_prep" || run["trigger_surface"] != "chat_router" ||
		run["approver"] != "aj" || run["proposal_id"] != "prop-42" {
		t.Fatalf("provenance fields not persisted: %v", run)
	}
	seats := run["seats"].([]any)
	if len(seats) != 2 || seats[0] != "orchestrator" || seats[1] != "review" {
		t.Fatalf("seats not persisted: %v", seats)
	}
}

func TestUsageLedgerDefaultPathSitsBesideMemoryStore(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(dataDir, "memory.jsonl"))
	t.Setenv("USAGE_LEDGER_PATH", "")
	if got, want := usageLedgerDir(), filepath.Join(dataDir, "usage"); got != want {
		t.Fatalf("usageLedgerDir() = %q, want %q", got, want)
	}
	t.Setenv("USAGE_LEDGER_PATH", "/custom/usage")
	if got := usageLedgerDir(); got != "/custom/usage" {
		t.Fatalf("USAGE_LEDGER_PATH override ignored: %q", got)
	}
}

func TestSeatVocabularyIsCompleteUniqueAndWellFormed(t *testing.T) {
	if len(allLLMSeats) != 34 {
		t.Fatalf("seat vocabulary drifted: %d seats, want 34 (update the frozen contract deliberately)", len(allLLMSeats))
	}
	seen := map[string]bool{}
	for _, seat := range allLLMSeats {
		if seat == "" {
			t.Fatalf("empty seat name in allLLMSeats")
		}
		if seen[seat] {
			t.Fatalf("duplicate seat %q", seat)
		}
		seen[seat] = true
		if seat != strings.ToLower(seat) || strings.ContainsAny(seat, " -") {
			t.Fatalf("seat %q must be lowercase snake_case", seat)
		}
	}
	// Spot-check the constants downstream agents will grep for.
	for _, want := range []string{
		seatOrchestrator, seatDeliverable, seatReview, seatFallback, seatChat,
		seatRouter, seatMemoryQA, seatVoiceRecall, seatFollowup, seatAttachments,
		seatNarrative, seatTaste, seatHouseStyle, seatBrain, seatBoard,
		seatSuggestion, seatDecisionLedger, seatEntityLedger, seatMeetingDigest,
		seatCompanyDigest, seatMissionIntel, seatSlop, seatRecallMapReduce,
		seatAgentThreadText, seatVoiceRoom, seatVoicePrivate, seatTranscriptionLane,
		seatTranscriptionSession, seatEmbeddings, seatImages, seatCodex,
		seatGoalEngine, seatGoalReview, seatUntagged,
	} {
		if !seen[want] {
			t.Fatalf("seat constant %q missing from allLLMSeats", want)
		}
	}
}

func floatClose(a, b float64) bool {
	diff := a - b
	if diff < 0 {
		diff = -diff
	}
	return diff < 1e-9
}
