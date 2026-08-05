package main

import "testing"

func TestCoworkerWorkflowProfilesAreVersionedAndExplicit(t *testing.T) {
	plain := coworkerWorkflowProfiles("build the launch plan")
	if len(plain) != 1 || plain[0] != coworkerWorkflowGoalLoop {
		t.Fatalf("plain profiles=%v", plain)
	}
	explicit := coworkerWorkflowProfiles("use $strategic-design, then run a $critic-loop")
	if len(explicit) != 3 || explicit[1] != coworkerWorkflowStrategicDesign || explicit[2] != coworkerWorkflowCriticLoop {
		t.Fatalf("explicit profiles=%v", explicit)
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
