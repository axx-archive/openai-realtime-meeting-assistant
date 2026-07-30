/**
 * THE STRIDE SIGNAL — canonical geometry.
 *
 * One source of truth for the mark, shared by the app icon, the desktop
 * centrepiece, the native centrepiece, and the marketing site. If the mark
 * changes, it changes HERE and every surface is regenerated from it. Three
 * hand-drawn approximations of the same logo is how a brand drifts.
 *
 * ── The mark ──────────────────────────────────────────────────────────────
 *
 * An aperture. One closed, symmetric, horizontal lens — a slot that opens
 * because something is listening.
 *
 * There is exactly one curve in the identity:
 *
 *     t(p) = T · sin(pπ)^0.85          p ∈ [0, 1] along the width
 *
 * ...the half-height at each point across the mark. The exponent is the whole
 * character of it: at 1.0 the form is a plain ellipse, and below about 0.7 the
 * tips grow needle-sharp and brittle. 0.85 keeps a full belly and draws the ends
 * to a clean point, which is what lets the shape survive being scaled to a
 * hairline.
 *
 * ── Openness is the system ────────────────────────────────────────────────
 *
 * The mark is not one fixed shape. It is one shape at a stated OPENNESS, and
 * openness carries meaning everywhere it appears — the logo, the talk control,
 * a section rule, a progress indicator, a live badge, a focus underscore. That
 * is why this is an identity rather than a picture: a divider is the mark
 * closed, and a microphone that can hear you is the mark open.
 *
 * Openness is expressed as an ASPECT RATIO, width : full height. A bigger
 * number is a tighter slot.
 *
 *     RATIO_IDLE = 25   the logo. Also the resting state of the instrument, so
 *                       pressing the control never swaps one picture for another
 *     RATIO_OPEN = 8    fully open, at the loudest voice and the smallest icon
 *
 * ── Why 8 is a hard floor ─────────────────────────────────────────────────
 *
 * A lens is eye-adjacent, and the wider it opens the more it reads as an eye
 * rather than a mouth or a slot. On a product whose entire job is remembering
 * what people said, that connotation is a liability, not a flourish. So 8:1 is a
 * FLOOR ON THE WHOLE IDENTITY — not a soft guideline for the animation. Static
 * cuts obey it too, which is why the small icon sizes stop opening at 8:1 and
 * find their legibility in the tile instead. Ratified by AJ, 2026-07-29.
 */

/** Canonical Stride Orange. The mark, the ember, and the icon are one colour. */
export const STRIDE_ORANGE = '#FF5A19';
/** The mark's ground, and the mark's own colour when it sits on orange. */
export const STRIDE_INK = '#050505';

export const CANVAS = 1024;

/** The exponent that gives the lens its belly and its points. */
export const LENS_EXPONENT = 0.85;

/** The logo, and the instrument at rest. */
export const RATIO_IDLE = 25;
/** Fully open. A hard floor on the entire identity — see the header. */
export const RATIO_OPEN = 8;

/**
 * The cuts that ship as artwork.
 *
 * `logo` is the lockup and large-display cut. The icon needs its own, because a
 * 25:1 sliver inside a 40px tile is a hairline nobody can see — and `micro`
 * exists because even the icon cut disappears in a browser tab. Three cuts, one
 * curve, one rule: none of them opens past RATIO_OPEN.
 */
export const CUTS = {
  logo: RATIO_IDLE,
  /** App tiles and anything at or above 40px. */
  icon: 12,
  /** Favicons, tab icons, notification badges — below 40px. */
  micro: RATIO_OPEN,
};

/** How much of a square tile's width the mark spans. */
export const TILE_INSET = 0.66;

const clamp = (value, min, max) => Math.min(max, Math.max(min, value));

/**
 * Half-height of the mark at a normalised position across it.
 *
 * `peak` is the half-height at the centre. Consumers that animate the mark
 * should interpolate PEAK, not the ratio — the ratio is a reciprocal, so
 * interpolating it directly makes the opening rush at one end of the range and
 * crawl at the other.
 */
export function lensHalfHeight(p, peak) {
  const at = clamp(p, 0, 1);
  // Exactly closed at the tips. Math.sin(Math.PI) is 1.22e-16, not 0, and that
  // residue survives the exponent — so without this the mark's ends are
  // infinitesimally open, and "closed at the tips" becomes a claim the geometry
  // does not actually make. It is a design law, so it holds exactly.
  if (at === 0 || at === 1) return 0;
  return peak * Math.pow(Math.sin(at * Math.PI), LENS_EXPONENT);
}

/** The centre half-height for a given width and aspect ratio. */
export function peakFor(width, ratio) {
  return width / ratio / 2;
}

/**
 * The mark's outline as an SVG path, centred on y = 0.
 *
 * Sampled rather than expressed as béziers on purpose: the sampled curve is the
 * exact shape that was reviewed and approved, and every surface — SVG, Canvas,
 * React Native — can reproduce the same sampling. A bézier approximation would
 * introduce a second, slightly different mark.
 */
export function lensPath(width, ratio, steps = 160) {
  const peak = peakFor(width, ratio);
  const top = [];
  const bottom = [];
  for (let i = 0; i <= steps; i += 1) {
    const p = i / steps;
    const x = (p * width).toFixed(2);
    const t = lensHalfHeight(p, peak);
    top.push(`${i === 0 ? 'M' : 'L'}${x} ${(-t).toFixed(2)}`);
    bottom.push(`L${x} ${t.toFixed(2)}`);
  }
  return `${top.join(' ')} ${bottom.reverse().join(' ')} Z`;
}

/**
 * The openness the instrument should hold at a given amplitude.
 *
 * Returns a ratio. Amplitude 0 is the logo exactly; amplitude 1 is RATIO_OPEN.
 * Interpolation happens on the peak so the opening reads as even.
 */
export function ratioForAmplitude(amplitude, width = CANVAS) {
  const level = clamp(amplitude, 0, 1);
  const idle = peakFor(width, RATIO_IDLE);
  const open = peakFor(width, RATIO_OPEN);
  const peak = idle + (open - idle) * level;
  return width / (peak * 2);
}

/**
 * The travelling ripple that runs along the open aperture.
 *
 * This is the side-to-side element, and it is deliberately a DETAIL on top of
 * the lens rather than a competing wave: an earlier pass let it reach 0.42 and
 * the slot stopped reading as a lens and became an amoeba. `depth` is a
 * fraction of the local half-height.
 */
export const RIPPLE = {
  depth: 0.18,
  /** Cycles across the mark's width. */
  frequency: 6.2,
  /** Radians per second the crest travels. Negative would run it leftward. */
  speed: 1.9,
  /** Phase offset between the top and bottom edges, so the two do not mirror. */
  edgeOffset: 0.8,
};

/**
 * A ripple multiplier for one edge at one position and time.
 *
 * `time` is seconds. `sign` is -1 for the top edge, +1 for the bottom. Returns
 * 1 when `amplitude` is 0, so a silent mark is the exact logo.
 *
 * ── Why the ripple only ever closes ───────────────────────────────────────
 *
 * The obvious form is `1 + depth·amplitude·sin(phase)`, which undulates
 * symmetrically about the stated opening. It is wrong, and measurement caught
 * it: at full amplitude the crests added 18% on top of RATIO_OPEN and the mark
 * opened to 7.07:1 — straight through the floor that exists so a lens never
 * reads as an eye. A decorative detail cannot be allowed to breach a
 * founder-ratified constraint.
 *
 * So the multiplier spans [1 − depth·amplitude, 1]: the ripple modulates
 * INWARD from the opening rather than around it. The peak half-height is
 * therefore exactly the one `ratioForAmplitude` promises, the floor holds by
 * construction at every amplitude and every phase, and the travelling
 * undulation reads the same.
 */
export function rippleAt(p, time, amplitude, sign) {
  if (amplitude <= 0) return 1;
  const phase = p * RIPPLE.frequency - time * RIPPLE.speed + (sign > 0 ? RIPPLE.edgeOffset : 0);
  return 1 - RIPPLE.depth * amplitude * (0.5 + 0.5 * Math.sin(phase));
}

/**
 * The transform that fits a mark of the given width inside a centred safe
 * circle, for maskable and circular-crop icons.
 *
 * A lens is mostly empty space, so it survives a circular crop far better than
 * the mark it replaced — but a 66%-width slot still has its tips outside a 40%
 * radius, and launchers would clip them into blunt ends.
 */
export function safeFit(width, ratio, radiusFraction) {
  const half = peakFor(width, ratio);
  const corner = Math.hypot(width / 2, half);
  const limit = radiusFraction * CANVAS;
  return Math.min(1, limit / corner);
}

/**
 * The mark as a complete SVG.
 *
 * `ratio` picks the cut. `background` produces the icon-tile form; omitting it
 * produces the transparent form used for lockups, splash, and inline glyph use.
 * `safeCircle` (a fraction of the tile) shrinks the mark to survive a crop.
 */
export function strideSignalSvg({
  fill = STRIDE_ORANGE,
  background = null,
  ratio = CUTS.logo,
  title = 'Stride Signal',
  size = CANVAS,
  inset = TILE_INSET,
  safeCircle = null,
  radius = 0,
} = {}) {
  const width = CANVAS * inset;
  const scale = safeCircle ? safeFit(width, ratio, safeCircle * 0.95) : 1;
  const centre = CANVAS / 2;
  const x = centre - (width * scale) / 2;

  const tile = background
    ? `  <rect id="background" width="${CANVAS}" height="${CANVAS}"${radius ? ` rx="${radius}"` : ''} fill="${background}"/>`
    : null;

  return [
    `<svg xmlns="http://www.w3.org/2000/svg" width="${size}" height="${size}" viewBox="0 0 ${CANVAS} ${CANVAS}">`,
    `  <title>${title}</title>`,
    tile,
    `  <g id="stride-signal" fill="${fill}" transform="translate(${x.toFixed(2)} ${centre}) scale(${scale.toFixed(5)})">`,
    `    <path d="${lensPath(width, ratio)}"/>`,
    '  </g>',
    '</svg>',
    '',
  ]
    .filter((line) => line !== null)
    .join('\n');
}

/**
 * `node scripts/stride-signal-geometry.mjs --print-ts` prints the constants the
 * native and marketing copies need. Both are derived data: paste the output,
 * never hand-edit the copies. `--print-spec` prints the whole canon for a
 * reviewer.
 */
if (process.argv[1]?.endsWith('stride-signal-geometry.mjs')) {
  if (process.argv.includes('--print-ts')) {
    console.log(`export const LENS_EXPONENT = ${LENS_EXPONENT};`);
    console.log(`export const RATIO_IDLE = ${RATIO_IDLE};`);
    console.log(`export const RATIO_OPEN = ${RATIO_OPEN};`);
    console.log(`export const TILE_INSET = ${TILE_INSET};`);
    console.log(`export const RIPPLE = ${JSON.stringify(RIPPLE)};`);
  }
  if (process.argv.includes('--print-spec')) {
    console.log(`orange       ${STRIDE_ORANGE}`);
    console.log(`ink          ${STRIDE_INK}`);
    console.log(`exponent     ${LENS_EXPONENT}`);
    console.log(`idle ratio   ${RATIO_IDLE}:1`);
    console.log(`open ratio   ${RATIO_OPEN}:1  (hard floor on the whole identity)`);
    for (const [name, ratio] of Object.entries(CUTS)) {
      const width = CANVAS * TILE_INSET;
      console.log(`cut ${name.padEnd(6)} ${String(ratio).padStart(2)}:1  width ${width} · full height ${(width / ratio).toFixed(1)}`);
    }
    for (const level of [0, 0.25, 0.5, 0.75, 1]) {
      console.log(`amp ${level.toFixed(2)}     ${ratioForAmplitude(level).toFixed(2)}:1`);
    }
  }
}
