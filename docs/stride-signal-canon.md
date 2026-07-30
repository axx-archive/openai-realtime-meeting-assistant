# Stride identity and Signal Cradle canon

**Status:** Canon. Ratified by AJ, 2026-07-30.
**Static source of truth:** `brand/stride-strike-source.svg`
**Source SHA-256:** `6a9ea0e4858dd5d6e15842766b646aa807ee6da9bf6c9d87eb0111820e621475`
**Guard:** `npm run test:brand`
**Regenerate:** `npm run brand:regen`

Stride has two deliberately separate visual jobs:

1. **The Strike** is the static identity: app icon, favicon, lockup, login mark,
   social card, and every other place a logo appears.
2. The **Signal Cradle** is the live voice instrument: the large control that
   shows whether audio energy is entering or leaving the conversation.

The native loading screen is the cradle at rest. It occupies the Canvas
cradle's exact composition and cross-fades into the live control after
bootstrap. The instrument is not otherwise used as a static logo.

## 1. Static identity — The Strike

The Strike is one caught frame of the cradle rather than a drawing of the whole
apparatus. It uses a Stride Ink field, one energised Stride Orange mass entering
from the left, and the equal neutral-mass row exiting the right.

- Every mass has radius `0.2 × tile width`.
- The active mass is centred `0.3r` outside the left frame.
- The visible receiving masses are centred at `0.6w` and `1.0w`, so the row
  deliberately runs off the right edge.
- No strings, frame, glow, gradient, trail, or type appear in the tile.
- The crop is the meaning: a moment caught, not an apparatus described.

### Colorways

- **Field:** Stride Ink `#050505`.
- **Active mass:** Stride Orange `#FF5A19`.
- **Receiving masses:** Signal Graphite `#5E5E66`.
- **iOS tinted / Android monochrome:** white Strike geometry on black.

Every raster derivative is generated deterministically from the SVG source.

## 2. Signal Cradle

The voice control is an abstract Newton's cradle: two edge masses, four fixed
centre masses, no suspension lines, no literal frame, and no decorative
perpetual-motion loop.

- At rest, all six masses touch and remain still.
- An open but silent microphone adds a quiet field glow, not fake momentum.
  Motion begins on measured voice energy and is allowed to decay naturally
  across the following collisions.
- While listening, the left mass falls under the nonlinear pendulum equation,
  stops at contact, and transfers its velocity through four equal, still centre
  masses to the far right. The right mass rises, returns under gravity, stops at
  contact, and sends the far left back out.
- Restitution and air damping remove a small amount of energy on every cycle.
  Live microphone amplitude acts as an external force only at impact, setting
  the target energy without falsifying the free swing between collisions.
- Hertzian sphere contact is much shorter than a UI frame and remains
  instantaneous in the motion model. A separate 140 ms perceptual trace makes
  that otherwise invisible transfer legible at 30–60 fps without changing the
  pendulum physics; the centre masses do not fake a travelling displacement.
- Impact color is one continuous signal, never a sequence of flashes. A small
  Stride Orange carrier moves linearly through the contact row while the two
  nearest masses receive a low-opacity barycentric tint. Over the final half of
  the trace, the carrier merges into the physically moving receiving edge.
  Luminance never falls into the gap between two masses.
- The carrier, its halo, the contact tint, and the moving edge all use exact
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
- The **four fixed centres are shared context**: the words, company memory,
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

| Surface | Static logo | Live instrument |
|---|---|---|
| Expo app icon | The Strike | — |
| Expo native loading screen | — | Signal Cradle at rest |
| Native login | The Strike | — |
| Native home | — | Signal Cradle |
| Desktop rail / sign-in / favicon | The Strike | — |
| Desktop voice home | — | Signal Cradle |
| Marketing hero / nav / product window / closing / footer | The Strike | — |
| Marketing favicon / social card | The Strike | — |
| Native Apple companion icons | The Strike | — |

## 4. Release rules

- Run `npm run brand:regen`; never hand-edit a derived PNG.
- Run root brand tests, mobile tests/typecheck, marketing build/render tests,
  and native icon generation before calling the rollout complete.
- Inspect the actual 1024px icon and at least one home-screen-size render.
- A successful local build does not authorize TestFlight, device installation,
  Git shipping, or deployment.
