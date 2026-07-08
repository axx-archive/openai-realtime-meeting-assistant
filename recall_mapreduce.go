package main

// On-demand map-reduce briefing (Track-2 Wave 6) — the freshness/coverage
// fallback that replaces the silent 8-keyword-hit collapse when the rollup
// tiers do not cover a queried range (pre-backfill window, digest producers
// disabled, or history older than the backfill baseline).
//
// Amendment A6 (share the chunking/packing/budget code with the digest
// producers; precomputed digests act as the cache of one shared path) shapes
// the whole design:
//
//	MAP    one bounded model call (or chunk-carried sequence) per in-range
//	       meeting produces a meetingDigestPayload using the PRODUCER'S OWN
//	       schema, instructions, parser, and clamps (meetingDigestInstructions
//	       / parseMeetingDigest / clampMeetingDigestPayload) — never a novel
//	       summary format. A meeting whose CURRENT stored digest already
//	       covers its in-range material is a CACHE HIT: its digest is used
//	       as-is, zero model calls (write-through in the read direction).
//	REDUCE deterministic, zero model calls (amendment A2 doctrine — records
//	       are regrouped, never re-summarized): the mapped payloads ride the
//	       SAME foldDayDigest + composeBriefingFromDigests path the stored
//	       digests ride, with active decisions injected verbatim.
//
// Selection scans store entries by CreatedAt — NOT the meetings directory —
// so ranges older than the 200-record meetingStoreCap and the legacy
// null-meetingId entries (grouped per local day via digestKeyForBrain's
// synthetic key) are still reachable; the directory only contributes titles.
//
// Mapped payloads are NEVER persisted: an ad-hoc meeting_digest appended
// without the producer's throughBrainId cursor would be read back as the
// newest artifact by unconsumedEntriesAfter and corrupt the ambient
// producer's resume position (the position fallback treats a cursor-less
// artifact as "consumed through my own position"). They live only inside the
// one briefing being composed — the ledger_state prompt-only precedent.

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	// mapReduceMaxMeetings bounds total model spend per briefing.
	mapReduceMaxMeetings = 12
	// mapReduceChunkBudgetChars bounds ONE map call's source material (chars,
	// ~4K tokens); a meeting with more material chunks sequentially with the
	// producer's prior-digest continuity carry.
	mapReduceChunkBudgetChars = 16000
	// mapReduceMaxChunksPerMeeting caps the sequential carry so a marathon
	// meeting cannot monopolize the pass; the newest chunks win (a briefing
	// biases recent material once the budget is hit).
	mapReduceMaxChunksPerMeeting = 4
	// mapReduceParallelism is the map-stage semaphore width across meetings
	// (chunks within one meeting are sequential — continuity carry).
	mapReduceParallelism = 3
	// mapReduceDigestFreshSlack: a stored current digest whose spanEnd is
	// within this of the meeting's newest in-range entry counts as covering
	// it (brains lag transcripts by design).
	mapReduceDigestFreshSlack = 15 * time.Minute
	// comprehensiveBriefingTimeout bounds the whole fallback composition.
	comprehensiveBriefingTimeout = 90 * time.Second
)

// briefingSourceEntriesInRange gathers the raw material for the map stage:
// brain + transcript entries with CreatedAt in [start, end), grouped by
// meeting (legacy null-meetingId entries land on digestKeyForBrain's
// synthetic per-day key). The kind WHITELIST is the blob firewall — an
// os_artifact/base64 body is structurally unreachable — and stripOversizeBody
// bounds pathological prose as belt-and-suspenders. Hidden (quarantined/
// expired) material never surfaces.
func (store *meetingMemoryStore) briefingSourceEntriesInRange(start time.Time, end time.Time) map[string][]meetingMemoryEntry {
	if store == nil || !end.After(start) {
		return nil
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	groups := map[string][]meetingMemoryEntry{}
	for _, entry := range store.entries {
		switch entry.Kind {
		case meetingMemoryKindBrain, meetingMemoryKindTranscript:
		default:
			continue
		}
		if memoryEntryHiddenFromRecall(entry) {
			continue
		}
		if entry.CreatedAt.Before(start) || !entry.CreatedAt.Before(end) {
			continue
		}
		key := digestKeyForBrain(entry)
		groups[key] = append(groups[key], stripOversizeBody(cloneMemoryEntry(entry)))
	}
	return groups
}

// digestCoversWindow reports whether a stored current digest already covers a
// meeting's newest in-range material (the A6 cache test).
func digestCoversWindow(digest meetingMemoryEntry, newest time.Time) bool {
	spanEnd, err := time.Parse(time.RFC3339, strings.TrimSpace(digest.Metadata[digestSpanEndMetadataKey]))
	if err != nil {
		return false
	}
	return !spanEnd.Add(mapReduceDigestFreshSlack).Before(newest)
}

// buildBriefingMapInput assembles one map chunk's prompt: the digest-so-far
// carry (the producer's "previous digest" continuity trick) plus the raw
// source lines. Brain/transcript text only — blob-free by the caller's kind
// whitelist.
func buildBriefingMapInput(meetingKey string, title string, carryJSON string, entries []meetingMemoryEntry, generatedAt time.Time) string {
	var builder strings.Builder
	builder.WriteString("# Generated at\n")
	builder.WriteString(generatedAt.Format(time.RFC3339))
	builder.WriteString("\n\n# Meeting\nid: ")
	builder.WriteString(meetingKey)
	if title != "" {
		builder.WriteString("\ntitle: ")
		builder.WriteString(title)
	}
	if carryJSON != "" {
		builder.WriteString("\n\n# Previous digest for this meeting (continuity — carry forward, update statuses, never silently drop)\n")
		builder.WriteString(carryJSON)
	}
	builder.WriteString("\n\n# New source material (oldest first; transcript lines carry their entry id — use those ids as anchors)\n")
	for _, entry := range entries {
		builder.WriteString("- id=")
		builder.WriteString(entry.ID)
		builder.WriteString(" kind=")
		builder.WriteString(entry.Kind)
		builder.WriteString(" time=")
		builder.WriteString(entry.CreatedAt.Format(time.RFC3339))
		builder.WriteByte('\n')
		for _, line := range strings.Split(entry.Text, "\n") {
			builder.WriteString("  ")
			builder.WriteString(strings.TrimSpace(line))
			builder.WriteByte('\n')
		}
	}
	return builder.String()
}

// chunkEntriesForMap packs entries (oldest first) into char-budgeted chunks.
// When the chunk cap would overflow, the OLDEST chunks are dropped so the
// carry sequence always ends at the meeting's newest material.
func chunkEntriesForMap(entries []meetingMemoryEntry, budgetChars int, maxChunks int) [][]meetingMemoryEntry {
	if len(entries) == 0 {
		return nil
	}
	chunks := make([][]meetingMemoryEntry, 0, 4)
	current := make([]meetingMemoryEntry, 0, 16)
	used := 0
	for _, entry := range entries {
		cost := len(entry.Text) + 80 // id/time framing overhead
		if used+cost > budgetChars && len(current) > 0 {
			chunks = append(chunks, current)
			current = make([]meetingMemoryEntry, 0, 16)
			used = 0
		}
		current = append(current, entry)
		used += cost
	}
	if len(current) > 0 {
		chunks = append(chunks, current)
	}
	if len(chunks) > maxChunks {
		chunks = chunks[len(chunks)-maxChunks:]
	}
	return chunks
}

// mapMeetingForBriefing produces one meeting's digest payload from raw
// material: brains preferred (the producer's own input class — already
// distilled), raw transcripts otherwise; chunked with sequential continuity
// carry; every call reuses the producer's instructions/schema/parser/clamps.
func (app *kanbanBoardApp) mapMeetingForBriefing(ctx context.Context, apiKey string, meetingKey string, entries []meetingMemoryEntry, prior meetingMemoryEntry, hasPrior bool, responder openAITextResponder) (meetingDigestPayload, time.Time, time.Time, error) {
	source := make([]meetingMemoryEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.Kind == meetingMemoryKindBrain {
			source = append(source, entry)
		}
	}
	if len(source) == 0 {
		source = entries
	}
	sort.SliceStable(source, func(i, j int) bool {
		return source[i].CreatedAt.Before(source[j].CreatedAt)
	})

	windowStart := source[0].CreatedAt
	windowEnd := source[len(source)-1].CreatedAt
	carryJSON := ""
	if hasPrior {
		// a stale stored digest still seeds continuity: its older facts carry
		// so the mapped payload stays cumulative like the producer's.
		carryJSON = prior.Text
	}

	var payload meetingDigestPayload
	title := app.meetingRecordTitle(meetingKey)
	for _, chunk := range chunkEntriesForMap(source, mapReduceChunkBudgetChars, mapReduceMaxChunksPerMeeting) {
		text, err := responder(ctx, apiKey, openAITextRequest{
			Model:           meetingBrainModel(),
			Instructions:    meetingDigestInstructions(),
			Input:           buildBriefingMapInput(meetingKey, title, carryJSON, chunk, time.Now().UTC()),
			ReasoningEffort: "low",
			Verbosity:       "low",
			MaxOutputTokens: meetingDigestMaxOutputTokens,
		})
		if err != nil {
			return meetingDigestPayload{}, windowStart, windowEnd, err
		}
		parsed, ok := parseMeetingDigest(text)
		if !ok {
			return meetingDigestPayload{}, windowStart, windowEnd, fmt.Errorf("map stage returned non-JSON output for %s", meetingKey)
		}
		chunkEnd := chunk[len(chunk)-1].CreatedAt
		clampMeetingDigestPayload(&parsed, meetingKey, dayBucket(chunkEnd), windowStart, chunkEnd)
		payload = parsed
		canonical, err := json.Marshal(parsed)
		if err != nil {
			return meetingDigestPayload{}, windowStart, windowEnd, err
		}
		carryJSON = string(canonical)
	}
	return payload, windowStart, windowEnd, nil
}

// syntheticBriefingDigestEntry wraps a mapped payload as an in-memory
// meeting_digest entry so the reduce stage can ride the exact stored-digest
// fold. NEVER persisted (see the file comment: a cursor-less digest would
// corrupt the ambient producer's resume position).
func syntheticBriefingDigestEntry(meetingKey string, payload meetingDigestPayload, windowStart time.Time, windowEnd time.Time) (meetingMemoryEntry, error) {
	canonical, err := json.Marshal(payload)
	if err != nil {
		return meetingMemoryEntry{}, err
	}
	return meetingMemoryEntry{
		ID:        "briefing-map-" + meetingKey,
		Kind:      meetingMemoryKindMeetingDigest,
		Text:      string(canonical),
		CreatedAt: windowEnd,
		Metadata: map[string]string{
			digestKeyMetadataKey:       meetingKey,
			digestCurrentMetadataKey:   digestCurrentTrue,
			"meetingId":                meetingKey,
			digestDayMetadataKey:       dayBucket(windowEnd),
			digestSpanStartMetadataKey: windowStart.UTC().Format(time.RFC3339),
			digestSpanEndMetadataKey:   windowEnd.UTC().Format(time.RFC3339),
			"source":                   "briefing_map_reduce",
		},
	}, nil
}

// buildComprehensiveBriefing composes a fresh cross-meeting briefing for
// [rangeStart, rangeEnd) directly from raw meeting memory: bounded parallel
// map calls per meeting (stored current digests are cache hits and cost
// nothing), deterministic reduce through the shared briefing composer. A
// failed meeting is skipped (logged), never fatal, unless NOTHING could be
// composed — the caller then keeps its own last resort.
func (app *kanbanBoardApp) buildComprehensiveBriefing(ctx context.Context, apiKey string, rangeStart time.Time, rangeEnd time.Time, responder openAITextResponder) (crossMeetingBriefingResult, error) {
	if app == nil || app.memory == nil {
		return crossMeetingBriefingResult{}, fmt.Errorf("meeting memory is unavailable")
	}
	if responder == nil {
		responder = createOpenAITextResponse
	}

	groups := app.memory.briefingSourceEntriesInRange(rangeStart, rangeEnd)
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	// deterministic oldest-first meeting order; the cap drops the OLDEST
	// overflow so the briefing biases what happened most recently.
	sort.SliceStable(keys, func(i, j int) bool {
		if !groups[keys[i]][0].CreatedAt.Equal(groups[keys[j]][0].CreatedAt) {
			return groups[keys[i]][0].CreatedAt.Before(groups[keys[j]][0].CreatedAt)
		}
		return keys[i] < keys[j]
	})
	omitted := 0
	if len(keys) > mapReduceMaxMeetings {
		omitted = len(keys) - mapReduceMaxMeetings
		keys = keys[omitted:]
	}

	current := app.memory.latestDigestPerMeeting()
	digestEntries := map[string]meetingMemoryEntry{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	var firstErr error
	sem := make(chan struct{}, mapReduceParallelism)
	for _, key := range keys {
		entries := groups[key]
		newest := entries[0].CreatedAt
		for _, entry := range entries {
			if entry.CreatedAt.After(newest) {
				newest = entry.CreatedAt
			}
		}
		prior, hasPrior := current[key]
		if hasPrior && digestCoversWindow(prior, newest) {
			// A6 cache hit: the stored digest IS the map output. Locked —
			// workers spawned for earlier keys may be writing concurrently.
			mu.Lock()
			digestEntries[key] = prior
			mu.Unlock()
			continue
		}
		wg.Add(1)
		go func(key string, entries []meetingMemoryEntry, prior meetingMemoryEntry, hasPrior bool) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				mu.Lock()
				if firstErr == nil {
					firstErr = ctx.Err()
				}
				mu.Unlock()
				return
			}
			payload, windowStart, windowEnd, err := app.mapMeetingForBriefing(ctx, apiKey, key, entries, prior, hasPrior, responder)
			if err == nil {
				var entry meetingMemoryEntry
				entry, err = syntheticBriefingDigestEntry(key, payload, windowStart, windowEnd)
				if err == nil {
					mu.Lock()
					digestEntries[key] = entry
					mu.Unlock()
					return
				}
			}
			log.Errorf("briefing map stage failed for %s: %v", key, err)
			mu.Lock()
			if firstErr == nil {
				firstErr = err
			}
			mu.Unlock()
		}(key, entries, prior, hasPrior)
	}
	wg.Wait()

	if len(digestEntries) == 0 && firstErr != nil {
		return crossMeetingBriefingResult{}, firstErr
	}
	briefing := app.composeBriefingFromDigests(rangeStart, rangeEnd, digestEntries, nil, briefingSourceMapReduce, omitted)
	return briefing, nil
}
