package main

// Chat-native extractor (Wave 8 D5): the channel digest producer on the digest
// chassis, keyed by threadId.
//
// Channel and Riff messages are filed as kind=transcript rows (source=channel|
// riff, channel_brain_ingestion.go). Before this wave they rode the meeting
// brain prompt as if they were spoken in the office. Now:
//
//   - the meeting brain's window filter (meetingSourcedTranscript,
//     brain_worker.go) structurally excludes them — they never reach the
//     brain prompt, and because the filter is applied when the window is
//     collected (not after), the brain cursor never has to "skip over" them;
//   - this worker consumes ONLY channel-sourced transcripts (its own window
//     filter), groups them by threadId, and maintains ONE cumulative
//     kind=channel_digest per thread through upsertDigest — the same strict
//     anchored-JSON schema, clamps, and carry-forward guard the meeting digest
//     uses, driven by a chat-specific instruction variant.
//
// Cursor: a kind=channel_digest_pass artifact (the day_digest_pass pattern)
// stamps throughChannelTranscriptId after every pass, so a window that lands
// nothing (all rows filtered, or output rejected on a thin thread) still
// advances and can never starve newer channels.
//
// ACL: the runner's shared-room service principal already excludes
// project-scoped (member-restricted) channel rows from any ambient window, so
// only organization-public channel rows are ever digested here; the digest
// carries the group's own visibility/owner/member stamps regardless, so a
// future member-scoped pass would inherit the right recall fence.
//
// Keyless-safe: startAmbientAgent never starts without an OpenAI key.

import (
	"context"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	channelDigestAgentName          = "channel digest"
	defaultChannelDigestInterval    = 10 * time.Minute
	channelDigestRequestTimeout     = 90 * time.Second
	defaultChannelDigestMinMessages = 3
	defaultChannelDigestMaxMessages = 40
	channelDigestCursorMetadataKey  = "throughChannelTranscriptId"
	channelDigestMaxOutputTokens    = meetingDigestMaxOutputTokens
	// A channel digest invalidated by a message delete/edit (Wave 8 D12
	// follow-up) is expired + stamped digestStale=true; the next pass rebuilds
	// it from LIVE rows only, never carrying facts forward from the stale
	// payload, and a drained window still services it.
	channelDigestStaleMetadataKey       = "digestStale"
	channelDigestStaleReasonMetadataKey = "digestStaleReason"
	channelDigestStaleAtMetadataKey     = "digestStaleAt"
	// channelDigestRebuildRowCap bounds a from-scratch rebuild to the newest
	// live rows of the thread: with no carry-forward available, privacy wins
	// over completeness for a very long thread.
	channelDigestRebuildRowCap = 4 * defaultChannelDigestMaxMessages
)

func channelDigestAgent() ambientAgentConfig {
	return ambientAgentConfig{
		name:              channelDigestAgentName,
		defaultInterval:   defaultChannelDigestInterval,
		intervalEnv:       "CHANNEL_DIGEST_INTERVAL",
		disabledEnv:       "CHANNEL_DIGEST_DISABLED",
		backfillEnv:       "CHANNEL_DIGEST_BACKFILL",
		minBatchEnv:       "CHANNEL_DIGEST_MIN_MESSAGES",
		defaultMinBatch:   defaultChannelDigestMinMessages,
		maxBatchEnv:       "CHANNEL_DIGEST_MAX_MESSAGES",
		defaultMaxBatch:   defaultChannelDigestMaxMessages,
		inputKind:         meetingMemoryKindTranscript,
		artifactKind:      meetingMemoryKindChannelDigestPass,
		cursorMetadataKey: channelDigestCursorMetadataKey,
		requestTimeout:    channelDigestRequestTimeout,
		inputFilter:       channelSourcedTranscript,
		produce:           (*kanbanBoardApp).produceChannelDigests,
		drainedWork:       (*kanbanBoardApp).rebuildStaleChannelDigests,
	}
}

func (app *kanbanBoardApp) startChannelDigestWorker(apiKey string) {
	app.startAmbientAgent(channelDigestAgent(), apiKey)
}

// channelSourcedTranscript admits only the chat-filed transcript rows: a
// channel/riff source with a thread to key the digest on.
func channelSourcedTranscript(entry meetingMemoryEntry) bool {
	if entry.Kind != meetingMemoryKindTranscript {
		return false
	}
	source := strings.ToLower(strings.TrimSpace(entry.Metadata["source"]))
	if source != transcriptSourceChannel && source != transcriptSourceRiff {
		return false
	}
	return strings.TrimSpace(entry.Metadata["threadId"]) != ""
}

// meetingSourcedTranscript is the meeting brain's window filter: everything
// that is NOT a chat-filed row (spoken transcript, room chat, intake).
func meetingSourcedTranscript(entry meetingMemoryEntry) bool {
	return !channelSourcedTranscript(entry)
}

type channelDigestGroup struct {
	threadID string
	title    string
	messages []meetingMemoryEntry
	// rebuild marks a thread whose digest was invalidated: messages holds the
	// thread's live rows (not just the window) and no prior digest is fed
	// back — the rebuild starts from what is actually still said.
	rebuild bool
}

// groupTranscriptsByThread buckets the window per threadId, oldest first,
// threads ordered by first appearance so a pass is deterministic.
func groupTranscriptsByThread(inputs []meetingMemoryEntry) []channelDigestGroup {
	index := map[string]int{}
	groups := make([]channelDigestGroup, 0, 4)
	for _, entry := range inputs {
		if !channelSourcedTranscript(entry) {
			continue
		}
		threadID := strings.TrimSpace(entry.Metadata["threadId"])
		position, ok := index[threadID]
		if !ok {
			position = len(groups)
			index[threadID] = position
			groups = append(groups, channelDigestGroup{threadID: threadID})
		}
		if groups[position].title == "" {
			groups[position].title = firstNonEmptyString(strings.TrimSpace(entry.Metadata["channelTitle"]), strings.TrimSpace(entry.Metadata["sourceTitle"]))
		}
		groups[position].messages = append(groups[position].messages, entry)
	}
	for position := range groups {
		sort.SliceStable(groups[position].messages, func(i, j int) bool {
			return groups[position].messages[i].CreatedAt.Before(groups[position].messages[j].CreatedAt)
		})
	}
	return groups
}

func channelDigestInstructions() string {
	return strings.Join([]string{
		"You are Bonfire's channel digest compiler.",
		"Fold the previous digest (when present) and the new chat messages of ONE channel into a cumulative digest of what this channel has established so far.",
		"Return STRICT JSON only, no markdown fence, matching:",
		`{"meetingId":string,"title":string(<=80 chars),"day":"YYYY-MM-DD","started":RFC3339,"ended":RFC3339,"attendees":[string](<=12),"topics":[{"t":string(<=160),"anchor":string,"at":RFC3339,"importance":int}],"decisions":[{"d":string(<=240),"by":string,"status":string,"anchor":string,"at":RFC3339,"importance":int}],"actionItems":[{"a":string(<=200),"owner":string,"status":string,"anchor":string,"at":RFC3339,"importance":int}],"openQuestions":[{"q":string(<=200),"anchor":string,"at":RFC3339,"importance":int}],"themes":[string],"aliases":[string]}.`,
		"meetingId = the channel thread id supplied below (copied verbatim); attendees = the people who wrote in the window.",
		"Chat is asynchronous and terse: a message can settle a question asked days earlier, and a thread can hold several unrelated topics — keep them as separate topics, never merge them.",
		"importance scores each fact 1-5: 5 = blocking or company-critical, 4 = a real commitment or decision, 3 = notable, 2 = context, 1 = passing chatter.",
		"anchor = one message entry id copied VERBATIM from the ids supplied below; empty string when uncertain — never fabricate ids.",
		"at = the RFC3339 time of the message the fact surfaced in; empty string when unknown.",
		"Resolve every relative date ('tomorrow', 'next Friday', 'end of the month') to an absolute YYYY-MM-DD using the message timestamps; never leave a relative date in a fact's text, and put an absolute RFC3339 in 'at'.",
		"aliases = 3-5 short alternate phrasings someone might SEARCH this channel's topics by — synonyms, nicknames, acronyms; empty when nothing is ambiguous. Max 5, each <=60 chars.",
		"Carry forward still-relevant facts from the previous digest and update their statuses; a decision stays until explicitly reversed; an action item marked done keeps its row with status done.",
		"Authorship in chat is exact (the author is stamped), so attribute plainly by name; never invent facts, people, clients, dates, decisions, or action items.",
		"Reactions, greetings, thanks, and link-only messages are not facts. If the window is thin, return fewer items, never filler.",
		"Caps: topics<=12, decisions<=12, actionItems<=16, openQuestions<=10, themes<=8, aliases<=5.",
	}, " ")
}

// buildChannelDigestInput assembles one thread's prompt: prior digest
// continuity plus the new messages (id, time, author, text).
func buildChannelDigestInput(group channelDigestGroup, prior meetingMemoryEntry, hasPrior bool, generatedAt time.Time) string {
	var builder strings.Builder
	builder.WriteString("# Generated at\n")
	builder.WriteString(generatedAt.Format(time.RFC3339))
	builder.WriteString("\n\n# Channel\nthread id: ")
	builder.WriteString(group.threadID)
	if group.title != "" {
		builder.WriteString("\ntitle: ")
		builder.WriteString(group.title)
	}
	if hasPrior {
		builder.WriteString("\n\n# Previous digest for this channel (continuity — carry forward, update statuses, never silently drop)\n")
		builder.WriteString(prior.Text)
	}
	builder.WriteString("\n\n# New messages (oldest first)\n")
	for _, message := range group.messages {
		builder.WriteString("- id=")
		builder.WriteString(message.ID)
		builder.WriteString(" time=")
		builder.WriteString(message.CreatedAt.UTC().Format(time.RFC3339))
		if speaker := strings.TrimSpace(message.Metadata["speaker"]); speaker != "" {
			builder.WriteString(" author=")
			builder.WriteString(speaker)
		}
		builder.WriteString("\n  ")
		builder.WriteString(strings.TrimSpace(message.Text))
		builder.WriteByte('\n')
	}
	return builder.String()
}

// produceChannelDigests is the chassis producer: one model call per thread in
// the window, one upsert per accepted output, then the pass artifact that
// carries the cursor. Provider outages hold the cursor (no pass artifact);
// a rejected/unparseable output for ONE thread keeps that thread's prior
// digest and still lets the pass land, because the thread re-feeds naturally
// with its next message.
func (app *kanbanBoardApp) produceChannelDigests(ctx context.Context, apiKey string, inputs []meetingMemoryEntry, responder openAITextResponder) (meetingMemoryEntry, error) {
	if app == nil || app.memory == nil {
		return meetingMemoryEntry{}, nil
	}
	if responder == nil {
		responder = createOpenAITextResponse
	}
	groups := app.withStaleChannelDigestRebuilds(groupTranscriptsByThread(inputs))
	if len(groups) == 0 {
		return meetingMemoryEntry{}, nil
	}
	model := meetingBrainModel()
	now := time.Now().UTC()
	rebuilt := make([]string, 0, len(groups))
	rejected := make([]string, 0, 2)
	for _, group := range groups {
		prior, hasPrior := meetingMemoryEntry{}, false
		if !group.rebuild {
			prior, hasPrior = app.memory.currentDigest(meetingMemoryKindChannelDigest, group.threadID)
		}
		generatedAt := group.messages[len(group.messages)-1].CreatedAt.UTC()
		text, err := responder(ctx, apiKey, openAITextRequest{
			Model:           model,
			Seat:            seatMeetingDigest,
			Workflow:        "channel_digest",
			Instructions:    channelDigestInstructions(),
			Input:           buildChannelDigestInput(group, prior, hasPrior, generatedAt),
			ReasoningEffort: meetingBrainReasoningEffort(),
			Verbosity:       "low",
			MaxOutputTokens: channelDigestMaxOutputTokens,
			JSONSchema:      meetingDigestJSONSchema(),
			ValidateOutput: func(text string) error {
				var payload meetingDigestPayload
				return json.Unmarshal([]byte(strings.TrimSpace(text)), &payload)
			},
		})
		if err != nil {
			if isProviderInvocationFailure(err) {
				return meetingMemoryEntry{}, &ambientAgentHoldError{err: err}
			}
			if reason, isRejection := openAIOutputRejectionReason(err); isRejection {
				log.Errorf("%s output rejected for thread %s (%s); keeping the prior digest", channelDigestAgentName, group.threadID, reason)
				rejected = append(rejected, group.threadID)
				continue
			}
			return meetingMemoryEntry{}, err
		}
		payload, ok := parseMeetingDigest(text)
		if !ok {
			log.Errorf("%s returned non-JSON output for thread %s; keeping the prior digest", channelDigestAgentName, group.threadID)
			rejected = append(rejected, group.threadID)
			continue
		}
		spanStart := group.messages[0].CreatedAt.UTC()
		spanEnd := generatedAt
		if hasPrior {
			if priorStart, _, ok := parseDigestSpanMetadata(prior); ok && priorStart.Before(spanStart) {
				spanStart = priorStart
			}
		}
		day := dayBucket(spanEnd)
		// meetingId doubles as the digest key on the shared payload shape: for a
		// channel digest it is the thread id.
		clampMeetingDigestPayload(&payload, group.threadID, day, spanStart, spanEnd)
		if payload.Title == "" && group.title != "" {
			payload.Title = trimForStorage(group.title, 80)
		}
		droppedFacts := 0
		if hasPrior {
			if priorPayload, priorOK := parseMeetingDigest(prior.Text); priorOK {
				droppedFacts = carryForwardMeetingDigestFacts(&payload, priorPayload)
				payload.Aliases = clampDigestAliases(append(payload.Aliases, priorPayload.Aliases...))
			}
		}
		canonical, err := json.Marshal(payload)
		if err != nil {
			return meetingMemoryEntry{}, err
		}
		first := group.messages[0]
		newest := group.messages[len(group.messages)-1]
		metadata := map[string]string{
			"source":                   "openai_responses",
			"model":                    model,
			"threadId":                 group.threadID,
			"channelTitle":             group.title,
			digestDayMetadataKey:       day,
			digestSpanStartMetadataKey: spanStart.Format(time.RFC3339),
			digestSpanEndMetadataKey:   spanEnd.Format(time.RFC3339),
			"fromTranscriptId":         first.ID,
			"throughTranscriptId":      newest.ID,
			"messageCount":             strconv.Itoa(len(group.messages)),
			"generatedAt":              now.Format(time.RFC3339),
		}
		if len(inputs) > 0 {
			metadata[channelDigestCursorMetadataKey] = inputs[len(inputs)-1].ID
		}
		if group.rebuild {
			metadata["rebuiltFromLiveRows"] = "true"
		}
		// Recall fence from the thread's CURRENT state (visibility + member
		// list at rebuild time), falling back to the newest row's stamps —
		// never the oldest row in the window, which predates any membership
		// change.
		for key, value := range app.channelDigestRecallFence(group.threadID, newest) {
			metadata[key] = value
		}
		if aliases := digestAliasesMetadata(payload.Aliases); aliases != "" {
			metadata[digestAliasesMetadataKey] = aliases
		}
		if droppedFacts > 0 {
			metadata[digestDroppedFactsMetadataKey] = strconv.Itoa(droppedFacts)
		}
		if _, err := app.memory.upsertDigest(meetingMemoryKindChannelDigest, group.threadID, string(canonical), metadata); err != nil {
			return meetingMemoryEntry{}, err
		}
		rebuilt = append(rebuilt, group.threadID)
	}
	if len(inputs) == 0 {
		// Stale-only sweep (drained window): the cursor did not move, so no
		// pass artifact is minted.
		return meetingMemoryEntry{}, nil
	}

	passText := "channel digest pass: no channel rebuilt"
	if len(rebuilt) > 0 {
		passText = "channel digest pass: rebuilt " + strings.Join(rebuilt, ", ")
	}
	passMetadata := applyAmbientDerivedScope(map[string]string{
		channelDigestCursorMetadataKey: inputs[len(inputs)-1].ID,
		"threadsRebuilt":               strconv.Itoa(len(rebuilt)),
		"threadsRejected":              strconv.Itoa(len(rejected)),
		"generatedAt":                  now.Format(time.RFC3339),
	}, inputs)
	if len(rejected) > 0 {
		passMetadata["rejectedThreadIds"] = strings.Join(rejected, ",")
	}
	passEntry, _, err := app.memory.appendAmbientEntry(meetingMemoryKindChannelDigestPass, durableTimestampID("channel-digest-pass", now), passText, passMetadata)
	if err != nil {
		return meetingMemoryEntry{}, err
	}
	return passEntry, nil
}

// rebuildStaleChannelDigests is the chassis drainedWork hook: with nothing new
// past the cursor it still rebuilds every thread whose digest was invalidated
// by a delete/edit, from live rows only. The cursor never moves and no pass
// artifact is minted.
func (app *kanbanBoardApp) rebuildStaleChannelDigests(ctx context.Context, apiKey string, responder openAITextResponder) (meetingMemoryEntry, error) {
	return app.produceChannelDigests(ctx, apiKey, nil, responder)
}

// invalidateChannelDigest expires the thread's current channel digest and
// stamps it stale: it drops out of every recall lane at once (the stale
// payload still holds the deleted / pre-edit text) and the next pass rebuilds
// the thread from its live rows without carrying anything forward. Reports
// whether a current digest existed to invalidate.
func (app *kanbanBoardApp) invalidateChannelDigest(threadID string, reason string) bool {
	if app == nil || app.memory == nil {
		return false
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return false
	}
	digest, ok := app.memory.currentDigest(meetingMemoryKindChannelDigest, threadID)
	if !ok {
		return false
	}
	updates := map[string]string{
		relevanceMetadataKey:                relevanceExpired,
		digestCurrentMetadataKey:            digestCurrentFalse,
		channelDigestStaleMetadataKey:       "true",
		channelDigestStaleReasonMetadataKey: strings.TrimSpace(reason),
		channelDigestStaleAtMetadataKey:     time.Now().UTC().Format(time.RFC3339Nano),
	}
	if _, _, err := app.memory.updateEntryWithMetadata(meetingMemoryKindChannelDigest, digest.ID, digest.Text, updates); err != nil {
		log.Errorf("Failed to invalidate channel digest %s for thread %s: %v", digest.ID, threadID, err)
		return false
	}
	return true
}

// staleChannelDigests returns, per thread, the newest channel digest when that
// digest is stale. A rebuild appends a newer, non-stale digest, so a rebuilt
// thread is not stale; a resolved marker (nothing live left) is not either.
func (app *kanbanBoardApp) staleChannelDigests() map[string]meetingMemoryEntry {
	if app == nil || app.memory == nil {
		return nil
	}
	newest := map[string]meetingMemoryEntry{}
	for _, entry := range app.memory.entriesOfKind(meetingMemoryKindChannelDigest, 0) {
		threadID := firstNonEmptyString(strings.TrimSpace(entry.Metadata["threadId"]), digestEntryKey(entry))
		if threadID == "" {
			continue
		}
		newest[threadID] = entry
	}
	stale := map[string]meetingMemoryEntry{}
	for threadID, entry := range newest {
		if strings.EqualFold(strings.TrimSpace(entry.Metadata[channelDigestStaleMetadataKey]), "true") {
			stale[threadID] = entry
		}
	}
	return stale
}

// liveChannelThreadRows returns the thread's recall-visible channel rows the
// ambient service principal may read (the same fence the chassis window
// applies), oldest first, capped to the newest limit rows.
func (app *kanbanBoardApp) liveChannelThreadRows(threadID string, limit int) []meetingMemoryEntry {
	if app == nil || app.memory == nil {
		return nil
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return nil
	}
	principal := app.currentRoomMediaRecallPrincipal(officeRoomID, app.memory.currentMeetingID(officeRoomID))
	rows := make([]meetingMemoryEntry, 0, 8)
	for _, entry := range app.memory.entriesOfKind(meetingMemoryKindTranscript, 0) {
		if !channelSourcedTranscript(entry) || memoryEntryHiddenFromRecall(entry) || strings.TrimSpace(entry.Metadata["threadId"]) != threadID {
			continue
		}
		if principal.Audience != "" && !recallEntryScopeAllowed(entry.Metadata, principal) {
			continue
		}
		rows = append(rows, entry)
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].CreatedAt.Before(rows[j].CreatedAt) })
	if limit > 0 && len(rows) > limit {
		rows = rows[len(rows)-limit:]
	}
	return rows
}

// withStaleChannelDigestRebuilds folds the stale threads into a pass: a stale
// thread already in the window is widened to its live rows (rebuild), one not
// in the window is appended, and a stale thread with nothing live left has
// its marker resolved (the expired digest is already hidden; there is nothing
// to rebuild).
func (app *kanbanBoardApp) withStaleChannelDigestRebuilds(groups []channelDigestGroup) []channelDigestGroup {
	stale := app.staleChannelDigests()
	if len(stale) == 0 {
		return groups
	}
	threadIDs := make([]string, 0, len(stale))
	for threadID := range stale {
		threadIDs = append(threadIDs, threadID)
	}
	sort.Strings(threadIDs)
	index := map[string]int{}
	for position, group := range groups {
		index[group.threadID] = position
	}
	for _, threadID := range threadIDs {
		rows := app.liveChannelThreadRows(threadID, channelDigestRebuildRowCap)
		if len(rows) == 0 {
			marker := stale[threadID]
			if _, _, err := app.memory.updateEntryWithMetadata(meetingMemoryKindChannelDigest, marker.ID, marker.Text, map[string]string{channelDigestStaleMetadataKey: "resolved"}); err != nil {
				log.Errorf("Failed to resolve stale channel digest %s: %v", marker.ID, err)
			}
			continue
		}
		group := channelDigestGroup{threadID: threadID, messages: rows, rebuild: true}
		for _, row := range rows {
			if title := firstNonEmptyString(strings.TrimSpace(row.Metadata["channelTitle"]), strings.TrimSpace(row.Metadata["sourceTitle"])); title != "" {
				group.title = title
				break
			}
		}
		if position, ok := index[threadID]; ok {
			group.title = firstNonEmptyString(groups[position].title, group.title)
			groups[position] = group
			continue
		}
		groups = append(groups, group)
	}
	return groups
}

// channelDigestRecallFence resolves the digest's recall fence from the
// thread's CURRENT state (visibility + member list now), falling back to the
// newest row's stamps when the thread record is unreadable; tenant/room ride
// the newest row either way.
func (app *kanbanBoardApp) channelDigestRecallFence(threadID string, newest meetingMemoryEntry) map[string]string {
	fence := map[string]string{}
	if app != nil && app.memory != nil {
		if entry, ok := app.memory.entryByKindAndID(meetingMemoryKindScoutChat, strings.TrimSpace(threadID)); ok {
			if thread, decoded := decodeScoutChatThreadEntry(entry); decoded {
				for key, value := range channelThreadRecallFence(thread) {
					if value = strings.TrimSpace(value); value != "" {
						fence[key] = value
					}
				}
			}
		}
	}
	if fence["visibility"] == "" {
		for _, key := range []string{"visibility", "ownerEmail", "memberEmails"} {
			if value := strings.TrimSpace(newest.Metadata[key]); value != "" {
				fence[key] = value
			}
		}
	}
	for _, key := range []string{"tenantId", "roomId"} {
		if value := strings.TrimSpace(newest.Metadata[key]); value != "" {
			fence[key] = value
		}
	}
	return fence
}
