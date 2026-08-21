package main

import "testing"

func TestScoutChatWorkRefsCarryProcessIdentityForNativePresentation(t *testing.T) {
	thread := scoutAgentThread{
		ID:     "packaging-run",
		Mode:   "goal",
		Query:  "Build the presentation",
		Status: "running",
		Artifact: meetingMemoryEntry{
			ID: "packaging-parent",
			Metadata: map[string]string{
				"processId":       packagingStudioProcessID,
				"currentStage":    "execute",
				"progressPercent": "11",
			},
		},
	}
	ref := scoutChatThreadRefForAgent(thread, STRIDEProductAgentContextProfile{AgentID: "scout", DisplayName: "Scout"}, "")
	if ref == nil || ref.ProcessID != packagingStudioProcessID {
		t.Fatalf("agent ref processId=%q, want %q", ref.ProcessID, packagingStudioProcessID)
	}
	replay := conversationWorkReplayCard(scoutChatMessageRecord{ID: "launch-message"}, thread)
	if replay.Thread == nil || replay.Thread.ProcessID != packagingStudioProcessID {
		t.Fatalf("replay ref processId=%q, want %q", replay.Thread.ProcessID, packagingStudioProcessID)
	}
}
