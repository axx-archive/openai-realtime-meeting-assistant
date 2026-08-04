# Stride identity and Signal Cradle canon

**Status:** Canon. Ratified by AJ, 2026-07-30; §1, §1a and §4 revised 2026-07-31.  
**Code of record (tile):** `scripts/stride-strike-geometry.mjs`  
**Code of record (wordmark):** `brand/stride-wordmark-source.svg`  
**Printed tile source:** `brand/stride-strike-source.svg`  
**Source SHA-256:** `1e861145f455804b608d7c663ff4e9a892957be9f27094dd69f64b5cbdee2423`  
**Guard:** `npm run test:brand`  
**Regenerate:** `npm run brand:regen`

Stride has three deliberately separate visual jobs:

1. **The Strike** is the static tile: app icon, favicon, lockup mark, login
   mark, social card.
2. **The Wordmark** is the name, set as artwork rather than type — everywhere
   the product says "Stride" as a brand rather than as a sentence.
3. The **Signal Cradle** is the live voice instrument: the large control that
   shows whether audio energy is entering or leaving the conversation.

The native loading screen is the cradle at rest. It occupies the Canvas
cradle's exact composition and cross-fades into the live control after
bootstrap. The instrument is not otherwise used as a static logo.

## 1. Static identity — The Strike

The Strike is one caught frame of the cradle rather than a drawing of the whole
apparatus: one energised Stride Orange mass falling in from the left, and the
equal neutral-mass row exiting the right.

- Every mass has radius `0.2 × tile width`.
- **The striking mass is bisected by the left frame** (`cx = 0`). Ratified by
  AJ 2026-07-31, replacing the earlier `0.3r`-outside crop, which left only 14%
  of the tile orange — about five pixels at icon size, where the lift below
  could not be seen at all.
- **The striking mass rides `0.08 × tile` above the row's axis** — exactly two
  fifths of a mass radius. A mass level with the row it is about to hit has
  already arrived; the lift is what gives the tile a before and an after. It
  stays under a full radius on purpose: at `1.0r` the mass clears the row and
  the tile reads as two unrelated objects.
- The receiving masses are centred at `0.6w` and `1.0w`, so the row runs off the
  right edge, and they are **exactly tangent** — a cradle row is in contact.
- The receiving masses must be drawn as **separate layers**. Two tangent circles
  in one shape give a renderer a single fused outline to rim, which pinches the
  contact into a metallic web; steel does not bleed into steel.
- No strings, frame, glow, trail, or type appear in the tile.
- The crop is the meaning: a moment caught, not an apparatus described.

### Material

The masses carry **satin** depth: one broad, low-contrast highlight from the
upper left, no specular dot, and a bounce on the shadowed limb. The ramp lives
in OKLab so it applies equally to any base colour — shadows gain chroma and
highlights lose a little, which is what pigment does and what the approved comp
measures.

**Where the platform supplies its own material, we do not draw ours.** On the
Icon Composer path the layers ship flat and iOS composes the depth; the satin
build is for every surface that will never get that — the web, Android, the
favicon, the Xcode catalog.

This material belongs to the Strike only. The live Signal Cradle stays flat and
its tests forbid gradients.

### Colorways

The Strike has an **appearance set**, not a colorway list:

| Appearance | Field | Active mass | Receiving row |
|---|---|---|---|
| light (iOS Default) | Warm Putty `#CFC5B7` | Stride Orange `#FF5A19` | `#54545C` |
| dark | `#121212` → Stride Ink `#050505` | Stride Orange `#FF5A19` | `#77777D` |
| tinted | black | white | mid grey `#8A8A8A` |

The tinted cut is a luminance map the system re-tints, so the only thing it has
to preserve is the story: the striking mass stays the brightest thing in the
tile, so custody of the energy still reads after all colour is gone. Painting
every mass white leaves three identical dots and throws that away.

**On the web, one tile in both themes, and it is the dark one.** The light
appearance exists because Apple asks for it on the home screen, not as a general
theming rule. A putty tile at 16px on light browser chrome dissolves.

Every raster derivative is generated deterministically from the code of record.

## 1a. The Wordmark

The name is artwork, not type. A wordmark set in a font is whatever the font
fallback decides it is on a machine that does not have the font.

- **Source:** `brand/stride-wordmark-source.svg` — a sampled outline traced from
  the approved 1430px master at a 0.2px simplification tolerance (0.12% edge
  deviation). Sampled rather than bézier-fitted, for the same reason the lens
  geometry is: the sampled curve IS the approved shape, and a fit would be a
  second wordmark.
- **Eight closed rings.** Six letters, three counters. A trace that loses one has
  dropped a counter and the mark is wrong in a way nobody notices until it is on
  a wall.
- **The wordmark is NEVER orange.** Ratified by AJ 2026-07-31:

  > "the only orange thing is the ball in motion"

  This is the whole identity in one line. Orange is **custody of energy** — the
  mass that is moving. The neutral row is everything at rest: the name, the
  shared context, the company. A wordmark painted orange claims to be in motion,
  and it is the one thing on screen that never is. So the name takes the row's
  graphite and orange stays earned in the logotype exactly as it is everywhere
  else in the product.
- The two cuts come from the **same graphite family the icon's receiving rows
  use**, but are chosen by the **ground the mark sits on** — not copied from
  whichever tile is beside it. A tile's row is picked for that tile's own field,
  so the two do not always coincide (the desktop sign-in shows the dark tile on
  putty). Ground decides legibility, so ground decides this:

  | Ground | Wordmark | Contrast |
  |---|---|---|
  | light / putty | `#54545C` | 4.4:1 |
  | dark / ink | `#77777D` | 4.9:1 |

  While Scout is actively listening (or has just heard its name), the shell's
  wordmark may shift from the ground's graphite to the ground's
  **full-contrast foreground** — ink on the light ground, near-white on the
  dark one. Presence is expressed as *value*, never as hue; no state ever
  re-colors the name beyond this shift. (2026-08-03; supersedes an earlier
  green `--live` cue that violated the rule above.)

  The orange it replaced measured **1.83:1** on putty, so this is more legible
  as well as more correct. An earlier pass shipped it orange and then black;
  both are superseded.
- Where a page has light and dark *sections* rather than a theme — the marketing
  site — each placement takes the cut that matches its own ground. The hero and
  footer ride `--black` and take `#77777D`; the nav rides paper and takes
  `#54545C`.
- One asset, every surface: desktop and marketing paint it through a CSS mask so
  colour is `color`; native draws the printed outline in
  `mobile/src/theme/strideWordmark.ts`. Nothing carries a second copy.
- **Ink, putty and white cuts are generated but unused in product.** They exist
  for photographs, partner slides, and merchandise.

### The lockup

Where the tile and the name appear together they are ONE lockup: **the tile sits
beside the wordmark**, never stacked above it. Stacked, they read as two
separate marks that happen to be near each other. This holds on the desktop
sign-in, the native login, and the marketing nav and footer. The gate's column
order is unchanged — lockup → invite line → glass panel.

## 2. Signal Cradle

The voice control is an abstract Newton's cradle: two edge masses, three fixed
centre masses, no suspension lines, no literal frame, and no decorative
perpetual-motion loop.

- At rest, all five masses touch and remain still.
- An open but silent microphone adds a quiet field glow, not fake momentum.
  Motion begins on measured voice energy and is allowed to decay naturally
  across the following collisions.
- While listening, the left mass falls under the nonlinear pendulum equation,
  stops at contact, and transfers its velocity through three equal, still centre
  masses to the far right. The right mass rises, returns under gravity, stops at
  contact, and sends the far left back out.
- Restitution and air damping remove a small amount of energy on every cycle.
  Live microphone amplitude acts as an external force only at impact, setting
  the target energy without falsifying the free swing between collisions.
- Hertzian sphere contact is much shorter than a UI frame and remains
  instantaneous in the motion model. A separate 260 ms perceptual trace makes
  that otherwise invisible transfer legible at 30–60 fps without changing the
  pendulum physics; the centre masses do not fake a travelling displacement.
- Impact color is a rapid sequence of localized flashes across the five masses.
  The masses themselves reveal the transfer; no separate particle or carrier
  crosses the row. Over the final half of the trace, the receiving edge earns
  the impulse and rises under the physical pendulum model.
- The contact tint and the moving edge both use exact
  Stride Orange. Do not introduce a peach or white impact core, multi-color
  sparks, gradients, or accumulating trails; intensity comes from opacity and
  spatial focus rather than hue changes.
- Attack is fast and release is slower so consonants register without flicker.
- The 0.52 m virtual pendulum length deliberately slows the exchange by roughly
  eleven percent from the initial cradle study. It should feel calm and
  hypnotic without delaying the first response to live audio.
- Reduced Motion removes the pendulum travel but keeps amplitude as a static
  glow. The control must still answer “can you hear me?”
- Press feedback is `scale(0.96)` and remains interruptible.

### Conversation interpretation

This is not a literal desk toy. It is a picture of conversational momentum:

- The **left edge is the human**. Their voice lifts and releases that mass.
- The **three fixed centres are shared context**: the words, company memory,
  constraints, and work already in the conversation. They transmit the impulse
  without being displaced by every new turn.
- The **right edge is the active agent**. It receives the human impulse and
  swings out; when the agent speaks, the system seeds from the right and returns
  momentum toward the human.
- Only the active edge and the short contact wave carry Stride Orange. The
  inactive row stays neutral, making the direction and custody of energy clear.

Amplitude controls release energy. Cadence controls when new energy enters.
Role controls which edge originates it. Color shows custody, not decoration.
The result should read first as a futuristic conversational waveform and only
then reveal the Newton's-cradle idea underneath.

Direction must come from the audio source that is actually producing the
measured level. Desktop may seed the right edge only from the active agent's
real output analyser; it must never relabel microphone energy as agent speech.
The native home currently meters only the human microphone, so it truthfully
uses the left edge. Native right-to-left motion remains dormant until a real
agent-output meter is connected—never synthesize a reply merely for symmetry.
Changing roles during an active flight must not teleport the energy to the
other edge; the current collision resolves before the newly measured source can
seed a settled cradle.

The cradle should feel like information moving through a system, not like a
skeuomorphic executive toy. The masses and the travelling energy are the entire
visual; no strings, stand, or frame are drawn.

## 3. Surface map

| Surface | Tile | Wordmark | Live instrument |
|---|---|---|---|
| iOS app icon | `Stride.icon` (all 6 renditions) | — | — |
| Android launcher | The Strike, flat cut | — | — |
| Expo native loading screen | — | — | Signal Cradle at rest |
| Native login | The Strike | orange, 22pt | — |
| Native home | — | — | Signal Cradle |
| Desktop sign-in | The Strike | orange, `0.82em` | — |
| Desktop tool rail chip | — | orange, 11px | — |
| Desktop sheet eyebrow | — | orange, `0.92em` | — |
| Desktop favicon | The Strike (dark) | — | — |
| Desktop voice home | — | — | Signal Cradle |
| Marketing hero / nav / footer | The Strike (nav, footer) | orange | — |
| Marketing favicon / social card | The Strike (dark) | orange (card) | — |
| Native Apple companion icons | The Strike | — | — |

## 3a. The iOS bundle

iOS reads `mobile/assets/Stride.icon`, an Icon Composer bundle, not the PNG
appearance set. Ratified by AJ 2026-07-31. The PNGs are still generated for
Android, the web manifest and the Xcode catalog, and remain the fallback.

The bundle's schema is undocumented. It was read off `IconComposerFoundation`'s
symbols and then **proved** with `ictool` (ships with Xcode 26) by setting each
value to magenta and checking whether it appeared. What the renderer actually
honours, because half of it is silently ignored:

- `fill` — honoured, **but only in the Default rendition**. Dark replaces the
  background with the system's own ground. This is why Apple's guidance is to
  drop the dark background rather than paint one.
- **every `*-specializations` key — IGNORED**, at icon, group and layer level.
  Writing one reads like a per-appearance override exists when it does not.
- layer `fill` — honoured; it **replaces** the asset's colour rather than
  tinting it, so layer art ships flat white.
- `translucency` — honoured, and load-bearing. Left unset the masses blend
  toward the ground (3.47:1 on putty collapsing to 1.50:1 on dark). Disabled,
  they hold their colour. They are steel, not glass: momentum is conserved
  through solid bodies.
- `specular`, `shadow` — honoured. This is where the depth comes from.

The consequence is that **there is no per-appearance colour**. One set of layer
colours holds against putty in Default and Apple's dark ground in Dark, which is
why the row is `#72727A` — the only value in the graphite family clearing 2.7:1
on both.

Verify with:

```bash
"/Applications/Xcode.app/Contents/Applications/Icon Composer.app/Contents/Executables/ictool" mobile/assets/Stride.icon --export-image --output-file /tmp/icon.png --platform iOS --rendition Default --width 1024 --height 1024 --scale 1
```

Renditions: `Default`, `Dark`, `TintedLight`, `TintedDark`, `ClearLight`,
`ClearDark`. Look at all six before calling an icon change done.

## 4. Light mode is grounded on putty

Ratified by AJ 2026-07-31. Light mode uses the putty family the icon's light
appearance is built on, so the tile on the home screen and the app behind it are
one material.

**Desktop softening, 2026-08-03 (AJ: "the putty seems a bit too intense as the
light mode").** A phone shows the ground in slivers between cards; a desktop
shows it as a wall, and the wall read heavy. The desktop ramp slides up ONE
step: the field is putty softened to `#DDD4C6` (`STRIDE_PUTTY_SOFT` in
`scripts/stride-strike-geometry.mjs`), and ratified putty `#CFC5B7` stays in
the desktop system as the well. Brand assets (Strike tile, icons, splash) and
native keep `#CFC5B7` as their field — the logo did not move, the room got
better lighting.

The ramp keeps the old relationship exactly — **panels lift OFF the ground,
wells sink UNDER it** — which is why no screen had to be re-thought, only
re-measured. The token family is still called `paper`: the role did not change,
only the stock.

| Token | Desktop | Native | Role |
|---|---|---|---|
| `--paper-0` | `#F2EDE4` | `#EDE8DF` | panels, cards |
| `--paper-50` | `#DDD4C6` | `#CFC5B7` | the ground |
| `--paper-100` | `#CFC5B7` | `#C2B7A7` | wells — **the worst case for text** |
| `--paper-200` | `#C2B7A7` | `#B2A695` | deepest well |

Desktop ladder re-solved 2026-08-03 (all AA): text-1 10.7 / text-2 7.7 /
text-3 5.5 on the ground; text-1 9.2 / text-2 6.8 / text-3 5.0 on the well;
ember-text 6.1 ground / 5.3 well / 4.5 deep well; wordmark 5.1; focus ring 3.4
on the well. The negative pins hold: raw ember 2.1 and `#B83A18` 3.9 both stay
below AA on the softened ground.

**The ink is a warm dark grey, not black.** `#26231E`. Near-black on warm putty
reads as a printing error — the two sit on opposite sides of neutral and the eye
sees the mismatch before it sees the words.

The ground's luminance fell from 0.90 to 0.57, so **every alpha was re-solved
from scratch**; nothing was carried over. On `--paper-100`: text-1 7.9:1 ·
text-2 6.1:1 · text-3 4.6:1 · focus ring 3.2:1.

Two things moved that are easy to miss:

- **The ground is no longer the worst case.** On white paper the background and
  the deepest well were close enough that measuring against the background was
  near enough. On putty the well is a full step darker and text-3 clears the
  floor there by nine hundredths. Measure against the WELL.
- **Ember text had to move again.** `#B83A18` — itself already a fix for the
  brand orange failing on white — fell to 3.37:1 on putty. `--ember-text` /
  `emberText` is now `#86290F` (5.27:1 on the ground, 4.55:1 on the well).
  Fills keep `--ember`; so does the wordmark (§1a).

Orange enters both themes ambiently at 3% in `--backdrop-ambient` and nowhere
else. Earned stays earned.

The marketing site keeps its own warmer paper (`#f4f0e8`) and was deliberately
left alone — it was already in this family.

## 5. Release rules

- Run `npm run brand:regen`; never hand-edit a derived PNG, and never hand-edit
  `brand/stride-strike-*.svg` — they are prints of the code of record.
- Run root brand tests, mobile tests/typecheck, marketing build/render tests,
  and native icon generation before calling the rollout complete.
- Inspect the actual 1024px icon and at least one home-screen-size render, and
  **all six `ictool` renditions** (§3a).
- Re-measure contrast after any palette move; `mobile/src/__tests__/contrast.test.ts`
  pins the VALUES, not just their usage, and it is the only thing that caught
  the last two ember failures.
- A successful local build does not authorize TestFlight, device installation,
  Git shipping, or deployment.
