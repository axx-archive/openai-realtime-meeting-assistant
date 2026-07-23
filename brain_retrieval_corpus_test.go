package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBrainRetrievalNinetyDayFortyMeetingCorpusIsExhaustive(t *testing.T) {
	start := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(90 * 24 * time.Hour)
	temporal, err := NewBoundedTemporalQuery(TemporalExplicitRange, start, end, "America/Los_Angeles", "", "", "ninety day release corpus")
	if err != nil {
		t.Fatal(err)
	}
	const meetings = 40
	const entriesPerMeeting = 7
	path := filepath.Join(t.TempDir(), "meeting-memory.jsonl")
	store, err := newMeetingMemoryStore(path)
	if err != nil {
		t.Fatal(err)
	}
	principal := ACLPrincipal{TenantID: "tenant-a", ID: "aj@shareability.com", Kind: ACLPrincipalUser, TeamIDs: []string{"organization"}}
	entries := make([]meetingMemoryEntry, 0, meetings*entriesPerMeeting)
	acl := &MemoryACLStore{Objects: make(map[string]ACLObject, meetings*entriesPerMeeting), Grants: make(map[string][]ACLGrant, meetings*entriesPerMeeting)}
	for meeting := 0; meeting < meetings; meeting++ {
		meetingAt := start.Add(time.Duration(meeting) * (89 * 24 * time.Hour) / (meetings - 1))
		for line := 0; line < entriesPerMeeting; line++ {
			index := meeting*entriesPerMeeting + line
			body := fmt.Sprintf("meeting %02d line %02d source-bound operating evidence", meeting, line)
			if meeting == 0 && line == 0 {
				body = strings.Repeat("multi chunk primary evidence ", 20)
			}
			occurred := meetingAt.Add(time.Duration(line) * time.Minute)
			entry := meetingMemoryEntry{ID: fmt.Sprintf("source-%03d", index), Kind: meetingMemoryKindTranscript, Text: body, CreatedAt: occurred.Add(time.Second), Metadata: map[string]string{
				"roomId": fmt.Sprintf("room-%d", meeting%5), "meetingId": fmt.Sprintf("sitting-%02d", meeting), "speaker": "AJ",
				"captureSequence": fmt.Sprint(index + 1), "capturedAt": occurred.Add(time.Second).Format(time.RFC3339Nano),
				"occurredStart": occurred.Format(time.RFC3339Nano), "occurredEnd": occurred.Add(30 * time.Second).Format(time.RFC3339Nano), "source": transcriptSourceRoomChat,
			}}
			entries = append(entries, entry)
			ref := ACLObjectRef{TenantID: principal.TenantID, Type: "memory", ID: entry.ID, ACLVersion: 2}
			acl.Objects[aclObjectKey(ref)] = ACLObject{Ref: ref, RoomID: entry.Metadata["roomId"], SittingID: entry.Metadata["meetingId"], CurrentContentRevision: 1, CurrentContentDigest: digestBrainString(body)}
			acl.Grants[aclObjectKey(ref)] = []ACLGrant{{ID: "grant-" + entry.ID, TenantID: ref.TenantID, ObjectType: ref.Type, ObjectID: ref.ID, ACLVersion: ref.ACLVersion,
				SubjectKind: ACLSubjectPrincipal, SubjectID: principal.ID, SubjectPrincipalKind: principal.Kind, Actions: []ACLAction{ACLReadMetadata, ACLReadContent}}}
		}
	}
	persistAdapterEntries(t, store, entries)
	// Reopen from the authoritative JSONL so this release corpus proves restart
	// behavior through the production adapter rather than only the generic
	// planner's in-memory fixtures.
	restarted, err := newMeetingMemoryStore(path)
	if err != nil {
		t.Fatal(err)
	}
	adapter := &MeetingMemoryBrainAdapter{Memory: restarted, Objects: aclBrainCurrentObjectResolver{Store: acl}, Kernel: AuthorizationKernel{Store: acl}, Purge: adapterBrainPurgeGeneration(37), Consent: selectiveBrainConsent{}, Now: func() time.Time { return end }}
	planner := BrainRetrievalPlanner{Inventory: adapter, Bodies: adapter, Kernel: AuthorizationKernel{Store: acl}, Purge: adapterBrainPurgeGeneration(37),
		PromptLimits: BrainPromptLimits{MaxSourceChunkBytes: 64, MaxPromptBytes: 1 << 20, MaxFoldInputs: 8, MaxFoldOutputBytes: 4096}}
	result, err := planner.Resolve(context.Background(), BrainRetrievalRequest{Principal: principal, Query: "operating evidence", Temporal: temporal})
	if err != nil {
		t.Fatal(err)
	}
	if result.Coverage.Status != RecallCoverageComplete || len(result.Sources) != meetings*entriesPerMeeting || result.Coverage.AuthorizedSources != meetings*entriesPerMeeting {
		t.Fatalf("corpus retrieval incomplete: coverage=%+v sources=%d", result.Coverage, len(result.Sources))
	}
	sittings := make(map[string]bool, meetings)
	firstEvidenceID := result.Sources[0].EvidenceID
	firstChunks := 0
	for _, source := range result.Sources {
		sittings[source.Evidence.SittingID] = true
	}
	for _, chunk := range result.PromptPlan.Chunks {
		if chunk.EvidenceID == firstEvidenceID {
			firstChunks++
		}
	}
	if len(sittings) != meetings || firstChunks < 4 || !result.Sources[0].Evidence.OccurredStart.Before(start.Add(24*time.Hour)) ||
		result.Sources[len(result.Sources)-1].Evidence.OccurredStart.Before(end.Add(-48*time.Hour)) {
		t.Fatalf("corpus shape sittings=%d firstChunks=%d first=%s last=%s", len(sittings), firstChunks,
			result.Sources[0].Evidence.OccurredStart, result.Sources[len(result.Sources)-1].Evidence.OccurredStart)
	}
}
