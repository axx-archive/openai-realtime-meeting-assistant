// usage_rollup.go — W0 item 9 of docs/model-routing-master-plan-2026-07-11.md:
// the daily rollup worker that folds the usage/eval JSONL ledgers
// (usage_ledger.go) into per-day/per-seat/per-model aggregates, publishes them
// as ONE living "LLM Spend & Health" company-brain artifact (visible to every
// roster user — founder decision 2, everyone-aware pillar), serves the same
// snapshot as GET /api/usage/rollup (signed-in), and runs the alert engine
// (spend cap, unknown models, ledger write errors, parse-failure spike,
// board-op error spike, router truncation spike, no-vocab warning, transcript
// failed-segment rate) through the existing notifications system with a 6h
// per-kind dedupe and its own USAGE_ALERTS_DISABLED kill switch.
//
// Also home to the W0 private-voice usage beacon endpoint (founder decision 3:
// no unmetered lanes in the baseline): the private dashboard voice peer is
// browser-owned, so the client POSTs each response.done usage object to
// /assistant/realtime/usage and the server ledgers it under seat
// voice_private after auth + sanity bounds.
//
// The fold is a deterministic $0 Go pass over the JSONL files — no LLM calls,
// no wire traffic. Everything here is additive and killable:
// USAGE_ROLLUP_DISABLED stops the worker, USAGE_ALERTS_DISABLED silences
// alerts, and the ledger's own USAGE_LEDGER_DISABLED starves the books.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// Dials
// ---------------------------------------------------------------------------

const (
	// usageRollupArtifactID is fixed so the living artifact updates in place
	// forever (appendOSArtifact dedupes on ID; updates ride
	// updateOSArtifactWithMetadata) — the gmail-consent seed pattern.
	usageRollupArtifactID    = "os-artifact-usage-spend-health"
	usageRollupArtifactTitle = "LLM Spend & Health"

	// usageRollupBaselineMetadataKey stamps when the books started (W0-10
	// baseline scaffolding). Set once on first publish, never rewritten — the
	// first usageBaselineDays() after it are the frozen pre-flip baseline.
	usageRollupBaselineMetadataKey = "baselineStartedAt"

	// usageRollupWindowDays is today + the trailing week — enough for every
	// spike threshold (vs trailing 7-day) and the daily table.
	usageRollupWindowDays = 8

	usageAlertDedupeWindow     = 6 * time.Hour
	defaultUsageRollupInterval = 15 * time.Minute
	defaultUsageBaselineDays   = 7
	usageRollupFirstRunDelay   = 90 * time.Second

	// Ledger retention: the rollup worker prunes usage-/eval- JSONL files older
	// than this daily. The living artifact + per-day aggregates are the durable
	// record; the raw ledgers past the window are just disk.
	usageLedgerRetentionDays = 90
	usageLedgerRetention     = usageLedgerRetentionDays * 24 * time.Hour
)

// usageAlertsEnabled: USAGE_ALERTS_DISABLED silences the alert engine only —
// the rollup artifact and endpoint keep updating so the books stay visible.
func usageAlertsEnabled() bool {
	return !boolEnv("USAGE_ALERTS_DISABLED")
}

func usageRollupInterval() time.Duration {
	return durationEnv("USAGE_ROLLUP_INTERVAL", defaultUsageRollupInterval, time.Minute)
}

func usageBaselineDays() int {
	days := int(floatEnv("USAGE_BASELINE_DAYS", defaultUsageBaselineDays))
	if days <= 0 {
		days = defaultUsageBaselineDays
	}
	return days
}

// ---------------------------------------------------------------------------
// Snapshot shapes (the /api/usage/rollup payload and the artifact's source)
// ---------------------------------------------------------------------------

// usageRollupCell is one seat's (or model's) aggregate for one day.
type usageRollupCell struct {
	Calls               int64   `json:"calls"`
	InputTokens         int64   `json:"input_tokens,omitempty"`
	CachedInputTokens   int64   `json:"cached_input_tokens,omitempty"`
	CacheCreationTokens int64   `json:"cache_creation_tokens,omitempty"`
	OutputTokens        int64   `json:"output_tokens,omitempty"`
	AudioSeconds        float64 `json:"audio_seconds,omitempty"`
	EstCostUSD          float64 `json:"est_cost_usd"`
	Errors              int64   `json:"errors,omitempty"`
	Fallbacks           int64   `json:"fallbacks,omitempty"`
	PriceMissing        int64   `json:"price_missing,omitempty"`
	WireSuccesses       int64   `json:"wire_successes,omitempty"`
	AcceptedOutputs     int64   `json:"accepted_outputs,omitempty"`
	RejectedOutputs     int64   `json:"rejected_outputs,omitempty"`
}

// usageRollupEvalCounters folds the eval/proposal/workflow event funnel for
// one day — the quality half of "Spend & Health".
type usageRollupEvalCounters struct {
	ParseFailures      int64 `json:"parse_failures"`
	BoardOps           int64 `json:"board_ops"`
	BoardOpErrors      int64 `json:"board_op_errors"`
	RouterOutcomes     int64 `json:"router_outcomes"`
	RouterTruncations  int64 `json:"router_truncations"`
	GateResults        int64 `json:"gate_results"`
	NoVocabWarnings    int64 `json:"no_vocab_warnings"`
	TranscriptSegments int64 `json:"transcript_segments"`
	TranscriptFailed   int64 `json:"transcript_failed"`
	CorrectionHits     int64 `json:"correction_hits"`
	ProposalsMinted    int64 `json:"proposals_minted"`
	ProposalsResolved  int64 `json:"proposals_resolved"`
	ProposalsLaunched  int64 `json:"proposals_launched"`
	WorkflowLaunches   int64 `json:"workflow_launches"`
	DigestAccepted     int64 `json:"digest_accepted"`
	DigestRejected     int64 `json:"digest_rejected"`
	DigestSuppressed   int64 `json:"digest_suppressed"`
	DigestRecovered    int64 `json:"digest_recovered"`
}

// usageRollupDay is one UTC day of books: totals plus seat×model splits plus
// the eval funnel.
type usageRollupDay struct {
	Date  string                      `json:"date"` // YYYY-MM-DD (UTC)
	Total usageRollupCell             `json:"total"`
	Seats map[string]*usageRollupCell `json:"seats,omitempty"`

	// Models is keyed provider/model so the same id under two providers can
	// never fold together.
	Models map[string]*usageRollupCell `json:"models,omitempty"`
	Eval   usageRollupEvalCounters     `json:"eval"`
}

// usageRollupSnapshot is the whole payload: chronological days (oldest first,
// today last) plus boot-scoped health counters and the baseline stamp.
type usageRollupSnapshot struct {
	GeneratedAt        time.Time        `json:"generated_at"`
	BaselineStartedAt  time.Time        `json:"baseline_started_at"`
	BaselineDays       int              `json:"baseline_days"`
	BaselineActive     bool             `json:"baseline_active"`
	LedgerEnabled      bool             `json:"ledger_enabled"`
	AlertsEnabled      bool             `json:"alerts_enabled"`
	DroppedWrites      int64            `json:"dropped_writes"`
	PriceMissingModels []string         `json:"price_missing_models,omitempty"`
	SpendAlertDailyUSD float64          `json:"spend_alert_daily_usd"`
	Days               []usageRollupDay `json:"days"`
}

// today returns the last (current) day of the window; the zero value when the
// snapshot is empty so callers never index a nil slice.
func (snap usageRollupSnapshot) today() usageRollupDay {
	if len(snap.Days) == 0 {
		return usageRollupDay{}
	}
	return snap.Days[len(snap.Days)-1]
}

// ---------------------------------------------------------------------------
// The fold: JSONL files → snapshot. Pure with respect to the ledger directory
// so tests hand it a fixture dir and hand-check the numbers.
// ---------------------------------------------------------------------------

// foldUsageRollup reads the last usageRollupWindowDays of usage-/eval- files
// under dir and aggregates them. Missing files are quiet days, not errors;
// malformed lines are skipped (the ledger is append-only JSONL — a torn final
// line during a write is expected once in a while).
func foldUsageRollup(dir string, now time.Time) usageRollupSnapshot {
	snap := usageRollupSnapshot{GeneratedAt: now.UTC()}
	for i := usageRollupWindowDays - 1; i >= 0; i-- {
		date := now.UTC().AddDate(0, 0, -i)
		day := usageRollupDay{
			Date:   date.Format("2006-01-02"),
			Seats:  map[string]*usageRollupCell{},
			Models: map[string]*usageRollupCell{},
		}
		foldUsageFileInto(&day, filepath.Join(dir, usageLedgerFilePrefix+"-"+day.Date+".jsonl"))
		foldEvalFileInto(&day, filepath.Join(dir, evalLedgerFilePrefix+"-"+day.Date+".jsonl"))
		snap.Days = append(snap.Days, day)
	}
	return snap
}

func foldUsageFileInto(day *usageRollupDay, path string) {
	forEachLedgerLine(path, func(line []byte) {
		var entry llmUsageEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			return
		}
		seat := entry.Seat
		if seat == "" {
			seat = seatUntagged
		}
		modelKey := strings.TrimSpace(entry.Provider) + "/" + strings.TrimSpace(entry.Model)
		for _, cell := range []*usageRollupCell{&day.Total, rollupCell(day.Seats, seat), rollupCell(day.Models, modelKey)} {
			cell.Calls++
			cell.InputTokens += entry.InputTokens
			cell.CachedInputTokens += entry.CachedInputTokens
			cell.CacheCreationTokens += entry.CacheCreationTokens
			cell.OutputTokens += entry.OutputTokens
			cell.AudioSeconds += entry.AudioSeconds
			cell.EstCostUSD += entry.EstCostUSD
			if entry.Error != "" {
				cell.Errors++
			}
			if entry.FallbackUsed {
				cell.Fallbacks++
			}
			if entry.PriceMissing {
				cell.PriceMissing++
			}
			if entry.WireSuccess {
				cell.WireSuccesses++
				if entry.AcceptedOutput {
					cell.AcceptedOutputs++
				} else {
					cell.RejectedOutputs++
				}
			}
		}
	})
}

func foldEvalFileInto(day *usageRollupDay, path string) {
	forEachLedgerLine(path, func(line []byte) {
		var event telemetryEvent
		if err := json.Unmarshal(line, &event); err != nil {
			return
		}
		eval := &day.Eval
		switch event.Type {
		case telemetryTypeEval:
			switch event.Kind {
			case evalKindParseFailure:
				eval.ParseFailures++
			case evalKindBoardOpFidelity:
				eval.BoardOps += fieldInt64(event.Fields, "op_count")
				eval.BoardOpErrors += fieldInt64(event.Fields, "error_count")
			case evalKindRouterOutcome:
				// Only routing turns feed the truncation denominator; the
				// confirm/dismiss resolve-time router_outcome events share this
				// kind but are not turns (scout_chat_threads.go).
				verdict, _ := event.Fields["verdict"].(string)
				if isRouterRoutingVerdict(verdict) {
					eval.RouterOutcomes++
				}
			case evalKindRouterTruncation:
				eval.RouterTruncations++
			case evalKindGateResult:
				eval.GateResults++
			case evalKindNoVocabWarning:
				eval.NoVocabWarnings++
			case evalKindTranscriptSegment:
				eval.TranscriptSegments++
				if status, _ := event.Fields["status"].(string); status == "failed" {
					eval.TranscriptFailed++
				}
			case evalKindCorrectionHit:
				eval.CorrectionHits++
			case evalKindDigestOutput:
				switch outcome, _ := event.Fields["outcome"].(string); outcome {
				case "accepted":
					eval.DigestAccepted++
					if recovery, _ := event.Fields["recovery"].(bool); recovery {
						eval.DigestRecovered++
					}
				case "rejected":
					eval.DigestRejected++
				case "circuit_open":
					eval.DigestSuppressed++
				}
			}
		case telemetryTypeProposal:
			switch event.Kind {
			case proposalEventMinted:
				eval.ProposalsMinted++
			case proposalEventResolved:
				eval.ProposalsResolved++
			case proposalEventLaunched:
				eval.ProposalsLaunched++
			}
		case telemetryTypeWorkflowRun:
			if event.Kind == workflowOutcomeLaunched {
				eval.WorkflowLaunches++
			}
		}
	})
}

func rollupCell(cells map[string]*usageRollupCell, key string) *usageRollupCell {
	cell, ok := cells[key]
	if !ok {
		cell = &usageRollupCell{}
		cells[key] = cell
	}
	return cell
}

// fieldInt64 reads a numeric field from a decoded telemetryEvent (JSON numbers
// arrive as float64; instrumenters sometimes stamp ints directly in tests).
func fieldInt64(fields map[string]any, key string) int64 {
	switch value := fields[key].(type) {
	case float64:
		return int64(value)
	case int:
		return int64(value)
	case int64:
		return value
	default:
		return 0
	}
}

// forEachLedgerLine streams one JSONL file. A missing file is a quiet day.
func forEachLedgerLine(path string, fn func(line []byte)) {
	file, err := os.Open(path)
	if err != nil {
		return
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}
		fn(line)
	}
}

// buildUsageRollupSnapshot is the fold plus the boot-scoped health fields and
// the baseline stamp (read from the living artifact when it exists, so the
// stamp survives restarts).
func buildUsageRollupSnapshot(app *kanbanBoardApp, now time.Time) usageRollupSnapshot {
	snap := foldUsageRollup(usageLedgerDir(), now)
	snap.LedgerEnabled = usageLedgerEnabled()
	snap.AlertsEnabled = usageAlertsEnabled()
	snap.DroppedWrites = usageLedgerDroppedWrites()
	snap.PriceMissingModels = priceMissingModels()
	snap.SpendAlertDailyUSD = spendAlertDailyUSD()
	snap.BaselineDays = usageBaselineDays()

	baseline := now.UTC()
	if app != nil && app.memory != nil {
		if existing, ok := app.osArtifactByID(usageRollupArtifactID); ok {
			if parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(existing.Metadata[usageRollupBaselineMetadataKey])); err == nil {
				baseline = parsed.UTC()
			}
		}
	}
	snap.BaselineStartedAt = baseline
	snap.BaselineActive = now.UTC().Before(baseline.AddDate(0, 0, snap.BaselineDays))
	return snap
}

// ---------------------------------------------------------------------------
// Alert engine: 8 thresholds → notifications, 6h dedupe per kind
// ---------------------------------------------------------------------------

// Alert kinds double as the dedupe keys.
const (
	usageAlertSpendDaily        = "usage_spend_daily"
	usageAlertPriceMissing      = "usage_price_missing"
	usageAlertLedgerWriteErrors = "usage_ledger_write_errors"
	usageAlertParseFailureSpike = "usage_parse_failure_spike"
	usageAlertBoardOpErrorSpike = "usage_board_op_error_spike"
	usageAlertRouterTruncation  = "usage_router_truncation_spike"
	usageAlertNoVocabWarning    = "usage_no_vocab_warning"
	usageAlertTranscriptDropOff = "usage_transcript_failed_rate"
)

// Spike floors: below these absolute volumes a ratio is noise, not a signal
// (3 failed parses on 3 calls is a quiet day, not a 100% regression).
const (
	usageAlertParseFailureMinCount    = 5
	usageAlertBoardOpsMinCount        = 5
	usageAlertRouterOutcomesMinCount  = 10
	usageAlertTranscriptSegmentsMin   = 25
	usageAlertParseFailureSpikeFactor = 3.0
	usageAlertBoardOpErrorRate        = 0.20
	usageAlertRouterTruncationRate    = 0.10
	usageAlertTranscriptFailedRate    = 0.02
)

type usageAlert struct {
	Kind string
	Text string
}

// evaluateUsageAlerts applies every threshold to the snapshot and returns the
// alerts that SHOULD fire — pure so tests hand-build snapshots. Dedupe is the
// caller's job (shouldFireUsageAlert).
func evaluateUsageAlerts(snap usageRollupSnapshot) []usageAlert {
	var alerts []usageAlert
	today := snap.today()

	if snap.SpendAlertDailyUSD > 0 && today.Total.EstCostUSD > snap.SpendAlertDailyUSD {
		alerts = append(alerts, usageAlert{usageAlertSpendDaily, fmt.Sprintf(
			"LLM spend today is $%.2f — over the $%.2f/day alert line (SPEND_ALERT_DAILY_USD)", today.Total.EstCostUSD, snap.SpendAlertDailyUSD)})
	}
	if today.Total.PriceMissing > 0 || len(snap.PriceMissingModels) > 0 {
		ids := strings.Join(snap.PriceMissingModels, ", ")
		if ids == "" {
			ids = "see today's ledger entries with price_missing"
		}
		alerts = append(alerts, usageAlert{usageAlertPriceMissing, fmt.Sprintf(
			"LLM calls billed against unknown model ids (typo'd env flip tripwire): %s", ids)})
	}
	if snap.DroppedWrites > 0 {
		alerts = append(alerts, usageAlert{usageAlertLedgerWriteErrors, fmt.Sprintf(
			"usage ledger dropped %d write(s) since boot — the books are undercounting", snap.DroppedWrites)})
	}

	// Parse-failure spike vs the trailing window's daily average.
	if today.Eval.ParseFailures >= usageAlertParseFailureMinCount {
		var trailing int64
		trailingDays := len(snap.Days) - 1
		for _, day := range snap.Days[:max(trailingDays, 0)] {
			trailing += day.Eval.ParseFailures
		}
		average := 0.0
		if trailingDays > 0 {
			average = float64(trailing) / float64(trailingDays)
		}
		if float64(today.Eval.ParseFailures) > usageAlertParseFailureSpikeFactor*average {
			alerts = append(alerts, usageAlert{usageAlertParseFailureSpike, fmt.Sprintf(
				"strict-JSON parse failures spiked: %d today vs %.1f/day trailing average", today.Eval.ParseFailures, average)})
		}
	}
	if today.Eval.BoardOps >= usageAlertBoardOpsMinCount {
		rate := float64(today.Eval.BoardOpErrors) / float64(today.Eval.BoardOps)
		if rate > usageAlertBoardOpErrorRate {
			alerts = append(alerts, usageAlert{usageAlertBoardOpErrorSpike, fmt.Sprintf(
				"board-op error rate is %.0f%% today (%d/%d ops) — over the %.0f%% line", rate*100, today.Eval.BoardOpErrors, today.Eval.BoardOps, usageAlertBoardOpErrorRate*100)})
		}
	}
	if today.Eval.RouterOutcomes >= usageAlertRouterOutcomesMinCount {
		rate := float64(today.Eval.RouterTruncations) / float64(today.Eval.RouterOutcomes)
		if rate > usageAlertRouterTruncationRate {
			alerts = append(alerts, usageAlert{usageAlertRouterTruncation, fmt.Sprintf(
				"router truncation rate is %.0f%% today (%d/%d turns) — proposals may be silently degrading", rate*100, today.Eval.RouterTruncations, today.Eval.RouterOutcomes)})
		}
	}
	if today.Eval.NoVocabWarnings > 0 {
		alerts = append(alerts, usageAlert{usageAlertNoVocabWarning, fmt.Sprintf(
			"authoritative transcription ran WITHOUT vocabulary biasing %d time(s) today — fidelity is degraded (whisper-family pin?)", today.Eval.NoVocabWarnings)})
	}
	if today.Eval.TranscriptSegments >= usageAlertTranscriptSegmentsMin {
		rate := float64(today.Eval.TranscriptFailed) / float64(today.Eval.TranscriptSegments)
		if rate > usageAlertTranscriptFailedRate {
			alerts = append(alerts, usageAlert{usageAlertTranscriptDropOff, fmt.Sprintf(
				"transcription drop-off: %.1f%% of segments failed today (%d/%d) — speech the brain never heard", rate*100, today.Eval.TranscriptFailed, today.Eval.TranscriptSegments)})
		}
	}
	return alerts
}

var (
	usageAlertMu        sync.Mutex
	usageAlertLastFired = map[string]time.Time{}
)

// shouldFireUsageAlert is the 6h per-kind dedupe. In-memory by design: a
// restart re-arming every alert is the right failure mode for an ops channel.
func shouldFireUsageAlert(kind string, now time.Time) bool {
	usageAlertMu.Lock()
	defer usageAlertMu.Unlock()
	if last, ok := usageAlertLastFired[kind]; ok && now.Sub(last) < usageAlertDedupeWindow {
		return false
	}
	usageAlertLastFired[kind] = now
	return true
}

func resetUsageAlertDedupeForTest() {
	usageAlertMu.Lock()
	defer usageAlertMu.Unlock()
	usageAlertLastFired = map[string]time.Time{}
}

// ---------------------------------------------------------------------------
// Worker: boot-registered goroutine, backup-ticker style
// ---------------------------------------------------------------------------

// startUsageRollupWorker is the single call registered from main.go. Never
// fires under `go test` (main() is not invoked there). USAGE_ROLLUP_DISABLED
// kills the worker; a disabled ledger makes the rollup pointless, so it also
// stands the worker down (loudly — the books being off should never be quiet).
func startUsageRollupWorker(app *kanbanBoardApp) {
	if app == nil || app.memory == nil || boolEnv("USAGE_ROLLUP_DISABLED") {
		return
	}
	if !usageLedgerEnabled() {
		log.Warnf("usage rollup: USAGE_LEDGER_DISABLED is set — no books, no rollup, no alerts")
		return
	}
	log.Infof("usage rollup: armed — every %s, alerts %v, ledger dir %s", usageRollupInterval(), usageAlertsEnabled(), usageLedgerDir())
	go runUsageRollupLoop(app)
}

func runUsageRollupLoop(app *kanbanBoardApp) {
	timer := time.NewTimer(usageRollupFirstRunDelay)
	<-timer.C
	runUsageRollupOnce(app, time.Now())

	ticker := time.NewTicker(usageRollupInterval())
	defer ticker.Stop()
	for range ticker.C {
		runUsageRollupOnce(app, time.Now())
	}
}

// runUsageRollupOnce is one whole pass: fold → living artifact → alerts.
func runUsageRollupOnce(app *kanbanBoardApp, now time.Time) {
	if app == nil || app.memory == nil {
		return
	}
	snap := buildUsageRollupSnapshot(app, now)
	body := renderUsageRollupMarkdown(snap)
	baselineStamp := snap.BaselineStartedAt.Format(time.RFC3339Nano)

	existing, exists := app.osArtifactByID(usageRollupArtifactID)
	if !exists {
		// First publish: fixed-ID artifact, published so every roster user
		// sees it (founder decision 2) — the gmail-consent seed pattern.
		metadata := map[string]string{
			"mode":                         "research",
			"query":                        "LLM spend & health rollup",
			"title":                        usageRollupArtifactTitle,
			"status":                       "published",
			"published":                    "true",
			"publishedBy":                  scoutParticipantName,
			"publishedAt":                  now.UTC().Format(time.RFC3339Nano),
			"type":                         artifactTypeMarkdown,
			artifactVersionMetadataKey:     "1",
			"createdBy":                    scoutParticipantName,
			usageRollupBaselineMetadataKey: baselineStamp,
		}
		entry, appended, err := app.memory.appendOSArtifact(usageRollupArtifactID, body, metadata)
		if err != nil {
			log.Errorf("usage rollup: failed to publish the Spend & Health artifact: %v", err)
		} else if appended {
			emitOSArtifactEvent(entry)
		}
	} else if strings.TrimSpace(existing.Text) != strings.TrimSpace(body) {
		// Update in place only when the numbers moved — a quiet interval
		// leaves the store untouched (no version churn on idle nights).
		if _, _, err := app.updateOSArtifactWithMetadata(usageRollupArtifactID, usageRollupArtifactTitle, body, scoutParticipantName, map[string]string{
			usageRollupBaselineMetadataKey: baselineStamp,
		}); err != nil {
			log.Errorf("usage rollup: failed to update the Spend & Health artifact: %v", err)
		}
	}

	// Daily ledger retention sweep (once per day; the worker itself ticks every
	// ~15m). Runs regardless of the alert kill switch — retention is bookkeeping,
	// not alerting.
	if shouldRunRetentionSweep(now) {
		pruneUsageLedger(now)
	}

	if !usageAlertsEnabled() {
		return
	}
	for _, alert := range evaluateUsageAlerts(snap) {
		if !shouldFireUsageAlert(alert.Kind, now) {
			continue
		}
		if _, err := app.createNotification("", notificationKindAlert, alert.Text, "usage", usageRollupArtifactID, "", false); err != nil {
			log.Errorf("usage rollup: failed to send %s alert: %v", alert.Kind, err)
		}
	}
}

var (
	usageRetentionMu        sync.Mutex
	usageRetentionLastSweep time.Time
)

// shouldRunRetentionSweep is the once-per-day gate for the ledger prune. The
// rollup worker ticks far more often than daily; the in-memory clock (a boot
// re-arming the sweep is harmless — the prune is idempotent) mirrors the
// alert-dedupe pattern.
func shouldRunRetentionSweep(now time.Time) bool {
	usageRetentionMu.Lock()
	defer usageRetentionMu.Unlock()
	if !usageRetentionLastSweep.IsZero() && now.Sub(usageRetentionLastSweep) < 24*time.Hour {
		return false
	}
	usageRetentionLastSweep = now
	return true
}

func resetUsageRetentionSweepForTest() {
	usageRetentionMu.Lock()
	defer usageRetentionMu.Unlock()
	usageRetentionLastSweep = time.Time{}
}

// pruneUsageLedger deletes usage-/eval- JSONL files older than the retention
// window from usageLedgerDir(), returning the count removed. The date in the
// filename (<prefix>-YYYY-MM-DD.jsonl, appendLedgerLine) is authoritative and
// deterministic — parse it rather than trust mtime, which a restore/rsync
// rewrites. Honors the ledger kill switch (a disabled ledger owns no files to
// sweep), skips anything that is not a dated ledger file, and never fails the
// worker. Logs one line per sweep with the removed count.
func pruneUsageLedger(now time.Time) int {
	if !usageLedgerEnabled() {
		return 0
	}
	dir := usageLedgerDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		// The dir is created lazily on the first ledger write; absent means
		// nothing has been written yet, so there is nothing to prune.
		return 0
	}
	cutoff := now.UTC().Add(-usageLedgerRetention)
	removed := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		day, ok := ledgerFileDate(entry.Name())
		if !ok || !day.Before(cutoff) {
			continue
		}
		if err := os.Remove(filepath.Join(dir, entry.Name())); err != nil {
			log.Errorf("usage ledger retention: could not remove %s: %v", entry.Name(), err)
			continue
		}
		removed++
	}
	log.Infof("usage ledger retention sweep: removed %d file(s) older than %d days", removed, usageLedgerRetentionDays)
	return removed
}

// ledgerFileDate parses the UTC day out of a ledger filename
// (<prefix>-YYYY-MM-DD.jsonl). ok is false for any other file, so a stray file
// sharing the ledger dir is never touched.
func ledgerFileDate(name string) (time.Time, bool) {
	for _, prefix := range []string{usageLedgerFilePrefix, evalLedgerFilePrefix} {
		if !strings.HasPrefix(name, prefix+"-") || !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		datePart := strings.TrimSuffix(strings.TrimPrefix(name, prefix+"-"), ".jsonl")
		if day, err := time.Parse("2006-01-02", datePart); err == nil {
			return day, true
		}
	}
	return time.Time{}, false
}

// ---------------------------------------------------------------------------
// Artifact body
// ---------------------------------------------------------------------------

// renderUsageRollupMarkdown renders the living artifact. Deliberately excludes
// GeneratedAt so an unchanged ledger renders byte-identical markdown and the
// worker can skip the store write (freshness rides the artifact's updatedAt).
func renderUsageRollupMarkdown(snap usageRollupSnapshot) string {
	var b strings.Builder
	today := snap.today()

	b.WriteString("# " + usageRollupArtifactTitle + "\n\n")
	b.WriteString("Living rollup of every LLM seat: spend, token flow, and the quality funnel, folded from the usage ledger. Updated automatically; JSON twin at `GET /api/usage/rollup`.\n\n")

	b.WriteString("## Baseline\n\n")
	baselineState := "COMPLETE — flips may proceed one at a time against the frozen numbers"
	if snap.BaselineActive {
		baselineState = fmt.Sprintf("ACTIVE — capturing the pre-flip baseline (first %d days); no model flips until it freezes", snap.BaselineDays)
	}
	b.WriteString(fmt.Sprintf("- Started: %s\n- Status: %s\n\n", snap.BaselineStartedAt.UTC().Format("2006-01-02 15:04 UTC"), baselineState))

	b.WriteString("## Daily spend (UTC days)\n\n")
	b.WriteString("| Date | Calls | Est cost USD | Errors | Fallbacks | Price-missing |\n|---|---|---|---|---|---|\n")
	for _, day := range snap.Days {
		b.WriteString(fmt.Sprintf("| %s | %d | $%.4f | %d | %d | %d |\n",
			day.Date, day.Total.Calls, day.Total.EstCostUSD, day.Total.Errors, day.Total.Fallbacks, day.Total.PriceMissing))
	}
	b.WriteString("\n")

	writeRollupCellTable(&b, "## Today by seat", "Seat", today.Seats)
	writeRollupCellTable(&b, "## Today by model", "Provider/model", today.Models)

	eval := today.Eval
	b.WriteString("## Quality funnel (today)\n\n")
	b.WriteString(fmt.Sprintf("- Strict-JSON parse failures: %d\n", eval.ParseFailures))
	b.WriteString(fmt.Sprintf("- Board ops: %d (%d errors)\n", eval.BoardOps, eval.BoardOpErrors))
	b.WriteString(fmt.Sprintf("- Router turns: %d (%d truncations)\n", eval.RouterOutcomes, eval.RouterTruncations))
	b.WriteString(fmt.Sprintf("- Gate results: %d\n", eval.GateResults))
	b.WriteString(fmt.Sprintf("- Transcript segments: %d (%d failed), correction hits: %d, no-vocab warnings: %d\n", eval.TranscriptSegments, eval.TranscriptFailed, eval.CorrectionHits, eval.NoVocabWarnings))
	b.WriteString(fmt.Sprintf("- Proposals minted/resolved/launched: %d/%d/%d, workflow launches: %d\n\n", eval.ProposalsMinted, eval.ProposalsResolved, eval.ProposalsLaunched, eval.WorkflowLaunches))

	b.WriteString("## Callouts\n\n")
	b.WriteString(fmt.Sprintf("- Ledger: %s, alerts: %s, dropped writes since boot: %d\n", onOff(snap.LedgerEnabled), onOff(snap.AlertsEnabled), snap.DroppedWrites))
	if len(snap.PriceMissingModels) > 0 {
		b.WriteString(fmt.Sprintf("- UNPRICED model ids seen since boot (typo tripwire): %s\n", strings.Join(snap.PriceMissingModels, ", ")))
	} else {
		b.WriteString("- Unpriced model ids: none\n")
	}
	b.WriteString(fmt.Sprintf("- Daily spend alert line: $%.2f (SPEND_ALERT_DAILY_USD)\n", snap.SpendAlertDailyUSD))
	b.WriteString("- Unmetered lanes: none — the private dashboard voice reports through the browser beacon (seat voice_private)\n")

	return b.String()
}

func writeRollupCellTable(b *strings.Builder, heading, keyLabel string, cells map[string]*usageRollupCell) {
	b.WriteString(heading + "\n\n")
	if len(cells) == 0 {
		b.WriteString("No entries yet today.\n\n")
		return
	}
	keys := make([]string, 0, len(cells))
	for key := range cells {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if cells[keys[i]].EstCostUSD != cells[keys[j]].EstCostUSD {
			return cells[keys[i]].EstCostUSD > cells[keys[j]].EstCostUSD
		}
		return keys[i] < keys[j]
	})
	b.WriteString("| " + keyLabel + " | Calls | In | Cached | Cache-write | Out | Audio s | Est cost USD |\n|---|---|---|---|---|---|---|---|\n")
	for _, key := range keys {
		cell := cells[key]
		b.WriteString(fmt.Sprintf("| %s | %d | %d | %d | %d | %d | %.1f | $%.4f |\n",
			key, cell.Calls, cell.InputTokens, cell.CachedInputTokens, cell.CacheCreationTokens, cell.OutputTokens, cell.AudioSeconds, cell.EstCostUSD))
	}
	b.WriteString("\n")
}

func onOff(enabled bool) string {
	if enabled {
		return "on"
	}
	return "off"
}

// ---------------------------------------------------------------------------
// GET /api/usage/rollup — the JSON twin (signed-in members only)
// ---------------------------------------------------------------------------

func usageRollupHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if userFromRequest(r) == nil {
		writeAuthError(w, http.StatusUnauthorized, "not signed in")
		return
	}
	if kanbanApp == nil {
		writeAuthError(w, http.StatusServiceUnavailable, "assistant is unavailable")
		return
	}
	// Fold on demand: 8 days of small JSONL is cheap, and a fresh fold can
	// never serve stale books.
	writeAuthJSON(w, http.StatusOK, buildUsageRollupSnapshot(kanbanApp, time.Now()))
}

// ---------------------------------------------------------------------------
// POST /assistant/realtime/usage — private-voice usage beacon (W0, ratified).
// The browser owns the private voice peer, so the server can only see this
// lane's usage if the page posts each response.done usage object here.
// ---------------------------------------------------------------------------

// privateVoiceBeaconTokenCap bounds every client-claimed token count: nothing
// a single realtime response can legitimately produce comes near it, and it
// keeps a hostile client from inflating the books unboundedly per call.
const privateVoiceBeaconTokenCap = 10_000_000

func assistantRealtimeUsageHandler(w http.ResponseWriter, r *http.Request) {
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

	payload := struct {
		CallID string               `json:"callId"`
		Model  string               `json:"model"`
		Usage  *kanbanRealtimeUsage `json:"usage"`
	}{}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10)).Decode(&payload); err != nil {
		writeAuthError(w, http.StatusBadRequest, "could not read usage beacon")
		return
	}
	if payload.Usage == nil {
		writeAuthError(w, http.StatusBadRequest, "usage is required")
		return
	}
	if !privateVoiceUsageWithinBounds(payload.Usage) {
		writeAuthError(w, http.StatusBadRequest, "usage is out of bounds")
		return
	}

	entry := llmUsageEntry{
		Provider: providerOpenAI,
		Model:    privateVoiceBeaconModel(payload.Model),
		Seat:     seatVoicePrivate,
		// ThreadID carries the realtime response/call id — the join key for
		// per-call analysis; the entry struct has no dedicated call field.
		ThreadID: trimForStorage(payload.CallID, 128),
	}
	if !realtimeUsageTokens(payload.Usage, &entry) {
		writeAuthError(w, http.StatusBadRequest, "usage carries no tokens")
		return
	}
	recordLLMUsage(entry)
	writeAuthJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// privateVoiceBeaconModel trusts the client's claimed model only within the
// realtime family (the only models this lane can run); anything else falls
// back to the server's own dial so a hostile payload can neither bill an
// arbitrary id nor pollute the price_missing tripwire.
func privateVoiceBeaconModel(claimed string) string {
	claimed = strings.ToLower(strings.TrimSpace(claimed))
	if len(claimed) <= 64 && strings.HasPrefix(claimed, "gpt-realtime") {
		return claimed
	}
	return realtimeModel()
}

// privateVoiceUsageWithinBounds rejects negative or absurd token counts —
// the beacon is authed but still browser-supplied data.
func privateVoiceUsageWithinBounds(usage *kanbanRealtimeUsage) bool {
	counts := []int64{usage.TotalTokens, usage.InputTokens, usage.OutputTokens}
	if details := usage.InputTokenDetails; details != nil {
		counts = append(counts, details.TextTokens, details.AudioTokens, details.CachedTokens)
		if cached := details.CachedTokensDetails; cached != nil {
			counts = append(counts, cached.TextTokens, cached.AudioTokens)
		}
	}
	if details := usage.OutputTokenDetails; details != nil {
		counts = append(counts, details.TextTokens, details.AudioTokens)
	}
	for _, count := range counts {
		if count < 0 || count > privateVoiceBeaconTokenCap {
			return false
		}
	}
	return usage.Seconds >= 0 && usage.Seconds <= 86_400
}

// ---------------------------------------------------------------------------
// healthz telemetry block (W0-9 surfacing)
// ---------------------------------------------------------------------------

// healthTelemetrySnapshot extends kanban.go's telemetryLaneSnapshot with the
// ledger/alert posture for /healthz — additive to the existing payload shape.
func healthTelemetrySnapshot() map[string]any {
	snapshot := telemetryLaneSnapshot()
	snapshot["usage_ledger_enabled"] = usageLedgerEnabled()
	snapshot["usage_alerts_enabled"] = usageAlertsEnabled()
	snapshot["usage_ledger_dropped_writes"] = usageLedgerDroppedWrites()
	return snapshot
}
