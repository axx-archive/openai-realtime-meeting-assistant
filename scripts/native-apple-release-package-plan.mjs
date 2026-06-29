#!/usr/bin/env node
import { existsSync, mkdirSync, readFileSync, renameSync, writeFileSync } from "node:fs";
import { dirname, join, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const rootDir = resolve(dirname(fileURLToPath(import.meta.url)), "..");

function usage() {
  return [
    "Usage:",
    "  node scripts/native-apple-release-package-plan.mjs [--apple-dir apple]",
    "    [--artifacts-dir artifacts/native-apple] [--proofpack-dir path]",
    "    [--run-id id] [--created-at iso] [--configuration Release]",
    "    [--output-dir path] [--write] [--force]",
    "",
    "Creates a non-secret native Apple archive/export/notarization command plan.",
    "The plan writes export option plists and shell-safe command strings, but it",
    "does not archive, upload to TestFlight, notarize, staple, or access Apple",
    "credentials. Run it on the Apple-account machine before the real release run.",
  ].join("\n");
}

function parseArgs(argv) {
  const args = {
    appleDir: "apple",
    artifactsDir: "artifacts/native-apple",
    proofpackDir: "",
    runId: "",
    createdAt: "",
    configuration: "Release",
    outputDir: "",
    write: false,
    force: false,
    help: false,
  };

  for (let index = 0; index < argv.length; index += 1) {
    const arg = argv[index];
    if (arg === "--apple-dir") {
      args.appleDir = requiredValue(argv, index, arg);
      index += 1;
    } else if (arg === "--artifacts-dir") {
      args.artifactsDir = requiredValue(argv, index, arg);
      index += 1;
    } else if (arg === "--proofpack-dir") {
      args.proofpackDir = requiredValue(argv, index, arg);
      index += 1;
    } else if (arg === "--run-id") {
      args.runId = requiredValue(argv, index, arg);
      index += 1;
    } else if (arg === "--created-at") {
      args.createdAt = requiredValue(argv, index, arg);
      index += 1;
    } else if (arg === "--configuration") {
      args.configuration = requiredValue(argv, index, arg);
      index += 1;
    } else if (arg === "--output-dir") {
      args.outputDir = requiredValue(argv, index, arg);
      index += 1;
    } else if (arg === "--write") {
      args.write = true;
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

function readJSON(path) {
  return JSON.parse(readFileSync(path, "utf8"));
}

function writeTextAtomic(path, value, { force = false } = {}) {
  if (existsSync(path) && !force) {
    throw new Error(`Refusing to overwrite existing release package plan file: ${path}. Use --force to replace it.`);
  }
  mkdirSync(dirname(path), { recursive: true });
  const tempPath = `${path}.tmp-${process.pid}-${Date.now()}`;
  writeFileSync(tempPath, value);
  renameSync(tempPath, path);
}

function writeJSONAtomic(path, value, options = {}) {
  writeTextAtomic(path, `${JSON.stringify(value, null, 2)}\n`, options);
}

function cleanBuildValue(value) {
  return String(value ?? "").trim().replace(/^["']|["']$/g, "").replace(/;$/, "").trim();
}

function bundleIdentifierForTarget(projectText, targetName) {
  const start = projectText.indexOf(`  ${targetName}:`);
  if (start === -1) {
    return "";
  }
  const targetBlock = projectText.slice(start + targetName.length + 4);
  const nextTarget = targetBlock.search(/\n  [A-Za-z0-9_]+:/);
  const block = nextTarget >= 0 ? targetBlock.slice(0, nextTarget) : targetBlock;
  return cleanBuildValue(/PRODUCT_BUNDLE_IDENTIFIER:\s*([^\n#]+)/.exec(block)?.[1] ?? "");
}

function readProjectMetadata(appleDir) {
  const projectYml = join(appleDir, "project.yml");
  if (!existsSync(projectYml)) {
    throw new Error(`Missing Apple XcodeGen project spec: ${projectYml}`);
  }
  const projectText = readText(projectYml);
  const marketing = /MARKETING_VERSION:\s*([^\n#]+)/.exec(projectText)?.[1];
  const build = /CURRENT_PROJECT_VERSION:\s*([^\n#]+)/.exec(projectText)?.[1];
  if (!marketing || !build) {
    throw new Error(`Could not read MARKETING_VERSION and CURRENT_PROJECT_VERSION from ${projectYml}`);
  }
  return {
    version: cleanBuildValue(marketing),
    build: cleanBuildValue(build),
    bundleIdentifiers: {
      ios: bundleIdentifierForTarget(projectText, "MeetingAssistAppleApp"),
      macos: bundleIdentifierForTarget(projectText, "MeetingAssistMacApp"),
    },
  };
}

function isoForId(value) {
  return value.replaceAll(":", "").replaceAll("-", "").replace(/\.\d+Z$/, "Z");
}

function defaultRunId(createdAt) {
  return `native-apple-package-${isoForId(createdAt).replace(/[TZ]/g, "-").replace(/-$/, "")}`;
}

function nonSecretIdentifier(value, label) {
  const trimmed = String(value ?? "").trim();
  if (!trimmed) {
    throw new Error(`${label} is required.`);
  }
  if (!/^[A-Za-z0-9._-]{3,96}$/.test(trimmed)) {
    throw new Error(`${label} must use only letters, numbers, dot, underscore, or dash.`);
  }
  if (
    /\bsk-[A-Za-z0-9_-]{20,}\b/.test(trimmed) ||
    /\bxox[baprs]-[A-Za-z0-9-]{20,}\b/.test(trimmed) ||
    /\b[A-Z0-9]{10}\b/.test(trimmed) ||
    /\.(p8|p12|mobileprovision|provisionprofile)$/i.test(trimmed)
  ) {
    throw new Error(`${label} looks like a secret or credential file and must not be used.`);
  }
  return trimmed;
}

function configurationName(value) {
  const trimmed = String(value ?? "").trim();
  if (!/^[A-Za-z0-9._ -]{3,40}$/.test(trimmed)) {
    throw new Error("--configuration must be a normal Xcode configuration name.");
  }
  return trimmed;
}

function repoRelative(path) {
  const relativePath = relative(rootDir, path);
  if (relativePath.startsWith("..")) {
    return path;
  }
  return relativePath.split(/[/\\]/).join("/");
}

function artifactRef(path) {
  const relativePath = relative(rootDir, path);
  if (relativePath.startsWith("..")) {
    throw new Error(`Release package artifacts must stay under the repository: ${path}`);
  }
  return relativePath.split(/[/\\]/).join("/");
}

function shellQuote(value) {
  const text = String(value);
  if (/^\$[A-Z_][A-Z0-9_]*$/.test(text)) {
    return `"${text}"`;
  }
  if (/^[A-Za-z0-9_@%+=:,./-]+$/.test(text)) {
    return text;
  }
  return `'${text.replaceAll("'", "'\\''")}'`;
}

function commandSpec(argv, note) {
  return {
    argv,
    shell: argv.map(shellQuote).join(" "),
    note,
  };
}

function exportOptionsPlist(entries) {
  const body = Object.entries(entries)
    .map(([key, value]) => plistEntry(key, value))
    .join("");
  return [
    '<?xml version="1.0" encoding="UTF-8"?>',
    '<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">',
    '<plist version="1.0">',
    '<dict>',
    body.trimEnd(),
    '</dict>',
    '</plist>',
    '',
  ].join("\n");
}

function plistEntry(key, value) {
  const escapedKey = escapeXml(key);
  if (typeof value === "boolean") {
    return `  <key>${escapedKey}</key>\n  <${value ? "true" : "false"}/>\n`;
  }
  return `  <key>${escapedKey}</key>\n  <string>${escapeXml(value)}</string>\n`;
}

function escapeXml(value) {
  return String(value)
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&apos;");
}

function loadProofpack(args, metadata) {
  if (!args.proofpackDir) {
    return null;
  }
  const proofpackDir = resolve(rootDir, args.proofpackDir);
  const proofpackPath = join(proofpackDir, "proofpack.json");
  const draftPath = join(proofpackDir, "ReleaseEvidence.draft.json");
  if (!existsSync(proofpackPath)) {
    throw new Error(`Missing proof-pack manifest: ${proofpackPath}`);
  }
  if (!existsSync(draftPath)) {
    throw new Error(`Missing proof-pack draft: ${draftPath}`);
  }
  const proofpack = readJSON(proofpackPath);
  const draft = readJSON(draftPath);
  const problems = [];
  for (const key of ["version", "build"]) {
    if (String(proofpack[key] ?? "") !== String(metadata[key] ?? "")) {
      problems.push(`proofpack.${key}`);
    }
    if (String(draft[key] ?? "") !== String(metadata[key] ?? "")) {
      problems.push(`draft.${key}`);
    }
  }
  if (proofpack.runId && draft.runId && String(proofpack.runId) !== String(draft.runId)) {
    problems.push("runId.match");
  }
  if (proofpack.roomId && draft.roomId && String(proofpack.roomId) !== String(draft.roomId)) {
    problems.push("roomId.match");
  }
  if (problems.length > 0) {
    throw new Error(`Proof pack does not match current Apple project metadata: ${problems.join(", ")}`);
  }
  return { proofpackDir, proofpackPath, draftPath, proofpack, draft };
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
        /\b[A-Za-z0-9_-]{20,}\.[A-Za-z0-9_-]{20,}\.[A-Za-z0-9_-]{20,}\b/.test(trimmed) ||
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
      /(secret|password|passwd|token|credential|api_?key|apikey|private_?key|provision|certificate|cert|p8|p12|issuer_?id|key_?id|authorization|jwt|apple_?id|username|headers?|cookies?)/i.test(
        key
      )
    ) {
      problems.push(`${path}.${key}`);
    }
    problems.push(...collectUnsafeContent(item, `${path}.${key}`));
  }
  return problems;
}

function buildPlan(args) {
  const appleDir = resolve(rootDir, args.appleDir);
  const createdAt = args.createdAt || new Date().toISOString();
  if (!Number.isFinite(Date.parse(createdAt))) {
    throw new Error("--created-at must be an ISO-like timestamp.");
  }
  const configuration = configurationName(args.configuration);
  const metadata = readProjectMetadata(appleDir);
  const proofpack = loadProofpack(args, metadata);
  const runId = nonSecretIdentifier(
    args.runId || proofpack?.proofpack?.runId || defaultRunId(createdAt),
    "runId"
  );
  const outputDir = resolve(
    rootDir,
    args.outputDir || (proofpack ? join(proofpack.proofpackDir, "operator") : join(args.artifactsDir, runId, "operator"))
  );
  const archivesDir = join(outputDir, "archives");
  const exportsDir = join(outputDir, "exports");
  const iosArchivePath = join(archivesDir, "MeetingAssist-iOS.xcarchive");
  const macArchivePath = join(archivesDir, "MeetingAssist-macOS.xcarchive");
  const iosExportPath = join(exportsDir, "testflight");
  const macExportPath = join(exportsDir, "macos");
  const testflightOptionsPath = join(outputDir, "ExportOptions.testflight.plist");
  const developerIdOptionsPath = join(outputDir, "ExportOptions.developer-id.plist");
  const notarizedExportPath = join(exportsDir, "macos-notarized");
  const macZipPath = join(exportsDir, "macos", "MeetingAssistMacApp.zip");
  const macAppPath = join(exportsDir, "macos", "MeetingAssist.app");
  const projectPath = join(appleDir, "MeetingAssist.xcodeproj");
  const relativeProjectPath = repoRelative(projectPath);

  const exportOptions = {
    testflight: {
      destination: "upload",
      method: "app-store-connect",
      manageAppVersionAndBuildNumber: false,
      stripSwiftSymbols: true,
      uploadSymbols: true,
      testFlightInternalTestingOnly: false,
    },
    developerId: {
      destination: "export",
      method: "developer-id",
      signingStyle: "automatic",
      stripSwiftSymbols: true,
    },
  };

  const observationInputs = proofpack
    ? {
        testFlightTemplate: proofpack.proofpack.observationTemplates?.testFlight ?? null,
        testFlightInput: artifactRef(join(proofpack.proofpackDir, "inbox", "testflight-observation.json")),
        notarizationTemplate: proofpack.proofpack.observationTemplates?.notarization ?? null,
        notarizationInput: artifactRef(join(proofpack.proofpackDir, "inbox", "notarization-observation.json")),
      }
    : null;

  const commands = {
    preflightSigning: commandSpec(
      ["node", "scripts/native-apple-configure-signing.mjs", "--apple-dir", repoRelative(appleDir), "--validate-only"],
      "Validate the ignored local Apple Team ID configuration before archive/export."
    ),
    defaultReadiness: commandSpec(
      ["node", "scripts/native-apple-release-readiness.mjs", "--apple-dir", repoRelative(appleDir)],
      "Repo-owned Apple release prerequisites must pass before archive/export."
    ),
    iosArchive: commandSpec(
      [
        "xcodebuild",
        "-project",
        relativeProjectPath,
        "-scheme",
        "MeetingAssistAppleApp",
        "-configuration",
        configuration,
        "-destination",
        "generic/platform=iOS",
        "-archivePath",
        artifactRef(iosArchivePath),
        "archive",
      ],
      "Creates the universal iPhone/iPadOS archive. Add -allowProvisioningUpdates only on the Apple-account machine when Xcode account state is intentional."
    ),
    testflightUpload: commandSpec(
      [
        "xcodebuild",
        "-exportArchive",
        "-archivePath",
        artifactRef(iosArchivePath),
        "-exportPath",
        artifactRef(iosExportPath),
        "-exportOptionsPlist",
        artifactRef(testflightOptionsPath),
      ],
      "Uploads the iOS archive to App Store Connect/TestFlight using Xcode's app-store-connect export destination."
    ),
    macArchive: commandSpec(
      [
        "xcodebuild",
        "-project",
        relativeProjectPath,
        "-scheme",
        "MeetingAssistMacApp",
        "-configuration",
        configuration,
        "-destination",
        "generic/platform=macOS",
        "-archivePath",
        artifactRef(macArchivePath),
        "archive",
      ],
      "Creates the native macOS archive with hardened runtime and macOS media entitlements from the project."
    ),
    macDeveloperIdExport: commandSpec(
      [
        "xcodebuild",
        "-exportArchive",
        "-archivePath",
        artifactRef(macArchivePath),
        "-exportPath",
        artifactRef(macExportPath),
        "-exportOptionsPlist",
        artifactRef(developerIdOptionsPath),
      ],
      "Exports the macOS archive for Developer ID distribution."
    ),
    macZipForNotary: commandSpec(
      ["ditto", "-c", "-k", "--keepParent", artifactRef(macAppPath), artifactRef(macZipPath)],
      "Creates a zip suitable for notarytool submission after Developer ID export."
    ),
    macSubmitNotary: commandSpec(
      ["xcrun", "notarytool", "submit", artifactRef(macZipPath), "--keychain-profile", "$NOTARYTOOL_KEYCHAIN_PROFILE", "--wait"],
      "Submits the zipped macOS app for notarization using a preconfigured local keychain profile. The profile name is not stored in this plan."
    ),
    macStaple: commandSpec(
      ["xcrun", "stapler", "staple", artifactRef(macAppPath)],
      "Staples the accepted notarization ticket to the exported app."
    ),
    macGatekeeper: commandSpec(
      ["spctl", "-a", "-vv", "--type", "execute", artifactRef(macAppPath)],
      "Verifies Gatekeeper acceptance after stapling."
    ),
    macExportNotarizedArchive: commandSpec(
      ["xcodebuild", "-exportNotarizedApp", "-archivePath", artifactRef(macArchivePath), "-exportPath", artifactRef(notarizedExportPath)],
      "Alternative Xcode export path after the archive has been notarized by Apple."
    ),
  };
  if (proofpack) {
    commands.promoteTestFlightObservation = commandSpec(
      [
        "node",
        "scripts/native-apple-promote-distribution-evidence.mjs",
        "--proofpack-dir",
        artifactRef(proofpack.proofpackDir),
        "--kind",
        "testflight",
        "--input",
        observationInputs.testFlightInput,
        "--confirm-app-store-connect-upload",
        "--confirm-no-secrets",
        "--confirm-current-build",
      ],
      "Promotes the sanitized TestFlight observation into ReleaseEvidence.draft.json after the upload is visible in App Store Connect."
    );
    commands.promoteMacNotarizationObservation = commandSpec(
      [
        "node",
        "scripts/native-apple-promote-distribution-evidence.mjs",
        "--proofpack-dir",
        artifactRef(proofpack.proofpackDir),
        "--kind",
        "notarization",
        "--input",
        observationInputs.notarizationInput,
        "--confirm-developer-id-archive",
        "--confirm-notary-accepted",
        "--confirm-stapled-app",
        "--confirm-gatekeeper-accepted",
        "--confirm-current-build",
      ],
      "Promotes the sanitized macOS notarization observation after notary acceptance, stapling, and Gatekeeper verification."
    );
  }

  const blockers = [];
  const warnings = [];
  if (!existsSync(projectPath)) {
    blockers.push({ id: "xcode_project", detail: `Missing generated Xcode project at ${repoRelative(projectPath)}.` });
  }
  if (!metadata.bundleIdentifiers.ios) {
    blockers.push({ id: "ios_bundle_identifier", detail: "Missing MeetingAssistAppleApp PRODUCT_BUNDLE_IDENTIFIER." });
  }
  if (!metadata.bundleIdentifiers.macos) {
    blockers.push({ id: "macos_bundle_identifier", detail: "Missing MeetingAssistMacApp PRODUCT_BUNDLE_IDENTIFIER." });
  }
  if (!existsSync(join(appleDir, "Config", "Signing.xcconfig"))) {
    blockers.push({ id: "signing_xcconfig", detail: "Missing apple/Config/Signing.xcconfig." });
  }
  if (!existsSync(join(appleDir, "Xcode", "PrivacyInfo.xcprivacy"))) {
    warnings.push({
      id: "privacy_manifest",
      detail: "Strict readiness still requires apple/Xcode/PrivacyInfo.xcprivacy before external TestFlight or notarized release.",
    });
  }
  if (!proofpack) {
    warnings.push({
      id: "proofpack",
      detail: "No --proofpack-dir was provided; create one before the real external run so TestFlight/notarization observations can be promoted.",
    });
  }

  const plan = {
    schemaVersion: 1,
    artifactType: "native_apple_release_package_plan",
    status: blockers.length === 0 ? "ready_for_operator" : "blocked",
    createdAt,
    runId,
    configuration,
    version: metadata.version,
    build: metadata.build,
    appleDir: repoRelative(appleDir),
    proofpack: proofpack
      ? {
          proofpackDir: artifactRef(proofpack.proofpackDir),
          proofpackPath: artifactRef(proofpack.proofpackPath),
          evidenceDraft: artifactRef(proofpack.draftPath),
        }
      : null,
    outputDir: artifactRef(outputDir),
    bundleIdentifiers: metadata.bundleIdentifiers,
    exportOptions: {
      testflight: artifactRef(testflightOptionsPath),
      developerId: artifactRef(developerIdOptionsPath),
    },
    observationInputs,
    archives: {
      ios: artifactRef(iosArchivePath),
      macos: artifactRef(macArchivePath),
    },
    exports: {
      testflight: artifactRef(iosExportPath),
      macos: artifactRef(macExportPath),
      macosNotarized: artifactRef(notarizedExportPath),
    },
    commands,
    operatorEnvironment: {
      appleDevelopmentTeam: "Configure through apple/Config/Signing.local.xcconfig or APPLE_DEVELOPMENT_TEAM; this plan intentionally does not print the Team ID.",
      notarytoolKeychainProfile: "Set NOTARYTOOL_KEYCHAIN_PROFILE in the local shell before running the notarytool command.",
    },
    blockers,
    warnings,
    nextSteps: [
      "Run preflightSigning and defaultReadiness.",
      "Archive and upload MeetingAssistAppleApp for TestFlight only on the Apple-account machine.",
      "Archive and export MeetingAssistMacApp with Developer ID signing.",
      "Submit, staple, and Gatekeeper-verify the macOS app.",
      "Fill proof-pack inbox TestFlight and macOS notarization observations from the real operator run.",
      "Promote sanitized TestFlight and macOS notarization observations into the proof pack.",
      "Run node scripts/native-apple-release-readiness.mjs --strict.",
    ],
  };

  const unsafe = collectUnsafeContent(plan);
  if (unsafe.length > 0) {
    throw new Error(`Release package plan contains secret-shaped fields and will not be written: ${unsafe.slice(0, 6).join(", ")}`);
  }

  return {
    ok: blockers.length === 0,
    plan,
    files: {
      plan: join(outputDir, "release-command-plan.json"),
      readme: join(outputDir, "release-commands.md"),
      testflightOptions: testflightOptionsPath,
      developerIdOptions: developerIdOptionsPath,
    },
    exportOptions,
  };
}

function packageReadme(plan) {
  const lines = [
    "# Native Apple Release Package Plan",
    "",
    `Run ID: ${plan.runId}`,
    `Version/build: ${plan.version} (${plan.build})`,
    "",
    "This directory is a non-secret command plan for the Apple-account machine. It",
    "does not contain certificates, profiles, App Store Connect keys, notarytool",
    "credentials, raw upload logs, or Team IDs.",
    "",
    "Run commands in this order:",
    "",
  ];
  for (const [name, command] of Object.entries(plan.commands)) {
    lines.push(`## ${name}`, "", command.note, "", "```bash", command.shell, "```", "");
  }
  lines.push(
    "After TestFlight upload and macOS notarization, promote sanitized observations",
    "from the proof-pack inbox and run strict readiness before claiming release",
    "readiness.",
    ""
  );
  return `${lines.join("\n")}\n`;
}

function writePlan(result, args) {
  writeJSONAtomic(result.files.plan, result.plan, { force: args.force });
  writeTextAtomic(result.files.readme, packageReadme(result.plan), { force: args.force });
  writeTextAtomic(result.files.testflightOptions, exportOptionsPlist(result.exportOptions.testflight), { force: args.force });
  writeTextAtomic(result.files.developerIdOptions, exportOptionsPlist(result.exportOptions.developerId), { force: args.force });
}

function main() {
  try {
    const args = parseArgs(process.argv.slice(2));
    if (args.help) {
      console.log(usage());
      return;
    }
    const result = buildPlan(args);
    if (args.write) {
      writePlan(result, args);
    }
    console.log(
      JSON.stringify(
        {
          ok: result.ok,
          status: result.plan.status,
          runId: result.plan.runId,
          outputDir: result.plan.outputDir,
          planPath: args.write ? artifactRef(result.files.plan) : undefined,
          exportOptions: args.write ? result.plan.exportOptions : undefined,
          blockers: result.plan.blockers,
          warnings: result.plan.warnings,
          commands: result.plan.commands,
        },
        null,
        2
      )
    );
    if (!result.ok) {
      process.exitCode = 1;
    }
  } catch (error) {
    console.error(JSON.stringify({ ok: false, error: error.message, usage: usage() }, null, 2));
    process.exitCode = 1;
  }
}

main();
