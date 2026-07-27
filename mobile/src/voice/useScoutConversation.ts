import { useCallback, useRef, useState } from 'react';
import { api, BonfireApiError } from '../api/client';
import type { ScoutMessage } from '../api/types';
import { useAuth } from '../auth/AuthContext';

/**
 * The conversation loop — design §5.
 *
 * Tapping the Dock opens a loop: you speak, Scout answers on the canvas, and the
 * mic re-arms so you can just keep talking. That is what makes tap mean
 * *converse* rather than *dictate*, and it is true in Wave 1 even though the
 * transport is record→transcribe→answer rather than a live WebRTC line. Wave 2
 * swaps the transport underneath without changing what the gesture means.
 *
 * The thread is created lazily on the first turn and then reused, so a
 * conversation is one thread in company memory rather than a scatter of
 * one-message threads.
 */

export type ConversationTurn = {
  question: string;
  answer: string;
};

function scoutReply(messages: ScoutMessage[] | undefined): string {
  if (!messages?.length) return '';
  // The answer is the last non-user message the server appended for this turn.
  for (let index = messages.length - 1; index >= 0; index -= 1) {
    const message = messages[index];
    if (message.role === 'user') continue;
    const text = String(message.text ?? message.content ?? '').trim();
    if (text) return text;
  }
  return '';
}

export function useScoutConversation() {
  const { sessionToken } = useAuth();
  const [open, setOpen] = useState(false);
  const [thinking, setThinking] = useState(false);
  const [turn, setTurn] = useState<ConversationTurn | null>(null);
  const [error, setError] = useState<string | null>(null);
  const threadIdRef = useRef<string | null>(null);

  const start = useCallback(() => {
    setOpen(true);
    setError(null);
  }, []);

  const end = useCallback(() => {
    setOpen(false);
    setThinking(false);
    // The thread id is deliberately retained: reopening the loop continues the
    // same conversation rather than starting a fresh one mid-thought.
  }, []);

  /** Clears the conversation entirely — next turn starts a new thread. */
  const reset = useCallback(() => {
    threadIdRef.current = null;
    setTurn(null);
    setError(null);
    setOpen(false);
  }, []);

  const ask = useCallback(
    async (question: string) => {
      const text = question.trim();
      if (!text || !sessionToken) return;
      setThinking(true);
      setError(null);
      setTurn({ question: text, answer: '' });
      try {
        if (!threadIdRef.current) {
          const title = text.length > 54 ? `${text.slice(0, 51).trimEnd()}…` : text;
          const created = await api.createScoutThread(sessionToken, { title, visibility: 'private' });
          const id = String(created.thread?.id ?? '');
          if (!id) throw new Error('Scout did not open a thread.');
          threadIdRef.current = id;
        }
        const response = await api.sendScoutMessage(sessionToken, threadIdRef.current, text);
        const answer = scoutReply(response.thread?.messages ?? response.messages);
        setTurn({ question: text, answer });
      } catch (err) {
        setError(err instanceof BonfireApiError ? err.message : 'Scout could not answer that.');
        setTurn((previous) => (previous ? { ...previous, answer: '' } : null));
      } finally {
        setThinking(false);
      }
    },
    [sessionToken],
  );

  return {
    open,
    thinking,
    turn,
    error,
    threadId: threadIdRef.current,
    start,
    end,
    reset,
    ask,
  };
}
