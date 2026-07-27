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

test('approved STRIDE momentum master is the exact release source', () => {
  const canonicalSvg = read('assets/icon-source.svg');
  const masterPng = read('assets/bonfire-stride-master.png');
  const releasePng = read('assets/icon.png');
  const darkPng = read('assets/ios-icon-dark.png');
  const tintedPng = read('assets/ios-icon-tinted.png');

  assert.equal(sha256(canonicalSvg), '9257a122ee8d5964753853fc74f98d43d45ad974f6e85e5388b554d466ad7707');
  assert.match(canonicalSvg.toString('utf8'), /bonfire-stride-master\.png/);
  assert.equal(sha256(masterPng), 'e2ff95e0396c7cc1bae147530f8c2414440c2069115b5836eb72588042159bde');
  assert.equal(sha256(releasePng), sha256(masterPng));
  assert.equal(sha256(darkPng), sha256(masterPng));
  assert.equal(sha256(tintedPng), '81af6fac3171fb2b480c5f7652f3aeceaf91f9351e0b1dbc7c5f2169ea465541');
});

test('React Native shell uses the approved momentum assets everywhere', () => {
  const component = text('src/components/BrandMark.tsx');
  assert.match(component, /from 'expo-image'/);
  assert.match(component, /bonfire-stride-mark\.png/);
  assert.match(component, /android-icon-monochrome\.png/);
  assert.match(component, /export function MomentumGlyph/);
  assert.doesNotMatch(component, /fullFlamePath|microFlamePath|bonfireMicroLogCutout/);

  const navigation = text('src/navigation/RootNavigator.tsx');
  assert.match(navigation, /<MomentumGlyph/);
  assert.doesNotMatch(navigation, /BonfireGlyph/);
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
    ['assets/bonfire-stride-mark.png', [1024, 1024, 6]],
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
  // The textured mark deliberately carries midtone ember detail instead of a
  // flat white glyph. Keep enough near-white core for the system tint to read
  // while allowing the grayscale relief to survive.
  assert.ok(
    light / pixelCount > 0.08,
    `tinted icon needs a substantial light Bonfire core (got ${light / pixelCount})`,
  );

  const config = text('app.config.ts');
  assert.match(config, /light: '\.\/assets\/icon\.png'/);
  assert.match(config, /dark: '\.\/assets\/ios-icon-dark\.png'/);
  assert.match(config, /tinted: '\.\/assets\/ios-icon-tinted\.png'/);
  assert.match(config, /imageWidth: 144/);
  assert.match(config, /image: '\.\/assets\/splash-icon-dark\.png'/);
});
