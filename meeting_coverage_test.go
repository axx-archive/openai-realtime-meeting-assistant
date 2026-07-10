package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

/* ---------- pure coverage-label classification (kanban-card-107) ---------- */

func TestMeetingCoverageLabel(t *testing.T) {
	base := time.Date(2026, 7, 6, 16, 0, 0, 0, time.UTC)
	cases := []struct {
		name       string
		resolvable bool
		sitting    time.Time
		span       time.Time
		gap        time.Duration
		want       string
	}{
		{"full — opens with the sitting, no gap", true, base, base.Add(time.Minute), 0, coverageLabelFull},
		{"full — start tolerance boundary is inclusive", true, base, base.Add(coverageStartTolerance), 0, coverageLabelFull},
		{"full — gap threshold boundary is inclusive", true, base, base.Add(time.Minute), coverageGapThreshold, coverageLabelFull},
		{"late start — capture opens well after admission", true, base, base.Add(10 * time.Minute), 0, coverageLabelPartialLateStart},
		{"gaps — long stretch with no captured transcript", true, base, base.Add(time.Minute), coverageGapThreshold + time.Minute, coverageLabelPartialGaps},
		{"late start wins over gaps", true, base, base.Add(10 * time.Minute), coverageGapThreshold + time.Minute, coverageLabelPartialLateStart},
		{"unknown — unresolvable (legacy / no record)", false, base, base.Add(time.Minute), 0, coverageLabelUnknown},
		{"unknown — no sitting start", true, time.Time{}, base.Add(time.Minute), 0, coverageLabelUnknown},
		{"unknown — no captured span (no digest yet)", true, base, time.Time{}, 0, coverageLabelUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := meetingCoverageLabel(tc.resolvable, tc.sitting, tc.span, tc.gap); got != tc.want {
				t.Fatalf("meetingCoverageLabel = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestIsLegacyMeetingKey(t *testing.T) {
	if !isLegacyMeetingKey("meeting-legacy-2026-07-06") {
		t.Fatal("synthetic per-day key must read legacy")
	}
	if isLegacyMeetingKey("meeting-20260706-101500-000000001") {
		t.Fatal("a real minted meeting id must not read legacy")
	}
}

/* ---------- transcript coverage read ---------- */

func TestTranscriptCoverageForMeeting(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	t0 := time.Date(2026, 7, 6, 17, 0, 0, 0, time.UTC)
	seed := func(id string, kind string, meetingID string, at time.Time) {
		app.memory.entries = append(app.memory.entries, meetingMemoryEntry{
			ID:        id,
			Kind:      kind,
			Text:      "line " + id,
			CreatedAt: at,
			Metadata:  map[string]string{"meetingId": meetingID},
		})
	}
	// meeting-x: three transcripts, largest gap 7m between the 2nd and 3rd.
	seed("tx-1", meetingMemoryKindTranscript, "meeting-x", t0)
	seed("tx-2", meetingMemoryKindTranscript, "meeting-x", t0.Add(1*time.Minute))
	seed("tx-3", meetingMemoryKindTranscript, "meeting-x", t0.Add(8*time.Minute))
	// noise that must be excluded: another meeting, and a non-transcript kind.
	seed("tx-other", meetingMemoryKindTranscript, "meeting-y", t0.Add(3*time.Minute))
	seed("brain-x", meetingMemoryKindBrain, "meeting-x", t0.Add(30*time.Minute))

	coverage := app.memory.transcriptCoverageForMeeting("meeting-x")
	if coverage.Count != 3 {
		t.Fatalf("count = %d, want 3 (other meetings and non-transcript kinds excluded)", coverage.Count)
	}
	if !coverage.FirstAt.Equal(t0) {
		t.Fatalf("firstAt = %v, want %v", coverage.FirstAt, t0)
	}
	if !coverage.LastAt.Equal(t0.Add(8 * time.Minute)) {
		t.Fatalf("lastAt = %v, want %v", coverage.LastAt, t0.Add(8*time.Minute))
	}
	if coverage.MaxInternalGap != 7*time.Minute {
		t.Fatalf("maxInternalGap = %v, want 7m", coverage.MaxInternalGap)
	}

	if empty := app.memory.transcriptCoverageForMeeting("meeting-none"); empty.Count != 0 || empty.MaxInternalGap != 0 {
		t.Fatalf("unknown meeting coverage = %+v, want zero", empty)
	}
}

/* ---------- digest producer stamps coverage ---------- */

// runCoverageDigest sets up one meeting with a directory record started at
// sittingStart, a brain whose captured span opens at 16:55Z, runs the meeting
// digest producer, and returns the stamped digest entry.
func runCoverageDigest(t *testing.T, sittingStart time.Time, listenOnly bool) meetingMemoryEntry {
	t.Helper()
	app := newIsolatedKanbanBoardApp(t)
	appendTestTranscript(t, app, "tx-1", "We choose vendor Zebra for the packaging pilot.")
	appendTestTranscript(t, app, "tx-2", "Tyler will draft the pricing sheet by Friday.")
	meetingID := app.memory.currentMeetingID(officeRoomID)
	if meetingID == "" {
		t.Fatal("expected a minted meeting id")
	}
	if _, ok := app.meetings.startMeeting(officeRoomID, meetingID, sittingStart, []string{"AJ"}); !ok {
		t.Fatal("startMeeting did not create a directory record")
	}
	if listenOnly {
		app.meetings.latchListenOnly(meetingID)
	}
	appendDigestTestBrain(t, app, "brain-1", meetingID,
		"## Overview\nVendor Zebra chosen.\n## Transcript reference\ntx-1, tx-2",
		map[string]string{
			"fromTranscriptCreatedAt":    "2026-07-06T16:55:00Z",
			"throughTranscriptCreatedAt": "2026-07-06T17:10:00Z",
		})
	responder := func(context.Context, string, openAITextRequest) (string, error) {
		return cannedMeetingDigestJSON(), nil
	}
	entry, err := app.runAmbientAgentOnce(meetingDigestAgent(), context.Background(), "test-key", responder, 1)
	if err != nil {
		t.Fatalf("runAmbientAgentOnce: %v", err)
	}
	return entry
}

func TestMeetingDigestStampsCoverageFull(t *testing.T) {
	// sitting opened 30s before the captured span → within tolerance → full.
	entry := runCoverageDigest(t, time.Date(2026, 7, 6, 16, 54, 30, 0, time.UTC), false)
	if got := entry.Metadata[digestCoverageMetadataKey]; got != coverageLabelFull {
		t.Fatalf("coverage = %q, want %q", got, coverageLabelFull)
	}
	if entry.Metadata[digestSittingStartedAtMetadataKey] == "" {
		t.Fatalf("sittingStartedAt stamp missing: %+v", entry.Metadata)
	}
	if _, ok := entry.Metadata[externalMayPredateCaptureMetadataKey]; ok {
		t.Fatal("a full non-listen-only digest must not carry externalMayPredateCapture")
	}
}

func TestMeetingDigestStampsCoveragePartialLateStart(t *testing.T) {
	// sitting opened 15m before the captured span → capture began late.
	entry := runCoverageDigest(t, time.Date(2026, 7, 6, 16, 40, 0, 0, time.UTC), false)
	if got := entry.Metadata[digestCoverageMetadataKey]; got != coverageLabelPartialLateStart {
		t.Fatalf("coverage = %q, want %q", got, coverageLabelPartialLateStart)
	}
}

func TestMeetingDigestStampsListenOnlyExternalPredate(t *testing.T) {
	entry := runCoverageDigest(t, time.Date(2026, 7, 6, 16, 54, 30, 0, time.UTC), true)
	if entry.Metadata[listenOnlyMetadataKey] != "true" {
		t.Fatalf("listenOnly stamp missing: %+v", entry.Metadata)
	}
	if entry.Metadata[externalMayPredateCaptureMetadataKey] != "true" {
		t.Fatalf("listen-only digest must carry externalMayPredateCapture: %+v", entry.Metadata)
	}
}

func TestMeetingDigestCoverageUnknownWithoutRecord(t *testing.T) {
	// No directory record (the legacy / pre-record path): coverage degrades to
	// an explicit "unknown", never a fabricated "full".
	app := newIsolatedKanbanBoardApp(t)
	appendTestTranscript(t, app, "tx-1", "Kicking off the pilot.")
	meetingID := app.memory.currentMeetingID(officeRoomID)
	appendDigestTestBrain(t, app, "brain-1", meetingID, "## Overview\nKickoff.",
		map[string]string{"fromTranscriptCreatedAt": "2026-07-06T16:55:00Z", "throughTranscriptCreatedAt": "2026-07-06T17:00:00Z"})
	responder := func(context.Context, string, openAITextRequest) (string, error) {
		return cannedMeetingDigestJSON(), nil
	}
	entry, err := app.runAmbientAgentOnce(meetingDigestAgent(), context.Background(), "test-key", responder, 1)
	if err != nil {
		t.Fatalf("runAmbientAgentOnce: %v", err)
	}
	if got := entry.Metadata[digestCoverageMetadataKey]; got != coverageLabelUnknown {
		t.Fatalf("coverage = %q, want %q for a meeting with no directory record", got, coverageLabelUnknown)
	}
	if !strings.HasPrefix(entry.Kind, "meeting_digest") {
		t.Fatalf("kind = %q, want the meeting digest", entry.Kind)
	}
}
