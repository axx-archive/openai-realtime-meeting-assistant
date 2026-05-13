// AssistantPanel.jsx — assistant feed + ask form.
const { useEffect: useEffectAssistant, useRef: useRefAssistant, useState: useStateAssistant } = React;

function assistantLabel(entry) {
  const time = entry.time || "now";
  return `${entry.kind || "status"} · ${time}`;
}

function AssistantMessage({ entry, entering }) {
  const cls = `assistant-message assistant-message--${entry.kind || "status"}${entering ? " assistant-message--entering" : ""}`;
  return (
    <article className={cls}>
      <span className="assistant-meta">{assistantLabel(entry)}</span>
      <span className="assistant-text">{entry.text}</span>
      {entry.downloadUrl && (
        <a className="assistant-link" href={entry.downloadUrl} download>Download archive</a>
      )}
    </article>
  );
}

function AssistantPanel({ messages = [], onAsk, latestId, stateLabel = "ready" }) {
  const [query, setQuery] = useStateAssistant("");
  const feedRef = useRefAssistant(null);
  useEffectAssistant(() => {
    if (feedRef.current) feedRef.current.scrollTop = feedRef.current.scrollHeight;
  }, [messages.length]);

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
      <form
        className="assistant-form"
        onSubmit={(e) => {
          e.preventDefault();
          if (!query.trim()) return;
          onAsk?.(query.trim());
          setQuery("");
        }}
      >
        <input
          className="assistant-input"
          type="text"
          autoComplete="off"
          placeholder="Ask meeting memory"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
        />
        <button className="btn btn--primary assistant-send" type="submit">Ask</button>
      </form>
    </section>
  );
}

Object.assign(window, { AssistantMessage, AssistantPanel });
