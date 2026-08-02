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
  assert.match(client, /strideStartTrial\(sessionToken: string, listingId: string\)/);
  assert.match(client, /strideHire\(sessionToken: string, listingId: string, revision: number\)/);
  assert.match(client, /strideWorkDestination/);
  assert.match(client, /strideWorkDecision/);
  assert.match(client, /\/api\/stride\/v1\/status/);
  assert.match(client, /\/api\/stride\/v1\/roster/);
  assert.match(client, /\/api\/stride\/v1\/marketplace/);
  assert.match(client, /\/api\/stride\/v1\/work/);
});

test('marketplace and suggested work stay visibly provider-fenced but human-actionable', () => {
  const screen = source('src', 'screens', 'AgentTeamScreen.tsx');
  assert.match(screen, /activation fenced/);
  assert.match(screen, /Start preview/);
  assert.match(screen, /Hire with approval/);
  assert.match(screen, /Approve & run/);
  assert.match(screen, /Dismiss/);
  assert.match(screen, /FlatList/);
  assert.doesNotMatch(screen, /ScrollView/);
  assert.doesNotMatch(screen, /Mary|Marketing Agent|Research Agent|Design Agent|Builder Agent/);
});

test('agent team exposes inspectable coworkers, opt-in growth, and a closed private template', () => {
  const screen = source('src', 'screens', 'AgentTeamScreen.tsx');
  const client = source('src', 'api', 'client.ts');

  for (const label of [
    'Create private agent',
    'Details',
    'Preview semantic diff',
    'Approve update',
    'Reject & roll back',
    'Add assignment',
    'Correct latest',
    'Forget latest',
    'Clean export receipt',
  ]) {
    assert.match(screen, new RegExp(label.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')));
  }
  assert.match(screen, /Nothing changes until the semantic diff is reviewed and approved/);
  assert.match(screen, /Templates cannot contain code, commands, credentials, environment values, hooks, or raw tool-server configuration/);
  assert.match(screen, /marketplace\?\.canManage/);
  assert.match(client, /strideCreatePrivateAgentTemplate/);
  assert.match(client, /strideProposeAgentUpdate/);
  assert.match(client, /strideResolveAgentUpdate/);
  assert.match(client, /strideAssignAgent/);
  assert.match(client, /strideRecordAgentLearning/);
  assert.match(client, /strideResolveAgentLearning/);
  assert.match(client, /strideExportAgent/);
  assert.doesNotMatch(client, /\/configure/);
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
