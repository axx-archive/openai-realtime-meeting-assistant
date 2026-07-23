package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/openai/openai-realtime-meeting-assistant/internal/dr"
)

const restoreProfileMarkerValue = "bonfire-restore-profile-v1"

var restoreProfileMarkerPath = "/run/bonfire-dr/restore-profile-v1"

var restoreGate = struct {
	enabled bool
	ready   bool
	reason  string
}{}

// initializeRestoreGate runs before any durable store is opened. A restored
// deployment therefore cannot expose application reads merely because its
// containers start or its ordinary health checks are green.
func initializeRestoreGate(now time.Time) error {
	restoreGate = struct {
		enabled bool
		ready   bool
		reason  string
	}{false, false, "restore_gate_uninitialized"}
	mode := strings.TrimSpace(os.Getenv("BONFIRE_RESTORE_MODE"))
	markerPresent, err := restoreProfileMarkerPresent()
	if err != nil {
		return fmt.Errorf("restore profile marker: %w", err)
	}
	if mode == "" || strings.EqualFold(mode, "off") {
		if markerPresent {
			return errors.New("restore profile marker requires BONFIRE_RESTORE_MODE=isolated")
		}
		restoreGate = struct {
			enabled bool
			ready   bool
			reason  string
		}{false, true, "not_restore_mode"}
		return nil
	}
	restoreGate.enabled = true
	restoreGate.ready = false
	restoreGate.reason = "restore_receipt_invalid"
	if mode != "isolated" {
		return errors.New("BONFIRE_RESTORE_MODE must be isolated or off")
	}
	if !markerPresent {
		return errors.New("isolated restore mode requires the immutable restore profile marker")
	}
	receiptPath := strings.TrimSpace(os.Getenv("BONFIRE_RESTORE_RECEIPT_PATH"))
	keyID := strings.TrimSpace(os.Getenv("BONFIRE_DR_RESTORE_RECEIPT_PUBLIC_KEY_ID"))
	keyRaw := strings.TrimSpace(os.Getenv("BONFIRE_DR_RESTORE_RECEIPT_PUBLIC_KEY"))
	verifier, err := dr.ParsePublicVerifier(keyID, dr.RoleReceipt, keyRaw)
	if err != nil {
		return fmt.Errorf("restore receipt verifier: %w", err)
	}
	var receipt dr.SecureDrillReceipt
	if err := readRestoreGateJSON(receiptPath, &receipt); err != nil {
		return fmt.Errorf("restore receipt: %w", err)
	}
	environmentID := strings.TrimSpace(os.Getenv("BONFIRE_RESTORE_ENVIRONMENT_ID"))
	nonce := os.Getenv("BONFIRE_RESTORE_NONCE")
	imagePin, err := dr.ReadPinnedDigestFile(os.Getenv("BONFIRE_RUNNING_IMAGE_DIGEST_PIN_PATH"))
	if err != nil {
		return fmt.Errorf("running OCI image pin: %w", err)
	}
	imageDigest := "sha256:" + imagePin
	authorityHead, err := dr.ReadPinnedDigestFile(os.Getenv("BONFIRE_DR_AUTHORITY_HEAD_PIN_PATH"))
	if err != nil {
		return fmt.Errorf("restore authority head pin: %w", err)
	}
	var manifest dr.SecureManifest
	if err := readRestoreGateJSON(os.Getenv("BONFIRE_RESTORE_MANIFEST_PATH"), &manifest); err != nil {
		return fmt.Errorf("restore manifest: %w", err)
	}
	var protected map[string]string
	if err := readRestoreGateJSON(os.Getenv("BONFIRE_DR_PROTECTED_ROOTS_PATH"), &protected); err != nil || len(protected) != 4 {
		return errors.New("restore protected-root configuration is missing or invalid")
	}
	roots := make([]string, 0, len(protected))
	sources := make([]dr.ArtifactSource, 0, len(protected))
	artifacts := make(map[string]dr.SnapshotArtifact, len(manifest.Artifacts))
	for _, artifact := range manifest.Artifacts {
		artifacts[artifact.Volume] = artifact
	}
	for _, volume := range []string{dr.VolumeCanonical, dr.VolumeData, dr.VolumeQueue, dr.VolumeUsage} {
		root := protected[volume]
		artifact, ok := artifacts[volume]
		if root == "" || !ok || !filepath.IsAbs(root) || filepath.Clean(root) != root {
			return fmt.Errorf("restore protected root %s is missing", volume)
		}
		if err := dr.ValidateArtifactRoot(root); err != nil {
			return fmt.Errorf("restore protected root %s: %w", volume, err)
		}
		roots = append(roots, root)
		sources = append(sources, dr.ArtifactSource{Volume: volume, SnapshotID: artifact.SnapshotID, Path: root})
	}
	databaseState, err := dr.QueryDatabaseState(context.Background(), os.Getenv("BONFIRE_CANONICAL_DATABASE_URL"), manifest.AuthorityHead.Tenants)
	if err != nil {
		return fmt.Errorf("restored canonical database: %w", err)
	}
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("running binary path: %w", err)
	}
	trust, err := restoreSecureTrustFromEnvironment()
	if err != nil {
		return err
	}
	commit := strings.TrimSpace(mediaSoakBuildCommit)
	input := dr.SecureRestoreInput{
		EnvironmentID: environmentID, Nonce: nonce, EvaluatedAt: now.UTC(), Sources: sources,
		DownloadedEnvelopePath: os.Getenv("BONFIRE_RESTORE_ENVELOPE_PATH"), Tenants: databaseState.Tenants,
		AuthorityTenantPrefixes: databaseState.AuthorityTenantPrefixes, DatabaseSHA256: databaseState.LogicalSHA256, RunningReleaseCommit: commit, RunningImageDigest: imageDigest,
		RunningBinaryPath: executable, PinnedAuthorityHeadSHA256: authorityHead, AuthorityHead: manifest.AuthorityHead,
	}
	candidate, failures := dr.VerifySecureCandidate(manifest, input, trust)
	if len(failures) != 0 {
		return fmt.Errorf("restore candidate verification: %s", strings.Join(failures, ","))
	}
	if receipt.ManifestSHA256 != manifest.ManifestSHA256 {
		return errors.New("restore receipt does not bind the mounted manifest")
	}
	if err := dr.VerifySecureReceipt(receipt, verifier, environmentID, nonce, candidate, databaseState.LogicalSHA256, imageDigest, authorityHead, now.UTC()); err != nil {
		return fmt.Errorf("restore receipt verification: %w", err)
	}
	if commit == "" || commit == "unqualified" || !strings.EqualFold(commit, receipt.ReleaseCommit) {
		return errors.New("restore receipt release does not match the embedded running release")
	}
	marker := strings.TrimSpace(os.Getenv("BONFIRE_RESTORE_RECEIPT_CONSUMED_PATH"))
	if err := dr.ValidateIndependentPath(marker, roots); err != nil {
		return fmt.Errorf("restore receipt consumption path: %w", err)
	}
	if err := dr.ConsumeSecureReceipt(marker, receipt); err != nil {
		return fmt.Errorf("consume restore receipt: %w", err)
	}
	restoreGate.ready = true
	restoreGate.reason = "signed_receipt_consumed"
	return nil
}

func restoreProfileMarkerPresent() (bool, error) {
	if _, err := os.Lstat(restoreProfileMarkerPath); errors.Is(err, os.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	raw, err := dr.ReadFileNoLinks(restoreProfileMarkerPath, 128)
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(string(raw)) != restoreProfileMarkerValue {
		return false, errors.New("marker content is invalid")
	}
	return true, nil
}

func restoreSecureTrustFromEnvironment() (dr.SecureTrust, error) {
	roles := []dr.EvidenceRole{dr.RoleAuthority, dr.RoleManifest, dr.RoleProvider, dr.RoleCustody, dr.RoleEncryption, dr.RoleRelease}
	verifiers := map[dr.EvidenceRole]dr.PublicVerifier{}
	for _, role := range roles {
		prefix := "BONFIRE_DR_" + strings.ToUpper(string(role))
		verifier, err := dr.ParsePublicVerifier(os.Getenv(prefix+"_PUBLIC_KEY_ID"), role, os.Getenv(prefix+"_PUBLIC_KEY"))
		if err != nil {
			return dr.SecureTrust{}, fmt.Errorf("%s verifier: %w", role, err)
		}
		verifiers[role] = verifier
	}
	return dr.SecureTrust{Authority: verifiers[dr.RoleAuthority], Manifest: verifiers[dr.RoleManifest], Provider: verifiers[dr.RoleProvider], Custody: verifiers[dr.RoleCustody], Encryption: verifiers[dr.RoleEncryption], Release: verifiers[dr.RoleRelease]}, nil
}

func readRestoreGateJSON(path string, output any) error {
	raw, err := dr.ReadFileNoLinks(path, 1<<20)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON content")
	}
	return nil
}

func restoreGateSnapshot() map[string]any {
	return map[string]any{"enabled": restoreGate.enabled, "ready": restoreGate.ready, "reason": restoreGate.reason}
}
