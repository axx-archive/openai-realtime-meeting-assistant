# 003 — Board video tiles: composite-only speaker/hover emphasis

- **Status**: DONE
- **Commit**: bd289db
- **Severity**: HIGH
- **Category**: Performance
- **Estimated scope**: 1 file, 1 rule-block rewrite (4 rules)

## Problem

The board video strip animates `width` — a layout property — every time the active speaker changes or a tile is hovered/focused, reflowing the whole flex rail frame-by-frame for 220ms **while WebRTC video is decoding on the same main thread**. Speaker changes are frequent during a call, so the rail continuously jitters and reflows.

```css
/* index.html:16343-16375 — current */
      .board-video-tile {
        position: relative;
        width: 78px;
        aspect-ratio: 4/3;
        height: auto;
        overflow: hidden;
        border: 1px solid var(--line-1);
        border-radius: var(--r-md);
        background: var(--ink-900);
        color: #FFFFFF;
        transition: width var(--dur-med) var(--ease), transform var(--dur-med) var(--ease), box-shadow var(--dur-med) var(--ease), border-color var(--dur-med) var(--ease);
      }

      .board-video-tile.is-speaker {
        width: 96px;
        border-color: var(--live);
        box-shadow: var(--glow-live);
      }

      .board-video-tile:hover,
      .board-video-tile:focus-visible {
        width: 140px;
        transform: translateY(-4px);
        border-color: var(--line-2);
        outline: none;
        z-index: 2;
      }
      .board-video-tile.is-speaker:hover,
      .board-video-tile.is-speaker:focus-visible {
        width: 160px;
        border-color: var(--live);
      }
```

The container `.board-video-strip` (≈16332) is `display: flex; gap: 8px;` with **no** `overflow` set, so transform-scaled tiles will not be clipped. NOTE: line numbers are from commit bd289db — always match on the exact code excerpt.

## Target

Tiles stay a fixed 78px; all emphasis moves to `transform: scale()` (composited, no layout). Scale factors preserve the original visual sizes exactly (96/78 = 1.23, 140/78 = 1.79, 160/78 = 2.05). Emphasized tiles overlap neighbors (dock-style) instead of pushing them — that's intended: the rail stops shifting on every speaker change.

```css
/* target — replaces the four rules above, same order, same surrounding comments */
      .board-video-tile {
        position: relative;
        width: 78px;
        aspect-ratio: 4/3;
        height: auto;
        overflow: hidden;
        border: 1px solid var(--line-1);
        border-radius: var(--r-md);
        background: var(--ink-900);
        color: #FFFFFF;
        transition: transform var(--dur-med) var(--ease), box-shadow var(--dur-med) var(--ease), border-color var(--dur-med) var(--ease);
      }

      .board-video-tile.is-speaker {
        transform: scale(1.23);
        border-color: var(--live);
        box-shadow: var(--glow-live);
        z-index: 1;
      }

      .board-video-tile:hover,
      .board-video-tile:focus-visible {
        transform: translateY(-4px) scale(1.79);
        border-color: var(--line-2);
        outline: none;
        z-index: 2;
      }
      .board-video-tile.is-speaker:hover,
      .board-video-tile.is-speaker:focus-visible {
        transform: translateY(-4px) scale(2.05);
        border-color: var(--live);
      }
```

## Repo conventions to follow

- TEST PIN (selector-level): `frontend_latency_test.go` ≈line 1288 requires the exact substring `.board-video-tile.is-speaker` to exist in index.html. The target above keeps it — do not rename any selector.
- Motion tokens: `var(--dur-med)`, `var(--ease)` — already used here; keep them.
- `.board-video-tile video.is-local { transform: scaleX(-1); }` (≈16387) is on the child video element and is unaffected — leave it.

## Steps

1. In `/Users/ajhart/meetingassist/index.html`, replace the `transition:` line of `.board-video-tile` (drop the `width var(--dur-med) var(--ease),` term; keep transform/box-shadow/border-color).
2. In `.board-video-tile.is-speaker`, replace `width: 96px;` with `transform: scale(1.23);` and add `z-index: 1;` after the `box-shadow` line.
3. In `.board-video-tile:hover, .board-video-tile:focus-visible`, replace `width: 140px;` + `transform: translateY(-4px);` with the single `transform: translateY(-4px) scale(1.79);`.
4. In `.board-video-tile.is-speaker:hover, .board-video-tile.is-speaker:focus-visible`, replace `width: 160px;` with `transform: translateY(-4px) scale(2.05);`.
5. Search for any OTHER `.board-video-tile` width overrides or inline `style.width` writes targeting these tiles: `grep -n 'board-video-tile' index.html` — if JS sets tile widths, STOP and report.

## Boundaries

- Do NOT touch `.board-video-strip` (the container) or `.pip-tile` / `.video-tile` (different components).
- Do NOT rename selectors — `.board-video-tile.is-speaker` is test-pinned as a substring.
- Do NOT change the child `video` / `.owner-avatar` / `.monogram` rules.
- If a step doesn't match the code you find, STOP and report instead of improvising.

## Verification

- **Mechanical**:
  - `grep -n 'transition: width' index.html | grep board-video` → 0 hits.
  - `grep -c '\.board-video-tile\.is-speaker' index.html` → unchanged from before your edit (≥2).
  - `cd /Users/ajhart/meetingassist && go test -count=1 -run 'TestIndex' .` → PASS.
- **Feel check** (reviewer): in a room with the board expanded, hover a strip tile — it lifts and grows over its neighbors with no rail shift; active-speaker emphasis scales smoothly. In DevTools Performance panel, hovering/speaker changes show no Layout entries from the strip. Watch that the 2.05× hover border/corner radius don't read soft — if they do, reviewer may drop hover scales to 1.4/1.5 (taste call, not executor's).
- **Done when**: no width transition remains on the tile, tests pass, emphasis is transform-only.

## Reviewer addendum (post-execution)

The executor flagged a responsive breakpoint override (≈19350-19357) still written in the old width vocabulary; its intent is "no hover growth at this breakpoint". The reviewer modernized it: `.board-video-tile:hover { transform: none; }` and `.board-video-tile.is-speaker:hover { transform: scale(1.23); }` (inert `width:` declarations dropped). The reduced-motion override at ≈21176 needed no change — it now neutralizes emphasis more completely than before.
