import type {
  PrivateRiffBinding,
  ScoutMessage,
} from '../api/types';

export function privateRiffSourceTitle(riff: PrivateRiffBinding): string {
  const title = String(riff.sourceTitle ?? '').trim().replace(/^#+/, '');
  return title ? `#${title}` : 'source channel';
}

const privateRiffPacificFormatter = new Intl.DateTimeFormat('en-US', {
  timeZone: 'America/Los_Angeles',
  month: 'short',
  day: 'numeric',
  year: 'numeric',
  hour: 'numeric',
  minute: '2-digit',
  timeZoneName: 'short',
});

export function privateRiffPacificDateTime(value: string | undefined): string {
  if (!value) return 'Not available';
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? 'Not available' : privateRiffPacificFormatter.format(date);
}

export function privateRiffCheckpointSummary(riff: PrivateRiffBinding): string {
  const source = privateRiffSourceTitle(riff);
  const checkpoint = privateRiffPacificDateTime(riff.throughCreatedAt ?? riff.capturedAt);
  const through = checkpoint === 'Not available' ? '' : ` through ${checkpoint}`;
  return `Private Riff · ${source}${through}`;
}

export function privateRiffFreshnessSummary(riff: PrivateRiffBinding): string {
  if (!riff.sourceAvailable) return riff.unavailableReason || 'Source access is unavailable';
  if (riff.viewedEpisodeId && riff.viewedEpisodeId !== riff.activeEpisodeId) return 'Earlier pass is source-reauthorized and read-only';
  if (riff.autoFresh) return 'Channel context stays current automatically';
  return 'Context is fixed to this episode checkpoint';
}

export function privateRiffDisplayedPassNumber(riff: PrivateRiffBinding): number {
  const displayedID = String(riff.viewedEpisodeId ?? riff.activeEpisodeId ?? '').trim();
  const episodeIndex = riff.episodes?.findIndex((episode) => episode.id === displayedID) ?? -1;
  return episodeIndex >= 0 ? episodeIndex + 1 : Math.max(1, Number(riff.episodeCount ?? 1));
}

/**
 * A canonical Riff Space can contain many invocations. The server stamps every
 * new turn with the active episode; legacy one-off Riffs remain readable as a
 * single episode without guessing boundaries from message text or timestamps.
 */
export function privateRiffCurrentEpisodeMessages(
  riff: PrivateRiffBinding | null | undefined,
  messages: readonly ScoutMessage[],
): ScoutMessage[] {
  const activeEpisodeID = String(riff?.viewedEpisodeId ?? riff?.activeEpisodeId ?? '').trim();
  if (!riff || !activeEpisodeID) return [...messages];
  const current = messages.filter((message) => (
    String(message.riffEpisodeId ?? message.activity?.episodeId ?? '').trim() === activeEpisodeID
  ));
  return current;
}

export function privateRiffHasUpdates(riff: PrivateRiffBinding): boolean {
  return riff.sourceAvailable && Number(riff.newMessageCount ?? 0) > 0;
}

export function privateRiffReplyShareable(
  riff: PrivateRiffBinding | null | undefined,
  message: ScoutMessage | null | undefined,
  messages: readonly ScoutMessage[],
): boolean {
  if (!riff || !riff.sourceAvailable || !message?.id) return false;
  const role = String(message.role ?? '').toLowerCase();
  const text = String(message.text ?? message.content ?? '').trim();
  if (!['user', 'assistant', 'scout'].includes(role) || !text) return false;
  if (String(message.via ?? '') === 'private_riff_publication_control') return false;
  if (message.thread || message.work || message.proposal || message.choices || message.manifest
    || message.image || message.imageGeneration || message.publication || (message.files?.length ?? 0) > 0) return false;
  if (message.reply?.state === 'queued' || message.reply?.state === 'running') return false;
  if (role !== 'user' && (message.activity?.version !== 'stride-private-riff/v1'
    || message.activity?.status !== 'completed'
    || Number(message.activity?.contextRevision ?? 0) <= 0
    || !String(message.activity?.throughMessageId ?? '').trim())) return false;
  const initiatingMessage = messages.find((candidate) => {
    const candidateRole = String(candidate.role ?? '').toLowerCase();
    return candidateRole === 'user' && Boolean(String(candidate.text ?? candidate.content ?? '').trim());
  });
  return Boolean(initiatingMessage?.id)
    && String(initiatingMessage?.id) !== String(message.id);
}

export function privateRiffReplyAuthor(message: ScoutMessage | null | undefined): string {
  const role = String(message?.role ?? '').toLowerCase();
  return String(message?.authorName ?? '').trim()
    || (role === 'assistant' || role === 'scout' ? 'Scout' : 'You');
}

export function privateRiffShareAllCount(
  messages: readonly ScoutMessage[],
  riff?: PrivateRiffBinding | null,
): number {
  return privateRiffCurrentEpisodeMessages(riff, messages).filter((message) => {
    const role = String(message.role ?? '').toLowerCase();
    const text = String(message.text ?? message.content ?? '').trim();
    return ['user', 'assistant', 'scout'].includes(role)
      && Boolean(message.id)
      && Boolean(text)
      && String(message.via ?? '') !== 'private_riff_publication_control'
      && !message.thread
      && !message.work
      && !message.proposal
      && !message.choices
      && !message.manifest
      && !message.image
      && !message.imageGeneration
      && !message.publication
      && (message.files?.length ?? 0) === 0
      && message.reply?.state !== 'queued'
      && message.reply?.state !== 'running'
      && (role === 'user' || (message.activity?.version === 'stride-private-riff/v1'
        && message.activity?.status === 'completed'
        && Number(message.activity?.contextRevision ?? 0) > 0
        && Boolean(String(message.activity?.throughMessageId ?? '').trim())));
  }).length;
}
