export type TimelineMessage = {
  createdAt?: string;
};

export const timelineMarkerGapMs = 60 * 60 * 1000;

const timeFormatter = new Intl.DateTimeFormat('en-US', {
  hour: 'numeric',
  minute: '2-digit',
});

const weekdayFormatter = new Intl.DateTimeFormat('en-US', {
  weekday: 'long',
});

const recentDateFormatter = new Intl.DateTimeFormat('en-US', {
  weekday: 'short',
  month: 'short',
  day: 'numeric',
});

const olderDateFormatter = new Intl.DateTimeFormat('en-US', {
  month: 'short',
  day: 'numeric',
  year: 'numeric',
});

function parseDate(value: string | undefined): Date | undefined {
  if (!value) return undefined;
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? undefined : date;
}

function localDayOrdinal(date: Date): number {
  return Date.UTC(date.getFullYear(), date.getMonth(), date.getDate()) / 86_400_000;
}

function timelineLabel(date: Date, now: Date): string {
  const daysAgo = localDayOrdinal(now) - localDayOrdinal(date);
  const time = timeFormatter.format(date);

  if (daysAgo === 0) return `Today ${time}`;
  if (daysAgo === 1) return `Yesterday ${time}`;
  if (daysAgo >= 2 && daysAgo <= 6) return `${weekdayFormatter.format(date)} ${time}`;
  if (date.getFullYear() === now.getFullYear()) {
    return `${recentDateFormatter.format(date)} at ${time}`;
  }
  return `${olderDateFormatter.format(date)} at ${time}`;
}

/**
 * Sparse, iMessage-style timeline markers without adding synthetic list rows.
 * A marker opens the conversation, a new local calendar day, or a meaningful
 * same-day return after an hour. Invalid timestamps stay unlabelled and do not
 * erase the last trustworthy point in the timeline.
 */
export function buildTimelineMarkers(
  messages: TimelineMessage[],
  now: Date = new Date(),
): Array<string | undefined> {
  const labels: Array<string | undefined> = new Array(messages.length).fill(undefined);
  let previous: Date | undefined;

  messages.forEach((message, index) => {
    const current = parseDate(message.createdAt);
    if (!current) return;

    const newDay = previous
      ? localDayOrdinal(current) !== localDayOrdinal(previous)
      : true;
    const meaningfulReturn = previous
      ? current.getTime() - previous.getTime() >= timelineMarkerGapMs
      : false;

    if (newDay || meaningfulReturn) labels[index] = timelineLabel(current, now);
    previous = current;
  });

  return labels;
}
