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
	workflow := flag.String("workflow", "", "checked workflow pilot")
	release := flag.String("release-commit", "", "full release Git commit")
	manifestPath := flag.String("manifest", "testdata/w2/gates.json", "checked W2 manifest")
	flag.Parse()
	if *workflow != "insights-opportunities" || w2gate.ValidateReleaseCommit(*release) != nil {
		fatal(fmt.Errorf("checked workflow and full release commit are required"))
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
	result, err := client.RunSystemProbe(context.Background(), "workflow-pilot:"+*workflow, *release)
	if err != nil {
		fatal(err)
	}
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		fatal(err)
	}
}
func fatal(err error) { _, _ = fmt.Fprintln(os.Stderr, "w2-workflow-pilot:", err); os.Exit(1) }
