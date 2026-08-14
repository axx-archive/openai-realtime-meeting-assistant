export type RoomVoiceAgentAvailabilityInput = {
  inNativeRoom: boolean;
  currentScopeKey: string;
  resolvedScopeKey: string;
  scoutVoiceEnabled: boolean;
  specialistVoiceAvailable: boolean;
  controlAgentCount: number;
  liveAgentCount: number;
};

// Voice-agent controls are a qualified capability, not generic meeting
// chrome. An already-present agent remains manageable even if qualification
// changes, but stale results from another account or room never reopen it.
export function roomVoiceAgentControlsAvailable(input: RoomVoiceAgentAvailabilityInput): boolean {
  if (!input.inNativeRoom) return false;
  if (input.liveAgentCount > 0) return true;
  if (!input.currentScopeKey || input.resolvedScopeKey !== input.currentScopeKey) return false;
  return Boolean(
    input.scoutVoiceEnabled
    || input.specialistVoiceAvailable
    || input.controlAgentCount > 0
  );
}
