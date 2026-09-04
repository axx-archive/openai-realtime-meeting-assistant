package main

import (
	"errors"
	"testing"
	"time"
)

func TestRoomOperationalCapabilityRowsAreRoomScopedAndKeepHumanMediaIndependent(t *testing.T) {
	now := time.Date(2026, 7, 30, 14, 0, 0, 0, time.UTC)
	laneA := newMeetingTranscriptionLaneForRoomGeneration(nil, "key", "gpt-realtime-whisper", "room-a", 3)
	laneA.setConnected(true)
	bundleA, err := newRoomRealtimeBundle(RoomScoutScope{RoomID: "room-a", SittingID: "sitting-a", MediaGeneration: 3}, nil)
	if err != nil {
		t.Fatal(err)
	}
	bundleA.mu.Lock()
	bundleA.status = RoomScoutDegraded
	bundleA.lastError = "provider failed"
	bundleA.mu.Unlock()
	store := &meetingMemoryStore{entries: []meetingMemoryEntry{
		{ID: "tx-a", Kind: meetingMemoryKindTranscript, Text: "room a", CreatedAt: now.Add(-time.Minute), Metadata: map[string]string{"roomId": "room-a", "meetingId": "sitting-a"}},
		{ID: "brain-b", Kind: meetingMemoryKindBrain, Text: "room b only", CreatedAt: now, Metadata: map[string]string{"roomId": "room-b", "meetingId": "sitting-b"}},
	}}
	app := &kanbanBoardApp{memory: store, roomLive: map[string]*roomLiveState{
		"room-a": {id: "room-a", participants: map[string]time.Time{"AJ": now}, recordingEnabled: true, mediaActor: &roomMediaActor{}, mixer: &audioMixer{}, lane: laneA, realtime: bundleA, mediaGen: 3, mediaSittingID: "sitting-a"},
		"room-b": {id: "room-b", participants: map[string]time.Time{}, mediaGen: 1, mediaSittingID: "sitting-b"},
	}}
	rows, degraded := roomOperationalCapabilityRows(app, now, true, nil)
	if len(rows) != 2 || asString(rows[0]["roomId"]) != "room-a" || asString(rows[1]["roomId"]) != "room-b" {
		t.Fatalf("rows=%+v", rows)
	}
	roomA := rows[0]
	if roomA["media"].(map[string]any)["status"] != "healthy" {
		t.Fatalf("AI failure incorrectly degraded human media: %+v", roomA)
	}
	if roomA["scout"].(map[string]any)["status"] != string(RoomScoutDegraded) || roomA["transcript"].(map[string]any)["status"] != "fresh" {
		t.Fatalf("room A capability truth=%+v", roomA)
	}
	if roomA["analysis"].(map[string]any)["status"] != "missing" {
		t.Fatal("room B brain evidence leaked into room A")
	}
	if len(degraded) != 1 || degraded[0] != "rooms.room-a.scout" {
		t.Fatalf("degraded=%v", degraded)
	}
}

func TestRoomOperationalCapabilityRowsExposeConsentAndActiveSTTFailureWithoutSecrets(t *testing.T) {
	now := time.Date(2026, 7, 30, 14, 0, 0, 0, time.UTC)
	app := &kanbanBoardApp{memory: &meetingMemoryStore{}, roomLive: map[string]*roomLiveState{
		"room-a": {id: "room-a", participants: map[string]time.Time{"AJ": now}, recordingEnabled: true, mediaActor: &roomMediaActor{}, mixer: &audioMixer{}, mediaGen: 2, mediaSittingID: "sitting-a"},
	}}
	rows, degraded := roomOperationalCapabilityRows(app, now, false, errors.New("postgres connection secret host unavailable"))
	if len(rows) != 1 {
		t.Fatalf("rows=%v", rows)
	}
	row := rows[0]
	if row["stt"].(map[string]any)["status"] != "degraded" || row["consent"].(map[string]any)["status"] != "degraded" || row["media"].(map[string]any)["status"] != "healthy" {
		t.Fatalf("row=%+v", row)
	}
	// Two, not three: the provider being down is not a fault of a room that
	// never invited a Scout. Nobody asked for an AI participant here, so its
	// absence is by design and must not sit in degraded[] burning attention.
	if len(degraded) != 2 {
		t.Fatalf("degraded=%v, want consent + stt only", degraded)
	}
	for _, entry := range degraded {
		if entry == "rooms.room-a.scout" {
			t.Fatalf("an uninvited Scout was reported as a room fault: %v", degraded)
		}
	}
	redactCapabilityErrors(row)
	if _, leaked := row["consent"].(map[string]any)["lastError"]; leaked {
		t.Fatal("public capability row leaked consent error")
	}
}

// TestSTTRowDegradesWhenAConnectedLaneStopsLanding closes the guardrail that
// let a green pill sit over a 34-minute hole: isConnected() proves a websocket
// is open and nothing else. A connected lane with no completion in 45s while
// the mixer is still offering it audio is starving, not healthy.
func TestSTTRowDegradesWhenAConnectedLaneStopsLanding(t *testing.T) {
	now := time.Date(2026, 9, 2, 21, 0, 0, 0, time.UTC)
	lane := newMeetingTranscriptionLaneForRoomGeneration(nil, "key", "gpt-transcribe", "room-a", 3)
	lane.setConnected(true)
	state := &roomLiveState{
		id: "room-a", participants: map[string]time.Time{"Tyler": now}, recordingEnabled: true,
		mediaActor: &roomMediaActor{}, mixer: &audioMixer{}, lane: lane, mediaGen: 3, mediaSittingID: "sitting-a",
		// Audio is still being offered; nothing has landed for a minute.
		lastTranscriptFrameAt:   now.Add(-time.Second),
		lastTranscriptCommitAt:  now.Add(-time.Minute),
		transcriptFramesOffered: 3000, transcriptFramesAccepted: 0,
		transcriptFrameDrops: map[string]uint64{transcriptDropNoFence: 3000},
	}
	app := &kanbanBoardApp{memory: &meetingMemoryStore{entries: []meetingMemoryEntry{
		{ID: "tx-a", Kind: meetingMemoryKindTranscript, Text: "last line before the hole", CreatedAt: now.Add(-time.Minute), Metadata: map[string]string{"roomId": "room-a", "meetingId": "sitting-a"}},
	}}, roomLive: map[string]*roomLiveState{"room-a": state}}

	rows, degraded := roomOperationalCapabilityRows(app, now, true, nil)
	stt := rows[0]["stt"].(map[string]any)
	if stt["status"] != "degraded" {
		t.Fatalf("connected-but-starving lane reported stt=%v, want degraded", stt)
	}
	if stt["capture"] != "stalling" || stt["audioOffered"] != true {
		t.Fatalf("stt row does not explain itself: %v", stt)
	}
	found := false
	for _, entry := range degraded {
		if entry == "rooms.room-a.stt" {
			found = true
		}
	}
	if !found {
		t.Fatalf("starving lane missing from degraded: %v", degraded)
	}
	if rows[0]["transcriptFrameDrops"].(map[string]uint64)[transcriptDropNoFence] != 3000 {
		t.Fatalf("drop census missing from the room row: %v", rows[0])
	}

	// A quiet room with the same connected lane must stay healthy: no frames
	// offered means no evidence of a stall, and /readyz must not invent one.
	state.lastTranscriptFrameAt = now.Add(-10 * time.Minute)
	rows, degraded = roomOperationalCapabilityRows(app, now, true, nil)
	stt = rows[0]["stt"].(map[string]any)
	if stt["status"] != "healthy" {
		t.Fatalf("quiet room reported stt=%v, want healthy", stt)
	}
	for _, entry := range degraded {
		if entry == "rooms.room-a.stt" || entry == "rooms.room-a.transcript" {
			t.Fatalf("quiet room appeared degraded: %v", degraded)
		}
	}
}

// installRoomMixerForTest swaps the boot-owned shared mixer for the duration
// of one test. The office's media row is judged against this mixer's wiring,
// so a test that asserts anything about rooms.office.media has to own it.
func installRoomMixerForTest(t *testing.T, mixer *audioMixer) {
	t.Helper()
	previous := roomMixer
	roomMixer = mixer
	t.Cleanup(func() { roomMixer = previous })
}

// TestOfficeMediaRowIsJudgedAgainstTheMixerItActuallyUses closes a permanent
// false alarm. ensureOfficeMedia never assigns state.mixer — the office uses
// the boot-owned shared roomMixer instead — so `state.mixer != nil` made
// rooms.office.media degraded for the entire duration of every office sitting
// with nothing wrong. Production /readyz on 2026-09-03 published exactly that
// while the founder was sitting in a working call:
//
//	media: {active: true, actor: true, mixer: false, status: "degraded"}
//
// The fix judges the office against the wiring its own media path installs.
func TestOfficeMediaRowIsJudgedAgainstTheMixerItActuallyUses(t *testing.T) {
	now := time.Date(2026, 9, 3, 16, 3, 0, 0, time.UTC)
	app := &kanbanBoardApp{memory: &meetingMemoryStore{}, roomLive: map[string]*roomLiveState{
		officeRoomID: {
			id: officeRoomID, participants: map[string]time.Time{"AJ": now}, recordingEnabled: true,
			// No state.mixer: this is what the office looks like in production.
			mediaActor: &roomMediaActor{}, mediaGen: 7, mediaSittingID: "sitting-office",
		},
	}}
	mixer := &audioMixer{sinks: map[string]audioMixerSink{}, stop: make(chan struct{})}
	installRoomMixerForTest(t, mixer)
	mixer.setActivityListener(&roomAudioActivityListener{app: app, roomID: officeRoomID, sittingID: "sitting-office", generation: 7})

	rows, degraded := roomOperationalCapabilityRows(app, now, true, nil)
	media, _ := rows[0]["media"].(map[string]any)
	if media["mixer"] != true || media["status"] != "healthy" {
		t.Fatalf("an active office with its audio wired to the shared mixer reported media=%v, want healthy", media)
	}
	for _, entry := range degraded {
		if entry == "rooms."+officeRoomID+".media" {
			t.Fatalf("healthy office media appeared in /readyz degraded: %v", degraded)
		}
	}

	// And it is not a green lie. `roomMixer != nil` would answer true forever;
	// this signal is generation-scoped, so a shared mixer still feeding the
	// PREVIOUS sitting's attribution is honestly degraded.
	mixer.setActivityListener(&roomAudioActivityListener{app: app, roomID: officeRoomID, sittingID: "sitting-previous", generation: 6})
	rows, degraded = roomOperationalCapabilityRows(app, now, true, nil)
	media, _ = rows[0]["media"].(map[string]any)
	if media["mixer"] != false || media["status"] != "degraded" {
		t.Fatalf("office media judged healthy while the shared mixer fed generation 6: media=%v", media)
	}
	found := false
	for _, entry := range degraded {
		if entry == "rooms."+officeRoomID+".media" {
			found = true
		}
	}
	if !found {
		t.Fatalf("a wrong-generation mixer did not degrade office media: %v", degraded)
	}

	// A named room keeps the unchanged test: its own mixer is the authority.
	app.mu.Lock()
	app.roomLive["room-b"] = &roomLiveState{
		id: "room-b", participants: map[string]time.Time{"Tyler": now}, recordingEnabled: true,
		mediaActor: &roomMediaActor{}, mixer: &audioMixer{}, mediaGen: 2, mediaSittingID: "sitting-b",
	}
	app.mu.Unlock()
	rows, _ = roomOperationalCapabilityRows(app, now, true, nil)
	for _, row := range rows {
		if asString(row["roomId"]) != "room-b" {
			continue
		}
		if row["media"].(map[string]any)["status"] != "healthy" {
			t.Fatalf("named room media=%v, want healthy from its own mixer", row["media"])
		}
	}
}

// TestUninvitedScoutIsAbsentByDesignNotDegraded is the second permanent false
// alarm from the same live snapshot: rooms.office.scout sat in degraded[] on
// every active room in the process. Ordinary admission must never silently add
// an AI participant, and ensureOfficeRealtimePeer early-returns without an
// invitation, so an absent Scout is the designed state — not a fault.
func TestUninvitedScoutIsAbsentByDesignNotDegraded(t *testing.T) {
	now := time.Date(2026, 9, 3, 16, 3, 0, 0, time.UTC)
	state := &roomLiveState{
		id: "room-a", participants: map[string]time.Time{"AJ": now}, recordingEnabled: true,
		mediaActor: &roomMediaActor{}, mixer: &audioMixer{}, mediaGen: 4, mediaSittingID: "sitting-a",
	}
	app := &kanbanBoardApp{memory: &meetingMemoryStore{}, roomLive: map[string]*roomLiveState{"room-a": state}}

	rows, degraded := roomOperationalCapabilityRows(app, now, true, nil)
	scout, _ := rows[0]["scout"].(map[string]any)
	if scout["status"] != "not_invited" {
		t.Fatalf("uninvited Scout reported status=%v, want not_invited", scout["status"])
	}
	for _, entry := range degraded {
		if entry == "rooms.room-a.scout" {
			t.Fatalf("uninvited Scout in degraded: %v", degraded)
		}
	}

	// A provider outage is not a SCOUT fault where no Scout was asked for.
	// (The room's own transcription lane is a separate row and stays free to
	// report the outage on its own terms.)
	_, degraded = roomOperationalCapabilityRows(app, now, false, nil)
	for _, entry := range degraded {
		if entry == "rooms.room-a.scout" {
			t.Fatalf("provider outage degraded the Scout of a room that never invited one: %v", degraded)
		}
	}

	// An INVITED Scout with no runtime is a real fault and still degrades.
	app.mu.Lock()
	state.scoutInvited = true
	app.mu.Unlock()
	rows, degraded = roomOperationalCapabilityRows(app, now, true, nil)
	scout, _ = rows[0]["scout"].(map[string]any)
	if scout["status"] != "degraded" {
		t.Fatalf("invited-but-absent Scout reported status=%v, want degraded", scout["status"])
	}
	found := false
	for _, entry := range degraded {
		if entry == "rooms.room-a.scout" {
			found = true
		}
	}
	if !found {
		t.Fatalf("invited-but-absent Scout missing from degraded: %v", degraded)
	}
}

// TestTranscriptStaleEscalationRequiresAudioEvidence pins the difference
// between "audio is flowing and nothing is landing" and "nobody has spoken
// recently". Only the first is a fault; escalating the second flips /readyz to
// ok:false on a merely quiet meeting, which is the false alarm the whole
// watchdog design exists to avoid.
func TestTranscriptStaleEscalationRequiresAudioEvidence(t *testing.T) {
	now := time.Date(2026, 9, 3, 16, 3, 0, 0, time.UTC)
	state := &roomLiveState{
		id: "room-a", participants: map[string]time.Time{"AJ": now}, recordingEnabled: true,
		mediaActor: &roomMediaActor{}, mixer: &audioMixer{}, mediaGen: 4, mediaSittingID: "sitting-a",
		// The last thing anybody said landed ten minutes ago, and the room has
		// been silent since: no frames offered, no audio blocked.
		lastTranscriptFrameAt:  now.Add(-10 * time.Minute),
		lastTranscriptCommitAt: now.Add(-10 * time.Minute),
	}
	app := &kanbanBoardApp{memory: &meetingMemoryStore{entries: []meetingMemoryEntry{
		{ID: "tx-a", Kind: meetingMemoryKindTranscript, Text: "the last thing anyone said",
			CreatedAt: now.Add(-10 * time.Minute), Metadata: map[string]string{"roomId": "room-a", "meetingId": "sitting-a"}},
	}}, roomLive: map[string]*roomLiveState{"room-a": state}}

	rows, degraded := roomOperationalCapabilityRows(app, now, true, nil)
	transcript, _ := rows[0]["transcript"].(map[string]any)
	// The row still REPORTS the age honestly — it just is not an alarm.
	if transcript["status"] != "stale" {
		t.Fatalf("a ten-minute-old transcript reported status=%v; the stale branch is not being exercised and the test proves nothing", transcript["status"])
	}
	for _, entry := range degraded {
		if entry == "rooms.room-a.transcript" {
			t.Fatalf("a quiet meeting flipped /readyz to degraded: %v", degraded)
		}
	}

	// Now audio really is arriving and still nothing is landing. Same stale
	// transcript, different truth: this one escalates.
	app.mu.Lock()
	state.lastTranscriptFrameAt = now.Add(-time.Second)
	app.mu.Unlock()
	_, degraded = roomOperationalCapabilityRows(app, now, true, nil)
	found := false
	for _, entry := range degraded {
		if entry == "rooms.room-a.transcript" {
			found = true
		}
	}
	if !found {
		t.Fatalf("audio flowing with nothing landing did not escalate: %v", degraded)
	}

	// Audio refused UPSTREAM of the mixer is the same evidence: the consent
	// gate dropping every packet must not read as a quiet room.
	app.mu.Lock()
	state.lastTranscriptFrameAt = now.Add(-10 * time.Minute)
	state.lastAudioIngressAt = now.Add(-time.Second)
	app.mu.Unlock()
	rows, degraded = roomOperationalCapabilityRows(app, now, true, nil)
	found = false
	for _, entry := range degraded {
		if entry == "rooms.room-a.transcript" {
			found = true
		}
	}
	if !found {
		t.Fatalf("consent-blocked audio with nothing landing did not escalate: %v", degraded)
	}
	if rows[0]["lastBlockedAudioAt"] == nil {
		t.Fatalf("the row does not say WHERE the audio was seen: %v", rows[0])
	}
}
