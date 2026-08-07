#!/usr/bin/env node
import assert from "node:assert/strict";
import { mkdtempSync, mkdirSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { resolve } from "node:path";
import { buildResult, exitCode, localGateState, testMatrix, validateExpoManifest, validateProtectedSourceContracts, validateScreenShareTarget } from "./e9-native-readiness.mjs";

const manifest = {
  ios: {
    supportsTablet: true,
    bundleIdentifier: "xyz.thebonfire.app",
    appleTeamId: "73PT36P58W",
    associatedDomains: ["webcredentials:thebonfire.xyz", "applinks:thebonfire.xyz"],
    entitlements: { "com.apple.security.application-groups": ["group.xyz.thebonfire.app"] },
    infoPlist: {
      NSCameraUsageDescription: "camera",
      NSMicrophoneUsageDescription: "microphone",
      NSPhotoLibraryUsageDescription: "photos",
      UIBackgroundModes: ["audio", "voip"],
      RTCAppGroupIdentifier: "group.xyz.thebonfire.app",
      RTCScreenSharingExtension: "xyz.thebonfire.app.broadcast",
    },
  },
  plugins: ["expo-notifications", "@bacons/apple-targets", ["./plugins/withWebRTCBroadcastExtension", { stage: "native" }], ["./plugins/withWebRTCBroadcastExtension", { stage: "eas" }]],
  extra: {
    eas: {
      projectId: "30cd10a4-275d-45e3-8084-a1d7617b42f8",
      build: { experimental: { ios: { appExtensions: [{ parentBundleIdentifier: "xyz.thebonfire.app", bundleIdentifier: "xyz.thebonfire.app.broadcast", entitlements: { "com.apple.security.application-groups": ["group.xyz.thebonfire.app"] } }] } } },
    },
  },
};
const eas = {
  build: {
    development: { developmentClient: true, distribution: "internal", ios: { simulator: true } },
    preview: { distribution: "internal" },
    production: { autoIncrement: false, ios: { resourceClass: "m-medium" } },
  },
  submit: { production: { ios: { ascAppId: "6794029943" } } },
};

assert.equal(validateExpoManifest(manifest, eas).every((item) => item.ok), true);
const secretManifest = structuredClone(manifest);
secretManifest.extra.apiToken = "sk-abcdefghijklmnopqrstuvwxyz123456";
assert.equal(validateExpoManifest(secretManifest, eas).find((item) => item.id === "public_manifest_secret_safety")?.ok, false);
const brokenExtension = structuredClone(manifest);
brokenExtension.extra.eas.build.experimental.ios.appExtensions[0].entitlements["com.apple.security.application-groups"] = ["group.wrong"];
assert.equal(validateExpoManifest(brokenExtension, eas).find((item) => item.id === "screen_share_extension_binding")?.ok, false);
const brokenSimulator = structuredClone(eas);
brokenSimulator.build.production.ios.simulator = true;
assert.equal(validateExpoManifest(manifest, brokenSimulator).find((item) => item.id === "production_profile")?.ok, false);

const fixture = mkdtempSync(resolve(tmpdir(), "meetingassist-e9-native-"));
mkdirSync(resolve(fixture, "mobile/targets/screenshare"), { recursive: true });
writeFileSync(resolve(fixture, "mobile/targets/screenshare/expo-target.config.js"), "module.exports = { type: 'broadcast-upload', bundleIdentifier: 'xyz.thebonfire.app.broadcast', entitlements: { group: 'group.xyz.thebonfire.app' } };\n");
writeFileSync(resolve(fixture, "mobile/targets/screenshare/Info.plist"), "com.apple.broadcast-services-upload RPBroadcastProcessModeSampleBuffer\n");
assert.equal(validateScreenShareTarget(fixture).every((item) => item.ok), true);

for (const path of [
  "mobile/src/screens/ThreadScreen.tsx", "mobile/src/messaging/MessageActionSheet.tsx", "mobile/src/messaging/MentionComposerInput.tsx", "mobile/src/voice/useDictation.ts", "mobile/src/voice/useComposerDictation.ts", "mobile/src/push/deepLink.ts", "mobile/src/screens/RoomScreen.tsx",
]) mkdirSync(resolve(fixture, path, ".."), { recursive: true });
writeFileSync(resolve(fixture, "mobile/src/screens/ThreadScreen.tsx"), "MessageActionSheet onLongPress useComposerDictation Haptics.\n");
writeFileSync(resolve(fixture, "mobile/src/messaging/MessageActionSheet.tsx"), "onReact onReply onEdit onDelete\n");
writeFileSync(resolve(fixture, "mobile/src/messaging/MentionComposerInput.tsx"), "Haptics.selectionAsync TextInput\n");
writeFileSync(resolve(fixture, "mobile/src/voice/useDictation.ts"), "Haptics. onTranscript\n");
writeFileSync(resolve(fixture, "mobile/src/voice/useComposerDictation.ts"), "useDictation audioFocusRuntime\n");
writeFileSync(resolve(fixture, "mobile/src/push/deepLink.ts"), "parsePushTarget threadId\n");
writeFileSync(resolve(fixture, "mobile/src/screens/RoomScreen.tsx"), "screenShare stopScreenShare Haptics.\n");
assert.equal(validateProtectedSourceContracts(fixture).every((item) => item.ok), true);
writeFileSync(resolve(fixture, "mobile/src/screens/ThreadScreen.tsx"), "MessageActionSheet\n");
assert.equal(validateProtectedSourceContracts(fixture).find((item) => item.id === "protected_thread_chat_gestures_haptics_dictation")?.ok, false);

// A manifest check is intentionally not a local readiness receipt. The exact
// local gates must run and pass before localReady can become true.
writeFileSync(resolve(fixture, "mobile/src/screens/ThreadScreen.tsx"), "MessageActionSheet onLongPress useComposerDictation Haptics.\n");
const defaultResult = buildResult({ runLocal: false, runSimulator: false, dryRun: false }, manifest, eas, fixture);
assert.equal(defaultResult.manifestValid, true);
assert.equal(defaultResult.localGateStatus, "not_run");
assert.equal(defaultResult.localReady, false);
assert.equal(exitCode(defaultResult, false), 0);
const passingResult = buildResult(
  { runLocal: true, runSimulator: false, dryRun: false },
  manifest,
  eas,
  fixture,
  () => [{ id: "mobile_typecheck", status: "passed" }, { id: "mobile_unit_tests", status: "passed" }],
);
assert.equal(passingResult.localGateStatus, "passed");
assert.equal(passingResult.localReady, true);
assert.equal(exitCode(passingResult, false), 0);
const failedResult = buildResult(
  { runLocal: true, runSimulator: false, dryRun: false },
  manifest,
  eas,
  fixture,
  () => [{ id: "mobile_typecheck", status: "failed" }, { id: "mobile_unit_tests", status: "passed" }],
);
assert.equal(failedResult.localGateStatus, "failed");
assert.equal(failedResult.localReady, false);
assert.equal(exitCode(failedResult, false), 1);
assert.equal(localGateState({ runLocal: true }, [{ status: "planned" }]), "not_completed");

const matrix = testMatrix();
assert.equal(matrix.length, 7);
assert.deepEqual(matrix.map((item) => item.id), ["team_chat", "composer_dictation", "meeting_video", "screen_share", "push_deep_link", "audio_focus", "background_foreground_recovery"]);
assert.equal(matrix.every((item) => item.requiredEvidence.iphone.includes("E10") && item.requiredEvidence.ipad.includes("E10")), true);
assert.equal(matrix.every((item) => item.automated.localSimulator.includes("--run-simulator")), true);

console.log("e9-native-readiness: readiness-boundary checks passed");
