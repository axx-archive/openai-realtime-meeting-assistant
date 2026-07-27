#!/usr/bin/env node
import { execFileSync, spawnSync } from "node:child_process";
import { existsSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const rootDir = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const appleDir = join(rootDir, "apple");
const sourceSVG = join(appleDir, "Xcode", "AppIconSource.svg");
const macSourceSVG = join(appleDir, "Xcode", "AppIconMacSource.svg");
const canonicalIOSPNG = join(rootDir, "mobile", "assets", "bonfire-stride-master.png");
const assetCatalogDir = join(appleDir, "Xcode", "Assets.xcassets");
const iconSetDir = join(appleDir, "Xcode", "Assets.xcassets", "AppIcon.appiconset");
const materializedMacSource = join(iconSetDir, ".AppIconMacSource.render.svg");

const slots = [
  ["iphone", "20x20", "2x", 40],
  ["iphone", "20x20", "3x", 60],
  ["iphone", "29x29", "2x", 58],
  ["iphone", "29x29", "3x", 87],
  ["iphone", "40x40", "2x", 80],
  ["iphone", "40x40", "3x", 120],
  ["iphone", "60x60", "2x", 120],
  ["iphone", "60x60", "3x", 180],
  ["ipad", "20x20", "1x", 20],
  ["ipad", "20x20", "2x", 40],
  ["ipad", "29x29", "1x", 29],
  ["ipad", "29x29", "2x", 58],
  ["ipad", "40x40", "1x", 40],
  ["ipad", "40x40", "2x", 80],
  ["ipad", "76x76", "1x", 76],
  ["ipad", "76x76", "2x", 152],
  ["ipad", "83.5x83.5", "2x", 167],
  ["ios-marketing", "1024x1024", "1x", 1024],
  ["mac", "16x16", "1x", 16],
  ["mac", "16x16", "2x", 32],
  ["mac", "32x32", "1x", 32],
  ["mac", "32x32", "2x", 64],
  ["mac", "128x128", "1x", 128],
  ["mac", "128x128", "2x", 256],
  ["mac", "256x256", "1x", 256],
  ["mac", "256x256", "2x", 512],
  ["mac", "512x512", "1x", 512],
  ["mac", "512x512", "2x", 1024],
];

for (const source of [sourceSVG, macSourceSVG, canonicalIOSPNG]) {
  if (!existsSync(source)) {
    throw new Error(`Missing icon source: ${source}`);
  }
}

function commandAvailable(command, args) {
  const result = spawnSync(command, args, { stdio: "ignore" });
  return !result.error && result.status === 0;
}

const renderer = commandAvailable("rsvg-convert", ["--version"])
  ? "rsvg-convert"
  : commandAvailable("sips", ["--version"])
    ? "sips"
    : null;

if (!renderer) {
  throw new Error("App icon generation requires rsvg-convert or macOS sips.");
}

function pngHasAlpha(path) {
  const png = readFileSync(path);
  if (png.length < 33 || png.toString("ascii", 1, 4) !== "PNG") {
    throw new Error(`Generated icon is not a PNG: ${path}`);
  }
  const colorType = png[25];
  return colorType === 4 || colorType === 6 || png.includes(Buffer.from("tRNS"));
}

function renderIcon({ idiom, pixels, output }) {
  // Preserve the exact approved RGB master for iOS. The macOS companion needs
  // an inset transparent tile, so only that idiom goes through the SVG source.
  if (idiom !== "mac") {
    execFileSync("sips", [
      "-z", String(pixels), String(pixels),
      "-s", "format", "png",
      canonicalIOSPNG,
      "--out", output,
    ], { stdio: "ignore" });
    return;
  }

  const svg = materializedMacSource;
  if (renderer === "rsvg-convert") {
    const args = [
      "--width", String(pixels),
      "--height", String(pixels),
    ];
    args.push("--output", output, svg);
    execFileSync(renderer, args);
    return;
  }

  // sips preserves the RGB color model of the approved opaque iOS PNG and
  // preserves the alpha channel when rasterizing the inset macOS SVG.
  const input = macSourceSVG;
  execFileSync(renderer, [
    "-z", String(pixels), String(pixels),
    "-s", "format", "png",
    input,
    "--out", output,
  ], { stdio: "ignore" });
}

rmSync(iconSetDir, { recursive: true, force: true });
mkdirSync(iconSetDir, { recursive: true });
const macTemplate = readFileSync(macSourceSVG, "utf8");
const momentumDataURI = `data:image/png;base64,${readFileSync(canonicalIOSPNG).toString("base64")}`;
if (!macTemplate.includes("__MOMENTUM_MASTER_DATA_URI__")) {
  throw new Error("macOS icon template is missing the momentum master placeholder.");
}
writeFileSync(
  materializedMacSource,
  macTemplate.replace("__MOMENTUM_MASTER_DATA_URI__", momentumDataURI),
);
writeFileSync(
  join(assetCatalogDir, "Contents.json"),
  `${JSON.stringify({ info: { author: "xcode", version: 1 } }, null, 2)}\n`
);
for (const stale of ["Contents.json"]) {
  rmSync(join(iconSetDir, stale), { force: true });
}

const images = [];
for (const [idiom, size, scale, pixels] of slots) {
  const filename = `AppIcon-${idiom}-${size.replaceAll(".", "_")}@${scale}.png`;
  const output = join(iconSetDir, filename);
  renderIcon({ idiom, pixels, output });
  const hasAlpha = pngHasAlpha(output);
  if (idiom === "mac" ? !hasAlpha : hasAlpha) {
    throw new Error(`${filename} has an invalid alpha model for ${idiom}.`);
  }
  images.push({ idiom, size, scale, filename });
}
rmSync(materializedMacSource, { force: true });

writeFileSync(
  join(iconSetDir, "Contents.json"),
  `${JSON.stringify({ images, info: { author: "xcode", version: 1 } }, null, 2)}\n`
);

console.log(`Generated ${images.length} app icon images with ${renderer} in ${iconSetDir}`);
