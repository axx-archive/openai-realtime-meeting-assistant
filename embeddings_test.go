package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// conceptEmbedder is a deterministic synthetic embedder: it maps text onto a
// tiny concept space (Korea/pricing/vendor + a default dim) so that texts
// sharing a CONCEPT but NOT any literal token still land close in vector space.
// No test ever calls OpenAI.
func conceptEmbedder(dims int) embeddingFunc {
	groups := [][]string{
		{"korea", "korean", "seoul", "samsung", "hallyu"},
		{"pricing", "usage", "revenue"},
		{"vendor", "onboarding", "supplier"},
	}
	return func(_ context.Context, inputs []string) ([][]float32, error) {
		out := make([][]float32, len(inputs))
		for i, in := range inputs {
			vec := make([]float32, dims)
			low := strings.ToLower(in)
			hit := false
			for g, terms := range groups {
				if g >= dims {
					break
				}
				for _, term := range terms {
					if strings.Contains(low, term) {
						vec[g] = 1
						hit = true
						break
					}
				}
			}
			if !hit && dims > 0 {
				vec[dims-1] = 1
			}
			out[i] = vec
		}
		return out, nil
	}
}

// spyEmbedder records every input string it is asked to embed, so a test can
// prove a forbidden entry (a chat thread) was NEVER sent to the API.
type spyEmbedder struct {
	mu     sync.Mutex
	inputs []string
	inner  embeddingFunc
}

func (s *spyEmbedder) fn() embeddingFunc {
	return func(ctx context.Context, inputs []string) ([][]float32, error) {
		s.mu.Lock()
		s.inputs = append(s.inputs, inputs...)
		s.mu.Unlock()
		return s.inner(ctx, inputs)
	}
}

func (s *spyEmbedder) sawContaining(marker string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, in := range s.inputs {
		if strings.Contains(strings.ToLower(in), strings.ToLower(marker)) {
			return true
		}
	}
	return false
}

func testIndex(t *testing.T, dims int, embed embeddingFunc) *embeddingIndex {
	t.Helper()
	return newEmbeddingIndex(filepath.Join(t.TempDir(), "embeddings.jsonl"), dims, "test-embed", embed)
}

func indexHasID(idx *embeddingIndex, id string) bool {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	_, ok := idx.byID[id]
	return ok
}

func indexHashForID(t *testing.T, idx *embeddingIndex, id string) string {
	t.Helper()
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	i, ok := idx.byID[id]
	if !ok {
		t.Fatalf("index has no row for %s", id)
	}
	return idx.rows[i].hash
}

// TestEmbeddingEligibilityAllowlistAndPrivacyPin pins the corpus boundary: the
// consolidated knowledge kinds are eligible, and scout_chat_thread (private AND
// team-channel), the transcript firehose, and other UI-state kinds are NOT.
func TestEmbeddingEligibilityAllowlistAndPrivacyPin(t *testing.T) {
	eligible := []string{
		meetingMemoryKindBrain, meetingMemoryKindMeetingDigest, meetingMemoryKindDayDigest,
		meetingMemoryKindCompanyDigest, meetingMemoryKindNarrative, meetingMemoryKindDecision,
		meetingMemoryKindReflection, meetingMemoryKindRunLog, meetingMemoryKindNote,
	}
	for _, kind := range eligible {
		entry := meetingMemoryEntry{ID: "e-" + kind, Kind: kind, Text: "Some consolidated knowledge about pricing."}
		if !embeddingEligible(entry) {
			t.Fatalf("kind %q should be embeddable", kind)
		}
	}
	forbidden := []string{
		meetingMemoryKindScoutChat, // the privacy pin — never embed chat threads
		meetingMemoryKindTranscript,
		meetingMemoryKindSignal,
		meetingMemoryKindCodexProposal,
		meetingMemoryKindMissionInsight,
		meetingMemoryKindDealRoom,
		meetingMemoryKindLedgerEvent,
	}
	for _, kind := range forbidden {
		entry := meetingMemoryEntry{ID: "e-" + kind, Kind: kind, Text: "Private or firehose content that must never embed."}
		if embeddingEligible(entry) {
			t.Fatalf("kind %q must NOT be embeddable", kind)
		}
	}
	// An eligible kind with no usable text is still refused.
	if embeddingEligible(meetingMemoryEntry{ID: "blank", Kind: meetingMemoryKindBrain, Text: "   "}) {
		t.Fatal("empty-text entry must not be embeddable")
	}
	// A completed artifact is eligible; a running scaffold is not.
	ready := meetingMemoryEntry{ID: "art-1", Kind: meetingMemoryKindOSArtifact, Text: "Deck body", Metadata: map[string]string{"title": "Q3 deck", "threadStatus": "complete"}}
	if !embeddingEligible(ready) {
		t.Fatal("a completed artifact should be embeddable (title+excerpt)")
	}
	running := meetingMemoryEntry{ID: "art-2", Kind: meetingMemoryKindOSArtifact, Text: "…", Metadata: map[string]string{"threadStatus": "running"}}
	if embeddingEligible(running) {
		t.Fatal("a running artifact scaffold must not be embeddable")
	}
}

// TestEmbeddingMaintainerReconcileSkipsChatThreads drives one reconcile pass
// over a mixed corpus and proves the chat thread is neither indexed nor ever
// sent to the embedding API.
func TestEmbeddingMaintainerReconcileSkipsChatThreads(t *testing.T) {
	spy := &spyEmbedder{inner: conceptEmbedder(4)}
	idx := testIndex(t, 4, spy.fn())

	const secret = "supersecretchatmarker"
	entries := []meetingMemoryEntry{
		{ID: "brain-1", Kind: meetingMemoryKindBrain, Text: "Pricing sync brain notes.", CreatedAt: time.Now()},
		{ID: "digest-1", Kind: meetingMemoryKindMeetingDigest, Text: "Vendor onboarding digest.", CreatedAt: time.Now()},
		{ID: "chat-1", Kind: meetingMemoryKindScoutChat, Text: "private thread " + secret, CreatedAt: time.Now()},
		{ID: "tx-1", Kind: meetingMemoryKindTranscript, Text: "AJ: raw transcript firehose line.", CreatedAt: time.Now()},
	}
	if _, _, err := idx.reconcile(context.Background(), entries, 100); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if !indexHasID(idx, "brain-1") || !indexHasID(idx, "digest-1") {
		t.Fatal("knowledge kinds must be indexed")
	}
	if indexHasID(idx, "chat-1") {
		t.Fatal("scout_chat_thread must NEVER be indexed (privacy pin)")
	}
	if indexHasID(idx, "tx-1") {
		t.Fatal("transcript firehose must never be indexed")
	}
	if spy.sawContaining(secret) {
		t.Fatal("chat thread text was sent to the embedding API — privacy breach")
	}
	if got := idx.size(); got != 2 {
		t.Fatalf("index size = %d, want 2", got)
	}
}

// TestEmbeddingSidecarRoundTrip: reconcile → persist, then a FRESH index loads
// the sidecar and serves cosine from the reloaded matrix.
func TestEmbeddingSidecarRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "embeddings.jsonl")
	embed := conceptEmbedder(4)
	idx := newEmbeddingIndex(path, 4, "test-embed", embed)

	entries := []meetingMemoryEntry{
		{ID: "korea", Kind: meetingMemoryKindRunLog, Text: "Samsung TV Plus rollout with the Seoul broadcaster.", CreatedAt: time.Now()},
		{ID: "vendor", Kind: meetingMemoryKindRunLog, Text: "Vendor onboarding runbook.", CreatedAt: time.Now()},
	}
	if _, _, err := idx.reconcile(context.Background(), entries, 100); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	reloaded := newEmbeddingIndex(path, 4, "test-embed", embed)
	if err := reloaded.load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	if reloaded.size() != 2 {
		t.Fatalf("reloaded size = %d, want 2", reloaded.size())
	}
	queryVec := reloaded.queryEmbedding("the korean thing")
	if queryVec == nil {
		t.Fatal("query embedding returned nil")
	}
	hits := reloaded.topK(queryVec, 2)
	if len(hits) == 0 || hits[0].id != "korea" {
		t.Fatalf("top hit = %+v, want the Korea entry served from the reloaded matrix", hits)
	}
}

// TestEmbeddingContentHashReEmbed: an edited entry (new text) re-embeds; an
// untouched one does not.
func TestEmbeddingContentHashReEmbed(t *testing.T) {
	idx := testIndex(t, 4, conceptEmbedder(4))
	entries := []meetingMemoryEntry{
		{ID: "note-1", Kind: meetingMemoryKindNote, Text: "Original pricing note.", CreatedAt: time.Now()},
	}
	embedded, _, err := idx.reconcile(context.Background(), entries, 100)
	if err != nil || embedded != 1 {
		t.Fatalf("first reconcile embedded=%d err=%v, want 1", embedded, err)
	}
	firstHash := indexHashForID(t, idx, "note-1")

	// Re-run with the SAME content: nothing re-embeds.
	if embedded, _, _ := idx.reconcile(context.Background(), entries, 100); embedded != 0 {
		t.Fatalf("unchanged reconcile embedded=%d, want 0", embedded)
	}

	// Edit the text: the content hash changes and the row re-embeds.
	entries[0].Text = "Revised pricing note with new usage terms."
	embedded, dropped, err := idx.reconcile(context.Background(), entries, 100)
	if err != nil || embedded != 1 || dropped != 0 {
		t.Fatalf("edited reconcile embedded=%d dropped=%d err=%v, want embedded=1 dropped=0", embedded, dropped, err)
	}
	if newHash := indexHashForID(t, idx, "note-1"); newHash == firstHash {
		t.Fatal("content hash did not change after edit; re-embed detection is broken")
	}
	if idx.size() != 1 {
		t.Fatalf("size = %d, want 1 after in-place re-embed", idx.size())
	}
}

// TestEmbeddingVanishedEntryCleanup: an entry that disappears from the corpus
// is dropped from the index on the next pass.
func TestEmbeddingVanishedEntryCleanup(t *testing.T) {
	idx := testIndex(t, 4, conceptEmbedder(4))
	entries := []meetingMemoryEntry{
		{ID: "a", Kind: meetingMemoryKindBrain, Text: "alpha", CreatedAt: time.Now()},
		{ID: "b", Kind: meetingMemoryKindBrain, Text: "bravo", CreatedAt: time.Now()},
		{ID: "c", Kind: meetingMemoryKindBrain, Text: "charlie", CreatedAt: time.Now()},
	}
	if _, _, err := idx.reconcile(context.Background(), entries, 100); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if idx.size() != 3 {
		t.Fatalf("size = %d, want 3", idx.size())
	}
	// "b" vanishes from the corpus; the next pass drops it and re-embeds nothing.
	survivors := []meetingMemoryEntry{entries[0], entries[2]}
	embedded, dropped, err := idx.reconcile(context.Background(), survivors, 100)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if embedded != 0 {
		t.Fatalf("embedded = %d, want 0 (survivors already embedded)", embedded)
	}
	if dropped != 1 {
		t.Fatalf("dropped = %d, want 1", dropped)
	}
	if idx.size() != 2 {
		t.Fatalf("size = %d, want 2 after cleanup", idx.size())
	}
	if indexHasID(idx, "b") {
		t.Fatal("vanished entry b must be dropped from the index")
	}
	if !indexHasID(idx, "a") || !indexHasID(idx, "c") {
		t.Fatal("surviving entries must remain")
	}
}

// TestEmbeddingCosineTopK: brute-force cosine returns the nearest rows in order.
func TestEmbeddingCosineTopK(t *testing.T) {
	idx := testIndex(t, 3, nil)
	rows := []embeddingRow{
		{id: "x", vec: []float32{1, 0, 0}},
		{id: "y", vec: []float32{0, 1, 0}},
		{id: "xy", vec: []float32{0.8, 0.6, 0}},
		{id: "z", vec: []float32{0, 0, 1}},
	}
	for i := range rows {
		normalizeEmbeddingVector(rows[i].vec)
		idx.byID[rows[i].id] = i
	}
	idx.rows = rows

	hits := idx.topK([]float32{1, 0, 0}, 3)
	if len(hits) != 3 {
		t.Fatalf("hits = %d, want 3", len(hits))
	}
	if hits[0].id != "x" || hits[1].id != "xy" {
		t.Fatalf("order = [%s %s ...], want [x xy ...]", hits[0].id, hits[1].id)
	}
	if hits[2].id == "z" {
		t.Fatal("orthogonal z should not outrank y for a mostly-x query")
	}
}

// TestEmbeddingVectorCodec: base64 float32 round-trips exactly.
func TestEmbeddingVectorCodec(t *testing.T) {
	vec := []float32{0.125, -0.5, 1.0, 0, 3.5}
	decoded, err := decodeEmbeddingVector(encodeEmbeddingVector(vec), len(vec))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	for i := range vec {
		if decoded[i] != vec[i] {
			t.Fatalf("decoded[%d] = %v, want %v", i, decoded[i], vec[i])
		}
	}
	if _, err := decodeEmbeddingVector(encodeEmbeddingVector(vec), len(vec)+1); err == nil {
		t.Fatal("wrong-dimension decode should error")
	}
}

// buildFusionStore is the shared fixture for the retrieval-lane tests: pinned
// digests that must lead, a lexical decoy so the tail fallback never fires, and
// a run_log ("runlog-korea") reachable ONLY by the semantic lane.
func buildFusionStore(t *testing.T) *meetingMemoryStore {
	t.Helper()
	store, err := newMeetingMemoryStore(filepath.Join(t.TempDir(), "memory.jsonl"))
	if err != nil {
		t.Fatalf("newMeetingMemoryStore: %v", err)
	}
	base := time.Now().UTC()
	store.entries = []meetingMemoryEntry{
		testDigestContextEntry("digest-company", meetingMemoryKindCompanyDigest, companyDigestKey,
			`{"narrative":"Rollout is the running focus."}`, base.Add(5*time.Hour), nil),
		testDigestContextEntry("digest-meeting-0", meetingMemoryKindMeetingDigest, "meeting-0",
			`{"topics":[{"t":"Vendor onboarding"}]}`, base.Add(time.Hour), map[string]string{"meetingId": "meeting-0"}),
		testDigestContextEntry("digest-meeting-1", meetingMemoryKindMeetingDigest, "meeting-1",
			`{"topics":[{"t":"Vendor onboarding"}]}`, base.Add(2*time.Hour), map[string]string{"meetingId": "meeting-1"}),
		// Lexical decoy: shares "broadcast"/"venture" with the query so `selected`
		// is never empty and the unfiltered tail fallback stays dormant.
		{ID: "runlog-decoy", Kind: meetingMemoryKindRunLog, Text: "Broadcast venture metrics dashboard.", CreatedAt: base.Add(3 * time.Hour)},
		// Semantic-only target: it shares NO token with the query (not even a
		// stopword), so only the embedding lane can reach it — yet "Samsung" and
		// "Seoul" land it on the Korea concept vector.
		{ID: "runlog-korea", Kind: meetingMemoryKindRunLog, Text: "Samsung Seoul rollout collaboration.", CreatedAt: base.Add(4 * time.Hour)},
	}
	return store
}

const fusionQuery = "korean broadcast venture"

// TestSemanticLaneFusionSurfacesLexicalMiss is the flagship: with the semantic
// index published, a lexically-absent but semantically-close entry enters the
// raw-candidate band via RRF fusion, while the pinned digest lane still LEADS.
// Without the index the same entry is invisible — proving the semantic lane is
// what surfaced it.
func TestSemanticLaneFusionSurfacesLexicalMiss(t *testing.T) {
	store := buildFusionStore(t)

	// Keyless baseline: no published index → the semantic-only entry is absent.
	publishEmbeddingIndex(nil)
	baseline := store.contextEntriesForQuery(fusionQuery, 20, time.Now())
	if len(baseline) == 0 || baseline[0].ID != "digest-company" {
		t.Fatalf("baseline lead = %v, want the pinned company digest leading", laneIDs(baseline))
	}
	if memoryEntriesContain(baseline, "runlog-korea") {
		t.Fatal("without the semantic lane the lexically-absent entry must NOT appear")
	}

	// Publish a synthetic index covering the corpus; the query embeds to the
	// Korea concept.
	idx := testIndex(t, 4, conceptEmbedder(4))
	if _, _, err := idx.reconcile(context.Background(), store.entries, 100); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	publishEmbeddingIndex(idx)
	t.Cleanup(func() { publishEmbeddingIndex(nil) })

	fused := store.contextEntriesForQuery(fusionQuery, 20, time.Now())
	if len(fused) == 0 || fused[0].ID != "digest-company" {
		t.Fatalf("fused lead = %v, want the pinned digest lane still leading (untouched)", laneIDs(fused))
	}
	// The pinned digests keep leading, in order, ahead of any raw candidate.
	for _, want := range []string{"digest-company"} {
		if !memoryEntriesContain(fused, want) {
			t.Fatalf("pinned digest %s missing", want)
		}
	}
	if !memoryEntriesContain(fused, "runlog-korea") {
		t.Fatal("semantic lane failed to surface the lexically-absent entry")
	}
}

// TestSemanticLaneKeylessContributesNothing: with no index published the
// retrieval lane behaves exactly like the prior lexical-only path and never
// errors.
func TestSemanticLaneKeylessContributesNothing(t *testing.T) {
	publishEmbeddingIndex(nil)
	if got := semanticLaneCandidates("anything at all", 12); got != nil {
		t.Fatalf("keyless semanticLaneCandidates = %v, want nil", got)
	}
	store := buildFusionStore(t)
	entries := store.contextEntriesForQuery(fusionQuery, 20, time.Now())
	if len(entries) == 0 || entries[0].ID != "digest-company" {
		t.Fatalf("keyless lane lead = %v, want the pinned digest leading", laneIDs(entries))
	}
	if memoryEntriesContain(entries, "runlog-korea") {
		t.Fatal("keyless path must not surface the semantic-only entry")
	}
}

// TestSemanticLaneQueryEmbedFailureDegrades: when the query embed call fails,
// the lane silently contributes nothing (no panic, no error surfaced).
func TestSemanticLaneQueryEmbedFailureDegrades(t *testing.T) {
	failing := func(_ context.Context, _ []string) ([][]float32, error) {
		return nil, fmt.Errorf("simulated embeddings outage")
	}
	idx := testIndex(t, 4, failing)
	// Seed a row directly so the matrix is non-empty but the QUERY embed fails.
	idx.rows = []embeddingRow{{id: "runlog-korea", kind: meetingMemoryKindRunLog, vec: normalizedVec(4, 0)}}
	idx.byID = map[string]int{"runlog-korea": 0}
	publishEmbeddingIndex(idx)
	t.Cleanup(func() { publishEmbeddingIndex(nil) })

	if got := semanticLaneCandidates(fusionQuery, 12); got != nil {
		t.Fatalf("failing query embed = %v, want nil (silent degrade)", got)
	}
	store := buildFusionStore(t)
	entries := store.contextEntriesForQuery(fusionQuery, 20, time.Now())
	if len(entries) == 0 || entries[0].ID != "digest-company" {
		t.Fatalf("degraded lane lead = %v, want the pinned digest leading", laneIDs(entries))
	}
	if memoryEntriesContain(entries, "runlog-korea") {
		t.Fatal("a failed query embed must not surface the semantic-only entry")
	}
}

// TestEmbeddingMaintainerKeylessNeverRegisters: startEmbeddingMaintainer with
// no key registers nothing (the lexical-only degrade posture).
func TestEmbeddingMaintainerKeylessNeverRegisters(t *testing.T) {
	store, err := newMeetingMemoryStore(filepath.Join(t.TempDir(), "memory.jsonl"))
	if err != nil {
		t.Fatalf("newMeetingMemoryStore: %v", err)
	}
	app := &kanbanBoardApp{memory: store}
	app.ensureEmbeddingMaintainerStarted("")
	app.mu.Lock()
	_, registered := app.agentCancels[embeddingMaintainerAgentName]
	app.mu.Unlock()
	if registered {
		t.Fatal("keyless deploy must not register the embedding maintainer")
	}
}

// TestEmbeddingMaintainerDisabledEnv: EMBEDDINGS_DISABLED keeps it off even with
// a key present.
func TestEmbeddingMaintainerDisabledEnv(t *testing.T) {
	t.Setenv("EMBEDDINGS_DISABLED", "true")
	store, err := newMeetingMemoryStore(filepath.Join(t.TempDir(), "memory.jsonl"))
	if err != nil {
		t.Fatalf("newMeetingMemoryStore: %v", err)
	}
	app := &kanbanBoardApp{memory: store}
	app.startEmbeddingMaintainer("sk-test-key")
	app.mu.Lock()
	_, registered := app.agentCancels[embeddingMaintainerAgentName]
	app.mu.Unlock()
	if registered {
		t.Fatal("EMBEDDINGS_DISABLED must keep the maintainer off")
	}
}

func laneIDs(entries []meetingMemoryEntry) []string {
	ids := make([]string, len(entries))
	for i, entry := range entries {
		ids[i] = entry.ID
	}
	return ids
}

func normalizedVec(dims int, hot int) []float32 {
	vec := make([]float32, dims)
	if hot >= 0 && hot < dims {
		vec[hot] = 1
	}
	normalizeEmbeddingVector(vec)
	return vec
}

func indexRowCopy(t *testing.T, idx *embeddingIndex, id string) embeddingRow {
	t.Helper()
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	i, ok := idx.byID[id]
	if !ok {
		t.Fatalf("index has no row for %s", id)
	}
	return idx.rows[i]
}

func vectorsEqual(a, b []float32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestEmbeddingQueryFailureBreaker (F10): a query-embed failure opens a short
// breaker so subsequent recall queries skip the inline embed (and its full
// timeout) until it clears; a later success closes it.
func TestEmbeddingQueryFailureBreaker(t *testing.T) {
	var calls int
	idx := testIndex(t, embeddingDims, func(context.Context, []string) ([][]float32, error) {
		calls++
		return nil, fmt.Errorf("simulated embeddings outage")
	})

	if got := idx.queryEmbedding("first query"); got != nil {
		t.Fatal("a failed embed must return nil")
	}
	if got := idx.queryEmbedding("second query"); got != nil {
		t.Fatal("nil expected while the breaker is open")
	}
	if calls != 1 {
		t.Fatalf("embedder called %d times, want 1 (the breaker must skip the 2nd query's embed)", calls)
	}

	// Simulate the cooldown elapsing, then provider recovery.
	idx.queryFailUntil.Store(0)
	good := make([]float32, embeddingDims)
	good[0] = 1
	idx.embedder = func(context.Context, []string) ([][]float32, error) {
		return [][]float32{good}, nil
	}
	if got := idx.queryEmbedding("third query"); got == nil {
		t.Fatal("a vector was expected after recovery")
	}
	if idx.queryFailUntil.Load() != 0 {
		t.Fatal("a successful embed must close the breaker")
	}
}

func TestEmbeddingProviderCircuitPersistsBoundedHalfOpenCanary(t *testing.T) {
	now := time.Date(2026, time.July, 29, 12, 0, 0, 0, time.UTC)
	path := filepath.Join(t.TempDir(), "provider-circuit.json")
	circuit := newEmbeddingProviderCircuit(path, 2, 10*time.Second, 40*time.Second)
	circuit.now = func() time.Time { return now }

	firstPermit, ok := circuit.acquire()
	if !ok {
		t.Fatal("closed circuit did not admit first request")
	}
	_ = circuit.complete(firstPermit, nil, 1, 1, fmt.Errorf("quota exhausted"))
	if got := circuit.snapshot(); got.State != embeddingProviderCircuitClosed || got.ConsecutiveFailures != 1 {
		t.Fatalf("first failure snapshot=%+v, want closed with one failure", got)
	}
	secondPermit, ok := circuit.acquire()
	if !ok {
		t.Fatal("closed circuit did not admit second request")
	}
	_ = circuit.complete(secondPermit, nil, 1, 1, fmt.Errorf("quota exhausted"))
	if got := circuit.snapshot(); got.State != embeddingProviderCircuitOpen || !got.OpenUntil.Equal(now.Add(10*time.Second)) {
		t.Fatalf("threshold failure snapshot=%+v, want open through %s", got, now.Add(10*time.Second))
	}
	if _, allowed := circuit.acquire(); allowed {
		t.Fatal("open circuit must block provider traffic")
	}

	// Loading a fresh circuit proves a restart retains the known outage instead
	// of silently re-enabling an expensive background loop.
	reloaded := newEmbeddingProviderCircuit(path, 2, 10*time.Second, 40*time.Second)
	reloaded.now = func() time.Time { return now }
	if got := reloaded.snapshot(); got.State != embeddingProviderCircuitOpen || got.ConsecutiveFailures != 2 {
		t.Fatalf("reloaded snapshot=%+v, want persisted open state", got)
	}
	now = now.Add(10 * time.Second)
	canary, allowed := reloaded.acquire()
	if !allowed {
		t.Fatal("cooldown expiry must permit one bounded canary")
	}
	if got := reloaded.snapshot(); got.State != embeddingProviderCircuitHalfOpen {
		t.Fatalf("canary snapshot=%+v, want half_open", got)
	}
	if _, allowed := reloaded.acquire(); allowed {
		t.Fatal("half-open circuit must permit only one in-flight canary")
	}

	// A failed canary doubles the pause; only a later successful canary closes.
	_ = reloaded.complete(canary, nil, 1, 1, fmt.Errorf("quota exhausted"))
	if got := reloaded.snapshot(); got.State != embeddingProviderCircuitOpen || got.Cooldown != 20*time.Second {
		t.Fatalf("failed canary snapshot=%+v, want reopened 20s cooldown", got)
	}
	now = now.Add(20 * time.Second)
	canary, allowed = reloaded.acquire()
	if !allowed {
		t.Fatal("second cooldown expiry must permit the next single canary")
	}
	if err := reloaded.complete(canary, [][]float32{{1}}, 1, 1, nil); err != nil {
		t.Fatalf("complete successful canary: %v", err)
	}
	if got := reloaded.snapshot(); got.State != embeddingProviderCircuitClosed || got.ConsecutiveFailures != 0 || !got.OpenUntil.IsZero() {
		t.Fatalf("successful canary must close/reset circuit, got %+v", got)
	}
}

func TestEmbeddingProviderCircuitStopsRepeatedMaintainerFailures(t *testing.T) {
	now := time.Date(2026, time.July, 29, 12, 0, 0, 0, time.UTC)
	idx := testIndex(t, 2, nil)
	idx.providerCircuit = newEmbeddingProviderCircuit(filepath.Join(t.TempDir(), "provider-circuit.json"), 2, time.Minute, 4*time.Minute)
	idx.providerCircuit.now = func() time.Time { return now }
	var calls int
	idx.embedder = func(context.Context, []string) ([][]float32, error) {
		calls++
		return nil, &apiRequestFailure{status: "429 Too Many Requests", body: `{"error":{"code":"insufficient_quota"}}`}
	}
	entries := []meetingMemoryEntry{{ID: "brain-1", Kind: meetingMemoryKindBrain, Text: "Important durable knowledge", CreatedAt: now}}
	for pass := 0; pass < 3; pass++ {
		_, _, _ = idx.reconcile(context.Background(), entries, 1)
	}
	if calls != 2 {
		t.Fatalf("provider called %d times, want 2 before durable circuit blocks third pass", calls)
	}
	if got := idx.providerCircuit.snapshot(); got.State != embeddingProviderCircuitOpen || got.LastFailureClass != embeddingProviderFailureQuota {
		t.Fatalf("circuit snapshot=%+v, want open quota state", got)
	}
	if evidence := func() map[string]any {
		previous := loadedEmbeddingIndex()
		publishEmbeddingIndex(idx)
		t.Cleanup(func() { publishEmbeddingIndex(previous) })
		return embeddingProviderCircuitEvidence()
	}(); evidence["circuit"] != "open" || evidence["providerFailureClass"] != "quota" {
		t.Fatalf("health evidence=%v, want open quota receipt", evidence)
	}

	now = now.Add(time.Minute)
	idx.embedder = func(context.Context, []string) ([][]float32, error) {
		calls++
		return [][]float32{{1, 0}}, nil
	}
	if embedded, _, err := idx.reconcile(context.Background(), entries, 1); err != nil || embedded != 1 {
		t.Fatalf("half-open canary reconcile embedded=%d err=%v, want success", embedded, err)
	}
	if calls != 3 || idx.providerCircuit.snapshot().State != embeddingProviderCircuitClosed {
		t.Fatalf("successful canary calls=%d state=%s, want 3 and closed", calls, idx.providerCircuit.snapshot().State)
	}
}

func TestClassifyEmbeddingProviderFailure(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		want embeddingProviderFailureClass
	}{
		{"quota", &apiRequestFailure{status: "429 Too Many Requests", body: "insufficient_quota"}, embeddingProviderFailureQuota},
		{"rate limit", &apiRequestFailure{status: "429 Too Many Requests", body: "rate_limit_error"}, embeddingProviderFailureRateLimit},
		{"authentication", &apiRequestFailure{status: "401 Unauthorized", body: "invalid_api_key"}, embeddingProviderFailureAuth},
		{"model access", &apiRequestFailure{status: "404 Not Found", body: "model_not_found"}, embeddingProviderFailureModelAccess},
		{"model access before generic 403", &apiRequestFailure{status: "403 Forbidden", body: "You do not have access to model text-embedding-x"}, embeddingProviderFailureModelAccess},
		{"transient", fmt.Errorf("dial tcp: connection reset"), embeddingProviderFailureTransient},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := classifyEmbeddingProviderFailure(test.err); got != test.want {
				t.Fatalf("classify=%q, want %q", got, test.want)
			}
		})
	}
}

func TestEmbeddingProviderCircuitRejectsMalformedCompletions(t *testing.T) {
	for _, test := range []struct {
		name    string
		vectors [][]float32
	}{
		{"wrong count", nil},
		{"wrong dimensions", [][]float32{{1}}},
		{"nan", [][]float32{{float32(math.NaN()), 0}}},
		{"infinity", [][]float32{{float32(math.Inf(1)), 0}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			idx := testIndex(t, 2, func(context.Context, []string) ([][]float32, error) {
				return test.vectors, nil
			})
			idx.providerCircuit = newEmbeddingProviderCircuit(filepath.Join(t.TempDir(), "circuit.json"), 1, time.Minute, time.Hour)
			if _, err := idx.embedWithProviderCircuit(context.Background(), []string{"input"}); !errors.Is(err, errEmbeddingProviderInvalidVector) {
				t.Fatalf("malformed completion error=%v, want invalid-vector sentinel", err)
			}
			if idx.providerSuccesses.Load() != 0 {
				t.Fatal("malformed completion must not record provider success")
			}
			if got := idx.providerCircuit.snapshot(); got.State != embeddingProviderCircuitOpen || got.LastFailureClass != embeddingProviderFailureTransient {
				t.Fatalf("malformed completion snapshot=%+v, want open transient failure", got)
			}
		})
	}
}

func TestEmbeddingProviderLateSuccessCannotCloseNewGeneration(t *testing.T) {
	circuit := newEmbeddingProviderCircuit(filepath.Join(t.TempDir(), "circuit.json"), 2, time.Minute, time.Hour)
	first, ok := circuit.acquire()
	if !ok {
		t.Fatal("first request not admitted")
	}
	second, ok := circuit.acquire()
	if !ok {
		t.Fatal("second request not admitted")
	}
	lateSuccess, ok := circuit.acquire()
	if !ok {
		t.Fatal("concurrent request not admitted")
	}
	_ = circuit.complete(first, nil, 1, 2, errors.New("temporary provider failure"))
	_ = circuit.complete(second, nil, 1, 2, errors.New("quota exhausted"))
	opened := circuit.snapshot()
	if opened.State != embeddingProviderCircuitOpen {
		t.Fatalf("threshold failures state=%s, want open", opened.State)
	}
	if err := circuit.complete(lateSuccess, [][]float32{{1, 0}}, 1, 2, nil); !errors.Is(err, errEmbeddingProviderStalePermit) {
		t.Fatalf("late completion error=%v, want stale permit", err)
	}
	if after := circuit.snapshot(); after.State != embeddingProviderCircuitOpen || after.Generation != opened.Generation {
		t.Fatalf("late success changed open generation: before=%+v after=%+v", opened, after)
	}
}

func TestEmbeddingProviderHalfOpenAdmitsExactlyOneConcurrentCanary(t *testing.T) {
	now := time.Date(2026, time.July, 29, 15, 0, 0, 0, time.UTC)
	circuit := newEmbeddingProviderCircuit(filepath.Join(t.TempDir(), "circuit.json"), 1, time.Minute, time.Hour)
	circuit.now = func() time.Time { return now }
	permit, _ := circuit.acquire()
	_ = circuit.complete(permit, nil, 1, 2, errors.New("quota exhausted"))
	now = now.Add(time.Minute)

	const callers = 32
	var admitted atomic.Int32
	start := make(chan struct{})
	done := make(chan struct{}, callers)
	for i := 0; i < callers; i++ {
		go func() {
			<-start
			if _, ok := circuit.acquire(); ok {
				admitted.Add(1)
			}
			done <- struct{}{}
		}()
	}
	close(start)
	for i := 0; i < callers; i++ {
		<-done
	}
	if got := admitted.Load(); got != 1 {
		t.Fatalf("half-open admitted %d canaries, want exactly 1", got)
	}
	if circuit.snapshot().State != embeddingProviderCircuitHalfOpen {
		t.Fatalf("state=%s, want half_open", circuit.snapshot().State)
	}
}

func TestEmbeddingProviderCircuitPersistenceFailsClosed(t *testing.T) {
	for _, test := range []struct {
		name  string
		setup func(t *testing.T) string
	}{
		{
			name: "corrupt",
			setup: func(t *testing.T) string {
				path := filepath.Join(t.TempDir(), "circuit.json")
				if err := os.WriteFile(path, []byte("not-json"), 0o600); err != nil {
					t.Fatal(err)
				}
				return path
			},
		},
		{
			name: "insecure mode",
			setup: func(t *testing.T) string {
				path := filepath.Join(t.TempDir(), "circuit.json")
				if err := os.WriteFile(path, []byte(`{"state":"closed"}`), 0o644); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(path, 0o644); err != nil {
					t.Fatal(err)
				}
				return path
			},
		},
		{
			name: "symlink",
			setup: func(t *testing.T) string {
				dir := t.TempDir()
				target := filepath.Join(dir, "target")
				if err := os.WriteFile(target, []byte("target"), 0o600); err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(dir, "circuit.json")
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
				return path
			},
		},
		{
			name: "unwritable shape",
			setup: func(t *testing.T) string {
				parent := filepath.Join(t.TempDir(), "not-a-directory")
				if err := os.WriteFile(parent, []byte("x"), 0o600); err != nil {
					t.Fatal(err)
				}
				return filepath.Join(parent, "circuit.json")
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			circuit := newEmbeddingProviderCircuit(test.setup(t), 1, time.Minute, time.Hour)
			if got := circuit.snapshot(); !got.PersistenceError || got.State != embeddingProviderCircuitOpen {
				t.Fatalf("snapshot=%+v, want fail-closed persistence error", got)
			}
			if _, ok := circuit.acquire(); ok {
				t.Fatal("persistence error must block provider admission")
			}
		})
	}
}

func TestEmbeddingProviderCircuitSecureAtomicStateAndHealth(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "circuit.json")
	circuit := newEmbeddingProviderCircuit(path, 1, time.Minute, time.Hour)
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("state file: %v", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		t.Fatalf("state mode=%v, want regular 0600", info.Mode())
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".tmp-") {
			t.Fatalf("atomic persistence left temporary file %q", entry.Name())
		}
	}

	permit, _ := circuit.acquire()
	_ = circuit.complete(permit, nil, 1, 2, errors.New("quota exhausted"))
	if got := capabilityStatus(map[string]any{"enabled": true, "circuit": "open", "lastSuccessAt": "old"}, true); got != "degraded" {
		t.Fatalf("open capability status=%q, want degraded", got)
	}
	if got := capabilityStatus(map[string]any{"enabled": true, "circuit": "half_open", "lastSuccessAt": "old"}, true); got != "degraded" {
		t.Fatalf("half-open capability status=%q, want degraded", got)
	}
	if got := capabilityStatus(map[string]any{"enabled": true, "circuit": "persistence_error", "lastSuccessAt": "old"}, true); got != "degraded" {
		t.Fatalf("persistence capability status=%q, want degraded", got)
	}
}

func TestEmbeddingMaintainerDoesNotReportHealthSuccessWithoutValidatedProviderCall(t *testing.T) {
	resetCapabilityRuntimeForTest(t)
	store, err := newMeetingMemoryStore(filepath.Join(t.TempDir(), "memory.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	app := &kanbanBoardApp{memory: store}
	idx := testIndex(t, 2, func(context.Context, []string) ([][]float32, error) {
		t.Fatal("empty corpus must not call provider")
		return nil, nil
	})
	if err := app.runEmbeddingMaintainerOnce(idx); err != nil {
		t.Fatalf("empty pass: %v", err)
	}
	if got := capabilityState(capabilityEmbedding); !got.LastSuccess.IsZero() {
		t.Fatalf("empty pass manufactured health success at %s", got.LastSuccess)
	}

	store.mu.Lock()
	store.entries = append(store.entries, meetingMemoryEntry{
		ID:        "brain-malformed",
		Kind:      meetingMemoryKindBrain,
		Text:      "Validated provider response required.",
		CreatedAt: time.Now().UTC(),
		Metadata:  map[string]string{"visibility": "organization"},
	})
	store.mu.Unlock()
	idx.embedder = func(context.Context, []string) ([][]float32, error) { return [][]float32{{1}}, nil }
	if err := app.runEmbeddingMaintainerOnce(idx); !errors.Is(err, errEmbeddingProviderInvalidVector) {
		t.Fatalf("malformed pass error=%v, want invalid vector", err)
	}
	if got := capabilityState(capabilityEmbedding); !got.LastSuccess.IsZero() {
		t.Fatalf("malformed pass manufactured health success at %s", got.LastSuccess)
	}
}

func TestEmbeddingProviderCircuitWriteFailureBecomesFailClosed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "circuit.json")
	circuit := newEmbeddingProviderCircuit(path, 1, time.Minute, time.Hour)
	permit, ok := circuit.acquire()
	if !ok {
		t.Fatal("initial request not admitted")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = circuit.complete(permit, nil, 1, 2, errors.New("quota exhausted"))
	if got := circuit.snapshot(); !got.PersistenceError || got.State != embeddingProviderCircuitOpen {
		t.Fatalf("write failure snapshot=%+v, want persistence_error fail-closed", got)
	}
	if _, allowed := circuit.acquire(); allowed {
		t.Fatal("write failure must block subsequent provider calls")
	}
}

// TestEmbeddingReconcileKeepsStaleVectorOverBudget (F26): a changed row that is
// over this pass's embed budget keeps its previous (stale) vector instead of
// vanishing from the index until a later pass gets to it.
func TestEmbeddingReconcileKeepsStaleVectorOverBudget(t *testing.T) {
	idx := testIndex(t, 4, conceptEmbedder(4))
	older := time.Now().Add(-time.Hour)
	newer := time.Now()
	entries := []meetingMemoryEntry{
		{ID: "a", Kind: meetingMemoryKindBrain, Text: "Samsung Korea Seoul rollout.", CreatedAt: older},
		{ID: "b", Kind: meetingMemoryKindBrain, Text: "Vendor supplier onboarding.", CreatedAt: newer},
	}
	if embedded, _, err := idx.reconcile(context.Background(), entries, 100); err != nil || embedded != 2 {
		t.Fatalf("initial reconcile embedded=%d err=%v, want 2", embedded, err)
	}
	oldVecA := append([]float32(nil), indexRowCopy(t, idx, "a").vec...)

	// Change BOTH rows across concept boundaries. A budget of 1 embeds only the
	// NEWER row (b), leaving the changed "a" over budget this pass.
	entries[0].Text = "Vendor supplier onboarding runbook." // korea -> vendor
	entries[1].Text = "Pricing usage revenue update."       // vendor -> pricing
	embedded, dropped, err := idx.reconcile(context.Background(), entries, 1)
	if err != nil || embedded != 1 || dropped != 0 {
		t.Fatalf("budgeted reconcile embedded=%d dropped=%d err=%v, want 1/0/nil", embedded, dropped, err)
	}
	if idx.size() != 2 {
		t.Fatalf("size=%d, want 2 — the over-budget row must be retained, not dropped", idx.size())
	}
	rowA := indexRowCopy(t, idx, "a")
	if !rowA.stale {
		t.Fatal("the retained over-budget row must be marked stale")
	}
	if !vectorsEqual(rowA.vec, oldVecA) {
		t.Fatal("the retained row must keep its OLD vector, not a fresh embed of the new text")
	}

	// A later unbudgeted pass finally re-embeds "a"; it is no longer stale.
	if embedded, _, err := idx.reconcile(context.Background(), entries, 100); err != nil || embedded != 1 {
		t.Fatalf("catch-up reconcile embedded=%d err=%v, want 1", embedded, err)
	}
	if rowA := indexRowCopy(t, idx, "a"); rowA.stale {
		t.Fatal("a re-embedded row must no longer be stale")
	}
}

// TestFuseRawCandidatesSkipsHiddenSemanticCandidate (F29): a semantic-only
// candidate that is hidden from recall (e.g. a superseded/quarantined entry that
// still carries a vector) must be excluded from the fused band, not occupy a
// slot the caller then discards.
func TestFuseRawCandidatesSkipsHiddenSemanticCandidate(t *testing.T) {
	store, err := newMeetingMemoryStore(filepath.Join(t.TempDir(), "memory.jsonl"))
	if err != nil {
		t.Fatalf("newMeetingMemoryStore: %v", err)
	}
	store.entries = []meetingMemoryEntry{
		{ID: "live", Kind: meetingMemoryKindRunLog, Text: "Live run log.", CreatedAt: time.Now()},
		{ID: "hidden", Kind: meetingMemoryKindRunLog, Text: "Quarantined content.", CreatedAt: time.Now(),
			Metadata: map[string]string{relevanceMetadataKey: relevanceQuarantined}},
	}
	semantic := []embeddingHit{
		{id: "live", kind: meetingMemoryKindRunLog, score: 0.9},
		{id: "hidden", kind: meetingMemoryKindRunLog, score: 0.8},
	}
	fused := fuseRawCandidates(nil, semantic, store, 10)
	if !memoryEntriesContain(fused, "live") {
		t.Fatal("the live candidate must survive fusion")
	}
	if memoryEntriesContain(fused, "hidden") {
		t.Fatal("a hidden-from-recall candidate must be excluded from the fused band (F29)")
	}
}

// W0-5 lane metering (seat embeddings): the production embedder records one
// ledger row per API call — wire-reported prompt_tokens when present, a
// ~4-bytes/token estimate flagged Estimated when absent, and Error on failed
// calls. Drives the REAL request/decode path against a fake HTTP server (the
// openAIImagesURL var-seam precedent).
func TestOpenAIEmbeddingFuncRecordsUsage(t *testing.T) {
	dir := ledgerTestDir(t)
	fixed := time.Date(2026, time.July, 11, 20, 0, 0, 0, time.UTC)
	prevNow := usageLedgerNow
	usageLedgerNow = func() time.Time { return fixed }
	defer func() { usageLedgerNow = prevNow }()

	var modeMu sync.Mutex
	withUsage := true
	failNext := false
	setMode := func(usage bool, fail bool) {
		modeMu.Lock()
		withUsage, failNext = usage, fail
		modeMu.Unlock()
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		modeMu.Lock()
		usage, fail := withUsage, failNext
		modeMu.Unlock()
		if fail {
			http.Error(w, `{"error":{"message":"synthetic outage"}}`, http.StatusInternalServerError)
			return
		}
		var payload openAIEmbeddingsPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode embeddings request: %v", err)
		}
		data := make([]map[string]any, 0, len(payload.Input))
		for i := range payload.Input {
			data = append(data, map[string]any{"index": i, "embedding": []float32{0.1, 0.2}})
		}
		body := map[string]any{"data": data}
		if usage {
			body["usage"] = map[string]any{"prompt_tokens": 7, "total_tokens": 7}
		}
		json.NewEncoder(w).Encode(body)
	}))
	defer server.Close()
	originalURL := embeddingsOpenAIURL
	embeddingsOpenAIURL = server.URL
	t.Cleanup(func() { embeddingsOpenAIURL = originalURL })

	embed := openAIEmbeddingFunc("test-embed-key", "text-embedding-3-small")

	// 1. Wire-reported usage.
	vectors, err := embed(context.Background(), []string{"alpha", "beta"})
	if err != nil || len(vectors) != 2 {
		t.Fatalf("embed: vectors=%d err=%v", len(vectors), err)
	}
	// 2. No usage block -> byte-estimate flagged Estimated.
	setMode(false, false)
	if _, err := embed(context.Background(), []string{"alpha", "beta"}); err != nil {
		t.Fatalf("embed without usage: %v", err)
	}
	// 3. Failure -> Error stamped, still one row.
	setMode(false, true)
	if _, err := embed(context.Background(), []string{"alpha"}); err == nil {
		t.Fatal("synthetic outage must error")
	}

	rows := readLedgerLines(t, filepath.Join(dir, "usage-2026-07-11.jsonl"))
	if len(rows) != 3 {
		t.Fatalf("usage rows = %d, want one per API call", len(rows))
	}
	wired := rows[0]
	if wired["provider"] != providerOpenAI || wired["model"] != "text-embedding-3-small" || wired["seat"] != seatEmbeddings {
		t.Fatalf("wired row identity wrong: %v", wired)
	}
	if got := wired["input_tokens"].(float64); got != 7 {
		t.Fatalf("wired input_tokens = %v, want the reported 7", got)
	}
	if _, present := wired["estimated"]; present {
		t.Fatalf("wire-reported usage must not flag estimated: %v", wired)
	}
	if got := wired["est_cost_usd"].(float64); !floatClose(got, 7.0/1e6*0.02) {
		t.Fatalf("wired est_cost_usd = %v, want %v", got, 7.0/1e6*0.02)
	}
	estimated := rows[1]
	// len("alpha")+len("beta") = 9 bytes -> ceil(9/4) = 3 tokens.
	if got := estimated["input_tokens"].(float64); got != 3 {
		t.Fatalf("estimated input_tokens = %v, want 3", got)
	}
	if estimated["estimated"] != true {
		t.Fatalf("byte-estimate row must flag estimated: %v", estimated)
	}
	failed := rows[2]
	if errText, _ := failed["error"].(string); !strings.Contains(errText, "api request failed") {
		t.Fatalf("failed row must carry the wire error: %v", failed)
	}
}
