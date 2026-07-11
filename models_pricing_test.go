package main

import (
	"testing"
	"time"
)

// july11 is the canonical "today" for pricing assertions that predate the
// Sonnet Sep-1 step-up.
var july11 = time.Date(2026, time.July, 11, 12, 0, 0, 0, time.UTC)

// TestModelPriceTableCoversEveryAuditModelID asserts every live dial + seat
// default in the routing audit (docs/llm-routing-audit-2026-07-11.md) resolves
// to a priced row. A missing row is the typo'd-env-flip class of bug this table
// exists to prevent — a new dial must add its row here deliberately.
func TestModelPriceTableCoversEveryAuditModelID(t *testing.T) {
	auditModelIDs := []string{
		// Anthropic executive stack + worker seats.
		"claude-fable-5", "claude-opus-4-8", "claude-sonnet-5", "claude-haiku-4-5",
		// OpenAI text (5.5 today; 5.6 sol/terra/luna the flip targets).
		"gpt-5.5", "gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna",
		// Realtime voice (2 today; 2.1 + 2.1-mini the flip targets).
		"gpt-realtime-2", "gpt-realtime-2.1", "gpt-realtime-2.1-mini",
		// Transcription (whisper fossil today; gpt-4o-transcribe the flip target).
		"gpt-4o-transcribe", "gpt-realtime-whisper",
		// Non-text lanes.
		"gpt-image-2", "text-embedding-3-small",
	}
	for _, id := range auditModelIDs {
		if _, ok := priceForModel(id, july11); !ok {
			t.Errorf("model id %q has no price row (add it to modelPriceTable)", id)
		}
	}
}

// TestEstimateCostUSDPricingMath is the per-model cost table. Every row bills a
// clean 1M-token (or 60s duration) unit so the expected dollar figure is the
// per-MTok rate itself.
func TestEstimateCostUSDPricingMath(t *testing.T) {
	const M = 1_000_000
	tests := []struct {
		name  string
		model string
		usage llmTokenUsage
		want  float64
	}{
		{"opus input", "claude-opus-4-8", llmTokenUsage{InputTokens: M}, 5},
		{"opus cache read (0.1x)", "claude-opus-4-8", llmTokenUsage{CachedInputTokens: M}, 0.50},
		{"opus cache write (1.25x)", "claude-opus-4-8", llmTokenUsage{CacheCreationTokens: M}, 6.25},
		{"opus output", "claude-opus-4-8", llmTokenUsage{OutputTokens: M}, 25},
		{"fable input", "claude-fable-5", llmTokenUsage{InputTokens: M}, 10},
		{"fable output", "claude-fable-5", llmTokenUsage{OutputTokens: M}, 50},
		{"haiku input", "claude-haiku-4-5", llmTokenUsage{InputTokens: M}, 1},
		{"gpt-5.5 input", "gpt-5.5", llmTokenUsage{InputTokens: M}, 5},
		{"gpt-5.5 output", "gpt-5.5", llmTokenUsage{OutputTokens: M}, 30},
		{"luna input", "gpt-5.6-luna", llmTokenUsage{InputTokens: M}, 1},
		{"luna output", "gpt-5.6-luna", llmTokenUsage{OutputTokens: M}, 6},
		{"terra input", "gpt-5.6-terra", llmTokenUsage{InputTokens: M}, 2.50},
		{"sol output", "gpt-5.6-sol", llmTokenUsage{OutputTokens: M}, 30},
		{"realtime-2 audio in", "gpt-realtime-2", llmTokenUsage{AudioInputTokens: M}, 32},
		{"realtime-2 audio out", "gpt-realtime-2", llmTokenUsage{AudioOutputTokens: M}, 64},
		{"realtime-2 text in", "gpt-realtime-2", llmTokenUsage{InputTokens: M}, 4},
		{"realtime-2.1 audio out (price-identical)", "gpt-realtime-2.1", llmTokenUsage{AudioOutputTokens: M}, 64},
		{"realtime-2.1-mini audio in", "gpt-realtime-2.1-mini", llmTokenUsage{AudioInputTokens: M}, 10},
		{"image text in", "gpt-image-2", llmTokenUsage{InputTokens: M}, 5},
		{"image image-in", "gpt-image-2", llmTokenUsage{ImageInputTokens: M}, 8},
		{"embeddings input", "text-embedding-3-small", llmTokenUsage{InputTokens: M}, 0.02},
		// Duration-billed STT lanes: 60s = one minute at the per-minute rate.
		{"gpt-4o-transcribe 1min", "gpt-4o-transcribe", llmTokenUsage{AudioSeconds: 60}, 0.006},
		{"gpt-4o-transcribe 2min", "gpt-4o-transcribe", llmTokenUsage{AudioSeconds: 120}, 0.012},
		{"whisper 1min", "gpt-realtime-whisper", llmTokenUsage{AudioSeconds: 60}, 0.017},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, priced := estimateCostUSDAt(tc.model, july11, tc.usage)
			if !priced {
				t.Fatalf("%s: expected priced=true", tc.model)
			}
			if !floatClose(got, tc.want) {
				t.Fatalf("%s: cost = %v, want %v", tc.model, got, tc.want)
			}
		})
	}
}

// TestEstimateCostUSDCombinedTokenSplit prices a realistic Fable turn with a
// cache-heavy later turn (cache reads >> fresh input), confirming every token
// field contributes and the cache-read discount lands.
func TestEstimateCostUSDCombinedTokenSplit(t *testing.T) {
	got, priced := estimateCostUSDAt("claude-fable-5", july11, llmTokenUsage{
		InputTokens:         100_000, // 0.1M * 10  = 1.00
		CachedInputTokens:   900_000, // 0.9M * 1.0 = 0.90
		CacheCreationTokens: 50_000,  // 0.05M * 12.5 = 0.625
		OutputTokens:        200_000, // 0.2M * 50  = 10.00
	})
	if !priced {
		t.Fatalf("expected priced=true")
	}
	want := 1.00 + 0.90 + 0.625 + 10.00
	if !floatClose(got, want) {
		t.Fatalf("combined cost = %v, want %v", got, want)
	}
}

// TestSonnet5DatedStepUp exercises the intro→step-up boundary: $2/$10 through
// 2026-08-31, $3/$15 from 2026-09-01 (founder decision 5 — eat the step).
func TestSonnet5DatedStepUp(t *testing.T) {
	tests := []struct {
		name          string
		at            time.Time
		wantIn, wantO float64
	}{
		{"intro on July 11", july11, 2, 10},
		{"intro on Aug 31 (last intro day)", time.Date(2026, time.August, 31, 23, 59, 59, 0, time.UTC), 2, 10},
		{"step-up exactly Sep 1 00:00", sonnet5StepUpDate, 3, 15},
		{"step-up on Sep 2", time.Date(2026, time.September, 2, 0, 0, 0, 0, time.UTC), 3, 15},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			row, ok := priceForModel("claude-sonnet-5", tc.at)
			if !ok {
				t.Fatalf("no sonnet-5 row at %v", tc.at)
			}
			if !floatClose(row.InputPerMTok, tc.wantIn) || !floatClose(row.OutputPerMTok, tc.wantO) {
				t.Fatalf("sonnet-5 at %v: in=%v out=%v, want in=%v out=%v",
					tc.at, row.InputPerMTok, row.OutputPerMTok, tc.wantIn, tc.wantO)
			}
			// And the cost path must agree with the resolved row.
			cost, _ := estimateCostUSDAt("claude-sonnet-5", tc.at, llmTokenUsage{InputTokens: 1_000_000})
			if !floatClose(cost, tc.wantIn) {
				t.Fatalf("sonnet-5 cost at %v = %v, want %v", tc.at, cost, tc.wantIn)
			}
		})
	}
}

// TestEstimateCostUSDDefaultsToNowWhenZeroTime confirms the no-date helper
// resolves against the current row (July 11 run resolves to intro Sonnet).
func TestEstimateCostUSDUsesTodayWhenUndated(t *testing.T) {
	// Before the step-up date this must resolve to a valid row regardless of the
	// wall clock at test time; we only assert it is priced and non-negative.
	cost, priced := estimateCostUSD("claude-opus-4-8", llmTokenUsage{InputTokens: 1_000_000})
	if !priced || cost <= 0 {
		t.Fatalf("estimateCostUSD(opus) = (%v, %v), want priced positive cost", cost, priced)
	}
}

// TestUnknownModelWarnsOnceAndCounts covers the price_missing tripwire: cost 0,
// priced=false, the id listed once, and every call counted. A unique id keeps
// the assertion isolated from other tests that share the global accounting.
func TestUnknownModelWarnsOnceAndCounts(t *testing.T) {
	const bogus = "gpt-nonexistent-pricing-probe-42"
	beforeCount := priceMissingCount()

	cost, priced := estimateCostUSD(bogus, llmTokenUsage{InputTokens: 1000})
	if priced || cost != 0 {
		t.Fatalf("unknown model must bill 0 unpriced, got (%v, %v)", cost, priced)
	}
	// Second call: still counted (events), still only one seen entry.
	estimateCostUSD(bogus, llmTokenUsage{OutputTokens: 5})

	if got := priceMissingCount(); got != beforeCount+2 {
		t.Fatalf("priceMissingCount = %d, want %d (every unpriced call counts)", got, beforeCount+2)
	}
	seen := 0
	for _, m := range priceMissingModels() {
		if m == bogus {
			seen++
		}
	}
	if seen != 1 {
		t.Fatalf("unknown model should appear exactly once in priceMissingModels, saw %d", seen)
	}
}

// TestEmptyModelAccountsAsExplicitPlaceholder confirms an empty model id (an
// unset env dial) is still tracked rather than silently blank.
func TestEmptyModelAccountsAsExplicitPlaceholder(t *testing.T) {
	_, priced := estimateCostUSD("", llmTokenUsage{InputTokens: 10})
	if priced {
		t.Fatalf("empty model must be unpriced")
	}
	found := false
	for _, m := range priceMissingModels() {
		if m == "(empty)" {
			found = true
		}
	}
	if !found {
		t.Fatalf("empty model id should record as %q in priceMissingModels", "(empty)")
	}
}

// TestSpendAlertDailyUSDDefaultAndOverride pins the founder-ratified default
// (75) and the env override.
func TestSpendAlertDailyUSDDefaultAndOverride(t *testing.T) {
	t.Setenv("SPEND_ALERT_DAILY_USD", "")
	if got := spendAlertDailyUSD(); !floatClose(got, defaultSpendAlertDailyUSD) || !floatClose(got, 75) {
		t.Fatalf("default SPEND_ALERT_DAILY_USD = %v, want 75", got)
	}
	t.Setenv("SPEND_ALERT_DAILY_USD", "120.5")
	if got := spendAlertDailyUSD(); !floatClose(got, 120.5) {
		t.Fatalf("SPEND_ALERT_DAILY_USD override = %v, want 120.5", got)
	}
}

// TestPriceForModelPicksLatestEffectiveRow guards the dated-row resolver: with
// two rows it must return the newest whose EffectiveFrom is at or before the
// query time, never a future row.
func TestPriceForModelPicksLatestEffectiveRow(t *testing.T) {
	before := time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC)
	after := time.Date(2026, time.October, 1, 0, 0, 0, 0, time.UTC)

	rowBefore, _ := priceForModel("claude-sonnet-5", before)
	if !rowBefore.EffectiveFrom.IsZero() {
		t.Fatalf("pre-step-up row should be the undated intro row, got EffectiveFrom=%v", rowBefore.EffectiveFrom)
	}
	rowAfter, _ := priceForModel("claude-sonnet-5", after)
	if !rowAfter.EffectiveFrom.Equal(sonnet5StepUpDate) {
		t.Fatalf("post-step-up row should be the Sep-1 row, got EffectiveFrom=%v", rowAfter.EffectiveFrom)
	}
}
