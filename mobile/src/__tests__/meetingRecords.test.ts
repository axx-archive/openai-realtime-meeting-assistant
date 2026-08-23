import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import test from 'node:test';
import type { MeetingRecordDetail, MeetingRecordIndexItem } from '../api/types';
import { isDefinitiveMeetingRecordDenial, meetingRecordDetailRemainsCurrent, meetingRecordReferenceHasExactDestination, meetingRecordReturnLabel, meetingRecordSourceScrollOffset } from '../screens/meetingRecordsState';

const root = path.resolve(import.meta.dirname, '..');
const screen = fs.readFileSync(path.join(root, 'screens', 'MeetingsScreen.tsx'), 'utf8');
const thread = fs.readFileSync(path.join(root, 'screens', 'ThreadScreen.tsx'), 'utf8');
const bubble = fs.readFileSync(path.join(root, 'messaging', 'MessageBubble.tsx'), 'utf8');
const client = fs.readFileSync(path.join(root, 'api', 'client.ts'), 'utf8');
const types = fs.readFileSync(path.join(root, 'api', 'types.ts'), 'utf8');
const navigator = fs.readFileSync(path.join(root, 'navigation', 'RootNavigator.tsx'), 'utf8');
const navigationTypes = fs.readFileSync(path.join(root, 'navigation', 'types.ts'), 'utf8');
const card = fs.readFileSync(path.join(root, 'components', 'Card.tsx'), 'utf8');
const screenFrame = fs.readFileSync(path.join(root, 'components', 'Screen.tsx'), 'utf8');

test('Meeting Records fence list detail and conversation requests independently', () => {
  assert.match(screen, /rowsGenerationRef/u);
  assert.match(screen, /detailGenerationRef/u);
  assert.match(screen, /conversationGenerationRef/u);
  assert.match(screen, /rowsToken === sessionToken/u);
  assert.match(screen, /detailToken === sessionToken/u);
  assert.match(client, /meetingConversation\([\s\S]*recordRevision/u);
  assert.match(screen, /api\.meetingConversation\(token, current\.id, current\.recordRevision\)/u);
});

test('Meeting Record citations return to the exact transcript interval', () => {
  assert.match(thread, /source\.kind === 'meeting_transcript'/u);
  assert.match(thread, /navigation\.navigate\('Meetings', \{ meetingId: source\.meetingId, segmentId: source\.segmentId \}\)/u);
  assert.match(bubble, /Opens the exact Meeting Record transcript interval/u);
  assert.match(screen, /segmentId: route\.params\?\.meetingId === selectedId/u);
  assert.match(screen, /setTranscriptOpen\(true\)/u);
});

test('Ask Scout opens an exact-revision private ordinary conversation', () => {
  assert.match(screen, /Ask Scout about this meeting/u);
  assert.match(screen, /Starts a private conversation bound to this exact Meeting Record revision/u);
  assert.match(screen, /navigation\.navigate\('Thread'/u);
});

test('Meeting Record opens only exact Project or successor-artifact Work destinations', () => {
  assert.match(screen, /\[\.\.\.visibleDetail\.work, \.\.\.visibleDetail\.projects, \.\.\.visibleDetail\.artifacts\]/u);
  assert.match(screen, /Open Meeting Record \$\{reference\.kind === 'project' \? 'Project'/u);
  assert.match(types, /work: MeetingRecordReference\[\]/);
  assert.match(types, /work\?: MeetingRecordReference\[\]/);
  assert.match(screen, /Open linked \$\{reference\.kind === 'project' \? 'Project' : 'Work'\}/u);
  assert.equal(meetingRecordReferenceHasExactDestination({ id: 'legacy-card', title: 'Old card', kind: 'work' }), false);
  assert.equal(meetingRecordReferenceHasExactDestination({ id: 'legacy-card', title: 'Current result', kind: 'work', openKind: 'artifact', openId: 'artifact-1' }), true);
  assert.equal(meetingRecordReferenceHasExactDestination({ id: 'project-1', title: 'Project', kind: 'project', openKind: 'project', openId: 'thread-1' }), true);
  assert.match(screen, /filter\(meetingRecordReferenceHasExactDestination\)/u);
  assert.match(screen, /\[\.\.\.visibleDetail\.work, \.\.\.visibleDetail\.projects, \.\.\.visibleDetail\.artifacts\]\.filter\(meetingRecordReferenceHasExactDestination\)/u);
  assert.match(screen, /navigation\.navigate\('Thread', \{ threadId: openId/u);
	assert.match(screen, /navigation\.navigate\('Files', \{ fileId: openId \}\)/u);
	assert.doesNotMatch(screen, /navigation\.navigate\('WorkHome'\)/u);
	assert.match(navigationTypes, /Board: \{ cardId\?: string \} \| undefined/u);
	assert.match(navigator, /name="Board" component=\{WorkHubScreen\}/u);
	assert.doesNotMatch(navigator, /component=\{BoardScreen\}/u);
});

test('Meeting Record library pages older permanent records', () => {
  assert.match(client, /meetingCursor/u);
  assert.match(types, /nextCursor\?: string/u);
  assert.match(screen, /Load older Meeting Records/u);
  assert.match(screen, /api\.meetings\(token, cursor\)/u);
});

test('Meeting Record typography keeps title outcome and status together at accessibility sizes', () => {
  assert.match(card, /rowLargeText:[\s\S]*flexDirection: 'column'/u);
  assert.match(card, /<Text maxFontSizeMultiplier=\{2\} style=\{styles\.title\}>/u);
  assert.match(card, /maxFontSizeMultiplier=\{2\}[\s\S]*styles\.badgeText/u);
  assert.match(card, /<Text maxFontSizeMultiplier=\{2\} style=\{styles\.subtitle\}>/u);
  assert.match(card, /<Text maxFontSizeMultiplier=\{2\} style=\{styles\.meta\}>/u);
  assert.match(screenFrame, /maxFontSizeMultiplier=\{2\} style=\{styles\.title\}/u);
  assert.match(screenFrame, /maxFontSizeMultiplier=\{2\} style=\{styles\.subtitle\}/u);
});

test('Meeting Record cache fails closed on denial, removal, or revision drift', () => {
  const row = { id: 'meeting-1', recordRevision: 'rev-1' } as MeetingRecordIndexItem;
  const detail = { id: 'meeting-1', recordRevision: 'rev-1' } as MeetingRecordDetail;
  assert.equal(meetingRecordDetailRemainsCurrent([row], 'meeting-1', detail), true);
  assert.equal(meetingRecordDetailRemainsCurrent([], 'meeting-1', detail), false);
  assert.equal(meetingRecordDetailRemainsCurrent([{ ...row, recordRevision: 'rev-2' }], 'meeting-1', detail), false);
  assert.equal(isDefinitiveMeetingRecordDenial(403), true);
  assert.equal(isDefinitiveMeetingRecordDenial(404), true);
  assert.equal(isDefinitiveMeetingRecordDenial(500), false);
  assert.match(screen, /setDetail\(null\)[\s\S]*setDetailToken\(null\)/u);
  assert.match(screen, /if \(sessionToken\) void loadRows\(\)/u);
  assert.match(screen, /useFocusEffect[\s\S]*loadRows\(true\)/u);
  assert.match(screen, /useFocusEffect[\s\S]*selectedIdRef\.current[\s\S]*loadDetail\(selectedIdRef\.current\)/u);
  assert.doesNotMatch(screen, /useEffect\([\s\S]{0,500}\}, \[isFocused/u);
});

test('Meeting Record source affordances expose exact revision and preserve return state', () => {
  assert.match(screen, /Open exact transcript source/u);
  assert.match(screen, /source \{claim\.sources\[0\]\.correctionState\}/u);
  assert.match(screen, /setTranscriptOpen\(true\)[\s\S]*segmentId: source\.segmentId/u);
  assert.match(screen, /Blur[\s\S]*preserves the exact selected/u);
  assert.equal(meetingRecordSourceScrollOffset(780, 240, 12), 1008);
  assert.equal(meetingRecordSourceScrollOffset(0, 4, 12), 0);
  assert.equal(meetingRecordReturnLabel('recap'), 'Back to live recap');
  assert.match(screen, /scrollTo\(\{ y, animated: true \}\)/u);
  assert.match(screen, /setAccessibilityFocus/u);
  assert.match(screen, /announceForAccessibility\('Exact transcript source opened'\)/u);
});

test('live recap and transcript open the permanent record and retain an exact return seam', () => {
  const room = fs.readFileSync(path.join(root, 'screens', 'RoomScreen.tsx'), 'utf8');
  const sheet = fs.readFileSync(path.join(root, 'components', 'RoomConversationSheet.tsx'), 'utf8');
  const shell = fs.readFileSync(path.join(root, 'navigation', 'nativeShellModel.ts'), 'utf8');
  assert.match(room, /navigation\.navigate\('Meetings',[\s\S]*returnToRoomId:[\s\S]*returnMode: mode/u);
  assert.match(room, /meetingRecordReturnModeRef[\s\S]*setConversationMode\(mode\)[\s\S]*setConversationVisible\(true\)/u);
  assert.match(sheet, /Open permanent Meeting Record/u);
  assert.match(sheet, /transcriptOffsetRef/u);
  assert.match(sheet, /recapOffsetRef/u);
  assert.match(sheet, /setAccessibilityFocus/u);
  // Meetings routes to 'home' per 4-destination model (STRIDE mobile E2E evolution)
  assert.match(shell, /Meetings: 'home'/u);
});
