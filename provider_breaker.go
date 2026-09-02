package main

// Wave 9 (Scout never dark) — resilience WITHIN OpenAI.
//
// Doctrine (scout_openai_routes.go): Scout's typed path is OpenAI-owned and no
// Claude fallback lands on a core route without AJ's ratification. This file
// therefore builds only what the doctrine allows:
//
//   - a small per-provider+seat circuit breaker (closed / open with cooldown /
//     half-open single probe) driven by the existing wire-failure classes;
//   - a one-replay same-provider fallback onto the seat's twin model dial
//     (router luna -> terra, answer terra -> luna, extraction luna -> terra)
//     with provenance stamped on the result, the usage ledger, and the eval
//     ledger;
//   - honest breaker evidence for /readyz and /capabilities.
//
// scoutFallbackProvider() is the founder-gated seam for a cross-provider
// fallback. It returns none and must stay that way until ratified.

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	providerBreakerClosed   = "closed"
	providerBreakerOpen     = "open"
	providerBreakerHalfOpen = "half_open"

	// providerBreakerFailureThreshold consecutive classified wire failures on
	// the primary dial open the breaker (routing plan #40: 3 fails -> 10-min
	// cooldown -> half-open). A quota-class failure opens it immediately.
	providerBreakerFailureThreshold = 3
	providerBreakerCooldown         = 10 * time.Minute
)

// Wire-failure classes. Only these feed the breaker; output rejections,
// configuration errors, and caller cancellation never do.
const (
	providerFailureClassQuota            = "quota"
	providerFailureClassRateLimited      = "rate_limited"
	providerFailureClassServerError      = "server_error"
	providerFailureClassTimeout          = "timeout"
	providerFailureClassModelUnavailable = "model_unavailable"
	providerFailureClassTransport        = "transport"
)

const evalKindProviderFallback = "provider_fallback" // fields: seat, primary_model, fallback_model, failure_class, breaker

// scoutFallbackProvider is the founder-gated seam (execution plan Wave 9 D2).
// It intentionally returns no provider: Scout's typed router/answer seats stay
// OpenAI-owned per the doctrine in scout_openai_routes.go until AJ ratifies a
// cross-provider fallback. Nothing may read a non-empty value from here without
// that ratification being recorded in the plan.
func scoutFallbackProvider() string {
	return ""
}

// providerFallbackModelTwin returns the same-provider fallback dial for a
// model. The two admitted Scout text dials are twins of each other so a seat
// on either can replay once on the other without leaving OpenAI. This is the
// model-level half only; seat eligibility (including the hard-pinned seats
// that never get a twin) is decided by seatFallbackModel.
func providerFallbackModelTwin(model string) string {
	switch strings.TrimSpace(model) {
	case "gpt-5.6-luna":
		return "gpt-5.6-terra"
	case "gpt-5.6-terra":
		return "gpt-5.6-luna"
	default:
		return ""
	}
}

// providerResilientSeats lists the text seats that ride the breaker + fallback
// wrapper: Scout's typed router/answer seats, the attachment/proactive
// extraction seats, and every ambient extraction worker seat. Seats with their
// own replay discipline (orchestrator, deliverable, goal engine, agent-thread
// text, images, recall map/reduce) are deliberately absent and pass through
// untouched.
var providerResilientSeats = map[string]bool{
	seatRouter: true, seatChat: true, seatVoiceRecall: true,
	seatAttachments: true, seatProactiveAttention: true,
	seatBrain: true, seatBoard: true, seatSuggestion: true, seatMissionIntel: true,
	seatDecisionLedger: true, seatNarrative: true, seatMeetingDigest: true,
	seatCompanyDigest: true, seatEntityLedger: true, seatSlop: true,
	seatTaste: true, seatHouseStyle: true,
}

func providerResilientSeat(seat string) bool {
	return providerResilientSeats[strings.TrimSpace(seat)]
}

// providerHardPinnedSeats ride the breaker but never a twin dial. Routing plan
// #32 pins the board and suggestion proposal engines to Terra "never ride the
// cheapest tier, even transiently": their output is structured and
// state-changing, so a transient down-tier replay is not an acceptable
// recovery. An open breaker pauses these workers (D4) instead of replaying.
var providerHardPinnedSeats = map[string]bool{
	seatBoard:      true,
	seatSuggestion: true,
}

func providerHardPinnedSeat(seat string) bool {
	return providerHardPinnedSeats[strings.TrimSpace(seat)]
}

// seatFallbackModel resolves the fallback dial for one seat + primary model.
// Empty means the seat has no same-provider replay and only the breaker
// applies: seats outside the resilient set, and hard-pinned seats whose tier
// must never move even for one call.
func seatFallbackModel(seat, primary string) string {
	seat = strings.TrimSpace(seat)
	if !providerResilientSeat(seat) || providerHardPinnedSeat(seat) {
		return ""
	}
	return providerFallbackModelTwin(primary)
}

// providerFailureClassReplayable reports whether a primary failure of this
// class may be replayed on the fallback dial. Quota is account-wide: the twin
// dial bills the same account, so a replay cannot succeed and is not spent.
func providerFailureClassReplayable(class string) bool {
	switch class {
	case providerFailureClassRateLimited, providerFailureClassServerError, providerFailureClassTimeout,
		providerFailureClassModelUnavailable, providerFailureClassTransport:
		return true
	default:
		return false
	}
}

func apiRequestFailureStatusCode(status string) int {
	fields := strings.Fields(strings.TrimSpace(status))
	if len(fields) == 0 {
		return 0
	}
	code, err := strconv.Atoi(fields[0])
	if err != nil {
		return 0
	}
	return code
}

func providerFailureTextLooksLikeQuota(text string) bool {
	text = strings.ToLower(text)
	return strings.Contains(text, "insufficient_quota") || strings.Contains(text, "current quota") ||
		strings.Contains(text, "billing quota") || strings.Contains(text, "billing_not_active") ||
		strings.Contains(text, "exceeded your") && strings.Contains(text, "quota")
}

func providerFailureTextLooksLikeRateLimit(text string) bool {
	text = strings.ToLower(text)
	return strings.Contains(text, "rate_limit") || strings.Contains(text, "rate limit") ||
		strings.Contains(text, "requests per minute") || strings.Contains(text, "tokens per minute")
}

func providerFailureTextLooksLikeTimeout(text string) bool {
	text = strings.ToLower(text)
	return strings.Contains(text, "timeout") || strings.Contains(text, "deadline exceeded") ||
		strings.Contains(text, "timed out")
}

// classifyProviderFailure maps an error to a wire-failure class. The second
// result is false for errors the breaker must ignore: nil, caller
// cancellation, output rejections (a successful wire exchange), configuration
// errors, and non-retryable 4xx client errors.
func classifyProviderFailure(err error) (string, bool) {
	if err == nil || errors.Is(err, context.Canceled) || isProviderOutputRejection(err) {
		return "", false
	}
	var requestFailure *apiRequestFailure
	if errors.As(err, &requestFailure) {
		code := apiRequestFailureStatusCode(requestFailure.status)
		body := requestFailure.body
		switch {
		case providerFailureTextLooksLikeQuota(body):
			return providerFailureClassQuota, true
		case code == 429 || providerFailureTextLooksLikeRateLimit(body):
			return providerFailureClassRateLimited, true
		case code == 404:
			return providerFailureClassModelUnavailable, true
		case code == 408 || code == 504:
			return providerFailureClassTimeout, true
		case code >= 500 && code <= 599:
			return providerFailureClassServerError, true
		default:
			return "", false
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return providerFailureClassTimeout, true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return providerFailureClassTimeout, true
	}
	text := err.Error()
	if !isProviderInvocationFailure(err) {
		return "", false
	}
	switch {
	case providerFailureTextLooksLikeQuota(text):
		return providerFailureClassQuota, true
	case providerFailureTextLooksLikeRateLimit(text):
		return providerFailureClassRateLimited, true
	case providerFailureTextLooksLikeTimeout(text):
		return providerFailureClassTimeout, true
	case strings.Contains(strings.ToLower(text), "overloaded") || strings.Contains(strings.ToLower(text), "server_error"):
		return providerFailureClassServerError, true
	default:
		return providerFailureClassTransport, true
	}
}

type providerBreakerKey struct {
	provider string
	seat     string
}

type providerBreakerRecord struct {
	state               string
	consecutiveFailures int
	openedAt            time.Time
	retryAt             time.Time
	probing             bool
	epoch               uint64
	lastRequestAt       time.Time
	lastSuccessAt       time.Time
	lastFailureAt       time.Time
	lastFailureClass    string
	lastFallbackAt      time.Time
	lastOutcomeFallback bool
	fallbackReplays     uint64
	openReason          string
	// staleSuccesses counts primary successes whose admission epoch no longer
	// matched the record (admitted closed, completed after the breaker opened).
	// They are ignored rather than allowed to re-close the breaker.
	staleSuccesses uint64
}

// providerBreakerSnapshot is the read model for capability health.
type providerBreakerSnapshot struct {
	Provider            string
	Seat                string
	State               string
	ConsecutiveFailures int
	Epoch               uint64
	OpenedAt            time.Time
	RetryAt             time.Time
	OpenReason          string
	LastRequestAt       time.Time
	LastSuccessAt       time.Time
	LastFailureAt       time.Time
	LastFailureClass    string
	LastFallbackAt      time.Time
	FallbackUsed        bool
	FallbackReplays     uint64
	StaleSuccesses      uint64
	Known               bool
}

// FallbackActive reports whether requests on this seat are being served by
// the fallback dial: the breaker is not closed (primary steered away) or the
// most recent completed request was a fallback replay.
func (snapshot providerBreakerSnapshot) FallbackActive() bool {
	return snapshot.Known && (snapshot.State != providerBreakerClosed || snapshot.FallbackUsed)
}

type providerBreakerAdmission struct {
	state        string
	allowPrimary bool
	probe        bool
	retryAt      time.Time
	// epoch is the record epoch at admission. A primary success may only
	// close/reset the breaker when its epoch still matches (M2): a call admitted
	// while closed that lands after the breaker opened proves nothing about
	// the dial now and must not re-close it (oscillation).
	epoch uint64
	// openReason is the failure class that opened the breaker (empty unless
	// open). The open branch consults it: a class the twin dial cannot
	// survive (quota bills the same account) fails fast instead of steering
	// every call on the seat into a doomed replay for the whole cooldown (M1).
	openReason string
}

// providerBreakerOpenClass extracts the wire-failure class from a record's
// openReason ("quota", "server_error", "probe_failed:<class>").
func providerBreakerOpenClass(reason string) string {
	reason = strings.TrimSpace(reason)
	if class, ok := strings.CutPrefix(reason, "probe_failed:"); ok {
		return strings.TrimSpace(class)
	}
	return reason
}

// providerBreakerOpenReplayable reports whether an OPEN breaker may steer the
// seat to its fallback dial. Only the replayable wire classes qualify: an
// account-wide quota (or any other non-replayable class) makes the twin dial
// just as dead, so the honest answer is the fail-closed open error. An empty
// reason (never stamped) keeps the pre-existing steer-to-twin behavior.
func providerBreakerOpenReplayable(reason string) bool {
	class := providerBreakerOpenClass(reason)
	if class == "" {
		return true
	}
	return providerFailureClassReplayable(class)
}

type providerBreakerRegistry struct {
	mu      sync.Mutex
	now     func() time.Time
	records map[providerBreakerKey]*providerBreakerRecord
}

func newProviderBreakerRegistry(now func() time.Time) *providerBreakerRegistry {
	if now == nil {
		now = time.Now
	}
	return &providerBreakerRegistry{now: now, records: map[providerBreakerKey]*providerBreakerRecord{}}
}

// providerBreakers is the process-wide registry. Tests swap the clock through
// setProviderBreakerClockForTest so cooldown never needs a real sleep.
var providerBreakers = newProviderBreakerRegistry(time.Now)

func (registry *providerBreakerRegistry) recordLocked(provider, seat string) *providerBreakerRecord {
	key := providerBreakerKey{provider: strings.TrimSpace(provider), seat: strings.TrimSpace(seat)}
	record := registry.records[key]
	if record == nil {
		record = &providerBreakerRecord{state: providerBreakerClosed}
		registry.records[key] = record
	}
	return record
}

// settleLocked advances an open breaker whose cooldown has elapsed into
// half-open so the next admission may probe the primary dial.
func (registry *providerBreakerRegistry) settleLocked(record *providerBreakerRecord, now time.Time) {
	if record.state == providerBreakerOpen && !now.Before(record.retryAt) {
		record.state = providerBreakerHalfOpen
		record.probing = false
	}
}

func (registry *providerBreakerRegistry) openLocked(record *providerBreakerRecord, now time.Time, reason string) {
	if record.state != providerBreakerOpen {
		record.epoch++
	}
	record.state = providerBreakerOpen
	record.openedAt = now
	record.retryAt = now.Add(providerBreakerCooldown)
	record.probing = false
	record.openReason = reason
}

// admit decides whether a call may try the primary dial. Closed always admits;
// open never does; half-open admits exactly one in-flight probe.
func (registry *providerBreakerRegistry) admit(provider, seat string) providerBreakerAdmission {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	now := registry.now()
	record := registry.recordLocked(provider, seat)
	record.lastRequestAt = now
	registry.settleLocked(record, now)
	admission := providerBreakerAdmission{state: record.state, retryAt: record.retryAt, epoch: record.epoch}
	if record.state != providerBreakerClosed {
		admission.openReason = record.openReason
	}
	switch record.state {
	case providerBreakerClosed:
		admission.allowPrimary = true
	case providerBreakerHalfOpen:
		if !record.probing {
			record.probing = true
			admission.allowPrimary = true
			admission.probe = true
		}
	}
	return admission
}

// recordPrimarySuccess closes the breaker (a probe or an ordinary call proved
// the primary dial healthy) and resets the consecutive-failure count — but
// only for a call admitted under the record's CURRENT epoch. A success whose
// admission predates the open (a long in-flight call admitted while closed)
// is stale evidence: it is counted and otherwise ignored, so it can neither
// re-close the breaker nor zero the failures that opened it. Returns whether
// the success was applied.
func (registry *providerBreakerRegistry) recordPrimarySuccess(provider, seat string, probe bool, epoch uint64) bool {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	now := registry.now()
	record := registry.recordLocked(provider, seat)
	if epoch != record.epoch {
		record.staleSuccesses++
		return false
	}
	record.consecutiveFailures = 0
	record.lastSuccessAt = now
	record.lastOutcomeFallback = false
	if probe || record.state == providerBreakerHalfOpen {
		record.probing = false
	}
	record.state = providerBreakerClosed
	record.openReason = ""
	return true
}

// releaseProbe frees a half-open probe slot after a call that produced no
// wire evidence either way (configuration error, caller cancellation).
func (registry *providerBreakerRegistry) releaseProbe(provider, seat string) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	record := registry.recordLocked(provider, seat)
	record.probing = false
}

// recordPrimaryFailure counts one classified wire failure on the primary dial.
// A failed probe re-opens for a full cooldown; a quota failure opens
// immediately; otherwise the consecutive-failure threshold applies.
func (registry *providerBreakerRegistry) recordPrimaryFailure(provider, seat, class string, probe bool) providerBreakerSnapshot {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	now := registry.now()
	record := registry.recordLocked(provider, seat)
	record.lastFailureAt = now
	record.lastFailureClass = class
	record.consecutiveFailures++
	switch {
	case probe:
		registry.openLocked(record, now, "probe_failed:"+class)
	case class == providerFailureClassQuota:
		registry.openLocked(record, now, class)
	case record.consecutiveFailures >= providerBreakerFailureThreshold && record.state == providerBreakerClosed:
		registry.openLocked(record, now, class)
	}
	return registry.snapshotLocked(provider, seat, record, now)
}

// recordFallbackOutcome records the result of a replay on the fallback dial.
// It never changes the breaker state: the primary dial is what the breaker
// judges, and a steered fallback failure is provider-wide evidence only.
func (registry *providerBreakerRegistry) recordFallbackOutcome(provider, seat string, err error, class string) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	now := registry.now()
	record := registry.recordLocked(provider, seat)
	if err != nil {
		record.lastFailureAt = now
		if class != "" {
			record.lastFailureClass = class
		}
		return
	}
	record.lastSuccessAt = now
	record.lastFallbackAt = now
	record.lastOutcomeFallback = true
	record.fallbackReplays++
}

func (registry *providerBreakerRegistry) snapshotLocked(provider, seat string, record *providerBreakerRecord, now time.Time) providerBreakerSnapshot {
	registry.settleLocked(record, now)
	return providerBreakerSnapshot{
		Provider: strings.TrimSpace(provider), Seat: strings.TrimSpace(seat),
		State: record.state, ConsecutiveFailures: record.consecutiveFailures, Epoch: record.epoch,
		OpenedAt: record.openedAt, RetryAt: record.retryAt, OpenReason: record.openReason,
		LastRequestAt: record.lastRequestAt, LastSuccessAt: record.lastSuccessAt,
		LastFailureAt: record.lastFailureAt, LastFailureClass: record.lastFailureClass,
		LastFallbackAt: record.lastFallbackAt, FallbackUsed: record.lastOutcomeFallback,
		FallbackReplays: record.fallbackReplays, StaleSuccesses: record.staleSuccesses, Known: true,
	}
}

// snapshot reads the effective breaker state for one provider+seat. An
// unknown seat reports closed with Known=false so health surfaces can tell
// "never used" from "closed after traffic".
func (registry *providerBreakerRegistry) snapshot(provider, seat string) providerBreakerSnapshot {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	key := providerBreakerKey{provider: strings.TrimSpace(provider), seat: strings.TrimSpace(seat)}
	record := registry.records[key]
	if record == nil {
		return providerBreakerSnapshot{Provider: key.provider, Seat: key.seat, State: providerBreakerClosed}
	}
	return registry.snapshotLocked(provider, seat, record, registry.now())
}

// paused reports whether background work on this seat should skip its pass:
// only a fully open breaker pauses; half-open lets one pass through as the
// probe that can close it again.
func (registry *providerBreakerRegistry) paused(provider, seat string) (providerBreakerSnapshot, bool) {
	snapshot := registry.snapshot(provider, seat)
	return snapshot, snapshot.State == providerBreakerOpen
}

func (registry *providerBreakerRegistry) reset() {
	registry.mu.Lock()
	registry.records = map[providerBreakerKey]*providerBreakerRecord{}
	registry.mu.Unlock()
}

func (registry *providerBreakerRegistry) setClock(now func() time.Time) {
	if now == nil {
		now = time.Now
	}
	registry.mu.Lock()
	registry.now = now
	registry.mu.Unlock()
}

// providerBreakerOpenError is the fail-closed error for a seat with an open
// breaker and no fallback dial. It carries the provider-invocation marker so
// existing callers classify it exactly like any other provider outage (hold
// the cursor, never dead-letter, "provider_unavailable" for chat).
type providerBreakerOpenError struct {
	Provider string
	Seat     string
	RetryAt  time.Time
	Reason   string
}

func (failure *providerBreakerOpenError) Error() string {
	return fmt.Sprintf("%s %s seat circuit is open (%s); retry after %s", failure.Provider, failure.Seat, firstNonEmptyString(failure.Reason, "repeated failures"), failure.RetryAt.UTC().Format(time.RFC3339))
}

func (failure *providerBreakerOpenError) providerInvocationFailure() {}

// providerCallProvenance is stamped on a resilient seat call's result so the
// caller can see which dial actually answered.
type providerCallProvenance struct {
	Provider            string
	Seat                string
	Model               string
	PrimaryModel        string
	FallbackUsed        bool
	PrimaryFailureClass string
	BreakerState        string
	Probe               bool
}

type providerCallProvenanceCapture struct {
	mu       sync.Mutex
	last     providerCallProvenance
	observed bool
}

type providerCallProvenanceCaptureContextKey struct{}

func withProviderCallProvenanceCapture(ctx context.Context, capture *providerCallProvenanceCapture) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if capture == nil {
		return ctx
	}
	return context.WithValue(ctx, providerCallProvenanceCaptureContextKey{}, capture)
}

func stampProviderCallProvenance(ctx context.Context, provenance providerCallProvenance) {
	if ctx == nil {
		return
	}
	capture, _ := ctx.Value(providerCallProvenanceCaptureContextKey{}).(*providerCallProvenanceCapture)
	if capture == nil {
		return
	}
	capture.mu.Lock()
	capture.last = provenance
	capture.observed = true
	capture.mu.Unlock()
}

func (capture *providerCallProvenanceCapture) snapshot() (providerCallProvenance, bool) {
	if capture == nil {
		return providerCallProvenance{}, false
	}
	capture.mu.Lock()
	defer capture.mu.Unlock()
	return capture.last, capture.observed
}

// withProviderResilience wraps an OpenAI text responder with the per-seat
// breaker and the one-replay same-provider fallback. Seats outside
// providerResilientSeats pass through byte-for-byte. The wrapped responder is
// what production installs as createOpenAITextResponse; tests that swap the
// responder var bypass it unless they wrap their fake explicitly.
//
// Contract:
//   - closed / half-open probe: try the primary dial; on a replayable wire
//     failure replay ONCE on the fallback dial; both fail -> the primary error
//     returns unchanged (today's error path);
//   - open: never touch the primary; serve the fallback dial directly when the
//     class that opened the breaker is replayable, or fail closed with
//     providerBreakerOpenError when the seat has no twin OR the open class is
//     one the twin cannot survive (quota bills the same account);
//   - no path calls the primary twice.
func withProviderResilience(responder openAITextResponder) openAITextResponder {
	if responder == nil {
		return nil
	}
	return func(ctx context.Context, apiKey string, request openAITextRequest) (string, error) {
		seat := strings.TrimSpace(request.Seat)
		if !providerResilientSeat(seat) || request.PreflightError != nil {
			return responder(ctx, apiKey, request)
		}
		if ctx == nil {
			ctx = context.Background()
		}
		primary := strings.TrimSpace(request.Model)
		if primary == "" {
			primary = meetingBrainModel()
		}
		fallback := seatFallbackModel(seat, primary)
		admission := providerBreakers.admit(providerOpenAI, seat)
		provenance := providerCallProvenance{Provider: providerOpenAI, Seat: seat, PrimaryModel: primary, BreakerState: admission.state, Probe: admission.probe}

		replay := func(class string) (string, error) {
			replayRequest := request
			replayRequest.Model = fallback
			replayRequest.FallbackReplay = true
			output, err := responder(ctx, apiKey, replayRequest)
			replayClass, _ := classifyProviderFailure(err)
			providerBreakers.recordFallbackOutcome(providerOpenAI, seat, err, replayClass)
			recordEvalEvent(seat, evalKindProviderFallback, map[string]any{
				"seat": seat, "primary_model": primary, "fallback_model": fallback,
				"failure_class": class, "breaker": admission.state, "accepted": err == nil,
			})
			if err != nil {
				return "", err
			}
			provenance.Model = fallback
			provenance.FallbackUsed = true
			provenance.PrimaryFailureClass = class
			stampProviderCallProvenance(ctx, provenance)
			return output, nil
		}

		if !admission.allowPrimary {
			if fallback == "" || !providerBreakerOpenReplayable(admission.openReason) {
				// No twin, or a twin that would fail the same way (quota is
				// account-wide): the honest answer is the open error, not a
				// replay that is doomed for the whole cooldown.
				return "", &providerBreakerOpenError{Provider: providerOpenAI, Seat: seat, RetryAt: admission.retryAt, Reason: firstNonEmptyString(admission.openReason, "breaker_open")}
			}
			return replay("breaker_open")
		}

		output, err := responder(ctx, apiKey, request)
		if err == nil {
			providerBreakers.recordPrimarySuccess(providerOpenAI, seat, admission.probe, admission.epoch)
			provenance.Model = primary
			stampProviderCallProvenance(ctx, provenance)
			return output, nil
		}
		if isProviderOutputRejection(err) {
			// The wire exchange succeeded; only the output was unusable. That is
			// evidence the primary dial is reachable, never a breaker failure.
			providerBreakers.recordPrimarySuccess(providerOpenAI, seat, admission.probe, admission.epoch)
			return output, err
		}
		class, wire := classifyProviderFailure(err)
		if !wire {
			if admission.probe {
				providerBreakers.releaseProbe(providerOpenAI, seat)
			}
			return output, err
		}
		providerBreakers.recordPrimaryFailure(providerOpenAI, seat, class, admission.probe)
		if fallback == "" || !providerFailureClassReplayable(class) || ctx.Err() != nil {
			return output, err
		}
		replayOutput, replayErr := replay(class)
		if replayErr != nil {
			// Both dials failed: the caller sees exactly today's primary error.
			return output, err
		}
		return replayOutput, nil
	}
}

// providerBreakerEvidence merges breaker truth into a capability lane map:
// lastRequestAt / lastFailureAt / lastFailureClass / fallbackUsed plus a
// nested breaker block. It only ever adds keys the producer did not already
// report, so runtime evidence from recordCapability* stays authoritative.
func providerBreakerEvidence(snap map[string]any, provider, seat string, hasFallbackDial bool) providerBreakerSnapshot {
	breaker := providerBreakers.snapshot(provider, seat)
	snap["fallbackUsed"] = breaker.FallbackUsed
	snap["fallbackDial"] = hasFallbackDial
	if breaker.Known {
		if _, reported := snap["lastRequestAt"]; !reported && !breaker.LastRequestAt.IsZero() {
			snap["lastRequestAt"] = breaker.LastRequestAt.UTC().Format(time.RFC3339Nano)
		}
		if _, reported := snap["lastFailureAt"]; !reported && !breaker.LastFailureAt.IsZero() {
			snap["lastFailureAt"] = breaker.LastFailureAt.UTC().Format(time.RFC3339Nano)
		}
		if breaker.LastFailureClass != "" {
			snap["lastFailureClass"] = breaker.LastFailureClass
		}
		if !breaker.LastFallbackAt.IsZero() {
			snap["lastFallbackAt"] = breaker.LastFallbackAt.UTC().Format(time.RFC3339Nano)
		}
	}
	block := map[string]any{"state": breaker.State, "consecutiveFailures": breaker.ConsecutiveFailures, "seat": breaker.Seat}
	if breaker.State != providerBreakerClosed {
		block["openedAt"] = breaker.OpenedAt.UTC().Format(time.RFC3339Nano)
		block["retryAt"] = breaker.RetryAt.UTC().Format(time.RFC3339Nano)
		block["reason"] = breaker.OpenReason
	}
	if breaker.FallbackReplays > 0 {
		block["fallbackReplays"] = breaker.FallbackReplays
	}
	snap["breaker"] = block
	if breaker.FallbackActive() {
		snap["fallbackActive"] = true
	}
	return breaker
}
