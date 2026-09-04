package main

// Recall coverage on answers (Wave 8 D7). Every generic Scout answer now
// carries `coverage` ∈ {complete, partial, unavailable}, graded by the same
// RecallCoverage derivation the brain retrieval planner uses
// (recall_coverage.go deriveRecallCoverageStatus) over the inventory the
// answer was actually composed from:
//
//   - sources: one row per context entry, fresh unless the entry is archived
//     (stale) or its body was capped for the prompt (partial);
//   - lanes: lexical active when the search band matched, semantic active
//     when an embedding index is loaded, digest active when a rollup rode
//     the context, raw active when any raw evidence did;
//   - range: the resolved query range (or the store's visible span when the
//     question named none).
//
// Only authorized inventory ever enters the grade — the entries handed in
// have already passed the principal's recall store.

import (
	"strings"
	"time"
)

// answerDigestOnlyCoverageComplete re-checks every derive() condition except
// the raw lane for a digest-only answer: authorized sources, the requested
// range fully resolved, every source fresh.
func answerDigestOnlyCoverageComplete(coverage RecallCoverage) bool {
	if coverage.AuthorizedSources == 0 || !coverage.ResolvedStartUTC.Equal(coverage.RequestedStartUTC) || !coverage.ResolvedEndUTC.Equal(coverage.RequestedEndUTC) {
		return false
	}
	for _, source := range coverage.Sources {
		if source.Status != RecallSourceFresh {
			return false
		}
	}
	return true
}

// answerRecallCoverage grades one answer's evidence.
func (app *kanbanBoardApp) answerRecallCoverage(query string, matches []meetingMemoryMatch, contextEntries []meetingMemoryEntry, now time.Time) RecallCoverage {
	if now.IsZero() {
		now = time.Now()
	}
	now = now.UTC()
	location := meetingTimeLocation()
	requestedStart, requestedEnd, hasTimeRange := relativeQueryTimeRange(query, now)
	if !hasTimeRange || !requestedStart.Before(requestedEnd) {
		requestedStart, requestedEnd = time.Time{}, now.Add(time.Second)
		if app != nil && app.memory != nil {
			// The visible span's start: one read-locked timestamp scan, never a
			// full-store clone (this runs on every answer).
			if earliest := app.memory.earliestVisibleCreatedAt(); !earliest.IsZero() {
				requestedStart = earliest.UTC()
			}
		}
		if requestedStart.IsZero() || !requestedStart.Before(requestedEnd) {
			requestedStart = requestedEnd.Add(-time.Second)
		}
	}
	requestedStart, requestedEnd = requestedStart.UTC(), requestedEnd.UTC()

	sources := make([]RecallSourceCoverage, 0, len(contextEntries))
	seen := make(map[string]struct{}, len(contextEntries))
	digestLane, rawLane := false, false
	resolvedStart, resolvedEnd := time.Time{}, time.Time{}
	for _, entry := range contextEntries {
		id := strings.TrimSpace(entry.ID)
		if id == "" {
			continue
		}
		key := entry.Kind + "\x00" + id
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		status := RecallSourceFresh
		switch {
		case memoryEntryRelevance(entry) == relevanceArchived:
			status = RecallSourceStale
		case strings.TrimSpace(entry.Metadata[promptBodyOmittedMetadataKey]) != "":
			status = RecallSourcePartial
		}
		sources = append(sources, RecallSourceCoverage{
			SourceFamily:  entry.Kind,
			ObjectID:      id,
			ContentDigest: sha256Hex([]byte(entry.Text)),
			Status:        status,
		})
		if isMeetingDigestKind(entry.Kind) {
			digestLane = true
		} else {
			// Raw evidence, or the deterministic ledger fold (authoritative
			// state computed in Go) — either grounds the answer directly.
			rawLane = true
		}
		if !entry.CreatedAt.IsZero() && entry.Kind != memoryContextKindLedgerState {
			at := entry.CreatedAt.UTC()
			if resolvedStart.IsZero() || at.Before(resolvedStart) {
				resolvedStart = at
			}
			if resolvedEnd.IsZero() || at.After(resolvedEnd) {
				resolvedEnd = at
			}
		}
	}
	if !hasTimeRange || resolvedStart.IsZero() || resolvedEnd.IsZero() {
		// Without a named range the whole visible span is the request, and
		// the evidence resolves it; with a named range but no dated evidence
		// the range is unresolved (partial) by construction.
		resolvedStart, resolvedEnd = requestedStart, requestedEnd
		if hasTimeRange && len(sources) == 0 {
			resolvedEnd = requestedStart.Add(time.Second)
		}
	} else {
		// Evidence inside the range resolves the range; evidence that stops
		// short leaves the tail uncovered (partial).
		if !resolvedStart.After(requestedStart) {
			resolvedStart = requestedStart
		}
		if !resolvedEnd.Before(requestedEnd) {
			resolvedEnd = requestedEnd
		} else if !resolvedEnd.After(resolvedStart) {
			resolvedEnd = resolvedStart.Add(time.Second)
		}
	}

	lanes := RecallLaneCoverage{Lexical: RecallLaneDegraded, Semantic: RecallLaneNotRequired, Digest: RecallLaneNotRequired, Raw: RecallLaneUnavailable}
	if len(matches) > 0 {
		lanes.Lexical = RecallLaneActive
	}
	if loadedEmbeddingIndex() != nil {
		lanes.Semantic = RecallLaneActive
	}
	if digestLane {
		lanes.Digest = RecallLaneActive
	}
	if rawLane {
		lanes.Raw = RecallLaneActive
	}
	if len(sources) == 0 {
		lanes.Lexical, lanes.Digest, lanes.Raw = RecallLaneUnavailable, RecallLaneUnavailable, RecallLaneUnavailable
	}

	coverage := RecallCoverage{
		SnapshotID:        sha256Hex([]byte(query + "\x00" + now.Format(time.RFC3339Nano))),
		RequestedStartUTC: requestedStart, RequestedEndUTC: requestedEnd,
		ResolvedStartUTC: resolvedStart, ResolvedEndUTC: resolvedEnd,
		Timezone: location.String(),
		Settled:  true,
		Sources:  sources, AuthorizedSources: len(sources),
		Lanes: lanes, AsOf: now,
	}
	for _, source := range sources {
		switch source.Status {
		case RecallSourceFresh:
			coverage.FreshSources++
		case RecallSourcePartial:
			coverage.PartialSources++
		case RecallSourceStale:
			coverage.StaleSources++
		}
	}
	coverage.Status = deriveRecallCoverageStatus(coverage)
	if coverage.Status == RecallCoveragePartial && digestLane && !rawLane && answerDigestOnlyCoverageComplete(coverage) {
		// A current digest is the T2 rollup of the raw evidence it folded: an
		// answer composed entirely from fresh digests that resolve the
		// requested range is complete coverage. deriveRecallCoverageStatus
		// requires the raw lane because raw is the retrieval PLANNER's primary;
		// for an answer the pinned digest lane is primary and raw is simply not
		// required — production graded exactly this shape (digest lane pinned,
		// lexical band empty) as partial with an empty reason.
		coverage.Lanes.Raw = RecallLaneNotRequired
		coverage.Status = RecallCoverageComplete
	}
	if coverage.Status != RecallCoverageComplete {
		coverage.Reason = brainCoverageReason(coverage)
		if strings.TrimSpace(coverage.Reason) == "" {
			coverage.Reason = "evidence did not fully cover the question"
		}
	}
	coverage.Digest, _ = coverage.CanonicalDigest()
	return coverage
}
