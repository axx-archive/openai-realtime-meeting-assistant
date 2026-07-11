// models_pricing.go — the single dated price table (W0 item 2 of
// docs/model-routing-master-plan-2026-07-11.md; merged harness+telemetry
// duplicates — built exactly once, owned here).
//
// One row per billable model id the repo can dial, USD per MILLION tokens
// (or USD per minute on duration-billed STT lanes). Rows are DATED: a model
// may carry multiple rows with EffectiveFrom boundaries (Sonnet 5's intro
// price steps up 2026-09-01) and priceForModel picks by the entry timestamp.
//
// Sources (2026-07-11): https://developers.openai.com/api/docs/pricing,
// https://platform.claude.com/docs/en/about-claude/pricing, and the audited
// figures in docs/llm-routing-audit-2026-07-11.md (webResearch + configAudit
// sections). Anthropic cache reads bill 0.1x input, 5-min cache writes 1.25x
// input; OpenAI cache reads keep the 90% discount everywhere, cache writes
// bill 1.25x input from GPT-5.6 onward (free before).
//
// Consumers: recordLLMUsage (est_cost_usd), the W0 rollup/alert engine, W4
// boot seat validation, and the Fable cost-awareness prompt. estimateCostUSD
// never fails: an unknown model id warns ONCE per id (the typo'd-env-flip
// tripwire), returns cost 0 with priced=false, and is counted + listed for the
// rollup's price_missing callout.
package main

import (
	"sort"
	"sync"
	"time"
)

// SPEND_ALERT_DAILY_USD default — founder-ratified 2026-07-11 (decision 2/11;
// audit models heavy days at ~$50 typical). The W0 alert engine reads
// spendAlertDailyUSD().
const defaultSpendAlertDailyUSD = 75.0

// spendAlertDailyUSD is the daily spend alert threshold in USD
// (SPEND_ALERT_DAILY_USD override, default 75).
func spendAlertDailyUSD() float64 {
	return floatEnv("SPEND_ALERT_DAILY_USD", defaultSpendAlertDailyUSD)
}

// modelPrice is one dated pricing row. All *PerMTok fields are USD per 1M
// tokens; PerMinuteUSD covers duration-billed STT lanes (token fields zero
// there). Zero fields mean "this modality is not billed / not published".
type modelPrice struct {
	InputPerMTok       float64
	CachedInputPerMTok float64 // cache READS
	CacheWritePerMTok  float64 // cache WRITES (0 = writes not billed)
	OutputPerMTok      float64

	AudioInputPerMTok       float64
	CachedAudioInputPerMTok float64
	AudioOutputPerMTok      float64

	ImageInputPerMTok float64 // gpt-image-2 image-input tier

	PerMinuteUSD float64 // duration-billed lanes (gpt-4o-transcribe, gpt-realtime-whisper)

	// EffectiveFrom dates the row: zero time = since forever. When a model has
	// several rows, priceForModel picks the latest row whose EffectiveFrom is
	// at or before the entry timestamp.
	EffectiveFrom time.Time
	// SourceDate documents when the figure was verified against the official
	// pricing page ("2026-07-11").
	SourceDate string
}

// sonnet5StepUpDate: Sonnet 5 intro pricing ($2/$10) expires 2026-08-31; the
// $3/$15 row takes effect 2026-09-01 UTC (founder decision 5: eat it).
var sonnet5StepUpDate = time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC)

// modelPriceTable: model id → dated rows (ascending EffectiveFrom). Every seat
// default and live env dial in the audit inventory has a row here —
// models_pricing_test.go enforces that.
var modelPriceTable = map[string][]modelPrice{
	// ---- Anthropic (per platform.claude.com pricing, verified 2026-07-11) ----
	"claude-fable-5": {{
		InputPerMTok: 10, CachedInputPerMTok: 1.00, CacheWritePerMTok: 12.50, OutputPerMTok: 50,
		SourceDate: "2026-07-11",
	}},
	"claude-opus-4-8": {{
		InputPerMTok: 5, CachedInputPerMTok: 0.50, CacheWritePerMTok: 6.25, OutputPerMTok: 25,
		SourceDate: "2026-07-11",
	}},
	"claude-sonnet-5": {
		{ // intro pricing through 2026-08-31
			InputPerMTok: 2, CachedInputPerMTok: 0.20, CacheWritePerMTok: 2.50, OutputPerMTok: 10,
			SourceDate: "2026-07-11",
		},
		{ // step-up from 2026-09-01
			InputPerMTok: 3, CachedInputPerMTok: 0.30, CacheWritePerMTok: 3.75, OutputPerMTok: 15,
			EffectiveFrom: sonnet5StepUpDate, SourceDate: "2026-07-11",
		},
	},
	"claude-haiku-4-5": {{ // doctrine-refused, priced for completeness
		InputPerMTok: 1, CachedInputPerMTok: 0.10, CacheWritePerMTok: 1.25, OutputPerMTok: 5,
		SourceDate: "2026-07-11",
	}},

	// ---- OpenAI text (developers.openai.com/api/docs/pricing, 2026-07-11) ----
	// gpt-5.5: cache reads $0.50; cache writes not billed pre-5.6.
	"gpt-5.5": {{
		InputPerMTok: 5, CachedInputPerMTok: 0.50, OutputPerMTok: 30,
		SourceDate: "2026-07-11",
	}},
	// GPT-5.6 family (GA 2026-07-09): cache writes bill 1.25x uncached input.
	"gpt-5.6-sol": {{
		InputPerMTok: 5, CachedInputPerMTok: 0.50, CacheWritePerMTok: 6.25, OutputPerMTok: 30,
		SourceDate: "2026-07-11",
	}},
	"gpt-5.6-terra": {{
		InputPerMTok: 2.50, CachedInputPerMTok: 0.25, CacheWritePerMTok: 3.125, OutputPerMTok: 15,
		SourceDate: "2026-07-11",
	}},
	"gpt-5.6-luna": {{
		InputPerMTok: 1, CachedInputPerMTok: 0.10, CacheWritePerMTok: 1.25, OutputPerMTok: 6,
		SourceDate: "2026-07-11",
	}},

	// ---- OpenAI realtime voice (2 and 2.1 are price-identical) ----
	"gpt-realtime-2": {{
		InputPerMTok: 4, CachedInputPerMTok: 0.40, OutputPerMTok: 24,
		AudioInputPerMTok: 32, CachedAudioInputPerMTok: 0.40, AudioOutputPerMTok: 64,
		SourceDate: "2026-07-11",
	}},
	"gpt-realtime-2.1": {{
		InputPerMTok: 4, CachedInputPerMTok: 0.40, OutputPerMTok: 24,
		AudioInputPerMTok: 32, CachedAudioInputPerMTok: 0.40, AudioOutputPerMTok: 64,
		SourceDate: "2026-07-11",
	}},
	"gpt-realtime-2.1-mini": {{
		InputPerMTok: 0.60, CachedInputPerMTok: 0.06, OutputPerMTok: 2.40,
		AudioInputPerMTok: 10, CachedAudioInputPerMTok: 0.30, AudioOutputPerMTok: 20,
		SourceDate: "2026-07-11",
	}},

	// ---- STT (duration-billed, per minute of audio) ----
	"gpt-4o-transcribe":    {{PerMinuteUSD: 0.006, SourceDate: "2026-07-11"}},
	"gpt-realtime-whisper": {{PerMinuteUSD: 0.017, SourceDate: "2026-07-11"}},

	// ---- Images ----
	// gpt-image-2: text in $5 ($1.25 cached), image in $8, image out $30/MTok.
	// Per-image dollar figures are calculator estimates, not list prices.
	"gpt-image-2": {{
		InputPerMTok: 5, CachedInputPerMTok: 1.25, ImageInputPerMTok: 8, OutputPerMTok: 30,
		SourceDate: "2026-07-11",
	}},

	// ---- Embeddings ----
	"text-embedding-3-small": {{InputPerMTok: 0.02, SourceDate: "2026-07-11"}},
}

// priceForModel resolves the dated row for a model id at a given time. False
// when the id has no row at all (the caller decides whether to warn — the
// estimate path warns once per id).
func priceForModel(model string, at time.Time) (modelPrice, bool) {
	rows, ok := modelPriceTable[model]
	if !ok || len(rows) == 0 {
		return modelPrice{}, false
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	best := -1
	for i, row := range rows {
		if row.EffectiveFrom.IsZero() || !row.EffectiveFrom.After(at) {
			if best < 0 || rows[i].EffectiveFrom.After(rows[best].EffectiveFrom) {
				best = i
			}
		}
	}
	if best < 0 {
		// Every dated row starts in the future (shouldn't happen for a sane
		// table); bill at the earliest known row rather than silently zero.
		best = 0
	}
	return rows[best], true
}

// llmTokenUsage is the billable-unit bundle estimateCostUSD prices. Mirrors
// the token fields of llmUsageEntry; fill what the wire reported.
type llmTokenUsage struct {
	InputTokens         int64
	CachedInputTokens   int64 // cache reads
	CacheCreationTokens int64 // cache writes
	OutputTokens        int64

	AudioInputTokens       int64
	CachedAudioInputTokens int64
	AudioOutputTokens      int64

	ImageInputTokens int64

	AudioSeconds float64 // duration-billed lanes
}

// estimateCostUSD prices usage against today's row for the model. Unknown
// model → warn once per id, cost 0, priced=false, counted (price_missing
// tripwire). Never fails.
func estimateCostUSD(model string, usage llmTokenUsage) (float64, bool) {
	return estimateCostUSDAt(model, time.Now().UTC(), usage)
}

// estimateCostUSDAt is estimateCostUSD with an explicit pricing date — the
// ledger passes the entry timestamp so the Sonnet Sep-1 step-up (and any
// future dated row) bills correctly.
func estimateCostUSDAt(model string, at time.Time, usage llmTokenUsage) (float64, bool) {
	price, ok := priceForModel(model, at)
	if !ok {
		notePriceMissing(model)
		return 0, false
	}
	const mtok = 1e6
	cost := float64(usage.InputTokens)/mtok*price.InputPerMTok +
		float64(usage.CachedInputTokens)/mtok*price.CachedInputPerMTok +
		float64(usage.CacheCreationTokens)/mtok*price.CacheWritePerMTok +
		float64(usage.OutputTokens)/mtok*price.OutputPerMTok +
		float64(usage.AudioInputTokens)/mtok*price.AudioInputPerMTok +
		float64(usage.CachedAudioInputTokens)/mtok*price.CachedAudioInputPerMTok +
		float64(usage.AudioOutputTokens)/mtok*price.AudioOutputPerMTok +
		float64(usage.ImageInputTokens)/mtok*price.ImageInputPerMTok +
		usage.AudioSeconds/60*price.PerMinuteUSD
	return cost, true
}

// ---------------------------------------------------------------------------
// Unknown-model accounting (warn once per id, count all, list for the rollup)
// ---------------------------------------------------------------------------

var (
	priceMissingMu     sync.Mutex
	priceMissingSeen   = map[string]bool{}
	priceMissingEvents int64
)

func notePriceMissing(model string) {
	if model == "" {
		model = "(empty)"
	}
	priceMissingMu.Lock()
	first := !priceMissingSeen[model]
	priceMissingSeen[model] = true
	priceMissingEvents++
	priceMissingMu.Unlock()
	if first {
		log.Warnf("models_pricing: no price row for model %q — est_cost_usd=0, entries flagged price_missing (typo'd env flip?)", model)
	}
}

// priceMissingModels lists every unknown model id seen since boot (sorted) —
// the rollup's price_missing callout; any entry here is an alert condition.
func priceMissingModels() []string {
	priceMissingMu.Lock()
	defer priceMissingMu.Unlock()
	models := make([]string, 0, len(priceMissingSeen))
	for model := range priceMissingSeen {
		models = append(models, model)
	}
	sort.Strings(models)
	return models
}

// priceMissingCount is the total number of unpriced estimate calls since boot.
func priceMissingCount() int64 {
	priceMissingMu.Lock()
	defer priceMissingMu.Unlock()
	return priceMissingEvents
}
