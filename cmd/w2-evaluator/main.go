package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/openai/openai-realtime-meeting-assistant/internal/w2driver"
	"github.com/openai/openai-realtime-meeting-assistant/internal/w2gate"
)

func main() {
	gateID := flag.String("gate", "", "checked W2D gate id")
	corpusPath := flag.String("corpus-manifest", "", "checked body-free corpus manifest")
	releaseCommit := flag.String("release-commit", "", "full release Git commit")
	manifestPath := flag.String("manifest", "testdata/w2/gates.json", "checked W2D manifest")
	flag.Parse()
	startedAt := time.Now().UTC()
	if *gateID == "" || *corpusPath == "" || w2gate.ValidateReleaseCommit(*releaseCommit) != nil {
		fatal(errorsNew("gate, corpus manifest, and full release commit are required"))
	}
	root, err := os.Getwd()
	if err != nil {
		fatal(err)
	}
	if err := w2gate.VerifyReleaseCheckout(context.Background(), root, *releaseCommit); err != nil {
		fatal(err)
	}
	manifest, digest, err := w2gate.LoadManifest(filepath.Join(root, *manifestPath))
	if err != nil {
		fatal(err)
	}
	gate, ok := manifest.Gate(*gateID)
	if !ok || gate.Corpus.ManifestPath != *corpusPath {
		fatal(errorsNew("gate/corpus binding is not checked by the manifest"))
	}
	// The evaluator never receives the receipt HMAC key. Corpus admission is
	// performed by the parent gate before this process starts.
	corpus, err := loadCheckedCorpus(root, gate)
	if err != nil {
		fatal(err)
	}
	client, err := w2driver.LoadCheckedClient(root, manifest, os.Getenv("BONFIRE_W2D_COLLECTOR_URL"), os.Getenv("BONFIRE_W2D_COLLECTOR_TOKEN"))
	if err != nil {
		fatal(err)
	}
	observation, err := client.CollectGate(context.Background(), digest, gate, corpus, *releaseCommit, startedAt)
	if err != nil {
		fatal(err)
	}
	encoder := json.NewEncoder(os.Stdout)
	if err := encoder.Encode(observation); err != nil {
		fatal(err)
	}
}

func loadCheckedCorpus(root string, gate w2gate.GateDefinition) (w2gate.CorpusManifest, error) {
	raw, err := os.ReadFile(filepath.Join(root, gate.Corpus.ManifestPath))
	if err != nil {
		return w2gate.CorpusManifest{}, err
	}
	if w2gate.DigestBytes(raw) != gate.Corpus.Digest {
		return w2gate.CorpusManifest{}, errorsNew("corpus bytes do not match the parent-admitted manifest")
	}
	var corpus w2gate.CorpusManifest
	if err := json.Unmarshal(raw, &corpus); err != nil {
		return w2gate.CorpusManifest{}, err
	}
	return corpus, nil
}

func errorsNew(message string) error { return fmt.Errorf("%s", message) }
func fatal(err error)                { _, _ = fmt.Fprintln(os.Stderr, "w2-evaluator:", err); os.Exit(1) }
