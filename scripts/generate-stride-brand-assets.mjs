/**
 * Regenerates every raster form of the Stride Signal from the canonical
 * geometry in `stride-signal-geometry.mjs`.
 *
 *   npm run brand:regen
 *
 * `mobile/assets/icon-source.svg` is written first and then rasterised, so the
 * committed SVG is always a faithful print of the geometry module rather than a
 * parallel hand-drawn copy that can drift. `mobile/src/__tests__/brandAssets.
 * test.ts` pins the SHA-256 of the SVG and of the release PNGs; if you change
 * the mark, run this and update those digests in the same commit.
 *
 * ── Which tile is the icon ────────────────────────────────────────────────
 *
 * The INVERTED tile is primary: a full-bleed Stride Orange field with the
 * aperture cut out of it in near-black. It is louder than the dark tile and
 * unmistakable on a crowded home screen, which is what an icon has to win at.
 * The dark tile (ink ground, orange mark) is the sanctioned alternate and does
 * real work — it is the iOS dark-appearance icon, so the two ship together
 * rather than one being decoration. Ratified by AJ, 2026-07-29.
 *
 * ── Which cut goes where ──────────────────────────────────────────────────
 *
 * `CUTS.logo` (25:1) never appears in a tile — it is the lockup cut. Tiles use
 * `CUTS.icon` (12:1), and anything below 40px uses `CUTS.micro` (8:1), which is
 * also the identity's hard floor. A 25:1 sliver inside a 40px favicon is a
 * hairline nobody can see, and opening it is how the system keeps it legible.
 */
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { CUTS, STRIDE_INK, STRIDE_ORANGE, strideSignalSvg } from './stride-signal-geometry.mjs';
import sharp from 'sharp';

const repositoryRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..');

/* The primary tile: orange ground, aperture cut out in ink. */
const inverted = (ratio = CUTS.icon, extra = {}) =>
  strideSignalSvg({ fill: STRIDE_INK, background: STRIDE_ORANGE, ratio, ...extra });
/* The sanctioned alternate: ink ground, orange aperture. */
const darkTile = (ratio = CUTS.icon, extra = {}) =>
  strideSignalSvg({ fill: STRIDE_ORANGE, background: STRIDE_INK, ratio, ...extra });

const sourcePath = resolve(repositoryRoot, 'mobile/assets/icon-source.svg');
await writeFile(sourcePath, inverted(), 'utf8');
const source = await readFile(sourcePath, 'utf8');

async function render(svg, outputPath, width, height, options = {}) {
  await mkdir(dirname(outputPath), { recursive: true });
  let pipeline = sharp(Buffer.from(svg), { density: 384 }).resize(width, height, {
    fit: 'fill',
    kernel: sharp.kernel.lanczos3,
  });
  if (options.opaque) {
    pipeline = pipeline.flatten({ background: options.background ?? STRIDE_INK });
  }
  await pipeline.png({ compressionLevel: 9, adaptiveFiltering: true }).toFile(outputPath);
}

async function solid(outputPath, width, height, color) {
  await sharp({
    create: { width, height, channels: 3, background: color },
  }).png({ compressionLevel: 9 }).toFile(outputPath);
}

/* ── web ─────────────────────────────────────────────────────────────── */
const publicDir = resolve(repositoryRoot, 'public');
await render(source, resolve(publicDir, 'app-icon.png'), 512, 512, { opaque: true });
await render(source, resolve(publicDir, 'apple-touch-icon.png'), 180, 180, { opaque: true });
await render(inverted(CUTS.micro), resolve(publicDir, 'favicon.png'), 64, 64, { opaque: true });
await render(source, resolve(publicDir, 'icon-192.png'), 192, 192, { opaque: true });
await render(source, resolve(publicDir, 'icon-512.png'), 512, 512, { opaque: true });
// Launchers crop to a circle, and the aperture's tips are the first thing they
// would blunt. Fit the whole mark inside the 80% safe circle.
await render(
  inverted(CUTS.icon, { safeCircle: 0.4 }),
  resolve(publicDir, 'icon-maskable-512.png'),
  512, 512, { opaque: true, background: STRIDE_ORANGE },
);

/* ── native ──────────────────────────────────────────────────────────── */
const mobileDir = resolve(repositoryRoot, 'mobile/assets');
await render(source, resolve(mobileDir, 'stride-signal-master.png'), 1024, 1024, { opaque: true, background: STRIDE_ORANGE });
await render(source, resolve(mobileDir, 'icon.png'), 1024, 1024, { opaque: true, background: STRIDE_ORANGE });
await render(source, resolve(mobileDir, 'stride-signal-mark.png'), 1024, 1024, { opaque: true, background: STRIDE_ORANGE });
await render(source, resolve(mobileDir, 'adaptive-icon.png'), 512, 512, { opaque: true, background: STRIDE_ORANGE });
await render(inverted(CUTS.micro), resolve(mobileDir, 'favicon.png'), 48, 48, { opaque: true, background: STRIDE_ORANGE });

// The alternate earns its keep as the iOS dark-appearance icon.
await render(darkTile(), resolve(mobileDir, 'ios-icon-dark.png'), 1024, 1024, { opaque: true });

// iOS 18 reads the tinted image as a LUMINOSITY MAP, so the mark has to be the
// light part of a dark field rather than a colour render. The micro cut, because
// a 12:1 slot has too little light area for the system tint to find.
await render(
  strideSignalSvg({ fill: '#FFFFFF', background: STRIDE_INK, ratio: CUTS.micro }),
  resolve(mobileDir, 'ios-icon-tinted.png'), 1024, 1024, { opaque: true },
);

// Splash and Android layers are transparent; the launcher supplies the ground.
await render(strideSignalSvg({ ratio: CUTS.icon }), resolve(mobileDir, 'splash-icon.png'), 1024, 1024);
await render(strideSignalSvg({ fill: '#FFFFFF', ratio: CUTS.icon }), resolve(mobileDir, 'splash-icon-dark.png'), 1024, 1024);
await render(
  strideSignalSvg({ fill: STRIDE_INK, ratio: CUTS.icon, safeCircle: 0.35 }),
  resolve(mobileDir, 'android-icon-foreground.png'), 1024, 1024,
);
await render(
  strideSignalSvg({ fill: '#FFFFFF', ratio: CUTS.icon, safeCircle: 0.35 }),
  resolve(mobileDir, 'android-icon-monochrome.png'), 1024, 1024,
);
// Inverted primary means the adaptive icon's ground is the orange.
await solid(resolve(mobileDir, 'android-icon-background.png'), 1024, 1024, STRIDE_ORANGE);

console.log(`Stride Signal regenerated — inverted primary, ${STRIDE_ORANGE} on ${STRIDE_INK}.`);
