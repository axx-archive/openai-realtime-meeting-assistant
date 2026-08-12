package main

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
)

const officeScoutRequesterTTL = 2 * time.Minute

// officeScoutRequesterBinding is minted from the server's live speaker state,
// never from model-authored tool arguments. Sitting + media generation prevent
// a late callback or a successor meeting from inheriting the prior speaker.
type officeScoutRequesterBinding struct {
	Email           string
	Name            string
	SittingID       string
	MediaGeneration uint64
	BoundAt         time.Time
	ExpiresAt       time.Time
}

func (app *kanbanBoardApp) captureOfficeScoutRequesterCandidate() {
	if app == nil {
		return
	}
	now := time.Now().UTC()
	app.mu.Lock()
	state := app.roomLiveLocked(officeRoomID)
	name := canonicalRoomActorName(state.activeSpeakerName)
	sittingID := strings.TrimSpace(state.mediaSittingID)
	generation := state.mediaGen
	present := name != "" && state.participantCounts[name] > 0
	// A tool-result continuation is Scout's turn, not a new human request. Do
	// not replace the human binding when the second spoken response is created.
	continuation := app.scoutSpokenResponseSent
	app.mu.Unlock()
	if continuation {
		return
	}
	if sittingID == "" {
		sittingID = app.officeScoutSittingID()
	}
	requester, ok := authenticatedRequester(participantEmail(name))
	if !present || !ok || sittingID == "" || generation == 0 {
		app.mu.Lock()
		app.officeScoutRequester = officeScoutRequesterBinding{}
		app.mu.Unlock()
		return
	}

	app.mu.Lock()
	current := app.roomLiveLocked(officeRoomID)
	if current.mediaGen == generation && strings.TrimSpace(current.mediaSittingID) == sittingID && current.participantCounts[name] > 0 && canonicalRoomActorName(current.activeSpeakerName) == name {
		app.officeScoutRequester = officeScoutRequesterBinding{
			Email: normalizeAccountEmail(requester.Email), Name: name,
			SittingID: sittingID, MediaGeneration: generation,
			BoundAt: now, ExpiresAt: now.Add(officeScoutRequesterTTL),
		}
	}
	app.mu.Unlock()
}

// bindOfficeScoutRequesterToInputItem freezes the speaker observed for one
// committed input item. Realtime may finish transcription after another human
// has already spoken; item_id is the provider-owned correlation that prevents
// the later speaker from inheriting the earlier turn.
func (app *kanbanBoardApp) bindOfficeScoutRequesterToInputItem(itemID string) {
	if app == nil || strings.TrimSpace(itemID) == "" {
		return
	}
	itemID = strings.TrimSpace(itemID)
	app.mu.Lock()
	defer app.mu.Unlock()
	if app.officeScoutRequesterByInput == nil {
		app.officeScoutRequesterByInput = make(map[string]officeScoutRequesterBinding)
	}
	now := time.Now().UTC()
	for existingItemID, binding := range app.officeScoutRequesterByInput {
		if binding.ExpiresAt.IsZero() || now.After(binding.ExpiresAt) {
			delete(app.officeScoutRequesterByInput, existingItemID)
		}
	}
	if _, exists := app.officeScoutRequesterByInput[itemID]; !exists && app.officeScoutRequester.Email != "" {
		app.officeScoutRequesterByInput[itemID] = app.officeScoutRequester
	}
	app.officeScoutRequester = officeScoutRequesterBinding{}
}

func (app *kanbanBoardApp) officeScoutRequesterForInputItem(itemID string) (officeScoutRequesterBinding, bool) {
	if app == nil || strings.TrimSpace(itemID) == "" {
		return officeScoutRequesterBinding{}, false
	}
	itemID = strings.TrimSpace(itemID)
	app.mu.Lock()
	defer app.mu.Unlock()
	binding, exists := app.officeScoutRequesterByInput[itemID]
	if !exists || binding.Email == "" || binding.ExpiresAt.IsZero() || time.Now().UTC().After(binding.ExpiresAt) {
		delete(app.officeScoutRequesterByInput, itemID)
		return officeScoutRequesterBinding{}, false
	}
	return binding, true
}

// bindOfficeScoutRequesterInputToResponse consumes the server-owned input item
// metadata echoed by Realtime on response.created. Two transcripts can be
// admitted before either response is created without sharing a mutable slot.
func (app *kanbanBoardApp) bindOfficeScoutRequesterInputToResponse(itemID, responseID string) {
	if app == nil || strings.TrimSpace(itemID) == "" || strings.TrimSpace(responseID) == "" {
		return
	}
	itemID, responseID = strings.TrimSpace(itemID), strings.TrimSpace(responseID)
	app.mu.Lock()
	defer app.mu.Unlock()
	binding, exists := app.officeScoutRequesterByInput[itemID]
	if !exists || binding.Email == "" || binding.ExpiresAt.IsZero() || time.Now().UTC().After(binding.ExpiresAt) {
		delete(app.officeScoutRequesterByInput, itemID)
		return
	}
	if app.officeScoutRequesterByResponse == nil {
		app.officeScoutRequesterByResponse = make(map[string]officeScoutRequesterBinding)
	}
	if _, collision := app.officeScoutRequesterByResponse[responseID]; !collision {
		app.officeScoutRequesterByResponse[responseID] = binding
	}
	delete(app.officeScoutRequesterByInput, itemID)
}

func (app *kanbanBoardApp) discardOfficeScoutRequesterForInputItem(itemID string) {
	if app == nil || strings.TrimSpace(itemID) == "" {
		return
	}
	app.mu.Lock()
	delete(app.officeScoutRequesterByInput, strings.TrimSpace(itemID))
	app.mu.Unlock()
}

// armOfficeScoutRequesterCandidate freezes the pending speaker at the exact
// transcript turn the server admitted. Later speech can update the pending
// slot, but cannot replace the requester already owned by this response.
func (app *kanbanBoardApp) armOfficeScoutRequesterCandidate() {
	if app == nil {
		return
	}
	app.mu.Lock()
	app.officeScoutArmedRequester = app.officeScoutRequester
	app.officeScoutRequester = officeScoutRequesterBinding{}
	app.mu.Unlock()
}

func (app *kanbanBoardApp) bindOfficeScoutRequesterToResponse(responseID string) {
	if app == nil || strings.TrimSpace(responseID) == "" {
		return
	}
	responseID = strings.TrimSpace(responseID)
	app.mu.Lock()
	defer app.mu.Unlock()
	if app.officeScoutRequesterByResponse == nil {
		app.officeScoutRequesterByResponse = make(map[string]officeScoutRequesterBinding)
	}
	if _, exists := app.officeScoutRequesterByResponse[responseID]; !exists && app.officeScoutArmedRequester.Email != "" {
		app.officeScoutRequesterByResponse[responseID] = app.officeScoutArmedRequester
	}
	app.officeScoutArmedRequester = officeScoutRequesterBinding{}
}

// bindOfficeScoutRequesterToCall consumes one response-level human authority
// into one immutable provider call. A response cannot authorize two launches.
func (app *kanbanBoardApp) bindOfficeScoutRequesterToCall(responseID, callID string) {
	if app == nil || strings.TrimSpace(responseID) == "" || strings.TrimSpace(callID) == "" {
		return
	}
	responseID, callID = strings.TrimSpace(responseID), strings.TrimSpace(callID)
	app.mu.Lock()
	defer app.mu.Unlock()
	if app.officeScoutRequesterByCall == nil {
		app.officeScoutRequesterByCall = make(map[string]officeScoutRequesterBinding)
	}
	if _, exists := app.officeScoutRequesterByCall[callID]; exists {
		return
	}
	if binding, exists := app.officeScoutRequesterByResponse[responseID]; exists {
		app.officeScoutRequesterByCall[callID] = binding
		delete(app.officeScoutRequesterByResponse, responseID)
	}
}

func (app *kanbanBoardApp) consumeOfficeScoutRequesterForCall(callID, sittingID string) (officeScoutRequesterBinding, bool) {
	if app == nil {
		return officeScoutRequesterBinding{}, false
	}
	now := time.Now().UTC()
	callID, sittingID = strings.TrimSpace(callID), strings.TrimSpace(sittingID)
	app.mu.Lock()
	defer app.mu.Unlock()
	binding := app.officeScoutRequesterByCall[callID]
	state := app.roomLiveLocked(officeRoomID)
	valid := binding.Email != "" && binding.SittingID == sittingID &&
		binding.MediaGeneration != 0 && binding.MediaGeneration == state.mediaGen &&
		binding.SittingID == strings.TrimSpace(state.mediaSittingID) &&
		!binding.ExpiresAt.IsZero() && !now.After(binding.ExpiresAt) &&
		state.participantCounts[binding.Name] > 0
	if !valid {
		delete(app.officeScoutRequesterByCall, callID)
		return officeScoutRequesterBinding{}, false
	}
	// One attributed human turn can start at most one direct work run. A later
	// tool callback needs a new server-observed speech boundary and speaker.
	delete(app.officeScoutRequesterByCall, callID)
	return binding, true
}

func (app *kanbanBoardApp) clearOfficeScoutRequesterBindingsLocked() {
	app.officeScoutRequester = officeScoutRequesterBinding{}
	app.officeScoutRequesterByInput = nil
	app.officeScoutArmedRequester = officeScoutRequesterBinding{}
	app.officeScoutRequesterByResponse = nil
	app.officeScoutRequesterByCall = nil
}

func (app *kanbanBoardApp) clearOfficeScoutRequesterBindings() {
	if app == nil {
		return
	}
	app.mu.Lock()
	app.clearOfficeScoutRequesterBindingsLocked()
	app.mu.Unlock()
}

// officeScoutSittingID snapshots the server-owned sitting bound to the legacy
// office Realtime peer. It never accepts a provider/tool argument.
func (app *kanbanBoardApp) officeScoutSittingID() string {
	if app == nil {
		return ""
	}
	if app.meetings != nil {
		if meeting, ok := app.meetings.activeRecord(officeRoomID); ok {
			return strings.TrimSpace(meeting.ID)
		}
	}
	if app.memory != nil {
		return strings.TrimSpace(app.memory.currentMeetingID(officeRoomID))
	}
	return ""
}

func (app *kanbanBoardApp) initializeOfficeScoutWorkLocked(sittingID string) {
	if app.officeWorkCond == nil {
		app.officeWorkCond = sync.NewCond(&app.mu)
	}
	if app.officeWorkInFlight == nil {
		app.officeWorkInFlight = make(map[uint64]int)
	}
	if app.officeWorkCtx == nil {
		app.officeWorkEpoch++
		app.officeWorkCtx, app.officeWorkCancel = context.WithCancel(context.Background())
		app.officeWorkSittingID = strings.TrimSpace(sittingID)
	}
}

func (app *kanbanBoardApp) beginOfficeScoutWork(ctx context.Context, sittingID string) (context.Context, uint64, error) {
	if app == nil {
		return nil, 0, ErrRoomScoutFence
	}
	sittingID = strings.TrimSpace(sittingID)
	app.mu.Lock()
	app.initializeOfficeScoutWorkLocked(sittingID)
	if app.officeWorkSittingID != sittingID {
		oldEpoch, oldCancel := app.officeWorkEpoch, app.officeWorkCancel
		app.officeWorkEpoch++
		app.officeWorkSittingID = sittingID
		app.officeWorkCtx, app.officeWorkCancel = context.WithCancel(context.Background())
		oldCancel()
		for app.officeWorkInFlight[oldEpoch] > 0 {
			app.officeWorkCond.Wait()
		}
		delete(app.officeWorkInFlight, oldEpoch)
	}
	epoch, epochCtx := app.officeWorkEpoch, app.officeWorkCtx
	app.officeWorkInFlight[epoch]++
	app.mu.Unlock()

	if ctx == nil {
		ctx = context.Background()
	}
	workCtx, cancel := context.WithCancel(epochCtx)
	go func() {
		select {
		case <-ctx.Done():
			cancel()
		case <-workCtx.Done():
		}
	}()
	return context.WithValue(workCtx, officeScoutWorkCancelKey{}, cancel), epoch, nil
}

type officeScoutWorkCancelKey struct{}

func (app *kanbanBoardApp) endOfficeScoutWork(ctx context.Context, epoch uint64) {
	if cancel, ok := ctx.Value(officeScoutWorkCancelKey{}).(context.CancelFunc); ok {
		cancel()
	}
	app.mu.Lock()
	app.officeWorkInFlight[epoch]--
	if app.officeWorkInFlight[epoch] == 0 && app.officeWorkCond != nil {
		app.officeWorkCond.Broadcast()
	}
	app.mu.Unlock()
}

func (app *kanbanBoardApp) officeScoutWorkCurrent(epoch uint64, sittingID string) bool {
	if app == nil {
		return false
	}
	app.mu.Lock()
	current := app.officeWorkEpoch == epoch && app.officeWorkSittingID == strings.TrimSpace(sittingID)
	app.mu.Unlock()
	if !current {
		return false
	}
	// A meeting rotation is an authority change even before another tool begins
	// and advances the local epoch. This final live comparison suppresses old-
	// sitting mutations/results on the archive/idle-close boundary.
	return app.officeScoutSittingID() == strings.TrimSpace(sittingID)
}

func (app *kanbanBoardApp) runOfficeScoutWorkFenced(ctx context.Context, sittingID string, run func(context.Context, uint64) error) error {
	if run == nil {
		return ErrRoomScoutFence
	}
	workCtx, epoch, err := app.beginOfficeScoutWork(ctx, sittingID)
	if err != nil {
		return err
	}
	defer app.endOfficeScoutWork(workCtx, epoch)
	if err := workCtx.Err(); err != nil {
		return ErrRoomScoutFence
	}
	err = run(workCtx, epoch)
	if workCtx.Err() != nil || !app.officeScoutWorkCurrent(epoch, sittingID) {
		return ErrRoomScoutFence
	}
	return err
}

// cancelOfficeScoutWorkForSitting is invoked only after the withdrawal is
// durable and the generation is bumped. It cancels the old sitting/consent
// epoch and waits for every tool callback to acknowledge the fence before the
// provider buffer is cleared or a new epoch may publish results.
func (app *kanbanBoardApp) cancelOfficeScoutWorkForSitting(sittingID string) {
	if app == nil {
		return
	}
	sittingID = strings.TrimSpace(sittingID)
	// Serialize the epoch transition with the final mutation commit. The lock
	// is released before waiting so stale work can reacquire it, observe the
	// bumped epoch, abort, and drain its in-flight count.
	app.officeWorkCommitMu.Lock()
	app.mu.Lock()
	app.initializeOfficeScoutWorkLocked(sittingID)
	if app.officeWorkSittingID != sittingID {
		app.mu.Unlock()
		app.officeWorkCommitMu.Unlock()
		return
	}
	oldEpoch, oldCancel := app.officeWorkEpoch, app.officeWorkCancel
	app.officeWorkEpoch++
	app.officeWorkCtx, app.officeWorkCancel = context.WithCancel(context.Background())
	oldCancel()
	app.officeWorkCommitMu.Unlock()
	for app.officeWorkInFlight[oldEpoch] > 0 {
		app.officeWorkCond.Wait()
	}
	delete(app.officeWorkInFlight, oldEpoch)
	app.mu.Unlock()
}

// archiveMeetingWithOfficeScoutFence serializes a manual rollover with the
// final Realtime mutation boundary. Work is either wholly committed to the
// predecessor before this lock or observes the rotated sitting and aborts.
func (app *kanbanBoardApp) archiveMeetingWithOfficeScoutFence(archivedBy string) (meetingArchiveResult, error) {
	if app == nil {
		return meetingArchiveResult{}, ErrRoomScoutFence
	}
	sittingID := app.officeScoutSittingID()
	app.cancelOfficeScoutWorkForSitting(sittingID)
	app.officeWorkCommitMu.Lock()
	defer app.officeWorkCommitMu.Unlock()
	app.clearOfficeScoutRequesterBindings()
	return app.archiveMeeting(archivedBy)
}

func isOfficeScoutWorkFence(err error) bool {
	return errors.Is(err, ErrRoomScoutFence) || errors.Is(err, context.Canceled)
}
