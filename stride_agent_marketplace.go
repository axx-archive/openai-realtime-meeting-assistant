package main

// E8 marketplace is an offline control plane. It stores only signed contract
// references and digests; it neither loads packages nor starts an agent.

import (
	"errors"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	ErrSTRIDEPackageInvalid     = errors.New("invalid STRIDE agent package")
	ErrSTRIDEPackageExists      = errors.New("STRIDE agent package already exists")
	ErrSTRIDEPackageUnknown     = errors.New("unknown STRIDE agent package")
	ErrSTRIDEListingInvalid     = errors.New("invalid STRIDE marketplace listing")
	ErrSTRIDEListingUnavailable = errors.New("STRIDE marketplace listing is unavailable")
	ErrSTRIDEAdminRequired      = errors.New("STRIDE workforce admin required")
	ErrSTRIDETemplateUnsafe     = errors.New("unsafe STRIDE agent template field")
	ErrSTRIDEImmutable          = errors.New("STRIDE record is immutable")
	ErrSTRIDEReceiptRequired    = errors.New("STRIDE verification receipt required")
)

type STRIDEWorkforceActor struct {
	ID      string
	IsAdmin bool
}

func (actor STRIDEWorkforceActor) Validate() error {
	if !strideIdentifier(actor.ID) {
		return ErrSTRIDEAdminRequired
	}
	return nil
}

// STRIDEAgentTemplate is deliberately declarative. The disallowed fields are
// retained solely so deserializers can reject them, rather than silently omit
// code, shell hooks, environment material, credentials, or raw MCP details.
type STRIDEAgentTemplate struct {
	TemplateID          string
	Package             STRIDEReference
	Category            string
	OutcomeDigest       string
	PersonalityDigest   string
	Evidence            []STRIDEReference
	AccessSummaryDigest string
	CostBand            string
	Memberships         []string
	PerRunBudgetCents   int64
	DailyBudgetCents    int64
	MonthlyBudgetCents  int64
	Concurrency         int
	Proactivity         string
	Code                string
	Commands            []string
	Hooks               []string
	Environment         map[string]string
	Credentials         string
	RawMCP              string
}

func (template STRIDEAgentTemplate) Validate() error {
	if !strideIdentifier(template.TemplateID) || template.Package.Validate() != nil || template.Package.ContractType != STRIDEContractAgentPackageManifest || !strideIdentifier(template.Category) ||
		!isHexDigest(template.OutcomeDigest) || !isHexDigest(template.PersonalityDigest) || !validateSTRIDERefs(template.Evidence) || !isHexDigest(template.AccessSummaryDigest) || !strideIdentifier(template.CostBand) ||
		!uniqueSTRIDEIDs(template.Memberships) || template.PerRunBudgetCents < 0 || template.DailyBudgetCents < 0 || template.MonthlyBudgetCents < 0 || template.Concurrency < 1 || !oneOf(template.Proactivity, "disabled", "quiet") {
		return ErrSTRIDEPackageInvalid
	}
	if strings.TrimSpace(template.Code) != "" || len(template.Commands) != 0 || len(template.Hooks) != 0 || len(template.Environment) != 0 || strings.TrimSpace(template.Credentials) != "" || strings.TrimSpace(template.RawMCP) != "" {
		return ErrSTRIDETemplateUnsafe
	}
	return nil
}

type STRIDEMarketplaceListingState string

const (
	STRIDEListingUnderReview STRIDEMarketplaceListingState = "under_review"
	STRIDEListingUnavailable STRIDEMarketplaceListingState = "unavailable"
	STRIDEListingAvailable   STRIDEMarketplaceListingState = "available"
	STRIDEListingSuspended   STRIDEMarketplaceListingState = "suspended"
	STRIDEListingRevoked     STRIDEMarketplaceListingState = "revoked"
)

type STRIDEMarketplaceRecord struct {
	Manifest         AgentPackageManifest
	Reference        STRIDEReference
	State            string
	RollbackTo       *STRIDEReference
	QuarantineReason string
	CreatedAt        time.Time
}

type STRIDEMarketplaceListingRecord struct {
	Listing            MarketplaceListing
	State              STRIDEMarketplaceListingState
	PersonalityDigest  string
	Provenance         string
	ReceiptSetComplete bool
	Available          bool
	CreatedAt          time.Time
}

// STRIDEMarketplaceView is the intentionally read-only catalog projection used
// by a future UI. It has no asset URLs, provider routes, or executable data.
type STRIDEMarketplaceView struct {
	ListingID           string
	Package             STRIDEReference
	Category            string
	OutcomeDigest       string
	PersonalityDigest   string
	Evidence            []STRIDEReference
	AccessSummaryDigest string
	CostBand            string
	Provenance          string
	State               STRIDEMarketplaceListingState
	ReceiptSetComplete  bool
	Available           bool
}

type STRIDEMarketplaceSnapshot struct {
	Packages []STRIDEMarketplaceRecord
	Listings []STRIDEMarketplaceListingRecord
	Digest   string
}

type STRIDEMarketplace struct {
	mu       sync.RWMutex
	packages map[string]STRIDEMarketplaceRecord
	listings map[string]STRIDEMarketplaceListingRecord
}

func NewSTRIDEMarketplace() *STRIDEMarketplace {
	return &STRIDEMarketplace{packages: map[string]STRIDEMarketplaceRecord{}, listings: map[string]STRIDEMarketplaceListingRecord{}}
}

func (marketplace *STRIDEMarketplace) IngestPackage(actor STRIDEWorkforceActor, manifest AgentPackageManifest, reference STRIDEReference, now time.Time) (STRIDEMarketplaceRecord, error) {
	if marketplace == nil || actor.Validate() != nil || !actor.IsAdmin {
		return STRIDEMarketplaceRecord{}, ErrSTRIDEAdminRequired
	}
	if manifest.Validate() != nil || manifest.Status != "verified" || reference.Validate() != nil || reference.ContractType != STRIDEContractAgentPackageManifest || reference.ID != manifest.Header.ID || reference.Revision != manifest.Header.Revision || reference.Digest != manifest.Header.ContentDigest || now.IsZero() {
		return STRIDEMarketplaceRecord{}, ErrSTRIDEPackageInvalid
	}
	marketplace.mu.Lock()
	defer marketplace.mu.Unlock()
	if _, exists := marketplace.packages[manifest.PackageID]; exists {
		return STRIDEMarketplaceRecord{}, ErrSTRIDEPackageExists
	}
	record := STRIDEMarketplaceRecord{Manifest: manifest, Reference: reference, State: "verified", CreatedAt: now.UTC()}
	marketplace.packages[manifest.PackageID] = cloneSTRIDEMarketplaceRecord(record)
	return record, nil
}

func (marketplace *STRIDEMarketplace) Package(packageID string) (STRIDEMarketplaceRecord, error) {
	if marketplace == nil || !strideIdentifier(packageID) {
		return STRIDEMarketplaceRecord{}, ErrSTRIDEPackageUnknown
	}
	marketplace.mu.RLock()
	defer marketplace.mu.RUnlock()
	record, found := marketplace.packages[packageID]
	if !found {
		return STRIDEMarketplaceRecord{}, ErrSTRIDEPackageUnknown
	}
	return cloneSTRIDEMarketplaceRecord(record), nil
}

func (marketplace *STRIDEMarketplace) SetPackageState(actor STRIDEWorkforceActor, packageID, state string, rollbackTo *STRIDEReference, reason string) error {
	if marketplace == nil || actor.Validate() != nil || !actor.IsAdmin {
		return ErrSTRIDEAdminRequired
	}
	if !oneOf(state, "quarantined", "revoked", "suspended") || (state == "quarantined" && !strideIdentifier(reason)) || (state != "quarantined" && reason != "") || (rollbackTo != nil && rollbackTo.Validate() != nil) {
		return ErrSTRIDEPackageInvalid
	}
	marketplace.mu.Lock()
	defer marketplace.mu.Unlock()
	record, found := marketplace.packages[packageID]
	if !found {
		return ErrSTRIDEPackageUnknown
	}
	if rollbackTo != nil && rollbackTo.ContractType != STRIDEContractAgentPackageManifest {
		return ErrSTRIDEPackageInvalid
	}
	if rollbackTo != nil {
		rollback, found := marketplace.packageByReferenceLocked(*rollbackTo)
		if !found || rollback.State != "verified" || rollback.Reference == record.Reference {
			return ErrSTRIDEPackageInvalid
		}
	}
	record.State, record.RollbackTo, record.QuarantineReason = state, cloneSTRIDEReference(rollbackTo), reason
	marketplace.packages[packageID] = cloneSTRIDEMarketplaceRecord(record)
	return nil
}

func (marketplace *STRIDEMarketplace) ReviewListing(actor STRIDEWorkforceActor, listing MarketplaceListing, personalityDigest string, now time.Time) (STRIDEMarketplaceListingRecord, error) {
	if marketplace == nil || actor.Validate() != nil || !actor.IsAdmin {
		return STRIDEMarketplaceListingRecord{}, ErrSTRIDEAdminRequired
	}
	if listing.Validate() != nil || !isHexDigest(personalityDigest) || now.IsZero() {
		return STRIDEMarketplaceListingRecord{}, ErrSTRIDEListingInvalid
	}
	marketplace.mu.Lock()
	defer marketplace.mu.Unlock()
	packageRecord, found := marketplace.packageByReferenceLocked(listing.Package)
	if !found || packageRecord.State != "verified" {
		return STRIDEMarketplaceListingRecord{}, ErrSTRIDEListingUnavailable
	}
	if _, exists := marketplace.listings[listing.Header.ID]; exists {
		return STRIDEMarketplaceListingRecord{}, ErrSTRIDEImmutable
	}
	// E8 may prove that a candidate has a complete shaped receipt set, but only
	// E10 can verify that those refs bind current provider, human-review, cost,
	// and rollback evidence. A merely well-formed reference must never unlock a
	// visible listing or a hire path.
	receiptsComplete := strideMarketplaceReceiptShapeComplete(listing)
	record := STRIDEMarketplaceListingRecord{Listing: listing, State: STRIDEListingUnavailable, PersonalityDigest: personalityDigest, Provenance: packageRecord.Manifest.Provenance, ReceiptSetComplete: receiptsComplete, Available: false, CreatedAt: now.UTC()}
	marketplace.listings[listing.Header.ID] = cloneSTRIDEMarketplaceListingRecord(record)
	return record, nil
}

func (marketplace *STRIDEMarketplace) ListingView(listingID string) (STRIDEMarketplaceView, error) {
	if marketplace == nil || !strideIdentifier(listingID) {
		return STRIDEMarketplaceView{}, ErrSTRIDEListingUnavailable
	}
	marketplace.mu.RLock()
	defer marketplace.mu.RUnlock()
	record, found := marketplace.listings[listingID]
	if !found {
		return STRIDEMarketplaceView{}, ErrSTRIDEListingUnavailable
	}
	return marketplaceListingView(record), nil
}

func (marketplace *STRIDEMarketplace) Snapshot() (STRIDEMarketplaceSnapshot, error) {
	if marketplace == nil {
		return STRIDEMarketplaceSnapshot{}, ErrSTRIDEPackageInvalid
	}
	marketplace.mu.RLock()
	defer marketplace.mu.RUnlock()
	snapshot := STRIDEMarketplaceSnapshot{}
	for _, record := range marketplace.packages {
		snapshot.Packages = append(snapshot.Packages, cloneSTRIDEMarketplaceRecord(record))
	}
	for _, record := range marketplace.listings {
		snapshot.Listings = append(snapshot.Listings, cloneSTRIDEMarketplaceListingRecord(record))
	}
	sort.Slice(snapshot.Packages, func(i, j int) bool {
		return snapshot.Packages[i].Manifest.PackageID < snapshot.Packages[j].Manifest.PackageID
	})
	sort.Slice(snapshot.Listings, func(i, j int) bool {
		return snapshot.Listings[i].Listing.Header.ID < snapshot.Listings[j].Listing.Header.ID
	})
	digest, err := strideMarketplaceSnapshotDigest(snapshot.Packages, snapshot.Listings)
	if err != nil {
		return STRIDEMarketplaceSnapshot{}, err
	}
	snapshot.Digest = digest
	return snapshot, nil
}

func RestoreSTRIDEMarketplace(snapshot STRIDEMarketplaceSnapshot) (*STRIDEMarketplace, error) {
	if !isHexDigest(snapshot.Digest) {
		return nil, ErrSTRIDEPackageInvalid
	}
	copySnapshot := snapshot
	copySnapshot.Digest = ""
	calculated, err := strideMarketplaceSnapshotDigest(copySnapshot.Packages, copySnapshot.Listings)
	if err != nil || calculated != snapshot.Digest {
		return nil, ErrSTRIDEPackageInvalid
	}
	marketplace := NewSTRIDEMarketplace()
	for _, record := range snapshot.Packages {
		if record.Manifest.Validate() != nil || record.Reference.Validate() != nil || record.CreatedAt.IsZero() ||
			record.Reference.ContractType != STRIDEContractAgentPackageManifest || record.Reference.ID != record.Manifest.Header.ID ||
			record.Reference.Revision != record.Manifest.Header.Revision || record.Reference.Digest != record.Manifest.Header.ContentDigest ||
			!oneOf(record.State, "verified", "quarantined", "revoked", "suspended") ||
			(record.State == "quarantined") != (record.QuarantineReason != "") || (record.QuarantineReason != "" && !strideIdentifier(record.QuarantineReason)) ||
			(record.RollbackTo != nil && (record.RollbackTo.Validate() != nil || record.RollbackTo.ContractType != STRIDEContractAgentPackageManifest)) {
			return nil, ErrSTRIDEPackageInvalid
		}
		if _, exists := marketplace.packages[record.Manifest.PackageID]; exists {
			return nil, ErrSTRIDEPackageInvalid
		}
		marketplace.packages[record.Manifest.PackageID] = cloneSTRIDEMarketplaceRecord(record)
	}
	for _, record := range snapshot.Listings {
		packageRecord, packageFound := marketplace.packageByReferenceLocked(record.Listing.Package)
		// E8 restore is not an activation path. Snapshot digests detect drift;
		// they are not provider/human qualification signatures. Serialized
		// availability must therefore fail closed until E10 introduces a
		// separately verified admission receipt.
		if record.Listing.Validate() != nil || !isHexDigest(record.PersonalityDigest) || record.CreatedAt.IsZero() ||
			!oneOf(string(record.State), string(STRIDEListingUnavailable), string(STRIDEListingSuspended), string(STRIDEListingRevoked)) ||
			record.Available || record.ReceiptSetComplete != strideMarketplaceReceiptShapeComplete(record.Listing) ||
			!packageFound || record.Provenance != packageRecord.Manifest.Provenance {
			return nil, ErrSTRIDEListingInvalid
		}
		if _, exists := marketplace.listings[record.Listing.Header.ID]; exists {
			return nil, ErrSTRIDEListingInvalid
		}
		marketplace.listings[record.Listing.Header.ID] = cloneSTRIDEMarketplaceListingRecord(record)
	}
	for _, record := range marketplace.packages {
		if record.RollbackTo == nil {
			continue
		}
		rollback, found := marketplace.packageByReferenceLocked(*record.RollbackTo)
		if !found || rollback.State != "verified" || rollback.Reference == record.Reference {
			return nil, ErrSTRIDEPackageInvalid
		}
	}
	return marketplace, nil
}

func strideMarketplaceReceiptShapeComplete(listing MarketplaceListing) bool {
	return listing.QualityReceipt != nil && listing.SafetyReceipt != nil && listing.VoiceReceipt != nil && listing.PublisherStatus == "active"
}

func strideMarketplaceSnapshotDigest(packages []STRIDEMarketplaceRecord, listings []STRIDEMarketplaceListingRecord) (string, error) {
	return STRIDEContractDigest(struct {
		Packages []STRIDEMarketplaceRecord
		Listings []STRIDEMarketplaceListingRecord
	}{packages, listings})
}

func (marketplace *STRIDEMarketplace) packageByReferenceLocked(reference STRIDEReference) (STRIDEMarketplaceRecord, bool) {
	for _, record := range marketplace.packages {
		if record.Reference == reference {
			return record, true
		}
	}
	return STRIDEMarketplaceRecord{}, false
}

func marketplaceListingView(record STRIDEMarketplaceListingRecord) STRIDEMarketplaceView {
	return STRIDEMarketplaceView{ListingID: record.Listing.Header.ID, Package: record.Listing.Package, Category: record.Listing.Category, OutcomeDigest: record.Listing.OutcomeDigest, PersonalityDigest: record.PersonalityDigest, Evidence: SortedSTRIDEReferences(record.Listing.Evidence), AccessSummaryDigest: record.Listing.PermissionSummaryDigest, CostBand: record.Listing.CostBand, Provenance: record.Provenance, State: record.State, ReceiptSetComplete: record.ReceiptSetComplete, Available: record.Available}
}

func cloneSTRIDEMarketplaceRecord(record STRIDEMarketplaceRecord) STRIDEMarketplaceRecord {
	record.Manifest.AssetRefs = append([]STRIDEReference(nil), record.Manifest.AssetRefs...)
	record.Manifest.EvalBundleRefs = append([]STRIDEReference(nil), record.Manifest.EvalBundleRefs...)
	record.Manifest.DependencySBOMRefs = append([]STRIDEReference(nil), record.Manifest.DependencySBOMRefs...)
	record.RollbackTo = cloneSTRIDEReference(record.RollbackTo)
	return record
}
func cloneSTRIDEMarketplaceListingRecord(record STRIDEMarketplaceListingRecord) STRIDEMarketplaceListingRecord {
	record.Listing.Evidence = append([]STRIDEReference(nil), record.Listing.Evidence...)
	record.Listing.QualityReceipt = cloneSTRIDEReference(record.Listing.QualityReceipt)
	record.Listing.SafetyReceipt = cloneSTRIDEReference(record.Listing.SafetyReceipt)
	record.Listing.VoiceReceipt = cloneSTRIDEReference(record.Listing.VoiceReceipt)
	return record
}
