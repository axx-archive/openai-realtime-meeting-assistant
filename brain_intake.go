package main

// Brain onboarding — the guided "Feed the brain" intake (card 082).
//
// This is glue over machinery that already exists, not new machinery: chat
// uploads flow through scout_chat_threads.go (+ card 085 derived text), raw
// material is filed via appendAttributedTranscriptEntry (memory.go), and
// synthesis is the ambient brain worker (brain_worker.go / agent_runner.go).
// The intake binds them with a fixed, deterministic script so a user can teach
// the company to itself in one guided session and have it recallable — by the
// whole team — within minutes.
//
// The script makes ZERO model calls, so the interview runs identically keyed or
// keyless; only the final synthesis flush needs OPENAI_API_KEY, and keyless it
// degrades to "raw material persisted, synthesized on the next brain pass."
//
// The privacy contract is load-bearing: the intake thread is private (owner +
// Scout) but the knowledge becomes room-global memory. The seeded welcome
// message discloses that verbatim; it is the consent surface, pinned by a test.

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// brainIntakeKind is the Intake marker stamped on a Feed-the-brain thread.
const brainIntakeKind = "brain"

// brainIntakeStep is one deterministic step in the interview.
type brainIntakeStep struct {
	key    string
	prompt string
}

// brainIntakeSteps is the fixed, ordered script. Prompts are hardcoded strings
// — no model calls, keyless-safe. Order is doctrine; the IntakeStep cursor is a
// 0-based index into this slice.
var brainIntakeSteps = []brainIntakeStep{
	{
		key:    "company_history",
		prompt: "First, the origin story. How did Shareability start — who founded it, what did the earliest days look like, and what were the pivot moments? Type as much as you have: dates, names, the turns that mattered.",
	},
	{
		key:    "hero_stories",
		prompt: "Now the proof. Tell me your best hero stories — the campaigns, case studies, and client wins you're proudest of. What happened, and what was the result? Numbers welcome.",
	},
	{
		key:    "decks",
		prompt: "Upload your best decks. Attach any pitch, sales, or credentials decks (PDF, slides, or exported text) and I'll read them into the brain. Say \"skip\" to move on.",
	},
	{
		key:    "brand",
		prompt: "Your visual identity. Attach brand guidelines, logos, or key imagery — or type a few lines on how Shareability looks and sounds. Say \"skip\" if you don't have files handy.",
	},
	{
		key:    "docs",
		prompt: "Any other reference docs — one-pagers, rate cards, process docs, playbooks, org charts. Attach or paste them here, or \"skip\".",
	},
	{
		key:    "comms_style",
		prompt: "How does Shareability talk? Paste a few real examples — an outreach email, a proposal intro, a LinkedIn post — anything that shows the house voice.",
	},
	{
		key:    "clients_deals",
		prompt: "Last one: the relationships. Who are your marquee clients, active deals, and the partners that matter? When you're finished, say \"done\" and I'll synthesize everything into the room brain.",
	},
}

// brainIntakeCompletionMessage is Scout's wrap-up when the interview finishes.
const brainIntakeCompletionMessage = "That's everything — thank you. I'm synthesizing what you shared into the room brain now; it'll be recallable by the whole team in a few minutes. You can close this thread, or reopen \"Feed the brain\" anytime to add more."

// brainIntakeWelcome frames the flow, discloses the privacy contract verbatim
// (the consent surface — pinned by a test), and poses step 1 in one message.
func brainIntakeWelcome() string {
	return strings.Join([]string{
		"Let's feed the brain. Over the next few minutes I'll ask about Shareability's history, hero stories, decks, brand, docs, comms style, and clients. Answer in your own words, attach files, or say \"skip\" to move on — say \"done\" anytime to wrap up.",
		"Heads up on privacy: this thread is private to you, but everything you share here becomes part of the shared room brain so Scout can recall it for the whole team. Don't paste anything you wouldn't want the office to know.",
		brainIntakeSteps[0].prompt,
	}, "\n\n")
}

// brainIntakeStepKey returns the step key for an IntakeStep cursor, clamped so
// an out-of-range cursor still stamps a stable marker.
func brainIntakeStepKey(index int) string {
	if index < 0 || index >= len(brainIntakeSteps) {
		return "wrap"
	}
	return brainIntakeSteps[index].key
}

// brainIntakeIsControlVerb reports whether a bare answer is a flow-control word
// (skip/done) rather than content — control words advance the script but are
// not filed as brain material (attachments riding them still are).
func brainIntakeIsControlVerb(text string) bool {
	switch strings.ToLower(strings.TrimSpace(text)) {
	case "skip", "next", "pass", "done", "finish", "that's all", "that is all", "stop":
		return true
	}
	return false
}

// brainIntakeIsDone reports whether a bare answer ends the interview early.
func brainIntakeIsDone(text string) bool {
	switch strings.ToLower(strings.TrimSpace(text)) {
	case "done", "finish", "that's all", "that is all", "stop":
		return true
	}
	return false
}

// startBrainIntakeThread creates a private Feed-the-brain thread and seeds the
// welcome + privacy disclosure + step-1 prompt. The intake flag routes every
// subsequent message through handleBrainIntakeMessage instead of the router.
func (app *kanbanBoardApp) startBrainIntakeThread(user *userAccount) (scoutChatThreadRecord, error) {
	if user == nil {
		return scoutChatThreadRecord{}, fmt.Errorf("sign in to feed the brain")
	}
	thread, err := app.createScoutChatThread(user.Email, user.Name, "Feed the brain", scoutChatVisibilityPrivate)
	if err != nil {
		return scoutChatThreadRecord{}, err
	}
	return app.seedBrainIntake(thread.OwnerEmail, thread.ID)
}

// seedBrainIntake stamps the intake flag and appends the welcome message under
// the same per-thread lock + re-read + save discipline as message commits.
func (app *kanbanBoardApp) seedBrainIntake(ownerEmail string, threadID string) (scoutChatThreadRecord, error) {
	lock := app.scoutChatThreadLock(threadID)
	lock.Lock()
	defer lock.Unlock()

	thread, _, err := app.scoutChatThreadByID(ownerEmail, threadID)
	if err != nil {
		return scoutChatThreadRecord{}, err
	}
	now := time.Now().UTC()
	welcome := scoutChatMessageRecord{
		ID:        fmt.Sprintf("scout-chat-message-%d", now.UnixNano()),
		Kind:      "message",
		Role:      "scout",
		Text:      brainIntakeWelcome(),
		CreatedAt: now.Format(time.RFC3339Nano),
	}
	thread.Intake = brainIntakeKind
	thread.IntakeStep = 0
	thread.Messages = append(thread.Messages, welcome)
	thread.UpdatedAt = now.Format(time.RFC3339Nano)
	thread.Preview = scoutChatThreadPreview(thread)
	if err := app.saveScoutChatThread(thread); err != nil {
		return scoutChatThreadRecord{}, err
	}
	deliverScoutChatThreadUpdate(thread, welcome)
	return thread, nil
}

// handleBrainIntakeMessage runs one turn of the guided interview: file the
// contribution as raw brain material, advance the script, and reply with the
// next scripted prompt (or, on completion, the wrap-up + a synthesis flush).
// No router, no proposal cards, no keyword launches — the whole point is a
// deterministic, private, keyless-safe flow. response already carries
// {"ok":true,"message":userMessage} from the caller.
func (app *kanbanBoardApp) handleBrainIntakeMessage(user *userAccount, thread scoutChatThreadRecord, userMessage scoutChatMessageRecord, response map[string]any) (map[string]any, error) {
	currentKey := brainIntakeStepKey(thread.IntakeStep)
	app.appendBrainIntakeContribution(user, currentKey, userMessage)

	nextStep := thread.IntakeStep + 1
	complete := brainIntakeIsDone(userMessage.Text) || nextStep >= len(brainIntakeSteps)

	now := time.Now().UTC()
	scoutMessage := scoutChatMessageRecord{
		ID:        fmt.Sprintf("scout-chat-message-%d", now.UnixNano()+1),
		Kind:      "message",
		Role:      "scout",
		CreatedAt: now.Format(time.RFC3339Nano),
	}
	if complete {
		nextStep = len(brainIntakeSteps)
		scoutMessage.Text = brainIntakeCompletionMessage
	} else {
		scoutMessage.Text = brainIntakeSteps[nextStep].prompt
	}

	saved, err := app.commitBrainIntakeStep(user.Email, thread.ID, nextStep, complete, userMessage, scoutMessage)
	if err != nil {
		return nil, err
	}
	if complete {
		// Force one brain pass now so the user does not wait the ambient
		// interval; keyless or with the worker disabled this is a silent no-op
		// and the raw material synthesizes on the next real pass.
		go app.flushBrainForIntake()
	}
	response["answer"] = scoutMessage
	response["thread"] = saved
	return response, nil
}

// appendBrainIntakeContribution files one intake turn as raw brain material:
// the typed answer (unless it is a bare control verb) plus each attachment's
// text, each as a transcript entry with source=brain_intake + step metadata,
// bypassing the usefulness filter (a terse client name is signal, not filler)
// and deduped on an event id derived from the chat message id.
func (app *kanbanBoardApp) appendBrainIntakeContribution(user *userAccount, stepKey string, message scoutChatMessageRecord) {
	if app == nil || app.memory == nil {
		return
	}
	speaker := scoutChatAuthorName(user)
	baseMeta := func() map[string]string {
		return map[string]string{"source": "brain_intake", "intakeStep": stepKey}
	}

	text := strings.TrimSpace(message.Text)
	if text != "" && !brainIntakeIsControlVerb(text) {
		eventID := "brain-intake-" + message.ID
		if _, _, err := app.memory.appendAttributedTranscriptEntry(eventID, "", speaker, "", text, baseMeta(), true, ""); err != nil {
			log.Errorf("Failed to file brain intake answer: %v", err)
		}
	}

	for index, file := range message.Files {
		fileText := strings.TrimSpace(file.Text)
		if fileText == "" {
			// A binary with no derived text (keyless, or an unreadable upload)
			// still leaves a marker so the brain knows the file exists.
			fileText = "Attached: " + strings.TrimSpace(file.Name)
		}
		meta := baseMeta()
		if name := strings.TrimSpace(file.Name); name != "" {
			meta["attachmentName"] = name
			fileText = name + "\n" + fileText
		}
		if ref := strings.TrimSpace(file.Ref); ref != "" {
			meta["blobRef"] = ref
		}
		eventID := fmt.Sprintf("brain-intake-%s-file-%d", message.ID, index)
		if _, _, err := app.memory.appendAttributedTranscriptEntry(eventID, "", speaker, "", fileText, meta, true, ""); err != nil {
			log.Errorf("Failed to file brain intake attachment: %v", err)
		}
	}
}

// commitBrainIntakeStep appends the turn's messages AND advances the intake
// cursor (clearing the flag on completion) under one per-thread lock + re-read
// + save, so a concurrent write cannot revert the cursor or resurrect the flag.
func (app *kanbanBoardApp) commitBrainIntakeStep(ownerEmail string, threadID string, nextStep int, complete bool, messages ...scoutChatMessageRecord) (scoutChatThreadRecord, error) {
	lock := app.scoutChatThreadLock(threadID)
	lock.Lock()
	defer lock.Unlock()

	thread, _, err := app.scoutChatThreadByID(ownerEmail, threadID)
	if err != nil {
		return scoutChatThreadRecord{}, err
	}
	thread.Messages = append(thread.Messages, messages...)
	thread.IntakeStep = nextStep
	if complete {
		thread.Intake = ""
	}
	thread.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	thread.Preview = scoutChatThreadPreview(thread)
	if err := app.saveScoutChatThread(thread); err != nil {
		return scoutChatThreadRecord{}, err
	}
	for _, message := range messages {
		deliverScoutChatThreadUpdate(thread, message)
	}
	return thread, nil
}

// flushBrainForIntake synchronously runs one brain pass with a batch minimum of
// one, so the raw material a just-finished intake filed is synthesized in
// minutes rather than at the next ambient tick. It is the brain-agent-only twin
// of flushAmbientAgentsForArchive (agent_runner.go): read the key under the
// lock, honor both disable forms, register the boot baseline, run one pass.
// Skips silently when no key is configured or the worker is disabled.
func (app *kanbanBoardApp) flushBrainForIntake() {
	if app == nil || app.memory == nil {
		return
	}
	app.mu.Lock()
	apiKey := app.apiKey
	app.mu.Unlock()
	if strings.TrimSpace(apiKey) == "" {
		return
	}
	agent := meetingBrainAgent()
	if boolEnv(agent.disabledEnv) || agent.interval() <= 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), meetingBrainRequestTimeout)
	defer cancel()
	app.ensureAmbientAgentBaseline(agent)
	if _, err := app.runAmbientAgentOnce(agent, ctx, apiKey, nil, 1); err != nil {
		log.Errorf("brain intake flush failed: %v", err)
	}
}
