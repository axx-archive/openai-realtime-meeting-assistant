import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';

const mobileRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..', '..');
const source = (...parts: string[]) => fs.readFileSync(path.join(mobileRoot, ...parts), 'utf8');
const room = source('src', 'screens', 'RoomScreen.tsx');
const sheet = source('src', 'components', 'RoomSpecialistsSheet.tsx');
const api = source('src', 'api', 'client.ts');

test('native room exposes authenticated meeting-agent controls through the real API', () => {
  assert.match(room, /label: 'Agent team'/);
  assert.match(room, /api\.meetingSpecialists\(sessionToken, route\.params\.roomId\)/);
  assert.match(room, /api\.requestMeetingSpecialist/);
  assert.match(room, /api\.resolveMeetingSpecialist/);
  assert.match(api, /\/api\/stride\/v1\/meeting-specialists/);
});

test('native meeting-agent sheet keeps approval, dismissal, and provider honesty visible', () => {
  assert.match(sheet, /never joins until a person approves the exact invitation/);
  assert.match(sheet, /Voice joining remains off until its provider route passes qualification/);
  assert.match(sheet, />Approve</);
  assert.match(sheet, />Decline</);
  assert.match(sheet, />Dismiss agent</);
  assert.match(sheet, /invitation\.purposeSummary/);
  assert.match(sheet, /contextSummary\(invitation\.contextClasses\)/);
  assert.match(sheet, /audienceSummary\(invitation\)/);
  assert.match(sheet, /expectedSummary\(invitation\)/);
  assert.match(sheet, /limitsSummary\(invitation\)/);
  assert.match(sheet, /providerSessionStarted/);
  assert.match(sheet, />HARD LIMITS</);
  assert.doesNotMatch(sheet, /Mary · Marketing Agent/);
});
