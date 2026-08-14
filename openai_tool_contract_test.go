package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestOpenAIToolManifestV1ClosedAdmissionAndStrictSchemas(t *testing.T) {
	manifest, err := buildOpenAIToolManifest()
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Version != openAIToolManifestVersion || manifest.SchemaVersion != openAIToolSchemaVersion || len(manifest.DigestSHA256) != 64 {
		t.Fatalf("manifest identity is incomplete: %+v", manifest)
	}
	admitted := map[string]bool{}
	all := map[string]bool{}
	for _, entry := range manifest.Tools {
		if all[entry.Name] {
			t.Fatalf("duplicate manifest tool %q", entry.Name)
		}
		all[entry.Name] = true
		if !entry.Admitted {
			continue
		}
		admitted[entry.Name] = true
		if entry.SchemaSHA256 == "" || entry.PolicyRevision == "" || entry.PreimageContract == "" || entry.PostimageContract == "" || entry.ReconciliationContract == "" || entry.FinalUseContract == "" || entry.FanOutContract == "" || entry.MinimizedResult == "" || len(entry.RequiredSuites) != 1 {
			t.Fatalf("admitted tool %q lacks a frozen typed contract: %+v", entry.Name, entry)
		}
		properties := entry.Parameters["properties"].(map[string]any)
		required := entry.Parameters["required"].([]string)
		if len(required) != len(properties) || entry.Parameters["additionalProperties"] != false || len(entry.Normalization) != len(properties) {
			t.Fatalf("tool %q is not recursively strict: %+v", entry.Name, entry.Parameters)
		}
	}
	if len(admitted) != 4 {
		t.Fatalf("admitted tools=%v, want exactly four", admitted)
	}
	for name := range openAIAdmittedToolNames {
		if !admitted[name] {
			t.Fatalf("required admitted tool %q is absent", name)
		}
	}
	for _, name := range []string{"publish_artifact", "launch_agent_thread", "request_coworker_help", "initiate_goal"} {
		if !all[name] || admitted[name] {
			t.Fatalf("known tool %q is not explicitly unadmitted", name)
		}
	}
	if all["create_ticket"] {
		t.Fatal("retired Board mutation survived in the OpenAI tool catalog")
	}
	if len(manifest.responsesTools()) != 4 {
		t.Fatalf("provider saw %d tools, want four", len(manifest.responsesTools()))
	}
	t.Logf("OpenAI manifest v1 digest: %s", manifest.DigestSHA256)
}

func TestOpenAIToolStrictArgumentsRejectAmbiguityAndNormalizeNull(t *testing.T) {
	manifest, err := buildOpenAIToolManifest()
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := manifest.admitted("create_artifact")
	if !ok {
		t.Fatal("create_artifact was not admitted")
	}
	valid, err := decodeOpenAIToolArguments([]byte(`{"mode":"artifacts","query":"Save this","content":null}`))
	if err != nil {
		t.Fatal(err)
	}
	normalized, err := normalizeOpenAIToolArguments(entry, valid)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := normalized["content"]; exists {
		t.Fatalf("nullable optional field survived normalization: %+v", normalized)
	}
	for name, raw := range map[string]string{
		"duplicate":        `{"mode":"artifacts","query":"one","query":"two","content":null}`,
		"unknown":          `{"mode":"artifacts","query":"one","content":null,"model":"gpt-5.6-sol"}`,
		"missing_optional": `{"mode":"artifacts","query":"one"}`,
		"required_null":    `{"mode":null,"query":"one","content":null}`,
		"wrong_type":       `{"mode":"artifacts","query":7,"content":null}`,
	} {
		t.Run(name, func(t *testing.T) {
			decoded, decodeErr := decodeOpenAIToolArguments([]byte(raw))
			if decodeErr == nil {
				_, decodeErr = normalizeOpenAIToolArguments(entry, decoded)
			}
			if decodeErr == nil {
				t.Fatalf("ambiguous arguments were accepted: %s", raw)
			}
		})
	}
	report, _ := manifest.admitted(controlToolReportGoalState)
	for _, raw := range []string{
		`{"goal_status":null,"review_gate":null,"stage":null,"progress_percent":1e2,"note":null}`,
		`{"goal_status":null,"review_gate":null,"stage":null,"progress_percent":01,"note":null}`,
	} {
		decoded, decodeErr := decodeOpenAIToolArguments([]byte(raw))
		if decodeErr == nil {
			_, decodeErr = normalizeOpenAIToolArguments(report, decoded)
		}
		if decodeErr == nil {
			t.Fatalf("noncanonical integer was accepted: %s", raw)
		}
	}
	decoded, err := decodeOpenAIToolArguments([]byte(`{"goal_status":"running","review_gate":"pending","stage":"execute","progress_percent":42,"note":null}`))
	if err != nil {
		t.Fatal(err)
	}
	normalized, err = normalizeOpenAIToolArguments(report, decoded)
	if err != nil {
		t.Fatal(err)
	}
	if normalized["progress_percent"] != json.Number("42") || strings.TrimSpace(asString(normalized["note"])) != "" {
		t.Fatalf("canonical report arguments mismatch: %+v", normalized)
	}
}

func TestOpenAIToolMinimizedResultContractsRejectBodiesAndShapeDrift(t *testing.T) {
	valid := map[string]json.RawMessage{
		controlToolReportGoalState: json.RawMessage(`{"goal_status":"running","stage":"execute","receipt":"goal:1"}`),
		"answer_memory_question":   json.RawMessage(`{"answer":"Approved","sources":["memory:1"]}`),
		"create_artifact":          json.RawMessage(`{"artifact_id":"artifact-1","title":"Decision","type":"document","status":"created"}`),
		"update_artifact":          json.RawMessage(`{"artifact_id":"artifact-1","revision":"4","status":"updated"}`),
	}
	for tool, result := range valid {
		if err := validateOpenAIToolMinimizedResult(tool, result); err != nil {
			t.Fatalf("valid minimized %s result rejected: %v", tool, err)
		}
		var object map[string]any
		if err := json.Unmarshal(result, &object); err != nil {
			t.Fatal(err)
		}
		object["content"] = "secret body"
		unsafe, _ := json.Marshal(object)
		if err := validateOpenAIToolMinimizedResult(tool, unsafe); err == nil {
			t.Fatalf("%s result leaked an unbounded body field", tool)
		}
	}
}
