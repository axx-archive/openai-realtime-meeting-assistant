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

test('approved Stride Signal master is the exact release source', () => {
  const canonicalSvg = read('assets/icon-source.svg');
  const masterPng = read('assets/stride-signal-master.png');
  const releasePng = read('assets/icon.png');
  const darkPng = read('assets/ios-icon-dark.png');
  const tintedPng = read('assets/ios-icon-tinted.png');

  // Regenerate with `node scripts/generate-stride-brand-assets.mjs` and update
  // these digests in the same commit. The SVG is a print of
  // `scripts/stride-signal-geometry.mjs`, never hand-edited.
  assert.equal(sha256(canonicalSvg), '0a153994ed0b746f5be5446126b872990ca426be610c12a7d1d60a6fba71fa2f');
  assert.match(canonicalSvg.toString('utf8'), /<title>Stride Signal<\/title>/);
  assert.equal(sha256(masterPng), '7fde78fe45760658fd7f8e434b163b824ded954ef163bbbdcbaf68366ec61961');
  assert.equal(sha256(releasePng), sha256(masterPng));
  // The dark-appearance icon is no longer a copy of the master. The primary tile
  // is INVERTED (orange ground, aperture cut out in ink), so the sanctioned
  // dark-ground alternate does real work as the iOS dark icon rather than being
  // a duplicate.
  assert.notEqual(sha256(darkPng), sha256(masterPng));
  assert.equal(sha256(darkPng), '9a9c0800f3f255d6c90dc9184dfdb530fff99e5d8fd7f3ba62855477f00a073e');
  assert.equal(sha256(tintedPng), 'db4569211ed0faa8bd38a1499f526c33b1a3aff1df913f9821f95f552ee36aa3');
});

test('React Native shell uses the approved Stride Signal assets everywhere', () => {
  const component = text('src/components/BrandMark.tsx');
  assert.match(component, /from 'expo-image'/);
  assert.match(component, /stride-signal-mark\.png/);
  assert.doesNotMatch(component, /android-icon-monochrome\.png/);
  assert.doesNotMatch(component, /fullFlamePath|microFlamePath|bonfireMicroLogCutout/);

  // The voice-first shell uses the bare signal at all three interaction scales:
  // a quiet Scout label, the real-amplitude centrepiece, and the bottom talk
  // control. Boxed tiles belong to launchers, never in-product chrome.
  const canvas = text('src/screens/CanvasScreen.tsx');
  assert.match(canvas, /<StridePulse trace=\{dictation\.trace\} listening=\{false\} size=\{22\}/);
  assert.match(canvas, /<StridePulse trace=\{dictation\.trace\} listening=\{listening\}/);

  // The bottom control is the second explicit path into the same voice loop.
  const dock = text('src/components/Dock.tsx');
  assert.match(dock, /<StridePulse trace=\{trace\} listening=\{listening\} size=\{28\}/);
  assert.doesNotMatch(dock, /Type instead|onKeyboard|keyboard/);

  // The centrepiece is the APERTURE, drawn from the shared geometry — not the
  // sliced disc the founder rejected, and not a hand-drawn near-copy of the mark.
  const pulse = text('src/components/StridePulse.tsx');
  assert.match(pulse, /from 'react-native-svg'/);
  assert.match(pulse, /aperturePathData/);
  assert.doesNotMatch(pulse, /STRIDE_BANDS|strideOffset/);

  const navigation = text('src/navigation/RootNavigator.tsx');
  assert.doesNotMatch(navigation, /Home:\s*'flame\.fill'/);
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
    ['assets/stride-signal-mark.png', [1024, 1024, 2]],
    ['assets/favicon.png', [48, 48, 2]],
  ]);
  for (const [path, wanted] of expected) {
    const actual = pngHeader(path);
    assert.deepEqual([actual.width, actual.height, actual.colorType], wanted, path);
  }

  // iOS 18 uses this grayscale image as a luminosity map. Guard both sides of
  // that map so the tinted appearance can never silently become a blank tile.
  const tinted = decodeOpaquePng('assets/ios-icon-tinted.png');
  let dark = 0;
  let light = 0;
  for (let offset = 0; offset < tinted.pixels.length; offset += tinted.channels) {
    const luminance = tinted.channels === 1
      ? tinted.pixels[offset]
      : 0.2126 * tinted.pixels[offset]
        + 0.7152 * tinted.pixels[offset + 1]
        + 0.0722 * tinted.pixels[offset + 2];
    if (luminance < 32) dark += 1;
    if (luminance > 160) light += 1;
  }
  const pixelCount = tinted.width * tinted.height;
  assert.ok(dark / pixelCount > 0.5, 'tinted icon needs a substantial dark field');
  // The mark is an APERTURE — a thin lens — so its light area is a few percent
  // of the tile, not the double-digit share a filled disc gave. Measured at
  // 3.6%; the floor is 2% so the test is not brittle, while a blank or
  // all-dark tile (0%) still fails, which is the regression this exists for.
  assert.ok(
    light / pixelCount > 0.02,
    `tinted icon needs a substantial light Stride Signal (got ${light / pixelCount})`,
  );

  const config = text('app.config.ts');
  assert.match(config, /light: '\.\/assets\/icon\.png'/);
  assert.match(config, /dark: '\.\/assets\/ios-icon-dark\.png'/);
  assert.match(config, /tinted: '\.\/assets\/ios-icon-tinted\.png'/);
  assert.match(config, /imageWidth: 144/);
  assert.match(config, /image: '\.\/assets\/splash-icon-dark\.png'/);
});
