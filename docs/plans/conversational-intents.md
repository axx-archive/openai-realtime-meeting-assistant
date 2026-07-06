# Conversational intents — the intent map, the pill grammar, and the founder's test script

The typed-chat layer that lets a team member say what they want in human language and
get routed to the right machinery — with Scout asking ONE clarifying question (with
quick-reply pills) when the route is genuinely ambiguous. Built on the propose-confirm
router (`scout_chat.go`), the tool registry (`tool_registry.go`), and the process
registry (`process_definitions.go` / `packaging_studio.go`).

## The law

Nothing a person types, and nothing a person taps, ever launches work directly.
The escalation ladder is:

1. **Tier 0 — inline answer.** Questions, recall, opinions, discussion. The
   heavily-biased default.
2. **Choices — one question, 2-4 pills.** Only when the ask is clearly work but the
   route is ambiguous between concrete options (or one decisive input is missing).
   A pill tap sends that text as the user's reply; a tool-armed pill **arms** the
   proposal card.
3. **Proposal — the confirmation card.** The trust surface: tool + group, one legible
   sentence (gate + kill condition for tools; the checkpoint law for processes),
   editable fields, target package. Its **Run** button is the only launch door
   (`runGoalPipeline` → `POST /assistant/goal`, the identical palette spec).

Keyless deploys (`ANTHROPIC_API_KEY` unset): no router turn at all — plain Q&A,
never a pill, never an error.

## The intent map

The router's system prompt carries this map verbatim (compact form), so the live
Haiku turn routes these phrasings confidently. `scoutRouterProposalFromToolUse`
validates every routed id against the registry — tools via `toolByID`, processes via
`routerToolByID` (non-hidden only) — so a hallucinated id degrades to an inline
answer, never an error.

| Intent | Trigger language (examples) | Scout's interpretation (one sentence) | When ambiguous, Scout asks | Pills offered |
| --- | --- | --- | --- | --- |
| **Pitch outline** | "let's work on the pitch outline", "outline the deck", "sequence the narrative slide by slide" | "this is a Deck Outline run — {objective}. gate: rubric-scored (deck_outline_gate_v1), kill condition: a missing narrative beat…" | Who's in the room? (audience is the tool's one required field — the card collects it) | — (routes straight to the `deck_outline` proposal card) |
| **Design identity** | "develop a design identity", "brand direction", "the look and feel", "visual system for this" | "this is a Brand & Design Brief run — {objective}. gate: rubric-scored (design_brief_gate_v1)…" | Whether they want the brief (north star doc) or the studio's full identity stage — only if they say "identity" with a packaging run already in flight | `the design brief` (`brand_design_brief`) · `identity inside the full run` (`packaging_studio`) |
| **Deck from an existing outline** | "take the outline we have and build the deck from it", "turn the outline into the deck" | "this is the Packaging Studio staged process — build the deck end to end using the existing outline as the spine. it parks at each human checkpoint…" | "outline work, or the deck built end to end?" — when it's unclear whether they want outline revision or the built deck | `tighten the outline ▸` (`deck_outline`) · `full packaging run ▸` (`packaging_studio`) |
| **Full end-to-end packaging** | "package this end to end", "the full packaging run", "take it from 0 to 100" | "this is the Packaging Studio staged process — {objective}. it parks at each human checkpoint; nothing ships without your approval." | Nothing — this one is unambiguous | — (routes straight to the `packaging_studio` proposal card) |
| **Research / ground truth** | "dig into X", "what's the real market for this" | "this is a Deep Research run — {objective}…" | Full pass vs quick read, when the ask reads like an opinion question with a research-shaped tail | `full research pass ▸` (`deep_research`) · `just give me your read` (plain — Tier-0 answer) |
| **Comps / pricing** | "what's this worth", "what has this shape sold for" | "this is a Comps & Precedent run — {objective}…" | The thesis + format (the tool's required fields — the card collects them) | — |
| **Grill** | "grill it", "pressure test the pitch", "make it face the hostile room" | "this is a Grill / Pressure-Test run — {objective}…" | — | — |

Decision recorded: **deck-from-existing-outline routes to `packaging_studio`**, not a
deck-focused single run — the registry has no standalone deck-builder tool
(`deck_outline` produces the sequenced outline; the built presenter deck ships from
the studio process), so the objective names the existing outline as the spine and the
intake stage picks it up. When the utterance could mean either outline work or the
built deck, the router offers the two-pill choice instead of guessing.

## The flagship guarantees (Wave A — server seams)

Three interlocking server fixes close the 2026-07-05 live-sim failure, where Scout
answered a packageable ask with "that's a bigger ask than I can spin up" because the
answer brain was told only what it could NOT do, and the router lost the literal
full-run words ("end to end", "the full run") to the 6-turn context fold inside
`scoutRouterInput`:

1. **Capabilities digest + offer-never-deny** (`assistantCapabilitiesDigest`, keyed
   deploys only). The chat answer prompt's pure prohibition (`memory_query.go`) is
   REPLACED with a compact block — one line per capability (name · promise · id),
   generated from `buildToolsPayload()` so it can never drift from what is launchable —
   plus the rule: when a request maps to a capability, name it and offer to set it up;
   never deny a capability on the list. Scout still cannot launch anything itself. A
   golden test caps the block length and asserts every router-enum id appears.
2. **Router prompt patches** (`scoutRouterSystemPrompt`): `package_assembly` is ONLY
   "compile the artifacts we already made"; any end-to-end / full-run / from-scratch
   language is `packaging_studio` even mid-thread about an existing package (genuinely
   torn → `offer_choices` "compile what we have" / "the full staged run"); a correction
   that names a different tool/process is never Tier 0 — re-route it; economics /
   business model / unit economics / projections → `economics_waterfall`.
3. **Deterministic pre-router guard** (`deterministicRouterGuard`, before the Haiku
   turn): a work-shaped, non-negated message containing a reviewed full-run phrase
   ("end to end", "the full run", "0 to 100", "packaging studio", "full packaging") →
   `packaging_studio`, or a verbatim registry tool/process name → that capability, is
   committed as a PROPOSAL card BEFORE the model turn, so thread-context gravity can
   never drag the literal words off the flagship again. Propose-only, never a launch;
   the capped phrase list never fires on "package" alone. A bare leading "no," is a
   correction (it re-arms), not a skip; only action-negating tokens ("don't", "no need",
   "instead of") and questions defer to the answer brain.

**Flagship-first payload** (`buildToolsPayload`): the processes group renders FIRST
under the label **"End-to-end"** (it used to render last, below all 12 instruments),
so packaging_studio leads the palette, the digest, and the router's injected enum.

## The pill grammar (Kind `"choices"`)

**Wire/storage shape** (persisted like proposals, on `scoutChatMessageRecord.Choices`):

```json
{
  "kind": "choices",
  "role": "scout",
  "text": "outline work, or the deck built end to end?",
  "choices": {
    "question": "outline work, or the deck built end to end?",
    "query": "we need to work on the deck",
    "options": [
      { "id": "opt-1", "label": "tighten the outline", "toolId": "deck_outline" },
      { "id": "opt-2", "label": "full packaging run",
        "reply": "build the deck end to end from the existing outline",
        "toolId": "packaging_studio" },
      { "id": "opt-3", "label": "just talk it through" }
    ],
    "status": "", "selectedId": ""
  }
}
```

- `label` — the pill text (≤ ~6 words). `reply` — the fuller sentence sent as the
  user's message when tapped (defaults to the label). `toolId` — optional arm.
- The router emits this via the third routing tool `offer_choices`
  (question + 2-4 options; `tool_id` enum = the same registry-injected ids as
  `propose_tool_run`). Validation: blank labels drop, unknown `tool_id`s degrade to
  plain pills, >4 caps, <2 usable options degrades to an inline answer.
- **Tap** → `POST /assistant/chat-threads/{id}/choice` with `{messageId, optionId}`
  only. The server resolves against the persisted record (first tap wins; replays and
  sibling pills reject), commits the reply as the user's turn, records the
  `router_choice_selected` signal, then:
  - **tool-armed pill** → commits the deterministic proposal card
    (`scoutRouterProposalForToolID`) — the user still confirms on the card;
  - **plain pill** → answers the reply as Tier 0.
- **Render**: Scout question bubble + `.scout-choices` pill row (sheet grammar:
  999px radius, hairline border, label type; `▸` marks a tool arm). A resolved card
  keeps the chosen pill lit (`--accent`) and the rest recede — across reloads, from
  the persisted `status`/`selectedId`.

## Simulation script — the founder's human-language test

Type each of these into a **private Scout thread** on the live deploy. Expected
behavior is what the machinery guarantees; the routing itself rides the live model,
so tier-off-by-one on the ambiguous ones (a proposal where you expected pills, or an
inline answer where you expected a proposal) is a prompt-tuning note, not a bug.
Nothing on this list may ever launch without a Run tap.

| # | You type | Expected |
| --- | --- | --- |
| 1 | `let's work on the pitch outline for Station Tenn` | Deck Outline proposal card — audience field to fill, Run ▸ / "just answer instead". No launch until Run. |
| 2 | `who's seen the latest one-pager?` | Tier 0 — plain inline answer. No card. |
| 3 | `we need a design identity for the country culture studio` | Brand & Design Brief proposal card (audience/tone/references fields pre-filled where the message named them). |
| 4 | `take the outline we already have and build the deck from it` | Either the Packaging Studio proposal (objective naming the outline as the spine) or the two-pill question "outline work, or the deck built end to end?" — both correct. |
| 5 | *(if pills appeared on #4)* tap `full packaging run ▸` | Your reply posts as a message, the Packaging Studio proposal card appears — parked on YOUR Run tap. The other pill goes inert. |
| 6 | `package this end to end — the full run` | Packaging Studio proposal card, summary naming the human-checkpoint law. Run launches the staged process (goalcard parks at each checkpoint). |
| 7 | `what do you think about the buyer landscape?` | Tier 0 answer, or the choice pair `full research pass ▸` / `just give me your read`. Tapping the plain pill gets an inline answer — no run. |
| 8 | `what has a show like this actually sold for?` | Comps & Precedent proposal card, thesis/format fields to fill. |
| 9 | `grill the Station Tenn pitch before tomorrow` | Grill / Pressure-Test proposal card. |
| 10 | `run it` typed as a reply to a proposal card *(instead of tapping Run)* | Conversation — Scout may re-offer, but text never launches; only the card's Run button does. |
| 11 | tap a pill on a card you already answered *(second tab / stale reload)* | Rejected — "those options were already answered." First tap won. |
| 12 | `remind me what the kill condition on the one-pager is` | Tier 0 — recall answer from the registry/memory, no card. |
| 13 | `no, the full Packaging Studio staged run` *(as a correction to a prior proposal/answer)* | **Not a denial** — a re-route. The deterministic guard fires on the verbatim process name (a correction is never Tier 0), re-arming the Packaging Studio proposal card. Run still parks at each checkpoint. |
| 14 | `can you run the full packaging studio from here?` | An **offer, zero denial**: Scout says yes, names the Packaging Studio staged run, and offers to set it up — never "that's a bigger ask than I can spin up." The answer brain carries the capabilities digest + offer-never-deny (keyed deploys only). |
| 15 | `what can you actually do?` | Scout enumerates the real menu (the 12 tools + the End-to-end flagship) from the same digest the palette/router read, and offers to set any of it up — never a flat "I just answer questions." |

**Watch for on every row:** the thinking shimmer resolves into exactly one committed
turn; a reload re-renders cards and spent pills in their resolved state; the sidebar
preview updates; nothing appears in the goalcard rail until a Run tap.

## Files

- `scout_chat.go` — router: `offer_choices` tool, intent map in the system prompt
  (Wave A patches: package_assembly-vs-studio, the correction rule, economics vocab),
  `scoutChatChoices` types, `routerToolByID` (processes now validate — the
  packaging_studio enum/validation gap is closed), `scoutRouterProposalForToolID`,
  and the Wave A `deterministicRouterGuard` + `scoutRouterFullRunPhrases` +
  `scoutGuardEligibleMessage` (the pre-router flagship guarantee).
- `tool_registry.go` — `buildToolsPayload` (processes group first, "End-to-end"
  label) and `assistantCapabilitiesDigest` (the keyed self-knowledge block).
- `memory_query.go` — `assistantQueryInstructions` (keyed offer-never-deny protocol +
  digest; keyless keeps the honest prohibition).
- `scout_chat_threads.go` — `Choices` on the message record, the choices commit in
  the routing branch, `POST /assistant/chat-threads/{id}/choice`,
  `resolveScoutChatChoice` / `claimScoutChatChoice`.
- `index.html` — `scoutChoicesNode` + `postScoutChoiceSelection` + one marked CSS
  block (`Quick-reply pills (conversational intent layer)`).
- Tests: `scout_chat_choices_test.go` (round-trip, router choices turn, tool/plain
  pill paths, replay rejection, the four scenario routings, HTTP route, keyless),
  `frontend_choices_test.go` (component pins + the no-launch scope check),
  `invocation_wave_a_test.go` (the Wave A regression fence: digest length/coverage,
  offer-never-deny gating, the deterministic guard, the two-turn sim, the capability
  question).
