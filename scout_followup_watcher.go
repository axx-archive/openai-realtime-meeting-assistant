package main

// Scout follow-up watcher (Wave 11 D12).
//
// Founder intent, verbatim: "Scout should be frequently checking via chron for
// any replies to messages it sent and reading and deciding whether it needs to
// respond, the user can always re-tag them, but it should be smart enough to
// know when to reply and when to not … if others jump in, Scout would need to
// parse the opinions".
//
// An ambient worker on the generic chassis (agent_runner.go): 75 s cadence
// (SCOUT_FOLLOWUP_INTERVAL), the chat seat's provider breaker pauses it like
// every other worker, and its cursor rides the chat-filed transcript rows
// (channel_brain_ingestion.go) through unconsumedEntriesAfter*. Because
// private threads never file transcript rows, each pass ALSO sweeps threads
// with a waiting Packaging Studio intake and recently updated private threads,
// keeping its own per-thread reviewed-through message cursor in one
// scout_followup_state row. Idle-cheap: no window and no recent activity means
// no thread decode and no model call.
//
// Per thread the watcher reads the human messages since its cursor that are
// addressed to Scout — a reply chain rooted at a Scout message, a top-level
// message after Scout's last message in a private thread, or any fresh @scout
// mention — and decides ONE of:
//
//   reply  — a direct question to Scout, an answer that completes a
//            commission brief but needs one more fact, a correction, or
//            several people whose opinions Scout reconciles in one answer;
//   act    — a completed brief launches (private) or proposes (public);
//            "go ahead" / "yes do it" starts held work;
//   silent — people talking to each other, acknowledgements, asides, a turn
//            the synchronous append path already answered, a rate-limited
//            unsolicited reply, or Scout being the last speaker.
//
// A fresh @scout mention always forces a reply. Unsolicited replies are
// limited to one per thread per 10 minutes. Scout never replies to itself.
// Every decision is journaled on the brain run log (thread id, message ids,
// verdict, reason, model provenance) for the inspector.

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	scoutFollowupAgentName              = "scout follow-up"
	defaultScoutFollowupInterval        = 75 * time.Second
	scoutFollowupRequestTimeout         = 60 * time.Second
	meetingMemoryKindScoutFollowupPass  = "scout_followup_pass"
	meetingMemoryKindScoutFollowupState = "scout_followup_state"
	scoutFollowupStateID                = "scout-followup-state"
	scoutFollowupCursorMetadataKey      = "throughFollowupTranscriptId"
	scoutFollowupVia                    = "scout_followup"
	scoutFollowupWorkflow               = "scout_followup"
	scoutFollowupUnsolicitedGap         = 10 * time.Minute
	scoutFollowupMaxThreadsPerPass      = 40
	scoutFollowupRecentThreadWindow     = 48 * time.Hour
	scoutFollowupContextMessages        = 14
	scoutFollowupMaxOutputTokens        = 700
	scoutFollowupMaxTrackedThreads      = 500

	scoutFollowupVerdictReply  = "reply"
	scoutFollowupVerdictAct    = "act"
	scoutFollowupVerdictSilent = "silent"
)

func scoutFollowupAgent() ambientAgentConfig {
	return ambientAgentConfig{
		name:              scoutFollowupAgentName,
		defaultInterval:   defaultScoutFollowupInterval,
		intervalEnv:       "SCOUT_FOLLOWUP_INTERVAL",
		disabledEnv:       "SCOUT_FOLLOWUP_DISABLED",
		backfillEnv:       "SCOUT_FOLLOWUP_BACKFILL",
		providerSeat:      seatChat,
		minBatchEnv:       "SCOUT_FOLLOWUP_MIN_MESSAGES",
		defaultMinBatch:   1,
		maxBatchEnv:       "SCOUT_FOLLOWUP_MAX_MESSAGES",
		defaultMaxBatch:   40,
		inputKind:         meetingMemoryKindTranscript,
		artifactKind:      meetingMemoryKindScoutFollowupPass,
		cursorMetadataKey: scoutFollowupCursorMetadataKey,
		// This worker first ships into workspaces that already have durable
		// channel transcript history. It has never produced a pass at that
		// point, so there is no consumption cursor to reconstruct and the
		// continuity chassis must anchor at the newest pre-boot transcript.
		// scoutFollowupCandidateThreads separately seeds recent public threads
		// through the durable per-thread cursor, so the generic anchor cannot
		// drop a still-fresh pre-release @Scout ask.
		firstRunAnchor: true,
		requestTimeout: scoutFollowupRequestTimeout,
		inputFilter:    channelSourcedTranscript,
		// On-demand contract: a quiet deployment with nothing addressed to
		// Scout is healthy, not stale.
		healthWorkDue: func(*kanbanBoardApp, time.Time) bool { return false },
		produce:       (*kanbanBoardApp).produceScoutFollowupPass,
		drainedWork:   (*kanbanBoardApp).drainedScoutFollowupPass,
	}
}

func (app *kanbanBoardApp) startScoutFollowupWatcher(apiKey string) {
	app.startAmbientAgent(scoutFollowupAgent(), apiKey)
}

// ---------------------------------------------------------------------------
// State: per-thread reviewed-through message ids.
// ---------------------------------------------------------------------------

type scoutFollowupThreadCursor struct {
	MessageID string `json:"messageId"`
	At        string `json:"at"`
}

func (app *kanbanBoardApp) scoutFollowupCursors() map[string]scoutFollowupThreadCursor {
	cursors := map[string]scoutFollowupThreadCursor{}
	if app == nil || app.memory == nil {
		return cursors
	}
	entry, ok := app.memory.entryByKindAndID(meetingMemoryKindScoutFollowupState, scoutFollowupStateID)
	if !ok {
		return cursors
	}
	_ = json.Unmarshal([]byte(entry.Metadata["cursors"]), &cursors)
	return cursors
}

func (app *kanbanBoardApp) saveScoutFollowupCursors(cursors map[string]scoutFollowupThreadCursor) error {
	if app == nil || app.memory == nil {
		return nil
	}
	if len(cursors) > scoutFollowupMaxTrackedThreads {
		type aged struct {
			id string
			at string
		}
		ordered := make([]aged, 0, len(cursors))
		for id, cursor := range cursors {
			ordered = append(ordered, aged{id: id, at: cursor.At})
		}
		sort.Slice(ordered, func(i, j int) bool { return ordered[i].at < ordered[j].at })
		for _, stale := range ordered[:len(ordered)-scoutFollowupMaxTrackedThreads] {
			delete(cursors, stale.id)
		}
	}
	raw, err := json.Marshal(cursors)
	if err != nil {
		return err
	}
	metadata := map[string]string{"cursors": string(raw), "tenantId": canonicalArtifactTenantID(), "visibility": "organization"}
	text := fmt.Sprintf("Scout follow-up watcher state · %d threads", len(cursors))
	if _, exists := app.memory.entryByKindAndID(meetingMemoryKindScoutFollowupState, scoutFollowupStateID); exists {
		_, _, err = app.memory.updateEntryWithMetadata(meetingMemoryKindScoutFollowupState, scoutFollowupStateID, text, metadata)
		return err
	}
	_, _, err = app.memory.appendEntry(meetingMemoryKindScoutFollowupState, scoutFollowupStateID, text, metadata)
	return err
}

// ---------------------------------------------------------------------------
// Pass: discover threads, review each, stamp the cursor.
// ---------------------------------------------------------------------------

func (app *kanbanBoardApp) produceScoutFollowupPass(ctx context.Context, apiKey string, inputs []meetingMemoryEntry, responder openAITextResponder) (meetingMemoryEntry, error) {
	if app == nil || app.memory == nil {
		return meetingMemoryEntry{}, nil
	}
	summary := app.runScoutFollowupSweep(ctx, apiKey, inputs, responder)
	if len(inputs) == 0 {
		return meetingMemoryEntry{}, nil
	}
	// The pass artifact is the transcript cursor; it is appended even when
	// nothing was addressed to Scout so the window never re-bills.
	now := time.Now().UTC()
	entry, _, err := app.memory.appendEntry(meetingMemoryKindScoutFollowupPass, fmt.Sprintf("scout-followup-pass-%d", now.UnixNano()), summary, map[string]string{
		scoutFollowupCursorMetadataKey: inputs[len(inputs)-1].ID,
		"tenantId":                     canonicalArtifactTenantID(),
		"visibility":                   "organization",
	})
	return entry, err
}

// drainedScoutFollowupPass runs when no new channel rows are queued: private
// threads and waiting intakes still get their sweep. Nothing is appended.
func (app *kanbanBoardApp) drainedScoutFollowupPass(ctx context.Context, apiKey string, responder openAITextResponder) (meetingMemoryEntry, error) {
	if app == nil || app.memory == nil {
		return meetingMemoryEntry{}, nil
	}
	app.runScoutFollowupSweep(ctx, apiKey, nil, responder)
	return meetingMemoryEntry{}, nil
}

// scoutFollowupThreadEligible fences the surfaces the watcher may read, model
// and write. A Private Riff is excluded the way every other Wave 11 ambient
// seam excludes it (packagingIntakeTurn bails on thread.Riff): a Riff is one
// person's private space with its own synchronous engine, so an unsolicited
// Scout message there — and shipping its verbatim turns to the chat seat when
// that engine failed — is never wanted.
func scoutFollowupThreadEligible(thread scoutChatThreadRecord) bool {
	return thread.ArchivedAt == "" && thread.Riff == nil
}

// scoutFollowupCandidateThreads unions the four discovery lanes and keeps the
// sweep bounded: current transcript input, waiting intakes, recent public
// threads that have never been cursor-seeded, and recent private threads.
func (app *kanbanBoardApp) scoutFollowupCandidateThreads(inputs []meetingMemoryEntry, now time.Time) []scoutChatThreadRecord {
	seen := map[string]bool{}
	ids := make([]string, 0, 8)
	add := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		ids = append(ids, id)
	}
	for _, entry := range inputs {
		add(entry.Metadata["threadId"])
	}
	for _, record := range app.pendingPackagingIntakes() {
		add(record.ThreadID)
	}
	threads := make([]scoutChatThreadRecord, 0, len(ids))
	decode := func(id string) (scoutChatThreadRecord, bool) {
		entry, ok := app.memory.entryByKindAndID(meetingMemoryKindScoutChat, id)
		if !ok {
			return scoutChatThreadRecord{}, false
		}
		return decodeScoutChatThreadEntry(entry)
	}
	for _, id := range ids {
		if thread, ok := decode(id); ok && scoutFollowupThreadEligible(thread) {
			threads = append(threads, thread)
		}
	}
	// The generic first-run anchor intentionally skips old channel transcript
	// history. Preserve recent addressed public work exactly once by discovering
	// only threads that do not yet have a durable per-thread cursor. This is the
	// worker's bounded catch-up contract: older history remains out of scope,
	// while a deferred decision deliberately stays cursorless and retries.
	cutoff := now.Add(-scoutFollowupRecentThreadWindow)
	cursors := app.scoutFollowupCursors()
	for _, entry := range app.memory.entriesOfKind(meetingMemoryKindScoutChat, 0) {
		if len(threads) >= scoutFollowupMaxThreadsPerPass {
			break
		}
		if seen[entry.ID] || strings.TrimSpace(entry.Metadata["archivedAt"]) != "" {
			continue
		}
		if normalizeScoutChatVisibility(entry.Metadata["visibility"]) != scoutChatVisibilityPublic || cursors[entry.ID].MessageID != "" {
			continue
		}
		updated, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(entry.Metadata["updatedAt"]))
		if err != nil || updated.Before(cutoff) {
			continue
		}
		if thread, ok := decodeScoutChatThreadEntry(entry); ok && scoutFollowupThreadEligible(thread) && scoutFollowupHasRecentMessage(thread, cutoff) {
			seen[entry.ID] = true
			threads = append(threads, thread)
		}
	}
	// Recently updated private threads have no transcript rows, so they always
	// retain their existing metadata-only discovery lane.
	for _, entry := range app.memory.entriesOfKind(meetingMemoryKindScoutChat, 0) {
		if len(threads) >= scoutFollowupMaxThreadsPerPass {
			break
		}
		if seen[entry.ID] || strings.TrimSpace(entry.Metadata["archivedAt"]) != "" {
			continue
		}
		if normalizeScoutChatVisibility(entry.Metadata["visibility"]) == scoutChatVisibilityPublic {
			continue
		}
		updated, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(entry.Metadata["updatedAt"]))
		if err != nil || updated.Before(cutoff) {
			continue
		}
		if thread, ok := decodeScoutChatThreadEntry(entry); ok && scoutFollowupThreadEligible(thread) {
			seen[entry.ID] = true
			threads = append(threads, thread)
		}
	}
	if len(threads) > scoutFollowupMaxThreadsPerPass {
		threads = threads[:scoutFollowupMaxThreadsPerPass]
	}
	return threads
}

// Thread metadata can change without a new conversation turn (for example a
// rename or import). A recent update alone must not resurrect an old ask.
func scoutFollowupHasRecentMessage(thread scoutChatThreadRecord, cutoff time.Time) bool {
	for _, message := range thread.Messages {
		if message.Kind != "message" {
			continue
		}
		if at, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(message.CreatedAt)); err == nil && !at.Before(cutoff) {
			return true
		}
	}
	return false
}

// runScoutFollowupSweep reviews every candidate thread once and returns a
// one-line summary for the pass artifact.
func (app *kanbanBoardApp) runScoutFollowupSweep(ctx context.Context, apiKey string, inputs []meetingMemoryEntry, responder openAITextResponder) string {
	now := time.Now().UTC()
	threads := app.scoutFollowupCandidateThreads(inputs, now)
	if len(threads) == 0 {
		return "Scout follow-up pass · nothing addressed to Scout"
	}
	cursors := app.scoutFollowupCursors()
	replied, acted, silent := 0, 0, 0
	changed := false
	for _, thread := range threads {
		cursor := cursors[thread.ID].MessageID
		stamp := func(messageID string) {
			if messageID == "" {
				return
			}
			cursors[thread.ID] = scoutFollowupThreadCursor{MessageID: messageID, At: now.Format(time.RFC3339Nano)}
			changed = true
		}
		review := app.scoutFollowupReview(thread, cursor, now)
		if len(review.fresh) == 0 {
			// A newly seeded recent thread containing only Scout/system turns
			// still needs a durable floor or it would be decoded every 75 s.
			if cursor == "" && len(thread.Messages) > 0 {
				stamp(thread.Messages[len(thread.Messages)-1].ID)
			}
			continue
		}
		newestFresh := review.fresh[len(review.fresh)-1].ID
		if len(review.addressed) == 0 {
			// Not Scout's conversation: no journal line, no spend.
			stamp(newestFresh)
			continue
		}
		decision := app.scoutFollowupDecide(ctx, apiKey, responder, review)
		app.journalScoutFollowupDecision(review, decision)
		// The cursor moves only past what this pass actually DISPOSED of, in
		// both directions:
		//   · a verdict that is a deferral rather than a resolution
		//     (rate-limited, no model seat, the post failed) decided NOTHING,
		//     so every addressed message stays in view for the next pass;
		//   · a decision that acted on ONE message and returned there (the
		//     intake-answer branch) names it in Through, so the messages that
		//     came after it in the same window stay in view too.
		// Stamping the newest fresh message in either case silently dropped
		// direct questions forever. No loop: the disposed message is behind
		// the new cursor.
		switch {
		case scoutFollowupDecisionDeferred(decision):
			stamp(scoutFollowupCursorBefore(review))
		case decision.Through != "":
			stamp(decision.Through)
		default:
			stamp(newestFresh)
		}
		switch decision.Verdict {
		case scoutFollowupVerdictReply:
			replied++
		case scoutFollowupVerdictAct:
			acted++
		default:
			silent++
		}
	}
	if changed {
		if err := app.saveScoutFollowupCursors(cursors); err != nil {
			log.Errorf("%s: save cursors: %v", scoutFollowupAgentName, err)
		}
	}
	return fmt.Sprintf("Scout follow-up pass · %d threads · replied %d · acted %d · silent %d", len(threads), replied, acted, silent)
}

// scoutFollowupDecisionDeferred reports a verdict that decided NOTHING about
// the addressed messages: the reason is transient (a rate-limit window, a
// missing model seat, a failed post), not a judgement that silence is right.
func scoutFollowupDecisionDeferred(decision scoutFollowupDecision) bool {
	reason := strings.TrimSpace(decision.Reason)
	return reason == "rate_limited" || strings.HasPrefix(reason, "no_model:") || strings.HasPrefix(reason, "post_failed:")
}

// scoutFollowupCursorBefore returns the last fresh message the pass may still
// claim when the decision was deferred: everything before the OLDEST addressed
// message. "" means "leave the cursor where it was".
func scoutFollowupCursorBefore(review scoutFollowupReview) string {
	if len(review.addressed) == 0 {
		return ""
	}
	oldest := review.addressed[0].ID
	held := ""
	for _, message := range review.fresh {
		if message.ID == oldest {
			return held
		}
		held = message.ID
	}
	return held
}

// ---------------------------------------------------------------------------
// Review: which new messages are addressed to Scout?
// ---------------------------------------------------------------------------

type scoutFollowupReview struct {
	thread     scoutChatThreadRecord
	fresh      []scoutChatMessageRecord // human messages after the cursor
	addressed  []scoutChatMessageRecord // the subset aimed at Scout
	forced     bool                     // a fresh @scout mention
	answered   bool                     // Scout already replied after the newest addressed message
	lastScout  int                      // index of Scout's last message, -1 if none
	pending    *packagingIntakeRecord   // the NEWEST waiting intake: the model prompt's subject
	pendingAll []packagingIntakeRecord  // every waiting intake in the thread, oldest first
	authors    []string                 // distinct human authors of addressed messages
}

func scoutFollowupMessageIsScout(message scoutChatMessageRecord) bool {
	if strings.EqualFold(strings.TrimSpace(message.Role), "user") {
		return false
	}
	author := strings.TrimSpace(message.AuthorName)
	return author == "" || strings.EqualFold(author, scoutParticipantName) || strings.TrimSpace(message.PostedOnBehalfOf) == "" && strings.EqualFold(strings.TrimSpace(message.Role), "scout")
}

// scoutFollowupReplyRootsAtScout walks the reply chain up to its root and
// reports whether any ancestor is a Scout message.
func scoutFollowupReplyRootsAtScout(thread scoutChatThreadRecord, message scoutChatMessageRecord) bool {
	seen := map[string]bool{}
	current := message
	for current.ReplyTo != nil {
		parentID := strings.TrimSpace(current.ReplyTo.MessageID)
		if parentID == "" || seen[parentID] {
			return false
		}
		seen[parentID] = true
		index := scoutChatMessageIndex(thread, parentID)
		if index < 0 {
			// Deleted parent: the snapshot still names the author.
			return strings.EqualFold(strings.TrimSpace(current.ReplyTo.AuthorName), scoutParticipantName)
		}
		parent := thread.Messages[index]
		if scoutFollowupMessageIsScout(parent) {
			return true
		}
		current = parent
	}
	return false
}

// scoutFollowupAnswersAPendingIntake reports that this message could be the
// requester's untethered answer to SOME intake still waiting in the thread —
// not merely the newest one. It mirrors packagingIntakeAnswerBindingFor's
// implicit case (same author, after that record's own ask), which is what
// finally decides WHICH record the message lands on; this is only the gate
// that keeps the message in view for that decision.
func scoutFollowupAnswersAPendingIntake(thread scoutChatThreadRecord, pending []packagingIntakeRecord, author string, index int) bool {
	if author == "" {
		return false
	}
	for _, record := range pending {
		if author != record.RequesterEmail {
			continue
		}
		if askIndex := scoutChatMessageIndex(thread, record.AskMessageID); askIndex >= 0 && index <= askIndex {
			continue
		}
		return true
	}
	return false
}

// scoutFollowupScoutSpokeTo reports that a Scout message after newestAddressed
// is plausibly an answer TO it. A Scout message that names an OLDER message as
// its cause, or is threaded under one, answered that earlier turn: the proposal
// card this watcher posts for an intake answer is threaded under the answer, so
// it is not a reply to the question somebody typed a second later. Counting it
// as one made the next pass call that question "already answered" and stamp
// past it — the drop the cursor fix exists to prevent.
func scoutFollowupScoutSpokeTo(thread scoutChatThreadRecord, newestAddressed int) bool {
	for index := len(thread.Messages) - 1; index > newestAddressed; index-- {
		message := thread.Messages[index]
		if !scoutFollowupMessageIsScout(message) {
			continue
		}
		target := strings.TrimSpace(message.CausedByMessageID)
		if target == "" && message.ReplyTo != nil {
			target = strings.TrimSpace(message.ReplyTo.MessageID)
		}
		if target != "" {
			if at := scoutChatMessageIndex(thread, target); at >= 0 && at < newestAddressed {
				continue
			}
		}
		return true
	}
	return false
}

func (app *kanbanBoardApp) scoutFollowupReview(thread scoutChatThreadRecord, cursorMessageID string, now time.Time) scoutFollowupReview {
	review := scoutFollowupReview{thread: thread, lastScout: -1}
	start := 0
	if cursorMessageID != "" {
		if index := scoutChatMessageIndex(thread, cursorMessageID); index >= 0 {
			start = index + 1
		}
	} else {
		// First sight of a thread: only look at messages after Scout's last
		// message so a long history is never replayed.
		for index := len(thread.Messages) - 1; index >= 0; index-- {
			if scoutFollowupMessageIsScout(thread.Messages[index]) {
				start = index + 1
				break
			}
		}
	}
	for index, message := range thread.Messages {
		if scoutFollowupMessageIsScout(message) {
			review.lastScout = index
		}
	}
	// EVERY intake still waiting in this thread, oldest first. Deciding what
	// is "addressed" from the newest pending record alone dropped an
	// untethered answer to an older intake whenever a second commission had
	// been started in the same channel: the message was never considered, and
	// the sweep then stamped the cursor past it. review.pending (the newest)
	// stays only as the model prompt's and the journal row's subject.
	for _, record := range app.packagingIntakeRecordsForThread(thread.ID) {
		if record.pending() {
			review.pendingAll = append(review.pendingAll, record)
		}
	}
	if len(review.pendingAll) > 0 {
		newestPending := review.pendingAll[len(review.pendingAll)-1]
		review.pending = &newestPending
	}
	private := scoutChatThreadVisibility(thread) != scoutChatVisibilityPublic
	authors := map[string]bool{}
	newestAddressed := -1
	for index := start; index < len(thread.Messages); index++ {
		message := thread.Messages[index]
		if message.Kind != "message" || !strings.EqualFold(strings.TrimSpace(message.Role), "user") {
			continue
		}
		if cursorMessageID == "" && !private {
			if at, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(message.CreatedAt)); err == nil && at.Before(now.Add(-scoutFollowupRecentThreadWindow)) {
				continue
			}
		}
		author := normalizeAccountEmail(message.AuthorEmail)
		if author == "" || !scoutChatThreadAllowsViewer(thread, author) || accountIsDisabled(author) {
			// Hard stop: a message without a human author (Scout's own, a relay
			// without disclosure) or from someone no longer in the fence.
			continue
		}
		review.fresh = append(review.fresh, message)
		mentions := scoutChatMessageMentionsScout(message)
		addressed := mentions || scoutFollowupReplyRootsAtScout(thread, message) ||
			(private && review.lastScout >= 0 && index > review.lastScout && message.ReplyTo == nil) ||
			scoutFollowupAnswersAPendingIntake(thread, review.pendingAll, author, index)
		if !addressed {
			continue
		}
		review.addressed = append(review.addressed, message)
		review.forced = review.forced || mentions
		authors[author] = true
		newestAddressed = index
	}
	for author := range authors {
		review.authors = append(review.authors, author)
	}
	sort.Strings(review.authors)
	if newestAddressed >= 0 && review.lastScout > newestAddressed && scoutFollowupScoutSpokeTo(thread, newestAddressed) {
		// The synchronous append path (or a prior pass) already answered.
		review.answered = true
	}
	_ = now
	return review
}

// ---------------------------------------------------------------------------
// Decide: reply / act / silent.
// ---------------------------------------------------------------------------

type scoutFollowupDecision struct {
	Verdict    string
	Reason     string
	Reply      string
	Provenance string // deterministic | model:<model> | model:<model>:fallback
	ReplyID    string
	Forced     bool
	// Through is the newest message this decision actually DISPOSED of, when
	// that is not the whole window. The intake branch binds ONE message and
	// returns on it; everything the same window carried after that message is
	// still undecided, so the sweep stamps the cursor here and reconsiders the
	// rest next pass. Empty means the decision covered the whole window.
	Through string
}

var scoutFollowupAcknowledgements = []string{"thanks", "thank you", "thx", "ty", "ok", "okay", "got it", "cool", "nice", "great", "perfect", "sounds good", "will do", "noted", "👍", "🙏", "👌", "❤️", "lol", "haha", "sure", "yep", "yup", "k", "ack", "roger", "cheers", "awesome", "love it", "amazing", "makes sense"}

var scoutFollowupGoAheads = []string{"go ahead", "yes do it", "do it", "start it", "kick it off", "ship it", "run it", "let's go", "lets go", "proceed", "green light", "make it so", "yes please", "go for it", "start"}

var scoutFollowupCorrections = []string{"actually", "instead", "not that", "that's wrong", "thats wrong", "correction", "i meant", "change that", "no,", "no -", "wrong", "scratch that", "rather than", "swap"}

func scoutFollowupNormalized(text string) string {
	lower := strings.ToLower(strings.TrimSpace(text))
	lower = strings.TrimRight(lower, ".!,;: ")
	return lower
}

func scoutFollowupIsAcknowledgement(text string) bool {
	lower := scoutFollowupNormalized(text)
	if lower == "" {
		return true
	}
	if len(chatAgentWorkWords(lower)) > 6 {
		return false
	}
	for _, phrase := range scoutFollowupAcknowledgements {
		if lower == phrase || strings.HasPrefix(lower, phrase+" ") || strings.HasPrefix(lower, phrase+",") {
			rest := strings.TrimSpace(strings.TrimPrefix(lower, phrase))
			rest = strings.TrimLeft(rest, ", ")
			if rest == "" || rest == "scout" || rest == "@scout" || scoutFollowupIsAcknowledgementTail(rest) {
				return true
			}
		}
	}
	return false
}

// scoutFollowupPraiseTails complete a two-word compliment — "nice work",
// "great job", "cool stuff". Whole-tail only: "nice work on the pricing memo"
// carries a subject and is not an acknowledgement.
var scoutFollowupPraiseTails = map[string]bool{"work": true, "job": true, "one": true, "stuff": true, "effort": true,
	"catch": true, "find": true, "call": true, "thing": true}

func scoutFollowupIsAcknowledgementTail(rest string) bool {
	if scoutFollowupPraiseTails[rest] {
		return true
	}
	for _, phrase := range scoutFollowupAcknowledgements {
		if rest == phrase || rest == phrase+" scout" || rest == phrase+" @scout" {
			return true
		}
	}
	return false
}

func scoutFollowupIsGoAhead(text string) bool {
	lower := scoutFollowupNormalized(text)
	lower = strings.TrimPrefix(lower, "@scout ")
	lower = strings.TrimPrefix(lower, "scout ")
	if len(chatAgentWorkWords(lower)) > 8 {
		return false
	}
	for _, phrase := range scoutFollowupGoAheads {
		if lower == phrase || strings.HasPrefix(lower, phrase) || strings.HasSuffix(lower, phrase) {
			return true
		}
	}
	return false
}

func scoutFollowupIsCorrection(text string) bool {
	lower := scoutFollowupNormalized(text)
	for _, phrase := range scoutFollowupCorrections {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	return false
}

func scoutFollowupIsQuestion(text string) bool {
	return strings.Contains(text, "?")
}

// scoutFollowupLastUnsolicitedReplyAt reads the rate-limit clock off the
// thread itself: Scout's newest follow-up reply that was NOT forced.
func scoutFollowupLastUnsolicitedReplyAt(thread scoutChatThreadRecord) (time.Time, bool) {
	for index := len(thread.Messages) - 1; index >= 0; index-- {
		message := thread.Messages[index]
		if message.Via != scoutFollowupVia || message.IntentOutcome == "forced" {
			continue
		}
		if at, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(message.CreatedAt)); err == nil {
			return at, true
		}
	}
	return time.Time{}, false
}

func scoutFollowupAuthorName(thread scoutChatThreadRecord, email string) string {
	for index := len(thread.Messages) - 1; index >= 0; index-- {
		if normalizeAccountEmail(thread.Messages[index].AuthorEmail) == email && strings.TrimSpace(thread.Messages[index].AuthorName) != "" {
			return strings.TrimSpace(thread.Messages[index].AuthorName)
		}
	}
	return firstNonEmptyString(participantNameForEmail(email), email)
}

// scoutFollowupDecide is the per-thread decision. Deterministic rules first
// (they cost nothing and are the hard fences); the bounded seatChat call only
// decides the genuinely ambiguous single-reply case and writes the reply text
// when a reply is due.
func (app *kanbanBoardApp) scoutFollowupDecide(ctx context.Context, apiKey string, responder openAITextResponder, review scoutFollowupReview) scoutFollowupDecision {
	decision := scoutFollowupDecision{Verdict: scoutFollowupVerdictSilent, Provenance: "deterministic", Forced: review.forced}
	thread := review.thread
	if len(review.addressed) == 0 {
		decision.Reason = "not_addressed"
		return decision
	}
	newest := review.addressed[len(review.addressed)-1]
	if review.answered && !review.forced {
		decision.Reason = "already_answered"
		return decision
	}
	if review.answered && review.forced {
		// A fresh @scout the append path already answered synchronously
		// (public channels answer mentions inline). Forcing again would
		// double-post.
		decision.Reason = "already_answered_mention"
		return decision
	}
	// Answers to a waiting Packaging Studio intake → act (complete / launch /
	// propose) or reply with the one remaining question.
	if len(review.pendingAll) > 0 {
		for _, message := range review.addressed {
			// The message must be BOUND to a waiting intake before a word of
			// it touches that brief: an unrelated @scout ask elsewhere in the
			// channel is addressed to Scout but answers nobody's questions,
			// and it used to complete (and launch) someone else's commission.
			// acceptImplicit is true here because a channel asker's untethered
			// answer is exactly what this watcher exists to pick up.
			record, bound := app.packagingIntakeAnswerTarget(thread, message, nil, true)
			if !bound {
				continue
			}
			user := accountStore().findUser(normalizeAccountEmail(message.AuthorEmail))
			if user == nil {
				user = &userAccount{Email: message.AuthorEmail, Name: message.AuthorName}
			}
			response, handled := app.packagingIntakeAnswerTurn(ctx, user, thread, message, &record, nil, app.scoutFollowupCommitter(thread))
			if handled {
				decision.Verdict = scoutFollowupVerdictAct
				decision.Reason = "intake_answer"
				// This pass disposed of THIS message and returns on it. Any
				// later addressed message in the same window — a direct
				// question, a "fold in the pricing memo" correction — has not
				// been decided, so the cursor must not run past it.
				decision.Through = message.ID
				if len(record.OpenQuestions) > 0 {
					decision.Verdict = scoutFollowupVerdictReply
					decision.Reason = "intake_follow_up_question"
				}
				if answer, ok := response["answer"].(scoutChatMessageRecord); ok {
					decision.ReplyID = answer.ID
					decision.Reply = answer.Text
				}
				return decision
			}
		}
	}
	allAcknowledgements := true
	anyQuestion, anyCorrection, anyGoAhead := false, false, false
	for _, message := range review.addressed {
		if !scoutFollowupIsAcknowledgement(message.Text) {
			allAcknowledgements = false
		}
		anyQuestion = anyQuestion || scoutFollowupIsQuestion(message.Text)
		anyCorrection = anyCorrection || scoutFollowupIsCorrection(message.Text)
		anyGoAhead = anyGoAhead || scoutFollowupIsGoAhead(message.Text)
	}
	if allAcknowledgements && !review.forced {
		decision.Reason = "acknowledgement"
		return decision
	}
	// "go ahead" on a completed-but-unlaunched private brief → act.
	if anyGoAhead {
		for _, record := range app.packagingIntakeRecordsForThread(thread.ID) {
			if oneOf(record.Status, packagingIntakeStatusBriefComplete, packagingIntakeStatusFailed) && scoutChatThreadVisibility(thread) != scoutChatVisibilityPublic &&
				normalizeAccountEmail(newest.AuthorEmail) == record.RequesterEmail {
				user := accountStore().findUser(record.RequesterEmail)
				if user == nil {
					user = &userAccount{Email: record.RequesterEmail, Name: record.RequesterName}
				}
				launch := record
				response, handled := app.packagingIntakeLaunch(ctx, thread, newest, user, &launch, app.scoutFollowupCommitter(thread))
				if handled {
					decision.Verdict = scoutFollowupVerdictAct
					decision.Reason = "go_ahead"
					if answer, ok := response["answer"].(scoutChatMessageRecord); ok {
						decision.ReplyID = answer.ID
						decision.Reply = answer.Text
					}
					return decision
				}
			}
		}
	}
	// Rate limit: one unsolicited reply per thread per 10 minutes. A fresh
	// @scout is never unsolicited.
	if !review.forced {
		if last, ok := scoutFollowupLastUnsolicitedReplyAt(thread); ok && time.Since(last) < scoutFollowupUnsolicitedGap {
			decision.Reason = "rate_limited"
			return decision
		}
	}
	multi := len(review.authors) > 1
	needsModel := false
	switch {
	case review.forced:
		decision.Verdict, decision.Reason = scoutFollowupVerdictReply, "mentioned"
	case multi:
		decision.Verdict, decision.Reason = scoutFollowupVerdictReply, "reconcile_opinions"
	case anyQuestion:
		decision.Verdict, decision.Reason = scoutFollowupVerdictReply, "direct_question"
	case anyCorrection:
		decision.Verdict, decision.Reason = scoutFollowupVerdictReply, "correction"
	case review.lastScout >= 0 && len(review.authors) == 1:
		// Scout was the last speaker and one person replied: it MAY reply —
		// the model decides whether the reply adds anything.
		needsModel = true
	default:
		decision.Reason = "aside"
		return decision
	}
	modelDecision, provenance, err := app.scoutFollowupModelDecision(ctx, apiKey, responder, review, needsModel)
	if err != nil {
		if needsModel {
			decision.Verdict = scoutFollowupVerdictSilent
			decision.Reason = "no_model:" + err.Error()
			return decision
		}
		decision.Reply = scoutFollowupDeterministicReply(review, multi)
		decision.Provenance = "deterministic:" + err.Error()
	} else {
		decision.Provenance = provenance
		if needsModel {
			if modelDecision.Verdict != scoutFollowupVerdictReply {
				decision.Verdict = scoutFollowupVerdictSilent
				decision.Reason = "model_silent:" + firstNonEmptyString(strings.TrimSpace(modelDecision.Reason), "nothing to add")
				return decision
			}
			decision.Verdict, decision.Reason = scoutFollowupVerdictReply, "model_reply:"+firstNonEmptyString(strings.TrimSpace(modelDecision.Reason), "useful follow-up")
		}
		decision.Reply = strings.TrimSpace(modelDecision.Reply)
		if decision.Reply == "" {
			decision.Reply = scoutFollowupDeterministicReply(review, multi)
		}
	}
	if decision.Verdict != scoutFollowupVerdictReply {
		return decision
	}
	posted, postErr := app.postScoutFollowupReply(thread, newest, decision.Reply, review.forced)
	if postErr != nil {
		decision.Verdict = scoutFollowupVerdictSilent
		decision.Reason = "post_failed:" + postErr.Error()
		return decision
	}
	decision.ReplyID = posted.ID
	return decision
}

// scoutFollowupDeterministicReply is the keyless / model-failure text: honest
// about what Scout could and could not do, and for several voices a plain
// reconciliation with the flip offer.
func scoutFollowupDeterministicReply(review scoutFollowupReview, multi bool) string {
	thread := review.thread
	if multi {
		lines := make([]string, 0, len(review.authors)+1)
		positions := map[string]string{}
		for _, message := range review.addressed {
			email := normalizeAccountEmail(message.AuthorEmail)
			positions[email] = trimForStorage(compactAssistantLine(message.Text), 120)
		}
		first := ""
		for _, email := range review.authors {
			name := scoutFollowupAuthorName(thread, email)
			lines = append(lines, name+": "+positions[email])
			if first == "" {
				first = name
			}
		}
		return strings.Join(lines, " · ") + " — I'll go with " + first + "'s direction first and keep the other point in; say the word to flip."
	}
	newest := review.addressed[len(review.addressed)-1]
	name := firstNonEmptyString(strings.TrimSpace(newest.AuthorName), scoutFollowupAuthorName(thread, normalizeAccountEmail(newest.AuthorEmail)))
	return "@" + strings.Fields(name)[0] + " — I read this, but I can't answer it properly right now (my model seat is unavailable). Re-tag me in a bit and I'll pick it up."
}

// ---------------------------------------------------------------------------
// Model: one bounded seatChat call.
// ---------------------------------------------------------------------------

type scoutFollowupModelVerdict struct {
	Verdict string `json:"verdict"`
	Reason  string `json:"reason"`
	Reply   string `json:"reply"`
}

func scoutFollowupModelSchema() *openAIJSONSchema {
	return &openAIJSONSchema{
		Name: "scout_followup_v1",
		Schema: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"verdict", "reason", "reply"},
			"properties": map[string]any{
				"verdict": map[string]any{"type": "string", "enum": []string{scoutFollowupVerdictReply, scoutFollowupVerdictSilent}},
				"reason":  map[string]any{"type": "string"},
				"reply":   map[string]any{"type": "string"},
			},
		},
	}
}

func scoutFollowupModelInstructions(decideOnly bool) string {
	lines := []string{
		"You are Scout, a teammate in Bonfire's team chat, deciding whether to follow up on replies to your own earlier message.",
		"Reply only when it adds something: a direct question to you, a correction you should acknowledge and apply, an answer that completes work you asked about, or several people with different opinions — then reconcile them ONCE (say who wants what, pick a direction, keep the other thread alive, and offer to flip: e.g. \"Tim wants it data-led, Ana prefers narrative — I'll lead with data and keep a narrative thread; say the word to flip\").",
		"Stay silent for acknowledgements, asides, and people talking to each other.",
		"Never launch or promise work you cannot do here; never invent facts; keep the reply to one to three short sentences in a natural teammate voice, addressing the person by @FirstName.",
	}
	if decideOnly {
		lines = append(lines, "Scout was the last speaker and exactly one person replied without a question: reply ONLY if silence would leave them hanging.")
	} else {
		lines = append(lines, "A reply IS due for this thread; write it. verdict must be reply.")
	}
	lines = append(lines, "Output strict JSON: {verdict: reply|silent, reason, reply}.")
	return strings.Join(lines, " ")
}

func scoutFollowupModelInput(review scoutFollowupReview) string {
	thread := review.thread
	builder := strings.Builder{}
	builder.WriteString("Thread: #" + firstNonEmptyString(strings.TrimSpace(thread.Title), "chat") + " (" + scoutChatThreadVisibility(thread) + ")\n")
	fresh := map[string]bool{}
	for _, message := range review.addressed {
		fresh[message.ID] = true
	}
	start := len(thread.Messages) - scoutFollowupContextMessages
	if start < 0 {
		start = 0
	}
	builder.WriteString("Recent messages (oldest first; NEW marks what arrived since Scout last looked):\n")
	for _, message := range thread.Messages[start:] {
		if message.Kind != "message" || strings.TrimSpace(message.Text) == "" {
			continue
		}
		author := firstNonEmptyString(strings.TrimSpace(message.AuthorName), scoutParticipantName)
		marker := ""
		if fresh[message.ID] {
			marker = "NEW "
		}
		reply := ""
		if message.ReplyTo != nil {
			reply = " (replying to " + firstNonEmptyString(strings.TrimSpace(message.ReplyTo.AuthorName), "someone") + ")"
		}
		builder.WriteString(marker + author + reply + ": " + trimForStorage(compactAssistantLine(message.Text), 400) + "\n")
	}
	if review.pending != nil {
		builder.WriteString("Scout is waiting on " + firstNonEmptyString(review.pending.WaitingOnName, review.pending.WaitingOn) + " for a Packaging Studio brief (" + firstNonEmptyString(review.pending.Kind, "unclear kind") + ").\n")
	}
	if len(review.authors) > 1 {
		builder.WriteString(fmt.Sprintf("%d different people replied; reconcile their opinions in one answer.\n", len(review.authors)))
	}
	return builder.String()
}

func (app *kanbanBoardApp) scoutFollowupModelDecision(ctx context.Context, apiKey string, responder openAITextResponder, review scoutFollowupReview, decideOnly bool) (scoutFollowupModelVerdict, string, error) {
	if responder == nil {
		responder = createOpenAITextResponse
	}
	if strings.TrimSpace(apiKey) == "" {
		return scoutFollowupModelVerdict{}, "", fmt.Errorf("provider_not_configured")
	}
	if _, paused := providerBreakers.paused(providerOpenAI, seatChat); paused {
		return scoutFollowupModelVerdict{}, "", fmt.Errorf("breaker_open")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	capture := &providerCallProvenanceCapture{}
	callCtx := withProviderCallProvenanceCapture(ctx, capture)
	output, err := responder(callCtx, apiKey, openAITextRequest{
		Model:           scoutChatModel(),
		Seat:            seatChat,
		Workflow:        scoutFollowupWorkflow,
		Instructions:    scoutFollowupModelInstructions(decideOnly),
		Input:           scoutFollowupModelInput(review),
		ReasoningEffort: "low",
		Verbosity:       "low",
		MaxOutputTokens: scoutFollowupMaxOutputTokens,
		JSONSchema:      scoutFollowupModelSchema(),
		ValidateOutput: func(text string) error {
			var payload scoutFollowupModelVerdict
			return json.Unmarshal([]byte(strings.TrimSpace(text)), &payload)
		},
	})
	if err != nil {
		return scoutFollowupModelVerdict{}, "", fmt.Errorf("model_error")
	}
	var verdict scoutFollowupModelVerdict
	if json.Unmarshal([]byte(strings.TrimSpace(output)), &verdict) != nil {
		return scoutFollowupModelVerdict{}, "", fmt.Errorf("model_invalid")
	}
	provenance := "model:" + scoutChatModel()
	if stamped, ok := capture.snapshot(); ok {
		provenance = "model:" + firstNonEmptyString(strings.TrimSpace(stamped.Model), scoutChatModel())
		if stamped.FallbackUsed {
			provenance += ":fallback"
		}
	}
	return verdict, provenance, nil
}

// ---------------------------------------------------------------------------
// Effects: post the reply, commit intake cards, journal.
// ---------------------------------------------------------------------------

// scoutFollowupCommitter adapts the intake's committer contract to a Scout-
// initiated commit: the human message is already persisted, so only Scout's
// cards are appended, under the thread owner's (always-allowed) identity.
func (app *kanbanBoardApp) scoutFollowupCommitter(thread scoutChatThreadRecord) scoutChatMessageCommitter {
	return func(messages ...scoutChatMessageRecord) (scoutChatThreadRecord, error) {
		scoutMessages := make([]scoutChatMessageRecord, 0, len(messages))
		for _, message := range messages {
			if strings.EqualFold(strings.TrimSpace(message.Role), "user") {
				continue
			}
			if message.Via == "" || message.Via == packagingIntakeVia {
				message.Via = scoutFollowupVia
			}
			scoutMessages = append(scoutMessages, message)
		}
		if len(scoutMessages) == 0 {
			return thread, nil
		}
		saved, err := app.commitScoutChatThreadMessages(thread.OwnerEmail, thread.ID, scoutMessages...)
		if err != nil {
			return saved, err
		}
		for _, message := range scoutMessages {
			app.notifyScoutChatTargets(saved, message)
		}
		return saved, nil
	}
}

func (app *kanbanBoardApp) postScoutFollowupReply(thread scoutChatThreadRecord, target scoutChatMessageRecord, text string, forced bool) (scoutChatMessageRecord, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return scoutChatMessageRecord{}, fmt.Errorf("empty reply")
	}
	if scoutFollowupMessageIsScout(target) {
		return scoutChatMessageRecord{}, fmt.Errorf("refusing to reply to Scout's own message")
	}
	if thread.ArchivedAt != "" {
		return scoutChatMessageRecord{}, fmt.Errorf("thread is archived")
	}
	if thread.Riff != nil {
		return scoutChatMessageRecord{}, fmt.Errorf("refusing to post an unsolicited reply into a Riff")
	}
	replyTo := &scoutChatReplyRef{
		MessageID: target.ID, RootMessageID: scoutChatMessageReplyRootID(thread, target),
		AuthorName: strings.TrimSpace(target.AuthorName), AuthorEmail: normalizeAccountEmail(target.AuthorEmail), Text: trimForStorage(target.Text, 280),
	}
	message := scoutChatMessageRecord{
		ID:                "scout-chat-message-followup-" + sha256Hex([]byte("scout-followup-reply/v1\x00" + thread.ID + "\x00" + target.ID))[:24],
		Kind:              "message",
		Role:              "scout",
		AuthorName:        scoutParticipantName,
		Via:               scoutFollowupVia,
		Text:              text,
		CreatedAt:         time.Now().UTC().Format(time.RFC3339Nano),
		ReplyTo:           replyTo,
		CausedByMessageID: target.ID,
	}
	if forced {
		message.IntentOutcome = "forced"
	}
	if scoutChatMessageIndex(thread, message.ID) >= 0 {
		return scoutChatMessageRecord{}, fmt.Errorf("already replied to this message")
	}
	saved, err := app.commitScoutChatThreadMessages(thread.OwnerEmail, thread.ID, message)
	if err != nil {
		return scoutChatMessageRecord{}, err
	}
	app.notifyScoutChatTargets(saved, message)
	return message, nil
}

// journalScoutFollowupDecision writes one run_log line per decided thread:
// thread id, the message ids reviewed, verdict, reason and model provenance.
func (app *kanbanBoardApp) journalScoutFollowupDecision(review scoutFollowupReview, decision scoutFollowupDecision) {
	if app == nil || app.memory == nil || len(review.addressed) == 0 {
		return
	}
	ids := make([]string, 0, len(review.addressed))
	for _, message := range review.addressed {
		ids = append(ids, message.ID)
	}
	newest := review.addressed[len(review.addressed)-1]
	text := fmt.Sprintf("Scout follow-up — #%s: %s (%s). Reviewed %d message(s) from %d person(s); %s.",
		firstNonEmptyString(strings.TrimSpace(review.thread.Title), review.thread.ID), decision.Verdict, decision.Reason, len(review.addressed), len(review.authors), decision.Provenance)
	if decision.Reply != "" {
		text += " Reply: " + trimForStorage(compactAssistantLine(decision.Reply), 200)
	}
	metadata := map[string]string{
		"workflow":   scoutFollowupWorkflow,
		"threadId":   review.thread.ID,
		"messageIds": strings.Join(ids, ","),
		"messageId":  newest.ID,
		"verdict":    decision.Verdict,
		"reason":     decision.Reason,
		"provenance": decision.Provenance,
		"forced":     fmt.Sprintf("%t", decision.Forced),
		"status":     decision.Verdict,
		"mode":       "scout_followup",
	}
	if decision.ReplyID != "" {
		metadata["replyMessageId"] = decision.ReplyID
	}
	if review.pending != nil {
		metadata["packagingIntake"] = review.pending.ID
	}
	// The row quotes thread text (title, reply excerpt): it inherits the
	// thread's exact recall fence — private → private + owner, member
	// channels → project + members, office channels → organization — so
	// recall/search can never surface a private thread's brief to another
	// member. Same vocabulary channelBrainMetadata stamps on ingestion rows.
	for key, value := range channelThreadRecallFence(review.thread) {
		metadata[key] = value
	}
	metadata["tenantId"] = canonicalArtifactTenantID()
	id := "run-log-scout-followup-" + sha256Hex([]byte(review.thread.ID + "\x00" + newest.ID + "\x00" + decision.Verdict))[:24]
	if _, _, err := app.memory.appendRunLog(id, text, metadata); err != nil {
		log.Errorf("%s: journal: %v", scoutFollowupAgentName, err)
	}
}
