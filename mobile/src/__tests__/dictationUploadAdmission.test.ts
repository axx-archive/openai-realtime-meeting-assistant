import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import test from 'node:test';
import vm from 'node:vm';
import { fileURLToPath } from 'node:url';
import ts from 'typescript';

type Attempt = { generation: number; uri: string };
type Admission = { admitted: boolean; attempt: Attempt };
type Gate = {
  admitDictationUploadAttempt: (active: Attempt | null, uri: string, generation: number) => Admission;
  clearDictationUploadAttempt: (active: Attempt | null, exact: Attempt) => Attempt | null;
  hasUsableDictationTranscript: (text: string) => boolean;
};

const mobileRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..', '..');
const hookPath = path.join(mobileRoot, 'src', 'voice', 'useDictation.ts');

/**
 * Load only the pure gate functions from the production hook. Importing the
 * whole hook under node:test would execute React Native's platform entrypoint.
 */
function loadProductionGate(): Gate {
  const source = fs.readFileSync(hookPath, 'utf8');
  const tree = ts.createSourceFile(hookPath, source, ts.ScriptTarget.Latest, true, ts.ScriptKind.TS);
  const wanted = new Set([
    'admitDictationUploadAttempt',
    'clearDictationUploadAttempt',
    'hasUsableDictationTranscript',
  ]);
  const declarations = tree.statements.filter((statement): statement is ts.FunctionDeclaration => (
    ts.isFunctionDeclaration(statement)
    && Boolean(statement.name && wanted.has(statement.name.text))
  ));
  assert.equal(declarations.length, wanted.size, 'production upload gate functions must remain directly testable');
  const isolatedSource = declarations.map((declaration) => declaration.getText(tree)).join('\n');
  const javascript = ts.transpileModule(isolatedSource, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
  }).outputText;
  const module = { exports: {} as Record<string, unknown> };
  vm.runInNewContext(javascript, { exports: module.exports, module });
  return module.exports as unknown as Gate;
}

test('two immediate sends admit one provider request and deliver its transcript once', async () => {
  const gate = loadProductionGate();
  let activeAttempt: Attempt | null = null;
  let requestGeneration = 0;
  let providerRequests = 0;
  let deliveries = 0;
  let resolveProvider!: (result: { text: string }) => void;
  const provider = new Promise<{ text: string }>((resolve) => { resolveProvider = resolve; });

  const send = async () => {
    const generation = requestGeneration + 1;
    const admission = gate.admitDictationUploadAttempt(activeAttempt, 'file:///held.m4a', generation);
    if (!admission.admitted) return false;
    const exactAttempt = admission.attempt;
    activeAttempt = exactAttempt;
    requestGeneration = generation;
    providerRequests += 1;
    try {
      const result = await provider;
      if (activeAttempt !== exactAttempt || requestGeneration !== generation) return false;
      if (result.text) deliveries += 1;
      return true;
    } finally {
      activeAttempt = gate.clearDictationUploadAttempt(activeAttempt, exactAttempt);
    }
  };

  const first = send();
  const duplicate = send();
  assert.equal(providerRequests, 1, 'admission occurs synchronously before the provider await');
  assert.equal(await duplicate, false);
  resolveProvider({ text: 'One transcript' });
  assert.equal(await first, true);
  assert.equal(providerRequests, 1);
  assert.equal(deliveries, 1);
  assert.equal(activeAttempt, null);
});

test('an old attempt completion cannot clear a newer admitted upload', () => {
  const gate = loadProductionGate();
  const oldAttempt = gate.admitDictationUploadAttempt(null, 'file:///old.m4a', 1).attempt;
  const newAttempt = gate.admitDictationUploadAttempt(null, 'file:///new.m4a', 3).attempt;
  assert.equal(gate.clearDictationUploadAttempt(newAttempt, oldAttempt), newAttempt);
  assert.equal(gate.clearDictationUploadAttempt(newAttempt, newAttempt), null);
});

test('an HTTP-200 blank transcript remains retryable and cannot delete the held clip', () => {
  const gate = loadProductionGate();
  for (const blank of ['', '   ', '\n\t']) assert.equal(gate.hasUsableDictationTranscript(blank), false);
  assert.equal(gate.hasUsableDictationTranscript('  Useful words.  '), true);

  const source = fs.readFileSync(hookPath, 'utf8');
  const upload = source.slice(source.indexOf('const upload = useCallback'), source.indexOf('const fenceFocusLease'));
  const blankGuard = upload.indexOf('if (!hasUsableDictationTranscript(result.text))');
  const deleteFile = upload.indexOf('deleteDictationFile(recording.uri)');
  const clearPending = upload.indexOf('pendingRef.current = null');
  assert.ok(blankGuard >= 0 && blankGuard < deleteFile && blankGuard < clearPending);
  const retryableBranch = upload.slice(blankGuard, deleteFile);
  assert.match(retryableBranch, /setState\('error'\)/);
  assert.match(retryableBranch, /recording is saved — retry or delete it/);
  assert.match(retryableBranch, /return false;/);
});

test('the hook installs the gate before provider invocation and clears only in finally', () => {
  const source = fs.readFileSync(hookPath, 'utf8');
  const upload = source.slice(source.indexOf('const upload = useCallback'), source.indexOf('const fenceFocusLease'));
  const admission = upload.indexOf('admitDictationUploadAttempt(');
  const install = upload.indexOf('activeUploadAttemptRef.current = exactAttempt');
  const provider = upload.indexOf('api.transcribeDictation(');
  const exactGuard = upload.indexOf('activeUploadAttemptRef.current !== exactAttempt');
  const clearing = upload.indexOf('clearDictationUploadAttempt(');
  assert.ok(admission >= 0 && admission < install);
  assert.ok(install < provider, 'attempt lock is installed synchronously before the provider call');
  assert.ok(provider < exactGuard && exactGuard < clearing);
  assert.match(upload, /finally \{\s*activeUploadAttemptRef\.current = clearDictationUploadAttempt\(/);
});
