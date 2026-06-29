#!/usr/bin/env node
import assert from "node:assert/strict";
import { mkdtempSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { spawnSync } from "node:child_process";

const scriptsDir = dirname(fileURLToPath(import.meta.url));
const rootDir = resolve(scriptsDir, "..");
const proofpackScriptPath = resolve(scriptsDir, "native-apple-release-proofpack.mjs");
const promoteScriptPath = resolve(scriptsDir, "native-apple-promote-distribution-evidence.mjs");

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
  const dir = mkdtempSync(resolve(tmpdir(), "meetingassist-promote-distribution-"));
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
    "distribution-promotion-room-test",
    "--created-at",
    "2026-06-29T20:00:00Z",
    "--skip-gates",
  ]);
}

function testFlightObservation(overrides = {}) {
  const base = {
    schemaVersion: 1,
    artifactType: "native_testflight_upload_observation",
    status: "observed",
    runId: "",
    uploadedAt: "2026-06-29T20:15:00Z",
    app: {
      version: "1.0",
      build: "15",
      target: "MeetingAssistAppleApp",
      clientPlatform: "ios",
      bundleIdentifier: "co.thebonfire.meetingassist.ios",
    },
    appStoreConnect: {
      buildId: "asc-build-20260629-15",
      processingStatus: "ready",
    },
  };
  return {
    ...base,
    ...overrides,
    app: { ...base.app, ...(overrides.app ?? {}) },
    appStoreConnect: { ...base.appStoreConnect, ...(overrides.appStoreConnect ?? {}) },
  };
}

function notarizationObservation(overrides = {}) {
  const base = {
    schemaVersion: 1,
    artifactType: "native_macos_notarization_observation",
    status: "accepted",
    runId: "",
    checkedAt: "2026-06-29T20:25:00Z",
    distributionArtifact: {
      kind: "zip",
      filename: "MeetingAssistMacApp.zip",
      sha256: "9cde1a328d7a4e80b3c577f9e0f536b89cde1a328d7a4e80b3c577f9e0f536b8",
    },
    app: {
      version: "1.0",
      build: "15",
      target: "MeetingAssistMacApp",
      clientPlatform: "macos",
      bundleIdentifier: "co.thebonfire.meetingassist.mac",
    },
    signing: {
      style: "developer_id",
      signed: true,
      hardenedRuntime: true,
      timestamped: true,
    },
    notarization: {
      requestId: "8d7a1a32-9cde-4e80-b3c5-77f9e0f536b8",
      status: "accepted",
      issueCount: 0,
    },
    staple: {
      stapled: true,
      validated: true,
    },
    gatekeeper: {
      assessment: "accepted",
      source: "Notarized Developer ID",
    },
  };
  return {
    ...base,
    ...overrides,
    distributionArtifact: { ...base.distributionArtifact, ...(overrides.distributionArtifact ?? {}) },
    app: { ...base.app, ...(overrides.app ?? {}) },
    signing: { ...base.signing, ...(overrides.signing ?? {}) },
    notarization: { ...base.notarization, ...(overrides.notarization ?? {}) },
    staple: { ...base.staple, ...(overrides.staple ?? {}) },
    gatekeeper: { ...base.gatekeeper, ...(overrides.gatekeeper ?? {}) },
  };
}

function writeObservation(dir, name, observation) {
  const path = resolve(dir, name);
  writeFixtureFile(path, `${JSON.stringify(observation, null, 2)}\n`);
  return path;
}

function promote(args) {
  return runNode(promoteScriptPath, args);
}

const fixture = makeAppleFixture();
const runId = `native-apple-promote-distribution-test-${process.pid}`;
const created = createProofpack(fixture.appleDir, runId);
assert.equal(created.status, 0);
assert.equal(created.output.ok, true);

function boundTestFlightObservation(overrides = {}) {
  return testFlightObservation({ runId, ...overrides });
}

function boundNotarizationObservation(overrides = {}) {
  return notarizationObservation({ runId, ...overrides });
}

const testFlightPath = writeObservation(fixture.dir, "testflight-observation.json", boundTestFlightObservation());
const promotedTestFlight = promote([
  "--proofpack-dir",
  created.output.proofpackDir,
  "--kind",
  "testflight",
  "--input",
  testFlightPath,
  "--confirm-app-store-connect-upload",
  "--confirm-no-secrets",
  "--confirm-current-build",
  "--promoted-at",
  "2026-06-29T20:20:00Z",
]);
assert.equal(promotedTestFlight.status, 0);
assert.equal(promotedTestFlight.output.ok, true);
assert.equal(promotedTestFlight.output.kind, "testflight");

let draft = JSON.parse(readFileSync(promotedTestFlight.output.evidenceDraft, "utf8"));
assert.equal(draft.testFlight.status, "ready");
assert.equal(draft.testFlight.appStoreConnectBuildId, "asc-build-20260629-15");
assert.equal(draft.testFlight.uploadedAt, "2026-06-29T20:15:00Z");
const testFlightArtifact = JSON.parse(readFileSync(promotedTestFlight.output.artifactPath, "utf8"));
assert.equal(testFlightArtifact.claimScope, "app_store_connect_upload");
assert.equal(testFlightArtifact.releaseEligible, true);
assert.equal(testFlightArtifact.app.build, "15");
assert.equal(testFlightArtifact.appStoreConnect.buildId, "asc-build-20260629-15");
assert.equal(testFlightArtifact.promotion.sourceRunId, runId);
assert.equal(testFlightArtifact.promotion.sourceUploadedAt, "2026-06-29T20:15:00Z");
assert.equal(testFlightArtifact.promotion.operatorConfirmedAppStoreConnectUpload, true);
assert.equal(testFlightArtifact.promotion.operatorConfirmedNoSecrets, true);

const duplicateTestFlight = promote([
  "--proofpack-dir",
  created.output.proofpackDir,
  "--kind",
  "testflight",
  "--input",
  testFlightPath,
  "--confirm-app-store-connect-upload",
  "--confirm-no-secrets",
  "--confirm-current-build",
]);
assert.equal(duplicateTestFlight.status, 1);
assert.match(duplicateTestFlight.output.error, /already passed/);

const blankRunRejected = promote([
  "--proofpack-dir",
  created.output.proofpackDir,
  "--kind",
  "testflight",
  "--input",
  writeObservation(fixture.dir, "blank-run.json", testFlightObservation()),
  "--confirm-app-store-connect-upload",
  "--confirm-no-secrets",
  "--confirm-current-build",
  "--force",
]);
assert.equal(blankRunRejected.status, 1);
assert.match(blankRunRejected.output.error, /runId:empty/);

const notarizationPath = writeObservation(fixture.dir, "notarization-observation.json", boundNotarizationObservation());
const promotedNotarization = promote([
  "--proofpack-dir",
  created.output.proofpackDir,
  "--kind",
  "notarization",
  "--input",
  notarizationPath,
  "--confirm-developer-id-archive",
  "--confirm-notary-accepted",
  "--confirm-stapled-app",
  "--confirm-gatekeeper-accepted",
  "--confirm-current-build",
  "--promoted-at",
  "2026-06-29T20:30:00Z",
]);
assert.equal(promotedNotarization.status, 0);
assert.equal(promotedNotarization.output.ok, true);
assert.equal(promotedNotarization.output.kind, "notarization");

draft = JSON.parse(readFileSync(promotedNotarization.output.evidenceDraft, "utf8"));
assert.equal(draft.macNotarization.status, "accepted");
assert.equal(draft.macNotarization.requestId, "8d7a1a32-9cde-4e80-b3c5-77f9e0f536b8");
assert.equal(draft.macNotarization.stapled, true);
assert.equal(draft.macNotarization.checkedAt, "2026-06-29T20:25:00Z");
const notarizationArtifact = JSON.parse(readFileSync(promotedNotarization.output.artifactPath, "utf8"));
assert.equal(notarizationArtifact.claimScope, "macos_notarization");
assert.equal(notarizationArtifact.releaseEligible, true);
assert.equal(notarizationArtifact.app.target, "MeetingAssistMacApp");
assert.equal(notarizationArtifact.distributionArtifact.kind, "zip");
assert.equal(notarizationArtifact.signing.hardenedRuntime, true);
assert.equal(notarizationArtifact.staple.validated, true);
assert.equal(notarizationArtifact.gatekeeper.assessment, "accepted");
assert.equal(notarizationArtifact.promotion.sourceRunId, runId);
assert.equal(notarizationArtifact.promotion.sourceCheckedAt, "2026-06-29T20:25:00Z");
assert.equal(notarizationArtifact.promotion.operatorConfirmedStapledApp, true);
assert.equal(notarizationArtifact.promotion.operatorConfirmedGatekeeperAccepted, true);

const missingUploadConfirm = promote([
  "--proofpack-dir",
  created.output.proofpackDir,
  "--kind",
  "testflight",
  "--input",
  writeObservation(fixture.dir, "missing-upload-confirm.json", boundTestFlightObservation()),
  "--confirm-current-build",
  "--force",
]);
assert.equal(missingUploadConfirm.status, 1);
assert.match(missingUploadConfirm.output.error, /confirmAppleUpload/);

const wrongBuildRejected = promote([
  "--proofpack-dir",
  created.output.proofpackDir,
  "--kind",
  "testflight",
  "--input",
  writeObservation(fixture.dir, "wrong-build.json", boundTestFlightObservation({ app: { build: "14" } })),
  "--confirm-app-store-connect-upload",
  "--confirm-no-secrets",
  "--confirm-current-build",
  "--force",
]);
assert.equal(wrongBuildRejected.status, 1);
assert.match(wrongBuildRejected.output.error, /app.build/);

const unsafeTestFlightRejected = promote([
  "--proofpack-dir",
  created.output.proofpackDir,
  "--kind",
  "testflight",
  "--input",
  writeObservation(
    fixture.dir,
    "unsafe-testflight.json",
    boundTestFlightObservation({ appStoreConnectApiKey: "-----BEGIN PRIVATE KEY-----\nabc\n-----END PRIVATE KEY-----" })
  ),
  "--confirm-app-store-connect-upload",
  "--confirm-no-secrets",
  "--confirm-current-build",
  "--force",
]);
assert.equal(unsafeTestFlightRejected.status, 1);
assert.match(unsafeTestFlightRejected.output.error, /unsafeContent/);

const missingNotaryConfirm = promote([
  "--proofpack-dir",
  created.output.proofpackDir,
  "--kind",
  "notarization",
  "--input",
  writeObservation(fixture.dir, "missing-notary-confirm.json", boundNotarizationObservation()),
  "--confirm-current-build",
  "--force",
]);
assert.equal(missingNotaryConfirm.status, 1);
assert.match(missingNotaryConfirm.output.error, /confirmDeveloperIdArchive|confirmNotarizedApp|confirmStapledApp|confirmGatekeeperAccepted/);

const unstapledRejected = promote([
  "--proofpack-dir",
  created.output.proofpackDir,
  "--kind",
  "notarization",
  "--input",
  writeObservation(
    fixture.dir,
    "unstapled.json",
    boundNotarizationObservation({ staple: { stapled: false, validated: false }, gatekeeper: { assessment: "rejected" } })
  ),
  "--confirm-developer-id-archive",
  "--confirm-notary-accepted",
  "--confirm-stapled-app",
  "--confirm-gatekeeper-accepted",
  "--confirm-current-build",
  "--force",
]);
assert.equal(unstapledRejected.status, 1);
assert.match(unstapledRejected.output.error, /staple.stapled|staple.validated|gatekeeper.assessment/);

const unsafeNotarizationRejected = promote([
  "--proofpack-dir",
  created.output.proofpackDir,
  "--kind",
  "notarization",
  "--input",
  writeObservation(
    fixture.dir,
    "unsafe-notarization.json",
    boundNotarizationObservation({ notarytoolLog: "submitted with keychain profile secret-profile" })
  ),
  "--confirm-developer-id-archive",
  "--confirm-notary-accepted",
  "--confirm-stapled-app",
  "--confirm-gatekeeper-accepted",
  "--confirm-current-build",
  "--force",
]);
assert.equal(unsafeNotarizationRejected.status, 1);
assert.match(unsafeNotarizationRejected.output.error, /unsafeContent/);

rmSync(fixture.dir, { recursive: true, force: true });

console.log("native-apple-promote-distribution-evidence: 11 checks passed");
