package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
)

const (
	canonicalBoardRepairManifestSchema       = "bonfire.canonical-board-repair.v2"
	canonicalBoardRepairReceiptSchema        = "bonfire.canonical-repair-receipt.v1"
	canonicalBoardNormalizationInputSchema   = "bonfire.canonical-board-normalization-input.v1"
	canonicalBoardNormalizationReceiptSchema = "bonfire.canonical-board-normalization-receipt.v1"
	canonicalBoardRepairEvidenceSchema       = "bonfire.canonical-board-repair-evidence.v1"
	canonicalBoardRepairEvidenceRecordSchema = "bonfire.canonical-board-repair-evidence-record.v1"
	canonicalBoardCloneRunAuthoritySchema    = "bonfire.canonical-board-repair-clone-run-authority.v1"
	canonicalBoardCloneQualificationSchema   = "bonfire.canonical-board-repair-clone-qualification.v1"
	canonicalBoardRestartObservationSchema   = "bonfire.canonical-board-repair-restart-observation.v1"
	canonicalColdCloneReceiptSchema          = "bonfire.cold-clone-rehearsal-receipt.v1"
	canonicalBoardRepairReason               = "legacy_reconciliation_source_absence_backfill_v1"
	canonicalBoardRepairExactCount           = 7
)

type canonicalBoardRepairFileSeal struct {
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type canonicalBoardRepairEvidenceFile struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type canonicalBoardRepairTarget struct {
	ObjectID            string                           `json:"objectId"`
	StateSHA256         string                           `json:"stateSha256"`
	TargetVersion       int64                            `json:"targetVersion"`
	ObservedAbsenceAt   string                           `json:"observedAbsenceAt"`
	EvidenceBasis       string                           `json:"evidenceBasis"`
	SelectedStateRole   string                           `json:"selectedStateRole"`
	SourceRecord        canonicalBoardRepairEvidenceFile `json:"sourceRecord"`
	ArchiveRecord       canonicalBoardRepairEvidenceFile `json:"archiveRecord"`
	PositiveObservation canonicalBoardRepairEvidenceFile `json:"positiveObservation"`
	AbsenceEvidence     canonicalBoardRepairEvidenceFile `json:"absenceEvidence"`
	PriorPrincipals     []string                         `json:"priorPrincipals"`
}

type canonicalBoardRepairManifest struct {
	Schema                  string                           `json:"schema"`
	ReleaseCommit           string                           `json:"releaseCommit"`
	TenantID                string                           `json:"tenantId"`
	DataDir                 string                           `json:"dataDir"`
	CloneID                 string                           `json:"cloneId"`
	Environment             string                           `json:"environment"`
	EvidenceDir             string                           `json:"evidenceDir"`
	EvidenceDescriptor      canonicalBoardRepairEvidenceFile `json:"evidenceDescriptor"`
	BackupManifest          canonicalBoardRepairEvidenceFile `json:"backupManifest"`
	NormalizationReceipt    canonicalBoardRepairEvidenceFile `json:"normalizationReceipt"`
	QualificationRun        bool                             `json:"qualificationRun"`
	CloneAuthority          canonicalBoardRepairEvidenceFile `json:"cloneAuthority"`
	ReleaseSourceReceipt    canonicalBoardRepairEvidenceFile `json:"releaseSourceReceipt"`
	NormalizedObservation   canonicalBoardRepairEvidenceFile `json:"normalizedObservation"`
	DatabaseURLSHA256       string                           `json:"databaseUrlSha256"`
	DatabaseSHA256          string                           `json:"databaseSha256"`
	Board                   canonicalBoardRepairFileSeal     `json:"board"`
	JournalPrefix           canonicalBoardRepairFileSeal     `json:"journalPrefix"`
	VersionMap              canonicalBoardRepairFileSeal     `json:"versionMap"`
	VersionEntriesSHA256    string                           `json:"versionEntriesSha256"`
	Spool                   canonicalBoardRepairFileSeal     `json:"spool"`
	NormalizedProofSHA256   string                           `json:"normalizedProofSha256"`
	ImportInputSHA256       string                           `json:"importInputSha256"`
	CandidateSetSHA256      string                           `json:"candidateSetSha256"`
	TerminalCandidateSHA256 string                           `json:"terminalCandidateSha256"`
	Candidates              []canonicalBoardRepairTarget     `json:"candidates"`
}

type canonicalBoardNormalizationInput struct {
	Schema                         string                           `json:"schema"`
	ReleaseCommit                  string                           `json:"releaseCommit"`
	TenantID                       string                           `json:"tenantId"`
	DataDir                        string                           `json:"dataDir"`
	Environment                    string                           `json:"environment"`
	CloneID                        string                           `json:"cloneId"`
	QualificationRun               bool                             `json:"qualificationRun"`
	DatabaseURLSHA256              string                           `json:"databaseUrlSha256"`
	EvidenceDir                    string                           `json:"evidenceDir"`
	BackupReceipt                  canonicalBoardRepairEvidenceFile `json:"backupReceipt"`
	FenceReceipt                   canonicalBoardRepairEvidenceFile `json:"fenceReceipt"`
	NormalizationAuthorityMarker   canonicalBoardRepairEvidenceFile `json:"normalizationAuthorityMarker"`
	BeforeObservation              canonicalBoardRepairEvidenceFile `json:"beforeObservation"`
	BeforeFingerprintSHA256        string                           `json:"beforeFingerprintSha256"`
	ExpectedTerminalCandidateCount int                              `json:"expectedTerminalCandidateCount"`
	MaxApplyPasses                 int                              `json:"maxApplyPasses"`
}

type canonicalBoardRepairEvidenceTarget struct {
	ObjectID            string                           `json:"objectId"`
	ObservedAbsenceAt   string                           `json:"observedAbsenceAt"`
	EvidenceBasis       string                           `json:"evidenceBasis"`
	SelectedStateRole   string                           `json:"selectedStateRole"`
	SourceRecord        canonicalBoardRepairEvidenceFile `json:"sourceRecord"`
	ArchiveRecord       canonicalBoardRepairEvidenceFile `json:"archiveRecord"`
	PositiveObservation canonicalBoardRepairEvidenceFile `json:"positiveObservation"`
	AbsenceEvidence     canonicalBoardRepairEvidenceFile `json:"absenceEvidence"`
}

type canonicalBoardRepairEvidenceDescriptor struct {
	Schema                string                               `json:"schema"`
	ReleaseCommit         string                               `json:"releaseCommit"`
	TenantID              string                               `json:"tenantId"`
	DataDir               string                               `json:"dataDir"`
	CloneID               string                               `json:"cloneId"`
	Environment           string                               `json:"environment"`
	BackupManifest        canonicalBoardRepairEvidenceFile     `json:"backupManifest"`
	NormalizationReceipt  canonicalBoardRepairEvidenceFile     `json:"normalizationReceipt"`
	QualificationRun      bool                                 `json:"qualificationRun"`
	CloneAuthority        canonicalBoardRepairEvidenceFile     `json:"cloneAuthority"`
	ReleaseSourceReceipt  canonicalBoardRepairEvidenceFile     `json:"releaseSourceReceipt"`
	NormalizedObservation canonicalBoardRepairEvidenceFile     `json:"normalizedObservation"`
	Targets               []canonicalBoardRepairEvidenceTarget `json:"targets"`
}

type canonicalBoardRepairWatermarks struct {
	EventHighWater        int64  `json:"eventHighWater"`
	CaptureSpoolHighWater uint64 `json:"captureSpoolHighWater"`
}

type canonicalBoardRepairCountDelta struct {
	TenantEvents   int64 `json:"tenantEvents"`
	ImportOutbox   int64 `json:"importOutbox"`
	VersionEntries int   `json:"versionEntries"`
}

type canonicalBoardRepairStateSeal struct {
	TenantEventCount      int64                        `json:"tenantEventCount"`
	EventHighWater        int64                        `json:"eventHighWater"`
	ImportOutboxCount     int64                        `json:"importOutboxCount"`
	VersionEntryCount     int                          `json:"versionEntryCount"`
	VersionEntriesSHA256  string                       `json:"versionEntriesSha256"`
	CaptureSpoolHighWater uint64                       `json:"captureSpoolHighWater"`
	Board                 canonicalBoardRepairFileSeal `json:"board"`
	Journal               canonicalBoardRepairFileSeal `json:"journal"`
	VersionMap            canonicalBoardRepairFileSeal `json:"versionMap"`
	Spool                 canonicalBoardRepairFileSeal `json:"spool"`
	DatabaseSHA256        string                       `json:"databaseSha256"`
	ImportInputSHA256     string                       `json:"importInputSha256"`
	ProofSHA256           string                       `json:"proofSha256"`
	CandidateCount        int                          `json:"candidateCount"`
	CandidateSHA256       string                       `json:"candidateSha256"`
}

type canonicalBoardRepairProof struct {
	Candidates            []CanonicalRepairCandidate
	Diverged              bool
	PrincipalParity       bool
	ProjectionReplayValid bool
	ParitySHA256          string
	EventStoreSHA256      string
	EventHighWater        int64
	EventCount            int64
	SpoolHighWater        uint64
	OutboxCount           int64
	VersionEntryCount     int
	VersionEntriesSHA256  string
	DatabaseSHA256        string
	ImportInputSHA256     string
	Board                 canonicalBoardRepairFileSeal
	Journal               canonicalBoardRepairFileSeal
	VersionMap            canonicalBoardRepairFileSeal
	Spool                 canonicalBoardRepairFileSeal
	PriorPrincipals       map[string][]string
	JournalRaw            []byte
	JournalRecords        []CanonicalLifecycleJournalRecord
}

type canonicalBoardRepairEvidenceRecord struct {
	Schema         string                           `json:"schema"`
	Role           string                           `json:"role"`
	ObjectID       string                           `json:"objectId"`
	Present        bool                             `json:"present"`
	ObservedAt     time.Time                        `json:"observedAt"`
	StateSHA256    string                           `json:"stateSha256,omitempty"`
	SourceArtifact canonicalBoardRepairEvidenceFile `json:"sourceArtifact"`
}

type canonicalBoardCloneRunAuthority struct {
	Schema                     string                           `json:"schema"`
	Status                     string                           `json:"status"`
	ReleaseCommit              string                           `json:"releaseCommit"`
	CloneID                    string                           `json:"cloneId"`
	QualificationRun           bool                             `json:"qualificationRun"`
	BackupManifestSHA256       string                           `json:"backupManifestSha256"`
	ReleaseSourceReceiptSHA256 string                           `json:"releaseSourceReceiptSha256"`
	ColdCloneReceipt           canonicalBoardRepairEvidenceFile `json:"coldCloneReceipt"`
	CreatedAt                  time.Time                        `json:"createdAt"`
	SelfSHA256                 string                           `json:"receiptSha256,omitempty"`
}

type canonicalColdCloneReceipt struct {
	Schema               string    `json:"schema"`
	Status               string    `json:"status"`
	ReleaseCommit        string    `json:"releaseCommit"`
	CloneID              string    `json:"cloneId"`
	QualificationRun     bool      `json:"qualificationRun"`
	BackupManifestSHA256 string    `json:"backupManifestSha256"`
	RestoredVolumeCount  int       `json:"restoredVolumeCount"`
	RestoredVolumes      []string  `json:"restoredVolumes"`
	RawVolumeCompare     bool      `json:"rawVolumeCompare"`
	PostgresRestore      bool      `json:"postgresRestore"`
	PostgresDumpSHA256   string    `json:"postgresDumpSha256"`
	MigrationRowsSHA256  string    `json:"migrationRowsSha256"`
	TableCountsSHA256    string    `json:"tableCountsSha256"`
	CompletedAt          time.Time `json:"completedAt"`
	SelfSHA256           string    `json:"receiptSha256,omitempty"`
}

type canonicalBoardCloneQualificationRun struct {
	CloneID              string                           `json:"cloneId"`
	Manifest             canonicalBoardRepairEvidenceFile `json:"manifest"`
	CloneRunAuthority    canonicalBoardRepairEvidenceFile `json:"cloneRunAuthority"`
	ColdCloneReceipt     canonicalBoardRepairEvidenceFile `json:"coldCloneReceipt"`
	NormalizationReceipt canonicalBoardRepairEvidenceFile `json:"normalizationReceipt"`
	RepairReceipt        canonicalBoardRepairEvidenceFile `json:"repairReceipt"`
	RestartObservation   canonicalBoardRepairEvidenceFile `json:"restartObservation"`
}

type canonicalBoardCloneQualification struct {
	Schema                     string                                `json:"schema"`
	Status                     string                                `json:"status"`
	ReleaseCommit              string                                `json:"releaseCommit"`
	BackupManifestSHA256       string                                `json:"backupManifestSha256"`
	ReleaseSourceReceiptSHA256 string                                `json:"releaseSourceReceiptSha256"`
	Runs                       []canonicalBoardCloneQualificationRun `json:"runs"`
	CompletedAt                time.Time                             `json:"completedAt"`
	SelfSHA256                 string                                `json:"receiptSha256,omitempty"`
}

type canonicalBoardRestartObservation struct {
	Schema                     string                        `json:"schema"`
	Status                     string                        `json:"status"`
	ReleaseCommit              string                        `json:"releaseCommit"`
	CloneID                    string                        `json:"cloneId"`
	Environment                string                        `json:"environment"`
	QualificationRun           bool                          `json:"qualificationRun"`
	NormalizationReceiptSHA256 string                        `json:"normalizationReceiptSha256"`
	RepairReceiptSHA256        string                        `json:"repairReceiptSha256"`
	State                      canonicalBoardRepairStateSeal `json:"state"`
	ZeroCandidates             bool                          `json:"zeroCandidates"`
	PrincipalParity            bool                          `json:"principalParity"`
	ProjectionReplayValid      bool                          `json:"projectionReplayValid"`
	ZeroDeltaReplay            bool                          `json:"zeroDeltaReplay"`
	ObservedAt                 time.Time                     `json:"observedAt"`
	SelfSHA256                 string                        `json:"receiptSha256,omitempty"`
}

type canonicalReleaseSourceReceipt struct {
	Schema                  string            `json:"schema"`
	ReleaseCommit           string            `json:"releaseCommit"`
	ReviewedRef             string            `json:"reviewedRef"`
	GitTreeObject           string            `json:"gitTreeObject"`
	GitTreeDigest           string            `json:"gitTreeDigest"`
	ReviewedInventorySHA256 string            `json:"reviewedInventorySha256"`
	ScopePolicySHA256       string            `json:"scopePolicySha256"`
	SourceArchiveSHA256     string            `json:"sourceArchiveSha256"`
	TransitiveInputsSHA256  string            `json:"transitiveInputsSha256"`
	BuildConfigSHA256       string            `json:"buildConfigSha256"`
	ConfigFiles             map[string]string `json:"configFiles"`
	InputCount              int               `json:"inputCount"`
	SourceDateEpoch         int64             `json:"sourceDateEpoch"`
}

type canonicalBoardRepairObservedTarget struct {
	ObjectID        string   `json:"objectId"`
	StateSHA256     string   `json:"stateSha256"`
	TargetVersion   int64    `json:"targetVersion"`
	PriorPrincipals []string `json:"priorPrincipals"`
}

type canonicalBoardRepairReceipt struct {
	Schema                 string                            `json:"schema"`
	Status                 string                            `json:"status"`
	ReleaseCommit          string                            `json:"releaseCommit"`
	Version                string                            `json:"version"`
	TenantID               string                            `json:"tenantId"`
	CloneID                string                            `json:"cloneId"`
	Environment            string                            `json:"environment"`
	QualificationRun       bool                              `json:"qualificationRun"`
	ManifestSHA256         string                            `json:"candidateManifestSha256"`
	AuthoritySHA256        string                            `json:"authorityMarkerSha256"`
	CandidateSetSHA256     string                            `json:"candidateFingerprintSha256"`
	CandidateCount         int                               `json:"candidateCount"`
	AppliedCount           int                               `json:"appliedCount"`
	FirstAppendObserved    bool                              `json:"firstAppendObserved"`
	Before                 canonicalBoardRepairWatermarks    `json:"before"`
	After                  canonicalBoardRepairWatermarks    `json:"after"`
	BeforeState            canonicalBoardRepairStateSeal     `json:"beforeState"`
	AfterState             canonicalBoardRepairStateSeal     `json:"afterState"`
	Delta                  canonicalBoardRepairCountDelta    `json:"delta"`
	JournalAppendedRecords []CanonicalLifecycleJournalRecord `json:"journalAppendedRecords"`
	BoardSHA256            string                            `json:"boardSha256"`
	JournalBeforeSHA256    string                            `json:"journalBeforeSha256"`
	JournalAfterSHA256     string                            `json:"journalAfterSha256"`
	VersionMapBeforeSHA256 string                            `json:"versionMapBeforeSha256"`
	VersionMapAfterSHA256  string                            `json:"versionMapAfterSha256"`
	DatabaseBeforeSHA256   string                            `json:"databaseBeforeSha256"`
	DatabaseAfterSHA256    string                            `json:"databaseAfterSha256"`
	BeforeCandidateSHA256  string                            `json:"beforeCandidateSha256"`
	AfterCandidateSHA256   string                            `json:"afterCandidateSha256"`
	ZeroCandidates         bool                              `json:"zeroCandidates"`
	PrincipalParity        bool                              `json:"principalParity"`
	ProjectionParity       bool                              `json:"projectionParity"`
	FinalParitySHA256      string                            `json:"finalParitySha256"`
	AfterFingerprintSHA256 string                            `json:"afterFingerprintSha256"`
	IdempotentSecondReplay bool                              `json:"idempotentSecondReplay"`
	CompletedAt            time.Time                         `json:"completedAt"`
	SelfSHA256             string                            `json:"receiptSha256,omitempty"`
}

type canonicalBoardNormalizationReceipt struct {
	Schema                    string                         `json:"schema"`
	Status                    string                         `json:"status"`
	ReleaseCommit             string                         `json:"releaseCommit"`
	Version                   string                         `json:"version"`
	TenantID                  string                         `json:"tenantId"`
	CloneID                   string                         `json:"cloneId"`
	Environment               string                         `json:"environment"`
	QualificationRun          bool                           `json:"qualificationRun"`
	InputSHA256               string                         `json:"normalizationInputSha256"`
	BackupReceiptSHA256       string                         `json:"backupReceiptSha256"`
	FenceReceiptSHA256        string                         `json:"fenceReceiptSha256"`
	BeforeObservationSHA256   string                         `json:"beforeObservationSha256"`
	BeforeFingerprintSHA256   string                         `json:"beforeFingerprintSha256"`
	AfterFingerprintSHA256    string                         `json:"afterFingerprintSha256"`
	BeforeCandidateCount      int                            `json:"beforeCandidateCount"`
	BeforeCandidateSHA256     string                         `json:"beforeCandidateSha256"`
	AfterCandidateCount       int                            `json:"afterCandidateCount"`
	AfterCandidateSHA256      string                         `json:"afterCandidateSha256"`
	ApplyPasses               int                            `json:"applyPasses"`
	LifecycleAppendCount      int                            `json:"lifecycleAppendCount"`
	BeforeState               canonicalBoardRepairStateSeal  `json:"beforeState"`
	AfterState                canonicalBoardRepairStateSeal  `json:"afterState"`
	Delta                     canonicalBoardRepairCountDelta `json:"delta"`
	JournalBefore             canonicalBoardRepairFileSeal   `json:"journalBefore"`
	JournalAfter              canonicalBoardRepairFileSeal   `json:"journalAfter"`
	BoardAfter                canonicalBoardRepairFileSeal   `json:"boardAfter"`
	VersionMapAfter           canonicalBoardRepairFileSeal   `json:"versionMapAfter"`
	VersionEntriesSHA256      string                         `json:"versionEntriesSha256"`
	SpoolAfter                canonicalBoardRepairFileSeal   `json:"spoolAfter"`
	DatabaseAfterSHA256       string                         `json:"databaseAfterSha256"`
	EventHighWater            int64                          `json:"eventHighWater"`
	TenantEventCount          int64                          `json:"tenantEventCount"`
	CaptureSpoolHighWater     uint64                         `json:"captureSpoolHighWater"`
	OutboxCount               int64                          `json:"outboxCount"`
	VersionEntryCount         int                            `json:"versionEntryCount"`
	ExactTerminalSeven        bool                           `json:"exactTerminalSeven"`
	PrincipalParity           bool                           `json:"principalParity"`
	ProjectionReplayValid     bool                           `json:"projectionReplayValid"`
	FullZeroDeltaSecondReplay bool                           `json:"fullZeroDeltaSecondReplay"`
	CompletedAt               time.Time                      `json:"completedAt"`
	SelfSHA256                string                         `json:"receiptSha256,omitempty"`
}

type canonicalBoardRepairEngine interface {
	Observe(context.Context) (canonicalBoardRepairProof, error)
	ApplyOrdinary(context.Context) error
	AppendLifecycle(context.Context, canonicalBoardRepairTarget) error
}

type canonicalBoardRepairRun struct {
	manifest canonicalBoardRepairManifest
	engine   canonicalBoardRepairEngine
	progress func(string, int)
}

type canonicalBoardNormalizationRun struct {
	input    canonicalBoardNormalizationInput
	engine   canonicalBoardRepairEngine
	progress func(string, int)
}

type canonicalBoardRepairObservation struct {
	Schema                string                               `json:"schema"`
	ReleaseCommit         string                               `json:"releaseCommit"`
	TenantID              string                               `json:"tenantId"`
	DataDir               string                               `json:"dataDir"`
	DatabaseURLSHA256     string                               `json:"databaseUrlSha256"`
	DatabaseSHA256        string                               `json:"databaseSha256"`
	ImportInputSHA256     string                               `json:"importInputSha256"`
	Board                 canonicalBoardRepairFileSeal         `json:"board"`
	Journal               canonicalBoardRepairFileSeal         `json:"journal"`
	VersionMap            canonicalBoardRepairFileSeal         `json:"versionMap"`
	VersionEntriesSHA256  string                               `json:"versionEntriesSha256"`
	Spool                 canonicalBoardRepairFileSeal         `json:"spool"`
	CandidateCount        int                                  `json:"candidateCount"`
	CandidateFingerprint  string                               `json:"candidateFingerprintSha256"`
	ProofFingerprint      string                               `json:"proofFingerprintSha256"`
	Candidates            []CanonicalRepairCandidate           `json:"candidates"`
	Targets               []canonicalBoardRepairObservedTarget `json:"targets"`
	PrincipalParity       bool                                 `json:"principalParity"`
	ProjectionReplayValid bool                                 `json:"projectionReplayValid"`
	EventHighWater        int64                                `json:"eventHighWater"`
	TenantEventCount      int64                                `json:"tenantEventCount"`
	OutboxCount           int64                                `json:"outboxCount"`
	VersionEntryCount     int                                  `json:"versionEntryCount"`
	ObservedAt            time.Time                            `json:"observedAt"`
}

func (run canonicalBoardNormalizationRun) execute(ctx context.Context) (canonicalBoardNormalizationReceipt, error) {
	before, err := run.engine.Observe(ctx)
	if err != nil {
		return canonicalBoardNormalizationReceipt{}, fmt.Errorf("observe canonical normalization preconditions: %w", err)
	}
	beforeFingerprint := canonicalRepairProofFingerprint(before)
	if beforeFingerprint != run.input.BeforeFingerprintSHA256 {
		return canonicalBoardNormalizationReceipt{}, errors.New("normalization start state is not the sealed before state; cold restore required")
	}
	beforeCandidateSHA, err := canonicalRepairCandidateDigest(before.Candidates)
	if err != nil {
		return canonicalBoardNormalizationReceipt{}, err
	}
	journalBefore := before.Journal
	var converged canonicalBoardRepairProof
	convergencePasses := 0
	for ; convergencePasses < run.input.MaxApplyPasses; convergencePasses++ {
		if err := run.engine.ApplyOrdinary(ctx); err != nil {
			return canonicalBoardNormalizationReceipt{}, fmt.Errorf("ordinary normalization pass %d: %w", convergencePasses+1, err)
		}
		proof, observeErr := run.engine.Observe(ctx)
		if observeErr != nil {
			return canonicalBoardNormalizationReceipt{}, observeErr
		}
		if proof.Journal != journalBefore {
			return canonicalBoardNormalizationReceipt{}, errors.New("normalization changed lifecycle journal; cold restore required")
		}
		if validateCanonicalNormalizationTerminal(proof) == nil {
			converged = proof
			convergencePasses++
			break
		}
	}
	if err := validateCanonicalNormalizationTerminal(converged); err != nil {
		return canonicalBoardNormalizationReceipt{}, fmt.Errorf("normalization did not converge to exact seven: %w", err)
	}
	if err := validateCanonicalNormalizationTransition(before, converged); err != nil {
		return canonicalBoardNormalizationReceipt{}, fmt.Errorf("normalization mutation boundary: %w", err)
	}
	firstFingerprint := canonicalRepairProofFingerprint(converged)
	if err := run.engine.ApplyOrdinary(ctx); err != nil {
		return canonicalBoardNormalizationReceipt{}, fmt.Errorf("normalization zero-delta second replay: %w", err)
	}
	second, err := run.engine.Observe(ctx)
	if err != nil {
		return canonicalBoardNormalizationReceipt{}, err
	}
	if err := validateCanonicalNormalizationTerminal(second); err != nil || canonicalRepairProofFingerprint(second) != firstFingerprint {
		return canonicalBoardNormalizationReceipt{}, errors.New("normalization second replay was not full zero-delta")
	}
	beforeState, err := canonicalRepairStateFromProof(before)
	if err != nil {
		return canonicalBoardNormalizationReceipt{}, err
	}
	afterState, err := canonicalRepairStateFromProof(second)
	if err != nil {
		return canonicalBoardNormalizationReceipt{}, err
	}
	afterCandidateSHA, _ := canonicalRepairCandidateDigest(second.Candidates)
	return canonicalBoardNormalizationReceipt{
		Status: "complete", BeforeFingerprintSHA256: beforeFingerprint, AfterFingerprintSHA256: firstFingerprint,
		BeforeCandidateCount: len(before.Candidates), BeforeCandidateSHA256: beforeCandidateSHA,
		AfterCandidateCount: len(second.Candidates), AfterCandidateSHA256: afterCandidateSHA,
		ApplyPasses: convergencePasses + 1, LifecycleAppendCount: 0,
		BeforeState: beforeState, AfterState: afterState,
		Delta:         canonicalBoardRepairCountDelta{TenantEvents: 2, ImportOutbox: 2, VersionEntries: 2},
		JournalBefore: journalBefore, JournalAfter: second.Journal,
		BoardAfter: second.Board, VersionMapAfter: second.VersionMap, VersionEntriesSHA256: second.VersionEntriesSHA256,
		SpoolAfter: second.Spool, DatabaseAfterSHA256: second.DatabaseSHA256, EventHighWater: second.EventHighWater, TenantEventCount: second.EventCount,
		CaptureSpoolHighWater: second.SpoolHighWater, OutboxCount: second.OutboxCount, VersionEntryCount: second.VersionEntryCount,
		ExactTerminalSeven: true, PrincipalParity: second.PrincipalParity, ProjectionReplayValid: second.ProjectionReplayValid,
		FullZeroDeltaSecondReplay: true,
	}, nil
}

func (run canonicalBoardRepairRun) execute(ctx context.Context) (canonicalBoardRepairReceipt, error) {
	before, err := run.engine.Observe(ctx)
	if err != nil {
		return canonicalBoardRepairReceipt{}, fmt.Errorf("observe canonical repair preconditions: %w", err)
	}
	if err := validateCanonicalBoardRepairProof(run.manifest, before); err != nil {
		return canonicalBoardRepairReceipt{}, fmt.Errorf("terminal repair precondition: %w", err)
	}
	beforeCandidateSHA, err := canonicalRepairCandidateDigest(before.Candidates)
	if err != nil {
		return canonicalBoardRepairReceipt{}, err
	}
	if run.progress != nil {
		run.progress("preconditions_verified", 0)
	}

	newlyApplied := 0
	for index := range run.manifest.Candidates {
		if err := run.engine.AppendLifecycle(ctx, run.manifest.Candidates[index]); err != nil {
			return canonicalBoardRepairReceipt{}, fmt.Errorf("append lifecycle record %d of %d: %w", index+1, len(run.manifest.Candidates), err)
		}
		newlyApplied++
		if run.progress != nil {
			run.progress("journal_append_committed", index+1)
		}
	}
	if err := run.engine.ApplyOrdinary(ctx); err != nil {
		return canonicalBoardRepairReceipt{}, fmt.Errorf("ordinary canonical replay after repair: %w", err)
	}
	finalProof, err := run.engine.Observe(ctx)
	if err != nil {
		return canonicalBoardRepairReceipt{}, err
	}
	if err := validateCanonicalBoardRepairFinalProof(finalProof); err != nil {
		return canonicalBoardRepairReceipt{}, err
	}
	if err := validateCanonicalRepairTransition(before, finalProof, run.manifest.Candidates); err != nil {
		return canonicalBoardRepairReceipt{}, fmt.Errorf("repair mutation boundary: %w", err)
	}

	firstFinalFingerprint := canonicalRepairProofFingerprint(finalProof)
	if err := run.engine.ApplyOrdinary(ctx); err != nil {
		return canonicalBoardRepairReceipt{}, fmt.Errorf("idempotent second canonical replay: %w", err)
	}
	secondProof, err := run.engine.Observe(ctx)
	if err != nil {
		return canonicalBoardRepairReceipt{}, err
	}
	if err := validateCanonicalBoardRepairFinalProof(secondProof); err != nil {
		return canonicalBoardRepairReceipt{}, fmt.Errorf("second replay parity: %w", err)
	}
	secondFinalFingerprint := canonicalRepairProofFingerprint(secondProof)
	if firstFinalFingerprint != secondFinalFingerprint {
		return canonicalBoardRepairReceipt{}, errors.New("idempotent second replay changed canonical proof")
	}
	beforeState, err := canonicalRepairStateFromProof(before)
	if err != nil {
		return canonicalBoardRepairReceipt{}, err
	}
	afterState, err := canonicalRepairStateFromProof(secondProof)
	if err != nil {
		return canonicalBoardRepairReceipt{}, err
	}

	return canonicalBoardRepairReceipt{
		Status: "complete", CandidateCount: len(run.manifest.Candidates), AppliedCount: len(run.manifest.Candidates),
		FirstAppendObserved:    newlyApplied > 0,
		Before:                 canonicalBoardRepairWatermarks{EventHighWater: before.EventHighWater, CaptureSpoolHighWater: before.SpoolHighWater},
		After:                  canonicalBoardRepairWatermarks{EventHighWater: secondProof.EventHighWater, CaptureSpoolHighWater: secondProof.SpoolHighWater},
		BeforeState:            beforeState,
		AfterState:             afterState,
		Delta:                  canonicalBoardRepairCountDelta{TenantEvents: 7, ImportOutbox: 7, VersionEntries: 7},
		JournalAppendedRecords: append([]CanonicalLifecycleJournalRecord(nil), secondProof.JournalRecords[len(before.JournalRecords):]...),
		BeforeCandidateSHA256:  beforeCandidateSHA, AfterCandidateSHA256: sha256Hex([]byte("[]")),
		ZeroCandidates: true, PrincipalParity: secondProof.PrincipalParity,
		ProjectionParity: secondProof.ProjectionReplayValid && !secondProof.Diverged, FinalParitySHA256: secondProof.ParitySHA256,
		AfterFingerprintSHA256: secondFinalFingerprint, IdempotentSecondReplay: true,
	}, nil
}

func canonicalRepairStateFromProof(proof canonicalBoardRepairProof) (canonicalBoardRepairStateSeal, error) {
	candidateSHA, err := canonicalRepairCandidateDigest(proof.Candidates)
	if err != nil {
		return canonicalBoardRepairStateSeal{}, err
	}
	return canonicalBoardRepairStateSeal{
		TenantEventCount: proof.EventCount, EventHighWater: proof.EventHighWater,
		ImportOutboxCount: proof.OutboxCount, VersionEntryCount: proof.VersionEntryCount,
		VersionEntriesSHA256: proof.VersionEntriesSHA256, CaptureSpoolHighWater: proof.SpoolHighWater,
		Board: proof.Board, Journal: proof.Journal, VersionMap: proof.VersionMap, Spool: proof.Spool,
		DatabaseSHA256: proof.DatabaseSHA256, ImportInputSHA256: proof.ImportInputSHA256,
		ProofSHA256: canonicalRepairProofFingerprint(proof), CandidateCount: len(proof.Candidates), CandidateSHA256: candidateSHA,
	}, nil
}

func validateCanonicalNormalizationTransition(before, after canonicalBoardRepairProof) error {
	if after.EventCount-before.EventCount != 2 || after.OutboxCount-before.OutboxCount != 2 || after.VersionEntryCount-before.VersionEntryCount != 2 {
		return errors.New("ordinary normalization must append exactly two tenant events, import outbox rows, and version entries")
	}
	if before.Board != after.Board || before.Journal != after.Journal || before.Spool != after.Spool || before.SpoolHighWater != after.SpoolHighWater {
		return errors.New("ordinary normalization changed visible board, lifecycle journal, or capture spool")
	}
	if before.ImportInputSHA256 != after.ImportInputSHA256 {
		return errors.New("ordinary normalization changed sealed import inputs")
	}
	return nil
}

func validateCanonicalRepairTransition(before, after canonicalBoardRepairProof, targets []canonicalBoardRepairTarget) error {
	if after.EventCount-before.EventCount != canonicalBoardRepairExactCount || after.OutboxCount-before.OutboxCount != canonicalBoardRepairExactCount || after.VersionEntryCount-before.VersionEntryCount != canonicalBoardRepairExactCount {
		return errors.New("repair must append exactly seven tenant events, import outbox rows, and version entries")
	}
	if before.Board != after.Board || before.Spool != after.Spool || before.SpoolHighWater != after.SpoolHighWater {
		return errors.New("repair changed visible board or capture spool")
	}
	if len(before.JournalRaw) == 0 && before.Journal.Size != 0 {
		return errors.New("repair pre-state omitted lifecycle journal bytes")
	}
	if !bytes.HasPrefix(after.JournalRaw, before.JournalRaw) || int64(len(after.JournalRaw)) != after.Journal.Size {
		return errors.New("repair lifecycle journal is not the exact sealed old prefix")
	}
	if len(after.JournalRecords) != len(before.JournalRecords)+canonicalBoardRepairExactCount {
		return errors.New("repair lifecycle journal did not append exactly seven records")
	}
	for index, target := range targets {
		record := after.JournalRecords[len(before.JournalRecords)+index]
		absenceAt, err := time.Parse(time.RFC3339Nano, target.ObservedAbsenceAt)
		if err != nil {
			return fmt.Errorf("repair target %d absence time: %w", index+1, err)
		}
		if record.Family != "board_card" || record.ObjectID != target.ObjectID || record.StateDigest != target.StateSHA256 ||
			record.Reason != canonicalBoardRepairReason || record.EvidenceBasis != target.EvidenceBasis || record.At.IsZero() || record.At.Location() != time.UTC || record.At.Before(absenceAt) ||
			record.OperationID != "" || record.Phase != "" || record.BoardBeforeSHA256 != "" || record.BoardAfterSHA256 != "" {
			return fmt.Errorf("repair lifecycle journal record %d is not the exact authorized semantic record", index+1)
		}
	}
	return nil
}

func validateCanonicalRepairStateSeal(state canonicalBoardRepairStateSeal) error {
	if state.TenantEventCount < 0 || state.EventHighWater < 0 || state.ImportOutboxCount < 0 || state.VersionEntryCount < 0 ||
		!isHexDigest(state.VersionEntriesSHA256) || !isHexDigest(state.DatabaseSHA256) || !isHexDigest(state.ImportInputSHA256) ||
		!isHexDigest(state.ProofSHA256) || state.CandidateCount < 0 || !isHexDigest(state.CandidateSHA256) {
		return errors.New("invalid canonical repair state seal")
	}
	for _, seal := range []canonicalBoardRepairFileSeal{state.Board, state.Journal, state.VersionMap, state.Spool} {
		if seal.Size < 0 || !isHexDigest(seal.SHA256) {
			return errors.New("invalid canonical repair state file seal")
		}
	}
	return nil
}

func validateCanonicalNormalizationReceiptContract(receipt canonicalBoardNormalizationReceipt) error {
	if err := validateCanonicalRepairStateSeal(receipt.BeforeState); err != nil {
		return err
	}
	if err := validateCanonicalRepairStateSeal(receipt.AfterState); err != nil {
		return err
	}
	if receipt.Delta != (canonicalBoardRepairCountDelta{TenantEvents: 2, ImportOutbox: 2, VersionEntries: 2}) ||
		receipt.AfterState.TenantEventCount-receipt.BeforeState.TenantEventCount != 2 ||
		receipt.AfterState.ImportOutboxCount-receipt.BeforeState.ImportOutboxCount != 2 ||
		receipt.AfterState.VersionEntryCount-receipt.BeforeState.VersionEntryCount != 2 {
		return errors.New("normalization receipt does not prove exact +2/+2/+2 counts")
	}
	if receipt.BeforeState.Board != receipt.AfterState.Board || receipt.BeforeState.Journal != receipt.AfterState.Journal ||
		receipt.BeforeState.Spool != receipt.AfterState.Spool || receipt.BeforeState.CaptureSpoolHighWater != receipt.AfterState.CaptureSpoolHighWater ||
		receipt.BeforeState.ImportInputSHA256 != receipt.AfterState.ImportInputSHA256 {
		return errors.New("normalization receipt changed a protected board/journal/spool/import-input seal")
	}
	if receipt.BeforeState.ProofSHA256 != receipt.BeforeFingerprintSHA256 || receipt.AfterState.ProofSHA256 != receipt.AfterFingerprintSHA256 ||
		receipt.BeforeState.CandidateCount != receipt.BeforeCandidateCount || receipt.BeforeState.CandidateSHA256 != receipt.BeforeCandidateSHA256 ||
		receipt.AfterState.CandidateCount != receipt.AfterCandidateCount || receipt.AfterState.CandidateSHA256 != receipt.AfterCandidateSHA256 ||
		receipt.AfterState.TenantEventCount != receipt.TenantEventCount || receipt.AfterState.EventHighWater != receipt.EventHighWater ||
		receipt.AfterState.ImportOutboxCount != receipt.OutboxCount || receipt.AfterState.VersionEntryCount != receipt.VersionEntryCount ||
		receipt.AfterState.VersionEntriesSHA256 != receipt.VersionEntriesSHA256 || receipt.AfterState.CaptureSpoolHighWater != receipt.CaptureSpoolHighWater ||
		receipt.AfterState.Board != receipt.BoardAfter || receipt.AfterState.Journal != receipt.JournalAfter || receipt.BeforeState.Journal != receipt.JournalBefore ||
		receipt.AfterState.VersionMap != receipt.VersionMapAfter || receipt.AfterState.Spool != receipt.SpoolAfter || receipt.AfterState.DatabaseSHA256 != receipt.DatabaseAfterSHA256 {
		return errors.New("normalization receipt compatibility fields do not match its sealed before/after state")
	}
	return nil
}

func validateCanonicalRepairReceiptContract(receipt canonicalBoardRepairReceipt) error {
	if err := validateCanonicalRepairStateSeal(receipt.BeforeState); err != nil {
		return err
	}
	if err := validateCanonicalRepairStateSeal(receipt.AfterState); err != nil {
		return err
	}
	if receipt.Delta != (canonicalBoardRepairCountDelta{TenantEvents: 7, ImportOutbox: 7, VersionEntries: 7}) ||
		receipt.AfterState.TenantEventCount-receipt.BeforeState.TenantEventCount != 7 ||
		receipt.AfterState.ImportOutboxCount-receipt.BeforeState.ImportOutboxCount != 7 ||
		receipt.AfterState.VersionEntryCount-receipt.BeforeState.VersionEntryCount != 7 {
		return errors.New("repair receipt does not prove exact +7/+7/+7 counts")
	}
	if receipt.BeforeState.Board != receipt.AfterState.Board || receipt.BeforeState.Spool != receipt.AfterState.Spool ||
		receipt.BeforeState.CaptureSpoolHighWater != receipt.AfterState.CaptureSpoolHighWater || receipt.BeforeState.CandidateCount != 7 || receipt.AfterState.CandidateCount != 0 ||
		receipt.AfterState.CandidateSHA256 != sha256Hex([]byte("[]")) {
		return errors.New("repair receipt does not preserve board/spool or prove exact seven-to-zero candidates")
	}
	if receipt.Before.EventHighWater != receipt.BeforeState.EventHighWater || receipt.After.EventHighWater != receipt.AfterState.EventHighWater ||
		receipt.Before.CaptureSpoolHighWater != receipt.BeforeState.CaptureSpoolHighWater || receipt.After.CaptureSpoolHighWater != receipt.AfterState.CaptureSpoolHighWater ||
		receipt.BeforeCandidateSHA256 != receipt.BeforeState.CandidateSHA256 || receipt.AfterCandidateSHA256 != receipt.AfterState.CandidateSHA256 ||
		receipt.AfterFingerprintSHA256 != receipt.AfterState.ProofSHA256 {
		return errors.New("repair receipt compatibility fields do not match its sealed before/after state")
	}
	if receipt.BoardSHA256 != receipt.BeforeState.Board.SHA256 || receipt.BoardSHA256 != receipt.AfterState.Board.SHA256 ||
		receipt.JournalBeforeSHA256 != receipt.BeforeState.Journal.SHA256 || receipt.JournalAfterSHA256 != receipt.AfterState.Journal.SHA256 ||
		receipt.VersionMapBeforeSHA256 != receipt.BeforeState.VersionMap.SHA256 || receipt.VersionMapAfterSHA256 != receipt.AfterState.VersionMap.SHA256 ||
		receipt.DatabaseBeforeSHA256 != receipt.BeforeState.DatabaseSHA256 || receipt.DatabaseAfterSHA256 != receipt.AfterState.DatabaseSHA256 {
		return errors.New("repair receipt compatibility seals do not match before/after state")
	}
	if len(receipt.JournalAppendedRecords) != canonicalBoardRepairExactCount {
		return errors.New("repair receipt does not contain exactly seven lifecycle records")
	}
	for index, record := range receipt.JournalAppendedRecords {
		if record.Family != "board_card" || strings.TrimSpace(record.ObjectID) == "" || !isHexDigest(record.StateDigest) || record.At.IsZero() ||
			record.At.Location() != time.UTC || receipt.CompletedAt.IsZero() || receipt.CompletedAt.Location() != time.UTC || record.At.After(receipt.CompletedAt) || receipt.CompletedAt.Sub(record.At) > 10*time.Minute ||
			record.Reason != canonicalBoardRepairReason || (record.EvidenceBasis != "done_archive_absence" && record.EvidenceBasis != "last_positive_source_current_absence") ||
			record.OperationID != "" || record.Phase != "" || record.BoardBeforeSHA256 != "" || record.BoardAfterSHA256 != "" {
			return fmt.Errorf("repair receipt lifecycle record %d is invalid", index+1)
		}
		if index > 0 && record.At.Before(receipt.JournalAppendedRecords[index-1].At) {
			return errors.New("repair receipt lifecycle records are not time ordered")
		}
	}
	return nil
}

func validateCanonicalNormalizationTerminal(proof canonicalBoardRepairProof) error {
	if !proof.PrincipalParity || !proof.ProjectionReplayValid || len(proof.Candidates) != canonicalBoardRepairExactCount {
		return errors.New("terminal proof is not principal-safe exact-seven")
	}
	for _, candidate := range proof.Candidates {
		if candidate.Family != "board_card" || candidate.Kind != "tombstone_required" || candidate.ConfirmedByJournal || candidate.TargetVersion < 1 || !isHexDigest(candidate.StateDigest) {
			return errors.New("terminal set contains a non-tombstone repair candidate")
		}
	}
	return nil
}

func validateCanonicalBoardRepairProof(manifest canonicalBoardRepairManifest, proof canonicalBoardRepairProof) error {
	if !isHexDigest(manifest.NormalizedProofSHA256) || canonicalRepairProofFingerprint(proof) != manifest.NormalizedProofSHA256 ||
		!isHexDigest(manifest.ImportInputSHA256) || proof.ImportInputSHA256 != manifest.ImportInputSHA256 {
		return errors.New("canonical repair live full proof does not match the normalized manifest authority")
	}
	if !proof.PrincipalParity || !proof.ProjectionReplayValid || !isHexDigest(proof.ParitySHA256) || !isHexDigest(proof.EventStoreSHA256) {
		return errors.New("canonical repair lacks principal/projection parity proof")
	}
	expected := map[string]canonicalBoardRepairTarget{}
	for _, target := range manifest.Candidates {
		expected[target.ObjectID] = target
	}
	seen := map[string]bool{}
	for _, candidate := range proof.Candidates {
		target, authorized := expected[candidate.ObjectID]
		if !authorized || candidate.Family != "board_card" {
			return fmt.Errorf("unauthorized repair candidate %s/%s kind=%s", candidate.Family, candidate.ObjectID, candidate.Kind)
		}
		if candidate.Kind != "tombstone_required" || candidate.ConfirmedByJournal || candidate.StateDigest != target.StateSHA256 || candidate.TargetVersion != target.TargetVersion {
			return fmt.Errorf("candidate %s does not match exact unjournaled board tombstone authority", candidate.ObjectID)
		}
		if seen[candidate.ObjectID] {
			return fmt.Errorf("duplicate repair candidate %s", candidate.ObjectID)
		}
		seen[candidate.ObjectID] = true
	}
	for _, target := range manifest.Candidates {
		if !seen[target.ObjectID] {
			return fmt.Errorf("authorized repair candidate %s is missing", target.ObjectID)
		}
		principals := append([]string(nil), proof.PriorPrincipals[target.ObjectID]...)
		sort.Strings(principals)
		if !stringSlicesEqual(principals, target.PriorPrincipals) {
			return fmt.Errorf("authorized repair candidate %s prior principals mismatch", target.ObjectID)
		}
	}
	return nil
}

func validateCanonicalBoardRepairFinalProof(proof canonicalBoardRepairProof) error {
	if proof.Diverged || len(proof.Candidates) != 0 {
		return fmt.Errorf("canonical repair retained %d candidates", len(proof.Candidates))
	}
	if !proof.PrincipalParity || !proof.ProjectionReplayValid || !isHexDigest(proof.ParitySHA256) || !isHexDigest(proof.EventStoreSHA256) {
		return errors.New("canonical repair final parity is incomplete")
	}
	return nil
}

func canonicalRepairProofFingerprint(proof canonicalBoardRepairProof) string {
	candidateSHA, _ := canonicalRepairCandidateDigest(proof.Candidates)
	raw, _ := canonicalJSON(map[string]any{
		"candidate_count": len(proof.Candidates), "candidate_sha256": candidateSHA, "diverged": proof.Diverged, "principal_parity": proof.PrincipalParity,
		"projection_replay_valid": proof.ProjectionReplayValid, "parity_sha256": proof.ParitySHA256,
		"event_store_sha256": proof.EventStoreSHA256, "event_high_water": proof.EventHighWater, "event_count": proof.EventCount,
		"spool_high_water": proof.SpoolHighWater, "outbox_count": proof.OutboxCount,
		"version_entry_count": proof.VersionEntryCount, "version_entries_sha256": proof.VersionEntriesSHA256,
		"database_sha256": proof.DatabaseSHA256, "import_input_sha256": proof.ImportInputSHA256, "board": proof.Board, "journal": proof.Journal,
		"version_map": proof.VersionMap, "spool": proof.Spool, "prior_principals": proof.PriorPrincipals,
	})
	return sha256Hex(raw)
}

type postgresCanonicalBoardRepairEngine struct {
	manifest         canonicalBoardRepairManifest
	registry         *CanonicalPayloadRegistry
	store            *PostgresCanonicalStore
	versions         *FileCanonicalObjectVersionMap
	spoolPath        string
	usersPath        string
	journalPath      string
	memberPrincipals []string
	paths            CanonicalImportPaths
}

func (engine *postgresCanonicalBoardRepairEngine) buildPlan(ctx context.Context) (CanonicalImportPlan, error) {
	paths := engine.paths
	if paths.Board == "" {
		paths = canonicalImportPathsForRepair(engine.manifest.DataDir)
	}
	return (&CanonicalImporter{
		TenantID: engine.manifest.TenantID, Registry: engine.registry, Versions: engine.versions,
		OrgPrincipals: engine.memberPrincipals, Paths: paths,
	}).Build(ctx)
}

func (engine *postgresCanonicalBoardRepairEngine) buildPlanReadOnly(ctx context.Context) (CanonicalImportPlan, error) {
	paths := engine.paths
	if paths.Board == "" {
		paths = canonicalImportPathsForRepair(engine.manifest.DataDir)
	}
	versionRaw, err := os.ReadFile(engine.versions.path)
	if err != nil {
		return CanonicalImportPlan{}, err
	}
	boardRaw, err := os.ReadFile(paths.Board)
	if err != nil {
		return CanonicalImportPlan{}, err
	}
	journalRaw, err := os.ReadFile(paths.DeletedJournal)
	if err != nil {
		return CanonicalImportPlan{}, err
	}
	scratch, err := os.MkdirTemp("", "bonfire-canonical-repair-plan-")
	if err != nil {
		return CanonicalImportPlan{}, err
	}
	defer os.RemoveAll(scratch)
	if err := os.Chmod(scratch, 0o700); err != nil {
		return CanonicalImportPlan{}, err
	}
	paths.Board = filepath.Join(scratch, "kanban-board.json")
	paths.DeletedJournal = filepath.Join(scratch, "deleted-objects.jsonl")
	versionPath := filepath.Join(scratch, "object-versions.json")
	for path, raw := range map[string][]byte{paths.Board: boardRaw, paths.DeletedJournal: journalRaw, versionPath: versionRaw} {
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			return CanonicalImportPlan{}, err
		}
	}
	versions, err := OpenFileCanonicalObjectVersionMap(versionPath)
	if err != nil {
		return CanonicalImportPlan{}, err
	}
	return (&CanonicalImporter{
		TenantID: engine.manifest.TenantID, Registry: engine.registry, Versions: versions,
		OrgPrincipals: engine.memberPrincipals, Paths: paths,
	}).Build(ctx)
}

func (engine *postgresCanonicalBoardRepairEngine) Observe(ctx context.Context) (canonicalBoardRepairProof, error) {
	paths := engine.paths
	if paths.Board == "" {
		paths = canonicalImportPathsForRepair(engine.manifest.DataDir)
	}
	boardRaw, err := readRegularNoSymlink(paths.Board)
	if err != nil {
		return canonicalBoardRepairProof{}, err
	}
	journalRaw, err := readRegularNoSymlink(paths.DeletedJournal)
	if err != nil {
		return canonicalBoardRepairProof{}, err
	}
	versionRaw, err := readRegularNoSymlink(engine.versions.path)
	if err != nil {
		return canonicalBoardRepairProof{}, err
	}
	spoolRaw, spoolHighWater, err := inspectCanonicalRepairSpoolFromScratch(engine.spoolPath)
	if err != nil {
		return canonicalBoardRepairProof{}, err
	}
	observedPrincipals, err := repairMemberPrincipals(engine.usersPath)
	if err != nil {
		return canonicalBoardRepairProof{}, err
	}
	if !stringSlicesEqual(observedPrincipals, engine.memberPrincipals) {
		return canonicalBoardRepairProof{}, errors.New("users.json principal corpus changed before canonical observation")
	}
	inputBeforeSHA, err := canonicalRepairImportInputFingerprint(paths, engine.usersPath)
	if err != nil {
		return canonicalBoardRepairProof{}, err
	}
	plan, err := engine.buildPlanReadOnly(ctx)
	if err != nil {
		return canonicalBoardRepairProof{}, err
	}
	report, err := ReconcileCanonicalPlanWithOptions(ctx, plan, canonicalLegacyEventView{CanonicalEventStore: engine.store}, CanonicalReconcileOptions{
		ACL: NewPostgresCanonicalParityACL(engine.store, engine.manifest.TenantID), TestedPrincipals: plan.TestedPrincipals,
	})
	if err != nil {
		return canonicalBoardRepairProof{}, err
	}
	parityRaw, err := canonicalJSON(report.Target)
	if err != nil {
		return canonicalBoardRepairProof{}, err
	}
	events, err := (canonicalLegacyEventView{CanonicalEventStore: engine.store}).Events(ctx)
	if err != nil {
		return canonicalBoardRepairProof{}, err
	}
	tenantEvents := events[:0]
	var highWater int64
	for _, event := range events {
		if event.TenantID == engine.manifest.TenantID {
			tenantEvents = append(tenantEvents, event)
		}
	}
	eventCount := int64(len(tenantEvents))
	eventRaw, err := canonicalJSON(tenantEvents)
	if err != nil {
		return canonicalBoardRepairProof{}, err
	}
	if err := engine.store.pool.QueryRow(ctx, "SELECT COALESCE(max(sequence),0) FROM canonical_events WHERE tenant_id=$1", engine.manifest.TenantID).Scan(&highWater); err != nil {
		return canonicalBoardRepairProof{}, err
	}
	var outboxCount int64
	if err := engine.store.pool.QueryRow(ctx, `SELECT count(*) FROM outbox o JOIN canonical_events e ON e.event_id=o.event_id WHERE e.tenant_id=$1 AND e.event_type=$2`, engine.manifest.TenantID, canonicalLegacyImportEventType).Scan(&outboxCount); err != nil {
		return canonicalBoardRepairProof{}, err
	}
	freshVersions, err := OpenFileCanonicalObjectVersionMap(engine.versions.path)
	if err != nil {
		return canonicalBoardRepairProof{}, err
	}
	versionSnapshot, err := freshVersions.Snapshot()
	if err != nil {
		return canonicalBoardRepairProof{}, err
	}
	projection := NewCanonicalProjection()
	for _, event := range tenantEvents {
		if err := projection.Apply(event); err != nil {
			return canonicalBoardRepairProof{}, fmt.Errorf("replay canonical projection: %w", err)
		}
	}
	if _, err := projection.Checksum(); err != nil {
		return canonicalBoardRepairProof{}, err
	}
	databaseSHA, err := canonicalRepairDatabaseFingerprint(ctx, engine.store, engine.manifest.TenantID)
	if err != nil {
		return canonicalBoardRepairProof{}, err
	}
	inputSHA, err := canonicalRepairImportInputFingerprint(paths, engine.usersPath)
	if err != nil {
		return canonicalBoardRepairProof{}, err
	}
	boardAfter, boardErr := readRegularNoSymlink(paths.Board)
	journalAfter, journalErr := readRegularNoSymlink(paths.DeletedJournal)
	versionAfter, versionErr := readRegularNoSymlink(engine.versions.path)
	spoolAfter, spoolHighWaterAfter, spoolErr := inspectCanonicalRepairSpoolFromScratch(engine.spoolPath)
	databaseAfterSHA, databaseErr := canonicalRepairDatabaseFingerprint(ctx, engine.store, engine.manifest.TenantID)
	if boardErr != nil || journalErr != nil || versionErr != nil || spoolErr != nil || databaseErr != nil || inputBeforeSHA != inputSHA ||
		!bytes.Equal(boardRaw, boardAfter) || !bytes.Equal(journalRaw, journalAfter) || !bytes.Equal(versionRaw, versionAfter) || !bytes.Equal(spoolRaw, spoolAfter) ||
		spoolHighWater != spoolHighWaterAfter || databaseSHA != databaseAfterSHA {
		return canonicalBoardRepairProof{}, errors.New("canonical repair observation changed while it was being captured")
	}
	priorPrincipals := map[string][]string{}
	resolver := NewPostgresCanonicalParityACL(engine.store, engine.manifest.TenantID)
	for _, candidate := range report.Candidates {
		if candidate.Family != "board_card" || candidate.Kind != "tombstone_required" {
			continue
		}
		var targetEvent CanonicalEvent
		for _, event := range tenantEvents {
			if event.AggregateType == candidate.Family && event.AggregateID == candidate.ObjectID && event.AggregateVersion == candidate.TargetVersion {
				targetEvent = event
			}
		}
		if targetEvent.EventID == uuid.Nil {
			return canonicalBoardRepairProof{}, fmt.Errorf("repair candidate %s lacks its exact target event", candidate.ObjectID)
		}
		for _, principal := range plan.TestedPrincipals {
			allowed, resolveErr := resolver.CanReadCanonicalObject(ctx, principal, targetEvent)
			if resolveErr != nil {
				return canonicalBoardRepairProof{}, resolveErr
			}
			if allowed {
				priorPrincipals[candidate.ObjectID] = append(priorPrincipals[candidate.ObjectID], principal)
			}
		}
	}
	journalRecords, err := readCanonicalLifecycleJournal(paths.DeletedJournal)
	if err != nil {
		return canonicalBoardRepairProof{}, err
	}
	return canonicalBoardRepairProof{
		Candidates: report.Candidates, Diverged: report.Diverged, PrincipalParity: report.PrincipalParityProven,
		// Pre-repair source/target parity is intentionally allowed to differ only
		// by the separately validated exact candidates. ProjectionReplayValid here
		// proves the complete target event stream replays without a gap/conflict;
		// the final proof additionally requires report.Diverged=false below.
		ProjectionReplayValid: true,
		ParitySHA256:          sha256Hex(parityRaw), EventStoreSHA256: sha256Hex(eventRaw),
		EventHighWater: highWater, EventCount: eventCount, SpoolHighWater: spoolHighWater,
		OutboxCount: outboxCount, VersionEntryCount: len(versionSnapshot.Entries), VersionEntriesSHA256: hex.EncodeToString(versionSnapshot.Checksum[:]), DatabaseSHA256: databaseSHA, ImportInputSHA256: inputSHA,
		Board:           canonicalBoardRepairFileSeal{Size: int64(len(boardRaw)), SHA256: sha256Hex(boardRaw)},
		Journal:         canonicalBoardRepairFileSeal{Size: int64(len(journalRaw)), SHA256: sha256Hex(journalRaw)},
		VersionMap:      canonicalBoardRepairFileSeal{Size: int64(len(versionRaw)), SHA256: sha256Hex(versionRaw)},
		Spool:           canonicalBoardRepairFileSeal{Size: int64(len(spoolRaw)), SHA256: sha256Hex(spoolRaw)},
		PriorPrincipals: priorPrincipals, JournalRaw: append([]byte(nil), journalRaw...), JournalRecords: journalRecords,
	}, nil
}

func (engine *postgresCanonicalBoardRepairEngine) ApplyOrdinary(ctx context.Context) error {
	if err := engine.store.ApplyMigrations(ctx); err != nil {
		return err
	}
	plan, err := engine.buildPlan(ctx)
	if err != nil {
		return err
	}
	if err := plan.Apply(ctx, engine.store); err != nil {
		return err
	}
	return engine.store.SyncImportGrants(ctx, plan)
}

func (engine *postgresCanonicalBoardRepairEngine) AppendLifecycle(_ context.Context, target canonicalBoardRepairTarget) error {
	return ensureCanonicalLifecycleJournal(engine.journalPath, CanonicalLifecycleJournalRecord{
		Family: "board_card", ObjectID: target.ObjectID, StateDigest: target.StateSHA256,
		At: time.Now().UTC(), Reason: canonicalBoardRepairReason, EvidenceBasis: target.EvidenceBasis,
	})
}

func inspectCanonicalRepairSpoolFromScratch(path string) ([]byte, uint64, error) {
	raw, err := readRegularNoSymlink(path)
	if err != nil {
		return nil, 0, err
	}
	scratch, err := os.MkdirTemp("", "bonfire-canonical-spool-inspect-")
	if err != nil {
		return nil, 0, err
	}
	defer os.RemoveAll(scratch)
	if err := os.Chmod(scratch, 0o700); err != nil {
		return nil, 0, err
	}
	scratchPath := filepath.Join(scratch, "mutation-spool.bcs")
	if err := os.WriteFile(scratchPath, raw, 0o600); err != nil {
		return nil, 0, err
	}
	file, err := os.Open(scratchPath)
	if err != nil {
		return nil, 0, err
	}
	_, _, truncated, inspectErr := readCanonicalSpoolFrames(file)
	closeErr := file.Close()
	if inspectErr != nil {
		return nil, 0, inspectErr
	}
	if closeErr != nil {
		return nil, 0, closeErr
	}
	if truncated != 0 {
		return nil, 0, errors.New("canonical spool has an incomplete frame; live file left untouched")
	}
	spool, err := OpenCanonicalCaptureSpool(scratchPath, CanonicalModeRequired)
	if err != nil {
		return nil, 0, err
	}
	spool.mu.Lock()
	defer spool.mu.Unlock()
	if spool.next == 0 {
		return raw, 0, nil
	}
	return raw, spool.next - 1, nil
}

func canonicalImportPathsForRepair(dataDir string) CanonicalImportPaths {
	return CanonicalImportPaths{
		MeetingMemory: filepath.Join(dataDir, "meeting-memory.jsonl"), Board: filepath.Join(dataDir, "kanban-board.json"),
		Rooms: filepath.Join(dataDir, "rooms.json"), Meetings: filepath.Join(dataDir, "meetings.json"),
		Notifications: filepath.Join(dataDir, "notifications.json"), ShareLinks: filepath.Join(dataDir, "share-links.json"),
		FileFolders: filepath.Join(dataDir, "file-folders.json"),
		QueueDirs:   []string{codexRunnerQueuePath(), renderRunnerQueuePath()},
		ArchivesDir: filepath.Join(dataDir, "archives"), BlobsDir: filepath.Join(dataDir, "blobs"),
		DeletedJournal: filepath.Join(dataDir, "deleted-objects.jsonl"), EvictedJournal: filepath.Join(dataDir, "evicted-objects.jsonl"),
	}
}

func canonicalRepairImportInputFingerprint(paths CanonicalImportPaths, usersPath string) (string, error) {
	type entry struct {
		Role    string `json:"role"`
		Path    string `json:"path"`
		Missing bool   `json:"missing"`
		Size    int64  `json:"size"`
		SHA256  string `json:"sha256"`
	}
	var result []entry
	inputs := []struct {
		role string
		path string
		dir  bool
	}{
		{"meetingMemory", paths.MeetingMemory, false}, {"board", paths.Board, false}, {"rooms", paths.Rooms, false},
		{"meetings", paths.Meetings, false}, {"notifications", paths.Notifications, false}, {"shareLinks", paths.ShareLinks, false},
		{"fileFolders", paths.FileFolders, false}, {"archives", paths.ArchivesDir, true}, {"blobs", paths.BlobsDir, true},
		{"deletedJournal", paths.DeletedJournal, false}, {"evictedJournal", paths.EvictedJournal, false}, {"users", usersPath, false},
	}
	for index, queue := range paths.QueueDirs {
		inputs = append(inputs, struct {
			role string
			path string
			dir  bool
		}{fmt.Sprintf("queue-%d", index), queue, true})
	}
	for _, input := range inputs {
		if strings.TrimSpace(input.path) == "" {
			result = append(result, entry{Role: input.role, Missing: true})
			continue
		}
		if err := rejectSymlinkPathComponents(input.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		info, err := os.Lstat(input.path)
		if errors.Is(err, os.ErrNotExist) {
			if err := rejectSymlinkPathComponents(filepath.Dir(input.path)); err != nil {
				return "", err
			}
			result = append(result, entry{Role: input.role, Missing: true})
			continue
		}
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("canonical import input %s is a symlink", input.role)
		}
		if !input.dir {
			raw, err := readRegularNoSymlink(input.path)
			if err != nil {
				return "", err
			}
			result = append(result, entry{Role: input.role, Path: ".", Size: int64(len(raw)), SHA256: sha256Hex(raw)})
			continue
		}
		if !info.IsDir() {
			return "", fmt.Errorf("canonical import input %s is not a directory", input.role)
		}
		err = filepath.WalkDir(input.path, func(path string, item os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if path == input.path {
				return nil
			}
			if item.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("canonical import input %s contains symlink %s", input.role, path)
			}
			if item.IsDir() {
				return nil
			}
			if !item.Type().IsRegular() {
				return fmt.Errorf("canonical import input %s contains non-regular file %s", input.role, path)
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			relative, err := filepath.Rel(input.path, path)
			if err != nil {
				return err
			}
			result = append(result, entry{Role: input.role, Path: filepath.ToSlash(relative), Size: int64(len(raw)), SHA256: sha256Hex(raw)})
			return nil
		})
		if err != nil {
			return "", err
		}
	}
	raw, err := canonicalJSON(result)
	return sha256Hex(raw), err
}

func openCanonicalRepairEngine(ctx context.Context, dataDir, tenantID string) (*postgresCanonicalBoardRepairEngine, *PostgresCanonicalStore, error) {
	databaseURL := strings.TrimSpace(os.Getenv("BONFIRE_CANONICAL_DATABASE_URL"))
	if databaseURL == "" {
		return nil, nil, errors.New("BONFIRE_CANONICAL_DATABASE_URL is required")
	}
	principals, err := repairMemberPrincipals(filepath.Join(dataDir, "users.json"))
	if err != nil {
		return nil, nil, err
	}
	registry, err := NewCanonicalImportPayloadRegistry()
	if err != nil {
		return nil, nil, err
	}
	store, err := OpenPostgresCanonicalStore(ctx, databaseURL, registry)
	if err != nil {
		return nil, nil, err
	}
	versionPath := filepath.Join(dataDir, "canonical", "object-versions.json")
	versions, err := OpenFileCanonicalObjectVersionMap(versionPath)
	if err != nil {
		store.Close()
		return nil, nil, err
	}
	manifest := canonicalBoardRepairManifest{TenantID: tenantID, DataDir: dataDir}
	return &postgresCanonicalBoardRepairEngine{
		manifest: manifest, registry: registry, store: store, versions: versions,
		spoolPath: filepath.Join(dataDir, "canonical", "mutation-spool.bcs"), usersPath: filepath.Join(dataDir, "users.json"),
		journalPath: filepath.Join(dataDir, "deleted-objects.jsonl"), memberPrincipals: principals,
	}, store, nil
}

func validateCanonicalRepairRuntimeInputPaths() error {
	for _, queuePath := range []string{codexRunnerQueuePath(), renderRunnerQueuePath()} {
		if !filepath.IsAbs(queuePath) {
			return errors.New("canonical repair queue paths must be absolute")
		}
		if err := validatePrivateDirectory(queuePath); err != nil {
			return fmt.Errorf("canonical repair queue path %s: %w", queuePath, err)
		}
	}
	return nil
}

func runCanonicalBoardNormalizationCLI(ctx context.Context, inputPath, inputSHA, receiptPath string) error {
	if os.Geteuid() != 0 {
		return errors.New("canonical board normalization must run as root")
	}
	inputPath, receiptPath = filepath.Clean(strings.TrimSpace(inputPath)), filepath.Clean(strings.TrimSpace(receiptPath))
	if !filepath.IsAbs(inputPath) || !filepath.IsAbs(receiptPath) || !isHexDigest(inputSHA) {
		return errors.New("normalization input/receipt paths must be absolute and input sha256 exact")
	}
	inputRaw, err := readRootOnlyRegularFile(inputPath)
	if err != nil || sha256Hex(inputRaw) != strings.ToLower(inputSHA) {
		return errors.New("normalization input seal mismatch")
	}
	var input canonicalBoardNormalizationInput
	if err := decodeCanonicalRepairJSON(inputRaw, &input); err != nil {
		return fmt.Errorf("decode normalization input: %w", err)
	}
	if err := validateCanonicalBoardNormalizationInput(input, inputPath, receiptPath); err != nil {
		return err
	}
	if err := validateCanonicalRepairRelease(input.ReleaseCommit); err != nil {
		return err
	}
	if err := validateCanonicalRepairRuntimeInputPaths(); err != nil {
		return err
	}
	if strings.TrimSpace(os.Getenv("BONFIRE_CANONICAL_DATABASE_URL")) == "" || sha256Hex([]byte(strings.TrimSpace(os.Getenv("BONFIRE_CANONICAL_DATABASE_URL")))) != input.DatabaseURLSHA256 {
		return errors.New("normalization database URL binding mismatch")
	}
	observationRaw, err := validateCanonicalRepairEvidenceFiles(input.EvidenceDir, []canonicalBoardRepairEvidenceFile{input.BackupReceipt, input.FenceReceipt, input.NormalizationAuthorityMarker, input.BeforeObservation})
	if err != nil {
		return err
	}
	beforeObservationBytes := observationRaw[input.BeforeObservation.Path]
	var beforeObservation canonicalBoardRepairObservation
	if err := decodeCanonicalRepairJSON(beforeObservationBytes, &beforeObservation); err != nil {
		return fmt.Errorf("decode normalization before observation: %w", err)
	}
	if beforeObservation.Schema != "bonfire.canonical-board-repair-observation.v1" || beforeObservation.ReleaseCommit != input.ReleaseCommit ||
		beforeObservation.TenantID != input.TenantID || filepath.Clean(beforeObservation.DataDir) != filepath.Clean(input.DataDir) ||
		beforeObservation.DatabaseURLSHA256 != input.DatabaseURLSHA256 || !isHexDigest(beforeObservation.ImportInputSHA256) ||
		beforeObservation.ProofFingerprint != input.BeforeFingerprintSHA256 {
		return errors.New("normalization before observation binding mismatch")
	}
	authorityWant := canonicalNormalizationAuthorityText(input)
	if string(observationRaw[input.NormalizationAuthorityMarker.Path]) != authorityWant {
		return errors.New("normalization authority marker is not exact")
	}
	engine, store, err := openCanonicalRepairEngine(ctx, input.DataDir, input.TenantID)
	if err != nil {
		return err
	}
	defer store.Close()
	if existing, found, err := readCanonicalBoardNormalizationReceipt(receiptPath); err != nil {
		return err
	} else if found {
		proof, observeErr := engine.Observe(ctx)
		if observeErr != nil {
			return observeErr
		}
		return validateExistingCanonicalBoardNormalizationReceipt(existing, input, strings.ToLower(inputSHA), canonicalRepairProofFingerprint(proof))
	}
	proof, err := engine.Observe(ctx)
	if err != nil {
		return err
	}
	if canonicalRepairProofFingerprint(proof) != input.BeforeFingerprintSHA256 {
		return errors.New("normalization input no longer matches live state; cold restore required")
	}
	receipt, err := (canonicalBoardNormalizationRun{input: input, engine: engine, progress: writeCanonicalRepairProgress}).execute(ctx)
	if err != nil {
		return fmt.Errorf("normalization failed; cold restore required: %w", err)
	}
	receipt.Schema, receipt.ReleaseCommit, receipt.Version, receipt.TenantID = canonicalBoardNormalizationReceiptSchema, input.ReleaseCommit, input.ReleaseCommit, input.TenantID
	receipt.CloneID, receipt.Environment, receipt.QualificationRun = input.CloneID, input.Environment, input.QualificationRun
	receipt.InputSHA256, receipt.BackupReceiptSHA256, receipt.FenceReceiptSHA256 = strings.ToLower(inputSHA), input.BackupReceipt.SHA256, input.FenceReceipt.SHA256
	receipt.BeforeObservationSHA256, receipt.CompletedAt = input.BeforeObservation.SHA256, time.Now().UTC()
	if err := validateCanonicalNormalizationReceiptContract(receipt); err != nil {
		return fmt.Errorf("normalization completed without an exact receipt contract; cold restore required: %w", err)
	}
	if err := writeCanonicalBoardNormalizationReceipt(receiptPath, &receipt); err != nil {
		return fmt.Errorf("normalization completed but receipt seal failed; cold restore required: %w", err)
	}
	return nil
}

func canonicalNormalizationAuthorityText(input canonicalBoardNormalizationInput) string {
	if input.Environment == "isolated_cold_clone" && input.QualificationRun {
		return "AUTHORIZE CANONICAL BOARD NORMALIZATION " + input.CloneID + " " + input.BeforeObservation.SHA256 + " " + input.BackupReceipt.SHA256 + "\n"
	}
	return "AUTHORIZE CANONICAL BOARD NORMALIZATION " + input.BeforeObservation.SHA256 + " " + input.BackupReceipt.SHA256 + "\n"
}

func validateCanonicalBoardNormalizationInput(input canonicalBoardNormalizationInput, inputPath, receiptPath string) error {
	if input.Schema != canonicalBoardNormalizationInputSchema || !releaseCommitPattern.MatchString(input.ReleaseCommit) || strings.TrimSpace(input.TenantID) == "" ||
		!filepath.IsAbs(input.DataDir) || filepath.Clean(input.DataDir) != filepath.Clean(filepath.Dir(meetingMemoryPath())) ||
		(input.Environment != "isolated_cold_clone" && input.Environment != "production_protected_maintenance") || strings.TrimSpace(input.CloneID) == "" ||
		(input.Environment == "isolated_cold_clone") != input.QualificationRun || !filepath.IsAbs(input.EvidenceDir) ||
		!isHexDigest(input.DatabaseURLSHA256) || !isHexDigest(input.BeforeFingerprintSHA256) || input.ExpectedTerminalCandidateCount != canonicalBoardRepairExactCount ||
		input.MaxApplyPasses < 1 || input.MaxApplyPasses > 8 {
		return errors.New("invalid canonical normalization input")
	}
	if !pathWithin(inputPath, input.EvidenceDir) || !pathWithin(receiptPath, input.EvidenceDir) || pathWithin(receiptPath, input.DataDir) {
		return errors.New("normalization input and receipt must remain in the evidence directory outside application data")
	}
	if err := validateRootOwnedDirectory(input.DataDir); err != nil {
		return err
	}
	if err := validateRootOwnedDirectory(input.EvidenceDir); err != nil {
		return err
	}
	for _, reference := range []canonicalBoardRepairEvidenceFile{input.BackupReceipt, input.FenceReceipt, input.NormalizationAuthorityMarker, input.BeforeObservation} {
		if err := validateCanonicalRepairEvidenceReference(reference); err != nil {
			return err
		}
	}
	return nil
}

func validateExistingCanonicalBoardNormalizationReceipt(receipt canonicalBoardNormalizationReceipt, input canonicalBoardNormalizationInput, inputSHA, liveFingerprint string) error {
	if err := validateCanonicalNormalizationReceiptContract(receipt); err != nil {
		return err
	}
	if receipt.Schema != canonicalBoardNormalizationReceiptSchema || receipt.Status != "complete" || receipt.ReleaseCommit != input.ReleaseCommit ||
		receipt.Version != input.ReleaseCommit || receipt.TenantID != input.TenantID || receipt.CloneID != input.CloneID || receipt.Environment != input.Environment ||
		receipt.QualificationRun != input.QualificationRun || receipt.InputSHA256 != inputSHA ||
		receipt.BackupReceiptSHA256 != input.BackupReceipt.SHA256 || receipt.FenceReceiptSHA256 != input.FenceReceipt.SHA256 ||
		receipt.BeforeObservationSHA256 != input.BeforeObservation.SHA256 || receipt.BeforeFingerprintSHA256 != input.BeforeFingerprintSHA256 ||
		receipt.AfterFingerprintSHA256 != liveFingerprint || receipt.AfterCandidateCount != canonicalBoardRepairExactCount ||
		receipt.LifecycleAppendCount != 0 || receipt.JournalBefore != receipt.JournalAfter || !receipt.ExactTerminalSeven || !receipt.PrincipalParity ||
		!receipt.ProjectionReplayValid || !receipt.FullZeroDeltaSecondReplay || receipt.CompletedAt.IsZero() {
		return errors.New("existing normalization receipt is incomplete or does not match live normalized state")
	}
	return nil
}

func generateCanonicalBoardRepairManifestCLI(ctx context.Context, evidenceDir, descriptorPath, normalizationReceiptPath, observationPath, manifestPath string) error {
	if os.Geteuid() != 0 {
		return errors.New("canonical repair manifest generation must run as root")
	}
	evidenceDir = filepath.Clean(strings.TrimSpace(evidenceDir))
	paths := []*string{&descriptorPath, &normalizationReceiptPath, &observationPath, &manifestPath}
	for _, value := range paths {
		*value = filepath.Clean(strings.TrimSpace(*value))
		if !filepath.IsAbs(*value) || !pathWithin(*value, evidenceDir) {
			return errors.New("manifest-generation paths must be absolute children of the evidence directory")
		}
	}
	if err := validateRootOwnedDirectory(evidenceDir); err != nil {
		return err
	}
	descriptorRaw, err := readRootOnlyRegularFile(descriptorPath)
	if err != nil {
		return fmt.Errorf("classified evidence descriptor: %w", err)
	}
	var descriptor canonicalBoardRepairEvidenceDescriptor
	if err := decodeCanonicalRepairJSON(descriptorRaw, &descriptor); err != nil {
		return fmt.Errorf("classified evidence descriptor: %w", err)
	}
	if err := validateCanonicalBoardRepairEvidenceDescriptor(descriptor); err != nil {
		return err
	}
	references := []canonicalBoardRepairEvidenceFile{descriptor.BackupManifest, descriptor.NormalizationReceipt, descriptor.CloneAuthority, descriptor.ReleaseSourceReceipt, descriptor.NormalizedObservation}
	for _, target := range descriptor.Targets {
		references = append(references, target.SourceRecord, target.ArchiveRecord, target.PositiveObservation, target.AbsenceEvidence)
	}
	evidenceRaw, err := validateCanonicalRepairEvidenceFiles(evidenceDir, references)
	if err != nil {
		return err
	}
	if filepath.Join(evidenceDir, descriptor.NormalizationReceipt.Path) != normalizationReceiptPath || filepath.Join(evidenceDir, descriptor.NormalizedObservation.Path) != observationPath {
		return errors.New("passed normalization receipt/observation do not match descriptor references")
	}
	normalizationReceipt, err := decodeCanonicalBoardNormalizationReceipt(evidenceRaw[descriptor.NormalizationReceipt.Path])
	if err != nil {
		return err
	}
	var observation canonicalBoardRepairObservation
	if err := decodeCanonicalRepairJSON(evidenceRaw[descriptor.NormalizedObservation.Path], &observation); err != nil {
		return err
	}
	if normalizationReceipt.Schema != canonicalBoardNormalizationReceiptSchema || normalizationReceipt.Status != "complete" ||
		normalizationReceipt.ReleaseCommit != descriptor.ReleaseCommit || normalizationReceipt.TenantID != descriptor.TenantID ||
		normalizationReceipt.CloneID != descriptor.CloneID || normalizationReceipt.Environment != descriptor.Environment || normalizationReceipt.QualificationRun != descriptor.QualificationRun ||
		!normalizationReceipt.ExactTerminalSeven || normalizationReceipt.LifecycleAppendCount != 0 || !normalizationReceipt.FullZeroDeltaSecondReplay {
		return errors.New("normalization receipt does not authorize manifest generation")
	}
	if err := validateCanonicalNormalizationReceiptContract(normalizationReceipt); err != nil {
		return err
	}
	if observation.Schema != "bonfire.canonical-board-repair-observation.v1" || observation.ReleaseCommit != descriptor.ReleaseCommit ||
		observation.TenantID != descriptor.TenantID || filepath.Clean(observation.DataDir) != filepath.Clean(descriptor.DataDir) ||
		observation.CandidateCount != canonicalBoardRepairExactCount || observation.ProofFingerprint != normalizationReceipt.AfterFingerprintSHA256 ||
		observation.TenantEventCount != normalizationReceipt.AfterState.TenantEventCount || observation.OutboxCount != normalizationReceipt.AfterState.ImportOutboxCount ||
		observation.VersionEntryCount != normalizationReceipt.AfterState.VersionEntryCount {
		return errors.New("normalized observation does not match normalization receipt")
	}
	if err := validateCanonicalRepairRelease(descriptor.ReleaseCommit); err != nil {
		return err
	}
	if err := validateCanonicalRepairRuntimeInputPaths(); err != nil {
		return err
	}
	engine, store, err := openCanonicalRepairEngine(ctx, descriptor.DataDir, descriptor.TenantID)
	if err != nil {
		return err
	}
	defer store.Close()
	proof, err := engine.Observe(ctx)
	if err != nil {
		return err
	}
	if canonicalRepairProofFingerprint(proof) != observation.ProofFingerprint {
		return errors.New("normalized observation is no longer current")
	}
	if err := validateCanonicalNormalizationTerminal(proof); err != nil {
		return err
	}
	descriptorRel, err := filepath.Rel(evidenceDir, descriptorPath)
	if err != nil {
		return err
	}
	manifest, err := buildCanonicalBoardRepairManifest(evidenceDir, descriptor, canonicalBoardRepairEvidenceFile{Path: descriptorRel, Size: int64(len(descriptorRaw)), SHA256: sha256Hex(descriptorRaw)}, observation, proof)
	if err != nil {
		return err
	}
	outputRaw, generatedManifestSHA, err := marshalCanonicalBoardRepairManifestFile(manifest)
	if err != nil {
		return err
	}
	if err := validateCanonicalBoardRepairInputs(manifest, generatedManifestSHA, manifestPath); err != nil {
		return err
	}
	if err := validateCanonicalRepairEvidence(manifest); err != nil {
		return err
	}
	if _, err := os.Lstat(manifestPath); err == nil {
		existing, readErr := readRootOnlyRegularFile(manifestPath)
		if readErr != nil || !bytes.Equal(existing, outputRaw) {
			return errors.New("candidate manifest output already exists with different content")
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return writeFileAtomicallyDurable(manifestPath, outputRaw, 0o600)
}

func marshalCanonicalBoardRepairManifestFile(manifest canonicalBoardRepairManifest) ([]byte, string, error) {
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, "", err
	}
	raw = append(raw, '\n')
	return raw, sha256Hex(raw), nil
}

func buildCanonicalBoardRepairManifest(evidenceDir string, descriptor canonicalBoardRepairEvidenceDescriptor, descriptorFile canonicalBoardRepairEvidenceFile, observation canonicalBoardRepairObservation, proof canonicalBoardRepairProof) (canonicalBoardRepairManifest, error) {
	if err := validateCanonicalBoardRepairEvidenceDescriptor(descriptor); err != nil {
		return canonicalBoardRepairManifest{}, err
	}
	if err := validateCanonicalNormalizationTerminal(proof); err != nil {
		return canonicalBoardRepairManifest{}, err
	}
	evidenceByID := map[string]canonicalBoardRepairEvidenceTarget{}
	for _, target := range descriptor.Targets {
		evidenceByID[target.ObjectID] = target
	}
	candidates := append([]CanonicalRepairCandidate(nil), proof.Candidates...)
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].ObjectID < candidates[j].ObjectID })
	targets := make([]canonicalBoardRepairTarget, 0, canonicalBoardRepairExactCount)
	for _, candidate := range candidates {
		evidence, ok := evidenceByID[candidate.ObjectID]
		principals := append([]string(nil), proof.PriorPrincipals[candidate.ObjectID]...)
		sort.Strings(principals)
		if !ok || len(principals) == 0 {
			return canonicalBoardRepairManifest{}, fmt.Errorf("classified evidence/prior principals missing for target %s", candidate.ObjectID)
		}
		targets = append(targets, canonicalBoardRepairTarget{
			ObjectID: candidate.ObjectID, StateSHA256: candidate.StateDigest, TargetVersion: candidate.TargetVersion,
			ObservedAbsenceAt: evidence.ObservedAbsenceAt, EvidenceBasis: evidence.EvidenceBasis, SelectedStateRole: evidence.SelectedStateRole,
			SourceRecord: evidence.SourceRecord, ArchiveRecord: evidence.ArchiveRecord,
			PositiveObservation: evidence.PositiveObservation, AbsenceEvidence: evidence.AbsenceEvidence,
			PriorPrincipals: principals,
		})
	}
	targetRaw, _ := canonicalJSON(targets)
	terminalSHA, err := canonicalRepairCandidateDigest(candidates)
	if err != nil {
		return canonicalBoardRepairManifest{}, err
	}
	return canonicalBoardRepairManifest{
		Schema: canonicalBoardRepairManifestSchema, ReleaseCommit: descriptor.ReleaseCommit, TenantID: descriptor.TenantID,
		DataDir: descriptor.DataDir, CloneID: descriptor.CloneID, Environment: descriptor.Environment, EvidenceDir: evidenceDir,
		EvidenceDescriptor: descriptorFile, BackupManifest: descriptor.BackupManifest, NormalizationReceipt: descriptor.NormalizationReceipt,
		QualificationRun: descriptor.QualificationRun, CloneAuthority: descriptor.CloneAuthority,
		ReleaseSourceReceipt: descriptor.ReleaseSourceReceipt, NormalizedObservation: descriptor.NormalizedObservation,
		DatabaseURLSHA256: observation.DatabaseURLSHA256, DatabaseSHA256: proof.DatabaseSHA256,
		Board: proof.Board, JournalPrefix: proof.Journal, VersionMap: proof.VersionMap, VersionEntriesSHA256: proof.VersionEntriesSHA256, Spool: proof.Spool,
		NormalizedProofSHA256: canonicalRepairProofFingerprint(proof), ImportInputSHA256: proof.ImportInputSHA256,
		CandidateSetSHA256: sha256Hex(targetRaw), TerminalCandidateSHA256: terminalSHA, Candidates: targets,
	}, nil
}

func validateCanonicalBoardRepairEvidenceDescriptor(descriptor canonicalBoardRepairEvidenceDescriptor) error {
	if descriptor.Schema != canonicalBoardRepairEvidenceSchema || !releaseCommitPattern.MatchString(descriptor.ReleaseCommit) || strings.TrimSpace(descriptor.TenantID) == "" ||
		!filepath.IsAbs(descriptor.DataDir) || filepath.Clean(descriptor.DataDir) != filepath.Clean(filepath.Dir(meetingMemoryPath())) || strings.TrimSpace(descriptor.CloneID) == "" ||
		(descriptor.Environment != "isolated_cold_clone" && descriptor.Environment != "production_protected_maintenance") ||
		(descriptor.Environment == "isolated_cold_clone") != descriptor.QualificationRun || len(descriptor.Targets) != canonicalBoardRepairExactCount {
		return errors.New("invalid classified evidence descriptor")
	}
	for _, reference := range []canonicalBoardRepairEvidenceFile{descriptor.BackupManifest, descriptor.NormalizationReceipt, descriptor.CloneAuthority, descriptor.ReleaseSourceReceipt, descriptor.NormalizedObservation} {
		if err := validateCanonicalRepairEvidenceReference(reference); err != nil {
			return err
		}
	}
	targets := append([]canonicalBoardRepairEvidenceTarget(nil), descriptor.Targets...)
	sort.Slice(targets, func(i, j int) bool { return targets[i].ObjectID < targets[j].ObjectID })
	for index, target := range targets {
		if target.ObjectID == "" || (target.EvidenceBasis != "done_archive_absence" && target.EvidenceBasis != "last_positive_source_current_absence") ||
			!canonicalRepairSelectedStateRole(target.SelectedStateRole) {
			return errors.New("invalid classified target evidence")
		}
		observed, err := time.Parse(time.RFC3339Nano, target.ObservedAbsenceAt)
		if err != nil || observed.Location() != time.UTC {
			return errors.New("classified target absence time must be UTC RFC3339Nano")
		}
		for _, reference := range []canonicalBoardRepairEvidenceFile{target.SourceRecord, target.ArchiveRecord, target.PositiveObservation, target.AbsenceEvidence} {
			if err := validateCanonicalRepairEvidenceReference(reference); err != nil {
				return err
			}
		}
		if index > 0 && targets[index-1].ObjectID == target.ObjectID {
			return errors.New("duplicate classified target evidence")
		}
	}
	if !canonicalRepairEvidenceTargetsEqual(targets, descriptor.Targets) {
		return errors.New("classified target evidence must be sorted by objectId")
	}
	return nil
}

func canonicalRepairEvidenceTargetsEqual(left, right []canonicalBoardRepairEvidenceTarget) bool {
	leftRaw, leftErr := canonicalJSON(left)
	rightRaw, rightErr := canonicalJSON(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftRaw, rightRaw)
}

func validateCanonicalRepairEvidenceFiles(evidenceDir string, references []canonicalBoardRepairEvidenceFile) (map[string][]byte, error) {
	if err := validateRootOwnedDirectory(evidenceDir); err != nil {
		return nil, err
	}
	result := map[string][]byte{}
	for _, reference := range references {
		if err := validateCanonicalRepairEvidenceReference(reference); err != nil {
			return nil, err
		}
		path := filepath.Join(evidenceDir, reference.Path)
		if !pathWithin(path, evidenceDir) {
			return nil, errors.New("evidence file escapes evidence directory")
		}
		raw, err := readRootOnlyRegularFile(path)
		if err != nil {
			return nil, fmt.Errorf("evidence file %s: %w", reference.Path, err)
		}
		if int64(len(raw)) != reference.Size || sha256Hex(raw) != reference.SHA256 {
			return nil, fmt.Errorf("evidence file %s seal mismatch", reference.Path)
		}
		result[reference.Path] = raw
	}
	return result, nil
}

func runCanonicalBoardRepairCLI(ctx context.Context, manifestPath, manifestSHA, authorityPath, receiptPath string) error {
	if os.Geteuid() != 0 {
		return errors.New("canonical board repair must run as root")
	}
	for name, value := range map[string]string{"candidate manifest": manifestPath, "candidate manifest sha256": manifestSHA, "authority marker": authorityPath, "repair receipt": receiptPath} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s is required", name)
		}
	}
	manifestPath, authorityPath, receiptPath = filepath.Clean(manifestPath), filepath.Clean(authorityPath), filepath.Clean(receiptPath)
	if !filepath.IsAbs(manifestPath) || !filepath.IsAbs(authorityPath) || !filepath.IsAbs(receiptPath) || !isHexDigest(manifestSHA) {
		return errors.New("canonical repair paths must be absolute and manifest sha256 must be exact")
	}
	manifestRaw, err := readRootOnlyRegularFile(manifestPath)
	if err != nil {
		return fmt.Errorf("candidate manifest: %w", err)
	}
	if sha256Hex(manifestRaw) != strings.ToLower(manifestSHA) {
		return errors.New("candidate manifest sha256 mismatch")
	}
	var manifest canonicalBoardRepairManifest
	if err := decodeCanonicalRepairJSON(manifestRaw, &manifest); err != nil {
		return fmt.Errorf("candidate manifest: %w", err)
	}
	authorityRaw, err := readRootOnlyRegularFile(authorityPath)
	if err != nil {
		return fmt.Errorf("authority marker: %w", err)
	}
	if err := validateCanonicalRepairAuthority(authorityRaw, strings.ToLower(manifestSHA)); err != nil {
		return err
	}
	if err := validateCanonicalBoardRepairInputs(manifest, strings.ToLower(manifestSHA), receiptPath); err != nil {
		return err
	}
	if err := validateCanonicalRepairRelease(manifest.ReleaseCommit); err != nil {
		return err
	}
	if err := validateCanonicalRepairEvidence(manifest); err != nil {
		return err
	}
	existingReceipt, receiptFound, err := readCanonicalBoardRepairReceipt(receiptPath)
	if err != nil {
		return err
	}
	if !receiptFound {
		if err := validateCanonicalRepairAuthorityFresh(authorityPath, time.Now().UTC()); err != nil {
			return err
		}
	}

	dataDir := filepath.Clean(manifest.DataDir)
	if !filepath.IsAbs(dataDir) || dataDir != filepath.Clean(filepath.Dir(meetingMemoryPath())) {
		return errors.New("manifest data_dir does not match the configured application data directory")
	}
	if err := validateRootOwnedDirectory(dataDir); err != nil {
		return fmt.Errorf("data_dir: %w", err)
	}
	for _, queuePath := range []string{codexRunnerQueuePath(), renderRunnerQueuePath()} {
		if !filepath.IsAbs(queuePath) {
			return errors.New("canonical repair queue paths must be absolute")
		}
		if err := validatePrivateDirectory(queuePath); err != nil {
			return fmt.Errorf("canonical repair queue path %s: %w", queuePath, err)
		}
	}
	if err := validateRootOwnedReceiptParent(receiptPath, dataDir); err != nil {
		return err
	}
	memberPrincipals, err := repairMemberPrincipals(filepath.Join(dataDir, "users.json"))
	if err != nil {
		return err
	}
	databaseURL := strings.TrimSpace(os.Getenv("BONFIRE_CANONICAL_DATABASE_URL"))
	if databaseURL == "" || sha256Hex([]byte(databaseURL)) != manifest.DatabaseURLSHA256 {
		return errors.New("canonical database URL binding mismatch")
	}
	if receiptFound {
		engine, store, openErr := openCanonicalRepairEngine(ctx, dataDir, manifest.TenantID)
		if openErr != nil {
			return openErr
		}
		defer store.Close()
		proof, observeErr := engine.Observe(ctx)
		if observeErr != nil {
			return observeErr
		}
		if err := validateCanonicalBoardRepairFinalProof(proof); err != nil {
			return errors.New("completed repair receipt exists but live post-state is not complete")
		}
		return validateExistingCanonicalBoardRepairReceipt(existingReceipt, manifest, strings.ToLower(manifestSHA), sha256Hex(authorityRaw), canonicalRepairProofFingerprint(proof))
	}

	boardPath := filepath.Join(dataDir, "kanban-board.json")
	journalPath := filepath.Join(dataDir, "deleted-objects.jsonl")
	versionPath := filepath.Join(dataDir, "canonical", "object-versions.json")
	spoolPath := filepath.Join(dataDir, "canonical", "mutation-spool.bcs")
	boardRaw, err := readRegularNoSymlink(boardPath)
	if err != nil || !canonicalRepairFileSealMatches(manifest.Board, boardRaw) {
		return errors.New("board fingerprint mismatch")
	}
	if err := ensureRepairTargetsAbsentFromBoard(boardRaw, manifest.Candidates); err != nil {
		return err
	}
	journalRaw, err := readRegularNoSymlink(journalPath)
	if err != nil {
		return fmt.Errorf("journal: %w", err)
	}
	versionRaw, err := readRegularNoSymlink(versionPath)
	if err != nil {
		return fmt.Errorf("version map: %w", err)
	}
	spoolRaw, _, err := inspectCanonicalRepairSpoolFromScratch(spoolPath)
	if err != nil {
		return err
	}
	registry, err := NewCanonicalImportPayloadRegistry()
	if err != nil {
		return err
	}
	store, err := OpenPostgresCanonicalStore(ctx, databaseURL, registry)
	if err != nil {
		return err
	}
	defer store.Close()
	databaseSHA, err := canonicalRepairDatabaseFingerprint(ctx, store, manifest.TenantID)
	if err != nil {
		return fmt.Errorf("canonical database fingerprint mismatch: %w", err)
	}
	if err := validateCanonicalRepairNormalizedStart(manifest, journalRaw, versionRaw, spoolRaw, databaseSHA); err != nil {
		return err
	}
	versions, err := OpenFileCanonicalObjectVersionMap(versionPath)
	if err != nil {
		return err
	}
	engine := &postgresCanonicalBoardRepairEngine{
		manifest: manifest, registry: registry, store: store, versions: versions, spoolPath: spoolPath,
		usersPath: filepath.Join(dataDir, "users.json"), journalPath: journalPath, memberPrincipals: memberPrincipals,
	}
	startProof, err := engine.Observe(ctx)
	if err != nil {
		return err
	}
	if err := validateCanonicalBoardRepairProof(manifest, startProof); err != nil {
		return fmt.Errorf("repair full normalized-state preflight: %w", err)
	}
	run := canonicalBoardRepairRun{manifest: manifest, engine: engine, progress: writeCanonicalRepairProgress}
	receipt, err := run.execute(ctx)
	if err != nil {
		return err
	}
	journalAfter, err := os.ReadFile(journalPath)
	if err != nil {
		return err
	}
	versionAfter, err := os.ReadFile(versionPath)
	if err != nil {
		return err
	}
	databaseAfterSHA, err := canonicalRepairDatabaseFingerprint(ctx, store, manifest.TenantID)
	if err != nil {
		return err
	}
	receipt.Schema = canonicalBoardRepairReceiptSchema
	receipt.ReleaseCommit, receipt.Version, receipt.TenantID = manifest.ReleaseCommit, manifest.ReleaseCommit, manifest.TenantID
	receipt.CloneID, receipt.Environment, receipt.QualificationRun = manifest.CloneID, manifest.Environment, manifest.QualificationRun
	receipt.ManifestSHA256, receipt.AuthoritySHA256 = strings.ToLower(manifestSHA), sha256Hex(authorityRaw)
	receipt.CandidateSetSHA256 = manifest.CandidateSetSHA256
	receipt.BoardSHA256, receipt.JournalBeforeSHA256, receipt.JournalAfterSHA256 = sha256Hex(boardRaw), sha256Hex(journalRaw), sha256Hex(journalAfter)
	receipt.VersionMapBeforeSHA256, receipt.VersionMapAfterSHA256 = sha256Hex(versionRaw), sha256Hex(versionAfter)
	receipt.DatabaseBeforeSHA256, receipt.DatabaseAfterSHA256, receipt.CompletedAt = databaseSHA, databaseAfterSHA, time.Now().UTC()
	if err := validateCanonicalRepairReceiptContract(receipt); err != nil {
		return fmt.Errorf("refuse incomplete canonical repair receipt: %w", err)
	}
	if err := writeCanonicalBoardRepairReceipt(receiptPath, &receipt); err != nil {
		return err
	}
	writeCanonicalRepairProgress("receipt_sealed", len(manifest.Candidates))
	return nil
}

func validateCanonicalRepairNormalizedStart(manifest canonicalBoardRepairManifest, journalRaw, versionRaw, spoolRaw []byte, databaseSHA string) error {
	if !canonicalRepairFileSealMatches(manifest.JournalPrefix, journalRaw) {
		return errors.New("lifecycle journal differs from normalized seal without a complete matching repair receipt; cold restore required")
	}
	if !canonicalRepairFileSealMatches(manifest.VersionMap, versionRaw) {
		return errors.New("canonical version map differs from normalized seal; cold restore required")
	}
	if !canonicalRepairFileSealMatches(manifest.Spool, spoolRaw) {
		return errors.New("canonical spool differs from normalized seal; cold restore required")
	}
	if databaseSHA != manifest.DatabaseSHA256 {
		return errors.New("canonical database differs from normalized semantic fingerprint; cold restore required")
	}
	return nil
}

func validateExistingCanonicalBoardRepairReceipt(receipt canonicalBoardRepairReceipt, manifest canonicalBoardRepairManifest, manifestSHA, authoritySHA, liveFingerprint string) error {
	if err := validateCanonicalRepairReceiptContract(receipt); err != nil {
		return err
	}
	if receipt.Schema != canonicalBoardRepairReceiptSchema || receipt.Status != "complete" || receipt.ManifestSHA256 != manifestSHA || receipt.AuthoritySHA256 != authoritySHA ||
		receipt.ReleaseCommit != manifest.ReleaseCommit || receipt.Version != manifest.ReleaseCommit || receipt.TenantID != manifest.TenantID ||
		receipt.CloneID != manifest.CloneID || receipt.Environment != manifest.Environment || receipt.QualificationRun != manifest.QualificationRun ||
		receipt.CandidateSetSHA256 != manifest.CandidateSetSHA256 || receipt.CandidateCount != canonicalBoardRepairExactCount || receipt.AppliedCount != canonicalBoardRepairExactCount ||
		!receipt.FirstAppendObserved || !receipt.ZeroCandidates ||
		!receipt.PrincipalParity || !receipt.ProjectionParity || !receipt.IdempotentSecondReplay || receipt.CompletedAt.IsZero() ||
		receipt.AfterFingerprintSHA256 != liveFingerprint || !isHexDigest(receipt.JournalAfterSHA256) || !isHexDigest(receipt.VersionMapAfterSHA256) || !isHexDigest(receipt.DatabaseAfterSHA256) {
		return errors.New("existing canonical repair receipt does not match this completed authority")
	}
	if receipt.BeforeState.Journal != manifest.JournalPrefix || receipt.BeforeState.Board != manifest.Board || receipt.BeforeState.VersionMap != manifest.VersionMap ||
		receipt.BeforeState.VersionEntriesSHA256 != manifest.VersionEntriesSHA256 || receipt.BeforeState.Spool != manifest.Spool ||
		receipt.BeforeState.DatabaseSHA256 != manifest.DatabaseSHA256 || receipt.BeforeState.ImportInputSHA256 != manifest.ImportInputSHA256 ||
		receipt.BeforeState.ProofSHA256 != manifest.NormalizedProofSHA256 || receipt.BeforeState.CandidateSHA256 != manifest.TerminalCandidateSHA256 ||
		len(receipt.JournalAppendedRecords) != len(manifest.Candidates) {
		return errors.New("existing canonical repair receipt pre-state does not match its manifest")
	}
	for index, target := range manifest.Candidates {
		record := receipt.JournalAppendedRecords[index]
		absenceAt, err := time.Parse(time.RFC3339Nano, target.ObservedAbsenceAt)
		if err != nil || record.ObjectID != target.ObjectID || record.StateDigest != target.StateSHA256 || record.EvidenceBasis != target.EvidenceBasis || record.At.Before(absenceAt) {
			return fmt.Errorf("existing canonical repair receipt journal record %d does not match manifest target", index+1)
		}
	}
	return nil
}

func validateCanonicalRepairAuthority(raw []byte, manifestSHA string) error {
	if !isHexDigest(manifestSHA) || string(raw) != "CONFIRM CANONICAL BOARD REPAIR "+strings.ToLower(manifestSHA)+"\n" {
		return errors.New("authority marker is not the exact manifest-bound confirmation token")
	}
	return nil
}

func validateCanonicalRepairAuthorityFresh(path string, now time.Time) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	modified := info.ModTime().UTC()
	if modified.After(now.Add(time.Minute)) || now.Sub(modified) > 5*time.Minute {
		return errors.New("authority marker was not created immediately before repair execution")
	}
	return nil
}

// writeCanonicalBoardRepairObservation is a strictly read-only discovery
// path. It copies every importer-owned mutable input (board lifecycle journal,
// version map, and capture spool) into a private scratch directory before
// building the plan, and only queries PostgreSQL. Candidate IDs and digests are
// written to the requested root-only file, never stdout or application data.
func writeCanonicalBoardRepairObservation(ctx context.Context, outputPath string) error {
	if os.Geteuid() != 0 {
		return errors.New("canonical repair observation must run as root")
	}
	outputPath = filepath.Clean(strings.TrimSpace(outputPath))
	if !filepath.IsAbs(outputPath) {
		return errors.New("repair observation path must be absolute")
	}
	dataDir := filepath.Clean(filepath.Dir(meetingMemoryPath()))
	if err := validateRootOwnedDirectory(dataDir); err != nil {
		return err
	}
	for _, queuePath := range []string{codexRunnerQueuePath(), renderRunnerQueuePath()} {
		if !filepath.IsAbs(queuePath) {
			return errors.New("canonical repair observation queue paths must be absolute")
		}
		if err := validatePrivateDirectory(queuePath); err != nil {
			return fmt.Errorf("canonical repair observation queue path %s: %w", queuePath, err)
		}
	}
	if err := validateRootOwnedReceiptParent(outputPath, dataDir); err != nil {
		return err
	}
	release := currentReleaseIdentity()
	if !release.ProcessQualified {
		return errors.New("canonical repair observation requires an exact qualified release binary")
	}
	databaseURL := strings.TrimSpace(os.Getenv("BONFIRE_CANONICAL_DATABASE_URL"))
	if databaseURL == "" {
		return errors.New("canonical repair observation requires BONFIRE_CANONICAL_DATABASE_URL")
	}
	paths := canonicalImportPathsForRepair(dataDir)
	boardRaw, err := readRegularNoSymlink(paths.Board)
	if err != nil {
		return err
	}
	journalRaw, err := readRegularNoSymlink(paths.DeletedJournal)
	if err != nil {
		return err
	}
	versionPath := filepath.Join(dataDir, "canonical", "object-versions.json")
	versionRaw, err := readRegularNoSymlink(versionPath)
	if err != nil {
		return err
	}
	spoolPath := filepath.Join(dataDir, "canonical", "mutation-spool.bcs")
	spoolRaw, _, err := inspectCanonicalRepairSpoolFromScratch(spoolPath)
	if err != nil {
		return err
	}
	usersRaw, err := readRegularNoSymlink(filepath.Join(dataDir, "users.json"))
	if err != nil {
		return err
	}
	scratch, err := os.MkdirTemp("", "bonfire-canonical-repair-observe-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(scratch)
	if err := os.Chmod(scratch, 0o700); err != nil {
		return err
	}
	scratchBoard := filepath.Join(scratch, "kanban-board.json")
	scratchJournal := filepath.Join(scratch, "deleted-objects.jsonl")
	scratchVersion := filepath.Join(scratch, "object-versions.json")
	scratchSpool := filepath.Join(scratch, "mutation-spool.bcs")
	scratchUsers := filepath.Join(scratch, "users.json")
	for path, raw := range map[string][]byte{scratchBoard: boardRaw, scratchJournal: journalRaw, scratchVersion: versionRaw, scratchSpool: spoolRaw, scratchUsers: usersRaw} {
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			return err
		}
	}
	paths.Board, paths.DeletedJournal = scratchBoard, scratchJournal
	registry, err := NewCanonicalImportPayloadRegistry()
	if err != nil {
		return err
	}
	store, err := OpenPostgresCanonicalStore(ctx, databaseURL, registry)
	if err != nil {
		return err
	}
	defer store.Close()
	versions, err := OpenFileCanonicalObjectVersionMap(scratchVersion)
	if err != nil {
		return err
	}
	memberPrincipals, err := repairMemberPrincipals(scratchUsers)
	if err != nil {
		return err
	}
	manifest := canonicalBoardRepairManifest{TenantID: canonicalTenantID(), DataDir: dataDir}
	engine := &postgresCanonicalBoardRepairEngine{
		manifest: manifest, registry: registry, store: store, versions: versions, spoolPath: scratchSpool,
		usersPath: scratchUsers, journalPath: scratchJournal, memberPrincipals: memberPrincipals, paths: paths,
	}
	proof, err := engine.Observe(ctx)
	if err != nil {
		return err
	}
	candidateSHA, err := canonicalRepairCandidateDigest(proof.Candidates)
	if err != nil {
		return err
	}
	databaseSHA, err := canonicalRepairDatabaseFingerprint(ctx, store, manifest.TenantID)
	if err != nil {
		return err
	}
	versionSnapshot, err := versions.Snapshot()
	if err != nil {
		return err
	}
	observedTargets := make([]canonicalBoardRepairObservedTarget, 0)
	for _, candidate := range proof.Candidates {
		if candidate.Family == "board_card" && candidate.Kind == "tombstone_required" {
			observedTargets = append(observedTargets, canonicalBoardRepairObservedTarget{
				ObjectID: candidate.ObjectID, StateSHA256: candidate.StateDigest, TargetVersion: candidate.TargetVersion,
				PriorPrincipals: append([]string(nil), proof.PriorPrincipals[candidate.ObjectID]...),
			})
		}
	}
	observation := canonicalBoardRepairObservation{
		Schema: "bonfire.canonical-board-repair-observation.v1", ReleaseCommit: release.ReleaseCommit,
		TenantID: manifest.TenantID, DataDir: dataDir, DatabaseURLSHA256: sha256Hex([]byte(databaseURL)), DatabaseSHA256: databaseSHA, ImportInputSHA256: proof.ImportInputSHA256,
		Board:                canonicalBoardRepairFileSeal{Size: int64(len(boardRaw)), SHA256: sha256Hex(boardRaw)},
		Journal:              canonicalBoardRepairFileSeal{Size: int64(len(journalRaw)), SHA256: sha256Hex(journalRaw)},
		VersionMap:           canonicalBoardRepairFileSeal{Size: int64(len(versionRaw)), SHA256: sha256Hex(versionRaw)},
		VersionEntriesSHA256: hex.EncodeToString(versionSnapshot.Checksum[:]),
		Spool:                canonicalBoardRepairFileSeal{Size: int64(len(spoolRaw)), SHA256: sha256Hex(spoolRaw)},
		CandidateCount:       len(proof.Candidates), CandidateFingerprint: candidateSHA, ProofFingerprint: canonicalRepairProofFingerprint(proof), Candidates: proof.Candidates, Targets: observedTargets,
		PrincipalParity: proof.PrincipalParity, ProjectionReplayValid: proof.ProjectionReplayValid,
		EventHighWater: proof.EventHighWater, TenantEventCount: proof.EventCount, OutboxCount: proof.OutboxCount, VersionEntryCount: proof.VersionEntryCount, ObservedAt: time.Now().UTC(),
	}
	raw, err := json.MarshalIndent(observation, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomicallyDurable(outputPath, append(raw, '\n'), 0o600)
}

func writeCanonicalRepairProgress(phase string, count int) {
	raw, _ := json.Marshal(map[string]any{"schema": "bonfire.canonical-board-repair-progress.v1", "phase": phase, "applied_count": count})
	fmt.Fprintln(os.Stdout, string(raw))
}

func validateCanonicalRepairRelease(expected string) error {
	snapshot := currentReleaseIdentity()
	if !snapshot.ProcessQualified || snapshot.ReleaseCommit != expected {
		return errors.New("running binary does not match the exact authorized release commit")
	}
	return nil
}

func validateCanonicalBoardRepairInputs(manifest canonicalBoardRepairManifest, manifestSHA, receiptPath string) error {
	if manifest.Schema != canonicalBoardRepairManifestSchema {
		return errors.New("unsupported canonical repair manifest schema")
	}
	if !releaseCommitPattern.MatchString(manifest.ReleaseCommit) || strings.TrimSpace(manifest.TenantID) == "" || !filepath.IsAbs(manifest.DataDir) ||
		strings.TrimSpace(manifest.CloneID) == "" || (manifest.Environment != "isolated_cold_clone" && manifest.Environment != "production_protected_maintenance") ||
		(manifest.Environment == "isolated_cold_clone") != manifest.QualificationRun ||
		!filepath.IsAbs(manifest.EvidenceDir) ||
		!isHexDigest(manifest.DatabaseURLSHA256) || !isHexDigest(manifest.DatabaseSHA256) || !isHexDigest(manifest.VersionEntriesSHA256) ||
		!isHexDigest(manifest.NormalizedProofSHA256) || !isHexDigest(manifest.ImportInputSHA256) || !isHexDigest(manifest.CandidateSetSHA256) || !isHexDigest(manifest.TerminalCandidateSHA256) {
		return errors.New("invalid canonical repair manifest binding")
	}
	for _, evidence := range []canonicalBoardRepairEvidenceFile{manifest.EvidenceDescriptor, manifest.BackupManifest, manifest.NormalizationReceipt, manifest.CloneAuthority, manifest.ReleaseSourceReceipt, manifest.NormalizedObservation} {
		if err := validateCanonicalRepairEvidenceReference(evidence); err != nil {
			return err
		}
	}
	for _, seal := range []canonicalBoardRepairFileSeal{manifest.Board, manifest.JournalPrefix, manifest.VersionMap, manifest.Spool} {
		if seal.Size < 0 || !isHexDigest(seal.SHA256) {
			return errors.New("invalid canonical repair file seal")
		}
	}
	targets := append([]canonicalBoardRepairTarget(nil), manifest.Candidates...)
	if len(targets) != canonicalBoardRepairExactCount {
		return errors.New("canonical repair requires exactly seven candidates")
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].ObjectID < targets[j].ObjectID })
	for index := range targets {
		target := targets[index]
		if strings.TrimSpace(target.ObjectID) == "" || !isHexDigest(target.StateSHA256) || target.TargetVersion < 1 ||
			(target.EvidenceBasis != "done_archive_absence" && target.EvidenceBasis != "last_positive_source_current_absence") ||
			!canonicalRepairSelectedStateRole(target.SelectedStateRole) {
			return errors.New("invalid canonical repair target")
		}
		for _, evidence := range []canonicalBoardRepairEvidenceFile{target.SourceRecord, target.ArchiveRecord, target.PositiveObservation, target.AbsenceEvidence} {
			if err := validateCanonicalRepairEvidenceReference(evidence); err != nil {
				return fmt.Errorf("candidate %s: %w", target.ObjectID, err)
			}
		}
		if len(target.PriorPrincipals) == 0 || !sort.StringsAreSorted(target.PriorPrincipals) {
			return errors.New("canonical repair target requires exact sorted prior principals")
		}
		for principalIndex, principal := range target.PriorPrincipals {
			if !strings.HasPrefix(principal, "user:") || strings.TrimSpace(strings.TrimPrefix(principal, "user:")) == "" ||
				(principalIndex > 0 && target.PriorPrincipals[principalIndex-1] == principal) {
				return errors.New("canonical repair target has invalid prior principal authority")
			}
		}
		parsed, err := time.Parse(time.RFC3339Nano, target.ObservedAbsenceAt)
		if err != nil || parsed.Location() != time.UTC {
			return errors.New("canonical repair target observedAbsenceAt must be exact UTC RFC3339Nano")
		}
		if index > 0 && targets[index-1].ObjectID == target.ObjectID {
			return errors.New("duplicate canonical repair target")
		}
	}
	if !canonicalBoardRepairTargetsEqual(targets, manifest.Candidates) {
		return errors.New("canonical repair targets must be sorted by object_id")
	}
	targetRaw, _ := canonicalJSON(targets)
	if sha256Hex(targetRaw) != manifest.CandidateSetSHA256 {
		return errors.New("candidate set fingerprint mismatch")
	}
	terminalCandidates := make([]CanonicalRepairCandidate, 0, len(targets))
	for _, target := range targets {
		terminalCandidates = append(terminalCandidates, CanonicalRepairCandidate{
			Family: "board_card", ObjectID: target.ObjectID, Kind: "tombstone_required",
			StateDigest: target.StateSHA256, TargetVersion: target.TargetVersion,
		})
	}
	terminalSHA, terminalErr := canonicalRepairCandidateDigest(terminalCandidates)
	if terminalErr != nil || terminalSHA != manifest.TerminalCandidateSHA256 {
		return errors.New("terminal reconciliation candidate fingerprint mismatch")
	}
	if !filepath.IsAbs(receiptPath) || strings.TrimSpace(manifestSHA) == "" {
		return errors.New("canonical repair receipt or manifest binding is invalid")
	}
	return nil
}

func canonicalRepairSelectedStateRole(role string) bool {
	return role == "source_record" || role == "archive_record" || role == "positive_observation"
}

func validateCanonicalRepairEvidenceReference(reference canonicalBoardRepairEvidenceFile) error {
	if reference.Path == "" || filepath.IsAbs(reference.Path) || filepath.Clean(reference.Path) != reference.Path || reference.Path == "." ||
		reference.Path == ".." || strings.HasPrefix(reference.Path, ".."+string(filepath.Separator)) || reference.Size < 0 || !isHexDigest(reference.SHA256) {
		return errors.New("invalid relative evidence file reference")
	}
	return nil
}

func validateCanonicalRepairEvidence(manifest canonicalBoardRepairManifest) error {
	references := []canonicalBoardRepairEvidenceFile{manifest.EvidenceDescriptor, manifest.BackupManifest, manifest.NormalizationReceipt, manifest.CloneAuthority, manifest.ReleaseSourceReceipt, manifest.NormalizedObservation}
	for _, target := range manifest.Candidates {
		references = append(references, target.SourceRecord, target.ArchiveRecord, target.PositiveObservation, target.AbsenceEvidence)
	}
	rawByPath, err := validateCanonicalRepairEvidenceFiles(manifest.EvidenceDir, references)
	if err != nil {
		return err
	}
	var descriptor canonicalBoardRepairEvidenceDescriptor
	if err := decodeCanonicalRepairJSON(rawByPath[manifest.EvidenceDescriptor.Path], &descriptor); err != nil {
		return fmt.Errorf("decode evidence descriptor: %w", err)
	}
	if err := validateCanonicalBoardRepairEvidenceDescriptor(descriptor); err != nil {
		return err
	}
	if descriptor.ReleaseCommit != manifest.ReleaseCommit || descriptor.TenantID != manifest.TenantID || filepath.Clean(descriptor.DataDir) != filepath.Clean(manifest.DataDir) ||
		descriptor.CloneID != manifest.CloneID || descriptor.Environment != manifest.Environment || descriptor.BackupManifest != manifest.BackupManifest ||
		descriptor.NormalizationReceipt != manifest.NormalizationReceipt || descriptor.QualificationRun != manifest.QualificationRun || descriptor.CloneAuthority != manifest.CloneAuthority ||
		descriptor.ReleaseSourceReceipt != manifest.ReleaseSourceReceipt || descriptor.NormalizedObservation != manifest.NormalizedObservation {
		return errors.New("evidence descriptor does not match repair manifest")
	}
	if err := validateCanonicalBackupManifest(rawByPath[manifest.BackupManifest.Path]); err != nil {
		return err
	}
	if err := validateCanonicalReleaseSourceReceipt(rawByPath[manifest.ReleaseSourceReceipt.Path], manifest.ReleaseCommit); err != nil {
		return err
	}
	normalization, err := decodeCanonicalBoardNormalizationReceipt(rawByPath[manifest.NormalizationReceipt.Path])
	if err != nil {
		return err
	}
	var observation canonicalBoardRepairObservation
	if err := decodeCanonicalRepairJSON(rawByPath[manifest.NormalizedObservation.Path], &observation); err != nil {
		return err
	}
	if normalization.Schema != canonicalBoardNormalizationReceiptSchema || normalization.Status != "complete" || normalization.ReleaseCommit != manifest.ReleaseCommit ||
		normalization.TenantID != manifest.TenantID || normalization.CloneID != manifest.CloneID || normalization.Environment != manifest.Environment ||
		normalization.QualificationRun != manifest.QualificationRun || !normalization.ExactTerminalSeven || normalization.LifecycleAppendCount != 0 ||
		!normalization.PrincipalParity || !normalization.ProjectionReplayValid || !normalization.FullZeroDeltaSecondReplay ||
		normalization.AfterCandidateCount != canonicalBoardRepairExactCount || normalization.AfterCandidateSHA256 != manifest.TerminalCandidateSHA256 ||
		normalization.BoardAfter != manifest.Board || normalization.JournalAfter != manifest.JournalPrefix || normalization.VersionMapAfter != manifest.VersionMap ||
		normalization.VersionEntriesSHA256 != manifest.VersionEntriesSHA256 || normalization.SpoolAfter != manifest.Spool || normalization.DatabaseAfterSHA256 != manifest.DatabaseSHA256 {
		return errors.New("normalization receipt does not bind the repair manifest state")
	}
	if err := validateCanonicalNormalizationReceiptContract(normalization); err != nil {
		return err
	}
	if err := validateCanonicalCloneAuthority(manifest, normalization, rawByPath[manifest.CloneAuthority.Path]); err != nil {
		return err
	}
	if observation.Schema != "bonfire.canonical-board-repair-observation.v1" || observation.ReleaseCommit != manifest.ReleaseCommit || observation.TenantID != manifest.TenantID ||
		filepath.Clean(observation.DataDir) != filepath.Clean(manifest.DataDir) || observation.DatabaseURLSHA256 != manifest.DatabaseURLSHA256 ||
		observation.DatabaseSHA256 != manifest.DatabaseSHA256 || observation.Board != manifest.Board || observation.Journal != manifest.JournalPrefix ||
		observation.VersionMap != manifest.VersionMap || observation.VersionEntriesSHA256 != manifest.VersionEntriesSHA256 || observation.Spool != manifest.Spool ||
		observation.CandidateCount != canonicalBoardRepairExactCount || observation.CandidateFingerprint != manifest.TerminalCandidateSHA256 ||
		observation.ProofFingerprint != normalization.AfterFingerprintSHA256 || observation.ProofFingerprint != manifest.NormalizedProofSHA256 ||
		observation.ImportInputSHA256 != manifest.ImportInputSHA256 || observation.TenantEventCount != normalization.AfterState.TenantEventCount ||
		observation.OutboxCount != normalization.AfterState.ImportOutboxCount || observation.VersionEntryCount != normalization.AfterState.VersionEntryCount ||
		!observation.PrincipalParity || !observation.ProjectionReplayValid {
		return errors.New("normalized observation does not bind the repair manifest state")
	}
	evidenceByID := map[string]canonicalBoardRepairEvidenceTarget{}
	for _, target := range descriptor.Targets {
		evidenceByID[target.ObjectID] = target
	}
	observedByID := map[string]canonicalBoardRepairObservedTarget{}
	for _, target := range observation.Targets {
		observedByID[target.ObjectID] = target
	}
	allWrapperPaths := map[string]bool{}
	for _, reference := range []canonicalBoardRepairEvidenceFile{manifest.EvidenceDescriptor, manifest.BackupManifest, manifest.NormalizationReceipt, manifest.CloneAuthority, manifest.ReleaseSourceReceipt, manifest.NormalizedObservation} {
		allWrapperPaths[reference.Path] = true
	}
	for _, target := range manifest.Candidates {
		for _, reference := range []canonicalBoardRepairEvidenceFile{target.SourceRecord, target.ArchiveRecord, target.PositiveObservation, target.AbsenceEvidence} {
			if allWrapperPaths[reference.Path] {
				return errors.New("classified evidence aliases a role wrapper across targets")
			}
			allWrapperPaths[reference.Path] = true
		}
	}
	allArtifactPaths := map[string]bool{}
	for _, target := range manifest.Candidates {
		evidence, evidenceOK := evidenceByID[target.ObjectID]
		observed, observedOK := observedByID[target.ObjectID]
		if !evidenceOK || !observedOK || evidence.ObservedAbsenceAt != target.ObservedAbsenceAt || evidence.EvidenceBasis != target.EvidenceBasis || evidence.SelectedStateRole != target.SelectedStateRole ||
			evidence.SourceRecord != target.SourceRecord || evidence.ArchiveRecord != target.ArchiveRecord || evidence.PositiveObservation != target.PositiveObservation || evidence.AbsenceEvidence != target.AbsenceEvidence ||
			observed.StateSHA256 != target.StateSHA256 || observed.TargetVersion != target.TargetVersion || !stringSlicesEqual(observed.PriorPrincipals, target.PriorPrincipals) {
			return fmt.Errorf("evidence binding mismatch for target %s", target.ObjectID)
		}
		if err := validateCanonicalTargetEvidenceRecords(manifest.EvidenceDir, target, rawByPath, allWrapperPaths, allArtifactPaths); err != nil {
			return err
		}
	}
	return nil
}

func validateCanonicalTargetEvidenceRecords(evidenceDir string, target canonicalBoardRepairTarget, rawByPath map[string][]byte, allWrapperPaths, allArtifactPaths map[string]bool) error {
	type roleReference struct {
		role string
		ref  canonicalBoardRepairEvidenceFile
	}
	references := []roleReference{
		{role: "source_record", ref: target.SourceRecord},
		{role: "archive_record", ref: target.ArchiveRecord},
		{role: "positive_observation", ref: target.PositiveObservation},
		{role: "absence_observation", ref: target.AbsenceEvidence},
	}
	absenceAt, err := time.Parse(time.RFC3339Nano, target.ObservedAbsenceAt)
	if err != nil || absenceAt.Location() != time.UTC {
		return fmt.Errorf("target %s has invalid exact absence time", target.ObjectID)
	}
	records := map[string]canonicalBoardRepairEvidenceRecord{}
	wrapperPaths := map[string]bool{}
	artifactRefs := make([]canonicalBoardRepairEvidenceFile, 0, len(references))
	for _, expected := range references {
		if wrapperPaths[expected.ref.Path] {
			return fmt.Errorf("target %s reuses an evidence wrapper across roles", target.ObjectID)
		}
		wrapperPaths[expected.ref.Path] = true
		var record canonicalBoardRepairEvidenceRecord
		if err := decodeCanonicalRepairJSON(rawByPath[expected.ref.Path], &record); err != nil {
			return fmt.Errorf("target %s %s evidence record: %w", target.ObjectID, expected.role, err)
		}
		if record.Schema != canonicalBoardRepairEvidenceRecordSchema || record.Role != expected.role || record.ObjectID != target.ObjectID ||
			record.ObservedAt.IsZero() || record.ObservedAt.Location() != time.UTC {
			return fmt.Errorf("target %s %s evidence record binding mismatch", target.ObjectID, expected.role)
		}
		if err := validateCanonicalRepairEvidenceReference(record.SourceArtifact); err != nil {
			return fmt.Errorf("target %s %s source artifact: %w", target.ObjectID, expected.role, err)
		}
		if record.SourceArtifact.Path == expected.ref.Path || allWrapperPaths[record.SourceArtifact.Path] || allArtifactPaths[record.SourceArtifact.Path] {
			return fmt.Errorf("target %s has aliased or cyclic evidence artifacts", target.ObjectID)
		}
		allArtifactPaths[record.SourceArtifact.Path] = true
		artifactRefs = append(artifactRefs, record.SourceArtifact)
		if record.Present {
			if !isHexDigest(record.StateSHA256) {
				return fmt.Errorf("target %s %s present record lacks state digest", target.ObjectID, expected.role)
			}
		} else if record.StateSHA256 != "" {
			return fmt.Errorf("target %s %s absent record carries a state digest", target.ObjectID, expected.role)
		}
		records[expected.role] = record
	}
	// Catch a source artifact that aliases a wrapper encountered later.
	for path := range allArtifactPaths {
		if allWrapperPaths[path] {
			return fmt.Errorf("target %s evidence artifact aliases a role wrapper", target.ObjectID)
		}
	}
	if _, err := validateCanonicalRepairEvidenceFiles(evidenceDir, artifactRefs); err != nil {
		return fmt.Errorf("target %s source artifact: %w", target.ObjectID, err)
	}
	if !records["source_record"].Present || !records["positive_observation"].Present || records["absence_observation"].Present {
		return fmt.Errorf("target %s evidence does not prove positive source history and current absence", target.ObjectID)
	}
	if !records["positive_observation"].ObservedAt.After(absenceAt) && records["absence_observation"].ObservedAt.Equal(absenceAt) {
		// exact chronology proven below
	} else {
		return fmt.Errorf("target %s evidence observation chronology mismatch", target.ObjectID)
	}
	if records["source_record"].ObservedAt.After(absenceAt) || records["archive_record"].ObservedAt.After(absenceAt) {
		return fmt.Errorf("target %s source/archive evidence occurs after exact absence", target.ObjectID)
	}
	selected, ok := records[target.SelectedStateRole]
	if !ok || !selected.Present || selected.StateSHA256 != target.StateSHA256 {
		return fmt.Errorf("target %s selected source state does not match the canonical pre-state", target.ObjectID)
	}
	switch target.EvidenceBasis {
	case "done_archive_absence":
		if target.SelectedStateRole != "archive_record" || !records["archive_record"].Present {
			return fmt.Errorf("target %s done/archive basis lacks a selected present archive record", target.ObjectID)
		}
	case "last_positive_source_current_absence":
		if target.SelectedStateRole != "source_record" && target.SelectedStateRole != "positive_observation" {
			return fmt.Errorf("target %s last-positive basis selected an unrelated source role", target.ObjectID)
		}
	default:
		return fmt.Errorf("target %s uses unsupported evidence basis", target.ObjectID)
	}
	return nil
}

func validateCanonicalBackupManifest(raw []byte) error {
	required := []string{
		"./postgres.pgcustom", "./postgres.list", "./migrations-before.tsv", "./table-counts-before.tsv",
		"./private/volumes.inspect.json", "./private/containers.inspect.json", "./private/base.env", "./private/legacy-docker-compose.yml", "./private/legacy-Caddyfile",
		"./private/opt-meetingassist.tar", "./private/opt-meetingassist-workspace.tar", "./images/legacy-images.tar", "./meta/legacy-image-map.tsv",
		"./meta/networks.inspect.json", "./meta/legacy-container-authority.tsv", "./meta/expected-volumes", "./meta/actual-volumes",
		"./volumes/digitalocean_caddy_config.tar", "./volumes/digitalocean_caddy_data.tar", "./volumes/digitalocean_canonical_postgres.tar",
		"./volumes/digitalocean_codex_home.tar", "./volumes/digitalocean_codex_queue.tar", "./volumes/digitalocean_codex_runner_data.tar",
		"./volumes/digitalocean_meeting_data.tar", "./volumes/digitalocean_usage_ledger.tar",
	}
	seen := map[string]bool{}
	lines := bytes.Split(raw, []byte{'\n'})
	for index, line := range lines {
		if len(line) == 0 && index == len(lines)-1 {
			continue
		}
		if len(line) < 68 || line[64] != ' ' || line[65] != ' ' || !isHexDigest(string(line[:64])) {
			return errors.New("backup checksum manifest contains a non-canonical checksum record")
		}
		path := string(line[66:])
		if !strings.HasPrefix(path, "./") || path == "./" || strings.Contains(path, "\\") || strings.Contains(path, "//") ||
			strings.Contains(path, "/../") || strings.HasSuffix(path, "/..") || strings.Contains(path, "/./") || strings.HasSuffix(path, "/.") ||
			strings.ContainsAny(path, "\x00\r\n") || seen[path] {
			return errors.New("backup checksum manifest contains an unsafe or duplicate path")
		}
		seen[path] = true
	}
	if len(seen) == 0 {
		return errors.New("backup checksum manifest is empty")
	}
	for _, path := range required {
		if !seen[path] {
			return fmt.Errorf("backup checksum manifest omits required artifact %s", path)
		}
	}
	return nil
}

func validateCanonicalReleaseSourceReceipt(raw []byte, releaseCommit string) error {
	var receipt canonicalReleaseSourceReceipt
	if err := decodeCanonicalRepairJSON(raw, &receipt); err != nil {
		return fmt.Errorf("decode release source receipt: %w", err)
	}
	if receipt.Schema != "bonfire.release-source.v3" || receipt.ReleaseCommit != releaseCommit || receipt.ReviewedRef != receipt.ReleaseCommit ||
		!releaseCommitPattern.MatchString(receipt.ReleaseCommit) || !releaseCommitPattern.MatchString(receipt.GitTreeObject) || receipt.InputCount < 1 || receipt.SourceDateEpoch <= 0 {
		return errors.New("release source receipt identity is invalid")
	}
	for _, digest := range []string{receipt.GitTreeDigest, receipt.ReviewedInventorySHA256, receipt.ScopePolicySHA256, receipt.SourceArchiveSHA256, receipt.TransitiveInputsSHA256, receipt.BuildConfigSHA256} {
		if !isHexDigest(digest) {
			return errors.New("release source receipt contains an invalid digest")
		}
	}
	requiredConfig := []string{
		".dockerignore", "Dockerfile", "Dockerfile.render", "go.mod", "go.sum", "deploy/digitalocean/docker-compose.yml", "deploy/digitalocean/Caddyfile",
		"deploy/digitalocean/bonfire-render-runner-v1.apparmor", "deploy/digitalocean/bonfire-render-runner-v1.seccomp.json",
		"deploy/digitalocean/release-build-inputs.json", "deploy/digitalocean/release-scope-policy.json", "scripts/bonfire-release.mjs",
	}
	if len(receipt.ConfigFiles) != len(requiredConfig) || receipt.InputCount < len(requiredConfig) {
		return errors.New("release source receipt config inventory is not exact")
	}
	for _, path := range requiredConfig {
		if !isHexDigest(receipt.ConfigFiles[path]) {
			return fmt.Errorf("release source receipt omits config binding %s", path)
		}
	}
	if receipt.ScopePolicySHA256 != receipt.ConfigFiles["deploy/digitalocean/release-scope-policy.json"] {
		return errors.New("release source receipt scope-policy binding mismatch")
	}
	configDigest, err := canonicalReleaseConfigDigest(receipt.ConfigFiles, requiredConfig)
	if err != nil || configDigest != receipt.BuildConfigSHA256 {
		return errors.New("release source receipt build-config binding mismatch")
	}
	return nil
}

func canonicalReleaseConfigDigest(files map[string]string, orderedPaths []string) (string, error) {
	var raw bytes.Buffer
	raw.WriteString(`{"schema":"bonfire.release-config.v2","files":{`)
	for index, path := range orderedPaths {
		if index > 0 {
			raw.WriteByte(',')
		}
		pathRaw, err := json.Marshal(path)
		if err != nil {
			return "", err
		}
		valueRaw, err := json.Marshal(files[path])
		if err != nil {
			return "", err
		}
		raw.Write(pathRaw)
		raw.WriteByte(':')
		raw.Write(valueRaw)
	}
	raw.WriteString("}}\n")
	return sha256Hex(raw.Bytes()), nil
}

func validateCanonicalCloneAuthority(manifest canonicalBoardRepairManifest, productionNormalization canonicalBoardNormalizationReceipt, authorityRaw []byte) error {
	if manifest.QualificationRun {
		var authority canonicalBoardCloneRunAuthority
		if err := decodeCanonicalRepairJSON(authorityRaw, &authority); err != nil {
			return fmt.Errorf("decode clone-run authority: %w", err)
		}
		if err := validateCanonicalCloneRunAuthorityDigest(authority); err != nil {
			return err
		}
		if manifest.Environment != "isolated_cold_clone" || authority.Schema != canonicalBoardCloneRunAuthoritySchema || authority.Status != "authorized" ||
			authority.ReleaseCommit != manifest.ReleaseCommit || authority.CloneID != manifest.CloneID || !authority.QualificationRun || authority.BackupManifestSHA256 != manifest.BackupManifest.SHA256 ||
			authority.ReleaseSourceReceiptSHA256 != manifest.ReleaseSourceReceipt.SHA256 || authority.CreatedAt.IsZero() || authority.CreatedAt.Location() != time.UTC {
			return errors.New("isolated qualification run lacks an exact clone-run authority")
		}
		if err := validateCanonicalRepairEvidenceReference(authority.ColdCloneReceipt); err != nil {
			return err
		}
		rawByPath, err := validateCanonicalRepairEvidenceFiles(manifest.EvidenceDir, []canonicalBoardRepairEvidenceFile{authority.ColdCloneReceipt})
		if err != nil {
			return err
		}
		var receipt canonicalColdCloneReceipt
		if err := decodeCanonicalRepairJSON(rawByPath[authority.ColdCloneReceipt.Path], &receipt); err != nil {
			return fmt.Errorf("decode cold-clone receipt: %w", err)
		}
		if err := validateCanonicalColdCloneReceiptDigest(receipt); err != nil {
			return err
		}
		if err := validateCanonicalColdCloneBinding(receipt, manifest.ReleaseCommit, manifest.CloneID, manifest.BackupManifest.SHA256, authority.CreatedAt); err != nil {
			return err
		}
		return nil
	}

	if manifest.Environment != "production_protected_maintenance" {
		return errors.New("production clone qualification cannot authorize a non-production repair")
	}
	var qualification canonicalBoardCloneQualification
	if err := decodeCanonicalRepairJSON(authorityRaw, &qualification); err != nil {
		return fmt.Errorf("decode clone qualification: %w", err)
	}
	if err := validateCanonicalCloneQualificationDigest(qualification); err != nil {
		return err
	}
	if qualification.Schema != canonicalBoardCloneQualificationSchema || qualification.Status != "complete" || qualification.ReleaseCommit != manifest.ReleaseCommit ||
		qualification.BackupManifestSHA256 != manifest.BackupManifest.SHA256 || qualification.ReleaseSourceReceiptSHA256 != manifest.ReleaseSourceReceipt.SHA256 ||
		len(qualification.Runs) != 2 || qualification.CompletedAt.IsZero() || qualification.CompletedAt.Location() != time.UTC {
		return errors.New("production repair lacks an exact two-run clone qualification")
	}
	seenCloneIDs := map[string]bool{}
	seenPaths := map[string]bool{}
	seenDigests := map[string]bool{}
	var commonBefore canonicalBoardRepairStateSeal
	for runIndex, run := range qualification.Runs {
		if strings.TrimSpace(run.CloneID) == "" || seenCloneIDs[run.CloneID] {
			return errors.New("clone qualification run IDs must be two distinct non-empty values")
		}
		seenCloneIDs[run.CloneID] = true
		if runIndex > 0 && qualification.Runs[runIndex-1].CloneID >= run.CloneID {
			return errors.New("clone qualification runs must be strictly sorted by cloneId")
		}
		references := []canonicalBoardRepairEvidenceFile{run.Manifest, run.CloneRunAuthority, run.ColdCloneReceipt, run.NormalizationReceipt, run.RepairReceipt, run.RestartObservation}
		for _, reference := range references {
			if err := validateCanonicalRepairEvidenceReference(reference); err != nil {
				return err
			}
			if seenPaths[reference.Path] {
				return errors.New("clone qualification aliases receipt files across runs")
			}
			if seenDigests[reference.SHA256] {
				return errors.New("clone qualification aliases receipt payloads across runs")
			}
			seenPaths[reference.Path] = true
			seenDigests[reference.SHA256] = true
		}
		rawByPath, err := validateCanonicalRepairEvidenceFiles(manifest.EvidenceDir, references)
		if err != nil {
			return err
		}
		normalization, err := decodeCanonicalBoardNormalizationReceipt(rawByPath[run.NormalizationReceipt.Path])
		if err != nil {
			return err
		}
		repair, err := decodeCanonicalBoardRepairReceipt(rawByPath[run.RepairReceipt.Path])
		if err != nil {
			return err
		}
		var restart canonicalBoardRestartObservation
		if err := decodeCanonicalRepairJSON(rawByPath[run.RestartObservation.Path], &restart); err != nil {
			return err
		}
		if err := validateCanonicalRestartObservationDigest(restart); err != nil {
			return err
		}
		var cloneManifest canonicalBoardRepairManifest
		if err := decodeCanonicalRepairJSON(rawByPath[run.Manifest.Path], &cloneManifest); err != nil {
			return fmt.Errorf("decode qualification clone manifest: %w", err)
		}
		if sha256Hex(rawByPath[run.Manifest.Path]) != run.Manifest.SHA256 || cloneManifest.Schema != canonicalBoardRepairManifestSchema ||
			cloneManifest.ReleaseCommit != manifest.ReleaseCommit || cloneManifest.CloneID != run.CloneID || cloneManifest.Environment != "isolated_cold_clone" || !cloneManifest.QualificationRun ||
			cloneManifest.BackupManifest.SHA256 != manifest.BackupManifest.SHA256 || cloneManifest.ReleaseSourceReceipt.SHA256 != manifest.ReleaseSourceReceipt.SHA256 ||
			cloneManifest.CloneAuthority != run.CloneRunAuthority || cloneManifest.NormalizationReceipt != run.NormalizationReceipt ||
			cloneManifest.CandidateSetSHA256 != manifest.CandidateSetSHA256 || cloneManifest.TerminalCandidateSHA256 != manifest.TerminalCandidateSHA256 {
			return errors.New("qualification clone manifest does not bind the exact run and production terminal candidate authority")
		}
		if err := validateCanonicalBoardRepairInputs(cloneManifest, run.Manifest.SHA256, filepath.Join(manifest.EvidenceDir, "qualification-receipt-check.json")); err != nil {
			return fmt.Errorf("qualification clone manifest: %w", err)
		}
		var runAuthority canonicalBoardCloneRunAuthority
		if err := decodeCanonicalRepairJSON(rawByPath[run.CloneRunAuthority.Path], &runAuthority); err != nil {
			return err
		}
		if err := validateCanonicalCloneRunAuthorityDigest(runAuthority); err != nil {
			return err
		}
		if runAuthority.Schema != canonicalBoardCloneRunAuthoritySchema || runAuthority.Status != "authorized" || runAuthority.ReleaseCommit != manifest.ReleaseCommit ||
			runAuthority.CloneID != run.CloneID || !runAuthority.QualificationRun || runAuthority.BackupManifestSHA256 != manifest.BackupManifest.SHA256 ||
			runAuthority.ReleaseSourceReceiptSHA256 != manifest.ReleaseSourceReceipt.SHA256 || runAuthority.ColdCloneReceipt != run.ColdCloneReceipt {
			return errors.New("qualification clone-run authority is not exact")
		}
		var cold canonicalColdCloneReceipt
		if err := decodeCanonicalRepairJSON(rawByPath[run.ColdCloneReceipt.Path], &cold); err != nil {
			return err
		}
		if err := validateCanonicalColdCloneReceiptDigest(cold); err != nil {
			return err
		}
		if err := validateCanonicalColdCloneBinding(cold, manifest.ReleaseCommit, run.CloneID, manifest.BackupManifest.SHA256, runAuthority.CreatedAt); err != nil {
			return err
		}
		if normalization.Schema != canonicalBoardNormalizationReceiptSchema || normalization.Status != "complete" || normalization.ReleaseCommit != manifest.ReleaseCommit ||
			normalization.CloneID != run.CloneID || normalization.Environment != "isolated_cold_clone" || !normalization.QualificationRun ||
			!normalization.ExactTerminalSeven || !normalization.FullZeroDeltaSecondReplay || normalization.AfterCandidateSHA256 != manifest.TerminalCandidateSHA256 ||
			normalization.AfterState != productionNormalization.AfterState {
			return errors.New("clone qualification normalization receipt is incomplete")
		}
		if err := validateCanonicalNormalizationReceiptContract(normalization); err != nil {
			return err
		}
		if repair.Schema != canonicalBoardRepairReceiptSchema || repair.Status != "complete" || repair.ReleaseCommit != manifest.ReleaseCommit ||
			repair.CloneID != run.CloneID || repair.Environment != "isolated_cold_clone" || !repair.QualificationRun ||
			repair.ManifestSHA256 != run.Manifest.SHA256 || repair.CandidateSetSHA256 != manifest.CandidateSetSHA256 || repair.BeforeCandidateSHA256 != manifest.TerminalCandidateSHA256 ||
			repair.CandidateCount != 7 || repair.AppliedCount != 7 || !repair.ZeroCandidates || !repair.PrincipalParity || !repair.ProjectionParity || !repair.IdempotentSecondReplay {
			return errors.New("clone qualification repair receipt is incomplete")
		}
		if err := validateCanonicalRepairReceiptContract(repair); err != nil {
			return err
		}
		if normalization.TenantID != repair.TenantID || normalization.AfterState != repair.BeforeState {
			return errors.New("clone qualification normalization and repair states are not contiguous")
		}
		if commonBefore == (canonicalBoardRepairStateSeal{}) {
			commonBefore = normalization.BeforeState
		} else if normalization.BeforeState != commonBefore {
			return errors.New("clone qualification runs did not start from the same fresh restored state")
		}
		if restart.Schema != canonicalBoardRestartObservationSchema || restart.Status != "complete" || restart.ReleaseCommit != manifest.ReleaseCommit || restart.CloneID != run.CloneID ||
			restart.Environment != "isolated_cold_clone" || !restart.QualificationRun ||
			restart.NormalizationReceiptSHA256 != run.NormalizationReceipt.SHA256 || restart.RepairReceiptSHA256 != run.RepairReceipt.SHA256 ||
			restart.State != repair.AfterState || !restart.ZeroCandidates || !restart.PrincipalParity || !restart.ProjectionReplayValid || !restart.ZeroDeltaReplay ||
			restart.ObservedAt.IsZero() || restart.ObservedAt.Location() != time.UTC {
			return errors.New("clone qualification restart observation does not match the complete repair post-state")
		}
		if cold.CompletedAt.IsZero() || cold.CompletedAt.Location() != time.UTC || runAuthority.CreatedAt.IsZero() || runAuthority.CreatedAt.Location() != time.UTC ||
			normalization.CompletedAt.IsZero() || normalization.CompletedAt.Location() != time.UTC || repair.CompletedAt.IsZero() || repair.CompletedAt.Location() != time.UTC ||
			restart.ObservedAt.IsZero() || restart.ObservedAt.Location() != time.UTC ||
			cold.CompletedAt.After(runAuthority.CreatedAt) || runAuthority.CreatedAt.After(normalization.CompletedAt) || normalization.CompletedAt.After(repair.CompletedAt) ||
			repair.CompletedAt.After(restart.ObservedAt) || restart.ObservedAt.After(qualification.CompletedAt) {
			return errors.New("clone qualification evidence chronology is invalid")
		}
	}
	return nil
}

func validateCanonicalCloneRunAuthorityDigest(value canonicalBoardCloneRunAuthority) error {
	want := value.SelfSHA256
	value.SelfSHA256 = ""
	raw, err := canonicalJSON(value)
	if err != nil || !isHexDigest(want) || sha256Hex(raw) != want {
		return errors.New("clone-run authority self-digest mismatch")
	}
	return nil
}

func validateCanonicalColdCloneReceiptDigest(value canonicalColdCloneReceipt) error {
	want := value.SelfSHA256
	value.SelfSHA256 = ""
	raw, err := canonicalJSON(value)
	if err != nil || !isHexDigest(want) || sha256Hex(raw) != want {
		return errors.New("cold-clone receipt self-digest mismatch")
	}
	return nil
}

func validateCanonicalColdCloneBinding(receipt canonicalColdCloneReceipt, releaseCommit, cloneID, backupSHA string, authorityCreatedAt time.Time) error {
	wantVolumes := []string{"digitalocean_caddy_config", "digitalocean_caddy_data", "digitalocean_canonical_postgres", "digitalocean_codex_home", "digitalocean_codex_queue", "digitalocean_codex_runner_data", "digitalocean_meeting_data", "digitalocean_usage_ledger"}
	if receipt.Schema != canonicalColdCloneReceiptSchema || receipt.Status != "complete" || receipt.ReleaseCommit != releaseCommit || receipt.CloneID != cloneID || !receipt.QualificationRun ||
		receipt.BackupManifestSHA256 != backupSHA || receipt.RestoredVolumeCount != 8 || !stringSlicesEqual(receipt.RestoredVolumes, wantVolumes) ||
		!receipt.RawVolumeCompare || !receipt.PostgresRestore || !isHexDigest(receipt.PostgresDumpSHA256) || !isHexDigest(receipt.MigrationRowsSHA256) ||
		!isHexDigest(receipt.TableCountsSHA256) || receipt.CompletedAt.IsZero() || receipt.CompletedAt.Location() != time.UTC || authorityCreatedAt.IsZero() ||
		authorityCreatedAt.Location() != time.UTC || receipt.CompletedAt.After(authorityCreatedAt) {
		return errors.New("cold-clone receipt is not a complete, ordered, exact-run backup restore proof")
	}
	return nil
}

func validateCanonicalCloneQualificationDigest(value canonicalBoardCloneQualification) error {
	want := value.SelfSHA256
	value.SelfSHA256 = ""
	raw, err := canonicalJSON(value)
	if err != nil || !isHexDigest(want) || sha256Hex(raw) != want {
		return errors.New("clone qualification self-digest mismatch")
	}
	return nil
}

func validateCanonicalRestartObservationDigest(value canonicalBoardRestartObservation) error {
	want := value.SelfSHA256
	value.SelfSHA256 = ""
	raw, err := canonicalJSON(value)
	if err != nil || !isHexDigest(want) || sha256Hex(raw) != want {
		return errors.New("restart observation self-digest mismatch")
	}
	return nil
}

func canonicalBoardRepairTargetsEqual(left, right []canonicalBoardRepairTarget) bool {
	leftRaw, leftErr := canonicalJSON(left)
	rightRaw, rightErr := canonicalJSON(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftRaw, rightRaw)
}

func canonicalRepairCandidateDigest(candidates []CanonicalRepairCandidate) (string, error) {
	type candidate struct {
		Family, ObjectID, Kind, StateDigest, SourceStateDigest, TargetStateDigest, Principal string
		SourceVersion, TargetVersion                                                         int64
		ConfirmedByJournal                                                                   bool
	}
	values := make([]candidate, 0, len(candidates))
	for _, value := range candidates {
		values = append(values, candidate{value.Family, value.ObjectID, value.Kind, value.StateDigest, value.SourceStateDigest, value.TargetStateDigest, value.Principal, value.SourceVersion, value.TargetVersion, value.ConfirmedByJournal})
	}
	raw, err := canonicalJSON(values)
	return sha256Hex(raw), err
}

func canonicalRepairFileSealMatches(seal canonicalBoardRepairFileSeal, raw []byte) bool {
	return int64(len(raw)) == seal.Size && sha256Hex(raw) == seal.SHA256
}

func ensureRepairTargetsAbsentFromBoard(raw []byte, targets []canonicalBoardRepairTarget) error {
	var state kanbanBoardState
	if err := json.Unmarshal(raw, &state); err != nil {
		return fmt.Errorf("decode board: %w", err)
	}
	expected := map[string]bool{}
	for _, target := range targets {
		expected[target.ObjectID] = true
	}
	for _, card := range state.Cards {
		if expected[card.ID] {
			return fmt.Errorf("authorized repair target %s is still a live board object", card.ID)
		}
	}
	return nil
}

func canonicalRepairDatabaseFingerprint(ctx context.Context, store *PostgresCanonicalStore, tenantID string) (string, error) {
	var databaseName, serverVersion, migrations, objects, grants, outbox string
	err := store.pool.QueryRow(ctx, `SELECT current_database(), current_setting('server_version_num'),
		COALESCE((SELECT jsonb_agg(jsonb_build_object('version',version,'sha256',encode(sha256,'hex')) ORDER BY version)::text FROM schema_migrations),'[]')`).
		Scan(&databaseName, &serverVersion, &migrations)
	if err != nil {
		return "", err
	}
	if err := store.pool.QueryRow(ctx, `SELECT COALESCE(jsonb_agg(to_jsonb(q) ORDER BY q.object_type,q.object_id)::text,'[]') FROM (
		SELECT o.object_type,o.object_id,o.state_revision,o.content_revision,o.owner_principal_type,o.owner_principal_id,
			o.room_id,o.meeting_id,o.classification,o.state,encode(o.content_sha256,'hex') AS content_sha256,o.acl_version,
			e.event_id::text AS last_event_id,o.deleted_at,o.retain_until,o.legal_hold
		FROM objects o JOIN canonical_events e ON e.sequence=o.last_event_sequence WHERE o.tenant_id=$1) q`, tenantID).Scan(&objects); err != nil {
		return "", err
	}
	if err := store.pool.QueryRow(ctx, `SELECT COALESCE(jsonb_agg(to_jsonb(q) ORDER BY q.grant_id)::text,'[]') FROM (
		SELECT grant_id::text,object_type,object_id,acl_version,revision,subject_type,subject_id,action,room_id,sitting_id,
			granted_by_type,granted_by_id,expires_at,revoked_at,conditions
		FROM object_grants WHERE tenant_id=$1) q`, tenantID).Scan(&grants); err != nil {
		return "", err
	}
	if err := store.pool.QueryRow(ctx, `SELECT COALESCE(jsonb_agg(to_jsonb(q) ORDER BY q.event_id)::text,'[]') FROM (
		SELECT e.event_id::text,o.topic,o.payload,(o.delivered_at IS NOT NULL) AS delivered,o.attempts,o.last_error_code
		FROM outbox o JOIN canonical_events e ON e.event_id=o.event_id WHERE e.tenant_id=$1) q`, tenantID).Scan(&outbox); err != nil {
		return "", err
	}
	events, err := store.Events(ctx)
	if err != nil {
		return "", err
	}
	eventFingerprints := make([]string, 0)
	for _, event := range events {
		if event.TenantID != tenantID {
			continue
		}
		fingerprint, fingerprintErr := canonicalEventFingerprint(event)
		if fingerprintErr != nil {
			return "", fingerprintErr
		}
		eventFingerprints = append(eventFingerprints, fingerprint)
	}
	// PostgreSQL OIDs and sequence values are deliberately excluded: they are
	// restore-local. Immutable event identities/fingerprints, current projection,
	// grants, outbox semantics, migrations, and database ABI remain bound.
	raw, err := canonicalJSON(map[string]any{
		"database": databaseName, "server_version": serverVersion, "migrations": migrations, "tenant": tenantID,
		"event_fingerprints": eventFingerprints, "objects": objects, "grants": grants, "outbox": outbox,
	})
	return sha256Hex(raw), err
}

func repairMemberPrincipals(path string) ([]string, error) {
	raw, err := readRegularNoSymlink(path)
	if err != nil {
		return nil, fmt.Errorf("read users.json principals: %w", err)
	}
	var users []*userAccount
	if err := json.Unmarshal(raw, &users); err != nil {
		return nil, fmt.Errorf("decode users.json principals: %w", err)
	}
	var principals []string
	for _, user := range users {
		if user == nil {
			return nil, errors.New("users.json contains an empty user principal")
		}
		email := normalizeAccountEmail(user.Email)
		if email == "" {
			return nil, errors.New("users.json contains an empty user principal")
		}
		principals = append(principals, "user:"+email)
	}
	principals = uniqueSortedStrings(principals)
	if len(principals) == 0 {
		return nil, errors.New("users.json contains no valid user principals")
	}
	return principals, nil
}

func decodeCanonicalRepairJSON(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	return ensureJSONEOF(decoder)
}

func readRegularNoSymlink(path string) ([]byte, error) {
	if err := rejectSymlinkPathComponents(path); err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("path is not a regular non-symlink file")
	}
	return os.ReadFile(path)
}

func readRootOnlyRegularFile(path string) ([]byte, error) {
	raw, err := readRegularNoSymlink(path)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != 0 || info.Mode().Perm() != 0o600 {
		return nil, errors.New("file must be owned by root with mode 0600")
	}
	return raw, nil
}

func validateRootOwnedDirectory(path string) error {
	if err := rejectSymlinkPathComponents(path); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != 0 || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o022 != 0 {
		return errors.New("directory must be root-owned, non-symlink, and not group/world writable")
	}
	return nil
}

func validatePrivateDirectory(path string) error {
	if err := rejectSymlinkPathComponents(path); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o022 != 0 {
		return errors.New("directory must be a non-symlink private directory")
	}
	return nil
}

func rejectSymlinkPathComponents(path string) error {
	path = filepath.Clean(path)
	if !filepath.IsAbs(path) {
		return errors.New("path must be absolute")
	}
	for current := path; current != string(filepath.Separator); current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink path component is forbidden: %s", current)
		}
		next := filepath.Dir(current)
		if next == current {
			break
		}
	}
	return nil
}

func validateRootOwnedReceiptParent(receiptPath, dataDir string) error {
	if pathWithin(receiptPath, dataDir) {
		return errors.New("repair receipt must be outside the application data directory")
	}
	return validateRootOwnedDirectory(filepath.Dir(receiptPath))
}

func pathWithin(path, root string) bool {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func readCanonicalBoardRepairReceipt(path string) (canonicalBoardRepairReceipt, bool, error) {
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return canonicalBoardRepairReceipt{}, false, nil
	}
	raw, err := readRootOnlyRegularFile(path)
	if err != nil {
		return canonicalBoardRepairReceipt{}, false, err
	}
	receipt, err := decodeCanonicalBoardRepairReceipt(raw)
	return receipt, err == nil, err
}

func decodeCanonicalBoardRepairReceipt(raw []byte) (canonicalBoardRepairReceipt, error) {
	var receipt canonicalBoardRepairReceipt
	if err := decodeCanonicalRepairJSON(raw, &receipt); err != nil {
		return canonicalBoardRepairReceipt{}, err
	}
	want := receipt.SelfSHA256
	receipt.SelfSHA256 = ""
	encoded, err := canonicalJSON(receipt)
	if err != nil || want != sha256Hex(encoded) {
		return canonicalBoardRepairReceipt{}, errors.New("canonical repair receipt self-digest mismatch")
	}
	receipt.SelfSHA256 = want
	return receipt, nil
}

func writeCanonicalBoardRepairReceipt(path string, receipt *canonicalBoardRepairReceipt) error {
	if receipt == nil {
		return errors.New("canonical repair receipt is required")
	}
	receipt.SelfSHA256 = ""
	canonical, err := canonicalJSON(*receipt)
	if err != nil {
		return err
	}
	receipt.SelfSHA256 = sha256Hex(canonical)
	raw, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomicallyDurable(path, append(raw, '\n'), 0o600)
}

func readCanonicalBoardNormalizationReceipt(path string) (canonicalBoardNormalizationReceipt, bool, error) {
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return canonicalBoardNormalizationReceipt{}, false, nil
	}
	raw, err := readRootOnlyRegularFile(path)
	if err != nil {
		return canonicalBoardNormalizationReceipt{}, false, err
	}
	receipt, err := decodeCanonicalBoardNormalizationReceipt(raw)
	return receipt, err == nil, err
}

func decodeCanonicalBoardNormalizationReceipt(raw []byte) (canonicalBoardNormalizationReceipt, error) {
	var receipt canonicalBoardNormalizationReceipt
	if err := decodeCanonicalRepairJSON(raw, &receipt); err != nil {
		return canonicalBoardNormalizationReceipt{}, err
	}
	want := receipt.SelfSHA256
	receipt.SelfSHA256 = ""
	encoded, err := canonicalJSON(receipt)
	if err != nil || want != sha256Hex(encoded) {
		return canonicalBoardNormalizationReceipt{}, errors.New("canonical normalization receipt self-digest mismatch")
	}
	receipt.SelfSHA256 = want
	return receipt, nil
}

func writeCanonicalBoardNormalizationReceipt(path string, receipt *canonicalBoardNormalizationReceipt) error {
	if receipt == nil {
		return errors.New("canonical normalization receipt is required")
	}
	receipt.SelfSHA256 = ""
	canonical, err := canonicalJSON(*receipt)
	if err != nil {
		return err
	}
	receipt.SelfSHA256 = sha256Hex(canonical)
	raw, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomicallyDurable(path, append(raw, '\n'), 0o600)
}
