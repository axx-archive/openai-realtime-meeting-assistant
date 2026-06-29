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
const promoteScriptPath = resolve(scriptsDir, "native-apple-promote-turn-evidence.mjs");

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
  const dir = mkdtempSync(resolve(tmpdir(), "meetingassist-promote-turn-"));
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
    "turn-promotion-room-test",
    "--created-at",
    "2026-06-29T19:00:00Z",
    "--skip-gates",
  ]);
}

function turnObservation(overrides = {}) {
  const base = {
    schemaVersion: 1,
    artifactType: "native_turn_relay_observation",
    status: "observed",
    runId: "",
    roomId: "",
    network: "restricted guest network",
    capturedAt: "2026-06-29T19:15:00Z",
    app: {
      version: "1.0",
      build: "15",
      target: "MeetingAssistAppleApp",
      clientPlatform: "ios",
    },
    device: {
      kind: "iphone",
      model: "iPhone physical",
      os: "iOS 26.5",
      physical: true,
    },
    selectedCandidate: {
      relayProtocol: "turns",
      relayCandidateType: "relay",
      relayCandidateSelected: true,
      localCandidateType: "relay",
      remoteCandidateType: "srflx",
      currentRoundTripTime: 0.082,
      protocol: "udp",
      networkType: "wifi",
    },
    iceReadiness: {
      ok: true,
      hasIceServers: true,
      iceServerCount: 2,
      knownUrlCount: 3,
      unknownUrlCount: 0,
      stunCount: 1,
      stunsCount: 0,
      turnCount: 1,
      turnsCount: 1,
      turnServersWithCredentials: 1,
      turnServersMissingCredentials: 0,
      relayTransports: ["tls", "udp"],
      warnings: [],
      errors: [],
    },
  };
  return {
    ...base,
    ...overrides,
    app: { ...base.app, ...(overrides.app ?? {}) },
    device: { ...base.device, ...(overrides.device ?? {}) },
    selectedCandidate: { ...base.selectedCandidate, ...(overrides.selectedCandidate ?? {}) },
    iceReadiness: { ...base.iceReadiness, ...(overrides.iceReadiness ?? {}) },
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
const runId = `native-apple-promote-turn-test-${process.pid}`;
const created = createProofpack(fixture.appleDir, runId);
assert.equal(created.status, 0);
assert.equal(created.output.ok, true);

const observationPath = writeObservation(fixture.dir, "turn-observation.json", turnObservation());
const promoted = promote([
  "--proofpack-dir",
  created.output.proofpackDir,
  "--input",
  observationPath,
  "--network",
  "restricted guest network",
  "--confirm-restrictive-network",
  "--confirm-same-room",
  "--promoted-at",
  "2026-06-29T19:20:00Z",
]);
assert.equal(promoted.status, 0);
assert.equal(promoted.output.ok, true);
assert.equal(promoted.output.relayProtocol, "turns");
assert.equal(promoted.output.relayCandidateType, "relay");

const draft = JSON.parse(readFileSync(promoted.output.evidenceDraft, "utf8"));
assert.equal(draft.restrictiveNetworkTurn.status, "passed");
assert.equal(draft.restrictiveNetworkTurn.network, "restricted guest network");
assert.equal(draft.restrictiveNetworkTurn.testedAt, "2026-06-29T19:15:00Z");
assert.equal(draft.restrictiveNetworkTurn.runId, runId);
assert.equal(draft.restrictiveNetworkTurn.roomId, "turn-promotion-room-test");

const artifact = JSON.parse(readFileSync(promoted.output.artifactPath, "utf8"));
assert.equal(artifact.claimScope, "restrictive_network_turn");
assert.equal(artifact.releaseEligible, true);
assert.equal(artifact.status, "passed");
assert.equal(artifact.app.build, "15");
assert.equal(artifact.device.physical, true);
assert.equal(artifact.selectedCandidate.relayCandidateSelected, true);
assert.equal(artifact.selectedCandidate.localCandidateType, "relay");
assert.equal(artifact.iceReadiness.turnServersWithCredentials, 1);
assert.equal(artifact.promotion.operatorConfirmedRestrictiveNetwork, true);
assert.equal(artifact.promotion.operatorConfirmedSameRoom, true);

const duplicatePromotion = promote([
  "--proofpack-dir",
  created.output.proofpackDir,
  "--input",
  observationPath,
  "--network",
  "restricted guest network",
  "--confirm-restrictive-network",
  "--confirm-same-room",
]);
assert.equal(duplicatePromotion.status, 1);
assert.match(duplicatePromotion.output.error, /already passed/);

const missingConfirm = promote([
  "--proofpack-dir",
  created.output.proofpackDir,
  "--input",
  writeObservation(fixture.dir, "missing-confirm.json", turnObservation()),
  "--network",
  "restricted guest network",
  "--force",
]);
assert.equal(missingConfirm.status, 1);
assert.match(missingConfirm.output.error, /confirmRestrictiveNetwork|confirmSameRoom/);

const missingNetwork = promote([
  "--proofpack-dir",
  created.output.proofpackDir,
  "--input",
  writeObservation(fixture.dir, "missing-network.json", turnObservation()),
  "--confirm-restrictive-network",
  "--confirm-same-room",
  "--force",
]);
assert.equal(missingNetwork.status, 1);
assert.match(missingNetwork.output.error, /--network is required/);

const nonRelayRejected = promote([
  "--proofpack-dir",
  created.output.proofpackDir,
  "--input",
  writeObservation(
    fixture.dir,
    "non-relay.json",
    turnObservation({
      selectedCandidate: {
        relayProtocol: "stun",
        relayCandidateType: "host",
        relayCandidateSelected: false,
        localCandidateType: "host",
        remoteCandidateType: "srflx",
        currentRoundTripTime: 0,
      },
    })
  ),
  "--network",
  "restricted guest network",
  "--confirm-restrictive-network",
  "--confirm-same-room",
  "--force",
]);
assert.equal(nonRelayRejected.status, 1);
assert.match(nonRelayRejected.output.error, /selectedCandidate/);

const promotedArtifactInputRejected = promote([
  "--proofpack-dir",
  created.output.proofpackDir,
  "--input",
  writeObservation(
    fixture.dir,
    "already-promoted-input.json",
    turnObservation({
      artifactType: "native_restrictive_turn",
    })
  ),
  "--network",
  "restricted guest network",
  "--confirm-restrictive-network",
  "--confirm-same-room",
  "--force",
]);
assert.equal(promotedArtifactInputRejected.status, 1);
assert.match(promotedArtifactInputRejected.output.error, /artifactType/);

const wrongBuildRejected = promote([
  "--proofpack-dir",
  created.output.proofpackDir,
  "--input",
  writeObservation(fixture.dir, "wrong-build.json", turnObservation({ app: { build: "14" } })),
  "--network",
  "restricted guest network",
  "--confirm-restrictive-network",
  "--confirm-same-room",
  "--force",
]);
assert.equal(wrongBuildRejected.status, 1);
assert.match(wrongBuildRejected.output.error, /app.build/);

const mismatchedRunRejected = promote([
  "--proofpack-dir",
  created.output.proofpackDir,
  "--input",
  writeObservation(fixture.dir, "wrong-run.json", turnObservation({ runId: "native-apple-other-run" })),
  "--network",
  "restricted guest network",
  "--confirm-restrictive-network",
  "--confirm-same-room",
  "--force",
]);
assert.equal(mismatchedRunRejected.status, 1);
assert.match(mismatchedRunRejected.output.error, /runId/);

const networkMismatchRejected = promote([
  "--proofpack-dir",
  created.output.proofpackDir,
  "--input",
  writeObservation(fixture.dir, "network-mismatch.json", turnObservation({ network: "carrier hotspot restricted" })),
  "--network",
  "restricted guest network",
  "--confirm-restrictive-network",
  "--confirm-same-room",
  "--force",
]);
assert.equal(networkMismatchRejected.status, 1);
assert.match(networkMismatchRejected.output.error, /network.match/);

const unsafeRejected = promote([
  "--proofpack-dir",
  created.output.proofpackDir,
  "--input",
  writeObservation(
    fixture.dir,
    "unsafe.json",
    turnObservation({
      diagnostics: {
        rawSdp: "v=0\r\na=candidate:842163049 1 udp 1677729535 192.168.1.25 56143 typ host\r\n",
      },
    })
  ),
  "--network",
  "restricted guest network",
  "--confirm-restrictive-network",
  "--confirm-same-room",
  "--force",
]);
assert.equal(unsafeRejected.status, 1);
assert.match(unsafeRejected.output.error, /unsafeContent/);

const iceWarningsRejected = promote([
  "--proofpack-dir",
  created.output.proofpackDir,
  "--input",
  writeObservation(
    fixture.dir,
    "ice-warning.json",
    turnObservation({ iceReadiness: { warnings: ["1 TURN server entries are missing username or credential."] } })
  ),
  "--network",
  "restricted guest network",
  "--confirm-restrictive-network",
  "--confirm-same-room",
  "--force",
]);
assert.equal(iceWarningsRejected.status, 1);
assert.match(iceWarningsRejected.output.error, /iceReadiness.warnings/);

rmSync(fixture.dir, { recursive: true, force: true });

console.log("native-apple-promote-turn-evidence: 11 checks passed");
