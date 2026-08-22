package main

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var processEvidenceDossierReceiptPattern = regexp.MustCompile(`(?m)<!--\s*stride-process-evidence-dossier:v1 process=([a-z0-9_-]+) external=([0-9]+) internal=([0-9]+) digest=([a-f0-9]{64})\s*-->`)

type processInternalClaim struct {
	ID         string
	Claim      string
	ExactQuote string
	SourceRef  string
}

type processInternalAuthoritySource struct {
	Ref  string
	Text string
}

type processExternalClaim struct {
	ID                 string
	Claim              string
	SourceTitle        string
	RequestedURL       string
	FinalURL           string
	PublishedOrUpdated string
	Units              string
	ExactQuote         string
	WindowDigest       string
	SourceAnchor       string
}

var processExternalManifestColumns = []string{
	"Claim ID", "Exact claim", "Source title", "Requested URL", "Final URL", "Published / updated", "Units", "Exact source quote", "Window digest", "Source anchor", "Status",
}

var processInternalManifestColumns = []string{
	"Claim ID", "Exact claim", "Source ref", "Exact source quote", "Status",
}

var processMissingExternalProofColumns = []string{
	"Candidate ID", "Candidate fact", "Source title", "URL", "Proof status", "Missing proof",
}

var processResearchQuestionCoverageColumns = []string{
	"Research question", "Candidate claims", "Admitted claims", "Strong claims", "Coverage",
}

const (
	processEvidenceAdequacyNotRequired  = "not_required"
	processEvidenceAdequacySufficient   = "sufficient"
	processEvidenceAdequacyInsufficient = "insufficient"

	processResearchCoverageSupported = "supported"
	processResearchCoverageWeak      = "weak"
	processResearchCoveragePartial   = "partial"
	processResearchCoverageMissing   = "missing"
)

// processResearchQuestionCoverage is deliberately small and replayable. The
// compile seam derives it from the frozen question authority, provider
// candidates, source snapshots, and entailment rows; later stages validate the
// durable counters and digest without resolving or fetching any source again.
type processResearchQuestionCoverage struct {
	Question   string `json:"question"`
	Candidates int    `json:"candidates"`
	Admitted   int    `json:"admitted"`
	Strong     int    `json:"strong"`
	Coverage   string `json:"coverage"`
}

func processResearchQuestionCoverageStatus(candidates, admitted, strong int) (string, error) {
	if candidates < 0 || admitted < 0 || strong < 0 || admitted > candidates || strong > admitted {
		return "", fmt.Errorf("research question coverage counts are inconsistent")
	}
	switch {
	case strong > 0:
		return processResearchCoverageSupported, nil
	case admitted > 0:
		return processResearchCoverageWeak, nil
	case candidates > 0:
		return processResearchCoveragePartial, nil
	default:
		return processResearchCoverageMissing, nil
	}
}

func processResearchQuestionCoverageDigest(coverage []processResearchQuestionCoverage) string {
	raw, err := json.Marshal(coverage)
	if err != nil {
		return ""
	}
	return sha256Hex(raw)
}

// processExternalResearchQuestionCoverage binds every provider candidate and
// admitted row back to one exact authorized question. A claim is strong only
// when both discovery and the independent entailment seat call it High
// confidence; decision-grade source quality and exact relevance were already
// required for the row to enter admittedRows. Thus weak/partial/missing proof
// stays visible for audit but cannot become story authority.
func processExternalResearchQuestionCoverage(authorities []externalEvidenceResearchQuestionAuthority, authority externalEvidenceEntailmentAuthority, admittedRows [][]string) ([]processResearchQuestionCoverage, error) {
	if len(authorities) < 1 || len(authorities) > 3 {
		return nil, fmt.Errorf("external evidence must bind one to three exact authorized research questions")
	}
	coverage := make([]processResearchQuestionCoverage, len(authorities))
	questionIndex := make(map[string]int, len(authorities))
	for index, item := range authorities {
		question := canonicalEvidenceText(item.Question)
		if question == "" || questionIndex[question] != 0 {
			return nil, fmt.Errorf("external evidence research questions are empty or duplicated")
		}
		coverage[index].Question = question
		questionIndex[question] = index + 1
	}

	snapshots := make(map[string]externalSourceSnapshot, len(authority.SourceEnvelope.Snapshots))
	for index, snapshot := range authority.SourceEnvelope.Snapshots {
		candidateID := strings.TrimSpace(snapshot.CandidateID)
		candidate, ok := authority.Candidates[candidateID]
		question := canonicalEvidenceText(snapshot.ResearchQuestion)
		position := questionIndex[question] - 1
		if !ok || position < 0 || candidateID != externalEvidenceCandidateID(candidate) || canonicalEvidenceText(candidate.ResearchQuestion) != question || snapshots[candidateID].CandidateID != "" {
			return nil, fmt.Errorf("external evidence source snapshot %d is outside the exact authorized question manifest", index+1)
		}
		snapshot.CandidateID = candidateID
		snapshots[candidateID] = snapshot
		coverage[position].Candidates++
	}
	if len(snapshots) != len(authority.Candidates) {
		return nil, fmt.Errorf("external evidence source snapshots do not cover every provider candidate")
	}

	seenAdmitted := map[string]bool{}
	for index, row := range admittedRows {
		if len(row) != len(externalEvidenceEntailmentColumns) {
			return nil, fmt.Errorf("admitted external evidence row %d is malformed", index+1)
		}
		candidateID := strings.TrimSpace(row[0])
		candidate, candidateOK := authority.Candidates[candidateID]
		snapshot, snapshotOK := snapshots[candidateID]
		question := canonicalEvidenceText(snapshot.ResearchQuestion)
		position := questionIndex[question] - 1
		if !candidateOK || !snapshotOK || position < 0 || seenAdmitted[candidateID] || strings.TrimSpace(row[1]) != strings.TrimSpace(candidate.SourceFact) || strings.TrimSpace(row[3]) != strings.TrimSpace(candidate.URL) {
			return nil, fmt.Errorf("admitted external evidence row %d is outside the exact authorized question manifest", index+1)
		}
		seenAdmitted[candidateID] = true
		coverage[position].Admitted++
		if strings.EqualFold(strings.TrimSpace(candidate.Confidence), "High") && strings.EqualFold(strings.TrimSpace(row[10]), "High") {
			coverage[position].Strong++
		}
	}
	for index := range coverage {
		status, err := processResearchQuestionCoverageStatus(coverage[index].Candidates, coverage[index].Admitted, coverage[index].Strong)
		if err != nil {
			return nil, err
		}
		coverage[index].Coverage = status
	}
	return coverage, nil
}

func canonicalProcessResearchQuestionCoverageManifest(researchMode string, coverage []processResearchQuestionCoverage) (string, string, int, string, error) {
	lines := []string{
		"## Research question coverage",
		"| " + strings.Join(processResearchQuestionCoverageColumns, " | ") + " |",
		"|---|---|---|---|---|",
	}
	if researchMode != "external" {
		if len(coverage) != 0 {
			return "", "", 0, "", fmt.Errorf("non-external research cannot carry external question coverage")
		}
		lines = append(lines, "| None required | 0 | 0 | 0 | not_required |")
		return strings.Join(lines, "\n"), processEvidenceAdequacyNotRequired, 0, processResearchQuestionCoverageDigest([]processResearchQuestionCoverage{}), nil
	}
	strongQuestions := 0
	for _, item := range coverage {
		if item.Coverage == processResearchCoverageSupported {
			strongQuestions++
		}
		lines = append(lines, "| "+strings.Join([]string{
			externalEvidenceMarkdownCell(item.Question), strconv.Itoa(item.Candidates), strconv.Itoa(item.Admitted), strconv.Itoa(item.Strong), item.Coverage,
		}, " | ")+" |")
	}
	adequacy := processEvidenceAdequacyInsufficient
	if len(coverage) > 0 && strongQuestions == len(coverage) {
		adequacy = processEvidenceAdequacySufficient
	}
	return strings.Join(lines, "\n"), adequacy, strongQuestions, processResearchQuestionCoverageDigest(coverage), nil
}

func processResearchQuestionCoverageRows(body, researchMode string) ([]processResearchQuestionCoverage, error) {
	rows, err := processManifestTableRows(body, "Research question coverage", processResearchQuestionCoverageColumns)
	if err != nil {
		return nil, err
	}
	if researchMode != "external" {
		want := []string{"None required", "0", "0", "0", processEvidenceAdequacyNotRequired}
		if len(rows) != 1 {
			return nil, fmt.Errorf("non-external research coverage sentinel is not canonical")
		}
		for index := range want {
			if strings.TrimSpace(rows[0][index]) != want[index] {
				return nil, fmt.Errorf("non-external research coverage sentinel is not canonical")
			}
		}
		return []processResearchQuestionCoverage{}, nil
	}
	if len(rows) < 1 || len(rows) > 3 {
		return nil, fmt.Errorf("external research coverage must contain one to three questions")
	}
	coverage := make([]processResearchQuestionCoverage, 0, len(rows))
	seen := map[string]bool{}
	for index, row := range rows {
		question := canonicalEvidenceText(row[0])
		candidates, candidatesErr := strconv.Atoi(strings.TrimSpace(row[1]))
		admitted, admittedErr := strconv.Atoi(strings.TrimSpace(row[2]))
		strong, strongErr := strconv.Atoi(strings.TrimSpace(row[3]))
		status, statusErr := processResearchQuestionCoverageStatus(candidates, admitted, strong)
		if question == "" || seen[question] || candidatesErr != nil || admittedErr != nil || strongErr != nil || statusErr != nil || strings.TrimSpace(row[4]) != status {
			return nil, fmt.Errorf("external research coverage row %d is malformed", index+1)
		}
		seen[question] = true
		coverage = append(coverage, processResearchQuestionCoverage{Question: question, Candidates: candidates, Admitted: admitted, Strong: strong, Coverage: status})
	}
	return coverage, nil
}

func canonicalEvidenceText(value string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
}

func processInternalAuthoritySources(app *kanbanBoardApp, plan *goalPlan) (map[string]processInternalAuthoritySource, error) {
	if app == nil || plan == nil {
		return nil, fmt.Errorf("internal evidence authority is unavailable")
	}
	sources := map[string]processInternalAuthoritySource{}
	add := func(ref, body string) {
		ref, body = canonicalEvidenceText(ref), canonicalEvidenceText(body)
		if ref != "" && body != "" {
			// The route selection contains the exact source body. A later display
			// line from Company Brain may carry the same reference plus UI labels;
			// never let that presentation wrapper replace the exact authority text.
			if _, exists := sources[ref]; exists {
				return
			}
			sources[ref] = processInternalAuthoritySource{Ref: ref, Text: body}
		}
	}
	engine := newGoalEngine(app)
	var packetErr error
	if app.externalEvidenceSourcePacket != nil {
		_, packetErr = app.externalEvidenceSourcePacket(context.Background(), plan)
	} else {
		_, packetErr = engine.processStageSourcePacket(context.Background(), plan)
	}
	if packetErr != nil {
		return nil, packetErr
	}
	if plan.RouteReceipt != nil {
		selection, err := app.goalRouteSourceSelection(*plan.RouteReceipt)
		if err != nil || selection.Digest != strings.TrimSpace(plan.RouteReceipt.SourceSelectionDigest) {
			return nil, fmt.Errorf("internal evidence source selection is no longer authorized")
		}
		for _, source := range selection.InternalEvidenceSources {
			add(source.Ref, source.Text)
		}
	}

	company := engine.processStageCompanyContext(plan)
	if strings.TrimSpace(company) == "" {
		return sources, nil
	}
	sharedDestination := false
	if receipt := plan.RouteReceipt; receipt != nil && strings.TrimSpace(receipt.OriginID) != "" {
		if thread, _, err := app.scoutChatThreadByID(receipt.Requester, receipt.OriginID); err == nil {
			sharedDestination = normalizeScoutChatVisibility(thread.Visibility) != "private"
		}
	}
	var scoped *kanbanBoardApp
	if sharedDestination {
		scoped = app.scopedRecallApp(context.Background(), sharedRoomRecallPrincipal(officeRoomID, ""))
	} else if requester, ok := authenticatedRequester(goalPlanRequestedBy(*plan)); ok {
		scoped = app.scopedRecallApp(context.Background(), recallPrincipalForUser(requester))
	}
	if scoped == nil || scoped.memory == nil {
		return sources, nil
	}
	knownRefs := map[string]string{}
	for _, entry := range scoped.memory.entriesOfKind(meetingMemoryKindOSArtifact, 40) {
		knownRefs[fmt.Sprintf("artifact_id=%s revision=%d digest=%s", entry.ID, artifactVersion(entry), sha256Hex([]byte(entry.Text)))] = entry.Text
	}
	for _, entry := range scoped.memory.entriesOfKind(meetingMemoryKindDecision, 40) {
		knownRefs[fmt.Sprintf("decision_id=%s digest=%s", entry.ID, sha256Hex([]byte(entry.Text)))] = entry.Text
	}
	for _, entry := range scoped.memorySnapshotForClients(12) {
		knownRefs[fmt.Sprintf("source_id=%s digest=%s", entry.ID, sha256Hex([]byte(entry.Text)))] = entry.Text
	}
	for _, line := range strings.Split(company, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "- [") {
			continue
		}
		end := strings.Index(line, "]")
		if end < 3 {
			continue
		}
		ref := canonicalEvidenceText(line[3:end])
		if sourceText := knownRefs[ref]; sourceText != "" {
			add(ref, sourceText)
		}
	}
	return sources, nil
}

// processResearchObjectiveAuthoritySource is an intent-only authority. It lets
// the context stage bind a proposed web question to the exact approved goal,
// but it is deliberately kept out of processInternalAuthoritySources so a user
// request can never become factual evidence merely because it was requested.
func processResearchObjectiveAuthoritySource(plan *goalPlan) (processInternalAuthoritySource, bool) {
	if plan == nil {
		return processInternalAuthoritySource{}, false
	}
	goalID := strings.TrimSpace(plan.GoalID)
	objective := strings.TrimSpace(plan.Objective)
	if goalID == "" || objective == "" {
		return processInternalAuthoritySource{}, false
	}
	ref := fmt.Sprintf("goal_objective_id=%s digest=%s", goalID, sha256Hex([]byte(plan.Objective)))
	return processInternalAuthoritySource{Ref: ref, Text: objective}, true
}

// processResearchAuthoritySources is the per-question research scope map. It
// reuses every currently authorized factual source revision, then adds the
// exact approved objective as a separate intent-only source. The generated
// context artifact is never part of this map, preventing a model-authored
// question from authorizing itself by appearing in that same JSON object.
func processResearchAuthoritySources(app *kanbanBoardApp, plan *goalPlan) (map[string]processInternalAuthoritySource, error) {
	sources, err := processInternalAuthoritySources(app, plan)
	if err != nil {
		return nil, err
	}
	objective, ok := processResearchObjectiveAuthoritySource(plan)
	if !ok {
		return nil, fmt.Errorf("approved research objective is unavailable")
	}
	if _, exists := sources[objective.Ref]; exists {
		return nil, fmt.Errorf("approved research objective identity collides with a factual source")
	}
	sources[objective.Ref] = objective
	return sources, nil
}

func processInternalSourceEntailsExactClaim(claim, sourceText string) bool {
	claim = externalEvidenceCanonicalAssertion(claim)
	sourceText = externalEvidenceCanonicalAssertion(sourceText)
	if claim == "" || sourceText == "" {
		return false
	}
	// A source that is exactly the asserted sentence/body is allowed even when
	// the author omitted terminal punctuation. It still must not be an
	// attribution, uncertainty statement, or negation.
	if claim == sourceText {
		return !strings.HasSuffix(claim, "?") &&
			!externalEvidenceConditionalAssertionPattern.MatchString(claim) &&
			!externalEvidenceAttributedAssertionPattern.MatchString(claim) &&
			!externalEvidenceNegationPattern.MatchString(claim) &&
			!externalEvidenceUncertaintyPattern.MatchString(claim)
	}
	for _, item := range externalEvidenceAssertionContexts(sourceText) {
		if item.Assertion != claim {
			continue
		}
		window := externalSourceWindow{Anchor: "Internal source", Assertion: item.Assertion, Text: item.Context}
		window.Digest = externalEvidenceSourceWindowDigest(window.Anchor, window.Assertion, window.Text)
		if externalEvidenceWindowEntailsCandidate(claim, window) {
			return true
		}
	}
	return false
}

func processInternalAdmittedClaims(app *kanbanBoardApp, plan *goalPlan, contextObject map[string]any) ([]processInternalClaim, int, error) {
	if app == nil || plan == nil {
		return nil, 0, fmt.Errorf("internal evidence authority is unavailable")
	}
	authority, err := processInternalAuthoritySources(app, plan)
	if err != nil {
		return nil, 0, err
	}
	field := "known_facts"
	if plan.ProcessID == documentReportProcessID {
		field = "settled_facts"
	}
	rawClaims, _ := contextObject[field].([]any)
	admitted := make([]processInternalClaim, 0, len(rawClaims))
	rejected := 0
	seen := map[string]bool{}
	for _, raw := range rawClaims {
		object, ok := raw.(map[string]any)
		if !ok {
			rejected++
			continue
		}
		claim, _ := object["claim"].(string)
		quote, _ := object["exact_quote"].(string)
		sourceRef, _ := object["source_ref"].(string)
		claim, quote, sourceRef = canonicalEvidenceText(claim), canonicalEvidenceText(quote), canonicalEvidenceText(sourceRef)
		source, validRef := authority[sourceRef]
		if claim == "" || len(claim) > 2000 || claim != quote || !validRef || !processInternalSourceEntailsExactClaim(claim, source.Text) {
			rejected++
			continue
		}
		id := sha256Hex([]byte(sourceRef + "\x00" + quote))
		if seen[id] {
			rejected++
			continue
		}
		seen[id] = true
		admitted = append(admitted, processInternalClaim{ID: id, Claim: claim, ExactQuote: quote, SourceRef: sourceRef})
	}
	return admitted, rejected, nil
}

func processManifestTableRows(body, heading string, columns []string) ([][]string, error) {
	lines := strings.Split(body, "\n")
	inSection := false
	foundSection := false
	foundHeader := false
	wantSeparator := false
	rows := make([][]string, 0)
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			current := strings.ToLower(strings.TrimSpace(strings.TrimLeft(trimmed, "#")))
			wanted := current == strings.ToLower(strings.TrimSpace(heading))
			if inSection && !wanted {
				break
			}
			if wanted {
				if foundSection {
					return nil, fmt.Errorf("duplicate %s section", heading)
				}
				foundSection = true
				inSection = true
			}
			continue
		}
		if !inSection || !strings.HasPrefix(trimmed, "|") || !strings.HasSuffix(trimmed, "|") {
			continue
		}
		cells := markdownEvidenceTableCells(trimmed)
		if !foundHeader {
			if len(cells) != len(columns) {
				return nil, fmt.Errorf("%s table has %d columns, want %d", heading, len(cells), len(columns))
			}
			for index, column := range columns {
				if !strings.EqualFold(strings.TrimSpace(cells[index]), column) {
					return nil, fmt.Errorf("%s table column %d is %q, want %q", heading, index+1, cells[index], column)
				}
			}
			foundHeader = true
			wantSeparator = true
			continue
		}
		if wantSeparator {
			if len(cells) != len(columns) {
				return nil, fmt.Errorf("%s separator is malformed", heading)
			}
			for _, cell := range cells {
				if !regexp.MustCompile(`^:?-{3,}:?$`).MatchString(strings.TrimSpace(cell)) {
					return nil, fmt.Errorf("%s separator is malformed", heading)
				}
			}
			wantSeparator = false
			continue
		}
		if len(cells) != len(columns) {
			return nil, fmt.Errorf("%s row has %d columns, want %d", heading, len(cells), len(columns))
		}
		rows = append(rows, cells)
	}
	if !foundSection || !foundHeader || wantSeparator {
		return nil, fmt.Errorf("missing complete %s table", heading)
	}
	return rows, nil
}

func processExternalManifestRows(body string) ([]processExternalClaim, error) {
	rows, err := processManifestTableRows(body, "Entailment-checked claims", processExternalManifestColumns)
	if err != nil {
		return nil, err
	}
	claims := make([]processExternalClaim, 0, len(rows))
	seen := map[string]bool{}
	for index, row := range rows {
		if strings.EqualFold(strings.TrimSpace(row[0]), "None admitted") {
			want := []string{"None admitted", "None admitted", "N/A", "N/A", "N/A", "N/A", "N/A", "N/A", "N/A", "N/A", "unavailable"}
			if len(rows) != 1 {
				return nil, fmt.Errorf("None admitted sentinel is mixed with external claims")
			}
			for cell := range want {
				if strings.TrimSpace(row[cell]) != want[cell] {
					return nil, fmt.Errorf("None admitted external sentinel is not canonical")
				}
			}
			return nil, nil
		}
		claim := processExternalClaim{
			ID: strings.TrimSpace(row[0]), Claim: strings.TrimSpace(row[1]), SourceTitle: strings.TrimSpace(row[2]),
			RequestedURL: strings.TrimSpace(row[3]), FinalURL: strings.TrimSpace(row[4]), PublishedOrUpdated: strings.TrimSpace(row[5]),
			Units: strings.TrimSpace(row[6]), ExactQuote: strings.TrimSpace(row[7]), WindowDigest: strings.TrimSpace(row[8]), SourceAnchor: strings.TrimSpace(row[9]),
		}
		if !isHexDigest(claim.ID) || claim.Claim == "" || claim.SourceTitle == "" || claim.ExactQuote == "" || claim.SourceAnchor == "" || claim.PublishedOrUpdated == "" || claim.Units == "" || !isHexDigest(claim.WindowDigest) || strings.TrimSpace(row[10]) != "external_source_bound" {
			return nil, fmt.Errorf("external claim row %d is malformed", index+1)
		}
		if _, ok := parseBareHTTPSURL(claim.RequestedURL); !ok || seen[claim.ID] {
			return nil, fmt.Errorf("external claim row %d has an invalid URL or duplicate identity", index+1)
		}
		if _, ok := parseBareHTTPSURL(claim.FinalURL); !ok {
			return nil, fmt.Errorf("external claim row %d has an invalid final URL", index+1)
		}
		seen[claim.ID] = true
		claims = append(claims, claim)
	}
	return claims, nil
}

func processInternalManifestRows(body string) ([]processInternalClaim, error) {
	rows, err := processManifestTableRows(body, "Internally admitted claims", processInternalManifestColumns)
	if err != nil {
		return nil, err
	}
	claims := make([]processInternalClaim, 0, len(rows))
	seen := map[string]bool{}
	for index, row := range rows {
		if strings.EqualFold(strings.TrimSpace(row[0]), "None admitted") {
			want := []string{"None admitted", "None admitted", "N/A", "N/A", "unavailable"}
			if len(rows) != 1 {
				return nil, fmt.Errorf("None admitted sentinel is mixed with internal claims")
			}
			for cell := range want {
				if strings.TrimSpace(row[cell]) != want[cell] {
					return nil, fmt.Errorf("None admitted internal sentinel is not canonical")
				}
			}
			return nil, nil
		}
		claim := processInternalClaim{ID: strings.TrimSpace(row[0]), Claim: strings.TrimSpace(row[1]), SourceRef: strings.TrimSpace(row[2]), ExactQuote: strings.TrimSpace(row[3])}
		if !isHexDigest(claim.ID) || claim.Claim == "" || claim.Claim != claim.ExactQuote || claim.SourceRef == "" || strings.TrimSpace(row[4]) != "internal_source_bound" || claim.ID != sha256Hex([]byte(claim.SourceRef+"\x00"+claim.ExactQuote)) || seen[claim.ID] {
			return nil, fmt.Errorf("internal claim row %d is malformed", index+1)
		}
		seen[claim.ID] = true
		claims = append(claims, claim)
	}
	return claims, nil
}

func canonicalExternalEvidenceManifest(app *kanbanBoardApp, thread scoutAgentThread, body string) (string, int, error) {
	if err := validateExternalEvidenceEntailmentArtifact(app, thread, body); err != nil {
		return "", 0, err
	}
	authority, err := authorizedExternalEvidenceEntailmentAuthority(app, thread)
	if err != nil {
		return "", 0, err
	}
	rows, err := externalEvidenceEntailmentAdmittedRows(body)
	if err != nil {
		return "", 0, err
	}
	lines := []string{
		"## Entailment-checked claims",
		"| " + strings.Join(processExternalManifestColumns, " | ") + " |",
		"|---|---|---|---|---|---|---|---|---|---|---|",
	}
	for _, row := range rows {
		candidateID := strings.TrimSpace(row[0])
		candidate, candidateOK := authority.Candidates[candidateID]
		var snapshot externalSourceSnapshot
		for _, current := range authority.SourceEnvelope.Snapshots {
			if current.CandidateID == candidateID {
				snapshot = current
				break
			}
		}
		if !candidateOK || snapshot.CandidateID == "" || snapshot.Status != "fetched_with_relevant_text" {
			return "", 0, fmt.Errorf("admitted external claim is absent from the current authority manifest")
		}
		cells := []string{
			candidateID, candidate.SourceFact, firstNonEmptyString(snapshot.SourceTitle, "Provider-fetched source"), candidate.URL,
			snapshot.FinalURL, candidate.PublishedOrUpdated, candidate.Units, row[4], row[5], row[6], "external_source_bound",
		}
		for index := range cells {
			cells[index] = externalEvidenceMarkdownCell(cells[index])
		}
		lines = append(lines, "| "+strings.Join(cells, " | ")+" |")
	}
	if len(rows) == 0 {
		lines = append(lines, "| None admitted | None admitted | N/A | N/A | N/A | N/A | N/A | N/A | N/A | N/A | unavailable |")
	}
	return strings.Join(lines, "\n"), len(rows), nil
}

// canonicalExternalMissingProofManifest preserves the identity of evidence that
// could not be admitted without making it available as factual authority. The
// row is reconstructed from the server-bound candidate/source snapshot, and the
// explanation is deterministic; model-authored rejection prose never becomes a
// downstream fact. This is especially important for direct PDFs: the process
// can finish honestly while naming the exact source that still needs isolated
// text extraction.
func canonicalExternalMissingProofManifest(app *kanbanBoardApp, thread scoutAgentThread, body string) (string, int, error) {
	if err := validateExternalEvidenceEntailmentArtifact(app, thread, body); err != nil {
		return "", 0, err
	}
	authority, err := authorizedExternalEvidenceEntailmentAuthority(app, thread)
	if err != nil {
		return "", 0, err
	}
	rows, err := processManifestTableRows(body, "Rejected or unclear candidate claims", externalEvidenceEntailmentColumns)
	if err != nil {
		return "", 0, err
	}
	snapshots := make(map[string]externalSourceSnapshot, len(authority.SourceEnvelope.Snapshots))
	for _, snapshot := range authority.SourceEnvelope.Snapshots {
		snapshots[strings.TrimSpace(snapshot.CandidateID)] = snapshot
	}
	lines := []string{
		"## Missing external proof",
		"These rows are explicitly unavailable as factual authority. They identify sources Scout could not prove from the captured source text; downstream work may describe the gap but may not use the candidate fact as true.",
		"| " + strings.Join(processMissingExternalProofColumns, " | ") + " |",
		"|---|---|---|---|---|---|",
	}
	missingCount := 0
	for index, row := range rows {
		candidateID := strings.TrimSpace(row[0])
		if strings.EqualFold(candidateID, "None") {
			if len(rows) != 1 {
				return "", 0, fmt.Errorf("missing-proof sentinel is mixed with rejected rows")
			}
			break
		}
		candidate, candidateOK := authority.Candidates[candidateID]
		snapshot, snapshotOK := snapshots[candidateID]
		if !candidateOK || !snapshotOK || candidateID != externalEvidenceCandidateID(candidate) || strings.TrimSpace(row[1]) != strings.TrimSpace(candidate.SourceFact) || strings.TrimSpace(row[3]) != strings.TrimSpace(candidate.URL) {
			return "", 0, fmt.Errorf("missing external proof row %d is outside the current authority manifest", index+1)
		}
		status := strings.TrimSpace(snapshot.Status)
		reason := "The independent source check did not admit this candidate claim."
		switch status {
		case "extraction_required", "fetch_failed", "fetched_no_relevant_text":
			reason = firstNonEmptyString(strings.TrimSpace(snapshot.Note), reason)
		case "fetched_with_relevant_text":
			verdict := strings.ToLower(strings.TrimSpace(row[9]))
			if oneOf(verdict, "not_entailed", "unclear") {
				reason = "The captured source text was " + verdict + " for this exact candidate claim."
			}
		default:
			return "", 0, fmt.Errorf("missing external proof row %d has an unsupported source status", index+1)
		}
		title := firstNonEmptyString(strings.TrimSpace(snapshot.SourceTitle), strings.TrimSpace(row[2]), "Provider-fetched source")
		cells := []string{candidateID, candidate.SourceFact, title, candidate.URL, status, reason}
		for cell := range cells {
			cells[cell] = externalEvidenceMarkdownCell(cells[cell])
		}
		lines = append(lines, "| "+strings.Join(cells, " | ")+" |")
		missingCount++
	}
	if missingCount == 0 {
		lines = append(lines, "| None | None | N/A | N/A | available | No external proof gaps were recorded. |")
	}
	return strings.Join(lines, "\n"), missingCount, nil
}

func processContextDownstreamBrief(text, processID string) string {
	var object map[string]any
	if json.Unmarshal([]byte(extractJSONObject(text)), &object) != nil {
		return "{\"brief_error\":\"authorized context could not be decoded; do not infer missing facts\"}"
	}
	fields := []string{"direct_ask", "audience", "decision", "desired_response", "slide_count", "context_used", "settled_decisions", "taste_signals", "brand_assets", "research_mode", "research_questions", "reversible_inferences"}
	if processID == documentReportProcessID {
		fields = []string{"direct_ask", "reader", "decision", "intended_use", "document_shape", "scope", "voice", "constraints", "context_used", "research_mode", "research_questions", "reversible_inferences", "success_criteria"}
	}
	brief := map[string]any{}
	for _, key := range fields {
		if value, ok := object[key]; ok {
			if key == "research_questions" {
				raw, ok := value.([]any)
				if !ok {
					brief[key] = []string{}
					continue
				}
				questions := make([]string, 0, len(raw))
				for _, item := range raw {
					question := ""
					switch typed := item.(type) {
					case string:
						question = canonicalEvidenceText(typed)
					case map[string]any:
						question, _ = typed["question"].(string)
						question = canonicalEvidenceText(question)
					}
					if question != "" {
						questions = append(questions, question)
					}
				}
				brief[key] = questions
				continue
			}
			brief[key] = value
		}
	}
	raw, err := json.MarshalIndent(brief, "", "  ")
	if err != nil {
		return "{\"brief_error\":\"authorized context could not be encoded; do not infer missing facts\"}"
	}
	return string(raw)
}

// compileProcessEvidenceDossier is the deterministic admission seam between
// research and creative synthesis. It replaces a model-only evidence pass:
// downstream story/copy stages receive exact entailed external rows plus only
// the context snapshot's explicitly settled internal fields. Rejected claim
// text, raw provider discovery, and uncertain claims never enter their prompt.
func compileProcessEvidenceDossier(app *kanbanBoardApp, plan *goalPlan, parentID string, stage ProcessStage) (string, map[string]string, error) {
	if app == nil || plan == nil || stage.ID != "evidence" || (plan.ProcessID != packagingStudioProcessID && plan.ProcessID != documentReportProcessID) {
		return "", nil, fmt.Errorf("evidence dossier has no authorized process context")
	}
	parentID = strings.TrimSpace(parentID)
	parent, parentOK := app.osArtifactByID(parentID)
	if !parentOK || strings.TrimSpace(parent.Metadata["threadId"]) != strings.TrimSpace(plan.GoalID) || strings.TrimSpace(parent.Metadata["processId"]) != plan.ProcessID || strings.TrimSpace(parent.Metadata["processDigest"]) != strings.TrimSpace(plan.ProcessDigest) {
		return "", nil, fmt.Errorf("evidence dossier parent does not match the exact process goal")
	}
	contextStage := plan.subtaskByID("context_snapshot")
	if contextStage == nil || contextStage.Status != subtaskComplete || strings.TrimSpace(contextStage.ArtifactID) == "" {
		return "", nil, fmt.Errorf("completed context snapshot is required")
	}
	contextArtifact, ok := app.osArtifactByID(contextStage.ArtifactID)
	if !ok || strings.TrimSpace(contextArtifact.Metadata["goalParentId"]) != parentID || strings.TrimSpace(contextArtifact.Metadata["goalSubtaskId"]) != "context_snapshot" || strings.TrimSpace(contextArtifact.Metadata["processId"]) != plan.ProcessID || strings.TrimSpace(contextArtifact.Metadata["processStage"]) != "context_snapshot" {
		return "", nil, fmt.Errorf("context snapshot is not the exact completed process artifact")
	}
	var contextObject map[string]any
	if err := json.Unmarshal([]byte(extractJSONObject(contextArtifact.Text)), &contextObject); err != nil {
		return "", nil, fmt.Errorf("context snapshot is not valid structured context")
	}
	researchMode, _ := contextObject["research_mode"].(string)
	researchMode = strings.ToLower(strings.TrimSpace(researchMode))
	if !oneOf(researchMode, "none", "internal", "external") {
		return "", nil, fmt.Errorf("context snapshot has an invalid research mode")
	}
	internalFields := []string{"direct_ask", "context_used", "settled_decisions", "taste_signals", "brand_assets"}
	if plan.ProcessID == documentReportProcessID {
		internalFields = []string{"direct_ask", "context_used", "reader", "decision", "intended_use", "scope", "voice", "constraints", "success_criteria"}
	}
	internal := map[string]any{}
	for _, key := range internalFields {
		if value, exists := contextObject[key]; exists {
			internal[key] = value
		}
	}
	internalRaw, err := json.MarshalIndent(internal, "", "  ")
	if err != nil {
		return "", nil, err
	}
	internalClaims, internalRejected, err := processInternalAdmittedClaims(app, plan, contextObject)
	if err != nil {
		return "", nil, err
	}
	internalLines := []string{
		"## Internally admitted claims",
		"| Claim ID | Exact claim | Source ref | Exact source quote | Status |",
		"|---|---|---|---|---|",
	}
	for _, claim := range internalClaims {
		internalLines = append(internalLines, "| "+strings.Join([]string{
			externalEvidenceMarkdownCell(claim.ID), externalEvidenceMarkdownCell(claim.Claim), externalEvidenceMarkdownCell(claim.SourceRef), externalEvidenceMarkdownCell(claim.ExactQuote), "internal_source_bound",
		}, " | ")+" |")
	}
	if len(internalClaims) == 0 {
		internalLines = append(internalLines, "| None admitted | None admitted | N/A | N/A | unavailable |")
	}

	external := strings.Join([]string{
		"## Entailment-checked claims",
		"| " + strings.Join(processExternalManifestColumns, " | ") + " |",
		"|---|---|---|---|---|---|---|---|---|---|---|",
		"| None admitted | None admitted | N/A | N/A | N/A | N/A | N/A | N/A | N/A | N/A | unavailable |",
	}, "\n")
	externalCount := 0
	missingExternal := strings.Join([]string{
		"## Missing external proof",
		"These rows are explicitly unavailable as factual authority. They identify sources Scout could not prove from the captured source text; downstream work may describe the gap but may not use the candidate fact as true.",
		"| " + strings.Join(processMissingExternalProofColumns, " | ") + " |",
		"|---|---|---|---|---|---|",
		"| None | None | N/A | N/A | available | No external proof gaps were recorded. |",
	}, "\n")
	missingExternalCount := 0
	var researchCoverage []processResearchQuestionCoverage
	if researchMode == "external" {
		authorizedQuestions, authorizedMode, authorityErr := externalEvidenceResearchQuestionAuthoritiesFromText(contextArtifact.Text)
		if authorityErr != nil {
			return "", nil, fmt.Errorf("external research question authority is invalid: %w", authorityErr)
		}
		if authorizedMode != "external" {
			return "", nil, fmt.Errorf("external research question authority has mode %q", authorizedMode)
		}
		entailmentStage := plan.subtaskByID("evidence_entailment")
		if entailmentStage == nil || entailmentStage.Status != subtaskComplete || strings.TrimSpace(entailmentStage.ArtifactID) == "" {
			return "", nil, fmt.Errorf("completed evidence entailment is required for external research")
		}
		artifact, found := app.osArtifactByID(entailmentStage.ArtifactID)
		if !found {
			return "", nil, fmt.Errorf("evidence entailment artifact is unavailable")
		}
		thread := scoutAgentThread{
			ID:     firstNonEmptyString(strings.TrimSpace(artifact.Metadata["threadId"]), strings.TrimSpace(artifact.Metadata["latestThreadRun"])),
			Mode:   firstNonEmptyString(strings.TrimSpace(artifact.Metadata["mode"]), "artifacts"),
			Query:  firstNonEmptyString(strings.TrimSpace(artifact.Metadata["threadQuery"]), strings.TrimSpace(artifact.Metadata["query"])),
			Status: firstNonEmptyString(strings.TrimSpace(artifact.Metadata["threadStatus"]), "complete"), Artifact: artifact,
		}
		external, externalCount, err = canonicalExternalEvidenceManifest(app, thread, artifact.Text)
		if err != nil {
			return "", nil, err
		}
		missingExternal, missingExternalCount, err = canonicalExternalMissingProofManifest(app, thread, artifact.Text)
		if err != nil {
			return "", nil, err
		}
		entailmentAuthority, authorityErr := authorizedExternalEvidenceEntailmentAuthority(app, thread)
		if authorityErr != nil {
			return "", nil, authorityErr
		}
		admittedRows, admittedErr := externalEvidenceEntailmentAdmittedRows(artifact.Text)
		if admittedErr != nil {
			return "", nil, admittedErr
		}
		researchCoverage, err = processExternalResearchQuestionCoverage(authorizedQuestions, entailmentAuthority, admittedRows)
		if err != nil {
			return "", nil, err
		}
	}
	researchCoverageManifest, evidenceAdequacy, strongQuestionCount, researchCoverageDigest, err := canonicalProcessResearchQuestionCoverageManifest(researchMode, researchCoverage)
	if err != nil {
		return "", nil, err
	}

	body := strings.Join([]string{
		"## Evidence admission dossier",
		"Only the externally admitted claims below may be used as factual evidence. The decision context is direction, audience, taste, and constraints from the authorized brief; it is not a factual claim manifest and must not be presented as market proof.",
		researchCoverageManifest,
		external,
		missingExternal,
		strings.Join(internalLines, "\n"),
		"## Authorized decision context",
		"```json\n" + string(internalRaw) + "\n```",
		"## Admission rule",
		"Any external claim absent from Entailment-checked claims is unauthorized. Missing-proof rows disclose a gap; they do not license the candidate fact.",
	}, "\n\n")
	// OS artifacts preserve structure but canonicalize line whitespace at the
	// storage boundary. Sign those exact durable bytes; otherwise indented JSON
	// in Authorized decision context changes after append and the freshly
	// created dossier immediately fails its own receipt check.
	body = normalizeMemoryEntryText(meetingMemoryKindOSArtifact, body)
	digest := sha256Hex([]byte(body))
	body += fmt.Sprintf("\n\n<!-- stride-process-evidence-dossier:v1 process=%s external=%d internal=%d digest=%s -->", plan.ProcessID, externalCount, len(internalClaims), digest)
	return body, map[string]string{
		"evidenceAdmissionDigest":        digest,
		"externalClaimsAdmitted":         fmt.Sprintf("%d", externalCount),
		"externalClaimsMissing":          fmt.Sprintf("%d", missingExternalCount),
		"internalClaimsAdmitted":         fmt.Sprintf("%d", len(internalClaims)),
		"internalClaimsRejected":         fmt.Sprintf("%d", internalRejected),
		"researchMode":                   researchMode,
		"researchQuestionsAuthorized":    strconv.Itoa(len(researchCoverage)),
		"researchQuestionsStrong":        strconv.Itoa(strongQuestionCount),
		"researchQuestionCoverageDigest": researchCoverageDigest,
		"evidenceAdequacy":               evidenceAdequacy,
	}, nil
}

func validateProcessEvidenceDossier(plan *goalPlan, artifact meetingMemoryEntry) error {
	if plan == nil || (plan.ProcessID != packagingStudioProcessID && plan.ProcessID != documentReportProcessID) {
		return fmt.Errorf("evidence dossier process is unavailable")
	}
	body := strings.TrimSpace(artifact.Text)
	matches := processEvidenceDossierReceiptPattern.FindAllStringSubmatchIndex(body, -1)
	if len(matches) != 1 {
		return fmt.Errorf("evidence dossier must carry exactly one canonical receipt")
	}
	match := matches[0]
	processID := body[match[2]:match[3]]
	externalCount, _ := strconv.Atoi(body[match[4]:match[5]])
	internalCount, _ := strconv.Atoi(body[match[6]:match[7]])
	digest := body[match[8]:match[9]]
	prefix := strings.TrimSpace(body[:match[0]])
	if strings.TrimSpace(body[match[1]:]) != "" || processID != plan.ProcessID || digest != sha256Hex([]byte(prefix)) || strings.TrimSpace(artifact.Metadata["evidenceAdmissionDigest"]) != digest {
		return fmt.Errorf("evidence dossier receipt does not bind the exact process artifact")
	}
	externalRows, err := processExternalManifestRows(prefix)
	if err != nil {
		return fmt.Errorf("evidence dossier external manifest is invalid: %w", err)
	}
	if len(externalRows) != externalCount {
		return fmt.Errorf("evidence dossier receipt count does not match admitted manifest")
	}
	internalRows, err := processInternalManifestRows(prefix)
	if err != nil {
		return fmt.Errorf("evidence dossier internal manifest is invalid: %w", err)
	}
	if len(internalRows) != internalCount {
		return fmt.Errorf("evidence dossier receipt count does not match internal manifest")
	}
	if metadataCount, countErr := strconv.Atoi(strings.TrimSpace(artifact.Metadata["externalClaimsAdmitted"])); countErr != nil || metadataCount != externalCount {
		return fmt.Errorf("evidence dossier external metadata count does not match its receipt")
	}
	if metadataCount, countErr := strconv.Atoi(strings.TrimSpace(artifact.Metadata["internalClaimsAdmitted"])); countErr != nil || metadataCount != internalCount {
		return fmt.Errorf("evidence dossier internal metadata count does not match its receipt")
	}
	researchMode := strings.ToLower(strings.TrimSpace(artifact.Metadata["researchMode"]))
	if !oneOf(researchMode, "none", "internal", "external") {
		return fmt.Errorf("evidence dossier has an invalid research mode")
	}
	coverage, err := processResearchQuestionCoverageRows(prefix, researchMode)
	if err != nil {
		return fmt.Errorf("evidence dossier research coverage is invalid: %w", err)
	}
	if strings.TrimSpace(artifact.Metadata["researchQuestionCoverageDigest"]) != processResearchQuestionCoverageDigest(coverage) {
		return fmt.Errorf("evidence dossier research coverage digest does not bind the exact coverage manifest")
	}
	strongQuestions := 0
	for _, item := range coverage {
		if item.Coverage == processResearchCoverageSupported {
			strongQuestions++
		}
	}
	if metadataCount, countErr := strconv.Atoi(strings.TrimSpace(artifact.Metadata["researchQuestionsAuthorized"])); countErr != nil || metadataCount != len(coverage) {
		return fmt.Errorf("evidence dossier authorized research question count does not match its manifest")
	}
	if metadataCount, countErr := strconv.Atoi(strings.TrimSpace(artifact.Metadata["researchQuestionsStrong"])); countErr != nil || metadataCount != strongQuestions {
		return fmt.Errorf("evidence dossier strong research question count does not match its manifest")
	}
	wantAdequacy := processEvidenceAdequacyNotRequired
	if researchMode == "external" {
		wantAdequacy = processEvidenceAdequacyInsufficient
		if len(coverage) > 0 && strongQuestions == len(coverage) {
			wantAdequacy = processEvidenceAdequacySufficient
		}
	}
	if strings.TrimSpace(artifact.Metadata["evidenceAdequacy"]) != wantAdequacy {
		return fmt.Errorf("evidence dossier adequacy metadata does not match its coverage manifest")
	}
	if researchMode == "external" && wantAdequacy != processEvidenceAdequacySufficient {
		gaps := make([]string, 0, len(coverage))
		for _, item := range coverage {
			if item.Coverage != processResearchCoverageSupported {
				gaps = append(gaps, fmt.Sprintf("%s (%s)", item.Question, item.Coverage))
			}
		}
		return fmt.Errorf("load-bearing external research coverage is insufficient: %s", strings.Join(gaps, "; "))
	}
	return nil
}

func (e *goalEngine) validateProcessStageInputAuthority(plan *goalPlan, stage ProcessStage) error {
	if e == nil || e.app == nil || plan == nil {
		return fmt.Errorf("process input authority is unavailable")
	}
	for _, from := range stage.InputFrom {
		if from != "evidence" {
			continue
		}
		subtask := plan.subtaskByID(from)
		if subtask == nil || subtask.Status != subtaskComplete || strings.TrimSpace(subtask.ArtifactID) == "" {
			return fmt.Errorf("completed evidence admission dossier is required")
		}
		artifact, ok := e.app.osArtifactByID(subtask.ArtifactID)
		parentID := strings.TrimSpace(artifact.Metadata["goalParentId"])
		parent, parentOK := e.app.osArtifactByID(parentID)
		if !ok || !parentOK || parentID == "" || strings.TrimSpace(parent.Metadata["threadId"]) != strings.TrimSpace(plan.GoalID) || strings.TrimSpace(parent.Metadata["processId"]) != plan.ProcessID || strings.TrimSpace(parent.Metadata["processDigest"]) != strings.TrimSpace(plan.ProcessDigest) || strings.TrimSpace(artifact.Metadata["goalSubtaskId"]) != "evidence" || strings.TrimSpace(artifact.Metadata["processId"]) != plan.ProcessID || strings.TrimSpace(artifact.Metadata["processStage"]) != "evidence" || strings.TrimSpace(artifact.Metadata["status"]) != "complete" {
			return fmt.Errorf("evidence dossier is not the exact completed process artifact")
		}
		if err := validateProcessEvidenceDossier(plan, artifact); err != nil {
			return err
		}
	}
	return nil
}
