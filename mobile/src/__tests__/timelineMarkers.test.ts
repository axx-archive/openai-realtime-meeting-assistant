import test from 'node:test';
import assert from 'node:assert/strict';
import { buildTimelineMarkers, timelineMarkerGapMs } from '../messaging/timelineMarkers';

const localISO = (year: number, month: number, day: number, hour: number, minute: number) =>
  new Date(year, month - 1, day, hour, minute).toISOString();

const message = (createdAt: string) => ({ createdAt });

test('opens a current-day conversation with a time and stays sparse during active chat', () => {
  const now = new Date(2026, 7, 1, 17, 0);
  const labels = buildTimelineMarkers([
    message(localISO(2026, 8, 1, 16, 0)),
    message(localISO(2026, 8, 1, 16, 12)),
    message(localISO(2026, 8, 1, 16, 59)),
  ], now);

  assert.deepEqual(labels, ['Today 4:00 PM', undefined, undefined]);
});

test('adds a time when people return after an hour on the same day', () => {
  const now = new Date(2026, 7, 1, 17, 0);
  const labels = buildTimelineMarkers([
    message(localISO(2026, 8, 1, 10, 0)),
    message(localISO(2026, 8, 1, 10, 59)),
    message(localISO(2026, 8, 1, 11, 59)),
  ], now);

  assert.equal(timelineMarkerGapMs, 3_600_000);
  assert.deepEqual(labels, ['Today 10:00 AM', undefined, 'Today 11:59 AM']);
});

test('uses conversational labels for yesterday and the last week', () => {
  const now = new Date(2026, 7, 1, 17, 0);
  const labels = buildTimelineMarkers([
    message(localISO(2026, 7, 30, 12, 59)),
    message(localISO(2026, 7, 31, 21, 27)),
  ], now);

  assert.deepEqual(labels, ['Thursday 12:59 PM', 'Yesterday 9:27 PM']);
});

test('uses day and month for older messages and adds a year when needed', () => {
  const now = new Date(2026, 7, 1, 17, 0);
  const labels = buildTimelineMarkers([
    message(localISO(2025, 12, 31, 23, 5)),
    message(localISO(2026, 7, 15, 21, 27)),
  ], now);

  assert.deepEqual(labels, ['Dec 31, 2025 at 11:05 PM', 'Wed, Jul 15 at 9:27 PM']);
});

test('invalid timestamps stay silent without breaking the next valid comparison', () => {
  const now = new Date(2026, 7, 1, 17, 0);
  const labels = buildTimelineMarkers([
    message(localISO(2026, 8, 1, 10, 0)),
    message('not-a-date'),
    message(localISO(2026, 8, 1, 10, 20)),
  ], now);

  assert.deepEqual(labels, ['Today 10:00 AM', undefined, undefined]);
});
