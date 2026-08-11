import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import path from 'node:path';
import test from 'node:test';

const client = readFileSync(path.resolve(import.meta.dirname, '..', 'api', 'client.ts'), 'utf8');
const screen = readFileSync(path.resolve(import.meta.dirname, '..', 'screens', 'ThreadScreen.tsx'), 'utf8');
const bubble = readFileSync(path.resolve(import.meta.dirname, '..', 'messaging', 'MessageBubble.tsx'), 'utf8');

test('generic deliverables use only the closed save-only Drive capability', () => {
  assert.match(client, /artifactDriveSaveCapability\(\s*sessionToken: string,?\s*\)/u);
  assert.match(client, /saveArtifactToDrive\([\s\S]*?operationId: string;[\s\S]*?artifact: ArtifactDispositionRef;[\s\S]*?["']\/api\/artifact-drive-saves\/v1["']/u);
  const saveOnlyMethod = client.match(/saveArtifactToDrive\([\s\S]*?\n  \},\n/u)?.[0] ?? '';
  assert.doesNotMatch(saveOnlyMethod, /action|discard|confirmation/u);
  assert.match(screen, /api\s*\.artifactDriveSaveCapability\(sessionToken\)/u);
  assert.match(screen, /capability\.action === ["']save["'] &&\s*capability\.receiptBacked === true/u);
  assert.match(screen, /api\.saveArtifactToDrive\(sessionToken/u);
  assert.match(screen, /receipt\?\.operationId !== attempt\.operationId/u);
  assert.match(screen, /receipt\.action !== ["']save["']/u);
  assert.match(screen, /sameDispositionRef\(receipt\.artifact, artifact\.dispositionRef\)/u);
  assert.match(screen, /receipt\.drive\.sourceArtifactId !== artifactId/u);
  assert.doesNotMatch(screen.match(/const saveWorkArtifact = useCallback[\s\S]*?const beginRegenerateWorkArtifact/u)?.[0] ?? '', /artifactDisposition\(/u);
});

test('native work cards are honest while save authority is checking or unavailable', () => {
  assert.match(bubble, /workDriveSaveAvailability !== ["']available["']/u);
  assert.match(bubble, /Checking Save to Drive availability/u);
  assert.match(bubble, /Save to Drive unavailable/u);
  assert.match(bubble, /workDriveSaveAvailability === ["']unavailable["'] \? ["']Unavailable["']/u);
  assert.match(screen, /\.catch\(\(\) => \{[\s\S]*setWorkDriveSaveAvailability\(["']unavailable["']\)/u);
});
