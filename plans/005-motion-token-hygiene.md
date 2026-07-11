# 005 — Motion token hygiene: fold stray durations/easings into tokens

- **Status**: DONE
- **Commit**: bd289db
- **Severity**: MEDIUM
- **Category**: Cohesion & tokens / Physicality
- **Estimated scope**: 1 file, ~25 one-line edits

## Problem

The token system (`--dur-fast: 120ms`, `--dur-med: 220ms`, `--dur-slow: 360ms`, `--ease`) is well-established, but ~25 transition declarations hand-type near-token values, silently forking the scale. One chip uses the weak CSS built-in `ease`. And the notification panel scales in from center although it is anchored to the bell on the left tool rail.

Representative current code (all in index.html, commit bd289db — match excerpts, not line numbers):

```css
/* ≈1941-1943 (login-signin; same shape at ≈2046-2048 login-passkey) */
        transition:
          transform .12s var(--ease),
          background .12s var(--ease),
          opacity .12s var(--ease);
```

```css
/* ≈8056 — the ONLY weak built-in `ease` on a hover surface */
        transition: border-color 120ms ease, color 120ms ease, background 120ms ease;
```

```css
/* ≈14395 (and 8 more `0.15s` sites: ≈16881, 16900, 16924, 16950, 17016, 17060, 17271, 17436) */
        transition: transform 0.15s var(--ease);
```

```css
/* ≈10228-10231 — notification panel: trigger-anchored but center-origin */
        box-shadow: var(--glass-highlight), var(--glass-shadow);
        animation: bf-islandin var(--dur-med) var(--ease);
```

## Target

Apply this mapping to **transition durations only** (never keyframe/`animation` durations), only where the easing is `var(--ease)` or the bare `ease` keyword:

| Hand-typed | Becomes |
|---|---|
| `.12s` / `0.12s` / `120ms` | `var(--dur-fast)` |
| `.15s` / `0.15s` / `150ms` | `var(--dur-fast)` |
| `.2s` / `0.2s` / `200ms` / `0.25s` | `var(--dur-med)` |
| `.3s` / `0.3s` / `0.4s` | `var(--dur-slow)` |
| bare `ease` keyword (≈8056 only) | `var(--ease)` |

Known sites: ≈364 (`background-color 0.4s`, `color 0.4s` on the root), 1941-1943, 2046-2048, 2362-2363 (`transform .2s`, `box-shadow .2s`), 2778 (`box-shadow 0.2s`), 2841 (`transform 0.25s`), 5749 (`opacity 0.3s`), 8056, 14308, 14395, 16278, 16881, 16900, 16924, 16950, 17016, 17060, 17271, 17436, 18939 (`background 0.3s`), plus ≈17107 `animation: bf-sheetin 220ms var(--ease);` → `animation: bf-sheetin var(--dur-med) var(--ease);` (the one sanctioned animation-shorthand edit — 220ms is exactly the token).

Notification panel: add one declaration after its `animation:` line:

```css
        animation: bf-islandin var(--dur-med) var(--ease);
        transform-origin: left center;
```

## Repo conventions to follow

- Token block: index.html:236-257. Do not add new tokens; snap to existing ones.
- Exemplar of correct usage: `transition: transform var(--dur-fast) var(--ease);` (≈390).

## Steps

1. Discovery: `grep -nE 'transition[^;]*[^-](0?\.[0-9]+s|1[25]0ms) ' index.html` (and `grep -n '0.4s var(--ease)\|0.3s var(--ease)\|0.25s var(--ease)\|0.2s var(--ease)\|.12s var(--ease)\|0.15s var(--ease)\|120ms ease\|220ms var(--ease)' index.html`) to enumerate every candidate line.
2. **Pin check before every edit**: for each exact line you intend to change, `grep -F "<the exact substring>" *_test.go` — if ANY test pins it, SKIP that site and list it in your report instead.
3. Apply the mapping site by site with exact string replacement. Multi-property lines keep their layout; only the duration token changes (and `ease` → `var(--ease)` at ≈8056).
4. Add `transform-origin: left center;` to the `.notification-panel` rule (≈10222-10231), directly after its `animation:` line.
5. EXCLUSIONS — do not touch: `transition: width 80ms linear` progress/meter bars (correct linear, and width conversion is a separate deferred plan); `bf-wave 430ms` waveform; any `animation:` shorthand except the sanctioned ≈17107 sheet line; anything inside `@keyframes`; the two `transition: background-color 0.4s var(--ease), color 0.4s var(--ease)` root line IS in scope (→ `var(--dur-slow)`).

## Boundaries

- Do NOT change what property is transitioned anywhere — durations/easings only, plus the single `transform-origin` addition.
- Do NOT touch `--dur-breathe`, `--pulse-cycle`, or any keyframe timing.
- Do NOT edit any string a `*_test.go` file pins (step 2 check is mandatory).
- If a step doesn't match the code you find, STOP and report instead of improvising.

## Verification

- **Mechanical**:
  - `grep -cE 'transition[^;]*(\.12s|0\.15s|0\.2s|0\.25s|0\.3s|0\.4s) var\(--ease\)' index.html` → 0 (excluding any pin-skipped sites you reported).
  - `grep -c '120ms ease,' index.html` → 0.
  - `cd /Users/ajhart/meetingassist && go test -count=1 -run 'TestIndex' .` → PASS.
- **Feel check** (reviewer): hover login buttons, lobby join, topbar back, "live now" rows — feedback feels identical or crisper (120-220ms); open the notification bell — the panel now grows from the rail edge, not from its center.
- **Done when**: greps clean, tests pass, skipped-pin list (if any) reported.
