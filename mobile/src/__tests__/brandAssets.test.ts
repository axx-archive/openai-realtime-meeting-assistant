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

function decodeRgbPng(path: string) {
  const png = read(path);
  const { width, height, colorType } = pngHeader(path);
  assert.equal(png[24], 8, `${path} must use 8-bit channels`);
  assert.equal(colorType, 2, `${path} must be an opaque RGB PNG`);
  assert.equal(png[28], 0, `${path} must not be interlaced`);

  const idat: Buffer[] = [];
  for (let offset = 8; offset < png.length; ) {
    const length = png.readUInt32BE(offset);
    const type = png.toString('ascii', offset + 4, offset + 8);
    if (type === 'IDAT') idat.push(png.subarray(offset + 8, offset + 8 + length));
    offset += 12 + length;
  }
  const encoded = inflateSync(Buffer.concat(idat));
  const stride = width * 3;
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
      const left = x >= 3 ? pixels[rowStart + x - 3] : 0;
      const above = y > 0 ? pixels[rowStart + x - stride] : 0;
      const upperLeft = y > 0 && x >= 3 ? pixels[rowStart + x - stride - 3] : 0;
      const predictor = filter === 0 ? 0
        : filter === 1 ? left
          : filter === 2 ? above
            : filter === 3 ? Math.floor((left + above) / 2)
              : filter === 4 ? paeth(left, above, upperLeft)
                : assert.fail(`${path} uses unsupported PNG filter ${filter}`);
      pixels[rowStart + x] = (raw + predictor) & 0xff;
    }
  }
  return { width, height, pixels };
}

test('approved Bonfire masters are the exact release sources', () => {
  const masterSvg = read('assets/bonfire-icon-v2.svg');
  const canonicalSvg = read('assets/icon-source.svg');
  const masterPng = read('assets/bonfire-icon-v2.png');
  const releasePng = read('assets/icon.png');
  const tintedPng = read('assets/ios-icon-tinted.png');

  assert.equal(sha256(masterSvg), 'd3800f27d5ae917390af6303fc29472f3f6623339a8df55982d63539e51bcc39');
  assert.equal(sha256(canonicalSvg), sha256(masterSvg));
  assert.equal(sha256(releasePng), sha256(masterPng));
  assert.equal(sha256(tintedPng), sha256(masterPng));
});

test('React Native full and micro marks carry the approved paths exactly', () => {
  const component = text('src/components/BrandMark.tsx');
  const microSvg = text('assets/bonfire-icon-micro.svg');
  const masterSvg = text('assets/bonfire-icon-v2.svg');

  for (const source of [masterSvg, microSvg]) {
    const paths = [...source.matchAll(/\bd="([^"]+)"/g)].map((match) => match[1]);
    assert.equal(paths.length, 3);
    for (const path of paths) assert.ok(component.includes(path), 'BrandMark path drifted from approved SVG');
  }
  assert.match(component, /<Mask id="bonfireMicroLogCutout"/);
  assert.match(component, /mask="url\(#bonfireMicroLogCutout\)"/);
  assert.doesNotMatch(component, /cutoutColor/);

  const navigation = text('src/navigation/RootNavigator.tsx');
  assert.match(navigation, /<BonfireGlyph/);
  assert.doesNotMatch(navigation, /Home:\s*'flame\.fill'/);
});

test('Expo icon and splash sources have release-safe dimensions and alpha models', () => {
  const expected = new Map<string, [number, number, number]>([
    ['assets/icon.png', [1024, 1024, 2]],
    ['assets/ios-icon-tinted.png', [1024, 1024, 2]],
    ['assets/splash-icon.png', [1024, 1024, 6]],
    ['assets/splash-icon-dark.png', [1024, 1024, 6]],
    ['assets/android-icon-foreground.png', [1024, 1024, 6]],
    ['assets/android-icon-background.png', [1024, 1024, 2]],
    ['assets/android-icon-monochrome.png', [1024, 1024, 6]],
    ['assets/favicon.png', [48, 48, 2]],
  ]);
  for (const [path, wanted] of expected) {
    const actual = pngHeader(path);
    assert.deepEqual([actual.width, actual.height, actual.colorType], wanted, path);
  }

  // iOS 18 uses this grayscale image as a luminosity map. Guard both sides of
  // that map so the tinted appearance can never silently become a blank tile.
  const tinted = decodeRgbPng('assets/ios-icon-tinted.png');
  let dark = 0;
  let light = 0;
  for (let offset = 0; offset < tinted.pixels.length; offset += 3) {
    const luminance = 0.2126 * tinted.pixels[offset]
      + 0.7152 * tinted.pixels[offset + 1]
      + 0.0722 * tinted.pixels[offset + 2];
    if (luminance < 32) dark += 1;
    if (luminance > 223) light += 1;
  }
  const pixelCount = tinted.width * tinted.height;
  assert.ok(dark / pixelCount > 0.5, 'tinted icon needs a substantial dark field');
  assert.ok(light / pixelCount > 0.15, 'tinted icon needs a substantial light Bonfire silhouette');

  const config = text('app.config.ts');
  assert.match(config, /light: '\.\/assets\/icon\.png'/);
  assert.match(config, /dark: '\.\/assets\/icon\.png'/);
  assert.match(config, /tinted: '\.\/assets\/ios-icon-tinted\.png'/);
  assert.match(config, /imageWidth: 144/);
  assert.match(config, /image: '\.\/assets\/splash-icon-dark\.png'/);
});
