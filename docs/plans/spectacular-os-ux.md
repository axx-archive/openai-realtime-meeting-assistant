# Spectacular OS — UX Design Specification

**Owner:** UX Designer · **Date:** 2026-07-03 · **Wave:** Spectacular OS
**Scope:** quick-select tool menu · Scout do-it-all voice UX · BonfireOS rename + shell polish · surprise-and-delight inventory · mobile parity · settings honesty for AV · working inside the 34.9k-line monolith

> This document extends the **live** design language — the glass-and-ink token system declared inline at `index.html:28–243` (the `--ink-*`, `--paper-*`, `--signal-*`, `--glass-*`, `--type-*`, `--r-*`, `--ease*` families). It does **not** use the legacy `design-system/colors_and_type.css` ember tokens; those are a superseded skin. Every value below is either an existing live token or a new token proposed in §7. Nothing here forks the system.

---

## 0. Design north star for this wave

Five rules, applied everywhere below:

1. **Glass and ink, one warm accent with a job.** The live system is deliberately neutral: `--accent` is ink (inverts light↔dark), and `--signal-500` (#30D158) is "the loudest color in the system," reserved for *who is live/speaking* (`index.html:56`). This wave introduces **exactly one** new accent — a warm ember — and gives it a single, honest job: **"an agent is doing work for you."** Green = a human is live. Ember = a machine is in flight. They never overlap. This is the "warm fire accents" mandate delivered as *meaning*, not decoration, and it ties back to the flame brandmark (`index.html:14787`).
2. **Motion is state.** We animate transitions between real states (idle→listening, queued→running→done). We never spin a fake loader. The `--breathe` amplitude switch (`index.html:177–180`, `245–248`) is the model: one CSS variable flips the whole room rest→live.
3. **Mobile is a different instrument.** Not a narrowed desktop. The thread composer, quick-select, and voice island each get a native-quality mobile form, not a media-query squeeze.
4. **Honesty is luxury.** Settings show the *true* suppression state per device. The /goal card shows the *real* 10 stages advancing. No spinner stands in for unknown progress.
5. **Every delight survives 50 views/day.** The taste test for each moment below: would it still feel good the fiftieth time, or would it start to feel like the OS is performing for you? If the latter, it's cut or made quieter.

---

## 1. Quick-select tool menu

The packaging tool suite surfaced on thread/chat screens. Three converging entry points (menu + `/` + voice) that all land on the same `/goal` pipeline.

### 1.1 Trigger — three doors, one room

| Door | Interaction | Rationale |
|---|---|---|
| **Button** | A `+ Tools` affordance pinned to the left of the composer send button (`index.html:15557` composer region). Tap/click opens the palette. | Discoverable — a first-time user finds it without knowing the magic keystroke. |
| **`/` typed** | Typing `/` as the **first character** of an empty composer opens the palette inline, filtering as you type (`/mar` → Market Map). Escape or deleting the `/` closes it. `/` mid-sentence is literal. | Power path. Mirrors Slack/Linear muscle memory; zero mouse travel. |
| **Voice** | "Scout, build a market map for…" routes through the same tool registry (§2). | Hands-free path; same pipeline, same output artifact. |

All three call one function, `openToolPalette(source)`, and all three ultimately call `runGoalPipeline(toolId, inputs)`. The palette is a **view onto the tool registry**, never a fork of it.

### 1.2 Desktop layout — the palette

A centered glass sheet (`--glass-chrome`, `--blur-overlay`), `--r-2xl` corners, over a `--scrim` backdrop. Grid of tool tiles, each a card with icon + name + **promise line** (what it will produce, not what it is). Search field auto-focused. Recents row on top.

```
┌───────────────────────────────────────────────────────────────────────┐
│  ⌕  build a market map for the fintech thesis…                     esc  │   ← search, autofocus, --type-body
│                                                                         │
│  RECENT                                                                 │   ← --type-label, --text-3
│  ┌──────────┐ ┌──────────┐ ┌──────────┐                                 │
│  │ ◆ Market │ │ ◇ One-   │ │ ✦ Deck   │                                 │
│  │   Map    │ │   Pager  │ │          │                                 │
│  └──────────┘ └──────────┘ └──────────┘                                 │
│                                                                         │
│  PACKAGE                                    RESEARCH & PROOF            │
│  ┌────────────────────┐ ┌────────────────┐ ┌────────────────────────┐  │
│  │ ◆                   │ │ ✦               │ │ ⌕                       │  │
│  │ Market Map          │ │ Pitch Deck      │ │ Deep Research          │  │
│  │ landscape + comps   │ │ 10-slide story  │ │ sourced brief + risks  │  │
│  └────────────────────┘ └────────────────┘ └────────────────────────┘  │
│  ┌────────────────────┐ ┌────────────────┐ ┌────────────────────────┐  │
│  │ ◇ One-Pager         │ │ ▤ Comps Table   │ │ ⚖ Rights & IP Scan     │  │
│  │ the single-page ask │ │ valuation set   │ │ freedom-to-operate     │  │
│  └────────────────────┘ └────────────────┘ └────────────────────────┘  │
│                                                                         │
│  ↑↓ navigate   ↵ open   ⌘↵ run with defaults                       ⓘ    │   ← footer hint, --type-label
└───────────────────────────────────────────────────────────────────────┘
```

- **Tile anatomy:** 44×44 icon well (satisfies `--hit-min`), tool name in `--type-body-medium`, promise line in `--type-caption` `--text-2`, `text-wrap: pretty` so it never orphans. Tile radius `--r-lg` (16); inner icon well `--r-md` (12) — **concentric** (`outer = inner + padding`, the skill's rule #1).
- **Grouping** by package stage relevance (PACKAGE / RESEARCH & PROOF / GO-TO-MARKET / OPS) — the Domain Strategist owns the taxonomy; the UI reads group labels from the registry so adding a tool never touches layout code.
- **Sizing:** `width: min(760px, 92vw)`, max 3 columns, tiles reflow to 2 then 1. Body scrolls inside the sheet (`overflow-y: auto`), header (search) and footer (hints) pinned.

### 1.3 Keyboard model

- Palette opens with search focused. `↑/↓/←/→` move a **roving focus ring** (`:focus-visible` = `2px solid var(--accent)`, `index.html:286`) across the grid; typing filters and resets focus to the first match.
- `Enter` on a tile → opens that tool's **input step** (§1.5). `⌘/Ctrl+Enter` → runs immediately with defaults (skips the form for tools that have sane defaults).
- `Esc` closes, returning focus to the composer with the `/` stripped. Full focus-trap while open; `Tab` cycles within the sheet only.
- Every tile is a real `<button>` — screen readers announce name + promise line via `aria-describedby`.

### 1.4 Search / filter

- Fuzzy match over tool name + promise line + registry keywords. Match highlights with `--accent-soft` background on the matched substring, not bold (bold shifts metrics; background doesn't).
- Empty result → **poetic empty state** (the system already has a voice for this, `.t-empty-poetry`, serif italic): *"No tool for that yet — describe it to Scout and she'll improvise."* with a one-tap handoff to the conversational path. This turns a dead end into the do-it-all promise.

### 1.5 Input collection — hybrid, tool-declared

Each registry tool declares an `inputMode`. Two forms, chosen per-tool by the Domain Strategist:

**(a) Inline form** — for tools with 1–3 structured inputs (Market Map: *thesis*, *segment*, *depth*). Renders as a compact card that **replaces the palette in place** (no new modal — the sheet morphs), so the user never loses context:

```
┌───────────────────────────────────────────────────────────┐
│  ← Market Map                                          esc  │
│  landscape + comps for a thesis                            │
│                                                             │
│  Thesis            ┌────────────────────────────────────┐  │
│                    │ AI-native underwriting for SMB…     │  │
│                    └────────────────────────────────────┘  │
│  Segment           ┌────────────────────────────────────┐  │
│                    │ fintech · insurtech                 │  │
│                    └────────────────────────────────────┘  │
│  Depth             ( ) quick   (•) standard   ( ) deep      │
│                                                             │
│                                   [ Cancel ]  [ Run ▸ ]     │  ← Run = accent button
└───────────────────────────────────────────────────────────┘
```

**(b) Conversational** — for open-ended tools (Deep Research, Design). Selecting the tile **pre-fills the composer** with a scaffolded prompt and focuses it — reusing the existing `promptScoutForWork(mode)` / `scoutStarterText(mode)` machinery (`index.html:19308–19335`), which already does exactly this for research/design/grill. The user finishes the sentence and sends. This is the path of least new code and it already feels good.

The Run button and the composer send both call `runGoalPipeline`. From the pipeline's perspective there is no difference between a form submit, a `/` command, and a voice request.

### 1.6 Running-state card — the 10 stages made visible

Once `/goal` starts, the tool produces a **running artifact card** in the thread (the system already writes running `os_artifact`s with `agentLoop`, `workflowStages`, `goalStatus`, `reviewGate`, `progressPercent` — context brief, Agentic section). This is the single most important surface-and-delight opportunity in the wave: the 10-step loop is *legible*, and each advance is a small, earned moment.

The 10 pipeline steps (context brief pillar 3) map to visible stages:

```
┌─────────────────────────────────────────────────────────────┐
│  ◆  Market Map · fintech underwriting            ⏸  ⋯        │  ← ember dot = in flight
│  ─────────────────────────────────────────────────────────  │
│  ●─────●─────●─────◍─────○─────○─────○─────○─────○─────○      │  ← stage rail
│  goal  break  assign exec  review gate  save  report verify ship│
│                    down            ↑ you are here             │
│                                                               │
│  ◍  Executing · drafting the competitive landscape            │  ← live stage line, --type-body
│     3 of 6 subtasks · gpt-5.5 · 00:42                         │  ← --type-numeric, tabular-nums
│                                                               │
│  ▸ show working                                          58%  │  ← disclosure + tabular % 
└─────────────────────────────────────────────────────────────┘
```

- **Stage rail:** ten nodes. States: `done` (filled ink `●`), `active` (ember ring `◍`, breathing), `pending` (hairline `○`). The connector between done nodes fills ember→ink as it completes. The active node uses the **`--breathe` amplitude system** so it pulses in sync with everything else on screen — one heartbeat (`--pulse-cycle`, `index.html:189`).
- **Advance animation** (the delight moment, §4 item 3): when a stage completes, the node does a single `scale(1) → 1.18 → 1` spring (`--ease-spring`, 260ms), the connector wipes ember left-to-right (300ms `--ease`), and the live-stage line cross-fades to the next (opacity+blur, 220ms). Ember on the just-finished node cools to ink over 400ms. **No confetti** — this fires up to 10× per run; it must stay a whisper.
- **`show working`** disclosure expands to a log of subtask lines (assign→agent, tool calls, review notes) using `comment-preview-open` keyframe (`index.html:4519`). Collapsed by default — honesty available, not forced.
- **Numbers** (`3 of 6`, `00:42`, `58%`) use `--type-numeric` / `tabular-nums` (skill rule #9) so the card never reflows as they tick.
- **Controls:** `⏸` pause (where the runner supports it), `⋯` for cancel / open-as-full-artifact. Cancel is a real state, not a hidden kill.

### 1.7 Terminal states

| State | Card treatment | Motion |
|---|---|---|
| **Complete** | Stage rail all ink-filled, header dot goes from ember → `--signal`-adjacent *warm gold* success tick, title gains a "▸ open artifact" affordance and result summary (the "report only what matters" step — one paragraph, not the whole log). | The **ember-settle**: the whole card does one gentle `translateY(2px)→0` settle (180ms) and the header emits a **single** ember-spark burst (§4 item 1). Fires once per completed goal — earns it. |
| **Needs review / gate** | Rail pauses at the `gate` node, which turns `--warn` amber. Inline "Review before shipping" with the diff/summary and **Approve ▸ / Send back** buttons. For external-write tasks this is the admin gate (`canApproveExternalWrites`, `index.html:16665`) — non-admins see "waiting on AJ." | Amber node breathes (not spins). No celebratory motion — this is a *stop*, and motion here would lie. |
| **Error** | Header dot → `--danger`. Rail freezes at the failed node (danger ring). Honest message ("Design agent timed out at execute") + **Retry from here** / **Restart** / **Ask Scout**. | Node does a 2px horizontal shake **once** (`--dur-fast`), then rests. Never loops. |
| **Empty (pre-run)** | The card doesn't exist until run; the composer just shows the pre-filled prompt. | — |

All terminal-state motion is gated by `prefers-reduced-motion` (the color/state change always applies; only the movement is dropped — extend the block at `index.html:14703`).

### 1.8 Mobile — quick-select as a bottom sheet

The palette is **wrong** as a centered modal on mobile. It becomes a bottom sheet rising from the composer, thumb-reachable, with the search pinned to the top of the sheet (right under the notch-safe area) and tiles in a single scrolling column of wider tiles.

```
        (thread scrolls, dimmed)
┌───────────────────────────────┐
│                               │
│  ⌕ build a market map…        │  ← pinned, autofocus (but see kb note)
│  ─────────────────────────    │
│  ┌───────────────────────────┐│
│  │ ◆  Market Map              ││  ← full-width tiles, 64px tall
│  │    landscape + comps       ││     (comfortable thumb target)
│  └───────────────────────────┘│
│  ┌───────────────────────────┐│
│  │ ✦  Pitch Deck              ││
│  │    10-slide story          ││
│  └───────────────────────────┘│
│  ┌───────────────────────────┐│
│  │ ⌕  Deep Research           ││
│  └───────────────────────────┘│
│         ▁▁▁▁ (grab handle)     │
└───────────────────────────────┘
```

- **Sheet mechanics:** rises with `bf-sheetin` (`index.html:3512`), grab handle at bottom for one-thumb dismissal, swipe-down to close. Backdrop `--scrim`.
- **Keyboard tact:** do **not** auto-open the keyboard on sheet-open (it would eat half the sheet before the user has seen the tools). First tap on search raises it; the sheet then rides above the keyboard via `interactive-widget=resizes-content` (already set, `index.html:5`) + `env(keyboard-inset-height)` fallback.
- **`/` trigger** still works on mobile (external keyboards, iPad), but the **button is primary** on touch.
- Running-state card is identical structurally; the stage rail collapses to a **5-node compressed rail** with a "3/10" counter on narrow screens, expanding on tap.

---

## 2. Scout do-it-all voice UX

Scout is not a feature; she's a **presence**. The private Realtime-2 session becomes a persona that can act across the whole OS. The UX job: make her actions *legible and trustworthy* — you always know what she's doing, on whose behalf, and you can stop her.

### 2.1 Entry point

Scout lives on the **office/home** surface, already scaffolded at `index.html:14940–14964`: the `office-launch__island` with the flame mark, the waveform button (`data-start-realtime-voice`), and the greeting line. This is the front door. Refinements:

- The greeting (`#officeLaunchGreeting`, currently "good morning.") becomes **time- and context-aware**: "good morning, AJ." / "welcome back — the fintech package advanced while you were out." Pulled from memory recall; falls back to the plain greeting keylessly.
- The waveform at rest **breathes** (existing `bf-wave` + `--breathe-rest`), a slow idle. Tapping it opens the session and the whole island lifts into the **voice island** persona.
- One-line affordance below: *"ask me to build, research, notify the team, or grill you."* — sets the do-it-all expectation without a wall of help text.

### 2.2 The voice island as Scout's body

The existing `#voiceIsland` (`index.html:15907`) is a top-center glass pill with `data-state` driving everything (idle/listening/hearing/thinking/talking/error). We **extend its state vocabulary** to carry the do-it-all actions. It stays one component; we add states and a narration line.

**State inventory (extends `index.html:5612`):**

| `data-state` | Wave | Label / meta | Feel |
|---|---|---|---|
| `idle` | slow rest breathe | "Scout" / "tap to talk" | Dormant, warm. |
| `listening` | gentle idle bars | "Scout" / "listening…" | Open, waiting. Green-adjacent *neutral* — she is not "live-speaking," so **not** `--signal`; use `--text-2`. |
| `hearing` | bars react to **your** input level | "Scout" / "…" (live partial transcript) | She's catching your words — bars driven by mic RMS. |
| `thinking` | bars collapse to a **traveling dot** (`bf-think`, `index.html:7446`) | "Scout" / "thinking…" | A pause with dignity. No spinner. |
| `acting` **(new)** | ember-tinted bars + a small **task chip** | "Scout" / "opening the board…" | The do-it-all state. Ember = machine working (§0). |
| `talking` | bars animate to **her** output | "Scout" / (caption of what she's saying) | She has the floor. |
| `hand-raised` **(new)** | bars still, single **amber pulse** | "Scout · needs you" / "post to #dealflow as you?" | She's paused for consent (post-as-user, external write). |
| `error` | bars flat, danger edge (exists, `index.html:5618`) | "Scout" / honest error | Something failed; she says why. |

The island's border/glow shifts per state using existing patterns: `--glass-shadow` at rest, `--shadow-2` add on active (already `index.html:5615`), `--danger` edge on error. **New:** `acting` and `hand-raised` add a subtle ember / amber edge respectively via `color-mix` (matching the existing `error` treatment's approach).

### 2.3 Narrating multi-step action

When Scout takes an action, the **meta line** (`#voiceIslandMeta`) becomes a live action narration, and she says it aloud. Every action follows the same three-beat rhythm so it's predictable:

```
   announce            →     act        →      confirm
"opening the board…"      (state=acting)     "board's open."   (state=talking)
"posting to dealflow…"    (state=acting)     "posted."          + toast
```

- **Announce before act**, always. She never silently does a thing and reports after — the announce is what makes an autonomous agent feel like a colleague instead of a poltergeist.
- Each completed action also drops a **durable toast** (existing `toastRegion`, `index.html:15919`) and, where it wrote something (a post, a notification), a link to it. Voice is ephemeral; the toast is the receipt.
- Chained actions narrate as a **sequence**, not a dump: "opening the board… done. now pulling the fintech card…" — each with its own announce/confirm, so a 4-step task reads as four small certainties.

### 2.4 Disclosure — posting as you

The highest-trust action: Scout starting chats and **posting on your behalf**. The UX must make authorship unambiguous. Three layers:

1. **Consent at the moment** (first time in a session, or always for external writes): `hand-raised` state — "post this to #dealflow as you? *[reads the draft aloud]*" with voice "yes/no" or a tap. For external writes this is the existing admin gate; Scout narrates the wait honestly ("that needs AJ's sign-off — I've queued it").
2. **Attribution on the artifact:** anything Scout posts as you carries a visible **"via Scout"** chip next to your name in the thread/channel — never hidden. Uses the existing attribution treatment (`attribution-fade`, `index.html:11527`). The team always knows a message was Scout-authored on your behalf.
3. **Session ledger:** the island's `⋯` (or long-press) opens **"what Scout did"** — a reverse-chron list of this session's actions with undo where reversible (delete the post, dismiss the notification). This is the trust backstop: full recall of her autonomy, one tap away.

### 2.5 Reading responses aloud

When Scout recalls information or a thread reply comes in, she reads it — but **summarized, not verbatim**, and with a visible caption so you can read along or mute. The caption uses the existing `scout-caption` component (`index.html:14772` references `.scout-caption__dot`). A `speaker` glyph in the island toggles read-aloud off for the session (honesty: if you muted her, she shows muted, she doesn't secretly keep talking).

### 2.6 Grill-mode ritual — the set piece

Grill mode run *by Scout herself* is the wave's signature theatrical moment. It has three acts and the UI ceremonially shifts between them. This is the one place we spend real motion budget.

**Act I — Pitch phase.**
The island expands into a **grill stage** (a larger centered surface, `bf-islandin` entrance, `index.html:3513`). Scout: "Okay. Pitch me. You've got two minutes — what are we building?" A calm **timer ring** (tabular, `--type-numeric`) counts your two minutes. Bars react to *your* voice (`hearing`). The stage is quiet, warm, low-pressure — deliberately un-adversarial so you open up.

```
┌──────────────────────────────┐
│           ◆ grill             │
│                               │
│        ⟳ 1:47                 │  ← timer ring, tabular
│                               │
│      ▁▃▅▇▅▃▁  (your voice)     │
│                               │
│   "pitch me — what is it?"    │
│                               │
│         [ done ▸ ]            │
└──────────────────────────────┘
```

**Act II — Grill phase.**
Tone shift, signaled by motion: the stage **cools** (background deepens one step), the ember accent **sharpens** to full strength, and Scout's cadence gets faster. She fires questions — one at a time, each appearing as a **struck line** (`card-place`, `index.html:11532`). "Who's the buyer. Not the user — the buyer." She interrupts hedging. A subtle **pressure meter** at the edge (not a health bar — a warm-to-hot gradient) reflects intensity so it feels like a real grilling with stakes, not a quiz. The `--breathe` amplitude goes to `--breathe-live` here — the room leans in.

**Act III — Scorecard reveal.**
The climax. The stage does a single **assemble** animation (`stage-arrive`, `index.html:11184`): the scorecard builds in staggered rows (100ms stagger per skill rule #5) — Clarity / Market / Moat / Ask / Conviction, each with a score that **counts up** (tabular, ~600ms ease-out) and a one-line note. A verdict line in serif (`.t-display`) lands last: *"Strong on story. Thin on the moat. Fix that before the room."* The scorecard becomes a **first-class `os_artifact`** so it's recallable later ("Scout, what did you ding me on last time?").

```
┌──────────────────────────────┐
│         grill · scorecard     │
│  ───────────────────────────  │
│  Clarity     ████████░░  8     │  ← rows stagger in, scores count up
│  Market      ██████░░░░  6     │
│  Moat        ███░░░░░░░  3  ⚠  │  ← weak scores flagged --warn
│  Ask         ███████░░░  7     │
│  Conviction  █████████░  9     │
│  ───────────────────────────  │
│  "Strong on story. Thin on    │  ← serif verdict, lands last
│   the moat. Fix that."        │
│           [ save ]  [ again ] │
└──────────────────────────────┘
```

Reduced-motion: all three acts keep their *state* changes (tone, color, content) but drop movement — the scorecard appears assembled, scores show final values, no count-up.

### 2.7 What it should feel like

A sharp colleague who happens to live in the machine. At rest: warm and unobtrusive. Working: transparent — you always see the verb ("opening…", "posting…"). Acting on your behalf: scrupulously honest about authorship. Grilling: theatrical but never cruel, and the scorecard is a gift, not a judgment. The emotional arc of a Scout session should be *"I felt accompanied, and I got something done I couldn't have done alone as fast."*

---

## 3. BonfireOS rename + shell polish

### 3.1 Exact label changes

The "Office" tab becomes **BonfireOS** everywhere it surfaces. Concrete edits:

| Location | Current | New | Line |
|---|---|---|---|
| Rail button label chip | `office` | `BonfireOS` | `index.html:14793` |
| Rail button `aria-label` | `Home` | `BonfireOS home` | `index.html:14791` |
| `toolTitles` map | `office: 'Office'` | `office: 'BonfireOS'` | `index.html:19217` |
| Phone office title (the special-case) | `'bonfire'` | `'BonfireOS'` | `index.html:19232` |
| Topbar back button `aria-label` | `Back to the office` | `Back to BonfireOS` | `index.html:14891` |
| `applyStatusPillForTool` fallback | `'office ready'` | `'BonfireOS ready'` | `index.html:19267` |
| Office launch section `aria-label` | `Office` | `BonfireOS` | `index.html:14937` |
| `syncToolTopbar` default fallback | `|| 'Office'` | `|| 'BonfireOS'` | `index.html:19233` |

**Casing rule:** the product name is **`BonfireOS`** (camel, no space) in titles and the rail chip. The lowercase-label house style (`chat`, `board`, `memory`, `office`) is a system convention (`.tool-rail__label`), so the one exception — a proper product name in mixed case — should be *intentional and consistent*, not accidental. Grep for the string `'Office'`, `"office"` label usages, and the phone `'bonfire'` special-case before shipping; there is exactly one phone special-case and it's easy to miss.

**Do not** rename the internal tool id `office` (it's the `data-tool` key wired through `TOOL_IDS`, `osToolIds`, `setActiveTool`, `appShell.dataset.tool`, and dozens of `[data-tool="office"]` selectors). Rename the **label**, never the **key**. This is the single highest-risk line-item in the rename; the id is load-bearing.

### 3.2 Shell polish pass — concrete CSS-level refinements to hit A++

The rail and topbar are already strong. A++ is in the last 10%. Specific, named changes:

1. **Rail active-state weight.** Currently `.tool-rail__tool` transitions `all 0.22s` (`index.html:375`) — this violates "never `transition: all`" (skill rule #14) and animates layout props. Change to `transition: color, background, box-shadow var(--dur-med) var(--ease)`. Active tool gets a **left ember hairline** (2px, `--r-full`, inset) as the "you are here" mark instead of relying on color alone — reads at a glance, on-brand, and the only ember in the chrome.
2. **Label flyout timing.** The glass label chips (`index.html:384`) should reveal on hover **after a ~350ms delay** (so a fast mouse-through doesn't flicker every chip) but **instantly** on keyboard focus (a11y — focus needs immediate feedback). Two different `transition-delay` values keyed on `:hover` vs `:focus-visible`.
3. **Concentric radii audit.** The rail tool is `--r-md` (12) inside a 60px column; the label chip is `--r-md`. Fine. But verify the account avatar well and bell badge use radii that nest correctly with their containers — the badge (`.notification-bell__unread`) sitting on a `--r-md` button should be `--r-full`, which it is. Audit pass, likely no change.
4. **Topbar heading optical rhythm.** `.topbar__heading` mixes `#topbarProject` and `#topbarToolTitle`. With the rename, the tool title carries the weight. Set `text-wrap: balance` on the heading and confirm the `--type-title-2` tracking (`--track-title-2: -0.016em`) reads tight at the new "BonfireOS" length.
5. **Bell warmth.** The notification bell badge is currently the neutral unread pill. Give an **unread** badge a faint `--glow-accent`-scale warmth (see §4 item 4) so a new alert has a whisper of heat — earned attention, not a red-dot scream.
6. **Card hover consistency.** Grep shows `.card:hover { transform }` handled in reduced-motion but confirm all interactive cards (office launch, tool tiles, artifact cards) share **one** hover recipe: `translateY(-2px)` + `--shadow-2`, `--dur-fast`. Right now composers and cards have slightly divergent hover treatments; unify to one token-driven recipe.
7. **Press feedback everywhere.** Audit that all primary buttons use `active { scale(var(--press-scale)) }` (0.97, `index.html:176`) — the voice island main already does (`index.html:5633`). Tool tiles, Run button, palette tiles must too (skill rule #12). One shared `.pressable` utility class.
8. **Focus-visible on glass.** The global `:focus-visible` is `2px solid var(--accent)` — on the glass palette over a scrim, `--accent` (ink) can be low-contrast. Add `outline-offset: 2px` (already there) and a `--glow-accent` fallback shadow so focus reads on glass. Verify in both themes.

---

## 4. Surprise-and-delight inventory

Ranked by **earn-rate** — frequency-adjusted payoff. High earn-rate = rare + meaningful (spend budget). Low = frequent + risks annoyance (spend nothing or near-nothing). Every item respects `prefers-reduced-motion` by keeping the *state change* and dropping the *movement*.

| # | Moment | Trigger | Frequency | Motion spec | Earn |
|---|---|---|---|---|---|
| 1 | **Artifact-complete ember burst** | A `/goal` run reaches Complete | Rare (a few/day) | Single ember spark: 5–7 particles from header dot, `translateY`/opacity out over 520ms `--ease-out`, no loop. Card settles `translateY(2px)→0` 180ms. | ★★★★★ Rare + it's the payoff of real work. Spend here. |
| 2 | **Grill scorecard assemble** | Grill Act III | Rare | Rows stagger 100ms; scores count up 600ms tabular; serif verdict lands last (blur 4→0, `index.html:47` icon recipe applied to text). | ★★★★★ Set-piece. The one place to be theatrical. |
| 3 | **Stage-advance glow** | Each of 10 pipeline stages completes | Medium (≤10/run) | Node spring `1→1.18→1` 260ms `--ease-spring`; connector wipe ember→ink 300ms; live-line cross-fade 220ms. Ember cools to ink 400ms. | ★★★★☆ Must stay a whisper — fires often. Quiet on purpose. |
| 4 | **Notification bell warmth** | New unread arrives | Medium | Badge fades in with a 1-cycle `--glow-accent`-scale ember pulse (one breath, `--pulse-cycle`), then rests warm. Never loops. | ★★★★☆ A red-dot scream 50×/day is hostile; one warm breath is not. |
| 5 | **Scout wake breathe** | Tapping the office waveform | Medium | Island lifts via `voice-island-enter` (exists, 220ms); bars transition rest→listening amplitude. | ★★★★☆ Already half-built; the rest→live `--breathe` flip sells "she woke up." |
| 6 | **Package stage-advance** | A package moves stage (e.g. Ideation→Packaging) | Rare | The stage pill fills left-to-right (300ms `--ease`), a soft ember sweep crosses the package card once (`card-place`-adjacent). | ★★★★☆ Meaningful milestone, infrequent. |
| 7 | **Tool palette open** | `/` or `+Tools` | High | Sheet `bf-popin`/`bf-sheetin` (exists); tiles stagger in 40ms each, cap at ~8 (never wait on a 30-tile cascade). | ★★★☆☆ Frequent — keep it fast (<220ms total) or it's friction. |
| 8 | **Message send** | Composer send | Very high | Send glyph `scale(0.97)` press only; the sent bubble uses `assistant-message-in` (exists, `index.html:4514`). Nothing more. | ★★★☆☆ 100×/day — near-zero motion is correct. |
| 9 | **Theme toggle cross-fade** | Theme switch | Low | Already canon: 0.4s base-coat cross-fade (`index.html:275`). Sun/moon icon cross-fades (opacity+scale+blur, skill rule #7). | ★★★★☆ Rare, delightful, already partly there. Polish the icon swap. |
| 10 | **Room-entry choreography** | Joining the room | Low | Exists (`rise-in` stagger, `index.html:14682`). Leave it — it's already good. | ★★★★☆ Rare, ceremonial, done. Don't touch. |
| 11 | **"via Scout" attribution land** | Scout posts as you | Medium | Chip fades in `attribution-fade` (exists) beside the name. No extra flourish — it's a trust signal, not a toy. | ★★★☆☆ Honest, quiet by design. |
| 12 | **Slop quarantine slide** | Item moved to discarded archive | Rare | Item does a subtle exit: `translateY(8px)` + fade 220ms (subtle-exit, skill rule #6), lands in the "discarded" count which ticks up (tabular). | ★★★☆☆ Makes an invisible policy visible without alarm. |
| 13 | **Voice island thinking dot** | Scout `thinking` | Medium | `bf-think` traveling dot (exists) — a dignified pause, not a spinner. | ★★★★☆ Replaces the one thing that would cheapen her: a loader. |
| 14 | **Copy-link confirm** | Copy room/board link | Low | Button label swaps to "copied ✓" for 1.4s, icon cross-fades (rule #7), no bounce. | ★★★☆☆ Tiny honesty of feedback. |
| 15 | **Empty-state serif** | Any empty list/result | Low | `.t-empty-poetry` serif italic fades in (`bf-fade`). Static — the delight is the *voice*, not motion. | ★★★★☆ Cheap, characterful, ownable. |

**The strict taste rule — when NOT to animate:**

> Do not animate anything that (a) happens more than ~once per minute in normal use, unless the animation is ≤120ms and non-looping; (b) stands in for unknown progress (use a real state or a dignified pause, never a spinner); (c) would draw the eye away from where the user is actually working; or (d) fires on page load (`initial={false}` equivalent — the mount-stagger already guards this, `index.html:14692`). When in doubt, animate the **state** (color, content) and leave the **position** alone. Green stays reserved for live/speaking; ember stays reserved for agent-working; nothing else earns a hue.

All 15 must appear in the reduced-motion block at `index.html:14703` (state kept, motion dropped) before ship. This is a review-checklist item, not optional.

---

## 5. Mobile parity

Mobile currently lags in four named surfaces. Each gets a native-quality spec, not a media-query shrink.

### 5.1 Drawer nav (the rail on mobile)

The 60px rail is a desktop instrument. On phone it's a **drawer** (the code already has `closeMobileToolRailForSelection`, `index.html:19291`, and a mobile tool-rail concept).

- **Trigger:** swipe from the left edge, or tap the brandmark. Opens a glass drawer (`--blur-panel`) with full-width rows: icon + label + unread count, comfortable 56px rows.
- **Dismiss:** swipe-left, tap-scrim, or select-to-navigate (existing behavior). Rides `bf-slidein` (`index.html:12184`).
- **Safe area:** top padding `env(safe-area-inset-top)`, bottom `env(safe-area-inset-bottom)` so the last row and the account chip clear the home indicator.
- The rename lands here too — the drawer header reads **BonfireOS** with the flame mark.

### 5.2 Bottom bar

- A **floating tab bar** for the 3–4 primary destinations (BonfireOS / Room / Chat / Board), glass, `--r-full` ends, riding above the home indicator (`padding-bottom: env(safe-area-inset-bottom)`). The code already references "floating tab bar" clearance (`index.html:14322`).
- Active tab: the ember "you are here" hairline from §3.2, adapted to a top-edge mark on the bar.
- Touch targets ≥ 44×44 (`--hit-min`); no two hit areas overlap (skill rule #16).
- The bar **hides on scroll-down, reveals on scroll-up** (a native pattern) so it never covers content while reading a long thread — but always returns instantly on any upward intent.

### 5.3 Thread composer (the hardest mobile surface)

The composer + keyboard interaction is where mobile web usually falls apart. Spec:

- **Keyboard-avoidance:** the composer is pinned to the visual viewport bottom and rides *above* the keyboard using `interactive-widget=resizes-content` (already set, `index.html:5`). Fallback for browsers without it: `VisualViewport` API listener adjusting a `--kb-inset` custom property. The composer must **never** slide under the keyboard (the code already fought this, `index.html:13283–13297` — reuse that safe-inset approach).
- **Safe area:** `padding-bottom: max(env(safe-area-inset-bottom), 8px)` so it clears the home indicator when the keyboard is down.
- **Growth:** textarea auto-grows to ~5 lines then scrolls internally (existing `resizeScoutChatInput`, `index.html:19328`), so the thread above never gets crushed to 60px (a bug the code comments already flag, `index.html:6685`).
- **`+Tools` and send** are ≥44px thumb targets, flanking the field; the `/` trigger still works with external keyboards.
- **Attachment chips** wrap above the field, not beside it, so they never squeeze the input on narrow screens.

### 5.4 Settings (mobile)

- The settings dialog's **left nav** (`index.html:16026`, profile/account/audio&video/appearance) becomes a **top segmented control or a stacked accordion** on phone — a vertical side-nav wastes the narrow width.
- Each section is full-width, single-column. The audio & video controls (§6) get large tap targets — the noise-mode radios become **full-width option rows** (the whole row is the hit area, not just the 20px radio).
- The dialog is a **full-height sheet** on phone (not a centered modal), rising with `bf-sheetin`, dismiss by swipe-down or the existing close button.
- Live preview (§6 video look) sits at the top, sticky, so the user sees the effect while scrolling options.

**Mobile review rule:** every new surface in this wave ships with a phone spec *before* it ships desktop-only. The palette (§1.8), the voice island (already fluid, `index.html:5889`), the /goal card, and settings all have explicit phone forms above. "We'll do mobile later" is how mobile lags — parity is a gate, not a follow-up.

---

## 6. Settings honesty for AV

The audio & video section (`index.html:16085`) must report the **true** state, per device, with no fake reassurance. Current state: three noise-mode radios (voice-focus / standard / off) and a voice-focus training block. Spec to make it honest:

### 6.1 True noise-suppression state per device

Each mode row shows its **real, resolved** status — not just which radio is selected, but what the browser actually granted on *this* device. Four honest states:

```
┌─────────────────────────────────────────────────────────────┐
│  noise reduction                                             │
│                                                              │
│  (•) voice focus          ● active · RNNoise worklet         │  ← green dot = confirmed running
│      stronger local cleanup for noisy rooms                  │
│                                                              │
│  ( ) standard cleanup     ◐ fallback · browser NS only       │  ← amber = degraded/fell back
│      browser echo cancellation and noise suppression         │
│                                                              │
│  ( ) raw mic              ○ off                               │  ← neutral = intentionally off
│      no added cleanup beyond the hardware                    │
│                                                              │
│  ⚠ voice focus unavailable on this device — AudioWorklet     │  ← honest failure, only when true
│    blocked. Using standard cleanup instead.                  │
└─────────────────────────────────────────────────────────────┘
```

| State | Dot | Meaning | Source |
|---|---|---|---|
| **active** | `--live` green | The selected mode is confirmed running (worklet loaded, graph built) | health diagnostics `index.html:22896–22979` |
| **fallback** | `--warn` amber | Requested mode failed; a lesser mode is actually running. Says which. | fallback ladder `index.html:23313–23414` |
| **unavailable** | `--text-3` + ⚠ | This device can't do the requested mode at all (no worklet, no getUserMedia constraint). Disables the radio with an honest reason. | constraints ladder `index.html:16866` |
| **off** | hairline `○` | Intentionally off. | user choice |

The dot is driven by the *actual* audio-graph state (`createOutboundAudioForSource`, context brief), polled/pushed from the existing health diagnostics — **never** by which radio is checked. If voice-focus is selected but silently fell back, the row shows amber "fallback," not green. This is the core honesty contract.

### 6.2 Video look picker with live preview

Lightweight looks only this wave (no ML segmentation — user-confirmed, context brief §2). The picker:

```
┌─────────────────────────────────────────────────┐
│  video look                                      │
│  ┌───────────────────────────────────────────┐  │
│  │                                           │  │  ← LIVE self-preview, sticky on mobile
│  │         [ your camera, filtered ]         │  │
│  │                                           │  │
│  └───────────────────────────────────────────┘  │
│  ( none )  ( warm )  ( crisp )  ( soft )  ( studio )│  ← look chips, selected = accent
│                                                  │
│  brightness  ────●────────                       │  ← lightweight adjustments
│  warmth      ──────●──────                        │
│                                                  │
│  ✓ saved for this device                         │
└─────────────────────────────────────────────────┘
```

- **Live preview** updates in real time as you pick a look or drag a slider — you see exactly what the room will see. The preview is your actual camera through the actual CSS/canvas filter pipeline (the "looks"/touch-up path the RTC engineer owns), so there's zero gap between preview and reality.
- Looks are **presets over the same lightweight adjustments** (brightness/warmth/contrast/soften) — picking "warm" just sets the sliders, which stay visible and adjustable. No hidden magic.
- Selected chip uses `--accent`; the preview frame gets a subtle 1px outline (`rgba(255,255,255,0.1)` dark / `rgba(0,0,0,0.1)` light — skill rule #11, pure black/white, never tinted).

### 6.3 Per-device persistence indication

The system already persists per-account+device (`bonfire.audio.settings.v1`, preferred mic, `index.html:16815, 17854`). Make persistence **visible and honest**:

- A quiet **"✓ saved for this device"** line under each device-specific control (mic choice, noise mode, video look), in `--type-caption` `--text-3`. It appears on change, confirming the setting stuck to *this* browser/device — not silently global.
- **Cross-device honesty:** if the user is on a new device, show "using defaults on this device" instead of pretending their laptop settings followed them. When they change something, it flips to "saved for this device."
- The **preferred mic** shows its remembered choice and, if that device is currently unplugged, an honest "your usual mic (AirPods) isn't connected — using MacBook mic" rather than silently swapping.

The through-line: **the settings surface never claims a state the audio graph isn't actually in.** Selected ≠ active; the UI shows active.

---

## 7. Working inside the monolith

A++ inside one 34,900-line `index.html` with no build step is a discipline problem, not a talent problem. The rules that make it tractable:

### 7.1 Token discipline

- **Never hardcode a value that a token exists for.** Colors, radii, spacing, shadows, durations, eases all come from the live token table (`index.html:28–190`). A raw `#FF7A2B` or `16px` in a new rule is a bug — grep for hex literals and bare px in any diff before ship.
- **The one new token family** this wave introduces is the ember accent, scoped and named for its job, added to *both* the `:root` (light) and `[data-theme="dark"]` blocks:
  ```css
  /* agent-working accent — the ONE warm hue. Green owns live/speaking;
     ember owns "a machine is doing work." They never co-occur. */
  --agent: #FF7A2B;               /* ember (heritage brand flame) */
  --agent-soft: rgba(255,122,43,0.14);
  --glow-agent: 0 0 0 1.5px var(--agent), 0 4px 20px rgba(255,122,43,0.28);
  ```
  Defining it once, in the token table, means the /goal card, the palette active-tool mark, the bell warmth, and Scout's `acting` state all pull from one source. Change the ember once, it changes everywhere.
- **Reduced-motion is a token behavior.** The kit zeroes `--dur-fast/med/slow` under `prefers-reduced-motion` (`index.html:237`), so any transition expressed in *token time* self-heals. Hardcoded-duration animations (keyframes) must be added by hand to the block at `index.html:14703`. Rule: **prefer token-timed transitions over keyframes** wherever the effect is a state change, precisely so reduced-motion is free.

### 7.2 Section conventions

- The file is organized `<style>` (tokens → base → components) then `<body>` markup then `<script>`. **New CSS goes in a labeled section** with a banner comment matching the existing style (e.g. `/* ---------- Tool palette (Spectacular OS) ---------- */`), grouped near related components (palette near composer, /goal card near artifact cards).
- **New component classes get a namespace prefix** to prevent collisions in a file this large: `.palette__*`, `.goalcard__*`, `.grillstage__*`. The codebase already does this (`.tool-rail__*`, `.voice-island__*`, `.agent-composer__*`) — follow it exactly. A flat class name like `.tile` or `.card-2` in a 34.9k-line file is a time bomb.
- **JS state follows the existing pattern:** module-scope `let` declarations grouped with the others (`index.html:16593–16656`), functions named by verb (`openToolPalette`, `runGoalPipeline`), wired through the existing `appShell.dataset.tool` state machine rather than a parallel one. The palette and /goal card are *views onto existing state* (`os_artifact`s, the tool registry), not new stores.

### 7.3 Avoiding CSS collisions

- **Prefer BEM-ish scoped classes over element/descendant selectors.** `.palette__tile` not `.palette button`. In a monolith, `.palette button` will eventually match something you didn't mean.
- **Never restyle a shared primitive to fit one surface.** If `.btn--primary` needs to look different in the palette, add `.palette .btn--primary` or a modifier, don't touch the base.
- **`z-index` is a scarce shared resource.** The system has an implicit stack (rail `z-90`, voice island `z-1350`, `index.html:5575`). New floating surfaces must slot into that ladder deliberately — the palette sits below the voice island (Scout stays reachable over the palette) but above the rail. Document the chosen value in a comment.
- **`transition: all` is banned** (skill rule #14, and the existing `.tool-rail__tool` at `index.html:375` violates it — fix in this wave). Always name properties.

### 7.4 Review checklist for any wave touching index.html

Every diff to this file passes this gate before ship:

- [ ] No hardcoded colors/radii/spacing/durations that a token covers (grep hex + bare px).
- [ ] New classes are namespaced (`__` scoped), no bare `.tile`/`.card` collisions.
- [ ] Concentric radii on all nested rounded elements (`outer = inner + padding`).
- [ ] Every new animation added to the `prefers-reduced-motion` block (`index.html:14703`) — state kept, motion dropped.
- [ ] No `transition: all` — properties named explicitly.
- [ ] Dynamic numbers use `tabular-nums` / `--type-numeric`.
- [ ] Interactive elements ≥ 44×44 hit area (`--hit-min`); no overlapping hit areas.
- [ ] `:focus-visible` legible in **both** themes and over glass/scrim.
- [ ] Green (`--signal`) used only for live/speaking; ember (`--agent`) only for agent-working; no other new hue.
- [ ] A **phone spec exists** for every new surface (drawer/sheet/keyboard-avoidance), not just a desktop layout.
- [ ] `go test ./...` passes (frontend-latency and guard tests assert from Go — the `--speaker-accent` and room-entry lines are test-pinned, `index.html:186`).
- [ ] Keyless-local still boots (`go run .`, Scout 503s cleanly) and the native `/native/config` contract is untouched.
- [ ] `data-tool` **keys** unchanged (only **labels** renamed) — the `office` id stays.

---

## Summary — five key design calls

1. **One new accent with one job.** Ember (`--agent`) means "a machine is working"; green (`--signal`) keeps its monopoly on "a human is live." This delivers the "warm fire accents" mandate as *meaning*, not decoration, and ties to the flame brandmark — without forking the neutral glass-and-ink system.
2. **Three doors, one pipeline.** The `+Tools` button, `/`-command, and Scout voice all render the same tool registry and converge on one `runGoalPipeline` — and the **10-step loop is made legible** as a breathing stage rail on the running-artifact card, with honest complete/gate/error states.
3. **Scout is a legible presence, not a poltergeist.** She announces before she acts ("opening the board…"), carries a visible **"via Scout"** attribution when posting as you, gates consent with a `hand-raised` state, and keeps a one-tap **"what Scout did"** ledger with undo. Grill mode is the one theatrical set-piece (pitch → grill → scorecard reveal).
4. **Honesty is the settings UX.** AV settings show the **actual audio-graph state** (active / fallback / unavailable / off) per device — selected ≠ active — with live video-look preview and visible per-device persistence. No fake spinners anywhere; unknown progress becomes a dignified pause, never a loader.
5. **Discipline makes A++ survivable in a 34.9k-line file.** Namespaced classes, token-only values, one new scoped accent token, reduced-motion as a gate not a follow-up, mobile-spec-before-ship, and a 13-point review checklist — rename **labels** only, never the load-bearing `office` **key**.

**Deliverable written to `docs/plans/spectacular-os-ux.md`.**
