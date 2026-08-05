package main

import (
	"image/png"
	"os"
	"strings"
	"testing"
)

func TestSettingsMemoryImportIsPrivateReviewableAndThemeBranded(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	source := string(raw)
	for _, marker := range []string{
		`--wordmark-image: url(/public/stride-wordmark-black.png)`,
		`--wordmark-image: url(/public/stride-wordmark-orange.png)`,
		`background: var(--wordmark-image) no-repeat center / contain`,
		`id="memoryImportDialog"`,
		`Import memory to STRIDE`,
		`Instructions`,
		`Identity`,
		`Career`,
		`Projects`,
		`Preferences`,
		`Do not include passwords, credentials, payment details, medical information, or private data about other people.`,
		`Saved entries are private to you by default.`,
		`Imported memory is separate from Drive and company artifacts.`,
		`Public chats and recorded meetings build separate, attributed company context`,
		`function parseSTRIDEMemoryImport(raw)`,
		`One import can contain up to 200 memories`,
		`/api/stride/v1/coworker/relationships/import`,
		`Memory is updating`,
		`expectedRevision: relationshipMemorySnapshot.revision`,
		`alreadyPresentCount`,
		`saved until you remove it`,
		`relationship_memory_changed`,
	} {
		if !strings.Contains(source, marker) {
			t.Fatalf("Settings memory import missing %q", marker)
		}
	}
	if strings.Contains(source, `importSTRIDEMemories()`+"\n"+`        const parsed`) {
		t.Fatal("import invocation was serialized into the page instead of implemented as a function")
	}

	for _, path := range []string{"public/stride-wordmark-orange.png", "public/stride-wordmark-black.png"} {
		file, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		config, err := png.DecodeConfig(file)
		_ = file.Close()
		if err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
		if config.Width < config.Height*3 || config.Width > 1600 || config.Height > 500 {
			t.Fatalf("%s was not cropped to a usable wordmark: %dx%d", path, config.Width, config.Height)
		}
	}
}
