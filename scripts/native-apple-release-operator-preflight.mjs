#!/usr/bin/env node
import { existsSync, readFileSync } from "node:fs";
import { dirname, join, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { spawnSync } from "node:child_process";

const rootDir = resolve(dirname(fileURLToPath(import.meta.url)), "..");

function usage() {
  return [
    "Usage:",
    "  node scripts/native-apple-release-operator-preflight.mjs [--apple-dir apple]",
    "    [--proofpack-dir artifacts/native-apple/<run-id>] [--configuration Release]",
    "    [--require-proofpack] [--require-privacy-manifest]",
    "    [--require-notary-profile]",
    "    [--run-build-rehearsal]",
    "",
    "Runs an offline, non-secret preflight for the Apple-account machine before",
    "executing the native Apple release command pack. It does not archive, upload,",
    "notarize, contact Apple, or print Team IDs, notary profile names, keys,",
    "certificates, provisioning profiles, or raw command logs.",
  ].join("\n");
}

function parseArgs(argv) {
  const args = {
    appleDir: "apple",
    proofpackDir: "",
    configuration: "Release",
    requireProofpack: false,
    requirePrivacyManifest: false,
    requireNotaryProfile: false,
    runBuildRehearsal: false,
    help: false,
  };

  for (let index = 0; index < argv.length; index += 1) {
    const arg = argv[index];
    if (arg === "--apple-dir") {
      args.appleDir = requiredValue(argv, index, arg);
      index += 1;
    } else if (arg === "--proofpack-dir") {
      args.proofpackDir = requiredValue(argv, index, arg);
      index += 1;
    } else if (arg === "--configuration") {
      args.configuration = requiredValue(argv, index, arg);
      index += 1;
    } else if (arg === "--require-proofpack") {
      args.requireProofpack = true;
    } else if (arg === "--require-privacy-manifest") {
      args.requirePrivacyManifest = true;
    } else if (arg === "--require-notary-profile") {
      args.requireNotaryProfile = true;
    } else if (arg === "--run-build-rehearsal") {
      args.runBuildRehearsal = true;
    } else if (arg === "--help" || arg === "-h") {
      args.help = true;
    } else {
      throw new Error(`Unknown argument: ${arg}`);
    }
  }

  return args;
}

function requiredValue(argv, index, flag) {
  const value = argv[index + 1] ?? "";
  if (!value || value.startsWith("--")) {
    throw new Error(`${flag} requires a value.`);
  }
  return value;
}

function readJSON(path) {
  return JSON.parse(readFileSync(path, "utf8"));
}

function repoRelative(path) {
  const relativePath = relative(rootDir, path);
  if (relativePath.startsWith("..")) {
    return path;
  }
  return relativePath.split(/[/\\]/).join("/");
}

function run(command, args, options = {}) {
  return spawnSync(command, args, {
    cwd: rootDir,
    encoding: "utf8",
    env: process.env,
    maxBuffer: 32 * 1024 * 1024,
    timeout: options.timeout ?? 30_000,
  });
}

function clean(value) {
  return String(value ?? "").trim();
}

function cleanBuildValue(value) {
  return clean(value).replace(/^["']|["']$/g, "").replace(/;$/, "").trim();
}

function bundleIdentifierForTarget(projectText, targetName) {
  const start = projectText.indexOf(`  ${targetName}:`);
  if (start === -1) {
    return "";
  }
  const targetBlock = projectText.slice(start + targetName.length + 4);
  const nextTarget = targetBlock.search(/\n  [A-Za-z0-9_]+:/);
  const block = nextTarget >= 0 ? targetBlock.slice(0, nextTarget) : targetBlock;
  return cleanBuildValue(/PRODUCT_BUNDLE_IDENTIFIER:\s*([^\n#]+)/.exec(block)?.[1] ?? "");
}

function readProjectMetadata(appleDir) {
  const projectYml = join(appleDir, "project.yml");
  if (!existsSync(projectYml)) {
    return null;
  }
  const projectText = readFileSync(projectYml, "utf8");
  const marketing = /MARKETING_VERSION:\s*([^\n#]+)/.exec(projectText)?.[1];
  const build = /CURRENT_PROJECT_VERSION:\s*([^\n#]+)/.exec(projectText)?.[1];
  if (!marketing || !build) {
    return null;
  }
  return {
    version: cleanBuildValue(marketing),
    build: cleanBuildValue(build),
    bundleIdentifiers: {
      ios: bundleIdentifierForTarget(projectText, "MeetingAssistAppleApp"),
      macos: bundleIdentifierForTarget(projectText, "MeetingAssistMacApp"),
    },
  };
}

function checkTool(name, command, args, checks, blockers, options = {}) {
  const result = run(command, args, { timeout: options.timeout ?? 20_000 });
  const ok = result.status === 0;
  checks.push({
    id: `tool_${name}`,
    ok,
    detail: ok ? options.okDetail ?? `${name} is available.` : `${name} is unavailable or failed to run.`,
  });
  if (!ok) {
    blockers.push({
      id: `tool_${name}`,
      detail: `Install or select an Xcode command line environment where ${name} is available.`,
    });
  }
  return result;
}

function checkPathTool(name, checks, blockers) {
  return checkTool(name, "/usr/bin/which", [name], checks, blockers, {
    okDetail: `${name} is available on PATH.`,
  });
}

function validIdentifier(value) {
  const trimmed = clean(value);
  if (!trimmed || /^<[^>]+>$/.test(trimmed)) {
    return false;
  }
  const normalized = trimmed.toUpperCase().replace(/[\s-]+/g, "_");
  return !["TODO", "TBD", "CHANGE_ME", "YOUR_VALUE", "PLACEHOLDER", "EXAMPLE", "SAMPLE", "UNKNOWN", "NONE", "NULL"].includes(normalized);
}

function safeNotaryProfileConfigured() {
  const value = clean(process.env.NOTARYTOOL_KEYCHAIN_PROFILE);
  if (!validIdentifier(value)) {
    return false;
  }
  if (
    /\bsk-[A-Za-z0-9_-]{20,}\b/.test(value) ||
    /\b[A-Z0-9]{10}\b/.test(value) ||
    /\.(p8|p12|mobileprovision|provisionprofile)$/i.test(value)
  ) {
    return false;
  }
  return true;
}

function parseXcodeVersion(output) {
  const firstLine = output.split(/\r?\n/).map((line) => line.trim()).find(Boolean);
  return firstLine || "available";
}

function schemePresent(listOutput, scheme) {
  return new RegExp(`(^|\\n)\\s*${scheme}\\s*(\\n|$)`).test(listOutput);
}

function parsePlist(path) {
  const result = run("plutil", ["-convert", "json", "-o", "-", path]);
  if (result.status !== 0) {
    throw new Error(`plutil could not parse ${repoRelative(path)}`);
  }
  return JSON.parse(result.stdout);
}

function checkSigning(appleDir, checks, blockers) {
  const result = run(process.execPath, [
    "scripts/native-apple-configure-signing.mjs",
    "--apple-dir",
    repoRelative(appleDir),
    "--validate-only",
  ]);
  const ok = result.status === 0;
  checks.push({
    id: "signing_configuration",
    ok,
    detail: ok ? "A non-placeholder Apple development team source is configured locally." : "No valid local Apple development team source was found.",
  });
  if (!ok) {
    blockers.push({
      id: "signing_configuration",
      detail: "Set APPLE_DEVELOPMENT_TEAM/DEVELOPMENT_TEAM or ignored apple/Config/Signing.local.xcconfig before archive/device/TestFlight work.",
    });
  }
}

function checkDefaultReadiness(appleDir, checks, blockers, args) {
  const result = run(process.execPath, [
    "scripts/native-apple-release-readiness.mjs",
    "--apple-dir",
    repoRelative(appleDir),
  ]);
  const ok = result.status === 0;
  checks.push({
    id: "default_release_readiness",
    ok,
    detail: ok ? "Repo-owned native Apple release prerequisites pass." : "Default native Apple release readiness failed.",
  });
  if (!ok) {
    blockers.push({
      id: "default_release_readiness",
      detail: "Run node scripts/native-apple-release-readiness.mjs and fix repo-owned prerequisites before the operator run.",
    });
  }

  let readiness = null;
  try {
    readiness = JSON.parse(result.stdout || "{}");
  } catch {
    readiness = null;
  }

  if (args.requirePrivacyManifest) {
    const privacyBlocker = readiness?.blockers?.find((blocker) => blocker?.id === "privacy_manifest");
    const privacyOk = ok && !privacyBlocker;
    checks.push({
      id: "privacy_manifest_required",
      ok: privacyOk,
      detail: privacyOk
        ? "PrivacyInfo.xcprivacy is present, shape-complete, and wired into the generated app targets."
        : "PrivacyInfo.xcprivacy is missing, invalid, or not wired into the generated app targets.",
    });
    if (!privacyOk) {
      blockers.push({
        id: "privacy_manifest",
        detail:
          privacyBlocker?.detail ??
          "Generate and wire apple/Xcode/PrivacyInfo.xcprivacy from approved privacy decisions before TestFlight or macOS distribution.",
      });
    }
  }
}

function checkReleaseBuildRehearsal(projectPath, configuration, checks, blockers) {
  const rehearsals = [
    {
      id: "release_rehearsal_ios",
      scheme: "MeetingAssistAppleApp",
      destination: "generic/platform=iOS",
      detail: "Release generic iOS/iPadOS build with signing disabled passes.",
    },
    {
      id: "release_rehearsal_macos",
      scheme: "MeetingAssistMacApp",
      destination: "generic/platform=macOS",
      detail: "Release generic macOS build with signing disabled passes.",
    },
  ];

  for (const rehearsal of rehearsals) {
    const result = run(
      "xcodebuild",
      [
        "-project",
        projectPath,
        "-quiet",
        "-scheme",
        rehearsal.scheme,
        "-configuration",
        configuration,
        "-destination",
        rehearsal.destination,
        "build",
        "CODE_SIGNING_ALLOWED=NO",
      ],
      { timeout: 180_000 }
    );
    const ok = result.status === 0;
    checks.push({
      id: rehearsal.id,
      ok,
      detail: ok ? rehearsal.detail : `${rehearsal.scheme} Release generic build failed with signing disabled.`,
    });
    if (!ok) {
      blockers.push({
        id: rehearsal.id,
        detail: `Fix the ${rehearsal.scheme} Release generic build before moving this proof pack to the Apple-account machine.`,
      });
    }
  }
}

function checkProofpack(proofpackDir, appleDir, configuration, checks, blockers, warnings) {
  const proofpackPath = join(proofpackDir, "proofpack.json");
  const draftPath = join(proofpackDir, "ReleaseEvidence.draft.json");
  const planPath = join(proofpackDir, "operator", "release-command-plan.json");
  const commandReadmePath = join(proofpackDir, "operator", "release-commands.md");

  const missing = [proofpackPath, draftPath, planPath, commandReadmePath].filter((path) => !existsSync(path));
  checks.push({
    id: "proofpack_operator_files",
    ok: missing.length === 0,
    detail: missing.length === 0 ? "Proof pack and operator command plan files are present." : `Missing ${missing.map(repoRelative).join(", ")}.`,
  });
  if (missing.length > 0) {
    blockers.push({
      id: "proofpack_operator_files",
      detail: "Run native-apple-release-proofpack and native-apple-release-package-plan --write before the operator preflight.",
    });
    return;
  }

  const proofpack = readJSON(proofpackPath);
  const draft = readJSON(draftPath);
  const plan = readJSON(planPath);
  const identityProblems = [];
  const currentMetadata = readProjectMetadata(appleDir);
  if (!currentMetadata) {
    identityProblems.push("currentProject");
  } else {
    if (String(proofpack.version ?? "") !== currentMetadata.version || String(draft.version ?? "") !== currentMetadata.version || String(plan.version ?? "") !== currentMetadata.version) {
      identityProblems.push("currentProject.version");
    }
    if (String(proofpack.build ?? "") !== currentMetadata.build || String(draft.build ?? "") !== currentMetadata.build || String(plan.build ?? "") !== currentMetadata.build) {
      identityProblems.push("currentProject.build");
    }
    if (String(plan.bundleIdentifiers?.ios ?? "") !== currentMetadata.bundleIdentifiers.ios) {
      identityProblems.push("currentProject.bundleIdentifiers.ios");
    }
    if (String(plan.bundleIdentifiers?.macos ?? "") !== currentMetadata.bundleIdentifiers.macos) {
      identityProblems.push("currentProject.bundleIdentifiers.macos");
    }
  }
  for (const key of ["runId", "roomId", "version", "build"]) {
    if (proofpack[key] && draft[key] && String(proofpack[key]) !== String(draft[key])) {
      identityProblems.push(`proofpack/draft ${key}`);
    }
  }
  for (const key of ["runId", "version", "build"]) {
    if (proofpack[key] && plan[key] && String(proofpack[key]) !== String(plan[key])) {
      identityProblems.push(`proofpack/plan ${key}`);
    }
  }
  if (String(plan.configuration ?? "") !== configuration) {
    identityProblems.push("configuration");
  }
  checks.push({
    id: "proofpack_identity",
    ok: identityProblems.length === 0,
    detail: identityProblems.length === 0 ? "Proof pack, draft, and command plan identity match." : identityProblems.join(", "),
  });
  if (identityProblems.length > 0) {
    blockers.push({
      id: "proofpack_identity",
      detail: "Regenerate the operator command plan for the current proof pack and configuration.",
    });
  }

  const requiredCommands = [
    "operatorPreflight",
    "preflightSigning",
    "defaultReadiness",
    "iosArchive",
    "testflightUpload",
    "macArchive",
    "macDeveloperIdExport",
    "macZipForNotary",
    "macSubmitNotary",
    "macStaple",
    "macGatekeeper",
    "promoteIPhoneMediaEvidence",
    "promoteIPadMediaEvidence",
    "promoteMacMediaEvidence",
    "promoteTurnRelayObservation",
    "createRoomInteropObservation",
    "promoteRoomInteropObservation",
    "createAppStoreReviewObservation",
    "promoteAppStoreReviewObservation",
    "createTestFlightObservation",
    "promoteTestFlightObservation",
    "createMacNotarizationObservation",
    "promoteMacNotarizationObservation",
    "writeLocalReleaseEvidence",
    "strictReleaseReadiness",
  ];
  const missingCommands = requiredCommands.filter((name) => !plan.commands?.[name]?.shell);
  const expectedProofpackRef = repoRelative(proofpackDir);
  const requiredCommandFragments = {
    operatorPreflight: [
      "native-apple-release-operator-preflight.mjs",
      "--proofpack-dir",
      "--require-proofpack",
      "--require-privacy-manifest",
      "--require-notary-profile",
      "--run-build-rehearsal",
    ],
    preflightSigning: ["native-apple-configure-signing.mjs", "--validate-only"],
    defaultReadiness: ["native-apple-release-readiness.mjs"],
    iosArchive: ["xcodebuild", "MeetingAssistAppleApp", "generic/platform=iOS", "archive"],
    testflightUpload: ["xcodebuild", "-exportArchive", "ExportOptions.testflight.plist"],
    macArchive: ["xcodebuild", "MeetingAssistMacApp", "generic/platform=macOS", "archive"],
    macDeveloperIdExport: ["xcodebuild", "-exportArchive", "ExportOptions.developer-id.plist"],
    macZipForNotary: ["ditto", "MeetingAssistMacApp.zip"],
    macSubmitNotary: ["notarytool", "submit", "$NOTARYTOOL_KEYCHAIN_PROFILE"],
    macStaple: ["stapler", "staple"],
    macGatekeeper: ["spctl"],
    promoteIPhoneMediaEvidence: ["native-apple-promote-media-evidence.mjs", "--platform iphone", "iphone-qa_snapshot.json", "--confirm-physical-device", "--confirm-same-room"],
    promoteIPadMediaEvidence: ["native-apple-promote-media-evidence.mjs", "--platform ipad", "ipad-qa_snapshot.json", "--confirm-physical-device", "--confirm-same-room"],
    promoteMacMediaEvidence: ["native-apple-promote-media-evidence.mjs", "--platform mac", "mac-qa_snapshot.json", "--confirm-physical-device", "--confirm-same-room"],
    promoteTurnRelayObservation: [
      "native-apple-promote-turn-evidence.mjs",
      "turn-relay-observation.json",
      "$NATIVE_APPLE_RESTRICTIVE_NETWORK",
      "--confirm-restrictive-network",
      "--confirm-same-room",
    ],
    createRoomInteropObservation: [
      "native-apple-create-room-interop-observation.mjs",
      "--proofpack-dir",
      expectedProofpackRef,
      "--participant-count",
      "$NATIVE_APPLE_ROOM_INTEROP_PARTICIPANT_COUNT",
      "--client-platforms",
      "$NATIVE_APPLE_ROOM_INTEROP_CLIENT_PLATFORMS",
      "--confirm-browser-native-mixed-room",
      "--confirm-three-plus-participants",
      "--confirm-remote-audio-audible",
      "--confirm-remote-video-rendered",
      "--confirm-no-missing-remote-health",
      "--confirm-no-duplicate-participants",
      "--confirm-no-stalled-remote-media",
      "--confirm-clean-leave",
      "--confirm-recording-off",
      "--confirm-current-build",
      "--confirm-no-secrets",
    ],
    promoteRoomInteropObservation: [
      "native-apple-promote-room-gate-evidence.mjs",
      "room-interop-observation.json",
      "--confirm-browser-native-mixed-room",
      "--confirm-three-plus-participants",
      "--confirm-clean-leave",
      "--confirm-recording-off",
      "--confirm-current-build",
      "--confirm-no-secrets",
    ],
    createAppStoreReviewObservation: [
      "native-apple-create-app-review-observation.mjs",
      "--proofpack-dir",
      expectedProofpackRef,
      "--support-url",
      "$NATIVE_APPLE_SUPPORT_URL",
      "--privacy-policy-url",
      "$NATIVE_APPLE_PRIVACY_POLICY_URL",
      "--confirm-description-ready",
      "--confirm-keywords-ready",
      "--confirm-screenshots-ready",
      "--confirm-app-privacy-ready",
      "--confirm-age-rating-complete",
      "--confirm-export-compliance-complete",
      "--confirm-test-information-ready",
      "--confirm-external-testing-group-ready",
      "--confirm-current-build",
      "--confirm-no-secrets",
    ],
    promoteAppStoreReviewObservation: [
      "native-apple-promote-distribution-evidence.mjs",
      "--kind app-review",
      "app-store-review-observation.json",
      "--confirm-review-metadata-complete",
      "--confirm-app-privacy-complete",
      "--confirm-external-testing-ready",
      "--confirm-no-secrets",
      "--confirm-current-build",
    ],
    createTestFlightObservation: [
      "native-apple-create-testflight-observation.mjs",
      "--proofpack-dir",
      expectedProofpackRef,
      "--app-store-connect-build-id",
      "$NATIVE_APPLE_APP_STORE_CONNECT_BUILD_ID",
      "--processing-status",
      "$NATIVE_APPLE_TESTFLIGHT_PROCESSING_STATUS",
      "--confirm-app-store-connect-upload",
      "--confirm-current-build",
      "--confirm-no-secrets",
    ],
    promoteTestFlightObservation: [
      "native-apple-promote-distribution-evidence.mjs",
      "--kind testflight",
      "testflight-observation.json",
      "--confirm-app-store-connect-upload",
      "--confirm-no-secrets",
      "--confirm-current-build",
    ],
    createMacNotarizationObservation: [
      "native-apple-create-notarization-observation.mjs",
      "--proofpack-dir",
      expectedProofpackRef,
      "--distribution-artifact-kind",
      "$NATIVE_APPLE_MAC_DISTRIBUTION_KIND",
      "--distribution-artifact-filename",
      "$NATIVE_APPLE_MAC_DISTRIBUTION_FILENAME",
      "--distribution-artifact-sha256",
      "$NATIVE_APPLE_MAC_DISTRIBUTION_SHA256",
      "--notary-request-id",
      "$NATIVE_APPLE_NOTARY_REQUEST_ID",
      "--gatekeeper-source",
      "$NATIVE_APPLE_GATEKEEPER_SOURCE",
      "--confirm-developer-id-archive",
      "--confirm-notary-accepted",
      "--confirm-stapled-app",
      "--confirm-gatekeeper-accepted",
      "--confirm-current-build",
      "--confirm-no-secrets",
    ],
    promoteMacNotarizationObservation: [
      "native-apple-promote-distribution-evidence.mjs",
      "--kind notarization",
      "notarization-observation.json",
      "--confirm-developer-id-archive",
      "--confirm-notary-accepted",
      "--confirm-stapled-app",
      "--confirm-gatekeeper-accepted",
      "--confirm-no-secrets",
      "--confirm-current-build",
    ],
    writeLocalReleaseEvidence: ["native-apple-release-proofpack.mjs", "--write-evidence"],
    strictReleaseReadiness: ["native-apple-release-readiness.mjs", "--strict", "ReleaseEvidence.draft.json"],
  };
  const staleCommands = Object.entries(requiredCommandFragments).flatMap(([name, fragments]) => {
    const shell = String(plan.commands?.[name]?.shell ?? "");
    return fragments.filter((fragment) => !shell.includes(fragment)).map((fragment) => `${name}:${fragment}`);
  });
  const commandProblems = [...missingCommands, ...staleCommands];
  checks.push({
    id: "operator_commands",
    ok: commandProblems.length === 0,
    detail:
      commandProblems.length === 0
        ? "Operator command pack includes archive, upload, notarization, and full proof-loop commands."
        : `Missing or stale commands: ${commandProblems.join(", ")}`,
  });
  if (commandProblems.length > 0) {
    blockers.push({
      id: "operator_commands",
      detail: "Regenerate the command plan so the Apple-account machine has the full ordered command pack.",
    });
  }

  const testflightOptionsPath = resolve(rootDir, plan.exportOptions?.testflight ?? "");
  const developerIdOptionsPath = resolve(rootDir, plan.exportOptions?.developerId ?? "");
  try {
    const testflight = parsePlist(testflightOptionsPath);
    const developerId = parsePlist(developerIdOptionsPath);
    const ok =
      testflight.method === "app-store-connect" &&
      testflight.destination === "upload" &&
      developerId.method === "developer-id" &&
      developerId.destination === "export";
    checks.push({
      id: "export_options",
      ok,
      detail: ok ? "TestFlight and Developer ID export option plists are parseable and shaped correctly." : "Export option plist method/destination values are not release-ready.",
    });
    if (!ok) {
      blockers.push({
        id: "export_options",
        detail: "Regenerate the package plan export option plists before archive/export.",
      });
    }
  } catch (error) {
    checks.push({
      id: "export_options",
      ok: false,
      detail: error.message,
    });
    blockers.push({
      id: "export_options",
      detail: "Regenerate or repair the package plan export option plists before archive/export.",
    });
  }

  if (draft.status && draft.status !== "pending") {
    warnings.push({
      id: "proofpack_draft_status",
      detail: "Proof-pack draft is no longer pending; confirm strict readiness after any additional operator changes.",
    });
  }
}

function buildPreflight(args) {
  const appleDir = resolve(rootDir, args.appleDir);
  const projectPath = join(appleDir, "MeetingAssist.xcodeproj");
  const checks = [];
  const blockers = [];
  const warnings = [];
  const toolVersions = {};

  const xcodeVersion = checkTool("xcodebuild", "xcodebuild", ["-version"], checks, blockers);
  if (xcodeVersion.status === 0) {
    toolVersions.xcodebuild = parseXcodeVersion(xcodeVersion.stdout);
  }
  checkTool("xcrun", "xcrun", ["--version"], checks, blockers);
  checkTool("notarytool", "xcrun", ["--find", "notarytool"], checks, blockers);
  checkTool("stapler", "xcrun", ["--find", "stapler"], checks, blockers);
  checkTool("plutil", "plutil", ["-help"], checks, blockers);
  checkPathTool("ditto", checks, blockers);
  checkPathTool("spctl", checks, blockers);

  if (!existsSync(projectPath)) {
    checks.push({
      id: "xcode_project",
      ok: false,
      detail: `Missing ${repoRelative(projectPath)}.`,
    });
    blockers.push({
      id: "xcode_project",
      detail: "Generate apple/MeetingAssist.xcodeproj before the operator run.",
    });
  } else {
    const list = run("xcodebuild", ["-list", "-project", projectPath]);
    const schemesOk = list.status === 0 && schemePresent(list.stdout, "MeetingAssistAppleApp") && schemePresent(list.stdout, "MeetingAssistMacApp");
    checks.push({
      id: "xcode_schemes",
      ok: schemesOk,
      detail: schemesOk ? "iOS/iPadOS and macOS app schemes are available." : "MeetingAssistAppleApp and MeetingAssistMacApp schemes were not both listed.",
    });
    if (!schemesOk) {
      blockers.push({
        id: "xcode_schemes",
        detail: "Regenerate the Xcode project or select the correct project before archive/export.",
      });
    }
  }

  checkSigning(appleDir, checks, blockers);
  checkDefaultReadiness(appleDir, checks, blockers, args);

  if (args.runBuildRehearsal) {
    if (existsSync(projectPath)) {
      checkReleaseBuildRehearsal(projectPath, args.configuration, checks, blockers);
    } else {
      checks.push({
        id: "release_rehearsal",
        ok: false,
        detail: "Release build rehearsal skipped because the Xcode project is missing.",
      });
    }
  } else {
    warnings.push({
      id: "release_build_rehearsal",
      detail: "Release generic iOS/macOS builds were not rehearsed; pass --run-build-rehearsal before the Apple-account run.",
    });
  }

  if (args.requireNotaryProfile) {
    const ok = safeNotaryProfileConfigured();
    checks.push({
      id: "notarytool_keychain_profile",
      ok,
      detail: ok ? "NOTARYTOOL_KEYCHAIN_PROFILE is set to a non-placeholder local keychain profile name." : "NOTARYTOOL_KEYCHAIN_PROFILE is missing or placeholder-shaped.",
    });
    if (!ok) {
      blockers.push({
        id: "notarytool_keychain_profile",
        detail: "Set NOTARYTOOL_KEYCHAIN_PROFILE to a local notarytool keychain profile name before macOS notarization.",
      });
    }
  } else if (!safeNotaryProfileConfigured()) {
    warnings.push({
      id: "notarytool_keychain_profile",
      detail: "NOTARYTOOL_KEYCHAIN_PROFILE is not set; pass --require-notary-profile on the Apple-account machine before notarization.",
    });
  }

  if (args.proofpackDir) {
    checkProofpack(resolve(rootDir, args.proofpackDir), appleDir, args.configuration, checks, blockers, warnings);
  } else if (args.requireProofpack) {
    checks.push({
      id: "proofpack_required",
      ok: false,
      detail: "No proof pack directory was provided.",
    });
    blockers.push({
      id: "proofpack_dir",
      detail: "Pass --proofpack-dir for the generated proof pack before running the Apple-account operator preflight.",
    });
  } else {
    warnings.push({
      id: "proofpack_dir",
      detail: "No proof pack was provided; operator command-plan consistency was not checked.",
    });
  }

  warnings.push({
    id: "offline_only",
    detail: "This preflight does not verify App Store Connect login, provisioning profile download, notary profile validity, or physical-device availability.",
  });

  return {
    appleDir: repoRelative(appleDir),
    ok: blockers.length === 0,
    readyForOperator: blockers.length === 0,
    configuration: args.configuration,
    checkedAt: new Date().toISOString(),
    toolVersions,
    checks,
    blockers,
    warnings,
  };
}

function main() {
  try {
    const args = parseArgs(process.argv.slice(2));
    if (args.help) {
      console.log(usage());
      return;
    }
    const output = buildPreflight(args);
    console.log(JSON.stringify(output, null, 2));
    process.exitCode = output.ok ? 0 : 1;
  } catch (error) {
    console.log(JSON.stringify({ ok: false, readyForOperator: false, error: error.message }, null, 2));
    process.exitCode = 1;
  }
}

main();
