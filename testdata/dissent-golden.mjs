#!/usr/bin/env node
// dissent-golden.mjs — regenerates testdata/dissent_canonical_golden.json by
// running the REAL Dissent TypeScript (src/crypto.ts) over the fixture inputs.
// Committed so the only cross-language evidence for dissent_canonical.go can
// be re-derived and audited: without it nobody can tell whether the fixtures
// came from the TypeScript or from the Go port itself.
//
//   DISSENT_SRC=/path/to/dissent node testdata/dissent-golden.mjs
//   DISSENT_SRC=/path/to/dissent node testdata/dissent-golden.mjs --check
//
// --check regenerates in memory and exits non-zero if the committed file
// differs, which is what a CI drift gate should run.
//
// CONTRACT: the vector INPUTS in the golden file are hand-authored data and
// are preserved verbatim; every other field (canonical, canonicalHex, sha256,
// hmac) is derived here. To add a vector, add `{ "name": ..., "input": ... }`
// and re-run. sourceSha256 records the digest of the TypeScript the vectors
// were derived from, so a drifted source is detectable.
//
// Three inputs cannot be written as plain JSON and use a tag the loader on
// both sides expands (dissent_canonical_test.go does the same):
//   {"__number": "-0"}          negative zero — JSON.stringify(-0) is "0", so
//                               a literal -0 round-trips to +0 and the vector
//                               would silently assert nothing.
//   {"__stringHex": "61eda080"} a string holding a lone surrogate, as WTF-8
//                               bytes — Go's encoding/json turns a \ud800
//                               escape into U+FFFD, JSON.parse does not.
//   {"__keysHex": {"eda080":1}} an object whose KEYS are those strings. The
//                               value tags cannot express one, so before this
//                               existed no vector carried a non-ASCII object
//                               key and the key SORT — the half of
//                               canonicalJson that decides byte order, and
//                               therefore every planId — had no cross-language
//                               evidence at all.

import { createHash } from "node:crypto";
import { readFileSync, writeFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const SRC = process.env.DISSENT_SRC ?? "/Users/ajhart/Documents/business";
const CRYPTO_TS = join(SRC, "src", "crypto.ts");
const { canonicalJson, sha256, hmacSha256 } = await import(CRYPTO_TS);

const here = dirname(fileURLToPath(import.meta.url));
const goldenPath = join(here, "dissent_canonical_golden.json");
const golden = JSON.parse(readFileSync(goldenPath, "utf8"));

// WTF-8 -> UTF-16, so {"__stringHex": ...} can carry a lone surrogate.
function decodeWtf8(hex) {
  const bytes = Buffer.from(hex, "hex");
  let out = "";
  for (let i = 0; i < bytes.length; ) {
    const b = bytes[i];
    if (b === 0xed && i + 2 < bytes.length && bytes[i + 1] >= 0xa0 && bytes[i + 1] <= 0xbf) {
      out += String.fromCharCode(((b & 0x0f) << 12) | ((bytes[i + 1] & 0x3f) << 6) | (bytes[i + 2] & 0x3f));
      i += 3;
      continue;
    }
    const size = b < 0x80 ? 1 : b < 0xe0 ? 2 : b < 0xf0 ? 3 : 4;
    out += bytes.slice(i, i + size).toString("utf8");
    i += size;
  }
  return out;
}

function expand(value) {
  if (value === null || typeof value !== "object") return value;
  if (Array.isArray(value)) return value.map(expand);
  const keys = Object.keys(value);
  if (keys.length === 1 && keys[0] === "__number") return Number(value.__number);
  if (keys.length === 1 && keys[0] === "__stringHex") return decodeWtf8(value.__stringHex);
  if (keys.length === 1 && keys[0] === "__keysHex") {
    const inner = value.__keysHex;
    return Object.fromEntries(Object.keys(inner).map((hex) => [decodeWtf8(hex), expand(inner[hex])]));
  }
  return Object.fromEntries(keys.map((key) => [key, expand(value[key])]));
}

const secret = golden.secret;
const vectors = golden.vectors.map(({ name, input }) => {
  const canonical = canonicalJson(expand(input));
  return {
    name,
    input,
    canonical,
    canonicalHex: Buffer.from(canonical, "utf8").toString("hex"),
    sha256: sha256(canonical),
    hmac: hmacSha256(secret, canonical)
  };
});

let undefinedThrows = false;
try {
  canonicalJson({ a: undefined });
} catch {
  undefinedThrows = true;
}

const next = {
  secret,
  generatedBy: `testdata/dissent-golden.mjs over ${"src/crypto.ts"}`,
  sourceSha256: { "src/crypto.ts": createHash("sha256").update(readFileSync(CRYPTO_TS)).digest("hex") },
  undefinedThrows,
  vectors
};
const text = `${JSON.stringify(next, null, 2)}\n`;

if (process.argv.includes("--check")) {
  const current = readFileSync(goldenPath, "utf8");
  if (current !== text) {
    console.error("dissent_canonical_golden.json is stale — re-run testdata/dissent-golden.mjs");
    process.exit(1);
  }
  console.log(`ok: ${vectors.length} vectors replay from ${CRYPTO_TS}`);
} else {
  writeFileSync(goldenPath, text);
  console.log(`wrote ${vectors.length} vectors from ${CRYPTO_TS}`);
}
