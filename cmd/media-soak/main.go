package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/openai/openai-realtime-meeting-assistant/internal/mediasoak"
)

func main() {
	manifestPath := flag.String("manifest", "testdata/w2a/media-soak.json", "path to the checked W2A soak manifest")
	observationPath := flag.String("observation", "", "path to a signed, body-free soak observation")
	collect := flag.Bool("collect", false, "run the checked same-commit live collector")
	publicKeyPath := flag.String("public-key-file", "", "path to the trusted base64 Ed25519 public key")
	collectorPublicKeyPath := flag.String("collector-public-key-file", "", "path to the pinned independent collector Ed25519 public key")
	releasePrivateKeyPath := flag.String("release-private-key-file", "", "path to the release operator Ed25519 private key; never passed to the collector")
	releaseCommit := flag.String("release-commit", "", "full release-candidate Git SHA")
	verifyReceipt := flag.String("verify-receipt", "", "repository-relative receipt path to verify")
	flag.Parse()

	root, err := os.Getwd()
	if err != nil {
		fatal(err)
	}
	absoluteManifest := *manifestPath
	if !filepath.IsAbs(absoluteManifest) {
		absoluteManifest = filepath.Join(root, absoluteManifest)
	}
	manifest, manifestDigest, err := mediasoak.LoadManifest(absoluteManifest)
	if err != nil {
		fatal(err)
	}
	if strings.TrimSpace(*publicKeyPath) == "" || strings.TrimSpace(*collectorPublicKeyPath) == "" {
		fatal(fmt.Errorf("--public-key-file and --collector-public-key-file are required"))
	}
	publicKeyRaw, err := os.ReadFile(*publicKeyPath)
	if err != nil {
		fatal(err)
	}
	publicKey, err := mediasoak.ParsePublicKey(publicKeyRaw)
	if err != nil {
		fatal(err)
	}
	collectorPublicKeyRaw, err := os.ReadFile(*collectorPublicKeyPath)
	if err != nil {
		fatal(err)
	}
	collectorPublicKey, err := mediasoak.ParsePublicKey(collectorPublicKeyRaw)
	if err != nil {
		fatal(err)
	}
	runner := mediasoak.Runner{Root: root, Manifest: manifest, ManifestDigest: manifestDigest, CollectorPublicKey: collectorPublicKey}
	if strings.TrimSpace(*verifyReceipt) != "" {
		receipt, err := runner.VerifyReceipt(*verifyReceipt, *releaseCommit, publicKey)
		if err != nil {
			fatal(err)
		}
		writeJSON(receipt)
		if !receipt.ReleaseQualified {
			os.Exit(2)
		}
		return
	}
	if *collect == (strings.TrimSpace(*observationPath) != "") {
		fatal(fmt.Errorf("select exactly one of --collect or --observation unless --verify-receipt is set"))
	}
	var probe mediasoak.Probe
	if *collect {
		if strings.TrimSpace(*releasePrivateKeyPath) == "" {
			nonQualifying(*releaseCommit, fmt.Errorf("--release-private-key-file is required with --collect"))
		}
		privateRaw, err := os.ReadFile(*releasePrivateKeyPath)
		if err != nil {
			nonQualifying(*releaseCommit, err)
		}
		privateKey, err := mediasoak.ParsePrivateKey(privateRaw)
		if err != nil {
			nonQualifying(*releaseCommit, err)
		}
		probe = mediasoak.CommandProbe{
			Root: root, ReleaseKeyID: manifest.AllowedSignerKeyIDs[0], ReleasePrivateKey: privateKey,
		}
	} else {
		probe = mediasoak.FileProbe{Path: *observationPath}
	}
	receipt, outputPath, err := runner.Run(context.Background(), *releaseCommit, publicKey, probe)
	if err != nil {
		nonQualifying(*releaseCommit, err)
	}
	writeJSON(struct {
		ReceiptPath string            `json:"receiptPath"`
		Receipt     mediasoak.Receipt `json:"receipt"`
	}{ReceiptPath: outputPath, Receipt: receipt})
	if !receipt.ReleaseQualified {
		os.Exit(2)
	}
}

func nonQualifying(releaseCommit string, err error) {
	writeJSON(struct {
		Schema           string `json:"schema"`
		ReleaseCommit    string `json:"releaseCommit"`
		ReleaseQualified bool   `json:"releaseQualified"`
		StopTriggered    bool   `json:"stopTriggered"`
		Reason           string `json:"reason"`
	}{
		Schema: "bonfire.w2a.media-soak.non-qualifying.v1", ReleaseCommit: releaseCommit,
		ReleaseQualified: false, StopTriggered: true, Reason: err.Error(),
	})
	os.Exit(2)
}

func writeJSON(value any) {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	_, _ = fmt.Fprintln(os.Stderr, "media-soak:", err)
	os.Exit(1)
}
