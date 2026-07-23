package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeRoomScoutProviderSession struct {
	writes   atomic.Int64
	clears   atomic.Int64
	closed   atomic.Bool
	mu       sync.Mutex
	events   []map[string]any
	writeErr error
}

type blockingEffectiveConsentStore struct {
	base    ConsentStore
	once    sync.Once
	entered chan struct{}
	release chan struct{}
}

func (store *blockingEffectiveConsentStore) Append(ctx context.Context, record ConsentRecord) (bool, error) {
	return store.base.Append(ctx, record)
}

func (store *blockingEffectiveConsentStore) Effective(ctx context.Context, query ConsentQuery) (ConsentDecision, error) {
	store.once.Do(func() {
		close(store.entered)
		select {
		case <-store.release:
		case <-ctx.Done():
		}
	})
	return store.base.Effective(ctx, query)
}

func (session *fakeRoomScoutProviderSession) WriteMixedPCM(context.Context, []int16) error {
	session.writes.Add(1)
	return session.writeErr
}

func (session *fakeRoomScoutProviderSession) SendEvent(payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	var event map[string]any
	if err := json.Unmarshal(raw, &event); err != nil {
		return err
	}
	if event["type"] == "input_audio_buffer.clear" {
		session.clears.Add(1)
	}
	session.mu.Lock()
	session.events = append(session.events, event)
	session.mu.Unlock()
	return nil
}

func (session *fakeRoomScoutProviderSession) Close() error {
	session.closed.Store(true)
	return nil
}

func (session *fakeRoomScoutProviderSession) eventTypes() []string {
	session.mu.Lock()
	defer session.mu.Unlock()
	types := make([]string, 0, len(session.events))
	for _, event := range session.events {
		types = append(types, asString(event["type"]))
	}
	return types
}

func newScopedRoomScoutTransportTest(t *testing.T, dial roomScoutProviderDialer) (*kanbanBoardApp, *roomRealtimeBundle, *openAIRoomScoutTransport, RoomScoutScope) {
	t.Helper()
	app := newW2ATestApp(t)
	t.Cleanup(func() { _ = app.Close() })
	roomID := "room-scout1111"
	sittingID := app.memory.ensureMeetingID(roomID)
	if _, changed := app.meetings.startMeeting(roomID, sittingID, time.Now().UTC(), []string{"AJ"}); !changed {
		t.Fatal("start test sitting")
	}
	scope := RoomScoutScope{RoomID: roomID, SittingID: sittingID, MediaGeneration: 7}
	bundle, err := newRoomRealtimeBundle(scope, func(string, any) {})
	if err != nil {
		t.Fatal(err)
	}
	app.mu.Lock()
	state := app.roomLiveLocked(roomID)
	state.mediaGen = scope.MediaGeneration
	state.realtime = bundle
	app.mu.Unlock()
	transport, err := newOpenAIRoomScoutTransport(bundle.ctx, app, "test-key", scope, RoomScoutCallbacks{
		Publish: bundle.publishFenced,
		RunTool: bundle.runToolFenced,
		Status:  bundle.setStatusFenced,
	}, dial)
	if err != nil {
		t.Fatal(err)
	}
	bundle.mu.Lock()
	bundle.transport = transport
	bundle.status = RoomScoutReady
	bundle.mu.Unlock()
	t.Cleanup(func() { _ = bundle.close() })
	return app, bundle, transport, scope
}

func TestOpenAIRoomScoutTransportRestartsWithoutSharingSessionState(t *testing.T) {
	var mu sync.Mutex
	sessions := []*fakeRoomScoutProviderSession{}
	dial := func(context.Context, *openAIRoomScoutTransport, uint64) (roomScoutProviderSession, error) {
		session := &fakeRoomScoutProviderSession{}
		mu.Lock()
		sessions = append(sessions, session)
		mu.Unlock()
		return session, nil
	}
	_, _, transport, _ := newScopedRoomScoutTransportTest(t, dial)
	if err := transport.WriteMixedPCM(context.Background(), make([]int16, roomAudioMixFrameSize)); err != nil {
		t.Fatal(err)
	}
	transport.requestRestart(1, "test expiry")
	deadline := time.Now().Add(time.Second)
	for {
		mu.Lock()
		count := len(sessions)
		mu.Unlock()
		if count == 2 || time.Now().After(deadline) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	mu.Lock()
	if len(sessions) != 2 {
		mu.Unlock()
		t.Fatalf("provider sessions=%d, want replacement", len(sessions))
	}
	first, second := sessions[0], sessions[1]
	mu.Unlock()
	if !first.closed.Load() {
		t.Fatal("expired provider session was not closed")
	}
	if transport.publish(1, "answer", map[string]any{"text": "stale"}) {
		t.Fatal("replaced provider generation published a stale callback")
	}
	if err := transport.WriteMixedPCM(context.Background(), make([]int16, roomAudioMixFrameSize)); err != nil {
		t.Fatal(err)
	}
	if first.writes.Load() != 1 || second.writes.Load() != 1 {
		t.Fatalf("writes leaked across sessions first=%d second=%d", first.writes.Load(), second.writes.Load())
	}
	if err := transport.CancelBufferedAudio(context.Background()); err != nil {
		t.Fatal(err)
	}
	if second.clears.Load() != 1 || !hasRoomScoutEvent(second, "response.cancel") {
		t.Fatalf("withdrawal did not clear/cancel current provider buffer: %v", second.eventTypes())
	}
}

func TestRoomScoutWakeGateAllowsNormalAnswerAndSuppressesBackgroundSpeech(t *testing.T) {
	provider := &fakeRoomScoutProviderSession{}
	app, _, transport, _ := newScopedRoomScoutTransportTest(t, func(context.Context, *openAIRoomScoutTransport, uint64) (roomScoutProviderSession, error) {
		return provider, nil
	})
	_ = app

	// Ambient/background speech resolves through do_nothing but does not get a
	// response.create, so the provider cannot speak unprompted.
	transport.noteVoiceTranscript("We should probably wrap up soon")
	transport.handleProviderToolCall(provider, 1, kanbanRealtimeOutputItem{
		Type: "function_call", Name: "do_nothing", CallID: "background-1", Arguments: `{"reason":"background conversation"}`,
	}, false)
	waitForRoomScoutEventCount(t, provider, 1)
	if hasRoomScoutEvent(provider, "response.create") {
		t.Fatal("background speech triggered an unprompted Scout response")
	}

	// A normal addressed question takes the same tool-first path, then gets an
	// explicit tool_choice=none continuation that can answer aloud once. The
	// continuation waits for the function-call response.done seam, avoiding the
	// provider's "active response in progress" rejection.
	transport.noteVoiceTranscript("Hey Scout, what can you do?")
	transport.handleProviderEvent(provider, 1, []byte(`{"type":"response.created"}`))
	transport.handleProviderToolCall(provider, 1, kanbanRealtimeOutputItem{
		Type: "function_call", Name: "do_nothing", CallID: "addressed-1", Arguments: `{"reason":"answer the capability question"}`,
	}, false)
	waitForRoomScoutEventCount(t, provider, 2)
	if hasRoomScoutEvent(provider, "response.create") {
		t.Fatal("spoken continuation was requested while the function-call response was active")
	}
	transport.handleProviderEvent(provider, 1, []byte(`{"type":"response.done","response":{"status":"completed"}}`))
	waitForRoomScoutEventCount(t, provider, 3)
	provider.mu.Lock()
	defer provider.mu.Unlock()
	var response map[string]any
	for _, event := range provider.events {
		if event["type"] == "response.create" {
			response, _ = event["response"].(map[string]any)
		}
	}
	if response == nil || response["tool_choice"] != "none" {
		t.Fatalf("addressed answer response=%v, want tool_choice=none", response)
	}
}

func TestRoomScoutToolsAreClosedAllowlistAndUnknownToolsFailClosed(t *testing.T) {
	app := newW2ATestApp(t)
	defer app.Close()
	exposed := map[string]bool{}
	for _, tool := range app.roomScoutTools() {
		name := asString(tool["name"])
		if !roomScoutAllowedTools[name] {
			t.Fatalf("tool %q escaped the closed allowlist", name)
		}
		exposed[name] = true
	}
	for name := range roomScoutAllowedTools {
		if !exposed[name] {
			t.Fatalf("allowlisted tool %q has no exposed schema (registry drift)", name)
		}
	}
	for _, forbidden := range []string{"archive_meeting", "set_voice_control", "start_grill_session", "create_ticket", "create_artifact", "send_notification", "post_to_channel", "future_office_admin_tool"} {
		if exposed[forbidden] {
			t.Fatalf("office/global tool %q exposed to named-room Scout", forbidden)
		}
	}
}

func TestRoomScoutSideEffectsAndDeliveryStayInOwningRoom(t *testing.T) {
	app := newW2ATestApp(t)
	defer app.Close()
	roomA, roomB := "room-side-a", "room-side-b"
	scopeA := RoomScoutScope{RoomID: roomA, SittingID: app.memory.ensureMeetingID(roomA), MediaGeneration: 11}
	scopeB := RoomScoutScope{RoomID: roomB, SittingID: app.memory.ensureMeetingID(roomB), MediaGeneration: 12}
	var eventsMu sync.Mutex
	eventsA, eventsB := []string{}, []string{}
	bundleA, err := newRoomRealtimeBundle(scopeA, func(event string, _ any) {
		eventsMu.Lock()
		eventsA = append(eventsA, event)
		eventsMu.Unlock()
	})
	if err != nil {
		t.Fatal(err)
	}
	bundleB, err := newRoomRealtimeBundle(scopeB, func(event string, _ any) {
		eventsMu.Lock()
		eventsB = append(eventsB, event)
		eventsMu.Unlock()
	})
	if err != nil {
		t.Fatal(err)
	}
	defer bundleA.close()
	defer bundleB.close()
	app.mu.Lock()
	stateA, stateB := app.roomLiveLocked(roomA), app.roomLiveLocked(roomB)
	stateA.mediaGen, stateA.realtime = scopeA.MediaGeneration, bundleA
	stateB.mediaGen, stateB.realtime = scopeB.MediaGeneration, bundleB
	beforeCards := len(app.cards)
	app.mu.Unlock()

	if _, _, err := app.applyRoomScoutToolArgs(context.Background(), scopeA, "set_recording", map[string]any{
		"enabled": false,
		// Model-supplied room selectors are ignored; scopeA is server-owned.
		"room_id": roomB,
	}); err != nil {
		t.Fatal(err)
	}
	if app.transcriptRecordingActiveInRoom(roomA) || !app.transcriptRecordingActiveInRoom(roomB) {
		t.Fatalf("recording mutation crossed rooms: roomA=%t roomB=%t", app.transcriptRecordingActiveInRoom(roomA), app.transcriptRecordingActiveInRoom(roomB))
	}
	if _, _, err := app.applyRoomScoutToolArgs(context.Background(), scopeA, "create_ticket", map[string]any{"title": "CROSS-ROOM-CANARY"}); err == nil {
		t.Fatal("office/global side effect escaped the named-room adapter")
	}
	app.mu.Lock()
	afterCards := len(app.cards)
	app.mu.Unlock()
	if afterCards != beforeCards {
		t.Fatalf("rejected room side effect changed global board: before=%d after=%d", beforeCards, afterCards)
	}
	if !bundleA.publishFenced(scopeA, "ROOM-A-CANARY", nil) || bundleB.publishFenced(scopeA, "ROOM-A-LEAK", nil) {
		t.Fatal("room-scoped event delivery fence failed")
	}
	eventsMu.Lock()
	defer eventsMu.Unlock()
	if len(eventsA) != 1 || eventsA[0] != "ROOM-A-CANARY" || len(eventsB) != 0 {
		t.Fatalf("cross-room delivery eventsA=%v eventsB=%v", eventsA, eventsB)
	}
}

func TestRoomScoutTranscriptCommitRejectsSittingRollover(t *testing.T) {
	app := newW2ATestApp(t)
	defer app.Close()
	roomID := "room-rollover11"
	email := "aj@shareability.com"
	oldSitting := admitMemberWithTranscriptConsentForTest(t, app, roomID, email)
	authority := currentConsentLaneAuthority()
	blocking := &blockingEffectiveConsentStore{
		base: authority.Store, entered: make(chan struct{}), release: make(chan struct{}),
	}
	authority.Store = blocking

	oldScope := RoomScoutScope{RoomID: roomID, SittingID: oldSitting, MediaGeneration: 21}
	oldBundle, err := newRoomRealtimeBundle(oldScope, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer oldBundle.close()
	app.mu.Lock()
	state := app.roomLiveLocked(roomID)
	state.mediaGen, state.realtime = oldScope.MediaGeneration, oldBundle
	app.mu.Unlock()
	name := participantNameForEmail(email)
	now := time.Now().UTC()
	app.noteAudioActivityForRoom(roomID, now, []audioActivityLevel{{TrackKey: "rollover-track", ParticipantName: name, RMS: 900}})
	if !app.noteRoomScoutSpeechStarted(oldScope) || !app.noteRoomScoutSpeechStopped(oldScope) || !app.freezeRoomScoutAttribution(oldScope) {
		t.Fatal("old sitting attribution did not arm")
	}

	rememberDone := make(chan struct{})
	go func() {
		app.rememberRoomScoutTranscript(oldScope, kanbanRealtimeEvent{EventID: "ROLLOVER-CANARY", Transcript: "must not enter successor sitting"}, "scout_realtime", "test-model")
		close(rememberDone)
	}()
	select {
	case <-blocking.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("transcript did not reach blocked consent revalidation")
	}

	if _, changed := app.meetings.endMeeting(oldSitting, time.Now().UTC(), meetingEndedReasonIdle, ""); !changed {
		t.Fatal("old sitting did not close")
	}
	if !app.memory.rotateMeetingIDIfCurrent(roomID, oldSitting) {
		t.Fatal("old sitting memory id did not rotate")
	}
	newSitting := app.memory.ensureMeetingID(roomID)
	if _, changed := app.meetings.startMeeting(roomID, newSitting, time.Now().UTC(), []string{name}); !changed {
		t.Fatal("successor sitting did not start")
	}
	newScope := RoomScoutScope{RoomID: roomID, SittingID: newSitting, MediaGeneration: oldScope.MediaGeneration + 1}
	newBundle, err := newRoomRealtimeBundle(newScope, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer newBundle.close()
	app.mu.Lock()
	state = app.roomLiveLocked(roomID)
	state.mediaGen, state.realtime = newScope.MediaGeneration, newBundle
	app.mu.Unlock()
	close(blocking.release)
	select {
	case <-rememberDone:
	case <-time.After(5 * time.Second):
		t.Fatal("stale transcript callback did not finish")
	}
	for _, entry := range app.memory.entriesOfKind(meetingMemoryKindTranscript, 0) {
		if entry.ID == "ROLLOVER-CANARY" || strings.Contains(entry.Text, "successor sitting") {
			t.Fatalf("old Scout transcript crossed sitting rollover: %+v", entry)
		}
	}
}

func TestJoinConferenceRoomWiresNamedRoomScoutFactoryWithoutDialing(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key")
	t.Setenv("MEETING_BRAIN_DISABLED", "1")
	t.Setenv("MEETING_BOARD_DISABLED", "1")
	t.Setenv("RESEARCH_SUGGESTIONS_DISABLED", "1")
	app := newW2ATestApp(t)
	defer app.Close()
	if app.roomScoutFactory != nil {
		t.Fatal("test app unexpectedly began with a production room Scout factory")
	}
	if err := app.JoinConferenceRoom(); err != nil {
		t.Fatal(err)
	}
	app.mu.Lock()
	factory := app.roomScoutFactory
	app.mu.Unlock()
	if factory == nil {
		t.Fatal("JoinConferenceRoom did not wire the concrete named-room Scout factory")
	}
}

type fakeBufferedRoomScoutTransport struct {
	canceled atomic.Int64
	closed   atomic.Bool
}

func (*fakeBufferedRoomScoutTransport) WriteMixedPCM(context.Context, []int16) error { return nil }
func (transport *fakeBufferedRoomScoutTransport) CancelBufferedAudio(context.Context) error {
	transport.canceled.Add(1)
	return nil
}
func (transport *fakeBufferedRoomScoutTransport) Close() error {
	transport.closed.Store(true)
	return nil
}

func TestRoomRealtimeBundleCancelBufferedAudioDrainsWithoutClosingMedia(t *testing.T) {
	scope := RoomScoutScope{RoomID: "room-cancel11", SittingID: "sitting-cancel", MediaGeneration: 1}
	bundle, err := newRoomRealtimeBundle(scope, nil)
	if err != nil {
		t.Fatal(err)
	}
	provider := &fakeBufferedRoomScoutTransport{}
	bundle.mu.Lock()
	bundle.transport = provider
	bundle.status = RoomScoutReady
	bundle.mu.Unlock()
	bundle.audioInput <- make([]int16, roomAudioMixFrameSize)
	bundle.audioInput <- make([]int16, roomAudioMixFrameSize)
	if err := bundle.cancelBufferedAudio(); err != nil {
		t.Fatal(err)
	}
	if provider.canceled.Load() != 1 || provider.closed.Load() {
		t.Fatalf("cancel=%d closed=%t", provider.canceled.Load(), provider.closed.Load())
	}
	if len(bundle.audioInput) != 0 || bundle.snapshot().Status != RoomScoutReady {
		t.Fatalf("queue=%d snapshot=%+v", len(bundle.audioInput), bundle.snapshot())
	}
}

func TestRoomRealtimeBundleCancelBufferedAudioFailsClosedWhenUnsupported(t *testing.T) {
	scope := RoomScoutScope{RoomID: "room-cancel22", SittingID: "sitting-cancel", MediaGeneration: 1}
	bundle, err := newRoomRealtimeBundle(scope, nil)
	if err != nil {
		t.Fatal(err)
	}
	provider := &testRoomScoutTransport{}
	bundle.mu.Lock()
	bundle.transport = provider
	bundle.status = RoomScoutReady
	bundle.mu.Unlock()
	if err := bundle.cancelBufferedAudio(); err == nil {
		t.Fatal("transport without provider-buffer cancellation was accepted")
	}
	if !provider.closed.Load() || bundle.snapshot().Status != RoomScoutDegraded {
		t.Fatalf("unsupported cancellation did not fail Scout closed/degraded: closed=%t snapshot=%+v", provider.closed.Load(), bundle.snapshot())
	}
}

func TestOpenAIRoomScoutTransportConcurrentRestartAndClose(t *testing.T) {
	dial := func(context.Context, *openAIRoomScoutTransport, uint64) (roomScoutProviderSession, error) {
		return &fakeRoomScoutProviderSession{}, nil
	}
	_, _, transport, _ := newScopedRoomScoutTransportTest(t, dial)
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_ = transport.WriteMixedPCM(context.Background(), make([]int16, roomAudioMixFrameSize))
		}()
		go func() {
			defer wg.Done()
			transport.requestRestart(1, "race")
		}()
	}
	wg.Wait()
	if err := transport.Close(); err != nil && !errors.Is(err, ErrRoomScoutClosed) {
		t.Fatal(err)
	}
	if transport.publish(1, "answer", map[string]any{"text": "stale"}) {
		t.Fatal("closed transport accepted callback")
	}
}

func waitForRoomScoutEventCount(t *testing.T, provider *fakeRoomScoutProviderSession, count int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		provider.mu.Lock()
		got := len(provider.events)
		provider.mu.Unlock()
		if got >= count {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("provider events=%v, want at least %d", provider.eventTypes(), count)
		}
		time.Sleep(time.Millisecond)
	}
}

func hasRoomScoutEvent(provider *fakeRoomScoutProviderSession, eventType string) bool {
	for _, current := range provider.eventTypes() {
		if current == eventType {
			return true
		}
	}
	return false
}
