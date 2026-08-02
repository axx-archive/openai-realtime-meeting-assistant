package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/openai/openai-realtime-meeting-assistant/internal/e9readiness"
)

type e9LocalReplicaPayload struct {
	ReplicaID       string `json:"replicaId"`
	RuntimeState    string `json:"runtimeState"`
	Restored        bool   `json:"restored"`
	Generation      uint64 `json:"generation"`
	PurgeGeneration int64  `json:"purgeGeneration"`
	DecisionCount   int    `json:"decisionCount"`
}

type e9LocalRouteTarget struct {
	ID  string
	URL string
}

type e9LocalRouter struct {
	mu                  sync.Mutex
	targets             []e9LocalRouteTarget
	activeID            string
	statePath           string
	client              *http.Client
	deadPrimaryObserved bool
}

type e9LocalRouteState struct {
	ActiveReplica string `json:"activeReplica"`
}

func TestE9LocalAppControlPersistenceFailoverRestore(t *testing.T) {
	hermeticChild := strings.EqualFold(strings.TrimSpace(os.Getenv(e9readiness.LocalIntegrationHermeticEnv)), "true")
	root := strings.TrimSpace(os.Getenv("E9_LOCAL_INTEGRATION_ROOT"))
	if root == "" {
		root = t.TempDir()
	} else {
		root = filepath.Clean(root)
		if !hermeticChild && !e9PathWithin(root, os.TempDir()) {
			t.Fatalf("E9 local integration root must remain under the OS temp directory: %s", root)
		}
		if err := os.MkdirAll(root, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	receiptPath := strings.TrimSpace(os.Getenv("E9_LOCAL_INTEGRATION_RECEIPT"))
	if hermeticChild {
		if err := e9readiness.ValidateLocalIntegrationChildEnvironment(os.Environ(), root, receiptPath); err != nil {
			t.Fatalf("E9 child environment was not hermetic: %v", err)
		}
	} else {
		// A directly-invoked test still installs the same temp-only application
		// contract, while only cmd/e9-readiness may claim the inherited child
		// boundary was checked before the application booted.
		localEnvironment, err := e9readiness.LocalIntegrationApplicationEnvironment(root, filepath.Join(root, "direct-receipt.json"))
		if err != nil {
			t.Fatal(err)
		}
		delete(localEnvironment, "E9_LOCAL_INTEGRATION_ROOT")
		delete(localEnvironment, "E9_LOCAL_INTEGRATION_RECEIPT")
		delete(localEnvironment, e9readiness.LocalIntegrationHermeticEnv)
		for key, value := range localEnvironment {
			t.Setenv(key, value)
		}
	}
	dataDir := filepath.Join(root, "app-data")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	authorityKey := []byte("0123456789abcdef0123456789abcdef")

	var primary, secondary, restored *kanbanBoardApp
	var primaryServer, secondaryServer, restoredServer, controlServer, restoredControlServer, mediaServer *httptest.Server
	t.Cleanup(func() {
		for _, server := range []*httptest.Server{primaryServer, secondaryServer, restoredServer, controlServer, restoredControlServer, mediaServer} {
			if server != nil {
				server.Close()
			}
		}
		for _, app := range []*kanbanBoardApp{primary, secondary, restored} {
			if app != nil {
				_ = app.Close()
			}
		}
	})

	primary = newKanbanBoardApp()
	if primary.strideRuntime == nil {
		t.Fatal("primary app did not install the STRIDE runtime")
	}
	meetingStart := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	temporalConfig := TemporalMeetingBrainConfig{
		TenantID: "tenant-1", RoomID: "room-1", SittingID: "sitting-1",
		SittingStart: meetingStart, SittingEnd: meetingStart.Add(time.Hour),
	}
	transcript := temporalTestTranscript("segment-e9", "revision-e9", "erase this local failover canary", "authoritative_final", "", 1, 1,
		meetingStart, meetingStart.Add(time.Minute), meetingStart.Add(time.Minute), []string{"founder"}, []string{"resilience"})
	analysis := temporalTestAnalysis("decision-e9", "decision", "local failover decision canary", 2, 1, transcript,
		meetingStart, meetingStart.Add(time.Minute), meetingStart.Add(2*time.Minute), []string{"founder"}, []string{"resilience"})
	for _, event := range []TemporalMeetingEvent{transcript, analysis} {
		if err := primary.strideRuntime.ApplyTemporalEvidence("tenant-1", temporalConfig, event); err != nil {
			t.Fatalf("apply initial temporal evidence: %v", err)
		}
	}
	if err := primary.strideRuntime.Save(); err != nil {
		t.Fatalf("persist primary runtime: %v", err)
	}
	initialState := e9ReadTemporalState(t, primary, temporalConfig)
	initialHealth := primary.strideRuntime.Health()
	if initialState.PurgeGeneration != 0 || len(initialState.Decisions) != 1 || initialHealth.Generation == 0 {
		t.Fatalf("initial durable state=%+v health=%+v", initialState, initialHealth)
	}

	snapshotPath := filepath.Join(dataDir, defaultSTRIDERuntimeSnapshot)
	generationPath := filepath.Join(dataDir, defaultSTRIDERuntimeGeneration)
	prePurgeSnapshot := e9MustRead(t, snapshotPath)
	prePurgeGeneration := e9MustRead(t, generationPath)

	primaryServer = e9StartLocalReplica(t, "app-a", primary, temporalConfig)
	e9RequireLoopback(t, primaryServer.URL)
	primaryURL := primaryServer.URL
	routeStatePath := filepath.Join(root, "control", "active-route.json")
	router := newE9LocalRouter(routeStatePath, []e9LocalRouteTarget{{ID: "app-a", URL: primaryURL}})
	controlServer = httptest.NewServer(router)
	e9RequireLoopback(t, controlServer.URL)
	initialRoute, initialStatus := e9RequestReplica(t, controlServer.URL)
	if initialStatus != http.StatusOK || initialRoute.ReplicaID != "app-a" {
		t.Fatalf("initial route status=%d payload=%+v", initialStatus, initialRoute)
	}

	roomAGeneration := primary.ensureRoomMedia("room-1")
	roomBGeneration := primary.ensureRoomMedia("room-2")
	if roomAGeneration == 0 || roomBGeneration == 0 || !primary.roomMediaGenerationCurrent("room-1", roomAGeneration) || !primary.roomMediaGenerationCurrent("room-2", roomBGeneration) {
		t.Fatalf("app room-media generations were not current: room-1=%d room-2=%d", roomAGeneration, roomBGeneration)
	}
	mediaServer, roomAToken, roomBToken := e9StartRoomScopeMediaProbe(t, "room-1", roomAGeneration, "room-2", roomBGeneration)
	e9RequireLoopback(t, mediaServer.URL)
	sameRoomStatus := e9MediaProbeStatus(t, mediaServer.URL, "room-1", roomAToken)
	crossRoomStatus := e9MediaProbeStatus(t, mediaServer.URL, "room-2", roomAToken)
	if sameRoomStatus != http.StatusOK || crossRoomStatus != http.StatusForbidden || roomAToken == roomBToken {
		t.Fatalf("media room-scope probe same=%d cross=%d tokensDistinct=%t", sameRoomStatus, crossRoomStatus, roomAToken != roomBToken)
	}

	appFailoverStart := time.Now()
	primaryServer.Close()
	primaryServer = nil
	if err := primary.Close(); err != nil {
		t.Fatalf("close primary app: %v", err)
	}
	primary = nil
	mediaDuringLossStart := time.Now()
	mediaDuringLossStatus := e9MediaProbeStatus(t, mediaServer.URL, "room-1", roomAToken)
	mediaDuringLossElapsed := e9MeasuredElapsed(mediaDuringLossStart)
	if mediaDuringLossStatus != http.StatusOK {
		t.Fatalf("room-scoped media control probe failed during app loss: %d", mediaDuringLossStatus)
	}

	secondary = newKanbanBoardApp()
	secondaryState := e9ReadTemporalState(t, secondary, temporalConfig)
	secondaryHealth := secondary.strideRuntime.Health()
	if !secondaryHealth.Restored || secondaryHealth.Generation < initialHealth.Generation || len(secondaryState.Decisions) != 1 {
		t.Fatalf("secondary restore state=%+v health=%+v initial=%+v", secondaryState, secondaryHealth, initialHealth)
	}
	secondaryServer = e9StartLocalReplica(t, "app-b", secondary, temporalConfig)
	secondaryURL := secondaryServer.URL
	router.setTargets([]e9LocalRouteTarget{{ID: "app-a", URL: primaryURL}, {ID: "app-b", URL: secondaryURL}})
	failoverRoute, failoverStatus := e9RequestReplica(t, controlServer.URL)
	appFailoverElapsed := e9MeasuredElapsed(appFailoverStart)
	if failoverStatus != http.StatusOK || failoverRoute.ReplicaID != "app-b" || !router.observedDeadPrimary() {
		t.Fatalf("failover route status=%d payload=%+v deadPrimary=%t", failoverStatus, failoverRoute, router.observedDeadPrimary())
	}

	controlRestoreStart := time.Now()
	controlServer.Close()
	controlServer = nil
	restoredRouter := newE9LocalRouter(routeStatePath, []e9LocalRouteTarget{{ID: "app-b", URL: secondaryURL}})
	restoredControlServer = httptest.NewServer(restoredRouter)
	restoredControlRoute, restoredControlStatus := e9RequestReplica(t, restoredControlServer.URL)
	controlRestoreElapsed := e9MeasuredElapsed(controlRestoreStart)
	if restoredControlStatus != http.StatusOK || restoredControlRoute.ReplicaID != "app-b" || restoredRouter.activeReplica() != "app-b" {
		t.Fatalf("restored control route status=%d payload=%+v active=%s", restoredControlStatus, restoredControlRoute, restoredRouter.activeReplica())
	}

	purgeStart := time.Now()
	purge := TemporalMeetingEvent{Sequence: 3, Kind: TemporalMeetingEventPurge, Purge: &TemporalPurgeEvent{
		TenantID: "tenant-1", SegmentID: "segment-e9", RevisionID: "revision-e9", PurgeGeneration: 1,
	}}
	if err := secondary.strideRuntime.ApplyTemporalEvidence("tenant-1", temporalConfig, purge); err != nil {
		t.Fatalf("apply temporal purge: %v", err)
	}
	if err := secondary.strideRuntime.Save(); err != nil {
		t.Fatalf("persist temporal purge: %v", err)
	}
	purgePersistElapsed := e9MeasuredElapsed(purgeStart)
	purgedState := e9ReadTemporalState(t, secondary, temporalConfig)
	purgedHealth := secondary.strideRuntime.Health()
	if purgedState.PurgeGeneration != 1 || len(purgedState.Decisions) != 0 || purgedHealth.Generation <= secondaryHealth.Generation {
		t.Fatalf("purged state=%+v health=%+v before=%+v", purgedState, purgedHealth, secondaryHealth)
	}

	currentSnapshot := e9MustRead(t, snapshotPath)
	currentGeneration := e9MustRead(t, generationPath)
	rollbackRoot := filepath.Join(root, "stale-rollback")
	if err := os.MkdirAll(rollbackRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	rollbackSnapshotPath := filepath.Join(rollbackRoot, "runtime.snapshot.json")
	rollbackGenerationPath := filepath.Join(rollbackRoot, "runtime.generation.json")
	e9MustWrite(t, rollbackSnapshotPath, prePurgeSnapshot)
	e9MustWrite(t, rollbackGenerationPath, currentGeneration)
	rollbackStart := time.Now()
	rollbackRuntime, rollbackErr := NewSTRIDERuntime(STRIDERuntimeConfig{
		Enabled: true, TenantID: "tenant-1", SnapshotPath: rollbackSnapshotPath, GenerationPath: rollbackGenerationPath,
		Authority: STRIDESnapshotMACAuthority{KeyID: "e9_local_failover_key", Key: authorityKey}, MinimumGeneration: 91,
		RecallThreadIDs: []string{"team"}, BootstrapEmpty: false,
	})
	staleRollbackElapsed := e9MeasuredElapsed(rollbackStart)
	if rollbackRuntime != nil {
		_ = rollbackRuntime.Close()
	}
	if rollbackErr == nil || (!errors.Is(rollbackErr, ErrSTRIDERuntimeGeneration) && !errors.Is(rollbackErr, ErrSTRIDERuntimeSnapshot)) {
		t.Fatalf("stale pre-purge snapshot was not refused: %v", rollbackErr)
	}
	if bytes.Equal(prePurgeSnapshot, currentSnapshot) || bytes.Equal(prePurgeGeneration, currentGeneration) {
		t.Fatal("purge persistence did not advance both signed snapshot and generation ledger")
	}

	restoreStart := time.Now()
	secondaryServer.Close()
	secondaryServer = nil
	if err := secondary.Close(); err != nil {
		t.Fatalf("close purged secondary: %v", err)
	}
	secondary = nil
	restored = newKanbanBoardApp()
	restoredState := e9ReadTemporalState(t, restored, temporalConfig)
	restoredHealth := restored.strideRuntime.Health()
	restoredServer = e9StartLocalReplica(t, "app-c", restored, temporalConfig)
	restoredRouter.setTargets([]e9LocalRouteTarget{{ID: "app-b", URL: secondaryURL}, {ID: "app-c", URL: restoredServer.URL}})
	persistenceRoute, persistenceStatus := e9RequestReplica(t, restoredControlServer.URL)
	signedRestoreElapsed := e9MeasuredElapsed(restoreStart)
	if !restoredHealth.Restored || restoredHealth.Generation < purgedHealth.Generation || restoredState.PurgeGeneration != 1 || len(restoredState.Decisions) != 0 ||
		persistenceStatus != http.StatusOK || persistenceRoute.ReplicaID != "app-c" {
		t.Fatalf("current signed restore health=%+v state=%+v route=%+v status=%d", restoredHealth, restoredState, persistenceRoute, persistenceStatus)
	}

	receipt := e9readiness.LocalIntegrationReceipt{
		SchemaVersion: e9readiness.LocalIntegrationReceiptSchema, EvidenceClass: "local_deterministic_integration", State: "passed",
		TempResourcesOnly: true, NetworkScope: "loopback_only", ProviderCalls: false, ProductionMutation: false, DockerMutation: false,
		ExecutedSystems: []string{
			"meetingassist.kanban_app", "meetingassist.stride_runtime", "meetingassist.temporal_purge",
			"meetingassist.signed_snapshot_restore", "loopback.http_replica_router", "loopback.room_scope_media_probe",
		},
		Routing: e9readiness.LocalRoutingObservation{
			InitialReplica: initialRoute.ReplicaID, FailoverReplica: failoverRoute.ReplicaID,
			RestoredControlReplica: restoredControlRoute.ReplicaID, PersistenceReplica: persistenceRoute.ReplicaID,
			InitialStatus: initialStatus, FailoverStatus: failoverStatus, RestoredControlStatus: restoredControlStatus,
			PersistenceStatus: persistenceStatus, DeadPrimaryObserved: router.observedDeadPrimary(),
			SelectedRoutePersisted: e9ReadRouteState(t, routeStatePath).ActiveReplica == "app-c",
		},
		MediaIsolation: e9readiness.LocalMediaIsolationObservation{
			EvidenceClass: "room_scope_control_probe", RoomA: "room-1", RoomB: "room-2",
			SameRoomStatus: sameRoomStatus, CrossRoomStatus: crossRoomStatus, DuringAppLossStatus: mediaDuringLossStatus,
			RoomLaneDistinct: roomAToken != roomBToken,
		},
		Persistence: e9readiness.LocalPersistenceObservation{
			InitialGeneration: initialHealth.Generation, FailoverGeneration: secondaryHealth.Generation,
			PurgedGeneration: purgedHealth.Generation, RestoredGeneration: restoredHealth.Generation,
			InitialDecisionCount: len(initialState.Decisions), FailoverDecisionCount: len(secondaryState.Decisions),
			PurgedDecisionCount: len(purgedState.Decisions), RestoredDecisionCount: len(restoredState.Decisions),
			PurgeGeneration: restoredState.PurgeGeneration, PurgePersisted: restoredState.PurgeGeneration == 1 && len(restoredState.Decisions) == 0,
			StaleRollbackRefused: rollbackErr != nil, CurrentRestoreAccepted: restoredHealth.Restored,
		},
		Timings: []e9readiness.MeasuredTiming{
			{Operation: "app_failover", ElapsedNanoseconds: appFailoverElapsed},
			{Operation: "media_during_app_loss", ElapsedNanoseconds: mediaDuringLossElapsed},
			{Operation: "control_restore", ElapsedNanoseconds: controlRestoreElapsed},
			{Operation: "purge_persist", ElapsedNanoseconds: purgePersistElapsed},
			{Operation: "signed_state_restore", ElapsedNanoseconds: signedRestoreElapsed},
			{Operation: "stale_rollback_refusal", ElapsedNanoseconds: staleRollbackElapsed},
		},
		ClaimsExcluded: []string{
			"paid provider quality or compatibility",
			"WebRTC, TURN, RTP, media-device, or physical-device continuity",
			"production HA, traffic shift, restore, RPO, RTO, or soak",
			"release, deployment, or live-data qualification",
		},
	}
	e9readiness.SortLocalIntegrationReceipt(&receipt)
	if err := e9readiness.ValidateLocalIntegrationReceipt(receipt); err != nil {
		t.Fatalf("invalid local integration receipt: %v", err)
	}
	if receiptPath != "" {
		if !e9PathWithin(receiptPath, root) {
			t.Fatalf("receipt path escaped integration root: %s", receiptPath)
		}
		if err := writeJSONFileAtomically(receiptPath, "E9 local integration receipt", receipt); err != nil {
			t.Fatalf("write local integration receipt: %v", err)
		}
	}
	t.Logf("E9_LOCAL_INTEGRATION_RECEIPT=%+v", receipt)
}

func e9StartLocalReplica(t *testing.T, replicaID string, app *kanbanBoardApp, config TemporalMeetingBrainConfig) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/e9/session" || r.URL.Query().Get("room") != config.RoomID {
			http.NotFound(w, r)
			return
		}
		state, err := e9ReadTemporalStateFromApp(app, config)
		if err != nil {
			http.Error(w, "runtime unavailable", http.StatusServiceUnavailable)
			return
		}
		health := app.strideRuntime.Health()
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(e9LocalReplicaPayload{
			ReplicaID: replicaID, RuntimeState: string(health.State), Restored: health.Restored, Generation: health.Generation,
			PurgeGeneration: state.PurgeGeneration, DecisionCount: len(state.Decisions),
		})
	}))
	return server
}

func newE9LocalRouter(statePath string, targets []e9LocalRouteTarget) *e9LocalRouter {
	router := &e9LocalRouter{statePath: statePath, client: &http.Client{Timeout: time.Second}}
	router.setTargets(targets)
	if raw, err := os.ReadFile(statePath); err == nil {
		var state e9LocalRouteState
		if json.Unmarshal(raw, &state) == nil {
			router.activeID = strings.TrimSpace(state.ActiveReplica)
		}
	}
	return router
}

func (router *e9LocalRouter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet || r.URL.Path != "/e9/session" {
		http.NotFound(w, r)
		return
	}
	router.mu.Lock()
	targets := append([]e9LocalRouteTarget(nil), router.targets...)
	activeID := router.activeID
	router.mu.Unlock()
	sort.SliceStable(targets, func(i, j int) bool { return targets[i].ID == activeID && targets[j].ID != activeID })

	for index, target := range targets {
		request, err := http.NewRequestWithContext(r.Context(), http.MethodGet, target.URL+"/e9/session?room="+url.QueryEscape(r.URL.Query().Get("room")), nil)
		if err != nil {
			continue
		}
		response, err := router.client.Do(request)
		if err != nil {
			if index == 0 && activeID != "" {
				router.mu.Lock()
				router.deadPrimaryObserved = true
				router.mu.Unlock()
			}
			continue
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		_ = response.Body.Close()
		if readErr != nil || response.StatusCode != http.StatusOK {
			continue
		}
		router.mu.Lock()
		router.activeID = target.ID
		persistErr := router.persistLocked()
		router.mu.Unlock()
		if persistErr != nil {
			http.Error(w, "control state unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(response.StatusCode)
		_, _ = w.Write(body)
		return
	}
	http.Error(w, "no healthy replica", http.StatusServiceUnavailable)
}

func (router *e9LocalRouter) setTargets(targets []e9LocalRouteTarget) {
	router.mu.Lock()
	defer router.mu.Unlock()
	router.targets = append([]e9LocalRouteTarget(nil), targets...)
	if router.activeID == "" && len(targets) > 0 {
		router.activeID = targets[0].ID
	}
}

func (router *e9LocalRouter) persistLocked() error {
	if err := os.MkdirAll(filepath.Dir(router.statePath), 0o700); err != nil {
		return err
	}
	raw, err := json.Marshal(e9LocalRouteState{ActiveReplica: router.activeID})
	if err != nil {
		return err
	}
	temp := router.statePath + ".tmp"
	if err := os.WriteFile(temp, append(raw, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(temp, router.statePath)
}

func (router *e9LocalRouter) observedDeadPrimary() bool {
	router.mu.Lock()
	defer router.mu.Unlock()
	return router.deadPrimaryObserved
}

func (router *e9LocalRouter) activeReplica() string {
	router.mu.Lock()
	defer router.mu.Unlock()
	return router.activeID
}

func e9StartRoomScopeMediaProbe(t *testing.T, roomA string, generationA uint64, roomB string, generationB uint64) (*httptest.Server, string, string) {
	t.Helper()
	roomAToken := temporalDigest(fmt.Sprintf("%s\x00%d", roomA, generationA))
	roomBToken := temporalDigest(fmt.Sprintf("%s\x00%d", roomB, generationB))
	lanes := map[string]string{roomA: roomAToken, roomB: roomBToken}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/media/control" {
			http.NotFound(w, r)
			return
		}
		room := r.URL.Query().Get("room")
		want, found := lanes[room]
		if !found {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("X-E9-Room-Lane") != want {
			http.Error(w, "room lane denied", http.StatusForbidden)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
	}))
	return server, roomAToken, roomBToken
}

func e9RequestReplica(t *testing.T, baseURL string) (e9LocalReplicaPayload, int) {
	t.Helper()
	response, err := http.Get(baseURL + "/e9/session?room=room-1") // #nosec G107 -- test-only loopback URL.
	if err != nil {
		t.Fatalf("request local control route: %v", err)
	}
	defer response.Body.Close()
	var payload e9LocalReplicaPayload
	if response.StatusCode == http.StatusOK {
		if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&payload); err != nil {
			t.Fatalf("decode local replica response: %v", err)
		}
	}
	return payload, response.StatusCode
}

func e9MediaProbeStatus(t *testing.T, baseURL, room, token string) int {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, baseURL+"/media/control?room="+url.QueryEscape(room), nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-E9-Room-Lane", token)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("request local media-scope probe: %v", err)
	}
	_ = response.Body.Close()
	return response.StatusCode
}

func e9ReadTemporalState(t *testing.T, app *kanbanBoardApp, config TemporalMeetingBrainConfig) TemporalCurrentMeetingState {
	t.Helper()
	state, err := e9ReadTemporalStateFromApp(app, config)
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func e9ReadTemporalStateFromApp(app *kanbanBoardApp, config TemporalMeetingBrainConfig) (TemporalCurrentMeetingState, error) {
	if app == nil || app.strideRuntime == nil {
		return TemporalCurrentMeetingState{}, ErrSTRIDERuntimeUnavailable
	}
	var state TemporalCurrentMeetingState
	err := app.strideRuntime.ReadTemporalMeetingBrain(config.TenantID, config.RoomID, config.SittingID, func(brain *TemporalMeetingBrain) error {
		state = brain.CurrentState()
		return nil
	})
	return state, err
}

func e9RequireLoopback(t *testing.T, rawURL string) {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	host, _, err := net.SplitHostPort(parsed.Host)
	if err != nil || !net.ParseIP(host).IsLoopback() {
		t.Fatalf("integration server is not loopback-only: %s", rawURL)
	}
}

func e9ReadRouteState(t *testing.T, path string) e9LocalRouteState {
	t.Helper()
	raw := e9MustRead(t, path)
	var state e9LocalRouteState
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatal(err)
	}
	return state
}

func e9MustRead(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func e9MustWrite(t *testing.T, path string, raw []byte) {
	t.Helper()
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func e9PathWithin(path, root string) bool {
	path, pathErr := filepath.Abs(filepath.Clean(path))
	root, rootErr := filepath.Abs(filepath.Clean(root))
	if pathErr != nil || rootErr != nil {
		return false
	}
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func e9MeasuredElapsed(start time.Time) int64 {
	return time.Since(start).Nanoseconds()
}
