package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	canonicalLegacyRepairManifestSchema = "bonfire.canonical-legacy-lifecycle-repair.v1"
	canonicalLegacyRepairReceiptSchema  = "bonfire.canonical-legacy-lifecycle-repair-receipt.v1"
	canonicalLegacyRepairReason         = "sealed_target_only_legacy_lifecycle_repair"
	canonicalLegacyRepairEvidence       = "two_stable_source_absence_observations"
	canonicalLegacyRepairMaxCandidates  = 1024
)

var canonicalLegacyRepairFamilies = map[string]bool{
	"artifact_revision": true,
	"board_card":        true,
	"file_assignment":   true,
	"file_folder":       true,
	"memory":            true,
	"notification":      true,
}

type canonicalLegacyRepairManifest struct {
	Schema                  string                       `json:"schema"`
	ReleaseCommit           string                       `json:"releaseCommit"`
	TenantID                string                       `json:"tenantId"`
	DataDir                 string                       `json:"dataDir"`
	DatabaseURLSHA256       string                       `json:"databaseUrlSha256"`
	FirstObservationSHA256  string                       `json:"firstObservationSha256"`
	SecondObservationSHA256 string                       `json:"secondObservationSha256"`
	ImportInputSHA256       string                       `json:"importInputSha256"`
	ProofSHA256             string                       `json:"proofSha256"`
	DatabaseSHA256          string                       `json:"databaseSha256"`
	CandidateSetSHA256      string                       `json:"candidateSetSha256"`
	Candidates              []CanonicalRepairCandidate   `json:"candidates"`
	Board                   canonicalBoardRepairFileSeal `json:"board"`
	Journal                 canonicalBoardRepairFileSeal `json:"journal"`
	VersionMap              canonicalBoardRepairFileSeal `json:"versionMap"`
	Spool                   canonicalBoardRepairFileSeal `json:"spool"`
	VersionEntriesSHA256    string                       `json:"versionEntriesSha256"`
	EventHighWater          int64                        `json:"eventHighWater"`
	TenantEventCount        int64                        `json:"tenantEventCount"`
	ImportOutboxCount       int64                        `json:"importOutboxCount"`
	VersionEntryCount       int                          `json:"versionEntryCount"`
	PrincipalParity         bool                         `json:"principalParity"`
	ProjectionReplayValid   bool                         `json:"projectionReplayValid"`
	FirstObservedAt         time.Time                    `json:"firstObservedAt"`
	SecondObservedAt        time.Time                    `json:"secondObservedAt"`
}

type canonicalLegacyRepairReceipt struct {
	Schema                 string                       `json:"schema"`
	Status                 string                       `json:"status"`
	ReleaseCommit          string                       `json:"releaseCommit"`
	TenantID               string                       `json:"tenantId"`
	ManifestSHA256         string                       `json:"manifestSha256"`
	AuthoritySHA256        string                       `json:"authoritySha256"`
	CandidateSetSHA256     string                       `json:"candidateSetSha256"`
	CandidateCount         int                          `json:"candidateCount"`
	AppliedEventCount      int64                        `json:"appliedEventCount"`
	AppliedOutboxCount     int64                        `json:"appliedOutboxCount"`
	AppliedVersionCount    int                          `json:"appliedVersionCount"`
	JournalBefore          canonicalBoardRepairFileSeal `json:"journalBefore"`
	JournalAfter           canonicalBoardRepairFileSeal `json:"journalAfter"`
	DatabaseBeforeSHA256   string                       `json:"databaseBeforeSha256"`
	DatabaseAfterSHA256    string                       `json:"databaseAfterSha256"`
	BeforeProofSHA256      string                       `json:"beforeProofSha256"`
	AfterProofSHA256       string                       `json:"afterProofSha256"`
	EventHighWaterBefore   int64                        `json:"eventHighWaterBefore"`
	EventHighWaterAfter    int64                        `json:"eventHighWaterAfter"`
	ZeroCandidates         bool                         `json:"zeroCandidates"`
	PrincipalParity        bool                         `json:"principalParity"`
	ProjectionReplayValid  bool                         `json:"projectionReplayValid"`
	IdempotentSecondReplay bool                         `json:"idempotentSecondReplay"`
	CompletedAt            time.Time                    `json:"completedAt"`
	SelfSHA256             string                       `json:"receiptSha256,omitempty"`
}

type canonicalLegacyRepairEngine interface {
	Observe(context.Context) (canonicalBoardRepairProof, error)
	AppendLifecycleBatch([]CanonicalLifecycleJournalRecord) error
	ApplyOrdinary(context.Context) error
}

type postgresCanonicalLegacyRepairEngine struct {
	*postgresCanonicalBoardRepairEngine
	beforeAppend func() error
}

func (engine postgresCanonicalLegacyRepairEngine) AppendLifecycleBatch(records []CanonicalLifecycleJournalRecord) error {
	if engine.beforeAppend != nil {
		if err := engine.beforeAppend(); err != nil {
			return err
		}
	}
	return appendCanonicalLegacyRepairBatchAtomic(engine.journalPath, records)
}

func appendCanonicalLegacyRepairBatchAtomic(path string, requested []CanonicalLifecycleJournalRecord) error {
	canonicalLifecycleJournalMu.Lock()
	defer canonicalLifecycleJournalMu.Unlock()
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		raw = nil
	} else if err != nil {
		return err
	}
	records, err := readCanonicalLifecycleJournal(path)
	if err != nil {
		return err
	}
	committed, _, err := classifyLifecycleJournal(records)
	if err != nil {
		return err
	}
	already := 0
	var suffix bytes.Buffer
	for _, record := range requested {
		if strings.TrimSpace(record.Family) == "" || strings.TrimSpace(record.ObjectID) == "" || !isHexDigest(record.StateDigest) ||
			record.At.IsZero() || record.At.Location() != time.UTC || strings.TrimSpace(record.Reason) == "" || record.OperationID != "" || record.Phase != "" {
			return errors.New("canonical legacy repair batch contains an invalid lifecycle record")
		}
		matched := false
		for _, existing := range committed {
			if existing.Family == record.Family && existing.ObjectID == record.ObjectID && existing.StateDigest == record.StateDigest {
				if existing.Reason != record.Reason || existing.EvidenceBasis != record.EvidenceBasis {
					return fmt.Errorf("canonical legacy repair conflicts with existing lifecycle evidence for %s/%s", record.Family, record.ObjectID)
				}
				matched = true
				break
			}
		}
		if matched {
			already++
			continue
		}
		encoded, err := json.Marshal(record)
		if err != nil {
			return err
		}
		suffix.Write(encoded)
		suffix.WriteByte('\n')
	}
	if already != 0 {
		if already == len(requested) {
			return nil
		}
		return errors.New("canonical legacy repair journal contains only part of the requested logical batch")
	}
	if suffix.Len() == 0 {
		return nil
	}
	combined := append(append([]byte(nil), raw...), suffix.Bytes()...)
	return writeFileAtomicallyDurable(path, combined, 0o600)
}

func canonicalLegacyRepairCandidateSet(candidates []CanonicalRepairCandidate) ([]CanonicalRepairCandidate, string, error) {
	copyCandidates := append([]CanonicalRepairCandidate(nil), candidates...)
	sort.Slice(copyCandidates, func(i, j int) bool {
		if copyCandidates[i].Family != copyCandidates[j].Family {
			return copyCandidates[i].Family < copyCandidates[j].Family
		}
		return copyCandidates[i].ObjectID < copyCandidates[j].ObjectID
	})
	seen := map[string]bool{}
	for _, candidate := range copyCandidates {
		key := candidate.Family + "\x00" + candidate.ObjectID
		if seen[key] {
			return nil, "", fmt.Errorf("duplicate legacy repair candidate %s/%s", candidate.Family, candidate.ObjectID)
		}
		seen[key] = true
		if !canonicalLegacyRepairFamilies[candidate.Family] || candidate.Kind != "tombstone_required" ||
			strings.TrimSpace(candidate.ObjectID) == "" || !isHexDigest(candidate.StateDigest) || candidate.TargetVersion < 1 || candidate.ConfirmedByJournal ||
			candidate.SourceStateDigest != "" || candidate.TargetStateDigest != "" || candidate.SourceVersion != 0 || candidate.Principal != "" || candidate.Event != nil {
			return nil, "", fmt.Errorf("invalid legacy repair candidate %s/%s", candidate.Family, candidate.ObjectID)
		}
	}
	raw, err := canonicalJSON(copyCandidates)
	if err != nil {
		return nil, "", err
	}
	return copyCandidates, sha256Hex(raw), nil
}

func validateCanonicalLegacyRepairManifest(manifest canonicalLegacyRepairManifest) error {
	if manifest.Schema != canonicalLegacyRepairManifestSchema || !isExactLowerHex(manifest.ReleaseCommit, 40) ||
		strings.TrimSpace(manifest.TenantID) == "" || !filepath.IsAbs(manifest.DataDir) || !isHexDigest(manifest.DatabaseURLSHA256) ||
		!isHexDigest(manifest.FirstObservationSHA256) || !isHexDigest(manifest.SecondObservationSHA256) || !isHexDigest(manifest.ImportInputSHA256) || !isHexDigest(manifest.ProofSHA256) ||
		!isHexDigest(manifest.DatabaseSHA256) || !isHexDigest(manifest.CandidateSetSHA256) || !isHexDigest(manifest.VersionEntriesSHA256) ||
		manifest.EventHighWater < 1 || manifest.TenantEventCount < 1 || manifest.ImportOutboxCount < 0 || manifest.VersionEntryCount < 1 ||
		manifest.FirstObservedAt.IsZero() || manifest.FirstObservedAt.Location() != time.UTC || manifest.SecondObservedAt.IsZero() || manifest.SecondObservedAt.Location() != time.UTC ||
		manifest.SecondObservedAt.Sub(manifest.FirstObservedAt) < 10*time.Second || manifest.SecondObservedAt.Sub(manifest.FirstObservedAt) > 15*time.Minute ||
		!manifest.PrincipalParity || !manifest.ProjectionReplayValid {
		return errors.New("invalid canonical legacy repair manifest binding")
	}
	if len(manifest.Candidates) < 1 || len(manifest.Candidates) > canonicalLegacyRepairMaxCandidates {
		return errors.New("canonical legacy repair candidate count is out of bounds")
	}
	for _, seal := range []canonicalBoardRepairFileSeal{manifest.Board, manifest.Journal, manifest.VersionMap, manifest.Spool} {
		if seal.Size < 0 || !isHexDigest(seal.SHA256) {
			return errors.New("invalid canonical legacy repair file seal")
		}
	}
	ordered, digest, err := canonicalLegacyRepairCandidateSet(manifest.Candidates)
	if err != nil {
		return err
	}
	if digest != manifest.CandidateSetSHA256 {
		return errors.New("canonical legacy repair candidate digest mismatch")
	}
	for index := range ordered {
		if ordered[index].Family != manifest.Candidates[index].Family || ordered[index].ObjectID != manifest.Candidates[index].ObjectID {
			return errors.New("canonical legacy repair candidates are not in canonical order")
		}
	}
	return nil
}

func canonicalLegacyRepairManifestFromObservations(first, second canonicalBoardRepairObservation, firstSHA, secondSHA string) (canonicalLegacyRepairManifest, error) {
	if first.Schema != "bonfire.canonical-board-repair-observation.v1" || second.Schema != first.Schema ||
		second.ObservedAt.Sub(first.ObservedAt) < 10*time.Second || second.ObservedAt.Sub(first.ObservedAt) > 15*time.Minute ||
		first.ReleaseCommit != second.ReleaseCommit || first.TenantID != second.TenantID || filepath.Clean(first.DataDir) != filepath.Clean(second.DataDir) ||
		first.DatabaseURLSHA256 != second.DatabaseURLSHA256 || first.DatabaseSHA256 != second.DatabaseSHA256 || first.ImportInputSHA256 != second.ImportInputSHA256 ||
		first.ProofFingerprint != second.ProofFingerprint || first.CandidateFingerprint != second.CandidateFingerprint || first.CandidateCount != second.CandidateCount ||
		first.Board != second.Board || first.Journal != second.Journal || first.VersionMap != second.VersionMap || first.Spool != second.Spool ||
		first.VersionEntriesSHA256 != second.VersionEntriesSHA256 || first.EventHighWater != second.EventHighWater || first.TenantEventCount != second.TenantEventCount ||
		first.OutboxCount != second.OutboxCount || first.VersionEntryCount != second.VersionEntryCount || !first.PrincipalParity || !second.PrincipalParity ||
		!first.ProjectionReplayValid || !second.ProjectionReplayValid {
		return canonicalLegacyRepairManifest{}, errors.New("canonical legacy repair observations are not an exact stable pair")
	}
	firstCandidates, firstCandidateSHA, err := canonicalLegacyRepairCandidateSet(first.Candidates)
	if err != nil {
		return canonicalLegacyRepairManifest{}, err
	}
	ordered, candidateSHA, err := canonicalLegacyRepairCandidateSet(second.Candidates)
	if err != nil {
		return canonicalLegacyRepairManifest{}, err
	}
	if firstCandidateSHA != candidateSHA || len(firstCandidates) != len(ordered) {
		return canonicalLegacyRepairManifest{}, errors.New("canonical legacy repair candidate sets changed between observations")
	}
	manifest := canonicalLegacyRepairManifest{
		Schema: canonicalLegacyRepairManifestSchema, ReleaseCommit: second.ReleaseCommit, TenantID: second.TenantID,
		DataDir: second.DataDir, DatabaseURLSHA256: second.DatabaseURLSHA256, FirstObservationSHA256: firstSHA, SecondObservationSHA256: secondSHA,
		ImportInputSHA256: second.ImportInputSHA256, ProofSHA256: second.ProofFingerprint, DatabaseSHA256: second.DatabaseSHA256,
		CandidateSetSHA256: candidateSHA, Candidates: ordered, Board: second.Board, Journal: second.Journal,
		VersionMap: second.VersionMap, Spool: second.Spool, VersionEntriesSHA256: second.VersionEntriesSHA256,
		EventHighWater: second.EventHighWater, TenantEventCount: second.TenantEventCount, ImportOutboxCount: second.OutboxCount,
		VersionEntryCount: second.VersionEntryCount, PrincipalParity: second.PrincipalParity,
		ProjectionReplayValid: second.ProjectionReplayValid, FirstObservedAt: first.ObservedAt, SecondObservedAt: second.ObservedAt,
	}
	return manifest, validateCanonicalLegacyRepairManifest(manifest)
}

func generateCanonicalLegacyRepairManifestCLI(firstObservationPath, secondObservationPath, manifestPath string) error {
	if os.Geteuid() != 0 {
		return errors.New("canonical legacy repair manifest generation must run as root")
	}
	firstObservationPath, secondObservationPath, manifestPath = filepath.Clean(strings.TrimSpace(firstObservationPath)), filepath.Clean(strings.TrimSpace(secondObservationPath)), filepath.Clean(strings.TrimSpace(manifestPath))
	if !filepath.IsAbs(firstObservationPath) || !filepath.IsAbs(secondObservationPath) || !filepath.IsAbs(manifestPath) || firstObservationPath == secondObservationPath {
		return errors.New("two distinct canonical legacy repair observations and the manifest path must be absolute")
	}
	firstRaw, err := readRootOnlyRegularFile(firstObservationPath)
	if err != nil {
		return err
	}
	secondRaw, err := readRootOnlyRegularFile(secondObservationPath)
	if err != nil {
		return err
	}
	var first, second canonicalBoardRepairObservation
	if err := decodeCanonicalRepairJSON(firstRaw, &first); err != nil {
		return err
	}
	if err := decodeCanonicalRepairJSON(secondRaw, &second); err != nil {
		return err
	}
	for _, observation := range []canonicalBoardRepairObservation{first, second} {
		if observation.Schema != "bonfire.canonical-board-repair-observation.v1" || observation.CandidateCount != len(observation.Candidates) {
			return errors.New("invalid canonical repair observation")
		}
		candidateSHA, err := canonicalRepairCandidateDigest(observation.Candidates)
		if err != nil || candidateSHA != observation.CandidateFingerprint {
			return errors.New("canonical repair observation candidate seal mismatch")
		}
	}
	manifest, err := canonicalLegacyRepairManifestFromObservations(first, second, sha256Hex(firstRaw), sha256Hex(secondRaw))
	if err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomicallyDurable(manifestPath, append(encoded, '\n'), 0o600)
}

func canonicalLegacyRepairRecords(manifest canonicalLegacyRepairManifest, at time.Time) []CanonicalLifecycleJournalRecord {
	records := make([]CanonicalLifecycleJournalRecord, 0, len(manifest.Candidates))
	for index, candidate := range manifest.Candidates {
		records = append(records, CanonicalLifecycleJournalRecord{
			Family: candidate.Family, ObjectID: candidate.ObjectID, StateDigest: candidate.StateDigest,
			At: at.UTC().Add(time.Duration(index) * time.Nanosecond), Reason: canonicalLegacyRepairReason, EvidenceBasis: canonicalLegacyRepairEvidence,
		})
	}
	return records
}

func canonicalLegacyRepairProofMatchesManifest(proof canonicalBoardRepairProof, manifest canonicalLegacyRepairManifest) error {
	if canonicalRepairProofFingerprint(proof) != manifest.ProofSHA256 || proof.DatabaseSHA256 != manifest.DatabaseSHA256 ||
		proof.ImportInputSHA256 != manifest.ImportInputSHA256 || proof.Board != manifest.Board || proof.Journal != manifest.Journal ||
		proof.VersionMap != manifest.VersionMap || proof.Spool != manifest.Spool || proof.EventHighWater != manifest.EventHighWater ||
		proof.EventCount != manifest.TenantEventCount || proof.OutboxCount != manifest.ImportOutboxCount || proof.VersionEntryCount != manifest.VersionEntryCount ||
		proof.VersionEntriesSHA256 != manifest.VersionEntriesSHA256 || !proof.PrincipalParity || !proof.ProjectionReplayValid {
		return errors.New("live canonical legacy repair pre-state does not match manifest")
	}
	_, digest, err := canonicalLegacyRepairCandidateSet(proof.Candidates)
	if err != nil || digest != manifest.CandidateSetSHA256 || len(proof.Candidates) != len(manifest.Candidates) {
		return errors.New("live canonical legacy repair candidates do not match manifest")
	}
	return nil
}

func validateCanonicalLegacyRepairFinalProof(proof canonicalBoardRepairProof, manifest canonicalLegacyRepairManifest) error {
	expectedDelta := int64(2 * len(manifest.Candidates))
	if len(proof.Candidates) != 0 || proof.Diverged || !proof.PrincipalParity || !proof.ProjectionReplayValid ||
		proof.EventCount-manifest.TenantEventCount != expectedDelta || proof.EventHighWater-manifest.EventHighWater != expectedDelta ||
		proof.OutboxCount-manifest.ImportOutboxCount != expectedDelta || proof.VersionEntryCount-manifest.VersionEntryCount != int(expectedDelta) {
		return fmt.Errorf("canonical legacy repair did not converge to its exact zero-candidate delta: candidates=%d diverged=%t principal=%t projection=%t event_delta=%d high_water_delta=%d outbox_delta=%d version_delta=%d expected_events=%d expected_versions=%d",
			len(proof.Candidates), proof.Diverged, proof.PrincipalParity, proof.ProjectionReplayValid,
			proof.EventCount-manifest.TenantEventCount, proof.EventHighWater-manifest.EventHighWater,
			proof.OutboxCount-manifest.ImportOutboxCount, proof.VersionEntryCount-manifest.VersionEntryCount,
			expectedDelta, expectedDelta)
	}
	return nil
}

func canonicalLegacyRepairRecoveryProofAllowed(proof canonicalBoardRepairProof, manifest canonicalLegacyRepairManifest) error {
	eventDelta := proof.EventCount - manifest.TenantEventCount
	versionDelta := proof.VersionEntryCount - manifest.VersionEntryCount
	maxEventDelta := int64(2 * len(manifest.Candidates))
	if eventDelta < 0 || eventDelta > maxEventDelta || proof.EventHighWater-manifest.EventHighWater != eventDelta ||
		proof.OutboxCount-manifest.ImportOutboxCount != eventDelta || versionDelta < 0 || versionDelta > int(maxEventDelta) ||
		proof.Board != manifest.Board || proof.Spool != manifest.Spool || !proof.PrincipalParity || !proof.ProjectionReplayValid {
		return errors.New("canonical legacy repair recovery state is outside the sealed mutation envelope")
	}
	return nil
}

func executeCanonicalLegacyRepairWithRecovery(ctx context.Context, manifest canonicalLegacyRepairManifest, engine canonicalLegacyRepairEngine, now time.Time, recoveryAllowed bool) (canonicalLegacyRepairReceipt, error) {
	if err := validateCanonicalLegacyRepairManifest(manifest); err != nil {
		return canonicalLegacyRepairReceipt{}, err
	}
	before, err := engine.Observe(ctx)
	if err != nil {
		return canonicalLegacyRepairReceipt{}, err
	}
	return executeCanonicalLegacyRepairFromObserved(ctx, manifest, engine, now, recoveryAllowed, before)
}

// executeCanonicalLegacyRepairFromObserved consumes the exact proof already
// observed by the production CLI. Keeping that proof avoids repeating the
// expensive full-store observation while the root authority marker ages.
func executeCanonicalLegacyRepairFromObserved(ctx context.Context, manifest canonicalLegacyRepairManifest, engine canonicalLegacyRepairEngine, now time.Time, recoveryAllowed bool, before canonicalBoardRepairProof) (canonicalLegacyRepairReceipt, error) {
	if err := validateCanonicalLegacyRepairManifest(manifest); err != nil {
		return canonicalLegacyRepairReceipt{}, err
	}
	if err := canonicalLegacyRepairProofMatchesManifest(before, manifest); err != nil {
		if !recoveryAllowed {
			return canonicalLegacyRepairReceipt{}, err
		}
		if recoveryErr := canonicalLegacyRepairRecoveryProofAllowed(before, manifest); recoveryErr != nil {
			return canonicalLegacyRepairReceipt{}, recoveryErr
		}
	}
	if err := engine.AppendLifecycleBatch(canonicalLegacyRepairRecords(manifest, now)); err != nil {
		return canonicalLegacyRepairReceipt{}, fmt.Errorf("append sealed canonical lifecycle batch: %w", err)
	}
	if err := engine.ApplyOrdinary(ctx); err != nil {
		return canonicalLegacyRepairReceipt{}, fmt.Errorf("apply canonical legacy repair: %w", err)
	}
	after, err := engine.Observe(ctx)
	if err != nil {
		return canonicalLegacyRepairReceipt{}, err
	}
	if err := validateCanonicalLegacyRepairFinalProof(after, manifest); err != nil {
		return canonicalLegacyRepairReceipt{}, err
	}
	firstFinal := canonicalRepairProofFingerprint(after)
	if err := engine.ApplyOrdinary(ctx); err != nil {
		return canonicalLegacyRepairReceipt{}, fmt.Errorf("replay canonical legacy repair: %w", err)
	}
	second, err := engine.Observe(ctx)
	if err != nil {
		return canonicalLegacyRepairReceipt{}, err
	}
	if err := validateCanonicalLegacyRepairFinalProof(second, manifest); err != nil || canonicalRepairProofFingerprint(second) != firstFinal {
		return canonicalLegacyRepairReceipt{}, errors.New("canonical legacy repair second replay was not idempotent")
	}
	return canonicalLegacyRepairReceipt{
		Schema: canonicalLegacyRepairReceiptSchema, Status: "complete", ReleaseCommit: manifest.ReleaseCommit, TenantID: manifest.TenantID,
		CandidateSetSHA256: manifest.CandidateSetSHA256, CandidateCount: len(manifest.Candidates), AppliedEventCount: second.EventCount - manifest.TenantEventCount,
		AppliedOutboxCount: second.OutboxCount - manifest.ImportOutboxCount, AppliedVersionCount: second.VersionEntryCount - manifest.VersionEntryCount,
		JournalBefore: manifest.Journal, JournalAfter: second.Journal, DatabaseBeforeSHA256: manifest.DatabaseSHA256, DatabaseAfterSHA256: second.DatabaseSHA256,
		BeforeProofSHA256: manifest.ProofSHA256, AfterProofSHA256: firstFinal, EventHighWaterBefore: manifest.EventHighWater, EventHighWaterAfter: second.EventHighWater,
		ZeroCandidates: true, PrincipalParity: second.PrincipalParity, ProjectionReplayValid: second.ProjectionReplayValid,
		IdempotentSecondReplay: true, CompletedAt: now.UTC(),
	}, nil
}

func executeCanonicalLegacyRepair(ctx context.Context, manifest canonicalLegacyRepairManifest, engine canonicalLegacyRepairEngine, now time.Time) (canonicalLegacyRepairReceipt, error) {
	return executeCanonicalLegacyRepairWithRecovery(ctx, manifest, engine, now, false)
}

func canonicalLegacyRepairReceiptDigest(receipt canonicalLegacyRepairReceipt) (string, error) {
	receipt.SelfSHA256 = ""
	raw, err := canonicalJSON(receipt)
	if err != nil {
		return "", err
	}
	return sha256Hex(raw), nil
}

func validateCanonicalLegacyRepairReceipt(receipt canonicalLegacyRepairReceipt) error {
	want, err := canonicalLegacyRepairReceiptDigest(receipt)
	if err != nil {
		return err
	}
	if receipt.Schema != canonicalLegacyRepairReceiptSchema || receipt.Status != "complete" || !isExactLowerHex(receipt.ReleaseCommit, 40) ||
		strings.TrimSpace(receipt.TenantID) == "" || !isHexDigest(receipt.ManifestSHA256) || !isHexDigest(receipt.AuthoritySHA256) ||
		!isHexDigest(receipt.CandidateSetSHA256) || receipt.CandidateCount < 1 || receipt.AppliedEventCount != int64(2*receipt.CandidateCount) ||
		receipt.AppliedOutboxCount != receipt.AppliedEventCount || receipt.AppliedVersionCount != int(receipt.AppliedEventCount) ||
		!isHexDigest(receipt.DatabaseBeforeSHA256) || !isHexDigest(receipt.DatabaseAfterSHA256) || !isHexDigest(receipt.BeforeProofSHA256) ||
		!isHexDigest(receipt.AfterProofSHA256) || receipt.EventHighWaterAfter-receipt.EventHighWaterBefore != receipt.AppliedEventCount ||
		!receipt.ZeroCandidates || !receipt.PrincipalParity || !receipt.ProjectionReplayValid || !receipt.IdempotentSecondReplay ||
		receipt.CompletedAt.IsZero() || receipt.CompletedAt.Location() != time.UTC || receipt.SelfSHA256 != want {
		return errors.New("invalid canonical legacy repair receipt")
	}
	for _, seal := range []canonicalBoardRepairFileSeal{receipt.JournalBefore, receipt.JournalAfter} {
		if seal.Size < 0 || !isHexDigest(seal.SHA256) {
			return errors.New("invalid canonical legacy repair receipt journal seal")
		}
	}
	if receipt.JournalAfter.Size <= receipt.JournalBefore.Size || receipt.JournalAfter.SHA256 == receipt.JournalBefore.SHA256 {
		return errors.New("canonical legacy repair receipt does not prove a journal append")
	}
	return nil
}

func writeCanonicalLegacyRepairReceipt(path string, receipt canonicalLegacyRepairReceipt) error {
	digest, err := canonicalLegacyRepairReceiptDigest(receipt)
	if err != nil {
		return err
	}
	receipt.SelfSHA256 = digest
	if err := validateCanonicalLegacyRepairReceipt(receipt); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomicallyDurable(path, append(raw, '\n'), 0o600)
}

func runCanonicalLegacyRepairCLI(ctx context.Context, manifestPath, manifestSHA, authorityPath, receiptPath string) error {
	if os.Geteuid() != 0 {
		return errors.New("canonical legacy lifecycle repair must run as root")
	}
	manifestPath, authorityPath, receiptPath = filepath.Clean(strings.TrimSpace(manifestPath)), filepath.Clean(strings.TrimSpace(authorityPath)), filepath.Clean(strings.TrimSpace(receiptPath))
	if !filepath.IsAbs(manifestPath) || !filepath.IsAbs(authorityPath) || !filepath.IsAbs(receiptPath) || !isHexDigest(manifestSHA) {
		return errors.New("canonical legacy repair paths and manifest digest are required")
	}
	manifestRaw, err := readRootOnlyRegularFile(manifestPath)
	if err != nil || sha256Hex(manifestRaw) != strings.ToLower(manifestSHA) {
		return errors.New("canonical legacy repair manifest seal mismatch")
	}
	var manifest canonicalLegacyRepairManifest
	if err := decodeCanonicalRepairJSON(manifestRaw, &manifest); err != nil {
		return err
	}
	if err := validateCanonicalLegacyRepairManifest(manifest); err != nil {
		return err
	}
	if err := validateCanonicalRepairRelease(manifest.ReleaseCommit); err != nil {
		return err
	}
	dataDir := filepath.Clean(filepath.Dir(meetingMemoryPath()))
	if filepath.Clean(manifest.DataDir) != dataDir || !filepath.IsAbs(dataDir) {
		return errors.New("canonical legacy repair data directory mismatch")
	}
	if err := validateRootOwnedDirectory(dataDir); err != nil {
		return err
	}
	if err := validateRootOwnedReceiptParent(receiptPath, dataDir); err != nil {
		return err
	}
	authorityRaw, err := readRootOnlyRegularFile(authorityPath)
	if err != nil {
		return err
	}
	wantAuthority := "CONFIRM CANONICAL LEGACY LIFECYCLE REPAIR " + strings.ToLower(manifestSHA) + "\n"
	if string(authorityRaw) != wantAuthority {
		return errors.New("canonical legacy repair authority marker is invalid")
	}
	databaseURL := strings.TrimSpace(os.Getenv("BONFIRE_CANONICAL_DATABASE_URL"))
	if databaseURL == "" || sha256Hex([]byte(databaseURL)) != manifest.DatabaseURLSHA256 {
		return errors.New("canonical legacy repair database binding mismatch")
	}
	engine, store, err := openCanonicalRepairEngine(ctx, dataDir, manifest.TenantID)
	if err != nil {
		return err
	}
	defer store.Close()
	productionEngine := postgresCanonicalLegacyRepairEngine{
		postgresCanonicalBoardRepairEngine: engine,
		beforeAppend: func() error {
			currentAuthority, err := readRootOnlyRegularFile(authorityPath)
			if err != nil || !bytes.Equal(currentAuthority, authorityRaw) || string(currentAuthority) != wantAuthority {
				return errors.New("canonical legacy repair authority marker changed before mutation")
			}
			if err := validateCanonicalRepairAuthorityFresh(authorityPath, time.Now().UTC()); err != nil {
				return errors.New("canonical legacy repair authority marker expired before mutation")
			}
			return nil
		},
	}
	current, err := productionEngine.Observe(ctx)
	if err != nil {
		return err
	}
	if existing, found, err := readCanonicalLegacyRepairReceipt(receiptPath); err != nil {
		return err
	} else if found {
		return validateExistingCanonicalLegacyRepairReceipt(existing, manifest, strings.ToLower(manifestSHA), sha256Hex(authorityRaw), current)
	}
	if err := validateCanonicalRepairAuthorityFresh(authorityPath, time.Now().UTC()); err != nil {
		return errors.New("canonical legacy repair authority marker is stale")
	}
	recoveryAllowed := canonicalLegacyRepairProofMatchesManifest(current, manifest) != nil
	if recoveryAllowed {
		if err := validateCanonicalLegacyRepairRecoveryState(current, manifest); err != nil {
			return err
		}
	}
	receipt, err := executeCanonicalLegacyRepairFromObserved(ctx, manifest, productionEngine, time.Now().UTC(), recoveryAllowed, current)
	if err != nil {
		return err
	}
	receipt.ManifestSHA256, receipt.AuthoritySHA256 = strings.ToLower(manifestSHA), sha256Hex(authorityRaw)
	return writeCanonicalLegacyRepairReceipt(receiptPath, receipt)
}

func readCanonicalLegacyRepairReceipt(path string) (canonicalLegacyRepairReceipt, bool, error) {
	_, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return canonicalLegacyRepairReceipt{}, false, nil
	}
	if err != nil {
		return canonicalLegacyRepairReceipt{}, false, err
	}
	raw, err := readRootOnlyRegularFile(path)
	if err != nil {
		return canonicalLegacyRepairReceipt{}, false, err
	}
	var receipt canonicalLegacyRepairReceipt
	if err := decodeCanonicalRepairJSON(raw, &receipt); err != nil {
		return canonicalLegacyRepairReceipt{}, false, err
	}
	if err := validateCanonicalLegacyRepairReceipt(receipt); err != nil {
		return canonicalLegacyRepairReceipt{}, false, err
	}
	return receipt, true, nil
}

func validateExistingCanonicalLegacyRepairReceipt(receipt canonicalLegacyRepairReceipt, manifest canonicalLegacyRepairManifest, manifestSHA, authoritySHA string, live canonicalBoardRepairProof) error {
	if err := validateCanonicalLegacyRepairReceipt(receipt); err != nil {
		return err
	}
	if receipt.ReleaseCommit != manifest.ReleaseCommit || receipt.TenantID != manifest.TenantID || receipt.ManifestSHA256 != manifestSHA ||
		receipt.AuthoritySHA256 != authoritySHA || receipt.CandidateSetSHA256 != manifest.CandidateSetSHA256 || receipt.CandidateCount != len(manifest.Candidates) ||
		receipt.JournalBefore != manifest.Journal || receipt.DatabaseBeforeSHA256 != manifest.DatabaseSHA256 || receipt.BeforeProofSHA256 != manifest.ProofSHA256 ||
		receipt.EventHighWaterBefore != manifest.EventHighWater || receipt.JournalAfter != live.Journal || receipt.DatabaseAfterSHA256 != live.DatabaseSHA256 ||
		receipt.AfterProofSHA256 != canonicalRepairProofFingerprint(live) || receipt.EventHighWaterAfter != live.EventHighWater {
		return errors.New("canonical legacy repair receipt does not match the manifest and live state")
	}
	return validateCanonicalLegacyRepairFinalProof(live, manifest)
}

func validateCanonicalLegacyRepairRecoveryState(proof canonicalBoardRepairProof, manifest canonicalLegacyRepairManifest) error {
	if err := canonicalLegacyRepairRecoveryProofAllowed(proof, manifest); err != nil {
		return err
	}
	journalRaw := proof.JournalRaw
	if int64(len(journalRaw)) < manifest.Journal.Size {
		return errors.New("canonical legacy repair journal is shorter than its sealed prefix")
	}
	prefix := journalRaw[:manifest.Journal.Size]
	if int64(len(prefix)) != manifest.Journal.Size || sha256Hex(prefix) != manifest.Journal.SHA256 {
		return errors.New("canonical legacy repair journal prefix mismatch")
	}
	suffix := bytes.TrimSpace(journalRaw[manifest.Journal.Size:])
	lines := bytes.Split(suffix, []byte{'\n'})
	if len(suffix) == 0 || len(lines) != len(manifest.Candidates) {
		return errors.New("canonical legacy repair journal suffix is not the exact candidate batch")
	}
	var prior time.Time
	for index, line := range lines {
		var record CanonicalLifecycleJournalRecord
		if json.Unmarshal(line, &record) != nil {
			return errors.New("canonical legacy repair journal suffix is invalid JSON")
		}
		candidate := manifest.Candidates[index]
		if record.Family != candidate.Family || record.ObjectID != candidate.ObjectID || record.StateDigest != candidate.StateDigest ||
			record.Reason != canonicalLegacyRepairReason || record.EvidenceBasis != canonicalLegacyRepairEvidence || record.OperationID != "" || record.Phase != "" ||
			record.BoardBeforeSHA256 != "" || record.BoardAfterSHA256 != "" || record.At.IsZero() || record.At.Location() != time.UTC || (!prior.IsZero() && !record.At.After(prior)) {
			return errors.New("canonical legacy repair journal suffix does not match the sealed candidates")
		}
		prior = record.At
	}
	scratch, err := os.MkdirTemp("", "bonfire-legacy-repair-recovery-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(scratch)
	if err := os.Chmod(scratch, 0o700); err != nil {
		return err
	}
	scratchJournal := filepath.Join(scratch, "deleted-objects.jsonl")
	if err := os.WriteFile(scratchJournal, prefix, 0o600); err != nil {
		return err
	}
	paths := canonicalImportPathsForRepair(manifest.DataDir)
	paths.DeletedJournal = scratchJournal
	fingerprint, err := canonicalRepairImportInputFingerprint(paths, filepath.Join(manifest.DataDir, "users.json"))
	if err != nil {
		return err
	}
	if fingerprint != manifest.ImportInputSHA256 {
		return errors.New("non-journal canonical import inputs changed during legacy repair recovery")
	}
	return nil
}

// canonicalLegacyRepairManifestDigest is kept small and independent for
// operator-side verification without loading any source bodies.
func canonicalLegacyRepairManifestDigest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func isExactLowerHex(value string, length int) bool {
	if len(value) != length || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func canonicalLegacyRepairJournalHasExactBatch(raw []byte, records []CanonicalLifecycleJournalRecord) bool {
	for _, record := range records {
		encoded, err := json.Marshal(record)
		if err != nil || !bytes.Contains(raw, encoded) {
			return false
		}
	}
	return true
}
