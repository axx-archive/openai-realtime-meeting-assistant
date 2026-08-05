package main

import (
	"strings"
	"sync"
	"time"
)

// STRIDEScoutInvocationSurface is intentionally smaller than a transport.
// It expresses the human interaction contract without opening a websocket,
// microphone, or model session.
type STRIDEScoutInvocationSurface string

const (
	STRIDEScoutPrivate      STRIDEScoutInvocationSurface = "private"
	STRIDEScoutPublic       STRIDEScoutInvocationSurface = "public"
	STRIDEScoutProject      STRIDEScoutInvocationSurface = "project"
	STRIDEScoutMeetingText  STRIDEScoutInvocationSurface = "meeting_text"
	STRIDEScoutMeetingVoice STRIDEScoutInvocationSurface = "meeting_voice"
)

type STRIDEScoutEngagementState string

const (
	STRIDEScoutIdle      STRIDEScoutEngagementState = "idle"
	STRIDEScoutEngaged   STRIDEScoutEngagementState = "engaged"
	STRIDEScoutDismissed STRIDEScoutEngagementState = "dismissed"
)

type STRIDEScoutInvocationInput struct {
	Surface             STRIDEScoutInvocationSurface
	Text                string
	At                  time.Time
	ExplicitButton      bool
	SpokenWake          bool
	Quoted              bool
	Code                bool
	Metadata            bool
	Guest               bool
	Member              bool
	ConsentAllowed      bool
	HumanSpeechPriority bool
	// SpeakerID is the server-attributed human for a meeting-voice turn. A
	// follow-up window never crosses speakers in a shared room.
	SpeakerID string
}

type STRIDEScoutInvocationDecision struct {
	Invoke        bool
	Reason        string
	State         STRIDEScoutEngagementState
	FollowUpUntil time.Time
}

// STRIDEScoutInvocationMachine holds only an in-memory, bounded engagement
// window. It cannot make an ordinary conversation address Scout by accident.
type STRIDEScoutInvocationMachine struct {
	mu             sync.Mutex
	followUpWindow time.Duration
	state          STRIDEScoutEngagementState
	followUpUntil  time.Time
	speakerID      string
}

func NewSTRIDEScoutInvocationMachine(followUpWindow time.Duration) *STRIDEScoutInvocationMachine {
	if followUpWindow <= 0 {
		followUpWindow = 20 * time.Second
	}
	return &STRIDEScoutInvocationMachine{followUpWindow: followUpWindow, state: STRIDEScoutIdle}
}

func (machine *STRIDEScoutInvocationMachine) Evaluate(input STRIDEScoutInvocationInput) STRIDEScoutInvocationDecision {
	if machine == nil {
		return STRIDEScoutInvocationDecision{Reason: "unavailable", State: STRIDEScoutIdle}
	}
	if input.At.IsZero() {
		input.At = time.Now().UTC()
	}
	machine.mu.Lock()
	defer machine.mu.Unlock()
	if !validSTRIDEScoutInvocationSurface(input.Surface) {
		return machine.decision(false, "invalid_surface")
	}
	if input.Guest || !input.Member || !input.ConsentAllowed {
		machine.clearLocked(STRIDEScoutIdle)
		return machine.decision(false, "membership_or_consent_required")
	}
	if input.Quoted || input.Code || input.Metadata {
		return machine.decision(false, "non_addressable_content")
	}

	switch input.Surface {
	case STRIDEScoutPrivate:
		return machine.decision(true, "direct_private")
	case STRIDEScoutPublic, STRIDEScoutProject:
		if strideLexicallyMentionsScout(input.Text) {
			return machine.decision(true, "explicit_mention")
		}
		return machine.decision(false, "mention_required")
	case STRIDEScoutMeetingText:
		if input.ExplicitButton || strideLexicallyMentionsScout(input.Text) {
			return machine.decision(true, "explicit_meeting_text")
		}
		return machine.decision(false, "mention_or_button_required")
	case STRIDEScoutMeetingVoice:
		if input.HumanSpeechPriority {
			machine.clearLocked(STRIDEScoutIdle)
			return machine.decision(false, "human_barge_in")
		}
		if input.SpokenWake || input.ExplicitButton {
			machine.state = STRIDEScoutEngaged
			machine.followUpUntil = input.At.Add(machine.followUpWindow)
			machine.speakerID = strings.TrimSpace(input.SpeakerID)
			return machine.decision(true, "spoken_wake")
		}
		if machine.state == STRIDEScoutEngaged && machine.speakerID != "" && strings.TrimSpace(input.SpeakerID) == machine.speakerID && input.At.Before(machine.followUpUntil) {
			return machine.decision(true, "visible_follow_up_window")
		}
		if machine.state == STRIDEScoutEngaged {
			machine.clearLocked(STRIDEScoutIdle)
		}
		return machine.decision(false, "spoken_wake_required")
	}
	return machine.decision(false, "invalid_surface")
}

func (machine *STRIDEScoutInvocationMachine) Dismiss() {
	if machine == nil {
		return
	}
	machine.mu.Lock()
	defer machine.mu.Unlock()
	machine.clearLocked(STRIDEScoutDismissed)
}

func (machine *STRIDEScoutInvocationMachine) BargeIn() {
	if machine == nil {
		return
	}
	machine.mu.Lock()
	defer machine.mu.Unlock()
	machine.clearLocked(STRIDEScoutIdle)
}

func (machine *STRIDEScoutInvocationMachine) decision(invoke bool, reason string) STRIDEScoutInvocationDecision {
	return STRIDEScoutInvocationDecision{Invoke: invoke, Reason: reason, State: machine.state, FollowUpUntil: machine.followUpUntil}
}

func (machine *STRIDEScoutInvocationMachine) clearLocked(state STRIDEScoutEngagementState) {
	machine.state = state
	machine.followUpUntil = time.Time{}
	machine.speakerID = ""
}

func validSTRIDEScoutInvocationSurface(surface STRIDEScoutInvocationSurface) bool {
	return surface == STRIDEScoutPrivate || surface == STRIDEScoutPublic || surface == STRIDEScoutProject || surface == STRIDEScoutMeetingText || surface == STRIDEScoutMeetingVoice
}

// strideLexicallyMentionsScout delegates to the single authored-text parser
// shared with live channel mentions, keeping notification and Scout invocation
// boundaries identical across product surfaces.
func strideLexicallyMentionsScout(text string) bool {
	return scoutChatMentionsScout(text)
}

// strideVoiceMentionsScout accepts natural spoken references such as
// "What is Scout's opinion?" while still requiring Scout's name as a full
// token. Typed public-chat invocation remains stricter and continues to use
// the authored @Scout parser above.
func strideVoiceMentionsScout(text string) bool {
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9')
	})
	for _, word := range words {
		if _, mentioned := scoutWakeWords[word]; mentioned {
			return true
		}
	}
	return false
}

type STRIDEScoutResponseMode string

const (
	STRIDEScoutResponseText         STRIDEScoutResponseMode = "text"
	STRIDEScoutResponseTextGIF      STRIDEScoutResponseMode = "text_gif"
	STRIDEScoutResponseGIFOnly      STRIDEScoutResponseMode = "gif_only"
	STRIDEScoutResponseFileCard     STRIDEScoutResponseMode = "file_card"
	STRIDEScoutResponseArtifactCard STRIDEScoutResponseMode = "artifact_card"
	STRIDEScoutResponseSafeRefusal  STRIDEScoutResponseMode = "safe_refusal"
)

type STRIDEScoutResponseRequest struct {
	Member             bool
	ConsentAllowed     bool
	Sensitive          bool
	Authority          string
	Social             bool
	GIFAllowed         bool
	ChannelGIFAllowed  bool
	RequestGIFOnly     bool
	AuthorizedFile     bool
	AuthorizedArtifact bool
}

// ChooseSTRIDEScoutResponseMode applies safety and authority checks before
// personality. A GIF is an optional low-risk social response, never a way to
// sidestep audience, consent, or artifact authorization.
func ChooseSTRIDEScoutResponseMode(request STRIDEScoutResponseRequest) STRIDEScoutResponseMode {
	if !request.Member || !request.ConsentAllowed || request.Sensitive || (request.Authority != "" && request.Authority != "read_only") {
		return STRIDEScoutResponseSafeRefusal
	}
	if request.AuthorizedArtifact {
		return STRIDEScoutResponseArtifactCard
	}
	if request.AuthorizedFile {
		return STRIDEScoutResponseFileCard
	}
	if request.Social && request.GIFAllowed && request.ChannelGIFAllowed {
		if request.RequestGIFOnly {
			return STRIDEScoutResponseGIFOnly
		}
		return STRIDEScoutResponseTextGIF
	}
	return STRIDEScoutResponseText
}
