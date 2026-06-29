#!/usr/bin/env node
import assert from "node:assert/strict";
import { appendFileSync, mkdtempSync, mkdirSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { spawnSync } from "node:child_process";

const scriptsDir = dirname(fileURLToPath(import.meta.url));
const rootDir = resolve(scriptsDir, "..");
const scriptPath = resolve(scriptsDir, "native-apple-release-readiness.mjs");

function syntheticTeamId(...parts) {
  return parts.join("");
}

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

function writeSigningFixture(appleDir, localTeam = "") {
  writeFixtureFile(
    resolve(appleDir, "Config", "Signing.xcconfig"),
    `CODE_SIGN_STYLE = Automatic
DEVELOPMENT_TEAM = $(APPLE_DEVELOPMENT_TEAM)
#include? "Signing.local.xcconfig"
`
  );
  if (localTeam) {
    writeFixtureFile(resolve(appleDir, "Config", "Signing.local.xcconfig"), `DEVELOPMENT_TEAM = ${localTeam}\n`);
  }
}

function writePrivacyManifestFixture(appleDir, body = "complete") {
  if (body === "empty") {
    writeFixtureFile(resolve(appleDir, "Xcode", "PrivacyInfo.xcprivacy"), "{}\n");
    return;
  }

  if (body === "incomplete") {
    writePlist(
      resolve(appleDir, "Xcode", "PrivacyInfo.xcprivacy"),
      `<dict>
  <key>NSPrivacyTracking</key>
  <false/>
  <key>NSPrivacyTrackingDomains</key>
  <array/>
  <key>NSPrivacyAccessedAPITypes</key>
  <array/>
  <key>NSPrivacyCollectedDataTypes</key>
  <array>
    <dict>
      <key>NSPrivacyCollectedDataType</key>
      <string>NSPrivacyCollectedDataTypeName</string>
      <key>NSPrivacyCollectedDataTypePurposes</key>
      <array/>
    </dict>
  </array>
</dict>`
    );
    return;
  }

  writePlist(
    resolve(appleDir, "Xcode", "PrivacyInfo.xcprivacy"),
    `<dict>
  <key>NSPrivacyTracking</key>
  <false/>
  <key>NSPrivacyTrackingDomains</key>
  <array/>
  <key>NSPrivacyAccessedAPITypes</key>
  <array/>
  <key>NSPrivacyCollectedDataTypes</key>
  <array>
    <dict>
      <key>NSPrivacyCollectedDataType</key>
      <string>NSPrivacyCollectedDataTypeName</string>
      <key>NSPrivacyCollectedDataTypeLinked</key>
      <true/>
      <key>NSPrivacyCollectedDataTypeTracking</key>
      <false/>
      <key>NSPrivacyCollectedDataTypePurposes</key>
      <array>
        <string>NSPrivacyCollectedDataTypePurposeAppFunctionality</string>
      </array>
    </dict>
  </array>
</dict>`
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

function makeFixture({ includeIcons, includePrivacy, localTeam = "" }) {
  const dir = mkdtempSync(resolve(tmpdir(), "meetingassist-apple-release-"));
  const appleDir = resolve(dir, "apple");
  mkdirSync(resolve(appleDir, "MeetingAssist.xcodeproj"), { recursive: true });

  writeFixtureFile(
    resolve(appleDir, "project.yml"),
    `targets:
  MeetingAssistAppleApp:
    configFiles:
      Debug: Config/Signing.xcconfig
      Release: Config/Signing.xcconfig
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
    configFiles:
      Debug: Config/Signing.xcconfig
      Release: Config/Signing.xcconfig
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
Signing.xcconfig;
`
  );
  writeSigningFixture(appleDir, localTeam);
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
    writePrivacyManifestFixture(appleDir, includePrivacy);
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
  DEVELOPMENT_TEAM: syntheticTeamId("A1", "B2", "C3", "D4", "E5"),
});
assert.equal(readyFixture.status, 0);
assert.equal(readyFixture.output.ok, true);
assert.equal(readyFixture.output.readyForDistribution, true);
assert.deepEqual(readyFixture.output.blockers, []);

const localTeamFixturePath = makeFixture({
  includeIcons: true,
  includePrivacy: true,
  localTeam: syntheticTeamId("B1", "C2", "D3", "E4", "F5"),
});
const localTeamFixture = runReadiness(["--apple-dir", localTeamFixturePath, "--strict"]);
assert.equal(localTeamFixture.status, 0);
assert.equal(localTeamFixture.output.ok, true);
assert.equal(localTeamFixture.output.readyForDistribution, true);
assert.deepEqual(localTeamFixture.output.blockers, []);

const emptyPrivacyFixturePath = makeFixture({ includeIcons: true, includePrivacy: "empty" });
const emptyPrivacyFixture = runReadiness(["--apple-dir", emptyPrivacyFixturePath, "--strict"], {
  DEVELOPMENT_TEAM: syntheticTeamId("A1", "B2", "C3", "D4", "E5"),
});
assert.equal(emptyPrivacyFixture.status, 1);
assert.equal(emptyPrivacyFixture.output.ok, true);
assert.equal(emptyPrivacyFixture.output.readyForDistribution, false);
assert.equal(emptyPrivacyFixture.output.blockers.some((blocker) => blocker.id === "privacy_manifest"), true);

const incompletePrivacyFixturePath = makeFixture({ includeIcons: true, includePrivacy: "incomplete" });
const incompletePrivacyFixture = runReadiness(["--apple-dir", incompletePrivacyFixturePath, "--strict"], {
  DEVELOPMENT_TEAM: syntheticTeamId("A1", "B2", "C3", "D4", "E5"),
});
assert.equal(incompletePrivacyFixture.status, 1);
assert.equal(incompletePrivacyFixture.output.ok, true);
assert.equal(incompletePrivacyFixture.output.readyForDistribution, false);
assert.equal(incompletePrivacyFixture.output.blockers.some((blocker) => blocker.id === "privacy_manifest"), true);

const committedTeamFixturePath = makeFixture({ includeIcons: true, includePrivacy: true });
appendFileSync(
  resolve(committedTeamFixturePath, "MeetingAssist.xcodeproj", "project.pbxproj"),
  `DEVELOPMENT_TEAM = ${syntheticTeamId("C1", "D2", "E3", "F4", "G5")};\n`
);
const committedTeamFixture = runReadiness(["--apple-dir", committedTeamFixturePath], {
  DEVELOPMENT_TEAM: syntheticTeamId("A1", "B2", "C3", "D4", "E5"),
});
assert.equal(committedTeamFixture.status, 1);
assert.equal(committedTeamFixture.output.ok, false);
assert.equal(
  committedTeamFixture.output.checks.some((check) => check.id === "no_committed_development_team" && !check.ok),
  true
);

const committedXcodeTeamFixturePath = makeFixture({ includeIcons: true, includePrivacy: true });
appendFileSync(
  resolve(committedXcodeTeamFixturePath, "MeetingAssist.xcodeproj", "project.pbxproj"),
  `DevelopmentTeam = ${syntheticTeamId("D1", "E2", "F3", "G4", "H5")};\n`
);
const committedXcodeTeamFixture = runReadiness(["--apple-dir", committedXcodeTeamFixturePath], {
  DEVELOPMENT_TEAM: syntheticTeamId("A1", "B2", "C3", "D4", "E5"),
});
assert.equal(committedXcodeTeamFixture.status, 1);
assert.equal(committedXcodeTeamFixture.output.ok, false);
assert.equal(
  committedXcodeTeamFixture.output.checks.some((check) => check.id === "no_committed_development_team" && !check.ok),
  true
);

const committedXcconfigTeamFixturePath = makeFixture({ includeIcons: true, includePrivacy: true });
writeSigningFixture(committedXcconfigTeamFixturePath, "");
writeFixtureFile(
  resolve(committedXcconfigTeamFixturePath, "Config", "Signing.xcconfig"),
  `CODE_SIGN_STYLE = Automatic
DEVELOPMENT_TEAM = ${syntheticTeamId("E1", "F2", "G3", "H4", "J5")}
DEVELOPMENT_TEAM = $(APPLE_DEVELOPMENT_TEAM)
#include? "Signing.local.xcconfig"
`
);
const committedXcconfigTeamFixture = runReadiness(["--apple-dir", committedXcconfigTeamFixturePath], {
  DEVELOPMENT_TEAM: syntheticTeamId("A1", "B2", "C3", "D4", "E5"),
});
assert.equal(committedXcconfigTeamFixture.status, 1);
assert.equal(committedXcconfigTeamFixture.output.ok, false);
assert.equal(
  committedXcconfigTeamFixture.output.checks.some(
    (check) => check.id === "no_committed_development_team" && !check.ok
  ),
  true
);

const committedXcconfigTrailingTeamFixturePath = makeFixture({ includeIcons: true, includePrivacy: true });
appendFileSync(
  resolve(committedXcconfigTrailingTeamFixturePath, "Config", "Signing.xcconfig"),
  `DEVELOPMENT_TEAM = ${syntheticTeamId("F1", "G2", "H3", "J4", "K5")}\n`
);
const committedXcconfigTrailingTeamFixture = runReadiness(
  ["--apple-dir", committedXcconfigTrailingTeamFixturePath],
  { DEVELOPMENT_TEAM: syntheticTeamId("A1", "B2", "C3", "D4", "E5") }
);
assert.equal(committedXcconfigTrailingTeamFixture.status, 1);
assert.equal(committedXcconfigTrailingTeamFixture.output.ok, false);
assert.equal(
  committedXcconfigTrailingTeamFixture.output.checks.some(
    (check) => check.id === "no_committed_development_team" && !check.ok
  ),
  true
);

console.log("native-apple-release-readiness: 10 checks passed");
