#!/usr/bin/env node
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const rootDir = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const scriptPath = resolve(rootDir, "scripts/native-apple-local-gates.mjs");

function run(args) {
  return spawnSync(process.execPath, [scriptPath, ...args], {
    cwd: rootDir,
    encoding: "utf8",
    maxBuffer: 1024 * 1024 * 5,
  });
}

function readJSON(result) {
  assert.equal(result.stderr, "", "expected no stderr");
  return JSON.parse(result.stdout);
}

{
  const result = run(["--dry-run"]);
  assert.equal(result.status, 0);
  const summary = readJSON(result);
  assert.equal(summary.ok, true);
  assert.equal(summary.complete, false);
  assert.equal(summary.liveMediaSmoke.status, "skipped");
  assert.match(summary.liveMediaSmoke.blocker, /--live-url/);
  assert.deepEqual(
    summary.results.map(item => item.name),
    ["mediaFixVerification", "voiceFocusBenchmark", "goTests", "swiftPackageTests", "releaseReadiness"]
  );
  assert.equal(summary.results.every(item => item.status === "planned"), true);
}

{
  const result = run(["--dry-run", "--require-live-media-smoke"]);
  assert.equal(result.status, 1);
  const summary = readJSON(result);
  assert.equal(summary.ok, false);
  assert.equal(summary.complete, false);
  assert.equal(summary.liveMediaSmoke.status, "skipped");
  assert.equal(summary.liveMediaSmoke.required, true);
}

{
  const result = run([
    "--dry-run",
    "--live-url",
    "http://127.0.0.1:3100",
    "--participants",
    "Tom,Caitlyn",
    "--live-timeout-ms",
    "100000",
  ]);
  assert.equal(result.status, 0);
  const summary = readJSON(result);
  assert.equal(summary.ok, true);
  assert.equal(summary.complete, false);
  assert.equal(summary.liveMediaSmoke.status, "included");
  const liveGate = summary.results.find(item => item.name === "liveMediaSmoke");
  assert.ok(liveGate, "expected liveMediaSmoke gate");
  assert.equal(
    liveGate.command,
    "node scripts/live-media-smoke.mjs --url http://127.0.0.1:3100 --participants Tom,Caitlyn --timeout-ms 100000"
  );
}

{
  const result = run(["--dry-run", "--run-xcode"]);
  assert.equal(result.status, 0);
  const summary = readJSON(result);
  assert.deepEqual(
    summary.results.slice(-3).map(item => item.name),
    ["xcodegen", "iosSimulatorXcodeTests", "macosXcodeTests"]
  );
  assert.match(summary.results.at(-2).command, /MeetingAssistAppleApp/);
  assert.match(summary.results.at(-2).command, /CODE_SIGNING_ALLOWED=NO/);
  assert.match(summary.results.at(-1).command, /MeetingAssistMacApp/);
  assert.match(summary.results.at(-1).command, /CODE_SIGNING_ALLOWED=NO/);
}

{
  const result = run(["--dry-run", "--unknown"]);
  assert.equal(result.status, 1);
  assert.match(result.stderr, /Unknown argument: --unknown/);
  assert.match(result.stderr, /native Apple local gates failed/);
}

console.log("native-apple-local-gates: 5 checks passed");
