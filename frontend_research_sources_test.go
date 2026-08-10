package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestResearchSourceMarkdownURLsExcludePresentationBackticks(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(raw)
	start := strings.Index(html, "function normalizeArtifactSourceURL(rawUrl)")
	end := strings.Index(html[start:], "function researchArtifactSources(entry)")
	if start < 0 || end < 0 {
		t.Fatal("could not isolate the production research source parser")
	}
	parser := html[start : start+end]
	fixture := map[string]string{
		"text": "## Sources\n" +
			"- [Primary filing](https://example.com/filing)\n" +
			"- Receipt source — https://receipt.example/report\n" +
			"- Inline code citation `https://ticks.example/source`\n" +
			"- Punctuated https://punctuation.example/item.,;\n" +
			"- Duplicate https://example.com/filing\n" +
			"- Not a source javascript:alert(1)",
	}
	encoded, err := json.Marshal(fixture)
	if err != nil {
		t.Fatal(err)
	}
	script := parser + "\nprocess.stdout.write(JSON.stringify(artifactSources(" + string(encoded) + ")));"
	output, err := exec.Command("node", "-e", script).CombinedOutput()
	if err != nil {
		t.Fatalf("execute production parser: %v\n%s", err, output)
	}
	var got []struct {
		URL  string `json:"url"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(output, &got); err != nil {
		t.Fatalf("decode parser output: %v\n%s", err, output)
	}
	want := []struct {
		URL  string
		Name string
	}{
		{URL: "https://example.com/filing", Name: "Primary filing"},
		{URL: "https://receipt.example/report", Name: "receipt.example"},
		{URL: "https://ticks.example/source", Name: "ticks.example"},
		{URL: "https://punctuation.example/item", Name: "punctuation.example"},
	}
	if len(got) != len(want) {
		t.Fatalf("sources=%+v, want %+v", got, want)
	}
	for index := range want {
		if got[index].URL != want[index].URL || got[index].Name != want[index].Name {
			t.Fatalf("source[%d]=%+v, want %+v", index, got[index], want[index])
		}
		if strings.Contains(got[index].URL, "`") {
			t.Fatalf("source[%d] retained Markdown delimiter: %q", index, got[index].URL)
		}
	}
}

func TestRichReportSourceRenderersUseOnlyParsedSourceURL(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(raw)
	for _, signature := range []string{
		"function artifactReportSourcesNode(sources)",
		"function artifactReadSourceNode(source)",
		"function researchSourceLink(source)",
	} {
		start := strings.Index(html, signature)
		if start < 0 {
			t.Fatalf("missing rich source renderer %q", signature)
		}
		body := functionBodyAfterSignature(html[start:], signature)
		if !strings.Contains(body, "link.href = source.url") || !strings.Contains(body, "source.url") {
			t.Fatalf("renderer %q does not bind display and href to the parsed source URL", signature)
		}
	}
}
