package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	ErrSTRIDEAcceptanceTargetInvalid = errors.New("invalid STRIDE acceptance target")
	ErrSTRIDEAcceptanceSignature     = errors.New("invalid STRIDE acceptance registry signature")
	ErrSTRIDEAcceptanceSigner        = errors.New("STRIDE acceptance registry signer changed")
	ErrSTRIDEAcceptanceRevision      = errors.New("invalid STRIDE acceptance registry revision")
	ErrSTRIDEAcceptanceLoosened      = errors.New("measured STRIDE acceptance target cannot be loosened")
	ErrSTRIDEAcceptanceUnsealed      = errors.New("STRIDE acceptance target is not sealed")
)

type STRIDEAcceptanceComparator string

const (
	STRIDEAcceptanceAtLeast       STRIDEAcceptanceComparator = "at_least"
	STRIDEAcceptanceAtMost        STRIDEAcceptanceComparator = "at_most"
	STRIDEAcceptanceExactlyZero   STRIDEAcceptanceComparator = "exactly_zero"
	STRIDEAcceptanceNonInferiorBy STRIDEAcceptanceComparator = "noninferiority_margin_at_most"
)

type STRIDEAcceptanceTarget struct {
	ID                  string                     `json:"id"`
	Wave                string                     `json:"wave"`
	MetricDefinition    string                     `json:"metricDefinition"`
	FixtureDigest       string                     `json:"fixtureDigest"`
	EnvironmentDigest   string                     `json:"environmentDigest"`
	SampleSize          int                        `json:"sampleSize"`
	Comparator          STRIDEAcceptanceComparator `json:"comparator"`
	Threshold           float64                    `json:"threshold"`
	ConfidenceMethod    string                     `json:"confidenceMethod"`
	MeasurementRevision string                     `json:"measurementRevision"`
	OwnerRole           string                     `json:"ownerRole"`
	RollbackTrigger     string                     `json:"rollbackTrigger"`
	Reviewer            string                     `json:"reviewer"`
	HardGate            bool                       `json:"hardGate"`
	CreatedAt           time.Time                  `json:"createdAt"`
}

func (target STRIDEAcceptanceTarget) Validate() error {
	if !strideIdentifier(target.ID) || !validSTRIDEWave(target.Wave) || strings.TrimSpace(target.MetricDefinition) == "" ||
		!isHexDigest(target.FixtureDigest) || !isHexDigest(target.EnvironmentDigest) || target.SampleSize < 1 ||
		!oneOf(string(target.Comparator), string(STRIDEAcceptanceAtLeast), string(STRIDEAcceptanceAtMost), string(STRIDEAcceptanceExactlyZero), string(STRIDEAcceptanceNonInferiorBy)) ||
		target.Threshold < 0 || !oneOf(target.ConfidenceMethod, "none", "wilson_95", "bootstrap_95", "paired_95") ||
		!isHexDigest(target.MeasurementRevision) || !strideIdentifier(target.OwnerRole) || strings.TrimSpace(target.RollbackTrigger) == "" ||
		!strideIdentifier(target.Reviewer) || target.CreatedAt.IsZero() {
		return ErrSTRIDEAcceptanceTargetInvalid
	}
	if target.Comparator == STRIDEAcceptanceExactlyZero && target.Threshold != 0 {
		return ErrSTRIDEAcceptanceTargetInvalid
	}
	if target.HardGate && (target.Comparator != STRIDEAcceptanceExactlyZero || target.Threshold != 0) {
		return ErrSTRIDEAcceptanceTargetInvalid
	}
	return nil
}

type STRIDEAcceptanceRegistryRevision struct {
	RegistryID     string                   `json:"registryId"`
	Revision       int64                    `json:"revision"`
	PreviousDigest string                   `json:"previousDigest,omitempty"`
	Targets        []STRIDEAcceptanceTarget `json:"targets"`
	CreatedAt      time.Time                `json:"createdAt"`
	SignerKeyID    string                   `json:"signerKeyId"`
	ContentDigest  string                   `json:"contentDigest"`
	Signature      string                   `json:"signature"`
}

type strideAcceptanceUnsignedRevision struct {
	RegistryID     string                   `json:"registryId"`
	Revision       int64                    `json:"revision"`
	PreviousDigest string                   `json:"previousDigest,omitempty"`
	Targets        []STRIDEAcceptanceTarget `json:"targets"`
	CreatedAt      time.Time                `json:"createdAt"`
	SignerKeyID    string                   `json:"signerKeyId"`
}

func NewSignedSTRIDEAcceptanceRegistryRevision(registryID string, revision int64, previousDigest string, targets []STRIDEAcceptanceTarget, createdAt time.Time, signerKeyID string, privateKey ed25519.PrivateKey) (STRIDEAcceptanceRegistryRevision, error) {
	record := STRIDEAcceptanceRegistryRevision{RegistryID: registryID, Revision: revision, PreviousDigest: previousDigest, Targets: append([]STRIDEAcceptanceTarget(nil), targets...), CreatedAt: createdAt.UTC(), SignerKeyID: signerKeyID}
	if err := record.validateUnsigned(); err != nil || len(privateKey) != ed25519.PrivateKeySize {
		return STRIDEAcceptanceRegistryRevision{}, ErrSTRIDEAcceptanceTargetInvalid
	}
	digest, err := record.unsignedDigest()
	if err != nil {
		return STRIDEAcceptanceRegistryRevision{}, err
	}
	record.ContentDigest = digest
	record.Signature = base64.RawURLEncoding.EncodeToString(ed25519.Sign(privateKey, []byte(digest)))
	return record, nil
}

func (record STRIDEAcceptanceRegistryRevision) Verify(publicKey ed25519.PublicKey) error {
	if err := record.validateUnsigned(); err != nil || len(publicKey) != ed25519.PublicKeySize || !isHexDigest(record.ContentDigest) {
		return ErrSTRIDEAcceptanceSignature
	}
	digest, err := record.unsignedDigest()
	if err != nil || digest != record.ContentDigest {
		return ErrSTRIDEAcceptanceSignature
	}
	signature, err := base64.RawURLEncoding.DecodeString(record.Signature)
	if err != nil || !ed25519.Verify(publicKey, []byte(record.ContentDigest), signature) {
		return ErrSTRIDEAcceptanceSignature
	}
	return nil
}

func (record STRIDEAcceptanceRegistryRevision) validateUnsigned() error {
	if !strideIdentifier(record.RegistryID) || record.Revision < 1 || (record.Revision == 1) != (record.PreviousDigest == "") ||
		(record.PreviousDigest != "" && !isHexDigest(record.PreviousDigest)) || len(record.Targets) == 0 || record.CreatedAt.IsZero() || !strideIdentifier(record.SignerKeyID) {
		return ErrSTRIDEAcceptanceTargetInvalid
	}
	previous := ""
	for _, target := range record.Targets {
		if target.Validate() != nil || (previous != "" && target.ID <= previous) {
			return ErrSTRIDEAcceptanceTargetInvalid
		}
		previous = target.ID
	}
	return nil
}

func (record STRIDEAcceptanceRegistryRevision) unsignedDigest() (string, error) {
	payload := strideAcceptanceUnsignedRevision{RegistryID: record.RegistryID, Revision: record.Revision, PreviousDigest: record.PreviousDigest, Targets: record.Targets, CreatedAt: record.CreatedAt.UTC(), SignerKeyID: record.SignerKeyID}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw)
	return fmt.Sprintf("%x", digest[:]), nil
}

type STRIDEAcceptanceMeasurementStart struct {
	RegistryID        string    `json:"registryId"`
	RegistryRevision  int64     `json:"registryRevision"`
	RegistryDigest    string    `json:"registryDigest"`
	TargetID          string    `json:"targetId"`
	EvidenceClass     string    `json:"evidenceClass"`
	StartedAt         time.Time `json:"startedAt"`
	ProviderQualified bool      `json:"providerQualified"`
}

type STRIDEAcceptanceTargetBook struct {
	mu               sync.Mutex
	current          map[string]STRIDEAcceptanceRegistryRevision
	measured         map[string]map[string]bool
	trustedSigner    strideAcceptanceSignerBinding
	trustedPublicKey ed25519.PublicKey
}

type strideAcceptanceSignerBinding struct {
	keyID     string
	keyDigest [sha256.Size]byte
}

func NewSTRIDEAcceptanceTargetBook(signerKeyID string, publicKey ed25519.PublicKey) (*STRIDEAcceptanceTargetBook, error) {
	if !strideIdentifier(signerKeyID) || len(publicKey) != ed25519.PublicKeySize {
		return nil, ErrSTRIDEAcceptanceSigner
	}
	key := append(ed25519.PublicKey(nil), publicKey...)
	return &STRIDEAcceptanceTargetBook{
		current:          map[string]STRIDEAcceptanceRegistryRevision{},
		measured:         map[string]map[string]bool{},
		trustedSigner:    strideAcceptanceSignerBinding{keyID: signerKeyID, keyDigest: sha256.Sum256(key)},
		trustedPublicKey: key,
	}, nil
}

func (book *STRIDEAcceptanceTargetBook) Register(record STRIDEAcceptanceRegistryRevision) error {
	if book == nil || record.SignerKeyID != book.trustedSigner.keyID {
		return ErrSTRIDEAcceptanceSigner
	}
	if record.Verify(book.trustedPublicKey) != nil {
		return ErrSTRIDEAcceptanceSignature
	}
	book.mu.Lock()
	defer book.mu.Unlock()
	prior, exists := book.current[record.RegistryID]
	if !exists {
		if record.Revision != 1 || record.PreviousDigest != "" {
			return ErrSTRIDEAcceptanceRevision
		}
		book.current[record.RegistryID] = cloneSTRIDEAcceptanceRevision(record)
		return nil
	}
	if record.Revision != prior.Revision+1 || record.PreviousDigest != prior.ContentDigest || !record.CreatedAt.After(prior.CreatedAt) {
		return ErrSTRIDEAcceptanceRevision
	}
	measured := book.measured[record.RegistryID]
	if len(measured) > 0 && acceptanceRevisionLoosens(prior, record, measured) {
		return ErrSTRIDEAcceptanceLoosened
	}
	book.current[record.RegistryID] = cloneSTRIDEAcceptanceRevision(record)
	return nil
}

func (book *STRIDEAcceptanceTargetBook) BeginMeasurement(registryID string, revision int64, targetID, evidenceClass string, at time.Time) (STRIDEAcceptanceMeasurementStart, error) {
	if book == nil || !oneOf(evidenceClass, "synthetic", "recorded_provider_fixture", "live_provider", "external_operation") || at.IsZero() {
		return STRIDEAcceptanceMeasurementStart{}, ErrSTRIDEAcceptanceUnsealed
	}
	book.mu.Lock()
	defer book.mu.Unlock()
	record, exists := book.current[registryID]
	if !exists || record.Revision != revision {
		return STRIDEAcceptanceMeasurementStart{}, ErrSTRIDEAcceptanceUnsealed
	}
	found := false
	for _, target := range record.Targets {
		if target.ID == targetID {
			found = true
			break
		}
	}
	if !found {
		return STRIDEAcceptanceMeasurementStart{}, ErrSTRIDEAcceptanceUnsealed
	}
	if book.measured[registryID] == nil {
		book.measured[registryID] = map[string]bool{}
	}
	book.measured[registryID][targetID] = true
	return STRIDEAcceptanceMeasurementStart{RegistryID: registryID, RegistryRevision: revision, RegistryDigest: record.ContentDigest, TargetID: targetID, EvidenceClass: evidenceClass, StartedAt: at.UTC(), ProviderQualified: false}, nil
}

func acceptanceRevisionLoosens(prior, next STRIDEAcceptanceRegistryRevision, measured map[string]bool) bool {
	nextByID := map[string]STRIDEAcceptanceTarget{}
	for _, target := range next.Targets {
		nextByID[target.ID] = target
	}
	for _, old := range prior.Targets {
		if !measured[old.ID] {
			continue
		}
		candidate, exists := nextByID[old.ID]
		if !exists || acceptanceTargetLooser(old, candidate) {
			return true
		}
	}
	return false
}

func acceptanceTargetLooser(old, next STRIDEAcceptanceTarget) bool {
	if old.Comparator != next.Comparator || old.MetricDefinition != next.MetricDefinition || old.FixtureDigest != next.FixtureDigest ||
		old.EnvironmentDigest != next.EnvironmentDigest || old.MeasurementRevision != next.MeasurementRevision || old.HardGate != next.HardGate ||
		next.SampleSize < old.SampleSize || next.ConfidenceMethod != old.ConfidenceMethod {
		return true
	}
	switch old.Comparator {
	case STRIDEAcceptanceAtLeast:
		return next.Threshold < old.Threshold
	case STRIDEAcceptanceAtMost, STRIDEAcceptanceNonInferiorBy:
		return next.Threshold > old.Threshold
	case STRIDEAcceptanceExactlyZero:
		return next.Threshold != 0
	default:
		return true
	}
}

func cloneSTRIDEAcceptanceRevision(record STRIDEAcceptanceRegistryRevision) STRIDEAcceptanceRegistryRevision {
	record.Targets = append([]STRIDEAcceptanceTarget(nil), record.Targets...)
	return record
}

func validSTRIDEWave(wave string) bool {
	if len(wave) < 2 || wave[0] != 'E' {
		return false
	}
	for _, candidate := range []string{"E0", "E1", "E2", "E3", "E4", "E5", "E6", "E7", "E8", "E9", "E10"} {
		if wave == candidate {
			return true
		}
	}
	return false
}

func SortSTRIDEAcceptanceTargets(targets []STRIDEAcceptanceTarget) []STRIDEAcceptanceTarget {
	result := append([]STRIDEAcceptanceTarget(nil), targets...)
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}
