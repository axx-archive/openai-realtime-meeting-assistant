package main

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
)

type studioDissentExecutionView struct {
	Status          string `json:"status"`
	Provider        string `json:"provider"`
	RequestedModel  string `json:"requestedModel"`
	ActualModel     string `json:"actualModel,omitempty"`
	ReasoningEffort string `json:"reasoningEffort"`
	Qualification   string `json:"qualification"`
	ReceiptDigest   string `json:"receiptDigest,omitempty"`
	Policy          string `json:"policy,omitempty"`
	FallbackUsed    bool   `json:"fallbackUsed"`
}

type studioDissentAssuranceView struct {
	Type             string `json:"type"`
	Status           string `json:"status"`
	Independent      bool   `json:"independent"`
	JudgmentRequired bool   `json:"judgmentRequired,omitempty"`
	ReceiptDigest    string `json:"receiptDigest,omitempty"`
}

type dissentDocumentReviewEvidence struct {
	Version         string   `json:"version"`
	ArtifactID      string   `json:"artifactId"`
	ArtifactVersion int      `json:"artifactVersion"`
	ContentDigest   string   `json:"contentDigest"`
	PagesDigest     string   `json:"pagesDigest"`
	JuryID          string   `json:"juryId"`
	JuryDigest      string   `json:"juryDigest"`
	SeatIDs         []string `json:"seatIds"`
	ExecutionDigest string   `json:"executionDigest,omitempty"`
	ReviewType      string   `json:"reviewType"`
	Independent     bool     `json:"independent"`
	Digest          string   `json:"digest"`
}

// This receipt records what the existing rendered admission actually proved.
// Multiple personas from one provider are not independent model families.
func documentDissentReviewMetadata(app *kanbanBoardApp, review documentReportQualityReview) map[string]string {
	evidence := dissentDocumentReviewEvidence{
		Version: "stride.dissent.document-review.v1", ArtifactID: review.ArtifactID,
		ArtifactVersion: review.ArtifactVersion, ContentDigest: review.ContentDigest,
		PagesDigest: review.PagesDigest, JuryID: review.JuryID, JuryDigest: review.JuryDigest,
		SeatIDs: append([]string(nil), review.SeatIDs...), ReviewType: "same_provider_rendered_review",
	}
	if artifact, ok := app.osArtifactByID(review.ArtifactID); ok {
		var receipt dissentDocumentExecutionReceipt
		raw := artifact.Metadata[dissentDocumentReceiptKey]
		if verifyDissentDocumentReceipt(raw, artifact.ID, artifact.Text) == nil && json.Unmarshal([]byte(raw), &receipt) == nil && dissentReceiptMatchesArtifactVersion(receipt, artifact) {
			evidence.ExecutionDigest = receipt.Digest
		}
	}
	evidence.Digest, _ = dissentReceiptDigest(evidence)
	raw, _ := json.Marshal(evidence)
	return map[string]string{"dissentDocumentReview": string(raw), "dissentDocumentReviewDigest": evidence.Digest}
}

// Resolve the exact result under current viewer authority, then separately
// authorize every review artifact. Internal prompts and provider response IDs
// never enter the project DTO. Human acceptance is a separate Work event and
// cannot upgrade this machine evidence to qualified or independent.
func (app *kanbanBoardApp) studioDissentEvidenceForViewer(ctx context.Context, viewer *userAccount, result studioProjectResultRef) (*studioDissentExecutionView, *studioDissentAssuranceView) {
	if app == nil || viewer == nil {
		return nil, nil
	}
	artifact, ok := app.authorizedScoutChatResultArtifact(ctx, viewer, result.ArtifactID)
	if !ok || artifactVersion(artifact) != result.Version || artifactCapabilityDigest(artifact) != result.Digest {
		return nil, nil
	}
	var receipt dissentDocumentExecutionReceipt
	raw := artifact.Metadata[dissentDocumentReceiptKey]
	if raw == "" || json.Unmarshal([]byte(raw), &receipt) != nil || receipt.ArtifactID != artifact.ID {
		return nil, nil
	}
	execution := &studioDissentExecutionView{Status: "unavailable", Provider: providerOpenAI, Qualification: "not_evaluated"}
	assurance := &studioDissentAssuranceView{Type: "not_performed", Status: "not_performed", Independent: false}
	var reviewSources []meetingMemoryEntry
	if verifyDissentDocumentReceipt(raw, artifact.ID, artifact.Text) == nil && dissentReceiptMatchesArtifactVersion(receipt, artifact) && (artifact.Metadata["latestThreadRun"] == "" || receipt.RunID == artifact.Metadata["latestThreadRun"]) {
		execution.Status, execution.RequestedModel, execution.ActualModel = "observed", receipt.RequestedModel, receipt.ActualModel
		execution.ReasoningEffort, execution.ReceiptDigest, execution.FallbackUsed = receipt.ReasoningEffort, receipt.Digest, receipt.FallbackUsed
		if receipt.Plan != nil {
			execution.Policy = receipt.Plan.Policy
			assurance.JudgmentRequired = receipt.Plan.JudgmentRequired
			if receipt.Plan.IndependentReviewRequired {
				assurance.Status = "independent_review_unavailable"
			}
		}
	}
	parentID := strings.TrimSpace(artifact.Metadata["goalParentId"])
	parent, parentOK := app.authorizedScoutChatResultArtifact(ctx, viewer, parentID)
	if parentOK {
		if plan, parsed := decodeGoalPlan(parent.Metadata["goalPlan"]); parsed {
			if review, err := resolvePublishedDocumentReportQuality(app, &plan, parentID); err == nil && review.ArtifactID == artifact.ID && review.ArtifactVersion == result.Version {
				jury, juryOK := app.authorizedScoutChatResultArtifact(ctx, viewer, review.JuryID)
				publish := plan.subtaskByID(documentReportPublishStageID)
				if juryOK && artifactCapabilityDigest(jury) == review.JuryDigest && publish != nil {
					if published, authorized := app.authorizedScoutChatResultArtifact(ctx, viewer, publish.ArtifactID); authorized {
						if expected := documentDissentReviewMetadata(app, review); published.Metadata["dissentDocumentReview"] == expected["dissentDocumentReview"] {
							assurance.Type, assurance.ReceiptDigest = "same_provider_rendered_review", expected["dissentDocumentReviewDigest"]
							reviewSources = []meetingMemoryEntry{parent, jury, published}
							if !assurance.JudgmentRequired {
								assurance.Status = "passed"
							}
						}
					}
				}
			}
		}
	}
	current, stillAuthorized := app.authorizedScoutChatResultArtifact(ctx, viewer, result.ArtifactID)
	if !stillAuthorized || artifactVersion(current) != result.Version || artifactCapabilityDigest(current) != result.Digest || !dissentEvidenceSnapshotEqual(artifact, current) {
		return nil, nil
	}
	for _, source := range reviewSources {
		currentSource, authorized := app.authorizedScoutChatResultArtifact(ctx, viewer, source.ID)
		if !authorized || !dissentEvidenceSnapshotEqual(source, currentSource) {
			return execution, nil
		}
	}
	return execution, assurance
}

func dissentEvidenceSnapshotEqual(left, right meetingMemoryEntry) bool {
	if !artifactAuthorizationHeaderEqual(artifactAuthorizationHeaderFromEntry(left), artifactAuthorizationHeaderFromEntry(right)) {
		return false
	}
	leftDigest, leftErr := dissentReceiptDigest(left.Metadata)
	rightDigest, rightErr := dissentReceiptDigest(right.Metadata)
	return leftErr == nil && rightErr == nil && leftDigest == rightDigest
}

func dissentReceiptMatchesArtifactVersion(receipt dissentDocumentExecutionReceipt, artifact meetingMemoryEntry) bool {
	if receipt.OutputVersion < 1 {
		return false
	}
	if receipt.OutputVersion == artifactVersion(artifact) {
		return true
	}
	// Rendering appends assets as a new artifact revision without rewriting the
	// generated document. Accept only that renderer's exact source/version bind.
	return artifact.Metadata["renderStatus"] == renderJobStatusComplete &&
		artifact.Metadata[renderSourceArtifactVersionMetadataKey] == strconv.Itoa(receipt.OutputVersion) &&
		artifact.Metadata[renderPDFArtifactVersionMetadataKey] == strconv.Itoa(artifactVersion(artifact))
}
