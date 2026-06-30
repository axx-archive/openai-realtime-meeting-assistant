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
const createScriptPath = resolve(scriptsDir, "native-apple-create-testflight-observation.mjs");

const confirmationFlags = [
  "--confirm-app-store-connect-upload",
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
  const dir = mkdtempSync(resolve(tmpdir(), "meetingassist-testflight-observation-"));
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
    "testflight-observation-room-test",
    "--created-at",
    "2026-06-30T15:00:00Z",
    "--skip-gates",
  ]);
}

function createObservation(proofpackDir, extraArgs = []) {
  return runNode(createScriptPath, [
    "--proofpack-dir",
    proofpackDir,
    "--app-store-connect-build-id",
    "asc-build-20260630-15",
    "--processing-status",
    "ready",
    "--uploaded-at",
    "2026-06-30T15:18:00Z",
    ...confirmationFlags,
    ...extraArgs,
  ]);
}

const fixture = makeAppleFixture();
const runId = `native-apple-testflight-observation-test-${process.pid}`;
const proofpack = createProofpack(fixture.appleDir, runId);
assert.equal(proofpack.status, 0);

const created = createObservation(proofpack.output.proofpackDir);
assert.equal(created.status, 0);
assert.equal(created.output.ok, true);
assert.equal(created.output.runId, runId);
assert.equal(created.output.processingStatus, "ready");
assert.ok(created.output.outputRef.endsWith("/inbox/testflight-observation.json"));
assert.ok(existsSync(created.output.outputPath));
assert.ok(created.output.nextSteps.some((step) => step.includes("--kind testflight")));

const observation = JSON.parse(readFileSync(created.output.outputPath, "utf8"));
assert.equal(observation.schemaVersion, 1);
assert.equal(observation.artifactType, "native_testflight_upload_observation");
assert.equal(observation.status, "observed");
assert.equal(observation.runId, runId);
assert.equal(observation.uploadedAt, "2026-06-30T15:18:00Z");
assert.equal(observation.app.version, "1.0");
assert.equal(observation.app.build, "15");
assert.equal(observation.app.target, "MeetingAssistAppleApp");
assert.equal(observation.app.bundleIdentifier, "co.thebonfire.meetingassist.ios");
assert.equal(observation.appStoreConnect.buildId, "asc-build-20260630-15");
assert.equal(observation.appStoreConnect.processingStatus, "ready");

const duplicate = createObservation(proofpack.output.proofpackDir);
assert.equal(duplicate.status, 1);
assert.match(duplicate.output.error, /Refusing to overwrite/);

const forced = createObservation(proofpack.output.proofpackDir, ["--force"]);
assert.equal(forced.status, 0);

const uppercaseStatusRunId = `native-apple-testflight-observation-uppercase-status-${process.pid}`;
const uppercaseStatusProofpack = createProofpack(fixture.appleDir, uppercaseStatusRunId);
assert.equal(uppercaseStatusProofpack.status, 0);
const uppercaseStatus = createObservation(uppercaseStatusProofpack.output.proofpackDir, [
  "--processing-status",
  "Processing",
]);
assert.equal(uppercaseStatus.status, 0);
const uppercaseStatusObservation = JSON.parse(readFileSync(uppercaseStatus.output.outputPath, "utf8"));
assert.equal(uppercaseStatusObservation.appStoreConnect.processingStatus, "processing");

const numericBuildRunId = `native-apple-testflight-observation-numeric-build-${process.pid}`;
const numericBuildProofpack = createProofpack(fixture.appleDir, numericBuildRunId);
assert.equal(numericBuildProofpack.status, 0);
const numericBuild = createObservation(numericBuildProofpack.output.proofpackDir, [
  "--app-store-connect-build-id",
  "1234567890",
]);
assert.equal(numericBuild.status, 0);
const numericBuildObservation = JSON.parse(readFileSync(numericBuild.output.outputPath, "utf8"));
assert.equal(numericBuildObservation.appStoreConnect.buildId, "1234567890");

const missingFlagRunId = `native-apple-testflight-observation-missing-flag-${process.pid}`;
const missingFlagProofpack = createProofpack(fixture.appleDir, missingFlagRunId);
assert.equal(missingFlagProofpack.status, 0);
const missingFlag = runNode(createScriptPath, [
  "--proofpack-dir",
  missingFlagProofpack.output.proofpackDir,
  "--app-store-connect-build-id",
  "asc-build-20260630-15",
  "--processing-status",
  "ready",
  "--confirm-current-build",
]);
assert.equal(missingFlag.status, 1);
assert.match(missingFlag.output.error, /confirmAppleUpload|confirmNoSecrets/);

const badStatusRunId = `native-apple-testflight-observation-bad-status-${process.pid}`;
const badStatusProofpack = createProofpack(fixture.appleDir, badStatusRunId);
assert.equal(badStatusProofpack.status, 0);
const badStatus = createObservation(badStatusProofpack.output.proofpackDir, ["--processing-status", "external-testing-ready"]);
assert.equal(badStatus.status, 1);
assert.match(badStatus.output.error, /processingStatus/);

const secretBuildRunId = `native-apple-testflight-observation-secret-build-${process.pid}`;
const secretBuildProofpack = createProofpack(fixture.appleDir, secretBuildRunId);
assert.equal(secretBuildProofpack.status, 0);
const secretBuild = createObservation(secretBuildProofpack.output.proofpackDir, [
  "--app-store-connect-build-id",
  "A1B2C3D4E5",
]);
assert.equal(secretBuild.status, 1);
assert.match(secretBuild.output.error, /appStoreConnectBuildId|unsafe/);

const staleTemplateRunId = `native-apple-testflight-observation-stale-template-${process.pid}`;
const staleTemplateProofpack = createProofpack(fixture.appleDir, staleTemplateRunId);
assert.equal(staleTemplateProofpack.status, 0);
const staleManifest = JSON.parse(readFileSync(staleTemplateProofpack.output.proofpackPath, "utf8"));
const staleTemplatePath = resolve(rootDir, staleManifest.observationTemplates.testFlight);
const staleTemplate = JSON.parse(readFileSync(staleTemplatePath, "utf8"));
staleTemplate.app.build = "14";
writeFixtureFile(staleTemplatePath, `${JSON.stringify(staleTemplate, null, 2)}\n`);
const staleTemplateResult = createObservation(staleTemplateProofpack.output.proofpackDir);
assert.equal(staleTemplateResult.status, 1);
assert.match(staleTemplateResult.output.error, /template\.app\.build/);

const unexpectedTemplateRunId = `native-apple-testflight-observation-unexpected-template-${process.pid}`;
const unexpectedTemplateProofpack = createProofpack(fixture.appleDir, unexpectedTemplateRunId);
assert.equal(unexpectedTemplateProofpack.status, 0);
const unexpectedManifest = JSON.parse(readFileSync(unexpectedTemplateProofpack.output.proofpackPath, "utf8"));
const unexpectedTemplatePath = resolve(rootDir, unexpectedManifest.observationTemplates.testFlight);
const unexpectedTemplate = JSON.parse(readFileSync(unexpectedTemplatePath, "utf8"));
unexpectedTemplate.externalTestingAvailable = true;
unexpectedTemplate.appStoreConnect.uploadLog = "not allowed";
writeFixtureFile(unexpectedTemplatePath, `${JSON.stringify(unexpectedTemplate, null, 2)}\n`);
const unexpectedTemplateResult = createObservation(unexpectedTemplateProofpack.output.proofpackDir);
assert.equal(unexpectedTemplateResult.status, 1);
assert.match(unexpectedTemplateResult.output.error, /template\.externalTestingAvailable|template\.appStoreConnect\.uploadLog/);

const staleRoomRunId = `native-apple-testflight-observation-stale-room-${process.pid}`;
const staleRoomProofpack = createProofpack(fixture.appleDir, staleRoomRunId);
assert.equal(staleRoomProofpack.status, 0);
const staleRoomDraft = JSON.parse(readFileSync(staleRoomProofpack.output.evidenceDraft, "utf8"));
staleRoomDraft.roomId = "wrong-room-id";
writeFixtureFile(staleRoomProofpack.output.evidenceDraft, `${JSON.stringify(staleRoomDraft, null, 2)}\n`);
const staleRoomResult = createObservation(staleRoomProofpack.output.proofpackDir);
assert.equal(staleRoomResult.status, 1);
assert.match(staleRoomResult.output.error, /roomId\.match/);

const staleProjectFixture = makeAppleFixture();
const staleProjectRunId = `native-apple-testflight-observation-stale-project-${process.pid}`;
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
        CURRENT_PROJECT_VERSION: 16
        MARKETING_VERSION: 1.0
        PRODUCT_BUNDLE_IDENTIFIER: co.thebonfire.meetingassist.mac
`
);
const staleProjectResult = createObservation(staleProjectProofpack.output.proofpackDir);
assert.equal(staleProjectResult.status, 1);
assert.match(staleProjectResult.output.error, /currentProject\.build/);

console.log("native-apple-create-testflight-observation: 12 checks passed");
