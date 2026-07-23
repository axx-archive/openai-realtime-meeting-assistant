package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRestoreGateDefaultsOffAndFailsClosedWhenArmed(t *testing.T) {
	priorMarkerPath := restoreProfileMarkerPath
	markerRoot, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	restoreProfileMarkerPath = filepath.Join(markerRoot, "missing-restore-marker")
	t.Cleanup(func() { restoreProfileMarkerPath = priorMarkerPath })
	t.Setenv("BONFIRE_RESTORE_MODE", "off")
	if err := initializeRestoreGate(time.Date(2026, 7, 22, 21, 0, 0, 0, time.UTC)); err != nil || restoreGate.enabled || !restoreGate.ready {
		t.Fatalf("off gate=%+v err=%v", restoreGate, err)
	}
	t.Setenv("BONFIRE_RESTORE_MODE", "isolated")
	t.Setenv("BONFIRE_DR_RESTORE_RECEIPT_PUBLIC_KEY_ID", "")
	t.Setenv("BONFIRE_DR_RESTORE_RECEIPT_PUBLIC_KEY", "")
	if err := initializeRestoreGate(time.Date(2026, 7, 22, 21, 0, 0, 0, time.UTC)); err == nil || restoreGate.ready {
		t.Fatalf("armed gate did not fail closed: gate=%+v err=%v", restoreGate, err)
	}
}

func TestRestoreProfileMarkerRefusesOmittedMode(t *testing.T) {
	priorMarkerPath := restoreProfileMarkerPath
	markerRoot, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	restoreProfileMarkerPath = filepath.Join(markerRoot, "restore-profile-v1")
	t.Cleanup(func() { restoreProfileMarkerPath = priorMarkerPath })
	if err := os.WriteFile(restoreProfileMarkerPath, []byte(restoreProfileMarkerValue+"\n"), 0o444); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BONFIRE_RESTORE_MODE", "")
	err = initializeRestoreGate(time.Date(2026, 7, 22, 21, 0, 0, 0, time.UTC))
	if err == nil || !strings.Contains(err.Error(), "requires BONFIRE_RESTORE_MODE=isolated") || restoreGate.ready {
		t.Fatalf("restore profile booted without isolated mode: gate=%+v err=%v", restoreGate, err)
	}
}
