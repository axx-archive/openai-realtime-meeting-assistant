# STRIDE design source

Edit `stride.tokens.json`, then run `node scripts/generate-stride-design-tokens.mjs` from the repository root. The generator checks text, selected/control, action and status contrast before producing the web stylesheet, native data and the versioned website export. `--check` rejects stale generated files; the Go release suite runs that check.

The source uses logical dimensions. Web role line heights are ratios and tracking is in em; native role line heights and tracking are logical points because React Native expects those values. Native font names reference the already bundled files. Colors with an alpha suffix are allowed only for overlays; ordinary text and control contrast checks require opaque colors. Disabled explanatory text stays readable.

The separate website checks in the exact `exports/stride-tokens.v1.css` as `app/generated/stride-tokens.css`. Each generated file contains the source SHA. Its release must record that SHA; changing app tokens does not deploy the website automatically.

`public/design/appearance.js` paints the existing light/dark/system preference and defaults to dark. It never saves preferences or calls account APIs. Host applications retain responsibility for those writes. Palette changes are atomic so a transitioning button does not briefly pair an old fill with a new foreground.

The generated palette is not evidence that every component has migrated. `public/design/legacy-tokens.css` is a compatibility boundary for inherited web surfaces. It preserves the current call canvas until foregrounds migrate with it, and scopes replacement of old accent names to reviewed navigation/Home components. Native call sheets and authored content have their own remaining migration work. See the screen inventory in `docs/plans/stride-3-design-system.md`.
