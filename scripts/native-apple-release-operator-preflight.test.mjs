#!/usr/bin/env node
import assert from "node:assert/strict";
import { cpSync, mkdirSync, mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { spawnSync } from "node:child_process";

const scriptsDir = dirname(fileURLToPath(import.meta.url));
const rootDir = resolve(scriptsDir, "..");
const proofpackScriptPath = resolve(scriptsDir, "native-apple-release-proofpack.mjs");
const packagePlanScriptPath = resolve(scriptsDir, "native-apple-release-package-plan.mjs");
const preflightScriptPath = resolve(scriptsDir, "native-apple-release-operator-preflight.mjs");
const privacyManifestScriptPath = resolve(scriptsDir, "native-apple-generate-privacy-manifest.mjs");

function runNode(scriptPath, args = [], env = {}) {
  const result = spawnSync(process.execPath, [scriptPath, ...args], {
    cwd: rootDir,
    encoding: "utf8",
    env: {
      ...process.env,
      DEVELOPMENT_TEAM: "",
      APPLE_DEVELOPMENT_TEAM: "",
      NOTARYTOOL_KEYCHAIN_PROFILE: "",
      ...env,
    },
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
  const dir = mkdtempSync(resolve(tmpdir(), "meetingassist-operator-preflight-"));
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
    writeFixtureFile(resolve(appleDir, "MeetingAssist.xcodeproj", "project.pbxproj"), "// generated fixture\n");
  }
  return { dir, appleDir };
}

function createProofpack(appleDir, runId) {
  const result = runNode(proofpackScriptPath, [
    "--apple-dir",
    appleDir,
    "--artifacts-dir",
    resolve(rootDir, "artifacts", "native-apple"),
    "--run-id",
    runId,
    "--room-id",
    "operator-preflight-room-test",
    "--created-at",
    "2026-06-29T22:55:00Z",
    "--skip-gates",
  ]);
  assert.equal(result.status, 0);
  return result.output.proofpackDir;
}

function createPackagePlan(appleDir, proofpackDir) {
  const result = runNode(packagePlanScriptPath, [
    "--apple-dir",
    appleDir,
    "--proofpack-dir",
    proofpackDir,
    "--created-at",
    "2026-06-29T23:00:00Z",
    "--write",
  ]);
  assert.equal(result.status, 0);
}

function writeApprovedPrivacyDecisions(path) {
  writeFixtureFile(
    path,
    `${JSON.stringify(
      {
        schemaVersion: 1,
        approval: {
          approved: true,
          approvedAt: "2026-06-29T23:10:00Z",
          approvedBy: "product-legal-reviewer",
        },
        tracking: false,
        trackingDomains: [],
        accessedAPITypes: [],
        collectedDataTypes: [
          {
            dataType: "NSPrivacyCollectedDataTypeName",
            linked: true,
            tracking: false,
            purposes: ["NSPrivacyCollectedDataTypePurposeAppFunctionality"],
          },
        ],
      },
      null,
      2
    )}\n`
  );
}

const appleDir = resolve(rootDir, "apple");
const runId = `native-apple-operator-preflight-test-${process.pid}`;
const proofpackDir = createProofpack(appleDir, runId);
createPackagePlan(appleDir, proofpackDir);

const passing = runNode(
  preflightScriptPath,
  [
    "--apple-dir",
    appleDir,
    "--proofpack-dir",
    proofpackDir,
    "--require-notary-profile",
  ],
  {
    APPLE_DEVELOPMENT_TEAM: "A1B2C3D4E5",
    NOTARYTOOL_KEYCHAIN_PROFILE: "meetingassist-notary-profile",
  }
);
assert.equal(passing.status, 0);
assert.equal(passing.output.ok, true);
assert.equal(passing.output.readyForOperator, true);
assert.ok(passing.output.checks.some((check) => check.id === "signing_configuration" && check.ok));
assert.ok(passing.output.checks.some((check) => check.id === "notarytool_keychain_profile" && check.ok));
assert.ok(passing.output.checks.some((check) => check.id === "proofpack_identity" && check.ok));
assert.ok(passing.output.checks.some((check) => check.id === "operator_commands" && check.ok));
assert.ok(passing.output.checks.some((check) => check.id === "export_options" && check.ok));
assert.doesNotMatch(JSON.stringify(passing.output), /A1B2C3D4E5|meetingassist-notary-profile|\.p8|\.p12|mobileprovision|provisionprofile/);

const missingPrivacyManifest = runNode(
  preflightScriptPath,
  [
    "--apple-dir",
    appleDir,
    "--proofpack-dir",
    proofpackDir,
    "--require-privacy-manifest",
    "--require-notary-profile",
  ],
  {
    APPLE_DEVELOPMENT_TEAM: "A1B2C3D4E5",
    NOTARYTOOL_KEYCHAIN_PROFILE: "meetingassist-notary-profile",
  }
);
assert.equal(missingPrivacyManifest.status, 1);
assert.equal(missingPrivacyManifest.output.readyForOperator, false);
assert.ok(missingPrivacyManifest.output.checks.some((check) => check.id === "privacy_manifest_required" && !check.ok));
assert.ok(missingPrivacyManifest.output.blockers.some((blocker) => blocker.id === "privacy_manifest"));

const privacyFixtureDir = mkdtempSync(resolve(tmpdir(), "meetingassist-operator-preflight-privacy-"));
const privacyAppleDir = resolve(privacyFixtureDir, "apple");
cpSync(appleDir, privacyAppleDir, {
  recursive: true,
  filter: (source) => !relative(appleDir, source).split(/[/\\]/).includes(".build"),
});
const decisionsPath = resolve(privacyFixtureDir, "PrivacyManifest.decisions.local.json");
writeApprovedPrivacyDecisions(decisionsPath);
const generatedPrivacy = runNode(privacyManifestScriptPath, [
  "--apple-dir",
  privacyAppleDir,
  "--decisions-file",
  decisionsPath,
  "--confirm-approved",
  "--wire-project",
  "--generate-xcode-project",
]);
assert.equal(generatedPrivacy.status, 0);
const privacyRunId = `native-apple-operator-preflight-privacy-test-${process.pid}`;
const privacyProofpackDir = createProofpack(privacyAppleDir, privacyRunId);
createPackagePlan(privacyAppleDir, privacyProofpackDir);
const privacyPassing = runNode(
  preflightScriptPath,
  [
    "--apple-dir",
    privacyAppleDir,
    "--proofpack-dir",
    privacyProofpackDir,
    "--require-privacy-manifest",
    "--require-notary-profile",
  ],
  {
    APPLE_DEVELOPMENT_TEAM: "A1B2C3D4E5",
    NOTARYTOOL_KEYCHAIN_PROFILE: "meetingassist-notary-profile",
  }
);
assert.equal(privacyPassing.status, 0);
assert.equal(privacyPassing.output.readyForOperator, true);
assert.ok(privacyPassing.output.checks.some((check) => check.id === "privacy_manifest_required" && check.ok));

const missingSigning = runNode(preflightScriptPath, [
  "--apple-dir",
  appleDir,
  "--proofpack-dir",
  proofpackDir,
  "--require-notary-profile",
]);
assert.equal(missingSigning.status, 1);
assert.equal(missingSigning.output.readyForOperator, false);
assert.ok(missingSigning.output.blockers.some((blocker) => blocker.id === "signing_configuration"));
assert.ok(missingSigning.output.blockers.some((blocker) => blocker.id === "notarytool_keychain_profile"));

const noProjectFixture = makeAppleFixture({ project: false });
const noProject = runNode(
  preflightScriptPath,
  ["--apple-dir", noProjectFixture.appleDir],
  { APPLE_DEVELOPMENT_TEAM: "A1B2C3D4E5" }
);
assert.equal(noProject.status, 1);
assert.ok(noProject.output.blockers.some((blocker) => blocker.id === "xcode_project"));

console.log("native-apple-release-operator-preflight: 5 checks passed");
