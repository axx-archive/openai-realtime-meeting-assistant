package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	dataVisualizationMaterializationFormat     = 1
	dataVisualizationMaterializationReceiptKey = "dataVisualizationMaterializationReceipt"
)

var (
	ErrDataVisualizationMaterializationInvalid  = errors.New("invalid data visualization materialization")
	ErrDataVisualizationMaterializationDenied   = errors.New("data visualization materialization denied")
	ErrDataVisualizationMaterializationConflict = errors.New("data visualization materialization conflict")

	// dataVisualizationMaterializationBeforeCASProbe is a test-only race seam.
	// Production leaves it nil. The final source fence and artifact CAS run
	// after this callback, so a test can prove that either kind of drift wins.
	dataVisualizationMaterializationBeforeCASProbe func()
	// dataVisualizationMaterializationBeforeReplayProbe lets tests force an
	// artifact/authority change after preflight but before replay admission.
	dataVisualizationMaterializationBeforeReplayProbe func()

	dataVisualizationMaterializationAdmissions = dataVisualizationMaterializationAdmissionRegistry{
		entries: make(map[string]*dataVisualizationMaterializationAdmission),
	}
	// dataVisualizationMaterializationAfterAdmissionProbe is test-only. It runs
	// after the exact key is locked and before the durable receipt scan.
	dataVisualizationMaterializationAfterAdmissionProbe func(string)
)

type dataVisualizationMaterializationAdmission struct {
	mu   sync.Mutex
	refs int
}

type dataVisualizationMaterializationAdmissionRegistry struct {
	mu      sync.Mutex
	entries map[string]*dataVisualizationMaterializationAdmission
}

func dataVisualizationMaterializationAdmissionKey(receipt DataVisualizationMaterializationReceipt) string {
	return receipt.ActorSHA256 + "\x00" + receipt.Target.TenantID + "\x00" + receipt.OperationID
}

func (registry *dataVisualizationMaterializationAdmissionRegistry) lock(key string) func() {
	registry.mu.Lock()
	if registry.entries == nil {
		registry.entries = make(map[string]*dataVisualizationMaterializationAdmission)
	}
	admission := registry.entries[key]
	if admission == nil {
		admission = &dataVisualizationMaterializationAdmission{}
		registry.entries[key] = admission
	}
	admission.refs++
	registry.mu.Unlock()

	admission.mu.Lock()
	return func() {
		admission.mu.Unlock()
		registry.mu.Lock()
		admission.refs--
		if admission.refs == 0 && registry.entries[key] == admission {
			delete(registry.entries, key)
		}
		registry.mu.Unlock()
	}
}

type DataVisualizationMaterializationRequest struct {
	OperationID string                          `json:"operationId"`
	Actor       string                          `json:"actor"`
	Artifact    ArtifactDispositionRef          `json:"artifact"`
	Compile     DataVisualizationCompileRequest `json:"compile"`
}

// DataVisualizationMaterializationReceipt is deliberately body-free. Source
// labels, cells, title, SVG, and table HTML live only in ACL-governed artifact
// bodies/blobs; this receipt binds their digests and the exact target preimage.
type DataVisualizationMaterializationReceipt struct {
	Format             int                    `json:"format"`
	OperationID        string                 `json:"operationId"`
	ActorSHA256        string                 `json:"actorSha256"`
	RequestSHA256      string                 `json:"requestSha256"`
	Target             ArtifactDispositionRef `json:"target"`
	SourceBlobSHA256   string                 `json:"sourceBlobSha256"`
	SVGSHA256          string                 `json:"svgSha256"`
	TableSHA256        string                 `json:"tableSha256"`
	ManifestBlobSHA256 string                 `json:"manifestBlobSha256"`
	ManifestSHA256     string                 `json:"manifestSha256"`
	ArtifactSHA256     string                 `json:"artifactSha256"`
}

type DataVisualizationMaterializationResult struct {
	Artifact      meetingMemoryEntry
	Receipt       DataVisualizationMaterializationReceipt
	ReceiptSHA256 string
	Replayed      bool
}

type preparedDataVisualizationMaterialization struct {
	compiled    CompiledDataVisualization
	sourceRaw   []byte
	manifestRaw []byte
	receiptRaw  []byte
	receipt     DataVisualizationMaterializationReceipt
	receiptRef  string
	body        string
	assets      []artifactAsset
}

func (receipt DataVisualizationMaterializationReceipt) validate() error {
	if receipt.Format != dataVisualizationMaterializationFormat || !strideIdentifier(receipt.OperationID) ||
		!isNonZeroHexDigest(receipt.ActorSHA256) || !isNonZeroHexDigest(receipt.RequestSHA256) ||
		receipt.Target.Validate() != nil || !isNonZeroHexDigest(receipt.SourceBlobSHA256) ||
		!isNonZeroHexDigest(receipt.SVGSHA256) || !isNonZeroHexDigest(receipt.TableSHA256) ||
		!isNonZeroHexDigest(receipt.ManifestBlobSHA256) || !isNonZeroHexDigest(receipt.ManifestSHA256) ||
		!isNonZeroHexDigest(receipt.ArtifactSHA256) {
		return ErrDataVisualizationMaterializationInvalid
	}
	return nil
}

func (app *kanbanBoardApp) MaterializeDataVisualization(ctx context.Context, user *userAccount, request DataVisualizationMaterializationRequest) (DataVisualizationMaterializationResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	actor := normalizeAccountEmail(request.Actor)
	if app == nil || app.memory == nil || user == nil || actor == "" || actor != normalizeAccountEmail(user.Email) ||
		!strideIdentifier(request.OperationID) || request.Artifact.Validate() != nil {
		return DataVisualizationMaterializationResult{}, ErrDataVisualizationMaterializationInvalid
	}

	current, currentHeader, ok := app.dataVisualizationAuthorizedTarget(ctx, user, request.Artifact.ArtifactID)
	if !ok || !dataVisualizationPrivateWorkOwnedBy(current, currentHeader, actor) {
		return DataVisualizationMaterializationResult{}, ErrDataVisualizationMaterializationDenied
	}

	prepared, err := prepareDataVisualizationMaterialization(request, actor)
	if err != nil {
		return DataVisualizationMaterializationResult{}, fmt.Errorf("%w: %v", ErrDataVisualizationMaterializationInvalid, err)
	}
	admissionKey := dataVisualizationMaterializationAdmissionKey(prepared.receipt)
	unlockAdmission := dataVisualizationMaterializationAdmissions.lock(admissionKey)
	defer unlockAdmission()
	if dataVisualizationMaterializationAfterAdmissionProbe != nil {
		dataVisualizationMaterializationAfterAdmissionProbe(admissionKey)
	}
	if app.dataVisualizationOperationBindingConflicts(prepared) {
		return DataVisualizationMaterializationResult{}, ErrDataVisualizationMaterializationConflict
	}
	var initialReplay DataVisualizationMaterializationResult
	var initialReplayFound bool
	var frozenSemanticPreimage string
	var frozenFullPreimage string
	if dataVisualizationMaterializationBeforeReplayProbe != nil {
		dataVisualizationMaterializationBeforeReplayProbe()
	}
	err = app.withCurrentAgentThreadSource(scoutAgentThread{Artifact: current}, func() error {
		latest, latestHeader, authorized := app.dataVisualizationAuthorizedTargetInsideSourceFence(ctx, user, request.Artifact.ArtifactID)
		if !authorized || !dataVisualizationPrivateWorkOwnedBy(latest, latestHeader, actor) {
			return ErrDataVisualizationMaterializationDenied
		}
		current, currentHeader = latest, latestHeader
		replay, found, replayErr := app.dataVisualizationMaterializationReplay(latest, prepared)
		initialReplay, initialReplayFound = replay, found
		if replayErr != nil || found {
			return replayErr
		}
		if !artifactDispositionRefFromHeader(latestHeader).Equal(request.Artifact) || !dataVisualizationWorkIsRunning(latest) {
			return ErrDataVisualizationMaterializationConflict
		}
		frozenSemanticPreimage, err = openAIToolProductSemanticPostimageDigest(latest, "")
		if err != nil {
			return fmt.Errorf("digest visualization semantic preimage: %w", err)
		}
		frozenFullPreimage, err = openAIToolArtifactPostimageDigest(latest)
		if err != nil {
			return fmt.Errorf("digest visualization full preimage: %w", err)
		}
		return nil
	})
	if err != nil {
		return DataVisualizationMaterializationResult{}, err
	}
	if initialReplayFound {
		if err := app.commitDataVisualizationTerminalProjection(ctx, initialReplay.Artifact, actor); err != nil {
			return DataVisualizationMaterializationResult{}, err
		}
		return initialReplay, nil
	}
	// Blob publication precedes the single artifact CAS. Content addressing
	// makes every successful write immutable and idempotent. A later denied or
	// failed CAS can leave only unreferenced blobs, never partial artifact refs.
	if err := persistPreparedDataVisualization(prepared); err != nil {
		return DataVisualizationMaterializationResult{}, err
	}
	if dataVisualizationMaterializationBeforeCASProbe != nil {
		dataVisualizationMaterializationBeforeCASProbe()
	}

	var committed meetingMemoryEntry
	var replayed DataVisualizationMaterializationResult
	var replayedInside bool
	err = app.withCurrentAgentThreadSource(scoutAgentThread{Artifact: current}, func() error {
		latest, latestHeader, authorized := app.dataVisualizationAuthorizedTargetInsideSourceFence(ctx, user, request.Artifact.ArtifactID)
		if !authorized || !dataVisualizationPrivateWorkOwnedBy(latest, latestHeader, actor) {
			return ErrDataVisualizationMaterializationDenied
		}
		if replay, found, replayErr := app.dataVisualizationMaterializationReplay(latest, prepared); found {
			if replayErr != nil {
				return replayErr
			}
			replayed, replayedInside = replay, true
			return nil
		}
		if !artifactDispositionRefFromHeader(latestHeader).Equal(request.Artifact) || !dataVisualizationWorkIsRunning(latest) {
			return ErrDataVisualizationMaterializationConflict
		}
		semanticPreimage, digestErr := openAIToolProductSemanticPostimageDigest(latest, "")
		if digestErr != nil || semanticPreimage != frozenSemanticPreimage {
			return ErrDataVisualizationMaterializationConflict
		}
		fullPreimage, digestErr := openAIToolArtifactPostimageDigest(latest)
		if digestErr != nil || fullPreimage != frozenFullPreimage {
			return ErrDataVisualizationMaterializationConflict
		}

		assets := mergeDataVisualizationAssets(artifactAssets(latest), prepared.assets)
		assetsRaw, encodeErr := json.Marshal(assets)
		if encodeErr != nil {
			return fmt.Errorf("encode visualization assets: %w", encodeErr)
		}
		metadata := map[string]string{
			artifactAssetsMetadataKey:                  string(assetsRaw),
			dataVisualizationMaterializationReceiptKey: string(prepared.receiptRaw),
			"dataVisualizationArtifactSha256":          prepared.receipt.ArtifactSHA256,
			"dataVisualizationManifestSha256":          prepared.receipt.ManifestSHA256,
			"status":                                   artifactStatusComplete,
			"threadStatus":                             "complete",
			"goalStatus":                               "verified",
			"currentStage":                             "verify_goal_completed",
			"progressPercent":                          "100",
			"progressNote":                             "Visualization ready",
			"reviewGate":                               "passed",
			"completedAt":                              time.Now().UTC().Format(time.RFC3339Nano),
			"type":                                     artifactTypeMarkdown,
		}
		updated, changed, updateErr := app.memory.updateOSArtifactWithMetadataIfHeaderAndToolPreimagesMatch(
			latestHeader, frozenSemanticPreimage, frozenFullPreimage, latest.ID, request.Compile.Spec.Title, prepared.body, user.Name, metadata,
		)
		if updateErr != nil {
			return updateErr
		}
		if !changed {
			return ErrDataVisualizationMaterializationConflict
		}
		committed = updated
		return nil
	})
	if err != nil {
		if errors.Is(err, ErrDurableReplaceAmbiguous) {
			app.memory.mu.Lock()
			reloadErr := app.memory.reloadVisibleMemoryGenerationLocked()
			app.memory.mu.Unlock()
			if reloadErr != nil {
				return DataVisualizationMaterializationResult{}, fmt.Errorf("%w; reload visible artifact generation: %v", err, reloadErr)
			}
		}
		return DataVisualizationMaterializationResult{}, err
	}
	if replayedInside {
		if err := app.commitDataVisualizationTerminalProjection(ctx, replayed.Artifact, actor); err != nil {
			return DataVisualizationMaterializationResult{}, err
		}
		return replayed, nil
	}
	if strings.TrimSpace(committed.ID) == "" {
		return DataVisualizationMaterializationResult{}, ErrDataVisualizationMaterializationConflict
	}
	if err := app.commitDataVisualizationTerminalProjection(ctx, committed, actor); err != nil {
		return DataVisualizationMaterializationResult{}, err
	}
	return DataVisualizationMaterializationResult{
		Artifact: committed, Receipt: prepared.receipt, ReceiptSHA256: prepared.receiptRef,
	}, nil
}

type dataVisualizationMaterializationReceiptNamespace struct {
	OperationID string `json:"operationId"`
	ActorSHA256 string `json:"actorSha256"`
	Target      struct {
		TenantID string `json:"tenantId"`
	} `json:"target"`
}

// Operation identity is actor-and-tenant scoped, not artifact scoped. Every
// committed receipt is therefore considered before any blob publication. A
// matching malformed claim fails closed; unrelated namespaces are ignored so
// corrupt legacy metadata cannot deny independent actors, tenants, or ops.
func (app *kanbanBoardApp) dataVisualizationOperationBindingConflicts(prepared preparedDataVisualizationMaterialization) bool {
	if app == nil || app.memory == nil {
		return true
	}
	for _, artifact := range app.memory.entriesOfKind(meetingMemoryKindOSArtifact, 0) {
		raw := strings.TrimSpace(artifact.Metadata[dataVisualizationMaterializationReceiptKey])
		if raw == "" {
			continue
		}
		var namespace dataVisualizationMaterializationReceiptNamespace
		if json.Unmarshal([]byte(raw), &namespace) != nil || namespace.OperationID != prepared.receipt.OperationID ||
			namespace.ActorSHA256 != prepared.receipt.ActorSHA256 || namespace.Target.TenantID != prepared.receipt.Target.TenantID {
			continue
		}
		var stored DataVisualizationMaterializationReceipt
		if json.Unmarshal([]byte(raw), &stored) != nil || stored.validate() != nil || stored != prepared.receipt ||
			strings.TrimSpace(artifact.ID) != prepared.receipt.Target.ArtifactID || stored.Target.ArtifactID != artifact.ID {
			return true
		}
	}
	return false
}

// The caller holds the source-thread lock and has already validated the exact
// Project/workstream binding through withCurrentAgentThreadSource. Re-entering
// projectBoundArtifactCurrent here would attempt to acquire that same lock.
func (app *kanbanBoardApp) dataVisualizationAuthorizedTargetInsideSourceFence(ctx context.Context, user *userAccount, id string) (meetingMemoryEntry, ArtifactAuthorizationHeader, bool) {
	if app == nil || app.memory == nil || user == nil {
		return meetingMemoryEntry{}, ArtifactAuthorizationHeader{}, false
	}
	header, found := app.memory.artifactAuthorizationHeaderByID(id)
	if !found || !artifactHeaderAuthorized(ctx, user, ACLReadContent, header) || !artifactHeaderAuthorized(ctx, user, ACLWrite, header) {
		return meetingMemoryEntry{}, ArtifactAuthorizationHeader{}, false
	}
	artifact, found := app.memory.artifactSnapshotIfHeaderMatches(id, header)
	return artifact, header, found
}

func (app *kanbanBoardApp) commitDataVisualizationTerminalProjection(ctx context.Context, artifact meetingMemoryEntry, actor string) error {
	metadata := artifact.Metadata
	threadID := strings.TrimSpace(metadata["originId"])
	agentThreadID := strings.TrimSpace(metadata["threadId"])
	if threadID == "" || agentThreadID == "" || normalizeAccountEmail(metadata["requestedBy"]) != actor {
		return ErrDataVisualizationMaterializationConflict
	}
	return app.commitScoutChatThreadRefStatusWithContext(ctx, threadID, actor, agentThreadID, "complete", artifact.ID)
}

func (app *kanbanBoardApp) dataVisualizationAuthorizedTarget(ctx context.Context, user *userAccount, id string) (meetingMemoryEntry, ArtifactAuthorizationHeader, bool) {
	if app == nil || app.memory == nil || user == nil {
		return meetingMemoryEntry{}, ArtifactAuthorizationHeader{}, false
	}
	header, found := app.memory.artifactAuthorizationHeaderByID(id)
	if !found || !artifactHeaderAuthorized(ctx, user, ACLReadContent, header) || !artifactHeaderAuthorized(ctx, user, ACLWrite, header) {
		return meetingMemoryEntry{}, ArtifactAuthorizationHeader{}, false
	}
	artifact, found := app.memory.artifactSnapshotIfHeaderMatches(id, header)
	if !found || !app.projectBoundArtifactCurrent(ctx, artifact) {
		return meetingMemoryEntry{}, ArtifactAuthorizationHeader{}, false
	}
	return artifact, header, true
}

func dataVisualizationPrivateWorkOwnedBy(artifact meetingMemoryEntry, header ArtifactAuthorizationHeader, actor string) bool {
	metadata := artifact.Metadata
	return artifact.Kind == meetingMemoryKindOSArtifact && strings.TrimSpace(metadata["source"]) == "scout_thread" &&
		strings.TrimSpace(metadata["threadId"]) != "" && strings.TrimSpace(metadata["originKind"]) == agentThreadOriginPrivateThread &&
		strings.TrimSpace(metadata["originId"]) != "" && strings.TrimSpace(metadata["sourceMessageId"]) != "" &&
		strings.TrimSpace(metadata["sourceMessageDigest"]) != "" && strings.TrimSpace(metadata["sourceWindowDigest"]) != "" &&
		normalizeScoutChatVisibility(header.Visibility) == scoutChatVisibilityPrivate &&
		normalizeAccountEmail(header.OwnerEmail) == actor && normalizeAccountEmail(metadata["requestedBy"]) == actor
}

func dataVisualizationWorkIsRunning(artifact meetingMemoryEntry) bool {
	return strings.EqualFold(strings.TrimSpace(firstNonEmptyString(artifact.Metadata["threadStatus"], artifact.Metadata["status"])), "running")
}

func prepareDataVisualizationMaterialization(request DataVisualizationMaterializationRequest, actor string) (preparedDataVisualizationMaterialization, error) {
	if err := validateDataVisualizationMarkdownCompatibility(request.Compile); err != nil {
		return preparedDataVisualizationMaterialization{}, err
	}
	compiled, err := CompileDataVisualization(request.Compile)
	if err != nil {
		return preparedDataVisualizationMaterialization{}, err
	}
	sourceRaw, err := canonicalJSON(struct {
		Domain  string                          `json:"domain"`
		Version string                          `json:"version"`
		Compile DataVisualizationCompileRequest `json:"compile"`
	}{"stride.data_visualization.materialized_source", dataVisualizationSchemaVersion, request.Compile})
	if err != nil {
		return preparedDataVisualizationMaterialization{}, err
	}
	manifestRaw, err := canonicalJSON(compiled.Manifest)
	if err != nil {
		return preparedDataVisualizationMaterialization{}, err
	}
	requestRaw, err := canonicalJSON(struct {
		Domain      string                          `json:"domain"`
		OperationID string                          `json:"operationId"`
		ActorSHA256 string                          `json:"actorSha256"`
		Target      ArtifactDispositionRef          `json:"target"`
		Compile     DataVisualizationCompileRequest `json:"compile"`
	}{"stride.data_visualization.materialization_request", request.OperationID, dataVisualizationActorDigest(actor), request.Artifact, request.Compile})
	if err != nil {
		return preparedDataVisualizationMaterialization{}, err
	}
	receipt := DataVisualizationMaterializationReceipt{
		Format: dataVisualizationMaterializationFormat, OperationID: request.OperationID,
		ActorSHA256: dataVisualizationActorDigest(actor), RequestSHA256: sha256Hex(requestRaw), Target: request.Artifact,
		SourceBlobSHA256: sha256Hex(sourceRaw), SVGSHA256: sha256Hex(compiled.SVG), TableSHA256: sha256Hex(compiled.AccessibleTableHTML),
		ManifestBlobSHA256: sha256Hex(manifestRaw), ManifestSHA256: compiled.Manifest.ManifestSHA256,
		ArtifactSHA256: compiled.Manifest.ArtifactSHA256,
	}
	if err := receipt.validate(); err != nil {
		return preparedDataVisualizationMaterialization{}, err
	}
	receiptRaw, err := canonicalJSON(receipt)
	if err != nil {
		return preparedDataVisualizationMaterialization{}, err
	}
	receiptRef := sha256Hex(receiptRaw)
	assets := []artifactAsset{
		{Ref: receipt.SourceBlobSHA256, Mime: "application/json", Name: "visualization-source.json", Kind: "export"},
		{Ref: receipt.SVGSHA256, Mime: "image/svg+xml", Name: "visualization.svg", Kind: "image"},
		{Ref: receipt.TableSHA256, Mime: "text/html; charset=utf-8", Name: "visualization-table.html", Kind: "export"},
		{Ref: receipt.ManifestBlobSHA256, Mime: "application/json", Name: "visualization-manifest.json", Kind: "export"},
		{Ref: receiptRef, Mime: "application/json", Name: "visualization-receipt.json", Kind: "export"},
	}
	return preparedDataVisualizationMaterialization{
		compiled: compiled, sourceRaw: sourceRaw, manifestRaw: manifestRaw, receiptRaw: receiptRaw,
		receipt: receipt, receiptRef: receiptRef, body: normalizeMemoryEntryText(meetingMemoryKindOSArtifact, dataVisualizationMarkdownBody(request.Compile)), assets: assets,
	}, nil
}

func persistPreparedDataVisualization(prepared preparedDataVisualizationMaterialization) error {
	items := []struct {
		data []byte
		mime string
		want string
	}{
		{prepared.sourceRaw, "application/json", prepared.receipt.SourceBlobSHA256},
		{prepared.compiled.SVG, "image/svg+xml", prepared.receipt.SVGSHA256},
		{prepared.compiled.AccessibleTableHTML, "text/html; charset=utf-8", prepared.receipt.TableSHA256},
		{prepared.manifestRaw, "application/json", prepared.receipt.ManifestBlobSHA256},
		{prepared.receiptRaw, "application/json", prepared.receiptRef},
	}
	for _, item := range items {
		ref, err := putBlob(item.data, item.mime)
		if err != nil {
			return fmt.Errorf("persist visualization blob: %w", err)
		}
		if ref != item.want {
			return errors.New("persisted visualization blob digest changed")
		}
		data, meta, readErr := getBlob(ref)
		if readErr != nil || meta.Mime != item.mime || string(data) != string(item.data) {
			return errors.New("persisted visualization blob failed exact verification")
		}
	}
	return nil
}

func (app *kanbanBoardApp) dataVisualizationMaterializationReplay(current meetingMemoryEntry, prepared preparedDataVisualizationMaterialization) (DataVisualizationMaterializationResult, bool, error) {
	raw := strings.TrimSpace(current.Metadata[dataVisualizationMaterializationReceiptKey])
	if raw == "" {
		return DataVisualizationMaterializationResult{}, false, nil
	}
	var stored DataVisualizationMaterializationReceipt
	if json.Unmarshal([]byte(raw), &stored) != nil || stored.validate() != nil {
		return DataVisualizationMaterializationResult{}, true, ErrDataVisualizationMaterializationConflict
	}
	if stored.OperationID != prepared.receipt.OperationID {
		return DataVisualizationMaterializationResult{}, false, nil
	}
	if stored != prepared.receipt || sha256Hex([]byte(raw)) != prepared.receiptRef || !app.dataVisualizationReceiptAssetsCurrent(current, prepared) {
		return DataVisualizationMaterializationResult{}, true, ErrDataVisualizationMaterializationConflict
	}
	return DataVisualizationMaterializationResult{
		Artifact: current, Receipt: stored, ReceiptSHA256: prepared.receiptRef, Replayed: true,
	}, true, nil
}

func (app *kanbanBoardApp) dataVisualizationReceiptAssetsCurrent(current meetingMemoryEntry, prepared preparedDataVisualizationMaterialization) bool {
	want := make(map[string]artifactAsset, len(prepared.assets))
	for _, asset := range prepared.assets {
		want[asset.Ref] = asset
	}
	for _, asset := range artifactAssets(current) {
		if expected, ok := want[asset.Ref]; ok && asset == expected {
			delete(want, asset.Ref)
		}
	}
	if len(want) != 0 || artifactStatus(current) != artifactStatusComplete ||
		!strings.EqualFold(strings.TrimSpace(current.Metadata["threadStatus"]), "complete") {
		return false
	}
	for _, item := range []struct {
		ref  string
		want []byte
	}{
		{prepared.receipt.SourceBlobSHA256, prepared.sourceRaw},
		{prepared.receipt.SVGSHA256, prepared.compiled.SVG},
		{prepared.receipt.TableSHA256, prepared.compiled.AccessibleTableHTML},
		{prepared.receipt.ManifestBlobSHA256, prepared.manifestRaw},
		{prepared.receiptRef, prepared.receiptRaw},
	} {
		data, meta, err := getBlob(item.ref)
		expectedAsset, ok := func() (artifactAsset, bool) {
			for _, asset := range prepared.assets {
				if asset.Ref == item.ref {
					return asset, true
				}
			}
			return artifactAsset{}, false
		}()
		if err != nil || !ok || meta.Mime != expectedAsset.Mime || string(data) != string(item.want) {
			return false
		}
	}
	header, found := app.memory.artifactAuthorizationHeaderByID(current.ID)
	if !found || artifactCapabilityDigest(current) != header.ContentDigest {
		return false
	}
	currentRef := artifactDispositionRefFromHeader(header)
	if currentRef.TenantID != prepared.receipt.Target.TenantID || currentRef.ArtifactID != prepared.receipt.Target.ArtifactID ||
		currentRef.ContentRevision != prepared.receipt.Target.ContentRevision+1 || currentRef.ACLVersion != prepared.receipt.Target.ACLVersion ||
		currentRef.AudienceDigest != prepared.receipt.Target.AudienceDigest || current.Text != prepared.body {
		return false
	}
	return true
}

func mergeDataVisualizationAssets(existing, fresh []artifactAsset) []artifactAsset {
	reservedNames := map[string]bool{}
	for _, asset := range fresh {
		reservedNames[asset.Name] = true
	}
	merged := make([]artifactAsset, 0, len(existing)+len(fresh))
	for _, asset := range existing {
		if !reservedNames[asset.Name] {
			merged = append(merged, asset)
		}
	}
	merged = append(merged, fresh...)
	return merged
}

func dataVisualizationActorDigest(actor string) string {
	return sha256Hex([]byte("stride.data_visualization.actor\x00" + normalizeAccountEmail(actor)))
}

func dataVisualizationMarkdownBody(request DataVisualizationCompileRequest) string {
	var out strings.Builder
	out.WriteString("# ")
	out.WriteString(dataVisualizationMarkdownCell(request.Spec.Title))
	out.WriteString("\n\n")
	out.WriteString(strings.Title(string(request.Spec.Kind))) //nolint:staticcheck // frozen user-facing v1 output
	out.WriteString(" chart · source `")
	out.WriteString(request.ExpectedSourceSHA256)
	out.WriteString("`\n\n")
	for _, column := range request.Table.Columns {
		out.WriteString("| ")
		out.WriteString(dataVisualizationMarkdownCell(dataVisualizationColumnDisplay(column)))
		out.WriteByte(' ')
	}
	out.WriteString("|\n")
	for range request.Table.Columns {
		out.WriteString("| --- ")
	}
	out.WriteString("|\n")
	for _, row := range request.Table.Rows {
		for _, cell := range row {
			out.WriteString("| ")
			if cell.Type == DataVisualizationCategory {
				out.WriteString(dataVisualizationMarkdownCell(cell.Text))
			} else {
				out.WriteString(formatDataVisualizationNumber(cell.Number))
			}
			out.WriteByte(' ')
		}
		out.WriteString("|\n")
	}
	out.WriteString("\nThe chart, accessible HTML table, exact typed source, and body-free verification manifest are attached to this Work artifact.\n")
	return out.String()
}

func dataVisualizationMarkdownCell(value string) string {
	value = strings.ReplaceAll(value, "\\", "\\\\")
	return strings.ReplaceAll(value, "|", "\\|")
}

// Current Work Markdown readers do not consistently honor table-cell escapes.
// The pure compiler can safely render these values into SVG/HTML, but the
// materializer must reject them until every governed Markdown reader agrees.
func validateDataVisualizationMarkdownCompatibility(request DataVisualizationCompileRequest) error {
	reject := func(value string) bool { return strings.ContainsAny(value, "|\\") }
	if reject(request.Spec.Title) {
		return errors.New("visualization title is incompatible with Work Markdown")
	}
	for _, column := range request.Table.Columns {
		if reject(column.Label) || reject(column.Unit) {
			return errors.New("visualization column label or unit is incompatible with Work Markdown")
		}
	}
	for _, row := range request.Table.Rows {
		for _, cell := range row {
			if cell.Type == DataVisualizationCategory && reject(cell.Text) {
				return errors.New("visualization category is incompatible with Work Markdown")
			}
		}
	}
	return nil
}

func sortedDataVisualizationAssetRefs(entry meetingMemoryEntry) []string {
	refs := make([]string, 0, len(artifactAssets(entry)))
	for _, asset := range artifactAssets(entry) {
		refs = append(refs, asset.Ref)
	}
	sort.Strings(refs)
	return refs
}
