/** Pure lifecycle vocabulary shared by the hook and token-free tests. */
export type DictationLifecycleState = 'idle' | 'listening' | 'held' | 'transcribing' | 'error';

export function canSendDictation(state: DictationLifecycleState, hasClip: boolean): boolean {
  return hasClip && (state === 'held' || state === 'error');
}

export function canDeleteDictation(state: DictationLifecycleState, hasClip: boolean): boolean {
  return hasClip && (state === 'held' || state === 'transcribing' || state === 'error');
}
