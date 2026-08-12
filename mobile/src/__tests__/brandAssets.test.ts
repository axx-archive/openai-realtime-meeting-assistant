import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import test from 'node:test';
import { inflateSync } from 'node:zlib';

const mobileRoot = resolve(dirname(fileURLToPath(import.meta.url)), '../..');
const read = (path: string) => readFileSync(resolve(mobileRoot, path));
const text = (path: string) => read(path).toString('utf8');
const sha256 = (value: Buffer) => createHash('sha256').update(value).digest('hex');

function pngHeader(path: string) {
  const png = read(path);
  assert.equal(png.toString('ascii', 1, 4), 'PNG', `${path} must be a PNG`);
  return {
    width: png.readUInt32BE(16),
    height: png.readUInt32BE(20),
    colorType: png[25],
  };
}

function decodeOpaquePng(path: string) {
  const png = read(path);
  const { width, height, colorType } = pngHeader(path);
  assert.equal(png[24], 8, `${path} must use 8-bit channels`);
  assert.ok(colorType === 0 || colorType === 2, `${path} must be an opaque grayscale or RGB PNG`);
  assert.equal(png[28], 0, `${path} must not be interlaced`);

  const idat: Buffer[] = [];
  for (let offset = 8; offset < png.length; ) {
    const length = png.readUInt32BE(offset);
    const type = png.toString('ascii', offset + 4, offset + 8);
    if (type === 'IDAT') idat.push(png.subarray(offset + 8, offset + 8 + length));
    offset += 12 + length;
  }
  const encoded = inflateSync(Buffer.concat(idat));
  const channels = colorType === 0 ? 1 : 3;
  const stride = width * channels;
  const pixels = Buffer.alloc(stride * height);
  const paeth = (a: number, b: number, c: number) => {
    const p = a + b - c;
    const pa = Math.abs(p - a);
    const pb = Math.abs(p - b);
    const pc = Math.abs(p - c);
    return pa <= pb && pa <= pc ? a : pb <= pc ? b : c;
  };
  for (let y = 0; y < height; y += 1) {
    const filter = encoded[y * (stride + 1)];
    const rowStart = y * stride;
    const sourceStart = y * (stride + 1) + 1;
    for (let x = 0; x < stride; x += 1) {
      const raw = encoded[sourceStart + x];
      const left = x >= channels ? pixels[rowStart + x - channels] : 0;
      const above = y > 0 ? pixels[rowStart + x - stride] : 0;
      const upperLeft = y > 0 && x >= channels ? pixels[rowStart + x - stride - channels] : 0;
      const predictor = filter === 0 ? 0
        : filter === 1 ? left
          : filter === 2 ? above
            : filter === 3 ? Math.floor((left + above) / 2)
              : filter === 4 ? paeth(left, above, upperLeft)
                : assert.fail(`${path} uses unsupported PNG filter ${filter}`);
      pixels[rowStart + x] = (raw + predictor) & 0xff;
    }
  }
  return { width, height, pixels, channels };
}

test('The Strike vector is the exact static identity source', () => {
  const strikeSource = read('../brand/stride-strike-source.svg');
  const canonicalSvg = read('assets/icon-source.svg');
  const releasePng = read('assets/icon.png');
  const darkPng = read('assets/ios-icon-dark.png');
  const tintedPng = read('assets/ios-icon-tinted.png');

  assert.equal(sha256(strikeSource), '1e861145f455804b608d7c663ff4e9a892957be9f27094dd69f64b5cbdee2423');
  assert.equal(sha256(canonicalSvg), '96ba41b8f1bbfb4407d443e4647ba455774f9676834b5cb230d72237b55d3dda');
  assert.match(canonicalSvg.toString('utf8'), /<title>Stride — The Strike<\/title>/);
  // icon.png is the LIGHT appearance — the Default rendition, and what every
  // non-Apple platform means when it says "the icon".
  assert.equal(sha256(releasePng), '729c00fafe50a48b6101db057de5795790361fec1c71dbf8053db2822ef16e4a');
  // These used to be asserted EQUAL, which is the signature of an icon that has
  // no appearance set at all: one tile pretending to be three. All three must
  // now differ, or a variant has silently collapsed back onto another.
  assert.notEqual(sha256(darkPng), sha256(releasePng), 'the dark appearance must not be the light one');
  assert.notEqual(sha256(tintedPng), sha256(releasePng), 'the tinted appearance must not be the light one');
  assert.notEqual(sha256(tintedPng), sha256(darkPng), 'the tinted appearance must not be the dark one');
});

test('React Native shell separates the static logo from composer voice controls', () => {
  const component = text('src/components/BrandMark.tsx');
  assert.match(component, /from 'expo-image'/);
  assert.match(component, /stride-logo-mark\.png/);
  assert.match(component, /stride-logo-black\.png/);
  assert.match(component, /stride-logo-white\.png/);
  assert.match(component, /export function StrideLogo/);

  // Home now follows the familiar composer grammar: bounded Dictate mic and a
  // distinct circular full-duplex Live Voice control. No duplicate hero voice
  // control or static lockup may return.
  const canvas = text('src/screens/CanvasScreen.tsx');
  assert.doesNotMatch(canvas, /<StrideCradle\b/);
  assert.match(canvas, /accessibilityLabel="Dictate a message"/);
  assert.match(canvas, /Start a new private voice chat with Scout/);
  assert.match(canvas, /SymbolView name="waveform"/);
  assert.doesNotMatch(canvas, /<StrideLogo\b/);
  assert.doesNotMatch(canvas, /styles\.glow|rgba\(255,90,25,0\.035\)/);
  assert.doesNotMatch(canvas, />SCOUT<|<Dock/);

  const cradle = text('src/components/StrideCradle.tsx');
  const physics = text('src/theme/strideCradle.ts');
  assert.match(cradle, /BALL_COUNT = 5/);
  assert.match(cradle, /const spacing = radius \* 2;/);
  assert.match(cradle, /apertureAmplitude\(trace, listening\)/);
  assert.match(cradle, /useReduceMotion/);
  assert.match(cradle, /source\?: StrideCradleSource/);
  assert.match(cradle, /restTint = colors\.text2/);
  assert.doesNotMatch(cradle, /String\(colors\.text2\)/);
  assert.match(cradle, /draw\(amplitudeRef\.current, true\)/);
  assert.match(cradle, /const isSourceEdge = sourceRef\.current === 'human'/);
  assert.match(cradle, /strideCradleContactWeights\(physics, BALL_COUNT\)/);
  assert.doesNotMatch(cradle, /carrierRef|carrierHaloRef/);
  assert.doesNotMatch(cradle, /coreRefs|ember\[300\]/);
  assert.doesNotMatch(cradle, /<Line|\bLine,/);
  assert.doesNotMatch(cradle, /RadialGradient|<Defs|\bStop,/);
  assert.doesNotMatch(cradle, /Animated\.loop|withRepeat|setInterval/);
  assert.match(physics, /RESTITUTION = 0\.985/);
  assert.match(physics, /PENDULUM_LENGTH_METRES = 0\.52/);
  assert.match(physics, /STRIDE_CRADLE_TRANSFER_SECONDS = 0\.26/);
  assert.match(physics, /leftVelocity = 0/);
  assert.match(physics, /rightVelocity = outgoing/);
  assert.match(physics, /rightVelocity = 0/);
  assert.match(physics, /leftVelocity = -outgoing/);

  const navigation = text('src/navigation/RootNavigator.tsx');
  const composition = text('src/components/CanvasCradleComposition.tsx');
  assert.match(navigation, /return <LaunchCradle \/>/);
  assert.match(navigation, /Animated\.timing\(launchOpacity,[\s\S]*useNativeDriver: true/);
  assert.match(navigation, /<LaunchCradle \/>/g);
  assert.match(composition, /<StrideCradle trace=\{EMPTY_TRACE\} listening=\{false\} \/>/);
  assert.match(composition, /export const canvasCradleComposition/);
  assert.match(canvas, /contentContainerStyle=\{canvasCradleComposition\.body\}/);
  assert.match(canvas, /canvasCradleComposition\.skyAbove/);
  assert.match(canvas, /styles\.composerVoice/);
  assert.match(canvas, /SymbolView name="waveform"/);
  assert.match(canvas, /style=\{\[canvasCradleComposition\.copyBlock, styles\.homeCopyBlock\]\}/);
  assert.match(canvas, /homeCopyBlock:\s*\{ width: '100%', minHeight: 0 \}/);
  assert.match(canvas, /canvasCradleComposition\.skyBelow/);
  assert.doesNotMatch(navigation, /StrideSignalGlyph/);
  assert.doesNotMatch(navigation, /Home:\s*'flame\.fill'/);
});

test('native typography bundles the same brand families as desktop and marketing', () => {
  const app = text('App.tsx');
  const tokens = text('src/theme/tokens.ts');
  const install = text('src/theme/installTypography.ts');
  const packageJson = text('package.json');

  assert.match(packageJson, /"@expo-google-fonts\/google-sans-flex": "\^0\.4\.3"/);
  assert.match(packageJson, /"@expo-google-fonts\/geist-mono": "\^0\.4\.3"/);
  assert.match(app, /GoogleSansFlex_400Regular/);
  assert.match(app, /GoogleSansFlex_700Bold/);
  assert.match(app, /GeistMono_400Regular/);
  assert.match(app, /useFonts\(\{/);
  assert.match(tokens, /sansRegular: 'GoogleSansFlex_400Regular'/);
  assert.match(tokens, /monoMedium: 'GeistMono_500Medium'/);
  assert.match(install, /TextInput/);
  assert.match(install, /fontFamily: fonts\.sansRegular/);
});

test('Expo icon and splash sources have release-safe dimensions and alpha models', () => {
  const expected = new Map<string, [number, number, number]>([
    ['assets/icon.png', [1024, 1024, 2]],
    ['assets/ios-icon-dark.png', [1024, 1024, 2]],
    ['assets/ios-icon-tinted.png', [1024, 1024, 2]],
    ['assets/splash-icon.png', [1024, 1024, 6]],
    ['assets/splash-icon-dark.png', [1024, 1024, 6]],
    ['assets/android-icon-foreground.png', [1024, 1024, 6]],
    ['assets/android-icon-background.png', [1024, 1024, 2]],
    ['assets/android-icon-monochrome.png', [1024, 1024, 6]],
    ['assets/stride-logo-mark.png', [1024, 1024, 2]],
    ['assets/stride-logo-black.png', [1024, 1024, 2]],
    ['assets/stride-logo-white.png', [1024, 1024, 2]],
    ['assets/favicon.png', [48, 48, 2]],
  ]);
  for (const [path, wanted] of expected) {
    const actual = pngHeader(path);
    assert.deepEqual([actual.width, actual.height, actual.colorType], wanted, path);
  }

  /**
   * iOS uses this grayscale image as a luminosity map, so the tinted appearance
   * has to survive losing all colour. Guard every side of that map.
   *
   * This used to assert one bright mass of pixels, because the tinted asset
   * painted EVERY mass white. That is a blank cheque: three identical dots pass
   * it, and three identical dots throw away the only thing the tile means —
   * which mass has the energy. The Strike now maps to THREE bands, and all
   * three are checked:
   *
   *   dark    the field
   *   mid     the receiving row, present and distinctly not the field
   *   light   the striking mass, and the brightest thing in the tile
   *
   * The striking mass is a half-disc, so it is only ~6% of the tile by area.
   * Requiring it to be a fifth of the pixels is what forced the old asset to
   * light up the row as well.
   */
  const tinted = decodeOpaquePng('assets/ios-icon-tinted.png');
  let dark = 0;
  let mid = 0;
  let light = 0;
  for (let offset = 0; offset < tinted.pixels.length; offset += tinted.channels) {
    const luminance = tinted.channels === 1
      ? tinted.pixels[offset]
      : 0.2126 * tinted.pixels[offset]
        + 0.7152 * tinted.pixels[offset + 1]
        + 0.0722 * tinted.pixels[offset + 2];
    if (luminance < 32) dark += 1;
    else if (luminance > 200) light += 1;
    else if (luminance > 96) mid += 1;
  }
  const pixelCount = tinted.width * tinted.height;
  assert.ok(dark / pixelCount > 0.5, `tinted icon needs a substantial dark field (got ${dark / pixelCount})`);
  assert.ok(
    mid / pixelCount > 0.1,
    `the tinted receiving row must survive the luminance map (got ${mid / pixelCount})`,
  );
  assert.ok(
    light / pixelCount > 0.04,
    `the tinted striking mass must survive the luminance map (got ${light / pixelCount})`,
  );
  // …and the striking mass must be the BRIGHTEST thing in the tile, or custody
  // of the energy is lost the moment colour is.
  assert.ok(
    mid > light,
    'the receiving row must cover more of the tile than the striking mass, not outshine it',
  );

  const config = text('app.config.ts');
  // iOS reads the Icon Composer bundle, not the PNG appearance set: the system
  // composes the material and the Clear renditions, which PNGs cannot reach.
  assert.match(config, /icon: '\.\/assets\/Stride\.icon'/);
  assert.doesNotMatch(config, /light: '\.\/assets\/icon\.png'/, 'the PNG appearance set was superseded by the bundle');
  // The PNGs remain the fallback and the Android/web source, so they must keep
  // being generated even though iOS no longer reads them.
  assert.ok(read('assets/ios-icon-dark.png').length > 0);
  assert.ok(read('assets/ios-icon-tinted.png').length > 0);
  assert.match(config, /imageWidth: 313/);
  assert.match(config, /image: '\.\/assets\/splash-icon-dark\.png'/);
});
