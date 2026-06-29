#!/usr/bin/env node
import { execFileSync } from "node:child_process";
import { existsSync, mkdirSync, rmSync, writeFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const rootDir = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const appleDir = join(rootDir, "apple");
const sourceSVG = join(appleDir, "Xcode", "AppIconSource.svg");
const assetCatalogDir = join(appleDir, "Xcode", "Assets.xcassets");
const iconSetDir = join(appleDir, "Xcode", "Assets.xcassets", "AppIcon.appiconset");

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

if (!existsSync(sourceSVG)) {
  throw new Error(`Missing icon source SVG: ${sourceSVG}`);
}

rmSync(iconSetDir, { recursive: true, force: true });
mkdirSync(iconSetDir, { recursive: true });
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
  execFileSync("rsvg-convert", [
    "--width", String(pixels),
    "--height", String(pixels),
    "--output", join(iconSetDir, filename),
    sourceSVG,
  ]);
  images.push({ idiom, size, scale, filename });
}

writeFileSync(
  join(iconSetDir, "Contents.json"),
  `${JSON.stringify({ images, info: { author: "xcode", version: 1 } }, null, 2)}\n`
);

console.log(`Generated ${images.length} app icon images in ${iconSetDir}`);
