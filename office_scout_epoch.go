package main

import (
	"context"
	"errors"
	"strings"
	"sync"
)

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

func isOfficeScoutWorkFence(err error) bool {
	return errors.Is(err, ErrRoomScoutFence) || errors.Is(err, context.Canceled)
}
