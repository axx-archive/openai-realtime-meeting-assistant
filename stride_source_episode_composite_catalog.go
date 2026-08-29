package main

import (
	"context"
	"sort"
	"strconv"
	"time"
)

// CompositeSourceEpisodeCatalog keeps the PostgreSQL meeting ledger
// authoritative for meeting analysis while retaining the file-backed native
// source catalog during the incremental migration. Meeting episodes from the
// native catalog are deliberately ignored so a stale development-ledger entry
// can never resurrect a PostgreSQL tombstone.
type CompositeSourceEpisodeCatalog struct {
	Native   DurableSourceEpisodeCatalog
	Meetings DurableSourceEpisodeCatalog
}

func NewCompositeSourceEpisodeCatalog(native, meetings DurableSourceEpisodeCatalog) (*CompositeSourceEpisodeCatalog, error) {
	if native == nil || meetings == nil {
		return nil, ErrSourceEpisodeCatalogUnavailable
	}
	return &CompositeSourceEpisodeCatalog{Native: native, Meetings: meetings}, nil
}

func (catalog *CompositeSourceEpisodeCatalog) ListSourceEpisodes(ctx context.Context, tenantID, cursor string) (SourceEpisodeCatalogPage, error) {
	if catalog == nil || catalog.Native == nil || catalog.Meetings == nil || !strideIdentifier(tenantID) {
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
	native, nativeID, nativeAt, nativePurge, err := sourceEpisodeCatalogSnapshot(ctx, catalog.Native, tenantID)
	if err != nil {
		return SourceEpisodeCatalogPage{}, err
	}
	meetings, meetingID, meetingAt, meetingPurge, err := sourceEpisodeCatalogSnapshot(ctx, catalog.Meetings, tenantID)
	if err != nil {
		return SourceEpisodeCatalogPage{}, err
	}
	if nativePurge != meetingPurge {
		return SourceEpisodeCatalogPage{}, ErrSourceEpisodeAuthorityStale
	}
	episodes := make([]SourceEpisode, 0, len(native)+len(meetings))
	seen := map[string]bool{}
	for _, episode := range meetings {
		if episode.Kind != SourceEpisodeMeetingAnalysis {
			return SourceEpisodeCatalogPage{}, ErrSourceEpisodeInvalid
		}
		key := episode.Header.ID + "\x00" + strconv.FormatInt(episode.Header.Revision, 10)
		seen[key] = true
		episodes = append(episodes, episode)
	}
	for _, episode := range native {
		if episode.Kind == SourceEpisodeMeetingAnalysis {
			continue
		}
		key := episode.Header.ID + "\x00" + strconv.FormatInt(episode.Header.Revision, 10)
		if seen[key] {
			return SourceEpisodeCatalogPage{}, ErrSourceEpisodeConflict
		}
		seen[key] = true
		episodes = append(episodes, episode)
	}
	sort.Slice(episodes, func(i, j int) bool {
		if episodes[i].Header.ID != episodes[j].Header.ID {
			return episodes[i].Header.ID < episodes[j].Header.ID
		}
		return episodes[i].Header.Revision < episodes[j].Header.Revision
	})
	if start > len(episodes) {
		return SourceEpisodeCatalogPage{}, ErrSourceEpisodeInvalid
	}
	snapshotID, err := STRIDEContractDigest(struct {
		TenantID, NativeSnapshot, MeetingSnapshot string
		Purge                                     int64
		Episodes                                  []SourceEpisode
	}{tenantID, nativeID, meetingID, nativePurge, episodes})
	if err != nil {
		return SourceEpisodeCatalogPage{}, err
	}
	snapshotAt := nativeAt
	if meetingAt.After(snapshotAt) {
		snapshotAt = meetingAt
	}
	end := start + 128
	if end > len(episodes) {
		end = len(episodes)
	}
	terminal := end == len(episodes)
	next := ""
	if !terminal {
		next = strconv.Itoa(end)
	}
	return SourceEpisodeCatalogPage{
		SnapshotID: snapshotID, SnapshotAt: snapshotAt, PurgeGeneration: nativePurge,
		Episodes: cloneSourceEpisodes(episodes[start:end]), NextCursor: next, Terminal: terminal,
	}, nil
}

func (catalog *CompositeSourceEpisodeCatalog) FindSourceEpisodeByRetrievalBody(ctx context.Context, tenantID string, locator SourceEpisodeBodyLocator) (SourceEpisode, bool, error) {
	if catalog == nil || catalog.Native == nil || catalog.Meetings == nil {
		return SourceEpisode{}, false, ErrSourceEpisodeCatalogUnavailable
	}
	if locator.SourceFamily == SourceEpisodeFamilyMeetingAnalysisBody {
		return catalog.Meetings.FindSourceEpisodeByRetrievalBody(ctx, tenantID, locator)
	}
	return catalog.Native.FindSourceEpisodeByRetrievalBody(ctx, tenantID, locator)
}

func (catalog *CompositeSourceEpisodeCatalog) FindSourceEpisodeByACLObject(ctx context.Context, ref ACLObjectRef) (SourceEpisode, bool, error) {
	if catalog == nil {
		return SourceEpisode{}, false, ErrSourceEpisodeCatalogUnavailable
	}
	selected := catalog.Native
	if ref.Type == SourceEpisodeFamilyMeetingAnalysisBody {
		selected = catalog.Meetings
	}
	authorization, ok := selected.(DurableSourceEpisodeAuthorizationCatalog)
	if !ok {
		return SourceEpisode{}, false, ErrSourceEpisodeCatalogUnavailable
	}
	return authorization.FindSourceEpisodeByACLObject(ctx, ref)
}

func sourceEpisodeCatalogSnapshot(ctx context.Context, catalog DurableSourceEpisodeCatalog, tenantID string) ([]SourceEpisode, string, time.Time, int64, error) {
	var episodes []SourceEpisode
	var snapshotID string
	var snapshotAt time.Time
	var purge int64
	cursor := ""
	seenCursors := map[string]bool{}
	for {
		page, err := catalog.ListSourceEpisodes(ctx, tenantID, cursor)
		if err != nil {
			return nil, "", time.Time{}, 0, err
		}
		if !isHexDigest(page.SnapshotID) || page.SnapshotAt.IsZero() || page.SnapshotAt.Location() != time.UTC || page.PurgeGeneration < 0 ||
			(page.Terminal && page.NextCursor != "") || (!page.Terminal && page.NextCursor == "") {
			return nil, "", time.Time{}, 0, ErrSourceEpisodeInvalid
		}
		if snapshotID == "" {
			snapshotID, snapshotAt, purge = page.SnapshotID, page.SnapshotAt, page.PurgeGeneration
		} else if page.SnapshotID != snapshotID || !page.SnapshotAt.Equal(snapshotAt) || page.PurgeGeneration != purge {
			return nil, "", time.Time{}, 0, ErrSourceEpisodeAuthorityStale
		}
		episodes = append(episodes, page.Episodes...)
		if page.Terminal {
			return episodes, snapshotID, snapshotAt, purge, nil
		}
		if page.NextCursor == cursor || seenCursors[page.NextCursor] {
			return nil, "", time.Time{}, 0, ErrSourceEpisodeInvalid
		}
		seenCursors[page.NextCursor] = true
		cursor = page.NextCursor
	}
}

var _ DurableSourceEpisodeCatalog = (*CompositeSourceEpisodeCatalog)(nil)
var _ DurableSourceEpisodeAuthorizationCatalog = (*CompositeSourceEpisodeCatalog)(nil)
