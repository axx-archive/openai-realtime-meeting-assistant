/**
 * Builds `Stride.icon` — the Icon Composer bundle iOS 26 renders with Liquid
 * Glass.
 *
 *   node scripts/build-stride-icon-bundle.mjs <outputDir>
 *
 * ── Why a bundle and not three PNGs ───────────────────────────────────────
 *
 * Expo 57 accepts either. The PNG set (`ios.icon: { light, dark, tinted }`) is
 * a flattened picture: whatever depth it has, we painted. The bundle hands the
 * system flat layers plus a description of the material, and iOS composes the
 * specular, the shadow, the dark ground, and the Tinted and Clear renditions
 * itself — so the icon matches the OS it is sitting in rather than
 * approximating a screenshot of it. It is the only route to ClearLight and
 * ClearDark, and the tinted rendition stops being a guess.
 *
 * ── Verification ──────────────────────────────────────────────────────────
 *
 * Xcode 26 ships `ictool`, which is the same renderer the OS uses. Every one of
 * the six renditions is exported and inspected before this is called done.
 * Nothing below was taken on trust: the schema is undocumented, so each key was
 * read off IconComposerFoundation's symbols and then PROVED by rendering a
 * deliberately wrong value (magenta) and checking whether it showed up.
 *
 * ── What the renderer actually honours ────────────────────────────────────
 *
 * This is the part that is not written down anywhere, and it is the difference
 * between a bundle that works and one that silently ignores half of itself:
 *
 *   fill                    HONOURED — but only in the Default rendition.
 *   fill-specializations    IGNORED. So is every other `*-specializations` key,
 *                           at icon, group, and layer level. Proved by setting
 *                           the dark specialization to magenta: it never
 *                           appeared. Do not write appearance overrides and
 *                           assume they took.
 *   layer `fill`            HONOURED. It REPLACES the asset's own colour rather
 *                           than tinting it, so a gradient in the SVG is thrown
 *                           away if the layer also carries a fill. That is why
 *                           the assets below are drawn in flat white.
 *   translucency            HONOURED, and it matters more than it looks. Left
 *                           unset, the masses blend toward the ground — 3.47:1
 *                           on putty collapsing to 1.50:1 on the dark ground.
 *                           Disabled, a layer holds its exact colour in every
 *                           rendition.
 *   specular, shadow        HONOURED. This is where the depth comes from.
 *
 * The consequence is the whole design: THERE IS NO PER-APPEARANCE COLOUR. One
 * set of layer colours has to hold against the putty ground in Default and
 * against Apple's system dark ground in Dark. That is not a limitation we
 * worked around, it is Apple's model — the dark icon is supposed to be the same
 * artwork on the system's own ground, which is also why their guidance is to
 * drop the background in dark rather than paint a darker one.
 *
 * ── The row grey ──────────────────────────────────────────────────────────
 *
 * Chosen by measuring, not by eye. Swept the graphite family through both
 * renditions with translucency off:
 *
 *   #54545C   4.44:1 on putty   1.76:1 on dark   ← the old light-appearance grey
 *   #63636B   3.52:1            2.21:1
 *   #72727A   2.82:1            2.76:1           ← shipped: the balance point
 *   #7F7F87   2.35:1            3.32:1
 *   #8C8C94   1.97:1            3.95:1
 *
 * #72727A is the only value that clears 2.7:1 on BOTH grounds, which is what
 * "one set of colours, two grounds" costs. It also sits between the two greys
 * the brand already had, so nothing new entered the palette.
 *
 * ── Why the masses are not translucent ────────────────────────────────────
 *
 * Turning translucency off is a design choice as much as a contrast fix. The
 * masses are steel — the whole picture is momentum conserved through solid
 * bodies. Glass masses would transmit light instead of transmitting force, and
 * the icon would be saying the opposite of what the product means. `specular`
 * stays on, so they still catch the light the OS puts on them.
 */
import { mkdir, rm, writeFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import {
  APPEARANCES,
  CANVAS,
  CUT,
  massesFor,
  radiusFor,
} from './stride-strike-geometry.mjs';

const outputDir = resolve(process.cwd(), process.argv[2] ?? 'Stride.icon');

/** The row colour that survives both grounds. See the header for the sweep. */
export const ICON_ROW_GREY = '#72727A';

/** "#RRGGBB" → the "srgb:r,g,b,a" literal Icon Composer expects. */
function iconColor(hex, alpha = 1) {
  const value = hex.replace('#', '');
  const parts = [0, 2, 4].map((i) => (parseInt(value.slice(i, i + 2), 16) / 255).toFixed(4));
  return `srgb:${parts.join(',')},${alpha.toFixed(4)}`;
}

const masses = massesFor(CUT);
const r = radiusFor(CUT);

/**
 * One mass's artwork: a flat white disc, recoloured by the layer `fill`.
 *
 * White because `fill` replaces rather than tints — see the header. Flat
 * because the material is the system's job on this pipeline, which is the
 * entire reason for choosing it.
 *
 * ── One mass per layer, and why it is not a detail ────────────────────────
 *
 * The receiving masses are exactly tangent: centres 2r apart, because a cradle
 * row is IN CONTACT and separating them would be a lie about the mechanism.
 * Drawn as two circles in ONE asset, the renderer sees a single fused outline
 * and rims THAT — which pinches the contact into a metallic web and the pair
 * stops reading as two steel balls touching and starts reading as one poured
 * shape. Steel does not bleed into steel.
 *
 * Split across layers, each mass keeps its own silhouette and its own specular,
 * the contact stays a contact, and the geometry never moves. Verified by
 * rendering both and looking at the contact region at 8×.
 */
function massSvg(mass) {
  return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 ${CANVAS} ${CANVAS}" width="${CANVAS}" height="${CANVAS}">\n  <circle cx="${mass.cx}" cy="${mass.cy}" r="${r}" fill="#FFFFFF"/>\n</svg>\n`;
}

await rm(outputDir, { recursive: true, force: true });
await mkdir(resolve(outputDir, 'Assets'), { recursive: true });

const strike = masses.filter((mass) => mass.role === 'active');
const row = masses.filter((mass) => mass.role === 'neutral');

// Ordered far-to-near so the mass closest to the strike is drawn last and its
// edge is the one that survives at the contact.
const rowLayers = row
  .slice()
  .sort((a, b) => b.cx - a.cx)
  .map((mass, index) => ({ mass, file: `receiving-mass-${index + 1}.svg`, name: `Mass ${index + 1}` }));
for (const layer of rowLayers) {
  await writeFile(resolve(outputDir, 'Assets', layer.file), massSvg(layer.mass));
}
await writeFile(resolve(outputDir, 'Assets', 'strike-mass.svg'), massSvg(strike[0]));

const solid = (hex) => ({ solid: iconColor(hex) });

/** Steel, not glass. See the header. */
const SOLID_MASS = { enabled: false, value: 0.5 };

const icon = {
  // Default only. Dark is the system's ground by design.
  fill: solid(APPEARANCES.light.field),
  groups: [
    {
      name: 'Receiving row',
      layers: rowLayers.map((layer) => ({
        name: layer.name,
        'image-name': layer.file,
        fill: solid(ICON_ROW_GREY),
      })),
      // The row is the shared context the impulse passes through. It sits on
      // the ground plane and takes the shallower shadow; lifting it would make
      // everything float and nothing arrive.
      shadow: { kind: 'neutral', opacity: 0.42 },
      translucency: SOLID_MASS,
      specular: true,
    },
    {
      name: 'Strike',
      layers: [{ name: 'Strike mass', 'image-name': 'strike-mass.svg', fill: solid(APPEARANCES.light.active) }],
      // A deeper shadow than the row: this is the mass in flight, and the depth
      // separation is the whole reason the tile has two groups rather than one.
      // It is also what holds the orange off the putty, which is close to it in
      // luminance (1.9:1) however much they differ in hue.
      shadow: { kind: 'neutral', opacity: 0.62 },
      translucency: SOLID_MASS,
      specular: true,
    },
  ],
  'supported-platforms': {
    circles: ['watchOS'],
    squares: ['iOS', 'macOS'],
  },
};

await writeFile(resolve(outputDir, 'icon.json'), `${JSON.stringify(icon, null, 2)}\n`);

console.log(`Wrote ${outputDir} — 2 groups, flat layers, system material`);
