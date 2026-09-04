package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type organizationExecutionLegacyBinding struct {
	OrganizationID    string
	CanonicalTenantID string
	ArtifactTenantID  string
	MigrationDigest   string
}

// W4 gave Bonfire an opaque organization ID while its existing stores retain
// the bonfire tenant. Those identifiers belong to different namespaces. Only
// the authenticated migration crosswalk can connect them; a matching display
// name, arbitrary membership, or client-selected ID cannot.
func bindOrganizationExecutionMigration(ctx context.Context, runtime *StrideE10ProductLiveRuntime, keys *strideE10W4Keyring) error {
	path := strings.TrimSpace(os.Getenv(strideE10W4StatePathEnv))
	if path == "" {
		return nil
	}
	if runtime == nil || runtime.organization == nil || keys == nil || !filepath.IsAbs(path) {
		return errors.New("legacy execution migration binding unavailable")
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 2<<20 {
		return errors.New("legacy execution migration binding unavailable")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return errors.New("legacy execution migration binding unavailable")
	}
	var state strideE10MigrationState
	if strideE10UnmarshalSigned(ctx, keys, raw, &state) != nil || validateStrideE10PersistedState(state) != nil {
		return errors.New("legacy execution migration binding invalid")
	}
	// The W4 migration command is explicitly the original Bonfire migration.
	// Retargeting either data store is a separate migration, never an alias here.
	if canonicalTenantID() != "bonfire" || canonicalArtifactTenantID() != "bonfire" {
		return errors.New("legacy execution data tenant differs from migrated source")
	}
	org, ok := runtime.organization.organizations[state.Manifest.OrganizationID]
	if !ok || org.Validate() != nil || org.Status != "active" || org.Header.ID != state.Receipt.OrganizationID {
		return errors.New("legacy execution migrated organization unavailable")
	}
	runtime.legacyExecutionBinding = &organizationExecutionLegacyBinding{OrganizationID: org.Header.ID, CanonicalTenantID: canonicalTenantID(), ArtifactTenantID: canonicalArtifactTenantID(), MigrationDigest: state.Manifest.MigrationDigest}
	return nil
}

func organizationExecutionTenantMatches(runtime *StrideE10ProductLiveRuntime, organizationID string) bool {
	if organizationID == "" {
		return false
	}
	if runtime != nil && runtime.legacyExecutionBinding != nil {
		binding := runtime.legacyExecutionBinding
		return organizationID == binding.OrganizationID && binding.CanonicalTenantID == canonicalTenantID() && binding.ArtifactTenantID == canonicalArtifactTenantID()
	}
	return organizationID == canonicalTenantID() && organizationID == canonicalArtifactTenantID()
}

// Runs before server startup. Intended for the exact candidate image with no
// network and read-only production data, proving the real migration topology.
// Emits counts only, never session keys, account identities or private payloads.
func verifyOrganizationExecutionBindingCLI(stdout, stderr io.Writer) int {
	if err := installStrideE10W4ProductionRuntimeFromEnvironment(); err != nil {
		fmt.Fprintln(stderr, "legacy execution binding: signed runtime unavailable")
		return 1
	}
	runtime := strideE10LiveProductRuntime
	if runtime == nil || runtime.legacyExecutionBinding == nil {
		fmt.Fprintln(stderr, "legacy execution binding: signed migration mapping unavailable")
		return 1
	}
	sessions := userSessionStore()
	sessions.mu.Lock()
	var hashes []string
	for hash, record := range sessions.sessions {
		if record.Kind == "" && time.Now().Before(record.Expires) && record.ActiveOrganizationID == runtime.legacyExecutionBinding.OrganizationID {
			hashes = append(hashes, hash)
		}
	}
	sessions.mu.Unlock()
	allowed, denied := 0, 0
	for _, hash := range hashes {
		if _, err := organizationExecutionScopeForSession(context.Background(), hash); err != nil {
			denied++
		} else {
			allowed++
		}
	}
	verified := allowed > 0 && denied == 0
	_ = json.NewEncoder(stdout).Encode(map[string]any{"verified": verified, "mode": "read_only", "mappedSessions": len(hashes), "allowedSessions": allowed, "deniedSessions": denied, "legacyDataRetargeted": false})
	if !verified {
		return 1
	}
	return 0
}
