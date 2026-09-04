package main

import (
	"errors"
	"strings"
)

const businessEpisodeBindingMetadataKey = "businessEpisodeBinding"

// This release recognizes future Business work but cannot execute it. There is
// deliberately no environment switch: enabling dispatch requires an implemented
// authority/budget contract and a new reviewed release.
var errBusinessExecutionUnavailable = errors.New("business execution is unavailable in this release; preserve this work for a compatible runtime")

func businessExecutionProcessReserved(id string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(id)), "business_")
}

func businessExecutionPlanError(plan *goalPlan) error {
	if plan != nil && (len(plan.BusinessEpisodeBinding) != 0 || businessExecutionProcessReserved(plan.ProcessID) || businessExecutionProcessReserved(plan.ToolTemplate)) {
		return errBusinessExecutionUnavailable
	}
	return nil
}

func businessExecutionMetadataError(metadata map[string]string) error {
	// Presence, including an empty/malformed value, is enough to fence. An
	// unknown version must never disappear into an unbound legacy execution.
	for _, key := range []string{businessEpisodeBindingMetadataKey, "businessId", "businessEpisodeId"} {
		if _, present := metadata[key]; present {
			return errBusinessExecutionUnavailable
		}
	}
	if metadata["originKind"] == "business_episode" || businessExecutionProcessReserved(metadata["processId"]) || businessExecutionProcessReserved(metadata["toolTemplate"]) {
		return errBusinessExecutionUnavailable
	}
	if plan, ok := decodeGoalPlan(metadata["goalPlan"]); ok {
		return businessExecutionPlanError(&plan)
	}
	return nil
}

func businessExecutionLaunchError(spec goalLaunchSpec) error {
	if businessExecutionProcessReserved(spec.ToolTemplate) {
		return errBusinessExecutionUnavailable
	}
	return businessExecutionMetadataError(spec.Origin)
}

func (e *goalEngine) businessExecutionCompatibilityError(plan *goalPlan, parentID string) error {
	if err := businessExecutionPlanError(plan); err != nil {
		return err
	}
	if e != nil && e.app != nil && strings.TrimSpace(parentID) != "" {
		if parent, ok := e.app.osArtifactByID(parentID); ok {
			return businessExecutionMetadataError(parent.Metadata)
		}
	}
	return nil
}
