import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath, URL } from 'node:url';
import test from 'node:test';

import type { ScoutThread } from '../api/types';
import {
  channelActiveWork,
  channelListRows,
  channelThreadAccessibilityLabel,
} from '../messaging/channelListPerformance';
import {
  threadWorkspaceLayout,
  THREAD_CONVERSATION_PANE_MIN_WIDTH,
} from '../messaging/threadWorkspaceLayout';

function source(path: string): string {
  return readFileSync(fileURLToPath(new URL(`../../${path}`, import.meta.url)), 'utf8');
}

test('checked-in Expo config declares rotation and iPad multitasking inputs', () => {
  const config = source('app.config.ts');
  assert.match(config, /orientation:\s*'default'/u);
  assert.match(config, /supportsTablet:\s*true/u);
  assert.doesNotMatch(config, /requireFullScreen:\s*true/u);
});

test('thread geometry preserves a useful detail pane across rotation, split view and large text', () => {
  assert.equal(THREAD_CONVERSATION_PANE_MIN_WIDTH, 1024);
  assert.deepEqual(threadWorkspaceLayout(743, 1, false), {
    conversationPane: false,
    detailWidth: 743,
    stackedActivity: false,
  });
  assert.deepEqual(threadWorkspaceLayout(744, 1, true), {
    conversationPane: false,
    detailWidth: 496,
    stackedActivity: false,
  });
  assert.equal(threadWorkspaceLayout(1023, 1, true).conversationPane, false);
  assert.deepEqual(threadWorkspaceLayout(1024, 1, true), {
    conversationPane: true,
    detailWidth: 476,
    stackedActivity: false,
  });
  assert.deepEqual(threadWorkspaceLayout(1024, 1.35, true), {
    conversationPane: false,
    detailWidth: 776,
    stackedActivity: true,
  });
  assert.deepEqual(threadWorkspaceLayout(1376, 1.35, false), {
    conversationPane: false,
    detailWidth: 1376,
    stackedActivity: true,
  });
  assert.deepEqual(threadWorkspaceLayout(1600, 1.35, false), {
    conversationPane: true,
    detailWidth: 1300,
    stackedActivity: true,
  });
  assert.equal(threadWorkspaceLayout(402, 1, false).detailWidth, 402);
  assert.equal(threadWorkspaceLayout(874, 1, true).detailWidth, 626);
  assert.equal(threadWorkspaceLayout(1376, 1, true).detailWidth, 828);
});

test('a 200-thread rail is virtualized, stable-keyed and driven by one shared clock', () => {
  const threads = Array.from({ length: 200 }, (_, index) => ({
    id: `thread-${index}`,
    title: `Thread ${index}`,
    visibility: index % 3 === 0 ? 'public' : 'private',
    messages: index < 12 ? [{
      id: `work-${index}`,
      kind: 'thread',
      thread: {
        id: `run-${index}`,
        mode: index % 2 ? 'document' : 'presentation',
        query: `Work ${index}`,
        status: 'running',
        agentName: 'Scout',
        currentStage: 'build_draft',
        progressPercent: 42,
      },
    }] : [],
  })) as unknown as ScoutThread[];
  const rows = channelListRows(threads);
  assert.equal(rows.filter((row) => row.kind === 'thread').length, 200);
  assert.equal(rows.filter((row) => row.kind === 'section').length, 2);
  assert.equal(new Set(rows.map((row) => row.id)).size, rows.length);

  const listSource = source('src/messaging/ChannelList.tsx');
  assert.match(listSource, /<FlashList/u);
  assert.match(listSource, /keyExtractor=\{\(row\) => row\.id\}/u);
  assert.match(listSource, /getItemType=\{\(row\) => row\.kind\}/u);
  assert.equal((listSource.match(/setInterval\(/gu) ?? []).length, 1);
  assert.match(listSource, /maxFontSizeMultiplier=\{2\}/u);
  assert.doesNotMatch(listSource, /section\.threads\.map/u);
});

test('channel Riff spaces never pollute the ordinary private-chat rail', () => {
  const rows = channelListRows([
    { id: 'channel', title: 'Design', visibility: 'public' },
    { id: 'private', title: 'Scout', visibility: 'private' },
    { id: 'riff-v2', title: 'Riff', visibility: 'private', conversationKind: 'channel_riff' },
    { id: 'riff-legacy', title: 'Legacy Riff', visibility: 'private', riff: {} as ScoutThread['riff'] },
  ] as ScoutThread[]);
  assert.deepEqual(
    rows.filter((row) => row.kind === 'thread').map((row) => row.id),
    ['channel', 'private'],
  );
});

test('thread rows and work surfaces expose state without clipping large text or spamming percent updates', () => {
  const thread = {
    id: 'thread-accessibility',
    title: 'Investor package planning',
    visibility: 'private',
    unreadCount: 4,
    messages: [{
      id: 'work-accessibility',
      kind: 'thread',
      thread: {
        id: 'run-accessibility',
        mode: 'mixed package',
        query: 'Assemble the complete investor package',
        status: 'running',
        agentName: 'Scout',
        currentStage: 'assemble_package',
        progressPercent: 61,
      },
    }],
  } as unknown as ScoutThread;
  const working = channelActiveWork(thread);
  assert.match(channelThreadAccessibilityLabel(thread, working), /4 unread/u);
  assert.match(channelThreadAccessibilityLabel(thread, working), /Scout working on Mixed package, Building/u);

  const bubble = source('src/messaging/MessageBubble.tsx');
  assert.match(bubble, /accessibilityRole="summary"/u);
  assert.match(bubble, /role="status"/u);
  assert.match(bubble, /accessibilityRole="progressbar"/u);
  assert.doesNotMatch(bubble, /<Text numberOfLines=\{2\} style=\{styles\.workQuery\}/u);

  const threadScreen = source('src/screens/ThreadScreen.tsx');
  const activityPill = source('src/messaging/WorkActivityPill.tsx');
  const shell = source('src/navigation/NativeUniversalShell.tsx');
  const sheet = source('src/messaging/LongMessageSheet.tsx');
  assert.match(sheet, /accessibilityViewIsModal/u);
  assert.match(sheet, /AccessibilityInfo\.setAccessibilityFocus/u);
  assert.match(sheet, /returnFocusHandle/u);
  assert.match(sheet, /maxFontSizeMultiplier=\{2\}/u);
  assert.match(bubble, /workDetailsTriggerRef/u);
  assert.match(bubble, /workSurfaceMaxFontSizeMultiplier = 2/u);
  assert.match(threadScreen, /<WorkActivityPill/u);
  assert.match(activityPill, /accessibilityLabel="Dismiss work status"/u);
  assert.match(activityPill, /accessibilityHint="Hides this update only for you/u);
  assert.match(activityPill, /actionStacked: \{ alignSelf: 'stretch', textAlign: 'right' \}/u);
  assert.match(activityPill, /maxFontSizeMultiplier=\{1\.8\}/u);
  assert.match(shell, /Platform\.isPad/u);
  assert.match(sheet, /accessibilityRole="header"/u);

  assert.match(threadScreen, /window\.width > window\.height\s*\? "position"\s*:\s*"padding"/u);
  assert.match(threadScreen, /contentContainerStyle=\{[\s\S]*window\.width > window\.height[\s\S]*styles\.fill/u);
  assert.match(threadScreen, /keyboardVerticalOffset=\{0\}/u);
  assert.match(threadScreen, /keyboardDismissMode=\{Platform\.OS === "ios" \? "interactive" : "on-drag"\}/u);
  assert.match(threadScreen, /workspaceLayout\.stackedActivity/u);
  assert.match(threadScreen, /nativeShellLayout\(\s*window\.width,[\s\S]*window\.fontScale,[\s\S]*\) === "sidebar"/u);
  assert.match(threadScreen, /window\.fontScale,[\s\S]*listRef\.current\?\.scrollToEnd/u);
});
