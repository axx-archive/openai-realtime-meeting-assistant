import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import test from 'node:test';

const messagingRoot = path.resolve(import.meta.dirname, '..', 'messaging');
const screenSource = fs.readFileSync(path.resolve(import.meta.dirname, '..', 'screens', 'ThreadScreen.tsx'), 'utf8');
const sheetSource = fs.readFileSync(path.join(messagingRoot, 'ThreadDetailSheet.tsx'), 'utf8');
const actionSource = fs.readFileSync(path.join(messagingRoot, 'MessageActionSheet.tsx'), 'utf8');
const activitySource = fs.readFileSync(path.join(messagingRoot, 'LongMessageSheet.tsx'), 'utf8');
const mentionSource = fs.readFileSync(path.join(messagingRoot, 'MentionComposerInput.tsx'), 'utf8');
const bubbleSource = fs.readFileSync(path.join(messagingRoot, 'MessageBubble.tsx'), 'utf8');
const apiSource = fs.readFileSync(path.resolve(import.meta.dirname, '..', 'api', 'client.ts'), 'utf8');

test('mobile channel rows render only topology roots with one persistent reply affordance', () => {
  assert.match(screenSource, /buildThreadReplyTopology\(visibleMessages\)/);
  assert.match(screenSource, /const transcriptFeedMessages = replyTopology\.feedMessages/);
  assert.match(screenSource, /compactThreadWorkMessages\(transcriptFeedMessages\)/);
  assert.match(screenSource, /compactThreadWorkMessages\(replyTopology\.repliesFor\(threadContextRoot\)\)/u);
  assert.match(screenSource, /threadReplies: compactThreadWorkMessages\(replyTopology\.repliesFor\(message\)\)\.map/u);
  assert.match(screenSource, /feedMessagesRef\.current = feedMessages/);
  assert.match(screenSource, /if \(isScoutWorkMessage\(message\)\)[\s\S]*setWorkActivityMessage\(message\)/u);
  assert.match(screenSource, /const currentWorkMessage = useMemo\([\s\S]*latestScoutWorkMessage\(visibleMessages\)/u);
  assert.doesNotMatch(screenSource, /activeWorkMessage \?\? latestResultWorkMessage/u);
  assert.match(screenSource, /feedMessages\.map\(\(message, index\)/);
  assert.match(screenSource, /onOpenThread=\{openThreadContext\}/);
  assert.doesNotMatch(screenSource, /const \[replyingTo,/);
  assert.doesNotMatch(screenSource, /Replying to \{String\(replyingTo/);
});

test('thread replies use a native dismissible page sheet with their own composer', () => {
  assert.match(sheetSource, /presentationStyle="pageSheet"/);
  assert.match(sheetSource, /measureSheetKeyboardOffset/);
  assert.match(sheetSource, /measureInWindow/);
  assert.match(sheetSource, /screenHeight - height/);
  assert.match(sheetSource, /keyboardVerticalOffset=\{keyboardOffset\}/);
  assert.match(sheetSource, /onRequestClose=\{onClose\}/);
  assert.match(sheetSource, /name="xmark"/);
  assert.match(sheetSource, /contentInsetAdjustmentBehavior="automatic"/);
  assert.match(sheetSource, /showReplyContext=\{false\}/);
  assert.match(sheetSource, /<MentionComposerInput/);
  assert.match(sheetSource, /candidates=\{mentionCandidates\}/);
  assert.match(sheetSource, /accessibilityLabel="Reply in thread"/);
  assert.match(sheetSource, /onSend\(text, pendingFiles, projectContextToken\)/);
  assert.match(sheetSource, /accessibilityLabel="Add attachment to reply"/);
  assert.match(sheetSource, /accessibilityLabel="Reply attachments"/);
  assert.match(screenSource, /text\.trim\(\),\s*\[\.\.\.files\],\s*rootID/s);
  assert.match(screenSource, /onGifs=\{\(\) => setGifPickerOpen\(true\)\}/);
  assert.match(screenSource, /addGiphyGif\(gif, attachmentTarget\)/);
  assert.match(sheetSource, /onLongPress=\{onLongPress\}/);
  assert.match(sheetSource, /\{actionOverlay\}/);
  assert.match(screenSource, /actionOverlay=\{\s*<>/);
  assert.match(screenSource, /renderLongMessageSheet\(true\)/);
  assert.match(screenSource, /renderMessageActionSheet\(true\)/);
  assert.match(screenSource, /\{threadContextRoot \? null : renderMessageActionSheet\(\)\}/);
  assert.match(actionSource, /if \(contained\) return visible \? content : null/);
  assert.match(actionSource, /containedModal: \{ position: 'absolute', inset: 0/);
  assert.doesNotMatch(sheetSource, /accessibilityLabel="Edit reply"/);
  assert.doesNotMatch(sheetSource, /accessibilityLabel="Delete reply"/);
  assert.doesNotMatch(sheetSource, /\.focus\(\)/);
  assert.match(sheetSource, /initialScrollCompleteRef/);
  assert.match(sheetSource, /scrollToEnd\(\{ animated: false \}\)/);
  assert.doesNotMatch(bubbleSource, /name="chevron\.up"/);
  assert.doesNotMatch(sheetSource, /autoFocus/);
  assert.doesNotMatch(screenSource, /focusComposer/);
});

test('reply summaries resolve current participant avatars instead of initials-only placeholders', () => {
  assert.match(screenSource, /participantByEmail\.get\(\s*String\(reply\.authorEmail/);
  assert.match(screenSource, /avatarDataURL: replyParticipant\.avatarDataURL/);
  assert.match(screenSource, /Array\.from\(participantByEmail\.values\(\)\)/);
});

test('mobile work activity uses the same native page-sheet behavior', () => {
  assert.match(activitySource, /presentationStyle="pageSheet"/);
  assert.match(activitySource, /onRequestClose=\{onClose\}/);
  assert.match(activitySource, /name="xmark"/);
  assert.match(activitySource, /SCOUT · ACTIVITY/);
  assert.match(activitySource, /STRIDE · DELIVERABLE/);
  assert.match(activitySource, /variant=\{report \? 'report' : 'message'\}/);
});

test('full responses opened from a reply stay inside the visible iOS thread sheet', () => {
  assert.match(activitySource, /contained\?: boolean/);
  assert.match(activitySource, /if \(contained\) return visible \? sheet : null/);
  assert.match(activitySource, /containedSheet: \{ position: 'absolute', inset: 0/);
  assert.match(screenSource, /\{threadContextRoot \? null : renderLongMessageSheet\(\)\}/);
  assert.match(screenSource, /renderLongMessageSheet\(true\)/);
});

test('mention suggestions describe every AI worker as a role-specific teammate', () => {
  assert.match(mentionSource, /Chief of staff/);
  assert.match(mentionSource, /Specialist/);
  assert.match(mentionSource, /Teammate/);
  assert.doesNotMatch(mentionSource, /Agent · confirm work/);
  assert.doesNotMatch(mentionSource, /AI teammate/);
});

test('work cards use the available narrow-screen width without a fixed minimum', () => {
  assert.match(bubbleSource, /\(workThread \|\| workProposal\) && styles\.stackWork/);
  assert.match(bubbleSource, /stackWork:\s*\{[^}]*width:\s*'100%'[^}]*maxWidth:\s*'100%'/s);
  assert.match(bubbleSource, /workCard:\s*\{[^}]*width:\s*'100%'[^}]*minWidth:\s*0/s);
  assert.doesNotMatch(bubbleSource, /workCard:\s*\{[^}]*minWidth:\s*2(?:48|60)/s);
});

test('reply-origin agent proposals can be confirmed or dismissed in the native thread sheet', () => {
  assert.match(bubbleSource, /proposalPending[\s\S]*Run once[\s\S]*Not now/);
  assert.match(bubbleSource, /onResolveProposal\?\.\(message, 'accepted',[\s\S]*\.trim\(\)\)/);
  assert.match(bubbleSource, /onResolveProposal\?\.\(message, 'dismissed',[\s\S]*\.trim\(\)\)/);
  assert.match(sheetSource, /onResolveProposal=\{onResolveProposal\}/);
  assert.match(screenSource, /api\.resolveScoutProposal\(/);
  assert.match(apiSource, /chat-threads\/\$\{encodeURIComponent\(threadId\)\}\/proposal/);
});

test('governed proposal approval is bound to the exact server-held objective', () => {
  assert.match(bubbleSource, /const exactApproval = String\(message\.intentOutcome \?\? proposal\?\.intentOutcome \?\? ''\) === 'approval_required'[\s\S]*proposal\?\.effectClass/u);
  assert.match(bubbleSource, /editable=\{!resolvingProposal && !exactApproval\}/u);
  assert.match(bubbleSource, /exactApproval \? \(proposal\?\.objective \?\? proposal\?\.summary \?\? body\)/u);
  assert.match(bubbleSource, /Approval is bound to this exact request\. Edit by sending a new message\./u);
});

test('native approval controls admit held workstream, goal, and registry-tool cards', () => {
  assert.match(bubbleSource, /\['workstream', 'tool_run', 'goal_run'\]\.includes\(proposalKind\)/u);
  assert.match(bubbleSource, /proposalKind === 'goal_run'[\s\S]*started this goal/u);
  assert.match(bubbleSource, /workProposal \? \([\s\S]*proposalPending[\s\S]*Run once/u);
});

test('governed completed work uses the rich card and authenticated product artifact path', () => {
  assert.match(bubbleSource, /kind !== 'work_result' && kind !== 'work_record'/u);
  assert.match(bubbleSource, /mode: 'completed work'/u);
  assert.match(bubbleSource, /status === 'complete' \|\| status === 'completed' \|\| status === 'published'/u);
  assert.match(bubbleSource, /Deterministic local · provider fenced/u);
  assert.match(bubbleSource, /!workThread\.governedRecord \? <Pressable[^>]*accessibilityLabel=\{workSaved/u);
  assert.match(bubbleSource, /!workThread\.governedRecord \? <Pressable[^>]*accessibilityLabel="Edit prompt and regenerate deliverable"/u);
  assert.match(screenSource, /api\.strideWorkArtifact\([\s\S]*governedWork\.artifactHref/u);
  assert.ok(apiSource.includes('/^\\/api\\/stride\\/v1\\/work\\/runs\\/[a-z0-9_-]+\\/artifact$/u'));
  assert.match(screenSource, /Approved outcome\\n/u);
  assert.match(screenSource, /Verified source\\n/u);
});
