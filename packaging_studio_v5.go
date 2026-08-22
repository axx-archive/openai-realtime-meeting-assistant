package main

import (
	"strings"
	"time"
)

const processForwardStatementPromptLawV5 = "FORWARD-STATEMENT LAW: a genuinely forward-looking recommendation or proposal may carry planned numbers only under an explicit contract. In JSON, put statement_type recommendation or proposal on that exact object and begin its visible string with the matching Recommendation:, Proposal: or Target: label; Phase N is also a proposal. Begin with one imperative action such as run, test, launch, set, or build, and keep it to that forward-looking clause. A qualitative inference may instead use statement_type inference plus a visible Inference: label, but it cannot introduce a number or URL. In Markdown, presenter notes, and slide text, begin the scope with the same visible label. A label never licenses a present or past factual assertion, external URL, or altered admitted claim. Never wrap an admitted claim with False, allegedly, reportedly, may, might, could, no longer, or any other polarity or modality. Every external URL must appear with its own exact admitted claim, never a different admitted row."

// Exact historical, unshipped Packaging Studio v5 definition. It remains only
// to authenticate and inspect persisted receipts. Unfinished v5 runs must
// relaunch under the current authority contract; these bytes are never an
// execution or migration path.
func studioIdentityJudgesV5() []ProcessPersona {
	return []ProcessPersona{
		{Name: "art_director", System: "You are an art director judging rival visual directions applied to the same 2-3 sample slides. Score each on palette, contrast, typography, spacing, image treatment, and graphic language — whether the system is distinctive, coherent, and suited to the actual material rather than a decorated default. Pick a winner and name the reusable tokens and rules it hands to the deck chassis."},
		{Name: "brand_strategist", System: "You are a brand strategist judging rival visual directions. Score each on whether the identity is born from this project's thesis, audience, source material, and any supplied brand assets rather than borrowed from a stock presentation style. Reward a direction an outsider would recognize as this project's own. Pick a winner."},
		{Name: "audience_proxy", System: "You represent the intended audience, judging rival visual directions on the same sample slides. Score each on whether it creates trust, attention, and the right emotional response for the actual decision. You are not a designer; react as the reader or room would. Pick a winner."},
	}
}

func packagingStudioDefinitionV5() ProcessDefinition {
	internal := true
	return ProcessDefinition{
		ID: packagingStudioProcessID, Version: 5, Title: "Packaging Studio",
		Description: "Turn a direct request and authorized company context into a researched-when-needed, reviewed, editable presentation.",
		Group:       toolGroupProcesses, Authority: toolAuthorityWorkspaceWrite,
		ImplementationRevision: "packaging_studio.runtime.v5",
		Budgets:                ProcessBudgets{MaxSubtasks: 16, MaxTokens: 56000, WallClock: 25 * time.Minute},
		Stages: []ProcessStage{
			{
				ID: "context_snapshot", Title: "Understand the brief", Role: processRoleSynthesizer, Internal: internal,
				PromptBody: strings.Join([]string{
					"Turn the direct approved request, exact reply-thread/source packet, and authorized Company Brain context into deck_context_snapshot_v3. The direct request is authoritative; older company context may support it but never override it.",
					"Resolve audience, decision, desired response, slide_count, known brand assets, likes/dislikes, exact language worth preserving, and constraints. Use a safe reversible inference instead of asking a routine question; label it. If the request states a slide count, copy it exactly.",
					"Choose research_mode as none, internal, or external. Use external only when current market facts, benchmarks, regulations, or credibility-critical numbers could materially change the story. Use the fewest decision-driving questions: one decisive lane is better than a broad scan. Do not ask hosted web research to reconstruct private account analytics, perform a multi-platform performance audit, or answer implementation diligence that can wait until after the recommendation; record those as an internal data need or deferred guardrail instead. Never invent a citation.",
					"When research_mode is external, research_questions must contain 1 to 3 atomic single-line objects and no other shape. Each object has exactly question, research_kind, source_ref, authority_quote, scope_anchor, decision_effect, and decision_relevance. research_kind is direct_evidence, comparative_evidence, or current_constraint. decision_effect is recommendation, scope, sequence, guardrail, or measurement. The question has exactly one question mark. Copy source_ref exactly as the full text inside one SOURCE [...] header, excluding only the literal brackets. Copy authority_quote exactly from that same source. scope_anchor is an exact 2 to 12 material-word phrase present in the direct ask, the authority_quote, the question, and decision_relevance; a company name or generic word such as market is insufficient. decision_relevance repeats that anchor and states concretely how the answer could change a recommendation, decision, pilot, sequence, scope, guardrail, or measurement in this deck. direct_evidence must preserve the authorized entity, population, measure, predicate, geography, and time window. comparative_evidence may introduce named comparators only when the question explicitly asks for a comparison or benchmark and stays within one measure lane. current_constraint may introduce a regulator or platform only to ask for current rules, policy, regulation, or requirements; it must not bundle market, spend, reach, or performance claims. When research_mode is none or internal, research_questions is an empty array.",
					"Return one JSON object with keys direct_ask, audience, decision, desired_response, slide_count, context_used, settled_decisions, taste_signals, brand_assets, research_mode, research_questions, known_facts, uncertain_claims, and reversible_inferences. known_facts must be an array of objects with claim, exact_quote, and source_ref. Copy claim and exact_quote verbatim from one authorized source and make them identical after whitespace normalization. Copy the complete bracketed reference exactly from the same SOURCE [...] block or source-linked Company Brain line into source_ref, including every id, revision, and digest field; never synthesize or combine a reference. If that same-source proof is unavailable, put the item in uncertain_claims instead.",
				}, "\n"), OutputContract: "deck_context_snapshot_v3",
			},
			{
				ID: "external_research", Title: "Verify the facts that matter", Role: processRoleWriter, Mode: "research", Internal: internal,
				InputFrom: []string{"context_snapshot"}, RunIf: &ProcessStageCondition{StageID: "context_snapshot", Field: "research_mode", Equals: "external"},
				PromptBody:     "Research only the credibility-critical questions authorized by the brief. Prefer primary and current sources. For every finding provide claim, source title, URL, publication date when known, units, confidence, and the exact implication for the deck. Separate source fact from inference; exclude anything not verified.",
				OutputContract: packagingStudioExternalEvidenceContract,
			},
			{
				ID: "source_snapshot", Title: "Capture exact source text", Role: processRoleCompile, Internal: internal,
				InputFrom: []string{"context_snapshot", "external_research"}, RunIf: &ProcessStageCondition{StageID: "context_snapshot", Field: "research_mode", Equals: "external"},
				PromptBody: "Fetch the exact provider-linked HTTPS sources server-side and preserve bounded, digest-bound text windows for each candidate claim. Fetch failures and pages with no relevant text remain explicit; URL membership alone never proves a claim.",
				Compile:    compileExternalEvidenceSourceSnapshots,
			},
			{
				ID: "evidence_entailment", Title: "Check what the sources actually prove", Role: processRoleWriter, Mode: "artifacts", Internal: internal,
				InputFrom: []string{"context_snapshot", "external_research", "source_snapshot"}, RunIf: &ProcessStageCondition{StageID: "context_snapshot", Field: "research_mode", Equals: "external"},
				PromptBody:     "Independently check every exact candidate fact + URL pair using only the exact authority-bound text windows the server fetched from that URL. Do not start a second search. Admit only claims those windows actually entail with matching population, date, units, and numeric fidelity. Reject unclear, contradicted, unfetched, or merely URL-associated claims; never repair a claim into a stronger one.",
				OutputContract: packagingStudioEntailmentContractV1,
			},
			{
				ID: "evidence", Title: "Lock the evidence", Role: processRoleCompile, Internal: internal,
				InputFrom:      []string{"context_snapshot", "evidence_entailment"},
				PromptBody:     "Produce evidence_dossier_v3 from authorized Company Brain/direct-source facts and the entailment-check record. Every deck_ready_claim carries claim, provenance, date when known, units, confidence, and exactly one status: entailment_checked for an external claim admitted by evidence_entailment, or internal for an attributable authorized company/direct-source fact. No verified, suggested, inferred, rejected, unclear, or merely provider-fetched claim may enter deck_ready_claims; keep all such material in excluded_claims or missing_proof.",
				OutputContract: "evidence_dossier_v3",
				Compile:        compileProcessEvidenceDossier,
			},
			{
				ID: "story_architects", Title: "Find the strongest story", Role: processRolePanel, Internal: internal,
				InputFrom: []string{"context_snapshot", "evidence"}, Personas: studioCompeteArchitects(),
				PromptBody:     "Develop genuinely different slide-by-slide arguments for the actual audience and decision, then synthesize the strongest one. Use only deck-ready evidence. Preserve explicit source language exactly. Score excitement, coherence, credibility, audience fit, and distinctiveness; select one causal story spine and graft only compatible best beats. Name any claim still needing proof. This is a story, not an outline of topics. For every JSON object that contains a material number, currency, percentage, date, external URL, or externally verifiable superlative, include sibling claim_ids and exact_claims arrays copied exactly from the admitted evidence row, and render the complete exact admitted claim verbatim in the fact-bearing string. If you write prose instead of JSON, append <!-- stride-claim:<claim id> | <exact admitted claim> --> in the same paragraph and render that exact claim verbatim in the factual sentence. " + processForwardStatementPromptLawV5,
				OutputContract: "story_spine_v2",
			},
			{
				ID: "write", Title: "Write the deck", Role: processRoleSynthesizer, Internal: internal,
				InputFrom: []string{"context_snapshot", "evidence", "story_architects"},
				PromptBody: strings.Join([]string{
					"Write the final deck_copy_v3 as exactly one JSON object with a slides array containing exactly the slide_count in the brief; when the direct request omitted a count, choose the shortest count that tells the story completely and record the inference.",
					"For each slide provide slide_id, purpose, headline, optional kicker, visible copy, evidence/source label, speaker intent, transition, and presenter_note. One claim per slide; use only deck_ready_claims.",
					"Keep presenter_note proportional to the slide's speaking job: use a natural 10-45 second note when it adds context, a brief transition note or an empty string when it does not, and [BEAT] only when a deliberate pause materially improves delivery. Never add filler to satisfy a duration or marker. The note owns parables and emotional turns; the slide owns numbers. Never speak a figure absent from its slide. A note that speaks a material claim must render the complete admitted claim verbatim and carry the same claim id as its slide.",
					"Every slide object containing a material number, currency, percentage, date, external URL, or externally verifiable superlative must carry sibling claim_ids and exact_claims arrays copied exactly from the admitted evidence row. Render the complete exact admitted claim verbatim in the fact-bearing visible string; never place claim metadata itself in visible copy.",
					processForwardStatementPromptLawV5,
					"Write in a specific human spoken register. Remove AI tells: throat-clearing, generic superlatives, slogan stacks, symmetrical filler, 'not just X but Y', empty abstraction, and invented quotes. No em dashes in client-facing copy. Keep normal slides under 45 visible words.",
					studioSourceLanguageLaw,
				}, "\n"), OutputContract: "deck_copy_v3",
			},
			{
				ID: "gate", Title: "Stress-test the story and copy", Role: processRoleGate, Internal: internal,
				InputFrom:  []string{"write", "context_snapshot", "evidence", "story_architects"},
				PromptBody: "Score Audience decision fit, Story causality, Evidence integrity, Human voice, Slide-count fidelity, and Source-language fidelity. Every weak score must name an executable repair. Do not accept unverified numbers, generic AI cadence, or a structurally correct outline that lacks a persuasive turn. " + studioSourceLanguageLaw,
				GateSpec: &ProcessGateSpec{Threshold: 9, Floor: 7, MaxRounds: 2, RepairTarget: "write", HoldOnFailure: true, Dimensions: []string{
					"Audience decision fit", "Story causality", "Evidence integrity", "Human voice", "Slide-count fidelity", "Source-language fidelity",
				}},
			},
			{
				ID: "identity", Title: "Create the visual identity", Role: processRoleJudges, Internal: internal,
				InputFrom: []string{"context_snapshot", "write", "gate", "story_architects"}, Personas: studioIdentityJudgesV5(),
				PromptBody: strings.Join([]string{
					"Now that story and copy are locked, develop the visual system around their actual emotional arc. Extend supplied brand assets when present; otherwise audition 2-3 distinctive systems on the same cover, evidence, and image-led slides. Choose one and define palette, type, spacing, grid, graphic motif, image treatment, data-viz treatment, and refusals. The cover is one powerful idea with one focal hierarchy, not a subtitle pile or generic AI gradient.",
					"Direct imagery inside that same chosen system only where it performs an emotional or explanatory job that type and evidence cannot. Zero images is a valid deliberately typographic deck; default to one to three purposeful images, use no more than four, and reserve at most one full-bleed crescendo. Ledger and number slides carry none.",
					"Return exactly one fenced JSON object and no prose. The object has exactly strategy, visual_system, identity, and shots. strategy and visual_system are non-empty strings. identity has exactly palette, type, spacing, grid, graphic_motif, image_treatment, data_viz_treatment, and refusals, each a non-empty string.",
					"shots is an array of zero to four objects; an intentional typographic direction is represented only by a valid empty shots array. Every shot has exactly fig, slide_id, slot, subject, composition, temperature, treatment, aspect, caption, place, and why. fig is a unique positive integer. slide_id exactly matches a slide_id in deck_copy_v3. slot is bleed or plate; aspect is landscape, portrait, or square. subject, composition, temperature, treatment, caption, and why are non-empty strings; place is a string and may be empty. Use at most one bleed shot. Use natural color and honest geography; reserve negative space for copy.",
				}, "\n"), OutputContract: "identity_direction_v3",
			},
			{
				ID: "imagery_generate", Title: "Generate selected imagery", Role: processRoleCompile, Internal: internal,
				InputFrom: []string{"identity"}, PromptBody: "Generate only the shots selected inside the locked identity direction. Per-shot provider failure is disclosed and skipped; zero shots remains a deliberate typographic deck.", Compile: compilePackagingStudioImagery,
			},
			{
				ID: "layout_plan", Title: "Compose every slide", Role: processRoleWriter, Mode: "artifacts", Internal: internal,
				InputFrom: []string{"identity", "write", "evidence", "imagery_generate"},
				PromptBody: strings.Join([]string{
					"Create layout_plan_v3 after copy and identity are locked. For every slide specify a 1920x1080 scene with composition type, background, grid, and element ids/types/x/y/width/height/z/typography/alignment/opacity. Tie imagery to crop, focal point, and copy-safe space.",
					studioCompositionRhythmLaw,
					"Use a radically simple cover, legible evidence furniture, and no overflow or accidental collision. Return structured JSON; do not rewrite copy. Preserve each fact-bearing string, visible forward label, statement_type, claim_ids, and exact_claims exactly from deck copy on that same text-element object; geometry numbers are structural and need no evidence annotation. " + processForwardStatementPromptLawV5,
				}, "\n"),
				OutputContract: "layout_plan_v3",
			},
			{
				ID: "ship_deck", Title: "Build the editable presentation", Role: processRoleWriter, Mode: "artifacts", Internal: internal,
				InputFrom:  []string{"write", "evidence", "identity", "imagery_generate", "layout_plan"},
				PromptBody: packagingStudioDeckWriterPromptV5(), OutputContract: packagingStudioDeckContract,
			},
			{
				ID: "draft_compile", Title: "Render the draft for review", Role: processRoleCompile, Internal: internal,
				InputFrom: []string{"ship_deck"}, PromptBody: "File and render the candidate deck internally so the visual critic sees the actual pages before delivery.", Compile: compilePackagingStudioDraft,
			},
			{
				ID: "slide_jury", Title: "Review every rendered slide", Role: processRoleCompile, Internal: internal,
				InputFrom: []string{"draft_compile"}, PromptBody: "Review all rendered pages before delivery. Return executable fixes and a machine verdict. A provider or render failure is needs_attention, never a silent pass.", Compile: compilePackagingStudioSlideJury,
			},
			{
				ID: "quality_gate", Title: "Hold or repair the presentation", Role: processRoleGate, Internal: internal,
				InputFrom:  []string{"ship_deck", "slide_jury"},
				PromptBody: "Score Render completeness, Text fit, Hierarchy, Layout craft, Brand coherence, Image purpose, Copy fidelity, and Presentation-distance legibility. Use the jury's page-level findings. Pass only when the actual rendered deck is ready; otherwise return executable repairs for ship_deck.",
				GateSpec: &ProcessGateSpec{Threshold: 9, Floor: 7, MaxRounds: 2, RepairTarget: "ship_deck", HoldOnFailure: true, Dimensions: []string{
					"Render completeness", "Text fit", "Hierarchy", "Layout craft", "Brand coherence", "Image purpose", "Copy fidelity", "Presentation-distance legibility",
				}},
			},
			{
				ID: "ship_compile", Title: "Presentation ready", Role: processRoleCompile,
				InputFrom: []string{"ship_deck", "quality_gate"}, PromptBody: "File the reviewed editable deck as the only default channel deliverable and enqueue its PDF render when available.", Compile: compilePackagingStudioShip,
			},
		},
	}
}

// packagingStudioDeckWriterPrompt is kept separate so the active definition is
// readable while the strict native-editor and print contracts remain exact.
func packagingStudioDeckWriterPromptV5() string {
	return strings.Join([]string{
		"Produce one complete self-contained HTML document beginning <!doctype html>, with all CSS inline and no external references except data: URIs used for embedded imagery. Use the locked copy and layout plan exactly; do not rewrite during rendering.",
		"Include this exact chassis style in <head>: <style>\n" + strings.TrimSpace(packagingDeckChassisCSS) + "\n</style>. Put every <section class=\"pg\"> inside one <div id=\"stage\">, add class on to the first slide, and give every section data-deck-slide=\"the exact deck_copy_v3 slide_id\".",
		"Every meaningful text, image, and shape needs a stable data-deck-element and data-deck-type plus inline absolute left/top/width/height/z-index/opacity/rotation in 1920x1080 coordinates. Text also needs inline family, size, weight, line-height, tracking, alignment, and color. Shapes need fill/stroke; images need object-fit. No overflow, clipping, off-canvas elements, or accidental intersections; mark only intentional overlap data-deck-overlap=\"allow\".",
		"Keep authored text inside the 96px safe zone. The only exception is non-copy typographic furniture deliberately reaching an edge: mark it data-deck-furniture=\"background\" (or \"full-bleed\") AND aria-hidden=\"true\". That marker never excuses off-canvas geometry, empty text, copy drift, or accidental collisions.",
		"Use the identity tokens and a 12-column grid to create varied, presentation-distance compositions, not a document in the upper-left. Keep a minimum 96px safe zone. " + studioCompositionRhythmLaw + " Keep one claim and no more than 45 client-facing words on a normal slide. Make metrics large, comparisons aligned, evidence sourced, and the cover radically simple.",
		"FULL-BLEED LAW: for generated imagery, create only matching native-importable <figure class=\"image-plate fig-N\"> plates with a div.ph; never invent image URLs. For a directed full bleed, add class \"bleed\" and use left:0;top:0;width:1920px;height:1080px with a purposeful scrim behind copy. If imagery was skipped, produce a deliberately typographic deck.",
		"Put each non-empty matching presenter_note from the locked deck copy in <div class=\"notes\" hidden>. Preserve [BEAT] only when the locked note uses it; do not invent a pause marker. Do not add custom JavaScript or presenter chrome; the native presenter owns navigation and presentation.",
		"CLAIM-AUTHORITY LAW: do not add or paraphrase factual copy. For every slide carrying a material number, currency, percentage, date, external URL, or externally verifiable superlative, render the complete exact admitted claim verbatim in its fact-bearing text element and place <!-- stride-claim:<claim id> | <exact admitted claim> --> inside that same <section class=\"pg\">. Preserve the marker in presenter notes too when they speak the fact. Page counters and scene geometry are structural, not claims.",
		processForwardStatementPromptLawV5,
		"Do not generate visible slide copy with CSS content or pseudo-elements; every visible word must live in an inspectable data-deck-type=\"text\" element or presenter note.",
		studioSourceLanguageLaw,
	}, "\n")
}
