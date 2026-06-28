#!/usr/bin/env node
import { execFileSync } from "node:child_process";
import { existsSync, readdirSync, readFileSync } from "node:fs";
import { join, resolve } from "node:path";

function usage() {
  return [
    "Usage:",
    "  node scripts/native-apple-release-readiness.mjs [--apple-dir apple] [--strict]",
    "",
    "Default mode exits nonzero only for broken repo prerequisites.",
    "--strict also exits nonzero for external distribution blockers such as missing",
    "Apple team configuration, app icons, or privacy manifest metadata.",
  ].join("\n");
}

function parseArgs(argv) {
  const args = {
    appleDir: "apple",
    strict: false,
    help: false,
  };

  for (let index = 0; index < argv.length; index += 1) {
    const arg = argv[index];
    if (arg === "--apple-dir") {
      args.appleDir = argv[index + 1] ?? "";
      index += 1;
    } else if (arg === "--strict") {
      args.strict = true;
    } else if (arg === "--help" || arg === "-h") {
      args.help = true;
    } else {
      throw new Error(`Unknown argument: ${arg}`);
    }
  }

  if (!args.appleDir) {
    throw new Error("--apple-dir requires a path.");
  }

  return args;
}

function readText(path) {
  return readFileSync(path, "utf8");
}

function parsePlist(path) {
  return JSON.parse(execFileSync("plutil", ["-convert", "json", "-o", "-", path], { encoding: "utf8" }));
}

function textHas(text, pattern) {
  return pattern.test(text);
}

function boolPlistValue(plist, key) {
  return plist[key] === true;
}

function nonEmptyPlistString(plist, key) {
  return typeof plist[key] === "string" && plist[key].trim() !== "";
}

function walk(dir, visit) {
  if (!existsSync(dir)) {
    return;
  }

  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const path = join(dir, entry.name);
    if (entry.isDirectory()) {
      visit(path);
      walk(path, visit);
    }
  }
}

function hasAppIconSet(appleDir, iconName) {
  let found = false;
  walk(appleDir, (path) => {
    if (path.endsWith(`${iconName}.appiconset`) && existsSync(join(path, "Contents.json"))) {
      found = true;
    }
  });
  return found;
}

function extractSetting(text, key, assignmentPattern) {
  const yamlMatch = new RegExp(`${key}:\\s*([^\\n]+)`).exec(text);
  if (yamlMatch) {
    return yamlMatch[1].trim().replace(/^["']|["']$/g, "");
  }
  const pbxMatch = assignmentPattern.exec(text);
  if (pbxMatch) {
    return pbxMatch[1].trim().replace(/^"|"$/g, "");
  }
  return "";
}

function hasBuildSetting(text, key, valuePattern) {
  return new RegExp(`${key}:\\s*${valuePattern.source}`).test(text) ||
    new RegExp(`${key} = ${valuePattern.source};`).test(text);
}

function addCheck(checks, ok, id, detail) {
  checks.push({ id, ok, detail });
}

function addBlocker(blockers, id, detail) {
  blockers.push({ id, detail });
}

function analyze(options) {
  const appleDir = resolve(options.appleDir);
  const projectYmlPath = join(appleDir, "project.yml");
  const projectPath = join(appleDir, "MeetingAssist.xcodeproj", "project.pbxproj");
  const iosInfoPath = join(appleDir, "Xcode", "MeetingAssistAppleApp-Info.plist");
  const macInfoPath = join(appleDir, "Xcode", "MeetingAssistMacApp-Info.plist");
  const macEntitlementsPath = join(appleDir, "Xcode", "MeetingAssistMacApp.entitlements");
  const privacyManifestPath = join(appleDir, "Xcode", "PrivacyInfo.xcprivacy");

  const checks = [];
  const blockers = [];
  const warnings = [];

  for (const [id, path] of [
    ["apple_dir", appleDir],
    ["xcodegen_spec", projectYmlPath],
    ["xcode_project", projectPath],
    ["ios_info_plist", iosInfoPath],
    ["mac_info_plist", macInfoPath],
  ]) {
    addCheck(checks, existsSync(path), id, path);
  }

  if (checks.some((check) => !check.ok)) {
    return { appleDir, ok: false, readyForDistribution: false, checks, blockers, warnings };
  }

  const projectYml = readText(projectYmlPath);
  const pbxproj = readText(projectPath);
  const iosInfo = parsePlist(iosInfoPath);
  const macInfo = parsePlist(macInfoPath);

  addCheck(
    checks,
    textHas(projectYml, /MeetingAssistAppleApp:/) && textHas(projectYml, /TARGETED_DEVICE_FAMILY:\s*["']?1,2["']?/),
    "ios_universal_target",
    "MeetingAssistAppleApp should target iPhone and iPad."
  );
  addCheck(
    checks,
    textHas(projectYml, /MeetingAssistMacApp:/),
    "mac_native_target",
    "MeetingAssistMacApp should remain a native macOS target."
  );
  addCheck(
    checks,
    nonEmptyPlistString(iosInfo, "NSCameraUsageDescription") &&
      nonEmptyPlistString(iosInfo, "NSMicrophoneUsageDescription"),
    "ios_camera_microphone_usage_strings",
    "iOS/iPadOS app needs camera and microphone usage descriptions."
  );
  addCheck(
    checks,
    nonEmptyPlistString(macInfo, "NSCameraUsageDescription") &&
      nonEmptyPlistString(macInfo, "NSMicrophoneUsageDescription"),
    "mac_camera_microphone_usage_strings",
    "macOS app needs camera and microphone usage descriptions."
  );
  addCheck(
    checks,
    iosInfo.CFBundleShortVersionString === "$(MARKETING_VERSION)" &&
      macInfo.CFBundleShortVersionString === "$(MARKETING_VERSION)" &&
      iosInfo.CFBundleVersion === "$(CURRENT_PROJECT_VERSION)" &&
      macInfo.CFBundleVersion === "$(CURRENT_PROJECT_VERSION)" &&
      hasBuildSetting(projectYml, "MARKETING_VERSION", /1\.0/) &&
      hasBuildSetting(projectYml, "CURRENT_PROJECT_VERSION", /\d+/) &&
      textHas(pbxproj, /MARKETING_VERSION = 1\.0;/) &&
      textHas(pbxproj, /CURRENT_PROJECT_VERSION = \d+;/),
    "version_build_settings",
    "App versions should come from MARKETING_VERSION and CURRENT_PROJECT_VERSION build settings."
  );
  addCheck(
    checks,
    textHas(projectYml, /CODE_SIGN_ENTITLEMENTS:\s*Xcode\/MeetingAssistMacApp\.entitlements/) &&
      textHas(pbxproj, /CODE_SIGN_ENTITLEMENTS = Xcode\/MeetingAssistMacApp\.entitlements;/),
    "mac_entitlements_wired",
    "macOS target should reference MeetingAssistMacApp.entitlements."
  );
  addCheck(
    checks,
    textHas(projectYml, /ENABLE_HARDENED_RUNTIME:\s*YES/) && textHas(pbxproj, /ENABLE_HARDENED_RUNTIME = YES;/),
    "mac_hardened_runtime_enabled",
    "macOS target should enable hardened runtime before Developer ID notarization."
  );

  if (existsSync(macEntitlementsPath)) {
    const macEntitlements = parsePlist(macEntitlementsPath);
    addCheck(
      checks,
      boolPlistValue(macEntitlements, "com.apple.security.device.camera") &&
        boolPlistValue(macEntitlements, "com.apple.security.device.audio-input"),
      "mac_media_entitlements",
      "macOS entitlements should allow camera and audio input."
    );
  } else {
    addCheck(checks, false, "mac_media_entitlements", "Missing macOS entitlements file.");
  }

  const iosBundleId = extractSetting(
    projectYml,
    "PRODUCT_BUNDLE_IDENTIFIER",
    /PRODUCT_BUNDLE_IDENTIFIER = ([^;]+);/
  );
  const hasBundleIds =
    iosBundleId &&
    textHas(projectYml, /PRODUCT_BUNDLE_IDENTIFIER:\s*co\.thebonfire\.meetingassist\.mac/) &&
    textHas(pbxproj, /PRODUCT_BUNDLE_IDENTIFIER = co\.thebonfire\.meetingassist\.ios;/) &&
    textHas(pbxproj, /PRODUCT_BUNDLE_IDENTIFIER = co\.thebonfire\.meetingassist\.mac;/);
  addCheck(checks, Boolean(hasBundleIds), "bundle_identifiers", "iOS and macOS bundle identifiers should be stable.");

  const hasTeam =
    Boolean(process.env.DEVELOPMENT_TEAM || process.env.APPLE_DEVELOPMENT_TEAM) ||
    textHas(projectYml, /DEVELOPMENT_TEAM:\s*[A-Z0-9]+/) ||
    textHas(pbxproj, /DEVELOPMENT_TEAM = [A-Z0-9]+;/);
  if (!hasTeam) {
    addBlocker(
      blockers,
      "apple_development_team",
      "Set DEVELOPMENT_TEAM or APPLE_DEVELOPMENT_TEAM in the build environment, or configure a team in a private xcconfig."
    );
  }

  const iosIconName = extractSetting(
    projectYml,
    "ASSETCATALOG_COMPILER_APPICON_NAME",
    /ASSETCATALOG_COMPILER_APPICON_NAME = ([^;]*);/
  );
  if (!iosIconName || !hasAppIconSet(appleDir, iosIconName)) {
    addBlocker(blockers, "ios_app_icon", "Add a real iOS/iPadOS AppIcon asset catalog before TestFlight upload.");
  }

  const macIconName = extractSetting(
    pbxproj,
    "ASSETCATALOG_COMPILER_APPICON_NAME",
    /ASSETCATALOG_COMPILER_APPICON_NAME = ([^;]*);/
  );
  if (!macIconName || !hasAppIconSet(appleDir, macIconName)) {
    addBlocker(blockers, "mac_app_icon", "Add a real macOS AppIcon asset catalog before distribution.");
  }

  if (!existsSync(privacyManifestPath)) {
    addBlocker(
      blockers,
      "privacy_manifest",
      "Add or generate PrivacyInfo.xcprivacy only after product-owned privacy data collection answers are final."
    );
  }

  const failedChecks = checks.filter((check) => !check.ok);
  return {
    appleDir,
    ok: failedChecks.length === 0,
    readyForDistribution: failedChecks.length === 0 && blockers.length === 0,
    checks,
    blockers,
    warnings,
  };
}

try {
  const args = parseArgs(process.argv.slice(2));
  if (args.help) {
    console.log(usage());
    process.exit(0);
  }

  const result = analyze(args);
  console.log(JSON.stringify(result, null, 2));
  process.exit(result.ok && (!args.strict || result.readyForDistribution) ? 0 : 1);
} catch (error) {
  console.error(JSON.stringify({ ok: false, error: error.message, usage: usage() }, null, 2));
  process.exit(1);
}
