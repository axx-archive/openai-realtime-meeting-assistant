package main

import "strings"

const (
	coworkerWorkflowGoalLoop        = "goal_loop_v1"
	coworkerWorkflowStrategicDesign = "strategic_design_v1"
	coworkerWorkflowCriticLoop      = "critic_loop_v1"
	coworkerWorkflowWavePlan        = "wave_plan_v1"
)

// coworkerWorkflowProfiles stamps the native, versioned process contract a
// STRIDE agent actually received. These are product-owned equivalents, not a
// claim that the server loaded a user's local Codex SKILL.md package.
func coworkerWorkflowProfiles(query string) []string {
	profiles := []string{coworkerWorkflowGoalLoop}
	lower := strings.ToLower(strings.Join(strings.Fields(query), " "))
	if containsWorkflowSignal(lower,
		"$strategic-design", "strategic design", "decision-complete design", "system architecture",
		"architecture decision", "design a fix", "migration design", "ownership model", "resolve the tradeoffs",
	) {
		profiles = append(profiles, coworkerWorkflowStrategicDesign)
	}
	if containsWorkflowSignal(lower,
		"$critic-loop", "critic loop", "adversarial review", "independent quality gate", "pre-ship review",
		"production-ready", "production ready", "best in class", "10/10", "stress-test the result", "stress test the result", "dialed in",
	) {
		profiles = append(profiles, coworkerWorkflowCriticLoop)
	}
	if containsWorkflowSignal(lower,
		"$wave-plan", "wave plan", "execution ledger", "rollout plan", "deployment plan", "migration rollout",
		"cutover plan", "rollback plan", "ship this live", "get this live", "testflight",
	) {
		profiles = append(profiles, coworkerWorkflowWavePlan)
	}
	return profiles
}

func containsWorkflowSignal(value string, signals ...string) bool {
	for _, signal := range signals {
		if strings.Contains(value, signal) {
			return true
		}
	}
	return false
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
		coworkerWorkflowWavePlan + ": when loaded, keep one dependency-aware execution ledger for work spanning contexts, handoffs, releases, migrations, or rollback checkpoints. Record verified state, the exact resume point, gates, and rollback; skip ceremony for a one-context reversible task.",
	}, "\n")
}
