/**
 * Renders the Strike approval sheets.
 *
 *   node scripts/render-strike-approval-sheet.mjs <outDir>
 *
 * Nothing here ships. This exists so a tile is judged the way it will actually
 * be seen — under the iOS squircle, at home-screen size, next to the tile it
 * replaces — rather than as a 1024px square in a file browser, which flatters
 * everything.
 */
import { mkdir, writeFile } from 'node:fs/promises';
import { createRequire } from 'node:module';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { APPEARANCES, CANVAS, strikeSvg } from './stride-strike-geometry.mjs';

const repositoryRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const requireFromSite = createRequire(resolve(repositoryRoot, 'stride-site/package.json'));
const sharp = requireFromSite('sharp');

const outDir = resolve(process.cwd(), process.argv[2] ?? 'strike-approval');
await mkdir(outDir, { recursive: true });

/**
 * The iOS icon silhouette is a continuous-corner superellipse, not a rounded
 * rect. Rendering the comp under a plain `rx` makes the corners look tighter
 * than they will be and hides how much of a mass the crop actually loses.
 */
function squirclePath(size, n = 5, steps = 240) {
  const a = size / 2;
  const points = [];
  for (let i = 0; i <= steps; i += 1) {
    const t = (i / steps) * Math.PI * 2;
    const cos = Math.cos(t);
    const sin = Math.sin(t);
    const x = a + Math.sign(cos) * a * Math.abs(cos) ** (2 / n);
    const y = a + Math.sign(sin) * a * Math.abs(sin) ** (2 / n);
    points.push(`${i === 0 ? 'M' : 'L'}${x.toFixed(2)} ${y.toFixed(2)}`);
  }
  return `${points.join(' ')} Z`;
}

async function tile(appearance, size, { masked = true, cut } = {}) {
  const flat = await sharp(Buffer.from(strikeSvg({ appearance, cut })), { density: 400 })
    .resize(size, size)
    .png()
    .toBuffer();
  if (!masked) return flat;
  const mask = Buffer.from(
    `<svg xmlns="http://www.w3.org/2000/svg" width="${size}" height="${size}"><path d="${squirclePath(size)}" fill="#fff"/></svg>`,
  );
  return sharp(flat)
    .composite([{ input: await sharp(mask, { density: 400 }).resize(size, size).png().toBuffer(), blend: 'dest-in' }])
    .png()
    .toBuffer();
}

/** The tile this replaces, drawn from the pre-lift, pre-satin geometry. */
function legacySvg() {
  return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1024 1024" width="1024" height="1024">
  <rect width="1024" height="1024" fill="#050505"/>
  <circle cx="-61.44" cy="512" r="204.8" fill="#FF5A19"/>
  <circle cx="614.4" cy="512" r="204.8" fill="#5E5E66"/>
  <circle cx="1024" cy="512" r="204.8" fill="#5E5E66"/>
</svg>`;
}

async function legacyTile(size) {
  const flat = await sharp(Buffer.from(legacySvg()), { density: 400 }).resize(size, size).png().toBuffer();
  const mask = Buffer.from(
    `<svg xmlns="http://www.w3.org/2000/svg" width="${size}" height="${size}"><path d="${squirclePath(size)}" fill="#fff"/></svg>`,
  );
  return sharp(flat)
    .composite([{ input: await sharp(mask, { density: 400 }).resize(size, size).png().toBuffer(), blend: 'dest-in' }])
    .png()
    .toBuffer();
}

const PAGE = '#1C1C1E';
const INK = '#F4F0E8';
const MUTED = '#8E8E96';

function label(text, x, y, { size = 26, weight = 700, fill = INK, anchor = 'start', spacing = 0.06 } = {}) {
  return `<text x="${x}" y="${y}" fill="${fill}" font-family="Helvetica Neue, Helvetica, Arial, sans-serif" font-size="${size}" font-weight="${weight}" letter-spacing="${spacing * size}" text-anchor="${anchor}">${text}</text>`;
}

async function sheet(name, width, height, background, layers, overlaySvg) {
  const canvas = sharp({
    create: { width, height, channels: 4, background },
  });
  const composited = await canvas.composite(layers).png().toBuffer();
  // The overlay carries explicit px width/height, so it must rasterise at 1:1.
  // Passing a density here scales the SVG past the canvas and sharp refuses the
  // composite — the failure looks like a layout bug and is not one.
  const text = await sharp(Buffer.from(overlaySvg)).resize(width, height, { fit: 'fill' }).png().toBuffer();
  const out = await sharp(composited)
    .composite([{ input: text }])
    .png({ compressionLevel: 9 })
    .toFile(resolve(outDir, name));
  return out;
}

/* ── Sheet 1: the three Apple appearances, large, under the real mask ────── */
{
  const size = 460;
  const gap = 56;
  const left = 72;
  const top = 190;
  const width = left * 2 + size * 3 + gap * 2;
  const height = top + size + 210;

  const names = ['light', 'dark', 'tinted'];
  const layers = [];
  const text = [];
  for (const [index, appearance] of names.entries()) {
    const x = left + index * (size + gap);
    layers.push({ input: await tile(appearance, size), left: x, top });
    text.push(label(appearance.toUpperCase(), x, top + size + 62, { size: 30 }));
  }
  const captions = {
    light: `field ${APPEARANCES.light.field} · row ${APPEARANCES.light.neutral}`,
    dark: `field ${APPEARANCES.dark.field} · row ${APPEARANCES.dark.neutral}`,
    tinted: 'luminance map · system re-tints',
  };
  const sub = {
    light: 'iOS light appearance',
    dark: 'iOS dark appearance — row lifted per Apple',
    tinted: 'strike stays brightest so custody still reads',
  };
  for (const [index, appearance] of names.entries()) {
    const x = left + index * (size + gap);
    text.push(label(captions[appearance], x, top + size + 100, { size: 20, weight: 400, fill: MUTED, spacing: 0 }));
    text.push(label(sub[appearance], x, top + size + 132, { size: 20, weight: 400, fill: MUTED, spacing: 0 }));
  }

  const overlay = `<svg xmlns="http://www.w3.org/2000/svg" width="${width}" height="${height}">
    ${label('THE STRIKE — APPLE APPEARANCE SET', left, 92, { size: 38 })}
    ${label('1024px masters, shown under the iOS continuous-corner mask. The lift is 0.08 of the tile; the masses carry satin depth.', left, 136, { size: 21, weight: 400, fill: MUTED, spacing: 0 })}
    ${text.join('')}
  </svg>`;

  await sheet('01-appearances.png', width, height, PAGE, layers, overlay);
}

/* ── Sheet 0: which crop? The one decision the brief does not settle ─────── */
{
  const big = 380;
  const small = 96;
  const gap = 64;
  const left = 72;
  const top = 210;
  const width = left * 2 + big * 3 + gap * 2;
  const height = top + big + 380;

  const cuts = [
    { key: 'frame', title: 'A · FRAME', note: ['shipped canon: striker centred', '0.3r outside the frame', '14% orange · 5.6px at 40px'] },
    { key: 'halved', title: 'B · HALVED  ← recommended', note: ['striker bisected by the frame.', 'same radius, twice the orange', '20% orange · 8.0px at 40px'] },
    { key: 'comp', title: 'C · COMP', note: ['measured off your comp:', 'smaller masses, pulled in', '21% orange · 8.3px at 40px'] },
  ];

  const layers = [];
  const text = [
    label('WHICH CROP?', left, 92, { size: 38 }),
    label('All three carry the same lift and the same satin. What changes is how much of the striking mass the frame keeps —', left, 136, { size: 21, weight: 400, fill: MUTED, spacing: 0 }),
    label('which is what decides whether the lift is legible at all. The small tiles are 96px; that is where it is won or lost.', left, 166, { size: 21, weight: 400, fill: MUTED, spacing: 0 }),
  ];

  for (const [index, cut] of cuts.entries()) {
    const x = left + index * (big + gap);
    layers.push({ input: await tile('dark', big, { cut: cut.key }), left: x, top });
    layers.push({ input: await tile('light', small, { cut: cut.key }), left: x, top: top + big + 34 });
    layers.push({ input: await tile('dark', small, { cut: cut.key }), left: x + small + 18, top: top + big + 34 });
    text.push(label(cut.title, x, top + big + small + 92, { size: 28 }));
    cut.note.forEach((line, lineIndex) => {
      text.push(label(line, x, top + big + small + 128 + lineIndex * 28, { size: 18, weight: 400, fill: MUTED, spacing: 0 }));
    });
  }

  const overlay = `<svg xmlns="http://www.w3.org/2000/svg" width="${width}" height="${height}">${text.join('')}</svg>`;
  await sheet('00-crops.png', width, height, PAGE, layers, overlay);
}

/* ── Sheet 2: before / after, at the sizes that actually decide it ───────── */
{
  const sizes = [180, 120, 87, 60, 40];
  const rowGap = 78;
  const left = 300;
  const top = 200;
  const cell = 200;
  const width = left + sizes.length * cell + 80;
  const height = top + 3 * (180 + rowGap) + 90;

  const layers = [];
  const text = [
    label('THE STRIKE AT HOME-SCREEN SIZE', 72, 92, { size: 38 }),
    label('Does the lift survive the crop? Does the satin survive the downsample? These are the sizes that decide it.', 72, 136, { size: 21, weight: 400, fill: MUTED, spacing: 0 }),
  ];

  const rows = [
    { key: 'legacy', title: 'BEFORE', note: 'flat, level row' },
    { key: 'dark', title: 'AFTER · DARK', note: 'lifted + satin' },
    { key: 'light', title: 'AFTER · LIGHT', note: 'putty field' },
  ];

  for (const [rowIndex, row] of rows.entries()) {
    const y = top + rowIndex * (180 + rowGap);
    text.push(label(row.title, 72, y + 96, { size: 26 }));
    text.push(label(row.note, 72, y + 128, { size: 19, weight: 400, fill: MUTED, spacing: 0 }));
    for (const [colIndex, size] of sizes.entries()) {
      const buffer = row.key === 'legacy' ? await legacyTile(size) : await tile(row.key, size);
      layers.push({
        input: buffer,
        left: left + colIndex * cell + Math.round((cell - size) / 2),
        top: y + Math.round((180 - size) / 2),
      });
      if (rowIndex === 0) {
        text.push(label(`${size}px`, left + colIndex * cell + cell / 2, top - 40, { size: 19, weight: 400, fill: MUTED, anchor: 'middle', spacing: 0 }));
      }
    }
  }

  const overlay = `<svg xmlns="http://www.w3.org/2000/svg" width="${width}" height="${height}">${text.join('')}</svg>`;
  await sheet('02-sizes.png', width, height, PAGE, layers, overlay);
}

/* ── Sheet 3: on the wallpapers the tile has to survive ─────────────────── */
{
  const size = 260;
  const gap = 40;
  const cols = 3;
  const left = 72;
  const top = 190;
  const width = left * 2 + cols * size + (cols - 1) * gap;
  const height = top + 2 * (size + 92) + 60;

  const grounds = [
    { fill: '#0B0B0D', name: 'near-black wallpaper', appearance: 'dark' },
    { fill: '#F2EFE9', name: 'paper wallpaper', appearance: 'light' },
    { fill: '#3E4C5E', name: 'slate photo', appearance: 'dark' },
    { fill: '#C7BCA9', name: 'putty wallpaper', appearance: 'light' },
    { fill: '#1E1B2E', name: 'deep violet', appearance: 'dark' },
    { fill: '#8E7F6B', name: 'warm mid', appearance: 'light' },
  ];

  const layers = [];
  const text = [
    label('THE STRIKE ON GROUND', left, 92, { size: 38 }),
    label('An icon is never seen on its own. The light tile has to hold on paper and the dark tile has to hold on black.', left, 136, { size: 21, weight: 400, fill: MUTED, spacing: 0 }),
  ];

  for (const [index, ground] of grounds.entries()) {
    const col = index % cols;
    const rowIndex = Math.floor(index / cols);
    const x = left + col * (size + gap);
    const y = top + rowIndex * (size + 92);
    const plate = await sharp({
      create: { width: size + 56, height: size + 56, channels: 4, background: ground.fill },
    }).png().toBuffer();
    layers.push({ input: plate, left: x - 28, top: y - 28 });
    layers.push({ input: await tile(ground.appearance, size), left: x, top: y });
    text.push(label(`${ground.appearance} · ${ground.name}`, x - 28, y + size + 58, { size: 19, weight: 400, fill: MUTED, spacing: 0 }));
  }

  const overlay = `<svg xmlns="http://www.w3.org/2000/svg" width="${width}" height="${height}">${text.join('')}</svg>`;
  await sheet('03-grounds.png', width, height, PAGE, layers, overlay);
}

/* ── The bare 1024 masters, for inspection at full size ─────────────────── */
for (const appearance of Object.keys(APPEARANCES)) {
  await writeFile(resolve(outDir, `master-${appearance}.svg`), strikeSvg({ appearance }));
  await sharp(Buffer.from(strikeSvg({ appearance })), { density: 400 })
    .resize(CANVAS, CANVAS)
    .png({ compressionLevel: 9 })
    .toFile(resolve(outDir, `master-${appearance}.png`));
}

console.log(`Approval sheets in ${outDir}`);
