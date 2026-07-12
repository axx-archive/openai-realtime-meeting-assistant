# 008 — Correct three durations that drifted up to --dur-slow, and un-fight the tile drag

- **Status**: DONE
- **Commit**: d8846df
- **Severity**: MEDIUM
- **Category**: Easing & duration / Interruptibility
- **Estimated scope**: 1 file, 4 small edits

## Problem

Plan 005's tokenization pass rounded three 0.3s values UP to `--dur-slow` (360ms), crossing the sub-300ms UI budget. One of them makes a live-room gesture measurably worse: the video tile's `transform` transition now fights the drag.

**A — video tile drag lag.** `moveVideoTileDrag` writes `style.transform` on every pointermove, but the base rule transitions `transform` over 360ms and `.is-dragging` never disables it — the grabbed tile eases toward the pointer through the whole curve instead of sticking to it.

```css
/* index.html ≈1348-1357 — current (tail of the .video-tile rule) */
        cursor: grab;
        user-select: none;
        transition: box-shadow var(--dur-slow) var(--ease), transform var(--dur-slow) var(--ease), opacity var(--dur-med) var(--ease);
      }
```

```css
/* index.html ≈1389-1393 — current */
      .video-tile.is-dragging {
        opacity: 0.94;
        box-shadow: var(--glow-accent), var(--shadow-2);
        cursor: grabbing;
      }
```

```js
/* index.html ≈31851-31857 — the driver (do not modify; context only) */
      function moveVideoTileDrag(dx, dy, x, y) {
        if (!videoDragState?.started) {
          return
        }
        videoDragState.tile.style.transform = `translate3d(${dx}px, ${dy}px, 0)`
        moveVideoDropSlot(x, y)
      }
```

**B — mobile voice-launch press feedback at 360ms.** Press feedback budget is 100–160ms; this drives the `:active` background swap on the primary mobile tap target.

```css
/* index.html ≈18968-18980 — current (inside @media (max-width: 640px)) */
        .office-launch__island {
          border: none;
          background: transparent;
          box-shadow: none;
          backdrop-filter: none;
          -webkit-backdrop-filter: none;
          padding: 30px 0 26px;
          gap: 18px;
          border-radius: 24px;
          transition: background var(--dur-slow) var(--ease);
        }
```

**C — waveform listening fade at 360ms.** Ambient decorative fade; `--dur-med` (220ms) is the nearer compliant token.

```css
/* index.html ≈5757-5762 — current */
      .office-launch__bars .bf-wave-bar {
        border-radius: 99px;
        transform-origin: bottom;
        opacity: 0.5;
        transition: opacity var(--dur-slow) var(--ease);
      }
```

**D — stale comment.** The comment above the tool-pane rule still says ".4s" although plan 001 cut the animation to `var(--dur-med)`:

```css
/* index.html ≈5555 — current comment (verbatim substring to find) */
         tab panes enter with the canon rise (bf-tabin .4s)
```

## Target

**A** — retune the transform term to `--dur-med` and exclude `transform` from transitions while dragging:

```css
        cursor: grab;
        user-select: none;
        transition: box-shadow var(--dur-slow) var(--ease), transform var(--dur-med) var(--ease), opacity var(--dur-med) var(--ease);
      }
```

```css
      .video-tile.is-dragging {
        opacity: 0.94;
        box-shadow: var(--glow-accent), var(--shadow-2);
        cursor: grabbing;
        transition: box-shadow var(--dur-med) var(--ease), opacity var(--dur-med) var(--ease);
      }
```

(Leaving `box-shadow` at `--dur-slow` in the base rule is deliberate — the speaker-glow fade is decorative and this plan changes only what the finding cited. The `.is-dragging` override lists box-shadow/opacity so the lift glow still eases in while transform snaps to the pointer. When the class is removed on release, the base rule's 220ms transform transition eases the tile into its settle position.)

**B**:

```css
          transition: background var(--dur-fast) var(--ease);
```

**C**:

```css
        transition: opacity var(--dur-med) var(--ease);
```

**D** — change the comment substring `(bf-tabin .4s)` to `(bf-tabin, token speed)`. Comment text only; no CSS change.

**E — orphaned keyframes.** Two toast entrance keyframes are defined but have zero `animation:` consumers anywhere in the file (verified at d8846df — toasts actually enter via `bf-popin`). Delete both definitions:

```css
/* index.html ≈4040 — delete this whole line */
      @keyframes bf-toastin { from { transform: translateY(10px); opacity: 0; } to { transform: translateY(0); opacity: 1; } }
```

```css
/* index.html ≈5274-5277 — delete these four lines */
      @keyframes toast-in {
        0%   { opacity: 0; transform: translate3d(0, 8px, 0) scale(0.98); }
        100% { opacity: 1; transform: translate3d(0, 0, 0) scale(1); }
      }
```

## Repo conventions to follow

- Tokens: `--dur-fast` 120ms / `--dur-med` 220ms / `--dur-slow` 360ms / `--ease`. Exemplar of press feedback at token speed: `.board-preview__card` (≈15846) transitions `background var(--dur-fast) var(--ease)`.
- Tests pin selectors `.video-tile.is-dragging` and `.video-tile.is-active-speaker` (frontend_latency_test.go ≈1252, ≈1281) — selector names must not change; property edits inside the rules are safe.

## Steps

1. Edit the `.video-tile` base transition (A, first block).
2. Add the `transition:` line to the existing `.video-tile.is-dragging` rule (A, second block) — extend the rule, don't create a duplicate.
3. Edit the `.office-launch__island` transition inside the max-width:640px media query (B). There may be a non-mobile `.office-launch__island` rule elsewhere — do NOT touch that one.
4. Edit the `.bf-wave-bar` opacity transition (C).
5. Edit the comment (D).
6. Delete the two orphaned keyframe definitions (E).
7. Run verification.

## Boundaries

- Do NOT touch `moveVideoTileDrag` or any JS.
- Do NOT retune any other `--dur-slow` usage — only the three cited rules.
- Do NOT rename selectors or restructure rules.
- If an excerpt doesn't match the code you find, STOP and report instead of improvising.

## Verification

- **Mechanical**:
  - `grep -n 'transform var(--dur-slow)' index.html` → the `.video-tile` rule no longer appears in the hits.
  - `grep -c 'bf-toastin\|@keyframes toast-in' index.html` → **0**
  - `cd /Users/ajhart/meetingassist && go test -count=1 -run 'TestIndex' .` → ok
- **Feel check** (keyless smoke on :3171, join a room so the stage renders):
  - Drag a video tile: it tracks the pointer 1:1 with zero rubber-banding; on release it settles over ~220ms.
  - On a phone-width viewport, tap-and-hold the voice-launch island: the background swap reads immediate (~120ms), release likewise.
  - Toggle listening state: waveform bars fade over ~220ms.
- **Done when**: greps and TestIndex pass and the drag feel-check shows no pointer lag.
