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
    settings:
      base:
        ASSETCATALOG_COMPILER_APPICON_NAME: AppIcon
        CURRENT_PROJECT_VERSION: 15
        MARKETING_VERSION: 1.0
        PRODUCT_BUNDLE_IDENTIFIER: co.thebonfire.meetingassist.ios
        TARGETED_DEVICE_FAMILY: "1,2"
  MeetingAssistMacApp:
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
    writeFixtureFile(resolve(appleDir, "Xcode", "Assets.xcassets", "AppIcon.appiconset", "Contents.json"), "{}\n");
  }
  if (includePrivacy) {
    writeFixtureFile(resolve(appleDir, "Xcode", "PrivacyInfo.xcprivacy"), "{}\n");
  }
  return appleDir;
}

const defaultRepo = runReadiness();
assert.equal(defaultRepo.status, 0);
assert.equal(defaultRepo.output.ok, true);

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
