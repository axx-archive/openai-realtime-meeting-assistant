package main

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	xhtml "golang.org/x/net/html"
)

// processAdmittedClaim is the compact, deterministic allowlist consumed after
// the evidence dossier. Creative stages never get to promote a plausible fact:
// a material number/date/URL must point back to one exact admitted row.
type processAdmittedClaim struct {
	ID           string
	ExactClaim   string
	RequestedURL string
	FinalURL     string
	Internal     bool
}

type processAdmittedClaimManifest map[string]processAdmittedClaim

const processForwardStatementPromptLaw = "FORWARD-STATEMENT LAW: a genuinely forward-looking recommendation or proposal may carry planned numbers only under an explicit contract. In JSON, put statement_type recommendation or proposal on that exact object and begin its visible string with the matching Recommendation:, Proposal: or Target: label; Phase N is also a proposal. Begin with one imperative action such as run, test, launch, set, or build, and keep it to that forward-looking clause. A qualitative inference may instead use statement_type inference plus a visible Inference: label, but it cannot introduce a number or URL. In Markdown, presenter notes, and slide text, begin the scope with the same visible label. A label never licenses a present or past factual assertion, external URL, or altered admitted claim. Never wrap an admitted claim with False, allegedly, reportedly, may, might, could, no longer, or any other polarity or modality. Every external URL must appear with its own exact admitted claim, never a different admitted row."

var (
	processClaimMarkerPattern               = regexp.MustCompile(`(?i)(?:<!--\s*stride-claim:|\[\[claim:)([a-f0-9]{64})`)
	processMaterialURLPattern               = regexp.MustCompile(`(?i)https://[^\s<>\[\]"']+`)
	processMaterialCurrencyPattern          = regexp.MustCompile(`(?i)(?:[$€£¥]\s*\d[\d,.]*(?:\s*(?:trillion|billion|million|bn|k|m|b))?|\b(?:USD|EUR|GBP|JPY|CAD|AUD)\s*\d[\d,.]*(?:\s*(?:trillion|billion|million|bn|k|m|b))?)`)
	processMaterialPercentPattern           = regexp.MustCompile(`(?i)\b\d+(?:\.\d+)?\s*(?:%|\bpercent(?:age\s+points?)?\b)`)
	processMaterialDatePattern              = regexp.MustCompile(`(?i)\b(?:19|20)\d{2}(?:-(?:0[1-9]|1[0-2])-(?:0[1-9]|[12]\d|3[01]))?\b`)
	processMaterialScaledNumberPattern      = regexp.MustCompile(`(?i)\b(?:\d{1,3}(?:,\d{3})+(?:\.\d+)?|\d+\.\d+|\d+(?:\.\d+)?\s*(?:trillion|billion|million|thousand|bn|k|m|b|creators?|people|users?|customers?|accounts?|companies|brands?|products?|locations?|jobs?|posts?|views?|impressions?|countries|markets?|regions?|states?|cities|years?|months?|days?|dollars?|euros?|pounds?|per\s+(?:day|week|month|year)))\b`)
	processMaterialWordScalePattern         = regexp.MustCompile(`(?i)\b(?:hundreds|thousands|millions|billions|trillions)(?:\s+of)?(?:\s+(?:creators?|people|users?|customers?|accounts?|companies|brands?|products?|locations?|jobs?|posts?|views?|impressions?|dollars?|euros?|pounds?))?\b`)
	processMaterialSpelledScalePattern      = regexp.MustCompile(`(?i)\b(?:one|two|three|four|five|six|seven|eight|nine|ten|eleven|twelve|thirteen|fourteen|fifteen|sixteen|seventeen|eighteen|nineteen|twenty|thirty|forty|fifty|sixty|seventy|eighty|ninety)(?:[- ](?:one|two|three|four|five|six|seven|eight|nine))?\s+(?:hundred|thousand|million|billion|trillion)\b`)
	processMaterialSpelledNominalPattern    = regexp.MustCompile(`(?i)\b(?:zero|one|two|three|four|five|six|seven|eight|nine|ten|eleven|twelve|thirteen|fourteen|fifteen|sixteen|seventeen|eighteen|nineteen|twenty|thirty|forty|fifty|sixty|seventy|eighty|ninety)(?:[- ](?:one|two|three|four|five|six|seven|eight|nine))?\s+(?:creators?|people|users?|customers?|accounts?|companies|brands?|products?|locations?|jobs?|posts?|views?|impressions?|countries|markets?|regions?|states?|cities|years?|months?|days?)\b`)
	processMaterialCollectiveNominalPattern = regexp.MustCompile(`(?i)\b(?:(?:a|one)\s+dozen|dozens(?:\s+of)?|(?:a|one)\s+score(?:\s+of)?)\s+(?:creators?|people|users?|customers?|accounts?|companies|brands?|products?|locations?|jobs?|posts?|views?|impressions?|countries|markets?|regions?|states?|cities|years?|months?|days?)\b`)
	processMaterialRomanNominalPattern      = regexp.MustCompile(`\b[IVXLCDM]{2,}\s+(?i:creators?|people|users?|customers?|accounts?|companies|brands?|products?|locations?|jobs?|posts?|views?|impressions?|countries|markets?|regions?|states?|cities|years?|months?|days?)\b`)
	processMaterialIntegerPattern           = regexp.MustCompile(`\b\d+\b`)
	processMaterialQualitativePattern       = regexp.MustCompile(`(?i)\b(?:market\s+leader|category\s+leader|industry\s+leader|largest|fastest[- ]growing|most\s+popular|most\s+widely\s+used|dominant|unprecedented|first[- ]of[- ]its[- ]kind|only\s+(?:company|platform|product|brand|network))\b`)
	processMarkdownParagraphPattern         = regexp.MustCompile(`\n\s*\n+`)
	processHTMLCommentPattern               = regexp.MustCompile(`(?s)<!--.*?-->`)
	processStructuralSlideRefPattern        = regexp.MustCompile(`(?i)\b(?:slide|page)\s+#?\s*\d+\b`)
	processUnsupportedPredicatePattern      = regexp.MustCompile(`(?i)\b(?:powers|powered|drives|drove|driven|delivers|delivered|enables|enabled|reaches|reached|grew|grows|grown|increased|decreased|declined|generates|generated|converts|converted|leads|led|outperforms|outperformed|dominates|dominated|represents|represented|shows|showed|demonstrates|demonstrated|proves|proved|serves|served|sells|sold|buys|bought|uses|used|creates|created|operates|operated|spans|spanned|includes|included|contains|contained|accounts|accounted|employs|employed|supports|supported|engages|engaged|connects|connected|builds|built|owns|owned|controls|controlled|purchased|surveyed)\b`)
	processUnsupportedCopulaPattern         = regexp.MustCompile(`(?i)\b(?:they|it|these|those|we|our\s+(?:company|platform|product|brand|network)|the\s+(?:company|market|platform|product|brand|network|audience|category|industry))\s+(?:all\s+)?(?:is|are|was|were|has|have|had|does|do|can|will|may|might|could)\b`)
	processDeclarativeAuxiliaryPattern      = regexp.MustCompile(`(?i)\b(?:is|are|was|were|has|have|had|remains?|became|becomes?|continues?|continued|will|does|did)\b`)
	processClaimMutationPattern             = regexp.MustCompile(`(?i)\b(?:false|untrue|allegedly|reportedly|purportedly|supposedly|apparently|possibly|perhaps|maybe|may|might|could|no\s+longer|used\s+to|does\s+not\s+apply|do\s+not\s+apply|did\s+not\s+apply)\b`)
	processVisibleStatementPattern          = regexp.MustCompile(`(?i)^(?:#{1,6}\s*)?(?:[-*]\s*)?(recommendation|proposal|target|inference)\s*:`)
	processVisiblePhasePattern              = regexp.MustCompile(`(?i)^(?:#{1,6}\s*)?(?:[-*]\s*)?phase\s+\d+\b`)
	processDeckFractionCounterPattern       = regexp.MustCompile(`^0*\d{1,3}\s*/\s*0*\d{1,3}$`)
	processDeckNamedCounterPattern          = regexp.MustCompile(`(?i)^(?:slide|page)\s+#?\s*0*\d{1,3}$`)
	processDeckAccessibleCounterPattern     = regexp.MustCompile(`(?i)^(?:slide|page)\s+#?\s*0*\d{1,3}\s+(?:of|/)\s+0*\d{1,3}$`)
	processDeckBareCounterPattern           = regexp.MustCompile(`^0*\d{1,3}$`)
	processForwardActionPattern             = regexp.MustCompile(`(?i)^(?:run|test|launch|pilot|start|set|target|reach|build|create|make|design|develop|ship|publish|measure|track|compare|invite|recruit|activate|engage|ask|offer|price|fund|hire|train|deploy|expand|reduce|increase|improve|validate|prove|explore|sequence|schedule|review|audit|choose|use|add|remove|move|keep|stop|protect|reserve|name|define|map|interview|survey|try|commit|plan|open|close|transition|imagine|consider|picture|notice|remember|meet|follow|watch|listen)\b`)
	processForwardClausePattern             = regexp.MustCompile(`(?i)\b(?:because|since|given\s+that|based\s+on|according\s+to|although|despite|whereas|while|but\s+(?:the|acme|it|they|we)|last\s+(?:year|month|week)|yesterday|previously|historically)\b`)
	processAllowedSourceFurniture           = regexp.MustCompile(`(?i)\b(?:source|citation|official\s+source|read\s+source)\b`)
	processJSONFieldNamePattern             = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]*$`)
	processJSONSlidePathPattern             = regexp.MustCompile(`^\$\.slides\[\d+\]$`)
	processJSONElementPathPattern           = regexp.MustCompile(`^\$\.slides\[\d+\]\.elements\[\d+\]$`)
	processJSONRootWrapperPathPattern       = regexp.MustCompile(`^\$\.(canvas|grid|palette|typography)$`)
	processJSONSceneWrapperPathPattern      = regexp.MustCompile(`^\$\.slides\[\d+\](?:\.elements\[\d+\])?\.(canvas|grid|palette|typography|style|position|dimensions|resolution)$`)
	processJSONStyleWrapperPathPattern      = regexp.MustCompile(`^\$\.slides\[\d+\](?:\.elements\[\d+\])?\.style\.(palette|typography|position|dimensions)$`)
)

type processMaterialToken struct {
	Value string
	Kind  string
}

func normalizeProcessMaterialUnicode(text string) string {
	return strings.Map(func(value rune) rune {
		if digit, ok := processUnicodeDecimalDigit(value); ok {
			return digit
		}
		switch value {
		case '％':
			return '%'
		case '＄':
			return '$'
		case '，':
			return ','
		case '．':
			return '.'
		default:
			return value
		}
	}, text)
}

func processUnicodeDecimalDigit(value rune) (rune, bool) {
	// Superscript and subscript digits are numeric presentation forms rather
	// than Unicode Nd blocks, so normalize them explicitly before the
	// contiguous decimal-block sweep below.
	switch value {
	case '⁰', '₀':
		return '0', true
	case '¹', '₁':
		return '1', true
	case '²', '₂':
		return '2', true
	case '³', '₃':
		return '3', true
	case '⁴', '₄':
		return '4', true
	case '⁵', '₅':
		return '5', true
	case '⁶', '₆':
		return '6', true
	case '⁷', '₇':
		return '7', true
	case '⁸', '₈':
		return '8', true
	case '⁹', '₉':
		return '9', true
	}
	// Unicode Nd blocks are contiguous decimal runs. Normalize the common
	// scripts plus every mathematical styled digit block before regexp-based
	// material-claim inspection; otherwise a styled 𝟜𝟟 or Arabic ٤٧ can evade
	// the same gate that correctly rejects ASCII 47.
	bases := [...]rune{
		'0', '０', '٠', '۰', '०', '০', '੦', '૦', '୦', '௦', '౦', '೦', '൦',
		'๐', '໐', '༠', '၀', '០', '᠐', '᥆', '᧐', '꩐', '𐒠', '𑁦', '𑃰', '𑄶',
		'𑑐', '𑓐', '𑙐', '𑜰', '𝟎', '𝟘', '𝟢', '𝟬', '𝟶',
	}
	for _, base := range bases {
		if value >= base && value <= base+9 {
			return '0' + (value - base), true
		}
	}
	return value, false
}

func processClaimGateStage(plan *goalPlan, stage ProcessStage) bool {
	if plan == nil {
		return false
	}
	switch plan.ProcessID {
	case packagingStudioProcessID:
		return oneOf(stage.ID, "story_architects", "write", "voice", "layout_plan", "ship_deck")
	case documentReportProcessID:
		return stage.ID == "story" || stage.ID == "write"
	default:
		return false
	}
}

func loadProcessAdmittedClaimManifest(app *kanbanBoardApp, plan *goalPlan, parentID string) (processAdmittedClaimManifest, error) {
	if app == nil || plan == nil {
		return nil, fmt.Errorf("process evidence authority is unavailable")
	}
	evidence := plan.subtaskByID("evidence")
	if evidence == nil || evidence.Status != subtaskComplete || strings.TrimSpace(evidence.ArtifactID) == "" {
		return nil, fmt.Errorf("completed evidence admission dossier is required")
	}
	artifact, ok := app.osArtifactByID(evidence.ArtifactID)
	if !ok || strings.TrimSpace(parentID) == "" || strings.TrimSpace(artifact.Metadata["goalParentId"]) != strings.TrimSpace(parentID) ||
		strings.TrimSpace(artifact.Metadata["goalSubtaskId"]) != "evidence" ||
		strings.TrimSpace(artifact.Metadata["processId"]) != strings.TrimSpace(plan.ProcessID) ||
		strings.TrimSpace(artifact.Metadata["processStage"]) != "evidence" ||
		strings.TrimSpace(artifact.Metadata["status"]) != "complete" {
		return nil, fmt.Errorf("evidence dossier is not the exact completed process artifact")
	}
	if err := validateProcessEvidenceDossier(plan, artifact); err != nil {
		return nil, err
	}
	external, err := processExternalManifestRows(artifact.Text)
	if err != nil {
		return nil, err
	}
	internal, err := processInternalManifestRows(artifact.Text)
	if err != nil {
		return nil, err
	}
	manifest := make(processAdmittedClaimManifest, len(external)+len(internal))
	for _, claim := range external {
		if _, duplicate := manifest[claim.ID]; duplicate {
			return nil, fmt.Errorf("evidence dossier repeats claim id %s", claim.ID)
		}
		manifest[claim.ID] = processAdmittedClaim{
			ID: claim.ID, ExactClaim: canonicalEvidenceText(claim.Claim),
			RequestedURL: claim.RequestedURL, FinalURL: claim.FinalURL,
		}
	}
	for _, claim := range internal {
		if _, duplicate := manifest[claim.ID]; duplicate {
			return nil, fmt.Errorf("evidence dossier repeats claim id %s", claim.ID)
		}
		manifest[claim.ID] = processAdmittedClaim{ID: claim.ID, ExactClaim: canonicalEvidenceText(claim.Claim), Internal: true}
	}
	return manifest, nil
}

func processMaterialTokens(text string) []processMaterialToken {
	type located struct {
		start int
		end   int
		kind  string
		value string
	}
	visible := normalizeProcessMaterialUnicode(processHTMLCommentPattern.ReplaceAllString(text, " "))
	visible = processStructuralSlideRefPattern.ReplaceAllString(visible, " ")
	locatedTokens := make([]located, 0)
	masked := []byte(visible)
	for _, indexes := range processMaterialURLPattern.FindAllStringIndex(visible, -1) {
		value := strings.TrimRight(strings.TrimSpace(visible[indexes[0]:indexes[1]]), ").,;:!?]}")
		if value != "" {
			locatedTokens = append(locatedTokens, located{start: indexes[0], end: indexes[1], kind: "url", value: value})
		}
		for index := indexes[0]; index < indexes[1]; index++ {
			masked[index] = ' '
		}
	}
	numericText := string(masked)
	patterns := []struct {
		kind    string
		pattern *regexp.Regexp
	}{
		{"currency", processMaterialCurrencyPattern},
		{"percent", processMaterialPercentPattern},
		{"date", processMaterialDatePattern},
		{"number", processMaterialScaledNumberPattern},
		{"number", processMaterialWordScalePattern},
		{"number", processMaterialSpelledScalePattern},
		{"number", processMaterialSpelledNominalPattern},
		{"number", processMaterialCollectiveNominalPattern},
		{"number", processMaterialRomanNominalPattern},
		{"integer", processMaterialIntegerPattern},
		{"qualitative", processMaterialQualitativePattern},
		{"assertion", processUnsupportedPredicatePattern},
		{"assertion", processUnsupportedCopulaPattern},
		{"claim modifier", processClaimMutationPattern},
	}
	for _, candidate := range patterns {
		for _, indexes := range candidate.pattern.FindAllStringIndex(numericText, -1) {
			value := strings.TrimSpace(numericText[indexes[0]:indexes[1]])
			if value != "" {
				locatedTokens = append(locatedTokens, located{start: indexes[0], end: indexes[1], kind: candidate.kind, value: value})
			}
		}
	}
	sort.SliceStable(locatedTokens, func(i, j int) bool {
		if locatedTokens[i].start != locatedTokens[j].start {
			return locatedTokens[i].start < locatedTokens[j].start
		}
		return locatedTokens[i].end > locatedTokens[j].end
	})
	result := make([]processMaterialToken, 0, len(locatedTokens))
	seen := map[string]bool{}
	for _, token := range locatedTokens {
		key := token.kind + "\x00" + strings.ToLower(token.value)
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, processMaterialToken{Value: token.value, Kind: token.kind})
	}
	return result
}

type processStatementType string

const (
	processStatementNone           processStatementType = ""
	processStatementRecommendation processStatementType = "recommendation"
	processStatementProposal       processStatementType = "proposal"
	processStatementInference      processStatementType = "inference"
)

func processVisibleStatementType(text string) processStatementType {
	visible := strings.TrimSpace(processHTMLCommentPattern.ReplaceAllString(text, " "))
	if processVisiblePhasePattern.MatchString(visible) {
		return processStatementProposal
	}
	match := processVisibleStatementPattern.FindStringSubmatch(visible)
	if len(match) != 2 {
		return processStatementNone
	}
	switch strings.ToLower(match[1]) {
	case "recommendation":
		return processStatementRecommendation
	case "proposal", "target":
		return processStatementProposal
	case "inference":
		return processStatementInference
	default:
		return processStatementNone
	}
}

func processVisibleStatementBody(text string) (processStatementType, string) {
	visible := strings.TrimSpace(processHTMLCommentPattern.ReplaceAllString(text, " "))
	if match := processVisiblePhasePattern.FindStringIndex(visible); match != nil && match[0] == 0 {
		return processStatementProposal, strings.TrimSpace(strings.TrimLeft(visible[match[1]:], ":-—– "))
	}
	match := processVisibleStatementPattern.FindStringSubmatchIndex(visible)
	if len(match) < 4 || match[0] != 0 {
		return processStatementNone, ""
	}
	statementType := processVisibleStatementType(visible)
	return statementType, strings.TrimSpace(visible[match[1]:])
}

func processDeclaredStatementType(object map[string]any) (processStatementType, error) {
	var value any
	exists := false
	for key, candidate := range object {
		if strings.EqualFold(strings.TrimSpace(key), "statement_type") {
			if exists {
				return processStatementNone, fmt.Errorf("statement_type may appear only once")
			}
			value, exists = candidate, true
		}
	}
	if !exists {
		return processStatementNone, nil
	}
	statement, ok := value.(string)
	if !ok {
		return processStatementNone, fmt.Errorf("statement_type must be one string")
	}
	switch processStatementType(strings.ToLower(strings.TrimSpace(statement))) {
	case processStatementRecommendation:
		return processStatementRecommendation, nil
	case processStatementProposal:
		return processStatementProposal, nil
	case processStatementInference:
		return processStatementInference, nil
	default:
		return processStatementNone, fmt.Errorf("statement_type must be recommendation, proposal, or inference")
	}
}

func processScopeContainsExactClaim(text string, anchors []processAdmittedClaim) bool {
	visible := canonicalEvidenceText(processHTMLCommentPattern.ReplaceAllString(text, " "))
	for _, claim := range anchors {
		if claim.ExactClaim != "" && strings.Contains(visible, claim.ExactClaim) {
			return true
		}
	}
	return false
}

func processForwardStatementAllowed(text string, declared processStatementType, anchors []processAdmittedClaim) bool {
	visibleType, body := processVisibleStatementBody(text)
	if visibleType == processStatementNone || (declared != processStatementNone && visibleType != declared) {
		return false
	}
	visible := processHTMLCommentPattern.ReplaceAllString(text, " ")
	if processScopeContainsExactClaim(visible, anchors) || len(processMaterialURLPattern.FindAllString(visible, -1)) > 0 {
		return false
	}
	switch visibleType {
	case processStatementRecommendation, processStatementProposal:
		if body == "" {
			return processVisiblePhasePattern.MatchString(strings.TrimSpace(visible))
		}
		if !processForwardActionPattern.MatchString(body) || processForwardClausePattern.MatchString(body) || processDeclarativeAuxiliaryPattern.MatchString(body) {
			return false
		}
		return true
	case processStatementInference:
		return len(processMaterialTokens(body)) == 0 && !processUnsupportedPredicatePattern.MatchString(body) && !processDeclarativeAuxiliaryPattern.MatchString(body) && !processMaterialQualitativePattern.MatchString(body)
	default:
		return false
	}
}

func processClaimOwnsURL(claim processAdmittedClaim, rawURL string) bool {
	url := strings.TrimRight(strings.TrimSpace(rawURL), ").,;:!?]}")
	return url != "" && (url == strings.TrimSpace(claim.RequestedURL) || url == strings.TrimSpace(claim.FinalURL))
}

// validateProcessClaimURLBindings prevents cross-row source laundering. It is
// not enough for both a claim and a URL to be admitted somewhere in the same
// dossier: the visible scope must render the exact claim owned by that URL.
func validateProcessClaimURLBindings(text, path string, anchors []processAdmittedClaim) error {
	visible := canonicalEvidenceText(processHTMLCommentPattern.ReplaceAllString(text, " "))
	for _, rawURL := range processMaterialURLPattern.FindAllString(visible, -1) {
		url := strings.TrimRight(strings.TrimSpace(rawURL), ").,;:!?]}")
		bound := false
		for _, claim := range anchors {
			if processClaimOwnsURL(claim, url) && strings.Contains(visible, claim.ExactClaim) {
				bound = true
				break
			}
		}
		if !bound {
			return fmt.Errorf("%s: external URL %q is not bound to its own exact admitted claim in visible copy", path, compactAssistantLine(url))
		}
	}
	return nil
}

func processClaimCitationUnits(text string) []string {
	visible := processHTMLCommentPattern.ReplaceAllString(text, " ")
	masked := []rune(visible)
	// Mask URL punctuation before sentence splitting so dots and query-string
	// punctuation cannot manufacture citation boundaries.
	for _, indexes := range processMaterialURLPattern.FindAllStringIndex(visible, -1) {
		rawURL := visible[indexes[0]:indexes[1]]
		actualURL := strings.TrimRight(rawURL, ").,;:!?]}")
		start := len([]rune(visible[:indexes[0]]))
		end := start + len([]rune(actualURL))
		for index := start; index < end && index < len(masked); index++ {
			masked[index] = ' '
		}
	}
	units := make([]string, 0, 4)
	start := 0
	for index, value := range masked {
		periodBoundary := value == '.' && (externalEvidencePeriodIsSentenceBoundary(masked, index) || (index > 0 && (masked[index-1] == ')' || masked[index-1] == ']')))
		boundary := value == '!' || value == '?' || periodBoundary
		if !boundary || (index+1 < len(masked) && !unicode.IsSpace(masked[index+1])) {
			continue
		}
		unit := strings.TrimSpace(string([]rune(visible)[start : index+1]))
		if unit != "" {
			units = append(units, unit)
		}
		start = index + 1
	}
	if trailing := strings.TrimSpace(string([]rune(visible)[start:])); trailing != "" {
		units = append(units, trailing)
	}
	// A conventional citation-only sentence such as "[Source](url)." belongs
	// to the immediately preceding sentence. It does not license any earlier or
	// later sentence in the paragraph.
	combined := make([]string, 0, len(units))
	for _, unit := range units {
		furniture := unit
		for _, rawURL := range processMaterialURLPattern.FindAllString(furniture, -1) {
			furniture = strings.ReplaceAll(furniture, rawURL, " ")
		}
		furniture = processAllowedSourceFurniture.ReplaceAllString(furniture, " ")
		onlyFurniture := processMaterialURLPattern.MatchString(unit)
		for _, value := range furniture {
			if unicode.IsLetter(value) || unicode.IsDigit(value) {
				onlyFurniture = false
				break
			}
		}
		if onlyFurniture && len(combined) > 0 {
			combined[len(combined)-1] = strings.TrimSpace(combined[len(combined)-1] + " " + unit)
			continue
		}
		combined = append(combined, unit)
	}
	return combined
}

func validateProcessSentenceLocalClaimURLBindings(text, path string, anchors []processAdmittedClaim) error {
	for index, unit := range processClaimCitationUnits(text) {
		visible := canonicalEvidenceText(unit)
		for _, rawURL := range processMaterialURLPattern.FindAllString(visible, -1) {
			url := strings.TrimRight(strings.TrimSpace(rawURL), ").,;:!?]}")
			bound := false
			for _, claim := range anchors {
				if processClaimOwnsURL(claim, url) && strings.Contains(visible, claim.ExactClaim) {
					bound = true
					break
				}
			}
			if !bound {
				return fmt.Errorf("%s sentence %d: external URL %q is not bound to its own exact admitted claim in that sentence", path, index+1, compactAssistantLine(url))
			}
		}
	}
	return nil
}

func processLikelyDeclarativeAssertion(text string) bool {
	visible := strings.TrimSpace(processHTMLCommentPattern.ReplaceAllString(text, " "))
	visible = strings.TrimSpace(strings.TrimLeft(visible, "#*-• "))
	if visible == "" || strings.HasSuffix(visible, "?") {
		return false
	}
	if processLikelyFactualHeadlineFragment(visible) {
		return true
	}
	if processUnsupportedPredicatePattern.MatchString(visible) || processUnsupportedCopulaPattern.MatchString(visible) || processDeclarativeAuxiliaryPattern.MatchString(visible) {
		return true
	}
	if !strings.HasSuffix(visible, ".") && !strings.HasSuffix(visible, "!") {
		return false
	}
	words := strings.FieldsFunc(visible, func(value rune) bool {
		return !unicode.IsLetter(value) && !unicode.IsDigit(value) && value != '\'' && value != '-'
	})
	if len(words) < 3 {
		return false
	}
	return !processForwardActionPattern.MatchString(visible)
}

// processLikelyFactualHeadlineFragment is the positive contract for terse
// marketing claims that omit a verb and terminal punctuation. Headlines such
// as "preferred platform" and "trusted worldwide" still assert external
// status; their fragmentary grammar must not exempt them from evidence. The
// contract uses claim-bearing status, comparative, subject, and scope classes
// rather than a phrase blacklist, while leaving ordinary narrative titles
// (for example, "A bold invitation" or "The next chapter") untouched.
func processLikelyFactualHeadlineFragment(text string) bool {
	words := strings.FieldsFunc(strings.ToLower(text), func(value rune) bool {
		return !unicode.IsLetter(value) && !unicode.IsDigit(value)
	})
	if len(words) < 2 {
		return false
	}
	status := map[string]bool{
		"trusted": true, "preferred": true, "available": true, "proven": true,
		"recognized": true, "renowned": true, "dominant": true,
	}
	comparative := map[string]bool{
		"top": true, "best": true, "leading": true, "most": true,
		"largest": true, "fastest": true, "only": true,
	}
	claimSubject := map[string]bool{
		"platform": true, "platforms": true, "network": true, "networks": true,
		"brand": true, "brands": true, "company": true, "companies": true,
		"product": true, "products": true, "service": true, "services": true,
		"choice": true, "partner": true, "partners": true, "provider": true,
		"providers": true, "solution": true, "solutions": true, "market": true,
		"markets": true, "industry": true, "creator": true, "creators": true,
		"customer": true, "customers": true, "user": true, "users": true,
		"team": true, "teams": true,
	}
	scope := map[string]bool{
		"worldwide": true, "global": true, "globally": true, "nationwide": true,
		"nationally": true, "international": true, "internationally": true,
		"everywhere": true, "industry": true, "market": true, "markets": true,
		"brands": true, "companies": true, "creators": true, "customers": true,
		"users": true, "teams": true, "countries": true, "regions": true,
	}
	hasStatus, hasComparative, hasSubject, hasScope := false, false, false, false
	for _, word := range words {
		hasStatus = hasStatus || status[word]
		hasComparative = hasComparative || comparative[word]
		hasSubject = hasSubject || claimSubject[word]
		hasScope = hasScope || scope[word]
	}
	if hasStatus && (hasSubject || hasScope || hasComparative) {
		return true
	}
	return hasComparative && hasSubject
}

// validateProcessExactClaimUnit is the positive factual rendering contract.
// Once a scope invokes an admitted claim, the entire factual unit must be the
// admitted text (one or more rows), its exact bound URLs, and tightly typed
// source furniture. Subtract-and-blacklist is deliberately insufficient: any
// remaining word can reverse or qualify the claim.
func validateProcessExactClaimUnit(text, path string, anchors []processAdmittedClaim) (bool, error) {
	visible := canonicalEvidenceText(processHTMLCommentPattern.ReplaceAllString(text, " "))
	remaining := visible
	matched := false
	for _, claim := range anchors {
		if claim.ExactClaim == "" || !strings.Contains(remaining, claim.ExactClaim) {
			continue
		}
		matched = true
		remaining = strings.ReplaceAll(remaining, claim.ExactClaim, " ")
	}
	if !matched {
		return false, nil
	}
	if strings.Contains(visible, "~~") {
		return true, fmt.Errorf("%s: exact admitted claims cannot be struck through", path)
	}
	for _, rawURL := range processMaterialURLPattern.FindAllString(remaining, -1) {
		remaining = strings.ReplaceAll(remaining, rawURL, " ")
	}
	remaining = processAllowedSourceFurniture.ReplaceAllString(remaining, " ")
	for _, value := range remaining {
		if strings.ContainsRune("¬⊘⨯≠", value) {
			return true, fmt.Errorf("%s: exact admitted claim has an unsupported polarity or status symbol %q", path, string(value))
		}
		if (value >= 0x2600 && value <= 0x27ff) || (value >= 0x1f000 && value <= 0x1faff) {
			return true, fmt.Errorf("%s: exact admitted claim has an unsupported polarity or status symbol %q", path, string(value))
		}
		if unicode.IsLetter(value) || unicode.IsDigit(value) {
			return true, fmt.Errorf("%s: exact admitted claim has unsupported surrounding text %q", path, compactAssistantLine(strings.TrimSpace(remaining)))
		}
	}
	return true, nil
}

func validateProcessFactText(text, path string, anchors []processAdmittedClaim, forceNumber bool) error {
	visible := canonicalEvidenceText(processHTMLCommentPattern.ReplaceAllString(text, " "))
	if matched, err := validateProcessExactClaimUnit(visible, path, anchors); matched {
		return err
	}
	remaining := visible
	for _, claim := range anchors {
		remaining = strings.ReplaceAll(remaining, claim.ExactClaim, " ")
		if claim.RequestedURL != "" {
			remaining = strings.ReplaceAll(remaining, claim.RequestedURL, " ")
		}
		if claim.FinalURL != "" {
			remaining = strings.ReplaceAll(remaining, claim.FinalURL, " ")
		}
	}
	tokens := processMaterialTokens(remaining)
	if forceNumber && len(tokens) == 0 && strings.TrimSpace(remaining) != "" {
		tokens = append(tokens, processMaterialToken{Value: strings.TrimSpace(remaining), Kind: "integer"})
	}
	if len(tokens) == 0 && processLikelyDeclarativeAssertion(remaining) {
		tokens = append(tokens, processMaterialToken{Value: strings.TrimSpace(remaining), Kind: "unsupported declarative assertion"})
	}
	if len(tokens) > 0 {
		token := tokens[0]
		return fmt.Errorf("%s: material %s %q remains outside every exact admitted claim rendering", path, token.Kind, compactAssistantLine(token.Value))
	}
	return nil
}

func processClaimPairs(ids, exactClaims []string, manifest processAdmittedClaimManifest) ([]processAdmittedClaim, error) {
	selected := make(map[string]processAdmittedClaim, len(ids))
	for _, rawID := range ids {
		id := strings.ToLower(strings.TrimSpace(rawID))
		claim, ok := manifest[id]
		if !ok {
			return nil, fmt.Errorf("claim id %q was not admitted by the evidence dossier", compactAssistantLine(rawID))
		}
		selected[id] = claim
	}
	for _, exact := range exactClaims {
		normalized := canonicalEvidenceText(exact)
		matched := false
		for _, claim := range selected {
			if normalized == claim.ExactClaim {
				matched = true
				break
			}
		}
		if !matched {
			return nil, fmt.Errorf("exact_claims contains text not paired to an admitted claim id")
		}
	}
	for _, claim := range selected {
		matched := false
		for _, exact := range exactClaims {
			if canonicalEvidenceText(exact) == claim.ExactClaim {
				matched = true
				break
			}
		}
		if !matched {
			return nil, fmt.Errorf("claim id %s is missing its exact admitted text", claim.ID)
		}
	}
	claims := make([]processAdmittedClaim, 0, len(selected))
	for _, claim := range selected {
		claims = append(claims, claim)
	}
	sort.Slice(claims, func(i, j int) bool {
		if len(claims[i].ExactClaim) != len(claims[j].ExactClaim) {
			return len(claims[i].ExactClaim) > len(claims[j].ExactClaim)
		}
		return claims[i].ID < claims[j].ID
	})
	return claims, nil
}

func processJSONAnchorField(key string) (ids bool, exact bool) {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "claim_id", "claim_ids":
		return true, false
	case "exact_claim", "exact_claims":
		return false, true
	default:
		return false, false
	}
}

func processJSONStructuralField(path, key string) bool {
	normalized := strings.ToLower(strings.TrimSpace(key))
	if path == "$" {
		return normalized == "slide_count"
	}
	if processJSONSlidePathPattern.MatchString(path) {
		return oneOf(normalized,
			"id", "slide_id", "slide_number", "page_number", "order", "index", "sequence", "fig",
			"aspect", "type", "composition_type", "composition", "background", "color", "fill", "opacity",
			"x", "y", "width", "height", "z", "z_index", "zindex")
	}
	if processJSONElementPathPattern.MatchString(path) {
		return oneOf(normalized,
			"id", "element_id", "type", "element_type", "fig", "aspect", "composition_type",
			"x", "y", "width", "height", "z", "z_index", "zindex", "opacity", "rotation", "blur", "scale", "zoom",
			"background", "color", "fill", "stroke", "border", "stroke_width", "strokewidth",
			"border_radius", "borderradius", "corner_radius", "cornerradius",
			"font", "font_family", "fontfamily", "font_size", "fontsize", "font_weight", "fontweight",
			"line_height", "lineheight", "letter_spacing", "letterspacing", "tracking", "alignment", "text_align", "textalign",
			"crop", "focal_point", "fit", "size")
	}
	match := processJSONRootWrapperPathPattern.FindStringSubmatch(path)
	if len(match) == 0 {
		match = processJSONSceneWrapperPathPattern.FindStringSubmatch(path)
	}
	if len(match) == 0 {
		match = processJSONStyleWrapperPathPattern.FindStringSubmatch(path)
	}
	if len(match) == 0 {
		return false
	}
	wrapper := match[1]
	switch wrapper {
	case "canvas", "position", "dimensions", "resolution":
		return oneOf(normalized, "aspect", "x", "y", "width", "height", "z", "z_index", "zindex", "rotation", "scale", "zoom", "size")
	case "grid":
		return oneOf(normalized, "columns", "column_count", "gutter", "gutters", "gap", "margin", "padding", "safe_zone", "alignment")
	case "palette":
		return oneOf(normalized, "background", "color", "fill", "stroke", "border", "primary", "secondary", "accent", "surface", "foreground")
	case "typography":
		return oneOf(normalized, "font", "font_family", "fontfamily", "font_size", "fontsize", "font_weight", "fontweight", "line_height", "lineheight", "letter_spacing", "letterspacing", "tracking", "alignment", "text_align", "textalign", "size")
	case "style":
		return oneOf(normalized,
			"x", "y", "width", "height", "z", "z_index", "zindex", "opacity", "rotation", "blur", "scale", "zoom",
			"gap", "margin", "padding", "safe_zone", "background", "color", "fill", "stroke", "border", "primary", "secondary", "accent", "surface", "foreground",
			"border_radius", "borderradius", "corner_radius", "cornerradius", "stroke_width", "strokewidth",
			"font", "font_family", "fontfamily", "font_size", "fontsize", "font_weight", "fontweight", "line_height", "lineheight", "letter_spacing", "letterspacing", "tracking", "alignment", "text_align", "textalign", "crop", "focal_point", "fit", "size")
	}
	return false
}

func processJSONScalarStrings(value any) []string {
	switch typed := value.(type) {
	case string:
		return []string{typed}
	case json.Number:
		return []string{typed.String()}
	case []any:
		values := make([]string, 0, len(typed))
		for _, item := range typed {
			switch scalar := item.(type) {
			case string:
				values = append(values, scalar)
			case json.Number:
				values = append(values, scalar.String())
			}
		}
		return values
	default:
		return nil
	}
}

type processJSONScalarLeaf struct {
	Text        string
	Path        string
	ForceNumber bool
}

func processJSONScalarLeaves(value any, path string) []processJSONScalarLeaf {
	switch typed := value.(type) {
	case string:
		return []processJSONScalarLeaf{{Text: typed, Path: path}}
	case json.Number:
		return []processJSONScalarLeaf{{Text: typed.String(), Path: path, ForceNumber: true}}
	case []any:
		leaves := make([]processJSONScalarLeaf, 0)
		for index, item := range typed {
			leaves = append(leaves, processJSONScalarLeaves(item, fmt.Sprintf("%s[%d]", path, index))...)
		}
		return leaves
	default:
		return nil
	}
}

func processJSONChildObjects(value any, path string, visit func(map[string]any, string) error) error {
	switch typed := value.(type) {
	case map[string]any:
		if err := visit(typed, path); err != nil {
			return err
		}
	case []any:
		for index, item := range typed {
			if err := processJSONChildObjects(item, fmt.Sprintf("%s[%d]", path, index), visit); err != nil {
				return err
			}
		}
	}
	return nil
}

func processStructuralScalarSafe(key string, leaf processJSONScalarLeaf) bool {
	value := strings.TrimSpace(leaf.Text)
	if value == "" {
		return true
	}
	normalizedKey := strings.ToLower(strings.TrimSpace(key))
	if leaf.ForceNumber || regexp.MustCompile(`^-?(?:\d+(?:\.\d+)?|\.\d+)(?:px|pt|em|rem|%|deg)?$`).MatchString(value) {
		return true
	}
	if oneOf(normalizedKey, "id", "slide_id", "type", "element_type", "composition_type") && deckIdentifierPattern.MatchString(value) {
		return true
	}
	if oneOf(normalizedKey, "aspect", "resolution", "dimensions", "size") && regexp.MustCompile(`^\d{1,5}\s*(?::|/|x|×)\s*\d{1,5}$`).MatchString(value) {
		return true
	}
	lowered := strings.ToLower(value)
	if oneOf(normalizedKey, "background", "color", "fill", "stroke", "border", "primary", "secondary", "accent", "surface", "foreground") {
		if validDeckColor(value) || strings.HasPrefix(lowered, "rgb(") || strings.HasPrefix(lowered, "rgba(") || strings.HasPrefix(lowered, "hsl(") || strings.HasPrefix(lowered, "hsla(") || strings.HasPrefix(lowered, "linear-gradient(") || strings.HasPrefix(lowered, "radial-gradient(") || strings.HasPrefix(lowered, "var(--") {
			return !processMaterialURLPattern.MatchString(value) && !processLikelyDeclarativeAssertion(value)
		}
		return false
	}
	return len(processMaterialTokens(value)) == 0 && !processLikelyDeclarativeAssertion(value)
}

func validateProcessJSONFieldName(key, path string) error {
	trimmed := strings.TrimSpace(key)
	if !processJSONFieldNamePattern.MatchString(trimmed) {
		return fmt.Errorf("%s: JSON field name %q is not a typed process field", path, compactAssistantLine(key))
	}
	// JSON keys can themselves surface or encode assertions (including boolean
	// facts), and punctuation in a key could spoof one of the exact path
	// allowlists above. Inspect the semantic form of every key before values.
	semantic := strings.NewReplacer("_", " ", "-", " ").Replace(trimmed)
	if len(processMaterialTokens(semantic)) > 0 || processLikelyDeclarativeAssertion(semantic) {
		return fmt.Errorf("%s: JSON field name %q encodes an unadmitted factual claim", path, compactAssistantLine(key))
	}
	return nil
}

func validateProcessJSONClaimObject(object map[string]any, path string, manifest processAdmittedClaimManifest) error {
	for key := range object {
		if err := validateProcessJSONFieldName(key, path); err != nil {
			return err
		}
	}
	ids, exactClaims := []string{}, []string{}
	for key, value := range object {
		isID, isExact := processJSONAnchorField(key)
		values := processJSONScalarStrings(value)
		switch {
		case isID:
			ids = append(ids, values...)
		case isExact:
			exactClaims = append(exactClaims, values...)
		}
	}
	anchors, err := processClaimPairs(ids, exactClaims, manifest)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	declared, err := processDeclaredStatementType(object)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	directVisible := make([]string, 0)
	for key, value := range object {
		isID, isExact := processJSONAnchorField(key)
		if isID || isExact || strings.EqualFold(strings.TrimSpace(key), "statement_type") {
			continue
		}
		for _, leaf := range processJSONScalarLeaves(value, path+"."+key) {
			if !processJSONStructuralField(path, key) {
				directVisible = append(directVisible, leaf.Text)
			}
		}
	}
	for key, value := range object {
		isID, isExact := processJSONAnchorField(key)
		if isID || isExact || strings.EqualFold(strings.TrimSpace(key), "statement_type") {
			continue
		}
		for _, leaf := range processJSONScalarLeaves(value, path+"."+key) {
			if processJSONStructuralField(path, key) {
				if !processStructuralScalarSafe(key, leaf) {
					return fmt.Errorf("%s: structural field %s has non-structural scalar %q", leaf.Path, key, compactAssistantLine(leaf.Text))
				}
				continue
			}
			if declared != processStatementNone && processForwardStatementAllowed(leaf.Text, declared, anchors) {
				continue
			}
			if err := validateProcessFactText(leaf.Text, leaf.Path, anchors, leaf.ForceNumber); err != nil {
				return err
			}
		}
	}
	if err := validateProcessClaimURLBindings(strings.Join(directVisible, " "), path, anchors); err != nil {
		return err
	}
	for key, value := range object {
		if err := processJSONChildObjects(value, path+"."+key, func(child map[string]any, childPath string) error {
			return validateProcessJSONClaimObject(child, childPath, manifest)
		}); err != nil {
			return err
		}
	}
	return nil
}

func decodeProcessClaimJSON(body string) (map[string]any, bool) {
	trimmed := strings.TrimSpace(body)
	if strings.HasPrefix(trimmed, "```json") && strings.HasSuffix(trimmed, "```") {
		trimmed = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(trimmed, "```json"), "```"))
	}
	if !strings.HasPrefix(trimmed, "{") {
		return nil, false
	}
	decoder := json.NewDecoder(strings.NewReader(trimmed))
	decoder.UseNumber()
	var object map[string]any
	if err := decoder.Decode(&object); err != nil || ensureJSONEOF(decoder) != nil {
		return nil, false
	}
	return object, true
}

func processClaimAnchorsFromScope(scope string, manifest processAdmittedClaimManifest) ([]processAdmittedClaim, error) {
	markers := processClaimMarkerPattern.FindAllStringSubmatch(scope, -1)
	ids := make([]string, 0, len(markers))
	exactClaims := make([]string, 0, len(markers))
	canonicalScope := canonicalEvidenceText(scope)
	for _, marker := range markers {
		id := strings.ToLower(strings.TrimSpace(marker[1]))
		claim, ok := manifest[id]
		if !ok {
			return nil, fmt.Errorf("claim id %s was not admitted by the evidence dossier", id)
		}
		ids = append(ids, id)
		if strings.Contains(canonicalScope, claim.ExactClaim) {
			exactClaims = append(exactClaims, claim.ExactClaim)
		}
	}
	return processClaimPairs(ids, exactClaims, manifest)
}

func validateProcessMarkdownClaimScope(scope string, index int, manifest processAdmittedClaimManifest) error {
	markers := processClaimMarkerPattern.FindAllStringSubmatch(scope, -1)
	if len(processMaterialTokens(scope)) == 0 && !processLikelyDeclarativeAssertion(scope) && len(markers) == 0 {
		return nil
	}
	anchors, err := processClaimAnchorsFromScope(scope, manifest)
	if err != nil {
		return fmt.Errorf("paragraph %d: %w", index, err)
	}
	if processForwardStatementAllowed(scope, processStatementNone, anchors) {
		return nil
	}
	if err := validateProcessFactText(scope, fmt.Sprintf("paragraph %d", index), anchors, false); err != nil {
		return fmt.Errorf("%w; use <!-- stride-claim:<id> | <exact admitted claim> --> and render the admitted claim text exactly", err)
	}
	if err := validateProcessClaimURLBindings(scope, fmt.Sprintf("paragraph %d", index), anchors); err != nil {
		return err
	}
	if err := validateProcessSentenceLocalClaimURLBindings(scope, fmt.Sprintf("paragraph %d", index), anchors); err != nil {
		return err
	}
	return nil
}

func processDeckSlideAuthorityComments(body string) ([][]string, error) {
	doc, err := xhtml.Parse(strings.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("deck HTML could not be parsed")
	}
	slides := make([]*xhtml.Node, 0)
	var collectSlides func(*xhtml.Node)
	collectSlides = func(node *xhtml.Node) {
		isStageChild := node.Type == xhtml.ElementNode && node.Data == "section" && node.Parent != nil && legacyNodeAttr(node.Parent, "id") == "stage"
		if node.Type == xhtml.ElementNode && (legacyNodeHasClass(node, "pg") || legacyNodeHasClass(node, "slide") || isStageChild) {
			slides = append(slides, node)
			return
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			collectSlides(child)
		}
	}
	collectSlides(doc)
	comments := make([][]string, len(slides))
	for index, slide := range slides {
		var collectComments func(*xhtml.Node)
		collectComments = func(node *xhtml.Node) {
			if node.Type == xhtml.CommentNode {
				comments[index] = append(comments[index], "<!--"+node.Data+"-->")
			}
			for child := node.FirstChild; child != nil; child = child.NextSibling {
				collectComments(child)
			}
		}
		collectComments(slide)
	}
	return comments, nil
}

func processDeckStructuralText(element deckElement) bool {
	text := strings.TrimSpace(element.Text)
	if processDeckFractionCounterPattern.MatchString(text) || processDeckNamedCounterPattern.MatchString(text) {
		return true
	}
	if !processDeckBareCounterPattern.MatchString(text) {
		return false
	}
	role := strings.ToLower(strings.NewReplacer("_", "-", " ", "-").Replace(strings.TrimSpace(element.ID)))
	return role == "folio" || strings.Contains(role, "page-counter") || strings.Contains(role, "slide-counter") || strings.Contains(role, "page-number") || strings.Contains(role, "slide-number")
}

type processCSSDeclaration struct {
	Property string
	Value    string
}

func processCSSHexByte(value byte) bool {
	return (value >= '0' && value <= '9') || (value >= 'a' && value <= 'f') || (value >= 'A' && value <= 'F')
}

func processCSSUnescape(value string) string {
	var result strings.Builder
	for index := 0; index < len(value); {
		if value[index] != '\\' {
			result.WriteByte(value[index])
			index++
			continue
		}
		index++
		start := index
		for index < len(value) && index-start < 6 && processCSSHexByte(value[index]) {
			index++
		}
		if index > start {
			parsed, err := strconv.ParseInt(value[start:index], 16, 32)
			if err == nil && parsed > 0 {
				result.WriteRune(rune(parsed))
			}
			if index < len(value) && unicode.IsSpace(rune(value[index])) {
				index++
			}
			continue
		}
		if index < len(value) {
			result.WriteByte(value[index])
			index++
		}
	}
	return result.String()
}

func processCSSDeclarations(css string) []processCSSDeclaration {
	cleaned := legacyCSSCommentPattern.ReplaceAllString(css, " ")
	parts := strings.Split(cleaned, ";")
	result := make([]processCSSDeclaration, 0, len(parts))
	for _, part := range parts {
		separator := strings.Index(part, ":")
		if separator < 0 {
			continue
		}
		property := strings.ToLower(strings.TrimSpace(processCSSUnescape(part[:separator])))
		value := strings.TrimSpace(part[separator+1:])
		if property != "" {
			result = append(result, processCSSDeclaration{Property: property, Value: value})
		}
	}
	return result
}

func processCSSDeclarationHidesText(declaration processCSSDeclaration) bool {
	value := strings.ToLower(strings.TrimSpace(strings.TrimSuffix(processCSSUnescape(declaration.Value), "!important")))
	switch declaration.Property {
	case "display":
		return value == "none"
	case "visibility":
		return value == "hidden" || value == "collapse"
	case "opacity", "font-size":
		number := strings.TrimSpace(strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(value, "px"), "em"), "rem"))
		parsed, err := strconv.ParseFloat(number, 64)
		return err == nil && parsed == 0
	case "color":
		return value == "transparent" || value == "rgba(0,0,0,0)" || value == "rgba(0, 0, 0, 0)"
	case "text-indent":
		return strings.HasPrefix(value, "-")
	case "clip", "clip-path":
		return value != "" && value != "none" && value != "auto"
	case "transform":
		compact := strings.ReplaceAll(value, " ", "")
		return strings.Contains(compact, "scale(0)") || strings.Contains(compact, "scale(0,") || strings.Contains(compact, "scalex(0)") || strings.Contains(compact, "scaley(0)")
	case "text-decoration", "text-decoration-line":
		return strings.Contains(value, "line-through")
	default:
		return false
	}
}

func processCSSSelectorMayHide(selector string) bool {
	selector = strings.ToLower(strings.TrimSpace(processCSSUnescape(selector)))
	selector = strings.Join(strings.Fields(selector), "")
	return selector == ".pg" || selector == ".notes" || selector == "#prompt,#phint,#railwrap,.navzone"
}

func processNodeHasAttr(node *xhtml.Node, name string) bool {
	for _, attribute := range node.Attr {
		if strings.EqualFold(attribute.Key, name) {
			return true
		}
	}
	return false
}

func validateProcessCSSDeclarations(css, path string, allowTextHiding bool) error {
	for _, declaration := range processCSSDeclarations(css) {
		if declaration.Property == "content" {
			return fmt.Errorf("%s uses CSS-generated visible content %q", path, compactAssistantLine(declaration.Value))
		}
		if !allowTextHiding && processCSSDeclarationHidesText(declaration) {
			return fmt.Errorf("%s hides or semantically suppresses inspectable text with %s:%s", path, declaration.Property, compactAssistantLine(declaration.Value))
		}
	}
	return nil
}

func validateProcessDeckNoGeneratedContent(body string) error {
	doc, err := xhtml.Parse(strings.NewReader(body))
	if err != nil {
		return fmt.Errorf("deck HTML could not be parsed for generated content")
	}
	var bodyNode *xhtml.Node
	var visit func(*xhtml.Node, bool) error
	visit = func(node *xhtml.Node, inTextElement bool) error {
		if node.Type == xhtml.ElementNode {
			if strings.EqualFold(node.Data, "body") {
				bodyNode = node
			}
			if strings.EqualFold(node.Data, "script") || oneOf(strings.ToLower(node.Data), "link", "iframe", "object", "embed") {
				return fmt.Errorf("final deck contains unsupported executable or external element <%s>", node.Data)
			}
			if strings.EqualFold(legacyNodeAttr(node, "data-deck-type"), "text") {
				inTextElement = true
			}
			if inTextElement && (processNodeHasAttr(node, "hidden") || strings.EqualFold(legacyNodeAttr(node, "aria-hidden"), "true")) {
				return fmt.Errorf("final deck hides an inspectable text element")
			}
			if inline := legacyNodeAttr(node, "style"); inline != "" {
				if err := validateProcessCSSDeclarations(inline, "inline CSS", !inTextElement); err != nil {
					return err
				}
			}
			if node.Data == "style" {
				var css strings.Builder
				for child := node.FirstChild; child != nil; child = child.NextSibling {
					if child.Type == xhtml.TextNode {
						css.WriteString(child.Data)
					}
				}
				rules := legacyCSSRulePattern.FindAllStringSubmatch(css.String(), -1)
				if len(rules) == 0 {
					if err := validateProcessCSSDeclarations(css.String(), "deck stylesheet", false); err != nil {
						return err
					}
				}
				for _, rule := range rules {
					if len(rule) == 3 {
						selector := strings.TrimSpace(processCSSUnescape(rule[1]))
						if regexp.MustCompile(`(?i)::?(?:before|after)\b`).MatchString(selector) {
							return fmt.Errorf("deck stylesheet uses a generated-content pseudo-element %q", compactAssistantLine(selector))
						}
						if err := validateProcessCSSDeclarations(rule[2], "deck stylesheet", processCSSSelectorMayHide(selector)); err != nil {
							return err
						}
					}
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			if err := visit(child, inTextElement); err != nil {
				return err
			}
		}
		return nil
	}
	if err := visit(doc, false); err != nil {
		return err
	}
	if bodyNode == nil {
		return fmt.Errorf("final deck has no body")
	}
	stageCount := 0
	for child := bodyNode.FirstChild; child != nil; child = child.NextSibling {
		switch child.Type {
		case xhtml.TextNode:
			if strings.TrimSpace(child.Data) != "" {
				return fmt.Errorf("final deck has visible text outside #stage")
			}
		case xhtml.ElementNode:
			if child.Data != "div" || legacyNodeAttr(child, "id") != "stage" {
				return fmt.Errorf("final deck has visible or interactive content outside #stage")
			}
			stageCount++
		}
	}
	if stageCount != 1 {
		return fmt.Errorf("final deck must have exactly one #stage body root")
	}
	return nil
}

func processDeckSlideExternalURLs(body string) ([][]string, error) {
	doc, err := xhtml.Parse(strings.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("deck HTML could not be parsed")
	}
	slides := make([]*xhtml.Node, 0)
	var collectSlides func(*xhtml.Node)
	collectSlides = func(node *xhtml.Node) {
		isStageChild := node.Type == xhtml.ElementNode && node.Data == "section" && node.Parent != nil && legacyNodeAttr(node.Parent, "id") == "stage"
		if node.Type == xhtml.ElementNode && (legacyNodeHasClass(node, "pg") || legacyNodeHasClass(node, "slide") || isStageChild) {
			slides = append(slides, node)
			return
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			collectSlides(child)
		}
	}
	collectSlides(doc)
	urls := make([][]string, len(slides))
	for index, slide := range slides {
		var collectURLs func(*xhtml.Node)
		collectURLs = func(node *xhtml.Node) {
			if node.Type == xhtml.ElementNode {
				for _, attribute := range node.Attr {
					for _, rawURL := range processMaterialURLPattern.FindAllString(attribute.Val, -1) {
						urls[index] = append(urls[index], strings.TrimRight(rawURL, ").,;:!?]}"))
					}
				}
			}
			for child := node.FirstChild; child != nil; child = child.NextSibling {
				collectURLs(child)
			}
		}
		collectURLs(slide)
	}
	return urls, nil
}

type processDeckSurfacedAttribute struct {
	Name  string
	Value string
}

func processDeckSlideSurfacedAttributes(body string) ([][]processDeckSurfacedAttribute, error) {
	doc, err := xhtml.Parse(strings.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("deck HTML could not be parsed")
	}
	slides := make([]*xhtml.Node, 0)
	var collectSlides func(*xhtml.Node)
	collectSlides = func(node *xhtml.Node) {
		isStageChild := node.Type == xhtml.ElementNode && node.Data == "section" && node.Parent != nil && legacyNodeAttr(node.Parent, "id") == "stage"
		if node.Type == xhtml.ElementNode && (legacyNodeHasClass(node, "pg") || legacyNodeHasClass(node, "slide") || isStageChild) {
			slides = append(slides, node)
			return
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			collectSlides(child)
		}
	}
	collectSlides(doc)
	attributes := make([][]processDeckSurfacedAttribute, len(slides))
	for index, slide := range slides {
		var collect func(*xhtml.Node)
		collect = func(node *xhtml.Node) {
			if node.Type == xhtml.ElementNode {
				for _, attribute := range node.Attr {
					name := strings.ToLower(strings.TrimSpace(attribute.Key))
					value := strings.TrimSpace(attribute.Val)
					if oneOf(name, "title", "aria-label", "aria-description", "aria-roledescription", "alt") && value != "" {
						attributes[index] = append(attributes[index], processDeckSurfacedAttribute{Name: name, Value: value})
					}
				}
			}
			for child := node.FirstChild; child != nil; child = child.NextSibling {
				collect(child)
			}
		}
		collect(slide)
	}
	return attributes, nil
}

func validateProcessDeckFactualClaims(body string, manifest processAdmittedClaimManifest) error {
	if err := validateProcessDeckNoGeneratedContent(body); err != nil {
		return err
	}
	artifact := meetingMemoryEntry{Text: body, Metadata: map[string]string{"type": artifactTypeHTMLDeck}}
	deck, quality := importLegacyDeckDocument(artifact)
	if quality != "faithful" {
		return fmt.Errorf("final deck could not be inspected as a faithful native scene")
	}
	comments, err := processDeckSlideAuthorityComments(body)
	if err != nil || len(comments) != len(deck.Slides) {
		return fmt.Errorf("final deck claim authority could not be matched to every slide")
	}
	urls, err := processDeckSlideExternalURLs(body)
	if err != nil || len(urls) != len(deck.Slides) {
		return fmt.Errorf("final deck source URLs could not be matched to every slide")
	}
	surfacedAttributes, err := processDeckSlideSurfacedAttributes(body)
	if err != nil || len(surfacedAttributes) != len(deck.Slides) {
		return fmt.Errorf("final deck surfaced accessibility copy could not be matched to every slide")
	}
	for index, slide := range deck.Slides {
		authorityScope := strings.Join(comments[index], "\n")
		anchors, anchorErr := processClaimAnchorsFromScope(authorityScope, manifest)
		if anchorErr != nil {
			return fmt.Errorf("slide %d claim authority is invalid: %w", index+1, anchorErr)
		}
		visible := make([]string, 0, len(slide.Elements))
		for attributeIndex, attribute := range surfacedAttributes[index] {
			if processDeckFractionCounterPattern.MatchString(attribute.Value) || processDeckNamedCounterPattern.MatchString(attribute.Value) || processDeckAccessibleCounterPattern.MatchString(attribute.Value) {
				continue
			}
			visible = append(visible, attribute.Value)
			if err := validateProcessFactText(attribute.Value, fmt.Sprintf("slide %d %s attribute %d", index+1, attribute.Name, attributeIndex+1), anchors, false); err != nil {
				return err
			}
		}
		for _, element := range slide.Elements {
			if element.Type != "text" || strings.TrimSpace(element.Text) == "" || processDeckStructuralText(element) {
				continue
			}
			if processForwardStatementAllowed(element.Text, processStatementNone, anchors) {
				continue
			}
			visible = append(visible, element.Text)
			if err := validateProcessFactText(element.Text, fmt.Sprintf("slide %d element %s", index+1, element.ID), anchors, false); err != nil {
				return err
			}
		}
		visibleAndURLs := strings.TrimSpace(strings.Join(append(visible, urls[index]...), " "))
		if err := validateProcessClaimURLBindings(visibleAndURLs, fmt.Sprintf("slide %d visible copy", index+1), anchors); err != nil {
			return err
		}
		if strings.TrimSpace(slide.Notes) != "" {
			if processForwardStatementAllowed(slide.Notes, processStatementNone, anchors) {
				continue
			}
			if err := validateProcessFactText(slide.Notes, fmt.Sprintf("slide %d presenter notes", index+1), anchors, false); err != nil {
				return err
			}
			if err := validateProcessClaimURLBindings(slide.Notes, fmt.Sprintf("slide %d presenter notes", index+1), anchors); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateProcessFactualClaims(body string, manifest processAdmittedClaimManifest) error {
	body = strings.TrimSpace(body)
	if object, ok := decodeProcessClaimJSON(body); ok {
		return validateProcessJSONClaimObject(object, "$", manifest)
	}
	paragraphs := processMarkdownParagraphPattern.Split(strings.ReplaceAll(body, "\r\n", "\n"), -1)
	unitIndex := 0
	for _, paragraph := range paragraphs {
		if strings.TrimSpace(paragraph) == "" {
			continue
		}
		lines := strings.Split(paragraph, "\n")
		hasTableRow := false
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "|") && strings.HasSuffix(trimmed, "|") {
				hasTableRow = true
				break
			}
		}
		units := []string{paragraph}
		if hasTableRow {
			units = units[:0]
			for _, line := range lines {
				if strings.TrimSpace(line) != "" {
					units = append(units, line)
				}
			}
		}
		for _, unit := range units {
			unitIndex++
			if err := validateProcessMarkdownClaimScope(unit, unitIndex, manifest); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateProcessStageFactualClaims(app *kanbanBoardApp, plan *goalPlan, parentID string, stage ProcessStage, body string) error {
	if !processClaimGateStage(plan, stage) {
		return nil
	}
	manifest, err := loadProcessAdmittedClaimManifest(app, plan, parentID)
	if err != nil {
		return fmt.Errorf("factual claim gate could not load authority: %w", err)
	}
	if stage.ID == "ship_deck" {
		err = validateProcessDeckFactualClaims(body, manifest)
	} else {
		err = validateProcessFactualClaims(body, manifest)
	}
	if err != nil {
		return fmt.Errorf("factual claim gate rejected %s: %w", stage.ID, err)
	}
	return nil
}

func validateGroundedProcessWriterFactualClaims(app *kanbanBoardApp, thread scoutAgentThread, body string) error {
	if !agentThreadUsesGroundedDeliverableContract(thread) {
		return nil
	}
	contract := strings.TrimSpace(thread.Artifact.Metadata["outputContract"])
	if !oneOf(contract, documentReportOutputContract, "presenter_script_v2", "layout_plan_v3", packagingStudioDeckContract) {
		return nil
	}
	if app == nil {
		return fmt.Errorf("process factual claim gate requires the live parent authority")
	}
	parentID := strings.TrimSpace(thread.Artifact.Metadata["goalParentId"])
	subtaskID := strings.TrimSpace(thread.Artifact.Metadata["goalSubtaskId"])
	parent, ok := app.osArtifactByID(parentID)
	if !ok {
		return fmt.Errorf("process factual claim gate could not load the parent goal")
	}
	plan, ok := decodeGoalPlan(parent.Metadata["goalPlan"])
	if !ok || !oneOf(plan.ProcessID, documentReportProcessID, packagingStudioProcessID) {
		return fmt.Errorf("process factual claim gate found an invalid parent process")
	}
	engine := newGoalEngine(app)
	if err := engine.prepareGoalRoute(&plan, parentID); err != nil {
		return fmt.Errorf("process factual claim gate found invalid route authority: %w", err)
	}
	definition, err := resolvePinnedProcessDefinition(&plan)
	if err != nil {
		return fmt.Errorf("process factual claim gate found process identity drift: %w", err)
	}
	stage, ok := definition.stageByID(subtaskID)
	if !ok || !processClaimGateStage(&plan, stage) || strings.TrimSpace(stage.OutputContract) != contract {
		return fmt.Errorf("process factual claim gate found an invalid writer stage")
	}
	subtask := plan.subtaskByID(subtaskID)
	if subtask == nil || strings.TrimSpace(subtask.ArtifactID) != strings.TrimSpace(thread.Artifact.ID) || strings.TrimSpace(subtask.ThreadID) != strings.TrimSpace(thread.ID) {
		return fmt.Errorf("process factual claim gate writer is not the exact current goal child")
	}
	return validateProcessStageFactualClaims(app, &plan, parentID, stage, body)
}
