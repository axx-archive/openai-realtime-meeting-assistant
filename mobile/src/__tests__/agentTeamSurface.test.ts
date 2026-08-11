import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';

const mobileRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..', '..');
const source = (...parts: string[]) => fs.readFileSync(path.join(mobileRoot, ...parts), 'utf8');

test('agent team surface reaches authenticated STRIDE product lifecycles', () => {
  const screen = source('src', 'screens', 'AgentTeamScreen.tsx');
  const client = source('src', 'api', 'client.ts');

  assert.match(screen, /Promise\.all\(\[\s*api\.strideStatus\(sessionToken\),\s*api\.strideRoster\(sessionToken\),\s*api\.strideMarketplace\(sessionToken\),\s*api\.strideWork\(sessionToken\),\s*api\.scoutThreads\(sessionToken\)/s);
  assert.match(client, /strideStatus\(sessionToken: string\)/);
  assert.match(client, /strideRoster\(sessionToken: string\)/);
  assert.match(client, /strideMarketplace\(sessionToken: string\)/);
  assert.match(client, /strideWork\(sessionToken: string\)/);
  assert.doesNotMatch(screen, /api\.strideStartTrial\(/);
  assert.doesNotMatch(screen, /api\.strideHire\(/);
  assert.match(client, /strideWorkDestination/);
  assert.match(client, /strideWorkDecision/);
  assert.match(client, /\/api\/stride\/v1\/status/);
  assert.match(client, /\/api\/stride\/v1\/roster/);
  assert.match(client, /\/api\/stride\/v1\/marketplace/);
  assert.match(client, /\/api\/stride\/v1\/work/);
});

test('member marketplace is a read-only directory while suggested work stays actionable', () => {
  const screen = source('src', 'screens', 'AgentTeamScreen.tsx');
  assert.match(screen, /activation fenced/);
  assert.match(screen, /profiles are read-only/);
  assert.match(screen, /each requires approval/);
  assert.match(screen, /BEST USED FOR/);
  assert.match(screen, /usageGuidance/);
  assert.match(screen, /listing\.id === 'scout' \|\| hiredListingIDs\.has\(listing\.id\)/);
  assert.doesNotMatch(screen, /Start preview/);
  assert.doesNotMatch(screen, /Hire with approval/);
  assert.doesNotMatch(screen, /Pause/);
  assert.doesNotMatch(screen, /Offboard/);
  assert.match(screen, /Approve & run/);
  assert.match(screen, /Dismiss/);
  assert.match(screen, /FlatList/);
  assert.doesNotMatch(screen, /ScrollView/);
  assert.doesNotMatch(screen, /Mary|Marketing Agent|Research Agent|Design Agent|Builder Agent/);
});

test('agent team exposes inspectable coworkers without member configuration controls', () => {
  const screen = source('src', 'screens', 'AgentTeamScreen.tsx');
  assert.match(screen, /Details/);
  assert.match(screen, /Read-only teammate profile/);
  assert.match(screen, /future administrator surface/);
  assert.match(screen, /Open chat/);
  for (const forbidden of ['Create private agent', 'Preview semantic diff', 'Approve update', 'Add assignment', 'Correct latest', 'Forget latest', 'Clean export receipt']) {
    assert.doesNotMatch(screen, new RegExp(forbidden));
  }
});

test('suggested work requires an explicit eligible project destination', () => {
  const screen = source('src', 'screens', 'AgentTeamScreen.tsx');
  assert.match(screen, /Choose project/);
  assert.match(screen, /Change project/);
  assert.match(screen, /presentationStyle="formSheet"/);
  assert.match(screen, /thread\.visibility === 'public'/);
  assert.match(screen, /thread\.table !== true/);
  assert.match(screen, /title !== 'team' && title !== 'general'/);
  assert.match(screen, /mode: 'existing', threadId/);
  assert.match(screen, /mode: 'new', title/);
  assert.doesNotMatch(screen, /Use source/);
});

test('work deck exposes the native team destination', () => {
  const deck = source('src', 'screens', 'DeckScreen.tsx');
  const navigator = source('src', 'navigation', 'RootNavigator.tsx');
  assert.match(deck, /route: 'AgentTeam', label: 'Agent team', hint: 'Coworkers and Marketplace'/);
  assert.match(navigator, /<Stack\.Screen name="AgentTeam" component=\{AgentTeamScreen\}/);
});
