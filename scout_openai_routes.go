package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

const (
	// Scout's required text path is OpenAI-owned. Routing is a bounded,
	// schema-checked classification seat; grounded conversation gets the
	// stronger routine-reasoning seat. Anthropic may remain available to
	// explicitly optional specialists, but an installed Anthropic key never
	// changes either core route.
	defaultScoutRouterModel     = "gpt-5.6-terra"
	defaultScoutChatModel       = "gpt-5.6-terra"
	defaultScoutExtractionModel = "gpt-5.6-luna"
	defaultRouterModel          = defaultScoutRouterModel
)

func scoutRouterModel() string {
	if model := strings.TrimSpace(os.Getenv("OPENAI_SCOUT_ROUTER_MODEL")); model != "" {
		if strings.HasPrefix(strings.ToLower(model), "gpt-") {
			return model
		}
	}
	return defaultScoutRouterModel
}

func scoutChatModel() string {
	if model := strings.TrimSpace(os.Getenv("OPENAI_SCOUT_CHAT_MODEL")); model != "" {
		if strings.HasPrefix(strings.ToLower(model), "gpt-") {
			return model
		}
	}
	return defaultScoutChatModel
}

func scoutExtractionModel() string {
	if model := strings.TrimSpace(os.Getenv("OPENAI_SCOUT_EXTRACTION_MODEL")); model != "" {
		if strings.HasPrefix(strings.ToLower(model), "gpt-") {
			return model
		}
	}
	return defaultScoutExtractionModel
}

type openAIScoutRouterField struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type openAIScoutRouterOption struct {
	Label  string `json:"label"`
	Reply  string `json:"reply"`
	ToolID string `json:"tool_id"`
}

// openAIScoutRouterOutput is deliberately flat. Strict Responses schemas
// require every property, so values irrelevant to the selected route are
// emitted as empty strings/arrays and rejected if they try to smuggle a
// second action into the turn.
type openAIScoutRouterOutput struct {
	Route         string                    `json:"route"`
	ToolID        string                    `json:"tool_id"`
	Mode          string                    `json:"mode"`
	Objective     string                    `json:"objective"`
	PackageID     string                    `json:"package_id"`
	AuthorityHint string                    `json:"authority_hint"`
	Prompt        string                    `json:"prompt"`
	Title         string                    `json:"title"`
	Question      string                    `json:"question"`
	Fields        []openAIScoutRouterField  `json:"fields"`
	Options       []openAIScoutRouterOption `json:"options"`
}

func scoutRouterJSONSchema() *openAIJSONSchema {
	stringField := func() map[string]any { return map[string]any{"type": "string"} }
	return &openAIJSONSchema{
		Name:        "scout_route",
		Description: "Exactly one bounded Scout routing verdict. Empty values represent fields unused by the selected route.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"route":          map[string]any{"type": "string", "enum": []string{"inline", "tool_run", "workstream", "goal_run", "image", "choices"}},
				"tool_id":        stringField(),
				"mode":           map[string]any{"type": "string", "enum": []string{"", "research", "design", "grill", "workflow"}},
				"objective":      stringField(),
				"package_id":     stringField(),
				"authority_hint": map[string]any{"type": "string", "enum": []string{"", toolAuthorityReadOnly, toolAuthorityWorkspaceWrite}},
				"prompt":         stringField(),
				"title":          stringField(),
				"question":       stringField(),
				"fields": map[string]any{
					"type": "array", "maxItems": 16,
					"items": map[string]any{
						"type":       "object",
						"properties": map[string]any{"key": stringField(), "value": stringField()},
						"required":   []string{"key", "value"}, "additionalProperties": false,
					},
				},
				"options": map[string]any{
					"type": "array", "maxItems": 4,
					"items": map[string]any{
						"type":       "object",
						"properties": map[string]any{"label": stringField(), "reply": stringField(), "tool_id": stringField()},
						"required":   []string{"label", "reply", "tool_id"}, "additionalProperties": false,
					},
				},
			},
			"required":             []string{"route", "tool_id", "mode", "objective", "package_id", "authority_hint", "prompt", "title", "question", "fields", "options"},
			"additionalProperties": false,
		},
	}
}

func scoutRouterInstructions() string {
	registry := make([]string, 0, 16)
	for _, group := range buildToolsPayload() {
		for _, tool := range group.Tools {
			registry = append(registry, fmt.Sprintf("%s (%s): %s", tool.ID, group.Label, tool.Promise))
		}
	}
	return scoutRouterSystemPrompt() + strings.Join([]string{
		"",
		"Registry routes (tool_id must be one exact id from this list):",
		strings.Join(registry, "\n"),
		"",
		"Return the strict JSON route object, not prose and not a function call.",
		"route=inline for Tier 0; leave every other field empty and arrays empty.",
		"route=tool_run for a registry tool; set tool_id, objective, package_id when known, and fields as key/value pairs.",
		"route=workstream for a bounded pass; set mode and objective.",
		"route=goal_run for a multi-step goal; set objective, package_id when known, and authority_hint to read_only or workspace_write.",
		"route=image for one concept render; set prompt and optional title.",
		"route=choices only for one decisive clarification; set question and 2-4 options. Each option has label, reply, and an optional tool_id represented by an empty string when absent.",
		"Exactly one route is allowed. Empty strings and empty arrays represent unused fields.",
	}, "\n")
}

func decodeOpenAIScoutRouterOutput(raw string) (openAIScoutRouterOutput, error) {
	var output openAIScoutRouterOutput
	decoder := json.NewDecoder(bytes.NewBufferString(strings.TrimSpace(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&output); err != nil {
		return output, fmt.Errorf("decode Scout route: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return openAIScoutRouterOutput{}, fmt.Errorf("decode Scout route: trailing output")
	}
	return output, nil
}

func scoutRouterVerdictFromOpenAI(output openAIScoutRouterOutput, query string) (*scoutRouterVerdict, error) {
	switch strings.ToLower(strings.TrimSpace(output.Route)) {
	case "inline":
		return nil, nil
	case "tool_run":
		proposal := scoutRouterProposalForToolID(output.ToolID, output.Objective, query)
		if proposal == nil {
			return nil, fmt.Errorf("unknown Scout tool route")
		}
		proposal.PackageID = strings.TrimSpace(output.PackageID)
		tool, _ := routerToolByID(proposal.ToolID)
		allowedFields := make(map[string]struct{}, len(tool.FormFields))
		for _, field := range tool.FormFields {
			allowedFields[field.Key] = struct{}{}
		}
		for _, field := range output.Fields {
			key := strings.TrimSpace(field.Key)
			value := strings.TrimSpace(field.Value)
			if _, ok := allowedFields[key]; !ok || value == "" {
				continue
			}
			if proposal.Fields == nil {
				proposal.Fields = make(map[string]string)
			}
			proposal.Fields[key] = value
		}
		return &scoutRouterVerdict{proposal: proposal, source: proposalSourceChatRouter}, nil
	case "workstream":
		mode := strings.ToLower(strings.TrimSpace(output.Mode))
		switch mode {
		case "research", "design", "grill", "workflow":
		default:
			return nil, fmt.Errorf("unknown Scout workstream route")
		}
		objective := firstNonBlank(strings.TrimSpace(output.Objective), strings.TrimSpace(query))
		return &scoutRouterVerdict{proposal: &scoutRouterProposal{
			Kind: scoutRouterProposalKindWorkstream, Mode: mode, Objective: objective,
			Query: strings.TrimSpace(query), Lane: scoutProposalLane(mode, "", ""),
			WeightLabel: scoutProposalWeightQuickPass,
			Summary:     "this looks like a quick " + assistantToolLabel(mode) + " pass — confirm and it runs once: " + objective,
		}, source: proposalSourceChatRouter}, nil
	case "goal_run":
		proposal := scoutRouterGoalProposal(output.Objective, output.AuthorityHint, output.PackageID, query)
		if proposal == nil {
			return nil, fmt.Errorf("empty Scout goal route")
		}
		return &scoutRouterVerdict{proposal: proposal, source: proposalSourceChatRouter}, nil
	case "image":
		if !openAIImageGenerationAvailable() {
			return nil, fmt.Errorf("Scout image route is unavailable")
		}
		proposal := scoutRouterImageProposal(firstNonBlank(firstNonBlank(output.Prompt, output.Objective), query), query)
		if proposal == nil {
			return nil, fmt.Errorf("empty Scout image route")
		}
		return &scoutRouterVerdict{proposal: proposal, source: proposalSourceChatRouter}, nil
	case "choices":
		question := trimForStorage(output.Question, 240)
		if question == "" {
			return nil, fmt.Errorf("empty Scout choices question")
		}
		options := make([]scoutChatChoiceOption, 0, 4)
		for _, raw := range output.Options {
			label := trimForStorage(raw.Label, 80)
			if label == "" {
				continue
			}
			toolID := ""
			if wanted := strings.TrimSpace(raw.ToolID); wanted != "" {
				if tool, ok := routerToolByID(wanted); ok {
					toolID = tool.ID
				}
			}
			options = append(options, scoutChatChoiceOption{
				ID: fmt.Sprintf("opt-%d", len(options)+1), Label: label,
				Reply: trimForStorage(raw.Reply, 400), ToolID: toolID,
			})
			if len(options) == 4 {
				break
			}
		}
		if len(options) < 2 {
			return nil, fmt.Errorf("Scout choices route requires at least two options")
		}
		return &scoutRouterVerdict{choices: &scoutChatChoices{
			Question: question, Options: options, Query: strings.TrimSpace(query),
		}, source: proposalSourceChatRouter}, nil
	default:
		return nil, fmt.Errorf("unknown Scout route")
	}
}
