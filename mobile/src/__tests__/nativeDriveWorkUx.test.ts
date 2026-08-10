import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import test from 'node:test';

const source = (relative: string) => fs.readFileSync(path.resolve(import.meta.dirname, '..', relative), 'utf8');
const bubble = source('messaging/MessageBubble.tsx');
const screen = source('screens/ThreadScreen.tsx');
const files = source('screens/FilesScreen.tsx');
const attachmentSheet = source('messaging/AttachmentSourceSheet.tsx');
const composer = source('messaging/MentionComposerInput.tsx');
const client = source('api/client.ts');

test('proposal approval submits the human-edited objective once', () => {
  assert.match(bubble, /accessibilityLabel=\{`\$\{proposal\?\.agentName \|\| 'Scout'\} work objective`\}/);
  assert.match(bubble, /onChangeProposalObjective\?\.\(message, value\)/);
  assert.match(bubble, /onResolveProposal\?\.\(message, 'accepted',[\s\S]*\.trim\(\)\)/);
  assert.match(screen, /editedObjective/);
  assert.match(screen, /api\.resolveScoutProposal\([\s\S]*objective/);
});

test('terminal work opens from the authorized read and receipts only mutating Save', () => {
  assert.match(bubble, /Open deliverable/);
  assert.match(bubble, /Save deliverable to Drive/);
  assert.match(bubble, /Edit prompt and regenerate deliverable/);
  assert.doesNotMatch(screen, /action: 'open'/);
  assert.match(screen, /const response = await api\.artifact\(sessionToken, artifactId\)/);
  assert.match(screen, /api\.saveArtifactToDrive\(sessionToken/);
  assert.doesNotMatch(screen, /action: 'save'/);
  assert.match(screen, /artifactDriveSaveCapability/);
  assert.match(bubble, /Save to Drive unavailable/);
  assert.match(screen, /fileName: normalizedName/);
  assert.match(client, /assistant\/threads\/follow-up/);
  assert.doesNotMatch(screen, /## Work log|Source trail and review receipts|progressPercent/);
  assert.match(bubble, /workProgressCopy/);
  assert.match(bubble, /progressPercent/);
});

test('running and failed research expose honest progress and recovery actions', () => {
  assert.doesNotMatch(bubble, /if \(workThread\?\.active\) return null/);
  assert.match(bubble, /output_truncated/);
  assert.match(bubble, /before a deliverable could be accepted/);
  assert.match(bubble, /View details/);
  assert.match(bubble, /Retry research/);
	assert.doesNotMatch(bubble, /review the draft|partial draft/iu);
  assert.match(bubble, /accessible=\{!\(workProposal \|\| workThread\)\}/);
  assert.match(bubble, /Open live work details/);
  assert.match(bubble, /started this research/);
  assert.doesNotMatch(bubble, /Confirmed · launched once/);
});

test('a failed revision stays openable but is labeled honestly', () => {
  assert.match(bubble, /revisionNeedsAttention/);
	assert.match(bubble, /followUpStatus === 'needs_attention'/);
  assert.match(bubble, /Deliverable ready · revision needs attention/);
  assert.match(bubble, /Delivered · revision needs attention/);
});

test('Drive browse and hash search mint exact destination-bound grants', () => {
  assert.match(attachmentSheet, /Browse Drive/);
  assert.match(composer, /activeDocumentQuery\(nextValue\)/);
  assert.match(screen, /onDocumentQuery=\{\(query\) => openDrivePicker\('message', query, true\)\}/);
  assert.match(screen, /onBrowseDrive=\{\(query = ''\) => openDrivePicker\('reply', query, true\)\}/);
  assert.match(client, /assistant\/attachments\/from-file/);
  assert.match(client, /!attachment\?\.ref \|\| !attachment\.mime \|\| !attachment\.sourceId \|\| !attachment\.sourceRevision/);
  assert.doesNotMatch(screen, /downloadUrl.*sourceId|file\.name.*sourceRevision/);
});

test('Drive file management puts rename in the vertical-dot menu', () => {
  assert.match(files, /More actions for \$\{file\.name\}/);
  assert.match(files, /verticalEllipsis/);
  assert.match(files, /'Rename', 'Move'/);
  assert.match(files, /api\.renameFile\(sessionToken, file\.id, name\)/);
  assert.match(client, /method: 'PATCH', body: \{ id, name \}/);
});
