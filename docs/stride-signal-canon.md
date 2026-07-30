# The Stride Signal — identity canon

**Status:** Canon. Ratified by AJ, 2026-07-29.
**Supersedes:** the sliced-disc mark, and the bar-waveform centrepiece on every surface.
**Code of record:** `scripts/stride-signal-geometry.mjs` — the mark is defined there and nowhere else.
**Guard:** `npm run test:brand`. **Regenerate artwork:** `npm run brand:regen`.

This document exists because the identity had drifted into three hand-drawn
approximations of itself and two different oranges. Everything below is either
derived from the code of record or was decided by the founder on a dated
decision. Where it contradicts an older document, this one wins, and §7 says
exactly which older claims are dead.

---

## 1. The mark

An **aperture**: one closed, symmetric, horizontal lens. A slot that opens
because something is listening.

There is exactly one curve in the identity:

```
t(p) = T · sin(pπ)^0.85        p ∈ [0,1] across the width
```

`t` is the half-height at each point; `T` is the half-height at the centre. The
exponent is the entire character of the form. At `1.0` it is a plain ellipse; below
about `0.7` the tips go needle-sharp and brittle. **0.85** keeps a full belly and
draws the ends to a clean point, which is what lets the mark scale down to a
hairline without its tips turning into blunt stubs. The curve is closed *exactly*
at `p = 0` and `p = 1` — `lensHalfHeight` special-cases the endpoints, because
`Math.sin(Math.PI)` is `1.22e-16` and that residue would leave the mark
infinitesimally open, making "closed at the tips" a claim the geometry did not
actually honour.

The outline is **sampled, not bézier-approximated**. Every surface — SVG, Canvas,
React Native — reproduces the same curve and ratios. Committed artwork uses 160
samples for clean rasterisation; live instruments use 64 to keep per-frame path
updates light. Those are intentional fidelity tiers of one formula, not parallel
hand-drawn marks.

### Why this and not the alternatives

Four other candidates were built and driven side by side against the same
synthetic utterance before this was chosen: The Thread (a line that keeps a record
of what you said), The Pair (two equal threads, an equals sign at rest), The Caret
(a text cursor with mass), and The Gait (two masses trading places). The bench that
produced them is preserved in the session record. The Thread and The Pair remain the
strongest unbuilt ideas in the drawer, and The Pair in particular encodes the
company thesis — two voices, same size — better than the Aperture does. They were
not rejected on quality. The Aperture won because it is **one closed form whose
openness is a variable**, which is what turns a picture into a system.

---

## 2. Openness is the system

The mark is not one fixed shape. It is one shape at a stated **openness**, and
openness carries meaning everywhere it appears — the logo, the talk control, a
section rule, a progress bar, a live badge, a focus underscore. A divider is the
mark closed. A microphone that can hear you is the mark open.

Openness is an **aspect ratio**, width : full height. Bigger is tighter.

| | Ratio | Where |
|---|---|---|
| `RATIO_IDLE` | **25:1** | The logo. Also the instrument at rest. |
| `CUTS.logo` | 25:1 | Lockups, large display. Never inside a tile. |
| `CUTS.icon` | 12:1 | App tiles and anything ≥ 40px. |
| `CUTS.micro` | 8:1 | Below 40px and small in-product apertures. |
| `RATIO_OPEN` | **8:1** | Fully open — loudest voice, smallest icon. |

### 8:1 is a hard floor on the whole identity

A lens is eye-adjacent, and the wider it opens the more it reads as an **eye**
rather than a mouth or a slot. On a product whose entire job is remembering what
people said, that connotation is a liability, not a flourish.

So 8:1 binds **static artwork as well as animation**. This is the rule that shaped
the small-size solution: below 40px the mark does *not* open further to stay
legible — it gets **wider**, spanning up to 88% of the tile instead of 66%. Same
ratio, longer mark. `npm run test:brand` enforces the floor against every cut and
every amplitude, including out-of-range input.

### Interpolate the peak, never the ratio

The ratio is a reciprocal of the opening. Interpolating it directly makes the
aperture rush at one end of the range and crawl at the other, which reads as a
broken control. `ratioForAmplitude` interpolates the **peak half-height** so equal
steps of amplitude give equal steps of height. Pinned by test.

---

## 3. Colour

**One orange.** `STRIDE_ORANGE = #FF5A19` is simultaneously the mark, the app
icon, and `--ember-500` — the product's ignition accent. Before this, the icon was
`#FF5A19` and the UI accent was a pinker coral `#FF6B4A`, so the logo and every
button in the product were two different oranges sitting next to each other.

The ramp is that hue at 100% saturation, stepped on lightness:

| Token | Value |
|---|---|
| `--ember-300` | `#FFA07A` |
| `--ember-400` | `#FF7F4D` |
| `--ember-500` | `#FF5A19` ← Stride Orange |
| `--ember-600` | `#E64100` |
| `--ember-soft` | `rgba(255, 90, 25, 0.12)` light · `0.16` dark |

`STRIDE_INK = #050505` is the mark's ground, and the mark's own colour when it
sits on orange.

**Contrast, re-derived after the hue change.** Ember correctly *fails* as
light-theme text (2.87:1) — that is why `emberText #B83A18` exists, clearing AA at
5.27:1 on `bgApp`. The tight one is **4.60:1 on an emberSoft chip**: a tenth of a
point of headroom, so darkening emberSoft or lightening `#B83A18` breaks AA.
`mobile/src/__tests__/contrast.test.ts` holds that line. Dark mode keeps
`ember[500]` at 6.37:1 on `bgApp` and 5.68:1 on a soft chip.

---

## 4. Lockups

**B — the mark as the baseline. Primary.** "Stride" with the lens swelling
underneath it. The mark becomes the company's own rule. It cannot be misread as
punctuation, and it echoes the hairline already above the site tagline.

**C — stacked, mark above. Secondary.** For the splash, the app, the hero.

**A — mark leading. Retired, do not use.** At a glance the sliver reads as an
em-dash in front of the word. Worst on dark, where the mark and the type are both
orange.

Wordmark is **Google Sans Flex**, 600, tracking −0.03em. Mono is **Geist Mono**.

---

## 5. Icons

**The inverted tile is primary**: a full-bleed Stride Orange field with the
aperture cut out of it in ink. It is louder than the dark tile and unmistakable on
a crowded home screen, which is what an icon has to win at.

**The dark tile is the sanctioned alternate**, and it does real work rather than
sitting in a folder: it ships as the **iOS dark-appearance icon**. It is therefore
no longer byte-identical to the master, which the brand-asset test now asserts
explicitly so the divergence cannot be mistaken for a regression.

- **Maskable / circular crops** use `safeFit`. Useful discovery: at the standard
  0.66 inset the lens *already* fits every safe circle, because a lens is nearly
  all width and 0.33 of the tile sits inside a 0.38 radius. `safeFit` is a no-op
  there and only bites at wider insets — the test asserts the property rather than
  demanding a shrink that is not needed.
- **iOS tinted** is a *luminosity map*, so it is white-on-ink at the micro cut. Its
  light area is **3.6%** of the tile — a few percent, not the double-digit share a
  filled disc gave — so the test floor moved from 8% to 2%. A blank tile is still
  0% and still fails, which is the regression that guard exists for.
- **Below 24px the aperture is faint and that is accepted.** At favicon size people
  recognise apps by colour and silhouette, not detail. No second simplified glyph
  enters the system; a system with an exception is a system people stop trusting.

---

## 6. The instrument

The talk control **is** the logo. Not a meter wearing the brand colour.

1. **Rest is the logo, exactly.** Amplitude 0 renders `RATIO_IDLE` to the number,
   and the ripple is exactly 1. Pressing the control never swaps one picture for
   another — it just opens.
2. **Grey at rest, orange only while listening.** Ember stays earned: colour means
   something is happening. The cost, accepted knowingly, is that the resting home
   screen carries almost no brand presence.
3. **Amplitude, never a keyframe loop.** Movement means audio is flowing. A mark
   that idles on a timer cannot answer "can you hear me?", which is the only
   question a voice interface is ever asked.
4. **The side-to-side element is the ripple** travelling along the open aperture —
   depth 0.18, ~6.2 cycles across the width. It is a *detail on top of* the lens,
   never a competing wave: an earlier pass let it reach 0.42 and the slot stopped
   reading as a lens and became an amoeba.
5. **Reduced Motion** drops the gait and keeps the amplitude answer as a static
   opening. Dropping the response too would leave a Reduce Motion user holding a
   control that cannot answer the only question it exists to answer.

### Small chrome

Small in-product placements use the **bare aperture**, never an app-tile box.
The Scout label stays quiet and the bottom Talk to Scout control repeats the same
shape as the primary voice surface. The tile belongs to the launcher only.

---

## 7. Reconciliation — what is dead, what is kept

### Dead. Do not follow these.

| Source | Retired claim | Replaced by |
|---|---|---|
| `docs/reskin-standing-requirements.md` §1 | `--ember #FF6B4A` coral | §3 — `#FF5A19` |
| `docs/reskin-standing-requirements.md` §1 | "brand mark" listed under monochrome `--accent` | §3 — the Stride Signal is orange; it is the one earned exception, not monochrome chrome |
| `docs/plans/voice-first-mobile-design.md` §3 | 28 **bars**, `RESTING_PROFILE`'s fourteen varied heights, bars `scaleY` | §1, §6 — one aperture that opens |
| `docs/plans/voice-first-mobile-design.md` | "the transcript materializes out of the bars" | Unbuilt, and now unspecified. Re-decide against the aperture before building it. |
| Session work, 2026-07-29 (uncommitted) | the sliced-disc mark, its 16-band table, `--stride-reach`, `@keyframes stride-signal-gait` | §1 — rejected by the founder before it shipped |

`docs/reskin-inventory.md` refers to an even older ember, `--agent:#FF7A2B`. It is a
dated audit of a state that no longer exists; no action, and it should be read as
history.

`stride-site/BRAND.md` documents **That's Cool**, the maker brand, not Stride. Its
"maker signs the work, does not overpower the product identity" rule is intact and
unaffected.

### Kept. These laws survived the redesign and still bind.

- **Breathe only while listening** (`voice-first-mobile-design.md`). Rest is static.
  The aperture honours this more literally than the bars did.
- **Amplitude drives the instrument; never a keyframe loop** (same source). This is
  now §6.3 and is the reason two `AnalyserNode` taps exist on desktop.
- **Transforms and opacity only; never animate layout** (`animation-wave-2`). The
  aperture animates its own path geometry inside a canvas/SVG, so it never
  triggers layout.
- **Ember is earned, never ambient** (`visual-upgrade-thesis.md`, founder-ratified).
  Directly responsible for §6.2.
- **`emberText` for text and glyphs; fills stay `ember`** (The Table wave D). The
  contrast floors in §3 are the re-derived version of that work, not a replacement.
- **Google Sans Flex + Geist Mono; system text lowercase mono**
  (`reskin-standing-requirements.md` §1). Unchanged.
- **The compact composer/Dock level meter stays a linear bar meter**
  (`mobile/src/components/Waveform.tsx`, `theme/waveformGeometry.ts`). It is a
  *meter*, not the mark, and one hero mark is the point. Do not convert it to an
  aperture; do not delete it.

---

## 8. Current implementation state

- Desktop and native are migrated to the aperture. Root and mobile tests pin
  their printed constants and the desktop idle path to the code of record.
- Marketing is migrated in its separately deployed repository and pins the same
  ratios, exponent, colour, and interaction floor in its own rendered-contract
  suite. It demonstrates the motion with a synthetic phrase; product surfaces
  replace that phrase with real audio amplitude.
- **The native centrepiece has never been seen on a device.** The Simulator MCP
  needs `sudo xcode-select`, outstanding across three waves now.
- The Thread and The Pair are in the drawer, undamaged. If the Aperture's quietness
  at rest becomes a problem in real use, The Pair is the first thing to revisit.
