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
const createScriptPath = resolve(scriptsDir, "native-apple-create-app-review-observation.mjs");

const confirmationFlags = [
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
  const dir = mkdtempSync(resolve(tmpdir(), "meetingassist-app-review-observation-"));
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
    "app-review-observation-room-test",
    "--created-at",
    "2026-06-29T20:00:00Z",
    "--skip-gates",
  ]);
}

function createObservation(proofpackDir, extraArgs = []) {
  return runNode(createScriptPath, [
    "--proofpack-dir",
    proofpackDir,
    "--support-url",
    "https://thebonfire.xyz/support",
    "--privacy-policy-url",
    "https://thebonfire.xyz/privacy",
    "--reviewed-at",
    "2026-06-29T20:18:00Z",
    ...confirmationFlags,
    ...extraArgs,
  ]);
}

const fixture = makeAppleFixture();
const runId = `native-apple-app-review-observation-test-${process.pid}`;
const proofpack = createProofpack(fixture.appleDir, runId);
assert.equal(proofpack.status, 0);

const created = createObservation(proofpack.output.proofpackDir);
assert.equal(created.status, 0);
assert.equal(created.output.ok, true);
assert.equal(created.output.runId, runId);
assert.ok(created.output.outputRef.endsWith("/inbox/app-store-review-observation.json"));
assert.ok(existsSync(created.output.outputPath));
assert.ok(created.output.nextSteps.some((step) => step.includes("--kind app-review")));

const observation = JSON.parse(readFileSync(created.output.outputPath, "utf8"));
assert.equal(observation.schemaVersion, 1);
assert.equal(observation.artifactType, "native_app_store_review_metadata_observation");
assert.equal(observation.status, "observed");
assert.equal(observation.runId, runId);
assert.equal(observation.reviewedAt, "2026-06-29T20:18:00Z");
assert.equal(observation.app.version, "1.0");
assert.equal(observation.app.build, "15");
assert.equal(observation.app.target, "MeetingAssistAppleApp");
assert.equal(observation.app.bundleIdentifier, "co.thebonfire.meetingassist.ios");
assert.equal(observation.metadata.supportURL, "https://thebonfire.xyz/support");
assert.equal(observation.metadata.privacyPolicyURL, "https://thebonfire.xyz/privacy");
for (const key of [
  "descriptionReady",
  "keywordsReady",
  "screenshotsReady",
  "appPrivacyReady",
  "ageRatingComplete",
  "exportComplianceComplete",
  "testInformationReady",
  "externalTestingGroupReady",
]) {
  assert.equal(observation.metadata[key], true);
}

const duplicate = createObservation(proofpack.output.proofpackDir);
assert.equal(duplicate.status, 1);
assert.match(duplicate.output.error, /Refusing to overwrite/);

const forced = createObservation(proofpack.output.proofpackDir, ["--force"]);
assert.equal(forced.status, 0);

const missingFlagRunId = `native-apple-app-review-observation-missing-flag-${process.pid}`;
const missingFlagProofpack = createProofpack(fixture.appleDir, missingFlagRunId);
assert.equal(missingFlagProofpack.status, 0);
const missingFlag = runNode(createScriptPath, [
  "--proofpack-dir",
  missingFlagProofpack.output.proofpackDir,
  "--support-url",
  "https://thebonfire.xyz/support",
  "--privacy-policy-url",
  "https://thebonfire.xyz/privacy",
  "--confirm-description-ready",
  "--confirm-keywords-ready",
  "--confirm-current-build",
  "--confirm-no-secrets",
]);
assert.equal(missingFlag.status, 1);
assert.match(missingFlag.output.error, /confirmScreenshotsReady|confirmAppPrivacyReady|confirmExternalTestingGroupReady/);

const privateURLRunId = `native-apple-app-review-observation-private-url-${process.pid}`;
const privateURLProofpack = createProofpack(fixture.appleDir, privateURLRunId);
assert.equal(privateURLProofpack.status, 0);
const privateURL = createObservation(privateURLProofpack.output.proofpackDir, [
  "--support-url",
  "https://localhost/support",
]);
assert.equal(privateURL.status, 1);
assert.match(privateURL.output.error, /supportURL/);

const unsafeURLRunId = `native-apple-app-review-observation-unsafe-url-${process.pid}`;
const unsafeURLProofpack = createProofpack(fixture.appleDir, unsafeURLRunId);
assert.equal(unsafeURLProofpack.status, 0);
const unsafeURL = createObservation(unsafeURLProofpack.output.proofpackDir, [
  "--privacy-policy-url",
  "https://reviewer@example.com@thebonfire.xyz/privacy",
]);
assert.equal(unsafeURL.status, 1);
assert.match(unsafeURL.output.error, /privacyPolicyURL/);

for (const [label, url] of [
  ["ipv6-loopback", "https://[::1]/support"],
  ["ipv6-private", "https://[fd00::1]/support"],
  ["ipv6-link-local", "https://[fe80::1]/support"],
]) {
  const ipv6RunId = `native-apple-app-review-observation-${label}-${process.pid}`;
  const ipv6Proofpack = createProofpack(fixture.appleDir, ipv6RunId);
  assert.equal(ipv6Proofpack.status, 0);
  const ipv6URL = createObservation(ipv6Proofpack.output.proofpackDir, ["--support-url", url]);
  assert.equal(ipv6URL.status, 1);
  assert.match(ipv6URL.output.error, /supportURL/);
}

const staleTemplateRunId = `native-apple-app-review-observation-stale-template-${process.pid}`;
const staleTemplateProofpack = createProofpack(fixture.appleDir, staleTemplateRunId);
assert.equal(staleTemplateProofpack.status, 0);
const staleManifest = JSON.parse(readFileSync(staleTemplateProofpack.output.proofpackPath, "utf8"));
const staleTemplatePath = resolve(rootDir, staleManifest.observationTemplates.appStoreReview);
const staleTemplate = JSON.parse(readFileSync(staleTemplatePath, "utf8"));
staleTemplate.app.build = "14";
writeFixtureFile(staleTemplatePath, `${JSON.stringify(staleTemplate, null, 2)}\n`);
const staleTemplateResult = createObservation(staleTemplateProofpack.output.proofpackDir);
assert.equal(staleTemplateResult.status, 1);
assert.match(staleTemplateResult.output.error, /template\.app\.build/);

const unexpectedTemplateRunId = `native-apple-app-review-observation-unexpected-template-${process.pid}`;
const unexpectedTemplateProofpack = createProofpack(fixture.appleDir, unexpectedTemplateRunId);
assert.equal(unexpectedTemplateProofpack.status, 0);
const unexpectedTemplateManifest = JSON.parse(readFileSync(unexpectedTemplateProofpack.output.proofpackPath, "utf8"));
const unexpectedTemplatePath = resolve(rootDir, unexpectedTemplateManifest.observationTemplates.appStoreReview);
const unexpectedTemplate = JSON.parse(readFileSync(unexpectedTemplatePath, "utf8"));
unexpectedTemplate.appleReviewStatus = "approved";
unexpectedTemplate.metadata.reviewerNotes = "do not ship";
writeFixtureFile(unexpectedTemplatePath, `${JSON.stringify(unexpectedTemplate, null, 2)}\n`);
const unexpectedTemplateResult = createObservation(unexpectedTemplateProofpack.output.proofpackDir);
assert.equal(unexpectedTemplateResult.status, 1);
assert.match(unexpectedTemplateResult.output.error, /template\.appleReviewStatus|template\.metadata\.reviewerNotes/);

const staleProjectFixture = makeAppleFixture();
const staleProjectRunId = `native-apple-app-review-observation-stale-project-${process.pid}`;
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

console.log("native-apple-create-app-review-observation: 12 checks passed");
