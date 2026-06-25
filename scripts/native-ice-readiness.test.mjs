#!/usr/bin/env node
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const scriptPath = resolve(dirname(fileURLToPath(import.meta.url)), "native-ice-readiness.mjs");

function runReadiness(config, args = []) {
  const result = spawnSync(process.execPath, [scriptPath, "--stdin", ...args], {
    input: JSON.stringify(config),
    encoding: "utf8",
  });

  let parsed;
  try {
    parsed = JSON.parse(result.stdout || result.stderr);
  } catch (error) {
    throw new Error(
      `Could not parse readiness output.\nstatus=${result.status}\nstdout=${result.stdout}\nstderr=${result.stderr}\n${error}`
    );
  }

  return { status: result.status, output: parsed };
}

const validTurn = runReadiness(
  {
    rtcConfiguration: {
      iceServers: [
        { urls: "stun:stun.example.com:3478" },
        {
          urls: [
            "turn:turn.example.com:3478?transport=udp",
            "turns:turn.example.com:5349?transport=tcp",
          ],
          username: "native",
          credential: "secret",
          credentialType: "password",
        },
      ],
    },
  },
  ["--require-turn"]
);
assert.equal(validTurn.status, 0);
assert.equal(validTurn.output.ok, true);
assert.equal(validTurn.output.turnCount, 1);
assert.equal(validTurn.output.turnsCount, 1);
assert.equal(validTurn.output.turnServersWithCredentials, 1);
assert.deepEqual(validTurn.output.relayTransports, ["tcp", "udp"]);

const stunOnly = runReadiness(
  {
    rtcConfiguration: {
      iceServers: [{ urls: "stun:stun.example.com:3478" }],
    },
  },
  ["--require-turn"]
);
assert.equal(stunOnly.status, 1);
assert.equal(stunOnly.output.ok, false);
assert.match(stunOnly.output.errors.join("\n"), /No TURN or TURNS relay URLs were found/);

const unknownScheme = runReadiness({
  rtcConfiguration: {
    iceServers: [{ urls: "not-ice:example" }],
  },
});
assert.equal(unknownScheme.status, 1);
assert.equal(unknownScheme.output.ok, false);
assert.equal(unknownScheme.output.unknownUrlCount, 1);
assert.match(unknownScheme.output.errors.join("\n"), /No STUN, STUNS, TURN, or TURNS ICE server URLs were found/);

const malformedServer = runReadiness({
  rtcConfiguration: {
    iceServers: [
      { urls: [" ", 7] },
      "bad-server",
      { urls: "stuns:stun.example.com:5349" },
    ],
  },
});
assert.equal(malformedServer.status, 0);
assert.equal(malformedServer.output.ok, true);
assert.equal(malformedServer.output.iceServerCount, 1);
assert.equal(malformedServer.output.stunsCount, 1);
assert.match(malformedServer.output.warnings.join("\n"), /2 ICE server entries were ignored/);

const missingCredential = runReadiness(
  {
    rtcConfiguration: {
      iceServers: [{ urls: "turn:turn.example.com:3478?transport=udp", username: "native" }],
    },
  },
  ["--require-turn"]
);
assert.equal(missingCredential.status, 1);
assert.equal(missingCredential.output.ok, false);
assert.equal(missingCredential.output.turnServersMissingCredentials, 1);
assert.match(missingCredential.output.errors.join("\n"), /none have both username and credential/);

console.log("native-ice-readiness: 5 checks passed");
