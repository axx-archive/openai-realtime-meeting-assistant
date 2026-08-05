package main

// This file binds a live meeting specialist to the same durable coworker
// identity shown in Marketplace and direct chat. The provider still receives
// only a server-authorized brief; identity data never grants room, file, tool,
// or provider authority.

import "strings"

const (
	coltonMeetingSpecialistAgentID = "agent_colton-research"
	coltonMeetingSpecialistVoice   = "cedar"
)

// MeetingSpecialistRealtimeIdentity is the bounded, inspectable personality
// projection included in a specialist's provider brief. ProfileRevision binds
// it to the exact approved Product/Workforce overlay. IdentityDigest detects
// accidental or stale mutations before a model session can start.
type MeetingSpecialistRealtimeIdentity struct {
	AgentID            string                         `json:"agentId"`
	DisplayName        string                         `json:"displayName"`
	RoleTitle          string                         `json:"roleTitle"`
	PersonalitySummary string                         `json:"personalitySummary"`
	VoiceSummary       string                         `json:"voiceSummary"`
	WorkingStyle       string                         `json:"workingStyle"`
	PersonalityNotes   string                         `json:"personalityNotes"`
	MemoryPolicy       string                         `json:"memoryPolicy"`
	CoreMemories       []STRIDEProductAgentCoreMemory `json:"coreMemories"`
	ActiveLearning     []STRIDEProductAgentLearning   `json:"activeLearning,omitempty"`
	ProfileRevision    STRIDEReference                `json:"profileRevision"`
	IdentityDigest     string                         `json:"identityDigest"`
}

type meetingSpecialistRealtimeIdentityMaterial struct {
	AgentID            string
	DisplayName        string
	RoleTitle          string
	PersonalitySummary string
	VoiceSummary       string
	WorkingStyle       string
	PersonalityNotes   string
	MemoryPolicy       string
	CoreMemories       []STRIDEProductAgentCoreMemory
	ActiveLearning     []STRIDEProductAgentLearning
	ProfileRevision    STRIDEReference
}

func (identity MeetingSpecialistRealtimeIdentity) material() meetingSpecialistRealtimeIdentityMaterial {
	return meetingSpecialistRealtimeIdentityMaterial{
		AgentID: identity.AgentID, DisplayName: identity.DisplayName, RoleTitle: identity.RoleTitle,
		PersonalitySummary: identity.PersonalitySummary, VoiceSummary: identity.VoiceSummary,
		WorkingStyle: identity.WorkingStyle, PersonalityNotes: identity.PersonalityNotes,
		MemoryPolicy:    identity.MemoryPolicy,
		CoreMemories:    append([]STRIDEProductAgentCoreMemory(nil), identity.CoreMemories...),
		ActiveLearning:  append([]STRIDEProductAgentLearning(nil), identity.ActiveLearning...),
		ProfileRevision: identity.ProfileRevision,
	}
}

// meetingSpecialistRealtimeIdentityFromProfile projects only the current,
// reviewed coworker profile. Relationship memories and meeting/chat bodies stay
// in their independently ACL-bound references; they are never copied here.
func meetingSpecialistRealtimeIdentityFromProfile(profile STRIDEProductAgentContextProfile, revision STRIDEReference) (MeetingSpecialistRealtimeIdentity, error) {
	identity := MeetingSpecialistRealtimeIdentity{
		AgentID: profile.AgentID, DisplayName: strings.TrimSpace(profile.DisplayName), RoleTitle: strings.TrimSpace(profile.RoleTitle),
		PersonalitySummary: strings.TrimSpace(profile.PersonalitySummary), VoiceSummary: strings.TrimSpace(profile.VoiceSummary),
		WorkingStyle: strings.TrimSpace(profile.WorkingStyle), PersonalityNotes: strings.TrimSpace(profile.PersonalityNotes),
		MemoryPolicy:    strings.TrimSpace(profile.MemoryPolicy),
		CoreMemories:    append([]STRIDEProductAgentCoreMemory(nil), profile.CoreMemories...),
		ActiveLearning:  append([]STRIDEProductAgentLearning(nil), profile.ActiveLearning...),
		ProfileRevision: revision,
	}
	digest, err := STRIDEContractDigest(identity.material())
	if err != nil {
		return MeetingSpecialistRealtimeIdentity{}, ErrMeetingSpecialistProviderConfig
	}
	identity.IdentityDigest = digest
	if identity.validate(profile.AgentID, revision) != nil {
		return MeetingSpecialistRealtimeIdentity{}, ErrMeetingSpecialistProviderConfig
	}
	return identity, nil
}

func (identity MeetingSpecialistRealtimeIdentity) validate(agentID string, revision STRIDEReference) error {
	if !strideIdentifier(identity.AgentID) || identity.AgentID != agentID || strings.TrimSpace(identity.DisplayName) == "" || strings.TrimSpace(identity.RoleTitle) == "" ||
		strings.TrimSpace(identity.PersonalitySummary) == "" || strings.TrimSpace(identity.VoiceSummary) == "" || strings.TrimSpace(identity.WorkingStyle) == "" ||
		strings.TrimSpace(identity.PersonalityNotes) == "" || strings.TrimSpace(identity.MemoryPolicy) == "" || len(identity.CoreMemories) < 2 ||
		identity.ProfileRevision != revision || revision.Validate() != nil || !isHexDigest(identity.IdentityDigest) || validateSTRIDEProductCoreMemories(identity.CoreMemories) != nil {
		return ErrMeetingSpecialistProviderConfig
	}
	digest, err := STRIDEContractDigest(identity.material())
	if err != nil || digest != identity.IdentityDigest {
		return ErrMeetingSpecialistProviderConfig
	}
	for _, learning := range identity.ActiveLearning {
		if !oneOf(learning.Status, "reviewed", "corrected") || strings.TrimSpace(learning.Summary) == "" {
			return ErrMeetingSpecialistProviderConfig
		}
	}
	return nil
}

// First-party team agents always use their current durable identity. Legacy
// test/third-party seats remain compatible until their package contract adopts
// the same projection.
func meetingSpecialistIdentityRequired(agentID string) bool {
	return strings.HasPrefix(strings.TrimSpace(agentID), "agent_")
}

// Colton's voice is intentionally different from Scout's marin voice. The
// chosen voice is part of the external provider qualification subject, so this
// policy cannot silently switch a live session after qualification.
func meetingSpecialistVoiceMatchesIdentity(agentID, voice string) bool {
	if strings.TrimSpace(agentID) == coltonMeetingSpecialistAgentID {
		return strings.TrimSpace(voice) == coltonMeetingSpecialistVoice
	}
	return strings.TrimSpace(voice) != ""
}
