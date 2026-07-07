package main

import "testing"

// artifact.Kind is always "os_artifact", so the rerun action's old
// normalizeAgentThreadMode(artifact.Kind) silently dropped every rerun to
// workflow mode and lost the research contract. The mode metadata the launch
// stamped is the truth; Kind stays the last-resort fallback (the same
// firstNonEmptyString pattern the follow-up runner uses).
func TestRerunThreadModeUsesMetadataMode(t *testing.T) {
	for _, tt := range []struct {
		name     string
		artifact meetingMemoryEntry
		want     string
	}{
		{
			name:     "research mode survives a rerun",
			artifact: meetingMemoryEntry{Kind: "os_artifact", Metadata: map[string]string{"mode": "research"}},
			want:     "research",
		},
		{
			name:     "grill mode survives a rerun",
			artifact: meetingMemoryEntry{Kind: "os_artifact", Metadata: map[string]string{"mode": "grill"}},
			want:     "grill",
		},
		{
			name:     "missing mode falls back to workflow",
			artifact: meetingMemoryEntry{Kind: "os_artifact", Metadata: map[string]string{}},
			want:     "workflow",
		},
		{
			name:     "unknown mode falls back to workflow",
			artifact: meetingMemoryEntry{Kind: "os_artifact", Metadata: map[string]string{"mode": "nonsense"}},
			want:     "workflow",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := rerunThreadMode(tt.artifact); got != tt.want {
				t.Fatalf("rerunThreadMode=%q, want %q", got, tt.want)
			}
		})
	}
}
