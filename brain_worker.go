package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	meetingBrainAgentName             = "meeting brain"
	meetingBrainCursorMetadataKey     = "processedThroughTranscriptId"
	defaultMeetingBrainInterval       = 5 * time.Minute
	defaultMeetingBrainMinTranscripts = 4
	// defaultMeetingBrainMaxTranscripts (A7) is lowered from 80 so a single dense
	// window can no longer outgrow the output budget and truncate the mandated
	// Transcript-reference audit trail mid-word. With the A3 event nudge firing
	// passes closer to real time, windows are smaller in practice anyway.
	defaultMeetingBrainMaxTranscripts = 48
	meetingBrainRequestTimeout        = 90 * time.Second
	// meetingBrain output-budget scaling (A7): the base covers a small window;
	// each additional transcript widens the budget so the reference section (the
	// LAST section the model writes) survives, capped so a large backfill window
	// can never request an unbounded completion.
	meetingBrainBaseMaxOutputTokens = 900
	meetingBrainPerTranscriptTokens = 26
	meetingBrainMaxOutputTokensCap  = 2400
)

// brainMaxOutputTokens scales the brain's completion budget with the transcript
// window size so the trailing "Transcript reference" IDs are not truncated in a
// dense window (A7).
func brainMaxOutputTokens(transcriptCount int) int {
	if transcriptCount < 0 {
		transcriptCount = 0
	}
	tokens := meetingBrainBaseMaxOutputTokens + transcriptCount*meetingBrainPerTranscriptTokens
	if tokens > meetingBrainMaxOutputTokensCap {
		tokens = meetingBrainMaxOutputTokensCap
	}
	return tokens
}

func meetingBrainAgent() ambientAgentConfig {
	return ambientAgentConfig{
		name:            meetingBrainAgentName,
		defaultInterval: defaultMeetingBrainInterval,
		intervalEnv:     "MEETING_BRAIN_INTERVAL",
		disabledEnv:     "MEETING_BRAIN_DISABLED",
		backfillEnv:     "MEETING_BRAIN_BACKFILL",
		minBatchEnv:     "MEETING_BRAIN_MIN_TRANSCRIPTS",
		defaultMinBatch: defaultMeetingBrainMinTranscripts,
		maxBatchEnv:     "MEETING_BRAIN_MAX_TRANSCRIPTS",
		defaultMaxBatch: defaultMeetingBrainMaxTranscripts,
		inputKind:       meetingMemoryKindTranscript,
		artifactKind:    meetingMemoryKindBrain,
		// Provenance and consumption are deliberately separate. The public
		// from/through fields describe only sources authorized for the model;
		// this private cursor may advance across denied inputs that were checked
		// and omitted.
		cursorMetadataKey: meetingBrainCursorMetadataKey,
		requestTimeout:    meetingBrainRequestTimeout,
		// W4 §7.4: per-room cursors — one room's brain pass must never advance
		// another room's transcript window. §6.5: a guests-only room defers its
		// scheduled passes until a member is present (or the close flush).
		roomScoped:           true,
		defersWhenGuestsOnly: true,
		produce:              (*kanbanBoardApp).produceMeetingBrainWriteUp,
	}
}

func (app *kanbanBoardApp) startMeetingBrainWorker(apiKey string) {
	app.startAmbientAgent(meetingBrainAgent(), apiKey)
}

func (app *kanbanBoardApp) runMeetingBrainOnce(ctx context.Context, apiKey string, responder openAITextResponder) (meetingMemoryEntry, error) {
	agent := meetingBrainAgent()
	return app.runAmbientAgentOnce(agent, ctx, apiKey, responder, agent.minBatch())
}

func (app *kanbanBoardApp) produceMeetingBrainWriteUp(ctx context.Context, apiKey string, transcripts []meetingMemoryEntry, responder openAITextResponder) (meetingMemoryEntry, error) {
	// W4: the runner hands each pass ONE room's window; the room rides the
	// artifact (cursor partitioning) and the downstream nudges.
	roomID := ambientWindowRoomID(transcripts)
	rawTranscripts := transcripts
	verifier := appBrainSourceConsentVerifier{App: app}
	transcripts = make([]meetingMemoryEntry, 0, len(rawTranscripts))
	for _, transcript := range rawTranscripts {
		_, err := verifier.AuthorizeBrainSourceConsent(ctx, transcript)
		if err == nil {
			transcripts = append(transcripts, transcript)
			continue
		}
		if errors.Is(err, ErrBrainSourceConsentAbsent) {
			continue
		}
		// Authority outages and other indeterminate states hold the cursor and
		// never send a partial prompt to a model.
		return meetingMemoryEntry{}, err
	}
	if len(transcripts) == 0 {
		return meetingMemoryEntry{}, nil
	}
	// Subscribe before the final provider-ingress check. A withdrawal that lands
	// after this point cancels the provider context; one that landed just before
	// the subscription is caught by the durable reauthorization below.
	allOrgFences := make([]ConsentFence, 0, len(transcripts))
	for _, transcript := range transcripts {
		fences, err := verifier.AuthorizeBrainSourceConsent(ctx, transcript)
		if err != nil {
			if errors.Is(err, ErrBrainSourceConsentAbsent) {
				return meetingMemoryEntry{}, nil
			}
			return meetingMemoryEntry{}, err
		}
		allOrgFences = append(allOrgFences, fences...)
	}
	providerCtx, cancelProvider := context.WithCancel(ctx)
	defer cancelProvider()
	boundContributors := make(map[string]struct{}, len(allOrgFences))
	for _, fence := range allOrgFences {
		boundContributors[consentBindingKey(fence.binding)] = struct{}{}
	}
	unsubscribeWithdrawal := subscribeConsentWithdrawals(func(notice ConsentWithdrawalNotice) {
		if _, matches := boundContributors[consentBindingKey(notice.Binding)]; matches {
			cancelProvider()
		}
	})
	defer unsubscribeWithdrawal()
	for _, fence := range allOrgFences {
		if err := currentConsentLaneAuthority().ValidateFence(providerCtx, fence); err != nil {
			if errors.Is(err, ErrConsentFenceStale) {
				return meetingMemoryEntry{}, nil
			}
			return meetingMemoryEntry{}, err
		}
	}
	model := meetingBrainModel()
	text, err := responder(providerCtx, apiKey, openAITextRequest{
		Model:           model,
		Seat:            seatBrain,
		Instructions:    meetingBrainInstructions(),
		Input:           buildMeetingBrainInput(transcripts, app.snapshotState(), app.participantSnapshotForRoom(roomID), time.Now().UTC()),
		ReasoningEffort: "low",
		Verbosity:       "low",
		MaxOutputTokens: brainMaxOutputTokens(len(transcripts)),
	})
	if err != nil {
		return meetingMemoryEntry{}, err
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return meetingMemoryEntry{}, nil
	}

	firstTranscript := transcripts[0]
	lastTranscript := transcripts[len(transcripts)-1]
	metadata := map[string]string{
		"source":                      "openai_responses",
		"model":                       model,
		"roomId":                      roomID,
		"fromTranscriptId":            firstTranscript.ID,
		"throughTranscriptId":         lastTranscript.ID,
		"fromTranscriptCreatedAt":     firstTranscript.CreatedAt.Format(time.RFC3339Nano),
		"throughTranscriptCreatedAt":  lastTranscript.CreatedAt.Format(time.RFC3339Nano),
		"transcriptCount":             strconv.Itoa(len(transcripts)),
		meetingBrainCursorMetadataKey: rawTranscripts[len(rawTranscripts)-1].ID,
	}
	metadata = applyAmbientDerivedScope(metadata, transcripts)
	// §6.4 provenance (inclusion RATIFIED 2026-07-09): a write-up over a
	// listen-only sitting's transcripts carries the origin stamp — the rollups
	// consume it like any other material; the stamp keeps the external-guest
	// origin visible and is the key a re-quarantine filter would use.
	if app.windowIncludesListenOnly(transcripts) {
		metadata[listenOnlyMetadataKey] = "true"
	}
	id := durableTimestampID("brain", time.Now())
	var entry meetingMemoryEntry
	var appended bool
	commit := func() error {
		var appendErr error
		entry, appended, appendErr = app.memory.appendBrainWriteUp(id, text, metadata)
		return appendErr
	}
	if len(allOrgFences) == 0 {
		err = commit()
	} else {
		err = currentConsentLaneAuthority().CommitWithFences(ctx, allOrgFences, commit)
	}
	if err != nil || !appended {
		return entry, err
	}

	broadcastScopedMemoryEntry("memory_brain", entry, entry)
	// Office memory rails stay live via the snapshot path: the entry-shaped
	// memory_brain event stays room-only because the client's addMemoryEntry
	// does not dedupe by id.
	broadcastOfficeKanbanEvent("memory", nil)
	broadcastScopedMemoryEntry("assistant_event", entry, map[string]any{
		"kind": "action", "text": "Scout updated the room brain.", "memoryKind": meetingMemoryKindBrain, "createdAt": time.Now().UTC().Format(time.RFC3339Nano),
	})

	// A3 cascade: a fresh write-up just landed — wake every worker that consumes
	// brains so the board / ledger / mission / narrative reflect it promptly
	// instead of each waiting for its own floor tick. Each nudge is debounced,
	// carries THIS pass's room (W4), and runs under its agent's (agent, room)
	// run lock, so this cannot double-fire a pass.
	app.nudgeAmbientAgentForRoom(meetingBoardAgentName, roomID)
	app.nudgeAmbientAgentForRoom(decisionLedgerAgentName, roomID)
	app.nudgeAmbientAgentForRoom(missionIntelAgentName, roomID)
	app.nudgeAmbientAgentForRoom(narrativeMaintainerAgentName, roomID)

	return entry, nil
}

func positiveIntEnv(name string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 {
		return fallback
	}

	return value
}

func meetingBrainInstructions() string {
	return strings.Join([]string{
		"You are Scout's durable meeting brain for Bonfire.",
		"Create a faithful, high-signal memory write-up from the supplied transcript window.",
		"Preserve who said what. Do not invent facts, participants, clients, dates, decisions, or action items.",
		"When the transcript is unclear, say it is unclear instead of guessing.",
		"Resolve every spoken relative date ('yesterday', 'next Friday', 'end of the month') to an absolute YYYY-MM-DD using the generation date above; never leave a relative date unresolved in the write-up.",
		"Write compact markdown with these sections: Overview, People, Topics, Decisions, Follow-ups, Project and client notes, Transcript reference.",
		"Keep the Transcript reference brief but include the transcript IDs that matter so raw transcript entries can be checked later.",
	}, " ")
}

func buildMeetingBrainInput(transcripts []meetingMemoryEntry, board kanbanBoardState, participants []string, generatedAt time.Time) string {
	var builder strings.Builder
	builder.WriteString("# Generated at\n")
	builder.WriteString(generatedAt.Format(time.RFC3339))
	builder.WriteString("\n\n# Active participants\n")
	if len(participants) == 0 {
		builder.WriteString("Unknown\n")
	} else {
		builder.WriteString(strings.Join(participants, ", "))
		builder.WriteByte('\n')
	}

	builder.WriteString("\n# Current board snapshot\n")
	if rawBoard, err := json.Marshal(board.Cards); err == nil {
		builder.WriteString(string(rawBoard))
	} else {
		builder.WriteString("[]")
	}

	builder.WriteString("\n\n# Transcript window\n")
	for _, entry := range transcripts {
		builder.WriteString("- ")
		builder.WriteString(entry.ID)
		builder.WriteString(" | ")
		builder.WriteString(entry.CreatedAt.Format(time.RFC3339))
		if speaker := strings.TrimSpace(entry.Metadata["speaker"]); speaker != "" {
			builder.WriteString(" | speaker=")
			builder.WriteString(speaker)
		}
		builder.WriteString(" | ")
		builder.WriteString(entry.Text)
		builder.WriteByte('\n')
	}

	return builder.String()
}

func (store *meetingMemoryStore) unsummarizedTranscripts(limit int) []meetingMemoryEntry {
	return store.unsummarizedTranscriptsAfter(limit, "")
}

func (store *meetingMemoryStore) unsummarizedTranscriptsAfter(limit int, baselineTranscriptID string) []meetingMemoryEntry {
	return store.unconsumedEntriesAfter(meetingMemoryKindTranscript, meetingMemoryKindBrain, meetingBrainCursorMetadataKey, limit, baselineTranscriptID)
}

func (store *meetingMemoryStore) latestTranscriptID() string {
	return store.latestEntryIDOfKind(meetingMemoryKindTranscript)
}
