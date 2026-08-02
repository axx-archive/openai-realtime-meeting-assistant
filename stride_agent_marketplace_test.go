package main

import (
	"errors"
	"testing"
	"time"
)

func strideMarketplaceManifestForTest() AgentPackageManifest {
	return AgentPackageManifest{
		Header: strideTestHeader(STRIDEContractAgentPackageManifest, "package_marketing_v1"), PackageID: "package_marketing_v1", PublisherID: "stride", PublisherAttestationDigest: strideTestDigest("a"), Version: "v1", Provenance: "stride_authored", PersonaSeedDigest: strideTestDigest("b"),
		AssetRefs: []STRIDEReference{strideTestRef(STRIDEContractRichMessagePart, "asset_marketing")}, RequestedCapabilities: []string{"research"}, RuntimeClasses: []string{"server"}, ModelClasses: []string{"text"}, VoiceClasses: []string{"none"}, DataClassifications: []string{"internal"}, EvalBundleRefs: []STRIDEReference{strideTestRef(STRIDEContractOutcome, "eval_marketing")}, DependencySBOMRefs: []STRIDEReference{strideTestRef(STRIDEContractOutcome, "sbom_marketing")}, LicenseID: "internal", UpdatePolicy: "manual", MigrationCompatibility: "v1", Status: "verified",
	}
}

func strideMarketplaceManifestReference(manifest AgentPackageManifest) STRIDEReference {
	return STRIDEReference{ContractType: STRIDEContractAgentPackageManifest, ID: manifest.Header.ID, Revision: manifest.Header.Revision, Digest: manifest.Header.ContentDigest}
}

func strideMarketplaceListingForTest(manifest AgentPackageManifest, receipts bool) MarketplaceListing {
	listing := MarketplaceListing{Header: strideTestHeader(STRIDEContractMarketplaceListing, "listing_marketing_v1"), Package: strideMarketplaceManifestReference(manifest), Category: "marketing", OutcomeDigest: strideTestDigest("c"), Evidence: []STRIDEReference{strideTestRef(STRIDEContractOutcome, "evidence_marketing")}, PermissionSummaryDigest: strideTestDigest("d"), Surfaces: []string{"chat"}, CostBand: "low", Audience: strideTestAudience(), PublisherStatus: "active", UpdateChannel: "stable", Reviewer: "member_aj", Status: "available"}
	if receipts {
		quality, safety, voice := strideTestRef(STRIDEContractOutcome, "quality_marketing"), strideTestRef(STRIDEContractOutcome, "safety_marketing"), strideTestRef(STRIDEContractOutcome, "voice_marketing")
		listing.QualityReceipt, listing.SafetyReceipt, listing.VoiceReceipt = &quality, &safety, &voice
	}
	return listing
}

func TestSTRIDEMarketplaceOnlyIngestsClosedVerifiedPackages(t *testing.T) {
	marketplace := NewSTRIDEMarketplace()
	manifest := strideMarketplaceManifestForTest()
	actor := STRIDEWorkforceActor{ID: "member_aj", IsAdmin: true}
	if _, err := marketplace.IngestPackage(actor, manifest, strideMarketplaceManifestReference(manifest), time.Now().UTC()); err != nil {
		t.Fatalf("ingest verified package: %v", err)
	}
	manifest.AssetRefs[0].ID = "mutated_client_value"
	stored, err := marketplace.Package("package_marketing_v1")
	if err != nil || stored.Manifest.AssetRefs[0].ID != "asset_marketing" {
		t.Fatalf("immutable package copy: %#v %v", stored, err)
	}
	if err := marketplace.SetPackageState(actor, "package_marketing_v1", "quarantined", nil, "eval_failure"); err != nil {
		t.Fatalf("quarantine: %v", err)
	}
	if _, err := marketplace.IngestPackage(STRIDEWorkforceActor{ID: "member_aj"}, manifest, strideMarketplaceManifestReference(manifest), time.Now().UTC()); !errors.Is(err, ErrSTRIDEAdminRequired) {
		t.Fatalf("non-admin ingest error=%v", err)
	}
}

func TestSTRIDEMarketplaceListingStaysUnavailableWithoutFullReceipts(t *testing.T) {
	marketplace := NewSTRIDEMarketplace()
	manifest := strideMarketplaceManifestForTest()
	actor := STRIDEWorkforceActor{ID: "member_aj", IsAdmin: true}
	if _, err := marketplace.IngestPackage(actor, manifest, strideMarketplaceManifestReference(manifest), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	record, err := marketplace.ReviewListing(actor, strideMarketplaceListingForTest(manifest, false), strideTestDigest("e"), time.Now().UTC())
	if err != nil || record.Available || record.State != STRIDEListingUnavailable {
		t.Fatalf("incomplete review: %#v %v", record, err)
	}
	view, err := marketplace.ListingView("listing_marketing_v1")
	if err != nil || view.Available || view.PersonalityDigest != strideTestDigest("e") || view.Provenance != "stride_authored" {
		t.Fatalf("read model: %#v %v", view, err)
	}
	manifest2 := strideMarketplaceManifestForTest()
	manifest2.Header.ID, manifest2.PackageID = "package_research_v1", "package_research_v1"
	manifest2.Header.ContentDigest = strideTestDigest("f")
	if _, err := marketplace.IngestPackage(actor, manifest2, strideMarketplaceManifestReference(manifest2), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	listing := strideMarketplaceListingForTest(manifest2, true)
	listing.Header.ID = "listing_research_v1"
	if record, err := marketplace.ReviewListing(actor, listing, strideTestDigest("a"), time.Now().UTC()); err != nil || record.Available || record.State != STRIDEListingUnavailable || !record.ReceiptSetComplete {
		t.Fatalf("complete shaped receipts must remain fenced until E10: %#v %v", record, err)
	}
}

func TestSTRIDEMarketplaceTemplateRejectsExecutableOrSecretInputs(t *testing.T) {
	template := STRIDEAgentTemplate{TemplateID: "template_marketing", Package: strideTestRef(STRIDEContractAgentPackageManifest, "package_marketing_v1"), Category: "marketing", OutcomeDigest: strideTestDigest("a"), PersonalityDigest: strideTestDigest("b"), Evidence: []STRIDEReference{strideTestRef(STRIDEContractOutcome, "evidence_1")}, AccessSummaryDigest: strideTestDigest("c"), CostBand: "low", Memberships: []string{"team"}, Concurrency: 1, Proactivity: "disabled"}
	if err := template.Validate(); err != nil {
		t.Fatalf("safe template: %v", err)
	}
	template.Commands = []string{"curl"}
	if err := template.Validate(); !errors.Is(err, ErrSTRIDETemplateUnsafe) {
		t.Fatalf("command template error=%v", err)
	}
	template.Commands = nil
	template.RawMCP = "server config"
	if err := template.Validate(); !errors.Is(err, ErrSTRIDETemplateUnsafe) {
		t.Fatalf("raw MCP template error=%v", err)
	}
}

func TestSTRIDEMarketplaceSnapshotRestoresDeterministically(t *testing.T) {
	marketplace := NewSTRIDEMarketplace()
	manifest := strideMarketplaceManifestForTest()
	actor := STRIDEWorkforceActor{ID: "member_aj", IsAdmin: true}
	if _, err := marketplace.IngestPackage(actor, manifest, strideMarketplaceManifestReference(manifest), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := marketplace.ReviewListing(actor, strideMarketplaceListingForTest(manifest, true), strideTestDigest("e"), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	snapshot, err := marketplace.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	restored, err := RestoreSTRIDEMarketplace(snapshot)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	second, err := restored.Snapshot()
	if err != nil || second.Digest != snapshot.Digest {
		t.Fatalf("snapshot drift %q %q %v", snapshot.Digest, second.Digest, err)
	}
}

func TestSTRIDEMarketplaceRestoreCannotActivateSerializedListing(t *testing.T) {
	marketplace := NewSTRIDEMarketplace()
	manifest := strideMarketplaceManifestForTest()
	actor := STRIDEWorkforceActor{ID: "member_aj", IsAdmin: true}
	if _, err := marketplace.IngestPackage(actor, manifest, strideMarketplaceManifestReference(manifest), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := marketplace.ReviewListing(actor, strideMarketplaceListingForTest(manifest, true), strideTestDigest("e"), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	snapshot, err := marketplace.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Listings[0].State = STRIDEListingAvailable
	snapshot.Listings[0].Available = true
	snapshot.Digest, err = strideMarketplaceSnapshotDigest(snapshot.Packages, snapshot.Listings)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RestoreSTRIDEMarketplace(snapshot); !errors.Is(err, ErrSTRIDEListingInvalid) {
		t.Fatalf("serialized availability error=%v", err)
	}
}
