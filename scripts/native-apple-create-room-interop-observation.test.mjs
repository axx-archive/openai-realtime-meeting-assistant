#!/usr/bin/env node
import assert from "node:assert/strict";
import { existsSync, mkdirSync, mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { spawnSync } from "node:child_process";

const scriptsDir = dirname(fileURLToPath(import.meta.url));
const rootDir = resolve(scriptsDir, "..");
const proofpackScriptPath = resolve(scriptsDir, "native-apple-release-proofpack.mjs");
const createScriptPath = resolve(scriptsDir, "native-apple-create-room-interop-observation.mjs");

const confirmationFlags = [
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
];

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
  const dir = mkdtempSync(resolve(tmpdir(), "meetingassist-room-interop-observation-"));
  const appleDir = resolve(dir, "apple");
  writeFixtureFile(
    resolve(appleDir, "project.yml"),
    `targets:
  MeetingAssistAppleApp:
    settings:
      base:
        CURRENT_PROJECT_VERSION: 15
        MARKETING_VERSION: 1.0
        PRODUCT_BUNDLE_IDENTIFIER: co.thebonfire.meetingassist.ios
  MeetingAssistMacApp:
    settings:
      base:
        CURRENT_PROJECT_VERSION: 15
        MARKETING_VERSION: 1.0
        PRODUCT_BUNDLE_IDENTIFIER: co.thebonfire.meetingassist.mac
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
    "room-interop-observation-room-test",
    "--created-at",
    "2026-06-30T17:00:00Z",
    "--skip-gates",
  ]);
}

function createObservation(proofpackDir, extraArgs = []) {
  return runNode(createScriptPath, [
    "--proofpack-dir",
    proofpackDir,
    "--participant-count",
    "4",
    "--client-platforms",
    "browser,ios,ipados,macos",
    "--tested-at",
    "2026-06-30T17:18:00Z",
    ...confirmationFlags,
    ...extraArgs,
  ]);
}

const fixture = makeAppleFixture();
const runId = `native-apple-room-interop-observation-test-${process.pid}`;
const proofpack = createProofpack(fixture.appleDir, runId);
assert.equal(proofpack.status, 0);
const draftBefore = readFileSync(proofpack.output.evidenceDraft, "utf8");

const created = createObservation(proofpack.output.proofpackDir);
assert.equal(created.status, 0);
assert.equal(created.output.ok, true);
assert.equal(created.output.runId, runId);
assert.equal(created.output.roomId, "room-interop-observation-room-test");
assert.equal(created.output.participantCount, 4);
assert.deepEqual(created.output.clientPlatforms, ["browser", "ios", "ipados", "macos"]);
assert.ok(created.output.outputRef.endsWith("/inbox/room-interop-observation.json"));
assert.ok(existsSync(created.output.outputPath));
assert.ok(created.output.nextSteps.some((step) => step.includes("native-apple-promote-room-gate-evidence.mjs")));
assert.equal(readFileSync(proofpack.output.evidenceDraft, "utf8"), draftBefore);

const observation = JSON.parse(readFileSync(created.output.outputPath, "utf8"));
assert.equal(observation.schemaVersion, 1);
assert.equal(observation.artifactType, "native_room_interop_observation");
assert.equal(observation.status, "observed");
assert.equal(observation.runId, runId);
assert.equal(observation.roomId, "room-interop-observation-room-test");
assert.equal(observation.testedAt, "2026-06-30T17:18:00Z");
assert.equal(observation.app.version, "1.0");
assert.equal(observation.app.build, "15");
assert.equal(observation.room.participantCount, 4);
assert.equal(observation.room.browserNativeMixed, true);
assert.equal(observation.room.threePlusParticipants, true);
assert.equal(observation.media.remoteAudioAudible, true);
assert.equal(observation.media.remoteVideoRendered, true);
assert.equal(observation.media.noMissingRemoteHealth, true);
assert.equal(observation.media.noDuplicateParticipants, true);
assert.equal(observation.media.noStalledRemoteMedia, true);
assert.equal(observation.lifecycle.cleanLeaveParticipantsEmpty, true);
assert.equal(observation.lifecycle.participantsAfterLeave, 0);
assert.equal(observation.recording.recordingOffStopsForwarding, true);
assert.equal(observation.recording.recordingOffTranscriptForwarded, false);
assert.equal(observation.recording.recordingOffRealtimeForwarded, false);

const duplicate = createObservation(proofpack.output.proofpackDir);
assert.equal(duplicate.status, 1);
assert.match(duplicate.output.error, /Refusing to overwrite/);

const forced = createObservation(proofpack.output.proofpackDir, ["--force"]);
assert.equal(forced.status, 0);

const normalizedPlatformsRunId = `native-apple-room-interop-observation-platforms-${process.pid}`;
const normalizedPlatformsProofpack = createProofpack(fixture.appleDir, normalizedPlatformsRunId);
assert.equal(normalizedPlatformsProofpack.status, 0);
const normalizedPlatforms = createObservation(normalizedPlatformsProofpack.output.proofpackDir, [
  "--client-platforms",
  " browser,IOS,ios,macos ",
]);
assert.equal(normalizedPlatforms.status, 0);
const normalizedPlatformsObservation = JSON.parse(readFileSync(normalizedPlatforms.output.outputPath, "utf8"));
assert.deepEqual(normalizedPlatformsObservation.room.clientPlatforms, ["browser", "ios", "macos"]);

const missingFlagRunId = `native-apple-room-interop-observation-missing-flag-${process.pid}`;
const missingFlagProofpack = createProofpack(fixture.appleDir, missingFlagRunId);
assert.equal(missingFlagProofpack.status, 0);
const missingFlag = runNode(createScriptPath, [
  "--proofpack-dir",
  missingFlagProofpack.output.proofpackDir,
  "--participant-count",
  "4",
  "--client-platforms",
  "browser,ios",
  "--confirm-current-build",
]);
assert.equal(missingFlag.status, 1);
assert.match(
  missingFlag.output.error,
  /confirmBrowserNativeMixedRoom|confirmThreePlusParticipants|confirmRemoteAudioAudible|confirmRemoteVideoRendered|confirmNoSecrets/
);

const weakParticipantRunId = `native-apple-room-interop-observation-weak-participant-${process.pid}`;
const weakParticipantProofpack = createProofpack(fixture.appleDir, weakParticipantRunId);
assert.equal(weakParticipantProofpack.status, 0);
const weakParticipant = createObservation(weakParticipantProofpack.output.proofpackDir, ["--participant-count", "2"]);
assert.equal(weakParticipant.status, 1);
assert.match(weakParticipant.output.error, /participantCount/);

const weakPlatformsRunId = `native-apple-room-interop-observation-weak-platforms-${process.pid}`;
const weakPlatformsProofpack = createProofpack(fixture.appleDir, weakPlatformsRunId);
assert.equal(weakPlatformsProofpack.status, 0);
const weakPlatforms = createObservation(weakPlatformsProofpack.output.proofpackDir, ["--client-platforms", "ios,ipados"]);
assert.equal(weakPlatforms.status, 1);
assert.match(weakPlatforms.output.error, /clientPlatforms/);

const badTimestampRunId = `native-apple-room-interop-observation-bad-timestamp-${process.pid}`;
const badTimestampProofpack = createProofpack(fixture.appleDir, badTimestampRunId);
assert.equal(badTimestampProofpack.status, 0);
const badTimestamp = createObservation(badTimestampProofpack.output.proofpackDir, ["--tested-at", "not-a-date"]);
assert.equal(badTimestamp.status, 1);
assert.match(badTimestamp.output.error, /testedAt/);

const staleTemplateRunId = `native-apple-room-interop-observation-stale-template-${process.pid}`;
const staleTemplateProofpack = createProofpack(fixture.appleDir, staleTemplateRunId);
assert.equal(staleTemplateProofpack.status, 0);
const staleManifest = JSON.parse(readFileSync(staleTemplateProofpack.output.proofpackPath, "utf8"));
const staleTemplatePath = resolve(rootDir, staleManifest.observationTemplates.roomInterop);
const staleTemplate = JSON.parse(readFileSync(staleTemplatePath, "utf8"));
staleTemplate.app.build = "14";
writeFixtureFile(staleTemplatePath, `${JSON.stringify(staleTemplate, null, 2)}\n`);
const staleTemplateResult = createObservation(staleTemplateProofpack.output.proofpackDir);
assert.equal(staleTemplateResult.status, 1);
assert.match(staleTemplateResult.output.error, /template\.app\.build/);

const unexpectedTemplateRunId = `native-apple-room-interop-observation-unexpected-template-${process.pid}`;
const unexpectedTemplateProofpack = createProofpack(fixture.appleDir, unexpectedTemplateRunId);
assert.equal(unexpectedTemplateProofpack.status, 0);
const unexpectedManifest = JSON.parse(readFileSync(unexpectedTemplateProofpack.output.proofpackPath, "utf8"));
const unexpectedTemplatePath = resolve(rootDir, unexpectedManifest.observationTemplates.roomInterop);
const unexpectedTemplate = JSON.parse(readFileSync(unexpectedTemplatePath, "utf8"));
unexpectedTemplate.rawLog = "not allowed";
unexpectedTemplate.room.screenshots = ["not allowed"];
writeFixtureFile(unexpectedTemplatePath, `${JSON.stringify(unexpectedTemplate, null, 2)}\n`);
const unexpectedTemplateResult = createObservation(unexpectedTemplateProofpack.output.proofpackDir);
assert.equal(unexpectedTemplateResult.status, 1);
assert.match(unexpectedTemplateResult.output.error, /template\.rawLog|template\.room\.screenshots/);

const staleRoomRunId = `native-apple-room-interop-observation-stale-room-${process.pid}`;
const staleRoomProofpack = createProofpack(fixture.appleDir, staleRoomRunId);
assert.equal(staleRoomProofpack.status, 0);
const staleRoomDraft = JSON.parse(readFileSync(staleRoomProofpack.output.evidenceDraft, "utf8"));
staleRoomDraft.roomId = "wrong-room-id";
writeFixtureFile(staleRoomProofpack.output.evidenceDraft, `${JSON.stringify(staleRoomDraft, null, 2)}\n`);
const staleRoomResult = createObservation(staleRoomProofpack.output.proofpackDir);
assert.equal(staleRoomResult.status, 1);
assert.match(staleRoomResult.output.error, /roomId\.match/);

const staleProjectFixture = makeAppleFixture();
const staleProjectRunId = `native-apple-room-interop-observation-stale-project-${process.pid}`;
const staleProjectProofpack = createProofpack(staleProjectFixture.appleDir, staleProjectRunId);
assert.equal(staleProjectProofpack.status, 0);
writeFixtureFile(
  resolve(staleProjectFixture.appleDir, "project.yml"),
  `targets:
  MeetingAssistAppleApp:
    settings:
      base:
        CURRENT_PROJECT_VERSION: 16
        MARKETING_VERSION: 1.0
        PRODUCT_BUNDLE_IDENTIFIER: co.thebonfire.meetingassist.ios
  MeetingAssistMacApp:
    settings:
      base:
        CURRENT_PROJECT_VERSION: 15
        MARKETING_VERSION: 1.0
        PRODUCT_BUNDLE_IDENTIFIER: co.thebonfire.meetingassist.mac
`
);
const staleProjectResult = createObservation(staleProjectProofpack.output.proofpackDir);
assert.equal(staleProjectResult.status, 1);
assert.match(staleProjectResult.output.error, /currentProject\.ios\.build/);

console.log("native-apple-create-room-interop-observation: 12 checks passed");
