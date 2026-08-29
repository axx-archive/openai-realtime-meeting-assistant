package main

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"
)

type sourceEpisodeBrainTestCatalog struct {
	episodes        []SourceEpisode
	purgeGeneration int64
	snapshotAt      time.Time
	pageSize        int
}

func (catalog *sourceEpisodeBrainTestCatalog) snapshotID() string {
	digest, _ := STRIDEContractDigest(struct {
		Episodes []SourceEpisode
		Purge    int64
	}{catalog.episodes, catalog.purgeGeneration})
	return digest
}

func (catalog *sourceEpisodeBrainTestCatalog) ListSourceEpisodes(_ context.Context, _ string, cursor string) (SourceEpisodeCatalogPage, error) {
	start := 0
	if cursor != "" {
		var err error
		start, err = strconv.Atoi(cursor)
		if err != nil || start < 0 || start > len(catalog.episodes) {
			return SourceEpisodeCatalogPage{}, ErrSourceEpisodeCatalogUnavailable
		}
	}
	end := start + catalog.pageSize
	if end > len(catalog.episodes) {
		end = len(catalog.episodes)
	}
	terminal := end == len(catalog.episodes)
	next := ""
	if !terminal {
		next = strconv.Itoa(end)
	}
	return SourceEpisodeCatalogPage{
		SnapshotID: catalog.snapshotID(), SnapshotAt: catalog.snapshotAt, PurgeGeneration: catalog.purgeGeneration,
		Episodes: append([]SourceEpisode(nil), catalog.episodes[start:end]...), NextCursor: next, Terminal: terminal,
	}, nil
}

func (catalog *sourceEpisodeBrainTestCatalog) FindSourceEpisodeByRetrievalBody(_ context.Context, tenantID string, ref SourceEpisodeBodyLocator) (SourceEpisode, bool, error) {
	for index := len(catalog.episodes) - 1; index >= 0; index-- {
		episode := catalog.episodes[index]
		body := episode.RetrievalBody
		if episode.Header.TenantID == tenantID && body.SourceFamily == ref.SourceFamily && body.ObjectID == ref.ObjectID &&
			body.ContentRevision == ref.ContentRevision && body.ContentDigest == ref.ContentDigest {
			return episode, true, nil
		}
	}
	return SourceEpisode{}, false, nil
}

type sourceEpisodeBrainTestAuthority struct {
	revoked      map[string]string
	currentPurge int64
}

func (authority *sourceEpisodeBrainTestAuthority) AuthorizeSourceEpisodeMetadata(_ context.Context, principal ACLPrincipal, episode SourceEpisode) (bool, error) {
	if authority.revoked[episode.Header.ID] != "" || episode.Authority.PurgeGeneration != authority.currentPurge {
		return false, nil
	}
	if episode.Authority.Audience.Visibility != "private" {
		return true, nil
	}
	for _, allowed := range episode.Authority.Audience.Principals {
		if principal.ID == allowed {
			return true, nil
		}
	}
	return false, ErrSourceEpisodeAuthorityDenied
}

func (authority *sourceEpisodeBrainTestAuthority) WithCurrentSourceEpisodeAuthority(_ context.Context, episode SourceEpisode, use func() error) error {
	if authority.revoked[episode.Header.ID] != "" || episode.Authority.PurgeGeneration != authority.currentPurge {
		return ErrSourceEpisodeAuthorityStale
	}
	return use()
}

type sourceEpisodeBrainTestBodies struct {
	bodies map[SourceEpisodeRevisionRef]string
	reads  []SourceEpisodeRevisionRef
}

func (reader *sourceEpisodeBrainTestBodies) ReadExactSourceEpisodeBody(_ context.Context, ref SourceEpisodeRevisionRef) (SourceEpisodeNativeBody, error) {
	reader.reads = append(reader.reads, ref)
	body, ok := reader.bodies[ref]
	if !ok {
		return SourceEpisodeNativeBody{}, ErrSourceEpisodeBodyMissing
	}
	return SourceEpisodeNativeBody{Revision: ref, Body: body}, nil
}

func sourceEpisodeBrainFixture(t *testing.T) ([]SourceEpisode, map[SourceEpisodeRevisionRef]string) {
	t.Helper()
	kinds := []SourceEpisodeKind{
		SourceEpisodeMeetingAnalysis, SourceEpisodePublicChannelSegment, SourceEpisodePrivateConversationSegment,
		SourceEpisodeRealtimeVoiceSession, SourceEpisodeDriveFileRevision, SourceEpisodeWorkArtifactRevision,
	}
	episodes := make([]SourceEpisode, 0, len(kinds))
	bodies := map[SourceEpisodeRevisionRef]string{}
	for index, kind := range kinds {
		body := fmt.Sprintf("%s durable body %d", kind, index+1)
		input := sourceEpisodeAdapterFixture(kind)
		input.Header.ID = fmt.Sprintf("brain_episode_%d", index+1)
		input.Header.CreatedAt = input.Header.CreatedAt.Add(time.Duration(index) * time.Second)
		input.OccurredStart = input.OccurredStart.Add(time.Duration(index) * time.Minute)
		input.OccurredEnd = input.OccurredEnd.Add(time.Duration(index) * time.Minute)
		input.PhaseProof.BoundaryAt = input.PhaseProof.BoundaryAt.Add(time.Duration(index) * time.Minute)
		input.Authority.ObservedAt = input.Authority.ObservedAt.Add(time.Duration(index) * time.Minute)
		input.Header.CreatedAt = input.Header.CreatedAt.Add(time.Duration(index) * time.Minute)
		if kind == SourceEpisodeMeetingAnalysis {
			input.RetrievalBody.ContentDigest = digestBrainString(body)
			input.RetrievalBody.SizeBytes = int64(len(body))
		} else {
			input.Source.ContentDigest = digestBrainString(body)
			input.Source.SizeBytes = int64(len(body))
			input.RetrievalBody = input.Source
		}
		episode, err := sourceEpisodeAdapterForKind(kind, input)
		if err != nil {
			t.Fatalf("adapt %s: %v", kind, err)
		}
		episodes = append(episodes, episode)
		bodies[episode.RetrievalBody] = body
	}
	return episodes, bodies
}

func sourceEpisodeBrainTemporal() TemporalQuery {
	return TemporalQuery{
		StartUTC: sourceEpisodeTestTime.Add(-time.Hour), EndUTC: sourceEpisodeTestTime.Add(2 * time.Hour),
		Timezone: "UTC", Interpretation: TemporalExplicitRange, InterpretationNote: "source episode test range",
	}
}

func sourceEpisodeBrainPrincipal(id string) ACLPrincipal {
	return ACLPrincipal{TenantID: "company_1", Kind: ACLPrincipalUser, ID: id}
}

func collectSourceEpisodeInventory(t *testing.T, adapter *SourceEpisodeBrainAdapter, principal ACLPrincipal) ([]BrainSourceMetadata, []BrainSourceInventoryPage) {
	t.Helper()
	request := BrainSourceInventoryRequest{TenantID: "company_1", Principal: principal, Temporal: sourceEpisodeBrainTemporal()}
	var sources []BrainSourceMetadata
	var pages []BrainSourceInventoryPage
	cursor := ""
	for {
		page, err := adapter.InventoryBrainSources(context.Background(), request, cursor)
		if err != nil {
			t.Fatalf("inventory page %q: %v", cursor, err)
		}
		pages = append(pages, page)
		sources = append(sources, page.Sources...)
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	return sources, pages
}

func TestSourceEpisodeBrainAdapterInventoriesAndReadsAllSixKinds(t *testing.T) {
	episodes, bodies := sourceEpisodeBrainFixture(t)
	catalog := &sourceEpisodeBrainTestCatalog{episodes: episodes, purgeGeneration: 6, snapshotAt: sourceEpisodeTestTime.Add(20 * time.Minute), pageSize: 2}
	authority := &sourceEpisodeBrainTestAuthority{revoked: map[string]string{}, currentPurge: 6}
	native := &sourceEpisodeBrainTestBodies{bodies: bodies}
	adapter := &SourceEpisodeBrainAdapter{Catalog: catalog, Authority: authority, Bodies: native, PageSize: 2, Now: func() time.Time { return sourceEpisodeTestTime.Add(time.Hour) }}

	sources, pages := collectSourceEpisodeInventory(t, adapter, sourceEpisodeBrainPrincipal("person_1"))
	if len(sources) != 6 || len(pages) != 3 {
		t.Fatalf("inventory=%d pages=%d, want six sources across three pages", len(sources), len(pages))
	}
	for _, page := range pages {
		if page.ExpectedSourceCount != 6 || page.SourceHighWater != 6 || page.InventoryID != pages[0].InventoryID || page.InventoryManifest != pages[0].InventoryManifest || !page.SnapshotAt.Equal(pages[0].SnapshotAt) {
			t.Fatalf("unstable or leaking page proof: %+v", page)
		}
	}
	for _, source := range sources {
		read, err := adapter.ReadBrainSource(context.Background(), source.Evidence)
		if err != nil || !read.BodyAvailable || read.Status != RecallSourceFresh || digestBrainString(read.Body) != source.Evidence.ContentDigest {
			t.Fatalf("body read failed for %+v: read=%+v err=%v", source.Evidence, read, err)
		}
	}
	if len(native.reads) != 6 {
		t.Fatalf("native reader calls=%d, want six exact bodies", len(native.reads))
	}
	meetingRead := native.reads[0]
	for _, ref := range native.reads {
		if ref.SourceFamily == SourceEpisodeFamilyMeetingAnalysisBody {
			meetingRead = ref
		}
		if ref.SourceFamily == string(STRIDEContractTranscriptRevision) || strings.Contains(ref.SourceFamily, "transcript") {
			t.Fatalf("ordinary brain retrieval reached raw transcript source: %+v", ref)
		}
	}
	if meetingRead.SourceFamily != SourceEpisodeFamilyMeetingAnalysisBody || meetingRead.ObjectID != episodes[0].RetrievalBody.ObjectID || meetingRead == episodes[0].Source {
		t.Fatalf("meeting retrieval did not use analysis body: read=%+v source=%+v", meetingRead, episodes[0].Source)
	}
}

func TestSourceEpisodeBrainAdapterExcludesPrivateBeforeCountsAndPaging(t *testing.T) {
	episodes, bodies := sourceEpisodeBrainFixture(t)
	adapter := &SourceEpisodeBrainAdapter{
		Catalog:   &sourceEpisodeBrainTestCatalog{episodes: episodes, purgeGeneration: 6, snapshotAt: sourceEpisodeTestTime, pageSize: 2},
		Authority: &sourceEpisodeBrainTestAuthority{revoked: map[string]string{}, currentPurge: 6}, Bodies: &sourceEpisodeBrainTestBodies{bodies: bodies},
		PageSize: 2, Now: func() time.Time { return sourceEpisodeTestTime.Add(time.Hour) },
	}
	sources, pages := collectSourceEpisodeInventory(t, adapter, sourceEpisodeBrainPrincipal("person_outsider"))
	if len(sources) != 4 || pages[0].ExpectedSourceCount != 4 || pages[0].SourceHighWater != 4 {
		t.Fatalf("private sources influenced visible counts: sources=%d page=%+v", len(sources), pages[0])
	}
	for _, source := range sources {
		if source.Evidence.ObjectID == episodes[2].RetrievalBody.ObjectID || source.Evidence.ObjectID == episodes[3].RetrievalBody.ObjectID {
			t.Fatalf("private source entered outsider inventory: %+v", source.Evidence)
		}
	}
}

func TestSourceEpisodeBrainAdapterRejectsPagingAndBodyDrift(t *testing.T) {
	episodes, bodies := sourceEpisodeBrainFixture(t)
	catalog := &sourceEpisodeBrainTestCatalog{episodes: episodes, purgeGeneration: 6, snapshotAt: sourceEpisodeTestTime, pageSize: 2}
	authority := &sourceEpisodeBrainTestAuthority{revoked: map[string]string{}, currentPurge: 6}
	native := &sourceEpisodeBrainTestBodies{bodies: bodies}
	adapter := &SourceEpisodeBrainAdapter{Catalog: catalog, Authority: authority, Bodies: native, PageSize: 1, Now: func() time.Time { return sourceEpisodeTestTime.Add(time.Hour) }}
	request := BrainSourceInventoryRequest{TenantID: "company_1", Principal: sourceEpisodeBrainPrincipal("person_1"), Temporal: sourceEpisodeBrainTemporal()}
	first, err := adapter.InventoryBrainSources(context.Background(), request, "")
	if err != nil || first.NextCursor == "" {
		t.Fatalf("first page: %+v err=%v", first, err)
	}
	mutated := catalog.episodes[1]
	mutated.Authority.ACLRevision++
	mutated.Header.ContentDigest, _ = mutated.ContentDigest()
	catalog.episodes[1] = mutated
	if _, err := adapter.InventoryBrainSources(context.Background(), request, first.NextCursor); !errors.Is(err, ErrBrainRetrievalRetry) {
		t.Fatalf("paging snapshot drift did not retry: %v", err)
	}

	evidence := BrainEvidenceRef{
		TenantID: "company_1", SourceFamily: episodes[0].RetrievalBody.SourceFamily, ObjectID: episodes[0].RetrievalBody.ObjectID,
		ContentRevision: episodes[0].RetrievalBody.ContentRevision, ACLVersion: episodes[0].Authority.ACLRevision, ContentDigest: episodes[0].RetrievalBody.ContentDigest,
		RoomID: episodes[0].Scope.RoomID, SittingID: episodes[0].Scope.SittingID, OccurredStart: episodes[0].OccurredStart, OccurredEnd: episodes[0].OccurredEnd,
		PurgeGeneration: 6, Trust: BrainEvidenceTrusted,
	}
	native.bodies[episodes[0].RetrievalBody] = "changed analysis bytes"
	if _, err := adapter.ReadBrainSource(context.Background(), evidence); !errors.Is(err, ErrBrainRetrievalRetry) {
		t.Fatalf("body digest drift did not retry: %v", err)
	}
}

func TestSourceEpisodeBrainAdapterRechecksACLConsentAndPurge(t *testing.T) {
	for _, cause := range []string{"acl", "consent", "purge"} {
		t.Run(cause, func(t *testing.T) {
			episodes, bodies := sourceEpisodeBrainFixture(t)
			catalog := &sourceEpisodeBrainTestCatalog{episodes: episodes, purgeGeneration: 6, snapshotAt: sourceEpisodeTestTime, pageSize: 3}
			authority := &sourceEpisodeBrainTestAuthority{revoked: map[string]string{}, currentPurge: 6}
			adapter := &SourceEpisodeBrainAdapter{Catalog: catalog, Authority: authority, Bodies: &sourceEpisodeBrainTestBodies{bodies: bodies}, PageSize: 8, Now: func() time.Time { return sourceEpisodeTestTime.Add(time.Hour) }}
			sources, _ := collectSourceEpisodeInventory(t, adapter, sourceEpisodeBrainPrincipal("person_1"))
			var target BrainEvidenceRef
			for _, source := range sources {
				if source.Evidence.ObjectID == episodes[1].RetrievalBody.ObjectID {
					target = source.Evidence
				}
			}
			if target.ObjectID == "" {
				t.Fatal("missing target evidence")
			}
			if cause == "purge" {
				authority.currentPurge++
				catalog.purgeGeneration++
			} else {
				authority.revoked[episodes[1].Header.ID] = cause
			}
			if _, err := adapter.ReadBrainSource(context.Background(), target); !errors.Is(err, ErrBrainRetrievalRetry) {
				t.Fatalf("%s revocation did not invalidate body read: %v", cause, err)
			}
			fresh, _ := collectSourceEpisodeInventory(t, adapter, sourceEpisodeBrainPrincipal("person_1"))
			for _, source := range fresh {
				if source.Evidence.ObjectID == target.ObjectID {
					t.Fatalf("%s-revoked source influenced fresh inventory", cause)
				}
			}
		})
	}
}
