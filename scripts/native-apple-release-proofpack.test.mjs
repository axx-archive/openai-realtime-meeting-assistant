#!/usr/bin/env node
import assert from "node:assert/strict";
import { existsSync, mkdtempSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { spawnSync } from "node:child_process";

const scriptsDir = dirname(fileURLToPath(import.meta.url));
const rootDir = resolve(scriptsDir, "..");
const scriptPath = resolve(scriptsDir, "native-apple-release-proofpack.mjs");

function runProofpack(args = []) {
  const result = spawnSync(process.execPath, [scriptPath, ...args], {
    cwd: rootDir,
    encoding: "utf8",
  });

  let output;
  try {
    output = JSON.parse(result.stdout || result.stderr);
  } catch (error) {
    throw new Error(
      `Could not parse proofpack output.\nstatus=${result.status}\nstdout=${result.stdout}\nstderr=${result.stderr}\n${error}`
    );
  }
  return { status: result.status, output };
}

function writeFixtureFile(path, contents) {
  mkdirSync(dirname(path), { recursive: true });
  writeFileSync(path, contents);
}

function makeAppleFixture() {
  const dir = mkdtempSync(resolve(tmpdir(), "meetingassist-proofpack-"));
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

const fixture = makeAppleFixture();
const runId = `native-apple-proofpack-test-${process.pid}`;
const roomId = "proofpack-room-test";
const createdAt = "2026-06-29T16:00:00Z";
const artifactsDir = resolve(rootDir, "artifacts", "native-apple");

const created = runProofpack([
  "--apple-dir",
  fixture.appleDir,
  "--artifacts-dir",
  artifactsDir,
  "--run-id",
  runId,
  "--room-id",
  roomId,
  "--created-at",
  createdAt,
  "--skip-gates",
]);
assert.equal(created.status, 0);
assert.equal(created.output.ok, true);
assert.equal(created.output.version, "1.0");
assert.equal(created.output.build, "15");
assert.equal(created.output.runId, runId);
assert.equal(created.output.roomId, roomId);
assert.ok(existsSync(created.output.proofpackPath));
assert.ok(existsSync(created.output.evidenceDraft));

const proofpack = JSON.parse(readFileSync(created.output.proofpackPath, "utf8"));
assert.equal(proofpack.schemaVersion, 1);
assert.equal(proofpack.runId, runId);
assert.equal(proofpack.roomId, roomId);
for (const ref of Object.values(proofpack.evidenceArtifacts)) {
  assert.match(ref, new RegExp(`^artifacts/native-apple/${runId}/evidence/`));
  assert.ok(existsSync(resolve(rootDir, ref)));
}

const draft = JSON.parse(readFileSync(created.output.evidenceDraft, "utf8"));
assert.equal(draft.version, "1.0");
assert.equal(draft.build, "15");
assert.equal(draft.physicalDeviceMedia.iphone.status, "pending");
assert.equal(draft.physicalDeviceMedia.iphone.mediaAssertions.cameraPublished, false);
assert.equal(draft.restrictiveNetworkTurn.status, "pending");
assert.equal(draft.testFlight.status, "pending");
assert.equal(draft.macNotarization.status, "pending");

const wroteEvidence = runProofpack([
  "--apple-dir",
  fixture.appleDir,
  "--proofpack-dir",
  created.output.proofpackDir,
  "--write-evidence",
  "--skip-gates",
]);
assert.equal(wroteEvidence.status, 0);
assert.ok(wroteEvidence.output.localEvidenceWritten.endsWith("apple/ReleaseEvidence.local.json"));
assert.deepEqual(JSON.parse(readFileSync(wroteEvidence.output.localEvidenceWritten, "utf8")), draft);

const duplicate = runProofpack([
  "--apple-dir",
  fixture.appleDir,
  "--artifacts-dir",
  artifactsDir,
  "--run-id",
  runId,
  "--room-id",
  roomId,
  "--created-at",
  createdAt,
  "--skip-gates",
]);
assert.equal(duplicate.status, 1);
assert.match(duplicate.output.error, /already exists/);

const secretRunId = runProofpack([
  "--apple-dir",
  fixture.appleDir,
  "--artifacts-dir",
  resolve(rootDir, "artifacts", "native-apple"),
  "--run-id",
  "sk-thisShouldNotAppearInAProofPack000000",
  "--skip-gates",
]);
assert.equal(secretRunId.status, 1);
assert.match(secretRunId.output.error, /secret/);

const writeWithoutProofpackDir = runProofpack([
  "--apple-dir",
  fixture.appleDir,
  "--artifacts-dir",
  resolve(rootDir, "artifacts", "native-apple"),
  "--run-id",
  `native-apple-proofpack-write-${process.pid}`,
  "--write-evidence",
  "--skip-gates",
]);
assert.equal(writeWithoutProofpackDir.status, 1);
assert.match(writeWithoutProofpackDir.output.error, /requires --proofpack-dir/);

const localEvidencePath = resolve(fixture.appleDir, "ReleaseEvidence.local.json");
rmSync(localEvidencePath, { force: true });
const missingProofpackWrite = runProofpack([
  "--apple-dir",
  fixture.appleDir,
  "--proofpack-dir",
  resolve(rootDir, "artifacts", "native-apple", `missing-proofpack-${process.pid}`),
  "--write-evidence",
  "--skip-gates",
]);
assert.equal(missingProofpackWrite.status, 1);
assert.match(missingProofpackWrite.output.error, /does not exist/);
assert.equal(existsSync(localEvidencePath), false);

console.log("native-apple-release-proofpack: 6 checks passed");
