package main

import (
	"os"
	"strings"
	"testing"
)

func TestProductionComposeRequiresTURNSecret(t *testing.T) {
	raw, err := os.ReadFile("deploy/digitalocean/docker-compose.yml")
	if err != nil {
		t.Fatal(err)
	}
	compose := string(raw)
	if !strings.Contains(compose, "${MEETING_TURN_SECRET:?MEETING_TURN_SECRET is required}") {
		t.Fatal("production coturn must fail closed when MEETING_TURN_SECRET is absent")
	}
	if strings.Contains(compose, "meetingassist-dev-turn-secret") || strings.Contains(compose, "MEETING_TURN_SECRET:-") {
		t.Fatal("production coturn must not carry a known or optional TURN secret")
	}
}

func TestProductionComposeExcludesInProcessCodexRunner(t *testing.T) {
	raw, err := os.ReadFile("deploy/digitalocean/docker-compose.yml")
	if err != nil {
		t.Fatal(err)
	}
	compose := string(raw)
	for _, forbidden := range []string{"  codex-runner:\n", "target: codex-runner", "codex_runner_cache", "BONFIRE_CODEX_EXTERNAL_WRITE_ENABLED"} {
		if strings.Contains(compose, forbidden) {
			t.Fatalf("production Compose regained removed in-process runner marker %q", forbidden)
		}
	}
}

func TestProductionComposePreservesFirstHopCanaryEnvironmentShape(t *testing.T) {
	raw, err := os.ReadFile("deploy/digitalocean/docker-compose.yml")
	if err != nil {
		t.Fatal(err)
	}
	compose := string(raw)
	for _, forbidden := range []string{"STRIDE_E10_W4_RELEASE_MODE", "STRIDE_E10_W4_ACTIVATION_BACKUP_DIR", "STRIDE_E10_W4_ACTIVATION_RECEIPT_PATH"} {
		if strings.Contains(compose, forbidden) {
			t.Fatalf("compatibility canary widened the legacy Compose environment with %s", forbidden)
		}
	}
	policyRaw, err := os.ReadFile("deploy/digitalocean/stride-e10-w4-deployment-policy.json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(policyRaw), `"releaseMode": "canary"`) {
		t.Fatal("first-hop deployment policy is not the compatibility canary")
	}
}
