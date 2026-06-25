#!/usr/bin/env node
import { readFile } from "node:fs/promises";

function usage() {
  return [
    "Usage:",
    "  node scripts/native-ice-readiness.mjs --file client-config.json [--require-turn]",
    "  node scripts/native-ice-readiness.mjs --stdin [--require-turn]",
    "  node scripts/native-ice-readiness.mjs --json '{\"rtcConfiguration\":{\"iceServers\":[]}}' [--require-turn]",
    "",
    "Use --file or --stdin for real credential-bearing configs. Reserve --json for synthetic fixtures.",
  ].join("\n");
}

function parseArgs(argv) {
  const args = {
    file: null,
    json: null,
    stdin: false,
    requireTurn: false,
    help: false,
  };

  for (let index = 0; index < argv.length; index += 1) {
    const arg = argv[index];
    if (arg === "--file") {
      args.file = argv[index + 1] ?? null;
      index += 1;
    } else if (arg === "--json") {
      args.json = argv[index + 1] ?? null;
      index += 1;
    } else if (arg === "--stdin") {
      args.stdin = true;
    } else if (arg === "--require-turn") {
      args.requireTurn = true;
    } else if (arg === "--help" || arg === "-h") {
      args.help = true;
    } else {
      throw new Error(`Unknown argument: ${arg}`);
    }
  }

  const sources = [args.file, args.json, args.stdin].filter(Boolean);
  if (!args.help && sources.length !== 1) {
    throw new Error("Provide exactly one of --file, --json, or --stdin.");
  }

  return args;
}

async function readStdin() {
  let input = "";
  process.stdin.setEncoding("utf8");
  for await (const chunk of process.stdin) {
    input += chunk;
  }
  return input;
}

async function readInput(args) {
  if (args.file) {
    return readFile(args.file, "utf8");
  }
  if (args.json) {
    return args.json;
  }
  if (args.stdin) {
    return readStdin();
  }
  throw new Error("No input source provided.");
}

function rtcConfigurationFrom(config) {
  if (!config || typeof config !== "object" || Array.isArray(config)) {
    return {};
  }
  if (
    config.rtcConfiguration &&
    typeof config.rtcConfiguration === "object" &&
    !Array.isArray(config.rtcConfiguration)
  ) {
    return config.rtcConfiguration;
  }
  return config;
}

function normalizedUrls(value) {
  const values = Array.isArray(value) ? value : [value];
  return values
    .filter((url) => typeof url === "string")
    .map((url) => url.trim())
    .filter(Boolean);
}

function relayTransport(url, kind) {
  const match = /[?&]transport=([^&#]+)/i.exec(url);
  if (match) {
    return decodeURIComponent(match[1]).trim().toLowerCase() || "unspecified";
  }
  return kind === "turns" ? "tls" : "unspecified";
}

function classifyUrl(url) {
  const normalized = url.trim().toLowerCase();
  if (normalized.startsWith("turns:")) {
    return { kind: "turns", relayTransport: relayTransport(normalized, "turns") };
  }
  if (normalized.startsWith("turn:")) {
    return { kind: "turn", relayTransport: relayTransport(normalized, "turn") };
  }
  if (normalized.startsWith("stuns:")) {
    return { kind: "stuns", relayTransport: null };
  }
  if (normalized.startsWith("stun:")) {
    return { kind: "stun", relayTransport: null };
  }
  return { kind: "unknown", relayTransport: null };
}

function analyze(config, options) {
  const rtcConfiguration = rtcConfigurationFrom(config);
  const iceServers = rtcConfiguration.iceServers;
  const warnings = [];
  const errors = [];
  const transports = new Set();
  let malformedServerCount = 0;
  let iceServerCount = 0;
  let knownUrlCount = 0;
  let unknownUrlCount = 0;
  let stunCount = 0;
  let stunsCount = 0;
  let turnCount = 0;
  let turnsCount = 0;
  let turnServersWithCredentials = 0;
  let turnServersMissingCredentials = 0;

  if (!Array.isArray(iceServers)) {
    return {
      ok: false,
      hasIceServers: false,
      iceServerCount: 0,
      knownUrlCount: 0,
      unknownUrlCount: 0,
      stunCount: 0,
      stunsCount: 0,
      turnCount: 0,
      turnsCount: 0,
      turnServersWithCredentials: 0,
      turnServersMissingCredentials: 0,
      relayTransports: [],
      warnings,
      errors: ["rtcConfiguration.iceServers must be an array."],
    };
  }

  for (const server of iceServers) {
    if (!server || typeof server !== "object" || Array.isArray(server)) {
      malformedServerCount += 1;
      continue;
    }

    const urls = normalizedUrls(server.urls);
    if (urls.length === 0) {
      malformedServerCount += 1;
      continue;
    }

    iceServerCount += 1;
    let hasTurnRelay = false;
    for (const url of urls) {
      const classification = classifyUrl(url);
      if (classification.kind === "stun") {
        stunCount += 1;
        knownUrlCount += 1;
      } else if (classification.kind === "stuns") {
        stunsCount += 1;
        knownUrlCount += 1;
      } else if (classification.kind === "turn") {
        turnCount += 1;
        knownUrlCount += 1;
        hasTurnRelay = true;
      } else if (classification.kind === "turns") {
        turnsCount += 1;
        knownUrlCount += 1;
        hasTurnRelay = true;
      } else {
        unknownUrlCount += 1;
      }

      if (classification.relayTransport) {
        transports.add(classification.relayTransport);
      }
    }

    if (hasTurnRelay) {
      const hasUsername = typeof server.username === "string" && server.username.trim() !== "";
      const hasCredential = typeof server.credential === "string" && server.credential.trim() !== "";
      if (hasUsername && hasCredential) {
        turnServersWithCredentials += 1;
      } else {
        turnServersMissingCredentials += 1;
      }
    }
  }

  if (malformedServerCount > 0) {
    warnings.push(`${malformedServerCount} ICE server entries were ignored because they were malformed or blank.`);
  }
  if (unknownUrlCount > 0) {
    warnings.push(`${unknownUrlCount} ICE server URLs used an unknown scheme.`);
  }
  if (iceServerCount === 0) {
    errors.push("No usable ICE servers were found.");
  } else if (knownUrlCount === 0) {
    errors.push("No STUN, STUNS, TURN, or TURNS ICE server URLs were found.");
  }
  if (turnServersMissingCredentials > 0) {
    warnings.push(`${turnServersMissingCredentials} TURN server entries are missing username or credential.`);
  }
  if (options.requireTurn && turnCount + turnsCount === 0) {
    errors.push("No TURN or TURNS relay URLs were found.");
  }
  if (options.requireTurn && turnCount + turnsCount > 0 && turnServersWithCredentials === 0) {
    errors.push("TURN relay URLs were found, but none have both username and credential.");
  }

  return {
    ok: errors.length === 0,
    hasIceServers: iceServerCount > 0,
    iceServerCount,
    knownUrlCount,
    unknownUrlCount,
    stunCount,
    stunsCount,
    turnCount,
    turnsCount,
    turnServersWithCredentials,
    turnServersMissingCredentials,
    relayTransports: [...transports].sort(),
    warnings,
    errors,
  };
}

try {
  const args = parseArgs(process.argv.slice(2));
  if (args.help) {
    console.log(usage());
    process.exit(0);
  }

  const input = await readInput(args);
  const config = JSON.parse(input);
  const result = analyze(config, { requireTurn: args.requireTurn });
  console.log(JSON.stringify(result, null, 2));
  process.exit(result.ok ? 0 : 1);
} catch (error) {
  console.error(JSON.stringify({ ok: false, error: error.message, usage: usage() }, null, 2));
  process.exit(1);
}
