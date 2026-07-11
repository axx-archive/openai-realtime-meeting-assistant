package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCanonicalizeDomainTermsCorrectsKnownMishearings(t *testing.T) {
	t.Setenv("USAGE_LEDGER_PATH", t.TempDir())
	got := canonicalizeDomainTerms("Suit Barn needs Web RTC support for H E V C over R T P.")
	for _, want := range []string{"Boot Barn", "WebRTC", "HEVC", "RTP"} {
		if !strings.Contains(got, want) {
			t.Fatalf("canonicalized text %q does not contain %q", got, want)
		}
	}
	if strings.Contains(got, "Suit Barn") {
		t.Fatalf("canonicalized text still contains Suit Barn: %q", got)
	}
}

func TestCardToolsCanonicalizeDomainTerms(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", t.TempDir()+"/memory.jsonl")
	t.Setenv("KANBAN_BOARD_PATH", t.TempDir()+"/board.json")

	app := newKanbanBoardApp()
	result, changed, err := app.createTicket(map[string]any{
		"title":  "Suit Barn launch",
		"notes":  "Open AI Web RTC follow-up for H E V C",
		"owner":  "AJ",
		"tags":   []any{"suit barn", "web rtc"},
		"status": "Backlog",
	})
	if err != nil {
		t.Fatalf("createTicket: %v", err)
	}
	if !changed {
		t.Fatal("createTicket changed=false, want true")
	}

	card := result["card"].(kanbanCard)
	if card.Title != "Boot Barn launch" {
		t.Fatalf("title=%q, want Boot Barn launch", card.Title)
	}
	if !strings.Contains(card.Notes, "OpenAI WebRTC") || !strings.Contains(card.Notes, "HEVC") {
		t.Fatalf("notes did not preserve canonical technical terms: %q", card.Notes)
	}
	if got, want := card.Tags, []string{"Boot Barn", "WebRTC"}; !sameStringSlice(got, want) {
		t.Fatalf("tags=%v, want %v", got, want)
	}
}

// W1-17 phase 1: the static vocabulary closes the live-client + model-family
// gaps, and the additions ride the realtime transcription prompt's keyword
// bias.
func TestMeetingDomainVocabularyPhase1GapsClosed(t *testing.T) {
	vocab := map[string]bool{}
	for _, term := range meetingDomainVocabulary {
		vocab[term] = true
	}
	wanted := []string{
		"StationTenn", "fiscal.ai", "Fable", "Claude Fable", "Sonnet", "Haiku",
		"Codex", "Resend", "Luna", "Terra", "Sol", "BonfireOS", "Bonfire OS",
	}
	for _, want := range wanted {
		if !vocab[want] {
			t.Errorf("meetingDomainVocabulary missing phase-1 term %q", want)
		}
	}

	prompt := realtimeTranscriptionPrompt()
	for _, want := range []string{"StationTenn", "fiscal.ai", "BonfireOS", "Claude Fable"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("realtimeTranscriptionPrompt missing keyword %q", want)
		}
	}
}

// W1-17: correction regexes exist ONLY for the compound spellings an unbiased
// STT can never produce (StationTenn, fiscal.ai) — and they never touch text
// that already reads canonically or innocent near-misses.
func TestCanonicalizeDomainTermsStationTennAndFiscalAI(t *testing.T) {
	t.Setenv("USAGE_LEDGER_PATH", t.TempDir())
	cases := map[string]string{
		"Station Ten wants the drive-through cut.":          "StationTenn wants the drive-through cut.",
		"station tenn kickoff is Friday":                    "StationTenn kickoff is Friday",
		"Station 10 renewed for the fall":                   "StationTenn renewed for the fall",
		"pull comps from fiscal AI":                         "pull comps from fiscal.ai",
		"grounding rides fiscal dot ai now":                 "grounding rides fiscal.ai now",
		"StationTenn and fiscal.ai stay canonical":          "StationTenn and fiscal.ai stay canonical",
		"the station tender kept the tennis court schedule": "the station tender kept the tennis court schedule",
	}
	for input, want := range cases {
		if got := canonicalizeDomainTerms(input); got != want {
			t.Errorf("canonicalizeDomainTerms(%q) = %q, want %q", input, got, want)
		}
	}
}

// W0-5: every rule that actually changed text records one correction_hit eval
// event (term + count) — the live vocab-error proxy. Matches that already read
// canonically (the acronym patterns match a literal "RTP"/"WebRTC" too) are
// identity matches, not corrections, and record nothing.
func TestCanonicalizeDomainTermsRecordsCorrectionHits(t *testing.T) {
	dir := ledgerTestDir(t)
	fixed := time.Date(2026, time.July, 11, 15, 0, 0, 0, time.UTC)
	prevNow := usageLedgerNow
	usageLedgerNow = func() time.Time { return fixed }
	defer func() { usageLedgerNow = prevNow }()

	// Identity matches: canonical text passes through and records nothing.
	if got := canonicalizeDomainTerms("WebRTC keeps RTP flowing"); got != "WebRTC keeps RTP flowing" {
		t.Fatalf("canonical text mutated: %q", got)
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
		t.Fatalf("identity matches must record nothing: entries=%d err=%v", len(entries), err)
	}

	got := canonicalizeDomainTerms("Suit Barn and boot burn synced over Web RTC with bon fire")
	want := "Boot Barn and Boot Barn synced over WebRTC with Bonfire"
	if got != want {
		t.Fatalf("canonicalized = %q, want %q", got, want)
	}

	rows := readLedgerLines(t, filepath.Join(dir, "eval-2026-07-11.jsonl"))
	if len(rows) != 4 {
		t.Fatalf("expected 4 correction_hit rows (two Boot Barn rules, WebRTC, Bonfire), got %d: %v", len(rows), rows)
	}
	hits := map[string]float64{}
	for _, row := range rows {
		if row["type"] != telemetryTypeEval || row["kind"] != evalKindCorrectionHit {
			t.Fatalf("unexpected eval row shape: %v", row)
		}
		if row["lane"] != seatTranscriptionLane {
			t.Fatalf("correction_hit lane = %v, want %q", row["lane"], seatTranscriptionLane)
		}
		fields := row["fields"].(map[string]any)
		hits[fields["term"].(string)] += fields["count"].(float64)
	}
	if hits["Boot Barn"] != 2 || hits["WebRTC"] != 1 || hits["Bonfire"] != 1 {
		t.Fatalf("hit counts = %v, want Boot Barn 2 / WebRTC 1 / Bonfire 1", hits)
	}
}

func TestCleanBoardNotesRemovesUserRequestNarration(t *testing.T) {
	got := cleanBoardNotes("User requested adding 'Impossible moments' to the board. Blocked: waiting on Erick to provide update.")
	want := "Waiting on Erick to provide update."
	if got != want {
		t.Fatalf("notes=%q, want %q", got, want)
	}
}

func TestCleanBoardNotesKeepsDirectFacts(t *testing.T) {
	got := cleanBoardNotes("User said Boot Barn is waiting on legal review.")
	want := "Boot Barn is waiting on legal review."
	if got != want {
		t.Fatalf("notes=%q, want %q", got, want)
	}
}

func TestCleanBoardNotesDropsBoardOnlyNarration(t *testing.T) {
	got := cleanBoardNotes("User requested adding Impossible Moments to the board.")
	if got != "" {
		t.Fatalf("notes=%q, want empty notes", got)
	}
}

func TestCardToolsCleanBoardNotes(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", t.TempDir()+"/memory.jsonl")
	t.Setenv("KANBAN_BOARD_PATH", t.TempDir()+"/board.json")

	app := newKanbanBoardApp()
	result, changed, err := app.createTicket(map[string]any{
		"title":  "Impossible Moments",
		"notes":  "User requested adding 'Impossible moments' to the board. Blocked: waiting on Erick to provide update.",
		"owner":  "Erick",
		"tags":   []any{"project", "blocked", "dependency"},
		"status": "Blocked",
	})
	if err != nil {
		t.Fatalf("createTicket: %v", err)
	}
	if !changed {
		t.Fatal("createTicket changed=false, want true")
	}

	card := result["card"].(kanbanCard)
	if card.Notes != "Waiting on Erick to provide update." {
		t.Fatalf("notes=%q, want direct project fact", card.Notes)
	}
}
