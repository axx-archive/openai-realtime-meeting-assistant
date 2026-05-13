// MemoryPanel.jsx — list of meeting memory items.
function memoryLabel(entry) {
  const time = entry.time || "now";
  if (entry.kind === "answer") return `answer · ${time}`;
  if (entry.kind === "archive") return `archive · ${time}`;
  return `transcript · ${time}`;
}

function MemoryPanel({ entries = [] }) {
  return (
    <section className="memory-panel mount-stagger" aria-label="Meeting memory">
      <header>
        <h2>meeting memory</h2>
        <span className="memory-count">{entries.length} saved</span>
      </header>
      <div className="memory-list">
        {entries.length === 0 ? (
          <article className="memory-item">
            <span className="memory-meta">ready</span>
            <span>memory starts when the room speaks.</span>
          </article>
        ) : (
          [...entries].reverse().map((e) => (
            <article key={e.id} className={`memory-item ${e.kind === "answer" || e.kind === "archive" ? e.kind : ""}`.trim()}>
              <span className="memory-meta">{memoryLabel(e)}</span>
              <span>{e.text}</span>
            </article>
          ))
        )}
      </div>
    </section>
  );
}

Object.assign(window, { MemoryPanel });
