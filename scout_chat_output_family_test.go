package main

import (
	"strings"
	"testing"
)

func TestScoutChatOutputFamilyUsesProcessAndResultAuthorityBeforeMode(t *testing.T) {
	for mode, want := range map[string]string{
		"deck":            "Presentation",
		"presentation":    "Presentation",
		"slides":          "Presentation",
		"document":        "Document",
		"report":          "Document",
		"scheduled":       "Scheduled work",
		"recurring":       "Scheduled work",
		"revision":        "Revision",
		"meeting recap":   "Meeting recap",
		"visualization":   "Data visualization",
		"build":           "Build",
		"project plan":    "Project plan",
		"financial model": "Financial model",
		"design":          "Design",
		"research":        "Research",
	} {
		if got := scoutChatOutputFamilyForMode(mode); got != want {
			t.Errorf("mode %q family=%q want=%q", mode, got, want)
		}
	}
	if got := scoutChatOutputFamilyForMode("research and write a deck"); got != "" {
		t.Fatalf("unrecognized mode inferred family %q", got)
	}
	if got := scoutChatClosedWorkStatus("blocked"); got != "Needs attention" {
		t.Fatalf("blocked status=%q, want Needs attention", got)
	}

	tests := []struct {
		name     string
		artifact meetingMemoryEntry
		want     string
	}{
		{
			name:     "presentation process outranks research worker",
			artifact: meetingMemoryEntry{Metadata: map[string]string{"processId": packagingStudioProcessID, "mode": "research"}},
			want:     "Presentation",
		},
		{
			name:     "document process outranks research worker",
			artifact: meetingMemoryEntry{Metadata: map[string]string{"processId": documentReportProcessID, "mode": "research"}},
			want:     "Document",
		},
		{
			name:     "typed deck outranks research worker",
			artifact: meetingMemoryEntry{Text: "<!doctype html><html><body></body></html>", Metadata: map[string]string{"type": artifactTypeHTMLDeck, "mode": "research"}},
			want:     "Presentation",
		},
		{
			name:     "typed document outranks research worker",
			artifact: meetingMemoryEntry{Metadata: map[string]string{"type": artifactTypeMarkdown, "mode": "research"}},
			want:     "Document",
		},
		{
			name:     "ordinary scheduled work keeps its family",
			artifact: meetingMemoryEntry{Metadata: map[string]string{"mode": "scheduled"}},
			want:     "Scheduled work",
		},
		{
			name:     "ordinary research keeps its family",
			artifact: meetingMemoryEntry{Metadata: map[string]string{"mode": "research"}},
			want:     "Research",
		},
		{
			name:     "unknown mode fails closed",
			artifact: meetingMemoryEntry{Metadata: map[string]string{"mode": "research_and_design", "threadQuery": "make a deck"}},
			want:     "",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := scoutChatOutputFamilyForArtifact(test.artifact); got != test.want {
				t.Fatalf("family=%q want=%q", got, test.want)
			}
		})
	}

	for _, test := range []struct {
		name string
		ref  scoutChatThreadRef
		want string
	}{
		{
			name: "process corrects stale internal family",
			ref:  scoutChatThreadRef{ProcessID: packagingStudioProcessID, OutputFamily: "Research", Mode: "research"},
			want: "Presentation",
		},
		{
			name: "result type corrects stale internal family",
			ref:  scoutChatThreadRef{ResultArtifactType: artifactTypeMarkdown, OutputFamily: "Research", Mode: "research"},
			want: "Document",
		},
		{
			name: "ordinary mode fallback",
			ref:  scoutChatThreadRef{Mode: "financial model"},
			want: "Financial model",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := scoutChatOutputFamilyForRef(&test.ref); got != test.want {
				t.Fatalf("family=%q want=%q", got, test.want)
			}
		})
	}
}

func TestScoutChatThreadPreviewClosesWorkCopyBeforePersistence(t *testing.T) {
	internal := "identity_judges synthesizer failed: unsupported factual material needs attention needs attention"
	document := scoutChatMessageRecord{
		Kind: "thread",
		Text: internal,
		Thread: &scoutChatThreadRef{
			Mode:         "research",
			ProcessID:    documentReportProcessID,
			OutputFamily: "Research",
			Status:       "needs_attention",
			CurrentStage: "document_jury",
		},
	}
	thread := scoutChatThreadRecord{Messages: []scoutChatMessageRecord{document}}
	if got := scoutChatThreadPreview(thread); got != "Document · Needs attention" {
		t.Fatalf("preview=%q", got)
	}
	updateScoutChatThreadSummary(&thread, scoutChatMessageRecord{}, document)
	if thread.Preview != "Document · Needs attention" {
		t.Fatalf("persisted preview=%q", thread.Preview)
	}
	if strings.Contains(thread.Preview, "identity_judges") || strings.Count(strings.ToLower(thread.Preview), "needs attention") != 1 {
		t.Fatalf("persistent rail leaked or duplicated internal copy: %q", thread.Preview)
	}

	governed := scoutChatMessageRecord{
		Kind: "work_result",
		Text: "Artifact: /api/stride/v1/work/runs/private/artifact",
		Work: &scoutChatWorkRecordRef{
			Status:  "completed",
			Summary: "synthesizer report: provider-private details",
		},
	}
	thread = scoutChatThreadRecord{Messages: []scoutChatMessageRecord{governed}}
	if got := scoutChatThreadPreview(thread); got != "Work · Delivered" {
		t.Fatalf("governed preview=%q", got)
	}
	updateScoutChatThreadSummary(&thread, scoutChatMessageRecord{}, governed)
	if thread.Preview != "Work · Delivered" {
		t.Fatalf("governed persisted preview=%q", thread.Preview)
	}

	root := scoutChatMessageRecord{
		Kind:   "thread",
		Thread: &scoutChatThreadRef{ProcessID: packagingStudioProcessID, Mode: "goal", Status: "running"},
	}
	stage := scoutChatMessageRecord{
		Kind:   "artifact",
		Text:   "identity_judges synthesizer finished internal stage",
		Thread: &scoutChatThreadRef{Mode: "workflow", Status: "complete", ArtifactID: "internal-stage"},
	}
	thread = scoutChatThreadRecord{Messages: []scoutChatMessageRecord{root, stage}}
	if got := scoutChatThreadPreview(thread); got != "Presentation · Building" {
		t.Fatalf("internal stage replaced root preview: %q", got)
	}
	updateScoutChatThreadSummary(&thread, scoutChatMessageRecord{}, stage)
	if thread.Preview != "Presentation · Building" || strings.Contains(thread.Preview, "identity_judges") {
		t.Fatalf("internal stage persisted into rail: %q", thread.Preview)
	}
}
