package main

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// rollupTestNow is a fixed pre-Sep-2026 instant so claude-sonnet-5 prices at
// the intro tier and every hand-computed number below is deterministic.
var rollupTestNow = time.Date(2026, time.July, 11, 15, 0, 0, 0, time.UTC)

func setUsageLedgerDirForTest(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("USAGE_LEDGER_PATH", dir)
	return dir
}

// swapUsageLedgerNow pins recordEvalEvent/recordProposalEvent/recordWorkflowRun
// (which stamp their own timestamps) to a fixed instant.
func swapUsageLedgerNow(t *testing.T, fixed time.Time) {
	t.Helper()
	previous := usageLedgerNow
	usageLedgerNow = func() time.Time { return fixed }
	t.Cleanup(func() { usageLedgerNow = previous })
}

func readUsageLedgerEntries(t *testing.T, dir string, date time.Time) []llmUsageEntry {
	t.Helper()
	path := filepath.Join(dir, usageLedgerFilePrefix+"-"+date.UTC().Format("2006-01-02")+".jsonl")
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatalf("read usage ledger: %v", err)
	}
	var entries []llmUsageEntry
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var entry llmUsageEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("decode usage ledger line %q: %v", line, err)
		}
		entries = append(entries, entry)
	}
	return entries
}

func TestFoldUsageRollupAggregatesFixture(t *testing.T) {
	dir := setUsageLedgerDirForTest(t)
	yesterday := rollupTestNow.AddDate(0, 0, -1)

	// Usage entries carry explicit TS values, so the daily files rotate by
	// entry date without touching usageLedgerNow.
	recordLLMUsage(llmUsageEntry{
		TS: rollupTestNow, Provider: providerAnthropic, Model: "claude-sonnet-5",
		Seat: seatRouter, InputTokens: 1000, OutputTokens: 500,
	}) // $0.002 + $0.005 = $0.007 at the intro tier
	recordLLMUsage(llmUsageEntry{
		TS: rollupTestNow, Provider: providerOpenAI, Model: "gpt-5.6-luna",
		Seat: seatBrain, InputTokens: 2000, CachedInputTokens: 1000, OutputTokens: 1000,
		WireSuccess: true, AcceptedOutput: true,
	}) // $0.002 + $0.0001 + $0.006 = $0.0081
	recordLLMUsage(llmUsageEntry{
		TS: rollupTestNow, Provider: providerAnthropic, Model: "claude-opus-4-8",
		Seat: seatFallback, FallbackUsed: true, Error: "upstream status 429",
	})
	recordLLMUsage(llmUsageEntry{
		TS: rollupTestNow, Provider: providerOpenAI, Model: "junk-model-rollup-test",
		Seat: seatCodex, InputTokens: 10, WireSuccess: true, OutputFailureReason: "max_output_truncation",
	}) // no price row → PriceMissing stamped by the ledger
	recordLLMUsage(llmUsageEntry{
		TS: yesterday, Provider: providerAnthropic, Model: "claude-sonnet-5",
		Seat: seatChat, InputTokens: 1000,
	}) // $0.002 on the trailing day

	// Eval/proposal/workflow events stamp their own TS via usageLedgerNow.
	swapUsageLedgerNow(t, rollupTestNow)
	recordEvalEvent(seatBoard, evalKindBoardOpFidelity, map[string]any{"op_count": 5, "error_count": 2})
	recordEvalEvent(seatRouter, evalKindRouterOutcome, map[string]any{"verdict": "proposed_tool"})
	recordEvalEvent(seatRouter, evalKindRouterOutcome, map[string]any{"verdict": "inline"})
	// Resolve-time confirm/dismiss router_outcome events share the kind but are
	// NOT routing turns: they must not inflate the truncation denominator.
	recordEvalEvent(seatRouter, evalKindRouterOutcome, map[string]any{"verdict": routerVerdictConfirmed})
	recordEvalEvent(seatRouter, evalKindRouterOutcome, map[string]any{"verdict": routerVerdictDismissed})
	recordEvalEvent(seatRouter, evalKindRouterTruncation, map[string]any{"stop_reason": "max_tokens"})
	recordEvalEvent(seatBrain, evalKindParseFailure, map[string]any{"seat": seatBrain, "model": "gpt-5.6-luna"})
	recordEvalEvent(seatGoalReview, evalKindGateResult, map[string]any{"runner": "anthropic_fable", "verdict": "passed"})
	recordEvalEvent(seatTranscriptionLane, evalKindTranscriptSegment, map[string]any{"status": "completed", "audio_seconds": 4.2})
	recordEvalEvent(seatTranscriptionLane, evalKindTranscriptSegment, map[string]any{"status": "failed"})
	recordEvalEvent(seatTranscriptionSession, evalKindNoVocabWarning, nil)
	recordEvalEvent(evalLaneTranscript, evalKindCorrectionHit, map[string]any{"term": "StationTenn"})
	recordEvalEvent(seatMeetingDigest, evalKindDigestOutput, map[string]any{"outcome": "accepted", "recovery": true})
	recordEvalEvent(seatMeetingDigest, evalKindDigestOutput, map[string]any{"outcome": "rejected"})
	recordEvalEvent(seatMeetingDigest, evalKindDigestOutput, map[string]any{"outcome": "circuit_open"})
	recordProposalEvent(proposalEventMinted, "prop-1", map[string]any{"source": proposalSourceBoardWorker})
	recordProposalEvent(proposalEventResolved, "prop-1", map[string]any{"resolution": "approved"})
	recordProposalEvent(proposalEventLaunched, "prop-1", map[string]any{"path": "codex"})
	recordWorkflowRun(workflowRunEntry{WorkflowID: "wf-1", TriggerSurface: triggerSurfacePalette, Outcome: workflowOutcomeLaunched})

	snap := foldUsageRollup(dir, rollupTestNow)
	if len(snap.Days) != usageRollupWindowDays {
		t.Fatalf("days=%d, want %d", len(snap.Days), usageRollupWindowDays)
	}
	today := snap.today()
	if today.Date != "2026-07-11" {
		t.Fatalf("today=%s, want 2026-07-11", today.Date)
	}
	if today.Total.Calls != 4 {
		t.Fatalf("today calls=%d, want 4", today.Total.Calls)
	}
	wantCost := 0.007 + 0.0081
	if math.Abs(today.Total.EstCostUSD-wantCost) > 1e-9 {
		t.Fatalf("today cost=%.6f, want %.6f", today.Total.EstCostUSD, wantCost)
	}
	if today.Total.Errors != 1 || today.Total.Fallbacks != 1 || today.Total.PriceMissing != 1 {
		t.Fatalf("today counters errors=%d fallbacks=%d priceMissing=%d, want 1/1/1", today.Total.Errors, today.Total.Fallbacks, today.Total.PriceMissing)
	}
	if today.Total.WireSuccesses != 2 || today.Total.AcceptedOutputs != 1 || today.Total.RejectedOutputs != 1 {
		t.Fatalf("output truth counters=%+v, want wire/accepted/rejected 2/1/1", today.Total)
	}

	router := today.Seats[seatRouter]
	if router == nil || router.Calls != 1 || math.Abs(router.EstCostUSD-0.007) > 1e-9 {
		t.Fatalf("router seat cell=%+v, want 1 call at $0.007", router)
	}
	brain := today.Seats[seatBrain]
	if brain == nil || brain.InputTokens != 2000 || brain.CachedInputTokens != 1000 || brain.OutputTokens != 1000 {
		t.Fatalf("brain seat cell=%+v, want the token splits preserved", brain)
	}
	sonnet := today.Models["anthropic/claude-sonnet-5"]
	if sonnet == nil || sonnet.Calls != 1 || math.Abs(sonnet.EstCostUSD-0.007) > 1e-9 {
		t.Fatalf("sonnet model cell=%+v, want 1 call at $0.007", sonnet)
	}

	eval := today.Eval
	if eval.BoardOps != 5 || eval.BoardOpErrors != 2 {
		t.Fatalf("board ops=%d errors=%d, want 5/2", eval.BoardOps, eval.BoardOpErrors)
	}
	if eval.RouterOutcomes != 2 || eval.RouterTruncations != 1 {
		t.Fatalf("router outcomes=%d truncations=%d, want 2/1", eval.RouterOutcomes, eval.RouterTruncations)
	}
	if eval.ParseFailures != 1 || eval.GateResults != 1 || eval.NoVocabWarnings != 1 || eval.CorrectionHits != 1 {
		t.Fatalf("eval counters=%+v, want parse/gate/vocab/correction all 1", eval)
	}
	if eval.TranscriptSegments != 2 || eval.TranscriptFailed != 1 {
		t.Fatalf("transcript segments=%d failed=%d, want 2/1", eval.TranscriptSegments, eval.TranscriptFailed)
	}
	if eval.DigestAccepted != 1 || eval.DigestRejected != 1 || eval.DigestSuppressed != 1 || eval.DigestRecovered != 1 {
		t.Fatalf("digest output funnel=%+v, want accepted/rejected/suppressed/recovered 1/1/1/1", eval)
	}
	if eval.ProposalsMinted != 1 || eval.ProposalsResolved != 1 || eval.ProposalsLaunched != 1 || eval.WorkflowLaunches != 1 {
		t.Fatalf("proposal funnel=%+v, want 1/1/1/1", eval)
	}

	trailing := snap.Days[len(snap.Days)-2]
	if trailing.Total.Calls != 1 || math.Abs(trailing.Total.EstCostUSD-0.002) > 1e-9 {
		t.Fatalf("yesterday=%+v, want 1 call at $0.002", trailing.Total)
	}
}

func TestEvaluateUsageAlertsThresholds(t *testing.T) {
	quiet := usageRollupSnapshot{SpendAlertDailyUSD: 75, Days: make([]usageRollupDay, usageRollupWindowDays)}
	if alerts := evaluateUsageAlerts(quiet); len(alerts) != 0 {
		t.Fatalf("quiet snapshot fired %v, want none", alerts)
	}

	hot := usageRollupSnapshot{
		SpendAlertDailyUSD: 75,
		DroppedWrites:      2,
		PriceMissingModels: []string{"gpt-5.7-typo"},
		Days:               make([]usageRollupDay, usageRollupWindowDays),
	}
	hot.Days[len(hot.Days)-1] = usageRollupDay{
		Total: usageRollupCell{EstCostUSD: 80.5, PriceMissing: 1},
		Eval: usageRollupEvalCounters{
			ParseFailures:      6, // trailing days all zero → spike
			BoardOps:           10,
			BoardOpErrors:      3, // 30% > 20%
			RouterOutcomes:     20,
			RouterTruncations:  3, // 15% > 10%
			NoVocabWarnings:    1,
			TranscriptSegments: 100,
			TranscriptFailed:   3, // 3% > 2%
		},
	}
	got := map[string]bool{}
	for _, alert := range evaluateUsageAlerts(hot) {
		got[alert.Kind] = true
		if strings.TrimSpace(alert.Text) == "" {
			t.Errorf("alert %s has empty text", alert.Kind)
		}
	}
	for _, kind := range []string{
		usageAlertSpendDaily, usageAlertPriceMissing, usageAlertLedgerWriteErrors,
		usageAlertParseFailureSpike, usageAlertBoardOpErrorSpike, usageAlertRouterTruncation,
		usageAlertNoVocabWarning, usageAlertTranscriptDropOff,
	} {
		if !got[kind] {
			t.Errorf("alert %s did not fire; fired set=%v", kind, got)
		}
	}
	if len(got) != 8 {
		t.Fatalf("fired %d kinds=%v, want exactly 8", len(got), got)
	}

	// A high trailing parse-failure baseline absorbs the same today count —
	// the spike is relative, not absolute.
	noisy := hot
	noisy.Days = make([]usageRollupDay, usageRollupWindowDays)
	copy(noisy.Days, hot.Days)
	for i := 0; i < len(noisy.Days)-1; i++ {
		noisy.Days[i] = usageRollupDay{Eval: usageRollupEvalCounters{ParseFailures: 10}}
	}
	for _, alert := range evaluateUsageAlerts(noisy) {
		if alert.Kind == usageAlertParseFailureSpike {
			t.Fatalf("parse-failure spike fired against a 10/day trailing average with 6 today")
		}
	}

	// Ratio thresholds stay silent below their volume floors.
	tiny := usageRollupSnapshot{SpendAlertDailyUSD: 75, Days: make([]usageRollupDay, usageRollupWindowDays)}
	tiny.Days[len(tiny.Days)-1] = usageRollupDay{Eval: usageRollupEvalCounters{
		BoardOps: 2, BoardOpErrors: 2, RouterOutcomes: 3, RouterTruncations: 3,
		TranscriptSegments: 10, TranscriptFailed: 5, ParseFailures: 2,
	}}
	if alerts := evaluateUsageAlerts(tiny); len(alerts) != 0 {
		t.Fatalf("below-floor snapshot fired %v, want none", alerts)
	}
}

func TestUsageAlertDedupeWindow(t *testing.T) {
	resetUsageAlertDedupeForTest()
	t.Cleanup(resetUsageAlertDedupeForTest)

	now := rollupTestNow
	if !shouldFireUsageAlert(usageAlertSpendDaily, now) {
		t.Fatal("first alert must fire")
	}
	if shouldFireUsageAlert(usageAlertSpendDaily, now.Add(5*time.Hour)) {
		t.Fatal("same kind within 6h must be deduped")
	}
	if !shouldFireUsageAlert(usageAlertPriceMissing, now.Add(time.Minute)) {
		t.Fatal("a different kind must not share the dedupe slot")
	}
	if !shouldFireUsageAlert(usageAlertSpendDaily, now.Add(6*time.Hour+time.Minute)) {
		t.Fatal("after the 6h window the kind must fire again")
	}
}

func TestRunUsageRollupOncePublishesLivingArtifactAndBaseline(t *testing.T) {
	dir := setUsageLedgerDirForTest(t)
	_ = dir
	resetUsageAlertDedupeForTest()
	t.Cleanup(resetUsageAlertDedupeForTest)

	app := newIsolatedKanbanBoardApp(t)
	previousApp := kanbanApp
	kanbanApp = app
	t.Cleanup(func() { kanbanApp = previousApp })

	now := time.Now().UTC()
	// One spend entry over the $75 default line so the alert engine fires.
	recordLLMUsage(llmUsageEntry{
		TS: now, Provider: providerAnthropic, Model: "claude-fable-5",
		Seat: seatOrchestrator, InputTokens: 1000, EstCostUSD: 100,
	})

	runUsageRollupOnce(app, now)

	artifact, ok := app.osArtifactByID(usageRollupArtifactID)
	if !ok {
		t.Fatal("Spend & Health artifact was not published")
	}
	if artifact.Metadata["title"] != usageRollupArtifactTitle || artifact.Metadata["published"] != "true" {
		t.Fatalf("artifact metadata=%v, want published %q", artifact.Metadata, usageRollupArtifactTitle)
	}
	baseline := strings.TrimSpace(artifact.Metadata[usageRollupBaselineMetadataKey])
	if baseline == "" {
		t.Fatal("baselineStartedAt stamp missing (W0-10 scaffolding)")
	}
	if !strings.Contains(artifact.Text, "## Baseline") || !strings.Contains(artifact.Text, "## Daily spend") {
		t.Fatalf("artifact body missing rollup sections: %.200s", artifact.Text)
	}

	// Count the SPEND alert specifically: priceMissingModels() is boot-scoped,
	// so unpriced ids recorded by sibling tests in this process can legally
	// add a price_missing alert alongside.
	countSpendAlerts := func() int {
		count := 0
		app.mu.Lock()
		for _, record := range app.notifications {
			if record.Kind == notificationKindAlert && record.ArtifactID == usageRollupArtifactID &&
				strings.Contains(record.Text, "LLM spend today") {
				count++
			}
		}
		app.mu.Unlock()
		return count
	}
	if got := countSpendAlerts(); got != 1 {
		t.Fatalf("spend alert notifications=%d, want exactly 1", got)
	}

	// Second pass an hour later: the baseline stamp survives and the alert is
	// deduped inside the 6h window.
	runUsageRollupOnce(app, now.Add(time.Hour))
	artifact, _ = app.osArtifactByID(usageRollupArtifactID)
	if got := strings.TrimSpace(artifact.Metadata[usageRollupBaselineMetadataKey]); got != baseline {
		t.Fatalf("baseline moved %q → %q, must be stamped once", baseline, got)
	}
	if got := countSpendAlerts(); got != 1 {
		t.Fatalf("spend alert notifications after rerun=%d, want still 1 (6h dedupe)", got)
	}

	snap := buildUsageRollupSnapshot(app, now.Add(time.Hour))
	if !snap.BaselineActive {
		t.Fatal("baseline must be active inside the first baseline window")
	}
	if snap.BaselineStartedAt.Format(time.RFC3339Nano) != baseline {
		t.Fatalf("snapshot baseline=%s, want the artifact stamp %s", snap.BaselineStartedAt.Format(time.RFC3339Nano), baseline)
	}
}

func TestUsageRollupHandlerRequiresSignInAndServesSnapshot(t *testing.T) {
	setupAuthTestEnv(t)
	setUsageLedgerDirForTest(t)
	app := newIsolatedKanbanBoardApp(t)
	previousApp := kanbanApp
	kanbanApp = app
	t.Cleanup(func() { kanbanApp = previousApp })

	anonymous := httptest.NewRequest(http.MethodGet, "/api/usage/rollup", nil)
	recorder := httptest.NewRecorder()
	usageRollupHandler(recorder, anonymous)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous status=%d, want 401", recorder.Code)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/usage/rollup", nil)
	for _, cookie := range loginAs(t, "aj@shareability.com", "B0NFIRE!") {
		req.AddCookie(cookie)
	}
	recorder = httptest.NewRecorder()
	usageRollupHandler(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("signed-in status=%d body=%s, want 200", recorder.Code, recorder.Body.String())
	}
	var snap usageRollupSnapshot
	if err := json.Unmarshal(recorder.Body.Bytes(), &snap); err != nil {
		t.Fatalf("decode rollup payload: %v", err)
	}
	if len(snap.Days) != usageRollupWindowDays {
		t.Fatalf("payload days=%d, want %d", len(snap.Days), usageRollupWindowDays)
	}
	if snap.SpendAlertDailyUSD != defaultSpendAlertDailyUSD {
		t.Fatalf("spend line=%v, want %v", snap.SpendAlertDailyUSD, defaultSpendAlertDailyUSD)
	}
	if !snap.LedgerEnabled {
		t.Fatal("payload must report the ledger enabled")
	}
}

func TestAssistantRealtimeUsageBeaconRecordsPrivateVoiceEntry(t *testing.T) {
	setupAuthTestEnv(t)
	dir := setUsageLedgerDirForTest(t)
	cookies := loginAs(t, "aj@shareability.com", "B0NFIRE!")

	send := func(body string, withAuth bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/assistant/realtime/usage", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if withAuth {
			for _, cookie := range cookies {
				req.AddCookie(cookie)
			}
		}
		recorder := httptest.NewRecorder()
		assistantRealtimeUsageHandler(recorder, req)
		return recorder
	}

	beacon := `{"callId":"resp_123","model":"gpt-realtime-2","usage":{
		"input_tokens":1200,"output_tokens":300,
		"input_token_details":{"text_tokens":200,"audio_tokens":1000,"cached_tokens":100,
			"cached_tokens_details":{"text_tokens":100,"audio_tokens":0}},
		"output_token_details":{"text_tokens":50,"audio_tokens":250}}}`

	if recorder := send(beacon, false); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status=%d, want 401", recorder.Code)
	}
	if entries := readUsageLedgerEntries(t, dir, time.Now()); len(entries) != 0 {
		t.Fatalf("unauthenticated beacon must record nothing, got %d entries", len(entries))
	}

	if recorder := send(beacon, true); recorder.Code != http.StatusOK {
		t.Fatalf("beacon status=%d body=%s, want 200", recorder.Code, recorder.Body.String())
	}
	entries := readUsageLedgerEntries(t, dir, time.Now())
	if len(entries) != 1 {
		t.Fatalf("ledger entries=%d, want 1", len(entries))
	}
	entry := entries[0]
	if entry.Seat != seatVoicePrivate || entry.Provider != providerOpenAI || entry.Model != "gpt-realtime-2" {
		t.Fatalf("entry seat/provider/model=%s/%s/%s, want voice_private/openai/gpt-realtime-2", entry.Seat, entry.Provider, entry.Model)
	}
	if entry.ThreadID != "resp_123" {
		t.Fatalf("thread_id=%q, want the call id resp_123", entry.ThreadID)
	}
	// The wire's cached share must be split OUT of the raw text tokens.
	if entry.InputTokens != 100 || entry.CachedInputTokens != 100 ||
		entry.AudioInputTokens != 1000 || entry.CachedAudioInputTokens != 0 ||
		entry.OutputTokens != 50 || entry.AudioOutputTokens != 250 {
		t.Fatalf("token splits=%+v, want 100/100/1000/0/50/250", entry)
	}
	if entry.EstCostUSD <= 0 || entry.PriceMissing {
		t.Fatalf("cost=%v priceMissing=%v, want a positive priced estimate", entry.EstCostUSD, entry.PriceMissing)
	}

	// A non-realtime model claim falls back to the server dial instead of
	// billing an arbitrary id.
	spoofed := strings.Replace(beacon, `"model":"gpt-realtime-2"`, `"model":"claude-fable-5"`, 1)
	if recorder := send(spoofed, true); recorder.Code != http.StatusOK {
		t.Fatalf("spoofed-model status=%d, want 200 with model substituted", recorder.Code)
	}
	entries = readUsageLedgerEntries(t, dir, time.Now())
	if len(entries) != 2 || entries[1].Model != realtimeModel() {
		t.Fatalf("spoofed model recorded as %q, want %q", entries[len(entries)-1].Model, realtimeModel())
	}

	for name, body := range map[string]string{
		"negative tokens": `{"callId":"resp_9","usage":{"input_tokens":-5,"output_tokens":10}}`,
		"absurd tokens":   fmt.Sprintf(`{"callId":"resp_9","usage":{"input_tokens":%d}}`, privateVoiceBeaconTokenCap+1),
		"no usage":        `{"callId":"resp_9"}`,
		"empty usage":     `{"callId":"resp_9","usage":{}}`,
	} {
		if recorder := send(body, true); recorder.Code != http.StatusBadRequest {
			t.Errorf("%s: status=%d, want 400", name, recorder.Code)
		}
	}
	if entries := readUsageLedgerEntries(t, dir, time.Now()); len(entries) != 2 {
		t.Fatalf("rejected beacons must record nothing; entries=%d, want 2", len(entries))
	}
}

func TestHealthzCarriesTelemetryLaneSnapshot(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	recorder := httptest.NewRecorder()

	healthHandler(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("health status=%d", recorder.Code)
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode health payload: %v", err)
	}
	telemetry, ok := payload["telemetry"].(map[string]any)
	if !ok {
		t.Fatalf("telemetry block missing from healthz: %v", payload)
	}
	if model, _ := telemetry["realtime_model"].(string); strings.TrimSpace(model) == "" {
		t.Fatalf("telemetry.realtime_model empty: %v", telemetry)
	}
	if vocab, present := telemetry["transcription_lane_vocab"]; !present {
		t.Fatalf("telemetry.transcription_lane_vocab missing: %v", vocab)
	}
	if enabled, _ := telemetry["usage_ledger_enabled"].(bool); !enabled {
		t.Fatalf("telemetry.usage_ledger_enabled=%v, want true by default", telemetry["usage_ledger_enabled"])
	}
	if enabled, _ := telemetry["usage_alerts_enabled"].(bool); !enabled {
		t.Fatalf("telemetry.usage_alerts_enabled=%v, want true by default", telemetry["usage_alerts_enabled"])
	}
	// The existing contract stays intact.
	if payload["ok"] != true || payload["service"] != "meetingassist" {
		t.Fatalf("existing healthz fields mutated: %v", payload)
	}
}

// F4: the daily retention sweep deletes usage-/eval- ledger files older than
// the 90-day window, keeps recent ones, never touches a non-ledger file, and
// stands down under the ledger kill switch.
func TestPruneUsageLedgerHonorsRetentionWindow(t *testing.T) {
	dir := ledgerTestDir(t)
	now := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)

	write := func(name string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte("{}\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	dateName := func(prefix string, day time.Time) string {
		return prefix + "-" + day.Format("2006-01-02") + ".jsonl"
	}

	oldUsage := dateName(usageLedgerFilePrefix, now.AddDate(0, 0, -200)) // ~200d old
	oldEval := dateName(evalLedgerFilePrefix, now.AddDate(0, 0, -91))    // just past the window
	freshUsage := dateName(usageLedgerFilePrefix, now.AddDate(0, 0, -1)) // yesterday
	freshEval := dateName(evalLedgerFilePrefix, now)                     // today
	stray := "rollup-notes.txt"                                          // not a dated ledger file

	for _, name := range []string{oldUsage, oldEval, freshUsage, freshEval, stray} {
		write(name)
	}

	if removed := pruneUsageLedger(now); removed != 2 {
		t.Fatalf("pruneUsageLedger removed=%d, want 2", removed)
	}

	exists := func(name string) bool {
		_, err := os.Stat(filepath.Join(dir, name))
		return err == nil
	}
	for _, gone := range []string{oldUsage, oldEval} {
		if exists(gone) {
			t.Fatalf("expected %s pruned", gone)
		}
	}
	for _, kept := range []string{freshUsage, freshEval, stray} {
		if !exists(kept) {
			t.Fatalf("expected %s kept", kept)
		}
	}
}

// F4: a disabled ledger owns no files to sweep — pruneUsageLedger is a no-op.
func TestPruneUsageLedgerSkipsWhenLedgerDisabled(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("USAGE_LEDGER_PATH", dir)
	t.Setenv("USAGE_LEDGER_DISABLED", "1")
	now := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)

	old := usageLedgerFilePrefix + "-" + now.AddDate(0, 0, -200).Format("2006-01-02") + ".jsonl"
	if err := os.WriteFile(filepath.Join(dir, old), []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write %s: %v", old, err)
	}

	if removed := pruneUsageLedger(now); removed != 0 {
		t.Fatalf("disabled-ledger sweep removed=%d, want 0", removed)
	}
	if _, err := os.Stat(filepath.Join(dir, old)); err != nil {
		t.Fatalf("disabled-ledger sweep must not delete files: %v", err)
	}
}

// F4: the once-per-day gate lets the first sweep of a day through and holds the
// rest of the interval's ticks.
func TestShouldRunRetentionSweepDailyGate(t *testing.T) {
	resetUsageRetentionSweepForTest()
	t.Cleanup(resetUsageRetentionSweepForTest)
	now := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)

	if !shouldRunRetentionSweep(now) {
		t.Fatal("first sweep of the day must run")
	}
	if shouldRunRetentionSweep(now.Add(15 * time.Minute)) {
		t.Fatal("a same-day tick must be gated")
	}
	if !shouldRunRetentionSweep(now.Add(25 * time.Hour)) {
		t.Fatal("the next day's sweep must run")
	}
}
