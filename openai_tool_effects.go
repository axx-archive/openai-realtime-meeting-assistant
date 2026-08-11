package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// openAIToolEffectBackend is deliberately four-tool-shaped. Adding a generic
// execute-by-name escape hatch would silently widen the admission boundary.
// A production backend must implement each admitted operation and its
// immutable-operation-id reconciliation independently.
type openAIToolEffectBackend interface {
	ReconcileGoalState(context.Context, openAIToolEffectRequest) (openAIToolReconciliation, error)
	ApplyGoalState(context.Context, openAIToolEffectRequest) (openAIToolEffectCommit, error)
	ReconcileMemoryAnswer(context.Context, openAIToolEffectRequest) (openAIToolReconciliation, error)
	ReadMemoryAnswer(context.Context, openAIToolEffectRequest) (openAIToolEffectCommit, error)
	ReconcileArtifactCreate(context.Context, openAIToolEffectRequest) (openAIToolReconciliation, error)
	CreatePrivateArtifact(context.Context, openAIToolEffectRequest) (openAIToolEffectCommit, error)
	ReconcileArtifactUpdate(context.Context, openAIToolEffectRequest) (openAIToolReconciliation, error)
	UpdateAuthorizedArtifact(context.Context, openAIToolEffectRequest) (openAIToolEffectCommit, error)
}

func validateOpenAIToolMinimizedResult(tool string, raw json.RawMessage) error {
	object, err := decodeOpenAIToolArguments(raw)
	if err != nil {
		return fmt.Errorf("OpenAI tool %q result is not a strict JSON object: %w", tool, err)
	}
	required := map[string]bool{}
	switch tool {
	case controlToolReportGoalState:
		required = map[string]bool{"goal_status": true, "stage": true, "receipt": true}
	case "answer_memory_question":
		required = map[string]bool{"answer": true, "sources": true}
	case "create_artifact":
		required = map[string]bool{"artifact_id": true, "title": true, "type": true, "status": true}
	case "update_artifact":
		required = map[string]bool{"artifact_id": true, "revision": true, "status": true}
	default:
		return errors.New("OpenAI tool result belongs to an unadmitted tool")
	}
	if len(object) != len(required) {
		return fmt.Errorf("OpenAI tool %q result is not the exact minimized shape", tool)
	}
	for key := range required {
		value, ok := object[key]
		if !ok {
			return fmt.Errorf("OpenAI tool %q result omitted %q", tool, key)
		}
		if key == "sources" {
			sources, ok := value.([]any)
			if !ok || len(sources) == 0 {
				return errors.New("OpenAI memory result requires bounded source references")
			}
			for _, source := range sources {
				text, ok := source.(string)
				if !ok || strings.TrimSpace(text) == "" {
					return errors.New("OpenAI memory source reference is invalid")
				}
			}
			continue
		}
		text, ok := value.(string)
		if !ok || strings.TrimSpace(text) == "" {
			return fmt.Errorf("OpenAI tool %q result field %q must be a non-empty string", tool, key)
		}
	}
	return nil
}

type openAIToolEffectRequest struct {
	Current        openAIToolCurrentAuthority
	OperationID    string
	Expectation    openAIToolAuthorityExpectation
	Entry          openAIToolManifestEntry
	Arguments      map[string]any
	PreimageDigest string
}

type openAIToolEffectAdapter struct {
	Backend openAIToolEffectBackend
}

func (adapter openAIToolEffectAdapter) ReconcileOpenAITool(ctx context.Context, current openAIToolCurrentAuthority, operationID string, expectation openAIToolAuthorityExpectation, entry openAIToolManifestEntry, arguments map[string]any, preimageDigest string) (openAIToolReconciliation, error) {
	if adapter.Backend == nil {
		return openAIToolReconciliation{}, errors.New("OpenAI tool effect backend is unavailable")
	}
	request := openAIToolEffectRequest{Current: current, OperationID: operationID, Expectation: expectation, Entry: entry, Arguments: arguments, PreimageDigest: preimageDigest}
	switch entry.Name {
	case controlToolReportGoalState:
		return adapter.Backend.ReconcileGoalState(ctx, request)
	case "answer_memory_question":
		return adapter.Backend.ReconcileMemoryAnswer(ctx, request)
	case "create_artifact":
		return adapter.Backend.ReconcileArtifactCreate(ctx, request)
	case "update_artifact":
		return adapter.Backend.ReconcileArtifactUpdate(ctx, request)
	default:
		return openAIToolReconciliation{}, errors.New("OpenAI tool effect adapter rejects an unadmitted tool")
	}
}

func (adapter openAIToolEffectAdapter) ExecuteOpenAITool(ctx context.Context, current openAIToolCurrentAuthority, operationID string, expectation openAIToolAuthorityExpectation, entry openAIToolManifestEntry, arguments map[string]any, preimageDigest string) (openAIToolEffectCommit, error) {
	if adapter.Backend == nil {
		return openAIToolEffectCommit{}, errors.New("OpenAI tool effect backend is unavailable")
	}
	request := openAIToolEffectRequest{Current: current, OperationID: operationID, Expectation: expectation, Entry: entry, Arguments: arguments, PreimageDigest: strings.TrimSpace(preimageDigest)}
	switch entry.Name {
	case controlToolReportGoalState:
		return adapter.Backend.ApplyGoalState(ctx, request)
	case "answer_memory_question":
		return adapter.Backend.ReadMemoryAnswer(ctx, request)
	case "create_artifact":
		return adapter.Backend.CreatePrivateArtifact(ctx, request)
	case "update_artifact":
		return adapter.Backend.UpdateAuthorizedArtifact(ctx, request)
	default:
		return openAIToolEffectCommit{}, errors.New("OpenAI tool effect adapter rejects an unadmitted tool")
	}
}
