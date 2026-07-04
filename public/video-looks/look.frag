#version 300 es
// Bonfire video "looks" — one parameterized fragment shader (Spectacular OS, Wave 13).
// GPU-cheap enhancement the FAR END actually sees: the pipeline wraps the outbound
// camera track (MediaStreamTrackProcessor -> this shader -> MediaStreamTrackGenerator,
// or a canvas.captureStream fallback on Safari). No ML segmentation / blur — pure
// per-pixel curves + a 3x3 neighbourhood for sharpen/denoise. Every look is a
// uniform preset, so switching looks is a uniform swap, never a pipeline rebuild.
//
// uIntensity (0..1) scales the whole look toward identity: at 0 the output is the
// raw frame, so the thermal governor can ease a look off without a black tile.
precision highp float;

in vec2 vUv;
out vec4 fragColor;

uniform sampler2D uTex;
uniform vec2  uTexel;        // 1.0 / resolution — neighbour step for 3x3 taps

uniform float uIntensity;    // 0..1 master mix toward identity
uniform float uBypass;       // 1.0 => cheap single-tap passthrough (governor relief)

uniform float uBrightness;   // additive, default 0
uniform float uContrast;     // multiplier about 0.5, default 1
uniform float uSaturation;   // default 1
uniform float uTemperature;  // -1..1, + toward amber, default 0
uniform float uGamma;        // default 1 (lower = brighter mids)
uniform float uVignette;     // 0..1 corner darkening, default 0
uniform float uSoftClip;     // 0..1 highlight roll-off, default 0
uniform float uMono;         // 0/1 Rec.709 luma
uniform float uSCurve;       // 0..1 gentle contrast S-curve
uniform float uSharpen;      // unsharp amount, default 0
uniform float uBlackLift;    // 0..1 raise the black point, default 0
uniform float uGrain;        // 0..1 film grain, default 0
uniform float uDenoise;      // 0..1 3x3 bilateral-ish smooth, default 0
uniform float uLowLightGain; // 0..1 adaptive shadow gain, default 0
uniform float uTargetLuma;   // adaptive-gain target mean, default 0.5
uniform float uTime;         // seconds, drives grain

const vec3 LUMA = vec3(0.2126, 0.7152, 0.0722);

float luma(vec3 c) { return dot(c, LUMA); }

// Cheap hash for film grain — no texture, no branch.
float hash(vec2 p) {
  p = fract(p * vec2(123.34, 456.21));
  p += dot(p, p + 45.32);
  return fract(p.x * p.y);
}

void main() {
  vec3 base = texture(uTex, vUv).rgb;

  // Governor relief / identity: skip every neighbourhood tap and curve.
  if (uBypass > 0.5 || uIntensity <= 0.001) {
    fragColor = vec4(base, 1.0);
    return;
  }

  vec3 col = base;

  // --- 3x3 neighbourhood (denoise + unsharp) sampled once, reused ---
  vec3 n  = texture(uTex, vUv + vec2(0.0, -uTexel.y)).rgb;
  vec3 s  = texture(uTex, vUv + vec2(0.0,  uTexel.y)).rgb;
  vec3 e  = texture(uTex, vUv + vec2( uTexel.x, 0.0)).rgb;
  vec3 w  = texture(uTex, vUv + vec2(-uTexel.x, 0.0)).rgb;
  vec3 ne = texture(uTex, vUv + vec2( uTexel.x, -uTexel.y)).rgb;
  vec3 nw = texture(uTex, vUv + vec2(-uTexel.x, -uTexel.y)).rgb;
  vec3 se = texture(uTex, vUv + vec2( uTexel.x,  uTexel.y)).rgb;
  vec3 sw = texture(uTex, vUv + vec2(-uTexel.x,  uTexel.y)).rgb;

  // Bilateral-ish denoise: weight neighbours by how close they are to centre, so
  // edges survive while flat low-light noise averages out (cheap, no full kernel).
  if (uDenoise > 0.001) {
    float sigma = 0.14;
    vec3 acc = col;
    float wsum = 1.0;
    vec3 samples[8] = vec3[8](n, s, e, w, ne, nw, se, sw);
    for (int i = 0; i < 8; i++) {
      float d = distance(luma(samples[i]), luma(col));
      float wt = exp(-(d * d) / (2.0 * sigma * sigma));
      acc += samples[i] * wt;
      wsum += wt;
    }
    col = mix(col, acc / wsum, uDenoise);
  }

  // Low-light adaptive gain: lift dark pixels toward the target mean far more than
  // bright ones (a local tone map — no histogram readback needed).
  if (uLowLightGain > 0.001) {
    float l = max(luma(col), 0.001);
    float gain = clamp(uTargetLuma / l, 1.0, 3.2);
    gain = mix(1.0, gain, uLowLightGain);
    col *= gain;
  }

  // White balance (temperature): push toward amber / away, keep luma steady.
  if (abs(uTemperature) > 0.001) {
    vec3 warm = col + vec3(uTemperature * 0.10, uTemperature * 0.03, -uTemperature * 0.10);
    col = warm;
  }

  // Black-point lift (studio matte shadows).
  col = mix(col, uBlackLift + col * (1.0 - uBlackLift), step(0.001, uBlackLift));

  // Brightness + contrast about mid-grey.
  col = (col - 0.5) * uContrast + 0.5 + uBrightness;

  // Gamma (mids).
  col = pow(clamp(col, 0.0, 1.0), vec3(uGamma));

  // Gentle S-curve for contrast without crushing.
  if (uSCurve > 0.001) {
    vec3 sc = col * col * (3.0 - 2.0 * col); // smoothstep curve
    col = mix(col, sc, uSCurve);
  }

  // Saturation (after tone so it reads true).
  if (abs(uSaturation - 1.0) > 0.001) {
    float l = luma(col);
    col = mix(vec3(l), col, uSaturation);
  }

  // Mono (editorial black & white) — Rec.709 luma.
  if (uMono > 0.5) {
    col = vec3(luma(col));
  }

  // Highlight soft-clip roll-off — tames blown highlights.
  if (uSoftClip > 0.001) {
    vec3 rolled = 1.0 - exp(-col * 1.35);
    col = mix(col, rolled, uSoftClip);
  }

  // Unsharp mask: centre minus the blurred neighbourhood.
  if (uSharpen > 0.001) {
    vec3 blur = (n + s + e + w + ne + nw + se + sw) / 8.0;
    col += (col - blur) * uSharpen;
  }

  // Vignette — subtle corner darkening.
  if (uVignette > 0.001) {
    vec2 d = vUv - 0.5;
    float vig = smoothstep(0.85, 0.35, dot(d, d) * 2.0);
    col *= mix(1.0, vig, uVignette);
  }

  // Film grain — animated, luma-scaled so shadows get more (where it hides).
  if (uGrain > 0.001) {
    float g = hash(vUv / max(uTexel, vec2(1e-4)) + uTime) - 0.5;
    col += g * uGrain * 0.12;
  }

  col = clamp(col, 0.0, 1.0);

  // Master intensity: scale the entire look toward the untouched frame.
  fragColor = vec4(mix(base, col, clamp(uIntensity, 0.0, 1.0)), 1.0);
}
