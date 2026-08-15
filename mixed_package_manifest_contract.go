package main

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

const (
	mixedPackageIdentitySchemaVersion   = "stride.mixed_package.identity.v1"
	mixedPackageIdentityCompilerVersion = "stride.mixed_package.identity.compiler.v1"
	mixedPackageIdentityMaxPerRole      = 16
	mixedPackageIdentityMaxComponents   = 64
	mixedPackageIdentityMaxRevision     = int64(9_007_199_254_740_991)
)

// MixedPackageComponentRole is a closed package-inventory role. It describes
// what an already-authorized artifact contributes to the package; it does not
// grant authority or select a provider, renderer, destination, or export path.
type MixedPackageComponentRole string

const (
	MixedPackageResearch       MixedPackageComponentRole = "research"
	MixedPackageMemo           MixedPackageComponentRole = "memo"
	MixedPackageDeck           MixedPackageComponentRole = "deck"
	MixedPackageWorkbook       MixedPackageComponentRole = "workbook"
	MixedPackageImagery        MixedPackageComponentRole = "imagery"
	MixedPackageSourceRegister MixedPackageComponentRole = "source_register"
)

var (
	mixedPackageIdentityArtifactIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	mixedPackageIdentityRoleOrder         = []MixedPackageComponentRole{
		MixedPackageResearch,
		MixedPackageMemo,
		MixedPackageDeck,
		MixedPackageWorkbook,
		MixedPackageImagery,
		MixedPackageSourceRegister,
	}
)

// MixedPackageComponentIdentity contains identity only. Callers must resolve
// and authorize the artifact before invoking the compiler. Titles, bodies,
// URLs, ACLs, provider data, preview state, and export state are intentionally
// outside this contract.
type MixedPackageComponentIdentity struct {
	Role          MixedPackageComponentRole `json:"role"`
	ArtifactID    string                    `json:"artifactId"`
	Revision      int64                     `json:"revision"`
	ContentSHA256 string                    `json:"contentSha256"`
}

type MixedPackageIdentityRoleCounts struct {
	Research       int `json:"research"`
	Memo           int `json:"memo"`
	Deck           int `json:"deck"`
	Workbook       int `json:"workbook"`
	Imagery        int `json:"imagery"`
	SourceRegister int `json:"source_register"`
}

// MixedPackageIdentityManifest is body-free. ArtifactSHA256 identifies the
// exact canonical component inventory. ManifestSHA256 identifies the complete
// manifest body, excluding only ManifestSHA256 itself.
type MixedPackageIdentityManifest struct {
	SchemaVersion   string                          `json:"schemaVersion"`
	CompilerVersion string                          `json:"compilerVersion"`
	ComponentCount  int                             `json:"componentCount"`
	RoleCounts      MixedPackageIdentityRoleCounts  `json:"roleCounts"`
	Components      []MixedPackageComponentIdentity `json:"components"`
	ArtifactSHA256  string                          `json:"artifactSha256"`
	ManifestSHA256  string                          `json:"manifestSha256"`
}

type mixedPackageIdentityManifestBody struct {
	SchemaVersion   string                          `json:"schemaVersion"`
	CompilerVersion string                          `json:"compilerVersion"`
	ComponentCount  int                             `json:"componentCount"`
	RoleCounts      MixedPackageIdentityRoleCounts  `json:"roleCounts"`
	Components      []MixedPackageComponentIdentity `json:"components"`
	ArtifactSHA256  string                          `json:"artifactSha256"`
}

// CompileMixedPackageIdentityManifest compiles an already-authorized component
// inventory into one deterministic identity manifest. It performs no reads,
// writes, provider calls, authorization decisions, preview work, or exports.
func CompileMixedPackageIdentityManifest(components []MixedPackageComponentIdentity) (MixedPackageIdentityManifest, error) {
	normalized, counts, err := normalizeMixedPackageComponentIdentities(components)
	if err != nil {
		return MixedPackageIdentityManifest{}, err
	}
	artifactDigest, err := mixedPackageIdentityArtifactSHA256(normalized)
	if err != nil {
		return MixedPackageIdentityManifest{}, err
	}
	manifest := MixedPackageIdentityManifest{
		SchemaVersion:   mixedPackageIdentitySchemaVersion,
		CompilerVersion: mixedPackageIdentityCompilerVersion,
		ComponentCount:  len(normalized),
		RoleCounts:      counts,
		Components:      normalized,
		ArtifactSHA256:  artifactDigest,
	}
	manifest.ManifestSHA256, err = mixedPackageIdentityManifestSHA256(manifest)
	if err != nil {
		return MixedPackageIdentityManifest{}, err
	}
	if err := VerifyMixedPackageIdentityManifest(manifest); err != nil {
		return MixedPackageIdentityManifest{}, err
	}
	return manifest, nil
}

// VerifyMixedPackageIdentityManifest independently derives canonical order,
// all counts, the component-inventory digest, and the manifest self-digest.
// These are unkeyed integrity digests, not signatures: callers must still
// resolve and authorize every artifact/revision/digest and cannot use this
// verifier as proof of authorship, custody, content existence, or role fitness.
func VerifyMixedPackageIdentityManifest(manifest MixedPackageIdentityManifest) error {
	if manifest.SchemaVersion != mixedPackageIdentitySchemaVersion || manifest.CompilerVersion != mixedPackageIdentityCompilerVersion {
		return errors.New("mixed-package identity manifest version mismatch")
	}
	if !mixedPackageIdentityNonZeroSHA256(manifest.ArtifactSHA256) || !mixedPackageIdentityNonZeroSHA256(manifest.ManifestSHA256) {
		return errors.New("mixed-package identity manifest has an invalid digest")
	}
	normalized, counts, err := normalizeMixedPackageComponentIdentities(manifest.Components)
	if err != nil {
		return err
	}
	if manifest.ComponentCount != len(normalized) || manifest.RoleCounts != counts {
		return errors.New("mixed-package identity manifest counts are invalid")
	}
	if !mixedPackageComponentIdentitiesEqual(manifest.Components, normalized) {
		return errors.New("mixed-package identity manifest components are not canonically sorted")
	}
	artifactDigest, err := mixedPackageIdentityArtifactSHA256(normalized)
	if err != nil {
		return err
	}
	if manifest.ArtifactSHA256 != artifactDigest {
		return errors.New("mixed-package identity artifact digest mismatch")
	}
	manifestDigest, err := mixedPackageIdentityManifestSHA256(manifest)
	if err != nil {
		return err
	}
	if manifest.ManifestSHA256 != manifestDigest {
		return errors.New("mixed-package identity manifest digest mismatch")
	}
	return nil
}

func normalizeMixedPackageComponentIdentities(components []MixedPackageComponentIdentity) ([]MixedPackageComponentIdentity, MixedPackageIdentityRoleCounts, error) {
	if len(components) < len(mixedPackageIdentityRoleOrder) || len(components) > mixedPackageIdentityMaxComponents {
		return nil, MixedPackageIdentityRoleCounts{}, fmt.Errorf("mixed-package identity requires %d-%d components", len(mixedPackageIdentityRoleOrder), mixedPackageIdentityMaxComponents)
	}
	normalized := append([]MixedPackageComponentIdentity(nil), components...)
	roleArtifactSeen := make(map[string]struct{}, len(normalized))
	artifactSeen := make(map[string]MixedPackageComponentIdentity, len(normalized))
	counts := MixedPackageIdentityRoleCounts{}
	for index, component := range normalized {
		if mixedPackageIdentityRoleRank(component.Role) < 0 {
			return nil, MixedPackageIdentityRoleCounts{}, fmt.Errorf("mixed-package component %d has an unsupported role", index)
		}
		if !mixedPackageIdentityArtifactIDPattern.MatchString(component.ArtifactID) {
			return nil, MixedPackageIdentityRoleCounts{}, fmt.Errorf("mixed-package component %d has an invalid artifact id", index)
		}
		if component.Revision < 1 || component.Revision > mixedPackageIdentityMaxRevision {
			return nil, MixedPackageIdentityRoleCounts{}, fmt.Errorf("mixed-package component %d has an invalid revision", index)
		}
		if !mixedPackageIdentityNonZeroSHA256(component.ContentSHA256) {
			return nil, MixedPackageIdentityRoleCounts{}, fmt.Errorf("mixed-package component %d has an invalid content digest", index)
		}
		roleArtifactKey := string(component.Role) + "\x00" + component.ArtifactID
		if _, duplicate := roleArtifactSeen[roleArtifactKey]; duplicate {
			return nil, MixedPackageIdentityRoleCounts{}, fmt.Errorf("mixed-package identity repeats role %s and artifact %s", component.Role, component.ArtifactID)
		}
		roleArtifactSeen[roleArtifactKey] = struct{}{}
		if prior, repeated := artifactSeen[component.ArtifactID]; repeated {
			if prior.Revision != component.Revision || prior.ContentSHA256 != component.ContentSHA256 {
				return nil, MixedPackageIdentityRoleCounts{}, fmt.Errorf("mixed-package artifact %s has conflicting repeated identities", component.ArtifactID)
			}
		} else {
			artifactSeen[component.ArtifactID] = component
		}
		mixedPackageIdentityIncrementRoleCount(&counts, component.Role)
		if mixedPackageIdentityRoleCount(counts, component.Role) > mixedPackageIdentityMaxPerRole {
			return nil, MixedPackageIdentityRoleCounts{}, fmt.Errorf("mixed-package role %s exceeds the %d-component limit", component.Role, mixedPackageIdentityMaxPerRole)
		}
	}
	for _, role := range mixedPackageIdentityRoleOrder {
		if mixedPackageIdentityRoleCount(counts, role) == 0 {
			return nil, MixedPackageIdentityRoleCounts{}, fmt.Errorf("mixed-package identity is missing role %s", role)
		}
	}
	sort.Slice(normalized, func(i, j int) bool {
		left, right := normalized[i], normalized[j]
		if left.Role != right.Role {
			return mixedPackageIdentityRoleRank(left.Role) < mixedPackageIdentityRoleRank(right.Role)
		}
		if left.ArtifactID != right.ArtifactID {
			return left.ArtifactID < right.ArtifactID
		}
		if left.Revision != right.Revision {
			return left.Revision < right.Revision
		}
		return left.ContentSHA256 < right.ContentSHA256
	})
	return normalized, counts, nil
}

func mixedPackageIdentityArtifactSHA256(components []MixedPackageComponentIdentity) (string, error) {
	raw, err := canonicalJSON(struct {
		Domain     string                          `json:"domain"`
		Version    string                          `json:"version"`
		Components []MixedPackageComponentIdentity `json:"components"`
	}{
		Domain:     "stride.mixed_package.identity.artifact",
		Version:    mixedPackageIdentitySchemaVersion,
		Components: components,
	})
	if err != nil {
		return "", fmt.Errorf("canonicalize mixed-package artifact identity: %w", err)
	}
	return sha256Hex(raw), nil
}

func mixedPackageIdentityManifestSHA256(manifest MixedPackageIdentityManifest) (string, error) {
	raw, err := canonicalJSON(struct {
		Domain   string                           `json:"domain"`
		Manifest mixedPackageIdentityManifestBody `json:"manifest"`
	}{
		Domain:   "stride.mixed_package.identity.manifest",
		Manifest: mixedPackageIdentityManifestDigestBody(manifest),
	})
	if err != nil {
		return "", fmt.Errorf("canonicalize mixed-package manifest identity: %w", err)
	}
	return sha256Hex(raw), nil
}

func mixedPackageIdentityManifestDigestBody(manifest MixedPackageIdentityManifest) mixedPackageIdentityManifestBody {
	return mixedPackageIdentityManifestBody{
		SchemaVersion:   manifest.SchemaVersion,
		CompilerVersion: manifest.CompilerVersion,
		ComponentCount:  manifest.ComponentCount,
		RoleCounts:      manifest.RoleCounts,
		Components:      manifest.Components,
		ArtifactSHA256:  manifest.ArtifactSHA256,
	}
}

func mixedPackageIdentityRoleRank(role MixedPackageComponentRole) int {
	for index, candidate := range mixedPackageIdentityRoleOrder {
		if role == candidate {
			return index
		}
	}
	return -1
}

func mixedPackageIdentityIncrementRoleCount(counts *MixedPackageIdentityRoleCounts, role MixedPackageComponentRole) {
	switch role {
	case MixedPackageResearch:
		counts.Research++
	case MixedPackageMemo:
		counts.Memo++
	case MixedPackageDeck:
		counts.Deck++
	case MixedPackageWorkbook:
		counts.Workbook++
	case MixedPackageImagery:
		counts.Imagery++
	case MixedPackageSourceRegister:
		counts.SourceRegister++
	}
}

func mixedPackageIdentityRoleCount(counts MixedPackageIdentityRoleCounts, role MixedPackageComponentRole) int {
	switch role {
	case MixedPackageResearch:
		return counts.Research
	case MixedPackageMemo:
		return counts.Memo
	case MixedPackageDeck:
		return counts.Deck
	case MixedPackageWorkbook:
		return counts.Workbook
	case MixedPackageImagery:
		return counts.Imagery
	case MixedPackageSourceRegister:
		return counts.SourceRegister
	default:
		return 0
	}
}

func mixedPackageComponentIdentitiesEqual(left, right []MixedPackageComponentIdentity) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func mixedPackageIdentityNonZeroSHA256(value string) bool {
	return isHexDigest(value) && strings.Trim(value, "0") != ""
}
