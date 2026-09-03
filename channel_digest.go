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
	// over completeness for a very long thread. It is also the chunk size of
	// the first-run history catch-up (withChannelDigestRebuilds).
	channelDigestRebuildRowCap = 4 * defaultChannelDigestMaxMessages
	// First-run catch-up bookkeeping (2026-09-02), carried on the thread's
	// current digest: seedThroughTranscriptId is the oldest-first high-water
	// of folded history, seedPendingRows how many history rows still wait,
	// historyFromTranscriptId the chain's floor — the oldest row a window or
	// rebuild digest folded, before which everything is "history".
	channelDigestSeedThroughMetadataKey = "seedThroughTranscriptId"
	channelDigestSeedPendingMetadataKey = "seedPendingRows"
	channelDigestHistoryEndMetadataKey  = "historyFromTranscriptId"
	// defaultChannelDigestMaxCatchUpThreadsPerTick bounds how many threads the
	// first-run history catch-up may fold in ONE pass. Every chunk is a
	// provider call and the whole pass shares one channelDigestRequestTimeout
	// deadline, so an uncapped catch-up on a store with many pre-boot threads
	// walks the pass into its own timeout — which the runner classifies as a
	// provider hold and, after ambientProviderMaxWindowAttempts, converts into
	// the restart-required circuit this catch-up exists to avoid.
	defaultChannelDigestMaxCatchUpThreadsPerTick = 2
)

// channelDigestMaxCatchUpThreadsPerTick is the per-pass catch-up cap
// (meetingDigestMaxMeetingsPerTick's precedent, same override shape).
func channelDigestMaxCatchUpThreadsPerTick() int {
	return positiveIntEnv("CHANNEL_DIGEST_MAX_CATCHUP_THREADS_PER_TICK", defaultChannelDigestMaxCatchUpThreadsPerTick)
}

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
		// First run (2026-09-02, gen 248): this worker consumes a stream
		// (transcript) that existed long before it did, so the chassis graded
		// its very first boot durable_cursor_ambiguous and it never ran. It
		// opts into the first-run anchor: the cursor starts at the pre-boot
		// transcript, and withChannelDigestRebuilds catches every thread's
		// pre-boot history up oldest-first in bounded chunks over successive
		// passes, carrying the digest forward — nothing pre-boot is skipped,
		// nothing replays through the stream cursor.
		firstRunAnchor: true,
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
	// seeded marks a history catch-up chunk (withChannelDigestRebuilds):
	// messages is the next oldest-first slice of the thread's not-yet-folded
	// history, seedThroughID its last row, seedPending the rows still waiting
	// after it. The prior digest is fed back, so facts carry forward across
	// chunks exactly like ordinary window passes.
	seeded        bool
	seedThroughID string
	seedPending   int
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
	groups := app.withChannelDigestRebuilds(groupTranscriptsByThread(inputs))
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
		// A history catch-up chunk folds OLD rows under a digest that already
		// reaches further forward: its span/day/through stamps must keep the
		// NEWER reach, or a thread mid-catch-up advertises a current digest
		// that claims to end far in the past (recall surfaces, embedding
		// rebuilds and parseDigestSpanMetadata all read exactly these keys).
		// The chunk's own reach stays visible under the seed keys below.
		carryPriorReach := false
		priorThroughID, priorMessageCount := "", 0
		if hasPrior {
			if priorStart, priorEnd, ok := parseDigestSpanMetadata(prior); ok {
				if priorStart.Before(spanStart) {
					spanStart = priorStart
				}
				if group.seeded && priorEnd.After(spanEnd) {
					spanEnd = priorEnd
					carryPriorReach = true
					priorThroughID = strings.TrimSpace(prior.Metadata["throughTranscriptId"])
					if count, convErr := strconv.Atoi(strings.TrimSpace(prior.Metadata["messageCount"])); convErr == nil && count > 0 {
						priorMessageCount = count
					}
				}
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
		if carryPriorReach {
			// from/through/count now describe the chain's CUMULATIVE coverage:
			// oldest row this chunk reached back to, newest row the prior
			// already covered, rows folded across both.
			if priorThroughID != "" {
				metadata["throughTranscriptId"] = priorThroughID
			}
			metadata["messageCount"] = strconv.Itoa(priorMessageCount + len(group.messages))
		}
		if len(inputs) > 0 {
			metadata[channelDigestCursorMetadataKey] = inputs[len(inputs)-1].ID
		}
		switch {
		case group.rebuild:
			metadata["rebuiltFromLiveRows"] = "true"
			// a rebuild deliberately caps at the newest rows (privacy over
			// completeness): declare the older history handled
			metadata[channelDigestHistoryEndMetadataKey] = first.ID
			metadata[channelDigestSeedThroughMetadataKey] = first.ID
			metadata[channelDigestSeedPendingMetadataKey] = "0"
		case group.seeded:
			metadata["seededFromLiveRows"] = "true"
			metadata[channelDigestSeedThroughMetadataKey] = group.seedThroughID
			metadata[channelDigestSeedPendingMetadataKey] = strconv.Itoa(group.seedPending)
			if hasPrior {
				// Stamp the floor the catch-up itself resolved, not just the
				// prior's literal stamp: a legacy rebuild prior carries its
				// floor as fromTranscriptId, and leaving the new digest without
				// one would let the NEXT pass fall through to "no floor" and
				// re-fold everything from the high-water to the newest live row.
				if floor, _ := channelDigestChainFloorID(prior); floor != "" {
					metadata[channelDigestHistoryEndMetadataKey] = floor
				}
			}
			// With no prior at all the chain has no floor yet: nothing but this
			// catch-up has folded a row, so leaving the key unset (rather than
			// stamping this chunk's first row) is what keeps the NEXT chunk
			// reachable — a floor at the chunk's own head would end the
			// catch-up after one chunk.
		default:
			// a window digest: the chain floor is the oldest row a window digest
			// ever folded; catch-up bookkeeping rides along unchanged
			floor := first.ID
			if hasPrior {
				resolved, eligible := channelDigestChainFloorID(prior)
				switch {
				case !eligible:
					// A chain that predates this bookkeeping (window stamps
					// only): its true floor is unknowable here, and everything
					// below the last window it folded is already summarized.
					// Stamping THIS window's head as the floor would turn that
					// whole folded history into "pending" on the next pass — the
					// same replay channelDigestChainFloorID exists to refuse —
					// so the chain stays legacy-shaped and stays caught up.
					floor = ""
				case resolved != "":
					floor = resolved
				}
				for _, key := range []string{channelDigestSeedThroughMetadataKey, channelDigestSeedPendingMetadataKey} {
					if value := strings.TrimSpace(prior.Metadata[key]); value != "" {
						metadata[key] = value
					}
				}
			}
			if floor != "" {
				metadata[channelDigestHistoryEndMetadataKey] = floor
			}
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
	if len(rebuilt) > 0 {
		// A drained-window pass that rebuilt or seeded threads mints no pass
		// artifact (the cursor did not move), so record the success directly:
		// the first pass after the first-run anchor is exactly this shape.
		recordCapabilitySuccess(channelDigestAgentName, now)
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

// channelThreadLiveRows is one scan of the transcript corpus: every thread's
// recall-visible channel rows the ambient service principal may read (the
// same fence the chassis window applies), oldest first, capped to the newest
// limit rows per thread.
func (app *kanbanBoardApp) channelThreadLiveRows(limit int) map[string][]meetingMemoryEntry {
	if app == nil || app.memory == nil {
		return nil
	}
	principal := app.currentRoomMediaRecallPrincipal(officeRoomID, app.memory.currentMeetingID(officeRoomID))
	threads := map[string][]meetingMemoryEntry{}
	for _, entry := range app.memory.entriesOfKind(meetingMemoryKindTranscript, 0) {
		if !channelSourcedTranscript(entry) || memoryEntryHiddenFromRecall(entry) {
			continue
		}
		if principal.Audience != "" && !recallEntryScopeAllowed(entry.Metadata, principal) {
			continue
		}
		threadID := strings.TrimSpace(entry.Metadata["threadId"])
		threads[threadID] = append(threads[threadID], entry)
	}
	for threadID, rows := range threads {
		sort.SliceStable(rows, func(i, j int) bool { return rows[i].CreatedAt.Before(rows[j].CreatedAt) })
		if limit > 0 && len(rows) > limit {
			rows = rows[len(rows)-limit:]
		}
		threads[threadID] = rows
	}
	return threads
}

// withChannelDigestRebuilds folds the work a window cannot key into a pass:
//
//   - stale threads (digest invalidated by a delete/edit): a stale thread
//     already in the window is widened to its live rows (rebuild), one not in
//     the window is appended, and a stale thread with nothing live left has
//     its marker resolved (the expired digest is already hidden; there is
//     nothing to rebuild);
//   - history catch-up (2026-09-02 first-run follow-up): every thread whose
//     live rows include history the digest chain has never folded — rows
//     older than the chain floor (historyFromTranscriptId) and newer than the
//     catch-up high-water (seedThroughTranscriptId) — gets ONE chunk per pass:
//     the OLDEST channelDigestRebuildRowCap pending rows, fed with the prior
//     digest so facts carry forward, and the digest records the new
//     high-water plus the rows still pending. Successive passes (drained or
//     not) repeat until nothing is pending, so no pre-boot row is ever
//     skipped and no thread replays through the stream cursor. Chunks come
//     BEFORE the window groups so a thread's window digest already sees the
//     chunk as its prior. This is what makes the worker's first-run anchor
//     (firstRunAnchor) honest.
func (app *kanbanBoardApp) withChannelDigestRebuilds(groups []channelDigestGroup) []channelDigestGroup {
	stale := app.staleChannelDigests()
	live := app.channelThreadLiveRows(0)
	index := map[string]int{}
	for position, group := range groups {
		index[group.threadID] = position
	}
	titleFrom := func(rows []meetingMemoryEntry) string {
		for _, row := range rows {
			if title := firstNonEmptyString(strings.TrimSpace(row.Metadata["channelTitle"]), strings.TrimSpace(row.Metadata["sourceTitle"])); title != "" {
				return title
			}
		}
		return ""
	}

	staleIDs := make([]string, 0, len(stale))
	for threadID := range stale {
		staleIDs = append(staleIDs, threadID)
	}
	sort.Strings(staleIDs)
	for _, threadID := range staleIDs {
		rows := live[threadID]
		if len(rows) > channelDigestRebuildRowCap {
			rows = rows[len(rows)-channelDigestRebuildRowCap:]
		}
		if len(rows) == 0 {
			marker := stale[threadID]
			if _, _, err := app.memory.updateEntryWithMetadata(meetingMemoryKindChannelDigest, marker.ID, marker.Text, map[string]string{channelDigestStaleMetadataKey: "resolved"}); err != nil {
				log.Errorf("Failed to resolve stale channel digest %s: %v", marker.ID, err)
			}
			continue
		}
		group := channelDigestGroup{threadID: threadID, title: titleFrom(rows), messages: rows, rebuild: true}
		if position, ok := index[threadID]; ok {
			group.title = firstNonEmptyString(groups[position].title, group.title)
			groups[position] = group
			continue
		}
		index[threadID] = len(groups)
		groups = append(groups, group)
	}

	threadIDs := make([]string, 0, len(live))
	for threadID := range live {
		threadIDs = append(threadIDs, threadID)
	}
	sort.Strings(threadIDs)
	maxCatchUp := channelDigestMaxCatchUpThreadsPerTick()
	deferred := 0
	chunks := make([]channelDigestGroup, 0, 2)
	for _, threadID := range threadIDs {
		if _, isStale := stale[threadID]; isStale {
			continue
		}
		rows := live[threadID]
		pending := app.channelDigestPendingHistory(threadID, rows, groups, index)
		if len(pending) == 0 {
			continue
		}
		if len(chunks) >= maxCatchUp {
			// Per-pass cap (the meetingDigestMaxMeetingsPerTick precedent):
			// every chunk is a provider call, and the whole pass runs inside
			// ONE agent.requestTimeout deadline (fireAmbientAgentPass). A store
			// with many un-caught-up threads would otherwise spend calls until
			// the ctx expired, and a context error on a non-empty window is
			// classified as a provider hold — four of those open the
			// restart-required provider circuit. Deferred, never dropped: the
			// remaining threads catch up on the next pass and stay visible as
			// seedPendingRows.
			deferred++
			continue
		}
		chunk := pending
		if len(chunk) > channelDigestRebuildRowCap {
			chunk = chunk[:channelDigestRebuildRowCap]
		}
		remaining := len(pending) - len(chunk)
		log.Infof("%s catching up thread %s history: folding %d row(s), %d still pending", channelDigestAgentName, threadID, len(chunk), remaining)
		chunks = append(chunks, channelDigestGroup{
			threadID: threadID, title: titleFrom(rows), messages: chunk,
			seeded: true, seedThroughID: chunk[len(chunk)-1].ID, seedPending: remaining,
		})
	}
	if deferred > 0 {
		log.Infof("%s deferred history catch-up for %d thread(s) past this pass's cap of %d", channelDigestAgentName, deferred, maxCatchUp)
	}
	if len(chunks) == 0 {
		return groups
	}
	return append(chunks, groups...)
}

// channelDigestChainFloorID resolves the chain floor a prior digest declares —
// the oldest row the digest chain folded, before which everything is unfolded
// history — and whether the thread is eligible for a history catch-up at all.
//
// eligible=false means "already caught up, queue nothing". That is the answer
// for a digest chain that predates this bookkeeping (2026-09-02) and is NOT a
// rebuild: such a digest stamps only fromTranscriptId, which is the first row
// of the LAST window it folded, not the oldest row the chain ever folded.
// Trusting it as the floor re-queued every already-summarized row before it,
// so every legacy thread would have re-summarized its whole history in
// channelDigestRebuildRowCap chunks — one provider call per thread per pass.
// A legacy REBUILD digest is the one exception: a rebuild deliberately caps at
// the newest live rows and declares everything older handled, so its
// fromTranscriptId genuinely IS the chain floor.
//
// floorID=="" with eligible=true means the chain has no floor to honor yet
// (a thread mid-catch-up whose chain has never folded a window row); the
// caller's high-water bound alone decides what is pending.
func channelDigestChainFloorID(prior meetingMemoryEntry) (string, bool) {
	if floorID := strings.TrimSpace(prior.Metadata[channelDigestHistoryEndMetadataKey]); floorID != "" {
		return floorID, true
	}
	if strings.TrimSpace(prior.Metadata[channelDigestSeedThroughMetadataKey]) != "" {
		return "", true
	}
	if !strings.EqualFold(strings.TrimSpace(prior.Metadata["rebuiltFromLiveRows"]), "true") {
		return "", false
	}
	floorID := strings.TrimSpace(prior.Metadata["fromTranscriptId"])
	return floorID, floorID != ""
}

// channelDigestPendingHistory lists, oldest first, the thread's live rows the
// digest chain has never folded: newer than the catch-up high-water, older
// than the chain floor (or than the current window's head, whichever is
// earlier), never a row of the current window. A digest chain that predates
// the bookkeeping is caught up already unless it is a rebuild
// (channelDigestChainFloorID).
func (app *kanbanBoardApp) channelDigestPendingHistory(threadID string, rows []meetingMemoryEntry, groups []channelDigestGroup, index map[string]int) []meetingMemoryEntry {
	if len(rows) == 0 {
		return nil
	}
	ordinal := make(map[string]int, len(rows))
	for at, row := range rows {
		ordinal[row.ID] = at
	}
	// A boundary resolves by ORDINAL position whenever the boundary row is one
	// of this thread's live rows, and falls back to its timestamp only when it
	// is not (deleted, hidden, or older than the per-thread cap). Two rows can
	// carry the SAME timestamp — a bulk import stamps whole seconds — and a
	// purely time-based bound would silently drop the tie, which is exactly the
	// omission this catch-up exists to prevent.
	bounds := func(id string) (afterCut int, beforeCut int, ok bool) {
		id = strings.TrimSpace(id)
		if id == "" {
			return 0, 0, false
		}
		if at, found := ordinal[id]; found {
			return at + 1, at, true
		}
		entry, found := app.memory.entryByKindAndID(meetingMemoryKindTranscript, id)
		if !found {
			return 0, 0, false
		}
		lower, upper := len(rows), len(rows)
		for at, row := range rows {
			if lower == len(rows) && !row.CreatedAt.Before(entry.CreatedAt) {
				lower = at
			}
			if row.CreatedAt.After(entry.CreatedAt) {
				upper = at
				break
			}
		}
		return upper, lower, true
	}
	start, end := 0, len(rows)
	if prior, hasPrior := app.memory.currentDigest(meetingMemoryKindChannelDigest, threadID); hasPrior {
		floorID, eligible := channelDigestChainFloorID(prior)
		if !eligible {
			return nil
		}
		throughID := strings.TrimSpace(prior.Metadata[channelDigestSeedThroughMetadataKey])
		if _, beforeCut, ok := bounds(floorID); ok {
			end = beforeCut
		}
		if afterCut, _, ok := bounds(throughID); ok {
			start = afterCut
		}
	}
	windowIDs := map[string]struct{}{}
	if position, inWindow := index[threadID]; inWindow {
		group := groups[position]
		if group.rebuild {
			return nil
		}
		for _, message := range group.messages {
			windowIDs[message.ID] = struct{}{}
		}
		if len(group.messages) > 0 {
			// the live window's head is the newest row the catch-up may reach:
			// everything from it forward is the stream cursor's business.
			if _, beforeCut, ok := bounds(group.messages[0].ID); ok && beforeCut < end {
				end = beforeCut
			}
		}
	}
	if start >= end {
		return nil
	}
	pending := make([]meetingMemoryEntry, 0, end-start)
	for _, row := range rows[start:end] {
		if _, inWindow := windowIDs[row.ID]; inWindow {
			continue
		}
		pending = append(pending, row)
	}
	return pending
}

// channelDigestSeedPendingRows sums the history rows still waiting for the
// first-run catch-up across every current channel digest (capability
// snapshot seedPendingRows).
func (app *kanbanBoardApp) channelDigestSeedPendingRows() int {
	if app == nil || app.memory == nil {
		return 0
	}
	newest := map[string]meetingMemoryEntry{}
	for _, entry := range app.memory.entriesOfKind(meetingMemoryKindChannelDigest, 0) {
		if memoryEntryHiddenFromRecall(entry) {
			continue
		}
		threadID := firstNonEmptyString(strings.TrimSpace(entry.Metadata["threadId"]), digestEntryKey(entry))
		if threadID != "" {
			newest[threadID] = entry
		}
	}
	total := 0
	for _, entry := range newest {
		if pending, err := strconv.Atoi(strings.TrimSpace(entry.Metadata[channelDigestSeedPendingMetadataKey])); err == nil && pending > 0 {
			total += pending
		}
	}
	return total
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
