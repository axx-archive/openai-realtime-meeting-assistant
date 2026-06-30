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
const createScriptPath = resolve(scriptsDir, "native-apple-create-notarization-observation.mjs");

const confirmationFlags = [
  "--confirm-developer-id-archive",
  "--confirm-notary-accepted",
  "--confirm-stapled-app",
  "--confirm-gatekeeper-accepted",
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
  const dir = mkdtempSync(resolve(tmpdir(), "meetingassist-notarization-observation-"));
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
    "notarization-observation-room-test",
    "--created-at",
    "2026-06-30T16:00:00Z",
    "--skip-gates",
  ]);
}

function createObservation(proofpackDir, extraArgs = []) {
  return runNode(createScriptPath, [
    "--proofpack-dir",
    proofpackDir,
    "--distribution-kind",
    "zip",
    "--distribution-filename",
    "MeetingAssistMacApp.zip",
    "--distribution-sha256",
    "9cde1a328d7a4e80b3c577f9e0f536b89cde1a328d7a4e80b3c577f9e0f536b8",
    "--notary-request-id",
    "8d7a1a32-9cde-4e80-b3c5-77f9e0f536b8",
    "--gatekeeper-source",
    "Notarized Developer ID",
    "--checked-at",
    "2026-06-30T16:18:00Z",
    ...confirmationFlags,
    ...extraArgs,
  ]);
}

const fixture = makeAppleFixture();
const runId = `native-apple-notarization-observation-test-${process.pid}`;
const proofpack = createProofpack(fixture.appleDir, runId);
assert.equal(proofpack.status, 0);
const draftBefore = readFileSync(proofpack.output.evidenceDraft, "utf8");

const created = createObservation(proofpack.output.proofpackDir);
assert.equal(created.status, 0);
assert.equal(created.output.ok, true);
assert.equal(created.output.runId, runId);
assert.equal(created.output.requestId, "8d7a1a32-9cde-4e80-b3c5-77f9e0f536b8");
assert.ok(created.output.outputRef.endsWith("/inbox/notarization-observation.json"));
assert.ok(existsSync(created.output.outputPath));
assert.ok(created.output.nextSteps.some((step) => step.includes("--kind notarization")));
assert.equal(readFileSync(proofpack.output.evidenceDraft, "utf8"), draftBefore);

const observation = JSON.parse(readFileSync(created.output.outputPath, "utf8"));
assert.equal(observation.schemaVersion, 1);
assert.equal(observation.artifactType, "native_macos_notarization_observation");
assert.equal(observation.status, "accepted");
assert.equal(observation.runId, runId);
assert.equal(observation.checkedAt, "2026-06-30T16:18:00Z");
assert.equal(observation.distributionArtifact.kind, "zip");
assert.equal(observation.distributionArtifact.filename, "MeetingAssistMacApp.zip");
assert.equal(observation.distributionArtifact.sha256, "9cde1a328d7a4e80b3c577f9e0f536b89cde1a328d7a4e80b3c577f9e0f536b8");
assert.equal(observation.app.version, "1.0");
assert.equal(observation.app.build, "15");
assert.equal(observation.app.target, "MeetingAssistMacApp");
assert.equal(observation.app.bundleIdentifier, "co.thebonfire.meetingassist.mac");
assert.equal(observation.signing.style, "developer_id");
assert.equal(observation.signing.signed, true);
assert.equal(observation.signing.hardenedRuntime, true);
assert.equal(observation.signing.timestamped, true);
assert.equal(observation.notarization.status, "accepted");
assert.equal(observation.notarization.issueCount, 0);
assert.equal(observation.staple.stapled, true);
assert.equal(observation.staple.validated, true);
assert.equal(observation.gatekeeper.assessment, "accepted");
assert.equal(observation.gatekeeper.source, "Notarized Developer ID");

const duplicate = createObservation(proofpack.output.proofpackDir);
assert.equal(duplicate.status, 1);
assert.match(duplicate.output.error, /Refusing to overwrite/);

const forced = createObservation(proofpack.output.proofpackDir, ["--force"]);
assert.equal(forced.status, 0);

const dmgRunId = `native-apple-notarization-observation-dmg-${process.pid}`;
const dmgProofpack = createProofpack(fixture.appleDir, dmgRunId);
assert.equal(dmgProofpack.status, 0);
const dmg = createObservation(dmgProofpack.output.proofpackDir, [
  "--distribution-kind",
  "DMG",
  "--distribution-filename",
  "MeetingAssistMacApp.dmg",
  "--distribution-sha256",
  "9CDE1A328D7A4E80B3C577F9E0F536B89CDE1A328D7A4E80B3C577F9E0F536B8",
]);
assert.equal(dmg.status, 0);
const dmgObservation = JSON.parse(readFileSync(dmg.output.outputPath, "utf8"));
assert.equal(dmgObservation.distributionArtifact.kind, "dmg");
assert.equal(dmgObservation.distributionArtifact.sha256, "9cde1a328d7a4e80b3c577f9e0f536b89cde1a328d7a4e80b3c577f9e0f536b8");

const missingFlagRunId = `native-apple-notarization-observation-missing-flag-${process.pid}`;
const missingFlagProofpack = createProofpack(fixture.appleDir, missingFlagRunId);
assert.equal(missingFlagProofpack.status, 0);
const missingFlag = runNode(createScriptPath, [
  "--proofpack-dir",
  missingFlagProofpack.output.proofpackDir,
  "--distribution-kind",
  "zip",
  "--distribution-filename",
  "MeetingAssistMacApp.zip",
  "--distribution-sha256",
  "9cde1a328d7a4e80b3c577f9e0f536b89cde1a328d7a4e80b3c577f9e0f536b8",
  "--notary-request-id",
  "8d7a1a32-9cde-4e80-b3c5-77f9e0f536b8",
  "--gatekeeper-source",
  "Notarized Developer ID",
  "--confirm-current-build",
]);
assert.equal(missingFlag.status, 1);
assert.match(missingFlag.output.error, /confirmDeveloperIdArchive|confirmNotarizedApp|confirmStapledApp|confirmGatekeeperAccepted|confirmNoSecrets/);

const badKindRunId = `native-apple-notarization-observation-bad-kind-${process.pid}`;
const badKindProofpack = createProofpack(fixture.appleDir, badKindRunId);
assert.equal(badKindProofpack.status, 0);
const badKind = createObservation(badKindProofpack.output.proofpackDir, ["--distribution-kind", "tar"]);
assert.equal(badKind.status, 1);
assert.match(badKind.output.error, /distributionKind/);

const badSHA256RunId = `native-apple-notarization-observation-bad-sha-${process.pid}`;
const badSHA256Proofpack = createProofpack(fixture.appleDir, badSHA256RunId);
assert.equal(badSHA256Proofpack.status, 0);
const badSHA256 = createObservation(badSHA256Proofpack.output.proofpackDir, ["--distribution-sha256", "not-a-sha"]);
assert.equal(badSHA256.status, 1);
assert.match(badSHA256.output.error, /distributionSHA256/);

const secretRequestRunId = `native-apple-notarization-observation-secret-request-${process.pid}`;
const secretRequestProofpack = createProofpack(fixture.appleDir, secretRequestRunId);
assert.equal(secretRequestProofpack.status, 0);
const secretRequest = createObservation(secretRequestProofpack.output.proofpackDir, ["--notary-request-id", "A1B2C3D4E5"]);
assert.equal(secretRequest.status, 1);
assert.match(secretRequest.output.error, /notaryRequestId|unsafe/);

const unsafeSourceRunId = `native-apple-notarization-observation-unsafe-source-${process.pid}`;
const unsafeSourceProofpack = createProofpack(fixture.appleDir, unsafeSourceRunId);
assert.equal(unsafeSourceProofpack.status, 0);
const unsafeSource = createObservation(unsafeSourceProofpack.output.proofpackDir, [
  "--gatekeeper-source",
  "Developer ID Application: Axxon Labs (A1B2C3D4E5)",
]);
assert.equal(unsafeSource.status, 1);
assert.match(unsafeSource.output.error, /unsafe/);

const badFilenameRunId = `native-apple-notarization-observation-bad-filename-${process.pid}`;
const badFilenameProofpack = createProofpack(fixture.appleDir, badFilenameRunId);
assert.equal(badFilenameProofpack.status, 0);
const badFilename = createObservation(badFilenameProofpack.output.proofpackDir, ["--distribution-filename", "../MeetingAssistMacApp.zip"]);
assert.equal(badFilename.status, 1);
assert.match(badFilename.output.error, /distributionFilename/);

const staleTemplateRunId = `native-apple-notarization-observation-stale-template-${process.pid}`;
const staleTemplateProofpack = createProofpack(fixture.appleDir, staleTemplateRunId);
assert.equal(staleTemplateProofpack.status, 0);
const staleManifest = JSON.parse(readFileSync(staleTemplateProofpack.output.proofpackPath, "utf8"));
const staleTemplatePath = resolve(rootDir, staleManifest.observationTemplates.notarization);
const staleTemplate = JSON.parse(readFileSync(staleTemplatePath, "utf8"));
staleTemplate.app.build = "14";
writeFixtureFile(staleTemplatePath, `${JSON.stringify(staleTemplate, null, 2)}\n`);
const staleTemplateResult = createObservation(staleTemplateProofpack.output.proofpackDir);
assert.equal(staleTemplateResult.status, 1);
assert.match(staleTemplateResult.output.error, /template\.app\.build/);

const unexpectedTemplateRunId = `native-apple-notarization-observation-unexpected-template-${process.pid}`;
const unexpectedTemplateProofpack = createProofpack(fixture.appleDir, unexpectedTemplateRunId);
assert.equal(unexpectedTemplateProofpack.status, 0);
const unexpectedManifest = JSON.parse(readFileSync(unexpectedTemplateProofpack.output.proofpackPath, "utf8"));
const unexpectedTemplatePath = resolve(rootDir, unexpectedManifest.observationTemplates.notarization);
const unexpectedTemplate = JSON.parse(readFileSync(unexpectedTemplatePath, "utf8"));
unexpectedTemplate.notarytoolLog = "accepted with local profile";
unexpectedTemplate.notarization.profile = "not allowed";
writeFixtureFile(unexpectedTemplatePath, `${JSON.stringify(unexpectedTemplate, null, 2)}\n`);
const unexpectedTemplateResult = createObservation(unexpectedTemplateProofpack.output.proofpackDir);
assert.equal(unexpectedTemplateResult.status, 1);
assert.match(unexpectedTemplateResult.output.error, /template\.notarytoolLog|template\.notarization\.profile/);

const staleRoomRunId = `native-apple-notarization-observation-stale-room-${process.pid}`;
const staleRoomProofpack = createProofpack(fixture.appleDir, staleRoomRunId);
assert.equal(staleRoomProofpack.status, 0);
const staleRoomDraft = JSON.parse(readFileSync(staleRoomProofpack.output.evidenceDraft, "utf8"));
staleRoomDraft.roomId = "wrong-room-id";
writeFixtureFile(staleRoomProofpack.output.evidenceDraft, `${JSON.stringify(staleRoomDraft, null, 2)}\n`);
const staleRoomResult = createObservation(staleRoomProofpack.output.proofpackDir);
assert.equal(staleRoomResult.status, 1);
assert.match(staleRoomResult.output.error, /roomId\.match/);

const staleProjectFixture = makeAppleFixture();
const staleProjectRunId = `native-apple-notarization-observation-stale-project-${process.pid}`;
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

const staleMacOnlyProjectFixture = makeAppleFixture();
const staleMacOnlyProjectRunId = `native-apple-notarization-observation-stale-mac-only-project-${process.pid}`;
const staleMacOnlyProjectProofpack = createProofpack(staleMacOnlyProjectFixture.appleDir, staleMacOnlyProjectRunId);
assert.equal(staleMacOnlyProjectProofpack.status, 0);
writeFixtureFile(
  resolve(staleMacOnlyProjectFixture.appleDir, "project.yml"),
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
        CURRENT_PROJECT_VERSION: 16
        MARKETING_VERSION: 1.0
        PRODUCT_BUNDLE_IDENTIFIER: co.thebonfire.meetingassist.mac
`
);
const staleMacOnlyProjectResult = createObservation(staleMacOnlyProjectProofpack.output.proofpackDir);
assert.equal(staleMacOnlyProjectResult.status, 1);
assert.match(staleMacOnlyProjectResult.output.error, /currentProject\.build/);

console.log("native-apple-create-notarization-observation: 14 checks passed");
