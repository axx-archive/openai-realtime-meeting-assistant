package main

import (
	"context"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// D3: finalization posts exactly one recap card into the meeting's channel
// (#meetings for the office — AJ 2026-09-02 — never Bonfire Chat) and never
// re-posts on retries.
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
	meetings, ok := app.findMeetingsChannelThread()
	if !ok || meetings.ID == table.ID || scoutChatThreadSystem(meetings) != scoutChatSystemMeetings || meetings.Title != meetingsChannelTitle {
		t.Fatalf("#meetings=%+v ok=%v, want the system channel provisioned by the first recap", meetings, ok)
	}
	if finalized.RecapCardPostedAt == "" || finalized.RecapCardThreadID != meetings.ID || finalized.RecapCardMessageID != messageID {
		t.Fatalf("recap stamp=%q thread=%q message=%q, want #meetings %s", finalized.RecapCardPostedAt, finalized.RecapCardThreadID, finalized.RecapCardMessageID, meetings.ID)
	}
	if bonfire, _, err := app.scoutChatThreadByID(table.OwnerEmail, table.ID); err != nil || scoutChatMessageIndex(bonfire, messageID) >= 0 {
		t.Fatalf("Bonfire Chat err=%v carries the card=%v, want office recaps only in #meetings", err, scoutChatMessageIndex(bonfire, messageID) >= 0)
	}
	countCards := func() (int, scoutChatMessageRecord) {
		thread, _, err := app.scoutChatThreadByID(meetings.OwnerEmail, meetings.ID)
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
	// compact card: decisions + the mono overflow footer, no action-item list
	for _, want := range []string{"Meeting recap", "Decisions\n• Choose vendor Zebra", "1 action item · 1 open", "Meeting Record: https://bonfire.test/?record=" + record.ID} {
		if !strings.Contains(card.Text, want) {
			t.Fatalf("card text missing %q:\n%s", want, card.Text)
		}
	}
	if strings.Contains(card.Text, "Action items") || strings.Contains(card.Text, "Draft the pricing sheet") {
		t.Fatalf("compact card must not list action items:\n%s", card.Text)
	}
	if payload := app.meetingRecordPayload(finalized, time.Now().UTC()); payload["recapCardThreadId"] != meetings.ID {
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
// channel carrying its name; the office maps to #meetings, minted on demand
// and stable across calls.
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
	if _, ok := app.findMeetingsChannelThread(); ok {
		t.Fatal("#meetings must not exist before the first office recap or list")
	}
	office, ok := app.meetingRecapCardChannel(officeRoomID)
	if !ok || scoutChatThreadSystem(office) != scoutChatSystemMeetings || office.Title != meetingsChannelTitle || !scoutChatThreadIsOrganizationPublic(office) {
		t.Fatalf("office channel=%+v ok=%v, want a public #meetings minted on first recap", office, ok)
	}
	if again, ok := app.meetingRecapCardChannel(officeRoomID); !ok || again.ID != office.ID {
		t.Fatalf("office channel again=%+v ok=%v, want the same #meetings %s", again, ok, office.ID)
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
	if resolved, ok := app.meetingRecapCardChannel(officeRoomID); !ok || resolved.ID != office.ID || resolved.ID == table.ID {
		t.Fatalf("office channel=%+v ok=%v, want #meetings %s, never the Table %s", resolved, ok, office.ID, table.ID)
	}
	// A record with no digest source never posts.
	record := meetingRecord{ID: "meeting-no-digest", Finalization: &meetingFinalizationReceipt{State: meetingFinalizationFinalized}}
	if posted, err := app.postMeetingRecapCard(record); posted || err != nil {
		t.Fatalf("no-source post posted=%v err=%v", posted, err)
	}
	text := buildMeetingRecapCardText(meetingRecord{ID: "m1", Title: "Launch", StartedAt: "2026-09-01T10:00:00Z", EndedAt: "2026-09-01T11:12:00Z", Participants: []string{"AJ", "Tim"}},
		meetingDigestPayload{Decisions: []meetingDigestDecision{{D: "Ship Friday"}, {D: ""}}, ActionItems: []meetingDigestAction{{A: "Write the checklist", Owner: "Tyler"}}}, "the office", time.Now().UTC())
	for _, want := range []string{"Meeting recap — Launch", "the office · 1h 12m · 2 people", "Decisions\n• Ship Friday", "\n1 action item\n", "Meeting Record: https://bonfire.test/?record=m1"} {
		if !strings.Contains(text, want) {
			t.Fatalf("card text missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "Write the checklist") {
		t.Fatalf("compact card must not list action items:\n%s", text)
	}
}

// AJ 2026-09-02: compact recap card — "the top THREE decisions, by the
// digest's own order": the SERVER truncates, and decision.Importance is
// deliberately NOT a re-ranking key (it is optional and model-emitted, so
// ranking on it would reorder the card away from the order the decisions were
// made in and leave "+N decisions" ambiguous). Plus a mono footer counting
// what the card leaves off, one Meeting Record link, no action-item list.
func TestMeetingRecapCardCompactPayloadShape(t *testing.T) {
	t.Setenv("BONFIRE_PUBLIC_URL", "https://bonfire.test")
	record := meetingRecord{ID: "m-compact", Title: "Roadmap", StartedAt: "2026-09-02T09:00:00Z", EndedAt: "2026-09-02T09:45:00Z", Participants: []string{"AJ", "Tim", "Tyler"}}
	payload := meetingDigestPayload{
		// importance is scrambled on purpose: a high-importance decision late
		// in the digest must NOT jump the queue.
		Decisions: []meetingDigestDecision{
			{D: "First: pick the vendor", Importance: 2},
			{D: "   "},
			{D: "Second: freeze scope", Importance: 1},
			{D: "Third: ship Friday", Importance: 3},
			{D: "Fourth (most important, still cut)", Importance: 9},
			{D: "Fifth", Importance: 8},
		},
		ActionItems:   []meetingDigestAction{{A: "Write the plan", Owner: "Tyler"}, {A: "Book the review"}, {A: ""}},
		OpenQuestions: []meetingDigestQuestion{{Q: "Which SKU ships first?"}},
	}
	card := buildMeetingRecapCard(record, payload, "the office", time.Now().UTC())
	if card.Title != "Roadmap" || card.Meta != "the office · 45 min · 3 people" || card.RecordPath != "https://bonfire.test/?record=m-compact" {
		t.Fatalf("card=%+v, want title/meta/record path", card)
	}
	if want := []string{"First: pick the vendor", "Second: freeze scope", "Third: ship Friday"}; strings.Join(card.Decisions, "|") != strings.Join(want, "|") {
		t.Fatalf("decisions=%v, want the digest's own first three %v (never importance-ranked)", card.Decisions, want)
	}
	if card.MoreDecisions != 2 || card.ActionItems != 2 || card.OpenQuestions != 1 {
		t.Fatalf("counts=%+v, want +2 decisions · 2 action items · 1 open", card)
	}
	if card.Footer() != "+2 decisions · 2 action items · 1 open" {
		t.Fatalf("footer=%q", card.Footer())
	}
	text := card.Text()
	want := "**Meeting recap — Roadmap**\nthe office · 45 min · 3 people\n\nDecisions\n• First: pick the vendor\n• Second: freeze scope\n• Third: ship Friday\n\n+2 decisions · 2 action items · 1 open\n\nMeeting Record: https://bonfire.test/?record=m-compact"
	if text != want {
		t.Fatalf("text=\n%s\nwant\n%s", text, want)
	}
	// a blank decision is never counted as one left off
	blanks := buildMeetingRecapCard(record, meetingDigestPayload{Decisions: []meetingDigestDecision{{D: "A"}, {D: " "}, {D: "B"}, {D: ""}, {D: "C"}, {D: "  "}}}, "", time.Now().UTC())
	if len(blanks.Decisions) != 3 || blanks.MoreDecisions != 0 || blanks.Footer() != "" {
		t.Fatalf("blank-padded card=%+v, want three decisions and no overflow footer", blanks)
	}
	// nothing left off → no footer; no decisions → the honest empty line
	bare := buildMeetingRecapCard(record, meetingDigestPayload{Decisions: []meetingDigestDecision{{D: "Only one"}}}, "", time.Now().UTC())
	if bare.Footer() != "" || strings.Contains(bare.Text(), "\n\n\n") || !strings.Contains(bare.Text(), "Decisions\n• Only one\n\nMeeting Record:") {
		t.Fatalf("bare card=%+v text=\n%s", bare, bare.Text())
	}
	empty := buildMeetingRecapCard(record, meetingDigestPayload{ActionItems: []meetingDigestAction{{A: "Follow up"}}}, "", time.Now().UTC())
	if !strings.Contains(empty.Text(), "No grounded decisions were captured.\n\n1 action item\n") {
		t.Fatalf("empty-decisions text=\n%s", empty.Text())
	}
}

// A meeting finalized BEFORE office recaps moved to #meetings can have its
// card sitting in Bonfire Chat with no stamp — stampMeetingRecapCard is
// fail-soft. The dedupe re-reads only the TARGET thread, so moving the target
// would otherwise post a second copy of the same card into #meetings. The
// one-card guarantee is per meeting, not per channel.
func TestMeetingRecapCardHonorsStamplessBonfireChatCard(t *testing.T) {
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
	record, ok := app.meetings.activeRecord(officeRoomID)
	if !ok {
		t.Fatal("missing active meeting")
	}
	messageID := meetingRecapCardMessageID(record.ID)
	// The pre-change world: the card landed in Bonfire Chat and the fail-soft
	// stamp write did not happen, so the record carries no RecapCardThreadID.
	commitTestChatMessage(t, app, table, messageID, "**Meeting recap — posted before the move**")

	authority := newAmbientConsentAuthorityForTest(t)
	grantAmbientConsentForTest(t, app, authority, officeRoomID, "tom@shareability.com")
	appendTestTranscript(t, app, "recap-card-legacy-tx-1", "We chose the durable launch plan and Tyler will publish the checklist.")
	source := app.meetingFinalizationSource(record.ID)
	if _, changed, err := app.meetings.endMeetingWithFinalization(record.ID, time.Now().UTC(), meetingEndedReasonIdle, "", source); err != nil || !changed {
		t.Fatalf("begin finalization changed=%v err=%v", changed, err)
	}
	var calls atomic.Int32
	finalized, err := app.finalizeMeetingCore(context.Background(), record.ID, meetingFinalizationResponder(&calls, "recap-card-legacy-tx-1"))
	if err != nil || finalized.Finalization == nil || finalized.Finalization.State != meetingFinalizationFinalized {
		t.Fatalf("finalize=%+v err=%v", finalized.Finalization, err)
	}

	// The stamp now points at where the card actually is, not at the new target.
	if finalized.RecapCardPostedAt == "" || finalized.RecapCardThreadID != table.ID || finalized.RecapCardMessageID != messageID {
		t.Fatalf("recap stamp=%q thread=%q message=%q, want the Bonfire Chat card at %s adopted", finalized.RecapCardPostedAt, finalized.RecapCardThreadID, finalized.RecapCardMessageID, table.ID)
	}
	countCards := func(thread scoutChatThreadRecord) int {
		current, _, err := app.scoutChatThreadByID(thread.OwnerEmail, thread.ID)
		if err != nil {
			t.Fatal(err)
		}
		count := 0
		for _, message := range current.Messages {
			if message.ID == messageID {
				count++
			}
		}
		return count
	}
	meetings, ok := app.findMeetingsChannelThread()
	if !ok || meetings.ID == table.ID {
		t.Fatalf("#meetings=%+v ok=%v, want the system channel", meetings, ok)
	}
	if got := countCards(meetings); got != 0 {
		t.Fatalf("#meetings carries %d copies of the card, want 0 — the meeting already has one in Bonfire Chat", got)
	}
	if got := countCards(table); got != 1 {
		t.Fatalf("Bonfire Chat carries %d copies of the card, want the original 1", got)
	}

	// A crash-replay whose stamp is gone again still refuses to re-post.
	unstamped := finalized
	unstamped.RecapCardPostedAt = ""
	unstamped.RecapCardThreadID = ""
	unstamped.RecapCardMessageID = ""
	if posted, err := app.postMeetingRecapCard(unstamped); posted || err != nil {
		t.Fatalf("crash-replay post posted=%v err=%v, want the Bonfire Chat card to count", posted, err)
	}
	if got := countCards(meetings); got != 0 {
		t.Fatalf("#meetings carries %d copies after the replay, want 0", got)
	}
}
