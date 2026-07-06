package main

// The 12-tool packaging suite as data (Spectacular OS domain §1). A tool is a
// goal preset: selecting it opens the /goal composer pre-filled with a tuned
// objective and this tool's prompt template, then runs the one /goal loop. The
// registry is the single source of taxonomy — the palette (Wave 11), the /goal
// text parser, Scout's initiate_goal, and the goal engine all read it, so the
// menu lives in exactly one place.
//
// The prose bodies + master wrapper live in tool_prompts.go; this file holds the
// machine-checkable metadata (group, stage mapping, authority, structured gate
// rubric) that the evals gate on and the engine consumes when a goal carries a
// toolTemplate.

import (
	"net/http"
	"strings"
)

// Lifecycle groups, ordered ideate -> package -> market -> portfolio so the
// palette reads as the studio workflow (domain §1.2). This order is load-bearing
// for the GET /assistant/tools payload.
const (
	toolGroupIdeate    = "ideate"
	toolGroupPackage   = "package"
	toolGroupMarket    = "market"
	toolGroupPortfolio = "portfolio"
	// toolGroupProcesses is the fifth, additive payload group (Wave 4 item 17):
	// authored ProcessDefinitions served beside the 12 tools so every door —
	// palette, /goal, voice, the router's propose_tool_run — reaches a process
	// by id exactly the way it reaches a tool. The 12 tools never live here.
	toolGroupProcesses = "processes"
)

// toolGroupOrder is the canonical ordering of the 12-TOOL lifecycle groups.
// The processes group is not a tool group: buildToolsPayload appends it after
// these four, from the process registry, so this list keeps meaning "where do
// packagingTools live" for every existing consumer.
var toolGroupOrder = []string{toolGroupIdeate, toolGroupPackage, toolGroupMarket, toolGroupPortfolio}

var toolGroupLabels = map[string]string{
	toolGroupIdeate:    "Ideate",
	toolGroupPackage:   "Package",
	toolGroupMarket:    "Market",
	toolGroupPortfolio: "Portfolio",
	// "End-to-end" (Wave A item 4): the processes group is the company's
	// flagship lane (packaging_studio), so it leads the payload under this label
	// instead of the trailing "Processes" it once carried. The label flows to
	// the palette section header AND to every process proposal's GroupLabel
	// (scoutRouterProposalForToolID reads this same map), so the two never drift.
	toolGroupProcesses: "End-to-end",
}

// Input modes: a form tool collects 1-3 typed fields before launch; a
// conversational tool prefills the composer and lets the user talk it out.
const (
	toolInputForm           = "form"
	toolInputConversational = "conversational"
)

// Authority classes mirror the codex authority ladder the /goal engine clamps
// on. external_write is never a tool property — it is earned at the ship gate.
// externalWriteGated marks the memo/deal-room class whose shipping crosses the
// building boundary and therefore forces the approval gate even though the tool
// itself launches at workspace_write.
const (
	toolAuthorityReadOnly       = "read_only"
	toolAuthorityWorkspaceWrite = "workspace_write"
)

// toolFormField is one labeled input a form tool collects (domain: 1-3 per tool).
type toolFormField struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Placeholder string `json:"placeholder,omitempty"`
	Required    bool   `json:"required"`
}

// toolRubricDimension is one scored axis of a gate rubric (domain §2.2): a name,
// what it measures, and the minimum bar (1-10) to ship.
type toolRubricDimension struct {
	Name     string `json:"name"`
	Measures string `json:"measures"`
	Bar      int    `json:"bar"`
}

// toolRubric is the per-tool gate contract: 3-5 scored dimensions plus one kill
// condition that auto-fails regardless of the other scores. The engine's
// review/gate steps run this against the original goal statement.
type toolRubric struct {
	Ref           string                `json:"ref"`
	Dimensions    []toolRubricDimension `json:"dimensions"`
	KillCondition string                `json:"killCondition"`
}

// packagingTool is one entry on the 12-tool menu.
type packagingTool struct {
	ID                 string          `json:"id"`
	Group              string          `json:"group"`
	Name               string          `json:"name"`
	Promise            string          `json:"promise"`
	Stages             []string        `json:"stages"`
	Mode               string          `json:"mode"`     // base agent-thread mode the contract rides on
	Contract           string          `json:"contract"` // output-contract id (research_brief_v2, one_pager_v1, ...)
	InputMode          string          `json:"inputMode"`
	FormFields         []toolFormField `json:"formFields,omitempty"`
	Authority          string          `json:"authority"`
	ExternalWriteGated bool            `json:"externalWriteGated"`
	// ClientFacing marks contracts whose output is copy a client/investor reads
	// (one-pager, deck outline, update memo, package binder). The engine's
	// deterministic law sweeps (toolLawSweep) enforce the packaging copy laws —
	// no em dashes — on exactly this class, so the list lives here as data, not
	// hardcoded in the engine.
	ClientFacing bool       `json:"clientFacing"`
	Rubric       toolRubric `json:"rubric"`
}

// KillCondition is the one-sentence non-negotiable, surfaced for quick access
// (it also lives inside Rubric).
func (t packagingTool) KillCondition() string { return t.Rubric.KillCondition }

// packagingTools returns the definitive 12-tool menu in group order. Constructed
// fresh each call (small, cheap) so no caller can mutate the shared slice.
func packagingTools() []packagingTool {
	return []packagingTool{
		// ---- Group A: Ideate --------------------------------------------------
		{
			ID:        "deep_research",
			Group:     toolGroupIdeate,
			Name:      "Deep Research",
			Promise:   "Bring back the ground truth on any question, with receipts.",
			Stages:    []string{"research"},
			Mode:      "research",
			Contract:  "research_brief_v2",
			InputMode: toolInputConversational,
			Authority: toolAuthorityReadOnly,
			Rubric: toolRubric{
				Ref: "research_brief_gate_v1",
				Dimensions: []toolRubricDimension{
					{Name: "Grounding", Measures: "every non-obvious claim has a source or memory cite", Bar: 8},
					{Name: "Counter-case", Measures: "the strongest opposing view is present and fair", Bar: 7},
					{Name: "Actionability", Measures: "a partner could decide from the Recommendation", Bar: 7},
					{Name: "Recency honesty", Measures: "decaying facts are dated or flagged stale", Bar: 8},
				},
				KillCondition: "any invented/unverifiable source, or a claim asserted as fact that is actually the agent's assumption.",
			},
		},
		{
			ID:        "comps_precedent",
			Group:     toolGroupIdeate,
			Name:      "Comps & Precedent",
			Promise:   "What has this idea's shape sold for, and to whom?",
			Stages:    []string{"thesis", "research"},
			Mode:      "research",
			Contract:  "research_brief_v2",
			InputMode: toolInputForm,
			FormFields: []toolFormField{
				{Key: "thesis", Label: "IP thesis", Placeholder: "the idea and its shape", Required: true},
				{Key: "format", Label: "Format / medium", Placeholder: "film, series, game, book…", Required: true},
				{Key: "buyers", Label: "Target buyers (optional)", Placeholder: "who might acquire it", Required: false},
			},
			Authority: toolAuthorityReadOnly,
			Rubric: toolRubric{
				Ref: "comps_gate_v1",
				Dimensions: []toolRubricDimension{
					{Name: "Comparability", Measures: "every comp states why it is a fair comp", Bar: 9},
					{Name: "Sourcing", Measures: "each comp names the deal/precedent and its source", Bar: 8},
					{Name: "Valuation honesty", Measures: "a value range with confidence, not false precision", Bar: 7},
					{Name: "Challenge awareness", Measures: "the two most-challengeable comps are named", Bar: 7},
				},
				KillCondition: "a comp asserted without a comparability rationale.",
			},
		},
		{
			ID:        "market_map",
			Group:     toolGroupIdeate,
			Name:      "Market Map",
			Promise:   "Where does this IP sit in its landscape, and where's the whitespace?",
			Stages:    []string{"thesis", "research"},
			Mode:      "research",
			Contract:  "research_brief_v2",
			InputMode: toolInputForm,
			FormFields: []toolFormField{
				{Key: "category", Label: "Category / genre / thesis", Placeholder: "the landscape to map", Required: true},
				{Key: "axes", Label: "Axes to map on (optional)", Placeholder: "e.g. audience × budget", Required: false},
			},
			Authority: toolAuthorityReadOnly,
			Rubric: toolRubric{
				Ref: "market_map_gate_v1",
				Dimensions: []toolRubricDimension{
					{Name: "Boundedness", Measures: "an explicit statement of what was NOT covered", Bar: 8},
					{Name: "Currency", Measures: "named players each carry a last-move date", Bar: 8},
					{Name: "Whitespace", Measures: "the whitespace argument is specific, not generic", Bar: 7},
					{Name: "Demand evidence", Measures: "the demand signals are sourced", Bar: 7},
				},
				KillCondition: "a landscape with no coverage boundary, or players asserted with no last-move date (staleness laundered as completeness).",
			},
		},
		// ---- Group B: Package -------------------------------------------------
		{
			ID:        "one_pager",
			Group:     toolGroupPackage,
			Name:      "One-Pager",
			Promise:   "The single page that makes someone take the meeting.",
			Stages:    []string{"pitch"},
			Mode:      "artifacts",
			Contract:  "one_pager_v1",
			InputMode: toolInputForm,
			FormFields: []toolFormField{
				{Key: "audience", Label: "Target reader", Placeholder: "capital, talent, or a buyer", Required: true},
				{Key: "ask", Label: "The ask", Placeholder: "exactly what you want from them", Required: true},
			},
			Authority:    toolAuthorityWorkspaceWrite,
			ClientFacing: true,
			Rubric: toolRubric{
				Ref: "one_pager_gate_v1",
				Dimensions: []toolRubricDimension{
					{Name: "Receipts", Measures: "every claim maps to a package source in the appendix", Bar: 9},
					{Name: "Reader-fit", Measures: "the lead and the ask match the audience", Bar: 8},
					{Name: "Compression", Measures: "genuinely one page, no filler, every line earns", Bar: 8},
					{Name: "Voice", Measures: "reads like a sharp studio, not a template", Bar: 7},
					{Name: "Candor", Measures: "names real risks/losses plainly; no hedging or hype", Bar: 7},
				},
				KillCondition: "any claim on the page with no receipt in the appendix.",
			},
		},
		{
			ID:        "deck_outline",
			Group:     toolGroupPackage,
			Name:      "Deck Outline",
			Promise:   "The pitch narrative, sequenced — slide by slide, not prose.",
			Stages:    []string{"pitch"},
			Mode:      "artifacts",
			Contract:  "deck_outline_v1",
			InputMode: toolInputForm,
			FormFields: []toolFormField{
				{Key: "audience", Label: "Audience", Placeholder: "who is in the room", Required: true},
				{Key: "length", Label: "Length target (optional)", Placeholder: "e.g. 10-12 slides", Required: false},
				{Key: "beats", Label: "Must-hit beats (optional)", Placeholder: "anything that must appear", Required: false},
			},
			Authority:    toolAuthorityWorkspaceWrite,
			ClientFacing: true,
			Rubric: toolRubric{
				Ref: "deck_outline_gate_v1",
				Dimensions: []toolRubricDimension{
					{Name: "Arc completeness", Measures: "problem → insight → what → why-us → why-now → ask → money slide", Bar: 8},
					{Name: "Evidence per slide", Measures: "each slide names its one job and its evidence", Bar: 8},
					{Name: "One job per slide", Measures: "no slide carries two arguments", Bar: 7},
					{Name: "Reader-fit", Measures: "the sequence is tuned to the named audience", Bar: 7},
				},
				KillCondition: "a missing narrative beat (no problem, no ask, or no money slide), or a slide with no stated evidence.",
			},
		},
		{
			ID:        "brand_design_brief",
			Group:     toolGroupPackage,
			Name:      "Brand & Design Brief",
			Promise:   "The creative north star a designer can build from.",
			Stages:    []string{"design"},
			Mode:      "design",
			Contract:  "design_brief_v1",
			InputMode: toolInputForm,
			FormFields: []toolFormField{
				{Key: "audience", Label: "Audience", Placeholder: "who this is for", Required: true},
				{Key: "tone", Label: "Tone words (optional)", Placeholder: "three words for the feel", Required: false},
				{Key: "references", Label: "References (optional)", Placeholder: "worlds to borrow from", Required: false},
			},
			Authority: toolAuthorityWorkspaceWrite,
			Rubric: toolRubric{
				Ref: "design_brief_gate_v1",
				Dimensions: []toolRubricDimension{
					{Name: "Justification", Measures: "each creative choice cites the research/thesis behind it", Bar: 8},
					{Name: "Research grounding", Measures: "states how a research brief in memory shaped it", Bar: 8},
					{Name: "Completeness", Measures: "intent, screens, states, responsive, handoff all present", Bar: 7},
					{Name: "Buildability", Measures: "a designer could start from it without a meeting", Bar: 7},
				},
				KillCondition: "a creative choice asserted as taste with no research or thesis that justifies it.",
			},
		},
		// ---- Group C: Market --------------------------------------------------
		{
			ID:        "grill_pressure_test",
			Group:     toolGroupMarket,
			Name:      "Grill / Pressure-Test",
			Promise:   "Face the hostile room before the real one.",
			Stages:    []string{"grill"},
			Mode:      "grill",
			Contract:  "grill_scorecard_v2",
			InputMode: toolInputConversational,
			Authority: toolAuthorityWorkspaceWrite,
			Rubric: toolRubric{
				Ref: "grill_gate_v1",
				Dimensions: []toolRubricDimension{
					{Name: "Format", Measures: "READINESS line present and correctly formatted", Bar: 10},
					{Name: "Groundedness", Measures: "objections tie to real package weaknesses, cited", Bar: 8},
					{Name: "Fairness", Measures: "objections are the strongest honest case, not strawmen", Bar: 7},
				},
				KillCondition: "missing/malformed READINESS line, or objections that are generic and not tied to this package.",
			},
		},
		{
			ID:        "rights_chain_of_title",
			Group:     toolGroupMarket,
			Name:      "Rights & Chain-of-Title",
			Promise:   "Who owns what, and what has to be true before we can sell it.",
			Stages:    []string{"research", "pitch"},
			Mode:      "artifacts",
			Contract:  "rights_map_v1",
			InputMode: toolInputForm,
			FormFields: []toolFormField{
				{Key: "underlying", Label: "Underlying work & origin", Placeholder: "the source IP, creators, prior agreements", Required: true},
				{Key: "encumbrances", Label: "Known encumbrances (optional)", Placeholder: "options, liens, prior grants", Required: false},
			},
			Authority: toolAuthorityWorkspaceWrite,
			Rubric: toolRubric{
				Ref: "rights_map_gate_v1",
				Dimensions: []toolRubricDimension{
					{Name: "Confirmed vs assumed", Measures: "every right is labeled confirmed (sourced) or assumed (flagged)", Bar: 9},
					{Name: "Conservatism", Measures: "errs toward flagging a gap over asserting a right", Bar: 8},
					{Name: "Encumbrance coverage", Measures: "known encumbrances are all accounted for", Bar: 7},
					{Name: "Diligence clarity", Measures: "the open questions that must close before a deal are explicit", Bar: 7},
				},
				KillCondition: "an assumed right presented as confirmed.",
			},
		},
		{
			ID:        "economics_waterfall",
			Group:     toolGroupMarket,
			Name:      "Economics / Waterfall",
			Promise:   "Where does the money go, and does the deal work for us?",
			Stages:    []string{"pitch", "grill"},
			Mode:      "artifacts",
			Contract:  "economics_scan_v1",
			InputMode: toolInputForm,
			FormFields: []toolFormField{
				{Key: "structure", Label: "Deal structure", Placeholder: "how the deal is shaped", Required: true},
				{Key: "assumptions", Label: "Cost / revenue assumptions (optional)", Placeholder: "the numbers you have", Required: false},
				{Key: "ask", Label: "The ask (optional)", Placeholder: "what you're raising / seeking", Required: false},
			},
			Authority: toolAuthorityWorkspaceWrite,
			Rubric: toolRubric{
				Ref: "economics_scan_gate_v1",
				Dimensions: []toolRubricDimension{
					{Name: "Labeled assumptions", Measures: "every input assumption is labeled and sourced", Bar: 9},
					{Name: "Sensitivity", Measures: "states what breaks the deal, not one hero number", Bar: 8},
					{Name: "Structure", Measures: "sources & uses and the waterfall order are clear", Bar: 8},
					{Name: "Clarity", Measures: "a CFO could sanity-check it in plain language", Bar: 7},
				},
				KillCondition: "false precision — a single hero number without labeled, sourced assumptions and stated sensitivities.",
			},
		},
		{
			ID:        "talent_match",
			Group:     toolGroupMarket,
			Name:      "Talent Match",
			Promise:   "Who should be attached, and what's the realistic path to yes.",
			Stages:    []string{"pitch"},
			Mode:      "research",
			Contract:  "research_brief_v2",
			InputMode: toolInputForm,
			FormFields: []toolFormField{
				{Key: "role", Label: "Role / creative need", Placeholder: "showrunner, director, lead…", Required: true},
				{Key: "constraints", Label: "Constraints (optional)", Placeholder: "budget tier, timing", Required: false},
			},
			Authority: toolAuthorityReadOnly,
			Rubric: toolRubric{
				Ref: "talent_match_gate_v1",
				Dimensions: []toolRubricDimension{
					{Name: "Specific rationale", Measures: "each name has a comparable credit / stated interest / relationship path", Bar: 9},
					{Name: "Realism", Measures: "availability and reach realism are stated per name", Bar: 8},
					{Name: "Path to contact", Measures: "a concrete route to reach each name", Bar: 7},
					{Name: "Fit grounding", Measures: "the slate is ranked against the package's actual need", Bar: 7},
				},
				KillCondition: "a name with no specific rationale (generic \"get a big star\" filler) or no availability/reach realism.",
			},
		},
		// ---- Group D: Portfolio ----------------------------------------------
		{
			ID:        "package_assembly",
			Group:     toolGroupPortfolio,
			Name:      "Package Assembly",
			Promise:   "Compile everything we've made into the document we actually send.",
			Stages:    []string{"assembled"},
			Mode:      "workflow",
			Contract:  "package_binder_v1",
			InputMode: toolInputForm,
			FormFields: []toolFormField{
				{Key: "recipient", Label: "Target recipient", Placeholder: "investor, talent's team, buyer", Required: true},
			},
			Authority:    toolAuthorityWorkspaceWrite,
			ClientFacing: true,
			Rubric: toolRubric{
				Ref: "package_assembly_gate_v1",
				Dimensions: []toolRubricDimension{
					{Name: "Reconciliation", Measures: "contradictions between source artifacts are reconciled or flagged", Bar: 9},
					{Name: "Provenance", Measures: "every section traces to the attached artifact it came from", Bar: 8},
					{Name: "One voice", Measures: "reads as one coherent document, not a stapled PDF", Bar: 7},
					{Name: "Completeness", Measures: "assembles only published/attached artifacts, none missing", Bar: 7},
				},
				KillCondition: "an unreconciled contradiction between source artifacts shipped silently.",
			},
		},
		{
			ID:                 "investor_update_memo",
			Group:              toolGroupPortfolio,
			Name:               "Investor-Update Memo",
			Promise:            "The portfolio report a chief of staff would write, sourced from what actually happened.",
			Stages:             []string{"assembled"},
			Mode:               "artifacts",
			Contract:           "update_memo_v1",
			InputMode:          toolInputForm,
			FormFields:         []toolFormField{{Key: "window", Label: "Time window", Placeholder: "e.g. the last two weeks", Required: true}, {Key: "recipient", Label: "Recipient", Placeholder: "LP or internal", Required: true}},
			Authority:          toolAuthorityWorkspaceWrite,
			ExternalWriteGated: true,
			ClientFacing:       true,
			Rubric: toolRubric{
				Ref: "update_memo_gate_v1",
				Dimensions: []toolRubricDimension{
					{Name: "Traceability", Measures: "every development traces to a decision, meeting, artifact, or stage-advance", Bar: 9},
					{Name: "Approval discipline", Measures: "it stops for human approval before it can leave the building", Bar: 8},
					{Name: "Forwardable voice", Measures: "reads like a memo an LP could receive unedited", Bar: 7},
					{Name: "Completeness", Measures: "what moved / decisions / what's next / what we need all present", Bar: 7},
					{Name: "Candor", Measures: "names real risks/losses plainly; no hedging or hype", Bar: 7},
				},
				KillCondition: "any stated development not traceable to a decision, meeting, artifact, or package stage-advance on record.",
			},
		},
	}
}

// imageryBoardTool is the Wave 5 imagery stage's registry entry — registered
// HIDDEN-UNTIL-PROVEN (the Hidden-ProcessDefinition precedent), and for now
// deliberately UNREACHABLE from every launch door: it does NOT resolve through
// toolByID and does NOT join packagingTools(). A toolByID launch would run the
// generic text-model pipeline against imageryBoardBody — the model would file
// an imagery_board_v1 artifact with a fabricated "Generation record" and
// invented blob refs while generating NOTHING (runImageryBoard, the actual
// generator in openai_images.go, has no launch path yet), violating the
// imagery law's honesty core. The registry entry exists so the contract,
// rubric, and prompt stay pinned by tests; wiring toolByID (routing the launch
// through runImageryBoard) is the promotion move once the standalone output
// earns it. Group: package — where it will surface when proven.
func imageryBoardTool() packagingTool {
	return packagingTool{
		ID:        "imagery_board",
		Group:     toolGroupPackage,
		Name:      "Imagery Board",
		Promise:   "Art-directed concept renders on one visual system, filed with receipts.",
		Stages:    []string{"design"},
		Mode:      "design",
		Contract:  "imagery_board_v1",
		InputMode: toolInputForm,
		FormFields: []toolFormField{
			{Key: "visualSystem", Label: "Visual system brief", Placeholder: "the one style block every shot carries", Required: true},
			{Key: "shots", Label: "Shot descriptions", Placeholder: "4-6 shots, one per line, each naming its emotional temperature", Required: true},
		},
		Authority: toolAuthorityWorkspaceWrite,
		Rubric: toolRubric{
			Ref: "imagery_board_gate_v1",
			Dimensions: []toolRubricDimension{
				{Name: "One system", Measures: "every shot prompt carries the identical visual system block", Bar: 9},
				{Name: "Temperature", Measures: "every shot names its emotional temperature and it matches the argument", Bar: 8},
				{Name: "Place honesty", Measures: "real places are asked for by name when the place is the claim; geography is never invented", Bar: 8},
				{Name: "Labeling", Measures: "every generated image carries a \"concept render\" FIG-caption label", Bar: 8},
			},
			KillCondition: "invented or relocated geography, or a generated image presented without its \"concept render\" label.",
		},
	}
}

// toolByID resolves a tool template id to its registry entry. Unknown ids return
// ok=false so callers treat a stray toolTemplate as a plain goal (graceful
// degradation — never an error).
func toolByID(id string) (packagingTool, bool) {
	id = strings.TrimSpace(strings.ToLower(id))
	if id == "" {
		return packagingTool{}, false
	}
	for _, tool := range packagingTools() {
		if tool.ID == id {
			return tool, true
		}
	}
	// imagery_board (imageryBoardTool) deliberately does NOT resolve here:
	// until its launch is wired to runImageryBoard, resolving it would execute
	// the tool as a generic model-written artifact that fabricates a generation
	// record for images that never existed. Unknown stays unknown.
	return packagingTool{}, false
}

// normalizeToolTemplate returns the canonical tool id if it resolves, else "".
func normalizeToolTemplate(id string) string {
	if tool, ok := toolByID(id); ok {
		return tool.ID
	}
	return ""
}

// toolsPayload is the GET /assistant/tools body: tools grouped in lifecycle
// order for the palette (Wave 11). Groups carry their display label; the palette
// renders them left to right as the studio workflow.
type toolsPayloadGroup struct {
	ID    string          `json:"id"`
	Label string          `json:"label"`
	Tools []packagingTool `json:"tools"`
}

func buildToolsPayload() []toolsPayloadGroup {
	byGroup := map[string][]packagingTool{}
	for _, tool := range packagingTools() {
		byGroup[tool.Group] = append(byGroup[tool.Group], tool)
	}
	// FLAGSHIP FIRST (Wave A item 4): the authored processes group leads the
	// payload under the "End-to-end" label, because packaging_studio is the
	// company's headline capability and used to render dead last, below all 12
	// instruments — teaching the wrong company. Hidden processes (test proofs
	// like process_probe) stay launchable by id but never serve here, so they
	// are absent from the palette AND from the router's injected enum.
	//
	// Consumers do not key on group ORDER (verified: the palette renders
	// straight off the payload and glyphs key on group id; the router enum only
	// needs the ids present, not positioned). Moving processes first therefore
	// only changes what renders first and a free nudge on the enum order.
	processes := toolsPayloadGroup{ID: toolGroupProcesses, Label: toolGroupLabels[toolGroupProcesses], Tools: []packagingTool{}}
	for _, def := range processDefinitions() {
		if def.Hidden {
			continue
		}
		processes.Tools = append(processes.Tools, processPaletteEntry(def))
	}
	groups := make([]toolsPayloadGroup, 0, len(toolGroupOrder)+1)
	groups = append(groups, processes)
	for _, id := range toolGroupOrder {
		// Registry order within a group is already the intended display order.
		// The four lifecycle groups keep their internal order and their full
		// 12-tool menu; only their position (after processes) moved.
		groups = append(groups, toolsPayloadGroup{ID: id, Label: toolGroupLabels[id], Tools: byGroup[id]})
	}
	return groups
}

// assistantCapabilitiesDigestMaxChars caps the self-knowledge block so it can
// never bloat the per-turn chat prompt. The golden test pins current length well
// under this; it rides every keyed answer turn (~300 net tokens, ≈$1 per 1,000
// turns), so a future capability with a runaway promise fails CI here first.
const assistantCapabilitiesDigestMaxChars = 2400

// assistantCapabilitiesDigest renders the compact self-knowledge block the chat
// answer brain reads so it can name what this workspace CAN do instead of
// dead-ending on denial (the 2026-07-05 sim: Scout called packaging "a bigger
// ask than I can spin up" because :1153 told it only what it could NOT do).
//
// It is generated from buildToolsPayload() — the SAME single taxonomy source the
// router enum (scoutRouterTools) and the palette read — so it can never drift
// from what is actually launchable: the 12 registry tools plus every non-hidden
// process (packaging_studio included, leading under "End-to-end"). One compact
// line per capability: "Name — promise (id)". The id rides along so a length
// test can assert every router-enum id appears, and so Stage 2's [[offer:id]]
// sentinel has the ids in front of the model.
func assistantCapabilitiesDigest() string {
	var b strings.Builder
	b.WriteString("# What this workspace can actually run (each is a confirmed, one-tap goal loop — you propose it, the user taps Run; nothing launches by itself):")
	for _, group := range buildToolsPayload() {
		if len(group.Tools) == 0 {
			continue
		}
		b.WriteString("\n")
		b.WriteString(group.Label)
		b.WriteString(" — ")
		parts := make([]string, 0, len(group.Tools))
		for _, tool := range group.Tools {
			parts = append(parts, tool.Name+": "+tool.Promise+" ("+tool.ID+")")
		}
		b.WriteString(strings.Join(parts, "; "))
	}
	b.WriteString("\nThree ways in: describe the work in chat, type / for the command lane, or tap + to browse the catalog.")
	return b.String()
}

// assistantToolsHandler serves the tool registry to signed-in clients so the
// quick-select palette reads the menu from the one source of truth. Same
// origin+session guard as its /assistant siblings; read-only GET.
func assistantToolsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !websocketOriginAllowed(r) {
		writeAuthError(w, http.StatusForbidden, "cross-origin request rejected")
		return
	}
	if userFromRequest(r) == nil {
		writeAuthError(w, http.StatusUnauthorized, "not signed in")
		return
	}
	writeAuthJSON(w, http.StatusOK, map[string]any{
		"ok":     true,
		"groups": buildToolsPayload(),
	})
}
