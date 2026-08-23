package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	openAIToolProductRequestPolicyRevision  = "conversation-first-router-v1"
	openAIToolOperationReceiptsMetadataKey  = "openaiToolOperationReceipts"
	openAIToolFinalRunDigestMetadataKey     = "openaiToolFinalRunDigest"
	openAIToolFinalUseDigestMetadataKey     = "openaiToolFinalUseDigest"
	openAIToolFanOutDigestMetadataKey       = "openaiToolFanOutDigest"
	openAIToolSessionDigestMetadataKey      = "openaiToolSessionDigest"
	openAIToolFinalizedMetadataKey          = "openaiToolFinalized"
	openAIToolBaseRevisionMetadataKey       = "openaiToolBaseArtifactRevision"
	openAIToolBasePostimageMetadataKey      = "openaiToolBaseArtifactPostimage"
	openAIToolBaseKeyIDMetadataKey          = "openaiToolBaseEffectKeyId"
	openAIToolBaseKeyVersionMetadataKey     = "openaiToolBaseEffectKeyVersion"
	openAIToolBaseAuthenticationMetadataKey = "openaiToolBaseAuthentication"
	openAIToolFinalReceiptMetadataKey       = "openaiToolFinalReceipt"
	openAIToolProjectionReceiptsMetadataKey = "openaiToolProjectionReceipts"
	openAIToolMaxPersistedEffectReceipts    = 256
	openAIToolProjectionPending             = "pending"
	openAIToolProjectionDelivered           = "delivered"
)

// Test-only crash/race seams around the terminal artifact transaction.
var (
	openAIToolBeforeFinalArtifactCASProbe   func()
	openAIToolAfterOperationProofProbe      func()
	openAIToolAfterFinalArtifactCommitProbe func(meetingMemoryEntry) error
	openAIToolBeforeProjectionDispatchProbe func(openAIToolProductEffectReceipt) error
	openAIToolAfterProjectionEventProbe     func(openAIToolProductEffectReceipt) error
	openAIToolProjectionEventProbe          func(openAIToolProductEffectReceipt)
)

var errOpenAIToolFinalizationInterrupted = errors.New("OpenAI tool finalization was interrupted after durable commit")

// openAIToolProductRuntime is installed directly by the server bootstrap. It
// has no environment constructor: a credential, legacy runner assignment, or
// client field cannot activate this carrier. The checked-in product keeps the
// field nil until a reviewed journal, key set, provider transport, and
// canonical tenant converter are installed together.
type openAIToolProductRuntime struct {
	Enabled bool
	Carrier *openAIToolLoopCarrier
}

// openAIToolProductRunner is the sole ordinary-work bridge into the secure
// four-tool carrier. It is deliberately not a selectable persisted runner
// name; selectAgentRunner may return it only from the server-owned app field.
type openAIToolProductRunner struct {
	app     *kanbanBoardApp
	runtime *openAIToolProductRuntime
}

func (runner *openAIToolProductRunner) Name() string { return "openai_tool_loop" }

func (runner *openAIToolProductRunner) Capabilities() AgentCapabilities {
	return AgentCapabilities{ToolLoop: true, MaxRuntime: defaultAgentThreadRequestTimeout}
}

func (runner *openAIToolProductRunner) RunJob(ctx context.Context, job AgentJob) (<-chan AgentProgress, error) {
	if runner == nil || runner.app == nil || runner.runtime == nil || !runner.runtime.Enabled || runner.runtime.Carrier == nil || !runner.runtime.Carrier.Enabled {
		return nil, errOpenAIToolCarrierUnavailable
	}
	backend, expectation, err := newOpenAIToolProductBackend(ctx, runner.app, job, runner.runtime.Carrier.Journal)
	if err != nil {
		return nil, err
	}
	manifest, err := buildOpenAIToolManifest()
	if err != nil {
		return nil, errOpenAIToolCarrierUnavailable
	}
	carrier := *runner.runtime.Carrier
	carrier.Manifest = manifest
	carrier.Authority = &openAIToolProductAuthorityLease{backend: backend}
	carrier.Executor = openAIToolEffectAdapter{Backend: backend}
	carrier.Finalizer = &openAIToolProductFinalizer{backend: backend}

	out := make(chan AgentProgress, 1)
	go func() {
		defer close(out)
		instructions := runner.app.agentThreadInstructionsForThread(job.thread)
		instructions += "\n\nTool boundary: You may use only the four server-admitted functions shown in this request. Use report_goal_state for truthful progress, answer_memory_question for bounded private recall, create_artifact only when explicit final content should become a new private artifact, and update_artifact only for the exact held artifact. Any other capability is unavailable."
		request := openAIToolLoopRequest{
			Instructions: instructions,
			UserTurn:     buildAgentThreadInput(job.thread, job.Context.Board, job.Context.Memory, time.Now()),
			Expectation:  expectation,
		}
		var result openAIToolLoopResult
		var runErr error
		for attempt := 0; attempt < 2; attempt++ {
			result, runErr = carrier.Run(ctx, request)
			if runErr == nil || ctx.Err() != nil || errors.Is(runErr, errOpenAIToolFinalizationInterrupted) {
				break
			}
		}
		metadata := map[string]string{
			"worker":          "openai_tool_loop",
			"workerBoundary":  "responses_four_tool_journal",
			"model":           openAIToolRunnerModel,
			"reasoningEffort": openAIToolRunnerReasoningEffort,
		}
		if runErr == nil {
			metadata[openAIToolFinalizedMetadataKey] = "true"
			metadata["openaiToolOperationCount"] = strconv.Itoa(len(result.OperationIDs))
		}
		out <- AgentProgress{Terminal: true, Text: result.Text, Err: runErr, Metadata: metadata}
	}()
	return out, nil
}

type openAIToolProductBackend struct {
	app         *kanbanBoardApp
	job         AgentJob
	expectation openAIToolAuthorityExpectation
	sessionHash string
	effectAuth  openAIToolProductEffectAuthenticator
	journal     *openAIToolJournal
}

type openAIToolProductEffectAuthenticator interface {
	signOpenAIToolProductEffectReceipt(context.Context, []byte) (string, string, string, error)
	verifyOpenAIToolProductEffectReceipt(context.Context, string, string, []byte, string) error
}

func openAIToolProductBaseAuthorityMaterial(artifact meetingMemoryEntry, revision, postimage string) ([]byte, error) {
	return canonicalJSON(map[string]any{
		"domain": "stride-openai-tool-product-base-authority-v1", "artifact_id": artifact.ID,
		"revision": revision, "postimage": postimage, "thread_id": artifact.Metadata["threadId"],
		"operation_id": artifact.Metadata["operationId"], "operation_body_digest": artifact.Metadata["operationBodyDigest"],
		"source_message_id": artifact.Metadata["sourceMessageId"], "source_window_digest": artifact.Metadata["sourceWindowDigest"],
		"session_digest": artifact.Metadata[openAIToolSessionDigestMetadataKey],
	})
}

func initializeOpenAIToolProductBaseAuthority(ctx context.Context, app *kanbanBoardApp, artifact meetingMemoryEntry) (meetingMemoryEntry, error) {
	if app == nil || app.openAIToolRuntime == nil || app.openAIToolRuntime.Carrier == nil || app.openAIToolRuntime.Carrier.Journal == nil {
		return meetingMemoryEntry{}, errOpenAIToolCarrierUnavailable
	}
	if strings.TrimSpace(artifact.Metadata[openAIToolBaseRevisionMetadataKey]) != "" {
		if err := verifyOpenAIToolProductBaseAuthority(ctx, app.openAIToolRuntime.Carrier.Journal, artifact); err != nil {
			return meetingMemoryEntry{}, err
		}
		return artifact, nil
	}
	header := artifactAuthorizationHeaderFromEntry(artifact)
	revision, err := openAIToolStableArtifactAuthorityDigest(header)
	if err != nil {
		return meetingMemoryEntry{}, err
	}
	postimage, err := openAIToolProductSemanticPostimageDigest(artifact, "")
	if err != nil {
		return meetingMemoryEntry{}, err
	}
	material, err := openAIToolProductBaseAuthorityMaterial(artifact, revision, postimage)
	if err != nil {
		return meetingMemoryEntry{}, err
	}
	keyID, keyVersion, authentication, err := app.openAIToolRuntime.Carrier.Journal.signOpenAIToolProductEffectReceipt(ctx, material)
	if err != nil {
		return meetingMemoryEntry{}, err
	}
	updated, changed, err := app.memory.updateOSArtifactMetadataIfHeaderAndMetadataMatch(header, map[string]string{
		openAIToolBaseRevisionMetadataKey: artifact.Metadata[openAIToolBaseRevisionMetadataKey],
	}, artifact.ID, map[string]string{
		openAIToolBaseRevisionMetadataKey: revision, openAIToolBasePostimageMetadataKey: postimage,
		openAIToolBaseKeyIDMetadataKey: keyID, openAIToolBaseKeyVersionMetadataKey: keyVersion, openAIToolBaseAuthenticationMetadataKey: authentication,
	})
	if err != nil || !changed {
		if current, ok := app.osArtifactByID(artifact.ID); ok {
			return current, nil
		}
		return meetingMemoryEntry{}, errors.New("OpenAI tool base artifact authority did not persist")
	}
	return updated, nil
}

func verifyOpenAIToolProductBaseAuthority(ctx context.Context, auth openAIToolProductEffectAuthenticator, artifact meetingMemoryEntry) error {
	if auth == nil {
		return errOpenAIToolCarrierUnavailable
	}
	revision := strings.TrimSpace(artifact.Metadata[openAIToolBaseRevisionMetadataKey])
	postimage := strings.TrimSpace(artifact.Metadata[openAIToolBasePostimageMetadataKey])
	material, err := openAIToolProductBaseAuthorityMaterial(artifact, revision, postimage)
	if err != nil || !isHexDigest(revision) || !isHexDigest(postimage) || !isHexDigest(strings.TrimSpace(artifact.Metadata[openAIToolBaseAuthenticationMetadataKey])) {
		return ErrStrideE10TenantAuthorityStale
	}
	return auth.verifyOpenAIToolProductEffectReceipt(ctx, artifact.Metadata[openAIToolBaseKeyIDMetadataKey], artifact.Metadata[openAIToolBaseKeyVersionMetadataKey], material, artifact.Metadata[openAIToolBaseAuthenticationMetadataKey])
}

func newOpenAIToolProductBackend(ctx context.Context, app *kanbanBoardApp, job AgentJob, journal *openAIToolJournal) (*openAIToolProductBackend, openAIToolAuthorityExpectation, error) {
	if app == nil || app.memory == nil || journal == nil || strings.TrimSpace(job.ArtifactID) == "" || strings.TrimSpace(job.ThreadID) == "" || normalizeAccountEmail(job.RequestedBy) == "" {
		return nil, openAIToolAuthorityExpectation{}, errOpenAIToolCarrierUnavailable
	}
	artifact, ok := app.osArtifactByID(job.ArtifactID)
	if !ok || artifact.ID != job.thread.Artifact.ID {
		return nil, openAIToolAuthorityExpectation{}, errors.New("OpenAI tool work artifact is unavailable")
	}
	sessionHash := strings.TrimSpace(artifact.Metadata[openAIToolSessionDigestMetadataKey])
	contextSessionHash := strideE10TenantSessionHashFromContext(ctx)
	if !validStrideE10SessionHash(sessionHash) || contextSessionHash != "" && sessionHash != contextSessionHash {
		return nil, openAIToolAuthorityExpectation{}, errors.New("OpenAI tool work lacks its server-derived session binding")
	}
	if err := verifyOpenAIToolProductBaseAuthority(ctx, journal, artifact); err != nil {
		return nil, openAIToolAuthorityExpectation{}, ErrStrideE10TenantAuthorityStale
	}
	backend := &openAIToolProductBackend{app: app, job: job, sessionHash: sessionHash, effectAuth: journal, journal: journal}
	var expectation openAIToolAuthorityExpectation
	err := backend.withCurrentTenantAuthority(ctx, func(snapshot StrideE10TenantAuthoritySnapshot, principal StrideE10TenantPrincipal) error {
		if snapshot.Organization.Validate() != nil || snapshot.Organization.Status != "active" || snapshot.Organization.Header.ID != principal.ActiveOrganizationID {
			return ErrStrideE10TenantAuthorityStale
		}
		if sha256Hex([]byte(normalizeAccountEmail(job.RequestedBy))) != snapshot.Session.AccountSubjectDigest {
			return ErrStrideE10TenantAuthorityStale
		}
		header, found := app.memory.artifactAuthorizationHeaderByID(job.ArtifactID)
		if !found || header.TenantID != principal.TenantID || header.ObjectID != job.ArtifactID {
			return ErrStrideE10TenantAuthorityStale
		}
		artifactRevision := strings.TrimSpace(artifact.Metadata[openAIToolBaseRevisionMetadataKey])
		basePostimage := strings.TrimSpace(artifact.Metadata[openAIToolBasePostimageMetadataKey])
		if !isHexDigest(artifactRevision) || !isHexDigest(basePostimage) {
			return ErrStrideE10TenantAuthorityStale
		}
		expectation = openAIToolAuthorityExpectation{
			TenantID: principal.TenantID, PersonID: principal.PersonID, RequesterAccount: normalizeAccountEmail(job.RequestedBy),
			SessionDigest: sessionHash, ActiveOrgSessionID: snapshot.ActiveSession.Header.ID, ActiveOrgSessionRev: uint64(principal.ActiveOrganizationSessionRev),
			MembershipID: principal.OrganizationMembershipID, ActiveOrganizationID: principal.ActiveOrganizationID,
			MembershipRevision: uint64(principal.OrganizationMembershipRev), OrganizationRevision: uint64(snapshot.Organization.Header.Revision),
			ThreadID: job.ThreadID, ArtifactID: job.ArtifactID, ArtifactRevision: artifactRevision,
			SourceWindowDigest: strings.TrimSpace(artifact.Metadata["sourceWindowDigest"]), JobAuthority: normalizeCodexJobAuthority(job.Authority),
			RequestPolicyRevision: openAIToolProductRequestPolicyRevision, PolicyRevision: openAIToolProductRequestPolicyRevision,
		}
		if err := expectation.validate(); err != nil {
			return err
		}
		backend.expectation = expectation
		return backend.validateCurrentProductBinding(openAIToolProductTenantContext(ctx, principal), expectation)
	})
	if err != nil {
		return nil, openAIToolAuthorityExpectation{}, err
	}
	backend.expectation = expectation
	return backend, expectation, nil
}

func (backend *openAIToolProductBackend) withCurrentTenantAuthority(ctx context.Context, use func(StrideE10TenantAuthoritySnapshot, StrideE10TenantPrincipal) error) error {
	converter := currentStrideE10TenantRuntimeConverter()
	if backend == nil || converter == nil || converter.gate == nil || !converter.gate.Enabled() || converter.mode != StrideE10TenantConversionCutover || converter.resolver == nil || converter.legacyIDs == nil || use == nil {
		return ErrStrideE10TenantAuthorityStale
	}
	if fence := strideE10HeldTenantAuthorityFromContext(ctx); fence != nil && fence.converter == converter && fence.snapshot.SessionHash == backend.sessionHash {
		principal, principalErr := converter.principalFromSnapshot(fence.snapshot, backend.sessionHash, StrideE10TenantSurfaceScout)
		if principalErr != nil || principal != fence.principal {
			return ErrStrideE10TenantAuthorityStale
		}
		var callbackErr error
		err := converter.legacyIDs.WithMappedLegacyPerson(ctx, fence.snapshot.Legacy.AccountSubjectDigest, func(mappedPersonID string) error {
			receipt := strideE10TenantComparisonReceipt(converter.receiptKey, StrideE10TenantSurfaceScout, principal, fence.snapshot.Legacy, mappedPersonID)
			if receipt.ValidateWithKey(converter.receiptKey) != nil || !receipt.Matches {
				return ErrStrideE10TenantAuthorityStale
			}
			callbackErr = use(fence.snapshot, principal)
			return callbackErr
		})
		if callbackErr != nil {
			return callbackErr
		}
		if err != nil {
			return ErrStrideE10TenantAuthorityStale
		}
		return nil
	}
	var callbackErr error
	err := converter.resolver.WithCurrentTenantAuthority(ctx, StrideE10TenantSurfaceScout, backend.sessionHash, func(snapshot StrideE10TenantAuthoritySnapshot) error {
		principal, principalErr := converter.principalFromSnapshot(snapshot, backend.sessionHash, StrideE10TenantSurfaceScout)
		if principalErr != nil {
			return principalErr
		}
		return converter.legacyIDs.WithMappedLegacyPerson(ctx, snapshot.Legacy.AccountSubjectDigest, func(mappedPersonID string) error {
			receipt := strideE10TenantComparisonReceipt(converter.receiptKey, StrideE10TenantSurfaceScout, principal, snapshot.Legacy, mappedPersonID)
			if receipt.ValidateWithKey(converter.receiptKey) != nil || !receipt.Matches {
				return ErrStrideE10TenantAuthorityStale
			}
			callbackErr = use(snapshot, principal)
			return callbackErr
		})
	})
	if callbackErr != nil {
		return callbackErr
	}
	if err != nil {
		return ErrStrideE10TenantAuthorityStale
	}
	return nil
}

type openAIToolProductAuthorityLease struct{ backend *openAIToolProductBackend }

func (lease *openAIToolProductAuthorityLease) WithCurrentOpenAIToolAuthority(ctx context.Context, expectation openAIToolAuthorityExpectation, use func(context.Context, openAIToolCurrentAuthority) error) error {
	if lease == nil || lease.backend == nil || use == nil || !openAIToolSameRunExpectation(lease.backend.expectation, expectation) {
		return ErrStrideE10TenantAuthorityStale
	}
	return lease.backend.withCurrentTenantAuthority(ctx, func(snapshot StrideE10TenantAuthoritySnapshot, principal StrideE10TenantPrincipal) error {
		if !openAIToolTenantSnapshotMatchesExpectation(snapshot, principal, expectation) {
			return ErrStrideE10TenantAuthorityStale
		}
		boundContext := openAIToolProductTenantContext(ctx, principal)
		if converter := currentStrideE10TenantRuntimeConverter(); converter != nil && strideE10HeldTenantAuthorityFromContext(boundContext) == nil {
			if resolver, ok := converter.resolver.(*strideE10MainTenantAuthorityResolver); ok {
				now := time.Now().UTC()
				if resolver.now != nil {
					now = resolver.now().UTC()
				}
				fence := &strideE10HeldTenantAuthorityFence{
					resolver: resolver, converter: converter, principal: principal,
					accountSubjectDigest: snapshot.Session.AccountSubjectDigest, snapshot: snapshot, now: now,
				}
				fence.active.Store(true)
				defer fence.active.Store(false)
				boundContext = strideE10ContextWithHeldTenantAuthority(boundContext, fence)
			}
		}
		if err := lease.backend.validateCurrentProductBinding(boundContext, expectation); err != nil {
			return err
		}
		return use(boundContext, &openAIToolProductCurrentAuthority{backend: lease.backend})
	})
}

func openAIToolProductTenantContext(ctx context.Context, principal StrideE10TenantPrincipal) context.Context {
	bound := context.WithValue(ctx, strideE10TenantSurfaceContextKey{}, StrideE10TenantSurfaceScout)
	return context.WithValue(bound, strideE10TenantPrincipalContextKey{}, principal)
}

type openAIToolProductCurrentAuthority struct{ backend *openAIToolProductBackend }

func (current *openAIToolProductCurrentAuthority) AuthorizeOpenAITool(ctx context.Context, expectation openAIToolAuthorityExpectation, entry openAIToolManifestEntry, arguments map[string]any) (string, error) {
	if current == nil || current.backend == nil {
		return "", ErrStrideE10TenantAuthorityStale
	}
	return current.backend.authorizePreimage(ctx, expectation, entry, arguments)
}

func (current *openAIToolProductCurrentAuthority) SnapshotOpenAIToolProviderAdmission(ctx context.Context) (string, error) {
	if current == nil || current.backend == nil || ctx.Err() != nil {
		return "", ErrStrideE10TenantAuthorityStale
	}
	return current.backend.productStoreGeneration()
}

func (current *openAIToolProductCurrentAuthority) WithOpenAIToolProviderAdmission(ctx context.Context, expectedGeneration string, use func(context.Context) error) error {
	if current == nil || current.backend == nil || current.backend.app == nil || current.backend.app.memory == nil || use == nil || strings.TrimSpace(expectedGeneration) == "" {
		return ErrStrideE10TenantAuthorityStale
	}
	store := current.backend.app.memory
	store.mu.Lock()
	defer store.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	generation, err := openAIToolProductStoreGenerationLocked(store)
	if err != nil || generation != expectedGeneration {
		return errors.New("OpenAI tool provider source generation changed before admission")
	}
	return use(ctx)
}

func openAIToolTenantSnapshotMatchesExpectation(snapshot StrideE10TenantAuthoritySnapshot, principal StrideE10TenantPrincipal, expectation openAIToolAuthorityExpectation) bool {
	return principal.TenantID == expectation.TenantID && principal.PersonID == expectation.PersonID &&
		principal.ActiveOrganizationID == expectation.ActiveOrganizationID && principal.OrganizationMembershipID == expectation.MembershipID &&
		uint64(principal.OrganizationMembershipRev) == expectation.MembershipRevision && uint64(principal.ActiveOrganizationSessionRev) == expectation.ActiveOrgSessionRev &&
		uint64(snapshot.Organization.Header.Revision) == expectation.OrganizationRevision && snapshot.ActiveSession.Header.ID == expectation.ActiveOrgSessionID &&
		snapshot.SessionHash == expectation.SessionDigest && snapshot.Organization.Status == "active"
}

func (backend *openAIToolProductBackend) validateCurrentProductBinding(ctx context.Context, expectation openAIToolAuthorityExpectation) error {
	if backend == nil || backend.app == nil || backend.app.memory == nil || !openAIToolSameRunExpectation(backend.expectation, expectation) {
		return ErrStrideE10TenantAuthorityStale
	}
	artifact, ok := backend.app.osArtifactByID(expectation.ArtifactID)
	if !ok || strings.TrimSpace(artifact.Metadata["threadId"]) != expectation.ThreadID || normalizeAccountEmail(artifact.Metadata["requestedBy"]) != expectation.RequesterAccount ||
		strings.TrimSpace(artifact.Metadata["sourceWindowDigest"]) != expectation.SourceWindowDigest || strings.TrimSpace(artifact.Metadata[openAIToolSessionDigestMetadataKey]) != expectation.SessionDigest {
		return ErrStrideE10TenantAuthorityStale
	}
	header, ok := backend.app.memory.artifactAuthorizationHeaderByID(expectation.ArtifactID)
	if !ok || header.TenantID != expectation.TenantID || header.ObjectID != expectation.ArtifactID {
		return ErrStrideE10TenantAuthorityStale
	}
	principal, canonical := strideE10TenantPrincipalFromContext(ctx)
	user, userOK := authenticatedRequester(expectation.RequesterAccount)
	if !canonical || !userOK || principal.TenantID != expectation.TenantID || principal.PersonID != expectation.PersonID || !artifactHeaderAuthorized(ctx, user, ACLReadContent, header) || !artifactHeaderAuthorized(ctx, user, ACLWrite, header) {
		return ErrStrideE10TenantAuthorityStale
	}
	if verifyOpenAIToolProductBaseAuthority(ctx, backend.effectAuth, artifact) != nil || strings.TrimSpace(artifact.Metadata[openAIToolBaseRevisionMetadataKey]) != expectation.ArtifactRevision {
		return ErrStrideE10TenantAuthorityStale
	}
	currentRevision, revisionErr := openAIToolStableArtifactAuthorityDigest(header)
	currentPostimage, postimageErr := openAIToolProductSemanticPostimageDigest(artifact, "")
	basePostimage := strings.TrimSpace(artifact.Metadata[openAIToolBasePostimageMetadataKey])
	runAuthorityDigest, runDigestErr := openAIToolCanonicalDigestOnly(openAIToolRunBaseExpectation(expectation))
	receipts, receiptErr := openAIToolProductReceipts(artifact)
	if receiptErr == nil {
		for _, receipt := range receipts {
			if receipt.ArtifactID != artifact.ID || receipt.RunAuthorityDigest != runAuthorityDigest || backend.verifyOpenAIToolProductEffectReceipt(ctx, receipt) != nil {
				receiptErr = ErrStrideE10TenantAuthorityStale
				break
			}
		}
	}
	if receiptErr == nil {
		receiptErr = backend.validateOpenAIToolProductProjectionSet(ctx, artifact, receipts)
	}
	if revisionErr != nil || postimageErr != nil || runDigestErr != nil || receiptErr != nil ||
		(currentRevision != expectation.ArtifactRevision || currentPostimage != basePostimage) && !backend.openAIToolAuthenticatedPostimageReachable(ctx, artifact, basePostimage, currentPostimage, runAuthorityDigest) {
		return ErrStrideE10TenantAuthorityStale
	}
	thread, _, err := backend.app.scoutChatThreadByID(expectation.RequesterAccount, strings.TrimSpace(artifact.Metadata["originId"]))
	if err != nil || thread.ID != strings.TrimSpace(artifact.Metadata["originId"]) || thread.ArchivedAt != "" || scoutChatThreadVisibility(thread) != scoutChatVisibilityPrivate {
		return ErrStrideE10TenantAuthorityStale
	}
	window, binding, err := scoutChatSourceWindow(thread, strings.TrimSpace(artifact.Metadata["sourceMessageId"]))
	if err != nil || binding.MessageDigest != strings.TrimSpace(artifact.Metadata["sourceMessageDigest"]) || binding.WindowDigest != expectation.SourceWindowDigest {
		return ErrStrideE10TenantAuthorityStale
	}
	operation := strings.TrimSpace(artifact.Metadata["operationId"])
	operationDigest := strings.TrimSpace(artifact.Metadata["operationBodyDigest"])
	sourceMessageID := strings.TrimSpace(artifact.Metadata["sourceMessageId"])
	sourceOperationBound := false
	for _, message := range window {
		if message.ID == sourceMessageID && message.SourceOperationID == operation && message.SourceOperationDigest == operationDigest {
			sourceOperationBound = true
			break
		}
	}
	projectionBound := false
	for _, message := range thread.Messages {
		if message.CausedByMessageID == sourceMessageID && message.IntentOutcome == string(conversationIntentStartPrivateWork) && message.Thread != nil &&
			message.Thread.ID == expectation.ThreadID && message.Thread.ArtifactID == expectation.ArtifactID {
			projectionBound = true
			break
		}
	}
	if operation == "" || operationDigest == "" || !sourceOperationBound || !projectionBound {
		return ErrStrideE10TenantAuthorityStale
	}
	return ctx.Err()
}

func (backend *openAIToolProductBackend) openAIToolAuthenticatedPostimageReachable(ctx context.Context, artifact meetingMemoryEntry, basePostimage, currentPostimage, runAuthorityDigest string) bool {
	if !isHexDigest(basePostimage) || !isHexDigest(currentPostimage) || !isHexDigest(runAuthorityDigest) {
		return false
	}
	if basePostimage == currentPostimage {
		return true
	}
	receipts, err := openAIToolProductReceipts(artifact)
	if err != nil {
		return false
	}
	ordered, err := backend.orderedOpenAIToolProductReceipts(ctx, receipts, artifact.ID)
	if err != nil {
		return false
	}
	cursor := basePostimage
	for index, receipt := range ordered {
		if receipt.RunAuthorityDigest != runAuthorityDigest || receipt.PriorPostimage != cursor || receipt.ArtifactSequence != uint64(index+1) {
			return false
		}
		cursor = receipt.PostimageDigest
	}
	if cursor == currentPostimage {
		return true
	}
	finalReceipt, err := openAIToolProductFinalReceiptFromArtifact(artifact)
	return err == nil && finalReceipt.RunAuthorityDigest == runAuthorityDigest && finalReceipt.PriorPostimage == cursor && finalReceipt.PostimageDigest == currentPostimage && backend.openAIToolFinalReceiptFollowsReceipts(finalReceipt, ordered) && backend.verifyOpenAIToolProductFinalReceipt(ctx, finalReceipt) == nil
}

func openAIToolStableArtifactAuthorityDigest(header ArtifactAuthorizationHeader) (string, error) {
	assets := make([]string, 0, len(header.AssetRefs))
	for ref := range header.AssetRefs {
		assets = append(assets, ref)
	}
	sort.Strings(assets)
	return openAIToolCanonicalDigestOnly(map[string]any{
		"tenant_id": header.TenantID, "object_id": header.ObjectID, "acl_version": header.ACLVersion,
		"content_revision": header.ContentRevision, "content_digest": header.ContentDigest,
		"visibility": header.Visibility, "owner": header.OwnerEmail, "origin": header.OriginSurface,
		"room_id": header.RoomID, "sitting_id": header.SittingID, "media_generation": header.MediaGeneration, "asset_refs": assets,
	})
}

func (backend *openAIToolProductBackend) authorizePreimage(ctx context.Context, expectation openAIToolAuthorityExpectation, entry openAIToolManifestEntry, arguments map[string]any) (string, error) {
	if err := backend.validateCurrentProductBinding(ctx, expectation); err != nil {
		return "", err
	}
	wantEntry, admitted := backendManifestEntry(entry.Name)
	if !admitted || !entry.Admitted || entry.SchemaSHA256 != wantEntry.SchemaSHA256 || entry.PolicyRevision != wantEntry.PolicyRevision || entry.Authority != wantEntry.Authority || entry.Effect != wantEntry.Effect {
		return "", errors.New("OpenAI tool product authority rejected manifest drift")
	}
	if expectation.ToolName != entry.Name || expectation.ManifestDigest != openAIToolManifestV1SHA256 || expectation.SchemaDigest != entry.SchemaSHA256 || expectation.PolicyRevision != entry.PolicyRevision {
		return "", errors.New("OpenAI tool product authority rejected operation binding drift")
	}
	argumentsDigest, _, err := openAIToolCanonicalDigest(arguments)
	if err != nil || argumentsDigest != expectation.ArgumentsDigest {
		return "", errors.New("OpenAI tool product authority rejected argument drift")
	}
	switch entry.Name {
	case controlToolReportGoalState, "update_artifact":
		artifact, ok := backend.app.osArtifactByID(expectation.ArtifactID)
		if !ok {
			return "", errors.New("OpenAI tool artifact is unavailable")
		}
		if entry.Name == "update_artifact" && strings.TrimSpace(asString(arguments["artifact_id"])) != artifact.ID {
			return "", errors.New("OpenAI tool update target changed")
		}
		return openAIToolArtifactPostimageDigest(artifact)
	case "create_artifact":
		return backend.artifactCollectionGeneration()
	case "answer_memory_question":
		window, err := backend.memoryWindow(ctx, strings.TrimSpace(asString(arguments["query"])))
		if err != nil {
			return "", err
		}
		return window.PreimageDigest, nil
	default:
		return "", errors.New("OpenAI tool product authority rejected an unadmitted tool")
	}
}

func backendManifestEntry(name string) (openAIToolManifestEntry, bool) {
	manifest, err := buildOpenAIToolManifest()
	if err != nil {
		return openAIToolManifestEntry{}, false
	}
	return manifest.admitted(name)
}

type openAIToolProductEffectReceipt struct {
	OperationID           string          `json:"operation_id"`
	ArtifactSequence      uint64          `json:"artifact_sequence"`
	PriorOperationID      string          `json:"prior_operation_id,omitempty"`
	PriorReceiptDigest    string          `json:"prior_receipt_digest,omitempty"`
	ToolName              string          `json:"tool_name"`
	ArtifactID            string          `json:"artifact_id"`
	ExpectationDigest     string          `json:"expectation_digest"`
	RunAuthorityDigest    string          `json:"run_authority_digest"`
	PreimageDigest        string          `json:"preimage_digest"`
	PriorPostimage        string          `json:"prior_postimage_digest,omitempty"`
	PostimageDigest       string          `json:"postimage_digest"`
	ReconciliationHash    string          `json:"reconciliation_digest"`
	FunctionOutput        json.RawMessage `json:"function_output"`
	ProjectionDigest      string          `json:"projection_digest,omitempty"`
	ProjectionEventAt     string          `json:"projection_event_at,omitempty"`
	ProjectionCardID      string          `json:"projection_card_id,omitempty"`
	EffectKeyID           string          `json:"effect_key_id"`
	EffectKeyVersion      string          `json:"effect_key_version"`
	ReceiptAuthentication string          `json:"receipt_authentication"`
}

func openAIToolProductReceipts(entry meetingMemoryEntry) (map[string]openAIToolProductEffectReceipt, error) {
	result := map[string]openAIToolProductEffectReceipt{}
	raw := strings.TrimSpace(entry.Metadata[openAIToolOperationReceiptsMetadataKey])
	if raw == "" {
		return result, nil
	}
	decoder := json.NewDecoder(bytes.NewReader([]byte(raw)))
	decoder.UseNumber()
	value, err := decodeUniqueJSONValue(decoder)
	if err != nil || ensureJSONEOF(decoder) != nil {
		return nil, errors.New("OpenAI tool artifact effect receipts are malformed")
	}
	object, ok := value.(map[string]any)
	if !ok || len(object) > openAIToolMaxPersistedEffectReceipts {
		return nil, errors.New("OpenAI tool artifact effect receipts are not a bounded object")
	}
	for operationID, value := range object {
		encoded, marshalErr := json.Marshal(value)
		if marshalErr != nil {
			return nil, errors.New("OpenAI tool artifact effect receipt is malformed")
		}
		var receipt openAIToolProductEffectReceipt
		receiptDecoder := json.NewDecoder(bytes.NewReader(encoded))
		receiptDecoder.DisallowUnknownFields()
		if receiptDecoder.Decode(&receipt) != nil || ensureJSONEOF(receiptDecoder) != nil {
			return nil, errors.New("OpenAI tool artifact effect receipt failed strict validation")
		}
		projectionRequired := receipt.ToolName != "answer_memory_question"
		projectionValid := !projectionRequired && receipt.ProjectionDigest == "" && receipt.ProjectionEventAt == "" && receipt.ProjectionCardID == "" || projectionRequired && isHexDigest(receipt.ProjectionDigest) && strings.TrimSpace(receipt.ProjectionEventAt) != "" && strings.TrimSpace(receipt.ProjectionCardID) != ""
		if operationID != receipt.OperationID || receipt.ArtifactSequence == 0 || receipt.ArtifactSequence == 1 && (receipt.PriorOperationID != "" || receipt.PriorReceiptDigest != "") || receipt.ArtifactSequence > 1 && (strings.TrimSpace(receipt.PriorOperationID) == "" || !isHexDigest(receipt.PriorReceiptDigest)) || strings.TrimSpace(receipt.ToolName) == "" || !projectionValid || strings.TrimSpace(receipt.ArtifactID) == "" || !isHexDigest(receipt.ExpectationDigest) || !isHexDigest(receipt.RunAuthorityDigest) || !isHexDigest(receipt.PreimageDigest) || receipt.PriorPostimage != "" && !isHexDigest(receipt.PriorPostimage) || !isHexDigest(receipt.PostimageDigest) || !isHexDigest(receipt.ReconciliationHash) || !json.Valid(receipt.FunctionOutput) || strings.TrimSpace(receipt.EffectKeyID) == "" || strings.TrimSpace(receipt.EffectKeyVersion) == "" || !isHexDigest(receipt.ReceiptAuthentication) {
			return nil, errors.New("OpenAI tool artifact effect receipt failed strict validation")
		}
		result[operationID] = receipt
	}
	return result, nil
}

func encodeOpenAIToolProductReceipts(receipts map[string]openAIToolProductEffectReceipt) (string, error) {
	if len(receipts) > openAIToolMaxPersistedEffectReceipts {
		return "", errors.New("OpenAI tool artifact effect receipt capacity is exhausted")
	}
	raw, err := json.Marshal(receipts)
	return string(raw), err
}

type openAIToolProductProjectionReceipt struct {
	OperationID           string `json:"operation_id"`
	ArtifactID            string `json:"artifact_id"`
	ProjectionDigest      string `json:"projection_digest"`
	DeliveryState         string `json:"delivery_state"`
	EffectKeyID           string `json:"effect_key_id"`
	EffectKeyVersion      string `json:"effect_key_version"`
	ReceiptAuthentication string `json:"receipt_authentication"`
}

func openAIToolProductProjectionReceiptMaterial(receipt openAIToolProductProjectionReceipt) ([]byte, error) {
	receipt.EffectKeyID, receipt.EffectKeyVersion, receipt.ReceiptAuthentication = "", "", ""
	return canonicalJSON(struct {
		Domain  string                             `json:"domain"`
		Receipt openAIToolProductProjectionReceipt `json:"receipt"`
	}{Domain: "stride-openai-tool-product-projection-delivery-v1", Receipt: receipt})
}

func (backend *openAIToolProductBackend) verifyOpenAIToolProductProjectionReceipt(ctx context.Context, receipt openAIToolProductProjectionReceipt) error {
	if receipt.ArtifactID == "" || receipt.OperationID == "" || !isHexDigest(receipt.ProjectionDigest) || !oneOf(receipt.DeliveryState, openAIToolProjectionPending, openAIToolProjectionDelivered) || receipt.EffectKeyID == "" || receipt.EffectKeyVersion == "" || !isHexDigest(receipt.ReceiptAuthentication) {
		return ErrStrideE10TenantAuthorityStale
	}
	material, err := openAIToolProductProjectionReceiptMaterial(receipt)
	if err != nil {
		return err
	}
	return backend.effectAuth.verifyOpenAIToolProductEffectReceipt(ctx, receipt.EffectKeyID, receipt.EffectKeyVersion, material, receipt.ReceiptAuthentication)
}

func (backend *openAIToolProductBackend) newOpenAIToolProductProjectionReceipt(ctx context.Context, receipt openAIToolProductEffectReceipt, state string) (openAIToolProductProjectionReceipt, error) {
	projection := openAIToolProductProjectionReceipt{
		OperationID: receipt.OperationID, ArtifactID: receipt.ArtifactID,
		ProjectionDigest: receipt.ProjectionDigest, DeliveryState: state,
	}
	material, err := openAIToolProductProjectionReceiptMaterial(projection)
	if err != nil {
		return openAIToolProductProjectionReceipt{}, err
	}
	projection.EffectKeyID, projection.EffectKeyVersion, projection.ReceiptAuthentication, err = backend.effectAuth.signOpenAIToolProductEffectReceipt(ctx, material)
	if err != nil || backend.verifyOpenAIToolProductProjectionReceipt(ctx, projection) != nil {
		return openAIToolProductProjectionReceipt{}, ErrStrideE10TenantAuthorityStale
	}
	return projection, nil
}

func openAIToolProductProjectionReceipts(artifact meetingMemoryEntry) (map[string]openAIToolProductProjectionReceipt, error) {
	result := map[string]openAIToolProductProjectionReceipt{}
	raw := strings.TrimSpace(artifact.Metadata[openAIToolProjectionReceiptsMetadataKey])
	if raw == "" {
		return result, nil
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	value, err := decodeUniqueJSONValue(decoder)
	if err != nil || ensureJSONEOF(decoder) != nil {
		return nil, errors.New("OpenAI tool projection receipts are malformed")
	}
	object, ok := value.(map[string]any)
	if !ok || len(object) > openAIToolMaxPersistedEffectReceipts {
		return nil, errors.New("OpenAI tool projection receipts are not a bounded object")
	}
	for operationID, value := range object {
		encoded, marshalErr := json.Marshal(value)
		if marshalErr != nil {
			return nil, marshalErr
		}
		var receipt openAIToolProductProjectionReceipt
		strict := json.NewDecoder(bytes.NewReader(encoded))
		strict.DisallowUnknownFields()
		if strict.Decode(&receipt) != nil || ensureJSONEOF(strict) != nil || operationID != receipt.OperationID {
			return nil, errors.New("OpenAI tool projection receipt failed strict validation")
		}
		result[operationID] = receipt
	}
	return result, nil
}

func encodeOpenAIToolProductProjectionReceipts(receipts map[string]openAIToolProductProjectionReceipt) (string, error) {
	if len(receipts) > openAIToolMaxPersistedEffectReceipts {
		return "", errors.New("OpenAI tool projection receipt capacity is exhausted")
	}
	raw, err := json.Marshal(receipts)
	return string(raw), err
}

// validateOpenAIToolProductProjectionSet authenticates the entire projection
// set and makes it an exact one-to-one cross-link of every projection-bearing
// effect receipt. Pending is a durable outbox claim; delivered is an at-most-
// once server dispatch receipt. Unknown, extra, missing, or cross-artifact rows
// fail closed before provider admission, reconciliation, or finalization.
func (backend *openAIToolProductBackend) validateOpenAIToolProductProjectionSet(ctx context.Context, artifact meetingMemoryEntry, effects map[string]openAIToolProductEffectReceipt) error {
	projections, err := openAIToolProductProjectionReceipts(artifact)
	if err != nil {
		return err
	}
	required := 0
	for operationID, effect := range effects {
		if effect.ToolName == "answer_memory_question" {
			continue
		}
		required++
		projection, ok := projections[operationID]
		if !ok || projection.OperationID != effect.OperationID || projection.ArtifactID != artifact.ID || projection.ArtifactID != effect.ArtifactID || projection.ProjectionDigest != effect.ProjectionDigest || backend.verifyOpenAIToolProductProjectionReceipt(ctx, projection) != nil {
			return ErrStrideE10TenantAuthorityStale
		}
	}
	if len(projections) != required {
		return ErrStrideE10TenantAuthorityStale
	}
	return nil
}

func (backend *openAIToolProductBackend) ensureOpenAIToolProductProjection(ctx context.Context, request openAIToolEffectRequest, receipt openAIToolProductEffectReceipt) error {
	if request.Entry.Name == "answer_memory_question" {
		return nil
	}
	if backend.verifyOpenAIToolProductEffectReceipt(ctx, receipt) != nil || receipt.OperationID != request.OperationID || receipt.ProjectionDigest == "" {
		return ErrStrideE10TenantAuthorityStale
	}
	artifact, ok := backend.app.osArtifactByID(receipt.ArtifactID)
	if !ok {
		return errors.New("OpenAI tool projection artifact is unavailable")
	}
	originID := strings.TrimSpace(backend.job.thread.Artifact.Metadata["originId"])
	if request.Entry.Name == "create_artifact" {
		card := backend.app.scoutChatArtifactRefMessage(artifact)
		card.ID, card.CreatedAt, card.CausedByMessageID = receipt.ProjectionCardID, receipt.ProjectionEventAt, backend.job.thread.Artifact.Metadata["sourceMessageId"]
		card.Text = firstNonEmptyString(artifact.Metadata["title"], "Private artifact") + " — created in this work stream"
		if _, err := backend.app.commitScoutChatThreadArtifactRefWithContext(ctx, backend.expectation.RequesterAccount, originID, card); err != nil {
			return err
		}
	} else if err := backend.app.commitScoutChatThreadRefStatusWithContext(ctx, originID, backend.expectation.RequesterAccount, backend.expectation.ThreadID, "running", backend.expectation.ArtifactID); err != nil {
		return err
	}

	artifact, ok = backend.app.osArtifactByID(receipt.ArtifactID)
	if !ok {
		return errors.New("OpenAI tool projection artifact disappeared")
	}
	effects, err := openAIToolProductReceipts(artifact)
	if err != nil || backend.validateOpenAIToolProductProjectionSet(ctx, artifact, effects) != nil {
		return ErrStrideE10TenantAuthorityStale
	}
	projectionReceipts, err := openAIToolProductProjectionReceipts(artifact)
	if err != nil {
		return err
	}
	existing, found := projectionReceipts[request.OperationID]
	if !found || existing.ArtifactID != artifact.ID || existing.ProjectionDigest != receipt.ProjectionDigest || backend.verifyOpenAIToolProductProjectionReceipt(ctx, existing) != nil {
		return ErrStrideE10TenantAuthorityStale
	}
	if existing.DeliveryState == openAIToolProjectionDelivered {
		return nil
	}
	if openAIToolBeforeProjectionDispatchProbe != nil {
		if err := openAIToolBeforeProjectionDispatchProbe(receipt); err != nil {
			return err
		}
	}
	event := osEvent{
		Kind: osArtifactEventKind(artifact.Metadata), Ref: artifact.ID,
		Title:         firstNonEmptyString(strings.TrimSpace(artifact.Metadata["title"]), assistantToolLabel(firstNonEmptyString(artifact.Metadata["mode"], artifact.Kind))+" artifact"),
		OriginSurface: firstNonEmptyString(strings.TrimSpace(artifact.Metadata["originSurface"]), "chat:"+originID),
		Actor:         firstNonEmptyString(artifact.Metadata["updatedBy"], artifact.Metadata["createdBy"], scoutParticipantName), At: receipt.ProjectionEventAt,
	}
	dispatched, err := sendOSEventToUserWithContextIdempotent(ctx, backend.expectation.RequesterAccount, event, receipt.ProjectionDigest)
	if err != nil {
		return err
	}
	if dispatched && openAIToolProjectionEventProbe != nil {
		openAIToolProjectionEventProbe(receipt)
	}
	if openAIToolAfterProjectionEventProbe != nil {
		if err := openAIToolAfterProjectionEventProbe(receipt); err != nil {
			return err
		}
	}
	delivered, err := backend.newOpenAIToolProductProjectionReceipt(ctx, receipt, openAIToolProjectionDelivered)
	if err != nil {
		return err
	}
	projectionReceipts[request.OperationID] = delivered
	encoded, err := encodeOpenAIToolProductProjectionReceipts(projectionReceipts)
	if err != nil {
		return err
	}
	current, ok := backend.app.osArtifactByID(artifact.ID)
	if !ok {
		return errors.New("OpenAI tool projection artifact disappeared")
	}
	header := artifactAuthorizationHeaderFromEntry(current)
	semantic, semanticErr := openAIToolProductSemanticPostimageDigest(current, "")
	full, fullErr := openAIToolArtifactPostimageDigest(current)
	if semanticErr != nil || fullErr != nil {
		return errors.New("OpenAI tool projection preimage is unavailable")
	}
	updated, changed, err := backend.app.memory.updateOSArtifactWithMetadataIfHeaderAndToolPreimagesMatch(header, semantic, full, current.ID, "", current.Text, scoutParticipantName, map[string]string{openAIToolProjectionReceiptsMetadataKey: encoded})
	if err != nil || !changed || backend.validateOpenAIToolProductProjectionSet(ctx, updated, effects) != nil {
		return errors.New("OpenAI tool projection delivery receipt did not persist")
	}
	return nil
}

func openAIToolEffectCommitFromReceipt(receipt openAIToolProductEffectReceipt) openAIToolEffectCommit {
	return openAIToolEffectCommit{FunctionOutput: append(json.RawMessage(nil), receipt.FunctionOutput...), PostimageDigest: receipt.PostimageDigest, ReconciliationDigest: receipt.ReconciliationHash}
}

func openAIToolProductEffectReceiptMaterial(receipt openAIToolProductEffectReceipt) ([]byte, error) {
	receipt.ReceiptAuthentication = ""
	receipt.EffectKeyID = ""
	receipt.EffectKeyVersion = ""
	return canonicalJSON(receipt)
}

func openAIToolProductEffectReceiptDigest(receipt openAIToolProductEffectReceipt) (string, error) {
	return openAIToolCanonicalDigestOnly(receipt)
}

func (backend *openAIToolProductBackend) orderedOpenAIToolProductReceipts(ctx context.Context, receipts map[string]openAIToolProductEffectReceipt, artifactID string) ([]openAIToolProductEffectReceipt, error) {
	ordered := make([]openAIToolProductEffectReceipt, 0, len(receipts))
	for _, receipt := range receipts {
		if receipt.ArtifactID != artifactID || backend.verifyOpenAIToolProductEffectReceipt(ctx, receipt) != nil {
			return nil, ErrStrideE10TenantAuthorityStale
		}
		ordered = append(ordered, receipt)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].ArtifactSequence < ordered[j].ArtifactSequence })
	for index := range ordered {
		wantSequence := uint64(index + 1)
		if ordered[index].ArtifactSequence != wantSequence {
			return nil, errors.New("OpenAI tool artifact receipt sequence is not contiguous")
		}
		if index == 0 {
			if ordered[index].PriorOperationID != "" || ordered[index].PriorReceiptDigest != "" {
				return nil, errors.New("OpenAI tool artifact receipt chain has an invalid root")
			}
			continue
		}
		prior := ordered[index-1]
		priorDigest, err := openAIToolProductEffectReceiptDigest(prior)
		if err != nil || ordered[index].PriorOperationID != prior.OperationID || ordered[index].PriorReceiptDigest != priorDigest || ordered[index].PriorPostimage != prior.PostimageDigest {
			return nil, errors.New("OpenAI tool artifact receipt predecessor changed")
		}
	}
	return ordered, nil
}

func (backend *openAIToolProductBackend) verifyOpenAIToolProductEffectReceipt(ctx context.Context, receipt openAIToolProductEffectReceipt) error {
	if backend == nil || backend.effectAuth == nil {
		return errOpenAIToolCarrierUnavailable
	}
	wantReconciliation, err := openAIToolCanonicalDigestOnly(map[string]any{
		"domain": "stride-openai-tool-product-effect-v1", "operation_id": receipt.OperationID, "tool": receipt.ToolName,
		"artifact_sequence": receipt.ArtifactSequence, "prior_operation_id": receipt.PriorOperationID, "prior_receipt_digest": receipt.PriorReceiptDigest,
		"artifact_id": receipt.ArtifactID, "expectation": receipt.ExpectationDigest, "run_authority": receipt.RunAuthorityDigest, "preimage": receipt.PreimageDigest,
		"prior_postimage": receipt.PriorPostimage, "postimage": receipt.PostimageDigest, "output": json.RawMessage(receipt.FunctionOutput),
		"projection_digest": receipt.ProjectionDigest, "projection_event_at": receipt.ProjectionEventAt, "projection_card_id": receipt.ProjectionCardID,
	})
	if err != nil || wantReconciliation != receipt.ReconciliationHash {
		return errors.New("OpenAI tool product effect receipt reconciliation digest changed")
	}
	if receipt.ToolName != "answer_memory_question" {
		wantProjection, projectionErr := backend.openAIToolProductProjectionDigest(receipt.OperationID, receipt.ToolName, receipt.ArtifactID, receipt.ProjectionEventAt, receipt.ProjectionCardID)
		if projectionErr != nil || wantProjection != receipt.ProjectionDigest {
			return errors.New("OpenAI tool product projection receipt changed")
		}
	}
	material, err := openAIToolProductEffectReceiptMaterial(receipt)
	if err != nil {
		return err
	}
	return backend.effectAuth.verifyOpenAIToolProductEffectReceipt(ctx, receipt.EffectKeyID, receipt.EffectKeyVersion, material, receipt.ReceiptAuthentication)
}

func (backend *openAIToolProductBackend) openAIToolProductProjectionDigest(operationID, toolName, artifactID, eventAt, cardID string) (string, error) {
	return openAIToolCanonicalDigestOnly(map[string]any{
		"domain": "stride-openai-tool-product-projection-v1", "operation_id": strings.TrimSpace(operationID), "tool": strings.TrimSpace(toolName),
		"artifact_id": strings.TrimSpace(artifactID), "thread_id": backend.expectation.ThreadID, "requester": backend.expectation.RequesterAccount,
		"event_at": strings.TrimSpace(eventAt), "card_id": strings.TrimSpace(cardID),
	})
}

func (backend *openAIToolProductBackend) newOpenAIToolProductEffectReceipt(ctx context.Context, request openAIToolEffectRequest, artifactID, priorPostimage string, output json.RawMessage, postimage string, existing map[string]openAIToolProductEffectReceipt) (openAIToolProductEffectReceipt, error) {
	expectationDigest, err := openAIToolCanonicalDigestOnly(request.Expectation)
	if err != nil {
		return openAIToolProductEffectReceipt{}, err
	}
	runAuthorityDigest, err := openAIToolCanonicalDigestOnly(openAIToolRunBaseExpectation(request.Expectation))
	if err != nil {
		return openAIToolProductEffectReceipt{}, err
	}
	ordered, err := backend.orderedOpenAIToolProductReceipts(ctx, existing, artifactID)
	if err != nil {
		return openAIToolProductEffectReceipt{}, err
	}
	sequence := uint64(len(ordered) + 1)
	priorOperationID, priorReceiptDigest := "", ""
	if len(ordered) > 0 {
		prior := ordered[len(ordered)-1]
		if prior.PostimageDigest != priorPostimage {
			return openAIToolProductEffectReceipt{}, errors.New("OpenAI tool artifact receipt head does not match the current postimage")
		}
		priorOperationID = prior.OperationID
		priorReceiptDigest, err = openAIToolProductEffectReceiptDigest(prior)
		if err != nil {
			return openAIToolProductEffectReceipt{}, err
		}
	}
	projectionDigest, projectionEventAt, projectionCardID := "", "", ""
	if request.Entry.Name != "answer_memory_question" {
		projectionEventAt = time.Now().UTC().Format(time.RFC3339Nano)
		projectionCardID = "scout-chat-message-work-" + sha256Hex([]byte(backend.job.thread.Artifact.Metadata["sourceMessageId"] + "\x00" + backend.expectation.ThreadID))[:24]
		if request.Entry.Name == "create_artifact" {
			projectionCardID = "scout-chat-message-tool-artifact-" + sha256Hex([]byte(request.OperationID))[:24]
		}
		projectionDigest, err = backend.openAIToolProductProjectionDigest(request.OperationID, request.Entry.Name, artifactID, projectionEventAt, projectionCardID)
		if err != nil {
			return openAIToolProductEffectReceipt{}, err
		}
	}
	reconciliation, err := openAIToolCanonicalDigestOnly(map[string]any{
		"domain": "stride-openai-tool-product-effect-v1", "operation_id": request.OperationID, "tool": request.Entry.Name,
		"artifact_sequence": sequence, "prior_operation_id": priorOperationID, "prior_receipt_digest": priorReceiptDigest,
		"artifact_id": artifactID, "expectation": expectationDigest, "run_authority": runAuthorityDigest, "preimage": request.PreimageDigest,
		"prior_postimage": priorPostimage, "postimage": postimage, "output": json.RawMessage(output),
		"projection_digest": projectionDigest, "projection_event_at": projectionEventAt, "projection_card_id": projectionCardID,
	})
	if err != nil {
		return openAIToolProductEffectReceipt{}, err
	}
	receipt := openAIToolProductEffectReceipt{
		OperationID: request.OperationID, ArtifactSequence: sequence, PriorOperationID: priorOperationID, PriorReceiptDigest: priorReceiptDigest, ToolName: request.Entry.Name, ArtifactID: artifactID,
		ExpectationDigest: expectationDigest, RunAuthorityDigest: runAuthorityDigest, PreimageDigest: request.PreimageDigest, PriorPostimage: priorPostimage,
		PostimageDigest: postimage, ReconciliationHash: reconciliation, FunctionOutput: append(json.RawMessage(nil), output...),
		ProjectionDigest: projectionDigest, ProjectionEventAt: projectionEventAt, ProjectionCardID: projectionCardID,
	}
	material, err := openAIToolProductEffectReceiptMaterial(receipt)
	if err != nil {
		return openAIToolProductEffectReceipt{}, err
	}
	receipt.EffectKeyID, receipt.EffectKeyVersion, receipt.ReceiptAuthentication, err = backend.effectAuth.signOpenAIToolProductEffectReceipt(ctx, material)
	if err != nil {
		return openAIToolProductEffectReceipt{}, err
	}
	return receipt, backend.verifyOpenAIToolProductEffectReceipt(ctx, receipt)
}

type openAIToolProductFinalReceipt struct {
	RunDigest             string `json:"run_digest"`
	ArtifactID            string `json:"artifact_id"`
	RunAuthorityDigest    string `json:"run_authority_digest"`
	ArtifactSequence      uint64 `json:"artifact_sequence"`
	PriorOperationID      string `json:"prior_operation_id,omitempty"`
	PriorReceiptDigest    string `json:"prior_receipt_digest,omitempty"`
	PriorPostimage        string `json:"prior_postimage_digest"`
	PostimageDigest       string `json:"postimage_digest"`
	OperationProofDigest  string `json:"operation_proof_digest"`
	StoreGeneration       string `json:"store_generation"`
	FinalUseDigest        string `json:"final_use_digest"`
	FanOutDigest          string `json:"fan_out_digest"`
	EffectKeyID           string `json:"effect_key_id"`
	EffectKeyVersion      string `json:"effect_key_version"`
	ReceiptAuthentication string `json:"receipt_authentication"`
}

func openAIToolProductFinalReceiptMaterial(receipt openAIToolProductFinalReceipt) ([]byte, error) {
	receipt.EffectKeyID, receipt.EffectKeyVersion, receipt.ReceiptAuthentication = "", "", ""
	return canonicalJSON(struct {
		Domain  string                        `json:"domain"`
		Receipt openAIToolProductFinalReceipt `json:"receipt"`
	}{Domain: "stride-openai-tool-product-final-receipt-v1", Receipt: receipt})
}

func (backend *openAIToolProductBackend) newOpenAIToolProductFinalReceipt(ctx context.Context, expectation openAIToolAuthorityExpectation, runDigest, priorPostimage, postimage, operationProofDigest, storeGeneration, finalUseDigest, fanOutDigest string, receipts map[string]openAIToolProductEffectReceipt) (openAIToolProductFinalReceipt, error) {
	runAuthorityDigest, err := openAIToolCanonicalDigestOnly(openAIToolRunBaseExpectation(expectation))
	if err != nil {
		return openAIToolProductFinalReceipt{}, err
	}
	ordered, err := backend.orderedOpenAIToolProductReceipts(ctx, receipts, expectation.ArtifactID)
	if err != nil {
		return openAIToolProductFinalReceipt{}, err
	}
	sequence := uint64(len(ordered) + 1)
	priorOperationID, priorReceiptDigest := "", ""
	if len(ordered) > 0 {
		prior := ordered[len(ordered)-1]
		if prior.PostimageDigest != priorPostimage {
			return openAIToolProductFinalReceipt{}, errors.New("OpenAI tool final receipt predecessor postimage changed")
		}
		priorOperationID = prior.OperationID
		priorReceiptDigest, err = openAIToolProductEffectReceiptDigest(prior)
		if err != nil {
			return openAIToolProductFinalReceipt{}, err
		}
	}
	receipt := openAIToolProductFinalReceipt{
		RunDigest: runDigest, ArtifactID: expectation.ArtifactID, RunAuthorityDigest: runAuthorityDigest,
		ArtifactSequence: sequence, PriorOperationID: priorOperationID, PriorReceiptDigest: priorReceiptDigest,
		PriorPostimage: priorPostimage, PostimageDigest: postimage, OperationProofDigest: operationProofDigest, StoreGeneration: storeGeneration,
		FinalUseDigest: finalUseDigest, FanOutDigest: fanOutDigest,
	}
	material, err := openAIToolProductFinalReceiptMaterial(receipt)
	if err != nil {
		return openAIToolProductFinalReceipt{}, err
	}
	receipt.EffectKeyID, receipt.EffectKeyVersion, receipt.ReceiptAuthentication, err = backend.effectAuth.signOpenAIToolProductEffectReceipt(ctx, material)
	if err != nil {
		return openAIToolProductFinalReceipt{}, err
	}
	return receipt, backend.verifyOpenAIToolProductFinalReceipt(ctx, receipt)
}

func (backend *openAIToolProductBackend) verifyOpenAIToolProductFinalReceipt(ctx context.Context, receipt openAIToolProductFinalReceipt) error {
	if backend == nil || backend.effectAuth == nil || !isHexDigest(receipt.RunDigest) || receipt.ArtifactID != backend.expectation.ArtifactID || !isHexDigest(receipt.RunAuthorityDigest) || receipt.ArtifactSequence == 0 || receipt.ArtifactSequence == 1 && (receipt.PriorOperationID != "" || receipt.PriorReceiptDigest != "") || receipt.ArtifactSequence > 1 && (strings.TrimSpace(receipt.PriorOperationID) == "" || !isHexDigest(receipt.PriorReceiptDigest)) || !isHexDigest(receipt.PriorPostimage) || !isHexDigest(receipt.PostimageDigest) || !isHexDigest(receipt.OperationProofDigest) || !isHexDigest(receipt.StoreGeneration) || !isHexDigest(receipt.FinalUseDigest) || !isHexDigest(receipt.FanOutDigest) || strings.TrimSpace(receipt.EffectKeyID) == "" || strings.TrimSpace(receipt.EffectKeyVersion) == "" || !isHexDigest(receipt.ReceiptAuthentication) {
		return ErrStrideE10TenantAuthorityStale
	}
	material, err := openAIToolProductFinalReceiptMaterial(receipt)
	if err != nil {
		return err
	}
	return backend.effectAuth.verifyOpenAIToolProductEffectReceipt(ctx, receipt.EffectKeyID, receipt.EffectKeyVersion, material, receipt.ReceiptAuthentication)
}

func (backend *openAIToolProductBackend) openAIToolFinalReceiptFollowsReceipts(finalReceipt openAIToolProductFinalReceipt, ordered []openAIToolProductEffectReceipt) bool {
	if finalReceipt.ArtifactSequence != uint64(len(ordered)+1) {
		return false
	}
	if len(ordered) == 0 {
		return finalReceipt.PriorOperationID == "" && finalReceipt.PriorReceiptDigest == ""
	}
	prior := ordered[len(ordered)-1]
	digest, err := openAIToolProductEffectReceiptDigest(prior)
	return err == nil && finalReceipt.PriorOperationID == prior.OperationID && finalReceipt.PriorReceiptDigest == digest
}

func openAIToolProductFinalReceiptFromArtifact(artifact meetingMemoryEntry) (openAIToolProductFinalReceipt, error) {
	raw := strings.TrimSpace(artifact.Metadata[openAIToolFinalReceiptMetadataKey])
	if raw == "" {
		return openAIToolProductFinalReceipt{}, errors.New("OpenAI tool product final receipt is unavailable")
	}
	var receipt openAIToolProductFinalReceipt
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&receipt) != nil || ensureJSONEOF(decoder) != nil {
		return openAIToolProductFinalReceipt{}, errors.New("OpenAI tool product final receipt is malformed")
	}
	return receipt, nil
}

func (backend *openAIToolProductBackend) reconcileArtifactReceipt(ctx context.Context, request openAIToolEffectRequest, artifactID string) (openAIToolReconciliation, error) {
	artifact, ok := backend.app.osArtifactByID(artifactID)
	if !ok {
		return openAIToolReconciliation{Status: openAIToolReconciliationNotApplied}, nil
	}
	receipts, receiptErr := openAIToolProductReceipts(artifact)
	if receiptErr != nil || backend.validateOpenAIToolProductProjectionSet(ctx, artifact, receipts) != nil {
		return openAIToolReconciliation{Status: openAIToolReconciliationAmbiguous}, nil
	}
	receipt, found := receipts[request.OperationID]
	if !found {
		return openAIToolReconciliation{Status: openAIToolReconciliationNotApplied}, nil
	}
	expectationDigest, expectationErr := openAIToolCanonicalDigestOnly(request.Expectation)
	runAuthorityDigest, runDigestErr := openAIToolCanonicalDigestOnly(openAIToolRunBaseExpectation(request.Expectation))
	if expectationErr != nil || runDigestErr != nil || receipt.OperationID != request.OperationID || receipt.ToolName != request.Entry.Name || receipt.ArtifactID != artifactID || receipt.ExpectationDigest != expectationDigest || receipt.RunAuthorityDigest != runAuthorityDigest || receipt.PreimageDigest != request.PreimageDigest || !json.Valid(receipt.FunctionOutput) || backend.verifyOpenAIToolProductEffectReceipt(ctx, receipt) != nil {
		return openAIToolReconciliation{Status: openAIToolReconciliationAmbiguous}, nil
	}
	currentPostimage, postimageErr := openAIToolProductSemanticPostimageDigest(artifact, request.Entry.Name)
	if postimageErr != nil || !backend.openAIToolProductReceiptReachesCurrentPostimage(ctx, artifact, receipts, receipt, currentPostimage) {
		return openAIToolReconciliation{Status: openAIToolReconciliationAmbiguous}, nil
	}
	commit := openAIToolEffectCommitFromReceipt(receipt)
	if validateOpenAIToolEffectCommit(request.Entry.Name, commit) != nil {
		return openAIToolReconciliation{Status: openAIToolReconciliationAmbiguous}, nil
	}
	if err := backend.ensureOpenAIToolProductProjection(ctx, request, receipt); err != nil {
		return openAIToolReconciliation{Status: openAIToolReconciliationAmbiguous}, err
	}
	return openAIToolReconciliation{Status: openAIToolReconciliationCommitted, Commit: commit}, nil
}

func (backend *openAIToolProductBackend) openAIToolProductReceiptReachesCurrentPostimage(ctx context.Context, artifact meetingMemoryEntry, receipts map[string]openAIToolProductEffectReceipt, start openAIToolProductEffectReceipt, currentPostimage string) bool {
	ordered, err := backend.orderedOpenAIToolProductReceipts(ctx, receipts, artifact.ID)
	if err != nil || start.ArtifactID != artifact.ID {
		return false
	}
	startIndex := -1
	for index, receipt := range ordered {
		if receipt.OperationID == start.OperationID {
			startIndex = index
		}
		if receipt.RunAuthorityDigest != start.RunAuthorityDigest {
			return false
		}
	}
	orderedStartDigest, orderedDigestErr := openAIToolProductEffectReceiptDigest(ordered[max(startIndex, 0)])
	startDigest, startDigestErr := openAIToolProductEffectReceiptDigest(start)
	if startIndex < 0 || orderedDigestErr != nil || startDigestErr != nil || orderedStartDigest != startDigest {
		return false
	}
	head := ordered[len(ordered)-1]
	if head.PostimageDigest == currentPostimage {
		return true
	}
	finalReceipt, err := openAIToolProductFinalReceiptFromArtifact(artifact)
	return err == nil && finalReceipt.RunAuthorityDigest == start.RunAuthorityDigest && finalReceipt.PriorPostimage == head.PostimageDigest && finalReceipt.PostimageDigest == currentPostimage && backend.openAIToolFinalReceiptFollowsReceipts(finalReceipt, ordered) && backend.verifyOpenAIToolProductFinalReceipt(ctx, finalReceipt) == nil
}

func (backend *openAIToolProductBackend) ReconcileGoalState(ctx context.Context, request openAIToolEffectRequest) (openAIToolReconciliation, error) {
	reconciliation, err := backend.reconcileArtifactReceipt(ctx, request, request.Expectation.ArtifactID)
	if err != nil || reconciliation.Status != openAIToolReconciliationCommitted {
		return reconciliation, err
	}
	if artifact, ok := backend.app.osArtifactByID(request.Expectation.ArtifactID); ok && strings.TrimSpace(artifact.Metadata[openAIToolFinalReceiptMetadataKey]) != "" {
		return reconciliation, nil
	}
	if err := backend.ensureGoalProgressProjection(ctx, request.Expectation); err != nil {
		return openAIToolReconciliation{Status: openAIToolReconciliationAmbiguous}, nil
	}
	return reconciliation, nil
}

func (backend *openAIToolProductBackend) ApplyGoalState(ctx context.Context, request openAIToolEffectRequest) (openAIToolEffectCommit, error) {
	if err := backend.validateCurrentProductBinding(ctx, request.Expectation); err != nil {
		return openAIToolEffectCommit{}, err
	}
	artifact, header, err := backend.currentArtifactForWrite(request)
	if err != nil {
		return openAIToolEffectCommit{}, err
	}
	status := firstNonEmptyString(strings.TrimSpace(asString(request.Arguments["goal_status"])), strings.TrimSpace(artifact.Metadata["goalStatus"]), "running")
	stage := firstNonEmptyString(strings.TrimSpace(asString(request.Arguments["stage"])), strings.TrimSpace(artifact.Metadata["currentStage"]), "execute_in_order")
	review := firstNonEmptyString(strings.TrimSpace(asString(request.Arguments["review_gate"])), strings.TrimSpace(artifact.Metadata["reviewGate"]), "pending")
	progress := artifactProgressPercent(artifact)
	if candidate, ok := asOptionalInt(request.Arguments["progress_percent"]); ok {
		if candidate < progress || candidate < 0 || candidate > 100 {
			return openAIToolEffectCommit{}, errors.New("OpenAI tool goal progress is not monotonic")
		}
		progress = candidate
	}
	if !validOpenAIToolGoalTransition(strings.TrimSpace(artifact.Metadata["goalStatus"]), status) {
		return openAIToolEffectCommit{}, errors.New("OpenAI tool goal status transition is not monotonic")
	}
	if !validOpenAIToolReviewGateTransition(strings.TrimSpace(artifact.Metadata["reviewGate"]), review) {
		return openAIToolEffectCommit{}, errors.New("OpenAI tool review gate is sticky and cannot be cleared by the control tool")
	}
	output, err := json.Marshal(map[string]any{"goal_status": status, "stage": stage, "receipt": request.OperationID})
	if err != nil {
		return openAIToolEffectCommit{}, err
	}
	target := cloneMemoryEntry(artifact)
	if target.Metadata == nil {
		target.Metadata = map[string]string{}
	}
	target.Metadata["goalStatus"], target.Metadata["currentStage"], target.Metadata["reviewGate"] = status, stage, review
	target.Metadata["progressPercent"], target.Metadata["progressNote"] = strconv.Itoa(progress), strings.TrimSpace(asString(request.Arguments["note"]))
	priorPostimage, err := openAIToolProductSemanticPostimageDigest(artifact, request.Entry.Name)
	if err != nil {
		return openAIToolEffectCommit{}, err
	}
	postimage, err := openAIToolProductSemanticPostimageDigest(target, request.Entry.Name)
	if err != nil {
		return openAIToolEffectCommit{}, err
	}
	if postimage == priorPostimage {
		return openAIToolEffectCommit{}, errors.New("OpenAI tool goal-state update is a semantic no-op")
	}
	receipts, err := openAIToolProductReceipts(artifact)
	if err != nil {
		return openAIToolEffectCommit{}, err
	}
	receipt, err := backend.newOpenAIToolProductEffectReceipt(ctx, request, artifact.ID, priorPostimage, output, postimage, receipts)
	if err != nil {
		return openAIToolEffectCommit{}, err
	}
	receipts[request.OperationID] = receipt
	encoded, err := encodeOpenAIToolProductReceipts(receipts)
	if err != nil {
		return openAIToolEffectCommit{}, err
	}
	projectionReceipts, err := openAIToolProductProjectionReceipts(artifact)
	if err != nil {
		return openAIToolEffectCommit{}, err
	}
	pendingProjection, err := backend.newOpenAIToolProductProjectionReceipt(ctx, receipt, openAIToolProjectionPending)
	if err != nil {
		return openAIToolEffectCommit{}, err
	}
	projectionReceipts[request.OperationID] = pendingProjection
	encodedProjections, err := encodeOpenAIToolProductProjectionReceipts(projectionReceipts)
	if err != nil {
		return openAIToolEffectCommit{}, err
	}
	metadata := map[string]string{
		"goalStatus": status, "currentStage": stage, "reviewGate": review, "progressPercent": strconv.Itoa(progress),
		"progressNote": strings.TrimSpace(asString(request.Arguments["note"])), openAIToolOperationReceiptsMetadataKey: encoded,
		openAIToolProjectionReceiptsMetadataKey: encodedProjections,
	}
	updated, changed, err := backend.app.memory.updateOSArtifactWithMetadataIfHeaderAndToolPreimagesMatch(header, priorPostimage, request.PreimageDigest, artifact.ID, "", artifact.Text, scoutParticipantName, metadata)
	if err != nil || !changed {
		return openAIToolEffectCommit{}, errors.New("OpenAI tool goal-state CAS did not commit")
	}
	actualPostimage, err := openAIToolProductSemanticPostimageDigest(updated, request.Entry.Name)
	if err != nil || actualPostimage != postimage {
		return openAIToolEffectCommit{}, errors.New("OpenAI tool goal-state successor did not match its receipt")
	}
	if err := backend.ensureGoalProgressProjection(ctx, request.Expectation); err != nil {
		return openAIToolEffectCommit{}, err
	}
	if err := backend.ensureOpenAIToolProductProjection(ctx, request, receipt); err != nil {
		return openAIToolEffectCommit{}, err
	}
	return openAIToolEffectCommitFromReceipt(receipt), nil
}

func (backend *openAIToolProductBackend) ensureGoalProgressProjection(ctx context.Context, expectation openAIToolAuthorityExpectation) error {
	artifact, ok := backend.app.osArtifactByID(expectation.ArtifactID)
	if !ok || strings.TrimSpace(artifact.Metadata["threadStatus"]) != "running" {
		return errors.New("OpenAI tool goal progress artifact is unavailable")
	}
	threadID := strings.TrimSpace(artifact.Metadata["originId"])
	if err := backend.app.commitScoutChatThreadRefStatusWithContext(ctx, threadID, expectation.RequesterAccount, expectation.ThreadID, "running", artifact.ID); err != nil {
		return err
	}
	thread, _, err := backend.app.scoutChatThreadByID(expectation.RequesterAccount, threadID)
	if err != nil {
		return err
	}
	wantProgress := float64(artifactProgressPercent(artifact))
	for _, message := range thread.Messages {
		if message.Thread != nil && message.Thread.ID == expectation.ThreadID && message.Thread.ArtifactID == artifact.ID && message.Thread.Status == "running" && message.Thread.CurrentStage == artifact.Metadata["currentStage"] && message.Thread.ProgressPercent == wantProgress {
			return nil
		}
	}
	return errors.New("OpenAI tool goal progress projection did not reconcile")
}

func validOpenAIToolGoalTransition(current, next string) bool {
	if current == "" || current == next {
		return true
	}
	if next == "needs_attention" || next == "approval_required" {
		return current != "verified"
	}
	rank := map[string]int{"running": 0, "review": 1, "verified": 2}
	currentRank, currentOK := rank[current]
	nextRank, nextOK := rank[next]
	return currentOK && nextOK && nextRank >= currentRank
}

func validOpenAIToolReviewGateTransition(current, next string) bool {
	current, next = strings.TrimSpace(current), strings.TrimSpace(next)
	if current == "" || current == next {
		return true
	}
	if current != "pending" {
		return false
	}
	switch next {
	case "approval_required", "blocked", "passed":
		return true
	default:
		return false
	}
}

func artifactProgressPercent(artifact meetingMemoryEntry) int {
	value, _ := strconv.Atoi(strings.TrimSpace(artifact.Metadata["progressPercent"]))
	if value < 0 || value > 100 {
		return 0
	}
	return value
}

func (backend *openAIToolProductBackend) ReconcileMemoryAnswer(ctx context.Context, request openAIToolEffectRequest) (openAIToolReconciliation, error) {
	window, err := backend.memoryWindow(ctx, strings.TrimSpace(asString(request.Arguments["query"])))
	if err != nil {
		return openAIToolReconciliation{}, err
	}
	if window.PreimageDigest != request.PreimageDigest {
		return openAIToolReconciliation{Status: openAIToolReconciliationAmbiguous}, nil
	}
	commit, err := window.commit(request)
	if err != nil {
		return openAIToolReconciliation{}, err
	}
	return openAIToolReconciliation{Status: openAIToolReconciliationCommitted, Commit: commit}, nil
}

func (backend *openAIToolProductBackend) ReadMemoryAnswer(ctx context.Context, request openAIToolEffectRequest) (openAIToolEffectCommit, error) {
	window, err := backend.memoryWindow(ctx, strings.TrimSpace(asString(request.Arguments["query"])))
	if err != nil {
		return openAIToolEffectCommit{}, err
	}
	if window.PreimageDigest != request.PreimageDigest {
		return openAIToolEffectCommit{}, errors.New("OpenAI tool memory source window changed before read")
	}
	return window.commit(request)
}

type openAIToolMemoryWindow struct {
	PreimageDigest string
	Answer         string
	Sources        []string
}

func (window openAIToolMemoryWindow) commit(request openAIToolEffectRequest) (openAIToolEffectCommit, error) {
	output, err := json.Marshal(map[string]any{"answer": window.Answer, "sources": window.Sources})
	if err != nil {
		return openAIToolEffectCommit{}, err
	}
	postimage, err := openAIToolCanonicalDigestOnly(map[string]any{"domain": "stride-openai-tool-memory-result-v1", "preimage": window.PreimageDigest, "output": json.RawMessage(output)})
	if err != nil {
		return openAIToolEffectCommit{}, err
	}
	reconciliation, err := openAIToolCanonicalDigestOnly(map[string]any{
		"domain": "stride-openai-tool-memory-effect-v1", "operation_id": request.OperationID,
		"expectation": request.Expectation, "preimage": request.PreimageDigest, "postimage": postimage, "output": json.RawMessage(output),
	})
	if err != nil {
		return openAIToolEffectCommit{}, err
	}
	return openAIToolEffectCommit{FunctionOutput: output, PostimageDigest: postimage, ReconciliationDigest: reconciliation}, nil
}

func (backend *openAIToolProductBackend) memoryWindow(ctx context.Context, query string) (openAIToolMemoryWindow, error) {
	query = canonicalizeBoardText(query)
	if query == "" {
		return openAIToolMemoryWindow{}, errors.New("OpenAI tool memory query is required")
	}
	user, ok := authenticatedRequester(backend.expectation.RequesterAccount)
	if !ok {
		return openAIToolMemoryWindow{}, ErrStrideE10TenantAuthorityStale
	}
	principal := recallPrincipalForUser(user)
	principal.TenantID = backend.expectation.TenantID
	recallApp := backend.app.scopedRecallApp(ctx, principal)
	if recallApp == nil || recallApp.memory == nil {
		return openAIToolMemoryWindow{}, errors.New("OpenAI tool memory is unavailable")
	}
	matches, contextEntries := recallApp.memoryMatchesAndContext(query)
	sourceEntries := make([]meetingMemoryEntry, 0, len(contextEntries)+len(matches))
	seen := map[string]bool{}
	for _, entry := range contextEntries {
		if entry.ID != "" && !seen[entry.ID] {
			sourceEntries = append(sourceEntries, entry)
			seen[entry.ID] = true
		}
	}
	for _, match := range matches {
		if match.Entry.ID != "" && !seen[match.Entry.ID] {
			sourceEntries = append(sourceEntries, match.Entry)
			seen[match.Entry.ID] = true
		}
	}
	sources := make([]string, 0, len(sourceEntries))
	digestRows := make([]map[string]any, 0, len(sourceEntries))
	for _, entry := range sourceEntries {
		sources = append(sources, entry.ID)
		digestRows = append(digestRows, map[string]any{"id": entry.ID, "kind": entry.Kind, "created_at": entry.CreatedAt.UTC().Format(time.RFC3339Nano), "body_digest": sha256Hex([]byte(entry.Text)), "metadata": entry.Metadata})
	}
	preimage, err := openAIToolCanonicalDigestOnly(map[string]any{"domain": "stride-openai-tool-memory-window-v1", "tenant": backend.expectation.TenantID, "person": backend.expectation.PersonID, "thread": backend.expectation.ThreadID, "query": query, "rows": digestRows})
	if err != nil {
		return openAIToolMemoryWindow{}, err
	}
	if len(sources) == 0 {
		sources = []string{"memory-window:" + preimage[:24]}
	}
	answer := buildMemoryAnswer(query, matches)
	if strings.TrimSpace(answer) == "" {
		answer = "No authorized memory matched this question."
	}
	return openAIToolMemoryWindow{PreimageDigest: preimage, Answer: answer, Sources: sources}, nil
}

func (backend *openAIToolProductBackend) ReconcileArtifactCreate(ctx context.Context, request openAIToolEffectRequest) (openAIToolReconciliation, error) {
	return backend.reconcileArtifactReceipt(ctx, request, openAIToolCreatedArtifactID(request.OperationID))
}

func (backend *openAIToolProductBackend) CreatePrivateArtifact(ctx context.Context, request openAIToolEffectRequest) (openAIToolEffectCommit, error) {
	if err := backend.validateCurrentProductBinding(ctx, request.Expectation); err != nil {
		return openAIToolEffectCommit{}, err
	}
	currentGeneration, err := backend.artifactCollectionGeneration()
	if err != nil || currentGeneration != request.PreimageDigest {
		return openAIToolEffectCommit{}, errors.New("OpenAI tool artifact collection changed before create")
	}
	mode := normalizeOSAssistantMode(asString(request.Arguments["mode"]))
	query := canonicalizeBoardText(asString(request.Arguments["query"]))
	content := strings.TrimSpace(asString(request.Arguments["content"]))
	if mode == "" || mode == "chat" || query == "" || content == "" {
		return openAIToolEffectCommit{}, errors.New("OpenAI tool artifact create arguments are invalid")
	}
	artifactID := openAIToolCreatedArtifactID(request.OperationID)
	metadata := map[string]string{
		"mode": mode, "query": query, "title": osArtifactTitle(mode, query, content), "status": "draft", "published": "false",
		"type": artifactType(meetingMemoryEntry{Text: content}), artifactVersionMetadataKey: "1",
		"createdBy": canonicalRoomActorName(backend.job.RequestedBy), "requestedBy": backend.expectation.RequesterAccount,
		"tenantId": backend.expectation.TenantID, "objectId": artifactID, "aclVersion": "1", "visibility": scoutChatVisibilityPrivate,
		"ownerEmail": backend.expectation.RequesterAccount, "originSurface": "chat:" + strings.TrimSpace(backend.job.thread.Artifact.Metadata["originId"]),
		"sourceThreadId": backend.expectation.ThreadID, "sourceWindowDigest": backend.expectation.SourceWindowDigest,
		"meetingId": "none", "roomId": officeRoomID,
	}
	output, err := json.Marshal(map[string]any{"artifact_id": artifactID, "title": metadata["title"], "type": metadata["type"], "status": "draft"})
	if err != nil {
		return openAIToolEffectCommit{}, err
	}
	target := meetingMemoryEntry{ID: artifactID, Kind: meetingMemoryKindOSArtifact, Text: normalizeMemoryEntryText(meetingMemoryKindOSArtifact, content), Metadata: metadata}
	target.Metadata[artifactContentDigestMetadataKey] = artifactCapabilityDigest(target)
	postimage, err := openAIToolProductSemanticPostimageDigest(target, request.Entry.Name)
	if err != nil {
		return openAIToolEffectCommit{}, err
	}
	receipt, err := backend.newOpenAIToolProductEffectReceipt(ctx, request, artifactID, "", output, postimage, nil)
	if err != nil {
		return openAIToolEffectCommit{}, err
	}
	encoded, err := encodeOpenAIToolProductReceipts(map[string]openAIToolProductEffectReceipt{request.OperationID: receipt})
	if err != nil {
		return openAIToolEffectCommit{}, err
	}
	metadata[openAIToolOperationReceiptsMetadataKey] = encoded
	pendingProjection, err := backend.newOpenAIToolProductProjectionReceipt(ctx, receipt, openAIToolProjectionPending)
	if err != nil {
		return openAIToolEffectCommit{}, err
	}
	encodedProjections, err := encodeOpenAIToolProductProjectionReceipts(map[string]openAIToolProductProjectionReceipt{request.OperationID: pendingProjection})
	if err != nil {
		return openAIToolEffectCommit{}, err
	}
	metadata[openAIToolProjectionReceiptsMetadataKey] = encodedProjections
	created, err := backend.appendPrivateArtifactWithCollectionCAS(request.PreimageDigest, artifactID, content, metadata)
	if err != nil {
		return openAIToolEffectCommit{}, err
	}
	actualPostimage, err := openAIToolProductSemanticPostimageDigest(created, request.Entry.Name)
	if err != nil || actualPostimage != postimage {
		return openAIToolEffectCommit{}, errors.New("OpenAI tool created artifact did not match its exact successor")
	}
	if err := backend.ensureOpenAIToolProductProjection(ctx, request, receipt); err != nil {
		return openAIToolEffectCommit{}, err
	}
	return openAIToolEffectCommitFromReceipt(receipt), nil
}

func openAIToolCreatedArtifactID(operationID string) string {
	return "os-artifact-openai-tool-" + sha256Hex([]byte("stride-openai-tool-created-artifact-v1\x00" + strings.TrimSpace(operationID)))[:24]
}

func (backend *openAIToolProductBackend) ReconcileArtifactUpdate(ctx context.Context, request openAIToolEffectRequest) (openAIToolReconciliation, error) {
	return backend.reconcileArtifactReceipt(ctx, request, request.Expectation.ArtifactID)
}

func (backend *openAIToolProductBackend) UpdateAuthorizedArtifact(ctx context.Context, request openAIToolEffectRequest) (openAIToolEffectCommit, error) {
	if err := backend.validateCurrentProductBinding(ctx, request.Expectation); err != nil {
		return openAIToolEffectCommit{}, err
	}
	artifact, header, err := backend.currentArtifactForWrite(request)
	if err != nil {
		return openAIToolEffectCommit{}, err
	}
	title := canonicalizeBoardText(asString(request.Arguments["title"]))
	content := strings.TrimSpace(asString(request.Arguments["content"]))
	if title == "" && content == "" {
		return openAIToolEffectCommit{}, errors.New("OpenAI tool artifact update has no change")
	}
	if content == "" {
		content = artifact.Text
	}
	if title == "" {
		title = artifact.Metadata["title"]
	}
	outputRevision := artifactVersion(artifact)
	if artifact.Text != content || artifact.Metadata["title"] != title {
		outputRevision++
	}
	output, err := json.Marshal(map[string]any{"artifact_id": artifact.ID, "revision": strconv.Itoa(outputRevision), "status": firstNonEmptyString(artifact.Metadata["status"], "draft")})
	if err != nil {
		return openAIToolEffectCommit{}, err
	}
	target := cloneMemoryEntry(artifact)
	target.Text = normalizeMemoryEntryText(meetingMemoryKindOSArtifact, content)
	target.Metadata["title"] = title
	if artifact.Text != target.Text || artifact.Metadata["title"] != title {
		target.Metadata[artifactVersionMetadataKey] = strconv.Itoa(outputRevision)
		target.Metadata[artifactContentDigestMetadataKey] = artifactCapabilityDigest(target)
	}
	priorPostimage, err := openAIToolProductSemanticPostimageDigest(artifact, request.Entry.Name)
	if err != nil {
		return openAIToolEffectCommit{}, err
	}
	postimage, err := openAIToolProductSemanticPostimageDigest(target, request.Entry.Name)
	if err != nil {
		return openAIToolEffectCommit{}, err
	}
	if postimage == priorPostimage {
		return openAIToolEffectCommit{}, errors.New("OpenAI tool artifact update is a semantic no-op")
	}
	receipts, err := openAIToolProductReceipts(artifact)
	if err != nil {
		return openAIToolEffectCommit{}, err
	}
	receipt, err := backend.newOpenAIToolProductEffectReceipt(ctx, request, artifact.ID, priorPostimage, output, postimage, receipts)
	if err != nil {
		return openAIToolEffectCommit{}, err
	}
	receipts[request.OperationID] = receipt
	encoded, err := encodeOpenAIToolProductReceipts(receipts)
	if err != nil {
		return openAIToolEffectCommit{}, err
	}
	projectionReceipts, err := openAIToolProductProjectionReceipts(artifact)
	if err != nil {
		return openAIToolEffectCommit{}, err
	}
	pendingProjection, err := backend.newOpenAIToolProductProjectionReceipt(ctx, receipt, openAIToolProjectionPending)
	if err != nil {
		return openAIToolEffectCommit{}, err
	}
	projectionReceipts[request.OperationID] = pendingProjection
	encodedProjections, err := encodeOpenAIToolProductProjectionReceipts(projectionReceipts)
	if err != nil {
		return openAIToolEffectCommit{}, err
	}
	updated, changed, err := backend.app.memory.updateOSArtifactWithMetadataIfHeaderAndToolPreimagesMatch(header, priorPostimage, request.PreimageDigest, artifact.ID, title, content, scoutParticipantName, map[string]string{
		openAIToolOperationReceiptsMetadataKey: encoded, openAIToolProjectionReceiptsMetadataKey: encodedProjections,
	})
	if err != nil || !changed {
		return openAIToolEffectCommit{}, errors.New("OpenAI tool artifact update CAS did not commit")
	}
	actualPostimage, err := openAIToolProductSemanticPostimageDigest(updated, request.Entry.Name)
	if err != nil || actualPostimage != postimage {
		return openAIToolEffectCommit{}, errors.New("OpenAI tool artifact successor did not match its receipt")
	}
	if err := backend.ensureOpenAIToolProductProjection(ctx, request, receipt); err != nil {
		return openAIToolEffectCommit{}, err
	}
	return openAIToolEffectCommitFromReceipt(receipt), nil
}

func (backend *openAIToolProductBackend) currentArtifactForWrite(request openAIToolEffectRequest) (meetingMemoryEntry, ArtifactAuthorizationHeader, error) {
	header, ok := backend.app.memory.artifactAuthorizationHeaderByID(request.Expectation.ArtifactID)
	if !ok {
		return meetingMemoryEntry{}, ArtifactAuthorizationHeader{}, errors.New("OpenAI tool artifact is unavailable")
	}
	artifact, ok := backend.app.memory.artifactSnapshotIfHeaderMatches(request.Expectation.ArtifactID, header)
	if !ok {
		return meetingMemoryEntry{}, ArtifactAuthorizationHeader{}, errors.New("OpenAI tool artifact changed during authorization")
	}
	if strings.TrimSpace(artifact.Metadata[openAIToolFinalReceiptMetadataKey]) != "" {
		return meetingMemoryEntry{}, ArtifactAuthorizationHeader{}, errors.New("OpenAI tool artifact is already terminal")
	}
	preimage, err := openAIToolArtifactPostimageDigest(artifact)
	if err != nil || preimage != request.PreimageDigest {
		return meetingMemoryEntry{}, ArtifactAuthorizationHeader{}, errors.New("OpenAI tool artifact preimage changed before effect")
	}
	return artifact, header, nil
}

func openAIToolArtifactPostimageDigest(artifact meetingMemoryEntry) (string, error) {
	return openAIToolCanonicalDigestOnly(map[string]any{
		"domain": "stride-openai-tool-artifact-postimage-v1", "id": artifact.ID, "kind": artifact.Kind,
		"text_digest": sha256Hex([]byte(artifact.Text)), "created_at": artifact.CreatedAt.UTC().Format(time.RFC3339Nano), "metadata": artifact.Metadata,
	})
}

// openAIToolProductSemanticPostimageDigest is known before a CAS and can
// therefore be persisted in the same artifact write as the immutable operation
// receipt. Store-owned clocks and version-history blobs are deliberately not
// part of this digest; the complete body, title/type, authorization header,
// version, lifecycle, and governed work projection are.
func openAIToolProductSemanticPostimageDigest(artifact meetingMemoryEntry, _ string) (string, error) {
	header := artifactAuthorizationHeaderFromEntry(artifact)
	return openAIToolCanonicalDigestOnly(map[string]any{
		"domain": "stride-openai-tool-product-postimage-v1",
		"id":     artifact.ID, "kind": artifact.Kind, "body_digest": sha256Hex([]byte(artifact.Text)),
		"title": artifact.Metadata["title"], "type": artifactType(artifact), "version": artifactVersion(artifact),
		"status": artifact.Metadata["status"], "thread_status": artifact.Metadata["threadStatus"],
		"goal_status": artifact.Metadata["goalStatus"], "stage": artifact.Metadata["currentStage"],
		"review_gate": artifact.Metadata["reviewGate"], "progress": artifact.Metadata["progressPercent"], "progress_note": artifact.Metadata["progressNote"],
		"tenant": header.TenantID, "object": header.ObjectID, "acl": header.ACLVersion,
		"content_revision": header.ContentRevision, "content_digest": header.ContentDigest,
		"visibility": header.Visibility, "owner": header.OwnerEmail, "origin": header.OriginSurface,
	})
}

func (backend *openAIToolProductBackend) artifactCollectionGeneration() (string, error) {
	if backend == nil || backend.app == nil || backend.app.memory == nil {
		return "", errors.New("OpenAI tool artifact collection is unavailable")
	}
	backend.app.memory.mu.Lock()
	defer backend.app.memory.mu.Unlock()
	return openAIToolArtifactCollectionGenerationLocked(backend.app.memory)
}

func (backend *openAIToolProductBackend) productStoreGeneration() (string, error) {
	if backend == nil || backend.app == nil || backend.app.memory == nil {
		return "", errors.New("OpenAI tool product store is unavailable")
	}
	backend.app.memory.mu.Lock()
	defer backend.app.memory.mu.Unlock()
	return openAIToolProductStoreGenerationLocked(backend.app.memory)
}

// commitOpenAIToolFinalArtifactAndChat is the terminal product transaction.
// The work artifact and its private work card live in the same append-only
// memory store, so one held store lock can fence the exact operation proof
// generation through both durable successors and the requester-scoped live
// fan-out. No completed artifact or card is visible independently.
func (backend *openAIToolProductBackend) commitOpenAIToolFinalArtifactAndChat(ctx context.Context, artifact meetingMemoryEntry, expectedHeader ArtifactAuthorizationHeader, expectedSemanticPostimage, expectedFullPreimage, expectedStoreGeneration, text string, metadataUpdates map[string]string) (meetingMemoryEntry, error) {
	if backend == nil || backend.app == nil || backend.app.memory == nil {
		return meetingMemoryEntry{}, errOpenAIToolCarrierUnavailable
	}
	threadID := strings.TrimSpace(artifact.Metadata["originId"])
	if threadID == "" {
		return meetingMemoryEntry{}, errors.New("OpenAI tool final thread is unavailable")
	}
	threadLock := backend.app.scoutChatThreadLock(threadID)
	threadLock.Lock()
	defer threadLock.Unlock()

	store := backend.app.memory
	store.mu.Lock()
	storeLocked := true
	defer func() {
		if storeLocked {
			store.mu.Unlock()
		}
	}()
	currentGeneration, err := openAIToolProductStoreGenerationLocked(store)
	if err != nil || currentGeneration != expectedStoreGeneration {
		return meetingMemoryEntry{}, errors.New("OpenAI tool final store generation changed")
	}
	artifactIndex, threadIndex := -1, -1
	for index := range store.entries {
		entry := store.entries[index]
		if entry.Kind == meetingMemoryKindOSArtifact && entry.ID == artifact.ID {
			artifactIndex = index
		}
		if entry.Kind == meetingMemoryKindScoutChat && entry.ID == threadID {
			threadIndex = index
		}
	}
	if artifactIndex < 0 || threadIndex < 0 {
		return meetingMemoryEntry{}, errors.New("OpenAI tool final artifact or chat is unavailable")
	}
	current := store.entries[artifactIndex]
	currentHeader := store.resolveArtifactHeaderSecurityLocked(artifactAuthorizationHeaderFromEntry(current))
	semantic, semanticErr := openAIToolProductSemanticPostimageDigest(current, "")
	full, fullErr := openAIToolArtifactPostimageDigest(current)
	if semanticErr != nil || fullErr != nil || !artifactAuthorizationHeaderEqual(expectedHeader, currentHeader) || semantic != expectedSemanticPostimage || full != expectedFullPreimage {
		return meetingMemoryEntry{}, errors.New("OpenAI tool final artifact changed before transaction")
	}
	if err := validateArtifactScopeMetadataUpdates(current.Metadata, metadataUpdates); err != nil {
		return meetingMemoryEntry{}, err
	}
	updated := cloneMemoryEntry(current)
	if updated.Metadata == nil {
		updated.Metadata = map[string]string{}
	}
	for key, value := range metadataUpdates {
		if key = strings.TrimSpace(key); key != "" {
			updated.Metadata[key] = strings.TrimSpace(value)
		}
	}
	updated.Text = normalizeMemoryEntryText(meetingMemoryKindOSArtifact, text)
	if updated.Text == "" {
		return meetingMemoryEntry{}, errors.New("OpenAI tool final artifact text is required")
	}
	if current.Text != updated.Text {
		bumpArtifactVersionLocked(&updated, current)
		invalidateArtifactApprovalForRevision(&updated, updated.Metadata["status"], updated.Metadata[artifactHumanApprovedAtKey])
		updated.Metadata[artifactContentDigestMetadataKey] = artifactCapabilityDigest(updated)
	}
	updated.Metadata["updatedBy"] = scoutParticipantName
	updated.Metadata["updatedAt"] = time.Now().UTC().Format(time.RFC3339Nano)

	threadEntry := store.entries[threadIndex]
	thread, ok := decodeScoutChatThreadEntry(threadEntry)
	if !ok || normalizeAccountEmail(thread.OwnerEmail) != backend.expectation.RequesterAccount || scoutChatThreadVisibility(thread) != scoutChatVisibilityPrivate {
		return meetingMemoryEntry{}, errors.New("OpenAI tool final chat authority changed")
	}
	var changedMessage *scoutChatMessageRecord
	for index := range thread.Messages {
		ref := thread.Messages[index].Thread
		if ref == nil || ref.ID != backend.expectation.ThreadID {
			continue
		}
		if strings.TrimSpace(ref.ArtifactID) != updated.ID || !scoutChatArtifactMatchesDurableProjection(updated, ref, backend.expectation.ThreadID, "complete", updated.ID) {
			return meetingMemoryEntry{}, errors.New("OpenAI tool final chat projection changed")
		}
		beforeText := thread.Messages[index].Text
		beforeRef := *ref
		ref.Status = "complete"
		ref.AgentID = firstNonBlank(updated.Metadata["agentId"], ref.AgentID)
		ref.AgentName = firstNonBlank(updated.Metadata["agentName"], ref.AgentName)
		ref.DelegatedBy = firstNonBlank(updated.Metadata["delegatedBy"], ref.DelegatedBy)
		ref.CurrentStage = updated.Metadata["currentStage"]
		ref.ProgressNote = updated.Metadata["progressNote"]
		ref.FollowUpStatus = updated.Metadata["followUpStatus"]
		ref.AttentionReason = scoutChatThreadAttentionReason(updated.Metadata)
		ref.StartedAt = firstNonBlank(updated.Metadata["startedAt"], ref.StartedAt)
		if progress, parseErr := strconv.ParseFloat(strings.TrimSpace(updated.Metadata["progressPercent"]), 64); parseErr == nil {
			ref.ProgressPercent = progress
		}
		if thread.Messages[index].Kind == "thread" {
			if statusCopy, copyOK := scoutChatWorkStatusCopy(updated, backend.expectation.ThreadID, "complete"); copyOK {
				thread.Messages[index].Text = statusCopy
			}
		}
		if thread.Messages[index].Text != beforeText || !scoutChatThreadRefsEqual(*thread.Messages[index].Thread, beforeRef) {
			copy := thread.Messages[index]
			changedMessage = &copy
		}
		break
	}
	if changedMessage == nil {
		return meetingMemoryEntry{}, errors.New("OpenAI tool final work card is unavailable or already terminal")
	}
	thread.Preview = scoutChatThreadPreview(thread)
	thread.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	encodedThread, err := encodeScoutChatThread(thread)
	if err != nil {
		return meetingMemoryEntry{}, err
	}
	updatedThreadEntry := cloneMemoryEntry(threadEntry)
	updatedThreadEntry.Text = normalizeMemoryEntryText(meetingMemoryKindScoutChat, encodedThread)
	if updatedThreadEntry.Metadata == nil {
		updatedThreadEntry.Metadata = map[string]string{}
	}
	for key, value := range scoutChatThreadMetadata(thread) {
		if key = strings.TrimSpace(key); key != "" {
			updatedThreadEntry.Metadata[key] = strings.TrimSpace(value)
		}
	}

	store.entries[artifactIndex], store.entries[threadIndex] = updated, updatedThreadEntry
	if err := store.rewriteLocked(false); err != nil {
		store.entries[artifactIndex], store.entries[threadIndex] = current, threadEntry
		return meetingMemoryEntry{}, err
	}
	// The artifact + chat commit above is one durable transaction. Release its
	// source-store lock before the viewer projection: result projection performs
	// fresh header/body ACL fences of its own, and recursively taking store.mu
	// here deadlocks every terminal tool result. A concurrent edit can therefore
	// produce only the new current result or a fail-closed sparse work card,
	// never a stale authorized body.
	store.mu.Unlock()
	storeLocked = false
	backend.app.sendScoutChatThreadUpdateToViewerWithContext(ctx, backend.expectation.RequesterAccount, thread, *changedMessage)
	if openAIToolAfterFinalArtifactCommitProbe != nil {
		if err := openAIToolAfterFinalArtifactCommitProbe(updated); err != nil {
			return meetingMemoryEntry{}, fmt.Errorf("%w: %v", errOpenAIToolFinalizationInterrupted, err)
		}
	}
	return cloneMemoryEntry(updated), nil
}

// openAIToolProductStoreGenerationLocked binds every durable product input
// that a four-tool terminal result can depend on. The final artifact CAS checks
// the same digest while holding the store lock, so a created-artifact edit or
// memory append cannot land between terminal reconciliation and final use.
func openAIToolProductStoreGenerationLocked(store *meetingMemoryStore) (string, error) {
	if store == nil {
		return "", errors.New("OpenAI tool product store is unavailable")
	}
	rows := make([]map[string]any, 0, len(store.entries))
	for _, entry := range store.entries {
		if memoryEntryHiddenFromRecall(entry) {
			continue
		}
		rows = append(rows, map[string]any{
			"id": entry.ID, "kind": entry.Kind, "created_at": entry.CreatedAt.UTC().Format(time.RFC3339Nano),
			"text_digest": sha256Hex([]byte(entry.Text)), "metadata": entry.Metadata,
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		left, right := asString(rows[i]["id"]), asString(rows[j]["id"])
		if left == right {
			return asString(rows[i]["kind"]) < asString(rows[j]["kind"])
		}
		return left < right
	})
	return openAIToolCanonicalDigestOnly(map[string]any{"domain": "stride-openai-tool-product-store-generation-v1", "rows": rows})
}

func openAIToolArtifactCollectionGenerationLocked(store *meetingMemoryStore) (string, error) {
	rows := make([]map[string]any, 0)
	for _, entry := range store.entries {
		if entry.Kind != meetingMemoryKindOSArtifact || memoryEntryHiddenFromRecall(entry) {
			continue
		}
		header := store.resolveArtifactHeaderSecurityLocked(artifactAuthorizationHeaderFromEntry(meetingMemoryEntry{ID: entry.ID, Kind: entry.Kind, Metadata: entry.Metadata}))
		rows = append(rows, map[string]any{"id": entry.ID, "tenant": header.TenantID, "acl": header.ACLVersion, "revision": header.ContentRevision, "digest": header.ContentDigest, "visibility": header.Visibility, "owner": header.OwnerEmail})
	}
	sort.Slice(rows, func(i, j int) bool { return asString(rows[i]["id"]) < asString(rows[j]["id"]) })
	meetingID := ""
	if store.meetingIDs != nil {
		meetingID = store.meetingIDs[officeRoomID]
	}
	return openAIToolCanonicalDigestOnly(map[string]any{"domain": "stride-openai-tool-artifact-collection-v1", "meeting_id": meetingID, "rows": rows})
}

func (backend *openAIToolProductBackend) appendPrivateArtifactWithCollectionCAS(expectedGeneration, artifactID, text string, metadata map[string]string) (meetingMemoryEntry, error) {
	store := backend.app.memory
	text = normalizeMemoryEntryText(meetingMemoryKindOSArtifact, text)
	if text == "" {
		return meetingMemoryEntry{}, errors.New("OpenAI tool artifact body is empty")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	current, err := openAIToolArtifactCollectionGenerationLocked(store)
	if err != nil || current != expectedGeneration {
		return meetingMemoryEntry{}, errors.New("OpenAI tool artifact collection CAS changed")
	}
	if _, seen := store.seen[artifactID]; seen {
		return meetingMemoryEntry{}, errors.New("OpenAI tool artifact ID already exists without reconciliation")
	}
	stamped := make(map[string]string, len(metadata)+3)
	for key, value := range metadata {
		stamped[key] = strings.TrimSpace(value)
	}
	if strings.TrimSpace(stamped["meetingId"]) == "" {
		stamped["meetingId"] = "none"
	}
	stamped["roomId"] = officeRoomID
	stamped[artifactContentDigestMetadataKey] = artifactCapabilityDigest(meetingMemoryEntry{ID: artifactID, Kind: meetingMemoryKindOSArtifact, Text: text, Metadata: stamped})
	entry := meetingMemoryEntry{ID: artifactID, Kind: meetingMemoryKindOSArtifact, Text: text, CreatedAt: time.Now().UTC(), Metadata: stamped}
	raw, err := json.Marshal(entry)
	if err != nil {
		return meetingMemoryEntry{}, err
	}
	persist := appendFileBestEffort
	if canonicalLegacyDurabilityRequired() {
		persist = appendFileDurably
	}
	if err := persist(store.path, append(raw, '\n'), 0o600); err != nil {
		return meetingMemoryEntry{}, err
	}
	store.entries = append(store.entries, entry)
	store.indexMeetingEntryLocked(len(store.entries)-1, entry)
	if store.seen == nil {
		store.seen = map[string]struct{}{}
	}
	store.seen[artifactID] = struct{}{}
	return cloneMemoryEntry(entry), nil
}

type openAIToolProductFinalizer struct{ backend *openAIToolProductBackend }

func (backend *openAIToolProductBackend) openAIToolOperationProofDigest(ctx context.Context, operationIDs []string) (string, error) {
	if backend == nil || backend.journal == nil {
		return "", errOpenAIToolCarrierUnavailable
	}
	manifest, err := buildOpenAIToolManifest()
	if err != nil {
		return "", err
	}
	proofs := make([]map[string]any, 0, len(operationIDs))
	for sequence, operationID := range operationIDs {
		record, envelope, recordErr := backend.journal.Record(ctx, operationID)
		if recordErr != nil || record.RunSequence != uint64(sequence) || record.Expectation.ArtifactID != backend.expectation.ArtifactID {
			return "", errors.New("OpenAI tool final operation membership changed")
		}
		entry, admitted := manifest.admitted(record.ToolName)
		if !admitted || record.ManifestSHA256 != manifest.DigestSHA256 || record.SchemaSHA256 != entry.SchemaSHA256 || record.PolicyRevision != entry.PolicyRevision {
			return "", errors.New("OpenAI tool final operation left the frozen manifest")
		}
		reconciliation, reconcileErr := (openAIToolEffectAdapter{Backend: backend}).ReconcileOpenAITool(ctx, &openAIToolProductCurrentAuthority{backend: backend}, operationID, record.Expectation, entry, envelope.Arguments, record.PreimageDigest)
		if reconcileErr != nil || reconciliation.Status != openAIToolReconciliationCommitted || validateOpenAIToolEffectCommit(record.ToolName, reconciliation.Commit) != nil || !openAIToolEffectCommitMatchesRecord(reconciliation.Commit, record, envelope) {
			return "", errors.New("OpenAI tool final operation postimage changed")
		}
		proofs = append(proofs, map[string]any{
			"operation_id": operationID, "sequence": sequence, "tool": record.ToolName,
			"preimage": record.PreimageDigest, "postimage": reconciliation.Commit.PostimageDigest,
			"reconciliation": reconciliation.Commit.ReconciliationDigest,
		})
	}
	return openAIToolCanonicalDigestOnly(map[string]any{"domain": "stride-openai-tool-product-operation-proof-set-v1", "proofs": proofs})
}

// openAIToolStableOperationProofAndStoreGeneration takes an optimistic atomic
// snapshot across the product store and every tool-specific reconciliation.
// The equal generation reads are the seqlock: drift during proof construction
// or in the proof-to-baseline gap fails, and the terminal transaction later
// compares that same generation again under the store lock.
func (backend *openAIToolProductBackend) openAIToolStableOperationProofAndStoreGeneration(ctx context.Context, operationIDs []string) (string, string, error) {
	before, err := backend.productStoreGeneration()
	if err != nil {
		return "", "", err
	}
	proof, err := backend.openAIToolOperationProofDigest(ctx, operationIDs)
	if err != nil {
		return "", "", err
	}
	if openAIToolAfterOperationProofProbe != nil {
		openAIToolAfterOperationProofProbe()
	}
	after, err := backend.productStoreGeneration()
	if err != nil || before != after {
		return "", "", errors.New("OpenAI tool operation proof store generation changed")
	}
	return proof, after, nil
}

// openAIToolRecordedOperationProofDigest authenticates the immutable journal
// proof set after the terminal product transaction. Mutable memory/artifact
// sources were fenced and revalidated by openAIToolOperationProofDigest while
// the store transaction was admitted; later legitimate memory growth must not
// retroactively invalidate a completed run.
func (backend *openAIToolProductBackend) openAIToolRecordedOperationProofDigest(ctx context.Context, operationIDs []string) (string, error) {
	if backend == nil || backend.journal == nil {
		return "", errOpenAIToolCarrierUnavailable
	}
	manifest, err := buildOpenAIToolManifest()
	if err != nil {
		return "", err
	}
	proofs := make([]map[string]any, 0, len(operationIDs))
	for sequence, operationID := range operationIDs {
		record, envelope, recordErr := backend.journal.Record(ctx, operationID)
		if recordErr != nil || record.RunSequence != uint64(sequence) || record.Expectation.ArtifactID != backend.expectation.ArtifactID {
			return "", errors.New("OpenAI tool final recorded operation membership changed")
		}
		entry, admitted := manifest.admitted(record.ToolName)
		if !admitted || record.ManifestSHA256 != manifest.DigestSHA256 || record.SchemaSHA256 != entry.SchemaSHA256 || record.PolicyRevision != entry.PolicyRevision {
			return "", errors.New("OpenAI tool final recorded operation left the frozen manifest")
		}
		commit := openAIToolEffectCommit{FunctionOutput: append(json.RawMessage(nil), envelope.ToolOutput...), PostimageDigest: record.PostimageDigest, ReconciliationDigest: record.ReconciliationDigest}
		if validateOpenAIToolEffectCommit(record.ToolName, commit) != nil || !openAIToolEffectCommitMatchesRecord(commit, record, envelope) {
			return "", errors.New("OpenAI tool final recorded operation proof changed")
		}
		proofs = append(proofs, map[string]any{
			"operation_id": operationID, "sequence": sequence, "tool": record.ToolName,
			"preimage": record.PreimageDigest, "postimage": record.PostimageDigest,
			"reconciliation": record.ReconciliationDigest,
		})
	}
	return openAIToolCanonicalDigestOnly(map[string]any{"domain": "stride-openai-tool-product-operation-proof-set-v1", "proofs": proofs})
}

func (finalizer *openAIToolProductFinalizer) ReconcileOpenAIToolRunFinalization(ctx context.Context, _ openAIToolCurrentAuthority, expectation openAIToolAuthorityExpectation, runID, text string, operationIDs []string) (openAIToolFinalizationReconciliation, error) {
	commit, status, err := finalizer.inspectFinalization(ctx, expectation, runID, text, operationIDs)
	return openAIToolFinalizationReconciliation{Status: status, Commit: commit}, err
}

func (finalizer *openAIToolProductFinalizer) FinalizeOpenAIToolRun(ctx context.Context, _ openAIToolCurrentAuthority, expectation openAIToolAuthorityExpectation, runID, text string, operationIDs []string) (openAIToolFinalizationCommit, error) {
	if finalizer == nil || finalizer.backend == nil {
		return openAIToolFinalizationCommit{}, errOpenAIToolCarrierUnavailable
	}
	if err := finalizer.backend.validateCurrentProductBinding(ctx, expectation); err != nil {
		return openAIToolFinalizationCommit{}, err
	}
	runDigest, err := openAIToolFinalizationRunDigest(expectation, runID, text, operationIDs)
	if err != nil {
		return openAIToolFinalizationCommit{}, err
	}
	artifact, ok := finalizer.backend.app.osArtifactByID(expectation.ArtifactID)
	if !ok {
		return openAIToolFinalizationCommit{}, errors.New("OpenAI tool final artifact is unavailable")
	}
	if existing := strings.TrimSpace(artifact.Metadata[openAIToolFinalRunDigestMetadataKey]); existing != "" && existing != runDigest {
		return openAIToolFinalizationCommit{}, errors.New("OpenAI tool final artifact belongs to another run")
	}
	if existing := strings.TrimSpace(artifact.Metadata[openAIToolFinalRunDigestMetadataKey]); existing == "" {
		if err := validateAgentThreadTerminalArtifactWithApp(finalizer.backend.app, finalizer.backend.job.thread, text); err != nil {
			return openAIToolFinalizationCommit{}, err
		}
		header := artifactAuthorizationHeaderFromEntry(artifact)
		finalUseDigest, fanOutDigest, err := openAIToolProductFinalDigests(runDigest, expectation)
		if err != nil {
			return openAIToolFinalizationCommit{}, err
		}
		metadata := map[string]string{
			"status": "complete", "threadStatus": "complete", "goalStatus": "verified", "currentStage": "verify_goal_completed",
			"progressPercent": "100", "reviewGate": "passed", "completedAt": time.Now().UTC().Format(time.RFC3339Nano),
			"latestThreadRun": expectation.ThreadID, "model": openAIToolRunnerModel, "reasoningEffort": openAIToolRunnerReasoningEffort,
			openAIToolActivationStateMetadataKey: openAIToolActivationFinalizing,
			openAIToolFinalRunDigestMetadataKey:  runDigest, openAIToolFinalUseDigestMetadataKey: finalUseDigest, openAIToolFanOutDigestMetadataKey: fanOutDigest,
		}
		priorPostimage, err := openAIToolProductSemanticPostimageDigest(artifact, "")
		if err != nil {
			return openAIToolFinalizationCommit{}, err
		}
		target := cloneMemoryEntry(artifact)
		target.Text = normalizeMemoryEntryText(meetingMemoryKindOSArtifact, strings.TrimSpace(text))
		for key, value := range metadata {
			target.Metadata[key] = value
		}
		if artifact.Text != target.Text {
			target.Metadata[artifactVersionMetadataKey] = strconv.Itoa(artifactVersion(artifact) + 1)
			target.Metadata[artifactContentDigestMetadataKey] = artifactCapabilityDigest(target)
		}
		postimage, err := openAIToolProductSemanticPostimageDigest(target, "")
		if err != nil {
			return openAIToolFinalizationCommit{}, err
		}
		fullPreimage, err := openAIToolArtifactPostimageDigest(artifact)
		if err != nil {
			return openAIToolFinalizationCommit{}, err
		}
		operationProofDigest, storeGeneration, err := finalizer.backend.openAIToolStableOperationProofAndStoreGeneration(ctx, operationIDs)
		if err != nil {
			return openAIToolFinalizationCommit{}, err
		}
		receipts, err := openAIToolProductReceipts(artifact)
		if err != nil {
			return openAIToolFinalizationCommit{}, err
		}
		finalReceipt, err := finalizer.backend.newOpenAIToolProductFinalReceipt(ctx, expectation, runDigest, priorPostimage, postimage, operationProofDigest, storeGeneration, finalUseDigest, fanOutDigest, receipts)
		if err != nil {
			return openAIToolFinalizationCommit{}, err
		}
		encodedFinalReceipt, err := json.Marshal(finalReceipt)
		if err != nil {
			return openAIToolFinalizationCommit{}, err
		}
		metadata[openAIToolFinalReceiptMetadataKey] = string(encodedFinalReceipt)
		if openAIToolBeforeFinalArtifactCASProbe != nil {
			openAIToolBeforeFinalArtifactCASProbe()
		}
		updated, updateErr := finalizer.backend.commitOpenAIToolFinalArtifactAndChat(ctx, artifact, header, priorPostimage, fullPreimage, storeGeneration, strings.TrimSpace(text), metadata)
		if updateErr != nil {
			if errors.Is(updateErr, errOpenAIToolFinalizationInterrupted) {
				return openAIToolFinalizationCommit{}, updateErr
			}
			return openAIToolFinalizationCommit{}, errors.New("OpenAI tool final artifact changed before its exact commit")
		}
		actualPostimage, postimageErr := openAIToolProductSemanticPostimageDigest(updated, "")
		if postimageErr != nil || actualPostimage != postimage {
			return openAIToolFinalizationCommit{}, errors.New("OpenAI tool final artifact did not match its authenticated successor")
		}
		artifact = updated
	}
	commit, status, err := finalizer.inspectFinalization(ctx, expectation, runID, text, operationIDs)
	if err != nil || status != openAIToolReconciliationCommitted {
		return openAIToolFinalizationCommit{}, errors.New("OpenAI tool final artifact or chat projection did not reconcile")
	}
	return commit, nil
}

func (finalizer *openAIToolProductFinalizer) inspectFinalization(ctx context.Context, expectation openAIToolAuthorityExpectation, runID, text string, operationIDs []string) (openAIToolFinalizationCommit, openAIToolReconciliationStatus, error) {
	if finalizer == nil || finalizer.backend == nil {
		return openAIToolFinalizationCommit{}, openAIToolReconciliationAmbiguous, errOpenAIToolCarrierUnavailable
	}
	if err := finalizer.backend.validateCurrentProductBinding(ctx, expectation); err != nil {
		return openAIToolFinalizationCommit{}, openAIToolReconciliationAmbiguous, err
	}
	runDigest, err := openAIToolFinalizationRunDigest(expectation, runID, text, operationIDs)
	if err != nil {
		return openAIToolFinalizationCommit{}, openAIToolReconciliationAmbiguous, err
	}
	artifact, ok := finalizer.backend.app.osArtifactByID(expectation.ArtifactID)
	if !ok || strings.TrimSpace(artifact.Metadata[openAIToolFinalRunDigestMetadataKey]) == "" {
		return openAIToolFinalizationCommit{}, openAIToolReconciliationNotApplied, nil
	}
	if artifact.Metadata[openAIToolFinalRunDigestMetadataKey] != runDigest || strings.TrimSpace(artifact.Text) != strings.TrimSpace(text) || strings.TrimSpace(artifact.Metadata["threadStatus"]) != "complete" {
		return openAIToolFinalizationCommit{}, openAIToolReconciliationAmbiguous, nil
	}
	finalUseDigest, fanOutDigest, err := openAIToolProductFinalDigests(runDigest, expectation)
	if err != nil || artifact.Metadata[openAIToolFinalUseDigestMetadataKey] != finalUseDigest || artifact.Metadata[openAIToolFanOutDigestMetadataKey] != fanOutDigest {
		return openAIToolFinalizationCommit{}, openAIToolReconciliationAmbiguous, err
	}
	finalReceipt, receiptErr := openAIToolProductFinalReceiptFromArtifact(artifact)
	currentPostimage, postimageErr := openAIToolProductSemanticPostimageDigest(artifact, "")
	receipts, receiptsErr := openAIToolProductReceipts(artifact)
	orderedReceipts, orderedErr := finalizer.backend.orderedOpenAIToolProductReceipts(ctx, receipts, artifact.ID)
	operationProofDigest, proofErr := finalizer.backend.openAIToolRecordedOperationProofDigest(ctx, operationIDs)
	if receiptErr != nil || postimageErr != nil || receiptsErr != nil || orderedErr != nil || proofErr != nil || finalReceipt.RunDigest != runDigest || finalReceipt.PostimageDigest != currentPostimage || finalReceipt.OperationProofDigest != operationProofDigest || !finalizer.backend.openAIToolFinalReceiptFollowsReceipts(finalReceipt, orderedReceipts) || finalReceipt.FinalUseDigest != finalUseDigest || finalReceipt.FanOutDigest != fanOutDigest || finalizer.backend.verifyOpenAIToolProductFinalReceipt(ctx, finalReceipt) != nil {
		return openAIToolFinalizationCommit{}, openAIToolReconciliationAmbiguous, nil
	}
	thread, _, threadErr := finalizer.backend.app.scoutChatThreadByID(expectation.RequesterAccount, strings.TrimSpace(artifact.Metadata["originId"]))
	if threadErr != nil {
		return openAIToolFinalizationCommit{}, openAIToolReconciliationAmbiguous, threadErr
	}
	projection := false
	for _, message := range thread.Messages {
		if message.Thread != nil && message.Thread.ID == expectation.ThreadID && message.Thread.ArtifactID == artifact.ID && message.Thread.Status == "complete" {
			projection = true
			break
		}
	}
	if !projection {
		return openAIToolFinalizationCommit{}, openAIToolReconciliationNotApplied, nil
	}
	return openAIToolFinalizationCommit{RunDigest: runDigest, OperationIDs: append([]string(nil), operationIDs...), FinalUseDigest: finalUseDigest, FanOutReceiptDigest: fanOutDigest}, openAIToolReconciliationCommitted, nil
}

func openAIToolProductFinalDigests(runDigest string, expectation openAIToolAuthorityExpectation) (string, string, error) {
	finalUse, err := openAIToolCanonicalDigestOnly(map[string]any{"domain": "stride-openai-tool-product-final-use-v1", "run": runDigest, "artifact": expectation.ArtifactID})
	if err != nil {
		return "", "", err
	}
	fanOut, err := openAIToolCanonicalDigestOnly(map[string]any{"domain": "stride-openai-tool-product-fan-out-v1", "run": runDigest, "thread": expectation.ThreadID, "artifact": expectation.ArtifactID, "status": "complete"})
	return finalUse, fanOut, err
}
