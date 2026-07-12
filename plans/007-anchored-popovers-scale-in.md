# 007 — Anchored popovers scale in from their trigger

- **Status**: DONE
- **Commit**: d8846df
- **Severity**: MEDIUM
- **Category**: Physicality & origin
- **Estimated scope**: 1 file, 4 rule edits + 4 small `@starting-style` blocks + 1 reduced-motion line

## Problem

Four anchored surfaces appear with zero entrance motion — they pop into existence at full size, with nothing explaining where they came from. Two toggle the `hidden` attribute (`display: none` ↔ shown), two are created and appended on open:

```css
/* index.html ≈1172-1190 — current (head of the rule; opens to the RIGHT of the rail avatar, bottom-aligned) */
      .account-menu {
        position: absolute;
        top: auto;
        right: auto;
        bottom: 0;
        left: calc(100% + 12px);
```

```css
/* index.html ≈15163-15177 — current (opens ABOVE the Invite button, centered; NOTE the existing translateX(-50%) — every transform below must compose with it) */
      .invite-pop {
        position: absolute;
        bottom: calc(100% + 10px);
        left: 50%;
        transform: translateX(-50%);
```

```css
/* index.html ≈16551-16565 — current (opens ABOVE its trigger, right-aligned) */
      .board-menu {
        position: absolute;
        right: 0;
        bottom: calc(100% + 10px);
```

```css
/* index.html ≈11039-11053 — current (opens BELOW its kebab trigger, left-aligned; a row/tile variant at ≈11055-11061 sets top:auto; right:10px — still below the trigger, right-aligned) */
      .files-folder-menu {
        position: absolute;
        top: calc(100% + 6px);
        left: 0;
```

None of these rules currently has a `transition` or `transform-origin`. Show/hide mechanics (context only — do NOT edit JS): `accountMenu.hidden = !next` (≈26391), `boardMenu` same pattern via `toggleBoardDockMenu` (≈32016); `invite-pop` is `createElement`d + `bar.append(pop)` (≈53630-53635) and `.remove()`d on close; `files-folder-menu` is created per open (≈49797, ≈49915). Known quirk to leave alone: the account menu portals between `document.body` and `topbarAccount` on phones (≈26396-26402, test-pinned) — a re-append while open would replay the entrance, which only occurs on viewport resize with the menu open; acceptable.

## Target

One pattern for all four: entrance scales from the anchor corner via `@starting-style` + a transform/opacity transition. Exit stays instant (the system's response snaps; `display:none`/`remove()` need no exit choreography). Browsers without `@starting-style` (pre-Chrome 117 / pre-Safari 17.5) degrade to today's instant appearance.

Add to the END of each base rule (before its closing brace), then add the paired `@starting-style` block immediately AFTER that rule closes:

```css
/* .account-menu — origin faces its trigger at bottom-left */
        transform-origin: bottom left;
        transition: opacity var(--dur-fast) var(--ease), transform var(--dur-fast) var(--ease);
      }

      @starting-style {
        .account-menu:not([hidden]) { opacity: 0; transform: scale(0.96); }
      }
```

```css
/* .invite-pop — opens upward, centered; compose with the existing translateX. Bigger surface → --dur-med */
        transform-origin: bottom center;
        transition: opacity var(--dur-med) var(--ease), transform var(--dur-med) var(--ease);
      }

      @starting-style {
        .invite-pop { opacity: 0; transform: translateX(-50%) scale(0.96); }
      }
```

```css
/* .board-menu — opens upward from a right-aligned trigger */
        transform-origin: bottom right;
        transition: opacity var(--dur-fast) var(--ease), transform var(--dur-fast) var(--ease);
      }

      @starting-style {
        .board-menu:not([hidden]) { opacity: 0; transform: scale(0.96); }
      }
```

```css
/* .files-folder-menu — opens downward from the kebab */
        transform-origin: top left;
        transition: opacity var(--dur-fast) var(--ease), transform var(--dur-fast) var(--ease);
      }

      @starting-style {
        .files-folder-menu { opacity: 0; transform: scale(0.96); }
      }
```

Additionally, inside the row/tile variant rule (≈11055-11061, `.files-row .files-folder-menu, .file-tile .files-folder-menu`), add:

```css
        transform-origin: top right;
```

Reduced motion — **no edit needed, do not add one.** The token-zeroing block at index.html ≈326-332 sets `--dur-fast`/`--dur-med`/`--dur-slow` to `0ms` under `prefers-reduced-motion: reduce`, so all four entrances (token-timed) become instant automatically. Do NOT touch any `@media (prefers-reduced-motion: reduce)` block: two tests slice from the LAST such block (notifications_test.go ≈542, frontend_latency_test.go ≈2170) and another requires `grillstage__` within 900 chars after `.voice-ledger { animation: none; }` (wave12_private_grill_test.go ≈528) — any insertion there is risk for zero benefit.

## Repo conventions to follow

- Tokens only: `--dur-fast` (120ms), `--dur-med` (220ms), `--ease`. No new curves, no hand-typed durations.
- Never `scale(0)` — the starting scale is exactly `0.96`.
- Style blocks are co-located per component in the monolith `<style>` — each `@starting-style` block goes right after its component's rule, matching the surrounding 6-space indentation.
- Exemplar of an anchored surface done right: the notification panel (≈10251-10252) pairs its entrance `animation` with `transform-origin: left center;`.

## Steps

1. `.account-menu` (≈1172): append the two declarations; add its `@starting-style` block after the rule (before `.account-menu[hidden]`).
2. `.invite-pop` (≈15163): append `transform-origin: bottom center;` and the `--dur-med` transition; add its `@starting-style` block after the rule.
3. `.board-menu` (≈16551): append the two declarations; add its `@starting-style` block after the rule (before `.board-menu[hidden]`).
4. `.files-folder-menu` (≈11039): append the two declarations; add its `@starting-style` block after the row/tile variant rule; add `transform-origin: top right;` inside the row/tile variant rule.
5. Run verification.

## Boundaries

- Do NOT touch any JS (show/hide mechanics stay exactly as they are).
- Do NOT add exit animations, `transition-behavior: allow-discrete`, or `display` transitions — entry only.
- Do NOT touch other popovers (scout-mention-popover, scout-deliverables-popover, filters popover, room ⋯ popover) — the mention popover especially is a high-frequency typing surface where entrance animation would be a regression.
- Do NOT change positioning declarations, sizes, colors, or shadows.
- If an excerpt doesn't match the code you find, STOP and report instead of improvising.

## Verification

- **Mechanical**:
  - `grep -c '@starting-style' index.html` → **4**
  - `cd /Users/ajhart/meetingassist && go test -count=1 -run 'TestIndex' .` → ok (existing pins `.account-menu[hidden]`, `#appShell... ~ .account-menu` unaffected)
- **Feel check** (keyless smoke on :3171):
  - Click the rail avatar: the account menu grows from its bottom-left corner (the side facing the avatar), not from center; ~120ms, subtle.
  - In DevTools Animations panel at 10% speed, confirm the invite popover stays horizontally centered through the entire entrance (translateX compose check — if it slides sideways while scaling, the starting transform dropped the translateX).
  - Open a files kebab menu: grows downward from the kebab.
  - Spam-click the account trigger: no flicker, no stuck intermediate state.
  - Toggle prefers-reduced-motion (DevTools Rendering panel): menus fade in with zero scale movement.
- **Done when**: grep count is exact, TestIndex green, all four surfaces visibly originate at their anchor corner.
