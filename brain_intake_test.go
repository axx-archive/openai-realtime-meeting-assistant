package main

import (
	"context"
	"strings"
	"testing"
)

// brainIntakeTranscripts returns every transcript entry the guided intake filed.
func brainIntakeTranscripts(app *kanbanBoardApp) []meetingMemoryEntry {
	var out []meetingMemoryEntry
	for _, entry := range app.memory.snapshot(0) {
		if entry.Kind == meetingMemoryKindTranscript && entry.Metadata["source"] == "brain_intake" {
			out = append(out, entry)
		}
	}
	return out
}

func TestBrainIntakeThreadSeedsWelcomeWithPrivacyDisclosure(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	user := accountStore().findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("seed user aj@shareability.com missing")
	}

	thread, err := kanbanApp.startBrainIntakeThread(user)
	if err != nil {
		t.Fatalf("startBrainIntakeThread: %v", err)
	}
	if thread.Intake != brainIntakeKind {
		t.Fatalf("intake=%q, want %q", thread.Intake, brainIntakeKind)
	}
	if thread.IntakeStep != 0 {
		t.Fatalf("intakeStep=%d, want 0", thread.IntakeStep)
	}
	if scoutChatThreadVisibility(thread) != scoutChatVisibilityPrivate {
		t.Fatalf("intake thread visibility=%q, want private", scoutChatThreadVisibility(thread))
	}
	if len(thread.Messages) != 1 || thread.Messages[0].Role != "scout" {
		t.Fatalf("messages=%#v, want one seeded scout message", thread.Messages)
	}
	welcome := thread.Messages[0].Text
	// The privacy disclosure is the consent surface — it must be present and
	// explicit that private-thread input becomes shared room memory.
	if !strings.Contains(welcome, "part of the shared room brain") {
		t.Fatalf("welcome missing privacy disclosure: %q", welcome)
	}
	if !strings.Contains(welcome, "private to you") {
		t.Fatalf("welcome missing private framing: %q", welcome)
	}
	// Step 1's prompt is seeded so the user has something to answer immediately.
	if !strings.Contains(welcome, brainIntakeSteps[0].prompt) {
		t.Fatalf("welcome missing step-1 prompt: %q", welcome)
	}
}

func TestBrainIntakeIngestsContributionsAndAdvances(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	user := accountStore().findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("seed user aj@shareability.com missing")
	}
	thread, err := kanbanApp.startBrainIntakeThread(user)
	if err != nil {
		t.Fatalf("startBrainIntakeThread: %v", err)
	}

	files := []scoutChatFileAttachment{{Name: "origin.txt", Kind: "text/plain", Size: 64, Text: "Founded 2015 by Tim and Nick."}}
	response, err := kanbanApp.appendScoutChatThreadMessage(context.Background(), user, thread.ID, "Shareability started as a viral video studio.", files, "")
	if err != nil {
		t.Fatalf("append intake message: %v", err)
	}

	// The turn is deterministic: no router proposal or clarifying-choices card.
	if _, proposed := response["proposal"]; proposed {
		t.Fatalf("intake produced a router proposal: %#v", response)
	}
	if _, chose := response["choices"]; chose {
		t.Fatalf("intake produced a choices card: %#v", response)
	}
	answer, ok := response["answer"].(scoutChatMessageRecord)
	if !ok {
		t.Fatalf("response answer=%#v, want a scout message", response["answer"])
	}
	// The reply is exactly the next scripted prompt — proof the script drove it,
	// not the model.
	if answer.Text != brainIntakeSteps[1].prompt {
		t.Fatalf("answer=%q, want step-2 prompt %q", answer.Text, brainIntakeSteps[1].prompt)
	}
	saved := response["thread"].(scoutChatThreadRecord)
	if saved.Intake != brainIntakeKind || saved.IntakeStep != 1 {
		t.Fatalf("thread intake=%q step=%d, want brain/1", saved.Intake, saved.IntakeStep)
	}

	// Both the typed answer and the attachment text landed as brain_intake
	// transcript entries stamped with the step key they answered.
	entries := brainIntakeTranscripts(kanbanApp)
	if len(entries) != 2 {
		t.Fatalf("brain_intake transcripts=%d, want 2 (answer + attachment)", len(entries))
	}
	var sawAnswer, sawAttachment bool
	for _, entry := range entries {
		if entry.Metadata["intakeStep"] != "company_history" {
			t.Fatalf("entry step=%q, want company_history: %#v", entry.Metadata["intakeStep"], entry)
		}
		if strings.Contains(entry.Text, "viral video studio") {
			sawAnswer = true
		}
		if strings.Contains(entry.Text, "Founded 2015 by Tim and Nick") {
			sawAttachment = true
			if entry.Metadata["attachmentName"] != "origin.txt" {
				t.Fatalf("attachment entry missing name metadata: %#v", entry)
			}
		}
	}
	if !sawAnswer || !sawAttachment {
		t.Fatalf("intake entries missing answer(%v) or attachment(%v): %#v", sawAnswer, sawAttachment, entries)
	}
}

func TestBrainIntakeBypassesUsefulnessFilter(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	user := accountStore().findUser("aj@shareability.com")
	thread, err := kanbanApp.startBrainIntakeThread(user)
	if err != nil {
		t.Fatalf("startBrainIntakeThread: %v", err)
	}

	// "test" is a low-quality phrase transcriptLooksUseful rejects — but a
	// deliberate intake answer must still be filed (bypass the filler filter).
	if _, err := kanbanApp.appendScoutChatThreadMessage(context.Background(), user, thread.ID, "test", nil, ""); err != nil {
		t.Fatalf("append filtered-word intake message: %v", err)
	}
	if transcriptLooksUseful("test") {
		t.Fatal("precondition: 'test' should be filtered by the usefulness filter")
	}
	if entries := brainIntakeTranscripts(kanbanApp); len(entries) != 1 {
		t.Fatalf("brain_intake transcripts=%d, want 1 — the filter must be bypassed", len(entries))
	}
}

func TestBrainIntakeSkipAdvancesDoneCompletesAndFlushesKeyless(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	user := accountStore().findUser("aj@shareability.com")
	thread, err := kanbanApp.startBrainIntakeThread(user)
	if err != nil {
		t.Fatalf("startBrainIntakeThread: %v", err)
	}

	// "skip" advances the cursor without filing content.
	response, err := kanbanApp.appendScoutChatThreadMessage(context.Background(), user, thread.ID, "skip", nil, "")
	if err != nil {
		t.Fatalf("append skip: %v", err)
	}
	saved := response["thread"].(scoutChatThreadRecord)
	if saved.IntakeStep != 1 || saved.Intake != brainIntakeKind {
		t.Fatalf("after skip intake=%q step=%d, want brain/1", saved.Intake, saved.IntakeStep)
	}
	if len(brainIntakeTranscripts(kanbanApp)) != 0 {
		t.Fatal("skip filed content, want none")
	}

	// "done" completes early: clears the flag, posts the wrap-up, and (keyless)
	// the synthesis flush is a silent no-op that must not error.
	response, err = kanbanApp.appendScoutChatThreadMessage(context.Background(), user, thread.ID, "done", nil, "")
	if err != nil {
		t.Fatalf("append done: %v", err)
	}
	saved = response["thread"].(scoutChatThreadRecord)
	if saved.Intake != "" {
		t.Fatalf("after done intake=%q, want cleared", saved.Intake)
	}
	if saved.IntakeStep != len(brainIntakeSteps) {
		t.Fatalf("after done step=%d, want %d", saved.IntakeStep, len(brainIntakeSteps))
	}
	answer := response["answer"].(scoutChatMessageRecord)
	if answer.Text != brainIntakeCompletionMessage {
		t.Fatalf("done answer=%q, want completion message", answer.Text)
	}

	// A completed intake thread is a normal private thread again: the next
	// message routes through the ordinary path, not the intake handler.
	response, err = kanbanApp.appendScoutChatThreadMessage(context.Background(), user, thread.ID, "one more thing", nil, "")
	if err != nil {
		t.Fatalf("append post-completion message: %v", err)
	}
	if postSaved := response["thread"].(scoutChatThreadRecord); postSaved.Intake != "" {
		t.Fatalf("post-completion intake=%q, want cleared", postSaved.Intake)
	}
	// No new brain_intake entry — the completed thread no longer ingests.
	if entries := brainIntakeTranscripts(kanbanApp); len(entries) != 0 {
		t.Fatalf("brain_intake transcripts=%d after completion, want 0", len(entries))
	}
}

func TestBrainIntakeArchivedThreadRejectsMessages(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	user := accountStore().findUser("aj@shareability.com")
	thread, err := kanbanApp.startBrainIntakeThread(user)
	if err != nil {
		t.Fatalf("startBrainIntakeThread: %v", err)
	}
	if _, err := kanbanApp.setScoutChatThreadArchived(user.Email, thread.ID, true); err != nil {
		t.Fatalf("archive intake thread: %v", err)
	}
	if _, err := kanbanApp.appendScoutChatThreadMessage(context.Background(), user, thread.ID, "late answer", nil, ""); err == nil {
		t.Fatal("expected archived intake thread to reject messages")
	}
}

func TestBrainIntakeContributionsSynthesizeIntoBrain(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("MEETING_BRAIN_MIN_TRANSCRIPTS", "1")
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	user := accountStore().findUser("aj@shareability.com")
	thread, err := kanbanApp.startBrainIntakeThread(user)
	if err != nil {
		t.Fatalf("startBrainIntakeThread: %v", err)
	}
	if _, err := kanbanApp.appendScoutChatThreadMessage(context.Background(), user, thread.ID, "Our biggest win was the Adobe launch — 400M views.", nil, ""); err != nil {
		t.Fatalf("append intake answer: %v", err)
	}

	// The raw material a keyed synthesis pass would consume is exactly the
	// intake transcript. Drive one pass with a stub responder (the
	// brain_worker_test pattern) and confirm the write-up lands as a brain entry.
	entry, err := kanbanApp.runMeetingBrainOnce(context.Background(), "test-key", func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		if !strings.Contains(request.Input, "Adobe launch") {
			t.Fatalf("brain input missing intake material: %s", request.Input)
		}
		return "## Overview\nShareability's Adobe launch drove 400M views.", nil
	})
	if err != nil {
		t.Fatalf("runMeetingBrainOnce: %v", err)
	}
	if entry.Kind != meetingMemoryKindBrain {
		t.Fatalf("entry kind=%q, want brain", entry.Kind)
	}
	if !strings.Contains(entry.Text, "Adobe launch") {
		t.Fatalf("brain write-up missing synthesized material: %q", entry.Text)
	}
}
