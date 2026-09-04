package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// fakeCommissionLauncher stands in for packaging_commissions.go's
// createPackagingCommission until it lands.
type fakeCommissionLauncher struct {
	calls []struct {
		principal string
		kind      string
		brief     packagingIntakeBrief
	}
}

func (fake *fakeCommissionLauncher) createPackagingCommission(principal *userAccount, kind string, brief packagingIntakeBrief) (packagingCommissionReceipt, error) {
	fake.calls = append(fake.calls, struct {
		principal string
		kind      string
		brief     packagingIntakeBrief
	}{normalizeAccountEmail(principal.Email), kind, brief})
	id := "commission-" + strings.TrimPrefix(brief.MessageID, "scout-chat-message-")
	return packagingCommissionReceipt{
		CommissionID: id, ArtifactID: "", Label: "presentation queued in the Packaging Studio",
		Thread: &scoutChatThreadRef{ID: id, Mode: "goal", ProcessID: packagingStudioProcessID, Query: brief.Ask, Status: "queued", OutputFamily: "Presentation"},
	}, nil
}

func installFakeCommissionLauncher(t *testing.T) *fakeCommissionLauncher {
	t.Helper()
	fake := &fakeCommissionLauncher{}
	previous := packagingCommissionLauncherFactory
	packagingCommissionLauncherFactory = func(*kanbanBoardApp) commissionLauncher { return fake }
	t.Cleanup(func() { packagingCommissionLauncherFactory = previous })
	return fake
}

// setupPackagingIntakeTestApp wires the fake launcher: the intake is dark
// until packaging_commissions.go wires the real one (or PACKAGING_CHAT_INTAKE
// is set), so every test that expects interception installs it.
func setupPackagingIntakeTestApp(t *testing.T) *kanbanBoardApp {
	t.Helper()
	setupAuthTestEnv(t)
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("PACKAGING_CHAT_INTAKE", "")
	installFakeCommissionLauncher(t)
	previousApp := kanbanApp
	app := newIsolatedKanbanBoardApp(t)
	kanbanApp = app
	t.Cleanup(func() { kanbanApp = previousApp })
	swapOpenAITextResponder(t, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		t.Fatalf("keyless intake must not call the provider (workflow=%s)", request.Workflow)
		return "", nil
	})
	return app
}

func TestPackagingIntakeDetectKinds(t *testing.T) {
	cases := []struct {
		text string
		kind string
		ok   bool
	}{
		{"@scout make me a pitch deck about our Q3 numbers for investors", packagingIntakeKindPresentation, true},
		// English puts the head noun last: the deliverable is the deck, even
		// though the compound's modifier ("market analysis") reads research and
		// sits at the edge of the verb window.
		{"@scout put together a market analysis deck for the board", packagingIntakeKindPresentation, true},
		{"@scout build a launch narrative deck for the team", packagingIntakeKindPresentation, true},
		// The window still holds where the noun is what the work is FOR.
		{"pull comps for the rodeo doc so we can price it", "", false},
		{"can you research the packaging market?", packagingIntakeKindResearch, true},
		{"@scout write a memo on the vendor decision", packagingIntakeKindDocument, true},
		{"@scout workshop the story for the Series A narrative", packagingIntakeKindStory, true},
		{"@scout build a deck or memo on the pilot", "", true},
		{"how's the deck coming?", "", false},
		{"the research looks good", "", false},
		{"@scout don't build a deck yet", "", false},
		{"@scout what did we decide yesterday?", "", false},
		{"thanks @scout", "", false},
	}
	for _, tc := range cases {
		kind, ok := packagingIntakeDetect(tc.text, nil, nil)
		if kind != tc.kind || ok != tc.ok {
			t.Errorf("packagingIntakeDetect(%q)=(%q,%v), want (%q,%v)", tc.text, kind, ok, tc.kind, tc.ok)
		}
	}
}

// A public work ask with gaps becomes a waiting commission and ONE threaded
// reply on the asking message, addressed to the asker — never a top-level
// post and never a proposal card yet.
func TestPackagingIntakeAskCreatesWaitingCommissionWithOneThreadedQuestion(t *testing.T) {
	app := setupPackagingIntakeTestApp(t)
	channel, err := app.createScoutChatThread("aj@shareability.com", "AJ", "packaging", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	tim := accountStore().findUser("tim@shareability.com")
	if tim == nil {
		t.Fatal("seed user tim missing")
	}
	response, err := app.appendScoutChatThreadMessage(context.Background(), tim, channel.ID, "@scout build a deck on our Q3 packaging results", nil, "")
	if err != nil {
		t.Fatalf("append ask: %v", err)
	}
	if response["intentOutcome"] != string(conversationIntentClarifyOnce) {
		t.Fatalf("intentOutcome=%v keys=%v, want clarify_once", response["intentOutcome"], responseKeys(response))
	}
	if _, proposed := response["proposal"]; proposed {
		t.Fatal("a gapped ask must not mint a proposal card yet")
	}
	ask := response["message"].(scoutChatMessageRecord)
	question, ok := response["answer"].(scoutChatMessageRecord)
	if !ok {
		t.Fatalf("answer=%#v", response["answer"])
	}
	if question.ReplyTo == nil || question.ReplyTo.MessageID != ask.ID {
		t.Fatalf("question replyTo=%+v, want a threaded reply on the ask %s", question.ReplyTo, ask.ID)
	}
	if !strings.HasPrefix(question.Text, "@Tim") {
		t.Fatalf("question must address the asker by mention: %q", question.Text)
	}
	if question.Clarifying == nil || len(question.Clarifying.Questions) == 0 || len(question.Clarifying.Questions) > packagingIntakeMaxQuestions {
		t.Fatalf("clarifying=%+v, want 1..3 questions", question.Clarifying)
	}
	ids := make([]string, 0, len(question.Clarifying.Questions))
	for _, item := range question.Clarifying.Questions {
		ids = append(ids, item.ID)
	}
	if strings.Join(ids, ",") != "audience,imagery" {
		t.Fatalf("question ids=%v, want audience,imagery (the only output-changing gaps)", ids)
	}
	for _, item := range question.Clarifying.Questions {
		if item.ID == "imagery" && (item.Kind != "choice" || len(item.Options) != 3) {
			t.Fatalf("imagery question must carry quick-answer options: %+v", item)
		}
	}
	if !strings.Contains(question.Text, "1. ") || !strings.Contains(question.Text, "2. ") || strings.Contains(question.Text, "3. ") {
		t.Fatalf("question text must number exactly two questions:\n%s", question.Text)
	}
	if question.Via != packagingIntakeVia || question.Role != "scout" {
		t.Fatalf("question via=%q role=%q", question.Via, question.Role)
	}
	record, waiting := app.pendingPackagingIntakeForThread(channel.ID)
	if !waiting || record.Status != packagingIntakeStatusWaiting || record.WaitingOn != "tim@shareability.com" || record.WaitingOnName != "Tim" {
		t.Fatalf("record=%+v, want waiting_on tim", record)
	}
	if record.Kind != packagingIntakeKindPresentation || record.QuestionMessageID != question.ID || record.AskMessageID != ask.ID {
		t.Fatalf("record binding=%+v", record)
	}
	if record.Classifier != "deterministic" {
		t.Fatalf("keyless classifier=%q, want deterministic", record.Classifier)
	}
	saved, _, err := app.scoutChatThreadByID("aj@shareability.com", channel.ID)
	if err != nil {
		t.Fatalf("reload channel: %v", err)
	}
	if len(saved.Messages) != 2 {
		t.Fatalf("messages=%d, want the ask + one threaded question", len(saved.Messages))
	}
	rows := app.packagingIntakeSnapshotForThread(channel.ID)
	if len(rows) != 1 || rows[0]["waitingOnName"] != "Tim" || rows[0]["briefComplete"] != false {
		t.Fatalf("snapshot=%v", rows)
	}
}

// Private thread: answers in the thread complete the brief and launch through
// the commission launcher without the user leaving chat.
func TestPackagingIntakeAnswersCompleteBriefAndLaunch(t *testing.T) {
	app := setupPackagingIntakeTestApp(t)
	fake := installFakeCommissionLauncher(t)
	aj := accountStore().findUser("aj@shareability.com")
	private, err := app.createScoutChatThread("aj@shareability.com", "AJ", "Scout", "")
	if err != nil {
		t.Fatalf("create private thread: %v", err)
	}
	response, err := app.appendScoutChatThreadMessage(context.Background(), aj, private.ID, "make me a deck on our packaging pilot", nil, "")
	if err != nil {
		t.Fatalf("append ask: %v", err)
	}
	question := response["answer"].(scoutChatMessageRecord)
	if question.Clarifying == nil || len(fake.calls) != 0 {
		t.Fatalf("gapped private ask: clarifying=%v launches=%d", question.Clarifying, len(fake.calls))
	}
	response, err = app.appendScoutChatThreadMessageWithReplyAndTool(context.Background(), aj, private.ID, "1. investors\n2. hybrid", nil, "", question.ID, "")
	if err != nil {
		t.Fatalf("append answers: %v", err)
	}
	if response["intentOutcome"] != string(conversationIntentStartPrivateWork) {
		t.Fatalf("intentOutcome=%v keys=%v, want start_private_work", response["intentOutcome"], responseKeys(response))
	}
	if len(fake.calls) != 1 {
		t.Fatalf("launcher calls=%d, want 1", len(fake.calls))
	}
	call := fake.calls[0]
	if call.principal != "aj@shareability.com" || call.kind != packagingIntakeKindPresentation {
		t.Fatalf("launch principal=%s kind=%s", call.principal, call.kind)
	}
	if call.brief.Audience != "investors" || call.brief.Imagery != "hybrid" || call.brief.Ask != "make me a deck on our packaging pilot" {
		t.Fatalf("brief=%+v", call.brief)
	}
	if call.brief.Answers["audience"] != "investors" || call.brief.Answers["imagery"] != "hybrid" {
		t.Fatalf("answers=%v", call.brief.Answers)
	}
	card := response["answer"].(scoutChatMessageRecord)
	if card.Kind != "thread" || card.Thread == nil || card.Thread.ID != "commission-"+strings.TrimPrefix(question.ID, "scout-chat-message-") && !strings.HasPrefix(card.Thread.ID, "commission-") {
		t.Fatalf("launch card=%+v", card)
	}
	if card.ReplyTo == nil {
		t.Fatal("launch card must stay in the thread (replyTo)")
	}
	records := app.packagingIntakeRecordsForThread(private.ID)
	if len(records) != 1 || records[0].Status != packagingIntakeStatusLaunched || !strings.HasPrefix(records[0].CommissionID, "commission-") {
		t.Fatalf("records=%+v", records)
	}
	if _, waiting := app.pendingPackagingIntakeForThread(private.ID); waiting {
		t.Fatal("no intake should still be waiting")
	}
	objective := renderPackagingIntakeObjective(call.brief)
	if !strings.Contains(objective, "Audience: investors") || !strings.Contains(objective, "Imagery mode: hybrid") || strings.Contains(objective, "Depth:") {
		t.Fatalf("objective:\n%s", objective)
	}
}

// Structured pill answers (D13) ride the request beside the reply text.
func TestPackagingIntakeStructuredAnswersFromPills(t *testing.T) {
	app := setupPackagingIntakeTestApp(t)
	fake := installFakeCommissionLauncher(t)
	channel, _ := app.createScoutChatThread("aj@shareability.com", "AJ", "packaging", scoutChatVisibilityPublic)
	tim := accountStore().findUser("tim@shareability.com")
	response, err := app.appendScoutChatThreadMessage(context.Background(), tim, channel.ID, "@scout put together a research brief on compostable mailers", nil, "")
	if err != nil {
		t.Fatalf("append ask: %v", err)
	}
	question := response["answer"].(scoutChatMessageRecord)
	if question.Clarifying == nil {
		t.Fatalf("expected clarifying questions, got %+v", question)
	}
	record, _ := app.pendingPackagingIntakeForThread(channel.ID)
	if record.Kind != packagingIntakeKindResearch {
		t.Fatalf("kind=%q, want research", record.Kind)
	}
	ctx := withPackagingIntakeAnswers(context.Background(), &scoutChatClarifyingAnswers{
		CommissionID: record.ID,
		Answers:      []scoutChatClarifyingAnswer{{ID: "audience", Value: "the board"}, {ID: "depth", Value: "deep"}},
	})
	response, err = app.appendScoutChatThreadMessageWithReplyAndTool(ctx, tim, channel.ID, "answered via pills", nil, "", question.ID, "")
	if err != nil {
		t.Fatalf("append pill answers: %v", err)
	}
	proposalMessage, ok := response["answer"].(scoutChatMessageRecord)
	if !ok || proposalMessage.Proposal == nil || !strings.Contains(proposalMessage.Proposal.Objective, "Audience: the board") || !strings.Contains(proposalMessage.Proposal.Objective, "Depth: deep") {
		t.Fatalf("pill answers must complete the brief into the public proposal: %#v", response["answer"])
	}
	if len(fake.calls) != 0 {
		t.Fatalf("public brief must not launch directly: %+v", fake.calls)
	}
}

// Routine facts the studio infers (slide count, decision, brand assets…) are
// never asked; a fully specified ask has no gaps at all.
func TestPackagingIntakeNoRoutineQuestions(t *testing.T) {
	aj := &userAccount{Email: "aj@shareability.com", Name: "AJ"}
	thread := scoutChatThreadRecord{ID: "intake-thread", Visibility: scoutChatVisibilityPrivate, OwnerEmail: aj.Email}
	deck := scoutChatMessageRecord{ID: "m1", Kind: "message", Role: "user", AuthorEmail: aj.Email, AuthorName: "AJ",
		Text: "@scout build a 10-slide deck for investors with full-bleed imagery based on the Q3 report"}
	brief := packagingIntakeDeterministicBrief(packagingIntakeKindPresentation, thread, deck, nil, nil, aj)
	if brief.Length != "10 slides" || brief.Audience != "investors" || brief.Imagery != "full-bleed" || brief.SourceMode != "named" {
		t.Fatalf("deck brief=%+v", brief)
	}
	if gaps := briefGaps(packagingIntakeKindPresentation, brief); len(gaps) != 0 {
		t.Fatalf("fully specified deck asked %v", gaps)
	}
	// A deck with audience + imagery but no slide count: the studio infers
	// slide_count, so length is never a question.
	partial := brief
	partial.Length = ""
	if gaps := briefGaps(packagingIntakeKindPresentation, partial); len(gaps) != 0 {
		t.Fatalf("deck without slide count asked %v (routine question)", gaps)
	}
	research := scoutChatMessageRecord{ID: "m2", Kind: "message", Role: "user", AuthorEmail: aj.Email, AuthorName: "AJ",
		Text: "@scout research the EU packaging regulations, deep dive, for the board"}
	researchBrief := packagingIntakeDeterministicBrief(packagingIntakeKindResearch, thread, research, nil, nil, aj)
	if researchBrief.Depth != "deep" || researchBrief.Audience != "board" {
		t.Fatalf("research brief=%+v", researchBrief)
	}
	if gaps := briefGaps(packagingIntakeKindResearch, researchBrief); len(gaps) != 0 {
		t.Fatalf("fully specified research asked %v", gaps)
	}
	// Kind ambiguity is the ONLY question until the kind is known.
	if gaps := briefGaps("", packagingIntakeBrief{}); len(gaps) != 1 || gaps[0].ID != "kind" || gaps[0].Kind != "choice" {
		t.Fatalf("ambiguous kind gaps=%v", gaps)
	}
	// Never more than three, and the cap keeps priority order.
	bare := packagingIntakeBrief{SourceMode: ""}
	gaps := briefGaps(packagingIntakeKindResearch, bare)
	if len(gaps) != 3 || gaps[0].ID != "audience" || gaps[1].ID != "depth" || gaps[2].ID != "sources" {
		t.Fatalf("bare research gaps=%v", gaps)
	}
	// "you decide" closes every open question by inference, never a re-ask.
	record := packagingIntakeRecord{Kind: packagingIntakeKindResearch, Brief: bare, OpenQuestions: gaps, AskedQuestionIDs: []string{"audience", "depth", "sources"}}
	if closed := applyBriefAnswers(&record, nil, "you decide"); closed != 3 || len(record.OpenQuestions) != 0 {
		t.Fatalf("deferral closed=%d open=%v", closed, record.OpenQuestions)
	}
	if record.Brief.SourceMode != "infer" {
		t.Fatalf("deferred sources must fall back to the studio's own inference: %+v", record.Brief)
	}
}

// Public channels: a completed brief mints the existing proposal card (the
// accept route stays the single launch door); the launcher is never called
// directly. Others may answer only once they @scout.
func TestPackagingIntakePublicGateHonoured(t *testing.T) {
	app := setupPackagingIntakeTestApp(t)
	fake := installFakeCommissionLauncher(t)
	channel, _ := app.createScoutChatThread("aj@shareability.com", "AJ", "packaging", scoutChatVisibilityPublic)
	tim := accountStore().findUser("tim@shareability.com")
	tyler := accountStore().findUser("tyler@shareability.com")
	response, err := app.appendScoutChatThreadMessage(context.Background(), tim, channel.ID, "@scout build a deck on our Q3 packaging results", nil, "")
	if err != nil {
		t.Fatalf("append ask: %v", err)
	}
	question := response["answer"].(scoutChatMessageRecord)

	// A teammate answering without @scout is not the asker: the brief stays
	// open and nothing is proposed.
	swapOpenAITextResponder(t, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		return "channel answer.", nil
	})
	if _, err := app.appendScoutChatThreadMessageWithReplyAndTool(context.Background(), tyler, channel.ID, "investors, hybrid", nil, "", question.ID, ""); err != nil {
		t.Fatalf("append bystander reply: %v", err)
	}
	if record, waiting := app.pendingPackagingIntakeForThread(channel.ID); !waiting || record.Status != packagingIntakeStatusWaiting {
		t.Fatalf("bystander answer must not complete the brief: %+v", record)
	}
	// The same teammate @scouts the answer: authorized, brief complete, and
	// the launch step is the public proposal gate.
	response, err = app.appendScoutChatThreadMessageWithReplyAndTool(context.Background(), tyler, channel.ID, "@scout for investors, hybrid imagery", nil, "", question.ID, "")
	if err != nil {
		t.Fatalf("append tagged answer: %v", err)
	}
	if response["intentOutcome"] != string(conversationIntentApprovalRequired) || response["approvalRequired"] != true {
		t.Fatalf("intentOutcome=%v keys=%v, want approval_required", response["intentOutcome"], responseKeys(response))
	}
	proposalMessage := response["answer"].(scoutChatMessageRecord)
	if proposalMessage.Kind != scoutChatMessageKindProposal || proposalMessage.Proposal == nil || proposalMessage.ReplyTo == nil {
		t.Fatalf("proposal message=%+v", proposalMessage)
	}
	// The deck commission is a tool_run bound to the Packaging Studio process
	// (was: a generic "design" workstream, which the accept route launched as a
	// plain research agent thread — never the studio the asker commissioned).
	if proposalMessage.Proposal.Kind != scoutRouterProposalKindToolRun || proposalMessage.Proposal.ToolID != packagingStudioProcessID || proposalMessage.Proposal.EffectClass != "expanded_audience" {
		t.Fatalf("proposal=%+v", proposalMessage.Proposal)
	}
	if !strings.Contains(proposalMessage.Proposal.Objective, "Audience: investors") || !strings.Contains(proposalMessage.Proposal.Objective, "Imagery mode: hybrid") {
		t.Fatalf("proposal objective:\n%s", proposalMessage.Proposal.Objective)
	}
	if len(fake.calls) != 0 {
		t.Fatalf("public brief must not launch directly: %+v", fake.calls)
	}
	records := app.packagingIntakeRecordsForThread(channel.ID)
	if len(records) != 1 || records[0].Status != packagingIntakeStatusProposed || records[0].ProposalMessageID != proposalMessage.ID {
		t.Fatalf("records=%+v", records)
	}
}

// Pre-D9 the intake is dark: with no launcher wired every ask — gapped or
// complete — stays on today's router path. Wired, a complete private ask
// launches immediately; ties and guard-owned asks still fall through.
func TestPackagingIntakeDarkUntilWiredAndYieldsToExistingGuards(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("PACKAGING_CHAT_INTAKE", "")
	previousFactory := packagingCommissionLauncherFactory
	packagingCommissionLauncherFactory = nil
	t.Cleanup(func() { packagingCommissionLauncherFactory = previousFactory })
	previousApp := kanbanApp
	app := newIsolatedKanbanBoardApp(t)
	kanbanApp = app
	t.Cleanup(func() { kanbanApp = previousApp })
	aj := accountStore().findUser("aj@shareability.com")
	private, _ := app.createScoutChatThread("aj@shareability.com", "AJ", "Scout", "")
	thread, _, _ := app.scoutChatThreadByID(aj.Email, private.ID)
	message := func(id, text string) scoutChatMessageRecord {
		return scoutChatMessageRecord{ID: id, Kind: "message", Role: "user", AuthorEmail: aj.Email, AuthorName: "AJ", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), Text: text}
	}
	commits := 0
	commit := func(messages ...scoutChatMessageRecord) (scoutChatThreadRecord, error) {
		commits++
		return app.commitScoutChatThreadMessages(aj.Email, private.ID, messages...)
	}
	complete := message("ask-complete", "build a 12-slide deck for investors with hybrid imagery on the pilot")
	gapped := message("ask-gapped", "build a deck on the pilot")
	for _, ask := range []scoutChatMessageRecord{complete, gapped} {
		if _, handled := app.packagingIntakeTurn(context.Background(), aj, thread, ask, nil, nil, packagingIntakeGate{}, commit); handled || commits != 0 {
			t.Fatalf("unwired ask %q handled=%v commits=%d, want fall-through", ask.Text, handled, commits)
		}
	}
	fake := installFakeCommissionLauncher(t)
	response, handled := app.packagingIntakeTurn(context.Background(), aj, thread, complete, nil, nil, packagingIntakeGate{}, commit)
	if !handled || len(fake.calls) != 1 || response["intentOutcome"] != string(conversationIntentStartPrivateWork) {
		t.Fatalf("wired complete ask handled=%v calls=%d response=%v", handled, len(fake.calls), responseKeys(response))
	}
	if fake.calls[0].brief.Length != "12 slides" || fake.calls[0].brief.Audience != "investors" {
		t.Fatalf("brief=%+v", fake.calls[0].brief)
	}
	// Guard-owned asks are never intercepted even when wired.
	channel, _ := app.createScoutChatThread("aj@shareability.com", "AJ", "packaging", scoutChatVisibilityPublic)
	public, _, _ := app.scoutChatThreadByID(aj.Email, channel.ID)
	for _, text := range []string{
		"@scout build a deck or memo on the pilot",                                     // two-kind tie → router
		"@scout research: the rodeo creator market",                                    // channel prefix mode → deterministic guard
		"@scout research: please create an Insights & Opportunities report for launch", // STRIDE I&O guard
		"we need to work on the deck",                                                  // no action verb: a discussion, the router's choices card
		"pull comps for the rodeo doc so we can price it",                              // the doc is what the comps are FOR
		"take the outline we already have and build the deck from it",                  // settled by existing material: direct Studio route
	} {
		if _, handled := app.packagingIntakeTurn(context.Background(), aj, public, message("ask-"+sha256Hex([]byte(text))[:8], text), nil, nil, packagingIntakeGate{}, commit); handled {
			t.Fatalf("guard-owned ask intercepted: %q", text)
		}
	}
	// Private research stays on the router; a reply-sourced public ask keeps
	// the router's exact source binding.
	if _, handled := app.packagingIntakeTurn(context.Background(), aj, thread, message("ask-research", "run the expensive rodeo creator market study"), nil, nil, packagingIntakeGate{}, commit); handled {
		t.Fatal("private research ask must stay on the router")
	}
	sourced := message("ask-sourced", "@scout put the source into a 10-slide presentation for this channel")
	sourced.ReplyTo = &scoutChatReplyRef{MessageID: "root-1", AuthorName: "Tyler", Text: "the pdf"}
	if _, handled := app.packagingIntakeTurn(context.Background(), aj, public, sourced, nil, nil, packagingIntakeGate{}, commit); handled {
		t.Fatal("reply-sourced public ask must stay on the router")
	}
	if len(fake.calls) != 1 {
		t.Fatalf("launcher calls=%d, want the single wired launch", len(fake.calls))
	}
}

// Two concurrent answer triggers (the append path and the watcher, or two
// tabs) launch exactly one commission: the per-intake lock spans read →
// launch → status write and the loser sees the terminal status.
func TestPackagingIntakeConcurrentAnswersLaunchOnce(t *testing.T) {
	app := setupPackagingIntakeTestApp(t)
	fake := installFakeCommissionLauncher(t)
	aj := accountStore().findUser("aj@shareability.com")
	private, _ := app.createScoutChatThread("aj@shareability.com", "AJ", "Scout", "")
	response, err := app.appendScoutChatThreadMessage(context.Background(), aj, private.ID, "make me a deck on our packaging pilot", nil, "")
	if err != nil {
		t.Fatalf("append ask: %v", err)
	}
	question := response["answer"].(scoutChatMessageRecord)
	thread, _, _ := app.scoutChatThreadByID(aj.Email, private.ID)
	record, waiting := app.pendingPackagingIntakeForThread(private.ID)
	if !waiting {
		t.Fatal("no waiting record")
	}
	answer := scoutChatMessageRecord{ID: "answer-1", Kind: "message", Role: "user", AuthorEmail: aj.Email, AuthorName: "AJ", Text: "1. investors\n2. hybrid",
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), ReplyTo: &scoutChatReplyRef{MessageID: question.ID, AuthorName: scoutParticipantName}}
	commit := func(messages ...scoutChatMessageRecord) (scoutChatThreadRecord, error) {
		scoutOnly := messages[:0]
		for _, message := range messages {
			if message.Role != "user" {
				scoutOnly = append(scoutOnly, message)
			}
		}
		if len(scoutOnly) == 0 {
			return thread, nil
		}
		return app.commitScoutChatThreadMessages(aj.Email, private.ID, scoutOnly...)
	}
	handled := make(chan bool, 2)
	for i := 0; i < 2; i++ {
		go func() {
			copyRecord := record
			applyBriefAnswers(&copyRecord, nil, answer.Text)
			_, ok := app.packagingIntakeContinue(context.Background(), aj, thread, answer, &copyRecord, commit)
			handled <- ok
		}()
	}
	wins := 0
	for i := 0; i < 2; i++ {
		if <-handled {
			wins++
		}
	}
	if wins != 1 || len(fake.calls) != 1 {
		t.Fatalf("handled=%d launches=%d, want exactly one", wins, len(fake.calls))
	}
	if records := app.packagingIntakeRecordsForThread(private.ID); len(records) != 1 || records[0].Status != packagingIntakeStatusLaunched {
		t.Fatalf("records=%+v", records)
	}
	// A later go-ahead through the watcher's door is a no-op too.
	launch := app.packagingIntakeRecordsForThread(private.ID)[0]
	if _, ok := app.packagingIntakeLaunch(context.Background(), thread, answer, aj, &launch, commit); ok || len(fake.calls) != 1 {
		t.Fatalf("relaunch handled=%v launches=%d, want no-op", ok, len(fake.calls))
	}
}

// PACKAGING_CHAT_INTAKE is a real operator switch: off disables intake even
// when the launcher is wired; on enables it unwired.
func TestPackagingChatIntakeKillSwitch(t *testing.T) {
	previous := packagingCommissionLauncherFactory
	t.Cleanup(func() { packagingCommissionLauncherFactory = previous })
	packagingCommissionLauncherFactory = func(*kanbanBoardApp) commissionLauncher { return &fakeCommissionLauncher{} }
	t.Setenv("PACKAGING_CHAT_INTAKE", "")
	if !packagingChatIntakeEnabled() {
		t.Fatal("wired intake must default ON")
	}
	for _, off := range []string{"0", "off", "false", "disabled"} {
		t.Setenv("PACKAGING_CHAT_INTAKE", off)
		if packagingChatIntakeEnabled() {
			t.Fatalf("PACKAGING_CHAT_INTAKE=%s must disable intake even when wired", off)
		}
	}
	packagingCommissionLauncherFactory = nil
	t.Setenv("PACKAGING_CHAT_INTAKE", "")
	if packagingChatIntakeEnabled() {
		t.Fatal("unwired intake must default OFF")
	}
	t.Setenv("PACKAGING_CHAT_INTAKE", "1")
	if !packagingChatIntakeEnabled() {
		t.Fatal("PACKAGING_CHAT_INTAKE=1 must enable intake unwired")
	}
}

// waitingIntakeFixture posts an ask into a thread and returns the thread as
// stored plus the waiting record and Scout's clarifying question.
func waitingIntakeAsk(t *testing.T, app *kanbanBoardApp, user *userAccount, threadID string, ask string) (scoutChatThreadRecord, packagingIntakeRecord, scoutChatMessageRecord) {
	t.Helper()
	response, err := app.appendScoutChatThreadMessage(context.Background(), user, threadID, ask, nil, "")
	if err != nil {
		t.Fatalf("append ask %q: %v", ask, err)
	}
	question, ok := response["answer"].(scoutChatMessageRecord)
	if !ok || question.Clarifying == nil {
		t.Fatalf("ask %q did not open an intake: %#v", ask, response["answer"])
	}
	record, waiting := app.pendingPackagingIntakeForThread(threadID)
	if !waiting {
		t.Fatalf("ask %q left no waiting record", ask)
	}
	thread, _, err := app.scoutChatThreadByID(user.Email, threadID)
	if err != nil {
		t.Fatalf("reload thread: %v", err)
	}
	return thread, record, question
}

func intakeUserMessage(id string, user *userAccount, text string) scoutChatMessageRecord {
	return scoutChatMessageRecord{ID: id, Kind: "message", Role: "user", AuthorEmail: user.Email, AuthorName: scoutChatAuthorName(user),
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), Text: text}
}

// A question back to Scout is not an answer. "is there anything else you need
// from me?" used to match the bare deferral word "anything", close every open
// question by inference and launch a real commission off a question.
func TestPackagingIntakeQuestionBackNeverClosesTheBrief(t *testing.T) {
	app := setupPackagingIntakeTestApp(t)
	fake := installFakeCommissionLauncher(t)
	aj := accountStore().findUser("aj@shareability.com")
	private, _ := app.createScoutChatThread("aj@shareability.com", "AJ", "Scout", "")
	thread, record, _ := waitingIntakeAsk(t, app, aj, private.ID, "@scout write a memo on the vendor decision, pull in the numbers")
	if len(record.OpenQuestions) != 3 {
		t.Fatalf("open questions=%+v, want audience/length/sources", record.OpenQuestions)
	}
	commits := 0
	commit := func(messages ...scoutChatMessageRecord) (scoutChatThreadRecord, error) {
		commits++
		return thread, nil
	}
	back := intakeUserMessage("question-back", aj, "is there anything else you need from me?")
	if _, handled := app.packagingIntakeTurn(context.Background(), aj, thread, back, nil, nil, packagingIntakeGate{}, commit); handled || commits != 0 {
		t.Fatalf("a question back was taken as an answer (handled=%v commits=%d)", handled, commits)
	}
	if len(fake.calls) != 0 {
		t.Fatalf("a question back launched work: %+v", fake.calls)
	}
	after, waiting := app.pendingPackagingIntakeForThread(private.ID)
	if !waiting || len(after.OpenQuestions) != 3 || len(after.Brief.Answers) != 0 {
		t.Fatalf("record after the question back=%+v, want all three questions still open", after)
	}
	// A real deferral still closes everything by inference.
	closed := applyBriefAnswers(&after, nil, "you decide")
	if closed != 3 || len(after.OpenQuestions) != 0 {
		t.Fatalf("deferral closed=%d open=%+v", closed, after.OpenQuestions)
	}
}

// A top-level message from the asker while Scout waits is only an answer when
// it reads as one: an aside ("let me check with Tim first") and a brand-new
// work ask are both handed back, never folded into the open brief.
func TestPackagingIntakeTopLevelAsideAndFreshAskAreNotAnswers(t *testing.T) {
	app := setupPackagingIntakeTestApp(t)
	fake := installFakeCommissionLauncher(t)
	aj := accountStore().findUser("aj@shareability.com")
	private, _ := app.createScoutChatThread("aj@shareability.com", "AJ", "Scout", "")
	thread, record, _ := waitingIntakeAsk(t, app, aj, private.ID, "make me a deck on our packaging pilot with hybrid imagery")
	if len(record.OpenQuestions) != 1 || record.OpenQuestions[0].ID != "audience" || record.OpenQuestions[0].Kind != "text" {
		t.Fatalf("open questions=%+v, want the single open audience question", record.OpenQuestions)
	}
	commits := 0
	commit := func(messages ...scoutChatMessageRecord) (scoutChatThreadRecord, error) {
		commits++
		return thread, nil
	}
	for _, tc := range []struct {
		text      string
		handsBack bool // an aside belongs to the ordinary path; a fresh ask is its own turn
	}{
		{"let me check with Tim first", true},
		{"actually, write me a research report on competitor pricing", false},
	} {
		_, handled := app.packagingIntakeTurn(context.Background(), aj, thread, intakeUserMessage("m-"+sha256Hex([]byte(tc.text))[:8], aj, tc.text), nil, nil, packagingIntakeGate{}, commit)
		if tc.handsBack && handled {
			t.Fatalf("%q was taken as the audience answer", tc.text)
		}
		after, ok := app.packagingIntakeRecordByID(record.ID)
		if !ok || !after.pending() || len(after.OpenQuestions) != 1 || after.Brief.Audience != "" || len(after.Brief.Answers) != 0 {
			t.Fatalf("after %q record=%+v, want the deck brief untouched and still waiting", tc.text, after)
		}
	}
	if len(fake.calls) != 0 || commits != 0 {
		t.Fatalf("an aside launched the commission: calls=%+v commits=%d", fake.calls, commits)
	}
	// The genuine answer still lands and launches on the real audience.
	answer := intakeUserMessage("answer-audience", aj, "the exec team")
	if _, handled := app.packagingIntakeTurn(context.Background(), aj, thread, answer, nil, nil, packagingIntakeGate{}, commit); !handled {
		t.Fatal("a plain top-level answer must still complete the brief")
	}
	if len(fake.calls) != 1 || fake.calls[0].brief.Audience != "the exec team" {
		t.Fatalf("launch calls=%+v", fake.calls)
	}
}

// An ordinary acknowledgement is courtesy, never the last open answer. With
// one text question left the take-whole branch stored "thanks!" as the sources
// answer, marked the brief complete and launched a real commission off it —
// and a waiting record never expires, so an ancient intake swallowed any later
// ack from its requester.
func TestPackagingIntakeAcknowledgementIsNeverTheLastAnswer(t *testing.T) {
	app := setupPackagingIntakeTestApp(t)
	fake := installFakeCommissionLauncher(t)
	aj := accountStore().findUser("aj@shareability.com")
	private, _ := app.createScoutChatThread("aj@shareability.com", "AJ", "Scout", "")
	_, record, question := waitingIntakeAsk(t, app, aj, private.ID, "@scout write a memo on the vendor decision, pull in the numbers")
	if len(record.OpenQuestions) != 3 {
		t.Fatalf("open questions=%+v, want audience/length/sources", record.OpenQuestions)
	}
	// Two of the three land; only the sources TEXT question is left, which is
	// exactly the state the greedy branch reads whole.
	if _, err := app.appendScoutChatThreadMessageWithReplyAndTool(context.Background(), aj, private.ID, "1. the exec team\n2. short", nil, "", question.ID, ""); err != nil {
		t.Fatalf("append numbered answers: %v", err)
	}
	waiting, ok := app.pendingPackagingIntakeForThread(private.ID)
	if !ok || len(waiting.OpenQuestions) != 1 || waiting.OpenQuestions[0].ID != "sources" || waiting.OpenQuestions[0].Kind != "text" {
		t.Fatalf("record after the numbered answers=%+v, want only the sources text question open", waiting)
	}
	thread, _, err := app.scoutChatThreadByID(aj.Email, private.ID)
	if err != nil {
		t.Fatalf("reload thread: %v", err)
	}
	commits := 0
	commit := func(messages ...scoutChatMessageRecord) (scoutChatThreadRecord, error) {
		commits++
		return thread, nil
	}
	for _, ack := range []string{"thanks!", "thanks scout", "ok cool", "👍", "sounds good", "will do", "nice work", "great, thanks", "sure thing", "great call"} {
		message := intakeUserMessage("ack-"+sha256Hex([]byte(ack))[:8], aj, ack)
		if _, handled := app.packagingIntakeTurn(context.Background(), aj, thread, message, nil, nil, packagingIntakeGate{}, commit); handled {
			t.Fatalf("%q was taken as the sources answer", ack)
		}
		if closed := applyBriefAnswers(&waiting, nil, ack); closed != 0 {
			t.Fatalf("applyBriefAnswers(%q) closed %d question(s)", ack, closed)
		}
		if packagingIntakeReadsAsAnswer(ack) {
			t.Fatalf("packagingIntakeReadsAsAnswer(%q)=true, want an acknowledgement fenced", ack)
		}
		after, still := app.pendingPackagingIntakeForThread(private.ID)
		if !still || len(after.OpenQuestions) != 1 || len(after.Brief.Sources) != 0 || after.Brief.SourceMode == "named" {
			t.Fatalf("after %q record=%+v, want the brief untouched and still waiting", ack, after)
		}
	}
	if len(fake.calls) != 0 || commits != 0 {
		t.Fatalf("an acknowledgement launched the commission: calls=%+v commits=%d", fake.calls, commits)
	}
	// A real sources answer that merely OPENS politely still lands whole: the
	// fence is anchored on the whole message, never on its first word.
	answer := intakeUserMessage("answer-sources", aj, "thanks — pull from the vendor scorecard in Drive")
	if _, handled := app.packagingIntakeTurn(context.Background(), aj, thread, answer, nil, nil, packagingIntakeGate{}, commit); !handled {
		t.Fatal("a genuine sources answer must still complete the brief")
	}
	completed, ok := app.packagingIntakeRecordByID(waiting.ID)
	if !ok || completed.pending() || len(completed.Brief.Sources) != 1 || !strings.Contains(completed.Brief.Sources[0], "vendor scorecard") {
		t.Fatalf("record after the real answer=%+v, want the sources answer stored and the brief no longer waiting", completed)
	}
}

// Option words are matched token-anchored and never inside a comparative
// idiom: "as long as it covers the Q3 numbers" is not a length choice.
func TestPackagingIntakeOptionMatchingIsWordAnchored(t *testing.T) {
	cases := []struct {
		option string
		reply  string
		want   bool
	}{
		{"long", "for the exec team — as long as it covers the Q3 numbers", false},
		{"long", "how long do you need it", false},
		{"long", "make it long", true},
		{"short", "keep it short", true},
		{"brief", "we can do a quick debriefing after", false},
		{"deep", "he cares deeply about this one", false},
		{"deep", "go deep", true},
		{"full-bleed", "full bleed please", true},
		{"full-bleed", "full-bleed", true},
		// Loose spellings are per-question aliases now, never a bare first
		// word: see TestPackagingIntakeFunctionWordsNeverChooseAnOption.
		{"research report", "research", false},
		{"research report", "research report", true},
	}
	for _, tc := range cases {
		if got := packagingIntakeOptionMatches(tc.option, strings.ToLower(tc.reply)); got != tc.want {
			t.Errorf("packagingIntakeOptionMatches(%q, %q)=%v, want %v", tc.option, tc.reply, got, tc.want)
		}
	}
	// End to end: the audience answer lands, the length question stays open —
	// a one-page memo request must not silently become a long document.
	record := packagingIntakeRecord{
		Kind: packagingIntakeKindDocument,
		OpenQuestions: []packagingIntakeQuestion{
			packagingIntakeQuestionCatalog["audience"], packagingIntakeQuestionCatalog["length"],
		},
		AskedQuestionIDs: []string{"audience", "length"},
		Brief:            packagingIntakeBrief{Kind: packagingIntakeKindDocument, SourceMode: "infer"},
	}
	if closed := applyBriefAnswers(&record, nil, "for the exec team — as long as it covers the Q3 numbers"); closed != 1 {
		t.Fatalf("closed=%d, want only the audience question", closed)
	}
	if record.Brief.Length != "" || len(record.OpenQuestions) != 1 || record.OpenQuestions[0].ID != "length" {
		t.Fatalf("record=%+v, want the length question still open", record)
	}
}

// Two commissions in one channel: an answer completes the intake it is bound
// to, not merely the newest one, so the first is never stranded.
func TestPackagingIntakeSecondCommissionDoesNotStrandTheFirst(t *testing.T) {
	app := setupPackagingIntakeTestApp(t)
	installFakeCommissionLauncher(t)
	channel, _ := app.createScoutChatThread("aj@shareability.com", "AJ", "packaging", scoutChatVisibilityPublic)
	tim := accountStore().findUser("tim@shareability.com")
	tyler := accountStore().findUser("tyler@shareability.com")
	_, first, firstQuestion := waitingIntakeAsk(t, app, tim, channel.ID, "@scout build a deck on our Q3 packaging results")
	_, second, _ := waitingIntakeAsk(t, app, tyler, channel.ID, "@scout put together a study of competitor pricing")
	if first.ID == second.ID || second.CreatedAt < first.CreatedAt {
		t.Fatalf("expected two records, newest second: %s / %s", first.ID, second.ID)
	}
	response, err := app.appendScoutChatThreadMessageWithReplyAndTool(context.Background(), tim, channel.ID, "investors, hybrid", nil, "", firstQuestion.ID, "")
	if err != nil {
		t.Fatalf("append answer: %v", err)
	}
	if response["packagingIntake"] != first.ID {
		t.Fatalf("answer landed on %v, want the record it replied to (%s)", response["packagingIntake"], first.ID)
	}
	records := map[string]packagingIntakeRecord{}
	for _, record := range app.packagingIntakeRecordsForThread(channel.ID) {
		records[record.ID] = record
	}
	if records[first.ID].Status != packagingIntakeStatusProposed {
		t.Fatalf("first record=%+v, want proposed", records[first.ID])
	}
	if records[second.ID].Status != packagingIntakeStatusWaiting || len(records[second.ID].Brief.Answers) != 0 {
		t.Fatalf("second record=%+v, want untouched and still waiting", records[second.ID])
	}
}

// Every ask of one intake is its own message: a follow-up that re-asks a
// SUBSET of the same questions must not reuse the first question's id (the
// thread appends without id dedupe, so a collision hides one of them).
func TestPackagingIntakeFollowUpQuestionGetsItsOwnMessageID(t *testing.T) {
	app := setupPackagingIntakeTestApp(t)
	installFakeCommissionLauncher(t)
	aj := accountStore().findUser("aj@shareability.com")
	private, _ := app.createScoutChatThread("aj@shareability.com", "AJ", "Scout", "")
	_, record, first := waitingIntakeAsk(t, app, aj, private.ID, "@scout write a memo on the vendor decision, pull in the numbers")
	if len(record.OpenQuestions) != 3 {
		t.Fatalf("open questions=%+v", record.OpenQuestions)
	}
	response, err := app.appendScoutChatThreadMessageWithReplyAndTool(context.Background(), aj, private.ID, "short", nil, "", first.ID, "")
	if err != nil {
		t.Fatalf("append partial answer: %v", err)
	}
	second, ok := response["answer"].(scoutChatMessageRecord)
	if !ok || second.Clarifying == nil {
		t.Fatalf("partial answer must re-ask what is left: %#v", response["answer"])
	}
	if second.ID == first.ID {
		t.Fatalf("follow-up question reused the first question's id %s", first.ID)
	}
	saved, _, err := app.scoutChatThreadByID(aj.Email, private.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	seen := map[string]int{}
	for _, message := range saved.Messages {
		seen[message.ID]++
	}
	if seen[first.ID] != 1 || seen[second.ID] != 1 {
		t.Fatalf("message ids in thread=%v, want both questions exactly once", seen)
	}
	if scoutChatMessageIndex(saved, second.ID) == scoutChatMessageIndex(saved, first.ID) {
		t.Fatalf("both questions resolve to one message index")
	}
	updated, waiting := app.pendingPackagingIntakeForThread(private.ID)
	if !waiting || updated.QuestionMessageID != second.ID || updated.AskRound != 2 {
		t.Fatalf("record=%+v, want the second ask recorded", updated)
	}
}

// A completed public brief proposes the SAME process the private launcher
// would run: the accept route launches the studio, not a generic research
// agent thread.
func TestPackagingIntakePublicProposalRoutesToItsStudioProcess(t *testing.T) {
	cases := []struct {
		kind     string
		wantKind conversationWorkKind
		wantTool string
		wantMode string
	}{
		{packagingIntakeKindPresentation, conversationWorkRegistryTool, packagingStudioProcessID, ""},
		{packagingIntakeKindDocument, conversationWorkRegistryTool, documentReportProcessID, ""},
		{packagingIntakeKindStory, conversationWorkRegistryTool, documentReportProcessID, ""},
		{packagingIntakeKindResearch, conversationWorkWorkstream, "", "research"},
	}
	for _, tc := range cases {
		record := packagingIntakeRecord{Kind: tc.kind, Brief: packagingIntakeBrief{Kind: tc.kind, Ask: "package the pilot", Audience: "investors"}}
		proposal := packagingIntakeProposal(&record)
		if proposal == nil || proposal.IntentOutcome != string(conversationIntentApprovalRequired) || proposal.EffectClass != "expanded_audience" {
			t.Fatalf("%s proposal=%+v", tc.kind, proposal)
		}
		work, err := conversationWorkFromScoutProposal(proposal)
		if err != nil {
			t.Fatalf("%s proposal does not validate as public work: %v", tc.kind, err)
		}
		if work.Kind != tc.wantKind || work.ToolID != tc.wantTool || (tc.wantMode != "" && work.Mode != tc.wantMode) {
			t.Fatalf("%s work=%+v, want kind=%s tool=%s mode=%s", tc.kind, work, tc.wantKind, tc.wantTool, tc.wantMode)
		}
		if tc.wantTool != "" {
			if _, ok := processByID(work.ToolID); !ok {
				t.Fatalf("%s routes to unknown process %q", tc.kind, work.ToolID)
			}
			if strings.TrimSpace(work.Authority) == "" {
				t.Fatalf("%s tool run lost its process authority: %+v", tc.kind, work)
			}
		}
	}
}

// Two answers landing at once both survive: the per-intake lock spans read →
// apply → write, so the second answer is applied to the first's saved brief
// instead of overwriting it from a stale copy.
func TestPackagingIntakeConcurrentPartialAnswersBothLand(t *testing.T) {
	app := setupPackagingIntakeTestApp(t)
	fake := installFakeCommissionLauncher(t)
	aj := accountStore().findUser("aj@shareability.com")
	private, _ := app.createScoutChatThread("aj@shareability.com", "AJ", "Scout", "")
	thread, record, question := waitingIntakeAsk(t, app, aj, private.ID, "make me a deck on our packaging pilot")
	if len(record.OpenQuestions) != 2 {
		t.Fatalf("open questions=%+v, want audience + imagery", record.OpenQuestions)
	}
	commit := func(messages ...scoutChatMessageRecord) (scoutChatThreadRecord, error) {
		scoutOnly := make([]scoutChatMessageRecord, 0, len(messages))
		for _, message := range messages {
			if message.Role != "user" {
				scoutOnly = append(scoutOnly, message)
			}
		}
		if len(scoutOnly) == 0 {
			return thread, nil
		}
		return app.commitScoutChatThreadMessages(aj.Email, private.ID, scoutOnly...)
	}
	answer := func(id, text string) scoutChatMessageRecord {
		message := intakeUserMessage(id, aj, text)
		message.ReplyTo = &scoutChatReplyRef{MessageID: question.ID, AuthorName: scoutParticipantName}
		return message
	}
	done := make(chan bool, 2)
	for _, message := range []scoutChatMessageRecord{answer("answer-audience", "for investors"), answer("answer-imagery", "hybrid")} {
		go func(message scoutChatMessageRecord) {
			copyRecord := record
			_, ok := app.packagingIntakeAnswerTurn(context.Background(), aj, thread, message, &copyRecord, nil, commit)
			done <- ok
		}(message)
	}
	handled := 0
	for i := 0; i < 2; i++ {
		if <-done {
			handled++
		}
	}
	if handled != 2 {
		t.Fatalf("handled=%d, want both answers taken", handled)
	}
	if len(fake.calls) != 1 {
		t.Fatalf("launches=%d, want exactly one", len(fake.calls))
	}
	brief := fake.calls[0].brief
	if brief.Audience != "investors" || brief.Imagery != "hybrid" {
		t.Fatalf("brief=%+v, want both concurrent answers", brief)
	}
	records := app.packagingIntakeRecordsForThread(private.ID)
	if len(records) != 1 || records[0].Status != packagingIntakeStatusLaunched {
		t.Fatalf("records=%+v", records)
	}
}

// The intake row quotes the asking thread verbatim, so it inherits that
// thread's recall fence instead of a flat "organization".
func TestPackagingIntakeRowInheritsThreadRecallFence(t *testing.T) {
	app := setupPackagingIntakeTestApp(t)
	installFakeCommissionLauncher(t)
	aj := accountStore().findUser("aj@shareability.com")
	private, _ := app.createScoutChatThread("aj@shareability.com", "AJ", "Scout", "")
	_, record, _ := waitingIntakeAsk(t, app, aj, private.ID, "make me a deck on our packaging pilot")
	entry, ok := app.memory.entryByKindAndID(meetingMemoryKindPackagingIntake, record.ID)
	if !ok {
		t.Fatal("intake row missing")
	}
	if entry.Metadata["visibility"] != "private" || entry.Metadata["ownerEmail"] != "aj@shareability.com" || entry.Metadata["tenantId"] == "" {
		t.Fatalf("private intake row metadata=%v, want the private fence", entry.Metadata)
	}
	owner := recallPrincipalForUser(accountStore().findUser("aj@shareability.com"))
	other := recallPrincipalForUser(accountStore().findUser("joel@shareability.com"))
	if !recallEntryScopeAllowed(entry.Metadata, owner) {
		t.Fatal("the asker cannot recall their own intake row")
	}
	if recallEntryScopeAllowed(entry.Metadata, other) {
		t.Fatal("another member can recall a private thread's intake row")
	}
	channel, _ := app.createScoutChatThread("aj@shareability.com", "AJ", "packaging", scoutChatVisibilityPublic)
	tim := accountStore().findUser("tim@shareability.com")
	_, channelRecord, _ := waitingIntakeAsk(t, app, tim, channel.ID, "@scout build a deck on our Q3 packaging results")
	channelEntry, ok := app.memory.entryByKindAndID(meetingMemoryKindPackagingIntake, channelRecord.ID)
	if !ok || channelEntry.Metadata["visibility"] != "organization" || !recallEntryScopeAllowed(channelEntry.Metadata, other) {
		t.Fatalf("office channel intake row=%v, want organization-visible", channelEntry.Metadata)
	}
}

// Function words never choose an option. Three catalog options begin with one
// — "on-slide", "one page", "full-bleed" — so a loose "first word of a
// multi-word option" fallback let ordinary prose ("base it on the transcript",
// "one of the partners asked") set an imagery mode or a length the asker never
// chose, and the phantom close then suppressed the take-whole branch that
// would have stored the answer they DID give.
func TestPackagingIntakeFunctionWordsNeverChooseAnOption(t *testing.T) {
	for _, tc := range []struct{ option, reply string }{
		{"on-slide", "base it on the transcript Ana posted"},
		{"on-slide", "the board — lead on the pilot outcome"},
		{"on-slide", "whatever works on the day"},
		{"on-slide", "is there anything else you need on this?"},
		{"one page", "one of the partners asked for it"},
		{"full-bleed", "drive folder, based on our full pipeline export"},
	} {
		if packagingIntakeOptionMatches(tc.option, strings.ToLower(tc.reply)) {
			t.Errorf("%q was read as a choice of %q", tc.reply, tc.option)
		}
	}
	// Only the kind question carries loose spellings, and they are matched the
	// same word-anchored way as the option itself.
	kind := packagingIntakeQuestionCatalog["kind"]
	for _, tc := range []struct {
		option string
		reply  string
		want   bool
	}{
		{"research report", "research", true},
		{"research report", "a study of competitor pricing", true},
		{"presentation", "a deck please", true},
		{"presentation", "slides", true},
		{"document", "a memo", true},
		{"story outline", "outline", true},
		{"document", "on the deck", false},
		{"story outline", "one of the partners asked", false},
	} {
		if got := packagingIntakeQuestionOptionMatches(kind, tc.option, strings.ToLower(tc.reply)); got != tc.want {
			t.Errorf("kind option %q vs %q = %v, want %v", tc.option, tc.reply, got, tc.want)
		}
	}
	// The length question keeps whole spellings of its one multi-word option.
	lengthQuestion := packagingIntakeQuestionCatalog["length"]
	for _, tc := range []struct {
		reply string
		want  bool
	}{
		{"one pager please", true},
		{"a single page", true},
		{"one of the partners asked for it", false},
		{"one", false},
	} {
		if got := packagingIntakeQuestionOptionMatches(lengthQuestion, "one page", strings.ToLower(tc.reply)); got != tc.want {
			t.Errorf("length option \"one page\" vs %q = %v, want %v", tc.reply, got, tc.want)
		}
	}
	// End to end on the real catalog: a sources answer that happens to contain
	// "on" leaves the imagery question open instead of inventing a mode.
	record := packagingIntakeRecord{
		Kind: packagingIntakeKindPresentation,
		OpenQuestions: []packagingIntakeQuestion{
			packagingIntakeQuestionCatalog["imagery"], packagingIntakeQuestionCatalog["sources"],
		},
		AskedQuestionIDs: []string{"imagery", "sources"},
		Brief:            packagingIntakeBrief{Kind: packagingIntakeKindPresentation, Audience: "the board"},
	}
	if closed := applyBriefAnswers(&record, nil, "base it on the transcript Ana posted"); closed != 0 {
		t.Fatalf("closed=%d, want nothing closed by prose containing \"on\"", closed)
	}
	if record.Brief.Imagery != "" || len(record.OpenQuestions) != 2 {
		t.Fatalf("record=%+v, want imagery unchosen and both questions open", record)
	}
	// With the imagery question already settled, the same reply is taken whole
	// as the sources answer — the close it used to steal.
	settled := packagingIntakeRecord{
		Kind:             packagingIntakeKindPresentation,
		OpenQuestions:    []packagingIntakeQuestion{packagingIntakeQuestionCatalog["sources"]},
		AskedQuestionIDs: []string{"imagery", "sources"},
		Brief: packagingIntakeBrief{Kind: packagingIntakeKindPresentation, Audience: "the board", Imagery: "hybrid",
			Answers: map[string]string{"imagery": "hybrid"}},
	}
	if closed := applyBriefAnswers(&settled, nil, "base it on the transcript Ana posted"); closed != 1 {
		t.Fatalf("closed=%d, want the sources question closed", closed)
	}
	if len(settled.Brief.Sources) != 1 || settled.Brief.SourceMode != "named" || len(settled.OpenQuestions) != 0 {
		t.Fatalf("record=%+v, want the transcript stored as the source", settled)
	}
	// "one of the partners asked for it" is not a one-page document.
	length := packagingIntakeRecord{
		Kind:             packagingIntakeKindDocument,
		OpenQuestions:    []packagingIntakeQuestion{packagingIntakeQuestionCatalog["length"]},
		AskedQuestionIDs: []string{"length"},
		Brief:            packagingIntakeBrief{Kind: packagingIntakeKindDocument, Audience: "the board", SourceMode: "infer"},
	}
	if closed := applyBriefAnswers(&length, nil, "one of the partners asked for it"); closed != 0 || length.Brief.Length != "" {
		t.Fatalf("closed=%d length=%q, want the length question still open", closed, length.Brief.Length)
	}
}

// The founder's case, pinned with the router CONFIGURED. Every other
// append-path intake test runs keyless, where "intake owned the turn" is
// indistinguishable from "no provider, so nothing else could have". With a key
// the deterministic studio route (scoutChatDeckRequestDetected) would launch
// this ask immediately, so this test is what proves the seam still sits AHEAD
// of that route for a plain, under-specified imperative deck ask — asks its two
// output-changing questions as ONE threaded reply, and launches on the answers.
func TestPackagingIntakeStillOwnsOrdinaryPrivateDeckAskWithRouterConfigured(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("PACKAGING_CHAT_INTAKE", "")
	fake := installFakeCommissionLauncher(t)
	previousApp := kanbanApp
	app := newIsolatedKanbanBoardApp(t)
	kanbanApp = app
	t.Cleanup(func() { kanbanApp = previousApp })
	app.apiKey = "openai-packaging-intake-router-configured"
	swapOpenAITextResponder(t, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		if request.Workflow != packagingIntakeWorkflow {
			t.Fatalf("intake-owned ask reached workflow %q; the seam yielded to the router", request.Workflow)
		}
		// The bounded pre-fill is the intake's own call. Degrade it so the
		// deterministic brief (and therefore the exact gap set) is what ships.
		return "", fmt.Errorf("classifier unavailable")
	})
	aj := accountStore().findUser("aj@shareability.com")
	private, err := app.createScoutChatThread("aj@shareability.com", "AJ", "Scout", "")
	if err != nil {
		t.Fatalf("create private thread: %v", err)
	}
	response, err := app.appendScoutChatThreadMessage(context.Background(), aj, private.ID, "make me a deck on our packaging pilot", nil, "")
	if err != nil {
		t.Fatalf("append ask: %v", err)
	}
	if response["intentOutcome"] != string(conversationIntentClarifyOnce) {
		t.Fatalf("intentOutcome=%v keys=%v, want clarify_once", response["intentOutcome"], responseKeys(response))
	}
	ask := response["message"].(scoutChatMessageRecord)
	question, ok := response["answer"].(scoutChatMessageRecord)
	if !ok || question.Clarifying == nil || question.Via != packagingIntakeVia {
		t.Fatalf("answer=%#v, want one threaded intake question", response["answer"])
	}
	if question.ReplyTo == nil || question.ReplyTo.MessageID != ask.ID {
		t.Fatalf("question replyTo=%+v, want a threaded reply on the ask %s", question.ReplyTo, ask.ID)
	}
	ids := make([]string, 0, len(question.Clarifying.Questions))
	for _, item := range question.Clarifying.Questions {
		ids = append(ids, item.ID)
	}
	if strings.Join(ids, ",") != "audience,imagery" || len(fake.calls) != 0 {
		t.Fatalf("question ids=%v launches=%d, want the two output-changing gaps and no launch yet", ids, len(fake.calls))
	}
	record, waiting := app.pendingPackagingIntakeForThread(private.ID)
	if !waiting || record.Status != packagingIntakeStatusWaiting || record.AskMessageID != ask.ID {
		t.Fatalf("record=%+v, want a waiting commission bound to the ask", record)
	}
	response, err = app.appendScoutChatThreadMessageWithReplyAndTool(context.Background(), aj, private.ID, "1. investors\n2. hybrid", nil, "", question.ID, "")
	if err != nil {
		t.Fatalf("append answers: %v", err)
	}
	if response["intentOutcome"] != string(conversationIntentStartPrivateWork) || len(fake.calls) != 1 {
		t.Fatalf("intentOutcome=%v launches=%d, want the commission to launch on the answers", response["intentOutcome"], len(fake.calls))
	}
	if fake.calls[0].brief.Audience != "investors" || fake.calls[0].brief.Imagery != "hybrid" {
		t.Fatalf("brief=%+v", fake.calls[0].brief)
	}
}

// The four yields that make intake a fallback rather than a pre-empt. Each one
// is a route the ordinary router had before this seam existed: a hired agent's
// own direct thread, a realtime voice turn, a turn whose sources are already
// bound, and an ask the deterministic guards already own.
func TestPackagingIntakeYieldsEveryRouteTheRouterAlreadyOwns(t *testing.T) {
	app := setupPackagingIntakeTestApp(t)
	fake := installFakeCommissionLauncher(t)
	aj := accountStore().findUser("aj@shareability.com")
	private, err := app.createScoutChatThread("aj@shareability.com", "AJ", "Scout", "")
	if err != nil {
		t.Fatalf("create private thread: %v", err)
	}
	thread, _, err := app.scoutChatThreadByID(aj.Email, private.ID)
	if err != nil {
		t.Fatal(err)
	}
	commits := 0
	commit := func(messages ...scoutChatMessageRecord) (scoutChatThreadRecord, error) {
		commits++
		return app.commitScoutChatThreadMessages(aj.Email, private.ID, messages...)
	}
	ask := func(text string) scoutChatMessageRecord {
		return intakeUserMessage("yield-"+sha256Hex([]byte(text))[:10], aj, text)
	}
	deck := ask("make me a deck on our packaging pilot")
	cases := []struct {
		name    string
		ctx     context.Context
		message scoutChatMessageRecord
		gate    packagingIntakeGate
	}{
		{name: "hired agent direct thread", ctx: context.Background(), message: deck, gate: packagingIntakeGate{AgentDirectThread: true}},
		{name: "targeted agent work", ctx: context.Background(), message: deck, gate: packagingIntakeGate{TargetedAgentWork: true}},
		{
			name:    "realtime voice turn",
			ctx:     withConversationTurnModality(context.Background(), conversationModalityPrivateRealtimeVoice),
			message: ask("build a six-slide presentation for the western creator opportunity"),
		},
		{name: "bound source context", ctx: context.Background(), message: ask("analyze today's meetings and prepare a report"), gate: packagingIntakeGate{SourceBound: true}},
		{name: "question to Scout", ctx: context.Background(), message: ask("Can you make a 10 slide deck pitching me this platform?")},
		{name: "conversation-owned goal route", ctx: context.Background(), message: ask("Create an investor one-pager for Aurora")},
	}
	for _, tc := range cases {
		if _, handled := app.packagingIntakeTurn(tc.ctx, aj, thread, tc.message, nil, nil, tc.gate, commit); handled {
			t.Fatalf("%s was intercepted by the intake", tc.name)
		}
	}
	if commits != 0 || len(fake.calls) != 0 {
		t.Fatalf("commits=%d launches=%d, want the router to keep every one of these turns", commits, len(fake.calls))
	}
	if records := app.packagingIntakeRecordsForThread(private.ID); len(records) != 0 {
		t.Fatalf("records=%+v, want no commission opened", records)
	}
	// The plain imperative ask in the same thread is still the intake's.
	if _, handled := app.packagingIntakeTurn(context.Background(), aj, thread, deck, nil, nil, packagingIntakeGate{}, commit); !handled {
		t.Fatal("the ordinary under-specified private deck ask stopped opening a commission")
	}
}
