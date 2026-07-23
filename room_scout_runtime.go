package main

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	ErrRoomScoutClosed       = errors.New("room Scout runtime is closed")
	ErrRoomScoutFence        = errors.New("room Scout runtime fence mismatch")
	ErrRoomScoutUnauthorized = errors.New("room Scout tool principal is unauthorized")
)

// RoomScoutScope is the immutable identity carried by every provider callback.
// A callback from an earlier sitting or media generation is discarded before
// it can publish audio, events, transcripts, or tools into the current room.
type RoomScoutScope struct {
	RoomID          string `json:"roomId"`
	SittingID       string `json:"sittingId"`
	MediaGeneration uint64 `json:"mediaGeneration"`
}

func (scope RoomScoutScope) valid() bool {
	return normalizeRoomID(scope.RoomID) == strings.TrimSpace(scope.RoomID) &&
		strings.TrimSpace(scope.SittingID) != "" && scope.MediaGeneration > 0
}

func (scope RoomScoutScope) same(other RoomScoutScope) bool {
	return normalizeRoomID(scope.RoomID) == normalizeRoomID(other.RoomID) &&
		strings.TrimSpace(scope.SittingID) == strings.TrimSpace(other.SittingID) &&
		scope.MediaGeneration == other.MediaGeneration
}

func (app *kanbanBoardApp) roomScoutScopeCurrentLocked(scope RoomScoutScope) bool {
	if app == nil || !scope.valid() {
		return false
	}
	state := app.roomLiveLocked(scope.RoomID)
	return state.mediaGen == scope.MediaGeneration && state.realtime != nil && state.realtime.scope.same(scope)
}

type RoomScoutStatus string

const (
	RoomScoutStarting RoomScoutStatus = "starting"
	RoomScoutReady    RoomScoutStatus = "ready"
	RoomScoutDegraded RoomScoutStatus = "degraded"
	RoomScoutClosed   RoomScoutStatus = "closed"
)

// RoomScoutTransport is provider-specific and intentionally narrower than the
// room media plane. Provider failures are recorded on the bundle and swallowed
// at the mixer seam so they can never take video, admission, or transcription
// down with Scout.
type RoomScoutTransport interface {
	WriteMixedPCM(context.Context, []int16) error
	Close() error
}

// RoomScoutBufferedAudioCanceler is the optional fail-closed withdrawal seam.
// A concrete provider transport must clear both buffered input and any response
// derived from it. A transport without this capability is closed and degraded;
// direct room media remains independent.
type RoomScoutBufferedAudioCanceler interface {
	CancelBufferedAudio(context.Context) error
}

type RoomScoutCallbacks struct {
	Publish func(RoomScoutScope, string, any) bool
	RunTool func(context.Context, RoomScoutScope, string, ACLPrincipal, func(context.Context) error) error
	Status  func(RoomScoutScope, RoomScoutStatus, error) bool
}

type RoomScoutTransportFactory func(context.Context, RoomScoutScope, RoomScoutCallbacks) (RoomScoutTransport, error)

type roomRealtimeBundle struct {
	mu sync.Mutex

	scope        RoomScoutScope
	ctx          context.Context
	cancel       context.CancelFunc
	transport    RoomScoutTransport
	status       RoomScoutStatus
	lastError    string
	handledCalls map[string]struct{}
	publish      func(string, any)
	audioInput   chan []int16
	audioWriteMu sync.Mutex
	audioPaused  bool
	workEpoch    uint64
	workCtx      context.Context
	workCancel   context.CancelFunc
	workInFlight map[uint64]int
	workCond     *sync.Cond
	// contributorFences conservatively accumulates every server-authorized
	// model-lane principal sent since the prior provider commit. The provider
	// has erased identity from PCM, so its fallback transcript must retain this
	// complete set through the attribution FIFO.
	contributorFences map[string]ConsentFence
}

type roomScoutRuntimeSnapshot struct {
	Scope     RoomScoutScope  `json:"scope"`
	Status    RoomScoutStatus `json:"status"`
	LastError string          `json:"lastError,omitempty"`
}

func newRoomRealtimeBundle(scope RoomScoutScope, publish func(string, any)) (*roomRealtimeBundle, error) {
	if !scope.valid() {
		return nil, ErrRoomScoutFence
	}
	ctx, cancel := context.WithCancel(context.Background())
	workCtx, workCancel := context.WithCancel(ctx)
	bundle := &roomRealtimeBundle{
		scope: scope, ctx: ctx, cancel: cancel, status: RoomScoutStarting,
		handledCalls: map[string]struct{}{}, publish: publish, audioInput: make(chan []int16, 8),
		workEpoch: 1, workCtx: workCtx, workCancel: workCancel, workInFlight: map[uint64]int{},
		contributorFences: map[string]ConsentFence{},
	}
	bundle.workCond = sync.NewCond(&bundle.mu)
	return bundle, nil
}

func (bundle *roomRealtimeBundle) start(factory RoomScoutTransportFactory) {
	if bundle == nil {
		return
	}
	if factory == nil {
		bundle.markDegraded(errors.New("room Scout transport is not configured"))
		return
	}
	transport, err := factory(bundle.ctx, bundle.scope, RoomScoutCallbacks{
		Publish: bundle.publishFenced,
		RunTool: bundle.runToolFenced,
		Status:  bundle.setStatusFenced,
	})
	if err != nil {
		bundle.markDegraded(err)
		return
	}
	if transport == nil {
		bundle.markDegraded(errors.New("room Scout transport factory returned no transport"))
		return
	}
	bundle.mu.Lock()
	if bundle.status == RoomScoutClosed {
		bundle.mu.Unlock()
		if transport != nil {
			_ = transport.Close()
		}
		return
	}
	bundle.transport = transport
	bundle.status = RoomScoutReady
	bundle.lastError = ""
	bundle.mu.Unlock()
	go bundle.runAudioInput(transport)
}

func (bundle *roomRealtimeBundle) snapshot() roomScoutRuntimeSnapshot {
	if bundle == nil {
		return roomScoutRuntimeSnapshot{}
	}
	bundle.mu.Lock()
	defer bundle.mu.Unlock()
	return roomScoutRuntimeSnapshot{Scope: bundle.scope, Status: bundle.status, LastError: bundle.lastError}
}

func (bundle *roomRealtimeBundle) markDegraded(err error) {
	if bundle == nil || err == nil {
		return
	}
	bundle.mu.Lock()
	if bundle.status != RoomScoutClosed {
		bundle.status = RoomScoutDegraded
		bundle.lastError = trimForStorage(err.Error(), 300)
	}
	bundle.mu.Unlock()
}

func (bundle *roomRealtimeBundle) setStatusFenced(scope RoomScoutScope, status RoomScoutStatus, err error) bool {
	if bundle == nil || !bundle.scope.same(scope) {
		return false
	}
	bundle.mu.Lock()
	defer bundle.mu.Unlock()
	if bundle.status == RoomScoutClosed {
		return false
	}
	switch status {
	case RoomScoutStarting, RoomScoutReady, RoomScoutDegraded:
		bundle.status = status
	default:
		return false
	}
	if err != nil {
		bundle.lastError = trimForStorage(err.Error(), 300)
	} else if status == RoomScoutReady {
		bundle.lastError = ""
	}
	return true
}

func (bundle *roomRealtimeBundle) writeMixedPCM(samples []int16) {
	bundle.writeMixedPCMWithConsent(samples, nil)
}

func (bundle *roomRealtimeBundle) writeMixedPCMWithConsent(samples []int16, fences []ConsentFence) {
	if bundle == nil || len(samples) == 0 {
		return
	}
	bundle.mu.Lock()
	status, paused, ctx, audioInput := bundle.status, bundle.audioPaused, bundle.ctx, bundle.audioInput
	for _, fence := range fences {
		if fence.binding.Validate() == nil {
			key := consentBindingKey(fence.binding)
			// Retain the first fence until the provider commits/clears. Replacing
			// it after a remote withdrawal/re-grant could make earlier buffered
			// speech appear to have arrived under the newer authority. The final
			// transcript commit validates this original fence and fails closed.
			if _, exists := bundle.contributorFences[key]; !exists {
				bundle.contributorFences[key] = fence
			}
		}
	}
	bundle.mu.Unlock()
	if status != RoomScoutReady || paused {
		return
	}
	copySamples := append([]int16(nil), samples...)
	select {
	case audioInput <- copySamples:
	case <-ctx.Done():
	default:
		// Provider backpressure sheds Scout input, never room media. The status
		// remains ready because a later frame can still recover the session.
	}
}

func (bundle *roomRealtimeBundle) runAudioInput(transport RoomScoutTransport) {
	for {
		select {
		case <-bundle.ctx.Done():
			return
		case samples := <-bundle.audioInput:
			bundle.audioWriteMu.Lock()
			bundle.mu.Lock()
			paused := bundle.audioPaused || bundle.status != RoomScoutReady
			bundle.mu.Unlock()
			if paused {
				bundle.audioWriteMu.Unlock()
				continue
			}
			if err := transport.WriteMixedPCM(bundle.ctx, samples); err != nil {
				bundle.audioWriteMu.Unlock()
				bundle.markDegraded(fmt.Errorf("room Scout audio input: %w", err))
				return
			}
			bundle.audioWriteMu.Unlock()
		}
	}
}

// cancelBufferedAudio is called by consent withdrawal after new ingress has
// been fenced. It pauses producers, waits for any in-flight provider write,
// drains the local queue, then clears provider-owned input/response state.
func (bundle *roomRealtimeBundle) cancelBufferedAudio() error {
	if bundle == nil {
		return nil
	}
	bundle.mu.Lock()
	if bundle.status == RoomScoutClosed {
		bundle.mu.Unlock()
		return ErrRoomScoutClosed
	}
	bundle.audioPaused = true
	transport := bundle.transport
	oldEpoch := bundle.workEpoch
	oldWorkCancel := bundle.workCancel
	bundle.workEpoch++
	bundle.workCtx, bundle.workCancel = context.WithCancel(bundle.ctx)
	bundle.contributorFences = map[string]ConsentFence{}
	bundle.mu.Unlock()

	// A withdrawal is an epoch transition for every uncommitted provider
	// callback/tool, not just audio. Cancel first, then wait for the old epoch to
	// acknowledge cancellation before clearing provider-owned buffers. New work
	// can only enter the new epoch after the durable withdrawal is already live.
	oldWorkCancel()
	bundle.mu.Lock()
	for bundle.workInFlight[oldEpoch] > 0 {
		bundle.workCond.Wait()
	}
	delete(bundle.workInFlight, oldEpoch)
	bundle.mu.Unlock()

	bundle.audioWriteMu.Lock()
	for {
		select {
		case <-bundle.audioInput:
			continue
		default:
		}
		break
	}
	canceler, supported := transport.(RoomScoutBufferedAudioCanceler)
	var err error
	if !supported || canceler == nil {
		err = fmt.Errorf("room Scout transport cannot clear buffered audio")
	} else {
		err = canceler.CancelBufferedAudio(bundle.ctx)
	}
	bundle.audioWriteMu.Unlock()

	// Fence the provider before publishing the terminal degraded state. This
	// makes that state the last word even if a recovery callback was already in
	// flight when withdrawal cancellation failed.
	if err != nil && transport != nil {
		_ = transport.Close()
	}

	bundle.mu.Lock()
	if err == nil {
		if bundle.status == RoomScoutReady {
			bundle.audioPaused = false
		}
	} else if bundle.status != RoomScoutClosed {
		bundle.status = RoomScoutDegraded
		bundle.lastError = trimForStorage(err.Error(), 300)
		bundle.transport = nil
	}
	bundle.mu.Unlock()
	return err
}

func (bundle *roomRealtimeBundle) takeContributorFences() []ConsentFence {
	if bundle == nil {
		return nil
	}
	bundle.mu.Lock()
	defer bundle.mu.Unlock()
	fences := make([]ConsentFence, 0, len(bundle.contributorFences))
	for _, fence := range bundle.contributorFences {
		fences = append(fences, fence)
	}
	bundle.contributorFences = map[string]ConsentFence{}
	sort.Slice(fences, func(i, j int) bool {
		return consentBindingKey(fences[i].binding) < consentBindingKey(fences[j].binding)
	})
	return fences
}

func (bundle *roomRealtimeBundle) publishFenced(scope RoomScoutScope, event string, payload any) bool {
	if bundle == nil || strings.TrimSpace(event) == "" {
		return false
	}
	bundle.mu.Lock()
	allowed := bundle.status != RoomScoutClosed && bundle.scope.same(scope)
	publish := bundle.publish
	if !allowed || publish == nil {
		bundle.mu.Unlock()
		return false
	}
	// Publish while the bundle lock is held so close() is the linearization
	// fence: a callback is wholly before close or wholly rejected after it.
	publish(event, payload)
	bundle.mu.Unlock()
	return true
}

func (bundle *roomRealtimeBundle) runToolFenced(ctx context.Context, scope RoomScoutScope, callID string, principal ACLPrincipal, run func(context.Context) error) error {
	if bundle == nil || !bundle.scope.same(scope) {
		return ErrRoomScoutFence
	}
	if principal.Kind != ACLPrincipalUser && principal.Kind != ACLPrincipalService {
		return ErrRoomScoutUnauthorized
	}
	if principal.TenantID == "" || principal.ID == "" || normalizeRoomID(principal.RoomID) != normalizeRoomID(scope.RoomID) || strings.TrimSpace(principal.SittingID) != strings.TrimSpace(scope.SittingID) {
		return ErrRoomScoutUnauthorized
	}
	callID = strings.TrimSpace(callID)
	if callID == "" || run == nil {
		return ErrRoomScoutUnauthorized
	}
	bundle.mu.Lock()
	if bundle.status == RoomScoutClosed {
		bundle.mu.Unlock()
		return ErrRoomScoutClosed
	}
	if _, exists := bundle.handledCalls[callID]; exists {
		bundle.mu.Unlock()
		return nil
	}
	bundle.handledCalls[callID] = struct{}{}
	epoch := bundle.workEpoch
	epochCtx := bundle.workCtx
	bundle.workInFlight[epoch]++
	bundle.mu.Unlock()
	defer func() {
		bundle.mu.Lock()
		bundle.workInFlight[epoch]--
		if bundle.workInFlight[epoch] == 0 {
			bundle.workCond.Broadcast()
		}
		bundle.mu.Unlock()
	}()

	if ctx == nil {
		ctx = context.Background()
	}
	toolCtx, cancel := context.WithCancel(epochCtx)
	defer cancel()
	go func() {
		select {
		case <-ctx.Done():
			cancel()
		case <-toolCtx.Done():
		}
	}()
	err := run(toolCtx)
	bundle.mu.Lock()
	current := bundle.status != RoomScoutClosed && bundle.workEpoch == epoch && bundle.scope.same(scope)
	bundle.mu.Unlock()
	if !current {
		return ErrRoomScoutFence
	}
	return err
}

func (bundle *roomRealtimeBundle) close() error {
	if bundle == nil {
		return nil
	}
	bundle.mu.Lock()
	if bundle.status == RoomScoutClosed {
		bundle.mu.Unlock()
		return nil
	}
	bundle.status = RoomScoutClosed
	transport := bundle.transport
	bundle.transport = nil
	workEpoch := bundle.workEpoch
	workCancel := bundle.workCancel
	bundle.cancel()
	bundle.mu.Unlock()
	workCancel()
	bundle.mu.Lock()
	for bundle.workInFlight[workEpoch] > 0 {
		bundle.workCond.Wait()
	}
	delete(bundle.workInFlight, workEpoch)
	bundle.mu.Unlock()
	if transport != nil {
		return transport.Close()
	}
	return nil
}

// The provider's attribution callbacks mutate room-local FIFOs. They therefore
// need the same exact room+sitting+generation fence as transcript persistence;
// checking only in the outer provider callback leaves a rollover window before
// the FIFO mutation lands.
func (app *kanbanBoardApp) noteRoomScoutSpeechStarted(scope RoomScoutScope) bool {
	if app == nil {
		return false
	}
	app.mu.Lock()
	defer app.mu.Unlock()
	if !app.roomScoutScopeCurrentLocked(scope) {
		return false
	}
	state := app.roomLiveLocked(scope.RoomID)
	state.currentSpeechStartedAt = time.Now().UTC()
	state.currentSpeechStoppedAt = time.Time{}
	return true
}

func (app *kanbanBoardApp) noteRoomScoutSpeechStopped(scope RoomScoutScope) bool {
	if app == nil {
		return false
	}
	app.mu.Lock()
	defer app.mu.Unlock()
	if !app.roomScoutScopeCurrentLocked(scope) {
		return false
	}
	app.roomLiveLocked(scope.RoomID).currentSpeechStoppedAt = time.Now().UTC()
	return true
}

func (app *kanbanBoardApp) freezeRoomScoutAttribution(scope RoomScoutScope) bool {
	return app.freezeRoomScoutAttributionWithConsent(scope, nil)
}

func (app *kanbanBoardApp) freezeRoomScoutAttributionWithConsent(scope RoomScoutScope, contributorFences []ConsentFence) bool {
	if app == nil {
		return false
	}
	now := time.Now().UTC()
	app.mu.Lock()
	defer app.mu.Unlock()
	if !app.roomScoutScopeCurrentLocked(scope) {
		return false
	}
	state := app.roomLiveLocked(scope.RoomID)
	startedAt, stoppedAt := state.currentSpeechStartedAt, state.currentSpeechStoppedAt
	if stoppedAt.IsZero() || (!startedAt.IsZero() && stoppedAt.Before(startedAt)) {
		stoppedAt = now
	}
	if startedAt.IsZero() {
		startedAt = stoppedAt.Add(-speakerAttributionFallbackSpan)
	}
	state.pendingAttributionWindows = append(state.pendingAttributionWindows, attributionWindow{
		startedAt: startedAt, stoppedAt: stoppedAt, contributorFences: append([]ConsentFence(nil), contributorFences...),
	})
	if overflow := len(state.pendingAttributionWindows) - maxPendingAttributionWindows; overflow > 0 {
		state.pendingAttributionWindows = append([]attributionWindow(nil), state.pendingAttributionWindows[overflow:]...)
	}
	state.currentSpeechStartedAt = time.Time{}
	state.currentSpeechStoppedAt = time.Time{}
	return true
}

func (app *kanbanBoardApp) takeRoomScoutContributorFences(scope RoomScoutScope) []ConsentFence {
	if app == nil {
		return nil
	}
	app.mu.Lock()
	if !app.roomScoutScopeCurrentLocked(scope) {
		app.mu.Unlock()
		return nil
	}
	bundle := app.roomLiveLocked(scope.RoomID).realtime
	app.mu.Unlock()
	if bundle == nil || !bundle.scope.same(scope) {
		return nil
	}
	return bundle.takeContributorFences()
}

func (app *kanbanBoardApp) popRoomScoutAttribution(scope RoomScoutScope) bool {
	if app == nil {
		return false
	}
	app.mu.Lock()
	defer app.mu.Unlock()
	if !app.roomScoutScopeCurrentLocked(scope) {
		return false
	}
	state := app.roomLiveLocked(scope.RoomID)
	if len(state.pendingAttributionWindows) > 0 {
		state.pendingAttributionWindows = append([]attributionWindow(nil), state.pendingAttributionWindows[1:]...)
	}
	return true
}

func (app *kanbanBoardApp) attributionForRoomScoutScope(scope RoomScoutScope, completedAt time.Time) (string, string, *transcriptCaptureStamp, bool) {
	speaker, confidence, capture, _, current := app.attributionForRoomScoutScopeWithConsent(scope, completedAt)
	return speaker, confidence, capture, current
}

func (app *kanbanBoardApp) attributionForRoomScoutScopeWithConsent(scope RoomScoutScope, completedAt time.Time) (string, string, *transcriptCaptureStamp, []ConsentFence, bool) {
	if app == nil {
		return "", "unknown", nil, nil, false
	}
	if completedAt.IsZero() {
		completedAt = time.Now().UTC()
	}
	app.mu.Lock()
	defer app.mu.Unlock()
	if !app.roomScoutScopeCurrentLocked(scope) {
		return "", "unknown", nil, nil, false
	}
	state := app.roomLiveLocked(scope.RoomID)
	if len(state.pendingAttributionWindows) == 0 {
		startedAt, stoppedAt := state.currentSpeechStartedAt, state.currentSpeechStoppedAt
		if stoppedAt.IsZero() || (!startedAt.IsZero() && stoppedAt.Before(startedAt)) {
			stoppedAt = completedAt
		}
		if startedAt.IsZero() {
			startedAt = stoppedAt.Add(-speakerAttributionFallbackSpan)
		}
		speaker, confidence := dominantTranscriptSpeaker(attributionScoresLocked(state, startedAt, stoppedAt))
		state.currentSpeechStartedAt = time.Time{}
		state.currentSpeechStoppedAt = time.Time{}
		return speaker, confidence, nil, nil, true
	}
	window := state.pendingAttributionWindows[0]
	state.pendingAttributionWindows = append([]attributionWindow(nil), state.pendingAttributionWindows[1:]...)
	speaker, confidence := dominantTranscriptSpeaker(attributionScoresLocked(state, window.startedAt, window.stoppedAt))
	return speaker, confidence, window.capture, append([]ConsentFence(nil), window.contributorFences...), true
}

func (app *kanbanBoardApp) ensureRoomScoutRuntime(roomID, sittingID string, mediaGeneration uint64) {
	if app == nil {
		return
	}
	roomID = normalizeRoomID(roomID)
	if roomID == officeRoomID || strings.TrimSpace(sittingID) == "" || mediaGeneration == 0 || app.sittingListenOnly(roomID) {
		return
	}
	scope := RoomScoutScope{RoomID: roomID, SittingID: strings.TrimSpace(sittingID), MediaGeneration: mediaGeneration}
	bundle, err := newRoomRealtimeBundle(scope, func(event string, payload any) {
		publishRoomScoutAssistantEvent(roomID, event, payload)
	})
	if err != nil {
		return
	}
	app.mu.Lock()
	state := app.roomLiveLocked(roomID)
	if state.mediaGen != mediaGeneration || state.realtime != nil {
		app.mu.Unlock()
		_ = bundle.close()
		return
	}
	state.realtime = bundle
	factory := app.roomScoutFactory
	app.mu.Unlock()

	// Provider setup is deliberately outside app.mu and outside the room media
	// admission path. A slow or failed AI provider cannot delay room access.
	go bundle.start(factory)
}

func (app *kanbanBoardApp) roomScoutSnapshot(roomID string) roomScoutRuntimeSnapshot {
	if app == nil {
		return roomScoutRuntimeSnapshot{}
	}
	app.mu.Lock()
	bundle := app.roomLiveLocked(roomID).realtime
	app.mu.Unlock()
	return bundle.snapshot()
}

func publishRoomScoutAssistantEvent(roomID, event string, payload any) {
	metadata := map[string]any{"payload": payload}
	text := ""
	switch value := payload.(type) {
	case string:
		text = value
	case map[string]any:
		text = asString(value["text"])
	}
	if strings.TrimSpace(text) == "" {
		text = "Scout " + strings.TrimSpace(event)
	}
	broadcastRoomAssistantTelemetry(roomID, event, text, metadata)
}
