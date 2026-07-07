// Bonfire background-blur segmenter (card 079) — a lazy wrapper over the vendored
// MediaPipe Tasks Vision selfie segmenter. It is imported ONLY when the "blur" look is
// selected, and the heavy ~9 MB wasm behind vision_bundle.mjs is fetched only when
// createBlurSegmenter() actually runs — so users who never pick blur pay nothing. This
// mirrors the rnnoise precedent (public/voice-focus/) of keeping a heavy, single-purpose
// dependency vendored and out of the 34.9k-line monolith.
//
// It returns a person-confidence mask (Uint8, 0..255; ~1 = subject, ~0 = background) for
// each frame the caller feeds it. The mask is read back to the CPU (getAsUint8Array)
// rather than shared as a GL texture, because MediaPipe runs in its OWN WebGL context —
// a cross-context texture cannot be sampled by the look pipeline's shader. At 256x256 the
// readback is ~64 KB.

const BUNDLE_URL = '/public/video-blur/vision_bundle.mjs'
const WASM_ROOT = '/public/video-blur'
const MODEL_URL = '/public/video-blur/selfie_segmenter.tflite'

// selfie_segmenter's native input is 256x256. Feed a pre-squashed square: the mask comes
// back normalized over the same field, so the look shader re-samples it by vUv and it
// re-aligns with the full frame — the horizontal/vertical squash cancels out on read.
const SEG_SIZE = 256

export async function createBlurSegmenter() {
  const { FilesetResolver, ImageSegmenter } = await import(BUNDLE_URL)
  const fileset = await FilesetResolver.forVisionTasks(WASM_ROOT)

  const build = delegate => ImageSegmenter.createFromOptions(fileset, {
    baseOptions: { modelAssetPath: MODEL_URL, delegate },
    runningMode: 'VIDEO',
    outputConfidenceMasks: true,
    outputCategoryMask: false
  })

  let segmenter
  try {
    segmenter = await build('GPU')
  } catch (_) {
    // Some machines / drivers refuse the GPU delegate — fall back to the wasm-SIMD CPU
    // delegate rather than losing blur entirely.
    segmenter = await build('CPU')
  }

  const scratch = new OffscreenCanvas(SEG_SIZE, SEG_SIZE)
  const ctx = scratch.getContext('2d', { alpha: false, desynchronized: true })
  // MediaPipe VIDEO mode requires strictly increasing timestamps (ms).
  let ts = 0

  return {
    // Segment one VideoFrame. Returns { data: Uint8Array(w*h), width, height } — person
    // confidence 0..255 — or null when no mask was produced. May throw on a transient
    // graph error; the caller reuses the previous mask (never a black tile).
    segment(frame) {
      if (!ctx) {
        return null
      }
      // 0.10.x's ImageSource union does not accept a raw VideoFrame, so draw it onto a
      // scratch canvas (drawImage does take a VideoFrame) at the model's square first.
      ctx.drawImage(frame, 0, 0, SEG_SIZE, SEG_SIZE)
      ts = Math.max(ts + 1, Math.round(performance.now()))
      const result = segmenter.segmentForVideo(scratch, ts)
      const mask = result && result.confidenceMasks && result.confidenceMasks[0]
      if (!mask) {
        result?.close?.()
        return null
      }
      const width = mask.width
      const height = mask.height
      // Copy out of MediaPipe-owned memory before close() reclaims it.
      const data = new Uint8Array(mask.getAsUint8Array())
      result.close()
      return { data, width, height }
    },
    close() {
      try { segmenter?.close?.() } catch (_) {}
      segmenter = null
    }
  }
}
