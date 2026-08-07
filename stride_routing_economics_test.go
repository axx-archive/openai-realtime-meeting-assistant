package main

import (
	"errors"
	"testing"
	"time"
)

func TestE8RoutingEconomicsManifestIsDeterministicAndCurrentRouteStaysIncumbent(t *testing.T) {
	first := e8TestManifest(t)
	second := e8TestManifest(t)
	if first.Digest != second.Digest {
		t.Fatalf("manifest digest differs for equivalent static inputs: %s != %s", first.Digest, second.Digest)
	}
	current, err := first.CurrentRoute("brain")
	if err != nil {
		t.Fatalf("current route: %v", err)
	}
	if current.Model != "gpt-5.6-terra" {
		t.Fatalf("current model=%q, want incumbent gpt-5.6-terra", current.Model)
	}
	if len(first.Canaries) != 1 || !first.Canaries[0].DefaultOff || !first.Canaries[0].E10Only || first.Canaries[0].Availability != "unverified" {
		t.Fatalf("candidate must remain an explicitly unavailable E10 canary: %#v", first.Canaries)
	}
}

func TestE8RoutingEconomicsFailsClosedOnPriceAndMultipleChanges(t *testing.T) {
	if _, err := NewE8PriceRevision("prices-v1", time.Date(2026, 7, 30, 0, 0, 0, 0, time.UTC), []string{"gpt-future-transcribe"}); !errors.Is(err, ErrE8PriceMissing) {
		t.Fatalf("unpriced future candidate error=%v, want ErrE8PriceMissing", err)
	}
	if _, err := NewE8RouteDescriptor(E8RouteDescriptor{Seat: "brain", Provider: providerOpenAI, Model: "imaginary-model", Effort: "low", PromptDigest: e8Hash("prompt"), SchemaDigest: e8Hash("schema"), SafetyDigest: e8Hash("safety"), RouteRevision: "brain-r1", PriceRevisionDigest: e8Hash("prices")}); !errors.Is(err, ErrE8UnknownModel) {
		t.Fatalf("unknown model error=%v, want ErrE8UnknownModel", err)
	}
	if _, err := NewE8RouteDescriptor(E8RouteDescriptor{Seat: "brain", Provider: providerOpenAI, Model: "gpt-5.6-terra", Effort: "impossible", PromptDigest: e8Hash("prompt"), SchemaDigest: e8Hash("schema"), SafetyDigest: e8Hash("safety"), RouteRevision: "brain-r1", PriceRevisionDigest: e8Hash("prices")}); !errors.Is(err, ErrE8RoutingInvalid) {
		t.Fatalf("unknown effort error=%v, want ErrE8RoutingInvalid", err)
	}
	if _, err := NewE8RouteDescriptor(E8RouteDescriptor{Seat: "made-up-seat", Provider: providerOpenAI, Model: "gpt-5.6-terra", Effort: "low", PromptDigest: e8Hash("prompt"), SchemaDigest: e8Hash("schema"), SafetyDigest: e8Hash("safety"), RouteRevision: "brain-r1", PriceRevisionDigest: e8Hash("prices")}); !errors.Is(err, ErrE8UnknownSeat) {
		t.Fatalf("unknown descriptor seat error=%v, want ErrE8UnknownSeat", err)
	}

	manifest := e8TestManifest(t)
	tooMany := manifest.Canaries[0]
	tooMany.Candidate.Effort = "medium"
	var err error
	tooMany.Candidate, err = NewE8RouteDescriptor(tooMany.Candidate)
	if err != nil {
		t.Fatal(err)
	}
	tooMany, err = NewE8CanaryManifest(tooMany)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Canaries = []E8CanaryManifest{tooMany}
	manifest.Digest = ""
	if _, err := NewE8RoutingEconomicsManifest(manifest); !errors.Is(err, ErrE8TooManyChanges) {
		t.Fatalf("two-variable canary error=%v, want ErrE8TooManyChanges", err)
	}

	unsafe := e8TestManifest(t)
	unsafe.Canaries[0].Candidate.SafetyDigest = e8Hash("new-safety")
	unsafe.Canaries[0].Candidate, err = NewE8RouteDescriptor(unsafe.Canaries[0].Candidate)
	if err != nil {
		t.Fatal(err)
	}
	unsafe.Canaries[0], err = NewE8CanaryManifest(unsafe.Canaries[0])
	if err != nil {
		t.Fatal(err)
	}
	unsafe.Digest = ""
	if _, err := NewE8RoutingEconomicsManifest(unsafe); !errors.Is(err, ErrE8SafetyRegression) {
		t.Fatalf("safety regression error=%v, want ErrE8SafetyRegression", err)
	}
}

func TestE8AccountingRequiresClassifiedFreshNonSyntheticNonFallbackReceipts(t *testing.T) {
	manifest := e8TestManifest(t)
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	route, err := manifest.CurrentRoute("brain")
	if err != nil {
		t.Fatal(err)
	}
	receipt := E8CallReceipt{
		Seat: "brain", RouteDigest: route.Digest, Provider: route.Provider, Model: route.Model, Effort: route.Effort,
		PromptDigest: route.PromptDigest, SchemaDigest: route.SchemaDigest, PriceRevisionDigest: manifest.PriceRevision.Digest,
		ProviderReceiptDigest: e8Hash("provider"), InternalReceiptDigest: e8Hash("internal"), Classification: E8OutputAccepted,
		CostMicros: 50, At: now.Add(-time.Hour),
	}
	if err := receipt.ValidateAgainst(manifest, now); err != nil {
		t.Fatalf("valid accounting receipt: %v", err)
	}
	receipt.Seat = "unknown-seat"
	if err := receipt.ValidateAgainst(manifest, now); !errors.Is(err, ErrE8UnknownSeat) {
		t.Fatalf("unknown seat error=%v", err)
	}
	receipt.Seat = "brain"

	receipt.Synthetic = true
	if err := receipt.ValidateAgainst(manifest, now); !errors.Is(err, ErrE8SyntheticReceipt) {
		t.Fatalf("synthetic receipt error=%v", err)
	}
	receipt.Synthetic = false
	receipt.FallbackRouteDigest = e8Hash("fallback")
	if err := receipt.ValidateAgainst(manifest, now); !errors.Is(err, ErrE8HiddenFallback) {
		t.Fatalf("fallback receipt error=%v", err)
	}
	receipt.FallbackRouteDigest = ""
	receipt.At = now.Add(-25 * time.Hour)
	if err := receipt.ValidateAgainst(manifest, now); !errors.Is(err, ErrE8ReceiptStale) {
		t.Fatalf("stale receipt error=%v", err)
	}

	reconcile := E8ReconciliationReceipt{Seat: "brain", RouteDigest: route.Digest, ProviderTotalMicros: 100000, InternalTotalMicros: 102000, ProviderStatementDigest: e8Hash("statement"), InternalLedgerDigest: e8Hash("ledger"), ObservedAt: now.Add(-time.Hour)}
	if err := reconcile.ValidateAgainst(manifest, now); err != nil {
		t.Fatalf("reconciliation within max(2%%,$.10): %v", err)
	}
	reconcile.InternalTotalMicros = 200001
	if err := reconcile.ValidateAgainst(manifest, now); !errors.Is(err, ErrE8ReconciliationFailed) {
		t.Fatalf("unreconciled receipt error=%v", err)
	}
	reconcile.Synthetic = true
	if err := reconcile.ValidateAgainst(manifest, now); !errors.Is(err, ErrE8SyntheticReceipt) {
		t.Fatalf("synthetic reconciliation error=%v", err)
	}

	circuit := NewE8BudgetCircuit()
	receipt.At = now.Add(-time.Hour)
	for i := 0; i < 3; i++ {
		if decision, err := circuit.Observe(manifest, receipt, now); err != nil || decision.Open {
			t.Fatalf("circuit call %d: decision=%#v err=%v", i+1, decision, err)
		}
	}
	if decision, err := circuit.Observe(manifest, receipt, now); !errors.Is(err, ErrE8BudgetExceeded) || !decision.Open || decision.RollbackRouteDigest != route.Digest {
		t.Fatalf("open circuit decision=%#v err=%v", decision, err)
	}
}

func TestE8ReplayAndSoakArePreparedButNeverActivated(t *testing.T) {
	manifest := e8TestManifest(t)
	replay := E8FinalRouteReplayManifest{FinalRouteMapDigest: manifest.Digest, CorpusDigest: e8Hash("final-corpus"), RollbackMapDigest: manifest.Digest, DefaultOff: true, E10Only: true}
	if err := replay.Validate(); err != nil {
		t.Fatal(err)
	}
	soak := E8SoakManifest{RouteMapDigest: manifest.Digest, MinimumDuration: 24 * time.Hour, MinimumSittings: 10, DefaultOff: true, E10Only: true}
	if err := soak.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestE8PreparedRoutingEconomicsSeedsAllRequiredDefaultOffExperiments(t *testing.T) {
	manifest, err := E8PreparedRoutingEconomicsManifest()
	if err != nil {
		t.Fatalf("prepared routing manifest: %v", err)
	}
	if len(manifest.Incumbents) != 7 || len(manifest.Canaries) != 11 || len(manifest.BlockedCanaries) != 0 {
		t.Fatalf("unexpected prepared seat map: incumbents=%d canaries=%d blocked=%d", len(manifest.Incumbents), len(manifest.Canaries), len(manifest.BlockedCanaries))
	}
	foundSTT := false
	for _, canary := range manifest.Canaries {
		if !canary.DefaultOff || !canary.E10Only || canary.Availability != "unverified" {
			t.Fatalf("prepared canary made an availability claim: %#v", canary)
		}
		if canary.ID == "stt-gpt-live-transcribe-model" {
			foundSTT = canary.Seat == seatTranscriptionLane && canary.Candidate.Model == "gpt-live-transcribe"
		}
	}
	if !foundSTT {
		t.Fatal("priced gpt-live-transcribe candidate is missing from the default-off E10 canaries")
	}
	plan, err := NewE8PreparedRoutingEconomicsPlan()
	if err != nil || plan.Manifest.Digest != manifest.Digest || plan.Replay.FinalRouteMapDigest != manifest.Digest || plan.Soak.MinimumDuration != 24*time.Hour || plan.Soak.MinimumSittings != 10 {
		t.Fatalf("final replay/soak plan=%#v err=%v", plan, err)
	}
}

func TestE8PreparedRoutingEconomicsPinsAug1TerraAndLunaRows(t *testing.T) {
	manifest, err := E8PreparedRoutingEconomicsManifest()
	if err != nil {
		t.Fatalf("prepared routing manifest: %v", err)
	}
	if manifest.PriceRevision.ID != "price-table-20260801" || !manifest.PriceRevision.AsOf.Equal(openAIPriceRefreshAug1) {
		t.Fatalf("price revision = %#v, want 2026-08-01 boundary", manifest.PriceRevision)
	}
	pins := make(map[string]E8ModelPricePin, len(manifest.PriceRevision.Models))
	for _, pin := range manifest.PriceRevision.Models {
		pins[pin.Model] = pin
	}
	for _, model := range []string{"gpt-5.6-terra", "gpt-5.6-luna"} {
		row, ok := priceForModel(model, openAIPriceRefreshAug1)
		if !ok || row.SourceDate != "2026-08-01" {
			t.Fatalf("current %s row = %#v, priced=%v; want 2026-08-01 row", model, row, ok)
		}
		wantDigest, digestErr := STRIDEContractDigest(struct {
			Model string     `json:"model"`
			Row   modelPrice `json:"row"`
		}{model, row})
		if digestErr != nil {
			t.Fatalf("digest %s row: %v", model, digestErr)
		}
		pin, exists := pins[model]
		if !exists || pin.SourceDate != row.SourceDate || pin.RowDigest != wantDigest {
			t.Fatalf("%s price pin = %#v, want source=%q digest=%q", model, pin, row.SourceDate, wantDigest)
		}
	}
}

func e8TestManifest(t *testing.T) E8RoutingEconomicsManifest {
	t.Helper()
	asOf := time.Date(2026, 7, 30, 0, 0, 0, 0, time.UTC)
	prices, err := NewE8PriceRevision("price-table-20260711", asOf, []string{"gpt-5.6-luna", "gpt-5.6-terra"})
	if err != nil {
		t.Fatalf("price revision: %v", err)
	}
	base, err := NewE8RouteDescriptor(E8RouteDescriptor{Seat: "brain", Provider: providerOpenAI, Model: "gpt-5.6-terra", Effort: "low", PromptDigest: e8Hash("prompt-v1"), SchemaDigest: e8Hash("schema-v1"), SafetyDigest: e8Hash("safety-v1"), RouteRevision: "brain-r1", PriceRevisionDigest: prices.Digest, StrictOutputSchema: true, ReadOnlyTools: true})
	if err != nil {
		t.Fatalf("base route: %v", err)
	}
	candidate, err := NewE8RouteDescriptor(E8RouteDescriptor{Seat: "brain", Provider: providerOpenAI, Model: "gpt-5.6-luna", Effort: "low", PromptDigest: base.PromptDigest, SchemaDigest: base.SchemaDigest, SafetyDigest: base.SafetyDigest, RouteRevision: "brain-r1", PriceRevisionDigest: prices.Digest, StrictOutputSchema: true, ReadOnlyTools: true})
	if err != nil {
		t.Fatalf("candidate route: %v", err)
	}
	canary, err := NewE8CanaryManifest(E8CanaryManifest{ID: "brain-luna-model", Seat: "brain", Kind: E8ExperimentModel, BaselineRouteDigest: base.Digest, Candidate: candidate, CorpusDigest: e8Hash("brain-corpus"), RollbackRouteDigest: base.Digest, DefaultOff: true, E10Only: true, Availability: "unverified"})
	if err != nil {
		t.Fatalf("canary: %v", err)
	}
	manifest, err := NewE8RoutingEconomicsManifest(E8RoutingEconomicsManifest{Version: 1, PriceRevision: prices, Incumbents: []E8RouteDescriptor{base}, Canaries: []E8CanaryManifest{canary}, Budgets: []E8RouteBudget{{Seat: "brain", MaxCallCostMicros: 100, MaxWorkflowCostMicros: 1000, MaxCalls: 3, RollbackRouteDigest: base.Digest}}})
	if err != nil {
		t.Fatalf("manifest: %v", err)
	}
	return manifest
}

func e8Hash(value string) string {
	digest, err := STRIDEContractDigest(value)
	if err != nil {
		panic(err)
	}
	return digest
}
