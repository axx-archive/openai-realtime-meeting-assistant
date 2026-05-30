function assistantLabel(entry) {
  const time = entry.time || "now";
  return `${entry.kind || "status"} · ${time}`;
}

export function AssistantMessage({ entry, entering }) {
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
