# 004 — Gate hover motion behind (hover:hover); finish reduced-motion coverage

- **Status**: DONE
- **Commit**: bd289db
- **Severity**: MEDIUM
- **Category**: Accessibility
- **Estimated scope**: 1 file, ~14 rule wraps + 1 insertion

## Problem

**Part A — sticky touch hovers.** index.html contains zero `@media (hover: hover)` guards, but the app ships on mobile/PWA (safe-area insets, mobile stage). On touch, a tap fires a sticky `:hover`, leaving elements stuck raised/scaled until the next tap elsewhere. Every hover rule that MOVES an element must be gated. Verified sites (line ≈, commit bd289db — match on selector text, not line):

| ≈Line | Selector | Motion |
|---|---|---|
| 567 | `.tool-rail__tool:hover .tool-rail__label` | `translate: 0 -50%` label reveal |
| 583 | `.tool-rail__tool:hover` | `transform: scale(1.08)` |
| 922 | `.topbar-live:hover` | transform |
| 2368 | `.card:hover` | transform |
| 3410 | `.controls .btn--primary:hover:not([disabled])` | transform |
| 3644 | `.controls .btn--danger:hover:not([disabled])` | transform |
| 5889 | `.office-app-card:hover` | transform |
| 6114 | `.office-agent-card:hover` | transform |
| 6386 | `.os-assistant__toggle:hover` | transform |
| 7713 | `.chat-thread-item:hover:not([aria-pressed="true"])` | transform |
| 9731 | `.scout-chat-send:hover:not([disabled])` | transform |
| 16288 | `#newCard:hover:not([disabled])` | transform |
| 16362 | `.board-video-tile:hover, .board-video-tile:focus-visible` | transform (rule text updated by plan 003 — read it fresh) |
| 19515 | `.palette__tile:hover` | `translateY(-2px)` |

**Part B — ungated infinite research animations.** The reduced-motion catch-all block (≈21151-21271) individually kills every ambient loop EXCEPT three deep-research spinners, which run infinite `scale()` movement for minutes even with reduced motion on:

```css
/* index.html:9352 — current */
        animation: bf-think var(--pulse-cycle) var(--ease) infinite;
/* index.html:13438 — current */
        animation: bf-ringpulse 1600ms var(--ease) infinite;
/* index.html:13509 — current */
        animation: bf-flamebreathe 1200ms var(--ease) infinite;
```

(selectors: `.scout-chat-research__step.is-active .scout-chat-research__step-dot`, `.research-ring__pulse`, `.research-overall__dot`)

## Target

**Part A**: each listed rule wrapped:

```css
/* pattern — exemplar for .palette__tile:hover */
      @media (hover: hover) and (pointer: fine) {
        .palette__tile:hover { transform: translateY(-2px); box-shadow: var(--shadow-2); border-color: var(--line-2); }
      }
```

For the one grouped rule (`.board-video-tile:hover, .board-video-tile:focus-visible`): SPLIT it — the `:hover` selector goes inside the media query; a duplicate rule with only the `:focus-visible` selector keeps the identical declarations OUTSIDE the media query (keyboard focus must work on touch devices too).

**Part B**: inside the existing `@media (prefers-reduced-motion: reduce)` block (≈21151), directly after the line `.login-presence__dot { animation: none; }`, insert:

```css
        .research-ring__pulse,
        .research-overall__dot,
        .scout-chat-research__step.is-active .scout-chat-research__step-dot { animation: none; }
```

## Repo conventions to follow

- The reduce block's style: grouped selectors, `{ animation: none; }`, two-space continuation — imitate the `.topbar__mark.is-listening, ...` group at its top.
- Keep each wrapped hover rule's declarations byte-identical — only the wrapping changes.
- Preserve surrounding comments (e.g. the "hover reveals after a beat" comment above the rail-label rule stays attached to its rule inside the wrap).

## Steps

1. For each Part-A row: read the current rule block, wrap it in `@media (hover: hover) and (pointer: fine) { ... }` at the same indent depth (+2 spaces for the rule inside). Where a companion `:focus-visible` rule already exists separately (e.g. `.tool-rail__tool:focus-visible .tool-rail__label` at ≈573), leave the companion untouched outside the wrap.
2. Split the grouped `.board-video-tile` rule per Target.
3. Sweep for stragglers the table may have missed:
   `grep -n ':hover' index.html` and inspect each block that declares `transform:`, `translate:`, `scale:`, or `rotate:` (excluding `: none`) — wrap any found using the same pattern. Rules that only change color/background/border/shadow are OUT of scope.
4. Insert the Part-B group into the reduce block.

## Boundaries

- Do NOT wrap hover rules that only recolor (background/border/color/box-shadow-only) — scope is motion.
- Do NOT touch the `:root` reduced-motion token zeroing block (≈326).
- Do NOT reformat or re-indent anything you aren't wrapping.
- Do NOT gate `:active` press feedback — presses are real on touch.
- If a step doesn't match the code you find, STOP and report instead of improvising.

## Verification

- **Mechanical**:
  - `grep -c '@media (hover: hover) and (pointer: fine)' index.html` → ≥14.
  - `grep -A3 'login-presence__dot' index.html | grep -c 'research-ring__pulse'` → 1.
  - `cd /Users/ajhart/meetingassist && go test -count=1 -run 'TestIndex' .` → PASS.
- **Feel check** (reviewer): in DevTools device emulation (touch), tap a feed card / palette tile — nothing sticks in a raised state. Rendering panel → emulate `prefers-reduced-motion: reduce`, start a deep research run — step dots, ring pulse, and overall dot are static; opacity feedback elsewhere remains.
- **Done when**: all counts pass, tests pass, touch emulation shows no stuck hover lift.
