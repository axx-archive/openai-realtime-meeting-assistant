package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/openai/openai-realtime-meeting-assistant/internal/w2gate"
)

func main() {
	manifestPath := flag.String("manifest", "testdata/w2/gates.json", "path to the W2 gate manifest")
	gateID := flag.String("gate", "", "trusted live gate driver to execute")
	checkID := flag.String("check", "", "integrated release check to execute")
	releaseCommit := flag.String("release-commit", "", "full release-candidate Git SHA")
	keyFile := flag.String("authority-key-file", "", "path to the HMAC authority key (minimum 32 bytes)")
	keyID := flag.String("authority-key-id", "", "custodied HMAC authority key id")
	verifyReceipts := flag.Bool("verify-receipts", false, "verify all integrated checks and nine W2D receipts for the release")
	flag.Parse()

	if *keyFile == "" || *keyID == "" {
		fatal(fmt.Errorf("--authority-key-file and --authority-key-id are required"))
	}
	authority, err := w2gate.LoadAuthority(*keyFile, *keyID)
	if err != nil {
		fatal(err)
	}
	root, err := os.Getwd()
	if err != nil {
		fatal(err)
	}
	absoluteManifest := *manifestPath
	if !filepath.IsAbs(absoluteManifest) {
		absoluteManifest = filepath.Join(root, absoluteManifest)
	}
	manifest, manifestDigest, err := w2gate.LoadManifest(absoluteManifest)
	if err != nil {
		fatal(err)
	}
	runner := w2gate.Runner{Root: root, Manifest: manifest, ManifestDigest: manifestDigest, Authority: authority}
	if *verifyReceipts {
		if *gateID != "" || *checkID != "" {
			fatal(fmt.Errorf("--verify-receipts cannot be combined with --gate or --check"))
		}
		summary, err := runner.VerifyReceiptSet(*releaseCommit)
		if err != nil {
			fatal(err)
		}
		writeJSON(summary)
		return
	}
	if (*gateID == "") == (*checkID == "") {
		fatal(fmt.Errorf("choose exactly one of --gate or --check"))
	}
	if *gateID != "" {
		receipt, outputPath, err := runner.RunGate(context.Background(), *gateID, *releaseCommit)
		if err != nil {
			fatal(err)
		}
		writeJSON(struct {
			ReceiptPath string         `json:"receiptPath"`
			Receipt     w2gate.Receipt `json:"receipt"`
		}{outputPath, receipt})
		return
	}
	receipt, outputPath, err := runner.RunCheck(context.Background(), *checkID, *releaseCommit)
	if err != nil {
		fatal(err)
	}
	writeJSON(struct {
		ReceiptPath string              `json:"receiptPath"`
		Receipt     w2gate.CheckReceipt `json:"receipt"`
	}{outputPath, receipt})
}

func writeJSON(value any) {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		fatal(err)
	}
}
func fatal(err error) { _, _ = fmt.Fprintln(os.Stderr, "w2gate:", err); os.Exit(1) }
