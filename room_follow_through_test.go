package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestParseRoomRecapFollowThroughRequiresIntentAndExactDestination(t *testing.T) {
	for _, test := range []struct {
		text      string
		title     string
		requested bool
	}{
		{"@Scout post a recap to the Ball Dogs channel after the call", "Ball Dogs", true},
		{"@Scout put meeting notes from this call in #team thread", "team", true},
		{"@Scout post the recap to #team after the call", "team", true},
		{"@Scout post a recap after the call", "", true},
		{"@Scout post a recap to a channel after the call", "", true},
		{"@Scout what is your opinion?", "", false},
	} {
		title, requested := parseRoomRecapFollowThrough(test.text)
		if title != test.title || requested != test.requested {
			t.Fatalf("parse %q = (%q,%t), want (%q,%t)", test.text, title, requested, test.title, test.requested)
		}
	}
}

func TestRoomRecapFollowThroughPersistsBeforeAckAndDeliversExactlyOnce(t *testing.T) {
	app := newW2ATestApp(t)
	defer app.Close()
	roomID := "room-follow1111"
	sittingID := app.memory.ensureMeetingID(roomID)
	if _, changed := app.meetings.startMeeting(roomID, sittingID, time.Now().UTC().Add(-time.Minute), []string{"AJ"}); !changed {
		t.Fatal("start meeting")
	}
	app.mu.Lock()
	state := app.roomLiveLocked(roomID)
	state.mediaGen = 4
	state.mediaSittingID = sittingID
	app.mu.Unlock()
	reauthorizations := 0
	app.catchUpRecapResolver = testCatchUpResolver{
		resolve: func(_ context.Context, request BrainRetrievalRequest) (BrainRetrievalResult, error) {
			return validCatchUpRetrieval(t, request, "Decision: ship the stable Scout room track.", RecallSourceFresh), nil
		},
		reauth: func(_ context.Context, _ ACLPrincipal, _ RetrievalSnapshot) error {
			reauthorizations++
			return nil
		},
	}

	channel, err := app.createScoutChatThread("aj@shareability.com", "AJ", "Ball Dogs", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatal(err)
	}
	scope := RoomScoutScope{RoomID: roomID, SittingID: sittingID, MediaGeneration: 4}
	record, err := app.scheduleRoomRecapFollowThrough(scope, "room-message-1", "@Scout post the recap to Ball Dogs channel", "aj@shareability.com", "AJ", "Ball Dogs")
	if err != nil {
		t.Fatal(err)
	}
	if record.Status != roomFollowThroughQueued || record.DeliveryMessageID == "" {
		t.Fatalf("record=%+v", record)
	}
	if raw, err := os.ReadFile(roomFollowThroughPath()); err != nil || !strings.Contains(string(raw), record.ID) || !strings.Contains(string(raw), roomFollowThroughQueued) {
		t.Fatalf("durable receipt missing before acknowledgement err=%v raw=%s", err, raw)
	}
	if _, _, err := app.memory.appendAmbientEntry(meetingMemoryKindBrain, "brain-follow-through", "Decision: ship the stable Scout room track.", map[string]string{
		"roomId": roomID, "meetingId": sittingID,
	}); err != nil {
		t.Fatal(err)
	}
	if delivered := app.flushRoomFollowThroughForMeeting(roomID, sittingID, "test"); delivered != 1 {
		t.Fatalf("delivered=%d want 1", delivered)
	}
	if delivered := app.flushRoomFollowThroughForMeeting(roomID, sittingID, "retry"); delivered != 0 {
		t.Fatalf("retry delivered=%d want 0", delivered)
	}
	updated, _, err := app.scoutChatThreadByID("aj@shareability.com", channel.ID)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, message := range updated.Messages {
		if message.ID == record.DeliveryMessageID {
			count++
			if !strings.Contains(message.Text, "stable Scout room track") {
				t.Fatalf("recap=%q", message.Text)
			}
		}
	}
	if count != 1 {
		t.Fatalf("delivery count=%d want 1", count)
	}
	if reauthorizations != 1 {
		t.Fatalf("source reauthorizations=%d want 1", reauthorizations)
	}
}

func TestRoomRecapFollowThroughDoesNotPublishAfterSourceAuthorizationRevocation(t *testing.T) {
	app := newW2ATestApp(t)
	defer app.Close()
	roomID := "room-follow2222"
	sittingID := app.memory.ensureMeetingID(roomID)
	if _, changed := app.meetings.startMeeting(roomID, sittingID, time.Now().UTC().Add(-time.Minute), []string{"AJ"}); !changed {
		t.Fatal("start meeting")
	}
	app.mu.Lock()
	state := app.roomLiveLocked(roomID)
	state.mediaGen = 5
	state.mediaSittingID = sittingID
	app.mu.Unlock()
	app.catchUpRecapResolver = testCatchUpResolver{
		resolve: func(_ context.Context, request BrainRetrievalRequest) (BrainRetrievalResult, error) {
			return validCatchUpRetrieval(t, request, "REVOKED SOURCE CANARY", RecallSourceFresh), nil
		},
		reauth: func(_ context.Context, _ ACLPrincipal, _ RetrievalSnapshot) error {
			return ErrBrainSourceConsentAbsent
		},
	}
	channel, err := app.createScoutChatThread("aj@shareability.com", "AJ", "Revoked Recap", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatal(err)
	}
	scope := RoomScoutScope{RoomID: roomID, SittingID: sittingID, MediaGeneration: 5}
	record, err := app.scheduleRoomRecapFollowThrough(scope, "room-message-revoked", "@Scout post the recap to Revoked Recap channel", "aj@shareability.com", "AJ", "Revoked Recap")
	if err != nil {
		t.Fatal(err)
	}
	if delivered := app.flushRoomFollowThroughForMeeting(roomID, sittingID, "test_revoked"); delivered != 0 {
		t.Fatalf("delivered=%d want 0", delivered)
	}
	thread, _, err := app.scoutChatThreadByID("aj@shareability.com", channel.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range thread.Messages {
		if message.ID == record.DeliveryMessageID || strings.Contains(message.Text, "REVOKED SOURCE CANARY") {
			t.Fatalf("revoked recap escaped into destination: %+v", message)
		}
	}
	app.roomFollowThroughMu.Lock()
	defer app.roomFollowThroughMu.Unlock()
	for _, candidate := range app.roomFollowThrough {
		if candidate.ID == record.ID {
			if candidate.Status != roomFollowThroughAwaitingInput || !strings.Contains(candidate.LastError, ErrBrainSourceConsentAbsent.Error()) {
				t.Fatalf("record=%+v", candidate)
			}
			return
		}
	}
	t.Fatal("follow-through record missing")
}

func TestRoomFollowThroughCompactionNeverDropsUnfinishedWork(t *testing.T) {
	records := make([]roomFollowThroughRecord, 0, roomFollowThroughStoreCap+3)
	for index := 0; index < roomFollowThroughStoreCap+2; index++ {
		records = append(records, roomFollowThroughRecord{ID: fmt.Sprintf("delivered-%03d", index), Status: roomFollowThroughDelivered})
	}
	records = append([]roomFollowThroughRecord{{ID: "queued-oldest", Status: roomFollowThroughQueued}}, records...)
	compacted := compactRoomFollowThroughRecords(records)
	if len(compacted) != roomFollowThroughStoreCap {
		t.Fatalf("compacted=%d want %d", len(compacted), roomFollowThroughStoreCap)
	}
	if compacted[0].ID != "queued-oldest" {
		t.Fatalf("oldest unfinished record was evicted: first=%+v", compacted[0])
	}
	unfinished := make([]roomFollowThroughRecord, roomFollowThroughStoreCap+1)
	for index := range unfinished {
		unfinished[index] = roomFollowThroughRecord{ID: fmt.Sprintf("queued-%03d", index), Status: roomFollowThroughQueued}
	}
	if got := compactRoomFollowThroughRecords(unfinished); len(got) != len(unfinished) {
		t.Fatalf("unfinished work was capped: got=%d want=%d", len(got), len(unfinished))
	}
}
