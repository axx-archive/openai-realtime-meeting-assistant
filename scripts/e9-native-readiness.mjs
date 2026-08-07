#!/usr/bin/env node
/**
 * Token-free Expo native readiness boundary for E9.
 *
 * This script intentionally resolves only the public Expo config and runs
 * local commands. It never invokes EAS, contacts App Store Connect, installs
 * to a device, starts a simulator, or treats a local test as TestFlight or
 * physical-device acceptance.
 */
import { spawnSync } from "node:child_process";
import { existsSync, readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const rootDir = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const mobileDir = resolve(rootDir, "mobile");

const requiredScenarios = [
  ["team_chat", "#team open, send, unread/reply/reaction state", ["chatRealtime.test.ts", "messageGestures.test.ts"]],
  ["composer_dictation", "tap record, stop or delete, transcribe, auto-send, retry", ["dictationLifecycle.test.ts", "audioFocusConsumers.test.ts"]],
  ["meeting_video", "join, mute, camera recovery, remote video", ["roomMedia.test.ts", "remoteStreamLifecycle.test.ts"]],
  ["screen_share", "start, cancel, restore camera track", ["roomMedia.test.ts", "webrtcIosCameraConfig.test.ts"]],
  ["push_deep_link", "notification routes to its exact #team thread/message", ["pushDeepLink.test.ts"]],
  ["audio_focus", "dictation and meeting media cannot own the microphone together", ["audioFocusCoordinator.test.ts", "audioFocusConsumers.test.ts"]],
  ["background_foreground_recovery", "stale media cannot reattach after app-state invalidation", ["roomMedia.test.ts", "mediaSession.test.ts"]],
];

const protectedSourceContracts = [
  {
    id: "thread_chat_gestures_haptics_dictation",
    path: "mobile/src/screens/ThreadScreen.tsx",
    required: ["MessageActionSheet", "onLongPress", "useComposerDictation", "Haptics."],
  },
  {
    id: "composer_dictation_audio_focus",
    path: "mobile/src/voice/useComposerDictation.ts",
    required: ["useDictation", "audioFocusRuntime"],
  },
  {
    id: "message_action_surface",
    path: "mobile/src/messaging/MessageActionSheet.tsx",
    required: ["onReact", "onReply", "onEdit", "onDelete"],
  },
  {
    id: "composer_mention_haptics",
    path: "mobile/src/messaging/MentionComposerInput.tsx",
    required: ["Haptics.selectionAsync", "TextInput"],
  },
  {
    id: "dictation_haptics",
    path: "mobile/src/voice/useDictation.ts",
    required: ["Haptics.", "onTranscript"],
  },
  {
    id: "push_deep_link_parser",
    path: "mobile/src/push/deepLink.ts",
    required: ["parsePushTarget", "threadId"],
  },
  {
    id: "room_video_screen_share_haptics",
    path: "mobile/src/screens/RoomScreen.tsx",
    required: ["screenShare", "stopScreenShare", "Haptics."],
  },
];

function usage() {
  return [
    "Usage:",
    "  node scripts/e9-native-readiness.mjs [--manifest-json path] [--run-local] [--run-simulator] [--strict] [--dry-run]",
    "",
    "Default mode resolves mobile's public Expo config and validates the token-free",
    "manifest, EAS profile, screen-share target, and protected chat contracts.",
    "It reports localReady:false until --run-local has actually completed successfully.",
    "--run-local additionally runs the deterministic Expo typecheck and unit suite.",
    "--run-simulator delegates only to the existing local Xcode simulator gate; it never targets hardware.",
    "--strict exits nonzero until Apple privacy/signing, TestFlight, and physical-device",
    "evidence are supplied; it does not perform any of those external operations.",
  ].join("\n");
}

function parseArgs(argv) {
  const args = { manifestJson: "", runLocal: false, runSimulator: false, strict: false, dryRun: false, help: false };
  for (let index = 0; index < argv.length; index += 1) {
    const arg = argv[index];
    if (arg === "--manifest-json") {
      const value = argv[index + 1] ?? "";
      if (!value || value.startsWith("--")) throw new Error("--manifest-json requires a path.");
      args.manifestJson = value;
      index += 1;
    } else if (arg === "--run-local") {
      args.runLocal = true;
    } else if (arg === "--run-simulator") {
      args.runSimulator = true;
      args.runLocal = true;
    } else if (arg === "--strict") {
      args.strict = true;
    } else if (arg === "--dry-run") {
      args.dryRun = true;
    } else if (arg === "--help" || arg === "-h") {
      args.help = true;
    } else {
      throw new Error(`Unknown argument: ${arg}`);
    }
  }
  return args;
}

function readJson(path) {
  return JSON.parse(readFileSync(path, "utf8"));
}

function resolvePublicManifest(args) {
  if (args.manifestJson) return readJson(resolve(rootDir, args.manifestJson));
  const result = spawnSync("npx", ["--no-install", "expo", "config", "--type", "public", "--json"], {
    cwd: mobileDir,
    encoding: "utf8",
  });
  if (result.status !== 0) {
    throw new Error(`Could not resolve Expo public config: ${(result.stderr || result.stdout || "unknown error").trim()}`);
  }
  return JSON.parse(result.stdout);
}

function check(checks, id, ok, detail) {
  checks.push({ id, ok, detail });
}

function pluginNames(plugins) {
  return (Array.isArray(plugins) ? plugins : []).map((plugin) => Array.isArray(plugin) ? plugin[0] : plugin).filter((name) => typeof name === "string");
}

function validTeamId(value) {
  return typeof value === "string" && /^(?=.*[A-Z])[A-Z0-9]{10}$/.test(value);
}

function validUuid(value) {
  return typeof value === "string" && /^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i.test(value);
}

function nonEmptyString(value) {
  return typeof value === "string" && value.trim().length > 0;
}

function arrayIncludes(value, expected) {
  return Array.isArray(value) && value.includes(expected);
}

function publicManifestHasSecretLikeContent(value, path = "$") {
  if (Array.isArray(value)) {
    return value.flatMap((item, index) => publicManifestHasSecretLikeContent(item, `${path}[${index}]`));
  }
  if (value && typeof value === "object") {
    return Object.entries(value).flatMap(([key, item]) => {
      const keyLooksSecret = /(secret|password|passwd|token|credential|api_?key|private_?key|provision|certificate|p8|p12|profile|authorization|cookie)/i.test(key);
      return [
        ...(keyLooksSecret ? [`${path}.${key}`] : []),
        ...publicManifestHasSecretLikeContent(item, `${path}.${key}`),
      ];
    });
  }
  if (typeof value !== "string") return [];
  return /\b(?:sk-[A-Za-z0-9_-]{20,}|AKIA[0-9A-Z]{16}|eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,})\b/.test(value) ? [path] : [];
}

function validateExpoManifest(manifest, eas) {
  const checks = [];
  const ios = manifest?.ios ?? {};
  const info = ios.infoPlist ?? {};
  const appGroups = ios.entitlements?.["com.apple.security.application-groups"];
  const appGroup = Array.isArray(appGroups) && appGroups.length === 1 ? appGroups[0] : "";
  const parentBundle = ios.bundleIdentifier;
  const extensions = manifest?.extra?.eas?.build?.experimental?.ios?.appExtensions;
  const broadcast = Array.isArray(extensions) && extensions.length === 1 ? extensions[0] : null;
  const plugins = pluginNames(manifest?.plugins);
  const secretPaths = publicManifestHasSecretLikeContent(manifest);

  check(checks, "ios_universal", ios.supportsTablet === true, "Expo iOS config must keep iPhone and iPad support enabled.");
  check(checks, "ios_bundle_identifier", nonEmptyString(parentBundle) && parentBundle.includes("."), "iOS bundle identifier must be stable and non-empty.");
  check(checks, "ios_public_team_identifier", validTeamId(ios.appleTeamId), "Only a public-format Apple Team ID may be present; credentials remain external.");
  check(checks, "eas_project_identifier", validUuid(manifest?.extra?.eas?.projectId), "EAS project ID must be a UUID, never a token or credential.");
  check(checks, "public_manifest_secret_safety", secretPaths.length === 0, secretPaths.length === 0 ? "Resolved public Expo config contains no secret-shaped values." : `Secret-shaped public config paths: ${secretPaths.join(", ")}`);
  check(checks, "camera_microphone_usage", nonEmptyString(info.NSCameraUsageDescription) && nonEmptyString(info.NSMicrophoneUsageDescription), "Camera and microphone usage strings are required.");
  check(checks, "photo_usage", nonEmptyString(info.NSPhotoLibraryUsageDescription), "Photo-library usage string is required for attachment selection.");
  check(checks, "background_audio_voip", arrayIncludes(info.UIBackgroundModes, "audio") && arrayIncludes(info.UIBackgroundModes, "voip"), "Audio and VoIP background modes must remain declared for room recovery.");
  check(checks, "associated_domains", arrayIncludes(ios.associatedDomains, "webcredentials:thebonfire.xyz") && arrayIncludes(ios.associatedDomains, "applinks:thebonfire.xyz"), "Passkey and universal-link domains must remain present.");
  check(checks, "app_group_entitlement", typeof appGroup === "string" && appGroup.startsWith("group."), "Main app must have exactly one valid app-group entitlement.");
  check(checks, "screen_share_app_group_binding", info.RTCAppGroupIdentifier === appGroup, "WebRTC app-group Info.plist value must match the main entitlement.");
  check(checks, "screen_share_extension_binding", Boolean(broadcast) && broadcast.parentBundleIdentifier === parentBundle && broadcast.bundleIdentifier === info.RTCScreenSharingExtension && Array.isArray(broadcast.entitlements?.["com.apple.security.application-groups"]) && broadcast.entitlements["com.apple.security.application-groups"].length === 1 && broadcast.entitlements["com.apple.security.application-groups"][0] === appGroup, "Broadcast extension parent, bundle, and entitlement must exactly match the main app.");
  check(checks, "screen_share_plugin_stages", plugins.filter((name) => name === "./plugins/withWebRTCBroadcastExtension").length === 2 && plugins.includes("@bacons/apple-targets"), "Both native and EAS screen-share config-plugin stages must remain wired.");
  check(checks, "push_plugin", plugins.includes("expo-notifications"), "expo-notifications must remain configured for push/deep-link handling.");

  const development = eas?.build?.development;
  const preview = eas?.build?.preview;
  const production = eas?.build?.production;
  check(checks, "simulator_profile", development?.developmentClient === true && development?.distribution === "internal" && development?.ios?.simulator === true, "Development profile is the only simulator profile.");
  check(checks, "internal_preview_profile", preview?.distribution === "internal" && preview?.ios?.simulator !== true, "Preview is internal-device capable but is not a simulator claim.");
  check(checks, "production_profile", production?.ios?.simulator !== true && production?.autoIncrement === false, "Production build profile must not be simulator-only or mutate build numbers implicitly.");
  check(checks, "submit_profile_is_not_submission", nonEmptyString(eas?.submit?.production?.ios?.ascAppId), "A submit profile may identify the ASC app but does not evidence a submission.");
  return checks;
}

function validateScreenShareTarget(root = rootDir) {
  const targetPath = resolve(root, "mobile/targets/screenshare/expo-target.config.js");
  const plistPath = resolve(root, "mobile/targets/screenshare/Info.plist");
  const checks = [];
  const target = existsSync(targetPath) ? readFileSync(targetPath, "utf8") : "";
  const plist = existsSync(plistPath) ? readFileSync(plistPath, "utf8") : "";
  check(checks, "screen_share_target_manifest", /type:\s*['"]broadcast-upload['"]/.test(target) && /bundleIdentifier:\s*['"]xyz\.thebonfire\.app\.broadcast['"]/.test(target) && /group\.xyz\.thebonfire\.app/.test(target), "ReplayKit target manifest must declare the expected broadcast extension and app group.");
  check(checks, "screen_share_extension_plist", /com\.apple\.broadcast-services-upload/.test(plist) && /RPBroadcastProcessModeSampleBuffer/.test(plist), "ReplayKit extension plist must declare the broadcast upload point and sample-buffer mode.");
  return checks;
}

function validateProtectedSourceContracts(root = rootDir) {
  return protectedSourceContracts.map((contract) => {
    const path = resolve(root, contract.path);
    const source = existsSync(path) ? readFileSync(path, "utf8") : "";
    const missing = contract.required.filter((needle) => !source.includes(needle));
    return {
      id: `protected_${contract.id}`,
      ok: missing.length === 0,
      detail: missing.length === 0 ? `${contract.path} retains its protected interaction contract.` : `${contract.path} is missing: ${missing.join(", ")}`,
    };
  });
}

function testMatrix() {
  return requiredScenarios.map(([id, assertion, tests]) => ({
    id,
    assertion,
    automated: { localUnit: tests, localSimulator: "node scripts/e9-native-readiness.mjs --run-simulator" },
    requiredEvidence: {
      iphone: "E10 physical-device acceptance receipt",
      ipad: "E10 physical-device acceptance receipt",
      simulator: "local simulator command output for the current build",
    },
  }));
}

function runLocalGates(dryRun, runSimulator) {
  const commands = [
    ["mobile_typecheck", "npm", ["run", "typecheck"]],
    ["mobile_unit_tests", "npm", ["test"]],
  ];
  if (runSimulator) {
    commands.push(["native_xcode_simulator", "node", ["scripts/native-apple-local-gates.mjs", "--run-xcode"], rootDir]);
  }
  return commands.map(([id, command, args, cwd = mobileDir]) => {
    if (dryRun) return { id, command: [command, ...args].join(" "), status: "planned", outputTail: "" };
    const result = spawnSync(command, args, { cwd, encoding: "utf8", maxBuffer: 20 * 1024 * 1024 });
    return { id, command: [command, ...args].join(" "), status: result.status === 0 ? "passed" : "failed", outputTail: `${result.stdout ?? ""}${result.stderr ?? ""}`.slice(-4000) };
  });
}

function localGateState(args, localGates) {
  if (!args.runLocal) return "not_run";
  if (localGates.some((gate) => gate.status === "failed")) return "failed";
  if (localGates.some((gate) => gate.status !== "passed")) return "not_completed";
  return "passed";
}

function buildResult(args, manifest, eas, root = rootDir, gateRunner = runLocalGates) {
  const checks = [
    ...validateExpoManifest(manifest, eas),
    ...validateScreenShareTarget(root),
    ...validateProtectedSourceContracts(root),
  ];
  const localGates = args.runLocal ? gateRunner(args.dryRun, args.runSimulator) : [];
  const localFailures = localGates.filter((gate) => gate.status === "failed");
  const localGateStatus = localGateState(args, localGates);
  const failedChecks = checks.filter((item) => !item.ok);
  const blockers = [
    "Approved Apple privacy manifest/App Privacy decisions are required before distribution.",
    "A valid local or CI signing configuration is required for archive/device work; this script neither reads nor writes credentials.",
    "TestFlight upload, processing, and tester availability require separate App Store Connect evidence.",
    "Physical iPhone and iPad acceptance, including lock/background recovery, require E10 operator evidence.",
  ];
  return {
    scope: "E9 token-free Expo native readiness",
    providerCalls: false,
    performedExternalOperations: false,
    manifestValid: failedChecks.length === 0,
    // Configuration validation is valuable, but it is not a local readiness
    // result. A caller must opt in to and complete every local gate.
    localReady: failedChecks.length === 0 && localFailures.length === 0 && localGateStatus === "passed",
    localGateStatus,
    simulatorReady: Boolean(args.runSimulator && localGates.find((gate) => gate.id === "native_xcode_simulator")?.status === "passed"),
    strictReady: false,
    checks,
    localGates,
    matrix: testMatrix(),
    releaseBoundary: {
      localBuildAndTests: "Only local readiness; not an archive, upload, or acceptance claim.",
      testFlight: "not_attempted — E10/App Store Connect operator evidence required",
      physicalDevice: "not_attempted — E10 iPhone and iPad operator acceptance required",
      providerIntegratedAcceptance: "pending E10 — no provider or live-room operation was performed",
    },
    blockers,
  };
}

function exitCode(result, strict, dryRun = false) {
  if (!result.manifestValid) return 1;
  if (dryRun) return strict && !result.strictReady ? 1 : 0;
  // Default mode is deliberately contract-only and may still be used in a
  // preflight/CI manifest job. It must not be displayed as local-ready.
  if (result.localGateStatus !== "not_run" && !result.localReady) return 1;
  return strict && !result.strictReady ? 1 : 0;
}

function main(argv = process.argv.slice(2)) {
  try {
    const args = parseArgs(argv);
    if (args.help) {
      console.log(usage());
      return 0;
    }
    const manifest = resolvePublicManifest(args);
    const eas = readJson(resolve(rootDir, "mobile/eas.json"));
    const result = buildResult(args, manifest, eas);
    console.log(JSON.stringify(result, null, 2));
    return exitCode(result, args.strict, args.dryRun);
  } catch (error) {
    console.error(JSON.stringify({ ok: false, error: error instanceof Error ? error.message : String(error), usage: usage() }, null, 2));
    return 1;
  }
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  process.exitCode = main();
}

export { buildResult, exitCode, localGateState, parseArgs, requiredScenarios, testMatrix, validateExpoManifest, validateProtectedSourceContracts, validateScreenShareTarget };
