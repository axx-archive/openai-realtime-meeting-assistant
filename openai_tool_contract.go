package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
)

const (
	openAIToolManifestVersion  = "stride-openai-tools-v1"
	openAIToolSchemaVersion    = "strict-schema-v1"
	openAIToolManifestV1SHA256 = "215a734ddc6d56b991b3d5a185bc50881f18dbbc3263f312318791bf83c7735a"
)

var openAIAdmittedToolNames = map[string]struct{}{
	controlToolReportGoalState: {},
	"answer_memory_question":   {},
	"create_artifact":          {},
	"update_artifact":          {},
}

// openAIToolManifest is the checked-in, server-owned admission boundary. The
// inventory deliberately includes every tool known to the product catalogs so
// an existing or newly added tool is explicitly unadmitted rather than becoming
// callable through a permissive default.
type openAIToolManifest struct {
	Version       string                    `json:"version"`
	SchemaVersion string                    `json:"schema_version"`
	DigestSHA256  string                    `json:"digest_sha256"`
	Tools         []openAIToolManifestEntry `json:"tools"`
}

type openAIToolManifestEntry struct {
	Name                   string            `json:"name"`
	Description            string            `json:"description,omitempty"`
	Admitted               bool              `json:"admitted"`
	Authority              string            `json:"authority,omitempty"`
	Effect                 string            `json:"effect,omitempty"`
	PolicyRevision         string            `json:"policy_revision,omitempty"`
	SchemaSHA256           string            `json:"schema_sha256,omitempty"`
	Normalization          map[string]string `json:"normalization,omitempty"`
	PreimageContract       string            `json:"preimage_contract,omitempty"`
	PostimageContract      string            `json:"postimage_contract,omitempty"`
	ReconciliationContract string            `json:"reconciliation_contract,omitempty"`
	FinalUseContract       string            `json:"final_use_contract,omitempty"`
	FanOutContract         string            `json:"fan_out_contract,omitempty"`
	MinimizedResult        string            `json:"minimized_result,omitempty"`
	RequiredSuites         []string          `json:"required_suites,omitempty"`
	Parameters             map[string]any    `json:"parameters,omitempty"`
	Source                 map[string]any    `json:"-"`
}

type openAIToolFrozenContract struct {
	Authority, Effect, PolicyRevision                                      string
	Preimage, Postimage, Reconciliation, FinalUse, FanOut, MinimizedResult string
	RequiredSuites                                                         []string
}

var openAIToolFrozenContracts = map[string]openAIToolFrozenContract{
	controlToolReportGoalState: {
		Authority: "goal-control-v1", Effect: "control", PolicyRevision: "goal-control-v1",
		Preimage: "sticky-goal-revision", Postimage: "exact-monotonic-goal-projection", Reconciliation: "compare-and-merge-same-operation",
		FinalUse: "held-goal-thread-capability", FanOut: "one-authorized-progress-event", MinimizedResult: "bounded-state-and-receipt-only",
		RequiredSuites: []string{"ToolGoalStateNormalRaceRestart"},
	},
	"answer_memory_question": {
		Authority: "read_only:read", Effect: "read", PolicyRevision: "orchestrator-tool-policy-v1",
		Preimage: "tenant-memory-high-water-and-source-window", Postimage: "same-source-receipt", Reconciliation: "reread-receipt-never-stale-body",
		FinalUse: "held-tenant-person-thread-read", FanOut: "none", MinimizedResult: "bounded-answer-and-source-references",
		RequiredSuites: []string{"ToolMemoryReadNormalRaceRestart"},
	},
	"create_artifact": {
		Authority: "workspace_write:artifact_write", Effect: "write", PolicyRevision: "orchestrator-tool-policy-v1",
		Preimage: "server-thread-and-artifact-collection-generation", Postimage: "created-artifact-authorization-header-and-content-digest", Reconciliation: "semantic-operation-id-owner-thread-private-postimage",
		FinalUse: "held-requester-thread-write-through-cas-and-delivery", FanOut: "one-authorized-projection-event", MinimizedResult: "artifact-id-title-type-status-never-body",
		RequiredSuites: []string{"ToolCreateArtifactNormalRaceRestart"},
	},
	"update_artifact": {
		Authority: "workspace_write:artifact_write", Effect: "write", PolicyRevision: "orchestrator-tool-policy-v1",
		Preimage: "current-authorization-header-and-full-postimage", Postimage: "next-header-and-full-postimage-digest", Reconciliation: "reread-prior-or-exact-committed-successor",
		FinalUse: "held-requester-thread-artifact-write-through-cas-and-delivery", FanOut: "one-authorized-projection-event", MinimizedResult: "artifact-id-revision-status-never-body",
		RequiredSuites: []string{"ToolUpdateArtifactNormalRaceRestart"},
	},
}

func buildOpenAIToolManifest() (openAIToolManifest, error) {
	catalog := make(map[string]map[string]any)
	app := &kanbanBoardApp{}
	for _, tool := range app.kanbanTools() {
		if err := addOpenAIToolCatalogEntry(catalog, tool); err != nil {
			return openAIToolManifest{}, err
		}
	}
	for _, tool := range privateScoutNativeToolDefinitions() {
		if err := addOpenAIToolCatalogEntry(catalog, tool); err != nil {
			return openAIToolManifest{}, err
		}
	}
	if err := addOpenAIToolCatalogEntry(catalog, coworkerDelegationToolDefinition()); err != nil {
		return openAIToolManifest{}, err
	}
	report := reportGoalStateTool()
	if err := addOpenAIToolCatalogEntry(catalog, map[string]any{
		"type": "function", "name": report.Name, "description": report.Description, "parameters": report.InputSchema,
	}); err != nil {
		return openAIToolManifest{}, err
	}

	names := make([]string, 0, len(catalog))
	for name := range catalog {
		names = append(names, name)
	}
	sort.Strings(names)
	manifest := openAIToolManifest{Version: openAIToolManifestVersion, SchemaVersion: openAIToolSchemaVersion}
	for _, name := range names {
		tool := catalog[name]
		entry := openAIToolManifestEntry{Name: name, Description: asString(tool["description"])}
		_, entry.Admitted = openAIAdmittedToolNames[name]
		if entry.Admitted {
			contract, ok := openAIToolFrozenContracts[name]
			if !ok || len(contract.RequiredSuites) == 0 {
				return openAIToolManifest{}, fmt.Errorf("admitted OpenAI tool %q lacks a complete frozen contract", name)
			}
			entry.Authority, entry.Effect, entry.PolicyRevision = contract.Authority, contract.Effect, contract.PolicyRevision
			entry.PreimageContract, entry.PostimageContract, entry.ReconciliationContract = contract.Preimage, contract.Postimage, contract.Reconciliation
			entry.FinalUseContract, entry.FanOutContract, entry.MinimizedResult = contract.FinalUse, contract.FanOut, contract.MinimizedResult
			entry.RequiredSuites = append([]string(nil), contract.RequiredSuites...)
			if name != controlToolReportGoalState {
				policy, policyOK := orchestratorToolPolicies[name]
				wantAuthority := policy.RequiredAuthority + ":" + policy.SideEffect
				if !policyOK || wantAuthority != contract.Authority {
					return openAIToolManifest{}, fmt.Errorf("admitted OpenAI tool %q policy drift: got %q want %q", name, wantAuthority, contract.Authority)
				}
			}
			source, ok := tool["parameters"].(map[string]any)
			if !ok {
				return openAIToolManifest{}, fmt.Errorf("admitted OpenAI tool %q has no object schema", name)
			}
			strict, err := compileOpenAIStrictSchema(source, true)
			if err != nil {
				return openAIToolManifest{}, fmt.Errorf("compile admitted OpenAI tool %q: %w", name, err)
			}
			raw, err := canonicalJSON(strict)
			if err != nil {
				return openAIToolManifest{}, fmt.Errorf("marshal admitted OpenAI tool %q schema: %w", name, err)
			}
			digest := sha256.Sum256(raw)
			entry.SchemaSHA256 = hex.EncodeToString(digest[:])
			entry.Parameters = strict
			entry.Source = source
			entry.Normalization, err = openAIToolNormalizationMap(source)
			if err != nil {
				return openAIToolManifest{}, fmt.Errorf("freeze admitted OpenAI tool %q normalization: %w", name, err)
			}
		}
		manifest.Tools = append(manifest.Tools, entry)
	}
	for name := range openAIAdmittedToolNames {
		if _, ok := catalog[name]; !ok {
			return openAIToolManifest{}, fmt.Errorf("admitted OpenAI tool %q is absent from source catalogs", name)
		}
	}
	digestible := manifest
	digestible.DigestSHA256 = ""
	raw, err := canonicalJSON(digestible)
	if err != nil {
		return openAIToolManifest{}, fmt.Errorf("canonicalize OpenAI tool manifest: %w", err)
	}
	digest := sha256.Sum256(raw)
	manifest.DigestSHA256 = hex.EncodeToString(digest[:])
	if manifest.DigestSHA256 != openAIToolManifestV1SHA256 {
		return openAIToolManifest{}, fmt.Errorf("OpenAI tool manifest drift: got %s want %s", manifest.DigestSHA256, openAIToolManifestV1SHA256)
	}
	return manifest, nil
}

func openAIToolNormalizationMap(source map[string]any) (map[string]string, error) {
	properties, ok := source["properties"].(map[string]any)
	if !ok {
		return nil, errors.New("tool source properties must be an object")
	}
	required, err := openAIToolStringSet(source["required"])
	if err != nil {
		return nil, err
	}
	normalization := make(map[string]string, len(properties))
	for name := range properties {
		if required[name] {
			normalization[name] = "required-reject-null"
		} else {
			normalization[name] = "nullable-wire-strip-null-before-default-and-digest"
		}
	}
	return normalization, nil
}

func addOpenAIToolCatalogEntry(catalog map[string]map[string]any, tool map[string]any) error {
	name := strings.TrimSpace(asString(tool["name"]))
	if name == "" {
		return errors.New("OpenAI tool catalog contains an unnamed tool")
	}
	if _, exists := catalog[name]; exists {
		return fmt.Errorf("OpenAI tool catalog contains duplicate tool %q", name)
	}
	catalog[name] = tool
	return nil
}

func (manifest openAIToolManifest) admitted(name string) (openAIToolManifestEntry, bool) {
	name = strings.TrimSpace(name)
	for _, entry := range manifest.Tools {
		if entry.Name == name && entry.Admitted {
			return entry, true
		}
	}
	return openAIToolManifestEntry{}, false
}

func (manifest openAIToolManifest) responsesTools() []map[string]any {
	tools := make([]map[string]any, 0, len(openAIAdmittedToolNames))
	for _, entry := range manifest.Tools {
		if !entry.Admitted {
			continue
		}
		tools = append(tools, map[string]any{
			"type":        "function",
			"name":        entry.Name,
			"description": entry.Description,
			"parameters":  entry.Parameters,
			"strict":      true,
		})
	}
	return tools
}

var openAIIntegerPattern = regexp.MustCompile(`^-?(0|[1-9][0-9]*)$`)

func compileOpenAIStrictSchema(source map[string]any, root bool) (map[string]any, error) {
	allowed := map[string]bool{"type": true, "description": true, "enum": true, "properties": true, "required": true, "additionalProperties": true, "items": true, "minimum": true, "maximum": true}
	for key := range source {
		if !allowed[key] {
			return nil, fmt.Errorf("unsupported schema keyword %q", key)
		}
	}
	typeName, ok := source["type"].(string)
	if !ok || strings.TrimSpace(typeName) == "" {
		return nil, errors.New("schema type must be one non-null string")
	}
	typeName = strings.TrimSpace(typeName)
	if typeName != "object" && typeName != "array" && typeName != "string" && typeName != "integer" && typeName != "number" && typeName != "boolean" {
		return nil, fmt.Errorf("unsupported schema type %q", typeName)
	}
	out := map[string]any{"type": typeName}
	for _, key := range []string{"description", "enum", "minimum", "maximum"} {
		if value, exists := source[key]; exists {
			out[key] = value
		}
	}
	if typeName == "object" {
		properties, ok := source["properties"].(map[string]any)
		if !ok {
			return nil, errors.New("object schema properties must be an object")
		}
		requiredSource, err := openAIToolStringSet(source["required"])
		if err != nil {
			return nil, err
		}
		if len(requiredSource) > len(properties) {
			return nil, errors.New("required contains a property absent from properties")
		}
		compiled := make(map[string]any, len(properties))
		required := make([]string, 0, len(properties))
		for name, raw := range properties {
			child, ok := raw.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("property %q schema must be an object", name)
			}
			strict, err := compileOpenAIStrictSchema(child, false)
			if err != nil {
				return nil, fmt.Errorf("property %q: %w", name, err)
			}
			if !requiredSource[name] {
				strict["type"] = []any{strict["type"], "null"}
			}
			compiled[name] = strict
			required = append(required, name)
		}
		for name := range requiredSource {
			if _, exists := properties[name]; !exists {
				return nil, fmt.Errorf("required property %q is absent", name)
			}
		}
		sort.Strings(required)
		out["properties"] = compiled
		out["required"] = required
		out["additionalProperties"] = false
		if value, exists := source["additionalProperties"]; exists && value != false {
			return nil, errors.New("additionalProperties must be false")
		}
	} else if typeName == "array" {
		items, ok := source["items"].(map[string]any)
		if !ok {
			return nil, errors.New("array schema items must be an object")
		}
		strict, err := compileOpenAIStrictSchema(items, false)
		if err != nil {
			return nil, fmt.Errorf("array items: %w", err)
		}
		out["items"] = strict
	}
	if root && typeName != "object" {
		return nil, errors.New("tool root schema must be an object")
	}
	return out, nil
}

func openAIToolStringSet(value any) (map[string]bool, error) {
	result := map[string]bool{}
	switch values := value.(type) {
	case nil:
		return result, nil
	case []string:
		for _, item := range values {
			if strings.TrimSpace(item) == "" || result[item] {
				return nil, errors.New("required contains an empty or duplicate property")
			}
			result[item] = true
		}
	case []any:
		for _, item := range values {
			name, ok := item.(string)
			if !ok || strings.TrimSpace(name) == "" || result[name] {
				return nil, errors.New("required must contain unique non-empty strings")
			}
			result[name] = true
		}
	default:
		return nil, errors.New("required must be an array")
	}
	return result, nil
}

// decodeOpenAIToolArguments rejects duplicate keys before schema validation.
// encoding/json otherwise keeps the last duplicate, which would make the
// operation alias and the executed arguments disagree across parsers.
func decodeOpenAIToolArguments(raw []byte) (map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	value, err := decodeUniqueJSONValue(decoder)
	if err != nil {
		return nil, err
	}
	if token, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("unexpected trailing JSON token %v", token)
		}
		return nil, fmt.Errorf("read trailing JSON: %w", err)
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, errors.New("tool arguments must be a JSON object")
	}
	return object, nil
}

func decodeUniqueJSONValue(decoder *json.Decoder) (any, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	delim, composite := token.(json.Delim)
	if !composite {
		return token, nil
	}
	switch delim {
	case '{':
		object := map[string]any{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return nil, err
			}
			key, ok := keyToken.(string)
			if !ok {
				return nil, errors.New("object key is not a string")
			}
			if _, exists := object[key]; exists {
				return nil, fmt.Errorf("duplicate JSON key %q", key)
			}
			value, err := decodeUniqueJSONValue(decoder)
			if err != nil {
				return nil, err
			}
			object[key] = value
		}
		if closeToken, err := decoder.Token(); err != nil || closeToken != json.Delim('}') {
			return nil, errors.New("unterminated JSON object")
		}
		return object, nil
	case '[':
		var array []any
		for decoder.More() {
			value, err := decodeUniqueJSONValue(decoder)
			if err != nil {
				return nil, err
			}
			array = append(array, value)
		}
		if closeToken, err := decoder.Token(); err != nil || closeToken != json.Delim(']') {
			return nil, errors.New("unterminated JSON array")
		}
		return array, nil
	default:
		return nil, fmt.Errorf("unexpected JSON delimiter %q", delim)
	}
}

func normalizeOpenAIToolArguments(entry openAIToolManifestEntry, args map[string]any) (map[string]any, error) {
	if !entry.Admitted || entry.Source == nil {
		return nil, fmt.Errorf("OpenAI tool %q is not admitted", entry.Name)
	}
	value, err := validateOpenAIToolValue(entry.Source, args, true)
	if err != nil {
		return nil, fmt.Errorf("validate OpenAI tool %q arguments: %w", entry.Name, err)
	}
	return value.(map[string]any), nil
}

func validateOpenAIToolValue(schema map[string]any, value any, root bool) (any, error) {
	typeName := asString(schema["type"])
	if value == nil {
		return nil, nil
	}
	switch typeName {
	case "object":
		object, ok := value.(map[string]any)
		if !ok {
			return nil, errors.New("expected object")
		}
		properties, _ := schema["properties"].(map[string]any)
		required, err := openAIToolStringSet(schema["required"])
		if err != nil {
			return nil, err
		}
		result := make(map[string]any, len(object))
		for name, childValue := range object {
			childSchema, exists := properties[name]
			if !exists {
				return nil, fmt.Errorf("unknown property %q", name)
			}
			if childValue == nil {
				if required[name] {
					return nil, fmt.Errorf("required property %q cannot be null", name)
				}
				continue
			}
			validated, err := validateOpenAIToolValue(childSchema.(map[string]any), childValue, false)
			if err != nil {
				return nil, fmt.Errorf("property %q: %w", name, err)
			}
			result[name] = validated
		}
		for name := range required {
			if _, exists := result[name]; !exists {
				return nil, fmt.Errorf("required property %q is absent", name)
			}
		}
		for name := range properties {
			if _, exists := object[name]; !exists {
				return nil, fmt.Errorf("strict-wire property %q is absent", name)
			}
		}
		return result, nil
	case "array":
		array, ok := value.([]any)
		if !ok {
			return nil, errors.New("expected array")
		}
		items := schema["items"].(map[string]any)
		result := make([]any, 0, len(array))
		for index, child := range array {
			validated, err := validateOpenAIToolValue(items, child, false)
			if err != nil {
				return nil, fmt.Errorf("item %d: %w", index, err)
			}
			result = append(result, validated)
		}
		return result, nil
	case "string":
		text, ok := value.(string)
		if !ok {
			return nil, errors.New("expected string")
		}
		if enum, exists := schema["enum"]; exists && !jsonEnumContains(enum, text) {
			return nil, fmt.Errorf("value %q is outside enum", text)
		}
		return text, nil
	case "integer":
		number, ok := value.(json.Number)
		if !ok || !openAIIntegerPattern.MatchString(number.String()) {
			return nil, errors.New("expected canonical integer")
		}
		parsed, err := number.Int64()
		if err != nil {
			return nil, errors.New("integer is outside int64")
		}
		if minimum, ok := jsonNumericInt64(schema["minimum"]); ok && parsed < minimum {
			return nil, fmt.Errorf("integer is below minimum %d", minimum)
		}
		if maximum, ok := jsonNumericInt64(schema["maximum"]); ok && parsed > maximum {
			return nil, fmt.Errorf("integer is above maximum %d", maximum)
		}
		return number, nil
	case "number":
		number, ok := value.(json.Number)
		if !ok {
			return nil, errors.New("expected number")
		}
		canonical, err := canonicalJSON(number)
		if err != nil || string(canonical) != number.String() {
			return nil, errors.New("expected canonical RFC 8785 number")
		}
		return number, nil
	case "boolean":
		if _, ok := value.(bool); !ok {
			return nil, errors.New("expected boolean")
		}
		return value, nil
	default:
		if root {
			return nil, fmt.Errorf("unsupported root type %q", typeName)
		}
		return nil, fmt.Errorf("unsupported type %q", typeName)
	}
}

func jsonEnumContains(raw any, value string) bool {
	switch enum := raw.(type) {
	case []string:
		for _, candidate := range enum {
			if candidate == value {
				return true
			}
		}
	case []any:
		for _, candidate := range enum {
			if text, ok := candidate.(string); ok && text == value {
				return true
			}
		}
	}
	return false
}

func jsonNumericInt64(value any) (int64, bool) {
	switch number := value.(type) {
	case int:
		return int64(number), true
	case int64:
		return number, true
	case float64:
		return int64(number), number == float64(int64(number))
	case json.Number:
		parsed, err := number.Int64()
		return parsed, err == nil
	default:
		return 0, false
	}
}
