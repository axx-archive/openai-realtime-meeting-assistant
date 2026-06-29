#!/usr/bin/env node
import assert from "node:assert/strict";
import { mkdtempSync, mkdirSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { spawnSync } from "node:child_process";

const scriptsDir = dirname(fileURLToPath(import.meta.url));
const rootDir = resolve(scriptsDir, "..");
const scriptPath = resolve(scriptsDir, "native-apple-release-readiness.mjs");

function runReadiness(args = [], env = {}) {
  const result = spawnSync(process.execPath, [scriptPath, ...args], {
    cwd: rootDir,
    encoding: "utf8",
    env: {
      ...process.env,
      DEVELOPMENT_TEAM: "",
      APPLE_DEVELOPMENT_TEAM: "",
      ...env,
    },
  });

  let output;
  try {
    output = JSON.parse(result.stdout || result.stderr);
  } catch (error) {
    throw new Error(
      `Could not parse release readiness output.\nstatus=${result.status}\nstdout=${result.stdout}\nstderr=${result.stderr}\n${error}`
    );
  }

  return { status: result.status, output };
}

function writeFixtureFile(path, contents) {
  mkdirSync(dirname(path), { recursive: true });
  writeFileSync(path, contents);
}

const appIconSlots = [
  ["iphone", "20x20", "2x"],
  ["iphone", "20x20", "3x"],
  ["iphone", "29x29", "2x"],
  ["iphone", "29x29", "3x"],
  ["iphone", "40x40", "2x"],
  ["iphone", "40x40", "3x"],
  ["iphone", "60x60", "2x"],
  ["iphone", "60x60", "3x"],
  ["ipad", "20x20", "1x"],
  ["ipad", "20x20", "2x"],
  ["ipad", "29x29", "1x"],
  ["ipad", "29x29", "2x"],
  ["ipad", "40x40", "1x"],
  ["ipad", "40x40", "2x"],
  ["ipad", "76x76", "1x"],
  ["ipad", "76x76", "2x"],
  ["ipad", "83.5x83.5", "2x"],
  ["ios-marketing", "1024x1024", "1x"],
  ["mac", "16x16", "1x"],
  ["mac", "16x16", "2x"],
  ["mac", "32x32", "1x"],
  ["mac", "32x32", "2x"],
  ["mac", "128x128", "1x"],
  ["mac", "128x128", "2x"],
  ["mac", "256x256", "1x"],
  ["mac", "256x256", "2x"],
  ["mac", "512x512", "1x"],
  ["mac", "512x512", "2x"],
];

function pixelsForSlot(size, scale) {
  return Math.round(Number(size.split("x")[0]) * Number(scale.replace("x", "")));
}

function pngWithDimensions(pixels) {
  const png = Buffer.alloc(33);
  Buffer.from("89504e470d0a1a0a", "hex").copy(png, 0);
  png.writeUInt32BE(13, 8);
  png.write("IHDR", 12, "ascii");
  png.writeUInt32BE(pixels, 16);
  png.writeUInt32BE(pixels, 20);
  png[24] = 8;
  png[25] = 6;
  return png;
}

function writeAppIconFixture(appleDir) {
  const iconSetDir = resolve(appleDir, "Xcode", "Assets.xcassets", "AppIcon.appiconset");
  writeFixtureFile(
    resolve(appleDir, "Xcode", "Assets.xcassets", "Contents.json"),
    `${JSON.stringify({ info: { author: "xcode", version: 1 } }, null, 2)}\n`
  );
  const images = appIconSlots.map(([idiom, size, scale]) => {
    const filename = `AppIcon-${idiom}-${size.replaceAll(".", "_")}@${scale}.png`;
    writeFixtureFile(resolve(iconSetDir, filename), pngWithDimensions(pixelsForSlot(size, scale)));
    return { idiom, size, scale, filename };
  });
  writeFixtureFile(
    resolve(iconSetDir, "Contents.json"),
    `${JSON.stringify({ images, info: { author: "xcode", version: 1 } }, null, 2)}\n`
  );
}

function writePlist(path, body) {
  writeFixtureFile(
    path,
    `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
${body}
</plist>
`
  );
}

function makeFixture({ includeIcons, includePrivacy }) {
  const dir = mkdtempSync(resolve(tmpdir(), "meetingassist-apple-release-"));
  const appleDir = resolve(dir, "apple");
  mkdirSync(resolve(appleDir, "MeetingAssist.xcodeproj"), { recursive: true });

  writeFixtureFile(
    resolve(appleDir, "project.yml"),
    `targets:
  MeetingAssistAppleApp:
    sources:
      - path: Xcode/MeetingAssistAppleApp.swift
      - path: Xcode/Assets.xcassets
    settings:
      base:
        ASSETCATALOG_COMPILER_APPICON_NAME: AppIcon
        CURRENT_PROJECT_VERSION: 15
        MARKETING_VERSION: 1.0
        PRODUCT_BUNDLE_IDENTIFIER: co.thebonfire.meetingassist.ios
        TARGETED_DEVICE_FAMILY: "1,2"
  MeetingAssistMacApp:
    sources:
      - path: Xcode/MeetingAssistMacApp.swift
      - path: Xcode/Assets.xcassets
    settings:
      base:
        CODE_SIGN_ENTITLEMENTS: Xcode/MeetingAssistMacApp.entitlements
        CURRENT_PROJECT_VERSION: 15
        ENABLE_HARDENED_RUNTIME: YES
        MARKETING_VERSION: 1.0
        PRODUCT_BUNDLE_IDENTIFIER: co.thebonfire.meetingassist.mac
`
  );
  writeFixtureFile(
    resolve(appleDir, "MeetingAssist.xcodeproj", "project.pbxproj"),
    `PRODUCT_BUNDLE_IDENTIFIER = co.thebonfire.meetingassist.ios;
PRODUCT_BUNDLE_IDENTIFIER = co.thebonfire.meetingassist.mac;
CODE_SIGN_ENTITLEMENTS = Xcode/MeetingAssistMacApp.entitlements;
ENABLE_HARDENED_RUNTIME = YES;
MARKETING_VERSION = 1.0;
CURRENT_PROJECT_VERSION = 15;
Assets.xcassets in Resources;
ASSETCATALOG_COMPILER_APPICON_NAME = AppIcon;
ASSETCATALOG_COMPILER_APPICON_NAME = AppIcon;
`
  );
  const infoBody = `<dict>
  <key>CFBundleShortVersionString</key>
  <string>$(MARKETING_VERSION)</string>
  <key>CFBundleVersion</key>
  <string>$(CURRENT_PROJECT_VERSION)</string>
  <key>NSCameraUsageDescription</key>
  <string>MeetingAssist uses the camera when you join a video room.</string>
  <key>NSMicrophoneUsageDescription</key>
  <string>MeetingAssist uses the microphone when you join a video room.</string>
</dict>`;
  writePlist(resolve(appleDir, "Xcode", "MeetingAssistAppleApp-Info.plist"), infoBody);
  writePlist(resolve(appleDir, "Xcode", "MeetingAssistMacApp-Info.plist"), infoBody);
  writePlist(
    resolve(appleDir, "Xcode", "MeetingAssistMacApp.entitlements"),
    `<dict>
  <key>com.apple.security.device.audio-input</key>
  <true/>
  <key>com.apple.security.device.camera</key>
  <true/>
</dict>`
  );
  if (includeIcons) {
    writeAppIconFixture(appleDir);
  }
  if (includePrivacy) {
    writeFixtureFile(resolve(appleDir, "Xcode", "PrivacyInfo.xcprivacy"), "{}\n");
  }
  return appleDir;
}

const defaultRepo = runReadiness();
assert.equal(defaultRepo.status, 0);
assert.equal(defaultRepo.output.ok, true);
assert.equal(defaultRepo.output.blockers.some((blocker) => blocker.id === "ios_app_icon"), false);
assert.equal(defaultRepo.output.blockers.some((blocker) => blocker.id === "mac_app_icon"), false);

const blockedFixturePath = makeFixture({ includeIcons: false, includePrivacy: false });
const blockedFixture = runReadiness(["--apple-dir", blockedFixturePath]);
assert.equal(blockedFixture.status, 0);
assert.equal(blockedFixture.output.ok, true);
assert.equal(blockedFixture.output.readyForDistribution, false);
assert.deepEqual(
  blockedFixture.output.blockers.map((blocker) => blocker.id).sort(),
  ["apple_development_team", "ios_app_icon", "mac_app_icon", "privacy_manifest"]
);

const strictBlockedFixture = runReadiness(["--apple-dir", blockedFixturePath, "--strict"]);
assert.equal(strictBlockedFixture.status, 1);
assert.equal(strictBlockedFixture.output.ok, true);
assert.equal(strictBlockedFixture.output.readyForDistribution, false);

const readyFixturePath = makeFixture({ includeIcons: true, includePrivacy: true });
const readyFixture = runReadiness(["--apple-dir", readyFixturePath, "--strict"], {
  DEVELOPMENT_TEAM: "ABCDE12345",
});
assert.equal(readyFixture.status, 0);
assert.equal(readyFixture.output.ok, true);
assert.equal(readyFixture.output.readyForDistribution, true);
assert.deepEqual(readyFixture.output.blockers, []);

console.log("native-apple-release-readiness: 3 checks passed");
