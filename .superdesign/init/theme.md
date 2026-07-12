# Theme

The deployed theme is the Glass & Ink system defined inline in `index.html`. It is light-first; the old warm-dark, parchment, Instrument Serif description is retired.

## Core tokens

```css
:root {
  --font-sans: "Google Sans Flex", -apple-system, "SF Pro Text", "Segoe UI", sans-serif;
  --font-mono: "Geist Mono", "SF Mono", "Menlo", monospace;
  --font-serif: var(--font-sans);

  --paper-0: #FFFFFF;
  --paper-50: #F5F5F7;
  --paper-100: #EDEDF0;
  --ink-950: #09090B;
  --ink-900: #101013;
  --text-1: #0E0E10;
  --text-2: rgba(14, 14, 16, 0.60);
  --text-3: rgba(14, 14, 16, 0.38);
  --bg-app: var(--paper-50);
  --bg-stage: #000000;

  --signal-500: #30D158;
  --ember-500: #FF6B4A;
  --red-500: #FF453A;
  --amber-500: #FF9F0A;
  --blue-500: #0A84FF;

  --glass-chrome: rgba(255, 255, 255, 0.62);
  --glass-panel: rgba(255, 255, 255, 0.44);
  --glass-blur: saturate(1.8) blur(28px);
  --glass-border: rgba(14, 14, 16, 0.10);
  --glass-highlight: inset 0 1px 0 rgba(255, 255, 255, 0.70);
  --glass-shadow: 0 8px 32px rgba(14, 14, 16, 0.10);
}
```

Use the complete token block in `index.html` for exact spacing, radii, type, shadows, motion, dark-mode overrides, and component states.

## Color doctrine

- Green is live, speaking, passed, or shipped state only.
- Ember is agent work/ignition only.
- Red is destructive state only.
- Video remains true black in light and dark themes.
- Neutral glass and ink—not warm brown—define the shell.
