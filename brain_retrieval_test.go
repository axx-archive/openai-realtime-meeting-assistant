package main

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

type testBrainInventory struct {
	mu    sync.Mutex
	pages map[string]BrainSourceInventoryPage
	calls int
}

func (inventory *testBrainInventory) InventoryBrainSources(_ context.Context, _ BrainSourceInventoryRequest, cursor string) (BrainSourceInventoryPage, error) {
	inventory.mu.Lock()
	defer inventory.mu.Unlock()
	inventory.calls++
	page, found := inventory.pages[cursor]
	if !found {
		return BrainSourceInventoryPage{}, errors.New("missing inventory page")
	}
	page.Sources = append([]BrainSourceMetadata(nil), page.Sources...)
	return page, nil
}

type testBrainBodyReader struct {
	mu       sync.Mutex
	bodies   map[string]string
	digests  map[string]string
	statuses map[string]RecallSourceStatus
	errors   map[string]error
	reads    []string
	after    func(BrainEvidenceRef)
}

func (reader *testBrainBodyReader) ReadBrainSource(_ context.Context, expected BrainEvidenceRef) (BrainSourceRead, error) {
	reader.mu.Lock()
	reader.reads = append(reader.reads, expected.ObjectID)
	body := reader.bodies[expected.ObjectID]
	bodyDigest := expected.ContentDigest
	if override, found := reader.digests[expected.ObjectID]; found {
		bodyDigest = override
	}
	status := reader.statuses[expected.ObjectID]
	err := reader.errors[expected.ObjectID]
	after := reader.after
	reader.mu.Unlock()
	if after != nil {
		after(expected)
	}
	if err != nil {
		return BrainSourceRead{}, err
	}
	if status == "" {
		status = RecallSourceFresh
	}
	available := status == RecallSourceFresh || status == RecallSourcePartial
	return BrainSourceRead{Evidence: expected, Body: body, BodyDigest: bodyDigest, BodyAvailable: available, Status: status}, nil
}

type testBrainPurgeResolver struct {
	mu     sync.Mutex
	values []int64
	calls  int
}

func (resolver *testBrainPurgeResolver) CurrentPurgeGeneration(context.Context, string) (int64, error) {
	resolver.mu.Lock()
	defer resolver.mu.Unlock()
	index := resolver.calls
	resolver.calls++
	if len(resolver.values) == 0 {
		return 0, errors.New("purge unavailable")
	}
	if index >= len(resolver.values) {
		index = len(resolver.values) - 1
	}
	return resolver.values[index], nil
}

type testBrainLaneHealth struct {
	lanes RecallLaneCoverage
	err   error
}

func (health testBrainLaneHealth) CurrentRecallLaneHealth(context.Context, string, TemporalQuery) (RecallLaneCoverage, error) {
	return health.lanes, health.err
}

type loggingBrainACLStore struct {
	base *MemoryACLStore
	mu   sync.Mutex
	log  []string
}

func (store *loggingBrainACLStore) ResolveACLObject(ctx context.Context, ref ACLObjectRef) (ACLObject, error) {
	store.mu.Lock()
	store.log = append(store.log, "resolve:"+ref.ID)
	store.mu.Unlock()
	return store.base.ResolveACLObject(ctx, ref)
}

func (store *loggingBrainACLStore) ListACLGrants(ctx context.Context, ref ACLObjectRef) ([]ACLGrant, error) {
	store.mu.Lock()
	store.log = append(store.log, "grants:"+ref.ID)
	store.mu.Unlock()
	return store.base.ListACLGrants(ctx, ref)
}

func testBrainTemporal(t *testing.T) TemporalQuery {
	t.Helper()
	query, err := NewBoundedTemporalQuery(TemporalExplicitRange,
		time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
		"America/Los_Angeles", "", "", "ninety day company recall")
	if err != nil {
		t.Fatal(err)
	}
	return query
}

func testBrainMetadata(index int, body string, temporal TemporalQuery) BrainSourceMetadata {
	start := temporal.StartUTC.Add(time.Duration(index+1) * time.Minute)
	return BrainSourceMetadata{
		Evidence: BrainEvidenceRef{
			TenantID: "tenant-a", SourceFamily: "transcript", ObjectID: fmt.Sprintf("source-%04d", index),
			ContentRevision: 1, ACLVersion: 1, ContentDigest: digestBrainString(body), RoomID: fmt.Sprintf("room-%d", index%7), SittingID: fmt.Sprintf("sitting-%d", index),
			OccurredStart: start, OccurredEnd: start.Add(30 * time.Second), PurgeGeneration: 0, Trust: BrainEvidenceTrusted,
		},
		CaptureSequence: uint64(index + 1), CapturedAt: start.Add(time.Minute),
	}
}

func testBrainInventoryPages(temporal TemporalQuery, sources []BrainSourceMetadata, pageSize int) map[string]BrainSourceInventoryPage {
	pages := map[string]BrainSourceInventoryPage{}
	inventoryID := digestBrainString("stable-test-inventory")
	manifest, err := brainInventoryManifestDigest(sources)
	if err != nil {
		panic(err)
	}
	snapshotAt := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	if pageSize < 1 {
		pageSize = len(sources) + 1
	}
	for start, cursor := 0, ""; ; {
		end := start + pageSize
		if end > len(sources) {
			end = len(sources)
		}
		next := ""
		if end < len(sources) {
			next = fmt.Sprintf("page-%d", end)
		}
		pages[cursor] = BrainSourceInventoryPage{
			InventoryID: inventoryID, InventoryManifest: manifest, ExpectedSourceCount: uint64(len(sources)),
			Sources: append([]BrainSourceMetadata(nil), sources[start:end]...), NextCursor: next, Terminal: next == "",
			SourceHighWater: uint64(len(sources)), CaptureCompleteThrough: uint64(len(sources)), ProjectionHighWater: uint64(len(sources)),
			ResolvedStartUTC: temporal.StartUTC, ResolvedEndUTC: temporal.EndUTC, SnapshotAt: snapshotAt,
		}
		if next == "" {
			break
		}
		start, cursor = end, next
	}
	return pages
}

func testBrainACL(sources []BrainSourceMetadata, principal ACLPrincipal, allowedIDs map[string]bool) *MemoryACLStore {
	store := &MemoryACLStore{Objects: map[string]ACLObject{}, Grants: map[string][]ACLGrant{}}
	for _, source := range sources {
		ref, _ := source.Evidence.ACLRefs()
		store.Objects[aclObjectKey(ref)] = ACLObject{Ref: ref, RoomID: source.Evidence.RoomID, SittingID: source.Evidence.SittingID,
			CurrentContentRevision: source.Evidence.ContentRevision, CurrentContentDigest: source.Evidence.ContentDigest}
		if allowedIDs == nil || allowedIDs[source.Evidence.ObjectID] {
			store.Grants[aclObjectKey(ref)] = []ACLGrant{{
				ID: "grant-" + source.Evidence.ObjectID, TenantID: ref.TenantID, ObjectType: ref.Type, ObjectID: ref.ID, ACLVersion: ref.ACLVersion,
				SubjectKind: ACLSubjectPrincipal, SubjectID: principal.ID, SubjectPrincipalKind: principal.Kind,
				Actions: []ACLAction{ACLReadMetadata, ACLReadContent},
			}}
		}
	}
	return store
}

func newTestBrainPlanner(t *testing.T, temporal TemporalQuery, sources []BrainSourceMetadata, bodies map[string]string, pageSize int, allowed map[string]bool) (BrainRetrievalPlanner, *testBrainInventory, *testBrainBodyReader, ACLPrincipal, *MemoryACLStore) {
	t.Helper()
	principal := ACLPrincipal{TenantID: "tenant-a", ID: "member-1", Kind: ACLPrincipalUser}
	inventory := &testBrainInventory{pages: testBrainInventoryPages(temporal, sources, pageSize)}
	reader := &testBrainBodyReader{bodies: bodies, digests: map[string]string{}, statuses: map[string]RecallSourceStatus{}, errors: map[string]error{}}
	acl := testBrainACL(sources, principal, allowed)
	planner := BrainRetrievalPlanner{
		Inventory: inventory, Bodies: reader, Kernel: AuthorizationKernel{Store: acl, Now: func() time.Time { return time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC) }},
		Purge: &testBrainPurgeResolver{values: []int64{0}}, PromptLimits: BrainPromptLimits{MaxSourceChunkBytes: 64, MaxPromptBytes: 256, MaxFoldInputs: 4, MaxFoldOutputBytes: 64},
	}
	return planner, inventory, reader, principal, acl
}

func TestBrainRetrievalInventoriesEntireRangeBeyondLegacyLimit(t *testing.T) {
	temporal := testBrainTemporal(t)
	const sourceCount = 277
	sources := make([]BrainSourceMetadata, 0, sourceCount)
	bodies := make(map[string]string, sourceCount)
	for index := 0; index < sourceCount; index++ {
		body := fmt.Sprintf("meeting %d carries full range primary evidence", index)
		source := testBrainMetadata(index, body, temporal)
		sources = append(sources, source)
		bodies[source.Evidence.ObjectID] = body
	}
	planner, inventory, reader, principal, _ := newTestBrainPlanner(t, temporal, sources, bodies, 71, nil)
	result, err := planner.Resolve(context.Background(), BrainRetrievalRequest{Principal: principal, Query: "primary evidence", Temporal: temporal})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Snapshot.Sources) != sourceCount || result.Coverage.AuthorizedSources != sourceCount || result.Coverage.FreshSources != sourceCount ||
		len(result.Sources) != sourceCount || result.PromptPlan.InventorySourceCount != sourceCount || result.PromptPlan.ReadableSourceCount != sourceCount {
		t.Fatalf("full-range counts snapshot=%d coverage=%+v sources=%d plan=%+v", len(result.Snapshot.Sources), result.Coverage, len(result.Sources), result.PromptPlan)
	}
	if result.Coverage.Status != RecallCoverageComplete || inventory.calls != 4 || len(reader.reads) != sourceCount {
		t.Fatalf("status=%s inventoryCalls=%d bodyReads=%d", result.Coverage.Status, inventory.calls, len(reader.reads))
	}
	if len(result.PromptPlan.Batches) < 2 || len(result.PromptPlan.Folds) == 0 || result.PromptPlan.RootID == "" {
		t.Fatalf("expected bounded multi-stage prompt plan: %+v", result.PromptPlan)
	}
	for _, batch := range result.PromptPlan.Batches {
		if batch.InputBytes > result.PromptPlan.Limits.MaxPromptBytes {
			t.Fatalf("oversized batch=%+v", batch)
		}
	}
}

func TestBrainRetrievalAuthorizesMetadataBeforeBodiesAndDoesNotLeakDeniedCounts(t *testing.T) {
	temporal := testBrainTemporal(t)
	bodies := map[string]string{}
	sources := make([]BrainSourceMetadata, 3)
	for index, label := range []string{"authorized organization text", "PRIVATE-CANARY", "ROOM-ONLY-CANARY"} {
		sources[index] = testBrainMetadata(index, label, temporal)
		bodies[sources[index].Evidence.ObjectID] = label
	}
	allowed := map[string]bool{sources[0].Evidence.ObjectID: true}
	planner, _, reader, principal, acl := newTestBrainPlanner(t, temporal, sources, bodies, 3, allowed)
	loggingStore := &loggingBrainACLStore{base: acl}
	planner.Kernel.Store = loggingStore
	result, err := planner.Resolve(context.Background(), BrainRetrievalRequest{Principal: principal, Query: "canary", Temporal: temporal})
	if err != nil {
		t.Fatal(err)
	}
	if result.Coverage.AuthorizedSources != 1 || len(result.Coverage.Sources) != 1 || len(result.Snapshot.Sources) != 1 || len(result.Sources) != 1 {
		t.Fatalf("denied count leaked into result: %+v", result)
	}
	if !reflect.DeepEqual(reader.reads, []string{sources[0].Evidence.ObjectID}) {
		t.Fatalf("body reader observed denied IDs: %v", reader.reads)
	}
	rendered := fmt.Sprintf("%+v", result)
	if strings.Contains(rendered, "PRIVATE-CANARY") || strings.Contains(rendered, "ROOM-ONLY-CANARY") || strings.Contains(rendered, sources[1].Evidence.ObjectID) || strings.Contains(rendered, sources[2].Evidence.ObjectID) {
		t.Fatalf("denied metadata or body leaked: %s", rendered)
	}
	loggingStore.mu.Lock()
	log := append([]string(nil), loggingStore.log...)
	loggingStore.mu.Unlock()
	firstBodyWasAuthorized := false
	for index := range log {
		if log[index] == "grants:"+sources[0].Evidence.ObjectID {
			firstBodyWasAuthorized = true
			break
		}
	}
	if !firstBodyWasAuthorized {
		t.Fatalf("authorization log=%v", log)
	}
}

func TestBrainRetrievalRevisionAndPurgeDriftRequireRetry(t *testing.T) {
	temporal := testBrainTemporal(t)
	body := "revision fenced body"
	source := testBrainMetadata(0, body, temporal)

	t.Run("revision", func(t *testing.T) {
		planner, _, reader, principal, acl := newTestBrainPlanner(t, temporal, []BrainSourceMetadata{source}, map[string]string{source.Evidence.ObjectID: body}, 1, nil)
		reader.after = func(expected BrainEvidenceRef) {
			ref, _ := expected.ACLRefs()
			acl.mu.Lock()
			object := acl.Objects[aclObjectKey(ref)]
			object.CurrentContentRevision++
			object.CurrentContentDigest = digestBrainString("new revision")
			acl.Objects[aclObjectKey(ref)] = object
			acl.mu.Unlock()
		}
		_, err := planner.Resolve(context.Background(), BrainRetrievalRequest{Principal: principal, Query: "revision", Temporal: temporal})
		if !errors.Is(err, ErrBrainRetrievalRetry) {
			t.Fatalf("revision drift error=%v", err)
		}
	})

	t.Run("purge", func(t *testing.T) {
		planner, _, _, principal, _ := newTestBrainPlanner(t, temporal, []BrainSourceMetadata{source}, map[string]string{source.Evidence.ObjectID: body}, 1, nil)
		planner.Purge = &testBrainPurgeResolver{values: []int64{0, 1}}
		_, err := planner.Resolve(context.Background(), BrainRetrievalRequest{Principal: principal, Query: "purge", Temporal: temporal})
		if !errors.Is(err, ErrBrainRetrievalRetry) {
			t.Fatalf("purge drift error=%v", err)
		}
	})

	t.Run("body digest", func(t *testing.T) {
		planner, _, reader, principal, _ := newTestBrainPlanner(t, temporal, []BrainSourceMetadata{source}, map[string]string{source.Evidence.ObjectID: body}, 1, nil)
		reader.digests[source.Evidence.ObjectID] = digestBrainString("different body revision")
		_, err := planner.Resolve(context.Background(), BrainRetrievalRequest{Principal: principal, Query: "digest", Temporal: temporal})
		if !errors.Is(err, ErrBrainRetrievalRetry) {
			t.Fatalf("body digest drift error=%v", err)
		}
	})
}

func TestBrainRetrievalSemanticOutageDoesNotGateCompleteLexicalRawResult(t *testing.T) {
	temporal := testBrainTemporal(t)
	bodies := map[string]string{}
	sources := make([]BrainSourceMetadata, 0, 3)
	for index := 0; index < 3; index++ {
		body := fmt.Sprintf("blocked launch decision %d", index)
		source := testBrainMetadata(index, body, temporal)
		sources = append(sources, source)
		bodies[source.Evidence.ObjectID] = body
	}
	planner, inventory, _, principal, _ := newTestBrainPlanner(t, temporal, sources, bodies, 2, nil)
	for cursor, page := range inventory.pages {
		page.ProjectionHighWater = 0
		inventory.pages[cursor] = page
	}
	planner.LaneHealth = testBrainLaneHealth{lanes: RecallLaneCoverage{Lexical: RecallLaneActive, Semantic: RecallLaneUnavailable, Digest: RecallLaneNotRequired, Raw: RecallLaneActive}}
	result, err := planner.Resolve(context.Background(), BrainRetrievalRequest{Principal: principal, Query: "blocked launch", Temporal: temporal})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Sources) != 3 || len(result.LexicalMatches) != 3 || result.Coverage.FreshSources != 3 || result.Coverage.Lanes.Raw != RecallLaneActive || result.Coverage.Lanes.Lexical != RecallLaneActive {
		t.Fatalf("semantic outage gated raw/lexical result: %+v", result)
	}
	if result.Coverage.Status != RecallCoverageComplete || result.Coverage.Lanes.Semantic != RecallLaneUnavailable || result.Coverage.Reason != "" ||
		result.Coverage.ProjectionHighWater >= result.Coverage.SourceHighWater {
		t.Fatalf("optional accelerator outage or projection lag changed complete raw-primary coverage: %+v", result.Coverage)
	}
}

func TestBrainRetrievalLabelsSourceFailureAndInventoryGapPartial(t *testing.T) {
	temporal := testBrainTemporal(t)
	bodies := map[string]string{}
	sources := make([]BrainSourceMetadata, 2)
	for index := range sources {
		body := fmt.Sprintf("source body %d", index)
		sources[index] = testBrainMetadata(index, body, temporal)
		bodies[sources[index].Evidence.ObjectID] = body
	}

	t.Run("source failure", func(t *testing.T) {
		planner, _, reader, principal, _ := newTestBrainPlanner(t, temporal, sources, bodies, 2, nil)
		reader.errors[sources[1].Evidence.ObjectID] = errors.New("raw source unavailable")
		result, err := planner.Resolve(context.Background(), BrainRetrievalRequest{Principal: principal, Query: "body", Temporal: temporal})
		if err != nil {
			t.Fatal(err)
		}
		if result.Coverage.Status != RecallCoveragePartial || result.Coverage.FailedSources != 1 || result.Coverage.Lanes.Raw != RecallLaneDegraded ||
			result.PromptPlan.InventorySourceCount != 2 || result.PromptPlan.ReadableSourceCount != 1 || result.PromptPlan.ClassifiedWithoutBody != 1 {
			t.Fatalf("failed source coverage/plan=%+v / %+v", result.Coverage, result.PromptPlan)
		}
	})

	t.Run("range gap", func(t *testing.T) {
		planner, inventory, _, principal, _ := newTestBrainPlanner(t, temporal, sources, bodies, 2, nil)
		page := inventory.pages[""]
		page.ResolvedEndUTC = temporal.EndUTC.Add(-24 * time.Hour)
		inventory.pages[""] = page
		result, err := planner.Resolve(context.Background(), BrainRetrievalRequest{Principal: principal, Query: "body", Temporal: temporal})
		if err != nil {
			t.Fatal(err)
		}
		if result.Coverage.Status != RecallCoveragePartial || !strings.Contains(result.Coverage.Reason, "inventory gap") {
			t.Fatalf("gap coverage=%+v", result.Coverage)
		}
	})
}

func TestBrainRetrievalRestartProducesIdenticalSnapshotCoverageAndPromptPlan(t *testing.T) {
	temporal := testBrainTemporal(t)
	bodies := map[string]string{}
	sources := make([]BrainSourceMetadata, 4)
	for index := range sources {
		body := strings.Repeat(fmt.Sprintf("stable-%d ", index), 18)
		sources[index] = testBrainMetadata(index, body, temporal)
		bodies[sources[index].Evidence.ObjectID] = body
	}
	firstPlanner, _, _, principal, _ := newTestBrainPlanner(t, temporal, sources, bodies, 2, nil)
	secondPlanner, _, _, _, _ := newTestBrainPlanner(t, temporal, sources, bodies, 2, nil)
	request := BrainRetrievalRequest{Principal: principal, Query: "stable", Temporal: temporal}
	first, err := firstPlanner.Resolve(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := secondPlanner.Resolve(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if first.Snapshot.SnapshotID != second.Snapshot.SnapshotID || first.Coverage.Digest != second.Coverage.Digest ||
		!reflect.DeepEqual(first.Snapshot, second.Snapshot) || !reflect.DeepEqual(first.PromptPlan, second.PromptPlan) || !reflect.DeepEqual(first.LexicalMatches, second.LexicalMatches) {
		t.Fatalf("restart was nondeterministic\nfirst=%+v\nsecond=%+v", first, second)
	}
}

func TestBrainRetrievalDeniesGuestAndCapabilityBeforeInventory(t *testing.T) {
	temporal := testBrainTemporal(t)
	body := "must remain durable-member-only"
	source := testBrainMetadata(0, body, temporal)
	planner, inventory, _, principal, _ := newTestBrainPlanner(t, temporal, []BrainSourceMetadata{source}, map[string]string{source.Evidence.ObjectID: body}, 1, nil)
	for _, kind := range []ACLPrincipalKind{ACLPrincipalGuest, ACLPrincipalCapability} {
		denied := principal
		denied.Kind = kind
		denied.RoomID, denied.SittingID = source.Evidence.RoomID, source.Evidence.SittingID
		_, err := planner.Resolve(context.Background(), BrainRetrievalRequest{Principal: denied, Query: "durable", Temporal: temporal})
		if !errors.Is(err, ErrBrainRetrievalInvalid) {
			t.Fatalf("kind=%s error=%v", kind, err)
		}
	}
	if inventory.calls != 0 {
		t.Fatalf("denied principal reached inventory %d time(s)", inventory.calls)
	}
}

func TestBrainRetrievalHashesReturnedBodyBytesLocally(t *testing.T) {
	temporal := testBrainTemporal(t)
	source := testBrainMetadata(0, "authorized body", temporal)
	planner, _, _, principal, _ := newTestBrainPlanner(t, temporal, []BrainSourceMetadata{source}, map[string]string{source.Evidence.ObjectID: "tampered body"}, 1, nil)
	_, err := planner.Resolve(context.Background(), BrainRetrievalRequest{Principal: principal, Query: "body", Temporal: temporal})
	if !errors.Is(err, ErrBrainRetrievalRetry) {
		t.Fatalf("reader-controlled digest admitted altered bytes: %v", err)
	}
}

func TestBrainRetrievalClipsBoundaryCrossingBodyByAuthoritativeByteSegments(t *testing.T) {
	start := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	temporal, err := NewBoundedTemporalQuery(TemporalExplicitRange, start, start.Add(10*time.Minute), "UTC", "room-a", "sitting-a", "bounded segment test")
	if err != nil {
		t.Fatal(err)
	}
	body := "BEFORE|INSIDE|AFTER"
	source := testBrainMetadata(0, body, temporal)
	source.Evidence.RoomID, source.Evidence.SittingID = "room-a", "sitting-a"
	source.Evidence.OccurredStart = start.Add(-time.Minute)
	source.Evidence.OccurredEnd = temporal.EndUTC.Add(time.Minute)
	source.Segments = []BrainSourceSegmentMetadata{
		{OccurredStart: start.Add(-time.Minute), OccurredEnd: start, ByteStart: 0, ByteEnd: 7},
		{OccurredStart: start, OccurredEnd: temporal.EndUTC, ByteStart: 7, ByteEnd: 13},
		{OccurredStart: temporal.EndUTC, OccurredEnd: temporal.EndUTC.Add(time.Minute), ByteStart: 13, ByteEnd: len(body)},
	}
	planner, _, _, principal, _ := newTestBrainPlanner(t, temporal, []BrainSourceMetadata{source}, map[string]string{source.Evidence.ObjectID: body}, 1, nil)
	result, err := planner.Resolve(context.Background(), BrainRetrievalRequest{Principal: principal, Query: "inside", Temporal: temporal})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Sources) != 1 || result.Sources[0].Body != "INSIDE" || result.Sources[0].Status != RecallSourcePartial || result.Coverage.Status != RecallCoveragePartial {
		t.Fatalf("boundary clip result=%+v", result)
	}
	rendered := fmt.Sprintf("%+v", result.PromptPlan)
	if strings.Contains(rendered, "BEFORE") || strings.Contains(rendered, "AFTER") {
		t.Fatalf("out-of-window bytes entered prompt: %s", rendered)
	}
}

func TestBrainRetrievalRequiresTerminalCountAndManifestProof(t *testing.T) {
	temporal := testBrainTemporal(t)
	body := "provable exhaustive inventory"
	source := testBrainMetadata(0, body, temporal)
	for _, test := range []struct {
		name   string
		mutate func(*BrainSourceInventoryPage)
	}{
		{name: "terminal", mutate: func(page *BrainSourceInventoryPage) { page.Terminal = false }},
		{name: "count", mutate: func(page *BrainSourceInventoryPage) { page.ExpectedSourceCount++ }},
		{name: "manifest", mutate: func(page *BrainSourceInventoryPage) { page.InventoryManifest = digestBrainString("truncated") }},
	} {
		t.Run(test.name, func(t *testing.T) {
			planner, inventory, _, principal, _ := newTestBrainPlanner(t, temporal, []BrainSourceMetadata{source}, map[string]string{source.Evidence.ObjectID: body}, 1, nil)
			page := inventory.pages[""]
			test.mutate(&page)
			inventory.pages[""] = page
			if _, err := planner.Resolve(context.Background(), BrainRetrievalRequest{Principal: principal, Query: "inventory", Temporal: temporal}); err == nil {
				t.Fatal("incomplete inventory proof was accepted")
			}
		})
	}
}

func TestBrainRetrievalBeforeAdmissionCoverageExposesSettleContinuityAndLateArrival(t *testing.T) {
	admittedAt := time.Date(2026, 7, 1, 12, 10, 0, 0, time.UTC)
	anchor := normalizeAdmissionAnchor(admissionAnchorForTest(admittedAt, "sitting-a", memberAdmissionPrincipal("member@example.com"), 5))
	anchor.RoomID = "room-a"
	anchor.CaptureWatermark = admittedAt.Add(-10 * time.Second)
	anchor.AnchorID = deterministicAdmissionAnchorID(anchor)
	temporal, err := NewBeforeAdmissionTemporalQuery(anchor, admittedAt.Add(-10*time.Minute), "UTC", 30*time.Second, "pre-admission recap")
	if err != nil {
		t.Fatal(err)
	}
	body := "pre admission source"
	base := testBrainMetadata(0, body, temporal)
	base.Evidence.RoomID, base.Evidence.SittingID = "room-a", "sitting-a"
	base.Evidence.OccurredStart, base.Evidence.OccurredEnd = admittedAt.Add(-time.Minute), admittedAt.Add(-30*time.Second)
	base.CaptureSequence, base.CapturedAt = 4, admittedAt.Add(-20*time.Second)

	t.Run("unsettled", func(t *testing.T) {
		planner, inventory, _, principal, _ := newTestBrainPlanner(t, temporal, []BrainSourceMetadata{base}, map[string]string{base.Evidence.ObjectID: body}, 1, nil)
		page := inventory.pages[""]
		page.SnapshotAt = temporal.SettleUntil.Add(-time.Nanosecond)
		page.CaptureCompleteThrough = 5
		inventory.pages[""] = page
		result, err := planner.Resolve(context.Background(), BrainRetrievalRequest{Principal: principal, Query: "pre admission", Temporal: temporal})
		if err != nil {
			t.Fatal(err)
		}
		if result.Coverage.Status != RecallCoveragePartial || result.Coverage.Settled || !strings.Contains(result.Coverage.Reason, "settling") {
			t.Fatalf("unsettled coverage=%+v", result.Coverage)
		}
	})

	t.Run("capture gap", func(t *testing.T) {
		planner, inventory, _, principal, _ := newTestBrainPlanner(t, temporal, []BrainSourceMetadata{base}, map[string]string{base.Evidence.ObjectID: body}, 1, nil)
		page := inventory.pages[""]
		page.CaptureCompleteThrough = 4
		inventory.pages[""] = page
		result, err := planner.Resolve(context.Background(), BrainRetrievalRequest{Principal: principal, Query: "pre admission", Temporal: temporal})
		if err != nil {
			t.Fatal(err)
		}
		if result.Coverage.Status != RecallCoveragePartial || !strings.Contains(result.Coverage.Reason, "continuity") {
			t.Fatalf("capture-gap coverage=%+v", result.Coverage)
		}
	})

	t.Run("late arrival", func(t *testing.T) {
		late := base
		late.CapturedAt = admittedAt.Add(-5 * time.Second)
		planner, inventory, _, principal, _ := newTestBrainPlanner(t, temporal, []BrainSourceMetadata{late}, map[string]string{late.Evidence.ObjectID: body}, 1, nil)
		page := inventory.pages[""]
		page.CaptureCompleteThrough = 5
		inventory.pages[""] = page
		result, err := planner.Resolve(context.Background(), BrainRetrievalRequest{Principal: principal, Query: "pre admission", Temporal: temporal})
		if err != nil {
			t.Fatal(err)
		}
		if result.Coverage.Status != RecallCoveragePartial || result.Coverage.LateArrivalSources != 1 || !strings.Contains(result.Coverage.Reason, "late-arriving") {
			t.Fatalf("late-arrival coverage=%+v", result.Coverage)
		}
	})

	t.Run("denied late arrival is nonleaking", func(t *testing.T) {
		lateDenied := testBrainMetadata(1, "private late source", temporal)
		lateDenied.Evidence.RoomID, lateDenied.Evidence.SittingID = "room-a", "sitting-a"
		lateDenied.Evidence.OccurredStart, lateDenied.Evidence.OccurredEnd = admittedAt.Add(-2*time.Minute), admittedAt.Add(-90*time.Second)
		lateDenied.CaptureSequence, lateDenied.CapturedAt = 3, admittedAt.Add(-5*time.Second)
		sources := []BrainSourceMetadata{base, lateDenied}
		bodies := map[string]string{base.Evidence.ObjectID: body, lateDenied.Evidence.ObjectID: "private late source"}
		allowed := map[string]bool{base.Evidence.ObjectID: true}
		planner, inventory, _, principal, _ := newTestBrainPlanner(t, temporal, sources, bodies, 2, allowed)
		page := inventory.pages[""]
		page.CaptureCompleteThrough = 5
		inventory.pages[""] = page
		result, err := planner.Resolve(context.Background(), BrainRetrievalRequest{Principal: principal, Query: "pre admission", Temporal: temporal})
		if err != nil {
			t.Fatal(err)
		}
		if result.Coverage.Status != RecallCoverageComplete || result.Coverage.LateArrivalSources != 0 || len(result.Sources) != 1 || strings.Contains(result.Coverage.Reason, "late") {
			t.Fatalf("denied late source changed authorized result: %+v", result)
		}
	})
}

func TestBrainRetrievalServicePrincipalUsesExplicitACL(t *testing.T) {
	temporal := testBrainTemporal(t)
	body := "service scoped source"
	source := testBrainMetadata(0, body, temporal)
	planner, _, _, _, _ := newTestBrainPlanner(t, temporal, []BrainSourceMetadata{source}, map[string]string{source.Evidence.ObjectID: body}, 1, nil)
	service := ACLPrincipal{TenantID: "tenant-a", ID: "service-insights", Kind: ACLPrincipalService}
	planner.Kernel.Store = testBrainACL([]BrainSourceMetadata{source}, service, nil)
	result, err := planner.Resolve(context.Background(), BrainRetrievalRequest{Principal: service, Query: "service", Temporal: temporal})
	if err != nil || len(result.Sources) != 1 {
		t.Fatalf("explicitly authorized service retrieval result=%+v err=%v", result, err)
	}
}
