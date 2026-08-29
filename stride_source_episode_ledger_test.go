package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func sourceEpisodeLedgerEpisode(t *testing.T, kind SourceEpisodeKind, id string, purge int64) SourceEpisode {
	t.Helper()
	input := sourceEpisodeAdapterFixture(kind)
	input.Header.ID = id
	input.Authority.PurgeGeneration = purge
	episode, err := sourceEpisodeAdapterForKind(kind, input)
	if err != nil {
		t.Fatal(err)
	}
	return episode
}

func reviseSourceEpisodeForLedger(t *testing.T, prior SourceEpisode, purge int64) SourceEpisode {
	t.Helper()
	input := sourceEpisodeAdapterFixture(prior.Kind)
	input.Header.ID = prior.Header.ID
	input.Header.Revision = prior.Header.Revision + 1
	input.Header.CreatedAt = prior.Header.CreatedAt.Add(time.Minute)
	input.Source.ObjectID = prior.Source.ObjectID
	input.Source.ContentRevision = prior.Source.ContentRevision + 1
	input.Source.ContentDigest = strings.Repeat("5", 64)
	input.Authority.PurgeGeneration = purge
	previous := referenceFromHeader(prior.Header)
	input.Supersedes = &previous
	if prior.Kind == SourceEpisodeMeetingAnalysis {
		input.RetrievalBody.ObjectID = prior.RetrievalBody.ObjectID
		input.RetrievalBody.ContentRevision = prior.RetrievalBody.ContentRevision + 1
		input.RetrievalBody.ContentDigest = strings.Repeat("6", 64)
	} else {
		input.RetrievalBody = input.Source
	}
	next, err := sourceEpisodeAdapterForKind(prior.Kind, input)
	if err != nil {
		t.Fatal(err)
	}
	return next
}

func sourceEpisodeLedgerTombstone(episode SourceEpisode, cause string, purge int64, at time.Time) SourceEpisodeTombstone {
	tombstone := SourceEpisodeTombstone{
		TenantID: episode.Header.TenantID, Episode: referenceFromHeader(episode.Header), Cause: cause, PurgeGeneration: purge,
		ReasonDigest: strings.Repeat("7", 64), OccurredAt: at.UTC(),
	}
	tombstone.IdempotencyKeyDigest = SourceEpisodeTombstoneIdempotencyKey(tombstone.TenantID, tombstone.Episode, tombstone.Cause, tombstone.PurgeGeneration)
	return tombstone
}

func TestFileSourceEpisodeLedgerRestartReplayIdempotencyAndCAS(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source-episodes.jsonl")
	ledger, err := OpenFileSourceEpisodeLedger(path)
	if err != nil {
		t.Fatal(err)
	}
	ledger.now = func() time.Time { return sourceEpisodeTestTime.Add(10 * time.Minute) }
	first := sourceEpisodeLedgerEpisode(t, SourceEpisodePublicChannelSegment, "episode_public", 6)
	result, err := ledger.DualWriteSourceEpisode(context.Background(), first, nil)
	if err != nil || result.Replayed || result.Reference != referenceFromHeader(first.Header) {
		t.Fatalf("first dual-write: result=%+v err=%v", result, err)
	}
	replay, err := ledger.DualWriteSourceEpisode(context.Background(), first, nil)
	if err != nil || !replay.Replayed {
		t.Fatalf("idempotent replay: result=%+v err=%v", replay, err)
	}
	conflict := first
	conflict.Authority.ConsentRevision++
	conflict.Header.ContentDigest, _ = conflict.ContentDigest()
	if _, err := ledger.DualWriteSourceEpisode(context.Background(), conflict, nil); !errors.Is(err, ErrSourceEpisodeConflict) {
		t.Fatalf("same idempotency key accepted changed authority: %v", err)
	}

	second := reviseSourceEpisodeForLedger(t, first, 6)
	prior := referenceFromHeader(first.Header)
	if _, err := ledger.DualWriteSourceEpisode(context.Background(), second, &prior); err != nil {
		t.Fatalf("superseding write failed: %v", err)
	}
	current, found, err := ledger.CurrentSourceEpisode(context.Background(), "company_1", first.Header.ID)
	if err != nil || !found || current.Header.Revision != 2 {
		t.Fatalf("current revision=%+v found=%v err=%v", current, found, err)
	}

	restarted, err := OpenFileSourceEpisodeLedger(path)
	if err != nil {
		t.Fatalf("restart replay failed: %v", err)
	}
	current, found, err = restarted.CurrentSourceEpisode(context.Background(), "company_1", first.Header.ID)
	if err != nil || !found || current.Header.ContentDigest != second.Header.ContentDigest {
		t.Fatalf("restart lost current revision: current=%+v found=%v err=%v", current, found, err)
	}
	replay, err = restarted.DualWriteSourceEpisode(context.Background(), second, &prior)
	if err != nil || !replay.Replayed {
		t.Fatalf("restart did not replay idempotently: result=%+v err=%v", replay, err)
	}
	page, err := restarted.ListSourceEpisodes(context.Background(), "company_1", "")
	if err != nil || !page.Terminal || len(page.Episodes) != 1 || page.Episodes[0].Header.Revision != 2 {
		t.Fatalf("catalog projection after restart: page=%+v err=%v", page, err)
	}
	foundEpisode, found, err := restarted.FindSourceEpisodeByRetrievalBody(context.Background(), "company_1", sourceEpisodeBodyLocator(second.RetrievalBody))
	if err != nil || !found || foundEpisode.Header.ContentDigest != second.Header.ContentDigest {
		t.Fatalf("exact retrieval lookup after restart: found=%v episode=%+v err=%v", found, foundEpisode, err)
	}
	fileBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(fileBytes), "native body bytes must never be in the ledger") {
		t.Fatal("ledger duplicated a native body")
	}
}

func TestFileSourceEpisodeLedgerTombstonePurgeAndSafeRederivation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source-episodes.jsonl")
	ledger, _ := OpenFileSourceEpisodeLedger(path)
	ledger.now = func() time.Time { return sourceEpisodeTestTime.Add(20 * time.Minute) }
	first := sourceEpisodeLedgerEpisode(t, SourceEpisodePublicChannelSegment, "episode_one", 6)
	second := sourceEpisodeLedgerEpisode(t, SourceEpisodeDriveFileRevision, "episode_two", 6)
	if _, err := ledger.DualWriteSourceEpisode(context.Background(), first, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.DualWriteSourceEpisode(context.Background(), second, nil); err != nil {
		t.Fatal(err)
	}
	retraction := sourceEpisodeLedgerTombstone(first, SourceEpisodeTombstoneRetraction, 6, sourceEpisodeTestTime.Add(21*time.Minute))
	if replayed, err := ledger.TombstoneSourceEpisode(context.Background(), retraction); err != nil || replayed {
		t.Fatalf("retraction failed: replayed=%v err=%v", replayed, err)
	}
	if replayed, err := ledger.TombstoneSourceEpisode(context.Background(), retraction); err != nil || !replayed {
		t.Fatalf("retraction replay failed: replayed=%v err=%v", replayed, err)
	}
	distinctTombstone := sourceEpisodeLedgerTombstone(first, SourceEpisodeTombstoneConsent, 6, sourceEpisodeTestTime.Add(22*time.Minute))
	if replayed, err := ledger.TombstoneSourceEpisode(context.Background(), distinctTombstone); !errors.Is(err, ErrSourceEpisodeConflict) || replayed {
		t.Fatalf("inactive episode accepted a second tombstone: replayed=%v err=%v", replayed, err)
	}
	if _, found, _ := ledger.CurrentSourceEpisode(context.Background(), "company_1", first.Header.ID); found {
		t.Fatal("retracted episode remained current")
	}
	page, _ := ledger.ListSourceEpisodes(context.Background(), "company_1", "")
	if len(page.Episodes) != 1 || page.Episodes[0].Header.ID != second.Header.ID {
		t.Fatalf("retraction affected wrong catalog rows: %+v", page.Episodes)
	}

	purge := sourceEpisodeLedgerTombstone(second, SourceEpisodeTombstonePurge, 7, sourceEpisodeTestTime.Add(22*time.Minute))
	if _, err := ledger.TombstoneSourceEpisode(context.Background(), purge); err != nil {
		t.Fatal(err)
	}
	page, _ = ledger.ListSourceEpisodes(context.Background(), "company_1", "")
	if page.PurgeGeneration != 7 || len(page.Episodes) != 0 {
		t.Fatalf("purge did not invalidate old generation: %+v", page)
	}
	if _, found, _ := ledger.FindSourceEpisodeByRetrievalBody(context.Background(), "company_1", sourceEpisodeBodyLocator(second.RetrievalBody)); found {
		t.Fatal("purged body locator remained discoverable")
	}

	rederived := reviseSourceEpisodeForLedger(t, first, 7)
	prior := referenceFromHeader(first.Header)
	if _, err := ledger.DualWriteSourceEpisode(context.Background(), rederived, &prior); err != nil {
		t.Fatalf("safe rederivation at current purge generation failed: %v", err)
	}
	page, _ = ledger.ListSourceEpisodes(context.Background(), "company_1", "")
	if page.PurgeGeneration != 7 || len(page.Episodes) != 1 || page.Episodes[0].Header.ContentDigest != rederived.Header.ContentDigest {
		t.Fatalf("rederived catalog incorrect: %+v", page)
	}
	restarted, err := OpenFileSourceEpisodeLedger(path)
	if err != nil {
		t.Fatal(err)
	}
	page, _ = restarted.ListSourceEpisodes(context.Background(), "company_1", "")
	if page.PurgeGeneration != 7 || len(page.Episodes) != 1 {
		t.Fatalf("restart lost tombstone/purge projection: %+v", page)
	}
}

func TestFileSourceEpisodeLedgerFailsClosedOnAmbiguousAndPartialWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source-episodes.jsonl")
	ledger, _ := OpenFileSourceEpisodeLedger(path)
	episode := sourceEpisodeLedgerEpisode(t, SourceEpisodePublicChannelSegment, "episode_ambiguous", 6)
	writeErr := errors.New("fsync outcome ambiguous")
	ledger.write = func(string, []byte) error { return writeErr }
	if _, err := ledger.DualWriteSourceEpisode(context.Background(), episode, nil); !errors.Is(err, writeErr) {
		t.Fatalf("ambiguous append error lost: %v", err)
	}
	if _, err := ledger.DualWriteSourceEpisode(context.Background(), episode, nil); !errors.Is(err, ErrSourceEpisodeUnavailable) {
		t.Fatalf("poisoned writer accepted another append: %v", err)
	}
	if _, found, err := ledger.CurrentSourceEpisode(context.Background(), "company_1", episode.Header.ID); !errors.Is(err, ErrSourceEpisodeUnavailable) || found {
		t.Fatalf("poisoned writer exposed ghost state: found=%v err=%v", found, err)
	}

	clean, err := OpenFileSourceEpisodeLedger(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := clean.DualWriteSourceEpisode(context.Background(), episode, nil); err != nil {
		t.Fatal(err)
	}
	if err := appendFileDurably(path, []byte(`{"partial":`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenFileSourceEpisodeLedger(path); !errors.Is(err, ErrSourceEpisodeInvalid) {
		t.Fatalf("partial non-newline tail did not fail closed: %v", err)
	}
}

func TestFileSourceEpisodeLedgerRejectsHashChainCorruption(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source-episodes.jsonl")
	ledger, _ := OpenFileSourceEpisodeLedger(path)
	episode := sourceEpisodeLedgerEpisode(t, SourceEpisodePublicChannelSegment, "episode_hash", 6)
	if _, err := ledger.DualWriteSourceEpisode(context.Background(), episode, nil); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw = []byte(strings.Replace(string(raw), `"sequence":1`, `"sequence":2`, 1))
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenFileSourceEpisodeLedger(path); !errors.Is(err, ErrSourceEpisodeInvalid) {
		t.Fatalf("hash-chain corruption replayed: %v", err)
	}
}

func TestSourceEpisodeRuntimeRegistryBuildsLocalShadowWithoutOwningBodies(t *testing.T) {
	episodes, bodies := sourceEpisodeBrainFixture(t)
	ledger, err := OpenFileSourceEpisodeLedger("")
	if err != nil {
		t.Fatal(err)
	}
	for _, episode := range episodes {
		if _, err := ledger.DualWriteSourceEpisode(context.Background(), episode, nil); err != nil {
			t.Fatal(err)
		}
	}
	authority := &sourceEpisodeBrainTestAuthority{revoked: map[string]string{}, currentPurge: 6}
	native := &sourceEpisodeBrainTestBodies{bodies: bodies}
	registry := NewSourceEpisodeRuntimeRegistry()
	seenAuthority := map[string]bool{}
	seenBody := map[string]bool{}
	for _, episode := range episodes {
		if !seenAuthority[episode.Source.SourceFamily] {
			if err := registry.RegisterAuthority(episode.Source.SourceFamily, authority); err != nil {
				t.Fatal(err)
			}
			seenAuthority[episode.Source.SourceFamily] = true
		}
		if !seenBody[episode.RetrievalBody.SourceFamily] {
			if err := registry.RegisterBodyReader(episode.RetrievalBody.SourceFamily, native); err != nil {
				t.Fatal(err)
			}
			seenBody[episode.RetrievalBody.SourceFamily] = true
		}
	}
	if err := registry.RegisterAuthority(episodes[0].Source.SourceFamily, authority); !errors.Is(err, ErrSourceEpisodeConflict) {
		t.Fatalf("duplicate authority registration replaced native owner: %v", err)
	}
	adapter, err := NewSourceEpisodeShadowBrainAdapter(ledger, registry, 3, func() time.Time { return sourceEpisodeTestTime.Add(time.Hour) })
	if err != nil {
		t.Fatal(err)
	}
	sources, _ := collectSourceEpisodeInventory(t, adapter, sourceEpisodeBrainPrincipal("person_1"))
	if len(sources) != 6 {
		t.Fatalf("registry-backed shadow inventory=%d, want six", len(sources))
	}
	for _, source := range sources {
		read, err := adapter.ReadBrainSource(context.Background(), source.Evidence)
		if err != nil || !read.BodyAvailable {
			t.Fatalf("registry dispatch failed for %s: read=%+v err=%v", source.Evidence.SourceFamily, read, err)
		}
	}
}
