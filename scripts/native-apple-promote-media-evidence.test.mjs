#!/usr/bin/env node
import assert from "node:assert/strict";
import { existsSync, mkdtempSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { spawnSync } from "node:child_process";

const scriptsDir = dirname(fileURLToPath(import.meta.url));
const rootDir = resolve(scriptsDir, "..");
const proofpackScriptPath = resolve(scriptsDir, "native-apple-release-proofpack.mjs");
const promoteScriptPath = resolve(scriptsDir, "native-apple-promote-media-evidence.mjs");

function runNode(scriptPath, args = []) {
  const result = spawnSync(process.execPath, [scriptPath, ...args], {
    cwd: rootDir,
    encoding: "utf8",
  });
  let output;
  try {
    output = JSON.parse(result.stdout || result.stderr);
  } catch (error) {
    throw new Error(`Could not parse output.\nstatus=${result.status}\nstdout=${result.stdout}\nstderr=${result.stderr}\n${error}`);
  }
  return { status: result.status, output };
}

function writeFixtureFile(path, contents) {
  mkdirSync(dirname(path), { recursive: true });
  writeFileSync(path, contents);
}

function makeAppleFixture() {
  const dir = mkdtempSync(resolve(tmpdir(), "meetingassist-promote-media-"));
  const appleDir = resolve(dir, "apple");
  writeFixtureFile(
    resolve(appleDir, "project.yml"),
    `targets:
  MeetingAssistAppleApp:
    settings:
      base:
        CURRENT_PROJECT_VERSION: 15
        MARKETING_VERSION: 1.0
  MeetingAssistMacApp:
    settings:
      base:
        CURRENT_PROJECT_VERSION: 15
        MARKETING_VERSION: 1.0
`
  );
  return { dir, appleDir };
}

function createProofpack(appleDir, runId) {
  return runNode(proofpackScriptPath, [
    "--apple-dir",
    appleDir,
    "--artifacts-dir",
    resolve(rootDir, "artifacts", "native-apple"),
    "--run-id",
    runId,
    "--room-id",
    "promotion-room-test",
    "--created-at",
    "2026-06-29T17:30:00Z",
    "--skip-gates",
  ]);
}

function qaSnapshot(platform, overrides = {}) {
  const config = {
    iphone: {
      clientPlatform: "ios",
      target: "MeetingAssistAppleApp",
      device: { kind: "iphone", model: "iPhone physical", os: "iOS 26.5", physical: true },
    },
    ipad: {
      clientPlatform: "ipados",
      target: "MeetingAssistAppleApp",
      device: { kind: "ipad", model: "iPad physical", os: "iPadOS 26.5", physical: true },
    },
    mac: {
      clientPlatform: "macos",
      target: "MeetingAssistMacApp",
      device: { kind: "mac", model: "MacBookPro18,3", os: "macOS 26.5", physical: true },
    },
  }[platform];
  const snapshotRunId = overrides.runId ?? "";
  const snapshotRoomId = overrides.roomId ?? "";
  const base = {
    schemaVersion: 1,
    artifactType: "native_device_media",
    claimScope: "qa_snapshot",
    releaseEligible: false,
    status: "observed",
    runId: snapshotRunId,
    roomId: snapshotRoomId,
    platform: config.clientPlatform,
    capturedAt: "2026-06-29T17:45:00Z",
    app: {
      version: "1.0",
      build: "15",
      target: config.target,
      clientPlatform: config.clientPlatform,
      clientVersion: "test",
    },
    device: config.device,
    lifecycle: "connected",
    remoteVideoTiles: 1,
    renderer: {
      source: "native_remote_video_renderer",
      remoteVideoFramesRendered: 3,
      observedRemoteVideoTracks: 1,
      latestFrameWidth: 1280,
      latestFrameHeight: 720,
      latestRenderedAt: "2026-06-29T17:45:01Z",
      capturesPixels: false,
    },
    mediaAssertions: {
      cameraPublished: true,
      microphonePublished: true,
      remoteAudioReceived: true,
      remoteVideoRendered: true,
    },
    releaseEvidenceSummary: {
      status: "pending",
      runId: snapshotRunId,
      roomId: snapshotRoomId,
      device: config.device.model,
      os: config.device.os,
      testedAt: "2026-06-29T17:45:00Z",
      mediaAssertions: {
        cameraPublished: true,
        microphonePublished: true,
        remoteAudioReceived: true,
        remoteVideoRendered: true,
      },
    },
    assertionEvidence: {
      cameraPublished: { source: "outboundVideoFramesSent", value: 90, passed: true },
      microphonePublished: { source: "outboundAudioPacketsSent", value: 120, passed: true },
      remoteAudioReceived: { source: "inboundAudioPacketsReceived", value: 180, passed: true },
      remoteVideoRendered: { source: "nativeRemoteVideoRenderer+inboundVideoDecoded", value: 3, passed: true },
    },
    counters: {
      outboundAudioPacketsSent: 120,
      outboundVideoFramesSent: 90,
      inboundAudioPacketsReceived: 180,
      inboundVideoDecoded: 140,
    },
  };
  return {
    ...base,
    ...overrides,
    app: { ...base.app, ...(overrides.app ?? {}) },
    device: { ...base.device, ...(overrides.device ?? {}) },
    renderer: overrides.renderer === null ? null : { ...base.renderer, ...(overrides.renderer ?? {}) },
    mediaAssertions: { ...base.mediaAssertions, ...(overrides.mediaAssertions ?? {}) },
    assertionEvidence: { ...base.assertionEvidence, ...(overrides.assertionEvidence ?? {}) },
    counters: { ...base.counters, ...(overrides.counters ?? {}) },
  };
}

function writeSnapshot(dir, name, snapshot) {
  const path = resolve(dir, name);
  writeFixtureFile(path, `${JSON.stringify(snapshot, null, 2)}\n`);
  return path;
}

function promote(args) {
  return runNode(promoteScriptPath, args);
}

const fixture = makeAppleFixture();
const runId = `native-apple-promote-test-${process.pid}`;
const roomId = "promotion-room-test";
const created = createProofpack(fixture.appleDir, runId);
assert.equal(created.status, 0);
assert.equal(created.output.ok, true);

function boundSnapshot(platform, overrides = {}) {
  return qaSnapshot(platform, { runId, roomId, ...overrides });
}

const iphoneSnapshotPath = writeSnapshot(fixture.dir, "iphone-qa.json", boundSnapshot("iphone"));
const promoted = promote([
  "--proofpack-dir",
  created.output.proofpackDir,
  "--platform",
  "iphone",
  "--input",
  iphoneSnapshotPath,
  "--confirm-physical-device",
  "--confirm-same-room",
  "--promoted-at",
  "2026-06-29T18:00:00Z",
]);
assert.equal(promoted.status, 0);
assert.equal(promoted.output.ok, true);
assert.equal(promoted.output.platform, "iphone");

const draft = JSON.parse(readFileSync(promoted.output.evidenceDraft, "utf8"));
assert.equal(draft.physicalDeviceMedia.iphone.status, "passed");
assert.equal(draft.physicalDeviceMedia.iphone.device, "iPhone physical");
assert.equal(draft.physicalDeviceMedia.iphone.mediaAssertions.remoteVideoRendered, true);
assert.equal(draft.physicalDeviceMedia.ipad.status, "pending");

const artifact = JSON.parse(readFileSync(promoted.output.artifactPath, "utf8"));
assert.equal(artifact.claimScope, "physical_device");
assert.equal(artifact.releaseEligible, true);
assert.equal(artifact.status, "passed");
assert.equal(artifact.runId, runId);
assert.equal(artifact.roomId, "promotion-room-test");
assert.equal(artifact.promotion.operatorConfirmedPhysicalDevice, true);
assert.equal(artifact.promotion.operatorConfirmedSameRoom, true);
assert.equal(artifact.assertionEvidence.cameraPublished.passed, true);
assert.equal(JSON.parse(readFileSync(iphoneSnapshotPath, "utf8")).claimScope, "qa_snapshot");

const duplicatePromotion = promote([
  "--proofpack-dir",
  created.output.proofpackDir,
  "--platform",
  "iphone",
  "--input",
  iphoneSnapshotPath,
  "--confirm-physical-device",
  "--confirm-same-room",
]);
assert.equal(duplicatePromotion.status, 1);
assert.match(duplicatePromotion.output.error, /already passed/);

const missingConfirm = promote([
  "--proofpack-dir",
  created.output.proofpackDir,
  "--platform",
  "ipad",
  "--input",
  writeSnapshot(fixture.dir, "ipad-qa.json", boundSnapshot("ipad")),
]);
assert.equal(missingConfirm.status, 1);
assert.match(missingConfirm.output.error, /confirmPhysicalDevice/);

const blankRunRoomRejected = promote([
  "--proofpack-dir",
  created.output.proofpackDir,
  "--platform",
  "ipad",
  "--input",
  writeSnapshot(fixture.dir, "ipad-blank-run-room.json", qaSnapshot("ipad")),
  "--confirm-physical-device",
  "--confirm-same-room",
]);
assert.equal(blankRunRoomRejected.status, 1);
assert.match(blankRunRoomRejected.output.error, /runId:empty|roomId:empty/);

const simulatorRejected = promote([
  "--proofpack-dir",
  created.output.proofpackDir,
  "--platform",
  "ipad",
  "--input",
  writeSnapshot(
    fixture.dir,
    "ipad-simulator.json",
    boundSnapshot("ipad", { device: { kind: "simulator", model: "iPad Simulator", physical: false } })
  ),
  "--confirm-physical-device",
  "--confirm-same-room",
]);
assert.equal(simulatorRejected.status, 1);
assert.match(simulatorRejected.output.error, /device.kind|device.physical/);

const falseAssertionRejected = promote([
  "--proofpack-dir",
  created.output.proofpackDir,
  "--platform",
  "mac",
  "--input",
  writeSnapshot(
    fixture.dir,
    "mac-false-assertion.json",
    boundSnapshot("mac", {
      mediaAssertions: { remoteVideoRendered: false },
      assertionEvidence: { remoteVideoRendered: { source: "nativeRemoteVideoRenderer+inboundVideoDecoded", value: 0, passed: false } },
      counters: { inboundVideoDecoded: 0 },
    })
  ),
  "--confirm-physical-device",
  "--confirm-same-room",
]);
assert.equal(falseAssertionRejected.status, 1);
assert.match(falseAssertionRejected.output.error, /mediaAssertions|assertionEvidence|counters/);

const missingRendererRejected = promote([
  "--proofpack-dir",
  created.output.proofpackDir,
  "--platform",
  "mac",
  "--input",
  writeSnapshot(fixture.dir, "mac-missing-renderer.json", boundSnapshot("mac", { renderer: null })),
  "--confirm-physical-device",
  "--confirm-same-room",
]);
assert.equal(missingRendererRejected.status, 1);
assert.match(missingRendererRejected.output.error, /renderer/);

const wrongBuildRejected = promote([
  "--proofpack-dir",
  created.output.proofpackDir,
  "--platform",
  "mac",
  "--input",
  writeSnapshot(fixture.dir, "mac-wrong-build.json", boundSnapshot("mac", { app: { build: "14" } })),
  "--confirm-physical-device",
  "--confirm-same-room",
]);
assert.equal(wrongBuildRejected.status, 1);
assert.match(wrongBuildRejected.output.error, /app.build/);

const unsafeRejected = promote([
  "--proofpack-dir",
  created.output.proofpackDir,
  "--platform",
  "mac",
  "--input",
  writeSnapshot(
    fixture.dir,
    "mac-unsafe.json",
    boundSnapshot("mac", {
      diagnostics: {
        rawSdp: "v=0\r\na=candidate:842163049 1 udp 1677729535 192.168.1.25 56143 typ host\r\n",
      },
    })
  ),
  "--confirm-physical-device",
  "--confirm-same-room",
]);
assert.equal(unsafeRejected.status, 1);
assert.match(unsafeRejected.output.error, /unsafeContent/);

const mismatchedRunRejected = promote([
  "--proofpack-dir",
  created.output.proofpackDir,
  "--platform",
  "mac",
  "--input",
  writeSnapshot(fixture.dir, "mac-wrong-run.json", boundSnapshot("mac", { runId: "native-apple-other-run" })),
  "--confirm-physical-device",
  "--confirm-same-room",
]);
assert.equal(mismatchedRunRejected.status, 1);
assert.match(mismatchedRunRejected.output.error, /runId/);

rmSync(fixture.dir, { recursive: true, force: true });

console.log("native-apple-promote-media-evidence: 10 checks passed");
