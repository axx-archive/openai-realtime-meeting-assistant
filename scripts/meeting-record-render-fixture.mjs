#!/usr/bin/env node

import { createServer } from 'node:http';
import { existsSync, readFileSync, statSync } from 'node:fs';
import { extname, resolve } from 'node:path';

const port = Number.parseInt(process.env.PORT || '3000', 10);
const repoRoot = resolve(import.meta.dirname, '..');
const indexHTML = readFileSync(resolve(repoRoot, 'index.html'), 'utf8');
const now = '2026-08-14T16:45:00Z';

const source = (id, revision, speaker, at, correctionState = 'current') => ({
  segmentId: id,
  revision,
  speaker,
  at,
  correctionState,
});

const transcript = [
  {
    id: 'segment-corrected-1',
    revision: 'segment-corrected-1-rev-2',
    speaker: 'AJ',
    at: '2026-08-14T16:05:00Z',
    text: 'The customer pilot begins Monday with the revised onboarding sequence.',
    source: 'room',
    captureSequence: 1,
    correctionState: 'corrected',
  },
  {
    id: 'segment-corrected-2',
    revision: 'segment-corrected-2-rev-1',
    speaker: 'Tyler',
    at: '2026-08-14T16:09:00Z',
    text: 'Tyler owns the launch checklist and will confirm the final participant list tomorrow.',
    source: 'room',
    captureSequence: 2,
    correctionState: 'current',
  },
  {
    id: 'segment-corrected-3',
    revision: 'segment-corrected-3-rev-1',
    speaker: 'Joel',
    at: '2026-08-14T16:14:00Z',
    text: 'The unresolved risk is whether the export is ready before the first pilot session.',
    source: 'room',
    captureSequence: 3,
    correctionState: 'current',
  },
  ...Array.from({ length: 15 }, (_, index) => ({
    id: `segment-long-${index + 4}`,
    revision: `segment-long-${index + 4}-rev-1`,
    speaker: index % 2 ? 'AJ' : 'Tyler',
    at: `2026-08-14T16:${String(index + 15).padStart(2, '0')}:00Z`,
    text: `Current source detail ${index + 1} stays available in the permanent record without expanding the executive summary.`,
    source: 'room',
    captureSequence: index + 4,
    correctionState: 'current',
  })),
];

const correctedRow = {
  contract: 'meeting-record-v1',
  id: 'meeting-corrected-current',
  roomId: 'office',
  title: 'Customer pilot readiness',
  outcomePreview: 'Pilot timing is set; launch ownership is clear and one export risk remains open.',
  recordRevision: 'meeting-corrected-current-rev-7',
  startedAt: '2026-08-14T16:00:00Z',
  endedAt: '2026-08-14T16:40:00Z',
  active: false,
  durationSeconds: 2400,
  participants: ['AJ', 'Tyler', 'Joel'],
  coverageState: 'partial_synthesis',
  decisionCount: 1,
  commitmentCount: 1,
  unresolvedCount: 1,
  transcriptCount: transcript.length,
};

const emptyRow = {
  contract: 'meeting-record-v1',
  id: 'meeting-empty-current',
  roomId: 'room-empty',
  title: 'Quiet planning room',
  outcomePreview: 'No authorized transcript was captured for this sitting.',
  recordRevision: 'meeting-empty-current-rev-1',
  startedAt: '2026-08-14T14:00:00Z',
  endedAt: '2026-08-14T14:12:00Z',
  active: false,
  durationSeconds: 720,
  participants: ['AJ'],
  coverageState: 'no_transcript',
  decisionCount: 0,
  commitmentCount: 0,
  unresolvedCount: 0,
  transcriptCount: 0,
};

const activeRow = {
  contract: 'meeting-record-v1',
  id: 'meeting-active-current',
  roomId: 'room-active',
  title: 'Live product review',
  outcomePreview: 'Analysis is catching up with the current transcript.',
  recordRevision: 'meeting-active-current-rev-3',
  startedAt: '2026-08-14T16:30:00Z',
  active: true,
  durationSeconds: 900,
  participants: ['AJ', 'Tyler'],
  coverageState: 'catching_up',
  decisionCount: 0,
  commitmentCount: 0,
  unresolvedCount: 1,
  transcriptCount: 3,
};

const correctedSource = source(
  'segment-corrected-1',
  'segment-corrected-1-rev-2',
  'AJ',
  '2026-08-14T16:05:00Z',
  'corrected',
);
const launchSource = source('segment-corrected-2', 'segment-corrected-2-rev-1', 'Tyler', '2026-08-14T16:09:00Z');
const riskSource = source('segment-corrected-3', 'segment-corrected-3-rev-1', 'Joel', '2026-08-14T16:14:00Z');

const correctedDetail = {
  ...correctedRow,
  executiveRecap: [{
    kind: 'topic',
    text: 'The customer pilot begins Monday with a revised onboarding sequence and a clearly owned launch checklist.',
    status: 'current',
    sources: [correctedSource, launchSource],
  }],
  needsToKnow: [{
    kind: 'topic',
    text: 'The transcript source was corrected; this record presents only the current revision.',
    status: 'current',
    sources: [correctedSource],
  }],
  decisions: [{
    kind: 'decision',
    text: 'Begin the customer pilot on Monday using the revised onboarding sequence.',
    status: 'confirmed',
    sources: [correctedSource],
  }],
  commitments: [{
    kind: 'commitment',
    text: 'Complete the launch checklist and confirm the participant list tomorrow.',
    owner: 'Tyler',
    ownerState: 'resolved',
    dueState: 'resolved',
    workState: 'unresolved',
    projectState: 'unresolved',
    status: 'open',
    sources: [launchSource],
  }],
  blockers: [{
    kind: 'unresolved_question',
    text: 'Will the export be ready before the first pilot session?',
    status: 'open',
    sources: [riskSource],
  }],
  people: ['AJ', 'Tyler', 'Joel'],
  work: [],
  projects: [],
  artifacts: [],
  coverage: {
    state: 'partial_synthesis',
    transcriptCount: transcript.length,
    transcriptThrough: '2026-08-14T16:29:00Z',
    analysisThrough: '2026-08-14T16:24:00Z',
    unavailableClaims: 0,
    gaps: ['Analysis is catching up with the final five minutes.'],
    listenOnly: false,
  },
  transcript: { segments: transcript, hasMore: false },
};

const emptyDetail = {
  ...emptyRow,
  executiveRecap: [],
  needsToKnow: [],
  decisions: [],
  commitments: [],
  blockers: [],
  people: ['AJ'],
  work: [],
  projects: [],
  artifacts: [],
  coverage: {
    state: 'no_transcript',
    transcriptCount: 0,
    unavailableClaims: 0,
    gaps: ['Transcript unavailable under current source authority.'],
    listenOnly: false,
  },
  transcript: { segments: [], hasMore: false },
};

const activeDetail = {
  ...activeRow,
  executiveRecap: [],
  needsToKnow: [],
  decisions: [],
  commitments: [],
  blockers: [{
    kind: 'unresolved_question',
    text: 'Analysis has not yet reached the newest current transcript.',
    status: 'catching_up',
    sources: [source('segment-active-3', 'segment-active-3-rev-1', 'AJ', '2026-08-14T16:43:00Z')],
  }],
  people: ['AJ', 'Tyler'],
  work: [],
  projects: [],
  artifacts: [],
  coverage: {
    state: 'catching_up',
    transcriptCount: 3,
    transcriptThrough: '2026-08-14T16:43:00Z',
    analysisThrough: '2026-08-14T16:38:00Z',
    unavailableClaims: 0,
    gaps: ['Analysis is catching up.'],
    listenOnly: false,
  },
  transcript: {
    segments: [
      { id: 'segment-active-1', revision: 'segment-active-1-rev-1', speaker: 'Tyler', at: '2026-08-14T16:34:00Z', text: 'We are reviewing the current product state.', source: 'room', captureSequence: 1, correctionState: 'current' },
      { id: 'segment-active-2', revision: 'segment-active-2-rev-1', speaker: 'AJ', at: '2026-08-14T16:38:00Z', text: 'The next step is to validate the exact customer path.', source: 'room', captureSequence: 2, correctionState: 'current' },
      { id: 'segment-active-3', revision: 'segment-active-3-rev-1', speaker: 'AJ', at: '2026-08-14T16:43:00Z', text: 'This newest source is still waiting for analysis.', source: 'room', captureSequence: 3, correctionState: 'current' },
    ],
    hasMore: false,
  },
};

const details = new Map([
  [correctedRow.id, correctedDetail],
  [emptyRow.id, emptyDetail],
  [activeRow.id, activeDetail],
]);

function json(response, status, body) {
  response.writeHead(status, {
    'access-control-allow-headers': '*',
    'access-control-allow-origin': '*',
    'cache-control': 'no-store',
    'content-type': 'application/json; charset=utf-8',
  });
  response.end(JSON.stringify(body));
}

const contentTypes = new Map([
  ['.css', 'text/css; charset=utf-8'],
  ['.gif', 'image/gif'],
  ['.ico', 'image/x-icon'],
  ['.jpeg', 'image/jpeg'],
  ['.jpg', 'image/jpeg'],
  ['.js', 'text/javascript; charset=utf-8'],
  ['.json', 'application/json; charset=utf-8'],
  ['.png', 'image/png'],
  ['.svg', 'image/svg+xml'],
  ['.webp', 'image/webp'],
  ['.woff', 'font/woff'],
  ['.woff2', 'font/woff2'],
]);

function serveCurrentAsset(url, response) {
  let decodedPath;
  try {
    decodedPath = decodeURIComponent(url.pathname);
  } catch {
    return false;
  }
  const candidate = resolve(repoRoot, `.${decodedPath}`);
  if (!candidate.startsWith(`${repoRoot}/`) || !existsSync(candidate) || !statSync(candidate).isFile()) {
    return false;
  }
  response.writeHead(200, {
    'cache-control': 'no-store',
    'content-type': contentTypes.get(extname(candidate).toLowerCase()) || 'application/octet-stream',
  });
  response.end(readFileSync(candidate));
  return true;
}

const server = createServer((request, response) => {
  const url = new URL(request.url || '/', `http://${request.headers.host || `127.0.0.1:${port}`}`);
  if (request.method === 'OPTIONS') {
    response.writeHead(204, {
      'access-control-allow-headers': '*',
      'access-control-allow-methods': 'GET,POST,OPTIONS',
      'access-control-allow-origin': '*',
    });
    response.end();
    return;
  }
  if (url.pathname === '/auth/me') {
    json(response, 200, { email: 'synthetic@example.test', name: 'AJ', shellAccess: 'full', themePref: 'system' });
    return;
  }
  if (url.pathname === '/auth/login' && request.method === 'POST') {
    json(response, 200, { email: 'synthetic@example.test', name: 'AJ', shellAccess: 'full', themePref: 'system', sessionToken: 'synthetic-meeting-record-session' });
    return;
  }
  if (url.pathname === '/assistant/meetings') {
    json(response, 200, { ok: true, contract: 'meeting-record-v1', meetings: [activeRow, correctedRow, emptyRow], hasMore: false, serverNow: now });
    return;
  }
  const conversationMatch = url.pathname.match(/^\/assistant\/meetings\/([^/]+)\/conversation$/u);
  if (conversationMatch) {
    const meetingId = decodeURIComponent(conversationMatch[1]);
    json(response, 200, { ok: true, thread: { id: `thread-${meetingId}`, title: `Meeting · ${details.get(meetingId)?.title || 'Record'}` } });
    return;
  }
  const detailMatch = url.pathname.match(/^\/assistant\/meetings\/([^/]+)$/u);
  if (detailMatch) {
    const detail = details.get(decodeURIComponent(detailMatch[1]));
    if (!detail) {
      json(response, 404, { ok: false, error: 'Meeting Record unavailable' });
      return;
    }
    json(response, 200, { ok: true, meeting: detail, serverNow: now });
    return;
  }
  if (url.pathname === '/assistant/home') {
    json(response, 200, { ok: true, home: { version: 'home-v2', generatedAt: now, items: [], starters: [], allClear: true } });
    return;
  }
  if (url.pathname === '/rooms') {
    json(response, 200, { ok: true, rooms: [] });
    return;
  }
  if (url.pathname === '/client-config') {
    json(response, 200, { ok: true, nativeRealtimeVoiceEnabled: false });
    return;
  }
  if (url.pathname.startsWith('/assistant/') || url.pathname.startsWith('/api/') || url.pathname.startsWith('/notifications')) {
    json(response, 200, { ok: true, notifications: [], threads: [], files: [] });
    return;
  }
  if (serveCurrentAsset(url, response)) {
    return;
  }
  response.writeHead(200, { 'cache-control': 'no-store', 'content-type': 'text/html; charset=utf-8' });
  response.end(indexHTML);
});

server.listen(port, '0.0.0.0', () => {
  process.stdout.write(`Meeting Record render fixture listening on http://127.0.0.1:${port}\n`);
});

for (const signal of ['SIGINT', 'SIGTERM']) {
  process.on(signal, () => server.close(() => process.exit(0)));
}
