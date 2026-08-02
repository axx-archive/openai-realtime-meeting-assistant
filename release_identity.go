package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"sync"
)

const releaseIdentitySchema = "bonfire.release-identity.v1"

var (
	releaseCommitPattern = regexp.MustCompile(`^[0-9a-f]{40}(?:[0-9a-f]{24})?$`)
	releaseDigestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

	releaseExecutableDigestOnce  sync.Once
	releaseExecutableDigestValue string
	releaseExecutableDigestErr   error
)

// releaseIdentitySnapshot is deliberately self-contained: it is safe to expose
// from health/readiness and contains no filesystem paths, keys, registry
// credentials, or environment values outside the release receipt contract.
type releaseIdentitySnapshot struct {
	Schema                      string `json:"schema"`
	Required                    bool   `json:"required"`
	Qualified                   bool   `json:"qualified"`
	ProcessQualified            bool   `json:"processQualified"`
	ExternallyAttested          bool   `json:"externallyAttested"`
	ExternalAttestationRequired bool   `json:"externalAttestationRequired"`
	AttestationReason           string `json:"attestationReason,omitempty"`
	Reason                      string `json:"reason,omitempty"`
	ReleaseCommit               string `json:"releaseCommit,omitempty"`
	GitTreeDigest               string `json:"gitTreeDigest,omitempty"`
	SourceArchiveSHA256         string `json:"sourceArchiveSha256,omitempty"`
	TransitiveInputsSHA256      string `json:"transitiveInputsSha256,omitempty"`
	BuildConfigSHA256           string `json:"buildConfigSha256,omitempty"`
	BuildInputManifestSHA256    string `json:"buildInputManifestSha256,omitempty"`
	ClaimedBuildManifestSHA256  string `json:"claimedBuildManifestSha256,omitempty"`
	BinarySHA256                string `json:"binarySha256,omitempty"`
	ClaimedImageDigest          string `json:"claimedImageDigest,omitempty"`
	EnvironmentMarker           string `json:"environmentMarker,omitempty"`
}

// releaseEmbeddedBuildInputManifestDigest is populated by -ldflags. Unlike the
// post-build receipt and image ID, this input digest can be truthfully embedded
// before the binary and image exist.
var releaseEmbeddedBuildInputManifestDigest string

func currentReleaseIdentity() releaseIdentitySnapshot {
	snapshot := releaseIdentitySnapshot{
		Schema:                     releaseIdentitySchema,
		Required:                   boolEnv("BONFIRE_RELEASE_IDENTITY_REQUIRED"),
		ReleaseCommit:              strings.TrimSpace(os.Getenv("BONFIRE_RELEASE_COMMIT")),
		GitTreeDigest:              releaseDigestEnv("BONFIRE_GIT_TREE_DIGEST"),
		SourceArchiveSHA256:        releaseDigestEnv("BONFIRE_SOURCE_ARCHIVE_SHA256"),
		TransitiveInputsSHA256:     releaseDigestEnv("BONFIRE_BUILD_TRANSITIVE_INPUTS_SHA256"),
		BuildConfigSHA256:          releaseDigestEnv("BONFIRE_BUILD_CONFIG_SHA256"),
		BuildInputManifestSHA256:   releaseDigestEnv("BONFIRE_BUILD_INPUT_MANIFEST_SHA256"),
		ClaimedBuildManifestSHA256: releaseDigestEnv("BONFIRE_BUILD_MANIFEST_SHA256"),
		BinarySHA256:               releaseDigestEnv("BONFIRE_BINARY_SHA256"),
		ClaimedImageDigest:         releaseDigestEnv("BONFIRE_IMAGE_DIGEST"),
		EnvironmentMarker:          releaseDigestEnv("BONFIRE_RELEASE_ENVIRONMENT_MARKER"),
	}

	if !snapshot.Required && releaseIdentityEnvironmentEmpty(snapshot) {
		snapshot.Reason = "not_configured"
		return snapshot
	}
	snapshot.ExternalAttestationRequired = true
	snapshot.AttestationReason = "unsigned_external_verification_required"
	if !releaseCommitPattern.MatchString(snapshot.ReleaseCommit) {
		snapshot.Reason = "invalid_release_commit"
		return snapshot
	}
	for _, field := range []struct {
		name  string
		value string
	}{
		{"git_tree_digest", snapshot.GitTreeDigest},
		{"source_archive_sha256", snapshot.SourceArchiveSHA256},
		{"transitive_inputs_sha256", snapshot.TransitiveInputsSHA256},
		{"build_config_sha256", snapshot.BuildConfigSHA256},
		{"build_input_manifest_sha256", snapshot.BuildInputManifestSHA256},
		{"claimed_build_manifest_sha256", snapshot.ClaimedBuildManifestSHA256},
		{"binary_sha256", snapshot.BinarySHA256},
		{"claimed_image_digest", snapshot.ClaimedImageDigest},
		{"environment_marker", snapshot.EnvironmentMarker},
	} {
		if !releaseDigestPattern.MatchString(field.value) {
			snapshot.Reason = "invalid_" + field.name
			return snapshot
		}
	}

	if strings.TrimSpace(mediaSoakBuildCommit) != snapshot.ReleaseCommit ||
		normalizeReleaseDigest(mediaSoakBuildTreeDigest) != snapshot.GitTreeDigest ||
		normalizeReleaseDigest(mediaSoakBuildSourceArchiveDigest) != snapshot.SourceArchiveSHA256 ||
		normalizeReleaseDigest(mediaSoakBuildInputsDigest) != snapshot.TransitiveInputsSHA256 ||
		normalizeReleaseDigest(mediaSoakBuildConfigDigest) != snapshot.BuildConfigSHA256 ||
		normalizeReleaseDigest(releaseEmbeddedBuildInputManifestDigest) != snapshot.BuildInputManifestSHA256 {
		snapshot.Reason = "embedded_build_identity_mismatch"
		return snapshot
	}

	executableDigest, err := runningExecutableSHA256()
	if err != nil || executableDigest != snapshot.BinarySHA256 {
		snapshot.Reason = "running_binary_mismatch"
		return snapshot
	}

	expectedMarker := computeReleaseEnvironmentMarker(snapshot)
	if snapshot.EnvironmentMarker != expectedMarker {
		snapshot.Reason = "environment_marker_mismatch"
		return snapshot
	}

	// The process can prove its executable bytes and the source/build-input
	// identity embedded in those bytes. It cannot inspect the OCI image ID that
	// launched it, authenticate an unsigned release receipt, or confer off-host
	// custody on itself. Only the external verifier may make that separate claim.
	snapshot.ProcessQualified = true
	return snapshot
}

func releaseIdentityEnvironmentEmpty(snapshot releaseIdentitySnapshot) bool {
	return snapshot.ReleaseCommit == "" && snapshot.GitTreeDigest == "" && snapshot.SourceArchiveSHA256 == "" &&
		snapshot.TransitiveInputsSHA256 == "" && snapshot.BuildConfigSHA256 == "" && snapshot.BuildInputManifestSHA256 == "" &&
		snapshot.ClaimedBuildManifestSHA256 == "" && snapshot.BinarySHA256 == "" && snapshot.ClaimedImageDigest == "" &&
		snapshot.EnvironmentMarker == ""
}

func normalizeReleaseDigest(value string) string {
	return strings.ToLower(strings.TrimPrefix(strings.TrimSpace(value), "sha256:"))
}

func releaseDigestEnv(name string) string {
	return normalizeReleaseDigest(os.Getenv(name))
}

// computeReleaseEnvironmentMarker is the canonical environment receipt hash.
// Keep the field order stable and mirrored by scripts/bonfire-release.mjs.
func computeReleaseEnvironmentMarker(snapshot releaseIdentitySnapshot) string {
	values := []string{
		releaseIdentitySchema,
		snapshot.ReleaseCommit,
		snapshot.GitTreeDigest,
		snapshot.SourceArchiveSHA256,
		snapshot.TransitiveInputsSHA256,
		snapshot.BuildConfigSHA256,
		snapshot.BuildInputManifestSHA256,
		snapshot.ClaimedBuildManifestSHA256,
		snapshot.BinarySHA256,
		snapshot.ClaimedImageDigest,
	}
	digest := sha256.Sum256([]byte(strings.Join(values, "\n") + "\n"))
	return hex.EncodeToString(digest[:])
}

func runningExecutableSHA256() (string, error) {
	releaseExecutableDigestOnce.Do(func() {
		path := "/proc/self/exe"
		file, err := os.Open(path)
		if err != nil {
			path, err = os.Executable()
			if err != nil {
				releaseExecutableDigestErr = err
				return
			}
			file, err = os.Open(path)
		}
		if err != nil {
			releaseExecutableDigestErr = err
			return
		}
		defer file.Close()
		hash := sha256.New()
		if _, err = io.Copy(hash, file); err != nil {
			releaseExecutableDigestErr = err
			return
		}
		releaseExecutableDigestValue = hex.EncodeToString(hash.Sum(nil))
	})
	return releaseExecutableDigestValue, releaseExecutableDigestErr
}

func validateRequiredReleaseIdentity() error {
	snapshot := currentReleaseIdentity()
	if !releaseIdentityAllowsTraffic(snapshot) {
		return fmt.Errorf("release process identity is required but unqualified: %s", snapshot.Reason)
	}
	return nil
}

func releaseIdentityAllowsTraffic(snapshot releaseIdentitySnapshot) bool {
	return !snapshot.Required || snapshot.ProcessQualified
}
