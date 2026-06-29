#!/usr/bin/env node
import { execFileSync } from "node:child_process";
import { existsSync, readdirSync, readFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";

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

const iosAppIconSlots = [
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
];

const macAppIconSlots = [
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

function appIconSlotKey(idiom, size, scale) {
  return `${idiom}:${size}:${scale}`;
}

function expectedIconPixels(size, scale) {
  const points = Number(size.split("x")[0]);
  const multiplier = Number(scale.replace("x", ""));
  return Math.round(points * multiplier);
}

function pngDimensions(path) {
  const data = readFileSync(path);
  const signature = "89504e470d0a1a0a";
  if (
    data.length < 24 ||
    data.subarray(0, 8).toString("hex") !== signature ||
    data.subarray(12, 16).toString("ascii") !== "IHDR"
  ) {
    return null;
  }
  return {
    width: data.readUInt32BE(16),
    height: data.readUInt32BE(20),
  };
}

function cleanBuildSettingValue(value) {
  return String(value ?? "")
    .trim()
    .replace(/^["']|["']$/g, "")
    .replace(/;$/, "")
    .trim();
}

function expandBuildSettingValue(value, settings, options = {}) {
  const { includeEnv = true } = options;
  return cleanBuildSettingValue(value).replace(/\$\(([^)]+)\)/g, (_match, key) => {
    return cleanBuildSettingValue(settings[key] ?? (includeEnv ? process.env[key] : "") ?? "");
  });
}

function validDevelopmentTeam(value) {
  const normalized = cleanBuildSettingValue(value);
  const placeholders = new Set(["ABCDE12345", "YOURTEAMID", "YOUR_TEAM_ID", "TEAMID1234"]);
  return /^[A-Z0-9]{10}$/.test(normalized) && !placeholders.has(normalized);
}

function developmentTeamValuesFromText(text) {
  const values = [];
  const patterns = [
    /DEVELOPMENT_TEAM:\s*([^\n#]+)/g,
    /DEVELOPMENT_TEAM\s*=\s*([^;\n#]+)/g,
    /DevelopmentTeam:\s*([^\n#]+)/g,
    /DevelopmentTeam\s*=\s*([^;\n#]+)/g,
  ];
  for (const pattern of patterns) {
    let match = pattern.exec(text);
    while (match) {
      values.push(cleanBuildSettingValue(match[1]));
      match = pattern.exec(text);
    }
  }
  return values;
}

function stripXcconfigComment(line) {
  const commentStart = line.indexOf("//");
  return (commentStart === -1 ? line : line.slice(0, commentStart)).trim();
}

function parseXcconfigSettings(path, options = {}, seen = new Set()) {
  const { includeOptional = true } = options;
  const settings = {};
  if (!existsSync(path)) {
    return settings;
  }

  const resolved = resolve(path);
  if (seen.has(resolved)) {
    return settings;
  }
  seen.add(resolved);

  for (const rawLine of readText(resolved).split(/\r?\n/)) {
    const line = stripXcconfigComment(rawLine);
    if (!line) {
      continue;
    }

    const includeMatch = /^#include(\?)?\s+"([^"]+)"/.exec(line);
    if (includeMatch) {
      const optional = includeMatch[1] === "?";
      if (optional && !includeOptional) {
        continue;
      }
      const includePath = resolve(dirname(resolved), includeMatch[2]);
      if (existsSync(includePath) || !optional) {
        Object.assign(settings, parseXcconfigSettings(includePath, options, seen));
      }
      continue;
    }

    const assignmentMatch = /^([A-Za-z0-9_]+)\s*=\s*(.*)$/.exec(line);
    if (assignmentMatch) {
      settings[assignmentMatch[1]] = cleanBuildSettingValue(assignmentMatch[2]);
    }
  }

  return settings;
}

function privacyManifestStatus(path) {
  if (!existsSync(path)) {
    return { ok: false, missing: ["missing_file"] };
  }

  let manifest;
  try {
    manifest = parsePlist(path);
  } catch {
    return { ok: false, missing: ["invalid_plist"] };
  }

  const missing = [];
  if (typeof manifest.NSPrivacyTracking !== "boolean") {
    missing.push("NSPrivacyTracking");
  }

  if (!Array.isArray(manifest.NSPrivacyTrackingDomains)) {
    missing.push("NSPrivacyTrackingDomains");
  } else if (manifest.NSPrivacyTracking === true && manifest.NSPrivacyTrackingDomains.length === 0) {
    missing.push("NSPrivacyTrackingDomains:required_when_tracking");
  }

  if (!Array.isArray(manifest.NSPrivacyAccessedAPITypes)) {
    missing.push("NSPrivacyAccessedAPITypes");
  } else {
    manifest.NSPrivacyAccessedAPITypes.forEach((entry, index) => {
      if (!entry || typeof entry !== "object") {
        missing.push(`NSPrivacyAccessedAPITypes[${index}]`);
        return;
      }
      if (!nonEmptyPlistString(entry, "NSPrivacyAccessedAPIType")) {
        missing.push(`NSPrivacyAccessedAPITypes[${index}].NSPrivacyAccessedAPIType`);
      }
      if (
        !Array.isArray(entry.NSPrivacyAccessedAPITypeReasons) ||
        entry.NSPrivacyAccessedAPITypeReasons.length === 0 ||
        entry.NSPrivacyAccessedAPITypeReasons.some((reason) => typeof reason !== "string" || reason.trim() === "")
      ) {
        missing.push(`NSPrivacyAccessedAPITypes[${index}].NSPrivacyAccessedAPITypeReasons`);
      }
    });
  }

  if (!Array.isArray(manifest.NSPrivacyCollectedDataTypes)) {
    missing.push("NSPrivacyCollectedDataTypes");
  } else if (manifest.NSPrivacyCollectedDataTypes.length === 0) {
    missing.push("NSPrivacyCollectedDataTypes:empty");
  } else {
    manifest.NSPrivacyCollectedDataTypes.forEach((entry, index) => {
      if (!entry || typeof entry !== "object") {
        missing.push(`NSPrivacyCollectedDataTypes[${index}]`);
        return;
      }
      if (!nonEmptyPlistString(entry, "NSPrivacyCollectedDataType")) {
        missing.push(`NSPrivacyCollectedDataTypes[${index}].NSPrivacyCollectedDataType`);
      }
      if (typeof entry.NSPrivacyCollectedDataTypeLinked !== "boolean") {
        missing.push(`NSPrivacyCollectedDataTypes[${index}].NSPrivacyCollectedDataTypeLinked`);
      }
      if (typeof entry.NSPrivacyCollectedDataTypeTracking !== "boolean") {
        missing.push(`NSPrivacyCollectedDataTypes[${index}].NSPrivacyCollectedDataTypeTracking`);
      }
      if (
        !Array.isArray(entry.NSPrivacyCollectedDataTypePurposes) ||
        entry.NSPrivacyCollectedDataTypePurposes.length === 0 ||
        entry.NSPrivacyCollectedDataTypePurposes.some((purpose) => typeof purpose !== "string" || purpose.trim() === "")
      ) {
        missing.push(`NSPrivacyCollectedDataTypes[${index}].NSPrivacyCollectedDataTypePurposes`);
      }
    });
  }

  return { ok: missing.length === 0, missing };
}

function findAppIconSet(appleDir, iconName) {
  let found = "";
  walk(appleDir, (path) => {
    if (path.endsWith(`${iconName}.appiconset`) && existsSync(join(path, "Contents.json"))) {
      found = path;
    }
  });
  return found;
}

function appIconSetStatus(appleDir, iconName, requiredSlots) {
  if (!iconName) {
    return { ok: false, missing: ["missing_app_icon_name"] };
  }
  const iconSetPath = findAppIconSet(appleDir, iconName);
  if (!iconSetPath) {
    return { ok: false, missing: [`missing_${iconName}.appiconset`] };
  }

  let contents;
  try {
    contents = JSON.parse(readText(join(iconSetPath, "Contents.json")));
  } catch {
    return { ok: false, missing: [`invalid_${iconName}_contents_json`] };
  }

  const images = Array.isArray(contents.images) ? contents.images : [];
  const imagesBySlot = new Map(
    images.map((image) => [appIconSlotKey(image.idiom, image.size, image.scale), image])
  );
  const missing = [];
  for (const [idiom, size, scale] of requiredSlots) {
    const key = appIconSlotKey(idiom, size, scale);
    const image = imagesBySlot.get(key);
    if (!image?.filename) {
      missing.push(key);
      continue;
    }
    const imagePath = join(iconSetPath, image.filename);
    if (!existsSync(imagePath)) {
      missing.push(`${key}:file`);
      continue;
    }
    const dimensions = pngDimensions(imagePath);
    const pixels = expectedIconPixels(size, scale);
    if (!dimensions || dimensions.width !== pixels || dimensions.height !== pixels) {
      missing.push(`${key}:dimensions`);
    }
  }

  return { ok: missing.length === 0, missing };
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
  const signingConfigPath = join(appleDir, "Config", "Signing.xcconfig");

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

  addCheck(
    checks,
    textHas(projectYml, /path:\s*Xcode\/Assets\.xcassets/) &&
      textHas(pbxproj, /Assets\.xcassets in Resources/),
    "asset_catalog_wired",
    "iOS and macOS app targets should include the shared asset catalog."
  );
  addCheck(
    checks,
    textHas(projectYml, /ASSETCATALOG_COMPILER_APPICON_NAME:\s*AppIcon/) &&
      (pbxproj.match(/ASSETCATALOG_COMPILER_APPICON_NAME = AppIcon;/g) ?? []).length >= 2,
    "app_icon_build_settings",
    "Generated Xcode project should compile AppIcon for both app targets."
  );
  addCheck(
    checks,
    existsSync(signingConfigPath) &&
      (projectYml.match(/Config\/Signing\.xcconfig/g) ?? []).length >= 2 &&
      textHas(pbxproj, /Signing\.xcconfig/),
    "signing_xcconfig_wired",
    "App targets should use Config/Signing.xcconfig while keeping local team IDs out of git."
  );

  const signingSettings = parseXcconfigSettings(signingConfigPath);
  const signingTeamValue = expandBuildSettingValue(signingSettings.DEVELOPMENT_TEAM, signingSettings);
  const trackedSigningText = existsSync(signingConfigPath) ? readText(signingConfigPath) : "";
  const committedTeamValues = [
    ...developmentTeamValuesFromText(projectYml),
    ...developmentTeamValuesFromText(pbxproj),
    ...developmentTeamValuesFromText(trackedSigningText),
  ];
  addCheck(
    checks,
    !committedTeamValues.some(validDevelopmentTeam),
    "no_committed_development_team",
    "Apple development team IDs should come from environment or ignored local xcconfig, not committed project files or tracked xcconfig."
  );
  const hasTeam = [
    process.env.DEVELOPMENT_TEAM,
    process.env.APPLE_DEVELOPMENT_TEAM,
    signingTeamValue,
  ].some(validDevelopmentTeam);
  if (!hasTeam) {
    addBlocker(
      blockers,
      "apple_development_team",
      "Set DEVELOPMENT_TEAM or APPLE_DEVELOPMENT_TEAM in the build environment, or copy apple/Config/Signing.local.example.xcconfig to ignored apple/Config/Signing.local.xcconfig and set DEVELOPMENT_TEAM."
    );
  }

  const iosIconName = extractSetting(
    projectYml,
    "ASSETCATALOG_COMPILER_APPICON_NAME",
    /ASSETCATALOG_COMPILER_APPICON_NAME = ([^;]*);/
  );
  const iosIconStatus = appIconSetStatus(appleDir, iosIconName, iosAppIconSlots);
  if (!iosIconStatus.ok) {
    addBlocker(
      blockers,
      "ios_app_icon",
      `Add a complete iOS/iPadOS AppIcon asset catalog before TestFlight upload. Missing: ${iosIconStatus.missing.slice(0, 5).join(", ")}`
    );
  }

  const macIconName = extractSetting(
    projectYml,
    "ASSETCATALOG_COMPILER_APPICON_NAME",
    /ASSETCATALOG_COMPILER_APPICON_NAME = ([^;]*);/
  );
  const macIconStatus = appIconSetStatus(appleDir, macIconName, macAppIconSlots);
  if (!macIconStatus.ok) {
    addBlocker(
      blockers,
      "mac_app_icon",
      `Add a complete macOS AppIcon asset catalog before distribution. Missing: ${macIconStatus.missing.slice(0, 5).join(", ")}`
    );
  }

  const privacyStatus = privacyManifestStatus(privacyManifestPath);
  if (!privacyStatus.ok) {
    addBlocker(
      blockers,
      "privacy_manifest",
      `Add apple/Xcode/PrivacyInfo.xcprivacy only after docs/native-apple-privacy-review.md decisions are final. Missing or invalid: ${privacyStatus.missing.slice(0, 6).join(", ")}`
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
