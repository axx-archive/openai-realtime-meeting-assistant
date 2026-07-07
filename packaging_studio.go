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

// studioFounderWordsLaw is the standing instruction spliced into every
// downstream stage: the founder's verbatim words captured at INTAKE (and
// carried in the goal objective) are law — quoted, never paraphrased. It is
// the mechanism behind "the founder's words are LAW downstream: quote them in
// every gate".
const studioFounderWordsLaw = "The founder's verbatim words from the INTAKE brief (and the goal objective) are LAW: quote them exactly, never paraphrase them, and never contradict them."

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
		{Name: "cultural_moment", System: "You are a narrative architect building the spine around the CULTURAL MOMENT: why the world is ready for this now, what shift makes it inevitable. Write a complete, distinctive narrative spine (the slide-by-slide argument). Quote the founder's verbatim words. Make it genuinely different from a franchise or founder-conviction angle."},
		{Name: "franchise_playbook", System: "You are a narrative architect building the spine around the FRANCHISE PLAYBOOK: the durable, expandable machine — the universe, the flywheel, the second and third act the first success unlocks. Write a complete, distinctive narrative spine. Quote the founder's verbatim words."},
		{Name: "founder_conviction", System: "You are a narrative architect building the spine around FOUNDER CONVICTION: the earned insight, the why-this-team, the thing they see that others do not. Write a complete, distinctive narrative spine. Quote the founder's verbatim words."},
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
// carries the house seat. The stage bodies splice studioFounderWordsLaw so the
// founder's verbatim words are quoted at every downstream stage, and the
// InputFrom chains carry the INTAKE brief forward so the gate re-reads them.
func packagingStudioDefinition() ProcessDefinition {
	return ProcessDefinition{
		ID:          packagingStudioProcessID,
		Version:     1,
		Title:       "Packaging Studio",
		Description: "Take a venture from a founder's words to a gated, presenter-ready deck — red-team, rival narrative competition, identity, closed-loop gate, speechwriter, and a founder pass, shipped as an attacked-and-documented package.",
		Group:       toolGroupProcesses,
		Authority:   toolAuthorityWorkspaceWrite,
		// 14 stages + headroom; the free-form cap (6) never applies to an authored
		// pipeline. Tokens/wall-clock raised for a long adversarial run.
		Budgets: ProcessBudgets{MaxSubtasks: 16, MaxTokens: 48000, WallClock: 20 * time.Minute},
		Stages: []ProcessStage{
			{
				ID:    "intake",
				Title: "Intake — the founder's words, the audience, the assets",
				Role:  processRoleHumanCheckpoint,
				CheckpointSpec: &ProcessCheckpointSpec{
					Question: "Confirm the intake brief before the studio runs: the sources, the founder's VERBATIM words (these are law downstream), and the real audience. Do brand assets (logo, colors, type) already exist, or should the studio develop a visual identity?",
					Options: []ProcessCheckpointOption{
						{Label: "brand assets provided"},
						{Label: "no brand assets — develop identity"},
					},
				},
			},
			{
				ID:        "red_team",
				Title:     "Red-team — the hostile room, with teeth",
				Role:      processRolePanel,
				InputFrom: []string{"intake"},
				Personas:  studioRedTeamPersonas(),
				PromptBody: strings.Join([]string{
					"Attack the venture as the hostile room it will actually face. " + studioFounderWordsLaw,
					"Produce an objection ledger: the objections that would sink the meeting, each tied to a SPECIFIC weakness — generic clichés fail.",
					"CONTRACTUAL: name strengths_to_keep — what already works and must survive every downstream revision. The synthesis carries both the objections and the strengths_to_keep list forward.",
				}, "\n"),
				OutputContract: "objection_ledger_v1",
			},
			{
				ID:        "identity",
				Title:     "Identity — develop the visual system, or disclose the skip",
				Role:      processRoleJudges,
				InputFrom: []string{"intake", "red_team"},
				Personas:  studioIdentityJudges(),
				PromptBody: strings.Join([]string{
					"Read the INTAKE choice. TWO BRANCHES, pick by what INTAKE declared:",
					"- If INTAKE says 'brand assets provided': DISCLOSE A SKIP — state in one short paragraph that a client identity exists, that the deck chassis recolors to it, and that no identity competition was run. Do not invent directions.",
					"- If INTAKE says 'no brand assets — develop identity': run the competition. Propose 2-3 RIVAL visual directions (each a token set + a type pairing + a duotone treatment) described in copy for the SAME 2-3 sample slides, judge them, and pick a WINNER. State the winner's tokens explicitly — they feed WRITE and SHIP's deck chassis.",
					studioFounderWordsLaw,
				}, "\n"),
				OutputContract: "identity_direction_v1",
			},
			{
				ID:        "compete_architects",
				Title:     "Compete — three rival narrative architects",
				Role:      processRolePanel,
				InputFrom: []string{"intake", "red_team"},
				Personas:  studioCompeteArchitects(),
				PromptBody: strings.Join([]string{
					"Each architect writes a COMPLETE, genuinely distinct narrative spine (the slide-by-slide argument) from their assigned angle. " + studioFounderWordsLaw,
					"Respect the red_team's strengths_to_keep; do not re-introduce a sunk objection. The synthesis presents all three spines side by side for judging.",
				}, "\n"),
				OutputContract: "narrative_spines_v1",
			},
			{
				ID:        "compete_judges",
				Title:     "Compete — judge the spines, steal the best beats",
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
				Title:     "Compete — the choice card (human overrule)",
				Role:      processRoleHumanCheckpoint,
				InputFrom: []string{"compete_judges"},
				CheckpointSpec: &ProcessCheckpointSpec{
					Question:    "The judges scored the rival spines and named the beats to steal. Confirm the winning angle, or overrule it — before WRITE spends tokens.",
					OptionsFrom: "compete_judges",
				},
			},
			{
				ID:        "write",
				Title:     "Write — graft the winning spine",
				Role:      processRoleSynthesizer,
				InputFrom: []string{"intake", "red_team", "identity", "compete_architects", "compete_judges", "compete_choice"},
				PromptBody: strings.Join([]string{
					"Write the deck copy: the CHOSEN spine (compete_choice) as the backbone, with the judges' best_beats_to_steal grafted in, honoring the red_team's strengths_to_keep as a CONTRACT — every one survives.",
					"Write in a spoken register. NO em dashes in any client-facing line (the engine's law sweep enforces this). Use the winning identity's tokens where the copy references look and feel.",
					studioFounderWordsLaw,
				}, "\n"),
				OutputContract: "deck_copy_v1",
			},
			{
				ID:        "gate",
				Title:     "Gate — the personas re-review, objections in hand",
				Role:      processRoleGate,
				InputFrom: []string{"write", "red_team"},
				PromptBody: strings.Join([]string{
					"Score the deck copy against the RED-TEAM's round-1 objection ledger (red_team), the closed loop generalized.",
					"Rubric dimensions: Objections answered (each round-1 objection is verifiably addressed, not ignored), Strengths kept (every strengths_to_keep entry survives), Spine integrity (the chosen angle and grafted steals cohere), Copy law (spoken register, no em dashes, no unearned hype).",
					"A dimension scores low when its objections remain open. " + studioFounderWordsLaw,
				}, "\n"),
				// The SKILL semantics: 9.0 threshold, 7.0 floor, 2 rounds,
				// force-accept below threshold ships with the gaps DISCLOSED (always
				// a human's call downstream), never blocks silently.
				GateSpec: &ProcessGateSpec{Threshold: 9.0, Floor: 7.0, MaxRounds: 2, ForceAccept: true},
			},
			{
				ID:        "voice",
				Title:     "Voice — the speechwriter's per-page script",
				Role:      processRoleWriter,
				Mode:      "artifacts",
				InputFrom: []string{"write", "gate"},
				PromptBody: strings.Join([]string{
					"Write the presenter script: for EACH deck page, a 25-45 second spoken script with exactly one [BEAT] marking the pause.",
					"Weave the founder's VERBATIM phrases into the spoken lines. " + studioFounderWordsLaw,
					"INTERLOCK RULE: the VOICE owns the parables and the emotional turns; the SLIDE owns the numbers. Never put a figure in the script that is not on its slide, and never make the slide carry a story the voice should tell.",
				}, "\n"),
				OutputContract: "presenter_script_v1",
			},
			{
				ID:        "founder_pass",
				Title:     "Founder pass — read the gated draft, mark do_not_touch",
				Role:      processRoleHumanCheckpoint,
				InputFrom: []string{"write", "voice", "gate"},
				CheckpointSpec: &ProcessCheckpointSpec{
					Question: "The gated draft and its presenter script are ready. Read them and decide: ship as-is, or send back — and mark any lines as do_not_touch so SHIP preserves them exactly. This is the taste pass.",
					// The labels tell the truth (the checkpoint-option teeth): a
					// send-back mechanically re-queues WRITE with the founder's
					// words as revision notes; ship-as-is proceeds.
					Options: []ProcessCheckpointOption{
						{Label: "ship as-is"},
						{Label: "send back for changes", Action: processCheckpointActionRevise, Target: "write"},
					},
				},
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
				Title:     "Imagery direction — where an image earns the beat",
				Role:      processRoleWriter,
				Mode:      "artifacts",
				InputFrom: []string{"identity", "write", "voice", "founder_pass"},
				PromptBody: strings.Join([]string{
					"You are the ART DIRECTOR for this packaging deck. You decide WHERE a photographic image earns an emotional beat that drives consensus / talent / capital, and WHERE its absence is stronger. You direct imagery; you do NOT generate it and you do NOT write the deck.",
					"Read the chosen narrative (WRITE + VOICE) page by page and the IDENTITY visual system. Imagery is EDITORIAL, never decoration: an image must do a job type and numbers cannot. If the story is carried by type and evidence, direct FEWER images or NONE — a deliberately typographic package is a valid, strong output.",
					"Honor the deck chassis laws VERBATIM: at most ~5 full-bleeds in the whole deck; at most 6 images total; EXACTLY ONE crescendo image at the deck's peak (its treatment note names it, the deck renders it at --heat:.45); ledger / numbers ('bone') pages carry NO imagery; one FIG. per photo plate. The duotone/heat treatment is applied later in the deck CSS, so describe each shot in NATURAL color and real subjects — never a brand-color wash, never invented geography.",
					"Name each shot's emotional temperature explicitly (drama, joy, awe, resolve, ...). When the PLACE is the claim, name the real place.",
					"Output EXACTLY ONE fenced ```json block and NOTHING else, of this shape:",
					"```json\n{\n  \"strategy\": \"one paragraph: where images earn a beat and where absence is stronger\",\n  \"visual_system\": \"the ONE visual-system brief, tied to the identity tokens, that rides every shot\",\n  \"shots\": [\n    { \"fig\": 1, \"slot\": \"bleed|plate\", \"subject\": \"what the image depicts (natural color, honest geography)\", \"composition\": \"framing, eyeline, scale\", \"temperature\": \"the NAMED emotional temperature\", \"treatment\": \"how it ties to the visual system; say if THIS is the one crescendo\", \"aspect\": \"landscape|portrait|square\", \"caption\": \"the FIG. caption line\", \"place\": \"real place by name when the place is the claim, else empty\", \"why\": \"the emotional job this image does\" }\n  ]\n}\n```",
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
				Title:      "Imagery — generate the directed shots",
				Role:       processRoleCompile,
				InputFrom:  []string{"imagery_direction"},
				PromptBody: "Deterministic generation step: read the imagery_direction shot list and generate each directed shot on the one visual system via the OpenAI image API, filing the results as {kind:image} assets. Per-shot failure is disclosed and skipped; keyless or zero shots ships the package typographic. Authored Go — never a model call.",
				Compile:    compilePackagingStudioImagery,
			},
			{
				ID:        "ship_deck",
				Title:     "Ship — the self-contained presenter deck",
				Role:      processRoleWriter,
				Mode:      "artifacts",
				InputFrom: []string{"write", "voice", "founder_pass", "imagery_direction", "imagery_generate"},
				PromptBody: strings.Join([]string{
					"Produce the deck as ONE self-contained HTML file: all CSS and JS inline, no external references — the ONLY URLs permitted anywhere are data: URIs (used for any embedded imagery). Start with <!doctype html>.",
					"Build the deck on the REQUIRED print chassis. Include this exact <style> block verbatim in <head>, lay every slide out as a <section class=\"pg\">…</section> inside a single <div id=\"stage\">…</div>, and give the FIRST slide the extra class \"on\". NEVER remove or weaken the @page or @media print rules — they are what make the exported PDF contain EVERY slide instead of only the first one:",
					"<style>\n" + strings.TrimSpace(packagingDeckChassisCSS) + "\n</style>",
					"Layer all brand aesthetics (colors, type, furniture) ON TOP of this chassis; do not fight its geometry (the 1920x1080 #stage, the .pg slide model).",
					"IMAGERY: place each FIG the imagery_generate record lists as GENERATED at the slide the imagery_direction assigned. Build that slide's photo element as a plate or full-bleed carrying BOTH its type class AND class \"fig-N\" (matching the FIG number), with an empty <div class=\"ph\"></div> inside and the FIG. caption. Do NOT paste any image data or invent src/url values — the image bytes are inlined at compile as a data: URI onto .fig-N .ph. Add a fig-N slot ONLY for FIG numbers the generation record generated; if imagery was skipped or zero, build a deliberately typographic deck with no photo plates.",
					"Embed presenter mode driven by VOICE's per-page script (the [BEAT] pauses and the spoken lines), so opening the file and pressing present gives the founder the script alongside each page.",
					"Honor every founder_pass do_not_touch line exactly. Keep client-facing copy free of em dashes. " + studioFounderWordsLaw,
				}, "\n"),
				OutputContract: packagingStudioDeckContract,
			},
			{
				ID:        "ship_compile",
				Title:     "Ship — compile the five-artifact package",
				Role:      processRoleCompile,
				InputFrom: []string{"red_team", "write", "gate", "voice", "founder_pass", "ship_deck"},
				// Documentation only — compile is authored Go (below), never a
				// model call. The flatten law stays server-owned: the compiler
				// stamps paperKit and serverRenderKindForArtifact picks the kind.
				PromptBody: "Deterministic compile step: file the five interlocking artifacts (deck html_deck, The Wall, The Talk with paperKit=true, rigor companion, findings record aggregated from the run's actual verdicts), attach every one to the venture package, and enqueue the render exports — the deck flattened, The Talk text-native — or disclose the skips when the sidecar is absent.",
				Compile:    compilePackagingStudioShip,
			},
			{
				ID:        "slide_jury",
				Title:     "Slide jury — the critics see the rendered pages",
				Role:      processRoleCompile,
				InputFrom: []string{"ship_compile"},
				// Documentation only — the jury stage is authored Go (below). It is
				// ADVISORY: findings land as revision notes on the findings record,
				// never as an auto-revise; keyless / sidecar-absent / export-timeout
				// all disclose a skip and the ship proceeds to its approval.
				PromptBody: "Vision jury step: once the deck's PDF export completes, the render-runner's page JPEGs go before the /packaging jury trio (headline ear, design eye, the domain-literate room gut) — each seat sees ALL pages, scores per page, names weakest_three/strongest_three, and every fix is executable or the literal word KEEP. The merged scoreboard files as slide_jury_v1 and lands as revision notes on the findings record; the founder decides what to apply. Sidecar absent or export incomplete: the skip is disclosed.",
				Compile:    compilePackagingStudioSlideJury,
			},
			{
				ID:        "ship_approval",
				Title:     "Ship approval — the package leaves the building",
				Role:      processRoleHumanCheckpoint,
				InputFrom: []string{"ship_compile", "slide_jury", "ship_deck"},
				CheckpointSpec: &ProcessCheckpointSpec{
					Question: "The five interlocking artifacts are filed and attached to the package — the deck, The Wall, The Talk, the rigor companion, and the findings record — with the render exports queued or their skips disclosed, and the slide jury's scoreboard (or its disclosed skip) on the findings record. Approve the ship, or hold the package.",
					Options: []ProcessCheckpointOption{
						{Label: "approve the ship"},
						// The first live run proved a bad deck can reach this park
						// with no way back short of holding forever: send-back
						// re-queues ship_deck and cascade re-runs compile + jury.
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
	deckCopy := stageBody("write")
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
		"Every client-facing line of the gated deck copy, on the record. No em dashes in a client-facing line; the founder's verbatim words are quoted, never paraphrased.",
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
		"The diligence trail behind the deck: what the hostile room said, what the gate verified, and what the founder locked.",
		"",
		"## The round-1 objection ledger (red team)",
		ledger,
		"",
		"## The gate's decision, objections in hand",
		gateRecord,
		"",
		"## The founder pass",
		founderPass,
	}, "\n")

	deckTitle := "Packaging Studio deck"
	if parent, ok := app.osArtifactByID(parentID); ok {
		if title := strings.TrimSpace(parent.Metadata["title"]); title != "" {
			deckTitle = title + " — presenter deck"
		}
	}

	filed, err := app.fileStudioShipDeliverables(studioShipInputs{
		GoalID:    parentID,
		PackageID: plan.PackageID,
		CreatedBy: plan.CreatedBy,
		DeckHTML:  deckHTML,
		Wall:      wall,
		Talk:      talk,
		Rigor:     rigor,
		Findings:  composeStudioFindingsRecord(app, plan, parentID),
		DeckTitle: deckTitle,
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
		}, "\n"), map[string]string{"slideJury": "skipped"}, nil
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
	if !hasAnthropicAPIKey() {
		return skip("no Anthropic key is configured (keyless deploy) — the jury seats cannot see")
	}

	ctx, cancel := context.WithTimeout(context.Background(), orchestratorTimeout())
	defer cancel()
	jury, err := runSlideJury(ctx, app, parentID, deck)
	if err != nil {
		// Advisory stage: a failed panel is disclosed, never a blocked ship.
		return skip("the jury panel failed: " + compactAssistantLine(err.Error()))
	}

	findingsNote := appendSlideJuryRevisionNotes(app, findings, jury)

	lines := []string{
		"Slide jury — the critics saw the rendered pages",
		"",
		fmt.Sprintf("- %d rendered page image(s) went before the 3-seat jury (headline ear, design eye, room gut) — every seat saw all pages.", len(artifactPageImageAssets(deck))),
		"- Merged scoreboard filed: " + slideJuryContract + " → " + jury.ID,
		"- " + findingsNote,
		"- Advisory by design: revision notes only, no auto-revise — the founder decides what to apply at ship approval.",
	}
	return strings.Join(lines, "\n"), map[string]string{"slideJuryArtifactId": jury.ID}, nil
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
	GoalID    string // the running goal, stamped for provenance
	PackageID string
	CreatedBy string
	DeckHTML  string // ship_deck's self-contained HTML
	Wall      string // the slide-copy record ("The Wall")
	Talk      string // the branded one-sheet ("The Talk") — text-native, paperKit
	Rigor     string // the diligence companion
	Findings  string // the findings audit trail (every panel/gate/jury verdict)
	DeckTitle string
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
	for _, entry := range app.osArtifactsSnapshot(0) {
		if entry.Metadata["source"] == "packaging_studio_ship" && entry.Metadata["goalId"] == goalID && entry.Metadata["artifactContract"] == contract {
			return entry, true
		}
	}
	return meetingMemoryEntry{}, false
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
		if existing, found := app.studioShipDeliverableByContract(in.GoalID, spec.contract); found {
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
	job, err := enqueueRenderExportPDFJob(artifact.ID, kind, artifact.Text, artifact.Metadata["title"])
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
