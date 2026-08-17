import fs from "node:fs";
import path from "node:path";
import { test } from "node:test";
import assert from "node:assert/strict";

const screen = fs.readFileSync(
  path.join(process.cwd(), "src/screens/ThreadScreen.tsx"),
  "utf8",
);
const channelList = fs.readFileSync(
  path.join(process.cwd(), "src/messaging/ChannelList.tsx"),
  "utf8",
);
const rootNavigator = fs.readFileSync(
  path.join(process.cwd(), "src/navigation/RootNavigator.tsx"),
  "utf8",
);

test("empty native threads teach conversation-first work without selecting a tool", () => {
  assert.match(screen, /ListEmptyComponent=/);
  assert.match(screen, /What do you want to accomplish\?/);
  assert.match(
    screen,
    /Scout can answer, start private work, or ask for approval when it actually matters\./,
  );
  assert.match(screen, /Starts as a draft\. Nothing runs until you send\./);
  assert.match(screen, /Create a polished 10-slide pitch deck for /);
  assert.match(screen, /Build a financial model for /);
  assert.match(screen, /setDraft\(starter\.prompt\)/);
  assert.doesNotMatch(
    screen,
    /onPress=\{\(\) => api\.|toolTemplate|data-scout-starter|packaging_studio/,
  );
});

test("empty native channels retain the human multiplayer path", () => {
  assert.match(screen, /Start the conversation/);
  assert.match(screen, /Message the team\. Mention @Scout when you want help\./);
  assert.match(screen, /threadVisibility === "private"/);
});

test("iPad keeps destination, conversation, and selected thread context together", () => {
  assert.match(screen, /const workspaceLayout = threadWorkspaceLayout\(/);
  assert.match(screen, /const iPadWorkspace = workspaceLayout\.conversationPane/);
  assert.match(screen, /accessibilityLabel="Conversations"/);
  assert.match(screen, /<ChannelList/);
  assert.match(screen, /selectedThreadId=\{privateRiff\?\.sourceThreadId \?\? route\.params\.sourceThreadId \?\? route\.params\.threadId\}/);
  assert.match(screen, /navigation\.replace\("Thread"/);
  assert.match(channelList, /accessibilityState=\{\{ selected: selectedThreadId === threadID \}\}/);
  assert.match(rootNavigator, /keepSidebarForFocusedRoute=\{Boolean\(user && sessionToken && \(activeRoute === 'Thread' \|\| activeRoute === 'ChannelRiff'\)\)\}/);
  assert.match(rootNavigator, /presentation: 'card'/);
  assert.doesNotMatch(rootNavigator, /presentation: 'fullScreenModal'/);
});

test("Chat thread opening stacks on Chat, same pattern Meet uses for Room", () => {
  assert.match(channelList, /navigation\.navigate\('Thread'/);
  assert.doesNotMatch(channelList, /navigation\.replace\('Thread'/);
});

test("empty Chat shows a CTA for starting first conversation", () => {
  assert.match(channelList, /No conversations yet/);
  assert.match(channelList, /Start your first conversation/);
  // ChannelList passes displayMode for iPad workstation support
  assert.match(channelList, /navigation\.navigate\('NewConversation', \{ displayMode: useWorkstation \? 'workstation' : 'sheet' \}\)/);
  assert.match(channelList, /WORKSTATION_MIN_WIDTH = 1024/);
  assert.match(channelList, /emptyAction/);
});
