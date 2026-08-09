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

func TestProductionComposeRequiresReceiptedW4LiveEnvironment(t *testing.T) {
	raw, err := os.ReadFile("deploy/digitalocean/docker-compose.yml")
	if err != nil {
		t.Fatal(err)
	}
	compose := string(raw)
	for _, required := range []string{
		"STRIDE_E10_W4_MODE: ${STRIDE_E10_W4_RELEASE_MODE:?STRIDE_E10_W4_RELEASE_MODE is required}",
		"STRIDE_E10_W4_SNAPSHOT_PATH: ${STRIDE_E10_W4_SNAPSHOT_PATH:?STRIDE_E10_W4_SNAPSHOT_PATH is required}",
		"STRIDE_E10_W4_ACTIVATION_BACKUP_DIR: ${STRIDE_E10_W4_ACTIVATION_BACKUP_DIR:?STRIDE_E10_W4_ACTIVATION_BACKUP_DIR is required}",
		"STRIDE_E10_W4_ACTIVATION_RECEIPT_PATH: ${STRIDE_E10_W4_ACTIVATION_RECEIPT_PATH:?STRIDE_E10_W4_ACTIVATION_RECEIPT_PATH is required}",
	} {
		if !strings.Contains(compose, required) {
			t.Fatalf("production Compose is missing receipted W4 binding %q", required)
		}
	}
	if strings.Contains(compose, ":-bonfire_network_live") {
		t.Fatal("production Compose must not default into W4 live mode")
	}
	policyRaw, err := os.ReadFile("deploy/digitalocean/stride-e10-w4-deployment-policy.json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(policyRaw), `"releaseMode": "bonfire_network_live"`) {
		t.Fatal("second-hop deployment policy is not the receipted W4 live mode")
	}
}
