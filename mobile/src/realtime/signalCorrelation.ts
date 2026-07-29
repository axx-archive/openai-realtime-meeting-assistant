export type CorrelatedSignalEnvelope = {
  event?: string;
  offerId?: string;
  revision?: number;
};

export function correlatedOfferKey(envelope: CorrelatedSignalEnvelope): string {
  if (envelope.event !== 'offer') return '';
  const offerId = String(envelope.offerId ?? '').trim();
  const revision = Number(envelope.revision) || 0;
  if (!offerId && !revision) return '';
  return `${offerId}:${revision}`;
}
