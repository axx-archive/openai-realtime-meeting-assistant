import type { PersonalRealtimeStatus } from './personalRealtimeProtocol';

export type PersonalRealtimeIslandAction = 'start' | 'retry' | 'open_thread' | 'wait';

const labels: Record<PersonalRealtimeStatus, string> = {
  idle: 'Talk to Scout',
  connecting: 'Connecting',
  listening: 'Listening',
  hearing: 'Hearing you',
  thinking: 'Thinking',
  talking: 'Talking',
  acting: 'Working',
  error: 'Try again',
};

export function personalRealtimeIslandModel(input: {
  enabled: boolean;
  status: PersonalRealtimeStatus;
  threadId: string | null;
  canOpenThread: boolean;
  tearingDown?: boolean;
}): {
  label: string;
  action: PersonalRealtimeIslandAction;
  showClose: boolean;
} {
  if (input.tearingDown) {
    return { label: 'Stopping', action: 'wait', showClose: true };
  }
  let action: PersonalRealtimeIslandAction = 'wait';
  if (input.enabled && input.status === 'idle') action = 'start';
  else if (input.enabled && input.status === 'error') action = 'retry';
  else if (input.threadId && input.canOpenThread) action = 'open_thread';
  return {
    label: labels[input.status],
    action,
    showClose: input.status !== 'idle',
  };
}
