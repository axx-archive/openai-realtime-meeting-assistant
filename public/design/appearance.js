/* Shared pre-paint appearance. Load after generated tokens, before page content.
 * Painting never persists or calls settings APIs; each host owns preference writes. */
(() => {
  "use strict";
  const key = "bonfire.theme.v1";
  const root = document.documentElement;
  const system = window.matchMedia?.("(prefers-color-scheme: dark)");
  const normalize = (value) => ["light", "dark", "system"].includes(value) ? value : "dark";
  function readPreference() {
    try { return normalize(window.localStorage.getItem(key)); }
    catch { return "dark"; }
  }
  let preference = readPreference();
  let paintFrame;
  function resolve(mode) {
    const choice = normalize(mode);
    return choice === "system" ? (system?.matches ? "dark" : "light") : choice;
  }
  function apply(mode) {
    preference = normalize(mode);
    const theme = resolve(preference);
    if (paintFrame) window.cancelAnimationFrame(paintFrame);
    root.setAttribute("data-stride-appearance-updating", "");
    root.dataset.theme = theme;
    root.style.colorScheme = theme;
    const canvas = getComputedStyle(root).getPropertyValue("--stride-color-canvas").trim();
    const meta = document.querySelector('meta[name="theme-color"]');
    if (meta && canvas) meta.setAttribute("content", canvas);
    // Settle descendants before restoring hover transitions: adaptive button
    // foregrounds must never pair with a background from the previous theme.
    void getComputedStyle(document.querySelector("button") || root).backgroundColor;
    paintFrame = window.requestAnimationFrame(() => {
      root.removeAttribute("data-stride-appearance-updating");
      paintFrame = undefined;
    });
    return theme;
  }
  function refresh() { return apply(readPreference()); }
  window.StrideAppearance = Object.freeze({ readPreference, resolve, apply, refresh });
  system?.addEventListener?.("change", () => {
    if (preference === "system") apply("system");
  });
  window.addEventListener("storage", (event) => {
    if (event.key === key) refresh();
  });
  apply(preference);
})();
