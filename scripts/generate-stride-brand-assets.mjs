/**
 * Regenerates every static Stride identity asset.
 *
 *   npm run brand:regen
 *
 * Two marks, two jobs:
 *
 *   THE STRIKE     the tile — app icon, favicon, lockup mark. Code of record:
 *                  scripts/stride-strike-geometry.mjs. The striking mass is
 *                  bisected by the left frame and rides 0.08 of the tile above
 *                  the row; the masses carry satin depth everywhere the
 *                  platform does not supply its own material.
 *   THE WORDMARK   the name, set as artwork rather than type. Source of truth:
 *                  brand/stride-wordmark-source.svg, a sampled outline traced
 *                  from the approved 1430px master. It fills with currentColor,
 *                  so one file serves every colourway and nothing has to stay
 *                  in sync.
 *
 * iOS gets a third thing: `mobile/assets/Stride.icon`, an Icon Composer bundle
 * built by scripts/build-stride-icon-bundle.mjs, where the material is the
 * system's rather than ours. The PNG appearance set is still generated — Android,
 * the web, the favicons, and the Xcode catalog all need flattened tiles, and
 * they get the satin build because nothing is going to supply material for them.
 *
 * The live voice instrument remains the five-mass Signal Cradle and is NOT
 * generated here; splash artwork uses its rest geometry so native bootstrap can
 * cross-fade into the home control without a logo swap or a positional jump.
 */
import { copyFile, mkdir, readFile, writeFile } from 'node:fs/promises';
import { execFileSync } from 'node:child_process';
import { createRequire } from 'node:module';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import {
  APPEARANCES,
  STRIDE_INK,
  STRIDE_ORANGE,
  STRIDE_PUTTY,
  strikeSvg,
} from './stride-strike-geometry.mjs';

const repositoryRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const requireFromSite = createRequire(resolve(repositoryRoot, 'stride-site/package.json'));
const sharp = requireFromSite('sharp');

const STRIDE_MASS = '#5E5E66';
const STRIDE_MASS_DARK = '#77777D';

const publicDir = resolve(repositoryRoot, 'public');
const mobileDir = resolve(repositoryRoot, 'mobile/assets');
const sitePublicDir = resolve(repositoryRoot, 'stride-site/public');
const brandDir = resolve(repositoryRoot, 'brand');
const publicFontsDir = resolve(publicDir, 'fonts');

/* ── The Strike: print the source, then derive everything from it ────────── */

// The vector sources are PRINTS of the code of record, not hand-drawn files.
// Writing them here is what makes "regenerate, never hand-edit" true rather
// than aspirational — a hand edit is erased by the next regen, and the SHA pin
// in frontend_brand_assets_test.go catches it in between.
const strikeSources = {
  dark: resolve(brandDir, 'stride-strike-source.svg'),
  light: resolve(brandDir, 'stride-strike-light.svg'),
  tinted: resolve(brandDir, 'stride-strike-tinted.svg'),
};
for (const [appearance, path] of Object.entries(strikeSources)) {
  await writeFile(path, strikeSvg({ appearance }));
}

const tiles = {};
for (const appearance of Object.keys(APPEARANCES)) {
  tiles[appearance] = await sharp(Buffer.from(strikeSvg({ appearance })), { density: 400 })
    .resize(1024, 1024)
    .png({ compressionLevel: 9, adaptiveFiltering: true })
    .toBuffer();
}

/** The flat cut, for launchers that re-tint and re-mask the artwork themselves. */
function strikeGlyphSvg({ appearance = 'dark', transparent = true } = {}) {
  return Buffer.from(strikeSvg({ appearance, flat: true, transparent }));
}

function cradleSvg(color) {
  const centers = [256, 384, 512, 640, 768];
  return Buffer.from(`<svg xmlns="http://www.w3.org/2000/svg" width="1024" height="1024" viewBox="0 0 1024 1024">
    ${centers.map((cx) => `<circle cx="${cx}" cy="512" r="64" fill="${color}"/>`).join('')}
  </svg>`);
}

async function render(input, outputPath, width, options = {}) {
  const scale = options.scale ?? 1;
  const markSize = Math.max(1, Math.round(width * scale));
  const resized = await sharp(input)
    .resize(markSize, markSize, { fit: 'fill', kernel: sharp.kernel.lanczos3 })
    .png()
    .toBuffer();
  let pipeline = sharp({
    create: {
      width,
      height: width,
      channels: 4,
      background: { r: 0, g: 0, b: 0, alpha: 0 },
    },
  }).composite([{
    input: resized,
    left: Math.floor((width - markSize) / 2),
    top: Math.floor((width - markSize) / 2),
  }]);
  if (options.background) {
    pipeline = pipeline.flatten({ background: options.background }).removeAlpha();
  } else if (options.opaque) {
    pipeline = pipeline.removeAlpha();
  }
  await mkdir(dirname(outputPath), { recursive: true });
  await pipeline
    .toColourspace('srgb')
    .png({ compressionLevel: 9, adaptiveFiltering: true })
    .toFile(outputPath);
}

async function solid(outputPath, width, color) {
  await sharp({
    create: { width, height: width, channels: 3, background: color },
  }).png({ compressionLevel: 9 }).toFile(outputPath);
}

/* ── Typography ──────────────────────────────────────────────────────────── */

// One bundled typography contract across desktop web and native. The marketing
// build self-hosts the same Google Sans Flex and Geist Mono families via
// `next/font`; the desktop shell uses these local files instead of a network
// font request that can silently fall back.
await mkdir(publicFontsDir, { recursive: true });
for (const [sourceRelative, outputName] of [
  ['google-sans-flex/400Regular/GoogleSansFlex_400Regular.ttf', 'google-sans-flex-400.ttf'],
  ['google-sans-flex/500Medium/GoogleSansFlex_500Medium.ttf', 'google-sans-flex-500.ttf'],
  ['google-sans-flex/600SemiBold/GoogleSansFlex_600SemiBold.ttf', 'google-sans-flex-600.ttf'],
  ['google-sans-flex/700Bold/GoogleSansFlex_700Bold.ttf', 'google-sans-flex-700.ttf'],
  ['geist-mono/400Regular/GeistMono_400Regular.ttf', 'geist-mono-400.ttf'],
  ['geist-mono/500Medium/GeistMono_500Medium.ttf', 'geist-mono-500.ttf'],
]) {
  await copyFile(
    resolve(repositoryRoot, 'mobile/node_modules/@expo-google-fonts', sourceRelative),
    resolve(publicFontsDir, outputName),
  );
}

/* ── The wordmark ────────────────────────────────────────────────────────── */

/**
 * The wordmark is ONE outline. Colourways are that outline with a different
 * fill, generated here so no surface ever hand-writes the path.
 *
 * Orange is the only colourway the product uses — in both themes, by
 * direction. Ink, putty, and white exist because a wordmark that only has one
 * colour cannot be placed on a photograph, a partner's slide, or a garment, and
 * the day that comes up is not the day to re-trace it.
 */
const wordmarkSource = await readFile(resolve(brandDir, 'stride-wordmark-source.svg'), 'utf8');
const wordmarkViewBox = wordmarkSource.match(/viewBox="([^"]+)"/)?.[1];
const wordmarkPath = wordmarkSource.match(/ d="([^"]+)"/)?.[1];
if (!wordmarkViewBox || !wordmarkPath) {
  throw new Error('The wordmark source is missing its viewBox or its outline.');
}

const WORDMARK_COLOURWAYS = {
  orange: STRIDE_ORANGE,
  ink: STRIDE_INK,
  putty: STRIDE_PUTTY,
  white: '#FFFFFF',
};

function wordmarkSvg(fill) {
  return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="${wordmarkViewBox}" role="img" aria-label="Stride"><title>Stride</title><path fill="${fill}" fill-rule="evenodd" d="${wordmarkPath}"/></svg>\n`;
}

// The `currentColor` cut is the one web surfaces should reach for: it inherits
// the element's colour, so a theme switch is a colour change and not an asset
// swap that can be forgotten in one of the two themes.
const wordmarkCurrent = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="${wordmarkViewBox}" role="img" aria-label="Stride"><title>Stride</title><path fill="currentColor" fill-rule="evenodd" d="${wordmarkPath}"/></svg>\n`;

for (const dir of [publicDir, sitePublicDir]) {
  await mkdir(dir, { recursive: true });
  await writeFile(resolve(dir, 'wordmark.svg'), wordmarkCurrent);
  for (const [name, fill] of Object.entries(WORDMARK_COLOURWAYS)) {
    await writeFile(resolve(dir, `wordmark-${name}.svg`), wordmarkSvg(fill));
  }
}

// Native cannot load an SVG from disk without a loader, and react-native-svg
// wants the outline as data. This is a PRINT of the source — regenerate it,
// never edit it, exactly like mobile/src/theme/strideSignal.ts.
const wordmarkAspect = (() => {
  const [, , w, h] = wordmarkViewBox.split(/\s+/).map(Number);
  return { width: w, height: h, ratio: Number((w / h).toFixed(5)) };
})();
await writeFile(
  resolve(repositoryRoot, 'mobile/src/theme/strideWordmark.ts'),
  `/**
 * THE STRIDE WORDMARK — a faithful print of brand/stride-wordmark-source.svg.
 *
 * Generated by \`npm run brand:regen\`. Do not edit: the desktop, the marketing
 * site, and this file all draw the same outline, and a hand edit here is how
 * the phone's wordmark ends up a percent wider than the browser's without
 * anyone noticing until the two appear in one screenshot.
 */
export const STRIDE_WORDMARK_VIEWBOX = '${wordmarkViewBox}';
export const STRIDE_WORDMARK_WIDTH = ${wordmarkAspect.width};
export const STRIDE_WORDMARK_HEIGHT = ${wordmarkAspect.height};
/** width ÷ height. Multiply a target height by this to get the width. */
export const STRIDE_WORDMARK_RATIO = ${wordmarkAspect.ratio};
export const STRIDE_WORDMARK_PATH =
  '${wordmarkPath}';
`,
);

/* ── The Strike, everywhere it lands ─────────────────────────────────────── */

/**
 * The lockup tile, in the two cuts callers actually ask for.
 *
 * `black` and `white` are legacy filenames and they name the GROUND the tile is
 * going onto, not the tile's own colour: black = for light grounds, white = for
 * dark ones. Both used to be rendered from the ink tile, because before the
 * appearance set there was only one tile — which meant the marketing footer,
 * which asks for `tone="white"` and sits on `--black` (#050505), was painting a
 * near-black tile onto a near-black ground. It had no visible edge at all.
 *
 * Now that a real light appearance exists, `white` is that. The prop means
 * something again, and `test:brand` asserts the two cuts are not the same bytes
 * so they can never silently collapse back together.
 */
for (const output of [
  resolve(publicDir, 'brand-mark-black.png'),
  resolve(mobileDir, 'stride-logo-black.png'),
]) {
  await render(tiles.dark, output, 1024, { opaque: true });
}
for (const output of [
  resolve(publicDir, 'brand-mark-white.png'),
  resolve(mobileDir, 'stride-logo-white.png'),
]) {
  await render(tiles.light, output, 1024, { opaque: true });
}

for (const [name, size] of [
  ['app-icon.png', 512],
  ['apple-touch-icon.png', 180],
  ['favicon.png', 64],
  ['icon-192.png', 192],
  ['icon-512.png', 512],
  ['icon-maskable-512.png', 512],
]) {
  await render(tiles.dark, resolve(publicDir, name), size, { opaque: true });
}

// The light-appearance tile at web sizes. NOT wired to a favicon: a putty tile
// at 16px on light browser chrome dissolves, which was tried and rejected. It
// ships so the light appearance can be shown in docs and comps without anyone
// re-deriving it from the geometry by hand.
await render(tiles.light, resolve(publicDir, 'app-icon-light.png'), 512, { opaque: true });

// iOS reads the Icon Composer bundle; these PNGs remain for Android, the web
// manifest, the Xcode catalog, and as the fallback if the bundle is ever
// rejected. `icon.png` is the light appearance because that is the Default
// rendition — the one every other platform means when it says "the icon".
await render(tiles.light, resolve(mobileDir, 'icon.png'), 1024, { opaque: true });
await render(tiles.dark, resolve(mobileDir, 'stride-logo-mark.png'), 1024, { opaque: true });
await render(tiles.light, resolve(mobileDir, 'adaptive-icon.png'), 512, { opaque: true });
await render(tiles.light, resolve(mobileDir, 'favicon.png'), 48, { opaque: true });
await render(tiles.dark, resolve(mobileDir, 'ios-icon-dark.png'), 1024, { opaque: true });
await render(tiles.tinted, resolve(mobileDir, 'ios-icon-tinted.png'), 1024, { opaque: true });

// The native loading artwork is the real five-mass cradle at rest—not the icon.
await render(cradleSvg(STRIDE_MASS), resolve(mobileDir, 'splash-icon.png'), 1024);
await render(cradleSvg(STRIDE_MASS_DARK), resolve(mobileDir, 'splash-icon-dark.png'), 1024);

await render(strikeGlyphSvg({ appearance: 'light' }), resolve(mobileDir, 'android-icon-foreground.png'), 1024);
await render(
  strikeGlyphSvg({ appearance: 'tinted' }),
  resolve(mobileDir, 'android-icon-monochrome.png'),
  1024,
);
await solid(resolve(mobileDir, 'android-icon-background.png'), 1024, STRIDE_PUTTY);

// The Icon Composer bundle. Built last so a failure here is loud rather than
// leaving a half-written bundle next to freshly regenerated PNGs.
execFileSync(
  process.execPath,
  [resolve(repositoryRoot, 'scripts/build-stride-icon-bundle.mjs'), resolve(mobileDir, 'Stride.icon')],
  { stdio: 'inherit' },
);

await writeFile(resolve(mobileDir, 'icon-source.svg'), `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1024 1024"><title>Stride — The Strike</title><image href="icon.png" width="1024" height="1024"/></svg>\n`);
await writeFile(resolve(publicDir, 'app-icon.svg'), `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 512 512"><image href="app-icon.png" width="512" height="512"/></svg>\n`);
await writeFile(resolve(publicDir, 'favicon.svg'), `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64"><image href="favicon.png" width="64" height="64"/></svg>\n`);
await writeFile(resolve(publicDir, 'logo-mark.svg'), `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1024 1024"><image href="brand-mark-black.png" width="1024" height="1024"/></svg>\n`);
await writeFile(resolve(publicDir, 'logo-mark-white.svg'), `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1024 1024"><image href="brand-mark-white.png" width="1024" height="1024"/></svg>\n`);

for (const name of [
  'favicon.png',
  'app-icon.png',
  'app-icon-light.png',
  'apple-touch-icon.png',
  'brand-mark-black.png',
  'brand-mark-white.png',
]) {
  await copyFile(resolve(publicDir, name), resolve(sitePublicDir, name));
}
await writeFile(resolve(sitePublicDir, 'favicon.svg'), `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64"><image href="favicon.png" width="64" height="64"/></svg>\n`);

/* ── The social card ─────────────────────────────────────────────────────── */

// The card sets the name in the wordmark for the same reason every other
// surface now does: a share preview is the one place the brand is seen by
// people who have never seen it, and a font fallback there is a different logo.
const ogMark = tiles.dark.toString('base64');
const ogWordmarkHeight = 96;
const ogWordmarkWidth = Math.round(ogWordmarkHeight * wordmarkAspect.ratio);
const ogMarkX = 600 - Math.round((170 + 28 + ogWordmarkWidth) / 2);
const ogSvg = `<svg xmlns="http://www.w3.org/2000/svg" width="1200" height="630" viewBox="0 0 1200 630">
  <defs>
    <clipPath id="ogMarkTile"><rect x="${ogMarkX}" y="255" width="170" height="170" rx="${Math.round(170 * 0.22)}"/></clipPath>
  </defs>
  <rect width="1200" height="630" fill="${STRIDE_INK}"/>
  <text x="600" y="205" fill="#FFFDF8" font-family="Google Sans Flex, sans-serif" font-size="70" font-weight="400" letter-spacing="-3" text-anchor="middle">my coworker never sleeps</text>
  <image href="data:image/png;base64,${ogMark}" x="${ogMarkX}" y="255" width="170" height="170" clip-path="url(#ogMarkTile)"/>
  <g transform="translate(${ogMarkX + 170 + 28} ${255 + Math.round((170 - ogWordmarkHeight) / 2)})">
    <g transform="scale(${(ogWordmarkWidth / wordmarkAspect.width).toFixed(5)})">
      <path fill="${STRIDE_ORANGE}" fill-rule="evenodd" d="${wordmarkPath}"/>
    </g>
  </g>
  <line x1="445" y1="486" x2="755" y2="486" stroke="${STRIDE_ORANGE}" stroke-width="3"/>
  <text x="600" y="535" fill="#FFFDF8" font-family="Google Sans Flex, sans-serif" font-size="23" font-weight="400" text-anchor="middle">welcome to the age of continuous productivity</text>
</svg>`;
await sharp(Buffer.from(ogSvg), { density: 144 }).resize(1200, 630).png({ compressionLevel: 9 }).toFile(resolve(sitePublicDir, 'og.png'));

console.log('Stride brand regenerated: Strike tiles (light/dark/tinted), Icon Composer bundle, wordmark colourways, cradle launch artwork.');
