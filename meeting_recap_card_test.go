package main

import (
	"context"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// D3: finalization posts exactly one recap card into the room's channel (the
// Table for the office) and never re-posts on retries.
func TestMeetingFinalizationPostsOneRecapCardIntoRoomChannel(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("BONFIRE_ROOMS_PATH", filepath.Join(t.TempDir(), "rooms.json"))
	app := newIsolatedKanbanBoardApp(t)
	previousApp := kanbanApp
	kanbanApp = app
	t.Cleanup(func() { kanbanApp = previousApp })
	app.mu.Lock()
	app.apiKey = "test-key"
	app.mu.Unlock()
	appRoomStore()
	table, err := app.ensureTable("aj@shareability.com")
	if err != nil {
		t.Fatal(err)
	}
	app.noteMeetingAdmission(officeRoomID, "AJ")
	authority := newAmbientConsentAuthorityForTest(t)
	grantAmbientConsentForTest(t, app, authority, officeRoomID, "tom@shareability.com")
	appendTestTranscript(t, app, "recap-card-tx-1", "We chose the durable launch plan and Tyler will publish the checklist.")
	record, ok := app.meetings.activeRecord(officeRoomID)
	if !ok {
		t.Fatal("missing active meeting")
	}
	source := app.meetingFinalizationSource(record.ID)
	if _, changed, err := app.meetings.endMeetingWithFinalization(record.ID, time.Now().UTC(), meetingEndedReasonIdle, "", source); err != nil || !changed {
		t.Fatalf("begin finalization changed=%v err=%v", changed, err)
	}
	var calls atomic.Int32
	finalized, err := app.finalizeMeetingCore(context.Background(), record.ID, meetingFinalizationResponder(&calls, "recap-card-tx-1"))
	if err != nil || finalized.Finalization == nil || finalized.Finalization.State != meetingFinalizationFinalized {
		t.Fatalf("finalize=%+v err=%v", finalized.Finalization, err)
	}
	messageID := meetingRecapCardMessageID(record.ID)
	if finalized.RecapCardPostedAt == "" || finalized.RecapCardThreadID != table.ID || finalized.RecapCardMessageID != messageID {
		t.Fatalf("recap stamp=%q thread=%q message=%q", finalized.RecapCardPostedAt, finalized.RecapCardThreadID, finalized.RecapCardMessageID)
	}
	countCards := func() (int, scoutChatMessageRecord) {
		thread, _, err := app.scoutChatThreadByID(table.OwnerEmail, table.ID)
		if err != nil {
			t.Fatal(err)
		}
		count := 0
		var card scoutChatMessageRecord
		for _, message := range thread.Messages {
			if message.ID == messageID {
				count++
				card = message
			}
		}
		return count, card
	}
	count, card := countCards()
	if count != 1 || card.Role != "scout" || card.AuthorName != scoutParticipantName {
		t.Fatalf("cards=%d card=%+v, want one Scout-authored card", count, card)
	}
	for _, want := range []string{"Meeting recap", "Action items", "Meeting Record: https://bonfire.test/?record=" + record.ID} {
		if !strings.Contains(card.Text, want) {
			t.Fatalf("card text missing %q:\n%s", want, card.Text)
		}
	}
	if payload := app.meetingRecordPayload(finalized, time.Now().UTC()); payload["recapCardThreadId"] != table.ID {
		t.Fatalf("payload=%v, want recap card stamp", payload)
	}

	// Retries and repeated finalization never re-post.
	again, err := app.finalizeMeetingCore(context.Background(), record.ID, meetingFinalizationResponder(&calls, "recap-card-tx-1"))
	if err != nil {
		t.Fatal(err)
	}
	if posted, err := app.postMeetingRecapCard(again); posted || err != nil {
		t.Fatalf("second post posted=%v err=%v, want idempotent skip", posted, err)
	}
	unstamped := again
	unstamped.RecapCardPostedAt = ""
	if posted, err := app.postMeetingRecapCard(unstamped); posted || err != nil {
		t.Fatalf("crash-replay post posted=%v err=%v, want dedupe by message id", posted, err)
	}
	if count, _ := countCards(); count != 1 {
		t.Fatalf("cards after retries=%d, want 1", count)
	}
}

// Rooms without a channel are skipped; a named room maps to the public
// channel carrying its name.
func TestMeetingRecapCardChannelMapping(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("BONFIRE_ROOMS_PATH", filepath.Join(t.TempDir(), "rooms.json"))
	app := newIsolatedKanbanBoardApp(t)
	previousApp := kanbanApp
	kanbanApp = app
	t.Cleanup(func() { kanbanApp = previousApp })
	room, err := appRoomStore().create("Ops", "", "aj@shareability.com", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := app.meetingRecapCardChannel(room.ID); ok {
		t.Fatal("a room without a channel must be skipped")
	}
	if _, ok := app.meetingRecapCardChannel(officeRoomID); ok {
		t.Fatal("the office without a Table must be skipped")
	}
	channel, err := app.createScoutChatThread("aj@shareability.com", "AJ", "Ops", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatal(err)
	}
	if resolved, ok := app.meetingRecapCardChannel(room.ID); !ok || resolved.ID != channel.ID {
		t.Fatalf("room channel=%+v ok=%v, want #Ops", resolved, ok)
	}
	table, err := app.ensureTable("aj@shareability.com")
	if err != nil {
		t.Fatal(err)
	}
	if resolved, ok := app.meetingRecapCardChannel(officeRoomID); !ok || resolved.ID != table.ID {
		t.Fatalf("office channel=%+v ok=%v, want the Table", resolved, ok)
	}
	// A record with no digest source never posts.
	record := meetingRecord{ID: "meeting-no-digest", Finalization: &meetingFinalizationReceipt{State: meetingFinalizationFinalized}}
	if posted, err := app.postMeetingRecapCard(record); posted || err != nil {
		t.Fatalf("no-source post posted=%v err=%v", posted, err)
	}
	text := buildMeetingRecapCardText(meetingRecord{ID: "m1", Title: "Launch", StartedAt: "2026-09-01T10:00:00Z", EndedAt: "2026-09-01T11:12:00Z", Participants: []string{"AJ", "Tim"}},
		meetingDigestPayload{Decisions: []meetingDigestDecision{{D: "Ship Friday"}, {D: ""}}, ActionItems: []meetingDigestAction{{A: "Write the checklist", Owner: "Tyler"}}}, "the office", time.Now().UTC())
	for _, want := range []string{"Meeting recap — Launch", "the office · 1h 12m · 2 people", "Decisions\n• Ship Friday", "Action items\n• Write the checklist — Tyler", "Meeting Record: https://bonfire.test/?record=m1"} {
		if !strings.Contains(text, want) {
			t.Fatalf("card text missing %q:\n%s", want, text)
		}
	}
}
