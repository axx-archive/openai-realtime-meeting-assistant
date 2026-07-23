package main

import (
	"context"
	"crypto/ed25519"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/openai/openai-realtime-meeting-assistant/internal/w2collector"
	"github.com/openai/openai-realtime-meeting-assistant/internal/w2driver"
	"github.com/openai/openai-realtime-meeting-assistant/internal/w2gate"
)

func main() {
	manifestPath := flag.String("manifest", "testdata/w2/gates.json", "checked W2 manifest")
	releaseCommit := flag.String("release-commit", "", "full release Git commit")
	bundleDir := flag.String("bundle-dir", "", "directory containing retained body-free evidence bundles")
	privateKeyFile := flag.String("private-key-file", "", "independently custodied collector Ed25519 private key")
	tokenFile := flag.String("token-file", "", "collector bearer-token file")
	listen := flag.String("listen", "127.0.0.1:8099", "collector listen address")
	flag.Parse()

	root, err := os.Getwd()
	if err != nil {
		fatal(err)
	}
	if w2gate.ValidateReleaseCommit(*releaseCommit) != nil || strings.TrimSpace(*bundleDir) == "" || strings.TrimSpace(*privateKeyFile) == "" || strings.TrimSpace(*tokenFile) == "" {
		fatal(fmt.Errorf("release commit, bundle dir, private key, and token file are required"))
	}
	actual, err := checkoutCommit(root)
	if err != nil || actual != *releaseCommit {
		fatal(fmt.Errorf("collector checkout is not release commit %s", *releaseCommit))
	}
	if err := w2gate.VerifyReleaseCheckout(context.Background(), root, *releaseCommit); err != nil {
		fatal(err)
	}
	manifest, _, err := w2gate.LoadManifest(filepath.Join(root, *manifestPath))
	if err != nil {
		fatal(err)
	}
	implementationDigest, err := manifest.VerifyEvidenceFiles(root)
	if err != nil {
		fatal(err)
	}
	privateKey, err := w2driver.LoadPrivateKey(*privateKeyFile, manifest.Evidence.CollectorKey.KeyID)
	if err != nil {
		fatal(err)
	}
	publicKey, err := w2driver.LoadCollectorPublicKey(root, manifest)
	if err != nil || !ed25519.PublicKey(privateKey.Public().(ed25519.PublicKey)).Equal(publicKey) {
		fatal(fmt.Errorf("collector private key does not match pinned release public key"))
	}
	token, err := os.ReadFile(*tokenFile)
	if err != nil {
		fatal(err)
	}
	server := &w2collector.Server{
		ReleaseCommit: *releaseCommit, CollectorImplementationDigest: implementationDigest,
		KeyID: manifest.Evidence.CollectorKey.KeyID, PrivateKey: privateKey,
		Token: strings.TrimSpace(string(token)), BundleDir: *bundleDir,
	}
	if err := server.Validate(); err != nil {
		fatal(err)
	}
	if err := http.ListenAndServe(*listen, server); err != nil {
		fatal(err)
	}
}

func checkoutCommit(root string) (string, error) {
	command := exec.Command("git", "rev-parse", "HEAD")
	command.Dir = root
	raw, err := command.Output()
	return strings.TrimSpace(string(raw)), err
}

func fatal(err error) {
	_, _ = fmt.Fprintln(os.Stderr, "w2-collector:", err)
	os.Exit(1)
}
