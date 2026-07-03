# Spectacular OS — Domain Strategy: The Packaging-Company Tool Suite

**Author:** Domain Strategist (+ Editorial Lead mandate) · **Date:** 2026-07-03 · **Wave:** Spectacular OS
**Owns:** the tool suite (pillar 3 content), the A++ prompt architecture, the "business as intelligence" flywheel, slop classification, Scout-run grill mode, and next-level venture-group ideas.

> **North star.** A packaging executive opens the quick-select menu and every tool on it feels *inevitable* — the exact instruments a studio uses to turn an idea into something talent and capital will say yes to. Nothing on the menu is a toy. Every output is a thing you could put in a data room. Every claim carries a receipt. The menu is short on purpose.

This document is the content and quality layer that rides on top of the `/goal` loop the Technical Analyst is engineering. I do not specify the execution engine; I specify **what runs through it and how good it has to be**. Where I reference machinery (`packages.go` stages, the `research_brief_v2` contract, the `READINESS: X/10` line, memory kinds), it already exists in code — I am extending contracts, not inventing them.

---

## 0. Grounding: what already exists (so I extend, not reinvent)

- **Package stages are real and fixed:** `thesis → research → design → pitch → grill → assembled` (`packages.go:25`). Every tool must declare which stage(s) it serves and, on completion, should be attachable to a package via `attach_to_package(ref_type=artifact)`.
- **Package gap detection already exists** (`packagePayload`, `packages.go:662`): a package computes `gaps` = deliverable stages at-or-before current stage with no attached artifact of that mode (`research`, `design`, `grill`). My tool→stage mapping must feed this so gaps stay meaningful.
- **Mode contracts already ship** (`agent_thread_runner.go:566–612`): `research` emits `research_brief_v2` with fixed headings + a Search tags line; `grill` emits `grill_scorecard_v2` with a machine-parsed `READINESS: <score>/10` first line. The readiness score is already stamped to metadata and read back by `packagePayload` for the readiness dial. **These are load-bearing contracts. My prompts preserve them exactly.**
- **Artifacts are the durable record** (`os_artifact`, memory kind). Payloads that fan out to all users carry **titles only, never body text** (`packageArtifactTuple`, `packages.go:593`). My slop classifier and completion reports must respect this trust boundary.
- **No slop-discard mechanism exists yet** (context brief §Memory). I design its classification criteria; the Technical Analyst designs the quarantine data model.
- **The A++ prompt architecture is new.** Today `agentThreadModeContract` is one terse sentence per mode. I am replacing that with a real prompt-template system: one master wrapper + per-tool bodies + a per-tool review rubric the gate step executes.

---

## 1. The Tool Suite — the definitive menu

### Design philosophy

A packaging company does exactly four things, in a loop: **ideate → package → market → support the portfolio**. The tool menu is organized by *what stage of that loop you're in*, not by *what the AI can do*. A tool earns its slot only if (a) its output is a real studio deliverable a human could put in front of talent or capital, and (b) it maps cleanly onto a package stage so it advances the flywheel. Anything that is "a chatbot that writes text about X" is cut.

I evaluated the candidate list in the mandate plus my own additions, and **killed** several (see §1.3). The final menu is **12 tools in 4 groups**. Twelve is the ceiling; the menu must never feel like a feature dump.

### 1.1 The menu (12 tools)

Each tool below is a **goal preset**: selecting it opens the composer pre-filled with a tuned objective and the tool's prompt template, then runs the one `/goal` loop. `Stage` = which package stage(s) it serves and advances. `Mode` = which existing agent-thread contract it extends (or `new` where I define a new contract). `Gate-critical` = the one thing the review-against-goal gate must verify before shipping.

#### Group A — Ideate (before a package exists, or at `thesis`)

**1. Deep Research** — *"Bring back the ground truth on any question, with receipts."*
- Stage: `research` · Mode: `research` (`research_brief_v2`) · Gate-critical: every non-obvious claim cites a source or Bonfire memory; counterarguments present.
- Inputs: a question or thesis; optional package context; optional "must-check" sources.
- Output: research brief — Executive Summary, Thesis, Evidence, Sources, Counterarguments, Recommendation, Open questions, Next checks, Worker evidence. Attachable to a package's `research` stage.

**2. Comps & Precedent Analysis** — *"What has this idea's shape sold for, and to whom?"*
- Stage: `thesis`, `research` · Mode: `research` (specialized) · Gate-critical: every comp names the deal/precedent, the source, and the *reasoning for comparability* — no comp asserted without a "why this is a fair comp" line.
- Inputs: the IP thesis + format/medium; optional target buyers.
- Output: a comp set table (precedent, terms/outcome where public, comparability rationale, source), a value range with confidence, and the two comps most likely to be challenged. The instrument that answers "what's it worth" defensibly.

**3. Market Map** — *"Where does this IP sit in its landscape, and where's the whitespace?"*
- Stage: `thesis`, `research` · Mode: `research` (specialized) · Gate-critical: the map is *bounded and current* — named players with a "last-move" date, and an explicit statement of what was NOT covered.
- Inputs: category/genre/thesis; optional axes to map on.
- Output: a landscape (incumbents, adjacents, emerging), the whitespace argument, and the demand signals supporting it. Feeds the one-pager's "why now."

#### Group B — Package (turn the thesis into materials: `research → design → pitch`)

**4. One-Pager** — *"The single page that makes someone take the meeting."*
- Stage: `pitch` · Mode: `new` (`one_pager_v1`) · Gate-critical: every number/claim traces to an attached research brief, comp, or decision in memory — the one-pager invents nothing.
- Inputs: the package (pulls thesis, research, comps, decisions); target reader (talent vs capital vs buyer); the ask.
- Output: a tight one-pager — logline, thesis, why-now, comps line, the team, the ask — written for the named reader, with a hidden "sources" appendix mapping each claim to its receipt.

**5. Deck Outline** — *"The pitch narrative, sequenced — slide by slide, not prose."*
- Stage: `pitch` · Mode: `new` (`deck_outline_v1`) · Gate-critical: narrative arc is complete (problem → insight → what → why-us → why-now → ask → the money slide) and each slide names its one job + its evidence.
- Inputs: the package; audience; length target; any must-hit beats.
- Output: a slide-by-slide outline — per slide: title, the one point, the evidence/visual, speaker note. Not a designed deck (design is the RTC/UX pipeline's job) — the *argument structure* a designer builds against.

**6. Brand & Design Brief** — *"The creative north star a designer can build from."*
- Stage: `design` · Mode: `design` (`design brief` contract) · Gate-critical: brief cites the research/thesis that justifies each creative choice — taste with a reason, never taste asserted.
- Inputs: the package thesis + audience; references/tone words.
- Output: design brief — intent, context/research used, tone & world, core screens/surfaces, interaction states, responsive plan, handoff notes, risks. Explicitly states how a research brief in memory shaped it.

#### Group C — Market (take it out: `pitch → grill`, and the deal itself)

**7. Grill / Pressure-Test** — *"Face the hostile room before the real one."*
- Stage: `grill` · Mode: `grill` (`grill_scorecard_v2`) · Gate-critical: the `READINESS: <score>/10` first line is present and correctly formatted; every objection is grounded in an actual weakness in the package, not a generic VC cliché.
- Inputs: the package (question bank drawn from its artifacts); persona; prior grill score (for the dial).
- Output: scorecard — `READINESS: X/10`, strongest objections, tough questions, revised ask, confidence gate. This is also the artifact Scout-run grill (§5) files. Feeds the readiness dial in `packagePayload`.

**8. Rights & Chain-of-Title Map** — *"Who owns what, and what has to be true before we can sell it."*
- Stage: `research`, `pitch` · Mode: `new` (`rights_map_v1`) · Gate-critical: every asserted right is marked **confirmed** (with source) vs **assumed** (flagged as a diligence gap) — the map never launders an assumption into a fact.
- Inputs: the IP's origin (underlying work, creators, prior agreements); known encumbrances.
- Output: a chain-of-title map — underlying rights, who holds each, encumbrances, the open questions that must close before a deal, and a red/yellow/green readiness stamp. **This is the tool that keeps the studio out of a lawsuit; it must be conservative to a fault.**

**9. Economics / Waterfall Scan** — *"Where does the money go, and does the deal work for us?"*
- Stage: `pitch`, `grill` · Mode: `new` (`economics_scan_v1`) · Gate-critical: every input assumption is labeled and sourced; the model states its sensitivities (what breaks the deal) rather than a single false-precision number.
- Inputs: deal structure, cost/revenue assumptions, the ask.
- Output: a plain-language economics scan — sources & uses, the waterfall (who gets paid in what order), the studio's position under base/up/down cases, and the two assumptions the whole thing hinges on. Not a spreadsheet engine — the *narrative + structure* a CFO would sanity-check.

**10. Talent Match** — *"Who should be attached, and what's the realistic path to yes."*
- Stage: `pitch` · Mode: `research` (specialized) · Gate-critical: each name has a *specific* rationale (a comparable credit, a stated interest, a relationship path) — no "get a big star" filler; availability/reach realism stated.
- Inputs: the package (role/creative needs); constraints (budget tier, timing).
- Output: a ranked talent slate — per name: fit rationale, comparable credit, path-to-contact, realism flag. The instrument that turns "we need a showrunner" into a call sheet.

#### Group D — Support the portfolio (after the deal: assembly + ongoing)

**11. Package Assembly** — *"Compile everything we've made into the document we actually send."*
- Stage: `assembled` · Mode: `workflow` → `artifacts` · Gate-critical: assembles ONLY published/attached artifacts, reconciles contradictions between them (flags any it can't), and produces one coherent voice — not a stapled PDF.
- Inputs: the package (reads every attached artifact body); target recipient.
- Output: the investor/talent-ready binder — one-pager + thesis + comps + rights readiness + economics + grill readiness, reconciled and in one voice, with a provenance appendix. Advancing to `assembled` should offer to run this. This is the artifact the studio *sells*.

**12. Investor-Update Memo** — *"The portfolio report a chief of staff would write, sourced from what actually happened."*
- Stage: post-`assembled` / portfolio · Mode: `new` (`update_memo_v1`) · Gate-critical: every stated development traces to a decision, meeting, artifact, or package stage-advance in memory — the memo reports facts on record, never optimism.
- Inputs: a time window; a package or the whole portfolio; recipient (LP vs internal).
- Output: a clean update memo — what moved (with links), decisions made, what's next, what we need — in a forwardable voice, gated behind human approval before it can leave the building (rides the existing `external_write` approval gate).

### 1.2 The menu at a glance

| # | Tool | Group | Stage(s) served | Contract | The receipt it must produce |
|---|------|-------|-----------------|----------|------------------------------|
| 1 | Deep Research | Ideate | research | `research_brief_v2` | source or memory per claim |
| 2 | Comps & Precedent | Ideate | thesis, research | research (spec.) | comparability rationale per comp |
| 3 | Market Map | Ideate | thesis, research | research (spec.) | dated players + coverage boundary |
| 4 | One-Pager | Package | pitch | `one_pager_v1` | claim→receipt appendix |
| 5 | Deck Outline | Package | pitch | `deck_outline_v1` | evidence per slide |
| 6 | Brand & Design Brief | Package | design | design brief | research→choice justification |
| 7 | Grill / Pressure-Test | Market | grill | `grill_scorecard_v2` | `READINESS: X/10` + grounded objections |
| 8 | Rights & Chain-of-Title | Market | research, pitch | `rights_map_v1` | confirmed-vs-assumed labeling |
| 9 | Economics / Waterfall | Market | pitch, grill | `economics_scan_v1` | labeled+sourced assumptions, sensitivities |
| 10 | Talent Match | Market | pitch | research (spec.) | specific rationale + realism per name |
| 11 | Package Assembly | Portfolio | assembled | workflow→artifacts | reconciled, provenance appendix |
| 12 | Investor-Update Memo | Portfolio | portfolio | `update_memo_v1` | every development on-record + approval gate |

### 1.3 What I killed, and why

- **"Go-to-market plan"** (mandate candidate) — cut as a top-level tool. For a *packaging* company (vs an operating company), GTM collapses into Talent Match + the pitch materials + the update memo. A standalone GTM tool would produce generic marketing-plan slop with no package stage to anchor it. If a specific venture in the portfolio needs one, it's a Deep Research goal, not a menu fixture.
- **Generic "Summarize" / "Draft an email" / "Brainstorm"** — deliberately absent. These are what makes an AI menu feel like a toy drawer. Summarization is Scout's ambient job (brain worker); ad-hoc drafting is what the `/goal` free-text command and Scout chat are *for*. Putting them on the curated menu cheapens it.
- **"SWOT" / "Persona builder" / "Competitor teardown"** — MBA-deck clichés. Market Map + Comps cover the real need with studio-specific framing; the clichés would invite exactly the box-filling slop this OS is designed to discard.
- **Merging Comps into Deep Research** — considered and rejected. Comps is worth its own slot because valuation-defense is a distinct, high-stakes deliverable with its own rubric (comparability rationale) that a general research prompt won't enforce.

### 1.4 Why this list feels inevitable

Walk a package left to right and the menu *is* the workflow: Research/Comps/Market-Map establish the thesis → Design Brief + One-Pager + Deck Outline package it → Rights/Economics/Talent/Grill de-risk and arm it for market → Assembly ships it → the Update Memo supports it in the portfolio. Every tool advances a real stage, closes a real `gap` in `packagePayload`, and produces a deliverable that goes in the data room. There is no tool a packaging exec would look at and ask "why is this here," and no obvious instrument they'd find missing.

---

## 2. A++ Prompt Architecture

The current system has one terse contract sentence per mode. A++ requires three layers per tool: a **master wrapper** (the 10-step loop scaffold, identical for every tool), a **tool body** (role, evidence standards, output contract), and a **gate rubric** (what the review-against-goal step scores before shipping). The wrapper and rubric are what make a mediocre model produce studio-grade work.

### 2.1 The master prompt template (the wrapper every tool inherits)

This wraps every tool body. Slots in `{{...}}`. It encodes the 10-step loop as *instructions to the orchestrator*, not decoration.

```
================ BONFIRE /goal MASTER WRAPPER v1 ================
You are an agent inside Bonfire OS, the operating system for a six-person
venture-packaging studio. The studio ideates IP, packages it, takes it to
market for talent and capital, and supports a portfolio. Your work will be
put in front of real talent and real investors. Mediocre is failure.

## THE GOAL (immutable — the gate reviews against THIS, not your draft)
{{goal_statement}}
Original requester: {{actor}} · Package context: {{package_name_or_none}}
Target reader of the output: {{audience}}
Success = {{success_criteria}}   ← the gate scores the output against this line.

## GROUND TRUTH YOU MUST USE (Bonfire's own memory)
You are not writing from general knowledge alone. Ground in the studio's
record where relevant, and PREFER it over your priors when they conflict:
- Package artifacts already attached: {{package_artifacts_titles_and_bodies}}
- Relevant decisions on record: {{relevant_decisions}}
- Relevant prior research/briefs: {{relevant_artifacts}}
- Relevant meeting facts: {{relevant_memory}}
If the studio's record contradicts your instinct, the record wins or you flag
the conflict explicitly. Never assert something the studio already decided
against without naming the decision you're overriding.

## EVIDENCE DISCIPLINE (non-negotiable, all tools)
1. Every non-obvious claim carries a receipt: an external source OR a citation
   to Bonfire memory (decision id, artifact title, meeting). Format receipts
   inline or in a Sources block per the tool contract.
2. Distinguish CONFIRMED (sourced) from ASSUMED (your inference) — label
   assumptions as assumptions. Laundering an assumption into a fact is the
   single worst failure mode. When unsure, say so; a flagged gap beats a
   confident fabrication.
3. If you used a worker tool (web fetch, file read), record what it returned in
   a Worker evidence block. No invented sources, ever. If you cannot verify a
   claim, mark it "unverified" rather than dropping the caveat.

## THE LOOP YOU ARE EXECUTING
1. GOAL — restate the goal in one line so the gate can check alignment.
2. DECOMPOSE — list the sub-questions/sections this goal requires.
3. ASSIGN/COORDINATE — note which need a worker (research, file, calc) vs your
   own synthesis; order them by dependency.
4. EXECUTE — do the work in order, gathering receipts as you go.
5. REVIEW — before writing the final, check each success criterion above.
6. GATE — self-score against the tool's rubric ({{rubric_ref}}); if any
   dimension is below bar, revise before emitting. State your gate result.
7. Emit the OUTPUT CONTRACT below — exactly the headings specified.
8. SAVE-WHAT-WORKED — end with a one-line note of what should carry forward
   (which finding feeds the next stage/tool).
9. REPORT-WHAT-MATTERS — your completion report follows the §3 discipline:
   what changed, the one headline, the open gap, the suggested next move.
   Omit process narration.
10. VERIFY — restate whether the goal is met, partially met, or blocked, and
    why. Do not claim done if a gate dimension failed.

## OUTPUT CONTRACT
{{tool_output_contract}}

## STYLE
Studio voice: precise, confident, zero filler. No "in today's fast-paced
landscape." No hedging where you have evidence; explicit hedging where you
don't. Write like a sharp analyst who respects the reader's time.
================================================================
```

**Why the wrapper is the quality lever:** it forces grounding in Bonfire memory (design principle 2), makes the gate review against the *original goal* not the draft (principle 3, the classic self-review trap), and bakes the confirmed-vs-assumed discipline into every tool at once. A weaker default model produces near-A work inside this scaffold because the scaffold does the judgment the model would otherwise skip.

### 2.2 The per-tool gate rubric contract

The gate step (loop step 6/7) is not vibes. Each tool ships a rubric of 3–5 dimensions scored 1–10, with a **bar** (minimum to ship) and one **kill condition** (an automatic fail regardless of other scores). The orchestrator runs the rubric as a review pass *against the goal statement*, not the produced draft. Below-bar → one revision round → re-score → ship-or-flag.

```
RUBRIC SCHEMA (per tool):
  dimensions: [{name, what_it_measures, bar (1-10)}]
  kill_condition: <one sentence — auto-fail>
  ship_if: all dimensions >= bar AND kill_condition not triggered
```

### 2.3 Three fully-written exemplar tool prompts

These are the tool bodies that drop into `{{tool_output_contract}}` + `{{rubric_ref}}`. Written at production quality.

---

#### Exemplar A — Deep Research (`research_brief_v2`)

```
## ROLE
You are the studio's head of research. You bring back ground truth, not vibes.
You would rather return "here is what I could verify and here is the gap" than
a confident answer you can't defend in a partner meeting.

## EVIDENCE STANDARD
- Primary/authoritative sources over aggregators; name each source and what it
  actually said. If a claim rests on a single weak source, say so.
- Recency matters: stamp dates on anything that decays (deals, headcounts,
  market size). Flag anything you could only find as stale.
- Actively seek the counter-case. A brief with no counterarguments is
  incomplete and fails the gate.
- Ground in Bonfire memory: if the studio has prior research or decisions on
  this, build on them and cite them; note where new findings update them.

## OUTPUT CONTRACT — use these EXACT headings (research_brief_v2):
Search tags: <5-10 comma-separated terms, near the top>
Executive Summary  — 3-5 sentences a partner can act on.
Thesis             — the one defensible claim this brief supports.
Evidence           — bulleted findings, each with an inline source.
Sources            — numbered list; only sources actually used.
Counterarguments   — the strongest case against the thesis, honestly made.
Recommendation     — what the studio should do with this.
Open questions     — what remains unresolved.
Next checks        — the follow-ups that would close the open questions.
Worker evidence    — raw returns from any tool/fetch you ran.

## GATE RUBRIC (research_brief_gate_v1)
  - Grounding (bar 8): every non-obvious claim has a source or memory cite.
  - Counter-case (bar 7): the strongest opposing view is present and fair.
  - Actionability (bar 7): a partner could decide from the Recommendation.
  - Recency honesty (bar 8): decaying facts are dated or flagged stale.
  kill_condition: any invented/unverifiable source, or a claim asserted as fact
    that is actually the agent's assumption.
  ship_if: all >= bar AND kill_condition not triggered.
```

---

#### Exemplar B — One-Pager (`one_pager_v1`)

```
## ROLE
You are the studio's packaging lead writing the single page that decides
whether {{audience}} takes the meeting. This page invents NOTHING. Every claim
on it already exists in the package — your job is selection, sequencing, and
voice, not fabrication. If a number isn't backed by an attached brief, a comp,
or a decision, it does not go on the page.

## INPUTS YOU MUST PULL FROM THE PACKAGE
- Thesis (package.thesis), attached research brief(s), comps, rights readiness,
  economics, and any decisions. If a needed input is MISSING, do not invent it
  — flag it in the sources appendix as a gap and write around it honestly.

## OUTPUT CONTRACT — one_pager_v1:
Title / Logline    — one line that makes someone lean in.
The Thesis         — 2-3 sentences: what this is and why it's true.
Why Now            — the market/timing argument (from Market Map / Research).
Comparables        — one line, the 1-2 strongest comps with the value signal.
The Team           — why this studio, for this IP.
The Ask            — exactly what you want from {{audience}} and what they get.
---
Sources appendix (NOT for the reader's copy) — a table mapping every claim on
the page to its receipt: claim → package artifact/decision id it came from.
Any claim with no receipt is a GATE FAILURE, not a stylistic choice.

## STYLE
Tuned to {{audience}}: for capital, lead with the economics and the ask; for
talent, lead with the creative and the company; for a buyer, lead with the
audience and the comp. Never generic. One page. Ruthless.

## GATE RUBRIC (one_pager_gate_v1)
  - Receipts (bar 9): every claim maps to a package source in the appendix.
  - Reader-fit (bar 8): the lead and the ask match {{audience}}.
  - Compression (bar 8): it is genuinely one page, no filler, every line earns.
  - Voice (bar 7): it reads like a sharp studio, not a template.
  kill_condition: any claim on the page with no receipt in the appendix.
  ship_if: all >= bar AND kill_condition not triggered.
```

---

#### Exemplar C — Grill / Pressure-Test (`grill_scorecard_v2`)

```
## ROLE
You are the hostile room the studio has to survive: {{persona|default: a
skeptical, well-prepared investor who has read the whole package and wants to
find the hole}}. You are not cruel for sport; you are rigorous. Every objection
you raise must point at a REAL weakness in this specific package — a thin comp,
an unconfirmed right, an assumption doing too much work — not a generic VC
cliché. Cite the package where a weakness lives. Generic objections fail the
gate.

## QUESTION BANK
Build your questions from the package's own artifacts: the thesis's soft
assumptions, the research brief's open questions, the rights map's ASSUMED
items, the economics scan's hinge assumptions. Cross-check against decisions in
memory — if the team is pitching something they earlier decided against, name
it.

## OUTPUT CONTRACT — grill_scorecard_v2:
READINESS: <score>/10   ← FIRST line after any vision line. One decimal
                          (e.g. "READINESS: 6.5/10"). MACHINE-PARSED. Never
                          omit or reformat — the readiness dial reads this.
Strongest objections    — the 3-5 that would actually sink the meeting, each
                          tied to the specific weak spot in the package.
Tough questions         — the questions they must be able to answer cold.
Revised ask             — how to reframe the ask to survive the objections.
Confidence gate         — what MUST be true/fixed before this is market-ready.

## SCORING RUBRIC (how you set READINESS — score these, average, round to .1):
  - Evidence: is every claim in the pitch backed? (weak → low)
  - Clarity: is the ask and thesis unmistakable?
  - Confidence: does it survive the strongest objection standing?

## GATE RUBRIC (grill_gate_v1)
  - Format (bar 10): READINESS line present and correctly formatted.
  - Groundedness (bar 8): objections tie to real package weaknesses, cited.
  - Fairness (bar 7): objections are the strongest honest case, not strawmen.
  kill_condition: missing/malformed READINESS line, OR objections that are
    generic and not tied to this package.
  ship_if: all >= bar AND kill_condition not triggered.
```

---

The remaining nine tools follow the identical pattern (role → evidence standard → exact output contract → gate rubric with a kill condition). The kill conditions are the studio's non-negotiables: **Rights** kills on an assumed-right presented as confirmed; **Economics** kills on false-precision without labeled assumptions; **Comps** kills on a comp with no comparability rationale; **Assembly** kills on an unreconciled contradiction shipped silently; **Update Memo** kills on any development not traceable to the record.

---

## 3. The Flywheel — "business as intelligence"

The thesis is that the studio *compounds*: every completed artifact makes the next one cheaper and better, and the OS discards what doesn't compound so the knowledge base never rots. Here's the actual mechanics.

### 3.1 What each completed artifact feeds

A tool completion is not a dead end — it fires three writes:

1. **Package stage advance.** On completion, the artifact is offered for `attach_to_package(ref_type=artifact)`, which stamps `packageId` bidirectionally (`packages.go:504`) and closes the matching `gap` in `packagePayload`. A grill completion additionally updates the readiness dial (its `readinessScore` is read by `packagePayload:638`). When the last gap for a stage closes, the OS offers to advance the stage — the board stops lying about where the package is.
2. **Decision ledger.** Any decision surfaced during the work ("we're pricing at $75k," "we're not attaching a director") is written as a `decision` memory entry, linked to the package. The next tool's wrapper pulls `relevant_decisions` — so the One-Pager can't contradict a pricing decision the Economics scan established.
3. **Next tool's context.** The artifact body is indexed for recall (per the simulation's structural fix #3 — Scout must read artifact *bodies*, not just card metadata). The next tool's `{{relevant_artifacts}}` slot pulls it. This is the compounding: Research feeds the One-Pager feeds the Deck feeds the Grill, each grounded in the last.

The chain is literal: `Deep Research → attached at research stage → One-Pager pulls it as a source → Deck Outline pulls both → Grill builds its question bank from all three → Assembly reconciles the set → Update Memo reports the whole arc.` Each hop is a `packageId`-linked memory read, not a re-derivation.

### 3.2 The "report only what matters" discipline

The single biggest threat to "business as intelligence" is the knowledge base snowballing into noise. The completion report is where that discipline lives. Every tool's step-9 report is **four lines, nothing more**:

```
COMPLETION REPORT (what matters):
  Changed:  <the one artifact produced + what package/stage it advanced>
  Headline: <the single most important finding/output, one sentence>
  Gap:      <the one thing still open or unverified — or "none">
  Next:     <the suggested next tool/stage, or "ready to ship">
```

**A completion report CONTAINS:** the deliverable's identity and where it landed, the one headline a busy founder needs, the one open gap, and the recommended next move.

**A completion report OMITS:** the loop narration ("I decomposed the goal into…"), the full artifact body (it's in the artifact — the report links, never inlines; and per the trust boundary, fan-out payloads carry titles only), process apologetics, and anything the reader can get by opening the artifact. If the report is longer than four lines, the tool is over-reporting and the gate should trim it.

This is also the delivery fix from the simulation: the report is what gets posted back into the originating channel/room ("close the loop where it started") — so it *must* be skimmable in two seconds and lead with the outcome.

### 3.3 The compounding loop, drawn

```
  IDEATE ──► Research / Comps / Market Map ──┐
                                             │ (attached, indexed, decisions logged)
  PACKAGE ─► One-Pager / Deck / Brand Brief ─┤◄─ each pulls the prior stage's receipts
                                             │
  MARKET ──► Rights / Economics / Talent /   │
             Grill (readiness dial) ─────────┤
                                             │
  ASSEMBLE ► Package Assembly ───────────────┤◄─ reconciles the whole set into the binder
                                             │
  PORTFOLIO► Investor-Update Memo ───────────┘──► reports the arc; feeds the next round

  Every arrow is a packageId-linked memory read. Slop classifier (§4) prunes
  anything that never gets attached, cited, or advanced — so the base stays
  dense, not just large.
```

The flywheel only spins if the base stays clean. That's §4.

---

## 4. Slop Classification

"Business as intelligence" fails the moment the knowledge base fills with material irrelevant to the company. The slop system's job: keep the base **dense** — every entry earns its place by being attached, cited, or acted on — while never silently destroying anything (design principle 5). Policy is confirmed: **quarantine-then-auto-expire** (context brief §6). I define the *classification*; the Technical Analyst defines the quarantine data model.

### 4.1 The core distinction: company-relevant vs slop

The test is not "is this good" — it's **"does this belong to the company's compounding knowledge?"** An entry is **company-relevant** if it is, or plausibly becomes, a receipt for something the studio does: a package, a decision, a deliverable, a portfolio fact. It is **slop** if it is none of those and never will be.

| | Company-relevant (keep) | Slop (quarantine) |
|---|---|---|
| **Artifacts** | Attached to a package, cited by another artifact, or a standalone deliverable a human published | A tool run abandoned mid-loop, a duplicate re-run of an existing brief, an experiment nobody attached, a draft superseded by a published version |
| **Transcripts** | Segments the brain worker turned into themes, decisions, board cards, or commitments | Pure logistics ("can you hear me?", "let me share my screen"), off-topic chatter, dead air, meeting-tool cross-talk |
| **Chat threads** | Threads that produced a decision, an artifact, or a channel post; `@scout` work threads | Empty threads, single-message dead ends, "test" threads, threads fully superseded by a later one on the same topic |
| **Board cards** | Cards linked to a package or an open commitment | Stale cards whose owner left, duplicates auto-created by linkage, cards for a killed package |

### 4.2 The edge cases (where naive classification breaks)

These are the cases a dumb classifier gets wrong, so the rules are explicit:

- **A dead package's research → ARCHIVE, not slop.** If a package was killed, its artifacts are not slop — they're precedent. A future IP in the same genre will want that comp set. Rule: **anything ever attached to a package is never slop; on package death it moves to `archive` (searchable, downranked), not `quarantine`.** This is the difference between "we didn't pursue it" (valuable) and "this is noise" (discard).
- **Superseded but load-bearing.** A One-Pager v1 replaced by v2 is superseded — but if v1 is what got sent to an investor last month, it's a record of what was represented, not slop. Rule: **superseded artifacts that were ever published/sent → archive; superseded drafts that were never published → quarantine.**
- **Logistics inside a valuable meeting.** A meeting that produced three decisions also contains "can everyone see my screen?" Rule: **quarantine operates at segment granularity for transcripts, not whole-meeting** — the decisions stay, the "can you hear me" goes. Never quarantine a whole transcript that produced any theme/decision/card.
- **Recent uncertainty.** A thread from this morning with one message might become something by this afternoon. Rule: **age gate — nothing younger than 7 days is eligible for quarantine.** Slop is judged on settled entries, not live ones.
- **The admin's scratch.** A human deliberately writing a note to themselves is relevant even if the OS can't see why. Rule: **anything a human explicitly published, pinned, or attached is exempt** — the classifier only judges auto-generated and orphaned material.

### 4.3 The classifier prompt

Runs as a fifth ambient agent (same `startAmbientAgent` recipe as brain/board/decision/mission), on a slow cadence (e.g. every 6h), over settled entries only.

```
## ROLE
You are the studio's knowledge steward. Your job is to keep the company's
memory DENSE: every entry should be a receipt for something the studio does.
You are conservative — quarantine is reversible but you still err toward KEEP
when unsure, because a wrongly-quarantined entry costs the studio a memory.

## THE TEST
For each candidate entry, decide: is this, or could this plausibly become, a
receipt for a package, a decision, a deliverable, or a portfolio fact?
  - YES, or attached/cited/acted-on ever → KEEP.
  - Was attached to a package but that package/context is dead → ARCHIVE.
  - Was published/sent to a human ever → KEEP or ARCHIVE, never quarantine.
  - None of the above, orphaned, duplicative, or superseded-and-never-sent,
    AND older than 7 days AND not human-pinned → QUARANTINE.

## HARD RULES (never violate)
- Never quarantine an entry younger than 7 days.
- Never quarantine a transcript that produced any theme/decision/card — operate
  at segment level; keep the substantive segments.
- Never quarantine anything a human published, pinned, or attached.
- Never quarantine anything ever attached to a package (archive instead).

## OUTPUT (per candidate, machine-parseable):
  {entry_id, verdict: keep|archive|quarantine, confidence: 0.0-1.0,
   reason: <one line>, evidence: <what it was/wasn't attached to>}

## CONFIDENCE THRESHOLDS
- quarantine requires confidence >= 0.85. Below that → keep and re-evaluate
  next pass. A borderline slop entry costs nothing to keep one more cycle; a
  wrongly-discarded decision costs the studio.
```

### 4.4 Confidence thresholds and what the human sees

- **Quarantine requires confidence ≥ 0.85.** Below → keep, re-check next pass. Archive (the softer move) can run at ≥ 0.7 since it stays searchable.
- **Quarantine is a visible, reversible state** (context brief §6): entries move to a "discarded" view excluded from recall, auto-delete after ~30 days, restorable one-click in settings.
- **The human review surface must enable a 10-second keep/discard decision.** For each quarantined entry it shows: **(1)** a one-line title/summary, **(2)** the classifier's one-line reason ("orphaned tool run, never attached, 12 days old"), **(3)** what it was/wasn't linked to, **(4)** age + auto-delete countdown, **(5)** a one-tap Restore and a one-tap "Delete now." No entry should require *opening* it to judge — the reason line does the work. Batch actions ("restore all from this package," "these 8 look right, confirm") for when the founder trusts the classifier.

The discipline: the classifier proposes, the human never *has* to dispose (auto-expire handles the default), and nothing is ever silently hard-deleted.

---

## 5. Grill Mode, Scout-Run

Pillar 2 requires Scout to run grill mode *herself* privately: you pitch her, she grills you. This is the domain contract for that — the marquee "she can do it all" demo. It reuses the `grill_scorecard_v2` contract (§2.3 Exemplar C) but wraps it in a live, voice-driven, multi-phase session.

### 5.1 The session shape (four phases)

**Phase 1 — Pitch capture.** Scout says "Pitch me. Take your time — I'm listening." The user pitches by voice; the transcript lane captures it (attributed). Scout does not interrupt during capture; she signals she's holding questions (a visible "listening / holding N questions" state on the voice island). Capture ends on a natural pause + "that's my pitch" or a timeout.

**Phase 2 — Question-bank generation.** Scout silently builds the question bank from **(a)** the captured pitch and **(b)** the package's artifacts if the pitch names a package — the thesis's soft assumptions, the research brief's open questions, the rights map's ASSUMED items, the economics hinge assumptions, and any *contradicting decision* in memory. This is the grounding that makes her grill feel omniscient: "your CAC number contradicts what the June 12 economics scan assumed."

**Phase 3 — The grilling.** Scout adopts the persona (default: prepared skeptical investor; user can request "grill me as a hostile buyer" / "as an operator"). She asks **one question at a time, out loud**, listens to the spoken answer, and asks a real follow-up based on what was actually said — not a script. She holds a politeness budget (doesn't pile on after a strong answer; presses after a weak one). Each answer is scored live on evidence/clarity/confidence.

**Phase 4 — Report + dial.** On "end grill" (or after N questions), Scout files a `grill_scorecard_v2` artifact and speaks the headline: "You're at a 6.8. Your thesis and ask are sharp; you fold on economics and the rights question. Fix those two and re-grill." The artifact attaches to the package (updating the readiness dial); if a prior grill exists, she reports the **delta** ("up from 6.2 last week").

### 5.2 Grilling persona standards

The persona is the product. Standards:
- **Prepared, not random.** Every question ties to a real weakness in the pitch or package — Scout has "read the file." Generic VC questions are banned (same kill condition as the Grill tool).
- **One at a time, conversational.** She waits for the answer, reacts to it, follows up. This is a realtime voice session (`gpt-realtime-2`), so it's a real back-and-forth, not a form.
- **Escalating, with a budget.** Weak answers get pressed ("that's an assumption — what's it based on?"); strong answers get acknowledged and she moves on. Max pressure per topic is bounded so it's rigorous, not abusive.
- **Cited when she strikes.** When she catches a contradiction with memory, she names the source: "That's not what the June 12 decision says." This is the moment that makes the demo land.
- **Private by default.** Scout-run grill is a private-voice session (the user pitching alone with Scout) — it does not broadcast to the room. It requires the private-voice tool allowlist to include grill (a pillar-2 parity item), with the room-only grill staying room-only.

### 5.3 Scoring rubric and the readiness dial

Reuses the `grill_scorecard_v2` scoring exactly (§2.3): **Evidence** (every claim backed?), **Clarity** (ask/thesis unmistakable?), **Confidence** (survives the strongest objection?), averaged to the machine-parsed `READINESS: X/10`.

The **readiness dial** is the compounding metric: `readinessScore` is stamped to artifact metadata and read by `packagePayload` (`packages.go:638`), with `readinessDelta` tracked across re-grills. The product story: a package's readiness is a *number that moves* as the team practices — 6.2 → 6.8 → 7.5 — turning a one-shot report into the studio's pitch-readiness gauge. The dial belongs on the package binder surface (UX's domain) with the trend, not just the latest score.

### 5.4 Report format

The spoken report is the four-line completion report (§3.2) delivered by voice, leading with the number:
```
"Readiness: 6.8, up from 6.2. Headline: thesis and ask are sharp.
 Gap: you fold on economics and the rights question. Next: fix those two,
 then say 'grill me again' and we'll see the dial move."
```
The written artifact is the full `grill_scorecard_v2`. Both attach to the package.

---

## 6. Next-Level Ideas

Six domain ideas that make "business as intelligence" real across the *full* ideate→package→market→portfolio lifecycle — beyond the tool suite. Ranked by wow-per-effort. **I recommend at most two for this wave** (marked ★).

### ★ 6.1 The Deal Room (wow-per-effort: highest)
**What:** A one-tap, read-only, *shareable* export of a package's assembled binder — a real data room a studio hands to an investor or talent's team. It's the Package Assembly output (§1, tool 11) rendered as a clean, permissioned, linkable surface with a provenance appendix, gated behind the same `external_write` human approval as the memo.
**Why it's next-level:** it's the moment Bonfire's internal intelligence becomes an *external* asset — the studio doesn't just know things, it can *present* them. This is the tangible payoff of the whole flywheel.
**Effort:** low-medium. Assembly already produces the content; this is a permissioned read-only view + a share/approval gate, both of which have precedents (artifact reader, external-write approval). **Recommended for this wave** — it's the natural capstone of the tool suite and the highest-leverage new surface.

### ★ 6.2 Portfolio Health (wow-per-effort: high)
**What:** A portfolio-level dashboard: every package's stage, readiness dial, freshness (days since last movement), and open gaps, in one view — plus a Scout-composed "state of the portfolio" the founder can request ("Scout, how's the portfolio?"). Stale packages surface themselves ("Nimbus hasn't moved in 3 weeks; its readiness is 6.2").
**Why it's next-level:** a six-person shop gets a portfolio-management view a fund would have — the OS watching the whole book, not one deal. It's where "business as intelligence" becomes visible at a glance.
**Effort:** medium. Every input exists (`venturePackagePayloads`, readiness, `updatedAt` freshness, gaps). It's an aggregation surface + one Scout summary tool. **Recommended for this wave** — it pairs with the Deal Room to complete the portfolio story, and it makes the flywheel's compounding legible.

### 6.3 Thesis Watch / Market Twin (wow-per-effort: medium-high)
**What:** Each package's thesis gets a standing watcher — a scheduled research thread monitoring comps, competing announcements, and talent moves relevant to that IP. A material move fires a "thesis check" notification and stamps a freshness score. (This is the roadmap's "Market twin.")
**Why:** the packages defend themselves; the studio gets ambient market awareness.
**Effort:** medium-high — the tool (Deep Research/Comps) exists, but the scheduling, noise-suppression, and materiality judgment are real work. **Defer** — high value but a wave of its own; depends on scheduled-agent infrastructure.

### 6.4 The Weekly Memo, autonomous (wow-per-effort: medium)
**What:** The Investor-Update Memo (tool 12) run automatically weekly by an ambient agent, composed from the week's decisions/advances/artifacts, gated behind human approval before it emails. (Roadmap "Weekly Memo.")
**Why:** Sunday-night chief-of-staff report, forwardable to LPs, untouched by human hands.
**Effort:** medium once the memo tool + decision ledger exist. **Defer to next wave** — it's the memo tool + a cron + the email gate; better shipped after the memo tool proves out manually.

### 6.5 Deal-Flow Intake (wow-per-effort: medium)
**What:** An inbound funnel — a form or a forwarded pitch/email that Scout triages into a candidate package with an auto-drafted thesis and a "should we take this?" first-pass score, landing as a proposal card.
**Why:** turns the top of the funnel (which today lives in inboxes) into the OS; the studio's dealflow becomes searchable, scored intelligence.
**Effort:** medium. **Defer** — valuable but it's a new intake surface + triage prompt; lower wow than the Deal Room for this wave, and it widens scope toward inbound-ops the studio may not need yet.

### 6.6 The Panel (wow-per-effort: low, high ceiling)
**What:** Grill mode's final form — Scout runs a multi-persona investor panel (skeptic, operator, believer) with distinct voices cross-examining a live pitch, scored per persona, ending in a term-sheet-style verdict. (Roadmap "The Panel.")
**Why:** a full partnership-meeting dress rehearsal on demand — the ultimate grill.
**Effort:** high — multi-voice orchestration on one realtime session is a frontier problem. **Defer** — spectacular ceiling, but it's a moonshot that depends on Scout-run grill (§5) shipping and proving stable first.

### Recommendation for this wave
Ship **the Deal Room (6.1)** and **Portfolio Health (6.2)**. Together they close the lifecycle: the tool suite *makes* the intelligence, the Deal Room *externalizes* it, and Portfolio Health *watches the whole book*. Both are low-medium effort riding existing plumbing (Assembly, `venturePackagePayloads`, the external-write gate), and both are the visible payoff that makes "business as intelligence" feel real to the founder — which is the entire point of the wave. Everything else (Thesis Watch, Weekly Memo, Deal-Flow, the Panel) is genuinely great and genuinely a later wave.

---

## Appendix — Integration notes for the other specialists

- **For the Technical Analyst:** the tool bodies (§2.3) are prompt content, but the **gate rubric schema** (§2.2) and the **classifier output schema** (§4.3) are contracts your `/goal` engine and quarantine model must honor. The `READINESS: X/10` line and the `research_brief_v2`/`grill_scorecard_v2` contracts are already parsed in code (`packages.go`, `agent_thread_runner.go`) — do not break them; the new contracts (`one_pager_v1`, etc.) follow the same "exact headings, machine-checkable kill condition" shape.
- **For UX:** the menu is 12 tools in 4 groups (§1.2) — the quick-select surface should group them by lifecycle phase (Ideate/Package/Market/Portfolio), not alphabetically, so the menu reads as the workflow. The readiness dial (§5.3) and Portfolio Health / Deal Room (§6) are your new surfaces.
- **For the Product Lead:** the flywheel (§3) is the cross-pillar spine — every tool completion fires the three writes (stage advance, decision log, next-context index) and the four-line report. The two recommended next-level ideas are Deal Room + Portfolio Health.
```