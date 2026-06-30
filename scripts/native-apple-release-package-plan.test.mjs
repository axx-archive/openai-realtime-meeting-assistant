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
const packagePlanScriptPath = resolve(scriptsDir, "native-apple-release-package-plan.mjs");

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

function makeAppleFixture({ build = "15", project = true } = {}) {
  const dir = mkdtempSync(resolve(tmpdir(), "meetingassist-package-plan-"));
  const appleDir = resolve(dir, "apple");
  writeFixtureFile(
    resolve(appleDir, "project.yml"),
    `targets:
  MeetingAssistAppleApp:
    settings:
      base:
        CURRENT_PROJECT_VERSION: ${build}
        MARKETING_VERSION: 1.0
        PRODUCT_BUNDLE_IDENTIFIER: co.thebonfire.meetingassist.ios
  MeetingAssistMacApp:
    settings:
      base:
        CURRENT_PROJECT_VERSION: ${build}
        MARKETING_VERSION: 1.0
        PRODUCT_BUNDLE_IDENTIFIER: co.thebonfire.meetingassist.mac
`
  );
  writeFixtureFile(resolve(appleDir, "Config", "Signing.xcconfig"), "DEVELOPMENT_TEAM = $(APPLE_DEVELOPMENT_TEAM)\n");
  if (project) {
    mkdirSync(resolve(appleDir, "MeetingAssist.xcodeproj"), { recursive: true });
  }
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
    "package-plan-room-test",
    "--created-at",
    "2026-06-29T22:30:00Z",
    "--skip-gates",
  ]);
}

function readPlistJSON(path) {
  const result = spawnSync("plutil", ["-convert", "json", "-o", "-", path], {
    cwd: rootDir,
    encoding: "utf8",
  });
  if (result.status !== 0) {
    throw new Error(`plutil failed for ${path}\nstdout=${result.stdout}\nstderr=${result.stderr}`);
  }
  return JSON.parse(result.stdout);
}

const fixture = makeAppleFixture();
const runId = `native-apple-package-plan-test-${process.pid}`;
const proofpack = createProofpack(fixture.appleDir, runId);
assert.equal(proofpack.status, 0);

const created = runNode(packagePlanScriptPath, [
  "--apple-dir",
  fixture.appleDir,
  "--proofpack-dir",
  proofpack.output.proofpackDir,
  "--created-at",
  "2026-06-29T22:45:00Z",
  "--write",
]);
assert.equal(created.status, 0);
assert.equal(created.output.ok, true);
assert.equal(created.output.status, "ready_for_operator");
assert.equal(created.output.runId, runId);
assert.ok(created.output.warnings.some((warning) => warning.id === "privacy_manifest"));
assert.ok(created.output.planPath.endsWith("/operator/release-command-plan.json"));

const plan = JSON.parse(readFileSync(resolve(rootDir, created.output.planPath), "utf8"));
assert.equal(plan.artifactType, "native_apple_release_package_plan");
assert.equal(plan.version, "1.0");
assert.equal(plan.build, "15");
assert.equal(plan.proofpack.proofpackDir, `artifacts/native-apple/${runId}`);
assert.equal(plan.outputDir, `artifacts/native-apple/${runId}/operator`);
assert.equal(plan.bundleIdentifiers.ios, "co.thebonfire.meetingassist.ios");
assert.equal(plan.bundleIdentifiers.macos, "co.thebonfire.meetingassist.mac");
assert.equal(plan.observationInputs.iphoneMediaTemplate, `artifacts/native-apple/${runId}/inbox/iphone-qa_snapshot.template.json`);
assert.equal(plan.observationInputs.iphoneMediaInput, `artifacts/native-apple/${runId}/inbox/iphone-qa_snapshot.json`);
assert.equal(plan.observationInputs.ipadMediaTemplate, `artifacts/native-apple/${runId}/inbox/ipad-qa_snapshot.template.json`);
assert.equal(plan.observationInputs.ipadMediaInput, `artifacts/native-apple/${runId}/inbox/ipad-qa_snapshot.json`);
assert.equal(plan.observationInputs.macMediaTemplate, `artifacts/native-apple/${runId}/inbox/mac-qa_snapshot.template.json`);
assert.equal(plan.observationInputs.macMediaInput, `artifacts/native-apple/${runId}/inbox/mac-qa_snapshot.json`);
assert.equal(plan.observationInputs.turnRelayTemplate, `artifacts/native-apple/${runId}/inbox/turn-relay-observation.template.json`);
assert.equal(plan.observationInputs.turnRelayInput, `artifacts/native-apple/${runId}/inbox/turn-relay-observation.json`);
assert.equal(plan.observationInputs.roomInteropTemplate, `artifacts/native-apple/${runId}/inbox/room-interop-observation.template.json`);
assert.equal(plan.observationInputs.roomInteropInput, `artifacts/native-apple/${runId}/inbox/room-interop-observation.json`);
assert.equal(plan.observationInputs.appStoreReviewTemplate, `artifacts/native-apple/${runId}/inbox/app-store-review-observation.template.json`);
assert.equal(plan.observationInputs.appStoreReviewInput, `artifacts/native-apple/${runId}/inbox/app-store-review-observation.json`);
assert.equal(plan.observationInputs.testFlightTemplate, `artifacts/native-apple/${runId}/inbox/testflight-observation.template.json`);
assert.equal(plan.observationInputs.testFlightInput, `artifacts/native-apple/${runId}/inbox/testflight-observation.json`);
assert.equal(plan.observationInputs.notarizationTemplate, `artifacts/native-apple/${runId}/inbox/notarization-observation.template.json`);
assert.equal(plan.observationInputs.notarizationInput, `artifacts/native-apple/${runId}/inbox/notarization-observation.json`);
assert.match(plan.commands.operatorPreflight.shell, /native-apple-release-operator-preflight\.mjs/);
assert.match(plan.commands.operatorPreflight.shell, /--run-build-rehearsal/);
assert.match(plan.commands.operatorPreflight.shell, /--require-proofpack/);
assert.match(plan.commands.operatorPreflight.shell, /--require-privacy-manifest/);
assert.match(plan.commands.operatorPreflight.shell, /--require-notary-profile/);
assert.match(plan.nextSteps[0], /operatorPreflight/);
assert.match(plan.commands.iosArchive.shell, /generic\/platform=iOS/);
assert.match(plan.commands.testflightUpload.shell, /ExportOptions\.testflight\.plist/);
assert.match(plan.commands.macArchive.shell, /generic\/platform=macOS/);
assert.match(plan.commands.macZipForNotary.shell, /MeetingAssistMacApp\.zip/);
assert.match(plan.commands.macSubmitNotary.shell, /"\$NOTARYTOOL_KEYCHAIN_PROFILE"/);
assert.match(plan.commands.promoteIPhoneMediaEvidence.shell, /iphone-qa_snapshot\.json/);
assert.match(plan.commands.promoteIPadMediaEvidence.shell, /ipad-qa_snapshot\.json/);
assert.match(plan.commands.promoteMacMediaEvidence.shell, /mac-qa_snapshot\.json/);
assert.match(plan.commands.promoteTurnRelayObservation.shell, /turn-relay-observation\.json/);
assert.match(plan.commands.promoteTurnRelayObservation.shell, /"\$NATIVE_APPLE_RESTRICTIVE_NETWORK"/);
assert.match(plan.commands.promoteRoomInteropObservation.shell, /room-interop-observation\.json/);
assert.match(plan.commands.promoteRoomInteropObservation.shell, /--confirm-browser-native-mixed-room/);
assert.match(plan.commands.promoteRoomInteropObservation.shell, /--confirm-recording-off/);
assert.match(plan.commands.createAppStoreReviewObservation.shell, /native-apple-create-app-review-observation\.mjs/);
assert.match(plan.commands.createAppStoreReviewObservation.shell, /"\$NATIVE_APPLE_SUPPORT_URL"/);
assert.match(plan.commands.createAppStoreReviewObservation.shell, /"\$NATIVE_APPLE_PRIVACY_POLICY_URL"/);
assert.match(plan.commands.createAppStoreReviewObservation.shell, /--confirm-screenshots-ready/);
assert.match(plan.commands.createAppStoreReviewObservation.shell, /--confirm-external-testing-group-ready/);
assert.match(plan.commands.promoteAppStoreReviewObservation.shell, /app-store-review-observation\.json/);
assert.match(plan.commands.promoteAppStoreReviewObservation.shell, /--kind app-review/);
assert.match(plan.commands.promoteAppStoreReviewObservation.shell, /--confirm-review-metadata-complete/);
assert.match(plan.commands.promoteAppStoreReviewObservation.shell, /--confirm-app-privacy-complete/);
assert.match(plan.commands.promoteAppStoreReviewObservation.shell, /--confirm-external-testing-ready/);
assert.match(plan.commands.createTestFlightObservation.shell, /native-apple-create-testflight-observation\.mjs/);
assert.match(plan.commands.createTestFlightObservation.shell, /"\$NATIVE_APPLE_APP_STORE_CONNECT_BUILD_ID"/);
assert.match(plan.commands.createTestFlightObservation.shell, /"\$NATIVE_APPLE_TESTFLIGHT_PROCESSING_STATUS"/);
assert.match(plan.commands.createTestFlightObservation.shell, /--confirm-app-store-connect-upload/);
assert.match(plan.commands.createTestFlightObservation.shell, /--confirm-current-build/);
assert.match(plan.commands.promoteTestFlightObservation.shell, /testflight-observation\.json/);
assert.match(plan.commands.createMacNotarizationObservation.shell, /native-apple-create-notarization-observation\.mjs/);
assert.match(plan.commands.createMacNotarizationObservation.shell, /"\$NATIVE_APPLE_MAC_DISTRIBUTION_KIND"/);
assert.match(plan.commands.createMacNotarizationObservation.shell, /"\$NATIVE_APPLE_MAC_DISTRIBUTION_FILENAME"/);
assert.match(plan.commands.createMacNotarizationObservation.shell, /"\$NATIVE_APPLE_MAC_DISTRIBUTION_SHA256"/);
assert.match(plan.commands.createMacNotarizationObservation.shell, /"\$NATIVE_APPLE_NOTARY_REQUEST_ID"/);
assert.match(plan.commands.createMacNotarizationObservation.shell, /"\$NATIVE_APPLE_GATEKEEPER_SOURCE"/);
assert.match(plan.commands.createMacNotarizationObservation.shell, /--confirm-gatekeeper-accepted/);
assert.match(plan.commands.createMacNotarizationObservation.shell, /--confirm-no-secrets/);
assert.match(plan.commands.promoteMacNotarizationObservation.shell, /notarization-observation\.json/);
assert.match(plan.commands.promoteMacNotarizationObservation.shell, /--confirm-no-secrets/);
assert.match(plan.commands.writeLocalReleaseEvidence.shell, /--write-evidence/);
assert.match(plan.commands.strictReleaseReadiness.shell, /--strict/);
assert.match(plan.commands.strictReleaseReadiness.shell, /ReleaseEvidence\.draft\.json/);
assert.doesNotMatch(JSON.stringify(plan), /-----BEGIN|\.p8|\.p12|mobileprovision|provisionprofile|sk-[A-Za-z0-9_-]{20,}/);

const testflightOptions = readPlistJSON(resolve(rootDir, plan.exportOptions.testflight));
assert.equal(testflightOptions.method, "app-store-connect");
assert.equal(testflightOptions.destination, "upload");
assert.equal(testflightOptions.testFlightInternalTestingOnly, false);
assert.equal(testflightOptions.manageAppVersionAndBuildNumber, false);
assert.equal(Object.hasOwn(testflightOptions, "teamID"), false);
assert.equal(Object.hasOwn(testflightOptions, "provisioningProfiles"), false);

const developerIdOptions = readPlistJSON(resolve(rootDir, plan.exportOptions.developerId));
assert.equal(developerIdOptions.method, "developer-id");
assert.equal(developerIdOptions.destination, "export");
assert.equal(developerIdOptions.signingStyle, "automatic");

const readme = readFileSync(resolve(rootDir, plan.outputDir, "release-commands.md"), "utf8");
assert.match(readme, /non-secret command plan/);
assert.match(readme, /promoteIPhoneMediaEvidence/);
assert.match(readme, /promoteTurnRelayObservation/);
assert.match(readme, /promoteRoomInteropObservation/);
assert.match(readme, /createAppStoreReviewObservation/);
assert.match(readme, /promoteAppStoreReviewObservation/);
assert.match(readme, /createTestFlightObservation/);
assert.match(readme, /createMacNotarizationObservation/);
assert.match(readme, /notarytool/);
assert.match(readme, /strict readiness/);

const duplicate = runNode(packagePlanScriptPath, [
  "--apple-dir",
  fixture.appleDir,
  "--proofpack-dir",
  proofpack.output.proofpackDir,
  "--created-at",
  "2026-06-29T22:45:00Z",
  "--write",
]);
assert.equal(duplicate.status, 1);
assert.match(duplicate.output.error, /Refusing to overwrite/);

const forced = runNode(packagePlanScriptPath, [
  "--apple-dir",
  fixture.appleDir,
  "--proofpack-dir",
  proofpack.output.proofpackDir,
  "--created-at",
  "2026-06-29T22:45:00Z",
  "--write",
  "--force",
]);
assert.equal(forced.status, 0);

const noProjectFixture = makeAppleFixture({ project: false });
const blocked = runNode(packagePlanScriptPath, [
  "--apple-dir",
  noProjectFixture.appleDir,
  "--run-id",
  `native-apple-package-plan-blocked-${process.pid}`,
  "--created-at",
  "2026-06-29T22:50:00Z",
]);
assert.equal(blocked.status, 1);
assert.ok(blocked.output.blockers.some((blocker) => blocker.id === "xcode_project"));
assert.ok(blocked.output.blockers.some((blocker) => blocker.id === "proofpack"));

const noProofpack = runNode(packagePlanScriptPath, [
  "--apple-dir",
  fixture.appleDir,
  "--run-id",
  `native-apple-package-plan-no-proofpack-${process.pid}`,
  "--created-at",
  "2026-06-29T22:52:00Z",
]);
assert.equal(noProofpack.status, 1);
assert.equal(noProofpack.output.ok, false);
assert.equal(noProofpack.output.status, "blocked");
assert.ok(noProofpack.output.blockers.some((blocker) => blocker.id === "proofpack"));

const noProofpackWrite = runNode(packagePlanScriptPath, [
  "--apple-dir",
  fixture.appleDir,
  "--run-id",
  `native-apple-package-plan-no-proofpack-write-${process.pid}`,
  "--created-at",
  "2026-06-29T22:53:00Z",
  "--write",
]);
assert.equal(noProofpackWrite.status, 1);
assert.match(noProofpackWrite.output.error, /Refusing to write a blocked release package plan/);

const secretRunId = runNode(packagePlanScriptPath, [
  "--apple-dir",
  fixture.appleDir,
  "--run-id",
  "sk-thisShouldNotAppearInAPackagePlan000000",
]);
assert.equal(secretRunId.status, 1);
assert.match(secretRunId.output.error, /secret/);

const teamIdRunId = runNode(packagePlanScriptPath, [
  "--apple-dir",
  fixture.appleDir,
  "--run-id",
  "A1B2C3D4E5",
]);
assert.equal(teamIdRunId.status, 1);
assert.match(teamIdRunId.output.error, /secret/);

const mismatchFixture = makeAppleFixture({ build: "16" });
const mismatch = runNode(packagePlanScriptPath, [
  "--apple-dir",
  mismatchFixture.appleDir,
  "--proofpack-dir",
  proofpack.output.proofpackDir,
]);
assert.equal(mismatch.status, 1);
assert.match(mismatch.output.error, /does not match/);

console.log("native-apple-release-package-plan: 11 checks passed");
