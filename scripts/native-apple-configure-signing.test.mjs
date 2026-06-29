#!/usr/bin/env node
import assert from "node:assert/strict";
import { existsSync, mkdtempSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { spawnSync } from "node:child_process";

const scriptsDir = dirname(fileURLToPath(import.meta.url));
const rootDir = resolve(scriptsDir, "..");
const scriptPath = resolve(scriptsDir, "native-apple-configure-signing.mjs");

function syntheticTeamId(...parts) {
  return parts.join("");
}

function runSigning(args = [], env = {}) {
  const result = spawnSync(process.execPath, [scriptPath, ...args], {
    cwd: rootDir,
    encoding: "utf8",
    env: {
      ...process.env,
      DEVELOPMENT_TEAM: "",
      APPLE_DEVELOPMENT_TEAM: "",
      ...env,
    },
  });
  let output;
  try {
    output = JSON.parse(result.stdout || result.stderr);
  } catch (error) {
    throw new Error(`Could not parse signing output.\nstatus=${result.status}\nstdout=${result.stdout}\nstderr=${result.stderr}\n${error}`);
  }
  return { status: result.status, stdout: result.stdout, stderr: result.stderr, output };
}

function writeFixtureFile(path, contents) {
  mkdirSync(dirname(path), { recursive: true });
  writeFileSync(path, contents);
}

function makeAppleFixture(overrides = {}) {
  const dir = mkdtempSync(resolve(tmpdir(), "meetingassist-signing-"));
  const appleDir = resolve(dir, "apple");
  writeFixtureFile(
    resolve(appleDir, "Config", "Signing.xcconfig"),
    overrides.signingConfig ??
      `CODE_SIGN_STYLE = Automatic
DEVELOPMENT_TEAM = $(APPLE_DEVELOPMENT_TEAM)
#include? "Signing.local.xcconfig"
`
  );
  writeFixtureFile(
    resolve(appleDir, "project.yml"),
    overrides.projectYml ??
      `targets:
  MeetingAssistAppleApp:
    configFiles:
      Debug: Config/Signing.xcconfig
      Release: Config/Signing.xcconfig
`
  );
  writeFixtureFile(
    resolve(appleDir, "MeetingAssist.xcodeproj", "project.pbxproj"),
    overrides.pbxproj ?? 'DevelopmentTeam = "$(APPLE_DEVELOPMENT_TEAM)";\n'
  );
  return { dir, appleDir };
}

const teamId = syntheticTeamId("A1", "B2", "C3", "D4", "E5");
const fixture = makeAppleFixture();
const configured = runSigning([
  "--apple-dir",
  fixture.appleDir,
  "--team-id",
  teamId.toLowerCase(),
  "--confirm-local-only",
]);
assert.equal(configured.status, 0);
assert.equal(configured.output.ok, true);
assert.equal(configured.output.teamIdRedacted, "A1******E5");
assert.equal(configured.stdout.includes(teamId), false);
const localSigningPath = resolve(fixture.appleDir, "Config", "Signing.local.xcconfig");
assert.equal(existsSync(localSigningPath), true);
assert.match(readFileSync(localSigningPath, "utf8"), new RegExp(`DEVELOPMENT_TEAM = ${teamId}`));

const validateLocal = runSigning(["--apple-dir", fixture.appleDir, "--validate-only"]);
assert.equal(validateLocal.status, 0);
assert.equal(validateLocal.output.ok, true);
assert.deepEqual(validateLocal.output.configuredTeams, [
  {
    source: "Config/Signing.local.xcconfig",
    teamIdRedacted: "A1******E5",
  },
]);

const validateEnvFixture = makeAppleFixture();
const validateEnv = runSigning(["--apple-dir", validateEnvFixture.appleDir, "--validate-only"], {
  APPLE_DEVELOPMENT_TEAM: syntheticTeamId("B1", "C2", "D3", "E4", "F5"),
});
assert.equal(validateEnv.status, 0);
assert.equal(validateEnv.output.ok, true);
assert.equal(validateEnv.output.configuredTeams[0].source, "APPLE_DEVELOPMENT_TEAM");
assert.equal(validateEnv.output.configuredTeams[0].teamIdRedacted, "B1******F5");

const validateDevelopmentTeamFixture = makeAppleFixture();
const validateDevelopmentTeam = runSigning(["--apple-dir", validateDevelopmentTeamFixture.appleDir, "--validate-only"], {
  DEVELOPMENT_TEAM: syntheticTeamId("D1", "E2", "F3", "G4", "H5"),
});
assert.equal(validateDevelopmentTeam.status, 0);
assert.equal(validateDevelopmentTeam.output.ok, true);
assert.equal(validateDevelopmentTeam.output.configuredTeams[0].source, "DEVELOPMENT_TEAM");
assert.equal(validateDevelopmentTeam.output.configuredTeams[0].teamIdRedacted, "D1******H5");

const validateMissingFixture = makeAppleFixture();
const validateMissing = runSigning(["--apple-dir", validateMissingFixture.appleDir, "--validate-only"]);
assert.equal(validateMissing.status, 1);
assert.equal(validateMissing.output.ok, false);
assert.deepEqual(validateMissing.output.configuredTeams, []);

const noConfirmFixture = makeAppleFixture();
const noConfirm = runSigning(["--apple-dir", noConfirmFixture.appleDir, "--team-id", teamId]);
assert.equal(noConfirm.status, 1);
assert.match(noConfirm.output.error, /confirm-local-only/);

const placeholderFixture = makeAppleFixture();
const placeholder = runSigning([
  "--apple-dir",
  placeholderFixture.appleDir,
  "--team-id",
  "ABCDE12345",
  "--confirm-local-only",
]);
assert.equal(placeholder.status, 1);
assert.match(placeholder.output.error, /not a placeholder/);

const duplicate = runSigning(["--apple-dir", fixture.appleDir, "--team-id", teamId, "--confirm-local-only"]);
assert.equal(duplicate.status, 1);
assert.match(duplicate.output.error, /already exists/);

const committedTeamFixture = makeAppleFixture({
  signingConfig: `DEVELOPMENT_TEAM = ${syntheticTeamId("C1", "D2", "E3", "F4", "G5")}\n`,
});
const committedTeam = runSigning([
  "--apple-dir",
  committedTeamFixture.appleDir,
  "--team-id",
  teamId,
  "--confirm-local-only",
]);
assert.equal(committedTeam.status, 1);
assert.match(committedTeam.output.error, /Tracked files contain committed Apple Team IDs/);

const unignoredRootFixtureDir = mkdtempSync(resolve(rootDir, ".tmp-signing-unignored-"));
const unignoredRootFixture = makeAppleFixture();
const unignoredAppleDir = resolve(unignoredRootFixtureDir, "apple");
mkdirSync(resolve(unignoredAppleDir, "Config"), { recursive: true });
writeFixtureFile(
  resolve(unignoredAppleDir, "Config", "Signing.xcconfig"),
  readFileSync(resolve(unignoredRootFixture.appleDir, "Config", "Signing.xcconfig"), "utf8")
);
writeFixtureFile(resolve(unignoredAppleDir, "project.yml"), readFileSync(resolve(unignoredRootFixture.appleDir, "project.yml"), "utf8"));
writeFixtureFile(
  resolve(unignoredAppleDir, "MeetingAssist.xcodeproj", "project.pbxproj"),
  readFileSync(resolve(unignoredRootFixture.appleDir, "MeetingAssist.xcodeproj", "project.pbxproj"), "utf8")
);
const unignoredLocalPath = runSigning([
  "--apple-dir",
  unignoredAppleDir,
  "--team-id",
  teamId,
  "--confirm-local-only",
]);
assert.equal(unignoredLocalPath.status, 1);
assert.match(unignoredLocalPath.output.error, /must be ignored/);

rmSync(fixture.dir, { recursive: true, force: true });
rmSync(validateEnvFixture.dir, { recursive: true, force: true });
rmSync(validateDevelopmentTeamFixture.dir, { recursive: true, force: true });
rmSync(validateMissingFixture.dir, { recursive: true, force: true });
rmSync(noConfirmFixture.dir, { recursive: true, force: true });
rmSync(placeholderFixture.dir, { recursive: true, force: true });
rmSync(committedTeamFixture.dir, { recursive: true, force: true });
rmSync(unignoredRootFixture.dir, { recursive: true, force: true });
rmSync(unignoredRootFixtureDir, { recursive: true, force: true });

console.log("native-apple-configure-signing: 10 checks passed");
