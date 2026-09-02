package main

import (
	"os"
	"strings"
	"sync"
)

const (
	roomScoutVoiceModeEnv          = "BONFIRE_ROOM_SCOUT_VOICE_MODE"
	roomScoutVoiceQualificationEnv = "BONFIRE_ROOM_SCOUT_VOICE_QUALIFICATION_SHA256"
)

type roomScoutVoiceAvailability struct {
	Enabled bool   `json:"enabled"`
	Reason  string `json:"reason"`
}

// A deployment flag is not qualification evidence. The optional in-room
// voice lane may be enabled only when an independently administered adapter
// verifies that the exact opaque receipt is still current. Production does
// not install an adapter yet, so the lane remains hard off even if stale
// environment variables survive a rollback or operator copy.
var roomScoutVoiceQualification = struct {
	sync.RWMutex
	verify func(string) bool
}{}

func roomScoutVoiceQualificationCurrent(receipt string) bool {
	roomScoutVoiceQualification.RLock()
	verify := roomScoutVoiceQualification.verify
	roomScoutVoiceQualification.RUnlock()
	return verify != nil && verify(receipt)
}

// installRoomScoutVoiceQualificationVerifier is an internal assembly/test
// seam. A real production caller must wrap the externally anchored E10 receipt
// authority; echoing local configuration or checking only the digest shape is
// not sufficient.
func installRoomScoutVoiceQualificationVerifier(verify func(string) bool) func() {
	roomScoutVoiceQualification.Lock()
	previous := roomScoutVoiceQualification.verify
	roomScoutVoiceQualification.verify = verify
	roomScoutVoiceQualification.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			roomScoutVoiceQualification.Lock()
			roomScoutVoiceQualification.verify = previous
			roomScoutVoiceQualification.Unlock()
		})
	}
}

// In-room speech is an optional qualified presentation layer. Meeting capture,
// analysis, Meeting Records and explicit @Scout room-chat turns do not consult
// this gate and remain available when voice is off.
func currentRoomScoutVoiceAvailability() roomScoutVoiceAvailability {
	mode := strings.ToLower(strings.TrimSpace(os.Getenv(roomScoutVoiceModeEnv)))
	receipt := strings.ToLower(strings.TrimSpace(os.Getenv(roomScoutVoiceQualificationEnv)))
	if mode != "qualified" || !isHexDigest(receipt) {
		return roomScoutVoiceAvailability{Reason: "quality_gate_pending"}
	}
	roomScoutVoiceQualification.RLock()
	verifierInstalled := roomScoutVoiceQualification.verify != nil
	roomScoutVoiceQualification.RUnlock()
	if !verifierInstalled {
		return roomScoutVoiceAvailability{Reason: "trusted_qualification_unavailable"}
	}
	if !roomScoutVoiceQualificationCurrent(receipt) {
		return roomScoutVoiceAvailability{Reason: "qualification_not_current"}
	}
	return roomScoutVoiceAvailability{Enabled: true, Reason: "qualified"}
}

// roomScoutTextAvailability is the chat-only Scout seat (Wave 6 D7): @Scout
// answered through the server-owned room-chat path with no realtime lane and
// no provider audio. It is reported separately from the voice gate so the
// client can offer a text-only invite while voice stays unqualified.
type roomScoutTextAvailability struct {
	Enabled bool   `json:"enabled"`
	Status  string `json:"status"`
	Reason  string `json:"reason,omitempty"`
	Model   string `json:"model,omitempty"`
}

// currentRoomScoutTextAvailability never consults the voice qualification:
// the typed path is OpenAI-owned and needs only the provider key that the
// existing @Scout room-chat turn already requires.
func currentRoomScoutTextAvailability() roomScoutTextAvailability {
	if strings.TrimSpace(os.Getenv("OPENAI_API_KEY")) == "" {
		return roomScoutTextAvailability{Status: "degraded", Reason: "provider_not_configured"}
	}
	return roomScoutTextAvailability{Enabled: true, Status: "available", Model: scoutChatModel()}
}

// snapshot is the /readyz projection of the text seat.
func (availability roomScoutTextAvailability) snapshot() map[string]any {
	snapshot := map[string]any{"status": availability.Status, "enabled": availability.Enabled}
	if availability.Reason != "" {
		snapshot["reason"] = availability.Reason
	}
	if availability.Model != "" {
		snapshot["model"] = availability.Model
	}
	return snapshot
}
