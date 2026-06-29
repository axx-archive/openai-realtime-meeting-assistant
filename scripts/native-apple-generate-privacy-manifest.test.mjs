#!/usr/bin/env node
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { existsSync, mkdtempSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const scriptsDir = dirname(fileURLToPath(import.meta.url));
const rootDir = resolve(scriptsDir, "..");
const scriptPath = resolve(scriptsDir, "native-apple-generate-privacy-manifest.mjs");

function runGenerator(args = []) {
  const result = spawnSync(process.execPath, [scriptPath, ...args], {
    cwd: rootDir,
    encoding: "utf8",
  });
  let output;
  try {
    output = JSON.parse(result.stdout || result.stderr);
  } catch (error) {
    throw new Error(`Could not parse generator output.\nstatus=${result.status}\nstdout=${result.stdout}\nstderr=${result.stderr}\n${error}`);
  }
  return { status: result.status, output };
}

function parsePlist(path) {
  const result = spawnSync("plutil", ["-convert", "json", "-o", "-", path], {
    cwd: rootDir,
    encoding: "utf8",
  });
  assert.equal(result.status, 0, result.stderr);
  return JSON.parse(result.stdout);
}

function writeFixtureFile(path, contents) {
  mkdirSync(dirname(path), { recursive: true });
  writeFileSync(path, contents);
}

function makeAppleFixture() {
  const dir = mkdtempSync(resolve(tmpdir(), "meetingassist-privacy-manifest-"));
  const appleDir = resolve(dir, "apple");
  writeFixtureFile(
    resolve(appleDir, "project.yml"),
    `targets:
  MeetingAssistAppleApp:
    sources:
      - path: Xcode/MeetingAssistAppleApp.swift
      - path: Xcode/Assets.xcassets
    dependencies: []
  MeetingAssistMacApp:
    sources:
      - path: Xcode/MeetingAssistMacApp.swift
      - path: Xcode/Assets.xcassets
    dependencies: []
schemes: {}
`
  );
  return { dir, appleDir };
}

function approvedDecisions(overrides = {}) {
  const base = {
    schemaVersion: 1,
    approval: {
      approved: true,
      approvedAt: "2026-06-29T21:00:00Z",
      approvedBy: "product-legal",
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
      {
        dataType: "NSPrivacyCollectedDataTypeOtherDiagnosticData",
        linked: false,
        tracking: false,
        purposes: ["NSPrivacyCollectedDataTypePurposeDiagnostics"],
      },
    ],
  };
  return {
    ...base,
    ...overrides,
    approval: { ...base.approval, ...(overrides.approval ?? {}) },
  };
}

function writeDecisions(dir, name, decisions) {
  const path = resolve(dir, name);
  writeFixtureFile(path, `${JSON.stringify(decisions, null, 2)}\n`);
  return path;
}

const fixture = makeAppleFixture();
const decisionsPath = writeDecisions(fixture.dir, "approved-decisions.json", approvedDecisions());
const generated = runGenerator([
  "--apple-dir",
  fixture.appleDir,
  "--decisions-file",
  decisionsPath,
  "--confirm-approved",
  "--wire-project",
]);
assert.equal(generated.status, 0);
assert.equal(generated.output.ok, true);
assert.equal(generated.output.collectedDataTypes, 2);
assert.equal(generated.output.accessedAPITypes, 0);
assert.equal(generated.output.tracking, false);
assert.equal(existsSync(generated.output.manifestPath), true);

const manifest = parsePlist(generated.output.manifestPath);
assert.equal(manifest.NSPrivacyTracking, false);
assert.deepEqual(manifest.NSPrivacyTrackingDomains, []);
assert.deepEqual(manifest.NSPrivacyAccessedAPITypes, []);
assert.equal(manifest.NSPrivacyCollectedDataTypes.length, 2);
assert.equal(manifest.NSPrivacyCollectedDataTypes[0].NSPrivacyCollectedDataType, "NSPrivacyCollectedDataTypeName");
assert.equal(manifest.NSPrivacyCollectedDataTypes[0].NSPrivacyCollectedDataTypeLinked, true);
assert.equal(manifest.NSPrivacyCollectedDataTypes[0].NSPrivacyCollectedDataTypeTracking, false);
assert.deepEqual(manifest.NSPrivacyCollectedDataTypes[0].NSPrivacyCollectedDataTypePurposes, [
  "NSPrivacyCollectedDataTypePurposeAppFunctionality",
]);

const projectText = readFileSync(resolve(fixture.appleDir, "project.yml"), "utf8");
assert.equal((projectText.match(/path: Xcode\/PrivacyInfo\.xcprivacy/g) ?? []).length, 2);

const noConfirm = runGenerator(["--apple-dir", fixture.appleDir, "--decisions-file", decisionsPath]);
assert.equal(noConfirm.status, 1);
assert.match(noConfirm.output.error, /confirmApproved/);

const unapproved = runGenerator([
  "--apple-dir",
  fixture.appleDir,
  "--decisions-file",
  writeDecisions(fixture.dir, "unapproved.json", approvedDecisions({ approval: { approved: false } })),
  "--confirm-approved",
]);
assert.equal(unapproved.status, 1);
assert.match(unapproved.output.error, /approval\.approved/);

const trackingWithoutDomain = runGenerator([
  "--apple-dir",
  fixture.appleDir,
  "--decisions-file",
  writeDecisions(fixture.dir, "tracking-without-domain.json", approvedDecisions({ tracking: true })),
  "--confirm-approved",
]);
assert.equal(trackingWithoutDomain.status, 1);
assert.match(trackingWithoutDomain.output.error, /trackingDomains:required_when_tracking/);

const placeholderPurpose = runGenerator([
  "--apple-dir",
  fixture.appleDir,
  "--decisions-file",
  writeDecisions(
    fixture.dir,
    "placeholder-purpose.json",
    approvedDecisions({
      collectedDataTypes: [
        {
          dataType: "NSPrivacyCollectedDataTypeName",
          linked: true,
          tracking: false,
          purposes: ["TODO"],
        },
      ],
    })
  ),
  "--confirm-approved",
]);
assert.equal(placeholderPurpose.status, 1);
assert.match(placeholderPurpose.output.error, /collectedDataTypes\[0\]\.purposes/);

const unsafeDecision = runGenerator([
  "--apple-dir",
  fixture.appleDir,
  "--decisions-file",
  writeDecisions(
    fixture.dir,
    "unsafe.json",
    approvedDecisions({
      uploadApiKey: "sk-thisShouldNotAppearInAPrivacyDecision000000",
    })
  ),
  "--confirm-approved",
]);
assert.equal(unsafeDecision.status, 1);
assert.match(unsafeDecision.output.error, /unsafeContent/);

rmSync(fixture.dir, { recursive: true, force: true });

console.log("native-apple-generate-privacy-manifest: 6 checks passed");
