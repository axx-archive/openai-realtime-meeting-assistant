package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/openai/openai-realtime-meeting-assistant/internal/w2driver"
	"github.com/openai/openai-realtime-meeting-assistant/internal/w2gate"
)

func main() {
	release := flag.String("release-commit", "", "full release Git commit")
	manifestPath := flag.String("manifest", "testdata/w2/gates.json", "checked W2 manifest")
	flag.Parse()
	if w2gate.ValidateReleaseCommit(*release) != nil {
		fatal(fmt.Errorf("full release commit is required"))
	}
	root, err := os.Getwd()
	if err != nil {
		fatal(err)
	}
	if err := w2gate.VerifyReleaseCheckout(context.Background(), root, *release); err != nil {
		fatal(err)
	}
	manifest, _, err := w2gate.LoadManifest(filepath.Join(root, *manifestPath))
	if err != nil {
		fatal(err)
	}
	client, err := w2driver.LoadCheckedClient(root, manifest, os.Getenv("BONFIRE_W2D_COLLECTOR_URL"), os.Getenv("BONFIRE_W2D_COLLECTOR_TOKEN"))
	if err != nil {
		fatal(err)
	}
	result, err := client.RunSystemProbe(context.Background(), "production-recall-replay", *release)
	if err != nil {
		fatal(err)
	}
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		fatal(err)
	}
}
func fatal(err error) { _, _ = fmt.Fprintln(os.Stderr, "w2-recall-replay:", err); os.Exit(1) }
