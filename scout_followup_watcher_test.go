package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func setupScoutFollowupTestApp(t *testing.T) *kanbanBoardApp {
	t.Helper()
	setupAuthTestEnv(t)
	t.Setenv("OPENAI_API_KEY", "")
	previousApp := kanbanApp
	app := newIsolatedKanbanBoardApp(t)
	kanbanApp = app
	t.Cleanup(func() { kanbanApp = previousApp })
	return app
}

type followupFixture struct {
	t      *testing.T
	app    *kanbanBoardApp
	thread scoutChatThreadRecord
	clock  time.Time
	seq    int
}

func newFollowupChannel(t *testing.T, app *kanbanBoardApp, id string, visibility string) *followupFixture {
	t.Helper()
	thread, _, err := app.ensureScoutChatThread(id, "aj@shareability.com", "AJ", "packaging", visibility, nil)
	if err != nil {
		t.Fatalf("create thread: %v", err)
	}
	return &followupFixture{t: t, app: app, thread: thread, clock: time.Now().UTC().Add(-30 * time.Minute)}
}

func (fixture *followupFixture) post(message scoutChatMessageRecord) scoutChatMessageRecord {
	fixture.t.Helper()
	fixture.seq++
	fixture.clock = fixture.clock.Add(20 * time.Second)
	if message.ID == "" {
		message.ID = fixture.thread.ID + "-msg-" + strings.Repeat("x", fixture.seq)
	}
	if message.Kind == "" {
		message.Kind = "message"
	}
	if message.CreatedAt == "" {
		message.CreatedAt = fixture.clock.Format(time.RFC3339Nano)
	}
	saved, err := fixture.app.commitScoutChatThreadMessages("aj@shareability.com", fixture.thread.ID, message)
	if err != nil {
		fixture.t.Fatalf("commit %s: %v", message.ID, err)
	}
	fixture.thread = saved
	return message
}

func (fixture *followupFixture) scout(text string) scoutChatMessageRecord {
	return fixture.post(scoutChatMessageRecord{Role: "scout", AuthorName: scoutParticipantName, Text: text})
}

func (fixture *followupFixture) human(email, name, text string, replyTo *scoutChatMessageRecord) scoutChatMessageRecord {
	message := scoutChatMessageRecord{Role: "user", AuthorEmail: email, AuthorName: name, Text: text}
	if replyTo != nil {
		message.ReplyTo = &scoutChatReplyRef{MessageID: replyTo.ID, AuthorName: replyTo.AuthorName, AuthorEmail: replyTo.AuthorEmail, Text: replyTo.Text}
	}
	return fixture.post(message)
}

func (fixture *followupFixture) reload() scoutChatThreadRecord {
	fixture.t.Helper()
	thread, _, err := fixture.app.scoutChatThreadByID("aj@shareability.com", fixture.thread.ID)
	if err != nil {
		fixture.t.Fatalf("reload: %v", err)
	}
	fixture.thread = thread
	return thread
}

func (fixture *followupFixture) decide(responder openAITextResponder) scoutFollowupDecision {
	fixture.t.Helper()
	thread := fixture.reload()
	review := fixture.app.scoutFollowupReview(thread, "", time.Now().UTC())
	decision := fixture.app.scoutFollowupDecide(context.Background(), "test-key", responder, review)
	fixture.app.journalScoutFollowupDecision(review, decision)
	return decision
}

func (fixture *followupFixture) scoutMessagesAfter(messageID string) []scoutChatMessageRecord {
	thread := fixture.reload()
	after := false
	var found []scoutChatMessageRecord
	for _, message := range thread.Messages {
		if message.ID == messageID {
			after = true
			continue
		}
		if after && scoutFollowupMessageIsScout(message) {
			found = append(found, message)
		}
	}
	return found
}

func followupJournalRows(app *kanbanBoardApp, threadID string) []meetingMemoryEntry {
	var rows []meetingMemoryEntry
	for _, entry := range app.memory.entriesOfKind(meetingMemoryKindRunLog, 0) {
		if entry.Metadata["workflow"] == scoutFollowupWorkflow && entry.Metadata["threadId"] == threadID {
			rows = append(rows, entry)
		}
	}
	return rows
}

func followupResponder(t *testing.T, verdict string, reply string, capture *[]openAITextRequest) openAITextResponder {
	return func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		if request.Workflow != scoutFollowupWorkflow || request.Seat != seatChat || request.JSONSchema == nil {
			t.Fatalf("follow-up request missing its contract: seat=%s workflow=%s", request.Seat, request.Workflow)
		}
		if capture != nil {
			*capture = append(*capture, request)
		}
		return `{"verdict":"` + verdict + `","reason":"test","reply":` + jsonQuote(reply) + `}`, nil
	}
}

func jsonQuote(value string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(value, `\`, `\\`), `"`, `\"`) + `"`
}

func TestScoutFollowupSilentOnAcknowledgementAndJournals(t *testing.T) {
	app := setupScoutFollowupTestApp(t)
	fixture := newFollowupChannel(t, app, "followup-ack", scoutChatVisibilityPublic)
	scout := fixture.scout("Here's the vendor comparison you asked for.")
	fixture.human("tim@shareability.com", "Tim", "thanks!", &scout)
	calls := 0
	decision := fixture.decide(func(context.Context, string, openAITextRequest) (string, error) {
		calls++
		return "", errors.New("must not be called")
	})
	if decision.Verdict != scoutFollowupVerdictSilent || decision.Reason != "acknowledgement" || calls != 0 {
		t.Fatalf("decision=%+v calls=%d, want silent/acknowledgement without a model call", decision, calls)
	}
	if got := fixture.scoutMessagesAfter(scout.ID); len(got) != 0 {
		t.Fatalf("silent verdict posted %d messages", len(got))
	}
	rows := followupJournalRows(app, fixture.thread.ID)
	if len(rows) != 1 || rows[0].Metadata["verdict"] != "silent" || rows[0].Metadata["reason"] != "acknowledgement" || rows[0].Metadata["provenance"] != "deterministic" || rows[0].Metadata["messageIds"] == "" {
		t.Fatalf("journal rows=%+v", rows)
	}
}

func TestScoutFollowupRepliesToDirectQuestionWithModelProvenance(t *testing.T) {
	app := setupScoutFollowupTestApp(t)
	fixture := newFollowupChannel(t, app, "followup-question", scoutChatVisibilityPublic)
	scout := fixture.scout("Zebra is the cheaper vendor at volume.")
	question := fixture.human("tim@shareability.com", "Tim", "does that hold at 10k units too?", &scout)
	var requests []openAITextRequest
	decision := fixture.decide(followupResponder(t, "reply", "@Tim yes — Zebra's tier pricing kicks in at 5k, so 10k is still cheaper.", &requests))
	if decision.Verdict != scoutFollowupVerdictReply || decision.Reason != "direct_question" || len(requests) != 1 {
		t.Fatalf("decision=%+v requests=%d", decision, len(requests))
	}
	if !strings.HasPrefix(decision.Provenance, "model:") {
		t.Fatalf("provenance=%q, want model provenance", decision.Provenance)
	}
	if !strings.Contains(requests[0].Input, "NEW Tim (replying to Scout): does that hold") {
		t.Fatalf("model input must mark the new reply:\n%s", requests[0].Input)
	}
	posted := fixture.scoutMessagesAfter(question.ID)
	if len(posted) != 1 || posted[0].Via != scoutFollowupVia || posted[0].ReplyTo == nil || posted[0].ReplyTo.MessageID != question.ID || posted[0].ID != decision.ReplyID {
		t.Fatalf("posted=%+v", posted)
	}
	if posted[0].IntentOutcome == "forced" {
		t.Fatal("a plain question is not a forced reply")
	}
	rows := followupJournalRows(app, fixture.thread.ID)
	if len(rows) != 1 || rows[0].Metadata["verdict"] != "reply" || rows[0].Metadata["replyMessageId"] != posted[0].ID || !strings.HasPrefix(rows[0].Metadata["provenance"], "model:") {
		t.Fatalf("journal rows=%+v", rows)
	}
}

func TestScoutFollowupRateLimitsUnsolicitedButMentionAlwaysForces(t *testing.T) {
	app := setupScoutFollowupTestApp(t)
	fixture := newFollowupChannel(t, app, "followup-rate", scoutChatVisibilityPublic)
	scout := fixture.scout("Draft two is up.")
	first := fixture.human("tim@shareability.com", "Tim", "can you tighten slide 3?", &scout)
	// Scout's unsolicited follow-up one minute ago.
	fixture.post(scoutChatMessageRecord{Role: "scout", AuthorName: scoutParticipantName, Via: scoutFollowupVia, Text: "@Tim done — slide 3 is tighter.",
		CreatedAt: time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano), ReplyTo: &scoutChatReplyRef{MessageID: first.ID, AuthorName: "Tim"}})
	followup := fixture.reload().Messages[len(fixture.thread.Messages)-1]
	second := fixture.human("tim@shareability.com", "Tim", "and slide 4?", &followup)
	second.CreatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	calls := 0
	decision := fixture.decide(func(context.Context, string, openAITextRequest) (string, error) {
		calls++
		return "", errors.New("rate-limited turn must not call the model")
	})
	if decision.Verdict != scoutFollowupVerdictSilent || decision.Reason != "rate_limited" || calls != 0 {
		t.Fatalf("decision=%+v calls=%d, want rate_limited", decision, calls)
	}
	// A fresh @scout mention always forces a reply, rate limit or not.
	tagged := fixture.human("tim@shareability.com", "Tim", "@scout what about slide 4?", &followup)
	decision = fixture.decide(followupResponder(t, "reply", "@Tim slide 4 is next — give me a minute.", nil))
	if decision.Verdict != scoutFollowupVerdictReply || decision.Reason != "mentioned" || !decision.Forced {
		t.Fatalf("decision=%+v, want forced reply", decision)
	}
	posted := fixture.scoutMessagesAfter(tagged.ID)
	if len(posted) != 1 || posted[0].IntentOutcome != "forced" || posted[0].ReplyTo == nil || posted[0].ReplyTo.MessageID != tagged.ID {
		t.Fatalf("posted=%+v", posted)
	}
	rows := followupJournalRows(app, fixture.thread.ID)
	if len(rows) != 2 || rows[1].Metadata["forced"] != "true" {
		t.Fatalf("journal rows=%+v", rows)
	}
}

func TestScoutFollowupReconcilesSeveralPeopleInOneAnswer(t *testing.T) {
	app := setupScoutFollowupTestApp(t)
	fixture := newFollowupChannel(t, app, "followup-multi", scoutChatVisibilityPublic)
	scout := fixture.scout("Two ways to open the deck: lead with the numbers, or with the founder story.")
	fixture.human("tim@shareability.com", "Tim", "numbers first, investors want data-led", &scout)
	tyler := fixture.human("tyler@shareability.com", "Tyler", "I'd go narrative, the story is the hook", &scout)
	var requests []openAITextRequest
	reply := "@Tim wants it data-led, @Tyler prefers narrative — I'll lead with data and keep a narrative thread; say the word to flip."
	decision := fixture.decide(followupResponder(t, "reply", reply, &requests))
	if decision.Verdict != scoutFollowupVerdictReply || decision.Reason != "reconcile_opinions" || len(requests) != 1 {
		t.Fatalf("decision=%+v requests=%d", decision, len(requests))
	}
	input := requests[0].Input
	if !strings.Contains(input, "NEW Tim") || !strings.Contains(input, "NEW Tyler") || !strings.Contains(input, "2 different people replied; reconcile") {
		t.Fatalf("model input must carry every voice:\n%s", input)
	}
	posted := fixture.scoutMessagesAfter(tyler.ID)
	if len(posted) != 1 || posted[0].Text != reply || posted[0].ReplyTo == nil || posted[0].ReplyTo.MessageID != tyler.ID {
		t.Fatalf("posted=%+v, want ONE reconciled answer on the newest reply", posted)
	}
	// Keyless: the deterministic reconciliation still names both people and
	// offers the flip, in one message.
	keyless := newFollowupChannel(t, app, "followup-multi-keyless", scoutChatVisibilityPublic)
	opener := keyless.scout("Numbers or story first?")
	keyless.human("tim@shareability.com", "Tim", "numbers first", &opener)
	last := keyless.human("tyler@shareability.com", "Tyler", "story first", &opener)
	thread := keyless.reload()
	review := app.scoutFollowupReview(thread, "", time.Now().UTC())
	decision = app.scoutFollowupDecide(context.Background(), "", nil, review)
	if decision.Verdict != scoutFollowupVerdictReply || !strings.HasPrefix(decision.Provenance, "deterministic:") {
		t.Fatalf("keyless decision=%+v", decision)
	}
	posted = keyless.scoutMessagesAfter(last.ID)
	if len(posted) != 1 || !strings.Contains(posted[0].Text, "Tim:") || !strings.Contains(posted[0].Text, "Tyler:") || !strings.Contains(posted[0].Text, "say the word to flip") {
		t.Fatalf("keyless reconciliation=%+v", posted)
	}
}

func TestScoutFollowupNeverRepliesToItselfOrAnAnsweredTurn(t *testing.T) {
	app := setupScoutFollowupTestApp(t)
	fixture := newFollowupChannel(t, app, "followup-self", scoutChatVisibilityPublic)
	scout := fixture.scout("Posting the summary now.")
	// Only Scout has spoken since: nothing fresh, nothing decided, no journal.
	thread := fixture.reload()
	review := app.scoutFollowupReview(thread, "", time.Now().UTC())
	if len(review.fresh) != 0 || len(review.addressed) != 0 {
		t.Fatalf("review=%+v, want nothing fresh after Scout's own message", review)
	}
	if _, err := app.postScoutFollowupReply(thread, scout, "talking to myself", false); err == nil {
		t.Fatal("posting a reply to Scout's own message must be refused")
	}
	// A reply the synchronous append path already answered is silent.
	question := fixture.human("tim@shareability.com", "Tim", "which vendor?", &scout)
	fixture.post(scoutChatMessageRecord{Role: "scout", AuthorName: scoutParticipantName, Text: "Zebra.", CausedByMessageID: question.ID,
		ReplyTo: &scoutChatReplyRef{MessageID: question.ID, AuthorName: "Tim"}})
	thread = fixture.reload()
	review = app.scoutFollowupReview(thread, scout.ID, time.Now().UTC())
	if len(review.addressed) != 1 || !review.answered {
		t.Fatalf("review=%+v, want the answered turn recognised", review)
	}
	decision := app.scoutFollowupDecide(context.Background(), "test-key", followupResponder(t, "reply", "never", nil), review)
	if decision.Verdict != scoutFollowupVerdictSilent || decision.Reason != "already_answered" {
		t.Fatalf("decision=%+v, want silent/already_answered", decision)
	}
	if got := fixture.scoutMessagesAfter(question.ID); len(got) != 1 {
		t.Fatalf("scout messages after the question=%d, want only the synchronous answer", len(got))
	}
}

func TestScoutFollowupStaysSilentWhenPeopleTalkToEachOther(t *testing.T) {
	app := setupScoutFollowupTestApp(t)
	fixture := newFollowupChannel(t, app, "followup-aside", scoutChatVisibilityPublic)
	scout := fixture.scout("Vendor comparison posted.")
	tim := fixture.human("tim@shareability.com", "Tim", "Tyler can you own the pricing sheet?", nil)
	fixture.human("tyler@shareability.com", "Tyler", "on it", &tim)
	thread := fixture.reload()
	review := app.scoutFollowupReview(thread, scout.ID, time.Now().UTC())
	if len(review.fresh) != 2 || len(review.addressed) != 0 {
		t.Fatalf("review fresh=%d addressed=%d, want two fresh human-to-human messages and nothing addressed", len(review.fresh), len(review.addressed))
	}
	if rows := followupJournalRows(app, fixture.thread.ID); len(rows) != 0 {
		t.Fatalf("nothing addressed must not journal: %+v", rows)
	}
}

// Scout was the last speaker and one person replied without a question: the
// model may auto-reply, and its silent verdict is honoured.
func TestScoutFollowupSingleReplyModelDecides(t *testing.T) {
	app := setupScoutFollowupTestApp(t)
	fixture := newFollowupChannel(t, app, "followup-single", scoutChatVisibilityPublic)
	scout := fixture.scout("I'd suggest opening with the pilot results.")
	fixture.human("tim@shareability.com", "Tim", "hm, I was leaning toward the market size slide honestly", &scout)
	decision := fixture.decide(followupResponder(t, "silent", "", nil))
	if decision.Verdict != scoutFollowupVerdictSilent || !strings.HasPrefix(decision.Reason, "model_silent:") {
		t.Fatalf("decision=%+v, want the model's silent verdict", decision)
	}
	fixture2 := newFollowupChannel(t, app, "followup-single-2", scoutChatVisibilityPublic)
	scout2 := fixture2.scout("I'd suggest opening with the pilot results.")
	reply := fixture2.human("tim@shareability.com", "Tim", "hm, I was leaning toward the market size slide honestly", &scout2)
	decision = fixture2.decide(followupResponder(t, "reply", "@Tim market size works too — want me to swap the opener?", nil))
	if decision.Verdict != scoutFollowupVerdictReply || !strings.HasPrefix(decision.Reason, "model_reply:") {
		t.Fatalf("decision=%+v, want the model's reply verdict", decision)
	}
	if posted := fixture2.scoutMessagesAfter(reply.ID); len(posted) != 1 {
		t.Fatalf("posted=%d, want one auto-reply", len(posted))
	}
	// Keyless the ambiguous case stays silent rather than guessing.
	fixture3 := newFollowupChannel(t, app, "followup-single-3", scoutChatVisibilityPublic)
	scout3 := fixture3.scout("I'd suggest opening with the pilot results.")
	fixture3.human("tim@shareability.com", "Tim", "hm, leaning market size", &scout3)
	thread := fixture3.reload()
	review := app.scoutFollowupReview(thread, "", time.Now().UTC())
	decision = app.scoutFollowupDecide(context.Background(), "", nil, review)
	if decision.Verdict != scoutFollowupVerdictSilent || !strings.HasPrefix(decision.Reason, "no_model:") {
		t.Fatalf("keyless decision=%+v", decision)
	}
}

// A brief answer that completes a waiting commission is an act: in a public
// channel that means the proposal card (the existing gate), threaded.
func TestScoutFollowupActsOnIntakeAnswer(t *testing.T) {
	app := setupScoutFollowupTestApp(t)
	fake := installFakeCommissionLauncher(t)
	swapOpenAITextResponder(t, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		t.Fatalf("keyless intake must not call the provider (workflow=%s)", request.Workflow)
		return "", nil
	})
	channel, _ := app.createScoutChatThread("aj@shareability.com", "AJ", "packaging", scoutChatVisibilityPublic)
	tim := accountStore().findUser("tim@shareability.com")
	response, err := app.appendScoutChatThreadMessage(context.Background(), tim, channel.ID, "@scout build a deck on our Q3 packaging results", nil, "")
	if err != nil {
		t.Fatalf("append ask: %v", err)
	}
	question := response["answer"].(scoutChatMessageRecord)
	fixture := &followupFixture{t: t, app: app, clock: time.Now().UTC()}
	fixture.thread, _, _ = app.scoutChatThreadByID("aj@shareability.com", channel.ID)
	// The asker answers top-level (no reply, no @scout): still theirs to answer.
	answer := fixture.human("tim@shareability.com", "Tim", "investors, hybrid", nil)
	decision := fixture.decide(func(context.Context, string, openAITextRequest) (string, error) {
		return "", errors.New("intake answers need no model")
	})
	if decision.Verdict != scoutFollowupVerdictAct || decision.Reason != "intake_answer" {
		t.Fatalf("decision=%+v, want act/intake_answer", decision)
	}
	posted := fixture.scoutMessagesAfter(answer.ID)
	if len(posted) != 1 || posted[0].Kind != scoutChatMessageKindProposal || posted[0].Proposal == nil || posted[0].Via != scoutFollowupVia || posted[0].ReplyTo == nil || posted[0].ReplyTo.MessageID != answer.ID {
		t.Fatalf("posted=%+v, want one threaded proposal card", posted)
	}
	if len(fake.calls) != 0 {
		t.Fatal("public channel act must go through the proposal gate, not the launcher")
	}
	records := app.packagingIntakeRecordsForThread(channel.ID)
	if len(records) != 1 || records[0].Status != packagingIntakeStatusProposed || records[0].QuestionMessageID != question.ID {
		t.Fatalf("records=%+v", records)
	}
	rows := followupJournalRows(app, channel.ID)
	if len(rows) != 1 || rows[0].Metadata["verdict"] != "act" || rows[0].Metadata["packagingIntake"] != records[0].ID {
		t.Fatalf("journal rows=%+v", rows)
	}
}

// The ambient pass: transcript rows drive the cursor, the sweep decides, the
// pass artifact stamps the consumed-through id, and a second pass re-bills
// nothing.
func TestScoutFollowupPassJournalsAndAdvancesCursor(t *testing.T) {
	app := setupScoutFollowupTestApp(t)
	fixture := newFollowupChannel(t, app, "followup-pass", scoutChatVisibilityPublic)
	scout := fixture.scout("Vendor comparison posted.")
	question := fixture.human("tim@shareability.com", "Tim", "does Zebra ship to the EU?", &scout)
	agent := scoutFollowupAgent()
	if agent.providerSeat != seatChat || agent.inputKind != meetingMemoryKindTranscript || agent.artifactKind != meetingMemoryKindScoutFollowupPass || agent.cursorMetadataKey != scoutFollowupCursorMetadataKey || agent.intervalEnv != "SCOUT_FOLLOWUP_INTERVAL" || agent.defaultInterval != 75*time.Second {
		t.Fatalf("agent contract=%+v", agent)
	}
	calls := 0
	responder := func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		calls++
		return `{"verdict":"reply","reason":"question","reply":"@Tim yes — EU shipping is in their standard terms."}`, nil
	}
	pass, err := app.runAmbientAgentOnce(agent, context.Background(), "test-key", responder, 1)
	if err != nil {
		t.Fatalf("pass: %v", err)
	}
	if pass.Kind != meetingMemoryKindScoutFollowupPass || pass.Metadata[scoutFollowupCursorMetadataKey] == "" {
		t.Fatalf("pass=%+v", pass)
	}
	if calls != 1 {
		t.Fatalf("model calls=%d, want 1", calls)
	}
	if posted := fixture.scoutMessagesAfter(question.ID); len(posted) != 1 || posted[0].Via != scoutFollowupVia {
		t.Fatalf("posted=%+v", posted)
	}
	rows := followupJournalRows(app, fixture.thread.ID)
	if len(rows) != 1 || rows[0].Metadata["verdict"] != "reply" {
		t.Fatalf("journal rows=%+v", rows)
	}
	cursors := app.scoutFollowupCursors()
	if cursors[fixture.thread.ID].MessageID != question.ID {
		t.Fatalf("cursors=%+v, want the thread reviewed through %s", cursors, question.ID)
	}
	// Drained window: nothing new, no spend, no duplicate reply.
	second, err := app.runAmbientAgentOnce(agent, context.Background(), "test-key", responder, 1)
	if err != nil || second.ID != "" || calls != 1 {
		t.Fatalf("second pass entry=%+v err=%v calls=%d, want a drained no-op", second, err, calls)
	}
	if posted := fixture.scoutMessagesAfter(question.ID); len(posted) != 1 {
		t.Fatalf("second pass re-posted: %d", len(posted))
	}
}

func TestScoutFollowupPausedByChatSeatBreaker(t *testing.T) {
	resetProviderBreakersForTest(t)
	resetCapabilityRuntimeForTest(t)
	app := setupScoutFollowupTestApp(t)
	fixture := newFollowupChannel(t, app, "followup-breaker", scoutChatVisibilityPublic)
	scout := fixture.scout("Vendor comparison posted.")
	fixture.human("tim@shareability.com", "Tim", "does Zebra ship to the EU?", &scout)
	providerBreakers.admit(providerOpenAI, seatChat)
	providerBreakers.recordPrimaryFailure(providerOpenAI, seatChat, providerFailureClassQuota, false)
	calls := 0
	responder := withProviderResilience(func(context.Context, string, openAITextRequest) (string, error) {
		calls++
		return "ok", nil
	})
	_, err := app.invokeAmbientAgentGuarded(scoutFollowupAgent(), context.Background(), "test-key", responder, 1, officeRoomID)
	var circuitErr *ambientAgentCircuitOpenError
	if !errors.As(err, &circuitErr) || !circuitErr.PausedByBreaker {
		t.Fatalf("err=%v, want paused-by-breaker", err)
	}
	if calls != 0 || len(followupJournalRows(app, fixture.thread.ID)) != 0 {
		t.Fatalf("paused worker still ran: calls=%d", calls)
	}
}

// A private-thread decision row quotes the thread; it carries the thread's
// recall fence so another member's recall never sees it.
func TestScoutFollowupJournalInheritsPrivateThreadFence(t *testing.T) {
	app := setupScoutFollowupTestApp(t)
	fixture := newFollowupChannel(t, app, "followup-private-journal", scoutChatVisibilityPrivate)
	scout := fixture.scout("Here is the confidential brief draft.")
	fixture.human("aj@shareability.com", "AJ", "can you keep the valuation line out of it?", &scout)
	fixture.decide(followupResponder(t, "reply", "@AJ done — valuation stays out.", nil))
	rows := followupJournalRows(app, fixture.thread.ID)
	if len(rows) != 1 || rows[0].Metadata["visibility"] != "private" || rows[0].Metadata["ownerEmail"] != "aj@shareability.com" || rows[0].Metadata["tenantId"] == "" {
		t.Fatalf("journal rows=%+v, want private + owner fence", rows)
	}
	owner := recallPrincipalForUser(accountStore().findUser("aj@shareability.com"))
	other := recallPrincipalForUser(accountStore().findUser("joel@shareability.com"))
	if !recallEntryScopeAllowed(rows[0].Metadata, owner) {
		t.Fatal("owner cannot recall their own private decision row")
	}
	if recallEntryScopeAllowed(rows[0].Metadata, other) {
		t.Fatal("another member can recall a private-thread decision row")
	}
	public := newFollowupChannel(t, app, "followup-public-journal", scoutChatVisibilityPublic)
	opener := public.scout("Vendor comparison posted.")
	public.human("tim@shareability.com", "Tim", "does Zebra ship to the EU?", &opener)
	public.decide(followupResponder(t, "reply", "@Tim yes.", nil))
	publicRows := followupJournalRows(app, public.thread.ID)
	if len(publicRows) != 1 || publicRows[0].Metadata["visibility"] != "organization" || !recallEntryScopeAllowed(publicRows[0].Metadata, other) {
		t.Fatalf("public journal rows=%+v, want organization-visible", publicRows)
	}
}

// An unrelated @scout ask elsewhere in the channel is addressed to Scout, but
// it answers nobody's questions: it must never complete (or launch) someone
// else's waiting commission.
func TestScoutFollowupUnrelatedAskNeverCompletesAnotherBrief(t *testing.T) {
	app := setupScoutFollowupTestApp(t)
	fake := installFakeCommissionLauncher(t)
	swapOpenAITextResponder(t, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		t.Fatalf("keyless intake must not call the provider (workflow=%s)", request.Workflow)
		return "", nil
	})
	channel, _ := app.createScoutChatThread("aj@shareability.com", "AJ", "packaging", scoutChatVisibilityPublic)
	tim := accountStore().findUser("tim@shareability.com")
	if _, err := app.appendScoutChatThreadMessage(context.Background(), tim, channel.ID, "@scout build a deck on our Q3 packaging results", nil, ""); err != nil {
		t.Fatalf("append ask: %v", err)
	}
	waiting, ok := app.pendingPackagingIntakeForThread(channel.ID)
	if !ok || len(waiting.OpenQuestions) != 2 {
		t.Fatalf("record=%+v, want audience + imagery open", waiting)
	}
	fixture := &followupFixture{t: t, app: app, clock: time.Now().UTC()}
	fixture.thread, _, _ = app.scoutChatThreadByID("aj@shareability.com", channel.ID)
	// Tyler's own chain, mentioning Scout, naming an option word ("full-bleed")
	// that belongs to Tim's open imagery question.
	card := fixture.scout("Marketing site preview is up.")
	fixture.human("tyler@shareability.com", "Tyler", "@scout can you make the hero image full-bleed on the marketing site?", &card)
	decision := fixture.decide(followupResponder(t, "reply", "@Tyler I'll take a look at the hero.", nil))
	if decision.Reason == "intake_answer" || decision.Reason == "intake_follow_up_question" {
		t.Fatalf("decision=%+v, Tyler's ask completed Tim's brief", decision)
	}
	after, stillWaiting := app.pendingPackagingIntakeForThread(channel.ID)
	if !stillWaiting || len(after.OpenQuestions) != 2 || len(after.Brief.Answers) != 0 || after.Brief.Imagery != "" {
		t.Fatalf("record=%+v, want Tim's brief untouched", after)
	}
	for _, message := range fixture.reload().Messages {
		if message.Kind == scoutChatMessageKindProposal {
			t.Fatalf("a proposal card was minted off an unrelated ask: %+v", message)
		}
	}
	if len(fake.calls) != 0 {
		t.Fatalf("launches=%+v", fake.calls)
	}
	// The asker's own untethered answer still completes it.
	fixture.human("tim@shareability.com", "Tim", "investors, hybrid", nil)
	decision = fixture.decide(func(context.Context, string, openAITextRequest) (string, error) {
		return "", errors.New("intake answers need no model")
	})
	if decision.Verdict != scoutFollowupVerdictAct || decision.Reason != "intake_answer" {
		t.Fatalf("decision=%+v, want act/intake_answer for the asker's answer", decision)
	}
	if records := app.packagingIntakeRecordsForThread(channel.ID); len(records) != 1 || records[0].Status != packagingIntakeStatusProposed {
		t.Fatalf("records=%+v", records)
	}
}

// A verdict that deferred (rate-limited, no model seat, failed post) decided
// nothing: the cursor stays put so the next pass still sees the question
// instead of losing it once the 10-minute window closes.
func TestScoutFollowupHoldsCursorWhenTheDecisionDefers(t *testing.T) {
	app := setupScoutFollowupTestApp(t)
	fixture := newFollowupChannel(t, app, "followup-cursor-hold", scoutChatVisibilityPrivate)
	scout := fixture.scout("Draft two is up.")
	first := fixture.human("aj@shareability.com", "AJ", "can you tighten slide 3?", &scout)
	fixture.post(scoutChatMessageRecord{Role: "scout", AuthorName: scoutParticipantName, Via: scoutFollowupVia, Text: "@AJ done — slide 3 is tighter.",
		CreatedAt: time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano), ReplyTo: &scoutChatReplyRef{MessageID: first.ID, AuthorName: "AJ"}})
	followup := fixture.reload().Messages[len(fixture.thread.Messages)-1]
	question := fixture.human("aj@shareability.com", "AJ", "and slide 4?", &followup)
	calls := 0
	responder := func(ctx context.Context, key string, request openAITextRequest) (string, error) {
		calls++
		return followupResponder(t, "reply", "@AJ slide 4 is next — give me a minute.", nil)(ctx, key, request)
	}
	if summary := app.runScoutFollowupSweep(context.Background(), "test-key", nil, responder); summary == "" {
		t.Fatal("sweep returned no summary")
	}
	if calls != 0 {
		t.Fatalf("rate-limited pass called the model %d times", calls)
	}
	if posted := fixture.scoutMessagesAfter(question.ID); len(posted) != 0 {
		t.Fatalf("rate-limited pass posted %d messages", len(posted))
	}
	if cursor := app.scoutFollowupCursors()[fixture.thread.ID]; cursor.MessageID == question.ID {
		t.Fatal("cursor advanced past a question the pass declined to answer")
	}
	// The unsolicited-reply window expires; the same question is still in view.
	thread := fixture.reload()
	for index := range thread.Messages {
		if thread.Messages[index].Via == scoutFollowupVia {
			thread.Messages[index].CreatedAt = time.Now().UTC().Add(-11 * time.Minute).Format(time.RFC3339Nano)
		}
	}
	if err := app.saveScoutChatThread(thread); err != nil {
		t.Fatalf("save thread: %v", err)
	}
	app.runScoutFollowupSweep(context.Background(), "test-key", nil, responder)
	if calls != 1 {
		t.Fatalf("model calls=%d, want the deferred question reconsidered once", calls)
	}
	posted := fixture.scoutMessagesAfter(question.ID)
	if len(posted) != 1 || posted[0].ReplyTo == nil || posted[0].ReplyTo.MessageID != question.ID {
		t.Fatalf("posted=%+v, want the answer to the held question", posted)
	}
	if cursor := app.scoutFollowupCursors()[fixture.thread.ID]; cursor.MessageID != question.ID {
		t.Fatalf("cursor=%+v, want it stamped once the turn was resolved", cursor)
	}
}

// A Private Riff is one person's space with its own synchronous engine: the
// watcher never decodes it, never ships its turns to the model, and never
// writes into it.
func TestScoutFollowupNeverTouchesARiff(t *testing.T) {
	app := setupScoutFollowupTestApp(t)
	fixture := newFollowupChannel(t, app, "followup-riff", scoutChatVisibilityPrivate)
	riff := fixture.reload()
	riff.Riff = &privateRiffBinding{Version: privateRiffBindingVersion, SourceThreadID: "source-thread", SourceTitle: "#packaging",
		AgentID: agentMindScoutID, AgentName: scoutParticipantName, SourceAvailable: true}
	if err := app.saveScoutChatThread(riff); err != nil {
		t.Fatalf("save riff: %v", err)
	}
	fixture.thread = riff
	scout := fixture.scout("Here is where that thread could go.")
	question := fixture.human("aj@shareability.com", "AJ", "what about the pricing angle?", &scout)
	if threads := app.scoutFollowupCandidateThreads(nil, time.Now().UTC()); len(threads) != 0 {
		t.Fatalf("candidate threads=%d, want the Riff excluded", len(threads))
	}
	calls := 0
	summary := app.runScoutFollowupSweep(context.Background(), "test-key", nil, func(context.Context, string, openAITextRequest) (string, error) {
		calls++
		return "", errors.New("a Riff must never reach the model")
	})
	if calls != 0 || !strings.Contains(summary, "nothing addressed to Scout") {
		t.Fatalf("sweep calls=%d summary=%q", calls, summary)
	}
	if posted := fixture.scoutMessagesAfter(question.ID); len(posted) != 0 {
		t.Fatalf("the watcher wrote %d messages into a Riff", len(posted))
	}
	if rows := followupJournalRows(app, fixture.thread.ID); len(rows) != 0 {
		t.Fatalf("a Riff was journaled: %+v", rows)
	}
	if _, err := app.postScoutFollowupReply(fixture.reload(), question, "unsolicited", true); err == nil {
		t.Fatal("posting an unsolicited reply into a Riff must be refused")
	}
}

// Two commissions waiting in one channel: the requester's untethered answer to
// the OLDER one is still Scout's to act on. Deciding "addressed" from the
// newest pending record alone never even considered it — the pass then stamped
// the cursor past the message, so the answer was lost and the older commission
// waited forever.
func TestScoutFollowupUntetheredAnswerToTheOlderIntakeIsNotDropped(t *testing.T) {
	app := setupScoutFollowupTestApp(t)
	installFakeCommissionLauncher(t)
	swapOpenAITextResponder(t, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		t.Fatalf("keyless intake must not call the provider (workflow=%s)", request.Workflow)
		return "", nil
	})
	channel, _ := app.createScoutChatThread("aj@shareability.com", "AJ", "packaging", scoutChatVisibilityPublic)
	tim := accountStore().findUser("tim@shareability.com")
	tyler := accountStore().findUser("tyler@shareability.com")
	_, first, _ := waitingIntakeAsk(t, app, tim, channel.ID, "@scout build a deck on our Q3 packaging results")
	_, second, _ := waitingIntakeAsk(t, app, tyler, channel.ID, "@scout put together a study of competitor pricing")
	if first.ID == second.ID || second.CreatedAt < first.CreatedAt {
		t.Fatalf("expected two records, newest second: %s / %s", first.ID, second.ID)
	}
	fixture := &followupFixture{t: t, app: app, clock: time.Now().UTC()}
	fixture.thread, _, _ = app.scoutChatThreadByID("aj@shareability.com", channel.ID)
	// Tim answers his own (now older) intake untethered: no @scout, no reply.
	answer := fixture.human("tim@shareability.com", "Tim", "investors, hybrid", nil)
	review := app.scoutFollowupReview(fixture.reload(), "", time.Now().UTC())
	if len(review.addressed) != 1 || review.addressed[0].ID != answer.ID {
		t.Fatalf("addressed=%+v, want Tim's untethered answer", review.addressed)
	}
	decision := app.scoutFollowupDecide(context.Background(), "test-key", func(context.Context, string, openAITextRequest) (string, error) {
		return "", errors.New("intake answers need no model")
	}, review)
	if decision.Verdict != scoutFollowupVerdictAct || decision.Reason != "intake_answer" {
		t.Fatalf("decision=%+v, want act/intake_answer on the older intake", decision)
	}
	records := map[string]packagingIntakeRecord{}
	for _, record := range app.packagingIntakeRecordsForThread(channel.ID) {
		records[record.ID] = record
	}
	if records[first.ID].Status != packagingIntakeStatusProposed || records[first.ID].Brief.Audience != "investors" || records[first.ID].Brief.Imagery != "hybrid" {
		t.Fatalf("first record=%+v, want it completed by Tim's answer", records[first.ID])
	}
	if records[second.ID].Status != packagingIntakeStatusWaiting || len(records[second.ID].Brief.Answers) != 0 {
		t.Fatalf("second record=%+v, want Tyler's brief untouched", records[second.ID])
	}
	// The sweep must not stamp the cursor past an answer it acted on without
	// journaling it, and someone else's ask still never counts as addressed.
	if rows := followupJournalRows(app, channel.ID); len(rows) != 0 {
		t.Fatalf("decide() must not journal on its own: %+v", rows)
	}
}

// One window, two addressed messages: the pass binds the first to a waiting
// intake, acts and RETURNS there — it never looked at the second. Stamping the
// cursor to the newest fresh message then dropped that second message forever
// (a direct question, a change to the brief), the same invariant the deferral
// branch already keeps. The cursor must stop at the message the pass actually
// disposed of.
func TestScoutFollowupCursorStopsAtTheMessageItDisposedOf(t *testing.T) {
	app := setupScoutFollowupTestApp(t)
	installFakeCommissionLauncher(t)
	swapOpenAITextResponder(t, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		t.Fatalf("keyless intake must not call the provider (workflow=%s)", request.Workflow)
		return "", nil
	})
	channel, _ := app.createScoutChatThread("aj@shareability.com", "AJ", "packaging", scoutChatVisibilityPublic)
	tim := accountStore().findUser("tim@shareability.com")
	if _, err := app.appendScoutChatThreadMessage(context.Background(), tim, channel.ID, "@scout build a deck on our Q3 packaging results", nil, ""); err != nil {
		t.Fatalf("append ask: %v", err)
	}
	fixture := &followupFixture{t: t, app: app, clock: time.Now().UTC()}
	fixture.thread, _, _ = app.scoutChatThreadByID("aj@shareability.com", channel.ID)
	// Both land inside ONE 75s window: the untethered answer, then a question.
	answer := fixture.human("tim@shareability.com", "Tim", "investors, hybrid", nil)
	question := fixture.human("tim@shareability.com", "Tim", "@scout also, when does the Q3 data land?", nil)
	calls := 0
	responder := func(ctx context.Context, key string, request openAITextRequest) (string, error) {
		calls++
		return followupResponder(t, "reply", "@Tim the Q3 data lands Friday.", nil)(ctx, key, request)
	}
	// A channel reaches the sweep through its transcript rows.
	inputs := []meetingMemoryEntry{{Metadata: map[string]string{"threadId": channel.ID}}}
	if summary := app.runScoutFollowupSweep(context.Background(), "test-key", inputs, responder); summary == "" {
		t.Fatal("sweep returned no summary")
	}
	records := app.packagingIntakeRecordsForThread(channel.ID)
	if len(records) != 1 || records[0].Status != packagingIntakeStatusProposed {
		t.Fatalf("records=%+v, want the intake completed by Tim's answer", records)
	}
	cursor := app.scoutFollowupCursors()[channel.ID]
	if cursor.MessageID != answer.ID {
		t.Fatalf("cursor=%+v, want it stamped to the answer the pass acted on (%s), never past the question %s", cursor, answer.ID, question.ID)
	}
	// The question is still in view for the next pass, and still Scout's. The
	// proposal card is threaded under the ANSWER, so it is not the question's
	// answer either — counting it as one stamped past the question anyway.
	review := app.scoutFollowupReview(fixture.reload(), cursor.MessageID, time.Now().UTC())
	if len(review.addressed) != 1 || review.addressed[0].ID != question.ID || review.answered {
		t.Fatalf("second-pass addressed=%+v answered=%v, want the question the first pass never decided", review.addressed, review.answered)
	}
	// The next pass answers it.
	app.runScoutFollowupSweep(context.Background(), "test-key", inputs, responder)
	if calls != 1 {
		t.Fatalf("model calls=%d, want the held question answered once", calls)
	}
	answers := 0
	for _, posted := range fixture.scoutMessagesAfter(answer.ID) {
		if posted.ReplyTo != nil && posted.ReplyTo.MessageID == question.ID {
			answers++
		}
	}
	if answers != 1 {
		t.Fatalf("replies threaded under the question=%d, want exactly one", answers)
	}
	if after := app.scoutFollowupCursors()[channel.ID]; after.MessageID != question.ID {
		t.Fatalf("cursor=%+v, want it stamped once the question was resolved", after)
	}
}
