package main

import (
	"fmt"
	"strings"
)

func coworkerDelegationToolDefinition() map[string]any {
	return map[string]any{
		"type":        "function",
		"name":        "request_coworker_help",
		"description": "Delegate one bounded subtask to Scout or a hired STRIDE coworker when it belongs to that coworker's job. This launches a separate visible work thread with attribution; it never impersonates the coworker and never grants broader authority.",
		"parameters": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"coworker":  map[string]any{"type": "string", "description": "Scout or the exact display name of a currently hired coworker."},
				"objective": map[string]any{"type": "string", "description": "The bounded subtask to hand off, with enough context to do it independently."},
				"mode":      map[string]any{"type": "string", "enum": []string{"research", "design", "grill", "workflow"}, "description": "The subtask's work mode."},
			},
			"required":             []string{"coworker", "objective", "mode"},
			"additionalProperties": false,
		},
	}
}

func (app *kanbanBoardApp) coworkerProfileByName(name string) (STRIDEProductAgentContextProfile, bool) {
	wanted := strings.TrimSpace(name)
	if app == nil || app.strideRuntime == nil || wanted == "" || strings.EqualFold(wanted, scoutParticipantName) {
		return STRIDEProductAgentContextProfile{}, false
	}
	var profile STRIDEProductAgentContextProfile
	found := false
	_ = app.strideRuntime.WithProductContext(canonicalTenantID(), STRIDEProductScopeMarketplace, func(ctx STRIDEProductContext) error {
		if ctx.Product == nil {
			return nil
		}
		ctx.Product.mu.RLock()
		ids := make([]string, 0, len(ctx.Product.agents))
		for id, agent := range ctx.Product.agents {
			if strings.EqualFold(strings.TrimSpace(agent.DisplayName), wanted) {
				ids = append(ids, id)
			}
		}
		ctx.Product.mu.RUnlock()
		if len(ids) == 1 {
			profile, found = ctx.Product.agentContextProfile(ids[0])
		}
		return nil
	})
	return profile, found
}

func (app *kanbanBoardApp) requestCoworkerHelp(job AgentJob, args map[string]any) (map[string]any, bool, error) {
	if strings.HasPrefix(strings.TrimSpace(job.thread.Artifact.Metadata["originSurface"]), "agent-delegation:") {
		return nil, false, fmt.Errorf("coworker delegation is limited to one hop; return this need to the requester")
	}
	coworker := strings.TrimSpace(asString(args["coworker"]))
	objective := canonicalizeBoardText(asString(args["objective"]))
	mode := normalizeAgentThreadMode(asString(args["mode"]))
	if coworker == "" || objective == "" || mode == "" {
		return nil, false, fmt.Errorf("coworker, objective, and mode are required")
	}
	user, ok := authenticatedRequester(job.RequestedBy)
	if !ok {
		return nil, false, fmt.Errorf("coworker delegation requires an active organization-member requester")
	}

	delegator := firstNonBlank(job.thread.Artifact.Metadata["agentName"], scoutParticipantName)
	spec := agentThreadGoalSpec{
		Objective:     objective,
		OriginSurface: "agent-delegation:" + job.ThreadID,
		RequestedBy:   user.Email,
		Authority:     normalizeCodexJobAuthority(job.Authority),
		DelegatedBy:   delegator,
	}
	if !strings.EqualFold(coworker, scoutParticipantName) {
		profile, found := app.coworkerProfileByName(coworker)
		if !found {
			return nil, false, fmt.Errorf("coworker %q is not currently hired and available", coworker)
		}
		identity := agentThreadGoalSpecForProfile(profile, delegator)
		identity.Objective = spec.Objective
		identity.OriginSurface = spec.OriginSurface
		identity.RequestedBy = spec.RequestedBy
		identity.Authority = spec.Authority
		spec = identity
	}

	thread, err := app.launchAgentThreadWithSpec(mode, objective, user.Name, map[string]string{
		"originKind":  "agent_delegation",
		"originId":    job.ThreadID,
		"requestedBy": user.Email,
	}, spec)
	if err != nil {
		return nil, false, err
	}
	return map[string]any{
		"ok":          true,
		"coworker":    firstNonBlank(thread.Artifact.Metadata["agentName"], scoutParticipantName),
		"delegatedBy": delegator,
		"threadId":    thread.ID,
		"artifactId":  thread.Artifact.ID,
		"status":      thread.Status,
		"summary":     delegator + " handed this to " + firstNonBlank(thread.Artifact.Metadata["agentName"], scoutParticipantName) + " in a separate work thread.",
	}, false, nil
}
