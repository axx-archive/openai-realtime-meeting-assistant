package main

import "strings"

const (
	coworkerWorkflowGoalLoop        = "goal_loop_v1"
	coworkerWorkflowStrategicDesign = "strategic_design_v1"
	coworkerWorkflowCriticLoop      = "critic_loop_v1"
)

// coworkerWorkflowProfiles stamps the native, versioned process contract a
// STRIDE agent actually received. These are product-owned equivalents, not a
// claim that the server loaded a user's local Codex SKILL.md package.
func coworkerWorkflowProfiles(query string) []string {
	profiles := []string{coworkerWorkflowGoalLoop}
	lower := strings.ToLower(query)
	if strings.Contains(lower, "$strategic-design") || strings.Contains(lower, "strategic design") || strings.Contains(lower, "decision-complete design") {
		profiles = append(profiles, coworkerWorkflowStrategicDesign)
	}
	if strings.Contains(lower, "$critic-loop") || strings.Contains(lower, "critic loop") || strings.Contains(lower, "adversarial review") || strings.Contains(lower, "independent quality gate") {
		profiles = append(profiles, coworkerWorkflowCriticLoop)
	}
	return profiles
}

func coworkerWorkflowProfileInstruction(metadata map[string]string) string {
	profiles := strings.TrimSpace(metadata["workflowProfiles"])
	if profiles == "" {
		profiles = coworkerWorkflowGoalLoop
	}
	return strings.Join([]string{
		"Workflow profiles loaded for this run: " + profiles + ". Report these exact versioned ids in your evidence; do not claim a profile that is absent.",
		coworkerWorkflowGoalLoop + ": own the objective end to end—establish the goal, decompose dependencies, use the right available capabilities, integrate results, review against the original goal, gate before shipping, and verify completion or name the blocker.",
		coworkerWorkflowStrategicDesign + ": when loaded, resolve consequential product/system ambiguity before implementation; make authority, ownership, state, failure, migration, and verification decisions explicit, and exit once the design is decision-complete.",
		coworkerWorkflowCriticLoop + ": when loaded, apply an independent evidence-based challenge to the result, prioritize concrete findings over scores, revise only bounded failures, and never substitute critique for tests or source-of-truth validation.",
	}, "\n")
}
