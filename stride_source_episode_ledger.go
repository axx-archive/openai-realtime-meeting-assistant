package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	sourceEpisodeLedgerFormat         = 1
	sourceEpisodeLedgerMaxRecordBytes = 2 << 20
	SourceEpisodeTombstonePurge       = "purge"
	SourceEpisodeTombstoneRetraction  = "source_retracted"
	SourceEpisodeTombstoneCorrection  = "source_corrected"
	SourceEpisodeTombstoneConsent     = "consent_withdrawn"
	SourceEpisodeTombstoneACL         = "acl_revoked"
)

type SourceEpisodeTombstone struct {
	TenantID             string          `json:"tenantId"`
	Episode              STRIDEReference `json:"episode"`
	Cause                string          `json:"cause"`
	PurgeGeneration      int64           `json:"purgeGeneration"`
	ReasonDigest         string          `json:"reasonDigest"`
	IdempotencyKeyDigest string          `json:"idempotencyKeyDigest"`
	OccurredAt           time.Time       `json:"occurredAt"`
}

type SourceEpisodeTenantPurge struct {
	TenantID             string    `json:"tenantId"`
	PurgeGeneration      int64     `json:"purgeGeneration"`
	ReasonDigest         string    `json:"reasonDigest"`
	IdempotencyKeyDigest string    `json:"idempotencyKeyDigest"`
	OccurredAt           time.Time `json:"occurredAt"`
}

func (purge SourceEpisodeTenantPurge) Validate() error {
	if !strideIdentifier(purge.TenantID) || purge.PurgeGeneration < 1 || !isHexDigest(purge.ReasonDigest) ||
		purge.IdempotencyKeyDigest != sha256Hex([]byte(fmt.Sprintf("source-episode-tenant-purge/v1\x00%s\x00%d", purge.TenantID, purge.PurgeGeneration))) ||
		purge.OccurredAt.IsZero() || purge.OccurredAt.Location() != time.UTC {
		return ErrSourceEpisodeInvalid
	}
	return nil
}

func (tombstone SourceEpisodeTombstone) Validate() error {
	if !strideIdentifier(tombstone.TenantID) || tombstone.Episode.Validate() != nil || tombstone.Episode.ContractType != STRIDEContractSourceEpisode ||
		!oneOf(tombstone.Cause, SourceEpisodeTombstonePurge, SourceEpisodeTombstoneRetraction, SourceEpisodeTombstoneCorrection, SourceEpisodeTombstoneConsent, SourceEpisodeTombstoneACL) ||
		tombstone.PurgeGeneration < 0 || !isHexDigest(tombstone.ReasonDigest) || !isHexDigest(tombstone.IdempotencyKeyDigest) ||
		tombstone.OccurredAt.IsZero() || tombstone.OccurredAt.Location() != time.UTC {
		return ErrSourceEpisodeInvalid
	}
	want := SourceEpisodeTombstoneIdempotencyKey(tombstone.TenantID, tombstone.Episode, tombstone.Cause, tombstone.PurgeGeneration)
	if tombstone.IdempotencyKeyDigest != want {
		return ErrSourceEpisodeInvalid
	}
	return nil
}

func SourceEpisodeTombstoneIdempotencyKey(tenantID string, episode STRIDEReference, cause string, purgeGeneration int64) string {
	digest, _ := STRIDEContractDigest(struct {
		TenantID        string
		Episode         STRIDEReference
		Cause           string
		PurgeGeneration int64
	}{tenantID, episode, cause, purgeGeneration})
	return digest
}

type sourceEpisodeLedgerRecord struct {
	Format         int                       `json:"format"`
	Sequence       uint64                    `json:"sequence"`
	Kind           string                    `json:"kind"`
	Episode        *SourceEpisode            `json:"episode,omitempty"`
	Tombstone      *SourceEpisodeTombstone   `json:"tombstone,omitempty"`
	TenantPurge    *SourceEpisodeTenantPurge `json:"tenantPurge,omitempty"`
	PreviousDigest string                    `json:"previousDigest,omitempty"`
	RecordDigest   string                    `json:"recordDigest"`
	RecordedAt     time.Time                 `json:"recordedAt"`
}

func (record sourceEpisodeLedgerRecord) Validate() error {
	if record.Format != sourceEpisodeLedgerFormat || record.Sequence == 0 || !oneOf(record.Kind, "episode", "tombstone", "tenant_purge") ||
		!validOptionalDigest(record.PreviousDigest) || !isHexDigest(record.RecordDigest) || record.RecordedAt.IsZero() || record.RecordedAt.Location() != time.UTC ||
		(record.Kind == "episode") != (record.Episode != nil) || (record.Kind == "tombstone") != (record.Tombstone != nil) || (record.Kind == "tenant_purge") != (record.TenantPurge != nil) {
		return ErrSourceEpisodeInvalid
	}
	if record.Episode != nil && record.Episode.Validate() != nil || record.Tombstone != nil && record.Tombstone.Validate() != nil || record.TenantPurge != nil && record.TenantPurge.Validate() != nil {
		return ErrSourceEpisodeInvalid
	}
	digest, err := sourceEpisodeLedgerRecordDigest(record)
	if err != nil || digest != record.RecordDigest {
		return ErrSourceEpisodeInvalid
	}
	return nil
}

// AdvanceTenantPurgeGeneration durably invalidates every older-generation
// episode, including when the tenant currently has no active episode.
func (ledger *FileSourceEpisodeLedger) AdvanceTenantPurgeGeneration(_ context.Context, tenantID string, generation int64, at time.Time) error {
	if ledger == nil || !strideIdentifier(tenantID) || generation < 0 || at.IsZero() {
		return ErrSourceEpisodeInvalid
	}
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	if generation == ledger.purge[tenantID] {
		return nil
	}
	if generation < ledger.purge[tenantID] || generation == 0 {
		return ErrSourceEpisodeConflict
	}
	idempotency := sha256Hex([]byte(fmt.Sprintf("source-episode-tenant-purge/v1\x00%s\x00%d", tenantID, generation)))
	purge := SourceEpisodeTenantPurge{TenantID: tenantID, PurgeGeneration: generation, ReasonDigest: sha256Hex([]byte("canonical-purge-ledger")), IdempotencyKeyDigest: idempotency, OccurredAt: at.UTC()}
	return ledger.appendLocked(sourceEpisodeLedgerRecord{Format: sourceEpisodeLedgerFormat, Kind: "tenant_purge", TenantPurge: &purge, RecordedAt: ledger.recordedAtLocked()})
}

func sourceEpisodeLedgerRecordDigest(record sourceEpisodeLedgerRecord) (string, error) {
	record.RecordDigest = ""
	return STRIDEContractDigest(record)
}

type FileSourceEpisodeLedger struct {
	mu          sync.Mutex
	path        string
	records     []sourceEpisodeLedgerRecord
	idempotency map[string]sourceEpisodeLedgerRecord
	latest      map[string]SourceEpisode
	active      map[string]SourceEpisode
	purge       map[string]int64
	write       func(string, []byte) error
	now         func() time.Time
	poisoned    error
}

var _ SourceEpisodeDualWriter = (*FileSourceEpisodeLedger)(nil)
var _ DurableSourceEpisodeCatalog = (*FileSourceEpisodeLedger)(nil)
var _ BrainPurgeGenerationResolver = (*FileSourceEpisodeLedger)(nil)

func OpenFileSourceEpisodeLedger(path string) (*FileSourceEpisodeLedger, error) {
	ledger := &FileSourceEpisodeLedger{
		path: strings.TrimSpace(path), idempotency: map[string]sourceEpisodeLedgerRecord{}, latest: map[string]SourceEpisode{},
		active: map[string]SourceEpisode{}, purge: map[string]int64{},
		write: func(path string, raw []byte) error { return appendFileDurably(path, raw, 0o600) },
		now:   func() time.Time { return time.Now().UTC() },
	}
	if ledger.path == "" {
		return ledger, nil
	}
	if err := ledger.reloadLocked(); err != nil {
		return nil, err
	}
	return ledger, nil
}

func (ledger *FileSourceEpisodeLedger) DualWriteSourceEpisode(_ context.Context, episode SourceEpisode, expectedCurrent *STRIDEReference) (SourceEpisodeDualWriteResult, error) {
	if ledger == nil || episode.Validate() != nil || (expectedCurrent == nil) != (episode.Supersedes == nil) ||
		expectedCurrent != nil && *expectedCurrent != *episode.Supersedes {
		return SourceEpisodeDualWriteResult{}, ErrSourceEpisodeInvalid
	}
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	if err := ledger.readyLocked(); err != nil {
		return SourceEpisodeDualWriteResult{}, err
	}
	if existing, found := ledger.idempotency[episode.IdempotencyKeyDigest]; found {
		if existing.Episode == nil {
			return SourceEpisodeDualWriteResult{}, ErrSourceEpisodeConflict
		}
		replayed, err := SourceEpisodeReplayDecision(*existing.Episode, episode)
		if err != nil || !replayed {
			return SourceEpisodeDualWriteResult{}, ErrSourceEpisodeConflict
		}
		return SourceEpisodeDualWriteResult{Reference: referenceFromHeader(existing.Episode.Header), Replayed: true}, nil
	}
	key := sourceEpisodeLedgerObjectKey(episode.Header.TenantID, episode.Header.ID)
	current, found := ledger.latest[key]
	if (expectedCurrent == nil) != !found || expectedCurrent != nil && *expectedCurrent != referenceFromHeader(current.Header) {
		return SourceEpisodeDualWriteResult{}, ErrSourceEpisodeConflict
	}
	currentPurge, hasPurge := ledger.purge[episode.Header.TenantID]
	if hasPurge && episode.Authority.PurgeGeneration != currentPurge {
		return SourceEpisodeDualWriteResult{}, ErrSourceEpisodeConflict
	}
	if !hasPurge {
		currentPurge = episode.Authority.PurgeGeneration
	}
	record := sourceEpisodeLedgerRecord{Format: sourceEpisodeLedgerFormat, Kind: "episode", Episode: &episode, RecordedAt: ledger.recordedAtLocked()}
	if err := ledger.appendLocked(record); err != nil {
		return SourceEpisodeDualWriteResult{}, err
	}
	ledger.purge[episode.Header.TenantID] = currentPurge
	ref := referenceFromHeader(episode.Header)
	return SourceEpisodeDualWriteResult{Reference: ref}, nil
}

// TombstoneSourceEpisode retracts the current exact revision. A purge
// tombstone advances the tenant generation and makes every older-generation
// episode invisible until it is safely re-derived.
func (ledger *FileSourceEpisodeLedger) TombstoneSourceEpisode(_ context.Context, tombstone SourceEpisodeTombstone) (bool, error) {
	if ledger == nil || tombstone.Validate() != nil {
		return false, ErrSourceEpisodeInvalid
	}
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	if err := ledger.readyLocked(); err != nil {
		return false, err
	}
	if existing, found := ledger.idempotency[tombstone.IdempotencyKeyDigest]; found {
		if existing.Tombstone != nil && *existing.Tombstone == tombstone {
			return true, nil
		}
		return false, ErrSourceEpisodeConflict
	}
	key := sourceEpisodeLedgerObjectKey(tombstone.TenantID, tombstone.Episode.ID)
	current, found := ledger.active[key]
	if !found || referenceFromHeader(current.Header) != tombstone.Episode {
		return false, ErrSourceEpisodeConflict
	}
	currentPurge, hasPurge := ledger.purge[tombstone.TenantID]
	if !hasPurge {
		currentPurge = current.Authority.PurgeGeneration
	}
	if tombstone.Cause == SourceEpisodeTombstonePurge {
		if tombstone.PurgeGeneration <= currentPurge {
			return false, ErrSourceEpisodeConflict
		}
	} else if tombstone.PurgeGeneration != currentPurge {
		return false, ErrSourceEpisodeConflict
	}
	record := sourceEpisodeLedgerRecord{Format: sourceEpisodeLedgerFormat, Kind: "tombstone", Tombstone: &tombstone, RecordedAt: ledger.recordedAtLocked()}
	if err := ledger.appendLocked(record); err != nil {
		return false, err
	}
	return false, nil
}

func (ledger *FileSourceEpisodeLedger) CurrentSourceEpisode(_ context.Context, tenantID, episodeID string) (SourceEpisode, bool, error) {
	if ledger == nil || !strideIdentifier(tenantID) || !strideIdentifier(episodeID) {
		return SourceEpisode{}, false, ErrSourceEpisodeInvalid
	}
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	if err := ledger.readyLocked(); err != nil {
		return SourceEpisode{}, false, err
	}
	episode, found := ledger.active[sourceEpisodeLedgerObjectKey(tenantID, episodeID)]
	if !found || episode.Authority.PurgeGeneration != ledger.purge[tenantID] {
		return SourceEpisode{}, false, nil
	}
	return cloneSourceEpisode(episode), true, nil
}

// WithCurrentSourceEpisodeLease holds the durable ledger head and purge
// generation stable through use. Callers must acquire the native source-owner
// authority first; that order matches retrieval and prevents a tombstone,
// superseding revision, or tenant purge from landing between the final fence
// check and an authority-sensitive publication.
func (ledger *FileSourceEpisodeLedger) WithCurrentSourceEpisodeLease(_ context.Context, tenantID string, expected STRIDEReference, use func() error) error {
	if ledger == nil || !strideIdentifier(tenantID) || expected.Validate() != nil || expected.ContractType != STRIDEContractSourceEpisode || use == nil {
		return ErrSourceEpisodeInvalid
	}
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	if err := ledger.readyLocked(); err != nil {
		return err
	}
	episode, found := ledger.active[sourceEpisodeLedgerObjectKey(tenantID, expected.ID)]
	if !found || referenceFromHeader(episode.Header) != expected || episode.Authority.PurgeGeneration != ledger.purge[tenantID] {
		return ErrSourceEpisodeUnavailable
	}
	return use()
}

// LatestSourceEpisode returns the durable lineage head even when a tombstone
// made it inactive. Post-commit adapters use it to resume a correction or
// reauthorization after a crash without scanning the ledger or resurrecting
// the tombstoned revision.
func (ledger *FileSourceEpisodeLedger) LatestSourceEpisode(_ context.Context, tenantID, episodeID string) (SourceEpisode, bool, error) {
	if ledger == nil || !strideIdentifier(tenantID) || !strideIdentifier(episodeID) {
		return SourceEpisode{}, false, ErrSourceEpisodeInvalid
	}
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	if err := ledger.readyLocked(); err != nil {
		return SourceEpisode{}, false, err
	}
	episode, found := ledger.latest[sourceEpisodeLedgerObjectKey(tenantID, episodeID)]
	if !found {
		return SourceEpisode{}, false, nil
	}
	return cloneSourceEpisode(episode), true, nil
}

func (ledger *FileSourceEpisodeLedger) ListSourceEpisodes(_ context.Context, tenantID, cursor string) (SourceEpisodeCatalogPage, error) {
	if ledger == nil || !strideIdentifier(tenantID) {
		return SourceEpisodeCatalogPage{}, ErrSourceEpisodeInvalid
	}
	start := 0
	if cursor != "" {
		var err error
		start, err = strconv.Atoi(cursor)
		if err != nil || start < 0 {
			return SourceEpisodeCatalogPage{}, ErrSourceEpisodeInvalid
		}
	}
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	if err := ledger.readyLocked(); err != nil {
		return SourceEpisodeCatalogPage{}, err
	}
	episodes := ledger.activeEpisodesLocked(tenantID)
	if start > len(episodes) {
		return SourceEpisodeCatalogPage{}, ErrSourceEpisodeInvalid
	}
	// Keep catalog pages small enough for bounded replay while allowing callers
	// to apply their own smaller authorized paging after filtering.
	end := start + 128
	if end > len(episodes) {
		end = len(episodes)
	}
	terminal := end == len(episodes)
	next := ""
	if !terminal {
		next = strconv.Itoa(end)
	}
	snapshotID, _ := STRIDEContractDigest(struct {
		TenantID string
		Purge    int64
		Episodes []SourceEpisode
	}{tenantID, ledger.purge[tenantID], episodes})
	snapshotAt := ledger.tenantSnapshotAtLocked(tenantID)
	return SourceEpisodeCatalogPage{
		SnapshotID: snapshotID, SnapshotAt: snapshotAt, PurgeGeneration: ledger.purge[tenantID],
		Episodes: cloneSourceEpisodes(episodes[start:end]), NextCursor: next, Terminal: terminal,
	}, nil
}

func (ledger *FileSourceEpisodeLedger) FindSourceEpisodeByRetrievalBody(_ context.Context, tenantID string, locator SourceEpisodeBodyLocator) (SourceEpisode, bool, error) {
	if ledger == nil || !strideIdentifier(tenantID) || !strideIdentifier(locator.SourceFamily) || !strideIdentifier(locator.ObjectID) || locator.ContentRevision < 1 || !isHexDigest(locator.ContentDigest) {
		return SourceEpisode{}, false, ErrSourceEpisodeInvalid
	}
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	if err := ledger.readyLocked(); err != nil {
		return SourceEpisode{}, false, err
	}
	var matched *SourceEpisode
	for _, episode := range ledger.active {
		if episode.Header.TenantID != tenantID || episode.Authority.PurgeGeneration != ledger.purge[tenantID] || sourceEpisodeBodyLocator(episode.RetrievalBody) != locator {
			continue
		}
		if matched != nil {
			return SourceEpisode{}, false, ErrSourceEpisodeConflict
		}
		copy := cloneSourceEpisode(episode)
		matched = &copy
	}
	if matched == nil {
		return SourceEpisode{}, false, nil
	}
	return *matched, true, nil
}

func (ledger *FileSourceEpisodeLedger) FindSourceEpisodeByACLObject(_ context.Context, ref ACLObjectRef) (SourceEpisode, bool, error) {
	if ledger == nil || !strideIdentifier(ref.TenantID) || !strideIdentifier(ref.Type) || !strideIdentifier(ref.ID) || ref.ACLVersion < 1 {
		return SourceEpisode{}, false, ErrSourceEpisodeInvalid
	}
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	if err := ledger.readyLocked(); err != nil {
		return SourceEpisode{}, false, err
	}
	var matched *SourceEpisode
	for _, episode := range ledger.active {
		if episode.Header.TenantID != ref.TenantID || episode.Authority.PurgeGeneration != ledger.purge[ref.TenantID] ||
			episode.RetrievalBody.SourceFamily != ref.Type || episode.RetrievalBody.ObjectID != ref.ID || episode.Authority.ACLRevision != ref.ACLVersion {
			continue
		}
		if matched != nil {
			return SourceEpisode{}, false, ErrSourceEpisodeConflict
		}
		copy := cloneSourceEpisode(episode)
		matched = &copy
	}
	if matched == nil {
		return SourceEpisode{}, false, nil
	}
	return *matched, true, nil
}

func (ledger *FileSourceEpisodeLedger) CurrentPurgeGeneration(_ context.Context, tenantID string) (int64, error) {
	if ledger == nil || !strideIdentifier(tenantID) {
		return 0, ErrSourceEpisodeInvalid
	}
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	if err := ledger.readyLocked(); err != nil {
		return 0, err
	}
	return ledger.purge[tenantID], nil
}

func (ledger *FileSourceEpisodeLedger) appendLocked(record sourceEpisodeLedgerRecord) error {
	record.Sequence = uint64(len(ledger.records) + 1)
	if len(ledger.records) > 0 {
		record.PreviousDigest = ledger.records[len(ledger.records)-1].RecordDigest
	}
	digest, err := sourceEpisodeLedgerRecordDigest(record)
	if err != nil {
		return err
	}
	record.RecordDigest = digest
	if record.Validate() != nil {
		return ErrSourceEpisodeInvalid
	}
	if ledger.path != "" {
		raw, err := json.Marshal(record)
		if err != nil {
			return err
		}
		if ledger.write == nil {
			return ErrSourceEpisodeUnavailable
		}
		if err := ledger.write(ledger.path, append(raw, '\n')); err != nil {
			ledger.poisoned = err
			return err
		}
	}
	if err := ledger.applyRecordLocked(record); err != nil {
		ledger.poisoned = err
		return err
	}
	return nil
}

func (ledger *FileSourceEpisodeLedger) applyRecordLocked(record sourceEpisodeLedgerRecord) error {
	var key string
	if record.Episode != nil {
		key = record.Episode.IdempotencyKeyDigest
	} else if record.Tombstone != nil {
		key = record.Tombstone.IdempotencyKeyDigest
	} else {
		key = record.TenantPurge.IdempotencyKeyDigest
	}
	if _, found := ledger.idempotency[key]; found {
		return ErrSourceEpisodeConflict
	}
	if record.Episode != nil {
		episode := cloneSourceEpisode(*record.Episode)
		objectKey := sourceEpisodeLedgerObjectKey(episode.Header.TenantID, episode.Header.ID)
		current, found := ledger.latest[objectKey]
		if (episode.Supersedes == nil) != !found || episode.Supersedes != nil && *episode.Supersedes != referenceFromHeader(current.Header) {
			return ErrSourceEpisodeConflict
		}
		currentPurge, hasPurge := ledger.purge[episode.Header.TenantID]
		if hasPurge && episode.Authority.PurgeGeneration != currentPurge {
			return ErrSourceEpisodeConflict
		}
		ledger.latest[objectKey] = episode
		ledger.active[objectKey] = episode
		if !hasPurge {
			ledger.purge[episode.Header.TenantID] = episode.Authority.PurgeGeneration
		}
	} else if record.Tombstone != nil {
		tombstone := *record.Tombstone
		objectKey := sourceEpisodeLedgerObjectKey(tombstone.TenantID, tombstone.Episode.ID)
		current, found := ledger.active[objectKey]
		if !found || referenceFromHeader(current.Header) != tombstone.Episode {
			return ErrSourceEpisodeConflict
		}
		currentPurge := ledger.purge[tombstone.TenantID]
		if tombstone.Cause == SourceEpisodeTombstonePurge {
			if tombstone.PurgeGeneration <= currentPurge {
				return ErrSourceEpisodeConflict
			}
			ledger.purge[tombstone.TenantID] = tombstone.PurgeGeneration
		} else if tombstone.PurgeGeneration != currentPurge {
			return ErrSourceEpisodeConflict
		}
		delete(ledger.active, objectKey)
	} else {
		purge := *record.TenantPurge
		if purge.PurgeGeneration <= ledger.purge[purge.TenantID] {
			return ErrSourceEpisodeConflict
		}
		ledger.purge[purge.TenantID] = purge.PurgeGeneration
		for objectKey, episode := range ledger.active {
			if episode.Header.TenantID == purge.TenantID {
				delete(ledger.active, objectKey)
			}
		}
	}
	ledger.records = append(ledger.records, cloneSourceEpisodeLedgerRecord(record))
	ledger.idempotency[key] = cloneSourceEpisodeLedgerRecord(record)
	return nil
}

func (ledger *FileSourceEpisodeLedger) activeEpisodesLocked(tenantID string) []SourceEpisode {
	episodes := make([]SourceEpisode, 0, len(ledger.active))
	for _, episode := range ledger.active {
		if episode.Header.TenantID == tenantID && episode.Authority.PurgeGeneration == ledger.purge[tenantID] {
			episodes = append(episodes, cloneSourceEpisode(episode))
		}
	}
	sort.Slice(episodes, func(i, j int) bool {
		if episodes[i].Header.ID != episodes[j].Header.ID {
			return episodes[i].Header.ID < episodes[j].Header.ID
		}
		return episodes[i].Header.Revision < episodes[j].Header.Revision
	})
	return episodes
}

func (ledger *FileSourceEpisodeLedger) readyLocked() error {
	if ledger.poisoned != nil {
		return fmt.Errorf("%w: %v", ErrSourceEpisodeUnavailable, ledger.poisoned)
	}
	return nil
}

func (ledger *FileSourceEpisodeLedger) recordedAtLocked() time.Time {
	now := time.Now().UTC()
	if ledger.now != nil {
		now = ledger.now().UTC()
	}
	if len(ledger.records) > 0 && now.Before(ledger.records[len(ledger.records)-1].RecordedAt) {
		return ledger.records[len(ledger.records)-1].RecordedAt
	}
	return now
}

func (ledger *FileSourceEpisodeLedger) tenantSnapshotAtLocked(tenantID string) time.Time {
	for index := len(ledger.records) - 1; index >= 0; index-- {
		record := ledger.records[index]
		if (record.Episode != nil && record.Episode.Header.TenantID == tenantID) ||
			(record.Tombstone != nil && record.Tombstone.TenantID == tenantID) ||
			(record.TenantPurge != nil && record.TenantPurge.TenantID == tenantID) {
			return record.RecordedAt
		}
	}
	return time.Unix(0, 1).UTC()
}

func (ledger *FileSourceEpisodeLedger) reloadLocked() error {
	records, err := loadSourceEpisodeLedgerRecords(ledger.path)
	if err != nil {
		return err
	}
	ledger.records = nil
	ledger.idempotency = map[string]sourceEpisodeLedgerRecord{}
	ledger.latest = map[string]SourceEpisode{}
	ledger.active = map[string]SourceEpisode{}
	ledger.purge = map[string]int64{}
	for _, record := range records {
		if err := ledger.applyRecordLocked(record); err != nil {
			return err
		}
	}
	return nil
}

func loadSourceEpisodeLedgerRecords(path string) ([]sourceEpisodeLedgerRecord, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	reader := bufio.NewReader(file)
	var records []sourceEpisodeLedgerRecord
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			if errors.Is(readErr, io.EOF) || len(line) > sourceEpisodeLedgerMaxRecordBytes {
				return nil, ErrSourceEpisodeInvalid
			}
			decoder := json.NewDecoder(bytes.NewReader(line))
			decoder.DisallowUnknownFields()
			var record sourceEpisodeLedgerRecord
			if err := decoder.Decode(&record); err != nil || ensureJSONEOF(decoder) != nil || record.Validate() != nil ||
				record.Sequence != uint64(len(records)+1) || len(records) == 0 && record.PreviousDigest != "" ||
				len(records) > 0 && record.PreviousDigest != records[len(records)-1].RecordDigest {
				return nil, ErrSourceEpisodeInvalid
			}
			records = append(records, record)
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			return nil, readErr
		}
	}
	return records, nil
}

func sourceEpisodeLedgerObjectKey(tenantID, episodeID string) string {
	return tenantID + "\x00" + episodeID
}

func cloneSourceEpisode(value SourceEpisode) SourceEpisode {
	raw, _ := json.Marshal(value)
	var clone SourceEpisode
	_ = json.Unmarshal(raw, &clone)
	return clone
}

func cloneSourceEpisodes(values []SourceEpisode) []SourceEpisode {
	clones := make([]SourceEpisode, len(values))
	for index := range values {
		clones[index] = cloneSourceEpisode(values[index])
	}
	return clones
}

func cloneSourceEpisodeLedgerRecord(value sourceEpisodeLedgerRecord) sourceEpisodeLedgerRecord {
	raw, _ := json.Marshal(value)
	var clone sourceEpisodeLedgerRecord
	_ = json.Unmarshal(raw, &clone)
	return clone
}
