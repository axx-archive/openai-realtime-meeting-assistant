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

function appStoreReviewObservation(overrides = {}) {
  const base = {
    schemaVersion: 1,
    artifactType: "native_app_store_review_metadata_observation",
    status: "observed",
    runId: "",
    reviewedAt: "2026-06-29T20:18:00Z",
    app: {
      version: "1.0",
      build: "15",
      target: "MeetingAssistAppleApp",
      clientPlatform: "ios",
      bundleIdentifier: "co.thebonfire.meetingassist.ios",
    },
    metadata: {
      supportURL: "https://thebonfire.xyz/support",
      privacyPolicyURL: "https://thebonfire.xyz/privacy",
      descriptionReady: true,
      keywordsReady: true,
      screenshotsReady: true,
      appPrivacyReady: true,
      ageRatingComplete: true,
      exportComplianceComplete: true,
      testInformationReady: true,
      externalTestingGroupReady: true,
    },
  };
  return {
    ...base,
    ...overrides,
    app: { ...base.app, ...(overrides.app ?? {}) },
    metadata: { ...base.metadata, ...(overrides.metadata ?? {}) },
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

function boundAppStoreReviewObservation(overrides = {}) {
  return appStoreReviewObservation({ runId, ...overrides });
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

const appStoreReviewPath = writeObservation(fixture.dir, "app-store-review-observation.json", boundAppStoreReviewObservation());
const promotedAppStoreReview = promote([
  "--proofpack-dir",
  created.output.proofpackDir,
  "--kind",
  "app-review",
  "--input",
  appStoreReviewPath,
  "--confirm-review-metadata-complete",
  "--confirm-app-privacy-complete",
  "--confirm-external-testing-ready",
  "--confirm-no-secrets",
  "--confirm-current-build",
  "--promoted-at",
  "2026-06-29T20:21:00Z",
]);
assert.equal(promotedAppStoreReview.status, 0);
assert.equal(promotedAppStoreReview.output.ok, true);
assert.equal(promotedAppStoreReview.output.kind, "app-review");

draft = JSON.parse(readFileSync(promotedAppStoreReview.output.evidenceDraft, "utf8"));
assert.equal(draft.appStoreReview.status, "ready");
assert.equal(draft.appStoreReview.supportURL, "https://thebonfire.xyz/support");
assert.equal(draft.appStoreReview.privacyPolicyURL, "https://thebonfire.xyz/privacy");
assert.equal(draft.appStoreReview.externalTestingGroupReady, true);
const appStoreReviewArtifact = JSON.parse(readFileSync(promotedAppStoreReview.output.artifactPath, "utf8"));
assert.equal(appStoreReviewArtifact.claimScope, "app_store_external_testing_review");
assert.equal(appStoreReviewArtifact.releaseEligible, true);
assert.equal(appStoreReviewArtifact.metadata.keywordsReady, true);
assert.equal(appStoreReviewArtifact.releaseEvidenceSummary.externalTestingReady, true);
assert.equal(appStoreReviewArtifact.promotion.sourceRunId, runId);
assert.equal(appStoreReviewArtifact.promotion.sourceReviewedAt, "2026-06-29T20:18:00Z");
assert.equal(appStoreReviewArtifact.promotion.operatorConfirmedReviewMetadataComplete, true);
assert.equal(appStoreReviewArtifact.promotion.operatorConfirmedAppPrivacyComplete, true);

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

const missingAppReviewConfirm = promote([
  "--proofpack-dir",
  created.output.proofpackDir,
  "--kind",
  "app-review",
  "--input",
  writeObservation(fixture.dir, "missing-app-review-confirm.json", boundAppStoreReviewObservation()),
  "--confirm-current-build",
  "--force",
]);
assert.equal(missingAppReviewConfirm.status, 1);
assert.match(missingAppReviewConfirm.output.error, /confirmReviewMetadataComplete|confirmAppPrivacyComplete|confirmExternalTestingReady|confirmNoSecrets/);

const unsafeAppReviewRejected = promote([
  "--proofpack-dir",
  created.output.proofpackDir,
  "--kind",
  "app-review",
  "--input",
  writeObservation(
    fixture.dir,
    "unsafe-app-review.json",
    boundAppStoreReviewObservation({ metadata: { supportURL: "http://thebonfire.xyz/support", reviewerContact: "operator@example.com" } })
  ),
  "--confirm-review-metadata-complete",
  "--confirm-app-privacy-complete",
  "--confirm-external-testing-ready",
  "--confirm-no-secrets",
  "--confirm-current-build",
  "--force",
]);
assert.equal(unsafeAppReviewRejected.status, 1);
assert.match(unsafeAppReviewRejected.output.error, /metadata.supportURL|unsafeContent/);

for (const [label, url] of [
  ["localhost", "https://localhost/support"],
  ["private-ipv4", "https://192.168.1.2/support"],
  ["ipv6-loopback", "https://[::1]/support"],
  ["ipv6-private", "https://[fd00::1]/support"],
]) {
  const privateURLRejected = promote([
    "--proofpack-dir",
    created.output.proofpackDir,
    "--kind",
    "app-review",
    "--input",
    writeObservation(
      fixture.dir,
      `private-${label}-app-review.json`,
      boundAppStoreReviewObservation({ metadata: { supportURL: url } })
    ),
    "--confirm-review-metadata-complete",
    "--confirm-app-privacy-complete",
    "--confirm-external-testing-ready",
    "--confirm-no-secrets",
    "--confirm-current-build",
    "--force",
  ]);
  assert.equal(privateURLRejected.status, 1);
  assert.match(privateURLRejected.output.error, /metadata.supportURL/);
}

const unexpectedAppReviewRejected = promote([
  "--proofpack-dir",
  created.output.proofpackDir,
  "--kind",
  "app-review",
  "--input",
  writeObservation(
    fixture.dir,
    "unexpected-app-review.json",
    boundAppStoreReviewObservation({ appleReviewStatus: "approved", metadata: { reviewerNotes: "not allowed" } })
  ),
  "--confirm-review-metadata-complete",
  "--confirm-app-privacy-complete",
  "--confirm-external-testing-ready",
  "--confirm-no-secrets",
  "--confirm-current-build",
  "--force",
]);
assert.equal(unexpectedAppReviewRejected.status, 1);
assert.match(unexpectedAppReviewRejected.output.error, /input\.appleReviewStatus|input\.metadata\.reviewerNotes/);

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

console.log("native-apple-promote-distribution-evidence: 19 checks passed");
