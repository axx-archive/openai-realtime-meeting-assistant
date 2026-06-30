#!/usr/bin/env node
import assert from "node:assert/strict";
import { cpSync, mkdirSync, mkdtempSync, readFileSync, writeFileSync } from "node:fs";
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
    "--require-proofpack",
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
    "--require-proofpack",
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
    "--require-proofpack",
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
  "--require-proofpack",
  "--require-notary-profile",
]);
assert.equal(missingSigning.status, 1);
assert.equal(missingSigning.output.readyForOperator, false);
assert.ok(missingSigning.output.blockers.some((blocker) => blocker.id === "signing_configuration"));
assert.ok(missingSigning.output.blockers.some((blocker) => blocker.id === "notarytool_keychain_profile"));

const missingProofpack = runNode(
  preflightScriptPath,
  ["--apple-dir", appleDir, "--require-proofpack"],
  { APPLE_DEVELOPMENT_TEAM: "A1B2C3D4E5" }
);
assert.equal(missingProofpack.status, 1);
assert.equal(missingProofpack.output.readyForOperator, false);
assert.ok(missingProofpack.output.checks.some((check) => check.id === "proofpack_required" && !check.ok));
assert.ok(missingProofpack.output.blockers.some((blocker) => blocker.id === "proofpack_dir"));

const staleRunId = `native-apple-operator-preflight-stale-test-${process.pid}`;
const staleProofpackDir = createProofpack(appleDir, staleRunId);
createPackagePlan(appleDir, staleProofpackDir);
const stalePlanPath = resolve(rootDir, staleProofpackDir, "operator", "release-command-plan.json");
const stalePlan = JSON.parse(readFileSync(stalePlanPath, "utf8"));
stalePlan.commands.promoteIPhoneMediaEvidence.shell = "echo stale-promoter";
writeFileSync(stalePlanPath, `${JSON.stringify(stalePlan, null, 2)}\n`);
const stalePlanPreflight = runNode(
  preflightScriptPath,
  [
    "--apple-dir",
    appleDir,
    "--proofpack-dir",
    staleProofpackDir,
    "--require-proofpack",
    "--require-notary-profile",
  ],
  {
    APPLE_DEVELOPMENT_TEAM: "A1B2C3D4E5",
    NOTARYTOOL_KEYCHAIN_PROFILE: "meetingassist-notary-profile",
  }
);
assert.equal(stalePlanPreflight.status, 1);
assert.equal(stalePlanPreflight.output.readyForOperator, false);
assert.ok(stalePlanPreflight.output.checks.some((check) => check.id === "operator_commands" && !check.ok && /promoteIPhoneMediaEvidence/.test(check.detail)));
assert.ok(stalePlanPreflight.output.blockers.some((blocker) => blocker.id === "operator_commands"));

const staleAppReviewRunId = `native-apple-operator-preflight-stale-app-review-test-${process.pid}`;
const staleAppReviewProofpackDir = createProofpack(appleDir, staleAppReviewRunId);
createPackagePlan(appleDir, staleAppReviewProofpackDir);
const staleAppReviewPlanPath = resolve(rootDir, staleAppReviewProofpackDir, "operator", "release-command-plan.json");
const staleAppReviewPlan = JSON.parse(readFileSync(staleAppReviewPlanPath, "utf8"));
const staleAppReviewProofpackRef = relative(rootDir, staleAppReviewProofpackDir).split(/[/\\]/).join("/");
staleAppReviewPlan.commands.createAppStoreReviewObservation.shell = staleAppReviewPlan.commands.createAppStoreReviewObservation.shell
  .replace(staleAppReviewProofpackRef, "artifacts/native-apple/wrong-proofpack")
  .replace("--support-url ", "");
writeFileSync(staleAppReviewPlanPath, `${JSON.stringify(staleAppReviewPlan, null, 2)}\n`);
const staleAppReviewPreflight = runNode(
  preflightScriptPath,
  [
    "--apple-dir",
    appleDir,
    "--proofpack-dir",
    staleAppReviewProofpackDir,
    "--require-proofpack",
    "--require-notary-profile",
  ],
  {
    APPLE_DEVELOPMENT_TEAM: "A1B2C3D4E5",
    NOTARYTOOL_KEYCHAIN_PROFILE: "meetingassist-notary-profile",
  }
);
assert.equal(staleAppReviewPreflight.status, 1);
assert.equal(staleAppReviewPreflight.output.readyForOperator, false);
assert.ok(
  staleAppReviewPreflight.output.checks.some(
    (check) =>
      check.id === "operator_commands" &&
      !check.ok &&
      /createAppStoreReviewObservation/.test(check.detail) &&
      /--support-url|native-apple-operator-preflight-stale-app-review-test/.test(check.detail)
  )
);
assert.ok(staleAppReviewPreflight.output.blockers.some((blocker) => blocker.id === "operator_commands"));

const staleCurrentProjectFixture = makeAppleFixture({ build: "15" });
const staleCurrentProjectRunId = `native-apple-operator-preflight-current-project-test-${process.pid}`;
const staleCurrentProjectProofpackDir = createProofpack(staleCurrentProjectFixture.appleDir, staleCurrentProjectRunId);
createPackagePlan(staleCurrentProjectFixture.appleDir, staleCurrentProjectProofpackDir);
writeFixtureFile(
  resolve(staleCurrentProjectFixture.appleDir, "project.yml"),
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
const staleCurrentProjectPreflight = runNode(
  preflightScriptPath,
  [
    "--apple-dir",
    staleCurrentProjectFixture.appleDir,
    "--proofpack-dir",
    staleCurrentProjectProofpackDir,
    "--require-proofpack",
    "--require-notary-profile",
  ],
  {
    APPLE_DEVELOPMENT_TEAM: "A1B2C3D4E5",
    NOTARYTOOL_KEYCHAIN_PROFILE: "meetingassist-notary-profile",
  }
);
assert.equal(staleCurrentProjectPreflight.status, 1);
assert.equal(staleCurrentProjectPreflight.output.readyForOperator, false);
assert.ok(
  staleCurrentProjectPreflight.output.checks.some(
    (check) => check.id === "proofpack_identity" && !check.ok && /currentProject\.build/.test(check.detail)
  )
);
assert.ok(staleCurrentProjectPreflight.output.blockers.some((blocker) => blocker.id === "proofpack_identity"));

const noProjectFixture = makeAppleFixture({ project: false });
const noProject = runNode(
  preflightScriptPath,
  ["--apple-dir", noProjectFixture.appleDir],
  { APPLE_DEVELOPMENT_TEAM: "A1B2C3D4E5" }
);
assert.equal(noProject.status, 1);
assert.ok(noProject.output.blockers.some((blocker) => blocker.id === "xcode_project"));

console.log("native-apple-release-operator-preflight: 9 checks passed");
