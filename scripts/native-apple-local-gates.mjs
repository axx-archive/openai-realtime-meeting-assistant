#!/usr/bin/env node
import { spawnSync } from "node:child_process";
import { dirname, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const rootDir = resolve(dirname(fileURLToPath(import.meta.url)), "..");

function usage() {
  return [
    "Usage:",
    "  node scripts/native-apple-local-gates.mjs [--apple-dir apple]",
    "    [--live-url url] [--participants Tom,Caitlyn] [--live-timeout-ms 100000]",
    "    [--require-live-media-smoke] [--run-xcode] [--dry-run]",
    "",
    "Runs the repo-owned native Apple checkpoint gates in one place. The live",
    "media smoke is only executed when --live-url is provided; without it the",
    "summary is intentionally incomplete unless --require-live-media-smoke is",
    "used to make that omission fail the command.",
  ].join("\n");
}

function parseArgs(argv) {
  const args = {
    appleDir: "apple",
    liveUrl: "",
    participants: "Tom,Caitlyn",
    liveTimeoutMs: 100000,
    requireLiveMediaSmoke: false,
    runXcode: false,
    dryRun: false,
    help: false,
  };

  for (let index = 0; index < argv.length; index += 1) {
    const arg = argv[index];
    if (arg === "--apple-dir") {
      args.appleDir = requiredValue(argv, index, arg);
      index += 1;
    } else if (arg === "--live-url") {
      args.liveUrl = requiredValue(argv, index, arg);
      index += 1;
    } else if (arg === "--participants") {
      args.participants = requiredValue(argv, index, arg);
      index += 1;
    } else if (arg === "--live-timeout-ms") {
      args.liveTimeoutMs = positiveNumber(requiredValue(argv, index, arg), arg);
      index += 1;
    } else if (arg === "--require-live-media-smoke") {
      args.requireLiveMediaSmoke = true;
    } else if (arg === "--run-xcode") {
      args.runXcode = true;
    } else if (arg === "--dry-run") {
      args.dryRun = true;
    } else if (arg === "--help" || arg === "-h") {
      args.help = true;
    } else {
      throw new Error(`Unknown argument: ${arg}`);
    }
  }

  return args;
}

function requiredValue(argv, index, flag) {
  const value = argv[index + 1] ?? "";
  if (!value || value.startsWith("--")) {
    throw new Error(`${flag} requires a value.`);
  }
  return value;
}

function positiveNumber(value, flag) {
  const parsed = Number(value);
  if (!Number.isFinite(parsed) || parsed <= 0) {
    throw new Error(`${flag} must be a positive number.`);
  }
  return parsed;
}

function commandString(command) {
  return [command.command, ...command.args].join(" ");
}

function gate(name, command, args, options = {}) {
  return {
    name,
    command,
    args,
    cwd: options.cwd ?? rootDir,
    required: options.required ?? true,
  };
}

function buildGatePlan(args) {
  const appleDir = resolve(rootDir, args.appleDir);
  const gates = [
    gate("mediaFixVerification", "node", ["scripts/media-fix-verification.mjs"]),
    gate("voiceFocusBenchmark", "node", ["scripts/voice-focus-benchmark.mjs"]),
    gate("goTests", "go", ["test", "./..."]),
    gate("swiftPackageTests", "swift", ["test", "--package-path", repoRelative(appleDir)]),
    gate("releaseReadiness", "node", ["scripts/native-apple-release-readiness.mjs", "--apple-dir", repoRelative(appleDir)]),
  ];

  if (args.liveUrl) {
    gates.push(
      gate("liveMediaSmoke", "node", [
        "scripts/live-media-smoke.mjs",
        "--url",
        args.liveUrl,
        "--participants",
        args.participants,
        "--timeout-ms",
        String(args.liveTimeoutMs),
      ])
    );
  }

  if (args.runXcode) {
    gates.push(
      gate("xcodegen", "xcodegen", ["generate", "--spec", "project.yml"], { cwd: appleDir }),
      gate("iosSimulatorXcodeTests", "xcodebuild", [
        "-project",
        "MeetingAssist.xcodeproj",
        "-scheme",
        "MeetingAssistAppleApp",
        "-destination",
        "platform=iOS Simulator,name=iPhone 17",
        "test",
        "CODE_SIGNING_ALLOWED=NO",
      ], { cwd: appleDir }),
      gate("macosXcodeTests", "xcodebuild", [
        "-project",
        "MeetingAssist.xcodeproj",
        "-scheme",
        "MeetingAssistMacApp",
        "-destination",
        "platform=macOS,arch=arm64",
        "test",
        "CODE_SIGNING_ALLOWED=NO",
      ], { cwd: appleDir })
    );
  }

  return gates;
}

function repoRelative(path) {
  const value = relative(rootDir, path);
  return value || ".";
}

function runGate(command, dryRun) {
  const startedAt = new Date().toISOString();
  if (dryRun) {
    return {
      name: command.name,
      command: commandString(command),
      cwd: repoRelative(command.cwd),
      status: "planned",
      exitCode: null,
      startedAt,
      completedAt: new Date().toISOString(),
      outputTail: "",
    };
  }

  const result = spawnSync(command.command, command.args, {
    cwd: command.cwd,
    encoding: "utf8",
    maxBuffer: 1024 * 1024 * 20,
    stdio: ["ignore", "pipe", "pipe"],
  });
  return {
    name: command.name,
    command: commandString(command),
    cwd: repoRelative(command.cwd),
    status: result.status === 0 ? "passed" : "failed",
    exitCode: result.status,
    startedAt,
    completedAt: new Date().toISOString(),
    outputTail: `${result.stdout ?? ""}${result.stderr ?? ""}`.slice(-4000),
  };
}

function liveMediaSmokeSummary(args) {
  if (args.liveUrl) {
    return {
      status: "included",
      required: true,
      url: args.liveUrl,
      participants: args.participants,
      timeoutMs: args.liveTimeoutMs,
    };
  }

  return {
    status: "skipped",
    required: args.requireLiveMediaSmoke,
    url: "",
    participants: args.participants,
    timeoutMs: args.liveTimeoutMs,
    blocker: "Provide --live-url <local-or-live-url> to run scripts/live-media-smoke.mjs before a mergeable native Apple checkpoint.",
  };
}

function summarize(args, results) {
  const liveMediaSmoke = liveMediaSmokeSummary(args);
  const failed = results.filter(result => result.status === "failed");
  const planned = results.filter(result => result.status === "planned");
  const missingRequiredLiveSmoke = liveMediaSmoke.status === "skipped" && args.requireLiveMediaSmoke;
  const complete = failed.length === 0 && liveMediaSmoke.status !== "skipped" && planned.length === 0;
  const ok = failed.length === 0 && !missingRequiredLiveSmoke;

  const warnings = [];
  if (liveMediaSmoke.status === "skipped") {
    warnings.push(liveMediaSmoke.blocker);
  }
  if (planned.length > 0) {
    warnings.push("Dry run only: no gates were executed.");
  }
  if (!args.runXcode) {
    warnings.push("Xcode simulator/macOS tests were not included; pass --run-xcode for those local gates.");
  }

  return {
    ok,
    complete,
    dryRun: args.dryRun,
    runXcode: args.runXcode,
    liveMediaSmoke,
    failed: failed.map(result => result.name),
    warnings,
    results,
  };
}

function exitCodeForSummary(summary) {
  if (!summary.ok) {
    return 1;
  }
  return 0;
}

async function main(argv = process.argv.slice(2)) {
  try {
    const args = parseArgs(argv);
    if (args.help) {
      console.log(usage());
      return 0;
    }

    const plan = buildGatePlan(args);
    const results = plan.map(command => runGate(command, args.dryRun));
    const summary = summarize(args, results);
    console.log(JSON.stringify(summary, null, 2));
    return exitCodeForSummary(summary);
  } catch (error) {
    console.error(`native Apple local gates failed: ${error.message}`);
    console.error(usage());
    return 1;
  }
}

const isMain = process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url);

if (isMain) {
  process.exitCode = await main();
}

export {
  buildGatePlan,
  exitCodeForSummary,
  liveMediaSmokeSummary,
  main,
  parseArgs,
  summarize,
};
