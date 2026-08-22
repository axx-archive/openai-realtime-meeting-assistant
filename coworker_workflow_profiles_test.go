package main

import (
	"strings"
	"testing"
	"time"
)

func TestCoworkerWorkflowProfilesAreVersionedAndExplicit(t *testing.T) {
	plain := coworkerWorkflowProfiles("build the launch plan")
	if len(plain) != 1 || plain[0] != coworkerWorkflowGoalLoop {
		t.Fatalf("plain profiles=%v", plain)
	}
	explicit := coworkerWorkflowProfiles("use $strategic-design, then run a $critic-loop and keep $wave-plan state")
	if len(explicit) != 4 || explicit[1] != coworkerWorkflowStrategicDesign || explicit[2] != coworkerWorkflowCriticLoop || explicit[3] != coworkerWorkflowWavePlan {
		t.Fatalf("explicit profiles=%v", explicit)
	}
}

func TestCoworkerWorkflowProfilesSelectProportionateInternalPolicy(t *testing.T) {
	for _, test := range []struct {
		query string
		want  string
	}{
		{"design a fix for the ownership model", coworkerWorkflowStrategicDesign},
		{"make the result best in class and production-ready", coworkerWorkflowCriticLoop},
		{"ship this live, retain a rollback plan, then send it to TestFlight", coworkerWorkflowWavePlan},
	} {
		profiles := strings.Join(coworkerWorkflowProfiles(test.query), ",")
		if !strings.Contains(profiles, test.want) {
			t.Fatalf("profiles for %q = %q, want %q", test.query, profiles, test.want)
		}
	}
	if profiles := coworkerWorkflowProfiles("what are 10 ways to welcome a customer?"); len(profiles) != 1 || profiles[0] != coworkerWorkflowGoalLoop {
		t.Fatalf("simple answer picked specialist workflow profiles: %v", profiles)
	}
}

func TestCodexWorkerReceivesExactSelectedWorkflowProfiles(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	thread := scoutAgentThread{
		ID: "workflow-profile-codex", Mode: "workflow", Query: "ship this live with an independent quality gate",
		Artifact: meetingMemoryEntry{Metadata: map[string]string{
			"workflowProfiles": strings.Join([]string{coworkerWorkflowGoalLoop, coworkerWorkflowCriticLoop, coworkerWorkflowWavePlan}, ","),
		}},
	}
	prompt := app.buildCodexAgentThreadPrompt(thread, time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC), codexJobAuthorityWorkspaceWrite)
	for _, want := range []string{coworkerWorkflowGoalLoop, coworkerWorkflowCriticLoop, coworkerWorkflowWavePlan, "skip ceremony for a one-context reversible task"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("Codex prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestCoworkerRunnerGetsNativeControlsAndOneHopDelegationTool(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	runner := newAnthropicFableRunner(app)
	seen := map[string]bool{}
	for _, tool := range runner.toolsForAuthority("workflow", codexJobAuthorityWorkspaceWrite) {
		seen[tool.Name] = true
	}
	for _, name := range []string{"archive_channel", "delete_file_folder", "delete_file", "request_coworker_help"} {
		if !seen[name] {
			t.Fatalf("coworker runner missing %s", name)
		}
	}
	for _, tool := range runner.toolsForAuthority("workflow", codexJobAuthorityReadOnly) {
		if tool.Name == "request_coworker_help" || tool.Name == "delete_file" {
			t.Fatalf("read-only coworker received write tool %s", tool.Name)
		}
	}
}
