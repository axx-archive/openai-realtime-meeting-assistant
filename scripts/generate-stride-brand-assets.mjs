/**
 * Regenerates every static Stride identity asset from “The Strike”.
 *
 *   npm run brand:regen
 *
 * `brand/stride-strike-source.svg` is the source of truth. The live voice
 * instrument remains the six-mass Signal Cradle; splash artwork uses its rest
 * geometry so native bootstrap can cross-fade into the home control without a
 * logo swap or positional jump.
 */
import { copyFile, mkdir, readFile, writeFile } from 'node:fs/promises';
import { createRequire } from 'node:module';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const repositoryRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const requireFromSite = createRequire(resolve(repositoryRoot, 'stride-site/package.json'));
const sharp = requireFromSite('sharp');

const STRIDE_ORANGE = '#FF5A19';
const STRIDE_INK = '#050505';
const STRIDE_MASS = '#5E5E66';
const STRIDE_MASS_DARK = '#77777D';
const sourcePath = resolve(repositoryRoot, 'brand/stride-strike-source.svg');
const source = await readFile(sourcePath);
const sourceText = source.toString('utf8');

for (const required of [
  '<title>Stride — The Strike</title>',
  'cx="-61.44"',
  'cx="614.4"',
  'cx="1024"',
  `fill="${STRIDE_ORANGE}"`,
  `fill="${STRIDE_MASS}"`,
]) {
  if (!sourceText.includes(required)) {
    throw new Error(`The canonical Strike source is missing ${required}.`);
  }
}

const strikeTile = await sharp(source, { density: 144 })
  .resize(1024, 1024)
  .png({ compressionLevel: 9, adaptiveFiltering: true })
  .toBuffer();

function strikeGlyphSvg({ active = STRIDE_ORANGE, mass = STRIDE_MASS } = {}) {
  return Buffer.from(`<svg xmlns="http://www.w3.org/2000/svg" width="1024" height="1024" viewBox="0 0 1024 1024">
    <circle cx="-61.44" cy="512" r="204.8" fill="${active}"/>
    <circle cx="614.4" cy="512" r="204.8" fill="${mass}"/>
    <circle cx="1024" cy="512" r="204.8" fill="${mass}"/>
  </svg>`);
}

function cradleSvg(color) {
  const centers = [192, 320, 448, 576, 704, 832];
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

const publicDir = resolve(repositoryRoot, 'public');
const mobileDir = resolve(repositoryRoot, 'mobile/assets');
const sitePublicDir = resolve(repositoryRoot, 'stride-site/public');
const publicFontsDir = resolve(publicDir, 'fonts');

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

// Static identity surfaces all use the same caught-momentum tile. The old
// black/white filenames remain compatibility contracts for existing callers.
for (const output of [
  resolve(publicDir, 'brand-mark-black.png'),
  resolve(publicDir, 'brand-mark-white.png'),
  resolve(mobileDir, 'stride-logo-black.png'),
  resolve(mobileDir, 'stride-logo-white.png'),
]) {
  await render(strikeTile, output, 1024, { opaque: true });
}

for (const [name, size] of [
  ['app-icon.png', 512],
  ['apple-touch-icon.png', 180],
  ['favicon.png', 64],
  ['icon-192.png', 192],
  ['icon-512.png', 512],
  ['icon-maskable-512.png', 512],
]) {
  await render(strikeTile, resolve(publicDir, name), size, { opaque: true });
}

await render(strikeTile, resolve(mobileDir, 'icon.png'), 1024, { opaque: true });
await render(strikeTile, resolve(mobileDir, 'stride-logo-mark.png'), 1024, { opaque: true });
await render(strikeTile, resolve(mobileDir, 'adaptive-icon.png'), 512, { opaque: true });
await render(strikeTile, resolve(mobileDir, 'favicon.png'), 48, { opaque: true });
await render(strikeTile, resolve(mobileDir, 'ios-icon-dark.png'), 1024, { opaque: true });

// iOS tinted icons are luminosity maps; preserve the Strike geometry in white.
const monochromeTile = await sharp({
  create: { width: 1024, height: 1024, channels: 3, background: STRIDE_INK },
}).composite([{ input: strikeGlyphSvg({ active: '#FFFFFF', mass: '#FFFFFF' }) }]).png().toBuffer();
await render(monochromeTile, resolve(mobileDir, 'ios-icon-tinted.png'), 1024, { opaque: true });

// The native loading artwork is the real six-mass cradle at rest—not the icon.
await render(cradleSvg(STRIDE_MASS), resolve(mobileDir, 'splash-icon.png'), 1024);
await render(cradleSvg(STRIDE_MASS_DARK), resolve(mobileDir, 'splash-icon-dark.png'), 1024);

await render(strikeGlyphSvg(), resolve(mobileDir, 'android-icon-foreground.png'), 1024);
await render(
  strikeGlyphSvg({ active: '#FFFFFF', mass: '#FFFFFF' }),
  resolve(mobileDir, 'android-icon-monochrome.png'),
  1024,
);
await solid(resolve(mobileDir, 'android-icon-background.png'), 1024, STRIDE_INK);

await writeFile(resolve(mobileDir, 'icon-source.svg'), `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1024 1024"><title>Stride — The Strike</title><image href="icon.png" width="1024" height="1024"/></svg>\n`);
await writeFile(resolve(publicDir, 'app-icon.svg'), `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 512 512"><image href="app-icon.png" width="512" height="512"/></svg>\n`);
await writeFile(resolve(publicDir, 'favicon.svg'), `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64"><image href="favicon.png" width="64" height="64"/></svg>\n`);
await writeFile(resolve(publicDir, 'logo-mark.svg'), `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1024 1024"><image href="brand-mark-black.png" width="1024" height="1024"/></svg>\n`);
await writeFile(resolve(publicDir, 'logo-mark-white.svg'), `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1024 1024"><image href="brand-mark-white.png" width="1024" height="1024"/></svg>\n`);

for (const name of ['favicon.png', 'app-icon.png', 'apple-touch-icon.png', 'brand-mark-black.png', 'brand-mark-white.png']) {
  await copyFile(resolve(publicDir, name), resolve(sitePublicDir, name));
}
await writeFile(resolve(sitePublicDir, 'favicon.svg'), `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64"><image href="favicon.png" width="64" height="64"/></svg>\n`);

const ogMark = strikeTile.toString('base64');
const ogSvg = `<svg xmlns="http://www.w3.org/2000/svg" width="1200" height="630" viewBox="0 0 1200 630">
  <rect width="1200" height="630" fill="#050505"/>
  <text x="600" y="205" fill="#FFFDF8" font-family="Google Sans Flex, sans-serif" font-size="70" font-weight="400" letter-spacing="-3" text-anchor="middle">my coworker never sleeps</text>
  <image href="data:image/png;base64,${ogMark}" x="330" y="255" width="170" height="170"/>
  <text x="530" y="380" fill="#FFFDF8" font-family="Google Sans Flex, sans-serif" font-size="112" font-weight="700" letter-spacing="-6">Stride</text>
  <line x1="445" y1="486" x2="755" y2="486" stroke="#FF5A19" stroke-width="3"/>
  <text x="600" y="535" fill="#FFFDF8" font-family="Google Sans Flex, sans-serif" font-size="23" font-weight="400" text-anchor="middle">welcome to the age of continuous productivity</text>
</svg>`;
await sharp(Buffer.from(ogSvg)).png({ compressionLevel: 9 }).toFile(resolve(sitePublicDir, 'og.png'));

console.log('Stride brand regenerated from The Strike; cradle launch artwork ready.');
