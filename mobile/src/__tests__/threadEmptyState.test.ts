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
  assert.match(screen, /selectedThreadId=\{route\.params\.threadId\}/);
  assert.match(screen, /navigation\.replace\("Thread"/);
  assert.match(channelList, /accessibilityState=\{\{ selected: selectedThreadId === threadID \}\}/);
  assert.match(rootNavigator, /keepSidebarForFocusedRoute=\{Boolean\(user && sessionToken && activeRoute === 'Thread'\)\}/);
  assert.match(rootNavigator, /presentation: 'card'/);
  assert.doesNotMatch(rootNavigator, /presentation: 'fullScreenModal'/);
});
