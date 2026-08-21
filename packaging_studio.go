package main

// packaging_studio.go — the flagship ProcessDefinition (packaging OS §3
// "Porting /packaging" Phase 2, Wave 4 item 18). It authors ONE opinionated
// pipeline on the process-def runtime (process_definitions.go / goal_engine.go)
// — the moat is the pipeline, not a platform ("What we are explicitly NOT
// doing"). Every stage maps onto an ENGINE role: human_checkpoint (the four
// judgment touchpoints: intake, compete_choice, founder_pass, ship approval),
// panel/judges (red-team + rival competitions), synthesizer/writer (the
// deliverables), gate (the closed-loop re-review), and compile (the
// five-artifact SHIP assembler, which owns the flatten-law render enqueues).
// Nothing here reaches into the engine; it composes the runtime's vocabulary.
//
// The phases (spec §3 "Where humans sit" + item 18):
//   1. INTAKE       human_checkpoint — sources / the founder's verbatim words
//                   (LAW downstream) / the real audience, and whether brand
//                   assets exist (the branch IDENTITY reads).
//   2. RED-TEAM     panel — growth VC, family office/LP, veteran operator, a
//                   domain insider with teeth, + the house judge seat when the
//                   distiller has written one → an objection ledger with a
//                   contractual strengths_to_keep.
//   3. IDENTITY     judges — the design-identity gap: when INTAKE declares no
//                   brand assets, 2-3 rival visual directions on the same sample
//                   slides, judged, winner's tokens feed WRITE/SHIP; when assets
//                   exist, the stage discloses a skip. (Always present; the
//                   branch is behavioural, since the runtime does not skip
//                   stages.)
//   4. COMPETE      panel of 3 rival narrative architects (cultural-moment /
//                   franchise-playbook / founder-conviction) → judges of 3
//                   scoring excitement/coherence/credibility/distinctiveness
//                   with MANDATORY best_beats_to_steal → the choice card
//                   (human overrules before WRITE spends tokens).
//   5. WRITE        synthesizer — the winning spine + grafted steals + the
//                   strengths_to_keep contract; the copy law (no em dashes
//                   client-facing) is enforced by the engine's own law sweep.
//   6. GATE         gate — the personas' round-1 objections in hand (InputFrom
//                   red_team): threshold 9.0, floor 7.0, 2 rounds, force-accept
//                   disclosed. A revise re-queues WRITE with the unanswered
//                   objections as notes — the grill loop generalized.
//   7. VOICE        writer — the speechwriter: a 25-45s per-page script with one
//                   [BEAT] each, the founder's verbatim phrases woven in, the
//                   interlock rule (voice owns parables, slides own numbers).
//   8. FOUNDER PASS human_checkpoint (touchpoint 3) — the gated draft + "mark
//                   do_not_touch", the highest-leverage taste moment; the
//                   do_not_touch lines ride the decision artifact into SHIP.
//   9. SHIP         writer + compile — ship_deck writes the self-contained
//                   html_deck (presenter mode embedded from VOICE), then the
//                   ship_compile stage runs fileStudioShipDeliverables: the
//                   five interlocking artifacts (deck html_deck + The Wall +
//                   The Talk with paperKit=true + rigor companion + findings
//                   record aggregated from the run's ACTUAL stage verdicts),
//                   all attached to the venture package, with the deck + Talk
//                   render enqueues (or their disclosed skips).
//  9b. SLIDE JURY   compile (Wave 5 item 21) — once the deck's PDF export has
//                   completed and the render-runner's page JPEGs are on the
//                   deck as {kind: image} assets, the vision jury trio SEES
//                   the rendered pages and files a slide_jury_v1 scoreboard;
//                   its findings land as revision notes on the findings
//                   record (advisory — the founder decides, never an
//                   auto-revise). Sidecar absent / keyless / export timed
//                   out → a disclosed skip, and the ship proceeds.
//  10. SHIP APPROVAL human_checkpoint (touchpoint 4) — with the five artifacts
//                   filed, the goal parks on the approval surface for the
//                   explicit ship decision; nothing leaves the building
//                   without it.

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const packagingStudioProcessID = "packaging_studio"

// The studio's output contracts. The deck is the process deliverable contract
// (processDeliverableContract picks the LAST writer stage's contract → ship_deck).
const (
	packagingStudioDeckContract             = "packaging_deck_v1"
	packagingStudioImageryDirectionContract = "imagery_direction_v1"
	packagingStudioWallContract             = "packaging_wall_v1"
	packagingStudioTalkContract             = "packaging_talk_v1"
	packagingStudioRigorContract            = "packaging_rigor_v1"
	packagingStudioFindingsContract         = "packaging_findings_v1"
)

// studioSourceLanguageLaw keeps quoted source language faithful downstream
// without turning an internal production rule into customer-facing jargon.
const studioSourceLanguageLaw = "Language explicitly quoted in the approved intake brief or goal objective is fixed source material: preserve its exact wording when used, never attribute a paraphrase as a quote, and do not contradict it."

// packagingDeckChassisCSS is the INVARIANT deck chassis (stage geometry, the
// .pg slide model, and — critically — the @page + @media print pagination).
// ship_deck is required to embed it verbatim so the exported PDF contains EVERY
// slide, not just the single on-screen frame; the render runner re-injects
// packagingDeckPrintCSS() as a safety net when an authored deck drops it. This
// is the single source of truth for the print contract.
//
//go:embed packaging_deck_chassis.css
var packagingDeckChassisCSS string

// packagingDeckPrintCSS is the pagination-only tail of the chassis (from the
// @page rule to the end). The render sidecar injects just this block into any
// deck HTML that lacks an @page rule, so a deck still paginates even if the
// writer dropped the print CSS. Deriving it from the chassis keeps them in
// lockstep — there is no second copy to drift.
func packagingDeckPrintCSS() string {
	// Match the rule "@page{", not the word "@page" in the chassis comment.
	if idx := strings.Index(packagingDeckChassisCSS, "@page{"); idx >= 0 {
		return strings.TrimSpace(packagingDeckChassisCSS[idx:])
	}
	return strings.TrimSpace(packagingDeckChassisCSS)
}

// --- Personas ---------------------------------------------------------------

// studioRedTeamPersonas is the RED-TEAM quartet with explicit teeth, plus the
// house judge seat when the distiller has written a living house_style
// (houseJudgePersona, the same seam grill.go's red-team panel uses). Absent a
// house_style — every deploy until the distiller first runs, and every keyless
// deploy — the quartet stands alone: no extra seat, no behaviour change.
func studioRedTeamPersonas() []ProcessPersona {
	base := []ProcessPersona{
		{
			Name:   "growth_vc",
			System: "You are a growth-stage VC on Bonfire's red-team panel. You have teeth: name the market-size hand-wave, the unproven wedge, the competitor who already owns this, and the metric that would have to be true for the round to clear. Attack the money slide and the why-now. Never a generic cliché — every objection ties to a specific claim in the material.",
		},
		{
			Name:   "family_office_lp",
			System: "You are a family-office / LP allocator on Bonfire's red-team panel. You have teeth: name the downside case, the illiquidity, the key-person risk, and the line where the founder is selling a dream instead of pricing a risk. You ask what a loss looks like and whether the terms protect you. Every objection ties to a specific claim.",
		},
		{
			Name:   "veteran_operator",
			System: "You are a veteran operator who has actually shipped in this category on Bonfire's red-team panel. You have teeth: name the execution lie, the timeline that never survives contact, the org the plan silently assumes, and the thing that is hard that the deck treats as easy. Every objection ties to a specific claim.",
		},
		{
			Name:   "domain_insider",
			System: "You are a domain insider — you know how this specific industry actually clears deals — on Bonfire's red-team panel. You have teeth: name the gatekeeper the plan ignores, the rights/relationship/regulatory reality it waves past, and the insider objection an outsider would never see. Every objection ties to a specific claim.",
		},
	}
	if seat, ok := studioHouseJudgeSeat(); ok {
		base = append(base, ProcessPersona(seat))
	}
	return base
}

// studioIdentityJudges is the IDENTITY design panel: three judges scoring rival
// visual directions on the same sample slides (the design-identity gap).
func studioIdentityJudges() []ProcessPersona {
	return []ProcessPersona{
		{Name: "art_director", System: "You are an art director judging rival visual directions applied to the same 2-3 sample slides. Score each on token set (color, the --heat dial), type pairing, and duotone treatment — whether the system is distinctive and coherent, not a recolored default. Pick a winner and say which tokens it hands to the deck chassis."},
		{Name: "brand_strategist", System: "You are a brand strategist judging rival visual directions. Score each on whether the identity is BORN from this project's thesis and audience, not borrowed. Reward a direction that an outsider would recognize as this venture's own. Pick a winner."},
		{Name: "audience_proxy", System: "You are the real audience this venture is selling to, judging rival visual directions on the same sample slides. Score each on whether it makes YOU lean in or bounce. You are not a designer; you react. Pick a winner."},
	}
}

// studioCompeteArchitects is the trio of rival narrative architects — three
// genuinely different spines, not three phrasings of one.
func studioCompeteArchitects() []ProcessPersona {
	return []ProcessPersona{
		{Name: "cultural_moment", System: "You are a narrative architect building the spine around the CULTURAL MOMENT: why the world is ready for this now, what shift makes it inevitable. Write a complete, distinctive narrative spine (the slide-by-slide argument). Preserve approved source language exactly when quoted. Make it genuinely different from a franchise or leadership-conviction angle."},
		{Name: "franchise_playbook", System: "You are a narrative architect building the spine around the FRANCHISE PLAYBOOK: the durable, expandable machine — the universe, the flywheel, the second and third act the first success unlocks. Write a complete, distinctive narrative spine. Preserve approved source language exactly when quoted."},
		{Name: "founder_conviction", System: "You are a narrative architect building the spine around LEADERSHIP CONVICTION: the earned insight, the why-this-team, the thing the team sees that others do not. Write a complete, distinctive narrative spine. Preserve approved source language exactly when quoted."},
	}
}

// studioCompeteJudges scores the rival spines and, mandatorily, names the best
// beats to steal from the losers. It gains the house judge seat too, so the
// office's distilled taste weighs the narrative competition. The synthesis
// closes with a JSON array of the angle names so the COMPETE checkpoint reads
// its options from this stage (OptionsFrom).
func studioCompeteJudges() []ProcessPersona {
	base := []ProcessPersona{
		{Name: "excitement_judge", System: "You judge the rival narrative spines on EXCITEMENT and DISTINCTIVENESS: which one makes a reader lean forward, which is unmistakably this venture and not a template. Score every spine 0-10 on excitement and distinctiveness. MANDATORY: name the single best beat to STEAL from each spine you did not pick."},
		{Name: "coherence_judge", System: "You judge the rival narrative spines on COHERENCE: which argument holds from problem to ask with no gap a skeptic drives a truck through. Score every spine 0-10 on coherence. MANDATORY: name the single best beat to STEAL from each spine you did not pick."},
		{Name: "credibility_judge", System: "You judge the rival narrative spines on CREDIBILITY: which one a diligent investor believes, which claims are load-bearing and earned. Score every spine 0-10 on credibility. MANDATORY: name the single best beat to STEAL from each spine you did not pick."},
	}
	if seat, ok := studioHouseJudgeSeat(); ok {
		base = append(base, ProcessPersona(seat))
	}
	return base
}

// studioHouseJudgeSeat resolves the optional house judge seat from the global
// app (the seam grill.go and any judges-role stage share). It is a persona, not
// a tool, so it degrades exactly like houseJudgePersona: no seat until the
// House-Style Distiller has written the office's living house_style.
func studioHouseJudgeSeat() (ProcessPersona, bool) {
	if kanbanApp == nil {
		return ProcessPersona{}, false
	}
	seat, ok := kanbanApp.houseJudgePersona()
	if !ok {
		return ProcessPersona{}, false
	}
	return ProcessPersona{Name: seat.Name, System: seat.System}, true
}

// --- The definition ---------------------------------------------------------

// packagingStudioDefinition builds the flagship pipeline. It is constructed
// fresh on every processDefinitions() call (the builtin pattern), so the
// conditional house judge seats reflect the CURRENT house_style — a definition
// listed before the distiller runs carries the base panels; one listed after
// carries the house seat. The stage bodies splice studioSourceLanguageLaw so
// approved source language stays exact downstream, and the
// InputFrom chains carry the INTAKE brief forward so the gate re-reads them.
func packagingStudioDefinition() ProcessDefinition {
	return ProcessDefinition{
		ID:          packagingStudioProcessID,
		Version:     2,
		Title:       "Packaging Studio",
		Description: "Turn source material into a reviewed, presenter-ready deck with a clear story, a coherent visual system, editable imagery, presenter notes, and customer checkpoints before delivery.",
		Group:       toolGroupProcesses,
		Authority:   toolAuthorityWorkspaceWrite,
		// V2 adds context, evidence, copy, and layout decisions while keeping the
		// internal work out of the channel feed.
		Budgets: ProcessBudgets{MaxSubtasks: 20, MaxTokens: 64000, WallClock: 25 * time.Minute},
		Stages: []ProcessStage{
			{
				ID:    "intake",
				Title: "Intake — source, audience, and visual direction",
				Role:  processRoleHumanCheckpoint,
				CheckpointSpec: &ProcessCheckpointSpec{
					Question: "Should Scout use existing brand assets for this presentation, or develop the visual direction from the source material and company context?",
					Options: []ProcessCheckpointOption{
						{Label: "brand assets provided"},
						{Label: "no brand assets — develop identity"},
					},
				},
			},
			{
				ID:        "context_snapshot",
				Title:     "Understand the request and company context",
				Role:      processRoleSynthesizer,
				InputFrom: []string{"intake"},
				PromptBody: strings.Join([]string{
					"Build deck_context_snapshot_v1 from the approved request, exact reply-thread/source packet, settled project decisions, and Company Brain context. The direct ask is authoritative.",
					"Identify audience, decision the deck must unlock, desired response, known brand assets, explicit likes/dislikes, exact source language worth preserving, constraints, and unresolved facts. Do not ask a human when a safe reversible inference is available; label the inference.",
					"Choose research_mode as none, internal, or external. External is warranted only when current facts, market claims, benchmarks, or credibility-critical numbers materially change the recommendation. Never invent a citation.",
					"Return concise structured JSON with keys direct_ask, audience, decision, desired_response, context_used, settled_decisions, taste_signals, brand_assets, research_mode, research_questions, known_facts, uncertain_claims, and reversible_inferences.",
				}, "\n"),
				OutputContract: "deck_context_snapshot_v1",
			},
			{
				ID:        "evidence",
				Title:     "Build the evidence dossier",
				Role:      processRoleWriter,
				Mode:      "research",
				InputFrom: []string{"intake", "context_snapshot"},
				PromptBody: strings.Join([]string{
					"Follow the context snapshot's research decision. If mode is none, ground the deck only in approved source material. If internal, use exact source and Company Brain facts. If external, investigate only the listed credibility-critical questions.",
					"Produce evidence_dossier_v2. Every factual claim carries claim, source title, source URL or source artifact id, date when known, units, confidence, and status verified|internal|suggested. Suggested facts are never deck-ready and must not be rendered as truth.",
					"Prefer primary and current sources when recency matters. Separate source fact from inference. End with deck_ready_claims and excluded_claims.",
				}, "\n"),
				OutputContract: "evidence_dossier_v2",
			},
			{
				ID:        "red_team",
				Title:     "Stress-test the brief",
				Role:      processRolePanel,
				InputFrom: []string{"intake", "context_snapshot", "evidence"},
				Personas:  studioRedTeamPersonas(),
				PromptBody: strings.Join([]string{
					"Attack the venture as the skeptical room it will actually face. " + studioSourceLanguageLaw,
					"Produce an objection ledger: the objections that would sink the meeting, each tied to a SPECIFIC weakness — generic clichés fail.",
					"CONTRACTUAL: name strengths_to_keep — what already works and must survive every downstream revision. The synthesis carries both the objections and the strengths_to_keep list forward.",
				}, "\n"),
				OutputContract: "objection_ledger_v1",
			},
			{
				ID:        "identity",
				Title:     "Build the visual system",
				Role:      processRoleJudges,
				InputFrom: []string{"intake", "context_snapshot", "evidence", "red_team"},
				Personas:  studioIdentityJudges(),
				PromptBody: strings.Join([]string{
					"Read the context snapshot. When exact brand assets are present, respect them and extend only what is missing. Otherwise develop a distinctive identity born from the thesis, audience, company taste, and subject matter.",
					"When INTAKE says 'brand assets provided', skip invention and record the extension rules. Otherwise audition 2-3 rival systems on the SAME sample slides: cover, evidence slide, and image-led slide. Define palette tokens, type pairing, spacing rhythm, graphic motif, image treatment, data-viz treatment, and what this system refuses to do.",
					"Pick one winner. The cover must be powerful and simple: one idea, one focal hierarchy, no subtitle pile, no generic gradient-orb AI aesthetic. State the winning tokens explicitly for layout and rendering.",
					studioSourceLanguageLaw,
				}, "\n"),
				OutputContract: "identity_direction_v1",
			},
			{
				ID:        "compete_architects",
				Title:     "Explore narrative directions",
				Role:      processRolePanel,
				InputFrom: []string{"intake", "evidence", "red_team"},
				Personas:  studioCompeteArchitects(),
				PromptBody: strings.Join([]string{
					"Each architect writes a COMPLETE, genuinely distinct narrative spine (the slide-by-slide argument) from their assigned angle. " + studioSourceLanguageLaw,
					"Respect the red_team's strengths_to_keep; do not re-introduce a sunk objection. The synthesis presents all three spines side by side for judging.",
				}, "\n"),
				OutputContract: "narrative_spines_v1",
			},
			{
				ID:        "compete_judges",
				Title:     "Choose the strongest story",
				Role:      processRoleJudges,
				InputFrom: []string{"compete_architects"},
				Personas:  studioCompeteJudges(),
				PromptBody: strings.Join([]string{
					"Score every rival spine 0-10 on excitement, coherence, credibility, and distinctiveness. MANDATORY: best_beats_to_steal — the single strongest beat to graft from each spine that did not win.",
					"The synthesis declares the WINNER, the per-judge scores, and the beats to steal.",
					"END the synthesis with a JSON array on its own line naming the three angles exactly, e.g. [\"cultural-moment\", \"franchise-playbook\", \"founder-conviction\"], so the human can overrule the winner at the choice card.",
				}, "\n"),
				OutputContract: "compete_verdict_v1",
			},
			{
				ID:        "compete_choice",
				Title:     "Narrative direction",
				Role:      processRoleHumanCheckpoint,
				InputFrom: []string{"compete_judges"},
				CheckpointSpec: &ProcessCheckpointSpec{
					Question:    "Scout evaluated three narrative directions. Which one should shape the deck?",
					OptionsFrom: "compete_judges",
				},
			},
			{
				ID:        "write",
				Title:     "Build the 10-slide story",
				Role:      processRoleSynthesizer,
				InputFrom: []string{"intake", "evidence", "red_team", "identity", "compete_architects", "compete_judges", "compete_choice"},
				PromptBody: strings.Join([]string{
					"Write structured deck_copy_v2: exactly one claim per slide with slide_id, purpose, headline, optional kicker, body, evidence, source label, speaker intent, and transition. Use only deck_ready_claims from the evidence dossier.",
					"Use the chosen spine as the backbone and graft the judges' best beats while preserving strengths_to_keep. Write in a human spoken register. Remove AI tells: no throat-clearing, generic superlatives, slogan stacks, symmetrical filler, 'not just X but Y', or invented quotes. NO em dashes in client-facing copy. Keep normal slides under 45 visible words.",
					studioSourceLanguageLaw,
				}, "\n"),
				OutputContract: "deck_copy_v2",
			},
			{
				ID:        "gate",
				Title:     "Check the story against the brief",
				Role:      processRoleGate,
				InputFrom: []string{"write", "red_team"},
				PromptBody: strings.Join([]string{
					"Score the deck copy against the RED-TEAM's round-1 objection ledger (red_team), the closed loop generalized.",
					"Rubric dimensions: Objections answered (each round-1 objection is verifiably addressed, not ignored), Strengths kept (every strengths_to_keep entry survives), Spine integrity (the chosen angle and grafted steals cohere), Copy law (spoken register, no em dashes, no unearned hype).",
					"A dimension scores low when its objections remain open. " + studioSourceLanguageLaw,
				}, "\n"),
				// The SKILL semantics: 9.0 threshold, 7.0 floor, 2 rounds,
				// force-accept below threshold ships with the gaps DISCLOSED (always
				// a human's call downstream), never blocks silently.
				GateSpec: &ProcessGateSpec{Threshold: 9.0, Floor: 7.0, MaxRounds: 2, ForceAccept: true},
			},
			{
				ID:        "voice",
				Title:     "Add presenter notes",
				Role:      processRoleWriter,
				Mode:      "artifacts",
				InputFrom: []string{"write", "gate"},
				PromptBody: strings.Join([]string{
					"Write the presenter script: for EACH deck page, a 25-45 second spoken script with exactly one [BEAT] marking the pause.",
					"Use approved source language naturally in the spoken lines when it strengthens the story. " + studioSourceLanguageLaw,
					"INTERLOCK RULE: the VOICE owns the parables and the emotional turns; the SLIDE owns the numbers. Never put a figure in the script that is not on its slide, and never make the slide carry a story the voice should tell.",
				}, "\n"),
				OutputContract: "presenter_script_v1",
			},
			{
				ID:        "founder_pass",
				Title:     "Final content review",
				Role:      processRoleHumanCheckpoint,
				InputFrom: []string{"write", "voice", "gate"},
				CheckpointSpec: &ProcessCheckpointSpec{
					Question: "The story and presenter notes are ready. Approve this direction, or send it back with the specific lines or ideas Scout must preserve exactly.",
					Options: []ProcessCheckpointOption{
						{Label: "ship as-is"},
						{Label: "send back for changes", Action: processCheckpointActionRevise, Target: "write"},
					},
				},
			},
			{
				ID:             "copy_edit",
				Title:          "Make the copy sound human",
				Role:           processRoleSynthesizer,
				InputFrom:      []string{"write", "voice", "gate", "evidence", "founder_pass"},
				PromptBody:     "Run the human-ear copy pass on every visible line and presenter transition. Preserve exact approved source language, remove AI cadence and empty abstraction, tighten headlines until each can be said aloud naturally, verify every number against the evidence dossier, and keep the narrative turn intact. Output the final locked deck_copy_v2, not commentary about editing it.",
				OutputContract: "deck_copy_v2",
			},
			{
				// The ART DIRECTOR. Reads the chosen narrative page-by-page + the
				// identity visual system and decides the imagery STRATEGY: which
				// beats earn an image and where absence is stronger. Imagery is
				// EDITORIAL — zero images is a legitimate output (a deliberately
				// typographic package). It directs; it does NOT generate (that is
				// the next compile step) and does NOT embed bytes (ship_compile
				// inlines them). Output is a machine-readable shot list.
				ID:        "imagery_direction",
				Title:     "Plan the visual beats",
				Role:      processRoleWriter,
				Mode:      "artifacts",
				InputFrom: []string{"identity", "write", "copy_edit", "voice", "founder_pass"},
				PromptBody: strings.Join([]string{
					"You are the ART DIRECTOR for this packaging deck. You decide WHERE a photographic image earns an emotional beat that drives consensus / talent / capital, and WHERE its absence is stronger. You direct imagery; you do NOT generate it and you do NOT write the deck.",
					"Read the chosen narrative (WRITE + VOICE) page by page and the IDENTITY visual system. Imagery is EDITORIAL, never decoration: an image must do a job type and numbers cannot. If the story is carried by type and evidence, direct FEWER images or NONE — a deliberately typographic package is a valid, strong output.",
					"Honor the deck chassis laws VERBATIM: at most ~5 full-bleeds in the whole deck; at most 6 images total; EXACTLY ONE crescendo image at the deck's peak (its treatment note names it, the deck renders it at --heat:.45); ledger / numbers ('bone') pages carry NO imagery; one FIG. per photo plate. The duotone/heat treatment is applied later in the deck CSS, so describe each shot in NATURAL color and real subjects — never a brand-color wash, never invented geography.",
					"Name each shot's emotional temperature explicitly (drama, joy, awe, resolve, ...). When the PLACE is the claim, name the real place.",
					"Output EXACTLY ONE fenced ```json block and NOTHING else, of this shape:",
					"```json\n{\n  \"strategy\": \"one paragraph: where images earn a beat and where absence is stronger\",\n  \"visual_system\": \"the ONE visual-system brief, tied to the identity tokens, that rides every shot\",\n  \"shots\": [\n    { \"fig\": 1, \"slide_id\": \"the exact deck_copy_v2 slide_id\", \"slot\": \"bleed|plate\", \"subject\": \"what the image depicts (natural color, honest geography)\", \"composition\": \"framing, eyeline, scale, focal point, and negative space reserved for copy\", \"temperature\": \"the NAMED emotional temperature\", \"treatment\": \"how it ties to the visual system; say if THIS is the one crescendo\", \"aspect\": \"landscape|portrait|square\", \"caption\": \"the FIG. caption line\", \"place\": \"real place by name when the place is the claim, else empty\", \"why\": \"the emotional job this image does\" }\n  ]\n}\n```",
					"For a typographic package return \"shots\": []. Every shot MUST carry a non-empty subject and temperature or it will be dropped.",
				}, "\n"),
				OutputContract: packagingStudioImageryDirectionContract,
			},
			{
				// Authored-Go generation compile (mirrors slide_jury / ship_compile):
				// reads the director's shot list and fulfills each brief via the
				// existing gpt-image generator. Per-shot failure (keyless / quota /
				// timeout) is DISCLOSED and skipped; zero generated images is a
				// valid, non-fatal outcome. It never blocks the ship.
				ID:         "imagery_generate",
				Title:      "Generate the selected imagery",
				Role:       processRoleCompile,
				InputFrom:  []string{"imagery_direction"},
				PromptBody: "Deterministic generation step: read the imagery_direction shot list and generate each directed shot on the one visual system via the OpenAI image API, filing the results as {kind:image} assets. Per-shot failure is disclosed and skipped; keyless or zero shots ships the package typographic. Authored Go — never a model call.",
				Compile:    compilePackagingStudioImagery,
			},
			{
				ID:        "layout_plan",
				Title:     "Compose every slide",
				Role:      processRoleWriter,
				Mode:      "artifacts",
				InputFrom: []string{"identity", "copy_edit", "voice", "evidence", "imagery_direction", "imagery_generate"},
				PromptBody: strings.Join([]string{
					"Create layout_plan_v2 only after copy is locked. For every slide_id select the composition best suited to its job: cover, section break, statement, evidence, comparison, numbers, timeline, quote, image-led, or close.",
					"Specify a 1920x1080 scene: background, grid columns, element ids, element types, x/y/width/height/z, typography token, alignment, opacity, and intentional overlap. Tie each directed image to its exact slide_id, crop, focal point, and copy-safe negative space.",
					"Use at least four composition types without novelty for novelty's sake. The cover is radically simple. Metrics become large-number compositions, evidence gets readable source furniture, and no text box may overflow or collide accidentally.",
					"Return structured JSON plus a short visual QA checklist. Do not rewrite copy in the layout stage.",
				}, "\n"),
				OutputContract: "layout_plan_v2",
			},
			{
				ID:        "ship_deck",
				Title:     "Build the editable presentation",
				Role:      processRoleWriter,
				Mode:      "artifacts",
				InputFrom: []string{"copy_edit", "founder_pass", "voice", "evidence", "identity", "imagery_direction", "imagery_generate", "layout_plan"},
				PromptBody: strings.Join([]string{
					"Produce the deck as ONE self-contained HTML file: all CSS and JS inline, no external references — the ONLY URLs permitted anywhere are data: URIs (used for any embedded imagery). Start with <!doctype html>.",
					"Treat copy_edit as the LOCKED copy and layout_plan as the LOCKED scene specification. Render them faithfully. Do not improvise a new title-plus-bullets layout and do not rewrite copy during rendering.",
					"Build the deck on the REQUIRED print chassis. Include this exact <style> block verbatim in <head>, lay every slide out as a <section class=\"pg\">…</section> inside a single <div id=\"stage\">…</div>, and give the FIRST slide the extra class \"on\". NEVER remove or weaken the @page or @media print rules — they are what make the exported PDF contain EVERY slide instead of only the first one:",
					"<style>\n" + strings.TrimSpace(packagingDeckChassisCSS) + "\n</style>",
					"Layer all brand aesthetics (colors, type, furniture) ON TOP of this chassis; do not fight its geometry (the 1920x1080 #stage, the .pg slide model). Treat every page as a designed 1920×1080 composition, never as a document paragraph parked in the upper-left.",
					"FIRST-CLASS LAYOUT SYSTEM: establish CSS tokens for palette, heading/body type, spacing, safe margins, and a 12-column grid. Use a minimum 96px safe zone. Headline type should normally be 88-160px, body 34-48px, labels 22-28px. Use deliberate scale, alignment, and contrast that remains legible from presentation distance.",
					"LAYOUT VARIETY WITH ONE VISUAL THESIS: choose the best supported composition per beat — cover, section break, statement, evidence, comparison, numbers, timeline, quote, image-led, or close. Mix at least four composition types. Do not repeat a title-plus-bullets template; do not leave accidental empty space; do not center every slide. Reserve centered type for intentional dramatic beats.",
					"DENSITY AND CRAFT: one claim per slide, no prose wall, no orphan labels, no tiny footnotes, and no more than 45 client-facing words on a normal slide unless it is a deliberately designed evidence page. Turn metrics into large-number compositions, comparisons into aligned columns, sequences into a visible path, and quotations into typographic moments. Add restrained slide numbers and source/caption furniture where evidence or imagery requires it.",
					"EDITOR COMPATIBILITY: put each slide's background color directly on its <section class=\"pg\"> inline style. Give every meaningful text block, image plate, and decorative shape a stable data-deck-element id plus data-deck-type=\"text|image|shape\". Put position:absolute and its left, top, width, height, z-index, opacity, and rotation directly in that element's inline style using 1920×1080 pixel coordinates; do not leave editable geometry only in a CSS class. Put text color/font-size/font-weight, image object-fit, and shape fill/stroke directly on the element too. Decorative background layers must carry data-deck-element when they are intended to be editable. This explicit geometry is part of the deliverable contract, not optional metadata.",
					"TEXT FIT CONTRACT: every text block must also put font-family, line-height, letter-spacing, and text-align inline. Author its width and height to contain the rendered text at that exact size, leading, and tracking. Independent text boxes must not intersect and should keep at least 24px of breathing room. If an intentional overlap is essential to the composition, mark the overlapping elements data-deck-overlap=\"allow\"; otherwise any text overflow, clipping, off-canvas geometry, or text-box intersection is a blocking render defect, not a style choice.",
					"IMAGERY: place each FIG the imagery_generate record lists as GENERATED at the slide the imagery_direction assigned. The photo element MUST use this native-importable grammar: <figure class=\"image-plate fig-N\" data-deck-element=\"unique-image-id\" data-deck-type=\"image\" style=\"position:absolute;left:...px;top:...px;width:...px;height:...px;z-index:...;opacity:1;transform:rotate(0deg);object-fit:cover\"><div class=\"ph\"></div><figcaption data-deck-element=\"unique-caption-id\" data-deck-type=\"text\" style=\"position:absolute;...\">FIG. caption</figcaption></figure>. For full bleed, add class \"bleed\" to that same figure and use left:0;top:0;width:1920px;height:1080px. Replace N with the matching FIG number. Do NOT paste any image data or invent src/url values — the image bytes are inlined at compile as a data: URI onto .fig-N .ph. Add a fig-N slot ONLY for FIG numbers the generation record generated; if imagery was skipped or zero, build a deliberately typographic deck with no photo plates.",
					"FULL-BLEED LAW: when a generated image carries the emotional beat, let it reach all four slide edges and place copy over a purpose-built solid or gradient scrim. When the image is evidence, use a disciplined plate with a caption. Never use a small decorative image that contributes no meaning.",
					"PRESENTER NOTES: put VOICE's matching per-page script inside that slide as one <div class=\"notes\" hidden>…</div>, preserving its [BEAT] pause. Do not add custom JavaScript or presenter chrome to the file: Bonfire's native presenter owns navigation, notes, full-screen behavior, and consistent controls. Unknown behavior makes the deck non-editable and the law sweep rejects it.",
					"Honor the decisions captured in Final content review, including any lines the reviewer explicitly asked Scout to preserve. Keep client-facing copy free of em dashes. " + studioSourceLanguageLaw,
				}, "\n"),
				OutputContract: packagingStudioDeckContract,
			},
			{
				ID:        "ship_compile",
				Title:     "Assemble the presentation package",
				Role:      processRoleCompile,
				InputFrom: []string{"red_team", "write", "copy_edit", "gate", "voice", "founder_pass", "ship_deck"},
				// Documentation only — compile is authored Go (below), never a
				// model call. The flatten law stays server-owned: the compiler
				// stamps paperKit and serverRenderKindForArtifact picks the kind.
				PromptBody: "Deterministic compile step: file the five interlocking artifacts (deck html_deck, The Wall, The Talk with paperKit=true, rigor companion, findings record aggregated from the run's actual verdicts), attach every one to the venture package, and enqueue the render exports — the deck flattened, The Talk text-native — or disclose the skips when the sidecar is absent.",
				Compile:    compilePackagingStudioShip,
			},
			{
				ID:        "slide_jury",
				Title:     "Review every rendered slide",
				Role:      processRoleCompile,
				InputFrom: []string{"ship_compile"},
				// Documentation only — the jury stage is authored Go (below). It is
				// ADVISORY: findings land as revision notes on the findings record,
				// never as an auto-revise; keyless / sidecar-absent / export-timeout
				// all disclose a skip and the ship proceeds to its approval.
				PromptBody: "Vision jury step: once the deck's PDF export completes, the render-runner's page JPEGs go before the /packaging jury trio (headline ear, design eye, the domain-literate room gut) — each seat sees ALL pages, scores per page, names weakest_three/strongest_three, and every fix is executable or the literal word KEEP. The merged scoreboard files as slide_jury_v1 and lands as revision notes on the findings record; the reviewer decides what to apply. Sidecar absent or export incomplete: the skip is disclosed.",
				Compile:    compilePackagingStudioSlideJury,
			},
			{
				ID:        "ship_approval",
				Title:     "Final deck review",
				Role:      processRoleHumanCheckpoint,
				InputFrom: []string{"ship_compile", "slide_jury", "ship_deck"},
				CheckpointSpec: &ProcessCheckpointSpec{
					Question: "The editable presentation is ready in this channel. Approve external sharing, request a rebuild, or keep it internal.",
					Options: []ProcessCheckpointOption{
						{Label: "approve the ship"},
						{Label: "send back — rebuild the deck", Action: processCheckpointActionRevise, Target: "ship_deck"},
						{Label: "hold the package", Action: processCheckpointActionHold},
					},
				},
			},
		},
	}
}

// --- The SHIP compile stage ---------------------------------------------------

// compilePackagingStudioShip is the ship_compile stage's ProcessCompileFunc —
// the seam that puts fileStudioShipDeliverables INSIDE the executing pipeline.
// Once the ship_deck writer lands, it assembles the run's own stage artifacts
// into the five interlocking deliverables: the deck verbatim from ship_deck,
// The Wall from WRITE's gated copy, The Talk from VOICE's presenter script,
// the rigor companion from the objection ledger + the gate record, and the
// findings record aggregated from the ACTUAL verdicts the engine filed for
// this goal. The returned body is the compile record — every filed id and
// every disclosed render skip — which becomes the ship_approval checkpoint's
// grounding.
func compilePackagingStudioShip(app *kanbanBoardApp, plan *goalPlan, parentID string, _ ProcessStage) (string, map[string]string, error) {
	if app == nil || plan == nil {
		return "", nil, fmt.Errorf("the studio compile stage has no app/plan to read")
	}
	stageBody := func(stageID string) string {
		st := plan.subtaskByID(stageID)
		if st == nil {
			return ""
		}
		artifact, ok := app.osArtifactByID(st.ArtifactID)
		if !ok {
			return ""
		}
		return strings.TrimSpace(artifact.Text)
	}

	deckHTML := stageBody("ship_deck")
	if deckHTML == "" {
		return "", nil, fmt.Errorf("ship_deck produced no deck body — nothing to compile")
	}
	// Inline the directed imagery as data: URIs at their .fig-N slots BEFORE the
	// deck is filed and its render enqueued — the render CSP admits only data:
	// images, so this is the one place the bytes can reach the self-contained
	// deck. A typographic package (no imagery) passes through untouched.
	deckHTML, imageryNote := injectStudioDeckImagery(app, plan, deckHTML)
	deckCopy := firstNonEmptyString(stageBody("copy_edit"), stageBody("write"))
	if deckCopy == "" {
		return "", nil, fmt.Errorf("the write stage left no gated copy — The Wall cannot compile")
	}
	script := stageBody("voice")
	if script == "" {
		return "", nil, fmt.Errorf("the voice stage left no presenter script — The Talk cannot compile")
	}
	// The rigor sections degrade with DISCLOSED placeholders rather than
	// failing the ship: an attacked-and-documented package with a hole named
	// is still shippable; a silent hole is not.
	ledger := firstNonEmptyString(stageBody("red_team"), "(the round-1 objection ledger was not produced — disclosed)")
	gateRecord := firstNonEmptyString(stageBody("gate"), "(the gate record was not produced — disclosed)")
	founderPass := firstNonEmptyString(stageBody("founder_pass"), "(no founder-pass record — disclosed)")

	wall := strings.Join([]string{
		"# The Wall — slide-copy record",
		"",
		"Every client-facing line of the reviewed deck copy, on the record. No em dashes in a client-facing line; quoted source language remains exact.",
		"",
		deckCopy,
	}, "\n")
	talk := strings.Join([]string{
		"# The Talk — presenter one-sheet",
		"",
		"The speechwriter's per-page script: 25-45 seconds a page, one [BEAT] each. The interlock rule holds — the voice owns the parables, the slides own the numbers.",
		"",
		script,
	}, "\n")
	rigor := strings.Join([]string{
		"# Rigor companion",
		"",
		"The diligence trail behind the deck: what the skeptical review raised, what the content check verified, and what was approved in final review.",
		"",
		"## The round-1 objection ledger (red team)",
		ledger,
		"",
		"## The gate's decision, objections in hand",
		gateRecord,
		"",
		"## Final content review",
		founderPass,
	}, "\n")

	deckTitle := "Packaging Studio deck"
	if parent, ok := app.osArtifactByID(parentID); ok {
		if title := strings.TrimSpace(parent.Metadata["title"]); title != "" {
			deckTitle = title + " — presenter deck"
		}
	}

	filed, err := app.fileStudioShipDeliverables(studioShipInputs{
		GoalID:     parentID,
		PackageID:  plan.PackageID,
		CreatedBy:  plan.CreatedBy,
		DeckHTML:   deckHTML,
		DeckAssets: studioDeckImageryAssets(app, plan),
		Wall:       wall,
		Talk:       talk,
		Rigor:      rigor,
		Findings:   composeStudioFindingsRecord(app, plan, parentID),
		DeckTitle:  deckTitle,
	})
	if err != nil {
		return "", nil, err
	}

	lines := []string{
		"Ship compile — the five interlocking artifacts",
		"",
	}
	filedIDs := make([]string, 0, len(filed))
	for _, deliverable := range filed {
		filedIDs = append(filedIDs, deliverable.ArtifactID)
		line := "- " + deliverable.Contract + " → " + deliverable.ArtifactID + " (" + deliverable.Type
		if deliverable.PaperKit {
			line += ", paper kit"
		}
		line += ")"
		if deliverable.RenderJob != "" {
			line += " — render export queued as " + deliverable.RenderJob
		}
		if deliverable.RenderNote != "" {
			line += " — render skipped (disclosed): " + deliverable.RenderNote
		}
		lines = append(lines, line)
	}
	if plan.PackageID != "" {
		lines = append(lines, "", "Every artifact is attached to package "+plan.PackageID+".")
	} else {
		lines = append(lines, "", "No venture package on this goal — the artifacts are filed unattached (disclosed).")
	}
	if strings.TrimSpace(imageryNote) != "" {
		lines = append(lines, "", imageryNote)
	}
	return strings.Join(lines, "\n"), map[string]string{"shipArtifactIds": strings.Join(filedIDs, ",")}, nil
}

// --- The IMAGERY seam: direction → generation → placement --------------------

// imageryDirectionShot is one entry the art director emits in its JSON block.
type imageryDirectionShot struct {
	Fig         int    `json:"fig"`
	SlideID     string `json:"slide_id"`
	Slot        string `json:"slot"`
	Subject     string `json:"subject"`
	Composition string `json:"composition"`
	Temperature string `json:"temperature"`
	Treatment   string `json:"treatment"`
	Aspect      string `json:"aspect"`
	Caption     string `json:"caption"`
	Place       string `json:"place"`
	Why         string `json:"why"`
}

type imageryDirectionDoc struct {
	Strategy     string                 `json:"strategy"`
	VisualSystem string                 `json:"visual_system"`
	Shots        []imageryDirectionShot `json:"shots"`
}

// parseImageryDirection extracts the art director's fenced JSON block and maps
// it to the generator's shots. A missing/garbled block or an empty shot list is
// a VALID typographic outcome (zero shots), never an error — imagery is
// editorial. Shots missing a subject or a named temperature are dropped (the
// generator requires both); the total is capped at the board ceiling.
func parseImageryDirection(body string) (visualSystem string, shots []imageryShot) {
	raw := extractFencedJSON(body)
	if raw == "" {
		return "", nil
	}
	var doc imageryDirectionDoc
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		return "", nil
	}
	visualSystem = strings.TrimSpace(doc.VisualSystem)
	for _, s := range doc.Shots {
		subject := strings.TrimSpace(s.Subject)
		temperature := strings.TrimSpace(s.Temperature)
		if subject == "" || temperature == "" {
			continue
		}
		description := subject
		if comp := strings.TrimSpace(s.Composition); comp != "" {
			description += ". Composition: " + comp
		}
		shots = append(shots, imageryShot{
			Fig:         s.Fig,
			Title:       firstNonEmptyString(strings.TrimSpace(s.Caption), subject),
			Description: description,
			Temperature: temperature,
			Place:       strings.TrimSpace(s.Place),
		})
		if len(shots) >= imageryBoardMaxShots {
			break
		}
	}
	return visualSystem, shots
}

// extractFencedJSON returns the first ```json (or ```) fenced block's contents,
// else the trimmed body when it already looks like a JSON object. Empty when
// nothing parseable is present.
func extractFencedJSON(body string) string {
	lower := strings.ToLower(body)
	if idx := strings.Index(lower, "```json"); idx >= 0 {
		rest := body[idx+len("```json"):]
		if end := strings.Index(rest, "```"); end >= 0 {
			return strings.TrimSpace(rest[:end])
		}
	}
	if idx := strings.Index(body, "```"); idx >= 0 {
		rest := body[idx+3:]
		if end := strings.Index(rest, "```"); end >= 0 {
			if candidate := strings.TrimSpace(rest[:end]); strings.HasPrefix(candidate, "{") {
				return candidate
			}
		}
	}
	if trimmed := strings.TrimSpace(body); strings.HasPrefix(trimmed, "{") && strings.HasSuffix(trimmed, "}") {
		return trimmed
	}
	return ""
}

// compilePackagingStudioImagery is the imagery_generate stage's compile: it
// reads the art director's shot list and fulfills each brief via the existing
// gpt-image generator. Imagery is EDITORIAL and never blocks the ship — a
// typographic direction (zero shots), a keyless deploy, or a quota/timeout that
// fails every shot all DISCLOSE and proceed with fewer/zero images. On success
// it stamps the fig→blob placements ship_compile inlines into the deck.
func compilePackagingStudioImagery(app *kanbanBoardApp, plan *goalPlan, parentID string, _ ProcessStage) (string, map[string]string, error) {
	if app == nil || plan == nil {
		return "", nil, fmt.Errorf("the imagery stage has no app/plan to read")
	}
	direction := ""
	if st := plan.subtaskByID("imagery_direction"); st != nil {
		if artifact, ok := app.osArtifactByID(st.ArtifactID); ok {
			direction = strings.TrimSpace(artifact.Text)
		}
	}
	visualSystem, shots := parseImageryDirection(direction)
	if len(shots) == 0 {
		return strings.Join([]string{
			"Imagery — the package is typographic (no images directed)",
			"",
			"The art director directed no imagery: type and evidence carry this package. This is a valid, deliberate outcome.",
		}, "\n"), map[string]string{"imageryShots": "0"}, nil
	}
	if strings.TrimSpace(visualSystem) == "" {
		visualSystem = "The deck's own visual identity; keep every shot on one coherent system."
	}

	title := "Imagery"
	if parent, ok := app.osArtifactByID(parentID); ok {
		if t := strings.TrimSpace(parent.Metadata["title"]); t != "" {
			title = t + " — imagery"
		}
	}
	// Each shot self-bounds at the generator's 120s HTTP ceiling; give the stage
	// generous headroom for the whole board and disclose on any per-shot miss.
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(len(shots)+1)*150*time.Second)
	defer cancel()
	board, generated, err := app.runImageryBoard(ctx, imageryBoardInput{
		Title:        title,
		VisualSystem: visualSystem,
		Shots:        shots,
		PackageID:    plan.PackageID,
		CreatedBy:    plan.CreatedBy,
	})
	if err != nil {
		// Disclosed + non-fatal: keyless / quota / every-shot-failed ships the
		// package typographic rather than blocking it.
		return strings.Join([]string{
			"Imagery — skipped (disclosed); the package ships typographic",
			"",
			fmt.Sprintf("%d shot(s) were directed but none were generated: %s", len(shots), compactAssistantLine(err.Error())),
		}, "\n"), map[string]string{"imageryShots": "skipped"}, nil
	}

	placements, _ := json.Marshal(generated)
	lines := []string{
		"Imagery — the directed shots were generated",
		"",
		fmt.Sprintf("- %d of %d directed shot(s) generated on one visual system; filed as {kind:image} assets on %s.", len(generated), len(shots), board.ID),
		"- Each generated FIG is inlined at its .fig-N slot as a data: URI by ship_compile.",
	}
	if len(generated) < len(shots) {
		lines = append(lines, fmt.Sprintf("- %d directed shot(s) failed generation and were disclosed on the imagery board (the ship proceeds).", len(shots)-len(generated)))
	}
	return strings.Join(lines, "\n"), map[string]string{
		"imageryShots":           fmt.Sprintf("%d", len(generated)),
		"imageryBoardArtifactId": board.ID,
		"imageryFigs":            string(placements),
	}, nil
}

// injectStudioDeckImagery inlines each generated image as a data: URI onto its
// .fig-N slot in the deck HTML, returning the augmented HTML and a disclosure
// note. It reads the imagery_generate stage's stamped fig→blob placements; a
// typographic package (no placements) passes the deck through unchanged. A blob
// that cannot be read, or a fig with no matching .fig-N slot the writer built,
// is disclosed in the note, never fatal.
func injectStudioDeckImagery(app *kanbanBoardApp, plan *goalPlan, deckHTML string) (string, string) {
	st := plan.subtaskByID("imagery_generate")
	if st == nil {
		return deckHTML, ""
	}
	record, ok := app.osArtifactByID(st.ArtifactID)
	if !ok {
		return deckHTML, ""
	}
	raw := strings.TrimSpace(record.Metadata["imageryFigs"])
	if raw == "" {
		return deckHTML, "Imagery: none placed — the package is typographic."
	}
	var placements []imageryGeneratedShot
	if err := json.Unmarshal([]byte(raw), &placements); err != nil || len(placements) == 0 {
		return deckHTML, "Imagery: none placed — the package is typographic."
	}

	images := make([]deckImage, 0, len(placements))
	unreadable := 0
	for _, p := range placements {
		dataURI, err := blobDataURI(p.Ref, p.Mime)
		if err != nil {
			unreadable++
			continue
		}
		images = append(images, deckImage{Fig: p.Fig, DataURI: dataURI})
	}
	return applyDeckImagery(deckHTML, images, len(placements), unreadable)
}

// deckImage is one resolved image ready to inline: its stable FIG and the
// base64 data: URI.
type deckImage struct {
	Fig     int
	DataURI string
}

// applyDeckImagery injects a <style> block mapping each image to its .fig-N .ph
// slot and returns the augmented deck HTML plus a disclosure note. Pure and
// testable: an image whose .fig-N slot the writer never built is disclosed as a
// missing slot; unreadable counts blobs that could not be loaded upstream.
func applyDeckImagery(deckHTML string, images []deckImage, generated int, unreadable int) (string, string) {
	var rules []string
	placed, missingSlot := 0, 0
	for _, img := range images {
		figClass := fmt.Sprintf("fig-%d", img.Fig)
		rules = append(rules, fmt.Sprintf(".%s .ph{background-image:url(%s);background-size:cover;background-position:center}", figClass, img.DataURI))
		if strings.Contains(deckHTML, figClass) {
			placed++
		} else {
			missingSlot++
		}
	}
	if len(rules) == 0 {
		if generated > 0 {
			return deckHTML, fmt.Sprintf("Imagery: %d image(s) generated but none could be inlined (blobs unreadable) — disclosed; the deck ships typographic.", generated)
		}
		return deckHTML, "Imagery: none placed — the package is typographic."
	}
	style := "<style id=\"bonfire-imagery\">" + strings.Join(rules, "\n") + "</style>"
	augmented := insertIntoDocumentHead(deckHTML, style)
	note := fmt.Sprintf("Imagery: %d image(s) inlined as data: URIs at their .fig-N slots.", placed)
	if missingSlot > 0 {
		note += fmt.Sprintf(" %d generated image(s) had no matching slot in the deck (disclosed).", missingSlot)
	}
	if unreadable > 0 {
		note += fmt.Sprintf(" %d image blob(s) were unreadable and skipped (disclosed).", unreadable)
	}
	return augmented, note
}

// --- The SLIDE JURY stage -----------------------------------------------------

// compilePackagingStudioSlideJury is the slide_jury stage's ProcessCompileFunc
// (Wave 5 item 21): the optional vision jury AFTER the SHIP compile. It runs
// only when the deck's PDF export completed and page images exist — the render
// callback persists them as {kind: image} assets (persistRenderPageImageAssets)
// — waiting a bounded window for the in-flight export. Every degraded path
// (keyless, sidecar absent, export timed out or failed, the jury panel itself
// erroring) is a DISCLOSED skip in the stage record, never a blocked ship: the
// jury is advisory. On success the merged scoreboard files as slide_jury_v1
// and lands as revision notes on the findings record — NOT an auto-revise; the
// founder sees the scoreboard at ship approval and decides what to apply.
func compilePackagingStudioSlideJury(app *kanbanBoardApp, plan *goalPlan, parentID string, _ ProcessStage) (string, map[string]string, error) {
	if app == nil || plan == nil {
		return "", nil, fmt.Errorf("the slide jury stage has no app/plan to read")
	}
	skip := func(reason string) (string, map[string]string, error) {
		return strings.Join([]string{
			"Slide jury — skipped (disclosed)",
			"",
			"The vision jury did not run: " + reason,
			"The package ships un-juried; export the deck PDF later and the page images will be on file for a future jury.",
		}, "\n"), map[string]string{"slideJury": "skipped", "reviewVerdict": "needs_attention"}, nil
	}

	deck, findings, err := studioShipArtifactsForJury(app, plan)
	if err != nil {
		return "", nil, err
	}
	if strings.TrimSpace(deck.Metadata["renderJobId"]) == "" && len(artifactPageImageAssets(deck)) == 0 {
		// ship_compile disclosed a render skip (sidecar absent, or a non-HTML
		// deck): no export means no page images, so the jury has nothing to see.
		return skip("the deck's PDF export was not queued (render sidecar absent or export skipped) — no rendered page images exist")
	}
	deck, ready := waitForDeckPageImages(app, deck.ID)
	if !ready {
		return skip(fmt.Sprintf("the deck's PDF export did not complete within the %s wait window — no rendered page images landed", slideJuryWaitTimeout()))
	}
	pageImages := artifactPageImageAssets(deck)
	if err := validatePackagingStudioDeckRender(deck); err != nil {
		return "", nil, err
	}
	if strings.TrimSpace(app.currentOpenAIAPIKey()) == "" {
		return skip("no OpenAI key is configured (providerless deploy) — the jury seats cannot see")
	}

	ctx, cancel := context.WithTimeout(context.Background(), orchestratorTimeout())
	defer cancel()
	jury, err := runSlideJury(ctx, app, parentID, deck)
	if err != nil {
		// Advisory stage: a failed panel is disclosed, never a blocked ship.
		return skip("the jury panel failed: " + compactAssistantLine(err.Error()))
	}

	findingsNote := appendSlideJuryRevisionNotes(app, findings, jury)

	verdict := firstNonEmptyString(strings.TrimSpace(jury.Metadata["reviewVerdict"]), "needs_attention")
	blockingPages := strings.TrimSpace(jury.Metadata["blockingPages"])
	readinessLine := "- Rendered review verdict: " + verdict + "."
	if blockingPages != "" {
		readinessLine = "- Rendered review verdict: needs changes on slide(s) " + blockingPages + "; final review must not describe this version as ready."
	}
	lines := []string{
		"Slide jury — the critics saw the rendered pages",
		"",
		fmt.Sprintf("- %d rendered page image(s) went before the 3-seat jury (headline ear, design eye, room gut) — every seat saw all pages.", len(pageImages)),
		"- Merged scoreboard filed: " + slideJuryContract + " → " + jury.ID,
		readinessLine,
		"- " + findingsNote,
		"- The reviewer applies the concrete fixes in Deck Studio or sends the deck back before approving.",
	}
	return strings.Join(lines, "\n"), map[string]string{
		"slideJuryArtifactId": jury.ID,
		"reviewVerdict":       verdict,
		"blockingPages":       blockingPages,
		"minimumAverage":      strings.TrimSpace(jury.Metadata["minimumAverage"]),
	}, nil
}

func validatePackagingStudioDeckRender(deck meetingMemoryEntry) error {
	expected := renderedDeckSlideCount(deck.Text)
	if expected <= 0 {
		return fmt.Errorf("the deck has no recognized authored slide topology; final review cannot proceed")
	}
	if pageCount := len(artifactPageImageAssets(deck)); pageCount != expected {
		return fmt.Errorf("the deck render is incomplete: %d authored slide(s), %d rendered page image(s); final review cannot proceed", expected, pageCount)
	}
	return nil
}

// studioShipArtifactsForJury resolves the deck and findings artifacts the SHIP
// compile filed, via the shipArtifactIds stamp on the ship_compile stage
// record — the jury reads the run's OWN deliverables, never a lookalike.
func studioShipArtifactsForJury(app *kanbanBoardApp, plan *goalPlan) (deck meetingMemoryEntry, findings meetingMemoryEntry, err error) {
	st := plan.subtaskByID("ship_compile")
	if st == nil {
		return deck, findings, fmt.Errorf("the plan has no ship_compile stage — the jury has no deck to see")
	}
	record, ok := app.osArtifactByID(st.ArtifactID)
	if !ok {
		return deck, findings, fmt.Errorf("the ship_compile record is missing — the jury has no deck to see")
	}
	deckFound := false
	for _, id := range strings.Split(record.Metadata["shipArtifactIds"], ",") {
		artifact, ok := app.osArtifactByID(strings.TrimSpace(id))
		if !ok {
			continue
		}
		switch artifact.Metadata["artifactContract"] {
		case packagingStudioDeckContract:
			deck = artifact
			deckFound = true
		case packagingStudioFindingsContract:
			findings = artifact
		}
	}
	if !deckFound {
		return deck, findings, fmt.Errorf("the ship compile filed no deck artifact — the jury has no deck to see")
	}
	return deck, findings, nil
}

// appendSlideJuryRevisionNotes lands the merged scoreboard on the findings
// record as revision notes — appended, disclosed, and explicitly NOT applied.
// A missing findings record degrades to a disclosed note on the stage record;
// the scoreboard artifact stands either way.
func appendSlideJuryRevisionNotes(app *kanbanBoardApp, findings meetingMemoryEntry, jury meetingMemoryEntry) string {
	if strings.TrimSpace(findings.ID) == "" {
		return "findings record missing — the scoreboard stands alone on the jury artifact (disclosed)"
	}
	// The merged scoreboard is the note; the per-seat transcript stays on the
	// jury artifact (the composeStudioFindingsRecord panel-voices posture).
	scoreboard := strings.TrimSpace(jury.Text)
	if cut := strings.Index(scoreboard, "\n## Jury voices"); cut > 0 {
		scoreboard = strings.TrimSpace(scoreboard[:cut])
	}
	body := strings.TrimSpace(findings.Text) + strings.Join([]string{
		"",
		"",
		"## Slide jury — revision notes (" + slideJuryContract + ")",
		"",
		"The vision jury saw the rendered pages. These are REVISION NOTES — human judgment decides what to apply; nothing below was auto-revised. Full scoreboard and per-seat voices: " + jury.ID,
		"",
		scoreboard,
	}, "\n")
	if _, _, err := app.updateOSArtifactWithMetadata(findings.ID, "", body, scoutParticipantName, map[string]string{
		"slideJuryArtifactId": jury.ID,
	}); err != nil {
		log.Errorf("slide jury: revision notes did not land on findings record %s: %v", findings.ID, err)
		return "revision notes did NOT land on the findings record (" + compactAssistantLine(err.Error()) + ") — read them on the jury artifact (disclosed)"
	}
	return "revision notes appended to the findings record " + findings.ID
}

// studioFindingsExcerptCap bounds how much of one panel synthesis the findings
// record quotes — the record is an audit trail, not a re-print; the full stage
// artifact stays on file and is named in the section header.
const studioFindingsExcerptCap = 1200

// composeStudioFindingsRecord aggregates the run's ACTUAL verdicts into the
// findings artifact: it queries the stage artifacts the engine filed for THIS
// goal (metadata goalParentId, the completeProcessStage/resumeProcessCheckpoint
// shape) and quotes, in filing order and revision rounds included, every panel
// synthesis, every gate decision with its score and disclosed gaps, every
// human checkpoint choice, and every render disclosure. "Clients trust a
// document more when they can see it was attacked."
func composeStudioFindingsRecord(app *kanbanBoardApp, plan *goalPlan, parentID string) string {
	lines := []string{
		"# Findings record — every verdict on the record",
		"",
		"The run's audit trail, aggregated from the stage artifacts the engine filed: every panel, gate, and checkpoint verdict, in filing order (revision rounds included).",
		"",
		"Goal: " + compactAssistantLine(plan.Objective),
	}
	found := 0
	for _, artifact := range app.osArtifactsSnapshot(0) {
		if strings.TrimSpace(artifact.Metadata["goalParentId"]) != parentID {
			continue
		}
		if artifact.Metadata["source"] != "process_stage" {
			continue
		}
		role := artifact.Metadata["processRole"]
		switch role {
		case processRolePanel, processRoleJudges, processRoleGate, processRoleHumanCheckpoint, processRoleRender:
			// The verdict-bearing roles. Writer/synthesizer outputs are the
			// deliverables themselves — they ship as the deck and The Wall,
			// not as findings.
		default:
			continue
		}
		found++
		stageID := firstNonEmptyString(artifact.Metadata["processStage"], artifact.Metadata["goalSubtaskId"])
		body := strings.TrimSpace(artifact.Text)
		if role == processRolePanel || role == processRoleJudges {
			// The synthesis is the verdict; the per-voice transcript stays on
			// the referenced stage artifact.
			if cut := strings.Index(body, "\n## Panel voices"); cut > 0 {
				body = strings.TrimSpace(body[:cut])
			}
			body = studioFindingsExcerpt(body)
		}
		lines = append(lines,
			"",
			"## "+stageID+" ("+role+") — "+firstNonEmptyString(strings.TrimSpace(artifact.Metadata["title"]), "stage record"),
			"Stage artifact: "+artifact.ID,
			"",
			body,
		)
	}
	if found == 0 {
		lines = append(lines, "", "(no stage verdicts were filed for this goal — nothing to disclose)")
	}
	return strings.Join(lines, "\n")
}

// studioFindingsExcerpt caps one quoted synthesis at a rune boundary, with the
// truncation announced — an audit trail never silently drops the middle.
func studioFindingsExcerpt(body string) string {
	if len(body) <= studioFindingsExcerptCap {
		return body
	}
	cut := studioFindingsExcerptCap
	for cut > 0 && !utf8.RuneStart(body[cut]) {
		cut--
	}
	return strings.TrimSpace(body[:cut]) + "\n[... excerpted for the findings record; the full synthesis is on the stage artifact ...]"
}

// --- The SHIP compiler ------------------------------------------------------

// studioShipDeliverable is one filed SHIP artifact and the outcome of its
// optional render enqueue.
type studioShipDeliverable struct {
	Contract   string
	ArtifactID string
	Type       string
	PaperKit   bool
	RenderJob  string // the enqueued render job id, "" when none/skipped
	RenderNote string // disclosed skip reason, "" when enqueued or not a render target
}

// studioShipInputs is the material the SHIP compiler assembles into the five
// interlocking artifacts — the outputs of the pipeline's WRITE / VOICE / RED-TEAM
// / GATE stages, already produced by the time SHIP runs.
type studioShipInputs struct {
	GoalID     string // the running goal, stamped for provenance
	PackageID  string
	CreatedBy  string
	DeckHTML   string          // ship_deck's self-contained HTML
	DeckAssets []artifactAsset // generated FIG images, attached for faithful editing
	Wall       string          // the slide-copy record ("The Wall")
	Talk       string          // the branded one-sheet ("The Talk") — text-native, paperKit
	Rigor      string          // the diligence companion
	Findings   string          // the findings audit trail (every panel/gate/jury verdict)
	DeckTitle  string
}

func studioDeckImageryAssets(app *kanbanBoardApp, plan *goalPlan) []artifactAsset {
	if app == nil || plan == nil {
		return nil
	}
	stage := plan.subtaskByID("imagery_generate")
	if stage == nil {
		return nil
	}
	record, ok := app.osArtifactByID(stage.ArtifactID)
	if !ok {
		return nil
	}
	var placements []imageryGeneratedShot
	if err := json.Unmarshal([]byte(record.Metadata["imageryFigs"]), &placements); err != nil {
		return nil
	}
	assets := make([]artifactAsset, 0, len(placements))
	for _, placement := range placements {
		if placement.Fig < 1 || !validBlobRef(placement.Ref) || !strings.HasPrefix(strings.ToLower(strings.TrimSpace(placement.Mime)), "image/") {
			continue
		}
		assets = append(assets, artifactAsset{
			Ref: placement.Ref, Mime: placement.Mime,
			Name: fmt.Sprintf("fig-%d.%s", placement.Fig, deckImageExtension(placement.Mime)), Kind: "image",
		})
	}
	return assets
}

// fileStudioShipDeliverables is the SHIP stage's compiler: it files the FIVE
// interlocking artifacts the packaging skill ships (deck html_deck + The Wall +
// The Talk with paperKit=true + rigor companion + findings record), attaches
// every one to the venture package, and enqueues the render exports — the deck
// flattened, The Talk text-native — when the render sidecar is live. Sidecar-
// absent (or keyless) it still files all five HTML artifacts and DISCLOSES the
// skipped exports, exactly like runProcessRenderStage. The print KIND is never
// chosen here: serverRenderKindForArtifact owns the flatten law, and it reads
// the paperKit stamp this compiler sets on The Talk.
//
// "Clients trust a document more when they can see it was attacked" — the
// findings record is filed as a first-class artifact, not a footnote.
// studioContractProducingStage maps a ship deliverable's contract to the
// stage whose output it compiles FROM — the stage a feedback drop on that
// deliverable must re-run (goal_engine feedbackTargetSubtask). The deck is
// ship_deck's own body; The Wall is write's gated copy; The Talk is voice's
// presenter script; the rigor companion leads with the red team's objection
// ledger. The findings record aggregates the run's verdicts and maps to no
// single stage — feedback on it falls through to the checkpoint's declared
// send-back target.
func studioContractProducingStage(contract string) (string, bool) {
	switch strings.TrimSpace(contract) {
	case packagingStudioDeckContract:
		return "ship_deck", true
	case packagingStudioWallContract:
		return "write", true
	case packagingStudioTalkContract:
		return "voice", true
	case packagingStudioRigorContract:
		return "red_team", true
	}
	return "", false
}

// studioShipDeliverableByContract finds the goal's already-filed ship
// deliverable for one contract, if any — the re-ship dedupe key. Goal-less
// studio runs (empty goalID) never dedupe: without a goal there is no re-open
// path, so every ship is a fresh filing.
func (app *kanbanBoardApp) studioShipDeliverableByContract(goalID string, contract string) (meetingMemoryEntry, bool) {
	goalID = strings.TrimSpace(goalID)
	if goalID == "" {
		return meetingMemoryEntry{}, false
	}
	var latest meetingMemoryEntry
	found := false
	for _, entry := range app.osArtifactsSnapshot(0) {
		if entry.Metadata["source"] == "packaging_studio_ship" && entry.Metadata["goalId"] == goalID && entry.Metadata["artifactContract"] == contract {
			// Artifacts are oldest-first. Keep walking so a retry candidate,
			// rather than the already accepted presentation, is the living
			// draft that subsequent pre-approval compiles update in place.
			latest = entry
			found = true
		}
	}
	return latest, found
}

func (app *kanbanBoardApp) acceptedStudioResultArtifactID(goalID string) string {
	goalID = strings.TrimSpace(goalID)
	if app == nil || goalID == "" {
		return ""
	}
	parent, ok := app.osArtifactByID(goalID)
	if !ok {
		return ""
	}
	acceptedID := strings.TrimSpace(parent.Metadata["acceptedResultArtifactId"])
	if acceptedID != "" {
		return acceptedID
	}
	var plan goalPlan
	if raw := strings.TrimSpace(parent.Metadata["goalPlan"]); raw != "" && json.Unmarshal([]byte(raw), &plan) == nil {
		return strings.TrimSpace(plan.Report.AcceptedResultArtifactID)
	}
	return ""
}

func (app *kanbanBoardApp) fileStudioShipDeliverables(in studioShipInputs) ([]studioShipDeliverable, error) {
	if app == nil || app.memory == nil {
		return nil, fmt.Errorf("artifact memory is unavailable")
	}
	createdBy := firstNonEmptyString(strings.TrimSpace(in.CreatedBy), scoutParticipantName)
	deckTitle := firstNonEmptyString(strings.TrimSpace(in.DeckTitle), "Packaging Studio deck")

	// The five interlocking artifacts, in send order. The deck is an html_deck
	// (the sandboxed viewer renders it); The Talk / The Wall are paper-kit
	// documents (paperKit=true → serverRenderKindForArtifact returns the
	// text-native paper print, no flatten). The deck is the render target that
	// flattens; The Talk is the render target that prints text-native.
	specs := []struct {
		contract     string
		title        string
		body         string
		artifactType string
		paperKit     bool
		renderTarget bool
	}{
		{packagingStudioDeckContract, deckTitle, in.DeckHTML, artifactTypeHTMLDeck, false, true},
		{packagingStudioWallContract, "The Wall — slide-copy record", in.Wall, artifactTypeMarkdown, true, false},
		{packagingStudioTalkContract, "The Talk — presenter one-sheet", in.Talk, artifactTypeMarkdown, true, true},
		{packagingStudioRigorContract, "Rigor companion", in.Rigor, artifactTypeMarkdown, false, false},
		{packagingStudioFindingsContract, "Findings record — every verdict on the record", in.Findings, artifactTypeMarkdown, false, false},
	}

	sidecar := renderSidecarAvailable()
	acceptedDeckID := app.acceptedStudioResultArtifactID(in.GoalID)
	filed := make([]studioShipDeliverable, 0, len(specs))
	for _, spec := range specs {
		body := strings.TrimSpace(spec.body)
		if body == "" {
			return filed, fmt.Errorf("ship deliverable %q has an empty body — SHIP files no blank artifact", spec.contract)
		}
		// The first live run filed a markdown DESCRIPTION of the deck stamped
		// html_deck, and the mistyping rode all the way to a failed render and
		// a starved jury. The compiler refuses to mistype: an html_deck spec
		// whose body is not an actual HTML document fails the stage honestly.
		if spec.artifactType == artifactTypeHTMLDeck && !strings.HasPrefix(strings.ToLower(body), "<!doctype html") {
			return filed, fmt.Errorf("ship deliverable %q is not an HTML document (starts %q) — the ship_deck stage must produce the deck itself, not a description of it", spec.contract, compactAssistantLine(body[:min(len(body), 60)]))
		}
		metadata := map[string]string{
			"artifactContract": spec.contract,
			"type":             spec.artifactType,
			"source":           "packaging_studio_ship",
			"processId":        packagingStudioProcessID,
		}
		if in.GoalID != "" {
			metadata["goalId"] = in.GoalID
		}
		if spec.paperKit {
			// The stamp render_runner.go's flatten law reads: paper-kit documents
			// print text-native, decks flatten. Set at filing time, so a later
			// export never has to guess.
			metadata["paperKit"] = "true"
		}
		if in.PackageID != "" {
			metadata["packageId"] = in.PackageID
		}
		// Wave 6 (deep 1:1 linkage): a re-ship for the SAME goal — a feedback
		// re-open re-running ship_compile — versions the existing deliverable
		// in place (updateOSArtifactWithMetadata mints v+1 and archives the
		// prior body) instead of filing a stranger, so chat refs, drawer rows,
		// and package links keep pointing at the living artifact.
		var artifact meetingMemoryEntry
		existing, found := app.studioShipDeliverableByContract(in.GoalID, spec.contract)
		// Once a human has approved a deck, never rewrite that artifact in
		// place. The feedback run gets a new candidate id; further compiles of
		// that still-unapproved candidate may version it normally. Supporting
		// documents keep their stable ids because only the presentation is the
		// rich channel handoff bound by acceptedResultArtifactId.
		if found && spec.contract == packagingStudioDeckContract && existing.ID == acceptedDeckID {
			found = false
		}
		if found {
			// The prior run's render exports are STALE against the revised
			// body — clear them so the re-enqueued export lands as the only
			// asset; a pending render reads honest, a superseded PDF does not.
			metadata[artifactAssetsMetadataKey] = ""
			var err error
			artifact, _, err = app.updateOSArtifactWithMetadata(existing.ID, "", body, createdBy, metadata)
			if err != nil {
				return filed, fmt.Errorf("re-file ship deliverable %q: %w", spec.contract, err)
			}
		} else {
			var appended bool
			var err error
			artifact, appended, err = app.createOSArtifactWithMetadata("workflow", spec.title, body, createdBy, metadata)
			if err != nil {
				return filed, fmt.Errorf("file ship deliverable %q: %w", spec.contract, err)
			}
			if !appended || strings.TrimSpace(artifact.ID) == "" {
				return filed, fmt.Errorf("ship deliverable %q was not saved", spec.contract)
			}
		}
		// Attach to the venture package — the bidirectional binder link SHIP
		// promises. A missing package is disclosed, not fatal: the artifact is
		// filed either way.
		if in.PackageID != "" {
			if _, err := app.attachToPackage(in.PackageID, packageRefTypeArtifact, artifact.ID, createdBy); err != nil {
				log.Errorf("packaging_studio ship: attach %s to package %s failed: %v", artifact.ID, in.PackageID, err)
			}
		}
		if spec.contract == packagingStudioDeckContract {
			for _, asset := range in.DeckAssets {
				updated, attachErr := app.appendArtifactAsset(artifact.ID, asset)
				if attachErr != nil {
					return filed, fmt.Errorf("attach deck imagery %q: %w", asset.Name, attachErr)
				}
				artifact = updated
			}
		}

		deliverable := studioShipDeliverable{
			Contract:   spec.contract,
			ArtifactID: artifact.ID,
			Type:       spec.artifactType,
			PaperKit:   spec.paperKit,
		}
		if spec.renderTarget {
			deliverable.RenderJob, deliverable.RenderNote = app.enqueueStudioRender(artifact, sidecar)
		}
		filed = append(filed, deliverable)
	}
	return filed, nil
}

// enqueueStudioRender enqueues one export_pdf job for a filed SHIP artifact, or
// discloses the skip when the sidecar is absent — the graceful degradation the
// spec requires. The kind is server-owned (serverRenderKindForArtifact), so the
// deck flattens and The Talk prints text-native without this caller deciding.
func (app *kanbanBoardApp) enqueueStudioRender(artifact meetingMemoryEntry, sidecar bool) (jobID string, skipNote string) {
	if !sidecar {
		return "", "render sidecar not available — the HTML artifact shipped; export the PDF later from the viewer"
	}
	if !artifactIsHTMLDocument(artifact) && serverRenderKindForArtifact(artifact) == renderJobKindDeck {
		// A deck target must be HTML for chromium to print; a non-HTML deck body
		// is disclosed rather than enqueued into a job nothing can render.
		return "", "the deck artifact is not self-contained HTML — nothing for the render runner to print"
	}
	kind := serverRenderKindForArtifact(artifact)
	printHTML := artifact.Text
	if strings.TrimSpace(artifact.Metadata[deckSceneRefMetadataKey]) != "" {
		expanded, expandErr := artifactRenderBody(artifact)
		if expandErr != nil {
			return "", "render export enqueue failed: deck images could not be prepared"
		}
		printHTML = string(expanded)
	}
	job, err := enqueueRenderExportPDFJob(artifact.ID, kind, printHTML, artifact.Metadata["title"])
	if err != nil {
		return "", "render export enqueue failed: " + compactAssistantLine(err.Error())
	}
	if _, _, err := app.memory.updateOSArtifactMetadata(artifact.ID, map[string]string{
		"renderJobId":  job.ID,
		"renderStatus": renderJobStatusQueued,
		"renderKind":   kind,
	}); err != nil {
		log.Errorf("packaging_studio ship: renderJobId stamp on %s failed: %v", artifact.ID, err)
	}
	return job.ID, ""
}
