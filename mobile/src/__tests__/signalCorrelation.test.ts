import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { correlatedOfferKey } from '../realtime/signalCorrelation';

describe('native signal correlation', () => {
  it('deduplicates only explicitly correlated offers', () => {
    assert.equal(
      correlatedOfferKey({ event: 'offer', offerId: 'session-offer-12', revision: 12 }),
      'session-offer-12:12',
    );
    assert.equal(correlatedOfferKey({ event: 'offer' }), '');
    assert.equal(correlatedOfferKey({ event: 'candidate', offerId: 'x', revision: 1 }), '');
  });
});
