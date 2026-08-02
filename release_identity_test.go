package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func configureQualifiedReleaseIdentity(t *testing.T) releaseIdentitySnapshot {
	t.Helper()
	previous := []string{mediaSoakBuildCommit, mediaSoakBuildTreeDigest, mediaSoakBuildSourceArchiveDigest, mediaSoakBuildInputsDigest, mediaSoakBuildConfigDigest, releaseEmbeddedBuildInputManifestDigest}
	t.Cleanup(func() {
		mediaSoakBuildCommit = previous[0]
		mediaSoakBuildTreeDigest = previous[1]
		mediaSoakBuildSourceArchiveDigest = previous[2]
		mediaSoakBuildInputsDigest = previous[3]
		mediaSoakBuildConfigDigest = previous[4]
		releaseEmbeddedBuildInputManifestDigest = previous[5]
	})

	commit := strings.Repeat("1", 40)
	digests := []string{
		strings.Repeat("2", 64), strings.Repeat("3", 64), strings.Repeat("4", 64),
		strings.Repeat("5", 64), strings.Repeat("6", 64), strings.Repeat("7", 64), strings.Repeat("8", 64),
	}
	binary, err := runningExecutableSHA256()
	if err != nil {
		t.Fatalf("hash running executable: %v", err)
	}
	mediaSoakBuildCommit = commit
	mediaSoakBuildTreeDigest = digests[0]
	mediaSoakBuildSourceArchiveDigest = digests[1]
	mediaSoakBuildInputsDigest = digests[2]
	mediaSoakBuildConfigDigest = digests[3]
	releaseEmbeddedBuildInputManifestDigest = digests[4]

	t.Setenv("BONFIRE_RELEASE_IDENTITY_REQUIRED", "true")
	t.Setenv("BONFIRE_RELEASE_COMMIT", commit)
	t.Setenv("BONFIRE_GIT_TREE_DIGEST", digests[0])
	t.Setenv("BONFIRE_SOURCE_ARCHIVE_SHA256", digests[1])
	t.Setenv("BONFIRE_BUILD_TRANSITIVE_INPUTS_SHA256", digests[2])
	t.Setenv("BONFIRE_BUILD_CONFIG_SHA256", digests[3])
	t.Setenv("BONFIRE_BUILD_INPUT_MANIFEST_SHA256", digests[4])
	t.Setenv("BONFIRE_BUILD_MANIFEST_SHA256", digests[5])
	t.Setenv("BONFIRE_BINARY_SHA256", binary)
	t.Setenv("BONFIRE_IMAGE_DIGEST", digests[6])
	snapshot := releaseIdentitySnapshot{
		ReleaseCommit: commit, GitTreeDigest: digests[0], SourceArchiveSHA256: digests[1],
		TransitiveInputsSHA256: digests[2], BuildConfigSHA256: digests[3], BuildInputManifestSHA256: digests[4],
		ClaimedBuildManifestSHA256: digests[5], BinarySHA256: binary, ClaimedImageDigest: digests[6],
	}
	t.Setenv("BONFIRE_RELEASE_ENVIRONMENT_MARKER", computeReleaseEnvironmentMarker(snapshot))
	return snapshot
}

func TestUnconfiguredDevelopmentIdentityDoesNotClaimAttestation(t *testing.T) {
	for _, name := range []string{
		"BONFIRE_RELEASE_IDENTITY_REQUIRED", "BONFIRE_RELEASE_COMMIT", "BONFIRE_GIT_TREE_DIGEST",
		"BONFIRE_SOURCE_ARCHIVE_SHA256", "BONFIRE_BUILD_TRANSITIVE_INPUTS_SHA256", "BONFIRE_BUILD_CONFIG_SHA256",
		"BONFIRE_BUILD_INPUT_MANIFEST_SHA256", "BONFIRE_BUILD_MANIFEST_SHA256", "BONFIRE_BINARY_SHA256",
		"BONFIRE_IMAGE_DIGEST", "BONFIRE_RELEASE_ENVIRONMENT_MARKER",
	} {
		t.Setenv(name, "")
	}
	got := currentReleaseIdentity()
	if got.Required || got.ProcessQualified || got.Qualified || got.ExternalAttestationRequired || got.Reason != "not_configured" {
		t.Fatalf("development identity=%+v, want unconfigured without attestation claim", got)
	}
}

func TestCurrentReleaseIdentityQualifiesExactReceipt(t *testing.T) {
	want := configureQualifiedReleaseIdentity(t)
	got := currentReleaseIdentity()
	if !got.Required || !got.ProcessQualified || got.Qualified || got.ExternallyAttested || got.Reason != "" {
		t.Fatalf("release identity=%+v, want required process-only qualification", got)
	}
	if got.ReleaseCommit != want.ReleaseCommit || got.BinarySHA256 != want.BinarySHA256 || got.ClaimedImageDigest != want.ClaimedImageDigest {
		t.Fatalf("release identity=%+v, want receipt fields %+v", got, want)
	}
	if err := validateRequiredReleaseIdentity(); err != nil {
		t.Fatalf("qualified startup identity rejected: %v", err)
	}
}

func TestCurrentReleaseIdentityRejectsEmbeddedAndEnvironmentDrift(t *testing.T) {
	configureQualifiedReleaseIdentity(t)
	mediaSoakBuildTreeDigest = strings.Repeat("a", 64)
	if got := currentReleaseIdentity(); got.ProcessQualified || got.Reason != "embedded_build_identity_mismatch" {
		t.Fatalf("embedded drift identity=%+v", got)
	}
	mediaSoakBuildTreeDigest = strings.Repeat("2", 64)
	t.Setenv("BONFIRE_RELEASE_ENVIRONMENT_MARKER", strings.Repeat("b", 64))
	if got := currentReleaseIdentity(); got.ProcessQualified || got.Reason != "environment_marker_mismatch" {
		t.Fatalf("environment drift identity=%+v", got)
	}
	if err := validateRequiredReleaseIdentity(); err == nil || !strings.Contains(err.Error(), "environment_marker_mismatch") {
		t.Fatalf("startup validation error=%v, want marker mismatch", err)
	}
}

func TestHealthHandlerExposesQualifiedReleaseReceipt(t *testing.T) {
	configureQualifiedReleaseIdentity(t)
	recorder := httptest.NewRecorder()
	healthHandler(recorder, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("health status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Release releaseIdentitySnapshot `json:"release"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode health: %v", err)
	}
	if !payload.Release.Required || !payload.Release.ProcessQualified || payload.Release.Qualified ||
		payload.Release.ExternallyAttested || !payload.Release.ExternalAttestationRequired || payload.Release.Schema != releaseIdentitySchema {
		t.Fatalf("health release=%+v, want honest process-only receipt", payload.Release)
	}
}

func TestReleaseEnvironmentMarkerIsFieldSensitive(t *testing.T) {
	base := releaseIdentitySnapshot{
		ReleaseCommit: strings.Repeat("1", 40), GitTreeDigest: strings.Repeat("2", 64),
		SourceArchiveSHA256: strings.Repeat("3", 64), TransitiveInputsSHA256: strings.Repeat("4", 64),
		BuildConfigSHA256: strings.Repeat("5", 64), BuildInputManifestSHA256: strings.Repeat("6", 64),
		ClaimedBuildManifestSHA256: strings.Repeat("7", 64), BinarySHA256: strings.Repeat("8", 64),
		ClaimedImageDigest: strings.Repeat("9", 64),
	}
	first := computeReleaseEnvironmentMarker(base)
	if first != computeReleaseEnvironmentMarker(base) {
		t.Fatal("environment marker is not deterministic")
	}
	base.ClaimedImageDigest = strings.Repeat("a", 64)
	if first == computeReleaseEnvironmentMarker(base) {
		t.Fatal("environment marker did not bind image digest")
	}
}

func TestEnvironmentClaimsCannotForgeFullQualification(t *testing.T) {
	configureQualifiedReleaseIdentity(t)
	t.Setenv("BONFIRE_BUILD_MANIFEST_SHA256", strings.Repeat("a", 64))
	t.Setenv("BONFIRE_IMAGE_DIGEST", strings.Repeat("b", 64))
	snapshot := currentReleaseIdentity()
	snapshot.ClaimedBuildManifestSHA256 = strings.Repeat("a", 64)
	snapshot.ClaimedImageDigest = strings.Repeat("b", 64)
	t.Setenv("BONFIRE_RELEASE_ENVIRONMENT_MARKER", computeReleaseEnvironmentMarker(snapshot))

	got := currentReleaseIdentity()
	if !got.ProcessQualified || got.Qualified || got.ExternallyAttested || !got.ExternalAttestationRequired {
		t.Fatalf("environment-forged claims elevated identity=%+v", got)
	}
	if !releaseIdentityAllowsTraffic(got) {
		t.Fatalf("required process-qualified identity unexpectedly blocked traffic: %+v", got)
	}
}

func TestRequiredBinaryMismatchBlocksStartupAndTraffic(t *testing.T) {
	configureQualifiedReleaseIdentity(t)
	t.Setenv("BONFIRE_BINARY_SHA256", strings.Repeat("c", 64))
	got := currentReleaseIdentity()
	if got.ProcessQualified || got.Reason != "running_binary_mismatch" || releaseIdentityAllowsTraffic(got) {
		t.Fatalf("binary mismatch identity=%+v, want blocked", got)
	}
	if err := validateRequiredReleaseIdentity(); err == nil || !strings.Contains(err.Error(), "running_binary_mismatch") {
		t.Fatalf("startup validation error=%v, want binary mismatch", err)
	}
}

func TestReleaseIdentityDeploymentWiring(t *testing.T) {
	dockerfile, err := os.ReadFile("Dockerfile")
	if err != nil {
		t.Fatalf("read Dockerfile: %v", err)
	}
	compose, err := os.ReadFile("deploy/digitalocean/docker-compose.yml")
	if err != nil {
		t.Fatalf("read compose: %v", err)
	}
	for _, token := range []string{
		"BONFIRE_RELEASE_COMMIT", "BONFIRE_GIT_TREE_DIGEST", "BONFIRE_BUILD_CONFIG_SHA256",
		"BONFIRE_BUILD_TRANSITIVE_INPUTS_SHA256", "BONFIRE_SOURCE_ARCHIVE_SHA256", "BONFIRE_BUILD_INPUT_MANIFEST_SHA256",
		"org.opencontainers.image.revision", "xyz.thebonfire.git-tree-digest",
		"xyz.thebonfire.config-digest", "xyz.thebonfire.transitive-inputs-digest", "xyz.thebonfire.source-archive-digest",
		"xyz.thebonfire.build-input-manifest-digest", "unsigned-external-verification-required",
	} {
		if !strings.Contains(string(dockerfile), token) {
			t.Fatalf("Dockerfile is missing release binding %q", token)
		}
	}
	for _, token := range []string{
		"BONFIRE_RELEASE_IDENTITY_REQUIRED", "BONFIRE_BUILD_INPUT_MANIFEST_SHA256", "BONFIRE_BUILD_MANIFEST_SHA256", "BONFIRE_BINARY_SHA256",
		"BONFIRE_IMAGE_DIGEST", "BONFIRE_RELEASE_ENVIRONMENT_MARKER", "BONFIRE_BUILD_VERSION",
	} {
		if !strings.Contains(string(compose), token) {
			t.Fatalf("Compose is missing release environment %q", token)
		}
	}
}
