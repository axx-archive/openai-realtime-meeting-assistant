package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/pion/webrtc/v4"
)

const mediaSoakCanaryTTL = 5 * time.Minute
const mediaSoakCanaryDwell = 250 * time.Millisecond

type mediaSoakCanaryPlant struct {
	ExpiresAt time.Time
	PlantedAt time.Time
	Checks    []*mediaSoakCanaryCheck
}

type mediaSoakCanaryCheck struct {
	Surface, Direction, Sentinel  string
	Source, Observed              mediaSoakScope
	ExpectedPresent               bool
	Token                         string
	EntryID                       string
	Track                         *webrtc.TrackLocalStaticRTP
	Scout                         *roomRealtimeBundle
	PublicationRecipients         []string
	DeletionRecipients            []string
	IngressAck, ReadAck, ScrubAck bool
	ObservedValue                 bool
	ResidueCount                  int
}

func (runtimeObserver *liveMediaSoakRuntime) plantCanaries(binding mediaSoakBinding) (any, error) {
	if runtimeObserver.app == nil || runtimeObserver.app.memory == nil || runtimeObserver.app != kanbanApp {
		return nil, errors.New("media-soak production stores are unavailable")
	}
	runtimeObserver.canaryMu.Lock()
	defer runtimeObserver.canaryMu.Unlock()
	if runtimeObserver.canaries == nil {
		runtimeObserver.canaries = map[string]mediaSoakCanaryPlant{}
	}
	now := time.Now().UTC()
	if _, exists := runtimeObserver.canaries[binding.Nonce]; exists {
		return nil, errors.New("media-soak canaries already planted")
	}
	surfaces := []string{"track", "chat", "scout", "transcript", "recap", "artifact"}
	sentinels := []string{"current", "prior_sitting", "prior_generation", "unrelated_room"}
	plant := mediaSoakCanaryPlant{ExpiresAt: now.Add(mediaSoakCanaryTTL), PlantedAt: now, Checks: make([]*mediaSoakCanaryCheck, 0, 48)}
	for _, direction := range []string{"a_to_b", "b_to_a"} {
		source, unrelated := binding.RoomA, binding.RoomB
		if direction == "b_to_a" {
			source, unrelated = binding.RoomB, binding.RoomA
		}
		for _, surface := range surfaces {
			for _, sentinel := range sentinels {
				observed := source
				switch sentinel {
				case "prior_sitting":
					observed.SittingID = "media-soak-prior-" + observed.SittingDigest[:12]
					observed.SittingDigest = mediaSoakDigest(direction + ":" + surface + ":prior-sitting")
				case "prior_generation":
					observed.Generation++
					observed.MediaGenerationDigest = mediaSoakDigest(direction + ":" + surface + ":prior-generation")
				case "unrelated_room":
					// Change only the room axis: sitting and generation keep the
					// source values so another fence cannot mask a room leak.
					observed.RoomID = unrelated.RoomID
					observed.RoomDigest = unrelated.RoomDigest
					observed.RecipientEmail = unrelated.RecipientEmail
				}
				token := mediaSoakDigest(strings.Join([]string{binding.Nonce, surface, direction, sentinel}, ":"))
				check := &mediaSoakCanaryCheck{Surface: surface, Direction: direction, Sentinel: sentinel,
					Source: source, Observed: observed, ExpectedPresent: sentinel == "current", Token: token}
				if err := runtimeObserver.plantCanary(check); err != nil {
					plant.Checks = append(plant.Checks, check)
					_ = runtimeObserver.scrubCanaryPlant(&plant)
					return nil, fmt.Errorf("media-soak %s ingress was not acknowledged: %w", surface, err)
				}
				plant.Checks = append(plant.Checks, check)
			}
		}
	}
	runtimeObserver.canaries[binding.Nonce] = plant
	time.AfterFunc(mediaSoakCanaryTTL, func() {
		runtimeObserver.canaryMu.Lock()
		defer runtimeObserver.canaryMu.Unlock()
		current, ok := runtimeObserver.canaries[binding.Nonce]
		if !ok || current.ExpiresAt.After(time.Now().UTC()) {
			return
		}
		if err := runtimeObserver.scrubCanaryPlant(&current); err != nil {
			return
		}
		delete(runtimeObserver.canaries, binding.Nonce)
	})
	return map[string]any{"planted": true, "checkCount": len(plant.Checks), "expiresAt": plant.ExpiresAt, "ingressAcknowledged": true}, nil
}

func (runtimeObserver *liveMediaSoakRuntime) plantCanary(check *mediaSoakCanaryCheck) error {
	metadata := map[string]string{
		"mediaSoakCanary": "true", "mediaSoakSurface": check.Surface, "mediaSoakToken": check.Token,
		"visibility": "room_only", "roomId": check.Source.RoomID, "meetingId": check.Source.SittingID, "sittingId": check.Source.SittingID,
		"mediaGeneration": strconv.FormatUint(check.Source.Generation, 10),
	}
	switch check.Surface {
	case "track":
		track, err := webrtc.NewTrackLocalStaticRTP(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeVP8}, "w2a-"+check.Token[:24], "w2a-"+check.Token[24:48])
		if err != nil {
			return err
		}
		listLock.Lock()
		trackLocals[track.ID()] = track
		trackParticipants[track.ID()] = "media-soak-" + check.Token[:12]
		trackParticipantSessions[track.ID()] = "media-soak-" + check.Token[12:24]
		trackRooms[track.ID()] = check.Source.RoomID
		trackSourceIDs[track.ID()] = "media-soak-" + check.Token[24:36]
		trackLayerRIDs[track.ID()] = ""
		trackLayerGroups[track.ID()] = "media-soak-" + check.Token[36:48]
		trackMediaOwners[track.ID()] = trackMediaOwner{track: track, generation: check.Source.Generation, sittingID: check.Source.SittingID}
		listLock.Unlock()
		check.Track = track
	case "chat":
		payload, ok := runtimeObserver.app.recordRoomChatMessageForMeeting(check.Source.RoomID, "Media Soak", check.Token, metadata, check.Source.SittingID)
		if !ok {
			return errors.New("chat ingress rejected")
		}
		check.EntryID, _ = payload["id"].(string)
		recipients, err := acknowledgeMediaSoakFanout(check.Source, "room_chat", payload)
		if err != nil {
			return fmt.Errorf("chat fanout was not acknowledged: %w", err)
		}
		check.PublicationRecipients = recipients
	case "scout":
		runtimeObserver.app.mu.Lock()
		bundle := runtimeObserver.app.roomLiveLocked(check.Source.RoomID).realtime
		runtimeObserver.app.mu.Unlock()
		if bundle == nil || !bundle.scope.same(check.Source.roomScoutScope()) || !bundle.publishFenced(check.Source.roomScoutScope(), "media_soak_canary", check.Token) {
			return errors.New("live Scout publish callback rejected")
		}
		check.Scout = bundle
	case "transcript":
		entry, appended, err := runtimeObserver.app.memory.appendAttributedTranscriptEntry(check.Source.RoomID, "media-soak-transcript-"+check.Token, "", "Media Soak", "", check.Token, metadata, true, check.Source.SittingID)
		if err != nil || !appended {
			return errors.New("transcript commit rejected")
		}
		check.EntryID = entry.ID
	case "recap":
		payload, ok := runtimeObserver.app.publishMeetingRecapToRoom(check.Source.RoomID, check.Source.SittingID, check.Token, metadata)
		if !ok {
			return errors.New("recap publication rejected")
		}
		check.EntryID, _ = payload["id"].(string)
		recipients, err := acknowledgeMediaSoakFanout(check.Source, "room_chat", payload)
		if err != nil {
			return fmt.Errorf("recap fanout was not acknowledged: %w", err)
		}
		check.PublicationRecipients = recipients
	case "artifact":
		metadata["ownerEmail"] = check.Source.RecipientEmail
		entry, appended, acks, err := runtimeObserver.app.createOSArtifactWithMetadataAcknowledged("artifacts", check.Token, check.Token, check.Source.RecipientEmail, metadata)
		if err != nil || !appended {
			return errors.New("artifact create rejected")
		}
		check.EntryID = entry.ID
		recipients, err := validateMediaSoakFanoutAcknowledgements(check.Source, acks)
		if err != nil {
			return fmt.Errorf("artifact fanout was not acknowledged: %w", err)
		}
		check.PublicationRecipients = recipients
	default:
		return errors.New("unsupported canary surface")
	}
	if check.Surface != "track" && check.Surface != "scout" && check.EntryID == "" {
		return errors.New("durable ingress omitted entry acknowledgement")
	}
	check.IngressAck = true
	return nil
}

func (scope mediaSoakScope) roomScoutScope() RoomScoutScope {
	return RoomScoutScope{RoomID: scope.RoomID, SittingID: scope.SittingID, MediaGeneration: scope.Generation}
}

func acknowledgeMediaSoakFanout(scope mediaSoakScope, event string, payload any) ([]string, error) {
	acks, err := broadcastScopedRoomKanbanEventAcknowledged(scope.roomScoutScope(), event, payload)
	if err != nil {
		return nil, err
	}
	return validateMediaSoakFanoutAcknowledgements(scope, acks)
}

func validateMediaSoakFanoutAcknowledgements(scope mediaSoakScope, acks []scopedRoomDeliveryAcknowledgement) ([]string, error) {
	authorized := make([]string, 0, 3)
	seenSessions := map[string]struct{}{}
	for _, ack := range acks {
		if ack.Delivered && !ack.Authorized {
			return nil, errors.New("fanout reached an unrelated recipient")
		}
		if !ack.Authorized {
			continue
		}
		if !ack.Delivered || ack.RoomID != normalizeRoomID(scope.RoomID) || ack.SittingID != scope.SittingID || ack.MediaGeneration != scope.Generation || strings.TrimSpace(ack.SessionID) == "" {
			return nil, errors.New("authorized recipient delivery was not acknowledged at the exact scope")
		}
		if _, duplicate := seenSessions[ack.SessionID]; duplicate {
			return nil, errors.New("fanout acknowledgement repeated a recipient session")
		}
		seenSessions[ack.SessionID] = struct{}{}
		authorized = append(authorized, mediaSoakDigest(ack.SessionID))
	}
	if len(authorized) < 3 {
		return nil, fmt.Errorf("fanout acknowledged %d exact recipients, want at least 3", len(authorized))
	}
	sort.Strings(authorized)
	return authorized, nil
}

func (runtimeObserver *liveMediaSoakRuntime) observeCanaries(binding mediaSoakBinding) (any, error) {
	runtimeObserver.canaryMu.Lock()
	defer runtimeObserver.canaryMu.Unlock()
	plant, ok := runtimeObserver.canaries[binding.Nonce]
	if !ok || !plant.ExpiresAt.After(time.Now().UTC()) {
		return nil, errors.New("media-soak canaries are absent or expired")
	}
	if remaining := mediaSoakCanaryDwell - time.Since(plant.PlantedAt); remaining > 0 {
		time.Sleep(remaining)
	}
	if err := runtimeObserver.verifyNoCanaryDownstreamEffects(&plant, true); err != nil {
		return nil, err
	}
	for _, check := range plant.Checks {
		observed, err := runtimeObserver.readCanary(check)
		// Visibility is defined solely by whether the exact canary ID came back.
		// Validate its server-owned metadata independently so malformed metadata
		// cannot turn an observed cross-scope leak into an apparent miss.
		check.ObservedValue = observed
		if err != nil {
			return nil, fmt.Errorf("media-soak %s read was not acknowledged: %w", check.Surface, err)
		}
		check.ReadAck = true
	}
	runtimeObserver.canaries[binding.Nonce] = plant
	return map[string]any{"observed": true, "readAcknowledged": true, "checkCount": len(plant.Checks)}, nil
}

func (runtimeObserver *liveMediaSoakRuntime) readCanary(check *mediaSoakCanaryCheck) (bool, error) {
	switch check.Surface {
	case "track":
		if check.Track == nil {
			return false, errors.New("track registry entry is absent")
		}
		listLock.RLock()
		present := trackLocals[check.Track.ID()] == check.Track
		accepted := peerConnectionState{participantName: "media-soak-recipient", roomID: check.Observed.RoomID, sittingID: check.Observed.SittingID, mediaGeneration: check.Observed.Generation}.acceptsTrack(check.Track)
		listLock.RUnlock()
		if !present {
			return false, errors.New("track registry read missed planted track")
		}
		return accepted, nil
	case "scout":
		if check.Scout == nil {
			return false, errors.New("live Scout canary runtime is absent")
		}
		value := check.Token + ":read"
		accepted := check.Scout.publishFenced(check.Observed.roomScoutScope(), "media_soak_canary", value)
		return accepted, nil
	case "chat", "transcript", "recap", "artifact":
		principal := mediaSoakRecallPrincipal(check.Observed)
		entry, found := runtimeObserver.app.mediaSoakCanaryEntryForPrincipal(context.Background(), principal, check.EntryID)
		if found {
			if err := validateReturnedMediaSoakCanary(entry, check); err != nil {
				return true, err
			}
		}
		return found, nil
	default:
		return false, errors.New("unsupported canary surface")
	}
}

func mediaSoakRecallPrincipal(scope mediaSoakScope) RecallPrincipal {
	user := accountStore().findUser(scope.RecipientEmail)
	if user == nil {
		user = &userAccount{Email: normalizeAccountEmail(scope.RecipientEmail), Name: scope.RecipientEmail}
	}
	return RecallPrincipal{User: user, TenantID: canonicalArtifactTenantID(), RoomID: normalizeRoomID(scope.RoomID), SittingID: scope.SittingID, MediaGeneration: scope.Generation, Audience: "private"}
}

func validateReturnedMediaSoakCanary(entry meetingMemoryEntry, check *mediaSoakCanaryCheck) error {
	if entry.ID != check.EntryID || entry.Text == "" || entry.Metadata["mediaSoakCanary"] != "true" || entry.Metadata["mediaSoakSurface"] != check.Surface ||
		entry.Metadata["mediaSoakToken"] != check.Token || normalizeRoomID(entry.Metadata["roomId"]) != normalizeRoomID(check.Source.RoomID) ||
		firstNonEmptyString(entry.Metadata["sittingId"], entry.Metadata["meetingId"]) != check.Source.SittingID ||
		entry.Metadata["mediaGeneration"] != strconv.FormatUint(check.Source.Generation, 10) || entry.Metadata["visibility"] != "room_only" {
		return errors.New("authorized reader returned a canary with invalid server-owned metadata")
	}
	switch check.Surface {
	case "chat", "recap":
		if entry.Kind != meetingMemoryKindTranscript || entry.Metadata["source"] != transcriptSourceRoomChat {
			return errors.New("room publication reader returned the wrong entry kind")
		}
	case "transcript":
		if entry.Kind != meetingMemoryKindTranscript {
			return errors.New("transcript reader returned the wrong entry kind")
		}
	case "artifact":
		if entry.Kind != meetingMemoryKindOSArtifact {
			return errors.New("artifact reader returned the wrong entry kind")
		}
	}
	return nil
}

func (runtimeObserver *liveMediaSoakRuntime) scrubCanaries(binding mediaSoakBinding) (any, error) {
	runtimeObserver.canaryMu.Lock()
	defer runtimeObserver.canaryMu.Unlock()
	plant, existed := runtimeObserver.canaries[binding.Nonce]
	if !existed {
		return nil, errors.New("media-soak canaries are absent")
	}
	for _, check := range plant.Checks {
		if !check.IngressAck || !check.ReadAck {
			return nil, errors.New("media-soak canary ingress/read acknowledgement is incomplete")
		}
	}
	if err := runtimeObserver.scrubCanaryPlant(&plant); err != nil {
		return nil, err
	}
	delete(runtimeObserver.canaries, binding.Nonce)
	checks := make([]map[string]any, 0, len(plant.Checks))
	residue := 0
	for _, check := range plant.Checks {
		residue += check.ResidueCount
		checks = append(checks, check.evidence())
	}
	return map[string]any{"scrubbed": true, "existed": true, "residueCount": residue, "checks": checks}, nil
}

func (runtimeObserver *liveMediaSoakRuntime) scrubCanaryPlant(plant *mediaSoakCanaryPlant) error {
	entryIDs := make([]string, 0, len(plant.Checks))
	for _, check := range plant.Checks {
		if check.EntryID == "" {
			continue
		}
		if check.Surface == "artifact" {
			_, acks, deleted, deleteErr := runtimeObserver.app.deleteEntryByIDAcknowledged(check.EntryID)
			recipients, acknowledgementErr := validateMediaSoakFanoutAcknowledgements(check.Source, acks)
			check.DeletionRecipients = recipients
			if deleteErr != nil || !deleted || acknowledgementErr != nil || !equalMediaSoakRecipients(recipients, check.PublicationRecipients) {
				return fmt.Errorf("media-soak artifact delete acknowledgement mismatch deleted=%t: delete=%v acknowledge=%v", deleted, deleteErr, acknowledgementErr)
			}
		} else {
			entryIDs = append(entryIDs, check.EntryID)
		}
	}
	removed, err := runtimeObserver.app.memory.deleteEntriesByID(entryIDs)
	if err != nil || removed != len(entryIDs) {
		return fmt.Errorf("media-soak durable scrub acknowledgement mismatch removed=%d expected=%d: %w", removed, len(entryIDs), err)
	}
	for _, check := range plant.Checks {
		if check.Track != nil {
			listLock.Lock()
			delete(trackLocals, check.Track.ID())
			delete(trackParticipants, check.Track.ID())
			delete(trackParticipantSessions, check.Track.ID())
			delete(trackRooms, check.Track.ID())
			delete(trackSourceIDs, check.Track.ID())
			delete(trackLayerRIDs, check.Track.ID())
			delete(trackLayerGroups, check.Track.ID())
			delete(trackMediaOwners, check.Track.ID())
			trackResidue := mediaSoakTrackResidueLocked(check.Track.ID())
			listLock.Unlock()
			if trackResidue {
				check.ResidueCount++
			}
		}
		if check.Scout != nil {
			runtimeObserver.app.mu.Lock()
			liveBundle := runtimeObserver.app.roomLiveLocked(check.Source.RoomID).realtime
			runtimeObserver.app.mu.Unlock()
			if liveBundle != check.Scout || !check.Scout.publishFenced(check.Source.roomScoutScope(), "media_soak_canary_scrub", check.Token+":scrub") {
				check.ResidueCount++
			}
		}
		if check.EntryID != "" {
			if _, remains := runtimeObserver.app.memory.entryByID(check.EntryID); remains {
				check.ResidueCount++
			}
			if check.Surface == "chat" || check.Surface == "recap" {
				recipients, broadcastErr := acknowledgeMediaSoakFanout(check.Source, "room_chat_delete", map[string]any{"id": check.EntryID, "roomId": check.Source.RoomID})
				check.DeletionRecipients = recipients
				if broadcastErr != nil || !equalMediaSoakRecipients(recipients, check.PublicationRecipients) {
					check.ResidueCount++
				}
			}
		}
		check.ScrubAck = check.ResidueCount == 0
	}
	if err := runtimeObserver.verifyNoCanaryDownstreamEffects(plant, false); err != nil {
		return err
	}
	for _, check := range plant.Checks {
		if !check.ScrubAck {
			return errors.New("media-soak canary scrub left residue")
		}
	}
	return nil
}

func mediaSoakTrackResidueLocked(trackID string) bool {
	_, localRemains := trackLocals[trackID]
	_, participantRemains := trackParticipants[trackID]
	_, sessionRemains := trackParticipantSessions[trackID]
	_, roomRemains := trackRooms[trackID]
	_, sourceRemains := trackSourceIDs[trackID]
	_, ridRemains := trackLayerRIDs[trackID]
	_, groupRemains := trackLayerGroups[trackID]
	_, ownerRemains := trackMediaOwners[trackID]
	return localRemains || participantRemains || sessionRemains || roomRemains || sourceRemains || ridRemains || groupRemains || ownerRemains
}

func equalMediaSoakRecipients(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func mediaSoakRecipientSetDigest(recipients []string) string {
	if len(recipients) == 0 {
		return ""
	}
	return mediaSoakDigest(strings.Join(recipients, ","))
}

func (runtimeObserver *liveMediaSoakRuntime) verifyNoCanaryDownstreamEffects(plant *mediaSoakCanaryPlant, durableExpected bool) error {
	if runtimeObserver.app == nil || runtimeObserver.app.memory == nil {
		return errors.New("media-soak durable store is unavailable")
	}
	expected := map[string]*mediaSoakCanaryCheck{}
	needles := make([]string, 0, len(plant.Checks)*2)
	for _, check := range plant.Checks {
		needles = append(needles, check.Token)
		if check.EntryID != "" {
			expected[check.EntryID] = check
			needles = append(needles, check.EntryID)
		}
	}
	found := map[string]bool{}
	runtimeObserver.app.memory.mu.Lock()
	entries := cloneMemoryEntries(runtimeObserver.app.memory.entries)
	runtimeObserver.app.memory.mu.Unlock()
	for _, entry := range entries {
		if check := expected[entry.ID]; check != nil {
			if !durableExpected {
				return errors.New("media-soak durable entry survived scrub")
			}
			found[entry.ID] = true
			if !memoryEntryHiddenFromRecall(entry) || embeddingEligible(entry) {
				return errors.New("media-soak canary entered a normal recall or embedding lane")
			}
			continue
		}
		for _, needle := range needles {
			if strings.Contains(entry.Text, needle) {
				return errors.New("media-soak canary produced a downstream durable entry")
			}
			for _, value := range entry.Metadata {
				if strings.Contains(value, needle) {
					return errors.New("media-soak canary produced downstream durable metadata")
				}
			}
		}
	}
	if durableExpected && len(found) != len(expected) {
		return fmt.Errorf("media-soak durable canary set is incomplete got=%d want=%d", len(found), len(expected))
	}
	for _, entry := range runtimeObserver.app.memory.snapshot(0) {
		if expected[entry.ID] != nil {
			return errors.New("media-soak canary reached the normal snapshot/model lane")
		}
	}
	for _, artifact := range runtimeObserver.app.osArtifactsSnapshot(0) {
		if expected[artifact.ID] != nil {
			return errors.New("media-soak canary reached the normal artifact reader")
		}
	}
	if idx := loadedEmbeddingIndex(); idx != nil {
		idx.mu.RLock()
		for id := range expected {
			if _, indexed := idx.byID[id]; indexed {
				idx.mu.RUnlock()
				return errors.New("media-soak canary reached the in-memory embedding index")
			}
		}
		indexPath := idx.path
		idx.mu.RUnlock()
		if indexPath != "" {
			if data, err := os.ReadFile(indexPath); err == nil {
				for id := range expected {
					if strings.Contains(string(data), id) {
						return errors.New("media-soak canary reached the durable embedding index")
					}
				}
			} else if !os.IsNotExist(err) {
				return fmt.Errorf("media-soak embedding residue check: %w", err)
			}
		}
	}
	if durableExpected {
		for _, check := range plant.Checks {
			if check.EntryID == "" {
				continue
			}
			if matches := runtimeObserver.app.memory.search(check.Token, 10); len(matches) != 0 {
				return errors.New("media-soak canary reached lexical/model recall")
			}
			if contextEntries := runtimeObserver.app.memory.contextEntriesForQuery(check.Token, 10, time.Now().UTC()); len(contextEntries) != 0 {
				for _, entry := range contextEntries {
					if expected[entry.ID] != nil {
						return errors.New("media-soak canary reached ambient agent context")
					}
				}
			}
		}
	}
	return nil
}

func (check *mediaSoakCanaryCheck) evidence() map[string]any {
	return map[string]any{
		"at": time.Now().UTC(), "surface": check.Surface, "direction": check.Direction, "sentinel": check.Sentinel,
		"sourceRoomDigest": check.Source.RoomDigest, "observedRoomDigest": check.Observed.RoomDigest,
		"sourceSittingDigest": check.Source.SittingDigest, "observedSittingDigest": check.Observed.SittingDigest,
		"sourceGenerationDigest": check.Source.MediaGenerationDigest, "observedGenerationDigest": check.Observed.MediaGenerationDigest,
		"expectedPresent": check.ExpectedPresent, "observed": check.ObservedValue,
		"publicationRecipientSetDigest": mediaSoakRecipientSetDigest(check.PublicationRecipients), "deletionRecipientSetDigest": mediaSoakRecipientSetDigest(check.DeletionRecipients),
		"publicationRecipientCount": len(check.PublicationRecipients), "deletionRecipientCount": len(check.DeletionRecipients),
		"ingressAcknowledged": check.IngressAck, "readAcknowledged": check.ReadAck, "scrubAcknowledged": check.ScrubAck, "residueCount": check.ResidueCount,
	}
}
