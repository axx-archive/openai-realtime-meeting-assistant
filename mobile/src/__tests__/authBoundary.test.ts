import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';
import { AudioFocusCoordinator } from '../voice/AudioFocusCoordinator';
import { SecureWriteSequencer } from '../auth/SecureWriteSequencer';
import { identityUpdateIsAuthorized } from '../auth/sessionAuthority';
import {
  readTextAfterUnauthorizedFence,
  setUnauthorizedHandler,
} from '../api/unauthorizedBoundary';
import {
  PushBindingCoordinator,
  type PushBindingAuthority,
  type PushBindingHandlers,
} from '../push/PushBindingCoordinator';
import { PushAuthorityLedger } from '../push/PushAuthorityLedger';

const mobileRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..', '..');
const source = (...parts: string[]) => fs.readFileSync(path.join(mobileRoot, ...parts), 'utf8');

async function waitFor(
  predicate: () => boolean,
  message: string,
  timeoutMs = 500,
): Promise<void> {
  const deadline = Date.now() + timeoutMs;
  while (!predicate()) {
    if (Date.now() >= deadline) throw new Error(message);
    await new Promise((resolve) => setTimeout(resolve, 2));
  }
}

test('auth clear fences every active or pending microphone owner synchronously', async () => {
  const focus = new AudioFocusCoordinator();
  let finishMeetingClose!: () => void;
  let meetingCloseStarted!: () => void;
  const closeStarted = new Promise<void>((resolve) => { meetingCloseStarted = resolve; });
  await focus.acquire('personal_realtime', {
    forceClose: () => new Promise<void>((resolve) => {
      finishMeetingClose = resolve;
      meetingCloseStarted();
    }),
  });

  const pendingDictation = focus.acquire('composer_dictation');
  await closeStarted;
  const authClear = focus.forceClose('forced_close');

  // forceClose's linearization point is before any native teardown await.
  assert.equal(focus.mode, 'idle');
  finishMeetingClose();
  const staleLease = await pendingDictation;
  await authClear;
  assert.equal(staleLease.isCurrent(), false);
  assert.equal(focus.mode, 'idle');
});

test('AuthContext clears local authority before I/O and bounds old-token logout', () => {
  const auth = source('src', 'auth', 'AuthContext.tsx');
  const clear = auth.slice(
    auth.indexOf('const beginLocalSessionClear = useCallback'),
    auth.indexOf('useEffect(() => {', auth.indexOf('const beginLocalSessionClear = useCallback')),
  );
  assert.ok(clear.indexOf("audioFocusRuntime.forceClose('forced_close')") < clear.indexOf('setUser(null)'));
  assert.ok(clear.indexOf("audioFocusRuntime.forceClose('forced_close')") < clear.indexOf('setSessionToken(null)'));
  assert.doesNotMatch(clear, /useCallback\(async/, 'the auth fence and React clear must not await I/O');
  assert.match(clear, /const generation = advanceAuthStorageGeneration\(\)/);
  assert.match(clear, /settleWithin\([\s\S]*LOCAL_AUTH_CLEANUP_TIMEOUT_MS/);

  const signOut = auth.slice(
    auth.indexOf('const signOut = useCallback'),
    auth.indexOf('const refreshMe = useCallback'),
  );
  assert.ok(signOut.indexOf('const capturedSessionToken') < signOut.indexOf('beginLocalSessionClear(capturedSessionToken)'));
  assert.ok(signOut.indexOf('beginLocalSessionClear(capturedSessionToken)') < signOut.indexOf('await settleWithin'));
  assert.match(signOut, /pushBindingCoordinator\.retire\(binding, retirementHandlers\)/);
  assert.match(signOut, /persistPushAuthorityLedger\(localClear\.generation\)/);
  assert.doesNotMatch(signOut, /writeAuthSecureForGeneration\(\s*PUSH_TOKEN_STORAGE_KEY/);
  assert.match(signOut, /settleWithin\([\s\S]*REMOTE_LOGOUT_TIMEOUT_MS/);
  assert.doesNotMatch(signOut, /registrationsDrained/);
  assert.ok(
    signOut.indexOf('beginLocalSessionClear(capturedSessionToken)')
      < signOut.indexOf('confirmSessionRevoked(capturedSessionToken)'),
    'local authority is fenced before the remote revocation request begins',
  );
  assert.ok(
    signOut.indexOf('confirmSessionRevoked(capturedSessionToken)')
      < signOut.indexOf('readSecureOutcome(PUSH_TOKEN_STORAGE_KEY)'),
    'session revocation must start before any device-token cleanup await',
  );
  assert.match(auth, /api\.logout\(authority\.sessionToken, null, true\)/);
  assert.match(auth, /sessionRevocationRetryTimers\.set\(token, setTimeout/);
  const signIn = auth.slice(
    auth.indexOf('const signIn = useCallback'),
    auth.indexOf('const signInWithPasskey = useCallback'),
  );
  assert.doesNotMatch(signIn, /confirmPendingPushSessionRevocations/);
});

test('a 401 fences exact authority before a suspended response body', async () => {
  const fenced: Array<string | null> = [];
  setUnauthorizedHandler((token) => fenced.push(token));
  let finishBody!: () => void;
  let bodyStarted = false;
  const body = new Promise<string>((resolve) => {
    finishBody = () => resolve('{"error":"expired"}');
  });
  const read = readTextAfterUnauthorizedFence({
    status: 401,
    text: () => {
      bodyStarted = true;
      return body;
    },
  }, 'token-a');
  assert.deepEqual(fenced, ['token-a']);
  assert.equal(bodyStarted, true);
  finishBody();
  await read;
  setUnauthorizedHandler(null);
});

test('late 401 and identity updates are scoped to exact session authority', () => {
  const auth = source('src', 'auth', 'AuthContext.tsx');
  const client = source('src', 'api', 'client.ts');
  assert.match(client, /readTextAfterUnauthorizedFence\([\s\S]*options\.sessionToken/);
  assert.match(client, /fenceUnauthorizedResponse\(response\.status, sessionToken\)/);
  assert.match(
    auth,
    /if \(!requestSessionToken \|\| currentSessionTokenRef\.current !== requestSessionToken\) return;/,
  );
  const unauthorizedHandler = auth.slice(
    auth.indexOf('setUnauthorizedHandler((requestSessionToken)'),
    auth.indexOf('return () => setUnauthorizedHandler(null)'),
  );
  assert.ok(
    unauthorizedHandler.indexOf('pushAuthorityLedger.markSessionPending')
      < unauthorizedHandler.indexOf('beginLocalSessionClear(requestSessionToken)'),
    'an ordinary 401 must capture push retirement authority before local auth is fenced',
  );
  assert.match(unauthorizedHandler, /pushBindingCoordinator\.retire\(binding, handlers\)/);
  assert.match(unauthorizedHandler, /persistPushAuthorityLedger\(localClear\.generation\)/);
  assert.match(
    auth,
    /identityUpdateIsAuthorized\([\s\S]*currentSessionTokenRef\.current,[\s\S]*expectedSessionToken/,
  );
  const passwordRotation = auth.slice(
    auth.indexOf('const changePassword = useCallback'),
    auth.indexOf('const value = useMemo'),
  );
  assert.ok(
    passwordRotation.indexOf('currentSessionTokenRef.current = nextToken')
      < passwordRotation.indexOf('await writeAuthSecureForGeneration'),
    'the rotated token must fence a late old-token 401 before storage I/O',
  );

  // The exact authority comparison is behavioral: A's delayed callback cannot
  // mutate the replacement B state, while B's own 401 can.
  let current: string | null = 'token-a';
  const clearFor401 = (failed: string | null) => {
    if (!failed || current !== failed) return;
    current = null;
  };
  current = 'token-b';
  clearFor401('token-a');
  assert.equal(current, 'token-b');
  clearFor401('token-b');
  assert.equal(current, null);

  assert.equal(identityUpdateIsAuthorized(
    'token-b',
    'token-a',
    'same@example.com',
    'same@example.com',
  ), false, 'A cannot overwrite a newer same-account B session');
  assert.equal(identityUpdateIsAuthorized(
    null,
    'token-a',
    'a@example.com',
    'a@example.com',
  ), false, 'A cannot resurrect identity after logout');
  assert.equal(identityUpdateIsAuthorized(
    'token-b',
    'token-b',
    'b@example.com',
    'b@example.com',
  ), true);
});

test('a never-settling A delete cannot block B and a late A delete is reconciled', async () => {
  let stored: string | null = 'token-a';
  let finishDelete!: () => void;
  let calls = 0;
  const sequencer = new SecureWriteSequencer(async (_key, value) => {
    calls += 1;
    if (calls === 1) {
      await new Promise<void>((resolve) => {
        finishDelete = () => {
          stored = value;
          resolve();
        };
      });
      return;
    }
    stored = value;
  }, 5);

  let generation = 1;
  const staleDelete = sequencer.write('session', null, () => generation === 1);
  await new Promise((resolve) => setTimeout(resolve, 0));
  generation = 2;
  const replacementWrite = sequencer.write('session', 'token-b', () => generation === 2);
  assert.equal(await replacementWrite, true);
  assert.equal(stored, 'token-b', 'hung A did not poison B admission');
  assert.equal(await staleDelete, false);

  finishDelete();
  await new Promise((resolve) => setTimeout(resolve, 0));
  assert.equal(stored, 'token-b', 'late A mutation was followed by the current B rewrite');
});

test('OfficeEvents context and callbacks are scoped to the exact identity and session', () => {
  const office = source('src', 'realtime', 'OfficeEventsContext.tsx');
  assert.match(office, /officeAuthScope\(sessionToken, user\?\.email\)/);
  assert.match(office, /currentAuthScopeRef\.current = authScope/);
  assert.match(office, /useLayoutEffect\(\(\) => \{/);
  assert.match(office, /currentAuthScopeRef\.current !== effectScope/);
  assert.match(office, /socketScopeRef\.current !== effectScope/);
  assert.match(office, /if \(!authScope \|\| state\.authScope !== authScope\)/);
  assert.match(office, /return emptyOfficeEventState\(null\)/);

  const send = office.slice(
    office.indexOf('const send = React.useCallback'),
    office.indexOf('const value = useMemo'),
  );
  assert.match(send, /socketScopeRef\.current !== currentScope/);
});

test('OfficeEvents is the fail-closed control plane for every personal Realtime path', () => {
  const office = source('src', 'realtime', 'OfficeEventsContext.tsx');
  const realtime = source('src', 'realtime', 'usePersonalRealtime.ts');
  const canvas = source('src', 'screens', 'CanvasScreen.tsx');
  assert.match(office, /markOfficeControlDisconnected\(sessionToken\)/);
  assert.match(office, /Date\.now\(\) - lastFrameAt > OFFICE_SILENCE_TIMEOUT_MS/);
  assert.match(office, /socketScopeRef\.current !== effectScope \|\| socketRef\.current !== socket/);
  assert.match(office, /closePersonalRealtimeForControlLoss\(\)/);
  assert.match(office, /audioFocusRuntime\.mode !== 'personal_realtime'/);
  assert.match(office, /markOfficeControlLive\(sessionToken\)/);
  assert.match(realtime, /if \(!officeControlChannelIsLive\(sessionToken\)\)/);
  const realtimeStart = realtime.slice(
    realtime.indexOf('const start = useCallback'),
    realtime.indexOf('const stop = useCallback'),
  );
  assert.ok(
    realtimeStart.indexOf('!officeControlChannelIsLive(sessionToken)')
      < realtimeStart.indexOf("audioFocusRuntime.acquire('personal_realtime'"),
  );
  assert.match(realtimeStart, /!lease\.isCurrent\(\)[\s\S]*!officeControlChannelIsLive\(sessionToken\)/);
  const toolAdmission = realtime.slice(
    realtime.indexOf('const handleToolCall = useCallback'),
    realtime.indexOf('const handleProviderEvent = useCallback'),
  );
  assert.match(toolAdmission, /leaseRef\.current !== lease/);
  assert.match(toolAdmission, /!officeControlChannelIsLive\(sessionToken\)/);
  assert.match(toolAdmission, /toolAbortController\.signal\.aborted/);
  assert.match(toolAdmission, /api\.realtimeTool\([\s\S]*toolAbortController\.signal/);
  assert.match(realtime, /toolAbortController\?\.abort\(\)/);
  assert.match(canvas, /usePersonalRealtime\(\{ onActions: handleRealtimeActions \}\)/);
  assert.doesNotMatch(canvas, /fallbackVoice|audioFocusRuntime\.acquire\('personal_realtime'/);
  assert.doesNotMatch(source('src', 'voice', 'useComposerDictation.ts'), /officeControlChannelIsLive/);
  assert.doesNotMatch(source('src', 'realtime', 'useNativeRoom.ts'), /officeControlChannelIsLive/);
});

test('push registration and pending navigation cannot cross an account boundary', () => {
  const push = source('src', 'push', 'usePushRegistration.ts');
  const coordinator = source('src', 'push', 'PushBindingCoordinator.ts');
  assert.match(push, /registrationGenerationRef\.current \+= 1/);
  assert.match(push, /currentSessionTokenRef\.current === registrationSessionToken/);
  assert.match(push, /currentAccountKeyRef\.current === registrationAccountKey/);
  assert.match(push, /await Notifications\.getPermissionsAsync\(\);\s*if \(!registrationIsCurrent\(\)\) return;/);
  assert.match(push, /await Notifications\.getExpoPushTokenAsync[\s\S]*if \(!registrationIsCurrent\(\)\) return;/);
  assert.match(push, /pushBindingCoordinator\.setDesired\(binding, pushBindingHandlers\)/);
  assert.ok(
    push.indexOf('await confirmPendingPushSessionRevocations()')
      < push.indexOf('pushBindingCoordinator.setDesired(binding, pushBindingHandlers)'),
    'B cannot mutate the shared push token before A session revocation is confirmed',
  );
  assert.match(push, /currentAccountKeyRef\.current === registrationAccountKey/);
  assert.match(push, /pushBindingCoordinator\.retire\(staleBinding, pushBindingHandlers\)/);
  assert.match(push, /pushAuthorityLedger\.remember\(/);
  assert.match(push, /pushAuthorityLedger\.trackRegistration\(/);
  assert.match(push, /PUSH_AUTHORITY_STORAGE_KEY/);
  assert.match(coordinator, /One process-wide mutation lane per Expo token/);
  assert.match(coordinator, /lane\.desiredEstablished = false/);
  assert.match(coordinator, /lane\.retired\.set\(lateID, entry\)/);
  assert.match(coordinator, /this\.scheduleRetry\(token, lane\)/);
  const hookUnregister = push.slice(
    push.indexOf('unregister: async (authority)'),
    push.indexOf('onRegistered: async (authority)'),
  );
  assert.doesNotMatch(hookUnregister, /status === 401/);
  const auth = source('src', 'auth', 'AuthContext.tsx');
  const authUnregister = auth.slice(
    auth.indexOf('unregister: async (authority)'),
    auth.indexOf('onRegistered: async (authority)'),
  );
  assert.doesNotMatch(authUnregister, /status === 401/);
  assert.match(push, /const response = await api\.notifications\(validationSessionToken\)/);
  assert.match(push, /resolveAuthorizedPushTarget\(/);
  assert.match(push, /allowBadge: true/);
  assert.match(push, /existing\.ios\?\.allowsBadge === true/);
  assert.match(push, /syncNotificationBadge\(sessionToken\)/);
  assert.match(push, /AppState\.addEventListener\('change'/);
  assert.match(push, /generation !== badgeSyncGeneration/);
  assert.match(push, /pendingColdCandidateRef\.current = bootstrappingRef\.current \? candidate : null/);

  const navigator = source('src', 'navigation', 'RootNavigator.tsx');
  assert.match(navigator, /if \(!accountKey \|\| target\.accountKey !== accountKey\)/);
  assert.match(navigator, /pending\.target\.accountKey !== accountKey/);
  assert.match(navigator, /bootstrapping,\s*onOpenTarget: openPushTarget/);
  assert.match(navigator, /\{user && sessionToken \? \(/);
});

test('late A registration and failed A delete still finish with B as last writer', async () => {
  const coordinator = new PushBindingCoordinator(8, 8);
  const authorityA: PushBindingAuthority = {
    accountKey: 'a@example.com',
    sessionToken: 'session-a',
    token: 'expo-shared',
  };
  const authorityB: PushBindingAuthority = {
    accountKey: 'b@example.com',
    sessionToken: 'session-b',
    token: 'expo-shared',
  };
  let serverBinding: string | null = null;
  let releaseFirstA: (() => void) | null = null;
  let aRegistrations = 0;
  let bRegistrations = 0;
  let failedDeletes = 0;
  const handlers: PushBindingHandlers = {
    register: async (authority) => {
      if (authority.accountKey === authorityA.accountKey) {
        aRegistrations += 1;
        if (aRegistrations === 1) {
          await new Promise<void>((resolve) => {
            releaseFirstA = () => {
              serverBinding = authority.accountKey;
              resolve();
            };
          });
          return;
        }
      } else {
        bRegistrations += 1;
      }
      serverBinding = authority.accountKey;
    },
    unregister: async (authority) => {
      if (authority.accountKey === authorityA.accountKey) {
        failedDeletes += 1;
        throw new Error('offline DELETE');
      }
      if (serverBinding === authority.accountKey) serverBinding = null;
    },
  };

  void coordinator.setDesired(authorityA, handlers);
  await waitFor(() => releaseFirstA !== null, 'A registration did not start');
  void coordinator.setDesired(authorityB, handlers);
  await waitFor(
    () => serverBinding === authorityB.accountKey && bRegistrations > 0,
    'B was blocked behind A or the failed A delete',
  );
  const bWritesBeforeLateA = bRegistrations;
  assert.ok(releaseFirstA);
  (releaseFirstA as () => void)();
  await waitFor(
    () => serverBinding === authorityB.accountKey && bRegistrations > bWritesBeforeLateA,
    'B was not reasserted after A committed late',
  );
  assert.ok(failedDeletes > 0, 'the test must exercise the failed compensating DELETE');
  assert.equal(serverBinding, authorityB.accountKey);
  coordinator.dispose();
});

test('sign-out retirement survives a drain timeout and cleans a later A commit', async () => {
  const coordinator = new PushBindingCoordinator(8, 8);
  const authorityA: PushBindingAuthority = {
    accountKey: 'a@example.com',
    sessionToken: 'session-a',
    token: 'expo-shared',
  };
  let serverBinding: string | null = null;
  let releaseA: (() => void) | null = null;
  let deleteAttempts = 0;
  let retired = 0;
  const handlers: PushBindingHandlers = {
    register: async (authority) => {
      await new Promise<void>((resolve) => {
        releaseA = () => {
          serverBinding = authority.accountKey;
          resolve();
        };
      });
    },
    unregister: async (authority) => {
      deleteAttempts += 1;
      if (deleteAttempts === 1) throw new Error('first cleanup attempt offline');
      if (serverBinding === authority.accountKey) serverBinding = null;
    },
    onRetired: () => {
      retired += 1;
    },
  };

  void coordinator.setDesired(authorityA, handlers);
  await waitFor(() => releaseA !== null, 'A registration did not start');
  const retirement = coordinator.retire(authorityA, handlers);
  await waitFor(
    () => deleteAttempts >= 2,
    'cleanup was abandoned while the registration remained unresolved',
  );
  assert.equal(
    retired,
    0,
    'the captured session must remain usable until the uncertain writer settles',
  );

  // The original POST commits after the bounded drain/ownership window. Its
  // observed late settlement must enqueue a final delete, and only then may
  // the captured session be logged out and the retirement waiter resolve.
  const deletesBeforeLateCommit = deleteAttempts;
  assert.ok(releaseA);
  (releaseA as () => void)();
  assert.equal(await retirement, true, 'late A cleanup/logout never completed');
  await waitFor(
    () => deleteAttempts > deletesBeforeLateCommit && serverBinding === null,
    'late server-committed A registration was not cleaned in-process',
  );
  assert.equal(retired, 1);
  coordinator.dispose();
});

test('offline push cleanup retains A authority and a successful B upsert supersedes it', async () => {
  const ledger = new PushAuthorityLedger();
  const authorityA = ledger.remember('A@example.com', 'session-a', 'expo-1');
  assert.ok(authorityA);
  let finishRegistration!: () => void;
  const registration = new Promise<void>((resolve) => { finishRegistration = resolve; });
  ledger.trackRegistration(authorityA, registration);
  const pendingA = ledger.markSessionPending('a@example.com', 'session-a');
  assert.equal(pendingA.length, 1);
  assert.equal(ledger.registrationsForSession('a@example.com', 'session-a').length, 1);

  // An offline/timeout path does not remove A or forget the old credential.
  const restored = new PushAuthorityLedger();
  restored.hydrate(ledger.serialize());
  assert.deepEqual(restored.pending(), [{
    accountKey: 'a@example.com',
    sessionToken: 'session-a',
    token: 'expo-1',
    pendingRevocation: true,
    deviceCleared: false,
  }]);

  // Expo token upserts are server-keyed. Once B's exact upsert succeeds, the
  // durable ledger can safely retire A for that token.
  const authorityB = restored.remember('b@example.com', 'session-b', 'expo-1');
  assert.ok(authorityB);
  const superseded = restored.registrationSucceeded(authorityB);
  assert.equal(superseded[0]?.deviceCleared, true);
  assert.deepEqual(restored.snapshot().sort((left, right) => (
    left.accountKey.localeCompare(right.accountKey)
  )), [{
    accountKey: 'a@example.com',
    sessionToken: 'session-a',
    token: 'expo-1',
    pendingRevocation: true,
    deviceCleared: true,
  }, {
    accountKey: 'b@example.com',
    sessionToken: 'session-b',
    token: 'expo-1',
    pendingRevocation: false,
    deviceCleared: false,
  }]);
  restored.removeSession('a@example.com', 'session-a');
  assert.equal(restored.snapshot().length, 1);
  finishRegistration();
  await registration;
});

test('a timed-out token read can retain unknown cleanup authority durably', () => {
  const ledger = new PushAuthorityLedger();
  ledger.remember('a@example.com', 'session-a', null, true);
  const restored = new PushAuthorityLedger();
  restored.hydrate(ledger.serialize());
  assert.equal(restored.pending()[0]?.sessionToken, 'session-a');
  assert.equal(restored.pending()[0]?.token, null);
  assert.ok(
    restored.attachTokenToPendingSession('a@example.com', 'session-a', 'expo-late'),
  );
  assert.equal(restored.pending()[0]?.token, 'expo-late');
});
