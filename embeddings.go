package main

// In-process semantic retrieval lane (memory-architecture study §6 item 2.4:
// "their channels 4-5 + fusion, minus the vendor").
//
// Two pieces work together:
//
//  1. The EMBEDDING MAINTAINER — an ambient own-loop worker (the taste-analyst /
//     house-style precedent, agent_runner.go: registered from startAmbientAgent,
//     silently absent keyless). Each pass it reconciles the consolidated
//     knowledge corpus (brains, digests, narratives, decisions, reflections,
//     run logs, notes, artifact title+excerpt — thousands of rows, NEVER the
//     transcript firehose and NEVER UI-state kinds like scout_chat_thread) into
//     a sidecar `data/embeddings.jsonl` and an in-RAM unit-normalized float32
//     matrix. New rows are embedded (lazy backfill, bounded per pass), edited
//     rows are re-embedded when their content hash changes, and rows whose entry
//     vanished are dropped.
//
//  2. The RETRIEVAL LANE — contextEntriesForQuery (memory_query.go) fuses the
//     lexical search candidates with this lane's cosine top-K using weighted
//     Reciprocal Rank Fusion, so a semantically-close but lexically-absent entry
//     ("the Korean TV thing" ↔ "Samsung TV Plus") joins the raw-candidate band
//     while the pinned ledger/digest lanes stay ABOVE, untouched (our stronger
//     form of Cloudflare's "fact-key weighs highest").
//
// Deliberately NO vector DB: brute-force cosine over an in-RAM matrix is
// sub-millisecond at our scale (thousands of rows × 1536 dims) even on the
// 2-vCPU droplet, and a vector DB would be pure operational overhead for a
// single-process company brain (study §6 "Do NOT adopt"). The sidecar is a
// plain JSONL that rides Wave B's data-dir snapshot automatically (it is a
// top-level regular file, so writeDataDirTarGz includes it — it is neither
// under backups/ nor blobs/).

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	embeddingMaintainerAgentName = "embedding_maintainer"

	// defaultEmbeddingModel + embeddingDims: OpenAI text-embedding-3-small
	// returns 1536-dim vectors. The study picked it explicitly (small, cheap,
	// strong for short-text recall); no existing embedding call exists to
	// mirror, so this file owns the wire.
	defaultEmbeddingModel = "text-embedding-3-small"
	embeddingDims         = 1536

	// defaultEmbeddingInterval is the maintainer's safety-floor sweep cadence.
	// Embedding is a background enrichment, not a latency surface — several
	// minutes of lag before a fresh brain is semantically searchable is fine, and
	// a longer cadence (F35) keeps the per-pass reconcile — which rewrites the
	// sidecar on any change — from firing every few minutes during live meetings.
	defaultEmbeddingInterval = 10 * time.Minute
	// defaultEmbeddingMaxBatch bounds how many NEW/CHANGED entries one pass
	// embeds, so a cold boot backfills lazily across passes instead of a single
	// giant burst of API calls. Newest entries are embedded first.
	defaultEmbeddingMaxBatch = 128
	// embeddingAPIBatchSize is how many inputs ride ONE embeddings API call.
	embeddingAPIBatchSize = 64
	// defaultEmbeddingLaneTopK is how many cosine neighbours the retrieval lane
	// pulls per query before fusion.
	defaultEmbeddingLaneTopK = 12
	// embeddingRRFK is the Reciprocal Rank Fusion constant (the standard 60):
	// fusedScore(d) = Σ_channel weight / (k + rank_channel(d)). A larger k
	// flattens the contribution of top ranks; 60 is the literature default.
	embeddingRRFK = 60.0
	// Fusion weights within the raw-candidate band. Lexical exact-token hits are
	// high precision; the semantic channel is high recall but fuzzier, so it
	// carries a slightly lighter weight — a strong lexical match is not displaced
	// by a merely-similar neighbour, while a lexical MISS still surfaces.
	defaultEmbeddingLexicalWeight  = 1.0
	defaultEmbeddingSemanticWeight = 0.85

	// embeddingRequestTimeout bounds one maintainer batch embed call;
	// embeddingQueryTimeout bounds the read-lane per-query embed (kept short —
	// it sits inline in the assistant answer path). A miss degrades the lane to
	// silence, never an error to the caller.
	embeddingRequestTimeout = 30 * time.Second
	embeddingQueryTimeout   = 6 * time.Second

	// embeddingExcerptRuneCap bounds the text sent to the embedding model per
	// entry: an excerpt, not the full body (artifact bodies are multi-KB and are
	// title+excerpt only). Well under the model's token limit and keeps cost and
	// the sidecar size bounded.
	embeddingExcerptRuneCap = 1200
	// embeddingQueryCacheCap bounds the per-query embedding cache (FIFO).
	embeddingQueryCacheCap = 256
	// embeddingQueryFailureCooldown opens a short breaker after a query-embed
	// failure (F10): during it the inline semantic-lane embed is skipped so an
	// OpenAI outage doesn't cost EVERY recall query (including the spoken voice
	// memory answer) the full embeddingQueryTimeout inline. A success clears it.
	embeddingQueryFailureCooldown = 60 * time.Second
)

// embeddingsOpenAIURL is a package VAR where most wire URLs are consts (the
// openAIImagesURL precedent): the W0-5 metering test drives the real request
// encoding + usage decode path against a fake HTTP server, so the seam is the
// endpoint itself.
var embeddingsOpenAIURL = "https://api.openai.com/v1/embeddings"

// embeddingCorpusKinds is the ALLOWLIST of consolidated knowledge kinds the
// maintainer embeds. Everything else — the transcript firehose, signals, and
// every UI-state kind (scout_chat_thread above all: private AND team-channel
// thread text must NEVER be embedded, per the study's privacy line) — is
// excluded by simply not appearing here. embeddingEligible re-checks
// isUIStateMemoryKind and memoryEntryHiddenFromRecall as defense in depth.
var embeddingCorpusKinds = map[string]struct{}{
	meetingMemoryKindBrain:         {},
	meetingMemoryKindMeetingDigest: {},
	meetingMemoryKindDayDigest:     {},
	meetingMemoryKindCompanyDigest: {},
	meetingMemoryKindNarrative:     {},
	meetingMemoryKindDecision:      {},
	meetingMemoryKindReflection:    {},
	meetingMemoryKindRunLog:        {},
	meetingMemoryKindNote:          {},
	meetingMemoryKindOSArtifact:    {}, // title + excerpt ONLY, never the full body
}

// embeddingFunc turns a batch of input strings into their vectors, in order.
// The production impl closes over the OpenAI key; tests inject a synthetic one
// so no test ever calls OpenAI.
type embeddingFunc func(ctx context.Context, inputs []string) ([][]float32, error)

// ---------------------------------------------------------------------------
// Config (env surface documented in deploy/digitalocean/.env.example)
// ---------------------------------------------------------------------------

func embeddingModel() string {
	if model := strings.TrimSpace(os.Getenv("EMBEDDINGS_MODEL")); model != "" {
		return model
	}
	return defaultEmbeddingModel
}

func embeddingsPath() string {
	if path := strings.TrimSpace(os.Getenv("EMBEDDINGS_PATH")); path != "" {
		return path
	}
	// Sidecar beside the main store, derived exactly like meetingMemoryPath but
	// NEVER the same file — it never touches the JSONL or its cursors.
	return filepath.Join(filepath.Dir(meetingMemoryPath()), "embeddings.jsonl")
}

func embeddingInterval() time.Duration {
	raw := strings.TrimSpace(os.Getenv("EMBEDDINGS_INTERVAL"))
	if raw == "" {
		return defaultEmbeddingInterval
	}
	switch strings.ToLower(raw) {
	case "0", "off", "false", "disabled":
		return 0
	}
	interval, err := time.ParseDuration(raw)
	if err != nil || interval < time.Second {
		return defaultEmbeddingInterval
	}
	return interval
}

func embeddingMaxBatch() int {
	return positiveIntEnv("EMBEDDINGS_MAX_BATCH", defaultEmbeddingMaxBatch)
}

func embeddingLaneTopK() int {
	return positiveIntEnv("EMBEDDINGS_LANE_TOPK", defaultEmbeddingLaneTopK)
}

func embeddingLexicalWeight() float64 {
	return floatEnv("EMBEDDINGS_LEXICAL_WEIGHT", defaultEmbeddingLexicalWeight)
}

func embeddingSemanticWeight() float64 {
	return floatEnv("EMBEDDINGS_SEMANTIC_WEIGHT", defaultEmbeddingSemanticWeight)
}

func floatEnv(name string, fallback float64) float64 {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || value < 0 {
		return fallback
	}
	return value
}

// ---------------------------------------------------------------------------
// Eligibility + embedding text
// ---------------------------------------------------------------------------

// embeddingEligible reports whether an entry belongs in the semantic corpus.
// Allowlist first, then the same recall guards store.search applies — so a
// quarantined/expired entry, or any UI-state kind that ever slipped into the
// allowlist by mistake, is still refused (the scout_chat_thread privacy pin).
func embeddingEligible(entry meetingMemoryEntry) bool {
	if strings.TrimSpace(entry.ID) == "" {
		return false
	}
	if _, ok := embeddingCorpusKinds[entry.Kind]; !ok {
		return false
	}
	if isUIStateMemoryKind(entry.Kind) {
		return false
	}
	if memoryEntryHiddenFromRecall(entry) {
		return false
	}
	if entry.Kind == meetingMemoryKindOSArtifact && !artifactReadyForContext(entry) {
		return false
	}
	return strings.TrimSpace(embeddingTextForEntry(entry)) != ""
}

// embeddingTextForEntry is the text handed to the embedding model: the entry's
// title + a bounded excerpt, plus its write-time digestAliases metadata (the
// study's write-time retrieval enrichment — "Samsung TV Plus" carries "the
// Korean TV deal"). Artifacts contribute title + a capped excerpt of the body,
// NEVER the full multi-KB body.
func embeddingTextForEntry(entry meetingMemoryEntry) string {
	var builder strings.Builder
	if title := strings.TrimSpace(entry.Metadata["title"]); title != "" {
		builder.WriteString(collapseEmbeddingWhitespace(title))
		builder.WriteString("\n")
	}
	builder.WriteString(collapseEmbeddingWhitespace(entry.Text))
	text := runeCap(strings.TrimSpace(builder.String()), embeddingExcerptRuneCap)
	if aliases := strings.TrimSpace(entry.Metadata[digestAliasesMetadataKey]); aliases != "" {
		text = strings.TrimSpace(text + "\n" + collapseEmbeddingWhitespace(aliases))
	}
	return text
}

func collapseEmbeddingWhitespace(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func runeCap(value string, limit int) string {
	if limit <= 0 {
		return value
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return strings.TrimSpace(string(runes[:limit]))
}

// embeddingContentHash is the change detector: a 128-bit hex digest of the
// embedding text so an edited artifact (new excerpt/aliases) re-embeds while an
// untouched entry is skipped. Content-hashed, not id-hashed, exactly because
// the payload — not the id — is what changed.
func embeddingContentHash(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:16])
}

// ---------------------------------------------------------------------------
// Sidecar row + in-RAM index
// ---------------------------------------------------------------------------

// embeddingSidecarRow is one persisted line of data/embeddings.jsonl. The
// vector is base64 of little-endian float32s (compact: ~8KB vs ~15KB as a JSON
// float array) and is stored UNIT-NORMALIZED so cosine similarity is a plain
// dot product at query time.
type embeddingSidecarRow struct {
	ID          string    `json:"id"`
	Kind        string    `json:"kind"`
	Dims        int       `json:"dims"`
	Vector      string    `json:"vector"`
	ContentHash string    `json:"contentHash"`
	EmbeddedAt  time.Time `json:"embeddedAt"`
	// Stale marks a vector whose content has since changed but which is retained
	// (F26) because the re-embed was over this pass's budget — usable, just aging.
	Stale bool `json:"stale,omitempty"`
}

type embeddingRow struct {
	id         string
	kind       string
	hash       string
	embeddedAt time.Time
	vec        []float32 // unit-normalized
	// stale is set when hash no longer matches the current content but the vector
	// is kept until a successful re-embed replaces it (F26).
	stale bool
}

type embeddingIndex struct {
	mu    sync.RWMutex
	dims  int
	rows  []embeddingRow
	byID  map[string]int
	path  string
	model string

	embedder embeddingFunc // nil keyless → the lane silently contributes nothing

	queryMu    sync.Mutex
	queryCache map[string][]float32
	queryOrder []string

	// queryFailUntil is the F10 breaker: unixnano before which the inline query
	// embed is skipped (0 == closed). Set on a failed embed, cleared on success.
	queryFailUntil atomic.Int64
}

func newEmbeddingIndex(path string, dims int, model string, embedder embeddingFunc) *embeddingIndex {
	return &embeddingIndex{
		dims:       dims,
		byID:       map[string]int{},
		path:       path,
		model:      model,
		embedder:   embedder,
		queryCache: map[string][]float32{},
	}
}

func (idx *embeddingIndex) size() int {
	if idx == nil {
		return 0
	}
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return len(idx.rows)
}

// load reads the sidecar into the in-RAM matrix. A missing file is not an
// error (first boot); a malformed or wrong-dimension line is skipped, matching
// the store's per-line resilience.
func (idx *embeddingIndex) load() error {
	if idx == nil {
		return nil
	}
	data, err := os.ReadFile(idx.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read embeddings sidecar: %w", err)
	}
	rows := make([]embeddingRow, 0, 256)
	byID := map[string]int{}
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		var row embeddingSidecarRow
		if err := json.Unmarshal([]byte(trimmed), &row); err != nil {
			log.Warnf("embedding maintainer: skipping malformed sidecar row: %v", err)
			continue
		}
		if strings.TrimSpace(row.ID) == "" || row.Dims != idx.dims {
			continue
		}
		vec, err := decodeEmbeddingVector(row.Vector, idx.dims)
		if err != nil {
			log.Warnf("embedding maintainer: skipping undecodable vector for %s: %v", row.ID, err)
			continue
		}
		if _, dup := byID[row.ID]; dup {
			continue
		}
		byID[row.ID] = len(rows)
		rows = append(rows, embeddingRow{
			id:         row.ID,
			kind:       row.Kind,
			hash:       row.ContentHash,
			embeddedAt: row.EmbeddedAt,
			vec:        vec,
			stale:      row.Stale,
		})
	}
	idx.mu.Lock()
	idx.rows = rows
	idx.byID = byID
	idx.mu.Unlock()
	return nil
}

// persist rewrites the whole sidecar atomically (temp + rename). Whole-file
// rewrite is acceptable at our scale: passes are ~10 minutes apart and only fire
// a rewrite when something changed. The file can reach tens of MB once the
// corpus is large (thousands of rows × a ~8KB base64 vector each), which is why
// the sweep cadence is unhurried (F35) rather than every few minutes.
func (idx *embeddingIndex) persist() error {
	if idx == nil {
		return nil
	}
	idx.mu.RLock()
	rows := make([]embeddingRow, len(idx.rows))
	copy(rows, idx.rows)
	dims := idx.dims
	path := idx.path
	idx.mu.RUnlock()

	var builder strings.Builder
	for _, row := range rows {
		line, err := json.Marshal(embeddingSidecarRow{
			ID:          row.id,
			Kind:        row.kind,
			Dims:        dims,
			Vector:      encodeEmbeddingVector(row.vec),
			ContentHash: row.hash,
			EmbeddedAt:  row.embeddedAt,
			Stale:       row.stale,
		})
		if err != nil {
			return fmt.Errorf("encode embedding row: %w", err)
		}
		builder.Write(line)
		builder.WriteByte('\n')
	}
	if err := writeFileAtomicallyForCanonicalMode(path, []byte(builder.String()), 0o600); err != nil {
		return fmt.Errorf("persist embeddings sidecar: %w", err)
	}
	return nil
}

// topK returns the cosine-nearest ids for a (raw) query vector. Query is
// normalized here; stored vectors are already unit-normalized, so similarity is
// a dot product. Brute force over the whole matrix — milliseconds at our scale.
func (idx *embeddingIndex) topK(query []float32, k int) []embeddingHit {
	if idx == nil || k <= 0 {
		return nil
	}
	q := append([]float32(nil), query...)
	normalizeEmbeddingVector(q)
	idx.mu.RLock()
	hits := make([]embeddingHit, 0, len(idx.rows))
	for i := range idx.rows {
		hits = append(hits, embeddingHit{
			id:    idx.rows[i].id,
			kind:  idx.rows[i].kind,
			score: dotProduct(q, idx.rows[i].vec),
		})
	}
	idx.mu.RUnlock()
	sort.SliceStable(hits, func(i, j int) bool {
		return hits[i].score > hits[j].score
	})
	if len(hits) > k {
		hits = hits[:k]
	}
	return hits
}

// queryEmbedding returns the (cached) vector for a query, embedding it on a
// short deadline. Keyless (embedder nil) or on API failure it returns nil and
// the lane silently contributes nothing.
func (idx *embeddingIndex) queryEmbedding(query string) []float32 {
	if idx == nil || idx.embedder == nil || strings.TrimSpace(query) == "" {
		return nil
	}
	idx.queryMu.Lock()
	if cached, ok := idx.queryCache[query]; ok {
		idx.queryMu.Unlock()
		return cached
	}
	idx.queryMu.Unlock()

	// F10: while the breaker is open (a recent embed failed), skip the live embed
	// so a provider outage doesn't cost every query the full inline timeout. The
	// cache above still serves; only the uncached live embed is short-circuited.
	if until := idx.queryFailUntil.Load(); until != 0 && time.Now().UnixNano() < until {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), embeddingQueryTimeout)
	defer cancel()
	vectors, err := idx.embedder(ctx, []string{query})
	if err != nil || len(vectors) == 0 || len(vectors[0]) != idx.dims {
		if err != nil {
			idx.queryFailUntil.Store(time.Now().Add(embeddingQueryFailureCooldown).UnixNano())
			recordCapabilityFailure(capabilityEmbedding, time.Now().UTC(), err)
			log.Warnf("embedding maintainer: query embed failed (semantic lane skipped ~%s): %v", embeddingQueryFailureCooldown, err)
		}
		return nil
	}
	idx.queryFailUntil.Store(0) // success closes any open breaker
	recordCapabilitySuccess(capabilityEmbedding, time.Now().UTC())
	vec := vectors[0]
	idx.queryMu.Lock()
	idx.queryCache[query] = vec
	idx.queryOrder = append(idx.queryOrder, query)
	if len(idx.queryOrder) > embeddingQueryCacheCap {
		evict := idx.queryOrder[0]
		idx.queryOrder = idx.queryOrder[1:]
		delete(idx.queryCache, evict)
	}
	idx.queryMu.Unlock()
	return vec
}

type embeddingHit struct {
	id    string
	kind  string
	score float32
}

// ---------------------------------------------------------------------------
// Reconciliation (backfill / re-embed / drop)
// ---------------------------------------------------------------------------

type embeddingCandidate struct {
	id        string
	kind      string
	text      string
	hash      string
	createdAt time.Time
}

// reconcile brings the index in line with the current eligible corpus: keep
// rows whose content hash is unchanged, re-embed changed rows, embed new rows
// (bounded to maxBatch this pass — lazy backfill, newest first), and drop rows
// whose entry vanished. Persists only when something changed.
func (idx *embeddingIndex) reconcile(ctx context.Context, entries []meetingMemoryEntry, maxBatch int) (embedded int, dropped int, err error) {
	if idx == nil {
		return 0, 0, nil
	}
	// Desired set + a fast lookup of what we already hold.
	desiredHash := make(map[string]string, len(entries))
	desiredOrder := make([]meetingMemoryEntry, 0, len(entries))
	for _, entry := range entries {
		if !embeddingEligible(entry) {
			continue
		}
		if _, dup := desiredHash[entry.ID]; dup {
			continue
		}
		desiredHash[entry.ID] = embeddingContentHash(embeddingTextForEntry(entry))
		desiredOrder = append(desiredOrder, entry)
	}

	idx.mu.RLock()
	existing := make(map[string]embeddingRow, len(idx.rows))
	for _, row := range idx.rows {
		existing[row.id] = row
	}
	idx.mu.RUnlock()

	kept := make([]embeddingRow, 0, len(desiredOrder))
	candidates := make([]embeddingCandidate, 0)
	for _, entry := range desiredOrder {
		hash := desiredHash[entry.ID]
		if row, ok := existing[entry.ID]; ok && row.hash == hash && len(row.vec) == idx.dims {
			row.stale = false // hash matches current content: the vector is fresh
			kept = append(kept, row)
			continue
		}
		candidates = append(candidates, embeddingCandidate{
			id:        entry.ID,
			kind:      entry.Kind,
			text:      embeddingTextForEntry(entry),
			hash:      hash,
			createdAt: entry.CreatedAt,
		})
	}
	// Newest first: recent knowledge is likeliest to be queried, so it should
	// win the bounded per-pass embed budget on a cold backfill.
	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].createdAt.After(candidates[j].createdAt)
	})
	// Only the newest maxBatch candidates embed this pass; the rest wait. The
	// full candidate list is retained so the over-budget ones can keep their
	// previous vector (F26) instead of vanishing.
	batch := candidates
	if maxBatch > 0 && len(batch) > maxBatch {
		batch = batch[:maxBatch]
	}

	newRows := kept
	embeddedIDs := make(map[string]struct{}, len(batch))
	if len(batch) > 0 && idx.embedder != nil {
		texts := make([]string, len(batch))
		for i, cand := range batch {
			texts[i] = cand.text
		}
		vectors, embedErr := embedInBatches(ctx, idx.embedder, texts, embeddingAPIBatchSize)
		if embedErr != nil {
			// Partial progress (whatever batches succeeded) is still applied
			// below; the rest retries next pass. Surface the error to the loop
			// for logging but never wedge the cursor.
			err = embedErr
		}
		for i := range vectors {
			if i >= len(batch) {
				break
			}
			vec := vectors[i]
			if len(vec) != idx.dims {
				continue
			}
			normalizeEmbeddingVector(vec)
			newRows = append(newRows, embeddingRow{
				id:         batch[i].id,
				kind:       batch[i].kind,
				hash:       batch[i].hash,
				embeddedAt: time.Now().UTC(),
				vec:        vec,
			})
			embeddedIDs[batch[i].id] = struct{}{}
			embedded++
		}
	}

	// F26: a changed row we did NOT freshly embed this pass — over budget, or its
	// batch failed — keeps its previous vector (marked stale) rather than
	// vanishing from the index until some later pass gets to it. Its stored hash
	// stays the OLD hash, so it remains a re-embed candidate next pass; a
	// successful re-embed above already replaced it (it's in embeddedIDs) and is
	// skipped here.
	for _, cand := range candidates {
		if _, freshlyEmbedded := embeddedIDs[cand.id]; freshlyEmbedded {
			continue
		}
		if old, ok := existing[cand.id]; ok && len(old.vec) == idx.dims {
			old.stale = true
			newRows = append(newRows, old)
		}
	}

	// Truly vanished = previously indexed but no longer desired (a re-embedded
	// entry is still desired, so it is NOT a drop).
	for id := range existing {
		if _, ok := desiredHash[id]; !ok {
			dropped++
		}
	}
	changed := embedded > 0 || dropped > 0

	if changed {
		byID := make(map[string]int, len(newRows))
		for i, row := range newRows {
			byID[row.id] = i
		}
		idx.mu.Lock()
		idx.rows = newRows
		idx.byID = byID
		idx.mu.Unlock()
		if persistErr := idx.persist(); persistErr != nil {
			log.Errorf("embedding maintainer: sidecar persist failed: %v", persistErr)
			if err == nil {
				err = persistErr
			}
		}
	}
	return embedded, dropped, err
}

// ---------------------------------------------------------------------------
// Vector math + OpenAI wire
// ---------------------------------------------------------------------------

func dotProduct(a, b []float32) float32 {
	if len(a) != len(b) {
		return 0
	}
	var sum float32
	for i := range a {
		sum += a[i] * b[i]
	}
	return sum
}

func normalizeEmbeddingVector(vec []float32) {
	var sum float64
	for _, f := range vec {
		sum += float64(f) * float64(f)
	}
	if sum == 0 {
		return
	}
	inv := float32(1 / math.Sqrt(sum))
	for i := range vec {
		vec[i] *= inv
	}
}

func encodeEmbeddingVector(vec []float32) string {
	buf := make([]byte, len(vec)*4)
	for i, f := range vec {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(f))
	}
	return base64.StdEncoding.EncodeToString(buf)
}

func decodeEmbeddingVector(encoded string, dims int) ([]float32, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		return nil, err
	}
	if len(raw) != dims*4 {
		return nil, fmt.Errorf("vector has %d bytes, want %d", len(raw), dims*4)
	}
	vec := make([]float32, dims)
	for i := range vec {
		vec[i] = math.Float32frombits(binary.LittleEndian.Uint32(raw[i*4:]))
	}
	return vec, nil
}

// embedInBatches chunks inputs into API-sized batches, preserving order. On the
// first failing batch it returns the vectors gathered so far plus the error, so
// a partial pass still makes progress.
func embedInBatches(ctx context.Context, embed embeddingFunc, inputs []string, batchSize int) ([][]float32, error) {
	if batchSize <= 0 {
		batchSize = embeddingAPIBatchSize
	}
	out := make([][]float32, 0, len(inputs))
	for start := 0; start < len(inputs); start += batchSize {
		end := start + batchSize
		if end > len(inputs) {
			end = len(inputs)
		}
		vectors, err := embed(ctx, inputs[start:end])
		if err != nil {
			return out, err
		}
		out = append(out, vectors...)
	}
	return out, nil
}

type openAIEmbeddingsPayload struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type openAIEmbeddingsBody struct {
	Data []struct {
		Index     int       `json:"index"`
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
	// Usage rides every embeddings response; the W0-5 metering records
	// prompt_tokens per call (seat embeddings).
	Usage struct {
		PromptTokens int64 `json:"prompt_tokens"`
		TotalTokens  int64 `json:"total_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message,omitempty"`
	} `json:"error,omitempty"`
}

// openAIEmbeddingFunc returns the production embedder — a closure over the key
// and model that POSTs to the OpenAI embeddings endpoint, mirroring the HTTP
// idioms in openai_responses.go (Bearer auth, bounded body read, status check).
func openAIEmbeddingFunc(apiKey string, model string) embeddingFunc {
	apiKey = strings.TrimSpace(apiKey)
	return func(ctx context.Context, inputs []string) (vectors [][]float32, err error) {
		if apiKey == "" {
			return nil, fmt.Errorf("OPENAI_API_KEY is not configured")
		}
		if len(inputs) == 0 {
			return nil, nil
		}
		rawPayload, err := json.Marshal(openAIEmbeddingsPayload{Model: model, Input: inputs})
		if err != nil {
			return nil, fmt.Errorf("encode embeddings request: %w", err)
		}
		if ctx == nil {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(context.Background(), embeddingRequestTimeout)
			defer cancel()
		}
		httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, embeddingsOpenAIURL, strings.NewReader(string(rawPayload)))
		if err != nil {
			return nil, fmt.Errorf("create embeddings request: %w", err)
		}
		httpRequest.Header.Set("Authorization", "Bearer "+apiKey)
		httpRequest.Header.Set("Content-Type", "application/json")

		// W0-5 lane metering (seat embeddings): one ledger row per API call —
		// wire prompt_tokens when the response reports them, else a ~4-bytes/
		// token estimate flagged Estimated; failed calls carry Error (they cost
		// latency and often still bill).
		started := time.Now()
		promptTokens := int64(0)
		defer func() {
			entry := llmUsageEntry{
				Provider:    providerOpenAI,
				Model:       model,
				Seat:        seatEmbeddings,
				InputTokens: promptTokens,
				DurationMS:  time.Since(started).Milliseconds(),
			}
			if entry.InputTokens == 0 {
				entry.InputTokens = estimateEmbeddingInputTokens(inputs)
				entry.Estimated = true
			}
			if err != nil {
				entry.Error = err.Error()
			}
			recordLLMUsage(entry)
		}()

		response, err := (&http.Client{Timeout: embeddingRequestTimeout}).Do(httpRequest)
		if err != nil {
			return nil, fmt.Errorf("create embeddings response: %w", err)
		}
		defer response.Body.Close()
		rawBody, err := io.ReadAll(io.LimitReader(response.Body, 32*1024*1024))
		if err != nil {
			return nil, fmt.Errorf("read embeddings response: %w", err)
		}
		if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
			return nil, apiRequestFailedError("OpenAI embeddings failed", response.Status, rawBody)
		}
		var body openAIEmbeddingsBody
		if err := json.Unmarshal(rawBody, &body); err != nil {
			return nil, fmt.Errorf("decode embeddings response: %w", err)
		}
		promptTokens = body.Usage.PromptTokens
		if body.Error != nil && strings.TrimSpace(body.Error.Message) != "" {
			return nil, fmt.Errorf("OpenAI embeddings error: %s", strings.TrimSpace(body.Error.Message))
		}
		out := make([][]float32, len(inputs))
		for _, item := range body.Data {
			if item.Index < 0 || item.Index >= len(out) {
				continue
			}
			out[item.Index] = item.Embedding
		}
		for i := range out {
			if out[i] == nil {
				return nil, fmt.Errorf("embeddings response missing vector %d of %d", i, len(inputs))
			}
		}
		return out, nil
	}
}

// estimateEmbeddingInputTokens is the fallback meter when the wire reports no
// usage: ~4 bytes per token, rounded up, summed across the batch.
func estimateEmbeddingInputTokens(inputs []string) int64 {
	total := 0
	for _, input := range inputs {
		total += len(input)
	}
	return int64((total + 3) / 4)
}

// ---------------------------------------------------------------------------
// Ambient maintainer loop (own-loop worker; the taste-analyst precedent)
// ---------------------------------------------------------------------------

// embeddingKeylessBootLog gates the single "lexical-only" boot line so it can
// never spam across the many registration calls.
var embeddingKeylessBootLog sync.Once

// ensureEmbeddingMaintainerStarted is the registration seam called from
// startAmbientAgent (agent_runner.go), alongside the taste-analyst / house-style
// distillers. Idempotent via the agent bookkeeping map. Because startAmbientAgent
// early-returns when the OpenAI key is empty, this only runs on keyed boots; a
// keyless deploy never reaches it, so the index stays nil and the retrieval lane
// degrades to lexical-only.
func (app *kanbanBoardApp) ensureEmbeddingMaintainerStarted(apiKey string) {
	if app == nil {
		return
	}
	app.mu.Lock()
	_, registered := app.agentCancels[embeddingMaintainerAgentName]
	app.mu.Unlock()
	if registered {
		return
	}
	app.startEmbeddingMaintainer(apiKey)
}

// startEmbeddingMaintainer builds + publishes the index and launches the loop.
// Keyless or disabled it silently never starts (one boot log line noting the
// lexical-only degradation), the goal-engine posture.
func (app *kanbanBoardApp) startEmbeddingMaintainer(apiKey string) {
	if app == nil || app.memory == nil || boolEnv("EMBEDDINGS_DISABLED") {
		return
	}
	if strings.TrimSpace(apiKey) == "" {
		embeddingKeylessBootLog.Do(func() {
			log.Infof("embedding maintainer: OPENAI_API_KEY unset; semantic recall lane disabled (lexical-only)")
		})
		return
	}
	interval := embeddingInterval()
	if interval <= 0 {
		return
	}

	model := embeddingModel()
	idx := newEmbeddingIndex(embeddingsPath(), embeddingDims, model, openAIEmbeddingFunc(apiKey, model))
	if err := idx.load(); err != nil {
		log.Warnf("embedding maintainer: sidecar load failed (starting empty): %v", err)
	}
	publishEmbeddingIndex(idx)
	log.Infof("embedding maintainer: loaded %d vector(s) (model=%s, corpus=%d kinds, topK=%d)", idx.size(), model, len(embeddingCorpusKinds), embeddingLaneTopK())

	cancel := make(chan struct{})
	done := make(chan struct{})
	app.mu.Lock()
	if app.agentCancels == nil {
		app.agentCancels = map[string]chan struct{}{}
		app.agentDones = map[string]chan struct{}{}
	}
	oldCancel := app.agentCancels[embeddingMaintainerAgentName]
	oldDone := app.agentDones[embeddingMaintainerAgentName]
	app.agentCancels[embeddingMaintainerAgentName] = cancel
	app.agentDones[embeddingMaintainerAgentName] = done
	app.mu.Unlock()
	if oldCancel != nil {
		close(oldCancel)
		if oldDone != nil {
			<-oldDone
		}
	}

	go app.runEmbeddingMaintainerLoop(idx, interval, cancel, done)
}

func (app *kanbanBoardApp) runEmbeddingMaintainerLoop(idx *embeddingIndex, interval time.Duration, cancel <-chan struct{}, done chan<- struct{}) {
	defer close(done)

	// An immediate pass backfills promptly on first boot instead of waiting a
	// full interval for the empty (or stale) sidecar to catch up.
	if err := app.runEmbeddingMaintainerOnce(idx); err != nil {
		log.Errorf("embedding maintainer pass failed: %v", err)
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if err := app.runEmbeddingMaintainerOnce(idx); err != nil {
				log.Errorf("embedding maintainer pass failed: %v", err)
			}
		case <-cancel:
			return
		}
	}
}

// eligibleEmbeddingEntriesSnapshot returns cloned copies of ONLY the
// embedding-eligible entries, taken under the store lock. It replaces the
// whole-store deep clone in the maintainer pass (F35) so the transcript firehose
// is never copied. reconcile re-checks embeddingEligible on these clones, so the
// desired set (and thus the drop computation) is byte-for-byte what a full clone
// would have produced.
func (store *meetingMemoryStore) eligibleEmbeddingEntriesSnapshot() []meetingMemoryEntry {
	if store == nil {
		return nil
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	out := make([]meetingMemoryEntry, 0, 256)
	for _, entry := range store.entries {
		if embeddingEligible(entry) {
			out = append(out, cloneMemoryEntry(entry))
		}
	}
	return out
}

// runEmbeddingMaintainerOnce reconciles the whole eligible corpus into the
// index once. Serialized by the shared per-agent run lock so overlapping ticks
// never double-embed.
func (app *kanbanBoardApp) runEmbeddingMaintainerOnce(idx *embeddingIndex) error {
	if app == nil || app.memory == nil || idx == nil {
		return nil
	}
	runLock := app.ambientAgentRunLock(embeddingMaintainerAgentName)
	runLock.Lock()
	defer runLock.Unlock()

	// Snapshot ONLY the embedding-eligible entries (F35): the transcript firehose
	// — the store's bulk and its fastest-growing part during a live meeting — is
	// never eligible, so a whole-store deep clone every pass was pure waste.
	// Deletions of eligible rows are still detected downstream: a vanished (or
	// superseded) entry is simply absent here, so reconcile drops it.
	entries := app.memory.eligibleEmbeddingEntriesSnapshot()

	ctx, cancel := context.WithTimeout(context.Background(), embeddingRequestTimeout+15*time.Second)
	defer cancel()
	embedded, dropped, err := idx.reconcile(ctx, entries, embeddingMaxBatch())
	if embedded > 0 || dropped > 0 {
		log.Infof("embedding maintainer: embedded %d, dropped %d; index now %d", embedded, dropped, idx.size())
	}
	if err != nil {
		recordCapabilityFailure(capabilityEmbedding, time.Now().UTC(), err)
		return err
	}
	recordCapabilitySuccess(capabilityEmbedding, time.Now().UTC())
	return nil
}

// ---------------------------------------------------------------------------
// Retrieval lane: the process-wide active index + fusion
// ---------------------------------------------------------------------------

// activeEmbeddingIndex is the process-wide published index. It is a package
// global (not a store/app field) because contextEntriesForQuery is a store
// method with no app handle and memory.go's store struct is owned elsewhere;
// atomic.Pointer gives lock-free publication and nil-safe keyless reads.
var activeEmbeddingIndex atomic.Pointer[embeddingIndex]

func publishEmbeddingIndex(idx *embeddingIndex) {
	activeEmbeddingIndex.Store(idx)
}

func loadedEmbeddingIndex() *embeddingIndex {
	return activeEmbeddingIndex.Load()
}

// semanticLaneCandidates returns the cosine top-K neighbours for a query, or
// nil when the lane is absent (keyless — no published index) or the query embed
// fails. Never errors: the retrieval lane degrades to lexical-only.
func semanticLaneCandidates(query string, topK int) []embeddingHit {
	idx := loadedEmbeddingIndex()
	if idx == nil {
		return nil
	}
	normalized := normalizeMemoryText(canonicalizeDomainTerms(query))
	if normalized == "" {
		return nil
	}
	vec := idx.queryEmbedding(normalized)
	if vec == nil {
		return nil
	}
	return idx.topK(vec, topK)
}

// fuseRawCandidates weighted-RRF-fuses the lexical search matches with the
// semantic neighbours into one ordered raw-candidate band (recency tiebreak),
// capped at limit — mirroring the slot share the lexical search lane took. This
// fuses ONLY the raw candidates; the pinned ledger/digest lanes stay above,
// untouched. With an empty semantic side (keyless) the result is the lexical
// matches in their original score order, so behaviour is unchanged.
func fuseRawCandidates(lexical []meetingMemoryMatch, semantic []embeddingHit, store *meetingMemoryStore, limit int) []meetingMemoryEntry {
	if limit <= 0 {
		return nil
	}
	scores := make(map[string]float64, len(lexical)+len(semantic))
	entries := make(map[string]meetingMemoryEntry, len(lexical)+len(semantic))

	lexWeight := embeddingLexicalWeight()
	for rank, match := range lexical {
		id := strings.TrimSpace(match.Entry.ID)
		if id == "" {
			continue
		}
		scores[id] += lexWeight / (embeddingRRFK + float64(rank+1))
		if _, ok := entries[id]; !ok {
			entries[id] = match.Entry
		}
	}
	semWeight := embeddingSemanticWeight()
	for rank, hit := range semantic {
		id := strings.TrimSpace(hit.id)
		if id == "" {
			continue
		}
		if _, ok := entries[id]; !ok {
			// A semantic-only candidate: resolve the full entry from the store
			// (it may be older than any recent snapshot). Refuse UI-state
			// defensively even though the corpus never embeds it, and refuse a
			// hidden-from-recall entry (F29 — a superseded digest still carries a
			// vector until the maintainer drops it; without this it takes a fused
			// slot the caller then discards, starving a live candidate).
			entry, ok := store.entryByID(id)
			if !ok || isUIStateMemoryKind(entry.Kind) || memoryEntryHiddenFromRecall(entry) {
				continue
			}
			entries[id] = entry
		}
		scores[id] += semWeight / (embeddingRRFK + float64(rank+1))
	}

	ordered := make([]meetingMemoryEntry, 0, len(entries))
	for _, entry := range entries {
		ordered = append(ordered, entry)
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		si, sj := scores[ordered[i].ID], scores[ordered[j].ID]
		if si != sj {
			return si > sj
		}
		return ordered[i].CreatedAt.After(ordered[j].CreatedAt)
	})
	if len(ordered) > limit {
		ordered = ordered[:limit]
	}
	return ordered
}
