package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v4"
)

type fakeMediaSoakRuntime struct {
	binds int
	calls []string
}

func (runtime *fakeMediaSoakRuntime) Bind(_ context.Context, nonce, roomA, roomB string) (mediaSoakBinding, error) {
	runtime.binds++
	return mediaSoakBinding{Nonce: nonce,
		RoomA: mediaSoakScope{RoomID: roomA, SittingID: "sitting-a", Generation: 1, RoomDigest: mediaSoakDigest("room:" + roomA), SittingDigest: mediaSoakDigest("sitting-a"), MediaGenerationDigest: mediaSoakDigest("generation-a")},
		RoomB: mediaSoakScope{RoomID: roomB, SittingID: "sitting-b", Generation: 2, RoomDigest: mediaSoakDigest("room:" + roomB), SittingDigest: mediaSoakDigest("sitting-b"), MediaGenerationDigest: mediaSoakDigest("generation-b")}}, nil
}

func (runtime *fakeMediaSoakRuntime) Observe(_ context.Context, kind string, binding mediaSoakBinding) (any, error) {
	runtime.calls = append(runtime.calls, kind)
	return map[string]any{"kind": kind, "roomAId": binding.RoomA.RoomID, "roomBId": binding.RoomB.RoomID}, nil
}

func newObserverHarness(t *testing.T, now time.Time) (*mediaSoakObserver, *fakeMediaSoakRuntime) {
	t.Helper()
	runtime := &fakeMediaSoakRuntime{}
	return &mediaSoakObserver{enabled: true, releaseCommit: "0123456789abcdef0123456789abcdef01234567", token: "0123456789abcdef0123456789abcdef", nonceDir: t.TempDir(), proxyCIDRs: "192.0.2.0/24", now: func() time.Time { return now }, runtime: runtime, bindings: map[string]mediaSoakBinding{}, seen: map[string]time.Time{}}, runtime
}

func mediaSoakRequest(t *testing.T, observer *mediaSoakObserver, now time.Time, kind, nonce, requestID, roomA, roomB string, mutate func(*mediaSoakObservationRequest)) *httptest.ResponseRecorder {
	t.Helper()
	payload := mediaSoakObservationRequest{Schema: mediaSoakRequestSchema, ReleaseCommit: observer.releaseCommit, Nonce: nonce, Purpose: kind, RequestID: requestID, IssuedAt: now, ExpiresAt: now.Add(mediaSoakRequestTTL)}
	payload.Inputs.RoomAID, payload.Inputs.RoomBID = roomA, roomB
	if kind == "head-of-line" || kind == "ai-failure" {
		payload.Inputs.FaultDurationMS = 10_000
	}
	if mutate != nil {
		mutate(&payload)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/internal/media-soak/"+kind, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+observer.token)
	request.Header.Set("X-Bonfire-Media-Soak-Purpose", kind)
	request.Header.Set("X-Bonfire-Media-Soak-MAC", observer.requestMAC(http.MethodPost, "/internal/media-soak/"+kind, payload, body))
	response := httptest.NewRecorder()
	observer.ServeHTTP(response, request)
	return response
}

func TestMediaSoakObserverDefaultOffAndPurposeScopedAuth(t *testing.T) {
	now := time.Date(2026, 7, 22, 20, 0, 0, 0, time.UTC)
	observer, _ := newObserverHarness(t, now)
	observer.enabled = false
	request := httptest.NewRequest(http.MethodPost, "/internal/media-soak/head-of-line", nil)
	response := httptest.NewRecorder()
	observer.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("default-off status=%d", response.Code)
	}

	observer.enabled = true
	request = httptest.NewRequest(http.MethodPost, "/internal/media-soak/head-of-line", bytes.NewReader([]byte("{}")))
	request.RemoteAddr = "203.0.113.9:4444"
	response = httptest.NewRecorder()
	observer.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("source-network status=%d", response.Code)
	}

	request = httptest.NewRequest(http.MethodPost, "/internal/media-soak/head-of-line", bytes.NewReader([]byte("{}")))
	request.Header.Set("Authorization", "Bearer wrong")
	request.Header.Set("X-Bonfire-Media-Soak-Purpose", "head-of-line")
	response = httptest.NewRecorder()
	observer.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status=%d", response.Code)
	}

	request.Header.Set("Authorization", "Bearer "+observer.token)
	request.Header.Set("X-Bonfire-Media-Soak-Purpose", "resources")
	response = httptest.NewRecorder()
	observer.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("wrong-purpose status=%d", response.Code)
	}
}

func TestMediaSoakObserverBindsRunAndRejectsReplayOrCrossRoom(t *testing.T) {
	now := time.Date(2026, 7, 22, 20, 0, 0, 0, time.UTC)
	observer, runtime := newObserverHarness(t, now)
	nonce := mediaSoakDigest("nonce")
	firstID := mediaSoakDigest("request-1")
	response := mediaSoakRequest(t, observer, now, "head-of-line", nonce, firstID, "room-a", "room-b", nil)
	if response.Code != http.StatusOK || runtime.binds != 1 {
		t.Fatalf("bind status=%d binds=%d body=%s", response.Code, runtime.binds, response.Body.String())
	}

	response = mediaSoakRequest(t, observer, now, "head-of-line", nonce, firstID, "room-a", "room-b", nil)
	if response.Code != http.StatusConflict {
		t.Fatalf("replay status=%d body=%s", response.Code, response.Body.String())
	}

	response = mediaSoakRequest(t, observer, now, "resources", nonce, mediaSoakDigest("request-2"), "room-a", "room-c", nil)
	if response.Code != http.StatusForbidden {
		t.Fatalf("cross-room status=%d body=%s", response.Code, response.Body.String())
	}

	response = mediaSoakRequest(t, observer, now, "resources", nonce, mediaSoakDigest("request-3"), "room-a", "room-b", nil)
	if response.Code != http.StatusOK || runtime.calls[len(runtime.calls)-1] != "resources" {
		t.Fatalf("bound follow-up status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestMediaSoakObserverRejectsExpiryAndReleaseMismatch(t *testing.T) {
	now := time.Date(2026, 7, 22, 20, 0, 0, 0, time.UTC)
	observer, _ := newObserverHarness(t, now)
	nonce := mediaSoakDigest("nonce")
	response := mediaSoakRequest(t, observer, now, "head-of-line", nonce, mediaSoakDigest("expired"), "room-a", "room-b", func(request *mediaSoakObservationRequest) {
		request.IssuedAt = now.Add(-time.Minute)
		request.ExpiresAt = now.Add(-time.Second)
	})
	if response.Code != http.StatusForbidden {
		t.Fatalf("expired status=%d", response.Code)
	}
	response = mediaSoakRequest(t, observer, now, "head-of-line", nonce, mediaSoakDigest("release"), "room-a", "room-b", func(request *mediaSoakObservationRequest) {
		request.ReleaseCommit = "ffffffffffffffffffffffffffffffffffffffff"
	})
	if response.Code != http.StatusForbidden {
		t.Fatalf("release mismatch status=%d", response.Code)
	}
}

func TestMediaSoakObserverRejectsWrongBearerAndUnboundedFaultBeforeBinding(t *testing.T) {
	now := time.Date(2026, 7, 22, 20, 0, 0, 0, time.UTC)
	observer, runtime := newObserverHarness(t, now)
	nonce := mediaSoakDigest("nonce")
	response := mediaSoakRequest(t, observer, now, "head-of-line", nonce, mediaSoakDigest("bad-duration"), "room-a", "room-b", func(request *mediaSoakObservationRequest) {
		request.Inputs.FaultDurationMS = 9_999
	})
	if response.Code != http.StatusForbidden || runtime.binds != 0 {
		t.Fatalf("unbounded fault status=%d binds=%d", response.Code, runtime.binds)
	}

	payload := mediaSoakObservationRequest{Schema: mediaSoakRequestSchema, ReleaseCommit: observer.releaseCommit, Nonce: nonce, Purpose: "head-of-line", RequestID: mediaSoakDigest("wrong-bearer"), IssuedAt: now, ExpiresAt: now.Add(mediaSoakRequestTTL)}
	payload.Inputs.RoomAID, payload.Inputs.RoomBID, payload.Inputs.FaultDurationMS = "room-a", "room-b", 10_000
	body, _ := json.Marshal(payload)
	request := httptest.NewRequest(http.MethodPost, "/internal/media-soak/head-of-line", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+observer.token+"-wrong")
	request.Header.Set("X-Bonfire-Media-Soak-Purpose", "head-of-line")
	request.Header.Set("X-Bonfire-Media-Soak-MAC", observer.requestMAC(http.MethodPost, "/internal/media-soak/head-of-line", payload, body))
	response = httptest.NewRecorder()
	observer.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized || runtime.binds != 0 {
		t.Fatalf("wrong bearer status=%d binds=%d", response.Code, runtime.binds)
	}
}

func TestMediaSoakCanaryAdaptersCoverEverySurfaceDirectionAndScrub(t *testing.T) {
	store, err := newMeetingMemoryStore(filepath.Join(t.TempDir(), "meeting-memory.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	app := &kanbanBoardApp{memory: store, roomLive: map[string]*roomLiveState{}}
	previousApp := kanbanApp
	kanbanApp = app
	t.Cleanup(func() { kanbanApp = previousApp })
	scope := func(name, email string, generation uint64) mediaSoakScope {
		sitting := store.ensureMeetingID(name)
		value := mediaSoakScope{RoomID: name, SittingID: sitting, Generation: generation, RoomDigest: mediaSoakDigest("room:" + name), SittingDigest: mediaSoakDigest("sitting:" + name + ":" + sitting), MediaGenerationDigest: mediaSoakDigest("generation:" + name), RecipientEmail: email}
		bundle, bundleErr := newRoomRealtimeBundle(value.roomScoutScope(), func(string, any) {})
		if bundleErr != nil {
			t.Fatal(bundleErr)
		}
		app.mu.Lock()
		state := app.roomLiveLocked(name)
		state.mediaGen, state.realtime = generation, bundle
		app.mu.Unlock()
		t.Cleanup(func() { _ = bundle.close() })
		return value
	}
	binding := mediaSoakBinding{Nonce: mediaSoakDigest("canary-run"), RoomA: scope("room-a", "aj@shareability.com", 1), RoomB: scope("room-b", "tim@shareability.com", 2)}

	listLock.Lock()
	previousPeers, previousLocals := peerConnections, trackLocals
	previousParticipants, previousSessions, previousRooms, previousOwners := trackParticipants, trackParticipantSessions, trackRooms, trackMediaOwners
	previousSources, previousGroups, previousRIDs, previousTiers := trackSourceIDs, trackLayerGroups, trackLayerRIDs, subscriberLayerTiers
	trackLocals = map[string]*webrtc.TrackLocalStaticRTP{}
	trackParticipants, trackParticipantSessions, trackRooms = map[string]string{}, map[string]string{}, map[string]string{}
	trackMediaOwners = map[string]trackMediaOwner{}
	trackSourceIDs, trackLayerGroups, trackLayerRIDs, subscriberLayerTiers = map[string]string{}, map[string]string{}, map[string]string{}, map[string]string{}
	peerConnections = []peerConnectionState{
		{sessionID: "a-1", roomID: "room-a", sittingID: binding.RoomA.SittingID, mediaGeneration: 1, sessionEmail: "aj@shareability.com", websocket: mediaSoakTestWriter(t)},
		{sessionID: "a-2", roomID: "room-a", sittingID: binding.RoomA.SittingID, mediaGeneration: 1, sessionEmail: "aj@shareability.com", websocket: mediaSoakTestWriter(t)},
		{sessionID: "a-3", roomID: "room-a", sittingID: binding.RoomA.SittingID, mediaGeneration: 1, sessionEmail: "aj@shareability.com", websocket: mediaSoakTestWriter(t)},
		{sessionID: "b-1", roomID: "room-b", sittingID: binding.RoomB.SittingID, mediaGeneration: 2, sessionEmail: "tim@shareability.com", websocket: mediaSoakTestWriter(t)},
		{sessionID: "b-2", roomID: "room-b", sittingID: binding.RoomB.SittingID, mediaGeneration: 2, sessionEmail: "tim@shareability.com", websocket: mediaSoakTestWriter(t)},
		{sessionID: "b-3", roomID: "room-b", sittingID: binding.RoomB.SittingID, mediaGeneration: 2, sessionEmail: "tim@shareability.com", websocket: mediaSoakTestWriter(t)},
	}
	listLock.Unlock()
	t.Cleanup(func() {
		listLock.Lock()
		peerConnections, trackLocals = previousPeers, previousLocals
		trackParticipants, trackParticipantSessions, trackRooms, trackMediaOwners = previousParticipants, previousSessions, previousRooms, previousOwners
		trackSourceIDs, trackLayerGroups, trackLayerRIDs, subscriberLayerTiers = previousSources, previousGroups, previousRIDs, previousTiers
		listLock.Unlock()
	})

	runtime := &liveMediaSoakRuntime{app: app}
	planted, err := runtime.plantCanaries(binding)
	if err != nil || planted.(map[string]any)["checkCount"] != 48 {
		t.Fatalf("plant=%v err=%v", planted, err)
	}
	if _, err := runtime.observeCanaries(binding); err != nil {
		t.Fatal(err)
	}
	scrubbed, err := runtime.scrubCanaries(binding)
	if err != nil {
		t.Fatal(err)
	}
	checks := scrubbed.(map[string]any)["checks"].([]map[string]any)
	coverage := map[string]bool{}
	for _, check := range checks {
		key := check["surface"].(string) + "|" + check["direction"].(string) + "|" + check["sentinel"].(string)
		coverage[key] = true
		if check["observed"].(bool) != check["expectedPresent"].(bool) {
			t.Fatalf("canary mismatch: %+v", check)
		}
		if check["ingressAcknowledged"] != true || check["readAcknowledged"] != true || check["scrubAcknowledged"] != true || check["residueCount"] != 0 {
			t.Fatalf("canary lifecycle was not fully acknowledged: %+v", check)
		}
		publicationSurface := check["surface"] == "chat" || check["surface"] == "recap" || check["surface"] == "artifact"
		if publicationSurface {
			if check["publicationRecipientCount"] != 3 || check["deletionRecipientCount"] != 3 || check["publicationRecipientSetDigest"] == "" || check["publicationRecipientSetDigest"] != check["deletionRecipientSetDigest"] {
				t.Fatalf("publication recipient proof mismatch: %+v", check)
			}
		} else if check["publicationRecipientCount"] != 0 || check["deletionRecipientCount"] != 0 || check["publicationRecipientSetDigest"] != "" || check["deletionRecipientSetDigest"] != "" {
			t.Fatalf("non-publication canary carried recipient proof: %+v", check)
		}
	}
	for _, surface := range []string{"track", "chat", "scout", "transcript", "recap", "artifact"} {
		for _, direction := range []string{"a_to_b", "b_to_a"} {
			for _, sentinel := range []string{"current", "prior_sitting", "prior_generation", "unrelated_room"} {
				if !coverage[surface+"|"+direction+"|"+sentinel] {
					t.Fatalf("missing canary coverage %s %s %s", surface, direction, sentinel)
				}
			}
		}
	}
	if scrubbed.(map[string]any)["scrubbed"] != true || scrubbed.(map[string]any)["residueCount"] != 0 {
		t.Fatalf("scrub=%v", scrubbed)
	}
	durable, err := os.ReadFile(store.path)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if strings.Contains(string(durable), "mediaSoakCanary") || strings.Contains(string(durable), binding.Nonce) {
		t.Fatal("media-soak durable store retained canary residue")
	}
	if _, err := runtime.observeCanaries(binding); err == nil {
		t.Fatal("scrubbed canary plant remained observable")
	}
}

func TestProductionScopedRoomPublicationExcludesStaleSockets(t *testing.T) {
	if attempted, delivered, err := broadcastRoomKanbanEventAcknowledged("room-a", "room_chat", map[string]any{"id": "unsafe"}); err == nil || attempted != 0 || delivered != 0 {
		t.Fatalf("room-only publication compatibility path did not fail closed attempted=%d delivered=%d err=%v", attempted, delivered, err)
	}
	listLock.Lock()
	previousPeers := peerConnections
	peerConnections = []peerConnectionState{
		{sessionID: "current-1", roomID: "room-a", sittingID: "sitting-a", mediaGeneration: 7, websocket: mediaSoakTestWriter(t)},
		{sessionID: "current-2", roomID: "room-a", sittingID: "sitting-a", mediaGeneration: 7, websocket: mediaSoakTestWriter(t)},
		{sessionID: "current-3", roomID: "room-a", sittingID: "sitting-a", mediaGeneration: 7, websocket: mediaSoakTestWriter(t)},
		{sessionID: "stale-sitting", roomID: "room-a", sittingID: "sitting-old", mediaGeneration: 7, websocket: mediaSoakTestWriter(t)},
		{sessionID: "stale-generation", roomID: "room-a", sittingID: "sitting-a", mediaGeneration: 6, websocket: mediaSoakTestWriter(t)},
		{sessionID: "other-room", roomID: "room-b", sittingID: "sitting-a", mediaGeneration: 7, websocket: mediaSoakTestWriter(t)},
	}
	listLock.Unlock()
	t.Cleanup(func() {
		listLock.Lock()
		peerConnections = previousPeers
		listLock.Unlock()
	})
	acks, err := broadcastScopedRoomKanbanEventAcknowledged(RoomScoutScope{RoomID: "room-a", SittingID: "sitting-a", MediaGeneration: 7}, "room_chat", map[string]any{"id": "publication"})
	if err != nil {
		t.Fatal(err)
	}
	delivered := map[string]bool{}
	for _, ack := range acks {
		delivered[ack.SessionID] = ack.Delivered
		if ack.Delivered != ack.Authorized {
			t.Fatalf("production scoped acknowledgement mismatch: %+v", ack)
		}
	}
	for _, sessionID := range []string{"current-1", "current-2", "current-3"} {
		if !delivered[sessionID] {
			t.Fatalf("current recipient %s missed publication", sessionID)
		}
	}
	for _, sessionID := range []string{"stale-sitting", "stale-generation", "other-room"} {
		if delivered[sessionID] {
			t.Fatalf("stale or unrelated recipient %s received publication", sessionID)
		}
	}
}

func TestOfficeGenerationZeroCompatibilityNeverAuthorizesPeers(t *testing.T) {
	if roomPublicationScopeValid(RoomScoutScope{RoomID: "named-room", SittingID: "sitting", MediaGeneration: 0}) {
		t.Fatal("named-room generation-zero scope was accepted")
	}
	store, err := newMeetingMemoryStore(filepath.Join(t.TempDir(), "meeting-memory.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	sittingID := store.ensureMeetingID(officeRoomID)
	app := &kanbanBoardApp{memory: store, roomLive: map[string]*roomLiveState{}}
	previousApp := kanbanApp
	kanbanApp = app
	listLock.Lock()
	previousPeers, previousOffice := peerConnections, officeConnections
	peerConnections = []peerConnectionState{
		{sessionID: "zero-peer-exact", roomID: officeRoomID, sittingID: sittingID, mediaGeneration: 0, websocket: mediaSoakTestWriter(t)},
		{sessionID: "zero-peer-stale", roomID: officeRoomID, sittingID: "stale-sitting", mediaGeneration: 0, websocket: mediaSoakTestWriter(t)},
	}
	officeConnections = map[string]officeConnectionState{
		"legacy-office-shell": {websocket: mediaSoakTestWriter(t), sessionEmail: "aj@shareability.com"},
	}
	listLock.Unlock()
	t.Cleanup(func() {
		listLock.Lock()
		peerConnections, officeConnections = previousPeers, previousOffice
		listLock.Unlock()
		kanbanApp = previousApp
	})

	acks, err := broadcastScopedRoomKanbanEventAcknowledged(RoomScoutScope{RoomID: officeRoomID, SittingID: sittingID, MediaGeneration: 0}, "room_chat", map[string]any{"id": "legacy"})
	if err != nil {
		t.Fatal(err)
	}
	results := map[string]scopedRoomDeliveryAcknowledgement{}
	for _, ack := range acks {
		results[ack.SessionID] = ack
	}
	if !results["legacy-office-shell"].Authorized || !results["legacy-office-shell"].Delivered {
		t.Fatalf("current legacy office shell was not delivered: %+v", results["legacy-office-shell"])
	}
	for _, sessionID := range []string{"zero-peer-exact", "zero-peer-stale"} {
		if results[sessionID].Authorized || results[sessionID].Delivered {
			t.Fatalf("generation-zero peer %s was accepted: %+v", sessionID, results[sessionID])
		}
	}

	staleAcks, err := broadcastScopedRoomKanbanEventAcknowledged(RoomScoutScope{RoomID: officeRoomID, SittingID: "stale-sitting", MediaGeneration: 0}, "room_chat", map[string]any{"id": "stale"})
	if err != nil {
		t.Fatal(err)
	}
	for _, ack := range staleAcks {
		if ack.Delivered {
			t.Fatalf("stale office scope delivered to %+v", ack)
		}
	}
}

func TestRoomArtifactEventUsesPersistedAuthorizationScope(t *testing.T) {
	store, err := newMeetingMemoryStore(filepath.Join(t.TempDir(), "meeting-memory.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	sittingID := store.ensureMeetingID("room-a")
	app := &kanbanBoardApp{memory: store, roomLive: map[string]*roomLiveState{}}
	previousApp := kanbanApp
	kanbanApp = app
	listLock.Lock()
	previousPeers := peerConnections
	peerConnections = []peerConnectionState{
		{sessionID: "artifact-current", roomID: "room-a", sittingID: sittingID, mediaGeneration: 7, websocket: mediaSoakTestWriter(t)},
		{sessionID: "artifact-stale", roomID: "room-a", sittingID: sittingID, mediaGeneration: 6, websocket: mediaSoakTestWriter(t)},
		{sessionID: "artifact-unrelated", roomID: "room-b", sittingID: sittingID, mediaGeneration: 7, websocket: mediaSoakTestWriter(t)},
	}
	listLock.Unlock()
	t.Cleanup(func() {
		listLock.Lock()
		peerConnections = previousPeers
		listLock.Unlock()
		kanbanApp = previousApp
	})
	entry, appended, err := store.appendOSArtifact("room-artifact-scope-proof", "private body", map[string]string{
		"title": "Room artifact", "visibility": "room_only", "ownerEmail": "aj@shareability.com",
		"roomId": "room-a", "meetingId": sittingID, "sittingId": sittingID, "mediaGeneration": "7",
	})
	if err != nil || !appended {
		t.Fatalf("append room artifact appended=%v err=%v", appended, err)
	}
	forged := cloneMemoryEntry(entry)
	forged.Metadata["visibility"] = "organization"
	forged.Metadata["roomId"] = "room-b"
	if acks, err := emitOSArtifactEventForApp(app, forged); err == nil || len(acks) != 0 {
		t.Fatalf("forged artifact scope was accepted: acks=%+v err=%v", acks, err)
	}
	missing := cloneMemoryEntry(entry)
	missing.ID = "nonexistent-room-artifact"
	if acks, err := emitOSArtifactEventForApp(app, missing); err == nil || len(acks) != 0 {
		t.Fatalf("nonexistent artifact was accepted: acks=%+v err=%v", acks, err)
	}

	acks, err := emitOSArtifactEventForApp(app, entry)
	if err != nil {
		t.Fatalf("stored artifact event: %v", err)
	}
	results := map[string]scopedRoomDeliveryAcknowledgement{}
	for _, ack := range acks {
		results[ack.SessionID] = ack
	}
	if !results["artifact-current"].Delivered {
		t.Fatalf("current room artifact recipient missed event: %+v", results)
	}
	for _, sessionID := range []string{"artifact-stale", "artifact-unrelated"} {
		if results[sessionID].Delivered {
			t.Fatalf("persisted artifact scope widened to %s: %+v", sessionID, results[sessionID])
		}
	}

	_, deleteAcks, deleted, err := app.deleteOSArtifactAndEmit(entry.ID)
	if err != nil || !deleted {
		t.Fatalf("real artifact delete deleted=%v err=%v", deleted, err)
	}
	deleteResults := map[string]scopedRoomDeliveryAcknowledgement{}
	for _, ack := range deleteAcks {
		deleteResults[ack.SessionID] = ack
	}
	for sessionID, publication := range results {
		if deletion := deleteResults[sessionID]; deletion.Delivered != publication.Delivered || deletion.Authorized != publication.Authorized {
			t.Fatalf("delete recipient set differs for %s: publication=%+v deletion=%+v", sessionID, publication, deletion)
		}
	}
	if _, found := store.artifactAuthorizationHeaderByIDForEvent(entry.ID); found {
		t.Fatal("artifact remained after acknowledged delete")
	}
	if acks, err := emitOSArtifactEventForApp(app, entry); err == nil || len(acks) != 0 {
		t.Fatalf("deleted artifact entry was accepted: acks=%+v err=%v", acks, err)
	}
	forgedProjection := artifactDeletionProjection{token: &artifactDeletionProjectionSeal{}}
	if acks, err := emitOSArtifactDeletionEvent(app, forgedProjection); err == nil || len(acks) != 0 {
		t.Fatalf("unsealed delete projection was accepted: acks=%+v err=%v", acks, err)
	}
}

func TestArtifactRoomScopeMetadataIsImmutable(t *testing.T) {
	store, err := newMeetingMemoryStore(filepath.Join(t.TempDir(), "meeting-memory.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	entry, appended, err := store.appendOSArtifact("immutable-room-artifact", "body", map[string]string{
		"title": "Immutable room artifact", "visibility": "room_only", "roomId": "room-a",
		"meetingId": "sitting-a", "sittingId": "sitting-a", "mediaGeneration": "7",
	})
	if err != nil || !appended {
		t.Fatalf("append artifact appended=%v err=%v", appended, err)
	}

	for key, value := range map[string]string{"roomId": "room-b", "meetingId": "sitting-b", "sittingId": "sitting-b", "mediaGeneration": "8"} {
		if _, changed, err := store.updateOSArtifactMetadata(entry.ID, map[string]string{key: value}); err == nil || changed {
			t.Errorf("metadata scope mutation %s accepted changed=%v err=%v", key, changed, err)
		}
	}
	if _, changed, err := store.updateOSArtifactWithMetadata(entry.ID, "Immutable room artifact", "body updated", "tester", map[string]string{"roomId": "room-b"}); err == nil || changed {
		t.Fatalf("body update mutated room scope changed=%v err=%v", changed, err)
	}
	if changed, err := store.updateOSArtifactsMetadataBatch([]string{entry.ID}, map[string]string{"mediaGeneration": "8"}); err == nil || changed != 0 {
		t.Fatalf("batch update mutated media generation changed=%d err=%v", changed, err)
	}
	header, found := store.artifactAuthorizationHeaderByID(entry.ID)
	if !found || header.RoomID != "room-a" || header.SittingID != "sitting-a" || header.MediaGeneration != 7 {
		t.Fatalf("immutable scope changed: found=%v header=%+v", found, header)
	}
}

func TestArtifactEventRejectsStaleAuthorizationHeader(t *testing.T) {
	store, err := newMeetingMemoryStore(filepath.Join(t.TempDir(), "meeting-memory.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	app := &kanbanBoardApp{memory: store, roomLive: map[string]*roomLiveState{}}
	stale, appended, err := store.appendOSArtifact("stale-event-header", "revision one", map[string]string{
		"title": "Stale header proof", "visibility": "organization",
	})
	if err != nil || !appended {
		t.Fatalf("append artifact appended=%v err=%v", appended, err)
	}
	if _, changed, err := store.updateOSArtifact(stale.ID, "Stale header proof", "revision two", "tester"); err != nil || !changed {
		t.Fatalf("update artifact changed=%v err=%v", changed, err)
	}
	if acks, err := emitOSArtifactEventForApp(app, stale); err == nil || len(acks) != 0 {
		t.Fatalf("stale authorization header was accepted: acks=%+v err=%v", acks, err)
	}
}

func TestProductionArtifactDeletePathsPreserveExactRecipients(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		relevance  string
		expiresAt  string
		deletePath func(*kanbanBoardApp, string) ([]scopedRoomDeliveryAcknowledgement, bool, error)
	}{
		{name: "generic", deletePath: func(app *kanbanBoardApp, id string) ([]scopedRoomDeliveryAcknowledgement, bool, error) {
			_, acknowledgements, deleted, err := app.deleteEntryByIDAcknowledged(id)
			return acknowledgements, deleted, err
		}},
		{name: "admin quarantine", relevance: relevanceQuarantined, expiresAt: time.Now().UTC().Add(time.Hour).Format(time.RFC3339Nano), deletePath: func(app *kanbanBoardApp, id string) ([]scopedRoomDeliveryAcknowledgement, bool, error) {
			acknowledgements, err := app.deleteQuarantinedEntryAcknowledged(id, "admin@shareability.com")
			return acknowledgements, err == nil, err
		}},
		{name: "expiry", relevance: relevanceQuarantined, expiresAt: time.Now().UTC().Add(-time.Hour).Format(time.RFC3339Nano), deletePath: func(app *kanbanBoardApp, id string) ([]scopedRoomDeliveryAcknowledgement, bool, error) {
			acknowledgements := app.sweepExpiredQuarantine("artifact-expiry-cursor")[id]
			_, found := app.memory.entryByID(id)
			return acknowledgements, !found, nil
		}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			store, err := newMeetingMemoryStore(filepath.Join(t.TempDir(), "meeting-memory.jsonl"))
			if err != nil {
				t.Fatal(err)
			}
			app := &kanbanBoardApp{memory: store, roomLive: map[string]*roomLiveState{}}
			previousApp := kanbanApp
			kanbanApp = app
			listLock.Lock()
			previousPeers := peerConnections
			peerConnections = []peerConnectionState{
				{sessionID: "delete-current", roomID: "room-a", sittingID: "sitting-a", mediaGeneration: 7, websocket: mediaSoakTestWriter(t)},
				{sessionID: "delete-stale-sitting", roomID: "room-a", sittingID: "sitting-old", mediaGeneration: 7, websocket: mediaSoakTestWriter(t)},
				{sessionID: "delete-stale-generation", roomID: "room-a", sittingID: "sitting-a", mediaGeneration: 6, websocket: mediaSoakTestWriter(t)},
				{sessionID: "delete-unrelated", roomID: "room-b", sittingID: "sitting-a", mediaGeneration: 7, websocket: mediaSoakTestWriter(t)},
			}
			listLock.Unlock()
			t.Cleanup(func() {
				listLock.Lock()
				peerConnections = previousPeers
				listLock.Unlock()
				kanbanApp = previousApp
			})

			metadata := map[string]string{
				"title": "Production delete proof", "visibility": "room_only", "roomId": "room-a",
				"meetingId": "sitting-a", "sittingId": "sitting-a", "mediaGeneration": "7",
			}
			if testCase.relevance != "" {
				metadata[relevanceMetadataKey] = testCase.relevance
				metadata["expiresAt"] = testCase.expiresAt
				metadata["classifierReason"] = "recipient proof"
			}
			id := "production-delete-" + strings.ReplaceAll(testCase.name, " ", "-")
			if _, appended, err := store.appendOSArtifact(id, "body", metadata); err != nil || !appended {
				t.Fatalf("append artifact appended=%v err=%v", appended, err)
			}
			acknowledgements, deleted, err := testCase.deletePath(app, id)
			if err != nil || !deleted {
				t.Fatalf("delete path deleted=%v err=%v", deleted, err)
			}
			results := map[string]scopedRoomDeliveryAcknowledgement{}
			for _, acknowledgement := range acknowledgements {
				results[acknowledgement.SessionID] = acknowledgement
			}
			if !results["delete-current"].Authorized || !results["delete-current"].Delivered {
				t.Fatalf("current recipient missed deletion: %+v", results)
			}
			for _, sessionID := range []string{"delete-stale-sitting", "delete-stale-generation", "delete-unrelated"} {
				if results[sessionID].Authorized || results[sessionID].Delivered {
					t.Fatalf("unauthorized recipient %s received deletion: %+v", sessionID, results[sessionID])
				}
			}
		})
	}
}

func TestRawGenericArtifactDeleteSeamsFailClosed(t *testing.T) {
	store, err := newMeetingMemoryStore(filepath.Join(t.TempDir(), "meeting-memory.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"raw-single-artifact", "raw-batch-artifact"} {
		if _, appended, err := store.appendOSArtifact(id, "body", map[string]string{"title": id}); err != nil || !appended {
			t.Fatalf("append %s appended=%v err=%v", id, appended, err)
		}
	}
	if _, deleted, err := store.deleteEntryByID("raw-single-artifact"); err == nil || deleted {
		t.Fatalf("raw single delete accepted artifact deleted=%v err=%v", deleted, err)
	}
	if deleted, err := store.deleteEntriesByID([]string{"raw-batch-artifact"}); err == nil || deleted != 0 {
		t.Fatalf("raw batch delete accepted artifact deleted=%d err=%v", deleted, err)
	}
	for _, id := range []string{"raw-single-artifact", "raw-batch-artifact"} {
		if _, found := store.entryByID(id); !found {
			t.Fatalf("failed-closed raw delete removed %s", id)
		}
	}
}

func TestMediaSoakCanariesNeverEnterUnsummarizedOrWorkerCursorWindows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "meeting-memory.jsonl")
	store, err := newMeetingMemoryStore(path)
	if err != nil {
		t.Fatal(err)
	}
	metadata := map[string]string{"roomId": officeRoomID, "meetingId": "sitting-a", "visibility": "organization"}
	if _, appended, err := store.appendEntry(meetingMemoryKindTranscript, "real-transcript", "real transcript", metadata); err != nil || !appended {
		t.Fatalf("append real transcript appended=%v err=%v", appended, err)
	}
	canaryMetadata := map[string]string{"roomId": officeRoomID, "meetingId": "sitting-a", "visibility": "organization", "mediaSoakCanary": "true"}
	if _, appended, err := store.appendEntry(meetingMemoryKindTranscript, "canary-transcript", "canary transcript", canaryMetadata); err != nil || !appended {
		t.Fatalf("append canary transcript appended=%v err=%v", appended, err)
	}
	window := store.unsummarizedTranscriptsAfter(10, "")
	if len(window) != 1 || window[0].ID != "real-transcript" {
		t.Fatalf("unsummarized transcript window=%+v", window)
	}
	if latest := store.latestEntryIDOfKindForRoom(meetingMemoryKindTranscript, officeRoomID); latest != "real-transcript" {
		t.Fatalf("startup latest transcript=%q", latest)
	}
	if inputs := store.unconsumedEntriesAfterForRoomForPrincipal(meetingMemoryKindTranscript, meetingMemoryKindBrain, meetingBrainCursorMetadataKey, 10, "canary-transcript", officeRoomID, RecallPrincipal{}); len(inputs) != 1 || inputs[0].ID != "real-transcript" {
		t.Fatalf("worker cursor window=%+v", inputs)
	}
	reloaded, err := newMeetingMemoryStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if baseline := reloaded.bootBaselineIDOfKindForRoom(meetingMemoryKindTranscript, officeRoomID); baseline != "real-transcript" {
		t.Fatalf("boot transcript baseline=%q", baseline)
	}
}

func TestMediaSoakFanoutRejectsUnauthorizedOrMissingDelivery(t *testing.T) {
	scope := mediaSoakScope{RoomID: "room-a", SittingID: "sitting-a", Generation: 7}
	acks := []scopedRoomDeliveryAcknowledgement{
		{SessionID: "a-1", RoomID: "room-a", SittingID: "sitting-a", MediaGeneration: 7, Authorized: true, Delivered: true},
		{SessionID: "a-2", RoomID: "room-a", SittingID: "sitting-a", MediaGeneration: 7, Authorized: true, Delivered: true},
		{SessionID: "a-3", RoomID: "room-a", SittingID: "sitting-a", MediaGeneration: 7, Authorized: true, Delivered: true},
		{SessionID: "b-1", RoomID: "room-b", SittingID: "sitting-b", MediaGeneration: 8, Authorized: false, Delivered: true},
	}
	if _, err := validateMediaSoakFanoutAcknowledgements(scope, acks); err == nil {
		t.Fatal("fanout leak to unrelated room passed acknowledgement")
	}
	acks[3].Delivered = false
	acks[2].Delivered = false
	if _, err := validateMediaSoakFanoutAcknowledgements(scope, acks); err == nil {
		t.Fatal("missing exact-scope recipient delivery passed acknowledgement")
	}
}

func TestMediaSoakTrackRegistryResidueFailsScrubCheck(t *testing.T) {
	trackID := "media-soak-adversarial-registry"
	listLock.Lock()
	wasNil := trackRooms == nil
	if wasNil {
		trackRooms = map[string]string{}
	}
	previous, existed := trackRooms[trackID]
	trackRooms[trackID] = "wrong-room"
	residue := mediaSoakTrackResidueLocked(trackID)
	if wasNil {
		trackRooms = nil
	} else if existed {
		trackRooms[trackID] = previous
	} else {
		delete(trackRooms, trackID)
	}
	listLock.Unlock()
	if !residue {
		t.Fatal("cross-room track registry residue passed scrub check")
	}
}

func TestMediaSoakAmbientDerivedEntryFailsDownstreamGate(t *testing.T) {
	store, err := newMeetingMemoryStore(filepath.Join(t.TempDir(), "meeting-memory.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	token := mediaSoakDigest("ambient-leak")
	store.mu.Lock()
	store.entries = append(store.entries, meetingMemoryEntry{ID: "derived-entry", Kind: meetingMemoryKindTranscript, Text: "derived " + token, Metadata: map[string]string{"visibility": "organization"}})
	store.mu.Unlock()
	runtime := &liveMediaSoakRuntime{app: &kanbanBoardApp{memory: store}}
	plant := mediaSoakCanaryPlant{Checks: []*mediaSoakCanaryCheck{{Surface: "chat", Token: token}}}
	if err := runtime.verifyNoCanaryDownstreamEffects(&plant, false); err == nil {
		t.Fatal("canary-derived ambient entry passed downstream-effects gate")
	}
}

func TestMediaSoakCanaryIsHiddenFromEveryNormalRecallLane(t *testing.T) {
	entry := meetingMemoryEntry{ID: "canary", Kind: meetingMemoryKindTranscript, Text: "canary", Metadata: map[string]string{"mediaSoakCanary": "true", "meetingId": "sitting-a"}}
	if !memoryEntryHiddenFromRecall(entry) {
		t.Fatal("server-owned canary was visible to normal recall")
	}
	if embeddingEligible(entry) {
		t.Fatal("server-owned canary was eligible for embeddings")
	}
	store := &meetingMemoryStore{entries: []meetingMemoryEntry{entry}}
	if entries := store.entriesOfKind(meetingMemoryKindTranscript, 0); len(entries) != 0 {
		t.Fatal("server-owned canary reached entries-of-kind worker/model lane")
	}
	if _, found := store.entryByKindAndID(meetingMemoryKindTranscript, entry.ID); found {
		t.Fatal("server-owned canary reached ordinary exact-kind reader")
	}
	if coverage := store.transcriptCoverageForMeeting("sitting-a"); coverage.Count != 0 {
		t.Fatal("server-owned canary changed transcript coverage")
	}
}

func mediaSoakTestWriter(t *testing.T) *threadSafeWriter {
	t.Helper()
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer connection.Close()
		for {
			if _, _, err := connection.ReadMessage(); err != nil {
				return
			}
		}
	}))
	connection, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = connection.Close()
		server.Close()
	})
	return &threadSafeWriter{Conn: connection}
}

func TestMediaSoakHOLFaultIsRoomScopedAndAutoReleased(t *testing.T) {
	roomMediaActors.Lock()
	previous := roomMediaActors.actors
	roomMediaActors.actors = map[string]*roomMediaActor{}
	roomMediaActors.Unlock()
	defer func() {
		roomMediaActors.Lock()
		for _, actor := range roomMediaActors.actors {
			actor.enqueue(roomMediaCommandClose)
		}
		roomMediaActors.actors = previous
		roomMediaActors.Unlock()
	}()

	actorA := actorForRoomGeneration("room-a", 1)
	actorB := actorForRoomGeneration("room-b", 2)
	binding := mediaSoakBinding{Nonce: mediaSoakDigest("nonce"),
		RoomA: mediaSoakScope{RoomID: "room-a", Generation: 1}, RoomB: mediaSoakScope{RoomID: "room-b", Generation: 2}}
	runtime := &liveMediaSoakRuntime{holBlockDuration: 20 * time.Millisecond}
	started := time.Now()
	result, err := runtime.observeHeadOfLine(context.Background(), binding)
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(started) < 20*time.Millisecond {
		t.Fatal("room A fault did not hold its bounded interval")
	}
	payload := result.(map[string]any)
	if !payload["releasedAt"].(time.Time).After(payload["blockedAt"].(time.Time)) {
		t.Fatal("HOL release was not observed")
	}
	if !actorA.enqueue(roomMediaCommandSignal) {
		t.Fatal("room A actor remained blocked after automatic cleanup")
	}
	if !actorB.enqueue(roomMediaCommandSignal) {
		t.Fatal("room B actor was blocked by room A fault")
	}
}
