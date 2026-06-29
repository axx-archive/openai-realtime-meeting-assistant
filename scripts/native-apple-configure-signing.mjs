#!/usr/bin/env node
import { existsSync, mkdirSync, readFileSync, renameSync, writeFileSync } from "node:fs";
import { dirname, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const rootDir = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const placeholderTeams = new Set(["ABCDE12345", "YOURTEAMID", "YOUR_TEAM_ID", "TEAMID1234"]);

function usage() {
  return [
    "Usage:",
    "  node scripts/native-apple-configure-signing.mjs --team-id TEAMID",
    "    [--apple-dir apple] --confirm-local-only [--force]",
    "  node scripts/native-apple-configure-signing.mjs --validate-only [--apple-dir apple]",
    "",
    "Writes ignored apple/Config/Signing.local.xcconfig for local Apple signing.",
    "The command refuses placeholder Team IDs and committed Team IDs in tracked",
    "project/config files. It redacts Team IDs in JSON output.",
  ].join("\n");
}

function parseArgs(argv) {
  const args = {
    appleDir: "apple",
    teamId: "",
    confirmLocalOnly: false,
    validateOnly: false,
    force: false,
    help: false,
  };

  for (let index = 0; index < argv.length; index += 1) {
    const arg = argv[index];
    if (arg === "--apple-dir") {
      args.appleDir = requiredValue(argv, index, arg);
      index += 1;
    } else if (arg === "--team-id") {
      args.teamId = requiredValue(argv, index, arg);
      index += 1;
    } else if (arg === "--confirm-local-only") {
      args.confirmLocalOnly = true;
    } else if (arg === "--validate-only") {
      args.validateOnly = true;
    } else if (arg === "--force") {
      args.force = true;
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

function readText(path) {
  return readFileSync(path, "utf8");
}

function writeTextAtomic(path, text) {
  mkdirSync(dirname(path), { recursive: true });
  const tempPath = `${path}.tmp-${process.pid}-${Date.now()}`;
  writeFileSync(tempPath, text);
  renameSync(tempPath, path);
}

function cleanBuildSettingValue(value) {
  return String(value ?? "")
    .trim()
    .replace(/^["']|["']$/g, "")
    .replace(/;$/, "")
    .trim();
}

function normalizeTeamId(value) {
  return cleanBuildSettingValue(value).toUpperCase();
}

function validDevelopmentTeam(value) {
  const normalized = normalizeTeamId(value);
  return /^[A-Z0-9]{10}$/.test(normalized) && !placeholderTeams.has(normalized);
}

function redactTeamId(value) {
  const normalized = normalizeTeamId(value);
  if (!normalized) {
    return "";
  }
  if (normalized.length <= 4) {
    return "*".repeat(normalized.length);
  }
  return `${normalized.slice(0, 2)}${"*".repeat(Math.max(0, normalized.length - 4))}${normalized.slice(-2)}`;
}

function stripXcconfigComment(line) {
  const commentStart = line.indexOf("//");
  return (commentStart === -1 ? line : line.slice(0, commentStart)).trim();
}

function parseXcconfigSettings(path, options = {}, seen = new Set()) {
  const { includeOptional = true } = options;
  const settings = {};
  if (!existsSync(path)) {
    return settings;
  }

  const resolved = resolve(path);
  if (seen.has(resolved)) {
    return settings;
  }
  seen.add(resolved);

  for (const rawLine of readText(resolved).split(/\r?\n/)) {
    const line = stripXcconfigComment(rawLine);
    if (!line) {
      continue;
    }

    const includeMatch = /^#include(\?)?\s+"([^"]+)"/.exec(line);
    if (includeMatch) {
      const optional = includeMatch[1] === "?";
      if (optional && !includeOptional) {
        continue;
      }
      const includePath = resolve(dirname(resolved), includeMatch[2]);
      if (existsSync(includePath) || !optional) {
        Object.assign(settings, parseXcconfigSettings(includePath, options, seen));
      }
      continue;
    }

    const assignmentMatch = /^([A-Za-z0-9_]+)\s*=\s*(.*)$/.exec(line);
    if (assignmentMatch) {
      settings[assignmentMatch[1]] = cleanBuildSettingValue(assignmentMatch[2]);
    }
  }

  return settings;
}

function developmentTeamValuesFromText(text) {
  const values = [];
  const patterns = [
    /DEVELOPMENT_TEAM:\s*([^\n#]+)/g,
    /DEVELOPMENT_TEAM\s*=\s*([^;\n#]+)/g,
    /DevelopmentTeam:\s*([^\n#]+)/g,
    /DevelopmentTeam\s*=\s*([^;\n#]+)/g,
  ];
  for (const pattern of patterns) {
    for (const match of text.matchAll(pattern)) {
      values.push(cleanBuildSettingValue(match[1]));
    }
  }
  return values;
}

function committedTeamProblems(appleDir) {
  const files = [
    ["project.yml", resolve(appleDir, "project.yml")],
    ["MeetingAssist.xcodeproj/project.pbxproj", resolve(appleDir, "MeetingAssist.xcodeproj", "project.pbxproj")],
    ["Config/Signing.xcconfig", resolve(appleDir, "Config", "Signing.xcconfig")],
  ];
  const problems = [];
  for (const [label, path] of files) {
    if (!existsSync(path)) {
      continue;
    }
    if (developmentTeamValuesFromText(readText(path)).some(validDevelopmentTeam)) {
      problems.push(label);
    }
  }
  return problems;
}

function gitignoreCoversLocalSigning(path) {
  const relativePath = relative(rootDir, path).split(/[/\\]/).join("/");
  if (relativePath.startsWith("..")) {
    return true;
  }
  const gitignorePath = resolve(rootDir, ".gitignore");
  if (!existsSync(gitignorePath)) {
    return false;
  }
  return readText(gitignorePath)
    .split(/\r?\n/)
    .map((line) => line.trim())
    .includes(relativePath);
}

function localSigningText(teamId) {
  return [
    "// Generated by scripts/native-apple-configure-signing.mjs.",
    "// Keep this file out of git. It contains account-specific Apple signing configuration.",
    `DEVELOPMENT_TEAM = ${teamId}`,
    "",
  ].join("\n");
}

function configuredTeamSources(appleDir) {
  const localSigningPath = resolve(appleDir, "Config", "Signing.local.xcconfig");
  const localSettings = parseXcconfigSettings(localSigningPath);
  const sources = [];
  if (validDevelopmentTeam(process.env.DEVELOPMENT_TEAM)) {
    sources.push({ source: "DEVELOPMENT_TEAM", teamId: normalizeTeamId(process.env.DEVELOPMENT_TEAM) });
  }
  if (validDevelopmentTeam(process.env.APPLE_DEVELOPMENT_TEAM)) {
    sources.push({ source: "APPLE_DEVELOPMENT_TEAM", teamId: normalizeTeamId(process.env.APPLE_DEVELOPMENT_TEAM) });
  }
  if (validDevelopmentTeam(localSettings.DEVELOPMENT_TEAM)) {
    sources.push({ source: "Config/Signing.local.xcconfig", teamId: normalizeTeamId(localSettings.DEVELOPMENT_TEAM) });
  }
  return sources;
}

function configure(args) {
  const appleDir = resolve(process.cwd(), args.appleDir);
  const signingConfigPath = resolve(appleDir, "Config", "Signing.xcconfig");
  const localSigningPath = resolve(appleDir, "Config", "Signing.local.xcconfig");
  if (!existsSync(signingConfigPath)) {
    throw new Error(`Missing signing config: ${signingConfigPath}`);
  }

  const committedProblems = committedTeamProblems(appleDir);
  if (committedProblems.length > 0) {
    throw new Error(`Tracked files contain committed Apple Team IDs: ${committedProblems.join(", ")}`);
  }

  if (!gitignoreCoversLocalSigning(localSigningPath)) {
    throw new Error("apple/Config/Signing.local.xcconfig must be ignored before configuring local signing.");
  }

  if (args.validateOnly) {
    const sources = configuredTeamSources(appleDir);
    return {
      ok: sources.length > 0,
      mode: "validate",
      appleDir,
      localSigningPath,
      configuredTeams: sources.map((item) => ({
        source: item.source,
        teamIdRedacted: redactTeamId(item.teamId),
      })),
      committedTeamIds: false,
      nextSteps:
        sources.length > 0
          ? ["Run node scripts/native-apple-release-readiness.mjs --strict."]
          : ["Run this script with --team-id TEAMID --confirm-local-only before Apple archive/device work."],
    };
  }

  if (!args.teamId) {
    throw new Error("--team-id is required unless --validate-only is used.");
  }
  const teamId = normalizeTeamId(args.teamId);
  if (!validDevelopmentTeam(teamId)) {
    throw new Error("--team-id must be a real 10-character Apple Developer Team ID, not a placeholder.");
  }
  if (!args.confirmLocalOnly) {
    throw new Error("--confirm-local-only is required so account-specific signing stays out of git.");
  }
  if (existsSync(localSigningPath) && !args.force) {
    throw new Error(`${localSigningPath} already exists. Use --force to replace it.`);
  }

  writeTextAtomic(localSigningPath, localSigningText(teamId));
  return {
    ok: true,
    mode: "write",
    appleDir,
    localSigningPath,
    teamIdRedacted: redactTeamId(teamId),
    committedTeamIds: false,
    nextSteps: [
      "Run node scripts/native-apple-configure-signing.mjs --validate-only.",
      "Run node scripts/native-apple-release-readiness.mjs --strict.",
      "Use this local signing config only for archive/device/TestFlight work on this machine.",
    ],
  };
}

function main() {
  try {
    const args = parseArgs(process.argv.slice(2));
    if (args.help) {
      console.log(usage());
      return;
    }
    const result = configure(args);
    console.log(JSON.stringify(result, null, 2));
    if (!result.ok) {
      process.exitCode = 1;
    }
  } catch (error) {
    console.error(JSON.stringify({ ok: false, error: error.message, usage: usage() }, null, 2));
    process.exitCode = 1;
  }
}

main();
