// Re-derive (or verify) the vendored MediaPipe background-blur assets under
// public/video-blur/ (card 079). These are large, version-pinned binaries committed to
// the repo so the app has no third-party runtime dependency; this script is the record
// of exactly which upstream bytes they are, and lets the ops gate re-derive or verify
// them deterministically.
//
//   node scripts/vendor-video-blur.mjs          # download the pinned assets (writes files)
//   node scripts/vendor-video-blur.mjs --check   # verify existing files' sha256 (no writes)
//
// If a pin changes, update PIN / MODEL_URL and the SHA256 map below, then re-run without
// --check and commit the new bytes + this file together.

import { createHash } from 'node:crypto'
import { mkdir, readFile, writeFile } from 'node:fs/promises'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

const PIN = '0.10.14' // @mediapipe/tasks-vision
const CDN = `https://cdn.jsdelivr.net/npm/@mediapipe/tasks-vision@${PIN}`
const MODEL_URL =
  'https://storage.googleapis.com/mediapipe-models/image_segmenter/selfie_segmenter/float16/latest/selfie_segmenter.tflite'

const OUT_DIR = join(dirname(fileURLToPath(import.meta.url)), '..', 'public', 'video-blur')

// file -> { url, sha256 }. sha256 pins the exact bytes we shipped (see COPYING notice).
const ASSETS = {
  'vision_bundle.mjs': {
    url: `${CDN}/vision_bundle.mjs`,
    sha256: 'e77f281f9619150d937023c355bae170e9120e3b9e43f1e23a2a7bee07197669'
  },
  'vision_wasm_internal.js': {
    url: `${CDN}/wasm/vision_wasm_internal.js`,
    sha256: '9440cf0cc0cea21800e31581ec32aeedcc5fbf9df4509796bbc7d3f99e52ab9c'
  },
  'vision_wasm_internal.wasm': {
    url: `${CDN}/wasm/vision_wasm_internal.wasm`,
    sha256: 'f82a8e6c05e08a44cc9f9e7ec5f845935bcbb1b1500ebe8c2f4812fb4e2917dc'
  },
  'selfie_segmenter.tflite': {
    url: MODEL_URL,
    sha256: '191ac9529ae506ee0beefa6b2c945a172dab9d07d1e802a290a4e4038226658b'
  }
}

const sha = buf => createHash('sha256').update(buf).digest('hex')
const check = process.argv.includes('--check')

let failures = 0
await mkdir(OUT_DIR, { recursive: true })

for (const [name, { url, sha256 }] of Object.entries(ASSETS)) {
  const dest = join(OUT_DIR, name)
  if (check) {
    try {
      const got = sha(await readFile(dest))
      if (got === sha256) {
        console.log(`  OK    ${name}`)
      } else {
        failures++
        console.log(`  BAD   ${name}\n        expected ${sha256}\n        actual   ${got}`)
      }
    } catch (err) {
      failures++
      console.log(`  MISS  ${name} (${err?.message || err})`)
    }
    continue
  }

  process.stdout.write(`  fetch ${name} ... `)
  const res = await fetch(url)
  if (!res.ok) {
    failures++
    console.log(`HTTP ${res.status}`)
    continue
  }
  const buf = Buffer.from(await res.arrayBuffer())
  const got = sha(buf)
  if (got !== sha256) {
    failures++
    console.log(`SHA MISMATCH\n        expected ${sha256}\n        actual   ${got}`)
    continue
  }
  await writeFile(dest, buf)
  console.log(`${buf.length} bytes, sha256 ok`)
}

if (failures) {
  console.error(`\n${failures} asset(s) failed. See above.`)
  process.exit(1)
}
console.log(check ? '\nAll vendored blur assets verified.' : '\nAll vendored blur assets written + verified.')
