package main

import (
	"encoding/json"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func mixedPackageIdentityFixture() []MixedPackageComponentIdentity {
	components := make([]MixedPackageComponentIdentity, 0, len(mixedPackageIdentityRoleOrder))
	for index, role := range mixedPackageIdentityRoleOrder {
		components = append(components, MixedPackageComponentIdentity{
			Role:          role,
			ArtifactID:    "artifact-" + strings.ReplaceAll(string(role), "_", "-"),
			Revision:      int64(index + 1),
			ContentSHA256: sha256Hex([]byte("content:" + string(role))),
		})
	}
	return components
}

func TestCompileMixedPackageIdentityManifestDeterministicAndBodyFree(t *testing.T) {
	components := mixedPackageIdentityFixture()
	first, err := CompileMixedPackageIdentityManifest(components)
	if err != nil {
		t.Fatal(err)
	}
	reversed := append([]MixedPackageComponentIdentity(nil), components...)
	for left, right := 0, len(reversed)-1; left < right; left, right = left+1, right-1 {
		reversed[left], reversed[right] = reversed[right], reversed[left]
	}
	second, err := CompileMixedPackageIdentityManifest(reversed)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("input order changed mixed-package identity:\nfirst=%+v\nsecond=%+v", first, second)
	}
	if err := VerifyMixedPackageIdentityManifest(first); err != nil {
		t.Fatalf("compiled manifest does not verify: %v", err)
	}
	if first.ComponentCount != 6 || first.RoleCounts != (MixedPackageIdentityRoleCounts{Research: 1, Memo: 1, Deck: 1, Workbook: 1, Imagery: 1, SourceRegister: 1}) {
		t.Fatalf("derived counts are wrong: total=%d roles=%+v", first.ComponentCount, first.RoleCounts)
	}
	if first.Components[0].Role != MixedPackageResearch || first.Components[len(first.Components)-1].Role != MixedPackageSourceRegister {
		t.Fatalf("components are not in fixed canonical role order: %+v", first.Components)
	}

	artifactRaw, err := canonicalJSON(struct {
		Domain     string                          `json:"domain"`
		Version    string                          `json:"version"`
		Components []MixedPackageComponentIdentity `json:"components"`
	}{"stride.mixed_package.identity.artifact", mixedPackageIdentitySchemaVersion, first.Components})
	if err != nil || sha256Hex(artifactRaw) != first.ArtifactSHA256 {
		t.Fatalf("independent artifact digest mismatch: digest=%s err=%v", first.ArtifactSHA256, err)
	}
	manifestRaw, err := canonicalJSON(struct {
		Domain   string                           `json:"domain"`
		Manifest mixedPackageIdentityManifestBody `json:"manifest"`
	}{"stride.mixed_package.identity.manifest", mixedPackageIdentityManifestDigestBody(first)})
	if err != nil || sha256Hex(manifestRaw) != first.ManifestSHA256 {
		t.Fatalf("independent manifest digest mismatch: digest=%s err=%v", first.ManifestSHA256, err)
	}

	encoded, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"title", "body", "url", "provider", "persistence", "authority", "preview", "export", "packageId"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("body-free identity manifest contains forbidden field %q: %s", forbidden, encoded)
		}
	}

	concurrentErrors := make(chan string, 32)
	var wait sync.WaitGroup
	for index := 0; index < cap(concurrentErrors); index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			compiled, compileErr := CompileMixedPackageIdentityManifest(reversed)
			if compileErr != nil {
				concurrentErrors <- compileErr.Error()
				return
			}
			if !reflect.DeepEqual(first, compiled) {
				concurrentErrors <- "concurrent compile changed identity"
			}
		}()
	}
	wait.Wait()
	close(concurrentErrors)
	for message := range concurrentErrors {
		t.Error(message)
	}
}

func TestCompileMixedPackageIdentityManifestRejectsMissingFamiliesDuplicatesAndBounds(t *testing.T) {
	valid := mixedPackageIdentityFixture()
	tests := map[string]func([]MixedPackageComponentIdentity) []MixedPackageComponentIdentity{
		"missing family": func(components []MixedPackageComponentIdentity) []MixedPackageComponentIdentity {
			components[len(components)-1] = MixedPackageComponentIdentity{
				Role: MixedPackageResearch, ArtifactID: "artifact-second-research", Revision: 1,
				ContentSHA256: sha256Hex([]byte("content:second-research")),
			}
			return components
		},
		"unknown role": func(components []MixedPackageComponentIdentity) []MixedPackageComponentIdentity {
			components[0].Role = "video"
			return components
		},
		"invalid artifact id": func(components []MixedPackageComponentIdentity) []MixedPackageComponentIdentity {
			components[0].ArtifactID = "https://example.com/body"
			return components
		},
		"zero revision": func(components []MixedPackageComponentIdentity) []MixedPackageComponentIdentity {
			components[0].Revision = 0
			return components
		},
		"unsafe revision": func(components []MixedPackageComponentIdentity) []MixedPackageComponentIdentity {
			components[0].Revision = mixedPackageIdentityMaxRevision + 1
			return components
		},
		"zero digest": func(components []MixedPackageComponentIdentity) []MixedPackageComponentIdentity {
			components[0].ContentSHA256 = strings.Repeat("0", 64)
			return components
		},
		"uppercase digest": func(components []MixedPackageComponentIdentity) []MixedPackageComponentIdentity {
			components[0].ContentSHA256 = strings.ToUpper(components[0].ContentSHA256)
			return components
		},
		"duplicate role artifact": func(components []MixedPackageComponentIdentity) []MixedPackageComponentIdentity {
			return append(components, components[0])
		},
		"conflicting repeated revision": func(components []MixedPackageComponentIdentity) []MixedPackageComponentIdentity {
			components[1].ArtifactID = components[0].ArtifactID
			components[1].Revision = components[0].Revision + 1
			components[1].ContentSHA256 = components[0].ContentSHA256
			return components
		},
		"conflicting repeated digest": func(components []MixedPackageComponentIdentity) []MixedPackageComponentIdentity {
			components[1].ArtifactID = components[0].ArtifactID
			components[1].Revision = components[0].Revision
			return components
		},
		"per role bound": func(components []MixedPackageComponentIdentity) []MixedPackageComponentIdentity {
			for index := 1; index <= mixedPackageIdentityMaxPerRole; index++ {
				components = append(components, MixedPackageComponentIdentity{
					Role: MixedPackageResearch, ArtifactID: "research-extra-" + mixedPackageTestIndex(index), Revision: 1,
					ContentSHA256: sha256Hex([]byte("research-extra:" + mixedPackageTestIndex(index))),
				})
			}
			return components
		},
		"total bound": func(_ []MixedPackageComponentIdentity) []MixedPackageComponentIdentity {
			components := make([]MixedPackageComponentIdentity, 0, 66)
			for _, role := range mixedPackageIdentityRoleOrder {
				for index := 0; index < 11; index++ {
					id := string(role) + "-" + mixedPackageTestIndex(index)
					components = append(components, MixedPackageComponentIdentity{Role: role, ArtifactID: id, Revision: 1, ContentSHA256: sha256Hex([]byte(id))})
				}
			}
			return components
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			components := append([]MixedPackageComponentIdentity(nil), valid...)
			components = mutate(components)
			if manifest, err := CompileMixedPackageIdentityManifest(components); err == nil || manifest.ManifestSHA256 != "" || len(manifest.Components) != 0 {
				t.Fatalf("invalid components produced a manifest=%+v err=%v", manifest, err)
			}
		})
	}

	// One exact artifact revision may intentionally satisfy multiple catalog
	// roles; it is not a conflicting identity and remains role-explicit.
	repeatedExact := append([]MixedPackageComponentIdentity(nil), valid...)
	repeatedExact[1].ArtifactID = repeatedExact[0].ArtifactID
	repeatedExact[1].Revision = repeatedExact[0].Revision
	repeatedExact[1].ContentSHA256 = repeatedExact[0].ContentSHA256
	if _, err := CompileMixedPackageIdentityManifest(repeatedExact); err != nil {
		t.Fatalf("one exact identity in distinct roles was treated as conflicting: %v", err)
	}
}

func TestVerifyMixedPackageIdentityManifestRejectsTamperAndRehashedInconsistency(t *testing.T) {
	manifest, err := CompileMixedPackageIdentityManifest(mixedPackageIdentityFixture())
	if err != nil {
		t.Fatal(err)
	}

	ordinary := manifest
	ordinary.Components = append([]MixedPackageComponentIdentity(nil), manifest.Components...)
	ordinary.Components[0].Revision++
	if err := VerifyMixedPackageIdentityManifest(ordinary); err == nil {
		t.Fatal("ordinary component tampering verified")
	}

	rehashedCount := manifest
	rehashedCount.RoleCounts.Research++
	rehashMixedPackageIdentityManifest(t, &rehashedCount)
	if err := VerifyMixedPackageIdentityManifest(rehashedCount); err == nil {
		t.Fatal("rehashed derived-count inconsistency verified")
	}

	rehashedArtifact := manifest
	rehashedArtifact.ArtifactSHA256 = sha256Hex([]byte("forged artifact identity"))
	rehashMixedPackageIdentityManifest(t, &rehashedArtifact)
	if err := VerifyMixedPackageIdentityManifest(rehashedArtifact); err == nil {
		t.Fatal("rehashed derived-artifact inconsistency verified")
	}

	rehashedComponents := manifest
	rehashedComponents.Components = append([]MixedPackageComponentIdentity(nil), manifest.Components...)
	rehashedComponents.Components[0].ContentSHA256 = sha256Hex([]byte("tampered content"))
	rehashMixedPackageIdentityManifest(t, &rehashedComponents)
	if err := VerifyMixedPackageIdentityManifest(rehashedComponents); err == nil {
		t.Fatal("rehashed component inconsistency without a matching artifact identity verified")
	}

	unsorted := manifest
	unsorted.Components = append([]MixedPackageComponentIdentity(nil), manifest.Components...)
	unsorted.Components[0], unsorted.Components[1] = unsorted.Components[1], unsorted.Components[0]
	unsorted.ArtifactSHA256, err = mixedPackageIdentityArtifactSHA256(unsorted.Components)
	if err != nil {
		t.Fatal(err)
	}
	rehashMixedPackageIdentityManifest(t, &unsorted)
	if err := VerifyMixedPackageIdentityManifest(unsorted); err == nil {
		t.Fatal("rehashed noncanonical component ordering verified")
	}
}

func FuzzCompileMixedPackageIdentityManifest(f *testing.F) {
	validDigest := sha256Hex([]byte("fuzz content"))
	f.Add("artifact-research", int64(1), validDigest)
	f.Add("https://example.com/body", int64(1), validDigest)
	f.Add("artifact-research", int64(0), strings.Repeat("0", 64))
	f.Fuzz(func(t *testing.T, artifactID string, revision int64, digest string) {
		components := mixedPackageIdentityFixture()
		components[0].ArtifactID = artifactID
		components[0].Revision = revision
		components[0].ContentSHA256 = digest
		manifest, err := CompileMixedPackageIdentityManifest(components)
		if err != nil {
			return
		}
		if err := VerifyMixedPackageIdentityManifest(manifest); err != nil {
			t.Fatalf("successful fuzz compile produced an unverifiable manifest: %v", err)
		}
		reversed := append([]MixedPackageComponentIdentity(nil), components...)
		for left, right := 0, len(reversed)-1; left < right; left, right = left+1, right-1 {
			reversed[left], reversed[right] = reversed[right], reversed[left]
		}
		recompiled, err := CompileMixedPackageIdentityManifest(reversed)
		if err != nil || !reflect.DeepEqual(manifest, recompiled) {
			t.Fatalf("successful fuzz compile was not deterministic: err=%v", err)
		}
	})
}

func rehashMixedPackageIdentityManifest(t *testing.T, manifest *MixedPackageIdentityManifest) {
	t.Helper()
	digest, err := mixedPackageIdentityManifestSHA256(*manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifest.ManifestSHA256 = digest
}

func mixedPackageTestIndex(index int) string {
	const digits = "0123456789abcdefghijklmnopqrstuvwxyz"
	if index < len(digits) {
		return string(digits[index])
	}
	return "x" + string(digits[index%len(digits)])
}
