type MainThreadProjectPreflight = {
  sessionToken: string | null;
  editingMessage: boolean;
  chooserOpen: boolean;
  hasSelectedProject: boolean;
};

type ReplyThreadProjectPreflight = {
  visible: boolean;
  sessionToken: string | null;
  threadId: string;
  rootMessageId: string;
  chooserOpen: boolean;
  hasSelectedProject: boolean;
};

// Project association is server-owned context, not a composer choice. Keep the
// retired preflight helpers available while older release responses and exact
// retry payloads age out, but do not let any mobile surface request or render
// the manual "Add project" accessory.
export const explicitProjectAttachmentEnabled = false;

export type SafeProjectContext = {
  available: boolean;
  scopeKey: string;
  choices: Array<{ title: string; token: string; choiceKey?: string; suggested?: boolean }>;
  suggested?: { title: string; token: string; choiceKey?: string; suggested?: boolean };
};

function safeProjectChoice(value: unknown): SafeProjectContext['choices'][number] | null {
  if (!value || typeof value !== 'object') return null;
  const candidate = value as Record<string, unknown>;
  if (typeof candidate.title !== 'string' || typeof candidate.token !== 'string') return null;
  return {
    title: candidate.title,
    token: candidate.token,
    ...(typeof candidate.choiceKey === 'string' ? { choiceKey: candidate.choiceKey } : {}),
    ...(typeof candidate.suggested === 'boolean' ? { suggested: candidate.suggested } : {}),
  };
}

/**
 * API response types are compile-time only. A skewed or partially activated
 * server can still return a 200 envelope without Project context; treat that
 * as unavailable instead of letting a render-time property read terminate the
 * Release app through RCTExceptionsManager.
 */
export function safeProjectContextFromResponse(value: unknown): SafeProjectContext | null {
  if (!value || typeof value !== 'object') return null;
  const raw = (value as Record<string, unknown>).projectContext;
  if (!raw || typeof raw !== 'object') return null;
  const candidate = raw as Record<string, unknown>;
  if (typeof candidate.available !== 'boolean') return null;
  const choices = Array.isArray(candidate.choices)
    ? candidate.choices.map(safeProjectChoice).filter((choice): choice is NonNullable<typeof choice> => Boolean(choice))
    : [];
  const suggested = safeProjectChoice(candidate.suggested);
  return {
    available: candidate.available,
    scopeKey: typeof candidate.scopeKey === 'string' ? candidate.scopeKey : '',
    choices,
    ...(suggested ? { suggested } : {}),
  };
}

/**
 * Manual Project selection is retired. Merely mounting an ordinary Chat or
 * private Scout thread must never start the old preflight request/render cycle.
 * Project/workstream association is inferred and governed on the server.
 */
export function shouldRequestMainThreadProjectContext(input: MainThreadProjectPreflight): boolean {
  return Boolean(
    explicitProjectAttachmentEnabled
    && input.sessionToken
    && !input.editingMessage
    && (input.chooserOpen || input.hasSelectedProject),
  );
}

/** Reply sheets retain the same fail-closed retirement boundary. */
export function shouldRequestReplyThreadProjectContext(input: ReplyThreadProjectPreflight): boolean {
  return Boolean(
    explicitProjectAttachmentEnabled
    && input.visible
    && input.sessionToken
    && input.threadId
    && input.rootMessageId
    && (input.chooserOpen || input.hasSelectedProject),
  );
}
