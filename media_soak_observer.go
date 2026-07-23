package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/pion/webrtc/v4"
)

const (
	mediaSoakRequestSchema  = "bonfire.w2a.runtime-observation-request.v1"
	mediaSoakResponseSchema = "bonfire.w2a.runtime-observation.v1"
	mediaSoakRequestTTL     = 30 * time.Second
	mediaSoakRunTTL         = 3 * time.Hour
)

// Set only by the release Docker build. An enabled observer refuses runtime
// attestation when the running binary was not compiled for the requested
// immutable commit/tree.
var mediaSoakBuildCommit, mediaSoakBuildTreeDigest, mediaSoakBuildConfigDigest, mediaSoakBuildInputsDigest, mediaSoakBuildSourceArchiveDigest string

var mediaSoakObserverKinds = map[string]bool{
	"head-of-line": true, "ai-failure": true, "canary-plant": true, "canary-observe": true, "canary-scrub": true,
	"runtime-attestation": true, "resources": true,
}

type mediaSoakObservationRequest struct {
	Schema        string    `json:"schema"`
	ReleaseCommit string    `json:"releaseCommit"`
	Nonce         string    `json:"nonce"`
	Purpose       string    `json:"purpose"`
	RequestID     string    `json:"requestId"`
	IssuedAt      time.Time `json:"issuedAt"`
	ExpiresAt     time.Time `json:"expiresAt"`
	Inputs        struct {
		RoomAID         string `json:"roomAId"`
		RoomBID         string `json:"roomBId"`
		FaultDurationMS int64  `json:"faultDurationMs,omitempty"`
	} `json:"inputs"`
}

type mediaSoakScope struct {
	RoomID                string `json:"roomId"`
	SittingID             string `json:"-"`
	Generation            uint64 `json:"-"`
	RoomDigest            string `json:"roomDigest"`
	SittingDigest         string `json:"sittingDigest"`
	MediaGenerationDigest string `json:"mediaGenerationDigest"`
	RecipientEmail        string `json:"-"`
}

type mediaSoakBinding struct {
	Nonce         string
	RoomA         mediaSoakScope
	RoomB         mediaSoakScope
	ExpiresAt     time.Time
	FaultDuration time.Duration
}

type mediaSoakRuntime interface {
	Bind(context.Context, string, string, string) (mediaSoakBinding, error)
	Observe(context.Context, string, mediaSoakBinding) (any, error)
}

type mediaSoakObserver struct {
	enabled       bool
	releaseCommit string
	token         string
	nonceDir      string
	proxyCIDRs    string
	now           func() time.Time
	runtime       mediaSoakRuntime
	mu            sync.Mutex
	bindings      map[string]mediaSoakBinding
	seen          map[string]time.Time
}

func newMediaSoakObserverFromEnv(app *kanbanBoardApp) *mediaSoakObserver {
	return &mediaSoakObserver{
		enabled:       strings.EqualFold(strings.TrimSpace(os.Getenv("BONFIRE_MEDIA_SOAK_OBSERVER_ENABLED")), "true"),
		releaseCommit: strings.TrimSpace(os.Getenv("BONFIRE_RELEASE_COMMIT")),
		token:         strings.TrimSpace(os.Getenv("BONFIRE_MEDIA_SOAK_OBSERVER_TOKEN")),
		nonceDir:      filepath.Join(filepath.Dir(meetingMemoryPath()), "media-soak-request-nonces"),
		proxyCIDRs:    strings.TrimSpace(os.Getenv("BONFIRE_MEDIA_SOAK_PROXY_CIDRS")),
		now:           time.Now, runtime: &liveMediaSoakRuntime{app: app, releaseCommit: strings.TrimSpace(os.Getenv("BONFIRE_RELEASE_COMMIT"))},
		bindings: map[string]mediaSoakBinding{}, seen: map[string]time.Time{},
	}
}

var liveMediaSoakObserver = newMediaSoakObserverFromEnv(nil)

func installLiveMediaSoakObserver(app *kanbanBoardApp) {
	liveMediaSoakObserver = newMediaSoakObserverFromEnv(app)
}

func mediaSoakObserverHandler(w http.ResponseWriter, r *http.Request) {
	liveMediaSoakObserver.ServeHTTP(w, r)
}

func (observer *mediaSoakObserver) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if observer == nil || !observer.enabled {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !observer.sourceAllowed(r.RemoteAddr) {
		writeAuthError(w, http.StatusForbidden, "media-soak source network rejected")
		return
	}
	kind := strings.TrimPrefix(r.URL.Path, "/internal/media-soak/")
	if !mediaSoakObserverKinds[kind] {
		http.NotFound(w, r)
		return
	}
	authorization := strings.TrimSpace(r.Header.Get("Authorization"))
	if len([]byte(observer.token)) < 32 || r.Header.Get("X-Bonfire-Media-Soak-Purpose") != kind ||
		!hmac.Equal([]byte(authorization), []byte("Bearer "+observer.token)) {
		writeAuthError(w, http.StatusUnauthorized, "media-soak observer authorization failed")
		return
	}
	body, bodyErr := io.ReadAll(io.LimitReader(r.Body, (16<<10)+1))
	if bodyErr != nil || len(body) > 16<<10 {
		writeAuthError(w, http.StatusBadRequest, "invalid media-soak body")
		return
	}
	var request mediaSoakObservationRequest
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeAuthError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeAuthError(w, http.StatusBadRequest, "invalid media-soak body")
		return
	}
	now := observer.now().UTC()
	if !hmac.Equal([]byte(strings.ToLower(strings.TrimSpace(r.Header.Get("X-Bonfire-Media-Soak-MAC")))), []byte(observer.requestMAC(r.Method, r.URL.Path, request, body))) {
		writeAuthError(w, http.StatusUnauthorized, "media-soak request MAC failed")
		return
	}
	if request.Schema != mediaSoakRequestSchema || request.Purpose != kind || request.ReleaseCommit != observer.releaseCommit ||
		!isMediaSoakDigest(request.Nonce) || !isMediaSoakDigest(request.RequestID) || request.IssuedAt.IsZero() || request.ExpiresAt.IsZero() ||
		request.ExpiresAt.Before(now) || request.IssuedAt.After(now.Add(5*time.Second)) || request.ExpiresAt.Sub(request.IssuedAt) > mediaSoakRequestTTL {
		writeAuthError(w, http.StatusForbidden, "media-soak request is expired or release/purpose mismatched")
		return
	}
	if (kind == "head-of-line" || kind == "ai-failure") && request.Inputs.FaultDurationMS != 10_000 {
		writeAuthError(w, http.StatusForbidden, "media-soak fault duration is not the checked bound")
		return
	}
	if kind != "head-of-line" && kind != "ai-failure" && request.Inputs.FaultDurationMS != 0 {
		writeAuthError(w, http.StatusForbidden, "media-soak non-fault request supplied a fault duration")
		return
	}

	observer.mu.Lock()
	for id, expires := range observer.seen {
		if !expires.After(now) {
			delete(observer.seen, id)
		}
	}
	if _, replay := observer.seen[request.RequestID]; replay {
		observer.mu.Unlock()
		writeAuthError(w, http.StatusConflict, "media-soak request replayed")
		return
	}
	observer.seen[request.RequestID] = request.ExpiresAt
	binding, bound := observer.bindings[request.Nonce]
	observer.mu.Unlock()
	if err := observer.consumeRequestNonce(request.RequestID, request.ExpiresAt); err != nil {
		writeAuthError(w, http.StatusConflict, "media-soak request replayed")
		return
	}

	var err error
	if !bound {
		if kind != "head-of-line" {
			writeAuthError(w, http.StatusConflict, "media-soak run scope is not established")
			return
		}
		binding, err = observer.runtime.Bind(r.Context(), request.Nonce, request.Inputs.RoomAID, request.Inputs.RoomBID)
		if err == nil {
			binding.ExpiresAt = now.Add(mediaSoakRunTTL)
			observer.mu.Lock()
			observer.bindings[request.Nonce] = binding
			observer.mu.Unlock()
		}
	} else if !binding.ExpiresAt.After(now) || request.Inputs.RoomAID != binding.RoomA.RoomID || request.Inputs.RoomBID != binding.RoomB.RoomID {
		err = errors.New("media-soak request escaped its bound rooms/sittings")
	}
	if err != nil {
		writeAuthError(w, http.StatusForbidden, err.Error())
		return
	}
	if kind == "head-of-line" || kind == "ai-failure" {
		binding.FaultDuration = 10 * time.Second
	}
	observation, err := observer.runtime.Observe(r.Context(), kind, binding)
	if err != nil {
		writeAuthError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	writeAuthJSON(w, http.StatusOK, map[string]any{
		"schema": mediaSoakResponseSchema, "kind": kind, "releaseCommit": observer.releaseCommit,
		"collectorNonce": request.Nonce, "requestId": request.RequestID, "observation": observation,
	})
}

func (observer *mediaSoakObserver) sourceAllowed(remoteAddress string) bool {
	host, _, err := net.SplitHostPort(remoteAddress)
	if err != nil {
		host = remoteAddress
	}
	ip := net.ParseIP(strings.TrimSpace(host))
	if ip == nil {
		return false
	}
	for _, raw := range strings.Split(observer.proxyCIDRs, ",") {
		_, network, err := net.ParseCIDR(strings.TrimSpace(raw))
		if err == nil && network.Contains(ip) {
			return true
		}
	}
	return false
}

func (observer *mediaSoakObserver) requestMAC(method, path string, request mediaSoakObservationRequest, body []byte) string {
	bodyDigest := sha256.Sum256(body)
	message := strings.Join([]string{method, path, request.ReleaseCommit, request.Nonce, request.Purpose, request.RequestID, request.IssuedAt.UTC().Format(time.RFC3339Nano), request.ExpiresAt.UTC().Format(time.RFC3339Nano), hex.EncodeToString(bodyDigest[:])}, "\n")
	mac := hmac.New(sha256.New, []byte(observer.token))
	_, _ = mac.Write([]byte(message))
	return hex.EncodeToString(mac.Sum(nil))
}

func (observer *mediaSoakObserver) consumeRequestNonce(requestID string, expires time.Time) error {
	if observer.nonceDir == "" {
		return errors.New("durable nonce directory is not configured")
	}
	if err := os.MkdirAll(observer.nonceDir, 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(filepath.Join(observer.nonceDir, requestID+".used"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, writeErr := fmt.Fprintln(file, expires.UTC().Format(time.RFC3339Nano))
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}

type liveMediaSoakRuntime struct {
	app              *kanbanBoardApp
	releaseCommit    string
	holBlockDuration time.Duration
	resourceMu       sync.Mutex
	lastWall         time.Time
	lastCPU          time.Duration
	canaryMu         sync.Mutex
	canaries         map[string]mediaSoakCanaryPlant
}

func (runtimeObserver *liveMediaSoakRuntime) Bind(_ context.Context, nonce, roomAID, roomBID string) (mediaSoakBinding, error) {
	if runtimeObserver.app == nil || roomAID == "" || roomBID == "" || normalizeRoomID(roomAID) == normalizeRoomID(roomBID) {
		return mediaSoakBinding{}, errors.New("two distinct live rooms are required")
	}
	bindRoom := func(roomID string) (mediaSoakScope, error) {
		roomID = normalizeRoomID(roomID)
		record, ok := appRoomStore().byID(roomID)
		if !ok || record.Archived || runtimeObserver.app.memory == nil || runtimeObserver.app.activeParticipantCount(roomID) < 3 {
			return mediaSoakScope{}, errors.New("media-soak room is absent, archived, or below three live participants")
		}
		sittingID := runtimeObserver.app.memory.currentMeetingID(roomID)
		generation := runtimeObserver.app.roomMediaGeneration(roomID)
		if sittingID == "" || generation == 0 || roomMediaActorForGeneration(roomID, generation) == nil {
			return mediaSoakScope{}, errors.New("media-soak room has no active sitting/media actor")
		}
		recipientEmail := ""
		listLock.RLock()
		for _, peer := range peerConnections {
			if normalizeRoomID(peer.roomID) == roomID && peer.mediaGeneration == generation && normalizeAccountEmail(peer.sessionEmail) != "" {
				recipientEmail = normalizeAccountEmail(peer.sessionEmail)
				break
			}
		}
		listLock.RUnlock()
		if recipientEmail == "" || accountStore().findUser(recipientEmail) == nil {
			return mediaSoakScope{}, errors.New("media-soak room has no authenticated recipient in its exact generation")
		}
		return mediaSoakScope{RoomID: roomID, SittingID: sittingID, Generation: generation,
			RoomDigest: mediaSoakDigest("room:" + roomID), SittingDigest: mediaSoakDigest("sitting:" + roomID + ":" + sittingID),
			MediaGenerationDigest: mediaSoakDigest(fmt.Sprintf("generation:%s:%s:%d", roomID, sittingID, generation)), RecipientEmail: recipientEmail}, nil
	}
	a, err := bindRoom(roomAID)
	if err != nil {
		return mediaSoakBinding{}, err
	}
	b, err := bindRoom(roomBID)
	if err != nil {
		return mediaSoakBinding{}, err
	}
	return mediaSoakBinding{Nonce: nonce, RoomA: a, RoomB: b}, nil
}

func (runtimeObserver *liveMediaSoakRuntime) Observe(ctx context.Context, kind string, binding mediaSoakBinding) (any, error) {
	if runtimeObserver.app == nil || runtimeObserver.app.roomMediaGeneration(binding.RoomA.RoomID) != binding.RoomA.Generation ||
		runtimeObserver.app.roomMediaGeneration(binding.RoomB.RoomID) != binding.RoomB.Generation ||
		runtimeObserver.app.memory.currentMeetingID(binding.RoomA.RoomID) != binding.RoomA.SittingID || runtimeObserver.app.memory.currentMeetingID(binding.RoomB.RoomID) != binding.RoomB.SittingID {
		return nil, errors.New("media-soak sitting fence changed")
	}
	switch kind {
	case "head-of-line":
		return runtimeObserver.observeHeadOfLine(ctx, binding)
	case "ai-failure":
		return runtimeObserver.observeAIFailure(ctx, binding)
	case "canary-plant":
		return runtimeObserver.plantCanaries(binding)
	case "canary-observe":
		return runtimeObserver.observeCanaries(binding)
	case "canary-scrub":
		return runtimeObserver.scrubCanaries(binding)
	case "runtime-attestation":
		return runtimeObserver.observeAttestation(binding)
	case "resources":
		return runtimeObserver.observeResources()
	default:
		return nil, errors.New("unsupported media-soak observation")
	}
}

type mediaSoakAttempt struct {
	StartedAt   time.Time `json:"startedAt"`
	CompletedAt time.Time `json:"completedAt"`
	Succeeded   bool      `json:"succeeded"`
}

func (runtimeObserver *liveMediaSoakRuntime) observeHeadOfLine(ctx context.Context, binding mediaSoakBinding) (any, error) {
	actorA := roomMediaActorForGeneration(binding.RoomA.RoomID, binding.RoomA.Generation)
	if actorA == nil || roomMediaActorForGeneration(binding.RoomB.RoomID, binding.RoomB.Generation) == nil {
		return nil, errors.New("media actors changed before HOL injection")
	}
	actorA.enqueueMu.Lock()
	locked := true
	defer func() {
		if locked {
			actorA.enqueueMu.Unlock()
		}
	}()
	blockedAt := time.Now().UTC()
	blockedDone := make(chan bool, 1)
	go func() { blockedDone <- actorA.enqueue(roomMediaCommandSignal) }()
	blockDuration := binding.FaultDuration
	if runtimeObserver.holBlockDuration > 0 {
		blockDuration = runtimeObserver.holBlockDuration
	}
	if blockDuration <= 0 {
		blockDuration = 10 * time.Second
	}
	timer := time.NewTimer(blockDuration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
	}
	actorA.enqueueMu.Unlock()
	locked = false
	if !<-blockedDone {
		return nil, errors.New("room A blocked command was not accepted")
	}
	releasedAt := time.Now().UTC()
	return map[string]any{"roomAId": binding.RoomA.RoomID, "roomBId": binding.RoomB.RoomID, "roomA": binding.RoomA, "roomB": binding.RoomB,
		"blockedAt": blockedAt, "releasedAt": releasedAt}, nil
}

func (runtimeObserver *liveMediaSoakRuntime) observeAIFailure(ctx context.Context, binding mediaSoakBinding) (any, error) {
	injected := time.Now().UTC()
	before := liveMediaPeerHealth(binding)
	scope := RoomScoutScope{RoomID: binding.RoomB.RoomID, SittingID: binding.RoomB.SittingID, MediaGeneration: binding.RoomB.Generation}
	runtimeObserver.app.mu.Lock()
	bundle := runtimeObserver.app.roomLiveLocked(binding.RoomB.RoomID).realtime
	runtimeObserver.app.mu.Unlock()
	if bundle == nil {
		return nil, errors.New("room B Scout runtime is unavailable for provider-failure injection")
	}
	bundle.mu.Lock()
	transport := bundle.transport
	bundleScope := bundle.scope
	bundle.mu.Unlock()
	if !bundleScope.same(scope) {
		return nil, errors.New("room B Scout runtime changed before provider-failure injection")
	}
	if _, ok := transport.(*openAIRoomScoutTransport); !ok {
		return nil, errors.New("room B Scout is not using the checked provider transport")
	}
	hitsBefore := mediaSoakProviderFaultHits(scope)
	setMediaSoakProviderFault(scope, true)
	defer setMediaSoakProviderFault(scope, false)
	if err := transport.WriteMixedPCM(context.Background(), []int16{1}); err != nil {
		return nil, err
	}
	if mediaSoakProviderFaultHits(scope) != hitsBefore+1 {
		return nil, errors.New("AI provider fault did not traverse the checked transport")
	}
	deadline := time.NewTimer(binding.FaultDuration)
	defer deadline.Stop()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-deadline.C:
	}
	accepted := requestRoomMediaCommandForGeneration(binding.RoomB.RoomID, binding.RoomB.Generation, roomMediaCommandSignal)
	after := liveMediaPeerHealth(binding)
	interruptions := before - after
	if interruptions < 0 {
		interruptions = 0
	}
	admissionFailures := 0
	if !accepted {
		admissionFailures = 1
	}
	return map[string]any{"injectedAt": injected, "recoveredAt": time.Now().UTC(), "mediaInterruptions": interruptions, "admissionFailures": admissionFailures}, nil
}

func liveMediaPeerHealth(binding mediaSoakBinding) int {
	allowed := map[string]bool{binding.RoomA.RoomID: true, binding.RoomB.RoomID: true}
	healthy := 0
	listLock.RLock()
	for _, peer := range peerConnections {
		if allowed[normalizeRoomID(peer.roomID)] && peer.peerConnection != nil {
			state := peer.peerConnection.ConnectionState()
			if state != webrtc.PeerConnectionStateFailed && state != webrtc.PeerConnectionStateClosed {
				healthy++
			}
		}
	}
	listLock.RUnlock()
	return healthy
}

func (runtimeObserver *liveMediaSoakRuntime) observeAttestation(binding mediaSoakBinding) (any, error) {
	gitTree := strings.TrimPrefix(strings.TrimSpace(os.Getenv("BONFIRE_GIT_TREE_DIGEST")), "sha256:")
	image := strings.TrimPrefix(strings.TrimSpace(os.Getenv("BONFIRE_IMAGE_DIGEST")), "sha256:")
	buildManifest := strings.TrimPrefix(strings.TrimSpace(os.Getenv("BONFIRE_BUILD_MANIFEST_SHA256")), "sha256:")
	configDigest := strings.TrimPrefix(strings.TrimSpace(os.Getenv("BONFIRE_BUILD_CONFIG_SHA256")), "sha256:")
	inputsDigest := strings.TrimPrefix(strings.TrimSpace(os.Getenv("BONFIRE_BUILD_TRANSITIVE_INPUTS_SHA256")), "sha256:")
	sourceArchiveDigest := strings.TrimPrefix(strings.TrimSpace(os.Getenv("BONFIRE_SOURCE_ARCHIVE_SHA256")), "sha256:")
	if !isMediaSoakDigest(gitTree) || !isMediaSoakDigest(image) || !isMediaSoakDigest(buildManifest) || !isMediaSoakDigest(configDigest) || !isMediaSoakDigest(inputsDigest) || !isMediaSoakDigest(sourceArchiveDigest) || runtimeObserver.releaseCommit == "" ||
		strings.TrimSpace(mediaSoakBuildCommit) != runtimeObserver.releaseCommit || strings.TrimPrefix(strings.TrimSpace(mediaSoakBuildTreeDigest), "sha256:") != gitTree ||
		strings.TrimPrefix(strings.TrimSpace(mediaSoakBuildConfigDigest), "sha256:") != configDigest || strings.TrimPrefix(strings.TrimSpace(mediaSoakBuildInputsDigest), "sha256:") != inputsDigest ||
		strings.TrimPrefix(strings.TrimSpace(mediaSoakBuildSourceArchiveDigest), "sha256:") != sourceArchiveDigest {
		return nil, errors.New("runtime image/tree attestation is not configured")
	}
	binary, err := os.Open("/proc/self/exe")
	if err != nil {
		binary, err = os.Open(os.Args[0])
	}
	if err != nil {
		return nil, err
	}
	hash := sha256.New()
	_, err = io.Copy(hash, binary)
	_ = binary.Close()
	if err != nil {
		return nil, err
	}
	hostname, _ := os.Hostname()
	return map[string]any{"hostDigest": mediaSoakDigest(hostname), "containerDigest": hex.EncodeToString(hash.Sum(nil)), "imageDigest": image,
		"gitTreeDigest": gitTree, "buildManifestSha256": buildManifest, "configSha256": configDigest, "transitiveInputsSha256": inputsDigest,
		"sourceArchiveSha256": sourceArchiveDigest, "collectorNonce": binding.Nonce, "releaseCommit": runtimeObserver.releaseCommit,
		"backend": "pion-room-actor", "featureFlag": "BONFIRE_MEDIA_BACKEND", "featureFlagValue": "pion", "capturedAt": time.Now().UTC()}, nil
}

func (runtimeObserver *liveMediaSoakRuntime) observeResources() (any, error) {
	now := time.Now().UTC()
	var usage syscall.Rusage
	_ = syscall.Getrusage(syscall.RUSAGE_SELF, &usage)
	cpu := time.Duration(usage.Utime.Sec)*time.Second + time.Duration(usage.Utime.Usec)*time.Microsecond + time.Duration(usage.Stime.Sec)*time.Second + time.Duration(usage.Stime.Usec)*time.Microsecond
	runtimeObserver.resourceMu.Lock()
	cpuPercent := 0.0
	if !runtimeObserver.lastWall.IsZero() && now.After(runtimeObserver.lastWall) {
		cpuPercent = float64(cpu-runtimeObserver.lastCPU) / float64(now.Sub(runtimeObserver.lastWall)) * 100 / float64(runtime.GOMAXPROCS(0))
	}
	runtimeObserver.lastWall, runtimeObserver.lastCPU = now, cpu
	runtimeObserver.resourceMu.Unlock()
	if cpuPercent < 0 {
		cpuPercent = 0
	}
	if cpuPercent > 100 {
		cpuPercent = 100
	}
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	readUint := func(paths ...string) uint64 {
		for _, path := range paths {
			if raw, err := os.ReadFile(path); err == nil {
				value, parseErr := strconv.ParseUint(strings.TrimSpace(string(raw)), 10, 64)
				if parseErr == nil && value > 0 {
					return value
				}
			}
		}
		return 0
	}
	limit := readUint("/sys/fs/cgroup/memory.max", "/sys/fs/cgroup/memory/memory.limit_in_bytes")
	if limit == 0 || limit >= uint64(1)<<60 {
		return nil, errors.New("container memory limit is missing or unlimited")
	}
	used := readUint("/sys/fs/cgroup/memory.current", "/sys/fs/cgroup/memory/memory.usage_in_bytes")
	if used == 0 {
		used = memory.Sys
	}
	rssPercent := 0.0
	if limit > 0 {
		rssPercent = float64(used) / float64(limit) * 100
	}
	if rssPercent > 100 {
		rssPercent = 100
	}
	return map[string]any{"processCpuPercent": cpuPercent, "rssContainerLimitPercent": rssPercent}, nil
}

func mediaSoakDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func isMediaSoakDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil && strings.ToLower(value) == value
}
