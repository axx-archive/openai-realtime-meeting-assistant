import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import path from 'node:path';
import test from 'node:test';

const screen = readFileSync(path.resolve(import.meta.dirname, '..', 'screens', 'ThreadScreen.tsx'), 'utf8');
const client = readFileSync(path.resolve(import.meta.dirname, '..', 'api', 'client.ts'), 'utf8');

test('native existing threads keep Project choice zero-effect until explicit Send', () => {
  assert.match(screen, /api\.projectContext\(sessionToken, \{ text: draft\.trim\(\), destination: \{ route: "thread", threadId: route\.params\.threadId \} \}\)/u);
  assert.match(screen, /accessibilityHint="Opens the authorized Project chooser\. Nothing changes until you send\."/u);
  assert.match(screen, /accessibilityRole="radio"/u);
  assert.match(screen, /\[\{ title: "No project", token: "" \}, \.\.\.projectContext\.choices\]/u);
  assert.match(screen, /projectExplicitNone \? "No project" : "Add project"/u);
  assert.match(screen, /setProjectExplicitNone\(!choice\.token\)/u);
  assert.match(screen, /!projectExplicitNone && next\.suggested\?\.token/u);
  assert.match(screen, /projectContextToken = !editingMessage && selectedProject\?\.text === text/u);
  assert.match(screen, /projectContextToken,\s*\);/u);
  assert.match(screen, /Send the Project-linked message first, then attach files in the next turn/u);
});

test('native message transport carries only the signed Project token', () => {
  assert.match(client, /\.\.\.\(projectContextToken \? \{ projectContextToken \} : \{\}\)/u);
  assert.doesNotMatch(client, /sendScoutMessage[\s\S]{0,900}projectId/u);
  assert.doesNotMatch(client, /sendScoutMessage[\s\S]{0,900}authorityGeneration/u);
});
