import type { LiveLineResult } from './liveLine';

/**
 * The canvas live line's DISPLAY contract — the wiring between what
 * `resolveLiveLine` decides and what actually reaches the screen.
 *
 * This exists because the arbitration ladder being correct does not mean the
 * canvas renders it correctly. `resolveLiveLine` is thoroughly tested; the JSX
 * that puts its output on screen was not testable at all, since this app runs
 * plain node:test with no React renderer. Extracting the presentational
 * decisions makes the part that was previously unverifiable — "does a
 * teammate's name and message actually reach the screen, and what does a
 * screen reader hear?" — a plain function with plain assertions.
 *
 * The component keeps the styling and the animation. Everything about WHAT
 * text appears lives here.
 */

export type LiveLineDisplay = {
  /** Rendered in text1/500 ahead of the body. Null when there is no preview. */
  authorSpan: string | null;
  /** The message, or the count when previews are off. */
  bodySpan: string;
  /** What VoiceOver announces — one phrase, not two disconnected fragments. */
  accessibilityLabel: string;
  accessibilityHint: string;
  /** True when the line should render at all. */
  visible: boolean;
};

/** Separator between author and message. A middot, matching the design sketch. */
const SEPARATOR = ' · ';

export function liveLineDisplay(live: LiveLineResult): LiveLineDisplay {
  if (!live.text) {
    return {
      authorSpan: null,
      bodySpan: '',
      accessibilityLabel: '',
      accessibilityHint: '',
      visible: false,
    };
  }

  const author = live.author?.trim() || null;

  return {
    authorSpan: author ? `${author}${SEPARATOR}` : null,
    bodySpan: live.text,
    // "Dana: pushed the pricing memo" reads as one sentence. Announcing the
    // author and the body as separate elements would make a screen reader
    // recite a name, pause, then recite a message with no stated relationship
    // between them.
    accessibilityLabel: author ? `${author}: ${live.text}` : live.text,
    // The hint has to match where the tap actually goes, or VoiceOver promises
    // one destination and the app delivers another.
    accessibilityHint: live.threadId ? 'Opens the thread.' : 'Opens threads.',
    visible: true,
  };
}
