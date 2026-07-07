# Glass & Ink Reskin — Master Defect Inventory (hand-off)

**Synthesis of 5 surface audits** — Shell/Home/Login, Chat, Intelligence/Deliverables, Board/Memory/Files, Room/Notifications/Settings/Menus.
**Base file (all line anchors):** `/Users/ajhart/meetingassist/.claude/worktrees/glass-ink-reskin/index.html`
**Method:** CSS anchors read from source; colors pixel-sampled from live captures in `/Users/ajhart/meetingassist/.playwright-mcp/audit/`. Where a hex is cited it was measured, not guessed.

> **ID-collision note.** IDs are unique *within* a surface, not across. Four collide across files and are surface-prefixed everywhere they leave their own table: `chat/AL-*` (agent-letter card) vs `intel/AL-*` (artifact library); `chat/MM-*` (@mentions) vs `board/MM*` (memory empty); `board/X*` (cross-cutting) vs `room/X-*` (systemic). The ember token itself is filed five times — `intel/EM-1` (P0), `chat/SYS-1`, `board/X1`, `room/E-1` (P1) — all the same defect.

---

## 1. Executive summary

**Total rows: 211.** Of these, **183 are actionable defects** and **28 are explicit compliance confirmations** (surfaces the auditors verified correct — the do-not-regress list, §6).

| Severity | Count | What it means |
|---|---|---|
| **P0** | 4 | Broken / crown-jewel-buried / loudest brand violation. Ship-blockers. |
| **P1** | 33 | On the primary surfaces a user lives in; visible, off-canon, cheap-to-medium fixes. |
| **P2** | 76 | Real law violations, one layer down or on lower-traffic states. |
| **P3** | 70 | Polish, token hygiene, naming residue, radius-off-grid, confirmations-with-a-nit. |
| _compliant_ | 28 | Already on-law; preserve through the reskin. |

**Verdict.** The reskin is *one root cause away from most of its wins*: the retired ember token `--agent:#FF7A2B` was never actually removed — it is still declared live in both `:root` blocks (lines 232-234 light, 284-286 dark) and consumed in ~50 places, so every "a machine is working" cue (goalcard, palette, nav marker, voice island, bell badge, deal-room, package pipeline) still burns orange. A single token-level redefinition cascades across all five surfaces. Beyond ember, the defects cluster into a small number of systemic laws being broken repeatedly — undefined `--font-serif` koans, sans/uppercase system labels, missing glass material on menus/dialogs, ghost-gray primaries, markdown leaking into memory rows, and the mobile floating-dock overlapping content — plus two genuine "it's broken" surfaces (the keyless room paints a **blank white void**, and the packaging deliverable opens as a **monospace run-log instead of the deck**). Fix the ~10 cross-cutting root causes in §2 and the P0/P1 build list in §3, and the bulk of the 211-row inventory clears.

---

## 2. Cross-cutting root causes (highest-leverage fixes)

These are defects that recur across many surfaces. Fix at the token/law level and the per-surface symptoms mostly evaporate.

### RC-A — Retired ember `--agent:#FF7A2B` is still LIVE (the dominant defect family)
`--agent`, `--agent-soft`, `--glow-agent` are declared in **both** `:root` blocks (**232-234 light / 284-286 dark**) with a doctrine comment enshrining ember as "the ONE warm hue / a machine is working." The retire order was never executed. ~50 `var(--agent)` consumers still burn orange.
**Fix (one edit cascades):** redefine `--agent`→`var(--accent)`, `--agent-soft`→`var(--accent-soft)`, `--glow-agent`→ a monochrome glow (or `--info #0A84FF` where "machine working" needs a hue); delete the doctrine comment and the `is-hot-ember` class name. For any "working / in-progress" state, the sanctioned replacement is a **mono breathing dot at 2400ms** or `--info`. Also sweep the related off-token warm-red `#FF6B61` → `--danger #FF453A` (room dock).
**Affected IDs:** token itself — `intel/EM-1` (P0), `chat/SYS-1`, `board/X1`, `room/E-1`. Consumers — `shell/TR1,H1,H2,H7,H9,GM1,OA1,OA2,OA4`; `chat/CP-1(P0),CP-2,MO-2,GC-1,CR-1,CR-3`; `intel/EM-2,EM-4,EM-5,EM-6,PK-5`; `room/E-2,N-1,D-1,D-2`. Naming residue — `shell/TR4`, `room/E-3`, `board/FL3`.

### RC-B — Undefined `--font-serif` → Georgia fallback in every empty-state koan
`--font-serif` is never defined, so every `.t-empty-poetry` / `.intel-empty` koan renders **Georgia serif-italic** — a foreign typeface inside a Geist mono/sans system.
**Fix:** either define a real serif token (if a serif voice is wanted) **or** move all koans to the mono system voice. One decision resolves them together.
**Affected IDs:** `chat/CP-4`, `intel/LT-4`, `intel/PK-3` (and every other `.intel-empty` koan).

### RC-C — Mono-label law violations (system text in sans / uppercase / timestamps not mono)
System labels must be lowercase Geist Mono via `--type-label`. Repeatedly they render sans, Title-case, or UPPERCASE; standalone timestamps render sans instead of mono.
**Fix:** route all system labels through `--type-label` (mono, lowercase, tracked); wrap standalone timestamps in a mono span.
**Affected IDs:** `shell/H7,H11`; `chat/CP-3,CP-8,CR-1`; `intel/DL-1,DL-2,IP-6,LT-2`; `board/B1,B3,MS2`; `room/N-3,AM-6`.

### RC-D — Voice: Title-case button verbs & camelCase brand status (lowercase law)
Primary-button verbs and the idle status string violate the lowercase system voice; two different idle strings coexist.
**Fix:** lowercase all UI verbs ("save profile", "upload avatar", "change password", "done", "join the room", "send notes"); lowercase topbar page titles ("room"/"memory"); unify the idle status on one lowercase string ("not connected") and **retire "BonfireOS ready"**.
**Affected IDs:** `room/X-2,R-2,R-6,R-7,D-5,S-2`; `shell/H5,TB1`; `chat/CP-8`; `board/BE3,MS3`.

### RC-E — Glass material not applied where the kit requires it
Menus and hero surfaces render as flat opaque slabs (no `backdrop-filter`, no `--glass-highlight` inset) where the kit calls for liquid glass — while sibling surfaces on the same screen *are* glass.
**Fix:** apply `--glass-chrome` + `--glass-blur` + `--glass-highlight`/`--glass-shadow` to floating **menus**; strengthen the ambient behind the login gate so its coded glass reads; consider glass on the hero deliverable drawer. **Doctrine to reconcile:** the notification panel (`room/N-4`) and settings dialog (`room/S-3`, opaque = *sanctioned*) disagree with the board's "Dialogs = glass" claim (`board/CD1`) — settle **menus = glass, dialogs = opaque**, then apply consistently.
**Affected IDs:** `room/AM-1` (P1, account menu), `shell/L1` (login card), `board/CD1` (card-detail — pending doctrine call), `intel/DD-6` (artifact stage), `board/B4` (unused glass rule).

### RC-F — Weak / disabled-looking primary buttons (ghost gray where ink required)
Primary CTAs render as transparent ghost gray-text links or, when disabled, as flat gray pills that read "secondary/off" instead of "the primary door, temporarily disabled."
**Fix:** primaries = ink fill (`--accent`/`--on-accent`); add explicit `[disabled]{opacity:.45}` so disabled reads intentional; unify the two "surface-primary" treatments (Board ink vs Files outline) onto one.
**Affected IDs:** `intel/PK-1,PK-2`; `board/NC1,NC2,NC3,FU2`; `shell/L3`.

### RC-G — Markdown / raw HTML leak in memory log rows
`memoryLogDisplayText` (43823) only skips lines starting with `#`; it does **not** strip `**bold**`, inline backticks, `>`, or raw HTML — so rows render literal `**Jury:**`, `**Status:**`, `<!doctype html>`. Directly contradicts the "markdown-leak killed" claim; this path regressed.
**Fix:** strip markdown tokens (`**`, `` ` ``, `#`, `>`) + collapse whitespace; render a **typed label** for html/artifact bodies (never `<!doctype…>`); make previews type-aware (artifact→title/kind, answer→first sentence, card→title).
**Affected IDs:** `board/ME1,ME2`; related content-scaffolding leak `board/KC4` (`--- Original:` in card notes).

### RC-H — Mobile floating-dock overlap / composer clipped by bottom nav
The fixed bottom dock floats over content on every mobile workspace; scroll containers don't reserve dock + safe-area height, and the chat composer's long placeholder wraps under the dock.
**Fix:** add `padding-bottom: dock-height + env(safe-area-inset-bottom) + gap` to every mobile scroll container (chat/intel/board/memory/files); shorten the mobile composer placeholder (or nowrap+ellipsis) and raise `#chatTool` clearance; add Files to the mobile dock and fix the active-state mapping.
**Affected IDs:** `chat/MO-1`; `intel/CL-4`; `board/X3,X4,X6`; related `board/ME4`.

### RC-I — Nav hover-tooltip z-index over content
`.tool-rail__label` (456-481) has no explicit z-index, inherits the rail's z-90, and projects into the workspace — sitting **on the first thread row** and colliding with stacked panels.
**Fix:** `z-index:300` (above workspace, below account-menu's 1000), dismiss on blur, add left clearance so a hovered label never overlaps a row.
**Affected IDs:** `shell/TR3` (P1); `chat/TL-1` (P2, same bug from the chat side).

### RC-J — Slashed-zero renders "0" as "∅"
Mono display numerals at 30px render a slashed zero, so "0 meetings today" reads as *empty-set / null / error*.
**Fix:** `font-feature-settings:"zero" 0;` (and `tnum`) on mono display numerals wherever a standalone 0 can appear.
**Affected IDs:** `intel/ST-1,ST-2`.

### RC-K — Per-user avatar color: **no hue violation found (good)** — but avatar-system consistency issues exist
Avatars are correctly **monochrome initials on `--surface-3`** everywhere; the memory/scout "flame" is a monochrome inline SVG (`fill:currentColor`, gray), **not** ember and **not** emoji. No per-user color to kill. The residual issues are structural, not chromatic: avatar background on the wrong surface, an "unassigned" avatar that reads as a real user, sub-legible initials, and two Scout avatar codepaths (flame vs "S" initials).
**Affected IDs:** `room/AM-4` (avatar bg surface-1 → should be surface-3); `board/KC3` ("U" unassigned chip reads as a user); `intel/CL-3` (8.5px initials sub-legible); `chat/BS-2` + `chat/AL-5` (Scout flame vs initials — pick one avatar system).

### RC-L — Banned "left-border-accent" card pattern (state = pill/dot, never an edge stripe)
Several cards signal state with a `border-left: 3px solid …` stripe, which the law explicitly forbids ("state = pill or dot").
**Fix:** replace every state stripe with a leading dot / pill.
**Affected IDs:** `shell/H8` (approval + stale-portfolio); `chat/AL-3`(letter, accent *top*-border) + `chat/CR-2` (returncard left stripe); `intel/EM-2` (deal-room active — also ember) + `intel/EM-3` (deal-room pending — warn).

### RC-M — Green-for-success doctrine conflict (needs a decision)
Green is the loudest, reserved-for-live-only signal — but the design sheet expanded it to **verified / complete / shipped / "ready"**. Unreconciled.
**Fix:** pick one rule — (a) green = live only (move terminal success to a mono ink check), or (b) green = live + terminal success — and apply everywhere.
**Affected IDs:** `chat/SYS-2,GC-3` (goalcard complete + manifest shipped); `board/MS3` (idle "ready" pill uses live-green); `room/R-8,T-1` (idle dot / speaking ring).

### RC-N — Motion: grow-on-hover instead of press-down (systemic)
The kit motion spec is press-down `.97` + breathe-only. Pervasive `transform:scale(1.03–1.08)` grow-on-hover violates it.
**Fix:** remove hover grows; keep `:active` press `.97` (+ the live breathe).
**Affected IDs:** `room/X-1` (rail 500, dock 3174, leave 3394, topbar-live 832, room-join 3297), `room/R-3,D-3`.

### RC-O — Text glyphs used as icons (icon law: Lucide stroke-1.75, no dingbats)
Close controls and confirmations use typographic `✕` / `×` / `✓` instead of Lucide icons.
**Fix:** replace with Lucide `x` / `check` (currentColor, 14–18px).
**Affected IDs:** `intel/DD-5`; `board/CD3`; `room/S-5`.

### RC-P — Empty-state over-signaling & fake/broken charts
Empty states pile up multiple signals (column fade + hero koan + per-column "nothing here yet" + zeroed stat row), reuse a koan as a full-page header while data is present, and fake a histogram out of ghost bars.
**Fix:** one centered koan per empty view; suppress per-column empties and the fade when the whole board is empty; hide the stats strip at all-zero; header subtitle = descriptive (reserve the koan for the true empty view); render an empty-pulse koan on a baseline instead of ghost bars.
**Affected IDs:** `board/BE1,BE2,MM1,MM2,FU1`; `intel/IP-1,IP-4`.

### RC-Q — Radius off-grid (token hygiene)
Many surfaces hardcode radii off the scale (r-sm 8 / r-md 12 / r-lg 16 / r-xl 22 / r-2xl 28).
**Fix:** route through the radius tokens.
**Affected IDs:** `chat/BY-2` (18), `shell/H12` (20), `shell/L2` (24), `board/NC4` (11), `board/FL1` (14/10), `board/PR2` (9), `room/R-2` (14), `room/P-1` (22→28), `room/AM-2` (22 vs 28).

---

## 3. P0 + P1 ranked cross-surface build list

Build in this order. IDs are surface-prefixed. `→RC-x` points at the root cause in §2.

### P0 (4) — ship-blockers

| ID | surface | element | defect | fix |
|---|---|---|---|---|
| **EM-1** | intel/all | ember token | `--agent:#FF7A2B` (+soft/glow) still declared live in both roots; ~50 consumers | redefine `--agent`→accent, `--agent-soft`→accent-soft, `--glow-agent`→mono/`--info`; delete doctrine + `is-hot-ember`. **→RC-A** |
| **CP-1** | chat | palette tool tiles | `.palette__well` icon tiles = ember-soft bg + ember glyph → big orange squares (17518) | `background:--surface-3` + `color:--text-2`; monochrome glyphs |
| **DD-1** | intel | deliverable drawer | packaging deliverable opens as monospace run-log; the investor deck (child `html_deck`) never surfaces | lead with deck-cover hero + primary "open the deck/present" feeding the existing `--deck` iframe; demote run-log to a disclosure *(ref)* |
| **R-1** | room | empty/pre-join | `.room-empty` gated behind `.is-authed` → keyless/pre-auth room = blank white void (13267) | drop the `.is-authed` gate; paint `#hearthStage` `--bg-stage` black so it's never white paper |

### P1 (33)

**Ember-token duplicates (fold into EM-1 / RC-A):**

| ID | surface | element | defect | fix |
|---|---|---|---|---|
| SYS-1 | chat | ember token/doctrine | same token, chat-side filing | →RC-A |
| X1 | board | ember token | same token, board-side filing | →RC-A |
| E-1 | room | ember token | same token, room-side filing | →RC-A |

**Ember consumers (resolve once RC-A lands, then verify per-surface):**

| ID | surface | element | defect | fix |
|---|---|---|---|---|
| TR1 | shell | rail+dock active tick | active `::after` = ember (524) | `background:var(--accent)` |
| E-2 | room | nav "you are here" marker | ember left-marker in persistent chrome (524) | `--accent` hairline or drop (chip already inverts) |
| MO-2 | chat | mobile nav active bar | bright ember bar via shared `::after` | mono/`--accent` mark, or rely on the filled ink pill |
| H1 | shell | home card icons | Morning-Brief/Portfolio svg icons ember (15123) | `color:var(--text-1)` |
| H2 | shell | unread badge | ember bg + hardcoded `#fff` text (15133) | `background:--accent; color:--on-accent` |
| OA1 | shell | voice-island "acting" | border/glow/wave ember ("machine working", 6125) | mono breathing dot or `--info` hairline |
| GC-1 | chat | goalcard running | ring/%/kicker/active-node/spark all ember (17712-18023) | recolor mono/`--info`; node = mono breathing dot 2400ms |
| EM-4 | intel | goalcard park dot | `--park::before` ember dot + glow (9157) | mono breathing dot |
| EM-5 | intel | goalcard node breathe | keyframes bake ember/glow (18590-91) | rewrite to mono ring/`--accent` |
| EM-2 | intel | deal-room active row | ember hue **and** banned left-border-accent (18699/18704) | ember→mono/`--live`; stripe→state pill/dot (→RC-L) |
| PK-5 | intel | package advancing sweep | `.is-advancing::after` = ember-soft (18646/18659) | `--accent-soft` or `--live-soft` |
| CP-2 | chat | palette empty-hand | ember-soft bg + 1px ember border pill (17552) | ink/outline pill |
| N-1 | room | notif-bell unread ring | rest ring ember-soft, arrival pulse ember-glow (9818-30) | ring→`--accent-soft`/`--info-soft`; pulse→mono/`--accent` |
| D-1 | room | mic/cam OFF glyph | off glyph `#FF6B61` warm-red instead of danger (3181) | glyph `--danger`; wash `--danger-soft` |
| D-2 | room | recording-paused glyph | paused glyph `#FF6B61` (3116) | `--danger` / `--danger-soft` |

**Non-ember P1:**

| ID | surface | element | defect | fix |
|---|---|---|---|---|
| TR3 | shell | nav hover label | no z-index → collides / sits on first thread row (456-481) | `z-index:300`, dismiss on blur, left clearance (→RC-I; chat/TL-1 same bug) |
| AL-1 | chat | agent-letter card | longform reply = full-width bordered card, short = borderless bubble → one voice reads as two senders | unify identity family: shared header/avatar, drop accent top-border, fix inverted elevation *(ref)* |
| MO-1 | chat | mobile composer | long placeholder wraps; dock reserve too small → dock overlaps 2nd line (17107) | shorten mobile placeholder / nowrap+ellipsis / raise clearance (→RC-H) |
| IP-1 | intel | ingestion pulse (empty) | all 36 bars 4%/0.18 → reads as broken empty ruler (41587) | koan on a 1px baseline in the all-zero state *(ref)* |
| IP-2 | intel | ingestion pulse (sparse) | one meeting normalizes to 100% → single giant lonely bar (41583) | floor peak-normalization / widen bins + caption *(ref)* |
| DD-3 | intel | deliverable body | dumps run-log bullets (Changed/Gate/QA) as the deliverable | hide internal run bullets into a "run details" disclosure (with DD-1) |
| B2 | board | columns | `flex:1 1 0` → 3 empty columns eat 75%, Done's 23 cards scroll off (2059) | collapse empty columns to a slim rail / let populated lanes grow *(ref)* |
| KC1 | board | done-column cards | `.column--done .card{opacity:.62}` + all cards in Done → whole board dimmed/dead (2166) | drop/raise the fade; express "done" via a state dot |
| KC2 | board | card footer | owner+tags+date on one non-wrapping row → tags clip past the rounded edge, owner ellipsizes to "o." (2318) | tags on their own wrapping row, "+N" cap, `overflow:hidden` |
| NC1 | board | new-card button | disabled off-room (`canEditBoard` false) → the board's one primary action is a permanent grey pill on the Board tab (19877) | enable create from the tab / ink "join to add" affordance |
| CD2 | board | card-detail modal | modal doesn't paint in not-connected state (capture identical to board) | ensure card click opens a view-modal without edit rights |
| ME1 | board | memory log rows | leak raw `**Jury:**`, `**Status:**`, `<!doctype html>` (43823 only skips `#`) | strip markdown tokens + collapse ws; typed label for html/artifact bodies (→RC-G) |
| ME2 | board | memory previews | "first non-# line" picks fence/doctype/`**` garbage | type-aware previews (→RC-G) |
| X3 | board | mobile Files dock | Files absent from the 5-item mobile dock; dock highlights Home on the Files screen | add Files to the dock/"more" sheet; fix active-state mapping (→RC-H) |
| AM-1 | room | account menu material | opaque surface-1, no blur/highlight while the sibling notif panel is glass (1059) | glass-chrome + blur + glass-highlight/shadow (→RC-E) |

---

## 4. Full per-surface inventories

Every finding, deduped, IDs preserved. `bp`: d=desktop ≥861 · m=mobile ≤860 · all=both. `sev`: — = compliant/keep (see §6). Dark tokens: `--accent #F5F5F7`, `--on-accent #0E0E10`, `--surface-1 #101012`, `--surface-2 #161619`, `--surface-3 #1E1E23`, `--live #30D158`, `--danger #FF453A`, `--warn #FF9F0A`, `--info #0A84FF`, `--agent #FF7A2B` (retired but live).

### 4.1 Shell + Home + Login + Global chrome

**Tool rail (60px desktop column / mobile bottom pill dock — same component, repositioned ≤640px @15982)**

| ID | element/state | bp | theme | defect | anchor | fix | sev | ref |
|---|---|---|---|---|---|---|---|---|
| TR1 | active nav tick | all | both | active `::after` = ember #FF7A2B on rail **and** mobile dock | `[aria-pressed=true]::after` bg 524 | `background:var(--accent)` (one change fixes rail+dock) | P1 | n |
| TR2 | dock active indicator | m | both | vertical 2×16 hairline pinned left:3px sits awkwardly on the horizontal pill | 515-526 + 15982 | drop the tick on dock; let the filled `--accent` circle carry active, or center a mono dot | P2 | **y** |
| TR3 | hover label flyout | d | both | no explicit z-index → collides with stacked workspace panels | 456-481 (rail z-90 @397) | `z-index:300`, or raise slot on hover (→RC-I) | P1 | n |
| TR4 | brand mark class | all | both | `is-hot-ember` retained (retired vocab); color already remapped to `--live` | 784-789 | rename `is-recognized`/`is-wake` | P3 | n |

**Top bar (52px hairline, ≥641px)**

| ID | element/state | bp | theme | defect | anchor | fix | sev | ref |
|---|---|---|---|---|---|---|---|---|
| TB1 | brand wordmark | d | both | shell says "BonfireOS" (794), gate says "bonfire" (audit-01) — casing drift | 791 / 1607 | pick one lockup (recommend lowercase `bonfire`) | P3 | n |
| TB2 | heading/subtitle/date | d | both | **compliant** — heading sans, `tue · jul 7` lowercase mono, status pill mono | 791-811 | — | — | n |

**Status pill**

| ID | element/state | bp | theme | defect | anchor | fix | sev | ref |
|---|---|---|---|---|---|---|---|---|
| SP1 | idle "ready" pill | d | dark | `.pill--idle` dot = `--text-2` neutral gray on `--surface-3` — correct semantics but low-contrast on true-black | 1186-1189 / 18997 | acceptable; optional bump dot to `--text-1` | P3 | n |

**Goal-loop marquee (goalcard 10-step strip — owned by goal-engine, not shell; only renders when a goal runs)**

| ID | element/state | bp | theme | defect | anchor | fix | sev | ref |
|---|---|---|---|---|---|---|---|---|
| GM1 | active stage node | d | both | active node breathes ember via `goalcard-node-breathe`; labels correctly lowercase mono | 18590 | retire ember → mono breathing dot / `--info`; flag to goal cluster | P2 | **y** |

**Home / office launch**

| ID | element/state | bp | theme | defect | anchor | fix | sev | ref |
|---|---|---|---|---|---|---|---|---|
| H1 | brief/portfolio card icons | all | both | ember svg icons (#FF7A2B) — first surface after login | 15123 | `color:var(--text-1)` | P1 | n |
| H2 | unread "2" badge | all | both | ember badge + hardcoded `#fff` text (law: unread = ink) | 15133-34 | `background:--accent; color:--on-accent` | P1 | n |
| H3 | icon consistency | m | both | 2 ember icons + 1 mono (intel card lacks the ember rule) | 15044 vs 15123 | H1 fix makes all three mono | P2 | n |
| H4 | intel card interaction | m | both | only `:active` scale, no `:hover` border feedback | 15044-61 | add `:hover{border-color:--line-2}` | P3 | n |
| H5 | greeting name casing | all | both | live "good morning, aj." vs local "…AJ." | ~45979 / 5429 | normalize name casing in the greeting builder | P3 | n |
| H6 | idle waveform bars | all | both | per-bar opacity renders bars ~gray → reads faintly disabled | 5414-27 | raise floor opacity so idle reads as dim-ink | P3 | n |
| H7 | sheet eyebrow | all | both | "BonfireOS" ember **+ UPPERCASE** — double violation | 15179 | `color:--text-3; text-transform:lowercase; font:--type-label` | P2 | n |
| H8 | approval + stale-portfolio cards | all | both | `border-left:3px solid --warn` — banned left-border-accent | 15223 / 15265 | warn dot/pill; drop the border (→RC-L) | P2 | n |
| H9 | portfolio readiness meter | all | both | ember progress fill | 15276 | `background:var(--accent)` | P2 | n |
| H10 | portfolio up-delta | all | both | `color:var(--signal)` but bare `--signal` is **undefined** → renders gray | 15273 | `color:var(--live)` (=`--signal-500`) | P2 | n |
| H11 | sheet type signature | all | both | section/stage/gap labels UPPERCASE sans, not lowercase mono | 15205/15268/15281 | lowercase + `--font-mono` via `--type-label` | P3 | n |
| H12 | sheet panel radius | all | both | r=20 non-canonical | 15163 | `border-radius:var(--r-2xl)` (28) | P3 | n |
| H13 | dark home card definition | d | dark | `--line-1` borders near-invisible on true-black → cards read as edgeless fills | 15115 | optionally `--line-2` at rest in dark | P3 | n |

**Login gate**

| ID | element/state | bp | theme | defect | anchor | fix | sev | ref |
|---|---|---|---|---|---|---|---|---|
| L1 | login card "glass" | all | light | reads flat white — ambient behind is too weak to refract; full glass recipe present but nothing to bend | 1666-78 / ambient 130-33 | strengthen `--backdrop-ambient`/vignette behind the gate; firm the border in light (→RC-E) | P2 | **y** |
| L2 | card radius | all | both | r=24 non-canonical | 1675 | `--r-2xl` (28) — it's the gate dialog | P3 | n |
| L3 | disabled "Enter your office" | all | light | `color-mix(accent 45%,bg)` → mid-gray dead slab | 1807-13 | keep ~60% ink or an ink outline so it still reads as the primary door (→RC-F) | P3 | n |
| L4 | mark/tagline/passkey | all | both | **compliant** — 56px ink mono tile, lowercase mono tagline, Lucide Face-ID glyph | 1595-1619, 1845 | — | — | n |

**Floating OS assistant (voice island + ledger)**

| ID | element/state | bp | theme | defect | anchor | fix | sev | ref |
|---|---|---|---|---|---|---|---|---|
| OA1 | voice-island "acting" | all | both | ember by design (border/glow/`.os-wave`) — "ember = machine working," the exact retired pattern | 6125-35 | mono breathing dot / `--info` hairline | P1 | **y** |
| OA2 | ledger undo control | all | both | ember text + hover ember border | 6242/6246 | mono (`--text-2`) or `--info` | P2 | n |
| OA3 | hand-raised / listening | all | both | **compliant** — `--warn` consent-wait, `--live-soft` listening glow | 6140-55, 6006-12 | — | — | n |
| OA4 | private grill stage (cross-cluster) | all | both | ember-saturated by a Wave-12 decision (6373-6636) | 6332-6640 | hand to grill cluster for the ember sweep | P2 | n |

### 4.2 Chat

**Systemic / tokens**

| ID | element/state | bp | theme | defect | anchor | fix | sev | ref |
|---|---|---|---|---|---|---|---|---|
| SYS-1 | ember token + doctrine | all | both | `--agent`/soft/glow live in both roots with an enshrining comment; ~50 uses | roots 232/281 | delete tokens+comment; migrate to mono/`--info`/breathing dot (→RC-A) | P1 | n |
| SYS-2 | green = success | all | both | green used for verified/complete/shipped, not just live — doctrine conflict | goalcard 17730; manifest 7994 | decide green=live-only vs +terminal-success; apply everywhere (→RC-M) | P2 | **y** |

**Thread list**

| ID | element/state | bp | theme | defect | anchor | fix | sev | ref |
|---|---|---|---|---|---|---|---|---|
| TL-1 | nav hover label over list | d | both | `.tool-rail__label` projects onto the first thread row, obscuring it; no z-index guard | 456-481 | high z-index + dismiss on blur + left clearance (→RC-I) | P2 | n |
| TL-2 | two competing list paradigms | d | both | live shows stacked "channels/private" sections; local build shows a public\|private toggle | 7236 | ship ONE — the stacked two-section model matches shipped CSS; retire the toggle | P2 | n |
| TL-3 | unread/status dot | all | both | leading ink dot unclear whether unread or run-status | 7492 | confirm mapping; if it also marks "active run," differentiate | P3 | n |
| TL-4 | section header casing | all | both | **compliant** — mono `--type-label` lowercase text-3 | 7245-53 | — | — | n |
| TL-5 | light thread cards | all | light | **compliant** — surface-2, 1px line-1, r16, sans title / mono timestamp | 17053-66 | — | — | n |

**Message bubbles — you / scout**

| ID | element/state | bp | theme | defect | anchor | fix | sev | ref |
|---|---|---|---|---|---|---|---|---|
| BY-1 | you-bubble fill | all | dark | **keep** — "white bubble" is `--accent #F5F5F7` on `--on-accent`, the ink-accent law (not #FFF, not a bug) | 8541-46 | keep (closes the "jarring white bubble" question) | — | n |
| BY-2 | bubble radius | all | both | hardcoded `18 18 6 18` (law = 16) | 8545/8548 | `--r-lg` (16) via token | P3 | n |
| BY-3 | mention chip on ink bubble | all | both | `color-mix(currentColor 16%)` → faint low-contrast wash on ink | 9554-56 | drop chip fill on you-bubbles; carry mention with weight/underline | P3 | n |
| BS-1 | scout bubble fill | all | both | **compliant** — surface-2 + 1px line-1, text-1 (radius nit = BY-2) | 8551-56 | — | — | n |
| BS-2 | scout avatar = flame | all | both | scout renders a monochrome flame while peers get initials — avatar-system split (not a color violation) | 8392-8401 | keep flame as Scout's one sanctioned mono avatar, or switch to "S" (→RC-K) | P3 | n |

**Agent-letter card ("claude · fable 5")**

| ID | element/state | bp | theme | defect | anchor | fix | sev | ref |
|---|---|---|---|---|---|---|---|---|
| AL-1 | letter vs bubble grammar | all | both | longform reply = full-width bordered id-header card; short reply = borderless bubble → one voice, two senders | 8284-91 / 8583-88 | unify identity family (shared header/avatar, bridge the card) *(ref)* | P1 | **y** |
| AL-2 | inverted elevation | all | dark | letter card on `--surface-1` is dimmer than a plain scout `--surface-2` bubble | 8573/8585 | card → `--surface-2` so lift tracks importance | P2 | n |
| AL-3 | accent top-border | all | both | `border-top:2px solid --accent` — accent edge-stripe on a card | 8589-91 | drop the stripe; signal via header + alignment (→RC-L) | P2 | n |
| AL-4 | tool name not mono-code | all | both | `create_image` renders bold sans, not inline mono `code` | 8499-8506 | author tool/function names as `` `code` `` | P2 | n |
| AL-5 | id-badge vs avatar codepath | all | both | `.idbadge` CSS is initials-on-surface-3 but the rendered header shows the flame | 8304 / 8392 | reconcile to one Scout avatar (→RC-K) | P3 | n |

**Deliverable / goal card**

| ID | element/state | bp | theme | defect | anchor | fix | sev | ref |
|---|---|---|---|---|---|---|---|---|
| GC-1 | running signals ember | all | both | ring/%/kicker/active-node/spark all ember | 17712/17719/17744/17810-11/18023 | mono/`--info`; node = mono breathing dot 2400ms (→RC-A) | P1 | **y** |
| GC-2 | rest shadow on card | all | both | `.goalcard` carries `--shadow-1` at rest (law: cards no rest shadow) | 17675 | remove rest shadow; keep verified-flash/hover lift | P3 | n |
| GC-3 | green verified glyph | all | both | complete glyph uses `--live-soft`/`--live` — see SYS-2 | 17730 | resolve per RC-M | P2 | **y** |
| GC-4 | action doors | all | both | **compliant** — already ember-swept to ink/outline pills | 17970-18002 | keep | — | n |
| GC-5 | gate/park amber dot | all | both | **compliant** — `--warn` breathing dot at 2400ms, semantically honest | 17736/18040 | keep | — | n |

**@mentions + markdown**

| ID | element/state | bp | theme | defect | anchor | fix | sev | ref |
|---|---|---|---|---|---|---|---|---|
| MM-1 | mention chip | all | both | `--accent-soft` r5 weight500 — on-brand; only nit r5 vs pill-full | 9545-51 | optional `--r-full` or weight-only | P3 | n |
| MM-2 | code/strong/quote | all | both | **compliant** — code surface-3 mono r6; strong 680; blockquote left-border allowed | 8494-8537 | keep (see AL-4) | — | n |

**Crumb + return-to-origin**

| ID | element/state | bp | theme | defect | anchor | fix | sev | ref |
|---|---|---|---|---|---|---|---|---|
| CR-1 | returncard tag ember+caps | all | both | `color:--agent` **+ text-transform:uppercase** | 18349 | mono lowercase `--text-3` (→RC-A, RC-C) | P2 | n |
| CR-2 | returncard left stripe | all | both | `border-left:2px solid --line-2` — left-border-accent (monochrome) | 18338 | leading dot/pill (→RC-L) | P3 | n |
| CR-3 | park note ember dot | all | both | `--park::before` ember dot, inconsistent with goalcard park (`--warn`) | 9151-59 | mono or `--warn` breathing dot | P2 | n |
| CR-4 | crumb pill | all | both | **compliant** — mono lowercase, outlined, monochrome | 9133 | keep | — | n |

**Thinking indicator**

| ID | element/state | bp | theme | defect | anchor | fix | sev | ref |
|---|---|---|---|---|---|---|---|---|
| TH-1 | dot cadence | all | both | dots animate 1.2s, off the 2400ms breathe cycle | 9224-43 | align to breathe, or document a distinct thinking pace | P3 | n |
| TH-2 | flame + 3 bouncing dots | all | both | monochrome (compliant) but 3-bouncing-dots is a generic messenger trope | 9199-9239 | optional: single mono breathing dot + flame | P3 | (y) |

**Composer + tools palette**

| ID | element/state | bp | theme | defect | anchor | fix | sev | ref |
|---|---|---|---|---|---|---|---|---|
| CP-1 | palette icon tiles ember | all | both | tool-icon tiles ember-soft bg + ember glyph — loudest decorative ember in chat | 17518-27 | `--surface-3` bg + `--text-2` glyph | **P0** | n |
| CP-2 | palette empty-state button | all | both | ember-soft bg + 1px ember border pill | 17552-61 | ink/outline pill | P1 | n |
| CP-3 | group labels UPPERCASE | all | both | "RECENT/END-TO-END/IDEATE" | 17482-87 | lowercase (→RC-C) | P2 | n |
| CP-4 | empty koan serif | all | both | `--font-serif` Georgia italic outside the type system | 17544-50 | mono koan or sans (→RC-B) | P2 | n |
| CP-5 | palette sheet glass | all | both | **compliant** — glass-chrome+blur+highlight+shadow r-2xl; mono keycaps | 17429-42 | keep | — | n |
| CP-6 | composer control chrome | all | both | `+` launcher ringed while attach/library are bare — mixed chrome | 9253-64 / 9349-81 | unify the three left controls | P3 | n |
| CP-7 | quick-reply pill fallback | all | both | hardcodes `#fff` instead of `--on-accent` | 7949 | use `var(--on-accent)` | P3 | n |
| CP-8 | status pill copy | all | both | "BonfireOS ready" mixed-case | content | lowercase system status (→RC-D) | P3 | n |

**Mobile chat**

| ID | element/state | bp | theme | defect | anchor | fix | sev | ref |
|---|---|---|---|---|---|---|---|---|
| MO-1 | composer clipped by dock | m | both | wrapped placeholder + small dock reserve → dock overlaps 2nd line | 17107-10 / 17012 | shorten placeholder / nowrap / raise clearance (→RC-H) | P1 | n |
| MO-2 | nav dock active = ember bar | m | both | bright ember `::after` bar on the active tab | 515-526 | mono active mark / filled ink pill (→RC-A) | P1 | n |
| MO-3 | convo-head truncation | m | both | title truncates to "wave6sm…"; back+pencil+status starve the title | 17086-97 | give title width priority; drop meta to a 2nd line | P2 | n |
| MO-4 | mobile thread cards | m | both | **compliant** — surface-2/line-1/r16, `:active` .985 | 17053-62 | keep | — | n |
| MO-5 | coverage gap | m | — | mobile **conversation** view never captured (inv-m-03 duplicated the list) | — | re-capture convo before/after (process) | P3 | n |

### 4.3 Intelligence + Deliverables

**Ingestion pulse**

| ID | element/state | bp | theme | defect | anchor | fix | sev | ref |
|---|---|---|---|---|---|---|---|---|
| IP-1 | pulse chart empty (0 data) | all | both | 36 bars @4%/0.18 → faint dashed ruler, reads broken; no koan | JS 41587; 10498 | koan on a 1px baseline in all-zero (→RC-P) | P1 | **y** |
| IP-2 | pulse chart sparse (1 mtg) | all | both | one event normalizes to 100% → lonely giant bar | JS 41583/41587 | floor peak-normalization / widen bins + caption | P1 | **y** |
| IP-3 | pulse hero whitespace | d | both | 68px band bottom-aligned in a tall card → ~60% empty air | 10502/10457 | raise band / tighten padding / add live-count readout | P2 | y |
| IP-4 | faint bars vs axis | all | both | ghost bars merge into the mono axis line — two stacked dashed rules | JS 41593; 10519 | drop ghost bars in empty; keep one baseline | P2 | n |
| IP-5 | bar fill hue | all | both | **compliant** — `--accent` bars, `--live` only on the live bar | 10510-15 | keep | — | n |
| IP-6 | "refresh themes" btn | all | both | label 500 12px sans — system control in sans | 10475 | optional mono 11px label (→RC-C) | P3 | n |

**Stat tiles**

| ID | element/state | bp | theme | defect | anchor | fix | sev | ref |
|---|---|---|---|---|---|---|---|---|
| ST-1 | big stat "0" | all | both | mono 30px slashed-zero renders "0" as "∅" → reads null/error | 10556 | `font-feature-settings:"zero" 0` (→RC-J) | P2 | n |
| ST-2 | big stat general | all | both | mono reads technical/terminal vs Cash-App display-stat | 10554-58 | confirm mono intent; if mono, kill slash | P3 | n |
| ST-3 | sub-label + delta | all | both | **compliant** — mono lowercase label+delta, delta opacity .65 | 10547-65 | keep | — | n |
| ST-4 | tile grid | m | both | **compliant** — 4→2×2 reflow; verify no squish ~360px | 10534 | verify only | — | n |

**Learned-today feed**

| ID | element/state | bp | theme | defect | anchor | fix | sev | ref |
|---|---|---|---|---|---|---|---|---|
| LT-1 | key column width | d | both | fixed 128px + gap16 → huge dead gutter before content | 10621/10607 | shrink key col / right-align mono time | P2 | n |
| LT-2 | type chip styling | all | both | `artifact/decision/card` is plain mono gray text, not a chip; competes with timestamp | 10618 | real mono pill/dot (surface-3+line-1 r-full) (→RC-C) | P2 | n |
| LT-3 | content truncation | all | both | single-line nowrap ellipsis truncates mid-word / ~18 chars on mobile | 10628 | 2-line clamp | P2 | n |
| LT-4 | empty state | all | both | koan renders Georgia serif-italic (`--font-serif` undefined) | 41558 / 18618 | serif token or mono koan (→RC-B) | P2 | n |

**Contribution lens**

| ID | element/state | bp | theme | defect | anchor | fix | sev | ref |
|---|---|---|---|---|---|---|---|---|
| CL-1 | zero-contribution rows | all | both | every member renders at 0%/0 lines → long empty tail | 10647 | collapse zeros to "+3 others · 0 lines" | P3 | n |
| CL-2 | bar fill hue | all | both | **compliant** — `--accent` @0.8 mono nominal bars | 10707-11 | keep | — | n |
| CL-3 | avatar | all | both | 8.5px initials sub-legible, hidden <480px | 10664 | bump to ~9.5-10px (→RC-K) | P3 | n |
| CL-4 | section head vs mobile dock | m | both | dock overlaps "contribution lens" heading; padding-bottom 64px too small | 10377 | `dock-height + safe-area + gap` (→RC-H) | P2 | n |
| CL-5 | 100% / 0 lines | all | both | full black bar with zero underlying lines — contradictory | JS contrib calc | guard: 0 lines → 0% fill | P3 | n |

**Themes / consensus**

| ID | element/state | bp | theme | defect | anchor | fix | sev | ref |
|---|---|---|---|---|---|---|---|---|
| TC-1 | open-question meta | all | both | every "open" tagged warn amber → decorative category color, dilutes the alert palette | 10823 | render "open" in mono `--text-3`; reserve amber for real warnings | P2 | n |
| TC-2 | recurring-theme counts | all | both | **compliant** — `×8/×7/×4` are `--text-3` mono (no ember leak) | 10791 | keep | — | n |
| TC-3 | theme summary truncation | all | both | single-line mono ellipsis truncates synthesized insight on mobile | 10782 | 2-line clamp | P3 | n |
| TC-4 | "in lockstep" glyph | all | both | **compliant** — `--live` green alignment glyph | 10814 | keep | — | n |
| TC-5 | consensus 3→1 col | m | both | **compliant** — reflow <900px | 10165 | verify only | — | n |

**Decision ledger**

| ID | element/state | bp | theme | defect | anchor | fix | sev | ref |
|---|---|---|---|---|---|---|---|---|
| DL-1 | detail row labels | all | both | `.label` (CONTEXT) UPPERCASE + inherits sans — the only uppercase label in a lowercase-mono system | 10920 | lowercase + Geist Mono `--type-label` (→RC-C) | P3 | n |
| DL-2 | meta line typography | all | both | "AJ · 21h ago · …" sans; the timestamp should be mono | 10839 | keep name sans; wrap timestamp mono (→RC-C) | P3 | n |
| DL-3 | row-as-button affordance | all | both | clean state-only, but no visible expand cue | 10878 | add small mono `⌄`/count cue | P3 | n |
| DL-4 | ratify pill | all | both | **compliant** — surface-2+line-1, ink text, disabled .5 | 10846 | keep | — | n |

**Packages tab**

| ID | element/state | bp | theme | defect | anchor | fix | sev | ref |
|---|---|---|---|---|---|---|---|---|
| PK-1 | "New package" CTA | all | both | `.btn--ghost` → transparent, gray text — weak link, not a button | 19569 | ink-filled (or outlined) primary (→RC-F) | P2 | n |
| PK-2 | "Open library" CTA | all | both | same weak ghost | 19583 | outlined/secondary pill distinct from primary | P2 | n |
| PK-3 | empty state voice | all | both | koan Georgia serif-italic (same as LT-4) | 42533/41558 | serif token or mono koan (→RC-B) | P2 | n |
| PK-4 | packages populated | all | both | never seen populated; `.package-rail` 6-stage `flex-wrap` risks a broken label row | 11016/11083 | needs populated composition | P2 | **y** |
| PK-5 | advancing animation | all | both | `.is-advancing::after` sweep/fill both ember-soft | 18646/18659 | `--accent-soft` or `--live-soft` (→RC-A) | P1 | n |
| PK-6 | head button alignment | all | both | `margin-left:auto` pushes weightless ghost CTA far-right | 10449 | resolves with PK-1/2 | P3 | n |

**Artifact library**

| ID | element/state | bp | theme | defect | anchor | fix | sev | ref |
|---|---|---|---|---|---|---|---|---|
| AL-1 (lib) | library populated | all | both | empty in every capture; 320px sidebar + detail editor unverified vs real artifacts | 11148/11269 | needs populated library | P2 | **y** |
| AL-2 (lib) | item status hues | all | both | **compliant** — published/complete→`--live`, running→`--accent-soft` (mono, not ember), error→`--danger` | 11326-402 | keep — this surface was done right | — | n |
| AL-3 (lib) | selected item | all | both | **compliant** — `--accent-soft` + `--line-2` mono selection | 11299 | keep | — | n |
| AL-4 (lib) | search focus ring | all | both | **compliant** — `0 0 0 3px --accent-soft` mono focus | 11241 | keep | — | n |

**Deliverable drawer (artifact stage) — crown jewel**

| ID | element/state | bp | theme | defect | anchor | fix | sev | ref |
|---|---|---|---|---|---|---|---|---|
| DD-1 | opens as text report, not the deck | all | both | `artifactIsHTMLDeck` false for the parent goal artifact → run-log renders; the real deck is an un-surfaced child | 24362-88 | deck-cover hero + primary "open the deck/present" feeding the existing `--deck` iframe (18532); demote run-log | **P0** | **y** |
| DD-2 | kicker vs body state mismatch | all | both | kicker "document · gate 9.0 · passed" vs body "goal artifact · running · 100%" — two kinds, contradictory states | 24279 / renderArtifactRead | single source of truth for kind+state | P2 | n |
| DD-3 | report is run-log | all | both | body dumps Changed/Headline/Gap/Next/Gate/QA — orchestration exhaust as the deliverable | renderArtifactRead / 18515-31 | hide run bullets into "run details" (with DD-1) | P1 | n |
| DD-4 | title truncation | all | both | 17px sans truncates to "…threads into produc…", no full-title affordance | 18461 | 2-3 line clamp or `title=` | P2 | n |
| DD-5 | close glyph | all | both | text "✕", not a Lucide icon | 24352 / 18480 | Lucide `x` (currentColor 16px) (→RC-O) | P3 | n |
| DD-6 | panel is surface-1, not glass | d | both | opaque slab + border-left + shadow-3, no backdrop-filter/highlight over the dimmed feed | 18423 | consider glass-chrome (→RC-E, judgment call) | P2 | **y** |
| DD-7 | escape button hierarchy | all | both | "open in intelligence" is same-weight beside ✕ — over-elevated while it's the only path to the deck | 18468 | once DD-1 lands, demote to a quiet link | P3 | n |

**Ember residue (deliverable-adjacent)**

| ID | element/state | bp | theme | defect | anchor | fix | sev | ref |
|---|---|---|---|---|---|---|---|---|
| EM-1 | `--agent` token live | all | both | ember + soft + glow declared in both roots — root cause | 232/284 | redefine→mono OR delete+migrate (→RC-A) | **P0** | n |
| EM-2 | deal-room active row | all | both | `border-left:3px solid --agent` + `__state{color:--agent}` — ember **and** banned left-border | 18699/18704 | ember→mono/`--live`; stripe→state pill/dot (→RC-L) | P1 | n |
| EM-3 | deal-room pending row | all | both | `border-left:3px solid --warn` — warn is fine but left-border-accent is banned | 18697 | warn-soft pill/dot; drop border (→RC-L) | P2 | n |
| EM-4 | goal-card park dot | all | both | ember dot + ember glow on the in-chat park note | 9157 / 18591 | mono breathing dot | P1 | n |
| EM-5 | goal-card node breathe | all | both | keyframes bake ember/glow/soft | 18590-91 | rewrite to mono ring/`--accent` | P1 | n |
| EM-6 | composer deliverable-tools trigger | all | both | `.scout-chat-tools:hover` = ember color/border/bg | 18582 | mono hover (`--text-1`/`--surface-3`) | P2 | n |

### 4.4 Board + Memory + Files

> Verification correction: the memory "flame" before *what the room decided* is a monochrome inline SVG (`fill:currentColor`, gray, 11px, @43905) — **not** emoji, **not** ember. On-law.

**Board — columns & headers**

| ID | element/state | bp | theme | defect | anchor | fix | sev | ref |
|---|---|---|---|---|---|---|---|---|
| B1 | column names | all | both | "Backlog/In Progress/Blocked/Done" sans 600 title-case (law: lowercase mono) | 2081-89 | `--type-label-lg` mono lowercase tracked `--text-3` (→RC-C) | P2 | n |
| B2 | 4 equal columns, lopsided data | d | both | `flex:1 1 0` → 3 empty columns eat ~75%, Done's 23 cards scroll off | 2059-69 / 2050-56 | collapse empty columns to a rail / populated lanes grow | P1 | **y** |
| B3 | header mixes mono count + sans name | all | both | two type systems in one label line | 2092-2102 | resolves with B1 | P3 | n |
| B4 | board container material | d | dark | `.board-surface` glass rule defined but the standalone tab paints flat near-black | 2001-13 | verify wrapper; drop unused glass rule or wrap in `--glass-panel` | P3 | n |

**Kanban card**

| ID | element/state | bp | theme | defect | anchor | fix | sev | ref |
|---|---|---|---|---|---|---|---|---|
| KC1 | done cards contrast | d | dark | `.column--done .card{opacity:.62}` × all-in-Done → whole board dimmed/low-contrast | 2166-68 | drop/raise fade; "done" via state dot; never fade when Done is the only lane | P1 | n |
| KC2 | footer owner+tags overflow | all | both | one non-wrapping row → tags clip past the rounded edge, owner ellipsizes to "o." | 2318-26 / 44746-66 | tags on own wrapping row, "+N" cap, `overflow:hidden` | P1 | n |
| KC3 | unassigned owner avatar | all | both | "U" initial chip reads as a real user named "U" | 44751-55 / 2329-41 | dashed/empty ring or neutral dot; soften "unassigned" (→RC-K) | P2 | n |
| KC4 | description content | all | both | 2-line clamp OK but text is ops-log noise ("SHIPPED … commit …", `--- Original:`) | 2291-2300 | store a real one-line description; strip scaffolding (→RC-G) | P2 | n |
| KC5 | card clipping guard | all | both | `.card` no `overflow` with r16 → overflowing text escapes the corner | 2141-53 | `overflow:hidden` (pairs KC2) | P2 | n |
| KC6 | hover-lift shadow in dark | d | dark | dark-ink shadow invisible over #000 → lift reads as translate only | 2155-58 | theme-aware lift: lighter/tinted shadow + inset highlight | P2 | n |

**New-card button**

| ID | element/state | bp | theme | defect | anchor | fix | sev | ref |
|---|---|---|---|---|---|---|---|---|
| NC1 | disabled on the Board tab | all | both | `canEditBoard` starts false off-room → the one primary action is a permanent grey pill | 19877 / 21102 | enable create from tab / ink "join to add" (→RC-F) | P1 | n |
| NC2 | disabled visual ambiguous | all | both | ink `#newCard` carries `btn--ghost`; disabled dim makes it read secondary/off; no `[disabled]` rule | 19877 / 14338-53 | remove `btn--ghost`; `#newCard[disabled]{opacity:.45}` | P2 | n |
| NC3 | inconsistent w/ Files primary | all | both | Board "new card" = ink fill; Files "upload" = surface-2 outline — two primary treatments | 14338 vs 10227-40 | unify (both ink) (→RC-F) | P2 | n |
| NC4 | radius off-grid | all | both | r=11 | 14343 | `var(--r-md)` (12) (→RC-Q) | P3 | n |

**Card-detail modal**

| ID | element/state | bp | theme | defect | anchor | fix | sev | ref |
|---|---|---|---|---|---|---|---|---|
| CD1 | dialog material | all | both | opaque surface-1 + shadow-3, no backdrop-filter/highlight (r28 ✓) | 4331-44 | glass-chrome+blur+highlight/shadow (**pending menus-vs-dialogs doctrine, →RC-E**) | P2 | **y** |
| CD2 | modal open behavior | d | dark | capture identical to board — dialog didn't paint in not-connected state | 19877 / 44660+ | ensure card click opens a view-modal without edit rights | P1 | n |
| CD3 | close glyph | all | both | typographic "×", not a Lucide X | 4378-91 | Lucide X (→RC-O) | P3 | n |

**Board empty / lopsided**

| ID | element/state | bp | theme | defect | anchor | fix | sev | ref |
|---|---|---|---|---|---|---|---|---|
| BE1 | fully-empty board triple-signals | all | both | column fade 0.42 + hero koan + per-column "nothing here yet" all at once | 2126-37 / 19884 / 44607 | one centered koan; suppress per-column + fade when whole board empty (→RC-P) | P2 | n |
| BE2 | repeated "nothing here yet" | d | both | 3 empty columns each print filler across 75% of the canvas | 2116-22 / 44442 | print koan once or collapse empty columns (ties B2) | P2 | n |
| BE3 | connection pill contradicts across tabs | all | both | grey "not connected" on Board vs green "BonfireOS ready" on Memory/Files | 18999 / 19875 | scope board's "not connected" to its toolbar; one honest global indicator (→RC-D) | P2 | n |

**Memory — search & stats**

| ID | element/state | bp | theme | defect | anchor | fix | sev | ref |
|---|---|---|---|---|---|---|---|---|
| MS1 | column width | d | both | ~780px centred column with large dead flanks | 12863-72 | intentional measure OK; codify the cap for stats+list | P3 | n |
| MS2 | stats separators | all | both | only first pair has "·"; the rest are space-runs → run-on | 2480-88 | consistent "·" between every stat (→RC-C) | P3 | n |
| MS3 | "ready" pill uses live-green | all | both | idle "ready" uses `--live` (green reserved for live/speaking) | 23213 | neutral/ink dot for "ready" (→RC-M) | P2 | n |

**Memory — entries**

| ID | element/state | bp | theme | defect | anchor | fix | sev | ref |
|---|---|---|---|---|---|---|---|---|
| ME1 | log rows leak markdown/HTML | all | both | raw `**Jury:**`, `**Status:**`, `<!doctype html>`; `memoryLogDisplayText` only skips `#` | 43823-35 | strip `**`/`` ` ``/`#`/`>`, collapse ws; typed label for html/artifact (→RC-G) | P1 | n |
| ME2 | preview picks first non-`#` line blindly | all | both | fence/doctype/`**` first line → garbage preview | 43823-35 / 43802-18 | type-aware previews (→RC-G) | P1 | n |
| ME3 | expanded meeting splits into two boxes | all | both | second `.memory-loose` box hugs the meeting card — looks like a render bug | 2739-48 | fold loose entries into the day stream as a labelled row | P2 | n |
| ME4 | log key col too wide on mobile | m | both | `width:152px` fixed → ~40% of 390px, value squeezed to a sliver | 2688-95 | `@media` shrink to ~96px or stack key/value (→RC-H) | P2 | n |
| ME5 | log kinds visually identical | all | both | artifact/card/answer/summary all same text-3 grey | 2693 / 43802 | vary weight/tint per kind or add a kind glyph | P3 | n |
| ME7 | meeting title hard 60% cap | all | both | `max-width:60%` truncates named meetings early even with short meta | 2576-84 | let title use available space; meta yields first | P3 | n |

**Memory — empty**

| ID | element/state | bp | theme | defect | anchor | fix | sev | ref |
|---|---|---|---|---|---|---|---|---|
| MM1 (mem) | empty koan reused as full-page subtitle | all | both | "memory starts when the room speaks." is header subtitle while memory is full (524 entries) | 19784 | header subtitle = descriptive; reserve koan for true empty (→RC-P) | P2 | n |
| MM2 (mem) | zeroed stats above the koan | all | both | "this week · 0 meetings 0h…" prints above the empty koan; `[hidden]` exists, unapplied | 2490 / 19777 | hide the stats strip when all counts 0 (→RC-P) | P2 | n |

**Files**

| ID | element/state | bp | theme | defect | anchor | fix | sev | ref |
|---|---|---|---|---|---|---|---|---|
| FL1 | row radius off-grid | all | both | row r14, icon r10 (cards = r-lg 16) | 10255-79 | `var(--r-lg)` (16) (→RC-Q) | P3 | n |
| FL2 | "in the brain" badge low visibility | all | both | ingested dot `--accent` (ink) = chrome hue → positive state easy to miss | 10359-66 | defensible as ink; if more signal, `--info`/`--live` *-soft dot | P3 | n |
| FL3 | residual ember language in comments | — | — | badge comment still says "ember dot" though code uses `--accent`; "feed the brain" repeats | 10333-34 | update comments off "ember"; de-dup copy (→RC-A) | P3 | n |
| FU1 | empty koan is a paragraph | all | both | two-line help paragraph (law: tiny koan) | 19810 / 43664 | koan + a secondary muted how-to line (→RC-P) | P2 | n |
| FU2 | only CTA under-emphasized | all | both | sole action is the quiet surface-2 outline "upload" | 10227-40 | promote upload to ink primary on empty (→RC-F) | P2 | n |
| FU3 | redundant "feed the brain" | all | both | header subtitle repeats the koan | 24038 / 43664 | say it once | P3 | n |

> Files list/row visuals unverified — every Files capture is the empty state; grid/list, icon-per-mime, uploader·date·size meta, "→ thread" origin (`renderFilesRow` 43575-641) not exercised with data.

**Codex proposal deck (memory-adjacent, bonus)**

| ID | element/state | bp | theme | defect | anchor | fix | sev | ref |
|---|---|---|---|---|---|---|---|---|
| PR1 | comment vs material | all | both | comment claims "glass" but CSS is `backdrop-filter:none` + opaque surface-2 (**correct** per card law; only the comment is wrong) | 10058-72 | fix the misleading comment | P3 | n |
| PR2 | action button radius off-grid | all | both | `.btn{border-radius:9px}` (also board draft accept/dismiss 9px) | 10141 / 2209 | `--r-sm` (8) or `--r-md` (12) (→RC-Q) | P3 | n |

**Cross-cutting (board/memory/files)**

| ID | element/state | bp | theme | defect | anchor | fix | sev | ref |
|---|---|---|---|---|---|---|---|---|
| X1 | ember tokens not retired | — | both | `--agent`/soft/glow still defined + consumed widely | 232-34/284-86 | delete tokens + `is-hot-ember`; migrate consumers (→RC-A) | P1 | n |
| X2 | off-spec warm red `#FF6B61` | — | both | room mic/cam controls hardcode `#FF6B61` (retire list) instead of `--danger` | 3120/3187 | `var(--danger)` (→RC-A) | P2 | n |
| X3 | mobile Files has no dock tab | m | both | Files absent from the 5-item dock; dock highlights Home on the Files screen | dock nav | add Files to dock/"more"; fix active mapping (→RC-H) | P1 | n |
| X4 | floating dock occludes list | m | both | last Memory cards sit behind the dock | list padding | `padding-bottom: dock + safe-area` (→RC-H) | P2 | n |
| X5 | stray pipeline text in a11y tree | all | both | "goal break down assign … verify · · ·" exposed as a flat text node | goal ribbon | `aria-hidden` / structure the ribbon | P3 | n |
| X6 | mobile toolbar crowds headers | m | both | "New card" overlaps the column-header row on mobile | 2015-21 | give the toolbar its own row (→RC-H) | P2 | n |

### 4.5 Room + Notifications + Settings + Menus

> Capture note: `inv-d-10` (dark, authed) renders the empty state correctly; `audit-05` (light, keyless) is the blank white void — the P0 is specifically the un-authed/pre-auth path (see R-1).

**Room — empty / pre-join**

| ID | element/state | bp | theme | defect | anchor | fix | sev | ref |
|---|---|---|---|---|---|---|---|---|
| R-1 | not-in-room, un-authed | d | light | `.room-empty` gated behind `.is-authed` → blank white void (no title/CTA/koan; stage not even `--bg-stage` black) | 13267 | drop `.is-authed` gate; paint `#hearthStage` black | **P0** | n |
| R-2 | "Join the room" pill | d | both | r14 off-token; `#fff`/`#0E0E10` hardcoded; Title-case label | 13282-94 / 19307 | `--r-full`/`--r-md`; stage-scoped white token; lowercase label (→RC-D) | P2 | n |
| R-3 | join:hover | d | both | grow-on-hover `scale(1.04)` | 13296-98 | press-down `.97` only (→RC-N) | P3 | n |
| R-4 | title/meta colors | d | both | `rgba(255,255,255,.45/.3)` hardcoded instead of stage-scoped tokens | 13278/13307 | tokenize `--text-on-stage-*` | P3 | n |
| R-5 | pre-join affordance | d | both | no self-view / device-check before joining — just text + button on black | 19305-08 | add self-preview tile + mic/cam toggles (glass on black) | P2 | **y** |
| R-6 | topbar title "Room" | d | both | Title-case sans while the surface is lowercase everywhere else (same for "Memory") | 791-800 | lowercase topbar headings (→RC-D) | P2 | n |
| R-7 | idle status string | d | both | two idle strings: "not connected" vs "BonfireOS ready" (camelCase brand) | 18999 | one lowercase idle string; retire "BonfireOS ready" (→RC-D) | P2 | n |
| R-8 | idle pill dot | d | light | idle dot reads greenish next to "BonfireOS ready" while not in room | pill / 844 | idle dot `--text-3`/neutral; never `--live` unless listening (→RC-M) | P2 | n |

**Room — stage & video tiles (code analysis, not captured live)**

| ID | element/state | bp | theme | defect | anchor | fix | sev | ref |
|---|---|---|---|---|---|---|---|---|
| T-1 | tile speaking | — | both | **compliant** — `0 0 0 2px --signal-500` ring + green glow, scale 1.012, r-xl, bottom-protection, Lucide muted glyph | 1231-1384 | keep | — | **y** |
| T-2 | video-label font | — | both | `500 13px sans` hardcoded; `padding` magic numbers | 1355/1352 | route through a caption token | P3 | y |
| T-3 | tile hairline | — | both | `inset 0 0 0 1px rgba(255,255,255,.07)` literal (theme-invariant on black) | 1251 | tokenize `--tile-hairline` | P3 | y |

**Room — control dock (ember residue)**

| ID | element/state | bp | theme | defect | anchor | fix | sev | ref |
|---|---|---|---|---|---|---|---|---|
| D-1 | mic/cam OFF glyph | — | both | off glyph `#FF6B61` warm-red instead of `--danger`; wash danger-derived but off-token | 3181-88 | glyph `--danger`; wash `--danger-soft` (→RC-A) | P1 | **y** |
| D-2 | recording paused | — | both | paused glyph `#FF6B61`; bg literal | 3116-21 | `--danger`/`--danger-soft` | P1 | **y** |
| D-3 | ghost/primary:hover | — | both | grow-on-hover `scale(1.07)` (danger too) | 3174/3392-95 | press-down only (→RC-N) | P3 | y |
| D-4 | dock control fills | — | both | literals `rgba(255,255,255,.12/.22)` + `#fff`; zero token coverage | 3156-93 | tokenize dock-on-glass fills | P3 | y |
| D-5 | "Send notes" | — | both | Title-case while goalcard equivalent is lowercase "send notes" | 19922/20020 vs 35570 | lowercase everywhere (→RC-D) | P2 | y |

> Leave button (`.btn--danger`, solid `--red-500` #FF453A, rotate 135°) is on-spec — the one sanctioned solid red.

**Room — PiP**

| ID | element/state | bp | theme | defect | anchor | fix | sev | ref |
|---|---|---|---|---|---|---|---|---|
| P-1 | window | — | both | mostly on-spec glass, but r-xl (22) — law PiP = r-2xl (28); shadow hardcoded | 12913-28 | `--r-2xl`; second shadow → `--glass-shadow` (→RC-Q) | P3 | y |
| P-2 | dot | — | both | live green + `pip-breathe infinite` whenever shown, not gated to listening | 12956-68 | acceptable (live meeting) or gate to listening | P3 | y |
| P-3 | speaker ring/avatar/label | — | both | **compliant** — green ring, mono avatar, quiet mono label | 13027-100 | keep | — | y |

**Notification center**

| ID | element/state | bp | theme | defect | anchor | fix | sev | ref |
|---|---|---|---|---|---|---|---|---|
| N-1 | unread badge ring | d | both | rest ring `--agent-soft` (ember), `is-arriving` pulses `--glow-agent` | 9818-30 | ring→`--accent-soft`/`--info-soft`; pulse→mono/`--accent` (badge fill ink already OK) (→RC-A) | P1 | n |
| N-2 | panel anchoring | d | both | `fixed; top:50%` screen-centered, but the bell sits at rail-bottom → detached floating window | 9834-53 | anchor near the bell (bottom-left) / draw a tether | P2 | n |
| N-3 | item copy | d | both | "Goal needs attention: Probe…" Title-case from an agent | 9980 | lowercase agent alert copy (→RC-D) | P3 | n |
| N-4 | panel glass | d | both | **compliant** — glass-chrome+blur+border + highlight/shadow + r-2xl; ink unread dot. Cite as the reference the account menu should match | 9834-993 | keep | — | n |

**Account menu**

| ID | element/state | bp | theme | defect | anchor | fix | sev | ref |
|---|---|---|---|---|---|---|---|---|
| AM-1 | material | d | both | opaque surface-1, no backdrop-filter/highlight while sibling notif panel is glass | 1059-75 | glass-chrome+blur+border+highlight/shadow (→RC-E) | P1 | n |
| AM-2 | radius | d | both | r-xl (22) while notif window is r-2xl (28) | 1071 | align both floating popovers on one radius (→RC-Q) | P3 | n |
| AM-3 | bottom-edge position | d | light | anchored off the bottom rail avatar → "sign out" clips at viewport bottom | 1059-64 | bottom collision guard; open upward | P2 | n |
| AM-4 | avatar fill | d | both | avatar on surface-1; law = surface-3 initials (nearly vanishes on white) | 973-86 | avatar bg → `--surface-3` (→RC-K) | P3 | n |
| AM-5 | avatar img outline | d | both | `outline:1px solid rgba(255,255,255,.1)` hardcoded | 1005 | tokenize | P3 | n |
| AM-6 | link font | d | both | "settings"/"sign out" sans (lowercase, but sans) | 1125-35 | consider `--type-label` mono, or accept sans consistently (→RC-C) | P3 | n |

**Settings dialog**

| ID | element/state | bp | theme | defect | anchor | fix | sev | ref |
|---|---|---|---|---|---|---|---|---|
| S-1 | close (×) hit target | d | both | 30×30 < 44px min | 3795-805 | grow to 44×44 (icon stays 30 via padding) | P2 | n |
| S-2 | primary casing | d | both | "Upload avatar/Save profile/Change password/Add a passkey…/Done" Title-case; siblings "clear"/"sign out" lowercase | 20270-297/20479 | lowercase all verbs (→RC-D) | P2 | n |
| S-3 | panel material | d | both | opaque surface-1 + shadow-3, r-2xl — comment "kit Dialog opaque" = **sanctioned** | 3711-63 | keep opaque; fix AM-1 (menus=glass) for a consistent material language (→RC-E) | P3 | n |
| S-4 | "Save profile" vs "Done" fill | d | dark | capture shows "Save profile" muted grey while "Done" bright white — both `.btn--primary` | 3359-69 / 20273 | confirm both resolve to `--accent`; remove any muting override | P2 | n |
| S-5 | "✓ saved" glyph | d | both | text "✓" as an icon | 20360/20376 | Lucide `check` + mono label (→RC-O) | P3 | n |
| S-6 | nav item | d | both | **compliant** — lowercase, r-md, `aria-current` fill `--surface-3` | 3823-54 | keep | — | n |

**Popover dismissal / stacking**

| ID | element/state | bp | theme | defect | anchor | fix | sev | ref |
|---|---|---|---|---|---|---|---|---|
| PV-1 | notif + account mutual exclusion | d | both | opening either leaves both stacked; neither closes the other | 36678-86 / 23031-49 | one "one-popover-at-a-time" dispatcher | P2 | n |
| PV-2 | notif light-dismiss | d | both | outside-click handler omits `notificationPanel` (closes only via Esc / re-tap) | 21833-40 | add notif panel to the outside-click close list | P2 | n |
| PV-3 | Escape order | d | both | two stacked popovers need two Escapes | 21852-72 | fine once PV-1 lands | P3 | n |

**Chrome-wide ember & systemic**

| ID | element/state | bp | theme | defect | anchor | fix | sev | ref |
|---|---|---|---|---|---|---|---|---|
| E-1 | `--agent` token defined | — | both | retired ember + soft + glow still declared, ~40 uses incl. N-1 + E-2 | 232-34/284-86 | delete + migrate (→RC-A) | P1 | n |
| E-2 | nav "you are here" marker | d | both | active-tool left marker ember over the already-inverted active chip — in persistent chrome | 515-25 | marker → `--accent` hairline / drop (→RC-A) | P1 | n |
| E-3 | `is-hot-ember` class | d | both | legacy name persists though color already migrated to `--live` | 784-89 | rename to a green/presence class (→RC-A) | P3 | n |
| X-1 | grow-on-hover (systemic) | d | both | `scale(1.03-1.08)` on rail/dock/leave/topbar-live/room-join | listed | remove hover grows; press-down `.97` + breathe (→RC-N) | P3 | n |
| X-2 | button casing (systemic) | — | both | Title-case primaries vs lowercase verbs across the app | S-2/R-2/D-5/35570 | lowercase all UI verbs (→RC-D) | P2 | n |

---

## 5. "Needs a generated reference screen" shortlist

Every `ref=y` finding plus bespoke compositions that can't be judged self-serve. Generate **desktop + mobile** unless noted. Ordered by build priority.

1. **Deliverable drawer — deck-hero** — `intel/DD-1, DD-6` (+DD-3). *States:* goal/packaging deliverable open, showing a deck-cover thumbnail hero + primary "open the deck/present" over a demoted "run details" disclosure; desktop + mobile. *Why:* a brand-new hero layout — the crown-jewel path doesn't exist yet; also settles whether the drawer panel is glass (DD-6).
2. **Ingestion pulse — empty / sparse / healthy** — `intel/IP-1, IP-2, IP-3`. *States:* 0-data koan-on-baseline, 1-meeting sparse, and a healthy histogram; desktop + mobile. *Why:* current renders read as broken; a designed empty/sparse target is needed to floor peak-normalization and replace ghost bars.
3. **Board — lopsided / empty column treatment** — `board/B2` (+BE1, BE2). *States:* one lane holding >80% of cards (collapse-empty-to-rail vs populated-lanes-grow) and the fully-empty board; desktop primary + mobile. *Why:* a layout decision that needs to be seen, not specced.
4. **Glass menu / dialog material reference** — `room/AM-1` (menu) + `board/CD1` (card-detail dialog). *State:* the account menu as glass beside the already-glass notification panel, plus the settled menus-vs-dialogs material call; desktop. *Why:* resolves RC-E's doctrine conflict with a picture (menus=glass, dialogs=opaque) so the material language is consistent.
5. **Agent-letter card unified with scout bubbles** — `chat/AL-1` (+AL-2, AL-3). *States:* a longform "letter" reply and a short reply reading as the *same* Scout identity; desktop + mobile. *Why:* bespoke — how to bridge two grammars into one sender identity isn't derivable from current CSS.
6. **Goalcard running state, de-embered** — `chat/GC-1`, `shell/GM1`, `shell/OA1` (+GC-3 green decision). *State:* the goalcard mid-run (ring/%/kicker/active-node/spark) and the voice-island "acting" cue, both using the sanctioned mono-breathing-dot/`--info` "working" language; desktop. *Why:* establishes the single replacement vocabulary for retired ember motion+color across every "machine working" surface.
7. **Populated packages binder + artifact library** — `intel/PK-4`, `intel/AL-1(lib)` (+PK-5 de-embered sweep). *States:* a populated package row + 6-stage rail + artifact list, and a populated library sidebar/detail editor; desktop + mobile. *Why:* never rendered with data — row/rail/list density, the flex-wrap label risk, and the advancing animation can't be judged empty.
8. **Login gate glass in light** — `shell/L1` (+L2/L3). *State:* the gate over a strengthened ambient so its coded glass actually refracts (not flat white), with the disabled primary still reading as the door; desktop + mobile. *Why:* the light-theme glass fails self-serve because the ambient behind it is too weak — needs a target to calibrate the backdrop.
9. **Live / connected room — stage + tiles + control dock + PiP** — `room/T-1,T-2,T-3, D-1..D-5, P-1,P-2,P-3`. *State:* a connected multi-tile room after ember removal (mic/cam-off = `--danger`, speaking ring = green, PiP glass r-2xl); desktop + mobile. *Why:* the entire connected room was never captured — a live capture or generated reference is required to verify the de-embered dock/tiles/PiP visually. (Prefer a live-room capture if feasible.)
10. **Room pre-join self-preview** — `room/R-5`. *State:* the black stage with a self-view tile + mic/cam toggles (glass controls) before joining; desktop + mobile. *Why:* a bespoke new affordance (device pre-flight) that doesn't exist in code yet.
11. **Green-for-success decision reference** — `chat/SYS-2, GC-3` (+`board/MS3`, `room/R-8`). *State:* goalcard-complete and manifest-shipped shown both ways (green=live-only with a mono ink check vs green=terminal-success); desktop. *Why:* a doctrine decision (RC-M) that needs to be seen in both readings before it's applied app-wide.
12. **Mobile dock active-state on the pill** — `shell/TR2`. *State:* the horizontal bottom pill dock's active indicator (filled ink circle / centered mono dot, not a left-flank vertical hairline); mobile. *Why:* the vertical-rail tick doesn't translate to the horizontal dock — needs a target for the mobile active mark. (Can fold into #9's mobile shot.)

**Re-capture (not generate):** `chat/MO-5` — the mobile **conversation** view was never captured (the mobile slot duplicated the thread list); re-capture bubble spacing/scroll on-device before/after fixes.

---

## 6. Already-compliant — do NOT regress

The auditors explicitly verified these correct. Preserve through the reskin.

**Shell:** `TB2` (topbar heading sans + lowercase mono subtitle/date + mono status pill); `L4` (56px ink mono login tile, lowercase mono tagline, Lucide Face-ID glyph); `OA3` (voice-island `--warn` consent-wait + `--live-soft` listening glow); `SP1` (idle pill semantics acceptable).

**Chat:** `TL-4` (thread section headers mono `--type-label` lowercase text-3); `TL-5` (light thread cards surface-2/line-1/r16, sans title/mono timestamp); `BY-1` (you-bubble is `--accent #F5F5F7` on `--on-accent` — the ink-accent law, **keep**); `BS-1` (scout bubble surface-2 + line-1); `GC-4` (goalcard action doors already ember-swept to ink/outline); `GC-5` (gate/park `--warn` breathing dot 2400ms); `MM-2` (code surface-3 mono r6, blockquote left-border allowed); `CR-4` (crumb pill mono lowercase outlined); `CP-5` (palette glass sheet + mono footer keycaps); `MO-4` (mobile thread cards surface-2/line-1/r16, `:active` .985).

**Intelligence:** `IP-5` (pulse bars `--accent` + `--live` only on the live bar); `ST-3` (tile sub-label + delta mono lowercase, delta opacity .65); `ST-4` (tile grid 4→2×2 reflow); `CL-2` (contribution bars `--accent` mono nominal); `TC-2` (recurring counts `--text-3` mono, no ember); `TC-4` ("in lockstep" `--live` glyph); `TC-5` (consensus 3→1 col reflow); `DL-4` (ratify pill surface-2+line-1 mono, disabled .5); `AL-2/AL-3/AL-4 (lib)` (**artifact library was done right** — status hues sanctioned state-only, `--accent-soft` selection, mono focus ring).

**Board/Memory/Files:** memory "flame" (monochrome inline SVG, gray, not emoji/ember — on-law); `PR1` (proposal-card material `backdrop-filter:none` opaque surface-2 is correct per card law — only the comment is wrong); `FL2` (ingested dot ink is defensible).

**Room/Notifications/Settings:** `T-1` (video-tile speaking ring `--signal-500` + glow, scale 1.012, r-xl, Lucide muted glyph); `P-3` (PiP tile green ring, mono avatar, quiet mono label); `N-4` (**notification panel is the glass reference** — glass-chrome+blur+border+highlight/shadow, r-2xl, ink unread dot; the account menu should match it); `S-3` (settings-panel opaque surface-1 = sanctioned dialog material — **dialogs stay opaque; only menus become glass**); `S-6` (settings-nav lowercase, r-md, `aria-current` surface-3); Leave button (solid `--red-500` #FF453A — the one sanctioned solid red).

**Doctrine guardrails for the waves:**
- Green (`--live`) is state-only; do not sprinkle it decoratively (pending the RC-M decision on terminal success).
- Warn (`--warn`) is for real waiting/alert states only (gate/park) — not a category color.
- `--accent` is the ink/white; never hardcode `#fff`/`#000` — use `--accent`/`--on-accent`.
- State = pill or dot, **never** an edge/left-border stripe.
- Menus = glass; dialogs = opaque; cards = opaque (no rest shadow, lift on hover only).
- System labels = lowercase Geist Mono via `--type-label`; timestamps = mono; button verbs = lowercase.
- Motion = press-down `.97` + a single 2400ms breathe; no grow-on-hover.
- Icons = Lucide stroke-1.75; no emoji/dingbats/text glyphs.
