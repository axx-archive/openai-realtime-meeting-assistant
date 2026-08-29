package main

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

var (
	ErrSourceEpisodeCatalogUnavailable = errors.New("source episode catalog is unavailable")
	ErrSourceEpisodeAuthorityDenied    = errors.New("source episode authority denied")
	ErrSourceEpisodeAuthorityStale     = errors.New("source episode authority is stale")
	ErrSourceEpisodeBodyMissing        = errors.New("source episode native body is missing")
)

// SourceEpisodeCatalogPage is body-free. SnapshotID and PurgeGeneration must
// stay identical across the catalog scan or the brain inventory retries.
type SourceEpisodeCatalogPage struct {
	SnapshotID      string
	SnapshotAt      time.Time
	PurgeGeneration int64
	Episodes        []SourceEpisode
	NextCursor      string
	Terminal        bool
}

type SourceEpisodeBodyLocator struct {
	SourceFamily    string
	ObjectID        string
	ContentRevision int64
	ContentDigest   string
}

func sourceEpisodeBodyLocator(ref SourceEpisodeRevisionRef) SourceEpisodeBodyLocator {
	return SourceEpisodeBodyLocator{
		SourceFamily: ref.SourceFamily, ObjectID: ref.ObjectID, ContentRevision: ref.ContentRevision, ContentDigest: ref.ContentDigest,
	}
}

type DurableSourceEpisodeCatalog interface {
	ListSourceEpisodes(context.Context, string, string) (SourceEpisodeCatalogPage, error)
	FindSourceEpisodeByRetrievalBody(context.Context, string, SourceEpisodeBodyLocator) (SourceEpisode, bool, error)
}

// SourceEpisodeBrainAuthority excludes inaccessible metadata before inventory
// counts and holds exact native revision, ACL, consent, purge, and retention
// authority current while a body is read.
type SourceEpisodeBrainAuthority interface {
	AuthorizeSourceEpisodeMetadata(context.Context, ACLPrincipal, SourceEpisode) (bool, error)
	WithCurrentSourceEpisodeAuthority(context.Context, SourceEpisode, func() error) error
}

type SourceEpisodeNativeBody struct {
	Revision SourceEpisodeRevisionRef
	Body     string
}

type SourceEpisodeNativeBodyReader interface {
	ReadExactSourceEpisodeBody(context.Context, SourceEpisodeRevisionRef) (SourceEpisodeNativeBody, error)
}

// SourceEpisodeBrainAdapter is the new shadow lane. It implements the existing
// retrieval interfaces without changing MeetingMemoryBrainAdapter.
type SourceEpisodeBrainAdapter struct {
	Catalog   DurableSourceEpisodeCatalog
	Authority SourceEpisodeBrainAuthority
	Bodies    SourceEpisodeNativeBodyReader
	PageSize  int
	Now       func() time.Time
}

var _ BrainSourceMetadataInventory = (*SourceEpisodeBrainAdapter)(nil)
var _ BrainSourceBodyReader = (*SourceEpisodeBrainAdapter)(nil)

func (adapter *SourceEpisodeBrainAdapter) InventoryBrainSources(ctx context.Context, request BrainSourceInventoryRequest, cursor string) (BrainSourceInventoryPage, error) {
	if adapter == nil || adapter.Catalog == nil || adapter.Authority == nil || adapter.Bodies == nil || adapter.PageSize < 1 ||
		!strideIdentifier(request.TenantID) || request.Principal.TenantID != request.TenantID || !strideIdentifier(request.Principal.ID) ||
		request.Temporal.Validate() != nil || request.Principal.Kind == ACLPrincipalGuest || request.Principal.Kind == ACLPrincipalCapability {
		return BrainSourceInventoryPage{}, ErrBrainRetrievalInvalid
	}
	expectedID, offset, snapshotAt, err := parseSourceEpisodeInventoryCursor(cursor)
	if err != nil {
		return BrainSourceInventoryPage{}, ErrBrainRetrievalInvalid
	}
	sources, purgeGeneration, err := adapter.authorizedInventory(ctx, request)
	if err != nil {
		return BrainSourceInventoryPage{}, err
	}
	manifest, err := brainInventoryManifestDigest(sources)
	if err != nil {
		return BrainSourceInventoryPage{}, err
	}
	inventoryID, err := sourceEpisodeInventoryID(request, manifest, purgeGeneration)
	if err != nil {
		return BrainSourceInventoryPage{}, err
	}
	if cursor == "" {
		snapshotAt = time.Now().UTC()
		if adapter.Now != nil {
			snapshotAt = adapter.Now().UTC()
		}
	} else if expectedID != inventoryID {
		return BrainSourceInventoryPage{}, fmt.Errorf("%w: authorized source inventory changed between pages", ErrBrainRetrievalRetry)
	}
	if snapshotAt.IsZero() || snapshotAt.Location() != time.UTC || offset < 0 || offset > len(sources) {
		return BrainSourceInventoryPage{}, ErrBrainRetrievalInvalid
	}
	end := offset + adapter.PageSize
	if end > len(sources) {
		end = len(sources)
	}
	terminal := end == len(sources)
	next := ""
	if !terminal {
		next = sourceEpisodeInventoryCursor(inventoryID, end, snapshotAt)
	}
	authorizedHighWater := uint64(len(sources))
	return BrainSourceInventoryPage{
		InventoryID: inventoryID, InventoryManifest: manifest, ExpectedSourceCount: authorizedHighWater,
		Sources: append([]BrainSourceMetadata(nil), sources[offset:end]...), NextCursor: next, Terminal: terminal,
		SourceHighWater: authorizedHighWater, CaptureCompleteThrough: authorizedHighWater, ProjectionHighWater: authorizedHighWater,
		ResolvedStartUTC: request.Temporal.StartUTC, ResolvedEndUTC: request.Temporal.EndUTC, SnapshotAt: snapshotAt,
	}, nil
}

func (adapter *SourceEpisodeBrainAdapter) authorizedInventory(ctx context.Context, request BrainSourceInventoryRequest) ([]BrainSourceMetadata, int64, error) {
	var catalogID string
	var catalogAt time.Time
	var purgeGeneration int64
	var episodes []SourceEpisode
	cursor := ""
	seen := map[string]bool{}
	for {
		page, err := adapter.Catalog.ListSourceEpisodes(ctx, request.TenantID, cursor)
		if err != nil {
			return nil, 0, fmt.Errorf("%w: %v", ErrBrainRetrievalUnavailable, err)
		}
		if !isHexDigest(page.SnapshotID) || page.SnapshotAt.IsZero() || page.SnapshotAt.Location() != time.UTC || page.PurgeGeneration < 0 ||
			(page.NextCursor == "" && !page.Terminal) || (page.NextCursor != "" && page.Terminal) {
			return nil, 0, ErrBrainRetrievalInvalid
		}
		if catalogID == "" {
			catalogID, catalogAt, purgeGeneration = page.SnapshotID, page.SnapshotAt, page.PurgeGeneration
		} else if page.SnapshotID != catalogID || !page.SnapshotAt.Equal(catalogAt) || page.PurgeGeneration != purgeGeneration {
			return nil, 0, fmt.Errorf("%w: durable episode catalog changed between pages", ErrBrainRetrievalRetry)
		}
		episodes = append(episodes, page.Episodes...)
		if page.NextCursor == "" {
			break
		}
		if page.NextCursor == cursor || seen[page.NextCursor] {
			return nil, 0, ErrBrainRetrievalInvalid
		}
		seen[page.NextCursor] = true
		cursor = page.NextCursor
	}
	metadata := make([]BrainSourceMetadata, 0, len(episodes))
	seenEpisodes := map[string]bool{}
	for _, episode := range episodes {
		if episode.Validate() != nil || episode.Header.TenantID != request.TenantID {
			return nil, 0, ErrBrainRetrievalInvalid
		}
		key := episode.Header.ID + "\x00" + strconv.FormatInt(episode.Header.Revision, 10)
		if seenEpisodes[key] {
			return nil, 0, ErrBrainRetrievalInvalid
		}
		seenEpisodes[key] = true
		if episode.Authority.PurgeGeneration != purgeGeneration || !sourceEpisodeScopeMatchesTemporal(episode.Scope, request.Temporal) ||
			!episode.OccurredStart.Before(request.Temporal.EndUTC) || !request.Temporal.StartUTC.Before(episode.OccurredEnd) {
			continue
		}
		allowed, err := adapter.Authority.AuthorizeSourceEpisodeMetadata(ctx, request.Principal, episode)
		if err != nil {
			if errors.Is(err, ErrSourceEpisodeAuthorityDenied) {
				continue
			}
			return nil, 0, fmt.Errorf("%w: source episode authority", ErrBrainRetrievalUnavailable)
		}
		if !allowed {
			continue
		}
		if episode.RetrievalBody.SizeBytes > int64(^uint(0)>>1) {
			return nil, 0, ErrBrainRetrievalInvalid
		}
		ref := BrainEvidenceRef{
			TenantID: request.TenantID, SourceFamily: episode.RetrievalBody.SourceFamily, ObjectID: episode.RetrievalBody.ObjectID,
			ContentRevision: episode.RetrievalBody.ContentRevision, ACLVersion: episode.Authority.ACLRevision, ContentDigest: episode.RetrievalBody.ContentDigest,
			RoomID: episode.Scope.RoomID, SittingID: episode.Scope.SittingID, OccurredStart: episode.OccurredStart, OccurredEnd: episode.OccurredEnd,
			PurgeGeneration: purgeGeneration, Trust: BrainEvidenceTrusted,
		}
		metadata = append(metadata, BrainSourceMetadata{
			Evidence: ref, CapturedAt: episode.Header.CreatedAt,
			Segments: []BrainSourceSegmentMetadata{{OccurredStart: episode.OccurredStart, OccurredEnd: episode.OccurredEnd, ByteStart: 0, ByteEnd: int(episode.RetrievalBody.SizeBytes)}},
		})
	}
	sort.Slice(metadata, func(i, j int) bool {
		left, right := metadata[i].Evidence, metadata[j].Evidence
		if left.SourceFamily != right.SourceFamily {
			return left.SourceFamily < right.SourceFamily
		}
		if left.ObjectID != right.ObjectID {
			return left.ObjectID < right.ObjectID
		}
		return left.ContentRevision < right.ContentRevision
	})
	for index := range metadata {
		metadata[index].CaptureSequence = uint64(index + 1)
	}
	return metadata, purgeGeneration, nil
}

func (adapter *SourceEpisodeBrainAdapter) ReadBrainSource(ctx context.Context, expected BrainEvidenceRef) (BrainSourceRead, error) {
	if adapter == nil || adapter.Catalog == nil || adapter.Authority == nil || adapter.Bodies == nil || expected.Validate() != nil {
		return BrainSourceRead{}, ErrBrainRetrievalInvalid
	}
	bodyLocator := SourceEpisodeBodyLocator{
		SourceFamily: expected.SourceFamily, ObjectID: expected.ObjectID, ContentRevision: expected.ContentRevision,
		ContentDigest: expected.ContentDigest,
	}
	episode, found, err := adapter.Catalog.FindSourceEpisodeByRetrievalBody(ctx, expected.TenantID, bodyLocator)
	if err != nil {
		return BrainSourceRead{}, fmt.Errorf("%w: source episode lookup", ErrBrainRetrievalUnavailable)
	}
	if !found {
		return BrainSourceRead{Evidence: expected, Status: RecallSourceMissing}, nil
	}
	if episode.Validate() != nil || sourceEpisodeBodyLocator(episode.RetrievalBody) != bodyLocator || episode.Header.TenantID != expected.TenantID ||
		episode.Authority.ACLRevision != expected.ACLVersion || episode.Authority.PurgeGeneration != expected.PurgeGeneration ||
		episode.Scope.RoomID != expected.RoomID || episode.Scope.SittingID != expected.SittingID ||
		episode.OccurredStart.After(expected.OccurredStart) || episode.OccurredEnd.Before(expected.OccurredEnd) {
		return BrainSourceRead{}, ErrBrainRetrievalRetry
	}
	var native SourceEpisodeNativeBody
	err = adapter.Authority.WithCurrentSourceEpisodeAuthority(ctx, episode, func() error {
		var readErr error
		native, readErr = adapter.Bodies.ReadExactSourceEpisodeBody(ctx, episode.RetrievalBody)
		return readErr
	})
	if err != nil {
		if errors.Is(err, ErrSourceEpisodeBodyMissing) {
			return BrainSourceRead{Evidence: expected, Status: RecallSourceMissing}, nil
		}
		return BrainSourceRead{}, ErrBrainRetrievalRetry
	}
	if native.Revision != episode.RetrievalBody || int64(len(native.Body)) != episode.RetrievalBody.SizeBytes || digestBrainString(native.Body) != episode.RetrievalBody.ContentDigest {
		return BrainSourceRead{}, ErrBrainRetrievalRetry
	}
	return BrainSourceRead{Evidence: expected, Body: native.Body, BodyDigest: episode.RetrievalBody.ContentDigest, BodyAvailable: true, Status: RecallSourceFresh}, nil
}

func sourceEpisodeScopeMatchesTemporal(scope SourceEpisodeScope, temporal TemporalQuery) bool {
	if temporal.RoomID != "" && scope.RoomID != temporal.RoomID {
		return false
	}
	if temporal.SittingID != "" && scope.SittingID != temporal.SittingID {
		return false
	}
	return true
}

func sourceEpisodeInventoryID(request BrainSourceInventoryRequest, manifest string, purgeGeneration int64) (string, error) {
	return STRIDEContractDigest(struct {
		TenantID        string
		PrincipalKind   ACLPrincipalKind
		PrincipalID     string
		PrincipalTeams  []string
		Temporal        TemporalQuery
		Manifest        string
		PurgeGeneration int64
	}{request.TenantID, request.Principal.Kind, request.Principal.ID, uniqueSortedStrings(request.Principal.TeamIDs), request.Temporal, manifest, purgeGeneration})
}

func sourceEpisodeInventoryCursor(inventoryID string, offset int, snapshotAt time.Time) string {
	return inventoryID + "." + strconv.Itoa(offset) + "." + strconv.FormatInt(snapshotAt.UnixNano(), 10)
}

func parseSourceEpisodeInventoryCursor(cursor string) (string, int, time.Time, error) {
	if cursor == "" {
		return "", 0, time.Time{}, nil
	}
	parts := strings.Split(cursor, ".")
	if len(parts) != 3 || !isHexDigest(parts[0]) {
		return "", 0, time.Time{}, ErrBrainRetrievalInvalid
	}
	offset, err := strconv.Atoi(parts[1])
	if err != nil || offset < 0 {
		return "", 0, time.Time{}, ErrBrainRetrievalInvalid
	}
	nanos, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil || nanos <= 0 {
		return "", 0, time.Time{}, ErrBrainRetrievalInvalid
	}
	return parts[0], offset, time.Unix(0, nanos).UTC(), nil
}
