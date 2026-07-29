import { copyFile, mkdir, readFile } from 'node:fs/promises';
import { createRequire } from 'node:module';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const repositoryRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const requireFromSite = createRequire(resolve(repositoryRoot, 'stride-site/package.json'));
const sharp = requireFromSite('sharp');

const sourcePath = resolve(repositoryRoot, 'mobile/assets/icon-source.svg');
const source = await readFile(sourcePath, 'utf8');

const transparent = source.replace(
  '<rect id="background" width="1024" height="1024" fill="#050505"/>',
  '',
);
const monochrome = transparent.replaceAll('#FF5A19', '#FFFFFF');
const tinted = source.replaceAll('#FF5A19', '#FFFFFF');

async function render(svg, outputPath, width, height, options = {}) {
  await mkdir(dirname(outputPath), { recursive: true });
  let pipeline = sharp(Buffer.from(svg)).resize(width, height, {
    fit: 'fill',
    kernel: sharp.kernel.lanczos3,
  });
  if (options.opaque) {
    pipeline = pipeline.flatten({ background: options.background ?? '#050505' });
  }
  await pipeline.png({ compressionLevel: 9, adaptiveFiltering: true }).toFile(outputPath);
}

async function solid(outputPath, width, height, color) {
  await sharp({
    create: {
      width,
      height,
      channels: 3,
      background: color,
    },
  }).png({ compressionLevel: 9 }).toFile(outputPath);
}

const publicDir = resolve(repositoryRoot, 'public');
await render(source, resolve(publicDir, 'app-icon.png'), 512, 512, { opaque: true });
await render(source, resolve(publicDir, 'apple-touch-icon.png'), 180, 180, { opaque: true });
await render(source, resolve(publicDir, 'favicon.png'), 64, 64, { opaque: true });
await render(source, resolve(publicDir, 'icon-192.png'), 192, 192, { opaque: true });
await render(source, resolve(publicDir, 'icon-512.png'), 512, 512, { opaque: true });
await render(source, resolve(publicDir, 'icon-maskable-512.png'), 512, 512, { opaque: true });

const mobileDir = resolve(repositoryRoot, 'mobile/assets');
await render(source, resolve(mobileDir, 'stride-signal-master.png'), 1024, 1024, { opaque: true });
await render(source, resolve(mobileDir, 'icon.png'), 1024, 1024, { opaque: true });
await render(source, resolve(mobileDir, 'ios-icon-dark.png'), 1024, 1024, { opaque: true });
await render(tinted, resolve(mobileDir, 'ios-icon-tinted.png'), 1024, 1024, { opaque: true });
await render(transparent, resolve(mobileDir, 'splash-icon.png'), 1024, 1024);
await render(monochrome, resolve(mobileDir, 'splash-icon-dark.png'), 1024, 1024);
await render(transparent, resolve(mobileDir, 'android-icon-foreground.png'), 1024, 1024);
await render(monochrome, resolve(mobileDir, 'android-icon-monochrome.png'), 1024, 1024);
await solid(resolve(mobileDir, 'android-icon-background.png'), 1024, 1024, '#050505');
await render(source, resolve(mobileDir, 'adaptive-icon.png'), 512, 512, { opaque: true });
await render(source, resolve(mobileDir, 'stride-signal-mark.png'), 1024, 1024, { opaque: true });
await render(source, resolve(mobileDir, 'favicon.png'), 48, 48, { opaque: true });

await copyFile(
  resolve(publicDir, 'favicon.png'),
  resolve(repositoryRoot, 'stride-site/public/favicon.png'),
);
