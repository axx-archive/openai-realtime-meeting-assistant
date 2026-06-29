#!/usr/bin/env node
import { spawnSync } from "node:child_process";
import { existsSync, mkdirSync, readFileSync, renameSync, writeFileSync } from "node:fs";
import { dirname, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const rootDir = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const manifestRelativePath = "Xcode/PrivacyInfo.xcprivacy";

function usage() {
  return [
    "Usage:",
    "  node scripts/native-apple-generate-privacy-manifest.mjs --decisions-file path",
    "    [--apple-dir apple] [--output apple/Xcode/PrivacyInfo.xcprivacy]",
    "    --confirm-approved [--wire-project] [--generate-xcode-project]",
    "",
    "Generates PrivacyInfo.xcprivacy from approved product/legal decisions.",
    "The script refuses placeholder decisions, empty collected-data declarations,",
    "tracking without domains, and secret-shaped values.",
  ].join("\n");
}

function parseArgs(argv) {
  const args = {
    appleDir: "apple",
    decisionsFile: "",
    output: "",
    confirmApproved: false,
    wireProject: false,
    generateXcodeProject: false,
    help: false,
  };

  for (let index = 0; index < argv.length; index += 1) {
    const arg = argv[index];
    if (arg === "--apple-dir") {
      args.appleDir = requiredValue(argv, index, arg);
      index += 1;
    } else if (arg === "--decisions-file") {
      args.decisionsFile = requiredValue(argv, index, arg);
      index += 1;
    } else if (arg === "--output") {
      args.output = requiredValue(argv, index, arg);
      index += 1;
    } else if (arg === "--confirm-approved") {
      args.confirmApproved = true;
    } else if (arg === "--wire-project") {
      args.wireProject = true;
    } else if (arg === "--generate-xcode-project") {
      args.generateXcodeProject = true;
      args.wireProject = true;
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

function readJSON(path) {
  return JSON.parse(readFileSync(path, "utf8"));
}

function writeTextAtomic(path, text) {
  mkdirSync(dirname(path), { recursive: true });
  const tempPath = `${path}.tmp-${process.pid}-${Date.now()}`;
  writeFileSync(tempPath, text);
  renameSync(tempPath, path);
}

function validTimestamp(value) {
  return typeof value === "string" && /^\d{4}-\d{2}-\d{2}T/.test(value) && !Number.isNaN(Date.parse(value));
}

function nonPlaceholderString(value) {
  if (typeof value !== "string") {
    return false;
  }
  const trimmed = value.trim();
  if (!trimmed || /^<[^>]+>$/.test(trimmed) || /^0+$/.test(trimmed)) {
    return false;
  }
  const normalized = trimmed.toUpperCase().replace(/[\s-]+/g, "_");
  return ![
    "TODO",
    "TBD",
    "FIXME",
    "CHANGE_ME",
    "YOUR_VALUE",
    "PLACEHOLDER",
    "EXAMPLE",
    "SAMPLE",
    "DUMMY",
    "UNKNOWN",
    "N_A",
    "NA",
    "NONE",
    "NULL",
  ].includes(normalized);
}

function collectUnsafeContent(value, path = "$") {
  const problems = [];
  if (Array.isArray(value)) {
    value.forEach((item, index) => {
      problems.push(...collectUnsafeContent(item, `${path}[${index}]`));
    });
    return problems;
  }
  if (!value || typeof value !== "object") {
    if (typeof value === "string") {
      const trimmed = value.trim();
      if (
        /\bsk-[A-Za-z0-9_-]{20,}\b/.test(trimmed) ||
        /\bAKIA[0-9A-Z]{16}\b/.test(trimmed) ||
        /\b[A-Z0-9]{10}\b/.test(trimmed) ||
        /\b[A-Za-z0-9_-]{20,}\.[A-Za-z0-9_-]{20,}\.[A-Za-z0-9_-]{20,}\b/.test(trimmed) ||
        /\b[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}\b/i.test(trimmed) ||
        /\/Users\/[^/\s]+/i.test(trimmed) ||
        /-----BEGIN [A-Z ]*PRIVATE KEY-----/.test(trimmed) ||
        /-----BEGIN CERTIFICATE-----/.test(trimmed) ||
        /\.(p8|p12|mobileprovision|provisionprofile)\b/i.test(trimmed)
      ) {
        problems.push(path);
      }
    }
    return problems;
  }

  for (const [key, item] of Object.entries(value)) {
    if (
      /(secret|password|passwd|token|credential|api_?key|apikey|private_?key|provision|certificate|cert|p8|p12|team_?id|development_?team|issuer_?id|key_?id|keychain|profile|authorization|jwt|apple_?id|username|headers?|cookies?)/i.test(
        key
      )
    ) {
      problems.push(`${path}.${key}`);
    }
    problems.push(...collectUnsafeContent(item, `${path}.${key}`));
  }
  return problems;
}

function validateStringArray(value, label, options = {}) {
  const { allowEmpty = false } = options;
  const problems = [];
  if (!Array.isArray(value)) {
    return [`${label}:not_array`];
  }
  if (!allowEmpty && value.length === 0) {
    problems.push(`${label}:empty`);
  }
  value.forEach((item, index) => {
    if (!nonPlaceholderString(item)) {
      problems.push(`${label}[${index}]`);
    }
  });
  return problems;
}

function validateDecisions(decisions, args) {
  const problems = [];
  if (!decisions || typeof decisions !== "object" || Array.isArray(decisions)) {
    return ["decisions:not_object"];
  }
  if (decisions.schemaVersion !== 1) {
    problems.push("schemaVersion");
  }
  if (!args.confirmApproved) {
    problems.push("confirmApproved");
  }
  const approval = decisions.approval;
  if (!approval || typeof approval !== "object" || Array.isArray(approval)) {
    problems.push("approval");
  } else {
    if (approval.approved !== true) {
      problems.push("approval.approved");
    }
    if (!validTimestamp(approval.approvedAt)) {
      problems.push("approval.approvedAt");
    }
    if (!nonPlaceholderString(approval.approvedBy)) {
      problems.push("approval.approvedBy");
    }
  }

  if (typeof decisions.tracking !== "boolean") {
    problems.push("tracking");
  }
  problems.push(...validateStringArray(decisions.trackingDomains, "trackingDomains", { allowEmpty: true }));
  if (decisions.tracking === true && Array.isArray(decisions.trackingDomains) && decisions.trackingDomains.length === 0) {
    problems.push("trackingDomains:required_when_tracking");
  }

  if (!Array.isArray(decisions.accessedAPITypes)) {
    problems.push("accessedAPITypes:not_array");
  } else {
    decisions.accessedAPITypes.forEach((entry, index) => {
      if (!entry || typeof entry !== "object" || Array.isArray(entry)) {
        problems.push(`accessedAPITypes[${index}]`);
        return;
      }
      if (!nonPlaceholderString(entry.apiType)) {
        problems.push(`accessedAPITypes[${index}].apiType`);
      }
      problems.push(...validateStringArray(entry.reasons, `accessedAPITypes[${index}].reasons`));
    });
  }

  if (!Array.isArray(decisions.collectedDataTypes)) {
    problems.push("collectedDataTypes:not_array");
  } else if (decisions.collectedDataTypes.length === 0) {
    problems.push("collectedDataTypes:empty");
  } else {
    decisions.collectedDataTypes.forEach((entry, index) => {
      if (!entry || typeof entry !== "object" || Array.isArray(entry)) {
        problems.push(`collectedDataTypes[${index}]`);
        return;
      }
      if (!nonPlaceholderString(entry.dataType)) {
        problems.push(`collectedDataTypes[${index}].dataType`);
      }
      if (typeof entry.linked !== "boolean") {
        problems.push(`collectedDataTypes[${index}].linked`);
      }
      if (typeof entry.tracking !== "boolean") {
        problems.push(`collectedDataTypes[${index}].tracking`);
      }
      problems.push(...validateStringArray(entry.purposes, `collectedDataTypes[${index}].purposes`));
    });
  }

  const unsafe = collectUnsafeContent(decisions);
  if (unsafe.length > 0) {
    problems.push(`unsafeContent:${unsafe.slice(0, 4).join("|")}`);
  }
  return [...new Set(problems)];
}

function escapeXml(value) {
  return String(value)
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&apos;");
}

function plistValue(value, indent) {
  const pad = " ".repeat(indent);
  if (typeof value === "boolean") {
    return `${pad}<${value ? "true" : "false"}/>`;
  }
  if (typeof value === "string") {
    return `${pad}<string>${escapeXml(value)}</string>`;
  }
  if (Array.isArray(value)) {
    if (value.length === 0) {
      return `${pad}<array/>`;
    }
    return [`${pad}<array>`, ...value.map((item) => plistValue(item, indent + 2)), `${pad}</array>`].join("\n");
  }
  if (value && typeof value === "object") {
    const lines = [`${pad}<dict>`];
    for (const [key, item] of Object.entries(value)) {
      lines.push(`${" ".repeat(indent + 2)}<key>${escapeXml(key)}</key>`);
      lines.push(plistValue(item, indent + 2));
    }
    lines.push(`${pad}</dict>`);
    return lines.join("\n");
  }
  throw new Error("Privacy manifest decisions contain an unsupported value type.");
}

function manifestFromDecisions(decisions) {
  return {
    NSPrivacyTracking: decisions.tracking,
    NSPrivacyTrackingDomains: decisions.trackingDomains,
    NSPrivacyAccessedAPITypes: decisions.accessedAPITypes.map((entry) => ({
      NSPrivacyAccessedAPIType: entry.apiType,
      NSPrivacyAccessedAPITypeReasons: entry.reasons,
    })),
    NSPrivacyCollectedDataTypes: decisions.collectedDataTypes.map((entry) => ({
      NSPrivacyCollectedDataType: entry.dataType,
      NSPrivacyCollectedDataTypeLinked: entry.linked,
      NSPrivacyCollectedDataTypeTracking: entry.tracking,
      NSPrivacyCollectedDataTypePurposes: entry.purposes,
    })),
  };
}

function plistDocument(manifest) {
  return [
    '<?xml version="1.0" encoding="UTF-8"?>',
    '<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">',
    '<plist version="1.0">',
    plistValue(manifest, 0),
    '</plist>',
    '',
  ].join("\n");
}

function targetBlockPattern(targetName) {
  return new RegExp(`(^  ${targetName}:\\n[\\s\\S]*?)(?=^  [A-Za-z0-9][A-Za-z0-9_]*:\\n|^schemes:|(?![\\s\\S]))`, "m");
}

function ensurePrivacySource(projectText, targetName) {
  const pattern = targetBlockPattern(targetName);
  const match = pattern.exec(projectText);
  if (!match) {
    throw new Error(`Could not find ${targetName} in apple/project.yml.`);
  }
  const block = match[1];
  if (/path:\s*Xcode\/PrivacyInfo\.xcprivacy/.test(block)) {
    return { text: projectText, changed: false };
  }
  const assetLine = "      - path: Xcode/Assets.xcassets\n";
  if (!block.includes(assetLine)) {
    throw new Error(`Could not find Xcode/Assets.xcassets source in ${targetName}.`);
  }
  const nextBlock = block.replace(assetLine, `${assetLine}      - path: Xcode/PrivacyInfo.xcprivacy\n`);
  return {
    text: projectText.slice(0, match.index) + nextBlock + projectText.slice(match.index + block.length),
    changed: true,
  };
}

function wireProject(appleDir, outputPath) {
  const expectedOutput = resolve(appleDir, manifestRelativePath);
  if (resolve(outputPath) !== expectedOutput) {
    throw new Error(`--wire-project requires --output to be ${expectedOutput}`);
  }
  const projectPath = resolve(appleDir, "project.yml");
  let projectText = readFileSync(projectPath, "utf8");
  let changed = false;
  for (const targetName of ["MeetingAssistAppleApp", "MeetingAssistMacApp"]) {
    const result = ensurePrivacySource(projectText, targetName);
    projectText = result.text;
    changed = changed || result.changed;
  }
  if (changed) {
    writeTextAtomic(projectPath, projectText);
  }
  return changed;
}

function runXcodeGen(appleDir) {
  const result = spawnSync("xcodegen", ["generate", "--spec", "project.yml"], {
    cwd: appleDir,
    encoding: "utf8",
    maxBuffer: 1024 * 1024 * 10,
  });
  if (result.status !== 0) {
    throw new Error(`xcodegen generate failed: ${`${result.stdout ?? ""}${result.stderr ?? ""}`.trim()}`);
  }
}

function generate(args) {
  if (!args.decisionsFile) {
    throw new Error("--decisions-file is required.");
  }
  const appleDir = resolve(process.cwd(), args.appleDir);
  const outputPath = args.output ? resolve(process.cwd(), args.output) : resolve(appleDir, manifestRelativePath);
  const decisionsPath = resolve(process.cwd(), args.decisionsFile);
  if (!existsSync(decisionsPath)) {
    throw new Error(`Missing privacy decisions file: ${decisionsPath}`);
  }
  const decisions = readJSON(decisionsPath);
  const problems = validateDecisions(decisions, args);
  if (problems.length > 0) {
    throw new Error(`Privacy decisions cannot generate a release manifest. Invalid: ${problems.slice(0, 10).join(", ")}`);
  }
  const manifest = manifestFromDecisions(decisions);
  writeTextAtomic(outputPath, plistDocument(manifest));

  let projectWired = false;
  if (args.wireProject) {
    projectWired = wireProject(appleDir, outputPath);
  }
  if (args.generateXcodeProject) {
    runXcodeGen(appleDir);
  }

  return {
    ok: true,
    decisionsFile: decisionsPath,
    manifestPath: outputPath,
    manifestRef: relative(rootDir, outputPath).split(/[/\\]/).join("/"),
    collectedDataTypes: manifest.NSPrivacyCollectedDataTypes.length,
    accessedAPITypes: manifest.NSPrivacyAccessedAPITypes.length,
    tracking: manifest.NSPrivacyTracking,
    projectWired,
    xcodeProjectGenerated: args.generateXcodeProject,
    nextSteps: [
      args.wireProject ? "Run native Apple release readiness." : "Run again with --wire-project before release readiness.",
      args.generateXcodeProject ? "Run app target tests." : "Run xcodegen generate --spec apple/project.yml after wiring.",
      "Run node scripts/native-apple-release-readiness.mjs --strict.",
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
    console.log(JSON.stringify(generate(args), null, 2));
  } catch (error) {
    console.error(JSON.stringify({ ok: false, error: error.message, usage: usage() }, null, 2));
    process.exitCode = 1;
  }
}

main();
