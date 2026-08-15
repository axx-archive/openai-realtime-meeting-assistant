import type {
  PrivateRiffBinding,
  PrivateRiffParagraph,
  ScoutMessage,
} from '../api/types';

export function privateRiffSourceTitle(riff: PrivateRiffBinding): string {
  const title = String(riff.sourceTitle ?? '').trim().replace(/^#+/, '');
  return title ? `#${title}` : 'source channel';
}

export function privateRiffCheckpointSummary(riff: PrivateRiffBinding): string {
  const source = privateRiffSourceTitle(riff);
  const at = new Date(String(riff.throughCreatedAt ?? riff.capturedAt ?? ''));
  const through = Number.isNaN(at.getTime())
    ? ''
    : ` through ${at.toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' })}`;
  return `Private · grounded in ${source}${through}`;
}

export function privateRiffHasUpdates(riff: PrivateRiffBinding): boolean {
  return riff.sourceAvailable && Number(riff.newMessageCount ?? 0) > 0;
}

export function privateRiffAnswerShareable(
  riff: PrivateRiffBinding | null | undefined,
  message: ScoutMessage | null | undefined,
): boolean {
  if (!riff || !riff.sourceAvailable || !message?.id) return false;
  const role = String(message.role ?? '').toLowerCase();
  const text = String(message.text ?? message.content ?? '').trim();
  const activity = message.activity;
  return (role === 'assistant' || role === 'scout')
    && Boolean(text)
    && activity?.version === 'stride-private-riff/v1'
    && activity?.status === 'completed'
    && Number(activity.contextRevision ?? 0) > 0
    && Boolean(String(activity.sourceThreadId ?? '').trim())
    && Boolean(String(activity.throughMessageId ?? '').trim())
    && message.reply?.state !== 'queued'
    && message.reply?.state !== 'running';
}

export function initialPrivateRiffParagraphTokens(
  paragraphs: readonly PrivateRiffParagraph[],
): Set<string> {
  return new Set(
    paragraphs
      .filter((paragraph) => paragraph.token.trim() && paragraph.text.trim())
      .map((paragraph) => paragraph.token),
  );
}

export function selectedPrivateRiffParagraphTokens(
  paragraphs: readonly PrivateRiffParagraph[],
  selected: ReadonlySet<string>,
): string[] {
  return paragraphs
    .filter((paragraph) => selected.has(paragraph.token))
    .map((paragraph) => paragraph.token);
}
