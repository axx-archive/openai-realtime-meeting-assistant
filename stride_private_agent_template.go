package main

import (
	"fmt"
	"strings"
	"time"
)

// STRIDEProductPrivateTemplateRequest is the entire organization-private
// authoring surface. It intentionally has no code, command, hook, environment,
// credential, network, tool-server, or raw MCP field. The HTTP decoder rejects
// unknown fields before this request reaches the product state.
type STRIDEProductPrivateTemplateRequest struct {
	TemplateID            string   `json:"templateId"`
	DisplayName           string   `json:"displayName"`
	Category              string   `json:"category"`
	OutcomeSummary        string   `json:"outcomeSummary"`
	PersonalitySummary    string   `json:"personalitySummary"`
	SampleOutputs         []string `json:"sampleOutputs"`
	RequestedCapabilities []string `json:"requestedCapabilities"`
	RequiredAccess        []string `json:"requiredAccess"`
	CostBand              string   `json:"costBand"`
	Memberships           []string `json:"memberships"`
	PerRunBudgetCents     int64    `json:"perRunBudgetCents"`
	DailyBudgetCents      int64    `json:"dailyBudgetCents"`
	MonthlyBudgetCents    int64    `json:"monthlyBudgetCents"`
	Concurrency           int      `json:"concurrency"`
	Proactivity           string   `json:"proactivity"`
}

func (request STRIDEProductPrivateTemplateRequest) normalized() (STRIDEProductPrivateTemplateRequest, error) {
	request.TemplateID = strings.TrimSpace(request.TemplateID)
	request.DisplayName = trimForStorage(strings.TrimSpace(request.DisplayName), 80)
	request.Category = strings.TrimSpace(request.Category)
	request.OutcomeSummary = trimForStorage(strings.TrimSpace(request.OutcomeSummary), 600)
	request.PersonalitySummary = trimForStorage(strings.TrimSpace(request.PersonalitySummary), 400)
	request.CostBand = strings.TrimSpace(request.CostBand)
	request.RequestedCapabilities = uniqueSortedStrings(request.RequestedCapabilities)
	request.RequiredAccess = uniqueSortedStrings(request.RequiredAccess)
	request.Memberships = uniqueSortedStrings(request.Memberships)
	for index := range request.SampleOutputs {
		request.SampleOutputs[index] = trimForStorage(strings.TrimSpace(request.SampleOutputs[index]), 120)
	}
	request.SampleOutputs = uniqueSortedStrings(request.SampleOutputs)
	if !strideIdentifier(request.TemplateID) || request.DisplayName == "" || !strideIdentifier(request.Category) || request.OutcomeSummary == "" || request.PersonalitySummary == "" ||
		len(request.SampleOutputs) == 0 || len(request.SampleOutputs) > 5 || len(request.RequestedCapabilities) == 0 || len(request.RequiredAccess) == 0 ||
		!uniqueSTRIDEIDs(request.RequestedCapabilities) || !uniqueSTRIDEIDs(request.RequiredAccess) || !strideIdentifier(request.CostBand) || !uniqueSTRIDEIDs(request.Memberships) ||
		request.PerRunBudgetCents < 0 || request.PerRunBudgetCents > 100_000 || request.DailyBudgetCents < 0 || request.DailyBudgetCents > 1_000_000 ||
		request.MonthlyBudgetCents < 0 || request.MonthlyBudgetCents > 10_000_000 || request.Concurrency < 1 || request.Concurrency > 8 || !oneOf(request.Proactivity, "disabled", "quiet") {
		return STRIDEProductPrivateTemplateRequest{}, ErrSTRIDEProductInvalid
	}
	for _, sample := range request.SampleOutputs {
		if sample == "" {
			return STRIDEProductPrivateTemplateRequest{}, ErrSTRIDEProductInvalid
		}
	}
	return request, nil
}

func (state *STRIDEProductState) createPrivateTemplateCandidate(request STRIDEProductPrivateTemplateRequest, owner string, now time.Time) (STRIDEProductMarketplaceCandidate, bool, error) {
	if state == nil || !strideIdentifier(owner) || now.IsZero() {
		return STRIDEProductMarketplaceCandidate{}, false, ErrSTRIDEProductInvalid
	}
	normalized, err := request.normalized()
	if err != nil {
		return STRIDEProductMarketplaceCandidate{}, false, err
	}
	requestDigest, err := STRIDEContractDigest(normalized)
	if err != nil {
		return STRIDEProductMarketplaceCandidate{}, false, ErrSTRIDEProductInvalid
	}
	packageID := "org-private-" + normalized.TemplateID + "-v1"
	packageRef := STRIDEReference{ContractType: STRIDEContractAgentPackageManifest, ID: "package_" + normalized.TemplateID, Revision: 1, Digest: temporalDigest("private-package:" + requestDigest)}
	evidence := STRIDEReference{ContractType: STRIDEContractOutcome, ID: "template_evidence_" + normalized.TemplateID, Revision: 1, Digest: temporalDigest("template-evidence:" + requestDigest)}
	template := STRIDEAgentTemplate{
		TemplateID: normalized.TemplateID, Package: packageRef, Category: normalized.Category,
		OutcomeDigest: temporalDigest(normalized.OutcomeSummary), PersonalityDigest: temporalDigest(normalized.PersonalitySummary), Evidence: []STRIDEReference{evidence},
		AccessSummaryDigest: temporalDigest(strings.Join(normalized.RequiredAccess, "\x00")), CostBand: normalized.CostBand, Memberships: normalized.Memberships,
		PerRunBudgetCents: normalized.PerRunBudgetCents, DailyBudgetCents: normalized.DailyBudgetCents, MonthlyBudgetCents: normalized.MonthlyBudgetCents,
		Concurrency: normalized.Concurrency, Proactivity: normalized.Proactivity,
	}
	if template.Validate() != nil {
		return STRIDEProductMarketplaceCandidate{}, false, ErrSTRIDEProductInvalid
	}
	candidate := STRIDEProductMarketplaceCandidate{
		ID: "private-" + normalized.TemplateID, PackageID: packageID, DisplayName: normalized.DisplayName, Category: normalized.Category,
		OutcomeSummary: normalized.OutcomeSummary, PersonalitySummary: normalized.PersonalitySummary, SampleOutputs: append([]string(nil), normalized.SampleOutputs...),
		Capabilities: append([]string(nil), normalized.RequestedCapabilities...), RequiredAccess: append([]string(nil), normalized.RequiredAccess...),
		AccessSummary: "Organization-private template; access remains limited to explicitly approved memberships and assignments.", CostBand: normalized.CostBand,
		Publisher: "Your organization", Version: "1.0.0-preview", Provenance: "organization_authored_template", Visibility: "organization_private", UpdatePolicy: "human_approval",
		MemoryPolicy: "Company-owned, source-linked learning with human inspection, correction, forget, and purge controls.", PackageDigest: temporalDigest(packageID + "\x00" + requestDigest),
		ReceiptStatus: map[string]bool{"package": true, "closedTemplate": true, "deterministicSample": true, "rollback": true, "providerQuality": false, "humanAdmission": false},
		Availability:  "internal_preview", LiveAvailable: false, ProviderExecutionFenced: true,
	}
	if validateSTRIDEProductCandidate(candidate) != nil {
		return STRIDEProductMarketplaceCandidate{}, false, ErrSTRIDEProductInvalid
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if prior, exists := state.candidates[candidate.ID]; exists {
		if workDigest(prior) == workDigest(candidate) {
			return cloneSTRIDEProductCandidate(prior), false, nil
		}
		return STRIDEProductMarketplaceCandidate{}, false, ErrSTRIDEProductConflict
	}
	state.candidates[candidate.ID] = cloneSTRIDEProductCandidate(candidate)
	return cloneSTRIDEProductCandidate(candidate), true, nil
}

func stridePrivateTemplateLifecycleLabel(candidate STRIDEProductMarketplaceCandidate) string {
	return fmt.Sprintf("%s:%s:%s", candidate.ID, candidate.Availability, candidate.PackageID)
}
