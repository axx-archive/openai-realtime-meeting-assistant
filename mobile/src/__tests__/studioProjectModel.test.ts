import assert from 'node:assert/strict';
import test from 'node:test';

import type { StudioProject } from '../api/types';
import {
  studioProjectListRows,
  studioProjectOpenTarget,
  studioProjectRelativeTime,
  studioProjectResultIsFinal,
  studioProjectSection,
} from '../work/studioProjectModel';

const exactDigest = 'a'.repeat(64);

function project(
  id: string,
  patch: Partial<StudioProject> = {},
): StudioProject {
  return {
    schemaVersion: 1,
    id,
    kind: 'presentation',
    title: `Project ${id}`,
    revision: 1,
    status: 'running',
    progressPercent: 42,
    phase: 'build',
    phases: [
      { id: 'brief', label: 'Brief', status: 'complete' },
      { id: 'build', label: 'Build', status: 'active' },
      { id: 'polish', label: 'Polish', status: 'upcoming' },
      { id: 'ready', label: 'Ready', status: 'upcoming' },
    ],
    createdAt: '2026-08-23T12:00:00Z',
    updatedAt: '2026-08-23T12:00:00Z',
    rootRunId: `run-${id}`,
    rootArtifactId: `root-${id}`,
    href: `/api/studio-projects/v1?id=${id}`,
    canRename: true,
    ...patch,
  };
}

test('Studio projects group into calm decision, active, and recent sections', () => {
  const projects = [
    project('ready', { status: 'ready', progressPercent: 100 }),
    project('input', { status: 'needs_input' }),
    project('running'),
    project('attention', { status: 'needs_attention' }),
    project('stopped', { status: 'stopped' }),
    project('queued', { status: 'queued' }),
  ];

  assert.equal(studioProjectSection(projects[1]), 'needs-you');
  assert.equal(studioProjectSection(projects[2]), 'in-progress');
  assert.equal(studioProjectSection(projects[0]), 'recent');
  assert.deepEqual(
    studioProjectListRows(projects, 'all').map((row) => row.id),
    [
      'section:needs-you',
      'project:input',
      'project:attention',
      'section:in-progress',
      'project:running',
      'project:queued',
      'section:recent',
      'project:ready',
      'project:stopped',
    ],
  );
});

test('Studio filters presentation and research without creating new navigation destinations', () => {
  const projects = [
    project('deck'),
    project('report', { kind: 'document', title: 'Market report' }),
  ];
  assert.deepEqual(
    studioProjectListRows(projects, 'presentation').filter((row) => row.type === 'project').map((row) => row.project.id),
    ['deck'],
  );
  assert.deepEqual(
    studioProjectListRows(projects, 'document').filter((row) => row.type === 'project').map((row) => row.project.id),
    ['report'],
  );
});

test('presentation results open only from an exact presentable artifact tuple', () => {
  const deck = project('deck', {
    result: {
      artifactId: 'deck-result',
      type: 'html_deck',
      version: 7,
      digest: exactDigest,
      title: 'Field Guide',
      canEdit: true,
      canContinue: true,
      canPresent: true,
      canExport: true,
    },
  });
  assert.deepEqual(studioProjectOpenTarget(deck), {
    kind: 'deck',
    artifactId: 'deck-result',
    artifactVersion: 7,
    artifactDigest: exactDigest,
    title: 'Field Guide',
    desktopEditable: true,
    canPresent: true,
  });
  assert.deepEqual(studioProjectOpenTarget({ ...deck, result: { ...deck.result!, canPresent: false } }), {
    kind: 'deck',
    artifactId: 'deck-result',
    artifactVersion: 7,
    artifactDigest: exactDigest,
    title: 'Field Guide',
    desktopEditable: true,
    canPresent: false,
  });
  assert.equal(studioProjectOpenTarget({ ...deck, result: { ...deck.result!, digest: '{"raw":"json"}' } }), null);
  assert.equal(studioProjectOpenTarget({ ...deck, result: { ...deck.result!, version: 0 } }), null);
  assert.equal(studioProjectOpenTarget({ ...deck, result: { ...deck.result!, type: 'markdown' } }), null);
});

test('research results route only from an exact document artifact tuple', () => {
  const report = project('report', {
    kind: 'document',
    result: {
      artifactId: 'report-result',
      type: 'markdown',
      version: 3,
      digest: exactDigest.toUpperCase(),
      title: 'Market opportunity',
      canEdit: false,
      canContinue: true,
      canPresent: false,
      canExport: true,
    },
  });
  assert.deepEqual(studioProjectOpenTarget(report), {
    kind: 'document',
    artifactId: 'report-result',
    artifactVersion: 3,
    artifactDigest: exactDigest,
    title: 'Market opportunity',
    canEdit: false,
  });
  assert.equal(studioProjectOpenTarget({ ...report, result: { ...report.result!, type: 'pdf' } }), null);
  assert.equal(studioProjectOpenTarget({ ...report, result: { ...report.result!, artifactId: '   ' } }), null);
});

test('final-result copy distinguishes managed admission from exact legacy work', () => {
  const managed = project('managed', {
    status: 'ready',
    result: {
      artifactId: 'managed-result', type: 'html_deck', version: 2, digest: exactDigest,
      title: 'Managed', qualityState: 'admitted', reviewManaged: true,
      canEdit: true, canContinue: false, canPresent: true, canExport: true,
    },
  });
  const legacy = project('legacy', {
    status: 'ready',
    result: {
      artifactId: 'legacy-result', type: 'html_deck', version: 1, digest: exactDigest,
      title: 'Legacy', qualityState: '', reviewManaged: false,
      canEdit: true, canContinue: false, canPresent: true, canExport: true,
    },
  });
  assert.equal(studioProjectResultIsFinal(managed), true);
  assert.equal(studioProjectResultIsFinal(legacy), true);
  assert.equal(studioProjectResultIsFinal({ ...legacy, result: { ...legacy.result!, reviewManaged: undefined } }), false);
  assert.equal(studioProjectResultIsFinal({ ...managed, result: { ...managed.result!, qualityState: 'draft_needs_attention' } }), false);
});

test('relative activity labels stay compact and predictable', () => {
  const now = Date.parse('2026-08-23T14:00:00Z');
  assert.equal(studioProjectRelativeTime('2026-08-23T14:00:00Z', now), 'Now');
  assert.equal(studioProjectRelativeTime('2026-08-23T13:48:00Z', now), '12m');
  assert.equal(studioProjectRelativeTime('2026-08-23T10:00:00Z', now), '4h');
  assert.equal(studioProjectRelativeTime('2026-08-21T14:00:00Z', now), '2d');
  assert.equal(studioProjectRelativeTime('not-a-date', now), '');
});
