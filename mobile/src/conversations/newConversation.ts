export type NewConversationKind = 'private' | 'channel';

export type NewConversationAttempt = {
  kind: NewConversationKind;
  title: string;
  operationId: string;
};

let fallbackSequence = 0;

export function createConversationOperationId(): string {
  const randomUUID = globalThis.crypto?.randomUUID?.bind(globalThis.crypto);
  if (randomUUID) return `mobile-conversation-${randomUUID()}`;
  fallbackSequence += 1;
  return `mobile-conversation-${Date.now().toString(36)}-${fallbackSequence.toString(36)}-${Math.random().toString(36).slice(2, 12)}`;
}

export function normalizeConversationTitle(value: string): string {
  return value.replace(/\s+/g, ' ').trim();
}

export function newConversationAttempt(
  current: NewConversationAttempt | null,
  kind: NewConversationKind,
  titleValue: string,
  createOperationId: () => string = createConversationOperationId,
): NewConversationAttempt | null {
  const title = normalizeConversationTitle(titleValue);
  if (!title || title.length > 80) return null;
  if (current?.kind === kind && current.title === title) return current;
  return { kind, title, operationId: createOperationId() };
}

export function newConversationBody(attempt: NewConversationAttempt) {
  return {
    title: attempt.title,
    visibility: attempt.kind === 'channel' ? 'public' : 'private',
    operationId: attempt.operationId,
  } as const;
}
