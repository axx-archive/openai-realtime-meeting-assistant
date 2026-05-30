// AssistantPanel.jsx — assistant feed + ask form.
import { useEffect, useRef, useState } from "react";

import { AssistantMessage } from "./AssistantMessage.jsx";

const EMPTY_MESSAGES = Object.freeze([]);

export function AssistantPanel({ messages = EMPTY_MESSAGES, onAsk, latestId, stateLabel = "ready" }) {
  const [query, setQuery] = useState("");
  const feedRef = useRef(null);

  useEffect(() => {
    if (feedRef.current) feedRef.current.scrollTop = feedRef.current.scrollHeight;
  }, [messages.length]);

  const ask = () => {
    const nextQuery = query.trim();
    if (!nextQuery) return;
    onAsk?.(nextQuery);
    setQuery("");
  };

  return (
    <section className="assistant-panel mount-stagger" aria-label="Assistant responses">
      <header>
        <h2>assistant</h2>
        <span className="assistant-state">{stateLabel}</span>
      </header>
      <div ref={feedRef} className="assistant-feed">
        {messages.length === 0 ? (
          <article className="assistant-message assistant-message--status">
            <span className="assistant-meta">ready</span>
            <span className="assistant-text">waiting for room audio.</span>
          </article>
        ) : (
          messages.map((m) => <AssistantMessage key={m.id} entry={m} entering={m.id === latestId} />)
        )}
      </div>
      <div
        className="assistant-form"
      >
        <input
          className="assistant-input"
          type="text"
          aria-label="Ask meeting memory"
          autoComplete="off"
          placeholder="Ask meeting memory"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") ask();
          }}
        />
        <button className="btn btn--primary assistant-send" type="button" onClick={ask}>Ask</button>
      </div>
    </section>
  );
}
