import type { ScoutMessage, ScoutReplyState } from '../api/types';

export type ScoutReplyLifecyclePresentation = {
  state: Exclude<ScoutReplyState, 'completed'>;
  label: string;
  fallbackText: string;
  active: boolean;
  retryable: boolean;
};

export function scoutReplyLifecyclePresentation(
  message: ScoutMessage,
): ScoutReplyLifecyclePresentation | null {
  const reply = message.reply;
  if (!reply || reply.state === 'completed') return null;
  switch (reply.state) {
    case 'project_pending':
      return {
        state: 'project_pending',
        label: 'Linking project',
        fallbackText: '',
        active: true,
        retryable: false,
      };
    case 'queued':
      return {
        state: 'queued',
        label: 'Scout is queued',
        fallbackText: '',
        active: true,
        retryable: false,
      };
    case 'running':
      return {
        state: 'running',
        label: 'Scout is responding',
        fallbackText: '',
        active: true,
        retryable: false,
      };
    case 'failed':
      return {
        state: 'failed',
        label: "Scout couldn't answer yet",
        fallbackText: "Scout couldn't answer yet. Your message is safe.",
        active: false,
        retryable: reply.retryable === true,
      };
    case 'canceled':
      return {
        state: 'canceled',
        label: 'Scout response canceled',
        fallbackText: 'Scout response canceled.',
        active: false,
        retryable: false,
      };
  }
}
