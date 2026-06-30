#!/usr/bin/env node
import assert from "node:assert/strict";
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { spawnSync } from "node:child_process";

const scriptsDir = dirname(fileURLToPath(import.meta.url));
const rootDir = resolve(scriptsDir, "..");
const proofpackScriptPath = resolve(scriptsDir, "native-apple-release-proofpack.mjs");
const promoteScriptPath = resolve(scriptsDir, "native-apple-promote-room-gate-evidence.mjs");

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

function writeJSONFile(path, value) {
  writeFixtureFile(path, `${JSON.stringify(value, null, 2)}\n`);
  return path;
}

function makeAppleFixture() {
  const dir = mkdtempSync(resolve(tmpdir(), "meetingassist-promote-room-gate-"));
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
    "room-gate-promotion-room-test",
    "--created-at",
    "2026-06-29T21:00:00Z",
    "--skip-gates",
  ]);
}

function roomObservation(overrides = {}) {
  const base = {
    schemaVersion: 1,
    artifactType: "native_room_interop_observation",
    status: "observed",
    runId: "",
    roomId: "room-gate-promotion-room-test",
    testedAt: "2026-06-29T21:15:00Z",
    app: {
      version: "1.0",
      build: "15",
    },
    room: {
      participantCount: 4,
      clientPlatforms: ["browser", "ios", "ipados", "macos"],
      browserNativeMixed: true,
      threePlusParticipants: true,
    },
    media: {
      remoteAudioAudible: true,
      remoteVideoRendered: true,
      noMissingRemoteHealth: true,
      noDuplicateParticipants: true,
      noStalledRemoteMedia: true,
    },
    lifecycle: {
      cleanLeaveParticipantsEmpty: true,
      participantsAfterLeave: 0,
    },
    recording: {
      recordingOffStopsForwarding: true,
      recordingOffTranscriptForwarded: false,
      recordingOffRealtimeForwarded: false,
    },
  };
  return {
    ...base,
    ...overrides,
    app: { ...base.app, ...(overrides.app ?? {}) },
    room: { ...base.room, ...(overrides.room ?? {}) },
    media: { ...base.media, ...(overrides.media ?? {}) },
    lifecycle: { ...base.lifecycle, ...(overrides.lifecycle ?? {}) },
    recording: { ...base.recording, ...(overrides.recording ?? {}) },
  };
}

function promote(args) {
  return runNode(promoteScriptPath, args);
}

const fixture = makeAppleFixture();
const runId = `native-apple-promote-room-gate-test-${process.pid}`;
const created = createProofpack(fixture.appleDir, runId);
assert.equal(created.status, 0);
assert.equal(created.output.ok, true);

const observationPath = writeJSONFile(resolve(fixture.dir, "room-interop-observation.json"), roomObservation({ runId }));
const promoted = promote([
  "--proofpack-dir",
  created.output.proofpackDir,
  "--input",
  observationPath,
  "--confirm-browser-native-mixed-room",
  "--confirm-three-plus-participants",
  "--confirm-clean-leave",
  "--confirm-recording-off",
  "--confirm-current-build",
  "--confirm-no-secrets",
  "--promoted-at",
  "2026-06-29T21:20:00Z",
]);
assert.equal(promoted.status, 0);
assert.equal(promoted.output.ok, true);
assert.equal(promoted.output.kind, "roomInterop");

let draft = JSON.parse(readFileSync(promoted.output.evidenceDraft, "utf8"));
assert.equal(draft.roomInterop.status, "passed");
assert.equal(draft.roomInterop.participantCount, 4);
assert.equal(draft.roomInterop.browserNativeMixed, true);
assert.equal(draft.roomInterop.threePlusParticipants, true);
assert.equal(draft.roomInterop.cleanLeaveParticipantsEmpty, true);
assert.equal(draft.roomInterop.recordingOffStopsForwarding, true);
const artifact = JSON.parse(readFileSync(promoted.output.artifactPath, "utf8"));
assert.equal(artifact.claimScope, "browser_native_room_gate");
assert.equal(artifact.releaseEligible, true);
assert.equal(artifact.room.clientPlatforms.includes("browser"), true);
assert.equal(artifact.room.clientPlatforms.includes("ios"), true);
assert.equal(artifact.media.noDuplicateParticipants, true);
assert.equal(artifact.lifecycle.participantsAfterLeave, 0);
assert.equal(artifact.recording.recordingOffTranscriptForwarded, false);
assert.equal(artifact.promotion.sourceRunId, runId);
assert.equal(artifact.promotion.sourceRoomId, "room-gate-promotion-room-test");
assert.equal(artifact.promotion.sourceTestedAt, "2026-06-29T21:15:00Z");
assert.equal(artifact.promotion.operatorConfirmedBrowserNativeMixedRoom, true);
assert.equal(artifact.promotion.operatorConfirmedNoSecrets, true);

const duplicate = promote([
  "--proofpack-dir",
  created.output.proofpackDir,
  "--input",
  observationPath,
  "--confirm-browser-native-mixed-room",
  "--confirm-three-plus-participants",
  "--confirm-clean-leave",
  "--confirm-recording-off",
  "--confirm-current-build",
  "--confirm-no-secrets",
]);
assert.equal(duplicate.status, 1);
assert.match(duplicate.output.error, /already passed/);

const missingConfirm = promote([
  "--proofpack-dir",
  created.output.proofpackDir,
  "--input",
  writeJSONFile(resolve(fixture.dir, "missing-confirm.json"), roomObservation({ runId })),
  "--confirm-current-build",
  "--confirm-no-secrets",
  "--force",
]);
assert.equal(missingConfirm.status, 1);
assert.match(missingConfirm.output.error, /confirmBrowserNativeMixedRoom|confirmThreePlusParticipants|confirmCleanLeave|confirmRecordingOff/);

const weakRoom = promote([
  "--proofpack-dir",
  created.output.proofpackDir,
  "--input",
  writeJSONFile(
    resolve(fixture.dir, "weak-room.json"),
    roomObservation({
      runId,
      room: {
        participantCount: 2,
        clientPlatforms: ["ios", "ipados"],
        browserNativeMixed: false,
        threePlusParticipants: false,
      },
    })
  ),
  "--confirm-browser-native-mixed-room",
  "--confirm-three-plus-participants",
  "--confirm-clean-leave",
  "--confirm-recording-off",
  "--confirm-current-build",
  "--confirm-no-secrets",
  "--force",
]);
assert.equal(weakRoom.status, 1);
assert.match(weakRoom.output.error, /room\.participantCount|room\.clientPlatforms\.browser|room\.browserNativeMixed/);

const unsupportedPlatform = promote([
  "--proofpack-dir",
  created.output.proofpackDir,
  "--input",
  writeJSONFile(
    resolve(fixture.dir, "unsupported-platform.json"),
    roomObservation({ runId, room: { clientPlatforms: ["browser", "ios", "android"] } })
  ),
  "--confirm-browser-native-mixed-room",
  "--confirm-three-plus-participants",
  "--confirm-clean-leave",
  "--confirm-recording-off",
  "--confirm-current-build",
  "--confirm-no-secrets",
  "--force",
]);
assert.equal(unsupportedPlatform.status, 1);
assert.match(unsupportedPlatform.output.error, /room\.clientPlatforms\.allowed/);

const badLifecycle = promote([
  "--proofpack-dir",
  created.output.proofpackDir,
  "--input",
  writeJSONFile(
    resolve(fixture.dir, "bad-lifecycle.json"),
    roomObservation({
      runId,
      lifecycle: { cleanLeaveParticipantsEmpty: false, participantsAfterLeave: 1 },
      recording: { recordingOffStopsForwarding: false, recordingOffTranscriptForwarded: true },
    })
  ),
  "--confirm-browser-native-mixed-room",
  "--confirm-three-plus-participants",
  "--confirm-clean-leave",
  "--confirm-recording-off",
  "--confirm-current-build",
  "--confirm-no-secrets",
  "--force",
]);
assert.equal(badLifecycle.status, 1);
assert.match(badLifecycle.output.error, /lifecycle\.cleanLeaveParticipantsEmpty|recording\.recordingOffStopsForwarding/);

const wrongBuild = promote([
  "--proofpack-dir",
  created.output.proofpackDir,
  "--input",
  writeJSONFile(resolve(fixture.dir, "wrong-build.json"), roomObservation({ runId, app: { build: "14" } })),
  "--confirm-browser-native-mixed-room",
  "--confirm-three-plus-participants",
  "--confirm-clean-leave",
  "--confirm-recording-off",
  "--confirm-current-build",
  "--confirm-no-secrets",
  "--force",
]);
assert.equal(wrongBuild.status, 1);
assert.match(wrongBuild.output.error, /app\.build/);

const blankIdentity = promote([
  "--proofpack-dir",
  created.output.proofpackDir,
  "--input",
  writeJSONFile(resolve(fixture.dir, "blank-identity.json"), roomObservation({ roomId: "" })),
  "--confirm-browser-native-mixed-room",
  "--confirm-three-plus-participants",
  "--confirm-clean-leave",
  "--confirm-recording-off",
  "--confirm-current-build",
  "--confirm-no-secrets",
  "--force",
]);
assert.equal(blankIdentity.status, 1);
assert.match(blankIdentity.output.error, /runId|roomId/);

const unsafeObservation = promote([
  "--proofpack-dir",
  created.output.proofpackDir,
  "--input",
  writeJSONFile(
    resolve(fixture.dir, "unsafe-room.json"),
    roomObservation({ runId, rawLog: "v=0\r\na=candidate:1 1 udp 1 192.168.1.10 9000 typ host\r\n" })
  ),
  "--confirm-browser-native-mixed-room",
  "--confirm-three-plus-participants",
  "--confirm-clean-leave",
  "--confirm-recording-off",
  "--confirm-current-build",
  "--confirm-no-secrets",
  "--force",
]);
assert.equal(unsafeObservation.status, 1);
assert.match(unsafeObservation.output.error, /unsafeContent/);

const unexpectedObservation = promote([
  "--proofpack-dir",
  created.output.proofpackDir,
  "--input",
  writeJSONFile(
    resolve(fixture.dir, "unexpected-room.json"),
    roomObservation({ runId, screenshots: ["not allowed"], media: { rawLog: "not allowed" } })
  ),
  "--confirm-browser-native-mixed-room",
  "--confirm-three-plus-participants",
  "--confirm-clean-leave",
  "--confirm-recording-off",
  "--confirm-current-build",
  "--confirm-no-secrets",
  "--force",
]);
assert.equal(unexpectedObservation.status, 1);
assert.match(unexpectedObservation.output.error, /input\.screenshots|input\.media\.rawLog/);

rmSync(fixture.dir, { recursive: true, force: true });

console.log("native-apple-promote-room-gate-evidence: 10 checks passed");
