package main

import (
	"strings"
	"testing"
)

func processClaimGateFixture() (processAdmittedClaimManifest, processAdmittedClaim) {
	claim := processAdmittedClaim{
		ID:           strings.Repeat("a", 64),
		ExactClaim:   "The addressable creator market was $4.2 billion in 2026.",
		RequestedURL: "https://example.com/market",
		FinalURL:     "https://www.example.com/research/market",
	}
	return processAdmittedClaimManifest{claim.ID: claim}, claim
}

func TestProcessClaimGateResearchModeNoneRejectsExternalNumber(t *testing.T) {
	// A none/internal research run has an empty admitted manifest. A plausible
	// market number from model memory must fail deterministically, even if the
	// prose otherwise looks polished.
	body := `{"story":{"thesis":"The market is worth $4.2 billion."}}`
	err := validateProcessFactualClaims(body, processAdmittedClaimManifest{})
	if err == nil || !strings.Contains(err.Error(), "outside every exact admitted claim") {
		t.Fatalf("unsupported external number passed: %v", err)
	}
}

func TestProcessClaimGateStoryRejectsMissingClaimIDOrExactText(t *testing.T) {
	manifest, claim := processClaimGateFixture()
	cases := []struct {
		name string
		body string
		want string
	}{
		{
			name: "missing id",
			body: `{"turns":[{"argument":"The market was $4.2 billion in 2026.","exact_claims":["The addressable creator market was $4.2 billion in 2026."]}]}`,
			want: "exact_claims contains text not paired",
		},
		{
			name: "missing exact admitted text",
			body: `{"turns":[{"argument":"The market was $4.2 billion in 2026.","claim_ids":["` + claim.ID + `"]}]}`,
			want: "missing its exact admitted text",
		},
		{
			name: "rejected id",
			body: `{"turns":[{"argument":"The market was $4.2 billion in 2026.","claim_ids":["` + strings.Repeat("b", 64) + `"],"exact_claims":["The addressable creator market was $4.2 billion in 2026."]}]}`,
			want: "was not admitted",
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			err := validateProcessFactualClaims(test.body, manifest)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("body passed or returned wrong failure: %v", err)
			}
		})
	}
}

func TestProcessClaimGateDeckCopyAcceptsSiblingExactAnchor(t *testing.T) {
	manifest, claim := processClaimGateFixture()
	body := `{"slides":[{"slide_id":"slide-2","visible_copy":"` + claim.ExactClaim + `","source_url":"https://example.com/market","claim_ids":["` + claim.ID + `"],"exact_claims":["` + claim.ExactClaim + `"]}]}`
	if err := validateProcessFactualClaims(body, manifest); err != nil {
		t.Fatalf("exactly anchored deck copy failed: %v", err)
	}

	tampered := strings.Replace(body, "$4.2 billion", "$6.8 billion", 1)
	if err := validateProcessFactualClaims(tampered, manifest); err == nil || !strings.Contains(err.Error(), "$6.8 billion") {
		t.Fatalf("numerically drifted deck copy passed: %v", err)
	}
}

func TestProcessClaimGateDocumentParagraphRequiresHiddenExactAnchor(t *testing.T) {
	manifest, claim := processClaimGateFixture()
	paragraph := claim.ExactClaim + " [Source](https://example.com/market)."
	if err := validateProcessFactualClaims("# Opportunity\n\n"+paragraph, manifest); err == nil || !strings.Contains(err.Error(), "stride-claim") {
		t.Fatalf("unanchored report paragraph passed: %v", err)
	}

	anchored := "# Opportunity\n\n" + paragraph + " <!-- stride-claim:" + claim.ID + " | " + claim.ExactClaim + " -->"
	if err := validateProcessFactualClaims(anchored, manifest); err != nil {
		t.Fatalf("exactly anchored report paragraph failed: %v", err)
	}

	wrongURL := strings.Replace(anchored, "https://example.com/market", "https://example.net/other", 1)
	if err := validateProcessFactualClaims(wrongURL, manifest); err == nil || !strings.Contains(err.Error(), "not bound to its own exact") {
		t.Fatalf("unadmitted report URL passed: %v", err)
	}
}

func TestProcessClaimGateRejectsSharedTokenPredicateSwapAndWrappers(t *testing.T) {
	claim := processAdmittedClaim{ID: strings.Repeat("c", 64), ExactClaim: "4,200 creators were surveyed in 2026."}
	manifest := processAdmittedClaimManifest{claim.ID: claim}
	anchor := `,"claim_ids":["` + claim.ID + `"],"exact_claims":["` + claim.ExactClaim + `"]}`
	cases := []string{
		`{"copy":"4,200 creators purchased in 2026."` + anchor,
		`{"copy":"It is false that 4,200 creators were surveyed in 2026."` + anchor,
		`{"copy":"4,200 creators were surveyed in 2026. They all purchased."` + anchor,
	}
	for index, body := range cases {
		if err := validateProcessFactualClaims(body, manifest); err == nil {
			t.Fatalf("semantic laundering case %d passed: %s", index+1, body)
		}
	}
}

func TestProcessClaimGateRejectsUnsupportedQualitativeAndGenericInteger(t *testing.T) {
	for _, body := range []string{
		`{"thesis":"We are the market leader."}`,
		`{"proof":"The network operates in 47 countries."}`,
	} {
		if err := validateProcessFactualClaims(body, processAdmittedClaimManifest{}); err == nil {
			t.Fatalf("unsupported factual assertion passed: %s", body)
		}
	}
}

func TestProcessClaimGateAllowsRealisticStructuralVoiceAndLayout(t *testing.T) {
	voice := "## Slide 1\n\nOpen with the human tension. [BEAT]\n\nTransition to slide 2."
	if err := validateProcessFactualClaims(voice, processAdmittedClaimManifest{}); err != nil {
		t.Fatalf("structural voice labels became claims: %v", err)
	}
	layout := `{"canvas":{"width":1920,"height":1080},"grid":{"columns":12,"gutter":24},"palette":{"primary":"#101014","accent":"#FF5500"},"slides":[{"slide_id":"slide-1","elements":[{"id":"headline","type":"text","x":96,"y":120,"width":1600,"height":220,"font_size":104,"font_weight":700,"text":"A bold invitation"}]}]}`
	if err := validateProcessFactualClaims(layout, processAdmittedClaimManifest{}); err != nil {
		t.Fatalf("layout geometry/style became factual claims: %v", err)
	}
}

func TestProcessClaimGateFinalDeckRejectsInjectedFact(t *testing.T) {
	claim := processAdmittedClaim{ID: strings.Repeat("d", 64), ExactClaim: "4,200 creators were surveyed in 2026."}
	manifest := processAdmittedClaimManifest{claim.ID: claim}
	marker := "<!-- stride-claim:" + claim.ID + " | " + claim.ExactClaim + " -->"
	withMarker := strings.Replace(faithfulDeckHTML, `<div data-deck-element="headline"`, marker+`<div data-deck-element="headline"`, 1)
	valid := strings.Replace(withMarker, "Like a Farmer", claim.ExactClaim, 1)
	if err := validateProcessDeckFactualClaims(valid, manifest); err != nil {
		t.Fatalf("exact fact in faithful final deck failed: %v", err)
	}
	injected := strings.Replace(withMarker, "Like a Farmer", "4,200 creators purchased in 2026.", 1)
	if err := validateProcessDeckFactualClaims(injected, manifest); err == nil {
		t.Fatal("final ship_deck predicate injection passed")
	}
	if err := validateProcessDeckFactualClaims(faithfulDeckHTML, processAdmittedClaimManifest{}); err != nil {
		t.Fatalf("ordinary structural deck failed factual gate: %v", err)
	}
}

func TestProcessClaimGateRejectsClaimModalityWrappersAcrossOutputs(t *testing.T) {
	claim := processAdmittedClaim{ID: strings.Repeat("e", 64), ExactClaim: "4,200 creators were surveyed in 2026."}
	manifest := processAdmittedClaimManifest{claim.ID: claim}
	marker := "<!-- stride-claim:" + claim.ID + " | " + claim.ExactClaim + " -->"
	jsonAnchor := `,"claim_ids":["` + claim.ID + `"],"exact_claims":["` + claim.ExactClaim + `"]}`
	wrappers := []string{
		"False: " + claim.ExactClaim,
		claim.ExactClaim + " — allegedly.",
		claim.ExactClaim + " no longer applies.",
		"It may be that " + claim.ExactClaim,
		"Reportedly " + claim.ExactClaim,
	}
	for _, wrapped := range wrappers {
		t.Run(compactAssistantLine(wrapped), func(t *testing.T) {
			jsonBody := `{"copy":"` + wrapped + `"` + jsonAnchor
			if err := validateProcessFactualClaims(jsonBody, manifest); err == nil {
				t.Fatal("JSON claim wrapper passed")
			}
			if err := validateProcessFactualClaims(wrapped+" "+marker, manifest); err == nil {
				t.Fatal("Markdown claim wrapper passed")
			}
			deck := strings.Replace(faithfulDeckHTML, `<div data-deck-element="headline"`, marker+`<div data-deck-element="headline"`, 1)
			deck = strings.Replace(deck, "Like a Farmer", wrapped, 1)
			if err := validateProcessDeckFactualClaims(deck, manifest); err == nil {
				t.Fatal("final-deck claim wrapper passed")
			}
		})
	}
}

func TestProcessClaimGateRejectsOrdinaryQualitativeAssertionsAcrossOutputs(t *testing.T) {
	assertions := []string{
		"Acme powers creator teams worldwide.",
		"Creator engagement drives sales.",
	}
	for _, assertion := range assertions {
		t.Run(assertion, func(t *testing.T) {
			if err := validateProcessFactualClaims(`{"copy":"`+assertion+`"}`, processAdmittedClaimManifest{}); err == nil {
				t.Fatal("JSON assertion passed")
			}
			if err := validateProcessFactualClaims(assertion, processAdmittedClaimManifest{}); err == nil {
				t.Fatal("Markdown assertion passed")
			}
			deck := strings.Replace(faithfulDeckHTML, "Like a Farmer", assertion, 1)
			if err := validateProcessDeckFactualClaims(deck, processAdmittedClaimManifest{}); err == nil {
				t.Fatal("final-deck assertion passed")
			}
		})
	}
}

func TestProcessClaimGateForwardStatementContract(t *testing.T) {
	recommendation := "Recommendation: run a 30-day pilot with 100 opt-in creators."
	if err := validateProcessFactualClaims(`{"statement_type":"recommendation","text":"`+recommendation+`"}`, processAdmittedClaimManifest{}); err != nil {
		t.Fatalf("typed recommendation failed: %v", err)
	}
	if err := validateProcessFactualClaims(recommendation, processAdmittedClaimManifest{}); err != nil {
		t.Fatalf("visibly labeled Markdown recommendation failed: %v", err)
	}
	for name, body := range map[string]string{
		"JSON missing type":  `{"text":"` + recommendation + `"}`,
		"JSON missing label": `{"statement_type":"recommendation","text":"Run a 30-day pilot with 100 opt-in creators."}`,
		"wrong JSON type":    `{"statement_type":"inference","text":"` + recommendation + `"}`,
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateProcessFactualClaims(body, processAdmittedClaimManifest{}); err == nil {
				t.Fatal("untyped or mismatched forward statement passed")
			}
		})
	}
	if err := validateProcessFactualClaims(`{"statement_type":"proposal","label":"Phase 1"}`, processAdmittedClaimManifest{}); err != nil {
		t.Fatalf("typed proposal phase failed: %v", err)
	}
	if err := validateProcessFactualClaims("## Phase 1", processAdmittedClaimManifest{}); err != nil {
		t.Fatalf("visible Markdown phase failed: %v", err)
	}
	if err := validateProcessFactualClaims(`{"label":"Phase 1"}`, processAdmittedClaimManifest{}); err == nil {
		t.Fatal("untyped JSON phase passed")
	}
	if err := validateProcessFactualClaims(`{"statement_type":"inference","text":"Inference: demand appears latent."}`, processAdmittedClaimManifest{}); err != nil {
		t.Fatalf("typed inference failed: %v", err)
	}
	if err := validateProcessFactualClaims(`{"statement_type":"inference","text":"Inference: Creator engagement drives sales."}`, processAdmittedClaimManifest{}); err == nil {
		t.Fatal("typed inference laundered a qualitative factual assertion")
	}
	if err := validateProcessFactualClaims(`{"statement_type":"recommendation","text":"Recommendation: review https://example.com/playbook."}`, processAdmittedClaimManifest{}); err == nil {
		t.Fatal("typed recommendation laundered an external URL")
	}
	deck := strings.Replace(faithfulDeckHTML, "Like a Farmer", recommendation, 1)
	if err := validateProcessDeckFactualClaims(deck, processAdmittedClaimManifest{}); err != nil {
		t.Fatalf("visibly labeled deck recommendation failed: %v", err)
	}
	unlabeledDeck := strings.Replace(faithfulDeckHTML, "Like a Farmer", "Run a 30-day pilot with 100 opt-in creators.", 1)
	if err := validateProcessDeckFactualClaims(unlabeledDeck, processAdmittedClaimManifest{}); err == nil {
		t.Fatal("unlabeled deck proposal passed")
	}
}

func TestProcessClaimGateBindsEachURLToItsOwnVisibleClaim(t *testing.T) {
	claimA := processAdmittedClaim{
		ID: strings.Repeat("1", 64), ExactClaim: "Acme surveyed 1,000 creators in 2026.",
		RequestedURL: "https://example.com/acme", FinalURL: "https://www.example.com/acme-study",
	}
	claimB := processAdmittedClaim{
		ID: strings.Repeat("2", 64), ExactClaim: "Beta surveyed 2,000 creators in 2026.",
		RequestedURL: "https://example.com/beta", FinalURL: "https://www.example.com/beta-study",
	}
	manifest := processAdmittedClaimManifest{claimA.ID: claimA, claimB.ID: claimB}
	ids := `"claim_ids":["` + claimA.ID + `","` + claimB.ID + `"]`
	exacts := `"exact_claims":["` + claimA.ExactClaim + `","` + claimB.ExactClaim + `"]`
	crossBoundJSON := `{"copy":"` + claimA.ExactClaim + `","source_url":"` + claimB.RequestedURL + `",` + ids + `,` + exacts + `}`
	if err := validateProcessFactualClaims(crossBoundJSON, manifest); err == nil || !strings.Contains(err.Error(), "not bound to its own exact") {
		t.Fatalf("cross-bound JSON URL passed or failed unclearly: %v", err)
	}
	ownBoundJSON := strings.Replace(crossBoundJSON, claimB.RequestedURL, claimA.RequestedURL, 1)
	if err := validateProcessFactualClaims(ownBoundJSON, manifest); err != nil {
		t.Fatalf("own-bound JSON URL failed: %v", err)
	}
	markerA := "<!-- stride-claim:" + claimA.ID + " | " + claimA.ExactClaim + " -->"
	markerB := "<!-- stride-claim:" + claimB.ID + " | " + claimB.ExactClaim + " -->"
	markdown := claimA.ExactClaim + " [Source](" + claimB.RequestedURL + "). " + markerA + markerB
	if err := validateProcessFactualClaims(markdown, manifest); err == nil || !strings.Contains(err.Error(), "not bound to its own exact") {
		t.Fatalf("cross-bound Markdown URL passed or failed unclearly: %v", err)
	}
	deck := strings.Replace(faithfulDeckHTML, `<div data-deck-element="headline"`, markerA+markerB+`<div data-source-url="`+claimB.RequestedURL+`" data-deck-element="headline"`, 1)
	deck = strings.Replace(deck, "Like a Farmer", claimA.ExactClaim, 1)
	if err := validateProcessDeckFactualClaims(deck, manifest); err == nil || !strings.Contains(err.Error(), "not bound to its own exact") {
		t.Fatalf("cross-bound final-deck URL passed or failed unclearly: %v", err)
	}
}

func TestProcessClaimGateRecursesStructuralSubtreesButAllowsGeometry(t *testing.T) {
	valid := `{"slides":[{"style":{"border_radius":24,"corner_radius":18,"padding":32,"gap":20,"blur":4,"scale":1.05,"zoom":1,"z_index":3,"palette":{"primary":"#101014","accent":"#FF5500"}},"elements":[{"style":{"border_radius":12,"padding":8},"text":"A bold invitation"}]}]}`
	if err := validateProcessFactualClaims(valid, processAdmittedClaimManifest{}); err != nil {
		t.Fatalf("nested geometry/style became evidence: %v", err)
	}
	injected := `{"slides":[{"style":{"border_radius":24,"states":[{"content":"Acme powers creator teams worldwide."}]}}]}`
	if err := validateProcessFactualClaims(injected, processAdmittedClaimManifest{}); err == nil {
		t.Fatal("content hidden in a structural subtree passed")
	}
}

func TestProcessClaimGateDeckCountersRequireExplicitFormatOrRole(t *testing.T) {
	bareMetric := strings.Replace(faithfulDeckHTML, "Like a Farmer", "47", 1)
	if err := validateProcessDeckFactualClaims(bareMetric, processAdmittedClaimManifest{}); err == nil {
		t.Fatal("bare final-deck metric was treated as a page counter")
	}
	fraction := strings.Replace(faithfulDeckHTML, "Like a Farmer", "1 / 10", 1)
	if err := validateProcessDeckFactualClaims(fraction, processAdmittedClaimManifest{}); err != nil {
		t.Fatalf("explicit fraction counter failed: %v", err)
	}
	roleCounter := strings.Replace(faithfulDeckHTML, `data-deck-element="headline"`, `data-deck-element="page-counter"`, 1)
	roleCounter = strings.Replace(roleCounter, "Like a Farmer", "1", 1)
	if err := validateProcessDeckFactualClaims(roleCounter, processAdmittedClaimManifest{}); err != nil {
		t.Fatalf("explicit page-counter role failed: %v", err)
	}
}

func TestProcessClaimGateRejectsCSSGeneratedVisibleCopy(t *testing.T) {
	cases := []string{
		strings.Replace(faithfulDeckHTML, "</body>", `<style>.metric::after{content:"47 countries"}</style></body>`, 1),
		strings.Replace(faithfulDeckHTML, `style="position:absolute`, `style="content:'Creator engagement drives sales';position:absolute`, 1),
	}
	for index, deck := range cases {
		if err := validateProcessDeckFactualClaims(deck, processAdmittedClaimManifest{}); err == nil || !(strings.Contains(err.Error(), "CSS-generated visible content") || strings.Contains(err.Error(), "generated-content pseudo-element")) {
			t.Fatalf("CSS content case %d passed or failed unclearly: %v", index+1, err)
		}
	}
}

func TestProcessClaimGatePositiveFactUnitRejectsUnboundedWrappers(t *testing.T) {
	claim := processAdmittedClaim{ID: strings.Repeat("f", 64), ExactClaim: "4,200 creators were surveyed in 2026."}
	manifest := processAdmittedClaimManifest{claim.ID: claim}
	marker := "<!-- stride-claim:" + claim.ID + " | " + claim.ExactClaim + " -->"
	wrappers := []string{
		"This is wrong: " + claim.ExactClaim,
		claim.ExactClaim + " (unconfirmed).",
		claim.ExactClaim + " was later corrected.",
		claim.ExactClaim + ", if true.",
		claim.ExactClaim + " but the source later retracted it.",
	}
	for _, wrapped := range wrappers {
		t.Run(wrapped, func(t *testing.T) {
			jsonBody := `{"copy":"` + wrapped + `","claim_ids":["` + claim.ID + `"],"exact_claims":["` + claim.ExactClaim + `"]}`
			if err := validateProcessFactualClaims(jsonBody, manifest); err == nil {
				t.Fatal("wrapped exact claim passed JSON")
			}
			if err := validateProcessFactualClaims(wrapped+" "+marker, manifest); err == nil {
				t.Fatal("wrapped exact claim passed Markdown")
			}
			deck := strings.Replace(faithfulDeckHTML, `<div data-deck-element="headline"`, marker+`<div data-deck-element="headline"`, 1)
			deck = strings.Replace(deck, "Like a Farmer", wrapped, 1)
			if err := validateProcessDeckFactualClaims(deck, manifest); err == nil {
				t.Fatal("wrapped exact claim passed final deck")
			}
		})
	}
	struck := "~~" + claim.ExactClaim + "~~ " + marker
	if err := validateProcessFactualClaims(struck, manifest); err == nil || !strings.Contains(err.Error(), "struck through") {
		t.Fatalf("Markdown strikethrough passed or failed unclearly: %v", err)
	}
}

func TestProcessClaimGateRejectsUnanchoredDeclarativeQualitativeClaims(t *testing.T) {
	assertions := []string{
		"Acme is trusted worldwide.",
		"Acme has exclusive creator contracts.",
		"Acme remains the category standard.",
	}
	for _, assertion := range assertions {
		t.Run(assertion, func(t *testing.T) {
			if err := validateProcessFactualClaims(`{"copy":"`+assertion+`"}`, processAdmittedClaimManifest{}); err == nil {
				t.Fatal("qualitative JSON claim passed")
			}
			if err := validateProcessFactualClaims(assertion, processAdmittedClaimManifest{}); err == nil {
				t.Fatal("qualitative Markdown claim passed")
			}
			deck := strings.Replace(faithfulDeckHTML, "Like a Farmer", assertion, 1)
			if err := validateProcessDeckFactualClaims(deck, processAdmittedClaimManifest{}); err == nil {
				t.Fatal("qualitative final-deck claim passed")
			}
		})
	}
}

func TestProcessClaimGateFactualHeadlineFragmentsRequireExactEvidence(t *testing.T) {
	for _, headline := range []string{
		"Trusted worldwide",
		"The preferred platform for leading brands",
		"Creators' top choice",
		"The industry's most trusted network",
		"Available worldwide",
		"Trusted by creator teams",
		"A leading platform",
	} {
		t.Run(headline, func(t *testing.T) {
			if err := validateProcessFactualClaims(headline, processAdmittedClaimManifest{}); err == nil {
				t.Fatal("unadmitted factual headline fragment passed")
			}
			deck := strings.Replace(faithfulDeckHTML, "Like a Farmer", headline, 1)
			if err := validateProcessDeckFactualClaims(deck, processAdmittedClaimManifest{}); err == nil {
				t.Fatal("unadmitted final-deck headline fragment passed")
			}
		})
	}
	for _, title := range []string{"A bold invitation", "The next chapter", "What we heard", "Opportunity"} {
		if err := validateProcessFactualClaims(title, processAdmittedClaimManifest{}); err != nil {
			t.Fatalf("nonfactual narrative title %q was rejected: %v", title, err)
		}
	}
	claim := processAdmittedClaim{ID: strings.Repeat("9", 64), ExactClaim: "Trusted worldwide"}
	marker := "<!-- stride-claim:" + claim.ID + " | " + claim.ExactClaim + " -->"
	if err := validateProcessFactualClaims(claim.ExactClaim+" "+marker, processAdmittedClaimManifest{claim.ID: claim}); err != nil {
		t.Fatalf("exactly admitted factual headline failed: %v", err)
	}
}

func TestProcessClaimGateForwardLabelsCannotLaunderPastFacts(t *testing.T) {
	cases := []string{
		"Recommendation: Acme earned $4.2 billion in 2026.",
		"Recommendation: Acme paid 100 creators last year.",
		"Target: Acme had 100 creators in 2026.",
	}
	for _, statement := range cases {
		t.Run(statement, func(t *testing.T) {
			statementType := "recommendation"
			if strings.HasPrefix(statement, "Target:") {
				statementType = "proposal"
			}
			if err := validateProcessFactualClaims(`{"statement_type":"`+statementType+`","text":"`+statement+`"}`, processAdmittedClaimManifest{}); err == nil {
				t.Fatal("past fact passed typed JSON forward scope")
			}
			if err := validateProcessFactualClaims(statement, processAdmittedClaimManifest{}); err == nil {
				t.Fatal("past fact passed visible Markdown forward scope")
			}
			deck := strings.Replace(faithfulDeckHTML, "Like a Farmer", statement, 1)
			if err := validateProcessDeckFactualClaims(deck, processAdmittedClaimManifest{}); err == nil {
				t.Fatal("past fact passed final-deck forward scope")
			}
		})
	}
}

func TestProcessClaimGateDetectsSpelledFullwidthAndUppercaseMaterial(t *testing.T) {
	values := []string{
		"The network has four thousand creators.",
		"The network operates in ４７ countries.",
		"Read HTTPS://evil.example/fact.",
	}
	for _, value := range values {
		t.Run(value, func(t *testing.T) {
			if err := validateProcessFactualClaims(`{"copy":"`+value+`"}`, processAdmittedClaimManifest{}); err == nil {
				t.Fatal("material JSON value passed")
			}
			if err := validateProcessFactualClaims(value, processAdmittedClaimManifest{}); err == nil {
				t.Fatal("material Markdown value passed")
			}
			deck := strings.Replace(faithfulDeckHTML, "Like a Farmer", value, 1)
			if err := validateProcessDeckFactualClaims(deck, processAdmittedClaimManifest{}); err == nil {
				t.Fatal("material final-deck value passed")
			}
		})
	}
}

func TestProcessClaimGateRecursesNestedArraysAndValidatesStructuralScalars(t *testing.T) {
	cases := []string{
		`{"copy":[[["four thousand creators"]]]}`,
		`{"style":{"states":[[[{"content":"Acme is trusted worldwide."}]]]]}}`,
		`{"background":"Acme powers 47 creator teams worldwide."}`,
		`{"composition":"Acme is trusted worldwide."}`,
		`{"style":"HTTPS://evil.example/fact"}`,
	}
	for _, body := range cases {
		if err := validateProcessFactualClaims(body, processAdmittedClaimManifest{}); err == nil {
			t.Fatalf("nested or structural scalar injection passed: %s", body)
		}
	}
}

func TestProcessClaimGateScopesMarkdownEvidencePerTableRow(t *testing.T) {
	claimA := processAdmittedClaim{ID: strings.Repeat("3", 64), ExactClaim: "Acme surveyed 1,000 creators in 2026.", RequestedURL: "https://example.com/acme"}
	claimB := processAdmittedClaim{ID: strings.Repeat("4", 64), ExactClaim: "Beta surveyed 2,000 creators in 2026.", RequestedURL: "https://example.com/beta"}
	manifest := processAdmittedClaimManifest{claimA.ID: claimA, claimB.ID: claimB}
	marker := func(claim processAdmittedClaim) string {
		return "<!-- stride-claim:" + claim.ID + " | " + claim.ExactClaim + " -->"
	}
	crossed := strings.Join([]string{
		"| Claim | Source |",
		"|---|---|",
		"| " + claimA.ExactClaim + " " + marker(claimA) + marker(claimB) + " | [Source](" + claimB.RequestedURL + ") |",
		"| " + claimB.ExactClaim + " " + marker(claimA) + marker(claimB) + " | [Source](" + claimA.RequestedURL + ") |",
	}, "\n")
	if err := validateProcessFactualClaims(crossed, manifest); err == nil || !strings.Contains(err.Error(), "not bound to its own exact") {
		t.Fatalf("cross-bound Markdown table passed or failed unclearly: %v", err)
	}
	bound := strings.Join([]string{
		"| Claim | Source |",
		"|---|---|",
		"| " + claimA.ExactClaim + " " + marker(claimA) + " | [Source](" + claimA.RequestedURL + ") |",
		"| " + claimB.ExactClaim + " " + marker(claimB) + " | [Source](" + claimB.RequestedURL + ") |",
	}, "\n")
	if err := validateProcessFactualClaims(bound, manifest); err != nil {
		t.Fatalf("correctly row-bound Markdown table failed: %v", err)
	}
}

func TestProcessClaimGateRejectsOutsideStageAndSemanticCSSHiding(t *testing.T) {
	cases := []string{
		strings.Replace(faithfulDeckHTML, "</body>", `<div style="position:fixed;inset:0">Acme earned $9.9 billion in 2026.</div></body>`, 1),
		strings.Replace(faithfulDeckHTML, "</body>", `<a href="HTTPS://evil.example/fact">Source</a></body>`, 1),
		strings.Replace(faithfulDeckHTML, "font-size:104px", "font-size:104px;display:none", 1),
		strings.Replace(faithfulDeckHTML, "Like a Farmer", `<span style="visibility:hidden">Acme is trusted worldwide.</span>Like a Farmer`, 1),
		strings.Replace(faithfulDeckHTML, "</body>", `<style>.pg::after{\63ontent:"47 countries"}</style></body>`, 1),
		strings.Replace(faithfulDeckHTML, "</body>", `<style>.pg::after{con\74 ent:"47 countries"}</style></body>`, 1),
	}
	for index, deck := range cases {
		if err := validateProcessDeckFactualClaims(deck, processAdmittedClaimManifest{}); err == nil {
			t.Fatalf("outside-stage/CSS semantic bypass %d passed", index+1)
		}
	}
}

func TestProcessClaimGateRejectsPolarityStatusGlyphWrappers(t *testing.T) {
	claim := processAdmittedClaim{ID: strings.Repeat("a", 64), ExactClaim: "Acme surveyed 4,200 creators in 2026."}
	manifest := processAdmittedClaimManifest{claim.ID: claim}
	marker := "<!-- stride-claim:" + claim.ID + " | " + claim.ExactClaim + " -->"
	for _, wrapped := range []string{
		"❌ " + claim.ExactClaim, "⚠️ " + claim.ExactClaim, "🙅 " + claim.ExactClaim, "✅ " + claim.ExactClaim,
		"¬ " + claim.ExactClaim, "⊘ " + claim.ExactClaim, "⨯ " + claim.ExactClaim, "≠ " + claim.ExactClaim,
	} {
		if err := validateProcessFactualClaims(wrapped+" "+marker, manifest); err == nil {
			t.Fatalf("polarity/status symbol passed: %q", wrapped)
		}
	}
}

func TestProcessClaimGateRejectsUnicodeAndSpelledNominalMetrics(t *testing.T) {
	for _, value := range []string{
		"Forty-seven countries",
		"𝟜𝟟 countries",
		"٤٧ countries",
		"۴۷ markets",
		"४७ regions",
		"A dozen countries",
		"Dozens of creators",
		"A score of markets",
		"XLVII countries",
		"⁴⁷ countries",
	} {
		if err := validateProcessFactualClaims(value, processAdmittedClaimManifest{}); err == nil {
			t.Fatalf("unadmitted Unicode or spelled nominal metric passed: %q", value)
		}
	}
}

func TestProcessClaimGateStructuralWhitelistRejectsSemanticSizeSuffixes(t *testing.T) {
	for _, body := range []string{
		`{"market_size":4.2}`,
		`{"audience_size":4200}`,
		`{"slides":[{"style":{"market_size":4.2}}]}`,
		`{"financial":{"opacity":4200}}`,
		`{"Acme_has_4200_creators":true}`,
		`{"slides":[{"elements":[{"type":"text","copy":{"size":4200}}]}]}`,
		`{"styleguide":{"width":4200}}`,
	} {
		if err := validateProcessFactualClaims(body, processAdmittedClaimManifest{}); err == nil {
			t.Fatalf("semantic numeric field passed structural whitelist: %s", body)
		}
	}
}

func TestProcessClaimGateBindsMarkdownCitationsToTheirOwnSentence(t *testing.T) {
	a := processAdmittedClaim{ID: strings.Repeat("a", 64), ExactClaim: "Acme surveyed 1,000 creators in 2026.", RequestedURL: "https://example.com/acme"}
	b := processAdmittedClaim{ID: strings.Repeat("b", 64), ExactClaim: "Beta surveyed 2,000 creators in 2026.", RequestedURL: "https://example.com/beta"}
	manifest := processAdmittedClaimManifest{a.ID: a, b.ID: b}
	marker := func(claim processAdmittedClaim) string {
		return "<!-- stride-claim:" + claim.ID + " | " + claim.ExactClaim + " -->"
	}
	crossed := a.ExactClaim + " [Source](" + b.RequestedURL + "). " + b.ExactClaim + " [Source](" + a.RequestedURL + "). " + marker(a) + marker(b)
	if err := validateProcessFactualClaims(crossed, manifest); err == nil || !strings.Contains(err.Error(), "that sentence") {
		t.Fatalf("cross-wired same-paragraph citations passed or failed unclearly: %v", err)
	}
	bound := a.ExactClaim + " [Source](" + a.RequestedURL + "). " + b.ExactClaim + " [Source](" + b.RequestedURL + "). " + marker(a) + marker(b)
	if err := validateProcessFactualClaims(bound, manifest); err != nil {
		t.Fatalf("sentence-local citations were rejected: %v", err)
	}
}

func TestProcessClaimGateInspectsSurfacedDeckAttributes(t *testing.T) {
	for _, replacement := range []string{
		`data-deck-element="headline" title="Acme earned $9.9 billion in 2026."`,
		`data-deck-element="headline" aria-label="Acme operates in forty-seven countries."`,
		`data-deck-element="headline" aria-description="Acme earned $9.9 billion in 2026."`,
		`data-deck-element="headline" aria-roledescription="Acme operates in forty-seven countries."`,
		`data-deck-element="headline" alt="Acme has 4,200 creators in 2026."`,
	} {
		deck := strings.Replace(faithfulDeckHTML, `data-deck-element="headline"`, replacement, 1)
		if err := validateProcessDeckFactualClaims(deck, processAdmittedClaimManifest{}); err == nil {
			t.Fatalf("factual surfaced attribute passed: %s", replacement)
		}
	}
	benign := strings.Replace(faithfulDeckHTML, `data-deck-element="headline"`, `data-deck-element="headline" aria-label="Cover headline"`, 1)
	if err := validateProcessDeckFactualClaims(benign, processAdmittedClaimManifest{}); err != nil {
		t.Fatalf("benign accessibility label failed: %v", err)
	}
}
