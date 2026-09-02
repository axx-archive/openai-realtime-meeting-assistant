package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
	"time"
)

type breakerTestClock struct {
	mu  sync.Mutex
	now time.Time
}

func (clock *breakerTestClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.now
}

func (clock *breakerTestClock) Advance(d time.Duration) {
	clock.mu.Lock()
	clock.now = clock.now.Add(d)
	clock.mu.Unlock()
}

// resetProviderBreakersForTest isolates the process-wide breaker registry and
// installs a deterministic clock so cooldown tests never sleep.
func resetProviderBreakersForTest(t *testing.T) *breakerTestClock {
	t.Helper()
	clock := &breakerTestClock{now: time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)}
	providerBreakers.reset()
	providerBreakers.setClock(clock.Now)
	t.Cleanup(func() {
		providerBreakers.reset()
		providerBreakers.setClock(time.Now)
	})
	return clock
}

func openAIWireFailure(status, body string) error {
	return &openAIProviderFailure{err: &apiRequestFailure{status: status, body: body}}
}

func TestProviderBreakerOpensAfterThresholdAndHalfOpensAfterCooldown(t *testing.T) {
	clock := resetProviderBreakersForTest(t)
	for attempt := 1; attempt < providerBreakerFailureThreshold; attempt++ {
		if admission := providerBreakers.admit(providerOpenAI, seatRouter); !admission.allowPrimary || admission.state != providerBreakerClosed {
			t.Fatalf("attempt %d admission=%+v, want closed/primary", attempt, admission)
		}
		if snapshot := providerBreakers.recordPrimaryFailure(providerOpenAI, seatRouter, providerFailureClassServerError, false); snapshot.State != providerBreakerClosed {
			t.Fatalf("attempt %d opened early: %+v", attempt, snapshot)
		}
	}
	providerBreakers.admit(providerOpenAI, seatRouter)
	opened := providerBreakers.recordPrimaryFailure(providerOpenAI, seatRouter, providerFailureClassServerError, false)
	if opened.State != providerBreakerOpen || opened.ConsecutiveFailures != providerBreakerFailureThreshold || opened.Epoch != 1 || !opened.RetryAt.Equal(clock.Now().Add(providerBreakerCooldown)) {
		t.Fatalf("threshold failure snapshot=%+v, want open with cooldown", opened)
	}
	if admission := providerBreakers.admit(providerOpenAI, seatRouter); admission.allowPrimary || admission.state != providerBreakerOpen {
		t.Fatalf("open breaker admitted primary: %+v", admission)
	}
	if _, paused := providerBreakers.paused(providerOpenAI, seatRouter); !paused {
		t.Fatal("open breaker must pause background work")
	}

	clock.Advance(providerBreakerCooldown - time.Second)
	if admission := providerBreakers.admit(providerOpenAI, seatRouter); admission.allowPrimary {
		t.Fatalf("breaker half-opened before cooldown: %+v", admission)
	}
	clock.Advance(time.Second)
	probe := providerBreakers.admit(providerOpenAI, seatRouter)
	if !probe.allowPrimary || !probe.probe || probe.state != providerBreakerHalfOpen {
		t.Fatalf("post-cooldown admission=%+v, want half-open probe", probe)
	}
	if _, paused := providerBreakers.paused(providerOpenAI, seatRouter); paused {
		t.Fatal("half-open breaker must let the probe pass through")
	}
	if second := providerBreakers.admit(providerOpenAI, seatRouter); second.allowPrimary {
		t.Fatalf("second caller admitted during probe: %+v", second)
	}

	// A failed probe re-opens for a full cooldown; a successful one closes.
	reopened := providerBreakers.recordPrimaryFailure(providerOpenAI, seatRouter, providerFailureClassServerError, true)
	if reopened.State != providerBreakerOpen || reopened.Epoch != 2 || !reopened.RetryAt.Equal(clock.Now().Add(providerBreakerCooldown)) {
		t.Fatalf("failed probe snapshot=%+v, want reopened epoch 2", reopened)
	}
	clock.Advance(providerBreakerCooldown)
	if probe = providerBreakers.admit(providerOpenAI, seatRouter); !probe.probe {
		t.Fatalf("second cooldown admission=%+v, want probe", probe)
	}
	providerBreakers.recordPrimarySuccess(providerOpenAI, seatRouter, true, probe.epoch)
	closed := providerBreakers.snapshot(providerOpenAI, seatRouter)
	if closed.State != providerBreakerClosed || closed.ConsecutiveFailures != 0 || closed.FallbackActive() {
		t.Fatalf("successful probe snapshot=%+v, want closed", closed)
	}
	if admission := providerBreakers.admit(providerOpenAI, seatRouter); !admission.allowPrimary || admission.probe {
		t.Fatalf("closed breaker admission=%+v", admission)
	}
}

func TestProviderBreakerQuotaOpensImmediately(t *testing.T) {
	resetProviderBreakersForTest(t)
	providerBreakers.admit(providerOpenAI, seatChat)
	snapshot := providerBreakers.recordPrimaryFailure(providerOpenAI, seatChat, providerFailureClassQuota, false)
	if snapshot.State != providerBreakerOpen || snapshot.ConsecutiveFailures != 1 || snapshot.OpenReason != providerFailureClassQuota || snapshot.LastFailureClass != providerFailureClassQuota {
		t.Fatalf("quota snapshot=%+v, want immediately open", snapshot)
	}
	// Seats are independent: the router seat is untouched.
	if other := providerBreakers.snapshot(providerOpenAI, seatRouter); other.Known || other.State != providerBreakerClosed {
		t.Fatalf("quota on chat leaked into router breaker: %+v", other)
	}
}

func TestClassifyProviderFailureUsesExistingWireClasses(t *testing.T) {
	timeoutErr := &openAIProviderFailure{err: fmt.Errorf("create OpenAI response: %w", context.DeadlineExceeded)}
	for _, tc := range []struct {
		name      string
		err       error
		wantClass string
		wantWire  bool
	}{
		{name: "nil", err: nil},
		{name: "429 rate limit", err: openAIWireFailure("429 Too Many Requests", `{"error":{"code":"rate_limit_exceeded"}}`), wantClass: providerFailureClassRateLimited, wantWire: true},
		{name: "429 quota", err: openAIWireFailure("429 Too Many Requests", `{"error":{"code":"insufficient_quota"}}`), wantClass: providerFailureClassQuota, wantWire: true},
		{name: "500", err: openAIWireFailure("500 Internal Server Error", ""), wantClass: providerFailureClassServerError, wantWire: true},
		{name: "529", err: openAIWireFailure("529 Overloaded", ""), wantClass: providerFailureClassServerError, wantWire: true},
		{name: "404 model", err: openAIWireFailure("404 Not Found", `{"error":{"message":"The model gpt-5.6-luna does not exist"}}`), wantClass: providerFailureClassModelUnavailable, wantWire: true},
		{name: "deadline", err: timeoutErr, wantClass: providerFailureClassTimeout, wantWire: true},
		{name: "transport", err: &openAIProviderFailure{err: errors.New("create OpenAI response: dial tcp: connection refused")}, wantClass: providerFailureClassTransport, wantWire: true},
		{name: "output rejection is a wire success", err: &openAIOutputRejection{reason: "empty_output"}},
		{name: "caller cancellation", err: &openAIProviderFailure{err: fmt.Errorf("create OpenAI response: %w", context.Canceled)}},
		{name: "401 is configuration", err: openAIWireFailure("401 Unauthorized", "")},
		{name: "plain error", err: errors.New("boom")},
		{name: "breaker open error is transport-class", err: &providerBreakerOpenError{Provider: providerOpenAI, Seat: seatRouter}, wantClass: providerFailureClassTransport, wantWire: true},
	} {
		class, wire := classifyProviderFailure(tc.err)
		if class != tc.wantClass || wire != tc.wantWire {
			t.Errorf("%s: class=%q wire=%v, want %q/%v", tc.name, class, wire, tc.wantClass, tc.wantWire)
		}
	}
}

type recordedTextRequest struct {
	Model    string
	Fallback bool
}

func TestProviderFallbackReplaysExactlyOnceOn429WithProvenance(t *testing.T) {
	resetProviderBreakersForTest(t)
	dir := ledgerTestDir(t)
	var calls []recordedTextRequest
	resilient := withProviderResilience(func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		calls = append(calls, recordedTextRequest{Model: request.Model, Fallback: request.FallbackReplay})
		if request.Model == defaultScoutRouterModel {
			return "", openAIWireFailure("429 Too Many Requests", `{"error":{"code":"rate_limit_exceeded"}}`)
		}
		return `{"ok":true}`, nil
	})
	capture := &providerCallProvenanceCapture{}
	ctx := withProviderCallProvenanceCapture(context.Background(), capture)
	output, err := resilient(ctx, "key", openAITextRequest{Model: defaultScoutRouterModel, Seat: seatRouter, Workflow: "scout_route", Input: "hi"})
	if err != nil || output != `{"ok":true}` {
		t.Fatalf("output=%q err=%v, want fallback answer", output, err)
	}
	if len(calls) != 2 || calls[0] != (recordedTextRequest{Model: "gpt-5.6-luna"}) || calls[1] != (recordedTextRequest{Model: "gpt-5.6-terra", Fallback: true}) {
		t.Fatalf("calls=%+v, want luna then one terra replay", calls)
	}
	provenance, observed := capture.snapshot()
	if !observed || !provenance.FallbackUsed || provenance.Model != "gpt-5.6-terra" || provenance.PrimaryModel != "gpt-5.6-luna" || provenance.PrimaryFailureClass != providerFailureClassRateLimited || provenance.Provider != providerOpenAI {
		t.Fatalf("provenance=%+v", provenance)
	}
	snapshot := providerBreakers.snapshot(providerOpenAI, seatRouter)
	if snapshot.State != providerBreakerClosed || snapshot.ConsecutiveFailures != 1 || !snapshot.FallbackUsed || !snapshot.FallbackActive() || snapshot.FallbackReplays != 1 || snapshot.LastFailureClass != providerFailureClassRateLimited {
		t.Fatalf("breaker after one replay=%+v", snapshot)
	}
	events := filterLedgerEvents(readRouterLedgerEvents(t, dir), telemetryTypeEval, evalKindProviderFallback)
	if len(events) != 1 || events[0]["lane"] != seatRouter {
		t.Fatalf("provider_fallback events=%v, want exactly one on the router lane", events)
	}
	if fields := ledgerEventFields(events[0]); fields["fallback_model"] != "gpt-5.6-terra" || fields["failure_class"] != providerFailureClassRateLimited || fields["accepted"] != true {
		t.Fatalf("provider_fallback fields=%v", fields)
	}
}

func TestProviderFallbackBothFailReturnsPrimaryErrorUnchanged(t *testing.T) {
	resetProviderBreakersForTest(t)
	primaryErr := openAIWireFailure("500 Internal Server Error", "")
	calls := 0
	resilient := withProviderResilience(func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		calls++
		if request.FallbackReplay {
			return "", openAIWireFailure("503 Service Unavailable", "")
		}
		return "", primaryErr
	})
	_, err := resilient(context.Background(), "key", openAITextRequest{Model: defaultScoutChatModel, Seat: seatChat})
	if !errors.Is(err, primaryErr) {
		t.Fatalf("both-fail err=%v, want the unchanged primary error", err)
	}
	if calls != 2 {
		t.Fatalf("calls=%d, want primary + exactly one replay", calls)
	}
	if snapshot := providerBreakers.snapshot(providerOpenAI, seatChat); snapshot.State != providerBreakerClosed || snapshot.ConsecutiveFailures != 1 || snapshot.FallbackUsed {
		t.Fatalf("breaker after both-fail=%+v", snapshot)
	}
}

func TestProviderFallbackIsNotSpentOnQuotaAndBreakerOpensAtOnce(t *testing.T) {
	resetProviderBreakersForTest(t)
	calls := 0
	quotaErr := openAIWireFailure("429 Too Many Requests", `{"error":{"code":"insufficient_quota","message":"You exceeded your current quota"}}`)
	resilient := withProviderResilience(func(context.Context, string, openAITextRequest) (string, error) {
		calls++
		return "", quotaErr
	})
	if _, err := resilient(context.Background(), "key", openAITextRequest{Model: defaultScoutChatModel, Seat: seatChat}); !errors.Is(err, quotaErr) {
		t.Fatalf("quota err=%v, want unchanged", err)
	}
	if calls != 1 {
		t.Fatalf("calls=%d, want no replay on an account-wide quota failure", calls)
	}
	if snapshot := providerBreakers.snapshot(providerOpenAI, seatChat); snapshot.State != providerBreakerOpen || snapshot.OpenReason != providerFailureClassQuota {
		t.Fatalf("quota breaker=%+v, want open immediately", snapshot)
	}
}

func TestProviderBreakerOpenSteersSeatToFallbackAndProbesAfterCooldown(t *testing.T) {
	clock := resetProviderBreakersForTest(t)
	for i := 0; i < providerBreakerFailureThreshold; i++ {
		providerBreakers.admit(providerOpenAI, seatChat)
		providerBreakers.recordPrimaryFailure(providerOpenAI, seatChat, providerFailureClassServerError, false)
	}
	var calls []recordedTextRequest
	resilient := withProviderResilience(func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		calls = append(calls, recordedTextRequest{Model: request.Model, Fallback: request.FallbackReplay})
		return "answer on " + request.Model, nil
	})
	capture := &providerCallProvenanceCapture{}
	ctx := withProviderCallProvenanceCapture(context.Background(), capture)
	output, err := resilient(ctx, "key", openAITextRequest{Model: defaultScoutChatModel, Seat: seatChat})
	if err != nil || output != "answer on gpt-5.6-luna" {
		t.Fatalf("open-breaker output=%q err=%v, want the luna fallback dial", output, err)
	}
	if len(calls) != 1 || !calls[0].Fallback || calls[0].Model != "gpt-5.6-luna" {
		t.Fatalf("open breaker calls=%+v, want exactly one fallback call and no primary", calls)
	}
	if provenance, _ := capture.snapshot(); !provenance.FallbackUsed || provenance.BreakerState != providerBreakerOpen || provenance.PrimaryFailureClass != "breaker_open" {
		t.Fatalf("steered provenance=%+v", provenance)
	}
	if snapshot := providerBreakers.snapshot(providerOpenAI, seatChat); snapshot.State != providerBreakerOpen || !snapshot.FallbackActive() {
		t.Fatalf("steered breaker=%+v, want still open + fallback active", snapshot)
	}

	clock.Advance(providerBreakerCooldown)
	calls = nil
	if output, err = resilient(ctx, "key", openAITextRequest{Model: defaultScoutChatModel, Seat: seatChat}); err != nil || output != "answer on gpt-5.6-terra" {
		t.Fatalf("probe output=%q err=%v, want primary terra", output, err)
	}
	if len(calls) != 1 || calls[0].Fallback {
		t.Fatalf("probe calls=%+v, want one primary probe", calls)
	}
	if provenance, _ := capture.snapshot(); provenance.FallbackUsed || !provenance.Probe || provenance.BreakerState != providerBreakerHalfOpen {
		t.Fatalf("probe provenance=%+v", provenance)
	}
	if snapshot := providerBreakers.snapshot(providerOpenAI, seatChat); snapshot.State != providerBreakerClosed || snapshot.FallbackActive() {
		t.Fatalf("post-probe breaker=%+v, want closed", snapshot)
	}
}

func TestProviderBreakerOpenWithoutTwinFailsClosedAsProviderOutage(t *testing.T) {
	resetProviderBreakersForTest(t)
	providerBreakers.admit(providerOpenAI, seatAttachments)
	providerBreakers.recordPrimaryFailure(providerOpenAI, seatAttachments, providerFailureClassQuota, false)
	calls := 0
	resilient := withProviderResilience(func(context.Context, string, openAITextRequest) (string, error) {
		calls++
		return "never", nil
	})
	_, err := resilient(context.Background(), "key", openAITextRequest{Model: "gpt-5.5-vision", Seat: seatAttachments})
	var openErr *providerBreakerOpenError
	if !errors.As(err, &openErr) || !isProviderInvocationFailure(err) || calls != 0 {
		t.Fatalf("no-twin open breaker err=%v calls=%d, want fail-closed provider outage", err, calls)
	}
}

func TestProviderResilienceIgnoresUnlistedSeatsAndOutputRejections(t *testing.T) {
	resetProviderBreakersForTest(t)
	calls := 0
	resilient := withProviderResilience(func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		calls++
		if request.Seat == seatOrchestrator {
			return "", openAIWireFailure("500 Internal Server Error", "")
		}
		return "", &openAIOutputRejection{reason: "empty_output"}
	})
	if _, err := resilient(context.Background(), "key", openAITextRequest{Model: defaultScoutChatModel, Seat: seatOrchestrator}); !isProviderInvocationFailure(err) || calls != 1 {
		t.Fatalf("orchestrator seat err=%v calls=%d, want untouched pass-through", err, calls)
	}
	if snapshot := providerBreakers.snapshot(providerOpenAI, seatOrchestrator); snapshot.Known {
		t.Fatalf("unlisted seat reached the breaker: %+v", snapshot)
	}
	calls = 0
	if _, err := resilient(context.Background(), "key", openAITextRequest{Model: defaultScoutRouterModel, Seat: seatRouter}); !isProviderOutputRejection(err) || calls != 1 {
		t.Fatalf("output rejection err=%v calls=%d, want no replay", err, calls)
	}
	if snapshot := providerBreakers.snapshot(providerOpenAI, seatRouter); snapshot.State != providerBreakerClosed || snapshot.ConsecutiveFailures != 0 || snapshot.LastSuccessAt.IsZero() {
		t.Fatalf("output rejection counted as wire failure: %+v", snapshot)
	}
}

// The typed router rides the wrapper end to end: a 5xx on Luna replays once on
// Terra, the routing verdict is unchanged, the router_outcome event carries the
// fallback provenance, and the capability lane reports fallback_active.
func TestScoutRouterSameCallFallbackStampsProvenanceAndLaneState(t *testing.T) {
	resetProviderBreakersForTest(t)
	resetCapabilityRuntimeForTest(t)
	dir := ledgerTestDir(t)
	t.Setenv("OPENAI_API_KEY", "openai-router-test")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("OPENAI_SCOUT_ROUTER_MODEL", "")
	previousApp := kanbanApp
	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "openai-router-test"
	kanbanApp = app
	t.Cleanup(func() { kanbanApp = previousApp })

	var calls []recordedTextRequest
	swapOpenAITextResponder(t, withProviderResilience(func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		calls = append(calls, recordedTextRequest{Model: request.Model, Fallback: request.FallbackReplay})
		if request.Seat != seatRouter || request.Workflow != "scout_route" {
			t.Fatalf("unexpected seat call: %+v", request)
		}
		if !request.FallbackReplay {
			return "", openAIWireFailure("503 Service Unavailable", "")
		}
		return openAIScoutRouteJSON(t, openAIScoutRouterOutput{Outcome: string(conversationIntentConversationalReply)}), nil
	}))

	if verdict := app.routeScoutChatTurn(context.Background(), "what did we decide about the rodeo creator market?", nil); verdict != nil {
		t.Fatalf("verdict=%#v, want inline conversational reply", verdict)
	}
	if len(calls) != 2 || calls[0].Model != defaultScoutRouterModel || calls[0].Fallback || calls[1].Model != defaultScoutChatModel || !calls[1].Fallback {
		t.Fatalf("router calls=%+v, want luna then one terra replay", calls)
	}
	outcomes := filterLedgerEvents(readRouterLedgerEvents(t, dir), telemetryTypeEval, evalKindRouterOutcome)
	if len(outcomes) != 1 {
		t.Fatalf("router_outcome events=%d, want exactly one", len(outcomes))
	}
	fields := ledgerEventFields(outcomes[0])
	if fields["fallbackUsed"] != true || fields["model"] != defaultScoutChatModel || fields["primaryModel"] != defaultScoutRouterModel || fields["primaryFailureClass"] != providerFailureClassServerError || fields["degraded"] != nil {
		t.Fatalf("router_outcome fields=%v, want fallback provenance without a degraded flag", fields)
	}

	snapshot, degraded := capabilitySnapshot(time.Now().UTC())
	router := snapshot["typedScoutRouter"].(map[string]any)
	if router["status"] != capabilityStatusFallbackActive || router["fallbackUsed"] != true || router["lastFailureClass"] != providerFailureClassServerError || router["fallbackModel"] != defaultScoutChatModel {
		t.Fatalf("router lane=%v, want fallback_active with provenance", router)
	}
	if breaker, _ := router["breaker"].(map[string]any); breaker["state"] != providerBreakerClosed || breaker["fallbackReplays"] != uint64(1) {
		t.Fatalf("router breaker block=%v", router["breaker"])
	}
	if slices.Contains(degraded, "typedScoutRouter") || slices.Contains(degraded, capabilityScout) {
		t.Fatalf("fallback_active counted as degraded: %v", degraded)
	}
	if scout := snapshot["scout"].(map[string]any); scout["status"] != capabilityStatusFallbackActive {
		t.Fatalf("scout aggregate=%v, want fallback_active", scout["status"])
	}
}

func TestCapabilityStatusDistinguishesIdleDegradedAndFallbackStates(t *testing.T) {
	success := time.Now().UTC().Format(time.RFC3339Nano)
	for _, tc := range []struct {
		name          string
		base          map[string]any
		providerReady bool
		want          string
	}{
		{name: "never used", base: map[string]any{"enabled": true}, providerReady: true, want: capabilityStatusIdle},
		{name: "stale success without failure", base: map[string]any{"enabled": true, "lastSuccessAt": success, "stale": true}, providerReady: true, want: capabilityStatusIdle},
		{name: "recent success", base: map[string]any{"enabled": true, "lastSuccessAt": success, "stale": false}, providerReady: true, want: capabilityStatusHealthy},
		{name: "last request failed", base: map[string]any{"enabled": true, "lastSuccessAt": success, "lastError": "503"}, providerReady: true, want: capabilityStatusDegraded},
		{name: "provider key missing", base: map[string]any{"enabled": true, "lastSuccessAt": success}, providerReady: false, want: capabilityStatusDegraded},
		{name: "fallback in use", base: map[string]any{"enabled": true, "lastSuccessAt": success, "fallbackActive": true}, providerReady: true, want: capabilityStatusFallbackActive},
		{name: "paused by breaker", base: map[string]any{"enabled": true, "pausedByBreaker": true}, providerReady: true, want: capabilityStatusPausedByBreaker},
		{name: "breaker pause beats fallback", base: map[string]any{"enabled": true, "pausedByBreaker": true, "fallbackActive": true}, providerReady: true, want: capabilityStatusPausedByBreaker},
		{name: "breaker pause beats the failures that opened it", base: map[string]any{"enabled": true, "pausedByBreaker": true, "lastError": "503", "circuit": "open"}, providerReady: true, want: capabilityStatusPausedByBreaker},
		{name: "persistence fault beats breaker pause", base: map[string]any{"enabled": true, "pausedByBreaker": true, "circuit": "persistence_error"}, providerReady: true, want: capabilityStatusDegraded},
		{name: "continuity fault beats breaker pause", base: map[string]any{"enabled": true, "pausedByBreaker": true, "continuityError": true}, providerReady: true, want: capabilityStatusDegraded},
		{name: "missing key beats breaker pause", base: map[string]any{"enabled": true, "pausedByBreaker": true}, providerReady: false, want: capabilityStatusDegraded},
		{name: "failure beats fallback", base: map[string]any{"enabled": true, "lastError": "x", "fallbackActive": true}, providerReady: true, want: capabilityStatusDegraded},
		{name: "unallocated disconnected", base: map[string]any{"enabled": true, "connected": false}, providerReady: true, want: capabilityStatusIdle},
		{name: "allocated disconnected", base: map[string]any{"enabled": true, "connected": false, "allocated": true}, providerReady: true, want: capabilityStatusDegraded},
		{name: "legacy retry circuit", base: map[string]any{"enabled": true, "lastSuccessAt": success, "circuit": "open"}, providerReady: true, want: capabilityStatusDegraded},
		{name: "overdue specialty work", base: map[string]any{"enabled": true, "lastSuccessAt": success, "stale": true, "overdue": true}, providerReady: true, want: capabilityStatusDegraded},
		{name: "disabled", base: map[string]any{"enabled": false, "lastError": "x"}, providerReady: true, want: capabilityStatusDisabled},
	} {
		if got := capabilityStatus(tc.base, tc.providerReady); got != tc.want {
			t.Errorf("%s: status=%q, want %q", tc.name, got, tc.want)
		}
	}
	lane := func(status string) map[string]any { return map[string]any{"status": status} }
	if got := aggregateCapabilityStatus(lane(capabilityStatusIdle), lane(capabilityStatusIdle)); got != capabilityStatusIdle {
		t.Errorf("all idle aggregate=%q, want idle", got)
	}
	if got := aggregateCapabilityStatus(lane(capabilityStatusIdle), lane(capabilityStatusHealthy)); got != capabilityStatusHealthy {
		t.Errorf("idle+healthy aggregate=%q, want healthy", got)
	}
	if got := aggregateCapabilityStatus(lane(capabilityStatusHealthy), lane(capabilityStatusFallbackActive)); got != capabilityStatusFallbackActive {
		t.Errorf("fallback aggregate=%q, want fallback_active", got)
	}
	if got := aggregateCapabilityStatus(lane(capabilityStatusFallbackActive), lane(capabilityStatusDegraded)); got != capabilityStatusDegraded {
		t.Errorf("degraded aggregate=%q, want degraded", got)
	}
}

// An idle system — key present, no traffic, no errors — is exactly what
// production showed on 2026-09-01 with every lane "degraded". /readyz must now
// report those lanes idle, keep them out of the degraded list, and carry the
// honest lane shape.
func TestReadinessScoutLanesReportIdleNotDegradedWithHonestShape(t *testing.T) {
	resetProviderBreakersForTest(t)
	resetCapabilityRuntimeForTest(t)
	t.Setenv("OPENAI_API_KEY", "test-openai-key")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("BACKUP_DISABLED", "true")
	previousApp := kanbanApp
	app := newIsolatedKanbanBoardApp(t)
	kanbanApp = app
	t.Cleanup(func() { kanbanApp = previousApp })

	recorder := httptest.NewRecorder()
	readinessHandler(recorder, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	var payload struct {
		Degraded     []string                   `json:"degraded"`
		Capabilities map[string]json.RawMessage `json:"capabilities"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode readiness: %v", err)
	}
	var scout struct {
		Status string                    `json:"status"`
		Lanes  map[string]map[string]any `json:"lanes"`
	}
	if err := json.Unmarshal(payload.Capabilities["scout"], &scout); err != nil {
		t.Fatalf("decode scout capability: %v", err)
	}
	if scout.Status != capabilityStatusIdle {
		t.Fatalf("idle scout aggregate=%q, want idle", scout.Status)
	}
	for _, name := range []string{"roomVoice", "privateVoice", "typedRouter", "typedAnswer"} {
		lane := scout.Lanes[name]
		if lane == nil {
			t.Fatalf("scout.lanes.%s missing: %v", name, scout.Lanes)
		}
		if lane["status"] != capabilityStatusIdle || lane["fallbackUsed"] != false {
			t.Errorf("scout.lanes.%s=%v, want idle with fallbackUsed=false", name, lane)
		}
		for _, key := range []string{"lastRequestAt", "lastSuccessAt", "lastFailureAt", "lastFailureClass"} {
			if _, present := lane[key]; present {
				t.Errorf("scout.lanes.%s.%s present on a lane that never ran: %v", name, key, lane)
			}
		}
	}
	if breaker, _ := scout.Lanes["typedRouter"]["breaker"].(map[string]any); breaker["state"] != providerBreakerClosed {
		t.Fatalf("typedRouter breaker=%v, want closed", scout.Lanes["typedRouter"]["breaker"])
	}
	for _, name := range []string{capabilityScout, capabilitySTT, "typedScoutRouter", "typedScoutAnswer", "meetingSTT", "dictation", "roomVoice", "privateVoice", "recap", "workflows", "brain"} {
		if slices.Contains(payload.Degraded, name) {
			t.Errorf("idle lane %q counted as degraded: %v", name, payload.Degraded)
		}
	}
	for _, name := range []string{"meetingSTT", "recap", "workflows", "brain"} {
		var lane map[string]any
		if err := json.Unmarshal(payload.Capabilities[name], &lane); err != nil {
			t.Fatalf("decode %s: %v", name, err)
		}
		if lane["status"] != capabilityStatusIdle {
			t.Errorf("%s status=%v, want idle", name, lane["status"])
		}
	}

	// A real failure is still degraded, and the lane says when and what.
	now := time.Now().UTC()
	recordCapabilityPoll(capabilityTypedScoutRouter, now)
	recordCapabilityFailure(capabilityTypedScoutRouter, now, errors.New("api request failed (503 Service Unavailable)"))
	snapshot, degraded := capabilitySnapshot(now)
	router := snapshot["typedScoutRouter"].(map[string]any)
	if router["status"] != capabilityStatusDegraded || router["lastRequestAt"] == nil || router["lastFailureAt"] == nil {
		t.Fatalf("failed router lane=%v, want degraded with request/failure timestamps", router)
	}
	if !slices.Contains(degraded, "typedScoutRouter") || !slices.Contains(degraded, capabilityScout) {
		t.Fatalf("degraded=%v, want typedScoutRouter + scout", degraded)
	}
	recordCapabilityPoll(capabilityTypedScoutRouter, now.Add(time.Second))
	recordCapabilitySuccess(capabilityTypedScoutRouter, now.Add(time.Second))
	snapshot, degraded = capabilitySnapshot(now.Add(2 * time.Second))
	router = snapshot["typedScoutRouter"].(map[string]any)
	if router["status"] != capabilityStatusHealthy || router["lastSuccessAt"] == nil || router["lastFailureAt"] == nil || slices.Contains(degraded, "typedScoutRouter") {
		t.Fatalf("recovered router lane=%v degraded=%v, want healthy with history retained", router, degraded)
	}
	// Idle again once the evidence window passes with no new request.
	snapshot, degraded = capabilitySnapshot(now.Add(10 * time.Minute))
	router = snapshot["typedScoutRouter"].(map[string]any)
	if router["status"] != capabilityStatusIdle || slices.Contains(degraded, "typedScoutRouter") || slices.Contains(degraded, capabilityScout) {
		t.Fatalf("aged router lane=%v degraded=%v, want idle and not degraded", router, degraded)
	}
}

func TestAllocatedTranscriptionLaneStillDegradesWhenDisconnected(t *testing.T) {
	resetProviderBreakersForTest(t)
	resetCapabilityRuntimeForTest(t)
	t.Setenv("OPENAI_API_KEY", "test-openai-key")
	previousApp := kanbanApp
	app := newIsolatedKanbanBoardApp(t)
	kanbanApp = app
	t.Cleanup(func() { kanbanApp = previousApp })

	snapshot, degraded := capabilitySnapshot(time.Now().UTC())
	if meeting := snapshot["meetingSTT"].(map[string]any); meeting["status"] != capabilityStatusIdle || meeting["allocated"] != false || slices.Contains(degraded, "meetingSTT") {
		t.Fatalf("unallocated STT lane=%v degraded=%v, want idle", meeting, degraded)
	}
	app.mu.Lock()
	app.transcriptLane = newMeetingTranscriptionLane(app, "test-openai-key", "gpt-transcribe")
	app.mu.Unlock()
	snapshot, degraded = capabilitySnapshot(time.Now().UTC())
	if meeting := snapshot["meetingSTT"].(map[string]any); meeting["status"] != capabilityStatusDegraded || meeting["allocated"] != true || !slices.Contains(degraded, "meetingSTT") {
		t.Fatalf("allocated but disconnected STT lane=%v degraded=%v, want degraded", meeting, degraded)
	}
}

// D4: an open breaker on the worker's seat skips the pass without spending a
// provider call, a held-window checkpoint, or the attempt budget; the cursor
// stays put; the capability entry reports paused_by_breaker; and the first
// pass after the cooldown is the probe that closes the breaker again.
func TestAmbientWorkerPausesBehindOpenSeatBreakerAndResumesAsProbe(t *testing.T) {
	clock := resetProviderBreakersForTest(t)
	resetCapabilityRuntimeForTest(t)
	t.Setenv("OPENAI_API_KEY", "test-openai-key")
	t.Setenv("ANTHROPIC_API_KEY", "")
	previousApp := kanbanApp
	app := newIsolatedKanbanBoardApp(t)
	kanbanApp = app
	t.Cleanup(func() { kanbanApp = previousApp })
	appendTestTranscript(t, app, "breaker-held-transcript", "This transcript waits out the breaker.")

	var produced [][]string
	agent := newTestAmbientAgent(&produced)
	agent.defaultMinBatch = 1
	agent.providerSeat = seatBrain
	// On-demand specialty contract: no supervisor is required for health.
	agent.healthWorkDue = func(*kanbanBoardApp, time.Time) bool { return false }
	baseProduce := agent.produce
	agent.produce = func(app *kanbanBoardApp, ctx context.Context, apiKey string, inputs []meetingMemoryEntry, responder openAITextResponder) (meetingMemoryEntry, error) {
		if _, err := responder(ctx, apiKey, openAITextRequest{Model: defaultMeetingBrainModel, Seat: seatBrain, Input: "fold"}); err != nil {
			return meetingMemoryEntry{}, err
		}
		return baseProduce(app, ctx, apiKey, inputs, responder)
	}
	wireCalls := 0
	responder := withProviderResilience(func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		wireCalls++
		return "ok", nil
	})

	providerBreakers.admit(providerOpenAI, seatBrain)
	providerBreakers.recordPrimaryFailure(providerOpenAI, seatBrain, providerFailureClassQuota, false)
	key := ambientAgentScopeKey(agent, officeRoomID)
	for pass := 0; pass < 3; pass++ {
		_, err := app.invokeAmbientAgentGuarded(agent, context.Background(), "test-openai-key", responder, 1, officeRoomID)
		var circuitErr *ambientAgentCircuitOpenError
		if !errors.As(err, &circuitErr) || !circuitErr.PausedByBreaker || circuitErr.RestartRequired {
			t.Fatalf("pass %d err=%v, want paused-by-breaker circuit error", pass, err)
		}
	}
	if wireCalls != 0 || len(produced) != 0 {
		t.Fatalf("paused worker still ran: wire=%d produced=%v", wireCalls, produced)
	}
	app.mu.Lock()
	failure := app.agentFailures[key]
	app.mu.Unlock()
	if failure != nil {
		t.Fatalf("breaker pause spent the worker's attempt budget: %+v", failure)
	}
	if headID, count, _, ok := app.peekUnconsumedWindow(agent, officeRoomID); !ok || headID != "breaker-held-transcript" || count != 1 {
		t.Fatalf("breaker pause moved the cursor: head=%q count=%d ok=%v", headID, count, ok)
	}
	if _, _, err := app.ambientHeldWindow(key); err != nil {
		t.Fatalf("held window read: %v", err)
	}
	ambientBreakerPauseLog.Lock()
	loggedEpoch := ambientBreakerPauseLog.epochs[agent.name]
	ambientBreakerPauseLog.Unlock()
	if loggedEpoch != providerBreakers.snapshot(providerOpenAI, seatBrain).Epoch {
		t.Fatalf("pause log epoch=%d, want the open epoch logged once", loggedEpoch)
	}
	health := ambientWorkerCapabilitySnapshot(agent, clock.Now(), providerOpenAI, true)
	if health["status"] != capabilityStatusPausedByBreaker || health["pausedByBreaker"] != true || health["lastFailureClass"] != providerFailureClassQuota || health["retryAt"] == nil {
		t.Fatalf("paused worker health=%v", health)
	}
	if breaker, _ := health["breaker"].(map[string]any); breaker["state"] != providerBreakerOpen || breaker["reason"] != providerFailureClassQuota {
		t.Fatalf("paused worker breaker block=%v", health["breaker"])
	}

	clock.Advance(providerBreakerCooldown)
	if _, err := app.invokeAmbientAgentGuarded(agent, context.Background(), "test-openai-key", responder, 1, officeRoomID); err != nil {
		t.Fatalf("post-cooldown probe pass: %v", err)
	}
	if wireCalls != 1 || len(produced) != 1 || produced[0][0] != "breaker-held-transcript" {
		t.Fatalf("probe pass wire=%d produced=%v, want one pass over the held transcript", wireCalls, produced)
	}
	if snapshot := providerBreakers.snapshot(providerOpenAI, seatBrain); snapshot.State != providerBreakerClosed {
		t.Fatalf("breaker after probe pass=%+v, want closed", snapshot)
	}
	health = ambientWorkerCapabilitySnapshot(agent, clock.Now(), providerOpenAI, true)
	if health["status"] == capabilityStatusPausedByBreaker || health["pausedByBreaker"] == true {
		t.Fatalf("resumed worker still reports paused: %v", health)
	}
}

// Routing plan #32: the board and suggestion proposal engines are hard-pinned
// to their tier and never ride a twin dial, even for one replay. They still
// ride the breaker so an outage pauses them instead of hammering the provider.
func TestProviderHardPinnedSeatsHaveNoFallbackTwin(t *testing.T) {
	if got := seatFallbackModel(seatBoard, meetingBoardModel()); got != "" {
		t.Fatalf("seatFallbackModel(board, %s)=%q, want no twin (hard-pinned tier)", meetingBoardModel(), got)
	}
	if got := seatFallbackModel(seatSuggestion, researchSuggestionModel()); got != "" {
		t.Fatalf("seatFallbackModel(suggestion, %s)=%q, want no twin (hard-pinned tier)", researchSuggestionModel(), got)
	}
	if !providerResilientSeat(seatBoard) || !providerResilientSeat(seatSuggestion) {
		t.Fatal("hard-pinned seats must still ride the provider breaker")
	}
	if providerFallbackModelTwin(meetingBoardModel()) == "" {
		t.Fatal("model-level twin exists; the pin must come from seat awareness, not a missing dial")
	}
	if got := seatFallbackModel(seatBrain, meetingBrainModel()); got != "gpt-5.6-luna" {
		t.Fatalf("ordinary extraction seat twin=%q, want luna", got)
	}
}

// A real board pass with an injected 500 makes exactly one wire call on the
// pinned model, never replays on Luna, and an open breaker pauses the next
// pass without any call at all.
func TestBoardSeatPassWithInjectedServerErrorNeverReplaysOnLuna(t *testing.T) {
	resetProviderBreakersForTest(t)
	t.Setenv("ANTHROPIC_API_KEY", "")
	app := newIsolatedKanbanBoardApp(t)
	if _, appended, err := app.memory.appendBrainWriteUp("board-pin-brain", "## Overview\nA decision the board worker must fold into cards.", map[string]string{"visibility": "organization"}); err != nil || !appended {
		t.Fatalf("append brain: appended=%v err=%v", appended, err)
	}
	var calls []recordedTextRequest
	serverErr := openAIWireFailure("500 Internal Server Error", "")
	responder := withProviderResilience(func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		calls = append(calls, recordedTextRequest{Model: request.Model, Fallback: request.FallbackReplay})
		if request.Seat != seatBoard {
			t.Fatalf("board pass used seat %q", request.Seat)
		}
		return "", serverErr
	})
	agent := meetingBoardAgent()
	key := ambientAgentScopeKey(agent, officeRoomID)
	if _, err := app.invokeAmbientAgentGuarded(agent, context.Background(), "test-key", responder, 1, officeRoomID); err == nil {
		t.Fatal("board pass with injected 500 returned no error")
	}
	if len(calls) != 1 || calls[0].Model != meetingBoardModel() || calls[0].Fallback {
		t.Fatalf("board calls=%+v, want exactly one primary call on %s and no Luna replay", calls, meetingBoardModel())
	}
	if snapshot := providerBreakers.snapshot(providerOpenAI, seatBoard); snapshot.State != providerBreakerClosed || snapshot.ConsecutiveFailures != 1 || snapshot.FallbackUsed || snapshot.FallbackReplays != 0 {
		t.Fatalf("board breaker after 500=%+v, want one counted failure and no replay", snapshot)
	}

	providerBreakers.admit(providerOpenAI, seatBoard)
	providerBreakers.recordPrimaryFailure(providerOpenAI, seatBoard, providerFailureClassQuota, false)
	expireAmbientAgentBackoffForTest(app, key)
	_, err := app.invokeAmbientAgentGuarded(agent, context.Background(), "test-key", responder, 1, officeRoomID)
	var circuitErr *ambientAgentCircuitOpenError
	if !errors.As(err, &circuitErr) || !circuitErr.PausedByBreaker {
		t.Fatalf("open-breaker board pass err=%v, want paused by breaker", err)
	}
	if len(calls) != 1 {
		t.Fatalf("open breaker still called the provider: %+v", calls)
	}
}

func TestScoutFallbackProviderSeamStaysClosedUntilRatified(t *testing.T) {
	if provider := scoutFallbackProvider(); provider != "" {
		t.Fatalf("scoutFallbackProvider()=%q, want none: Claude fallback on typed seats is founder-gated (scout_openai_routes.go doctrine)", provider)
	}
	if seatFallbackModel(seatRouter, defaultScoutRouterModel) != defaultScoutChatModel || seatFallbackModel(seatChat, defaultScoutChatModel) != defaultScoutRouterModel {
		t.Fatal("router/answer twin dials must stay within OpenAI (luna <-> terra)")
	}
	if seatFallbackModel(seatOrchestrator, defaultScoutChatModel) != "" {
		t.Fatal("non-resilient seats must not gain a fallback dial")
	}
}
