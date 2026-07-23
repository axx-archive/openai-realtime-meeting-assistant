package main

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type testRoomScoutTransport struct {
	writes atomic.Int64
	closed atomic.Bool
	err    error
}

func (transport *testRoomScoutTransport) WriteMixedPCM(_ context.Context, _ []int16) error {
	transport.writes.Add(1)
	return transport.err
}

func (transport *testRoomScoutTransport) Close() error {
	transport.closed.Store(true)
	return nil
}

func TestRoomRealtimeBundleFencesRoomsSittingsToolsAndCallbacks(t *testing.T) {
	scopeA := RoomScoutScope{RoomID: "room-aaaa1111", SittingID: "sitting-a", MediaGeneration: 4}
	scopeB := RoomScoutScope{RoomID: "room-bbbb2222", SittingID: "sitting-b", MediaGeneration: 4}
	var mu sync.Mutex
	eventsA, eventsB := []string{}, []string{}
	bundleA, err := newRoomRealtimeBundle(scopeA, func(event string, _ any) { mu.Lock(); eventsA = append(eventsA, event); mu.Unlock() })
	if err != nil {
		t.Fatal(err)
	}
	bundleB, err := newRoomRealtimeBundle(scopeB, func(event string, _ any) { mu.Lock(); eventsB = append(eventsB, event); mu.Unlock() })
	if err != nil {
		t.Fatal(err)
	}
	transportA, transportB := &testRoomScoutTransport{}, &testRoomScoutTransport{}
	bundleA.start(func(context.Context, RoomScoutScope, RoomScoutCallbacks) (RoomScoutTransport, error) {
		return transportA, nil
	})
	bundleB.start(func(context.Context, RoomScoutScope, RoomScoutCallbacks) (RoomScoutTransport, error) {
		return transportB, nil
	})

	if !bundleA.publishFenced(scopeA, "status", "ready") || bundleA.publishFenced(scopeB, "leak", "canary") || bundleA.publishFenced(RoomScoutScope{RoomID: scopeA.RoomID, SittingID: scopeA.SittingID, MediaGeneration: 3}, "stale", nil) {
		t.Fatal("room/sitting/media generation callback fence failed")
	}
	if !bundleB.publishFenced(scopeB, "status", "ready") {
		t.Fatal("room B callback was rejected")
	}
	mu.Lock()
	if len(eventsA) != 1 || eventsA[0] != "status" || len(eventsB) != 1 || eventsB[0] != "status" {
		t.Fatalf("cross-room events A=%v B=%v", eventsA, eventsB)
	}
	mu.Unlock()

	principalA := ACLPrincipal{TenantID: "bonfire", ID: "member-a", Kind: ACLPrincipalUser, RoomID: scopeA.RoomID, SittingID: scopeA.SittingID}
	var callsA, callsB atomic.Int64
	if err := bundleA.runToolFenced(context.Background(), scopeA, "same-provider-call-id", principalA, func(context.Context) error { callsA.Add(1); return nil }); err != nil {
		t.Fatal(err)
	}
	if err := bundleA.runToolFenced(context.Background(), scopeA, "same-provider-call-id", principalA, func(context.Context) error { callsA.Add(1); return nil }); err != nil {
		t.Fatal(err)
	}
	principalB := ACLPrincipal{TenantID: "bonfire", ID: "scout", Kind: ACLPrincipalService, RoomID: scopeB.RoomID, SittingID: scopeB.SittingID}
	if err := bundleB.runToolFenced(context.Background(), scopeB, "same-provider-call-id", principalB, func(context.Context) error { callsB.Add(1); return nil }); err != nil {
		t.Fatal(err)
	}
	if callsA.Load() != 1 || callsB.Load() != 1 {
		t.Fatalf("tool dedupe leaked across rooms callsA=%d callsB=%d", callsA.Load(), callsB.Load())
	}
	guest := principalA
	guest.Kind = ACLPrincipalGuest
	if err := bundleA.runToolFenced(context.Background(), scopeA, "guest-call", guest, func(context.Context) error { return nil }); !errors.Is(err, ErrRoomScoutUnauthorized) {
		t.Fatalf("guest tool error=%v", err)
	}
	wrongRoom := principalA
	wrongRoom.RoomID = scopeB.RoomID
	if err := bundleA.runToolFenced(context.Background(), scopeA, "wrong-room", wrongRoom, func(context.Context) error { return nil }); !errors.Is(err, ErrRoomScoutUnauthorized) {
		t.Fatalf("cross-room tool error=%v", err)
	}

	if err := bundleA.close(); err != nil {
		t.Fatal(err)
	}
	if !transportA.closed.Load() || bundleA.publishFenced(scopeA, "after-close", nil) {
		t.Fatal("closed bundle accepted provider output")
	}
	if err := bundleA.runToolFenced(context.Background(), scopeA, "after-close", principalA, func(context.Context) error { return nil }); !errors.Is(err, ErrRoomScoutClosed) {
		t.Fatalf("closed tool error=%v", err)
	}
}

func TestRoomRealtimeBundleProviderFailureIsMediaIndependent(t *testing.T) {
	scope := RoomScoutScope{RoomID: "room-fail1111", SittingID: "sitting-fail", MediaGeneration: 1}
	bundle, err := newRoomRealtimeBundle(scope, nil)
	if err != nil {
		t.Fatal(err)
	}
	transport := &testRoomScoutTransport{err: errors.New("provider unavailable")}
	bundle.start(func(context.Context, RoomScoutScope, RoomScoutCallbacks) (RoomScoutTransport, error) {
		return transport, nil
	})
	bundle.writeMixedPCM(make([]int16, roomAudioMixFrameSize))
	deadline := time.Now().Add(time.Second)
	for bundle.snapshot().Status != RoomScoutDegraded && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if snapshot := bundle.snapshot(); snapshot.Status != RoomScoutDegraded || snapshot.LastError == "" {
		t.Fatalf("provider failure was not isolated and visible: %+v", snapshot)
	}
	if transport.writes.Load() != 1 {
		t.Fatalf("writes=%d", transport.writes.Load())
	}
	// A degraded provider write returns immediately; it never propagates an
	// error to the mixer/media caller or blocks a second room.
	done := make(chan struct{})
	go func() { bundle.writeMixedPCM([]int16{1}); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("degraded Scout blocked the room media path")
	}
}

func TestRoomRealtimeBundleConsentEpochCancelsAndWaitsForBlockedTool(t *testing.T) {
	scope := RoomScoutScope{RoomID: "room-withdraw11", SittingID: "sitting-withdraw", MediaGeneration: 1}
	bundle, err := newRoomRealtimeBundle(scope, nil)
	if err != nil {
		t.Fatal(err)
	}
	provider := &fakeBufferedRoomScoutTransport{}
	bundle.mu.Lock()
	bundle.transport = provider
	bundle.status = RoomScoutReady
	bundle.mu.Unlock()

	principal := ACLPrincipal{TenantID: "bonfire", ID: "scout", Kind: ACLPrincipalService, RoomID: scope.RoomID, SittingID: scope.SittingID}
	entered := make(chan struct{})
	exited := make(chan struct{})
	toolDone := make(chan error, 1)
	go func() {
		toolDone <- bundle.runToolFenced(context.Background(), scope, "blocked-tool", principal, func(ctx context.Context) error {
			close(entered)
			<-ctx.Done()
			close(exited)
			return ctx.Err()
		})
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("tool did not enter blocked work")
	}

	withdrawDone := make(chan error, 1)
	go func() { withdrawDone <- bundle.cancelBufferedAudio() }()
	select {
	case <-exited:
	case <-time.After(time.Second):
		t.Fatal("consent epoch did not cancel blocked tool")
	}
	select {
	case err := <-withdrawDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("withdrawal did not wait for canceled tool to exit")
	}
	if err := <-toolDone; !errors.Is(err, ErrRoomScoutFence) {
		t.Fatalf("blocked tool err=%v, want epoch fence", err)
	}
	if provider.canceled.Load() != 1 {
		t.Fatalf("provider buffer cancels=%d, want 1", provider.canceled.Load())
	}

	var nextEpochRuns atomic.Int64
	if err := bundle.runToolFenced(context.Background(), scope, "new-epoch-tool", principal, func(context.Context) error {
		nextEpochRuns.Add(1)
		return nil
	}); err != nil || nextEpochRuns.Load() != 1 {
		t.Fatalf("new consent epoch run err=%v count=%d", err, nextEpochRuns.Load())
	}
}

func TestRoomScoutRuntimeIsNeverConstructedForListenOnlySitting(t *testing.T) {
	app := newW2ATestApp(t)
	defer app.Close()
	roomID := "room-guest1111"
	sittingID := app.memory.ensureMeetingID(roomID)
	if _, changed := app.meetings.startMeeting(roomID, sittingID, time.Now().UTC(), []string{"Guest Sam"}); !changed {
		t.Fatal("meeting did not start")
	}
	if _, changed := app.meetings.latchListenOnly(sittingID); !changed {
		t.Fatal("listen-only latch did not persist")
	}
	var factoryCalls atomic.Int64
	app.roomScoutFactory = func(context.Context, RoomScoutScope, RoomScoutCallbacks) (RoomScoutTransport, error) {
		factoryCalls.Add(1)
		return &testRoomScoutTransport{}, nil
	}
	app.mu.Lock()
	app.roomLiveLocked(roomID).mediaGen = 1
	app.mu.Unlock()
	app.ensureRoomScoutRuntime(roomID, sittingID, 1)
	time.Sleep(10 * time.Millisecond)
	if factoryCalls.Load() != 0 {
		t.Fatalf("listen-only sitting constructed Scout %d time(s)", factoryCalls.Load())
	}
	if snapshot := app.roomScoutSnapshot(roomID); snapshot.Status != "" {
		t.Fatalf("listen-only room has a runtime: %+v", snapshot)
	}
}
