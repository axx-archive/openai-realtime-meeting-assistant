# 001 — Cut tool-switch and message entrances to token speed

- **Status**: DONE
- **Commit**: bd289db
- **Severity**: HIGH
- **Category**: Purpose & frequency / Easing & duration
- **Estimated scope**: 2 files, 5 small edits

## Problem

Three high-frequency entrances overshoot the motion budget. Tool-tab switching is the app's most repeated navigation and carries a 400ms rise; chat messages rise on every arrival.

```css
/* index.html:5559 — current (inside the multi-selector tool-pane rule) */
        animation: bf-tabin 0.4s var(--ease) both;
```

```css
/* index.html:2848-2851 — current */
      .memory-card__body {
        padding: 0 18px 16px;
        animation: bf-tabin 0.3s var(--ease);
      }
```

```css
/* index.html:5277-5280 — current */
      @keyframes assistant-message-in {
        0%   { opacity: 0; transform: translate3d(0, 6px, 0); }
        100% { opacity: 1; transform: translate3d(0, 0, 0); }
      }
```

The third fires via `.assistant-message--entering` (index.html:3076-3078) on **every** assistant message in a busy chat — the anti-jarring purpose is met by opacity alone; the 6px rise is decorative motion on a high-frequency event.

NOTE: line numbers are from commit bd289db and may drift a few lines — always match on the exact code excerpt, never the line number.

## Target

```css
/* tool-pane rule — 220ms, `both` MUST remain (see test intent below) */
        animation: bf-tabin var(--dur-med) var(--ease) both;
```

```css
      .memory-card__body {
        padding: 0 18px 16px;
        animation: bf-tabin var(--dur-med) var(--ease);
      }
```

```css
      @keyframes assistant-message-in {
        0%   { opacity: 0; }
        100% { opacity: 1; }
      }
```

## Repo conventions to follow

- Duration tokens live in the `:root` block at index.html:236-257: `--dur-fast: 120ms`, `--dur-med: 220ms`, `--dur-slow: 360ms`. Use `var(--dur-med)`, never a raw `220ms`.
- Exemplar of the target pattern: index.html:5556 `.bf-view { animation: bf-fadein var(--dur-med) var(--ease); }`

## Steps

1. In `/Users/ajhart/meetingassist/index.html`, find the rule ending
   `#appShell.is-authed[data-tool="room"]:not(.is-in-room) .hearth-presentation {`
   whose body is `animation: bf-tabin 0.4s var(--ease) both;` (≈line 5559).
   Replace that one declaration with:
   `animation: bf-tabin var(--dur-med) var(--ease) both;`
2. In the comment block directly above that rule (≈lines 5540-5549), the text reads
   `without a held final frame the lobby vanishes when the 0.4s rise ends.`
   Update `the 0.4s rise` → `the rise` so the comment stays truthful.
3. Find `.memory-card__body` (≈line 2848) and replace
   `animation: bf-tabin 0.3s var(--ease);` with
   `animation: bf-tabin var(--dur-med) var(--ease);`
4. Find `@keyframes assistant-message-in` (≈line 5277) and make it opacity-only:
   `0%   { opacity: 0; }` and `100% { opacity: 1; }` (delete both `transform: translate3d(...)` values).
5. In `/Users/ajhart/meetingassist/frontend_rooms_test.go` (≈line 731), the test
   `TestIndexRoomsLobbyMountCascadeReleases` asserts the exact string
   `animation: bf-tabin 0.4s var(--ease) both`.
   Update the expected string to `animation: bf-tabin var(--dur-med) var(--ease) both`.
   Do NOT weaken the assertion — the test's intent (its comment, lines 714-722) is that the
   pane rule holds its final frame (`both`) so the lobby never blanks; the duration is incidental.

## Boundaries

- Do NOT touch the `bf-tabin` keyframe definition itself (≈line 5555) — 10px rise is fine at 220ms.
- Do NOT touch the mount cascade (`mount-rise`, ≈19371-19387) or room-entry choreography (`rise-in`, ≈19389-19400) — deliberate cinematic one-shots, documented settled decisions.
- Do NOT touch `.bf-view` / `bf-fadein`.
- Do NOT change any other duration in this pass.
- If a step doesn't match the code you find, STOP and report instead of improvising.

## Verification

- **Mechanical**:
  - `grep -c 'bf-tabin 0.4s' index.html frontend_rooms_test.go` → both 0.
  - `cd /Users/ajhart/meetingassist && go test -count=1 -run 'TestIndexRoomsLobbyMountCascadeReleases' .` → PASS.
- **Feel check** (reviewer): switch tools rapidly (office→chat→artifacts…) — panes settle in ~220ms with no restart stutter; open a memory card; send a chat message and confirm it fades in without rising.
- **Done when**: greps are 0, the named test passes, and no other `0.4s`/`0.3s` line changed.
