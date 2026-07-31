/**
 * THE STRIKE — canonical geometry and material for the static identity.
 *
 * One source of truth for the app icon, the favicon, the lockup tile, and every
 * Apple appearance variant. If the tile changes, it changes HERE and every
 * surface is regenerated from it.
 *
 * ── What the tile shows ───────────────────────────────────────────────────
 *
 * One caught frame of the Signal Cradle: the energised mass entering from the
 * left, the equal neutral row exiting the right. The crop is the meaning — a
 * moment caught, not an apparatus described.
 *
 * ── The lift ──────────────────────────────────────────────────────────────
 *
 * The striking mass rides ABOVE the row's axis. A mass sitting level with the
 * row it is about to hit has already arrived; nothing is about to happen. Lift
 * it and the tile has a before and an after — the frame reads as descent, and
 * the whole point of the cradle is that momentum is in transit.
 *
 *     STRIKE_LIFT = 0.08 × canvas = exactly two fifths of a mass radius
 *
 * Two fifths is the measured lift of the approved comp (0.388r), rounded to a
 * fraction of the tile so the number survives being restated. It is deliberately
 * short of a full radius: at 1.0r the mass clears the row entirely and the tile
 * stops reading as one row of masses with one of them raised, and starts reading
 * as two unrelated objects.
 *
 * ── Satin, and why it is baked here ───────────────────────────────────────
 *
 * Apple's guidance for Liquid Glass icons is to ship flat layers and let Icon
 * Composer's material system supply depth. That applies to the `.icon` pipeline.
 * This project ships the documented PNG appearance set instead
 * (`ios.icon: { light, dark, tinted }`), where there is no material system —
 * so the material is ours to draw, and flat discs would render flat.
 *
 * The ramp is satin, not gloss: one broad low-contrast highlight from the upper
 * left, no specular dot, and a bounce on the shadowed limb so a mass reads as a
 * solid in a room rather than a vignetted circle. The numbers below were
 * measured off the approved comp, then re-expressed in OKLab so the same ramp
 * can be applied to any base colour and stay perceptually equal — a fixed RGB
 * offset that flatters orange crushes graphite.
 *
 * Shadows GAIN chroma and highlights LOSE a little. That is not a stylistic
 * flourish; it is what the comp measures (the orange's blue channel falls from
 * 25 to 4 into shadow while red only falls from 255 to 223) and what pigment
 * actually does. Mixing toward grey instead produces the muddy plastic look
 * that makes generated icons obvious.
 *
 * This material belongs to THE STRIKE ONLY. The live Signal Cradle stays flat
 * on purpose and its tests forbid gradients — an instrument that has to be read
 * at a glance must not be shaded like an ornament.
 */

/** Canonical Stride Orange. The mark, the ember, and the icon are one colour. */
export const STRIDE_ORANGE = '#FF5A19';
/** The tile's dark ground. */
export const STRIDE_INK = '#050505';
/** The receiving row on a dark ground. */
export const STRIDE_GRAPHITE = '#5E5E66';
/** The receiving row on a light ground, and on dark where Apple wants lift. */
export const STRIDE_GRAPHITE_LIGHT = '#77777D';
/** Warm Putty — the light appearance's ground, and light mode's ground. */
export const STRIDE_PUTTY = '#CFC5B7';

export const CANVAS = 1024;

/** Every mass is one fifth of the tile across. */
export const MASS_RADIUS = CANVAS * 0.2;
/** The row the receiving masses sit on. */
export const ROW_AXIS = CANVAS / 2;
/** How far the striking mass rides above that axis. See the header. */
export const STRIKE_LIFT = CANVAS * 0.08;

/**
 * The crops.
 *
 * The striking mass sits outside or astride the left frame and the row runs off
 * the right edge; both are crops, not accidents. What is in dispute is HOW MUCH
 * of the striking mass the frame keeps, and that is a real decision rather than
 * a detail: it is the difference between a tile that reads as "a row of masses
 * with an orange edge" and one that reads as "a mass falling into a row".
 *
 *   frame   the shipped canon. The striker is centred 0.3r outside the frame,
 *           so 14% of the tile's width is orange. At 40px that is five pixels.
 *   halved  the striker is bisected by the left frame — half a mass, exactly.
 *           Same radius, same row, twice the orange, and a rule that survives
 *           being restated.
 *   comp    the geometry measured off the approved comp: smaller masses pulled
 *           further into frame.
 *
 * `radius`, `strike`, and `row` are all fractions of the canvas.
 */
export const CUTS = {
  frame: { radius: 0.2, strike: -0.06, row: [0.6, 1.0] },
  halved: { radius: 0.2, strike: 0, row: [0.6, 1.0] },
  comp: { radius: 0.171, strike: 0.037, row: [0.643, 0.985] },
};

/** The crop the tiles ship at. */
export const CUT = 'halved';

/** The three mass centres for a crop. Only the striker carries the lift. */
export function massesFor(cut = CUT) {
  const spec = CUTS[cut];
  if (!spec) throw new Error(`Unknown Strike cut: ${cut}`);
  return [
    { id: 'strike', cx: spec.strike * CANVAS, cy: ROW_AXIS - STRIKE_LIFT, role: 'active' },
    { id: 'receive-near', cx: spec.row[0] * CANVAS, cy: ROW_AXIS, role: 'neutral' },
    { id: 'receive-far', cx: spec.row[1] * CANVAS, cy: ROW_AXIS, role: 'neutral' },
  ];
}

/** Mass radius for a crop. */
export const radiusFor = (cut = CUT) => CUTS[cut].radius * CANVAS;

export const MASSES = massesFor(CUT);

/* ── colour ──────────────────────────────────────────────────────────────── */

const clamp01 = (v) => Math.min(1, Math.max(0, v));

function hexToRgb(hex) {
  const value = hex.replace('#', '');
  return [0, 2, 4].map((i) => parseInt(value.slice(i, i + 2), 16) / 255);
}

function rgbToHex(rgb) {
  return `#${rgb.map((v) => Math.round(clamp01(v) * 255).toString(16).padStart(2, '0')).join('').toUpperCase()}`;
}

const toLinear = (c) => (c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4);
const toSrgb = (c) => (c <= 0.0031308 ? c * 12.92 : 1.055 * c ** (1 / 2.4) - 0.055);

/** sRGB → OKLab. */
export function oklab(hex) {
  const [r, g, b] = hexToRgb(hex).map(toLinear);
  const l = Math.cbrt(0.4122214708 * r + 0.5363325363 * g + 0.0514459929 * b);
  const m = Math.cbrt(0.2119034982 * r + 0.6806995451 * g + 0.1073969566 * b);
  const s = Math.cbrt(0.0883024619 * r + 0.2817188376 * g + 0.6299787005 * b);
  return [
    0.2104542553 * l + 0.793617785 * m - 0.0040720468 * s,
    1.9779984951 * l - 2.428592205 * m + 0.4505937099 * s,
    0.0259040371 * l + 0.7827717662 * m - 0.808675766 * s,
  ];
}

/** OKLab → sRGB hex. */
export function oklabToHex([L, a, b]) {
  const l = (L + 0.3963377774 * a + 0.2158037573 * b) ** 3;
  const m = (L - 0.1055613458 * a - 0.0638541728 * b) ** 3;
  const s = (L - 0.0894841775 * a - 1.291485548 * b) ** 3;
  return rgbToHex([
    4.0767416621 * l - 3.3077115913 * m + 0.2309699292 * s,
    -1.2684380046 * l + 2.6097574011 * m - 0.3413193965 * s,
    -0.0041960863 * l - 0.7034186147 * m + 1.707614701 * s,
  ].map(toSrgb));
}

/**
 * Move a colour along the satin ramp.
 *
 * `stops` is lightness in OKLab units; positive is toward the lit limb.
 * Chroma rides the opposite way — see the header.
 */
export function shade(hex, deltaL) {
  const [L, a, b] = oklab(hex);
  const chroma = 1 - deltaL * 0.55;
  return oklabToHex([clamp01(L + deltaL), a * chroma, b * chroma]);
}

/**
 * The satin ramp, in OKLab lightness offsets.
 *
 * Measured from the approved comp: the orange's lit limb sits about +0.075 and
 * its shadowed limb about -0.085 from base, with base itself reached around
 * three quarters of the way down the ball.
 */
export const SATIN = {
  highlight: 0.075,
  base: 0,
  shadow: -0.085,
  /** Reflected light on the shadowed limb. Small — it is bounce, not a second key. */
  bounce: 0.055,
  bounceOpacity: 0.42,
  /** Where the light is, as a fraction of the mass radius from its centre. */
  keyX: -0.36,
  keyY: -0.44,
  /** How far the gradient reaches. Above 1 the terminator softens. */
  spread: 1.28,
};

/* ── drawing ─────────────────────────────────────────────────────────────── */

/**
 * The gradient defs and the circle for one mass.
 *
 * A mass is three passes: the satin body, the bounce on the shadowed limb, and
 * a faint seat that darkens the last few percent of the radius. The seat is
 * what stops a shaded circle from looking like it is lit from inside.
 */
function massLayers(mass, base, index, r) {
  const id = `satin-${index}`;
  const { cx, cy } = mass;
  const fx = cx + r * SATIN.keyX;
  const fy = cy + r * SATIN.keyY;

  const defs = [
    `    <radialGradient id="${id}-body" gradientUnits="userSpaceOnUse" cx="${fx.toFixed(2)}" cy="${fy.toFixed(2)}" r="${(r * SATIN.spread).toFixed(2)}">`,
    `      <stop offset="0" stop-color="${shade(base, SATIN.highlight)}"/>`,
    `      <stop offset="0.52" stop-color="${base}"/>`,
    `      <stop offset="1" stop-color="${shade(base, SATIN.shadow)}"/>`,
    '    </radialGradient>',
    `    <radialGradient id="${id}-bounce" gradientUnits="userSpaceOnUse" cx="${(cx + r * 0.42).toFixed(2)}" cy="${(cy + r * 0.54).toFixed(2)}" r="${(r * 0.78).toFixed(2)}">`,
    `      <stop offset="0" stop-color="${shade(base, SATIN.bounce)}" stop-opacity="${SATIN.bounceOpacity}"/>`,
    `      <stop offset="1" stop-color="${shade(base, SATIN.bounce)}" stop-opacity="0"/>`,
    '    </radialGradient>',
    `    <radialGradient id="${id}-seat" gradientUnits="userSpaceOnUse" cx="${cx.toFixed(2)}" cy="${cy.toFixed(2)}" r="${r.toFixed(2)}">`,
    `      <stop offset="0.84" stop-color="${shade(base, SATIN.shadow)}" stop-opacity="0"/>`,
    `      <stop offset="1" stop-color="${shade(base, SATIN.shadow)}" stop-opacity="0.5"/>`,
    '    </radialGradient>',
  ].join('\n');

  const body = [
    `  <circle cx="${cx}" cy="${cy}" r="${r}" fill="url(#${id}-body)"/>`,
    `  <circle cx="${cx}" cy="${cy}" r="${r}" fill="url(#${id}-bounce)"/>`,
    `  <circle cx="${cx}" cy="${cy}" r="${r}" fill="url(#${id}-seat)"/>`,
  ].join('\n');

  return { defs, body };
}

/**
 * The appearance set.
 *
 * `field` is the tile ground and `fieldFoot` the value it reaches at the bottom
 * edge — a ground that is dead flat reads as a sticker, and the comp measures a
 * real (tiny) vertical fall. `active` and `neutral` are the mass colours.
 *
 * dark  — Apple asks for boosted contrast so elements do not sink into black,
 *         so the receiving row lifts to the light graphite.
 * tinted— a luminance map: the system re-tints it, so the ONLY thing this
 *         variant has to preserve is the story. The striking mass is white and
 *         the receiving row is mid grey, so custody of the energy still reads
 *         after all colour is gone. Painting every mass white — as the previous
 *         tinted asset did — throws that away and leaves three identical dots.
 */
export const APPEARANCES = {
  light: {
    field: STRIDE_PUTTY,
    fieldFoot: shade(STRIDE_PUTTY, -0.02),
    active: STRIDE_ORANGE,
    neutral: '#54545C',
  },
  dark: {
    field: '#121212',
    fieldFoot: STRIDE_INK,
    active: STRIDE_ORANGE,
    neutral: STRIDE_GRAPHITE_LIGHT,
  },
  tinted: {
    field: '#000000',
    fieldFoot: '#000000',
    active: '#FFFFFF',
    neutral: '#8A8A8A',
  },
};

/**
 * The Strike as a complete SVG tile.
 *
 * No rounded corners and no mask: every platform that consumes this applies its
 * own, and a mask baked into the artwork shows up as a dark seam inside the
 * system's. The tile is fully opaque, which iOS requires.
 *
 * `flat` drops the satin for the surfaces that genuinely want flat art — the
 * Android monochrome layer and the adaptive foreground, which get re-tinted and
 * re-masked by the launcher.
 */
export function strikeSvg({ appearance = 'dark', flat = false, size = CANVAS, cut = CUT, transparent = false } = {}) {
  const palette = APPEARANCES[appearance];
  if (!palette) throw new Error(`Unknown Strike appearance: ${appearance}`);
  const masses = massesFor(cut);
  const r = radiusFor(cut);

  const colourFor = (mass) => (mass.role === 'active' ? palette.active : palette.neutral);

  if (flat) {
    return [
      `<svg xmlns="http://www.w3.org/2000/svg" width="${size}" height="${size}" viewBox="0 0 ${CANVAS} ${CANVAS}">`,
      '  <title>Stride — The Strike</title>',
      transparent ? null : `  <rect width="${CANVAS}" height="${CANVAS}" fill="${palette.field}"/>`,
      ...masses.map((mass) => `  <circle cx="${mass.cx}" cy="${mass.cy}" r="${r}" fill="${colourFor(mass)}"/>`),
      '</svg>',
      '',
    ].filter((line) => line !== null).join('\n');
  }

  const layers = masses.map((mass, index) => massLayers(mass, colourFor(mass), index, r));

  return [
    `<svg xmlns="http://www.w3.org/2000/svg" width="${size}" height="${size}" viewBox="0 0 ${CANVAS} ${CANVAS}">`,
    '  <title>Stride — The Strike</title>',
    '  <desc>The energised mass falls in from the left while the equal-mass row exits the right frame.</desc>',
    '  <defs>',
    `    <linearGradient id="field" x1="0" y1="0" x2="0" y2="${CANVAS}" gradientUnits="userSpaceOnUse">`,
    `      <stop offset="0" stop-color="${palette.field}"/>`,
    `      <stop offset="1" stop-color="${palette.fieldFoot}"/>`,
    '    </linearGradient>',
    ...layers.map((layer) => layer.defs),
    '  </defs>',
    transparent ? null : `  <rect width="${CANVAS}" height="${CANVAS}" fill="url(#field)"/>`,
    ...layers.map((layer) => layer.body),
    '</svg>',
    '',
  ].filter((line) => line !== null).join('\n');
}

/**
 * `node scripts/stride-strike-geometry.mjs --print-spec` prints the canon for a
 * reviewer; `--print <appearance>` prints one tile.
 */
if (process.argv[1]?.endsWith('stride-strike-geometry.mjs')) {
  if (process.argv.includes('--print-spec')) {
    console.log(`canvas        ${CANVAS}`);
    console.log(`mass radius   ${MASS_RADIUS}  (0.2 × canvas)`);
    console.log(`row axis      ${ROW_AXIS}`);
    console.log(`strike lift   ${STRIKE_LIFT}  (0.08 × canvas = ${(STRIKE_LIFT / MASS_RADIUS).toFixed(2)}r)`);
    console.log(`shipped cut   ${CUT}`);
    for (const [name, spec] of Object.entries(CUTS)) {
      const r = radiusFor(name);
      const visible = spec.strike * CANVAS + r;
      console.log(`cut ${name.padEnd(7)} r ${r.toFixed(1).padStart(6)}  strike cx ${(spec.strike * CANVAS).toFixed(1).padStart(7)}  orange visible ${visible.toFixed(0).padStart(4)}px (${(visible / CANVAS * 100).toFixed(1)}% of tile, ${(visible / CANVAS * 40).toFixed(1)}px at a 40px icon)`);
    }
    for (const mass of MASSES) console.log(`mass ${mass.id.padEnd(12)} cx ${String(mass.cx).padStart(8)}  cy ${String(mass.cy).padStart(6)}  ${mass.role}`);
    for (const [name, palette] of Object.entries(APPEARANCES)) {
      console.log(`appearance ${name.padEnd(7)} field ${palette.field} → ${palette.fieldFoot}   active ${palette.active}   neutral ${palette.neutral}`);
      console.log(`  ${name} active ramp   ${shade(palette.active, SATIN.highlight)} → ${palette.active} → ${shade(palette.active, SATIN.shadow)}`);
      console.log(`  ${name} neutral ramp  ${shade(palette.neutral, SATIN.highlight)} → ${palette.neutral} → ${shade(palette.neutral, SATIN.shadow)}`);
    }
  }
  const printIndex = process.argv.indexOf('--print');
  if (printIndex > -1) {
    console.log(strikeSvg({ appearance: process.argv[printIndex + 1] ?? 'dark' }));
  }
}
