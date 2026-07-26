import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import {
  ConsentContractError,
  buildConsentDecision,
  parseConsentDecisionResponse,
  parseConsentStatus,
} from '../api/consent';
import { consentDispositions, consentScopes } from '../api/types';

const status = {
  policyVersion: 'consent-v1',
  principalKind: 'user',
  roomId: 'office',
  sittingId: 'meeting-123',
  guestPolicyListenOnly: false,
  storeAvailable: true,
  lanes: {
    audio_transport: { allowed: true, recordIds: {} },
    audio_capture: { allowed: true, recordIds: { audio_capture: 'record-audio' } },
    transcription: {
      allowed: false,
      missingScopes: ['transcription'],
      recordIds: { audio_capture: 'record-audio' },
    },
    model_analysis: {
      allowed: false,
      missingScopes: ['transcription', 'model_analysis'],
    },
    org_memory: {
      allowed: false,
      missingScopes: ['transcription', 'model_analysis', 'org_memory'],
    },
  },
  scopes: { audio_capture: 'granted' },
};

describe('native consent API contract', () => {
  it('pins the exact server-owned scope and disposition vocabularies', () => {
    assert.deepEqual(consentScopes, [
      'audio_capture',
      'transcription',
      'model_analysis',
      'org_memory',
    ]);
    assert.deepEqual(consentDispositions, ['granted', 'denied', 'withdrawn']);
    assert.deepEqual(buildConsentDecision('org_memory', 'withdrawn'), {
      scope: 'org_memory',
      disposition: 'withdrawn',
    });
  });

  it('parses a complete status while allowing scopes with no decision yet', () => {
    const parsed = parseConsentStatus(status);
    assert.equal(parsed.lanes.audio_capture.allowed, true);
    assert.deepEqual(parsed.lanes.transcription.missingScopes, ['transcription']);
    assert.equal(parsed.scopes.audio_capture, 'granted');
    assert.equal(parsed.scopes.transcription, undefined);
  });

  it('parses a persisted decision and its refreshed authority snapshot', () => {
    const parsed = parseConsentDecisionResponse({
      recordId: 'record-org-memory',
      recordedAt: '2026-07-26T02:00:00Z',
      lastAcceptedCaptureSequence: null,
      consent: status,
    });
    assert.equal(parsed.recordId, 'record-org-memory');
    assert.equal(parsed.lastAcceptedCaptureSequence, null);
    assert.equal(parsed.consent.roomId, 'office');
  });

  it('fails closed on unknown scopes, dispositions, or incomplete lanes', () => {
    assert.throws(
      () => buildConsentDecision('audio_transport' as never, 'granted'),
      ConsentContractError,
    );
    assert.throws(
      () => parseConsentStatus({ ...status, scopes: { audio_capture: 'allowed' } }),
      ConsentContractError,
    );
    const lanes = { ...status.lanes } as Record<string, unknown>;
    delete lanes.org_memory;
    assert.throws(() => parseConsentStatus({ ...status, lanes }), ConsentContractError);
  });

  it('rejects malformed lane fields and capture fences', () => {
    assert.throws(
      () => parseConsentStatus({
        ...status,
        lanes: { ...status.lanes, audio_capture: { allowed: 'yes' } },
      }),
      ConsentContractError,
    );
    assert.throws(
      () => parseConsentDecisionResponse({
        recordId: 'record-audio',
        recordedAt: '2026-07-26T02:00:00Z',
        lastAcceptedCaptureSequence: -1,
        consent: status,
      }),
      ConsentContractError,
    );
  });
});
