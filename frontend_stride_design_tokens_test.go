package main

import (
	"os/exec"
	"testing"
)

// Drift between platforms is a release failure, even when each app compiles.
func TestStrideDesignExportsAndContrastRemainCurrent(t *testing.T) {
	cmd := exec.Command("node", "scripts/generate-stride-design-tokens.mjs", "--check")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("shared design export/contrast check: %v\n%s", err, out)
	}
}
